package usage

import (
	"context"
	"errors"
	"fmt"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

type AdditionalBillingDetail struct {
	Free           bool          `json:"free,omitempty"`
	BillingReason  string        `json:"billing_reason,omitempty"`
	Kind           string        `json:"kind"`
	Model          string        `json:"model"`
	PriceID        int64         `json:"price_id"`
	ExpressionHash string        `json:"expression_hash"`
	Detail         BillingDetail `json:"detail"`
}

// pricer prices one usage at its own frozen price and rate.
type pricer func(*pending) (amounts, BillingDetail, string, error)

func (s *Service) priceOf(ctx context.Context, p *pending) (amounts, BillingDetail, string, error) {
	return priceComponents(p, func(part *pending) (amounts, BillingDetail, string, error) { return priceOne(ctx, s.quoter, part) })
}

// priceComponents prices the primary usage and every replacement and
// additional item, each at its own frozen price and rate, and sums the three
// amounts of all of them. The top of the detail is a base fallback when any
// component is (CONTRACTS §97.3).
func priceComponents(p *pending, price pricer) (amounts, BillingDetail, string, error) {
	if len(p.Inputs.Additional)+len(p.Inputs.Replacement) > 128 {
		return amounts{}, BillingDetail{}, "", errors.New("additional usage exceeds limit")
	}
	if len(p.Inputs.Additional) == 0 && len(p.Inputs.Replacement) == 0 {
		return price(p)
	}
	sum, detail, hash, err := pricePrimary(p, price)
	if err != nil {
		return amounts{}, BillingDetail{}, "", err
	}
	ruled := detail.ListCost != nil
	items := append(append([]core.PricedUsage(nil), p.Inputs.Replacement...), p.Inputs.Additional...)
	for index, item := range items {
		d, amount, err := priceItem(p, item, price)
		if err != nil {
			return amounts{}, BillingDetail{}, "", err
		}
		sum = sum.add(amount)
		ruled = ruled || d.Detail.ListCost != nil
		detail.BaseFallback = detail.BaseFallback || d.Detail.BaseFallback
		detail.place(d, index < len(p.Inputs.Replacement))
	}
	detail.Cost, detail.TotalCost = sum.base, sum.total
	if ruled {
		detail.ListCost = &sum.list
	}
	return sum, detail, hash, nil
}

// place files an item's detail under the replacements or the additional items.
func (d *BillingDetail) place(item AdditionalBillingDetail, replacement bool) {
	if replacement {
		d.Replacement = append(d.Replacement, item)
		return
	}
	d.Additional = append(d.Additional, item)
}

// pricePrimary prices the primary usage unless it is replaced. A price whose
// expression the price history no longer has is refused by priceOne and the
// row fails, as it does without items (CONTRACTS §97.4). The top of the
// detail sums every component; what the primary usage itself cost is kept
// beside it.
func pricePrimary(p *pending, price pricer) (amounts, BillingDetail, string, error) {
	if len(p.Inputs.Replacement) > 0 {
		return amounts{}, BillingDetail{Inputs: p.Inputs, Attempts: p.Attempts + 1}, "", nil
	}
	primary := *p
	primary.Inputs.Additional, primary.Inputs.Replacement = nil, nil
	sum, detail, hash, err := price(&primary)
	if err != nil {
		return amounts{}, BillingDetail{}, "", err
	}
	cost, total := detail.Cost, detail.TotalCost
	detail.PrimaryCost, detail.PrimaryListCost, detail.PrimaryTotalCost = &cost, detail.ListCost, &total
	detail.Inputs = p.Inputs
	return sum, detail, hash, nil
}

// priceItem prices one replacement or additional item. Only a row written
// under the removed free missing-price policy holds an item without a price
// (CONTRACTS §17.3): it fails.
func priceItem(p *pending, item core.PricedUsage, price pricer) (AdditionalBillingDetail, amounts, error) {
	if err := core.ValidatePricedUsage(item); err != nil {
		return AdditionalBillingDetail{}, amounts{}, err
	}
	if item.Price == nil {
		return AdditionalBillingDetail{}, amounts{}, fmt.Errorf("additional usage %s/%s has no price", item.Kind, item.Model)
	}
	d := AdditionalBillingDetail{Kind: item.Kind, Model: item.Model, PriceID: item.Price.ID, Free: item.Free, BillingReason: item.BillingReason}
	if item.Free {
		return d, amounts{}, nil
	}
	part := &pending{Model: item.Model, Tokens: item.Tokens, Metrics: item.Metrics, CreatedAt: p.CreatedAt, Rate: item.RateMultiplier, Expression: item.Price.Expression, PriceID: item.Price.ID, Mode: item.Price.Mode, Attempts: p.Attempts, Inputs: pendingInputs{Semantics: item.UsageSemantics, Params: item.PriceParams, Headers: item.PriceHeaders, Pricing: pricingTrace(item.Price, 0, "")}}
	amount, sub, exprHash, err := price(part)
	if err != nil {
		return AdditionalBillingDetail{}, amounts{}, fmt.Errorf("additional usage %s/%s: %w", item.Kind, item.Model, err)
	}
	if amount.total.IsNegative() {
		return AdditionalBillingDetail{}, amounts{}, errors.New("negative additional usage charge")
	}
	d.ExpressionHash, d.Detail = exprHash, sub
	return d, amount, nil
}
