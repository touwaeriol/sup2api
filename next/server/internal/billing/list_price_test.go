package billing

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// Global prices a model rate may be folded into.
var (
	perTokenPrice = PriceDef{Mode: "visual", Config: json.RawMessage(`{"tiers": [{"name": "base", "p": 1, "c": 5}], "rules": []}`),
		Expression: `tier("base", p*1 + c*5)`}
	tieredPrice = PriceDef{Mode: "visual",
		Config: json.RawMessage(`{"tiers": [{"name": "standard", "when": {"var": "len", "op": "<=", "value": 200000}, "p": 3, "c": 15},
			{"name": "long", "p": 6, "c": 22.5}], "rules": [{"multiplier": 2, "conds": [{"source": "param", "key": "fast", "op": "eq", "value": true}]}]}`),
		Expression: `(len <= 200000 ? tier("standard", p*3 + c*15) : tier("long", p*6 + c*22.5)) * (param("fast") == true ? 2 : 1)`}
)

// The list price under a group's model price, as the pricing endpoints show
// it: the global price without one, the group's own price with one, also
// without a global price; none without either.
func TestListPrice(t *testing.T) {
	own := &storedRule{Rule: Rule{Mode: "visual", Config: json.RawMessage(`{"tiers":[{"name":"base","p":0.8,"c":4}]}`),
		Expression: `tier("base", p*0.8 + c*4)`}}
	price, source := listPrice(&perTokenPrice, nil)
	require.Equal(t, []any{&perTokenPrice, core.PriceSourceGlobal}, []any{price, source})
	const ownJSON = `{"mode": "visual", "config": {"tiers": [{"name": "base", "p": 0.8, "c": 4}]}, "expression": "tier(\"base\", p*0.8 + c*4)"}`
	for _, global := range []*PriceDef{&perTokenPrice, nil} {
		price, source = listPrice(global, own)
		require.JSONEq(t, ownJSON, jsonOf(t, price))
		require.Equal(t, core.PriceSourceGroup, source)
	}
	price, source = listPrice(nil, nil)
	require.Equal(t, []any{(*PriceDef)(nil), core.PriceSourceGlobal}, []any{price, source})
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

// handWritten is an expression price.
var handWritten = PriceDef{Mode: "expression", Config: json.RawMessage(`{}`), Expression: `tier("base", p*3 + c*15)`}

// export is exportPrice of model "m" with the rates as strings.
func export(price PriceDef, keyRate, modelRate string) (syncPrice, error) {
	return exportPrice("m", price, *dec(keyRate), *dec(modelRate))
}

// What a downstream sup2api imports is the whole list price at the key's
// rate: a model paid at another rate has the ratio of the two folded into
// its price, into the prices of a config where there is one and else around
// the expression.
func TestExportPrice(t *testing.T) {
	p, err := export(handWritten, "1", "1.2")
	require.NoError(t, err)
	require.Equal(t, syncPrice{Model: "m", Mode: "expression", Config: json.RawMessage(`{}`), Expression: `(tier("base", p*3 + c*15)) * 1.2`}, p)
	require.NoError(t, p.compile())

	p, err = export(perTokenPrice, "0.8", "0.4")
	require.NoError(t, err)
	require.Equal(t, `tier("base", p*0.5 + c*2.5)`, p.Expression)
	require.JSONEq(t, `{"tiers": [{"name": "base", "p": 0.5, "c": 2.5}], "rules": []}`, string(p.Config))

	// Free: the expression times 0.
	p, err = export(handWritten, "1.5", "0")
	require.NoError(t, err)
	require.Equal(t, `(tier("base", p*3 + c*15)) * 0`, p.Expression)
	require.Equal(t, "18", costOf(t, handWritten.Expression))
	require.Equal(t, "0", costOf(t, p.Expression))

	// The key's rate changes nothing.
	p, err = export(handWritten, "0.7", "0.70")
	require.NoError(t, err)
	require.Equal(t, syncPrice{Model: "m", Mode: handWritten.Mode, Config: handWritten.Config, Expression: handWritten.Expression}, p)

	// A rate of its own where the key pays nothing has no ratio.
	_, err = export(handWritten, "0", "1")
	require.Error(t, err)
}

// A ratio with no finite decimal is not rounded: both rates go into the
// expression, a visual price's too, and the key's rate gives back what the
// model's rate bills (CONTRACTS §97.6).
func TestExportPriceInexactRatio(t *testing.T) {
	for _, price := range []PriceDef{handWritten, perTokenPrice} {
		p, err := export(price, "0.3", "0.1")
		require.NoError(t, err)
		require.Equal(t, "("+price.Expression+") * 0.1 / 0.3", p.Expression)
		require.Equal(t, []any{"expression", "{}"}, []any{p.Mode, string(p.Config)})
		want := dec(costOf(t, price.Expression)).Mul(*dec("0.1"))
		got := dec(costOf(t, p.Expression)).Mul(*dec("0.3"))
		require.True(t, want.Equal(got.Round(12)), "want %s, got %s", want, got)
	}
}

// Tiers are scaled in the config, request rules are not, and the
// expression is generated from it; a unit=request tier scales its price.
func TestExportPriceScalesTiers(t *testing.T) {
	p, err := export(tieredPrice, "2", "1")
	require.NoError(t, err)
	require.JSONEq(t, `{"tiers": [{"name": "standard", "when": {"var": "len", "op": "<=", "value": 200000}, "p": 1.5, "c": 7.5}, {"name": "long", "p": 3, "c": 11.25}],
		"rules": [{"multiplier": 2, "conds": [{"source": "param", "key": "fast", "op": "eq", "value": true}]}]}`, string(p.Config))
	require.Equal(t, `(len <= 200000 ? tier("standard", p*1.5 + c*7.5) : tier("long", p*3 + c*11.25)) * (param("fast") == true ? 2 : 1)`, p.Expression)

	p, err = export(PriceDef{Mode: "visual", Config: json.RawMessage(`{"tiers": [{"name": "bulk", "unit": "request", "price": 0.05}]}`)}, "1", "2")
	require.NoError(t, err)
	require.Equal(t, `tier("bulk", flat(0.1))`, p.Expression)
}

// A line comment ends before the ")"; an expression that does not compile
// cannot carry the ratio.
func TestExportPriceComments(t *testing.T) {
	for _, comment := range []string{`// don't`, `// 3" display`, "// a `tick", `// list`} {
		quoted := PriceDef{Mode: "expression", Config: json.RawMessage(`{}`), Expression: `tier("base", p*3 + c*15) ` + comment}
		p, err := export(quoted, "1", "2")
		require.NoError(t, err, comment)
		require.Equal(t, "36", costOf(t, p.Expression))
	}
	_, err := export(PriceDef{Mode: "expression", Config: json.RawMessage(`{}`), Expression: `tier("base", p*3 + c*15) /* open`}, "1", "2")
	require.Error(t, err)
}
