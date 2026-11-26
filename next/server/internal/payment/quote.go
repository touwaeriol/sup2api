package payment

import (
	"context"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// quoteInput is the body of POST /me/topup/preview and /me/topup/orders:
// all a client chooses. Everything else comes from the settings.
type quoteInput struct {
	Amount    Num    `json:"amount"`
	Method    string `json:"method"`
	PromoCode string `json:"promo_code"`
}

func (in quoteInput) promoCode() string { return strings.ToUpper(strings.TrimSpace(in.PromoCode)) }

// price is what a top-up costs: the amount credited (balance units), the
// discount, and what the user pays in the currency, exact in its minor unit.
type price struct {
	Amount   decimal.Decimal
	Rate     decimal.Decimal
	Original decimal.Decimal
	Pay      decimal.Decimal
}

// amountDecimals is the precision of an amount a user tops up.
const amountDecimals = 2

// checkMethod returns the method in chose when top-up is open, the method
// and its gateway take new orders, and the amount is within the global and
// the method's limits.
func (s *Service) checkMethod(ctx context.Context, v Settings, ms []Method, in quoteInput) (Method, error) {
	if !s.usable(v) {
		return Method{}, errUnavailable(ctx)
	}
	m := findMethod(ms, in.Method)
	if m == nil || !m.Enabled || !s.gatewayOpen(v, m.Gateway) {
		return Method{}, core.ErrInvalidArgument.WithMessage(t(ctx, "this payment method is not available", "支付方式不可用"))
	}
	low := decimal.Max(v.MinTopup.Decimal, m.MinTopup.Decimal)
	a := in.Amount.Decimal
	if !a.IsPositive() || !exact(a, amountDecimals) || a.LessThan(low) || a.GreaterThan(v.MaxTopup.Decimal) {
		return Method{}, core.InvalidFields(core.FieldError{Field: "amount", Code: "invalid",
			Message: t(ctx, "the amount must be from "+low.String()+" to "+v.MaxTopup.String()+", at most 2 decimals",
				"充值金额须在 "+low.String()+" 到 "+v.MaxTopup.String()+" 之间，最多 2 位小数")})
	}
	return *m, nil
}

// priceOf prices amount under v: the lowest rate among the discount tiers
// it reaches, the payment rounded to the currency's minor unit.
func priceOf(ctx context.Context, v Settings, amount decimal.Decimal) (price, error) {
	rate := decimal.NewFromInt(1)
	for _, d := range v.Discounts {
		if !amount.LessThan(d.Amount.Decimal) && d.Rate.LessThan(rate) {
			rate = d.Rate.Decimal
		}
	}
	exp := core.PaymentExponent(v.Currency)
	base := amount.Mul(v.Price.Decimal)
	p := price{Amount: amount, Rate: rate, Original: base.Round(exp), Pay: base.Mul(rate).Round(exp)}
	if _, ok := core.PaymentMinorUnits(p.Pay, v.Currency); !ok {
		return price{}, core.InvalidFields(core.FieldError{Field: "amount", Code: "invalid",
			Message: t(ctx, "the amount is too small to pay", "充值金额太小，无法支付")})
	}
	return p, nil
}

// previewView is POST /me/topup/preview (the console's OrderPreview).
type previewView struct {
	Amount            Num    `json:"amount"`
	Bonus             Num    `json:"bonus"`
	DiscountRate      Num    `json:"discount_rate"`
	PayAmount         Num    `json:"pay_amount"`
	OriginalPayAmount Num    `json:"original_pay_amount"`
	PromoError        string `json:"promo_error,omitempty"`
}

// quote prices in for userID without reserving anything; a promo code that
// does not apply is reported in PromoError, not as a failure.
func (s *Service) quoteFor(ctx context.Context, userID int64, in quoteInput) (*previewView, error) {
	v, ms, err := s.terms(ctx, s.db.Gorm)
	if err != nil {
		return nil, err
	}
	if _, err := s.checkMethod(ctx, v, ms, in); err != nil {
		return nil, err
	}
	p, err := priceOf(ctx, v, in.Amount.Decimal)
	if err != nil {
		return nil, err
	}
	out := &previewView{Amount: num(p.Amount), DiscountRate: num(p.Rate), PayAmount: num(p.Pay), OriginalPayAmount: num(p.Original)}
	if code := in.promoCode(); code != "" {
		promo, err := checkPromo(ctx, s.db.Gorm, code, userID, p.Amount, s.now())
		if err != nil {
			return nil, err
		}
		out.Bonus, out.PromoError = promoOutcome(ctx, promo)
	}
	return out, nil
}
