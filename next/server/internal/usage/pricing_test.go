package usage

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// The three amounts of one priced usage (CONTRACTS §97): a multiplier rule
// scales the list cost and leaves the base cost and the breakdown at the
// global price; a custom rule sets the list cost and the base cost is the
// global expression over the same usage, normalized for itself.
func TestPriceOneAmounts(t *testing.T) {
	// The global price has a 1-hour cache write rate and the custom one does
	// not: normalized for the custom price, both cache writes count as cc.
	const (
		global = `tier("global", p*2 + c*4 + cc*3 + cc1h*6)` // 2 + 2 + 3 + 6
		custom = `tier("custom", p*1 + c*2 + cc*2)`          // 1 + 1 + 4
	)
	for _, tc := range []struct {
		name              string
		expression        string
		pricing           *core.PricingTrace
		base, list, total string
		listed            bool // billing_detail.list_cost is written
		tier              string
	}{
		{"no rule", global, nil, "13", "13", "19.5", false, "global"},
		{"the segment's rate alone", global, &core.PricingTrace{SegmentID: 4, RateSource: core.RateSourceDiscount, PriceSource: core.PriceSourceGlobal},
			"13", "13", "19.5", false, "global"},
		{"multiplier", global, &core.PricingTrace{PriceSource: core.PriceSourceGroup, Scale: decPtr("0.8")}, "13", "10.4", "15.6", true, "global"},
		{"multiplier 0", global, &core.PricingTrace{PriceSource: core.PriceSourceSegment, Scale: decPtr("0")}, "13", "0", "0", true, "global"},
		{"multiplier 1", global, &core.PricingTrace{PriceSource: core.PriceSourceSegment, Scale: decPtr("1")}, "13", "13", "19.5", true, "global"},
		{"custom", custom, &core.PricingTrace{PriceSource: core.PriceSourceGroup, Base: &core.PriceBase{Expression: global}}, "13", "6", "9", true, "custom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &pending{
				Expression: tc.expression, Rate: dec("1.5"), CreatedAt: time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC),
				Tokens: core.UsageTokens{Input: 1_000_000, Output: 500_000, CacheCreation: 1_000_000, CacheCreation1h: 1_000_000},
				Inputs: pendingInputs{Semantics: "exclusive", Pricing: tc.pricing},
			}
			cost, detail, hash, err := priceOne(context.Background(), billing.New(nil, nil, nil, nil, nil), p)
			require.NoError(t, err)
			require.NotEmpty(t, hash)
			requireDec(t, tc.base, cost.base, "base")
			requireDec(t, tc.list, cost.list, "list")
			requireDec(t, tc.total, cost.total, "total")

			requireDec(t, tc.base, detail.Cost, "billing_detail.cost")
			requireDec(t, tc.total, detail.TotalCost, "billing_detail.total_cost")
			requireDec(t, "1.5", detail.RateMultiplier, "billing_detail.rate_multiplier") // the rate, without the rule's multiplier
			require.Equal(t, tc.tier, detail.Tier)
			require.Equal(t, tc.pricing, detail.Inputs.Pricing)
			require.Equal(t, tc.listed, detail.ListCost != nil)
			if tc.listed {
				requireDec(t, tc.list, *detail.ListCost, "billing_detail.list_cost")
			}
		})
	}
}

// The base cost is shown only: a global price that cannot be evaluated
// leaves it at the list cost, and the row is charged as usual.
func TestPriceOneBaseExpressionErrors(t *testing.T) {
	p := &pending{Expression: `tier("custom", p*1)`, Rate: dec("2"), Tokens: core.UsageTokens{Input: 1_000_000},
		Inputs: pendingInputs{Semantics: "exclusive", Pricing: &core.PricingTrace{Base: &core.PriceBase{Expression: "invalid ("}}}}
	cost, detail, _, err := priceAmounts(p)
	require.NoError(t, err)
	requireDec(t, "1", cost.base, "base")
	requireDec(t, "1", cost.list, "list")
	requireDec(t, "2", cost.total, "total")
	requireDec(t, "1", detail.Cost, "billing_detail.cost")
}

// The primary usage, a referenced model under another rule, an item at the
// global price and a replacement: each is priced at its own frozen price and
// rate, and the three amounts are summed over all of them.
func TestPriceComponentsSumAmounts(t *testing.T) {
	million := core.UsageTokens{Input: 1_000_000}
	referenced := core.PricedUsage{Kind: "advisor", Model: "referenced", UsageSemantics: "exclusive", Tokens: million, RateMultiplier: dec("3"),
		Price: &core.PriceRule{ID: 9, Mode: "visual", Expression: `tier("custom", p*10)`, Source: core.PriceSourceGroup,
			Base: &core.PriceBase{Expression: `tier("global", p*30)`, ExprHash: "g9"}}} // base 30, list 10, total 30
	plain := core.PricedUsage{Kind: "tool", Model: "plain", UsageSemantics: "exclusive", Tokens: million, RateMultiplier: dec("2"),
		Price: &core.PriceRule{ID: 5, Expression: `tier("plain", p*5)`}} // base 5, list 5, total 10
	p := &pending{
		Expression: `tier("main", p*2 + c*4)`, Rate: dec("2"), Tokens: core.UsageTokens{Input: 1_000_000, Output: 500_000}, // base 4, list 2, total 4
		Inputs: pendingInputs{Semantics: "exclusive", Additional: []core.PricedUsage{referenced, plain},
			Pricing: &core.PricingTrace{SegmentID: 4, RateSource: core.RateSourceSegment, PriceSource: core.PriceSourceSegment, Scale: decPtr("0.5")}},
	}
	check := func(stage, base, list, total string) BillingDetail {
		t.Helper()
		cost, detail, _, err := priceAmounts(p)
		require.NoError(t, err, stage)
		requireDec(t, base, cost.base, stage+": base")
		requireDec(t, list, cost.list, stage+": list")
		requireDec(t, total, cost.total, stage+": total")
		requireDec(t, base, detail.Cost, stage+": billing_detail.cost")
		requireDec(t, total, detail.TotalCost, stage+": billing_detail.total_cost")
		require.NotNil(t, detail.ListCost, stage)
		requireDec(t, list, *detail.ListCost, stage+": billing_detail.list_cost")
		return detail
	}
	detail := check("additional", "39", "17", "44")
	require.Len(t, detail.Additional, 2)
	custom := detail.Additional[0].Detail
	requireDec(t, "30", custom.Cost, "referenced cost")
	requireDec(t, "10", *custom.ListCost, "referenced list_cost")
	require.Equal(t, &core.PricingTrace{PriceSource: core.PriceSourceGroup, Base: referenced.Price.Base}, custom.Inputs.Pricing)
	require.Nil(t, detail.Additional[1].Detail.ListCost)
	require.Nil(t, detail.Additional[1].Detail.Inputs.Pricing)

	// A replacement takes the primary usage's place, rule and all.
	free := core.PricedUsage{Kind: "attempt", Model: "refused", UsageSemantics: "exclusive", Tokens: million, RateMultiplier: dec("2"),
		Free: true, BillingReason: "refusal_before_output", Price: &core.PriceRule{Expression: `tier("refused", p*100)`, Scale: decPtr("7")}}
	fallback := core.PricedUsage{Kind: "attempt", Model: "fallback", UsageSemantics: "exclusive", Tokens: million, RateMultiplier: dec("2"),
		Price: &core.PriceRule{Expression: `tier("fallback", p*3)`, Source: core.PriceSourceSegment, Scale: decPtr("2")}} // base 3, list 6, total 12
	p.Inputs.Replacement = []core.PricedUsage{free, fallback}
	detail = check("replacement", "38", "21", "52")
	require.Len(t, detail.Replacement, 2)
	require.True(t, detail.Replacement[0].Free)
	requireDec(t, "6", *detail.Replacement[1].Detail.ListCost, "fallback list_cost")

	// What a retry reads back prices the same: the rules are in the row.
	raw, err := json.Marshal(pendingDetail{Inputs: p.Inputs})
	require.NoError(t, err)
	var stored pendingDetail
	require.NoError(t, json.Unmarshal(raw, &stored))
	require.Equal(t, core.PriceSourceGroup, stored.Inputs.Additional[0].Price.Source)
	require.Equal(t, referenced.Price.Base, stored.Inputs.Additional[0].Price.Base)
	requireDec(t, "2", *stored.Inputs.Replacement[1].Price.Scale, "stored scale")
	p.Inputs = stored.Inputs
	check("stored", "38", "21", "52")
}

// What a row freezes about its price and rate: nothing at the global price
// and the group's own rate, and otherwise enough to give the rule back. A
// zero multiplier is a rule, not the absence of one.
func TestPricingTrace(t *testing.T) {
	base := &core.PriceBase{Expression: `tier("global", p*2)`, ExprHash: "g"}
	for _, tc := range []struct {
		name       string
		price      *core.PriceRule
		segmentID  int64
		rateSource string
		want       *core.PricingTrace
	}{
		{"no price", nil, 4, "", nil},
		{"a price from before price rules", &core.PriceRule{ID: 1}, 4, "", nil},
		{"the global price at the group's rate", &core.PriceRule{ID: 1, Source: core.PriceSourceGlobal}, 4, core.RateSourceGroup, nil},
		{"the segment's discount", &core.PriceRule{ID: 1, Source: core.PriceSourceGlobal}, 4, core.RateSourceDiscount,
			&core.PricingTrace{SegmentID: 4, RateSource: core.RateSourceDiscount, PriceSource: core.PriceSourceGlobal}},
		{"the segment's rate without a price", nil, 4, core.RateSourceSegment, &core.PricingTrace{SegmentID: 4, RateSource: core.RateSourceSegment}},
		{"multiplier 0", &core.PriceRule{ID: 1, Source: core.PriceSourceGroup, Scale: decPtr("0")}, 4, core.RateSourceGroup,
			&core.PricingTrace{SegmentID: 4, RateSource: core.RateSourceGroup, PriceSource: core.PriceSourceGroup, Scale: decPtr("0")}},
		{"custom", &core.PriceRule{ID: 1, Source: core.PriceSourceSegment, Base: base}, 4, "",
			&core.PricingTrace{SegmentID: 4, PriceSource: core.PriceSourceSegment, Base: base}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := pricingTrace(tc.price, tc.segmentID, tc.rateSource)
			require.Equal(t, tc.want, got)
			// The trace gives the rule back: a retry prices with it.
			frozen := (&pending{Inputs: pendingInputs{Pricing: got}}).price()
			rule := tc.price
			if rule == nil {
				rule = &core.PriceRule{}
			}
			require.Equal(t, rule.Scale, frozen.Scale)
			require.Equal(t, rule.Base, frozen.Base)
		})
	}
}

// What a row stores at the global price and the group's own rate, as the
// release before price rules wrote it for unruledRecord: its documents must
// stay these bytes.
const (
	unruledPending = `{"inputs":{"additional":[{"Free":false,"BillingReason":"","Kind":"advisor","Model":"extra","Price":{"ID":9,"Model":"extra","Mode":"visual","Expression":"tier(\"extra\", p*10 + c*20)","ExprVersion":1,"ExprHash":"","VideoOnly":false},"UsageSemantics":"exclusive","Tokens":{"Input":1000000,"Output":500000,"CacheRead":0,"CacheCreation":0,"CacheCreation1h":0},"Metrics":null,"PriceParams":null,"PriceHeaders":null,"RateMultiplier":"3"}],"semantics":"exclusive","params":{"speed":"\"fast\""},"headers":{"x-tier":"fast"}},"attempts":0}`
	unruledBilled  = `{"additional":[{"kind":"advisor","model":"extra","price_id":9,"expression_hash":"d1319e8c32b45af4e097529fead6d34f225472e19800147265e9894525415424","detail":{"expr_version":1,"tier":"extra","rules":[],"breakdown":{"vars":{"p":1000000,"c":500000,"cr":0,"cc":0,"cc1h":0,"len":1000000},"tiers":["extra"],"items":[{"tier":"extra","label":"p","quantity":1000000,"rate":10,"expr":"p * 10","cost":"10"},{"tier":"extra","label":"c","quantity":500000,"rate":20,"expr":"c * 20","cost":"10"}],"subtotal":"20","base":"20","rules_multiplier":"1"},"cost":"20","rate_multiplier":"3","total_cost":"60","inputs":{"semantics":"exclusive"},"attempts":1}}],"expr_version":1,"tier":"main","rules":[{"cond":"header(\"x-tier\") == \"fast\"","multiplier":2,"matched":true}],"breakdown":{"vars":{"p":1000000,"c":500000,"cr":250000,"cc":0,"cc1h":0,"len":1250000},"tiers":["main"],"items":[{"tier":"main","label":"p","quantity":1000000,"rate":2,"expr":"p * 2","cost":"2"},{"tier":"main","label":"c","quantity":500000,"rate":4,"expr":"c * 4","cost":"2"},{"tier":"main","label":"cr","quantity":250000,"rate":0.2,"expr":"cr * 0.2","cost":"0.05"}],"subtotal":"4.05","base":"4.05","rules_multiplier":"2"},"cost":"28.1","rate_multiplier":"1.25","total_cost":"70.125","primary_cost":"8.1","primary_total_cost":"10.125","inputs":{"additional":[{"Free":false,"BillingReason":"","Kind":"advisor","Model":"extra","Price":{"ID":9,"Model":"extra","Mode":"visual","Expression":"tier(\"extra\", p*10 + c*20)","ExprVersion":1,"ExprHash":"","VideoOnly":false},"UsageSemantics":"exclusive","Tokens":{"Input":1000000,"Output":500000,"CacheRead":0,"CacheCreation":0,"CacheCreation1h":0},"Metrics":null,"PriceParams":null,"PriceHeaders":null,"RateMultiplier":"3"}],"semantics":"exclusive","params":{"speed":"\"fast\""},"headers":{"x-tier":"fast"}},"attempts":1}`
)

func unruledRecord() *core.UsageRecord {
	return &core.UsageRecord{
		RequestID: "req-golden", UserID: 1, GroupID: 2, Model: "m", UsageSemantics: "exclusive", Billable: true,
		Tokens:         core.UsageTokens{Input: 1_000_000, Output: 500_000, CacheRead: 250_000},
		Price:          &core.PriceRule{ID: 3, Model: "m", Mode: "expression", Expression: `(tier("main", p*2 + c*4 + cr*0.2)) * (header("x-tier") == "fast" ? 2 : 1)`, ExprVersion: 1},
		PriceParams:    map[string]string{"speed": `"fast"`},
		PriceHeaders:   map[string]string{"x-tier": "fast"},
		RateMultiplier: dec("1.25"),
		CreatedAt:      time.Date(2026, 10, 4, 8, 0, 0, 0, time.UTC),
		Additional: []core.PricedUsage{{Kind: "advisor", Model: "extra",
			Price:          &core.PriceRule{ID: 9, Model: "extra", Mode: "visual", Expression: `tier("extra", p*10 + c*20)`, ExprVersion: 1},
			UsageSemantics: "exclusive", Tokens: core.UsageTokens{Input: 1_000_000, Output: 500_000}, RateMultiplier: dec("3")}},
	}
}

func TestUnruledDetailIsUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name string
		mark func(*core.UsageRecord)
	}{
		{"a record from before segments", func(*core.UsageRecord) {}},
		{"the global price at the group's rate", func(rec *core.UsageRecord) {
			rec.SegmentID, rec.RateSource, rec.Price.Source = 7, core.RateSourceGroup, core.PriceSourceGlobal
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := unruledRecord()
			tc.mark(rec)
			p := fromRecord(rec)
			stored, err := json.Marshal(pendingDetail{Inputs: p.Inputs})
			require.NoError(t, err)
			require.Equal(t, unruledPending, string(stored))

			cost, detail, _, err := priceAmounts(p)
			require.NoError(t, err)
			requireDec(t, "28.1", cost.base, "base")
			requireDec(t, "28.1", cost.list, "list")
			requireDec(t, "70.125", cost.total, "total")
			billed, err := json.Marshal(detail)
			require.NoError(t, err)
			require.Equal(t, unruledBilled, string(billed))
		})
	}
}
