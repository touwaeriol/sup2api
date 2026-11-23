package billing

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// ownExpr is the private group's own price of claude-own, a model with no
// global price: 6 for a million input and output tokens.
const ownExpr = `tier("base", p*2 + c*4)`

// groupOnlyEnv is a pricing env whose private group also has an account
// that declares no model, and rates on claude-own, which nothing prices
// yet: the group's (3) and vip's there (0.25). vip also rates seg-only,
// which nothing else names.
func groupOnlyEnv(t *testing.T) *pricingEnv {
	t.Helper()
	e := newPricingEnv(t, allowAll{})
	unrestricted := e.one(`INSERT INTO accounts (name, plugin_key, type, credentials_enc, models)
		VALUES ('any', 'anthropic', 'apikey', '\x00'::bytea, '{}') RETURNING id`)
	e.exec(`INSERT INTO account_groups (account_id, group_id) VALUES ($1, $2)`, unrestricted, e.private)
	require.Nil(t, e.writeGroup(e.private, map[string]any{"model_rates": []any{rated(" claude-own ", "3")}}))
	e.exec(`INSERT INTO segment_model_rates (segment_id, group_id, model, rate) VALUES ($1, $2, 'claude-own', 0.25), ($1, $2, 'seg-only', 2)`,
		e.vip, e.private)
	e.svc.RatesChanged(t.Context())
	return e
}

// resolveIn resolves model in group for segment, at the group's base rate 2.
func (e *pricingEnv) resolveIn(group, segment int64, model string) (*core.PriceRule, core.RateLevels, error) {
	return e.svc.Resolve(context.Background(), core.PriceQuery{Model: model, GroupID: group}, segment, core.RateLevels{Group: decimal.NewFromInt(2)})
}

// A price or a rate takes any model, priced anywhere or not. A rate prices
// nothing until the model has a price; the group's own price then stands
// without a global one, in that group alone, and every pricing table of the
// group shows it.
func TestGroupOnlyPrice(t *testing.T) {
	e := groupOnlyEnv(t)
	checkUnpricedRates(t, e)
	require.Nil(t, e.writeGroup(e.private, map[string]any{"model_prices": []any{patch("claude-own", written(ownExpr))}}))
	checkGroupOnlyResolve(t, e)
	checkGroupOnlyTable(t, e)
	checkGroupOnlyQuote(t, e)
	checkGroupOnlyListings(t, e)

	// Removed, the model has no price again.
	require.Nil(t, e.writeGroup(e.private, map[string]any{"model_prices": []any{patch("claude-own", nil)}}))
	_, _, err := e.resolveIn(e.private, e.vip, "claude-own")
	require.Equal(t, "model_price_not_configured", core.AsError(err).Code)
	_, rows := e.modelPrices(e.private)
	require.Equal(t, []any{false, nil, "3"}, []any{rows["claude-own"]["offered"], rows["claude-own"]["price"], rows["claude-own"]["rate"]})
}

// Rates on a model nothing prices: saved and listed, the model refused (it
// is not offered, an account that declares no model serves priced models
// only).
func checkUnpricedRates(t *testing.T, e *pricingEnv) {
	_, _, err := e.resolveIn(e.private, e.vip, "claude-own")
	require.Equal(t, "model_price_not_configured", core.AsError(err).Code)
	_, rows := e.modelPrices(e.private)
	require.Equal(t, map[string]any{"model": "claude-own", "offered": false, "base": nil, "price": nil, "rate": "3"}, rows["claude-own"])
	require.Equal(t, map[string]any{"model": "seg-only", "offered": false, "base": nil, "price": nil, "rate": nil}, rows["seg-only"],
		"a model only a user group rates is listed")
	code, resp := e.call(e.admin, "POST", "/pricing/quote", map[string]any{"user_id": e.admin, "group_id": e.private, "model": "claude-own"})
	require.Equal(t, 400, code)
	field, _ := fieldError(t, resp)
	require.Equal(t, "model", field)
}

// The group's own price, with neither id nor base, at the rates saved
// before; no other group has it.
func checkGroupOnlyResolve(t *testing.T, e *pricingEnv) {
	r, levels, err := e.resolveIn(e.private, e.vip, "claude-own")
	require.NoError(t, err)
	require.Equal(t, []any{"claude-own", "expression", ownExpr, core.PriceSourceGroup, int64(0), (*core.PriceBase)(nil), false},
		[]any{r.Model, r.Mode, r.Expression, r.Source, r.ID, r.Base, r.PerRequest})
	rate, source := levels.Rate()
	require.Equal(t, []string{"0.25", core.RateSourceSegmentModel}, []string{rate.String(), source})
	_, levels, err = e.resolveIn(e.private, e.defaultSegment, "claude-own")
	require.NoError(t, err)
	rate, source = levels.Rate()
	require.Equal(t, []string{"3", core.RateSourceGroupModel}, []string{rate.String(), source})
	_, _, err = e.resolveIn(e.open, e.vip, "claude-own")
	require.Equal(t, "model_price_not_configured", core.AsError(err).Code)
}

// The group's pricing table: offered now, no global price, the group's own.
func checkGroupOnlyTable(t *testing.T, e *pricingEnv) {
	_, rows := e.modelPrices(e.private)
	own := rows["claude-own"]
	require.Equal(t, []any{true, nil, "3"}, []any{own["offered"], own["base"], own["rate"]})
	require.Equal(t, []any{"expression", ownExpr}, []any{own["price"].(map[string]any)["mode"], own["price"].(map[string]any)["expression"]})
	_, rows = e.modelPrices(e.open)
	require.NotContains(t, rows, "claude-own")
}

// A quote of the model: at the group's own price, without a base.
func checkGroupOnlyQuote(t *testing.T, e *pricingEnv) {
	out := e.quoteFor("user_id", e.admin, e.private, "claude-own")
	require.Equal(t, []any{nil, "group", ownExpr, ownExpr}, []any{out["base"], out["price_source"],
		out["list_price"].(map[string]any)["expression"], out["price_levels"].(map[string]any)["group"].(map[string]any)["expression"]})
	require.Equal(t, []any{"group_model", "6", "18"}, []any{out["rate"].(map[string]any)["source"], out["list_cost"], out["cost"]})
	out = e.quoteFor("user_id", e.member, e.private, "claude-own")
	require.Equal(t, []any{"segment_model", "6", "1.5"}, []any{out["rate"].(map[string]any)["source"], out["list_cost"], out["cost"]})
}

// /me/prices?group_id= and /key/prices of the group list the model at its
// own price.
func checkGroupOnlyListings(t *testing.T, e *pricingEnv) {
	row := pricesByModel(data(e.mustCall(e.member, 200, "GET", "/me/prices?group_id="+itoa(e.private), nil)))["claude-own"]
	require.Equal(t, []any{"expression", ownExpr, "group", "0.25", "segment_model"},
		[]any{row["mode"], row["expression"], row["source"], row["rate"], row["rate_source"]})
	require.NotContains(t, pricesByModel(data(e.mustCall(e.member, 200, "GET", "/me/prices", nil))), "claude-own",
		"without a group, global prices only")
	_, got := e.keyPrices(fakeKeys{Group: e.private})
	require.Contains(t, got, "claude-own")
	require.Equal(t, "9", costOf(t, got["claude-own"].Expression), "6 at the model's rate 3 over the key's 2")
	_, got = e.keyPrices(fakeKeys{Group: e.open})
	require.NotContains(t, got, "claude-own")
}
