package check

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

func paymentManifest() *manifest.Manifest {
	method := manifest.PaymentMethod{Type: "alipay", Name: manifest.LocalizedText{"en": "Alipay"}, Icon: "alipay"}
	gw := manifest.PaymentGateway{ID: "direct", Name: manifest.LocalizedText{"en": "Alipay direct"}, Icon: "alipay",
		Methods: []manifest.PaymentMethod{method}, Query: true}
	return &manifest.Manifest{
		Capabilities:    []manifest.Capability{{ID: manifest.CapPaymentGateway}},
		HostPermissions: []manifest.HostPermission{{ID: manifest.PermPaymentGateway}},
		Payments:        &manifest.Payments{Gateways: []manifest.PaymentGateway{gw}},
	}
}

// validatePayments runs the host permission and payments rules on m.
func validatePayments(m *manifest.Manifest) []FieldError {
	v := &validator{m: m, perms: map[string]*manifest.HostPermission{}}
	v.hostPermissions()
	v.payments()
	return v.errs
}

// paymentCase changes the valid manifest; field is the one error expected,
// "" for none.
type paymentCase struct {
	mod   func(m *manifest.Manifest, gw *manifest.PaymentGateway)
	field string
}

// The capability, the host permission and payments come together.
var paymentPairingCases = map[string]paymentCase{
	"valid":                 {func(*manifest.Manifest, *manifest.PaymentGateway) {}, ""},
	"no capability":         {func(m *manifest.Manifest, _ *manifest.PaymentGateway) { m.Capabilities = nil }, "payments"},
	"no host permission":    {func(m *manifest.Manifest, _ *manifest.PaymentGateway) { m.HostPermissions = nil }, "payments"},
	"capability alone":      {func(m *manifest.Manifest, _ *manifest.PaymentGateway) { m.Payments, m.HostPermissions = nil, nil }, "payments"},
	"host permission alone": {func(m *manifest.Manifest, _ *manifest.PaymentGateway) { m.Payments, m.Capabilities = nil, nil }, "payments"},
}

// Gateways: 1-8, ids of the key form (no dot), unique.
var paymentGatewayCases = map[string]paymentCase{
	"no gateways": {func(m *manifest.Manifest, _ *manifest.PaymentGateway) { m.Payments.Gateways = nil }, "payments.gateways"},
	"9 gateways":  {func(m *manifest.Manifest, gw *manifest.PaymentGateway) { m.Payments.Gateways = gateways(*gw, 9) }, "payments.gateways"},
	"8 gateways":  {func(m *manifest.Manifest, gw *manifest.PaymentGateway) { m.Payments.Gateways = gateways(*gw, 8) }, ""},
	"bad id":      {func(_ *manifest.Manifest, gw *manifest.PaymentGateway) { gw.ID = "Direct" }, "payments.gateways[0].id"},
	"dotted id":   {func(_ *manifest.Manifest, gw *manifest.PaymentGateway) { gw.ID = "direct.pay" }, "payments.gateways[0].id"},
	"duplicate id": {func(m *manifest.Manifest, _ *manifest.PaymentGateway) {
		m.Payments.Gateways = append(m.Payments.Gateways, m.Payments.Gateways[0])
	}, "payments.gateways[1].id"},
	"no name":  {func(_ *manifest.Manifest, gw *manifest.PaymentGateway) { gw.Name = nil }, "payments.gateways[0].name"},
	"bad icon": {func(_ *manifest.Manifest, gw *manifest.PaymentGateway) { gw.Icon = "../logo.svg" }, "payments.gateways[0].icon"},
}

// Methods: 1-16 per gateway, type keys unique and fitting varchar(50).
var paymentMethodCases = map[string]paymentCase{
	"no methods": {func(_ *manifest.Manifest, gw *manifest.PaymentGateway) { gw.Methods = nil }, "payments.gateways[0].methods"},
	"17 methods": {func(_ *manifest.Manifest, gw *manifest.PaymentGateway) { gw.Methods = methods(gw.Methods[0], 17) }, "payments.gateways[0].methods"},
	"16 methods": {func(_ *manifest.Manifest, gw *manifest.PaymentGateway) { gw.Methods = methods(gw.Methods[0], 16) }, ""},
	"bad type":   {func(_ *manifest.Manifest, gw *manifest.PaymentGateway) { gw.Methods[0].Type = "Ali Pay" }, "payments.gateways[0].methods[0].type"},
	"long type": {func(_ *manifest.Manifest, gw *manifest.PaymentGateway) {
		gw.Methods[0].Type = "a" + strings.Repeat("b", 50)
	}, "payments.gateways[0].methods[0].type"},
	"no type name":  {func(_ *manifest.Manifest, gw *manifest.PaymentGateway) { gw.Methods[0].Name = nil }, "payments.gateways[0].methods[0].name"},
	"bad type icon": {func(_ *manifest.Manifest, gw *manifest.PaymentGateway) { gw.Methods[0].Icon = "<svg>" }, "payments.gateways[0].methods[0].icon"},
	"duplicate type": {func(_ *manifest.Manifest, gw *manifest.PaymentGateway) {
		gw.Methods = append(gw.Methods, gw.Methods[0])
	}, "payments.gateways[0].methods[1].type"},
}

// gateways is n copies of gw with distinct ids.
func gateways(gw manifest.PaymentGateway, n int) []manifest.PaymentGateway {
	out := make([]manifest.PaymentGateway, n)
	for i := range out {
		out[i] = gw
		out[i].ID = fmt.Sprintf("gw%d", i)
	}
	return out
}

// methods is n copies of m with distinct types.
func methods(m manifest.PaymentMethod, n int) []manifest.PaymentMethod {
	out := make([]manifest.PaymentMethod, n)
	for i := range out {
		out[i] = m
		out[i].Type = fmt.Sprintf("m%d", i)
	}
	return out
}

func TestPaymentDeclaration(t *testing.T) {
	for _, cases := range []map[string]paymentCase{paymentPairingCases, paymentGatewayCases, paymentMethodCases} {
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) { checkPaymentCase(t, tc) })
		}
	}
}

func checkPaymentCase(t *testing.T, tc paymentCase) {
	m := paymentManifest()
	tc.mod(m, &m.Payments.Gateways[0])
	errs := validatePayments(m)
	if tc.field == "" && len(errs) != 0 {
		t.Fatal(errs)
	}
	if tc.field != "" && (len(errs) != 1 || errs[0].Field != tc.field) {
		t.Fatalf("want one error on %s, got %v", tc.field, errs)
	}
}

// The capability is known and the host permission is high risk, so an
// administrator consents to it explicitly.
func TestPaymentGatewayContract(t *testing.T) {
	if !KnownCapabilities[manifest.CapPaymentGateway] {
		t.Fatal("payment.gateway.v1 unknown")
	}
	if manifest.HostPermissionRisk[manifest.PermPaymentGateway] != manifest.RiskHigh {
		t.Fatalf("risk = %q", manifest.HostPermissionRisk[manifest.PermPaymentGateway])
	}
	if got := manifest.PaymentGatewayName("_erio__alipay", "direct"); got != "_erio__alipay.direct" {
		t.Fatalf("name = %q", got)
	}
}
