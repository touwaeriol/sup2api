package payment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
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

var orderStatuses = []string{statusPending, statusPaid, statusCompleted, statusFailed, statusExpired, statusCancelled}

// newTradeNo is an order number nobody can guess: s2a_ and 128 random bits.
func newTradeNo() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "s2a_" + hex.EncodeToString(b[:]), nil
}

// payable narrows q to the orders a paid notification may still credit:
// pending, or expired or cancelled (updated_at is when) within lateWindow.
func payable(q *gorm.DB, now time.Time) *gorm.DB {
	return q.Where("(status = ? OR (status IN (?, ?) AND updated_at > ?))",
		statusPending, statusExpired, statusCancelled, now.Add(-lateWindow))
}

// lateOK reports whether o may still be credited at now (payable).
func lateOK(o *model.PaymentOrder, now time.Time) bool {
	switch statusOf(o) {
	case statusPending:
		return true
	case statusExpired, statusCancelled:
		return o.UpdatedAt.After(now.Add(-lateWindow))
	}
	return false
}

func statusOf(o *model.PaymentOrder) string {
	if o.Status == nil {
		return statusPending
	}
	return *o.Status
}

// lockOrder reads the order numbered no under a row lock; nil when there
// is none.
func lockOrder(ctx context.Context, tx *gorm.DB, no string) (*model.PaymentOrder, error) {
	if err := store.RequireTx(tx); err != nil {
		return nil, err
	}
	var o model.PaymentOrder
	err := store.G(ctx, tx).Clauses(store.ForUpdate()).Where("out_trade_no = ?", no).Take(&o).Error
	if store.IsNoRows(err) {
		return nil, nil
	}
	return &o, err
}

// setStatus moves order id from one of from to status with the other
// columns of set, and reports whether it did: every step of the state
// machine is such a conditional update.
func setStatus(ctx context.Context, q *gorm.DB, id int64, status string, set map[string]any, from ...string) (bool, error) {
	if set == nil {
		set = map[string]any{}
	}
	set["status"], set["updated_at"] = status, clause.Expr{SQL: "now()"}
	res := store.G(ctx, q).Model(&model.PaymentOrder{}).Where("id = ? AND status = ANY(?)", id, store.Strings(from)).Updates(set)
	return res.RowsAffected == 1, res.Error
}

// orderView is an order in the API (the console's Order).
type orderView struct {
	ID           int64      `json:"id"`
	OutTradeNo   string     `json:"out_trade_no"`
	UserID       int64      `json:"user_id"`
	UserEmail    string     `json:"user_email,omitempty"`
	Amount       Num        `json:"amount"`
	Bonus        Num        `json:"bonus"`
	PayAmount    Num        `json:"pay_amount"`
	Currency     string     `json:"currency"`
	Method       string     `json:"method"`
	MethodName   string     `json:"method_name"`
	Gateway      string     `json:"gateway"`
	PromoCode    string     `json:"promo_code,omitempty"`
	Status       string     `json:"status"`
	TradeNo      string     `json:"trade_no,omitempty"`
	FailedReason string     `json:"failed_reason,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	PaidAt       *time.Time `json:"paid_at,omitempty"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	ExpiresAt    time.Time  `json:"expires_at"`
	PayURL       string     `json:"pay_url,omitempty"`
	QRCode       string     `json:"qr_code,omitempty"`
}

// viewOf is the order without where to pay (viewOwn adds it).
func viewOf(o *model.PaymentOrder, email string) orderView {
	v := orderView{ID: o.ID, OutTradeNo: o.OutTradeNo, UserID: o.UserID, UserEmail: email, Amount: num(o.Amount),
		Bonus: num(o.Bonus), PayAmount: num(o.PayAmount), Currency: o.Currency, Method: o.Method, MethodName: o.MethodName,
		Gateway: o.Gateway, PromoCode: o.PromoCode, Status: statusOf(o), FailedReason: o.FailReason,
		CreatedAt: o.CreatedAt, PaidAt: o.PaidAt, CompletedAt: o.CompletedAt, ExpiresAt: o.ExpiresAt}
	if o.TradeNo != nil {
		v.TradeNo = *o.TradeNo
	}
	return v
}

// viewOwn is the user's own order: while it can be paid, where to pay
// ("continue paying").
func viewOwn(o *model.PaymentOrder, now time.Time) orderView {
	v := viewOf(o, "")
	if v.Status == statusPending && o.ExpiresAt.After(now) {
		v.PayURL, v.QRCode = o.PayURL, o.QrCode
	}
	return v
}

// errNotFound is the answer for an order that is not the caller's.
func errNotFound(ctx context.Context) *core.Error {
	return core.ErrNotFound.WithMessage(t(ctx, "order not found", "订单不存在"))
}
