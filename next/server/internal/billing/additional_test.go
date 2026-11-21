package billing

import (
	"context"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/shopspring/decimal"
	"testing"
)

func TestAdditionalPrechargeQuotesIndependentComponents(t *testing.T) {
	s := &Service{}
	r := &core.UsageRecord{Price: &core.PriceRule{Expression: `tier("main",p*2)`}, UsageSemantics: "exclusive", Tokens: core.UsageTokens{Input: 1_000_000}, RateMultiplier: decimal.NewFromInt(1), Additional: []core.PricedUsage{{Kind: "advisor", Model: "other", Price: &core.PriceRule{Expression: `tier("other",c*10)`}, UsageSemantics: "exclusive", Tokens: core.UsageTokens{Output: 500_000}, RateMultiplier: decimal.NewFromInt(2)}}}
	amount, err := quoteRecord(context.Background(), s, r)
	if err != nil || !amount.Equal(decimal.NewFromInt(12)) {
		t.Fatal(amount, err)
	}
	r.Price = &core.PriceRule{Expression: `tier("main",p*0)`}
	amount, err = quoteRecord(context.Background(), s, r)
	if err != nil || !amount.Equal(decimal.NewFromInt(10)) {
		t.Fatal("zero-priced main hid extras", amount, err)
	}
	r.Additional[0].Tokens.Output = -1
	if _, err := quoteRecord(context.Background(), s, r); err == nil {
		t.Fatal("invalid count quoted")
	}
}

func TestReplacementPrechargeUsesAttemptsWithoutFinalDoubleCount(t *testing.T) {
	r := &core.UsageRecord{Price: &core.PriceRule{Expression: `tier("wrong-main",p*99)`}, Tokens: core.UsageTokens{Input: 1_000_000}, UsageSemantics: "exclusive", RateMultiplier: decimal.NewFromInt(1)}
	r.Replacement = []core.PricedUsage{{Kind: "fallback", Model: "primary", Price: r.Price, Tokens: r.Tokens, UsageSemantics: "exclusive", RateMultiplier: decimal.NewFromInt(1), Free: true, BillingReason: "refusal_before_output_free_cyber"}, {Kind: "fallback", Model: "next", Price: &core.PriceRule{Expression: `tier("actual",p*2)`}, Tokens: r.Tokens, UsageSemantics: "exclusive", RateMultiplier: decimal.NewFromInt(1)}}
	amount, e := quoteRecord(context.Background(), &Service{}, r)
	if e != nil || !amount.Equal(decimal.NewFromInt(2)) {
		t.Fatal(amount, e)
	}
}
