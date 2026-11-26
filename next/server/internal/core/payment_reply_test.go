package core

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestPaymentOutcomeReply(t *testing.T) {
	for o, want := range map[PaymentOutcome]int{PaymentHandled: 200, PaymentRejected: 400, PaymentRetry: 503, PaymentOutcome(9): 503} {
		r := StatusPaymentReply(o)
		if o.HTTPStatus() != want || r.Status != want || r.ContentType != "text/plain; charset=utf-8" || len(r.Body) == 0 {
			t.Fatalf("outcome %d: %+v", o, r)
		}
	}
}

// The core converts with the table the gateway plugins use.
func TestPaymentMinorUnits(t *testing.T) {
	for _, c := range []struct {
		amount   string
		currency string
		want     int64
		ok       bool
	}{{"12.3", "USD", 1230, true}, {"1500", "JPY", 1500, true}, {"1.005", "USD", 0, false}, {"-1", "USD", 0, false}, {"10", "KRW", 0, false}} {
		got, ok := PaymentMinorUnits(decimal.RequireFromString(c.amount), c.currency)
		if got != c.want || ok != c.ok {
			t.Fatalf("%s %s: %d %v", c.amount, c.currency, got, ok)
		}
	}
	if PaymentExponent("jpy") != 0 || PaymentExponent("CNY") != 2 {
		t.Fatal("exponents")
	}
}
