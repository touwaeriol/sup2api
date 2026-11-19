package usage

import (
	"context"
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

func (s *Service) priceOf(ctx context.Context, p *pending) (amounts, BillingDetail, string, error) {
	return priceComponents(p, func(part *pending) (amounts, BillingDetail, string, error) { return priceOne(ctx, s.quoter, part) })
}

// priceComponents prices the primary usage and every replacement and
// additional item, each at its own frozen price and rate, and sums the three
// amounts of all of them.
func priceComponents(p *pending, price func(*pending) (amounts, BillingDetail, string, error)) (amounts, BillingDetail, string, error) {
	if p.Inputs.BillingError != "" {
		return amounts{}, BillingDetail{}, "", fmt.Errorf("usage settlement blocked: %s", p.Inputs.BillingError)
	}
	if len(p.Inputs.Additional)+len(p.Inputs.Replacement) > 128 {
		return amounts{}, BillingDetail{}, "", fmt.Errorf("additional usage exceeds limit")
	}
	if len(p.Inputs.Additional) == 0 && len(p.Inputs.Replacement) == 0 {
		return price(p)
	}
	primary := *p
	primary.Inputs.Additional = nil
	primary.Inputs.Replacement = nil
	var sum amounts
	detail := BillingDetail{Inputs: p.Inputs, Attempts: p.Attempts + 1}
	hash := ""
	// The primary usage is priced unless it is replaced. A price whose
	// expression the price history no longer has is refused by priceOne and
	// the row fails, as it does without items (CONTRACTS §97.4).
	if len(p.Inputs.Replacement) == 0 {
		var err error
		sum, detail, hash, err = price(&primary)
		if err != nil {
			return amounts{}, BillingDetail{}, "", err
		}
		// The top of the detail sums every component below; what the
		// primary usage itself cost is kept beside it.
		cost, total := detail.Cost, detail.TotalCost
		detail.PrimaryCost, detail.PrimaryListCost, detail.PrimaryTotalCost = &cost, detail.ListCost, &total
	}
	detail.Inputs = p.Inputs
	ruled := detail.ListCost != nil
	items := append(append([]core.PricedUsage(nil), p.Inputs.Replacement...), p.Inputs.Additional...)
	for index, item := range items {
		if err := core.ValidatePricedUsage(item); err != nil {
			return amounts{}, BillingDetail{}, "", err
		}
		// Only a row written under the removed free missing-price policy
		// holds an item without a price (CONTRACTS §17.3): it fails.
		if item.Price == nil {
			return amounts{}, BillingDetail{}, "", fmt.Errorf("additional usage %s/%s has no price", item.Kind, item.Model)
		}
		if item.Free {
			d := AdditionalBillingDetail{Kind: item.Kind, Model: item.Model, PriceID: item.Price.ID, Free: true, BillingReason: item.BillingReason}
			if index < len(p.Inputs.Replacement) {
				detail.Replacement = append(detail.Replacement, d)
			} else {
				detail.Additional = append(detail.Additional, d)
			}
			continue
		}
		part := &pending{Model: item.Model, Tokens: item.Tokens, Metrics: item.Metrics, CreatedAt: p.CreatedAt, Rate: item.RateMultiplier, Expression: item.Price.Expression, PriceID: item.Price.ID, Mode: item.Price.Mode, Attempts: p.Attempts, Inputs: pendingInputs{Semantics: item.UsageSemantics, Params: item.PriceParams, Headers: item.PriceHeaders, Pricing: pricingTrace(item.Price, 0, "")}}
		amount, sub, exprHash, err := price(part)
		if err != nil {
			return amounts{}, BillingDetail{}, "", fmt.Errorf("additional usage %s/%s: %w", item.Kind, item.Model, err)
		}
		if amount.total.IsNegative() {
			return amounts{}, BillingDetail{}, "", fmt.Errorf("negative additional usage charge")
		}
		sum = sum.add(amount)
		ruled = ruled || sub.ListCost != nil
		d := AdditionalBillingDetail{Kind: item.Kind, Model: item.Model, PriceID: item.Price.ID, ExpressionHash: exprHash, Detail: sub, BillingReason: item.BillingReason}
		if index < len(p.Inputs.Replacement) {
			detail.Replacement = append(detail.Replacement, d)
		} else {
			detail.Additional = append(detail.Additional, d)
		}
	}
	detail.Cost, detail.TotalCost = sum.base, sum.total
	if ruled {
		detail.ListCost = &sum.list
	}
	return sum, detail, hash, nil
}
