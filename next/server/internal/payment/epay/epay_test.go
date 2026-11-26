package epay

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

const testKey = "s3cr3tKey"

func testConfig() Config {
	return Config{Endpoint: "https://pay.example.com", MerchantID: "1001", Key: testKey}
}

func newTestGateway(cfg Config, client *http.Client) *Gateway {
	if client == nil {
		client = http.DefaultClient
	}
	return New(func(context.Context) (Config, error) { return cfg, nil }, client)
}

func testCreate() core.PaymentCreate {
	return core.PaymentCreate{
		OutTradeNo: "s2a_0123456789abcdef",
		Amount:     decimal.RequireFromString("10"),
		Currency:   "CNY",
		MethodType: "alipay",
		Subject:    "Top-up 10.00",
		NotifyURL:  "https://site.example.com/api/v1/payment/notify/epay",
		ReturnURL:  "https://site.example.com/wallet",
	}
}

// The golden signature was computed outside Go with the go-epay algorithm:
// printf '%s' 'money=10.00&name=Top-up 10.00&notify_url=…&out_trade_no=…&pid=1001&return_url=…&type=alipay' 's3cr3tKey' | md5sum
func TestCreatePaymentGolden(t *testing.T) {
	got, err := newTestGateway(testConfig(), nil).CreatePayment(context.Background(), testCreate())
	if err != nil {
		t.Fatal(err)
	}
	want := "https://pay.example.com/submit.php?money=10.00&name=Top-up+10.00" +
		"&notify_url=https%3A%2F%2Fsite.example.com%2Fapi%2Fv1%2Fpayment%2Fnotify%2Fepay" +
		"&out_trade_no=s2a_0123456789abcdef&pid=1001" +
		"&return_url=https%3A%2F%2Fsite.example.com%2Fwallet" +
		"&sign=8f96c7c11882e4385bc5cef4d350ac17&sign_type=MD5&type=alipay"
	if got.PayURL != want {
		t.Fatalf("PayURL\n got %s\nwant %s", got.PayURL, want)
	}
	if got.MerchantRef != "1001" || got.SessionID != "" || got.QRCode != "" {
		t.Fatalf("created = %+v", got)
	}
}

func TestCreatePaymentEndpointPath(t *testing.T) {
	cfg := testConfig()
	cfg.Endpoint = "https://pay.example.com/epay/"
	got, err := newTestGateway(cfg, nil).CreatePayment(context.Background(), testCreate())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.PayURL, "https://pay.example.com/epay/submit.php?") {
		t.Fatalf("PayURL = %s", got.PayURL)
	}
}

func TestCreatePaymentRefuses(t *testing.T) {
	cases := map[string]func(*Config, *core.PaymentCreate){
		"currency USD":      func(_ *Config, in *core.PaymentCreate) { in.Currency = "USD" },
		"sub-fen amount":    func(_ *Config, in *core.PaymentCreate) { in.Amount = decimal.RequireFromString("10.001") },
		"zero amount":       func(_ *Config, in *core.PaymentCreate) { in.Amount = decimal.Zero },
		"negative amount":   func(_ *Config, in *core.PaymentCreate) { in.Amount = decimal.RequireFromString("-1") },
		"subject with &":    func(_ *Config, in *core.PaymentCreate) { in.Subject = "a&b" },
		"subject with =":    func(_ *Config, in *core.PaymentCreate) { in.Subject = "a=b" },
		"no method":         func(_ *Config, in *core.PaymentCreate) { in.MethodType = "" },
		"no notify url":     func(_ *Config, in *core.PaymentCreate) { in.NotifyURL = "" },
		"http endpoint":     func(c *Config, _ *core.PaymentCreate) { c.Endpoint = "http://pay.example.com" },
		"endpoint query":    func(c *Config, _ *core.PaymentCreate) { c.Endpoint = "https://pay.example.com/?a=1" },
		"endpoint userinfo": func(c *Config, _ *core.PaymentCreate) { c.Endpoint = "https://u:p@pay.example.com" },
		"no key":            func(c *Config, _ *core.PaymentCreate) { c.Key = "" },
		"no merchant":       func(c *Config, _ *core.PaymentCreate) { c.MerchantID = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg, in := testConfig(), testCreate()
			mutate(&cfg, &in)
			if _, err := newTestGateway(cfg, nil).CreatePayment(context.Background(), in); err == nil {
				t.Fatal("CreatePayment succeeded")
			}
		})
	}
}

func TestCreatePaymentConfigError(t *testing.T) {
	boom := errors.New("boom")
	g := New(func(context.Context) (Config, error) { return Config{}, boom }, http.DefaultClient)
	if _, err := g.CreatePayment(context.Background(), testCreate()); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

func TestCreatedURLVerifiesAsNotification(t *testing.T) {
	// The redirect's own parameters pass the notification signature check:
	// both sides use the one sign function.
	got, err := newTestGateway(testConfig(), nil).CreatePayment(context.Background(), testCreate())
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(got.PayURL)
	params := map[string]string{}
	for k := range u.Query() {
		params[k] = u.Query().Get(k)
	}
	if params["sign"] != sign(params, testKey) {
		t.Fatal("redirect signature does not verify")
	}
}

// Every notification gets Epay's replies, also one that failed: only
// "success" stops its retries.
func TestRespond(t *testing.T) {
	_, respond, _ := newTestGateway(testConfig(), nil).ParseNotification(context.Background(), core.PaymentNotification{Method: http.MethodPut})
	cases := []struct {
		o      core.PaymentOutcome
		status int
		body   string
	}{
		{core.PaymentHandled, 200, "success"},
		{core.PaymentRejected, 400, "fail"},
		{core.PaymentRetry, 503, "fail"},
	}
	for _, c := range cases {
		r := respond(c.o)
		if r.Status != c.status || string(r.Body) != c.body || !strings.HasPrefix(r.ContentType, "text/plain") {
			t.Fatalf("respond(%d) = %+v", c.o, r)
		}
	}
}

// Validate reports each bad field on its own error.
func TestConfigValidate(t *testing.T) {
	if err := testConfig().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		cfg  Config
		want []error
	}{
		{Config{}, []error{ErrEndpoint, ErrMerchantID, ErrKey}},
		{Config{Endpoint: "http://pay.example.com", MerchantID: "1001", Key: "k"}, []error{ErrEndpoint}},
		{Config{Endpoint: "https://u:p@pay.example.com", MerchantID: "1001", Key: "k"}, []error{ErrEndpoint}},
		{Config{Endpoint: "https://pay.example.com?a=1", MerchantID: "1001", Key: "k"}, []error{ErrEndpoint}},
		{Config{Endpoint: "https://pay.example.com", MerchantID: "10&01", Key: "k"}, []error{ErrMerchantID}},
		{Config{Endpoint: "https://pay.example.com", MerchantID: " ", Key: ""}, []error{ErrMerchantID, ErrKey}},
	} {
		err := c.cfg.Validate()
		for _, e := range []error{ErrEndpoint, ErrMerchantID, ErrKey} {
			if errors.Is(err, e) != slices.Contains(c.want, e) {
				t.Fatalf("%+v: %v", c.cfg, err)
			}
		}
	}
}
