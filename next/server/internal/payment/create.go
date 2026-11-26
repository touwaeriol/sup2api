package payment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// createdView is POST /me/topup/orders: where to pay.
type createdView struct {
	OutTradeNo string    `json:"out_trade_no"`
	PayURL     string    `json:"pay_url,omitempty"`
	QRImage    string    `json:"qr_image,omitempty"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// maxGatewayRef bounds the session id and merchant a gateway returns (the
// columns' width).
const maxGatewayRef = 200

// placeOrder writes a pending order, then asks its gateway for the
// payment. A gateway that fails leaves the order failed and its promo use
// released.
func (s *Service) placeOrder(ctx context.Context, userID int64, in quoteInput, clientIP string) (*createdView, error) {
	o, err := s.insertOrder(ctx, userID, in)
	if err != nil {
		return nil, err
	}
	view, err := s.startPayment(ctx, o, clientIP)
	if err != nil {
		s.abandon(context.WithoutCancel(ctx), o, err)
		return nil, core.ErrUnavailable.WithMessage(t(ctx, "the payment gateway is not available, try again later",
			"支付网关暂时不可用，请稍后重试")).WithCause(err)
	}
	return view, nil
}

// insertOrder checks and writes the order in one transaction under the
// user's advisory lock, so concurrent orders of a user cannot pass the
// pending cap together; the settings row is read under a share lock, so a
// merchant change waits for the order and then sees it.
func (s *Service) insertOrder(ctx context.Context, userID int64, in quoteInput) (*model.PaymentOrder, error) {
	var o *model.PaymentOrder
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		if err := userLock(ctx, tx, userID); err != nil {
			return err
		}
		var promo *model.PromoCode
		var err error
		if o, promo, err = s.buildOrder(ctx, tx, userID, in); err != nil {
			return err
		}
		if err := store.G(ctx, tx).Create(o).Error; err != nil {
			return err
		}
		if promo == nil {
			return nil
		}
		return reserveUse(ctx, tx, promo, userID, o.ID)
	})
	return o, err
}

// buildOrder checks in against the settings (under a share lock), the
// user's pending orders and the credit limits, and prices it, the promo
// code locked when there is one.
func (s *Service) buildOrder(ctx context.Context, tx *gorm.DB, userID int64, in quoteInput) (*model.PaymentOrder, *model.PromoCode, error) {
	if err := store.RequireTx(tx); err != nil {
		return nil, nil, err
	}
	q, err := s.quote(ctx, tx, in, store.Lock(clause.LockingStrengthShare))
	if err != nil {
		return nil, nil, err
	}
	if err := s.checkPending(ctx, tx, userID, q.settings.MaxPendingOrders); err != nil {
		return nil, nil, err
	}
	promo, err := s.orderPromo(ctx, tx, in.promoCode(), userID, q.price)
	if err != nil {
		return nil, nil, err
	}
	total := q.price.Amount
	if promo != nil {
		total = total.Add(promo.BonusAmount)
	}
	if err := s.checkOrderLimits(ctx, tx, userID, total); err != nil {
		return nil, nil, err
	}
	return s.newOrder(userID, q, promo), promo, nil
}

// checkPending refuses an order past the user's cap of open orders.
func (s *Service) checkPending(ctx context.Context, tx *gorm.DB, userID int64, max int) error {
	var n int64
	err := store.G(ctx, tx).Model(&model.PaymentOrder{}).Where("user_id = ?", userID).Scopes(openOrders(s.now())).Count(&n).Error
	if err != nil {
		return err
	}
	if n >= int64(max) {
		return core.ErrConflict.WithMessage(t(ctx, "too many unpaid orders: pay or cancel one first", "待支付订单过多，请先支付或取消"))
	}
	return nil
}

// orderPromo locks and checks the order's promo code; nil without one.
func (s *Service) orderPromo(ctx context.Context, tx *gorm.DB, code string, userID int64, p price) (*model.PromoCode, error) {
	if code == "" {
		return nil, nil
	}
	c, err := lockCheckPromo(ctx, tx, code, userID, p.Amount, s.now())
	if err != nil {
		return nil, err
	}
	if c.refusal != nil {
		return nil, c.refusal
	}
	return c.code, nil
}

// newOrder is the pending order of a quoted top-up: the rate, discount and
// promo bonus are its snapshot.
func (s *Service) newOrder(userID int64, q quoted, promo *model.PromoCode) *model.PaymentOrder {
	p, v := q.price, q.settings
	o := &model.PaymentOrder{OutTradeNo: newTradeNo(), UserID: userID, Amount: p.Amount, PayAmount: p.Pay, Currency: v.Currency,
		Price: v.Price, DiscountRate: &p.Rate, Method: q.method.Type, MethodName: q.method.Name, Gateway: q.method.Gateway,
		ExpiresAt: s.now().Add(time.Duration(v.OrderTimeoutMinutes) * time.Minute)}
	if promo != nil {
		o.PromoCodeID, o.PromoCode, o.Bonus = &promo.ID, promo.Code, promo.BonusAmount
	}
	return o
}

// startPayment asks the order's gateway for the payment, keeps where to
// pay and the merchant on the order, and returns where to pay. The
// addresses come from the site address only.
func (s *Service) startPayment(ctx context.Context, o *model.PaymentOrder, clientIP string) (*createdView, error) {
	gw, ok := s.gateway(o.Gateway)
	if !ok {
		return nil, errors.New("payment gateway " + o.Gateway + " is not available")
	}
	created, err := gw.CreatePayment(ctx, core.PaymentCreate{OutTradeNo: o.OutTradeNo, Amount: o.PayAmount,
		Currency: o.Currency, MethodType: o.Method, Subject: "Top-up " + o.Amount.String() + " USD",
		NotifyURL: s.siteURL(notifyPath + o.Gateway), ReturnURL: s.siteURL(walletPath),
		ExpiresAt: o.ExpiresAt, ClientIP: clientIP})
	if err != nil {
		return nil, err
	}
	img, err := checkCreated(created)
	if err != nil {
		return nil, err
	}
	set := map[string]any{"pay_url": created.PayURL, "qr_code": created.QRCode, "gateway_session_id": created.SessionID,
		"merchant_ref": created.MerchantRef}
	err = store.G(ctx, s.db.Gorm).Model(&model.PaymentOrder{}).Where("id = ? AND status = ?", o.ID, statusPending).Updates(set).Error
	return &createdView{OutTradeNo: o.OutTradeNo, PayURL: created.PayURL, QRImage: img, ExpiresAt: o.ExpiresAt}, err
}

// abandon fails an order whose payment could not be created; nobody was
// given where to pay it, so it is still pending.
func (s *Service) abandon(ctx context.Context, o *model.PaymentOrder, cause error) {
	slog.WarnContext(ctx, "payment: create payment failed", "order", o.OutTradeNo, "gateway", o.Gateway, "err", cause)
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		_, err := moveTo(ctx, tx, o.ID, statusFailed, map[string]any{"fail_reason": "create payment failed"})
		return err
	})
	if err != nil {
		slog.ErrorContext(ctx, "payment: fail abandoned order", "order", o.OutTradeNo, "err", err)
	}
}

// checkCreated refuses a created payment the order cannot keep or the user
// should not be sent to: references wider than their columns, nowhere to
// pay, a pay address that is not an absolute https URL without
// credentials (the wallet opens it), or only a QR code that does not
// render. It returns the QR code's image; an unrenderable code next to a
// good pay address is dropped.
func checkCreated(c *core.PaymentCreated) (string, error) {
	switch {
	case c == nil:
		return "", errNoResult
	case len(c.SessionID) > maxGatewayRef || len(c.MerchantRef) > maxGatewayRef:
		return "", errors.New("payment gateway returned an oversized session id or merchant")
	case c.PayURL == "" && c.QRCode == "":
		return "", errors.New("payment gateway returned nowhere to pay")
	case c.PayURL != "" && !httpsURL(c.PayURL):
		return "", errors.New("payment gateway returned a pay address that is not an absolute https URL")
	}
	img, err := qrImage(c.QRCode)
	switch {
	case err != nil && c.PayURL == "":
		return "", fmt.Errorf("payment gateway returned a QR code that does not render: %w", err)
	case err != nil:
		c.QRCode = "" // the pay address is enough; the order keeps no code that cannot be shown
	}
	return img, nil
}

func httpsURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}
