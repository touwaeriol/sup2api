package pluginsdk

import (
	"slices"
	"testing"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

func TestPaymentHelpers(t *testing.T) {
	if caps := Capabilities(PaymentGatewayMux{}); !slices.Equal(caps, []string{manifest.CapPaymentGateway}) {
		t.Fatalf("capabilities = %v", caps)
	}
	h := NotificationHeader(&pluginv1.ParseNotificationRequest{Headers: map[string]*pluginv1.HeaderValues{
		"stripe-signature": {Values: []string{"t=1,v1=x"}}, "X-A": {Values: []string{"1", "2"}}}})
	if h.Get("Stripe-Signature") != "t=1,v1=x" || len(h.Values("X-A")) != 2 {
		t.Fatalf("header = %v", h)
	}
	replies := PaymentReplies{Handled: TextReply(200, "success"), Rejected: TextReply(400, "fail")}
	if r := RejectedNotification("bad signature", replies); r.GetVerified() || r.GetReason() != "bad signature" ||
		string(r.GetHandled().GetBody()) != "success" || r.GetRetry() != nil {
		t.Fatalf("rejected = %v", r)
	}
	res := &pluginv1.PaymentResult{OutTradeNo: "s2a_1"}
	if r := VerifiedNotification(res, replies); !r.GetVerified() || r.GetResult() != res || r.GetRejected().GetStatus() != 400 {
		t.Fatalf("verified = %v", r)
	}
}
