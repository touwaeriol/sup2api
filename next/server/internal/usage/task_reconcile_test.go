package usage

import (
	"context"
	"fmt"
	"testing"
	"time"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

type taskTestSlots struct {
	allow bool
	calls int
}

func (s *taskTestSlots) Acquire(_ context.Context, kind string, id int64, limit int, rid string) (func(), bool, error) {
	s.calls++
	return func() {}, s.allow, nil
}
func (s *taskTestSlots) InUse(context.Context, string, int64) (int, error) { return 0, nil }
func (s *taskTestSlots) InUseMany(context.Context, string, []int64) (map[int64]int, error) {
	return nil, nil
}

func TestManagedTaskFencingAndFreePolling(t *testing.T) {
	rf := reconcileFixtureWith(t, &core.Account{AccountRef: core.AccountRef{ID: 7, PluginKey: "vid", Type: "vid_key"}, Status: "active"})
	f := rf.fixture
	ctx := context.Background()
	gen := f.svc.rec.Registry.Current().(*recGen)
	gen.pf.Endpoints[0].Task = &manifest.AsyncTaskEndpoint{Action: manifest.TaskActionSubmit, Kind: "video", IDPaths: []string{"id"}}
	gen.pf.Endpoints = append(gen.pf.Endpoints, manifest.Endpoint{ID: "query", Protocol: "vid.query", Task: &manifest.AsyncTaskEndpoint{Action: manifest.TaskActionQuery, Kind: "video", IDParam: "id", IDPaths: []string{"id"}}})
	prepare := func(request, ref string, free bool) string {
		t.Helper()
		r := f.reserved(request, ref, core.UsageTokens{Input: 1000, Output: 100})
		if free {
			r.Billable = false
			r.Price = nil
			r.Reservation = nil
		}
		id, err := f.svc.BeginTask(ctx, core.TaskIntent{RequestID: request, PluginKey: "vid", Kind: "video", UserID: f.user, APIKeyID: r.APIKeyID, GroupID: f.group, AccountID: 7, Model: r.Model, Protocol: r.Protocol})
		if err != nil {
			t.Fatal(err)
		}
		body := []byte(`{"id":"` + ref + `","status":"queued"}`)
		if err = f.svc.ReceiveTask(ctx, request, 200, body, ""); err != nil {
			t.Fatal(err)
		}
		if err = commitTask(f.svc, core.TaskRegistration{PublicID: id, UpstreamID: ref, Kind: "video", Snapshot: body, IDPaths: []string{"id"}, Deadline: time.Hour, Record: r}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := f.db.SQL.ExecContext(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	due := func(id string) { exec(`UPDATE async_tasks SET next_check_at=now() WHERE public_id=$1`, id) }
	claim := func() *settleEntry {
		t.Helper()
		es, err := f.svc.claimTasks(ctx, 1)
		if err != nil || len(es) != 1 {
			t.Fatalf("claim: %v %v", es, err)
		}
		return es[0]
	}
	q := func(id string) core.TaskQuery {
		return core.TaskQuery{ID: id, PluginKey: "vid", Kind: "video", UserID: f.user, GroupID: f.group, SubmitProtocol: "vid.gen", IDPaths: []string{"id"}}
	}

	id := prepare("managed-fence", "up-fence", false)
	due(id)
	old := claim()
	if entry := f.scalar(`SELECT id FROM pending_settlements WHERE task_public_id=$1`, id); fmt.Sprint(old.id) != entry {
		t.Fatalf("claim read entry %d, want the task's entry %s", old.id, entry)
	}
	row, err := f.svc.loadReserved(ctx, old.usageLogID)
	if err != nil {
		t.Fatal(err)
	}
	if es, err := f.svc.claimTasks(ctx, 1); err != nil || len(es) != 0 {
		t.Fatal("active task lease admitted another worker", err)
	}
	exec(`UPDATE async_tasks SET lease_until=now()-interval '1 second' WHERE public_id=$1`, id)
	old.task.state = "failed"
	old.task.snapshot = []byte(`{"id":"up-fence","status":"failed"}`)
	old.task.paths = []string{"id"}
	beforeExpired := f.balance()
	f.svc.refundFailed(ctx, old, row, "expired worker")
	if !f.balance().Equal(beforeExpired) || f.scalar(`SELECT state FROM async_tasks WHERE public_id=$1`, id) != "pending" {
		t.Fatal("expired claim changed billing before takeover")
	}
	newer := claim()
	if newer.task.token == old.task.token {
		t.Fatal("takeover reused claim token")
	}
	old.task.state = "failed"
	old.task.snapshot = []byte(`{"id":"up-fence","status":"failed"}`)
	old.task.paths = []string{"id"}
	balance := f.balance()
	f.svc.refundFailed(ctx, old, row, "stale failure")
	if !f.balance().Equal(balance) || f.scalar(`SELECT state FROM async_tasks WHERE public_id=$1`, id) != "pending" {
		t.Fatal("stale claim changed money or snapshot")
	}
	// A confirmed upstream result cannot escape its financial transaction:
	// if pricing fails, clients must still see the previous pending snapshot.
	newer.task.state = "succeeded"
	newer.task.snapshot = []byte(`{"id":"up-fence","status":"succeeded"}`)
	newer.task.paths = []string{"id"}
	badRow, err := f.svc.loadReserved(ctx, newer.usageLogID)
	if err != nil {
		t.Fatal(err)
	}
	badRow.p.Expression = ""
	f.svc.settleReconciled(ctx, newer, badRow, &pluginv1.ReconcileResult{State: pluginv1.ReconcileResult_SETTLED})
	if f.scalar(`SELECT state FROM async_tasks WHERE public_id=$1`, id) != "pending" || f.scalar(`SELECT convert_from(snapshot,'UTF8')::jsonb->>'status' FROM async_tasks WHERE public_id=$1`, id) != "queued" {
		t.Fatal("pricing failure published terminal snapshot")
	}
	due(id)
	newer = claim()
	rf.plugin.parse = func(in *monitorCall) (*pluginv1.ReconcileResult, error) {
		if in.Entry.RefId != "up-fence" || in.Account.Id != 7 || in.Entry.TaskKind != "video" {
			t.Fatalf("poll identity: %+v", in)
		}
		return &pluginv1.ReconcileResult{State: pluginv1.ReconcileResult_FAILED, TaskSnapshotJson: `{"id":"up-fence","status":"failed"}`, Reason: "failed"}, nil
	}
	f.svc.reconcileTask(ctx, newer)
	if !f.cost("managed-fence").IsZero() || f.scalar(`SELECT state FROM async_tasks WHERE public_id=$1`, id) != "failed" {
		t.Fatal("terminal observation and refund did not commit")
	}
	if f.scalar(`SELECT count(*) FROM task_submission_receipts WHERE response_body IS NOT NULL AND state='registered'`) != "0" {
		t.Fatal("registered receipt retained raw response")
	}

	// Free tasks have no reservation but share the same polling worker and
	// account concurrency gate. Busy deferrals do not spend attempt budget.
	freeID := prepare("managed-free", "up-free", true)
	due(freeID)
	slots := &taskTestSlots{}
	f.svc.rec.Slots = slots
	before := rf.hits.Load()
	f.svc.reconcileTask(ctx, claim())
	if rf.hits.Load() != before || f.scalar(`SELECT attempts FROM async_tasks WHERE public_id=$1`, freeID) != "0" {
		t.Fatal("busy account polled or spent an attempt")
	}
	slots.allow = true
	due(freeID)
	rf.plugin.parse = func(*monitorCall) (*pluginv1.ReconcileResult, error) {
		return &pluginv1.ReconcileResult{State: pluginv1.ReconcileResult_FAILED}, nil
	}
	f.svc.reconcileTask(ctx, claim())
	if f.scalar(`SELECT observation_status FROM async_tasks WHERE public_id=$1`, freeID) != "pending" {
		t.Fatal("terminal result without snapshot closed a task")
	}
	due(freeID)
	rf.plugin.parse = func(*monitorCall) (*pluginv1.ReconcileResult, error) {
		return &pluginv1.ReconcileResult{State: pluginv1.ReconcileResult_SETTLED, TaskSnapshotJson: `{"id":"up-free","status":"succeeded"}`}, nil
	}
	f.svc.reconcileTask(ctx, claim())
	if f.scalar(`SELECT state FROM async_tasks WHERE public_id=$1`, freeID) != "succeeded" || f.scalar(`SELECT count(*) FROM pending_settlements WHERE task_public_id=$1`, freeID) != "0" {
		t.Fatal("free task required a reservation or failed to finish")
	}
	// Reading a terminal snapshot uses its saved paths, independent of a new
	// manifest that changes where later observations carry their ID.
	query := q(freeID)
	query.IDPaths = []string{"data.id"}
	snap, err := f.svc.FindTask(ctx, query)
	if err != nil || len(snap.IDPaths) != 1 || snap.IDPaths[0] != "id" {
		t.Fatal("stored path contract lost", err)
	}

}
