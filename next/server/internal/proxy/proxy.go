// Package proxy implements outbound proxy management and core.ProxyDirectory,
// which hands out cached *http.Client values per proxy.
package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/secret"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/netguard"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// DefaultProbeURL is requested through a proxy by POST /proxies/:id/test.
const DefaultProbeURL = "https://api.ipify.org?format=json"

var passwordAAD = []byte("proxy")

// Options tunes the service; zero values select defaults.
type Options struct {
	Events core.EventPublisher
	// Registry (optional) hides accounts of disabled plugins from
	// account_count; nil counts every account.
	Registry     core.PluginRegistry
	ProbeURL     string        // default DefaultProbeURL
	ProbeTimeout time.Duration // default 15s
	// RecheckInterval is how often a cached client re-reads its proxy row to
	// pick up changes made on other nodes (default 30s).
	RecheckInterval time.Duration
	// AllowPrivate lets direct (non-proxied) upstream connections reach
	// loopback/private/link-local addresses (config AllowPrivateUpstream,
	// SUB2API_GATEWAY_ALLOW_PRIVATE_UPSTREAM). Test setups only.
	AllowPrivate bool
	// ScopeGrants clears the scoped grants naming a deleted proxy (CONTRACTS
	// §21); needed to delete proxies.
	ScopeGrants core.ScopeGrantClearer
}

// Service serves the proxy endpoints and implements core.ProxyDirectory.
type Service struct {
	db     *store.DB
	cipher *secret.Cipher
	bus    core.Bus // optional
	opts   Options

	direct *http.Client

	mu      sync.Mutex
	clients map[int64]*entry
	epoch   uint64
}

type entry struct {
	client    *http.Client
	ref       core.ProxyRef
	status    string
	updatedAt time.Time
	checkedAt time.Time
}

var _ core.ProxyDirectory = (*Service)(nil)

// New builds the service. bus may be nil (single node).
func New(db *store.DB, cipher *secret.Cipher, bus core.Bus, opts Options) *Service {
	if opts.ProbeURL == "" {
		opts.ProbeURL = DefaultProbeURL
	}
	if opts.ProbeTimeout <= 0 {
		opts.ProbeTimeout = 15 * time.Second
	}
	if opts.RecheckInterval <= 0 {
		opts.RecheckInterval = 30 * time.Second
	}
	dialer := netguard.Dialer(0, opts.AllowPrivate)
	direct := &http.Client{Transport: &http.Transport{
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: time.Second,
	}}
	return &Service{
		db: db, cipher: cipher, bus: bus, opts: opts,
		direct:  direct,
		clients: map[int64]*entry{},
	}
}

// RegisterRoutes mounts the proxy endpoints. Every route accepts any scope of
// its action (CONTRACTS §21.0); handlers narrow their SQL with the caller's
// scope (scopeOf).
func (s *Service) RegisterRoutes(r *httpapi.Router) {
	r.PermScoped("GET", "/proxies", s.list, perm.ProxyRead)
	r.PermScoped("POST", "/proxies", s.create, perm.ProxyManage)
	r.PermScoped("GET", "/proxies/:id", s.get, perm.ProxyRead)
	r.PermScoped("GET", "/proxies/:id/location", s.location, perm.ProxyRead)
	r.PermScoped("PATCH", "/proxies/:id", s.update, perm.ProxyManage)
	r.PermScoped("DELETE", "/proxies/:id", s.delete, perm.ProxyManage)
	r.PermScoped("POST", "/proxies/:id/test", s.test, perm.ProxyManage)
	// The URL carries the plain-text password: a step-up proof each time (CONTRACTS §80.2).
	r.StepUp(core.StepUpProxyReveal).PermScoped("POST", "/proxies/:id/reveal-url", s.revealURL, perm.ProxyManage)
	r.PermScoped("POST", "/proxies/probe", s.probeURL, perm.ProxyManage)
}

// changeMsg is published on config:changed when a proxy changes.
type changeMsg struct {
	Type string `json:"type"` // "proxy"
	ID   int64  `json:"id"`
}

// Run listens for proxy changes from other nodes until ctx is done.
func (s *Service) Run(ctx context.Context) {
	if s.bus == nil {
		<-ctx.Done()
		return
	}
	cancel := s.bus.Subscribe(core.ChannelConfigChanged, func(p []byte) {
		var m changeMsg
		if json.Unmarshal(p, &m) == nil && m.Type == "proxy" && m.ID > 0 {
			s.Invalidate(m.ID)
		}
	})
	<-ctx.Done()
	cancel()
}

// Invalidate drops the cached client of one proxy.
func (s *Service) Invalidate(id int64) {
	s.mu.Lock()
	s.epoch++
	e := s.clients[id]
	delete(s.clients, id)
	s.mu.Unlock()
	if e != nil {
		e.client.CloseIdleConnections()
	}
}

func (s *Service) changed(ctx context.Context, id int64) {
	s.Invalidate(id)
	if s.bus == nil {
		return
	}
	b, _ := json.Marshal(changeMsg{Type: "proxy", ID: id})
	if err := s.bus.Publish(ctx, core.ChannelConfigChanged, b); err != nil {
		slog.WarnContext(ctx, "proxy: publish config:changed", "err", err)
	}
}

// ---------------------------------------------------------------- directory

// newTransport builds an upstream transport for the given proxy URL. It sets
// no dial or TLS handshake timeout of its own: the operating system's
// connect timeout applies, and the caller's timers (the gateway's response
// header and relay timeouts, which start before the dial) bound the rest.
// Unless
// allowPrivate, the dial to the proxy (socks5 included: it goes through
// DialContext too) is refused when the resolved address is not public, so a
// host name pointing inside the deployment's network is caught as well.
func newTransport(proxyURL *url.URL, allowPrivate bool) *http.Transport {
	dialer := netguard.Dialer(0, allowPrivate)
	tr := &http.Transport{
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	if proxyURL != nil {
		tr.Proxy = http.ProxyURL(proxyURL)
	}
	return tr
}

type row struct {
	ID           int64
	Name         string
	Protocol     string
	Host         string
	Port         int
	Username     string
	PasswordEnc  []byte
	Status       string
	AllowPrivate bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (s *Service) proxyURL(r *row) (*url.URL, error) {
	u := &url.URL{Scheme: r.Protocol, Host: net.JoinHostPort(r.Host, strconv.Itoa(r.Port))}
	if r.Username != "" || len(r.PasswordEnc) > 0 {
		pw, err := s.password(r)
		if err != nil {
			return nil, err
		}
		u.User = url.UserPassword(r.Username, pw)
	}
	return u, nil
}

// password is the stored password of r in plain text ("" when none).
func (s *Service) password(r *row) (string, error) {
	if len(r.PasswordEnc) == 0 {
		return "", nil
	}
	b, err := s.cipher.Decrypt(r.PasswordEnc, passwordAAD)
	if err != nil {
		return "", fmt.Errorf("decrypt proxy password: %w", err)
	}
	return string(b), nil
}

func (s *Service) buildClient(r *row) (*http.Client, error) {
	u, err := s.proxyURL(r)
	if err != nil {
		return nil, err
	}
	return s.clientFor(r, u)
}

// buildEntry is buildClient together with the proxy it goes through (the
// password decrypted once, for both).
func (s *Service) buildEntry(id int64, r *row) (*entry, error) {
	u, err := s.proxyURL(r)
	if err != nil {
		return nil, err
	}
	c, err := s.clientFor(r, u)
	if err != nil {
		return nil, err
	}
	pw, _ := u.User.Password()
	return &entry{client: c, ref: core.ProxyRef{ID: id, ProxySpec: core.ProxySpec{
		Protocol: r.Protocol, Host: r.Host, Port: r.Port, Username: r.Username, Password: pw}}}, nil
}

// clientFor is the client of r through proxy URL u.
func (s *Service) clientFor(r *row, u *url.URL) (*http.Client, error) {
	// A proxy saved under the own key must not itself be a private address
	// unless the operator had proxy:manage (CONTRACTS §43.3): a literal
	// address fails here, a name resolving to one fails when dialing.
	allowPrivate := r.AllowPrivate || s.opts.AllowPrivate
	if !allowPrivate && netguard.LiteralPrivate(r.Host) {
		return nil, core.ErrUnavailable.WithMessage("proxy address is private and not allowed")
	}
	return &http.Client{Transport: newTransport(u, allowPrivate)}, nil
}

// scopeOf is the proxies the caller reaches with action, as the route
// (Router.PermScoped) computed it; outside such a route it reaches none.
func (s *Service) scopeOf(ctx context.Context, action perm.Key) core.DataScope {
	scope, _ := core.Scope(ctx, nil, action)
	return scope
}

// canCreate fails unless scope may create proxies: every row, or own (a new
// proxy is the caller's). proxy:manage:proxy/X reaches those proxies only.
func canCreate(ctx context.Context, scope core.DataScope) error {
	if scope.All || scope.Owned() {
		return nil
	}
	return core.ErrPermissionDenied.WithMessage(t(ctx,
		"creating a proxy requires proxy:manage for every proxy or your own",
		"新建代理需要全部或自己创建范围的 proxy:manage 权限")).
		WithDetails(map[string]any{"permission": "proxy:manage:own"})
}

// checkPrivate decides whether a proxy at host may be written in scope: only
// every-row proxy:manage may save a private address, and only rows it writes
// are stored with allow_private (CONTRACTS §43.3).
func (s *Service) checkPrivate(ctx context.Context, scope core.DataScope, host string) (allowPrivate bool, err error) {
	if scope.Restricted() && !s.opts.AllowPrivate && netguard.LiteralPrivate(host) {
		return false, core.ErrPermissionDenied.WithMessage(t(ctx,
			"saving a proxy at a private address requires proxy:manage",
			"保存私网地址代理需要 proxy:manage 权限")).
			WithDetails(map[string]any{"permission": "proxy:manage"})
	}
	return !scope.Restricted(), nil
}

// privateScope is the caller's proxy:manage scope, together with
// checkPrivate's answer for host.
func (s *Service) privateScope(ctx context.Context, host string) (scope core.DataScope, allowPrivate bool, err error) {
	scope = s.scopeOf(ctx, perm.ProxyManage)
	allowPrivate, err = s.checkPrivate(ctx, scope, host)
	return scope, allowPrivate, err
}

// HTTPClient returns the client for proxyID and the proxy it goes through,
// from one cache entry; nil means a direct connection. The client has no
// overall timeout: callers bound requests with ctx.
func (s *Service) HTTPClient(ctx context.Context, proxyID *int64) (core.ProxyClient, error) {
	if proxyID == nil {
		return core.ProxyClient{Client: s.direct}, nil
	}
	id := *proxyID
	now := time.Now()
	s.mu.Lock()
	e := s.clients[id]
	epoch := s.epoch
	s.mu.Unlock()
	if e != nil && now.Sub(e.checkedAt) < s.opts.RecheckInterval {
		return e.result()
	}
	var r row
	err := store.G(ctx, s.db.Gorm).Model(&model.Proxy{}).
		Select("protocol, host, port, username, password_enc, status, allow_private, updated_at").
		Where("id = ?", id).Take(&r).Error
	if store.IsNoRows(err) {
		s.Invalidate(id)
		return core.ProxyClient{}, core.ErrNotFound.WithMessage("proxy not found")
	}
	if err != nil {
		if e != nil {
			// Keep serving the last known client when the database hiccups.
			return e.result()
		}
		return core.ProxyClient{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur := s.clients[id]; cur != nil && !cur.updatedAt.Before(r.UpdatedAt) {
		if s.epoch == epoch && cur.updatedAt.Equal(r.UpdatedAt) {
			copy := *cur
			copy.checkedAt = now
			s.clients[id] = &copy
		}
		return cur.result()
	}
	if s.epoch != epoch {
		return core.ProxyClient{}, core.ErrUnavailable.WithMessage("proxy changed while resolving; retry")
	}
	ne, err := s.buildEntry(id, &r)
	if err != nil {
		return core.ProxyClient{}, err
	}
	if old := s.clients[id]; old != nil {
		old.client.CloseIdleConnections()
	}
	ne.status, ne.updatedAt, ne.checkedAt = r.Status, r.UpdatedAt, now
	s.clients[id] = ne
	return ne.result()
}

// result is the entry's client and proxy; a disabled proxy gives neither.
func (e *entry) result() (core.ProxyClient, error) {
	if e.status != "active" {
		// Never silently fall back to a direct connection.
		return core.ProxyClient{}, core.ErrUnavailable.WithMessage("proxy disabled")
	}
	ref := e.ref
	return core.ProxyClient{Client: e.client, Proxy: &ref}, nil
}

// ---------------------------------------------------------------- HTTP

// Proxy is the API view of a proxies row (the password is never returned).
type Proxy struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Protocol     string `json:"protocol"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Username     string `json:"username"`
	HasPassword  bool   `json:"has_password"`
	Status       string `json:"status"`
	AccountCount int64  `json:"account_count"`
	CreatedBy    *int64 `json:"created_by"`
	// CreatedByEmail is the creator's email, also for soft-deleted users.
	CreatedByEmail  *string    `json:"created_by_email"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	RealIP          *string    `json:"real_ip,omitempty"`
	RealIPUpdatedAt *time.Time `json:"real_ip_updated_at,omitempty"`
}

// proxyColumns are the columns of Proxy; account_count only counts accounts
// of enabled plugins (both placeholders take the same pluginKeys value).
const proxyColumns = `p.id, p.name, p.protocol, p.host, p.port, p.username, p.password_enc IS NOT NULL AS has_password,
	p.status, (SELECT count(*) FROM accounts a WHERE a.proxy_id = p.id AND a.deleted_at IS NULL
	  AND (?::text[] IS NULL OR a.plugin_key = ANY(?))) AS account_count,
	p.created_by, u.email AS created_by_email, p.created_at, p.updated_at, p.real_ip, p.real_ip_updated_at`

// proxyView selects Proxy rows of proxies p, counting the accounts of the
// plugins keys (core.ActivePluginKeys; nil counts every account).
func proxyView(db *gorm.DB, keys []string) *gorm.DB {
	arg := store.NullStrings(keys)
	return db.Table("proxies p").Select(proxyColumns, arg, arg).Joins("LEFT JOIN users u ON u.id = p.created_by")
}

func (s *Service) pluginKeys() []string { return core.ActivePluginKeys(s.opts.Registry) }

// scoped narrows proxies p to row id within scope.
func scoped(db *gorm.DB, id int64, scope core.DataScope) *gorm.DB {
	return db.Table("proxies p").Where("p.id = ?", id).Scopes(core.ProxyRows().Scope(scope))
}

func t(ctx context.Context, en, zh string) string { return core.Text(ctx, en, zh) }

func notFound(ctx context.Context) error {
	return core.ErrNotFound.WithMessage(t(ctx, "proxy not found", "代理不存在"))
}

// load returns one proxy in scope; others are 404.
func (s *Service) load(ctx context.Context, id int64, scope core.DataScope) (*Proxy, error) {
	var p Proxy
	err := proxyView(scoped(store.G(ctx, s.db.Gorm), id, scope), s.pluginKeys()).Take(&p).Error
	if store.IsNoRows(err) {
		return nil, notFound(ctx)
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Service) list(c *gin.Context) {
	ctx := c.Request.Context()
	page, size := httpapi.Pagination(c)
	q := strings.TrimSpace(c.Query("q"))
	status := c.Query("status")
	// Visible range: the caller's proxy:read scope (CONTRACTS §21.0);
	// mine=true narrows to own rows and created_by=<id> to that creator's,
	// both within it.
	var creator *int64
	if mine, _ := strconv.ParseBool(c.Query("mine")); mine {
		uid, _ := core.UserID(ctx)
		creator = &uid
	}
	if v := c.Query("created_by"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			httpapi.Fail(c, core.InvalidFields(core.FieldError{Field: "created_by", Code: "invalid",
				Message: t(ctx, "created_by must be a user id", "created_by 必须是用户 ID")}))
			return
		}
		if creator != nil && *creator != id {
			id = 0 // mine and someone else: nothing
		}
		creator = &id
	}
	filter := listFilter(q, status, creator, s.scopeOf(ctx, perm.ProxyRead))
	g := store.G(ctx, s.db.Gorm)
	var total int64
	if err := g.Table("proxies p").Scopes(filter).Count(&total).Error; err != nil {
		httpapi.Fail(c, err)
		return
	}
	items := []*Proxy{}
	if err := proxyView(g, s.pluginKeys()).Scopes(filter).Order("p.id").Limit(size).Offset((page - 1) * size).
		Find(&items).Error; err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.List(c, items, httpapi.Page{Page: page, PageSize: size, Total: total})
}

// listFilter is the WHERE clause of GET /proxies over proxies p: q matches
// within the name or host (a % or _ in q stays a wildcard, as it was),
// status and creator narrow when set, all within scope.
func listFilter(q, status string, creator *int64, scope core.DataScope) func(*gorm.DB) *gorm.DB {
	var f store.Conds
	if q != "" {
		f.And("(p.name ILIKE ? OR p.host ILIKE ?)", "%"+q+"%", "%"+q+"%")
	}
	if status != "" {
		f.And("p.status = ?", status)
	}
	if creator != nil {
		f.And("p.created_by = ?", *creator)
	}
	return append(f, core.ProxyRows().Scope(scope)).Scope()
}

func (s *Service) get(c *gin.Context) {
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	ctx := c.Request.Context()
	p, err := s.load(ctx, id, s.scopeOf(ctx, perm.ProxyRead))
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, p)
}

type input struct {
	Name     *string `json:"name"`
	Protocol *string `json:"protocol"`
	Host     *string `json:"host"`
	Port     *int    `json:"port"`
	Username *string `json:"username"`
	// Password: absent or null keeps the current one, "" clears it.
	Password *string `json:"password"`
	Status   *string `json:"status"`
}

func (in *input) validate(ctx context.Context, create bool) error {
	var fe []core.FieldError
	add := func(field, code, en, zh string) {
		fe = append(fe, core.FieldError{Field: field, Code: code, Message: t(ctx, en, zh)})
	}
	req := func(field string, set bool) bool {
		if !set && create {
			add(field, "required", field+" is required", field+" 必填")
			return false
		}
		return set
	}
	if req("name", in.Name != nil) {
		n := strings.TrimSpace(*in.Name)
		in.Name = &n
		if n == "" || utf8.RuneCountInString(n) > 100 {
			add("name", "invalid", "name must be 1-100 characters", "名称长度须为 1-100 个字符")
		}
	}
	if req("protocol", in.Protocol != nil) {
		switch *in.Protocol {
		case "http", "https", "socks5":
		default:
			add("protocol", "invalid", "protocol must be http, https or socks5", "协议必须为 http、https 或 socks5")
		}
	}
	if req("host", in.Host != nil) {
		h := strings.TrimSpace(*in.Host)
		in.Host = &h
		if h == "" || len(h) > 255 || strings.ContainsAny(h, "/?#@ \t") {
			add("host", "invalid", "invalid host", "主机地址无效")
		}
	}
	if req("port", in.Port != nil) && (*in.Port < 1 || *in.Port > 65535) {
		add("port", "invalid", "port must be 1-65535", "端口必须为 1-65535")
	}
	if in.Username != nil && len(*in.Username) > 255 {
		add("username", "too_long", "username is too long", "用户名过长")
	}
	if in.Password != nil && len(*in.Password) > 1024 {
		add("password", "too_long", "password is too long", "密码过长")
	}
	if in.Status != nil && *in.Status != "active" && *in.Status != "disabled" {
		add("status", "invalid", "status must be active or disabled", "状态必须为 active 或 disabled")
	}
	if len(fe) > 0 {
		return core.InvalidFields(fe...)
	}
	return nil
}

// addressChanged reports whether the PATCH moves the proxy: a protocol, host
// or port other than the stored one.
func (in *input) addressChanged(cur *row) bool {
	return differs(in.Protocol, cur.Protocol) || differs(in.Host, cur.Host) || differs(in.Port, cur.Port)
}

// connectionChanged reports whether the PATCH changes how the proxy is
// reached: its address or its credentials. A stored password that cannot be
// decrypted counts as different from any given one.
func (s *Service) connectionChanged(in *input, cur *row) bool {
	if in.addressChanged(cur) || differs(in.Username, cur.Username) {
		return true
	}
	if in.Password == nil {
		return false
	}
	pw, err := s.password(cur)
	return err != nil || *in.Password != pw
}

// errInUse refuses an owner changing the connection of a proxy that accounts
// of other users go through (details.account_count).
var errInUse = core.NewError(http.StatusConflict, "proxy_in_use", "proxy in use")

// connectionColumns are the columns of proxies p that say how the proxy is
// reached.
const connectionColumns = "p.protocol, p.host, p.port, p.username, p.password_enc"

// inUseByOthers counts the live accounts on proxy id that uid does not own
// (accounts.owner_id; uid 0: every account).
func inUseByOthers(ctx context.Context, q *gorm.DB, id, uid int64) (n int64, err error) {
	err = store.G(ctx, q).Model(&model.Account{}).
		Where("proxy_id = ? AND deleted_at IS NULL AND owner_id IS DISTINCT FROM ?", id, uid).Count(&n).Error
	return n, err
}

// differs reports whether a PATCH field is present with a value other than
// the stored one.
func differs[T comparable](in *T, cur T) bool { return in != nil && *in != cur }

func (s *Service) encryptPassword(pw *string) ([]byte, error) {
	if pw == nil || *pw == "" {
		return nil, nil
	}
	return s.cipher.Encrypt([]byte(*pw), passwordAAD)
}

// changes are the columns a PATCH writes: the fields present (enc is the
// encrypted password, nil clearing it), allow_private when set, and always
// updated_at, the version of the cached client (Service.HTTPClient), at
// clock_timestamp() as the raw SQL had it.
func (in *input) changes(enc []byte, allowPrivate *bool) map[string]any {
	set := map[string]any{"updated_at": gorm.Expr("clock_timestamp()")}
	store.SetIf(set, "name", in.Name)
	store.SetIf(set, "protocol", in.Protocol)
	store.SetIf(set, "host", in.Host)
	store.SetIf(set, "port", in.Port)
	store.SetIf(set, "username", in.Username)
	if in.Password != nil {
		set["password_enc"] = enc
	}
	store.SetIf(set, "status", in.Status)
	store.SetIf(set, "allow_private", allowPrivate)
	return set
}

// changedFields lists the input fields present in a PATCH (audit detail:
// names only, never values).
func (in *input) changedFields() []string {
	var out []string
	if in.Name != nil {
		out = append(out, "name")
	}
	if in.Protocol != nil {
		out = append(out, "protocol")
	}
	if in.Host != nil {
		out = append(out, "host")
	}
	if in.Port != nil {
		out = append(out, "port")
	}
	if in.Username != nil {
		out = append(out, "username")
	}
	if in.Password != nil {
		out = append(out, "password")
	}
	if in.Status != nil {
		out = append(out, "status")
	}
	return out
}

func (s *Service) create(c *gin.Context) {
	ctx := audit.Context(c)
	var in input
	if !httpapi.BindJSON(c, &in) {
		return
	}
	if err := in.validate(ctx, true); err != nil {
		httpapi.Fail(c, err)
		return
	}
	enc, err := s.encryptPassword(in.Password)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	user, status := "", "active"
	if in.Username != nil {
		user = *in.Username
	}
	if in.Status != nil {
		status = *in.Status
	}
	scope, allowPriv, err := s.privateScope(ctx, *in.Host)
	if err == nil {
		err = canCreate(ctx, scope)
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	uid, _ := core.UserID(ctx)
	var id int64
	err = s.db.Tx(ctx, func(tx *gorm.DB) error {
		p := newProxy(*in.Name, *in.Protocol, *in.Host, *in.Port, user, enc, status, allowPriv, uid)
		if err := store.G(ctx, tx).Create(p).Error; err != nil {
			return err
		}
		id = p.ID
		return audit.Audit(ctx, tx, uid, "proxy.create", "proxy", strconv.FormatInt(id, 10), map[string]any{
			"auto": false, "name": *in.Name, "protocol": *in.Protocol, "host": *in.Host, "port": *in.Port,
		})
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	p, err := s.load(ctx, id, core.AllScope)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.Created(c, p)
}

// newProxy is a proxies row to insert. GORM leaves a zero username or
// allow_private out of the INSERT (both columns have a default); those
// defaults, the empty string and false, are those zero values, so the row
// is as given.
func newProxy(name, protocol, host string, port int, username string, enc []byte, status string, allowPrivate bool, owner int64) *model.Proxy {
	return &model.Proxy{Name: name, Protocol: protocol, Host: host, Port: port, Username: username,
		PasswordEnc: enc, Status: &status, AllowPrivate: allowPrivate, CreatedBy: &owner}
}

func (s *Service) update(c *gin.Context) {
	ctx := audit.Context(c)
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	var in input
	if !httpapi.BindJSON(c, &in) {
		return
	}
	if err := in.validate(ctx, false); err != nil {
		httpapi.Fail(c, err)
		return
	}
	enc, err := s.encryptPassword(in.Password)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	scope := s.scopeOf(ctx, perm.ProxyManage)
	uid, _ := core.UserID(ctx)
	err = s.db.Tx(ctx, func(tx *gorm.DB) error {
		// Visibility first (404 outside the caller's range); the lock keeps
		// what the checks below read true until the commit.
		var cur row
		err := scoped(store.G(ctx, tx), id, scope).Select(connectionColumns).Clauses(store.ForUpdate()).Take(&cur).Error
		if store.IsNoRows(err) {
			return notFound(ctx)
		}
		if err != nil {
			return err
		}
		// A host spelled in another case is the stored host: not written, so
		// "unchanged" below means exactly that.
		if in.Host != nil && strings.EqualFold(*in.Host, cur.Host) {
			in.Host = nil
		}
		// A new address (protocol, host or port) is judged like a new proxy.
		// allow_private follows whoever set the host last, and an owner's
		// address edit clears it; proxy:manage moving only the port or
		// protocol keeps it (CONTRACTS §43.3). The console sends every
		// field, so only a value that differs counts.
		var allowPriv *bool
		if in.addressChanged(&cur) {
			host := cur.Host
			if in.Host != nil {
				host = *in.Host
			}
			allow, err := s.checkPrivate(ctx, scope, host)
			if err != nil {
				return err
			}
			if in.Host != nil || scope.Restricted() {
				allowPriv = &allow
			}
		}
		// Accounts of other users go through this proxy too: where they
		// connect is not the owner's to change (the full key may).
		if scope.Restricted() && s.connectionChanged(&in, &cur) {
			n, err := inUseByOthers(ctx, tx, id, uid)
			if err != nil {
				return err
			}
			if n > 0 {
				return errInUse.WithMessage(t(ctx,
					"accounts of other users use this proxy; its connection settings can only be changed with proxy:manage",
					"该代理正被其他用户的账号使用，连接设置只能由持有 proxy:manage 的人修改")).
					WithDetails(map[string]any{"account_count": n})
			}
		}
		if err := store.G(ctx, tx).Model(&model.Proxy{}).Where("id = ?", id).
			Updates(in.changes(enc, allowPriv)).Error; err != nil {
			return err
		}
		if err := s.notifyAccountProxyChange(ctx, tx, id, "proxy"); err != nil {
			return err
		}
		return audit.Audit(ctx, tx, uid, "proxy.update", "proxy", strconv.FormatInt(id, 10),
			map[string]any{"fields": in.changedFields()})
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.changed(ctx, id)
	p, err := s.load(ctx, id, scope)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, p)
}

func (s *Service) delete(c *gin.Context) {
	ctx := audit.Context(c)
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	scope := s.scopeOf(ctx, perm.ProxyManage)
	uid, _ := core.UserID(ctx)
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		// Visibility first: a proxy outside the caller's range is 404 even
		// when it is in use.
		var name string
		err := scoped(store.G(ctx, tx), id, scope).Select("p.name").Clauses(store.ForUpdate()).Take(&name).Error
		if store.IsNoRows(err) {
			return notFound(ctx)
		}
		if err != nil {
			return err
		}
		// No user has id 0: every account counts.
		n, err := inUseByOthers(ctx, tx, id, 0)
		if err != nil {
			return err
		}
		if n > 0 {
			// Deleting would silently switch these accounts to direct connections.
			return core.ErrConflict.WithMessage(t(ctx,
				"the proxy is used by accounts; reassign them first",
				"该代理仍被账号使用，请先修改这些账号的代理")).WithDetails(map[string]any{"account_count": n})
		}
		if err := store.G(ctx, tx).Where("id = ?", id).Delete(&model.Proxy{}).Error; err != nil {
			return err
		}
		if _, err = s.opts.ScopeGrants.ClearScopeGrants(ctx, tx, uid, core.ScopeRefProxy, []int64{id}); err != nil {
			return err
		}
		return audit.Audit(ctx, tx, uid, "proxy.delete", "proxy", strconv.FormatInt(id, 10), map[string]any{"name": name})
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.changed(ctx, id)
	httpapi.NoContent(c)
}

// revealURL returns the stored proxy as one URL including the decrypted
// password (protocol://user:pass@host:port), for the console's copy action.
// It needs the manage key (whoever may edit the proxy may read its password),
// is scoped like the other manage routes, and is audited.
func (s *Service) revealURL(c *gin.Context) {
	ctx := audit.Context(c)
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	var r row
	err := scoped(store.G(ctx, s.db.Gorm), id, s.scopeOf(ctx, perm.ProxyManage)).Select(connectionColumns).Take(&r).Error
	if store.IsNoRows(err) {
		httpapi.Fail(c, notFound(ctx))
		return
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	u, err := s.proxyURL(&r)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	uid, _ := core.UserID(ctx)
	if err := audit.Audit(ctx, s.db.Gorm, uid, "proxy.url.reveal", "proxy", strconv.FormatInt(id, 10), nil); err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, gin.H{"url": u.String()})
}

// TestResult is returned by POST /proxies/:id/test.
type TestResult struct {
	OK        bool   `json:"ok"`
	Status    int    `json:"status"`
	LatencyMs int64  `json:"latency_ms"`
	IP        string `json:"ip,omitempty"`
	Message   string `json:"message,omitempty"`
}

func (s *Service) test(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	var r row
	err := scoped(store.G(ctx, s.db.Gorm), id, s.scopeOf(ctx, perm.ProxyManage)).
		Select(connectionColumns + ", p.allow_private").Take(&r).Error
	if store.IsNoRows(err) {
		httpapi.Fail(c, notFound(ctx))
		return
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	// A fresh client, so the test reflects the stored settings even if the
	// proxy is disabled or the cached client is stale.
	client, err := s.buildClient(&r)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	defer client.CloseIdleConnections()
	result := s.probe(ctx, client)

	// Save the real IP to database if probe succeeded
	if result.OK && result.IP != "" {
		if err := s.recordObservedIP(ctx, id, result.IP); err != nil {
			httpapi.Fail(c, err)
			return
		}
	}

	httpapi.OK(c, result)
}

func (s *Service) recordObservedIP(ctx context.Context, id int64, ip string) error {
	return s.db.Tx(ctx, func(tx *gorm.DB) error {
		g := store.G(ctx, tx)
		var previous string
		if err := g.Model(&model.Proxy{}).Select("COALESCE(real_ip, '')").Where("id = ?", id).Clauses(store.ForUpdate()).
			Take(&previous).Error; err != nil {
			return err
		}
		// updated_at stays: it versions the cached client (HTTPClient), which
		// a probe leaves as it is.
		if err := g.Model(&model.Proxy{}).Where("id = ?", id).
			Updates(map[string]any{"real_ip": ip, "real_ip_updated_at": gorm.Expr("now()")}).Error; err != nil {
			return err
		}
		if previous == ip {
			return nil
		}
		return s.notifyAccountProxyChange(ctx, tx, id, "proxy.real_ip")
	})
}

func (s *Service) notifyAccountProxyChange(ctx context.Context, tx *gorm.DB, id int64, field string) error {
	if s.opts.Events == nil {
		return nil
	}
	g := store.G(ctx, tx)
	var accounts []model.Account
	if err := g.Model(&model.Account{}).Select("id, plugin_key, type, name").
		Where("proxy_id = ? AND deleted_at IS NULL", id).Order("id").Find(&accounts).Error; err != nil {
		return err
	}
	var events []core.Event
	for _, a := range accounts {
		events = append(events, core.Event{Type: core.EventAccountUpdated, Payload: map[string]any{"account_id": a.ID, "plugin_key": a.PluginKey, "type": a.Type, "name": a.Name, "changed_fields": []string{field}}})
	}
	return s.opts.Events.Emit(ctx, tx, events...)
}

func (s *Service) probe(ctx context.Context, client *http.Client) TestResult {
	ctx, cancel := context.WithTimeout(ctx, s.opts.ProbeTimeout)
	defer cancel()

	// Use a service that returns JSON with the client's IP address
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.opts.ProbeURL, nil)
	if err != nil {
		return TestResult{Message: err.Error()}
	}
	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start).Milliseconds()
	if errors.Is(err, netguard.ErrPrivate) {
		// The refused address is what the name resolved to: not echoed.
		return TestResult{LatencyMs: latency, Message: "proxy address is private and not allowed"}
	}
	if err != nil {
		return TestResult{LatencyMs: latency, Message: err.Error()}
	}
	defer resp.Body.Close()

	res := TestResult{OK: resp.StatusCode < 400, Status: resp.StatusCode, LatencyMs: latency}
	if !res.OK {
		res.Message = resp.Status
		return res
	}

	// Parse IP from response
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var ipResp struct {
		IP string `json:"ip"`
	}
	if json.Unmarshal(body, &ipResp) == nil && ipResp.IP != "" {
		res.IP = ipResp.IP
	}

	return res
}
