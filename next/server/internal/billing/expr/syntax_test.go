package expr

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIssuePositions(t *testing.T) {
	facts := ValidateOptions{Facts: map[string]bool{"images": true}}
	cases := []struct {
		name   string
		src    string
		opts   ValidateOptions
		code   string
		detail string
		line   int
		column int
	}{
		{name: "unknown name", src: `tier("a", p*2 + prams("x"))`, code: CodeSyntax, detail: "不认识的名字 prams，是不是 param？", line: 1, column: 17},
		{name: "unknown name without suggestion", src: `tier("a", qqqqq)`, code: CodeSyntax, detail: "不认识的名字 qqqqq", line: 1, column: 11},
		{name: "prefix, leading space and lines", src: "\n  v1:\n tier(\"a\",\n  p*2 +\n  cc1 )", code: CodeSyntax, detail: "不认识的名字 cc1，是不是 cc？", line: 5, column: 3},
		{name: "columns count characters", src: `tier("中文", p) + 价格`, code: CodeSyntax, detail: "不认识的名字 价格", line: 1, column: 17},
		{name: "extra right parenthesis", src: `tier("a", p*2))`, code: CodeSyntax, detail: "多了一个右括号", line: 1, column: 15},
		{name: "unclosed left parenthesis", src: "tier(\"a\",\n  (p*2 + c", code: CodeSyntax, detail: "这个左括号没有配对的右括号", line: 2, column: 3},
		{name: "parentheses in strings and comments", src: "tier(\"(\", p) // (\n + c)", code: CodeSyntax, detail: "多了一个右括号", line: 2, column: 5},
		{name: "other expr-lang error", src: `tier("a", p * * 2)`, code: CodeSyntax, detail: `表达式写法不对：unexpected token Operator("*")`, line: 1, column: 15},
		{name: "error without position", src: `header("a") == "b"`, code: CodeSyntax, detail: "表达式写法不对：expected float64, but got bool"},
		{name: "term that is not a number", src: `tier("a", p) + tier("b", "x")`, code: CodeSyntax, detail: "表达式写法不对：expected float64, but got string", line: 1, column: 26},
		{name: "reserved name", src: `tier("a", p) * (__rule(0, true) ? 2 : 1)`, code: CodeSyntax, detail: "不能使用保留名字 __rule", line: 1, column: 17},
		{name: "negative multiplier", src: "tier(\"a\", p)\n* (header(\"x\") == \"y\" ? -2 : 1)", code: CodeNegativeMultiplier, detail: `header("x") == "y" ? -2 : 1`, line: 2, column: 25},
		{name: "time zone", src: `tier("a", p) * (hour("Mars/Olympus") > 3 ? 1 : 2)`, code: CodeTimezone, detail: `hour("Mars/Olympus"): unknown time zone Mars/Olympus`, line: 1, column: 17},
		{name: "unknown fact", src: "tier(\"a\",\n  flat(u(\"secs\") * 0.1) + flat(u(\"secs\")))", opts: facts, code: CodeUnknownFact, detail: "secs", line: 2, column: 8},
		{name: "empty", src: "  ", code: CodeEmpty},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Validate(tc.src, tc.opts)
			if len(r.Errors) == 0 {
				t.Fatalf("no errors, want %s", tc.code)
			}
			got := r.Errors[0]
			if got.Code != tc.code || got.Detail != tc.detail || got.Line != tc.line || got.Column != tc.column {
				t.Fatalf("got %s %q at %d:%d, want %s %q at %d:%d", got.Code, got.Detail, got.Line, got.Column, tc.code, tc.detail, tc.line, tc.column)
			}
			b, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if placed := strings.Contains(string(b), `"line":`); placed != (tc.line > 0) || strings.Contains(string(b), `"column":`) != placed {
				t.Fatalf("json %s: line/column must appear exactly when placed", b)
			}
		})
	}
}

func TestUnknownName(t *testing.T) {
	cases := []struct{ name, want string }{
		{"prams", "不认识的名字 prams，是不是 param？"},
		{"cc1", "不认识的名字 cc1，是不是 cc？"},
		{"minut", "不认识的名字 minut，是不是 minute？"},
		{"img_0", "不认识的名字 img_0，是不是 img_o？"},
		{"flor", "不认识的名字 flor，是不是 floor？"},
		{"headers", "不认识的名字 headers，是不是 header？"},
		{"x", "不认识的名字 x"},         // one edit from p, but nothing like it
		{"价格", "不认识的名字 价格"},       // two edits from p
		{"price", "不认识的名字 price"}, // too far from anything
		{"__rul", "不认识的名字 __rul"}, // the trace function is not suggested
	}
	for _, tc := range cases {
		if got := unknownName(tc.name); got != tc.want {
			t.Errorf("unknownName(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestEditDistance(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"prams", "param", 2},
		{"kitten", "sitting", 3},
		{"价格", "价钱", 1},
	}
	for _, tc := range cases {
		if got := editDistance(tc.a, tc.b); got != tc.want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
