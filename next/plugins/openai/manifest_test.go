package main

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"path"
	"slices"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/plugins/openai/internal/openai"
	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey/apikeytest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest/conformance"
	preset "github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/openai"
)

const (
	key      = "openai"
	version  = "0.5.0"
	platform = "openai"
)

// TestManifest runs manifest.json through the checks the core runs before
// it installs a package and pins what the plugin keeps of the released
// manifest: the identity, the one account type of the built-in platform, its
// form and fields, and the guarded default upstream of the plugin's type.
func TestManifest(t *testing.T) {
	m := load(t)
	if m.Key != key || m.Version != version || m.Publisher != "sub2api" || m.Runtime != manifest.RuntimeGRPC {
		t.Fatalf("identity = %s %s %s %s", m.Key, m.Version, m.Publisher, m.Runtime)
	}
	if m.Entry.GRPC == nil || m.Entry.GRPC.Binaries == "" || m.HostCompat != ">=0.1.304 <0.2.0" {
		t.Fatalf("entry/hostCompat = %+v %s", m.Entry, m.HostCompat)
	}
	if caps := capabilityIDs(m); !slices.Equal(caps, []string{manifest.CapPlatformAdapter, manifest.CapPlatformWebSocket}) {
		t.Fatalf("capabilities = %v", caps)
	}
	if len(m.AccountTypes) != 1 {
		t.Fatalf("accountTypes = %+v", m.AccountTypes)
	}
	at := m.AccountTypes[0]
	if at.ID != openai.APIKey().ID || len(at.Platforms) != 1 || at.Platforms[0].Platform != platform {
		t.Fatalf("account type %s platforms = %+v, want apikey on %s", at.ID, at.Platforms, platform)
	}
	// The address is base_url, and guarded (below): not a custom upstream.
	if !slices.Equal(at.UpstreamFields, []string{"base_url"}) || at.CustomUpstream() {
		t.Errorf("upstreamFields = %v, custom upstream %v", at.UpstreamFields, at.CustomUpstream())
	}
	if !slices.Equal(at.SensitiveFields, []string{"api_key"}) || !slices.Equal(at.SettingsFields, []string{"base_url", "organization", "project"}) {
		t.Errorf("sensitive/settings fields = %v %v", at.SensitiveFields, at.SettingsFields)
	}
	// base_url is guarded (CONTRACTS §21.3): without account:settings:custom
	// only the default upstream of the plugin's type may be used, with or
	// without one of the version segments of the type's base URL.
	spec := openai.APIKey().Spec
	def := spec.DefaultBaseURL
	if gs := at.GuardedSettings; len(gs) != 1 || gs[0].Field != "base_url" || !slices.Equal(gs[0].Allowed, []string{def}) {
		t.Errorf("guardedSettings = %+v, want base_url -> [%s]", gs, def)
	}
	apikeytest.GuardedVersions(t, at, spec)
	if u, _ := url.Parse(def); !slices.Equal(m.ExternalServices, []string{u.Host}) {
		t.Errorf("externalServices = %v, want [%s]", m.ExternalServices, u.Host)
	}
	// The image models are a form's suggestion like the rest; the alias
	// OpenAI retires on 2026-12-01 is left out.
	for _, m := range []string{"gpt-image-2", "gpt-image-1.5", "gpt-image-1-mini"} {
		if !slices.Contains(at.DefaultModels, m) {
			t.Errorf("defaultModels lack %s", m)
		}
	}
	if slices.Contains(at.DefaultModels, "chatgpt-image-latest") {
		t.Error("defaultModels list chatgpt-image-latest")
	}
	perms := map[string]manifest.HostPermission{}
	for _, perm := range m.HostPermissions {
		perms[perm.ID] = perm
	}
	if len(perms) != 2 || perms["platform.register"].ID == "" || perms["ui.native"].ID == "" {
		t.Errorf("host permissions = %v", perms)
	}
}

func capabilityIDs(m manifest.Manifest) []string {
	ids := make([]string, len(m.Capabilities))
	for i, c := range m.Capabilities {
		ids[i] = c.ID
	}
	return ids
}

// TestManifestServes runs the plugin with its embedded manifest.
func TestManifestServes(t *testing.T) {
	h := pluginsdktest.Start(t, openai.New(), pluginsdktest.Options{Manifest: manifestJSON})
	if h.Info.GetPluginKey() != key || h.Info.GetVersion() != version {
		t.Fatalf("info = %v", h.Info)
	}
	if caps := h.Info.GetCapabilities(); !slices.Contains(caps, manifest.CapPlatformAdapter) || !slices.Contains(caps, manifest.CapPlatformWebSocket) {
		t.Errorf("capabilities = %v", caps)
	}
}

// TestConformance runs the platform plugin conformance suite over every
// protocol of the openai platform and a WebSocket turn.
func TestConformance(t *testing.T) {
	conformance.Run(t, conformance.Plugin{
		Platform:    openai.New(),
		Manifest:    manifestJSON,
		Protocols:   httpProtocols,
		Credentials: []string{"authorization"},
	}, conformanceCases()...)
}

var httpProtocols = []string{preset.ProtocolChat, preset.ProtocolResponses, preset.ProtocolEmbeddings, preset.ProtocolImages, preset.ProtocolImagesEdit}

const wsTurn = "openai.responses_ws turn"

func conformanceCases() []conformance.Case {
	account := &pluginv1.Account{Id: 1, Type: "apikey", CredentialsJson: `{"api_key":"sk-conformance-0123456789"}`,
		SettingsJson: `{"organization":"org-acct","project":"proj_acct"}`}
	var cases []conformance.Case
	for _, protocol := range httpProtocols {
		for _, stream := range []bool{false, true} {
			if stream && protocol == preset.ProtocolEmbeddings {
				continue // embeddings never stream (no streamPath)
			}
			model, field := "gpt-4o-mini", "input"
			if protocol == preset.ProtocolImages || protocol == preset.ProtocolImagesEdit {
				model, field = "gpt-image-1.5", "prompt"
			}
			body := `{"model":"` + model + `","` + field + `":"hi"}`
			if stream {
				body = `{"model":"` + model + `","` + field + `":"hi","stream":true}`
			}
			cases = append(cases, conformance.Case{Account: account, Body: []byte(body),
				Meta: &pluginv1.RequestMeta{Protocol: protocol, Model: model, Stream: stream}})
		}
	}
	return append(cases, conformance.Case{Name: wsTurn, Account: account, WebSocket: true,
		Meta: &pluginv1.RequestMeta{Protocol: preset.ProtocolResponsesWS, Model: "gpt-4o-mini", Stream: true},
		Body: []byte(`{"type":"response.create","model":"gpt-4o-mini","input":"hi"}`)})
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
