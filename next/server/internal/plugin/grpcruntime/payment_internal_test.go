package grpcruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
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
	parsed  *pluginv1.ParsePaymentNotificationResponse
	queried *pluginv1.QueryPaymentResponse
	err     error
}

func (p *paymentRPC) CreatePayment(context.Context, *pluginv1.CreatePaymentRequest, ...grpc.CallOption) (*pluginv1.CreatePaymentResponse, error) {
	p.calls++
	return p.created, p.err
}

func (p *paymentRPC) ParsePaymentNotification(context.Context, *pluginv1.ParsePaymentNotificationRequest, ...grpc.CallOption) (*pluginv1.ParsePaymentNotificationResponse, error) {
	p.calls++
	return p.parsed, p.err
}

func (p *paymentRPC) QueryPayment(context.Context, *pluginv1.QueryPaymentRequest, ...grpc.CallOption) (*pluginv1.QueryPaymentResponse, error) {
	p.calls++
	return p.queried, p.err
}

func paymentInstance(rpc pluginv1.PaymentGatewayServiceClient) *Instance {
	m := &manifest.Manifest{Payments: &manifest.Payments{Gateways: []manifest.PaymentGateway{{ID: "direct"}, {ID: "queried", Query: true}}}}
	i := &Instance{pkg: &registry.Package{Key: "alipay", Version: "1", Manifest: m}, caps: map[string]bool{manifest.CapPaymentGateway: true},
		sems: newSemaphores(Concurrency{Console: 1, Background: 1})}
	i.proc.Store(&proc{payment: rpc})
	i.reported.Store(&map[string]bool{manifest.CapPaymentGateway: true})
	return i
}

// Only a declared gateway of a plugin with the capability whose process
// reported the service is a gateway.
func TestPaymentGatewayAccessor(t *testing.T) {
	i := paymentInstance(&paymentRPC{})
	if i.PaymentGateway("direct") == nil || i.PaymentGateway("wap") != nil {
		t.Fatal("declared gateways")
	}
	i.reported.Store(&map[string]bool{manifest.CapHTTPRoutes: true})
	if i.PaymentGateway("direct") != nil {
		t.Fatal("gateway of a process not serving the service")
	}
	i = paymentInstance(&paymentRPC{})
	i.caps = map[string]bool{}
	if i.PaymentGateway("direct") != nil {
		t.Fatal("gateway without the capability")
	}
}

// The responder answers with the plugin's replies to that notification, the
// default one for a status net/http cannot write, and only defaults when
// the plugin call failed.
func TestPaymentRespond(t *testing.T) {
	rpc := &paymentRPC{parsed: &pluginv1.ParsePaymentNotificationResponse{Verified: true,
		Handled: &pluginv1.PaymentReply{Body: []byte("success")}, Rejected: &pluginv1.PaymentReply{Status: 1000, Body: []byte("fail")},
		Retry: &pluginv1.PaymentReply{Status: 500, ContentType: "text/plain", Body: []byte("again")}}}
	gw := paymentInstance(rpc).PaymentGateway("direct")
	_, respond, err := gw.ParseNotification(context.Background(), core.PaymentNotification{})
	if core.AsError(err).Code != core.ErrInternal.Code {
		t.Fatalf("result without a status: %v", err)
	}
	for o, want := range map[core.PaymentOutcome]core.PaymentReply{
		core.PaymentHandled:    {Status: 200, ContentType: core.PlainTextReplyType, Body: []byte("success")},
		core.PaymentRejected:   {Status: 400, ContentType: core.PlainTextReplyType, Body: []byte("fail")},
		core.PaymentRetry:      {Status: 500, ContentType: "text/plain", Body: []byte("again")},
		core.PaymentOutcome(7): core.StatusPaymentReply(core.PaymentRetry),
	} {
		if r := respond(o); r.Status != want.Status || r.ContentType != want.ContentType || string(r.Body) != string(want.Body) {
			t.Fatalf("outcome %d: %+v", o, r)
		}
	}
	rpc.err = status.Error(codes.Unavailable, "down")
	if _, respond, _ = gw.ParseNotification(context.Background(), core.PaymentNotification{}); respond(core.PaymentRetry).Status != 503 {
		t.Fatalf("failed call: %+v", respond(core.PaymentRetry))
	}
}

// A gateway declared without query is not asked; plugin failures are
// errors, never results.
func TestPaymentAdapterErrors(t *testing.T) {
	ctx := context.Background()
	rpc := &paymentRPC{created: &pluginv1.CreatePaymentResponse{SessionId: "s"}}
	gw := paymentInstance(rpc).PaymentGateway("direct")
	if _, err := gw.QueryPayment(ctx, core.PaymentQuery{}); err != core.ErrPaymentQueryUnsupported || rpc.calls != 0 {
		t.Fatalf("undeclared query: %v %d", err, rpc.calls)
	}
	in := core.PaymentCreate{OutTradeNo: "s2a_1", Amount: decimal.NewFromInt(5), Currency: "JPY"}
	// An answer with nowhere to pay is passed on: the core checks every
	// gateway's answer the same way (payment.checkCreated).
	if out, err := gw.CreatePayment(ctx, in); err != nil || out.SessionID != "s" || out.PayURL != "" || out.QRCode != "" {
		t.Fatalf("nothing to pay with: %+v %v", out, err)
	}
	rpc.err = status.Error(codes.Unknown, "gateway said no")
	if _, err := gw.CreatePayment(ctx, in); core.AsError(err).Code != core.ErrInternal.Code {
		t.Fatalf("plugin error: %v", err)
	}
	rpc.err = status.Error(codes.Unavailable, "down")
	if _, _, err := gw.ParseNotification(ctx, core.PaymentNotification{}); core.AsError(err).Code != core.ErrPluginUnavailable.Code {
		t.Fatalf("unavailable: %v", err)
	}
}

// A missing reply, or one of another media type or over 64 KiB, is replaced
// by the default one; an allowed type keeps its parameters, none means
// text/plain.
func TestPaymentRespondBounds(t *testing.T) {
	cases := []struct {
		reply  *pluginv1.PaymentReply
		status int
		ctype  string
		body   string
	}{
		{&pluginv1.PaymentReply{Status: 200, ContentType: "text/html", Body: []byte("<script>")}, 400, core.PlainTextReplyType, "Bad Request"},
		{&pluginv1.PaymentReply{ContentType: "image/svg+xml", Body: []byte("<svg/>")}, 400, core.PlainTextReplyType, "Bad Request"},
		{&pluginv1.PaymentReply{ContentType: "text/plain;;", Body: []byte("x")}, 400, core.PlainTextReplyType, "Bad Request"},
		{&pluginv1.PaymentReply{ContentType: "text/plain", Body: make([]byte, maxPaymentReplyBytes+1)}, 400, core.PlainTextReplyType, "Bad Request"},
		{nil, 400, core.PlainTextReplyType, "Bad Request"},
		{&pluginv1.PaymentReply{Status: 409, ContentType: "Application/JSON; charset=UTF-8", Body: []byte(`{}`)}, 409, "application/json; charset=UTF-8", "{}"},
		{&pluginv1.PaymentReply{Body: []byte("fail")}, 400, "text/plain; charset=utf-8", "fail"},
	}
	for n, tc := range cases {
		gw := paymentInstance(&paymentRPC{parsed: &pluginv1.ParsePaymentNotificationResponse{Rejected: tc.reply}}).PaymentGateway("direct")
		_, respond, _ := gw.ParseNotification(context.Background(), core.PaymentNotification{})
		if r := respond(core.PaymentRejected); r.Status != tc.status || r.ContentType != tc.ctype || string(r.Body) != tc.body {
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
		gw := paymentInstance(&paymentRPC{parsed: &pluginv1.ParsePaymentNotificationResponse{Reason: reason}}).PaymentGateway("direct")
		_, _, err := gw.ParseNotification(context.Background(), core.PaymentNotification{})
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

// echoRPC answers every notification with replies made from its body, as a
// plugin whose replies depend on the notification would.
type echoRPC struct {
	pluginv1.PaymentGatewayServiceClient
}

func (echoRPC) ParsePaymentNotification(_ context.Context, in *pluginv1.ParsePaymentNotificationRequest, _ ...grpc.CallOption) (*pluginv1.ParsePaymentNotificationResponse, error) {
	time.Sleep(time.Millisecond) // let the calls overlap
	reply := &pluginv1.PaymentReply{Body: in.GetBody()}
	return &pluginv1.ParsePaymentNotificationResponse{Verified: true, Handled: reply, Rejected: reply, Retry: reply,
		Result: &pluginv1.PaymentResult{OutTradeNo: string(in.GetBody()), Status: pluginv1.PaymentStatus_PAYMENT_STATUS_PAID}}, nil
}

// Concurrent notifications of one gateway each get the replies of their
// own answer, however the calls interleave.
func TestPaymentRespondConcurrent(t *testing.T) {
	i := paymentInstance(echoRPC{})
	i.sems = newSemaphores(Concurrency{Console: 64, Background: 1})
	gw := i.PaymentGateway("direct")
	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for n := range 64 {
		wg.Go(func() {
			body := fmt.Sprintf("notification-%d", n)
			res, respond, err := gw.ParseNotification(context.Background(), core.PaymentNotification{Body: []byte(body)})
			if err != nil || res.OutTradeNo != body {
				errs <- fmt.Sprintf("%s: %+v %v", body, res, err)
				return
			}
			time.Sleep(time.Millisecond) // others parse in between
			if got := string(respond(core.PaymentHandled).Body); got != body {
				errs <- body + " answered with " + got
			}
		})
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}
