package payment

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/ratelimit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// An order snapshots the rate, discount and merchant, pays the discounted
// price in the currency's minor unit, and sends the gateway the site's
// addresses only.
func TestCreateOrder(t *testing.T) {
	f := newFixture(t)
	f.configure()
	uid := f.user("buyer")
	w := f.call(uid, http.MethodPost, "/me/topup/orders", `{"amount":100,"method":"alipay"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var created struct{ Data createdView }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	require.Regexp(t, `^s2a_[0-9a-f]{32}$`, created.Data.OutTradeNo)
	o := f.load(created.Data.OutTradeNo)
	require.Equal(t, "657", o.PayAmount.String()) // 100 × 7.3 × 0.9
	require.Equal(t, "0.9", o.DiscountRate.String())
	require.Equal(t, "7.3", o.Price.String())
	require.Equal(t, "1001", o.MerchantRef)
	require.Equal(t, "https://pay.test/"+o.OutTradeNo, o.PayURL)
	in := f.gw.created[0]
	require.Equal(t, "https://site.test/api/v1/payment/notify/epay", in.NotifyURL)
	require.Equal(t, "https://site.test/me/wallet", in.ReturnURL)
	require.NotContains(t, in.Subject, "&")
}

// Amounts outside the limits, without a positive minor unit, or of a
// method that is off are refused; a gateway that fails leaves the order
// failed.
func TestCreateOrderRefusals(t *testing.T) {
	f := newFixture(t)
	f.configure(func(v *settingsValues) {
		v.Methods = append(v.Methods, Method{Type: "card", Name: "Card", Gateway: core.PaymentGatewayStripe, Enabled: true})
	})
	uid := f.user("refused")
	for _, body := range []string{`{"amount":0,"method":"alipay"}`, `{"amount":-5,"method":"alipay"}`, `{"amount":"NaN","method":"alipay"}`,
		`{"amount":1e400,"method":"alipay"}`, `{"amount":10.001,"method":"alipay"}`, `{"amount":20000,"method":"alipay"}`,
		`{"amount":10,"method":"card"}`, `{"amount":10,"method":"none"}`} {
		require.Equal(t, http.StatusBadRequest, f.call(uid, http.MethodPost, "/me/topup/orders", body).Code, body)
	}
	f.gw.fail = true
	w := f.call(uid, http.MethodPost, "/me/topup/orders", `{"amount":10,"method":"alipay"}`)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	var o model.PaymentOrder
	require.NoError(t, f.db.Gorm.Where("user_id = ?", uid).Take(&o).Error)
	require.Equal(t, statusFailed, *o.Status)
}

// Concurrent orders of one user never pass the pending cap.
func TestPendingCapUnderConcurrency(t *testing.T) {
	f := newFixture(t)
	f.configure()
	uid := f.user("racer")
	var ok atomic.Int32
	concurrently(12, func(int) {
		if _, err := f.svc.placeOrder(context.Background(), uid, quoteInput{Amount: numInt(10), Method: "alipay"}, ""); err == nil {
			ok.Add(1)
		}
	})
	require.EqualValues(t, 3, ok.Load())
	var n int64
	require.NoError(t, f.db.Gorm.Model(&model.PaymentOrder{}).Where("user_id = ? AND status = ?", uid, statusPending).Count(&n).Error)
	require.EqualValues(t, 3, n)
}

// A user sees and cancels only their own orders: someone else's are not
// found; the list and the order carry where to pay while pending.
func TestOrdersAreTheirOwners(t *testing.T) {
	f := newFixture(t)
	f.configure()
	a, b := f.user("a"), f.user("b")
	o := f.order(b, "10")
	for _, c := range []struct{ method, path string }{{http.MethodGet, "/me/topup/orders/" + o.OutTradeNo}, {http.MethodPost, "/me/topup/orders/" + o.OutTradeNo + "/cancel"}} {
		require.Equal(t, http.StatusNotFound, f.call(a, c.method, c.path, "{}").Code, c.path)
	}
	require.Contains(t, f.call(a, http.MethodGet, "/me/topup/orders", "").Body.String(), `"total":0`)
	w := f.call(b, http.MethodGet, "/me/topup/orders?q="+o.OutTradeNo[4:12]+"&status=pending", "")
	require.Contains(t, w.Body.String(), `"pay_url":"https://pay.test/`+o.OutTradeNo)
	require.Equal(t, http.StatusOK, f.call(b, http.MethodPost, "/me/topup/orders/"+o.OutTradeNo+"/cancel", "{}").Code)
	require.Equal(t, http.StatusConflict, f.call(b, http.MethodPost, "/me/topup/orders/"+o.OutTradeNo+"/cancel", "{}").Code)
	require.NotContains(t, f.call(b, http.MethodGet, "/me/topup/orders/"+o.OutTradeNo, "").Body.String(), "pay_url")
}

// The preview prices with the discount and reports a promo code problem
// without failing.
func TestPreview(t *testing.T) {
	f := newFixture(t)
	f.configure()
	f.promo("WELCOME", "5", "50", 10)
	uid := f.user("pv")
	w := f.call(uid, http.MethodPost, "/me/topup/preview", `{"amount":20,"method":"alipay","promo_code":"welcome"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.JSONEq(t, `{"data":{"amount":20,"bonus":0,"discount_rate":1,"pay_amount":146,"original_pay_amount":146,"promo_error":"top up at least $50 to use this promo code"}}`, w.Body.String())
	w = f.call(uid, http.MethodPost, "/me/topup/preview", `{"amount":100,"method":"alipay","promo_code":"welcome"}`)
	require.JSONEq(t, `{"data":{"amount":100,"bonus":5,"discount_rate":0.9,"pay_amount":657,"original_pay_amount":730}}`, w.Body.String())
}

// Orders are limited per user per minute.
func TestOrderRateLimit(t *testing.T) {
	f := newFixture(t)
	f.configure(func(v *settingsValues) { v.MaxPendingOrders = 100 })
	f.svc.order = ratelimit.NewMinute(redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()}), "topup_order")
	uid := f.user("limited")
	codes := map[int]int{}
	for i := 0; i < orderRatePerMinute+2; i++ {
		codes[f.call(uid, http.MethodPost, "/me/topup/orders", `{"amount":10,"method":"alipay"}`).Code]++
	}
	require.Equal(t, map[int]int{http.StatusCreated: orderRatePerMinute, http.StatusTooManyRequests: 2}, codes)
}

// The administrator's list finds orders by email and shows the stats.
func TestAdminOrders(t *testing.T) {
	f := newFixture(t)
	f.configure()
	uid := f.user("listed")
	o := f.order(uid, "10")
	require.Equal(t, http.StatusOK, f.notify("epay", paid(o, "ev-admin")))
	w := f.call(f.admin, http.MethodGet, "/topup/orders?q=listed@&status=completed", "")
	require.Contains(t, w.Body.String(), `"user_email":"listed@x.test"`)
	require.False(t, strings.Contains(w.Body.String(), "pay_url"), "no pay address for the administrator")
	w = f.call(f.admin, http.MethodGet, "/topup/orders/stats", "")
	require.JSONEq(t, `{"data":{"today_amount":10,"today_count":1,"month_amount":10,"pending_count":0,"failed_count":0}}`, w.Body.String())
}

// Quotes are limited per user per minute.
func TestPreviewRateLimit(t *testing.T) {
	f := newFixture(t)
	f.configure()
	f.svc.quote = ratelimit.NewMinute(redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()}), "topup_quote")
	uid := f.user("quoting")
	codes := map[int]int{}
	for i := 0; i < quoteRatePerMinute+1; i++ {
		codes[f.call(uid, http.MethodPost, "/me/topup/preview", `{"amount":10,"method":"alipay"}`).Code]++
	}
	require.Equal(t, map[int]int{http.StatusOK: quoteRatePerMinute, http.StatusTooManyRequests: 1}, codes)
}

// A gateway's pay address must be an absolute https URL without
// credentials, or the order fails and gives back its promo use; a QR code
// alone is enough.
func TestCreatedPaymentIsChecked(t *testing.T) {
	f := newFixture(t)
	f.configure(func(v *settingsValues) { v.MaxPendingOrders = 10 })
	id := f.promo("SAFE", "1", "0", 0)
	uid := f.user("payurl")
	for _, bad := range []string{"http://pay.test/x", "https://user:pw@pay.test/x", "/relative", "javascript:alert(1)"} {
		f.gw.answer = &core.PaymentCreated{PayURL: bad, MerchantRef: "1001"}
		_, err := f.svc.placeOrder(context.Background(), uid, quoteInput{Amount: numInt(10), Method: "alipay", PromoCode: "safe"}, "")
		require.Equal(t, http.StatusServiceUnavailable, core.AsError(err).Status, bad)
		require.Equal(t, 0, f.promoRow(id).UsedCount, bad)
	}
	f.gw.answer = &core.PaymentCreated{QRCode: "weixin://wxpay/x", MerchantRef: "1001"}
	o := f.order(uid, "10")
	require.Equal(t, statusPending, *o.Status)
	require.Equal(t, "weixin://wxpay/x", o.QrCode)
}
