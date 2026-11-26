package pluginsdk

import (
	"context"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
)

// Payment gateways ("payment.gateway.v1", manifest payments.gateways, host
// permission "payment.gateway", CONTRACTS §140).
//
// The core owns orders, amounts and crediting. A gateway plugin creates
// payments, says whether a notification is authentic and what it reports,
// and answers status queries; it never sees the order table or the ledger.
// The core checks every fact of a result against the order - the gateway,
// the amount and currency, the merchant, an unused transaction and event -
// so a plugin can at most get its own gateway's orders credited for the
// orders' amounts.

// PaymentGateway mirrors pluginv1.PaymentGatewayServiceServer. Each request
// names the plugin-local gateway id (Gateway); PaymentGatewayMux dispatches
// on it. ParsePaymentNotification answers an unauthentic notification with
// RejectedNotification, not an error: an error makes the core ask the
// gateway to retry. QueryPayment returns ErrPaymentQueryUnsupported when the
// gateway cannot be queried.
type PaymentGateway interface {
	CreatePayment(context.Context, *pluginv1.CreatePaymentRequest) (*pluginv1.CreatePaymentResponse, error)
	ParsePaymentNotification(context.Context, *pluginv1.ParsePaymentNotificationRequest) (*pluginv1.ParsePaymentNotificationResponse, error)
	QueryPayment(context.Context, *pluginv1.QueryPaymentRequest) (*pluginv1.QueryPaymentResponse, error)
}

// ErrPaymentQueryUnsupported is QueryPayment's answer for a gateway that
// cannot be queried; the core reads it as core.ErrPaymentQueryUnsupported.
var ErrPaymentQueryUnsupported = status.Error(codes.Unimplemented, "payment gateway cannot be queried")

// PaymentReplies are a gateway's answers to one notification, one per
// outcome the core decides (epay's "success", a webhook's 200). The core
// answers that notification with them and no other.
type PaymentReplies struct {
	Handled, Rejected, Retry *pluginv1.PaymentReply
}

func (r PaymentReplies) response() *pluginv1.ParsePaymentNotificationResponse {
	return &pluginv1.ParsePaymentNotificationResponse{Handled: r.Handled, Rejected: r.Rejected, Retry: r.Retry}
}

// TextReply is a plain-text answer.
func TextReply(status int, body string) *pluginv1.PaymentReply {
	return &pluginv1.PaymentReply{Status: int32(status), ContentType: "text/plain; charset=utf-8", Body: []byte(body)}
}

// VerifiedNotification answers an authentic notification with what it
// reports.
func VerifiedNotification(r *pluginv1.PaymentResult, replies PaymentReplies) *pluginv1.ParsePaymentNotificationResponse {
	out := replies.response()
	out.Verified, out.Result = true, r
	return out
}

// RejectedNotification answers a notification that is not authentic;
// reason goes to the event log and must hold no secret.
func RejectedNotification(reason string, replies PaymentReplies) *pluginv1.ParsePaymentNotificationResponse {
	out := replies.response()
	out.Reason = reason
	return out
}

// PaymentNotificationHeader is the notification's headers as an http.Header.
func PaymentNotificationHeader(in *pluginv1.ParsePaymentNotificationRequest) http.Header {
	h := make(http.Header, len(in.GetHeaders()))
	for name, v := range in.GetHeaders() {
		h[http.CanonicalHeaderKey(name)] = append([]string(nil), v.GetValues()...)
	}
	return h
}

// PaymentMinorUnits converts a decimal amount a gateway reports ("12.30")
// to the currency's minor unit, PaymentResult.amount_minor (1230); ok is
// false unless the currency is one a top-up settles in and the amount is
// positive and exact in that unit. The core uses the same table.
func PaymentMinorUnits(amount, currency string) (int64, bool) {
	return protocol.PaymentMinorUnits(amount, currency)
}

// PaymentGatewayMux serves several gateways of one plugin, by gateway id.
// A request for an id it does not hold is INVALID_ARGUMENT.
type PaymentGatewayMux map[string]PaymentGateway

func (m PaymentGatewayMux) gateway(id string) (PaymentGateway, error) {
	if gw, ok := m[id]; ok {
		return gw, nil
	}
	return nil, status.Errorf(codes.InvalidArgument, "unknown payment gateway %q", id)
}

func (m PaymentGatewayMux) CreatePayment(ctx context.Context, in *pluginv1.CreatePaymentRequest) (*pluginv1.CreatePaymentResponse, error) {
	gw, err := m.gateway(in.GetGateway())
	if err != nil {
		return nil, err
	}
	return gw.CreatePayment(ctx, in)
}

func (m PaymentGatewayMux) ParsePaymentNotification(ctx context.Context, in *pluginv1.ParsePaymentNotificationRequest) (*pluginv1.ParsePaymentNotificationResponse, error) {
	gw, err := m.gateway(in.GetGateway())
	if err != nil {
		return nil, err
	}
	return gw.ParsePaymentNotification(ctx, in)
}

func (m PaymentGatewayMux) QueryPayment(ctx context.Context, in *pluginv1.QueryPaymentRequest) (*pluginv1.QueryPaymentResponse, error) {
	gw, err := m.gateway(in.GetGateway())
	if err != nil {
		return nil, err
	}
	return gw.QueryPayment(ctx, in)
}

type paymentServer struct {
	pluginv1.UnimplementedPaymentGatewayServiceServer
	impl PaymentGateway
}

func (s paymentServer) CreatePayment(ctx context.Context, in *pluginv1.CreatePaymentRequest) (*pluginv1.CreatePaymentResponse, error) {
	return s.impl.CreatePayment(ctx, in)
}

func (s paymentServer) ParsePaymentNotification(ctx context.Context, in *pluginv1.ParsePaymentNotificationRequest) (*pluginv1.ParsePaymentNotificationResponse, error) {
	return s.impl.ParsePaymentNotification(ctx, in)
}

func (s paymentServer) QueryPayment(ctx context.Context, in *pluginv1.QueryPaymentRequest) (*pluginv1.QueryPaymentResponse, error) {
	return s.impl.QueryPayment(ctx, in)
}
