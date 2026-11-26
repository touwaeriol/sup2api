package epay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

// maxQueryResponse caps the order query's body so a broken or hostile
// endpoint cannot exhaust memory; a real answer is a few hundred bytes.
const maxQueryResponse = 1 << 20

// maxRemoteMessage caps how much of the gateway's msg goes into an error.
const maxRemoteMessage = 200

// QueryPayment asks api.php?act=order for the order. Epay forks disagree on
// this endpoint, so anything that is not a recognisable answer wraps
// core.ErrPaymentQueryUnsupported and the core falls back to expiring the
// order. The merchant key travels in the query string (the protocol), so
// neither the URL nor anything derived from it is put into an error.
func (g *Gateway) QueryPayment(ctx context.Context, q core.PaymentQuery) (*core.PaymentResult, error) {
	cfg, err := g.config(ctx)
	if err != nil {
		return nil, err
	}
	if q.OutTradeNo == "" {
		return nil, errors.New("epay: query needs an order number")
	}
	if q.MerchantRef != "" && q.MerchantRef != cfg.MerchantID {
		return nil, unsupported("order belongs to another merchant than the configured one")
	}
	body, err := g.fetchOrder(ctx, cfg, q.OutTradeNo)
	if err != nil {
		return nil, err
	}
	var resp queryResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, unsupported("answer is not the expected JSON")
	}
	return resp.result(cfg, q.OutTradeNo)
}

func unsupported(reason string) error {
	return fmt.Errorf("epay: query %s: %w", reason, core.ErrPaymentQueryUnsupported)
}

func (g *Gateway) fetchOrder(ctx context.Context, cfg Config, outTradeNo string) ([]byte, error) {
	params := map[string]string{"act": "order", "pid": cfg.MerchantID, "key": cfg.Key, "out_trade_no": outTradeNo}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.scriptURL("api.php", params), nil)
	if err != nil {
		return nil, errors.New("epay: query request could not be built")
	}
	res, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("epay: query request failed: %w", withoutURL(err))
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusNotFound, res.StatusCode == http.StatusMethodNotAllowed,
		res.StatusCode == http.StatusNotImplemented:
		return nil, unsupported(fmt.Sprintf("endpoint answered HTTP %d", res.StatusCode))
	case res.StatusCode < 200 || res.StatusCode > 299:
		return nil, fmt.Errorf("epay: query answered HTTP %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxQueryResponse))
	if err != nil {
		return nil, fmt.Errorf("epay: query read failed: %w", withoutURL(err))
	}
	return body, nil
}

// withoutURL drops the *url.Error wrapper, whose text is the full request
// URL including the key; the cause (timeout, refused, context canceled)
// stays matchable with errors.Is.
func withoutURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// queryResponse is the api.php answer, by field name: code, msg and the
// notification's fields (pid, trade_no, out_trade_no, type, money,
// trade_status or the older status, ...). Forks send numbers as strings or
// numbers, hence flexString.
type queryResponse map[string]flexString

func (r queryResponse) result(cfg Config, outTradeNo string) (*core.PaymentResult, error) {
	params := make(map[string]string, len(r))
	for k, v := range r {
		params[k] = string(v)
	}
	if params["code"] != "1" {
		return nil, unsupported(fmt.Sprintf("answered code %q: %q", params["code"], remoteMessage(params["msg"], cfg.Key)))
	}
	if !echoes(params, "out_trade_no", outTradeNo) || !echoes(params, "pid", cfg.MerchantID) {
		return nil, unsupported("answered about another order or merchant")
	}
	var cents int64
	if params["money"] != "" || statusOf(params) == core.PaymentPaid {
		var err error
		if cents, err = parseMoney(params["money"]); err != nil {
			return nil, unsupported(err.Error())
		}
	}
	return resultOf(params, cents), nil
}

// echoes: the answer's field k, which forks may leave out, is want; an
// absent one is filled in, so the result names the order and merchant
// asked about.
func echoes(params map[string]string, k, want string) bool {
	if params[k] == "" {
		params[k] = want
	}
	return params[k] == want
}

// remoteMessage is the gateway's msg made safe for an error: the key is
// masked in case the gateway echoes the request, and the text is capped.
func remoteMessage(msg, key string) string {
	return x.TruncateEllipsis(strings.ReplaceAll(msg, key, core.SecretMask), maxRemoteMessage)
}

// flexString is a JSON string or number as text. Anything else (null, an
// object a fork adds) reads as empty rather than failing the answer: the
// fields the gateway needs are checked where they are read.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*f = flexString(s)
		return nil
	}
	var n json.Number
	if json.Unmarshal(b, &n) == nil {
		*f = flexString(n.String())
	}
	return nil
}
