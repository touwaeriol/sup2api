// Package stripe is the built-in Stripe gateway (CONTRACTS §140): payments
// are Checkout Sessions, notifications are signed webhook events.
//
// The package depends only on internal/core and Stripe's official library;
// the payment module wires it in and owns orders, matching and crediting.
package stripe

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	stripego "github.com/stripe/stripe-go/v87"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// Config is the gateway's current settings, read on every call so a key
// change takes effect without rebuilding the gateway.
type Config struct{ SecretKey, WebhookSecret string }

// Gateway implements core.PaymentGateway for Stripe.
type Gateway struct {
	cfg    func(ctx context.Context) (Config, error)
	client *http.Client
	apiURL string // Stripe API base; tests point it at a fake server
	now    func() time.Time
}

var _ core.PaymentGateway = (*Gateway)(nil)

// New returns the Stripe gateway. Every Stripe API request goes through
// client, the core's egress policy; a nil client would make the library fall
// back to its own, so it is refused.
func New(cfg func(ctx context.Context) (Config, error), client *http.Client) *Gateway {
	if cfg == nil || client == nil {
		panic("stripe: New needs a config source and an HTTP client")
	}
	return &Gateway{cfg: cfg, client: client, apiURL: stripego.APIURL, now: time.Now}
}

// Merchant refs: an order created with a test key must not be credited by a
// live event and vice versa, so the core stores the mode as the merchant and
// requires notifications to name the same one.
const (
	merchantLive = "live"
	merchantTest = "test"
)

// keyMode tells whether a secret (sk_) or restricted (rk_) key is live or test.
func keyMode(secretKey string) (string, error) {
	switch {
	case strings.HasPrefix(secretKey, "sk_live_"), strings.HasPrefix(secretKey, "rk_live_"):
		return merchantLive, nil
	case strings.HasPrefix(secretKey, "sk_test_"), strings.HasPrefix(secretKey, "rk_test_"):
		return merchantTest, nil
	}
	return "", errors.New("stripe: secret key must start with sk_live_, sk_test_, rk_live_ or rk_test_")
}

func modeOf(livemode bool) string {
	if livemode {
		return merchantLive
	}
	return merchantTest
}

// quietLogger keeps the library from writing request errors to stderr: they
// are returned to the caller, which decides what to log.
var quietLogger = &stripego.LeveledLogger{Level: stripego.LevelNull}

// api builds a Stripe client for the current key and tells its mode.
// Retries are off: the core's scheduler queries and expires orders again, and
// a request must not sleep behind the caller's back.
func (g *Gateway) api(ctx context.Context) (*stripego.Client, string, error) {
	c, err := g.cfg(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("stripe: load config: %w", err)
	}
	mode, err := keyMode(c.SecretKey)
	if err != nil {
		return nil, "", err
	}
	backends := stripego.NewBackendsWithConfig(&stripego.BackendConfig{
		HTTPClient:        g.client,
		URL:               stripego.String(g.apiURL),
		MaxNetworkRetries: stripego.Int64(0),
		LeveledLogger:     quietLogger,
	})
	return stripego.NewClient(c.SecretKey, stripego.WithBackends(backends)), mode, nil
}

// Respond answers Stripe: any non-2xx makes it retry the event with backoff,
// which is what PaymentRetry wants; a rejection is retried too, but only a
// fixed configuration (or nothing) can change its outcome.
func (g *Gateway) Respond(o core.PaymentOutcome) core.PaymentReply {
	status := http.StatusInternalServerError
	switch o {
	case core.PaymentHandled:
		status = http.StatusOK
	case core.PaymentRejected:
		status = http.StatusBadRequest
	}
	return core.PaymentReply{
		Status:      status,
		ContentType: "text/plain; charset=utf-8",
		Body:        []byte(http.StatusText(status)),
	}
}
