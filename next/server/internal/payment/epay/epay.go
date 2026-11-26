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

const (
	// Epay only settles in yuan: amounts are formatted and parsed in the CNY
	// minor unit.
	currencyCNY        = "CNY"
	signTypeMD5        = "MD5"
	tradeStatusSuccess = "TRADE_SUCCESS"
	replyContentType   = "text/plain; charset=utf-8"
)

// Config is the merchant the gateway signs as. Endpoint is the gateway's
// base address (https://pay.example.com, optionally with a path), MerchantID
// the pid.
type Config struct {
	Endpoint   string
	MerchantID string
	Key        string
}

// Validate reports whether the configuration can be used. Settings reuse it
// so a bad endpoint is refused when saved, not when a user pays.
func (c Config) Validate() error {
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("epay: endpoint must be an https URL")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("epay: endpoint must not carry credentials, a query or a fragment")
	}
	if c.MerchantID == "" || c.Key == "" {
		return errors.New("epay: merchant id and key are required")
	}
	// The pid is echoed in every notification, which refuses '&' and '='.
	if strings.ContainsAny(c.MerchantID, "&=") {
		return errors.New("epay: merchant id must not contain '&' or '='")
	}
	return nil
}

// scriptURL is the endpoint's script with params encoded (sorted by key).
func (c Config) scriptURL(script string, params url.Values) string {
	return strings.TrimRight(c.Endpoint, "/") + "/" + script + "?" + params.Encode()
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
// the core asks the sender to retry instead of rejecting the notification.
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

// Respond answers a notification the way Epay expects: the literal
// "success" stops its retries, anything else makes it retry later.
func (g *Gateway) Respond(o core.PaymentOutcome) core.PaymentReply {
	switch o {
	case core.PaymentHandled:
		return reply(http.StatusOK, "success")
	case core.PaymentRejected:
		return reply(http.StatusBadRequest, "fail")
	default:
		return reply(http.StatusServiceUnavailable, "fail")
	}
}

func reply(status int, body string) core.PaymentReply {
	return core.PaymentReply{Status: status, ContentType: replyContentType, Body: []byte(body)}
}
