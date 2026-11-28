package account

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// A quota event's windows go to the writer of the passive samples under the
// window names of the headers; an event without usable windows writes
// nothing (CONTRACTS §142).
func TestObserveQuotaEvent(t *testing.T) {
	s := New(Deps{})
	var saved *[]savedQuota
	s.quota, saved, _ = recorderForTest()
	ev := manifest.QuotaEvent{Type: "codex.rate_limits", Windows: []manifest.QuotaEventWindow{
		{WindowMinutes: "rate_limits.primary.window_minutes", Utilization: "rate_limits.primary.used_percent",
			UtilizationUnit: manifest.QuotaUnitPercent, Reset: "rate_limits.primary.reset_at"},
		{WindowMinutes: "rate_limits.secondary.window_minutes", Utilization: "rate_limits.secondary.used_percent",
			UtilizationUnit: manifest.QuotaUnitPercent, Reset: "rate_limits.secondary.reset_at"},
	}}
	reset := time.Now().Add(time.Hour).Unix()
	msg := fmt.Sprintf(`{"type":"codex.rate_limits","rate_limits":{"primary":{"used_percent":12.5,"window_minutes":300,"reset_at":%d},`+
		`"secondary":{"used_percent":40,"window_minutes":10080}}}`, reset)
	s.ObserveQuotaEvent(1, ev, []byte(msg))
	s.ObserveQuotaEvent(2, ev, []byte(`{"type":"codex.rate_limits","rate_limits":{"primary":{"used_percent":"x"}}}`))
	s.quota.flush(context.Background(), true)
	if len(*saved) != 1 || (*saved)[0].id != 1 {
		t.Fatalf("saved = %+v", *saved)
	}
	w := (*saved)[0].w
	if w["5h"].Utilization != 12.5 || w["5h"].ResetsAt == nil || w["5h"].ResetsAt.Unix() != reset ||
		w["7d"] != (store.QuotaWindow{Utilization: 40}) || len(w) != 2 {
		t.Fatalf("windows = %+v", w)
	}
}
