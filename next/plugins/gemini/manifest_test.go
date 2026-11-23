package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path"
	"slices"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/plugins/gemini/internal/gemini"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey/apikeytest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard"
)

// The package and its one account type.
const (
	key      = "gemini"
	version  = "0.4.1"
	platform = "gemini"
)

// TestManifest runs manifest.json through the checks the core runs before
// it installs a package and pins what the move to a gRPC process keeps: the
// identity, the account type of the built-in platform, and a type that is
// the gemini preset forwarding every request field.
func TestManifest(t *testing.T) {
	m := load(t)
	if m.Key != key || m.Version != version || m.Publisher != "sub2api" || m.Runtime != manifest.RuntimeGRPC {
		t.Fatalf("identity = %s %s %s %s", m.Key, m.Version, m.Publisher, m.Runtime)
	}
	if m.Entry.GRPC == nil || m.Entry.GRPC.Binaries == "" || m.HostCompat != ">=0.1.304 <0.2.0" {
		t.Fatalf("entry/hostCompat = %+v %s", m.Entry, m.HostCompat)
	}
	if len(m.AccountTypes) != 1 {
		t.Fatalf("accountTypes = %+v", m.AccountTypes)
	}
	at := m.AccountTypes[0]
	if at.ID != gemini.TypeAPIKey || len(at.Platforms) != 1 || at.Platforms[0].Platform != platform {
		t.Fatalf("account type %s platforms = %+v, want %s on %s", at.ID, at.Platforms, gemini.TypeAPIKey, platform)
	}
	typ := gemini.APIKey()
	if len(typ.FilterFields) != 0 {
		t.Errorf("filter fields = %v, want none (the plugin forwards every field)", typ.FilterFields)
	}
	if err := standard.Check(typ); err != nil {
		t.Error(err)
	}
	// base_url is the address field and guarded (CONTRACTS §21.3): without
	// account:settings:custom only the default upstream may be used, with or
	// without one of the version segments of the type's base URL.
	if !slices.Equal(at.UpstreamFields, []string{"base_url"}) {
		t.Errorf("upstreamFields = %v, want [base_url]", at.UpstreamFields)
	}
	def := typ.Spec.DefaultBaseURL
	if gs := at.GuardedSettings; def != "https://generativelanguage.googleapis.com" || len(gs) != 1 || gs[0].Field != "base_url" || !slices.Equal(gs[0].Allowed, []string{def}) {
		t.Fatalf("guardedSettings = %+v, want base_url -> [%s]", gs, def)
	}
	apikeytest.GuardedVersions(t, at, typ.Spec)
}

// TestManifestServes runs the plugin with its embedded manifest.
func TestManifestServes(t *testing.T) {
	h := pluginsdktest.Start(t, gemini.New(), pluginsdktest.Options{Manifest: manifestJSON})
	if h.Info.GetPluginKey() != key || h.Info.GetVersion() != version {
		t.Fatalf("info = %v", h.Info)
	}
	if !slices.Contains(h.Info.GetCapabilities(), manifest.CapPlatformAdapter) {
		t.Errorf("capabilities = %v", h.Info.GetCapabilities())
	}
}

// load decodes manifest.json strictly and checks it, with the files a
// package carries, as the core does (no warnings either).
func load(t *testing.T) manifest.Manifest {
	t.Helper()
	var m manifest.Manifest
	dec := json.NewDecoder(bytes.NewReader(manifestJSON))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("manifest.json: %v", err)
	}
	files := map[string][]byte{"manifest.json": manifestJSON}
	for _, dir := range []string{"assets"} {
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
