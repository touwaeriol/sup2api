package market

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// linkIndex lists one publisher, plugin and version whose links are link.
func linkIndex(link string) []byte {
	raw, _ := json.Marshal(Index{Version: indexFormat,
		Publishers: []IndexPublisher{{ID: "acme", Homepage: link, Trust: pkg.TrustVerified, Status: pkg.PublisherActive}},
		Plugins: []IndexPlugin{{Key: "tool", Publisher: "acme", Source: link,
			Versions: []IndexVersion{{Version: "1.0.0", URL: "x", ReleaseURL: link}}}}})
	return raw
}

// The links the console opens (homepage, source, release_url) are kept only
// as absolute https URLs, http too with AllowHTTP; any other is dropped.
func TestParseIndexLinks(t *testing.T) {
	cases := []struct {
		link      string
		allowHTTP bool
		keep      bool
	}{
		{"https://github.com/acme/tool", false, true},
		{"http://github.com/acme/tool", false, false},
		{"http://github.com/acme/tool", true, true},
		{"javascript:alert(1)", true, false},
		{"//github.com/acme", true, false},
		{"/relative", true, false},
		{"ftp://example.com/x", true, false},
	}
	for _, c := range cases {
		idx, err := parseIndex(linkIndex(c.link), c.allowHTTP)
		if err != nil {
			t.Fatal(err)
		}
		want := map[bool]string{true: c.link}[c.keep]
		p, v := idx.Plugins[0], idx.Plugins[0].Versions[0]
		if idx.Publishers[0].Homepage != want || p.Source != want || v.ReleaseURL != want {
			t.Errorf("%q (allow http %v): %+v %+v", c.link, c.allowHTTP, idx.Publishers[0], p)
		}
	}
}

// A market fetch follows a redirect only to a URL a market may have: an
// https index redirected to plain http fails.
func TestMarketRedirect(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(testIndex(nil)) }))
	t.Cleanup(plain.Close)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := map[string]string{"/http/index.json": plain.URL + "/index.json", "/https/index.json": "/m/index.json"}[r.URL.Path]
		if target == "" {
			_, _ = w.Write(testIndex(nil))
			return
		}
		http.Redirect(w, r, target, http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	s := New(testutil.DB(t), fakeInstaller{}, srv.Client(), Options{})
	ctx := context.Background()
	if _, err := s.fetchIndex(ctx, srv.URL+"/https/index.json"); err != nil {
		t.Fatalf("https redirect: %v", err)
	}
	if _, err := s.fetchIndex(ctx, srv.URL+"/http/index.json"); err == nil {
		t.Fatal("redirect to http followed")
	}
}

// An install reads the index afresh, whatever the cache holds.
func TestDownloadFreshIndex(t *testing.T) {
	ctx := context.Background()
	s, src, _, _, hits := newMarket(t)
	if _, err := s.ListPlugins(ctx, src, false); err != nil || *hits != 1 {
		t.Fatalf("list: hits=%d %v", *hits, err)
	}
	if _, _, err := s.Download(ctx, src, "tool", "1.1.0"); err != nil || *hits != 2 {
		t.Fatalf("download: hits=%d %v", *hits, err)
	}
}
