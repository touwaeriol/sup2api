package usage

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// A fixed submission fee is ordinary usage, not a reservation for future
// reconciliation: it settles in the transaction that registers the task.
func TestTaskImmediateBillingWithoutReservationAndRetry(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	const expression = "flat(0.25)"
	hash := expr.Hash(expression)
	if _, err := f.db.SQL.ExecContext(ctx, `INSERT INTO model_price_history(expr_hash,expression,expr_version) VALUES($1,$2,1)`, hash, expression); err != nil {
		t.Fatal(err)
	}
	submission := func(request string, free bool) core.TaskRegistration {
		t.Helper()
		r := f.reserved(request, "raw-"+request, core.UsageTokens{})
		r.Reservation = nil
		r.Price = &core.PriceRule{Mode: "visual", PerRequest: true, Expression: expression, ExprHash: hash, ExprVersion: 1}
		if free {
			r.Billable = false
			r.Price = nil
		}
		id, err := f.svc.BeginTask(ctx, core.TaskIntent{RequestID: request, PluginKey: r.PluginKey, Kind: "video", Model: r.Model, Protocol: r.Protocol,
			UserID: r.UserID, APIKeyID: r.APIKeyID, GroupID: r.GroupID, AccountID: *r.AccountID})
		if err != nil {
			t.Fatal(err)
		}
		body := []byte(`{"id":"raw-` + request + `","status":"queued"}`)
		if err = f.svc.ReceiveTask(ctx, request, 200, body, ""); err != nil {
			t.Fatal(err)
		}
		return core.TaskRegistration{PublicID: id, UpstreamID: "raw-" + request, Kind: "video", Snapshot: body, IDPaths: []string{"id"}, Deadline: time.Hour, Record: r}
	}
	register := func(request string, free bool) string {
		t.Helper()
		reg := submission(request, free)
		if err := commitTask(f.svc, reg); err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.FindTask(ctx, core.TaskQuery{ID: reg.PublicID, PluginKey: reg.Record.PluginKey, Kind: "video", UserID: reg.Record.UserID, GroupID: reg.Record.GroupID}); err != nil {
			t.Fatal("committed task disappeared", err)
		}
		return reg.PublicID
	}
	before := f.balance()
	id := register("fixed-fee", false)
	if got := f.scalar(`SELECT billing_status FROM usage_logs WHERE request_id='fixed-fee'`); got != StatusBilled {
		t.Fatalf("fixed task fee was not settled immediately: %s", got)
	}
	fee := decimal.RequireFromString("0.25")
	if !before.Sub(f.balance()).Equal(fee) || !f.cost("fixed-fee").Equal(fee) {
		t.Fatal("fixed task fee was not charged exactly once")
	}
	if f.scalar(`SELECT count(*) FROM pending_settlements WHERE task_public_id=$1`, id) != "0" {
		t.Fatal("fixed fee created an unnecessary reservation")
	}

	// The fee settles in the commit's transaction: a settlement failure
	// fails the commit and leaves nothing behind, and the gateway's retry of
	// the same commit registers and charges once.
	f.ledger.fail.Store(1)
	before = f.balance()
	reg := submission("retry-fixed-fee", false)
	commit, err := observeTask(f.svc, reg)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.svc.CommitExecution(ctx, commit); err == nil {
		t.Fatal("a commit whose settlement failed was acknowledged")
	}
	if f.scalar(`SELECT count(*) FROM usage_logs WHERE request_id='retry-fixed-fee'`) != "0" || f.scalar(`SELECT count(*) FROM async_tasks WHERE public_id=$1`, reg.PublicID) != "0" || !f.balance().Equal(before) {
		t.Fatal("failed commit left a task, a row or a charge")
	}
	for range 2 {
		if err = f.svc.CommitExecution(ctx, commit); err != nil {
			t.Fatal(err)
		}
	}
	if f.scalar(`SELECT billing_status FROM usage_logs WHERE request_id='retry-fixed-fee'`) != StatusBilled || !before.Sub(f.balance()).Equal(fee) {
		t.Fatal("retried commit did not charge the fee once")
	}
	if n, err := f.svc.RetryPending(ctx); err != nil || n != 0 {
		t.Fatal("already billed task retried", n, err)
	}

	before = f.balance()
	register("still-free", true)
	if !f.balance().Equal(before) || f.scalar(`SELECT billing_status FROM usage_logs WHERE request_id='still-free'`) != StatusFree {
		t.Fatal("free task changed billing semantics")
	}
}
