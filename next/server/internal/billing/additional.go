package billing

import (
	"context"
	"errors"

	"github.com/shopspring/decimal"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// quoteRecord is what a request is precharged: its primary usage, unless
// replaced, and each additional or replacement item, at their rates.
func quoteRecord(ctx context.Context, q core.Quoter, r *core.UsageRecord) (decimal.Decimal, error) {
	items, err := pricedItems(r)
	if err != nil {
		return decimal.Zero, err
	}
	total := decimal.Zero
	for _, item := range items {
		if item.Free {
			continue
		}
		amount, err := quoteItem(ctx, q, item, r)
		if err != nil {
			return decimal.Zero, err
		}
		total = total.Add(amount)
	}
	return total, nil
}

// pricedItems are the checked items of r to price: the replacement and
// additional ones, after the primary usage when nothing replaces it.
func pricedItems(r *core.UsageRecord) ([]core.PricedUsage, error) {
	if len(r.Additional)+len(r.Replacement) > 128 {
		return nil, errors.New("additional usage exceeds limit")
	}
	items := append(append([]core.PricedUsage(nil), r.Replacement...), r.Additional...)
	for _, item := range items {
		if err := core.ValidatePricedUsage(item); err != nil {
			return nil, err
		}
	}
	if len(r.Replacement) == 0 {
		items = append([]core.PricedUsage{{Price: r.Price, UsageSemantics: r.UsageSemantics, Tokens: r.Tokens, Metrics: r.Metrics, PriceParams: r.PriceParams, PriceHeaders: r.PriceHeaders, RateMultiplier: r.RateMultiplier}}, items...)
	}
	return items, nil
}

// quoteItem is what one item of r costs at its rate; never negative.
func quoteItem(ctx context.Context, q core.Quoter, item core.PricedUsage, r *core.UsageRecord) (decimal.Decimal, error) {
	params := map[string]any{}
	for k, v := range item.PriceParams {
		params[k] = v
	}
	amount, err := q.Quote(ctx, core.QuoteInputs{Expression: item.Price.Expression, UsageSemantics: item.UsageSemantics, Tokens: item.Tokens, Metrics: item.Metrics, PriceParams: params, PriceHeaders: item.PriceHeaders, RateMultiplier: item.RateMultiplier, CreatedAt: r.CreatedAt})
	if err != nil {
		return decimal.Zero, err
	}
	if amount.IsNegative() {
		return decimal.Zero, errors.New("negative usage component")
	}
	return amount, nil
}
