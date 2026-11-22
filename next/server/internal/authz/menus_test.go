package authz

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// testMenuRoutes stands in for the route table (httpapi.RouteKeys) the app
// binds the menus to; app's start-up check binds the real one.
func testMenuRoutes(id string) ([]string, bool) {
	keys, ok := map[string][]string{
		"GET /me/api-keys": {"apikey:self:manage"}, "GET /me/prices": {"apikey:self:manage"},
		"GET /me/usage/overview": {"usage:self:read"}, "GET /me/usage": {"usage:self:read"},
		"GET /usage/overview": {"usage:all:read"}, "GET /usage": {"usage:all:read"},
		"GET /account-usage/overview": {"usage:account:read"}, "GET /account-usage": {"usage:account:read"},
		"GET /accounts": {"account:read"}, "GET /proxies": {"proxy:read"}, "GET /ledger": {"balance:all:read"},
		"GET /users": {"user:read"}, "GET /segments": {"segment:read"}, "GET /roles": {"role:read"},
		"GET /plugins": {"plugin:read"}, "GET /market/plugins": {"plugin:market:read"}, "GET /publishers": {"publisher:read"},
		"GET /nodes": {"node:read"}, "GET /system/tasks": {"task:read"}, "GET /system/upgrades": {"system:update:read"},
		"GET /audit-logs": {"audit:read"}, "GET /settings/gateway": {"settings:read"}, "GET /sticky-rules": {"sticky:read"},
		"GET /groups": {"group:read"}, "GET /prices": {"price:read"}, "GET /me/balance": {"balance:self:read"},
	}[id]
	return keys, ok
}

// A menu item shows to the holders of its main routes' keys; before the
// routes are bound it stays hidden, and binding fails on a missing route.
func TestBindMenuRoutes(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	s := newService(t, db, newMemBus(), nil)
	operator := addUser(t, s, "op@x.com", RoleOperator)
	if got := sectionPaths(t, s, operator, "gateway"); got != "/accounts,/groups,/proxies" {
		t.Fatalf("gateway = %s", got)
	}

	// A route keyed differently moves the menu with it.
	err := s.BindMenuRoutes(func(id string) ([]string, bool) {
		if id == "GET /proxies" {
			return []string{"settings:manage"}, true
		}
		return testMenuRoutes(id)
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := sectionPaths(t, s, operator, "gateway"); got != "/accounts,/groups" {
		t.Fatalf("gateway with GET /proxies under settings:manage = %s", got)
	}

	err = s.BindMenuRoutes(func(id string) ([]string, bool) {
		if id == "GET /accounts" {
			return nil, false
		}
		return testMenuRoutes(id)
	})
	if err == nil || !strings.Contains(err.Error(), "menu accounts: no route GET /accounts") {
		t.Fatalf("missing route: %v", err)
	}

	unbound := New(Deps{DB: db, Bus: newMemBus()})
	menus, err := unbound.Menus(context.Background(), operator)
	if err != nil {
		t.Fatal(err)
	}
	for _, sec := range menus {
		for _, it := range sec.Items {
			if it.ID != "dashboard" && it.ID != "my-profile" {
				t.Fatalf("unbound menus show %s", it.ID)
			}
		}
	}
}

// A plugin menu naming a scoped plugin permission shows to a holder of any
// of its scopes, and not to a holder of none.
func TestPluginMenusScopedPermission(t *testing.T) {
	t.Parallel()
	reg := fakeRegistry{gen: fakeGen{plugins: []core.PluginInfo{{Key: "mod", Manifest: &manifest.Manifest{UI: &manifest.UI{
		Menus: []manifest.Menu{{ID: "flows", Label: manifest.LocalizedText{"en": "Flows"}, Page: "flows", Permission: "flow:manage"}},
	}}}}}}
	s := &Service{plugins: reg}
	ids := func(set core.PermissionSet) []string {
		var out []string
		for _, m := range s.pluginMenus(set) {
			out = append(out, m.ID)
		}
		return out
	}
	assert.Equal(t, []string{"mod:flows"}, ids(core.PermissionSetOf("plugin.mod:flow:manage:own")))
	assert.Equal(t, []string{"mod:flows"}, ids(core.PermissionSetOf("plugin.mod:flow:manage:flow/3")))
	assert.Empty(t, ids(core.PermissionSetOf("plugin.mod:flow:read:own")))
}

// The account-side usage pages show to holders of usage:account:read at any
// scope and hide behind the site-wide ones (hideWith usage:all:read); the
// users page shows to a team holder of user:read (CONTRACTS §103.5, §22).
func TestAccountUsageMenus(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	s := newService(t, db, newMemBus(), nil)
	ctx := context.Background()
	const site, own, account = "/usage/overview,/usage", "/dashboard,/me/api-keys,/me/models,/me/usage/overview,/me/usage", "/account-usage/overview,/account-usage"
	supplier := addUser(t, s, "s@x.com", RoleUser, RoleSupplier)
	agent := addUser(t, s, "a@x.com", RoleUser, RoleSupplierAgent)
	admin := addUser(t, s, "admin@x.com", RoleAdmin)
	assert.Equal(t, own+","+account, sectionPaths(t, s, supplier, "overview"))
	assert.Equal(t, own+","+account, sectionPaths(t, s, agent, "overview"))
	assert.Equal(t, "/users,/roles", sectionPaths(t, s, agent, "users"))
	assert.Equal(t, "", sectionPaths(t, s, supplier, "users"))
	assert.Equal(t, own+","+site, sectionPaths(t, s, admin, "overview"))

	_, err := s.CreateRole(ctx, admin, CreateRoleInput{Key: "both", Name: lt("b", "b"), PermissionKeys: []string{"usage:account:read:own", "usage:all:read"}})
	if err != nil {
		t.Fatal(err)
	}
	both := addUser(t, s, "b@x.com", "both")
	assert.Equal(t, "/dashboard,"+site, sectionPaths(t, s, both, "overview"))
}
