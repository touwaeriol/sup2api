package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestPackRefusesToOverwriteExistingIdentity(t *testing.T) {
	dir, market := demoPlugin(t), t.TempDir()
	pkg := strings.TrimSpace(runOK(t, "pack", "--dir", dir, "--out-dir", market))
	original, err := os.ReadFile(pkg)
	if err != nil {
		t.Fatal(err)
	}
	// The overlay adds a migration but accidentally keeps the release version.
	writeFile(t, filepath.Join(dir, "testdata", "v2", "manifest.patch.json"), `{}`)
	for _, output := range [][]string{{"--out-dir", market}, {"--out", pkg}} {
		args := append([]string{"pack", "--dir", dir, "--overlay", "testdata/v2"}, output...)
		if msg := runFail(t, args...); !strings.Contains(msg, "refusing to overwrite existing package") {
			t.Fatalf("collision error = %s", msg)
		}
		got, err := os.ReadFile(pkg)
		if err != nil || !bytes.Equal(got, original) {
			t.Fatalf("colliding fixture changed the release: %v", err)
		}
	}
}

func TestConcurrentPackCannotReplaceWinner(t *testing.T) {
	dir, market := demoPlugin(t), t.TempDir()
	start := make(chan struct{})
	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			<-start
			var out, errOut bytes.Buffer
			codes <- run([]string{"pack", "--dir", dir, "--out-dir", market}, &out, &errOut)
		})
	}
	close(start)
	wg.Wait()
	close(codes)
	succeeded := 0
	for code := range codes {
		if code == 0 {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("%d concurrent packs succeeded, want exactly one", succeeded)
	}
	if _, err := readPackage(filepath.Join(market, "demo-0.1.0.s2plugin")); err != nil {
		t.Fatalf("winner is not a complete package: %v", err)
	}
}

// Use the real manifests and overlay. The image's built-in selector takes the
// package matching the release manifest version, which must never be a fixture.
func TestAnthropicReleaseAndUpgradeFixtureRemainDistinct(t *testing.T) {
	dir := filepath.Join("..", "..", "plugins", "anthropic")
	market, runtimes := t.TempDir(), t.TempDir()
	for _, platform := range []string{"linux-amd64", "linux-arm64"} {
		writeFile(t, filepath.Join(runtimes, "runtimes", platform, "plugin"), "fake runtime for package contents")
	}
	release := strings.TrimSpace(runOK(t, "pack", "--dir", dir, "--runtimes", runtimes, "--out-dir", market))
	before, err := os.ReadFile(release)
	if err != nil {
		t.Fatal(err)
	}
	fixture := strings.TrimSpace(runOK(t, "pack", "--dir", dir, "--runtimes", runtimes,
		"--overlay", "testdata/upgrade", "--out-dir", market))
	var shipped struct {
		Version string `json:"version"`
	}
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil || json.Unmarshal(raw, &shipped) != nil || shipped.Version == "" {
		t.Fatal("cannot read the release manifest version", err)
	}
	if release == fixture || filepath.Base(release) != "anthropic-"+shipped.Version+".s2plugin" || filepath.Base(fixture) != "anthropic-0.3.2-test.s2plugin" {
		t.Fatalf("release=%s fixture=%s", release, fixture)
	}
	after, _ := os.ReadFile(release)
	if !bytes.Equal(before, after) {
		t.Fatal("packaging the upgrade fixture changed the release")
	}
	for _, tc := range []struct {
		path        string
		wantFixture bool
	}{{release, false}, {fixture, true}} {
		files, err := readPackage(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		_, hasMigration := files["migrations/0002_add_family.sql"]
		if hasMigration != tc.wantFixture {
			t.Fatalf("%s: fixture migration presence=%v", tc.path, hasMigration)
		}
		var m map[string]any
		if err := json.Unmarshal(files["manifest.json"], &m); err != nil {
			t.Fatal(err)
		}
		if (m["version"] == "0.3.2-test") != tc.wantFixture {
			t.Fatalf("unexpected packaged manifest: %s", files["manifest.json"])
		}
	}
}
