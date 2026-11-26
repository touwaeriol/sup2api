package billing

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// The interleavings of settleSQL with another transaction that commits
// while the settlement waits for the user's balance lock, each forced:
// the other transaction holds the lock (or the new balance row) until the
// settlement queues behind it, then commits. The settlement's snapshot
// predates that commit, so these are the cases its comments argue.

// begin opens a transaction the test commits; the cleanup rolls back one
// that is still open.
func (e *env) begin() *gorm.DB {
	e.t.Helper()
	tx, err := testutil.Begin(context.Background(), e.db)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { tx.Rollback() })
	return tx
}

// settleBehind starts e.settle in the background, waits until it queues for
// a lock, commits first and returns the settlement's result.
func (e *env) settleBehind(first *gorm.DB, uid int64, requestID, total string) (*core.LedgerResult, error) {
	e.t.Helper()
	type result struct {
		res *core.LedgerResult
		err error
	}
	done := make(chan result, 1)
	go func() {
		res, err := e.settle(uid, requestID, total)
		done <- result{res, err}
	}()
	e.waitForLockWaiters(1)
	if err := first.Commit().Error; err != nil {
		e.t.Fatal(err)
	}
	r := <-done
	return r.res, r.err
}

func (e *env) balanceEvents(uid int64) int {
	e.t.Helper()
	return e.count(`SELECT count(*) FROM events WHERE type = 'balance.changed' AND payload->>'user_id' = $1`, itoa(uid))
}

// The expiry sweeper releases the reservation (ReleasePrechargeTx) while
// the settlement waits: the refund is written once, by the sweeper; the
// settlement deletes nothing more and charges the balance the refund left.
func TestSettleWaitsBehindRelease(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	uid := e.user("settle-behind-release@test.invalid")
	e.seed(uid, "10")
	if err := e.svc.Precharge(ctx, usageRecord(uid, "swept", "flat(2)")); err != nil {
		t.Fatal(err)
	}
	sweeper := e.begin()
	refund, err := e.svc.ReleasePrechargeTx(ctx, sweeper, uid, "swept")
	if err != nil || refund == nil || !refund.BalanceAfter.Equal(decimal.NewFromInt(10)) {
		t.Fatalf("release: %+v %v", refund, err)
	}
	res, err := e.settleBehind(sweeper, uid, "swept", "1.5")
	if err != nil || res == nil || res.Duplicate || !res.BalanceAfter.Equal(decimal.RequireFromString("8.5")) {
		t.Fatalf("settle: %+v %v", res, err)
	}
	if n := e.count(`SELECT count(*) FROM balance_ledger WHERE idempotency_key = 'precharge_release:swept'`); n != 1 {
		t.Fatalf("%d refunds", n)
	}
	if n := e.count(`SELECT count(*) FROM request_precharges WHERE request_id = 'swept'`); n != 0 {
		t.Fatalf("%d reservations left", n)
	}
	// 10 - 2 + 2 - 1.5, every row after the one before it.
	e.assertLedgerBalanced(uid, "8.5")
	// The seed, the precharge, the sweeper's refund and the charge.
	if n := e.balanceEvents(uid); n != 4 {
		t.Fatalf("%d balance events", n)
	}
}

// A new user's first precharge and first settlement meet on the balance
// row neither finds: the second waits for the first's row, then both
// apply, in either order.
func TestFirstPrechargeAndSettlementOfNewUser(t *testing.T) {
	ctx := context.Background()
	newUser := func(t *testing.T, name string) (*env, int64) {
		e := newEnv(t)
		e.exec(`INSERT INTO settings (key, value) VALUES ('billing', '{"min_balance":"-5"}')`)
		uid := e.user(name + "@test.invalid")
		if n := e.count(`SELECT count(*) FROM user_balances WHERE user_id = $1`, uid); n != 0 {
			t.Fatalf("%d balance rows before the first change", n)
		}
		return e, uid
	}
	t.Run("settlement first", func(t *testing.T) {
		e, uid := newUser(t, "first-settle")
		first := e.begin()
		charge := &core.LedgerChange{UserID: uid, Amount: decimal.RequireFromString("0.25"), Kind: core.LedgerKindUsage, RefType: "usage", RefID: "s1", IdempotencyKey: "usage:s1"}
		if res, err := e.svc.SettleTx(ctx, first, uid, "s1", charge); err != nil || res == nil {
			t.Fatalf("settle: %+v %v", res, err)
		}
		done := make(chan error, 1)
		go func() { done <- e.svc.Precharge(ctx, usageRecord(uid, "p1", "flat(1)")) }()
		e.waitForLockWaiters(1)
		if err := first.Commit().Error; err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatalf("precharge: %v", err)
		}
		e.assertLedgerBalanced(uid, "-1.25")
	})
	t.Run("precharge first", func(t *testing.T) {
		e, uid := newUser(t, "first-precharge")
		first := e.begin()
		ch := core.LedgerChange{UserID: uid, Amount: decimal.NewFromInt(1), Kind: core.LedgerKindUsage, RefType: "precharge",
			RefID: "p1", IdempotencyKey: "precharge:p1", Note: "request precharge"}
		if lr, err := e.svc.prechargeTx(ctx, first, ch, decimal.NewFromInt(-5)); err != nil || lr == nil {
			t.Fatalf("precharge: %+v %v", lr, err)
		}
		res, err := e.settleBehind(first, uid, "s1", "0.25")
		if err != nil || res == nil || !res.BalanceAfter.Equal(decimal.RequireFromString("-1.25")) {
			t.Fatalf("settle: %+v %v", res, err)
		}
		e.assertLedgerBalanced(uid, "-1.25")
	})
}

// Another settlement of the request commits while this one waits: its
// charge key is not in this statement's snapshot, so the insert meets it
// (ON CONFLICT) and the key is read again (applyRow.resolve): the same
// amount is that charge, a duplicate, with nothing written and no event; a
// different amount is a conflict. The reservation is refunded once, by the
// first.
func TestSettleWaitsBehindSameKey(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct {
		total    string
		conflict bool
	}{"same amount": {"1.5", false}, "other amount": {"2", true}} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			uid := e.user("settle-same-key@test.invalid")
			e.seed(uid, "10")
			if err := e.svc.Precharge(ctx, usageRecord(uid, "twice", "flat(2)")); err != nil {
				t.Fatal(err)
			}
			first := e.begin()
			charge := &core.LedgerChange{UserID: uid, Amount: decimal.RequireFromString("1.5"), Kind: core.LedgerKindUsage, RefType: "usage", RefID: "twice", IdempotencyKey: "usage:twice"}
			charged, err := e.svc.SettleTx(ctx, first, uid, "twice", charge)
			if err != nil || charged == nil || !charged.BalanceAfter.Equal(decimal.RequireFromString("8.5")) {
				t.Fatalf("first settlement: %+v %v", charged, err)
			}
			res, err := e.settleBehind(first, uid, "twice", c.total)
			if c.conflict {
				if core.AsError(err).Code != core.ErrConflict.Code {
					t.Fatalf("other amount: %+v %v", res, err)
				}
			} else if err != nil || res == nil || !res.Duplicate || res.LedgerID != charged.LedgerID || !res.BalanceAfter.Equal(charged.BalanceAfter) {
				t.Fatalf("replay: %+v %v, first %+v", res, err, charged)
			}
			for key, want := range map[string]int{"usage:twice": 1, "precharge_release:twice": 1} {
				if n := e.count(`SELECT count(*) FROM balance_ledger WHERE idempotency_key = $1`, key); n != want {
					t.Fatalf("%s: %d rows", key, n)
				}
			}
			e.assertLedgerBalanced(uid, "8.5")
			// The seed, the precharge, the first settlement's refund and charge.
			if n := e.balanceEvents(uid); n != 4 {
				t.Fatalf("%d balance events", n)
			}
		})
	}
}

// ApplyTx takes the same path: a change whose key commits while it waits
// for the balance lock is that change, a duplicate.
func TestApplyWaitsBehindSameKey(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	uid := e.user("apply-same-key@test.invalid")
	e.seed(uid, "10")
	ch := core.LedgerChange{UserID: uid, Amount: decimal.RequireFromString("3"), Kind: core.LedgerKindAdminAdjust, IdempotencyKey: "adjust:once"}
	first := e.begin()
	applied, err := e.svc.ApplyTx(ctx, first, ch)
	if err != nil || applied == nil || applied.Duplicate {
		t.Fatalf("first: %+v %v", applied, err)
	}
	done := make(chan error, 1)
	var replay *core.LedgerResult
	go func() {
		var err error
		replay, err = e.svc.Apply(ctx, ch)
		done <- err
	}()
	e.waitForLockWaiters(1)
	if err := first.Commit().Error; err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil || replay == nil || !replay.Duplicate || replay.LedgerID != applied.LedgerID {
		t.Fatalf("replay: %+v %v, first %+v", replay, err, applied)
	}
	e.assertLedgerBalanced(uid, "7")
}
