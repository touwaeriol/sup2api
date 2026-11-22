package billing

import (
	"context"
	"encoding/json"
	"maps"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

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
	e.mustCall(e.admin, 201, "POST", "/prices", map[string]any{"model": "web-search", "mode": "visual", "config": requestTier(0.01)})
	e.mustCall(e.admin, 201, "POST", "/prices", map[string]any{"model": "gpt-5", "mode": "visual", "config": requestTier(1)})
	return e
}

func multiplier(n string) map[string]any {
	return map[string]any{"kind": "multiplier", "multiplier": n}
}

func custom(mode string, config map[string]any) map[string]any {
	return map[string]any{"kind": "custom", "mode": mode, "config": config}
}

// written is a custom price written as an expression.
func written(expression string) map[string]any {
	return map[string]any{"kind": "custom", "mode": "expression", "expression": expression}
}

// shown is a custom price as the API shows it: with its expression.
func shown(rule map[string]any, expression string) map[string]any {
	out := maps.Clone(rule)
	out["expression"] = expression
	return out
}

// timesPrice is a price as the API shows it under a multiplier rule: as it is,
// with the multiplier beside it.
func timesPrice(price any, n string) map[string]any {
	out := maps.Clone(price.(map[string]any))
	out["multiplier"] = n
	return out
}

// patch is an item of PUT /groups/:id/model-prices.
func patch(model string, rule map[string]any) map[string]any {
	return map[string]any{"model": model, "rule": rule}
}

// modelPrices calls /groups/:id/model-prices and returns the answer with its
// rows by model; items makes it a PUT.
func (e *pricingEnv) modelPrices(group int64, items ...map[string]any) (map[string]any, map[string]map[string]any) {
	e.t.Helper()
	path := "/groups/" + itoa(group) + "/model-prices"
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

	// Without rules: the models the group provides at their global prices,
	// at the group's default rate.
	out, rows := e.modelPrices(e.open)
	require.Equal(t, map[string]any{"id": float64(e.open), "name": "open"}, out["group"])
	require.Equal(t, map[string]any{"value": "1.5", "source": "group", "group_default": "1.5", "discount": "1"}, out["rate"])
	var models []string
	for _, row := range out["models"].([]any) {
		models = append(models, row.(map[string]any)["model"].(string))
	}
	require.Equal(t, []string{"claude-haiku-4-5", "claude-sonnet-4-5", "unpriced", "web-search"}, models)
	haiku := map[string]any{"mode": "visual", "config": haikuConfig, "expression": `tier("base", p*1 + c*5 + cr*0.1 + cc*1.25 + cc1h*2)`}
	require.Equal(t, map[string]any{"model": "claude-haiku-4-5", "offered": true, "base": haiku, "group_rule": nil,
		"list_price": haiku, "source": "global"}, rows["claude-haiku-4-5"])
	require.Equal(t, map[string]any{"model": "unpriced", "offered": true, "base": nil, "group_rule": nil,
		"list_price": nil, "source": "global"}, rows["unpriced"])

	// Rules saved in one request, one on a model no account of the group
	// declares (gpt-5 has a global price): it is listed, not offered.
	_, rows = e.modelPrices(e.open,
		patch("claude-haiku-4-5", multiplier("1.20")),
		patch("web-search", custom("visual", requestTier(0.008))),
		patch("gpt-5", multiplier("0.5")))
	require.Contains(t, e.bus.sent, core.ChannelConfigChanged+` {"key":"price_rules"}`)
	row := rows["claude-haiku-4-5"]
	require.Equal(t, map[string]any{"kind": "multiplier", "multiplier": "1.2"}, row["group_rule"])
	require.Equal(t, "group", row["source"])
	// A multiplier goes beside the global price; nothing of it is scaled.
	require.Equal(t, timesPrice(haiku, "1.2"), row["list_price"])
	require.Equal(t, haiku, row["base"])
	row = rows["web-search"]
	require.Equal(t, shown(custom("visual", requestTier(0.008)), `tier("base", flat(0.008))`), row["group_rule"])
	require.Equal(t, map[string]any{"mode": "visual", "config": requestTier(0.008), "expression": `tier("base", flat(0.008))`}, row["list_price"])
	require.Equal(t, "global", rows["claude-sonnet-4-5"]["source"])
	gpt := rows["gpt-5"]
	require.Equal(t, []any{false, "group", multiplier("0.5")}, []any{gpt["offered"], gpt["source"], gpt["group_rule"]})
	require.Equal(t, timesPrice(gpt["base"], "0.5"), gpt["list_price"])
	require.Len(t, rows, 5)
	// The other group has none of them.
	_, rows = e.modelPrices(e.private)
	require.Len(t, rows, 4)
	require.Equal(t, "global", rows["claude-haiku-4-5"]["source"])

	// The expression of a custom price is in the history, where a settlement
	// retry reads it by its hash.
	var stored, inHistory int
	require.NoError(t, e.db.SQL.QueryRowContext(ctx, `SELECT count(*), count(h.expr_hash) FROM price_rules r
		LEFT JOIN model_price_history h ON h.expr_hash = r.expr_hash AND h.expression = 'v1:' || r.expression
		WHERE r.kind = 'custom'`).Scan(&stored, &inHistory))
	require.Equal(t, 1, stored)
	require.Equal(t, 1, inHistory)

	// A rule is replaced in place and removed by null; removing one that is
	// not there, on any model, is fine.
	_, rows = e.modelPrices(e.open,
		patch("claude-haiku-4-5", custom("visual", oneTierConfig(map[string]any{"p": 0.8, "c": 4}))),
		patch("web-search", nil),
		patch("no-such-model", nil))
	own := oneTierConfig(map[string]any{"p": 0.8, "c": 4.0})
	require.Equal(t, shown(custom("visual", own), `tier("base", p*0.8 + c*4)`), rows["claude-haiku-4-5"]["group_rule"])
	require.Equal(t, "global", rows["web-search"]["source"])
	require.Equal(t, 2, int(e.one(`SELECT count(*) FROM price_rules`)))

	// The global price changes its shape: the custom price still replaces
	// it, and is due by the token although the global price is by the request.
	haikuID := e.one(`SELECT id FROM model_prices WHERE model = 'claude-haiku-4-5'`)
	e.mustCall(e.admin, 200, "PATCH", "/prices/"+itoa(haikuID), map[string]any{"mode": "visual", "config": requestTier(1)})
	_, rows = e.modelPrices(e.open)
	row = rows["claude-haiku-4-5"]
	require.Equal(t, "group", row["source"])
	require.Equal(t, map[string]any{"mode": "visual", "config": own, "expression": `tier("base", p*0.8 + c*4)`}, row["list_price"])
	r, err := e.svc.Resolve(ctx, core.PriceQuery{Model: "claude-haiku-4-5", GroupID: e.open})
	require.NoError(t, err)
	require.Equal(t, core.PriceSourceGroup, r.Source)
	require.Equal(t, `tier("base", flat(1))`, r.Base.Expression)
	require.False(t, r.PerRequest)
	require.Nil(t, r.Scale)

	// A rule whose global price goes stays listed with it, priced at nothing.
	e.mustCall(e.admin, 200, "PATCH", "/prices/"+itoa(e.one(`SELECT id FROM model_prices WHERE model = 'gpt-5'`)), map[string]any{"enabled": false})
	_, rows = e.modelPrices(e.open)
	require.Equal(t, map[string]any{"model": "gpt-5", "offered": false, "base": nil, "group_rule": multiplier("0.5"),
		"list_price": nil, "source": "global"}, rows["gpt-5"])

	// Not found and malformed.
	e.mustCall(e.admin, 404, "GET", "/groups/999999/model-prices", nil)
	e.mustCall(e.admin, 400, "GET", "/groups/open/model-prices", nil)
	e.mustCall(e.admin, 404, "PUT", "/groups/999999/model-prices", map[string]any{"items": []any{}})

	// The old console's user group prices fail instead of pricing the group.
	path := "/groups/" + itoa(e.open) + "/model-prices"
	e.mustCall(e.admin, 400, "PUT", path+"?segment_id=1", map[string]any{"items": []any{patch("web-search", multiplier("0.9"))}})
	e.mustCall(e.admin, 400, "PUT", path, map[string]any{"items": []any{map[string]any{"model": "web-search", "segment_id": 1, "rule": multiplier("0.9")}}})
	require.Zero(t, e.one(`SELECT count(*) FROM price_rules WHERE model = 'web-search'`))
}

func TestGroupModelPricesValidation(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	path := "/groups/" + itoa(e.open) + "/model-prices"
	valid := patch("web-search", multiplier("3"))
	cases := []struct {
		name    string
		item    map[string]any
		message string
	}{
		{"no global price", patch("unpriced", multiplier("1")), "no global price"},
		{"no global price, no account", patch("nobody-has-it", custom("visual", requestTier(1))), "no global price"},
		{"unknown kind", patch("claude-haiku-4-5", map[string]any{"kind": "fixed"}), "a multiplier or a custom price"},
		{"negative multiplier", patch("claude-haiku-4-5", multiplier("-1")), "multiplier must be"},
		{"multiplier too large", patch("claude-haiku-4-5", multiplier("1000000")), "below 1000000"},
		{"multiplier too fine", patch("claude-haiku-4-5", multiplier("0.00001")), "at most 4 decimals"},
		{"multiplier that is no number", patch("claude-haiku-4-5", multiplier("much")), "multiplier must be"},
		{"custom in an old mode", patch("claude-haiku-4-5", custom("per_request", map[string]any{"price": 1})), "visual or expression"},
		{"custom expression that does not compile", patch("claude-haiku-4-5", written(`tier("base", p*`)), "invalid custom price"},
		{"custom expression of an unknown variable", patch("claude-haiku-4-5", written(`tier("base", nope*2)`)), "invalid custom price"},
		{"negative price", patch("claude-haiku-4-5", custom("visual", oneTierConfig(map[string]any{"p": 1, "c": -2}))), "config.tiers[0].c"},
		{"old flat prices", patch("claude-haiku-4-5", custom("visual", map[string]any{"p": 1, "c": 2})), `unknown field`},
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
	// A custom price of any shape replaces a global price of any shape.
	_, rows := e.modelPrices(e.open,
		patch("claude-sonnet-4-5", custom("visual", requestTier(1))),
		patch("claude-haiku-4-5", written(" tier(\"base\", p*2 + c*4)\n")))
	require.Equal(t, shown(custom("visual", requestTier(1.0)), `tier("base", flat(1))`), rows["claude-sonnet-4-5"]["group_rule"])
	require.Equal(t, map[string]any{"kind": "custom", "mode": "expression", "config": map[string]any{}, "expression": `tier("base", p*2 + c*4)`},
		rows["claude-haiku-4-5"]["group_rule"])
	// 0 and 1 are multipliers like any other.
	_, rows = e.modelPrices(e.open, patch("claude-haiku-4-5", multiplier("0")), patch("claude-sonnet-4-5", multiplier("1")))
	require.Equal(t, map[string]any{"kind": "multiplier", "multiplier": "0"}, rows["claude-haiku-4-5"]["group_rule"])
	require.Equal(t, timesPrice(rows["claude-sonnet-4-5"]["base"], "1"), rows["claude-sonnet-4-5"]["list_price"])
}

// A group is deleted while a rule of it is being saved: the write waits for
// the delete and, once that commits, fails on the foreign key. That is a
// conflict to retry, and nothing of the request is saved.
func TestGroupModelPricesConflict(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	ctx := context.Background()
	leaving := e.one(`INSERT INTO groups (name) VALUES ('leaving') RETURNING id`)
	tx, err := testutil.Begin(ctx, e.db)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = store.SQLTx(tx).ExecContext(ctx, `DELETE FROM groups WHERE id = $1`, leaving)
	require.NoError(t, err)

	type answer struct {
		code int
		body map[string]any
	}
	saved := make(chan answer, 1)
	go func() {
		code, body := e.call(e.admin, "PUT", "/groups/"+itoa(leaving)+"/model-prices", map[string]any{"items": []any{
			patch("web-search", multiplier("3")), patch("claude-haiku-4-5", multiplier("2"))}})
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

// Who may read and change the model prices of a group.
func TestGroupModelPricesPermissions(t *testing.T) {
	const reader, manager, outsider = 1001, 1002, 1003
	e := newPricingEnv(t, limited{perms: map[int64][]string{
		reader: {"group:read"}, manager: {"group:read", "group:manage"}, outsider: {"price:read", "user:read"}}})
	path := "/groups/" + itoa(e.open) + "/model-prices"
	items := map[string]any{"items": []any{patch("web-search", multiplier("0.9"))}}

	e.mustCall(outsider, 403, "GET", path, nil)
	e.mustCall(outsider, 403, "PUT", path, items)
	e.mustCall(reader, 403, "PUT", path, items)
	require.Zero(t, e.one(`SELECT count(*) FROM price_rules`))

	e.mustCall(reader, 200, "GET", path, nil)
	e.mustCall(manager, 200, "PUT", path, items)
	require.Equal(t, 1, int(e.one(`SELECT count(*) FROM price_rules WHERE updated_by = $1`, manager)))
}

// GET /pricing/catalog: the enabled global prices by model, as the pricing
// table shows them, to group:read.
func TestPricingCatalog(t *testing.T) {
	const reader, outsider = 1001, 1002
	e := newPricingEnv(t, limited{perms: map[int64][]string{reader: {"group:read"}, outsider: {"price:read", "segment:read"}}})
	e.mustCall(outsider, 403, "GET", "/pricing/catalog", nil)
	e.exec(`UPDATE model_prices SET enabled = false WHERE model = 'gpt-5'`)
	e.svc.invalidate()

	resp := e.mustCall(reader, 200, "GET", "/pricing/catalog", nil)
	var models []string
	for _, row := range resp["data"].([]any) {
		models = append(models, row.(map[string]any)["model"].(string))
	}
	require.Equal(t, []string{"claude-haiku-4-5", "claude-sonnet-4-5", "web-search"}, models)
	_, rows := e.modelPrices(e.open)
	require.Equal(t, map[string]any{"model": "web-search", "price": rows["web-search"]["base"]}, resp["data"].([]any)[2])
}

// A rule prices requests as soon as it is saved, and a price handed out is
// the caller's to change.
func TestResolveFollowsRules(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	ctx := context.Background()
	q := core.PriceQuery{Model: "claude-haiku-4-5", GroupID: e.open}
	resolve := func(q core.PriceQuery) *core.PriceRule {
		t.Helper()
		r, err := e.svc.Resolve(ctx, q)
		require.NoError(t, err)
		return r
	}

	// Without a group, and without a rule, it is the global price, field
	// for field what Resolve gave before there were rules.
	global := resolve(core.PriceQuery{Model: q.Model})
	var want core.PriceRule
	require.NoError(t, e.db.SQL.QueryRowContext(ctx, `SELECT id, model, mode, expression, expr_version, expr_hash FROM model_prices WHERE model = $1`, q.Model).
		Scan(&want.ID, &want.Model, &want.Mode, &want.Expression, &want.ExprVersion, &want.ExprHash))
	require.Equal(t, &want, global)
	require.Equal(t, global, resolve(q))

	e.modelPrices(e.open, patch(q.Model, multiplier("1.2")))
	r := resolve(q)
	require.Equal(t, core.PriceSourceGroup, r.Source)
	require.Equal(t, "1.2", r.Scale.String())
	require.Equal(t, "2.4", r.Multiplier(*dec("2")).String())
	want.Source, r.Scale = core.PriceSourceGroup, nil
	require.Equal(t, &want, r)
	require.Equal(t, global, resolve(core.PriceQuery{Model: q.Model}))
	require.Equal(t, global, resolve(core.PriceQuery{Model: q.Model, GroupID: e.private}))

	// The answer is a copy.
	scaled := resolve(q)
	*scaled.Scale = *dec("9")
	require.Equal(t, "1.2", resolve(q).Scale.String())

	e.modelPrices(e.open, patch(q.Model, custom("visual", oneTierConfig(map[string]any{"p": 0.8, "c": 4}))))
	r = resolve(q)
	require.Equal(t, core.PriceSourceGroup, r.Source)
	require.Nil(t, r.Scale)
	require.Equal(t, &core.PriceBase{Expression: global.Expression, ExprHash: global.ExprHash}, r.Base)
	require.Equal(t, `tier("base", p*0.8 + c*4)`, r.Expression)
	require.Equal(t, []any{global.ID, "visual", 1}, []any{r.ID, r.Mode, r.ExprVersion})
	var frozen string
	require.NoError(t, e.db.SQL.QueryRowContext(ctx, `SELECT expression FROM model_price_history WHERE expr_hash = $1`, r.ExprHash).Scan(&frozen))
	require.Contains(t, frozen, r.Expression)
	r.Base.Expression, r.Expression = "changed", "changed"
	require.Equal(t, global.Expression, resolve(q).Base.Expression)

	// A rule removed behind the service's back (another node) applies until
	// the snapshot is dropped.
	e.exec(`DELETE FROM price_rules WHERE group_id = $1`, e.open)
	require.Equal(t, core.PriceSourceGroup, resolve(q).Source)
	e.svc.invalidate()
	require.Empty(t, resolve(q).Source)

	// Without a global price there is nothing a rule could apply to.
	e.modelPrices(e.open, patch(q.Model, multiplier("2")))
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
	e.modelPrices(e.private,
		patch("claude-haiku-4-5", multiplier("1.2")),
		patch("web-search", custom("visual", requestTier(0.008))),
		patch("claude-sonnet-4-5", multiplier("0.5")))

	// A member of vip in the private group: granted, at vip's own rate, at
	// the group's rule: 6 x 1.2 = 7.2, x 0.5 = 3.6.
	out := quote("user_id", e.member, e.private, "claude-haiku-4-5")
	require.Equal(t, map[string]any{"id": float64(e.vip), "kind": "named", "name": "vip"}, out["segment"])
	require.Equal(t, []any{true, "grant", "group"}, []any{out["accessible"], out["access_source"], out["price_source"]})
	require.Equal(t, map[string]any{"group": multiplier("1.2")}, out["price_levels"])
	require.Equal(t, map[string]any{"value": "0.5", "source": "segment", "group_default": "2", "discount": "0.8"}, out["rate"])
	require.Equal(t, []any{"0.5", "7.2", "3.6"}, []any{out["rate_override"], out["list_cost"], out["cost"]})
	require.Equal(t, 1.0, out["base"].(map[string]any)["config"].(map[string]any)["tiers"].([]any)[0].(map[string]any)["p"])
	require.NotContains(t, out["base"], "multiplier")
	require.Equal(t, timesPrice(out["base"], "1.2"), out["list_price"])

	// The same for the segment itself; the group's custom price applies per request.
	out = quote("segment_id", e.vip, e.private, "web-search")
	require.Equal(t, "group", out["price_source"])
	require.Equal(t, map[string]any{"group": shown(custom("visual", requestTier(0.008)), `tier("base", flat(0.008))`)}, out["price_levels"])
	require.Equal(t, []any{"0.008", "0.004"}, []any{out["list_cost"], out["cost"]})
	require.Equal(t, requestTier(0.01), out["base"].(map[string]any)["config"])
	require.Equal(t, requestTier(0.008), out["list_price"].(map[string]any)["config"])

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
	require.Equal(t, []any{"group", "14.25", "28.5"}, []any{out["price_source"], out["list_cost"], out["cost"]})
	require.Equal(t, timesPrice(out["base"], "0.5"), out["list_price"])
	require.Equal(t, map[string]any{"id": float64(e.personal), "kind": "personal", "name": "solo@example.com", "user_id": float64(e.solo)}, out["segment"])

	// A model without a global price cannot be billed.
	out = quote("user_id", e.member, e.private, "unpriced")
	require.Equal(t, []any{nil, nil, "global", "0", "0"}, []any{out["base"], out["list_price"], out["price_source"], out["list_cost"], out["cost"]})

	// A disabled group serves nobody, as the gateway refuses it
	// (group_disabled); the price is quoted all the same.
	e.exec(`UPDATE groups SET status = 'disabled' WHERE id = $1`, e.private)
	out = quote("user_id", e.member, e.private, "claude-haiku-4-5")
	require.Equal(t, []any{false, "disabled", "7.2", "3.6"}, []any{out["accessible"], out["access_source"], out["list_cost"], out["cost"]})
	e.exec(`UPDATE groups SET status = 'active' WHERE id = $1`, e.private)

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
	e.svc.invalidate()
	after := data(e.mustCall(e.admin, 200, "POST", "/pricing/quote", body))
	require.Equal(t, []any{"group", "12"}, []any{after["price_source"], after["list_cost"]})
	require.Equal(t, multiplier("2"), after["price_levels"].(map[string]any)["group"])
	require.Equal(t, timesPrice(after["base"], "2"), after["list_price"])
}

// GET /me/prices?group_id=: what the group provides and can bill, at the
// caller's list price and rate there.
func TestMyPricesInGroup(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	e.modelPrices(e.private,
		patch("claude-haiku-4-5", multiplier("1.2")),
		patch("web-search", custom("visual", requestTier(0.008))))
	e.modelPrices(e.open, patch("web-search", multiplier("0")))
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
	require.Equal(t, map[string]any{"model": "claude-haiku-4-5", "mode": "visual", "source": "group", "multiplier": "1.2",
		"config":     oneTierConfig(map[string]any{"p": 1.0, "c": 5.0, "cr": 0.1, "cc": 1.25, "cc1h": 2.0}),
		"expression": `tier("base", p*1 + c*5 + cr*0.1 + cc*1.25 + cc1h*2)`}, rows["claude-haiku-4-5"])
	require.Equal(t, map[string]any{"model": "web-search", "mode": "visual", "source": "group",
		"config": requestTier(0.008), "expression": `tier("base", flat(0.008))`}, rows["web-search"])
	require.Equal(t, "global", rows["claude-sonnet-4-5"]["source"])
	require.NotContains(t, rows["claude-sonnet-4-5"], "multiplier")

	// The public group, at the discounted rate, at its own rules: the same
	// prices for everyone.
	d = data(e.mustCall(e.member, 200, "GET", "/me/prices?group_id="+itoa(e.open), nil))
	require.Equal(t, []any{"1.2", "discount"}, []any{d["rate_multiplier"], d["rate_source"]})
	require.Equal(t, "global", byModel(d)["claude-haiku-4-5"]["source"])
	free := map[string]any{"model": "web-search", "mode": "visual", "source": "group", "multiplier": "0",
		"config": requestTier(0.01), "expression": `tier("base", flat(0.01))`}
	require.Equal(t, free, byModel(d)["web-search"])
	require.Equal(t, free, byModel(data(e.mustCall(e.solo, 200, "GET", "/me/prices?group_id="+itoa(e.open), nil)))["web-search"])

	// Without a group the prices are the global ones and no rule shows.
	d = data(e.mustCall(e.member, 200, "GET", "/me/prices", nil))
	require.Nil(t, d["group"])
	require.NotContains(t, d, "rate_source")
	rows = byModel(d)
	require.Len(t, rows, 4)
	both := []any{map[string]any{"id": float64(e.open), "name": "open"}, map[string]any{"id": float64(e.private), "name": "private"}}
	require.Equal(t, map[string]any{"model": "web-search", "mode": "visual", "config": requestTier(0.01),
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
	e.modelPrices(e.open, patch("claude-sonnet-4-5", multiplier("2")))
	sonnet := byModel(data(e.mustCall(e.member, 200, "GET", "/me/prices?group_id="+itoa(e.open), nil)))["claude-sonnet-4-5"]
	require.Equal(t, []any{"group", "2", global}, []any{sonnet["source"], sonnet["multiplier"], sonnet["expression"]})
}

// GET /key/prices: the list prices of the key's group, in a form a
// downstream sup2api compiles to the same prices, and the rate of the key's
// owner.
func TestKeyPricesFollowRules(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	e.modelPrices(e.private,
		patch("claude-haiku-4-5", custom("visual", oneTierConfig(map[string]any{"p": 0.8, "c": 4}))),
		patch("claude-sonnet-4-5", multiplier("0.5")),
		patch("web-search", multiplier("3")))
	e.modelPrices(e.open, patch("claude-haiku-4-5", multiplier("9")))
	prices := func() (string, map[string]*syncPrice) {
		t.Helper()
		e.svc.SetSyncDeps(SyncDeps{Keys: fakeKeys{Group: e.private}})
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

	// The rate is the principal's; only models the key's group allows
	// (claude-*), at the rules of that group.
	rate, got := prices()
	require.Equal(t, "2", rate)
	require.Len(t, got, 2)
	require.Equal(t, `v1:tier("base", p*0.8 + c*4)`, got["claude-haiku-4-5"].Expression)
	require.Equal(t, "visual", got["claude-haiku-4-5"].Mode)
	// A multiplier on a hand-written expression becomes a rule of it.
	require.True(t, strings.HasSuffix(got["claude-sonnet-4-5"].Expression, `) * 0.5`), got["claude-sonnet-4-5"].Expression)

	// Free over a hand-written expression: it times 0. A multiplier on a
	// visual price scales its config; one of 1 is the global price, with
	// nothing appended.
	e.modelPrices(e.private, patch("claude-sonnet-4-5", multiplier("0")), patch("claude-haiku-4-5", multiplier("1.2")))
	_, got = prices()
	require.True(t, strings.HasSuffix(got["claude-sonnet-4-5"].Expression, `) * 0`), got["claude-sonnet-4-5"].Expression)
	require.Equal(t, "0", costOf(t, got["claude-sonnet-4-5"].Expression))
	require.Equal(t, `v1:tier("base", p*1.2 + c*6 + cr*0.12 + cc*1.5 + cc1h*2.4)`, got["claude-haiku-4-5"].Expression)
	e.modelPrices(e.private, patch("claude-haiku-4-5", multiplier("1")))
	_, got = prices()
	require.Equal(t, `v1:tier("base", p*1 + c*5 + cr*0.1 + cc*1.25 + cc1h*2)`, got["claude-haiku-4-5"].Expression)

	// An expression that ends in a line comment takes the multiplier too.
	sonnetID := e.one(`SELECT id FROM model_prices WHERE model = 'claude-sonnet-4-5'`)
	e.mustCall(e.admin, 200, "PATCH", "/prices/"+itoa(sonnetID), map[string]any{"expression": `tier("base", p*3 + c*15) // don't`})
	e.modelPrices(e.private, patch("claude-sonnet-4-5", multiplier("0.5")))
	_, got = prices()
	require.Len(t, got, 2)
	require.Equal(t, "9", costOf(t, got["claude-sonnet-4-5"].Expression))
	e.modelPrices(e.private, patch("claude-sonnet-4-5", nil))
	_, got = prices()
	require.Len(t, got, 2)
	require.Equal(t, "18", costOf(t, got["claude-sonnet-4-5"].Expression))
}

// The pricing tables (GET /groups/:id/model-prices, /me/prices?group_id=,
// /key/prices) read the price snapshot requests are billed by: a rule this
// node has not heard of yet shows nowhere, as it prices nothing yet.
func TestPricingTablesReadTheSnapshot(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	e.svc.SetSyncDeps(SyncDeps{Keys: fakeKeys{Group: e.open}})
	keyPrice := func() string {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/v1/key/prices", nil)
		req.Header.Set("Authorization", "Bearer sk-good")
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code, w.Body.String())
		f, err := parseSup2API(w.Body.Bytes(), false)
		require.NoError(t, err)
		haiku := f.prices["claude-haiku-4-5"]
		require.NoError(t, haiku.compile())
		return haiku.Expression
	}
	sources := func() []any {
		t.Helper()
		_, rows := e.modelPrices(e.open)
		var mine string
		for _, row := range data(e.mustCall(e.member, 200, "GET", "/me/prices?group_id="+itoa(e.open), nil))["prices"].([]any) {
			if row := row.(map[string]any); row["model"] == "claude-haiku-4-5" {
				mine = row["source"].(string)
			}
		}
		return []any{rows["claude-haiku-4-5"]["source"], mine, keyPrice()}
	}
	global := `v1:tier("base", p*1 + c*5 + cr*0.1 + cc*1.25 + cc1h*2)`
	require.Equal(t, []any{"global", "global", global}, sources())

	// Written behind the service's back, as another node would.
	e.exec(`INSERT INTO price_rules (group_id, model, kind, multiplier) VALUES ($1, 'claude-haiku-4-5', 'multiplier', 2)`, e.open)
	require.Equal(t, []any{"global", "global", global}, sources())
	e.svc.invalidate()
	require.Equal(t, []any{"group", "group", `v1:tier("base", p*2 + c*10 + cr*0.2 + cc*2.5 + cc1h*4)`}, sources())
}

// Callers that find the snapshot stale at once share one load of it.
func TestSnapshotLoadsOnce(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	ctx := context.Background()
	e.svc.invalidate()
	var queries atomic.Int64
	require.NoError(t, e.db.Gorm.Callback().Query().After("gorm:query").Register("count_price_loads", func(db *gorm.DB) {
		if db.Statement.Table == "model_prices" {
			queries.Add(1)
		}
	}))
	t.Cleanup(func() { _ = e.db.Gorm.Callback().Query().Remove("count_price_loads") })

	// The first load is held until every caller asked for the snapshot.
	tx, err := testutil.Begin(ctx, e.db)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = store.SQLTx(tx).ExecContext(ctx, `LOCK TABLE model_prices IN ACCESS EXCLUSIVE MODE`)
	require.NoError(t, err)
	const callers = 8
	snaps := make(chan *priceSnapshot, callers)
	for range callers {
		go func() {
			snap, err := e.svc.snapshot(ctx)
			assert.NoError(t, err)
			snaps <- snap
		}()
	}
	require.Eventually(t, func() bool {
		var waiting int
		err := e.db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting)
		return err == nil && waiting > 0
	}, 10*time.Second, 5*time.Millisecond)
	time.Sleep(50 * time.Millisecond) // the other callers join meanwhile
	require.NoError(t, tx.Commit().Error)
	first := <-snaps
	for range callers - 1 {
		require.Same(t, first, <-snaps)
	}
	require.Equal(t, int64(1), queries.Load())
}
