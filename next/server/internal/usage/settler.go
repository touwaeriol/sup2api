// Package usage persists gateway usage records, settles their cost through
// the ledger and serves the usage console API.
package usage

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// Billing statuses of usage_logs.
const (
	StatusPending = "pending"
	StatusBilled  = "billed"
	StatusFailed  = "failed"
	StatusFree    = "free"
	// StatusReserved is a row whose cost was CHARGED from a plugin's
	// estimate and is waiting for the real figure (CONTRACTS §25.4). It is
	// deliberately outside usage_logs_billing_pending_idx, which covers
	// ('pending','failed'): the money has already moved, so the settlement
	// retry loop must never see the row again.
	StatusReserved = "reserved"
)

// States of pending_settlements.state.
const (
	SettleStatePending = "pending"
	SettleStateSettled = "settled"
	SettleStateFailed  = "failed"
	// SettleStateAbandoned: the core stopped asking (attempts or deadline).
	// The reservation stands as the final charge.
	SettleStateAbandoned = "abandoned"
	// SettleStateEstimated: the plugin answered SETTLED_ESTIMATE - the work
	// finished and the upstream reported no usage. The reservation stands as
	// the final charge, for the opposite reason to abandoned; the manual
	// refund and re-check apply to both (settlements_api.go).
	SettleStateEstimated = "estimated"
)

// TxLedger is the transactional ledger used for settlement. The billing
// module's *billing.Service implements it; core.Ledger alone is not enough
// because the ledger row and the usage row must commit together.
type TxLedger interface {
	core.PrechargeReleaser
	ApplyTx(ctx context.Context, tx *gorm.DB, ch core.LedgerChange) (*core.LedgerResult, error)
	// SettleTx releases the request's precharge and applies the final charge
	// (nil: none) in one batch under the user's balance lock.
	SettleTx(ctx context.Context, tx *gorm.DB, userID int64, requestID string, charge *core.LedgerChange) (*core.LedgerResult, error)
	// CacheBalance refreshes the balance cache after commit.
	CacheBalance(ctx context.Context, userID, ledgerID int64, balance decimal.Decimal)
}

// Options tune the settler; zero values take defaults.
type Options struct {
	// CanRetry gates new background retry batches; queued request usage still flushes.
	CanRetry func() bool
	// Environment overrides the task timeout and the poll failure limit per
	// group and account (CONTRACTS §74.3); nil keeps the reconcile settings.
	Environment   core.EnvironmentResolver
	QueueSize     int           // default 10000
	Workers       int           // default 4
	BatchSize     int           // default 200
	FlushInterval time.Duration // default 200ms
	RetryInterval time.Duration // default 30s
	RetryAfter    time.Duration // default 1m: only retry rows older than this
	MaxAttempts   int           // default 10
}

func (o *Options) defaults() {
	if o.QueueSize <= 0 {
		o.QueueSize = 10000
	}
	if o.Workers <= 0 {
		o.Workers = 4
	}
	if o.BatchSize <= 0 {
		o.BatchSize = 200
	}
	if o.FlushInterval <= 0 {
		o.FlushInterval = 200 * time.Millisecond
	}
	if o.RetryInterval <= 0 {
		o.RetryInterval = 30 * time.Second
	}
	if o.RetryAfter <= 0 {
		o.RetryAfter = time.Minute
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 10
	}
}

// Service implements core.Settler and the usage console API.
type Service struct {
	db     *store.DB
	ledger TxLedger
	quoter core.Quoter
	events core.EventPublisher
	opts   Options

	queue    chan *core.UsageRecord
	wg       sync.WaitGroup
	stop     context.CancelFunc
	mu       sync.Mutex
	stopping bool // serializes Submit registration against Stop/Wait

	// Reconcile loop (CONTRACTS §25.4); zero until StartReconcile.
	rec            *reconciler
	recStop        context.CancelFunc
	reconcileCfg   *resolved
	reconcileCfgAt time.Time
	reconcileEpoch uint64

	// Authz answers the console statistics' optional sections (users and
	// accounts in GET /usage/overview); nil hides them.
	Authz core.Authorizer
	// overviews and summaries keep the operator's statistics briefly: they
	// scan every user's rows (snapshot_cache.go).
	overviews *snapshotCache[Overview]
	summaries *snapshotCache[[]SummaryRow]
}

var _ core.Settler = (*Service)(nil)

// New builds the usage service. Call Start to run the workers.
func New(db *store.DB, ledger TxLedger, quoter core.Quoter, events core.EventPublisher, opts Options) *Service {
	opts.defaults()
	return &Service{
		db: db, ledger: ledger, quoter: quoter, events: events, opts: opts, queue: make(chan *core.UsageRecord, opts.QueueSize),
		overviews: newSnapshotCache[Overview](overviewFresh, statsCacheTTL),
		summaries: newSnapshotCache[[]SummaryRow](statsCacheTTL, statsCacheTTL),
	}
}

// Start runs the settlement workers until Stop; retries are
// core.usage.settlement_retry (RetryTaskSpec).
func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stop != nil {
		return
	}
	ctx, s.stop = context.WithCancel(context.WithoutCancel(ctx))
	for range s.opts.Workers {
		s.wg.Go(func() { s.worker(ctx) })
	}
}

// Stop drains the queue and waits for the workers (bounded by ctx).
func (s *Service) Stop(ctx context.Context) {
	s.mu.Lock()
	stop, recStop := s.stop, s.recStop
	rec := s.rec
	s.stopping = true
	s.mu.Unlock()
	if recStop != nil {
		recStop()
	}
	if rec != nil {
		_ = rec.group.Close(ctx)
	}
	if stop != nil {
		stop()
	}
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		slog.Warn("usage: stop timed out", "queued", len(s.queue))
	}
}

// Submit implements core.Settler. During normal operation, when the queue is full
// the record is persisted by a separate goroutine and billed by the retry
// loop.
func (s *Service) Submit(rec *core.UsageRecord) {
	if rec == nil {
		return
	}
	rec = freeze(rec)
	persist := func() { s.persistOne(rec) }
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		// The HTTP/gateway shutdown barrier normally prevents late producers.
		// A late caller must persist synchronously, never enqueue into a stopped
		// worker or spawn a goroutine behind Stop's completion barrier.
		persist()
		return
	}
	defer s.mu.Unlock()
	select {
	case s.queue <- rec:
	default:
		// Add while holding mu: Stop cannot begin waiting before this fallback
		// has joined the same lifecycle as the regular workers.
		s.wg.Go(persist)
	}
}

// freeze copies rec, with its priced usage, so the caller may reuse it.
func freeze(rec *core.UsageRecord) *core.UsageRecord {
	frozen := *rec
	frozen.Additional = core.ClonePricedUsage(rec.Additional)
	frozen.Replacement = core.ClonePricedUsage(rec.Replacement)
	return &frozen
}

// persistOne inserts rec on its own, logging it as lost when that fails.
func (s *Service) persistOne(rec *core.UsageRecord) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := s.insert(ctx, []*core.UsageRecord{rec}); err != nil {
		logLost(rec, err)
	}
}

func (s *Service) worker(ctx context.Context) {
	batch := make([]*core.UsageRecord, 0, s.opts.BatchSize)
	for {
		batch = batch[:0]
		select {
		case rec := <-s.queue:
			batch = append(batch, rec)
		case <-ctx.Done():
			s.drain()
			return
		}
		timer := time.NewTimer(s.opts.FlushInterval)
	fill:
		for len(batch) < s.opts.BatchSize {
			select {
			case rec := <-s.queue:
				batch = append(batch, rec)
			case <-timer.C:
				break fill
			case <-ctx.Done():
				break fill
			}
		}
		timer.Stop()
		bctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		s.process(bctx, batch)
		cancel()
	}
}

// drain processes whatever is still queued at shutdown.
func (s *Service) drain() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for {
		batch := make([]*core.UsageRecord, 0, s.opts.BatchSize)
	fill:
		for len(batch) < s.opts.BatchSize {
			select {
			case rec := <-s.queue:
				batch = append(batch, rec)
			default:
				break fill
			}
		}
		if len(batch) == 0 {
			return
		}
		s.process(ctx, batch)
	}
}

// process persists a batch and settles its billable records.
func (s *Service) process(ctx context.Context, batch []*core.UsageRecord) {
	inserted, err := s.insertRetrying(ctx, batch)
	if err != nil && len(batch) > 1 {
		// Isolate bad records so one of them cannot sink the whole batch.
		inserted, err = s.insertEach(ctx, batch), nil
	}
	if err != nil {
		for _, rec := range batch {
			logLost(rec, err)
		}
		return
	}
	for _, rec := range inserted {
		if initialStatus(rec) != StatusPending {
			continue
		}
		p := fromRecord(rec)
		if err := s.settle(ctx, p, false); err != nil {
			s.markFailed(ctx, p, err)
		}
	}
}

// insertRetrying inserts batch, trying three times with a growing pause.
func (s *Service) insertRetrying(ctx context.Context, batch []*core.UsageRecord) (inserted []*core.UsageRecord, err error) {
	for attempt, backoff := 0, 500*time.Millisecond; attempt < 3; attempt, backoff = attempt+1, backoff*2 {
		if inserted, err = s.insert(ctx, batch); err == nil {
			break
		}
		slog.Error("usage: insert batch", "records", len(batch), "attempt", attempt+1, "err", err)
		time.Sleep(backoff)
	}
	return inserted, err
}

// insertEach inserts the records of batch one by one, logging each one lost.
func (s *Service) insertEach(ctx context.Context, batch []*core.UsageRecord) []*core.UsageRecord {
	var inserted []*core.UsageRecord
	for _, rec := range batch {
		one, err := s.insert(ctx, []*core.UsageRecord{rec})
		if err != nil {
			logLost(rec, err)
			continue
		}
		inserted = append(inserted, one...)
	}
	return inserted
}

func logLost(rec *core.UsageRecord, err error) {
	b, _ := json.Marshal(rec)
	slog.Error("usage: record lost", "request_id", rec.RequestID, "err", err, "record", string(b))
}

func initialStatus(rec *core.UsageRecord) string {
	if !rec.Billable {
		return StatusFree
	}
	// A reservation charges the estimate and then waits: the row is settled
	// the moment it is written, and it is the reconcile loop, not the
	// settlement retry loop, that finishes it.
	if rec.Reservation != nil {
		return StatusReserved
	}
	return StatusPending
}
