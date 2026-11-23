package usage

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// priceInGroup saves the fixture group's own price of model as billing does
// with a group write (POST/PATCH /groups model_prices), its expression in
// the price history.
func (f *fixture) priceInGroup(model, expression string) {
	f.t.Helper()
	ctx := core.WithUserID(f.t.Context(), f.user)
	item, err := json.Marshal([]any{map[string]any{"model": model, "rule": map[string]any{"mode": "expression", "expression": expression}}})
	require.NoError(f.t, err)
	w, fields, err := f.bill.PrepareGroupWrite(ctx, f.group, map[string]json.RawMessage{"model_prices": item})
	require.NoError(f.t, err)
	require.Nil(f.t, fields)
	require.NoError(f.t, f.db.Tx(ctx, func(tx *gorm.DB) error { return w.Apply(ctx, tx, f.group) }))
	w.AfterCommit(ctx, f.group)
}

// A request priced at its group's own price of a model without a global
// price settles like any other, at once and on retry from the price
// history: no price_id and no base, its base cost its list cost. A model
// with no price at all is refused before.
func TestSettleGroupOnlyPrice(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.priceInGroup("group-only", customExpr)
	rule, _, err := f.bill.Resolve(ctx, core.PriceQuery{Model: "group-only", GroupID: f.group}, 0, core.RateLevels{Group: decimal.NewFromInt(1)})
	require.NoError(t, err)
	require.Equal(t, []any{int64(0), (*core.PriceBase)(nil), core.PriceSourceGroup}, []any{rule.ID, rule.Base, rule.Source})
	_, _, err = f.bill.Resolve(ctx, core.PriceQuery{Model: "nowhere", GroupID: f.group}, 0, core.RateLevels{Group: decimal.NewFromInt(1)})
	require.Equal(t, core.ErrPriceNotConfigured.Code, core.AsError(err).Code)

	now, retried := f.record("own-now", true), f.record("own-retried", true)
	for _, rec := range []*core.UsageRecord{now, retried} {
		rec.Model, rec.Price, rec.RateMultiplier = "group-only", rule.Clone(), dec("2")
	}
	before := f.balance()
	f.svc.process(ctx, []*core.UsageRecord{now})
	_, err = f.svc.insert(ctx, []*core.UsageRecord{retried})
	require.NoError(t, err)
	time.Sleep(5 * time.Millisecond)
	n, err := f.svc.RetryPending(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	for _, id := range []string{"own-now", "own-retried"} {
		checkBilledAtGroupOnlyPrice(t, f, id)
	}
	require.True(t, before.Sub(f.balance()).Equal(dec("0.416")), "charged %s", before.Sub(f.balance()))
}

// checkBilledAtGroupOnlyPrice checks the row of id billed at customExpr, at
// rate 2, without a global price.
func checkBilledAtGroupOnlyPrice(t *testing.T, f *fixture, id string) {
	require.Equal(t, "billed 0.20800000 0.10400000 2.0000", f.moneyColumns(id), id) // 0.104 x 2
	require.Equal(t, "<nil>", f.scalar(`SELECT price_id FROM usage_logs WHERE request_id = $1`, id), id)
	detail := f.billingDetail(id)
	require.Equal(t, []any{"0.104", false, `{"price_source": "group"}`},
		[]any{detail.Get("cost").String(), detail.Get("list_cost").Exists(), detail.Get("inputs.pricing").Raw}, id)
}
