package payment

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// The same notification twenty times at once credits once.
func TestConcurrentNotificationsCreditOnce(t *testing.T) {
	f := newFixture(t)
	f.configure()
	uid := f.user("twenty")
	o := f.order(uid, "100")
	codes := make([]int, 20)
	concurrently(20, func(i int) { codes[i] = f.notify("epay", paid(o, "ev-20")) })
	for _, c := range codes {
		require.Contains(t, []int{http.StatusOK, http.StatusServiceUnavailable}, c)
	}
	require.NoError(t, f.svc.credit(context.Background(), o.ID)) // a retried credit of a credited order
	sum, n := f.ledgerSum(uid, "topup:")
	require.EqualValues(t, 1, n)
	require.Equal(t, "100", sum.String())
	require.Equal(t, statusCompleted, *f.load(o.OutTradeNo).Status)
	var ok int64
	require.NoError(t, f.db.Gorm.Model(&model.PaymentEvent{}).Where("kind = ?", eventNotifyOK).Count(&ok).Error)
	require.EqualValues(t, 1, ok)
}

// A notification that does not match its order changes nothing and is
// recorded; one about no order of the site is handled.
func TestNotificationMismatches(t *testing.T) {
	f := newFixture(t)
	f.configure()
	uid := f.user("mismatch")
	o := f.order(uid, "10")
	for kind, edit := range map[string]func(*core.PaymentResult){
		eventAmountMismatch:   func(r *core.PaymentResult) { r.Amount-- },
		eventMerchantMismatch: func(r *core.PaymentResult) { r.MerchantRef = "9999" },
		eventMethodMismatch:   func(r *core.PaymentResult) { r.MethodType = "wxpay" },
		eventSessionMismatch:  func(r *core.PaymentResult) { r.SessionID = "sess_other" },
		eventTradeNoMissing:   func(r *core.PaymentResult) { r.TradeNo = "" },
	} {
		res := paid(o, "")
		edit(&res)
		require.Equal(t, http.StatusBadRequest, f.notify("epay", res), kind)
		require.Equal(t, int64(1), f.events(o.ID, kind), kind)
	}
	other := paid(o, "")
	other.Currency = "USD"
	require.Equal(t, http.StatusBadRequest, f.notify("epay", other))
	require.Equal(t, http.StatusBadRequest, f.notify("stripe", paid(o, "")), "an order of another gateway")
	require.Equal(t, int64(1), f.events(o.ID, eventGatewayMismatch))
	require.Equal(t, http.StatusBadRequest, f.notify("epay", paid(o, ""), "X-Bad-Sign", "1"))
	ghost := paid(o, "")
	ghost.OutTradeNo = "s2a_ghost"
	require.Equal(t, http.StatusOK, f.notify("epay", ghost))
	require.Equal(t, statusPending, *f.load(o.OutTradeNo).Status)
	_, n := f.ledgerSum(uid, "topup:")
	require.Zero(t, n)
}

// A plugin gateway that is gone answers retry while its orders may still
// be paid, and its notifications credit once it is back.
func TestPluginGatewayUnavailable(t *testing.T) {
	f := newFixture(t)
	plugin := &fakeGateway{merchant: "pm"}
	f.reg.set(core.PaymentGatewayBinding{Plugin: core.PluginInfo{Key: "alipay_direct"}, Gateway: core.PaymentGatewayInfo{ID: "alipay_direct.alipay"}, Client: plugin})
	f.configure(func(v *settingsValues) { v.Methods[0].Gateway = "alipay_direct.alipay" })
	uid := f.user("plugin")
	o := f.order(uid, "10")
	require.Equal(t, "pm", o.MerchantRef)
	f.reg.set()
	require.Equal(t, http.StatusServiceUnavailable, f.notify("alipay_direct.alipay", paid(o, "pe1")))
	require.Equal(t, http.StatusNotFound, f.notify("nobody.gw", paid(o, "pe1")))
	f.reg.set(core.PaymentGatewayBinding{Gateway: core.PaymentGatewayInfo{ID: "alipay_direct.alipay"}, Client: plugin})
	require.Equal(t, http.StatusOK, f.notify("alipay_direct.alipay", paid(o, "pe1")))
	_, n := f.ledgerSum(uid, "topup:")
	require.EqualValues(t, 1, n)
}

// Paying a second before expiry, after expiry and after cancelling each
// credits once; past the late window nothing is credited.
func TestLatePayments(t *testing.T) {
	f := newFixture(t)
	f.configure(func(v *settingsValues) { v.MaxPendingOrders = 4 })
	uid := f.user("late")
	inTime, expired, cancelled, tooLate := f.order(uid, "1"), f.order(uid, "2"), f.order(uid, "3"), f.order(uid, "1")
	f.age(inTime, 30*time.Minute-10*time.Second)
	f.age(expired, 31*time.Minute)
	f.age(tooLate, lateWindow+31*time.Minute)
	_, err := f.svc.sweep(context.Background(), core.TaskRun{})
	require.NoError(t, err)
	require.Equal(t, statusExpired, *f.load(expired.OutTradeNo).Status)
	f.age(tooLate, lateWindow+time.Minute)
	_, err = f.svc.cancel(context.Background(), uid, cancelled.OutTradeNo)
	require.NoError(t, err)
	for _, o := range []*model.PaymentOrder{inTime, expired, cancelled, tooLate} {
		for i := 0; i < 2; i++ {
			require.Equal(t, http.StatusOK, f.notify("epay", paid(o, "late-"+o.OutTradeNo)))
		}
	}
	sum, n := f.ledgerSum(uid, "topup:")
	require.EqualValues(t, 3, n)
	require.Equal(t, "6", sum.String())
	require.Equal(t, statusExpired, *f.load(tooLate.OutTradeNo).Status)
	require.Equal(t, int64(2), f.events(tooLate.ID, eventNotPayable))
}

// The sweep asks the gateway before expiring and credits what it says was
// paid.
func TestSweepQueriesBeforeExpiring(t *testing.T) {
	f := newFixture(t)
	f.configure()
	uid := f.user("query")
	o := f.order(uid, "5")
	res := paid(o, "")
	f.gw.paid[o.OutTradeNo] = &res
	f.age(o, 29*time.Minute+30*time.Second) // within queryAhead of expiring
	_, err := f.svc.sweep(context.Background(), core.TaskRun{})
	require.NoError(t, err)
	require.Equal(t, statusCompleted, *f.load(o.OutTradeNo).Status)
}

func (f *fixture) events(orderID int64, kind string) int64 {
	f.t.Helper()
	var n int64
	require.NoError(f.t, f.db.Gorm.Model(&model.PaymentEvent{}).Where("order_id = ? AND kind = ?", orderID, kind).Count(&n).Error)
	return n
}
