package billing

import (
	"context"
	"encoding/json"
	"maps"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// pricingEnv is two groups whose accounts declare four models, and three
// users:
//
//	open     public,  rate 1.5
//	private  private, rate 2, granted to vip at its own rate 0.5
//
//	member   in segment vip (discount 0.8)
//	solo     in a personal segment
//	admin    in the default segment
//
// Of the models, claude-haiku-4-5 is priced per token (p 1, c 5),
// claude-sonnet-4-5 by a hand-written expression, web-search per request
// (0.01) and "unpriced" has no price. gpt-5 is priced but no group has it.
type pricingEnv struct {
	*env
	admin, member, solo           int64
	vip, personal, defaultSegment int64
	open, private                 int64
}

func (e *env) one(sql string, args ...any) int64 {
	e.t.Helper()
	var id int64
	require.NoError(e.t, e.db.SQL.QueryRowContext(context.Background(), sql, args...).Scan(&id))
	return id
}

func newPricingEnv(t *testing.T, authz core.Authorizer) *pricingEnv {
	t.Helper()
	e := &pricingEnv{env: newEnvAs(t, authz)}
	e.admin, e.member, e.solo = e.user("admin@example.com"), e.user("member@example.com"), e.user("solo@example.com")
	e.defaultSegment = e.one(`SELECT default_segment_id()`)
	e.vip = e.one(`INSERT INTO segments (name, discount) VALUES ('vip', 0.8) RETURNING id`)
	e.personal = e.one(`INSERT INTO segments (kind, user_id) VALUES ('personal', $1) RETURNING id`, e.solo)
	e.exec(`UPDATE users SET segment_id = $2 WHERE id = $1`, e.member, e.vip)
	e.exec(`UPDATE users SET segment_id = $2 WHERE id = $1`, e.solo, e.personal)
	e.open = e.one(`INSERT INTO groups (name, rate_multiplier) VALUES ('open', 1.5) RETURNING id`)
	e.private = e.one(`INSERT INTO groups (name, visibility, rate_multiplier) VALUES ('private', 'restricted', 2) RETURNING id`)
	e.exec(`INSERT INTO segment_groups (segment_id, group_id, granted, rate_override) VALUES ($1, $2, true, 0.5)`, e.vip, e.private)
	account := e.one(`INSERT INTO accounts (name, plugin_key, type, credentials_enc, models)
		VALUES ('a', 'anthropic', 'apikey', '\x00'::bytea, $1) RETURNING id`,
		[]string{"claude-haiku-4-5", "claude-sonnet-4-5", "web-search", "unpriced"})
	e.exec(`INSERT INTO account_groups (account_id, group_id) VALUES ($1, $2), ($1, $3)`, account, e.open, e.private)
	seedPrices(e.env, e.admin)
	e.mustCall(e.admin, 201, "POST", "/prices", map[string]any{"model": "web-search", "mode": "per_request", "config": map[string]any{"price": 0.01}})
	e.mustCall(e.admin, 201, "POST", "/prices", map[string]any{"model": "gpt-5", "mode": "per_request", "config": map[string]any{"price": 1}})
	return e
}

func multiplier(n string) map[string]any {
	return map[string]any{"kind": "multiplier", "multiplier": n}
}

func custom(mode string, config map[string]any) map[string]any {
	return map[string]any{"kind": "custom", "mode": mode, "config": config}
}

// timesPrice is a price as the API shows it under a multiplier rule: as it is,
// with the multiplier beside it.
func timesPrice(price any, n string) map[string]any {
	out := maps.Clone(price.(map[string]any))
	out["multiplier"] = n
	return out
}

// patch is an item of PUT /groups/:id/model-prices; segment 0 is the group's own rule.
func patch(segment int64, model string, rule map[string]any) map[string]any {
	item := map[string]any{"segment_id": nil, "model": model, "rule": rule}
	if segment != 0 {
		item["segment_id"] = segment
	}
	return item
}

// modelPrices calls /groups/:id/model-prices and returns the answer with its
// rows by model; items makes it a PUT.
func (e *pricingEnv) modelPrices(group, segment int64, items ...map[string]any) (map[string]any, map[string]map[string]any) {
	e.t.Helper()
	path := "/groups/" + itoa(group) + "/model-prices"
	if segment != 0 {
		path += "?segment_id=" + itoa(segment)
	}
	var out map[string]any
	if items == nil {
		out = data(e.mustCall(e.admin, 200, "GET", path, nil))
	} else {
		out = data(e.mustCall(e.admin, 200, "PUT", path, map[string]any{"items": items}))
	}
	rows := map[string]map[string]any{}
	for _, row := range out["models"].([]any) {
		rows[row.(map[string]any)["model"].(string)] = row.(map[string]any)
	}
	return out, rows
}

// fieldError returns the first field error of an invalid_argument response.
func fieldError(t *testing.T, resp map[string]any) (field, message string) {
	t.Helper()
	require.Equal(t, "invalid_argument", errCode(resp), "%v", resp)
	fields := resp["error"].(map[string]any)["details"].(map[string]any)["fields"].([]any)
	first := fields[0].(map[string]any)
	return first["field"].(string), first["message"].(string)
}

func TestGroupModelPrices(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	ctx := context.Background()
	haikuConfig := oneTierConfig(map[string]any{"p": 1.0, "c": 5.0, "cr": 0.1, "cc": 1.25, "cc1h": 2.0})

	// Without rules: the models the group provides at their global prices.
	out, rows := e.modelPrices(e.open, 0)
	require.Equal(t, map[string]any{"id": float64(e.open), "name": "open"}, out["group"])
	require.NotContains(t, out, "segment")
	require.Equal(t, map[string]any{"value": "1.5", "source": "group", "group_default": "1.5", "discount": "1"}, out["rate"])
	var models []string
	for _, row := range out["models"].([]any) {
		models = append(models, row.(map[string]any)["model"].(string))
	}
	require.Equal(t, []string{"claude-haiku-4-5", "claude-sonnet-4-5", "unpriced", "web-search"}, models)
	haiku := map[string]any{"mode": "per_token", "config": haikuConfig, "expression": `tier("base", p*1 + c*5 + cr*0.1 + cc*1.25 + cc1h*2)`}
	require.Equal(t, map[string]any{"model": "claude-haiku-4-5", "base": haiku, "group_rule": nil, "segment_rule_count": 0.0,
		"list_price": haiku, "source": "global"}, rows["claude-haiku-4-5"])
	require.Equal(t, map[string]any{"model": "unpriced", "base": nil, "group_rule": nil, "segment_rule_count": 0.0,
		"list_price": nil, "source": "global"}, rows["unpriced"])

	// Rules of the group and of a segment in it, saved in one request.
	sonnet := rows["claude-sonnet-4-5"]["base"]
	_, rows = e.modelPrices(e.open, 0,
		patch(0, "claude-haiku-4-5", multiplier("1.20")),
		patch(e.vip, "claude-haiku-4-5", custom("per_token", oneTierConfig(map[string]any{"p": 0.8, "c": 4}))),
		patch(e.vip, "claude-sonnet-4-5", multiplier("0.5")),
		patch(0, "web-search", custom("per_request", map[string]any{"price": 0.008})))
	require.Contains(t, e.bus.sent, core.ChannelConfigChanged+` {"key":"price_rules"}`)
	row := rows["claude-haiku-4-5"]
	require.Equal(t, map[string]any{"kind": "multiplier", "multiplier": "1.2"}, row["group_rule"])
	require.Equal(t, 1.0, row["segment_rule_count"])
	require.NotContains(t, row, "segment_rule")
	require.Equal(t, "group", row["source"])
	// A multiplier goes beside the global price; nothing of it is scaled.
	require.Equal(t, timesPrice(haiku, "1.2"), row["list_price"])
	require.Equal(t, haiku, row["base"])
	row = rows["web-search"]
	require.Equal(t, custom("per_request", map[string]any{"price": 0.008}), row["group_rule"])
	require.Equal(t, map[string]any{"mode": "per_request", "config": map[string]any{"price": 0.008}, "expression": `tier("base", flat(0.008))`}, row["list_price"])
	require.Equal(t, "global", rows["claude-sonnet-4-5"]["source"])
	require.Equal(t, 1.0, rows["claude-sonnet-4-5"]["segment_rule_count"])

	// The expression of a custom price is in the history, where a settlement
	// retry reads it by its hash.
	var stored, inHistory int
	require.NoError(t, e.db.SQL.QueryRowContext(ctx, `SELECT count(*), count(h.expr_hash) FROM price_rules r
		LEFT JOIN model_price_history h ON h.expr_hash = r.expr_hash AND h.expression = 'v1:' || r.expression
		WHERE r.kind = 'custom'`).Scan(&stored, &inHistory))
	require.Equal(t, 2, stored)
	require.Equal(t, 2, inHistory)

	// For a segment: its rate there, its own rule next to the group's.
	out, rows = e.modelPrices(e.open, e.vip)
	require.Equal(t, map[string]any{"id": float64(e.vip), "kind": "named", "name": "vip"}, out["segment"])
	require.Equal(t, map[string]any{"value": "1.2", "source": "discount", "group_default": "1.5", "discount": "0.8"}, out["rate"])
	row = rows["claude-haiku-4-5"]
	own := oneTierConfig(map[string]any{"p": 0.8, "c": 4.0})
	require.Equal(t, custom("per_token", own), row["segment_rule"])
	require.Equal(t, map[string]any{"kind": "multiplier", "multiplier": "1.2"}, row["group_rule"])
	require.NotContains(t, row, "segment_rule_count")
	require.Equal(t, "segment", row["source"])
	require.Equal(t, map[string]any{"mode": "per_token", "config": own, "expression": `tier("base", p*0.8 + c*4)`}, row["list_price"])
	// The same over a hand-written expression.
	row = rows["claude-sonnet-4-5"]
	require.Equal(t, "segment", row["source"])
	require.Equal(t, timesPrice(sonnet, "0.5"), row["list_price"])
	require.Equal(t, sonnet, row["base"])
	require.Contains(t, rows["web-search"], "segment_rule")
	require.Nil(t, rows["web-search"]["segment_rule"])
	require.Equal(t, "group", rows["web-search"]["source"])
	// Another segment only gets the group's rules.
	_, rows = e.modelPrices(e.open, e.defaultSegment)
	require.Equal(t, "group", rows["claude-haiku-4-5"]["source"])
	require.Equal(t, "global", rows["claude-sonnet-4-5"]["source"])

	// A rule is replaced in place and removed by null; removing one that is
	// not there, on any model, is fine.
	_, rows = e.modelPrices(e.open, e.vip,
		patch(0, "claude-haiku-4-5", multiplier("2")),
		patch(e.vip, "claude-sonnet-4-5", nil),
		patch(e.vip, "no-such-model", nil))
	require.Equal(t, map[string]any{"kind": "multiplier", "multiplier": "2"}, rows["claude-haiku-4-5"]["group_rule"])
	require.Equal(t, "global", rows["claude-sonnet-4-5"]["source"])
	require.Equal(t, 3, int(e.one(`SELECT count(*) FROM price_rules`)))

	// The global price changes its mode: the custom price of the old mode is
	// inactive and the next level prices the model.
	haikuID := e.one(`SELECT id FROM model_prices WHERE model = 'claude-haiku-4-5'`)
	e.mustCall(e.admin, 200, "PATCH", "/prices/"+itoa(haikuID), map[string]any{"mode": "per_request", "config": map[string]any{"price": 1}})
	_, rows = e.modelPrices(e.open, e.vip)
	row = rows["claude-haiku-4-5"]
	require.Equal(t, map[string]any{"kind": "custom", "mode": "per_token", "config": own, "inactive": true}, row["segment_rule"])
	require.Equal(t, "group", row["source"])
	require.Equal(t, map[string]any{"mode": "per_request", "config": map[string]any{"price": 1.0}, "expression": `tier("base", flat(1))`,
		"multiplier": "2"}, row["list_price"])
	r, err := e.svc.Resolve(ctx, core.PriceQuery{Model: "claude-haiku-4-5", GroupID: e.open, SegmentID: e.vip})
	require.NoError(t, err)
	require.Equal(t, core.PriceSourceGroup, r.Source)
	require.Nil(t, r.Base)
	require.Equal(t, "2", r.Scale.String())

	// Not found and malformed.
	e.mustCall(e.admin, 404, "GET", "/groups/999999/model-prices", nil)
	require.Equal(t, "user group not found", e.mustCall(e.admin, 404, "GET", "/groups/"+itoa(e.open)+"/model-prices?segment_id=999999", nil)["error"].(map[string]any)["message"])
	e.mustCall(e.admin, 400, "GET", "/groups/"+itoa(e.open)+"/model-prices?segment_id=vip", nil)
	e.mustCall(e.admin, 404, "PUT", "/groups/999999/model-prices", map[string]any{"items": []any{}})
	e.mustCall(e.admin, 404, "PUT", "/groups/"+itoa(e.open)+"/model-prices?segment_id=999999", map[string]any{"items": []any{}})
}

func TestGroupModelPricesValidation(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	path := "/groups/" + itoa(e.open) + "/model-prices"
	valid := patch(0, "web-search", multiplier("3"))
	cases := []struct {
		name    string
		item    map[string]any
		message string
	}{
		{"unknown segment", patch(999999, "claude-haiku-4-5", multiplier("1")), "unknown user group"},
		{"unknown segment, removing", patch(999999, "claude-haiku-4-5", nil), "unknown user group"},
		{"model of no account", patch(0, "gpt-5", multiplier("1")), "does not provide this model"},
		{"no global price", patch(0, "unpriced", multiplier("1")), "no global price"},
		{"unknown kind", patch(0, "claude-haiku-4-5", map[string]any{"kind": "fixed"}), "a multiplier or a custom price"},
		{"negative multiplier", patch(0, "claude-haiku-4-5", multiplier("-1")), "multiplier must be"},
		{"multiplier too large", patch(0, "claude-haiku-4-5", multiplier("1000000")), "below 1000000"},
		{"multiplier too fine", patch(0, "claude-haiku-4-5", multiplier("0.00001")), "at most 4 decimals"},
		{"multiplier that is no number", patch(0, "claude-haiku-4-5", multiplier("much")), "multiplier must be"},
		{"custom over an expression", patch(0, "claude-sonnet-4-5", custom("per_token", oneTierConfig(map[string]any{"p": 1, "c": 2}))), "multiplier only"},
		{"custom in another mode", patch(0, "claude-haiku-4-5", custom("per_request", map[string]any{"price": 1})), "a custom price is per_token"},
		{"negative price", patch(0, "claude-haiku-4-5", custom("per_token", oneTierConfig(map[string]any{"p": 1, "c": -2}))), "config.tiers[0].c"},
		{"old flat prices", patch(0, "claude-haiku-4-5", custom("per_token", map[string]any{"p": 1, "c": 2})), `unknown field`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// All or none: the valid item before it is not saved either.
			code, resp := e.call(e.admin, "PUT", path, map[string]any{"items": []any{valid, tc.item}})
			require.Equal(t, 400, code)
			field, message := fieldError(t, resp)
			require.Equal(t, "items.1", field)
			require.Contains(t, message, tc.message)
			require.Zero(t, e.one(`SELECT count(*) FROM price_rules`))
		})
	}
	// 0 and 1 are multipliers like any other.
	_, rows := e.modelPrices(e.open, e.vip, patch(0, "claude-haiku-4-5", multiplier("0")), patch(e.vip, "claude-haiku-4-5", multiplier("1")))
	require.Equal(t, "segment", rows["claude-haiku-4-5"]["source"])
	require.Equal(t, map[string]any{"kind": "multiplier", "multiplier": "0"}, rows["claude-haiku-4-5"]["group_rule"])
}

// A segment is deleted while a rule of it is being saved: the write waits for
// the delete and, once that commits, fails on the foreign key. That is a
// conflict to retry, and nothing of the request is saved.
func TestGroupModelPricesConflict(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	ctx := context.Background()
	leaving := e.one(`INSERT INTO segments (name) VALUES ('leaving') RETURNING id`)
	tx, err := testutil.Begin(ctx, e.db)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = store.SQLTx(tx).ExecContext(ctx, `DELETE FROM segments WHERE id = $1`, leaving)
	require.NoError(t, err)

	type answer struct {
		code int
		body map[string]any
	}
	saved := make(chan answer, 1)
	go func() {
		code, body := e.call(e.admin, "PUT", "/groups/"+itoa(e.open)+"/model-prices", map[string]any{"items": []any{
			patch(0, "web-search", multiplier("3")), patch(leaving, "web-search", multiplier("2"))}})
		saved <- answer{code, body}
	}()
	// The write is there once it waits for the lock the delete holds.
	require.Eventually(t, func() bool {
		var waiting int
		err := e.db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting)
		return err == nil && waiting > 0
	}, 10*time.Second, 5*time.Millisecond)
	require.NoError(t, tx.Commit().Error)

	got := <-saved
	require.Equal(t, 409, got.code, "%v", got.body)
	require.Equal(t, "conflict", errCode(got.body))
	require.Zero(t, e.one(`SELECT count(*) FROM price_rules`))
}

// Who may read and change the model prices of a group, and who sees the
// email a personal segment goes by.
func TestGroupModelPricesPermissions(t *testing.T) {
	const reader, manager, outsider = 1001, 1002, 1003
	e := newPricingEnv(t, limited{perms: map[int64][]string{
		reader: {"group:read"}, manager: {"group:read", "group:manage", "user:read"}, outsider: {"price:read", "user:read"}}})
	path := "/groups/" + itoa(e.open) + "/model-prices?segment_id=" + itoa(e.personal)
	items := map[string]any{"items": []any{patch(e.personal, "web-search", multiplier("0.9"))}}

	e.mustCall(outsider, 403, "GET", path, nil)
	e.mustCall(outsider, 403, "PUT", path, items)
	e.mustCall(reader, 403, "PUT", path, items)
	require.Zero(t, e.one(`SELECT count(*) FROM price_rules`))

	personal := func(name string) map[string]any {
		return map[string]any{"id": float64(e.personal), "kind": "personal", "name": name, "user_id": float64(e.solo)}
	}
	require.Equal(t, personal("#"+itoa(e.solo)), data(e.mustCall(reader, 200, "GET", path, nil))["segment"])
	require.Equal(t, personal("solo@example.com"), data(e.mustCall(manager, 200, "PUT", path, items))["segment"])
	require.Equal(t, 1, int(e.one(`SELECT count(*) FROM price_rules WHERE segment_id = $1 AND updated_by = $2`, e.personal, manager)))
}

// Nobody but a superuser sets the model prices of a user group they are in,
// a personal one included; the group's own prices and the default segment
// are not anyone's own (FU2). Nobody here manages itself (no superuser).
func TestGroupModelPricesOfOwnSegment(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	e.svc.Users = managesOthers{}
	put := func(caller, segment int64) (int, map[string]any) {
		return e.call(caller, "PUT", "/groups/"+itoa(e.open)+"/model-prices",
			map[string]any{"items": []any{patch(segment, "web-search", multiplier("0.9"))}})
	}
	for _, tc := range []struct {
		name            string
		caller, segment int64
		status          int
	}{
		{"own named segment", e.member, e.vip, 409},
		{"own personal segment", e.solo, e.personal, 409},
		{"another's segment", e.member, e.personal, 200},
		{"the group's own price", e.member, 0, 200},
		{"the default segment", e.admin, e.defaultSegment, 200},
	} {
		if status, resp := put(tc.caller, tc.segment); status != tc.status {
			t.Fatalf("%s: %d %v", tc.name, status, resp)
		}
	}
	require.Zero(t, e.one(`SELECT count(*) FROM price_rules WHERE segment_id = $1 OR (segment_id = $2 AND updated_by = $3)`, e.vip, e.personal, e.solo),
		"refused saves wrote nothing")
}

// managesOthers is a user guard under which every caller manages everyone
// but itself.
type managesOthers struct{}

func (managesOthers) CanActOnUsers(_ context.Context, actorID int64, _ core.UserAction, targetIDs ...int64) error {
	if slices.Contains(targetIDs, actorID) {
		return core.ErrPermissionDenied
	}
	return nil
}

// A rule prices requests as soon as it is saved, and a price handed out is
// the caller's to change.
func TestResolveFollowsRules(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	ctx := context.Background()
	q := core.PriceQuery{Model: "claude-haiku-4-5", GroupID: e.open, SegmentID: e.vip}
	resolve := func(q core.PriceQuery) *core.PriceRule {
		t.Helper()
		r, err := e.svc.Resolve(ctx, q)
		require.NoError(t, err)
		return r
	}

	// Without a group and a segment, and without a rule, it is the global
	// price, field for field what Resolve gave before there were rules.
	global := resolve(core.PriceQuery{Model: q.Model})
	var want core.PriceRule
	require.NoError(t, e.db.SQL.QueryRowContext(ctx, `SELECT id, model, mode, expression, expr_version, expr_hash FROM model_prices WHERE model = $1`, q.Model).
		Scan(&want.ID, &want.Model, &want.Mode, &want.Expression, &want.ExprVersion, &want.ExprHash))
	require.Equal(t, &want, global)
	require.Equal(t, global, resolve(q))

	e.modelPrices(e.open, 0, patch(0, q.Model, multiplier("1.2")))
	r := resolve(q)
	require.Equal(t, core.PriceSourceGroup, r.Source)
	require.Equal(t, "1.2", r.Scale.String())
	require.Equal(t, "2.4", r.Multiplier(*dec("2")).String())
	want.Source, r.Scale = core.PriceSourceGroup, nil
	require.Equal(t, &want, r)
	require.Equal(t, global, resolve(core.PriceQuery{Model: q.Model}))
	require.Equal(t, global, resolve(core.PriceQuery{Model: q.Model, GroupID: e.private, SegmentID: e.vip}))

	e.modelPrices(e.open, 0, patch(e.vip, q.Model, custom("per_token", oneTierConfig(map[string]any{"p": 0.8, "c": 4}))))
	r = resolve(q)
	require.Equal(t, core.PriceSourceSegment, r.Source)
	require.Nil(t, r.Scale)
	require.Equal(t, &core.PriceBase{Expression: global.Expression, ExprHash: global.ExprHash}, r.Base)
	require.Equal(t, `tier("base", p*0.8 + c*4)`, r.Expression)
	require.Equal(t, []any{global.ID, "per_token", 1}, []any{r.ID, r.Mode, r.ExprVersion})
	var frozen string
	require.NoError(t, e.db.SQL.QueryRowContext(ctx, `SELECT expression FROM model_price_history WHERE expr_hash = $1`, r.ExprHash).Scan(&frozen))
	require.Contains(t, frozen, r.Expression)
	// Another segment of the group still gets the group's rule.
	require.Equal(t, core.PriceSourceGroup, resolve(core.PriceQuery{Model: q.Model, GroupID: e.open, SegmentID: e.personal}).Source)

	// The answer is a copy.
	r.Base.Expression, r.Expression = "changed", "changed"
	require.Equal(t, global.Expression, resolve(q).Base.Expression)
	scaled := resolve(core.PriceQuery{Model: q.Model, GroupID: e.open})
	*scaled.Scale = *dec("9")
	require.Equal(t, "1.2", resolve(core.PriceQuery{Model: q.Model, GroupID: e.open}).Scale.String())

	// Rules another package removed apply once it says so.
	e.exec(`DELETE FROM price_rules WHERE segment_id = $1`, e.vip)
	require.Equal(t, core.PriceSourceSegment, resolve(q).Source)
	e.svc.PriceRulesChanged(ctx)
	require.Equal(t, core.PriceSourceGroup, resolve(q).Source)

	// Without a global price there is nothing a rule could apply to.
	e.exec(`UPDATE model_prices SET enabled = false WHERE model = $1`, q.Model)
	e.svc.invalidate()
	_, err := e.svc.Resolve(ctx, q)
	require.Equal(t, "model_price_not_configured", core.AsError(err).Code)
}

func TestPricingQuote(t *testing.T) {
	const reader, outsider = 1001, 1002
	e := newPricingEnv(t, limited{perms: map[int64][]string{reader: {"price:read", "group:read", "segment:read"}, outsider: {"group:read", "user:read"}}})
	usage := map[string]any{"p": 1_000_000, "c": 1_000_000}
	quote := func(who string, id, group int64, model string) map[string]any {
		t.Helper()
		return data(e.mustCall(e.admin, 200, "POST", "/pricing/quote", map[string]any{who: id, "group_id": group, "model": model, "usage": usage}))
	}
	e.modelPrices(e.private, 0,
		patch(0, "claude-haiku-4-5", multiplier("1.2")),
		patch(e.vip, "web-search", custom("per_request", map[string]any{"price": 0.008})),
		patch(e.personal, "claude-sonnet-4-5", multiplier("0.5")))

	// A member of vip in the private group: granted, at vip's own rate, at
	// the group's rule: 6 x 1.2 = 7.2, x 0.5 = 3.6.
	out := quote("user_id", e.member, e.private, "claude-haiku-4-5")
	require.Equal(t, map[string]any{"id": float64(e.vip), "kind": "named", "name": "vip"}, out["segment"])
	require.Equal(t, []any{true, "grant", "group"}, []any{out["accessible"], out["access_source"], out["price_source"]})
	require.Equal(t, map[string]any{"segment": nil, "group": multiplier("1.2")}, out["price_levels"])
	require.Equal(t, map[string]any{"value": "0.5", "source": "segment", "group_default": "2", "discount": "0.8"}, out["rate"])
	require.Equal(t, []any{"0.5", "7.2", "3.6"}, []any{out["rate_override"], out["list_cost"], out["cost"]})
	require.Equal(t, 1.0, out["base"].(map[string]any)["config"].(map[string]any)["tiers"].([]any)[0].(map[string]any)["p"])
	require.NotContains(t, out["base"], "multiplier")
	require.Equal(t, timesPrice(out["base"], "1.2"), out["list_price"])

	// The same for the segment itself; its own price applies per request.
	out = quote("segment_id", e.vip, e.private, "web-search")
	require.Equal(t, "segment", out["price_source"])
	require.Equal(t, map[string]any{"segment": custom("per_request", map[string]any{"price": 0.008}), "group": nil}, out["price_levels"])
	require.Equal(t, []any{"0.008", "0.004"}, []any{out["list_cost"], out["cost"]})
	require.Equal(t, 0.01, out["base"].(map[string]any)["config"].(map[string]any)["price"])
	require.Equal(t, 0.008, out["list_price"].(map[string]any)["config"].(map[string]any)["price"])

	// The default segment was not granted the group: quoted all the same, at
	// the group's rate.
	out = quote("user_id", e.admin, e.private, "claude-haiku-4-5")
	require.Equal(t, float64(e.defaultSegment), out["segment"].(map[string]any)["id"])
	require.Equal(t, []any{false, "none", nil}, []any{out["accessible"], out["access_source"], out["rate_override"]})
	require.Equal(t, map[string]any{"value": "2", "source": "group", "group_default": "2", "discount": "1"}, out["rate"])
	require.Equal(t, []any{"7.2", "14.4"}, []any{out["list_cost"], out["cost"]})

	// A public group at the discounted rate and the global price; a
	// multiplier on a hand-written expression (28.5 at this length) is in
	// the costs and beside the list price shown.
	out = quote("user_id", e.member, e.open, "claude-haiku-4-5")
	require.Equal(t, []any{true, "public", "global", "6", "7.2"},
		[]any{out["accessible"], out["access_source"], out["price_source"], out["list_cost"], out["cost"]})
	require.Equal(t, "discount", out["rate"].(map[string]any)["source"])
	out = quote("segment_id", e.personal, e.private, "claude-sonnet-4-5")
	require.Equal(t, []any{"segment", "14.25", "28.5"}, []any{out["price_source"], out["list_cost"], out["cost"]})
	require.Equal(t, timesPrice(out["base"], "0.5"), out["list_price"])
	require.Equal(t, map[string]any{"id": float64(e.personal), "kind": "personal", "name": "solo@example.com", "user_id": float64(e.solo)}, out["segment"])

	// A model without a global price cannot be billed.
	out = quote("user_id", e.member, e.private, "unpriced")
	require.Equal(t, []any{nil, nil, "global", "0", "0"}, []any{out["base"], out["list_price"], out["price_source"], out["list_cost"], out["cost"]})

	// Whom to quote for, and what.
	for name, body := range map[string]map[string]any{
		"neither": {"group_id": e.open, "model": "web-search"},
		"both":    {"user_id": e.member, "segment_id": e.vip, "group_id": e.open, "model": "web-search"},
	} {
		code, resp := e.call(e.admin, "POST", "/pricing/quote", body)
		require.Equal(t, 400, code, name)
		field, _ := fieldError(t, resp)
		require.Equal(t, "user_id", field, name)
	}
	code, resp := e.call(e.admin, "POST", "/pricing/quote", map[string]any{"user_id": e.member, "group_id": e.open, "model": "gpt-5"})
	require.Equal(t, 400, code)
	field, message := fieldError(t, resp)
	require.Equal(t, []string{"model", "the group does not provide this model"}, []string{field, message})
	resp = e.mustCall(e.admin, 404, "POST", "/pricing/quote", map[string]any{"user_id": 999999, "group_id": e.open, "model": "web-search"})
	require.Equal(t, "user not found", resp["error"].(map[string]any)["message"])
	resp = e.mustCall(e.admin, 404, "POST", "/pricing/quote", map[string]any{"segment_id": 999999, "group_id": e.open, "model": "web-search"})
	require.Equal(t, "user group not found", resp["error"].(map[string]any)["message"])
	e.mustCall(e.admin, 404, "POST", "/pricing/quote", map[string]any{"segment_id": e.vip, "group_id": 999999, "model": "web-search"})

	// price:read, group:read and segment:read quote for a segment; without
	// user:read a personal segment has no email.
	body := map[string]any{"segment_id": e.personal, "group_id": e.open, "model": "web-search"}
	e.mustCall(outsider, 403, "POST", "/pricing/quote", body)
	e.mustCall(outsider, 403, "GET", "/me/prices", nil)
	require.Equal(t, "#"+itoa(e.solo), data(e.mustCall(reader, 200, "POST", "/pricing/quote", body))["segment"].(map[string]any)["name"])
}

// A quote needs what the console's entry to it needs: price:read and
// group:read, and user:read for a user or segment:read for a segment. Without
// them it would tell which users exist and where they are priced.
func TestPricingQuotePermissions(t *testing.T) {
	const pricer, grouper, users, segments = 1001, 1002, 1003, 1004
	e := newPricingEnv(t, limited{perms: map[int64][]string{
		pricer:   {"price:read"},
		grouper:  {"price:read", "group:read"},
		users:    {"price:read", "group:read", "user:read"},
		segments: {"price:read", "group:read", "segment:read"},
	}})
	ofUser := map[string]any{"user_id": e.member, "group_id": e.open, "model": "web-search"}
	ofSegment := map[string]any{"segment_id": e.vip, "group_id": e.open, "model": "web-search"}
	denied := func(uid int64, body map[string]any, permission string) {
		t.Helper()
		code, resp := e.call(uid, "POST", "/pricing/quote", body)
		require.Equal(t, 403, code, "%v", resp)
		require.Equal(t, permission, resp["error"].(map[string]any)["details"].(map[string]any)["permission"])
	}
	denied(pricer, ofUser, "group:read")
	denied(pricer, ofSegment, "group:read")
	denied(grouper, ofUser, "user:read")
	denied(grouper, ofSegment, "segment:read")
	denied(segments, ofUser, "user:read")
	denied(users, ofSegment, "segment:read")
	// A user that does not exist is not told apart from one that does.
	denied(grouper, map[string]any{"user_id": 999999, "group_id": e.open, "model": "web-search"}, "user:read")

	require.Equal(t, "vip", data(e.mustCall(users, 200, "POST", "/pricing/quote", ofUser))["segment"].(map[string]any)["name"])
	require.Equal(t, "vip", data(e.mustCall(segments, 200, "POST", "/pricing/quote", ofSegment))["segment"].(map[string]any)["name"])
}

// The usage of a quote is bounded as a request's is: 0 to 1e12 tokens of
// each kind. Beyond that it would be a cost no request can have.
func TestPricingQuoteUsageBounds(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	quote := func(usage map[string]any) (int, map[string]any) {
		return e.call(e.admin, "POST", "/pricing/quote", map[string]any{"user_id": e.member, "group_id": e.open,
			"model": "claude-haiku-4-5", "usage": usage})
	}
	code, resp := quote(map[string]any{"p": 1000, "c": -1, "cr": 1e30, "cc": 1e12 + 1, "cc1h": 0})
	require.Equal(t, 400, code, "%v", resp)
	var fields []string
	for _, f := range resp["error"].(map[string]any)["details"].(map[string]any)["fields"].([]any) {
		fields = append(fields, f.(map[string]any)["field"].(string))
	}
	require.Equal(t, []string{"usage.c", "usage.cr", "usage.cc"}, fields)

	// The bounds themselves are a usage: 1e12 input tokens at 1 per million.
	code, resp = quote(map[string]any{"p": 1e12, "c": 0})
	require.Equal(t, 200, code, "%v", resp)
	require.Equal(t, "1000000", data(resp)["list_cost"])
}

// The prices a quote shows and the costs it gives come from one price
// snapshot: a rule this node has not heard of yet (its change broadcast was
// lost) is in neither, rather than shown beside costs without it.
func TestPricingQuoteOneSnapshot(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	body := map[string]any{"user_id": e.member, "group_id": e.open, "model": "claude-haiku-4-5",
		"usage": map[string]any{"p": 1_000_000, "c": 1_000_000}}
	before := data(e.mustCall(e.admin, 200, "POST", "/pricing/quote", body))
	require.Equal(t, []any{"global", "6"}, []any{before["price_source"], before["list_cost"]})

	// Written behind the service's back, as another node would.
	e.exec(`INSERT INTO price_rules (group_id, model, kind, multiplier) VALUES ($1, 'claude-haiku-4-5', 'multiplier', 2)`, e.open)
	stale := data(e.mustCall(e.admin, 200, "POST", "/pricing/quote", body))
	require.Equal(t, before, stale)

	// Once the change is heard of, both show it.
	e.svc.PriceRulesChanged(context.Background())
	after := data(e.mustCall(e.admin, 200, "POST", "/pricing/quote", body))
	require.Equal(t, []any{"group", "12"}, []any{after["price_source"], after["list_cost"]})
	require.Equal(t, multiplier("2"), after["price_levels"].(map[string]any)["group"])
	require.Equal(t, timesPrice(after["base"], "2"), after["list_price"])
}

// GET /me/prices?group_id=: what the group provides and can bill, at the
// caller's list price and rate there.
func TestMyPricesInGroup(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	e.modelPrices(e.private, 0,
		patch(0, "claude-haiku-4-5", multiplier("1.2")),
		patch(e.vip, "web-search", custom("per_request", map[string]any{"price": 0.008})))
	e.modelPrices(e.open, 0, patch(e.personal, "web-search", multiplier("0")))
	byModel := func(d map[string]any) map[string]map[string]any {
		rows := map[string]map[string]any{}
		for _, row := range d["prices"].([]any) {
			rows[row.(map[string]any)["model"].(string)] = row.(map[string]any)
		}
		return rows
	}

	d := data(e.mustCall(e.member, 200, "GET", "/me/prices?group_id="+itoa(e.private), nil))
	require.Equal(t, map[string]any{"id": float64(e.private), "name": "private"}, d["group"])
	require.Equal(t, []any{"0.5", "segment"}, []any{d["rate_multiplier"], d["rate_source"]})
	rows := byModel(d)
	require.Len(t, rows, 3, "a model without a global price is left out")
	require.Equal(t, map[string]any{"model": "claude-haiku-4-5", "mode": "per_token", "source": "group", "multiplier": "1.2",
		"config":     oneTierConfig(map[string]any{"p": 1.0, "c": 5.0, "cr": 0.1, "cc": 1.25, "cc1h": 2.0}),
		"expression": `tier("base", p*1 + c*5 + cr*0.1 + cc*1.25 + cc1h*2)`}, rows["claude-haiku-4-5"])
	require.Equal(t, map[string]any{"model": "web-search", "mode": "per_request", "source": "segment",
		"config": map[string]any{"price": 0.008}, "expression": `tier("base", flat(0.008))`}, rows["web-search"])
	require.Equal(t, "global", rows["claude-sonnet-4-5"]["source"])
	require.NotContains(t, rows["claude-sonnet-4-5"], "multiplier")

	// The public group, at the discounted rate; no rules for vip there.
	d = data(e.mustCall(e.member, 200, "GET", "/me/prices?group_id="+itoa(e.open), nil))
	require.Equal(t, []any{"1.2", "discount"}, []any{d["rate_multiplier"], d["rate_source"]})
	require.Equal(t, "global", byModel(d)["claude-haiku-4-5"]["source"])
	require.Equal(t, "global", byModel(d)["web-search"]["source"])
	// A personal segment's own rule there, a multiplier of 0, is its user's alone.
	free := byModel(data(e.mustCall(e.solo, 200, "GET", "/me/prices?group_id="+itoa(e.open), nil)))["web-search"]
	require.Equal(t, map[string]any{"model": "web-search", "mode": "per_request", "source": "segment", "multiplier": "0",
		"config": map[string]any{"price": 0.01}, "expression": `tier("base", flat(0.01))`}, free)

	// Without a group the prices are the global ones and no rule shows.
	d = data(e.mustCall(e.member, 200, "GET", "/me/prices", nil))
	require.Nil(t, d["group"])
	require.NotContains(t, d, "rate_source")
	rows = byModel(d)
	require.Len(t, rows, 4)
	both := []any{map[string]any{"id": float64(e.open), "name": "open"}, map[string]any{"id": float64(e.private), "name": "private"}}
	require.Equal(t, map[string]any{"model": "web-search", "mode": "per_request", "config": map[string]any{"price": 0.01},
		"expression": `tier("base", flat(0.01))`, "groups": both}, rows["web-search"])
	require.Equal(t, []any{}, rows["gpt-5"]["groups"])
	// Whoever was not granted the private group does not see it.
	require.Equal(t, both[:1], byModel(data(e.mustCall(e.admin, 200, "GET", "/me/prices", nil)))["web-search"]["groups"])
	e.mustCall(e.admin, 404, "GET", "/me/prices?group_id="+itoa(e.private), nil)

	// A disabled group is nobody's.
	e.exec(`UPDATE groups SET status = 'disabled' WHERE id = $1`, e.private)
	e.mustCall(e.member, 404, "GET", "/me/prices?group_id="+itoa(e.private), nil)
	require.Equal(t, both[:1], byModel(data(e.mustCall(e.member, 200, "GET", "/me/prices", nil)))["web-search"]["groups"])

	// A multiplier over a hand-written expression goes beside it: the
	// expression is the global price's.
	global := byModel(data(e.mustCall(e.member, 200, "GET", "/me/prices", nil)))["claude-sonnet-4-5"]["expression"]
	e.modelPrices(e.open, 0, patch(0, "claude-sonnet-4-5", multiplier("2")))
	sonnet := byModel(data(e.mustCall(e.member, 200, "GET", "/me/prices?group_id="+itoa(e.open), nil)))["claude-sonnet-4-5"]
	require.Equal(t, []any{"group", "2", global}, []any{sonnet["source"], sonnet["multiplier"], sonnet["expression"]})
}

// GET /key/prices: the list prices of the key's owner in the key's group, in
// a form a downstream sup2api compiles to the same prices.
func TestKeyPricesFollowRules(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	e.modelPrices(e.private, 0,
		patch(0, "claude-haiku-4-5", multiplier("1.2")),
		patch(e.vip, "claude-sonnet-4-5", multiplier("0.5")),
		patch(e.personal, "claude-haiku-4-5", custom("per_token", oneTierConfig(map[string]any{"p": 0.8, "c": 4}))),
		patch(0, "web-search", multiplier("3")))
	prices := func(segment int64) (string, map[string]*syncPrice) {
		t.Helper()
		e.svc.SetSyncDeps(SyncDeps{Keys: fakeKeys{Group: e.private, Segment: segment}})
		req := httptest.NewRequest("GET", "/api/v1/key/prices", nil)
		req.Header.Set("Authorization", "Bearer sk-good")
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code, w.Body.String())
		var body struct {
			Data struct {
				RateMultiplier string      `json:"rate_multiplier"`
				Prices         []syncPrice `json:"prices"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		for _, p := range body.Data.Prices {
			require.NotEmpty(t, p.Expression, p.Model)
		}
		// What a downstream instance makes of the answer, rate left aside.
		f, err := parseSup2API(w.Body.Bytes(), false)
		require.NoError(t, err)
		require.Zero(t, f.skipped)
		for _, p := range f.prices {
			require.NoError(t, p.compile(), p.Model)
		}
		return body.Data.RateMultiplier, f.prices
	}

	// The rate is the principal's; only models the key's group allows (claude-*).
	rate, got := prices(e.vip)
	require.Equal(t, "2", rate)
	require.Len(t, got, 2)
	require.Equal(t, `v1:tier("base", p*1.2 + c*6 + cr*0.12 + cc*1.5 + cc1h*2.4)`, got["claude-haiku-4-5"].Expression)
	require.Equal(t, "per_token", got["claude-haiku-4-5"].Mode)
	// A multiplier on a hand-written expression becomes a rule of it.
	require.True(t, strings.HasSuffix(got["claude-sonnet-4-5"].Expression, `) * 0.5`), got["claude-sonnet-4-5"].Expression)

	_, got = prices(e.personal)
	require.Equal(t, `v1:tier("base", p*0.8 + c*4)`, got["claude-haiku-4-5"].Expression)
	require.NotContains(t, got["claude-sonnet-4-5"].Expression, ") *")

	// Free over a hand-written expression: it times 0.
	// A multiplier of 1 is the global price, with nothing appended.
	e.modelPrices(e.private, 0,
		patch(e.personal, "claude-sonnet-4-5", multiplier("0")),
		patch(e.personal, "claude-haiku-4-5", multiplier("1")))
	_, got = prices(e.personal)
	require.True(t, strings.HasSuffix(got["claude-sonnet-4-5"].Expression, `) * 0`), got["claude-sonnet-4-5"].Expression)
	require.Equal(t, "0", costOf(t, got["claude-sonnet-4-5"].Expression))
	require.Equal(t, `v1:tier("base", p*1 + c*5 + cr*0.1 + cc*1.25 + cc1h*2)`, got["claude-haiku-4-5"].Expression)

	// An expression that ends in a line comment takes the multiplier too.
	sonnetID := e.one(`SELECT id FROM model_prices WHERE model = 'claude-sonnet-4-5'`)
	e.mustCall(e.admin, 200, "PATCH", "/prices/"+itoa(sonnetID), map[string]any{"expression": `tier("base", p*3 + c*15) // don't`})
	_, got = prices(e.vip)
	require.Len(t, got, 2)
	require.Equal(t, "9", costOf(t, got["claude-sonnet-4-5"].Expression))
	e.modelPrices(e.private, 0, patch(e.vip, "claude-sonnet-4-5", nil), patch(e.personal, "claude-sonnet-4-5", nil))
	_, got = prices(e.vip)
	require.Len(t, got, 2)
	require.Equal(t, "18", costOf(t, got["claude-sonnet-4-5"].Expression))
}
