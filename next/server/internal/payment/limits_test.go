package payment

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// completedOrder writes an order of uid credited amount just now.
func (f *fixture) completedOrder(uid int64, amount string) {
	f.t.Helper()
	no, err := newTradeNo()
	require.NoError(f.t, err)
	done, now := statusCompleted, time.Now()
	require.NoError(f.t, f.db.Gorm.Create(&model.PaymentOrder{OutTradeNo: no, UserID: uid, Amount: decimalOf(amount),
		PayAmount: decimalOf("1"), Currency: "CNY", Price: decimalOf("1"), Method: "alipay", Gateway: "epay",
		Status: &done, CompletedAt: &now, ExpiresAt: now}).Error)
}

func (f *fixture) configureMax() {
	f.configure(func(v *settingsValues) { v.MaxTopup, v.MaxPendingOrders = num(maxCreditPerTx), 100 })
}

// An order whose amount with its promo bonus passes the single limit is
// refused before anyone pays.
func TestOrderSingleLimitCountsTheBonus(t *testing.T) {
	f := newFixture(t)
	f.configureMax()
	f.promo("TEN", "10", "0", 0)
	uid := f.user("single")
	_, err := f.svc.placeOrder(context.Background(), uid, quoteInput{Amount: numInt(100000), Method: "alipay", PromoCode: "TEN"}, "")
	require.Equal(t, []string{"amount"}, fieldsOf(err))
	f.order(uid, "100000")
}

// The daily limit counts what was credited today and what open orders
// would credit; it is checked before anyone pays.
func TestOrderDailyLimit(t *testing.T) {
	f := newFixture(t)
	f.configureMax()
	uid := f.user("daily")
	f.completedOrder(uid, "900000")
	f.order(uid, "50000")
	for _, amount := range []int64{50001, 100000} {
		_, err := f.svc.placeOrder(context.Background(), uid, quoteInput{Amount: numInt(amount), Method: "alipay"}, "")
		require.Equal(t, http.StatusConflict, core.AsError(err).Status, amount)
	}
	f.order(uid, "50000")
	f.completedOrder(f.user("other"), "999999") // another user's credits do not count
}

// The credit's own check stays as the backstop: past the daily limit the
// order fails, gives back its promo use, and keeps the reason it had.
func TestCreditRefusalReleasesPromo(t *testing.T) {
	f := newFixture(t)
	f.configureMax()
	id := f.promo("BACK", "5", "0", 0)
	uid := f.user("backstop")
	o := f.order(uid, "100", "back")
	_, err := f.svc.settle(context.Background(), "epay", ptr(paid(o, "")))
	require.NoError(t, err)
	require.NoError(t, f.db.Gorm.Model(&model.PaymentOrder{}).Where("id = ?", o.ID).Update("fail_reason", "earlier").Error)
	f.completedOrder(uid, "999990")
	require.NoError(t, f.svc.credit(context.Background(), o.ID))
	got := f.load(o.OutTradeNo)
	require.Equal(t, statusFailed, *got.Status)
	require.Equal(t, "earlier; credit exceeds the daily top-up limit 1000000", got.FailReason)
	require.Equal(t, 0, f.promoRow(id).UsedCount)
	var uses int64
	require.NoError(t, f.db.Gorm.Model(&model.PromoCodeUsage{}).Where("order_id = ?", o.ID).Count(&uses).Error)
	require.Zero(t, uses)
}

// stealingLedger takes the order's lease away inside the crediting
// transaction, right after the ledger wrote: the holder lost its lease
// while crediting.
type stealingLedger struct {
	Ledger
	orderID int64
}

func (l stealingLedger) ApplyTx(ctx context.Context, tx *gorm.DB, ch core.LedgerChange) (*core.LedgerResult, error) {
	res, err := l.Ledger.ApplyTx(ctx, tx, ch)
	if err == nil {
		err = tx.Model(&model.PaymentOrder{}).Where("id = ?", l.orderID).Update("lease_token", "stolen").Error
	}
	return res, err
}

// A holder that loses its lease midway rolls back the ledger rows too.
func TestLostLeaseMidCreditRollsBackTheLedger(t *testing.T) {
	f := newFixture(t)
	f.configure()
	uid := f.user("midway")
	o := f.paidOrder(uid, "10")
	f.svc.ledger = stealingLedger{Ledger: f.svc.ledger, orderID: o.ID}
	require.ErrorIs(t, f.svc.credit(context.Background(), o.ID), errLostRace)
	_, n := f.ledgerSum(uid, "topup:")
	require.Zero(t, n)
	require.Equal(t, statusPaid, *f.load(o.OutTradeNo).Status)
}
