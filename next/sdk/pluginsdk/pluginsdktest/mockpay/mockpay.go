// Package mockpay is a simulated payment gateway ("payment.gateway.v1") for
// tests of the plugin gateway path: the SDK's own tests serve it in-process
// and the core's runtime tests run it as a plugin process
// (server/internal/plugin/grpcruntime/testdata/paymentplugin). It talks to
// no real gateway and is never packaged or published, which is why it lives
// here and not under next/plugins (every directory there is released).
//
// A notification is a JSON Notification signed with HMAC-SHA256 under
// Secret in the SignatureHeader header. Gateway GatewayMock answers status
// queries from what it was told; GatewayNoQuery cannot be queried.
package mockpay

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
)

const (
	// Secret is the HMAC key of the notifications the gateways accept.
	Secret          = "mockpay-test-secret"
	SignatureHeader = "X-Mock-Signature"
	GatewayMock     = "mock"
	GatewayNoQuery  = "mock_noquery"
	MethodType      = "mockpay"
	MerchantRef     = "mock-merchant"
)

// Replies are the gateways' answers to notifications.
var Replies = pluginsdk.PaymentReplies{
	Handled:  pluginsdk.TextReply(200, "success"),
	Rejected: pluginsdk.TextReply(400, "fail"),
	Retry:    pluginsdk.TextReply(503, "retry"),
}

// Notification is the body of a simulated notification. Status is paid,
// pending, failed or expired.
type Notification struct {
	EventID    string `json:"event_id"`
	OutTradeNo string `json:"out_trade_no"`
	TradeNo    string `json:"trade_no"`
	Amount     int64  `json:"amount"`
	Currency   string `json:"currency"`
	Status     string `json:"status"`
	MethodType string `json:"method_type,omitempty"`
}

var statuses = map[string]pluginv1.PaymentStatus{
	"paid":    pluginv1.PaymentStatus_PAYMENT_STATUS_PAID,
	"pending": pluginv1.PaymentStatus_PAYMENT_STATUS_PENDING,
	"failed":  pluginv1.PaymentStatus_PAYMENT_STATUS_FAILED,
	"expired": pluginv1.PaymentStatus_PAYMENT_STATUS_EXPIRED,
}

// Sign is the SignatureHeader value of body.
func Sign(body []byte) string {
	mac := hmac.New(sha256.New, []byte(Secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// Payments is the manifest declaration of the two gateways.
func Payments() *manifest.Payments {
	method := []manifest.PaymentMethod{{Type: MethodType, Name: manifest.LocalizedText{"en": "Mock pay", "zh": "模拟支付"}, Icon: "card"}}
	return &manifest.Payments{Gateways: []manifest.PaymentGateway{
		{ID: GatewayMock, Name: manifest.LocalizedText{"en": "Mock gateway"}, Methods: method, Query: true},
		{ID: GatewayNoQuery, Name: manifest.LocalizedText{"en": "Mock gateway without queries"}, Methods: method},
	}}
}

// New returns the plugin serving both gateways.
func New() pluginsdk.PaymentGatewayMux {
	s := &store{results: map[string]*pluginv1.PaymentResult{}}
	return pluginsdk.PaymentGatewayMux{
		GatewayMock:    gateway{store: s, query: true},
		GatewayNoQuery: gateway{store: s},
	}
}

// store keeps the last known result of each order, for QueryPayment.
type store struct {
	mu      sync.Mutex
	results map[string]*pluginv1.PaymentResult
}

func (s *store) put(r *pluginv1.PaymentResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results[r.GetOutTradeNo()] = r
}

func (s *store) get(outTradeNo string) (*pluginv1.PaymentResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.results[outTradeNo]
	return r, ok
}

type gateway struct {
	store *store
	query bool
}

func (g gateway) CreatePayment(_ context.Context, in *pluginv1.CreatePaymentRequest) (*pluginv1.CreatePaymentResponse, error) {
	if in.GetAmountMinor() <= 0 || in.GetOutTradeNo() == "" {
		return nil, status.Error(codes.InvalidArgument, "amount and out_trade_no are required")
	}
	session := "ms_" + in.GetOutTradeNo()
	g.store.put(&pluginv1.PaymentResult{OutTradeNo: in.GetOutTradeNo(), SessionId: session, AmountMinor: in.GetAmountMinor(),
		Currency: in.GetCurrency(), Status: pluginv1.PaymentStatus_PAYMENT_STATUS_PENDING, MerchantRef: MerchantRef, MethodType: in.GetMethodType()})
	return &pluginv1.CreatePaymentResponse{PayUrl: "https://mockpay.invalid/pay/" + in.GetOutTradeNo(),
		QrCode: "mockpay:" + in.GetOutTradeNo(), SessionId: session, MerchantRef: MerchantRef}, nil
}

func (g gateway) ParsePaymentNotification(_ context.Context, in *pluginv1.ParsePaymentNotificationRequest) (*pluginv1.ParsePaymentNotificationResponse, error) {
	sig := pluginsdk.PaymentNotificationHeader(in).Get(SignatureHeader)
	if !hmac.Equal([]byte(sig), []byte(Sign(in.GetBody()))) {
		return pluginsdk.RejectedNotification("bad signature", Replies), nil
	}
	var n Notification
	st, known := pluginv1.PaymentStatus(0), false
	if json.Unmarshal(in.GetBody(), &n) == nil {
		st, known = statuses[n.Status]
	}
	if !known {
		return pluginsdk.RejectedNotification("malformed notification", Replies), nil
	}
	r := &pluginv1.PaymentResult{OutTradeNo: n.OutTradeNo, TradeNo: n.TradeNo, SessionId: "ms_" + n.OutTradeNo, AmountMinor: n.Amount,
		Currency: n.Currency, Status: st, EventId: n.EventID, MerchantRef: MerchantRef, MethodType: n.MethodType,
		Fields: map[string]string{"trade_no": n.TradeNo, "status": n.Status}}
	g.store.put(r)
	return pluginsdk.VerifiedNotification(r, Replies), nil
}

func (g gateway) QueryPayment(_ context.Context, in *pluginv1.QueryPaymentRequest) (*pluginv1.QueryPaymentResponse, error) {
	if !g.query {
		return nil, pluginsdk.ErrPaymentQueryUnsupported
	}
	r, ok := g.store.get(in.GetOutTradeNo())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "no payment for %s", in.GetOutTradeNo())
	}
	return &pluginv1.QueryPaymentResponse{Result: r}, nil
}
