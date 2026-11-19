package core

import (
	"strings"
	"testing"
)

// SetUnreported is the one free record of a request whose plugin stated no
// facts; the usage module's close, the gateway's own submission and a task
// submission that could not be registered all take it. Everything billable
// goes, the host's identity of the request stays.
func TestSetUnreportedClearsEverythingBillable(t *testing.T) {
	account := int64(7)
	r := &UsageRecord{RequestID: "r1", AccountID: &account, Model: "m", StatusCode: 200, LatencyMs: 12,
		Success: true, ErrorType: "upstream", Tokens: UsageTokens{Input: 3, Output: 4}, Metrics: map[string]any{"k": 1},
		Additional: []PricedUsage{{Kind: "compaction"}}, Replacement: []PricedUsage{{Kind: "image"}},
		Billable: true, Price: &PriceRule{ID: 1}, BillingError: "x", Reservation: &UsageReservation{}, ReservationDropped: "too large",
		UsageExtract: UsageExtractPlugin}
	r.SetUnreported(strings.Repeat("é", 600))
	want := UsageRecord{RequestID: "r1", AccountID: &account, Model: "m", StatusCode: 200, LatencyMs: 12,
		ErrorType: UsageErrorUnreported, ErrorMessage: r.ErrorMessage, UsageExtract: UsageExtractUnreported}
	if r.Success || r.Billable || r.Tokens != (UsageTokens{}) || r.Metrics != nil || r.Additional != nil || r.Replacement != nil ||
		r.Price != nil || r.BillingError != "" || r.Reservation != nil || r.ReservationDropped != "" ||
		r.ErrorType != want.ErrorType || r.UsageExtract != want.UsageExtract || r.RequestID != "r1" || r.StatusCode != 200 || r.LatencyMs != 12 {
		t.Fatalf("free record %+v", r)
	}
	if len(r.ErrorMessage) > 1000 || !strings.HasPrefix(r.ErrorMessage, "é") || strings.ToValidUTF8(r.ErrorMessage, "") != r.ErrorMessage {
		t.Fatalf("reason %d bytes", len(r.ErrorMessage))
	}
}
