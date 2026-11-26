package grpcruntime

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

// TimeoutPayment bounds one PaymentGatewayService call, like a plugin HTTP
// route: at most a round trip to the gateway's API.
const TimeoutPayment = 30 * time.Second

// Bounds of what a plugin hands the core for a notification. The reply is
// written on the console's origin to anyone who calls the public notify
// route, so it stays small and inert; the reason lands in an event row.
const (
	maxPaymentReplyBytes  = 64 << 10
	maxPaymentReasonBytes = 200
	defaultReplyType      = "text/plain; charset=utf-8"
)

// paymentReplyTypes are the media types a reply may have: what gateways
// expect back, and nothing a browser renders as a document.
var paymentReplyTypes = map[string]bool{"text/plain": true, "application/json": true}

func paymentOf(p *proc) pluginv1.PaymentGatewayServiceClient { return p.payment }

// notePaymentService records whether the process reported
// payment.gateway.v1 in its handshake: a plugin that does not serve the
// service gets no gateway (PaymentGateway is nil, the registry marks it
// abnormal), unlike other capabilities, which only log a warning.
func (i *Instance) notePaymentService(reported map[string]bool) {
	i.payServed.Store(reported[manifest.CapPaymentGateway])
}

// PaymentGateway is the plugin's gateway id (manifest payments.gateways[].id)
// as a core.PaymentGateway (CONTRACTS §140), nil unless the plugin declares
// payment.gateway.v1 and the gateway, and its process serves the service.
func (i *Instance) PaymentGateway(id string) core.PaymentGateway {
	p := i.pkg.Manifest.Payments
	if !i.has(manifest.CapPaymentGateway) || p == nil || !i.payServed.Load() {
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

// defaultReplyStatus answers an outcome the plugin gave no usable reply for
// (or before it gave any replies, or with a status net/http cannot write).
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
// Respond; a notification the plugin did not verify is ErrPaymentSignature
// with the plugin's reason.
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
		return nil, a.unverified(out.GetReason())
	}
	return a.result(out.GetResult())
}

// unverified is the error of a notification the plugin did not verify:
// "plugin <id> payment gateway <gateway>: payment notification failed
// verification[: <reason>]". The core keeps the message on the event, so
// the reason loses control characters and is cut to 200 bytes.
func (a paymentAdapter) unverified(reason string) error {
	reason = strings.TrimSpace(x.Truncate(strings.Map(dropControl, reason), maxPaymentReasonBytes))
	if reason == "" {
		return fmt.Errorf("plugin %s payment gateway %s: %w", a.i.name(), a.gateway, core.ErrPaymentSignature)
	}
	return fmt.Errorf("plugin %s payment gateway %s: %w: %s", a.i.name(), a.gateway, core.ErrPaymentSignature, reason)
}

func dropControl(r rune) rune {
	if unicode.IsControl(r) {
		return -1
	}
	return r
}

// QueryPayment does not call a gateway declared without query; a plugin
// answering UNIMPLEMENTED cannot be queried either. It is the sweep's call,
// so it waits for a background slot, never for those of users' orders.
func (a paymentAdapter) QueryPayment(ctx context.Context, q core.PaymentQuery) (*core.PaymentResult, error) {
	if !a.query {
		return nil, core.ErrPaymentQueryUnsupported
	}
	req := &pluginv1.QueryPaymentRequest{Gateway: a.gateway, OutTradeNo: q.OutTradeNo, SessionId: q.SessionID, MethodType: q.MethodType, MerchantRef: q.MerchantRef}
	out, err := invoke(a.i, ctx, classBackground, TimeoutPayment, paymentOf, pluginv1.PaymentGatewayServiceClient.QueryPayment, req)
	// mapErr keeps the gRPC status as the cause; status.Code finds it.
	if status.Code(err) == codes.Unimplemented {
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
// verified result (the plugin failed, the notification was rejected). A
// reply of another media type or over the size bound is replaced by the
// default one as a whole.
func (a paymentAdapter) Respond(o core.PaymentOutcome) core.PaymentReply {
	if o < core.PaymentHandled || o > core.PaymentRetry {
		o = core.PaymentRetry
	}
	out := core.PaymentReply{Status: defaultReplyStatus[o]}
	var r *pluginv1.PaymentReply
	if last := a.replies.Load(); last != nil {
		r = last[o]
	}
	ct, ok := replyType(r)
	if !ok {
		return out
	}
	if s := int(r.GetStatus()); s >= 200 && s <= 599 {
		out.Status = s
	}
	out.ContentType, out.Body = ct, r.GetBody()
	return out
}

// replyType is the content type r is written with (text/plain when it has
// none); false for no reply, another media type or a body over the bound.
func replyType(r *pluginv1.PaymentReply) (string, bool) {
	if r == nil || len(r.GetBody()) > maxPaymentReplyBytes {
		return "", false
	}
	ct := r.GetContentType()
	if ct == "" {
		return defaultReplyType, true
	}
	mt, params, err := mime.ParseMediaType(ct)
	if err != nil || !paymentReplyTypes[mt] {
		return "", false
	}
	// Re-encoded: only what the parser understood reaches the header.
	ct = mime.FormatMediaType(mt, params)
	return ct, ct != ""
}
