package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/plugins/newapi/internal/newapi"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

const (
	key     = "newapi"
	version = "0.1.0"
)

// TestManifest runs manifest.json through the checks the core runs before
// it installs a package and pins what the package keeps: an
// account-type-only plugin whose apikey serves every endpoint of the
// built-in openai and anthropic platforms, gemini without countTokens (the
// site answers it 404) and the TypeSafe plugin's typesafe platform, each
// with the platform's own client headers; an address the caller sets
// (account:relay guards it), a balance, and its own process that reads
// only its own accounts and opens no connection.
func TestManifest(t *testing.T) {
	m := load(t)
	if m.Key != key || m.Version != version || m.Publisher != "sub2api" || m.Runtime != manifest.RuntimeGRPC || m.Icon != "text:N" {
		t.Fatalf("identity = %s %s %s %s %s", m.Key, m.Version, m.Publisher, m.Runtime, m.Icon)
	}
	if m.Entry.GRPC == nil || m.Entry.GRPC.Binaries == "" || m.HostCompat != ">=0.1.304 <0.2.0" {
		t.Fatalf("entry/hostCompat = %+v %s", m.Entry, m.HostCompat)
	}
	// WebSocket: the openai preset's responses_ws route.
	if caps := m.Capabilities; len(caps) != 2 || caps[0].ID != manifest.CapPlatformAdapter || caps[1].ID != manifest.CapPlatformWebSocket {
		t.Fatalf("capabilities = %+v", caps)
	}
	if len(m.Platforms) != 0 || len(m.AccountTypes) != 1 || len(m.ExternalServices) != 0 {
		t.Fatalf("platforms %+v accountTypes %+v externalServices %v, want one account type only", m.Platforms, m.AccountTypes, m.ExternalServices)
	}
	at := m.AccountTypes[0]
	want := []manifest.AccountPlatform{
		{Platform: "openai"},
		{Platform: "anthropic"},
		{Platform: "gemini", Endpoints: []string{"generate_content", "stream_generate_content"}},
		{Platform: "typesafe"},
	}
	if at.ID != newapi.TypeAPIKey || !reflect.DeepEqual(at.Platforms, want) {
		t.Fatalf("account type %s platforms = %+v, want %+v", at.ID, at.Platforms, want)
	}
	if !slices.Equal(at.SensitiveFields, []string{"api_key"}) || !slices.Equal(at.SettingsFields, []string{"base_url"}) {
		t.Errorf("sensitive/settings fields = %v %v", at.SensitiveFields, at.SettingsFields)
	}
	// The address is the operator's (no guardedSettings): a custom
	// upstream, which takes account:relay (CONTRACTS §21.3.1).
	if !slices.Equal(at.UpstreamFields, []string{"base_url"}) || len(at.GuardedSettings) != 0 || !at.CustomUpstream() {
		t.Errorf("upstreamFields = %v guardedSettings = %v custom = %v", at.UpstreamFields, at.GuardedSettings, at.CustomUpstream())
	}
	if !reflect.DeepEqual(at.Balance, &manifest.AccountBalance{UpdateInterval: 600}) {
		t.Errorf("balance = %+v", at.Balance)
	}
	// A site has no fixed model table: the operator fetches it.
	if len(at.DefaultModels) != 0 {
		t.Errorf("defaultModels = %v", at.DefaultModels)
	}
	perms := map[string]manifest.HostPermission{}
	for _, perm := range m.HostPermissions {
		if perm.Reason["en"] == "" || perm.Reason["zh"] == "" {
			t.Errorf("host permission %s needs an en and zh reason", perm.ID)
		}
		perms[perm.ID] = perm
	}
	// No net, no files: the core sends every request. ui.native: the account form.
	if len(perms) != 2 || perms["platform.register"].ID == "" || perms["ui.native"].ID == "" {
		t.Errorf("host permissions = %v", perms)
	}
}

// TestManifestServes runs the plugin with its embedded manifest.
func TestManifestServes(t *testing.T) {
	h := pluginsdktest.Start(t, newapi.New(), pluginsdktest.Options{Manifest: manifestJSON})
	if h.Info.GetPluginKey() != key || h.Info.GetVersion() != version || !slices.Contains(h.Info.GetCapabilities(), manifest.CapPlatformWebSocket) {
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
