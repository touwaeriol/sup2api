package epay

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// CreatePayment signs a submit.php redirect. Nothing is sent to the
// gateway: the user's browser opens PayURL and Epay creates its own order
// then, so there is no session id and the trade number arrives with the
// notification.
func (g *Gateway) CreatePayment(ctx context.Context, in core.PaymentCreate) (*core.PaymentCreated, error) {
	cfg, err := g.config(ctx)
	if err != nil {
		return nil, err
	}
	params, err := submitParams(cfg.MerchantID, in)
	if err != nil {
		return nil, err
	}
	params["sign"] = sign(params, cfg.Key)
	params["sign_type"] = signTypeMD5
	values := make(url.Values, len(params))
	for k, v := range params {
		values.Set(k, v)
	}
	return &core.PaymentCreated{PayURL: cfg.scriptURL("submit.php", values), MerchantRef: cfg.MerchantID}, nil
}

// echoedParams come back in the notification, whose parser refuses empty
// required values and values with '&' or '=': an order that could never be
// verified is refused here instead of being paid and stuck.
var echoedParams = []string{"type", "out_trade_no", "name", "money"}

func submitParams(pid string, in core.PaymentCreate) (map[string]string, error) {
	if in.Currency != currencyCNY {
		return nil, fmt.Errorf("epay: currency %q is not supported, only CNY", in.Currency)
	}
	cents, ok := core.PaymentMinorUnits(in.Amount, currencyCNY)
	if !ok {
		return nil, errors.New("epay: amount must be positive and whole fen")
	}
	if in.NotifyURL == "" || in.ReturnURL == "" {
		return nil, errors.New("epay: notify and return URLs are required")
	}
	params := map[string]string{
		"pid":          pid,
		"type":         in.MethodType,
		"out_trade_no": in.OutTradeNo,
		"notify_url":   in.NotifyURL,
		"return_url":   in.ReturnURL,
		"name":         in.Subject,
		"money":        fmt.Sprintf("%d.%02d", cents/100, cents%100),
	}
	for _, k := range echoedParams {
		if params[k] == "" || strings.ContainsAny(params[k], "&=") {
			return nil, fmt.Errorf("epay: %s must be non-empty without '&' or '='", k)
		}
	}
	return params, nil
}
