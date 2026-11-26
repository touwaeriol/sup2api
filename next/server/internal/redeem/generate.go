package redeem

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

// Batch bounds of POST /redeem-codes: a code's value is one ledger credit,
// so at most core.MaxCreditPerTx with the ledger's decimals.
const (
	maxCount    = 1000
	maxNotes    = 200
	valuePlaces = 8
)

// GenerateRequest is the body of POST /redeem-codes.
type GenerateRequest struct {
	Count     int             `json:"count"`
	Value     decimal.Decimal `json:"value"`
	ExpiresAt *time.Time      `json:"expires_at"`
	Notes     string          `json:"notes"`
}

// Generated is its answer: the plain codes, grouped for display, which
// nothing keeps.
type Generated struct {
	Codes []string `json:"codes"`
}

func (in GenerateRequest) validate(ctx context.Context) error {
	var fields []core.FieldError
	if in.Count < 1 || in.Count > maxCount {
		fields = append(fields, core.NewFieldError(ctx, "count", "invalid", fmt.Sprintf("1 to %d codes", maxCount), fmt.Sprintf("数量为 1–%d", maxCount)))
	}
	if !in.Value.IsPositive() || in.Value.GreaterThan(core.MaxCreditPerTx) || !x.Exact(in.Value, valuePlaces) {
		fields = append(fields, core.NewFieldError(ctx, "value", "invalid", fmt.Sprintf("above 0 and at most %s, with at most %d decimals", core.MaxCreditPerTx, valuePlaces),
			fmt.Sprintf("须大于 0、不超过 %s，最多 %d 位小数", core.MaxCreditPerTx, valuePlaces)))
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(time.Now()) {
		fields = append(fields, core.NewFieldError(ctx, "expires_at", "invalid", "must be in the future", "须晚于当前时间"))
	}
	if utf8.RuneCountInString(in.Notes) > maxNotes {
		fields = append(fields, core.NewFieldError(ctx, "notes", "invalid", fmt.Sprintf("at most %d characters", maxNotes), fmt.Sprintf("最多 %d 个字符", maxNotes)))
	}
	if len(fields) > 0 {
		return core.InvalidFields(fields...)
	}
	return nil
}

// Generate creates a batch of codes for the operator, audited, and returns
// them in plain text this once.
func (s *Service) Generate(ctx context.Context, operator int64, in GenerateRequest) (*Generated, error) {
	if err := in.validate(ctx); err != nil {
		return nil, err
	}
	plain, rows, err := newBatch(operator, in)
	if err != nil {
		return nil, err
	}
	err = s.db.Tx(ctx, func(tx *gorm.DB) error {
		if err := store.G(ctx, tx).Create(&rows).Error; err != nil {
			return err
		}
		return audit.Audit(ctx, tx, operator, "redeem_code.generate", "redeem_code", "", map[string]any{
			"count": in.Count, "value": in.Value, "expires_at": in.ExpiresAt, "first_id": rows[0].ID, "last_id": rows[len(rows)-1].ID,
		})
	})
	if err != nil {
		return nil, err
	}
	return &Generated{Codes: plain}, nil
}

// newBatch makes the codes and their rows (hash and tail only).
func newBatch(operator int64, in GenerateRequest) ([]string, []model.RedeemCode, error) {
	plain := make([]string, in.Count)
	rows := make([]model.RedeemCode, in.Count)
	for i := range rows {
		code, err := newCode()
		if err != nil {
			return nil, nil, err
		}
		plain[i] = displayCode(code)
		rows[i] = model.RedeemCode{CodeHash: hashCode(code), CodeTail: codeTail(code), Value: in.Value,
			ExpiresAt: in.ExpiresAt, Notes: in.Notes, CreatedBy: &operator}
	}
	return plain, rows, nil
}
