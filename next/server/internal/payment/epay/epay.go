// Package epay is the built-in Epay (易支付) payment gateway (CONTRACTS §140):
// it signs submit.php redirects, verifies the asynchronous notifications and
// asks api.php for an order's status. It only answers what a notification
// says; the payment module checks the order, the amount and the merchant
// against its own records and credits.
package epay

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// ID is the gateway's id in the payment module.
const ID = "epay"

const (
	// Epay only settles in yuan: amounts are formatted and parsed in the CNY
	// minor unit.
	currencyCNY        = "CNY"
	signTypeMD5        = "MD5"
	tradeStatusSuccess = "TRADE_SUCCESS"
)

// Config is the merchant the gateway signs as. Endpoint is the gateway's
// base address (https://pay.example.com, optionally with a path), MerchantID
// the pid.
type Config struct {
	Endpoint   string
	MerchantID string
	Key        string
}

// Errors of Validate, one per field, so the settings can report each on
// its own field (errors.Is on the joined error).
var (
	ErrEndpoint   = errors.New("epay: endpoint must be an https URL without credentials, a query or a fragment")
	ErrMerchantID = errors.New("epay: merchant id is required and must not contain '&' or '='")
	ErrKey        = errors.New("epay: key is required")
)

// Validate reports whether the configuration can be used. Settings reuse it
// so a bad endpoint is refused when saved, not when a user pays.
func (c Config) Validate() error {
	var errs []error
	if u, err := url.Parse(c.Endpoint); err != nil || u.Scheme != "https" || u.Host == "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		errs = append(errs, ErrEndpoint)
	}
	// The pid is echoed in every notification, which refuses '&' and '='.
	if strings.TrimSpace(c.MerchantID) == "" || strings.ContainsAny(c.MerchantID, "&=") {
		errs = append(errs, ErrMerchantID)
	}
	if c.Key == "" {
		errs = append(errs, ErrKey)
	}
	return errors.Join(errs...)
}

// scriptURL is the endpoint's script with params encoded (sorted by key).
func (c Config) scriptURL(script string, params map[string]string) string {
	values := make(url.Values, len(params))
	for k, v := range params {
		values.Set(k, v)
	}
	return strings.TrimRight(c.Endpoint, "/") + "/" + script + "?" + values.Encode()
}

// Gateway implements core.PaymentGateway for one Epay merchant.
type Gateway struct {
	cfg    func(ctx context.Context) (Config, error)
	client *http.Client
}

var _ core.PaymentGateway = (*Gateway)(nil)

// New returns the gateway. cfg is called on every operation so a settings
// change applies without rebuilding the gateway; client sends the order
// queries.
func New(cfg func(ctx context.Context) (Config, error), client *http.Client) *Gateway {
	if cfg == nil || client == nil {
		panic("epay: New needs a config source and an HTTP client")
	}
	return &Gateway{cfg: cfg, client: client}
}

// config is the current, validated configuration. Its errors are not
// ErrPaymentSignature: an unconfigured gateway is the site's problem, so
// the core asks the sender to retry instead of rejecting the notification
// (the Stripe gateway does the same).
func (g *Gateway) config(ctx context.Context) (Config, error) {
	c, err := g.cfg(ctx)
	if err != nil {
		return Config{}, fmt.Errorf("epay: load config: %w", err)
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// respond answers a notification the way Epay expects: the literal
// "success" stops its retries, anything else makes it retry later.
func respond(o core.PaymentOutcome) core.PaymentReply {
	if o == core.PaymentHandled {
		return core.PlainPaymentReply(o.HTTPStatus(), "success")
	}
	return core.PlainPaymentReply(o.HTTPStatus(), "fail")
}
