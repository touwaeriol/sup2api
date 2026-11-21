package registry_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	pluginpkg "github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry/registrytest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

func TestVersionAssetsReadApprovedPackageWithoutLocalProcess(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	m := registrytest.Manifest("assets", "1.0.0")
	raw := registrytest.Package(t, m, []byte("bin"), map[string][]byte{"ui/main.js": []byte("old version")})
	registrytest.Install(t, db, m, raw, nil, "enabled")
	packages := registry.NewPackages(db, t.TempDir(), registry.WithSource(registrytest.Source()))
	defer packages.Close()
	vh := "1.0.0-" + pluginpkg.SHA256Hex(raw)[:8]
	info, data, _, err := packages.ReadVersionAsset(ctx, "assets", vh, "ui/main.js")
	if err != nil || string(data) != "old version" || info.Version != "1.0.0" {
		t.Fatal(info, string(data), err)
	}
	if err := os.Remove(filepath.Join(packages.DataDir(), "assets", vh, "package.s2plugin")); err != nil {
		t.Fatalf("version asset read retained a package handle: %v", err)
	}
	if _, err := db.SQL.ExecContext(ctx, `UPDATE plugin_versions SET consent_status='awaiting' WHERE plugin_key='assets'`); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := packages.ReadVersionAsset(ctx, "assets", vh, "ui/main.js"); err == nil {
		t.Fatal("unapproved package exposed")
	}
}

// A plugin that is installed but does not run serves its icon and nothing
// else: the plugins list shows it with its logo. The icon is read from the
// package once; every read still asks whether the version may be served.
func TestVersionAssetsServeOnlyTheIconWhileNotRunning(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	m := registrytest.Manifest("idle", "1.0.0")
	m.Icon = "assets/logo.svg"
	raw := registrytest.Package(t, m, []byte("bin"), map[string][]byte{m.Icon: []byte("<svg/>"), "assets/other.svg": []byte("<svg/>")})
	registrytest.Install(t, db, m, raw, nil, "disabled")
	loads := 0
	packages := registry.NewPackages(db, t.TempDir(), registry.WithSource(registrytest.Source()),
		registry.WithVerifier(func(context.Context, string, string, []byte) error { loads++; return nil }))
	defer packages.Close()
	vh := "1.0.0-" + pluginpkg.SHA256Hex(raw)[:8]
	icon := func() error {
		_, data, ct, err := packages.ReadVersionAsset(ctx, "idle", vh, m.Icon)
		if err == nil && (string(data) != "<svg/>" || ct != "image/svg+xml") {
			t.Fatalf("icon = %q %s", data, ct)
		}
		return err
	}
	if err := icon(); err != nil {
		t.Fatalf("icon of a disabled plugin: %v", err)
	}
	for _, name := range []string{"ui/index.html", "i18n/en.json", "assets/other.svg"} {
		if _, _, _, err := packages.ReadVersionAsset(ctx, "idle", vh, name); !errors.Is(err, registry.ErrAssetNotFound) {
			t.Fatalf("a disabled plugin served %s: %v", name, err)
		}
	}
	// The second read does not load the package again.
	first := loads
	if err := icon(); err != nil || first == 0 || loads != first {
		t.Fatalf("icon read again: %v, package loads %d then %d", err, first, loads)
	}
	// The version hash must name the version's own package.
	for _, bad := range []string{"1.0.0-00000000", "1.0.0", "1.0.0-" + pluginpkg.SHA256Hex(raw)[:7]} {
		if _, _, _, err := packages.ReadVersionAsset(ctx, "idle", bad, m.Icon); !errors.Is(err, registry.ErrAssetNotFound) {
			t.Fatalf("version hash %s served: %v", bad, err)
		}
	}
	// One icon is kept per plugin: reading another version's replaces it.
	m2 := registrytest.Manifest("idle", "1.1.0")
	m2.Icon = m.Icon
	raw2 := registrytest.Package(t, m2, []byte("bin2"), map[string][]byte{m.Icon: []byte("<svg/>")})
	registrytest.AddVersion(t, db, m2, raw2)
	if _, _, _, err := packages.ReadVersionAsset(ctx, "idle", "1.1.0-"+pluginpkg.SHA256Hex(raw2)[:8], m.Icon); err != nil || loads == first {
		t.Fatalf("icon of another version: %v, package loads %d then %d", err, first, loads)
	}
	second := loads
	if err := icon(); err != nil || loads == second {
		t.Fatalf("replaced icon not read again: %v, package loads %d then %d", err, second, loads)
	}
	for _, refuse := range []struct{ why, set, reset string }{
		{"unapproved", `UPDATE plugin_versions SET consent_status='awaiting' WHERE plugin_key='idle'`, `UPDATE plugin_versions SET consent_status='approved' WHERE plugin_key='idle'`},
		{"revoked", `UPDATE plugin_versions SET signature_status='revoked' WHERE plugin_key='idle'`, `UPDATE plugin_versions SET signature_status='unsigned' WHERE plugin_key='idle'`},
		{"uninstalling", `INSERT INTO plugin_uninstalls (plugin_key, epoch) VALUES ('idle', 1)`, `DELETE FROM plugin_uninstalls WHERE plugin_key='idle'`},
	} {
		if _, err := db.SQL.ExecContext(ctx, refuse.set); err != nil {
			t.Fatal(refuse.why, err)
		}
		if err := icon(); !errors.Is(err, registry.ErrAssetNotFound) {
			t.Fatalf("%s plugin served its icon: %v", refuse.why, err)
		}
		if _, err := db.SQL.ExecContext(ctx, refuse.reset); err != nil {
			t.Fatal(refuse.why, err)
		}
	}
}
