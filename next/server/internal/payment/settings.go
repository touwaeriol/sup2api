package payment

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// settingsKey is the settings row of the top-up settings. The payment
// methods are rows of payment_methods; both are read and written together
// by GET/PUT /topup/settings.
const settingsKey = "topup"

// mask stands for a stored secret in the API; sending it back (or nothing)
// keeps the secret.
const mask = "******"

// Settings are the top-up settings. In the settings row the secret fields
// hold their ciphertext (base64); in memory and in the API the plain text
// or the mask.
type Settings struct {
	Enabled             bool           `json:"enabled"`
	Currency            string         `json:"currency"`
	Price               Num            `json:"price"` // currency per balance unit (USD)
	MinTopup            Num            `json:"min_topup"`
	MaxTopup            Num            `json:"max_topup"`
	OrderTimeoutMinutes int            `json:"order_timeout_minutes"`
	MaxPendingOrders    int            `json:"max_pending_orders"`
	AmountOptions       []Num          `json:"amount_options"`
	Discounts           []Discount     `json:"discounts"`
	Epay                EpaySettings   `json:"epay"`
	Stripe              StripeSettings `json:"stripe"`
}

// Discount: from Amount up, the user pays Rate of the price.
type Discount struct {
	Amount Num `json:"amount"`
	Rate   Num `json:"rate"`
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

// secretField is one secret of the settings, by its API path.
type secretField struct {
	path string
	at   func(*Settings) *string
}

// secretFields are the settings' secrets: sealed at rest, masked in the API.
var secretFields = []secretField{
	{"epay/key", func(s *Settings) *string { return &s.Epay.Key }},
	{"stripe/secret_key", func(s *Settings) *string { return &s.Stripe.SecretKey }},
	{"stripe/webhook_secret", func(s *Settings) *string { return &s.Stripe.WebhookSecret }},
}

func secretPaths() []string {
	out := make([]string, len(secretFields))
	for i, f := range secretFields {
		out[i] = f.path
	}
	return out
}

// defaultSettings are the settings of a site that never saved any.
func defaultSettings() Settings {
	return Settings{Currency: "CNY", Price: num(decimalOf("7.3")), MinTopup: numInt(1), MaxTopup: numInt(10000),
		OrderTimeoutMinutes: 30, MaxPendingOrders: 3, AmountOptions: []Num{}, Discounts: []Discount{}}
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

// decodeSettings decodes a settings row and opens its secrets.
func (s *Service) decodeSettings(raw json.RawMessage) (Settings, error) {
	v := defaultSettings()
	if err := json.Unmarshal(raw, &v); err != nil {
		return Settings{}, fmt.Errorf("decode top-up settings: %w", err)
	}
	for _, f := range secretFields {
		if err := s.openSecret(f, &v); err != nil {
			return Settings{}, err
		}
	}
	return v, nil
}

func (s *Service) openSecret(f secretField, v *Settings) error {
	p := f.at(v)
	if *p == "" {
		return nil
	}
	sealed, err := base64.StdEncoding.DecodeString(*p)
	if err == nil {
		var plain []byte
		if plain, err = s.cipher.Decrypt(sealed, secretAAD(f.path)); err == nil {
			*p = string(plain)
			return nil
		}
	}
	return fmt.Errorf("open top-up secret %s: %w", f.path, err)
}

// encodeSettings seals the secrets of v into a settings row.
func (s *Service) encodeSettings(v Settings) (json.RawMessage, error) {
	for _, f := range secretFields {
		p := f.at(&v)
		if *p == "" {
			continue
		}
		sealed, err := s.cipher.Encrypt([]byte(*p), secretAAD(f.path))
		if err != nil {
			return nil, err
		}
		*p = base64.StdEncoding.EncodeToString(sealed)
	}
	return json.Marshal(v)
}

// masked is v as the API shows it: each stored secret is the mask.
func masked(v Settings) Settings {
	for _, f := range secretFields {
		if p := f.at(&v); *p != "" {
			*p = mask
		}
	}
	return v
}

// keepSecrets puts the stored secret back where the input sent the mask or
// nothing.
func keepSecrets(in *Settings, stored Settings) {
	for _, f := range secretFields {
		if p := f.at(in); *p == "" || *p == mask {
			*p = *f.at(&stored)
		}
	}
}

// changedSecrets lists the secrets in differs from stored on.
func changedSecrets(in, stored Settings) []string {
	var out []string
	for _, f := range secretFields {
		if *f.at(&in) != *f.at(&stored) {
			out = append(out, f.path)
		}
	}
	return out
}

// settingsView is GET/PUT /topup/settings: the values with masked secrets,
// the paths of the secrets, and the permission that may edit them.
type settingsView struct {
	Mode         string         `json:"mode"`
	Values       settingsValues `json:"values"`
	SecretFields []string       `json:"secret_fields"`
	Permission   string         `json:"permission"`
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
