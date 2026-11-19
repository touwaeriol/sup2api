package routes_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry/registrytest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/routes"
)

type echo struct{ last *pluginv1.HTTPRequest }

func (e *echo) HandleHTTP(_ context.Context, in *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	e.last = in
	b, _ := json.Marshal(map[string]any{"path": in.GetPath(), "params": in.GetPathParams(), "user": in.GetCaller().GetUserId(), "body": string(in.GetBody())})
	return &pluginv1.HTTPResponse{Status: 201, Body: b, Headers: map[string]*pluginv1.HeaderValues{
		"X-Plugin":   {Values: []string{"yes"}},
		"Set-Cookie": {Values: []string{"session=evil"}},
	}}, nil
}

type ext struct {
	pkg *registry.Package
	h   *echo
}

func (e ext) Package() *registry.Package { return e.pkg }
func (e ext) Grants() registry.Grants {
	return registry.Grants{"routes.admin": []byte("{}"), "routes.webhook": []byte("{}"), "routes.public": []byte("{}")}
}
func (e ext) Platform() core.PlatformPlugin   { return nil }
func (e ext) Hook() core.HookPlugin           { return nil }
func (e ext) App() core.AppPlugin             { return nil }
func (e ext) HTTP() core.HTTPPlugin           { return e.h }
func (e ext) Scheduler() core.SchedulerPlugin { return nil }

type tokens struct{}

func (tokens) VerifySession(_ context.Context, tok string) (int64, string, error) {
	switch tok {
	case "admin":
		return 1, "", nil
	case "nobody":
		return 2, "", nil
	case "owner": // account:*:own (see accounts)
		return 3, "", nil
	case "plain": // no account key
		return 4, "", nil
	}
	if u, ok := scopeUsers[tok]; ok {
		return u.id, "", nil
	}
	return 0, "", core.ErrUnauthenticated
}

// VerifyAccessKey accepts any key as the admin's: sak_<permission> carries
// plugin.demo:<permission>; sak_<token>@<permission> is the key of the
// user of token.
func (t tokens) VerifyAccessKey(ctx context.Context, raw, _ string) (core.AccessKeyIdentity, error) {
	tok, perm, found := strings.Cut(strings.TrimPrefix(raw, core.AccessKeyPrefix), "@")
	uid := int64(1)
	if !found {
		perm = tok
	} else if id, _, err := t.VerifySession(ctx, tok); err == nil {
		uid = id
	}
	return core.AccessKeyIdentity{UserID: uid, KeyID: 1, Permissions: []string{"plugin.demo:" + perm}}, nil
}

type authz struct{}

func (a authz) Can(ctx context.Context, uid int64, perm string) (bool, error) {
	set, err := a.PermissionSet(ctx, uid)
	return set.Has(perm), err
}

// PermissionSet grants the demo plugin's keys to every user but "nobody"
// and the scope test users (their own grants), narrowed by the request's
// access key as authz does.
func (authz) PermissionSet(ctx context.Context, uid int64) (core.PermissionSet, error) {
	set := core.PermissionSetOf("plugin.demo:rules:read", "plugin.demo:reset")
	for _, u := range scopeUsers {
		if u.id == uid {
			set = core.PermissionSetOf(u.keys...)
		}
	}
	if uid == 2 {
		set = core.PermissionSetOf()
	}
	return core.RestrictToAccessKey(ctx, uid, set), nil
}
func (authz) CanGrant(context.Context, int64, []string) error { return nil }
func (authz) Version() int64                                  { return 42 }

func setup(t *testing.T, opts ...routes.Option) (*gin.Engine, *echo, *registry.Registry, *registry.Package) {
	return setupManifest(t, registrytest.Manifest("demo", "1.0.0"), opts...)
}

func setupManifest(t *testing.T, m *manifest.Manifest, opts ...routes.Option) (*gin.Engine, *echo, *registry.Registry, *registry.Package) {
	gin.SetMode(gin.TestMode)
	pkg, err := registry.LoadPackage(t.TempDir(), registrytest.Package(t, m, []byte("bin"), nil), "", "unsigned")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pkg.Close() })
	reg := registry.New()
	h := &echo{}
	reg.Publish([]registry.Extension{ext{pkg: pkg, h: h}})
	engine := gin.New()
	r := httpapi.NewRouter(engine, tokens{}, authz{})
	rh := routes.New(reg, tokens{}, authz{}, opts...)
	rh.RegisterRoutes(r)
	rh.RegisterAssets(engine)
	return engine, h, reg, pkg
}

type health struct{ ok atomic.Bool }

func (h *health) Healthy() bool { return h.ok.Load() }

// An unhealthy node fences itself: plugin APIs answer 503 unavailable
// without reaching the plugin; assets are still served.
func TestPluginAPISelfFencing(t *testing.T) {
	hc := &health{}
	e, h, _, pkg := setup(t, routes.WithHealth(hc))

	w := do(e, "POST", "/api/v1/p/demo/hook", "", `{}`)
	if w.Code != http.StatusServiceUnavailable || errorCode(w) != "unavailable" || h.last != nil {
		t.Fatalf("unhealthy: %d %s", w.Code, w.Body)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After")
	}
	if w := do(e, "GET", pkg.AssetBase()+"/ui/index.html", "", ""); w.Code != 200 {
		t.Fatalf("assets while unhealthy: %d", w.Code)
	}

	hc.ok.Store(true)
	if w := do(e, "POST", "/api/v1/p/demo/hook", "", `{}`); w.Code != 201 || h.last == nil {
		t.Fatalf("healthy again: %d %s", w.Code, w.Body)
	}
}

func do(e *gin.Engine, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Cookie", "console=secret")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	return w
}

func TestPluginAPIRoutes(t *testing.T) {
	e, h, reg, _ := setup(t)

	w := do(e, "GET", "/api/v1/p/demo/echo/42?x=1", "admin", "")
	if w.Code != 201 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out["path"] != "/echo/42" || out["params"].(map[string]any)["id"] != "42" || out["user"] != float64(1) {
		t.Fatalf("forwarded %v", out)
	}
	if _, ok := h.last.GetHeaders()["authorization"]; ok {
		t.Fatal("authorization header leaked to the plugin")
	}
	if _, ok := h.last.GetHeaders()["cookie"]; ok {
		t.Fatal("cookie leaked to the plugin")
	}
	if h.last.GetQuery()["x"].GetValues()[0] != "1" {
		t.Fatal("query not forwarded")
	}
	if w.Header().Get("Set-Cookie") != "" || w.Header().Get("X-Plugin") != "yes" {
		t.Fatalf("response headers %v", w.Header())
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatal("plugin responses must be sandboxed")
	}

	// Authenticated answers carry the authz version like the core's own.
	if got := w.Header().Get(httpapi.HeaderAuthzVersion); got != "42" {
		t.Fatalf("%s = %q", httpapi.HeaderAuthzVersion, got)
	}

	if w := do(e, "GET", "/api/v1/p/demo/echo/42", "", ""); w.Code != 401 || w.Header().Get(httpapi.HeaderAuthzVersion) != "" {
		t.Fatalf("no token: %d %v", w.Code, w.Header())
	}
	if w := do(e, "GET", "/api/v1/p/demo/echo/42", "nobody", ""); w.Code != 403 || w.Header().Get(httpapi.HeaderAuthzVersion) != "42" {
		t.Fatalf("no permission: %d %v", w.Code, w.Header())
	}
	// An access key reaches the route with its permission (期 4 S4), as
	// its user, and not without it.
	h.last = nil
	if w := do(e, "GET", "/api/v1/p/demo/echo/42", core.AccessKeyPrefix+"reset", ""); w.Code != 403 || errorCode(w) != "permission_denied" || h.last != nil {
		t.Fatalf("access key without the permission: %d %s", w.Code, w.Body)
	}
	if w := do(e, "GET", "/api/v1/p/demo/echo/42", core.AccessKeyPrefix+"rules:read", ""); w.Code != 201 || h.last == nil || h.last.Caller.UserId != 1 {
		t.Fatalf("access key with the permission: %d %s", w.Code, w.Body)
	}
	if w := do(e, "POST", "/api/v1/p/demo/echo/42", "admin", ""); w.Code != 405 {
		t.Fatalf("wrong method: %d", w.Code)
	}
	if w := do(e, "GET", "/api/v1/p/demo/nope", "admin", ""); w.Code != 404 {
		t.Fatalf("unknown route: %d", w.Code)
	}
	// Webhooks need no login.
	if w := do(e, "POST", "/api/v1/p/demo/hook", "", `{"a":1}`); w.Code != 201 || h.last.GetCaller().GetUserId() != 0 || w.Header().Get(httpapi.HeaderAuthzVersion) != "" {
		t.Fatalf("webhook: %d", w.Code)
	}
	// Body limit.
	if w := do(e, "POST", "/api/v1/p/demo/hook", "", strings.Repeat("x", routes.MaxBodyBytes+1)); w.Code != 413 {
		t.Fatalf("oversized body: %d", w.Code)
	}
	// Disabled plugin.
	reg.Publish(nil)
	if w := do(e, "GET", "/api/v1/p/demo/echo/1", "admin", ""); w.Code != 503 {
		t.Fatalf("disabled plugin: %d", w.Code)
	}
}

// A route without a permission (a package that skipped validation) is
// denied to everyone rather than open to any signed-in user.
func TestPluginAPIRouteWithoutPermissionDenied(t *testing.T) {
	m := registrytest.Manifest("demo", "1.0.0")
	m.Routes = append(m.Routes, manifest.Route{Method: "GET", Path: "/open", Scope: "admin"})
	e, h, _, _ := setupManifest(t, m)
	for _, tok := range []string{"admin", "nobody"} {
		if w := do(e, "GET", "/api/v1/p/demo/open", tok, ""); w.Code != 403 || errorCode(w) != "permission_denied" || h.last != nil {
			t.Fatalf("%s: %d %s", tok, w.Code, w.Body)
		}
	}
}

func errorCode(w *httptest.ResponseRecorder) string {
	var out struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out.Error.Code
}

type auditor struct{ writes []core.PluginWrite }

func (a *auditor) AuditPluginWrite(_ context.Context, w core.PluginWrite) {
	a.writes = append(a.writes, w)
}

// Writes served by admin routes are audited with who, what and the answer;
// reads and webhooks are not (CONTRACTS §75.2).
func TestPluginAPIWritesAudited(t *testing.T) {
	m := registrytest.Manifest("demo", "1.0.0")
	m.Routes = append(m.Routes, manifest.Route{Method: "PUT", Path: "/rules/:id", Scope: "admin", Permission: "rules:read"})
	a := &auditor{}
	e, _, _, _ := setupManifest(t, m, routes.WithAudit(a))
	do(e, "GET", "/api/v1/p/demo/echo/1", "admin", "")
	do(e, "POST", "/api/v1/p/demo/hook", "", `{}`)
	do(e, "PUT", "/api/v1/p/demo/rules/7", "nobody", `{}`)
	if w := do(e, "PUT", "/api/v1/p/demo/rules/7?x=1", "admin", `{"name":"r"}`); w.Code != 201 {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	want := core.PluginWrite{UserID: 1, Plugin: "demo", Method: "PUT", Path: "/rules/7", Route: "/rules/:id", Status: 201,
		ClientIP: "192.0.2.1", Body: []byte(`{"name":"r"}`)}
	if len(a.writes) != 1 || a.writes[0].RequestID == "" {
		t.Fatalf("writes %+v", a.writes)
	}
	want.RequestID = a.writes[0].RequestID
	if !reflect.DeepEqual(a.writes[0], want) {
		t.Fatalf("write %+v, want %+v", a.writes[0], want)
	}
}

func TestPluginAssets(t *testing.T) {
	e, _, _, pkg := setup(t)
	base := pkg.AssetBase()

	w := do(e, "GET", base+"/ui/index.html", "", "")
	if w.Code != 200 || w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("html: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox allow-scripts") ||
		!strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("headers %v", w.Header())
	}
	w = do(e, "GET", base+"/i18n/en.json", "", "")
	body, _ := io.ReadAll(w.Body)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || !json.Valid(body) {
		t.Fatalf("json asset: %d", w.Code)
	}
	if w := do(e, "GET", base+"/ui/icon.svg", "", ""); w.Code != 200 || w.Header().Get("Content-Type") != "image/svg+xml" {
		t.Fatalf("icon: %d", w.Code)
	}
	for _, p := range []string{"/secret.txt", "/manifest.json", "/ui/../secret.txt", "/runtimes/x/plugin", "/forms/apikey.schema.json"} {
		if w := do(e, "GET", base+p, "", ""); w.Code != http.StatusNotFound {
			t.Fatalf("%s: %d", p, w.Code)
		}
	}
	if w := do(e, "GET", "/plugin-ui/demo/0.9.0-deadbeef/ui/index.html", "", ""); w.Code != 404 {
		t.Fatalf("stale version hash: %d", w.Code)
	}
}
