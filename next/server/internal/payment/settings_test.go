package payment

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/payment/stripe"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// The secrets are sealed at rest, masked in the API, kept on the core.SecretMask, and
// the view names the permission that edits it.
func TestSettingsSecrets(t *testing.T) {
	f := newFixture(t)
	f.configure()
	raw, err := store.SettingValue(f.db.Gorm, settingsKey)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "epay-secret")

	w := f.call(f.admin, http.MethodGet, "/topup/settings", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), "epay-secret")
	var got struct{ Data settingsView }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(t, core.SecretMask, got.Data.Values.Epay.Key)
	require.Equal(t, protocol.PaymentCurrencies, got.Data.Currencies)
	require.Contains(t, w.Body.String(), `{"code":"JPY","exponent":0}`)
	require.Equal(t, []string{"epay/key", "stripe/secret_key", "stripe/webhook_secret"}, got.Data.SecretFields)
	require.Len(t, got.Data.Values.Methods, 1)

	body, _ := json.Marshal(map[string]any{"values": got.Data.Values})
	w = f.call(f.admin, http.MethodPut, "/topup/settings", string(body))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	v, err := f.svc.loadSettings(f.adminCtx(), f.db.Gorm)
	require.NoError(t, err)
	require.Equal(t, "epay-secret", v.Epay.Key)
	var audited int64
	require.NoError(t, f.db.Gorm.Table("audit_logs").Where("action = ? AND user_id = ?", "topup.settings.update", f.admin).Count(&audited).Error)
	require.EqualValues(t, 2, audited)
}

// Field errors name the console's paths; the maximum stays within the
// ledger's single credit limit; switching on needs an https site.
func TestSettingsValidation(t *testing.T) {
	f := newFixture(t)
	err := f.saveBad(func(v *settingsValues) {
		v.MaxTopup = dec("200000")
		v.Methods[0].Logo = "javascript:alert(1)"
		v.Discounts = append(v.Discounts, Discount{Amount: dec("100"), Rate: dec("2")})
		v.Stripe = StripeSettings{Enabled: true, SecretKey: "pk_test_x", WebhookSecret: "whsec_x"}
	})
	require.ElementsMatch(t, []string{"values.max_topup", "values.methods.0.logo", "values.discounts.1.amount",
		"values.discounts.1.rate", "values.stripe.secret_key"}, fieldsOf(err))

	f.svc.site.Scheme = "http"
	require.Equal(t, []string{"values.enabled"}, fieldsOf(f.saveBad(nil)))
}

// Each bad Epay field is reported on its own field, by the gateway's check.
func TestSettingsEpayFields(t *testing.T) {
	f := newFixture(t)
	f.configure()
	err := f.saveBad(func(v *settingsValues) { v.Epay.Endpoint, v.Epay.MerchantID = "http://pay.test", "10&01" })
	require.ElementsMatch(t, []string{"values.epay.endpoint", "values.epay.merchant_id"}, fieldsOf(err))
}

// A gateway with an order that may still be paid keeps its merchant and
// secrets; other settings still change.
func TestSettingsMerchantLockedByOpenOrders(t *testing.T) {
	f := newFixture(t)
	f.configure()
	o := f.order(f.user("lock"), "10")
	require.Equal(t, []string{"values.epay.merchant_id", "values.epay.key"},
		fieldsOf(f.saveBad(func(v *settingsValues) { v.Epay.MerchantID, v.Epay.Key = "2002", "other" })))
	f.configure(func(v *settingsValues) { v.Price = dec("7") })

	_, err := f.svc.cancel(f.adminCtx(), o.UserID, o.OutTradeNo)
	require.NoError(t, err)
	require.NotNil(t, fieldsOf(f.saveBad(func(v *settingsValues) { v.Epay.Key = "other" })), "cancelled within the late window")
	f.age(o, lateWindow+time.Minute)
	f.configure(func(v *settingsValues) { v.Epay.Key = "other" })
}

func (f *fixture) saveBad(edit func(*settingsValues)) error {
	v := settingsValues{Settings: defaultSettings(), Methods: []Method{{Type: "alipay", Name: "Alipay", Gateway: "epay", Enabled: true}}}
	v.Enabled = true
	v.Epay = EpaySettings{Enabled: true, Endpoint: "https://epay.test", MerchantID: "1001", Key: "epay-secret"}
	v.Discounts = []Discount{{Amount: dec("100"), Rate: dec("0.9")}}
	if edit != nil {
		edit(&v)
	}
	err := f.svc.saveSettings(f.adminCtx(), v)
	require.Error(f.t, err)
	return err
}

func fieldsOf(err error) []string {
	fields, _ := core.AsError(err).Details["fields"].([]core.FieldError)
	var out []string
	for _, fe := range fields {
		out = append(out, fe.Field)
	}
	return out
}

// GET /me/topup has no gateway, merchant or secret, and only the methods
// that take orders.
func TestCheckoutHidesMerchant(t *testing.T) {
	f := newFixture(t)
	f.configure(func(v *settingsValues) {
		v.Methods = append(v.Methods, Method{Type: "card", Name: "Card", Gateway: stripe.ID, Enabled: true})
	})
	w := f.call(f.user("co"), http.MethodGet, "/me/topup", "")
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	for _, secret := range []string{"epay-secret", "1001", "epay.test", "gateway", "merchant"} {
		require.NotContains(t, body, secret)
	}
	require.True(t, strings.Contains(body, `"alipay"`) && !strings.Contains(body, `"card"`), body)
	require.Contains(t, body, `"enabled":true`)
	require.Contains(t, body, `"price":"7.3"`)
	require.Contains(t, body, `"currency_exponent":2`)
}

// The view gives each built-in gateway's notify address while the site is
// https. A site that stops being https closes top-up: the wallet shows no
// method, quotes and orders are refused.
func TestNotifyURLsAndClosedSite(t *testing.T) {
	f := newFixture(t)
	f.configure()
	var got struct{ Data settingsView }
	require.NoError(t, json.Unmarshal(f.call(f.admin, http.MethodGet, "/topup/settings", "").Body.Bytes(), &got))
	require.Equal(t, map[string]string{"epay": "https://site.test/api/v1/payment/notify/epay",
		"stripe": "https://site.test/api/v1/payment/notify/stripe"}, got.Data.NotifyURLs)

	f.svc.site.Scheme = "http"
	require.Contains(t, f.call(f.admin, http.MethodGet, "/topup/settings", "").Body.String(), `"notify_urls":{"epay":"","stripe":""}`)
	uid := f.user("closed")
	w := f.call(uid, http.MethodGet, "/me/topup", "")
	require.Contains(t, w.Body.String(), `"enabled":false`)
	require.Contains(t, w.Body.String(), `"methods":[]`)
	for _, path := range []string{"/me/topup/preview", "/me/topup/orders"} {
		require.Equal(t, http.StatusConflict, f.call(uid, http.MethodPost, path, `{"amount":"10","method":"alipay"}`).Code, path)
	}
}

// Epay settles CNY only: switching it on needs the CNY currency, and
// settings stored otherwise take no epay orders.
func TestEpayNeedsCNY(t *testing.T) {
	f := newFixture(t)
	require.Equal(t, []string{"values.epay.enabled"}, fieldsOf(f.saveBad(func(v *settingsValues) { v.Currency = "USD" })))
	v := defaultSettings()
	v.Epay.Enabled = true
	require.True(t, f.svc.gatewayOpen(v, "epay"))
	v.Currency = "USD"
	require.False(t, f.svc.gatewayOpen(v, "epay"))
}

// Stripe's keys are checked by the gateway's own Validate, over the stored
// secrets the core.SecretMask keeps; the rate and discounts fit their snapshot
// columns.
func TestSettingsStripeKeysAndPrecision(t *testing.T) {
	f := newFixture(t)
	f.configure(withCard)
	f.configure(withCard, func(v *settingsValues) { v.Stripe.SecretKey, v.Stripe.WebhookSecret = core.SecretMask, "" })
	require.Equal(t, []string{"values.stripe.webhook_secret"}, fieldsOf(f.saveBad(func(v *settingsValues) {
		withCard(v)
		v.Stripe.WebhookSecret = "whsec_"
	})))
	err := f.saveBad(func(v *settingsValues) {
		v.Price = dec("7.123456789")
		v.Discounts[0].Rate = dec("0.12345")
	})
	require.Equal(t, []string{"values.price", "values.discounts.0.rate"}, fieldsOf(err))
	require.Equal(t, []string{"values.price"}, fieldsOf(f.saveBad(func(v *settingsValues) { v.Price = dec("1000001") })))
}
