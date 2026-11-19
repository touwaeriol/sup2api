package usage

import (
	"context"
	"testing"
	"time"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// An empty upstream body is stored as an empty bytea, not NULL, and a nil
// one (an oversized answer) keeps the stored body.
func TestTaskReceiveEmptyBody(t *testing.T) {
	s := &Service{db: testutil.DB(t)}
	ctx := context.Background()
	in := core.TaskIntent{RequestID: "req-empty", PluginKey: "vid", Kind: "video", UserID: 1, APIKeyID: 2, GroupID: 3, AccountID: 4, Model: "m", Protocol: "p"}
	if _, err := s.BeginTask(ctx, in); err != nil {
		t.Fatal(err)
	}
	body := func() *string {
		t.Helper()
		var b *string
		if err := s.db.SQL.QueryRowContext(ctx, `SELECT encode(response_body, 'escape') FROM task_submission_receipts WHERE request_id = $1`, in.RequestID).Scan(&b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	if err := s.ReceiveTask(ctx, in.RequestID, 204, []byte{}, ""); err != nil {
		t.Fatal(err)
	}
	if b := body(); b == nil || *b != "" {
		t.Fatalf("response_body = %v, want the empty bytea", b)
	}
	if err := s.ReceiveTask(ctx, in.RequestID, 200, []byte(`{"id":"up"}`), ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ReceiveTask(ctx, in.RequestID, 200, nil, "late"); err != nil {
		t.Fatal(err)
	}
	if b := body(); b == nil || *b != `{"id":"up"}` {
		t.Fatalf("response_body = %v, want the stored body", b)
	}
}

func TestUsageTaskSpecs(t *testing.T) {
	s := New(nil, nil, nil, nil, Options{})
	retry := s.RetryTaskSpec()
	if retry.Name != SettlementRetryTask || retry.Scope != core.TaskNode || !retry.RequireWork || retry.Every != 30*time.Second || retry.Timeout <= 0 {
		t.Fatalf("settlement retry: %+v", retry)
	}
	rec := s.ReconcileTaskSpec()
	if rec.Name != ReconcileTask || rec.Scope != core.TaskCluster || rec.Every != reconcileInterval || rec.Timeout < reconcileSweepBudget {
		t.Fatalf("reconcile: %+v", rec)
	}
	// Without StartReconcile the sweep has nothing to do.
	if res, err := rec.Handler(context.Background(), core.TaskRun{Name: ReconcileTask}); err != nil || res.Count != 0 {
		t.Fatalf("reconcile without dependencies: %+v %v", res, err)
	}
}

// A core.usage.reconcile run returns once the entries it claimed were
// checked, so the run's count and duration are the sweep's.
func TestReconcileTaskWaitsForTheSweep(t *testing.T) {
	rf := reconcileFixture(t)
	ctx := context.Background()
	rf.svc.process(ctx, []*core.UsageRecord{rf.reserved("req-task", "task-task", core.UsageTokens{Input: 1000, Output: 100})})
	rf.plugin.parse = func(*monitorCall) (*pluginv1.ReconcileResult, error) {
		time.Sleep(200 * time.Millisecond)
		return &pluginv1.ReconcileResult{State: pluginv1.ReconcileResult_SETTLED,
			Tokens: &pluginv1.UsageTokens{InputTokens: 1000, OutputTokens: 100}}, nil
	}
	rf.due()
	res, err := rf.svc.ReconcileTaskSpec().Handler(ctx, core.TaskRun{Name: ReconcileTask})
	if err != nil || res.Count != 1 {
		t.Fatalf("result %+v (%v), want one entry", res, err)
	}
	if s := rf.scalar(`SELECT state FROM pending_settlements`); s != SettleStateSettled {
		t.Fatalf("entry state %s when the run returned", s)
	}
}

// core.usage.settlement_retry settles a failed row again and counts it.
func TestSettlementRetryTask(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r := f.record("retry-task", true)
	r.CreatedAt = time.Now().Add(-time.Hour)
	f.ledger.fail.Store(1)
	f.svc.process(ctx, []*core.UsageRecord{r})
	if got := f.scalar(`SELECT billing_status FROM usage_logs WHERE request_id='retry-task'`); got != StatusFailed {
		t.Fatalf("billing_status %s before the run", got)
	}
	res, err := f.svc.RetryTaskSpec().Handler(ctx, core.TaskRun{Name: SettlementRetryTask})
	if err != nil || res.Count != 1 {
		t.Fatalf("result %+v (%v), want one row", res, err)
	}
	if got := f.scalar(`SELECT billing_status FROM usage_logs WHERE request_id='retry-task'`); got != StatusBilled {
		t.Fatalf("billing_status %s after the run", got)
	}
}
