package market

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/install"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg/pkgtest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

type fakeInstaller struct{}

func (fakeInstaller) HostVersion() string { return "0.1.0" }

func TestMarket(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	mk := pkgtest.NewKey("market")
	pkgBytes := pkgtest.Build(pkgtest.Minimal("tool", "1.1.0", "acme"), pkgtest.NewKey("acme-1"))

	idx := Index{Version: 1, Plugins: []IndexPlugin{{
		Key: "tool", Name: core.LocalizedText{"en": "Tool"}, Publisher: "acme",
		Versions: []IndexVersion{
			{Version: "1.0.0", URL: "pkgs/tool-1.0.0.s2plugin", SHA256: "00", Size: 1, HostCompat: ">=0.1.0"},
			{Version: "1.1.0", URL: "pkgs/tool-1.1.0.s2plugin", SHA256: pkg.SHA256Hex(pkgBytes), Size: int64(len(pkgBytes)), HostCompat: ">=0.1.0"},
			{Version: "2.0.0", URL: "pkgs/tool-2.0.0.s2plugin", SHA256: "00", Size: 1, HostCompat: ">=0.2.0"},
		},
	}}}
	raw, _ := json.Marshal(idx)
	sig := SignIndex(raw, mk.Priv)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/m/index.json":
			hits++
			_, _ = w.Write(raw)
		case "/m/index.json.sig":
			_, _ = w.Write(sig)
		case "/m/pkgs/tool-1.1.0.s2plugin":
			_, _ = w.Write(pkgBytes)
		case "/m/pkgs/tool-1.0.0.s2plugin":
			_, _ = w.Write([]byte("x"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := New(db, fakeInstaller{}, srv.Client(), 1<<20)
	seed := fmt.Sprintf(`[{"name":"official","url":%q,"public_key":%q}]`, srv.URL+"/m/", mk.PubB64())
	if err := s.SeedSources(ctx, seed); err != nil {
		t.Fatal(err)
	}
	if err := s.SeedSources(ctx, seed); err != nil { // idempotent
		t.Fatal(err)
	}
	srcs, err := s.ListSources(ctx)
	if err != nil || len(srcs) != 1 {
		t.Fatalf("sources: %v %v", err, srcs)
	}
	list, err := s.ListPlugins(ctx, srcs[0].ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].LatestVersion != "1.1.0" || len(list[0].Versions) != 3 || list[0].Versions[0].Compatible {
		t.Fatalf("list = %+v", list)
	}
	// Cached.
	if _, err := s.ListPlugins(ctx, 0, false); err != nil || hits != 1 {
		t.Fatalf("cache: hits=%d err=%v", hits, err)
	}

	checkDownload(t, s, srcs[0].ID, srv.URL+"/m/", pkgBytes)

	// Tampered index is rejected.
	other := pkgtest.NewKey("other")
	if _, err := VerifyIndex(raw, SignIndex(raw, other.Priv), mk.PubB64()); err == nil {
		t.Fatal("wrong key accepted")
	}
	if _, err := VerifyIndex(append(raw, ' '), sig, mk.PubB64()); err == nil {
		t.Fatal("modified index accepted")
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

// checkDownload downloads the versions of tool from source srcID (index at
// base): 1.1.0 is pkgBytes, 1.0.0 fails its sha256, 9.9.9 is not listed.
func checkDownload(t *testing.T, s *Service, srcID int64, base string, pkgBytes []byte) {
	ctx := context.Background()
	got, opt, err := s.Download(ctx, srcID, "tool", "1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	want := install.UploadOptions{ExpectKey: "tool", ExpectVersion: "1.1.0", Source: fmt.Sprintf("market:%d", srcID),
		PackageURL: base + "pkgs/tool-1.1.0.s2plugin"}
	if !bytes.Equal(got, pkgBytes) || opt != want {
		t.Fatalf("download = %d bytes, opts=%+v", len(got), opt)
	}
	if _, _, err := s.Download(ctx, srcID, "tool", "1.0.0"); core.AsError(err).Code != "invalid_argument" {
		t.Fatalf("sha mismatch: %v", err)
	}
	if _, _, err := s.Download(ctx, srcID, "tool", "9.9.9"); core.AsError(err).Code != "not_found" {
		t.Fatalf("missing version: %v", err)
	}
}
