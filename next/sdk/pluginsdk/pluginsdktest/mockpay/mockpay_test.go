package mockpay_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest/mockpay"
)

func start(t *testing.T) pluginv1.PaymentGatewayServiceClient {
	h := pluginsdktest.Start(t, mockpay.New(), pluginsdktest.Options{SDK: []pluginsdk.Option{pluginsdk.WithInfo("mockpay", "0.1.0")}})
	if !slices.Contains(h.Info.GetCapabilities(), manifest.CapPaymentGateway) {
		t.Fatalf("capabilities = %v", h.Info.GetCapabilities())
	}
	return h.Payment
}

func notification(gw string, n mockpay.Notification, sig string) *pluginv1.ParsePaymentNotificationRequest {
	body, _ := json.Marshal(n)
	if sig == "" {
		sig = mockpay.Sign(body)
	}
	return &pluginv1.ParsePaymentNotificationRequest{Gateway: gw, Method: "POST", Body: body,
		Headers: map[string]*pluginv1.HeaderValues{mockpay.SignatureHeader: {Values: []string{sig}}}}
}

// The three calls cross the SDK's gRPC service as they are, routed by the
// gateway id.
func TestMockGatewayOverGRPC(t *testing.T) {
	c, ctx := start(t), context.Background()
	created, err := c.CreatePayment(ctx, &pluginv1.CreatePaymentRequest{Gateway: mockpay.GatewayMock, OutTradeNo: "s2a_1",
		Amount: "12.30", AmountMinor: 1230, Currency: "USD", MethodType: mockpay.MethodType})
	if err != nil || created.GetPayUrl() == "" || created.GetSessionId() != "ms_s2a_1" || created.GetMerchantRef() != mockpay.MerchantRef {
		t.Fatalf("create: %v %v", created, err)
	}
	q, err := c.QueryPayment(ctx, &pluginv1.QueryPaymentRequest{Gateway: mockpay.GatewayMock, OutTradeNo: "s2a_1"})
	if err != nil || q.GetResult().GetStatus() != pluginv1.PaymentStatus_PAYMENT_STATUS_PENDING || q.GetResult().GetAmountMinor() != 1230 {
		t.Fatalf("query pending: %v %v", q, err)
	}
	paid := mockpay.Notification{EventID: "e1", OutTradeNo: "s2a_1", TradeNo: "t1", Amount: 1230, Currency: "USD", Status: "paid"}
	parsed, err := c.ParsePaymentNotification(ctx, notification(mockpay.GatewayMock, paid, ""))
	if r := parsed.GetResult(); err != nil || !parsed.GetVerified() || r.GetTradeNo() != "t1" || r.GetEventId() != "e1" ||
		r.GetStatus() != pluginv1.PaymentStatus_PAYMENT_STATUS_PAID || string(parsed.GetHandled().GetBody()) != "success" {
		t.Fatalf("parse: %v %v", parsed, err)
	}
	q, err = c.QueryPayment(ctx, &pluginv1.QueryPaymentRequest{Gateway: mockpay.GatewayMock, OutTradeNo: "s2a_1"})
	if err != nil || q.GetResult().GetStatus() != pluginv1.PaymentStatus_PAYMENT_STATUS_PAID {
		t.Fatalf("query paid: %v %v", q, err)
	}
}

func TestMockGatewayRefusals(t *testing.T) {
	c, ctx := start(t), context.Background()
	n := mockpay.Notification{EventID: "e1", OutTradeNo: "s2a_1", Amount: 1, Currency: "USD", Status: "paid"}
	parsed, err := c.ParsePaymentNotification(ctx, notification(mockpay.GatewayMock, n, "00"))
	if err != nil || parsed.GetVerified() || parsed.GetReason() != "bad signature" || parsed.GetRejected().GetStatus() != 400 {
		t.Fatalf("bad signature: %v %v", parsed, err)
	}
	n.Status = "refunded"
	if parsed, err = c.ParsePaymentNotification(ctx, notification(mockpay.GatewayMock, n, "")); err != nil || parsed.GetVerified() {
		t.Fatalf("unknown status: %v %v", parsed, err)
	}
	_, err = c.QueryPayment(ctx, &pluginv1.QueryPaymentRequest{Gateway: mockpay.GatewayNoQuery, OutTradeNo: "s2a_1"})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("no query: %v", err)
	}
	_, err = c.CreatePayment(ctx, &pluginv1.CreatePaymentRequest{Gateway: "other", OutTradeNo: "s2a_2", AmountMinor: 1})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("unknown gateway: %v", err)
	}
}
