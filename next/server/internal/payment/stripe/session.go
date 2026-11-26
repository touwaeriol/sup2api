package stripe

import (
	"encoding/json"
	"strconv"
	"strings"

	stripego "github.com/stripe/stripe-go/v87"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// metaOutTradeNo carries the order number in the session's metadata as well
// as in client_reference_id (see orderRef).
const metaOutTradeNo = "out_trade_no"

// session is the part of a Checkout Session the gateway reads. Webhook
// events arrive in the endpoint's API version, not the library's, so they
// are decoded into this instead of stripego.CheckoutSession: these fields
// have the same shape in every version, while a type change in any other
// field would make decoding the library's full struct fail and the event
// be refused.
type session struct {
	ID                string            `json:"id"`
	Object            string            `json:"object"`
	Status            string            `json:"status"`
	PaymentStatus     string            `json:"payment_status"`
	AmountTotal       int64             `json:"amount_total"`
	Currency          string            `json:"currency"`
	ClientReferenceID string            `json:"client_reference_id"`
	Metadata          map[string]string `json:"metadata"`
	PaymentIntent     expandableID      `json:"payment_intent"`
	Livemode          bool              `json:"livemode"`
}

// sessionOf takes the same fields from a session the API returned (in the
// library's pinned version, so its full struct decodes).
func sessionOf(s *stripego.CheckoutSession) session {
	out := session{
		ID: s.ID, Object: s.Object, Status: string(s.Status), PaymentStatus: string(s.PaymentStatus),
		AmountTotal: s.AmountTotal, Currency: string(s.Currency), ClientReferenceID: s.ClientReferenceID,
		Metadata: s.Metadata, Livemode: s.Livemode,
	}
	if s.PaymentIntent != nil {
		out.PaymentIntent = expandableID(s.PaymentIntent.ID)
	}
	return out
}

// expandableID is a Stripe reference sent as an id, an expanded object
// with an id, or null.
type expandableID string

func (e *expandableID) UnmarshalJSON(b []byte) error {
	var id string
	if err := json.Unmarshal(b, &id); err == nil {
		*e = expandableID(id)
		return nil
	}
	var obj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	*e = expandableID(obj.ID)
	return nil
}

// sessionResult maps a session the gateway trusts (signed event or API read)
// to a result. The core matches OutTradeNo and SessionID to the same order
// and compares amount, currency and merchant with the order's snapshot.
func sessionResult(s session, status core.PaymentStatus) (*core.PaymentResult, error) {
	ref, err := orderRef(s)
	if err != nil {
		return nil, err
	}
	return &core.PaymentResult{
		OutTradeNo:  ref,
		TradeNo:     tradeNo(s),
		SessionID:   s.ID,
		Amount:      s.AmountTotal,
		Currency:    strings.ToUpper(s.Currency),
		Status:      status,
		MerchantRef: modeOf(s.Livemode),
		Fields:      sessionFields(s),
	}, nil
}

// orderRef is the order a session pays. This gateway always sets both
// client_reference_id and metadata.out_trade_no. A session without the
// metadata was made by something else on the same Stripe account (a Payment
// Link, another integration) and is about no order here: an empty order
// number lets the core acknowledge it, where a rejection would make Stripe
// retry it for days and eventually disable the endpoint. Both present but
// different means the session is not as this gateway created it.
func orderRef(s session) (string, error) {
	meta := s.Metadata[metaOutTradeNo]
	switch {
	case meta == "":
		return "", nil
	case meta != s.ClientReferenceID:
		return "", rejected("client_reference_id and metadata.%s differ", metaOutTradeNo)
	}
	return meta, nil
}

// tradeNo is the PaymentIntent, Stripe's id of the money movement; a session
// without one (not paid yet) falls back to the session id.
func tradeNo(s session) string {
	if s.PaymentIntent != "" {
		return string(s.PaymentIntent)
	}
	return s.ID
}

// sessionFields are the non-secret fields kept on the payment_events row:
// no customer details, URLs or metadata beyond what the result carries.
func sessionFields(s session) map[string]string {
	return map[string]string{
		"session_status": s.Status,
		"payment_status": s.PaymentStatus,
		"livemode":       strconv.FormatBool(s.Livemode),
		"amount_total":   strconv.FormatInt(s.AmountTotal, 10),
		"currency":       s.Currency,
	}
}
