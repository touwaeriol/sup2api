package payment

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// A query result the core rejects (here the amount) does not keep the
// order pending: it is recorded and the order expires.
func TestSweepExpiresAfterRejectedQuery(t *testing.T) {
	f := newFixture(t)
	f.configure()
	o := f.order(f.user("rejected"), "5")
	res := paid(o, "")
	res.Amount--
	f.gw.paid[o.OutTradeNo] = &res
	f.age(o, 31*time.Minute)
	_, err := f.svc.sweep(context.Background(), core.TaskRun{})
	require.NoError(t, err)
	require.Equal(t, statusExpired, *f.load(o.OutTradeNo).Status)
	require.Equal(t, int64(1), f.events(o.ID, eventAmountMismatch))
}

// A gateway that fails to answer is asked once per run, under the query
// timeout, and its orders still expire.
func TestSweepAsksAFailingGatewayOnce(t *testing.T) {
	f := newFixture(t)
	f.configure()
	uid := f.user("down")
	a, b := f.order(uid, "1"), f.order(uid, "2")
	f.age(a, 31*time.Minute)
	f.age(b, 31*time.Minute)
	f.gw.queryErr = errors.New("gateway down")
	_, err := f.svc.sweep(context.Background(), core.TaskRun{})
	require.NoError(t, err)
	require.Equal(t, 1, f.gw.queries)
	require.InDelta(t, queryTimeout.Seconds(), f.gw.deadline.Seconds(), 1)
	require.Equal(t, statusExpired, *f.load(a.OutTradeNo).Status)
	require.Equal(t, statusExpired, *f.load(b.OutTradeNo).Status)
}

// A gateway that hangs until the run's time is up does not keep the paid
// orders from being credited: crediting runs first.
func TestSweepCreditsBeforeAHangingQuery(t *testing.T) {
	f := newFixture(t)
	f.configure()
	uid := f.user("hang")
	credit := f.paidOrder(uid, "3")
	f.age(f.order(uid, "4"), 31*time.Minute)
	f.gw.block = true
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _ = f.svc.sweep(ctx, core.TaskRun{})
	require.Equal(t, statusCompleted, *f.load(credit.OutTradeNo).Status)
}

// A late payment whose promo code was deleted meanwhile credits the top-up
// only and says why.
func TestLatePaymentAfterPromoDeleted(t *testing.T) {
	f := newFixture(t)
	f.configure()
	id := f.promo("GONE", "5", "0", 0)
	uid := f.user("gone")
	o := f.order(uid, "10", "gone")
	f.age(o, 31*time.Minute)
	_, err := f.svc.sweep(context.Background(), core.TaskRun{})
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, f.call(f.admin, http.MethodDelete, "/promo-codes/"+itoa(id), "").Code)
	require.Equal(t, http.StatusOK, f.notify("epay", paid(o, "late-gone")))
	got := f.load(o.OutTradeNo)
	require.Equal(t, statusCompleted, *got.Status)
	require.True(t, got.Bonus.IsZero())
	require.Contains(t, got.FailReason, "promo code no longer applies")
	_, n := f.ledgerSum(uid, "promo:")
	require.Zero(t, n)
}
