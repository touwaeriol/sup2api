package payment

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

const (
	// sweepTask is the name of the top-up sweep.
	sweepTask = "core.payment.sweep"
	// sweepBatch bounds the orders one sweep looks at per kind.
	sweepBatch = 200
	// queryAhead is how long before expiring a pending order's gateway is
	// asked about it: a lost notification is found while the order is open.
	queryAhead = time.Minute
)

// SweepTaskSpec is the top-up sweep: every minute, on one node.
func (s *Service) SweepTaskSpec() core.TaskSpec {
	return core.TaskSpec{
		Name:  sweepTask,
		Title: core.LocalizedText{"en": "Top-up orders", "zh": "充值订单"},
		Description: core.LocalizedText{
			"en": "Asks gateways about pending orders about to expire, expires them and releases their promo codes, and credits paid orders not credited yet.",
			"zh": "对快到期的待支付订单向网关查单，过期订单并释放优惠码名额，对已支付未入账的订单重试入账。"},
		Scope: core.TaskCluster, Every: time.Minute, Timeout: 5 * time.Minute, Handler: s.sweep,
	}
}

func (s *Service) sweep(ctx context.Context, _ core.TaskRun) (core.TaskResult, error) {
	expired, err1 := s.sweepPending(ctx)
	credited, err2 := s.sweepPaid(ctx)
	return core.TaskResult{Count: expired + credited}, errors.Join(err1, err2)
}

// sweepPending asks the gateway about each pending order about to expire,
// settles what it says was paid, and expires the rest once due. A gateway
// that cannot be asked (or fails to answer) does not keep an order open:
// a paid notification still credits it within the late window.
func (s *Service) sweepPending(ctx context.Context) (int, error) {
	var rows []model.PaymentOrder
	err := store.G(ctx, s.db.Gorm).Where("status = ? AND expires_at < ?", statusPending, s.now().Add(queryAhead)).
		Order("expires_at").Limit(sweepBatch).Find(&rows).Error
	if err != nil {
		return 0, err
	}
	n := 0
	for i := range rows {
		if ctx.Err() != nil {
			return n, ctx.Err()
		}
		if s.reconcile(ctx, &rows[i]) {
			n++
		}
	}
	return n, nil
}

// reconcile queries one pending order and expires it when due and not
// paid; true when it was handled.
func (s *Service) reconcile(ctx context.Context, o *model.PaymentOrder) bool {
	if gw, ok := s.gateway(o.Gateway); ok {
		res, err := gw.QueryPayment(ctx, core.PaymentQuery{OutTradeNo: o.OutTradeNo, SessionID: o.GatewaySessionID,
			MethodType: o.Method, MerchantRef: o.MerchantRef})
		switch {
		case err == nil && res.Status == core.PaymentPaid && res.OutTradeNo == o.OutTradeNo:
			return s.handleResult(ctx, o.Gateway, res, nil) == core.PaymentHandled
		case err != nil && !errors.Is(err, core.ErrPaymentQueryUnsupported):
			slog.WarnContext(ctx, "payment: query order", "order", o.OutTradeNo, "gateway", o.Gateway, "err", err)
		}
	}
	if o.ExpiresAt.After(s.now()) {
		return false
	}
	if err := s.expire(ctx, o.ID); err != nil {
		slog.ErrorContext(ctx, "payment: expire order", "order", o.OutTradeNo, "err", err)
		return false
	}
	return true
}

// expire expires a pending order and releases its promo use.
func (s *Service) expire(ctx context.Context, id int64) error {
	return s.db.Tx(ctx, func(tx *gorm.DB) error {
		ok, err := setStatus(ctx, tx, id, statusExpired, nil, statusPending)
		if err != nil || !ok {
			return err
		}
		return releaseUse(ctx, tx, id)
	})
}

// sweepPaid credits the paid orders whose lease is free or ran out.
func (s *Service) sweepPaid(ctx context.Context) (int, error) {
	var ids []int64
	err := store.G(ctx, s.db.Gorm).Model(&model.PaymentOrder{}).
		Where("status = ? AND (lease_until IS NULL OR lease_until < now())", statusPaid).
		Order("id").Limit(sweepBatch).Pluck("id", &ids).Error
	if err != nil {
		return 0, err
	}
	var errs []error
	for _, id := range ids {
		if err := s.credit(ctx, id); err != nil {
			errs = append(errs, err)
		}
	}
	return len(ids) - len(errs), errors.Join(errs...)
}
