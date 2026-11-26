package payment

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

var promoCodePattern = regexp.MustCompile(`^[A-Z0-9_-]{3,64}$`)

// promoView is a promo code in the API (the console's PromoCode): status is
// the stored switch, state what the code is now (promoState).
type promoView struct {
	ID          int64           `json:"id"`
	Code        string          `json:"code"`
	BonusAmount decimal.Decimal `json:"bonus_amount"`
	MinAmount   decimal.Decimal `json:"min_amount"`
	MaxUses     int             `json:"max_uses"`
	UsedCount   int             `json:"used_count"`
	Status      string          `json:"status"`
	State       string          `json:"state"`
	ExpiresAt   *time.Time      `json:"expires_at,omitempty"`
	Notes       string          `json:"notes,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

func promoViewOf(p *model.PromoCode, now time.Time) promoView {
	v := promoView{ID: p.ID, Code: p.Code, BonusAmount: p.BonusAmount, MinAmount: p.MinAmount, MaxUses: p.MaxUses,
		UsedCount: p.UsedCount, Status: promoActive, State: promoState(p, now), ExpiresAt: p.ExpiresAt, Notes: p.Notes,
		CreatedAt: p.CreatedAt}
	if p.Status != nil {
		v.Status = *p.Status
	}
	return v
}

// promoInput is the body of POST and PATCH /promo-codes: a PATCH changes
// the fields it sends (expires_at null clears it); the code never changes.
type promoInput struct {
	Code        *string          `json:"code"`
	BonusAmount *decimal.Decimal `json:"bonus_amount"`
	MinAmount   *decimal.Decimal `json:"min_amount"`
	MaxUses     *int             `json:"max_uses"`
	Status      *string          `json:"status"`
	ExpiresAt   json.RawMessage  `json:"expires_at"`
	Notes       *string          `json:"notes"`
}

// columns validates in and returns the columns it sets.
func (in promoInput) columns(ctx context.Context) (map[string]any, error) {
	f := &fieldErrs{ctx: ctx}
	set := map[string]any{}
	in.amounts(f, set)
	if in.Status != nil {
		f.check(*in.Status == promoActive || *in.Status == promoDisabled, "status", "active or disabled", "只能是 active 或 disabled")
		set["status"] = *in.Status
	}
	if len(in.ExpiresAt) > 0 {
		var at *time.Time
		f.check(json.Unmarshal(in.ExpiresAt, &at) == nil, "expires_at", "an RFC 3339 time or null", "RFC 3339 时间或 null")
		set["expires_at"] = at
	}
	if in.Notes != nil {
		set["notes"] = *in.Notes
	}
	for i := range f.list {
		f.list[i].Field = strings.TrimPrefix(f.list[i].Field, "values.")
	}
	return set, f.err()
}

// amounts validates the bonus, the minimum top-up and the use limit.
func (in promoInput) amounts(f *fieldErrs, set map[string]any) {
	if in.BonusAmount != nil {
		b := *in.BonusAmount
		f.check(b.IsPositive() && b.LessThanOrEqual(core.MaxCreditPerTx) && x.Exact(b, 8), "bonus_amount",
			"must be above 0 and within the single credit limit", "必须大于 0 且不超过单次入账上限")
		set["bonus_amount"] = b
	}
	if in.MinAmount != nil {
		f.check(!in.MinAmount.IsNegative(), "min_amount", "must not be negative", "不能小于 0")
		set["min_amount"] = *in.MinAmount
	}
	if in.MaxUses != nil {
		f.check(*in.MaxUses >= 0, "max_uses", "must not be negative (0 = unlimited)", "不能小于 0（0 表示不限）")
		set["max_uses"] = *in.MaxUses
	}
}

func (s *Service) listPromos(c *gin.Context) {
	page, size := httpapi.Pagination(c)
	db := store.G(c.Request.Context(), s.db.Gorm).Model(&model.PromoCode{})
	if q := c.Query("q"); q != "" {
		db = db.Where("code ILIKE ?", "%"+store.Like(q)+"%")
	}
	rows, total, err := store.FindPage[model.PromoCode](db.Order("id DESC"), page, size)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	out := make([]promoView, len(rows))
	for i := range rows {
		out[i] = promoViewOf(&rows[i], s.now())
	}
	httpapi.List(c, out, httpapi.Page{Page: page, PageSize: size, Total: total})
}

func (s *Service) createPromo(c *gin.Context) {
	var in promoInput
	if !httpapi.BindJSON(c, &in) {
		return
	}
	ctx := audit.Context(c)
	p, err := s.insertPromo(ctx, in)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.Created(c, promoViewOf(p, s.now()))
}

// insertPromo creates a promo code: the given code upper-cased, or a
// random one.
func (s *Service) insertPromo(ctx context.Context, in promoInput) (*model.PromoCode, error) {
	set, err := in.columns(ctx)
	if err != nil {
		return nil, err
	}
	if in.BonusAmount == nil {
		return nil, core.InvalidFields(core.FieldError{Field: "bonus_amount", Code: "required", Message: t(ctx, "required", "必填")})
	}
	code, err := newPromoCode(ctx, in.Code)
	if err != nil {
		return nil, err
	}
	actor, _ := core.UserID(ctx)
	set["code"], set["created_by"] = code, &actor
	var p model.PromoCode
	err = s.db.Tx(ctx, func(tx *gorm.DB) error {
		db := store.G(ctx, tx)
		if err := db.Model(&model.PromoCode{}).Create(set).Error; err != nil {
			return err
		}
		if err := db.Where("code = ?", code).Take(&p).Error; err != nil {
			return err
		}
		return audit.Audit(ctx, tx, actor, "promo_code.create", "promo_code", x.Itoa64(p.ID), map[string]any{"code": code})
	})
	if store.IsUniqueViolation(err, "promo_codes_code_key") {
		return nil, core.ErrConflict.WithMessage(t(ctx, "the promo code already exists", "优惠码已存在"))
	}
	return &p, err
}

// newPromoCode is the code a new promo code takes: the given one upper
// cased, or a random one.
func newPromoCode(ctx context.Context, given *string) (string, error) {
	code := randomCode()
	if given != nil && strings.TrimSpace(*given) != "" {
		code = strings.ToUpper(strings.TrimSpace(*given))
	}
	if !promoCodePattern.MatchString(code) {
		return "", core.InvalidFields(core.FieldError{Field: "code", Code: "invalid", Message: t(ctx, "3-64 of A-Z, 0-9, _ and -", "3-64 位大写字母、数字、下划线或连字符")})
	}
	return code, nil
}

// randomCode is a code of 10 characters from A-Z and 2-9 (no look-alikes).
func randomCode() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

func (s *Service) updatePromo(c *gin.Context) {
	id, ok := httpapi.PathID(c, "id")
	var in promoInput
	if !ok || !httpapi.BindJSON(c, &in) {
		return
	}
	ctx := audit.Context(c)
	p, err := s.patchPromo(ctx, id, in)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, promoViewOf(p, s.now()))
}

// patchPromo writes the fields in sends; the used count stays as it is.
func (s *Service) patchPromo(ctx context.Context, id int64, in promoInput) (*model.PromoCode, error) {
	set, err := in.columns(ctx)
	if err != nil {
		return nil, err
	}
	actor, _ := core.UserID(ctx)
	var p model.PromoCode
	err = s.db.Tx(ctx, func(tx *gorm.DB) error {
		db := store.G(ctx, tx)
		set["updated_at"] = sqlNow
		res := db.Model(&model.PromoCode{}).Where("id = ?", id).Updates(set)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errPromoNotFound(ctx)
		}
		if err := db.Where("id = ?", id).Take(&p).Error; err != nil {
			return err
		}
		return audit.Audit(ctx, tx, actor, "promo_code.update", "promo_code", x.Itoa64(id), auditColumns(set))
	})
	return &p, err
}

func errPromoNotFound(ctx context.Context) *core.Error {
	return core.ErrNotFound.WithMessage(t(ctx, "promo code not found", "优惠码不存在"))
}

// auditColumns are the changed columns for the audit log, values as text.
func auditColumns(set map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range set {
		if d, ok := v.(decimal.Decimal); ok {
			v = d.String()
		}
		if k != "updated_at" {
			out[k] = v
		}
	}
	return out
}

func (s *Service) deletePromo(c *gin.Context) {
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	ctx := audit.Context(c)
	if err := txOnceMore(ctx, s.db, func(tx *gorm.DB) error { return removePromo(ctx, tx, id) }); err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.NoContent(c)
}

// txOnceMore runs fn in a transaction, and once more when the database
// aborted it to break a deadlock (40P01): nothing of the first run was
// applied, and the second starts after the other writer has gone on.
func txOnceMore(ctx context.Context, db *store.DB, fn func(tx *gorm.DB) error) error {
	err := db.Tx(ctx, fn)
	if store.IsDeadlock(err) {
		err = db.Tx(ctx, fn)
	}
	return err
}

// removePromo deletes promo code id, audited. Deleting it sets its orders'
// promo_code_id to NULL, which locks those order rows after the code row,
// while every other path (moveTo releasing a use, a late payment taking one
// again, crediting) locks the order before the code; so the orders that
// already name the code are locked first here, in id order, and never wait
// on those paths in a cycle. An order naming it that commits between that
// lock and the delete is still locked code-first by the delete: that rare
// deadlock aborts one side whole, and deletePromo tries once more.
func removePromo(ctx context.Context, tx *gorm.DB, id int64) error {
	if err := lockPromoOrders(ctx, tx, id); err != nil {
		return err
	}
	res := store.G(ctx, tx).Delete(&model.PromoCode{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errPromoNotFound(ctx)
	}
	actor, _ := core.UserID(ctx)
	return audit.Audit(ctx, tx, actor, "promo_code.delete", "promo_code", x.Itoa64(id), nil)
}

// lockPromoOrders locks the orders that name promo code id, in id order.
func lockPromoOrders(ctx context.Context, tx *gorm.DB, id int64) error {
	if err := store.RequireTx(tx); err != nil {
		return err
	}
	var ids []int64
	return store.G(ctx, tx).Model(&model.PaymentOrder{}).Clauses(store.ForUpdate()).
		Where("promo_code_id = ?", id).Order("id").Pluck("id", &ids).Error
}
