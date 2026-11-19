package billing

import (
	"context"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
)

func TestPriceModelUsage(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.exec(`INSERT INTO model_prices (model, mode, expression, expr_hash, source)
		VALUES ('deepseek-x', 'per_token', 'tier("base", p*0.14 + c*0.5 + cr*0.035)', 'h1', 'manual')`)
	e.svc.invalidate()
	// 1M input of which 200k cached (inclusive), 100k output.
	ok, cost, reason, err := e.svc.PriceModelUsage(ctx, "deepseek-x", expr.SemanticsInclusive,
		expr.Tokens{Input: 1_000_000, Output: 100_000, CacheRead: 200_000})
	if err != nil || !ok || reason != "" || cost.StringFixed(8) != "0.16900000" {
		t.Fatalf("priced: %v %s %q %v", ok, cost, reason, err)
	}
	// Exclusive: input does not contain the cache.
	_, cost, _, _ = e.svc.PriceModelUsage(ctx, "deepseek-x", "", expr.Tokens{Input: 800_000, Output: 100_000, CacheRead: 200_000})
	if cost.StringFixed(8) != "0.16900000" {
		t.Fatalf("exclusive cost %s", cost)
	}
	if ok, _, reason, err := e.svc.PriceModelUsage(ctx, "nope", "", expr.Tokens{Input: 1}); ok || reason != UnpricedNoPrice || err != nil {
		t.Fatalf("missing: %v %q %v", ok, reason, err)
	}
}
