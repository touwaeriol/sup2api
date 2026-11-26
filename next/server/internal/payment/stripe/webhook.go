package stripe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	stripego "github.com/stripe/stripe-go/v87"
	"github.com/stripe/stripe-go/v87/webhook"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

const (
	signatureHeader     = "Stripe-Signature"
	checkoutEventPrefix = "checkout.session."
	checkoutObject      = "checkout.session"
)

// eventOptions: the 300 s tolerance rejects replays of old signed events.
// The API version check is off because a webhook endpoint sends events in
// its own (often the account's default) version; the fields read here (event
// id, type, livemode and the session's id, status, payment_status,
// amount_total, currency, client_reference_id, metadata, payment_intent) are
// the same in every version, and enforcing the library's release train
// would reject every event of an endpoint created on another one.
var eventOptions = webhook.ConstructEventOptions{
	Tolerance:                webhook.DefaultTolerance,
	IgnoreAPIVersionMismatch: true,
}

// ParseNotification verifies a webhook event and maps it to a result. Events
// other than the three checkout outcomes come back pending, so the core only
// records them.
func (g *Gateway) ParseNotification(ctx context.Context, n core.PaymentNotification) (*core.PaymentResult, error) {
	c, err := g.cfg(ctx)
	if err != nil {
		return nil, fmt.Errorf("stripe: load config: %w", err)
	}
	ev, err := verifyEvent(c, n)
	if err != nil {
		return nil, err
	}
	return eventResult(ev)
}

func rejected(format string, args ...any) error {
	return fmt.Errorf("%w: stripe: "+format, append([]any{core.ErrPaymentSignature}, args...)...)
}

// verifyEvent checks the signature and that the event comes from the same
// mode as the key: a test-mode payment must never credit a live balance.
func verifyEvent(c Config, n core.PaymentNotification) (stripego.Event, error) {
	header := n.Header.Get(signatureHeader)
	switch {
	case n.Method != http.MethodPost:
		return stripego.Event{}, rejected("method %s is not POST", n.Method)
	case c.WebhookSecret == "":
		return stripego.Event{}, rejected("webhook secret is not configured")
	case header == "":
		return stripego.Event{}, rejected("missing %s header", signatureHeader)
	}
	mode, err := keyMode(c.SecretKey)
	if err != nil {
		return stripego.Event{}, fmt.Errorf("%w: %w", core.ErrPaymentSignature, err)
	}
	ev, err := webhook.ConstructEventWithOptions(n.Body, header, c.WebhookSecret, eventOptions)
	if err != nil {
		return stripego.Event{}, rejected("%v", err)
	}
	if modeOf(ev.Livemode) != mode {
		return stripego.Event{}, rejected("event livemode=%t does not match the %s key", ev.Livemode, mode)
	}
	return ev, nil
}

func eventResult(ev stripego.Event) (*core.PaymentResult, error) {
	if !strings.HasPrefix(string(ev.Type), checkoutEventPrefix) {
		return &core.PaymentResult{
			Status:      core.PaymentPending,
			EventID:     ev.ID,
			MerchantRef: modeOf(ev.Livemode),
			Fields:      map[string]string{"event_type": string(ev.Type)},
		}, nil
	}
	s, err := eventSession(ev)
	if err != nil {
		return nil, err
	}
	r, err := sessionResult(s, eventStatus(ev.Type, s))
	if err != nil {
		return nil, err
	}
	r.EventID = ev.ID
	r.Fields["event_type"] = string(ev.Type)
	return r, nil
}

// eventSession decodes the event's Checkout Session. A signed event that
// does not carry one is malformed and will not get better on a retry.
func eventSession(ev stripego.Event) (*stripego.CheckoutSession, error) {
	var s stripego.CheckoutSession
	if ev.Data == nil || json.Unmarshal(ev.Data.Raw, &s) != nil || s.Object != checkoutObject {
		return nil, rejected("event %s does not carry a checkout session", ev.ID)
	}
	return &s, nil
}

func eventStatus(t stripego.EventType, s *stripego.CheckoutSession) core.PaymentStatus {
	switch t {
	case stripego.EventTypeCheckoutSessionCompleted, stripego.EventTypeCheckoutSessionAsyncPaymentSucceeded:
		return paidIf(s)
	case stripego.EventTypeCheckoutSessionExpired:
		return core.PaymentExpired
	}
	return core.PaymentPending
}
