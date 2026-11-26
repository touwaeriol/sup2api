package grpcruntime_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest/mockpay"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry/registrytest"
)

// paymentPlugin is built once for the package's tests.
var paymentPlugin = sync.OnceValues(func() ([]byte, error) { return buildFixture("./testdata/paymentplugin") })

// paymentGateways loads the simulated gateway plugin (testdata/paymentplugin)
// as a process and returns the bindings of a generation holding it. Its
// second gateway is declared queryable, so the plugin's own UNIMPLEMENTED
// answer is what tells it cannot be queried.
func paymentGateways(t *testing.T) (*payPlugin, map[string]core.PaymentGatewayBinding) {
	t.Helper()
	bin, err := paymentPlugin()
	if err != nil {
		t.Fatalf("build payment plugin: %v", err)
	}
	grants := registrytest.DefaultGrants()
	grants[manifest.PermPaymentGateway] = `{}`
	e := setupPackage(t, grants, bin, func(m *manifest.Manifest) {
		m.Platforms, m.AccountTypes, m.Hooks, m.Routes = nil, nil, nil, nil
		m.Capabilities = []manifest.Capability{{ID: manifest.CapPaymentGateway}}
		m.Payments = mockpay.Payments()
		m.Payments.Gateways[1].Query = true
	})
	inst := e.load(t, e.runtime(t, nil))
	out := map[string]core.PaymentGatewayBinding{}
	for _, b := range registry.New().Publish([]registry.Extension{inst}).(core.PaymentGatewayGeneration).PaymentGateways() {
		out[b.Gateway.ID] = b
	}
	if len(out) != 2 {
		t.Fatalf("gateways = %v", out)
	}
	return &payPlugin{key: e.key, stop: inst.Stop}, out
}

type payPlugin struct {
	key  string
	stop func()
}

func signed(t *testing.T, n mockpay.Notification) core.PaymentNotification {
	body, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	return core.PaymentNotification{Method: "POST", RawQuery: "a=1", Body: body,
		Header: http.Header{mockpay.SignatureHeader: {mockpay.Sign(body)}, "Content-Type": {"application/json"}}}
}

// The three calls cross a real plugin process through the binding's client.
// The adapter hands over what the plugin reports as it is - an amount or an
// order that does not match is the core's to refuse - and maps an
// unauthentic notification and an unqueryable gateway to the core errors.
func TestPaymentGatewayAcrossProcess(t *testing.T) {
	inst, gws := paymentGateways(t)
	gw := gws[inst.key+".mock"].Client
	assertPaymentCreate(t, gw)
	assertPaymentNotifications(t, gw)
	assertPaymentErrors(t, inst, gws)
}

// assertPaymentCreate: a created payment is pending on a query; an amount
// not exact in the minor unit never reaches the plugin.
func assertPaymentCreate(t *testing.T, gw core.PaymentGateway) {
	ctx := context.Background()
	created, err := gw.CreatePayment(ctx, core.PaymentCreate{OutTradeNo: "s2a_1", Amount: decimal.RequireFromString("12.3"), Currency: "USD",
		MethodType: mockpay.MethodType, ExpiresAt: time.Now().Add(30 * time.Minute)})
	if err != nil || created.PayURL != "https://mockpay.invalid/pay/s2a_1" || created.SessionID != "ms_s2a_1" || created.MerchantRef != mockpay.MerchantRef {
		t.Fatalf("create: %+v %v", created, err)
	}
	if _, err := gw.CreatePayment(ctx, core.PaymentCreate{OutTradeNo: "s2a_2", Amount: decimal.RequireFromString("1.005"), Currency: "USD"}); core.AsError(err).Code != core.ErrInvalidArgument.Code {
		t.Fatalf("inexact amount: %v", err)
	}
	if q, err := gw.QueryPayment(ctx, core.PaymentQuery{OutTradeNo: "s2a_1"}); err != nil || q.Status != core.PaymentPending || q.Amount != 1230 {
		t.Fatalf("query: %+v %v", q, err)
	}
}

// assertPaymentNotifications: a signed notification is passed on unchanged
// (another order and amount than created), a forged one is
// ErrPaymentSignature, and each gets the plugin's reply.
func assertPaymentNotifications(t *testing.T, gw core.PaymentGateway) {
	ctx := context.Background()
	n := mockpay.Notification{EventID: "e1", OutTradeNo: "s2a_other", TradeNo: "t1", Amount: 99999, Currency: "EUR", Status: "paid", MethodType: "x"}
	r, respond, err := gw.ParseNotification(ctx, signed(t, n))
	if err != nil || r.OutTradeNo != "s2a_other" || r.TradeNo != "t1" || r.Amount != 99999 || r.Currency != "EUR" || r.Status != core.PaymentPaid ||
		r.EventID != "e1" || r.MethodType != "x" || r.MerchantRef != mockpay.MerchantRef || r.SessionID != "ms_s2a_other" || r.Fields["trade_no"] != "t1" {
		t.Fatalf("parse: %+v %v", r, err)
	}
	if reply := respond(core.PaymentHandled); reply.Status != 200 || string(reply.Body) != "success" || reply.ContentType != "text/plain; charset=utf-8" {
		t.Fatalf("handled reply: %+v", reply)
	}
	forged := signed(t, n)
	forged.Header.Set(mockpay.SignatureHeader, "00")
	_, respond, err = gw.ParseNotification(ctx, forged)
	if !errors.Is(err, core.ErrPaymentSignature) || !strings.HasSuffix(err.Error(), ": bad signature") {
		t.Fatalf("forged: %v", err)
	}
	if reply := respond(core.PaymentRejected); reply.Status != 400 || string(reply.Body) != "fail" {
		t.Fatalf("rejected reply: %+v", reply)
	}
}

// assertPaymentErrors: plugin errors stay errors, and a stopped plugin
// answers unavailable (the registry drops its gateways with it).
func assertPaymentErrors(t *testing.T, inst *payPlugin, gws map[string]core.PaymentGatewayBinding) {
	ctx := context.Background()
	if _, err := gws[inst.key+".mock_noquery"].Client.QueryPayment(ctx, core.PaymentQuery{OutTradeNo: "s2a_1"}); !errors.Is(err, core.ErrPaymentQueryUnsupported) {
		t.Fatalf("no query: %v", err)
	}
	gw := gws[inst.key+".mock"].Client
	if _, err := gw.QueryPayment(ctx, core.PaymentQuery{OutTradeNo: "s2a_unknown"}); core.AsError(err).Code != core.ErrNotFound.Code {
		t.Fatalf("unknown order: %v", err)
	}
	inst.stop()
	if _, err := gw.QueryPayment(ctx, core.PaymentQuery{OutTradeNo: "s2a_1"}); core.AsError(err).Code != core.ErrPluginUnavailable.Code {
		t.Fatalf("stopped: %v", err)
	}
}
