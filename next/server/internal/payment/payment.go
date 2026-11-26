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
	"sync/atomic"
	"time"

	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/secret"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// refTypeOrder is the ledger rows' ref_type of a top-up order.
const refTypeOrder = "payment_order"

const (
	// lateWindow is how long after an order expired or was cancelled a paid
	// notification still credits it: the user may have paid in time.
	lateWindow = 24 * time.Hour
	// notifyBodyLimit caps a notification's body.
	notifyBodyLimit = 1 << 20
	// walletPath is where a gateway sends the user back to.
	walletPath = "/me/wallet"
	// notifyPath is the notify route, the gateway id appended.
	notifyPath = "/api/v1/payment/notify/"
	// orderRatePerMinute and quoteRatePerMinute cap a user's orders and quotes.
	orderRatePerMinute = 10
	quoteRatePerMinute = 30
	// notifyRatePerMinute caps the notifications of one client address: far
	// above what a gateway sends, it only stops a flood of the public route.
	notifyRatePerMinute = 600
	// unverifiedEventsPerMinute caps the payment_events rows one client
	// address writes with notifications tied to no order (failed
	// verification, gateway gone): anyone can send those.
	unverifiedEventsPerMinute = 10
	// queryTimeout bounds one gateway query of the sweep, background work
	// whose round must end to take the next orders.
	queryTimeout = 20 * time.Second
)

// sqlNow is the database's clock, for the timestamps a statement sets.
var sqlNow = clause.Expr{SQL: "now()"}

// Deps is what the payment module needs of the rest of the server.
type Deps struct {
	DB     *store.DB
	Cipher *secret.Cipher
	Ledger core.CachingLedger
	// Plugins gives the plugin gateways of the current generation.
	Plugins core.PluginRegistry
	// HTTP is the outbound client of the built-in gateways.
	HTTP *http.Client
	// PublicURL is the site address (SUB2API_PUBLIC_URL): the notify and
	// return addresses are built from it alone, and top-up needs https.
	PublicURL string
	// OrderLimit and QuoteLimit count a user's orders and quotes.
	OrderLimit, QuoteLimit core.Admitter
	// NotifyLimit counts the notifications of a client address (by hash),
	// and apart from them the unverified events it writes.
	NotifyLimit core.Admitter
}

// Service serves the top-up endpoints, notifications and the sweep.
type Service struct {
	db          *store.DB
	cipher      *secret.Cipher
	ledger      core.CachingLedger
	plugins     core.PluginRegistry
	site        *url.URL
	orderLimit  core.Admitter
	quoteLimit  core.Admitter
	notifyLimit core.Admitter
	builtin     map[string]core.PaymentGateway
	dropped     dropLog
	paidCursor  atomic.Int64 // creditPaid's position
	now         func() time.Time
}

// New builds the payment module.
func New(d Deps) *Service {
	s := &Service{db: d.DB, cipher: d.Cipher, ledger: d.Ledger, plugins: d.Plugins,
		orderLimit: d.OrderLimit, quoteLimit: d.QuoteLimit, notifyLimit: d.NotifyLimit, now: time.Now}
	if u, err := url.Parse(d.PublicURL); err == nil && u.Host != "" {
		s.site = u
	}
	s.builtin = builtinGateways(s.storedSettings, d.HTTP)
	return s
}

// storedSettings reads the stored settings: the built-in gateways' config
// source, read on every call.
func (s *Service) storedSettings(ctx context.Context) (Settings, error) {
	return s.loadSettings(ctx, s.db.Gorm)
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
	r.Perm("GET", "/topup/orders/:id/events", perm.TopupOrderRead, s.orderEvents)
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
