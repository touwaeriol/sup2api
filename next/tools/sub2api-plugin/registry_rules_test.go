package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// failCase is a pull request the check must reject.
type failCase struct {
	edit   func(t *testing.T, f *regFixture, head string)
	author string
	want   string
}

func checkFailCases() map[string]failCase {
	return map[string]failCase{
		"non-member new plugin": {func(t *testing.T, f *regFixture, head string) {
			raw, _ := os.ReadFile(filepath.Join(head, "plugins", "tester", "demo", "plugin.json"))
			writeFile(t, filepath.Join(head, "plugins", "tester", "demo_two", "plugin.json"), strings.Replace(string(raw), `"demo"`, `"demo_two"`, 1))
		}, "mallory", "plugins/tester/demo_two: mallory is not in the github list of publishers/tester.json"},
		"revoked publisher": {func(t *testing.T, f *regFixture, head string) {
			revoke(t, f.base)
			revoke(t, head)
			runOK(t, "registry", "add-version", "--registry", head, "--plugin", demoRef)
		}, "alice", "plugins/tester/demo: publisher tester is revoked"},
		"deprecation removed": {func(t *testing.T, f *regFixture, head string) {
			v := readVersionFile(t, f.base, demoRef, "0.1.0")
			v.Deprecated = &deprecation{Reason: map[string]string{"zh": "旧", "en": "Old"}}
			writeVersionFile(t, f.base, demoRef, v)
			v.Deprecated = nil
			writeVersionFile(t, head, demoRef, v)
		}, "alice", "a deprecation cannot be removed"},
		"symlinked versions": {func(t *testing.T, f *regFixture, head string) {
			moveBehindSymlink(t, filepath.Join(head, "plugins", "tester", "demo", "versions"))
		}, "alice", "plugins/tester/demo/versions: not a directory"},
		"control character": {func(t *testing.T, f *regFixture, head string) {
			writeFile(t, filepath.Join(head, "notes\n.txt"), "x")
		}, "alice", `"notes\n.txt": a path with control characters`},
	}
}

// moveBehindSymlink moves dir out of the checkout and links it back.
func moveBehindSymlink(t *testing.T, dir string) {
	t.Helper()
	outside := filepath.Join(t.TempDir(), filepath.Base(dir))
	if err := os.Rename(dir, outside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, dir); err != nil {
		t.Fatal(err)
	}
}

func revoke(t *testing.T, root string) {
	t.Helper()
	p := filepath.Join(root, "publishers", "tester.json")
	raw, _ := os.ReadFile(p)
	writeFile(t, p, strings.Replace(string(raw), `"active"`, `"revoked"`, 1))
}

func TestRegistryCheckFailures(t *testing.T) {
	for name, tc := range checkFailCases() {
		t.Run(name, func(t *testing.T) {
			f := newRegFixture(t)
			head := f.head(t)
			tc.edit(t, f, head)
			if res := f.check(t, head, tc.author, false); !strings.Contains(joined(res.Errors), tc.want) {
				t.Fatalf("errors = %q, want %q", res.Errors, tc.want)
			}
		})
	}
}

func TestRegistrySymlinksAreRefused(t *testing.T) {
	f := newRegFixture(t)
	if err := os.Symlink("/etc/hostname", filepath.Join(f.base, "publishers", "other.json")); err != nil {
		t.Fatal(err)
	}
	if msg := runFail(t, "index", "--registry", f.base); !strings.Contains(msg, `publishers: unexpected entry "other.json"`) {
		t.Fatalf("index: %s", msg)
	}
	if _, err := treeHashes(f.base); err == nil || !strings.Contains(err.Error(), "publishers/other.json: not a regular file") {
		t.Fatalf("tree: %v", err)
	}
}

func TestRegistryCheckBaseFailingTodaysRules(t *testing.T) {
	f := newRegFixture(t)
	head := f.head(t)
	runOK(t, "registry", "add-version", "--registry", head, "--plugin", demoRef)
	writeFile(t, filepath.Join(f.base, "plugins", "tester", "demo", "NOTES"), "an old stray file")
	res := f.check(t, head, "alice", true)
	if !res.NeedsManualReview || !strings.Contains(joined(res.ManualReview), "the base does not pass the current checks") || len(res.Versions) != 1 {
		t.Fatalf("result = %+v", res)
	}
}

func TestRegistryCommandsRefuseExtraArguments(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{
		{"index", "--registry", root, "extra"},
		{"registry", "codeowners", "--registry", root, "extra"},
		{"registry", "add-version", "--registry", root, "--plugin", demoRef, "extra"},
		{"registry", "check", "--registry", root, "--base", root, "--author", "a", "--json", "extra"},
	} {
		if msg := runFail(t, args...); !strings.Contains(msg, `unexpected argument "extra"`) {
			t.Errorf("%v: %s", args, msg)
		}
	}
}

func TestRegistryFieldRules(t *testing.T) {
	pub := regPublisher{indexPublisher: indexPublisher{ID: "p", Name: map[string]string{"zh": "p", "en": "p"}, Trust: "community", Status: "active"}}
	for logins, want := range map[string]string{
		"":                      "at least one",
		"a-b,A-B":               "listed twice",
		"-a":                    "invalid",
		"a--b":                  "invalid",
		strings.Repeat("a", 40): "invalid",
		strings.Repeat("a", 39): "",
		"alice,bob-1,Carol":     "",
	} {
		pub.GitHub = splitList(logins)
		if got := joined(checkPublisher("p", &pub)); (want == "") != (got == "") || !strings.Contains(got, want) {
			t.Errorf("github %q: %q, want %q", logins, got, want)
		}
	}
	for repo, ok := range map[string]bool{"acme/demo": true, "acme/..": false, "acme/.": false, "-acme/demo": false, "acme/de.mo": true} {
		if validRepo(repo) != ok {
			t.Errorf("repo %q valid = %v", repo, !ok)
		}
	}
	v := regVersion{Version: "1.0.0+build.1"}
	if !strings.Contains(joined(checkVersion("1.0.0+build.1", &v)), "without build metadata") {
		t.Error("build metadata accepted")
	}
}

func TestCodeSpan(t *testing.T) {
	for in, want := range map[string]string{
		"plugins/demo":  "`plugins/demo`",
		"a`b":           "``a`b``",
		"`x``":          "``` `x`` ```",
		"line\nbreak":   "`line�break`",
		"[x](http://e)": "`[x](http://e)`",
	} {
		if got := codeSpan(in); got != want {
			t.Errorf("codeSpan(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDownloadBudget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 100)))
	}))
	defer srv.Close()
	budget := int64(150)
	r := &remote{client: srv.Client(), budget: &budget}
	p, err := r.download(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(p)
	if _, err := r.download(srv.URL); err == nil || !strings.Contains(err.Error(), "exceeds 50 bytes") {
		t.Fatalf("second download: %v", err)
	}
	budget = 0
	if _, err := r.download(srv.URL); err == nil || !strings.Contains(err.Error(), "download budget") {
		t.Fatalf("spent budget: %v", err)
	}
}
