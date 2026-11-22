package usage

import (
	"context"
	"encoding/json"
	"slices"
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
	var run insertRun
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		if before != nil {
			if err := before(tx); err != nil {
				return err
			}
		}
		run = insertRun{ids: make(map[string]int64, len(batch))}
		if err := run.insertRows(ctx, tx, batch); err != nil {
			return err
		}
		if err := s.chargeInserted(ctx, tx, &run, settleNow); err != nil {
			return err
		}
		if after != nil {
			if err := after(tx, run.ids); err != nil {
				return err
			}
		}
		if s.events != nil && len(run.free) > 0 {
			return s.events.Emit(ctx, tx, run.free...)
		}
		return nil
	})
	if err == nil {
		for _, c := range run.cached {
			s.ledger.CacheBalance(ctx, c.userID, c.ledgerID, c.balance)
		}
	}
	return run.inserted, err
}

// insertRun is what one insertAtomic transaction did: the records it
// inserted with their row ids, the usage.recorded events of the free ones,
// the reserved rows, and the balance cache refreshes owed after commit.
type insertRun struct {
	inserted []*core.UsageRecord
	ids      map[string]int64
	free     []core.Event
	reserved []reservedRow
	cached   []balanceUpdate
}

// insertRows inserts the rows of batch on tx and sorts the new ones by
// their initial status; duplicate request ids are skipped.
func (run *insertRun) insertRows(ctx context.Context, tx *gorm.DB, batch []*core.UsageRecord) error {
	added, err := insertUsageRows(store.G(ctx, tx), batch)
	if err != nil {
		return err
	}
	for i, rec := range batch {
		id := added[i]
		if id == 0 {
			continue // duplicate request id
		}
		run.inserted = append(run.inserted, rec)
		run.ids[rec.RequestID] = id
		switch initialStatus(rec) {
		case StatusFree:
			run.free = append(run.free, recordedEvent(fromRecord(rec), decimal.Zero, StatusFree))
		case StatusReserved:
			run.reserved = append(run.reserved, reservedRow{rec: rec, id: id})
		}
	}
	return nil
}

// cacheAfterCommit records the balance cache refresh of userID after the ledger change
// lr, skipping a nil result and idempotent replays.
func (run *insertRun) cacheAfterCommit(userID int64, lr *core.LedgerResult) {
	if lr != nil && !lr.Duplicate {
		run.cached = append(run.cached, balanceUpdate{userID: userID, ledgerID: lr.LedgerID, balance: lr.BalanceAfter})
	}
}

// chargeInserted moves the money of the inserted rows on tx: free rows
// release their precharge, reserved rows charge the estimate and, with
// settleNow, pending rows are settled.
func (s *Service) chargeInserted(ctx context.Context, tx *gorm.DB, run *insertRun, settleNow bool) error {
	for _, rec := range run.inserted {
		if initialStatus(rec) != StatusFree {
			continue
		}
		lr, err := s.releasePrechargeTx(ctx, tx, rec.UserID, rec.RequestID)
		if err != nil {
			return err
		}
		run.cacheAfterCommit(rec.UserID, lr)
	}
	// Charging the estimate and registering the entry happen in the same
	// transaction as the row itself: a reserved row that exists without a
	// ledger entry would be a free request, and one without a
	// pending_settlements entry would keep the estimate forever with
	// nothing left to correct it.
	for _, r := range run.reserved {
		res, err := s.reserveTx(ctx, tx, r)
		if err != nil {
			return err
		}
		run.cacheAfterCommit(r.rec.UserID, res)
	}
	if settleNow {
		return s.settleInserted(ctx, tx, run)
	}
	return nil
}

// settleInserted settles the pending rows of run on tx.
func (s *Service) settleInserted(ctx context.Context, tx *gorm.DB, run *insertRun) error {
	for _, rec := range run.inserted {
		if initialStatus(rec) == StatusPending {
			lr, err := s.settleTx(ctx, tx, fromRecord(rec), false)
			if err != nil {
				return err
			}
			run.cacheAfterCommit(rec.UserID, lr)
		}
	}
	return nil
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
	return writtenIDs(db, ids)
}

// writtenIDs is ids with 0 for each id whose row was not written.
func writtenIDs(db *gorm.DB, ids []int64) ([]int64, error) {
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
	priceID, exprHash, mode, detail := pricingColumns(rec, status)
	// The observability markers have their own column, so they are
	// written once here and never touched again: settle() rewrites
	// billing_detail wholesale, and used to have to carry them
	// through the unbilled phase to put them back.
	anomalies := jsonOr(rec.Anomalies(), "{}")
	created := rec.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	attempts := max(rec.Attempts, 1)
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

// pricingColumns are the price id, expression hash, billing mode and
// billing_detail of rec's new row at status.
func pricingColumns(rec *core.UsageRecord, status string) (priceID *int64, exprHash, mode string, detail []byte) {
	// Free requests also need their token semantics for usage summaries.
	// Only billable requests retain pricing headers and parameters below.
	detail = jsonOr(pendingDetail{Inputs: pendingInputs{Semantics: rec.UsageSemantics}}, "{}")
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
	return priceID, exprHash, mode, detail
}
