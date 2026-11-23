package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/plugins/vertex/internal/vertex"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

// TestManifestPassesCoreChecks runs manifest.json through the checks the
// server runs before it installs a plugin (Tooling: the runtime binaries are
// produced by the build).
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

// wantType is what the manifest must declare of an account type
// (docs/DESIGN-2026-11-17-CLOUD-PROVIDERS.md §7).
type wantType struct {
	// component is the native account form (ui/native/src/entry.ts).
	id, component, authEN, authZH string
	// sensitive: the secret the form shows (masked when read back), and the
	// access token the core writes for a key file type.
	sensitive []string
	refresh   bool
}

var wantTypes = []wantType{
	{vertex.TypeServiceAccount, "VertexKeyFileForm", "Service Account", "服务账号", []string{"service_account_json", "access_token"}, true},
	{vertex.TypeAuthorizedUser, "VertexKeyFileForm", "User Credentials", "用户凭证", []string{"authorized_user_json", "access_token"}, true},
	{vertex.TypeAPIKey, "VertexApiKeyForm", "API Key", "API Key", []string{"api_key"}, false},
}

// TestManifestShape pins manifest.json to the design and to the constants
// the code uses.
func TestManifestShape(t *testing.T) {
	dec := json.NewDecoder(bytes.NewReader(manifestJSON))
	dec.DisallowUnknownFields()
	var m manifest.Manifest
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("manifest.json: %v", err)
	}
	if m.Key != "vertex" || m.Version != vertex.Version || m.Runtime != "grpc" || m.APIVersion != manifest.APIVersion || m.Publisher != "sub2api" {
		t.Fatalf("key/version/runtime = %s %s %s (plugin version %s)", m.Key, m.Version, m.Runtime, vertex.Version)
	}
	if m.Entry.GRPC == nil || m.Entry.GRPC.Binaries == "" || m.HostCompat != ">=0.1.312 <0.2.0" {
		t.Fatalf("entry/hostCompat = %+v %s", m.Entry, m.HostCompat)
	}
	if m.Name["en"] == "" || m.Name["zh"] == "" || m.Description["en"] == "" || m.Description["zh"] == "" {
		t.Fatal("name and description need en and zh")
	}
	// An ordinary forwarding plugin: no platform, routes, jobs or database
	// of its own; its UI is the account forms.
	if len(m.Capabilities) != 1 || m.Capabilities[0].ID != manifest.CapPlatformAdapter {
		t.Fatalf("capabilities = %+v", m.Capabilities)
	}
	if len(m.Platforms) != 0 || len(m.Routes) != 0 || m.Database != nil || len(m.UserPermissions) != 0 {
		t.Fatalf("the manifest declares more than account types: %+v", m)
	}
	if m.UI == nil || m.UI.Native == nil || m.UI.Native.Entry != "ui/native/entry.js" || m.UI.Settings != nil || len(m.UI.Slots) != 0 || m.HostUICompat != "^4.0" {
		t.Fatalf("ui = %+v, hostUICompat = %q", m.UI, m.HostUICompat)
	}

	if len(m.AccountTypes) != len(wantTypes) {
		t.Fatalf("accountTypes = %d", len(m.AccountTypes))
	}
	for i, want := range wantTypes {
		at := m.AccountTypes[i]
		if at.ID != want.id {
			t.Fatalf("accountTypes[%d] = %s, want %s", i, at.ID, want.id)
		}
		if at.Form.Mode != "native" || at.Form.Component != want.component {
			t.Errorf("%s form = %+v", at.ID, at.Form)
		}
		// One entry in the "new account" dialog, switched by auth method.
		if at.CreationGroup != "vertex" || at.AuthMethodLabel["en"] != want.authEN || at.AuthMethodLabel["zh"] != want.authZH {
			t.Errorf("%s creationGroup/authMethodLabel = %q %v", at.ID, at.CreationGroup, at.AuthMethodLabel)
		}
		if at.Label["en"] == "" || at.Label["zh"] == "" || at.Description["en"] == "" || at.Description["zh"] == "" {
			t.Errorf("%s needs en and zh label and description", at.ID)
		}
		// The host comes from the location: no address field, nothing guarded.
		if at.UpstreamFields == nil || len(at.UpstreamFields) != 0 || len(at.GuardedSettings) != 0 {
			t.Errorf("%s upstreamFields/guardedSettings = %v %v", at.ID, at.UpstreamFields, at.GuardedSettings)
		}
		if !slices.Equal(at.SensitiveFields, want.sensitive) || !slices.Equal(at.SettingsFields, []string{"project_id", "location"}) {
			t.Errorf("%s sensitive/settings = %v %v", at.ID, at.SensitiveFields, at.SettingsFields)
		}
		// Default refresh declaration: expires_at, 30 minutes ahead.
		if (at.Refresh != nil) != want.refresh || (at.Refresh != nil && (at.Refresh.Field() != "expires_at" || at.Refresh.BeforeExpirySec != 0)) {
			t.Errorf("%s refresh = %+v", at.ID, at.Refresh)
		}
		if len(at.Platforms) != 1 || at.Platforms[0].Platform != vertex.PlatformID ||
			!slices.Equal(at.Platforms[0].Endpoints, []string{"generate_content", "stream_generate_content", "count_tokens",
				"generate_content_v1", "stream_generate_content_v1", "count_tokens_v1"}) {
			t.Errorf("%s platforms = %+v", at.ID, at.Platforms)
		}
		if !slices.Contains(at.DefaultModels, vertex.DefaultTestModel) || len(at.DefaultModelMapping) != 0 {
			t.Errorf("%s defaultModels must hold the test model and no mapping: %v %v", at.ID, at.DefaultModels, at.DefaultModelMapping)
		}
		for _, model := range at.DefaultModels {
			if !strings.HasPrefix(model, "gemini-") {
				t.Errorf("%s default model %q is not a Gemini publisher model", at.ID, model)
			}
		}
		if !slices.Equal(at.DefaultModels, m.AccountTypes[0].DefaultModels) {
			t.Errorf("%s default models differ from %s", at.ID, m.AccountTypes[0].ID)
		}
	}
	// The forms' default location of a key file account (ui/native/src/fields.ts).
	if vertex.LocationGlobal != "global" {
		t.Errorf("LocationGlobal = %q: update ui/native/src/fields.ts with it", vertex.LocationGlobal)
	}

	perms := map[string]manifest.HostPermission{}
	for _, perm := range m.HostPermissions {
		if perm.Reason["en"] == "" || perm.Reason["zh"] == "" {
			t.Errorf("host permission %s needs an en and zh reason", perm.ID)
		}
		perms[perm.ID] = perm
	}
	// No "net": the core sends the upstream and token requests.
	if len(perms) != 2 || perms["platform.register"].ID == "" || perms["ui.native"].ID == "" {
		t.Errorf("host permissions = %v", perms)
	}
	// externalServices is googleapis.com: the global and the regional
	// Vertex AI hosts (aiplatform.googleapis.com, <region>-aiplatform...)
	// are all under it, and the core relays only to a host the manifest
	// declares or the account names (CONTRACTS §112.17); an account names
	// none.
	if !slices.Equal(m.ExternalServices, []string{"googleapis.com"}) {
		t.Errorf("externalServices = %v, want [googleapis.com]", m.ExternalServices)
	}
}

// TestManifestServes runs the plugin with its embedded manifest.
func TestManifestServes(t *testing.T) {
	h := pluginsdktest.Start(t, vertex.New(), pluginsdktest.Options{Manifest: manifestJSON})
	if h.Info.GetPluginKey() != "vertex" || h.Info.GetVersion() != vertex.Version {
		t.Fatalf("info = %v", h.Info)
	}
	if !slices.Contains(h.Info.GetCapabilities(), manifest.CapPlatformAdapter) {
		t.Errorf("capabilities = %v", h.Info.GetCapabilities())
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
