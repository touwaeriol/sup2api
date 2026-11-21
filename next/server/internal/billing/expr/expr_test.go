package expr

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

const claudeTiers = `len <= 200000
  ? tier("standard",     p*3 + c*15   + cr*0.3 + cc*3.75 + cc1h*6)
  : tier("long_context", p*6 + c*22.5 + cr*0.6 + cc*7.5  + cc1h*12)`

func mustCompile(t *testing.T, src string) *Program {
	t.Helper()
	p, err := Compile(src)
	if err != nil {
		t.Fatalf("compile %q: %v", src, err)
	}
	return p
}

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// Beijing 10:00 and 02:00 on 2026-09-24.
var (
	bj10 = time.Date(2026, 9, 24, 2, 0, 0, 0, time.UTC)
	bj02 = time.Date(2026, 9, 23, 18, 0, 0, 0, time.UTC)
)

func TestEvalExamples(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		in      Input
		cost    string
		tier    string
		matched []bool
	}{
		{
			name: "claude standard tier",
			src:  claudeTiers,
			in:   Input{Vars: Vars{P: 1000, C: 500, Len: 1000}},
			cost: "0.0105", tier: "standard",
		},
		{
			name: "claude boundary stays standard",
			src:  claudeTiers,
			in:   Input{Vars: Vars{P: 200000, Len: 200000}},
			cost: "0.6", tier: "standard",
		},
		{
			name: "claude long context by len not p",
			src:  claudeTiers,
			in:   Input{Vars: Vars{P: 50000, CR: 250000, C: 1000, Len: 300000}},
			// 50000*6 + 1000*22.5 + 250000*0.6 = 300000 + 22500 + 150000
			cost: "0.4725", tier: "long_context",
		},
		{
			name: "flat fee with night discount (Beijing 02:00)",
			src:  `tier("base", flat(0.01) + p*3 + c*15) * (hour("Asia/Shanghai") < 8 ? 0.8 : 1)`,
			in:   Input{Vars: Vars{P: 1000, C: 100, Len: 1000}, At: bj02},
			cost: "0.0116", tier: "base",
		},
		{
			name: "flat fee without discount (Beijing 10:00)",
			src:  `tier("base", flat(0.01) + p*3 + c*15) * (hour("Asia/Shanghai") < 8 ? 0.8 : 1)`,
			in:   Input{Vars: Vars{P: 1000, C: 100, Len: 1000}, At: bj10},
			cost: "0.0145", tier: "base",
		},
		{
			name: "param and header rules both hit",
			src:  `(tier("base", p*5 + c*25)) * (param("service_tier") == "priority" ? 1.5 : 1) * (has(header("anthropic-beta"), "fast-mode") ? 2 : 1)`,
			in: Input{Vars: Vars{P: 1000, C: 1000, Len: 1000},
				Params:  map[string]string{"service_tier": `"priority"`},
				Headers: map[string]string{"anthropic-beta": "context-1m,fast-mode-2026"}},
			cost: "0.09", tier: "base", matched: []bool{true, true},
		},
		{
			name: "param and header rules miss",
			src:  `(tier("base", p*5 + c*25)) * (param("service_tier") == "priority" ? 1.5 : 1) * (has(header("anthropic-beta"), "fast-mode") ? 2 : 1)`,
			in:   Input{Vars: Vars{P: 1000, C: 1000, Len: 1000}},
			cost: "0.03", tier: "base", matched: []bool{false, false},
		},
		{
			name: "per request",
			src:  `tier("base", flat(0.04))`,
			in:   Input{},
			cost: "0.04", tier: "base",
		},
		{
			name: "version prefix",
			src:  `v1: tier("base", p*2)`,
			in:   Input{Vars: Vars{P: 500000}},
			cost: "1", tier: "base",
		},
		{
			name: "numeric param comparison",
			src:  `(tier("base", p)) * ((param("max_tokens") ?? 0) > 1000 ? 1.2 : 1) * (param("n") == 2 ? 3 : 1)`,
			in:   Input{Vars: Vars{P: 1e6}, Params: map[string]string{"max_tokens": "4096", "n": "2"}},
			cost: "3.6", tier: "base", matched: []bool{true, true},
		},
		{
			name: "plugin metering value",
			src:  `(tier("images", flat(u("images") * 0.04))) * (u("quality") == "hd" ? 2 : 1)`,
			in:   Input{Metrics: map[string]any{"images": int64(3), "quality": "hd"}},
			cost: "0.24", tier: "images", matched: []bool{true},
		},
		{
			name: "builtins",
			src:  `tier("x", max(p, 1000) * 2 + min(c, 10) + abs(-3) + ceil(0.2) + floor(1.8))`,
			in:   Input{Vars: Vars{P: 10, C: 50}},
			// 2000 + 10 + 3 + 1 + 1
			cost: "0.002015", tier: "x",
		},
		{
			name: "time functions",
			src:  `tier("t", flat(weekday("UTC") == 4 && day("UTC") == 24 && month("UTC") == 9 && minute("UTC") == 30 ? 1 : 0))`,
			in:   Input{At: time.Date(2026, 9, 24, 3, 30, 0, 0, time.UTC)},
			cost: "1", tier: "t",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := mustCompile(t, tc.src)
			res, err := p.Eval(tc.in)
			if err != nil {
				t.Fatalf("eval: %v", err)
			}
			if !res.Cost.Equal(dec(tc.cost)) {
				t.Fatalf("cost = %s, want %s", res.Cost, tc.cost)
			}
			if res.Tier != tc.tier {
				t.Fatalf("tier = %q, want %q", res.Tier, tc.tier)
			}
			if len(tc.matched) > 0 {
				if len(res.Rules) != len(tc.matched) {
					t.Fatalf("rules = %+v", res.Rules)
				}
				for i, m := range tc.matched {
					if res.Rules[i].Matched != m {
						t.Fatalf("rule %d matched = %v, want %v (%+v)", i, res.Rules[i].Matched, m, res.Rules)
					}
				}
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	all := func(string) bool { return true }
	only := func(names ...string) func(string) bool {
		return func(n string) bool {
			for _, x := range names {
				if x == n {
					return true
				}
			}
			return false
		}
	}
	tok := Tokens{Input: 1000, Output: 500, CacheRead: 200, CacheCreation: 50, CacheCreation1h: 30}
	cases := []struct {
		name string
		sem  string
		uses func(string) bool
		want Vars
	}{
		{"inclusive, all priced", SemanticsInclusive, all, Vars{P: 720, C: 500, CR: 200, CC: 50, CC1h: 30, Len: 1000}},
		{"inclusive, unpriced read is free, unpriced writes stay input", SemanticsInclusive, only("p", "c"), Vars{P: 800, C: 500, Len: 1000}},
		{"inclusive, only cr", SemanticsInclusive, only("p", "cr"), Vars{P: 800, C: 500, CR: 200, Len: 1000}},
		{"inclusive, only cc1h: 5m writes stay input", SemanticsInclusive, only("p", "cc1h"), Vars{P: 770, C: 500, CC1h: 30, Len: 1000}},
		{"exclusive, all priced", SemanticsExclusive, all, Vars{P: 1000, C: 500, CR: 200, CC: 50, CC1h: 30, Len: 1280}},
		{"exclusive, unpriced cache is free", SemanticsExclusive, only("p", "c"), Vars{P: 1000, C: 500, Len: 1280}},
		{"exclusive, cc1h falls back to cc", SemanticsExclusive, only("p", "cc"), Vars{P: 1000, C: 500, CC: 80, Len: 1280}},
		{"default semantics is exclusive", "", only("p"), Vars{P: 1000, C: 500, Len: 1280}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Normalize(tc.sem, tok, tc.uses); got != tc.want {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
	// Inclusive totals smaller than the cache counts clamp p at zero.
	if got := Normalize(SemanticsInclusive, Tokens{Input: 10, CacheRead: 50}, all); got.P != 0 {
		t.Fatalf("p = %v", got.P)
	}
}

// Image tokens are part of input and output (CONTRACTS §118): priced, they
// leave p and c for img and img_o; unpriced, they stay in p and c. Either
// way every token is billed once, and p and c never go below 0.
func TestNormalizeImageTokens(t *testing.T) {
	only := func(names ...string) func(string) bool {
		return func(n string) bool { return slices.Contains(names, n) }
	}
	tok := Tokens{Input: 1000, Output: 4200, CacheRead: 100, ImageInput: 600, ImageOutput: 4000}
	for _, tc := range []struct {
		name string
		sem  string
		uses func(string) bool
		want Vars
	}{
		{"inclusive, both priced", SemanticsInclusive, only("p", "c", "cr", "img", "img_o"), Vars{P: 300, C: 200, CR: 100, Img: 600, ImgO: 4000, Len: 1000}},
		{"inclusive, unpriced images stay text", SemanticsInclusive, only("p", "c", "cr"), Vars{P: 900, C: 4200, CR: 100, Len: 1000}},
		{"inclusive, only img", SemanticsInclusive, only("p", "c", "img"), Vars{P: 300, C: 4200, Img: 600, Len: 1000}},
		{"inclusive, only img_o", SemanticsInclusive, only("p", "c", "img_o"), Vars{P: 900, C: 200, ImgO: 4000, Len: 1000}},
		{"exclusive, both priced", SemanticsExclusive, only("p", "c", "img", "img_o"), Vars{P: 400, C: 200, Img: 600, ImgO: 4000, Len: 1100}},
		{"exclusive, unpriced images stay text", SemanticsExclusive, only("p", "c"), Vars{P: 1000, C: 4200, Len: 1100}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Normalize(tc.sem, tok, tc.uses); got != tc.want {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
	// A count above its total clamps p and c at zero.
	got := Normalize(SemanticsInclusive, Tokens{Input: 10, Output: 5, ImageInput: 50, ImageOutput: 9}, only("img", "img_o"))
	if got.P != 0 || got.C != 0 || got.Img != 50 || got.ImgO != 9 {
		t.Fatalf("got %+v", got)
	}
}

func TestAnalysis(t *testing.T) {
	p := mustCompile(t, `(len <= 1000 ? tier("a", p + u("secs")) : (len > 5000 ? tier("b", c) : tier("c", cr*2))) * (param("x.y") == 1 ? 2 : 1) * (has(header("X-Beta"), "f") ? 3 : 1)`)
	if got := strings.Join(p.UsedVars(), ","); got != "p,c,cr,len" {
		t.Fatalf("vars = %s", got)
	}
	if got := strings.Join(p.Params(), ","); got != "x.y" {
		t.Fatalf("params = %s", got)
	}
	if got := strings.Join(p.Headers(), ","); got != "x-beta" {
		t.Fatalf("headers = %s", got)
	}
	if got := strings.Join(p.Facts(), ","); got != "secs" {
		t.Fatalf("facts = %s", got)
	}
	if got := strings.Join(p.TierNames(), ","); got != "a,b,c" {
		t.Fatalf("tiers = %s", got)
	}
	if b := p.LenBoundaries(); len(b) != 2 || b[0] != 1000 || b[1] != 5000 {
		t.Fatalf("bounds = %v", b)
	}
	if conds := p.RuleConds(); len(conds) != 2 || conds[0] != `param("x.y") == 1` {
		t.Fatalf("conds = %v", conds)
	}
}

func TestHash(t *testing.T) {
	a, b := Hash(`tier("base", p*3)`), Hash("  v1: tier(\"base\", p*3) ")
	if a != b || len(a) != 64 {
		t.Fatalf("hash mismatch %s %s", a, b)
	}
	if Hash(`tier("base", p*4)`) == a {
		t.Fatal("different expressions share a hash")
	}
	p1, err := CompileCached(`tier("base", p*3)`)
	if err != nil {
		t.Fatal(err)
	}
	p2, _ := CompileCached(`v1:tier("base", p*3)`)
	if p1 != p2 || p1.Hash() != a || p1.Expression() != `v1:tier("base", p*3)` {
		t.Fatal("cache miss for equivalent expression")
	}
}

func TestCompileErrors(t *testing.T) {
	cases := []struct {
		src  string
		code string
	}{
		{``, CodeEmpty},
		{`v2: p`, CodeVersion},
		{`p *`, CodeSyntax},
		{`foo * 3`, CodeSyntax},
		{`now()`, CodeSyntax},
		{`len("abc")`, CodeSyntax},
		{`tier("x", p) ||| header("a") == "b" ? 2 : 1`, CodeSyntax},
		{`(tier("x", p)) * (header("a") == "b" ? -2 : 1)`, CodeNegativeMultiplier},
		{`tier("x", p) * (__rule(0, header("a") == "b") ? 2 : 1)`, CodeSyntax},
		{`tier("x", p) * ($env.__rule(0, header("a") == "b") ? 2 : 1)`, CodeSyntax},
		{`tier("x", p) * ($env["__rule"](0, true) ? 2 : 1)`, CodeSyntax},
		{`param("a" + "b") == 1 ? p : c`, CodeLiteralArg},
		{`tier(header("x"), p)`, CodeLiteralArg},
		{`p * (hour("Mars/Olympus") > 3 ? 1 : 2)`, CodeTimezone},
		{`p * (hour("Local") > 3 ? 1 : 2)`, CodeTimezone},
		{`header("a") == "b"`, CodeSyntax}, // bool result
	}
	for _, tc := range cases {
		_, err := Compile(tc.src)
		ce, ok := err.(*CompileError)
		if !ok {
			t.Fatalf("%q: err = %v, want CompileError", tc.src, err)
		}
		if ce.Issue.Code != tc.code {
			t.Fatalf("%q: code = %s (%s), want %s", tc.src, ce.Issue.Code, ce.Issue.Detail, tc.code)
		}
		if ce.Issue.Message["en"] == "" || ce.Issue.Message["zh"] == "" {
			t.Fatalf("%q: missing localized message", tc.src)
		}
	}
}

func TestEvalErrors(t *testing.T) {
	p := mustCompile(t, `tier("x", p - 10)`)
	if _, err := p.Eval(Input{}); err == nil {
		t.Fatal("negative result accepted")
	}
	p = mustCompile(t, `tier("x", p / c)`)
	if _, err := p.Eval(Input{}); err == nil {
		t.Fatal("NaN accepted")
	}
	p = mustCompile(t, `(tier("x", p)) * (param("n") > 3 ? 2 : 1)`)
	if _, err := p.Eval(Input{Vars: Vars{P: 1}}); err == nil {
		t.Fatal("nil comparison should fail at runtime")
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		opts     ValidateOptions
		ok       bool
		errCode  string
		warnCode string
	}{
		{name: "claude tiers", src: claudeTiers, ok: true},
		{name: "negative coefficient fails smoke", src: `tier("x", p*3 - c*15)`, errCode: CodeSmoke},
		{name: "division by zero fails smoke", src: `tier("x", p / len)`, errCode: CodeSmoke},
		{name: "missing param without default fails smoke", src: `(tier("x", p)) * (param("n") > 3 ? 2 : 1)`, errCode: CodeSmoke},
		{name: "unknown fact", src: `tier("x", flat(u("secs") * 0.1))`, opts: ValidateOptions{Facts: map[string]bool{"images": true}}, errCode: CodeUnknownFact},
		{name: "declared fact", src: `tier("x", flat(u("secs") * 0.1))`, opts: ValidateOptions{Facts: map[string]bool{"secs": true}}, ok: true},
		{name: "big cost warning", src: `tier("x", p*15)`, ok: true, warnCode: CodeBigCost},
		{name: "rule surcharges on headers do not count", src: `(tier("x", p*6)) * (has(header("a"), "b") ? 2 : 1)`, ok: true},
		{name: "custom threshold", src: `tier("x", p*3)`, opts: ValidateOptions{BigCostUSD: dec("2")}, ok: true, warnCode: CodeBigCost},
		{name: "no tier warning", src: `p*3 + c*15`, ok: true, warnCode: CodeNoTier},
		{name: "duplicate tier warning", src: `len < 10 ? tier("a", p) : tier("a", p*2)`, ok: true, warnCode: CodeDuplicateTier},
		{name: "compile error", src: `p +`, errCode: CodeSyntax},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Validate(tc.src, tc.opts)
			if r.OK != tc.ok {
				t.Fatalf("ok = %v, errors = %+v", r.OK, r.Errors)
			}
			if tc.errCode != "" && (len(r.Errors) == 0 || r.Errors[0].Code != tc.errCode) {
				t.Fatalf("errors = %+v, want %s", r.Errors, tc.errCode)
			}
			if tc.warnCode != "" {
				found := false
				for _, w := range r.Warnings {
					found = found || w.Code == tc.warnCode
				}
				if !found {
					t.Fatalf("warnings = %+v, want %s", r.Warnings, tc.warnCode)
				}
			} else if tc.ok && len(r.Warnings) > 0 {
				t.Fatalf("unexpected warnings %+v", r.Warnings)
			}
			if tc.ok && (r.Samples == 0 || r.Analysis == nil || r.ExprHash == "") {
				t.Fatalf("report incomplete: %+v", r)
			}
		})
	}
}

func TestSmokeSamplesCoverBoundaries(t *testing.T) {
	p := mustCompile(t, claudeTiers)
	samples := SmokeSamples(p)
	var lens []float64
	for _, s := range samples {
		if strings.HasPrefix(s.Name, "len=") {
			lens = append(lens, s.Input.Vars.Len)
		}
	}
	if len(lens) != 3 || lens[0] != 199999 || lens[1] != 200000 || lens[2] != 200001 {
		t.Fatalf("len samples = %v", lens)
	}
	// Boundary samples land on both tiers.
	tiers := map[string]bool{}
	for _, s := range samples {
		res, err := p.Eval(s.Input)
		if err != nil {
			t.Fatal(err)
		}
		tiers[res.Tier] = true
	}
	if !tiers["standard"] || !tiers["long_context"] {
		t.Fatalf("tiers = %v", tiers)
	}
	// 1 zero + 5 vars*3 + 3 "all" + 3 boundaries
	if len(samples) != 22 {
		t.Fatalf("samples = %d", len(samples))
	}
}

func TestResultJSON(t *testing.T) {
	p := mustCompile(t, `tier("base", p*3)`)
	res, err := p.Eval(Input{Vars: Vars{P: 1234}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res)
	if !strings.Contains(string(b), `"cost":"0.003702"`) {
		t.Fatalf("json = %s", b)
	}
}

// Every `cond ? <literal> : 1` whose condition reads a probe is a traced
// rule wherever it is; one the evaluation does not reach is not matched.
func TestRuleTracing(t *testing.T) {
	const src = `(len <= 10 ? tier("s", p * (header("a") == "x" ? 2 : 1)) : tier("l", p * 3) * (param("q") == 1 && has(header("b"), "y") ? 4 : 1))` +
		` * (header("c") == "z" ? 2 : 1.5) * (len > 5 ? 2 : 1) * (u("hd") == true ? 0 : 1)`
	p := mustCompile(t, src)
	want := []string{`header("a") == "x"`, `param("q") == 1 && has(header("b"), "y")`, `u("hd") == true`}
	if got := p.RuleConds(); !slices.Equal(got, want) {
		t.Fatalf("conds = %q", got)
	}
	for _, tc := range []struct {
		name    string
		in      Input
		cost    string
		base    string
		mult    string
		matched []bool
	}{
		// The rule in the short tier is not reached on the long one, and a
		// false left operand of && leaves has() unevaluated.
		{"long tier, header a set", Input{Vars: Vars{P: 1e6, Len: 20}, Headers: map[string]string{"a": "x", "b": "y"}}, "9", "9", "1", []bool{false, false, false}},
		{"long tier, both conditions", Input{Vars: Vars{P: 1e6, Len: 20}, Params: map[string]string{"q": "1"}, Headers: map[string]string{"b": "y"}}, "36", "9", "4", []bool{false, true, false}},
		{"short tier, header a set", Input{Vars: Vars{P: 1e6, Len: 1}, Headers: map[string]string{"a": "x"}}, "3", "1.5", "2", []bool{true, false, false}},
		{"free by usage", Input{Vars: Vars{P: 1e6, Len: 1}, Headers: map[string]string{"a": "x"}, Metrics: map[string]any{"hd": true}}, "0", "1.5", "0", []bool{true, false, true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := p.Eval(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if !res.Cost.Equal(dec(tc.cost)) || !res.Breakdown.Base.Equal(dec(tc.base)) || !res.Breakdown.RulesMultiplier.Equal(dec(tc.mult)) {
				t.Fatalf("cost %s base %s mult %s", res.Cost, res.Breakdown.Base, res.Breakdown.RulesMultiplier)
			}
			for i, m := range tc.matched {
				if res.Rules[i].Matched != m || res.Rules[i].Cond != want[i] {
					t.Fatalf("rules = %+v", res.Rules)
				}
			}
		})
	}
	// A condition that is not a boolean fails as it did before tracing; an
	// absent param is false, as the old engine read it (a migrated rule
	// `param("fast") ? 2 : 1`).
	p = mustCompile(t, `tier("x", p) * (u("flag") ? 2 : 1)`)
	if _, err := p.Eval(Input{Vars: Vars{P: 1}, Metrics: map[string]any{"flag": "yes"}}); err == nil {
		t.Fatal("non-boolean rule condition accepted")
	}
	evalCost := func(src string, in Input) *Result {
		t.Helper()
		res, err := mustCompile(t, src).Eval(in)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := evalCost(`tier("x", p) * (param("fast") ? 2 : 1)`, Input{Vars: Vars{P: 1e6}}); !res.Cost.Equal(dec("1")) || res.Rules[0].Matched {
		t.Fatalf("absent param: cost %s, rules %+v", res.Cost, res.Rules)
	}
	// The multiplier is cost / base: a rule on one term doubles the cost
	// here, not triples it.
	res := evalCost(`tier("x", p*1 + c * (param("f") == 1 ? 3 : 1))`, Input{Vars: Vars{P: 1e6, C: 1e6}, Params: map[string]string{"f": "1"}})
	if !res.Cost.Equal(dec("4")) || !res.Breakdown.Base.Equal(dec("2")) || !res.Breakdown.RulesMultiplier.Equal(dec("2")) {
		t.Fatalf("cost %s base %s mult %s", res.Cost, res.Breakdown.Base, res.Breakdown.RulesMultiplier)
	}
	// Without its rules the price fails; the base is then the cost.
	res = evalCost(`(param("q") == 1 ? 2 : 1) == 2 ? tier("x", p*1) : tier("y", p*1 - 1e9)`, Input{Vars: Vars{P: 1e6}, Params: map[string]string{"q": "1"}})
	if !res.Cost.Equal(dec("1")) || !res.Breakdown.Base.Equal(dec("1")) || !res.Breakdown.RulesMultiplier.Equal(dec("1")) || !res.Rules[0].Matched {
		t.Fatalf("cost %s base %s mult %s", res.Cost, res.Breakdown.Base, res.Breakdown.RulesMultiplier)
	}
}

// LegacyProduct writes the old rule form as factors, as migration 0121 does.
func TestLegacyProduct(t *testing.T) {
	for src, want := range map[string]string{
		"v1 : tier(\"x\", p) ||| param(\"a\") == \"|||\" ? 2 : 1": "(tier(\"x\", p)) * (param(\"a\") == \"|||\" ? 2 : 1)",
		"p // note\n||| header(\"b\") == 'x\\'|||' ? 3 : 1":       "(p // note\n) * (header(\"b\") == 'x\\'|||' ? 3 : 1)",
	} {
		got, ok := LegacyProduct(src)
		if !ok || got != want {
			t.Fatalf("LegacyProduct(%q) = %q, %v", src, got, ok)
		}
		mustCompile(t, got)
	}
	if _, ok := LegacyProduct(`tier("x", p) * (header("a") == "|||" ? 2 : 1)`); ok {
		t.Fatal("rewrote an expression without a separator")
	}
}
