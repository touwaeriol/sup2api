package api

import (
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/install"
)

// GET /market/plugins keeps data as the plugin array and reports the core
// version as a top-level host_version.
func TestMarketPluginsHostVersion(t *testing.T) {
	svc := install.New(install.Deps{}, install.Options{HostVersion: "0.3.1-dev"})
	a := New(Deps{Install: svc})
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	a.RegisterRoutes(httpapi.NewRouter(engine, tokens{}, authz{}))
	h := &harness{t: t, engine: engine}

	code, out := h.do("GET", "/market/plugins", "admin", nil)
	if code != 200 || out["host_version"] != "0.3.1-dev" {
		t.Fatalf("market = %d %v", code, out)
	}
	if list, ok := out["data"].([]any); !ok || len(list) != 0 {
		t.Fatalf("market data = %v", out["data"])
	}
}

func TestResourcesMessage(t *testing.T) {
	running := resourcesMessage(true, install.StatusEnabled)
	if !strings.Contains(running["zh"], "已通知各节点按新限制重启插件实例") || !strings.Contains(running["en"], "restart") {
		t.Fatalf("running = %v", running)
	}
	if m := resourcesMessage(true, install.StatusDisabled); !strings.Contains(m["en"], "enabled") || m["zh"] == "" {
		t.Fatalf("disabled = %v", m)
	}
	if m := resourcesMessage(false, install.StatusEnabled); !strings.Contains(m["en"], "next time") || m["zh"] == "" {
		t.Fatalf("not notified = %v", m)
	}
}

// A table's "New" link replaces a sidebar entry for its form page: callers
// without the link's permission see neither the link nor the form, while a
// visible menu to the form keeps it.
func TestVisibleMenusAndPagesCreateLink(t *testing.T) {
	ui := &manifest.UI{
		Menus: []manifest.Menu{{ID: "assets", Page: "assets", Permission: "assets:read"}},
		Pages: map[string]manifest.Page{
			"assets":    {Type: "table", Create: &manifest.PageLink{Page: "new_asset", Permission: "assets:manage"}},
			"new_asset": {Type: "form"},
			"about":     {Type: "form"},
		},
	}
	can := func(perms ...string) func(string) bool {
		return func(p string) bool {
			if p == "" {
				return true
			}
			for _, x := range perms {
				if x == p {
					return true
				}
			}
			return false
		}
	}
	menus, pages := visibleMenusAndPages(ui, can("assets:read", "assets:manage"), noCore)
	if len(menus) != 1 || pages["assets"].Create == nil || pages["assets"].Create.Page != "new_asset" || len(pages) != 3 {
		t.Fatalf("manager: menus=%v pages=%v", menus, pages)
	}
	_, pages = visibleMenusAndPages(ui, can("assets:read"), noCore)
	if _, ok := pages["new_asset"]; ok || pages["assets"].Create != nil || len(pages) != 2 {
		t.Fatalf("reader: pages=%v", pages)
	}
	if ui.Pages["assets"].Create == nil {
		t.Fatal("filtering must not change the manifest")
	}
	ui.Menus = append(ui.Menus, manifest.Menu{ID: "new", Page: "new_asset"})
	if _, pages = visibleMenusAndPages(ui, can("assets:read"), noCore); pages["new_asset"].Type != "form" || pages["assets"].Create != nil {
		t.Fatalf("a visible menu keeps the form, not the link: %v", pages)
	}
}

// Two tables open the same form under different permissions: the link the
// caller may use keeps the form, whichever table is denied.
func TestVisibleMenusAndPagesSharedCreateForm(t *testing.T) {
	ui := &manifest.UI{
		Pages: map[string]manifest.Page{
			"mine":     {Type: "table", Create: &manifest.PageLink{Page: "new_item", Permission: "items:own:create"}},
			"all":      {Type: "table", Create: &manifest.PageLink{Page: "new_item", Permission: "items:manage"}},
			"new_item": {Type: "form"},
		},
	}
	own := func(p string) bool { return p == "items:own:create" }
	_, pages := visibleMenusAndPages(ui, own, noCore)
	if _, ok := pages["new_item"]; !ok || pages["mine"].Create == nil || pages["all"].Create != nil {
		t.Fatalf("own creator: pages=%v", pages)
	}
	_, pages = visibleMenusAndPages(ui, func(string) bool { return false }, noCore)
	if _, ok := pages["new_item"]; ok || pages["mine"].Create != nil || pages["all"].Create != nil {
		t.Fatalf("no create permission: pages=%v", pages)
	}
}

// A table hidden by its menu's permission does not reveal the form its
// "New" link opens, even when the link itself needs no permission.
func TestVisibleMenusAndPagesHiddenTableHidesCreateForm(t *testing.T) {
	ui := &manifest.UI{
		Menus: []manifest.Menu{{ID: "a", Page: "items", Permission: "items:read"}},
		Pages: map[string]manifest.Page{
			"items":    {Type: "table", Create: &manifest.PageLink{Page: "new_item"}},
			"new_item": {Type: "form"},
		},
	}
	menus, pages := visibleMenusAndPages(ui, func(p string) bool { return p == "" }, noCore)
	if len(menus) != 0 || len(pages) != 0 {
		t.Fatalf("caller without items:read: menus=%v pages=%v", menus, pages)
	}
	_, pages = visibleMenusAndPages(ui, func(p string) bool { return p == "" || p == "items:read" }, noCore)
	if _, ok := pages["new_item"]; !ok || pages["items"].Create == nil {
		t.Fatalf("reader: pages=%v", pages)
	}
}

// noCore is a caller holding no core permission.
func noCore(string) bool { return false }

// A page's nav keeps only the sections the caller may see: a plugin
// permission, a core one (either suffices) or none. A group left empty is
// dropped, and a nav with nothing left goes away; the manifest is untouched.
func TestVisibleMenusAndPagesNav(t *testing.T) {
	ui := &manifest.UI{Pages: map[string]manifest.Page{
		"panel": {Type: "native", Nav: &manifest.PageNav{Groups: []manifest.NavGroup{
			{Key: "rules", Items: []manifest.NavItem{
				{Key: "words", Permission: "rules:manage"},
				{Key: "models", Permission: "rules:manage", CorePermission: "settings:manage"},
				{Key: "about"},
			}},
			{Key: "logs", Items: []manifest.NavItem{{Key: "hits", Permission: "logs:read"}}},
		}}},
	}}
	keys := func(nav *manifest.PageNav) string {
		if nav == nil {
			return "<nil>"
		}
		var parts []string
		for _, g := range nav.Groups {
			var items []string
			for _, it := range g.Items {
				items = append(items, it.Key)
			}
			parts = append(parts, g.Key+":"+strings.Join(items, ","))
		}
		return strings.Join(parts, " ")
	}
	is := func(want ...string) func(string) bool {
		return func(p string) bool { return p == "" || slices.Contains(want, p) }
	}
	cases := []struct {
		name          string
		allowed, core func(string) bool
		want          string
	}{
		{"everything", is("rules:manage", "logs:read"), noCore, "rules:words,models,about logs:hits"},
		{"core permission alone", is(), is("settings:manage"), "rules:models,about"},
		{"empty group dropped", is(), noCore, "rules:about"},
	}
	for _, tc := range cases {
		_, pages := visibleMenusAndPages(ui, tc.allowed, tc.core)
		if got := keys(pages["panel"].Nav); got != tc.want {
			t.Errorf("%s: nav = %s, want %s", tc.name, got, tc.want)
		}
	}
	if got := keys(ui.Pages["panel"].Nav); got != "rules:words,models,about logs:hits" {
		t.Fatalf("filtering must not change the manifest: %s", got)
	}

	// With every item hidden the nav goes away rather than leaving an
	// empty sidebar.
	ui.Pages["panel"].Nav.Groups[0].Items = ui.Pages["panel"].Nav.Groups[0].Items[:2]
	if _, pages := visibleMenusAndPages(ui, is(), noCore); pages["panel"].Nav != nil {
		t.Fatalf("nothing visible: nav = %s", keys(pages["panel"].Nav))
	}
}
