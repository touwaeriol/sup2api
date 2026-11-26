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
	params := url.Values{"act": {"order"}, "pid": {cfg.MerchantID}, "key": {cfg.Key}, "out_trade_no": {outTradeNo}}
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

// queryResponse is the common api.php answer. Forks send numbers as
// strings or numbers, hence flexString.
type queryResponse struct {
	Code        flexString `json:"code"`
	Msg         flexString `json:"msg"`
	PID         flexString `json:"pid"`
	TradeNo     flexString `json:"trade_no"`
	OutTradeNo  flexString `json:"out_trade_no"`
	APITradeNo  flexString `json:"api_trade_no"`
	Type        flexString `json:"type"`
	Name        flexString `json:"name"`
	Money       flexString `json:"money"`
	Status      flexString `json:"status"`
	TradeStatus flexString `json:"trade_status"`
	AddTime     flexString `json:"addtime"`
	EndTime     flexString `json:"endtime"`
}

func (r queryResponse) result(cfg Config, outTradeNo string) (*core.PaymentResult, error) {
	if r.Code != "1" {
		return nil, unsupported(fmt.Sprintf("answered code %q: %q", r.Code, remoteMessage(string(r.Msg), cfg.Key)))
	}
	if (r.OutTradeNo != "" && string(r.OutTradeNo) != outTradeNo) || (r.PID != "" && string(r.PID) != cfg.MerchantID) {
		return nil, unsupported("answered about another order or merchant")
	}
	status := r.status()
	var cents int64
	if r.Money != "" || status == core.PaymentPaid {
		var err error
		if cents, err = parseMoney(string(r.Money)); err != nil {
			return nil, unsupported(err.Error())
		}
	}
	return &core.PaymentResult{
		OutTradeNo: outTradeNo, TradeNo: string(r.TradeNo),
		Amount: cents, Currency: currencyCNY, Status: status,
		MerchantRef: cfg.MerchantID, MethodType: string(r.Type),
		Fields: fields(r.fields()),
	}, nil
}

// status: trade_status wins when present; otherwise the older numeric
// status, where 1 means paid.
func (r queryResponse) status() core.PaymentStatus {
	paid := r.TradeStatus == tradeStatusSuccess || (r.TradeStatus == "" && r.Status == "1")
	if paid {
		return core.PaymentPaid
	}
	return core.PaymentPending
}

func (r queryResponse) fields() map[string]string {
	return map[string]string{
		"pid": string(r.PID), "trade_no": string(r.TradeNo), "out_trade_no": string(r.OutTradeNo),
		"api_trade_no": string(r.APITradeNo), "type": string(r.Type), "name": string(r.Name),
		"money": string(r.Money), "trade_status": string(r.TradeStatus), "status": string(r.Status),
		"addtime": string(r.AddTime), "endtime": string(r.EndTime),
	}
}

// remoteMessage is the gateway's msg made safe for an error: the key is
// masked in case the gateway echoes the request, and the text is capped.
func remoteMessage(msg, key string) string {
	msg = strings.ReplaceAll(msg, key, "******")
	if len(msg) <= maxRemoteMessage {
		return msg
	}
	return strings.ToValidUTF8(msg[:maxRemoteMessage], "") + "…"
}

// flexString accepts a JSON string, number or null.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*f = flexString(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*f = flexString(n.String())
	return nil
}
