package environment

import (
	"context"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/event"
)

// Plugins read the global layer over the process layer, read now; a saved
// layer records settings.changed in its transaction (CONTRACTS §107).
func TestSettingsForPlugins(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	e.svc.Events = event.NewPublisher(e.db)
	read := e.svc.SettingsReaders()[protocol.SettingsEnvironment]
	values := func() map[string]int64 {
		t.Helper()
		v, err := read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return v.(protocol.EnvironmentSettings).Values
	}
	events := func() (n int) {
		t.Helper()
		if err := e.db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM events WHERE type = 'settings.changed' AND payload = '{"sections":["environment"]}'::jsonb`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if got := values(); len(got) != 2 || got[core.EnvVarRelayTimeoutSeconds] != 900 {
		t.Fatalf("process layer: %v", got)
	}
	if code, out := e.do(e.admin, "PUT", "/settings/environment", map[string]any{"values": map[string]int64{"response_header_timeout_seconds": 64, "relay_timeout_seconds": 300}}); code != 200 {
		t.Fatalf("put: %d %v", code, out)
	}
	if got := values(); got[core.EnvVarResponseHeaderTimeoutSeconds] != 64 || got[core.EnvVarRelayTimeoutSeconds] != 300 || got[core.EnvVarStreamingIdleTimeoutSeconds] != 45 {
		t.Fatalf("global over process: %v", got)
	}
	if events() != 1 {
		t.Fatal("no settings.changed")
	}
	if code, _ := e.do(e.admin, "PUT", "/settings/environment", map[string]any{"values": map[string]int64{"nope": 1}}); code != 400 || events() != 1 {
		t.Fatal("a refused save recorded settings.changed")
	}
}
