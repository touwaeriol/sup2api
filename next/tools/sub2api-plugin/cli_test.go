package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pkgsig"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
)

func runOK(t *testing.T, args ...string) string {
	t.Helper()
	var out, errb bytes.Buffer
	if code := run(args, &out, &errb); code != 0 {
		t.Fatalf("%v: exit %d\nstdout: %s\nstderr: %s", args, code, out.String(), errb.String())
	}
	return out.String()
}

func runFail(t *testing.T, args ...string) string {
	t.Helper()
	var out, errb bytes.Buffer
	if code := run(args, &out, &errb); code == 0 {
		t.Fatalf("%v: expected failure\nstdout: %s", args, out.String())
	}
	return errb.String()
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const demoManifest = `{
  "apiVersion": 1, "key": "demo", "name": {"en": "Demo", "zh": "演示"}, "version": "0.1.0",
  "publisher": "tester", "runtime": "grpc", "entry": {"grpc": {"binaries": "runtimes/{os}-{arch}/plugin"}},
  "hostCompat": ">=0.1.0 <0.2.0", "hostUICompat": ">=0.1.0 <0.2.0", "capabilities": [{"id": "http.routes.v1"}],
  "database": {"migrations": "migrations/"},
  "hostPermissions": [{"id": "db.schema"}, {"id": "ui.native"}],
  "ui": {"native": {"entry": "ui/native/entry.js"}, "settings": {"mode": "native", "component": "DemoSettings", "secrets": []}}
}`

// demoPlugin creates a plugin directory with fake runtimes.
func demoPlugin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "manifest.json"), demoManifest)
	writeFile(t, filepath.Join(dir, "forms", "s.json"), `{}`)
	writeFile(t, filepath.Join(dir, "assets", "brand.svg"), `<svg/>`)
	writeFile(t, filepath.Join(dir, "assets", "LICENSE.txt"), `license notice`)
	writeFile(t, filepath.Join(dir, "migrations", "0001_init.sql"), `CREATE TABLE plg__tester__demo__t (id int);`)
	writeFile(t, filepath.Join(dir, "ui", "native", "src", "main.ts"), `source, not packed`)
	writeFile(t, filepath.Join(dir, "ui", "native", "dist", "entry.js"), `export function register() {}`)
	writeFile(t, filepath.Join(dir, "ui", "native", "dist", "assets", "a.css"), `.a{}`)
	writeFile(t, filepath.Join(dir, "ui", "iframe", "index.html"), `<html></html>`)
	writeFile(t, filepath.Join(dir, "i18n", "en.json"), `{}`)
	writeFile(t, filepath.Join(dir, "runtimes", "linux-amd64", "plugin"), "ELF-amd64")
	writeFile(t, filepath.Join(dir, "runtimes", "linux-arm64", "plugin"), "ELF-arm64")
	writeFile(t, filepath.Join(dir, ".hidden"), "skip me")
	writeFile(t, filepath.Join(dir, "testdata", "v2", "manifest.patch.json"), `{"version": "0.2.0", "name": {"zh": null}}`)
	writeFile(t, filepath.Join(dir, "testdata", "v2", "migrations", "0002_more.sql"), `ALTER TABLE plg__tester__demo__t ADD COLUMN x int;`)
	return dir
}

func TestPackSignVerify(t *testing.T) {
	dir, market := demoPlugin(t), t.TempDir()
	priv, pub := keygenDev(t)
	pkg1 := strings.TrimSpace(runOK(t, "pack", "--dir", dir, "--out-dir", market))
	if filepath.Base(pkg1) != "demo-0.1.0.s2plugin" {
		t.Fatalf("pkg1 = %s", pkg1)
	}
	checkPackedFiles(t, pkg1)
	// Deterministic output.
	again := filepath.Join(t.TempDir(), "again.s2plugin")
	runOK(t, "pack", "--dir", dir, "--out", again)
	a, _ := os.ReadFile(pkg1)
	b, _ := os.ReadFile(again)
	if !bytes.Equal(a, b) {
		t.Error("pack output is not deterministic")
	}
	pkg2 := checkOverlayPack(t, dir, market)
	checkSignVerify(t, priv, pub, pkg1, pkg2)
	checkTamperDetected(t, pub, pkg1)
}

// keygenDev creates the key dev-1 and returns its private and public files.
func keygenDev(t *testing.T) (string, string) {
	t.Helper()
	keys := t.TempDir()
	out := runOK(t, "keygen", "--key-id", "dev-1", "--out", keys)
	if !strings.Contains(out, "public_key:") {
		t.Fatalf("keygen output: %s", out)
	}
	runFail(t, "keygen", "--key-id", "dev-1", "--out", keys) // no overwrite
	return filepath.Join(keys, "dev-1.key"), filepath.Join(keys, "dev-1.pub")
}

func checkPackedFiles(t *testing.T, pkg string) {
	t.Helper()
	files, err := readPackage(pkg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"assets/brand.svg", "assets/LICENSE.txt", "manifest.json", "migrations/0001_init.sql", "ui/native/entry.js",
		"ui/native/assets/a.css", "ui/iframe/index.html", "i18n/en.json", "runtimes/linux-amd64/plugin", "runtimes/linux-arm64/plugin"} {
		if _, ok := files[want]; !ok {
			t.Errorf("missing %s", want)
		}
	}
	for p := range files {
		if strings.Contains(p, "src/") || strings.Contains(p, ".hidden") || strings.HasPrefix(p, "testdata") || strings.Contains(p, "dist/") || strings.HasPrefix(p, "forms/") {
			t.Errorf("unexpected %s", p)
		}
	}
	zr, _ := zip.OpenReader(pkg)
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == "runtimes/linux-amd64/plugin" && f.Mode().Perm() != 0o755 {
			t.Errorf("runtime mode = %v", f.Mode())
		}
	}
}

// checkOverlayPack packs 0.2.0: an extra migration and a patched manifest.
func checkOverlayPack(t *testing.T, dir, market string) string {
	t.Helper()
	pkg2 := strings.TrimSpace(runOK(t, "pack", "--dir", dir, "--overlay", "testdata/v2", "--out-dir", market))
	files2, _ := readPackage(pkg2)
	if _, ok := files2["migrations/0002_more.sql"]; !ok || filepath.Base(pkg2) != "demo-0.2.0.s2plugin" {
		t.Fatalf("overlay package %s: %v", pkg2, keysOf(files2))
	}
	var m2 map[string]any
	_ = json.Unmarshal(files2["manifest.json"], &m2)
	if m2["version"] != "0.2.0" || m2["name"].(map[string]any)["zh"] != nil || m2["key"] != "demo" {
		t.Fatalf("patched manifest = %v", m2)
	}
	return pkg2
}

// checkSignVerify: unsigned verify fails; sign; verify ok; re-signing
// replaces the signature.
func checkSignVerify(t *testing.T, priv, pub, pkg1, pkg2 string) {
	t.Helper()
	runFail(t, "verify", "--pub", pub, pkg1)
	runOK(t, "sign", pkg1, pkg2, "--key", priv, "--key-id", "dev-1")
	if out := runOK(t, "verify", "--pub", pub, pkg1, pkg2); strings.Count(out, "OK ") != 2 {
		t.Fatalf("verify output: %s", out)
	}
	files, _ := readPackage(pkg1)
	var sig pkgsig.Signature
	_ = json.Unmarshal(files[pkgsig.SignatureFile], &sig)
	if sig.Publisher != "tester" || sig.KeyID != "dev-1" || sig.Algorithm != "ed25519" {
		t.Fatalf("signature = %+v", sig)
	}
	runOK(t, "sign", "--key", priv, "--key-id", "dev-1", pkg1)
	runOK(t, "verify", "--pub", pub, pkg1)
}

// checkTamperDetected: a changed file or another key fails verification.
func checkTamperDetected(t *testing.T, pub, pkg string) {
	t.Helper()
	files, _ := readPackage(pkg)
	files["runtimes/linux-amd64/plugin"] = []byte("evil")
	tampered := filepath.Join(t.TempDir(), "tampered.s2plugin")
	if err := writePackage(tampered, files); err != nil {
		t.Fatal(err)
	}
	if msg := runFail(t, "verify", "--pub", pub, tampered); !strings.Contains(msg, "digest mismatch") {
		t.Fatalf("tamper message: %s", msg)
	}
	other := t.TempDir()
	runOK(t, "keygen", "--key-id", "other", "--out", other)
	runFail(t, "verify", "--pub", filepath.Join(other, "other.pub"), pkg)
}

func TestPackChecks(t *testing.T) {
	dir := demoPlugin(t)
	out := t.TempDir()
	if err := os.RemoveAll(filepath.Join(dir, "ui", "native", "dist")); err != nil {
		t.Fatal(err)
	}
	if msg := runFail(t, "pack", "--dir", dir, "--out-dir", out); !strings.Contains(msg, "native UI entry") {
		t.Fatalf("msg = %s", msg)
	}
	runOK(t, "pack", "--dir", dir, "--out-dir", out, "--allow-missing-ui")

	if err := os.RemoveAll(filepath.Join(dir, "migrations")); err != nil {
		t.Fatal(err)
	}
	if msg := runFail(t, "pack", "--dir", dir, "--out-dir", out, "--allow-missing-ui"); !strings.Contains(msg, "migrations") {
		t.Fatalf("msg = %s", msg)
	}
	writeFile(t, filepath.Join(dir, "manifest.json"), strings.Replace(demoManifest, `"runtime"`, `"runtme"`, 1))
	if msg := runFail(t, "pack", "--dir", dir, "--out-dir", out); !strings.Contains(msg, "unknown field") {
		t.Fatalf("msg = %s", msg)
	}
	runFail(t, "pack", "--dir", dir)
	runFail(t, "nope")
}

// Account types are top-level (ARCHITECTURE 6.6): their form is a native
// component, packed with the native UI, and they need platforms,
// upstreamFields, the platform adapter capability and the platform.register
// host permission. A schema form is refused.
func TestPackAccountTypes(t *testing.T) {
	const m = `{
  "apiVersion": 1, "key": "relayx", "name": {"en": "Relay"}, "version": "0.1.0",
  "publisher": "tester", "runtime": "grpc", "entry": {"grpc": {"binaries": "runtimes/{os}-{arch}/plugin"}},
  "hostCompat": ">=0.1.0 <0.2.0", "hostUICompat": "^4.0", "capabilities": [{"id": "platform.adapter.v1"}],
  "ui": {"native": {"entry": "ui/native/entry.js"}},
  "accountTypes": [{"id": "relay_key", "label": {"en": "Relay key"},
    "form": {"mode": "native", "component": "RelayKeyForm"},
    "sensitiveFields": ["api_key"], "upstreamFields": ["base_url"],
    "platforms": [{"platform": "anthropic", "passHeaders": ["anthropic-version"]}]}],
  "hostPermissions": [{"id": "platform.register"}, {"id": "ui.native"}]
}`
	dir := t.TempDir()
	out := t.TempDir()
	writeFile(t, filepath.Join(dir, "manifest.json"), m)
	writeFile(t, filepath.Join(dir, "runtimes", "linux-amd64", "plugin"), "ELF-amd64")
	writeFile(t, filepath.Join(dir, "runtimes", "linux-arm64", "plugin"), "ELF-arm64")
	if msg := runFail(t, "pack", "--dir", dir, "--out-dir", out); !strings.Contains(msg, "build ui/native first") {
		t.Fatalf("msg = %s", msg)
	}
	writeFile(t, filepath.Join(dir, "ui", "native", "dist", "entry.js"), `export function register() {}`)
	pkg := strings.TrimSpace(runOK(t, "pack", "--dir", dir, "--out-dir", out))
	files, err := readPackage(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files["ui/native/entry.js"]; !ok {
		t.Fatalf("form component not packed: %v", keysOf(files))
	}

	writeFile(t, filepath.Join(dir, "manifest.json"), strings.Replace(m, `{"mode": "native", "component": "RelayKeyForm"}`, `{"mode": "schema"}`, 1))
	if msg := runFail(t, "pack", "--dir", dir, "--out-dir", out); !strings.Contains(msg, "form mode schema is no longer supported") {
		t.Fatalf("msg = %s", msg)
	}
	// The CLI reads manifest.json strictly: the old form file keys are gone.
	writeFile(t, filepath.Join(dir, "manifest.json"), strings.Replace(m, `"component": "RelayKeyForm"`, `"component": "RelayKeyForm", "schema": "forms/k.schema.json"`, 1))
	if msg := runFail(t, "pack", "--dir", dir, "--out-dir", out); !strings.Contains(msg, `unknown field "schema"`) {
		t.Fatalf("msg = %s", msg)
	}

	writeFile(t, filepath.Join(dir, "manifest.json"), strings.Replace(m, `[{"platform": "anthropic", "passHeaders": ["anthropic-version"]}]`, `[]`, 1))
	if msg := runFail(t, "pack", "--dir", dir, "--out-dir", out); !strings.Contains(msg, "at least one platform") {
		t.Fatalf("msg = %s", msg)
	}
	writeFile(t, filepath.Join(dir, "manifest.json"), strings.Replace(m, `"passHeaders"`, `"usage": {"openai.chat": {"semantics": "inclusive"}}, "passHeaders"`, 1))
	if msg := runFail(t, "pack", "--dir", dir, "--out-dir", out); !strings.Contains(msg, "does not belong to platform") {
		t.Fatalf("msg = %s", msg)
	}
	writeFile(t, filepath.Join(dir, "manifest.json"), strings.Replace(m, `"platform.adapter.v1"`, `"http.routes.v1"`, 1))
	if msg := runFail(t, "pack", "--dir", dir, "--out-dir", out); !strings.Contains(msg, "platform.adapter.v1") {
		t.Fatalf("msg = %s", msg)
	}
	writeFile(t, filepath.Join(dir, "manifest.json"), strings.Replace(m, `{"id": "platform.register"}, `, ``, 1))
	if msg := runFail(t, "pack", "--dir", dir, "--out-dir", out); !strings.Contains(msg, "platform.register") {
		t.Fatalf("msg = %s", msg)
	}
	// The pre-6.6 layouts (protocols on account types, top-level gateway and
	// platform) are rejected.
	for _, old := range []string{
		strings.Replace(m, `"platforms": [{"platform"`, `"protocols": [{"protocol"`, 1),
		strings.Replace(m, `"accountTypes": [`, `"gateway": {"endpoints": []}, "x": [`, 1),
		strings.Replace(m, `"accountTypes": [`, `"platform": {"id": "x", "protocols": []}, "x": [`, 1),
	} {
		writeFile(t, filepath.Join(dir, "manifest.json"), old)
		if msg := runFail(t, "pack", "--dir", dir, "--out-dir", out); !strings.Contains(msg, "unknown field") {
			t.Fatalf("msg = %s", msg)
		}
	}
}

// A manifest of an older plugin contract - runtime "none", a declared
// upstream, request fields, an endpoint body limit, a removed capability or
// a removed account grant - gets the host's one outdated-contract message
// from every command that reads it, not a field or decode error.
func TestOutdatedContract(t *testing.T) {
	want := protocol.OutdatedContractMessage("tester/demo", "0.1.0", protocol.FirstCoreVersion)
	grpc := `"runtime": "grpc"`
	for name, old := range map[string]string{
		"runtime none":       strings.Replace(demoManifest, grpc, `"runtime": "none"`, 1),
		"upstream":           strings.Replace(demoManifest, `"database"`, `"accountTypes": [{"id": "k", "upstream": {"preset": "anthropic"}}], "database"`, 1),
		"request fields":     strings.Replace(demoManifest, `"database"`, `"platforms": [{"id": "p", "requestFields": ["model"]}], "database"`, 1),
		"max body bytes":     strings.Replace(demoManifest, `"database"`, `"platforms": [{"id": "p", "endpoints": [{"id": "e", "request": {"maxBodyBytes": 1}}]}], "database"`, 1),
		"execute capability": strings.Replace(demoManifest, `{"id": "http.routes.v1"}`, `{"id": "platform.execute.v1"}`, 1),
		"account grant":      strings.Replace(demoManifest, `{"id": "ui.native"}`, `{"id": "ui.native"}, {"id": "accounts.read"}`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			dir := demoPlugin(t)
			writeFile(t, filepath.Join(dir, "manifest.json"), old)
			for _, args := range [][]string{
				{"build", "--dir", dir},
				{"pack", "--dir", dir, "--out-dir", t.TempDir()},
			} {
				if msg := runFail(t, args...); !strings.Contains(msg, want) {
					t.Errorf("%s: %s", args[0], msg)
				}
			}
			// A package packed by an older CLI.
			pkg := filepath.Join(t.TempDir(), "old.s2plugin")
			if err := writePackage(pkg, map[string][]byte{"manifest.json": []byte(old)}); err != nil {
				t.Fatal(err)
			}
			keys := t.TempDir()
			runOK(t, "keygen", "--key-id", "k1", "--out", keys)
			if msg := runFail(t, "sign", "--key", filepath.Join(keys, "k1.key"), "--key-id", "k1", pkg); !strings.Contains(msg, want) {
				t.Errorf("sign: %s", msg)
			}
		})
	}
}

// Plugin platforms declare their endpoints (ARCHITECTURE 6.6, CONTRACTS 13).
func TestPackPlatforms(t *testing.T) {
	const m = `{
  "apiVersion": 1, "key": "videox", "name": {"en": "Video"}, "version": "0.1.0",
  "publisher": "tester", "runtime": "grpc", "entry": {"grpc": {"binaries": "runtimes/{os}-{arch}/plugin"}},
  "hostCompat": ">=0.1.0 <0.2.0", "capabilities": [{"id": "platform.adapter.v1"}],
  "platforms": [{"id": "myvideo", "label": {"en": "My video"},
    "endpoints": [
      {"id": "gen", "method": "POST", "path": "/v1/video/generations", "protocol": "myvideo.gen", "kind": "proxy",
       "auth": {"headers": ["authorization"]}, "request": {"modelPath": "model"}, "response": {"nonStream": "json"},
       "errorFormat": "plain", "billing":"usage","billingTypes":["per_request","per_token","expression"]},
      {"id": "run", "method": "POST", "path": "/v1/video/models/:model:run", "protocol": "myvideo.run", "kind": "proxy",
       "auth": {"headers": ["authorization"], "query": "key"}, "request": {"modelParam": "model"}, "response": {"nonStream": "json"},
       "errorFormat": "plain", "billing":"usage","billingTypes":["per_request","per_token","expression"]}
    ],
    "usage": {"semantics": "inclusive", "json": {"map": {"input_tokens": "usage.input"}}}}],
  "hostUICompat": "^4.0", "ui": {"native": {"entry": "ui/native/entry.js"}},
  "accountTypes": [{"id": "vkey", "label": {"en": "Key"}, "form": {"mode": "native", "component": "VideoKeyForm"},
    "upstreamFields": [], "platforms": [{"platform": "myvideo", "usage": {"myvideo.gen": {"semantics": "inclusive"}}}]}],
  "hostPermissions": [{"id": "gateway.endpoint"}, {"id": "platform.register"}, {"id": "ui.native"}]
}`
	dir := t.TempDir()
	out := t.TempDir()
	writeFile(t, filepath.Join(dir, "runtimes", "linux-amd64", "plugin"), "ELF-amd64")
	writeFile(t, filepath.Join(dir, "ui", "native", "dist", "entry.js"), `export function register() {}`)
	writeFile(t, filepath.Join(dir, "manifest.json"), m)
	runOK(t, "pack", "--dir", dir, "--out-dir", out)

	// The CLI runs the host's rules (sdk/manifest/check), so these are the
	// host's messages - including the endpoint and usage rules the core only
	// gained with the shared implementation.
	cases := []struct{ old, new, want string }{
		{`"id": "myvideo"`, `"id": "openai"`, "is a built-in platform"},
		{`{"id": "gateway.endpoint"}, `, ``, "gateway.endpoint"},
		{`"protocol": "myvideo.gen"`, `"protocol": "video.gen"`, `must be "myvideo."`},
		{`"/v1/video/generations"`, `"/api/v1/video"`, "must not start with /api"},
		{`"/v1/video/models/:model:run"`, `"/v1/video/models/x:run"`, "modelParam"},
		{`"/v1/video/generations"`, `"/v1/video/models/:id"`, "conflicts with platforms[0].endpoints[0]"},
		{`"/v1/video/generations"`, `"/v1/video/models/gemini:run"`, "conflicts"},
		{`"myvideo.gen": {`, `"myvideo.other": {`, "is not a protocol of platform"},
		{`"errorFormat": "plain", "billing":"usage","billingTypes":["per_request","per_token","expression"]},`, `"errorFormat": "xml", "billing":"usage","billingTypes":["per_request","per_token","expression"]},`, "errorFormat must be one of"},
		{`"response": {"nonStream": "json"},
       "errorFormat": "plain", "billing":"usage","billingTypes":["per_request","per_token","expression"]},`, `"response": {},
       "errorFormat": "plain", "billing":"usage","billingTypes":["per_request","per_token","expression"]},`, "response.nonStream is required"},
		{`"errorFormat": "plain", "billing":"usage","billingTypes":["per_request","per_token","expression"]},`, `"errorFormat": "plain"},`, "billing is required"},
		{`"usage": {"semantics": "inclusive", "json"`, `"usage": {"json"`, "semantics is required"},
		{`"map": {"input_tokens": "usage.input"}`, `"map": {"total_tokens": "usage.input"}`, "unknown usage field"},
		{`"map": {"input_tokens": "usage.input"}`, `"map": {"input_tokens": "usage..[["}`, "not a gjson path"},
	}
	for _, tc := range cases {
		bad := strings.Replace(m, tc.old, tc.new, 1)
		if bad == m {
			t.Fatalf("case %q does not change the manifest", tc.old)
		}
		writeFile(t, filepath.Join(dir, "manifest.json"), bad)
		if msg := runFail(t, "pack", "--dir", dir, "--out-dir", out); !strings.Contains(msg, tc.want) {
			t.Errorf("%s -> %s: msg = %s, want %q", tc.old, tc.new, msg, tc.want)
		}
	}
}

// The endpoint path rules (including overlap) are the host's and are tested
// in sdk/manifest/check; the CLI only feeds manifests to them.

func TestMergePatch(t *testing.T) {
	out, err := applyMergePatch([]byte(`{"a":1,"b":{"c":2,"d":3},"e":[1,2]}`), []byte(`{"a":null,"b":{"c":5},"e":[3],"f":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	want := `{"b":{"c":5,"d":3},"e":[3],"f":"x"}`
	gb, _ := json.Marshal(got)
	if string(gb) != want {
		t.Fatalf("got %s want %s", gb, want)
	}
}

func TestBuildRealPlugin(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a plugin")
	}
	dir := filepath.Join("..", "..", "plugins", "anthropic")
	out := t.TempDir()
	res := runOK(t, "build", "--dir", dir, "--out", out, "--platforms", "linux/amd64", "--dev", "--overlay", "testdata/upgrade")
	bin := filepath.Join(out, "runtimes", "linux-amd64", "plugin")
	if !strings.Contains(res, bin) {
		t.Fatalf("build output = %q", res)
	}
	b, err := os.ReadFile(bin)
	if err != nil || !bytes.HasPrefix(b, []byte("\x7fELF")) {
		t.Fatalf("binary: %v", err)
	}

	// Run the local (--dev) binary under go-plugin: the effective manifest
	// version must be reported (injected with -ldflags -X).
	local := filepath.Join(out, "runtimes", runtime.GOOS+"-"+runtime.GOARCH, binaryName(runtime.GOOS))
	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig:  protocol.Handshake,
		Plugins:          protocol.PluginMap(&protocol.GRPCPlugin{}),
		Cmd:              exec.Command(local),
		AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC},
		Logger:           hclog.NewNullLogger(),
	})
	defer client.Kill()
	rpc, err := client.Client()
	if err != nil {
		t.Fatalf("start plugin: %v", err)
	}
	raw, err := rpc.Dispense(protocol.PluginName)
	if err != nil {
		t.Fatal(err)
	}
	info, err := pluginv1.NewPluginServiceClient(raw.(*protocol.Client).Conn).GetInfo(context.Background(), &pluginv1.GetInfoRequest{})
	if err != nil || info.GetPluginKey() != "anthropic" || info.GetVersion() != "0.4.1-test" {
		t.Fatalf("GetInfo = %v %v", info, err)
	}
}

func keysOf(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
