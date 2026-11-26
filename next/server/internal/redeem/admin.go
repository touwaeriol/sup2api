package redeem

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

// Code is a row of GET /redeem-codes: never the code itself, its last four
// characters only.
type Code struct {
	ID          int64           `json:"id"`
	CodeTail    string          `json:"code_tail"`
	Value       decimal.Decimal `json:"value"`
	Status      string          `json:"status"`
	UsedBy      *int64          `json:"used_by,omitempty"`
	UsedByEmail *string         `json:"used_by_email,omitempty"`
	UsedAt      *time.Time      `json:"used_at,omitempty"`
	ExpiresAt   *time.Time      `json:"expires_at,omitempty"`
	Notes       string          `json:"notes"`
	CreatedAt   time.Time       `json:"created_at"`
}

// codeColumns reads a code with its reported status (expired: unused past
// expires_at) and the email of who used it.
const codeColumns = `r.id, r.code_tail, r.value,
	CASE WHEN r.status = '` + statusUnused + `' AND r.expires_at <= now() THEN '` + statusExpired + `' ELSE r.status END AS status,
	r.used_by, u.email AS used_by_email, r.used_at, r.expires_at, r.notes, r.created_at`

// statusConds are the ?status= filters by reported status.
var statusConds = map[string]string{
	statusUnused:  "r.status = '" + statusUnused + "' AND (r.expires_at IS NULL OR r.expires_at > now())",
	statusExpired: "r.status = '" + statusUnused + "' AND r.expires_at <= now()",
	statusUsed:    "r.status = '" + statusUsed + "'",
	statusVoided:  "r.status = '" + statusVoided + "'",
}

func codeRows(db *gorm.DB, f store.Conds) *gorm.DB {
	return db.Table("redeem_codes r").Scopes(f.Scope())
}

// List is a page of codes, newest first; status "" is every code.
func (s *Service) List(ctx context.Context, status string, page, size int) ([]Code, int64, error) {
	var f store.Conds
	if status != "" {
		cond, ok := statusConds[status]
		if !ok {
			return nil, 0, core.ErrInvalidArgument.WithMessage("invalid status")
		}
		f.And(cond)
	}
	g := store.G(ctx, s.db.Gorm)
	var total int64
	if err := codeRows(g, f).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	items := []Code{}
	err := codeRows(g, f).Select(codeColumns).Joins("LEFT JOIN users u ON u.id = r.used_by").
		Order("r.id DESC").Limit(size).Offset((page - 1) * size).Scan(&items).Error
	return items, total, err
}

// Void voids an unused code (an expired one too: it is still unused) for
// the operator, audited.
func (s *Service) Void(ctx context.Context, operator, id int64) error {
	return s.db.Tx(ctx, func(tx *gorm.DB) error {
		res := store.G(ctx, tx).Model(&model.RedeemCode{}).Where("id = ? AND status = ?", id, statusUnused).
			Updates(map[string]any{"status": statusVoided, "voided_by": operator, "updated_at": store.DBNow{}})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return notVoidable(ctx, tx, id)
		}
		return audit.Audit(ctx, tx, operator, "redeem_code.void", "redeem_code", x.Itoa64(id), nil)
	})
}

// notVoidable tells a missing code from one already used or voided.
func notVoidable(ctx context.Context, tx *gorm.DB, id int64) error {
	var n int64
	if err := store.G(ctx, tx).Model(&model.RedeemCode{}).Where("id = ?", id).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return core.ErrNotFound
	}
	return core.ErrConflict.WithMessage(core.Text(ctx, "only an unused code can be voided", "只能作废未使用的兑换码"))
}
