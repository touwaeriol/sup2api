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

// userLock is the advisory lock of a user's top-up: ordering and crediting
// take it, so the limits add up the user's orders consistently.
func userLock(ctx context.Context, tx *gorm.DB, userID int64) error {
	return store.XactLock(ctx, tx, "topup:user:"+x.Itoa64(userID))
}

// credit credits a paid order: paid → completed and the ledger rows commit
// in one transaction under the order's row lock, so concurrent credits of
// one order wait for each other and the later ones find it no longer paid
// (the ledger's idempotency keys stand behind that). An order that is not
// paid (any more) is not an error.
func (s *Service) credit(ctx context.Context, id int64) error {
	results, userID, err := s.creditTx(ctx, id)
	var r refusal
	if errors.As(err, &r) {
		return s.failCredit(ctx, id, r.reason)
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

// creditTx locks the paid order, checks the limits under the user's lock,
// applies its ledger rows and completes it.
func (s *Service) creditTx(ctx context.Context, id int64) ([]*core.LedgerResult, int64, error) {
	var out []*core.LedgerResult
	var userID int64
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		o, err := lockOrder(ctx, tx, inState(id, statusPaid))
		if err != nil || o == nil {
			return err
		}
		userID = o.UserID
		if err := userLock(ctx, tx, userID); err != nil {
			return err
		}
		if err := s.checkCreditLimits(ctx, tx, o); err != nil {
			return err
		}
		if out, err = s.applyLedger(ctx, tx, o); err != nil {
			return err
		}
		if err := markUsed(ctx, tx, o.ID); err != nil {
			return err
		}
		return mustMove(moveTo(ctx, tx, o.ID, statusCompleted, map[string]any{"completed_at": sqlNow}))
	})
	return out, userID, err
}

// mustMove turns a transition that did not happen into errLostRace.
func mustMove(ok bool, err error) error {
	if err == nil && !ok {
		return errLostRace
	}
	return err
}

// applyLedger credits the top-up and its promo bonus as two ledger rows
// (kinds topup and promo_bonus, keys topup:<no> and promo:<no>). A change
// the ledger refuses is a refusal.
func (s *Service) applyLedger(ctx context.Context, tx *gorm.DB, o *model.PaymentOrder) ([]*core.LedgerResult, error) {
	changes := []core.LedgerChange{{UserID: o.UserID, Amount: o.Amount, Credit: true, Kind: core.LedgerKindTopup,
		RefType: refTypeOrder, RefID: o.OutTradeNo, IdempotencyKey: "topup:" + o.OutTradeNo, Note: "online top-up"}}
	if o.Bonus.IsPositive() {
		changes = append(changes, core.LedgerChange{UserID: o.UserID, Amount: o.Bonus, Credit: true, Kind: core.LedgerKindPromoBonus,
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

// appendReason adds reason to the order's fail_reason, keeping one it
// already has (a late payment that lost its promo bonus).
const appendReason = "CASE WHEN fail_reason = '' THEN ? ELSE fail_reason || '; ' || ? END"

// failCredit fails a paid order whose credit was refused; moving to failed
// gives back its promo use, as the bonus will never be credited. The
// refused credit's transaction rolled back, so this is a new one.
func (s *Service) failCredit(ctx context.Context, id int64, reason string) error {
	slog.WarnContext(ctx, "payment: credit refused", "order_id", id, "reason", reason)
	return s.db.Tx(ctx, func(tx *gorm.DB) error {
		_, err := moveTo(ctx, tx, id, statusFailed, map[string]any{"fail_reason": clause.Expr{SQL: appendReason, Vars: []any{reason, reason}}})
		return err
	})
}
