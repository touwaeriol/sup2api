package payment

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/payment/stripe"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/ratelimit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// A repeat of the order's payment is a duplicate; another payment of a
// paid order is recorded as a second payment; neither credits again.
func TestDuplicateAndSecondPayment(t *testing.T) {
	f := newFixture(t)
	f.configure()
	uid := f.user("twice")
	o := f.order(uid, "10")
	res := paid(o, "ev-1")
	require.Equal(t, http.StatusOK, f.notify("epay", res))
	require.Equal(t, http.StatusOK, f.notify("epay", res))
	second := paid(o, "ev-2")
	second.TradeNo = "T-second"
	require.Equal(t, http.StatusOK, f.notify("epay", second))
	require.Equal(t, int64(1), f.events(o.ID, eventDuplicate))
	require.Equal(t, int64(1), f.events(o.ID, eventSecondPayment))
	_, n := f.ledgerSum(uid, "topup:")
	require.EqualValues(t, 1, n)
}

// An event id a paid order already used is a duplicate for any order of
// the gateway; a transaction number another order of the gateway holds is
// refused, while another gateway's equal number is not.
func TestEventAndTradeNoReuse(t *testing.T) {
	f := newFixture(t)
	f.configure(withCard)
	uid := f.user("reuse")
	a, b, card := f.order(uid, "1"), f.order(uid, "2"), f.place(uid, quoteInput{Amount: dec("3"), Method: "card"})
	require.Equal(t, http.StatusOK, f.notify("epay", paid(a, "ev-a")))
	sameEvent := paid(b, "ev-a")
	require.Equal(t, http.StatusOK, f.notify("epay", sameEvent))
	require.Equal(t, int64(1), f.events(b.ID, eventDuplicate))
	sameTrade := paid(b, "ev-b")
	sameTrade.TradeNo = "T" + a.OutTradeNo
	require.Equal(t, http.StatusBadRequest, f.notify("epay", sameTrade))
	require.Equal(t, int64(1), f.events(b.ID, eventTradeNoReused))
	require.Equal(t, statusPending, *f.load(b.OutTradeNo).Status)
	other := paid(card, "ev-card")
	other.TradeNo = "T" + a.OutTradeNo
	require.Equal(t, http.StatusOK, f.notify("stripe", other))
	require.Equal(t, statusCompleted, *f.load(card.OutTradeNo).Status)
}

func withCard(v *settingsValues) {
	v.Methods = append(v.Methods, Method{Type: "card", Name: "Card", Gateway: stripe.ID, Enabled: true})
	v.Stripe = StripeSettings{Enabled: true, SecretKey: "sk_test_x", WebhookSecret: "whsec_x"}
}

// A body past 1 MiB is rejected; every answer is inert and plain.
func TestNotifyBodyLimitAndHeaders(t *testing.T) {
	f := newFixture(t)
	f.configure()
	w := f.notifyRaw("epay", strings.Repeat("x", notifyBodyLimit+1))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "default-src 'none'; sandbox", w.Header().Get("Content-Security-Policy"))
	w = f.notifyRaw("nobody.gw", "{}")
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
}

// A notification naming no order (a session without one) is handled and
// recorded; a gateway reporting nothing at all gets a retry, not a panic.
func TestEmptyOrderNumberAndNoResult(t *testing.T) {
	f := newFixture(t)
	f.configure()
	o := f.order(f.user("empty"), "10")
	res := paid(o, "ev-empty")
	res.OutTradeNo = ""
	require.Equal(t, http.StatusOK, f.notify("epay", res))
	var n int64
	require.NoError(t, f.db.Gorm.Model(&model.PaymentEvent{}).Where("kind = ? AND order_id IS NULL", eventOrderNotFound).Count(&n).Error)
	require.EqualValues(t, 1, n)
	f.gw.nilResult = true
	require.Equal(t, http.StatusServiceUnavailable, f.notify("epay", paid(o, "")))
	require.Equal(t, statusPending, *f.load(o.OutTradeNo).Status)
}

// References too long for their columns and statuses the core does not
// know are rejected and recorded, not retried forever.
func TestOversizedAndUnknownResults(t *testing.T) {
	f := newFixture(t)
	f.configure()
	o := f.order(f.user("oversized"), "10")
	for _, edit := range []func(*core.PaymentResult){
		func(r *core.PaymentResult) { r.TradeNo = strings.Repeat("t", maxGatewayRef+1) },
		func(r *core.PaymentResult) { r.EventID = strings.Repeat("e", maxGatewayRef+1) },
		func(r *core.PaymentResult) { r.SessionID = strings.Repeat("s", maxGatewayRef+1) },
		func(r *core.PaymentResult) { r.MerchantRef = strings.Repeat("m", maxGatewayRef+1) },
	} {
		res := paid(o, "")
		edit(&res)
		require.Equal(t, http.StatusBadRequest, f.notify("epay", res))
	}
	require.Equal(t, int64(4), f.events(o.ID, eventFieldTooLong))
	odd := paid(o, "")
	odd.Status = core.PaymentStatus(strings.Repeat("weird", 20))
	require.Equal(t, http.StatusBadRequest, f.notify("epay", odd))
	require.Equal(t, int64(1), f.events(o.ID, eventStatusUnknown))
	require.Equal(t, statusPending, *f.load(o.OutTradeNo).Status)
}

// A failed verification keeps the gateway's reason, cut to 200 bytes
// without splitting a character.
func TestSignatureInvalidKeepsReason(t *testing.T) {
	f := newFixture(t)
	f.configure()
	o := f.order(f.user("badsig"), "10")
	require.Equal(t, http.StatusBadRequest, f.notify("epay", paid(o, ""), "X-Bad-Sign", "pid mismatch "+strings.Repeat("签", 300)))
	var ev model.PaymentEvent
	require.NoError(t, f.db.Gorm.Where("kind = ?", eventSignatureInvalid).Take(&ev).Error)
	var detail map[string]string
	require.NoError(t, json.Unmarshal(ev.Detail, &detail))
	require.Contains(t, detail["error"], "pid mismatch")
	require.True(t, utf8.ValidString(detail["error"]))
	require.LessOrEqual(t, len(detail["error"]), maxReasonBytes)
	require.Greater(t, len(detail["error"]), maxReasonBytes-utf8.UTFMax)
	require.False(t, ev.Verified)
}

// A client address is limited on the public route, and records only a
// few events tied to no order.
func TestNotifyRateLimit(t *testing.T) {
	f := newFixture(t)
	f.configure()
	f.svc.notifyLimit = ratelimit.NewMinute(redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()}), "topup_notify")
	codes := map[int]int{}
	for i := 0; i < notifyRatePerMinute+1; i++ {
		codes[f.notifyRaw("epay", "{}", "X-Bad-Sign", "flood").Code]++
	}
	require.Equal(t, map[int]int{http.StatusBadRequest: notifyRatePerMinute, http.StatusTooManyRequests: 1}, codes)
	var n int64
	require.NoError(t, f.db.Gorm.Model(&model.PaymentEvent{}).Where("kind = ?", eventSignatureInvalid).Count(&n).Error)
	require.EqualValues(t, unverifiedEventsPerMinute, n)
}

// A transaction number that another order of the gateway takes while this
// notification is being settled hits the unique constraint: the
// notification is rejected, and its TRADE_NO_REUSED event still names its
// order.
func TestTradeNoReusedRaceKeepsTheOrder(t *testing.T) {
	f := newFixture(t)
	f.configure()
	uid := f.user("race")
	a, b := f.order(uid, "1"), f.order(uid, "2")
	taken, release := make(chan struct{}), make(chan struct{})
	go func() {
		_ = f.db.Tx(context.Background(), func(tx *gorm.DB) error {
			err := tx.Model(&model.PaymentOrder{}).Where("id = ?", a.ID).Update("trade_no", "T-race").Error
			close(taken)
			<-release
			return err
		})
	}()
	<-taken
	res := paid(b, "")
	res.TradeNo = "T-race"
	code := make(chan int)
	go func() { code <- f.notify("epay", res) }()
	require.Eventually(t, func() bool {
		var waiting int64
		f.db.Gorm.Table("pg_stat_activity").Where("wait_event_type = ? AND datname = current_database()", "Lock").Count(&waiting)
		return waiting > 0
	}, 10*time.Second, 10*time.Millisecond, "the notification waits on the uncommitted number")
	close(release)
	require.Equal(t, http.StatusBadRequest, <-code)
	require.Equal(t, int64(1), f.events(b.ID, eventTradeNoReused))
	require.Equal(t, statusPending, *f.load(b.OutTradeNo).Status)
}
