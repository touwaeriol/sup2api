package payment

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

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
	v, ms, err := s.terms(c.Request.Context(), s.db.Gorm)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	urls := make(map[string]string, len(builtinSpecs))
	for _, b := range builtinSpecs {
		urls[b.id] = s.notifyURL(b.id)
	}
	httpapi.NoStore(c)
	httpapi.OK(c, settingsView{Values: settingsValues{Settings: masked(v), Methods: ms}, SecretFields: secretPaths(),
		NotifyURLs: urls, Currencies: protocol.PaymentCurrencies})
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
	busy := map[string]bool{}
	for b, m := range merchantFields() {
		if *m.at(&in.Settings) == *m.at(&stored) {
			continue
		}
		open, seen := busy[b.id]
		if !seen {
			var err error
			if open, err = hasPayableOrders(ctx, tx, b.id, s.now()); err != nil {
				return err
			}
			busy[b.id] = open
		}
		f.check(!open, b.id+"."+m.name, "the gateway has orders that may still be paid: its merchant and secrets cannot change now",
			"该网关还有可能付款的订单，暂时不能修改商户号和密钥")
	}
	return f.err()
}

// hasPayableOrders reports whether gateway has an order a notification may
// still credit (payable).
func hasPayableOrders(ctx context.Context, tx *gorm.DB, gateway string, now time.Time) (bool, error) {
	db := store.G(ctx, tx)
	return gormpg.Exists(db, db.Model(&model.PaymentOrder{}).Where("gateway = ?", gateway).Scopes(payableOrders(now)))
}

// settingsAudit is the audit detail of a settings change: what changed,
// never a secret.
func settingsAudit(in settingsValues, stored Settings) map[string]any {
	out := map[string]any{"enabled": in.Enabled, "currency": in.Currency, "price": in.Price.String(),
		"methods": len(in.Methods), "secrets_changed": changedSecrets(in.Settings, stored)}
	builtinAudit(out, in.Settings)
	return out
}
