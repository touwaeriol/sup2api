package epay

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"regexp"
	"sort"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// sign is the Epay MD5 signature: the non-empty parameters except sign and
// sign_type, sorted by key, joined as k=v&k=v, with the key appended
// directly, MD5 in lower-case hex. Values are used as they are (decoded),
// which is why notifications whose values contain '&' or '=' are refused
// before this runs: otherwise two different parameter sets could produce
// the same string (the go-epay v0.0.4 forgery).
func sign(params map[string]string, key string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if v != "" && k != "sign" && k != "sign_type" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(k + "=" + params[k])
	}
	sum := md5.Sum([]byte(b.String() + key))
	return hex.EncodeToString(sum[:])
}

// moneyPattern is a plain decimal: no sign, exponent, NaN or Inf text, and
// short enough that converting to cents cannot overflow.
var moneyPattern = regexp.MustCompile(`^[0-9]{1,12}(\.[0-9]{1,8})?$`)

// parseMoney converts Epay's money (yuan) to cents. The value is never put
// into the error: it can be long and comes from the request.
func parseMoney(s string) (int64, error) {
	if !moneyPattern.MatchString(s) {
		return 0, errors.New("money is not a plain decimal")
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return 0, errors.New("money is not a plain decimal")
	}
	cents, ok := core.PaymentMinorUnits(d, currencyCNY)
	if !ok {
		return 0, errors.New("money is not a positive amount in fen")
	}
	return cents, nil
}

// recordedFields are the parameters kept in payment_events: what an
// operator needs to reconcile, never the signature or the buyer.
var recordedFields = []string{
	"pid", "trade_no", "out_trade_no", "api_trade_no", "type", "name",
	"money", "trade_status", "status", "addtime", "endtime",
}

// fields is the recorded subset of params with a value.
func fields(params map[string]string) map[string]string {
	out := make(map[string]string, len(recordedFields))
	for _, k := range recordedFields {
		if v := params[k]; v != "" {
			out[k] = v
		}
	}
	return out
}
