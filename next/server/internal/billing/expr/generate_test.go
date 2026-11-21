package expr

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// productForm is how migration 0121 rewrote an expression of the old
// "base ||| rule ||| ..." form: "(base) * (rule) * ...".
func productForm(old string) string {
	parts := strings.Split(old, " ||| ")
	if len(parts) == 1 {
		return old
	}
	return "(" + strings.Join(parts, ") * (") + ")"
}

// The old generator's output (captured from it before it was replaced) for
// the old configs that migration 0121 turns into these per_token configs:
// a price without rules generates the same text, and so keeps its expr_hash;
// one with rules generates the text the migration rewrote it to.
func TestGenerateMatchesOldGenerator(t *testing.T) {
	cases := []struct {
		name, config, old string
	}{
		{"old per_token, all prices", `{"tiers":[{"name":"base","max_len":null,"unit":"token","p":3,"c":15,"cr":0.3,"cc":3.75,"cc1h":6}],"rules":[]}`,
			`tier("base", p*3 + c*15 + cr*0.3 + cc*3.75 + cc1h*6)`},
		{"old per_token, cache omitted", `{"tiers":[{"name":"base","max_len":null,"unit":"token","p":2.5,"c":10}],"rules":[]}`,
			`tier("base", p*2.5 + c*10)`},
		{"old per_token, free cache read", `{"tiers":[{"name":"base","max_len":null,"unit":"token","p":2.5,"c":10,"cr":0}],"rules":[]}`,
			`tier("base", p*2.5 + c*10 + cr*0)`},
		{"old per_token, no input price", `{"tiers":[{"name":"base","max_len":null,"unit":"token","c":5}],"rules":[]}`,
			`tier("base", p*0 + c*5)`},
		{"old per_token, numeric strings", `{"tiers":[{"name":"base","max_len":null,"unit":"token","p":"0.15","c":"0.6","cc":"0.1875"}],"rules":[]}`,
			`tier("base", p*0.15 + c*0.6 + cc*0.1875)`},
		{"old per_token, empty", `{"tiers":[{"name":"base","max_len":null,"unit":"token"}],"rules":[]}`,
			`tier("base", p*0 + c*0)`},
		{"old per_token, tiny and large", `{"tiers":[{"name":"base","max_len":null,"unit":"token","p":0.000001,"c":1e-7,"cr":1234567.5}],"rules":[]}`,
			`tier("base", p*0.000001 + c*0.0000001 + cr*1234567.5)`},
		{"old visual, one tier", `{"tiers":[{"name":"base","max_len":null,"unit":"token","p":1,"c":2}],"rules":[]}`,
			`tier("base", p*1 + c*2)`},
		{"old visual, synced 200K tiers", `{"tiers":[{"name":"standard","max_len":200000,"unit":"token","p":3,"c":15,"cr":0.3,"cc":3.75,"cc1h":6},{"name":"long_context","max_len":null,"unit":"token","p":6,"c":22.5,"cr":0.6,"cc":7.5,"cc1h":12}],"rules":[]}`,
			`len <= 200000 ? tier("standard", p*3 + c*15 + cr*0.3 + cc*3.75 + cc1h*6) : tier("long_context", p*6 + c*22.5 + cr*0.6 + cc*7.5 + cc1h*12)`},
		{"old visual, three unsorted tiers", `{"tiers":[{"name":"rest","max_len":null,"unit":"token","p":4,"c":8},{"name":"small","max_len":32000,"unit":"token","p":1,"c":2,"cr":0.1},{"name":"mid","max_len":128000,"unit":"token","p":2,"c":4}],"rules":[]}`,
			`len <= 32000 ? tier("small", p*1 + c*2 + cr*0.1) : (len <= 128000 ? tier("mid", p*2 + c*4) : tier("rest", p*4 + c*8))`},
		{"old visual, every single-condition rule", `{"tiers":[{"name":"base","max_len":null,"unit":"token","p":1,"c":2}],"rules":[
			{"multiplier":2,"conds":[{"source":"header","key":"Anthropic-Beta","op":"contains","value":"fast-mode"}]},
			{"multiplier":1.5,"conds":[{"source":"header","key":"x-tier","op":"eq","value":"pro"}]},
			{"multiplier":2,"conds":[{"source":"param","key":"service_tier","op":"eq","value":"priority"}]},
			{"multiplier":0.5,"conds":[{"source":"param","key":"model","op":"contains","value":"mini"}]},
			{"multiplier":3,"conds":[{"source":"param","key":"n","op":"eq","value":4}]},
			{"multiplier":1.1,"conds":[{"source":"param","key":"stream","op":"eq","value":true}]},
			{"multiplier":0.5,"conds":[{"source":"time","tz":"Asia/Shanghai","from_hour":0,"to_hour":8}]},
			{"multiplier":0.8,"conds":[{"source":"time","from_hour":22,"to_hour":6}]},
			{"multiplier":1.2,"conds":[{"source":"time","tz":"UTC","from_hour":9,"to_hour":24}]}]}`,
			`tier("base", p*1 + c*2) ||| has(header("anthropic-beta"), "fast-mode") ? 2 : 1 ||| header("x-tier") == "pro" ? 1.5 : 1 ||| param("service_tier") == "priority" ? 2 : 1 ||| has(param("model"), "mini") ? 0.5 : 1 ||| param("n") == 4 ? 3 : 1 ||| param("stream") == true ? 1.1 : 1 ||| hour("Asia/Shanghai") >= 0 && hour("Asia/Shanghai") < 8 ? 0.5 : 1 ||| (hour("UTC") >= 22 || hour("UTC") < 6) ? 0.8 : 1 ||| hour("UTC") >= 9 && hour("UTC") < 24 ? 1.2 : 1`},
		{"old visual, tiers and a free rule", `{"tiers":[{"name":"a","max_len":1000,"unit":"token","p":1},{"name":"b","max_len":null,"unit":"token","p":2}],"rules":[{"multiplier":0,"conds":[{"source":"header","key":"x","op":"contains","value":"y"}]}]}`,
			`len <= 1000 ? tier("a", p*1 + c*0) : tier("b", p*2 + c*0) ||| has(header("x"), "y") ? 0 : 1`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Generate(ModePerToken, json.RawMessage(tc.config))
			if err != nil {
				t.Fatal(err)
			}
			if want := productForm(tc.old); got != want {
				t.Fatalf("got\n%s\nwant\n%s", got, want)
			}
			if !strings.Contains(tc.old, "|||") && got != tc.old {
				t.Fatalf("a price without rules changed its expression")
			}
			p := mustCompile(t, got)
			if n := strings.Count(tc.old, "|||"); len(p.RuleConds()) != n {
				t.Fatalf("traced rules = %v, want %d", p.RuleConds(), n)
			}
		})
	}
	// per_request did not change at all.
	if got, err := Generate(ModePerRequest, json.RawMessage(`{"price":0.04}`)); err != nil || got != `tier("base", flat(0.04))` {
		t.Fatalf("per_request = %q, %v", got, err)
	}
}

func TestGenerate(t *testing.T) {
	const base = `"tiers":[{"name":"base","p":3,"c":15}]`
	rule := func(conds string) string {
		return `{` + base + `,"rules":[{"name":"r","multiplier":2,"conds":[` + conds + `]}]}`
	}
	cases := []struct {
		name string
		mode string
		cfg  string
		want string
		err  string
	}{
		{name: "per request", mode: ModePerRequest, cfg: `{"price": "0.01"}`, want: `tier("base", flat(0.01))`},
		{name: "per request missing", mode: ModePerRequest, cfg: `{}`, err: "config.price"},
		{name: "per request with rules", mode: ModePerRequest,
			cfg:  `{"price":0.04,"rules":[{"multiplier":2,"conds":[{"source":"param","key":"quality","op":"eq","value":"hd"}]},{"multiplier":0.5,"conds":[{"source":"time","tz":"Asia/Shanghai","from_hour":0,"to_hour":8}]}]}`,
			want: `(tier("base", flat(0.04))) * (param("quality") == "hd" ? 2 : 1) * (hour("Asia/Shanghai") >= 0 && hour("Asia/Shanghai") < 8 ? 0.5 : 1)`},
		{name: "per request rule without multiplier", mode: ModePerRequest, cfg: `{"price":1,"rules":[{"conds":[{"source":"param","key":"x","op":"exists"}]}]}`, err: "config.rules[0].multiplier"},
		{name: "expression takes {}", mode: ModeExpression, cfg: `{}`, want: ``},
		{name: "expression takes no tiers", mode: ModeExpression, cfg: `{"tiers":[{"name":"a"}]}`, err: "must be {}"},
		{name: "expression takes no video", mode: ModeExpression, cfg: `{"video":{"price_per_million_tokens":7}}`, err: "must be {}"},
		{name: "old per_token shape", mode: ModePerToken, cfg: `{"p": 1, "c": 2}`, err: `unknown field "p"`},
		{name: "old nested prices", mode: ModePerToken, cfg: `{"tiers": [{"name": "b", "prices": {"p": 1}}]}`, err: `unknown field "prices"`},
		{name: "old flat fee", mode: ModePerToken, cfg: `{"tiers": [{"name": "b", "p": 1, "flat": 0.01}]}`, err: `unknown field "flat"`},
		{name: "misspelt price", mode: ModePerToken, cfg: `{"tiers": [{"name": "b", "pp": 1}]}`, err: `unknown field "pp"`},
		{name: "exists with a value", mode: ModePerToken, cfg: rule(`{"source":"param","key":"x","op":"exists","value":"y"}`), err: "exists takes no value"},
		{name: "a param with a time zone", mode: ModePerToken, cfg: rule(`{"source":"param","key":"x","op":"eq","value":"y","tz":"UTC"}`), err: "only a time condition"},
		{name: "a time window with an op", mode: ModePerToken, cfg: rule(`{"source":"time","op":"eq","from_hour":1,"to_hour":2}`), err: "tz, from_hour and to_hour only"},
		{name: "unit request tier", mode: ModePerToken, cfg: `{"tiers":[{"name":"bulk","unit":"request","price":0.05}]}`, want: `tier("bulk", flat(0.05))`},
		{name: "request and token tiers, image prices", mode: ModePerToken,
			cfg:  `{"tiers":[{"name":"rest","p":1,"c":2,"img":10,"img_o":40},{"name":"small","max_len":1000,"unit":"request","price":0.01}]}`,
			want: `len <= 1000 ? tier("small", flat(0.01)) : tier("rest", p*1 + c*2 + img*10 + img_o*40)`},
		{name: "every source in one rule", mode: ModePerToken, cfg: rule(`
			{"source":"param","key":"service_tier","op":"eq","value":"priority"},
			{"source":"usage","key":"service_tier","op":"eq","value":"priority"},
			{"source":"header","key":"Anthropic-Beta","op":"contains","value":"fast-mode"},
			{"source":"time","tz":"Asia/Shanghai","from_hour":0,"to_hour":8}`),
			want: `(tier("base", p*3 + c*15)) * (param("service_tier") == "priority" && u("service_tier") == "priority" && has(header("anthropic-beta"), "fast-mode") && hour("Asia/Shanghai") >= 0 && hour("Asia/Shanghai") < 8 ? 2 : 1)`},
		{name: "eq and ne", mode: ModePerToken, cfg: rule(`{"source":"param","key":"n","op":"ne","value":1},{"source":"header","key":"x","op":"ne","value":"a"},{"source":"usage","key":"hd","op":"eq","value":true}`),
			want: `(tier("base", p*3 + c*15)) * (param("n") != 1 && header("x") != "a" && u("hd") == true ? 2 : 1)`},
		{name: "contains", mode: ModePerToken, cfg: rule(`{"source":"usage","key":"tier","op":"contains","value":"pri"}`),
			want: `(tier("base", p*3 + c*15)) * (has(u("tier"), "pri") ? 2 : 1)`},
		{name: "exists", mode: ModePerToken, cfg: rule(`{"source":"param","key":"tools","op":"exists"},{"source":"header","key":"x-a","op":"exists"},{"source":"usage","key":"secs","op":"exists"}`),
			want: `(tier("base", p*3 + c*15)) * (param("tools") != nil && header("x-a") != "" && u("secs") != 0 ? 2 : 1)`},
		{name: "numeric comparisons", mode: ModePerToken, cfg: rule(`{"source":"param","key":"max_tokens","op":"gt","value":1000},{"source":"param","key":"n","op":"gte","value":"2"},{"source":"usage","key":"secs","op":"lt","value":5},{"source":"usage","key":"w","op":"lte","value":1920.5}`),
			want: `(tier("base", p*3 + c*15)) * ((param("max_tokens") ?? 0) > 1000 && (param("n") ?? 0) >= 2 && u("secs") < 5 && u("w") <= 1920.5 ? 2 : 1)`},
		{name: "two rules over tiers", mode: ModePerToken,
			cfg:  `{"tiers":[{"name":"s","max_len":10,"p":1},{"name":"l","p":2}],"rules":[{"multiplier":1.5,"conds":[{"source":"param","key":"a","op":"exists"}]},{"multiplier":0,"conds":[{"source":"header","key":"b","op":"eq","value":"c"}]}]}`,
			want: `(len <= 10 ? tier("s", p*1 + c*0) : tier("l", p*2 + c*0)) * (param("a") != nil ? 1.5 : 1) * (header("b") == "c" ? 0 : 1)`},

		{name: "no tiers", mode: ModePerToken, cfg: `{"tiers":[]}`, err: "config.tiers"},
		{name: "one tier with max_len", mode: ModePerToken, cfg: `{"tiers":[{"name":"a","max_len":10}]}`, err: "exactly one tier"},
		{name: "no rest tier", mode: ModePerToken, cfg: `{"tiers":[{"name":"a","max_len":10},{"name":"b","max_len":20}]}`, err: "exactly one tier"},
		{name: "two rest tiers", mode: ModePerToken, cfg: `{"tiers":[{"name":"a"},{"name":"b"}]}`, err: "exactly one tier"},
		{name: "duplicate max_len", mode: ModePerToken, cfg: `{"tiers":[{"name":"a","max_len":10},{"name":"b","max_len":10},{"name":"c"}]}`, err: "duplicate max_len"},
		{name: "bad tier name", mode: ModePerToken, cfg: `{"tiers":[{"name":"a b"}]}`, err: "config.tiers[0].name"},
		{name: "duplicate tier name", mode: ModePerToken, cfg: `{"tiers":[{"name":"a","max_len":5},{"name":"a"}]}`, err: "duplicate tier"},
		{name: "negative price", mode: ModePerToken, cfg: `{"tiers":[{"name":"a","img_o":-1}]}`, err: "config.tiers[0].img_o"},
		{name: "unknown unit", mode: ModePerToken, cfg: `{"tiers":[{"name":"a","unit":"second"}]}`, err: "config.tiers[0].unit"},
		{name: "request tier with token prices", mode: ModePerToken, cfg: `{"tiers":[{"name":"a","unit":"request","price":1,"p":2}]}`, err: "no token prices"},
		{name: "request tier without price", mode: ModePerToken, cfg: `{"tiers":[{"name":"a","unit":"request"}]}`, err: "config.tiers[0].price"},
		{name: "token tier with price", mode: ModePerToken, cfg: `{"tiers":[{"name":"a","price":1}]}`, err: "config.tiers[0].price"},
		{name: "missing multiplier", mode: ModePerToken, cfg: `{` + base + `,"rules":[{"conds":[{"source":"param","key":"a","op":"exists"}]}]}`, err: "config.rules[0].multiplier"},
		{name: "negative multiplier", mode: ModePerToken, cfg: `{` + base + `,"rules":[{"multiplier":-1,"conds":[{"source":"param","key":"a","op":"exists"}]}]}`, err: "config.rules[0].multiplier"},
		{name: "rule without conditions", mode: ModePerToken, cfg: `{` + base + `,"rules":[{"multiplier":2,"conds":[]}]}`, err: "config.rules[0].conds"},
		{name: "unknown source", mode: ModePerToken, cfg: rule(`{"source":"body","key":"a","op":"exists"}`), err: "config.rules[0].conds[0].source"},
		{name: "unknown op", mode: ModePerToken, cfg: rule(`{"source":"param","key":"a","op":"range","value":[1,2]}`), err: "config.rules[0].conds[0].op"},
		{name: "missing op", mode: ModePerToken, cfg: rule(`{"source":"param","key":"a","value":"b"}`), err: "conds[0].op"},
		{name: "missing key", mode: ModePerToken, cfg: rule(`{"source":"usage","op":"exists"}`), err: "conds[0].key"},
		{name: "bad header name", mode: ModePerToken, cfg: rule(`{"source":"header","key":"a b","op":"exists"}`), err: "conds[0].key"},
		{name: "header compared as a number", mode: ModePerToken, cfg: rule(`{"source":"header","key":"x","op":"gt","value":1}`), err: "conds[0].op"},
		{name: "header eq a number", mode: ModePerToken, cfg: rule(`{"source":"header","key":"x","op":"eq","value":1}`), err: "conds[0].value"},
		{name: "contains nothing", mode: ModePerToken, cfg: rule(`{"source":"param","key":"x","op":"contains","value":""}`), err: "conds[0].value"},
		{name: "gt a word", mode: ModePerToken, cfg: rule(`{"source":"usage","key":"x","op":"gt","value":"abc"}`), err: "conds[0].value"},
		{name: "eq an object", mode: ModePerToken, cfg: rule(`{"source":"param","key":"x","op":"eq","value":{}}`), err: "conds[0].value"},
		{name: "bad time zone", mode: ModePerToken, cfg: rule(`{"source":"time","tz":"Nowhere/City","from_hour":1,"to_hour":2}`), err: "conds[0].tz"},
		{name: "time without hours", mode: ModePerToken, cfg: rule(`{"source":"time","tz":"UTC"}`), err: "from_hour and to_hour"},
		{name: "empty time window", mode: ModePerToken, cfg: rule(`{"source":"time","from_hour":3,"to_hour":3}`), err: "from_hour != to_hour"},
		{name: "unknown mode", mode: "tiered", cfg: `{}`, err: "mode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Generate(tc.mode, json.RawMessage(tc.cfg))
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want containing %q", err, tc.err)
				}
				if _, ok := err.(*ConfigError); !ok {
					t.Fatalf("err is %T, not a *ConfigError", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got\n%s\nwant\n%s", got, tc.want)
			}
			if got == "" {
				return
			}
			if r := Validate(got, ValidateOptions{}); !r.OK {
				t.Fatalf("generated expression invalid: %+v", r.Errors)
			}
		})
	}
}

func TestGeneratedRulesEvaluate(t *testing.T) {
	src, err := Generate(ModePerToken, json.RawMessage(`{"tiers":[{"name":"base","p":1}],"rules":[
		{"multiplier":2,"conds":[{"source":"param","key":"max_tokens","op":"gte","value":1000},{"source":"usage","key":"hd","op":"eq","value":true}]},
		{"multiplier":0.5,"conds":[{"source":"time","tz":"UTC","from_hour":22,"to_hour":6}]},
		{"multiplier":3,"conds":[{"source":"header","key":"x-a","op":"exists"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	p := mustCompile(t, src)
	night := time.Date(2026, 1, 1, 23, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		in      Input
		cost    string
		matched []bool
	}{
		{"every condition of the first rule, at night", Input{Vars: Vars{P: 1e6}, Params: map[string]string{"max_tokens": "3000"}, Metrics: map[string]any{"hd": true}, At: night},
			"1", []bool{true, true, false}},
		{"one condition of the first rule fails", Input{Vars: Vars{P: 1e6}, Params: map[string]string{"max_tokens": "3000"}, Metrics: map[string]any{"hd": false}, At: night},
			"0.5", []bool{false, true, false}},
		{"no param reads 0", Input{Vars: Vars{P: 1e6}, Metrics: map[string]any{"hd": true}, At: night.Add(8 * time.Hour)},
			"1", []bool{false, false, false}},
		{"a header is there", Input{Vars: Vars{P: 1e6}, Headers: map[string]string{"x-a": "1"}, At: night.Add(8 * time.Hour)},
			"3", []bool{false, false, true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := p.Eval(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if !res.Cost.Equal(dec(tc.cost)) {
				t.Fatalf("cost = %s, want %s", res.Cost, tc.cost)
			}
			for i, m := range tc.matched {
				if res.Rules[i].Matched != m {
					t.Fatalf("rules = %+v", res.Rules)
				}
			}
			if !res.Breakdown.Base.Equal(dec("1")) {
				t.Fatalf("base = %s", res.Breakdown.Base)
			}
		})
	}
}

// A.6 preview: input 100000, output 2000, cache read 80000, fast-mode x2,
// Beijing 10:00 -> standard tier, $0.7080.
func TestPreviewExampleA6(t *testing.T) {
	cfg := `{
	  "tiers": [
	    {"name": "long_context", "max_len": null, "p": 6, "c": 22.5, "cr": 0.6, "cc": 7.5, "cc1h": 12},
	    {"name": "standard", "max_len": 200000, "unit": "token", "p": 3, "c": 15, "cr": 0.3, "cc": 3.75, "cc1h": 6}
	  ],
	  "rules": [
	    {"name": "fast", "multiplier": 2, "conds": [{"source": "header", "key": "anthropic-beta", "op": "contains", "value": "fast-mode"}]},
	    {"name": "night", "multiplier": 0.8, "conds": [{"source": "time", "tz": "Asia/Shanghai", "from_hour": 0, "to_hour": 8}]}
	  ]
	}`
	src, err := Generate(ModePerToken, json.RawMessage(cfg))
	if err != nil {
		t.Fatal(err)
	}
	want := `(len <= 200000 ? tier("standard", p*3 + c*15 + cr*0.3 + cc*3.75 + cc1h*6) : tier("long_context", p*6 + c*22.5 + cr*0.6 + cc*7.5 + cc1h*12))` +
		` * (has(header("anthropic-beta"), "fast-mode") ? 2 : 1) * (hour("Asia/Shanghai") >= 0 && hour("Asia/Shanghai") < 8 ? 0.8 : 1)`
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
