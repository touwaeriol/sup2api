package billing

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

func TestResolveExactModel(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.exec(`INSERT INTO model_prices (model, mode, expression, expr_hash, source)
		VALUES ('claude-sonnet-4-5', 'visual', 'tier("base", p*3 + c*15)', 'h1', 'manual'),
		       ('web-search', 'visual', 'tier("base", flat(0.01))', 'h2', 'manual'),
		       ('gpt-4o', 'visual', 'tier("base", p*2.5)', 'h3', 'manual')`)
	e.exec(`UPDATE model_prices SET enabled = false WHERE model = 'gpt-4o'`)
	e.svc.invalidate()
	if r, err := priceOf(ctx, e.svc, core.PriceQuery{Model: "claude-sonnet-4-5"}); err != nil || r.Model != "claude-sonnet-4-5" || r.Mode != "visual" {
		t.Fatalf("resolve: %+v %v", r, err)
	}
	// A visual price that reads no token count is due per request.
	for model, perRequest := range map[string]bool{"claude-sonnet-4-5": false, "web-search": true} {
		r, err := priceOf(ctx, e.svc, core.PriceQuery{Model: model})
		if err != nil || r.PerRequest != perRequest {
			t.Fatalf("resolve %s: %+v %v", model, r, err)
		}
	}
	// Exact match only; a disabled price counts as missing.
	for _, m := range []string{"claude-sonnet-4-5-20250929", "claude-sonnet", "gpt-4o"} {
		if _, err := priceOf(ctx, e.svc, core.PriceQuery{Model: m}); core.AsError(err).Code != "model_price_not_configured" {
			t.Fatalf("resolve %s: err = %v", m, err)
		}
	}
	// One price per model; wildcards and unknown sources are rejected by the database.
	for _, sql := range []string{
		`INSERT INTO model_prices (model, mode, expression, expr_hash, source) VALUES ('web-search', 'visual', 'tier("base", p)', 'h4', 'sync')`,
		`INSERT INTO model_prices (model, mode, expression, expr_hash, source) VALUES ('claude-*', 'visual', 'tier("base", p)', 'h4', 'manual')`,
		`INSERT INTO model_prices (model, mode, expression, expr_hash, source) VALUES ('m1', 'visual', 'tier("base", p)', 'h4', 'plugin_default')`,
	} {
		if _, err := e.db.SQL.ExecContext(ctx, sql); err == nil {
			t.Fatalf("accepted: %s", sql)
		}
	}
	// The bus invalidates the cache.
	e.exec(`UPDATE model_prices SET expression = 'tier("base", p*9)' WHERE model = 'claude-sonnet-4-5'`)
	if r, _ := priceOf(ctx, e.svc, core.PriceQuery{Model: "claude-sonnet-4-5"}); r.Expression == `tier("base", p*9)` {
		t.Fatal("cache should still serve the old answer before invalidation")
	}
	_ = e.bus.Publish(ctx, core.ChannelConfigChanged, []byte(`{"key":"prices"}`))
	if r, _ := priceOf(ctx, e.svc, core.PriceQuery{Model: "claude-sonnet-4-5"}); r.Expression != `tier("base", p*9)` {
		t.Fatalf("after invalidation: %+v", r)
	}
	bodies, headers := e.svc.Inputs(&core.PriceRule{Expression: `(tier("b", p)) * (param("service_tier") == "x" ? 2 : 1) * (has(header("Anthropic-Beta"), "f") ? 2 : 1)`})
	if len(bodies) != 1 || bodies[0] != "service_tier" || len(headers) != 1 || headers[0] != "anthropic-beta" {
		t.Fatalf("inputs %v %v", bodies, headers)
	}
	// Under a custom rule the global price is evaluated too, for the base cost.
	bodies, headers = e.svc.Inputs(&core.PriceRule{Expression: `tier("b", p) * (param("speed") == "fast" ? 2 : 1)`,
		Base: &core.PriceBase{Expression: `tier("g", p) * (param("speed") == "fast" ? 3 : 1) * (header("X-Tier") == "a" ? 2 : 1)`}})
	if !slices.Equal(bodies, []string{"speed"}) || !slices.Equal(headers, []string{"x-tier"}) {
		t.Fatalf("inputs under a custom rule %v %v", bodies, headers)
	}
}

// fakeGen implements the parts of core.Generation used by declaredFacts.
type fakeGen struct {
	core.Generation
	plats []core.PlatformBinding
	types []core.AccountTypeBinding
}

func (g *fakeGen) Platforms() []core.PlatformBinding       { return g.plats }
func (g *fakeGen) AccountTypes() []core.AccountTypeBinding { return g.types }

func TestDeclaredFacts(t *testing.T) {
	facts := func(keys ...string) manifest.UsageRules {
		m := map[string]manifest.UsageFact{}
		for _, k := range keys {
			m[k] = manifest.UsageFact{Type: "number"}
		}
		return manifest.UsageRules{Facts: m}
	}
	epUsage := facts("frames")
	g := &fakeGen{
		plats: []core.PlatformBinding{
			{Builtin: true, Platform: manifest.Platform{ID: "anthropic", Usage: facts("web_search")}},
			{Plugin: core.PluginInfo{Key: "img"}, Platform: manifest.Platform{ID: "img", Usage: facts("images"),
				Endpoints: []manifest.Endpoint{{Protocol: "img.gen"}, {Protocol: "img.video", Usage: &epUsage}}}},
		},
		types: []core.AccountTypeBinding{{Type: manifest.AccountType{ID: "k", Platforms: []manifest.AccountPlatform{
			{Platform: "anthropic"},
			{Platform: "img", Usage: map[string]manifest.UsageRules{"img.gen": facts("seconds"), "img.video": facts("images")}},
		}}}},
	}
	got := declaredFacts(g)
	if len(got) != 4 || !got["images"] || !got["seconds"] || !got["web_search"] || !got["frames"] {
		t.Fatalf("facts %v", got)
	}
}

func TestLedger(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	uid := e.user("a@example.com")

	res, err := e.svc.Apply(ctx, core.LedgerChange{UserID: uid, Amount: decimal.RequireFromString("20"), Credit: true, Kind: core.LedgerKindAdminAdjust, IdempotencyKey: "k1"})
	if err != nil || res.Duplicate || !res.BalanceAfter.Equal(decimal.NewFromInt(20)) {
		t.Fatalf("credit: %+v %v", res, err)
	}
	res2, err := e.svc.Apply(ctx, core.LedgerChange{UserID: uid, Amount: decimal.RequireFromString("0.0158"), Kind: core.LedgerKindUsage, IdempotencyKey: "usage:r1", RefType: "usage", RefID: "r1"})
	if err != nil || !res2.BalanceAfter.Equal(decimal.RequireFromString("19.9842")) {
		t.Fatalf("debit: %+v %v", res2, err)
	}
	dup, err := e.svc.Apply(ctx, core.LedgerChange{UserID: uid, Amount: decimal.RequireFromString("0.0158"), Kind: core.LedgerKindUsage, IdempotencyKey: "usage:r1"})
	if err != nil || !dup.Duplicate || dup.LedgerID != res2.LedgerID {
		t.Fatalf("duplicate: %+v %v", dup, err)
	}
	var bal decimal.Decimal
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT balance FROM user_balances WHERE user_id = $1`, uid).Scan(&bal)
	if !bal.Equal(decimal.RequireFromString("19.9842")) {
		t.Fatalf("balance %s", bal)
	}
	var events int
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM events WHERE type = 'balance.changed'`).Scan(&events)
	if events != 2 {
		t.Fatalf("balance.changed events = %d", events)
	}
	if v, _ := e.mr.Get("balance:" + itoa(uid)); v != itoa(res2.LedgerID)+":19.9842" {
		t.Fatalf("cache = %q", v)
	}

	// An older observation never overwrites a newer one.
	e.svc.CacheBalance(ctx, uid, res.LedgerID, decimal.NewFromInt(20))
	if v, _ := e.mr.Get("balance:" + itoa(uid)); v != itoa(res2.LedgerID)+":19.9842" {
		t.Fatalf("cache regressed to %q", v)
	}

	// Validation and unknown users.
	bad := []core.LedgerChange{
		{UserID: uid, Amount: decimal.Zero, Kind: core.LedgerKindUsage, IdempotencyKey: "z"},
		{UserID: uid, Amount: decimal.RequireFromString("0.000000001"), Kind: core.LedgerKindUsage, IdempotencyKey: "z"},
		{UserID: uid, Amount: decimal.NewFromInt(1), Kind: "gift", IdempotencyKey: "z"},
		{UserID: uid, Amount: decimal.NewFromInt(1), Kind: core.LedgerKindUsage},
	}
	for _, ch := range bad {
		if _, err := e.svc.Apply(ctx, ch); core.AsError(err).Code != "invalid_argument" {
			t.Fatalf("%+v: err %v", ch, err)
		}
	}
	if _, err := e.svc.Apply(ctx, core.LedgerChange{UserID: 999999, Amount: decimal.NewFromInt(1), Credit: true, Kind: core.LedgerKindRefund, IdempotencyKey: "nouser"}); core.AsError(err).Code != "not_found" {
		t.Fatalf("unknown user: %v", err)
	}
}

func itoa(i int64) string { return decimal.NewFromInt(i).String() }

func TestBalanceGate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	uid := e.user("gate@example.com")

	// No balance row: 0 is not above the default minimum of 0.
	if err := e.svc.CheckBalance(ctx, uid); !errors.Is(err, core.ErrInsufficientBalance) && core.AsError(err).Code != "insufficient_balance" {
		t.Fatalf("empty balance: %v", err)
	}
	e.mr.FlushAll()
	if _, err := e.svc.Apply(ctx, core.LedgerChange{UserID: uid, Amount: decimal.NewFromInt(5), Credit: true, Kind: core.LedgerKindAdminAdjust, IdempotencyKey: "g1"}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.CheckBalance(ctx, uid); err != nil {
		t.Fatalf("funded: %v", err)
	}
	// Cache miss falls back to PostgreSQL and repopulates the cache.
	e.mr.FlushAll()
	if err := e.svc.CheckBalance(ctx, uid); err != nil {
		t.Fatal(err)
	}
	if !e.mr.Exists("balance:" + itoa(uid)) {
		t.Fatal("cache not repopulated")
	}
	// min_balance from settings.
	e.exec(`INSERT INTO settings (key, value) VALUES ('billing', '{"min_balance":"10"}')`)
	_ = e.bus.Publish(ctx, core.ChannelConfigChanged, []byte(`{}`))
	if err := e.svc.CheckBalance(ctx, uid); core.AsError(err).Code != "insufficient_balance" {
		t.Fatalf("below minimum: %v", err)
	}
	// Overdraft through usage makes the next request fail.
	e.exec(`UPDATE settings SET value = '{"min_balance":"0"}' WHERE key = 'billing'`)
	e.svc.invalidate()
	if _, err := e.svc.Apply(ctx, core.LedgerChange{UserID: uid, Amount: decimal.NewFromInt(6), Kind: core.LedgerKindUsage, IdempotencyKey: "usage:x"}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.CheckBalance(ctx, uid); core.AsError(err).Code != "insufficient_balance" {
		t.Fatalf("negative balance: %v", err)
	}
	// Redis outage: the gate still answers from PostgreSQL.
	e.mr.Close()
	if err := e.svc.CheckBalance(ctx, uid); core.AsError(err).Code != "insufficient_balance" {
		t.Fatalf("redis down: %v", err)
	}
}

func TestSettingsDefaultsAndCache(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	st, err := e.svc.Settings(ctx)
	if err != nil || !st.MinBalance.IsZero() || !st.BigCostWarningUSD.Equal(decimal.NewFromInt(10)) {
		t.Fatalf("defaults %+v %v", st, err)
	}
	e.svc.cacheTTL = time.Hour
	e.exec(`INSERT INTO settings (key, value) VALUES ('billing', '{"big_cost_warning_usd":"50"}')`)
	st, _ = e.svc.Settings(ctx)
	if !st.BigCostWarningUSD.Equal(decimal.NewFromInt(10)) {
		t.Fatal("expected cached value")
	}
	_ = e.bus.Publish(ctx, core.ChannelConfigChanged, nil)
	st, _ = e.svc.Settings(ctx)
	if !st.BigCostWarningUSD.Equal(decimal.NewFromInt(50)) {
		t.Fatalf("after invalidation %+v", st)
	}
}
