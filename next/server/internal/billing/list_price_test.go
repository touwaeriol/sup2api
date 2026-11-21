package billing

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// Global prices a multiplier may be on.
var (
	handWrittenPrice = PriceDef{Mode: "expression", Config: json.RawMessage(`{}`),
		Expression: `(tier("base", p*3 + c*15)) * (param("fast") == true ? 2 : 1)`}
	perTokenPrice = PriceDef{Mode: "visual", Config: json.RawMessage(`{"tiers": [{"name": "base", "p": 1, "c": 5}], "rules": []}`),
		Expression: `tier("base", p*1 + c*5)`}
	tieredPrice = PriceDef{Mode: "visual",
		Config: json.RawMessage(`{"tiers": [{"name": "standard", "when": {"var": "len", "op": "<=", "value": 200000}, "p": 3, "c": 15},
			{"name": "long", "p": 6, "c": 22.5}], "rules": [{"multiplier": 2, "conds": [{"source": "param", "key": "fast", "op": "eq", "value": true}]}]}`),
		Expression: `(len <= 200000 ? tier("standard", p*3 + c*15) : tier("long", p*6 + c*22.5)) * (param("fast") == true ? 2 : 1)`}
	videoPrice = PriceDef{Mode: "expression", Config: json.RawMessage(`{}`), Expression: `tier("video", c * (u("video_input") == true ? 4.3 : 7))`}
)

// The list price a rule makes of a global price, as the pricing endpoints
// show it: a multiplier goes beside the global price, which stays as it is.
func TestListPrice(t *testing.T) {
	scaled := func(base PriceDef, n string) PriceDef {
		base.Multiplier = dec(n)
		return base
	}
	own := &storedRule{Rule: Rule{Kind: core.PriceRuleCustom, Mode: "visual", Config: json.RawMessage(`{"tiers":[{"name":"base","p":0.8,"c":4}]}`)},
		expression: `tier("base", p*0.8 + c*4)`}
	cases := []struct {
		name string
		base PriceDef
		rule string // the multiplier of the rule; "" = no rule
		want PriceDef
	}{
		{"no rule", handWrittenPrice, "", handWrittenPrice},
		{"per token", perTokenPrice, "1.20", scaled(perTokenPrice, "1.2")},
		{"multiplier 0", perTokenPrice, "0", scaled(perTokenPrice, "0")},
		{"multiplier 1", perTokenPrice, "1", scaled(perTokenPrice, "1")},
		{"tiers", tieredPrice, "0.5", scaled(tieredPrice, "0.5")},
		{"video expression", videoPrice, "2", scaled(videoPrice, "2")},
		{"hand-written expression", handWrittenPrice, "2", scaled(handWrittenPrice, "2")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rule *storedRule
			if tc.rule != "" {
				rule = times(tc.rule)
			}
			require.Equal(t, jsonOf(t, tc.want), jsonOf(t, listPrice(tc.base, rule)))
		})
	}
	t.Run("custom price", func(t *testing.T) {
		require.JSONEq(t, `{"mode": "visual", "config": {"tiers": [{"name": "base", "p": 0.8, "c": 4}]}, "expression": "tier(\"base\", p*0.8 + c*4)"}`,
			jsonOf(t, listPrice(perTokenPrice, own)))
	})
	// The multiplier is a decimal string without trailing zeros, there only
	// under a multiplier rule.
	require.JSONEq(t, `{"mode": "visual", "config": {"tiers": [{"name": "base", "p": 1, "c": 5}], "rules": []}, "expression": "tier(\"base\", p*1 + c*5)"}`,
		jsonOf(t, listPrice(perTokenPrice, nil)))
	require.JSONEq(t, `{"mode": "visual", "config": {"tiers": [{"name": "base", "p": 1, "c": 5}], "rules": []}, "expression": "tier(\"base\", p*1 + c*5)", "multiplier": "1.2"}`,
		jsonOf(t, listPrice(perTokenPrice, times("1.20"))))
	require.JSONEq(t, `{"mode": "visual", "config": {"tiers": [{"name": "base", "p": 1, "c": 5}], "rules": []}, "expression": "tier(\"base\", p*1 + c*5)", "multiplier": "0"}`,
		jsonOf(t, listPrice(perTokenPrice, times("0"))))
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

// costOf evaluates an expression for a million input and output tokens.
func costOf(t *testing.T, expression string) string {
	t.Helper()
	prog, err := expr.Compile(expression)
	require.NoError(t, err)
	var in expr.Input
	in.Vars.Set("p", 1_000_000)
	in.Vars.Set("c", 1_000_000)
	res, err := prog.Eval(in)
	require.NoError(t, err)
	return res.Cost.String()
}

// What a downstream sup2api imports is the whole list price: the multiplier
// that listPrice leaves beside the price goes into it, into the prices of a
// config where there is one and else around the expression.
func TestExportPrice(t *testing.T) {
	handWritten := PriceDef{Mode: "expression", Config: json.RawMessage(`{}`), Expression: `tier("base", p*3 + c*15)`}
	stale := customRule("request", `tier("base", flat(1))`)

	p, err := exportPrice("m", handWritten, ruleLevels{group: times("1.2")})
	require.NoError(t, err)
	require.Equal(t, syncPrice{Model: "m", Mode: "expression", Config: json.RawMessage(`{}`), Expression: `(tier("base", p*3 + c*15)) * 1.2`}, p)
	require.NoError(t, p.compile())

	p, err = exportPrice("m", perTokenPrice, ruleLevels{segment: times("0.5"), group: times("1.2")})
	require.NoError(t, err)
	require.Equal(t, `tier("base", p*0.5 + c*2.5)`, p.Expression)
	require.JSONEq(t, `{"tiers": [{"name": "base", "p": 0.5, "c": 2.5}], "rules": []}`, string(p.Config))

	p, err = exportPrice("m", perTokenPrice, ruleLevels{segment: stale})
	require.NoError(t, err)
	require.Equal(t, syncPrice{Model: "m", Mode: perTokenPrice.Mode, Config: perTokenPrice.Config, Expression: perTokenPrice.Expression}, p)

	// Free: the expression times 0.
	p, err = exportPrice("m", handWritten, ruleLevels{group: times("0")})
	require.NoError(t, err)
	require.Equal(t, `(tier("base", p*3 + c*15)) * 0`, p.Expression)
	require.Equal(t, "18", costOf(t, handWritten.Expression))
	require.Equal(t, "0", costOf(t, p.Expression))

	// A multiplier of 1 changes nothing.
	p, err = exportPrice("m", handWritten, ruleLevels{group: times("1.0")})
	require.NoError(t, err)
	require.Equal(t, syncPrice{Model: "m", Mode: handWritten.Mode, Config: handWritten.Config, Expression: handWritten.Expression}, p)

	// Tiers are scaled in the config, request rules are not, and the
	// expression is generated from it.
	p, err = exportPrice("m", tieredPrice, ruleLevels{group: times("0.5")})
	require.NoError(t, err)
	require.JSONEq(t, `{"tiers": [{"name": "standard", "when": {"var": "len", "op": "<=", "value": 200000}, "p": 1.5, "c": 7.5}, {"name": "long", "p": 3, "c": 11.25}],
		"rules": [{"multiplier": 2, "conds": [{"source": "param", "key": "fast", "op": "eq", "value": true}]}]}`, string(p.Config))
	require.Equal(t, `(len <= 200000 ? tier("standard", p*1.5 + c*7.5) : tier("long", p*3 + c*11.25)) * (param("fast") == true ? 2 : 1)`, p.Expression)

	// A unit=request tier and a per-request price scale their price.
	p, err = exportPrice("m", PriceDef{Mode: "visual", Config: json.RawMessage(`{"tiers": [{"name": "bulk", "unit": "request", "price": 0.05}]}`)}, ruleLevels{group: times("2")})
	require.NoError(t, err)
	require.Equal(t, `tier("bulk", flat(0.1))`, p.Expression)
	p, err = exportPrice("m", PriceDef{Mode: "visual", Config: json.RawMessage(`{"tiers": [{"name": "base", "unit": "request", "price": 0.05}]}`)}, ruleLevels{group: times("3")})
	require.NoError(t, err)
	require.Equal(t, `tier("base", flat(0.15))`, p.Expression)

	// A line comment ends before the ")".
	for _, comment := range []string{`// don't`, `// 3" display`, "// a `tick", `// list`} {
		quoted := PriceDef{Mode: "expression", Config: json.RawMessage(`{}`), Expression: `tier("base", p*3 + c*15) ` + comment}
		p, err = exportPrice("m", quoted, ruleLevels{group: times("2")})
		require.NoError(t, err, comment)
		require.Equal(t, "36", costOf(t, p.Expression))
	}
	// An expression that does not compile cannot carry the multiplier.
	_, err = exportPrice("m", PriceDef{Mode: "expression", Config: json.RawMessage(`{}`), Expression: `tier("base", p*3 + c*15) /* open`},
		ruleLevels{group: times("2")})
	require.Error(t, err)
}
