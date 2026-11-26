package payment

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// promo creates a promo code through the API.
func (f *fixture) promo(code, bonus, min string, maxUses int) int64 {
	f.t.Helper()
	body, _ := json.Marshal(map[string]any{"code": code, "bonus_amount": json.Number(bonus), "min_amount": json.Number(min), "max_uses": maxUses})
	w := f.call(f.admin, http.MethodPost, "/promo-codes", string(body))
	require.Equal(f.t, http.StatusCreated, w.Code, w.Body.String())
	var out struct{ Data promoView }
	require.NoError(f.t, json.Unmarshal(w.Body.Bytes(), &out))
	return out.Data.ID
}

func (f *fixture) promoRow(id int64) model.PromoCode {
	f.t.Helper()
	var p model.PromoCode
	require.NoError(f.t, f.db.Gorm.Where("id = ?", id).Take(&p).Error)
	return p
}

// The last use of a code goes to one of the users racing for it.
func TestPromoLastUseUnderConcurrency(t *testing.T) {
	f := newFixture(t)
	f.configure()
	id := f.promo("LAST", "5", "0", 1)
	users := make([]int64, 10)
	for i := range users {
		users[i] = f.user("racer" + string(rune('a'+i)))
	}
	var won atomic.Int32
	concurrently(len(users), func(i int) {
		in := quoteInput{Amount: numInt(10), Method: "alipay", PromoCode: "last"}
		if _, err := f.svc.placeOrder(context.Background(), users[i], in, ""); err == nil {
			won.Add(1)
		} else {
			require.Equal(t, "the promo code is invalid or used up", core.AsError(err).Message)
		}
	})
	require.EqualValues(t, 1, won.Load())
	require.Equal(t, 1, f.promoRow(id).UsedCount)
}

// Cancelling or expiring an order releases its use; paying it marks the
// use used and credits the bonus as its own ledger row.
func TestPromoReleaseAndUse(t *testing.T) {
	f := newFixture(t)
	f.configure()
	id := f.promo("TWICE", "5", "10", 2)
	uid := f.user("promo")
	o := f.order(uid, "10", "twice")
	require.Equal(t, 1, f.promoRow(id).UsedCount)
	_, err := f.svc.cancel(context.Background(), uid, o.OutTradeNo)
	require.NoError(t, err)
	require.Equal(t, 0, f.promoRow(id).UsedCount)

	o = f.order(uid, "10", "twice")
	f.age(o, 31*time.Minute)
	_, err = f.svc.sweep(context.Background(), core.TaskRun{})
	require.NoError(t, err)
	require.Equal(t, 0, f.promoRow(id).UsedCount)

	o = f.order(uid, "10", "twice")
	require.Equal(t, http.StatusOK, f.notify("epay", paid(o, "promo-paid")))
	bonus, n := f.ledgerSum(uid, "promo:")
	require.EqualValues(t, 1, n)
	require.Equal(t, "5", bonus.String())
	var use model.PromoCodeUsage
	require.NoError(t, f.db.Gorm.Where("order_id = ?", o.ID).Take(&use).Error)
	require.Equal(t, usageUsed, *use.Status)
	w := f.call(f.admin, http.MethodGet, "/promo-codes/"+itoa(id)+"/usages", "")
	require.Contains(t, w.Body.String(), `"status":"used"`)
	require.Contains(t, w.Body.String(), `"out_trade_no":"`+o.OutTradeNo+`"`)
	_, err = f.svc.placeOrder(context.Background(), uid, quoteInput{Amount: numInt(10), Method: "alipay", PromoCode: "twice"}, "")
	require.Error(t, err, "one use per user")
}

// A late payment whose code was taken meanwhile credits the top-up only
// and says why.
func TestPromoLatePaymentWithoutUse(t *testing.T) {
	f := newFixture(t)
	f.configure()
	f.promo("ONCE", "5", "0", 1)
	a, b := f.user("late-a"), f.user("late-b")
	o := f.order(a, "10", "once")
	f.age(o, 31*time.Minute)
	_, err := f.svc.sweep(context.Background(), core.TaskRun{})
	require.NoError(t, err)
	f.order(b, "10", "once")
	require.Equal(t, http.StatusOK, f.notify("epay", paid(o, "late-promo")))
	got := f.load(o.OutTradeNo)
	require.Equal(t, statusCompleted, *got.Status)
	require.True(t, got.Bonus.IsZero())
	require.Contains(t, got.FailReason, "promo code no longer applies")
	_, n := f.ledgerSum(a, "promo:")
	require.Zero(t, n)
	sum, _ := f.ledgerSum(a, "topup:")
	require.Equal(t, "10", sum.String())
}

// Below the code's minimum the order is refused with what to top up.
func TestPromoMinimum(t *testing.T) {
	f := newFixture(t)
	f.configure()
	f.promo("BIG", "5", "50", 0)
	_, err := f.svc.placeOrder(context.Background(), f.user("small"), quoteInput{Amount: numInt(10), Method: "alipay", PromoCode: "BIG"}, "")
	require.Equal(t, "top up at least $50 to use this promo code", core.AsError(err).Message)
}

// Promo codes are edited and deleted with an audit record.
func TestPromoAdmin(t *testing.T) {
	f := newFixture(t)
	id := f.promo("EDIT", "5", "0", 0)
	w := f.call(f.admin, http.MethodPatch, "/promo-codes/"+itoa(id), `{"status":"disabled","expires_at":null,"bonus_amount":7}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"bonus_amount":7`)
	require.Equal(t, http.StatusBadRequest, f.call(f.admin, http.MethodPatch, "/promo-codes/"+itoa(id), `{"bonus_amount":-1}`).Code)
	require.Equal(t, http.StatusConflict, f.call(f.admin, http.MethodPost, "/promo-codes", `{"code":"edit","bonus_amount":1}`).Code)
	require.Equal(t, http.StatusNoContent, f.call(f.admin, http.MethodDelete, "/promo-codes/"+itoa(id), "").Code)
	require.Equal(t, http.StatusNotFound, f.call(f.admin, http.MethodDelete, "/promo-codes/"+itoa(id), "").Code)
	var audited int64
	require.NoError(t, f.db.Gorm.Table("audit_logs").Where("action LIKE 'promo_code.%' AND user_id = ?", f.admin).Count(&audited).Error)
	require.EqualValues(t, 3, audited)
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }
