package manifest

import (
	"testing"
	"time"
)

// codexRateLimits is the Codex backend's WebSocket usage event as the
// official client parses it (codex-rs codex-api/src/rate_limits.rs
// parse_rate_limit_event).
var codexRateLimits = QuotaEvent{Type: "codex.rate_limits", Windows: []QuotaEventWindow{
	{WindowMinutes: "rate_limits.primary.window_minutes", Utilization: "rate_limits.primary.used_percent",
		UtilizationUnit: QuotaUnitPercent, Reset: "rate_limits.primary.reset_at"},
	{WindowMinutes: "rate_limits.secondary.window_minutes", Utilization: "rate_limits.secondary.used_percent",
		UtilizationUnit: QuotaUnitPercent, Reset: "rate_limits.secondary.reset_at"},
}}

// The windows of an event are named by their length and read like headers.
func TestReadQuotaEvent(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	msg := []byte(`{"type":"codex.rate_limits","plan_type":"plus","rate_limits":{
		"primary":{"used_percent":42.5,"window_minutes":10080,"reset_at":1790500000},
		"secondary":{"used_percent":7,"window_minutes":300,"reset_at":1790003600}}}`)
	got := ReadQuotaEvent(codexRateLimits, msg, now)
	if len(got) != 2 {
		t.Fatalf("windows = %+v", got)
	}
	if w := got[0]; w.Key != "7d" || w.Utilization != 42.5 || w.ResetsAt == nil || w.ResetsAt.Unix() != 1790500000 {
		t.Fatalf("primary = %+v", w)
	}
	if w := got[1]; w.Key != "5h" || w.Utilization != 7 || w.ResetsAt == nil || w.ResetsAt.Unix() != 1790003600 {
		t.Fatalf("secondary = %+v", w)
	}
}

// Fields that are absent or of the wrong kind leave their value unknown; a
// window without a usable length or any usable value is left out.
func TestReadQuotaEventIgnoresBadFields(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	for name, msg := range map[string]string{
		"no rate_limits":  `{"type":"codex.rate_limits"}`,
		"not json":        `codex.rate_limits`,
		"bad length":      `{"rate_limits":{"primary":{"used_percent":5,"window_minutes":"soon"},"secondary":{"used_percent":5,"window_minutes":-1}}}`,
		"no usable value": `{"rate_limits":{"primary":{"used_percent":"lots","window_minutes":300,"reset_at":{}}}}`,
	} {
		if got := ReadQuotaEvent(codexRateLimits, []byte(msg), now); len(got) != 0 {
			t.Errorf("%s: windows = %+v", name, got)
		}
	}
	msg := []byte(`{"rate_limits":{"primary":{"used_percent":true,"window_minutes":300,"reset_at":1790003600}}}`)
	got := ReadQuotaEvent(codexRateLimits, msg, now)
	if len(got) != 1 || got[0].Key != "5h" || got[0].Utilization != 0 || got[0].ResetsAt == nil {
		t.Fatalf("a bad utilization beside a good reset: %+v", got)
	}
}

func TestAccountQuotaEvent(t *testing.T) {
	q := &AccountQuota{Events: []QuotaEvent{codexRateLimits}}
	if ev := q.Event("codex.rate_limits"); ev == nil || ev.Type != "codex.rate_limits" {
		t.Fatalf("event = %+v", ev)
	}
	if q.Event("response.completed") != nil || q.Event("") != nil || (*AccountQuota)(nil).Event("codex.rate_limits") != nil {
		t.Fatal("a type without a declaration matched")
	}
	if !(&AccountQuota{Events: q.Events}).Supported() {
		t.Fatal("events alone are a quota declaration")
	}
}
