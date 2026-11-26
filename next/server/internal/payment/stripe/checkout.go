package stripe

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	stripego "github.com/stripe/stripe-go/v87"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// Stripe accepts expires_at from 30 minutes to 24 hours after it creates the
// session. The minute of margin on each side absorbs request latency and
// clock skew, so a timestamp at the edge is not refused.
const (
	minSessionLife = 30*time.Minute + time.Minute
	maxSessionLife = 24*time.Hour - time.Minute
)

// CreatePayment opens a Checkout Session for the order. The price is the
// order's server-side amount (price_data), never a dashboard Price, and
// promotion codes stay off so the paid amount always equals the order's.
// Only cards are offered (allowed_payment_method_types, which replaced
// payment_method_types in the pinned API version; Apple Pay and Google Pay
// are cards): delayed methods (bank debits, vouchers) settle days later,
// after the core stops accepting payments for an expired order, and would
// be charged without being credited.
func (g *Gateway) CreatePayment(ctx context.Context, in core.PaymentCreate) (*core.PaymentCreated, error) {
	amount, ok := core.PaymentMinorUnits(in.Amount, in.Currency)
	if !ok {
		return nil, fmt.Errorf("stripe: amount %s is not payable in %s", in.Amount, in.Currency)
	}
	sc, mode, err := g.api(ctx)
	if err != nil {
		return nil, err
	}
	s, err := sc.V1CheckoutSessions.Create(ctx, g.sessionParams(in, amount))
	if err != nil {
		return nil, fmt.Errorf("stripe: create checkout session: %w", err)
	}
	return &core.PaymentCreated{PayURL: s.URL, SessionID: s.ID, MerchantRef: mode}, nil
}

func (g *Gateway) sessionParams(in core.PaymentCreate, amount int64) *stripego.CheckoutSessionCreateParams {
	return &stripego.CheckoutSessionCreateParams{
		Mode:                      stripego.String(string(stripego.CheckoutSessionModePayment)),
		AllowedPaymentMethodTypes: []*string{stripego.String("card")},
		LineItems: []*stripego.CheckoutSessionCreateLineItemParams{{
			PriceData: &stripego.CheckoutSessionCreateLineItemPriceDataParams{
				Currency:    stripego.String(strings.ToLower(in.Currency)),
				UnitAmount:  stripego.Int64(amount),
				ProductData: &stripego.CheckoutSessionCreateLineItemPriceDataProductDataParams{Name: stripego.String(in.Subject)},
			},
			Quantity: stripego.Int64(1),
		}},
		ClientReferenceID: stripego.String(in.OutTradeNo),
		Metadata:          map[string]string{metaOutTradeNo: in.OutTradeNo},
		SuccessURL:        stripego.String(in.ReturnURL),
		CancelURL:         stripego.String(in.ReturnURL),
		ExpiresAt:         stripego.Int64(g.expiresAt(in.ExpiresAt).Unix()),
	}
}

// expiresAt clamps the order's expiry into the window Stripe accepts. An
// order that expires sooner keeps a session open past its expiry; the core
// accepts payments for expired orders, so such a payment is still credited.
func (g *Gateway) expiresAt(want time.Time) time.Time {
	now := g.now()
	if lo := now.Add(minSessionLife); want.Before(lo) {
		return lo
	}
	if hi := now.Add(maxSessionLife); want.After(hi) {
		return hi
	}
	return want
}

// QueryPayment reads the order's Checkout Session.
func (g *Gateway) QueryPayment(ctx context.Context, q core.PaymentQuery) (*core.PaymentResult, error) {
	if q.SessionID == "" {
		return nil, errors.New("stripe: order has no checkout session")
	}
	sc, _, err := g.api(ctx)
	if err != nil {
		return nil, err
	}
	s, err := sc.V1CheckoutSessions.Retrieve(ctx, q.SessionID, nil)
	if err != nil {
		return nil, fmt.Errorf("stripe: retrieve checkout session: %w", err)
	}
	ss := sessionOf(s)
	return sessionResult(ss, queryStatus(ss))
}

func queryStatus(s session) core.PaymentStatus {
	switch stripego.CheckoutSessionStatus(s.Status) {
	case stripego.CheckoutSessionStatusComplete:
		return paidIf(s)
	case stripego.CheckoutSessionStatusExpired:
		return core.PaymentExpired
	}
	return core.PaymentPending
}

// paidIf: a completed session is paid only once its payment_status says so;
// delayed methods complete the session first and pay later.
func paidIf(s session) core.PaymentStatus {
	if stripego.CheckoutSessionPaymentStatus(s.PaymentStatus) == stripego.CheckoutSessionPaymentStatusPaid {
		return core.PaymentPaid
	}
	return core.PaymentPending
}
