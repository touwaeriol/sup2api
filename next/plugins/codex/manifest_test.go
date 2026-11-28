package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/plugins/codex/internal/codex"
	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

// TestManifestPassesCoreChecks runs manifest.json through the checks the
// server runs before it installs a plugin (Tooling: the runtime binaries and
// the native UI entry are produced by the build).
func TestManifestPassesCoreChecks(t *testing.T) {
	var m manifest.Manifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		t.Fatalf("manifest.json: %v", err)
	}
	fields, _ := check.Fields(check.Validate(&m, packageFiles(t), check.ValidateOptions{Tooling: true}))
	for _, f := range fields {
		t.Errorf("%s: %s (%s)", f.Field, f.Message, f.Code)
	}
}

// TestManifestShape checks manifest.json against the SDK types (no unknown
// fields) and the contract with the console form (docs/PLUGIN-CODEX.md §4).
func TestManifestShape(t *testing.T) {
	dec := json.NewDecoder(bytes.NewReader(manifestJSON))
	dec.DisallowUnknownFields()
	var m manifest.Manifest
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("manifest.json: %v", err)
	}
	if m.Key != "codex" || m.Version != codex.Version || m.Runtime != "grpc" || m.APIVersion != manifest.APIVersion {
		t.Fatalf("key/version/runtime = %s %s %s (plugin version %s)", m.Key, m.Version, m.Runtime, codex.Version)
	}
	// The single request path (Execute relaying without waiting) needs the
	// core that carries forwarding v2.
	if m.HostCompat != ">=0.1.304 <0.2.0" {
		t.Fatalf("hostCompat = %s", m.HostCompat)
	}
	// The compliance boundary is stated in both languages.
	if !strings.Contains(m.Description["en"], "own use with their own ChatGPT subscription") ||
		!strings.Contains(m.Description["en"], "do not pool several people's accounts or resell") ||
		!strings.Contains(m.Description["zh"], "仅用于账号所有者用自己的 ChatGPT 订阅") ||
		!strings.Contains(m.Description["zh"], "不得把多人账号做成号池对外出售") {
		t.Fatalf("description must state the own-use boundary: %v", m.Description)
	}
	if len(m.Platforms) != 0 || m.Database != nil {
		t.Fatalf("platforms = %+v database = %+v, want none", m.Platforms, m.Database)
	}
	if m.UI == nil || m.UI.Native == nil || m.UI.Native.Entry != "ui/native/entry.js" {
		t.Fatalf("ui.native = %+v", m.UI)
	}
	// The settings (user_agent, originator) hold no secret, so
	// ui.settings.secrets masks nothing.
	pluginsdktest.SettingsSecrets(t, manifestJSON)
	if st := m.UI.Settings; st == nil || st.Mode != "native" || st.Component != "CodexSettings" || st.Page != "" {
		t.Fatalf("ui.settings = %+v", st)
	}
	wantCaps := []string{manifest.CapPlatformAdapter, manifest.CapPlatformWebSocket, manifest.CapHTTPRoutes}
	var caps []string
	for _, c := range m.Capabilities {
		caps = append(caps, c.ID)
	}
	if !slices.Equal(caps, wantCaps) {
		t.Fatalf("capabilities = %v, want %v", caps, wantCaps)
	}

	if len(m.AccountTypes) != 2 || m.AccountTypes[0].ID != codex.TypeDevice || m.AccountTypes[1].ID != codex.TypeOAuth {
		t.Fatalf("accountTypes = %+v", m.AccountTypes)
	}
	labels := map[string]string{codex.TypeDevice: "Device Code", codex.TypeOAuth: "Codex"}
	zhLabels := map[string]string{codex.TypeDevice: "设备码", codex.TypeOAuth: "Codex"}
	if m.AccountTypes[0].Label["en"] == m.AccountTypes[1].Label["en"] || m.AccountTypes[0].Label["zh"] == m.AccountTypes[1].Label["zh"] {
		t.Errorf("account type labels must tell the two logins apart: %v %v", m.AccountTypes[0].Label, m.AccountTypes[1].Label)
	}
	for _, at := range m.AccountTypes {
		if at.Form.Mode != "native" || at.Form.Component != "CodexAuthForm" {
			t.Errorf("%s form = %+v", at.ID, at.Form)
		}
		if at.CreationGroup != "codex" || at.AuthMethodLabel["en"] != labels[at.ID] || at.AuthMethodLabel["zh"] != zhLabels[at.ID] {
			t.Errorf("%s creationGroup/authMethodLabel = %q %v", at.ID, at.CreationGroup, at.AuthMethodLabel)
		}
		if !slices.Equal(at.SensitiveFields, []string{"access_token", "refresh_token", "id_token"}) || len(at.SettingsFields) != 0 {
			t.Errorf("%s sensitive/settings = %v %v", at.ID, at.SensitiveFields, at.SettingsFields)
		}
		if at.Refresh == nil {
			t.Errorf("%s must declare refresh", at.ID)
		}
		if len(at.DefaultModels) == 0 || !slices.Equal(at.DefaultModels, m.AccountTypes[0].DefaultModels) {
			t.Errorf("%s default models = %v, want the same non-empty list for both types", at.ID, at.DefaultModels)
		}
		// Usage windows come from the x-codex-* headers of every answer,
		// named by window length (docs/PLUGIN-CODEX.md §13): no usage query.
		q := at.Quota
		if q == nil || q.Query || q.AutoRefresh || !reflect.DeepEqual(q.Headers, codexQuotaHeaders) || at.ModelAvailability {
			t.Fatalf("%s quota = %+v modelAvailability = %v", at.ID, q, at.ModelAvailability)
		}
		if len(q.Windows) != 2 || q.Windows[0].Key != "5h" || q.Windows[1].Key != "7d" {
			t.Fatalf("%s quota windows = %+v", at.ID, q.Windows)
		}
		for _, w := range q.Windows {
			if w.Label["en"] == "" || w.Label["zh"] == "" || w.ShortLabel["en"] == "" || w.ShortLabel["zh"] == "" {
				t.Errorf("%s window %s needs en and zh labels: %+v", at.ID, w.Key, w)
			}
		}
		if len(at.Platforms) != 1 {
			t.Fatalf("%s platforms = %+v", at.ID, at.Platforms)
		}
		ap := at.Platforms[0]
		// The plugin identifies itself and writes every header it sends; of
		// the client's headers it is handed only the Codex backend's turn
		// state routing token, which it passes on (upstream.go). The token is
		// the account's: the core leaves it out once the request moves to
		// another account (CONTRACTS §141.4).
		if ap.Platform != "openai" || !slices.Equal(ap.Endpoints, []string{"responses", "responses_websocket"}) ||
			!slices.Equal(ap.StreamOnly, []string{codex.ProtocolResponses}) || !slices.Equal(ap.PassHeaders, []string{"x-codex-turn-state"}) ||
			!slices.Equal(ap.AccountBoundHeaders, []string{"x-codex-turn-state"}) {
			t.Errorf("%s platform = %+v", at.ID, ap)
		}
		// Chat completions and the image endpoints are served through the
		// core's conversion to Responses; the images are generated by the
		// image tool driven by the plugin's chat model (CONTRACTS §121).
		wantAdapted := map[string]manifest.AdaptOptions{"chat_completions": {},
			"images_generations": {Model: codex.ImageChatModel}, "images_edits": {Model: codex.ImageChatModel}}
		if !reflect.DeepEqual(ap.Adapted, wantAdapted) {
			t.Errorf("%s adapted = %+v", at.ID, ap.Adapted)
		}
		// The Codex backend has one image model: gpt-image-2 is the one
		// name accounts are prefilled with.
		if images := slices.DeleteFunc(slices.Clone(at.DefaultModels), func(m string) bool { return !strings.HasPrefix(m, "gpt-image") }); !slices.Equal(images, []string{"gpt-image-2"}) {
			t.Errorf("%s image models = %v", at.ID, images)
		}
	}

	perms := map[string]manifest.HostPermission{}
	for _, perm := range m.HostPermissions {
		if _, ok := manifest.HostPermissionRisk[perm.ID]; !ok {
			t.Errorf("unknown host permission %q", perm.ID)
		}
		if perm.Reason["en"] == "" || perm.Reason["zh"] == "" {
			t.Errorf("host permission %s needs an en and zh reason", perm.ID)
		}
		perms[perm.ID] = perm
	}
	// No account grant: the plugin receives its own accounts (CONTRACTS §124).
	for _, id := range []string{"platform.register", "accounts.auto_state", "kv", "lock", "routes.admin", "ui.native"} {
		if _, ok := perms[id]; !ok {
			t.Errorf("host permission %s missing", id)
		}
	}
	if len(perms) != 6 {
		t.Errorf("host permissions = %v", perms)
	}
	if c := perms["accounts.auto_state"]; c.Scope["types"] != "own" || len(c.Scope) != 1 {
		t.Errorf("accounts.auto_state scope = %v", c.Scope)
	}
	// The plugin opens no connection: the core sends the authorization
	// requests (through the account form's proxy, CONTRACTS §117), the
	// upstream requests and the refreshes; so no net permission.
	if !slices.Equal(m.ExternalServices, []string{"auth.openai.com", "chatgpt.com"}) {
		t.Errorf("externalServices = %v", m.ExternalServices)
	}

	// Route path -> permission: authorization routes, then the usage reset
	// routes (docs/PLUGIN-CODEX.md §10). The reset routes act on accounts the
	// core checks the caller may act on (consume through its account action).
	wantRoutes := []struct {
		path, permission string
		accounts         *manifest.RouteAccounts
	}{
		{"/auth/device/start", "codex:manage", nil}, {"/auth/device/poll", "codex:manage", nil},
		{"/auth/codex/start", "codex:manage", nil}, {"/auth/codex/exchange", "codex:manage", nil},
		{"/reset-credits/list", "codex:reset", &manifest.RouteAccounts{Action: manifest.AccountsUpdate, From: []string{"body:account_id"}}},
		{"/reset-credits/consume", "codex:reset", &manifest.RouteAccounts{Action: manifest.AccountsUpdate, From: []string{manifest.AccountActionIDs}}},
		{"/reset-credits/summary", "codex:reset", &manifest.RouteAccounts{Action: manifest.AccountsRead, From: []string{"body:account_ids"}}},
	}
	if len(m.Routes) != len(wantRoutes) {
		t.Fatalf("routes = %+v", m.Routes)
	}
	// A fake issuer: no route may reach the real one.
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	defer issuer.Close()
	p, err := codex.NewWithEndpoints(codex.Endpoints{Auth: issuer.URL, ChatGPT: issuer.URL}, manifestJSON)
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range m.Routes {
		if r.Method != http.MethodPost || r.Path != wantRoutes[i].path || r.Scope != "admin" || r.Permission != wantRoutes[i].permission {
			t.Errorf("route %d = %+v", i, r)
		}
		if got := manifest.EffectiveRouteAccounts(&m, r); !reflect.DeepEqual(got, wantRoutes[i].accounts) {
			t.Errorf("route %s accounts = %+v, want %+v", r.Path, got, wantRoutes[i].accounts)
		}
		// Without a host the routes answer an error, but not "no route".
		resp, err := p.HandleHTTP(context.Background(), &pluginv1.HTTPRequest{Method: r.Method, Path: r.Path, RoutePath: r.Path, Body: []byte(`{}`)})
		if err != nil || resp.GetStatus() == http.StatusNotFound {
			t.Errorf("route %s %s is not served: %v %s", r.Method, r.Path, err, resp.GetBody())
		}
	}
	if len(m.UserPermissions) != 2 || m.UserPermissions[0].Key != "codex:manage" || m.UserPermissions[1].Key != "codex:reset" {
		t.Fatalf("userPermissions = %+v", m.UserPermissions)
	}
	// Spending a reset cannot be undone; the permission says so.
	if d := m.UserPermissions[1].Description; !strings.Contains(d["en"], "cannot be undone") || !strings.Contains(d["zh"], "不可撤回") {
		t.Errorf("codex:reset description = %v", d)
	}

	// The reset entry points show on both account types; the row action is
	// never bulk (consume takes exactly one account) and confirms.
	if len(m.UI.AccountActions) != 1 {
		t.Fatalf("accountActions = %+v", m.UI.AccountActions)
	}
	a := m.UI.AccountActions[0]
	if a.Route != "POST /reset-credits/consume" || a.Permission != "codex:reset" || a.Bulk || !a.Danger ||
		a.Confirm["en"] == "" || a.Confirm["zh"] == "" || !slices.Equal(a.AccountTypes, accountTypes) {
		t.Errorf("reset action = %+v", a)
	}
	for _, sl := range m.UI.Slots {
		if sl.Permission != "codex:reset" || !slices.Equal(sl.AccountTypes, accountTypes) {
			t.Errorf("slot %s = %+v", sl.Slot, sl)
		}
	}
}

// accountTypes are the plugin's account types, in manifest order.
var accountTypes = []string{codex.TypeDevice, codex.TypeOAuth}

// codexQuotaHeaders name the usage windows by the length the Codex backend
// sends with each answer (CONTRACTS §105).
var codexQuotaHeaders = []manifest.QuotaHeader{
	{WindowMinutes: "x-codex-primary-window-minutes", Utilization: "x-codex-primary-used-percent", UtilizationUnit: "percent", Reset: "x-codex-primary-reset-at"},
	{WindowMinutes: "x-codex-secondary-window-minutes", Utilization: "x-codex-secondary-used-percent", UtilizationUnit: "percent", Reset: "x-codex-secondary-reset-at"},
}

// TestManifestServes runs the plugin with its embedded manifest.
func TestManifestServes(t *testing.T) {
	p, err := codex.New(manifestJSON)
	if err != nil {
		t.Fatal(err)
	}
	h := pluginsdktest.Start(t, p, pluginsdktest.Options{Manifest: manifestJSON})
	if h.Info.GetPluginKey() != "codex" {
		t.Fatalf("info = %v", h.Info)
	}
	for _, c := range []string{manifest.CapPlatformAdapter, manifest.CapPlatformWebSocket, manifest.CapHTTPRoutes} {
		if !slices.Contains(h.Info.GetCapabilities(), c) {
			t.Errorf("capability %s missing from %v", c, h.Info.GetCapabilities())
		}
	}
}

// packageFiles collects the files a package would carry (the native UI's
// sources are not packaged, only its build output).
func packageFiles(t *testing.T) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case p == ".":
			return nil
		case strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules":
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		case d.IsDir() || !d.Type().IsRegular() || strings.HasSuffix(p, ".go"):
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(p)] = b
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
