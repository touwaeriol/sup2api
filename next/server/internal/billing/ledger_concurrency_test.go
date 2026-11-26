package billing

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// The ledger's concurrency and idempotency contracts against a real
// PostgreSQL: whatever interleaving the balance lock and the unique
// idempotency keys allow, each key moves money once, every replay reports
// the row that moved it, and the balance is the sum of the ledger.

// concurrently runs fn(i) for i in [0, n) at once and returns the errors.
func concurrently(n int, fn func(i int) error) []error {
	errs := make([]error, n)
	var start, done sync.WaitGroup
	start.Add(1)
	for i := range n {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			errs[i] = fn(i)
		}()
	}
	start.Done()
	done.Wait()
	return errs
}

// assertLedgerBalanced checks that the user's balance is the sum of the
// user's ledger rows and equals want, and that the rows chain in id order:
// each balance_after is the previous one plus the row's delta (a refund is
// written before the charge of its settlement).
func (e *env) assertLedgerBalanced(uid int64, want string) {
	e.t.Helper()
	var sum decimal.Decimal
	if err := e.db.SQL.QueryRowContext(context.Background(), `SELECT COALESCE(sum(delta), 0) FROM balance_ledger WHERE user_id = $1`, uid).Scan(&sum); err != nil {
		e.t.Fatal(err)
	}
	bal := e.balance(uid)
	if !bal.Equal(sum) || !bal.Equal(decimal.RequireFromString(want)) {
		e.t.Fatalf("balance %s, ledger sum %s, want %s", bal, sum, want)
	}
	if n := e.count(`SELECT count(*) FROM (
		SELECT balance_after, delta, lag(balance_after, 1, 0::numeric) OVER (ORDER BY id) AS before
		FROM balance_ledger WHERE user_id = $1) l
		WHERE balance_after <> before + delta`, uid); n != 0 {
		e.t.Fatalf("%d ledger rows do not follow the row before them", n)
	}
}

// waitForLockWaiters returns once n backends of the test database wait for
// a lock.
func (e *env) waitForLockWaiters(n int) {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for e.count(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`) < n {
		if time.Now().After(deadline) {
			e.t.Fatalf("fewer than %d backends wait for a lock", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// openPrecharge precharges 1 for the request in a transaction it leaves
// open (the caller commits it), holding the user's balance lock.
func (e *env) openPrecharge(uid int64, requestID string) *gorm.DB {
	e.t.Helper()
	ctx := context.Background()
	tx, err := testutil.Begin(ctx, e.db)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = tx.Rollback().Error })
	ch := core.LedgerChange{UserID: uid, Amount: decimal.NewFromInt(1), Kind: core.LedgerKindUsage, RefType: "precharge",
		RefID: requestID, IdempotencyKey: "precharge:" + requestID, Note: "request precharge"}
	if lr, err := e.svc.prechargeTx(ctx, tx, ch, decimal.Zero); err != nil || lr == nil {
		e.t.Fatalf("open precharge: %+v %v", lr, err)
	}
	return tx
}

// A replay that queued for the balance lock while the first precharge of
// its request committed. Its statement's snapshot predates that commit, so
// it does not see the key, and the locked row is a newer version than the
// snapshot's. It answers success and charges nothing, as Precharge did when
// it read the key after taking the lock: with a balance that no longer
// covers a second precharge (it must not report insufficient balance) and
// with one that does (the insert meets the key; no event either way).
func TestPrechargeReplayAfterLockWait(t *testing.T) {
	for name, seed := range map[string]string{"insufficient": "1.5", "sufficient": "10"} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			ctx := context.Background()
			uid := e.user("precharge-wait-" + name + "@test.invalid")
			e.seed(uid, seed)
			first := e.openPrecharge(uid, "replayed")
			done := make(chan error, 1)
			go func() { done <- e.svc.Precharge(ctx, usageRecord(uid, "replayed", "flat(1)")) }()
			e.waitForLockWaiters(1)
			if err := first.Commit().Error; err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatalf("replay: %v", err)
			}
			if n := e.count(`SELECT count(*) FROM balance_ledger WHERE idempotency_key = 'precharge:replayed'`); n != 1 {
				t.Fatalf("%d ledger rows of the replayed request", n)
			}
			if n := e.count(`SELECT count(*) FROM request_precharges WHERE request_id = 'replayed'`); n != 1 {
				t.Fatalf("%d reservations of the replayed request", n)
			}
			e.assertLedgerBalanced(uid, decimal.RequireFromString(seed).Sub(decimal.NewFromInt(1)).String())
			// The seed's event only: the open precharge emits none, the
			// replay must not either.
			if n := e.count(`SELECT count(*) FROM events WHERE type = 'balance.changed' AND payload->>'user_id' = $1`, itoa(uid)); n != 1 {
				t.Fatalf("%d balance events", n)
			}
		})
	}
}

// Another user's precharge of the same request id takes the key while this
// one waits on it (each holds only its own balance lock): the key belongs
// to someone else, a conflict, and this user is not charged.
func TestPrechargeKeyOfAnotherUser(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner, other := e.user("precharge-owner@test.invalid"), e.user("precharge-other@test.invalid")
	e.seed(owner, "10")
	e.seed(other, "10")
	first := e.openPrecharge(owner, "shared")
	done := make(chan error, 1)
	go func() { done <- e.svc.Precharge(ctx, usageRecord(other, "shared", "flat(1)")) }()
	e.waitForLockWaiters(1)
	if err := first.Commit().Error; err != nil {
		t.Fatal(err)
	}
	if err := <-done; core.AsError(err).Code != core.ErrConflict.Code {
		t.Fatalf("other user's precharge: %v", err)
	}
	e.assertLedgerBalanced(owner, "9")
	e.assertLedgerBalanced(other, "10")
}

// A user's first precharge finds no balance row: the row is created and the
// precharge applied, within min_balance; with the default minimum the
// precharge is refused, as when ensureBalanceSQL always ran first. A
// precharge whose balance row another transaction is inserting gives up
// after the lock timeout like one queueing for the lock.
func TestPrechargeCreatesBalanceRow(t *testing.T) {
	ctx := context.Background()
	t.Run("credit", func(t *testing.T) {
		e := newEnv(t)
		e.exec(`INSERT INTO settings (key, value) VALUES ('billing', '{"min_balance":"-5"}')`)
		uid := e.user("precharge-first@test.invalid")
		if err := e.svc.Precharge(ctx, usageRecord(uid, "first", "flat(1)")); err != nil {
			t.Fatal(err)
		}
		e.assertLedgerBalanced(uid, "-1")
	})
	t.Run("refused", func(t *testing.T) {
		e := newEnv(t)
		uid := e.user("precharge-empty@test.invalid")
		if err := e.svc.Precharge(ctx, usageRecord(uid, "first", "flat(1)")); core.AsError(err).Code != core.ErrInsufficientBalance.Code {
			t.Fatalf("precharge without balance: %v", err)
		}
	})
	t.Run("busy", func(t *testing.T) {
		e := newEnv(t)
		uid := e.user("precharge-busy-row@test.invalid")
		tx, err := testutil.Begin(ctx, e.db)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := store.SQLTx(tx).ExecContext(ctx, `INSERT INTO user_balances (user_id) VALUES ($1)`, uid); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		if err := e.svc.Precharge(ctx, usageRecord(uid, "blocked", "flat(1)")); core.AsError(err).Code != core.ErrUnavailable.Code {
			t.Fatalf("blocked precharge: %v", err)
		}
		if took := time.Since(start); took < 2*time.Second || took > 10*time.Second {
			t.Fatalf("gave up after %s", took)
		}
	})
}

// A settlement for a user without a balance row creates it, as
// ensureBalanceSQL did before every settlement: the charge makes the
// balance negative, and a release with nothing to release leaves an empty
// row.
func TestSettleCreatesBalanceRow(t *testing.T) {
	e := newEnv(t)
	charged, released := e.user("settle-first@test.invalid"), e.user("settle-nothing@test.invalid")
	res, err := e.settle(charged, "first", "0.25")
	if err != nil || res == nil || res.Duplicate || !res.BalanceAfter.Equal(decimal.RequireFromString("-0.25")) {
		t.Fatalf("charge: %+v %v", res, err)
	}
	e.assertLedgerBalanced(charged, "-0.25")
	if res, err := e.settle(released, "nothing", ""); err != nil || res != nil {
		t.Fatalf("release: %+v %v", res, err)
	}
	if !e.balance(released).IsZero() {
		t.Fatal("balance row not created")
	}
}

// Every request is settled three times at once (a retry racing the first
// settlement): one refund and one charge per request, the replays report
// the charge as a duplicate, and the balance is the ledger's sum.
func TestSettleConcurrentReplays(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	uid := e.user("settle-race@test.invalid")
	e.seed(uid, "100")
	const requests, replays = 6, 3
	for r := range requests {
		if err := e.svc.Precharge(ctx, usageRecord(uid, fmt.Sprintf("race-%d", r), "flat(2)")); err != nil {
			t.Fatal(err)
		}
	}
	e.assertLedgerBalanced(uid, "88")
	results := make([]*core.LedgerResult, requests*replays)
	errs := concurrently(requests*replays, func(i int) error {
		var err error
		results[i], err = e.settle(uid, fmt.Sprintf("race-%d", i%requests), "1.25")
		return err
	})
	for r := range requests {
		fresh, ledgerID := 0, int64(0)
		for k := range replays {
			i := k*requests + r
			if errs[i] != nil {
				t.Fatalf("settlement %d: %v", i, errs[i])
			}
			res := results[i]
			if res == nil || res.LedgerID == 0 || (ledgerID != 0 && res.LedgerID != ledgerID) {
				t.Fatalf("request %d replay %d: %+v (charge %d)", r, k, res, ledgerID)
			}
			ledgerID = res.LedgerID
			if !res.Duplicate {
				fresh++
			}
		}
		if fresh != 1 {
			t.Fatalf("request %d charged %d times", r, fresh)
		}
	}
	if n := e.count(`SELECT count(*) FROM balance_ledger WHERE user_id = $1 AND idempotency_key LIKE 'usage:race-%'`, uid); n != requests {
		t.Fatalf("%d charges", n)
	}
	if n := e.count(`SELECT count(*) FROM balance_ledger WHERE user_id = $1 AND idempotency_key LIKE 'precharge_release:race-%'`, uid); n != requests {
		t.Fatalf("%d refunds", n)
	}
	if n := e.count(`SELECT count(*) FROM request_precharges WHERE user_id = $1`, uid); n != 0 {
		t.Fatalf("%d reservations left", n)
	}
	// 100 - 6 × 1.25: the reservations came back, the charges went out once.
	e.assertLedgerBalanced(uid, "92.5")
	// seed, 6 precharges, 6 refunds, 6 charges: replays emit nothing.
	if n := e.count(`SELECT count(*) FROM events WHERE type = 'balance.changed' AND payload->>'user_id' = $1`, itoa(uid)); n != 1+3*requests {
		t.Fatalf("%d balance events", n)
	}
}

// One idempotency key applied by many callers at once moves the balance
// once; the others get that row back as a duplicate, and a different
// amount under the key is a conflict.
func TestApplyConcurrentSameKey(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	uid := e.user("apply-race@test.invalid")
	ch := core.LedgerChange{UserID: uid, Amount: decimal.RequireFromString("3.5"), Credit: true, Kind: core.LedgerKindAdminAdjust, IdempotencyKey: "race:apply"}
	const callers = 8
	results := make([]*core.LedgerResult, callers)
	errs := concurrently(callers, func(i int) error {
		var err error
		results[i], err = e.svc.Apply(ctx, ch)
		return err
	})
	fresh := 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
		if results[i].LedgerID != results[0].LedgerID || !results[i].BalanceAfter.Equal(decimal.RequireFromString("3.5")) {
			t.Fatalf("caller %d: %+v, caller 0: %+v", i, results[i], results[0])
		}
		if !results[i].Duplicate {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatalf("applied %d times", fresh)
	}
	e.assertLedgerBalanced(uid, "3.5")
	ch.Amount = decimal.NewFromInt(4)
	if _, err := e.svc.Apply(ctx, ch); core.AsError(err).Code != core.ErrConflict.Code {
		t.Fatalf("reused key: %v", err)
	}
}

// A precharge replayed concurrently (the gateway retrying one request id)
// reserves and charges once and answers success to every caller. Then
// precharges of other requests for the same user queue behind the balance
// lock and are admitted until the balance runs out, while further replays
// still succeed without charging.
func TestPrechargeConcurrentReplayAndAdmission(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	uid := e.user("precharge-race@test.invalid")
	e.seed(uid, "5")
	const replays, others = 6, 6
	same := func(int) error { return e.svc.Precharge(ctx, usageRecord(uid, "same", "flat(1)")) }
	for i, err := range concurrently(replays, same) {
		if err != nil {
			t.Fatalf("replay %d: %v", i, err)
		}
	}
	e.assertLedgerBalanced(uid, "4")
	errs := concurrently(replays+others, func(i int) error {
		if i < replays {
			return same(i)
		}
		return e.svc.Precharge(ctx, usageRecord(uid, fmt.Sprintf("other-%d", i), "flat(1)"))
	})
	admitted := 0
	for i, err := range errs {
		switch {
		case i < replays && err != nil:
			t.Fatalf("late replay %d: %v", i, err)
		case err == nil && i >= replays:
			admitted++
		case err != nil && core.AsError(err).Code != core.ErrInsufficientBalance.Code:
			t.Fatalf("precharge %d: %v", i, err)
		}
	}
	// 4 leaves room for three more of 1 above the 0 minimum (the balance
	// after a precharge must stay above it).
	if admitted != 3 {
		t.Fatalf("admitted %d other requests", admitted)
	}
	if n := e.count(`SELECT count(*) FROM request_precharges WHERE user_id = $1 AND request_id = 'same'`, uid); n != 1 {
		t.Fatalf("%d reservations of the replayed request", n)
	}
	if n := e.count(`SELECT count(*) FROM balance_ledger WHERE idempotency_key = 'precharge:same'`); n != 1 {
		t.Fatalf("%d ledger rows of the replayed request", n)
	}
	e.assertLedgerBalanced(uid, "1")
	// seed, the replayed request and the three admitted: replays emit nothing.
	if n := e.count(`SELECT count(*) FROM events WHERE type = 'balance.changed' AND payload->>'user_id' = $1`, itoa(uid)); n != 5 {
		t.Fatalf("%d balance events", n)
	}
}
