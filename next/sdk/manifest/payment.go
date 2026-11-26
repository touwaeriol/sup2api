package manifest

// CapPaymentGateway is the capability of a plugin serving
// PaymentGatewayService for the gateways it declares in payments.
const CapPaymentGateway = "payment.gateway.v1"

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
	ID   string        `json:"id"` // ^[a-z][a-z0-9_]{1,29}$, unique within the plugin
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

// PaymentGatewayName is the site-wide name of a plugin's gateway.
func PaymentGatewayName(pluginID, gatewayID string) string { return pluginID + "." + gatewayID }
