package payment

import (
	"context"
	"slices"
	"time"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// Order states: pending → paid → completed; failed when the payment could
// not be created or the credit was refused; expired and cancelled may still
// turn paid within lateWindow (the user paid in time).
const (
	statusPending   = "pending"
	statusPaid      = "paid"
	statusCompleted = "completed"
	statusFailed    = "failed"
	statusExpired   = "expired"
	statusCancelled = "cancelled"
)

// payableStatuses are the states a paid notification may move an order out
// of: pending, and expired or cancelled within lateWindow (payable).
var payableStatuses = []string{statusPending, statusExpired, statusCancelled}

// transitions is the state machine: each state and the states an order
// may enter it from. Failing from pending is a payment that could not be
// created (no one has a pay address yet), from paid a refused credit.
var transitions = map[string][]string{
	statusPaid:      payableStatuses,
	statusCompleted: {statusPaid},
	statusFailed:    {statusPending, statusPaid},
	statusExpired:   {statusPending},
	statusCancelled: {statusPending},
}

// releasing are the end states of an order that will never credit its
// bonus: entering one gives back the promo use the order holds.
var releasing = map[string]bool{statusFailed: true, statusExpired: true, statusCancelled: true}

// moveTo moves order id to state to, with the other columns of set, when
// the order is in a state transitions allows it from, and reports whether
// it did. Every step of the state machine goes through it: a conditional
// update that stamps updated_at (the start of the late window) and, into a
// releasing state, frees the order's promo use in the same transaction.
func moveTo(ctx context.Context, tx *gorm.DB, id int64, to string, set map[string]any) (bool, error) {
	if err := store.RequireTx(tx); err != nil {
		return false, err
	}
	if set == nil {
		set = map[string]any{}
	}
	set["status"], set["updated_at"] = to, sqlNow
	res := store.G(ctx, tx).Model(&model.PaymentOrder{}).
		Where("id = ? AND status = ANY(?)", id, store.Strings(transitions[to])).Updates(set)
	if res.Error != nil || res.RowsAffected != 1 {
		return false, res.Error
	}
	if releasing[to] {
		return true, releaseUse(ctx, tx, id)
	}
	return true, nil
}

// The two questions asked of an order's state, each as SQL and in Go:
// open is whether the user can pay it now (pending, not yet expired);
// payable is whether a paid notification may still credit it (pending, or
// expired or cancelled within lateWindow, by updated_at).
const (
	openCond    = "status = ? AND expires_at > ?"
	payableCond = "status = ANY(?) AND (status = ? OR updated_at > ?)"
)

func openOrders(now time.Time) func(*gorm.DB) *gorm.DB {
	return func(q *gorm.DB) *gorm.DB { return q.Where(openCond, statusPending, now) }
}

func payableOrders(now time.Time) func(*gorm.DB) *gorm.DB {
	return func(q *gorm.DB) *gorm.DB { return q.Where(payableCond, payableArgs(now)...) }
}

func payableArgs(now time.Time) []any {
	return []any{store.Strings(payableStatuses), statusPending, now.Add(-lateWindow)}
}

func isOpen(o *model.PaymentOrder, now time.Time) bool {
	return statusOf(o) == statusPending && o.ExpiresAt.After(now)
}

func isPayable(o *model.PaymentOrder, now time.Time) bool {
	st := statusOf(o)
	return slices.Contains(payableStatuses, st) && (st == statusPending || o.UpdatedAt.After(now.Add(-lateWindow)))
}

func statusOf(o *model.PaymentOrder) string {
	if o.Status == nil {
		return statusPending
	}
	return *o.Status
}

// lockOrder reads the order find selects under a row lock; nil when there
// is none.
func lockOrder(ctx context.Context, tx *gorm.DB, find func(*gorm.DB) *gorm.DB) (*model.PaymentOrder, error) {
	if err := store.RequireTx(tx); err != nil {
		return nil, err
	}
	var o model.PaymentOrder
	err := store.G(ctx, tx).Clauses(store.ForUpdate()).Scopes(find).Take(&o).Error
	if store.IsNoRows(err) {
		return nil, nil
	}
	return &o, err
}

// numbered selects the order numbered no.
func numbered(no string) func(*gorm.DB) *gorm.DB {
	return func(q *gorm.DB) *gorm.DB { return q.Where("out_trade_no = ?", no) }
}

// inState selects order id while it is in state status.
func inState(id int64, status string) func(*gorm.DB) *gorm.DB {
	return func(q *gorm.DB) *gorm.DB { return q.Where("id = ? AND status = ?", id, status) }
}
