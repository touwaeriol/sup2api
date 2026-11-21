package group

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// one runs a statement that returns an id.
func (e *env) one(sql string, args ...any) int64 {
	e.t.Helper()
	var id int64
	require.NoError(e.t, e.db.SQL.QueryRowContext(context.Background(), sql, args...).Scan(&id))
	return id
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	_, err := e.db.SQL.ExecContext(context.Background(), sql, args...)
	require.NoError(e.t, err)
}

// account adds an account declaring models (none = it serves every model) to
// the groups.
func (e *env) account(models []string, groups ...int64) {
	e.t.Helper()
	id := e.one(`INSERT INTO accounts (name, plugin_key, type, credentials_enc, models)
		VALUES ('a', 'anthropic', 'apikey', '\x00'::bytea, $1) RETURNING id`, models)
	for _, g := range groups {
		e.exec(`INSERT INTO account_groups (account_id, group_id) VALUES ($1, $2)`, id, g)
	}
}

func (e *env) price(model string, enabled bool) {
	e.t.Helper()
	e.exec(`INSERT INTO model_prices (model, mode, expression, expr_hash, source, enabled) VALUES ($1, 'expression', '0', 'h', 'manual', $2)`, model, enabled)
}

// listed returns the rows of a groups list by name, and its total.
func (e *env) listed(uid int64, path string) (map[string]map[string]any, float64) {
	e.t.Helper()
	code, out := e.do(uid, "GET", path, nil)
	require.Equal(e.t, 200, code, "%s: %v", path, out)
	rows := map[string]map[string]any{}
	for _, it := range out["data"].([]any) {
		g := it.(map[string]any)
		rows[g["name"].(string)] = g
	}
	total, _ := out["page"].(map[string]any)["total"].(float64)
	return rows, total
}

func TestGroupPricingSummary(t *testing.T) {
	e := setup(t)
	open := e.one(`INSERT INTO groups (name) VALUES ('open') RETURNING id`)
	private := e.one(`INSERT INTO groups (name, visibility) VALUES ('private', 'restricted') RETURNING id`)
	vip := e.one(`INSERT INTO segments (name) VALUES ('vip') RETURNING id`)
	team := e.one(`INSERT INTO segments (name) VALUES ('team') RETURNING id`)
	personal := e.one(`INSERT INTO segments (kind, user_id) VALUES ('personal', $1) RETURNING id`, e.user)
	e.exec(`INSERT INTO segment_groups (segment_id, group_id, granted, rate_override) VALUES
		($1, $4, true, NULL), ($2, $4, false, 0.5), ($3, $4, true, NULL), ($1, $5, true, NULL)`, vip, team, personal, private, open)
	e.exec(`INSERT INTO price_rules (group_id, model, kind, multiplier) VALUES ($1, 'm1', 'multiplier', 1.2)`, private)
	e.account([]string{"m1", "m2"}, private)

	rows, total := e.listed(e.admin, "/groups")
	require.EqualValues(t, 2, total)
	require.Equal(t, map[string]any{
		"segments":       []any{map[string]any{"id": float64(vip), "kind": core.SegmentNamed, "name": "vip"}},
		"personal_count": float64(1),
	}, rows["private"]["access"], "the named segments granted, and the personal ones counted")
	require.EqualValues(t, 1, rows["private"]["price_rule_count"])
	require.NotContains(t, rows["private"], "segment_rule_count")
	require.EqualValues(t, 2, rows["private"]["model_count"])

	require.Equal(t, map[string]any{"segments": []any{}, "personal_count": float64(0)}, rows["open"]["access"],
		"a public group lists nobody, whatever was granted")
	require.EqualValues(t, 0, rows["open"]["price_rule_count"])
	require.EqualValues(t, 0, rows["open"]["model_count"])

	code, out := e.do(e.admin, "GET", fmt.Sprintf("/groups/%d", private), nil)
	require.Equal(t, 200, code)
	detail := out["data"].(map[string]any)
	require.Equal(t, []any{}, detail["served_protocols"], "only the detail lists the protocols")
	delete(detail, "served_protocols")
	require.Equal(t, rows["private"], detail, "one group otherwise reads as its row in the list")

	// Grants, rates and price rules go with the group.
	code, _ = e.do(e.admin, "DELETE", fmt.Sprintf("/groups/%d", private), nil)
	require.Equal(t, 204, code)
	require.Zero(t, e.one(`SELECT (SELECT count(*) FROM segment_groups WHERE group_id = $1) + (SELECT count(*) FROM price_rules WHERE group_id = $1)`, private))
}

// Price rules count as the group's pricing table lists them: on any model,
// one the group no longer provides (or never did) included.
func TestRuleCountsOfAnyModel(t *testing.T) {
	e := setup(t)
	declared := e.one(`INSERT INTO groups (name, visibility) VALUES ('declared', 'restricted') RETURNING id`)
	open := e.one(`INSERT INTO groups (name) VALUES ('open') RETURNING id`)
	e.account([]string{"m1"}, declared)
	e.account([]string{}, open) // serves every model that has a global price
	e.price("priced", true)
	e.exec(`INSERT INTO price_rules (group_id, model, kind, multiplier) VALUES
		($1, 'm1', 'multiplier', 1), ($1, 'priced', 'multiplier', 1), ($1, 'gone', 'multiplier', 1), ($2, 'gone', 'multiplier', 1)`, declared, open)
	counts := func() map[string][]any {
		t.Helper()
		rows, _ := e.listed(e.admin, "/groups")
		out := map[string][]any{}
		for name, row := range rows {
			out[name] = []any{row["price_rule_count"], row["model_count"]}
		}
		return out
	}

	require.Equal(t, map[string][]any{"declared": {float64(3), float64(1)}, "open": {float64(1), float64(1)}}, counts())
	// Once the price is disabled, open provides nothing; its rule still counts.
	e.exec(`UPDATE model_prices SET enabled = false WHERE model = 'priced'`)
	require.Equal(t, map[string][]any{"declared": {float64(3), float64(1)}, "open": {float64(1), float64(0)}}, counts())
}

func TestGroupModels(t *testing.T) {
	e := setup(t)
	group := func(name, mode string, patterns ...string) int64 {
		return e.one(`INSERT INTO groups (name, model_filter_mode, model_allowlist) VALUES ($1, $2, to_jsonb($3::text[])) RETURNING id`,
			name, mode, append([]string{}, patterns...))
	}
	declared := group("declared", "blacklist")
	unrestricted := group("unrestricted", "blacklist")
	denied := group("denied", "blacklist", "m1")
	narrowed := group("narrowed", "whitelist", "p*")
	group("empty", "blacklist")
	e.account([]string{"m1"}, declared, denied)
	e.account([]string{}, unrestricted, narrowed)
	e.account([]string{"m1"}, narrowed)
	e.price("p1", true)
	e.price("p2", true)
	e.price("off", false)

	rows, total := e.listed(e.admin, "/groups")
	require.EqualValues(t, 5, total)
	for name, want := range map[string]int{
		"declared":     1, // m1
		"unrestricted": 2, // every priced model
		"denied":       0, // m1, which the policy refuses
		"narrowed":     2, // p1 and p2; m1 is outside the whitelist
		"empty":        0,
	} {
		require.EqualValues(t, want, rows[name]["model_count"], name)
	}

	for _, tc := range []struct {
		model string
		want  []string
	}{
		{"m1", []string{"declared"}},
		{"p1", []string{"unrestricted", "narrowed"}},
		{"off", nil}, // no enabled price: an account that serves every model does not provide it
		{"unknown", nil},
	} {
		rows, total := e.listed(e.admin, "/groups?model="+tc.model)
		require.EqualValues(t, len(tc.want), total, tc.model)
		require.Len(t, rows, len(tc.want), tc.model)
		for _, name := range tc.want {
			require.Contains(t, rows, name, tc.model)
		}
	}

	// The filter applies before paging.
	first, total := e.listed(e.admin, "/groups?model=p1&page_size=1")
	require.EqualValues(t, 2, total)
	require.Contains(t, first, "unrestricted")
	require.Len(t, first, 1)
	second, total := e.listed(e.admin, "/groups?model=p1&page_size=1&page=2")
	require.EqualValues(t, 2, total)
	require.Contains(t, second, "narrowed")
	require.Len(t, second, 1)
	rows, total = e.listed(e.admin, "/groups?model=p1&q=narrow")
	require.EqualValues(t, 1, total)
	require.Contains(t, rows, "narrowed")
}

func TestMyGroupsRates(t *testing.T) {
	e := setup(t)
	vip := e.one(`INSERT INTO segments (name, discount) VALUES ('vip', 0.8) RETURNING id`)
	e.exec(`UPDATE users SET segment_id = $2 WHERE id = $1`, e.user, vip)
	e.one(`INSERT INTO groups (name, rate_multiplier) VALUES ('open', 1.5) RETURNING id`)
	granted := e.one(`INSERT INTO groups (name, visibility, rate_multiplier) VALUES ('granted', 'restricted', 2) RETURNING id`)
	e.one(`INSERT INTO groups (name, visibility) VALUES ('closed', 'restricted') RETURNING id`)
	off := e.one(`INSERT INTO groups (name, visibility, status) VALUES ('off', 'restricted', 'disabled') RETURNING id`)
	e.exec(`INSERT INTO segment_groups (segment_id, group_id, granted, rate_override) VALUES ($1, $2, true, 0.5), ($1, $3, true, NULL)`, vip, granted, off)

	type row struct {
		rate, source, groupRate string
		exclusive               bool
	}
	for _, tc := range []struct {
		name string
		uid  int64
		want map[string]row
	}{
		{"a segment with a discount and an override", e.user, map[string]row{
			"open":    {"1.2", core.RateSourceDiscount, "1.5", false},
			"granted": {"0.5", core.RateSourceSegment, "2", true},
		}},
		{"the default segment", e.admin, map[string]row{
			"open": {"1.5", core.RateSourceGroup, "1.5", false},
		}},
	} {
		code, out := e.do(tc.uid, "GET", "/me/groups", nil)
		require.Equal(t, 200, code, tc.name)
		got := map[string]row{}
		for _, it := range out["data"].([]any) {
			g := it.(map[string]any)
			got[g["name"].(string)] = row{g["effective_rate"].(string), g["rate_source"].(string), g["rate_multiplier"].(string), g["exclusive"].(bool)}
		}
		require.Equal(t, tc.want, got, tc.name)
	}
}
