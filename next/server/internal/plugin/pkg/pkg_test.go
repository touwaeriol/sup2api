package pkg

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"strconv"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/platforms"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg/pkgtest"
)

type entry struct {
	name string
	body string
	mode fs.FileMode
}

func rawZip(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(e.body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestUnpackRejectsUnsafeEntries(t *testing.T) {
	cases := map[string][]entry{
		"zip-slip":      {{name: "../evil", body: "x"}},
		"nested-slip":   {{name: "a/../../evil", body: "x"}},
		"absolute":      {{name: "/etc/passwd", body: "x"}},
		"drive":         {{name: "C:/x", body: "x"}},
		"backslash":     {{name: "a\\b", body: "x"}},
		"symlink":       {{name: "link", body: "/etc/passwd", mode: fs.ModeSymlink | 0o777}},
		"duplicate":     {{name: "a", body: "1"}, {name: "a", body: "2"}},
		"case-dup":      {{name: "A.txt", body: "1"}, {name: "a.txt", body: "2"}},
		"dot-segment":   {{name: "a/./b", body: "1"}},
		"empty-archive": {},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Unpack(rawZip(t, entries...), Limits{})
			if err == nil {
				t.Fatal("expected error")
			}
			if core.AsError(err).Code != "invalid_argument" {
				t.Fatalf("code = %s", core.AsError(err).Code)
			}
		})
	}
}

func TestUnpackLimits(t *testing.T) {
	big := strings.Repeat("a", 1<<20) // compresses to almost nothing
	data := rawZip(t, entry{name: "a", body: big}, entry{name: "b", body: big})
	if _, err := Unpack(data, Limits{MaxUnpackedBytes: 1 << 20}); err == nil {
		t.Fatal("expected unpacked size error")
	}
	if _, err := Unpack(data, Limits{MaxPackageBytes: 10}); err == nil {
		t.Fatal("expected package size error")
	}
	if _, err := Unpack(data, Limits{MaxFiles: 1}); err == nil {
		t.Fatal("expected file count error")
	}
	files, err := Unpack(rawZip(t, entry{name: "dir/", mode: fs.ModeDir | 0o755}, entry{name: "dir/a", body: "x"}), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if string(files["dir/a"]) != "x" || len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	if _, err := Unpack([]byte("not a zip"), Limits{}); err == nil {
		t.Fatal("expected zip error")
	}
}

func opts() ValidateOptions {
	return ValidateOptions{HostVersion: testHostVersion, MaxMemoryMB: 1024}
}

func fieldCodes(err error) map[string]string {
	out := map[string]string{}
	var e *core.Error
	if !errors.As(err, &e) {
		return out
	}
	fs, _ := e.Details["fields"].([]core.FieldError)
	for _, f := range fs {
		out[f.Field] = f.Code
	}
	return out
}

func TestValidateGuardOK(t *testing.T) {
	m := pkgtest.Guard("guard", "0.1.0", "sub2api")
	if err := Validate(m, pkgtest.Files(m), opts()); err != nil {
		t.Fatalf("validate: %v %v", err, fieldCodes(err))
	}
}

// app.broadcast.v1 is a known capability and is accepted together with the
// broadcast host permission (CONTRACTS §14.3).
func TestValidateBroadcastOK(t *testing.T) {
	m := pkgtest.Guard("guard", "0.1.0", "sub2api")
	m.Capabilities = append(m.Capabilities, manifest.Capability{ID: manifest.CapAppBroadcast})
	m.HostPermissions = append(m.HostPermissions, manifest.HostPermission{ID: "broadcast",
		Reason: manifest.LocalizedText{"en": "Sync rules across nodes", "zh": "在节点间同步规则"}})
	if err := Validate(m, pkgtest.Files(m), opts()); err != nil {
		t.Fatalf("validate: %v %v", err, fieldCodes(err))
	}
}

// A plugin may declare its own sidebar section and put menus into it or
// into a core section (CONTRACTS §22).
func TestValidateMenuSectionsOK(t *testing.T) {
	m := pkgtest.Guard("guard", "0.1.0", "sub2api")
	m.UI.Sections = []manifest.MenuSection{{ID: "safety", Label: manifest.LocalizedText{"en": "Safety", "zh": "安全"}, Order: 350}}
	m.UI.Menus[0].Section = "safety"
	m.UI.Menus = append(m.UI.Menus, manifest.Menu{ID: "rules", Section: "gateway", Label: manifest.LocalizedText{"en": "Rules"}, Page: "dashboard"})
	if err := Validate(m, pkgtest.Files(m), opts()); err != nil {
		t.Fatalf("validate: %v %v", err, fieldCodes(err))
	}
}

// Hook needs may be gjson queries (CONTRACTS §20.1): an identical entry in
// gateway.hook scope.fields covers them; a different query does not. The
// hook timeout may go up to 30 s.
func TestValidateGjsonQueryNeeds(t *testing.T) {
	queries := []string{`messages|@reverse|#(role=="user")`, `input|@reverse|#(role=="user")`, `[input]|#(%"*")`, `contents|@reverse|#(role=="user")`}
	build := func(needs, fields []string, timeoutMs int) (*manifest.Manifest, map[string][]byte) {
		m := pkgtest.Guard("guard", "0.1.0", "sub2api")
		m.Hooks[0].Needs = needs
		m.Hooks[0].TimeoutMs = timeoutMs
		for i, hp := range m.HostPermissions {
			if hp.ID == "gateway.hook" {
				fa := make([]any, len(fields))
				for j, f := range fields {
					fa[j] = f
				}
				m.HostPermissions[i].Scope = map[string]any{"points": []any{"gateway.request"}, "fields": fa}
			}
		}
		// Round-trip through manifest.json like a real package.
		files := pkgtest.Files(m)
		var back manifest.Manifest
		if err := json.Unmarshal(files["manifest.json"], &back); err != nil {
			t.Fatal(err)
		}
		return &back, files
	}
	needs := append([]string{"model"}, queries...)
	m, files := build(needs, needs, 30000)
	if got := m.Hooks[0].Needs; len(got) != len(needs) || got[1] != queries[0] || got[3] != queries[2] {
		t.Fatalf("needs after round trip %q", got)
	}
	if err := Validate(m, files, opts()); err != nil {
		t.Fatalf("validate: %v %v", err, fieldCodes(err))
	}
	m, files = build(needs, []string{"model", `messages|@reverse|#(role=="assistant")`}, 300)
	if c := fieldCodes(Validate(m, files, opts())); c["hooks[0].needs"] != "exceeds_scope" {
		t.Fatalf("codes %v", c)
	}
	m, files = build(needs, needs, 30001)
	if c := fieldCodes(Validate(m, files, opts())); c["hooks[0].timeoutMs"] != "out_of_range" {
		t.Fatalf("codes %v", c)
	}
	// Consent: approving the identical (or a narrower) field list is within the request.
	req := NormalizeScope(map[string]any{"fields": needs})
	if !ScopeWithin(NormalizeScope(map[string]any{"fields": needs}), req) ||
		!ScopeWithin(NormalizeScope(map[string]any{"fields": []string{"model", queries[0]}}), req) ||
		ScopeWithin(NormalizeScope(map[string]any{"fields": []string{"messages"}}), req) {
		t.Fatal("ScopeWithin with query fields")
	}
}

// manifest scheduler.rank needs both the scheduler.rank host permission and
// the scheduler.rank.v1 capability, and its timeoutMs is 0 (host default) or
// 50-1000 ms. The order is unconstrained and match protocols are not checked
// here (the platform may belong to a plugin installed later).
func TestValidateSchedulerRank(t *testing.T) {
	build := func(timeoutMs int, withPerm, withCap bool) *manifest.Manifest {
		m := pkgtest.Guard("guard", "0.1.0", "sub2api")
		m.Scheduler = &manifest.Scheduler{Rank: &manifest.SchedulerRank{
			Order:     -50,
			Match:     manifest.HookMatch{Protocols: []string{"someplugin.chat"}, Models: []string{"gpt-*"}},
			TimeoutMs: timeoutMs,
		}}
		if withCap {
			m.Capabilities = append(m.Capabilities, manifest.Capability{ID: manifest.CapSchedulerRank})
		}
		if withPerm {
			m.HostPermissions = append(m.HostPermissions, manifest.HostPermission{ID: "scheduler.rank",
				Reason: manifest.LocalizedText{"en": "Prefer accounts with spare quota"}})
		}
		return m
	}
	m := build(200, true, true)
	if err := Validate(m, pkgtest.Files(m), opts()); err != nil {
		t.Fatalf("validate: %v %v", err, fieldCodes(err))
	}
	// 0 means "host default" and is accepted.
	m = build(0, true, true)
	if err := Validate(m, pkgtest.Files(m), opts()); err != nil {
		t.Fatalf("timeoutMs 0: %v %v", err, fieldCodes(err))
	}
	for _, bad := range []int{49, 1001, -1} {
		m = build(bad, true, true)
		if c := fieldCodes(Validate(m, pkgtest.Files(m), opts())); c["scheduler.rank.timeoutMs"] != "invalid" {
			t.Fatalf("timeoutMs %d: codes %v", bad, c)
		}
	}
	m = build(200, false, true)
	if c := fieldCodes(Validate(m, pkgtest.Files(m), opts())); c["scheduler.rank"] != "missing_host_permission" {
		t.Fatalf("without permission: codes %v", c)
	}
	m = build(200, true, false)
	if c := fieldCodes(Validate(m, pkgtest.Files(m), opts())); c["scheduler.rank"] != "missing_capability" {
		t.Fatalf("without capability: codes %v", c)
	}
	// scheduler without rank declares nothing and needs neither.
	m = pkgtest.Guard("guard", "0.1.0", "sub2api")
	m.Scheduler = &manifest.Scheduler{}
	if err := Validate(m, pkgtest.Files(m), opts()); err != nil {
		t.Fatalf("empty scheduler: %v %v", err, fieldCodes(err))
	}
}

func TestValidateConsistency(t *testing.T) {
	type mut func(m *manifest.Manifest, files map[string][]byte)
	dropPerm := func(id string) mut {
		return func(m *manifest.Manifest, _ map[string][]byte) {
			out := m.HostPermissions[:0]
			for _, p := range m.HostPermissions {
				if p.ID != id {
					out = append(out, p)
				}
			}
			m.HostPermissions = out
		}
	}
	cases := []struct {
		name  string
		mut   mut
		field string
		code  string
	}{
		{"bad key", func(m *manifest.Manifest, _ map[string][]byte) { m.Key = "Guard" }, "key", "invalid_format"},
		{"bad semver", func(m *manifest.Manifest, _ map[string][]byte) { m.Version = "1.0" }, "version", "invalid_semver"},
		{"runtime", func(m *manifest.Manifest, _ map[string][]byte) { m.Runtime = "js" }, "runtime", "unsupported"},
		{"missing arm64", func(_ *manifest.Manifest, f map[string][]byte) { delete(f, "runtimes/linux-arm64/plugin") }, "entry.grpc.binaries", "binary_missing"},
		{"host compat", func(m *manifest.Manifest, _ map[string][]byte) { m.HostCompat = ">=0.2.0" }, "hostCompat", "incompatible"},
		{"unknown cap", func(m *manifest.Manifest, _ map[string][]byte) {
			m.Capabilities = append(m.Capabilities, manifest.Capability{ID: "x.v1"})
		}, "capabilities[4]", "unknown"},
		{"hooks without perm", dropPerm("gateway.hook"), "hooks", "missing_host_permission"},
		{"hook needs exceed", func(m *manifest.Manifest, _ map[string][]byte) {
			m.Hooks[0].Needs = append(m.Hooks[0].Needs, "messages")
		}, "hooks[0].needs", "exceeds_scope"},
		{"plugin prices", func(_ *manifest.Manifest, f map[string][]byte) {
			var raw map[string]any
			_ = json.Unmarshal(f["manifest.json"], &raw)
			raw["pricing"] = []any{map[string]any{"model": "gpt-4o", "mode": "per_token", "config": map[string]any{"p": 1}}}
			f["manifest.json"], _ = json.Marshal(raw)
		}, "pricing", "unsupported"},
		{"events exceed", func(m *manifest.Manifest, _ map[string][]byte) {
			m.Events.Subscribe = append(m.Events.Subscribe, "balance.changed")
		}, "events.subscribe", "exceeds_scope"},
		{"jobs perm", dropPerm("jobs"), "jobs", "missing_host_permission"},
		{"broadcast perm", func(m *manifest.Manifest, _ map[string][]byte) {
			m.Capabilities = append(m.Capabilities, manifest.Capability{ID: manifest.CapAppBroadcast})
		}, "capabilities[4]", "missing_host_permission"},
		{"bad cron", func(m *manifest.Manifest, _ map[string][]byte) { m.Jobs[0].Schedule = "every day" }, "jobs[0].schedule", "invalid"},
		{"db schema name", func(m *manifest.Manifest, _ map[string][]byte) { m.Database.Schema = "plg_other" }, "database.schema", "invalid"},
		{"db perm", dropPerm("db.schema"), "database", "missing_host_permission"},
		{"no migrations", func(_ *manifest.Manifest, f map[string][]byte) { delete(f, "migrations/0001_init.sql") }, "database.migrations", "file_missing"},
		{"routes perm", dropPerm("routes.admin"), "routes[0].scope", "missing_host_permission"},
		{"route unknown permission", func(m *manifest.Manifest, _ map[string][]byte) { m.Routes[0].Permission = "nope:x" }, "routes[0].permission", "unknown"},
		{"menu perm", dropPerm("ui.menu"), "ui", "missing_host_permission"},
		{"menu section unknown", func(m *manifest.Manifest, _ map[string][]byte) { m.UI.Menus[0].Section = "nope" }, "ui.menus[0].section", "invalid"},
		{"section id reserved", func(m *manifest.Manifest, _ map[string][]byte) {
			m.UI.Sections = []manifest.MenuSection{{ID: "system", Label: manifest.LocalizedText{"en": "x"}}}
		}, "ui.sections[0].id", "invalid"},
		{"section label", func(m *manifest.Manifest, _ map[string][]byte) {
			m.UI.Sections = []manifest.MenuSection{{ID: "safety"}}
		}, "ui.sections[0].label", "required"},
		{"section duplicate", func(m *manifest.Manifest, _ map[string][]byte) {
			m.UI.Sections = []manifest.MenuSection{{ID: "safety", Label: manifest.LocalizedText{"en": "x"}}, {ID: "safety", Label: manifest.LocalizedText{"en": "y"}}}
		}, "ui.sections[1].id", "duplicate"},
		{"native perm", dropPerm("ui.native"), "ui.native", "missing_host_permission"},
		{"native ui compat", func(m *manifest.Manifest, _ map[string][]byte) { m.HostUICompat = "" }, "hostUICompat", "required"},
		{"native entry missing", func(_ *manifest.Manifest, f map[string][]byte) { delete(f, "ui/native/entry.js") }, "ui.native.entry", "file_missing"},
		{"settings secrets missing", func(m *manifest.Manifest, _ map[string][]byte) { m.UI.Settings.Secrets = nil }, "ui.settings.secrets", "required"},
		{"settings schema mode", func(m *manifest.Manifest, _ map[string][]byte) { m.UI.Settings = &manifest.Form{Mode: "schema"} }, "ui.settings.mode", "unsupported"},
		{"table page", func(m *manifest.Manifest, _ map[string][]byte) {
			m.UI.Pages["assets"] = manifest.Page{Type: "table"}
		}, "ui.pages.assets.type", "unsupported"},
		{"iframe page", func(m *manifest.Manifest, _ map[string][]byte) {
			m.UI.Pages["frame"] = manifest.Page{Type: "iframe", Src: "ui/iframe/index.html"}
		}, "ui.pages.frame", "missing_host_permission"},
		{"memory cap", func(m *manifest.Manifest, _ map[string][]byte) { m.Resources.MemoryMB = 4096 }, "resources.memoryMB", "out_of_range"},
		{"user perm key", func(m *manifest.Manifest, _ map[string][]byte) { m.UserPermissions[0].Key = "Rules" }, "userPermissions[0].key", "invalid_format"},
		{"icon missing", func(m *manifest.Manifest, _ map[string][]byte) { m.Icon = "icon.png" }, "icon", "file_missing"},
		{"platform without perm", func(m *manifest.Manifest, _ map[string][]byte) {
			m.HostPermissions = append(m.HostPermissions, manifest.HostPermission{ID: "platform.register"})
			m.Platforms = []manifest.Platform{{ID: "guardp", Endpoints: []manifest.Endpoint{guardEndpoint("guardp", "POST", "/v1/guard")}}}
		}, "platforms", "missing_host_permission"},
		{"reserved endpoint", func(m *manifest.Manifest, _ map[string][]byte) {
			m.HostPermissions = append(m.HostPermissions, manifest.HostPermission{ID: "gateway.endpoint"}, manifest.HostPermission{ID: "platform.register"})
			m.Platforms = []manifest.Platform{{ID: "guardp", Endpoints: []manifest.Endpoint{guardEndpoint("guardp", "GET", "/api/v1/x")}}}
		}, "platforms[0].endpoints[0].path", "invalid_path"},
		{"platform perms", func(m *manifest.Manifest, _ map[string][]byte) {
			m.HostPermissions = append(m.HostPermissions, manifest.HostPermission{ID: "gateway.endpoint"})
			m.Platforms = []manifest.Platform{{ID: "guardp", Endpoints: []manifest.Endpoint{guardEndpoint("guardp", "POST", "/v1/guard")}}}
		}, "platforms", "missing_host_permission"},
		// Plugins take no part in sticky sessions (CONTRACTS §5.6).
		{"sticky rules", func(m *manifest.Manifest, _ map[string][]byte) {
			m.HostPermissions = append(m.HostPermissions, manifest.HostPermission{ID: "gateway.endpoint"}, manifest.HostPermission{ID: "platform.register"})
			m.Platforms = []manifest.Platform{{ID: "guardp", Endpoints: []manifest.Endpoint{guardEndpoint("guardp", "POST", "/v1/guard")},
				StickyRules: []manifest.StickyRule{{Name: "s", KeySources: []manifest.StickyKeySource{{Type: "header", Name: "x"}}}}}}
		}, "platforms[0].stickyRules", "sticky_not_for_plugins"},
		{"affinity capability", func(m *manifest.Manifest, _ map[string][]byte) {
			m.Capabilities = append(m.Capabilities, manifest.Capability{ID: "scheduler.affinity.v1"})
		}, "capabilities[" + strconv.Itoa(len(pkgtest.Guard("guard", "0.1.0", "sub2api").Capabilities)) + "]", "sticky_not_for_plugins"},
		{"account types need adapter", func(m *manifest.Manifest, _ map[string][]byte) {
			m.HostPermissions = append(m.HostPermissions, manifest.HostPermission{ID: "platform.register"})
			m.AccountTypes = []manifest.AccountType{{ID: "apikey", Label: manifest.LocalizedText{"en": "API key"},
				Form: manifest.Form{Mode: "native", Component: "X"}, Platforms: []manifest.AccountPlatform{{Platform: "anthropic"}}}}
		}, "accountTypes", "missing_capability"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := pkgtest.Guard("guard", "0.1.0", "sub2api")
			files := pkgtest.Files(m)
			tc.mut(m, files)
			err := Validate(m, files, opts())
			if err == nil {
				t.Fatal("expected error")
			}
			codes := fieldCodes(err)
			if codes[tc.field] != tc.code {
				t.Fatalf("want %s=%s, got %v", tc.field, tc.code, codes)
			}
		})
	}
}

// guardEndpoint is a minimal valid endpoint of platform pid.
func guardEndpoint(pid, method, path string) manifest.Endpoint {
	return manifest.Endpoint{ID: "e", Method: method, Path: path, Protocol: pid + ".call", Kind: "proxy",
		Auth: manifest.EndpointAuth{Headers: []string{"authorization"}}, Request: manifest.EndpointRequest{ModelPath: "model"},
		Response: manifest.EndpointResp{NonStream: "json"}, ErrorFormat: "plain", Billing: "usage", BillingTypes: []string{"per_request", "per_token", "expression"}}
}

func TestValidatePlatformAndAccountTypes(t *testing.T) {
	m := pkgtest.Platform("video", "0.1.0", "sub2api")
	if err := Validate(m, pkgtest.Files(m), opts()); err != nil {
		t.Fatalf("validate: %v %v", err, fieldCodes(err))
	}

	// A platform does not need account types, and a plugin may declare
	// account types for a built-in platform only (the anthropic plugin).
	noTypes := pkgtest.Platform("video", "0.1.0", "sub2api")
	noTypes.AccountTypes = nil
	noTypes.Capabilities = nil
	noTypes.HostPermissions = noTypes.HostPermissions[:2]
	noTypes.UI, noTypes.HostUICompat = nil, ""
	if err := Validate(noTypes, pkgtest.Files(noTypes), opts()); err != nil {
		t.Fatalf("platform without account types: %v", fieldCodes(err))
	}
	anth := pkgtest.Anthropic("anthropic", "0.1.0", "sub2api")
	if err := Validate(anth, pkgtest.Files(anth), opts()); err != nil {
		t.Fatalf("account types for a built-in platform: %v", fieldCodes(err))
	}
	// Guarded settings (CONTRACTS §21.3): a settings field restricted to
	// official URLs.
	anth.AccountTypes[0].SettingsFields = []string{"base_url"}
	anth.AccountTypes[0].GuardedSettings = []manifest.GuardedSetting{{Field: "base_url",
		Allowed: []string{"https://api.anthropic.com", "http://localhost:8080/v1"}}}
	if err := Validate(anth, pkgtest.Files(anth), opts()); err != nil {
		t.Fatalf("guarded settings: %v", fieldCodes(err))
	}
	// Account types may reference platforms of other plugins (served once
	// that plugin is enabled).
	anth.AccountTypes[0].Platforms = append(anth.AccountTypes[0].Platforms, manifest.AccountPlatform{Platform: "video",
		Usage: map[string]manifest.UsageRules{"video.generate": {Semantics: "inclusive"}}})
	if err := Validate(anth, pkgtest.Files(anth), opts()); err != nil {
		t.Fatalf("account type for another plugin's platform: %v", fieldCodes(err))
	}

	type mut func(m *manifest.Manifest, files map[string][]byte)
	ep := func(m *manifest.Manifest, i int) *manifest.Endpoint { return &m.Platforms[0].Endpoints[i] }
	cases := []struct {
		name  string
		mut   mut
		field string
		code  string
	}{
		{"type id format", func(m *manifest.Manifest, _ map[string][]byte) { m.AccountTypes[0].ID = "1key" }, "accountTypes[0].id", "invalid_format"},
		{"type id too short", func(m *manifest.Manifest, _ map[string][]byte) { m.AccountTypes[0].ID = "k" }, "accountTypes[0].id", "invalid_format"},
		{"type id duplicate", func(m *manifest.Manifest, _ map[string][]byte) {
			m.AccountTypes = append(m.AccountTypes, m.AccountTypes[0])
		}, "accountTypes[1].id", "duplicate"},
		{"type label", func(m *manifest.Manifest, _ map[string][]byte) { m.AccountTypes[0].Label = nil }, "accountTypes[0].label", "required"},
		{"no platforms", func(m *manifest.Manifest, _ map[string][]byte) { m.AccountTypes[0].Platforms = nil }, "accountTypes[0].platforms", "required"},
		{"empty platform", func(m *manifest.Manifest, _ map[string][]byte) {
			m.AccountTypes[0].Platforms = []manifest.AccountPlatform{{}}
		}, "accountTypes[0].platforms[0].platform", "required"},
		{"platform duplicate", func(m *manifest.Manifest, _ map[string][]byte) {
			m.AccountTypes[0].Platforms = []manifest.AccountPlatform{{Platform: "anthropic"}, {Platform: "anthropic"}}
		}, "accountTypes[0].platforms[1].platform", "duplicate"},
		{"usage protocol of own platform", func(m *manifest.Manifest, _ map[string][]byte) {
			m.AccountTypes[0].Platforms[0].Usage = map[string]manifest.UsageRules{"video.nope": {}}
		}, "accountTypes[0].platforms[0].usage.video.nope", "unknown_protocol"},
		{"usage protocol of other platform", func(m *manifest.Manifest, _ map[string][]byte) {
			m.AccountTypes[0].Platforms = []manifest.AccountPlatform{{Platform: "anthropic",
				Usage: map[string]manifest.UsageRules{"openai.chat": {}}}}
		}, "accountTypes[0].platforms[0].usage.openai.chat", "unknown_protocol"},
		{"usage override", func(m *manifest.Manifest, _ map[string][]byte) {
			m.AccountTypes[0].Platforms[0].Usage = map[string]manifest.UsageRules{"video.generate": {Semantics: "both"}}
		}, "accountTypes[0].platforms[0].usage.video.generate.semantics", "invalid"},
		{"form component", func(m *manifest.Manifest, _ map[string][]byte) { m.AccountTypes[0].Form.Component = "" }, "accountTypes[0].form.component", "required"},
		{"schema form", func(m *manifest.Manifest, _ map[string][]byte) {
			m.AccountTypes[0].Form = manifest.Form{Mode: "schema"}
		}, "accountTypes[0].form.mode", "unsupported"},
		// Guarded settings (CONTRACTS §21.3).
		{"guarded field required", func(m *manifest.Manifest, _ map[string][]byte) {
			m.AccountTypes[0].SettingsFields = []string{"base_url"}
			m.AccountTypes[0].GuardedSettings = []manifest.GuardedSetting{{Allowed: []string{"https://api.example.com"}}}
		}, "accountTypes[0].guardedSettings[0].field", "required"},
		{"guarded field not a settings field", func(m *manifest.Manifest, _ map[string][]byte) {
			m.AccountTypes[0].SettingsFields = []string{"region"}
			m.AccountTypes[0].GuardedSettings = []manifest.GuardedSetting{{Field: "base_url", Allowed: []string{"https://api.example.com"}}}
		}, "accountTypes[0].guardedSettings[0].field", "unknown_field"},
		{"guarded field without settings fields", func(m *manifest.Manifest, _ map[string][]byte) {
			m.AccountTypes[0].GuardedSettings = []manifest.GuardedSetting{{Field: "base_url", Allowed: []string{"https://api.example.com"}}}
		}, "accountTypes[0].guardedSettings[0].field", "unknown_field"},
		{"guarded field duplicate", func(m *manifest.Manifest, _ map[string][]byte) {
			m.AccountTypes[0].SettingsFields = []string{"base_url"}
			m.AccountTypes[0].GuardedSettings = []manifest.GuardedSetting{
				{Field: "base_url", Allowed: []string{"https://api.example.com"}},
				{Field: "base_url", Allowed: []string{"https://api2.example.com"}}}
		}, "accountTypes[0].guardedSettings[1].field", "duplicate"},
		{"guarded allowed empty", func(m *manifest.Manifest, _ map[string][]byte) {
			m.AccountTypes[0].SettingsFields = []string{"base_url"}
			m.AccountTypes[0].GuardedSettings = []manifest.GuardedSetting{{Field: "base_url"}}
		}, "accountTypes[0].guardedSettings[0].allowed", "required"},
		{"guarded allowed relative", func(m *manifest.Manifest, _ map[string][]byte) {
			m.AccountTypes[0].SettingsFields = []string{"base_url"}
			m.AccountTypes[0].GuardedSettings = []manifest.GuardedSetting{{Field: "base_url", Allowed: []string{"https://api.example.com", "/v1"}}}
		}, "accountTypes[0].guardedSettings[0].allowed[1]", "invalid_url"},
		{"guarded allowed scheme", func(m *manifest.Manifest, _ map[string][]byte) {
			m.AccountTypes[0].SettingsFields = []string{"base_url"}
			m.AccountTypes[0].GuardedSettings = []manifest.GuardedSetting{{Field: "base_url", Allowed: []string{"ftp://api.example.com"}}}
		}, "accountTypes[0].guardedSettings[0].allowed[0]", "invalid_url"},
		{"guarded allowed no host", func(m *manifest.Manifest, _ map[string][]byte) {
			m.AccountTypes[0].SettingsFields = []string{"base_url"}
			m.AccountTypes[0].GuardedSettings = []manifest.GuardedSetting{{Field: "base_url", Allowed: []string{"https://"}}}
		}, "accountTypes[0].guardedSettings[0].allowed[0]", "invalid_url"},
		{"guarded allowed whitespace", func(m *manifest.Manifest, _ map[string][]byte) {
			m.AccountTypes[0].SettingsFields = []string{"base_url"}
			m.AccountTypes[0].GuardedSettings = []manifest.GuardedSetting{{Field: "base_url", Allowed: []string{"https://api.example.com "}}}
		}, "accountTypes[0].guardedSettings[0].allowed[0]", "invalid_url"},
		{"guarded allowed empty string", func(m *manifest.Manifest, _ map[string][]byte) {
			m.AccountTypes[0].SettingsFields = []string{"base_url"}
			m.AccountTypes[0].GuardedSettings = []manifest.GuardedSetting{{Field: "base_url", Allowed: []string{""}}}
		}, "accountTypes[0].guardedSettings[0].allowed[0]", "invalid_url"},
		{"needs platform.register", func(m *manifest.Manifest, _ map[string][]byte) {
			m.Platforms = nil
			m.AccountTypes[0].Platforms = []manifest.AccountPlatform{{Platform: "anthropic"}}
			m.HostPermissions = nil
		}, "accountTypes", "missing_host_permission"},
		{"removed account grant", func(m *manifest.Manifest, _ map[string][]byte) {
			m.HostPermissions = append(m.HostPermissions, manifest.HostPermission{ID: "accounts.credentials", Scope: map[string]any{"types": "own"}})
		}, "hostPermissions[3].id", "outdated_contract"},
		// Platforms.
		{"platform id format", func(m *manifest.Manifest, _ map[string][]byte) { m.Platforms[0].ID = "Video" }, "platforms[0].id", "invalid_format"},
		{"platform id built-in", func(m *manifest.Manifest, _ map[string][]byte) {
			m.Platforms[0].ID = manifest.PlatformAnthropic
		}, "platforms[0].id", "builtin_platform"},
		{"platform id duplicate", func(m *manifest.Manifest, _ map[string][]byte) {
			p := m.Platforms[0]
			p.Endpoints = []manifest.Endpoint{guardEndpoint("video", "POST", "/video/v2/x")}
			m.Platforms = append(m.Platforms, p)
		}, "platforms[1].id", "duplicate"},
		{"platform endpoints", func(m *manifest.Manifest, _ map[string][]byte) { m.Platforms[0].Endpoints = nil }, "platforms[0].endpoints", "required"},
		{"platform usage", func(m *manifest.Manifest, _ map[string][]byte) { m.Platforms[0].Usage.Semantics = "x" }, "platforms[0].usage.semantics", "invalid"},
		{"endpoint usage", func(m *manifest.Manifest, _ map[string][]byte) {
			ep(m, 0).Usage = &manifest.UsageRules{Semantics: "x"}
		}, "platforms[0].endpoints[0].usage.semantics", "invalid"},
		{"endpoint id duplicate", func(m *manifest.Manifest, _ map[string][]byte) { ep(m, 1).ID = "generate" }, "platforms[0].endpoints[1].id", "duplicate"},
		{"protocol of another platform", func(m *manifest.Manifest, _ map[string][]byte) {
			ep(m, 0).Protocol = "anthropic.messages"
		}, "platforms[0].endpoints[0].protocol", "invalid_format"},
		{"protocol without name", func(m *manifest.Manifest, _ map[string][]byte) { ep(m, 0).Protocol = "video." }, "platforms[0].endpoints[0].protocol", "invalid_format"},
		{"protocol required", func(m *manifest.Manifest, _ map[string][]byte) { ep(m, 0).Protocol = "" }, "platforms[0].endpoints[0].protocol", "required"},
		{"model source required", func(m *manifest.Manifest, _ map[string][]byte) {
			ep(m, 0).Request = manifest.EndpointRequest{}
		}, "platforms[0].endpoints[0].request", "required"},
		{"model param not in path", func(m *manifest.Manifest, _ map[string][]byte) {
			ep(m, 1).Request.ModelParam = "name"
		}, "platforms[0].endpoints[1].request.modelParam", "unknown_param"},
		{"bad param segment", func(m *manifest.Manifest, _ map[string][]byte) {
			ep(m, 1).Path = "/video/v1/models/:model:"
		}, "platforms[0].endpoints[1].path", "invalid_path"},
		{"catch-all not last", func(m *manifest.Manifest, _ map[string][]byte) {
			ep(m, 1).Path = "/video/*rest/x"
		}, "platforms[0].endpoints[1].path", "invalid_path"},
		{"own endpoints overlap", func(m *manifest.Manifest, _ map[string][]byte) {
			ep(m, 1).Method = "POST"
			ep(m, 1).Path = "/video/v1/:kind"
		}, "platforms[0].endpoints[1].path", "duplicate"},
		{"own endpoints overlap across platforms", func(m *manifest.Manifest, _ map[string][]byte) {
			m.Platforms = append(m.Platforms, manifest.Platform{ID: "video2", Endpoints: []manifest.Endpoint{
				guardEndpoint("video2", "post", "/video/:v/videos")}})
		}, "platforms[1].endpoints[0].path", "duplicate"},
		{"built-in endpoint conflict", func(m *manifest.Manifest, _ map[string][]byte) {
			// Matches POST /v1/messages/count_tokens; "/v1/:x" would also match
			// the core's /v1/files and be refused as a core path first.
			ep(m, 0).Path = "/v1/messages/:x"
			ep(m, 0).Request = manifest.EndpointRequest{ModelParam: "x"}
		}, "platforms[0].endpoints[0].path", "endpoint_conflict"},
		{"core gateway path", func(m *manifest.Manifest, _ map[string][]byte) {
			ep(m, 0).Path = "/v1/files/:id"
			ep(m, 0).Request = manifest.EndpointRequest{ModelParam: "id"}
		}, "platforms[0].endpoints[0].path", "invalid_path"},
		{"sticky rules", func(m *manifest.Manifest, _ map[string][]byte) {
			m.Platforms[0].StickyRules = []manifest.StickyRule{{Name: "session", KeySources: []manifest.StickyKeySource{{Type: "header", Name: "x-session-id"}}}}
		}, "platforms[0].stickyRules", "sticky_not_for_plugins"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := pkgtest.Platform("video", "0.1.0", "sub2api")
			files := pkgtest.Files(m)
			tc.mut(m, files)
			codes := fieldCodes(Validate(m, files, opts()))
			if codes[tc.field] != tc.code {
				t.Fatalf("want %s=%s, got %v", tc.field, tc.code, codes)
			}
		})
	}
}

// Platform ids and endpoints of other installed plugins are checked at
// install time (ARCHITECTURE 6.4).
func TestValidateOtherPlugins(t *testing.T) {
	other := pkgtest.Platform("video", "0.1.0", "sub2api")
	pfs, eps := OthersFromManifests([]*manifest.Manifest{other, pkgtest.Anthropic("anthropic", "0.1.0", "sub2api")})
	if len(pfs) != 1 || pfs[0].PluginKey != "video" || pfs[0].ID != "video" || len(pfs[0].Endpoints) != 2 || len(eps) != 2 || eps[1].Platform != "video" {
		t.Fatalf("others = %+v %+v", pfs, eps)
	}

	// Same platform id declared by another plugin.
	m := pkgtest.Platform("video", "0.1.0", "sub2api")
	m.Key = "clone"
	o := opts()
	o.OtherPlatforms, o.OtherEndpoints = pfs, eps
	codes := fieldCodes(Validate(m, pkgtest.Files(m), o))
	if codes["platforms[0].id"] != "platform_conflict" {
		t.Fatalf("codes = %v", codes)
	}
	// Endpoint overlapping another plugin's endpoint (parameter vs literal).
	if codes["platforms[0].endpoints[0].path"] != "endpoint_conflict" {
		t.Fatalf("codes = %v", codes)
	}

	// Distinct id and paths: fine.
	m2 := pkgtest.Platform("clip", "0.1.0", "sub2api")
	if err := Validate(m2, pkgtest.Files(m2), o); err != nil {
		t.Fatalf("distinct platform: %v", fieldCodes(err))
	}
	m2.Platforms[0].Endpoints[0].Path = "/:any/v1/videos"
	if codes := fieldCodes(Validate(m2, pkgtest.Files(m2), o)); codes["platforms[0].endpoints[0].path"] != "invalid_path" {
		t.Fatalf("first segment parameter: %v", codes)
	}
	m2.Platforms[0].Endpoints[0].Path = "/video/:ver/videos"
	if codes := fieldCodes(Validate(m2, pkgtest.Files(m2), o)); codes["platforms[0].endpoints[0].path"] != "endpoint_conflict" {
		t.Fatalf("parameter overlap: %v", codes)
	}

	// Upgrades of the same plugin do not conflict with themselves.
	self := pkgtest.Platform("video", "0.2.0", "sub2api")
	if err := Validate(self, pkgtest.Files(self), o); err != nil {
		t.Fatalf("own platform should not conflict: %v", fieldCodes(err))
	}
}

func TestPathsOverlap(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"/v1/messages", "/v1/messages", true},
		{"/v1/messages", "/v1/messages/", true},
		{"/v1/messages", "/v1/chat", false},
		{"/v1/:x/messages", "/v1/:y/messages", true},
		{"/v1/:x", "/v1/messages", true},
		{"/v1/:x", "/v1/messages/count", false},
		{"/v1beta/models/:model:generateContent", "/v1beta/models/:model:streamGenerateContent", false},
		{"/v1beta/models/:model:generateContent", "/v1beta/models/:m", true},
		{"/v1beta/models/:model:generateContent", "/v1beta/models/gemini:generateContent", true},
		{"/v1beta/models/:model:generateContent", "/v1beta/models/generateContent", false},
		{"/v1beta/models/:model:generateContent", "/v1beta/models/x:countTokens", false},
		{"/m/:a:Content", "/m/:b:generateContent", false},
		{"/m/:a:x", "/m/:b:y:x", true},
		{"/v1/*rest", "/v1/a/b/c", true},
		{"/v1/*rest", "/v2/a", false},
		{"/v1/*rest", "/v1", true},
		{"/v1/a", "/v1/a/b", false},
	}
	for _, tc := range cases {
		if got := check.PathsOverlap(tc.a, tc.b); got != tc.want {
			t.Errorf("PathsOverlap(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
		if got := check.PathsOverlap(tc.b, tc.a); got != tc.want {
			t.Errorf("PathsOverlap(%q, %q) = %v, want %v", tc.b, tc.a, got, tc.want)
		}
	}
	a := manifest.Endpoint{Method: "post", Path: "/v1/:x"}
	if !EndpointsConflict(a, manifest.Endpoint{Method: "POST", Path: "/v1/y"}) || EndpointsConflict(a, manifest.Endpoint{Method: "GET", Path: "/v1/y"}) {
		t.Fatal("EndpointsConflict")
	}
	if p := check.PathParams("/v1beta/models/:model:generateContent/*rest"); len(p) != 2 || p[0] != "model" || p[1] != "rest" {
		t.Fatalf("PathParams = %v", p)
	}
}

// Every built-in platform passes the endpoint rules applied to plugin
// platforms, and its endpoints do not overlap each other.
func TestBuiltinPlatformsValid(t *testing.T) {
	for _, p := range platforms.Builtin() {
		for i, e := range p.Endpoints {
			for _, o := range p.Endpoints[:i] {
				if EndpointsConflict(o, e) {
					t.Errorf("%s: %s %s overlaps %s %s", p.ID, e.Method, e.Path, o.Method, o.Path)
				}
			}
		}
		for _, fe := range check.CheckPlatform(p) {
			t.Errorf("%s: %s %s %s", p.ID, fe.Field, fe.Code, fe.Message)
		}
	}
}

func TestValidateDevModeBinary(t *testing.T) {
	m := pkgtest.Minimal("mini", "0.1.0", "p")
	files := pkgtest.Files(m)
	o := opts()
	o.DevMode, o.GOOS, o.GOARCH = true, "windows", "amd64"
	if err := Validate(m, files, o); err == nil {
		t.Fatal("expected missing windows binary")
	}
	files["runtimes/windows-amd64/plugin.exe"] = []byte("MZ")
	if err := Validate(m, files, o); err != nil {
		t.Fatalf("dev mode: %v", fieldCodes(err))
	}
}

func TestCheckTrust(t *testing.T) {
	m := pkgtest.Guard("guard", "0.1.0", "sub2api")
	if err := CheckTrust(m, TrustVerified); err != nil {
		t.Fatal(err)
	}
	codes := fieldCodes(CheckTrust(m, TrustCommunity))
	if codes["ui.native"] != "trust_insufficient" {
		t.Fatalf("codes = %v", codes)
	}
	if err := CheckTrust(pkgtest.Minimal("mini", "0.1.0", "p"), TrustUnsigned); err != nil {
		t.Fatal(err)
	}
}

func TestScopeWithin(t *testing.T) {
	req := map[string]any{"domains": []any{"a.com", "b.com"}, "max_usd": 10.0}
	cases := []struct {
		narrow map[string]any
		want   bool
	}{
		{map[string]any{"domains": []any{"a.com"}, "max_usd": 5.0}, true},
		{map[string]any{"domains": []any{"a.com", "b.com"}, "max_usd": 10.0}, true},
		{map[string]any{"domains": []any{"c.com"}, "max_usd": 5.0}, false},
		{map[string]any{"domains": []any{"a.com"}, "max_usd": 50.0}, false},
		{map[string]any{"domains": []any{"a.com"}}, false}, // unrestricted max_usd
		{map[string]any{"domains": []any{"a.com"}, "max_usd": 1.0, "extra": true}, true},
	}
	for i, tc := range cases {
		if got := ScopeWithin(tc.narrow, req); got != tc.want {
			t.Errorf("case %d: got %v", i, got)
		}
	}
	if !ScopeWithin(map[string]any{"subscribe": []any{"usage.recorded"}}, map[string]any{"subscribe": []any{"usage.*"}}) {
		t.Error("pattern should cover")
	}
	if !check.PatternCovers("*.example.com", "hooks.example.com") || check.PatternCovers("*.example.com", "example.com") {
		t.Error("domain wildcard")
	}
}

func TestOpenParsesSignature(t *testing.T) {
	k := pkgtest.NewKey("k1")
	m := pkgtest.Guard("guard", "0.1.0", "sub2api")
	p, err := Open(pkgtest.Build(m, k), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Signature == nil || p.Signature.KeyID != "k1" || p.Manifest.Key != "guard" || len(p.SHA256) != 64 {
		t.Fatalf("package = %+v", p.Signature)
	}
	if _, err := Open(pkgtest.Zip(map[string][]byte{"x": []byte("1")}), Limits{}); err == nil {
		t.Fatal("expected missing manifest")
	}
}
