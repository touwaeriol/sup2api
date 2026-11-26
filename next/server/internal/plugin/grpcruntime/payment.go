package grpcruntime

import (
	"context"
	"fmt"
	"mime"
	"slices"
	"strings"
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
const TimeoutPayment = TimeoutHTTP

// Bounds of what a plugin hands the core for a notification. The reply is
// written on the console's origin to anyone who calls the public notify
// route, so it stays small and inert; the reason lands in an event row.
const (
	maxPaymentReplyBytes  = 64 << 10
	maxPaymentReasonBytes = 200
)

// paymentReplyTypes are the media types a reply may have: what gateways
// expect back, and nothing a browser renders as a document.
var paymentReplyTypes = map[string]bool{"text/plain": true, "application/json": true}

func paymentOf(p *proc) pluginv1.PaymentGatewayServiceClient { return p.payment }

// PaymentGateway is the plugin's gateway id (manifest payments.gateways[].id)
// as a core.PaymentGateway (CONTRACTS §140), nil unless the plugin declares
// payment.gateway.v1 and the gateway, and its process reported the service
// in its handshake: unlike other capabilities, which only log a warning, a
// gateway the process does not serve is not registered (the registry marks
// the plugin abnormal).
func (i *Instance) PaymentGateway(id string) core.PaymentGateway {
	p := i.pkg.Manifest.Payments
	if !i.has(manifest.CapPaymentGateway) || p == nil || !i.served(manifest.CapPaymentGateway) {
		return nil
	}
	n := slices.IndexFunc(p.Gateways, func(g manifest.PaymentGateway) bool { return g.ID == id })
	if n < 0 {
		return nil
	}
	return paymentAdapter{i: i, gateway: id, query: p.Gateways[n].Query}
}

// paymentAdapter passes the facts both ways as they are: the core checks
// every one of them against the order, so the adapter neither fixes nor
// drops any, and plugin errors stay errors. It holds no state of a
// notification: each one's replies travel with its own answer.
type paymentAdapter struct {
	i       *Instance
	gateway string
	query   bool
}

// errorf is an error about the gateway: "plugin <id> payment gateway <id>: ...".
func (a paymentAdapter) errorf(format string, args ...any) error {
	return fmt.Errorf("plugin %s payment gateway %s: "+format, append([]any{a.i.name(), a.gateway}, args...)...)
}

func (a paymentAdapter) fail(format string, args ...any) error {
	return core.ErrInternal.WithCause(a.errorf(format, args...))
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
	return &core.PaymentCreated{PayURL: out.GetPayUrl(), QRCode: out.GetQrCode(), SessionID: out.GetSessionId(), MerchantRef: out.GetMerchantRef()}, nil
}

// ParseNotification answers the notification with the replies of the
// plugin's answer to it, verified or not, and with the defaults when the
// plugin gave none; a notification the plugin did not verify is
// ErrPaymentSignature with the plugin's reason.
func (a paymentAdapter) ParseNotification(ctx context.Context, n core.PaymentNotification) (*core.PaymentResult, core.PaymentResponder, error) {
	req := &pluginv1.ParsePaymentNotificationRequest{Gateway: a.gateway, Method: n.Method, RawQuery: n.RawQuery,
		Headers: make(map[string]*pluginv1.HeaderValues, len(n.Header)), Body: n.Body}
	for k, v := range n.Header {
		req.Headers[k] = &pluginv1.HeaderValues{Values: v}
	}
	out, err := invoke(a.i, ctx, classConsole, TimeoutPayment, paymentOf, pluginv1.PaymentGatewayServiceClient.ParsePaymentNotification, req)
	if err != nil {
		return nil, core.StatusPaymentReply, err
	}
	respond := paymentReplies{core.PaymentHandled: out.GetHandled(), core.PaymentRejected: out.GetRejected(), core.PaymentRetry: out.GetRetry()}.respond
	if !out.GetVerified() {
		return nil, respond, a.unverified(out.GetReason())
	}
	res, err := a.result(out.GetResult())
	return res, respond, err
}

// unverified is the error of a notification the plugin did not verify:
// "plugin <id> payment gateway <gateway>: payment notification failed
// verification[: <reason>]". The core keeps the message on the event, so
// the reason loses control characters and is cut to 200 bytes.
func (a paymentAdapter) unverified(reason string) error {
	reason = strings.TrimSpace(x.Truncate(strings.Map(dropControl, reason), maxPaymentReasonBytes))
	if reason == "" {
		return a.errorf("%w", core.ErrPaymentSignature)
	}
	return a.errorf("%w: %s", core.ErrPaymentSignature, reason)
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
		return nil, a.errorf("%w", core.ErrPaymentQueryUnsupported)
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
		Amount: r.GetAmountMinor(), Currency: r.GetCurrency(), Status: st, EventID: r.GetEventId(),
		MerchantRef: r.GetMerchantRef(), MethodType: r.GetMethodType(), Fields: r.GetFields()}, nil
}

// paymentReplies are the plugin's replies to one notification, by outcome.
type paymentReplies [3]*pluginv1.PaymentReply

// respond answers with the plugin's reply for the outcome. A missing reply,
// or one of another media type or over the size bound, is replaced by the
// default one as a whole; a status net/http cannot write only by the
// default status.
func (r paymentReplies) respond(o core.PaymentOutcome) core.PaymentReply {
	var reply *pluginv1.PaymentReply
	if o >= core.PaymentHandled && o <= core.PaymentRetry {
		reply = r[o]
	}
	ct, ok := replyType(reply)
	if !ok {
		return core.StatusPaymentReply(o)
	}
	status := int(reply.GetStatus())
	if status < 200 || status > 599 {
		status = o.HTTPStatus()
	}
	return core.PaymentReply{Status: status, ContentType: ct, Body: reply.GetBody()}
}

// replyType is the content type r is written with (text/plain when it has
// none); false for no reply, another media type or a body over the bound.
func replyType(r *pluginv1.PaymentReply) (string, bool) {
	if r == nil || len(r.GetBody()) > maxPaymentReplyBytes {
		return "", false
	}
	ct := r.GetContentType()
	if ct == "" {
		return core.PlainTextReplyType, true
	}
	mt, params, err := mime.ParseMediaType(ct)
	if err != nil || !paymentReplyTypes[mt] {
		return "", false
	}
	// Re-encoded: only what the parser understood reaches the header.
	ct = mime.FormatMediaType(mt, params)
	return ct, ct != ""
}
