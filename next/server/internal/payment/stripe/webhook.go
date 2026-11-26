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
	eventObject         = "event"
)

// event is the part of a webhook event the gateway reads. The payload is
// verified with webhook.ValidatePayloadWithTolerance and decoded into this
// rather than through ConstructEvent: the endpoint sends events in its own
// API version (often the account's default), and these fields have the
// same shape in every version, so no version check is needed and no field
// the gateway does not read can make an authentic event undecodable.
type event struct {
	ID       string `json:"id"`
	Object   string `json:"object"`
	Type     string `json:"type"`
	Livemode bool   `json:"livemode"`
	Data     struct {
		Object json.RawMessage `json:"object"`
	} `json:"data"`
}

// ParseNotification verifies a webhook event and maps it to a result. Events
// other than the two checkout outcomes come back pending, so the core only
// records them. The request's shape is checked before the configuration is
// read, so junk requests to this public route cost no settings read. Stripe
// reads only the reply's status (core.StatusPaymentReply): any non-2xx makes
// it retry the event with backoff, a rejection too, though only a fixed
// configuration (or nothing) can change its outcome.
func (g *Gateway) ParseNotification(ctx context.Context, n core.PaymentNotification) (*core.PaymentResult, core.PaymentResponder, error) {
	res, err := g.parse(ctx, n)
	return res, core.StatusPaymentReply, err
}

func (g *Gateway) parse(ctx context.Context, n core.PaymentNotification) (*core.PaymentResult, error) {
	header, err := signatureOf(n)
	if err != nil {
		return nil, err
	}
	c, mode, err := g.config(ctx)
	if err != nil {
		return nil, err
	}
	ev, err := verifyEvent(c.WebhookSecret, mode, n.Body, header)
	if err != nil {
		return nil, err
	}
	return eventResult(ev)
}

func rejected(format string, args ...any) error {
	return fmt.Errorf("stripe: "+format+": %w", append(args, core.ErrPaymentSignature)...)
}

// signatureOf is the Stripe-Signature header of a POST.
func signatureOf(n core.PaymentNotification) (string, error) {
	if n.Method != http.MethodPost {
		return "", rejected("method %s is not POST", n.Method)
	}
	header := n.Header.Get(signatureHeader)
	if header == "" {
		return "", rejected("missing %s header", signatureHeader)
	}
	return header, nil
}

// verifyEvent checks the signature (any of the header's v1 signatures, its
// timestamp within 300 s against replays) and that the event comes from the
// same mode as the key: a test-mode payment must never credit a live
// balance.
func verifyEvent(secret, mode string, body []byte, header string) (event, error) {
	if err := webhook.ValidatePayloadWithTolerance(body, header, secret, webhook.DefaultTolerance); err != nil {
		return event{}, rejected("%v", err)
	}
	var ev event
	if json.Unmarshal(body, &ev) != nil || ev.Object != eventObject || ev.ID == "" {
		return event{}, rejected("signed payload is not an event")
	}
	if modeOf(ev.Livemode) != mode {
		return event{}, rejected("event livemode=%t does not match the %s key", ev.Livemode, mode)
	}
	return ev, nil
}

func eventResult(ev event) (*core.PaymentResult, error) {
	if !strings.HasPrefix(ev.Type, checkoutEventPrefix) {
		return &core.PaymentResult{
			Status:      core.PaymentPending,
			EventID:     ev.ID,
			MerchantRef: modeOf(ev.Livemode),
			Fields:      map[string]string{"event_type": ev.Type},
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
	r.Fields["event_type"] = ev.Type
	return r, nil
}

// eventSession decodes the event's Checkout Session. A signed event that
// does not carry one is malformed and will not get better on a retry.
func eventSession(ev event) (session, error) {
	var s session
	if json.Unmarshal(ev.Data.Object, &s) != nil || s.Object != checkoutObject {
		return session{}, rejected("event %s does not carry a checkout session", ev.ID)
	}
	return s, nil
}

func eventStatus(t string, s session) core.PaymentStatus {
	switch stripego.EventType(t) {
	case stripego.EventTypeCheckoutSessionCompleted:
		return paidIf(s)
	case stripego.EventTypeCheckoutSessionExpired:
		return core.PaymentExpired
	}
	return core.PaymentPending
}
