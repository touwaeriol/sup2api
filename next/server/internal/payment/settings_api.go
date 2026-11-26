package payment

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// merchantField is a setting that names the merchant a gateway's orders
// were created under or verifies their notifications: it cannot change
// while that gateway has orders that may still be paid.
type merchantField struct {
	gateway, path string
	of            func(*Settings) string
}

var merchantFields = []merchantField{
	{core.PaymentGatewayEpay, "epay.merchant_id", func(v *Settings) string { return v.Epay.MerchantID }},
	{core.PaymentGatewayEpay, "epay.key", func(v *Settings) string { return v.Epay.Key }},
	{core.PaymentGatewayStripe, "stripe.secret_key", func(v *Settings) string { return v.Stripe.SecretKey }},
	{core.PaymentGatewayStripe, "stripe.webhook_secret", func(v *Settings) string { return v.Stripe.WebhookSecret }},
}

func (s *Service) getSettings(c *gin.Context) {
	s.renderSettings(c)
}

func (s *Service) putSettings(c *gin.Context) {
	body := struct {
		Values settingsValues `json:"values"`
	}{Values: settingsValues{Settings: defaultSettings()}}
	if !httpapi.BindJSON(c, &body) {
		return
	}
	if err := s.saveSettings(audit.Context(c), body.Values); err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.renderSettings(c)
}

func (s *Service) renderSettings(c *gin.Context) {
	ctx := c.Request.Context()
	v, err := s.loadSettings(ctx, s.db.Gorm)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	ms, err := loadMethods(ctx, s.db.Gorm)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.NoStore(c)
	httpapi.OK(c, settingsView{Mode: "native", Values: settingsValues{Settings: masked(v), Methods: ms},
		SecretFields: secretPaths(), Permission: string(perm.TopupManage)})
}

// saveSettings validates and stores the settings and methods in one
// transaction under the settings row lock, and audits who changed what.
func (s *Service) saveSettings(ctx context.Context, in settingsValues) error {
	actor, _ := core.UserID(ctx)
	if in.Methods == nil {
		in.Methods = []Method{}
	}
	return s.db.Tx(ctx, func(tx *gorm.DB) error {
		var stored Settings
		_, err := store.UpdateSettingJSONTx(ctx, tx, settingsKey, &actor, func(raw json.RawMessage) (json.RawMessage, error) {
			var err error
			if stored, err = s.decodeSettings(raw); err != nil {
				return nil, err
			}
			keepSecrets(&in.Settings, stored)
			if err := s.checkSettings(ctx, tx, in, stored); err != nil {
				return nil, err
			}
			return s.encodeSettings(in.Settings)
		})
		if err != nil {
			return err
		}
		if err := replaceMethods(ctx, tx, in.Methods); err != nil {
			return err
		}
		return audit.Audit(ctx, tx, actor, "topup.settings.update", "settings", settingsKey, settingsAudit(in, stored))
	})
}

// checkSettings validates in and refuses a merchant change of a gateway
// with orders that may still be paid: their notifications are verified
// with the stored merchant and secrets.
func (s *Service) checkSettings(ctx context.Context, tx *gorm.DB, in settingsValues, stored Settings) error {
	f := s.validateValues(ctx, in)
	open := map[string]bool{}
	for _, m := range merchantFields {
		if m.of(&in.Settings) == m.of(&stored) {
			continue
		}
		busy, ok := open[m.gateway]
		if !ok {
			var err error
			if busy, err = hasPayableOrders(ctx, tx, m.gateway, s.now()); err != nil {
				return err
			}
			open[m.gateway] = busy
		}
		f.check(!busy, m.path, "the gateway has orders that may still be paid: its merchant and secrets cannot change now",
			"该网关还有可能付款的订单，暂时不能修改商户号和密钥")
	}
	return f.err()
}

// hasPayableOrders reports whether gateway has an order a notification may
// still credit: pending, or expired or cancelled within the late window.
func hasPayableOrders(ctx context.Context, tx *gorm.DB, gateway string, now time.Time) (bool, error) {
	db := store.G(ctx, tx)
	return gormpg.Exists(db, payable(db.Model(&model.PaymentOrder{}).Where("gateway = ?", gateway), now))
}

// settingsAudit is the audit detail of a settings change: what changed,
// never a secret.
func settingsAudit(in settingsValues, stored Settings) map[string]any {
	return map[string]any{
		"enabled": in.Enabled, "currency": in.Currency, "price": in.Price.String(),
		"epay_enabled": in.Epay.Enabled, "epay_merchant_id": in.Epay.MerchantID, "stripe_enabled": in.Stripe.Enabled,
		"methods": len(in.Methods), "secrets_changed": changedSecrets(in.Settings, stored),
	}
}
