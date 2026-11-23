package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
)

// APIPrefix is the path of the console API group, the core route
// manifest.CoreRoutePrefixes reserves (/api/v1).
const APIPrefix = "/" + manifest.RouteAPI + "/" + manifest.RouteAPIVersion

// Router wraps the console API group (/api/v1).
type Router struct {
	api    *gin.RouterGroup
	tokens core.TokenVerifier
	authz  core.Authorizer
	stepUp core.StepUpVerifier
	noKeys bool
	scope  string // step-up scope of the StepUp view
	// routes records every route registered through r and its views
	// (Routes), shared by all of them.
	routes *[]*RouteSpec
}

// NewRouter mounts common middleware on engine and returns the /api/v1 router.
// tokens spends the step-up proofs of StepUp routes when it implements
// core.StepUpVerifier; otherwise those routes fail closed.
func NewRouter(engine *gin.Engine, tokens core.TokenVerifier, authz core.Authorizer) *Router {
	engine.Use(RequestContext(), Recover())
	stepUp, _ := tokens.(core.StepUpVerifier)
	return &Router{api: engine.Group(APIPrefix), tokens: tokens, authz: authz, stepUp: stepUp, routes: new([]*RouteSpec)}
}

// Public registers an unauthenticated route; on a SessionOnly or StepUp
// view it panics, since it would enforce neither.
func (r *Router) Public(method, path string, h ...gin.HandlerFunc) *RouteSpec {
	if r.noKeys || r.scope != "" {
		panic("httpapi: Public on a SessionOnly or StepUp view: " + method + " " + path)
	}
	r.api.Handle(method, path, h...)
	return r.record(method, path, ModePublic, nil)
}

// Handle registers a route for any logged-in user or access key, without a
// permission: the handler limits what the caller gets by itself. On a
// SessionOnly view it is a route for login sessions only (CONTRACTS §80:
// managing access keys and login sessions).
func (r *Router) Handle(method, path string, h ...gin.HandlerFunc) *RouteSpec {
	return r.handle(method, path, ModeAuthed, nil, nil, h)
}

// AuthedRead registers a read-only route for any logged-in user or access key:
// who the caller is and what they can see, under the key's own limits.
func (r *Router) AuthedRead(path string, h ...gin.HandlerFunc) *RouteSpec {
	return r.handle(http.MethodGet, path, ModeAuthed, nil, nil, h)
}

// Self registers a route of the caller's own account, under one of the self
// permissions (authz.SelfPermissionKeys) every signed-in user holds: a login
// session always passes, an access key only when it was given perm.
func (r *Router) Self(method, path string, key perm.Key, h ...gin.HandlerFunc) *RouteSpec {
	if !strings.HasPrefix(string(key), "self:") {
		panic("httpapi: Self with a permission outside the self module: " + string(key))
	}
	return r.Perm(method, path, key, h...)
}

// SessionOnly returns a view of r whose routes refuse access keys; r itself
// is unchanged.
func (r *Router) SessionOnly() *Router {
	v := *r
	v.noKeys = true
	return &v
}

// StepUp returns a view of r whose routes need, after their permission, a
// step-up proof for scope bound to the request's operation (header
// X-Verify-Proof, CONTRACTS §80.2): one the caller got from POST /me/verify
// with the same session or access key. Without one they answer 403
// verification_required. r itself is unchanged.
func (r *Router) StepUp(scope string) *Router {
	if scope == "" {
		panic("httpapi: StepUp without a scope")
	}
	v := *r
	v.scope = scope
	return &v
}

// Perm registers a route requiring permission.
func (r *Router) Perm(method, path string, permission perm.Key, h ...gin.HandlerFunc) *RouteSpec {
	return r.handle(method, path, ModePerm, []perm.Key{permission}, r.require(string(permission)), h)
}

// PermAny registers a route the caller may use with any one of keys, each
// unscoped (scoped actions use PermScoped). Every key the caller holds is
// recorded in the request context (Granted). Without a match the response
// is permission_denied with details.permission = keys[0].
func (r *Router) PermAny(method, path string, handler gin.HandlerFunc, keys ...perm.Key) *RouteSpec {
	if len(keys) == 0 {
		panic("httpapi: PermAny without permission keys")
	}
	return r.handle(method, path, ModeAny, keys, r.requireAny(perm.Strings(keys)), []gin.HandlerFunc{handler})
}

// PermScoped registers a route of rows: the caller needs one of actions
// with any scope (PermissionSet.HasAction), all rows or some (CONTRACTS
// §21); the handler narrows to the rows it reaches with core.Scope, which
// the middleware fills for each action held. An action held for every row
// is also recorded in Granted. Without any, the response is
// permission_denied with details.permission = actions[0].
func (r *Router) PermScoped(method, path string, handler gin.HandlerFunc, actions ...perm.Key) *RouteSpec {
	if len(actions) == 0 {
		panic("httpapi: PermScoped without an action")
	}
	return r.handle(method, path, ModeScoped, actions, r.requireScoped(actions), []gin.HandlerFunc{handler})
}

func (r *Router) requireScoped(actions []perm.Key) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		uid, _ := core.UserID(ctx)
		set, err := r.authz.PermissionSet(ctx, uid)
		if err != nil {
			Fail(c, err)
			return
		}
		var granted []string
		held := false
		for _, a := range actions {
			// Every action gets its scope, empty when not held.
			scope := set.DataScope(uid, a)
			ctx = core.WithScope(ctx, a, scope)
			held = held || !scope.Empty()
			if scope.All {
				granted = append(granted, string(a))
			}
		}
		if !held {
			Fail(c, core.ErrPermissionDenied.WithDetails(map[string]any{"permission": string(actions[0])}))
			return
		}
		c.Request = c.Request.WithContext(core.WithGranted(ctx, granted))
		c.Next()
	}
}

// handle registers a route of mode under keys: authentication, then check
// (the permission, when there is one), then the step-up proof on a StepUp
// view, then h.
func (r *Router) handle(method, path string, mode Mode, keys []perm.Key, check gin.HandlerFunc, h []gin.HandlerFunc) *RouteSpec {
	chain := r.auth()
	if check != nil {
		chain = append(chain, check)
	}
	if r.scope != "" {
		chain = append(chain, r.requireStepUp(r.scope))
	}
	r.api.Handle(method, path, append(chain, h...)...)
	return r.record(method, path, mode, keys)
}

// requireStepUp spends the request's step-up proof on scope.
func (r *Router) requireStepUp(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if r.SpendStepUp(c, scope) {
			c.Next()
		}
	}
}

// SpendStepUp spends the request's step-up proof on scope as the routes of
// a StepUp view do, rendering the failure (failing closed without a
// verifier); false: the caller stops. It is for a handler whose scope
// depends on its body, which registers its route on no StepUp view and
// spends the proof once it has read the body.
func (r *Router) SpendStepUp(c *gin.Context, scope string) bool {
	if r.stepUp == nil {
		Fail(c, core.ErrInternal.WithMessage("step-up verification is not available"))
		return false
	}
	if err := r.stepUp.ConsumeStepUp(c.Request.Context(), scope); err != nil {
		Fail(c, err)
		return false
	}
	return true
}

// Granted returns the permission keys the route middleware matched for the
// caller (see core.Granted).
func Granted(c *gin.Context) []string {
	return core.Granted(c.Request.Context())
}

// Group exposes the raw group for special cases (plugin route proxy).
func (r *Router) Group() *gin.RouterGroup { return r.api }

// auth is the authentication chain of r's routes: the bearer check, then,
// on a SessionOnly view, the access key refusal.
func (r *Router) auth() []gin.HandlerFunc {
	chain := []gin.HandlerFunc{func(c *gin.Context) {
		if Authenticate(c, r.tokens) {
			StampAuthzVersion(c, r.authz)
			c.Next()
		}
	}}
	if r.noKeys {
		chain = append(chain, func(c *gin.Context) {
			if RejectAccessKey(c) {
				c.Next()
			}
		})
	}
	return chain
}

// HeaderAuthzVersion carries core.AuthzVersioner's version on every
// authenticated response of the console API.
const HeaderAuthzVersion = "X-Authz-Version"

// StampAuthzVersion sets HeaderAuthzVersion when the authorizer tells its
// version. Router does it for its routes; a handler that authenticates by
// itself (the plugin route proxy) calls it after Authenticate.
func StampAuthzVersion(c *gin.Context, authz core.Authorizer) {
	if v, ok := authz.(core.AuthzVersioner); ok {
		c.Header(HeaderAuthzVersion, strconv.FormatInt(v.Version(), 10))
	}
}

// Authenticate resolves the request's bearer token (a login session or an
// access key) and stores the caller in the request context, with the
// step-up proof the request carries (core.WithStepUp). On failure it renders
// unauthenticated, or the server error as it is, and returns false.
func Authenticate(c *gin.Context, tokens core.TokenVerifier) bool {
	token, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
	if !ok || token == "" {
		Fail(c, core.ErrUnauthenticated)
		return false
	}
	ctx := core.WithUserAgent(c.Request.Context(), c.Request.UserAgent())
	ctx, _, err := core.AuthenticateBearer(ctx, tokens, token, c.ClientIP())
	if err != nil {
		Fail(c, authFailure(err))
		return false
	}
	ctx = core.WithStepUp(ctx, c.GetHeader(core.HeaderVerifyProof), c.Request.Method+" "+c.Request.URL.Path)
	c.Request = c.Request.WithContext(ctx)
	return true
}

// authFailure is what a failed authentication renders. A server error (the
// session store or the database down) is an outage, not a bad token: a 401
// would end every console session, and the login page cannot work either
// until it is back (CONTRACTS §100.5).
func authFailure(err error) *core.Error {
	if e := core.AsError(err); e.Status >= http.StatusInternalServerError {
		return e
	}
	return core.ErrUnauthenticated.WithCause(err)
}

// RejectAccessKey renders permission_denied and returns false when the
// caller authenticated with an access key.
func RejectAccessKey(c *gin.Context) bool {
	if _, ok := core.AccessKey(c.Request.Context()); ok {
		Fail(c, core.ErrPermissionDenied.WithMessage("access keys cannot use this endpoint"))
		return false
	}
	return true
}

func (r *Router) require(permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		uid, _ := core.UserID(ctx)
		ok, err := r.authz.Can(ctx, uid, permission)
		if err != nil {
			Fail(c, err)
			return
		}
		if !ok {
			Fail(c, core.ErrPermissionDenied.WithDetails(map[string]any{"permission": permission}))
			return
		}
		c.Request = c.Request.WithContext(core.WithGranted(ctx, []string{permission}))
		c.Next()
	}
}

func (r *Router) requireAny(keys []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if CanAny(c, r.authz, keys...) {
			c.Next()
		}
	}
}

// CanAny reports whether the caller holds one of keys (empty keys are
// skipped) and records every key it holds in the request context (Granted).
// Without a match it renders permission_denied with details.permission =
// keys[0]; handlers whose permission depends on the resource use it in place
// of a route permission.
func CanAny(c *gin.Context, authz core.Authorizer, keys ...string) bool {
	ctx := c.Request.Context()
	uid, _ := core.UserID(ctx)
	granted := make([]string, 0, len(keys))
	for _, k := range keys {
		if k == "" {
			continue
		}
		ok, err := authz.Can(ctx, uid, k)
		if err != nil {
			Fail(c, err)
			return false
		}
		if ok {
			granted = append(granted, k)
		}
	}
	if len(granted) == 0 {
		Fail(c, core.ErrPermissionDenied.WithDetails(map[string]any{"permission": keys[0]}))
		return false
	}
	c.Request = c.Request.WithContext(core.WithGranted(ctx, granted))
	return true
}

// RequestContext assigns X-Request-Id, locale (Accept-Language zh*/en) and
// the time zone of calendar figures (tz=<IANA name>; an invalid name is
// ignored here, endpoints that require one validate it themselves).
func RequestContext() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader("X-Request-Id")
		if rid == "" || len(rid) > 64 {
			rid = NewRequestID()
		}
		c.Header("X-Request-Id", rid)
		ctx := core.WithRequestID(c.Request.Context(), rid)
		if strings.HasPrefix(strings.ToLower(c.GetHeader("Accept-Language")), "zh") {
			ctx = core.WithLocale(ctx, "zh")
		}
		if loc, ok := core.LoadTimeZone(c.Query("tz")); ok {
			ctx = core.WithTimeZone(ctx, loc)
		}
		c.Request = c.Request.WithContext(ctx)
		start := time.Now()
		c.Next()
		slog.DebugContext(ctx, "http", "request_id", rid, "method", c.Request.Method,
			"path", c.Request.URL.Path, "status", c.Writer.Status(), "ms", time.Since(start).Milliseconds())
	}
}

// Recover converts panics to 500 without killing the process.
func Recover() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if p := recover(); p != nil {
				if p == http.ErrAbortHandler {
					panic(p)
				}
				slog.ErrorContext(c.Request.Context(), "panic", "request_id", core.RequestID(c.Request.Context()), "panic", p)
				if !c.Writer.Written() {
					c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": core.ErrInternal})
				}
			}
		}()
		c.Next()
	}
}

// NewRequestID returns a 24-hex-char random id.
func NewRequestID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
