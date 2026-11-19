package usage

import (
	"encoding/json"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/shopspring/decimal"
	"strings"
	"testing"
)

func TestAdditionalPriceSnapshotsSurviveRetryAndKeepMainSeparate(t *testing.T) {
	item := core.PricedUsage{Kind: "advisor", Model: "extra", Price: &core.PriceRule{ID: 9, Expression: `tier("extra",p*10+c*20) ||| header("premium") == "yes" ? 2 : 1`}, Tokens: core.UsageTokens{Input: 1_000_000, Output: 500_000}, UsageSemantics: "exclusive", PriceHeaders: map[string]string{"premium": "yes"}, RateMultiplier: decimal.NewFromInt(3)}
	p := &pending{Expression: `tier("main",p*2+c*4)`, Tokens: core.UsageTokens{Input: 1_000_000, Output: 500_000}, Rate: decimal.NewFromInt(2), Inputs: pendingInputs{Semantics: "exclusive", Additional: []core.PricedUsage{item}}}
	total, detail, _, err := priceOf(p)
	if err != nil {
		t.Fatal(err)
	}
	if !total.Equal(decimal.NewFromInt(128)) || len(detail.Additional) != 1 || detail.Breakdown.Vars.P != 1_000_000 {
		t.Fatal(total, detail)
	}
	raw, _ := json.Marshal(pendingDetail{Inputs: p.Inputs})
	var persisted pendingDetail
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	p.Inputs = persisted.Inputs
	again, _, _, err := priceOf(p)
	if err != nil || !again.Equal(total) {
		t.Fatal("retry changed frozen price", again, err)
	}
	p.Expression = ""
	if amount, _, _, err := priceOf(p); err == nil || !amount.IsZero() {
		t.Fatal("a main usage without its price was settled", amount, err)
	}
	p.Inputs.BillingError = "untrusted model"
	if _, _, _, err := priceOf(p); err == nil {
		t.Fatal("blocked usage charged")
	}
}

func TestAdditionalPricingFailureIsAtomicAndRecordedPending(t *testing.T) {
	rec := &core.UsageRecord{Billable: true, Price: &core.PriceRule{Expression: `tier("main",p)`}, Additional: []core.PricedUsage{{Kind: "advisor", Model: "extra", Price: &core.PriceRule{Expression: "invalid ("}, UsageSemantics: "exclusive", RateMultiplier: decimal.NewFromInt(1)}}}
	if initialStatus(rec) != StatusPending {
		t.Fatal("additional work not pending")
	}
	if amount, _, _, err := priceOf(fromRecord(rec)); err == nil || !amount.IsZero() {
		t.Fatal("partial amount returned on pricing failure", amount, err)
	}
	rec.Additional = nil
	rec.BillingError = "missing usage"
	if initialStatus(rec) != StatusPending {
		t.Fatal("billing error silently free")
	}
}

// The top of the detail sums every component; the primary usage's own
// amounts are kept beside it when it is priced, and only then.
func TestPrimaryAmountsBesideItems(t *testing.T) {
	item := core.PricedUsage{Kind: "advisor", Model: "extra", Price: &core.PriceRule{ID: 9, Expression: `tier("extra",p*10)`}, Tokens: core.UsageTokens{Input: 1_000_000}, UsageSemantics: "exclusive", RateMultiplier: decimal.NewFromInt(3)}
	p := &pending{Expression: `tier("main",p*2+c*4)`, Tokens: core.UsageTokens{Input: 1_000_000, Output: 500_000}, Rate: decimal.NewFromInt(2), Inputs: pendingInputs{Semantics: "exclusive", Additional: []core.PricedUsage{item}}}
	total, detail, _, err := priceOf(p)
	if err != nil {
		t.Fatal(err)
	}
	// main 1*2 + 0.5*4 = 4 at rate 2 = 8; advisor 1*10 at rate 3 = 30.
	if detail.PrimaryCost == nil || !detail.PrimaryCost.Equal(decimal.NewFromInt(4)) || detail.PrimaryTotalCost == nil || !detail.PrimaryTotalCost.Equal(decimal.NewFromInt(8)) || detail.PrimaryListCost != nil {
		t.Fatal("primary amounts", detail.PrimaryCost, detail.PrimaryTotalCost, detail.PrimaryListCost)
	}
	if !detail.PrimaryTotalCost.Add(detail.Additional[0].Detail.TotalCost).Equal(detail.TotalCost) || !detail.TotalCost.Equal(total) || !total.Equal(decimal.NewFromInt(38)) {
		t.Fatal("primary + items != total", detail.TotalCost, total)
	}

	// A retry prices the persisted inputs to the same amounts.
	raw, _ := json.Marshal(pendingDetail{Inputs: p.Inputs})
	var persisted pendingDetail
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	p.Inputs = persisted.Inputs
	_, again, _, err := priceOf(p)
	if err != nil || !again.PrimaryTotalCost.Equal(*detail.PrimaryTotalCost) || !again.PrimaryCost.Equal(*detail.PrimaryCost) || !again.TotalCost.Equal(detail.TotalCost) {
		t.Fatal("retry changed the primary amounts", again.PrimaryTotalCost, err)
	}

	// A multiplier rule on the primary usage: its list cost is kept too.
	scaled := &pending{Expression: p.Expression, Tokens: p.Tokens, Rate: p.Rate, Inputs: pendingInputs{Semantics: "exclusive", Additional: []core.PricedUsage{item},
		Pricing: &core.PricingTrace{PriceSource: core.PriceSourceGroup, Scale: decPtr("0.5")}}}
	_, d, _, err := priceAmounts(scaled)
	if err != nil || !d.PrimaryCost.Equal(dec("4")) || d.PrimaryListCost == nil || !d.PrimaryListCost.Equal(dec("2")) || !d.PrimaryTotalCost.Equal(dec("4")) || !d.TotalCost.Equal(dec("34")) {
		t.Fatal("scaled primary", d.PrimaryCost, d.PrimaryListCost, d.PrimaryTotalCost, d.TotalCost, err)
	}

	// A replaced primary usage is not priced; a record without items is all primary usage.
	replaced := &pending{Expression: p.Expression, Tokens: p.Tokens, Rate: p.Rate, Inputs: pendingInputs{Semantics: "exclusive", Replacement: []core.PricedUsage{item}}}
	plain := &pending{Expression: p.Expression, Tokens: p.Tokens, Rate: p.Rate, Inputs: pendingInputs{Semantics: "exclusive"}}
	for name, q := range map[string]*pending{"replaced": replaced, "plain": plain} {
		_, d, _, err := priceOf(q)
		if err != nil || d.PrimaryCost != nil || d.PrimaryListCost != nil || d.PrimaryTotalCost != nil {
			t.Fatal(name, d.PrimaryCost, d.PrimaryListCost, d.PrimaryTotalCost, err)
		}
		raw, _ := json.Marshal(d)
		if strings.Contains(string(raw), "primary_") {
			t.Fatal(name, string(raw))
		}
	}
}
