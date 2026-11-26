// Package payment is the core's online top-up (CONTRACTS §140): the top-up
// settings and payment methods, quotes and orders, the gateways'
// notifications, the order state machine that credits the ledger in the
// order's transaction, the sweep task, and promo codes. Gateways implement
// core.PaymentGateway: the built-in epay and stripe (subpackages) and the
// gateways plugins declare (core.PaymentGatewayGeneration), on one code path.
package payment

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/shopspring/decimal"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/secret"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// Ledger kinds and idempotency key prefixes of a top-up (the billing
// module's KindTopup and KindPromoBonus; modules share no packages).
const (
	kindTopup      = "topup"
	kindPromoBonus = "promo_bonus"
	refTypeOrder   = "payment_order"
)

// Limits of the money a top-up moves. The ledger has no cap of its own, so
// these carry over the old payment plugin's approved ledger.credit scope
// (maxPerTx 100000, maxPerDay 1000000): the settings' max_topup cannot
// exceed the first, and crediting an order fails past either.
var (
	maxCreditPerTx  = decimal.NewFromInt(100000)
	maxCreditPerDay = decimal.NewFromInt(1000000)
)

const (
	// lateWindow is how long after an order expired or was cancelled a paid
	// notification still credits it: the user may have paid in time.
	lateWindow = 24 * time.Hour
	// leaseTTL is how long a crediting lease holds before another worker
	// may take it over.
	leaseTTL = 5 * time.Minute
	// notifyBodyLimit caps a notification's body.
	notifyBodyLimit = 1 << 20
	// walletPath is where a gateway sends the user back to.
	walletPath = "/me/wallet"
	// orderRatePerMinute and quoteRatePerMinute cap a user's orders and quotes.
	orderRatePerMinute = 10
	quoteRatePerMinute = 30
)

// Ledger is the core ledger with the balance cache refresh billing asks for
// after a committed ApplyTx (billing's *Service).
type Ledger interface {
	core.Ledger
	CacheBalance(ctx context.Context, userID, ledgerID int64, balance decimal.Decimal)
}

// Admitter counts a user's requests per minute (ratelimit.Minute); nil
// admits everything.
type Admitter interface {
	Admit(ctx context.Context, id int64, rpm int, tpm int64) (ok bool, retry time.Duration, err error)
}

// Deps is what the payment module needs of the rest of the server.
type Deps struct {
	DB     *store.DB
	Cipher *secret.Cipher
	Ledger Ledger
	// Plugins gives the plugin gateways of the current generation.
	Plugins core.PluginRegistry
	// HTTP is the outbound client of the built-in gateways.
	HTTP *http.Client
	// PublicURL is the site address (SUB2API_PUBLIC_URL): the notify and
	// return addresses are built from it alone, and top-up needs https.
	PublicURL string
	// OrderLimit and QuoteLimit count a user's orders and quotes.
	OrderLimit, QuoteLimit Admitter
	// Builtin replaces the built-in gateways by id; nil builds epay and
	// stripe over the stored settings (builtin.go).
	Builtin map[string]core.PaymentGateway
}

// Service serves the top-up endpoints, notifications and the sweep.
type Service struct {
	db      *store.DB
	cipher  *secret.Cipher
	ledger  Ledger
	plugins core.PluginRegistry
	site    *url.URL
	order   Admitter
	quote   Admitter
	builtin map[string]core.PaymentGateway
	now     func() time.Time
}

// New builds the payment module.
func New(d Deps) *Service {
	s := &Service{db: d.DB, cipher: d.Cipher, ledger: d.Ledger, plugins: d.Plugins,
		order: d.OrderLimit, quote: d.QuoteLimit, builtin: d.Builtin, now: time.Now}
	if u, err := url.Parse(d.PublicURL); err == nil && u.Host != "" {
		s.site = u
	}
	if s.builtin == nil {
		s.builtin = builtinGateways(s, d.HTTP)
	}
	return s
}

// RegisterRoutes mounts the top-up endpoints.
func (s *Service) RegisterRoutes(r *httpapi.Router) {
	r.Perm("GET", "/me/topup", perm.TopupUse, s.checkout)
	r.Perm("POST", "/me/topup/preview", perm.TopupUse, s.preview)
	r.Perm("POST", "/me/topup/orders", perm.TopupUse, s.createOrder)
	r.Perm("GET", "/me/topup/orders", perm.TopupUse, s.myOrders)
	r.Perm("GET", "/me/topup/orders/:no", perm.TopupUse, s.myOrder)
	r.Perm("POST", "/me/topup/orders/:no/cancel", perm.TopupUse, s.cancelOrder)
	r.Perm("GET", "/topup/orders", perm.TopupOrderRead, s.adminOrders)
	r.Perm("GET", "/topup/orders/stats", perm.TopupOrderRead, s.orderStats)
	r.Perm("GET", "/topup/settings", perm.TopupManage, s.getSettings)
	r.Perm("PUT", "/topup/settings", perm.TopupManage, s.putSettings)
	r.Perm("GET", "/topup/gateways", perm.TopupManage, s.pluginGatewaysHandler)
	r.Perm("GET", "/promo-codes", perm.TopupManage, s.listPromos)
	r.Perm("POST", "/promo-codes", perm.TopupManage, s.createPromo)
	r.Perm("PATCH", "/promo-codes/:id", perm.TopupManage, s.updatePromo)
	r.Perm("DELETE", "/promo-codes/:id", perm.TopupManage, s.deletePromo)
	r.Perm("GET", "/promo-codes/:id/usages", perm.TopupManage, s.promoUsages)
	for _, method := range []string{"GET", "POST"} {
		r.Public(method, "/payment/notify/:gateway", s.notify)
	}
}

// siteSecure reports whether the site address is https, which top-up needs:
// gateways send users and notifications back to it.
func (s *Service) siteSecure() bool { return s.site != nil && s.site.Scheme == "https" }

// siteURL is the site address joined with path.
func (s *Service) siteURL(path string) string {
	return s.site.JoinPath(path).String()
}

func t(ctx context.Context, en, zh string) string { return core.Text(ctx, en, zh) }
