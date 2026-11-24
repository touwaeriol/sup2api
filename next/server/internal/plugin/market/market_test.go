package market

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg/pkgtest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

type fakeInstaller struct{}

func (fakeInstaller) HostVersion() string { return "0.1.0" }

var generatedAt = time.Date(2026, 11, 24, 6, 0, 0, 0, time.UTC)

var deprecated = &Deprecation{Reason: core.LocalizedText{"en": "use 1.1.0"}}

// testIndex is the index the market test serves: tool by acme (active):
// 1.1.0 is pkgBytes, 1.0.0 fails its sha256, 1.2.0 is deprecated, 2.0.0
// needs a newer host; gone by the revoked publisher old; lost by nobody
// the index lists.
func testIndex(pkgBytes []byte) []byte {
	idx := Index{Version: indexFormat, GeneratedAt: generatedAt, Publishers: []IndexPublisher{
		{ID: "acme", Name: core.LocalizedText{"en": "Acme"}, GitHub: []string{"acme-dev"}, Trust: pkg.TrustVerified, Status: pkg.PublisherActive},
		{ID: "old", Name: core.LocalizedText{"en": "Old"}, Trust: pkg.TrustCommunity, Status: pkg.PublisherRevoked},
	}, Plugins: []IndexPlugin{
		{Key: "tool", Name: core.LocalizedText{"en": "Tool"}, Publisher: "acme", Repo: "acme/tool", Builtin: true, Versions: []IndexVersion{
			{Version: "1.0.0", URL: "pkgs/tool-1.0.0.s2plugin", SHA256: "00", Size: 1, HostCompat: ">=0.1.0"},
			{Version: "1.1.0", URL: "pkgs/tool-1.1.0.s2plugin", SHA256: pkg.SHA256Hex(pkgBytes), Size: int64(len(pkgBytes)), HostCompat: ">=0.1.0",
				ReleaseURL: "https://github.com/acme/tool/releases/tag/v1.1.0"},
			{Version: "1.2.0", URL: "pkgs/tool-1.2.0.s2plugin", SHA256: "00", Size: 1, HostCompat: ">=0.1.0", Deprecated: deprecated},
			{Version: "2.0.0", URL: "pkgs/tool-2.0.0.s2plugin", SHA256: "00", Size: 1, HostCompat: ">=0.2.0"},
		}},
		{Key: "gone", Publisher: "old", Deprecated: deprecated, Versions: []IndexVersion{{Version: "1.0.0", URL: "x", HostCompat: ">=0.1.0"}}},
		{Key: "lost", Publisher: "nobody", Versions: []IndexVersion{{Version: "1.0.0", URL: "x", HostCompat: ">=0.1.0"}}},
	}}
	raw, _ := json.Marshal(idx)
	return raw
}

// serveIndex serves raw as the index of the channel main of the store at
// /m and the packages of tool over TLS; hits counts the index downloads.
func serveIndex(t *testing.T, raw, pkgBytes []byte) (srv *httptest.Server, hits *int) {
	hits = new(int)
	files := map[string][]byte{"/m/index-main/pkgs/tool-1.1.0.s2plugin": pkgBytes, "/m/index-main/pkgs/tool-1.0.0.s2plugin": []byte("x")}
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/m/index-main/index.json" {
			*hits++
			_, _ = w.Write(raw)
		} else if body, ok := files[r.URL.Path]; ok {
			_, _ = w.Write(body)
		} else {
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, hits
}

// seedChannels points the channels at the base of s, twice (it is
// idempotent), and returns the default channel's id.
func seedChannels(t *testing.T, s *Service) int64 {
	ctx := context.Background()
	for range 2 {
		if err := s.EnsureStores(ctx); err != nil {
			t.Fatal(err)
		}
	}
	srcs, err := s.ListSources(ctx, 0)
	if err != nil || len(srcs) != 2 || !srcs[0].IsDefault {
		t.Fatalf("sources: %v %v", err, srcs)
	}
	return srcs[0].ID
}

// newMarket serves the test index over TLS to a market (https only) as its
// default channel, whose id and index directory it returns, and the
// package of tool 1.1.0.
func newMarket(t *testing.T) (s *Service, src int64, base string, pkgBytes []byte, hits *int) {
	pkgBytes = pkgtest.Build(pkgtest.Minimal("tool", "1.1.0", "acme"), pkgtest.NewKey("acme-1"))
	srv, hits := serveIndex(t, testIndex(pkgBytes), pkgBytes)
	s = New(testutil.DB(t), fakeInstaller{}, srv.Client(), Options{MaxPackageBytes: 1 << 20, BaseURL: srv.URL + "/m/"})
	return s, seedChannels(t, s), srv.URL + "/m/index-main/", pkgBytes, hits
}

func TestMarket(t *testing.T) {
	ctx := context.Background()
	s, src, base, pkgBytes, hits := newMarket(t)
	l, err := s.ListPlugins(ctx, src, false)
	if err != nil || len(l.Plugins) != 3 || !l.GeneratedAt.Equal(generatedAt) || l.FetchedAt.IsZero() {
		t.Fatalf("list = %+v %v", l, err)
	}
	list := l.Plugins
	if _, err := s.ListPlugins(ctx, 0, false); err != nil || *hits != 1 {
		t.Fatalf("cache: hits=%d err=%v", *hits, err)
	}
	checkTool(t, list[0])
	checkPublishers(t, list[1], list[2])
	checkDownload(t, s, src, base, pkgBytes)
}

// checkTool: versions newest first, the deprecated 1.2.0 and the
// incompatible 2.0.0 are not the latest; the index's publisher, repo and
// builtin pass through.
func checkTool(t *testing.T, m MarketPlugin) {
	t.Helper()
	if m.LatestVersion != "1.1.0" || len(m.Versions) != 4 || m.Versions[0].Version != "2.0.0" || m.Versions[0].Compatible ||
		m.Versions[1].Deprecated == nil || m.Versions[2].Deprecated != nil || m.Versions[2].ReleaseURL != "https://github.com/acme/tool/releases/tag/v1.1.0" ||
		m.Versions[3].SHA256 != "00" {
		t.Fatalf("tool versions = %+v", m)
	}
	if m.ID != "_acme__tool" || m.DisplayID != "acme/tool" || m.Key != "tool" ||
		m.Trust != pkg.TrustVerified || m.PublisherStatus != pkg.PublisherActive || m.PublisherName["en"] != "Acme" || !slices.Equal(m.PublisherGitHub, []string{"acme-dev"}) ||
		m.Repo != "acme/tool" || !m.Builtin || m.Deprecated != nil {
		t.Fatalf("tool = %+v", m)
	}
}

// checkPublishers: a revoked publisher and one the index does not list are
// marked, a deprecated plugin passes its deprecation through.
func checkPublishers(t *testing.T, gone, lost MarketPlugin) {
	t.Helper()
	if gone.PublisherStatus != pkg.PublisherRevoked || gone.Trust != pkg.TrustCommunity || gone.Deprecated == nil {
		t.Fatalf("gone = %+v", gone)
	}
	if lost.PublisherStatus != PublisherMissing || lost.Trust != "" {
		t.Fatalf("lost = %+v", lost)
	}
}

// checkDownload downloads the versions of source srcID (index at base):
// tool 1.1.0 is pkgBytes with the index's trust, tool 1.0.0 fails its
// sha256, 9.9.9 is not listed, the plugins of a revoked and of an unlisted
// publisher are refused.
func checkDownload(t *testing.T, s *Service, srcID int64, base string, pkgBytes []byte) {
	ctx := context.Background()
	got, opt, err := s.Download(ctx, srcID, "acme", "tool", "1.1.0")
	if err != nil || !bytes.Equal(got, pkgBytes) {
		t.Fatalf("download = %d bytes, %v", len(got), err)
	}
	src, err := s.channel(ctx, srcID)
	if err != nil {
		t.Fatal(err)
	}
	want := pkg.IndexedPublisher{Name: "acme", Trust: pkg.TrustVerified, Status: pkg.PublisherActive, StoreID: src.StoreID}
	if opt.ExpectID != "_acme__tool" || opt.ExpectVersion != "1.1.0" || opt.Source != fmt.Sprintf("market:%d", srcID) ||
		opt.PackageURL != base+"pkgs/tool-1.1.0.s2plugin" || opt.Indexed == nil || *opt.Indexed != want {
		t.Fatalf("options = %+v", opt)
	}
	for _, c := range []struct{ publisher, key, version, code, reason string }{
		{"acme", "tool", "1.0.0", "invalid_argument", ""},
		{"acme", "tool", "9.9.9", "not_found", ""},
		{"sub2api", "tool", "1.1.0", "not_found", ""},
		{"old", "gone", "1.0.0", "permission_denied", "revoked"},
		{"nobody", "lost", "1.0.0", "permission_denied", "publisher_unlisted"},
	} {
		_, _, err := s.Download(ctx, srcID, c.publisher, c.key, c.version)
		if e := core.AsError(err); e == nil || e.Code != c.code || c.reason != "" && e.Details["reason"] != c.reason {
			t.Errorf("%s@%s: %v", c.key, c.version, err)
		}
	}
}

// update_available compares the installed version with the latest one,
// which is never deprecated: tool 1.1.0 installed has no update although
// the deprecated 1.2.0 is newer.
func TestUpdateAvailable(t *testing.T) {
	idx, err := parseIndex(testIndex(nil), false)
	if err != nil {
		t.Fatal(err)
	}
	for installed, want := range map[string]bool{"1.0.0": true, "1.1.0": false, "1.2.0": false} {
		st := &listState{hostVersion: "0.1.0", installed: map[string][2]string{"_acme__tool": {installed, "enabled"}, "tool": {"0.0.1", "enabled"}}}
		m := st.plugin(&Source{ID: 1}, idx, idx.Plugins[0])
		if m.UpdateAvailable != want || m.InstalledVersion != installed || m.LatestVersion != "1.1.0" {
			t.Errorf("installed %s: %+v", installed, m)
		}
	}
}

// Sources and package URLs are https; http only with AllowHTTP
// (SUB2API_MARKET_ALLOW_HTTP).
func TestMarketHTTPS(t *testing.T) {
	ctx := context.Background()
	if err := New(testutil.DB(t), fakeInstaller{}, nil, Options{BaseURL: "http://market.example/"}).EnsureStores(ctx); err == nil {
		t.Fatal("http source accepted")
	}
	s := New(testutil.DB(t), fakeInstaller{}, nil, Options{})
	dev := New(testutil.DB(t), fakeInstaller{}, nil, Options{AllowHTTP: true, BaseURL: "http://market.example/"})
	if err := dev.EnsureStores(ctx); err != nil {
		t.Fatalf("allow http: %v", err)
	}
	base, _ := s.indexURL("https://market.example/m/")
	e := &entry{base: base, version: &IndexVersion{URL: "http://cdn.example/tool.s2plugin"}}
	if _, err := s.downloadURL(e); err == nil {
		t.Fatal("http package URL accepted")
	}
	if dl, err := dev.downloadURL(e); err != nil || dl != "http://cdn.example/tool.s2plugin" {
		t.Fatalf("allow http package URL = %q %v", dl, err)
	}
}

// Only index.json v2 with valid publisher ids, known trust levels and
// statuses and each (publisher, key) once is read; two publishers may list
// one key.
func TestParseIndex(t *testing.T) {
	for _, raw := range []string{
		`{"version":1,"plugins":[]}`,
		`{"version":2,"publishers":[{"id":"a","trust":"unsigned","status":"active"}]}`,
		`{"version":2,"publishers":[{"id":"a","trust":"official","status":"paused"}]}`,
		`{"version":2,"publishers":[{"id":"Acme","trust":"official","status":"active"}]}`,
		`{"version":2,"publishers":[{"id":"","trust":"official","status":"active"}]}`,
		`{"version":2,"publishers":[{"id":"my-pub","trust":"official","status":"active"}]}`,
		`{"version":2,"plugins":[{"key":"codex","publisher":"erio"},{"key":"codex","publisher":"erio"}]}`,
		`not json`,
	} {
		if _, err := parseIndex([]byte(raw), false); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	if _, err := parseIndex(testIndex(nil), false); err != nil {
		t.Fatal(err)
	}
	two := `{"version":2,"plugins":[{"key":"codex","publisher":"sub2api"},{"key":"codex","publisher":"erio"}]}`
	if _, err := parseIndex([]byte(two), false); err != nil {
		t.Fatalf("one key of two publishers: %v", err)
	}
}

// find matches the (publisher, key) pair: two publishers' codex stay apart.
func TestIndexFindPair(t *testing.T) {
	v := []IndexVersion{{Version: "1.0.0"}}
	idx := &Index{Plugins: []IndexPlugin{{Key: "codex", Publisher: "sub2api", Versions: v}, {Key: "codex", Publisher: "erio", Name: core.LocalizedText{"en": "E"}, Versions: v}}}
	if p, _, ok := idx.find("erio", "codex", "1.0.0"); !ok || p.Name["en"] != "E" || p.ID() != "_erio__codex" {
		t.Fatalf("find erio/codex = %+v %v", p, ok)
	}
	if p, _, ok := idx.find("sub2api", "codex", "1.0.0"); !ok || p.ID() != "codex" {
		t.Fatalf("find codex = %+v %v", p, ok)
	}
	if _, _, ok := idx.find("acme", "codex", "1.0.0"); ok {
		t.Fatal("found codex of a publisher that does not list it")
	}
}

// compatible follows install-time validation: pre-release suffixes of the
// host version are ignored, empty or malformed ranges are incompatible.
func TestCompatible(t *testing.T) {
	cases := []struct {
		compat, host string
		want         bool
	}{
		{">=0.1.0 <0.2.0", "0.1.0", true},
		{">=0.1.0 <0.2.0", "0.1.5-dev", true},
		{">=0.1.0", "0.1.0-dev", true},
		{">=0.2.0", "0.1.9", false},
		{">=0.2.0", "0.2.0-rc.1", true},
		{"^0.1", "0.1.3+build.7", true},
		{"<0.1.0", "0.1.0-dev", false},
		{"", "0.1.0", false},
		{"not a range", "0.1.0", false},
		{">=0.1.0", "dev", false},
	}
	for _, tc := range cases {
		if got := Compatible(tc.compat, tc.host); got != tc.want {
			t.Errorf("Compatible(%q, %q) = %v, want %v", tc.compat, tc.host, got, tc.want)
		}
		// Must agree with manifest validation whenever the range is set.
		if tc.compat != "" {
			ok, err := pkg.HostCompatible(tc.compat, tc.host)
			if (err == nil && ok) != tc.want {
				t.Errorf("pkg.HostCompatible(%q, %q) disagrees", tc.compat, tc.host)
			}
		}
	}
}
