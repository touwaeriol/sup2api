package usage

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/event"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// The per-request database work of the settler, measured against a real
// PostgreSQL (TEST_DATABASE_URL). Run them before and after a change to the
// settler's SQL with
//
//	go test -run '^$' -bench Hot -benchmem -count 5 ./internal/usage/
//
// The settler's workers are not started: each benchmark calls the step it
// measures for one record, which is the shape of a quiet node's batches.

type benchSettler struct {
	svc   *Service
	bill  *billing.Service
	user  int64
	group int64
	price *core.PriceRule
}

func newBenchSettler(b *testing.B) *benchSettler {
	b.Helper()
	ctx := context.Background()
	db := testutil.DB(b)
	pub := event.NewPublisher(db)
	bill := billing.New(db, nil, nil, pub, nil)
	b.Cleanup(bill.Close)
	s := &benchSettler{svc: New(db, bill, bill, pub, Options{}), bill: bill,
		user: testutil.InsertUser(b, db, "bench@test.invalid"), group: testutil.InsertGroup(b, db, "bench")}
	testutil.FundUser(b, bill, s.user)
	prog, err := expr.Compile(a6Expr)
	if err != nil {
		b.Fatal(err)
	}
	s.price = &core.PriceRule{Model: "claude-sonnet-4-5", Mode: "expression", Expression: a6Expr, ExprVersion: 1, ExprHash: prog.Hash()}
	if err := db.SQL.QueryRowContext(ctx, `INSERT INTO model_prices (model, mode, expression, expr_hash, source)
		VALUES ('claude-sonnet-4-5', 'expression', $1, $2, 'manual') RETURNING id`, a6Expr, prog.Hash()).Scan(&s.price.ID); err != nil {
		b.Fatal(err)
	}
	return s
}

// record is a finished request as the gateway submits it.
func (s *benchSettler) record(i int, billable bool) *core.UsageRecord {
	rec := &core.UsageRecord{
		RequestID: "bench-" + strconv.Itoa(i), UserID: s.user, APIKeyID: 1, GroupID: s.group, PluginKey: "anthropic",
		Platform: "anthropic", Protocol: "anthropic.messages", AccountType: "apikey", UpstreamProtocol: "anthropic.messages",
		Endpoint: "/v1/messages", Model: "claude-sonnet-4-5", StatusCode: 200, Success: true, Attempts: 1,
		UsageSemantics: "exclusive", Tokens: core.UsageTokens{Input: 1200, Output: 800, CacheRead: 4000},
		Billable: billable, RateMultiplier: decimal.NewFromInt(1),
		HookDecisions: []core.HookDecision{{PluginKey: "guard", HookID: "h", Decision: "allow"}},
	}
	if billable {
		rec.Price = s.price
	}
	return rec
}

func (s *benchSettler) insert(b *testing.B, rec *core.UsageRecord) {
	b.Helper()
	if _, err := s.svc.insert(context.Background(), []*core.UsageRecord{rec}); err != nil {
		b.Fatal(err)
	}
}

// A free request's row: one transaction with the usage_logs insert (ON
// CONFLICT (request_id) DO NOTHING RETURNING id), the release of a
// precharge it might hold (releaseSQL) and the usage.recorded event.
func BenchmarkHotSettlerInsertFree(b *testing.B) {
	s := newBenchSettler(b)
	s.insert(b, s.record(-1, false)) // prepares the statements
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		s.insert(b, s.record(i, false))
	}
}

// A billable request's row, written pending: one transaction with the
// usage_logs insert alone.
func BenchmarkHotSettlerInsertPending(b *testing.B) {
	s := newBenchSettler(b)
	s.insert(b, s.record(-1, true))
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		s.insert(b, s.record(i, true))
	}
}

// A busy node's batch: 200 rows (the default BatchSize), half free and
// half pending, in one transaction. Per op is per batch.
func BenchmarkHotSettlerInsertBatch(b *testing.B) {
	s := newBenchSettler(b)
	batch := func(n int) []*core.UsageRecord {
		recs := make([]*core.UsageRecord, 200)
		for i := range recs {
			recs[i] = s.record(n*len(recs)+i, i%2 == 0)
		}
		return recs
	}
	ctx := context.Background()
	benchEach(b, func(i int) []*core.UsageRecord { return batch(i) }, func(recs []*core.UsageRecord) error {
		_, err := s.svc.insert(ctx, recs)
		return err
	})
}

// Settlement of a pending row whose request was precharged: one
// transaction locking the row (SELECT ... FOR UPDATE), the ledger statement
// (billing settleSQL: release the precharge, apply the charge), the
// balance.changed events, the usage_logs update and the usage.recorded
// event. The precharge and the insert before it are not timed.
func BenchmarkHotSettle(b *testing.B) {
	s := newBenchSettler(b)
	ctx := context.Background()
	prepare := func(i int) *pending {
		rec := s.record(i, true)
		if err := s.bill.Precharge(ctx, rec); err != nil {
			b.Fatal(err)
		}
		s.insert(b, rec)
		return fromRecord(rec)
	}
	benchEach(b, prepare, func(p *pending) error { return s.svc.settle(ctx, p, false) })
}

// benchEach times run on a new argument each iteration, which prepare makes
// untimed; the first call, untimed too, prepares the statements.
func benchEach[T any](b *testing.B, prepare func(i int) T, run func(T) error) {
	step := func(i int) {
		b.StopTimer()
		arg := prepare(i)
		b.StartTimer()
		if err := run(arg); err != nil {
			b.Fatal(err)
		}
	}
	step(-1)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		step(i)
	}
}

// benchAccount is the upstream account of the executions and tasks.
var benchAccount int64 = 7

// The plugin_executions writes of a request served through an Execute
// plugin: the intent and the observation on every attempt, and the commit
// (the row locked FOR UPDATE and marked committed in the usage transaction
// of a free record).
func BenchmarkHotExecution(b *testing.B) {
	s := newBenchSettler(b)
	ctx := context.Background()
	intent := func(i int) *core.UsageRecord {
		rec := s.record(i, false)
		rec.RequestID = "bench-exec-" + strconv.Itoa(i)
		rec.AccountID = &benchAccount
		return rec
	}
	begin := func(rec *core.UsageRecord) error {
		return s.svc.BeginExecution(ctx, core.ExecutionIntent{ID: rec.RequestID, RequestID: rec.RequestID,
			PluginKey: rec.PluginKey, AccountID: *rec.AccountID, Attempt: 1})
	}
	observe := func(rec *core.UsageRecord) error {
		return s.svc.ObserveExecution(ctx, rec.RequestID, rec, []byte("bounded observation"))
	}
	n := 0
	// then is the record of a new execution with steps run, untimed.
	then := func(steps ...func(*core.UsageRecord) error) func(int) *core.UsageRecord {
		return func(int) *core.UsageRecord {
			n++
			rec := intent(n)
			for _, step := range steps {
				if err := step(rec); err != nil {
					b.Fatal(err)
				}
			}
			return rec
		}
	}
	b.Run("begin", func(b *testing.B) { benchEach(b, then(), begin) })
	b.Run("observe", func(b *testing.B) { benchEach(b, then(begin), observe) })
	b.Run("commit", func(b *testing.B) {
		benchEach(b, then(begin, observe), func(rec *core.UsageRecord) error {
			err := s.svc.CommitExecution(ctx, core.ExecutionCommit{ID: rec.RequestID, Operation: core.ExecutionOpUsage,
				Digest: strings.Repeat("0", 64), Record: rec})
			return err
		})
	})
}

// The task endpoints: BeginTask's receipt before a submission goes
// upstream, ReceiveTask's write of the answer, the commit of the
// submission's execution (the usage row and recordTask in one transaction)
// before the client gets its public ID,
// and FindTask for every poll of a client.
func BenchmarkHotTask(b *testing.B) {
	s := newBenchSettler(b)
	ctx := context.Background()
	n := 0
	// submission is a new task submission, its receipt taken through steps,
	// untimed.
	submission := func(steps ...func(core.TaskRegistration) error) func(int) core.TaskRegistration {
		return func(int) core.TaskRegistration {
			n++
			rec := s.record(n, false)
			rec.RequestID = "bench-task-" + strconv.Itoa(n)
			rec.AccountID = &benchAccount
			upstream := "up-" + rec.RequestID
			reg := core.TaskRegistration{UpstreamID: upstream, Kind: "video",
				Snapshot: []byte(`{"id":"` + upstream + `","status":"queued"}`), IDPaths: []string{"id"}, Record: rec}
			for _, step := range steps {
				if err := step(reg); err != nil {
					b.Fatal(err)
				}
			}
			return reg
		}
	}
	var publicID string // BeginTask's, which the commit registers
	begin := func(reg core.TaskRegistration) (err error) {
		r := reg.Record
		publicID, err = s.svc.BeginTask(ctx, core.TaskIntent{RequestID: r.RequestID, PluginKey: r.PluginKey, Kind: reg.Kind,
			Model: r.Model, Protocol: r.Protocol, UserID: r.UserID, APIKeyID: r.APIKeyID, GroupID: r.GroupID, AccountID: *r.AccountID})
		return err
	}
	receive := func(reg core.TaskRegistration) error {
		return s.svc.ReceiveTask(ctx, reg.Record.RequestID, 200, reg.Snapshot, "")
	}
	var commit core.ExecutionCommit // observe's, which register commits
	observe := func(reg core.TaskRegistration) (err error) {
		reg.PublicID = publicID
		commit, err = observeTask(s.svc, reg)
		return err
	}
	register := func(core.TaskRegistration) error { return s.svc.CommitExecution(ctx, commit) }
	b.Run("begin", func(b *testing.B) { benchEach(b, submission(), begin) })
	b.Run("receive", func(b *testing.B) { benchEach(b, submission(begin), receive) })
	b.Run("register", func(b *testing.B) { benchEach(b, submission(begin, receive, observe), register) })
	b.Run("find", func(b *testing.B) {
		reg := submission(begin, receive, observe, register)(0)
		q := core.TaskQuery{ID: publicID, PluginKey: reg.Record.PluginKey, Kind: reg.Kind, UserID: s.user, GroupID: s.group}
		benchEach(b, func(int) core.TaskQuery { return q }, func(q core.TaskQuery) error {
			_, err := s.svc.FindTask(ctx, q)
			return err
		})
	})
}
