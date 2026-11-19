package main

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/plugins/moderation/internal/moderation"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

// TestManifestServes starts the plugin with its embedded manifest: the SDK
// checks the declared capabilities (app.broadcast.v1 needs OnBroadcast and
// the host permission "broadcast") and reports them.
func TestManifestServes(t *testing.T) {
	h := pluginsdktest.Start(t, moderation.New(), pluginsdktest.Options{Manifest: manifestJSON})
	caps := h.Info.GetCapabilities()
	for _, c := range []string{manifest.CapGatewayHook, manifest.CapAppJobs, manifest.CapAppBroadcast, manifest.CapHTTPRoutes} {
		if !slices.Contains(caps, c) {
			t.Fatalf("capability %s missing: %v", c, caps)
		}
	}
	if slices.Contains(caps, manifest.CapAppEvents) {
		t.Fatalf("app.events.v1 must not be reported: %v", caps)
	}
	// Every declared route has a handler (no database here: handlers answer
	// 503 or 4xx, never "no route").
	var m manifest.Manifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		t.Fatal(err)
	}
	for _, r := range m.Routes {
		path := strings.NewReplacer(":user_id", "1", ":id", "1", ":version", "1", ":group_id", "0", ":account_id", "1").Replace(r.Path)
		if resp := h.Do(r.Method, path, nil, "{}"); strings.Contains(string(resp.GetBody()), "no route for") {
			t.Errorf("route %s %s has no handler", r.Method, r.Path)
		}
	}
}

// TestManifest checks manifest.json against the SDK types (no unknown
// fields) and its internal consistency.
func TestManifest(t *testing.T) {
	dec := json.NewDecoder(bytes.NewReader(manifestJSON))
	dec.DisallowUnknownFields()
	var m manifest.Manifest
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("manifest.json: %v", err)
	}
	// The host's own manifest rules (sdk/manifest/check), as an install runs them.
	files := map[string][]byte{"manifest.json": manifestJSON}
	for _, f := range []string{"migrations/0001_init.sql"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		files[f] = b
	}
	if fields, ok := check.Fields(check.Validate(&m, files, check.ValidateOptions{Tooling: true})); ok {
		t.Fatalf("manifest check: %+v", fields)
	}
	// hostCompat: the core of SDK 0.2.0 and of risk and defaults, 0.1.290.
	// UI 4.0: native pages only; the settings pages are written with
	// useForm, SForm and SSecretInput (DESIGN-2026-11-19-PLUGIN-UI-CODE-ONLY).
	if m.Key != "moderation" || m.Version != "0.12.2" || m.HostCompat != ">=0.1.304 <0.2.0" || m.HostUICompat != "^4.0" || m.UI == nil || m.UI.Native == nil ||
		m.UI.Native.Entry != "ui/native/entry.js" || m.Database == nil || m.Database.Schema != "plg_moderation" {
		t.Fatalf("manifest = %+v", m)
	}
	perms := map[string]bool{}
	for _, up := range m.UserPermissions {
		perms[up.Key] = true
		// moderation:exempt (staff) is critical: no defaults and no
		// inheritFrom (a critical permission must not reach moderation:read's
		// holders), and no route or menu uses it. moderation:read shows the
		// moderated text, so it is sensitive.
		switch up.Key {
		case moderation.PermExempt:
			if up.RiskLevel() != manifest.RiskCritical || len(up.Defaults) != 0 || len(up.InheritFrom) != 0 {
				t.Errorf("exempt = %+v", up)
			}
		case "moderation:read":
			if up.RiskLevel() != manifest.RiskSensitive || !reflect.DeepEqual(up.DefaultGrants(), map[string][]string{"admin": {""}}) {
				t.Errorf("read = %+v", up)
			}
		}
	}
	if !perms["moderation:read"] || !perms["moderation:manage"] || !perms[moderation.PermExempt] || len(perms) != 3 {
		t.Fatalf("user permissions = %v", perms)
	}
	delete(perms, moderation.PermExempt)
	routes := map[string]string{}
	for _, r := range m.Routes {
		if !perms[r.Permission] {
			t.Errorf("route %s %s: undeclared permission %q", r.Method, r.Path, r.Permission)
		}
		if r.Scope != "admin" {
			t.Errorf("route %s %s: scope %q", r.Method, r.Path, r.Scope)
		}
		routes[r.Method+" "+r.Path] = r.Permission
	}
	for route, perm := range map[string]string{
		"GET /events": "moderation:read", "GET /events/:id/input": "moderation:read",
		"POST /events/review": "moderation:manage", "PATCH /watchlist/:user_id": "moderation:manage",
		"PUT /watchlist/:user_id/lock": "moderation:manage", "DELETE /watchlist/:user_id/lock": "moderation:manage", "POST /channels/:id/test": "moderation:manage",
		"POST /auditors/:id/try": "moderation:manage", "POST /flows/:id/publish": "moderation:manage", "POST /import": "moderation:manage",
		"POST /test": "moderation:manage", "GET /jev/default-questions": "moderation:read", "DELETE /blocks/:user_id": "moderation:manage",
		"GET /costs": "moderation:read", "POST /flows/:id/rollback": "moderation:manage", "POST /flows/:id/simulate": "moderation:manage",
		"GET /status": "moderation:read", "GET /stats/trend": "moderation:read", "GET /stats/performance": "moderation:read",
		"GET /stats/categories": "moderation:read", "POST /watchlist/:user_id/seen": "moderation:manage", "POST /auditors/try": "moderation:manage",
		"GET /users/:user_id": "moderation:read", "GET /users": "moderation:read",
		"GET /bindings": "moderation:read", "PUT /bindings": "moderation:manage", "DELETE /bindings/:group_id/:account_id": "moderation:manage",
		"POST /verdict-cache/clear": "moderation:manage",
	} {
		if routes[route] != perm {
			t.Errorf("route %s: permission %q, want %q", route, routes[route], perm)
		}
	}
	// The routes of the AI analysis are gone (removed in 0.10.0).
	for _, gone := range []string{"POST /watchlist/:user_id/analyze", "GET /watchlist/:user_id/analyses", "GET /analyses/:id", "GET /analysis/prompt", "GET /analysis/tools"} {
		if _, ok := routes[gone]; ok {
			t.Errorf("route %s is still declared", gone)
		}
	}
	if len(routes) != 56 {
		t.Errorf("routes = %v", routes)
	}
	granted := map[string]*manifest.HostPermission{}
	for i, hp := range m.HostPermissions {
		if _, ok := manifest.HostPermissionRisk[hp.ID]; !ok {
			t.Errorf("unknown host permission %q", hp.ID)
		}
		if hp.Reason["en"] == "" || hp.Reason["zh"] == "" {
			t.Errorf("host permission %s: reason needs en and zh", hp.ID)
		}
		granted[hp.ID] = &m.HostPermissions[i]
	}
	for _, need := range []string{"kv", "secrets", "prices", "db.schema", "gateway.hook", "jobs", "broadcast", "routes.admin", "ui.menu", "ui.native", "net"} {
		if granted[need] == nil {
			t.Errorf("missing host permission %s", need)
		}
	}
	if n := granted["net"]; n == nil || !n.Optional {
		t.Error("net must be optional")
	}
	// users.read (§75.3) is optional: without it user ids are not checked.
	if u := granted["users.read"]; u == nil || !u.Optional {
		t.Error("users.read must be declared and optional")
	}
	// moderation:manage may edit the settings (§75.1), all of them on the
	// plugin's own page (§72.15): native mode on the dashboard
	// (ValidateSettings checks them, ui.settings.secrets says what the core
	// masks).
	if s := m.UI.Settings; s == nil || s.Permission != "moderation:manage" || s.Mode != "native" || s.Page != "dashboard" {
		t.Errorf("ui.settings = %+v", m.UI.Settings)
	}
	if len(m.Hooks) != 3 {
		t.Fatalf("hooks = %+v", m.Hooks)
	}
	// The ban hook matches every protocol (an empty match, CONTRACTS §72.3):
	// banned users are refused on embeddings, count_tokens, task queries and
	// platform plugins' protocols too. It asks for no field.
	ban := m.Hooks[0]
	if ban.ID != moderation.HookBan || ban.Point != "gateway.request" || ban.Order >= m.Hooks[1].Order || len(ban.Needs) != 0 ||
		len(ban.Match.Protocols) != 0 || len(ban.Match.Models) != 0 || len(ban.Match.Groups) != 0 || ban.Failure != "open" {
		t.Fatalf("ban hook = %+v", ban)
	}
	hook := m.Hooks[1]
	wantNeeds := moderation.HookFields
	if hook.ID != moderation.HookModeration || hook.Point != "gateway.request" || hook.Order != 200 || hook.TimeoutMs != 30000 ||
		hook.Failure != "open" || !slices.Equal(hook.Needs, wantNeeds) {
		t.Fatalf("hook = %+v", hook)
	}
	// Every protocol whose content is moderated, including the per-turn
	// Responses WebSocket and the volcengine image and video prompts.
	if !slices.Equal(hook.Match.Protocols, moderation.ModeratedProtocols) {
		t.Fatalf("hook protocols = %v, want %v", hook.Match.Protocols, moderation.ModeratedProtocols)
	}
	// The attempt hook moderates the requests whose flow depends on the
	// account (CONTRACTS §72.17): same match, fields and limits, at
	// gateway.attempt, which the gateway.hook scope must list.
	attempt := m.Hooks[2]
	if attempt.ID != moderation.HookAttempt || attempt.Point != manifest.HookPointAttempt || attempt.Order != hook.Order ||
		attempt.TimeoutMs != hook.TimeoutMs || attempt.Failure != "open" || !slices.Equal(attempt.Needs, wantNeeds) ||
		!slices.Equal(attempt.Match.Protocols, moderation.ModeratedProtocols) {
		t.Fatalf("attempt hook = %+v", attempt)
	}
	points, _ := granted["gateway.hook"].Scope["points"].([]any)
	if len(points) != 2 || points[0] != manifest.HookPointRequest || points[1] != manifest.HookPointAttempt {
		t.Fatalf("gateway.hook scope.points = %v", points)
	}
	fields, _ := granted["gateway.hook"].Scope["fields"].([]any)
	if len(fields) != len(wantNeeds) {
		t.Fatalf("gateway.hook scope.fields = %v", fields)
	}
	for i, f := range fields {
		if f != wantNeeds[i] {
			t.Errorf("scope.fields[%d] = %v, want %s", i, f, wantNeeds[i])
		}
	}
	if len(m.Jobs) != 2 || m.Jobs[0].ID != moderation.JobCleanup || m.Jobs[0].Schedule != "0 4 * * *" ||
		m.Jobs[1].ID != moderation.JobAlerts || m.Jobs[1].Schedule != "@every 1m" {
		t.Fatalf("jobs = %+v", m.Jobs)
	}
	caps := map[string]bool{}
	for _, c := range m.Capabilities {
		caps[c.ID] = true
	}
	for _, c := range []string{manifest.CapGatewayHook, manifest.CapAppJobs, manifest.CapAppBroadcast, manifest.CapHTTPRoutes} {
		if !caps[c] {
			t.Errorf("capability %s not declared", c)
		}
	}
	menu := m.UI.Menus
	if len(menu) != 1 || menu[0].ID != "moderation" || menu[0].Permission != "moderation:read" || menu[0].Page != "dashboard" ||
		m.UI.Pages["dashboard"].Component != "ModerationDashboard" || m.UI.Pages["dashboard"].Type != "native" {
		t.Fatalf("ui = %+v", m.UI)
	}
}

// TestSettingsSecrets: every field the settings form of
// forms/settings.schema.json (removed in 0.12.2) marked secret is in
// ui.settings.secrets, credentials (writeOnly) as credentials and the
// webhook address (x-sensitive only) as sensitive, so the core keeps masking
// each of them in GET /settings (secret_fields) - a field left out would be
// read back in clear.
func TestSettingsSecrets(t *testing.T) {
	var m manifest.Manifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		t.Fatal(err)
	}
	// The schema's secret fields: pointer -> credential.
	schemaSecrets := map[string]bool{"api_key": true, "notify_webhook_secret": true, "notify_webhook_url": false}
	got := map[string]bool{}
	for _, f := range m.UI.Settings.SecretFields() {
		got[f.Pointer()] = f.Credential
	}
	if !reflect.DeepEqual(got, schemaSecrets) {
		t.Fatalf("ui.settings.secrets = %v, want %v", got, schemaSecrets)
	}
	// Each one is a setting the plugin accepts.
	for k := range schemaSecrets {
		for _, e := range moderation.CheckSettings([]byte(`{"` + k + `":""}`)) {
			t.Errorf("secret %s: %v", k, e)
		}
	}
}

// TestValidateSettings: the plugin answers ValidateSettings with
// CheckSettings (CONTRACTS §75.1), field errors as JSON pointers.
func TestValidateSettings(t *testing.T) {
	h := pluginsdktest.Start(t, moderation.New(), pluginsdktest.Options{Manifest: manifestJSON})
	resp, err := h.ValidateSettings(map[string]any{"mode": "enforce", "notify_webhook_url": "https://hooks.example/x", "jev_default_set": nil}, nil)
	if err != nil || len(resp.GetErrors()) != 0 || resp.GetNormalizedConfigJson() != "" {
		t.Fatalf("valid settings: %v %v", resp, err)
	}
	resp, err = h.ValidateSettings(`{"mode":"on","watch_days":0,"cache_ttl_seconds":1,"jev_default_set":{"items":[{"category":"a","label":"A","question":"q","threshold":0}]}}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range resp.GetErrors() {
		got[e.GetField()] = e.GetCode()
	}
	want := map[string]string{"mode": "enum", "watch_days": "minimum", "cache_ttl_seconds": "unknown", "jev_default_set/items/0/threshold": "minimum"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("errors = %v, want %v", got, want)
	}
}

// TestConfigureAppliesStoredSettings: stored settings that ValidateSettings
// would refuse - saved before it existed, with retired keys and values of
// older bounds - still configure (read and clamped), so the plugin starts.
func TestConfigureAppliesStoredSettings(t *testing.T) {
	h := pluginsdktest.Start(t, moderation.New(), pluginsdktest.Options{Manifest: manifestJSON})
	stored := `{"mode":"observe","cache_ttl_seconds":0,"ban_window_hours":2,"allowlist_user_ids":["8"],"base_url":"https://old.example","pass_retention_days":9,"watch_days":0}`
	if resp, err := h.ValidateSettings(stored, nil); err != nil || len(resp.GetErrors()) == 0 {
		t.Fatalf("ValidateSettings must refuse them: %v %v", resp, err)
	}
	if errs := h.Configure(stored, nil); len(errs) > 0 {
		t.Fatalf("Configure refused stored settings: %v", errs)
	}
}
