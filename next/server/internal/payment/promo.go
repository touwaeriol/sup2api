package payment

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// Promo codes credit a bonus with a paid top-up, never without one. A code
// is reserved by its order (a usage row and one more used_count, under the
// code's row lock), turns used when the order is credited, and is released
// when the order expires or is cancelled. Each user uses a code once.

const (
	usageReserved = "reserved"
	usageUsed     = "used"
	promoActive   = "active"
	promoDisabled = "disabled"
)

// promoCheck is a code that applies (code) or why it does not (refusal).
type promoCheck struct {
	code    *model.PromoCode
	refusal *core.Error
}

func errPromoInvalid(ctx context.Context) *core.Error {
	return core.ErrInvalidArgument.WithMessage(t(ctx, "the promo code is invalid or used up", "优惠码无效或已用完"))
}

func errPromoMinimum(ctx context.Context, min decimal.Decimal) *core.Error {
	return core.ErrInvalidArgument.WithMessage(t(ctx, "top up at least $"+min.String()+" to use this promo code",
		"充值满 $"+min.String()+" 才能使用这个优惠码"))
}

// checkPromo tells whether code applies to a top-up of amount by userID
// (the preview). Unknown, disabled, expired, used up and already used by
// the user all get the same refusal; only the minimum top-up has its own.
func checkPromo(ctx context.Context, q *gorm.DB, code string, userID int64, amount decimal.Decimal, now time.Time) (promoCheck, error) {
	return judgePromo(ctx, q, store.G(ctx, q).Where("code = ?", code), userID, amount, now)
}

// lockCheckPromo is checkPromo with the code row locked until tx ends, so
// the last use goes to one order only.
func lockCheckPromo(ctx context.Context, tx *gorm.DB, code string, userID int64, amount decimal.Decimal, now time.Time) (promoCheck, error) {
	if err := store.RequireTx(tx); err != nil {
		return promoCheck{}, err
	}
	return judgePromo(ctx, tx, store.G(ctx, tx).Clauses(store.ForUpdate()).Where("code = ?", code), userID, amount, now)
}

// judgePromo reads the code find selects and judges it for userID.
func judgePromo(ctx context.Context, q, find *gorm.DB, userID int64, amount decimal.Decimal, now time.Time) (promoCheck, error) {
	var p model.PromoCode
	if err := find.Take(&p).Error; store.IsNoRows(err) {
		return promoCheck{refusal: errPromoInvalid(ctx)}, nil
	} else if err != nil {
		return promoCheck{}, err
	}
	used, err := usedBy(ctx, q, p.ID, userID)
	if err != nil || used || !promoOpen(&p, now) {
		return promoCheck{refusal: errPromoInvalid(ctx)}, err
	}
	if amount.LessThan(p.MinAmount) {
		return promoCheck{refusal: errPromoMinimum(ctx, p.MinAmount)}, nil
	}
	return promoCheck{code: &p}, nil
}

// promoOpen: enabled, not expired, a use left.
func promoOpen(p *model.PromoCode, now time.Time) bool {
	return (p.Status == nil || *p.Status == promoActive) && (p.ExpiresAt == nil || p.ExpiresAt.After(now)) &&
		(p.MaxUses == 0 || p.UsedCount < p.MaxUses)
}

// usedBy reports whether userID holds a use of the code (reserved or used).
func usedBy(ctx context.Context, q *gorm.DB, promoID, userID int64) (bool, error) {
	db := store.G(ctx, q)
	return gormpg.Exists(db, db.Model(&model.PromoCodeUsage{}).Where("promo_code_id = ? AND user_id = ?", promoID, userID))
}

// promoOutcome is the bonus of a check and its refusal's message.
func promoOutcome(ctx context.Context, c promoCheck) (Num, string) {
	if c.refusal != nil {
		return numInt(0), c.refusal.Message
	}
	return num(c.code.BonusAmount), ""
}

// reserveUse takes one use of promo for userID's order (the code row is
// locked by lockCheckPromo or lockPromo).
func reserveUse(ctx context.Context, tx *gorm.DB, p *model.PromoCode, userID, orderID int64) error {
	db := store.G(ctx, tx)
	use := model.PromoCodeUsage{PromoCodeID: p.ID, UserID: userID, OrderID: &orderID, BonusAmount: p.BonusAmount}
	if err := db.Create(&use).Error; err != nil {
		return err
	}
	return db.Model(&model.PromoCode{}).Where("id = ?", p.ID).
		Updates(map[string]any{"used_count": gorm.Expr("used_count + 1"), "updated_at": clause.Expr{SQL: "now()"}}).Error
}

// releaseUse gives back the use the order holds, if it still holds one
// reserved: the order expired or was cancelled.
func releaseUse(ctx context.Context, tx *gorm.DB, orderID int64) error {
	db := store.G(ctx, tx)
	var use model.PromoCodeUsage
	err := db.Where("order_id = ? AND status = ?", orderID, usageReserved).Take(&use).Error
	if store.IsNoRows(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := db.Delete(&model.PromoCodeUsage{}, use.ID).Error; err != nil {
		return err
	}
	return db.Model(&model.PromoCode{}).Where("id = ? AND used_count > 0", use.PromoCodeID).
		Updates(map[string]any{"used_count": gorm.Expr("used_count - 1"), "updated_at": clause.Expr{SQL: "now()"}}).Error
}

// markUsed turns the order's use into a credited one.
func markUsed(ctx context.Context, tx *gorm.DB, orderID int64) error {
	return store.G(ctx, tx).Model(&model.PromoCodeUsage{}).Where("order_id = ? AND status = ?", orderID, usageReserved).
		Updates(map[string]any{"status": usageUsed, "used_at": clause.Expr{SQL: "now()"}}).Error
}

// reacquireUse makes a late-paid order hold its promo code again, its use
// having been released when it expired or was cancelled. It reports
// whether the order keeps its bonus: false when the code no longer applies
// (deleted, disabled, expired, used up, or the user used it since).
func reacquireUse(ctx context.Context, tx *gorm.DB, o *model.PaymentOrder, now time.Time) (bool, error) {
	if !o.Bonus.IsPositive() {
		return true, nil
	}
	if o.PromoCodeID == nil {
		return false, nil // the code was deleted (promo_code_id SET NULL)
	}
	db := store.G(ctx, tx)
	held, err := gormpg.Exists(db, db.Model(&model.PromoCodeUsage{}).Where("order_id = ?", o.ID))
	if err != nil || held {
		return held, err
	}
	p, err := lockPromo(ctx, tx, *o.PromoCodeID)
	if err != nil || p == nil {
		return false, err
	}
	used, err := usedBy(ctx, tx, p.ID, o.UserID)
	if err != nil || used || !promoOpen(p, now) {
		return false, err
	}
	p.BonusAmount = o.Bonus
	return true, reserveUse(ctx, tx, p, o.UserID, o.ID)
}

// lockPromo reads promo code id under a row lock; nil when it is gone.
func lockPromo(ctx context.Context, tx *gorm.DB, id int64) (*model.PromoCode, error) {
	if err := store.RequireTx(tx); err != nil {
		return nil, err
	}
	var p model.PromoCode
	err := store.G(ctx, tx).Clauses(store.ForUpdate()).Where("id = ?", id).Take(&p).Error
	if store.IsNoRows(err) {
		return nil, nil
	}
	return &p, err
}
