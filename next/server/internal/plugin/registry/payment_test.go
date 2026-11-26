package registry_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry/registrytest"
)

// payExt is a plugin whose runtime serves PaymentGatewayService.
type payExt struct{ ext }

func (payExt) PaymentGateway(id string) core.PaymentGateway { return payStub{id} }

type payStub struct{ id string }

func (payStub) CreatePayment(context.Context, core.PaymentCreate) (*core.PaymentCreated, error) {
	return nil, nil
}
func (payStub) ParseNotification(context.Context, core.PaymentNotification) (*core.PaymentResult, error) {
	return nil, nil
}
func (payStub) QueryPayment(context.Context, core.PaymentQuery) (*core.PaymentResult, error) {
	return nil, nil
}
func (payStub) Respond(core.PaymentOutcome) core.PaymentReply { return core.PaymentReply{} }

func paymentManifest(key string, ids ...string) *manifest.Manifest {
	m := registrytest.Manifest(key, "1.0.0")
	m.Platforms, m.AccountTypes = nil, nil
	m.Capabilities = append(m.Capabilities, manifest.Capability{ID: manifest.CapPaymentGateway})
	m.Payments = &manifest.Payments{}
	for _, id := range ids {
		m.Payments.Gateways = append(m.Payments.Gateways, manifest.PaymentGateway{ID: id, Name: manifest.LocalizedText{"en": id},
			Icon: "card", Query: id == "direct", Methods: []manifest.PaymentMethod{{Type: id + "_pay", Name: manifest.LocalizedText{"en": "Pay"}, Icon: "alipay"}}})
	}
	return m
}

var payGrant = registry.Grants{manifest.PermPaymentGateway: []byte(`{}`)}

func paymentGateways(exts ...registry.Extension) []core.PaymentGatewayBinding {
	return registry.New().Publish(exts).(core.PaymentGatewayGeneration).PaymentGateways()
}

// A gateway is "<plugin id>.<gateway id>", bound to the plugin's client with
// its declaration; it needs the capability, the grant and the service.
func TestPaymentGatewayBindings(t *testing.T) {
	alipay, third := load(t, paymentManifest("alipay", "direct", "wap")), paymentManifest("alipay", "direct")
	third.Publisher = "erio"
	thirdPkg := load(t, third)
	got := paymentGateways(payExt{ext{pkg: alipay, grants: payGrant}}, payExt{ext{pkg: thirdPkg, grants: payGrant}})
	// Plugins come in key order: "_erio__alipay" before "alipay".
	if len(got) != 3 || got[0].Gateway.ID != "_erio__alipay.direct" || got[1].Gateway.ID != "alipay.direct" || got[2].Gateway.ID != "alipay.wap" {
		t.Fatalf("gateways = %+v", got)
	}
	gw := got[1].Gateway
	if got[1].Plugin.Key != "alipay" || got[1].Client.(payStub).id != "direct" || !gw.Query || got[2].Gateway.Query ||
		gw.Name["en"] != "direct" || gw.Icon != "card" || len(gw.Methods) != 1 || gw.Methods[0].Type != "direct_pay" || gw.Methods[0].Icon != "alipay" {
		t.Fatalf("binding = %+v", got[1])
	}
	noCap := paymentManifest("nocap", "direct")
	noCap.Capabilities = noCap.Capabilities[:len(noCap.Capabilities)-1]
	for name, e := range map[string]registry.Extension{
		"no grant":      payExt{ext{pkg: alipay}},
		"no capability": payExt{ext{pkg: load(t, noCap), grants: payGrant}},
		"no service":    ext{pkg: alipay, grants: payGrant},
	} {
		if got := paymentGateways(e); len(got) != 0 {
			t.Fatalf("%s: %+v", name, got)
		}
	}
}

// unservedExt is a plugin whose process does not serve the service: the
// runtime has no client for any of its gateways.
type unservedExt struct{ ext }

func (unservedExt) PaymentGateway(string) core.PaymentGateway { return nil }

// A name already bound, or a gateway the process does not serve, is
// skipped and the plugin is abnormal.
func TestPaymentGatewayConflicts(t *testing.T) {
	for name, tc := range map[string]struct {
		e      registry.Extension
		bound  int
		reason string
	}{
		"duplicate": {payExt{ext{pkg: load(t, paymentManifest("mockpay", "mock", "mock")), grants: payGrant}}, 1, "already declared by plugin mockpay"},
		"unserved":  {unservedExt{ext{pkg: load(t, paymentManifest("mockpay", "mock")), grants: payGrant}}, 0, "does not serve payment.gateway.v1"},
	} {
		g := registry.New().Publish([]registry.Extension{tc.e})
		if got := g.(core.PaymentGatewayGeneration).PaymentGateways(); len(got) != tc.bound {
			t.Fatalf("%s: gateways = %+v", name, got)
		}
		info, _ := g.Plugin("mockpay")
		if len(info.Conflicts) != 1 || info.Conflicts[0].Resource != "payment_gateway" || info.Conflicts[0].ID != "mockpay.mock" ||
			!strings.Contains(info.Conflicts[0].Reason, tc.reason) {
			t.Fatalf("%s: conflicts = %+v", name, info.Conflicts)
		}
	}
}

// A plugin no longer in the generation (disabled, uninstalled) or whose
// grant was revoked has no gateway, which the core answers with a retry.
func TestPaymentGatewayDisableAndRevoke(t *testing.T) {
	pkg := load(t, paymentManifest("mockpay", "mock"))
	for name, next := range map[string][]registry.Extension{
		"disabled": nil,
		"revoked":  {payExt{ext{pkg: pkg, grants: registry.Grants{}}}},
	} {
		r := registry.New()
		if got := r.Publish([]registry.Extension{payExt{ext{pkg: pkg, grants: payGrant}}}).(core.PaymentGatewayGeneration).PaymentGateways(); len(got) != 1 {
			t.Fatalf("%s: before: %+v", name, got)
		}
		if got := r.Publish(next).(core.PaymentGatewayGeneration).PaymentGateways(); len(got) != 0 {
			t.Fatalf("%s: after: %+v", name, got)
		}
	}
}
