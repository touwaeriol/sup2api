package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/plugins/moderation/internal/moderation"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

// TestManifestServes starts the plugin with its embedded manifest: the SDK
// checks the declared capabilities (app.broadcast.v1 needs OnBroadcast and
// the host permission "broadcast") and reports them.
func TestManifestServes(t *testing.T) {
	h := pluginsdktest.Start(t, moderation.New(), pluginsdktest.Options{SDK: []pluginsdk.Option{pluginsdk.WithManifest(manifestJSON)}})
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
	for _, f := range []string{"forms/settings.schema.json", "forms/settings.ui.json", "migrations/0001_init.sql"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		files[f] = b
	}
	if fields, ok := check.Fields(check.Validate(&m, files, check.ValidateOptions{Tooling: true})); ok {
		t.Fatalf("manifest check: %+v", fields)
	}
	if m.Key != "moderation" || m.Version != "0.7.4" || m.HostUICompat == "" || m.UI == nil || m.UI.Native == nil ||
		m.UI.Native.Entry != "ui/native/entry.js" || m.Database == nil || m.Database.Schema != "plg_moderation" {
		t.Fatalf("manifest = %+v", m)
	}
	perms := map[string]bool{}
	for _, up := range m.UserPermissions {
		perms[up.Key] = true
	}
	if !perms["moderation:read"] || !perms["moderation:manage"] || len(perms) != 2 {
		t.Fatalf("user permissions = %v", perms)
	}
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
		"POST /events/review": "moderation:manage", "PATCH /watchlist/:user_id": "moderation:manage", "POST /channels/:id/test": "moderation:manage",
		"POST /auditors/:id/try": "moderation:manage", "POST /flows/:id/publish": "moderation:manage", "POST /import": "moderation:manage",
		"POST /test": "moderation:manage", "GET /jev/default-questions": "moderation:read", "DELETE /blocks/:user_id": "moderation:manage",
		"POST /watchlist/:user_id/analyze": "moderation:manage", "GET /analyses/:id": "moderation:read", "GET /costs": "moderation:read",
		"POST /flows/:id/rollback": "moderation:manage", "POST /flows/:id/simulate": "moderation:manage", "GET /analysis/prompt": "moderation:read",
		"GET /status": "moderation:read", "GET /stats/trend": "moderation:read", "GET /stats/performance": "moderation:read",
		"GET /stats/categories": "moderation:read", "POST /watchlist/:user_id/seen": "moderation:manage", "POST /auditors/try": "moderation:manage",
		"GET /users/:user_id": "moderation:read", "GET /users": "moderation:read",
		"GET /bindings": "moderation:read", "PUT /bindings": "moderation:manage", "DELETE /bindings/:group_id/:account_id": "moderation:manage",
	} {
		if routes[route] != perm {
			t.Errorf("route %s: permission %q, want %q", route, routes[route], perm)
		}
	}
	if len(routes) != 57 {
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
	// plugin's own page (§72.15): native mode on the dashboard, with the
	// schema the core validates and masks with.
	if s := m.UI.Settings; s == nil || s.Permission != "moderation:manage" || s.Mode != "native" || s.Page != "dashboard" ||
		s.Schema != "forms/settings.schema.json" || s.UISchema != "forms/settings.ui.json" {
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
	if len(m.Jobs) != 3 || m.Jobs[0].ID != moderation.JobCleanup || m.Jobs[0].Schedule != "0 4 * * *" ||
		m.Jobs[1].ID != moderation.JobAnalysis || m.Jobs[1].Schedule != "@every 1m" ||
		m.Jobs[2].ID != moderation.JobAlerts || m.Jobs[2].Schedule != "@every 1m" {
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

// TestSettingsForms checks that the schema and the ui schema cover exactly
// the same fields and that the schema defaults decode to DefaultSettings.
func TestSettingsForms(t *testing.T) {
	var m manifest.Manifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		t.Fatal(err)
	}
	var schema struct {
		AdditionalProperties *bool                     `json:"additionalProperties"`
		Properties           map[string]map[string]any `json:"properties"`
		Required             []string                  `json:"required"`
	}
	b, err := os.ReadFile(filepath.FromSlash(m.UI.Settings.Schema))
	if err != nil || json.Unmarshal(b, &schema) != nil {
		t.Fatalf("schema: %v", err)
	}
	if schema.AdditionalProperties == nil || *schema.AdditionalProperties || len(schema.Required) != 0 {
		t.Fatal("schema must set additionalProperties:false and require nothing ({\"mode\":\"off\"} alone is valid)")
	}
	var ui map[string]json.RawMessage
	b, err = os.ReadFile(filepath.FromSlash(m.UI.Settings.UISchema))
	if err != nil || json.Unmarshal(b, &ui) != nil {
		t.Fatalf("ui schema: %v", err)
	}
	var order []string
	_ = json.Unmarshal(ui["ui:order"], &order)
	if len(order) != len(schema.Properties) {
		t.Fatalf("ui:order has %d fields, schema %d", len(order), len(schema.Properties))
	}
	defaults := map[string]any{}
	for _, k := range order {
		prop, ok := schema.Properties[k]
		if !ok {
			t.Errorf("ui:order field %s not in schema", k)
			continue
		}
		if _, ok := prop["default"]; !ok {
			t.Errorf("field %s has no default", k)
		}
		defaults[k] = prop["default"]
		var fu map[string]json.RawMessage
		if json.Unmarshal(ui[k], &fu) != nil || fu["ui:title"] == nil {
			t.Errorf("field %s has no ui:title", k)
		}
	}
	for _, k := range []string{"api_key", "notify_webhook_secret"} {
		if schema.Properties[k]["writeOnly"] != true {
			t.Errorf("%s must be writeOnly", k)
		}
	}
	// The 0.1 fields stay in the schema (api_key above all: without it the
	// core would no longer mask it) but the settings page does not show
	// them (§72.15); every other field is shown.
	legacy := []string{"base_url", "api_key", "model", "system_prompt", "categories", "tool_choice", "max_turns", "temperature",
		"max_tokens", "timeout_ms", "on_error", "sample_rate", "group_ids", "model_patterns", "retention_days"}
	for _, k := range order {
		var fu struct {
			Widget string `json:"ui:widget"`
		}
		_ = json.Unmarshal(ui[k], &fu)
		if hidden := fu.Widget == "hidden"; hidden != slices.Contains(legacy, k) {
			t.Errorf("field %s: hidden = %v", k, hidden)
		}
	}
	// The first field carries a section, so no field falls into an untitled group.
	var first map[string]json.RawMessage
	if json.Unmarshal(ui[order[0]], &first) != nil || first["ui:section"] == nil {
		t.Errorf("first field %s starts no ui:section", order[0])
	}
	if schema.Properties["notify_webhook_url"]["x-sensitive"] != true {
		t.Error("notify_webhook_url must be x-sensitive (chat webhooks embed a token)")
	}
	raw, _ := json.Marshal(defaults)
	got, err := moderation.DecodeSettings(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := moderation.DefaultSettings()
	gb, _ := json.Marshal(got)
	wb, _ := json.Marshal(want)
	if !bytes.Equal(gb, wb) {
		t.Fatalf("schema defaults decode to\n%s\nwant\n%s", gb, wb)
	}
}
