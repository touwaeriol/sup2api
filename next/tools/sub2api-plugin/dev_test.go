package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Masterminds/semver/v3"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pkgsig"
)

func TestDevVersion(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"1.2.3", "1.2.4-dev.20261005010203"},
		{"0.1.0", "0.1.1-dev.20261005010203"},
		{"1.0.0-rc.1", "1.0.0-rc.1.dev.20261005010203"},
	} {
		got, err := devVersion(c.in, "20261005010203")
		if err != nil || got != c.want {
			t.Errorf("devVersion(%s) = %s %v, want %s", c.in, got, err, c.want)
		}
	}
	if _, err := devVersion("1.2", "1"); err == nil {
		t.Error("non-strict version accepted")
	}
	// Newer than the source version and than earlier builds, older than the
	// next release.
	a, _ := devVersion("1.2.3", "20261005010203")
	b, _ := devVersion("1.2.3", "20261005010204")
	va, vb := semver.MustParse(a), semver.MustParse(b)
	if !va.GreaterThan(semver.MustParse("1.2.3")) || !vb.GreaterThan(va) || !vb.LessThan(semver.MustParse("1.2.4")) {
		t.Fatalf("ordering: %s %s", a, b)
	}
}

func TestSetManifestVersionKeepsNumbers(t *testing.T) {
	out, err := setManifestVersion([]byte(`{"version":"1.0.0","limits":{"big":12345678901234567890,"f":1.50}}`), "1.0.1-dev.1")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte(`"version": "1.0.1-dev.1"`)) || !bytes.Contains(out, []byte("12345678901234567890")) || !bytes.Contains(out, []byte("1.50")) {
		t.Fatalf("manifest = %s", out)
	}
}

func TestDevPrepare(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a plugin")
	}
	// A package source: the demo plugin packed with its fake runtimes.
	demo := demoPlugin(t)
	demoPkg := strings.TrimSpace(runOK(t, "pack", "--dir", demo, "--out-dir", t.TempDir()))

	state := t.TempDir()
	// anthropic's native UI is built separately.
	d := &devSession{state: state, builtin: filepath.Join(state, "builtin"), work: filepath.Join(state, "work"), stderr: io.Discard, allowMissingUI: true}
	for _, p := range []string{filepath.Join("..", "..", "plugins", "anthropic"), demoPkg} {
		s, err := newDevSource(p, "")
		if err != nil {
			t.Fatal(err)
		}
		d.sources = append(d.sources, s)
	}
	// A stale package of an earlier session is removed.
	writeFile(t, filepath.Join(d.builtin, "old.s2plugin"), "stale")

	if err := d.prepare(); err != nil {
		t.Fatal(err)
	}
	first := d.versions["anthropic"]
	if !strings.Contains(first, "-dev.") || d.versions["demo"] != "0.1.0" {
		t.Fatalf("versions %v", d.versions)
	}
	entries, _ := os.ReadDir(d.builtin)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != "anthropic.s2plugin,demo.s2plugin" {
		t.Fatalf("built-in dir: %v", names)
	}
	// Each package verifies with the key the core is told to trust, and
	// each publisher has its own key.
	trusted := map[string]string{}
	for _, k := range d.officialKeys {
		id, pub, _ := strings.Cut(k, "=")
		trusted[id] = pub
	}
	if len(trusted) != 2 {
		t.Fatalf("official keys %v", d.officialKeys)
	}
	for _, name := range names {
		files, err := readPackage(filepath.Join(d.builtin, name))
		if err != nil {
			t.Fatal(err)
		}
		var sig pkgsig.Signature
		if err := json.Unmarshal(files[pkgsig.SignatureFile], &sig); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		pub, err := pkgsig.ParsePublicKey(trusted[sig.KeyID])
		if err != nil {
			t.Fatalf("%s signed with untrusted key %q", name, sig.KeyID)
		}
		delete(files, pkgsig.SignatureFile)
		if err := pkgsig.Verify(files, &sig, pub); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		m, _ := parseManifest(files[pkgsig.ManifestFile])
		if sig.Publisher != m.Publisher || m.Version != d.versions[m.Key] {
			t.Fatalf("%s: signature %+v manifest %s %s", name, sig, m.Publisher, m.Version)
		}
	}

	// The next build is newer and reuses the keys.
	time.Sleep(1100 * time.Millisecond)
	keys := append([]string{}, d.officialKeys...)
	if err := d.prepare(); err != nil {
		t.Fatal(err)
	}
	if !semver.MustParse(d.versions["anthropic"]).GreaterThan(semver.MustParse(first)) {
		t.Fatalf("rebuild %s not newer than %s", d.versions["anthropic"], first)
	}
	if strings.Join(keys, ",") != strings.Join(d.officialKeys, ",") {
		t.Fatalf("keys changed: %v -> %v", keys, d.officialKeys)
	}
}

func TestDevFingerprint(t *testing.T) {
	dir := demoPlugin(t)
	s, err := newDevSource(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	d := &devSession{sources: []*devSource{s}}
	base := d.fingerprint()
	touch := func(rel, content string) string {
		writeFile(t, filepath.Join(dir, filepath.FromSlash(rel)), content)
		return d.fingerprint()
	}
	if touch("runtimes/linux-amd64/plugin", "rebuilt") != base {
		t.Error("build output triggered a rebuild")
	}
	if touch("ui/native/src/main.ts", "edited") != base {
		t.Error("native UI source triggered a rebuild (only dist is packaged)")
	}
	if touch("node_modules/x/index.js", "dep") != base {
		t.Error("node_modules triggered a rebuild")
	}
	if touch("i18n/en.json", `{"title":"Demo"}`) == base {
		t.Error("package file change not detected")
	}
	base = d.fingerprint()
	if touch("ui/native/dist/entry.js", "export function register() { /* v2 */ }") == base {
		t.Error("native UI build not detected")
	}
}
