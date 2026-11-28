package gateway

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// rateLimitsEvent is the Codex backend's WebSocket usage event (codex-rs
// codex-api/src/rate_limits.rs).
var rateLimitsEvent = map[string]any{"type": "codex.rate_limits", "plan_type": "plus", "rate_limits": map[string]any{
	"primary":   map[string]any{"used_percent": 42.5, "window_minutes": 300, "reset_at": 1790003600},
	"secondary": map[string]any{"used_percent": 7, "window_minutes": 10080, "reset_at": 1790500000}}}

var rateLimitsQuota = &manifest.AccountQuota{Events: []manifest.QuotaEvent{{Type: "codex.rate_limits", Windows: []manifest.QuotaEventWindow{
	{WindowMinutes: "rate_limits.primary.window_minutes", Utilization: "rate_limits.primary.used_percent", UtilizationUnit: "percent"}}}}}

// Upstream messages whose type the account type declares in quota.events
// reach the observer with the declaration and the message; other messages
// and types without quota events never do (CONTRACTS §142).
func TestObserveQuotaEvent(t *testing.T) {
	obs := &fakeQuotaObserver{}
	g := &Gateway{d: Deps{Quota: obs}}
	withEvents := &typeRoute{binding: core.AccountTypeBinding{Type: manifest.AccountType{ID: "oauth", Quota: rateLimitsQuota}}}
	headersOnly := &typeRoute{binding: core.AccountTypeBinding{Type: manifest.AccountType{ID: "h",
		Quota: &manifest.AccountQuota{Headers: []manifest.QuotaHeader{{Key: "5h", Status: "x-s"}}}}}}
	apiKey := &typeRoute{binding: core.AccountTypeBinding{Type: manifest.AccountType{ID: "apikey"}}}
	msg := []byte(`{"type":"codex.rate_limits","rate_limits":{}}`)
	g.observeQuotaEvent(withEvents, 1, msg)
	g.observeQuotaEvent(withEvents, 2, []byte(`{"type":"response.created"}`))
	g.observeQuotaEvent(withEvents, 3, []byte(`not json`))
	g.observeQuotaEvent(headersOnly, 4, msg)
	g.observeQuotaEvent(apiKey, 5, msg)
	g.observeQuotaEvent(nil, 6, msg)
	if len(obs.events) != 1 || obs.events[0] != (quotaEventCall{1, "codex.rate_limits", string(msg)}) {
		t.Fatalf("events = %+v", obs.events)
	}
	(&Gateway{}).observeQuotaEvent(withEvents, 1, msg)
}

// A usage event inside a WebSocket turn is sampled and relayed unchanged;
// it never cools down or disables the account, and a turn without one is
// not sampled.
func TestWebSocketQuotaEventIsSampledAndRelayed(t *testing.T) {
	e := newWSEnv(t)
	obs := &fakeQuotaObserver{}
	e.gw.d.Quota = obs
	e.gen.accountTypes[len(e.gen.accountTypes)-1].Type.Quota = rateLimitsQuota
	c := e.mustDial()
	c.send(create(wsModel, "rate-limits"))
	evs := c.until("response.completed")
	if r := e.record(); !r.Success {
		t.Fatalf("turn with a usage event: %+v", r)
	}
	want, _ := json.Marshal(rateLimitsEvent)
	if evs[0].Raw != string(want) {
		t.Fatalf("first event = %s, want the usage event %s", evs[0].Raw, want)
	}
	if len(obs.events) != 1 || obs.events[0] != (quotaEventCall{11, "codex.rate_limits", string(want)}) {
		t.Fatalf("observed events = %+v", obs.events)
	}
	c.send(create(wsModel, "ok"))
	c.until("response.completed")
	e.record()
	if len(obs.events) != 1 {
		t.Fatalf("a turn without a usage event was sampled: %+v", obs.events)
	}
	if !e.accounts.cooldown[11].Before(time.Now()) || e.accounts.disabled[11] != "" {
		t.Fatalf("usage event touched the account: cooldown %v, disabled %q", e.accounts.cooldown[11], e.accounts.disabled[11])
	}
}
