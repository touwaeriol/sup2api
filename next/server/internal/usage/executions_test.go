package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// The waiting form of plugin execution: the facts arrive only in what
// Execute returned (CommitExecution) or not at all (CloseExecutionUnreported).

// executionAttempt numbers the executions the helpers begin, so a second
// execution of a request gets past BeginExecution to the commit itself.
var executionAttempt atomic.Int32

// observe begins and observes an execution of rec, the host's reading.
func observe(t *testing.T, s *Service, rec *core.UsageRecord, task bool) string {
	t.Helper()
	id, err := observeExecution(s, rec, task)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func observeExecution(s *Service, rec *core.UsageRecord, task bool) (string, error) {
	ctx := context.Background()
	id := uuid.NewString()
	err := s.BeginExecution(ctx, core.ExecutionIntent{TaskSubmit: task, ID: id, RequestID: rec.RequestID, PluginKey: rec.PluginKey,
		AccountID: *rec.AccountID, Attempt: int(executionAttempt.Add(1))})
	if err == nil {
		err = s.ObserveExecution(ctx, id, rec, []byte(`{"status":200}`))
	}
	return id, err
}

// observeTask is the observed execution of reg's submission and the commit
// Execute's task would make of it.
func observeTask(s *Service, reg core.TaskRegistration) (core.ExecutionCommit, error) {
	id, err := observeExecution(s, reg.Record, true)
	return core.ExecutionCommit{ID: id, Operation: core.ExecutionOpWatch, Record: reg.Record, Task: &reg,
		Digest: core.FactDigest(&pluginv1.TaskSubmission{UpstreamRefId: reg.UpstreamID})}, err
}

// commitTask registers reg the one way a task is registered.
func commitTask(s *Service, reg core.TaskRegistration) error {
	commit, err := observeTask(s, reg)
	if err != nil {
		return err
	}
	return s.CommitExecution(context.Background(), commit)
}

func syncRecord(f *fixture, id string) *core.UsageRecord {
	r := f.record(id, true)
	account := int64(7)
	r.AccountID = &account
	return r
}

func isConflict(err error) bool { return err != nil && core.AsError(err).Code == core.ErrConflict.Code }

// An observation and a reading far over the 512 KiB they were once limited
// to are stored whole.
func TestObserveExecutionStoresAnySize(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r := syncRecord(f, "large-observation")
	r.PluginDetail = core.RawJSON(`{"pad":"` + strings.Repeat("x", 600<<10) + `"}`)
	id := uuid.NewString()
	if err := f.svc.BeginExecution(ctx, core.ExecutionIntent{ID: id, RequestID: r.RequestID, PluginKey: r.PluginKey,
		AccountID: *r.AccountID, Attempt: int(executionAttempt.Add(1))}); err != nil {
		t.Fatal(err)
	}
	observation := []byte(`{"pad":"` + strings.Repeat("y", 600<<10) + `"}`)
	if err := f.svc.ObserveExecution(ctx, id, r, observation); err != nil {
		t.Fatalf("observe: %v", err)
	}
	var stored []byte
	if err := f.db.SQL.QueryRowContext(ctx, `SELECT observation FROM plugin_executions WHERE id=$1`, id).Scan(&stored); err != nil || len(stored) != len(observation) {
		t.Fatalf("stored %d of %d bytes: %v", len(stored), len(observation), err)
	}
}

func TestExecutionCommitsTheReturnedUsage(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	before := f.balance()
	r := syncRecord(f, "returned-usage")
	if err := f.bill.Precharge(ctx, r); err != nil {
		t.Fatal(err)
	}
	id := observe(t, f.svc, r, false)
	reported := &pluginv1.UsageReport{Tokens: &pluginv1.UsageTokens{InputTokens: 10}}
	commit := core.ExecutionCommit{ID: id, Operation: core.ExecutionOpUsage, Digest: core.FactDigest(reported), Record: r}

	f.ledger.fail.Store(1)
	if err := f.svc.CommitExecution(ctx, commit); err == nil {
		t.Fatal("failed ledger transaction acknowledged")
	}
	if f.scalar(`SELECT count(*) FROM usage_logs`) != "0" || f.scalar(`SELECT state FROM plugin_executions WHERE id=$1`, id) != core.ExecutionStateObserved ||
		f.scalar(`SELECT count(*) FROM request_precharges`) != "1" {
		t.Fatal("failed commit left partial state")
	}
	if err := f.svc.CommitExecution(ctx, commit); err != nil {
		t.Fatal(err)
	}
	charged := f.balance()
	if f.scalar(`SELECT billing_status FROM usage_logs WHERE request_id=$1`, r.RequestID) != StatusBilled ||
		!before.Sub(charged).Equal(f.cost(r.RequestID)) || f.scalar(`SELECT count(*) FROM request_precharges`) != "0" {
		t.Fatal("the commit did not bill once and release the precharge")
	}
	// The same facts again: nothing happens. Other facts: refused, and the
	// committed ones stand.
	if err := f.svc.CommitExecution(ctx, commit); err != nil {
		t.Fatal("retry of the same facts", err)
	}
	other := commit
	other.Digest = core.FactDigest(nil)
	if err := f.svc.CommitExecution(ctx, other); !isConflict(err) {
		t.Fatal("other facts for a committed execution", err)
	}
	if err := f.svc.CloseExecutionUnreported(ctx, core.ExecutionUnreported{ID: id, Reason: "late"}); !isConflict(err) {
		t.Fatal("a committed execution closed as unreported", err)
	}
	if !f.balance().Equal(charged) || f.scalar(`SELECT count(*) FROM usage_logs`) != "1" ||
		f.scalar(`SELECT state || ' ' || digest FROM plugin_executions WHERE id=$1`, id) != core.ExecutionStateCommitted+" "+commit.Digest {
		t.Fatal("a refused commit changed something")
	}
	if f.scalar(`SELECT count(*) FROM plugin_executions WHERE id=$1 AND record IS NULL AND observation IS NULL`, id) != "1" {
		t.Fatal("committed payload retained")
	}

	// host_usage: the plugin states the host's reading is the usage.
	host := syncRecord(f, "host-usage")
	host.Tokens = core.UsageTokens{Input: 1234, Output: 56}
	hid := observe(t, f.svc, host, false)
	if err := f.svc.CommitExecution(ctx, core.ExecutionCommit{ID: hid, Operation: core.ExecutionOpUsage, Digest: core.FactDigest(nil), Record: host}); err != nil {
		t.Fatal(err)
	}
	if f.scalar(`SELECT billing_status || ' ' || input_tokens::text || '/' || output_tokens::text FROM usage_logs WHERE request_id='host-usage'`) != "billed 1234/56" {
		t.Fatal("host reading not billed")
	}

	// Malformed commits never reach the database.
	bad := syncRecord(f, "malformed")
	bid := observe(t, f.svc, bad, false)
	for _, c := range []core.ExecutionCommit{
		{ID: bid, Operation: core.ExecutionOpWatch, Digest: commit.Digest, Record: bad},
		{ID: bid, Operation: "record", Digest: commit.Digest, Record: bad},
		{ID: bid, Operation: core.ExecutionOpUsage, Digest: "short", Record: bad},
		{ID: bid, Operation: core.ExecutionOpUsage, Digest: commit.Digest, Record: bad, Task: &core.TaskRegistration{}},
	} {
		if err := f.svc.CommitExecution(ctx, c); err == nil {
			t.Fatalf("malformed commit accepted: %+v", c)
		}
	}
	// An execution without an observation has nothing to commit.
	intent := syncRecord(f, "intent-only")
	iid := uuid.NewString()
	if err := f.svc.BeginExecution(ctx, core.ExecutionIntent{ID: iid, RequestID: intent.RequestID, PluginKey: intent.PluginKey, AccountID: 7, Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.CommitExecution(ctx, core.ExecutionCommit{ID: iid, Operation: core.ExecutionOpUsage, Digest: commit.Digest, Record: intent}); err == nil {
		t.Fatal("an unobserved execution committed")
	}
}

func TestExecutionWithoutFactsClosesUnreported(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	before := f.balance()
	r := syncRecord(f, "nothing-returned")
	if err := f.bill.Precharge(ctx, r); err != nil {
		t.Fatal(err)
	}
	if f.balance().Equal(before) {
		t.Fatal("not precharged")
	}
	id := observe(t, f.svc, r, false)
	for range 2 { // closing twice is closing once
		if err := f.svc.CloseExecutionUnreported(ctx, core.ExecutionUnreported{ID: id, Reason: "no report"}); err != nil {
			t.Fatal(err)
		}
	}
	row := f.scalar(`SELECT billing_status || ' ' || total_cost::text || ' ' || success::text || ' ' || error_type || ' ' ||
		(input_tokens + output_tokens + cache_read_tokens)::text || ' ' || (anomalies->>'usage_extract') || ' ' || error_message
		FROM usage_logs WHERE request_id=$1`, r.RequestID)
	if row != "free 0.00000000 false usage_unreported 0 unreported no report" {
		t.Fatalf("unreported row = %q", row)
	}
	if !f.balance().Equal(before) || f.scalar(`SELECT count(*) FROM request_precharges`) != "0" || f.scalar(`SELECT count(*) FROM usage_logs`) != "1" {
		t.Fatal("the precharge was not released exactly once")
	}
	// The host's reading stays as evidence.
	var kept core.UsageRecord
	if err := json.Unmarshal([]byte(f.scalar(`SELECT record::text FROM plugin_executions WHERE id=$1 AND state=$2 AND observation IS NOT NULL`, id, core.ExecutionStateUnreported)), &kept); err != nil || kept.Tokens != r.Tokens {
		t.Fatal("the observation was not kept", err)
	}
	// Facts arriving after the close are refused and change nothing.
	if err := f.svc.CommitExecution(ctx, core.ExecutionCommit{ID: id, Operation: core.ExecutionOpUsage, Digest: core.FactDigest(nil), Record: r}); !isConflict(err) {
		t.Fatal("facts accepted after the close", err)
	}
	if !f.balance().Equal(before) || f.scalar(`SELECT billing_status FROM usage_logs WHERE request_id=$1`, r.RequestID) != StatusFree {
		t.Fatal("a late commit billed an unreported execution")
	}
	// Without a durable observation there is nothing to close.
	intent := syncRecord(f, "close-intent")
	iid := uuid.NewString()
	if err := f.svc.BeginExecution(ctx, core.ExecutionIntent{ID: iid, RequestID: intent.RequestID, PluginKey: intent.PluginKey, AccountID: 7, Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.CloseExecutionUnreported(ctx, core.ExecutionUnreported{ID: iid, Reason: "no report"}); !errors.Is(err, core.ErrExecutionRefused) {
		t.Fatal("an unobserved execution closed, or not as a final refusal", err)
	}
	// Facts that cannot fit any execution are a final refusal: the gateway
	// does not retry them.
	if err := f.svc.CommitExecution(ctx, core.ExecutionCommit{ID: iid, Operation: core.ExecutionOpUsage, Record: intent}); !errors.Is(err, core.ErrExecutionRefused) {
		t.Fatal("an invalid commit is not a final refusal", err)
	}
	if err := f.svc.CommitExecution(ctx, core.ExecutionCommit{ID: iid, Operation: core.ExecutionOpUsage, Digest: core.FactDigest(nil), Record: intent}); !errors.Is(err, core.ErrExecutionRefused) {
		t.Fatal("a commit without an observation is not a final refusal", err)
	}
	if f.scalar(`SELECT count(*) FROM usage_logs WHERE request_id='close-intent'`) != "0" {
		t.Fatal("closing an intent wrote a record")
	}
}

// An observation stored while its acknowledgement was lost: the gateway
// submitted the request's free record itself. Closing the execution (the
// recovery sweep) finds that record and closes it without another, instead
// of failing on every round.
func TestExecutionCloseAfterTheGatewaysFreeRecord(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	before := f.balance()
	r := syncRecord(f, "ack-lost")
	if err := f.bill.Precharge(ctx, r); err != nil {
		t.Fatal(err)
	}
	id := observe(t, f.svc, r, false)
	free := *r
	free.Success, free.Billable, free.Price, free.Tokens, free.ErrorType = false, false, nil, core.UsageTokens{}, core.UsageErrorUnreported
	if _, err := f.svc.insert(ctx, []*core.UsageRecord{&free}); err != nil {
		t.Fatal(err)
	}
	if !f.balance().Equal(before) || f.scalar(`SELECT count(*) FROM request_precharges`) != "0" {
		t.Fatal("the gateway's free record did not release the precharge")
	}
	if err := f.svc.CloseExecutionUnreported(ctx, core.ExecutionUnreported{ID: id, Reason: "sweep"}); err != nil {
		t.Fatal(err)
	}
	if f.scalar(`SELECT state FROM plugin_executions WHERE id=$1`, id) != core.ExecutionStateUnreported ||
		f.scalar(`SELECT count(*) FROM usage_logs WHERE request_id='ack-lost'`) != "1" || !f.balance().Equal(before) {
		t.Fatal("the execution was not closed with the existing record alone")
	}
}

// The recovery sweep closes only executions whose gateway stopped renewing
// them, and never bills the host's reading: one not renewed for
// core.ExecutionStaleAfter is closed as unreported (its precharge
// released); one that is old but still renewed is left to its gateway,
// which then commits it once. Task submissions are left to their receipt,
// and everything goes after 7 days.
func TestExecutionRecoveryMarksAndNeverBills(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	before := f.balance()
	age := func(id, interval string) {
		t.Helper()
		if _, err := f.db.SQL.ExecContext(ctx, `UPDATE plugin_executions SET updated_at=now()-$2::interval, created_at=now()-$2::interval WHERE id=$1`, id, interval); err != nil {
			t.Fatal(err)
		}
	}
	past := fmt.Sprintf("%d seconds", int((core.ExecutionStaleAfter + time.Minute).Seconds()))
	staleRec := syncRecord(f, "stale")
	if err := f.bill.Precharge(ctx, staleRec); err != nil {
		t.Fatal(err)
	}
	stale := observe(t, f.svc, staleRec, false)
	age(stale, past)
	// A live execution an hour old whose gateway keeps renewing it.
	liveRec := syncRecord(f, "live")
	live := observe(t, f.svc, liveRec, false)
	age(live, "1 hour")
	if err := f.svc.RenewExecution(ctx, live); err != nil {
		t.Fatal(err)
	}
	task := observe(t, f.svc, syncRecord(f, "stale-task"), true)
	age(task, past)
	if err := f.svc.recoverExecutions(ctx); err != nil {
		t.Fatal(err)
	}
	if f.scalar(`SELECT state FROM plugin_executions WHERE id=$1`, stale) != core.ExecutionStateUnreported ||
		f.scalar(`SELECT billing_status || ' ' || error_type FROM usage_logs WHERE request_id='stale'`) != "free usage_unreported" ||
		f.scalar(`SELECT count(*) FROM request_precharges`) != "0" {
		t.Fatal("stale observation not closed as unreported with its precharge released")
	}
	if f.scalar(`SELECT state FROM plugin_executions WHERE id=$1`, live) != core.ExecutionStateObserved ||
		f.scalar(`SELECT state FROM plugin_executions WHERE id=$1`, task) != core.ExecutionStateObserved ||
		f.scalar(`SELECT count(*) FROM usage_logs`) != "1" || !f.balance().Equal(before) {
		t.Fatal("recovery touched a renewed observation or a task submission, or billed")
	}
	// The live one commits normally, billed once; renewing it afterwards
	// changes nothing and tells the gateway to stop.
	commit := core.ExecutionCommit{ID: live, Operation: core.ExecutionOpUsage, Digest: core.FactDigest(nil), Record: liveRec}
	if err := f.svc.CommitExecution(ctx, commit); err != nil {
		t.Fatal(err)
	}
	if f.scalar(`SELECT billing_status FROM usage_logs WHERE request_id='live'`) != StatusBilled || !before.Sub(f.balance()).Equal(f.cost("live")) {
		t.Fatal("the renewed execution was not billed once")
	}
	for _, id := range []string{live, stale, "no-such-execution"} {
		was := f.scalar(`SELECT coalesce(max(state || ' ' || updated_at::text), '') FROM plugin_executions WHERE id=$1`, id)
		if err := f.svc.RenewExecution(ctx, id); !errors.Is(err, core.ErrExecutionNotObserved) {
			t.Fatalf("renewal of %s: %v", id, err)
		}
		if f.scalar(`SELECT coalesce(max(state || ' ' || updated_at::text), '') FROM plugin_executions WHERE id=$1`, id) != was {
			t.Fatalf("a renewal changed the ended execution %s", id)
		}
	}
	// Seven days of evidence, then the sweep removes it.
	age(stale, "8 days")
	age(task, "6 days")
	if err := f.svc.recoverExecutions(ctx); err != nil {
		t.Fatal(err)
	}
	if f.scalar(`SELECT count(*) FROM plugin_executions WHERE id=$1`, stale) != "0" || f.scalar(`SELECT count(*) FROM plugin_executions WHERE id=$1`, task) != "1" {
		t.Fatal("retention is not seven days")
	}
}

// A task submission's commit is the task, its reserve replacing the
// request's precharge and its monitoring, in one transaction; the monitor
// then keeps the hold while the task runs and settles it from what Monitor
// returns.
func TestExecutionWatchCommitsTaskAndMonitorSettles(t *testing.T) {
	rf := reconcileFixtureWith(t, &core.Account{AccountRef: core.AccountRef{ID: 7, PluginKey: "vid", Type: "vid_key"}, Status: "active"})
	f, ctx := rf.fixture, context.Background()
	gen := f.svc.rec.Registry.Current().(*recGen)
	gen.pf.Endpoints = append(gen.pf.Endpoints, queryEndpoint())
	before := f.balance()
	r := f.reserved("watch", "raw-watch", core.UsageTokens{Input: 1000, Output: 100})
	if err := f.bill.Precharge(ctx, r); err != nil {
		t.Fatal(err)
	}
	precharged := f.balance()
	public, err := f.svc.BeginTask(ctx, core.TaskIntent{RequestID: r.RequestID, PluginKey: r.PluginKey, Kind: "video", UserID: r.UserID,
		APIKeyID: r.APIKeyID, GroupID: r.GroupID, AccountID: 7, Model: r.Model, Protocol: r.Protocol})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"id":"raw-watch","status":"queued"}`)
	if err = f.svc.ReceiveTask(ctx, r.RequestID, 200, body, ""); err != nil {
		t.Fatal(err)
	}
	commit, err := observeTask(f.svc, core.TaskRegistration{PublicID: public, UpstreamID: "raw-watch", Kind: "video", Snapshot: body,
		IDPaths: []string{"id"}, Deadline: time.Hour, Record: r})
	if err != nil {
		t.Fatal(err)
	}
	f.ledger.fail.Store(1)
	if err = f.svc.CommitExecution(ctx, commit); err == nil {
		t.Fatal("failed reserve acknowledged")
	}
	if f.scalar(`SELECT count(*) FROM async_tasks`) != "0" || f.scalar(`SELECT count(*) FROM usage_logs`) != "0" ||
		f.scalar(`SELECT count(*) FROM request_precharges`) != "1" || !f.balance().Equal(precharged) {
		t.Fatal("watch transaction leaked partial state")
	}
	for range 2 {
		if err = f.svc.CommitExecution(ctx, commit); err != nil {
			t.Fatal(err)
		}
	}
	reserved := f.balance()
	if f.scalar(`SELECT count(*) FROM pending_settlements WHERE task_public_id=$1`, public) != "1" || f.scalar(`SELECT count(*) FROM request_precharges`) != "0" ||
		!before.Sub(reserved).Equal(f.cost(r.RequestID)) || f.scalar(`SELECT state || ' ' || task_id FROM plugin_executions WHERE id=$1`, commit.ID) != "committed "+public {
		t.Fatal("task, reserve and monitoring did not commit together, once")
	}
	other := commit
	other.Digest = core.FactDigest(nil)
	if err = f.svc.CommitExecution(ctx, other); !isConflict(err) {
		t.Fatal("other task facts accepted", err)
	}

	due := func() {
		t.Helper()
		if _, err := f.db.SQL.ExecContext(ctx, `UPDATE async_tasks SET next_check_at=now(), lease_until=NULL WHERE public_id=$1`, public); err != nil {
			t.Fatal(err)
		}
		f.svc.ReconcileDue(ctx)
	}
	// Running: the hold stays.
	rf.plugin.parse = func(*monitorCall) (*pluginv1.ReconcileResult, error) {
		return &pluginv1.ReconcileResult{State: pluginv1.ReconcileResult_PENDING, TaskSnapshotJson: `{"id":"raw-watch","status":"running"}`}, nil
	}
	due()
	if !f.balance().Equal(reserved) || f.scalar(`SELECT billing_status FROM usage_logs WHERE request_id='watch'`) != StatusReserved {
		t.Fatal("a running task moved the hold")
	}
	// A terminal answer the fetch cannot back is one failed query.
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer broken.Close()
	rf.plugin.url = broken.URL
	settled := func(*monitorCall) (*pluginv1.ReconcileResult, error) {
		return &pluginv1.ReconcileResult{State: pluginv1.ReconcileResult_SETTLED, TaskSnapshotJson: `{"id":"raw-watch","status":"succeeded"}`,
			Tokens: &pluginv1.UsageTokens{InputTokens: 4000, OutputTokens: 400}}, nil
	}
	rf.plugin.parse = settled
	due()
	if !f.balance().Equal(reserved) || f.scalar(`SELECT poll_failures FROM async_tasks WHERE public_id=$1`, public) != "1" {
		t.Fatal("a terminal result without a complete fetch was applied")
	}
	// Success settles from the actual usage.
	rf.plugin.url = rf.up.URL
	due()
	final := f.cost(r.RequestID)
	if f.scalar(`SELECT billing_status || ' ' || input_tokens::text FROM usage_logs WHERE request_id='watch'`) != "billed 4000" ||
		!before.Sub(f.balance()).Equal(final) || f.scalar(`SELECT state FROM async_tasks WHERE public_id=$1`, public) != "succeeded" ||
		f.scalar(`SELECT count(*) FROM plugin_monitor_receipts WHERE task_id=$1`, public) != "2" {
		t.Fatal("the terminal result did not settle the actual usage")
	}
}

func queryEndpoint() manifest.Endpoint {
	return manifest.Endpoint{ID: "query", Protocol: "vid.query", Task: &manifest.AsyncTaskEndpoint{Action: manifest.TaskActionQuery, Kind: "video", IDParam: "id", IDPaths: []string{"id"}}}
}
