package payment

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"image/png"
	"time"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/qr"
	"github.com/shopspring/decimal"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// newTradeNo is an order number: s2a_ and 128 random bits in hex, which
// nobody can guess (crypto/rand.Read never fails).
func newTradeNo() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "s2a_" + hex.EncodeToString(b[:])
}

// orderView is an order in the API (the console's Order).
type orderView struct {
	ID           int64           `json:"id"`
	OutTradeNo   string          `json:"out_trade_no"`
	UserID       int64           `json:"user_id"`
	UserEmail    string          `json:"user_email,omitempty"`
	Amount       decimal.Decimal `json:"amount"`
	Bonus        decimal.Decimal `json:"bonus"`
	PayAmount    decimal.Decimal `json:"pay_amount"`
	Currency     string          `json:"currency"`
	Method       string          `json:"method"`
	MethodName   string          `json:"method_name"`
	Gateway      string          `json:"gateway"`
	PromoCode    string          `json:"promo_code,omitempty"`
	Status       string          `json:"status"`
	Payable      bool            `json:"payable"`
	TradeNo      string          `json:"trade_no,omitempty"`
	FailedReason string          `json:"failed_reason,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	PaidAt       *time.Time      `json:"paid_at,omitempty"`
	CompletedAt  *time.Time      `json:"completed_at,omitempty"`
	ExpiresAt    time.Time       `json:"expires_at"`
	PayURL       string          `json:"pay_url,omitempty"`
	QRImage      string          `json:"qr_image,omitempty"`
}

// viewOf is the order without where to pay (viewOwn adds it); payable
// tells whether it may still be credited (isPayable).
func viewOf(o *model.PaymentOrder, email string, now time.Time) orderView {
	v := orderView{ID: o.ID, OutTradeNo: o.OutTradeNo, UserID: o.UserID, UserEmail: email, Amount: o.Amount,
		Bonus: o.Bonus, PayAmount: o.PayAmount, Currency: o.Currency, Method: o.Method, MethodName: o.MethodName,
		Gateway: o.Gateway, PromoCode: o.PromoCode, Status: statusOf(o), Payable: isPayable(o, now),
		FailedReason: o.FailReason, CreatedAt: o.CreatedAt, PaidAt: o.PaidAt, CompletedAt: o.CompletedAt, ExpiresAt: o.ExpiresAt}
	if o.TradeNo != nil {
		v.TradeNo = *o.TradeNo
	}
	return v
}

// viewOwn is the user's own order: while it is open, where to pay. A list
// or a poll carries only the pay address; "continue paying" (viewPaying)
// also renders the QR code, one PNG per resume instead of one per row or
// per poll.
func viewOwn(o *model.PaymentOrder, now time.Time) orderView {
	v := viewOf(o, "", now)
	if isOpen(o, now) {
		v.PayURL = o.PayURL
	}
	return v
}

// viewPaying is viewOwn with the QR code's image while the order is open
// ("continue paying").
func viewPaying(o *model.PaymentOrder, now time.Time) orderView {
	v := viewOwn(o, now)
	if isOpen(o, now) {
		v.QRImage = qrImageOf(o.QrCode)
	}
	return v
}

// qrSize is the side of a payment QR code, in pixels.
const qrSize = 240

// qrImage renders a gateway's QR code text as a PNG data URL: the console
// shows it as an image and never handles the text.
func qrImage(text string) (string, error) {
	if text == "" {
		return "", nil
	}
	code, err := qr.Encode(text, qr.M, qr.Auto)
	if err != nil {
		return "", err
	}
	if code, err = barcode.Scale(code, qrSize, qrSize); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, code); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// qrImageOf is qrImage of a stored QR code. An order only keeps a code
// that rendered when it was created (checkCreated), so the error cannot
// happen; were it to, the order shows no image and its pay address stays.
func qrImageOf(text string) string {
	img, _ := qrImage(text)
	return img
}

// errNotFound is the answer for an order that is not the caller's.
func errNotFound(ctx context.Context) *core.Error {
	return core.ErrNotFound.WithMessage(t(ctx, "order not found", "订单不存在"))
}
