package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/install"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/market"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg/pkgtest"
)

// consentFileBody is fileBody with the multipart field consent.
func consentFileBody(t *testing.T, data []byte, consent string) multipartBody {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "guard.s2plugin")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(data)
	_ = mw.WriteField("consent", consent)
	_ = mw.Close()
	return multipartBody{buf: &buf, ct: mw.FormDataContentType()}
}

// guardConsent is the consent of the guard package with sha.
func guardConsent(sha string) install.InstallConsent {
	return install.InstallConsent{PackageSHA256: sha, ConsentRequest: install.ConsentRequest{Grants: []install.GrantInput{
		{Permission: "db.schema"}, {Permission: "gateway.hook"}, {Permission: "events"}, {Permission: "jobs"},
		{Permission: "routes.admin"}, {Permission: "ui.menu"}, {Permission: "ui.native"}}}}
}

// pluginRows counts the plugins and plugin_versions rows.
func (h *harness) pluginRows() (plugins, versions int) {
	h.t.Helper()
	if err := h.db.SQL.QueryRowContext(context.Background(), `SELECT (SELECT count(*) FROM plugins), (SELECT count(*) FROM plugin_versions)`).
		Scan(&plugins, &versions); err != nil {
		h.t.Fatal(err)
	}
	return plugins, versions
}

// serveMarket gives h a market source (id returned) whose index offers
// guard 0.1.0 as data.
func (h *harness) serveMarket(data []byte) int64 {
	h.t.Helper()
	mk := pkgtest.NewKey("market")
	idx := market.Index{Version: 1, Plugins: []market.IndexPlugin{{Key: "guard", Name: core.LocalizedText{"en": "Guard"}, Publisher: "sub2api",
		Versions: []market.IndexVersion{{Version: "0.1.0", URL: "guard.s2plugin", SHA256: pkg.SHA256Hex(data), Size: int64(len(data)), HostCompat: ">=0.1.0"}}}}}
	raw, _ := json.Marshal(idx)
	sig := market.SignIndex(raw, mk.Priv)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string][]byte{"/m/index.json": raw, "/m/index.json.sig": sig, "/m/guard.s2plugin": data}[r.URL.Path]
		_, _ = w.Write(body)
	}))
	h.t.Cleanup(srv.Close)
	m := market.New(h.db, h.api.d.Install, srv.Client(), 10<<20)
	ctx := context.Background()
	if err := m.SeedSources(ctx, fmt.Sprintf(`[{"name":"official","url":%q,"public_key":%q}]`, srv.URL+"/m/", mk.PubB64())); err != nil {
		h.t.Fatal(err)
	}
	srcs, err := m.ListSources(ctx)
	if err != nil || len(srcs) != 1 {
		h.t.Fatalf("sources: %v %v", srcs, err)
	}
	h.api.d.Market = m
	return srcs[0].ID
}

// POST /plugins/review-upload returns the review and writes nothing; upload
// with consent returns {review, consent}; a consent for another package is
// 409 package_changed, an unreadable one 400.
func TestAPIUploadWithConsent(t *testing.T) {
	h, root := newHarness(t)
	pkgBytes := pkgtest.Build(pkgtest.Guard("guard", "0.1.0", "sub2api"), root)
	sha := pkg.SHA256Hex(pkgBytes)
	if code, _ := h.do("POST", "/plugins/review-upload", "viewer", fileBody(t, pkgBytes)); code != http.StatusForbidden {
		t.Fatalf("viewer review = %d", code)
	}
	code, out := h.do("POST", "/plugins/review-upload", "admin", fileBody(t, pkgBytes))
	if r := data(out); code != 200 || r["package_sha256"] != sha || r["consent_status"] != "" || r["uploaded_at"] != nil {
		t.Fatalf("review = %d %v", code, out)
	}
	if p, v := h.pluginRows(); p != 0 || v != 0 {
		t.Fatalf("review wrote %d plugins, %d versions", p, v)
	}
	if code, out = h.do("POST", "/plugins/upload", "admin", consentFileBody(t, pkgBytes, "{")); code != 400 {
		t.Fatalf("bad consent = %d %v", code, out)
	}
	other, _ := json.Marshal(guardConsent(pkg.SHA256Hex([]byte("other"))))
	if code, out = h.do("POST", "/plugins/upload", "admin", consentFileBody(t, pkgBytes, string(other))); code != 409 || out["error"].(map[string]any)["code"] != "package_changed" {
		t.Fatalf("changed package = %d %v", code, out)
	}
	consent, _ := json.Marshal(guardConsent(sha))
	code, out = h.do("POST", "/plugins/upload", "admin", consentFileBody(t, pkgBytes, string(consent)))
	review, _ := data(out)["review"].(map[string]any)
	res, _ := data(out)["consent"].(map[string]any)
	if code != 200 || review["consent_status"] != install.ConsentApproved || res["status"] != install.StatusInstalled {
		t.Fatalf("upload with consent = %d %v", code, out)
	}
}

// The market preview and install with consent download the version and
// check it against the index, as the install without consent does.
func TestAPIMarketWithConsent(t *testing.T) {
	h, root := newHarness(t)
	pkgBytes := pkgtest.Build(pkgtest.Guard("guard", "0.1.0", "sub2api"), root)
	src := h.serveMarket(pkgBytes)
	body := map[string]any{"source_id": src, "key": "guard", "version": "0.1.0"}
	code, out := h.do("POST", "/plugins/review-from-market", "admin", body)
	if code != 200 || data(out)["package_sha256"] != pkg.SHA256Hex(pkgBytes) || data(out)["consent_status"] != "" {
		t.Fatalf("market review = %d %v", code, out)
	}
	if p, v := h.pluginRows(); p != 0 || v != 0 {
		t.Fatalf("review wrote %d plugins, %d versions", p, v)
	}
	body["consent"] = guardConsent(pkg.SHA256Hex(pkgBytes))
	code, out = h.do("POST", "/plugins/install-from-market", "admin", body)
	review, _ := data(out)["review"].(map[string]any)
	if code != 200 || review["consent_status"] != install.ConsentApproved {
		t.Fatalf("market install with consent = %d %v", code, out)
	}
}
