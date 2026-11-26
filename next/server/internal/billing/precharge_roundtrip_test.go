package billing

import (
	"context"
	"slices"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/event"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// A precharge is four flushes: BEGIN, prechargeSQL (lock timeout, lock,
// reservation and ledger row), the event, COMMIT.
func TestPrechargeRoundTrips(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	uid := e.user("precharge-round-trips@test.invalid")
	if _, err := e.svc.Apply(ctx, core.LedgerChange{UserID: uid, Amount: decimal.NewFromInt(10), Credit: true, Kind: core.LedgerKindAdminAdjust, IdempotencyKey: "seed"}); err != nil {
		t.Fatal(err)
	}
	// Every statement is one round trip to the server, BEGIN and COMMIT
	// included: the request path pays each one at a distant Supabase
	// (CONTRACTS §63.7).
	db, trace := testutil.Traced(e.db)
	svc := New(db, e.rdb, e.bus, event.NewPublisher(db), nil)
	record := func(id string) *core.UsageRecord {
		return &core.UsageRecord{RequestID: id, UserID: uid, Price: &core.PriceRule{Expression: "flat(0.5)"}, RateMultiplier: decimal.NewFromInt(1)}
	}
	// The first call of a statement on a connection prepares it (two
	// round trips); the steady state a busy node is in is what matters here.
	if err := svc.Precharge(ctx, record("warmup")); err != nil {
		t.Fatal(err)
	}
	trace.Take()
	if err := svc.Precharge(ctx, record("measured")); err != nil {
		t.Fatal(err)
	}
	oneTx(t, "one precharge", trace.Take(), 4)
}

// oneTx fails t unless statements, a testutil.Traced log, are one
// committed transaction of at most max round trips.
func oneTx(t *testing.T, what string, statements []string, max int) {
	t.Helper()
	if len(statements) > max {
		t.Fatalf("%d round trips for %s; want at most %d\n%q", len(statements), what, max, statements)
	}
	n := len(statements)
	if n < 2 || statements[0] != "begin" || statements[n-1] != "commit" ||
		slices.Contains(statements[1:n-1], "begin") || slices.Contains(statements[1:n-1], "commit") {
		t.Fatalf("%s is not one transaction: %q", what, statements)
	}
}
