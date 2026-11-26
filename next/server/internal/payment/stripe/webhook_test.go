package stripe

import (
	"cmp"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v87/webhook"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

const (
	testSecretKey     = "sk_test_51Example"
	testWebhookSecret = "whsec_test_secret"
	testOutTradeNo    = "s2a_00112233445566778899aabbccddeeff"
)

func testConfig() Config {
	return Config{SecretKey: testSecretKey, WebhookSecret: testWebhookSecret}
}

func newTestGateway(cfg Config, client *http.Client) *Gateway {
	if client == nil {
		client = http.DefaultClient
	}
	return New(func(context.Context) (Config, error) { return cfg, nil }, client)
}

// testSession is a checkout.session object as Stripe sends it.
func testSession() map[string]any {
	return map[string]any{
		"id":                  "cs_test_a1",
		"object":              "checkout.session",
		"status":              "complete",
		"payment_status":      "paid",
		"amount_total":        1234,
		"currency":            "usd",
		"client_reference_id": testOutTradeNo,
		"metadata":            map[string]string{"out_trade_no": testOutTradeNo},
		"payment_intent":      "pi_test_1",
		"livemode":            false,
		"customer_details":    map[string]any{"email": "payer@example.com"},
	}
}

func testEvent(typ string, livemode bool, object map[string]any) []byte {
	b, err := json.Marshal(map[string]any{
		"id":          "evt_test_1",
		"object":      "event",
		"type":        typ,
		"livemode":    livemode,
		"api_version": "2020-08-27",
		"data":        map[string]any{"object": object},
	})
	if err != nil {
		panic(err)
	}
	return b
}

// signed builds the notification Stripe would POST for payload.
func signed(payload []byte, secret string, at time.Time) core.PaymentNotification {
	sp := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: payload, Secret: secret, Timestamp: at})
	h := http.Header{}
	h.Set("Stripe-Signature", sp.Header)
	return core.PaymentNotification{Method: http.MethodPost, Header: h, Body: payload}
}

func parse(t *testing.T, cfg Config, n core.PaymentNotification) (*core.PaymentResult, error) {
	t.Helper()
	return newTestGateway(cfg, nil).ParseNotification(context.Background(), n)
}

// rejectCase is one notification that must fail verification: body signed
// with secret (default testWebhookSecret) age ago, then edited.
type rejectCase struct {
	cfg    Config
	body   []byte
	secret string
	age    time.Duration
	edit   func(*core.PaymentNotification)
}

func completed(object map[string]any) []byte {
	return testEvent("checkout.session.completed", false, object)
}

func rejectCases() map[string]rejectCase {
	good, cfg, live := completed(testSession()), testConfig(), Config{SecretKey: "rk_live_x", WebhookSecret: testWebhookSecret}
	return map[string]rejectCase{
		"wrong signature":       {cfg: cfg, body: good, secret: "whsec_other"},
		"timestamp too old":     {cfg: cfg, body: good, age: 301 * time.Second},
		"missing header":        {cfg: cfg, body: good, edit: func(n *core.PaymentNotification) { n.Header = nil }},
		"webhook secret unset":  {cfg: Config{SecretKey: testSecretKey}, body: good},
		"secret key unset":      {cfg: Config{WebhookSecret: testWebhookSecret}, body: good},
		"GET":                   {cfg: cfg, body: good, edit: func(n *core.PaymentNotification) { n.Method = http.MethodGet }},
		"tampered body":         {cfg: cfg, body: good, edit: func(n *core.PaymentNotification) { n.Body = append(n.Body, ' ') }},
		"live event, test key":  {cfg: cfg, body: testEvent("checkout.session.completed", true, testSession())},
		"test event, live key":  {cfg: live, body: good},
		"reference mismatch":    {cfg: cfg, body: completed(withField("client_reference_id", "s2a_other"))},
		"not a checkout object": {cfg: cfg, body: completed(withField("object", "payment_intent"))},
		"not an event":          {cfg: cfg, body: []byte(`{"id":"evt_1","object":"checkout.session"}`)},
		"webhook secret malformed": {cfg: Config{SecretKey: testSecretKey, WebhookSecret: "secret"}, body: good,
			secret: "secret"},
	}
}

func TestParseNotificationRejects(t *testing.T) {
	for name, tc := range rejectCases() {
		t.Run(name, func(t *testing.T) {
			n := signed(tc.body, cmp.Or(tc.secret, testWebhookSecret), time.Now().Add(-tc.age))
			if tc.edit != nil {
				tc.edit(&n)
			}
			r, err := parse(t, tc.cfg, n)
			require.ErrorIs(t, err, core.ErrPaymentSignature)
			require.Nil(t, r)
		})
	}
}

func withField(key string, value any) map[string]any {
	s := testSession()
	s[key] = value
	return s
}

func TestParseNotificationConfigError(t *testing.T) {
	boom := errors.New("settings unavailable")
	g := New(func(context.Context) (Config, error) { return Config{}, boom }, http.DefaultClient)
	_, err := g.ParseNotification(context.Background(), signed(testEvent("checkout.session.completed", false, testSession()), testWebhookSecret, time.Now()))
	require.ErrorIs(t, err, boom)
	require.NotErrorIs(t, err, core.ErrPaymentSignature, "an unreadable config must be retried, not rejected")
}

func TestParseNotificationStatusMapping(t *testing.T) {
	cases := []struct {
		event  string
		object map[string]any
		want   core.PaymentStatus
	}{
		{"checkout.session.completed", testSession(), core.PaymentPaid},
		{"checkout.session.completed", withField("payment_status", "unpaid"), core.PaymentPending},
		{"checkout.session.async_payment_succeeded", testSession(), core.PaymentPaid},
		{"checkout.session.expired", withField("status", "expired"), core.PaymentExpired},
		{"checkout.session.async_payment_failed", withField("payment_status", "unpaid"), core.PaymentPending},
	}
	for _, tc := range cases {
		t.Run(tc.event+"/"+tc.object["payment_status"].(string), func(t *testing.T) {
			r, err := parse(t, testConfig(), signed(testEvent(tc.event, false, tc.object), testWebhookSecret, time.Now()))
			require.NoError(t, err)
			require.Equal(t, tc.want, r.Status)
			require.Equal(t, testOutTradeNo, r.OutTradeNo)
			require.Equal(t, tc.event, r.Fields["event_type"])
		})
	}
}

func TestParseNotificationFields(t *testing.T) {
	jpy := withField("currency", "jpy")
	jpy["amount_total"] = 1500
	r, err := parse(t, testConfig(), signed(testEvent("checkout.session.completed", false, jpy), testWebhookSecret, time.Now()))
	require.NoError(t, err)
	require.Equal(t, &core.PaymentResult{
		OutTradeNo:  testOutTradeNo,
		TradeNo:     "pi_test_1",
		SessionID:   "cs_test_a1",
		Amount:      1500,
		Currency:    "JPY",
		Status:      core.PaymentPaid,
		EventID:     "evt_test_1",
		MerchantRef: merchantTest,
		Fields: map[string]string{
			"event_type": "checkout.session.completed", "session_status": "complete", "payment_status": "paid",
			"livemode": "false", "amount_total": "1500", "currency": "jpy",
		},
	}, r)
}

func TestParseNotificationLive(t *testing.T) {
	live := withField("livemode", true)
	delete(live, "payment_intent")
	cfg := Config{SecretKey: "sk_live_x", WebhookSecret: testWebhookSecret}
	r, err := parse(t, cfg, signed(testEvent("checkout.session.completed", true, live), testWebhookSecret, time.Now()))
	require.NoError(t, err)
	require.Equal(t, merchantLive, r.MerchantRef)
	require.Equal(t, "cs_test_a1", r.TradeNo, "no payment intent falls back to the session id")
}

// An event of another kind is authentic but about no order: the core only
// records it.
func TestParseNotificationOtherEvent(t *testing.T) {
	pi := map[string]any{"id": "pi_1", "object": "payment_intent", "amount": 999}
	r, err := parse(t, testConfig(), signed(testEvent("payment_intent.succeeded", false, pi), testWebhookSecret, time.Now()))
	require.NoError(t, err)
	require.Equal(t, &core.PaymentResult{
		Status: core.PaymentPending, EventID: "evt_test_1", MerchantRef: merchantTest,
		Fields: map[string]string{"event_type": "payment_intent.succeeded"},
	}, r)
}

func TestRespond(t *testing.T) {
	g := newTestGateway(testConfig(), nil)
	require.Equal(t, http.StatusOK, g.Respond(core.PaymentHandled).Status)
	require.Equal(t, http.StatusBadRequest, g.Respond(core.PaymentRejected).Status)
	require.Equal(t, http.StatusInternalServerError, g.Respond(core.PaymentRetry).Status)
}

// The method and the signature header are checked before the settings are
// read: junk requests to the public route cost no settings read.
func TestParseNotificationChecksRequestFirst(t *testing.T) {
	g := New(func(context.Context) (Config, error) {
		t.Fatal("config read before the request was checked")
		return Config{}, nil
	}, http.DefaultClient)
	good := signed(completed(testSession()), testWebhookSecret, time.Now())
	noHeader := good
	noHeader.Header = http.Header{}
	get := good
	get.Method = http.MethodGet
	for name, n := range map[string]core.PaymentNotification{"no header": noHeader, "GET": get} {
		_, err := g.ParseNotification(context.Background(), n)
		require.ErrorIs(t, err, core.ErrPaymentSignature, name)
	}
}

// While a webhook secret is being rolled Stripe signs with the old and the
// new one; any matching v1 signature is enough.
func TestParseNotificationSeveralSignatures(t *testing.T) {
	body, now := completed(testSession()), time.Now()
	sig := func(secret string) string { return hex.EncodeToString(webhook.ComputeSignature(now, body, secret)) }
	n := signed(body, testWebhookSecret, now)
	for _, order := range [][2]string{{"whsec_old", testWebhookSecret}, {testWebhookSecret, "whsec_old"}} {
		n.Header.Set(signatureHeader, fmt.Sprintf("t=%d,v1=%s,v1=%s", now.Unix(), sig(order[0]), sig(order[1])))
		r, err := parse(t, testConfig(), n)
		require.NoError(t, err)
		require.Equal(t, testOutTradeNo, r.OutTradeNo)
	}
	n.Header.Set(signatureHeader, fmt.Sprintf("t=%d,v1=%s,v1=%s", now.Unix(), sig("whsec_a"), sig("whsec_b")))
	_, err := parse(t, testConfig(), n)
	require.ErrorIs(t, err, core.ErrPaymentSignature)
}

// A session made by something else on the account (a Payment Link sets
// client_reference_id, not our metadata) is authentic but about no order:
// the core acknowledges it instead of making Stripe retry for days.
func TestParseNotificationForeignSession(t *testing.T) {
	for name, s := range map[string]map[string]any{
		"metadata missing": withField("metadata", map[string]string{}),
		"metadata absent":  withField("metadata", nil),
	} {
		r, err := parse(t, testConfig(), signed(completed(s), testWebhookSecret, time.Now()))
		require.NoError(t, err, name)
		require.Empty(t, r.OutTradeNo, name)
		require.Equal(t, "evt_test_1", r.EventID, name)
	}
}

// Fields the gateway does not read may have any shape: an event in another
// API version still decodes. payment_intent may also arrive expanded.
func TestParseNotificationOtherAPIVersion(t *testing.T) {
	s := testSession()
	s["payment_intent"] = map[string]any{"id": "pi_test_1", "object": "payment_intent"}
	s["customer_details"] = "a shape the library's struct does not expect"
	s["total_details"] = []int{1, 2}
	r, err := parse(t, testConfig(), signed(completed(s), testWebhookSecret, time.Now()))
	require.NoError(t, err)
	require.Equal(t, "pi_test_1", r.TradeNo)
	require.Equal(t, core.PaymentPaid, r.Status)
}

func TestConfigValidate(t *testing.T) {
	require.NoError(t, testConfig().Validate())
	require.NoError(t, Config{SecretKey: "rk_live_x", WebhookSecret: "whsec_x"}.Validate())
	err := Config{SecretKey: "pk_live_x", WebhookSecret: "whsec_"}.Validate()
	require.ErrorIs(t, err, ErrSecretKey)
	require.ErrorIs(t, err, ErrWebhookSecret)
	require.ErrorIs(t, Config{SecretKey: "sk_test_x ", WebhookSecret: "whsec_x"}.Validate(), ErrSecretKey)
	require.NotErrorIs(t, Config{SecretKey: "sk_test_x"}.Validate(), ErrSecretKey)
	mode, err := Config{SecretKey: "sk_live_x", WebhookSecret: "whsec_x"}.mode()
	require.NoError(t, err)
	require.Equal(t, merchantLive, mode)
}
