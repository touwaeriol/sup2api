package usage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/event"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

const a6Expr = `(len <= 200000 ? tier("standard", p*3 + c*15 + cr*0.3 + cc*3.75 + cc1h*6) : tier("long_context", p*6 + c*22.5 + cr*0.6 + cc*7.5 + cc1h*12)) * (has(header("anthropic-beta"), "fast-mode") ? 2 : 1)`

// flakyLedger fails the first n ApplyTx calls.
type flakyLedger struct {
	*billing.Service
	fail atomic.Int32
}

func (f *flakyLedger) ApplyTx(ctx context.Context, tx *gorm.DB, ch core.LedgerChange) (*core.LedgerResult, error) {
	if f.fail.Add(-1) >= 0 {
		return nil, errors.New("ledger unavailable")
	}
	return f.Service.ApplyTx(ctx, tx, ch)
}

func (f *flakyLedger) SettleTx(ctx context.Context, tx *gorm.DB, userID int64, requestID string, charge *core.LedgerChange) (*core.LedgerResult, error) {
	if charge != nil && f.fail.Add(-1) >= 0 {
		return nil, errors.New("ledger unavailable")
	}
	return f.Service.SettleTx(ctx, tx, userID, requestID, charge)
}

type fixture struct {
	t      *testing.T
	db     *store.DB
	bill   *billing.Service
	ledger *flakyLedger
	svc    *Service
	h      *gin.Engine
	user   int64
	other  int64
	group  int64
	price  *core.PriceRule
}

type allowAll struct{}

func (allowAll) VerifySession(_ context.Context, token string) (int64, string, error) {
	return testutil.SessionUser(token, "")
}
func (allowAll) Can(context.Context, int64, string) (bool, error) { return true, nil }

// denyPerms holds every permission but the listed ones.
type denyPerms struct {
	allowAll
	keys []string
}

func (d denyPerms) Can(_ context.Context, _ int64, perm string) (bool, error) {
	return !slices.Contains(d.keys, perm), nil
}
func (allowAll) PermissionSet(context.Context, int64) (core.PermissionSet, error) {
	return core.PermissionSet{Superuser: true}, nil
}
func (allowAll) CanGrant(context.Context, int64, []string) error { return nil }

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	db := testutil.DB(t)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	pub := event.NewPublisher(db)
	bill := billing.New(db, rdb, nil, pub, nil)
	f := &fixture{t: t, db: db, bill: bill, ledger: &flakyLedger{Service: bill}}
	f.svc = New(db, f.ledger, bill, pub, Options{FlushInterval: 10 * time.Millisecond, Workers: 2, RetryInterval: time.Hour, RetryAfter: time.Millisecond})
	f.svc.Authz = allowAll{} // the caller holds every permission unless a test says otherwise
	// Most tests read their own writes; TestOperatorStatsCached turns these on.
	f.svc.overviews = newSnapshotCache[Overview](0, 0)
	f.svc.summaries = newSnapshotCache[[]SummaryRow](0, 0)

	for _, email := range []string{"u@example.com", "o@example.com"} {
		var id int64
		if err := db.SQL.QueryRowContext(ctx, `INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id`, email).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if f.user == 0 {
			f.user = id
		} else {
			f.other = id
		}
	}
	if err := db.SQL.QueryRowContext(ctx, `INSERT INTO groups (name) VALUES ('default') RETURNING id`).Scan(&f.group); err != nil {
		t.Fatal(err)
	}
	if _, err := bill.Apply(ctx, core.LedgerChange{UserID: f.user, Amount: decimal.NewFromInt(10), Credit: true, Kind: "admin_adjust", IdempotencyKey: "seed"}); err != nil {
		t.Fatal(err)
	}
	// Price row + history, as the billing module would store them.
	prog, err := expr.Compile(a6Expr)
	if err != nil {
		t.Fatal(err)
	}
	f.price = &core.PriceRule{Model: "claude-sonnet-4-5", Mode: "expression", Expression: a6Expr, ExprVersion: 1, ExprHash: prog.Hash()}
	err = db.SQL.QueryRowContext(ctx, `INSERT INTO model_prices (model, mode, expression, expr_hash, source)
		VALUES ('claude-sonnet-4-5', 'expression', $1, $2, 'manual') RETURNING id`, a6Expr, prog.Hash()).Scan(&f.price.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.ExecContext(ctx, `INSERT INTO model_price_history (expr_hash, expression, expr_version) VALUES ($1, $2, 1)`, prog.Hash(), prog.Expression()); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	f.h = gin.New()
	f.svc.RegisterRoutes(httpapi.NewRouter(f.h, allowAll{}, allowAll{}))
	return f
}

var bj10 = time.Date(2026, 9, 24, 2, 0, 0, 0, time.UTC)

func (f *fixture) record(id string, billable bool) *core.UsageRecord {
	rec := &core.UsageRecord{
		RequestID: id, UserID: f.user, APIKeyID: 1, GroupID: f.group, PluginKey: "anthropic", Platform: "anthropic",
		Protocol: "anthropic.messages", AccountType: "apikey", UpstreamProtocol: "anthropic.messages", Endpoint: "/v1/messages", Model: "claude-sonnet-x", StatusCode: 200,
		Success: true, Attempts: 1, UsageSemantics: "exclusive",
		Tokens:         core.UsageTokens{Input: 100000, Output: 2000, CacheRead: 80000},
		Billable:       billable,
		PriceHeaders:   map[string]string{"anthropic-beta": "fast-mode"},
		RateMultiplier: decimal.NewFromInt(1),
		HookDecisions:  []core.HookDecision{{PluginKey: "guard", HookID: "h", Decision: "allow"}},
		SchedDecisions: []core.SchedDecision{{PluginKey: "guard",
			Changed: []core.RankChange{{AccountID: 42, Priority: 10, Weight: 500}}}},
		CreatedAt: bj10,
	}
	if billable {
		rec.Price = f.price
	}
	return rec
}

func (f *fixture) scalar(sql string, args ...any) string {
	f.t.Helper()
	var v any
	if err := f.db.SQL.QueryRowContext(context.Background(), sql, args...).Scan(&v); err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
	switch x := v.(type) {
	case nil:
		return "<nil>"
	case string:
		return x
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func (f *fixture) balance() decimal.Decimal {
	var d decimal.Decimal
	_ = f.db.SQL.QueryRowContext(context.Background(), `SELECT balance FROM user_balances WHERE user_id = $1`, f.user).Scan(&d)
	return d
}

// priceAmounts prices a pending charge as settlement does, without a
// database: the billing module's Quote reads none.
func priceAmounts(p *pending) (amounts, BillingDetail, string, error) {
	s := &Service{quoter: billing.New(nil, nil, nil, nil, nil)}
	return s.priceOf(context.Background(), p)
}

// priceOf is priceAmounts for the tests that read the charge alone.
func priceOf(p *pending) (decimal.Decimal, BillingDetail, string, error) {
	cost, detail, hash, err := priceAmounts(p)
	return cost.total, detail, hash, err
}

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func decPtr(s string) *decimal.Decimal {
	d := dec(s)
	return &d
}

func requireDec(t *testing.T, want string, got decimal.Decimal, msg string) {
	t.Helper()
	require.True(t, dec(want).Equal(got), "%s = %s, want %s", msg, got, want)
}

func TestRecordedEventPayload(t *testing.T) {
	rec := &core.UsageRecord{RequestID: "r", PluginKey: "relay", Platform: "openai", Protocol: "openai.chat",
		AccountType: "relay_key", UpstreamProtocol: "anthropic.messages", Model: "m", CreatedAt: time.Unix(0, 0)}
	ev := recordedEvent(fromRecord(rec), decimal.NewFromInt(1), StatusBilled)
	p := ev.Payload.(map[string]any)
	if ev.Type != core.EventUsageRecorded || p["account_type"] != "relay_key" || p["upstream_protocol"] != "anthropic.messages" ||
		p["platform"] != "openai" || p["protocol"] != "openai.chat" || p["plugin_key"] != "relay" {
		t.Fatalf("payload: %v", p)
	}
}

func TestSettleFlow(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.svc.Start(ctx)

	r1 := f.record("req-1", true)
	free := f.record("req-free", false)
	free.Success, free.StatusCode, free.ErrorType, free.Tokens = false, 403, "blocked_by_hook", core.UsageTokens{}
	r3 := f.record("req-3", true)
	r3.RateMultiplier = decimal.RequireFromString("1.5")
	r3.PriceHeaders = nil
	r3.Tokens = core.UsageTokens{Input: 1204, Output: 812}
	zero := f.record("req-zero", true)
	zero.Tokens = core.UsageTokens{}
	for _, r := range []*core.UsageRecord{r1, free, r3, zero, f.record("req-1", true)} {
		f.svc.Submit(r)
	}
	stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	f.svc.Stop(stopCtx)

	if n := f.scalar(`SELECT count(*) FROM usage_logs`); n != "4" {
		t.Fatalf("usage rows = %s", n)
	}
	if s := f.scalar(`SELECT billing_status || ' ' || total_cost::text || ' ' || matched_tier || ' ' || billing_mode FROM usage_logs WHERE request_id = 'req-1'`); s != "billed 0.70800000 standard expression" {
		t.Fatalf("req-1: %s", s)
	}
	if s := f.scalar(`SELECT billing_status || ' ' || total_cost::text FROM usage_logs WHERE request_id = 'req-free'`); s != "free 0.00000000" {
		t.Fatalf("req-free: %s", s)
	}
	// 1204*3 + 812*15 = 15792 micro-dollars, x1.5
	if s := f.scalar(`SELECT billing_status || ' ' || total_cost::text || ' ' || rate_multiplier::text FROM usage_logs WHERE request_id = 'req-3'`); s != "billed 0.02368800 1.5000" {
		t.Fatalf("req-3: %s", s)
	}
	if s := f.scalar(`SELECT billing_status || ' ' || total_cost::text FROM usage_logs WHERE request_id = 'req-zero'`); s != "billed 0.00000000" {
		t.Fatalf("req-zero: %s", s)
	}
	if b := f.balance(); !b.Equal(decimal.RequireFromString("9.268312")) {
		t.Fatalf("balance = %s", b)
	}
	if n := f.scalar(`SELECT count(*) FROM balance_ledger WHERE kind = 'usage'`); n != "2" {
		t.Fatalf("usage ledger rows = %s", n)
	}
	if n := f.scalar(`SELECT count(*) FROM events WHERE type = 'usage.recorded'`); n != "4" {
		t.Fatalf("usage.recorded events = %s", n)
	}
	ev := f.scalar(`SELECT payload->>'total_cost' || ' ' || (payload->>'billing_status') || ' ' || (payload->>'account_type') ||
		' ' || (payload->>'upstream_protocol') FROM events WHERE type = 'usage.recorded' AND payload->>'request_id' = 'req-1'`)
	if ev != "0.708 billed apikey anthropic.messages" {
		t.Fatalf("event: %s", ev)
	}
	if s := f.scalar(`SELECT account_type || ' ' || upstream_protocol FROM usage_logs WHERE request_id = 'req-free'`); s != "apikey anthropic.messages" {
		t.Fatalf("usage row: %s", s)
	}
	if ev := f.scalar(`SELECT payload->>'account_type' FROM events WHERE type = 'usage.recorded' AND payload->>'request_id' = 'req-free'`); ev != "apikey" {
		t.Fatalf("free event: %s", ev)
	}
	var detail BillingDetail
	raw := f.scalar(`SELECT billing_detail::text FROM usage_logs WHERE request_id = 'req-1'`)
	if err := json.Unmarshal([]byte(raw), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.LedgerID == nil || detail.Tier != "standard" || len(detail.Rules) != 1 || !detail.Rules[0].Matched ||
		detail.Breakdown.Vars.Len != 180000 || detail.Inputs.Headers["anthropic-beta"] != "fast-mode" {
		t.Fatalf("detail: %s", raw)
	}
}

func TestRetryPending(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.ledger.fail.Store(1)
	rec := f.record("req-r", true)
	f.svc.process(ctx, []*core.UsageRecord{rec})
	if s := f.scalar(`SELECT billing_status || ' ' || (billing_detail->>'attempts') FROM usage_logs WHERE request_id = 'req-r'`); s != "failed 1" {
		t.Fatalf("after failure: %s", s)
	}

	// A crash between insert and settlement leaves a pending row.
	crash := f.record("req-crash", true)
	if _, err := f.svc.insert(ctx, []*core.UsageRecord{crash}); err != nil {
		t.Fatal(err)
	}
	// A row whose price expression cannot be found fails permanently.
	lost := f.record("req-lost", true)
	lost.Price = &core.PriceRule{ID: 0, Mode: "expression", Expression: `tier("x", p*999)`, ExprHash: "missing"}
	if _, err := f.svc.insert(ctx, []*core.UsageRecord{lost}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)

	n, err := f.svc.RetryPending(ctx)
	if err != nil || n != 3 {
		t.Fatalf("retry: %d %v", n, err)
	}
	for _, id := range []string{"req-r", "req-crash"} {
		if s := f.scalar(`SELECT billing_status || ' ' || total_cost::text FROM usage_logs WHERE request_id = $1`, id); s != "billed 0.70800000" {
			t.Fatalf("%s: %s", id, s)
		}
	}
	if s := f.scalar(`SELECT billing_status || ' ' || (billing_detail->>'error') FROM usage_logs WHERE request_id = 'req-lost'`); s != "failed price expression not found" {
		t.Fatalf("req-lost: %s", s)
	}
	if b := f.balance(); !b.Equal(decimal.RequireFromString("8.584")) {
		t.Fatalf("balance = %s", b)
	}
	// Retrying again does not double charge, and exhausted rows are skipped.
	f.svc.opts.MaxAttempts = 2
	n, _ = f.svc.RetryPending(ctx)
	if n != 1 {
		t.Fatalf("second retry examined %d", n)
	}
	n, _ = f.svc.RetryPending(ctx)
	if n != 0 {
		t.Fatalf("exhausted rows retried: %d", n)
	}
	if b := f.balance(); !b.Equal(decimal.RequireFromString("8.584")) {
		t.Fatalf("balance after retries = %s", b)
	}
	// Settling an already billed row is a no-op.
	if err := f.svc.settle(ctx, fromRecord(rec), false); err != nil {
		t.Fatal(err)
	}
	if n := f.scalar(`SELECT count(*) FROM balance_ledger WHERE kind = 'usage'`); n != "2" {
		t.Fatalf("ledger rows = %s", n)
	}
}

func TestSubmitOverflow(t *testing.T) {
	f := newFixture(t)
	f.svc = New(f.db, f.ledger, f.ledger, event.NewPublisher(f.db), Options{QueueSize: 1})
	// Not started: the first record fills the queue, the second overflows
	// and is persisted directly as pending.
	f.svc.Submit(f.record("q-1", true))
	f.svc.Submit(f.record("q-2", true))
	deadline := time.Now().Add(10 * time.Second)
	for f.scalar(`SELECT count(*) FROM usage_logs WHERE request_id = 'q-2'`) != "1" {
		if time.Now().After(deadline) {
			t.Fatal("overflow record not persisted")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if s := f.scalar(`SELECT billing_status FROM usage_logs WHERE request_id = 'q-2'`); s != "pending" {
		t.Fatalf("overflow status %s", s)
	}
}

func (f *fixture) get(uid int64, path string, want int) map[string]any {
	f.t.Helper()
	req := httptest.NewRequest("GET", "/api/v1"+path, bytes.NewReader(nil))
	req.Header.Set("Authorization", "Bearer "+strconv.FormatInt(uid, 10))
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, req)
	if w.Code != want {
		f.t.Fatalf("GET %s: %d %s", path, w.Code, w.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out
}

func TestUsageAPI(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	other := f.record("req-other", false)
	other.UserID, other.Model = f.other, "claude-haiku-4"
	acc := int64(42)
	mine := f.record("req-mine", true)
	mine.AccountID = &acc
	mine.ClientRequestID = "cli-req-1"
	mine.UploadMs = 2120
	// Served through protocol conversion: client endpoint openai.chat, upstream
	// anthropic.messages.
	mine.Platform, mine.Protocol, mine.AccountType = "openai", "openai.chat", "relay_key"
	mineLater := f.record("req-mine-2", true)
	// The account's model mapping is upstream detail too: self views must not see it.
	mine.UpstreamModel, mineLater.UpstreamModel = "gpt-upstream-mapped", "claude-upstream-mapped"
	f.svc.process(ctx, []*core.UsageRecord{mine, other, mineLater})

	list := f.get(f.user, "/me/usage", 200)
	items := list["data"].([]any)
	if len(items) != 2 || list["page"].(map[string]any)["total"].(float64) != 2 {
		t.Fatalf("me/usage: %v", list)
	}
	first := items[0].(map[string]any)
	if first["account_id"] != nil || first["user_email"] != "u@example.com" || first["group_name"] != "default" || first["total_cost"] != "0.708" ||
		first["account_type"] != "" || first["upstream_protocol"] != "" || first["upstream_model"] != "" || first["upload_ms"] != float64(0) {
		t.Fatalf("row: %v", first)
	}
	all := f.get(f.user, "/usage?model=claude-haiku-4", 200)["data"].([]any)
	if len(all) != 1 || all[0].(map[string]any)["request_id"] != "req-other" ||
		all[0].(map[string]any)["account_type"] != "apikey" || all[0].(map[string]any)["upstream_protocol"] != "anthropic.messages" {
		t.Fatalf("usage filter: %v", all)
	}
	// An account type is (plugin key, type id): the filter needs both.
	if n := len(f.get(f.user, "/usage?plugin_key=anthropic&account_type=relay_key", 200)["data"].([]any)); n != 1 {
		t.Fatalf("account_type filter: %d", n)
	}
	if n := len(f.get(f.user, "/usage?plugin_key=relay&account_type=relay_key", 200)["data"].([]any)); n != 0 {
		t.Fatalf("account_type of another plugin: %d", n)
	}
	f.get(f.user, "/usage?account_type=relay_key", 400)
	byCRID := f.get(f.user, "/usage?client_request_id=cli-req-1", 200)["data"].([]any)
	if len(byCRID) != 1 || byCRID[0].(map[string]any)["request_id"] != "req-mine" ||
		byCRID[0].(map[string]any)["client_request_id"] != "cli-req-1" ||
		byCRID[0].(map[string]any)["upload_ms"] != float64(2120) {
		t.Fatalf("client_request_id filter: %v", byCRID)
	}
	if n := len(f.get(f.user, "/me/usage?client_request_id=cli-req-1", 200)["data"].([]any)); n != 1 {
		t.Fatalf("self client_request_id filter: %d", n)
	}
	if n := len(f.get(f.user, "/usage?success=true&user_id="+strconv.FormatInt(f.user, 10), 200)["data"].([]any)); n != 2 {
		t.Fatalf("success filter: %d", n)
	}
	if n := len(f.get(f.user, "/usage?from=2026-09-25T00:00:00Z", 200)["data"].([]any)); n != 0 {
		t.Fatalf("from filter: %d", n)
	}
	f.get(f.user, "/usage?from=bad", 400)

	var id, otherID int64
	_ = f.db.SQL.QueryRowContext(ctx, `SELECT id FROM usage_logs WHERE request_id = 'req-mine'`).Scan(&id)
	_ = f.db.SQL.QueryRowContext(ctx, `SELECT id FROM usage_logs WHERE request_id = 'req-other'`).Scan(&otherID)
	d := f.get(f.user, "/usage/"+strconv.FormatInt(id, 10), 200)["data"].(map[string]any)
	if d["ledger_id"] == nil || d["price"].(map[string]any)["model"] != "claude-sonnet-4-5" || d["account_id"].(float64) != 42 ||
		d["billing_detail"].(map[string]any)["tier"] != "standard" || len(d["hook_decisions"].([]any)) != 1 ||
		d["protocol"] != "openai.chat" || d["upstream_protocol"] != "anthropic.messages" || d["account_type"] != "relay_key" {
		t.Fatalf("detail: %v", d)
	}
	if _, has := d["price"].(map[string]any)["platform"]; has {
		t.Fatalf("price ref has platform: %v", d["price"])
	}
	// What the scheduler.rank plugins changed is stored per request (§24).
	if s := f.scalar(`SELECT sched_decisions->0->>'plugin' || ':' || (sched_decisions->0->'changed'->0->>'account_id')
		|| '/' || (sched_decisions->0->'changed'->0->>'priority') || '/' || (sched_decisions->0->'changed'->0->>'weight')
		FROM usage_logs WHERE request_id = 'req-mine'`); s != "guard:42/10/500" {
		t.Fatalf("sched_decisions %q", s)
	}
	md := f.get(f.user, "/me/usage/"+strconv.FormatInt(id, 10), 200)["data"].(map[string]any)
	if md["account_id"] != nil || md["account_type"] != "" || md["upstream_protocol"] != "" || md["upstream_model"] != "" {
		t.Fatalf("self detail leaks account: %v", md)
	}
	if md["client_request_id"] != "cli-req-1" || d["client_request_id"] != "cli-req-1" {
		t.Fatalf("client_request_id: self %v admin %v", md["client_request_id"], d["client_request_id"])
	}
	f.get(f.user, "/me/usage/"+strconv.FormatInt(otherID, 10), 404)
	f.get(f.user, "/usage/999999", 404)

	sum := f.get(f.user, "/usage/summary?group_by=model&from=2026-09-01T00:00:00Z", 200)["data"].([]any)
	if len(sum) != 2 {
		t.Fatalf("summary: %v", sum)
	}
	s0 := sum[1].(map[string]any)
	if s0["key"] != "claude-sonnet-x" || s0["requests"].(float64) != 2 || s0["total_cost"] != "1.416" || s0["input_tokens"].(float64) != 200000 {
		t.Fatalf("summary row: %v", s0)
	}
	days := f.get(f.user, "/usage/summary?group_by=day&from=2026-09-01T00:00:00Z", 200)["data"].([]any)
	if len(days) != 1 || days[0].(map[string]any)["key"] != "2026-09-24" {
		t.Fatalf("summary day: %v", days)
	}
	users := f.get(f.user, "/usage/summary?group_by=user&from=2026-09-01T00:00:00Z", 200)["data"].([]any)
	if len(users) != 2 || users[0].(map[string]any)["user_email"] == "" {
		t.Fatalf("summary user: %v", users)
	}
	f.get(f.user, "/usage/summary?group_by=week", 400)

	// The self summary only sees the caller's rows and cannot group by user.
	mine2 := f.get(f.user, "/me/usage/summary?group_by=model&from=2026-09-01T00:00:00Z", 200)["data"].([]any)
	if len(mine2) != 1 || mine2[0].(map[string]any)["key"] != "claude-sonnet-x" || mine2[0].(map[string]any)["requests"].(float64) != 2 {
		t.Fatalf("self summary: %v", mine2)
	}
	f.get(f.user, "/me/usage/summary?group_by=user", 400)
}

// A response shape the endpoint never declared is recorded in
// usage_logs.anomalies - its own column, so settling cannot erase it and
// settling does not have to carry it through the unbilled phase to put it
// back (CONTRACTS §26.5: the marker is observability, it changes nothing
// about the money, and billing_detail is not where it belongs).
func TestResponseMismatchInAnomalies(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	billed := f.record("req-mismatch", true)
	billed.ResponseMismatch = core.ResponseMismatchSSENotDeclared
	free := f.record("req-mismatch-free", false)
	free.ResponseMismatch = core.ResponseMismatchJSONWhileStream
	clean := f.record("req-clean", true)
	f.svc.process(ctx, []*core.UsageRecord{billed, free, clean})

	if s := f.scalar(`SELECT anomalies->>'response_mismatch' FROM usage_logs WHERE request_id = 'req-mismatch'`); s != core.ResponseMismatchSSENotDeclared {
		t.Fatalf("marker missing: %s", s)
	}
	// billing_detail holds billing and nothing else now.
	var detail BillingDetail
	raw := f.scalar(`SELECT billing_detail::text FROM usage_logs WHERE request_id = 'req-mismatch'`)
	if err := json.Unmarshal([]byte(raw), &detail); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "response_mismatch") {
		t.Fatalf("marker still in billing_detail: %s", raw)
	}
	// Everything settlement writes is still there.
	if detail.LedgerID == nil || detail.Tier != "standard" || len(detail.Rules) != 1 || !detail.Rules[0].Matched ||
		detail.Breakdown.Vars.Len != 180000 || detail.Inputs.Headers["anthropic-beta"] != "fast-mode" ||
		detail.Inputs.Semantics != "exclusive" || detail.Attempts != 1 || !detail.TotalCost.Equal(detail.Cost) {
		t.Fatalf("billing detail lost content: %s", raw)
	}
	if s := f.scalar(`SELECT billing_status FROM usage_logs WHERE request_id = 'req-mismatch'`); s != "billed" {
		t.Fatalf("status = %s", s)
	}

	// A free row is never settled, so the marker has to survive the insert.
	if s := f.scalar(`SELECT billing_status || ' ' || (anomalies->>'response_mismatch') FROM usage_logs WHERE request_id = 'req-mismatch-free'`); s != "free "+core.ResponseMismatchJSONWhileStream {
		t.Fatalf("free row: %s", s)
	}

	// A request whose shape matched must not be marked at all.
	if s := f.scalar(`SELECT anomalies::text FROM usage_logs WHERE request_id = 'req-clean'`); s != "{}" {
		t.Fatalf("clean row carries a marker: %s", s)
	}
}

// A row that fails to settle keeps the marker, and so does the row the retry
// loop finally bills: the column is written once at insert and neither
// markFailed nor settle touches it.
func TestResponseMismatchSurvivesFailedSettlement(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.ledger.fail.Store(1)
	rec := f.record("req-mismatch-retry", true)
	rec.ResponseMismatch = core.ResponseMismatchSSENotDeclared
	f.svc.process(ctx, []*core.UsageRecord{rec})
	if s := f.scalar(`SELECT billing_status || ' ' || (anomalies->>'response_mismatch') FROM usage_logs WHERE request_id = 'req-mismatch-retry'`); s != "failed "+core.ResponseMismatchSSENotDeclared {
		t.Fatalf("after failure: %s", s)
	}
	if _, err := f.svc.RetryPending(ctx); err != nil {
		t.Fatal(err)
	}
	if s := f.scalar(`SELECT billing_status || ' ' || (anomalies->>'response_mismatch') FROM usage_logs WHERE request_id = 'req-mismatch-retry'`); s != "billed "+core.ResponseMismatchSSENotDeclared {
		t.Fatalf("after retry: %s", s)
	}
}

// ---------------------------------------------------------------- plugin-reported usage

// What a plugin stated goes into usage_logs.plugin_detail and nowhere else.
// The row's attribution and money are the core's, computed from the record
// the gateway built - which is why UsageReport has no field for any of them
// (CONTRACTS §25.3, PLUGIN-EXECUTES-CORE-RECORDS §4).
func TestPluginDetailStoredAndBoundaryHolds(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	rec := f.record("req-plugin", true)
	rec.UsageExtract = core.UsageExtractPlugin
	// Everything a plugin can reach: its own opaque document (which here
	// claims a different user and a zero cost) and the metering facts.
	rec.PluginDetail = core.RawJSON(`{"task":"t-1","user_id":999,"total_cost":0}`)
	rec.Metrics = map[string]any{"images": float64(2)}
	f.svc.process(ctx, []*core.UsageRecord{rec})

	if s := f.scalar(`SELECT plugin_detail->>'task' FROM usage_logs WHERE request_id = 'req-plugin'`); s != "t-1" {
		t.Fatalf("plugin detail = %s", s)
	}
	// The claims inside plugin_detail changed nothing: the row is charged to
	// the user the gateway authenticated, for the cost the core computed.
	if s := f.scalar(`SELECT user_id::text || ' ' || billing_status || ' ' || total_cost::text FROM usage_logs WHERE request_id = 'req-plugin'`); s != strconv.FormatInt(f.user, 10)+" billed 0.70800000" {
		t.Fatalf("row = %s", s)
	}
	if s := f.scalar(`SELECT (anomalies->>'usage_extract') FROM usage_logs WHERE request_id = 'req-plugin'`); s != core.UsageExtractPlugin {
		t.Fatalf("usage_extract = %s", s)
	}

	// A row for an endpoint on the declarative rules carries neither.
	plain := f.record("req-rules", true)
	f.svc.process(ctx, []*core.UsageRecord{plain})
	if s := f.scalar(`SELECT plugin_detail::text FROM usage_logs WHERE request_id = 'req-rules'`); s != "{}" {
		t.Fatalf("plugin detail = %s", s)
	}
	if s := f.scalar(`SELECT anomalies::text FROM usage_logs WHERE request_id = 'req-rules'`); s != "{}" {
		t.Fatalf("clean row carries a marker: %s", s)
	}
}

// The fallback marker is the one that matters operationally: it says this row
// was billed from the declarative rules because the plugin could not answer.
// It must survive both the unbilled phase and a failed settlement.
func TestUsageExtractFallbackMarker(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	free := f.record("req-fb-free", false)
	free.UsageExtract = core.UsageExtractFallback
	f.ledger.fail.Store(1)
	billed := f.record("req-fb", true)
	billed.UsageExtract = core.UsageExtractFallback
	f.svc.process(ctx, []*core.UsageRecord{free, billed})

	if s := f.scalar(`SELECT billing_status || ' ' || (anomalies->>'usage_extract') FROM usage_logs WHERE request_id = 'req-fb-free'`); s != "free "+core.UsageExtractFallback {
		t.Fatalf("free row: %s", s)
	}
	if s := f.scalar(`SELECT billing_status || ' ' || (anomalies->>'usage_extract') FROM usage_logs WHERE request_id = 'req-fb'`); s != "failed "+core.UsageExtractFallback {
		t.Fatalf("after failure: %s", s)
	}
	if _, err := f.svc.RetryPending(ctx); err != nil {
		t.Fatal(err)
	}
	if s := f.scalar(`SELECT billing_status || ' ' || (anomalies->>'usage_extract') FROM usage_logs WHERE request_id = 'req-fb'`); s != "billed "+core.UsageExtractFallback {
		t.Fatalf("after retry: %s", s)
	}
}

// plugin_detail is jsonb, so a document that is not a JSON object would fail
// the whole batch insert and take unrelated records down with it. The gateway
// already guards this; the settler guards it again.
func TestPluginDetailRejectsNonObjects(t *testing.T) {
	for _, raw := range []string{"", "[1,2]", "{nope", `"text"`} {
		if got := string(pluginDetailJSON(core.RawJSON(raw))); got != "{}" {
			t.Errorf("pluginDetailJSON(%q) = %s", raw, got)
		}
	}
	if got := string(pluginDetailJSON(core.RawJSON(`{"a":1}`))); got != `{"a":1}` {
		t.Errorf("object dropped: %s", got)
	}
	big := core.RawJSON(`{"pad":"` + strings.Repeat("x", 64<<10) + `"}`)
	if got := string(pluginDetailJSON(big)); got != string(big) {
		t.Errorf("large document not stored whole: %d bytes", len(got))
	}
}

// With a cursor parameter the list pages by keyset: no total, page.next_cursor
// while there is more, and the pages neither overlap nor skip rows.
func TestUsageListKeyset(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	var recs []*core.UsageRecord
	for i := 0; i < 3; i++ {
		recs = append(recs, f.record(fmt.Sprintf("req-k%d", i), true))
	}
	f.svc.process(ctx, recs)

	seen := map[float64]bool{}
	cursor, pages := "", 0
	for {
		out := f.get(f.user, "/me/usage?page_size=2&cursor="+url.QueryEscape(cursor), 200)
		page := out["page"].(map[string]any)
		if _, ok := page["total"]; ok {
			t.Fatalf("keyset page has a total: %v", page)
		}
		for _, it := range out["data"].([]any) {
			id := it.(map[string]any)["id"].(float64)
			if seen[id] {
				t.Fatalf("row %v on two pages", id)
			}
			seen[id] = true
		}
		pages++
		next, _ := page["next_cursor"].(string)
		if page["has_more"] != (next != "") {
			t.Fatalf("has_more/next_cursor disagree: %v", page)
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if len(seen) != 3 || pages != 2 {
		t.Fatalf("saw %d rows on %d pages, want 3 on 2", len(seen), pages)
	}
	f.get(f.user, "/me/usage?cursor=not-a-cursor", 400)
	// Without a cursor the console's numbered pages and total are unchanged.
	if out := f.get(f.user, "/me/usage?page=2&page_size=2", 200); len(out["data"].([]any)) != 1 ||
		out["page"].(map[string]any)["total"].(float64) != 3 {
		t.Fatalf("numbered page 2: %v", out)
	}
}
