package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGitHub serves the releases of acme/demo: the REST API and the asset
// downloads, each tag holding one package. Tests may edit releases.
type fakeGitHub struct {
	pkgs     map[string]string // tag -> package file
	releases map[string]*ghRelease
	latest   string
	auth     []string // Authorization headers the API saw
}

func newFakeGitHub(t *testing.T, pkgs map[string]string, latest string) *fakeGitHub {
	g := &fakeGitHub{pkgs: pkgs, releases: map[string]*ghRelease{}, latest: latest}
	for tag, p := range pkgs {
		sum, _, err := fileSHA256(p)
		if err != nil {
			t.Fatal(err)
		}
		u := "https://github.com/acme/demo/releases/download/" + tag + "/" + filepath.Base(p)
		g.releases[tag] = &ghRelease{TagName: tag, HTMLURL: releasePage("acme/demo", tag), Assets: []ghAsset{
			{Name: "notes.txt", URL: u + ".txt"}, {Name: filepath.Base(p), URL: u, Digest: "sha256:" + sum}}}
	}
	return g
}

func (g *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if rest, ok := strings.CutPrefix(r.URL.Path, "/repos/acme/demo/releases/"); ok {
		g.auth = append(g.auth, r.Header.Get("Authorization"))
		tag := strings.TrimPrefix(rest, "tags/")
		if rest == "latest" {
			tag = g.latest
		}
		if rel := g.releases[tag]; rel != nil {
			_ = json.NewEncoder(w).Encode(rel)
			return
		}
	}
	rest, _ := strings.CutPrefix(r.URL.Path, "/acme/demo/releases/download/")
	tag, file, _ := strings.Cut(rest, "/")
	if p := g.pkgs[tag]; p != "" && filepath.Base(p) == file {
		http.ServeFile(w, r, p)
		return
	}
	http.NotFound(w, r)
}

// rewriteTransport sends every request to one test server.
type rewriteTransport struct{ target *url.URL }

func (rt rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = rt.target.Scheme, rt.target.Host
	return http.DefaultTransport.RoundTrip(r)
}

// regFixture is a base registry listing tester/demo 0.1.0 (GitHub login
// alice) and a fake GitHub releasing demo 0.1.0 and 0.2.0, both packed from
// dir.
type regFixture struct {
	gh   *fakeGitHub
	base string
	dir  string
}

// demoRef is the fixture plugin's ref.
const demoRef = "tester/demo"

func newRegFixture(t *testing.T) *regFixture {
	t.Helper()
	dir, out := demoPlugin(t), t.TempDir()
	p1 := strings.TrimSpace(runOK(t, "pack", "--dir", dir, "--out-dir", out))
	p2 := strings.TrimSpace(runOK(t, "pack", "--dir", dir, "--overlay", "testdata/v2", "--out-dir", out))
	gh := newFakeGitHub(t, map[string]string{"v0.1.0": p1, "demo-v0.2.0": p2}, "demo-v0.2.0")
	srv := httptest.NewServer(gh)
	t.Cleanup(srv.Close)
	target, _ := url.Parse(srv.URL)
	saved := newRemote
	newRemote = func() *remote {
		return &remote{client: &http.Client{Transport: rewriteTransport{target}}, apiBase: "https://api.github.com", token: "tok"}
	}
	t.Cleanup(func() { newRemote = saved })
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "publishers", "tester.json"), `{"id": "tester", "name": {"zh": "测试", "en": "Tester"},
  "github": ["alice"], "trust": "community", "status": "active"}`)
	writeFile(t, filepath.Join(base, "plugins", "tester", "demo", "plugin.json"), `{"key": "demo", "publisher": "tester",
  "name": {"zh": "演示", "en": "Demo"}, "description": {"zh": "演示插件", "en": "A demo"}, "repo": "acme/demo"}`)
	runOK(t, "registry", "add-version", "--registry", base, "--plugin", demoRef, "--tag", "v0.1.0")
	return &regFixture{gh: gh, base: base, dir: dir}
}

// head copies the base registry for a pull request.
func (f *regFixture) head(t *testing.T) string {
	t.Helper()
	head := filepath.Join(t.TempDir(), "head")
	if err := os.CopyFS(head, os.DirFS(f.base)); err != nil {
		t.Fatal(err)
	}
	return head
}

// check runs `registry check --json` (with extra flags) and decodes the
// result.
func (f *regFixture) check(t *testing.T, head, author string, wantOK bool, extra ...string) checkResult {
	t.Helper()
	var out, errb strings.Builder
	args := []string{"registry", "check", "--registry", head, "--base", f.base, "--author", author, "--json"}
	code := run(append(args, extra...), &out, &errb)
	var res checkResult
	if err := json.Unmarshal([]byte(out.String()), &res); err != nil {
		t.Fatalf("exit %d, output %q, stderr %s: %v", code, out.String(), errb.String(), err)
	}
	if (code == 0) != wantOK || res.OK != wantOK {
		t.Fatalf("exit %d, result %+v, want ok=%v", code, res, wantOK)
	}
	return res
}

func readVersionFile(t *testing.T, root, ref, ver string) regVersion {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "plugins", filepath.FromSlash(ref), "versions", ver+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var v regVersion
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func writeVersionFile(t *testing.T, root, ref string, v regVersion) {
	t.Helper()
	raw, err := encodeJSON(v)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "plugins", filepath.FromSlash(ref), "versions", v.Version+".json"), string(raw))
}

func TestRegistryAddVersion(t *testing.T) {
	f := newRegFixture(t)
	v := readVersionFile(t, f.base, demoRef, "0.1.0")
	sum, size, err := fileSHA256(f.gh.pkgs["v0.1.0"])
	if err != nil {
		t.Fatal(err)
	}
	digest, err := pluginDigest(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	want := regVersion{Version: "0.1.0", URL: "https://github.com/acme/demo/releases/download/v0.1.0/demo-0.1.0.s2plugin",
		SHA256: sum, Size: size, HostCompat: ">=0.1.0 <0.2.0", ReleaseURL: "https://github.com/acme/demo/releases/tag/v0.1.0", SourceDigest: digest}
	if v != want || f.gh.auth[0] != "Bearer tok" {
		t.Fatalf("version = %+v, auth %q", v, f.gh.auth)
	}
	if msg := runFail(t, "registry", "add-version", "--registry", f.base, "--plugin", demoRef, "--tag", "v0.1.0"); !strings.Contains(msg, "already exists") {
		t.Fatalf("second add: %s", msg)
	}
	runOK(t, "registry", "add-version", "--registry", f.base, "--plugin", demoRef)
	if v := readVersionFile(t, f.base, demoRef, "0.2.0"); !strings.HasSuffix(v.URL, "/demo-v0.2.0/demo-0.2.0.s2plugin") || v.SourceDigest != digest {
		t.Fatalf("latest = %+v", v)
	}
	// A bare key names a plugin of the official publisher.
	if msg := runFail(t, "registry", "add-version", "--registry", f.base, "--plugin", "demo"); !strings.Contains(msg, "no plugins/sub2api/demo/plugin.json") {
		t.Fatalf("bare key: %s", msg)
	}
}

func TestRegistryCheckNewVersion(t *testing.T) {
	f := newRegFixture(t)
	head := f.head(t)
	runOK(t, "registry", "add-version", "--registry", head, "--plugin", demoRef)
	res := f.check(t, head, "Alice", true)
	if res.NeedsManualReview || res.Publisher != "tester" || len(res.Versions) != 1 || res.Versions[0].Plugin != demoRef {
		t.Fatalf("result = %+v", res)
	}
	v := res.Versions[0]
	if v.Previous != "0.1.0" || v.PreviousError != "" || strings.Join(v.Changes.Migrations.Added, ",") != "0002_more.sql" ||
		len(v.Changes.HostPermissions.Added) != 0 || len(v.Changes.HighRiskAdded) != 0 {
		t.Fatalf("report = %+v", v)
	}
	summary := filepath.Join(t.TempDir(), "summary.md")
	out := runOK(t, "registry", "check", "--registry", head, "--base", f.base, "--author", "alice", "--summary", summary)
	md, _ := os.ReadFile(summary)
	if string(md) != out || !strings.Contains(out, "Migrations: added `0002_more.sql`") || !strings.Contains(out, "Compared with: 0.1.0") {
		t.Fatalf("summary:\n%s", md)
	}
	if res := f.check(t, head, "bob", false); !strings.Contains(strings.Join(res.Errors, "\n"), "bob is not in the github list") {
		t.Fatalf("bob: %+v", res)
	}
}

func TestRegistryAddVersionRejectsRelease(t *testing.T) {
	cases := map[string]struct {
		edit func(rel *ghRelease)
		want string
	}{
		"digest":    {func(rel *ghRelease) { rel.Assets[1].Digest = "sha256:" + strings.Repeat("0", 64) }, "asset digest"},
		"two":       {func(rel *ghRelease) { rel.Assets[0].Name = "x.s2plugin" }, "has 2 .s2plugin assets"},
		"none":      {func(rel *ghRelease) { rel.Assets = rel.Assets[:1] }, "has 0 .s2plugin assets"},
		"tag":       {func(rel *ghRelease) { rel.TagName = "release-1" }, `tag "release-1" must be one of v0.2.0, demo-v0.2.0, plugin-demo-v0.2.0`},
		"html_url":  {func(rel *ghRelease) { rel.HTMLURL = releasePage("acme/other", rel.TagName) }, "html_url"},
		"asset tag": {func(rel *ghRelease) { rel.TagName, rel.HTMLURL = "v0.2.0", releasePage("acme/demo", "v0.2.0") }, "is not under the tag"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newRegFixture(t)
			tc.edit(f.gh.releases["demo-v0.2.0"])
			if msg := runFail(t, "registry", "add-version", "--registry", f.base, "--plugin", demoRef); !strings.Contains(msg, tc.want) {
				t.Fatalf("message %q, want %q", msg, tc.want)
			}
		})
	}
}
