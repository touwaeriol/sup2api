package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/plugins/typesafe/internal/typesafe"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey/apikeytest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
	preset "github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/typesafe"
)

const (
	key     = "typesafe"
	version = "0.1.1"
)

// TestManifest runs manifest.json through the checks the core runs before
// it installs a package and pins what the package keeps: its own typesafe
// platform, exactly the SDK's definition (the one source a relay plugin
// serving TypeSafe reads too), one apikey type on it with the guarded
// default upstream, no balance (TypeSafe has no balance endpoint), and its
// own process that reads only its own accounts and opens no connection.
func TestManifest(t *testing.T) {
	m := load(t)
	if m.Key != key || m.Version != version || m.Publisher != "sub2api" || m.Runtime != manifest.RuntimeGRPC || m.Icon != "assets/typesafeai.svg" {
		t.Fatalf("identity = %s %s %s %s %s", m.Key, m.Version, m.Publisher, m.Runtime, m.Icon)
	}
	if m.Entry.GRPC == nil || m.Entry.GRPC.Binaries == "" || m.HostCompat != ">=0.1.304 <0.2.0" {
		t.Fatalf("entry/hostCompat = %+v %s", m.Entry, m.HostCompat)
	}
	if len(m.Capabilities) != 1 || m.Capabilities[0].ID != manifest.CapPlatformAdapter {
		t.Fatalf("capabilities = %+v", m.Capabilities)
	}
	if len(m.Platforms) != 1 || !reflect.DeepEqual(m.Platforms[0], preset.Platform()) {
		t.Fatalf("platforms = %+v, want exactly typesafe.Platform() %+v", m.Platforms, preset.Platform())
	}
	if len(m.AccountTypes) != 1 {
		t.Fatalf("accountTypes = %+v", m.AccountTypes)
	}
	at := m.AccountTypes[0]
	if at.ID != typesafe.TypeAPIKey || !reflect.DeepEqual(at.Platforms, []manifest.AccountPlatform{{Platform: preset.PlatformID}}) {
		t.Fatalf("account type %s platforms = %+v, want apikey on %s with its client headers", at.ID, at.Platforms, preset.PlatformID)
	}
	if !slices.Equal(at.SensitiveFields, []string{"api_key"}) || !slices.Equal(at.SettingsFields, []string{"base_url"}) {
		t.Errorf("sensitive/settings fields = %v %v", at.SensitiveFields, at.SettingsFields)
	}
	// base_url is guarded (CONTRACTS §21.3): without account:settings:custom
	// only the official address may be used, so it is no custom upstream.
	if !slices.Equal(at.UpstreamFields, []string{"base_url"}) || at.CustomUpstream() {
		t.Errorf("upstreamFields = %v, custom upstream %v", at.UpstreamFields, at.CustomUpstream())
	}
	// The official address may be used with or without one of the version
	// segments of the type's base URL.
	if gs := at.GuardedSettings; len(gs) != 1 || gs[0].Field != "base_url" || !slices.Equal(gs[0].Allowed, []string{preset.DefaultBaseURL}) {
		t.Errorf("guardedSettings = %+v, want base_url -> [%s]", gs, preset.DefaultBaseURL)
	}
	apikeytest.GuardedVersions(t, at, typesafe.APIKey().Spec)
	if u, _ := url.Parse(preset.DefaultBaseURL); !slices.Equal(m.ExternalServices, []string{u.Host}) {
		t.Errorf("externalServices = %v, want [%s]", m.ExternalServices, u.Host)
	}
	if at.Balance != nil {
		t.Errorf("balance = %+v, TypeSafe has no balance endpoint", at.Balance)
	}
	if !slices.Equal(at.DefaultModels, []string{"jev-latest", "jev-1.14.0", "jev-1.13.0", "jev-preview"}) {
		t.Errorf("defaultModels = %v", at.DefaultModels)
	}
	perms := map[string]manifest.HostPermission{}
	for _, perm := range m.HostPermissions {
		if perm.Reason["en"] == "" || perm.Reason["zh"] == "" {
			t.Errorf("host permission %s needs an en and zh reason", perm.ID)
		}
		perms[perm.ID] = perm
	}
	// No net: the core sends every request. ui.native: the account form.
	if len(perms) != 3 || perms["platform.register"].ID == "" || perms["gateway.endpoint"].ID == "" || perms["ui.native"].ID == "" {
		t.Errorf("host permissions = %v", perms)
	}
}

// TestManifestServes runs the plugin with its embedded manifest.
func TestManifestServes(t *testing.T) {
	h := pluginsdktest.Start(t, typesafe.New(), pluginsdktest.Options{Manifest: manifestJSON})
	if h.Info.GetPluginKey() != key || h.Info.GetVersion() != version || !slices.Contains(h.Info.GetCapabilities(), manifest.CapPlatformAdapter) {
		t.Fatalf("info = %v", h.Info)
	}
}

// load decodes manifest.json strictly and checks it, with the files the
// package carries, without errors or warnings.
func load(t *testing.T) manifest.Manifest {
	t.Helper()
	var m manifest.Manifest
	dec := json.NewDecoder(bytes.NewReader(manifestJSON))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("manifest.json: %v", err)
	}
	files := packageFiles(t)
	if err := check.Validate(&m, files, check.ValidateOptions{Tooling: true}); err != nil {
		t.Fatal(err)
	}
	return m
}

// packageFiles are the files a package carries: everything but Go sources
// and dot files.
func packageFiles(t *testing.T) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case p != "." && strings.HasPrefix(d.Name(), "."):
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		case d.IsDir() || !d.Type().IsRegular() || strings.HasSuffix(p, ".go"):
			return nil
		}
		b, err := os.ReadFile(p)
		files[filepath.ToSlash(p)] = b
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
