package stripe

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// fakeStripe records the last request and answers with session.
type fakeStripe struct {
	srv     *httptest.Server
	method  string
	path    string
	auth    string
	form    url.Values
	session map[string]any
	status  int
}

func newFakeStripe(t *testing.T) *fakeStripe {
	f := &fakeStripe{session: testSession(), status: http.StatusOK}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.method, f.path, f.auth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		require.NoError(t, r.ParseForm())
		f.form = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.status)
		require.NoError(t, json.NewEncoder(w).Encode(f.reply()))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeStripe) reply() any {
	if f.status != http.StatusOK {
		return map[string]any{"error": map[string]any{"type": "invalid_request_error", "message": "nope"}}
	}
	return f.session
}

// countingTransport proves requests go through the client given to New.
type countingTransport struct{ n atomic.Int32 }

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n.Add(1)
	return (&http.Transport{}).RoundTrip(r)
}

func (f *fakeStripe) gateway(cfg Config) (*Gateway, *countingTransport) {
	tr := &countingTransport{}
	g := newTestGateway(cfg, &http.Client{Transport: tr})
	g.apiURL = f.srv.URL
	return g, tr
}

var testNow = time.Date(2026, 11, 26, 8, 0, 0, 0, time.UTC)

func testCreate(amount, currency string) core.PaymentCreate {
	return core.PaymentCreate{
		OutTradeNo: testOutTradeNo,
		Amount:     decimal.RequireFromString(amount),
		Currency:   currency,
		Subject:    "Top-up",
		NotifyURL:  "https://site.example.com/api/v1/payment/notify/stripe",
		ReturnURL:  "https://site.example.com/wallet",
		ExpiresAt:  testNow.Add(2 * time.Hour),
	}
}

func TestCreatePayment(t *testing.T) {
	f := newFakeStripe(t)
	f.session["url"] = "https://checkout.stripe.com/c/pay/cs_test_a1"
	g, tr := f.gateway(testConfig())
	g.now = func() time.Time { return testNow }

	got, err := g.CreatePayment(context.Background(), testCreate("12.34", "USD"))
	require.NoError(t, err)
	require.Equal(t, &core.PaymentCreated{PayURL: "https://checkout.stripe.com/c/pay/cs_test_a1", SessionID: "cs_test_a1", MerchantRef: merchantTest}, got)
	require.EqualValues(t, 1, tr.n.Load())
	require.Equal(t, http.MethodPost, f.method)
	require.Equal(t, "/v1/checkout/sessions", f.path)
	require.Equal(t, "Bearer "+testSecretKey, f.auth)
	require.Equal(t, url.Values{
		"mode":                                          {"payment"},
		"line_items[0][price_data][currency]":           {"usd"},
		"line_items[0][price_data][unit_amount]":        {"1234"},
		"line_items[0][price_data][product_data][name]": {"Top-up"},
		"line_items[0][quantity]":                       {"1"},
		"client_reference_id":                           {testOutTradeNo},
		"metadata[out_trade_no]":                        {testOutTradeNo},
		"success_url":                                   {"https://site.example.com/wallet"},
		"cancel_url":                                    {"https://site.example.com/wallet"},
		"expires_at":                                    {strconv.FormatInt(testNow.Add(2*time.Hour).Unix(), 10)},
	}, f.form, "exactly these parameters: no promotion codes, no dashboard price")
}

func TestCreatePaymentZeroDecimalCurrency(t *testing.T) {
	f := newFakeStripe(t)
	g, _ := f.gateway(Config{SecretKey: "rk_live_x"})
	got, err := g.CreatePayment(context.Background(), testCreate("1500", "JPY"))
	require.NoError(t, err)
	require.Equal(t, merchantLive, got.MerchantRef)
	require.Equal(t, "jpy", f.form.Get("line_items[0][price_data][currency]"))
	require.Equal(t, "1500", f.form.Get("line_items[0][price_data][unit_amount]"))
}

func TestCreatePaymentRefusesBeforeCallingStripe(t *testing.T) {
	f := newFakeStripe(t)
	for name, tc := range map[string]struct {
		cfg Config
		in  core.PaymentCreate
	}{
		"inexact amount":  {testConfig(), testCreate("12.345", "USD")},
		"fractional JPY":  {testConfig(), testCreate("10.5", "JPY")},
		"zero amount":     {testConfig(), testCreate("0", "USD")},
		"no secret key":   {Config{}, testCreate("1", "USD")},
		"publishable key": {Config{SecretKey: "pk_test_x"}, testCreate("1", "USD")},
	} {
		t.Run(name, func(t *testing.T) {
			g, tr := f.gateway(tc.cfg)
			_, err := g.CreatePayment(context.Background(), tc.in)
			require.Error(t, err)
			require.Zero(t, tr.n.Load())
		})
	}
}

func TestCreatePaymentStripeError(t *testing.T) {
	f := newFakeStripe(t)
	f.status = http.StatusBadRequest
	g, tr := f.gateway(testConfig())
	_, err := g.CreatePayment(context.Background(), testCreate("1", "USD"))
	require.ErrorContains(t, err, "create checkout session")
	require.EqualValues(t, 1, tr.n.Load(), "no hidden retries")
}

func TestCreatePaymentHonorsContext(t *testing.T) {
	f := newFakeStripe(t)
	g, _ := f.gateway(testConfig())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := g.CreatePayment(ctx, testCreate("1", "USD"))
	require.ErrorIs(t, err, context.Canceled)
}

func TestExpiresAtClamp(t *testing.T) {
	g := newTestGateway(testConfig(), nil)
	g.now = func() time.Time { return testNow }
	require.Equal(t, testNow.Add(31*time.Minute), g.expiresAt(testNow.Add(10*time.Minute)), "below Stripe's 30 minutes")
	require.Equal(t, testNow.Add(31*time.Minute), g.expiresAt(time.Time{}))
	require.Equal(t, testNow.Add(90*time.Minute), g.expiresAt(testNow.Add(90*time.Minute)))
	require.Equal(t, testNow.Add(24*time.Hour-time.Minute), g.expiresAt(testNow.Add(48*time.Hour)), "above Stripe's 24 hours")
}

func TestQueryPayment(t *testing.T) {
	cases := []struct {
		name   string
		fields map[string]any
		want   core.PaymentStatus
	}{
		{"complete and paid", nil, core.PaymentPaid},
		{"complete, payment pending", map[string]any{"payment_status": "unpaid"}, core.PaymentPending},
		{"open", map[string]any{"status": "open", "payment_status": "unpaid", "payment_intent": nil}, core.PaymentPending},
		{"expired", map[string]any{"status": "expired", "payment_status": "unpaid"}, core.PaymentExpired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeStripe(t)
			for k, v := range tc.fields {
				f.session[k] = v
			}
			g, tr := f.gateway(testConfig())
			r, err := g.QueryPayment(context.Background(), core.PaymentQuery{OutTradeNo: testOutTradeNo, SessionID: "cs_test_a1"})
			require.NoError(t, err)
			require.EqualValues(t, 1, tr.n.Load())
			require.Equal(t, tc.want, r.Status)
			requireQueried(t, f, r)
		})
	}
}

func requireQueried(t *testing.T, f *fakeStripe, r *core.PaymentResult) {
	t.Helper()
	require.Equal(t, http.MethodGet, f.method)
	require.Equal(t, "/v1/checkout/sessions/cs_test_a1", f.path)
	require.Equal(t, "Bearer "+testSecretKey, f.auth)
	require.Equal(t, testOutTradeNo, r.OutTradeNo)
	require.Equal(t, "cs_test_a1", r.SessionID)
	require.EqualValues(t, 1234, r.Amount)
	require.Equal(t, "USD", r.Currency)
	require.Equal(t, merchantTest, r.MerchantRef)
	require.Empty(t, r.EventID)
}

func TestQueryPaymentRejects(t *testing.T) {
	f := newFakeStripe(t)
	f.session["metadata"] = map[string]string{"out_trade_no": "s2a_other"}
	g, _ := f.gateway(testConfig())
	_, err := g.QueryPayment(context.Background(), core.PaymentQuery{SessionID: "cs_test_a1"})
	require.ErrorIs(t, err, core.ErrPaymentSignature)

	g, tr := f.gateway(testConfig())
	_, err = g.QueryPayment(context.Background(), core.PaymentQuery{OutTradeNo: testOutTradeNo})
	require.Error(t, err, "an order without a session cannot be queried")
	require.Zero(t, tr.n.Load())
}
