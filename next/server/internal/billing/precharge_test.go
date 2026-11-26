package billing

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

func TestPrechargeConcurrentAdmissionAndIdempotentRelease(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	uid := e.user("precharge@test.invalid")
	e.exec(`INSERT INTO user_balances(user_id,balance) VALUES($1,1)`, uid)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, id := range []string{"request-a", "request-b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- e.svc.Precharge(ctx, &core.UsageRecord{RequestID: id, UserID: uid, Price: &core.PriceRule{Expression: "flat(0.75)"}, RateMultiplier: decimal.NewFromInt(1)})
		}()
	}
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		} else if core.AsError(err).Code != core.ErrInsufficientBalance.Code {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("admitted %d requests", success)
	}
	var id string
	if err := e.db.SQL.QueryRowContext(ctx, `SELECT request_id FROM request_precharges`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Precharge(ctx, &core.UsageRecord{RequestID: id, UserID: uid, Price: &core.PriceRule{Expression: "flat(0.75)"}, RateMultiplier: decimal.NewFromInt(1)}); err != nil {
		t.Fatal(err)
	}
	var balance decimal.Decimal
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT balance FROM user_balances WHERE user_id=$1`, uid).Scan(&balance)
	if !balance.Equal(decimal.RequireFromString("0.25")) {
		t.Fatalf("balance %s", balance)
	}
	for range 2 {
		if err := e.db.Tx(ctx, func(tx *gorm.DB) error { _, err := e.svc.ReleasePrechargeTx(ctx, tx, uid, id); return err }); err != nil {
			t.Fatal(err)
		}
	}
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT balance FROM user_balances WHERE user_id=$1`, uid).Scan(&balance)
	if !balance.Equal(decimal.NewFromInt(1)) {
		t.Fatalf("refund balance %s", balance)
	}
}

func TestPreConsumeSettings(t *testing.T) {
	e := newEnv(t)
	uid := e.user("settings@test.invalid")
	out := data(e.mustCall(uid, 200, "GET", "/settings/billing", nil))
	if out["pre_consume_tokens"] != float64(500) {
		t.Fatalf("default: %v", out)
	}
	e.mustCall(uid, 200, "PUT", "/settings/billing", map[string]any{"pre_consume_tokens": 1200})
	e.mustCall(uid, 200, "PUT", "/settings/billing", map[string]any{"min_balance": "0.1"})
	n, err := e.svc.PreConsumeTokens(context.Background())
	if err != nil || n != 1200 {
		t.Fatalf("persisted %d %v", n, err)
	}
	for _, v := range []any{-1, 1.5, 100000001} {
		code, _ := e.call(uid, "PUT", "/settings/billing", map[string]any{"pre_consume_tokens": v})
		if code < 400 {
			t.Fatalf("accepted %v", v)
		}
	}
}

// One precharge used to be a dozen serial statements, half of them repeated
// by ApplyTx. The merged path must keep every value and every refusal the
// same: reservation, ledger row, balance, event, idempotent replay and the
// rolled-back balance row of an insufficient first charge.
func TestPrechargeReservationLedgerAndReplay(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	uid := e.user("precharge-path@test.invalid")
	record := func(id string) *core.UsageRecord {
		return &core.UsageRecord{RequestID: id, UserID: uid, Price: &core.PriceRule{Expression: "flat(0.75)"}, RateMultiplier: decimal.NewFromInt(1)}
	}
	var n int
	if err := e.svc.Precharge(ctx, record("r1")); core.AsError(err).Code != core.ErrInsufficientBalance.Code {
		t.Fatalf("no balance row: %v", err)
	}
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM user_balances WHERE user_id=$1`, uid).Scan(&n)
	if n != 0 {
		t.Fatal("refused first charge left the balance row it had created")
	}
	if _, err := e.svc.Apply(ctx, core.LedgerChange{UserID: uid, Amount: decimal.NewFromInt(1), Credit: true, Kind: core.LedgerKindAdminAdjust, IdempotencyKey: "seed"}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Precharge(ctx, record("r1")); err != nil {
		t.Fatal(err)
	}
	var balance, delta, after, amount decimal.Decimal
	var kind, refType, refID, key, note string
	if err := e.db.SQL.QueryRowContext(ctx, `SELECT balance FROM user_balances WHERE user_id=$1`, uid).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if err := e.db.SQL.QueryRowContext(ctx, `SELECT delta,balance_after,kind,ref_type,ref_id,idempotency_key,note FROM balance_ledger WHERE idempotency_key='precharge:r1'`).
		Scan(&delta, &after, &kind, &refType, &refID, &key, &note); err != nil {
		t.Fatal(err)
	}
	if err := e.db.SQL.QueryRowContext(ctx, `SELECT amount FROM request_precharges WHERE request_id='r1' AND user_id=$1`, uid).Scan(&amount); err != nil {
		t.Fatal(err)
	}
	if !balance.Equal(decimal.RequireFromString("0.25")) || !delta.Equal(decimal.RequireFromString("-0.75")) ||
		!after.Equal(decimal.RequireFromString("0.25")) || !amount.Equal(decimal.RequireFromString("0.75")) ||
		kind != core.LedgerKindUsage || refType != "precharge" || refID != "r1" || key != "precharge:r1" || note != "request precharge" {
		t.Fatalf("charged %s/%s/%s/%s %s %s %s %s %q", balance, delta, after, amount, kind, refType, refID, key, note)
	}
	// Replay: success, nothing charged, nothing emitted.
	if err := e.svc.Precharge(ctx, record("r1")); err != nil {
		t.Fatal(err)
	}
	var events, ledger int
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT balance FROM user_balances WHERE user_id=$1`, uid).Scan(&balance)
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM events WHERE type='balance.changed' AND payload->>'user_id'=$1`, strconv.FormatInt(uid, 10)).Scan(&events)
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM balance_ledger WHERE user_id=$1`, uid).Scan(&ledger)
	if !balance.Equal(decimal.RequireFromString("0.25")) || events != 2 || ledger != 2 {
		t.Fatalf("replay charged again: balance %s events %d ledger %d", balance, events, ledger)
	}
	// Insufficient: no reservation, no ledger row, balance untouched.
	if err := e.svc.Precharge(ctx, record("r2")); core.AsError(err).Code != core.ErrInsufficientBalance.Code {
		t.Fatalf("insufficient: %v", err)
	}
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM request_precharges WHERE request_id='r2'`).Scan(&n)
	if n != 0 {
		t.Fatal("insufficient precharge left a reservation")
	}
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM balance_ledger WHERE idempotency_key='precharge:r2'`).Scan(&n)
	if n != 0 {
		t.Fatal("insufficient precharge wrote a ledger row")
	}
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT balance FROM user_balances WHERE user_id=$1`, uid).Scan(&balance)
	if !balance.Equal(decimal.RequireFromString("0.25")) {
		t.Fatalf("insufficient precharge changed the balance to %s", balance)
	}
}
