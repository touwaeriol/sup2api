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
			src:  `tier("base", p*5 + c*25) ||| param("service_tier") == "priority" ? 1.5 : 1 ||| has(header("anthropic-beta"), "fast-mode") ? 2 : 1`,
			in: Input{Vars: Vars{P: 1000, C: 1000, Len: 1000},
				Params:  map[string]string{"service_tier": `"priority"`},
				Headers: map[string]string{"anthropic-beta": "context-1m,fast-mode-2026"}},
			cost: "0.09", tier: "base", matched: []bool{true, true},
		},
		{
			name: "param and header rules miss",
			src:  `tier("base", p*5 + c*25) ||| param("service_tier") == "priority" ? 1.5 : 1 ||| has(header("anthropic-beta"), "fast-mode") ? 2 : 1`,
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
			src:  `tier("base", p) ||| (param("max_tokens") ?? 0) > 1000 ? 1.2 : 1 ||| param("n") == 2 ? 3 : 1`,
			in:   Input{Vars: Vars{P: 1e6}, Params: map[string]string{"max_tokens": "4096", "n": "2"}},
			cost: "3.6", tier: "base", matched: []bool{true, true},
		},
		{
			name: "plugin metering value",
			src:  `tier("images", flat(u("images") * 0.04)) ||| u("quality") == "hd" ? 2 : 1`,
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
			name: "rule separator inside string literal",
			src:  `tier("base", p) ||| header("x-tag") == "a|||b" ? 2 : 1`,
			in:   Input{Vars: Vars{P: 1e6}, Headers: map[string]string{"x-tag": "a|||b"}},
			cost: "2", tier: "base", matched: []bool{true},
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

// A.6 preview: input 100000, output 2000, cache read 80000, fast-mode x2,
// Beijing 10:00 -> standard tier, $0.7080.
func TestPreviewExampleA6(t *testing.T) {
	cfg := `{
	  "tiers": [
	    {"name": "long_context", "max_len": null, "prices": {"p": 6, "c": 22.5, "cr": 0.6, "cc": 7.5, "cc1h": 12}, "flat": 0},
	    {"name": "standard", "max_len": 200000, "prices": {"p": 3, "c": 15, "cr": 0.3, "cc": 3.75, "cc1h": 6}, "flat": 0}
	  ],
	  "rules": [
	    {"source": "header", "name": "anthropic-beta", "op": "contains", "value": "fast-mode", "multiplier": 2},
	    {"source": "time", "tz": "Asia/Shanghai", "from_hour": 0, "to_hour": 8, "multiplier": 0.8}
	  ]
	}`
	src, err := Generate(ModeExpression, json.RawMessage(cfg))
	if err != nil {
		t.Fatal(err)
	}
	want := `len <= 200000 ? tier("standard", p*3 + c*15 + cr*0.3 + cc*3.75 + cc1h*6) : tier("long_context", p*6 + c*22.5 + cr*0.6 + cc*7.5 + cc1h*12)` +
		` ||| has(header("anthropic-beta"), "fast-mode") ? 2 : 1 ||| hour("Asia/Shanghai") >= 0 && hour("Asia/Shanghai") < 8 ? 0.8 : 1`
	if src != want {
		t.Fatalf("generated\n%s\nwant\n%s", src, want)
	}
	p := mustCompile(t, src)
	vars := Normalize(SemanticsExclusive, Tokens{Input: 100000, Output: 2000, CacheRead: 80000}, p.Uses)
	if vars.P != 100000 || vars.Len != 180000 || vars.CR != 80000 {
		t.Fatalf("vars = %+v", vars)
	}
	res, err := p.Eval(Input{Vars: vars, Headers: map[string]string{"anthropic-beta": "fast-mode"}, At: bj10})
	if err != nil {
		t.Fatal(err)
	}
	if res.Cost.StringFixed(4) != "0.7080" || res.Tier != "standard" {
		t.Fatalf("cost %s tier %s", res.Cost, res.Tier)
	}
	if !res.Rules[0].Matched || res.Rules[0].Multiplier != 2 || res.Rules[1].Matched {
		t.Fatalf("rules = %+v", res.Rules)
	}
	b := res.Breakdown
	if !b.Subtotal.Equal(dec("0.354")) || !b.Base.Equal(dec("0.354")) || !b.RulesMultiplier.Equal(dec("2")) {
		t.Fatalf("breakdown = %+v", b)
	}
	got := map[string]string{}
	for _, it := range b.Items {
		got[it.Label] = it.Cost.String()
	}
	if got["p"] != "0.3" || got["c"] != "0.03" || got["cr"] != "0.024" || got["cc"] != "0" {
		t.Fatalf("items = %+v", got)
	}
	if b.Items[0].Rate == nil || *b.Items[0].Rate != 3 || *b.Items[0].Quantity != 100000 {
		t.Fatalf("first item = %+v", b.Items[0])
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
	p := mustCompile(t, `len <= 1000 ? tier("a", p + u("secs")) : (len > 5000 ? tier("b", c) : tier("c", cr*2)) ||| param("x.y") == 1 ? 2 : 1 ||| has(header("X-Beta"), "f") ? 3 : 1`)
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
		{`tier("x", p) ||| p`, CodeRuleShape},
		{`tier("x", p) ||| header("a") == "b" ? 2 : 1.5`, CodeRuleShape},
		{`tier("x", p) ||| `, CodeRuleShape},
		{`tier("x", p) ||| header("a") == "b" ? -2 : 1`, CodeNegativeMultiplier},
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
	p = mustCompile(t, `tier("x", p) ||| param("n") > 3 ? 2 : 1`)
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
		{name: "missing param without default fails smoke", src: `tier("x", p) ||| param("n") > 3 ? 2 : 1`, errCode: CodeSmoke},
		{name: "unknown fact", src: `tier("x", flat(u("secs") * 0.1))`, opts: ValidateOptions{Facts: map[string]bool{"images": true}}, errCode: CodeUnknownFact},
		{name: "declared fact", src: `tier("x", flat(u("secs") * 0.1))`, opts: ValidateOptions{Facts: map[string]bool{"secs": true}}, ok: true},
		{name: "big cost warning", src: `tier("x", p*15)`, ok: true, warnCode: CodeBigCost},
		{name: "rule surcharges on headers do not count", src: `tier("x", p*6) ||| has(header("a"), "b") ? 2 : 1`, ok: true},
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

func TestGenerate(t *testing.T) {
	cases := []struct {
		name string
		mode string
		cfg  string
		want string
		err  string
	}{
		{name: "per request", mode: ModePerRequest, cfg: `{"price": 0.04}`, want: `tier("base", flat(0.04))`},
		{name: "per request string", mode: ModePerRequest, cfg: `{"price": "0.01"}`, want: `tier("base", flat(0.01))`},
		{name: "per request missing", mode: ModePerRequest, cfg: `{}`, err: "config.price"},
		{name: "per token haiku", mode: ModePerToken, cfg: `{"p": 1, "c": 5, "cr": 0.1, "cc": 1.25, "cc1h": 2}`,
			want: `tier("base", p*1 + c*5 + cr*0.1 + cc*1.25 + cc1h*2)`},
		{name: "per token cache omitted", mode: ModePerToken, cfg: `{"p": 2.5, "c": 10}`, want: `tier("base", p*2.5 + c*10)`},
		{name: "per token explicit free cache", mode: ModePerToken, cfg: `{"p": 2.5, "c": 10, "cr": 0}`, want: `tier("base", p*2.5 + c*10 + cr*0)`},
		{name: "per token negative", mode: ModePerToken, cfg: `{"p": -1}`, err: "config.p"},
		{name: "three tiers sorted, flat prices on tier, flat fee", mode: ModeExpression, cfg: `{"tiers": [
			{"name": "xl", "max_len": null, "p": 9, "c": 30},
			{"name": "s", "max_len": 32000, "p": 1, "c": 2, "flat": 0.001},
			{"name": "m", "max_len": 128000, "p": 3, "c": 6}]}`,
			want: `len <= 32000 ? tier("s", flat(0.001) + p*1 + c*2) : (len <= 128000 ? tier("m", p*3 + c*6) : tier("xl", p*9 + c*30))`},
		{name: "single tier", mode: ModeExpression, cfg: `{"tiers": [{"name": "base", "prices": {"p": 1, "c": 2}}]}`, want: `tier("base", p*1 + c*2)`},
		{name: "param rules", mode: ModeExpression, cfg: `{"tiers": [{"name": "base", "p": 1}], "rules": [
			{"kind": "param", "path": "service_tier", "op": "eq", "value": "priority", "multiplier": 1.5},
			{"source": "param", "path": "max_tokens", "op": "range", "value": [1000, null], "multiplier": "1.1"},
			{"source": "param", "path": "tools", "op": "contains", "value": "web_search", "multiplier": 1.2},
			{"source": "header", "name": "X-Mode", "op": "eq", "value": "fast", "multiplier": 2}]}`,
			want: `tier("base", p*1 + c*0) ||| param("service_tier") == "priority" ? 1.5 : 1 ||| (param("max_tokens") ?? 0) >= 1000 ? 1.1 : 1 ||| has(param("tools"), "web_search") ? 1.2 : 1 ||| header("x-mode") == "fast" ? 2 : 1`},
		{name: "overnight time rule", mode: ModeExpression, cfg: `{"tiers": [{"name": "base", "p": 1}], "rules": [
			{"source": "time", "tz": "UTC", "from_hour": 22, "to_hour": 6, "multiplier": 0.5}]}`,
			want: `tier("base", p*1 + c*0) ||| (hour("UTC") >= 22 || hour("UTC") < 6) ? 0.5 : 1`},
		{name: "no rest tier", mode: ModeExpression, cfg: `{"tiers": [{"name": "a", "max_len": 10}, {"name": "b", "max_len": 20}]}`, err: "config.tiers"},
		{name: "two rest tiers", mode: ModeExpression, cfg: `{"tiers": [{"name": "a"}, {"name": "b"}]}`, err: "config.tiers"},
		{name: "bad tier name", mode: ModeExpression, cfg: `{"tiers": [{"name": "a b"}]}`, err: "name"},
		{name: "duplicate tier name", mode: ModeExpression, cfg: `{"tiers": [{"name": "a", "max_len": 5}, {"name": "a"}]}`, err: "duplicate"},
		{name: "bad time zone", mode: ModeExpression, cfg: `{"tiers": [{"name": "a"}], "rules": [{"source": "time", "tz": "Nowhere/City", "from_hour": 1, "to_hour": 2, "multiplier": 1}]}`, err: "tz"},
		{name: "header range", mode: ModeExpression, cfg: `{"tiers": [{"name": "a"}], "rules": [{"source": "header", "name": "x", "op": "range", "value": "1", "multiplier": 1}]}`, err: "op"},
		{name: "missing multiplier", mode: ModeExpression, cfg: `{"tiers": [{"name": "a"}], "rules": [{"source": "header", "name": "x", "value": "1"}]}`, err: "multiplier"},
		{name: "unknown mode", mode: "tiered", cfg: `{}`, err: "mode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Generate(tc.mode, json.RawMessage(tc.cfg))
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want containing %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got\n%s\nwant\n%s", got, tc.want)
			}
			if r := Validate(got, ValidateOptions{}); !r.OK {
				t.Fatalf("generated expression invalid: %+v", r.Errors)
			}
		})
	}
}

func TestGeneratedRulesEvaluate(t *testing.T) {
	src, err := Generate(ModeExpression, json.RawMessage(`{"tiers": [{"name": "base", "p": 1}], "rules": [
		{"source": "param", "path": "max_tokens", "op": "range", "value": [1000, 5000], "multiplier": 2},
		{"source": "time", "tz": "UTC", "from_hour": 22, "to_hour": 6, "multiplier": 0.5}]}`))
	if err != nil {
		t.Fatal(err)
	}
	p := mustCompile(t, src)
	at := time.Date(2026, 1, 1, 23, 0, 0, 0, time.UTC)
	res, err := p.Eval(Input{Vars: Vars{P: 1e6}, Params: map[string]string{"max_tokens": "3000"}, At: at})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Cost.Equal(dec("1")) || !res.Rules[0].Matched || !res.Rules[1].Matched {
		t.Fatalf("res = %+v", res)
	}
	res, _ = p.Eval(Input{Vars: Vars{P: 1e6}, Params: map[string]string{"max_tokens": "9000"}, At: at.Add(8 * time.Hour)})
	if !res.Cost.Equal(dec("1")) || res.Rules[0].Matched || res.Rules[1].Matched {
		t.Fatalf("res = %+v", res)
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
