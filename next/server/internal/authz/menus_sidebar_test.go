package authz

import (
	"context"
	"slices"
	"testing"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// sidebarPlugins are guard, with two menus in the shared plugins section,
// and mod, with its own section (order 350: between security and finance)
// and items joining core sections.
var sidebarPlugins = []core.PluginInfo{{
	Key: "guard",
	Manifest: &manifest.Manifest{UI: &manifest.UI{Menus: []manifest.Menu{
		{ID: "dash", Section: "plugins", Label: manifest.LocalizedText{"en": "Guard", "zh": "请求守卫"}, Page: "dashboard", Permission: "stats:read", Order: 2},
		{ID: "public", Section: "plugins", Label: manifest.LocalizedText{"en": "Info"}, Page: "info", Order: 1},
	}}},
}, {
	Key: "mod",
	Manifest: &manifest.Manifest{UI: &manifest.UI{
		Sections: []manifest.MenuSection{{ID: "safety", Label: manifest.LocalizedText{"en": "Safety", "zh": "安全"}, Order: 350}},
		Menus: []manifest.Menu{
			{ID: "dash", Section: "safety", Label: manifest.LocalizedText{"en": "Moderation"}, Page: "dashboard", Permission: "mod:read"},
			{ID: "rules", Section: "gateway", Label: manifest.LocalizedText{"en": "Mod rules"}, Page: "rules", Permission: "mod:read"},
			{ID: "audit", Section: "security", Label: manifest.LocalizedText{"en": "Mod audit"}, Page: "audit", Permission: "mod:read"},
			{ID: "weights", Section: "system", Label: manifest.LocalizedText{"en": "Mod weights"}, Page: "weights", Permission: "mod:read"},
		}}},
}}

// sidebarFixture is the authz service with the sidebarPlugins installed,
// their permissions given to the admin role, and an admin.
type sidebarFixture struct {
	t     *testing.T
	s     *Service
	reg   *fakeRegistry
	admin int64
}

func newSidebarFixture(t *testing.T) sidebarFixture {
	t.Helper()
	db := testutil.DB(t)
	ctx := context.Background()
	// The registry is read on every call: a test can take the generation away.
	f := sidebarFixture{t: t, reg: &fakeRegistry{gen: fakeGen{plugins: sidebarPlugins}}}
	f.s = newService(t, db, newMemBus(), f.reg)
	if _, err := db.SQL.ExecContext(ctx, `INSERT INTO plugins (key, name, status) VALUES ('guard', '{"en":"Guard"}', 'enabled'), ('mod', '{"en":"Mod"}', 'enabled')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Tx(ctx, func(tx *gorm.DB) error {
		if err := f.s.SyncPlugin(ctx, tx, "guard", withRoles([]core.PermissionDef{{Key: "stats:read"}}, RoleAdmin)); err != nil {
			return err
		}
		return f.s.SyncPlugin(ctx, tx, "mod", withRoles([]core.PermissionDef{{Key: "mod:read"}}, RoleAdmin))
	}); err != nil {
		t.Fatal(err)
	}
	f.admin = addUser(t, f.s, "a@x.com", RoleAdmin)
	eventually(t, func() bool { ok, _ := f.s.Can(ctx, f.admin, "plugin.guard:stats:read"); return ok })
	return f
}

// menus is the sidebar of the user, checked against GET /ui/plugins (sameAsUI).
func (f sidebarFixture) menus(userID int64) []MenuSection {
	f.t.Helper()
	menus, err := f.s.Menus(context.Background(), userID)
	if err != nil {
		f.t.Fatal(err)
	}
	f.sameAsUI(userID, menus)
	return menus
}

// sameAsUI fails unless the sidebar shows exactly the plugin menus
// manifest.FilterUI leaves, the rule GET /ui/plugins answers by, with the
// same permission check.
func (f sidebarFixture) sameAsUI(userID int64, menus []MenuSection) {
	f.t.Helper()
	set, err := f.s.PermissionSet(context.Background(), userID)
	if err != nil {
		f.t.Fatal(err)
	}
	var want, got []string
	for _, p := range sidebarPlugins {
		allowed := func(perm string) bool { return perm == "" || set.Has(manifest.PermissionKey(p.Key, perm)) }
		for _, m := range manifest.FilterUI(p.Manifest.UI, allowed, set.Has).Menus {
			want = append(want, p.Key+":"+m.ID)
		}
	}
	for _, sec := range menus {
		for _, it := range sec.Items {
			if it.PluginKey != "" {
				got = append(got, it.ID)
			}
		}
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		f.t.Fatalf("user %d: sidebar plugin menus %v, FilterUI menus %v", userID, got, want)
	}
}

// role adds a user with a new role of the permissions.
func (f sidebarFixture) role(key string, perms ...string) int64 {
	f.t.Helper()
	role, err := f.s.CreateRole(context.Background(), f.admin, CreateRoleInput{Key: key, Name: core.LocalizedText{"en": key}, PermissionKeys: perms})
	if err != nil {
		f.t.Fatal(err)
	}
	return addUser(f.t, f.s, key+"@x.com", role.Key)
}

// sectionsOf lists the sections of the menus and files them by key.
func sectionsOf(menus []MenuSection) (string, map[string]MenuSection) {
	keys := make([]string, len(menus))
	byKey := map[string]MenuSection{}
	for i, m := range menus {
		keys[i], byKey[m.Section] = m.Section, m
	}
	return join(keys), byKey
}

func TestMenusOfUser(t *testing.T) {
	t.Parallel()
	f := newSidebarFixture(t)
	menus := f.menus(addUser(t, f.s, "u@x.com", RoleUser))
	// The server orders plugins (600) before personal (800); the console
	// shows overview and personal first and the other sections after them
	// (CONTRACTS §90).
	if got, _ := sectionsOf(menus); got != "overview,plugins,me" {
		t.Fatalf("user sections = %s", got)
	}
	// The console of a user: its own keys, models and usage, no site-wide usage.
	if got := menuPaths(menus[0]); got != "/dashboard,/me/api-keys,/me/models,/me/usage/overview,/me/usage" {
		t.Fatalf("user console = %s", got)
	}
	if got := menuPaths(menus[2]); got != "/me/wallet,/me/topup-orders,/me/profile" {
		t.Fatalf("user personal = %s", got)
	}
	if p := menus[1].Items; len(p) != 1 || p[0].Path != "/p/guard/info" || p[0].PluginKey != "guard" {
		t.Fatalf("user plugin menus = %+v", p)
	}
}

func TestMenusOfAdmin(t *testing.T) {
	t.Parallel()
	f := newSidebarFixture(t)
	sections, byKey := sectionsOf(f.menus(f.admin))
	// Finance holds the site-wide balance ledger (balance:all:read).
	if sections != "overview,gateway,security,mod:safety,finance,topup,users,plugins,system,me" {
		t.Fatalf("admin sections = %s", sections)
	}
	for section, want := range map[string]string{
		// Plugin management first, then the plugin menus by order.
		"plugins":    "/plugins,/market,/publishers,/p/guard/info,/p/guard/dashboard",
		"mod:safety": "/p/mod/dashboard",
		"security":   "/p/mod/audit",
		// Site-wide usage sits in the console beside the caller's own.
		"overview": "/dashboard,/me/api-keys,/me/models,/me/usage/overview,/me/usage,/usage/overview,/usage",
		// Accounts, groups and proxies (§132); no model prices: the settings view lists them.
		"gateway": "/accounts,/groups,/proxies,/p/mod/rules",
		"users":   "/users,/segments,/roles",
		// Operations pages, then system settings as the last core item; a
		// plugin that still declares section "system" comes after it.
		"system": "/nodes,/system/tasks,/system/upgrades,/system/audit,/settings,/p/mod/weights",
	} {
		if got := menuPaths(byKey[section]); got != want {
			t.Fatalf("admin %s = %s", section, got)
		}
	}
}

// The labels of plugin menus and sections are theirs; core sections keep their own.
func TestMenuLabels(t *testing.T) {
	t.Parallel()
	f := newSidebarFixture(t)
	_, byKey := sectionsOf(f.menus(f.admin))
	for _, tc := range []struct{ name, got, want string }{
		{"plugin menu", byKey["plugins"].Items[4].Label.Get("zh"), "请求守卫"},
		{"plugin section", byKey["mod:safety"].Label.Get("zh"), "安全"},
		{"security", byKey["security"].Label.Get("zh"), "安全"},
		{"users", byKey["users"].Label.Get("zh"), "用户"},
		{"system", byKey["system"].Label.Get("zh"), "系统"},
	} {
		if tc.got != tc.want {
			t.Fatalf("%s label = %s", tc.name, tc.got)
		}
	}
}

// The menus follow the list endpoints and the settings pages a role holds.
func TestMenusFollowPermissions(t *testing.T) {
	t.Parallel()
	f := newSidebarFixture(t)
	for _, tc := range []struct {
		name, section, want string
		perms               []string
	}{
		// Own accounts with proxies in the gateway section, nothing of system.
		{"vendor", "gateway", "/accounts,/proxies", []string{"account:read:own", "proxy:read:own", "proxy:manage:own"}},
		{"vendor_system", "system", "", []string{"account:read:own", "proxy:read:own", "proxy:manage:own"}},
		// The create keys alone, which GET /accounts and GET /proxies refuse, open neither page.
		{"creator", "gateway", "", []string{"account:create:own", "proxy:manage:own"}},
		// One page of the settings view alone opens its entry and nothing
		// else of system: model prices are the operator role's without any
		// settings permission.
		{"sticky_read", "system", "/settings", []string{"sticky:read"}},
		{"price_read", "system", "/settings", []string{"price:read"}},
		// group:read alone opens groups beside the accounts, not system settings (§132).
		{"group_viewer", "gateway", "/groups", []string{"group:read"}},
		{"group_viewer_system", "system", "", []string{"group:read"}},
		{"bookkeeper", "finance", "/ledger", []string{"balance:all:read"}},
		// Top-up: the orders, redeem and promo codes in their own section;
		// the top-up settings are a page of system settings.
		{"cashier", "topup", "/topup/orders,/redeem-codes,/promo-codes", []string{"topup:order:read", "topup:manage", "redeem:manage"}},
		{"cashier_system", "system", "/settings", []string{"topup:manage"}},
		{"buyer", "me", "/me/topup-orders,/me/profile", []string{"topup:use"}},
	} {
		if got := sectionPaths(t, f.s, f.role(tc.name, tc.perms...), tc.section); got != tc.want {
			t.Fatalf("%s %s = %s", tc.name, tc.section, got)
		}
	}
	if got := sectionPaths(t, f.s, addUser(t, f.s, "u@x.com", RoleUser), "finance"); got != "" {
		t.Fatalf("user finance = %s", got)
	}
}

// Without a generation there are no plugin menus.
func TestMenusWithoutGeneration(t *testing.T) {
	t.Parallel()
	f := newSidebarFixture(t)
	f.reg.gen = nil
	if got := sectionPaths(t, f.s, f.admin, "gateway"); got != "/accounts,/groups,/proxies" {
		t.Fatalf("gateway without a generation = %s", got)
	}
}
