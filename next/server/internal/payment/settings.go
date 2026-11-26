package payment

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// settingsKey is the settings row of the top-up settings. The payment
// methods are rows of payment_methods; both are read and written together
// by GET/PUT /topup/settings.
const settingsKey = "topup"

// Settings are the top-up settings. In the settings row the secret fields
// hold their ciphertext (base64); in memory and in the API the plain text
// or core.SecretMask, which sent back (or nothing) keeps the secret.
type Settings struct {
	Enabled             bool              `json:"enabled"`
	Currency            string            `json:"currency"`
	Price               decimal.Decimal   `json:"price"` // currency per balance unit (USD)
	MinTopup            decimal.Decimal   `json:"min_topup"`
	MaxTopup            decimal.Decimal   `json:"max_topup"`
	OrderTimeoutMinutes int               `json:"order_timeout_minutes"`
	MaxPendingOrders    int               `json:"max_pending_orders"`
	AmountOptions       []decimal.Decimal `json:"amount_options"`
	Discounts           []Discount        `json:"discounts"`
	Epay                EpaySettings      `json:"epay"`
	Stripe              StripeSettings    `json:"stripe"`
}

// Discount: from Amount up, the user pays Rate of the price.
type Discount struct {
	Amount decimal.Decimal `json:"amount"`
	Rate   decimal.Decimal `json:"rate"`
}

// EpaySettings are the built-in epay gateway's.
type EpaySettings struct {
	Enabled    bool   `json:"enabled"`
	Endpoint   string `json:"endpoint"`
	MerchantID string `json:"merchant_id"`
	Key        string `json:"key"`
}

// StripeSettings are the built-in stripe gateway's.
type StripeSettings struct {
	Enabled       bool   `json:"enabled"`
	SecretKey     string `json:"secret_key"`
	WebhookSecret string `json:"webhook_secret"`
}

// secretPaths are the API paths of the secrets (the view's secret_fields).
func secretPaths() []string {
	var out []string
	for path := range secrets() {
		out = append(out, path)
	}
	return out
}

// defaultSettings are the settings of a site that never saved any.
func defaultSettings() Settings {
	return Settings{Currency: "CNY", Price: decimal.RequireFromString("7.3"), MinTopup: decimal.NewFromInt(1),
		MaxTopup: decimal.NewFromInt(10000), OrderTimeoutMinutes: 30, MaxPendingOrders: 3,
		AmountOptions: []decimal.Decimal{}, Discounts: []Discount{}}
}

// secretAAD binds a sealed secret to its field.
func secretAAD(path string) []byte { return []byte("topup:" + path) }

// loadSettings reads the settings with their secrets in plain text;
// clauses go into the SELECT (a row lock).
func (s *Service) loadSettings(ctx context.Context, q *gorm.DB, clauses ...clause.Expression) (Settings, error) {
	raw, err := store.SettingValue(store.G(ctx, q), settingsKey, clauses...)
	if store.IsNoRows(err) {
		return defaultSettings(), nil
	}
	if err != nil {
		return Settings{}, err
	}
	return s.decodeSettings(raw)
}

// terms reads the settings and the payment methods; clauses go into the
// settings' SELECT (a row lock).
func (s *Service) terms(ctx context.Context, q *gorm.DB, clauses ...clause.Expression) (Settings, []Method, error) {
	v, err := s.loadSettings(ctx, q, clauses...)
	if err != nil {
		return Settings{}, nil, err
	}
	ms, err := loadMethods(ctx, q)
	return v, ms, err
}

// decodeSettings decodes a settings row and opens its secrets.
func (s *Service) decodeSettings(raw json.RawMessage) (Settings, error) {
	v := defaultSettings()
	if err := json.Unmarshal(raw, &v); err != nil {
		return Settings{}, fmt.Errorf("decode top-up settings: %w", err)
	}
	for path, at := range secrets() {
		if err := s.openSecret(path, at(&v)); err != nil {
			return Settings{}, err
		}
	}
	return v, nil
}

func (s *Service) openSecret(path string, p *string) error {
	if *p == "" {
		return nil
	}
	sealed, err := base64.StdEncoding.DecodeString(*p)
	if err != nil {
		return fmt.Errorf("open top-up secret %s: %w", path, err)
	}
	plain, err := s.cipher.Decrypt(sealed, secretAAD(path))
	if err != nil {
		return fmt.Errorf("open top-up secret %s: %w", path, err)
	}
	*p = string(plain)
	return nil
}

// encodeSettings seals the secrets of v into a settings row.
func (s *Service) encodeSettings(v Settings) (json.RawMessage, error) {
	for path, at := range secrets() {
		p := at(&v)
		if *p == "" {
			continue
		}
		sealed, err := s.cipher.Encrypt([]byte(*p), secretAAD(path))
		if err != nil {
			return nil, err
		}
		*p = base64.StdEncoding.EncodeToString(sealed)
	}
	return json.Marshal(v)
}

// masked is v as the API shows it: each stored secret is the mask.
func masked(v Settings) Settings {
	for _, at := range secrets() {
		if p := at(&v); *p != "" {
			*p = core.SecretMask
		}
	}
	return v
}

// keepSecrets puts the stored secret back where the input sent the mask or
// nothing.
func keepSecrets(in *Settings, stored Settings) {
	for _, at := range secrets() {
		if p := at(in); *p == "" || *p == core.SecretMask {
			*p = *at(&stored)
		}
	}
}

// changedSecrets lists the secrets in differs from stored on.
func changedSecrets(in, stored Settings) []string {
	var out []string
	for path, at := range secrets() {
		if *at(&in) != *at(&stored) {
			out = append(out, path)
		}
	}
	return out
}

// settingsView is GET/PUT /topup/settings: the values with masked
// secrets, the paths of the secrets, each built-in gateway's notify
// address, and the settlement currencies.
type settingsView struct {
	Values       settingsValues             `json:"values"`
	SecretFields []string                   `json:"secret_fields"`
	NotifyURLs   map[string]string          `json:"notify_urls"`
	Currencies   []protocol.PaymentCurrency `json:"currencies"`
}

// settingsValues are the settings with the payment methods.
type settingsValues struct {
	Settings
	Methods []Method `json:"methods"`
}

// usable reports whether top-up is open: switched on, on an https site.
func (s *Service) usable(v Settings) bool { return v.Enabled && s.siteSecure() }

// errUnavailable is the refusal of a top-up while it is closed.
func errUnavailable(ctx context.Context) *core.Error {
	return core.ErrConflict.WithMessage(t(ctx, "online top-up is not available", "在线充值暂未开通"))
}

// notifyURL is where gateway id's notifications go; empty while the site
// address cannot take top-up (not https).
func (s *Service) notifyURL(id string) string {
	if !s.siteSecure() {
		return ""
	}
	return s.siteURL(notifyPath + id)
}
