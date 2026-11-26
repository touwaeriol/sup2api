package grpcruntime

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// TimeoutPayment bounds one PaymentGatewayService call, like a plugin HTTP
// route: at most a round trip to the gateway's API.
const TimeoutPayment = 30 * time.Second

func paymentOf(p *proc) pluginv1.PaymentGatewayServiceClient { return p.payment }

// PaymentGateway is the plugin's gateway id (manifest payments.gateways[].id)
// as a core.PaymentGateway (CONTRACTS §140), nil unless the plugin
// implements payment.gateway.v1 and declares the gateway.
func (i *Instance) PaymentGateway(id string) core.PaymentGateway {
	p := i.pkg.Manifest.Payments
	if !i.has(manifest.CapPaymentGateway) || p == nil {
		return nil
	}
	n := slices.IndexFunc(p.Gateways, func(g manifest.PaymentGateway) bool { return g.ID == id })
	if n < 0 {
		return nil
	}
	replies, _ := i.payReplies.LoadOrStore(id, &atomic.Pointer[paymentReplies]{})
	return paymentAdapter{i: i, gateway: id, query: p.Gateways[n].Query, replies: replies.(*atomic.Pointer[paymentReplies])}
}

// paymentAdapter passes the facts both ways as they are: the core checks
// every one of them against the order, so the adapter neither fixes nor
// drops any, and plugin errors stay errors.
type paymentAdapter struct {
	i       *Instance
	gateway string
	query   bool
	replies *atomic.Pointer[paymentReplies]
}

// paymentReplies are the plugin's answers to the gateway, one per outcome.
type paymentReplies [3]*pluginv1.PaymentReply

// defaultReplyStatus answers an outcome the plugin gave no status for (or
// before it gave any replies, or with a status net/http cannot write).
var defaultReplyStatus = [3]int{core.PaymentHandled: http.StatusOK, core.PaymentRejected: http.StatusBadRequest, core.PaymentRetry: http.StatusServiceUnavailable}

func (a paymentAdapter) fail(format string, args ...any) error {
	return core.ErrInternal.WithCause(fmt.Errorf("plugin %s payment gateway %s: %s", a.i.name(), a.gateway, fmt.Sprintf(format, args...)))
}

func (a paymentAdapter) CreatePayment(ctx context.Context, in core.PaymentCreate) (*core.PaymentCreated, error) {
	minor, ok := core.PaymentMinorUnits(in.Amount, in.Currency)
	if !ok {
		return nil, core.ErrInvalidArgument.WithMessage("payment amount must be positive and exact in the currency's minor unit")
	}
	req := &pluginv1.CreatePaymentRequest{Gateway: a.gateway, OutTradeNo: in.OutTradeNo,
		Amount: in.Amount.StringFixed(core.PaymentExponent(in.Currency)), AmountMinor: minor, Currency: in.Currency,
		MethodType: in.MethodType, Subject: in.Subject, NotifyUrl: in.NotifyURL, ReturnUrl: in.ReturnURL,
		ExpiresAtUnix: in.ExpiresAt.Unix(), ClientIp: in.ClientIP}
	out, err := invoke(a.i, ctx, classConsole, TimeoutPayment, paymentOf, pluginv1.PaymentGatewayServiceClient.CreatePayment, req)
	if err != nil {
		return nil, err
	}
	if out.GetPayUrl() == "" && out.GetQrCode() == "" {
		return nil, a.fail("CreatePayment returned neither pay_url nor qr_code")
	}
	return &core.PaymentCreated{PayURL: out.GetPayUrl(), QRCode: out.GetQrCode(), SessionID: out.GetSessionId(), MerchantRef: out.GetMerchantRef()}, nil
}

// ParseNotification keeps the replies of every answer, verified or not, for
// Respond; a notification the plugin did not verify is ErrPaymentSignature.
func (a paymentAdapter) ParseNotification(ctx context.Context, n core.PaymentNotification) (*core.PaymentResult, error) {
	req := &pluginv1.ParseNotificationRequest{Gateway: a.gateway, Method: n.Method, RawQuery: n.RawQuery,
		Headers: make(map[string]*pluginv1.HeaderValues, len(n.Header)), Body: n.Body}
	for k, v := range n.Header {
		req.Headers[k] = &pluginv1.HeaderValues{Values: v}
	}
	out, err := invoke(a.i, ctx, classConsole, TimeoutPayment, paymentOf, pluginv1.PaymentGatewayServiceClient.ParseNotification, req)
	if err != nil {
		return nil, err
	}
	a.replies.Store(&paymentReplies{core.PaymentHandled: out.GetHandled(), core.PaymentRejected: out.GetRejected(), core.PaymentRetry: out.GetRetry()})
	if !out.GetVerified() {
		return nil, fmt.Errorf("plugin %s payment gateway %s: %w: %s", a.i.name(), a.gateway, core.ErrPaymentSignature, out.GetReason())
	}
	return a.result(out.GetResult())
}

// QueryPayment does not call a gateway declared without query; a plugin
// answering UNIMPLEMENTED cannot be queried either.
func (a paymentAdapter) QueryPayment(ctx context.Context, q core.PaymentQuery) (*core.PaymentResult, error) {
	if !a.query {
		return nil, core.ErrPaymentQueryUnsupported
	}
	var replied error
	out, err := invoke(a.i, ctx, classConsole, TimeoutPayment, paymentOf,
		func(c pluginv1.PaymentGatewayServiceClient, ctx context.Context, in *pluginv1.QueryPaymentRequest, opts ...grpc.CallOption) (*pluginv1.QueryPaymentResponse, error) {
			resp, e := c.QueryPayment(ctx, in, opts...)
			replied = e
			return resp, e
		},
		&pluginv1.QueryPaymentRequest{Gateway: a.gateway, OutTradeNo: q.OutTradeNo, SessionId: q.SessionID, MethodType: q.MethodType, MerchantRef: q.MerchantRef})
	if status.Code(replied) == codes.Unimplemented {
		return nil, fmt.Errorf("plugin %s payment gateway %s: %w", a.i.name(), a.gateway, core.ErrPaymentQueryUnsupported)
	}
	if err != nil {
		return nil, err
	}
	return a.result(out.GetResult())
}

var paymentStatuses = map[pluginv1.PaymentStatus]core.PaymentStatus{
	pluginv1.PaymentStatus_PAYMENT_STATUS_PAID:    core.PaymentPaid,
	pluginv1.PaymentStatus_PAYMENT_STATUS_PENDING: core.PaymentPending,
	pluginv1.PaymentStatus_PAYMENT_STATUS_FAILED:  core.PaymentFailed,
	pluginv1.PaymentStatus_PAYMENT_STATUS_EXPIRED: core.PaymentExpired,
}

func (a paymentAdapter) result(r *pluginv1.PaymentResult) (*core.PaymentResult, error) {
	st, ok := paymentStatuses[r.GetStatus()]
	if !ok {
		return nil, a.fail("result without a payment status")
	}
	return &core.PaymentResult{OutTradeNo: r.GetOutTradeNo(), TradeNo: r.GetTradeNo(), SessionID: r.GetSessionId(),
		Amount: r.GetAmount(), Currency: r.GetCurrency(), Status: st, EventID: r.GetEventId(),
		MerchantRef: r.GetMerchantRef(), MethodType: r.GetMethodType(), Fields: r.GetFields()}, nil
}

// Respond answers with the plugin's reply for the outcome, from its latest
// ParseNotification answer: the core may decide an outcome without a
// verified result (the plugin failed, the notification was rejected).
func (a paymentAdapter) Respond(o core.PaymentOutcome) core.PaymentReply {
	if o < core.PaymentHandled || o > core.PaymentRetry {
		o = core.PaymentRetry
	}
	out := core.PaymentReply{Status: defaultReplyStatus[o]}
	var r *pluginv1.PaymentReply
	if last := a.replies.Load(); last != nil {
		r = last[o]
	}
	if r == nil {
		return out
	}
	if s := int(r.GetStatus()); s >= 200 && s <= 599 {
		out.Status = s
	}
	out.ContentType, out.Body = r.GetContentType(), r.GetBody()
	return out
}
