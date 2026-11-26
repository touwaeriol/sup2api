package payment

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

// createdView is POST /me/topup/orders: where to pay.
type createdView struct {
	OutTradeNo string    `json:"out_trade_no"`
	PayURL     string    `json:"pay_url,omitempty"`
	QRCode     string    `json:"qr_code,omitempty"`
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
	created, err := s.startPayment(ctx, o, clientIP)
	if err != nil {
		s.abandon(context.WithoutCancel(ctx), o, err)
		return nil, core.ErrUnavailable.WithMessage(t(ctx, "the payment gateway is not available, try again later",
			"支付网关暂时不可用，请稍后重试")).WithCause(err)
	}
	return &createdView{OutTradeNo: o.OutTradeNo, PayURL: created.PayURL, QRCode: created.QRCode, ExpiresAt: o.ExpiresAt}, nil
}

// insertOrder checks and writes the order in one transaction under the
// user's advisory lock, so concurrent orders of a user cannot pass the
// pending cap together; the settings row is read under a share lock, so a
// merchant change waits for the order and then sees it.
func (s *Service) insertOrder(ctx context.Context, userID int64, in quoteInput) (*model.PaymentOrder, error) {
	var o *model.PaymentOrder
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		if err := store.XactLock(ctx, tx, "topup:user:"+x.Itoa64(userID)); err != nil {
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

// buildOrder checks in against the settings, the user's pending orders and
// the credit limits, and prices it, the promo code locked when there is one.
func (s *Service) buildOrder(ctx context.Context, tx *gorm.DB, userID int64, in quoteInput) (*model.PaymentOrder, *model.PromoCode, error) {
	v, m, p, err := s.priceTerms(ctx, tx, userID, in)
	if err != nil {
		return nil, nil, err
	}
	promo, err := s.orderPromo(ctx, tx, in.promoCode(), userID, p)
	if err == nil {
		err = s.checkOrderLimits(ctx, tx, userID, p.Amount, promo)
	}
	if err != nil {
		return nil, nil, err
	}
	o, err := s.newOrder(userID, v, m, p, promo)
	return o, promo, err
}

// priceTerms checks in against the settings read under a share lock and the
// user's pending orders, and prices it.
func (s *Service) priceTerms(ctx context.Context, tx *gorm.DB, userID int64, in quoteInput) (Settings, Method, price, error) {
	if err := store.RequireTx(tx); err != nil {
		return Settings{}, Method{}, price{}, err
	}
	v, ms, err := s.terms(ctx, tx, store.Lock(clause.LockingStrengthShare))
	if err != nil {
		return Settings{}, Method{}, price{}, err
	}
	m, err := s.checkMethod(ctx, v, ms, in)
	if err == nil {
		err = s.checkPending(ctx, tx, userID, v.MaxPendingOrders)
	}
	if err != nil {
		return Settings{}, Method{}, price{}, err
	}
	p, err := priceOf(ctx, v, in.Amount.Decimal)
	return v, m, p, err
}

// checkPending refuses an order past the user's cap of pending orders.
func (s *Service) checkPending(ctx context.Context, tx *gorm.DB, userID int64, max int) error {
	var n int64
	err := store.G(ctx, tx).Model(&model.PaymentOrder{}).
		Where("user_id = ? AND status = ? AND expires_at > ?", userID, statusPending, s.now()).Count(&n).Error
	if err == nil && n >= int64(max) {
		err = core.ErrConflict.WithMessage(t(ctx, "too many unpaid orders: pay or cancel one first", "待支付订单过多，请先支付或取消"))
	}
	return err
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

// newOrder is the pending order of a priced top-up: the rate, discount and
// promo bonus are its snapshot.
func (s *Service) newOrder(userID int64, v Settings, m Method, p price, promo *model.PromoCode) (*model.PaymentOrder, error) {
	no, err := newTradeNo()
	if err != nil {
		return nil, err
	}
	o := &model.PaymentOrder{OutTradeNo: no, UserID: userID, Amount: p.Amount, PayAmount: p.Pay, Currency: v.Currency,
		Price: v.Price.Decimal, DiscountRate: &p.Rate, Method: m.Type, MethodName: m.Name, Gateway: m.Gateway,
		ExpiresAt: s.now().Add(time.Duration(v.OrderTimeoutMinutes) * time.Minute)}
	if promo != nil {
		o.PromoCodeID, o.PromoCode, o.Bonus = &promo.ID, promo.Code, promo.BonusAmount
	}
	return o, nil
}

// startPayment asks the order's gateway for the payment and keeps where to
// pay and the merchant on the order. The addresses come from the site
// address only.
func (s *Service) startPayment(ctx context.Context, o *model.PaymentOrder, clientIP string) (*core.PaymentCreated, error) {
	gw, ok := s.gateway(o.Gateway)
	if !ok {
		return nil, errors.New("payment gateway " + o.Gateway + " is not available")
	}
	created, err := gw.CreatePayment(ctx, core.PaymentCreate{OutTradeNo: o.OutTradeNo, Amount: o.PayAmount,
		Currency: o.Currency, MethodType: o.Method, Subject: "Top-up " + o.Amount.String() + " USD",
		NotifyURL: s.siteURL(notifyPath + o.Gateway), ReturnURL: s.siteURL(walletPath),
		ExpiresAt: o.ExpiresAt, ClientIP: clientIP})
	if err == nil {
		err = checkCreated(created)
	}
	if err != nil {
		return nil, err
	}
	o.PayURL, o.QrCode, o.GatewaySessionID, o.MerchantRef = created.PayURL, created.QRCode, created.SessionID, created.MerchantRef
	set := map[string]any{"pay_url": o.PayURL, "qr_code": o.QrCode, "gateway_session_id": o.GatewaySessionID, "merchant_ref": o.MerchantRef}
	err = store.G(ctx, s.db.Gorm).Model(&model.PaymentOrder{}).Where("id = ? AND status = ?", o.ID, statusPending).Updates(set).Error
	return created, err
}

// abandon fails an order whose payment could not be created.
func (s *Service) abandon(ctx context.Context, o *model.PaymentOrder, cause error) {
	slog.WarnContext(ctx, "payment: create payment failed", "order", o.OutTradeNo, "gateway", o.Gateway, "err", cause)
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		ok, err := setStatus(ctx, tx, o.ID, statusFailed, map[string]any{"fail_reason": "create payment failed"}, statusPending)
		if err != nil || !ok {
			return err
		}
		return releaseUse(ctx, tx, o.ID)
	})
	if err != nil {
		slog.ErrorContext(ctx, "payment: fail abandoned order", "order", o.OutTradeNo, "err", err)
	}
}

// checkCreated refuses a created payment the order cannot keep or the user
// should not be sent to: references wider than their columns, nowhere to
// pay, or a pay address that is not an absolute https URL without
// credentials (the wallet opens it).
func checkCreated(c *core.PaymentCreated) error {
	switch {
	case c == nil:
		return errNoResult
	case len(c.SessionID) > maxGatewayRef || len(c.MerchantRef) > maxGatewayRef:
		return errors.New("payment gateway returned an oversized session id or merchant")
	case c.PayURL == "" && c.QRCode == "":
		return errors.New("payment gateway returned nowhere to pay")
	case c.PayURL != "" && !httpsURL(c.PayURL):
		return errors.New("payment gateway returned a pay address that is not an absolute https URL")
	}
	return nil
}

func httpsURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}
