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

// The single and daily limits are checked twice, both times under the
// user's advisory lock: when ordering, so a user is never charged for a
// credit the limits would refuse (there is no manual crediting), and when
// crediting, as the backstop.
const (
	// dayCredited: the user's orders credited since the day began.
	dayCredited = "user_id = ? AND status = ? AND completed_at > ?"
	// dayCommitted adds the orders that may still credit: paid ones, and
	// pending ones not expired.
	dayCommitted = "user_id = ? AND (status = ? AND completed_at > ? OR status = ? OR status = ? AND expires_at > ?)"
)

// dayTotal is what the user's orders credit within the last 24 hours (the
// amount with the bonus); with open, also what their open orders would.
func dayTotal(ctx context.Context, tx *gorm.DB, userID int64, now time.Time, open bool) (decimal.Decimal, error) {
	db := store.G(ctx, tx).Model(&model.PaymentOrder{}).Select("sum(amount + bonus)")
	since := now.Add(-24 * time.Hour)
	if open {
		db = db.Where(dayCommitted, userID, statusCompleted, since, statusPaid, statusPending, now)
	} else {
		db = db.Where(dayCredited, userID, statusCompleted, since)
	}
	var sum decimal.NullDecimal
	err := db.Row().Scan(&sum)
	return sum.Decimal, err
}

// checkOrderLimits refuses an order whose credit (amount and promo bonus)
// the limits would refuse once it is paid.
func (s *Service) checkOrderLimits(ctx context.Context, tx *gorm.DB, userID int64, amount decimal.Decimal, promo *model.PromoCode) error {
	total := amount
	if promo != nil {
		total = total.Add(promo.BonusAmount)
	}
	if total.GreaterThan(maxCreditPerTx) {
		return core.InvalidFields(core.FieldError{Field: "amount", Code: "invalid", Message: t(ctx,
			"the amount with the promo bonus exceeds the single top-up limit "+maxCreditPerTx.String(),
			"充值金额加优惠码赠送超过单次入账上限 "+maxCreditPerTx.String())})
	}
	day, err := dayTotal(ctx, tx, userID, s.now(), true)
	if err == nil && day.Add(total).GreaterThan(maxCreditPerDay) {
		err = core.ErrConflict.WithMessage(t(ctx, "this top-up would exceed the daily limit "+maxCreditPerDay.String(),
			"本次充值将超过单日入账上限 "+maxCreditPerDay.String()))
	}
	return err
}

// checkLimits refuses crediting o past the single or the daily limit.
func (s *Service) checkLimits(ctx context.Context, tx *gorm.DB, o *model.PaymentOrder) error {
	total := o.Amount.Add(o.Bonus)
	if total.GreaterThan(maxCreditPerTx) {
		return refusal{"credit exceeds the single top-up limit " + maxCreditPerTx.String()}
	}
	day, err := dayTotal(ctx, tx, o.UserID, s.now(), false)
	if err != nil {
		return err
	}
	if day.Add(total).GreaterThan(maxCreditPerDay) {
		return refusal{"credit exceeds the daily top-up limit " + maxCreditPerDay.String()}
	}
	return nil
}
