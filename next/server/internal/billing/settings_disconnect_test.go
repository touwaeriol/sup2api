package billing

import (
	"context"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// client_disconnect_policy of the billing settings (CONTRACTS §8, §25.8).
func TestClientDisconnectSettings(t *testing.T) {
	e := newEnv(t)
	admin := e.user("admin@example.com")
	ctx := context.Background()
	policy := func() string {
		t.Helper()
		e.svc.invalidate()
		v, err := e.svc.ClientDisconnectPolicy(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	// No row: the default.
	st := data(e.mustCall(admin, 200, "GET", "/settings/billing", nil))
	if st["client_disconnect_policy"] != "continue" {
		t.Fatalf("defaults: %v", st)
	}
	if _, ok := st["client_disconnect_wait_seconds"]; ok {
		t.Fatalf("the removed wait is still answered: %v", st)
	}
	if got := policy(); got != core.DisconnectContinue {
		t.Fatalf("default policy: %q", got)
	}

	// A row written before the field existed reads as the default too; one
	// still holding the removed wait (core 0.1.284) is read without it.
	e.exec(`INSERT INTO settings (key, value) VALUES ('billing', '{"min_balance":"1","client_disconnect_wait_seconds":30}')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	if got := policy(); got != core.DisconnectContinue {
		t.Fatalf("policy of a row without the field: %q", got)
	}

	st = data(e.mustCall(admin, 200, "PUT", "/settings/billing", map[string]any{"client_disconnect_policy": "free"}))
	if st["client_disconnect_policy"] != "free" || st["min_balance"] != "1" {
		t.Fatalf("after update: %v", st)
	}
	if got := policy(); got != core.DisconnectFree {
		t.Fatalf("updated policy: %q", got)
	}

	if _, resp := e.call(admin, "PUT", "/settings/billing", map[string]any{"client_disconnect_policy": "maybe"}); errCode(resp) != "invalid_argument" {
		t.Fatalf("unknown policy: %v", resp)
	}
	if got := policy(); got != core.DisconnectFree {
		t.Fatalf("a rejected update changed the policy: %q", got)
	}

	// A stored value nobody could have chosen keeps the default.
	e.exec(`UPDATE settings SET value = value || '{"client_disconnect_policy":"later"}' WHERE key = 'billing'`)
	if got := policy(); got != core.DisconnectContinue {
		t.Fatalf("policy of an invalid row: %q", got)
	}
	// Updating another field answers with the same usable value GET gives.
	st = data(e.mustCall(admin, 200, "PUT", "/settings/billing", map[string]any{"min_balance": "2"}))
	if st["client_disconnect_policy"] != "continue" {
		t.Fatalf("update over an invalid row: %v", st)
	}
}
