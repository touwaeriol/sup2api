package epay

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"sort"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
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

// parseMoney converts Epay's money (yuan, a plain decimal: no sign,
// exponent, NaN or Inf text) to cents, with the table the core converts
// with. The value is never put into the error: it can be long and comes
// from the request.
func parseMoney(s string) (int64, error) {
	cents, ok := protocol.PaymentMinorUnits(s, currencyCNY)
	if !ok {
		return 0, errors.New("money is not a positive plain decimal in whole fen")
	}
	return cents, nil
}

// recordedFields are the parameters kept in payment_events: what an
// operator needs to reconcile, never the signature or the buyer, nor what
// the core records under its own keys (out_trade_no, trade_no, status).
var recordedFields = []string{
	"pid", "api_trade_no", "type", "name", "money", "trade_status", "addtime", "endtime",
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
