package redeem

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

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

// Redeem credits the code's value to the user. A wrong code counts towards
// the user's lockout; anything else that fails does not.
func (s *Service) Redeem(ctx context.Context, userID int64, raw string) (*Redeemed, error) {
	code := normalize(raw)
	if code == "" {
		return nil, core.InvalidFields(core.NewFieldError(ctx, "code", "required", "enter a redeem code", "请输入兑换码"))
	}
	if err := s.admit(ctx, userID); err != nil {
		return nil, err
	}
	out, err := s.redeem(ctx, userID, code)
	if err != nil && core.AsError(err).Code == codeInvalid {
		s.fails.Fail(ctx, failKey(userID))
	}
	return out, err
}

// redeem spends the code and credits its value in one transaction, so that a
// code is never used without its credit nor credited twice, then refreshes
// the balance cache.
func (s *Service) redeem(ctx context.Context, userID int64, code string) (*Redeemed, error) {
	if !wellFormed(code) {
		return nil, errInvalidCode(ctx)
	}
	var row *model.RedeemCode
	var res *core.LedgerResult
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		var err error
		if row, err = spend(ctx, tx, userID, hashCode(code)); err != nil {
			return err
		}
		res, err = s.credit(ctx, tx, userID, row)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.ledger.CacheBalance(ctx, userID, res.LedgerID, res.BalanceAfter)
	return &Redeemed{Amount: row.Value, Balance: res.BalanceAfter}, nil
}

// spentColumns are what spend reads back of the code it used.
var spentColumns = clause.Returning{Columns: []clause.Column{{Name: "id"}, {Name: "value"}}}

// spend marks the redeemable code of hash used by userID in one conditional
// UPDATE. Concurrent redemptions of a code queue on its row; PostgreSQL
// re-checks the condition for each once the first commits, so only one of
// them finds the code redeemable and the others get the invalid code.
func spend(ctx context.Context, tx *gorm.DB, userID int64, hash string) (*model.RedeemCode, error) {
	if err := store.RequireTx(tx); err != nil {
		return nil, err
	}
	row := model.RedeemCode{CodeHash: hash}
	res := store.G(ctx, tx).Model(&row).Clauses(spentColumns).Where("code_hash = ? AND "+redeemable, hash).Updates(map[string]any{
		"status": statusUsed, "used_by": userID, "used_at": store.DBNow{}, "updated_at": store.DBNow{},
	})
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected != 1 {
		return nil, errInvalidCode(ctx)
	}
	return &row, nil
}

// credit is the ledger credit of the code just spent, in its transaction.
func (s *Service) credit(ctx context.Context, tx *gorm.DB, userID int64, row *model.RedeemCode) (*core.LedgerResult, error) {
	res, err := s.ledger.ApplyTx(ctx, tx, core.LedgerChange{
		UserID: userID, Amount: row.Value, Credit: true, Kind: core.LedgerKindRedeem,
		RefType: "redeem_code", RefID: x.Itoa64(row.ID), IdempotencyKey: "redeem:" + row.CodeHash,
	})
	if err == nil && res.Duplicate {
		// Only a code row re-created under a credited hash gets here.
		err = fmt.Errorf("redeem: code %d already credited (ledger %d)", row.ID, res.LedgerID)
	}
	return res, err
}
