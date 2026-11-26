package check

import (
	"fmt"
	"regexp"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// Bounds of manifest payments.
const (
	MaxPaymentGateways = 8
	MaxPaymentMethods  = 16
)

// paymentKeyRe is a payment method type key or icon key: it fits the
// core's payment_methods columns (varchar(50)) and needs no escaping in a
// URL or a log line.
var paymentKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,49}$`)

// payments validates manifest payments: the declaration, the capability and
// the host permission come together or not at all.
func (v *validator) payments() {
	p := v.m.Payments
	if p == nil {
		if v.hasCap(manifest.CapPaymentGateway) {
			v.add("payments", "required", "%s requires payments", manifest.CapPaymentGateway)
		}
		if _, ok := v.perms[manifest.PermPaymentGateway]; ok {
			v.add("payments", "required", "host permission %q requires payments", manifest.PermPaymentGateway)
		}
		return
	}
	v.needCap("payments", manifest.CapPaymentGateway)
	v.needPerm("payments", manifest.PermPaymentGateway, "payment gateways")
	v.count("payments.gateways", len(p.Gateways), MaxPaymentGateways, "gateways")
	seen := map[string]bool{}
	for i, gw := range p.Gateways {
		f := fmt.Sprintf("payments.gateways[%d]", i)
		v.newKey(f+".id", "gateway id", gw.ID, keyRe, seen)
		v.paymentGateway(f, gw)
	}
}

// paymentGateway checks a gateway's name, icon and method templates.
func (v *validator) paymentGateway(f string, gw manifest.PaymentGateway) {
	if firstText(gw.Name) == "" {
		v.add(f+".name", "required", "name is required")
	}
	v.paymentIcon(f+".icon", gw.Icon)
	v.count(f+".methods", len(gw.Methods), MaxPaymentMethods, "methods")
	seen := map[string]bool{}
	for i, m := range gw.Methods {
		mf := fmt.Sprintf("%s.methods[%d]", f, i)
		v.newKey(mf+".type", "method type", m.Type, paymentKeyRe, seen)
		if firstText(m.Name) == "" {
			v.add(mf+".name", "required", "name is required")
		}
		v.paymentIcon(mf+".icon", m.Icon)
	}
}

func (v *validator) paymentIcon(field, icon string) {
	if icon != "" && !paymentKeyRe.MatchString(icon) {
		v.add(field, "invalid_format", "icon %q must match %s", icon, paymentKeyRe.String())
	}
}
