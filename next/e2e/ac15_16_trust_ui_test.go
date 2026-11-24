package e2e

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// AC 15: tampering with any file of a package makes installation fail;
// revoking a publisher disables its plugins; a community publisher cannot
// request ui.native.
func TestAC15_SignatureAndTrust(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()

	t.Run("tampered official package is rejected", func(t *testing.T) {
		e := e.With(t)
		data := e.MarketPackage("anthropic", e.defaultVersion("anthropic"))
		// Sanity: the untouched package passes verification on upload (then reject it).
		for _, target := range []string{"", "manifest.json"} {
			pkg := ReadPackage(t, data)
			if target == "" {
				target = pkg.FirstNonManifestFile()
			}
			pkg.Tamper(t, target)
			r := e.UploadPlugin(admin, "anthropic-tampered.s2plugin", pkg.Bytes(t))
			if r.Status != 400 {
				t.Fatalf("tampered %s accepted: %s", target, r)
			}
			if !strings.Contains(strings.ToLower(string(r.Body)), "signature") && !strings.Contains(strings.ToLower(string(r.Body)), "digest") {
				t.Fatalf("tampered %s: error does not mention the signature: %s", target, r)
			}
		}
		// An added file is tampering as well.
		pkg := ReadPackage(t, data)
		pkg["i18n/evil.json"] = []byte(`{}`)
		if r := e.UploadPlugin(admin, "anthropic-extra.s2plugin", pkg.Bytes(t)); r.Status != 400 {
			t.Fatalf("package with an extra file accepted: %s", r)
		}
		// Unsigned packages are refused (SUB2API_PLUGIN_ALLOW_UNSIGNED=false).
		pkg = ReadPackage(t, data)
		delete(pkg, "signature.json")
		if r := e.UploadPlugin(admin, "anthropic-unsigned.s2plugin", pkg.Bytes(t)); r.Status != 400 && r.Status != 403 {
			t.Fatalf("unsigned package accepted: %s", r)
		}
		// Signed by an unknown key.
		pkg = ReadPackage(t, data)
		pkg.Sign(t, NewSigningKey(t, "sub2api", "rogue-"+e.RunID))
		if r := e.UploadPlugin(admin, "anthropic-rogue.s2plugin", pkg.Bytes(t)); r.Status != 400 && r.Status != 403 {
			t.Fatalf("package signed by an unknown key accepted: %s", r)
		}
	})

	t.Run("revoking a publisher disables its plugins", func(t *testing.T) {
		e := e.With(t)
		const pk = "e2e_revoke"
		key := NewSigningKey(t, e.PublisherID(), "e2e-rev-"+e.RunID)
		id := key.PluginID(pk)
		pubID := e.RegisterPublisher(admin, key, "verified")
		pkg := e.DerivedGuardPackage(pk, key, false)
		r := e.UploadPlugin(admin, pk+".s2plugin", pkg.Bytes(t))
		if r.Status != 200 {
			t.Fatalf("upload: %s", r)
		}
		rev := reviewOf(e, r.Data())
		if rev.Get("trust").String() != "verified" || rev.Get("signature_status").String() != "valid" {
			t.Fatalf("review trust: %s", rev.Raw)
		}
		e.ConsentAll(admin, rev, nil)
		e.Enable(admin, id)

		admin.OK(t, http.MethodPost, fmt.Sprintf("/publishers/%d/revoke", pubID), nil)
		d := e.WaitPlugin(admin, id, "disabled", "")
		if !strings.Contains(strings.ToLower(d.Get("status_reason").String()), "revoked") {
			t.Fatalf("status_reason: %s", d.Raw)
		}
		e.Uninstall(admin, id, true)
	})

	t.Run("community publisher cannot request ui.native", func(t *testing.T) {
		e := e.With(t)
		key := NewSigningKey(t, e.PublisherID(), "e2e-com-"+e.RunID)
		pubID := e.RegisterPublisher(admin, key, "community")
		defer admin.API(t, http.MethodPost, fmt.Sprintf("/publishers/%d/revoke", pubID), nil)
		pkg := e.DerivedGuardPackage("e2e_native", key, true)
		r := e.UploadPlugin(admin, "e2e_native.s2plugin", pkg.Bytes(t))
		if r.Status == 200 {
			t.Fatalf("community package with ui.native accepted: %s", r)
		}
		if !strings.Contains(string(r.Body), "ui.native") {
			t.Fatalf("rejection does not name ui.native: %s", r)
		}
		// Without ui.native the same publisher is fine (community: no critical permissions).
		pkg = e.DerivedGuardPackage("e2e_native", key, false)
		r = e.UploadPlugin(admin, "e2e_native.s2plugin", pkg.Bytes(t))
		if r.Status != 200 {
			t.Fatalf("community package without ui.native: %s", r)
		}
		if rv := reviewOf(e, r.Data()); rv.Get("trust").String() != "community" {
			t.Fatalf("trust: %s", rv.Raw)
		}
		admin.API(t, http.MethodPost, "/plugins/"+key.PluginID("e2e_native")+"/versions/"+pkg.Manifest(t)["version"].(string)+"/reject", nil)
		Eventually(t, 10*time.Second, time.Second, "rejected plugin gone", func() bool {
			d, ok := e.Plugin(admin, key.PluginID("e2e_native"))
			return !ok || d.Get("status").String() != "awaiting_consent"
		})
	})
}

// AC 16: guard's native dashboard page and home widget are exposed to the
// console; disabling guard removes them.
func TestAC16_GuardNativeUI(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	e.EnsurePlugin(admin, "guard", "")

	ui := admin.OK(t, http.MethodGet, "/ui/plugins", nil).Array()
	g, ok := Find(ui, "key", "guard")
	if !ok {
		t.Fatalf("guard missing from /ui/plugins: %v", ui)
	}
	if g.Get("trust").String() != "official" || g.Get("native_entry").String() == "" || g.Get("asset_base").String() == "" {
		t.Fatalf("guard ui entry: %s", g.Raw)
	}
	if !strings.Contains(g.Get("slots").Raw, "dashboard.widgets") || !strings.Contains(g.Get("pages").Raw, "native") {
		t.Fatalf("guard slots/pages: %s", g.Raw)
	}
	// The entry module is served with long-lived caching.
	entry := strings.TrimRight(g.Get("asset_base").String(), "/") + "/" + strings.TrimPrefix(g.Get("native_entry").String(), "/")
	if !strings.HasPrefix(entry, "http") {
		entry = e.BaseURL + entry
	}
	resp, err := http.Get(entry)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "javascript") ||
		!strings.Contains(resp.Header.Get("Cache-Control"), "max-age") {
		t.Fatalf("native entry %s: %d %v", entry, resp.StatusCode, resp.Header)
	}
	menuHas := func() bool {
		for _, it := range admin.Menus(t) {
			if it.Get("plugin_key").String() == "guard" {
				return true
			}
		}
		return false
	}
	if !menuHas() {
		t.Fatal("guard menu missing")
	}

	e.Disable(admin, "guard")
	if _, ok := Find(admin.OK(t, http.MethodGet, "/ui/plugins", nil).Array(), "key", "guard"); ok {
		t.Fatal("guard still in /ui/plugins after disable")
	}
	if menuHas() {
		t.Fatal("guard menu still shown after disable")
	}
	e.Enable(admin, "guard")
}
