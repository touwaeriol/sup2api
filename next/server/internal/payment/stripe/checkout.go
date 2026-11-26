package stripe

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	stripego "github.com/stripe/stripe-go/v87"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// metaOutTradeNo carries the order number in the session's metadata as well
// as in client_reference_id; a session whose two copies differ was not
// created by this gateway as sent and is never matched to an order.
const metaOutTradeNo = "out_trade_no"

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
		Mode: stripego.String(string(stripego.CheckoutSessionModePayment)),
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
	return sessionResult(s, queryStatus(s))
}

func queryStatus(s *stripego.CheckoutSession) core.PaymentStatus {
	switch s.Status {
	case stripego.CheckoutSessionStatusComplete:
		return paidIf(s)
	case stripego.CheckoutSessionStatusExpired:
		return core.PaymentExpired
	}
	return core.PaymentPending
}

// paidIf: a completed session is paid only once its payment_status says so;
// delayed methods (bank debits) complete the session first and pay later.
func paidIf(s *stripego.CheckoutSession) core.PaymentStatus {
	if s.PaymentStatus == stripego.CheckoutSessionPaymentStatusPaid {
		return core.PaymentPaid
	}
	return core.PaymentPending
}

// sessionResult maps a session the gateway trusts (signed event or API read)
// to a result. The core matches OutTradeNo and SessionID to the same order
// and compares amount, currency and merchant with the order's snapshot.
func sessionResult(s *stripego.CheckoutSession, status core.PaymentStatus) (*core.PaymentResult, error) {
	if s.Metadata[metaOutTradeNo] != s.ClientReferenceID {
		return nil, rejected("client_reference_id and metadata.%s differ", metaOutTradeNo)
	}
	return &core.PaymentResult{
		OutTradeNo:  s.ClientReferenceID,
		TradeNo:     tradeNo(s),
		SessionID:   s.ID,
		Amount:      s.AmountTotal,
		Currency:    strings.ToUpper(string(s.Currency)),
		Status:      status,
		MerchantRef: modeOf(s.Livemode),
		Fields:      sessionFields(s),
	}, nil
}

// tradeNo is the PaymentIntent, Stripe's id of the money movement; a session
// without one (not paid yet) falls back to the session id.
func tradeNo(s *stripego.CheckoutSession) string {
	if s.PaymentIntent != nil && s.PaymentIntent.ID != "" {
		return s.PaymentIntent.ID
	}
	return s.ID
}

// sessionFields are the non-secret fields kept on the payment_events row:
// no customer details, URLs or metadata beyond what the result carries.
func sessionFields(s *stripego.CheckoutSession) map[string]string {
	return map[string]string{
		"session_status": string(s.Status),
		"payment_status": string(s.PaymentStatus),
		"livemode":       strconv.FormatBool(s.Livemode),
		"amount_total":   strconv.FormatInt(s.AmountTotal, 10),
		"currency":       string(s.Currency),
	}
}
