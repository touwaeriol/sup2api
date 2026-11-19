package billing

import (
	"context"
	"fmt"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/shopspring/decimal"
)

func quoteRecord(ctx context.Context, q core.Quoter, r *core.UsageRecord) (decimal.Decimal, error) {
	if r.BillingError != "" {
		return decimal.Zero, fmt.Errorf("usage billing blocked: %s", r.BillingError)
	}
	if len(r.Additional)+len(r.Replacement) > 128 {
		return decimal.Zero, fmt.Errorf("additional usage exceeds limit")
	}
	items := append(append([]core.PricedUsage(nil), r.Replacement...), r.Additional...)
	for _, item := range items {
		if err := core.ValidatePricedUsage(item); err != nil {
			return decimal.Zero, err
		}
	}
	if len(r.Replacement) == 0 {
		items = append([]core.PricedUsage{{Price: r.Price, UsageSemantics: r.UsageSemantics, Tokens: r.Tokens, Metrics: r.Metrics, PriceParams: r.PriceParams, PriceHeaders: r.PriceHeaders, RateMultiplier: r.RateMultiplier}}, items...)
	}
	total := decimal.Zero
	for _, item := range items {
		if item.Free {
			continue
		}
		params := map[string]any{}
		for k, v := range item.PriceParams {
			params[k] = v
		}
		amount, err := q.Quote(ctx, core.QuoteInputs{Expression: item.Price.Expression, UsageSemantics: item.UsageSemantics, Tokens: item.Tokens, Metrics: item.Metrics, PriceParams: params, PriceHeaders: item.PriceHeaders, RateMultiplier: item.Price.Multiplier(item.RateMultiplier), CreatedAt: r.CreatedAt})
		if err != nil {
			return decimal.Zero, err
		}
		if amount.IsNegative() {
			return decimal.Zero, fmt.Errorf("negative usage component")
		}
		total = total.Add(amount)
	}
	return total, nil
}
