package payment

import (
	"context"
	"net/http"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/payment/epay"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/payment/stripe"
)

// builtinGateways are the built-in gateways over the stored settings, read
// on every call. They do not look at the enabled switches: switching a
// gateway off stops new orders (gatewayOpen), while the notifications and
// queries of the orders already made keep using the stored merchant and
// secrets, which cannot change while such orders may still be paid.
func builtinGateways(s *Service, client *http.Client) map[string]core.PaymentGateway {
	return map[string]core.PaymentGateway{
		core.PaymentGatewayEpay:   epay.New(s.epayConfig, client),
		core.PaymentGatewayStripe: stripe.New(s.stripeConfig, client),
	}
}

func (s *Service) epayConfig(ctx context.Context) (epay.Config, error) {
	v, err := s.loadSettings(ctx, s.db.Gorm)
	return epayConfigOf(v), err
}

func epayConfigOf(v Settings) epay.Config {
	return epay.Config{Endpoint: v.Epay.Endpoint, MerchantID: v.Epay.MerchantID, Key: v.Epay.Key}
}

func (s *Service) stripeConfig(ctx context.Context) (stripe.Config, error) {
	v, err := s.loadSettings(ctx, s.db.Gorm)
	return stripe.Config{SecretKey: v.Stripe.SecretKey, WebhookSecret: v.Stripe.WebhookSecret}, err
}
