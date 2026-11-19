package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/plugins/bedrock/internal/bedrock"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

// hostCompat is the range of cores the plugin installs on. Its lower bound
// is the first core release with the forwarding V2 contract (Execute and
// RelayUpstream with UpstreamRequest.response_stream,
// docs/DESIGN-2026-11-18-PLUGIN-FORWARDING-V2.md "core V"); an older core
// speaks another plugin protocol version and refuses the package anyway.
const hostCompat = ">=0.1.304 <0.2.0"

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

// The plugin refuses cores before the forwarding V2 contract.
func TestHostCompat(t *testing.T) {
	for version, want := range map[string]bool{
		"0.1.290": false, "0.1.302": false, "0.1.303": false,
		"0.1.304": true, "0.1.304-rc.1": true, "0.1.999": true,
		"0.2.0": false,
	} {
		if got, err := check.HostCompatible(hostCompat, version); err != nil || got != want {
			t.Errorf("core %s: compatible = %v (%v), want %v", version, got, err, want)
		}
	}
}

// wantType is what the manifest must declare of an account type
// (docs/DESIGN-2026-11-17-CLOUD-PROVIDERS.md §9).
type wantType struct {
	id, authEN, authZH string
	// sensitive are the fields the account form shows as secrets
	// (ui/native/src/BedrockAccountForm.vue): masked when read back.
	sensitive []string
	settings  []string
}

var sharedSettings = []string{"region", "inference_profile_prefix"}

var wantTypes = []wantType{
	{bedrock.TypeAPIKey, "API Key", "API Key", []string{"api_key"}, sharedSettings},
	{bedrock.TypeAccessKey, "Access Key", "Access Key", []string{"secret_access_key"}, sharedSettings},
	{bedrock.TypeSTS, "Temporary Credentials", "临时凭证", []string{"secret_access_key", "session_token"}, append(slices.Clone(sharedSettings), "session_expires_at")},
}

// formComponent is the native account form of every type, registered by
// ui/native/src/entry.ts.
const formComponent = "BedrockAccountForm"

// TestManifestShape pins manifest.json to the design and to the constants
// the code uses.
func TestManifestShape(t *testing.T) {
	dec := json.NewDecoder(bytes.NewReader(manifestJSON))
	dec.DisallowUnknownFields()
	var m manifest.Manifest
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("manifest.json: %v", err)
	}
	if m.Key != "bedrock" || !regexp.MustCompile(`^[a-z][a-z0-9_]{1,29}$`).MatchString(m.Key) ||
		m.Version != bedrock.Version || m.Runtime != "grpc" || m.APIVersion != manifest.APIVersion || m.Publisher != "sub2api" {
		t.Fatalf("key/version/runtime = %s %s %s (plugin version %s)", m.Key, m.Version, m.Runtime, bedrock.Version)
	}
	if m.Entry.GRPC == nil || m.Entry.GRPC.Binaries == "" || m.HostCompat != hostCompat {
		t.Fatalf("entry/hostCompat = %+v %s", m.Entry, m.HostCompat)
	}
	if m.Name["en"] == "" || m.Name["zh"] == "" || m.Description["en"] == "" || m.Description["zh"] == "" {
		t.Fatal("name and description need en and zh")
	}
	// An ordinary forwarding plugin: no platform, routes, jobs or database
	// of its own; its UI is the account form.
	if len(m.Capabilities) != 1 || m.Capabilities[0].ID != manifest.CapPlatformAdapter {
		t.Fatalf("capabilities = %+v", m.Capabilities)
	}
	if len(m.Platforms) != 0 || len(m.Routes) != 0 || m.Database != nil || len(m.UserPermissions) != 0 {
		t.Fatalf("the manifest declares more than account types: %+v", m)
	}
	if m.UI == nil || m.UI.Native == nil || m.UI.Native.Entry != "ui/native/entry.js" || m.UI.Settings != nil || len(m.UI.Slots) != 0 || m.HostUICompat != "^4.0" {
		t.Fatalf("ui = %+v, hostUICompat = %q", m.UI, m.HostUICompat)
	}
	if _, err := os.Stat(m.Icon); err != nil || m.Icon != "assets/bedrock-color.svg" {
		t.Fatalf("icon %q: %v", m.Icon, err)
	}

	if len(m.AccountTypes) != len(wantTypes) {
		t.Fatalf("accountTypes = %d", len(m.AccountTypes))
	}
	for i, want := range wantTypes {
		at := m.AccountTypes[i]
		if at.ID != want.id {
			t.Fatalf("accountTypes[%d] = %s, want %s", i, at.ID, want.id)
		}
		if at.Form.Mode != "native" || at.Form.Component != formComponent {
			t.Errorf("%s form = %+v", at.ID, at.Form)
		}
		// One entry in the "new account" dialog, switched by auth method.
		if at.CreationGroup != "bedrock" || at.AuthMethodLabel["en"] != want.authEN || at.AuthMethodLabel["zh"] != want.authZH {
			t.Errorf("%s creationGroup/authMethodLabel = %q %v", at.ID, at.CreationGroup, at.AuthMethodLabel)
		}
		if at.Label["en"] == "" || at.Label["zh"] == "" || at.Description["en"] == "" || at.Description["zh"] == "" {
			t.Errorf("%s needs en and zh label and description", at.ID)
		}
		// The host comes from the region: no address field, nothing guarded.
		if at.UpstreamFields == nil || len(at.UpstreamFields) != 0 || len(at.GuardedSettings) != 0 {
			t.Errorf("%s upstreamFields/guardedSettings = %v %v", at.ID, at.UpstreamFields, at.GuardedSettings)
		}
		if !slices.Equal(at.SensitiveFields, want.sensitive) || !slices.Equal(at.SettingsFields, want.settings) {
			t.Errorf("%s sensitive/settings = %v %v", at.ID, at.SensitiveFields, at.SettingsFields)
		}
		// Nothing renews the credentials, no quota, no balance, and the
		// answer is metered by the platforms' own rules.
		if at.Refresh != nil || at.Quota != nil || at.Balance != nil {
			t.Errorf("%s declares refresh, quota or balance", at.ID)
		}
		checkPlatforms(t, at)
		checkModels(t, at, m.AccountTypes[0])
	}
	// The form's region default is the plugin's, and its prefix choices
	// (ui/native/src/fields.ts) are the plugin's values; the form's
	// default prefix is the global profile the prefilled models need.
	if bedrock.DefaultRegion != "us-east-1" || bedrock.DefaultProfilePrefix != "global" ||
		!slices.Equal(bedrock.ProfilePrefixes, []string{"none", "us", "eu", "apac", "jp", "au", "in", "global"}) {
		t.Errorf("defaults %s / %s, prefixes %v: update ui/native/src/fields.ts with them", bedrock.DefaultRegion, bedrock.DefaultProfilePrefix, bedrock.ProfilePrefixes)
	}

	perms := map[string]manifest.HostPermission{}
	for _, perm := range m.HostPermissions {
		if perm.Reason["en"] == "" || perm.Reason["zh"] == "" {
			t.Errorf("host permission %s needs an en and zh reason", perm.ID)
		}
		perms[perm.ID] = perm
	}
	// No "net": the core sends the upstream requests.
	if len(perms) != 2 || perms["platform.register"].ID == "" || perms["ui.native"].ID == "" {
		t.Errorf("host permissions = %v", perms)
	}
	// externalServices is amazonaws.com, the domain of every runtime host:
	// the core relays only to a host the manifest declares or the account
	// names (CONTRACTS §112.17), and an account names none.
	if !slices.Equal(m.ExternalServices, []string{bedrock.ServiceDomain}) {
		t.Errorf("externalServices = %v, want [%s]", m.ExternalServices, bedrock.ServiceDomain)
	}
	for _, region := range []string{bedrock.DefaultRegion, "eu-central-1", "us-gov-west-1"} {
		if host := bedrock.RuntimeHost(region); host != "bedrock-runtime."+region+".amazonaws.com" {
			t.Errorf("%s: host %s", region, host)
		}
	}
}

// checkPlatforms pins what a type serves: Claude's messages endpoint only,
// with the beta header passed to the plugin (it moves into the body). No
// type declares the openai platform: a native
// openai route would take the place of the core's conversion, and a client
// speaking an OpenAI protocol would be sent to Bedrock's /openai/v1 instead
// of InvokeModel. count_tokens is left to the core.
func checkPlatforms(t *testing.T, at manifest.AccountType) {
	t.Helper()
	if len(at.Platforms) != 1 {
		t.Fatalf("%s platforms = %+v", at.ID, at.Platforms)
	}
	claude := at.Platforms[0]
	if claude.Platform != "anthropic" || !slices.Equal(claude.Endpoints, []string{"messages"}) || !slices.Equal(claude.PassHeaders, []string{"anthropic-beta"}) {
		t.Errorf("%s anthropic platform = %+v", at.ID, claude)
	}
	if len(claude.Usage) != 0 || len(claude.StreamOnly) != 0 {
		t.Errorf("%s overrides usage or streamOnly", at.ID)
	}
}

// defaultModelMapping is the mapping every type prefills: Anthropic's model
// names to Bedrock model ids without a geography prefix (the account's
// prefix setting adds it; geoOnly is the exception). The ids are the
// bedrock-runtime model ids of the AWS model cards
// (https://docs.aws.amazon.com/bedrock/latest/userguide/model-cards-anthropic.html,
// read 2026-11-17): models up to Opus 4.6 carry a version suffix, the later
// ones do not. Models AWS retired or no longer lists are not prefilled:
// Sonnet 4 (end of life 2026-10-14) and Opus 4 (no model card).
var defaultModelMapping = map[string]string{
	"claude-fable-5-2":           "anthropic.claude-fable-5-2",
	"claude-fable-5-1":           "anthropic.claude-fable-5-1",
	"claude-fable-5":             "anthropic.claude-fable-5",
	"claude-opus-5-5":            "anthropic.claude-opus-5-5",
	"claude-opus-5":              "anthropic.claude-opus-5",
	"claude-opus-4-8":            "anthropic.claude-opus-4-8",
	"claude-opus-4-7":            "anthropic.claude-opus-4-7",
	"claude-opus-4-6":            "anthropic.claude-opus-4-6-v1",
	"claude-opus-4-5":            "anthropic.claude-opus-4-5-20251101-v1:0",
	"claude-opus-4-5-20251101":   "anthropic.claude-opus-4-5-20251101-v1:0",
	"claude-opus-4-1":            "us.anthropic.claude-opus-4-1-20250805-v1:0",
	"claude-sonnet-5-5":          "anthropic.claude-sonnet-5-5",
	"claude-sonnet-5":            "anthropic.claude-sonnet-5",
	"claude-sonnet-4-6":          "anthropic.claude-sonnet-4-6",
	"claude-sonnet-4-5":          "anthropic.claude-sonnet-4-5-20250929-v1:0",
	"claude-sonnet-4-5-20250929": "anthropic.claude-sonnet-4-5-20250929-v1:0",
	"claude-haiku-5-5":           "anthropic.claude-haiku-5-5",
	"claude-haiku-4-5":           "anthropic.claude-haiku-4-5-20251001-v1:0",
	"claude-haiku-4-5-20251001":  "anthropic.claude-haiku-4-5-20251001-v1:0",
}

// geoOnly are the default models without a global inference profile, with
// the prefix their mapping names instead (an id that has one is sent as it
// is). Opus 4.1 has the US profile only
// (https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-opus-4-1.html).
var geoOnly = map[string]string{"claude-opus-4-1": "us."}

// retiredModels are names the mapping must not bring back.
var retiredModels = []string{"claude-sonnet-4-20250514", "claude-opus-4-20250514"}

// checkModels pins the model defaults: the three types prefill the same
// models and the same mapping, clients name Claude models the Anthropic way
// and every one of them is mapped.
func checkModels(t *testing.T, at, first manifest.AccountType) {
	t.Helper()
	if !slices.Equal(at.DefaultModels, first.DefaultModels) || !maps.Equal(at.DefaultModelMapping, first.DefaultModelMapping) {
		t.Errorf("%s model defaults differ from %s's", at.ID, first.ID)
	}
	if !maps.Equal(at.DefaultModelMapping, defaultModelMapping) {
		t.Errorf("%s defaultModelMapping = %v", at.ID, at.DefaultModelMapping)
	}
	if !slices.Equal(slices.Sorted(slices.Values(at.DefaultModels)), slices.Sorted(maps.Keys(defaultModelMapping))) {
		t.Errorf("%s defaultModels = %v, the mapping has %d", at.ID, at.DefaultModels, len(defaultModelMapping))
	}
	for _, model := range at.DefaultModels {
		// A dated name and its alias lead to the same Bedrock model. The id
		// is plain, so that the account's prefix (global by default) picks
		// the inference profile, unless the model has no global profile.
		id := at.DefaultModelMapping[model]
		if !strings.HasPrefix(model, "claude-") || !strings.HasPrefix(id, geoOnly[model]+"anthropic."+model) {
			t.Errorf("%s: default model %q maps to %q", at.ID, model, id)
		}
	}
	for _, model := range retiredModels {
		if _, ok := at.DefaultModelMapping[model]; ok || slices.Contains(at.DefaultModels, model) {
			t.Errorf("%s prefills %s, which AWS no longer serves", at.ID, model)
		}
	}
	// The account test invokes what a new account's claude-haiku-4-5 request
	// does: the mapping's id behind the default prefix. It names the profile
	// itself, so an account with another prefix, or none, tests the same id.
	if want := bedrock.DefaultProfilePrefix + "." + at.DefaultModelMapping["claude-haiku-4-5"]; bedrock.DefaultTestModel != want {
		t.Errorf("%s: the default test model is %s, the default mapping and prefix lead to %s", at.ID, bedrock.DefaultTestModel, want)
	}
}

// TestManifestServes runs the plugin with its embedded manifest.
func TestManifestServes(t *testing.T) {
	h := pluginsdktest.Start(t, bedrock.New(), pluginsdktest.Options{Manifest: manifestJSON})
	if h.Info.GetPluginKey() != "bedrock" || h.Info.GetVersion() != bedrock.Version {
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
