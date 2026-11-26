package payment

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// The single and daily limits (core.MaxCreditPerTx, core.MaxCreditPerDay)
// are checked twice, both times under the user's advisory lock: when
// ordering, so a user is never charged for a credit the limits would
// refuse (there is no manual crediting), and when crediting, as the
// backstop. The daily limit is over the last 24 hours.

// creditedOrders: the user's orders credited within the last 24 hours.
func creditedOrders(userID int64, now time.Time) func(*gorm.DB) *gorm.DB {
	return func(q *gorm.DB) *gorm.DB {
		return q.Where("user_id = ? AND status = ? AND completed_at > ?", userID, statusCompleted, now.Add(-24*time.Hour))
	}
}

// committedOrders adds the orders that may still credit: paid ones, and the
// payable ones (pending, or expired or cancelled within the late window).
func committedOrders(userID int64, now time.Time) func(*gorm.DB) *gorm.DB {
	const committed = "user_id = ? AND (status = ? AND completed_at > ? OR status = ? OR " + payableCond + ")"
	return func(q *gorm.DB) *gorm.DB {
		args := append([]any{userID, statusCompleted, now.Add(-24 * time.Hour), statusPaid}, payableArgs(now)...)
		return q.Where(committed, args...)
	}
}

// breach is the limit a credit would pass.
type breach int

const (
	withinLimits breach = iota
	overSingle
	overDaily
)

// limitBreach tells which limit crediting total would pass, given what the
// orders day selects credit (the amount with the bonus).
func limitBreach(total decimal.Decimal, day *gorm.DB) (breach, error) {
	if total.GreaterThan(core.MaxCreditPerTx) {
		return overSingle, nil
	}
	var sum decimal.NullDecimal
	if err := day.Select("sum(amount + bonus)").Row().Scan(&sum); err != nil {
		return withinLimits, err
	}
	if sum.Decimal.Add(total).GreaterThan(core.MaxCreditPerDay) {
		return overDaily, nil
	}
	return withinLimits, nil
}

// dayOrders are the orders of the daily limit that day selects.
func dayOrders(ctx context.Context, tx *gorm.DB, day func(*gorm.DB) *gorm.DB) *gorm.DB {
	return store.G(ctx, tx).Model(&model.PaymentOrder{}).Scopes(day)
}

// checkOrderLimits refuses an order whose credit (amount and promo bonus)
// the limits would refuse once it is paid, counting the user's credited
// and committed orders.
func (s *Service) checkOrderLimits(ctx context.Context, tx *gorm.DB, userID int64, total decimal.Decimal) error {
	b, err := limitBreach(total, dayOrders(ctx, tx, committedOrders(userID, s.now())))
	switch b {
	case overSingle:
		return core.InvalidFields(core.FieldError{Field: "amount", Code: "invalid", Message: t(ctx,
			"the amount with the promo bonus exceeds the single top-up limit "+core.MaxCreditPerTx.String(),
			"充值金额加优惠码赠送超过单次入账上限 "+core.MaxCreditPerTx.String())})
	case overDaily:
		return core.ErrConflict.WithMessage(t(ctx, "this top-up would exceed the daily limit "+core.MaxCreditPerDay.String(),
			"本次充值将超过单日入账上限 "+core.MaxCreditPerDay.String()))
	}
	return err
}

// checkCreditLimits refuses crediting o past the single or the daily
// limit, counting the user's credited orders.
func (s *Service) checkCreditLimits(ctx context.Context, tx *gorm.DB, o *model.PaymentOrder) error {
	b, err := limitBreach(o.Amount.Add(o.Bonus), dayOrders(ctx, tx, creditedOrders(o.UserID, s.now())))
	switch b {
	case overSingle:
		return refusal{"credit exceeds the single top-up limit " + core.MaxCreditPerTx.String()}
	case overDaily:
		return refusal{"credit exceeds the daily top-up limit " + core.MaxCreditPerDay.String()}
	}
	return err
}
