package protocol

import (
	"regexp"
	"strconv"
	"strings"
)

// PaymentCurrency is a currency a top-up may settle in (CONTRACTS §140) and
// the decimals of its minor unit (ISO 4217 exponent). The core and the
// payment gateway plugins share this table: PaymentResult.amount_minor and
// CreatePaymentRequest.amount_minor are in that unit, so both sides must
// agree on it. The console's top-up settings get the table as JSON.
type PaymentCurrency struct {
	Code     string `json:"code"`
	Exponent int32  `json:"exponent"`
}

// PaymentCurrencies are the settlement currencies, in the order the console
// offers them.
var PaymentCurrencies = []PaymentCurrency{
	{"CNY", 2}, {"USD", 2}, {"HKD", 2}, {"EUR", 2}, {"GBP", 2}, {"JPY", 0},
}

// PaymentExponent is the exponent of a settlement currency (any case); ok
// is false for any other currency.
func PaymentExponent(currency string) (exp int32, ok bool) {
	code := strings.ToUpper(currency)
	for _, c := range PaymentCurrencies {
		if c.Code == code {
			return c.Exponent, true
		}
	}
	return 0, false
}

// plainAmount is an unsigned decimal without exponent, short enough that
// its minor units fit an int64.
var plainAmount = regexp.MustCompile(`^([0-9]{1,15})(?:\.([0-9]{1,18}))?$`)

// PaymentMinorUnits converts a decimal amount ("12.30") to the currency's
// minor unit (1230); ok is false unless the currency is a settlement
// currency and the amount is positive and exact in that unit.
func PaymentMinorUnits(amount, currency string) (int64, bool) {
	exp, ok := PaymentExponent(currency)
	m := plainAmount.FindStringSubmatch(amount)
	if !ok || m == nil {
		return 0, false
	}
	frac := strings.TrimRight(m[2], "0")
	if len(frac) > int(exp) {
		return 0, false
	}
	n, err := strconv.ParseInt(m[1]+frac+strings.Repeat("0", int(exp)-len(frac)), 10, 64)
	return n, err == nil && n > 0
}
