package account

import (
	"time"

	"github.com/tidwall/gjson"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// ObserveQuotaEvent implements core.QuotaObserver: it reads the windows a
// declared quota event of an upstream WebSocket message carries (CONTRACTS
// §142) and queues them for the throttled writer, as ObserveQuotaHeaders does
// for headers. It only records the windows; it never disables the account.
func (s *Service) ObserveQuotaEvent(accountID int64, event manifest.QuotaEvent, msg []byte) {
	now := time.Now()
	get := func(path string) string { return gjson.GetBytes(msg, path).String() }
	s.observeQuota(accountID, manifest.ReadQuotaEvent(event, get, now), now)
}

// observeQuota hands the windows of one passive sample to the throttled
// writer; a sample without windows writes nothing.
func (s *Service) observeQuota(accountID int64, readings []manifest.QuotaReading, now time.Time) {
	if len(readings) == 0 {
		return
	}
	windows := make(map[string]store.QuotaWindow, len(readings))
	for _, r := range readings {
		windows[r.Key] = store.QuotaWindow{Utilization: r.Utilization, ResetsAt: r.ResetsAt, Status: r.Status}
	}
	s.quota.observe(accountID, windows, now)
}
