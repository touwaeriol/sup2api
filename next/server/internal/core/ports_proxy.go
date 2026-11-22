package core

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"

	"gorm.io/gorm"
)

// ============================================================ proxies (owner: A)

// Egress is the way out of one outbound request: the HTTP client (the
// proxy's, or the direct one) and whether it goes through a proxy, which
// the SSRF guard needs (netguard.CheckURLVia). Proxied is set even when the
// proxy's client cannot be had (disabled, gone): the request then fails, it
// never goes out directly. Proxy is the proxy the client goes through,
// password included, from the same cache entry as Client: what a plugin is
// shown as Account.proxy (CONTRACTS §124). It is nil for a direct
// connection, and nil whenever the client cannot be had.
type Egress struct {
	Client  *http.Client
	Proxied bool
	Proxy   *ProxyRef
}

// ProxyRef is a proxy: its id and address, password included. ID is 0 for a
// proxy URL an account form pasted, which is not stored.
type ProxyRef struct {
	ID int64
	ProxySpec
}

// String describes the proxy without its password.
func (r ProxyRef) String() string { return fmt.Sprintf("proxy %d %s", r.ID, r.ProxySpec) }

// GoString keeps %#v from printing the password.
func (r ProxyRef) GoString() string { return r.String() }

// Format keeps every verb (%d, %x ...) from printing the password.
func (r ProxyRef) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, r.String()) }

// LogValue keeps slog from printing the password.
func (r ProxyRef) LogValue() slog.Value {
	return slog.GroupValue(slog.Int64("id", r.ID), slog.Any("spec", r.ProxySpec))
}

// ProxyClient is the HTTP client of a proxy and the proxy it goes through;
// Proxy is nil for the direct client.
type ProxyClient struct {
	Client *http.Client
	Proxy  *ProxyRef
}

// ProxyDirectory hands out HTTP clients per proxy (nil = direct), cached and
// rebuilt when a proxy changes.
type ProxyDirectory interface {
	HTTPClient(ctx context.Context, proxyID *int64) (ProxyClient, error)
	// HTTPClientFor builds a transient client for a proxy that is not (or
	// not yet) stored, e.g. a proxy_url given to a models/fetch request
	// (CONTRACTS §21.4). Nothing is cached or written; the caller should
	// CloseIdleConnections when done. spec must already be validated
	// (proxy.ParseURL).
	HTTPClientFor(ctx context.Context, spec ProxySpec) (*http.Client, error)
}

// ProxySpec is a parsed proxy address (proxy.ParseURL, CONTRACTS §21.4).
// Protocol is http, https or socks5 (socks5h is normalised to socks5);
// Host is lower-case without IPv6 brackets. String, GoString, Format and
// LogValue leave the password out, so a logged spec never shows it.
type ProxySpec struct {
	Protocol string
	Host     string
	Port     int
	Username string
	Password string
}

// String describes the proxy without its password.
func (s ProxySpec) String() string {
	return fmt.Sprintf("%s://%s username=%q password=%t", s.Protocol, net.JoinHostPort(s.Host, strconv.Itoa(s.Port)), s.Username, s.Password != "")
}

// GoString keeps %#v from printing the password.
func (s ProxySpec) GoString() string { return s.String() }

// Format keeps every verb (%d, %x ...) from printing the password.
func (s ProxySpec) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, s.String()) }

// LogValue keeps slog from printing the password.
func (s ProxySpec) LogValue() slog.Value {
	return slog.GroupValue(slog.String("protocol", s.Protocol), slog.String("host", s.Host), slog.Int("port", s.Port),
		slog.String("username", s.Username), slog.Bool("password", s.Password != ""))
}

// ProxyResolver finds or creates the proxy row matching a parsed proxy URL
// when an account is saved with proxy_url (CONTRACTS §21.4). Implemented by
// proxy.Service, injected into account.Deps.
type ProxyResolver interface {
	// FindOrCreate returns the id of the enabled proxy equal to spec in
	// protocol, host, port, username and password among the rows of scope
	// (the caller's proxy:manage, CONTRACTS §21.0), or, when scope may
	// create (every row or own), inserts a new one owned by ownerID (name
	// "<protocol>://<host>:<port>", status active). It must
	// run inside the caller's transaction (tx) together with the account
	// write; it takes pg_advisory_xact_lock on the match key first so two
	// concurrent saves of the same URL never create duplicates.
	//
	// It writes no audit row and sends no broadcast: the account id the
	// audit detail needs does not exist yet when the account is being
	// created. After the account row is written, callers pass created ==
	// true rows to AuditAutoCreate in the same transaction.
	FindOrCreate(ctx context.Context, tx *gorm.DB, spec ProxySpec, ownerID int64, scope DataScope) (id int64, created bool, err error)
	// AuditAutoCreate records the audit row proxy.create {auto:true,
	// account_id} (actor = ownerID, ip from audit.WithClientIP on ctx) for a
	// proxy FindOrCreate just inserted, and announces the new proxy on
	// config:changed. Call it once per created proxy, in the same
	// transaction, after the account id is known.
	AuditAutoCreate(ctx context.Context, tx *gorm.DB, proxyID, ownerID, accountID int64) error
}
