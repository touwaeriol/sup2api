package core

import (
	"testing"

	"github.com/shopspring/decimal"
)

// One rate applies, the most specific set, never a product.
func TestRateLevels(t *testing.T) {
	d := decimal.RequireFromString
	ptr := func(s string) *decimal.Decimal { v := d(s); return &v }
	for _, c := range []struct {
		name         string
		levels       RateLevels
		want, source string
	}{
		{"group base rate", RateLevels{Group: d("0.8")}, "0.8", RateSourceGroup},
		{"group model rate", RateLevels{GroupModel: ptr("1.2"), Group: d("0.8")}, "1.2", RateSourceGroupModel},
		{"segment rate wins over the group model rate", RateLevels{Segment: ptr("0.5"), GroupModel: ptr("1.2"), Group: d("0.8")}, "0.5", RateSourceSegment},
		{"segment model rate wins over all", RateLevels{SegmentModel: ptr("0.3"), Segment: ptr("0.5"), GroupModel: ptr("1.2"), Group: d("0.8")}, "0.3", RateSourceSegmentModel},
		{"a rate of zero is set", RateLevels{Segment: ptr("0"), Group: d("0.8")}, "0", RateSourceSegment},
	} {
		rate, source := c.levels.Rate()
		if !rate.Equal(d(c.want)) || source != c.source {
			t.Errorf("%s: got %s (%s), want %s (%s)", c.name, rate, source, c.want, c.source)
		}
	}
}

// A price is admitted under the endpoint billing type of its kind.
func TestPriceRuleBillingType(t *testing.T) {
	for want, r := range map[string]PriceRule{
		"per_token":   {Mode: "visual"},
		"per_request": {Mode: "visual", PerRequest: true},
		"expression":  {Mode: "expression"},
		"video":       {Mode: "expression", VideoOnly: true},
	} {
		if got := r.BillingType(); got != want {
			t.Errorf("%+v: got %s, want %s", r, got, want)
		}
	}
}

func TestPriceRuleClone(t *testing.T) {
	var none *PriceRule
	if none.Clone() != nil {
		t.Error("no price clones to none")
	}
	scale := decimal.RequireFromString("0.8")
	rule := &PriceRule{ID: 7, Expression: "p", Scale: &scale, Base: &PriceBase{Expression: "g", ExprHash: "h"}}
	c := rule.Clone()
	if c == rule || c.ID != 7 || c.Expression != "p" || !c.Scale.Equal(scale) || *c.Base != *rule.Base {
		t.Fatalf("clone differs: %+v", c)
	}
	*c.Scale = decimal.Zero
	c.Base.Expression = "changed"
	if !rule.Scale.Equal(decimal.RequireFromString("0.8")) || rule.Base.Expression != "g" {
		t.Errorf("changing the clone changed the rule: %s %+v", rule.Scale, rule.Base)
	}
	if c := (&PriceRule{ID: 1}).Clone(); c.Scale != nil || c.Base != nil {
		t.Errorf("a rule without scale or base clones without them: %+v", c)
	}
}

func TestParseRate(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string // "" = rejected
	}{
		{"0", "0"},
		{" 1.5 ", "1.5"},
		{"0.0001", "0.0001"},
		{"999999.9999", "999999.9999"},
		{"1000000", ""},
		{"0.00001", ""},
		{"-1", ""},
		{"", ""},
		{"abc", ""},
	} {
		got, ok := ParseRate(tc.in)
		if ok != (tc.want != "") || (ok && got.String() != tc.want) {
			t.Errorf("ParseRate(%q) = %s, %v; want %q", tc.in, got, ok, tc.want)
		}
	}
}
