package group

import (
	"context"
	"fmt"
	"slices"
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

// fakePriced is core.PricedModels over the prices the tests set: billing
// owns the rule and is tested there.
type fakePriced struct {
	global []string           // enabled global prices
	own    map[int64][]string // the groups' own prices
}

func (f *fakePriced) PricedModels(_ context.Context, ids []int64, model string) (map[int64][]string, error) {
	out := map[int64][]string{}
	for _, id := range ids {
		models := slices.Concat(f.global, f.own[id])
		if model != "" {
			models = slices.DeleteFunc(models, func(m string) bool { return m != model })
		}
		slices.Sort(models)
		out[id] = slices.Compact(models)
	}
	return out, nil
}

// rule gives the group a model price of its own on the model.
func (e *env) rule(group int64, model string) {
	e.t.Helper()
	e.exec(`INSERT INTO price_rules (group_id, model, mode, config, expression) VALUES ($1, $2, 'expression', '{}', '0')`, group, model)
	if e.priced.own == nil {
		e.priced.own = map[int64][]string{}
	}
	e.priced.own[group] = append(e.priced.own[group], model)
}

// price gives the models an enabled global price.
func (e *env) price(models ...string) { e.priced.global = append(e.priced.global, models...) }

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
	e.rule(private, "m1")
	e.exec(`INSERT INTO group_model_rates (group_id, model, rate) VALUES ($1, 'm1', 1.2), ($1, 'm3', 0.5)`, private)
	e.account([]string{"m1", "m2"}, private)

	rows, total := e.listed(e.admin, "/groups")
	require.EqualValues(t, 2, total)
	require.Equal(t, map[string]any{
		"segments":       []any{map[string]any{"id": float64(vip), "kind": core.SegmentNamed, "name": "vip"}},
		"personal_count": float64(1),
	}, rows["private"]["access"], "the named segments granted, and the personal ones counted")
	require.EqualValues(t, 2, rows["private"]["price_rule_count"], "the models with a price or a rate of their own, each once")
	require.NotContains(t, rows["private"], "segment_rule_count")
	require.EqualValues(t, 2, rows["private"]["model_count"])

	require.Equal(t, map[string]any{"segments": []any{}, "personal_count": float64(0)}, rows["open"]["access"],
		"a public group lists nobody, whatever was granted")
	require.EqualValues(t, 0, rows["open"]["price_rule_count"])
	require.EqualValues(t, 0, rows["open"]["model_count"])
	requireDetailIsRow(t, e, private, rows["private"])
}

// requireDetailIsRow requires GET /groups/:id to read as the group's row in
// the list, but for the protocols only the detail lists.
func requireDetailIsRow(t *testing.T, e *env, id int64, row map[string]any) {
	t.Helper()
	code, out := e.do(e.admin, "GET", fmt.Sprintf("/groups/%d", id), nil)
	require.Equal(t, 200, code)
	detail := out["data"].(map[string]any)
	require.Equal(t, []any{}, detail["served_protocols"], "only the detail lists the protocols")
	delete(detail, "served_protocols")
	require.Equal(t, row, detail, "one group otherwise reads as its row in the list")
}

// Grants, prices and rates go with the group.
func TestGroupDeleteTakesPricing(t *testing.T) {
	e := setup(t)
	g := e.one(`INSERT INTO groups (name) VALUES ('g') RETURNING id`)
	vip := e.one(`INSERT INTO segments (name) VALUES ('vip') RETURNING id`)
	e.exec(`INSERT INTO segment_groups (segment_id, group_id, granted) VALUES ($1, $2, true)`, vip, g)
	e.exec(`INSERT INTO segment_model_rates (segment_id, group_id, model, rate) VALUES ($1, $2, 'm1', 0.5)`, vip, g)
	e.exec(`INSERT INTO group_model_rates (group_id, model, rate) VALUES ($1, 'm1', 1.2)`, g)
	e.rule(g, "m1")
	code, _ := e.do(e.admin, "DELETE", fmt.Sprintf("/groups/%d", g), nil)
	require.Equal(t, 204, code)
	require.Zero(t, e.one(`SELECT (SELECT count(*) FROM segment_groups WHERE group_id = $1) + (SELECT count(*) FROM price_rules WHERE group_id = $1)
		+ (SELECT count(*) FROM group_model_rates WHERE group_id = $1) + (SELECT count(*) FROM segment_model_rates WHERE group_id = $1)`, g))
}

// Model prices and rates count as the group's pricing table lists them: on
// any model, one the group no longer provides (or never did) included.
func TestRuleCountsOfAnyModel(t *testing.T) {
	e := setup(t)
	declared := e.one(`INSERT INTO groups (name, visibility) VALUES ('declared', 'restricted') RETURNING id`)
	open := e.one(`INSERT INTO groups (name) VALUES ('open') RETURNING id`)
	e.account([]string{"m1"}, declared)
	e.account([]string{}, open) // serves every model that has a global price
	e.price("priced")
	e.rule(declared, "m1")
	e.rule(declared, "gone")
	e.exec(`INSERT INTO group_model_rates (group_id, model, rate) VALUES ($1, 'priced', 1), ($2, 'gone', 1)`, declared, open)
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
	e.priced.global = nil
	require.Equal(t, map[string][]any{"declared": {float64(3), float64(1)}, "open": {float64(1), float64(0)}}, counts())
}

// modelGroups adds five groups, their accounts and two priced models: what
// a group offers by its accounts and its model policy. It returns the ids
// of denied and narrowed.
func (e *env) modelGroups() (denied, narrowed int64) {
	group := func(name, mode string, patterns ...string) int64 {
		return e.one(`INSERT INTO groups (name, model_filter_mode, model_allowlist) VALUES ($1, $2, to_jsonb($3::text[])) RETURNING id`,
			name, mode, append([]string{}, patterns...))
	}
	declared := group("declared", "blacklist")
	unrestricted := group("unrestricted", "blacklist")
	denied = group("denied", "blacklist", "m1")
	narrowed = group("narrowed", "whitelist", "p*")
	group("empty", "blacklist")
	e.account([]string{"m1"}, declared, denied)
	e.account([]string{}, unrestricted, narrowed)
	e.account([]string{"m1"}, narrowed)
	e.price("p1", "p2")
	return denied, narrowed
}

// The gateway's model list of a key in a group (core.GroupModelLister)
// follows the rule of the group pages, with the policy the key's group has.
func TestGroupOfferedModels(t *testing.T) {
	e := setup(t)
	denied, narrowed := e.modelGroups()
	listed, err := e.svc.OfferedModels(context.Background(), core.GroupInfo{ID: narrowed, ModelFilterMode: "whitelist", ModelAllowlist: []string{"p*"}})
	require.NoError(t, err)
	require.Equal(t, []string{"p1", "p2"}, listed)
	listed, err = e.svc.OfferedModels(context.Background(), core.GroupInfo{ID: denied, ModelFilterMode: "blacklist", ModelAllowlist: []string{"m1"}})
	require.NoError(t, err)
	require.Equal(t, []string{}, listed)
}

func TestGroupModels(t *testing.T) {
	e := setup(t)
	e.modelGroups()

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

// A group's own price is a price there: an account that serves every model
// provides the model in that group, without a global price, and in no
// other group.
func TestGroupModelsOfOwnPrice(t *testing.T) {
	e := setup(t)
	own := e.one(`INSERT INTO groups (name) VALUES ('own') RETURNING id`)
	other := e.one(`INSERT INTO groups (name) VALUES ('other') RETURNING id`)
	e.account([]string{}, own, other)
	e.rule(own, "own-model")

	rows, _ := e.listed(e.admin, "/groups")
	require.Equal(t, []any{float64(1), float64(0)}, []any{rows["own"]["model_count"], rows["other"]["model_count"]})
	rows, total := e.listed(e.admin, "/groups?model=own-model")
	require.EqualValues(t, 1, total)
	require.Contains(t, rows, "own")
}

// myGroupRow is a row of GET /me/groups as TestMyGroupsRates reads it.
type myGroupRow struct {
	rate, source, groupRate string
	exclusive               bool
}

// myGroups reads GET /me/groups of the user, by group name.
func (e *env) myGroups(uid int64) map[string]myGroupRow {
	e.t.Helper()
	code, out := e.do(uid, "GET", "/me/groups", nil)
	require.Equal(e.t, 200, code, "%v", out)
	got := map[string]myGroupRow{}
	for _, it := range out["data"].([]any) {
		g := it.(map[string]any)
		got[g["name"].(string)] = myGroupRow{g["effective_rate"].(string), g["rate_source"].(string), g["rate_multiplier"].(string), g["exclusive"].(bool)}
	}
	return got
}

func TestMyGroupsRates(t *testing.T) {
	e := setup(t)
	vip := e.one(`INSERT INTO segments (name) VALUES ('vip') RETURNING id`)
	e.exec(`UPDATE users SET segment_id = $2 WHERE id = $1`, e.user, vip)
	open := e.one(`INSERT INTO groups (name, rate_multiplier) VALUES ('open', 1.5) RETURNING id`)
	granted := e.one(`INSERT INTO groups (name, visibility, rate_multiplier) VALUES ('granted', 'restricted', 2) RETURNING id`)
	e.one(`INSERT INTO groups (name, visibility) VALUES ('closed', 'restricted') RETURNING id`)
	off := e.one(`INSERT INTO groups (name, visibility, status) VALUES ('off', 'restricted', 'disabled') RETURNING id`)
	e.exec(`INSERT INTO segment_groups (segment_id, group_id, granted, rate_override) VALUES ($1, $2, true, 0.5), ($1, $3, true, NULL), ($1, $4, false, 1.2)`,
		vip, granted, off, open)
	// A model rate is not the group's rate.
	e.exec(`INSERT INTO group_model_rates (group_id, model, rate) VALUES ($1, 'm', 9)`, open)

	require.Equal(t, map[string]myGroupRow{
		"open":    {"1.2", core.RateSourceSegment, "1.5", false},
		"granted": {"0.5", core.RateSourceSegment, "2", true},
	}, e.myGroups(e.user), "a segment with its own rates")
	require.Equal(t, map[string]myGroupRow{"open": {"1.5", core.RateSourceGroup, "1.5", false}}, e.myGroups(e.admin), "the default segment")
}
