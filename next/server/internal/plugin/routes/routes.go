// Package routes serves plugin-owned console endpoints
// (ANY /api/v1/p/:key/*path) and plugin package assets
// (GET /plugin-ui/:key/:version_hash/*path).
package routes

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
)

// MaxBodyBytes caps request bodies forwarded to plugins.
const MaxBodyBytes = 1 << 20

// Handler serves plugin routes from the current registry generation.
type Handler struct {
	reg    core.PluginRegistry
	tokens core.TokenVerifier
	authz  core.Authorizer
	health HealthChecker
	assets VersionAssets
	audit  core.PluginWriteAuditor
	// accounts checks the accounts a route declaring manifest.RouteAccounts
	// names; without it such routes answer 503.
	accounts core.AccountAccess
}

// VersionAssets reads an approved immutable package independently of whichever
// version this node currently serves. The handler still enforces assetAllowed.
type VersionAssets interface {
	ReadVersionAsset(context.Context, string, string, string) (core.PluginInfo, []byte, string, error)
}

func WithVersionAssets(assets VersionAssets) Option { return func(h *Handler) { h.assets = assets } }

// WithAudit records every POST/PUT/PATCH/DELETE an admin route served
// (CONTRACTS §75.2).
func WithAudit(a core.PluginWriteAuditor) Option { return func(h *Handler) { h.audit = a } }

// WithAccountAccess enforces manifest.RouteAccounts (CONTRACTS §21.1).
func WithAccountAccess(aa core.AccountAccess) Option { return func(h *Handler) { h.accounts = aa } }

// HealthChecker reports whether this node may serve traffic
// (core.NodeRegistry satisfies it).
type HealthChecker interface {
	Healthy() bool
}

// Option configures optional Handler dependencies.
type Option func(*Handler)

// WithHealth makes plugin API routes answer 503 unavailable while the node
// is unhealthy (self-fencing, CONTRACTS §14.3). Assets are still served.
func WithHealth(h HealthChecker) Option {
	return func(x *Handler) { x.health = h }
}

// New creates the handler.
func New(reg core.PluginRegistry, tokens core.TokenVerifier, authz core.Authorizer, opts ...Option) *Handler {
	h := &Handler{reg: reg, tokens: tokens, authz: authz}
	for _, o := range opts {
		o(h)
	}
	return h
}

// RegisterRoutes mounts ANY /api/v1/p/:key/*path.
func (h *Handler) RegisterRoutes(r *httpapi.Router) {
	r.Group().Any("/p/:key/*path", h.serveAPI)
}

// RegisterAssets mounts GET/HEAD /plugin-ui/:key/:vh/*path on the engine.
func (h *Handler) RegisterAssets(e gin.IRoutes) {
	route := core.PluginAssetBase(":key", ":vh") + "/*path"
	e.GET(route, h.serveAsset)
	e.HEAD(route, h.serveAsset)
}

var (
	errMethodNotAllowed = core.NewError(http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	errTooLarge         = core.NewError(http.StatusRequestEntityTooLarge, "payload_too_large", "request body too large")
	errRouteNotFound    = core.ErrNotFound.WithMessage("plugin route not found")
	errNodeUnhealthy    = core.ErrUnavailable.WithMessage("this node is temporarily unavailable; retry on another node")
)

// Request headers never forwarded to plugins.
var strippedRequestHeaders = map[string]bool{
	"authorization":       true,
	"cookie":              true,
	"x-verify-proof":      true, // a step-up proof is the core's (CONTRACTS §80.2)
	"proxy-authorization": true,
	"connection":          true,
	"keep-alive":          true,
	"te":                  true,
	"trailer":             true,
	"transfer-encoding":   true,
	"upgrade":             true,
}

// Response headers never copied from plugins.
var strippedResponseHeaders = map[string]bool{
	"set-cookie":                       true,
	"content-length":                   true,
	"connection":                       true,
	"keep-alive":                       true,
	"transfer-encoding":                true,
	"upgrade":                          true,
	"trailer":                          true,
	"content-security-policy":          true,
	"access-control-allow-origin":      true,
	"access-control-allow-credentials": true,
}

func (h *Handler) serveAPI(c *gin.Context) {
	// A node that lost Redis/PG may hold a stale view of plugins and grants;
	// it stops serving plugin APIs until it recovers.
	if h.health != nil && !h.health.Healthy() {
		c.Header("Retry-After", "5")
		httpapi.Fail(c, errNodeUnhealthy)
		return
	}
	key := c.Param("key")
	sub := c.Param("path")
	if sub == "" {
		sub = "/"
	}
	gen := h.reg.Current()
	if _, ok := gen.Plugin(key); !ok {
		httpapi.Fail(c, core.ErrPluginUnavailable.WithDetails(map[string]any{"plugin_key": key}))
		return
	}
	var (
		chosen        *core.RouteBinding
		params        map[string]string
		methodMissing bool
	)
	routes := gen.Routes(key)
	for idx := range routes {
		rb := &routes[idx]
		p, ok := matchPath(rb.Route.Path, sub)
		if !ok {
			continue
		}
		if !strings.EqualFold(rb.Route.Method, c.Request.Method) {
			methodMissing = true
			continue
		}
		chosen, params = rb, p
		break
	}
	if chosen == nil {
		if methodMissing {
			httpapi.Fail(c, errMethodNotAllowed)
		} else {
			httpapi.Fail(c, errRouteNotFound)
		}
		return
	}

	var set core.PermissionSet // the caller's, on admin and user routes
	switch chosen.Route.Scope {
	case "admin", "user":
		// A session or an access key; the route's permission is checked as the
		// key narrows it (CONTRACTS §80). A route without a permission
		// matches nothing and is denied. The plugin is not told the call
		// came through a key: the admin write audit records it, and the
		// plugin's AuthzCheck/AccountScopeCheck are narrowed by it.
		if !httpapi.Authenticate(c, h.tokens) {
			return
		}
		httpapi.StampAuthzVersion(c, h.authz)
		var ok bool
		if set, ok = h.authorizeRoute(c, manifest.PermissionKey(key, chosen.Route.Permission)); !ok {
			return
		}
	case "public", "webhook":
	default:
		httpapi.Fail(c, errRouteNotFound)
		return
	}
	ctx := c.Request.Context()
	userID, _ := core.UserID(ctx) // 0 on public and webhook routes

	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, MaxBodyBytes))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			httpapi.Fail(c, errTooLarge)
			return
		}
		httpapi.Fail(c, core.ErrInvalidArgument.WithMessage("read request body").WithCause(err))
		return
	}
	if acc := chosen.Route.Accounts; acc != nil && !h.authorizeAccounts(c, acc, params, body) {
		return
	}
	actionScope, ok := h.authorizeScope(c, chosen, set, params, body)
	if !ok {
		return
	}

	req := &pluginv1.HTTPRequest{
		Caller:      CallerOf(c),
		Method:      c.Request.Method,
		Path:        sub,
		RoutePath:   chosen.Route.Path,
		PathParams:  params,
		Query:       map[string]*pluginv1.HeaderValues{},
		Headers:     map[string]*pluginv1.HeaderValues{},
		Body:        body,
		ActionScope: actionScope,
	}
	for k, v := range c.Request.URL.Query() {
		req.Query[k] = &pluginv1.HeaderValues{Values: v}
	}
	for k, v := range c.Request.Header {
		lk := strings.ToLower(k)
		if strippedRequestHeaders[lk] {
			continue
		}
		req.Headers[lk] = &pluginv1.HeaderValues{Values: v}
	}
	answered := false
	if h.audit != nil && chosen.Route.Scope == "admin" && auditedMethods[c.Request.Method] {
		// After the answer: the auditor only queues the row (CONTRACTS
		// §75.2), so the handler returns and the response ends without
		// waiting on the database. No answer means a panic, which the
		// recovery middleware answers 500.
		defer func() {
			status := http.StatusInternalServerError
			if answered {
				c.Writer.Flush()
				status = c.Writer.Status()
			}
			h.audit.AuditPluginWrite(ctx, core.PluginWrite{UserID: userID, Plugin: key, Method: c.Request.Method, Path: sub,
				Route: chosen.Route.Path, Status: status, RequestID: req.Caller.RequestId, ClientIP: req.Caller.ClientIp, Body: body})
		}()
	}
	if resp, err := chosen.Client.HandleHTTP(ctx, req); err != nil {
		httpapi.Fail(c, err)
	} else {
		writeResponse(c, resp)
	}
	answered = true
}

// CallerOf is the plugin's view of c's caller: user 0 on public and webhook
// routes.
func CallerOf(c *gin.Context) *pluginv1.Caller {
	ctx := c.Request.Context()
	userID, _ := core.UserID(ctx)
	return &pluginv1.Caller{UserId: userID, RequestId: core.RequestID(ctx), ClientIp: c.ClientIP(), Locale: core.Locale(ctx)}
}

// auditedMethods are the writes WithAudit records.
var auditedMethods = map[string]bool{http.MethodPost: true, http.MethodPut: true, http.MethodPatch: true, http.MethodDelete: true}

func writeResponse(c *gin.Context, resp *pluginv1.HTTPResponse) {
	hdr := c.Writer.Header()
	for k, v := range resp.GetHeaders() {
		lk := strings.ToLower(k)
		if strippedResponseHeaders[lk] || strings.HasPrefix(lk, "proxy-") || v == nil {
			continue
		}
		hdr.Del(k)
		for _, x := range v.GetValues() {
			hdr.Add(k, x)
		}
	}
	body := resp.GetBody()
	if hdr.Get("Content-Type") == "" && len(body) > 0 {
		if json.Valid(body) {
			hdr.Set("Content-Type", "application/json; charset=utf-8")
		} else {
			hdr.Set("Content-Type", "application/octet-stream")
		}
	}
	// Plugin responses share the console origin: never let them render as
	// an active document.
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; sandbox")
	status := int(resp.GetStatus())
	if status < 100 || status > 599 {
		status = http.StatusOK
	}
	c.Status(status)
	if c.Request.Method != http.MethodHead && len(body) > 0 {
		_, _ = c.Writer.Write(body)
	}
}

// matchPath matches a manifest route pattern ("/rules/:id", "/files/*rest")
// against a request path.
func matchPath(pattern, p string) (map[string]string, bool) {
	ps := splitPath(pattern)
	xs := splitPath(p)
	params := map[string]string{}
	for i, seg := range ps {
		if strings.HasPrefix(seg, "*") {
			params[strings.TrimPrefix(seg, "*")] = "/" + strings.Join(xs[min(i, len(xs)):], "/")
			return params, true
		}
		if i >= len(xs) {
			return nil, false
		}
		if strings.HasPrefix(seg, ":") {
			if xs[i] == "" {
				return nil, false
			}
			params[seg[1:]] = xs[i]
			continue
		}
		if seg != xs[i] {
			return nil, false
		}
	}
	if len(xs) != len(ps) {
		return nil, false
	}
	return params, true
}

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// ------------------------------------------------------------------ assets

const iframeCSP = "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; font-src 'self' data:; connect-src 'none'; frame-src 'none'; " +
	"object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'; sandbox allow-scripts"

const svgCSP = "default-src 'none'; style-src 'unsafe-inline'; sandbox"

func (h *Handler) serveAsset(c *gin.Context) {
	key, vh := c.Param("key"), c.Param("vh")
	gen := h.reg.Current()
	info, ok := gen.Plugin(key)
	current := ok && info.AssetBase == core.PluginAssetBase(key, vh)
	raw := c.Param("path")
	if strings.Contains(raw, "..") || strings.Contains(raw, "\\") {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+raw), "/")
	var data []byte
	var ct string
	var err error
	if !current {
		// A version this node does not serve costs a package load: refuse
		// what no package may serve before paying for it.
		if h.assets == nil || (!prefixAllowed(name) && !looksLikeIcon(name)) {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		info, data, ct, err = h.assets.ReadVersionAsset(c.Request.Context(), key, vh, name)
		if err != nil {
			httpapi.Fail(c, err)
			return
		}
	}
	if !assetAllowed(info, name) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	if current {
		data, ct, err = gen.ReadAsset(key, name)
		if err != nil && h.assets != nil {
			// The generation can be retired between the lookup and asset read.
			// Recover the exact approved version from shared immutable storage.
			info, data, ct, err = h.assets.ReadVersionAsset(c.Request.Context(), key, vh, name)
			if err == nil && !assetAllowed(info, name) {
				c.AbortWithStatus(http.StatusNotFound)
				return
			}
		}
	}
	if err != nil {
		if e := core.AsError(err); e.Status == http.StatusNotFound {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		httpapi.Fail(c, err)
		return
	}
	hdr := c.Writer.Header()
	hdr.Set("Cache-Control", "public, max-age=31536000, immutable")
	hdr.Set("X-Content-Type-Options", "nosniff")
	switch {
	case strings.HasPrefix(ct, "text/html"):
		hdr.Set("Content-Security-Policy", iframeCSP)
		hdr.Set("X-Frame-Options", "SAMEORIGIN")
	case strings.HasPrefix(ct, "image/svg"):
		hdr.Set("Content-Security-Policy", svgCSP)
	}
	if c.Request.Method == http.MethodHead {
		hdr.Set("Content-Type", ct)
		c.Status(http.StatusOK)
		return
	}
	c.Data(http.StatusOK, ct, data)
}

// assetAllowed reports whether a package may serve name: a file below a
// public directory, or one its manifest names as an icon.
func assetAllowed(info core.PluginInfo, name string) bool {
	return prefixAllowed(name) || iconAllowed(info, name)
}

// prefixAllowed reports whether name is a file below a public package
// directory. It needs no manifest.
func prefixAllowed(name string) bool {
	for _, prefix := range []string{"ui/", "i18n/"} {
		if strings.HasPrefix(name, prefix) && len(name) > len(prefix) {
			return true
		}
	}
	return false
}

// iconAllowed reports whether the manifest names name as the plugin's icon or
// an account type's.
func iconAllowed(info core.PluginInfo, name string) bool {
	m := info.Manifest
	if m == nil {
		return false
	}
	icons := []string{m.Icon}
	for _, at := range m.AccountTypes {
		icons = append(icons, at.Icon)
	}
	for _, icon := range icons {
		if icon != "" && !strings.HasPrefix(icon, "text:") && name == strings.TrimPrefix(path.Clean("/"+icon), "/") {
			return true
		}
	}
	return false
}

// iconExtensions are the image types the core serves (registry.ContentType):
// what a manifest icon outside the public directories can be.
var iconExtensions = []string{".svg", ".png", ".webp", ".ico", ".jpg", ".jpeg", ".gif"}

// looksLikeIcon reports whether name could be a manifest icon, by extension.
func looksLikeIcon(name string) bool {
	return slices.Contains(iconExtensions, strings.ToLower(path.Ext(name)))
}
