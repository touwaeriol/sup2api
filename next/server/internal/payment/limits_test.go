package payment

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// completedOrder writes an order of uid credited amount just now.
func (f *fixture) completedOrder(uid int64, amount string) {
	f.t.Helper()
	done, now := statusCompleted, time.Now()
	require.NoError(f.t, f.db.Gorm.Create(&model.PaymentOrder{OutTradeNo: newTradeNo(), UserID: uid, Amount: dec(amount),
		PayAmount: dec("1"), Currency: "CNY", Price: dec("1"), Method: "alipay", Gateway: "epay",
		Status: &done, CompletedAt: &now, ExpiresAt: now}).Error)
}

func (f *fixture) configureMax() {
	f.configure(func(v *settingsValues) { v.MaxTopup, v.MaxPendingOrders = core.MaxCreditPerTx, 100 })
}

// An order whose amount with its promo bonus passes the single limit is
// refused before anyone pays.
func TestOrderSingleLimitCountsTheBonus(t *testing.T) {
	f := newFixture(t)
	f.configureMax()
	f.promo("TEN", "10", "0", 0)
	uid := f.user("single")
	_, err := f.svc.placeOrder(context.Background(), uid, quoteInput{Amount: dec("100000"), Method: "alipay", PromoCode: "TEN"}, "")
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
		_, err := f.svc.placeOrder(context.Background(), uid, quoteInput{Amount: decimal.NewFromInt(amount), Method: "alipay"}, "")
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

// An expired or cancelled order may still be paid within the late window,
// so it keeps counting against the daily limit; past the window it no
// longer does.
func TestOrderDailyLimitCountsLateOrders(t *testing.T) {
	f := newFixture(t)
	f.configureMax()
	uid := f.user("late-daily")
	f.completedOrder(uid, "900000")
	late := f.order(uid, "100000")
	_, err := f.svc.cancel(context.Background(), uid, late.OutTradeNo)
	require.NoError(t, err)
	_, err = f.svc.placeOrder(context.Background(), uid, quoteInput{Amount: dec("1"), Method: "alipay"}, "")
	require.Equal(t, http.StatusConflict, core.AsError(err).Status)
	f.age(late, lateWindow+time.Minute)
	f.order(uid, "1")
}
