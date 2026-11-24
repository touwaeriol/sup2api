package e2e

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

// AC 2: the super admin creates a role "ops" with only account and proxy
// permissions and assigns it to a new user; the user sees only the matching
// menus and gets 403 on everything else.
func TestAC02_CustomRoleMenusAndForbidden(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()

	// The super admin sees everything.
	me := admin.Me(t)
	if !me.Get("superuser").Bool() {
		t.Fatalf("bootstrap admin is not superuser: %s", me.Raw)
	}

	perms := []string{"account:read", "account:create", "account:update", "proxy:read", "proxy:manage"}
	roleKey := strings.ReplaceAll(e.Name("ops"), "-", "_")
	e.CreateRole(admin, roleKey, "运营 ops", perms)

	// The role shows up with exactly these permissions.
	roles := admin.OK(t, http.MethodGet, "/roles", nil).Array()
	if _, ok := Find(roles, "key", roleKey); !ok {
		t.Fatalf("role %s not listed", roleKey)
	}

	ops := e.CreateUser(admin, UserSpec{Roles: []string{roleKey}})
	m := ops.Me(t)
	got := map[string]bool{}
	for _, p := range m.Get("permissions").Array() {
		got[p.String()] = true
	}
	for _, p := range perms {
		if !got[p] {
			t.Errorf("/me lacks %s", p)
		}
	}
	// The built-in "user" role may be added by default (CONTRACTS §4), and
	// every user holds the self: permissions.
	userRole := map[string]bool{"apikey:self:manage": true, "balance:self:read": true, "usage:self:read": true, "gateway:use": true}
	for p := range got {
		if !strings.HasPrefix(p, "account:") && !strings.HasPrefix(p, "proxy:") && !strings.HasPrefix(p, "self:") && !userRole[p] {
			t.Errorf("/me has unexpected permission %s", p)
		}
	}
	if m.Get("superuser").Bool() {
		t.Fatal("ops user must not be superuser")
	}

	// Menus: account and proxy entries present; no admin areas outside the role
	// (self-service entries of the built-in "user" role are tolerated).
	items := ops.Menus(t)
	var hasAccount, hasProxy bool
	for _, it := range items {
		s := strings.ToLower(it.Get("id").String() + " " + it.Get("path").String())
		hasAccount = hasAccount || strings.Contains(s, "account")
		hasProxy = hasProxy || strings.Contains(s, "prox")
		for _, bad := range []string{"user", "role", "group", "price", "plugin", "market", "node", "sticky", "setting", "publisher"} {
			if strings.Contains(s, bad) && !strings.Contains(s, "/me") {
				t.Errorf("ops user sees admin menu %q: %s", bad, it.Raw)
			}
		}
	}
	if !hasAccount || !hasProxy {
		t.Errorf("ops menus lack accounts/proxies: account=%v proxy=%v", hasAccount, hasProxy)
	}

	// Allowed.
	ops.OK(t, http.MethodGet, "/accounts", nil)
	ops.OK(t, http.MethodGet, "/proxies", nil)
	ops.OK(t, http.MethodGet, "/account-types", nil)

	// Forbidden: 403 permission_denied.
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/users"},
		{http.MethodGet, "/roles"},
		{http.MethodGet, "/groups"},
		{http.MethodGet, "/prices"},
		{http.MethodGet, "/plugins"},
		{http.MethodGet, "/usage"},
		{http.MethodGet, "/ledger"},
		{http.MethodGet, "/nodes"},
		{http.MethodPost, "/users"},
		{http.MethodDelete, "/accounts/1"},
		{http.MethodPost, "/accounts/1/credentials/reveal"},
	} {
		r := ops.API(t, c.method, c.path, map[string]any{})
		if r.Status != 403 || r.ErrCode() != "permission_denied" {
			t.Errorf("%s %s: want 403 permission_denied, got %s", c.method, c.path, r)
		}
	}

	// Revoking a permission takes effect immediately (authz:changed).
	roleID := func() int64 {
		r, _ := Find(admin.OK(t, http.MethodGet, "/roles", nil).Array(), "key", roleKey)
		return r.Get("id").Int()
	}()
	admin.OK(t, http.MethodPut, "/roles/"+itoa(roleID)+"/permissions",
		map[string]any{"permission_keys": []string{"account:read"}})
	e.OnEachNode(ops, func(n int, c *Client) {
		if r := c.API(t, http.MethodGet, "/proxies", nil); r.Status != 403 {
			t.Errorf("node-%d: proxy:read still granted after revoke: %s", n, r)
		}
	})

	// Unauthenticated.
	if r := NewClient(e.BaseURL).API(t, http.MethodGet, "/me", nil); r.Status != 401 || r.ErrCode() != "unauthenticated" {
		t.Errorf("anonymous /me: %s", r)
	}
	// role:define needs no password confirmation (CONTRACTS §3.3).
	if r := admin.API(t, http.MethodPut, "/roles/"+itoa(roleID)+"/permissions",
		map[string]any{"permission_keys": perms}); r.Status != 200 {
		t.Errorf("role:define without confirmation: %s", r)
	}
}

// AC 2 (plugin permissions): the per-plugin role matrix grants a plugin's
// permissions to a custom role; an upgrade that no longer declares one keeps
// the grant without effect (removed), and a later version declaring it again
// brings the grant back as it was, without applying the defaults again.
func TestAC02_PluginRoleMatrixRemovedAndRevived(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	dp := e.NewDerivedPlugin(admin, "e2emx")
	read, manage := "plugin."+dp.ID+":rules:read", "plugin."+dp.ID+":rules:manage"
	version := func(v string) func(map[string]any) { return func(m map[string]any) { m["version"] = v } }

	// rules:manage defaults to admin; the consent grants it to nobody.
	e.InstallVersion(admin, dp.Package(e, version("1.0.0")), map[string]any{"rules:manage": map[string]any{}})
	roleKey := e.RoleKey("mx")
	_, u := e.NewUserWithRoles(admin, roleKey, []string{})
	matrix := admin.OK(t, http.MethodPut, "/permissions/plugins/"+dp.ID,
		map[string]any{"roles": map[string]any{roleKey: []string{read, manage}}})
	rowKeys := func(m gjson.Result, role string) []string {
		row, ok := Find(m.Get("roles").Array(), "key", role)
		if !ok {
			t.Fatalf("matrix lacks role %s: %s", role, m.Raw)
		}
		keys := Strings(row.Get("permission_keys"))
		slices.Sort(keys)
		return keys
	}
	if got := rowKeys(matrix, roleKey); !slices.Equal(got, []string{manage, read}) {
		t.Fatalf("matrix row after PUT: %v", got)
	}
	if p := u.Permissions(t); !p[read] || !p[manage] {
		t.Fatalf("/me after the matrix PUT: %v", p)
	}

	// 1.0.1 drops rules:manage (and PUT /rules, which needs it).
	rev := e.InstallVersion(admin, dp.Package(e, version("1.0.1"), func(m map[string]any) {
		m["userPermissions"] = slices.DeleteFunc(m["userPermissions"].([]any), func(p any) bool { return p.(map[string]any)["key"] == "rules:manage" })
		m["routes"] = slices.DeleteFunc(m["routes"].([]any), func(r any) bool { return r.(map[string]any)["permission"] == "rules:manage" })
	}), nil)
	if gone, ok := Find(rev.Get("removed_user_permissions").Array(), "key", "rules:manage"); !ok || !slices.Contains(Strings(gone.Get("holders")), roleKey) {
		t.Errorf("1.0.1 review removed_user_permissions: %s", rev.Get("removed_user_permissions").Raw)
	}
	mod, _ := e.PluginPermissions(admin, dp.ID)
	if p, _ := Find(mod.Get("permissions").Array(), "key", manage); p.Get("status").String() != "removed" {
		t.Errorf("rules:manage after 1.0.1: %s", p.Raw)
	}
	if p := u.Permissions(t); !p[read] || p[manage] {
		t.Errorf("/me after 1.0.1: want %s without %s, got %v", read, manage, p)
	}
	matrix = admin.OK(t, http.MethodGet, "/permissions/plugins/"+dp.ID, nil)
	if got := rowKeys(matrix, roleKey); !slices.Equal(got, []string{manage, read}) {
		t.Errorf("removed grant not kept: %v", got)
	}

	// 1.0.2 declares it again: the role holds it as before, admin still not.
	rev = e.InstallVersion(admin, dp.Package(e, version("1.0.2")), nil)
	up, _ := Find(rev.Get("user_permissions").Array(), "key", "rules:manage")
	if !up.Get("revived").Bool() || up.Get("new").Bool() ||
		!slices.Equal(Strings(up.Get("cells."+roleKey+".grant")), []string{""}) || len(up.Get("cells.admin.grant").Array()) != 0 {
		t.Errorf("1.0.2 review of rules:manage: %s", up.Raw)
	}
	if p := u.Permissions(t); !p[read] || !p[manage] {
		t.Errorf("/me after 1.0.2: %v", p)
	}
	matrix = admin.OK(t, http.MethodGet, "/permissions/plugins/"+dp.ID, nil)
	if got := rowKeys(matrix, "admin"); slices.Contains(got, manage) {
		t.Errorf("revived rules:manage defaulted to admin again: %v", got)
	}
}
