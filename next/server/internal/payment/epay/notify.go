package epay

import (
	"context"
	"crypto/hmac"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// paramKey is the shape every notification parameter name must have. The
// protocol's fields (pid … sign_type, param) and the extensions forks add
// (api_trade_no, buyer, timestamp, …) all fit it. A parameter outside the
// protocol is accepted rather than refused because every non-empty
// parameter takes part in the signature: with '&' and '=' refused in
// values and repeated names refused, the signed string is unambiguous, so
// a sender cannot add one without the key, while refusing a fork's signed
// extension would reject every payment of that merchant. Only the
// whitelisted fields (recordedFields) are ever stored.
var paramKey = regexp.MustCompile(`^[a-z_]{1,32}$`)

// requiredParams are always sent by Epay; without them the core could not
// match the order, amount, method and trade number.
var requiredParams = []string{"out_trade_no", "trade_no", "type", "money", "trade_status"}

// ParseNotification verifies an Epay notification (GET query string, or a
// POST urlencoded form). Every problem with the notification itself wraps
// core.ErrPaymentSignature so the core rejects it; only a missing or
// invalid configuration returns another error (the core asks to retry).
func (g *Gateway) ParseNotification(ctx context.Context, n core.PaymentNotification) (*core.PaymentResult, error) {
	raw, err := notificationParams(n)
	if err != nil {
		return nil, err
	}
	params, err := parseParams(raw)
	if err != nil {
		return nil, err
	}
	cfg, err := g.config(ctx)
	if err != nil {
		return nil, err
	}
	if err := verify(params, cfg); err != nil {
		return nil, err
	}
	return notificationResult(params)
}

func rejected(reason string) error {
	return fmt.Errorf("epay: notification %s: %w", reason, core.ErrPaymentSignature)
}

// notificationParams is the raw parameter string: the query of a GET, the
// body of a POST form. A POST that also has a query string is refused, so
// there is exactly one source of parameters.
func notificationParams(n core.PaymentNotification) (string, error) {
	switch n.Method {
	case http.MethodGet:
		return n.RawQuery, nil
	case http.MethodPost:
		mt, _, err := mime.ParseMediaType(n.Header.Get("Content-Type"))
		if err != nil || mt != "application/x-www-form-urlencoded" {
			return "", rejected("POST is not an urlencoded form")
		}
		if n.RawQuery != "" {
			return "", rejected("POST also carries a query string")
		}
		return string(n.Body), nil
	default:
		return "", rejected("method is not GET or POST")
	}
}

// parseParams splits the parameters itself instead of using url.ParseQuery,
// which silently keeps repeated keys: a repeated or malformed parameter, a
// name outside paramKey, or a value containing '&' or '=', makes the
// notification ambiguous and is refused.
func parseParams(raw string) (map[string]string, error) {
	if raw == "" {
		return nil, rejected("has no parameters")
	}
	params := make(map[string]string)
	for _, pair := range strings.Split(raw, "&") {
		k, v, err := parsePair(pair)
		if err != nil {
			return nil, err
		}
		if _, dup := params[k]; dup {
			return nil, rejected("repeats parameter " + k)
		}
		params[k] = v
	}
	return params, nil
}

func parsePair(pair string) (string, string, error) {
	rawKey, rawValue, ok := strings.Cut(pair, "=")
	if !ok {
		return "", "", rejected("has a parameter without '='")
	}
	key, kerr := url.QueryUnescape(rawKey)
	value, verr := url.QueryUnescape(rawValue)
	if kerr != nil || verr != nil {
		return "", "", rejected("has a malformed escape")
	}
	// A name of the wrong shape is not echoed: it comes from the request.
	// Names that pass paramKey are short lower-case words, safe to quote.
	if !paramKey.MatchString(key) {
		return "", "", rejected("has a malformed parameter name")
	}
	if strings.ContainsAny(value, "&=") {
		return "", "", rejected("parameter " + key + " contains '&' or '='")
	}
	return key, value, nil
}

// verify checks the signature in constant time and that the notification
// names the configured merchant. A missing pid is refused, never filled in
// from the configuration.
func verify(params map[string]string, cfg Config) error {
	got := strings.ToLower(params["sign"])
	if got == "" {
		return rejected("has no sign")
	}
	if params["sign_type"] != signTypeMD5 {
		return rejected("sign_type is not MD5")
	}
	if !hmac.Equal([]byte(got), []byte(sign(params, cfg.Key))) {
		return rejected("signature does not match")
	}
	if params["pid"] != cfg.MerchantID {
		return rejected("pid is missing or not the configured merchant")
	}
	return nil
}

// notificationResult turns verified parameters into the payment result.
// Only TRADE_SUCCESS is paid; any other status is pending, which the core
// records without crediting.
func notificationResult(params map[string]string) (*core.PaymentResult, error) {
	for _, k := range requiredParams {
		if params[k] == "" {
			return nil, rejected("has no " + k)
		}
	}
	cents, err := parseMoney(params["money"])
	if err != nil {
		return nil, rejected(err.Error())
	}
	status := core.PaymentPending
	if params["trade_status"] == tradeStatusSuccess {
		status = core.PaymentPaid
	}
	return &core.PaymentResult{
		OutTradeNo: params["out_trade_no"], TradeNo: params["trade_no"],
		Amount: cents, Currency: currencyCNY, Status: status,
		MerchantRef: params["pid"], MethodType: params["type"],
		Fields: fields(params),
	}, nil
}
