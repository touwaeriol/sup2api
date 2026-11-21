package billing

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

func dec(s string) *decimal.Decimal {
	d := decimal.RequireFromString(s)
	return &d
}

// Global prices of each shape: a custom price replaces any of them.
var (
	oneTier  = PriceDef{Mode: "visual", Config: json.RawMessage(`{"tiers": [{"name": "base", "unit": "token", "p": 1, "c": 5}], "rules": []}`)}
	twoTiers = PriceDef{Mode: "visual", Config: json.RawMessage(`{"tiers": [{"name": "s", "when": {"var": "len", "op": "<=", "value": 10}, "p": 1}, {"name": "l", "p": 2}]}`)}
	ruled    = PriceDef{Mode: "visual", Config: json.RawMessage(`{"tiers": [{"name": "base", "p": 1}],
		"rules": [{"multiplier": 2, "conds": [{"source": "param", "key": "a", "op": "exists"}]}]}`)}
	perRequestTier = PriceDef{Mode: "visual", Config: json.RawMessage(`{"tiers": [{"name": "base", "unit": "request", "price": 1}]}`)}
	exprPrice      = PriceDef{Mode: "expression", Config: json.RawMessage(`{}`)}
)

func times(n string) *storedRule {
	r := &storedRule{Rule: Rule{Kind: core.PriceRuleMultiplier}}
	r.setScale(decimal.RequireFromString(n))
	return r
}

// customRule is a custom visual price of expression, with what the
// expression tells (priceTraits).
func customRule(expression string) *storedRule {
	r := &storedRule{Rule: Rule{Kind: core.PriceRuleCustom, Mode: "visual", Expression: expression}, exprHash: "hash of " + expression, exprVersion: 1}
	if prog, err := expr.CompileCached(expression); err == nil {
		r.traits = traitsOf(r.Mode, prog)
	}
	return r
}

// The list price of a query: the segment's rule, the group's rule, the global
// price, first hit; nothing applies without a global price. A custom price
// replaces a global price of any shape, and what its expression tells
// (VideoOnly, PerRequest) replaces the global price's. The global price comes
// back as it is, without a source.
func TestSnapshotResolve(t *testing.T) {
	const group, vip, other = 7, 3, 4
	global := core.PriceRule{ID: 11, Model: "m", Mode: "visual", Expression: `tier("base", p*1 + c*5)`, ExprVersion: 1, ExprHash: "g"}
	video := core.PriceRule{ID: 12, Model: "video", Mode: "expression", Expression: `tier("video", c*7)`, ExprVersion: 1, ExprHash: "v", VideoOnly: true}
	tiered := core.PriceRule{ID: 13, Model: "tiered", Mode: "visual", Expression: `len <= 10 ? tier("s", p*1 + c*0) : tier("l", p*2 + c*0)`, ExprVersion: 1, ExprHash: "t"}
	own := customRule(`tier("base", p*2 + c*3)`)
	perRequest := customRule(`tier("base", flat(1))`)

	// with returns the global price as a rule changes it.
	with := func(base core.PriceRule, source string, change func(*core.PriceRule)) *core.PriceRule {
		base.Source = source
		if change != nil {
			change(&base)
		}
		return &base
	}
	scaled := func(n string) func(*core.PriceRule) { return func(r *core.PriceRule) { r.Scale = dec(n) } }
	replacedBy := func(c *storedRule) func(*core.PriceRule) {
		return func(r *core.PriceRule) {
			r.Base = &core.PriceBase{Expression: r.Expression, ExprHash: r.ExprHash}
			r.Mode, r.Expression, r.ExprHash = c.Mode, c.Expression, c.exprHash
			r.VideoOnly, r.PerRequest = c.traits.videoOnly, c.traits.perRequest
		}
	}
	replaced := replacedBy(own)

	cases := []struct {
		name           string
		ofGroup, ofVIP *storedRule
		q              core.PriceQuery
		want           *core.PriceRule
	}{
		{name: "no rules", q: core.PriceQuery{Model: "m", GroupID: group, SegmentID: vip}, want: with(global, "", nil)},
		{name: "a query without group or segment is the global price", ofGroup: times("1.2"), ofVIP: own,
			q: core.PriceQuery{Model: "m"}, want: with(global, "", nil)},
		{name: "a segment alone selects no rule", ofGroup: times("1.2"), ofVIP: own,
			q: core.PriceQuery{Model: "m", SegmentID: vip}, want: with(global, "", nil)},
		{name: "rules of another group", ofGroup: times("1.2"), ofVIP: own,
			q: core.PriceQuery{Model: "m", GroupID: group + 1, SegmentID: vip}, want: with(global, "", nil)},
		{name: "group multiplier", ofGroup: times("1.2"),
			q: core.PriceQuery{Model: "m", GroupID: group}, want: with(global, core.PriceSourceGroup, scaled("1.2"))},
		{name: "group custom", ofGroup: own,
			q: core.PriceQuery{Model: "m", GroupID: group, SegmentID: vip}, want: with(global, core.PriceSourceGroup, replaced)},
		{name: "segment multiplier before group custom", ofGroup: own, ofVIP: times("0.8"),
			q: core.PriceQuery{Model: "m", GroupID: group, SegmentID: vip}, want: with(global, core.PriceSourceSegment, scaled("0.8"))},
		{name: "segment custom before group multiplier", ofGroup: times("1.2"), ofVIP: own,
			q: core.PriceQuery{Model: "m", GroupID: group, SegmentID: vip}, want: with(global, core.PriceSourceSegment, replaced)},
		{name: "the rule of another segment", ofGroup: times("1.2"), ofVIP: own,
			q: core.PriceQuery{Model: "m", GroupID: group, SegmentID: other}, want: with(global, core.PriceSourceGroup, scaled("1.2"))},
		{name: "a segment rule without the segment", ofVIP: own,
			q: core.PriceQuery{Model: "m", GroupID: group}, want: with(global, "", nil)},
		{name: "multiplier 0 is free", ofGroup: times("0"),
			q: core.PriceQuery{Model: "m", GroupID: group}, want: with(global, core.PriceSourceGroup, scaled("0"))},
		{name: "segment multiplier 1 hides the group's rule", ofGroup: times("2"), ofVIP: times("1"),
			q: core.PriceQuery{Model: "m", GroupID: group, SegmentID: vip}, want: with(global, core.PriceSourceSegment, scaled("1"))},
		{name: "a custom price by the request over one by the token is due per request", ofGroup: perRequest,
			q: core.PriceQuery{Model: "m", GroupID: group}, want: with(global, core.PriceSourceGroup, replacedBy(perRequest))},
		{name: "a custom price replaces an expression price and its video admission", ofGroup: own, ofVIP: times("0.5"),
			q: core.PriceQuery{Model: "video", GroupID: group}, want: with(video, core.PriceSourceGroup, replaced)},
		{name: "a custom price replaces a visual price with tiers", ofGroup: own,
			q: core.PriceQuery{Model: "tiered", GroupID: group}, want: with(tiered, core.PriceSourceGroup, replaced)},
		{name: "a multiplier keeps the video admission", ofGroup: own, ofVIP: times("0.5"),
			q: core.PriceQuery{Model: "video", GroupID: group, SegmentID: vip}, want: with(video, core.PriceSourceSegment, scaled("0.5"))},
		{name: "no global price, no rule", ofGroup: times("1.2"), ofVIP: own,
			q: core.PriceQuery{Model: "unpriced", GroupID: group, SegmentID: vip}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := &priceSnapshot{byModel: map[string]core.PriceRule{"m": global, "video": video, "tiered": tiered}, rules: ruleSet{},
				bases: catalog{"m": oneTier, "video": exprPrice, "tiered": twoTiers}}
			for _, model := range []string{"m", "video", "tiered", "unpriced"} {
				if tc.ofGroup != nil {
					snap.rules[core.PriceQuery{Model: model, GroupID: group}] = tc.ofGroup
				}
				if tc.ofVIP != nil {
					snap.rules[core.PriceQuery{Model: model, GroupID: group, SegmentID: vip}] = tc.ofVIP
				}
			}
			got := snap.resolve(tc.q)
			if tc.want == nil {
				require.Nil(t, got)
				return
			}
			require.Equal(t, tc.want.Scale == nil, got.Scale == nil)
			if tc.want.Scale != nil {
				require.True(t, tc.want.Scale.Equal(*got.Scale), "scale %s", got.Scale)
				got.Scale = tc.want.Scale
			}
			require.Equal(t, tc.want, got)
		})
	}
}

// What a caller does to a resolved price stays out of the snapshot.
func TestSnapshotResolveCopies(t *testing.T) {
	global := core.PriceRule{ID: 1, Model: "m", Mode: "visual", Expression: "p", ExprHash: "g"}
	rule := times("1.2")
	snap := &priceSnapshot{byModel: map[string]core.PriceRule{"m": global},
		rules: ruleSet{{Model: "m", GroupID: 7}: rule}}
	got := snap.resolve(core.PriceQuery{Model: "m", GroupID: 7})
	*got.Scale = decimal.NewFromInt(9)
	got.Expression = "changed"
	require.Equal(t, "1.2", rule.scale.String())
	require.Equal(t, "p", snap.byModel["m"].Expression)
	require.Equal(t, "", snap.byModel["m"].Source)
}

func TestCheckRule(t *testing.T) {
	perToken := &oneTier
	perRequest := &perRequestTier
	expression := &exprPrice
	multiplier := func(n string) Rule { return Rule{Kind: "multiplier", Multiplier: &n} }
	tokens := func(config string) Rule {
		return Rule{Kind: "custom", Mode: "visual", Config: json.RawMessage(config)}
	}
	written := func(src string) Rule { return Rule{Kind: "custom", Mode: "expression", Expression: src} }
	const cheap = `{"tiers": [{"name": "base", "p": 0.8, "c": 4, "cr": 0.08}], "rules": []}`
	const tiered = `{"tiers": [{"name": "s", "when": {"var": "len", "op": "<=", "value": 10}, "p": 1, "c": 1}, {"name": "l", "p": 2, "c": 2}], "rules": []}`
	const withRule = `{"tiers": [{"name": "base", "p": 1, "c": 1}], "rules": [{"multiplier": 2, "conds": [{"source": "param", "key": "x", "op": "exists"}]}]}`

	cases := []struct {
		name    string
		rule    Rule
		base    *PriceDef
		wantErr string
		// What is stored of a rule that passes.
		wantMultiplier string
		wantExpression string
		wantTraits     priceTraits
	}{
		{name: "no global price", rule: multiplier("1"), wantErr: "no global price"},
		{name: "unknown kind", rule: Rule{Kind: "discount"}, base: perToken, wantErr: "a multiplier or a custom price"},
		{name: "multiplier", rule: multiplier("1.20"), base: expression, wantMultiplier: "1.2"},
		{name: "multiplier over tiers", rule: multiplier("1.20"), base: &twoTiers, wantMultiplier: "1.2"},
		{name: "multiplier 0", rule: multiplier("0"), base: perToken, wantMultiplier: "0"},
		{name: "multiplier 1", rule: multiplier("1"), base: perToken, wantMultiplier: "1"},
		{name: "largest multiplier", rule: multiplier("999999.9999"), base: perToken, wantMultiplier: "999999.9999"},
		{name: "multiplier missing", rule: Rule{Kind: "multiplier"}, base: perToken, wantErr: "multiplier must be"},
		{name: "multiplier negative", rule: multiplier("-0.1"), base: perToken, wantErr: "multiplier must be"},
		{name: "multiplier too large", rule: multiplier("1000000"), base: perToken, wantErr: "below 1000000"},
		{name: "multiplier too fine", rule: multiplier("1.00001"), base: perToken, wantErr: "at most 4 decimals"},
		{name: "multiplier that is no number", rule: multiplier("much"), base: perToken, wantErr: "multiplier must be"},
		{name: "custom in an old mode", rule: Rule{Kind: "custom", Mode: "per_request", Config: json.RawMessage(`{"price": 1}`)}, base: perRequest, wantErr: "a custom price is visual or expression"},
		{name: "custom without a mode", rule: Rule{Kind: "custom", Config: json.RawMessage(`{"price": 1}`)}, base: perRequest, wantErr: "a custom price is visual or expression"},
		{name: "custom negative price", rule: tokens(`{"tiers": [{"name": "base", "p": 1, "cr": -1}]}`), base: perToken, wantErr: "config.tiers[0].cr"},
		{name: "custom in the old shape", rule: tokens(`{"p": 1, "c": 2}`), base: perToken, wantErr: `unknown field "p"`},
		{name: "custom without config", rule: Rule{Kind: "custom", Mode: "visual"}, base: perRequest, wantErr: "config.tiers: at least one tier"},
		{name: "custom request tier without a price", rule: tokens(`{"tiers": [{"name": "base", "unit": "request"}]}`), base: perRequest, wantErr: "config.tiers[0].price: required"},
		{name: "custom expression that does not compile", rule: written(`tier("base", p*`), base: perToken, wantErr: "invalid custom price"},
		{name: "custom expression with a config", rule: Rule{Kind: "custom", Mode: "expression", Config: json.RawMessage(cheap), Expression: `p`}, base: perToken, wantErr: "invalid custom price"},
		{name: "custom per token", base: perToken, rule: tokens(cheap), wantExpression: `tier("base", p*0.8 + c*4 + cr*0.08)`},
		{name: "custom per token with image prices", base: perToken, rule: tokens(`{"tiers": [{"name": "base", "p": 1, "c": 2, "img": 3, "img_o": 4}]}`),
			wantExpression: `tier("base", p*1 + c*2 + img*3 + img_o*4)`},
		{name: "custom per request, free", base: perRequest,
			rule:           Rule{Kind: "custom", Mode: "visual", Config: json.RawMessage(`{"tiers": [{"name": "base", "unit": "request", "price": 0}]}`)},
			wantExpression: `tier("base", flat(0))`, wantTraits: priceTraits{perRequest: true}},
		{name: "custom per request over a price by the token", base: perToken,
			rule:           tokens(`{"tiers": [{"name": "base", "unit": "request", "price": 1}], "rules": []}`),
			wantExpression: `tier("base", flat(1))`, wantTraits: priceTraits{perRequest: true}},
		{name: "custom with tiers over one tier", base: perToken, rule: tokens(tiered),
			wantExpression: `len <= 10 ? tier("s", p*1 + c*1) : tier("l", p*2 + c*2)`},
		{name: "custom with a rule over tiers", base: &twoTiers, rule: tokens(withRule),
			wantExpression: `(tier("base", p*1 + c*1)) * (param("x") != nil ? 2 : 1)`},
		{name: "custom over an expression price", base: expression, rule: tokens(cheap), wantExpression: `tier("base", p*0.8 + c*4 + cr*0.08)`},
		{name: "custom expression over a visual price", base: &ruled, rule: written("  tier(\"base\", p*2 + c*3)\n"),
			wantExpression: `tier("base", p*2 + c*3)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := checkRule(context.Background(), tc.rule, tc.base, nil)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.rule.Kind, got.Kind)
			if tc.rule.Kind == core.PriceRuleMultiplier {
				require.Equal(t, tc.wantMultiplier, *got.Multiplier)
				require.Equal(t, tc.wantMultiplier, got.scale.String())
				require.Empty(t, got.Expression)
				return
			}
			require.Equal(t, tc.rule.Mode, got.Mode)
			if tc.rule.Mode == "visual" {
				require.Equal(t, tc.rule.Config, got.Config)
			} else {
				require.JSONEq(t, `{}`, string(got.Config))
			}
			require.Equal(t, tc.wantExpression, got.Expression)
			require.Equal(t, tc.wantTraits, got.traits)
			require.NotEmpty(t, got.exprHash)
			require.Equal(t, 1, got.exprVersion)
		})
	}
	zh := core.WithLocale(context.Background(), "zh")
	_, err := checkRule(zh, Rule{Kind: "custom", Mode: "visual"}, perToken, nil)
	require.EqualError(t, err, "自定义价无效：config.tiers: at least one tier is required")
	_, err = checkRule(zh, Rule{Kind: "custom", Mode: "per_token"}, perToken, nil)
	require.EqualError(t, err, "自定义价的计费方式须是可视化或表达式")
}

// Precharge charges at the rate times the scale of the rule, as settlement does.
func TestQuoteRecordAppliesRuleScale(t *testing.T) {
	r := &core.UsageRecord{
		Price:          &core.PriceRule{Expression: `tier("main", p*2)`, Scale: dec("0.5")},
		UsageSemantics: "exclusive", Tokens: core.UsageTokens{Input: 1_000_000}, RateMultiplier: decimal.NewFromInt(3),
		Additional: []core.PricedUsage{{Kind: "advisor", Model: "other", Price: &core.PriceRule{Expression: `tier("other", c*10)`, Scale: dec("0")},
			UsageSemantics: "exclusive", Tokens: core.UsageTokens{Output: 1_000_000}, RateMultiplier: decimal.NewFromInt(3)}},
	}
	amount, err := quoteRecord(context.Background(), &Service{}, r)
	require.NoError(t, err)
	require.Equal(t, "3", amount.String())
}
