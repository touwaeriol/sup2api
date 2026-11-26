package payment

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// paidOrder is an order the gateway reported paid, not credited yet.
func (f *fixture) paidOrder(uid int64, amount string) *model.PaymentOrder {
	f.t.Helper()
	o := f.order(uid, amount)
	_, err := f.svc.settle(context.Background(), "epay", ptr(paid(o, "")))
	require.NoError(f.t, err)
	return f.load(o.OutTradeNo)
}

func ptr[T any](v T) *T { return &v }

// Concurrent credits of one paid order wait on its row lock: one credits,
// the others find it completed, and the ledger holds one credit.
func TestConcurrentCreditsCreditOnce(t *testing.T) {
	f := newFixture(t)
	f.configure()
	uid := f.user("credits")
	o := f.paidOrder(uid, "10")
	errs := make([]error, 8)
	concurrently(len(errs), func(i int) { errs[i] = f.svc.credit(context.Background(), o.ID) })
	require.NoError(t, errors.Join(errs...))
	_, n := f.ledgerSum(uid, "topup:")
	require.EqualValues(t, 1, n)
	require.Equal(t, statusCompleted, *f.load(o.OutTradeNo).Status)
}

// A credit past the single limit fails the order with the reason; the
// sweep retries paid orders not credited.
func TestCreditRefusedAndRetried(t *testing.T) {
	f := newFixture(t)
	f.configure()
	uid := f.user("limits")
	o := f.paidOrder(uid, "10")
	require.NoError(t, f.db.Gorm.Model(&model.PaymentOrder{}).Where("id = ?", o.ID).Update("bonus", core.MaxCreditPerTx).Error)
	require.NoError(t, f.svc.credit(context.Background(), o.ID))
	got := f.load(o.OutTradeNo)
	require.Equal(t, statusFailed, *got.Status)
	require.Contains(t, got.FailReason, "single top-up limit")

	o2 := f.paidOrder(uid, "3")
	_, err := f.svc.sweep(context.Background(), core.TaskRun{})
	require.NoError(t, err)
	require.Equal(t, statusCompleted, *f.load(o2.OutTradeNo).Status)
}

// failingLedger fails the promo bonus row with a temporary error, after
// the top-up row was written in the same transaction.
type failingLedger struct{ core.CachingLedger }

func (l failingLedger) ApplyTx(ctx context.Context, tx *gorm.DB, ch core.LedgerChange) (*core.LedgerResult, error) {
	if ch.Kind == core.LedgerKindPromoBonus {
		return nil, errors.New("ledger down")
	}
	return l.CachingLedger.ApplyTx(ctx, tx, ch)
}

// A credit that fails midway rolls back the ledger rows it wrote: the
// order stays paid for the retry, which credits both rows once.
func TestFailedCreditRollsBackTheLedger(t *testing.T) {
	f := newFixture(t)
	f.configure()
	f.promo("MID", "5", "0", 0)
	uid := f.user("midway")
	o := f.order(uid, "10", "mid")
	_, err := f.svc.settle(context.Background(), "epay", ptr(paid(o, "")))
	require.NoError(t, err)
	ledger := f.svc.ledger
	f.svc.ledger = failingLedger{ledger}
	require.Error(t, f.svc.credit(context.Background(), o.ID))
	_, n := f.ledgerSum(uid, "topup:")
	require.Zero(t, n)
	require.Equal(t, statusPaid, *f.load(o.OutTradeNo).Status)

	f.svc.ledger = ledger
	require.NoError(t, f.svc.credit(context.Background(), o.ID))
	sum, _ := f.ledgerSum(uid, "")
	require.Equal(t, "15", sum.String())
}

// orderLedger fails the credits of one order, every time.
type orderLedger struct {
	core.CachingLedger
	no string
}

func (l orderLedger) ApplyTx(ctx context.Context, tx *gorm.DB, ch core.LedgerChange) (*core.LedgerResult, error) {
	if ch.RefID == l.no {
		return nil, errors.New("ledger down")
	}
	return l.CachingLedger.ApplyTx(ctx, tx, ch)
}

// The paid orders are credited in turns: one whose credit keeps failing
// does not keep the sweep from the next ones.
func TestCreditPaidGoesRound(t *testing.T) {
	f := newFixture(t)
	f.configure()
	uid := f.user("round")
	stuck, next := f.paidOrder(uid, "1"), f.paidOrder(uid, "2")
	f.svc.ledger = orderLedger{f.svc.ledger, stuck.OutTradeNo}
	for range 2 {
		_, _ = f.svc.creditPaid(context.Background(), 1)
	}
	require.Equal(t, statusCompleted, *f.load(next.OutTradeNo).Status)
	require.Equal(t, statusPaid, *f.load(stuck.OutTradeNo).Status)
}
