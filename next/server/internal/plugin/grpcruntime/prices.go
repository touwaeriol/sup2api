package grpcruntime

import (
	"context"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
)

// PermPrices grants PriceUsage (CONTRACTS §72.13.1).
const PermPrices = "prices"

// maxPricedTokens bounds each token count of a PriceUsage request.
const maxPricedTokens = 1_000_000_000_000

// UsagePricer prices token usage of a model (billing.Service).
type UsagePricer interface {
	PriceModelUsage(ctx context.Context, model, semantics string, t expr.Tokens) (priced bool, cost decimal.Decimal, reason string, err error)
}

func (h *hostServer) PriceUsage(ctx context.Context, in *pluginv1.PriceUsageRequest) (*pluginv1.PriceUsageResponse, error) {
	if err := h.require(ctx, PermPrices); err != nil {
		return nil, err
	}
	p := h.i.rt.o.Prices
	if p == nil {
		return nil, status.Error(codes.Unavailable, "pricing is not available")
	}
	if in.GetModel() == "" {
		return nil, status.Error(codes.InvalidArgument, "model is required")
	}
	switch in.GetSemantics() {
	case "", expr.SemanticsInclusive, expr.SemanticsExclusive:
	default:
		return nil, status.Error(codes.InvalidArgument, "semantics must be inclusive or exclusive")
	}
	t := expr.Tokens{Input: in.GetInput(), Output: in.GetOutput(), CacheRead: in.GetCacheRead(),
		CacheCreation: in.GetCacheCreation(), CacheCreation1h: in.GetCacheCreation_1H()}
	for _, n := range []int64{t.Input, t.Output, t.CacheRead, t.CacheCreation, t.CacheCreation1h} {
		if n < 0 || n > maxPricedTokens {
			return nil, status.Error(codes.InvalidArgument, "token counts must be between 0 and 1e12")
		}
	}
	priced, cost, reason, err := p.PriceModelUsage(ctx, in.GetModel(), in.GetSemantics(), t)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "price table unavailable")
	}
	if !priced {
		return &pluginv1.PriceUsageResponse{Reason: reason}, nil
	}
	return &pluginv1.PriceUsageResponse{Priced: true, Cost: cost.StringFixed(8)}, nil
}
