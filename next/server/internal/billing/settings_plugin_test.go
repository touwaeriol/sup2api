package billing

import (
	"context"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
)

// Plugins read the billing section with its defaults, read now; a saved
// section records settings.changed in its transaction (CONTRACTS §107).
func TestSettingsForPlugins(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	admin := e.user("admin@example.com")
	read := e.svc.SettingsReaders()[protocol.SettingsBilling]
	settings := func() protocol.BillingSettings {
		t.Helper()
		v, err := read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return v.(protocol.BillingSettings)
	}
	events := func() (n int) {
		t.Helper()
		if err := e.db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM events WHERE type = 'settings.changed' AND payload = '{"sections":["billing"]}'::jsonb`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	d := DefaultSettings()
	if got := settings(); got != (protocol.BillingSettings{MinBalance: "0", BigCostWarningUSD: "10",
		PreConsumeTokens: d.PreConsumeTokens, OutputReserveTokens: d.OutputReserveTokens, ClientDisconnectPolicy: d.ClientDisconnectPolicy}) {
		t.Fatalf("defaults: %+v", got)
	}
	e.mustCall(admin, 200, "PUT", "/settings/billing", map[string]any{"min_balance": "1.5"})
	if got := settings(); got.MinBalance != "1.5" || events() != 1 {
		t.Fatalf("saved: %+v, events %d", got, events())
	}
	e.mustCall(admin, 400, "PUT", "/settings/billing", map[string]any{"big_cost_warning_usd": "0"})
	if events() != 1 {
		t.Fatal("a refused save recorded settings.changed")
	}
}
