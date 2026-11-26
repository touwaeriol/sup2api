package app

import (
	"fmt"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/netguard"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/payment"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/ratelimit"
)

// paymentTimeout bounds one request to a payment gateway.
const paymentTimeout = 30 * time.Second

// buildPayment builds online top-up (CONTRACTS §140): the built-in
// gateways reach out through the SSRF-guarded client, plugin gateways come
// from the plugin registry's current generation, orders and quotes are
// limited per user, and the sweep runs on one node every minute.
func (b *boot) buildPayment() error {
	b.pay = payment.New(payment.Deps{
		DB: b.db, Cipher: b.cipher, Ledger: b.bill, Plugins: b.reg, PublicURL: b.cfg.PublicURL,
		HTTP:       netguard.NewClient(netguard.ClientOptions{AllowPrivate: b.cfg.AllowPrivateUpstream, Timeout: paymentTimeout}),
		OrderLimit: ratelimit.NewMinute(b.rdb, "topup_order"),
		QuoteLimit: ratelimit.NewMinute(b.rdb, "topup_quote"),
	})
	if err := b.tasks.Register(b.pay.SweepTaskSpec()); err != nil {
		return fmt.Errorf("payment sweep task: %w", err)
	}
	return nil
}
