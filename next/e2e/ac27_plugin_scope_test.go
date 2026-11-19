package e2e

import (
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"
)

// AC 27: plugin resource scopes (CONTRACTS §21.0). A derived guard plugin
// declares an enum scope on rules:read (GET /rules checks it through
// scopeFrom query:set) and on stats:read (its menu). The core decides before
// forwarding, so guard itself, which ignores ?set=, needs no change.

// scopePlugin installs a fresh guard derivative with the scopes above and
// returns its key.
func (e *Env) scopePlugin(admin *Session) string {
	e.T.Helper()
	dp := e.NewDerivedPlugin(admin, "e2esc")
	pkg := dp.Package(e, scopedGuardManifest)
	pkg[statsPage] = []byte(`<!doctype html><title>stats</title>`)
	pkg.Sign(e.T, dp.Signer)
	e.InstallVersion(admin, pkg, nil)
	return dp.Key
}

// scopedGuardManifest gives rules:read and stats:read the values a and b,
// makes GET /rules name its value in ?set=, and adds an iframe page (the
// package file statsPage) for the stats:read menu (DerivedGuardPackage drops
// the native dashboard; declarative table pages are gone).
func scopedGuardManifest(m map[string]any) {
	values := map[string]any{"values": []any{
		map[string]any{"key": "a", "label": map[string]any{"en": "Set A", "zh": "A 组"}},
		map[string]any{"key": "b", "label": map[string]any{"en": "Set B", "zh": "B 组"}},
	}}
	for _, up := range m["userPermissions"].([]any) {
		p := up.(map[string]any)
		if p["key"] == "rules:read" || p["key"] == "stats:read" {
			p["scope"] = values
			p["defaults"] = map[string]any{"admin": []any{"*"}}
		}
	}
	for _, r := range m["routes"].([]any) {
		if rt := r.(map[string]any); rt["method"] == "GET" && rt["path"] == "/rules" {
			rt["scopeFrom"] = "query:set"
		}
	}
	title := map[string]any{"en": "Guard statistics", "zh": "拦截统计"}
	ui := m["ui"].(map[string]any)
	ui["pages"] = map[string]any{"stats": map[string]any{"type": "iframe", "title": title, "src": statsPage}}
	m["hostPermissions"] = append(m["hostPermissions"].([]any), map[string]any{"id": "ui.iframe"})
	ui["menus"] = []any{map[string]any{"id": "stats", "section": "security", "label": title, "icon": "shield", "page": "stats", "permission": "stats:read"}}
}

// statsPage is the iframe page of the stats:read menu.
const statsPage = "ui/iframe/stats.html"

func TestPluginScopeRouteE2E(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	pk := e.scopePlugin(admin)
	rulesRead := "plugin." + pk + ":rules:read"
	roleID, u := e.NewUserWithRoles(admin, e.RoleKey("scope"), []string{rulesRead + ":value/a"})
	if !u.Permissions(t)[rulesRead+":value/a"] {
		t.Fatalf("/me lacks %s:value/a: %s", rulesRead, u.Me(t).Get("permissions").Raw)
	}

	rules := func(set string) *Resp {
		if set == "" {
			return u.API(t, http.MethodGet, "/p/"+pk+"/rules", nil)
		}
		return u.API(t, http.MethodGet, "/p/"+pk+"/rules", nil, Query("set", set))
	}
	// A value in scope passes, one outside looks missing, none is refused.
	if r := rules("a"); r.Status != 200 {
		t.Fatalf("?set=a with value/a: %s", r)
	}
	if r := rules("b"); r.Status != 404 {
		t.Fatalf("?set=b with value/a: want 404, got %s", r)
	}
	if r := rules(""); r.Status != 403 || r.ErrCode() != "permission_denied" {
		t.Fatalf("no set with value/a: want 403 permission_denied, got %s", r)
	}

	// The unscoped key reaches every value.
	admin.OK(t, http.MethodPut, "/roles/"+itoa(roleID)+"/permissions", map[string]any{"permission_keys": []string{rulesRead}})
	for _, set := range []string{"a", "b", ""} {
		if r := rules(set); r.Status != 200 {
			t.Errorf("?set=%q with the unscoped key: %s", set, r)
		}
	}
}

func TestPluginScopeOptionsE2E(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	pk := e.scopePlugin(admin)
	rulesRead := "plugin." + pk + ":rules:read"
	_, reader := e.NewUserWithRoles(admin, e.RoleKey("scopeopt"), []string{"role:read", rulesRead + ":value/a"})
	_, outsider := e.NewUserWithRoles(admin, e.RoleKey("scopeno"), []string{rulesRead})

	options := func(s *Session) []string {
		return Strings(s.OK(t, http.MethodGet, "/plugins/"+pk+"/scope-options", nil, Query("permission", "rules:read")).Get("items.#.id"))
	}
	// The values come from the manifest, within the caller's own scope.
	if ids := options(admin); !slices.Equal(ids, []string{"a", "b"}) {
		t.Errorf("super admin options: %v", ids)
	}
	if ids := options(reader); !slices.Equal(ids, []string{"a"}) {
		t.Errorf("value/a holder options: %v", ids)
	}
	if r := outsider.API(t, http.MethodGet, "/plugins/"+pk+"/scope-options", nil, Query("permission", "rules:read")); r.Status != 403 {
		t.Errorf("without role:read or plugin:read: want 403, got %s", r)
	}
	if r := admin.API(t, http.MethodGet, "/plugins/"+pk+"/scope-options", nil, Query("permission", "rules:manage")); r.Status != 400 {
		t.Errorf("unscoped permission: want 400, got %s", r)
	}

	// An enum scope needs no running plugin; the grants of a disabled
	// plugin reach nothing, though.
	e.Disable(admin, pk)
	if ids := options(admin); !slices.Equal(ids, []string{"a", "b"}) {
		t.Errorf("super admin options while disabled: %v", ids)
	}
	if ids := options(reader); len(ids) != 0 {
		t.Errorf("value/a holder options while disabled: %v", ids)
	}
}

func TestPluginScopeMenuE2E(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	pk := e.scopePlugin(admin)
	hasMenu := func(s *Session) bool {
		_, ok := Find(s.Menus(t), "plugin_key", pk)
		return ok
	}
	// A menu shows for its permission at any scope.
	_, scoped := e.NewUserWithRoles(admin, e.RoleKey("scopemenu"), []string{"plugin." + pk + ":stats:read:value/a"})
	if !hasMenu(scoped) {
		t.Errorf("stats:read:value/a holder lacks the %s menu", pk)
	}
	_, other := e.NewUserWithRoles(admin, e.RoleKey("scopenomenu"), []string{"plugin." + pk + ":rules:read:value/a"})
	if hasMenu(other) {
		t.Errorf("user without stats:read sees the %s menu", pk)
	}
}

// The gateway asks gateway:use only: plugin scoped grants change nothing on
// the key path, with or without it.
func TestGatewayPathIgnoresPluginScopes(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	pk := e.scopePlugin(admin)
	tn := e.NewTenant(admin, TenantOpts{})
	scoped := e.RoleKey("gwscope")
	e.CreateRole(admin, scoped, scoped, []string{"plugin." + pk + ":rules:read:value/a", "plugin." + pk + ":stats:read:value/b"})
	empty := e.RoleKey("gwnone")
	e.CreateRole(admin, empty, empty, []string{})

	// outcome is the gateway's answer once the role change has reached it:
	// served (200) or not, as served says.
	outcome := func(roles []string, served bool) string {
		e.SetUserRoles(admin, tn.User.UserID, roles)
		var got *GatewayResult
		Eventually(t, 10*time.Second, 500*time.Millisecond, func() string {
			return fmt.Sprintf("roles %v: served=%v, got HTTP %d %s", roles, served, got.Status, got.Body)
		}, func() bool {
			got = e.Messages(tn.APIKey, MessagesBody(tn.Model, "plugin scopes "+e.RunID, false), nil)
			return (got.Status == 200) == served
		})
		return fmt.Sprintf("HTTP %d %s", got.Status, got.JSON().Get("error.type").String())
	}
	outcome([]string{"user"}, true)
	outcome([]string{"user", scoped}, true)
	if with, without := outcome([]string{scoped}, false), outcome([]string{empty}, false); with != without {
		t.Errorf("without gateway:use: plugin scopes give %s, no permissions %s", with, without)
	}
}
