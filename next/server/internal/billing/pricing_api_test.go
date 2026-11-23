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
//	open     public,  rate 1.5, vip at its own rate 1.2
//	private  private, rate 2, granted to vip at its own rate 0.5
//
//	member   in segment vip
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
	e.vip = e.one(`INSERT INTO segments (name) VALUES ('vip') RETURNING id`)
	e.personal = e.one(`INSERT INTO segments (kind, user_id) VALUES ('personal', $1) RETURNING id`, e.solo)
	e.exec(`UPDATE users SET segment_id = $2 WHERE id = $1`, e.member, e.vip)
	e.exec(`UPDATE users SET segment_id = $2 WHERE id = $1`, e.solo, e.personal)
	e.open = e.one(`INSERT INTO groups (name, rate_multiplier) VALUES ('open', 1.5) RETURNING id`)
	e.private = e.one(`INSERT INTO groups (name, visibility, rate_multiplier) VALUES ('private', 'restricted', 2) RETURNING id`)
	e.exec(`INSERT INTO segment_groups (segment_id, group_id, granted, rate_override) VALUES ($1, $2, true, 0.5), ($1, $3, false, 1.2)`,
		e.vip, e.private, e.open)
	account := e.one(`INSERT INTO accounts (name, plugin_key, type, credentials_enc, models)
		VALUES ('a', 'anthropic', 'apikey', '\x00'::bytea, $1) RETURNING id`,
		[]string{"claude-haiku-4-5", "claude-sonnet-4-5", "web-search", "unpriced"})
	e.exec(`INSERT INTO account_groups (account_id, group_id) VALUES ($1, $2), ($1, $3)`, account, e.open, e.private)
	seedPrices(e.env, e.admin)
	e.mustCall(e.admin, 201, "POST", "/prices", map[string]any{"model": "web-search", "mode": "visual", "config": requestTier(0.01)})
	e.mustCall(e.admin, 201, "POST", "/prices", map[string]any{"model": "gpt-5", "mode": "visual", "config": requestTier(1)})
	return e
}

func custom(mode string, config map[string]any) map[string]any {
	return map[string]any{"mode": mode, "config": config}
}

// written is a custom price written as an expression.
func written(expression string) map[string]any {
	return map[string]any{"mode": "expression", "expression": expression}
}

// shown is a custom price as the API shows it: with its expression.
func shown(rule map[string]any, expression string) map[string]any {
	out := maps.Clone(rule)
	out["expression"] = expression
	return out
}

// patch is an item of model_prices.
func patch(model string, rule map[string]any) map[string]any {
	return map[string]any{"model": model, "rule": rule}
}

// rated is an item of model_rates; nil removes the rate.
func rated(model string, rate any) map[string]any {
	return map[string]any{"model": model, "rate": rate}
}

// writeGroup has billing's part of a group write (POST/PATCH /groups, core.
// GroupWritePart) check body and write it to the group in one transaction,
// then run what follows the commit, as the group module does; it returns the
// field errors instead when there are any.
func (e *pricingEnv) writeGroup(group int64, body map[string]any) []core.FieldError {
	e.t.Helper()
	ctx := core.WithUserID(context.Background(), e.admin)
	raw := map[string]json.RawMessage{}
	for k, v := range body {
		b, err := json.Marshal(v)
		require.NoError(e.t, err)
		raw[k] = b
	}
	w, fields, err := e.svc.PrepareGroupWrite(ctx, group, raw)
	require.NoError(e.t, err)
	if fields != nil {
		require.Nil(e.t, w.Apply)
		return fields
	}
	require.NoError(e.t, e.db.Tx(ctx, func(tx *gorm.DB) error { return w.Apply(ctx, tx, group) }))
	w.AfterCommit(ctx, group)
	return nil
}

// modelPrices reads /groups/:id/model-prices and returns the answer with its
// rows by model.
func (e *pricingEnv) modelPrices(group int64) (map[string]any, map[string]map[string]any) {
	e.t.Helper()
	out := data(e.mustCall(e.admin, 200, "GET", "/groups/"+itoa(group)+"/model-prices", nil))
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

// haikuGlobal is the global price of claude-haiku-4-5 in a pricing env.
var haikuGlobal = map[string]any{"mode": "visual", "config": oneTierConfig(map[string]any{"p": 1.0, "c": 5.0, "cr": 0.1, "cc": 1.25, "cc1h": 2.0}),
	"expression": `tier("base", p*1 + c*5 + cr*0.1 + cc*1.25 + cc1h*2)`}

// The price table of a group: what it provides and what has a price or a
// rate of the group, as written with the group and as the global prices
// change.
func TestGroupModelPrices(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	checkPlainModelPrices(t, e)
	checkSavedModelPrices(t, e)
	checkReplacedModelPrices(t, e)
	checkModelPricesFollowGlobal(t, e)
	// Not found and malformed.
	e.mustCall(e.admin, 404, "GET", "/groups/999999/model-prices", nil)
	e.mustCall(e.admin, 400, "GET", "/groups/open/model-prices", nil)
}

// Without prices or rates: the models the group provides at their global
// prices, at the group's base rate.
func checkPlainModelPrices(t *testing.T, e *pricingEnv) {
	out, rows := e.modelPrices(e.open)
	require.Equal(t, map[string]any{"id": float64(e.open), "name": "open"}, out["group"])
	require.Equal(t, map[string]any{"value": "1.5", "source": "group", "group_default": "1.5"}, out["rate"])
	var models []string
	for _, row := range out["models"].([]any) {
		models = append(models, row.(map[string]any)["model"].(string))
	}
	require.Equal(t, []string{"claude-haiku-4-5", "claude-sonnet-4-5", "unpriced", "web-search"}, models)
	require.Equal(t, map[string]any{"model": "claude-haiku-4-5", "offered": true, "base": haikuGlobal, "price": nil, "rate": nil}, rows["claude-haiku-4-5"])
	require.Equal(t, map[string]any{"model": "unpriced", "offered": true, "base": nil, "price": nil, "rate": nil}, rows["unpriced"])
}

// Prices and rates saved in one write, a rate on a model no account of the
// group declares (gpt-5 has a global price): it is listed, not offered. The
// expression of a model price is in the history, where a settlement retry
// reads it by its hash.
func checkSavedModelPrices(t *testing.T, e *pricingEnv) {
	require.Nil(t, e.writeGroup(e.open, map[string]any{
		"model_prices": []any{patch("web-search", custom("visual", requestTier(0.008)))},
		"model_rates":  []any{rated("claude-haiku-4-5", "1.20"), rated("gpt-5", "0.5")}}))
	require.Contains(t, e.bus.sent, core.ChannelConfigChanged+` {"key":"price_rules"}`)
	_, rows := e.modelPrices(e.open)
	require.Equal(t, map[string]any{"model": "claude-haiku-4-5", "offered": true, "base": haikuGlobal, "price": nil, "rate": "1.2"}, rows["claude-haiku-4-5"])
	require.Equal(t, shown(custom("visual", requestTier(0.008)), `tier("base", flat(0.008))`), rows["web-search"]["price"])
	require.Nil(t, rows["web-search"]["rate"])
	require.Equal(t, []any{false, nil, "0.5"}, []any{rows["gpt-5"]["offered"], rows["gpt-5"]["price"], rows["gpt-5"]["rate"]})
	require.Len(t, rows, 5)
	// The other group has none of them.
	_, rows = e.modelPrices(e.private)
	require.Len(t, rows, 4)
	require.Equal(t, []any{nil, nil}, []any{rows["claude-haiku-4-5"]["price"], rows["claude-haiku-4-5"]["rate"]})
	require.Equal(t, int64(1), e.one(`SELECT count(*) FROM price_rules r
		JOIN model_price_history h ON h.expr_hash = r.expr_hash AND h.expression = 'v1:' || r.expression`))
}

// Replaced in place and removed by null; removing one that is not there, on
// any model, is fine.
func checkReplacedModelPrices(t *testing.T, e *pricingEnv) {
	require.Nil(t, e.writeGroup(e.open, map[string]any{
		"model_prices": []any{patch("claude-haiku-4-5", custom("visual", oneTierConfig(map[string]any{"p": 0.8, "c": 4}))),
			patch("web-search", nil), patch("no-such-model", nil)},
		"model_rates": []any{rated("claude-haiku-4-5", nil), rated("gpt-5", "0.75"), rated("no-such-model", nil)}}))
	_, rows := e.modelPrices(e.open)
	own := oneTierConfig(map[string]any{"p": 0.8, "c": 4.0})
	require.Equal(t, shown(custom("visual", own), `tier("base", p*0.8 + c*4)`), rows["claude-haiku-4-5"]["price"])
	require.Equal(t, []any{nil, nil, "0.75"}, []any{rows["claude-haiku-4-5"]["rate"], rows["web-search"]["price"], rows["gpt-5"]["rate"]})
	require.Equal(t, []int64{1, 1}, []int64{e.one(`SELECT count(*) FROM price_rules`), e.one(`SELECT count(*) FROM group_model_rates`)})
	require.Equal(t, e.admin, e.one(`SELECT updated_by FROM group_model_rates`))
}

// The global price changes its shape: the model price still replaces it,
// and is due by the token although the global price is by the request. A
// rate whose global price goes stays listed with it.
func checkModelPricesFollowGlobal(t *testing.T, e *pricingEnv) {
	haikuID := e.one(`SELECT id FROM model_prices WHERE model = 'claude-haiku-4-5'`)
	e.mustCall(e.admin, 200, "PATCH", "/prices/"+itoa(haikuID), map[string]any{"mode": "visual", "config": requestTier(1)})
	r, err := priceOf(context.Background(), e.svc, core.PriceQuery{Model: "claude-haiku-4-5", GroupID: e.open})
	require.NoError(t, err)
	require.Equal(t, []any{core.PriceSourceGroup, `tier("base", flat(1))`, false}, []any{r.Source, r.Base.Expression, r.PerRequest})

	e.mustCall(e.admin, 200, "PATCH", "/prices/"+itoa(e.one(`SELECT id FROM model_prices WHERE model = 'gpt-5'`)), map[string]any{"enabled": false})
	_, rows := e.modelPrices(e.open)
	require.Equal(t, map[string]any{"model": "gpt-5", "offered": false, "base": nil, "price": nil, "rate": "0.75"}, rows["gpt-5"])
}

// Who may read the model prices of a group; they are written with the group
// (POST/PATCH /groups), there is no PUT of their own.
func TestGroupModelPricesPermissions(t *testing.T) {
	const reader, outsider = 1001, 1002
	e := newPricingEnv(t, limited{perms: map[int64][]string{reader: {"group:read"}, outsider: {"price:read", "user:read"}}})
	path := "/groups/" + itoa(e.open) + "/model-prices"
	e.mustCall(outsider, 403, "GET", path, nil)
	e.mustCall(reader, 200, "GET", path, nil)
	req := httptest.NewRequest("PUT", "/api/v1"+path, strings.NewReader(`{"items": []}`))
	req.Header.Set("Authorization", "Bearer "+itoa(e.admin))
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	require.Equal(t, 404, w.Code)
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

// mustPrice is the list price of q.
func mustPrice(t *testing.T, e *pricingEnv, q core.PriceQuery) *core.PriceRule {
	t.Helper()
	r, err := priceOf(context.Background(), e.svc, q)
	require.NoError(t, err)
	return r
}

// A model price prices requests as soon as it is saved, and a price handed
// out is the caller's to change.
func TestResolveFollowsRules(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	q := core.PriceQuery{Model: "claude-haiku-4-5", GroupID: e.open}
	// Without a group, and without a rule, it is the global price, field
	// for field what Resolve gave before there were rules.
	global := mustPrice(t, e, core.PriceQuery{Model: q.Model})
	var want core.PriceRule
	require.NoError(t, e.db.SQL.QueryRowContext(t.Context(), `SELECT id, model, mode, expression, expr_version, expr_hash FROM model_prices WHERE model = $1`, q.Model).
		Scan(&want.ID, &want.Model, &want.Mode, &want.Expression, &want.ExprVersion, &want.ExprHash))
	require.Equal(t, &want, global)
	require.Equal(t, global, mustPrice(t, e, q))
	checkResolvedGroupPrice(t, e, q, global)
	checkResolveStaleness(t, e, q)
}

// The group's price, in no other group; a copy the caller changes changes
// nothing.
func checkResolvedGroupPrice(t *testing.T, e *pricingEnv, q core.PriceQuery, global *core.PriceRule) {
	require.Nil(t, e.writeGroup(e.open, map[string]any{"model_prices": []any{patch(q.Model, custom("visual", oneTierConfig(map[string]any{"p": 0.8, "c": 4})))}}))
	r := mustPrice(t, e, q)
	require.Equal(t, core.PriceSourceGroup, r.Source)
	require.Nil(t, r.Scale)
	require.Equal(t, &core.PriceBase{Expression: global.Expression, ExprHash: global.ExprHash}, r.Base)
	require.Equal(t, `tier("base", p*0.8 + c*4)`, r.Expression)
	require.Equal(t, []any{global.ID, "visual", 1}, []any{r.ID, r.Mode, r.ExprVersion})
	var frozen string
	require.NoError(t, e.db.SQL.QueryRowContext(t.Context(), `SELECT expression FROM model_price_history WHERE expr_hash = $1`, r.ExprHash).Scan(&frozen))
	require.Contains(t, frozen, r.Expression)
	r.Base.Expression, r.Expression = "changed", "changed"
	require.Equal(t, global.Expression, mustPrice(t, e, q).Base.Expression)
	require.Equal(t, global, mustPrice(t, e, core.PriceQuery{Model: q.Model, GroupID: e.private}))
}

// A rule removed behind the service's back (another node) applies until the
// snapshot is dropped. A group's price stands without a global price, with
// neither id nor base; without either the model is refused.
func checkResolveStaleness(t *testing.T, e *pricingEnv, q core.PriceQuery) {
	e.exec(`DELETE FROM price_rules WHERE group_id = $1`, e.open)
	require.Equal(t, core.PriceSourceGroup, mustPrice(t, e, q).Source)
	e.svc.invalidate()
	require.Empty(t, mustPrice(t, e, q).Source)

	require.Nil(t, e.writeGroup(e.open, map[string]any{"model_prices": []any{patch(q.Model, custom("visual", oneTierConfig(map[string]any{"p": 0.8, "c": 4})))}}))
	e.exec(`UPDATE model_prices SET enabled = false WHERE model = $1`, q.Model)
	e.svc.invalidate()
	r := mustPrice(t, e, q)
	require.Equal(t, []any{q.Model, core.PriceSourceGroup, int64(0), (*core.PriceBase)(nil), `tier("base", p*0.8 + c*4)`},
		[]any{r.Model, r.Source, r.ID, r.Base, r.Expression})
	require.Nil(t, e.writeGroup(e.open, map[string]any{"model_prices": []any{patch(q.Model, nil)}}))
	_, err := priceOf(context.Background(), e.svc, q)
	require.Equal(t, "model_price_not_configured", core.AsError(err).Code)
}

// Callers that find the snapshot stale at once share one load of it.
func TestSnapshotLoadsOnce(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	ctx := context.Background()
	e.svc.invalidate()
	queries := countPriceLoads(t, e)

	// The first load is held until every caller asked for the snapshot.
	tx, err := testutil.Begin(ctx, e.db)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = store.SQLTx(tx).ExecContext(ctx, `LOCK TABLE model_prices IN ACCESS EXCLUSIVE MODE`)
	require.NoError(t, err)
	const callers = 8
	snaps := askSnapshots(t, e, callers)
	wait, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	auditBillingWaitLocks(t, wait, e.db, 1)
	time.Sleep(50 * time.Millisecond) // the other callers join meanwhile
	require.NoError(t, tx.Commit().Error)
	first := <-snaps
	for range callers - 1 {
		require.Same(t, first, <-snaps)
	}
	require.Equal(t, int64(1), queries.Load())
}

// countPriceLoads counts the queries of model_prices from now on.
func countPriceLoads(t *testing.T, e *pricingEnv) *atomic.Int64 {
	var queries atomic.Int64
	require.NoError(t, e.db.Gorm.Callback().Query().After("gorm:query").Register("count_price_loads", func(db *gorm.DB) {
		if db.Statement.Table == "model_prices" {
			queries.Add(1)
		}
	}))
	t.Cleanup(func() { _ = e.db.Gorm.Callback().Query().Remove("count_price_loads") })
	return &queries
}

// askSnapshots asks for the snapshot from n callers at once; each sends
// what it got.
func askSnapshots(t *testing.T, e *pricingEnv, n int) <-chan *priceSnapshot {
	snaps := make(chan *priceSnapshot, n)
	for range n {
		go func() {
			snap, err := e.svc.snapshot(context.Background())
			assert.NoError(t, err)
			snaps <- snap
		}()
	}
	return snaps
}
