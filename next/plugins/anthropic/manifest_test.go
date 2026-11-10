package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/sdk/upstream"
)

// declaration is the upstream of the apikey account type. sdk/upstream's
// golden replay (TestGolden) proves it answers exactly like the plugin code
// it replaced, so keep the two in step.
const declaration = `{"preset":"anthropic","filterFields":[]}`

// TestManifest runs manifest.json through the checks the core runs before
// it installs a package (sdk/manifest/check in tooling mode, which also
// resolves the upstream declaration with the core's engine) and checks what
// the plugin keeps: the declared apikey account type of the built-in
// anthropic platform, the model catalog and its upgrade fixture.
func TestManifest(t *testing.T) {
	m := load(t)
	if m.Key != "anthropic" || m.Version != "0.3.1" || m.APIVersion != manifest.APIVersion {
		t.Fatalf("key/version = %s %s", m.Key, m.Version)
	}
	if m.Database == nil || m.Database.Schema != "plg_"+m.Key {
		t.Fatalf("database = %+v", m.Database)
	}

	// The anthropic platform and its endpoints are built into the core
	// (ARCHITECTURE 6.6): the plugin declares no platform of its own.
	if len(m.Platforms) != 0 {
		t.Fatalf("platforms = %+v, want none (anthropic is built in)", m.Platforms)
	}
	if len(m.AccountTypes) != 1 {
		t.Fatalf("accountTypes = %+v", m.AccountTypes)
	}
	at := m.AccountTypes[0]
	if at.ID != "apikey" || len(at.Platforms) != 1 || at.Platforms[0].Platform != "anthropic" {
		t.Fatalf("account type %s platforms = %+v, want apikey on anthropic", at.ID, at.Platforms)
	}
	// No overrides: the built-in platform defaults apply.
	if ap := at.Platforms[0]; len(ap.RequestFields)+len(ap.PassHeaders)+len(ap.Usage) != 0 {
		t.Fatalf("account type %s overrides the platform defaults: %+v", at.ID, ap)
	}
	if !slices.Contains(at.SensitiveFields, "api_key") {
		t.Fatal("api_key must be sensitive")
	}
	// Model mapping is a core account field (CONTRACTS §18): neither a
	// settings field nor a form property.
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	_ = json.Unmarshal(mustJSONFile(t, at.Form.Schema), &schema)
	if _, bad := schema.Properties["model_mapping"]; bad || slices.Contains(at.SettingsFields, "model_mapping") {
		t.Fatal("model_mapping must be neither a settings field nor a form property")
	}
	assertJSON(t, at.Upstream.Layer(), declaration)
	p, err := upstream.Resolve(&at, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// base_url is guarded (CONTRACTS §21.3): without account:settings:custom
	// only the default upstream may be used.
	if gs := at.GuardedSettings; len(gs) != 1 || gs[0].Field != "base_url" || len(gs[0].Allowed) != 1 || gs[0].Allowed[0] != p.Upstream().BaseURL.Default {
		t.Fatalf("guardedSettings = %+v, want base_url -> [%s]", gs, p.Upstream().BaseURL.Default)
	}

	caps := map[string]bool{}
	for _, c := range m.Capabilities {
		caps[c.ID] = true
	}
	// The process serves the catalog route only: platform.adapter.v1 is the
	// account type's (manifest/check), platform.execute.v1 would promise a
	// Platform the plugin no longer implements.
	if !caps[manifest.CapPlatformAdapter] || !caps[manifest.CapHTTPRoutes] || caps[manifest.CapPlatformExecute] {
		t.Fatalf("capabilities = %v, want platform.adapter.v1 and http.routes.v1", m.Capabilities)
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
	if _, ok := perms["platform.register"]; !ok {
		t.Error("account types need host permission platform.register")
	}
	if _, ok := perms["gateway.endpoint"]; ok {
		t.Error("gateway.endpoint is only for plugins that declare platforms")
	}
	// The credentials stay inside the core, which builds the requests.
	if _, ok := perms["accounts.credentials"]; ok {
		t.Error("a declared account type needs no accounts.credentials")
	}
	// The catalog is an admin route only: the console has no menu or page for it.
	if _, ok := perms["ui.menu"]; ok || m.UI != nil {
		t.Errorf("ui = %+v, want none and no ui.menu permission", m.UI)
	}
	for _, r := range m.Routes {
		found := false
		for _, up := range m.UserPermissions {
			found = found || up.Key == r.Permission
		}
		if !found {
			t.Errorf("route %s %s uses undeclared permission %q", r.Method, r.Path, r.Permission)
		}
	}
	if _, err := os.Stat(filepath.FromSlash(strings.TrimSuffix(m.Database.Migrations, "/"))); err != nil {
		t.Fatalf("migrations dir: %v", err)
	}
	var patch map[string]any
	raw := mustJSONFile(t, "testdata/upgrade/manifest.patch.json")
	_ = json.Unmarshal(raw, &patch)
	// The fixture upgrades the release (e2e AC05, deploy/ci/check.sh): its
	// version must stay above the manifest's.
	fixture, _ := patch["version"].(string)
	if v, err := semver.StrictNewVersion(fixture); err != nil || !v.GreaterThan(semver.MustParse(m.Version)) {
		t.Fatalf("upgrade fixture version %q must be above the release %s", fixture, m.Version)
	}
	// The overlay must not bring back the pre-6.6 layouts.
	for _, k := range []string{"platform", "gateway", "platforms"} {
		if _, bad := patch[k]; bad {
			t.Fatalf("upgrade fixture sets %q; anthropic is a built-in platform", k)
		}
	}
	for _, at := range asSlice(patch["accountTypes"]) {
		if e, ok := at.(map[string]any); ok && e["protocols"] != nil {
			t.Fatal("upgrade fixture sets accountTypes[].protocols; account types declare platforms")
		}
	}
	if patch["pricing"] != nil {
		t.Fatal("upgrade fixture declares model prices; administrators set prices in the core (CONTRACTS §17)")
	}
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

// load decodes manifest.json strictly and checks it with the files a
// package carries.
func load(t *testing.T) manifest.Manifest {
	t.Helper()
	var m manifest.Manifest
	dec := json.NewDecoder(bytes.NewReader(manifestJSON))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("manifest.json: %v", err)
	}
	files := map[string][]byte{"manifest.json": manifestJSON}
	for _, dir := range []string{"forms", "assets", "migrations"} {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			p := path.Join(dir, e.Name())
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			files[p] = b
		}
	}
	if err := check.Validate(&m, files, check.ValidateOptions{Tooling: true}); err != nil {
		t.Fatal(err)
	}
	return m
}

// assertJSON compares two JSON documents by value.
func assertJSON(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var g, w any
	_ = json.Unmarshal(got, &g)
	_ = json.Unmarshal([]byte(want), &w)
	if g == nil || !reflect.DeepEqual(g, w) {
		t.Fatalf("upstream = %s, want %s", got, want)
	}
}

func mustJSONFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash(p))
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	if !json.Valid(b) {
		t.Fatalf("%s is not valid JSON", p)
	}
	return b
}
