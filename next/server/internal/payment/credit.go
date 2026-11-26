package payment

import (
	"context"
	"errors"
	"log/slog"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

// errLostRace: the order moved on under a conditional update; the caller's
// transaction rolls back and the work is done (or retried) elsewhere.
var errLostRace = errors.New("payment: the order changed meanwhile")

// refusal is a credit the limits or the ledger refused: the order fails
// with the reason, which the administrator sees on it.
type refusal struct{ reason string }

func (r refusal) Error() string { return r.reason }

// credit credits a paid order under a crediting lease: paid → completed
// and the ledger rows commit together. The lease is a fresh token per
// claim, so a worker whose lease ran out and was taken over cannot finish
// (its conditional updates name the old token), and the ledger's
// idempotency keys stand behind that. A busy lease is not an error.
func (s *Service) credit(ctx context.Context, id int64) error {
	token, err := s.claimLease(ctx, id)
	if err != nil || token == "" {
		return err
	}
	results, userID, err := s.creditTx(ctx, id, token)
	var r refusal
	if errors.As(err, &r) {
		return s.failCredit(ctx, id, token, r.reason)
	}
	if err != nil {
		return err
	}
	for _, res := range results {
		if !res.Duplicate {
			s.ledger.CacheBalance(ctx, userID, res.LedgerID, res.BalanceAfter)
		}
	}
	return nil
}

// claimLease takes the crediting lease of a paid order when it is free or
// ran out; "" when another worker holds it.
func (s *Service) claimLease(ctx context.Context, id int64) (string, error) {
	token, err := randomHex()
	if err != nil {
		return "", err
	}
	res := store.G(ctx, s.db.Gorm).Model(&model.PaymentOrder{}).
		Where("id = ? AND status = ? AND (lease_until IS NULL OR lease_until < now())", id, statusPaid).
		Updates(map[string]any{"lease_token": token,
			"lease_until": clause.Expr{SQL: "now() + make_interval(secs => ?)", Vars: []any{leaseTTL.Seconds()}}})
	if res.Error != nil || res.RowsAffected == 0 {
		return "", res.Error
	}
	return token, nil
}

// creditTx applies the order's ledger rows and completes it, in one
// transaction under the user's advisory lock (the daily limit adds up the
// user's credits).
func (s *Service) creditTx(ctx context.Context, id int64, token string) ([]*core.LedgerResult, int64, error) {
	var out []*core.LedgerResult
	var userID int64
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		o, err := lockLeased(ctx, tx, id, token)
		if err != nil || o == nil {
			return err
		}
		userID = o.UserID
		if err := store.XactLock(ctx, tx, "topup:user:"+x.Itoa64(userID)); err != nil {
			return err
		}
		if err := s.checkLimits(ctx, tx, o); err != nil {
			return err
		}
		if out, err = s.applyLedger(ctx, tx, o); err != nil {
			return err
		}
		if err := markUsed(ctx, tx, o.ID); err != nil {
			return err
		}
		return finishLease(ctx, tx, o.ID, token, statusCompleted, map[string]any{"completed_at": clause.Expr{SQL: "now()"}})
	})
	return out, userID, err
}

// lockLeased reads the paid order under a row lock while token holds its
// lease; nil when it no longer does.
func lockLeased(ctx context.Context, tx *gorm.DB, id int64, token string) (*model.PaymentOrder, error) {
	if err := store.RequireTx(tx); err != nil {
		return nil, err
	}
	var o model.PaymentOrder
	err := store.G(ctx, tx).Clauses(store.ForUpdate()).
		Where("id = ? AND status = ? AND lease_token = ?", id, statusPaid, token).Take(&o).Error
	if store.IsNoRows(err) {
		return nil, nil
	}
	return &o, err
}

// applyLedger credits the top-up and its promo bonus as two ledger rows
// (kinds topup and promo_bonus, keys topup:<no> and promo:<no>). A change
// the ledger refuses is a refusal.
func (s *Service) applyLedger(ctx context.Context, tx *gorm.DB, o *model.PaymentOrder) ([]*core.LedgerResult, error) {
	changes := []core.LedgerChange{{UserID: o.UserID, Amount: o.Amount, Credit: true, Kind: kindTopup,
		RefType: refTypeOrder, RefID: o.OutTradeNo, IdempotencyKey: "topup:" + o.OutTradeNo, Note: "online top-up"}}
	if o.Bonus.IsPositive() {
		changes = append(changes, core.LedgerChange{UserID: o.UserID, Amount: o.Bonus, Credit: true, Kind: kindPromoBonus,
			RefType: refTypeOrder, RefID: o.OutTradeNo, IdempotencyKey: "promo:" + o.OutTradeNo, Note: "promo code " + o.PromoCode})
	}
	out := make([]*core.LedgerResult, 0, len(changes))
	for _, ch := range changes {
		res, err := s.ledger.ApplyTx(ctx, tx, ch)
		if e := core.AsError(err); err != nil && e.Status < 500 {
			return nil, refusal{"ledger refused the credit: " + e.Message}
		}
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, nil
}

// finishLease moves the order out of paid while token holds its lease,
// releasing the lease.
func finishLease(ctx context.Context, tx *gorm.DB, id int64, token, status string, set map[string]any) error {
	set["status"], set["updated_at"] = status, clause.Expr{SQL: "now()"}
	set["lease_token"], set["lease_until"] = "", nil
	res := store.G(ctx, tx).Model(&model.PaymentOrder{}).
		Where("id = ? AND status = ? AND lease_token = ?", id, statusPaid, token).Updates(set)
	if res.Error == nil && res.RowsAffected != 1 {
		return errLostRace
	}
	return res.Error
}

// appendReason adds reason to the order's fail_reason, keeping one it
// already has (a late payment that lost its promo bonus).
const appendReason = "CASE WHEN fail_reason = '' THEN ? ELSE fail_reason || '; ' || ? END"

// failCredit fails a paid order whose credit was refused and gives back
// the promo use it holds: the bonus will never be credited.
func (s *Service) failCredit(ctx context.Context, id int64, token, reason string) error {
	slog.WarnContext(ctx, "payment: credit refused", "order_id", id, "reason", reason)
	return s.db.Tx(ctx, func(tx *gorm.DB) error {
		set := map[string]any{"fail_reason": clause.Expr{SQL: appendReason, Vars: []any{reason, reason}}}
		if err := finishLease(ctx, tx, id, token, statusFailed, set); err != nil {
			return err
		}
		return releaseUse(ctx, tx, id)
	})
}
