package redeem

import (
	"context"
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

// Redeemed is the answer of POST /me/redeem.
type Redeemed struct {
	Amount  decimal.Decimal `json:"amount"`
	Balance decimal.Decimal `json:"balance"`
}

// usable is the code row a redemption may take: unused and not expired.
const usable = "code_hash = ? AND status = '" + statusUnused + "' AND (expires_at IS NULL OR expires_at > now())"

// Redeem credits the code's value to the user. Locking the code, marking it
// used and the ledger credit commit together or not at all, so a code is
// never used without its credit nor credited twice.
func (s *Service) Redeem(ctx context.Context, userID int64, raw string) (*Redeemed, error) {
	code := normalize(raw)
	if code == "" {
		return nil, core.InvalidFields(core.FieldError{Field: "code", Code: "required",
			Message: core.Text(ctx, "enter a redeem code", "请输入兑换码")})
	}
	if err := s.admit(ctx, userID); err != nil {
		return nil, err
	}
	out, res, err := s.redeem(ctx, userID, code)
	var invalid *core.Error
	if errors.As(err, &invalid) && invalid.Code == "invalid_redeem_code" {
		s.fails.fail(ctx, userID)
	}
	if err != nil {
		return nil, err
	}
	s.ledger.CacheBalance(ctx, userID, res.LedgerID, res.BalanceAfter)
	return out, nil
}

func (s *Service) redeem(ctx context.Context, userID int64, code string) (*Redeemed, *core.LedgerResult, error) {
	if !wellFormed(code) {
		return nil, nil, errInvalidCode(ctx)
	}
	hash := hashCode(code)
	var res *core.LedgerResult
	var value decimal.Decimal
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		row, err := takeUsable(ctx, tx, hash)
		if err != nil {
			return err
		}
		value = row.Value
		res, err = s.credit(ctx, tx, userID, row)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return &Redeemed{Amount: value, Balance: res.BalanceAfter}, res, nil
}

// takeUsable locks the usable code of hash; concurrent redemptions of one
// code queue here and every one after the first finds it used.
func takeUsable(ctx context.Context, tx *gorm.DB, hash string) (*model.RedeemCode, error) {
	if err := store.RequireTx(tx); err != nil {
		return nil, err
	}
	var row model.RedeemCode
	err := store.G(ctx, tx).Clauses(store.ForUpdate()).Select("id", "code_hash", "value").Where(usable, hash).Take(&row).Error
	if store.IsNoRows(err) {
		return nil, errInvalidCode(ctx)
	}
	return &row, err
}

// credit marks the locked code used by the user and credits its value.
func (s *Service) credit(ctx context.Context, tx *gorm.DB, userID int64, row *model.RedeemCode) (*core.LedgerResult, error) {
	err := store.G(ctx, tx).Model(&model.RedeemCode{}).Where("id = ?", row.ID).Updates(map[string]any{
		"status": statusUsed, "used_by": userID, "used_at": store.DBNow{}, "updated_at": store.DBNow{},
	}).Error
	if err != nil {
		return nil, err
	}
	res, err := s.ledger.ApplyTx(ctx, tx, core.LedgerChange{
		UserID: userID, Amount: row.Value, Credit: true, Kind: kindRedeem,
		RefType: "redeem_code", RefID: x.Itoa64(row.ID), IdempotencyKey: "redeem:" + row.CodeHash,
	})
	if err == nil && res.Duplicate {
		// Only a code row re-created under a credited hash gets here.
		err = fmt.Errorf("redeem: code %d already credited (ledger %d)", row.ID, res.LedgerID)
	}
	return res, err
}
