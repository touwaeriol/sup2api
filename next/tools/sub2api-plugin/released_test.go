package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// releasedFixture is a plugins directory holding demo (publisher tester)
// and an index served over HTTP that registers versions of it.
type releasedFixture struct {
	plugins string
	digest  string // of plugins/demo
	index   string // served at url
	url     string
}

func newReleasedFixture(t *testing.T) *releasedFixture {
	t.Helper()
	f := &releasedFixture{plugins: t.TempDir()}
	writeFile(t, filepath.Join(f.plugins, "demo", "manifest.json"), demoManifest)
	writeFile(t, filepath.Join(f.plugins, "demo", "main.go"), "package main")
	var err error
	if f.digest, err = pluginDigest(filepath.Join(f.plugins, "demo")); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(f.index))
	}))
	t.Cleanup(srv.Close)
	f.url = srv.URL + "/index.json"
	useClient(t, srv.Client())
	return f
}

// useClient makes the tool's HTTP requests with c.
func useClient(t *testing.T, c *http.Client) {
	saved := newRemote
	newRemote = func() *remote { return &remote{client: c} }
	t.Cleanup(func() { newRemote = saved })
}

// register serves an index listing demo of publisher pub at version ver
// with digest ("" for none).
func (f *releasedFixture) register(pub, ver, digest string) {
	v := `{"version": "` + ver + `"`
	if digest != "" {
		v += `, "source_digest": "` + digest + `"`
	}
	f.index = `{"version": 2, "publishers": [], "plugins": [{"key": "demo", "publisher": "` + pub + `", "versions": [` + v + `}]}]}`
}

func (f *releasedFixture) check(t *testing.T, extra ...string) (string, bool) {
	t.Helper()
	var out, errb strings.Builder
	code := run(append([]string{"released-check", "--plugins", f.plugins, "--index", f.url}, extra...), &out, &errb)
	return out.String() + errb.String(), code == 0
}

func TestReleasedCheck(t *testing.T) {
	other := "sha256:" + strings.Repeat("0", 64)
	cases := map[string]struct {
		pub, ver, digest string
		ok               bool
	}{
		"same sources":        {"tester", "0.1.0", "same", true},
		"no source_digest":    {"tester", "0.1.0", "", true},
		"bumped since":        {"tester", "0.0.9", other, true},
		"another publisher":   {"sub2api", "0.1.0", other, true},
		"changed, not bumped": {"tester", "0.1.0", other, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newReleasedFixture(t)
			digest := tc.digest
			if digest == "same" {
				digest = f.digest
			}
			f.register(tc.pub, tc.ver, digest)
			out, ok := f.check(t, "--strict")
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v: %s", ok, tc.ok, out)
			}
			if want := "tester/demo 0.1.0 is registered with other sources"; !ok && !strings.Contains(out, want) {
				t.Fatalf("message %s, want %q", out, want)
			}
		})
	}
}

func TestReleasedCheckUnreadableIndex(t *testing.T) {
	f := newReleasedFixture(t)
	f.index = "not json"
	if out, ok := f.check(t); !ok || !strings.Contains(out, "warning: released versions not checked") {
		t.Fatalf("local run: ok %v: %s", ok, out)
	}
	if out, ok := f.check(t, "--strict"); ok || !strings.Contains(out, "index.json") {
		t.Fatalf("strict: ok %v: %s", ok, out)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.url = "http://" + l.Addr().String() + "/index.json"
	l.Close()
	useClient(t, &http.Client{})
	if out, ok := f.check(t); !ok || !strings.Contains(out, "warning") {
		t.Fatalf("unreachable, local run: ok %v: %s", ok, out)
	}
	if out, ok := f.check(t, "--strict"); ok {
		t.Fatalf("unreachable, strict: %s", out)
	}
}

func TestPackWritesSourceDigest(t *testing.T) {
	dir := demoPlugin(t)
	pkg := strings.TrimSpace(runOK(t, "pack", "--dir", dir, "--out-dir", t.TempDir()))
	info, err := inspectPackage(pkg)
	if err != nil {
		t.Fatal(err)
	}
	want, err := pluginDigest(dir)
	if err != nil || info.sourceDigest != want {
		t.Fatalf("source digest %q, want %q (%v)", info.sourceDigest, want, err)
	}
	if _, err := packageSourceDigest(map[string][]byte{sourceFile: []byte(`{"digest": "md5:x"}`)}); err == nil {
		t.Fatal("malformed source.json accepted")
	}
}
