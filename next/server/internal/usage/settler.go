// Package usage persists gateway usage records, settles their cost through
// the ledger and serves the usage console API.
package usage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
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
	frozen := *rec
	frozen.Additional = core.ClonePricedUsage(rec.Additional)
	frozen.Replacement = core.ClonePricedUsage(rec.Replacement)
	rec = &frozen
	persist := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := s.insert(ctx, []*core.UsageRecord{rec}); err != nil {
			logLost(rec, err)
		}
	}
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
	var inserted []*core.UsageRecord
	var err error
	for attempt, backoff := 0, 500*time.Millisecond; attempt < 3; attempt, backoff = attempt+1, backoff*2 {
		if inserted, err = s.insert(ctx, batch); err == nil {
			break
		}
		slog.Error("usage: insert batch", "records", len(batch), "attempt", attempt+1, "err", err)
		time.Sleep(backoff)
	}
	if err != nil && len(batch) > 1 {
		// Isolate bad records so one of them cannot sink the whole batch.
		inserted = inserted[:0]
		for _, rec := range batch {
			one, err := s.insert(ctx, []*core.UsageRecord{rec})
			if err != nil {
				logLost(rec, err)
				continue
			}
			inserted = append(inserted, one...)
		}
		err = nil
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

// pendingInputs are stored in billing_detail while a record is unbilled so
// the retry loop can reproduce the calculation.
type pendingInputs struct {
	Replacement []core.PricedUsage `json:"replacement,omitempty"`
	Additional  []core.PricedUsage `json:"additional,omitempty"`
	// Audit of the segments not billed at their own price (CONTRACTS §120.4).
	RequestedModelSegments []core.RequestedModelSegment `json:"requested_model_segments,omitempty"`
	SegmentsDropped        string                       `json:"segments_dropped,omitempty"`
	Semantics              string                       `json:"semantics"`
	Params                 map[string]string            `json:"params,omitempty"`
	Headers                map[string]string            `json:"headers,omitempty"`
	Pricing                *core.PricingTrace           `json:"pricing,omitempty"` // price rule and rate source (CONTRACTS §97)
}

// recordInputs are the pricing inputs of a record: what its first settlement
// prices it by and what billing_detail keeps for a retry or a reconcile to
// price it by again. It is the one place they are taken from a record, so
// the two cannot price a request differently (CONTRACTS §97.4).
func recordInputs(rec *core.UsageRecord) pendingInputs {
	return pendingInputs{Semantics: rec.UsageSemantics, Params: rec.PriceParams, Headers: rec.PriceHeaders,
		Additional: core.ClonePricedUsage(rec.Additional), Replacement: core.ClonePricedUsage(rec.Replacement),
		RequestedModelSegments: slices.Clone(rec.RequestedModelSegments), SegmentsDropped: trunc(rec.SegmentsDropped, 1000),
		Pricing: pricingTrace(rec.Price, rec.SegmentID, rec.RateSource)}
}

// pricingTrace is what a row freezes about its list price and its rate. It is
// nil for the global price at the group's own rate, so such a row is stored
// as it was before price rules existed.
func pricingTrace(price *core.PriceRule, segmentID int64, rateSource string) *core.PricingTrace {
	ruled := price != nil && (price.Scale != nil || price.Base != nil || price.Source != "" && price.Source != core.PriceSourceGlobal)
	if !ruled && (rateSource == "" || rateSource == core.RateSourceGroup) {
		return nil
	}
	trace := &core.PricingTrace{SegmentID: segmentID, RateSource: rateSource}
	if price != nil {
		trace.PriceSource, trace.Scale, trace.Base = price.Source, price.Scale, price.Base
	}
	return trace
}

type pendingDetail struct {
	Inputs   pendingInputs `json:"inputs"`
	Attempts int           `json:"attempts"`
	Error    string        `json:"error,omitempty"`
}

// pluginDetailJSON is what goes into usage_logs.plugin_detail: the plugin's
// own document when it is a JSON object, of any size, "{}" otherwise. The
// gateway already checks it and warns; this is the last guard, because a
// non-object (or invalid) document would fail the whole batch insert and
// take unrelated records down with it.
func pluginDetailJSON(raw core.RawJSON) []byte {
	if len(raw) == 0 {
		return []byte("{}")
	}
	if !gjson.ValidBytes(raw) || !gjson.ParseBytes(raw).IsObject() {
		return []byte("{}")
	}
	return raw
}

func trunc(s string, n int) string { return x.Truncate(s, n) }

func jsonOr(v any, empty string) []byte {
	b, err := json.Marshal(v)
	if err != nil || string(b) == "null" {
		return []byte(empty)
	}
	return b
}

// conversionJSON is the usage_logs.conversion value: NULL for a native
// request nothing was refused for.
func conversionJSON(t *core.ConversionTrace) []byte {
	if t == nil {
		return nil
	}
	b, _ := json.Marshal(t) // plain fields: cannot fail
	return b
}

// lockUsageRow locks the usage row inside the caller's transaction and reads
// its billing status. Every settlement path takes this lock first, so the
// SQL and the lock order live here once.
func lockUsageRow(ctx context.Context, tx *gorm.DB, id int64) (string, error) {
	if err := store.RequireTx(tx); err != nil {
		return "", err
	}
	var status string
	err := store.G(ctx, tx).Model(&model.UsageLog{}).Select("billing_status").Where("id = ?", id).
		Clauses(store.ForUpdate()).Row().Scan(&status)
	return status, err
}

// cacheBalance refreshes the balance cache after a committed ledger change,
// skipping idempotent replays that returned the earlier ledger entry.
func (s *Service) cacheBalance(ctx context.Context, userID int64, res *core.LedgerResult) {
	if res != nil && !res.Duplicate {
		s.ledger.CacheBalance(ctx, userID, res.LedgerID, res.BalanceAfter)
	}
}

// insert writes the batch (ignoring duplicate request ids) and emits
// usage.recorded for free records. It returns the newly inserted records.
func (s *Service) insert(ctx context.Context, batch []*core.UsageRecord) ([]*core.UsageRecord, error) {
	return s.insertAtomic(ctx, batch, nil, nil, false)
}

func (s *Service) insertAtomic(ctx context.Context, batch []*core.UsageRecord, before func(*gorm.DB) error, after func(*gorm.DB, map[string]int64) error, settleNow bool) ([]*core.UsageRecord, error) {
	var inserted []*core.UsageRecord
	var reserved []reservedRow
	var cached []balanceUpdate
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		if before != nil {
			if err := before(tx); err != nil {
				return err
			}
		}
		ids := make(map[string]int64, len(batch))
		inserted = inserted[:0]
		reserved, cached = reserved[:0], cached[:0]
		added, err := insertUsageRows(store.G(ctx, tx), batch)
		if err != nil {
			return err
		}
		var free []core.Event
		for i, rec := range batch {
			id := added[i]
			if id == 0 {
				continue // duplicate request id
			}
			inserted = append(inserted, rec)
			ids[rec.RequestID] = id
			switch initialStatus(rec) {
			case StatusFree:
				free = append(free, recordedEvent(fromRecord(rec), decimal.Zero, StatusFree))
			case StatusReserved:
				reserved = append(reserved, reservedRow{rec: rec, id: id})
			}
		}
		for _, rec := range inserted {
			if initialStatus(rec) != StatusFree {
				continue
			}
			lr, err := s.releasePrechargeTx(ctx, tx, rec.UserID, rec.RequestID)
			if err != nil {
				return err
			}
			if lr != nil && !lr.Duplicate {
				cached = append(cached, balanceUpdate{userID: rec.UserID, ledgerID: lr.LedgerID, balance: lr.BalanceAfter})
			}
		}
		// Charging the estimate and registering the entry happen in the same
		// transaction as the row itself: a reserved row that exists without a
		// ledger entry would be a free request, and one without a
		// pending_settlements entry would keep the estimate forever with
		// nothing left to correct it.
		for _, r := range reserved {
			res, err := s.reserveTx(ctx, tx, r)
			if err != nil {
				return err
			}
			if res != nil && !res.Duplicate {
				cached = append(cached, balanceUpdate{userID: r.rec.UserID, ledgerID: res.LedgerID, balance: res.BalanceAfter})
			}
		}
		if settleNow {
			for _, rec := range inserted {
				if initialStatus(rec) == StatusPending {
					lr, err := s.settleTx(ctx, tx, fromRecord(rec), false)
					if err != nil {
						return err
					}
					if lr != nil && !lr.Duplicate {
						cached = append(cached, balanceUpdate{userID: rec.UserID, ledgerID: lr.LedgerID, balance: lr.BalanceAfter})
					}
				}
			}
		}
		if after != nil {
			if err := after(tx, ids); err != nil {
				return err
			}
		}
		if s.events != nil && len(free) > 0 {
			return s.events.Emit(ctx, tx, free...)
		}
		return nil
	})
	if err == nil {
		for _, c := range cached {
			s.ledger.CacheBalance(ctx, c.userID, c.ledgerID, c.balance)
		}
	}
	return inserted, err
}

// usageLogRow is the usage_logs row the settler inserts: every column it
// writes, each always written, so the INSERT has one text per row count
// whatever the values. It cannot be model.UsageLog, whose columns with a
// database default go into an INSERT only when some row's value is not
// zero (Select("*") does not change that) and come back with RETURNING;
// everything else on usage_logs (the UPDATEs, the reads) uses the model.
type usageLogRow struct {
	ID                    int64           `gorm:"column:id;primaryKey;autoIncrement:false"`
	RequestID             string          `gorm:"column:request_id"`
	UserID                int64           `gorm:"column:user_id"`
	APIKeyID              int64           `gorm:"column:api_key_id"`
	GroupID               int64           `gorm:"column:group_id"`
	AccountID             *int64          `gorm:"column:account_id"`
	PluginKey             string          `gorm:"column:plugin_key"`
	PluginVersion         string          `gorm:"column:plugin_version"`
	Platform              string          `gorm:"column:platform"`
	Protocol              string          `gorm:"column:protocol"`
	Endpoint              string          `gorm:"column:endpoint"`
	Model                 string          `gorm:"column:model"`
	UpstreamModel         string          `gorm:"column:upstream_model"`
	Stream                bool            `gorm:"column:stream"`
	StatusCode            int             `gorm:"column:status_code"`
	Success               bool            `gorm:"column:success"`
	ErrorType             string          `gorm:"column:error_type"`
	ErrorMessage          string          `gorm:"column:error_message"`
	Attempts              int             `gorm:"column:attempts"`
	InputTokens           int64           `gorm:"column:input_tokens"`
	OutputTokens          int64           `gorm:"column:output_tokens"`
	CacheReadTokens       int64           `gorm:"column:cache_read_tokens"`
	CacheCreationTokens   int64           `gorm:"column:cache_creation_tokens"`
	CacheCreation1hTokens int64           `gorm:"column:cache_creation_1h_tokens"`
	ImageInputTokens      int64           `gorm:"column:image_input_tokens"`
	ImageOutputTokens     int64           `gorm:"column:image_output_tokens"`
	Metrics               store.Bytes     `gorm:"column:metrics"`
	StickyRule            string          `gorm:"column:sticky_rule"`
	StickyHit             bool            `gorm:"column:sticky_hit"`
	HookDecisions         store.Bytes     `gorm:"column:hook_decisions"`
	RateMultiplier        decimal.Decimal `gorm:"column:rate_multiplier"`
	PriceID               *int64          `gorm:"column:price_id"`
	ExprHash              string          `gorm:"column:expr_hash"`
	BillingMode           string          `gorm:"column:billing_mode"`
	BillingDetail         store.Bytes     `gorm:"column:billing_detail"`
	BillingStatus         string          `gorm:"column:billing_status"`
	LatencyMs             int             `gorm:"column:latency_ms"`
	FirstTokenMs          int             `gorm:"column:first_token_ms"`
	ClientIP              string          `gorm:"column:client_ip"`
	UserAgent             string          `gorm:"column:user_agent"`
	NodeID                string          `gorm:"column:node_id"`
	// Created is a pointer, never nil: store stamps (and reads back) a
	// zero time.Time created_at, which this row never holds.
	Created          *time.Time  `gorm:"column:created_at"`
	AccountType      string      `gorm:"column:account_type"`
	UpstreamProtocol string      `gorm:"column:upstream_protocol"`
	ClientRequestID  string      `gorm:"column:client_request_id"`
	SchedDecisions   store.Bytes `gorm:"column:sched_decisions"`
	PluginDetail     store.Bytes `gorm:"column:plugin_detail"`
	Anomalies        store.Bytes `gorm:"column:anomalies"`
	UploadMs         int         `gorm:"column:upload_ms"`
	Conversion       store.Bytes `gorm:"column:conversion"`
}

func (usageLogRow) TableName() string { return "usage_logs" }

// skipLoggedRequest skips a row whose request id is there already, or taken
// by an earlier row of the same INSERT.
var skipLoggedRequest = clause.OnConflict{Columns: []clause.Column{{Name: "request_id"}}, DoNothing: true}

// insertUsageRows inserts the rows of batch on db, in batch order, and
// returns the id of each record's new row: 0 where its request id was
// already there, or taken by an earlier record of the batch (the rows of an
// INSERT go in in order, so the first record of a request id keeps it).
//
// A batch of one, the settler's usual one when it is not busy, is one
// INSERT that returns the new row's id. A larger batch reserves its ids
// first (gormpg.NextIDs, ascending in batch order) and inserts the rows with
// them, store.InsertChunk rows per INSERT: every full chunk has the same
// text whatever the batch size. Only when some row was skipped does a last
// read find which ids made it.
func insertUsageRows(db *gorm.DB, batch []*core.UsageRecord) ([]int64, error) {
	if len(batch) == 0 {
		return nil, nil
	}
	rows := make([]usageLogRow, len(batch))
	for i, rec := range batch {
		rows[i] = newUsageLogRow(rec)
	}
	if len(rows) == 1 {
		err := db.Omit("id").Clauses(skipLoggedRequest, clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&rows[0]).Error
		return []int64{rows[0].ID}, err
	}
	ids, err := gormpg.NextIDs(db, "usage_logs", len(rows))
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].ID = ids[i]
	}
	res := db.Clauses(skipLoggedRequest).CreateInBatches(&rows, store.InsertChunk)
	if res.Error != nil || res.RowsAffected == int64(len(rows)) {
		return ids, res.Error
	}
	var written []int64
	if err := db.Model(&model.UsageLog{}).Where("id = ANY(?)", store.Int64s(ids)).Pluck("id", &written).Error; err != nil {
		return nil, err
	}
	for i, id := range ids {
		if !slices.Contains(written, id) {
			ids[i] = 0
		}
	}
	return ids, nil
}

// newUsageLogRow is rec's new row.
func newUsageLogRow(rec *core.UsageRecord) usageLogRow {
	status := initialStatus(rec)
	var priceID *int64
	var exprHash, mode string
	// Free requests also need their token semantics for usage summaries.
	// Only billable requests retain pricing headers and parameters below.
	detail := jsonOr(pendingDetail{Inputs: pendingInputs{Semantics: rec.UsageSemantics}}, "{}")
	if status == StatusPending || status == StatusReserved {
		exprHash, mode = rec.Price.ExprHash, rec.Price.Mode
		if rec.Price.ID > 0 {
			priceID = &rec.Price.ID
		}
		if exprHash == "" {
			exprHash = expr.Hash(rec.Price.Expression)
		}
		detail = jsonOr(pendingDetail{Inputs: recordInputs(rec)}, "{}")
	}
	// The observability markers have their own column, so they are
	// written once here and never touched again: settle() rewrites
	// billing_detail wholesale, and used to have to carry them
	// through the unbilled phase to put them back.
	anomalies := jsonOr(rec.Anomalies(), "{}")
	created := rec.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	attempts := rec.Attempts
	if attempts <= 0 {
		attempts = 1
	}
	t := rec.Tokens
	return usageLogRow{
		RequestID: trunc(rec.RequestID, 64), UserID: rec.UserID, APIKeyID: rec.APIKeyID, GroupID: rec.GroupID, AccountID: rec.AccountID,
		PluginKey: trunc(rec.PluginKey, 30), PluginVersion: trunc(rec.PluginVersion, 50), Platform: trunc(rec.Platform, 50),
		Protocol: trunc(rec.Protocol, 100), Endpoint: trunc(rec.Endpoint, 200), Model: rec.Model, UpstreamModel: rec.UpstreamModel,
		Stream: rec.Stream, StatusCode: rec.StatusCode, Success: rec.Success, ErrorType: trunc(rec.ErrorType, 50), ErrorMessage: rec.ErrorMessage, Attempts: attempts,
		InputTokens: t.Input, OutputTokens: t.Output, CacheReadTokens: t.CacheRead, CacheCreationTokens: t.CacheCreation, CacheCreation1hTokens: t.CacheCreation1h,
		ImageInputTokens: t.ImageInput, ImageOutputTokens: t.ImageOutput,
		Metrics: jsonOr(rec.Metrics, "{}"), StickyRule: trunc(rec.StickyRule, 100), StickyHit: rec.StickyHit, HookDecisions: jsonOr(rec.HookDecisions, "[]"),
		RateMultiplier: rec.RateMultiplier, PriceID: priceID, ExprHash: exprHash, BillingMode: mode, BillingDetail: detail, BillingStatus: status,
		LatencyMs: rec.LatencyMs, FirstTokenMs: rec.FirstTokenMs,
		ClientIP: trunc(rec.ClientIP, 64), UserAgent: trunc(rec.UserAgent, 500), NodeID: trunc(rec.NodeID, 100), Created: &created,
		AccountType: trunc(rec.AccountType, 50), UpstreamProtocol: trunc(rec.UpstreamProtocol, 100), ClientRequestID: trunc(rec.ClientRequestID, 128),
		SchedDecisions: jsonOr(rec.SchedDecisions, "[]"), PluginDetail: pluginDetailJSON(rec.PluginDetail), Anomalies: anomalies,
		UploadMs: rec.UploadMs, Conversion: conversionJSON(rec.Conversion),
	}
}

// reservedRow is one inserted usage row whose cost was estimated by a plugin.
type reservedRow struct {
	rec *core.UsageRecord
	id  int64
}

// balanceUpdate is a cache refresh owed once the transaction commits.
type balanceUpdate struct {
	userID, ledgerID int64
	balance          decimal.Decimal
}

// reserveTx prices a plugin's estimate, charges it and registers the entry
// for the reconcile loop, inside the caller's transaction.
//
// Every step is the ordinary one. The price comes from the administrator's
// table exactly as for a finished request; the charge is kind "usage" with
// idempotency key "usage:{request_id}", the same key settlement would have
// used, so a reservation and a later settlement of the same request can never
// both take the base amount. What is new is only the row's status and the
// pending_settlements entry.
func (s *Service) reserveTx(ctx context.Context, tx *gorm.DB, r reservedRow) (*core.LedgerResult, error) {
	rec := r.rec
	p := fromRecord(rec)
	cost, detail, _, err := s.priceOf(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("price reservation: %w", err)
	}
	total := cost.total
	res, err := s.settleLedgerTx(ctx, tx, p.UserID, p.RequestID, total)
	if err != nil {
		return nil, err
	}
	if total.Sign() > 0 {
		detail.LedgerID = &res.LedgerID
	}
	if err := store.G(ctx, tx).Model(&model.UsageLog{}).Where("id = ?", r.id).Updates(map[string]any{
		"total_cost": cost.total, "base_cost": cost.base, "billing_detail": store.Bytes(jsonOr(detail, "{}")), "matched_tier": trunc(detail.Tier, 100),
	}).Error; err != nil {
		return nil, err
	}
	rv := rec.Reservation
	rs := s.reconcileSettingsIn(ctx, tx)
	now := time.Now()
	deadlineAt := now.Add(rs.clampDeadline(rv.Deadline))
	nextAt := now.Add(rs.clampDelay(rv.NextCheckAfter, 0))
	if nextAt.After(deadlineAt) {
		nextAt = deadlineAt
	}
	// The reconcile entry of the row; no row when the plugin's ref_id is
	// registered already.
	entry := model.PendingSettlement{PluginKey: trunc(rv.PluginKey, 30), RefID: trunc(rv.RefID, 200), UsageLogID: r.id,
		AccountID: rec.AccountID, NextCheckAt: nextAt, DeadlineAt: deadlineAt}
	ins := store.G(ctx, tx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "plugin_key"}, {Name: "ref_id"}}, DoNothing: true}).
		Create(&entry)
	err = ins.Error
	if err == nil && ins.RowsAffected == 0 {
		// The plugin reused a ref_id that is already being reconciled, so
		// this row would never be looked at again: it would sit "reserved"
		// forever, charged and invisible to both loops. Close it here, the
		// same way an abandoned entry is closed - billed at the estimate,
		// with the reason on the row - instead of leaking it.
		return res, s.closeUnreconcilable(ctx, tx, r.id,
			"duplicate ref_id "+trunc(rv.RefID, 100)+" for plugin "+rv.PluginKey)
	}
	if err != nil {
		return nil, fmt.Errorf("register pending settlement: %w", err)
	}
	return res, nil
}

// closeUnreconcilable turns a reserved row into a final billed one: the
// estimate stands, and usage_logs.anomalies says why nobody will ever
// correct it. Same shape as the reconcile loop's abandon path, and for the
// same reason it writes 'billed' and never 'failed' - the money has already
// moved, and usage_logs_billing_pending_idx covers ('pending','failed').
func (s *Service) closeUnreconcilable(ctx context.Context, tx *gorm.DB, usageLogID int64, reason string) error {
	slog.ErrorContext(ctx, "usage: a reserved row cannot be reconciled, billing the estimate",
		"usage_log_id", usageLogID, "reason", reason)
	return markUsageRow(store.G(ctx, tx), usageLogID, StatusBilled, nil, map[string]string{
		core.AnomalyReconcile:      core.ReconcileAbandoned,
		core.AnomalyReconcileError: trunc(reason, 500),
	}).Error
}

// pending is everything needed to settle one usage row.
type pending struct {
	RequestID string
	UserID    int64
	APIKeyID  int64
	GroupID   int64
	AccountID *int64
	PluginKey string
	Platform  string
	Protocol  string
	// AccountType and UpstreamProtocol are the account type (declared by
	// PluginKey) and the protocol sent upstream (ARCHITECTURE 6.6).
	AccountType      string
	UpstreamProtocol string
	Model            string
	Success          bool
	StatusCode       int
	ErrorType        string
	Tokens           core.UsageTokens
	Metrics          map[string]any
	LatencyMs        int
	CreatedAt        time.Time
	Rate             decimal.Decimal

	PriceID    int64
	Mode       string
	Expression string
	Inputs     pendingInputs
	Attempts   int
}

func fromRecord(rec *core.UsageRecord) *pending {
	p := &pending{
		RequestID: trunc(rec.RequestID, 64), UserID: rec.UserID, APIKeyID: rec.APIKeyID, GroupID: rec.GroupID,
		AccountID: rec.AccountID, PluginKey: rec.PluginKey, Platform: rec.Platform, Protocol: rec.Protocol,
		AccountType: trunc(rec.AccountType, 50), UpstreamProtocol: trunc(rec.UpstreamProtocol, 100),
		Model: rec.Model, Success: rec.Success, StatusCode: rec.StatusCode, ErrorType: rec.ErrorType,
		Tokens: rec.Tokens, Metrics: core.CloneUsageMetrics(rec.Metrics), LatencyMs: rec.LatencyMs, CreatedAt: rec.CreatedAt,
		Rate:   rec.RateMultiplier,
		Inputs: recordInputs(rec),
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}
	if rec.Price != nil {
		p.PriceID, p.Mode, p.Expression = rec.Price.ID, rec.Price.Mode, rec.Price.Expression
	}
	return p
}

// price is the price rule frozen in the row's inputs, as far as priceOne
// reads one: the multiplier and the base. It is a core.PriceRule because
// PriceRule.Multiplier is where a rule's multiplier meets the rate. A retry
// and a reconcile rebuild it from the row alone, so a rule changed after the
// request does not change the charge.
func (p *pending) price() *core.PriceRule {
	rule := &core.PriceRule{}
	if trace := p.Inputs.Pricing; trace != nil {
		rule.Scale, rule.Base = trace.Scale, trace.Base
	}
	return rule
}

// BillingDetail is stored in usage_logs.billing_detail once billed. It holds
// billing inputs and results only: the observability markers that used to
// share it live in usage_logs.anomalies (core.Anomaly*).
type BillingDetail struct {
	Additional     []AdditionalBillingDetail `json:"additional,omitempty"`
	Replacement    []AdditionalBillingDetail `json:"replacement,omitempty"`
	ExprVersion    int                       `json:"expr_version"`
	Tier           string                    `json:"tier"`
	Rules          []expr.RuleResult         `json:"rules"`
	Breakdown      expr.Breakdown            `json:"breakdown"`
	Cost           decimal.Decimal           `json:"cost"`                // at the global price, before the rate
	ListCost       *decimal.Decimal          `json:"list_cost,omitempty"` // at the list price, before the rate; only when a price rule applied
	RateMultiplier decimal.Decimal           `json:"rate_multiplier"`
	TotalCost      decimal.Decimal           `json:"total_cost"` // charged
	// The primary usage's own Cost, ListCost and TotalCost, when the record
	// has additional items and its primary usage is priced (not replaced):
	// Cost, ListCost and TotalCost above then sum it with every item.
	PrimaryCost      *decimal.Decimal `json:"primary_cost,omitempty"`
	PrimaryListCost  *decimal.Decimal `json:"primary_list_cost,omitempty"`
	PrimaryTotalCost *decimal.Decimal `json:"primary_total_cost,omitempty"`
	LedgerID         *int64           `json:"ledger_id,omitempty"`
	Inputs           pendingInputs    `json:"inputs"`
	Attempts         int              `json:"attempts"`
}

var errNotPending = errors.New("usage row is not pending")

// amounts are the three figures of priced usage (CONTRACTS §97): base is its
// cost at the global price and list its cost at the list price, both before
// the rate; total is what is charged. Without a price rule base and list are
// the same.
type amounts struct{ base, list, total decimal.Decimal }

func (a amounts) add(b amounts) amounts {
	return amounts{base: a.base.Add(b.base), list: a.list.Add(b.list), total: a.total.Add(b.total)}
}

// priceOne runs the administrator's price expression over one pending row and
// returns what it costs (amounts), the breakdown that records how, and the
// hash of the expression used. It is the single place a usage figure becomes
// money: settlement, a plugin's reservation and a reconcile that brings back
// the real usage all go through it, so none of them can price a request
// differently from the others.
func priceOne(ctx context.Context, q core.Quoter, p *pending) (amounts, BillingDetail, string, error) {
	if p.Expression == "" {
		return amounts{}, BillingDetail{}, "", errors.New("price expression not found")
	}
	prog, err := expr.CompileCached(p.Expression)
	if err != nil {
		return amounts{}, BillingDetail{}, "", fmt.Errorf("compile price expression: %w", err)
	}
	rule := p.price()
	params := make(map[string]any, len(p.Inputs.Params))
	for k, v := range p.Inputs.Params {
		params[k] = v
	}
	amount, err := q.Quote(ctx, core.QuoteInputs{
		Expression:     p.Expression,
		UsageSemantics: p.Inputs.Semantics,
		Tokens:         p.Tokens,
		Metrics:        p.Metrics,
		PriceParams:    params,
		PriceHeaders:   p.Inputs.Headers,
		RateMultiplier: rule.Multiplier(p.Rate),
		CreatedAt:      p.CreatedAt,
	})
	if err != nil {
		return amounts{}, BillingDetail{}, "", err
	}

	// Rebuild detail from the expression evaluation for the response.
	//
	// This is deliberately a second compile-free eval: Quote returns only the
	// final amount (that is all core.Quoter promises), while billing_detail
	// needs the tier, rules and per-token breakdown, which only prog.Eval
	// exposes. Do not "optimise" the Quote call away: it applies the rate
	// multiplier and rounding, so amount and res.Cost are not interchangeable.
	// A future core.Quoter method returning both would remove this second pass.
	res, err := evaluate(prog, p)
	if err != nil {
		return amounts{}, BillingDetail{}, "", err
	}
	cost := amounts{base: res.Cost, list: res.Cost, total: amount}
	detail := BillingDetail{
		ExprVersion: prog.Version(), Tier: res.Tier, Rules: res.Rules, Breakdown: res.Breakdown,
		Cost: res.Cost, RateMultiplier: p.Rate, TotalCost: amount, Inputs: p.Inputs, Attempts: p.Attempts + 1,
	}
	if rule.Scale == nil && rule.Base == nil {
		return cost, detail, prog.Hash(), nil
	}
	// A multiplier rule scales the global price: tier, rules and breakdown
	// stay the global price's. A custom rule replaces it: they are the
	// rule's, and the global price is evaluated for the base cost alone.
	if rule.Scale != nil {
		cost.list = res.Cost.Mul(*rule.Scale)
	}
	// The base cost is shown, not charged: a global price that cannot be
	// evaluated leaves it at the list cost rather than block the charge.
	if rule.Base != nil {
		if cost.base, err = baseCost(rule.Base, p); err != nil {
			slog.Warn("usage: base price not evaluated, base cost = list cost", "request_id", p.RequestID, "err", err)
			cost.base = cost.list
		}
	}
	detail.Cost, detail.ListCost = cost.base, &cost.list
	return cost, detail, prog.Hash(), nil
}

// evaluate runs prog over the row's usage, normalized for the token classes
// prog prices.
func evaluate(prog *expr.Program, p *pending) (*expr.Result, error) {
	vars := expr.Normalize(p.Inputs.Semantics, expr.Tokens(p.Tokens), prog.Uses)
	return prog.Eval(expr.Input{Vars: vars, Metrics: p.Metrics, Params: p.Inputs.Params, Headers: p.Inputs.Headers, At: p.CreatedAt})
}

// baseCost is what the row's usage costs at the global price a custom rule
// replaced. The usage is normalized again: the two expressions may price
// different cache classes.
func baseCost(base *core.PriceBase, p *pending) (decimal.Decimal, error) {
	prog, err := expr.CompileCached(base.Expression)
	if err != nil {
		return decimal.Zero, fmt.Errorf("compile base price expression: %w", err)
	}
	res, err := evaluate(prog, p)
	if err != nil {
		return decimal.Zero, err
	}
	return res.Cost, nil
}

// settle computes the cost and, in one transaction, writes the ledger row,
// the usage billing fields and the usage.recorded event.
func (s *Service) settle(ctx context.Context, p *pending, skipLocked bool) error {
	var err error
	var ledgerRes *core.LedgerResult
	err = s.db.Tx(ctx, func(tx *gorm.DB) error {
		var err error
		ledgerRes, err = s.settleTx(ctx, tx, p, skipLocked)
		return err
	})
	if errors.Is(err, errNotPending) {
		return nil
	}
	if err != nil {
		return err
	}
	s.cacheBalance(ctx, p.UserID, ledgerRes)
	return nil
}

func (s *Service) markFailed(ctx context.Context, p *pending, cause error) {
	attempts := p.Attempts + 1
	slog.WarnContext(ctx, "usage: settlement failed", "request_id", p.RequestID, "attempts", attempts, "err", cause)
	if attempts >= s.opts.MaxAttempts {
		slog.ErrorContext(ctx, "usage: settlement gave up", "request_id", p.RequestID, "attempts", attempts, "err", cause)
	}
	patch := jsonOr(map[string]any{"attempts": attempts, "error": trunc(cause.Error(), 1000), "inputs": p.Inputs}, "{}")
	// Only a row still unsettled; patch is merged into its billing detail.
	if err := store.G(ctx, s.db.Gorm).Model(&model.UsageLog{}).
		Where("request_id = ? AND billing_status IN ('pending', 'failed')", p.RequestID).
		Updates(map[string]any{
			"billing_status": StatusFailed,
			"billing_detail": gorm.Expr("billing_detail || ?::jsonb", patch),
		}).Error; err != nil {
		slog.ErrorContext(ctx, "usage: mark failed", "request_id", p.RequestID, "err", err)
	}
}

func recordedEvent(p *pending, total decimal.Decimal, status string) core.Event {
	t := p.Tokens
	return core.Event{Type: core.EventUsageRecorded, Payload: map[string]any{
		"request_id": p.RequestID, "user_id": p.UserID, "api_key_id": p.APIKeyID, "group_id": p.GroupID,
		"account_id": p.AccountID, "plugin_key": p.PluginKey, "platform": p.Platform, "protocol": p.Protocol,
		"account_type": p.AccountType, "upstream_protocol": p.UpstreamProtocol,
		"model": p.Model, "success": p.Success, "status_code": p.StatusCode, "error_type": p.ErrorType,
		"input_tokens": t.Input, "output_tokens": t.Output, "cache_read_tokens": t.CacheRead,
		"cache_creation_tokens": t.CacheCreation, "cache_creation_1h_tokens": t.CacheCreation1h,
		"image_input_tokens": t.ImageInput, "image_output_tokens": t.ImageOutput,
		"total_cost": total, "billing_status": status, "latency_ms": p.LatencyMs,
		"created_at": p.CreatedAt.UTC().Format(time.RFC3339Nano),
	}}
}

// ---------------------------------------------------------------- retry

// SettlementRetryTask is the node task that retries pending settlements.
const SettlementRetryTask = "core.usage.settlement_retry"

// RetryTaskSpec is core.usage.settlement_retry: every Options.RetryInterval
// on each node that may work, RetryPending (rows are shared through SKIP
// LOCKED and settled under their idempotency keys).
func (s *Service) RetryTaskSpec() core.TaskSpec {
	return core.TaskSpec{
		Name:  SettlementRetryTask,
		Title: core.LocalizedText{"en": "Retry pending settlements", "zh": "重试待结算用量"},
		Description: core.LocalizedText{
			"en": "Releases expired pre-charges and settles again the usage rows that are still pending or failed; every node that may work takes its share.",
			"zh": "释放过期的预扣，并重新结算仍处于待结算或失败的用量记录；每个可工作节点都跑，各自分摊。",
		},
		Scope:       core.TaskNode,
		Every:       s.opts.RetryInterval,
		Timeout:     5 * time.Minute,
		RequireWork: true,
		Handler: func(ctx context.Context, _ core.TaskRun) (core.TaskResult, error) {
			n, err := s.RetryPending(ctx)
			if n > 0 {
				slog.InfoContext(ctx, "usage: retried pending settlements", "rows", n)
			}
			return core.TaskResult{Count: n}, err
		},
	}
}

// frozenPrice reads usage_logs u back for pricing, the pendingColumns and
// more: h holds the expression the row froze, by its hash in the price
// history, and mp the current expression of its price for a row whose hash
// is not there. A row under a custom price rule gets no mp: its price_id is
// the global price, not what it was priced at, so without its history it
// has no expression and priceOne refuses it.
func frozenPrice(db *gorm.DB, more string) *gorm.DB {
	return db.Table("usage_logs u").Select(pendingColumns + more + ", COALESCE(h.expression, mp.expression, '')").
		Joins("LEFT JOIN model_price_history h ON h.expr_hash = u.expr_hash").
		Joins(`LEFT JOIN model_prices mp ON mp.id = u.price_id AND h.expr_hash IS NULL
			AND u.billing_detail #> '{inputs,pricing,base}' IS NULL`)
}

// pendingColumns are the usage_logs u columns of a pending, in scanPending's
// order.
const pendingColumns = `u.request_id, u.user_id, u.api_key_id, u.group_id, u.account_id, u.plugin_key, u.platform,
		u.protocol, u.account_type, u.upstream_protocol, u.model, u.success, u.status_code, u.error_type,
		u.input_tokens, u.output_tokens, u.cache_read_tokens, u.cache_creation_tokens, u.cache_creation_1h_tokens,
		u.image_input_tokens, u.image_output_tokens, u.metrics, u.latency_ms, u.created_at, u.rate_multiplier, COALESCE(u.price_id, 0), u.billing_mode, u.billing_detail`

// scanPending reads a frozenPrice row into p: pendingColumns, then more,
// then the expression.
func scanPending(row store.RowScanner, p *pending, more ...any) error {
	var metrics, detail []byte
	dest := append([]any{&p.RequestID, &p.UserID, &p.APIKeyID, &p.GroupID, &p.AccountID, &p.PluginKey, &p.Platform,
		&p.Protocol, &p.AccountType, &p.UpstreamProtocol, &p.Model, &p.Success, &p.StatusCode, &p.ErrorType,
		&p.Tokens.Input, &p.Tokens.Output, &p.Tokens.CacheRead, &p.Tokens.CacheCreation, &p.Tokens.CacheCreation1h,
		&p.Tokens.ImageInput, &p.Tokens.ImageOutput, &metrics, &p.LatencyMs, &p.CreatedAt, &p.Rate, &p.PriceID, &p.Mode, &detail}, more...)
	if err := row.Scan(append(dest, &p.Expression)...); err != nil {
		return err
	}
	_ = json.Unmarshal(metrics, &p.Metrics)
	var pd pendingDetail
	_ = json.Unmarshal(detail, &pd)
	p.Inputs, p.Attempts = pd.Inputs, pd.Attempts
	return nil
}

// RetryPending settles pending/failed rows older than Options.RetryAfter
// that have not exhausted their attempts. It returns the rows examined.
func (s *Service) RetryPending(ctx context.Context) (int, error) {
	if s.opts.CanRetry != nil && !s.opts.CanRetry() {
		return 0, nil
	}
	if s.ledger == nil {
		return 0, errors.New("no ledger configured")
	}
	if err := s.ledger.ReleaseExpiredPrecharges(ctx); err != nil {
		return 0, err
	}
	if err := s.recoverExecutions(ctx); err != nil {
		slog.WarnContext(ctx, "usage: recover execution observations", "err", err)
	}
	list, err := store.CollectRows(frozenPrice(store.G(ctx, s.db.Gorm), "").
		Where(`u.billing_status IN ('pending', 'failed')
		  AND u.created_at < now() - make_interval(secs => ?)
		  AND COALESCE((u.billing_detail->>'attempts')::int, 0) < ?`, s.opts.RetryAfter.Seconds(), s.opts.MaxAttempts).
		Order("u.id").Limit(500), func(rows *sql.Rows) (*pending, error) {
		p := &pending{}
		return p, scanPending(rows, p)
	})
	if err != nil {
		return 0, err
	}
	for _, p := range list {
		if ctx.Err() != nil {
			break
		}
		if err := s.settle(ctx, p, true); err != nil {
			s.markFailed(ctx, p, err)
		}
	}
	return len(list), nil
}

func (s *Service) settleTx(ctx context.Context, tx *gorm.DB, p *pending, skipLocked bool) (*core.LedgerResult, error) {
	if err := store.RequireTx(tx); err != nil {
		return nil, err
	}
	cost, detail, exprHash, err := s.priceOf(ctx, p)
	if err != nil {
		return nil, err
	}
	total := cost.total
	var ledgerRes *core.LedgerResult
	// skipLocked passes over a row another settlement holds (no row).
	lock := store.ForUpdate()
	if skipLocked {
		lock = lock.SkipLocked()
	}
	// The model is a value: an UPDATE through a pointer to one copies each
	// assigned value into it, on every settlement, for nobody to read.
	g, row := store.G(ctx, tx), model.UsageLog{}
	var status string
	if err := g.Model(row).Select("billing_status").Where("request_id = ?", p.RequestID).
		Clauses(lock).Row().Scan(&status); err != nil {
		if store.IsNoRows(err) {
			return nil, errNotPending
		}
		return nil, err
	}
	if status != StatusPending && status != StatusFailed {
		return nil, errNotPending
	}
	ledgerRes, err = s.settleLedgerTx(ctx, tx, p.UserID, p.RequestID, total)
	if err != nil {
		return nil, err
	}
	if total.Sign() > 0 {
		detail.LedgerID = &ledgerRes.LedgerID
	}
	var priceID *int64
	if p.PriceID > 0 {
		priceID = &p.PriceID
	}
	err = g.Model(row).Where("request_id = ?", p.RequestID).Updates(map[string]any{
		"total_cost": cost.total, "billing_status": StatusBilled, "billing_detail": store.Bytes(jsonOr(detail, "{}")),
		"matched_tier": trunc(detail.Tier, 100), "expr_hash": exprHash, "billing_mode": p.Mode, "price_id": priceID,
		"rate_multiplier": p.Rate, "base_cost": cost.base,
	}).Error
	if err != nil {
		return nil, err
	}
	if s.events != nil {
		return ledgerRes, s.events.Emit(ctx, tx, recordedEvent(p, total, StatusBilled))
	}
	return ledgerRes, nil
}
