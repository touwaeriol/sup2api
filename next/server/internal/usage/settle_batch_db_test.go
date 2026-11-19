package usage

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// The usage_logs insert of a settler batch is one INSERT of its rows, with
// ids reserved before it (insertUsageRows). A batch of 203 records holds a request id already
// stored, a repeat next to its first record, a repeat far from it and two
// ids that differ only past the 64 characters the column keeps: each of
// them is skipped, the first record of an id is the one inserted, and each
// inserted record gets its own row's id.
func TestInsertBatchChunksAndDuplicates(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	stored := f.record("batch-stored", false)
	if _, err := f.svc.insert(ctx, []*core.UsageRecord{stored}); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("x", 64)
	var batch []*core.UsageRecord
	for i := range 198 {
		rec := f.record(fmt.Sprintf("batch-%03d", i), i%3 != 0)
		rec.Tokens.Input = int64(i)
		batch = append(batch, rec)
	}
	// Index 3 repeats index 2 (pending) in the first chunk, as a free record.
	batch = slices.Insert(batch, 3, f.record("batch-002", false))
	batch = append(batch,
		f.record("batch-005", false),   // 199, in the chunk of 192..199
		f.record("batch-stored", true), // 200, in the chunk of 200..201
		f.record(long+"-a", false),     // 201
		f.record(long+"-b", true),      // 202, the last chunk: 201's row
	)
	if len(batch) != 203 {
		t.Fatalf("batch of %d", len(batch))
	}
	var ids map[string]int64
	inserted, err := f.svc.insertAtomic(ctx, batch, nil, func(_ *gorm.DB, got map[string]int64) error {
		ids = got
		return nil
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	skipped := map[*core.UsageRecord]bool{batch[3]: true, batch[199]: true, batch[200]: true, batch[202]: true}
	var want []*core.UsageRecord
	for _, rec := range batch {
		if !skipped[rec] {
			want = append(want, rec)
		}
	}
	if len(inserted) != len(want) {
		t.Fatalf("inserted %d records, want %d", len(inserted), len(want))
	}
	for i, rec := range want {
		if inserted[i] != rec {
			t.Fatalf("inserted[%d] is %s, want %s", i, inserted[i].RequestID, rec.RequestID)
		}
		// The id is the row of this record: its own request id and tokens.
		// A native request's conversion is NULL, its documents are stored.
		row := f.scalar(`SELECT request_id || ' ' || input_tokens || ' ' || billing_status || ' ' || (conversion IS NULL)
			|| ' ' || plugin_detail::text || ' ' || jsonb_array_length(sched_decisions) FROM usage_logs WHERE id = $1`, ids[rec.RequestID])
		if want := trunc(rec.RequestID, 64) + " " + fmt.Sprint(rec.Tokens.Input) + " " + initialStatus(rec) + " true {} 1"; row != want {
			t.Fatalf("row of %s: %q, want %q", rec.RequestID, row, want)
		}
	}
	if n := f.scalar(`SELECT count(*) FROM usage_logs`); n != fmt.Sprint(len(want)+1) {
		t.Fatalf("%s rows", n)
	}
	// A repeat never overwrote the first record's row.
	if s := f.scalar(`SELECT billing_status FROM usage_logs WHERE request_id = 'batch-002'`); s != StatusPending {
		t.Fatalf("batch-002 is %s", s)
	}
	free := 0
	for _, rec := range want {
		if initialStatus(rec) == StatusFree {
			free++
		}
	}
	// One usage.recorded per free record inserted (the stored one included).
	if n := f.scalar(`SELECT count(*) FROM events WHERE type = 'usage.recorded'`); n != fmt.Sprint(free+1) {
		t.Fatalf("%s usage.recorded events, want %d", n, free+1)
	}
}

// A pending row settled by several workers at once (a retry racing the
// queue) is charged once, and the precharge it held comes back once.
func TestSettleConcurrentSameRow(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	rec := f.record("race-row", true)
	if err := f.bill.Precharge(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.insert(ctx, []*core.UsageRecord{rec}); err != nil {
		t.Fatal(err)
	}
	p := fromRecord(rec)
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for i := range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- f.svc.settle(ctx, p, i%2 == 1) // the retry loop skips locked rows
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if s := f.scalar(`SELECT billing_status || ' ' || total_cost::text FROM usage_logs WHERE request_id = 'race-row'`); s != "billed 0.70800000" {
		t.Fatalf("row: %s", s)
	}
	for key, want := range map[string]string{"usage:race-row": "1", "precharge_release:race-row": "1", "precharge:race-row": "1"} {
		if n := f.scalar(`SELECT count(*) FROM balance_ledger WHERE idempotency_key = $1`, key); n != want {
			t.Fatalf("%s: %s rows", key, n)
		}
	}
	// 10 - 0.708: the precharge went out and came back once.
	if b := f.balance(); !b.Equal(dec("9.292")) {
		t.Fatalf("balance %s", b)
	}
	if s := f.scalar(`SELECT (SELECT sum(delta) FROM balance_ledger WHERE user_id = $1) = (SELECT balance FROM user_balances WHERE user_id = $1)`, f.user); s != "true" {
		t.Fatal("balance is not the ledger's sum")
	}
}

// valuesSQL and valuesArgs are a plain one-row VALUES insert: the statement
// and its arguments, written out on their own, not derived from
// usageLogRow and newUsageLogRow, which they check.
const valuesSQL = `
	INSERT INTO usage_logs (request_id, user_id, api_key_id, group_id, account_id, plugin_key, plugin_version,
		platform, protocol, endpoint, model, upstream_model, stream, status_code, success, error_type,
		error_message, attempts, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens,
		cache_creation_1h_tokens, image_input_tokens, image_output_tokens, metrics, sticky_rule, sticky_hit, hook_decisions, rate_multiplier,
		price_id, expr_hash, billing_mode, billing_detail, billing_status, latency_ms, first_token_ms,
		client_ip, user_agent, node_id, created_at, account_type, upstream_protocol, client_request_id,
		sched_decisions, plugin_detail, anomalies, upload_ms, conversion)
	VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,
		$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38,$39,$40,$41,$42,$43,$44,$45,$46,$47,$48,$49)
	ON CONFLICT (request_id) DO NOTHING
	RETURNING id`

func valuesArgs(rec *core.UsageRecord) []any {
	status := initialStatus(rec)
	var priceID *int64
	var exprHash, mode string
	detail := jsonOr(pendingDetail{Inputs: pendingInputs{Semantics: rec.UsageSemantics}}, "{}")
	if status == StatusPending || status == StatusReserved {
		if rec.Price != nil {
			exprHash, mode = rec.Price.ExprHash, rec.Price.Mode
			if rec.Price.ID > 0 {
				priceID = &rec.Price.ID
			}
			if exprHash == "" {
				exprHash = expr.Hash(rec.Price.Expression)
			}
		}
		detail = jsonOr(pendingDetail{Inputs: recordInputs(rec)}, "{}")
	}
	anomalies := jsonOr(rec.Anomalies(), "{}")
	created := rec.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	attempts := rec.Attempts
	if attempts <= 0 {
		attempts = 1
	}
	rate := rec.RateMultiplier
	t := rec.Tokens
	return []any{trunc(rec.RequestID, 64), rec.UserID, rec.APIKeyID, rec.GroupID, rec.AccountID,
		trunc(rec.PluginKey, 30), trunc(rec.PluginVersion, 50), trunc(rec.Platform, 50),
		trunc(rec.Protocol, 100), trunc(rec.Endpoint, 200), trunc(rec.Model, 200), trunc(rec.UpstreamModel, 200),
		rec.Stream, rec.StatusCode, rec.Success, trunc(rec.ErrorType, 50), rec.ErrorMessage, attempts,
		t.Input, t.Output, t.CacheRead, t.CacheCreation, t.CacheCreation1h, t.ImageInput, t.ImageOutput,
		jsonOr(rec.Metrics, "{}"), trunc(rec.StickyRule, 100), rec.StickyHit, jsonOr(rec.HookDecisions, "[]"),
		rate, priceID, exprHash, mode, detail, status, rec.LatencyMs, rec.FirstTokenMs,
		trunc(rec.ClientIP, 64), trunc(rec.UserAgent, 500), trunc(rec.NodeID, 100), created,
		trunc(rec.AccountType, 50), trunc(rec.UpstreamProtocol, 100), trunc(rec.ClientRequestID, 128),
		jsonOr(rec.SchedDecisions, "[]"), pluginDetailJSON(rec.PluginDetail), anomalies, rec.UploadMs, conversionJSON(rec.Conversion)}
}

// The batch insert stores each value as a one-row VALUES insert does
// (valuesArgs), column by column: a nanosecond
// timestamp, a rate past the column's scale, NULL and non-NULL account and
// conversion, documents, free and billable records.
func TestInsertBatchMatchesValuesInsert(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	account := int64(42)
	full := func(rec *core.UsageRecord) *core.UsageRecord {
		rec.AccountID, rec.Stream, rec.StickyRule, rec.StickyHit = &account, true, "rule", true
		rec.ErrorType, rec.ErrorMessage, rec.Attempts = "upstream", "boom ü", 3
		rec.LatencyMs, rec.FirstTokenMs, rec.UploadMs = 1234, 56, 7
		rec.ClientIP, rec.UserAgent, rec.NodeID, rec.ClientRequestID = "203.0.113.9", "ua/1", "node-1", "client-1"
		rec.RateMultiplier = decimal.RequireFromString("1.23456")
		rec.CreatedAt = time.Date(2026, 11, 15, 12, 0, 0, 123456789, time.UTC)
		rec.Metrics = map[string]any{"seconds": 8.5}
		rec.Tokens.ImageInput, rec.Tokens.ImageOutput = 30, 1500
		rec.PluginDetail = core.RawJSON(`{"job":"j-1"}`)
		rec.Conversion = &core.ConversionTrace{V: 1, Grade: "lossy", Hops: []string{"a", "b"}}
		return rec
	}
	variants := map[string]func(id string) *core.UsageRecord{
		"billable": func(id string) *core.UsageRecord { return full(f.record(id, true)) },
		"free":     func(id string) *core.UsageRecord { return f.record(id, false) },
	}
	var batch []*core.UsageRecord
	for name, build := range variants {
		var id int64
		if err := f.db.SQL.QueryRowContext(ctx, valuesSQL, valuesArgs(build("values-"+name))...).Scan(&id); err != nil {
			t.Fatal(err)
		}
		batch = append(batch, build("batch-"+name))
		// A batch of one is one INSERT without reserved ids.
		if _, err := f.svc.insert(ctx, []*core.UsageRecord{build("row-" + name)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.svc.insert(ctx, batch); err != nil {
		t.Fatal(err)
	}
	const row = `SELECT (to_jsonb(u) - 'id' - 'request_id')::text FROM usage_logs u WHERE request_id = $1`
	for name := range variants {
		want := f.scalar(row, "values-"+name)
		for _, form := range []string{"batch-", "row-"} {
			if got := f.scalar(row, form+name); got != want {
				t.Fatalf("%s%s:\n got %s\nwant %s", form, name, got, want)
			}
		}
	}
}

// A batch, 250 records here, is its ids, then an INSERT per store.InsertChunk
// rows, the full chunks in one statement text.
func TestInsertBatchChunks(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	batch := make([]*core.UsageRecord, 2*store.InsertChunk+50)
	for i := range batch {
		batch[i] = f.record(fmt.Sprintf("one-%03d", i), i%2 == 0)
	}
	db, trace := testutil.Traced(f.db)
	var statements []string
	err := db.Tx(ctx, func(tx *gorm.DB) error {
		trace.Take() // BEGIN
		ids, err := insertUsageRows(store.G(ctx, tx), batch)
		statements = trace.Take()
		for i, id := range ids {
			if id == 0 {
				t.Errorf("record %d not inserted", i)
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(statements) != 4 || statements[1] != statements[2] || statements[2] == statements[3] {
		t.Fatalf("a batch of %d: want the ids, two full chunks of one text and the rest, got %d statements", len(batch), len(statements))
	}
}
