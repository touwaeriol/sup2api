package core

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/shopspring/decimal"

	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
)

// Online top-up (CONTRACTS §140): the core owns orders, settings, promo
// codes and crediting; a payment gateway only creates payments and tells
// which order a notification is about and what was paid. The built-in
// gateways (epay, stripe) and the gateways plugins declare
// ("payment.gateway.v1") implement the same PaymentGateway, so creating,
// notifying and querying have one code path in the payment module. A
// plugin gateway's id is "<plugin id>.<gateway id>"
// (manifest.PaymentGatewayName).

// PaymentStatus is what a gateway says about a payment.
type PaymentStatus string

const (
	PaymentPaid    PaymentStatus = "paid"
	PaymentPending PaymentStatus = "pending"
	PaymentFailed  PaymentStatus = "failed"
	PaymentExpired PaymentStatus = "expired"
)

// PaymentCreate asks a gateway for a payment of one order. Amount is what the
// user pays in Currency, already rounded to the currency's minor unit; the
// URLs are built by the core from the site address, never taken from a
// request.
type PaymentCreate struct {
	OutTradeNo string
	Amount     decimal.Decimal
	Currency   string // ISO 4217, upper case
	MethodType string // the payment method's type key (epay "alipay", ...)
	Subject    string
	NotifyURL  string
	ReturnURL  string
	ExpiresAt  time.Time
	ClientIP   string
}

// PaymentCreated is where the user pays. MerchantRef is the merchant the
// payment was created under (epay pid, ...): the core stores it on the order
// and a notification must name the same one.
type PaymentCreated struct {
	PayURL      string
	QRCode      string
	SessionID   string
	MerchantRef string
}

// PaymentNotification is a gateway's asynchronous notification as it
// arrived: the core has capped the body and dropped the cookie and
// authorization headers.
type PaymentNotification struct {
	Method   string
	RawQuery string
	Header   http.Header
	Body     []byte
}

// PaymentQuery identifies an order's payment for a status query.
type PaymentQuery struct {
	OutTradeNo  string
	SessionID   string
	MethodType  string
	MerchantRef string
}

// PaymentResult is a verified statement about one payment. Amount is in the
// currency's minor unit (PaymentMinorUnits). Empty MerchantRef or MethodType
// mean the gateway does not report them; when reported the core requires
// them to equal the order's.
type PaymentResult struct {
	OutTradeNo  string
	TradeNo     string
	SessionID   string
	Amount      int64
	Currency    string
	Status      PaymentStatus
	EventID     string
	MerchantRef string
	MethodType  string
	// Fields are whitelisted, non-secret fields of the notification for the
	// payment_events row (never the signature, keys or the whole body).
	Fields map[string]string
}

// PaymentGateway is one gateway. ParseNotification returns
// ErrPaymentSignature (wrapped) when the notification is not authentic, and
// QueryPayment ErrPaymentQueryUnsupported when the gateway cannot be asked.
//
// ParseNotification always returns a responder, also with an error: it
// answers this notification, whatever the core makes of it, and no other.
// A plugin's replies come with its answer to each notification, so
// concurrent notifications must not share them.
type PaymentGateway interface {
	CreatePayment(ctx context.Context, in PaymentCreate) (*PaymentCreated, error)
	ParseNotification(ctx context.Context, n PaymentNotification) (*PaymentResult, PaymentResponder, error)
	QueryPayment(ctx context.Context, q PaymentQuery) (*PaymentResult, error)
}

var (
	ErrPaymentSignature        = errors.New("payment notification failed verification")
	ErrPaymentQueryUnsupported = errors.New("payment gateway cannot be queried")
)

// PaymentMethodTemplate is a payment method a gateway offers for the
// settings' method templates.
type PaymentMethodTemplate struct {
	Type string        `json:"type"`
	Name LocalizedText `json:"name"`
	Icon string        `json:"icon,omitempty"`
}

// PaymentGatewayInfo describes a plugin gateway.
type PaymentGatewayInfo struct {
	ID      string                  `json:"id"` // "<plugin id>.<gateway id>"
	Name    LocalizedText           `json:"name"`
	Icon    string                  `json:"icon,omitempty"`
	Methods []PaymentMethodTemplate `json:"methods"`
	Query   bool                    `json:"query"`
}

// PaymentGatewayBinding is one gateway of an enabled plugin. Ids are unique
// by construction: plugin ids are, and a plugin's gateway ids are.
type PaymentGatewayBinding struct {
	Plugin  PluginInfo
	Gateway PaymentGatewayInfo
	Client  PaymentGateway
}

// PaymentGatewayGeneration is implemented by generations that know payment
// gateways. A gateway missing from the current generation (plugin disabled
// or uninstalled) is unavailable: its notifications are answered without a
// gateway, 503 while orders of it may still be paid, else 404.
type PaymentGatewayGeneration interface {
	PaymentGateways() []PaymentGatewayBinding
}

// The settlement currencies and their minor units are shared with the
// gateway plugins (sdk/protocol), which report amounts in those units.

// PaymentExponent is the number of decimals of a settlement currency's
// minor unit; 0 for any other currency, which PaymentMinorUnits refuses.
func PaymentExponent(currency string) int32 {
	exp, _ := protocol.PaymentExponent(currency)
	return exp
}

// PaymentMinorUnits converts an amount to the currency's minor unit; ok is
// false unless the currency is a settlement currency and the amount is
// positive and exact in that unit.
func PaymentMinorUnits(amount decimal.Decimal, currency string) (int64, bool) {
	return protocol.PaymentMinorUnits(amount.String(), currency)
}
