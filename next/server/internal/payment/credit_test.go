package payment

import (
	"context"
	"testing"
	"time"

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

// A worker whose lease ran out is taken over: the new holder credits, the
// old one finishes nothing, and the ledger holds one credit.
func TestLeaseTakeoverCreditsOnce(t *testing.T) {
	f := newFixture(t)
	f.configure()
	uid := f.user("lease")
	o := f.paidOrder(uid, "10")
	ctx := context.Background()
	stale, err := f.svc.claimLease(ctx, o.ID)
	require.NoError(t, err)
	require.NotEmpty(t, stale)
	busy, err := f.svc.claimLease(ctx, o.ID)
	require.NoError(t, err)
	require.Empty(t, busy, "a held lease is not taken")

	require.NoError(t, f.db.Gorm.Model(&model.PaymentOrder{}).Where("id = ?", o.ID).Update("lease_until", time.Now().Add(-time.Second)).Error)
	fresh, err := f.svc.claimLease(ctx, o.ID)
	require.NoError(t, err)
	require.NotEmpty(t, fresh)
	_, _, err = f.svc.creditTx(ctx, o.ID, stale)
	require.NoError(t, err, "the old holder finds no order under its lease")
	require.Equal(t, statusPaid, *f.load(o.OutTradeNo).Status)
	_, _, err = f.svc.creditTx(ctx, o.ID, fresh)
	require.NoError(t, err)
	_, _, err = f.svc.creditTx(ctx, o.ID, stale)
	require.NoError(t, err)
	_, n := f.ledgerSum(uid, "topup:")
	require.EqualValues(t, 1, n)
	require.Equal(t, statusCompleted, *f.load(o.OutTradeNo).Status)
}

// The old holder that lost its lease while crediting rolls back.
func TestLostLeaseRollsBack(t *testing.T) {
	f := newFixture(t)
	f.configure()
	uid := f.user("lost")
	o := f.paidOrder(uid, "10")
	ctx := context.Background()
	token, err := f.svc.claimLease(ctx, o.ID)
	require.NoError(t, err)
	err = f.db.Tx(ctx, func(tx *gorm.DB) error {
		require.NoError(t, tx.Model(&model.PaymentOrder{}).Where("id = ?", o.ID).Update("lease_token", "someone-else").Error)
		return finishLease(ctx, tx, o.ID, token, statusCompleted, map[string]any{})
	})
	require.ErrorIs(t, err, errLostRace)
}

// A credit past the single limit fails the order with the reason; the
// sweep retries paid orders not credited.
func TestCreditRefusedAndRetried(t *testing.T) {
	f := newFixture(t)
	f.configure()
	uid := f.user("limits")
	o := f.paidOrder(uid, "10")
	require.NoError(t, f.db.Gorm.Model(&model.PaymentOrder{}).Where("id = ?", o.ID).Update("bonus", maxCreditPerTx).Error)
	require.NoError(t, f.svc.credit(context.Background(), o.ID))
	got := f.load(o.OutTradeNo)
	require.Equal(t, statusFailed, *got.Status)
	require.Contains(t, got.FailReason, "single top-up limit")

	o2 := f.paidOrder(uid, "3")
	_, err := f.svc.sweep(context.Background(), core.TaskRun{})
	require.NoError(t, err)
	require.Equal(t, statusCompleted, *f.load(o2.OutTradeNo).Status)
}
