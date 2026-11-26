package payment

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// The secrets are sealed at rest, masked in the API, kept on the mask, and
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
	require.Equal(t, mask, got.Data.Values.Epay.Key)
	require.Equal(t, "topup:manage", got.Data.Permission)
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
		v.MaxTopup = numInt(200000)
		v.Methods[0].Logo = "javascript:alert(1)"
		v.Discounts = append(v.Discounts, Discount{Amount: numInt(100), Rate: numInt(2)})
		v.Stripe = StripeSettings{Enabled: true, SecretKey: "pk_test_x", WebhookSecret: "whsec_x"}
	})
	require.ElementsMatch(t, []string{"values.max_topup", "values.methods.0.logo", "values.discounts.1.amount",
		"values.discounts.1.rate", "values.stripe.secret_key"}, fieldsOf(err))

	f.svc.site.Scheme = "http"
	require.Equal(t, []string{"values.enabled"}, fieldsOf(f.saveBad(nil)))
}

// A gateway with an order that may still be paid keeps its merchant and
// secrets; other settings still change.
func TestSettingsMerchantLockedByOpenOrders(t *testing.T) {
	f := newFixture(t)
	f.configure()
	o := f.order(f.user("lock"), "10")
	require.Equal(t, []string{"values.epay.merchant_id", "values.epay.key"},
		fieldsOf(f.saveBad(func(v *settingsValues) { v.Epay.MerchantID, v.Epay.Key = "2002", "other" })))
	f.configure(func(v *settingsValues) { v.Price = numInt(7) })

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
	v.Discounts = []Discount{{Amount: numInt(100), Rate: num(decimalOf("0.9"))}}
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
		v.Methods = append(v.Methods, Method{Type: "card", Name: "Card", Gateway: core.PaymentGatewayStripe, Enabled: true})
	})
	w := f.call(f.user("co"), http.MethodGet, "/me/topup", "")
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	for _, secret := range []string{"epay-secret", "1001", "epay.test", "gateway", "merchant"} {
		require.NotContains(t, body, secret)
	}
	require.True(t, strings.Contains(body, `"alipay"`) && !strings.Contains(body, `"card"`), body)
	require.Contains(t, body, `"enabled":true`)
	require.Contains(t, body, `"price":7.3`)
}
