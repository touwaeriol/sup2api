package e2e

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// AC 6: disabling a plugin keeps its accounts and unregisters its account
// types. The anthropic endpoints are built into the core, so they stay but
// answer 404 (no account in the group serves them) for a group with only
// anthropic accounts; re-enabling restores it. anthropic is a built-in
// plugin, so uninstalling it is refused.
func TestAC06_DisableEnableUninstall(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	tn := e.NewTenant(admin, TenantOpts{})
	e.MustMessages(tn.APIKey, MessagesBody(tn.Model, "before disable", false), nil)

	e.Disable(admin, "anthropic")

	// Accounts stay (orphaned, not deleted): the detail answers, the list
	// hides them unless ?orphaned=all.
	acct := admin.OK(t, http.MethodGet, fmt.Sprintf("/accounts/%d", tn.Accounts[0].ID), nil)
	if acct.Get("id").Int() != tn.Accounts[0].ID || !acct.Get("orphaned").Bool() {
		t.Fatalf("account gone after disable: %s", acct.Raw)
	}
	if _, ok := Find(admin.OK(t, http.MethodGet, "/accounts", nil, Query("group_id", fmt.Sprint(tn.GroupID))).Array(), "id", tn.Accounts[0].ID); ok {
		t.Fatal("orphaned account still listed by default")
	}
	if _, ok := Find(admin.OK(t, http.MethodGet, "/accounts", nil, Query("group_id", fmt.Sprint(tn.GroupID), "orphaned", "all")).Array(), "id", tn.Accounts[0].ID); !ok {
		t.Fatal("orphaned account missing from ?orphaned=all")
	}
	// The anthropic endpoints are built into the core (CONTRACTS 13) and
	// stay, but no account type in the group serves them any more: 404
	// not_found_error, which clients do not retry, in the anthropic error
	// format.
	for i := 0; i < 4; i++ {
		g := e.Messages(tn.APIKey, MessagesBody(tn.Model, "while disabled", false), nil)
		if j := g.JSON(); g.Status != 404 || j.Get("error.type").String() != "not_found_error" || j.Get("type").String() != "error" || j.Get("error.code").String() != "not_found" {
			t.Fatalf("gateway while disabled: HTTP %d %s (want 404 not_found_error)", g.Status, g.Body)
		}
	}
	// Account types disappear from the registry; the built-in platform stays
	// without them, and the group no longer serves it.
	if _, ok := Find(admin.OK(t, http.MethodGet, "/account-types", nil).Array(), "plugin_key", "anthropic"); ok {
		t.Error("anthropic account types still offered while disabled")
	}
	p, ok := Find(e.Platforms(admin), "id", "anthropic")
	if !ok || !p.Get("builtin").Bool() || len(p.Get("endpoints").Array()) == 0 {
		t.Fatalf("built-in anthropic platform missing while the plugin is disabled: %s", p.Raw)
	}
	if _, ok := PlatformAccountType(p, AnthropicPlugin, AnthropicAPIKey); ok {
		t.Errorf("anthropic/apikey still listed on the anthropic platform: %s", p.Raw)
	}
	if ids := e.GroupPlatforms(admin, tn.GroupID); slices.Contains(ids, "anthropic") {
		t.Errorf("group platforms while anthropic is disabled: %v", ids)
	}

	e.Enable(admin, "anthropic")
	e.MustMessages(tn.APIKey, MessagesBody(tn.Model, "after enable", true), nil)

	// Built-in: uninstall is refused even while disabled; data is untouched.
	e.Disable(admin, "anthropic")
	r := admin.API(t, http.MethodDelete, "/plugins/anthropic", nil, Query("purge", "false"))
	if r.Status != 403 || r.JSON().Get("error.details.reason").String() != "builtin" {
		t.Fatalf("uninstall of a built-in plugin: %s", r)
	}
	// While the plugin is disabled the account counts as orphaned (CONTRACTS
	// §15.9); after re-enabling it must be back, untouched by the refused
	// uninstall.
	e.Enable(admin, "anthropic")
	acct = admin.OK(t, http.MethodGet, fmt.Sprintf("/accounts/%d", tn.Accounts[0].ID), nil)
	if acct.Get("orphaned").Bool() {
		t.Errorf("account orphaned by a refused uninstall: %s", acct.Raw)
	}
	e.MustMessages(tn.APIKey, MessagesBody(tn.Model, "after re-enable", false), nil)
}

// AC 6 (permissions and routes): disabling a plugin greys out its
// permissions and takes its routes off every node; a role's grant of one of
// them outlives the disable but takes no effect meanwhile, and re-enabling
// restores both. dynweight is a built-in plugin with user permissions and
// admin routes; it is left enabled or disabled as it was found.
func TestAC06_DisableGreysOutPermissionsAndRoutes(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	const key, read = "dynweight", "plugin.dynweight:dynweight:read"
	d, _ := e.Plugin(admin, key)
	wasEnabled := d.Get("status").String() == "enabled"
	e.EnsurePlugin(admin, key, "")

	m, ok := e.PluginPermissions(admin, key)
	if !ok || m.Get("status").String() != "active" || m.Get("source").String() != "plugin" {
		t.Fatalf("plugin permission module before disable: %s", m.Raw)
	}
	// Permissions a former version no longer declares stay removed through
	// disable and enable; the others follow the plugin.
	removed := map[string]bool{}
	for _, p := range m.Get("permissions").Array() {
		removed[p.Get("key").String()] = p.Get("status").String() == "removed"
	}
	expectStatus := func(status string) {
		t.Helper()
		m, ok := e.PluginPermissions(admin, key)
		if !ok || m.Get("status").String() != status {
			t.Fatalf("plugin permission module, want %s: %s", status, m.Raw)
		}
		for _, p := range m.Get("permissions").Array() {
			want := status
			if removed[p.Get("key").String()] {
				want = "removed"
			}
			if got := p.Get("status").String(); got != want {
				t.Fatalf("permission %s is %s, want %s", p.Get("key").String(), got, want)
			}
		}
	}
	// The reader's grant is effective and the route serves it on every node.
	roleKey := e.RoleKey("dw")
	_, reader := e.NewUserWithRoles(admin, roleKey, []string{read})
	served := func(want bool) {
		t.Helper()
		if got := reader.Permissions(t)[read]; got != want {
			t.Fatalf("/me has %s: %v, want %v", read, got, want)
		}
		for n, base := range e.NodeURLs {
			if r := e.PluginRoute(reader, base, http.MethodGet, key, "/config", nil); want && r.Status != 200 || !want && r.Status < 400 {
				t.Fatalf("node-%d GET /p/%s/config (served %v): %s", n+1, key, want, r)
			}
		}
	}
	served(true)

	e.Disable(admin, key)
	expectStatus("disabled")
	if !e.RolePermissionKeys(admin, roleKey)[read] {
		t.Errorf("role %s lost %s on disable", roleKey, read)
	}
	served(false)

	e.Enable(admin, key)
	expectStatus("active")
	served(true)
	if !wasEnabled {
		e.Disable(admin, key)
	}
}

// AC 6 (plugin permissions): uninstalling a plugin deletes its permissions
// and every role's grants of them (D5); installing it again asks for them
// afresh, with nothing left for review.
func TestAC06_UninstallDropsPluginGrants(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	dp := e.NewDerivedPlugin(admin, "e2eun")
	pkg := dp.Package(e)
	e.InstallVersion(admin, pkg, nil)
	roleKey := e.RoleKey("un")
	e.CreateRole(admin, roleKey, roleKey, []string{"plugin." + dp.ID + ":rules:read"})

	e.Uninstall(admin, dp.ID, true)
	if m, ok := e.PluginPermissions(admin, dp.ID); ok {
		t.Errorf("plugin permissions left after uninstall: %s", m.Raw)
	}
	for k := range e.RolePermissionKeys(admin, roleKey) {
		if strings.HasPrefix(k, "plugin."+dp.ID+":") {
			t.Errorf("role %s kept %s after uninstall", roleKey, k)
		}
	}

	rev := e.UploadPackage(admin, pkg)
	for _, up := range rev.Get("user_permissions").Array() {
		if !up.Get("new").Bool() {
			t.Errorf("reinstall review: %s not new", up.Get("key").String())
		}
	}
	if d, _ := e.Plugin(admin, dp.ID); len(d.Get("role_grants_to_review").Array()) != 0 {
		t.Errorf("reinstall has role grants to review: %s", d.Get("role_grants_to_review").Raw)
	}
}
