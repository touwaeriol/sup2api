package protocol

import "testing"

func TestPaymentExponent(t *testing.T) {
	for cur, want := range map[string]int32{"CNY": 2, "usd": 2, "JPY": 0, "jpy": 0} {
		if got, ok := PaymentExponent(cur); !ok || got != want {
			t.Fatalf("%s: %d %v", cur, got, ok)
		}
	}
	for _, cur := range []string{"KRW", "VND", "", "XYZ"} {
		if _, ok := PaymentExponent(cur); ok {
			t.Fatalf("%s is not a settlement currency", cur)
		}
	}
}

var minorUnitCases = []struct {
	amount, currency string
	want             int64
	ok               bool
}{
	{"12.30", "USD", 1230, true},
	{"12.3", "usd", 1230, true},
	{"12", "CNY", 1200, true},
	{"0.01", "CNY", 1, true},
	{"1.230000", "EUR", 123, true},
	{"1500", "JPY", 1500, true},
	{"1500.0", "JPY", 1500, true},
	{"999999999999999.99", "USD", 99999999999999999, true},
	{"1.005", "USD", 0, false},
	{"1.5", "JPY", 0, false},
	{"0", "USD", 0, false},
	{"0.00", "USD", 0, false},
	{"-1", "USD", 0, false},
	{"+1", "USD", 0, false},
	{"1e2", "USD", 0, false},
	{"NaN", "USD", 0, false},
	{".5", "USD", 0, false},
	{"1.", "USD", 0, false},
	{"", "USD", 0, false},
	{"1000000000000000", "USD", 0, false},
	{"10", "KRW", 0, false},
}

func TestPaymentMinorUnits(t *testing.T) {
	for _, c := range minorUnitCases {
		got, ok := PaymentMinorUnits(c.amount, c.currency)
		if got != c.want || ok != c.ok {
			t.Fatalf("%q %s: %d %v", c.amount, c.currency, got, ok)
		}
	}
}
