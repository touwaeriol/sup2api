package core

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Online top-up (CONTRACTS §138): the core owns orders, settings, promo
// codes and crediting; a payment gateway only creates payments and tells
// which order a notification is about and what was paid. The built-in
// gateways (epay, stripe) and the gateways plugins declare
// ("payment.gateway.v1") implement the same PaymentGateway, so creating,
// notifying and querying have one code path in the payment module.

// Built-in gateway ids. A plugin gateway's id is "<plugin id>.<gateway id>".
const (
	PaymentGatewayEpay   = "epay"
	PaymentGatewayStripe = "stripe"
)

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

// PaymentOutcome is how the core handled a notification; the gateway turns
// it into the reply its sender expects (Respond).
type PaymentOutcome int

const (
	// PaymentHandled: processed, a duplicate, or about no order of this site.
	PaymentHandled PaymentOutcome = iota
	// PaymentRejected: failed verification or did not match the order.
	PaymentRejected
	// PaymentRetry: the core could not process it now (database, ledger,
	// gateway unavailable); the sender should retry.
	PaymentRetry
)

// PaymentReply is the HTTP answer to a notification.
type PaymentReply struct {
	Status      int
	ContentType string
	Body        []byte
}

// PaymentGateway is one gateway. ParseNotification returns
// ErrPaymentSignature (wrapped) when the notification is not authentic, and
// QueryPayment ErrPaymentQueryUnsupported when the gateway cannot be asked.
type PaymentGateway interface {
	CreatePayment(ctx context.Context, in PaymentCreate) (*PaymentCreated, error)
	ParseNotification(ctx context.Context, n PaymentNotification) (*PaymentResult, error)
	QueryPayment(ctx context.Context, q PaymentQuery) (*PaymentResult, error)
	Respond(o PaymentOutcome) PaymentReply
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
// across a generation: the first claimant wins.
type PaymentGatewayBinding struct {
	Plugin  PluginInfo
	Gateway PaymentGatewayInfo
	Client  PaymentGateway
}

// PaymentGatewayGeneration is implemented by generations that know payment
// gateways. A gateway missing from the current generation (plugin disabled
// or uninstalled) is unavailable: its notifications get PaymentRetry.
type PaymentGatewayGeneration interface {
	PaymentGateways() []PaymentGatewayBinding
}

// zeroDecimalCurrencies have no minor unit (ISO 4217 exponent 0) among the
// currencies a top-up may settle in.
var zeroDecimalCurrencies = map[string]bool{"JPY": true, "KRW": true, "VND": true, "CLP": true, "ISK": true}

// PaymentExponent is the number of decimals of currency's minor unit.
func PaymentExponent(currency string) int32 {
	if zeroDecimalCurrencies[strings.ToUpper(currency)] {
		return 0
	}
	return 2
}

// PaymentMinorUnits converts an amount to the currency's minor unit; ok is
// false unless the amount is positive and exact in that unit.
func PaymentMinorUnits(amount decimal.Decimal, currency string) (int64, bool) {
	exp := PaymentExponent(currency)
	if amount.Sign() <= 0 || !amount.Equal(amount.Round(exp)) {
		return 0, false
	}
	return amount.Shift(exp).IntPart(), true
}
