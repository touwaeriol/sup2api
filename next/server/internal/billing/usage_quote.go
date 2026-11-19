package billing

import (
	"context"

	"github.com/shopspring/decimal"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// Reasons a usage could not be priced (HostService.PriceUsage).
const (
	UnpricedNoPrice   = "no_price"
	UnpricedVideoOnly = "video_only"
	UnpricedEvalError = "eval_error"
)

// PriceModelUsage prices token usage of a model with the price table, as
// settlement would before the group multiplier (CONTRACTS §72.13.1). A model
// without a price is reported as not priced, never as free. Only a database
// failure is an error.
func (s *Service) PriceModelUsage(ctx context.Context, model, semantics string, t expr.Tokens) (priced bool, cost decimal.Decimal, reason string, err error) {
	snap, err := s.snapshot(ctx)
	if err != nil {
		return false, decimal.Zero, "", err
	}
	rule := snap.match(model)
	switch {
	case rule == nil:
		return false, decimal.Zero, UnpricedNoPrice, nil
	case rule.VideoOnly:
		return false, decimal.Zero, UnpricedVideoOnly, nil
	}
	if semantics != expr.SemanticsInclusive {
		semantics = expr.SemanticsExclusive
	}
	cost, err = s.Quote(ctx, core.QuoteInputs{
		Expression: rule.Expression, UsageSemantics: semantics, RateMultiplier: decimal.NewFromInt(1),
		Tokens: core.UsageTokens(t),
	})
	if err != nil {
		return false, decimal.Zero, UnpricedEvalError, nil
	}
	return true, cost, "", nil
}
