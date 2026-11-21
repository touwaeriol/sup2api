package billing

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

func dec(s string) *decimal.Decimal {
	d := decimal.RequireFromString(s)
	return &d
}

// Global prices by what a custom price may do over them.
var (
	oneTier  = PriceDef{Mode: "per_token", Config: json.RawMessage(`{"tiers": [{"name": "base", "max_len": null, "unit": "token", "p": 1, "c": 5}], "rules": []}`)}
	twoTiers = PriceDef{Mode: "per_token", Config: json.RawMessage(`{"tiers": [{"name": "s", "max_len": 10, "p": 1}, {"name": "l", "p": 2}]}`)}
	ruled    = PriceDef{Mode: "per_token", Config: json.RawMessage(`{"tiers": [{"name": "base", "p": 1}],
		"rules": [{"multiplier": 2, "conds": [{"source": "param", "key": "a", "op": "exists"}]}]}`)}
	perRequestTier = PriceDef{Mode: "per_token", Config: json.RawMessage(`{"tiers": [{"name": "base", "unit": "request", "price": 1}]}`)}
)

func times(n string) *storedRule {
	r := &storedRule{Rule: Rule{Kind: core.PriceRuleMultiplier}}
	r.setScale(decimal.RequireFromString(n))
	return r
}

func customRule(mode, expression string) *storedRule {
	return &storedRule{Rule: Rule{Kind: core.PriceRuleCustom, Mode: mode}, expression: expression, exprHash: "hash of " + expression, exprVersion: 1}
}

// The list price of a query: the segment's rule, the group's rule, the global
// price, first hit; an inactive rule is skipped and nothing applies without a
// global price. The global price comes back as it is, without a source.
func TestSnapshotResolve(t *testing.T) {
	const group, vip, other = 7, 3, 4
	global := core.PriceRule{ID: 11, Model: "m", Mode: "per_token", Expression: `tier("base", p*1 + c*5)`, ExprVersion: 1, ExprHash: "g"}
	video := core.PriceRule{ID: 12, Model: "video", Mode: "expression", Expression: `tier("video", c*7)`, ExprVersion: 1, ExprHash: "v", VideoOnly: true}
	tiered := core.PriceRule{ID: 13, Model: "tiered", Mode: "per_token", Expression: `len <= 10 ? tier("s", p*1 + c*0) : tier("l", p*2 + c*0)`, ExprVersion: 1, ExprHash: "t"}
	own := customRule("per_token", `tier("base", p*2 + c*3)`)
	stale := customRule("per_request", `tier("base", flat(1))`)

	// with returns the global price as a rule changes it.
	with := func(base core.PriceRule, source string, change func(*core.PriceRule)) *core.PriceRule {
		base.Source = source
		if change != nil {
			change(&base)
		}
		return &base
	}
	scaled := func(n string) func(*core.PriceRule) { return func(r *core.PriceRule) { r.Scale = dec(n) } }
	replaced := func(r *core.PriceRule) {
		r.Base = &core.PriceBase{Expression: r.Expression, ExprHash: r.ExprHash}
		r.Expression, r.ExprHash = own.expression, own.exprHash
	}

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
		{name: "inactive segment custom falls to the group", ofGroup: times("1.2"), ofVIP: stale,
			q: core.PriceQuery{Model: "m", GroupID: group, SegmentID: vip}, want: with(global, core.PriceSourceGroup, scaled("1.2"))},
		{name: "inactive group custom falls to the global price", ofGroup: stale,
			q: core.PriceQuery{Model: "m", GroupID: group, SegmentID: vip}, want: with(global, "", nil)},
		{name: "both inactive", ofGroup: stale, ofVIP: stale,
			q: core.PriceQuery{Model: "m", GroupID: group, SegmentID: vip}, want: with(global, "", nil)},
		{name: "a custom price never applies to an expression price", ofGroup: own, ofVIP: times("0.5"),
			q: core.PriceQuery{Model: "video", GroupID: group}, want: with(video, "", nil)},
		{name: "a custom price never applies to a per_token price with tiers", ofGroup: own,
			q: core.PriceQuery{Model: "tiered", GroupID: group}, want: with(tiered, "", nil)},
		{name: "a multiplier keeps the video admission", ofGroup: own, ofVIP: times("0.5"),
			q: core.PriceQuery{Model: "video", GroupID: group, SegmentID: vip}, want: with(video, core.PriceSourceSegment, scaled("0.5"))},
		{name: "no global price, no rule", ofGroup: times("1.2"), ofVIP: own,
			q: core.PriceQuery{Model: "unpriced", GroupID: group, SegmentID: vip}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := &priceSnapshot{byModel: map[string]core.PriceRule{"m": global, "video": video, "tiered": tiered}, rules: ruleSet{},
				bases:  catalog{"m": oneTier, "video": {Mode: "expression", Config: json.RawMessage(`{}`)}, "tiered": twoTiers},
				custom: map[string]string{"m": oneTier.customMode(), "video": "", "tiered": twoTiers.customMode()}}
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
	global := core.PriceRule{ID: 1, Model: "m", Mode: "per_token", Expression: "p", ExprHash: "g"}
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
	perRequest := &PriceDef{Mode: "per_request", Config: json.RawMessage(`{"price": 1}`)}
	expression := &PriceDef{Mode: "expression", Config: json.RawMessage(`{}`)}
	multiplier := func(n string) Rule { return Rule{Kind: "multiplier", Multiplier: &n} }
	tokens := func(config string) Rule {
		return Rule{Kind: "custom", Mode: "per_token", Config: json.RawMessage(config)}
	}
	const cheap = `{"tiers": [{"name": "base", "p": 0.8, "c": 4, "cr": 0.08}], "rules": []}`

	cases := []struct {
		name    string
		rule    Rule
		base    *PriceDef
		wantErr string
		// What is stored of a rule that passes.
		wantMultiplier string
		wantExpression string
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
		{name: "custom over an expression price", rule: tokens(cheap), base: expression, wantErr: "multiplier only"},
		{name: "custom over per_token tiers", rule: tokens(cheap), base: &twoTiers, wantErr: "multiplier only"},
		{name: "custom over per_token rules", rule: tokens(cheap), base: &ruled, wantErr: "multiplier only"},
		{name: "custom over a per_token request tier", rule: tokens(cheap), base: &perRequestTier, wantErr: "multiplier only"},
		{name: "custom in another mode", rule: tokens(cheap), base: perRequest, wantErr: "a custom price is per_request"},
		{name: "custom without a mode", rule: Rule{Kind: "custom", Config: json.RawMessage(`{"price": 1}`)}, base: perRequest, wantErr: "a custom price is per_request"},
		{name: "custom negative price", rule: tokens(`{"tiers": [{"name": "base", "p": 1, "cr": -1}]}`), base: perToken, wantErr: "config.tiers[0].cr"},
		{name: "custom in the old shape", rule: tokens(`{"p": 1, "c": 2}`), base: perToken, wantErr: `unknown field "p"`},
		{name: "custom with two tiers", base: perToken, wantErr: "one tier",
			rule: tokens(`{"tiers": [{"name": "s", "max_len": 10, "p": 1, "c": 1}, {"name": "l", "p": 2, "c": 2}], "rules": []}`)},
		{name: "custom priced per request", base: perToken, wantErr: "one tier",
			rule: tokens(`{"tiers": [{"name": "base", "unit": "request", "price": 1}], "rules": []}`)},
		{name: "custom with a rule", base: perToken, wantErr: "one tier",
			rule: tokens(`{"tiers": [{"name": "base", "p": 1, "c": 1}], "rules": [{"multiplier": 2, "conds": [{"source": "param", "key": "x", "op": "exists"}]}]}`)},
		{name: "custom without prices is not free", base: perToken, wantErr: "with p and c",
			rule: tokens(`{"tiers": [{"name": "base", "max_len": null}], "rules": []}`)},
		{name: "custom without config", rule: Rule{Kind: "custom", Mode: "per_request"}, base: perRequest, wantErr: "config.price: required"},
		{name: "custom per token", base: perToken, rule: tokens(cheap), wantExpression: `tier("base", p*0.8 + c*4 + cr*0.08)`},
		{name: "custom per token with image prices", base: perToken, rule: tokens(`{"tiers": [{"name": "base", "p": 1, "c": 2, "img": 3, "img_o": 4}]}`),
			wantExpression: `tier("base", p*1 + c*2 + img*3 + img_o*4)`},
		{name: "custom per request, free", base: perRequest,
			rule:           Rule{Kind: "custom", Mode: "per_request", Config: json.RawMessage(`{"price": 0}`), Inactive: true},
			wantExpression: `tier("base", flat(0))`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := checkRule(context.Background(), tc.rule, tc.base)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.rule.Kind, got.Kind)
			require.False(t, got.Inactive)
			if tc.rule.Kind == core.PriceRuleMultiplier {
				require.Equal(t, tc.wantMultiplier, *got.Multiplier)
				require.Equal(t, tc.wantMultiplier, got.scale.String())
				require.Empty(t, got.expression)
				return
			}
			require.Equal(t, tc.base.Mode, got.Mode)
			require.Equal(t, tc.rule.Config, got.Config)
			require.Equal(t, tc.wantExpression, got.expression)
			require.NotEmpty(t, got.exprHash)
			require.Equal(t, 1, got.exprVersion)
		})
	}
	zh := core.WithLocale(context.Background(), "zh")
	_, err := checkRule(zh, Rule{Kind: "custom", Mode: "per_token"}, perToken)
	require.EqualError(t, err, "自定义价无效：config.tiers: at least one tier is required")
	_, err = checkRule(zh, tokens(cheap), &twoTiers)
	require.EqualError(t, err, "表达式计费、分档或带加价规则的按 token 计费只能用倍数规则")
}

// A custom price is inactive over a global price that no longer takes one.
func TestRuleInactive(t *testing.T) {
	own := customRule("per_token", `tier("base", p*2)`)
	for base, inactive := range map[*PriceDef]bool{&oneTier: false, &twoTiers: true, &ruled: true, &perRequestTier: true,
		{Mode: "per_request", Config: json.RawMessage(`{"price": 1}`)}: true, {Mode: "expression", Config: json.RawMessage(`{}`)}: true} {
		require.Equal(t, inactive, own.view(base).Inactive, "%s %s", base.Mode, base.Config)
	}
	require.False(t, times("2").view(&twoTiers).Inactive)
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
