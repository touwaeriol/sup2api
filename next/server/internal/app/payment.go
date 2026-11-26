package app

import (
	"fmt"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/netguard"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/payment"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/ratelimit"
)

// buildPayment builds online top-up (CONTRACTS §140): the built-in
// gateways reach out through the SSRF-guarded client with its default
// bound (a payment API that hangs fails the order and frees its promo use
// instead of holding them), plugin gateways come from the plugin
// registry's current generation, orders and quotes are limited per user and
// notifications per client address, and the sweep runs on one node every
// minute.
func (b *boot) buildPayment() error {
	b.pay = payment.New(payment.Deps{
		DB: b.db, Cipher: b.cipher, Ledger: b.bill, Plugins: b.reg, PublicURL: b.cfg.PublicURL,
		HTTP:        netguard.NewClient(netguard.ClientOptions{AllowPrivate: b.cfg.AllowPrivateUpstream}),
		OrderLimit:  ratelimit.NewMinute(b.rdb, "topup_order"),
		QuoteLimit:  ratelimit.NewMinute(b.rdb, "topup_quote"),
		NotifyLimit: ratelimit.NewMinute(b.rdb, "topup_notify"),
	})
	if err := b.tasks.Register(b.pay.SweepTaskSpec()); err != nil {
		return fmt.Errorf("payment sweep task: %w", err)
	}
	return nil
}
