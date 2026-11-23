package registry

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pkgsig"
	pluginpkg "github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry/registrytest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

func zipFiles(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(files[n])
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// signedPackage returns a package signed with an official root key; with
// tamper the binary is changed after signing.
func signedPackage(t *testing.T, key, version string, tamper bool) (data []byte, rootKey string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	m := registrytest.Manifest(key, version)
	m.Publisher = "sub2api"
	mj, _ := json.Marshal(m)
	bin := "runtimes/" + goruntime.GOOS + "-" + goruntime.GOARCH + "/plugin"
	files := map[string][]byte{pkgsig.ManifestFile: mj, bin: []byte("binary")}
	sig, err := pkgsig.Sign(files, "sub2api", "root-1", priv)
	if err != nil {
		t.Fatal(err)
	}
	sj, _ := json.Marshal(sig)
	files[pkgsig.SignatureFile] = sj
	if tamper {
		files[bin] = []byte("evil binary")
	}
	return zipFiles(t, files), "root-1=" + base64.StdEncoding.EncodeToString(pub)
}

func pkgFor(dir string, data []byte, key, version string) *Package {
	sum := sha256.Sum256(data)
	p := &Package{Key: key, Version: version, SHA256: hex.EncodeToString(sum[:])}
	p.Dir = filepath.Join(dir, key, p.VersionHash())
	return p
}

func TestMaterializeRejectsBadSignature(t *testing.T) {
	data, root := signedPackage(t, "sig", "1.0.0", true)
	trust, err := pluginpkg.NewTrustStore([]string{root}, false)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	// No version row: the package is verified by its signature, which fails.
	s := NewPackages(nil, dir, WithVerifier(TrustVerifier(trust, testutil.DB(t).Gorm, pluginpkg.Limits{})))
	p := pkgFor(dir, data, "sig", "1.0.0")
	err = s.materialize(context.Background(), p, func() ([]byte, error) { return data, nil })
	if err == nil || !strings.Contains(err.Error(), "signature verification failed") {
		t.Fatalf("tampered package accepted: %v", err)
	}
	if p.zr != nil || p.file != nil {
		t.Fatal("rejected package was opened")
	}

	// A cached copy on disk is verified too (second load, no download).
	calls := 0
	err = s.materialize(context.Background(), pkgFor(dir, data, "sig", "1.0.0"), func() ([]byte, error) {
		calls++
		return data, nil
	})
	if err == nil || calls != 0 {
		t.Fatalf("cached tampered package: err=%v fetches=%d", err, calls)
	}
}

func TestMaterializeUnsignedPolicy(t *testing.T) {
	m := registrytest.Manifest("uns", "1.0.0")
	data := registrytest.Package(t, m, []byte("bin"), nil)
	dir := t.TempDir()

	db := testutil.DB(t).Gorm
	strict, _ := pluginpkg.NewTrustStore(nil, false)
	s := NewPackages(nil, dir, WithVerifier(TrustVerifier(strict, db, pluginpkg.Limits{})))
	if err := s.materialize(context.Background(), pkgFor(dir, data, "uns", "1.0.0"),
		func() ([]byte, error) { return data, nil }); err == nil {
		t.Fatal("unsigned package accepted although unsigned plugins are not allowed")
	}

	lax, _ := pluginpkg.NewTrustStore(nil, true)
	s = NewPackages(nil, dir, WithVerifier(TrustVerifier(lax, db, pluginpkg.Limits{})))
	p := pkgFor(dir, data, "uns", "1.0.0")
	if err := s.materialize(context.Background(), p, func() ([]byte, error) { return data, nil }); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if !p.Has("manifest.json") {
		t.Fatal("package not opened")
	}

	// Manifest identity must match the requested version.
	s = NewPackages(nil, t.TempDir(), WithVerifier(TrustVerifier(lax, db, pluginpkg.Limits{})))
	other := pkgFor(s.dataDir, data, "uns", "2.0.0")
	if err := s.materialize(context.Background(), other, func() ([]byte, error) { return data, nil }); err == nil {
		t.Fatal("package of another version accepted")
	}
}

func TestMaterializeVerifierErrorAndSHA(t *testing.T) {
	data := registrytest.Package(t, registrytest.Manifest("v", "1.0.0"), []byte("bin"), nil)
	dir := t.TempDir()
	boom := errors.New("boom")
	var seen []byte
	s := NewPackages(nil, dir, WithVerifier(func(_ context.Context, key, version string, b []byte) error {
		seen = b
		return boom
	}))
	err := s.materialize(context.Background(), pkgFor(dir, data, "v", "1.0.0"), func() ([]byte, error) { return data, nil })
	if !errors.Is(err, boom) || !bytes.Equal(seen, data) {
		t.Fatalf("err=%v", err)
	}
	// sha256 mismatch is still caught before the verifier runs.
	seen = nil
	bad := pkgFor(t.TempDir(), data, "v", "1.0.0")
	err = s.materialize(context.Background(), bad, func() ([]byte, error) { return append([]byte{1}, data...), nil })
	if err == nil || seen != nil {
		t.Fatalf("sha mismatch: err=%v verifier ran=%v", err, seen != nil)
	}
}

func mkdirs(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestPruneOrphansAndRelease(t *testing.T) {
	dir := t.TempDir()
	keepDir := filepath.Join(dir, "demo", "1.0.0-aaaaaaaa")
	oldDir := filepath.Join(dir, "demo", "0.9.0-bbbbbbbb")
	work := filepath.Join(dir, "demo", "work")
	goneDir := filepath.Join(dir, "gone", "1.0.0-cccccccc")
	odd := filepath.Join(dir, "demo", "notes")
	mkdirs(t, keepDir, oldDir, work, goneDir, odd)
	_ = os.WriteFile(filepath.Join(oldDir, "package.s2plugin"), []byte("x"), 0o644)

	// A cached package is never pruned even when not kept.
	data := registrytest.Package(t, registrytest.Manifest("demo", "2.0.0"), []byte("bin"), nil)
	cached, err := LoadPackage(dir, data, "", "unsigned")
	if err != nil {
		t.Fatal(err)
	}
	s := NewPackages(nil, dir)
	s.Adopt(cached)

	removed, err := s.PruneOrphans(func(key, versionHash string) bool {
		return key == "demo" && versionHash == "1.0.0-aaaaaaaa"
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 || exists(oldDir) || exists(goneDir) {
		t.Fatalf("removed %v", removed)
	}
	if !exists(keepDir) || !exists(work) || !exists(odd) || !exists(cached.Dir) {
		t.Fatal("kept or foreign directories were removed")
	}

	if refs := s.Cached(); len(refs) != 1 || refs[0] != (PackageRef{"demo", "2.0.0"}) {
		t.Fatalf("cached %v", refs)
	}
	if err := s.Release("demo", "2.0.0"); err != nil {
		t.Fatal(err)
	}
	if exists(cached.Dir) || len(s.Cached()) != 0 {
		t.Fatal("released package still present")
	}
	if _, err := cached.ReadFile("manifest.json"); err == nil {
		t.Fatal("package file handle still open after Release")
	}
	// Release without a cached package removes the version directories.
	if err := s.Release("demo", "1.0.0"); err != nil || exists(keepDir) {
		t.Fatalf("release uncached: %v", err)
	}
}

// Release keeps a package that a caller of Acquire still reads.
func TestReleaseKeepsAcquiredPackage(t *testing.T) {
	dir := t.TempDir()
	data := registrytest.Package(t, registrytest.Manifest("demo", "1.0.0"), []byte("bin"), nil)
	p, err := LoadPackage(dir, data, "", "unsigned")
	if err != nil {
		t.Fatal(err)
	}
	s := NewPackages(nil, dir)
	s.Adopt(p)
	got, done, err := s.Acquire(context.Background(), "demo", "1.0.0")
	if err != nil || got != p {
		t.Fatalf("acquire: %v", err)
	}
	if err := s.Release("demo", "1.0.0"); !errors.Is(err, ErrPackageInUse) {
		t.Fatalf("release of a held package: %v", err)
	}
	if _, err := p.ReadFile("manifest.json"); err != nil || !exists(p.Dir) {
		t.Fatalf("held package unreadable after Release: %v", err)
	}
	done()
	done() // idempotent
	if err := s.Release("demo", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ReadFile("manifest.json"); err == nil || exists(p.Dir) || len(s.Cached()) != 0 {
		t.Fatal("package kept after its hold was done")
	}
}

func TestVersionOfDir(t *testing.T) {
	for name, want := range map[string]string{
		"1.0.0-0123abcd":      "1.0.0",
		"1.0.0-rc.1-deadbeef": "1.0.0-rc.1",
		"work":                "",
		"1.0.0-DEADBEEF":      "",
		"1.0.0-0123abc":       "",
	} {
		got, ok := versionOfHash(name)
		if ok != (want != "") || got != want {
			t.Errorf("%s: got %q %v", name, got, ok)
		}
	}
}
