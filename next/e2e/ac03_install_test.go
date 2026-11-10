package e2e

import (
	"net/http"
	"testing"
)

// AC 3: anthropic is a built-in plugin: the core installs and enables it at
// startup (signed as official, every host permission granted), it is active
// on both nodes, it cannot be uninstalled, the "model catalog" menu appears
// and its data comes from the plugin schema. The market still lists it.
func TestAC03_InstallAnthropicFromMarket(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()

	// Market lists anthropic with the index metadata.
	src := e.MarketSource(admin)
	list := admin.OK(t, http.MethodGet, "/market/plugins", nil, Query("source_id", itoa(src))).Array()
	p, ok := Find(list, "key", "anthropic")
	if !ok {
		t.Fatalf("anthropic not in market listing: %v", list)
	}
	if p.Get("publisher").String() != "sub2api" || len(p.Get("versions").Array()) == 0 {
		t.Fatalf("market entry: %s", p.Raw)
	}

	// Installed and enabled by the core, without any operator action.
	d := e.WaitPlugin(admin, "anthropic", "enabled", "")
	if !d.Get("builtin").Bool() {
		t.Fatalf("anthropic is not marked builtin: %s", d.Raw)
	}
	if st := PluginNodeStates(d); len(st) != 2 {
		t.Fatalf("expected 2 node states, got %v", st)
	}
	version := d.Get("active_version").String()
	rev := admin.OK(t, http.MethodGet, "/plugins/anthropic/versions/"+version+"/review", nil)
	if rev.Get("trust").String() != "official" || rev.Get("signature_status").String() != "valid" {
		t.Fatalf("review trust: %s", rev.Raw)
	}
	if rev.Get("database.schema").String() != "plg_anthropic" {
		t.Fatalf("review database: %s", rev.Raw)
	}
	// The anthropic platform and its endpoints are built into the core
	// (CONTRACTS 13): the plugin declares no platform and no endpoint.
	if n := len(rev.Get("platforms").Array()); n != 0 || rev.Get("platform").Exists() {
		t.Fatalf("review: anthropic must declare no platforms: %s", rev.Raw)
	}
	if n := len(rev.Get("gateway_endpoints").Array()); n != 0 {
		t.Fatalf("review: anthropic must declare no gateway endpoints: %s", rev.Get("gateway_endpoints").Raw)
	}
	// Account types are top-level in the review and list the platforms they
	// serve.
	rat, ok := Find(rev.Get("account_types").Array(), "id", AnthropicAPIKey)
	if !ok {
		t.Fatalf("review lacks account type apikey: %s", rev.Get("account_types").Raw)
	}
	if ids := PlatformIDs(rat.Get("platforms")); len(ids) != 1 || ids[0] != "anthropic" || rat.Get("form_mode").String() != "schema" {
		t.Fatalf("review account type apikey: %s", rat.Raw)
	}
	if rat.Get("protocols").Exists() {
		t.Fatalf("review account type still lists protocols: %s", rat.Raw)
	}
	grants := admin.OK(t, http.MethodGet, "/plugins/anthropic/grants", nil).Array()
	if g, ok := Find(grants, "permission", "accounts.credentials"); !ok || g.Get("status").String() != "granted" {
		t.Fatalf("grant accounts.credentials: %v", grants)
	}
	if _, ok := Find(grants, "permission", "gateway.endpoint"); ok {
		t.Fatalf("anthropic no longer asks for gateway.endpoint: %v", grants)
	}

	// Built-in: uninstall is refused, the plugin stays.
	r := admin.API(t, http.MethodDelete, "/plugins/anthropic", nil, Query("purge", "true"))
	if r.Status != 403 || r.JSON().Get("error.details.reason").String() != "builtin" {
		t.Fatalf("uninstall of a built-in plugin: %s", r)
	}
	e.WaitPlugin(admin, "anthropic", "enabled", version)

	// The plugin declares no console UI: no menu of its own, and it is not
	// among the plugins the console loads UI for.
	for _, it := range admin.Menus(t) {
		if it.Get("plugin_key").String() == "anthropic" {
			t.Fatalf("anthropic menu in /me/menus: %s", it.Raw)
		}
	}
	ui := admin.OK(t, http.MethodGet, "/ui/plugins", nil).Array()
	if _, ok := Find(ui, "key", "anthropic"); ok {
		t.Fatalf("anthropic in /ui/plugins: %v", ui)
	}

	// Data served by the plugin from plg_anthropic.model_catalog, on every node.
	e.OnEachNode(admin, func(n int, c *Client) {
		r := c.API(t, http.MethodGet, "/p/anthropic/models", nil)
		if r.Status != 200 || len(r.Data().Array()) == 0 {
			t.Fatalf("node-%d /p/anthropic/models: %s", n, r)
		}
	})

	// Account type is offered by the registry with the platform it serves
	// (built in) and that platform's endpoints.
	types := admin.OK(t, http.MethodGet, "/account-types", nil).Array()
	at, ok := FindAccountType(types, AnthropicPlugin, AnthropicAPIKey)
	if !ok {
		t.Fatalf("account types: %v", types)
	}
	if p, ok := Find(at.Get("platforms").Array(), "id", "anthropic"); !ok || !p.Get("builtin").Bool() || !p.Get("available").Bool() {
		t.Fatalf("anthropic/apikey platforms: %s", at.Get("platforms").Raw)
	}
	if at.Get("protocols").Exists() {
		t.Fatalf("anthropic/apikey still lists protocols: %s", at.Raw)
	}
	for path, proto := range map[string]string{"/v1/messages": "anthropic.messages", "/v1/messages/count_tokens": "anthropic.count_tokens"} {
		ep, ok := AccountTypeEndpoint(at, http.MethodPost, path)
		if !ok || !ep.Get("native").Bool() || ep.Get("protocol").String() != proto || ep.Get("platform").String() != "anthropic" {
			t.Fatalf("anthropic/apikey endpoint %s: %s", path, at.Get("endpoints").Raw)
		}
	}
}
