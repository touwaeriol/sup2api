// Package pkgtest builds .s2plugin packages for tests.
package pkgtest

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"sort"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pkgsig"
)

// Key is a publisher signing key pair.
type Key struct {
	ID   string
	Pub  ed25519.PublicKey
	Priv ed25519.PrivateKey
}

// NewKey generates an Ed25519 key.
func NewKey(id string) Key {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	return Key{ID: id, Pub: pub, Priv: priv}
}

// PubB64 returns the base64 public key.
func (k Key) PubB64() string { return base64.StdEncoding.EncodeToString(k.Pub) }

// Guard returns a manifest modelled on the guard demo plugin (hooks,
// events, jobs, database, routes, native UI, settings).
func Guard(key, version, publisher string) *manifest.Manifest {
	return &manifest.Manifest{
		APIVersion:   1,
		Key:          key,
		Name:         manifest.LocalizedText{"en": "Guard", "zh": "请求守卫"},
		Version:      version,
		Publisher:    publisher,
		Runtime:      "grpc",
		Entry:        manifest.Entry{GRPC: &manifest.GRPCEntry{Binaries: "runtimes/{os}-{arch}/plugin"}},
		HostCompat:   ">=0.1.0 <0.2.0",
		HostUICompat: "^4.0",
		Capabilities: []manifest.Capability{
			{ID: manifest.CapGatewayHook}, {ID: manifest.CapAppEvents}, {ID: manifest.CapAppJobs}, {ID: manifest.CapHTTPRoutes},
		},
		Hooks: []manifest.Hook{{
			ID: "check", Point: "gateway.request", Order: 100,
			Match: manifest.HookMatch{Protocols: []string{"anthropic.messages"}, Models: []string{"*"}},
			Needs: []string{"model", "prompt_text"}, MaxPromptBytes: 32768, TimeoutMs: 300, Failure: "open",
		}},
		Events:   &manifest.Events{Subscribe: []string{"usage.recorded"}, BatchSize: 100},
		Jobs:     []manifest.Job{{ID: "rollup", Schedule: "@every 5m", TimeoutSec: 60}, {ID: "cleanup", Schedule: "0 3 * * *"}},
		Database: &manifest.Database{Schema: "plg_" + key, Migrations: "migrations/"},
		UserPermissions: []manifest.UserPermission{
			{Key: "rules:read", Label: manifest.LocalizedText{"en": "View rules", "zh": "查看拦截规则"}},
			{Key: "rules:manage", Label: manifest.LocalizedText{"en": "Manage rules", "zh": "管理拦截规则"}},
			{Key: "stats:read", Label: manifest.LocalizedText{"en": "View stats", "zh": "查看拦截统计"}},
		},
		Routes: []manifest.Route{
			{Method: "GET", Path: "/rules", Scope: "admin", Permission: "rules:read"},
			{Method: "PUT", Path: "/rules", Scope: "admin", Permission: "rules:manage"},
		},
		UI: &manifest.UI{
			Menus:    []manifest.Menu{{ID: "guard", Section: "plugins", Label: manifest.LocalizedText{"en": "Guard"}, Page: "dashboard", Permission: "stats:read"}},
			Pages:    map[string]manifest.Page{"dashboard": {Type: "native", Component: "GuardDashboard"}},
			Slots:    []manifest.Slot{{Slot: "dashboard.widgets", Component: "BlockedTodayCard", Permission: "stats:read"}},
			Native:   &manifest.NativeUI{Entry: "ui/native/entry.js"},
			Settings: &manifest.Form{Mode: "native", Component: "GuardSettings", Secrets: []manifest.SettingsSecret{{Path: "webhook_secret"}}},
		},
		Resources: &manifest.Resources{MemoryMB: 128, CPU: 0.25},
		HostPermissions: []manifest.HostPermission{
			{ID: "kv"},
			{ID: "db.schema", Reason: manifest.LocalizedText{"en": "Store rules"}},
			{ID: "gateway.hook", Scope: map[string]any{"points": []any{"gateway.request"}, "fields": []any{"model", "prompt_text"}}},
			{ID: "events", Scope: map[string]any{"subscribe": []any{"usage.recorded"}}},
			{ID: "jobs"},
			{ID: "routes.admin"},
			{ID: "ui.menu"},
			{ID: "ui.native"},
			{ID: "net", Scope: map[string]any{"domains": []any{"hooks.example.com"}}, Optional: true},
		},
		ExternalServices: []string{"hooks.example.com"},
	}
}

// Minimal returns a manifest with no optional sections and only low-risk
// permissions (suitable for community/unsigned tests).
func Minimal(key, version, publisher string) *manifest.Manifest {
	return &manifest.Manifest{
		APIVersion:      1,
		Key:             key,
		Name:            manifest.LocalizedText{"en": key},
		Version:         version,
		Publisher:       publisher,
		Runtime:         "grpc",
		Entry:           manifest.Entry{GRPC: &manifest.GRPCEntry{Binaries: "runtimes/{os}-{arch}/plugin"}},
		HostCompat:      ">=0.1.0",
		HostPermissions: []manifest.HostPermission{{ID: "kv"}},
	}
}

// Platform returns a manifest declaring a new platform (id = key) with two
// endpoints (one reading the model from a ":param:suffix" path segment), an
// account type serving it and default prices (ARCHITECTURE 6.6).
func Platform(key, version, publisher string) *manifest.Manifest {
	return &manifest.Manifest{
		APIVersion:   1,
		Key:          key,
		Name:         manifest.LocalizedText{"en": "Video " + key},
		Version:      version,
		Publisher:    publisher,
		Runtime:      "grpc",
		Entry:        manifest.Entry{GRPC: &manifest.GRPCEntry{Binaries: "runtimes/{os}-{arch}/plugin"}},
		HostCompat:   ">=0.1.0 <0.2.0",
		HostUICompat: "^4.0",
		Capabilities: []manifest.Capability{{ID: manifest.CapPlatformAdapter}},
		Platforms: []manifest.Platform{{
			ID:    key,
			Label: manifest.LocalizedText{"en": "Video " + key},
			Endpoints: []manifest.Endpoint{
				{ID: "generate", Method: "POST", Path: "/" + key + "/v1/videos", Protocol: key + ".generate", Kind: "proxy",
					Auth: manifest.EndpointAuth{Headers: []string{"authorization"}}, Request: manifest.EndpointRequest{ModelPath: "model"},
					Response: manifest.EndpointResp{NonStream: "json"}, ErrorFormat: "plain", Billing: "usage", BillingTypes: []string{"per_request", "per_token", "expression"}},
				{ID: "status", Method: "GET", Path: "/" + key + "/v1/models/:model:status", Protocol: key + ".status", Kind: "proxy",
					Auth: manifest.EndpointAuth{Headers: []string{"authorization"}}, Request: manifest.EndpointRequest{ModelParam: "model"},
					Response: manifest.EndpointResp{NonStream: "json"}, ErrorFormat: "plain", Billing: "free"},
			},
			Usage: manifest.UsageRules{Semantics: "inclusive"},
		}},
		AccountTypes: []manifest.AccountType{{
			ID: "apikey", Label: manifest.LocalizedText{"en": "API Key"},
			Form:            manifest.Form{Mode: "native", Component: "VideoKeyForm"},
			SensitiveFields: []string{"api_key"},
			Platforms:       []manifest.AccountPlatform{{Platform: key}},
		}},
		UI: &manifest.UI{Native: &manifest.NativeUI{Entry: "ui/native/entry.js"}},
		HostPermissions: []manifest.HostPermission{
			{ID: "gateway.endpoint"},
			{ID: "platform.register"},
			{ID: "ui.native"},
		},
	}
}

// Anthropic returns a manifest modelled on the anthropic plugin: no platform
// of its own, an account type serving the built-in anthropic platform and
// default prices (ARCHITECTURE 6.6).
func Anthropic(key, version, publisher string) *manifest.Manifest {
	return &manifest.Manifest{
		APIVersion:   1,
		Key:          key,
		Name:         manifest.LocalizedText{"en": "Anthropic"},
		Version:      version,
		Publisher:    publisher,
		Runtime:      "grpc",
		Entry:        manifest.Entry{GRPC: &manifest.GRPCEntry{Binaries: "runtimes/{os}-{arch}/plugin"}},
		HostCompat:   ">=0.1.0 <0.2.0",
		HostUICompat: "^4.0",
		Capabilities: []manifest.Capability{{ID: manifest.CapPlatformAdapter}},
		AccountTypes: []manifest.AccountType{{
			ID: "apikey", Label: manifest.LocalizedText{"en": "API Key"},
			Form:            manifest.Form{Mode: "native", Component: "AnthropicKeyForm"},
			SensitiveFields: []string{"api_key"},
			Platforms:       []manifest.AccountPlatform{{Platform: manifest.PlatformAnthropic}},
		}},
		UI: &manifest.UI{Native: &manifest.NativeUI{Entry: "ui/native/entry.js"}},
		HostPermissions: []manifest.HostPermission{
			{ID: "platform.register"},
			{ID: "ui.native"},
		},
	}
}

// Files returns manifest.json plus every file the manifest references.
func Files(m *manifest.Manifest) map[string][]byte {
	mb, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		panic(err)
	}
	files := map[string][]byte{
		pkgsig.ManifestFile:           mb,
		"i18n/en.json":                []byte(`{}`),
		"runtimes/linux-amd64/plugin": []byte("\x7fELF amd64"),
		"runtimes/linux-arm64/plugin": []byte("\x7fELF arm64"),
	}
	if m.Database != nil {
		files[m.Database.Migrations+"0001_init.sql"] = []byte("CREATE TABLE rules (id bigserial PRIMARY KEY);")
	}
	if m.UI != nil && m.UI.Native != nil {
		files[m.UI.Native.Entry] = []byte("export default {}")
	}
	return files
}

// Sign adds signature.json to files.
func Sign(files map[string][]byte, publisher string, k Key) map[string][]byte {
	sig, err := pkgsig.Sign(files, publisher, k.ID, k.Priv)
	if err != nil {
		panic(err)
	}
	b, _ := json.Marshal(sig)
	out := make(map[string][]byte, len(files)+1)
	for p, c := range files {
		out[p] = c
	}
	out[pkgsig.SignatureFile] = b
	return out
}

// Zip packs files deterministically.
func Zip(files map[string][]byte) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		w, err := zw.Create(p)
		if err != nil {
			panic(err)
		}
		_, _ = w.Write(files[p])
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// Build returns a signed .s2plugin for m (unsigned when k.Priv is nil).
func Build(m *manifest.Manifest, k Key) []byte {
	files := Files(m)
	if k.Priv != nil {
		files = Sign(files, m.Publisher, k)
	}
	return Zip(files)
}
