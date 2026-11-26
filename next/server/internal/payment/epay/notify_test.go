package epay

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// baseNotify is a successful notification; its golden signature was
// computed outside Go:
// printf '%s' 'money=12.34&name=Top-up&out_trade_no=s2a_order1&pid=1001&trade_no=T20261126&trade_status=TRADE_SUCCESS&type=wxpay' 's3cr3tKey' | md5sum
func baseNotify() map[string]string {
	return map[string]string{
		"pid": "1001", "trade_no": "T20261126", "out_trade_no": "s2a_order1",
		"type": "wxpay", "name": "Top-up", "money": "12.34",
		"trade_status": "TRADE_SUCCESS", "sign_type": "MD5",
	}
}

const baseNotifySign = "531f63264f4b3c09315d461cad93b756"

// encode signs params with key (unless they already carry a sign) and
// encodes them in a stable order.
func encode(params map[string]string, key string) string {
	if _, ok := params["sign"]; !ok {
		params["sign"] = sign(params, key)
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, len(keys))
	for i, k := range keys {
		pairs[i] = url.QueryEscape(k) + "=" + url.QueryEscape(params[k])
	}
	return strings.Join(pairs, "&")
}

func signed(mutate func(map[string]string)) string {
	p := baseNotify()
	mutate(p)
	return encode(p, testKey)
}

func asGET(raw string) core.PaymentNotification {
	return core.PaymentNotification{Method: http.MethodGet, RawQuery: raw}
}

func asPOST(raw string) core.PaymentNotification {
	h := http.Header{"Content-Type": {"application/x-www-form-urlencoded; charset=utf-8"}}
	return core.PaymentNotification{Method: http.MethodPost, Header: h, Body: []byte(raw)}
}

var transports = map[string]func(string) core.PaymentNotification{"GET": asGET, "POST": asPOST}

func parse(n core.PaymentNotification) (*core.PaymentResult, error) {
	return newTestGateway(testConfig(), nil).ParseNotification(context.Background(), n)
}

func TestNotificationGoldenSignature(t *testing.T) {
	if got := sign(baseNotify(), testKey); got != baseNotifySign {
		t.Fatalf("sign = %s, want %s", got, baseNotifySign)
	}
}

func TestNotificationAccepted(t *testing.T) {
	for name, as := range transports {
		t.Run(name, func(t *testing.T) {
			p := baseNotify()
			p["sign"] = strings.ToUpper(baseNotifySign) // hex case does not matter
			p["param"] = ""                             // empty values are skipped
			got, err := parse(as(encode(p, testKey)))
			if err != nil {
				t.Fatal(err)
			}
			want := core.PaymentResult{
				OutTradeNo: "s2a_order1", TradeNo: "T20261126", Amount: 1234, Currency: "CNY",
				Status: core.PaymentPaid, MerchantRef: "1001", MethodType: "wxpay",
			}
			if got.Fields["sign"] != "" || got.Fields["sign_type"] != "" || got.Fields["money"] != "12.34" {
				t.Fatalf("fields = %v", got.Fields)
			}
			got.Fields = nil
			if !reflect.DeepEqual(*got, want) {
				t.Fatalf("result\n got %+v\nwant %+v", *got, want)
			}
		})
	}
}

type passThroughCase struct {
	mutate func(map[string]string)
	check  func(*core.PaymentResult) bool
}

func passThroughCases() map[string]passThroughCase {
	return map[string]passThroughCase{
		"type is passed through": {
			func(p map[string]string) { p["type"] = "qqpay" },
			func(r *core.PaymentResult) bool { return r.MethodType == "qqpay" && r.Status == core.PaymentPaid },
		},
		"non-success status is pending": {
			func(p map[string]string) { p["trade_status"] = "WAIT_BUYER_PAY" },
			func(r *core.PaymentResult) bool { return r.Status == core.PaymentPending },
		},
		"trailing zeros are exact": {
			func(p map[string]string) { p["money"] = "12.340" },
			func(r *core.PaymentResult) bool { return r.Amount == 1234 },
		},
		"signed extension fields are accepted, buyer not recorded": {
			func(p map[string]string) { p["buyer"] = "someone@example.com"; p["api_trade_no"] = "A1" },
			func(r *core.PaymentResult) bool { return r.Fields["buyer"] == "" && r.Fields["api_trade_no"] == "A1" },
		},
		"signed unknown parameter is verified, not recorded": {
			func(p map[string]string) { p["timestamp"] = "1764140000" },
			func(r *core.PaymentResult) bool { return r.Status == core.PaymentPaid && r.Fields["timestamp"] == "" },
		},
	}
}

func TestNotificationPassThrough(t *testing.T) {
	for name, c := range passThroughCases() {
		for tn, as := range transports {
			t.Run(name+"/"+tn, func(t *testing.T) {
				got, err := parse(as(signed(c.mutate)))
				if err != nil || !c.check(got) {
					t.Fatalf("result = %+v, err = %v", got, err)
				}
			})
		}
	}
}

func del(k string) string { return signed(func(p map[string]string) { delete(p, k) }) }

func set(k, v string) string { return signed(func(p map[string]string) { p[k] = v }) }

// rejectedRaw are raw parameter strings that must fail verification:
// signature and ambiguity problems, then wrong or missing fields.
func rejectedRaw() map[string]string {
	other := encode(baseNotify(), "another-key")
	cases := map[string]string{
		"empty":                        "",
		"sign absent":                  "pid=1001&out_trade_no=s2a_order1&money=12.34&sign_type=MD5",
		"missing sign":                 encode(map[string]string{"sign": "", "pid": "1001", "money": "1.00"}, testKey),
		"wrong sign":                   strings.Replace(signed(func(map[string]string) {}), baseNotifySign, strings.Repeat("0", 32), 1),
		"signed with another key":      other,
		"missing sign_type":            del("sign_type"),
		"sign_type not MD5":            set("sign_type", "RSA"),
		"value with encoded &":         set("name", "x&money=0.01"),
		"value with encoded =":         set("name", "a=b"),
		"value with raw =":             signed(func(map[string]string) {}) + "&name=a=b",
		"repeated parameter":           signed(func(map[string]string) {}) + "&money=99.00",
		"repeated identical parameter": signed(func(map[string]string) {}) + "&money=12.34",
		"parameter without =":          signed(func(map[string]string) {}) + "&money",
		"empty segment":                signed(func(map[string]string) {}) + "&",
		"unsigned extra parameter":     signed(func(map[string]string) {}) + "&timestamp=1764140000",
		"extra parameter, wrong sign":  strings.Replace(set("timestamp", "1764140000"), "timestamp=1764140000", "timestamp=1764140001", 1),
		"name with upper case":         set("Timestamp", "1"),
		"name with a digit":            set("ext1", "1"),
		"name too long":                set(strings.Repeat("x", 33), "1"),
		"bad escape":                   signed(func(map[string]string) {}) + "&param=%zz",
	}
	for k, v := range rejectedFields() {
		cases[k] = v
	}
	return cases
}

func rejectedFields() map[string]string {
	return map[string]string{
		"pid mismatch":         set("pid", "2002"),
		"pid missing":          del("pid"),
		"money sub-fen":        set("money", "1.001"),
		"money negative":       set("money", "-1.00"),
		"money zero":           set("money", "0.00"),
		"money NaN":            set("money", "NaN"),
		"money exponent":       set("money", "1e2"),
		"money missing":        del("money"),
		"out_trade_no missing": del("out_trade_no"),
		"trade_no missing":     del("trade_no"),
		"type missing":         del("type"),
		"trade_status missing": del("trade_status"),
	}
}

func TestNotificationRejected(t *testing.T) {
	for name, raw := range rejectedRaw() {
		for tn, as := range transports {
			t.Run(name+"/"+tn, func(t *testing.T) {
				got, err := parse(as(raw))
				if !errors.Is(err, core.ErrPaymentSignature) {
					t.Fatalf("result = %+v, err = %v; want ErrPaymentSignature", got, err)
				}
				if strings.Contains(err.Error(), testKey) {
					t.Fatalf("error leaks the key: %v", err)
				}
			})
		}
	}
}

func TestNotificationTransportRejected(t *testing.T) {
	raw := signed(func(map[string]string) {})
	jsonPost := asPOST(raw)
	jsonPost.Header.Set("Content-Type", "application/json")
	both := asPOST(raw)
	both.RawQuery = raw
	put := asPOST(raw)
	put.Method = http.MethodPut
	for name, n := range map[string]core.PaymentNotification{
		"POST json": jsonPost, "POST with query": both, "PUT": put, "GET body only": {Method: http.MethodGet, Body: []byte(raw)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parse(n); !errors.Is(err, core.ErrPaymentSignature) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestNotificationConfigErrorIsNotRejection(t *testing.T) {
	boom := errors.New("settings unavailable")
	g := New(func(context.Context) (Config, error) { return Config{}, boom }, http.DefaultClient)
	_, err := g.ParseNotification(context.Background(), asGET(signed(func(map[string]string) {})))
	if !errors.Is(err, boom) || errors.Is(err, core.ErrPaymentSignature) {
		t.Fatalf("err = %v", err)
	}
}
