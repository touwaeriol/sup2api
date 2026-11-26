package grpcruntime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry"
)

type paymentRPC struct {
	pluginv1.PaymentGatewayServiceClient
	calls   int
	created *pluginv1.CreatePaymentResponse
	parsed  *pluginv1.ParseNotificationResponse
	queried *pluginv1.QueryPaymentResponse
	err     error
}

func (p *paymentRPC) CreatePayment(context.Context, *pluginv1.CreatePaymentRequest, ...grpc.CallOption) (*pluginv1.CreatePaymentResponse, error) {
	p.calls++
	return p.created, p.err
}

func (p *paymentRPC) ParseNotification(context.Context, *pluginv1.ParseNotificationRequest, ...grpc.CallOption) (*pluginv1.ParseNotificationResponse, error) {
	p.calls++
	return p.parsed, p.err
}

func (p *paymentRPC) QueryPayment(context.Context, *pluginv1.QueryPaymentRequest, ...grpc.CallOption) (*pluginv1.QueryPaymentResponse, error) {
	p.calls++
	return p.queried, p.err
}

func paymentInstance(rpc *paymentRPC) *Instance {
	m := &manifest.Manifest{Payments: &manifest.Payments{Gateways: []manifest.PaymentGateway{{ID: "direct"}, {ID: "queried", Query: true}}}}
	i := &Instance{pkg: &registry.Package{Key: "alipay", Version: "1", Manifest: m}, caps: map[string]bool{manifest.CapPaymentGateway: true},
		sems: newSemaphores(Concurrency{Console: 1, Background: 1})}
	i.proc.Store(&proc{payment: rpc})
	i.notePaymentService(map[string]bool{manifest.CapPaymentGateway: true})
	return i
}

// Only a declared gateway of a plugin with the capability whose process
// reported the service is a gateway.
func TestPaymentGatewayAccessor(t *testing.T) {
	i := paymentInstance(&paymentRPC{})
	if i.PaymentGateway("direct") == nil || i.PaymentGateway("wap") != nil {
		t.Fatal("declared gateways")
	}
	i.notePaymentService(map[string]bool{manifest.CapHTTPRoutes: true})
	if i.PaymentGateway("direct") != nil {
		t.Fatal("gateway of a process not serving the service")
	}
	i = paymentInstance(&paymentRPC{})
	i.caps = map[string]bool{}
	if i.PaymentGateway("direct") != nil {
		t.Fatal("gateway without the capability")
	}
}

// Respond takes the core's default until the plugin gave a reply, and for a
// status net/http cannot write; the replies outlive one generation's adapter.
func TestPaymentRespond(t *testing.T) {
	rpc := &paymentRPC{parsed: &pluginv1.ParseNotificationResponse{Verified: true,
		Handled: &pluginv1.PaymentReply{Body: []byte("success")}, Rejected: &pluginv1.PaymentReply{Status: 1000, Body: []byte("fail")},
		Retry: &pluginv1.PaymentReply{Status: 500, ContentType: "text/plain", Body: []byte("again")}}}
	i := paymentInstance(rpc)
	gw := i.PaymentGateway("direct")
	if r := gw.Respond(core.PaymentRetry); r.Status != 503 || r.Body != nil {
		t.Fatalf("default retry: %+v", r)
	}
	if _, err := gw.ParseNotification(context.Background(), core.PaymentNotification{}); core.AsError(err).Code != core.ErrInternal.Code {
		t.Fatalf("result without a status: %v", err)
	}
	gw = i.PaymentGateway("direct")
	for o, want := range map[core.PaymentOutcome]core.PaymentReply{
		core.PaymentHandled:  {Status: 200, ContentType: defaultReplyType, Body: []byte("success")},
		core.PaymentRejected: {Status: 400, ContentType: defaultReplyType, Body: []byte("fail")},
		core.PaymentRetry:    {Status: 500, ContentType: "text/plain", Body: []byte("again")},
	} {
		if r := gw.Respond(o); r.Status != want.Status || r.ContentType != want.ContentType || string(r.Body) != string(want.Body) {
			t.Fatalf("outcome %d: %+v", o, r)
		}
	}
}

// A gateway declared without query is not asked; plugin failures and empty
// answers are errors, never results.
func TestPaymentAdapterErrors(t *testing.T) {
	ctx := context.Background()
	rpc := &paymentRPC{created: &pluginv1.CreatePaymentResponse{SessionId: "s"}}
	gw := paymentInstance(rpc).PaymentGateway("direct")
	if _, err := gw.QueryPayment(ctx, core.PaymentQuery{}); err != core.ErrPaymentQueryUnsupported || rpc.calls != 0 {
		t.Fatalf("undeclared query: %v %d", err, rpc.calls)
	}
	in := core.PaymentCreate{OutTradeNo: "s2a_1", Amount: decimal.NewFromInt(5), Currency: "JPY"}
	if _, err := gw.CreatePayment(ctx, in); core.AsError(err).Code != core.ErrInternal.Code {
		t.Fatalf("nothing to pay with: %v", err)
	}
	rpc.err = status.Error(codes.Unknown, "gateway said no")
	if _, err := gw.CreatePayment(ctx, in); core.AsError(err).Code != core.ErrInternal.Code {
		t.Fatalf("plugin error: %v", err)
	}
	rpc.err = status.Error(codes.Unavailable, "down")
	if _, err := gw.ParseNotification(ctx, core.PaymentNotification{}); core.AsError(err).Code != core.ErrPluginUnavailable.Code {
		t.Fatalf("unavailable: %v", err)
	}
}

// A reply of another media type or over 64 KiB is replaced by the default
// one; an allowed type keeps its parameters, none means text/plain.
func TestPaymentRespondBounds(t *testing.T) {
	cases := []struct {
		reply  *pluginv1.PaymentReply
		status int
		ctype  string
		body   string
	}{
		{&pluginv1.PaymentReply{Status: 200, ContentType: "text/html", Body: []byte("<script>")}, 400, "", ""},
		{&pluginv1.PaymentReply{ContentType: "image/svg+xml", Body: []byte("<svg/>")}, 400, "", ""},
		{&pluginv1.PaymentReply{ContentType: "text/plain;;", Body: []byte("x")}, 400, "", ""},
		{&pluginv1.PaymentReply{ContentType: "text/plain", Body: make([]byte, maxPaymentReplyBytes+1)}, 400, "", ""},
		{&pluginv1.PaymentReply{Status: 409, ContentType: "Application/JSON; charset=UTF-8", Body: []byte(`{}`)}, 409, "application/json; charset=UTF-8", "{}"},
		{&pluginv1.PaymentReply{Body: []byte("fail")}, 400, "text/plain; charset=utf-8", "fail"},
	}
	for n, tc := range cases {
		i := paymentInstance(&paymentRPC{parsed: &pluginv1.ParseNotificationResponse{Rejected: tc.reply}})
		gw := i.PaymentGateway("direct")
		_, _ = gw.ParseNotification(context.Background(), core.PaymentNotification{})
		if r := gw.Respond(core.PaymentRejected); r.Status != tc.status || r.ContentType != tc.ctype || string(r.Body) != tc.body {
			t.Fatalf("case %d: %d %q %q", n, r.Status, r.ContentType, r.Body)
		}
	}
}

// An unverified notification is ErrPaymentSignature with the plugin's
// reason, without control characters and cut to 200 bytes.
func TestPaymentUnverifiedReason(t *testing.T) {
	for reason, want := range map[string]string{
		"bad\r\nsignature\x00":   "plugin alipay payment gateway direct: payment notification failed verification: badsignature",
		"":                       "plugin alipay payment gateway direct: payment notification failed verification",
		strings.Repeat("é", 150): "plugin alipay payment gateway direct: payment notification failed verification: " + strings.Repeat("é", 100),
	} {
		gw := paymentInstance(&paymentRPC{parsed: &pluginv1.ParseNotificationResponse{Reason: reason}}).PaymentGateway("direct")
		_, err := gw.ParseNotification(context.Background(), core.PaymentNotification{})
		if !errors.Is(err, core.ErrPaymentSignature) || err.Error() != want {
			t.Fatalf("reason %q: %v", reason, err)
		}
	}
}

// QueryPayment waits for a background slot: a full console class does not
// hold the sweep up.
func TestPaymentQueryBackgroundSlot(t *testing.T) {
	rpc := &paymentRPC{queried: &pluginv1.QueryPaymentResponse{Result: &pluginv1.PaymentResult{Status: pluginv1.PaymentStatus_PAYMENT_STATUS_PAID}}}
	i := paymentInstance(rpc)
	i.sems[classConsole] <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if r, err := i.PaymentGateway("queried").QueryPayment(ctx, core.PaymentQuery{}); err != nil || r.Status != core.PaymentPaid {
		t.Fatalf("query: %+v %v", r, err)
	}
	rpc.err = status.Error(codes.Unimplemented, "no")
	if _, err := i.PaymentGateway("queried").QueryPayment(ctx, core.PaymentQuery{}); !errors.Is(err, core.ErrPaymentQueryUnsupported) {
		t.Fatalf("unimplemented: %v", err)
	}
}
