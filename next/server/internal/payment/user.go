package payment

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// checkoutView is GET /me/topup: what the wallet's top-up card shows. No
// gateway, merchant or secret is in it.
type checkoutView struct {
	Enabled          bool              `json:"enabled"`
	Currency         string            `json:"currency"`
	CurrencyExponent int32             `json:"currency_exponent"`
	Price            decimal.Decimal   `json:"price"`
	MinTopup         decimal.Decimal   `json:"min_topup"`
	MaxTopup         decimal.Decimal   `json:"max_topup"`
	AmountOptions    []decimal.Decimal `json:"amount_options"`
	Discounts        []Discount        `json:"discounts"`
	Methods          []checkoutMethod  `json:"methods"`
}

type checkoutMethod struct {
	Type     string          `json:"type"`
	Name     string          `json:"name"`
	Icon     string          `json:"icon"`
	Color    string          `json:"color,omitempty"`
	Logo     string          `json:"logo,omitempty"`
	MinTopup decimal.Decimal `json:"min_topup"`
}

// checkout lists the methods that take orders now; top-up shows as enabled
// only when it is open and has at least one.
func (s *Service) checkout(c *gin.Context) {
	v, ms, err := s.terms(c.Request.Context(), s.db.Gorm)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	out := checkoutView{Currency: v.Currency, CurrencyExponent: core.PaymentExponent(v.Currency), Price: v.Price,
		MinTopup: v.MinTopup, MaxTopup: v.MaxTopup, AmountOptions: v.AmountOptions, Discounts: v.Discounts, Methods: []checkoutMethod{}}
	usable := s.usable(v)
	for _, m := range ms {
		if usable && s.methodOpen(v, m) {
			out.Methods = append(out.Methods, checkoutMethod{Type: m.Type, Name: m.Name, Icon: m.Icon, Color: m.Color, Logo: m.Logo, MinTopup: m.MinTopup})
		}
	}
	out.Enabled = len(out.Methods) > 0
	httpapi.NoStore(c)
	httpapi.OK(c, out)
}

// admit counts one request of the caller against rpm on a (core.Admit),
// rendering the refusal; false: the caller stops.
func admit(c *gin.Context, a core.Admitter, rpm int) bool {
	ctx := c.Request.Context()
	uid, _ := core.UserID(ctx)
	if err := core.Admit(ctx, a, uid, rpm, t(ctx, "too many requests, try again later", "请求过于频繁，请稍后再试")); err != nil {
		httpapi.Fail(c, err)
		return false
	}
	return true
}

func (s *Service) preview(c *gin.Context) {
	var in quoteInput
	if !admit(c, s.quoteLimit, quoteRatePerMinute) || !httpapi.BindJSON(c, &in) {
		return
	}
	uid, _ := core.UserID(c.Request.Context())
	out, err := s.previewFor(c.Request.Context(), uid, in)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, out)
}

func (s *Service) createOrder(c *gin.Context) {
	var in quoteInput
	if !admit(c, s.orderLimit, orderRatePerMinute) || !httpapi.BindJSON(c, &in) {
		return
	}
	uid, _ := core.UserID(c.Request.Context())
	out, err := s.placeOrder(c.Request.Context(), uid, in, c.ClientIP())
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.Created(c, out)
}

// myOrders lists the caller's orders, newest first: q finds an order
// number, status filters.
func (s *Service) myOrders(c *gin.Context) {
	ctx := c.Request.Context()
	uid, _ := core.UserID(ctx)
	var conds store.Conds
	conds.And("user_id = ?", uid)
	if q := c.Query("q"); q != "" {
		conds.And("out_trade_no ILIKE ?", "%"+store.Like(q)+"%")
	}
	if st := c.Query("status"); st != "" {
		conds.And("status = ?", st)
	}
	rows, page, err := s.pageOrders(c, conds)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	out := make([]orderView, len(rows))
	for i := range rows {
		out[i] = viewOwn(&rows[i], s.now())
	}
	httpapi.List(c, out, page)
}

// myOrder reads one of the caller's orders. Only ?qr=1 ("continue paying")
// renders its QR code: the waiting dialog polls the order every few seconds
// and has the image already, so a poll costs no PNG.
func (s *Service) myOrder(c *gin.Context) {
	ctx := c.Request.Context()
	uid, _ := core.UserID(ctx)
	var o model.PaymentOrder
	err := store.G(ctx, s.db.Gorm).Where("out_trade_no = ? AND user_id = ?", c.Param("no"), uid).Take(&o).Error
	if store.IsNoRows(err) {
		err = errNotFound(ctx)
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	view := viewOwn
	if c.Query("qr") == "1" {
		view = viewPaying
	}
	httpapi.NoStore(c)
	httpapi.OK(c, view(&o, s.now()))
}

func (s *Service) cancelOrder(c *gin.Context) {
	ctx := c.Request.Context()
	uid, _ := core.UserID(ctx)
	o, err := s.cancel(ctx, uid, c.Param("no"))
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, viewOwn(o, s.now()))
}

// cancel cancels the user's pending order, which releases its promo use;
// an order of someone else is not found.
func (s *Service) cancel(ctx context.Context, userID int64, no string) (*model.PaymentOrder, error) {
	var o *model.PaymentOrder
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		var err error
		if o, err = lockOrder(ctx, tx, numbered(no)); err != nil {
			return err
		}
		if o == nil || o.UserID != userID {
			return errNotFound(ctx)
		}
		ok, err := moveTo(ctx, tx, o.ID, statusCancelled, nil)
		if err != nil {
			return err
		}
		if !ok {
			return core.ErrConflict.WithMessage(t(ctx, "only an unpaid order can be cancelled", "只能取消待支付的订单"))
		}
		cancelled := statusCancelled
		o.Status, o.UpdatedAt = &cancelled, s.now()
		return nil
	})
	return o, err
}
