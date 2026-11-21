package core

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestSegmentRate(t *testing.T) {
	d := decimal.RequireFromString
	ptr := func(s string) *decimal.Decimal { v := d(s); return &v }
	for _, c := range []struct {
		name            string
		group, discount string
		override        *decimal.Decimal
		want, source    string
	}{
		{"group default", "0.8", "1", nil, "0.8", RateSourceGroup},
		{"discount", "0.8", "0.9", nil, "0.72", RateSourceDiscount},
		{"override wins over the discount", "0.8", "0.9", ptr("0.65"), "0.65", RateSourceSegment},
		{"override of zero is free", "0.8", "0.9", ptr("0"), "0", RateSourceSegment},
		{"discount keeps four decimals", "0.8333", "0.95", nil, "0.7916", RateSourceDiscount},
		{"discount rounds half up", "0.3333", "0.5", nil, "0.1667", RateSourceDiscount},
		{"discount on a free group", "0", "0.9", nil, "0", RateSourceDiscount},
		{"capped at what the column holds", "999999", "999999", nil, "999999.9999", RateSourceDiscount},
	} {
		rate, source := SegmentRate(d(c.group), d(c.discount), c.override)
		if !rate.Equal(d(c.want)) || source != c.source {
			t.Errorf("%s: got %s (%s), want %s (%s)", c.name, rate, source, c.want, c.source)
		}
	}
}

func TestPriceRuleMultiplier(t *testing.T) {
	d := decimal.RequireFromString
	rate := d("0.72")
	var none *PriceRule
	if got := none.Multiplier(rate); !got.Equal(rate) {
		t.Errorf("no price: got %s", got)
	}
	if got := (&PriceRule{}).Multiplier(rate); !got.Equal(rate) {
		t.Errorf("no scale: got %s", got)
	}
	scale := d("0.8")
	if got := (&PriceRule{Scale: &scale}).Multiplier(rate); !got.Equal(d("0.576")) {
		t.Errorf("scaled: got %s", got)
	}
	zero := decimal.Zero
	if got := (&PriceRule{Scale: &zero}).Multiplier(rate); !got.IsZero() {
		t.Errorf("a multiplier of zero is free: got %s", got)
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
