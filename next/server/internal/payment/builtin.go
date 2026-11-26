package payment

import (
	"context"
	"errors"
	"iter"
	"net/http"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/payment/epay"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/payment/stripe"
)

// builtinSpec is all the core knows of one built-in gateway: its switch
// and fields in the settings, the one currency it settles (if it is
// limited to one), how its own Validate errors map onto the fields, and
// how it is built over the stored settings.
type builtinSpec struct {
	id       string
	enabled  func(*Settings) bool
	fields   []builtinField
	currency string
	validate func(Settings) error
	errors   []validateErr
	build    func(settings func(context.Context) (Settings, error), client *http.Client) core.PaymentGateway
}

// builtinField is a string setting of a built-in gateway. A secret is
// sealed at rest (AAD "topup:<id>/<name>") and masked in the API; a
// merchant field names the merchant the gateway's orders were created
// under or verifies their notifications, so it cannot change while the
// gateway has orders that may still be paid.
type builtinField struct {
	name     string
	at       func(*Settings) *string
	secret   bool
	merchant bool
}

// validateErr is the field a gateway's Validate error is reported on.
type validateErr struct {
	err    error
	field  string
	en, zh string
}

var builtinSpecs = []builtinSpec{
	{
		id: epay.ID, enabled: func(v *Settings) bool { return v.Epay.Enabled }, currency: "CNY",
		fields: []builtinField{
			{name: "endpoint", at: func(v *Settings) *string { return &v.Epay.Endpoint }},
			{name: "merchant_id", at: func(v *Settings) *string { return &v.Epay.MerchantID }, merchant: true},
			{name: "key", at: func(v *Settings) *string { return &v.Epay.Key }, secret: true, merchant: true},
		},
		validate: func(v Settings) error { return epayConfigOf(v).Validate() },
		errors: []validateErr{
			{epay.ErrEndpoint, "endpoint", "an https address without credentials, a query or a fragment", "https 地址，不带用户信息、查询串和片段"},
			{epay.ErrMerchantID, "merchant_id", "required, without '&' or '='", "必填，不能含 & 或 ="},
			{epay.ErrKey, "key", "required", "必填"},
		},
		build: func(settings func(context.Context) (Settings, error), client *http.Client) core.PaymentGateway {
			return epay.New(configFrom(settings, epayConfigOf), client)
		},
	},
	{
		id: stripe.ID, enabled: func(v *Settings) bool { return v.Stripe.Enabled },
		fields: []builtinField{
			{name: "secret_key", at: func(v *Settings) *string { return &v.Stripe.SecretKey }, secret: true, merchant: true},
			{name: "webhook_secret", at: func(v *Settings) *string { return &v.Stripe.WebhookSecret }, secret: true, merchant: true},
		},
		validate: func(v Settings) error { return stripeConfigOf(v).Validate() },
		errors: []validateErr{
			{stripe.ErrSecretKey, "secret_key", "a Stripe secret key (sk_live_, sk_test_, rk_live_ or rk_test_)", "Stripe 密钥（sk_live_、sk_test_、rk_live_ 或 rk_test_ 开头）"},
			{stripe.ErrWebhookSecret, "webhook_secret", "a Stripe webhook secret (whsec_)", "Stripe Webhook 密钥（whsec_ 开头）"},
		},
		build: func(settings func(context.Context) (Settings, error), client *http.Client) core.PaymentGateway {
			return stripe.New(configFrom(settings, stripeConfigOf), client)
		},
	},
}

// builtinGateways are the built-in gateways over settings. They do not look
// at the enabled switches: switching a gateway off stops new orders
// (builtinSpec.open), while the notifications and queries of the orders
// already made keep using the stored merchant and secrets, which cannot
// change while such orders may still be paid.
func builtinGateways(settings func(context.Context) (Settings, error), client *http.Client) map[string]core.PaymentGateway {
	out := make(map[string]core.PaymentGateway, len(builtinSpecs))
	for _, b := range builtinSpecs {
		out[b.id] = b.build(settings, client)
	}
	return out
}

// builtinOf is the spec of built-in gateway id; nil for any other id.
func builtinOf(id string) *builtinSpec {
	for i := range builtinSpecs {
		if builtinSpecs[i].id == id {
			return &builtinSpecs[i]
		}
	}
	return nil
}

// open reports whether the gateway takes new orders under v: switched on,
// and v's currency is one it settles.
func (b *builtinSpec) open(v Settings) bool {
	return b.enabled(&v) && (b.currency == "" || b.currency == v.Currency)
}

// check reports the problems of the gateway's settings when it is on: its
// own Validate (over the stored secrets the mask kept), and the currency.
func (b *builtinSpec) check(f *fieldErrs, v Settings) {
	if !b.enabled(&v) {
		return
	}
	err := b.validate(v)
	for _, e := range b.errors {
		f.check(!errors.Is(err, e.err), b.id+"."+e.field, e.en, e.zh)
	}
	f.check(b.currency == "" || b.currency == v.Currency, b.id+".enabled",
		"settles "+b.currency+" only: set the currency to "+b.currency, "只支持 "+b.currency+" 结算，请把货币设为 "+b.currency)
}

// secrets yields the built-in gateways' secrets by their API path
// ("epay/key", also their AAD's), in spec order.
func secrets() iter.Seq2[string, func(*Settings) *string] {
	return func(yield func(string, func(*Settings) *string) bool) {
		for b, f := range builtinFields() {
			if f.secret && !yield(b.id+"/"+f.name, f.at) {
				return
			}
		}
	}
}

// merchantFields yields the built-in gateways' merchant fields, in spec
// order.
func merchantFields() iter.Seq2[*builtinSpec, builtinField] {
	return func(yield func(*builtinSpec, builtinField) bool) {
		for b, f := range builtinFields() {
			if f.merchant && !yield(b, f) {
				return
			}
		}
	}
}

// builtinFields yields every field of the built-in gateways with its spec.
func builtinFields() iter.Seq2[*builtinSpec, builtinField] {
	return func(yield func(*builtinSpec, builtinField) bool) {
		for i := range builtinSpecs {
			for _, f := range builtinSpecs[i].fields {
				if !yield(&builtinSpecs[i], f) {
					return
				}
			}
		}
	}
}

// builtinAudit adds each gateway's switch and plain merchant fields to a
// settings audit ("epay_enabled", "epay_merchant_id"); secrets stay out.
func builtinAudit(out map[string]any, v Settings) {
	for _, b := range builtinSpecs {
		out[b.id+"_enabled"] = b.enabled(&v)
	}
	for b, f := range merchantFields() {
		if !f.secret {
			out[b.id+"_"+f.name] = *f.at(&v)
		}
	}
}

// configFrom is a gateway's config source: of over settings.
func configFrom[C any](settings func(context.Context) (Settings, error), of func(Settings) C) func(context.Context) (C, error) {
	return func(ctx context.Context) (C, error) {
		v, err := settings(ctx)
		return of(v), err
	}
}

func epayConfigOf(v Settings) epay.Config {
	return epay.Config{Endpoint: v.Epay.Endpoint, MerchantID: v.Epay.MerchantID, Key: v.Epay.Key}
}

func stripeConfigOf(v Settings) stripe.Config {
	return stripe.Config{SecretKey: v.Stripe.SecretKey, WebhookSecret: v.Stripe.WebhookSecret}
}
