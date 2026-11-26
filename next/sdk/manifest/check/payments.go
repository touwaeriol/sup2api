package check

import (
	"fmt"
	"regexp"
	"slices"

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
		if slices.ContainsFunc(v.m.Capabilities, func(c manifest.Capability) bool { return c.ID == manifest.CapPaymentGateway }) {
			v.add("payments", "required", "%s requires payments", manifest.CapPaymentGateway)
		}
		if _, ok := v.perms[manifest.PermPaymentGateway]; ok {
			v.add("payments", "required", "host permission %q requires payments", manifest.PermPaymentGateway)
		}
		return
	}
	v.needCap("payments", manifest.CapPaymentGateway)
	v.needPerm("payments", manifest.PermPaymentGateway, "payment gateways")
	if len(p.Gateways) == 0 || len(p.Gateways) > MaxPaymentGateways {
		v.add("payments.gateways", "invalid", "declare between one and %d gateways", MaxPaymentGateways)
	}
	seen := map[string]bool{}
	for i, gw := range p.Gateways {
		f := fmt.Sprintf("payments.gateways[%d]", i)
		if !keyRe.MatchString(gw.ID) {
			v.add(f+".id", "invalid_format", "gateway id %q must match %s", gw.ID, keyRe.String())
		} else if seen[gw.ID] {
			v.add(f+".id", "duplicate", "gateway %q declared twice", gw.ID)
		}
		seen[gw.ID] = true
		v.paymentGateway(f, gw)
	}
}

// paymentGateway checks a gateway's name, icon and method templates.
func (v *validator) paymentGateway(f string, gw manifest.PaymentGateway) {
	if firstText(gw.Name) == "" {
		v.add(f+".name", "required", "name is required")
	}
	v.paymentIcon(f+".icon", gw.Icon)
	if len(gw.Methods) == 0 || len(gw.Methods) > MaxPaymentMethods {
		v.add(f+".methods", "invalid", "declare between one and %d methods", MaxPaymentMethods)
	}
	seen := map[string]bool{}
	for i, m := range gw.Methods {
		mf := fmt.Sprintf("%s.methods[%d]", f, i)
		if !paymentKeyRe.MatchString(m.Type) {
			v.add(mf+".type", "invalid_format", "method type %q must match %s", m.Type, paymentKeyRe.String())
		} else if seen[m.Type] {
			v.add(mf+".type", "duplicate", "method type %q declared twice", m.Type)
		}
		seen[m.Type] = true
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
