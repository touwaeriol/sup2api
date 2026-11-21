package billing

import (
	"testing"

	"github.com/shopspring/decimal"
)

// A charge rounds up to the 8 stored decimals, never down; float noise of
// the expression does not add a step to an exact amount.
func TestFinalCostRoundsUp(t *testing.T) {
	d := decimal.RequireFromString
	for _, tc := range []struct{ cost, rate, want string }{
		{"0.000000001", "1", "0.00000001"}, // below one step: one step
		{"0.123456781", "1", "0.12345679"}, // not half-up: up
		{"0.123456784999", "1", "0.12345679"},
		{"0.12345678", "1", "0.12345678"},            // exact stays
		{"0.00000030000000000004", "1", "0.0000003"}, // float noise of 0.1*3 dropped
		{"0.1", "1.5", "0.15"},
		{"0.0000001", "0.3333", "0.00000004"}, // 3.333e-8 up
		{"0", "1", "0"},
		{"1", "0", "0"},
	} {
		if got := FinalCost(d(tc.cost), d(tc.rate)); !got.Equal(d(tc.want)) {
			t.Errorf("FinalCost(%s, %s) = %s, want %s", tc.cost, tc.rate, got, tc.want)
		}
	}
}
