package manifest

import (
	"regexp"
	"strings"
)

// PermPaymentGateway is the host permission a payment gateway plugin asks
// for: whatever it verifies can mark its own gateway's orders paid (for the
// order's amount, which the core checks), so the administrator consents.
const PermPaymentGateway = "payment.gateway"

// Payments declares the payment gateways a plugin provides
// ("payment.gateway.v1", CONTRACTS §140). The core names a gateway
// "<plugin id>.<gateway id>", unique across the site, and takes its
// notifications on /api/v1/payment/notify/<that name>; orders, amounts and
// crediting stay in the core.
type Payments struct {
	Gateways []PaymentGateway `json:"gateways"`
}

// PaymentGateway is one gateway of a plugin.
type PaymentGateway struct {
	ID   string        `json:"id"` // KeyPattern, unique within the plugin
	Name LocalizedText `json:"name"`
	Icon string        `json:"icon,omitempty"` // icon key, see PaymentMethod.Icon
	// Methods are the payment methods it offers, the templates an
	// administrator configures payment methods from.
	Methods []PaymentMethod `json:"methods"`
	// Query says the gateway answers QueryPayment, so the core can recover a
	// payment whose notification was lost.
	Query bool `json:"query,omitempty"`
}

// PaymentMethod is a payment method template of a gateway.
type PaymentMethod struct {
	Type string        `json:"type"` // the type key sent to the gateway, unique within the gateway
	Name LocalizedText `json:"name"`
	// Icon is a console icon key (alipay, wxpay, qqpay, stripe, card); any
	// other key shows the name's initial.
	Icon string `json:"icon,omitempty"`
}

// KeyPattern is the shape of a plugin-local id: a payment gateway's, a
// notification channel's, a permission group's key. It holds no dot, so
// PaymentGatewayName can be read back.
var KeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,29}$`)

const gatewayNameSep = "."

// PaymentGatewayName is the site-wide name of a plugin's gateway.
func PaymentGatewayName(pluginID, gatewayID string) string {
	return pluginID + gatewayNameSep + gatewayID
}

// ParsePaymentGatewayName splits a name PaymentGatewayName made into its
// plugin id and gateway id; ok is false for any other string, which
// includes the built-in gateways' names. The plugin key, a first-party
// plugin's whole id, must have a key's shape too: ParsePluginID alone takes
// any non-empty string for a first-party id.
func ParsePaymentGatewayName(name string) (pluginID, gatewayID string, ok bool) {
	pluginID, gatewayID, ok = strings.Cut(name, gatewayNameSep)
	if !ok || len(pluginID) > PluginIDMaxLen || !KeyPattern.MatchString(gatewayID) {
		return "", "", false
	}
	if _, key, ok := ParsePluginID(pluginID); !ok || !KeyPattern.MatchString(key) {
		return "", "", false
	}
	return pluginID, gatewayID, true
}
