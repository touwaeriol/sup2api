package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// PublisherID is a publisher id unique to the run (check.ValidatePublisherID:
// lowercase letters, digits and single underscores), short enough to leave
// room for a key in a plugin id (manifest.PluginIDMaxLen).
func (e *Env) PublisherID() string {
	return fmt.Sprintf("e%s_%d", e.RunID, nextSeq())
}

// PluginID is the plugin id of key published by k's publisher.
func (k *SigningKey) PluginID(key string) string {
	return manifest.PluginID(k.Publisher, key)
}

// RegisterPublisher creates a publisher with trust level (verified|community)
// and registers key for it. Returns the publisher id.
func (e *Env) RegisterPublisher(admin *Session, key *SigningKey, trust string) int64 {
	e.T.Helper()
	d := admin.OK(e.T, http.MethodPost, "/publishers", map[string]any{"name": key.Publisher, "trust_level": trust})
	id := d.Get("id").Int()
	if id == 0 {
		e.T.Fatalf("POST /publishers returned no id: %s", d.Raw)
	}
	admin.OK(e.T, http.MethodPost, fmt.Sprintf("/publishers/%d/keys", id),
		map[string]any{"key_id": key.KeyID, "public_key": key.PublicB64()})
	return id
}

// DerivedGuardPackage builds a third-party package from the official guard
// package: new plugin key, publisher = key.Publisher (and the table prefix
// of their plugin id),
// signed with key. Unless keepNative, everything requiring ui.native is
// stripped so the package is acceptable for community/verified publishers.
// edits then change the manifest further (a version among them) before the
// binaries are built.
func (e *Env) DerivedGuardPackage(pluginKey string, key *SigningKey, keepNative bool, edits ...func(m map[string]any)) Package {
	e.T.Helper()
	pkg := ReadPackage(e.T, e.MarketPackage("guard", e.defaultVersion("guard")))
	m := pkg.Manifest(e.T)
	m["key"] = pluginKey
	m["publisher"] = key.Publisher
	m["name"] = map[string]any{"en": "E2E " + pluginKey, "zh": "E2E " + pluginKey}
	if !keepNative {
		delete(m, "hostUICompat")
		if ui, ok := m["ui"].(map[string]any); ok {
			delete(ui, "native")
			delete(ui, "slots")
			if s, ok := ui["settings"].(map[string]any); ok && s["mode"] == "native" {
				delete(ui, "settings")
			}
			pages, _ := ui["pages"].(map[string]any)
			for id, p := range pages {
				if pm, ok := p.(map[string]any); ok && pm["type"] == "native" {
					delete(pages, id)
				}
			}
			if menus, ok := ui["menus"].([]any); ok {
				var keep []any
				for _, mn := range menus {
					if mm, ok := mn.(map[string]any); ok {
						if _, still := pages[fmt.Sprint(mm["page"])]; !still {
							continue
						}
					}
					keep = append(keep, mn)
				}
				ui["menus"] = keep
			}
		}
		if hps, ok := m["hostPermissions"].([]any); ok {
			var keep []any
			for _, hp := range hps {
				if hm, ok := hp.(map[string]any); ok && hm["id"] == "ui.native" {
					continue
				}
				keep = append(keep, hp)
			}
			m["hostPermissions"] = keep
		}
		for p := range pkg {
			if strings.HasPrefix(p, "ui/native/") {
				delete(pkg, p)
			}
		}
	}
	for _, edit := range edits {
		edit(m)
	}
	// The tables are named after the plugin id (manifest.TablePrefix): the
	// migrations create the new id's (buildGuardBinary renames the
	// binary's).
	id := key.PluginID(pluginKey)
	for p, b := range pkg {
		if strings.HasPrefix(p, "migrations/") {
			pkg[p] = bytes.ReplaceAll(b, []byte(manifest.TablePrefix("guard")), []byte(manifest.TablePrefix(id)))
		}
	}
	// The host checks GetInfo against the package, so the binaries must
	// report the new key and version: rebuild them with the SDK build-time
	// values.
	for p := range pkg {
		if rest, ok := strings.CutPrefix(p, "runtimes/"); ok {
			osArch, _, _ := strings.Cut(rest, "/")
			goos, goarch, _ := strings.Cut(osArch, "-")
			pkg[p] = buildGuardBinary(e.T, goos, goarch, id, fmt.Sprint(m["version"]))
		}
	}
	pkg.SetManifest(e.T, m)
	pkg.Sign(e.T, key)
	return pkg
}

// DerivedPlugin is a third-party plugin derived from guard
// (DerivedGuardPackage) of a publisher unique to the run: Key is its
// manifest key, ID its plugin id (CONTRACTS §137), which the API and the
// permission keys use.
type DerivedPlugin struct {
	Key    string
	ID     string
	Signer *SigningKey
}

// NewDerivedPlugin registers a new verified publisher for a derived plugin
// keyed key.
func (e *Env) NewDerivedPlugin(admin *Session, key string) DerivedPlugin {
	e.T.Helper()
	signer := NewSigningKey(e.T, e.PublisherID(), e.Name("e2e-"+key))
	e.RegisterPublisher(admin, signer, "verified")
	return DerivedPlugin{Key: key, ID: signer.PluginID(key), Signer: signer}
}

// Package builds a version of the plugin (DerivedGuardPackage edits).
func (d DerivedPlugin) Package(e *Env, edits ...func(m map[string]any)) Package {
	e.T.Helper()
	return e.DerivedGuardPackage(d.Key, d.Signer, false, edits...)
}

// UploadPackage uploads pkg and returns its review.
func (e *Env) UploadPackage(admin *Session, pkg Package) gjson.Result {
	e.T.Helper()
	m := pkg.Manifest(e.T)
	r := e.UploadPlugin(admin, fmt.Sprintf("%v-%v.s2plugin", m["key"], m["version"]), pkg.Bytes(e.T))
	if r.Status != 200 {
		e.T.Fatalf("upload %v@%v: %s", m["key"], m["version"], r)
	}
	return reviewOf(e, r.Data())
}

// InstallVersion uploads pkg, consents with newPermissionRoles (Consent)
// when the version awaits consent, and makes it the enabled version: a
// first install is enabled, and uninstalled when the test ends; a later
// version is upgraded to. Returns the upload's review.
func (e *Env) InstallVersion(admin *Session, pkg Package, newPermissionRoles map[string]any) gjson.Result {
	e.T.Helper()
	m := pkg.Manifest(e.T)
	key, version := manifest.PluginID(fmt.Sprint(m["publisher"]), fmt.Sprint(m["key"])), fmt.Sprint(m["version"])
	_, installed := e.Plugin(admin, key)
	rev := e.UploadPackage(admin, pkg)
	if rev.Get("consent_status").String() == "awaiting_consent" {
		e.Consent(admin, rev, newPermissionRoles)
	}
	if installed {
		e.Upgrade(admin, key, version)
		return rev
	}
	e.Enable(admin, key)
	e.T.Cleanup(func() {
		if _, ok := e.Plugin(admin, key); ok {
			e.Uninstall(admin, key, true)
		}
	})
	return rev
}

// buildGuardBinary compiles plugins/guard as plugin id: the key of id is
// the binary's (GetInfo) and id's table prefix its tables'. extra adds Go
// files to the package (file name -> source), through a build overlay: the
// source tree stays untouched.
func buildGuardBinary(t testing.TB, goos, goarch, id, version string, extra ...map[string]string) []byte {
	t.Helper()
	_, key, _ := manifest.ParsePluginID(id)
	_, self, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(self), "..", "plugins", "guard")
	out := filepath.Join(t.TempDir(), "plugin")
	sdk := "github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	files := renamedTables(t, dir, id)
	for _, e := range extra {
		maps.Copy(files, e)
	}
	args := []string{"build", "-trimpath", "-o", out,
		"-ldflags", fmt.Sprintf("-s -w -X %s.buildKey=%s -X %s.buildVersion=%s", sdk, key, sdk, version),
		"-overlay", buildOverlay(t, dir, files)}
	cmd := exec.Command("go", append(args, ".")...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build guard for %s: %v\n%s", key, err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// renamedTables returns guard's Go files (tests aside) that name its
// tables, with the table prefix of plugin id (manifest.TablePrefix) for
// guard's.
func renamedTables(t testing.TB, dir, id string) map[string]string {
	t.Helper()
	from, to := manifest.TablePrefix("guard"), manifest.TablePrefix(id)
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && d.Name() == "node_modules":
			return filepath.SkipDir
		case d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go"):
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil || !bytes.Contains(src, []byte(from)) {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		files[filepath.ToSlash(rel)] = strings.ReplaceAll(string(src), from, to)
		return err
	})
	if err != nil || len(files) == 0 {
		t.Fatalf("guard: no Go file names its tables %s… (%v)", from, err)
	}
	return files
}

// buildOverlay writes files to a temporary directory and returns a go build
// -overlay file adding them to dir under their names.
func buildOverlay(t testing.TB, dir string, files map[string]string) string {
	t.Helper()
	tmp := t.TempDir()
	replace := map[string]string{}
	for name, src := range files {
		path := filepath.Join(tmp, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		abs, err := filepath.Abs(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		replace[abs] = path
	}
	raw, _ := json.Marshal(map[string]any{"Replace": replace})
	overlay := filepath.Join(tmp, "overlay.json")
	if err := os.WriteFile(overlay, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return overlay
}
