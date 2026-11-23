package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const otherPublisher = `{"id": "other", "name": {"zh": "其他", "en": "Other"}, "github": ["carol"], "trust": "community", "status": "active"}`

func joined(lines []string) string { return strings.Join(lines, "\n") }

func TestRegistryCheckRejectsBadVersions(t *testing.T) {
	f := newRegFixture(t)
	good := f.head(t)
	runOK(t, "registry", "add-version", "--registry", good, "--plugin", "demo")
	v := readVersionFile(t, good, "demo", "0.2.0")
	for name, tc := range badVersionCases() {
		t.Run(name, func(t *testing.T) {
			head := f.head(t)
			bad := v
			tc.edit(&bad)
			writeVersionFile(t, head, "demo", bad)
			if res := f.check(t, head, "alice", false); !strings.Contains(joined(res.Errors), tc.want) {
				t.Fatalf("errors = %q, want %q", res.Errors, tc.want)
			}
		})
	}
}

// badVersionCase edits a correct new version file into a wrong one.
type badVersionCase struct {
	edit func(*regVersion)
	want string
}

func badVersionCases() map[string]badVersionCase {
	return map[string]badVersionCase{
		"sha256":      {func(v *regVersion) { v.SHA256 = strings.Repeat("0", 64) }, "sha256 is"},
		"host_compat": {func(v *regVersion) { v.HostCompat = ">=0.1.0" }, "host_compat is"},
		"other repo":  {func(v *regVersion) { v.URL = strings.Replace(v.URL, "acme/demo", "acme/evil", 1) }, "must be https://github.com/acme/demo/releases/download/"},
		"not github":  {func(v *regVersion) { v.URL = "https://example.com/demo-0.2.0.s2plugin" }, "must be https://github.com/"},
		"missing": {func(v *regVersion) {
			v.URL, v.ReleaseURL = strings.Replace(v.URL, "/demo-v0.2.0/", "/plugin-demo-v0.2.0/", 1), releasePage("acme/demo", "plugin-demo-v0.2.0")
		}, "404"},
		"release_url": {func(v *regVersion) { v.ReleaseURL = releasePage("acme/demo", "v0.1.0") }, `release_url "`},
		"other tag":   {func(v *regVersion) { v.URL = strings.Replace(v.URL, "demo-v0.2.0", "v0.1.0", 1) }, `tag "v0.1.0" must be one of`},
	}
}

func TestRegistryCheckPublishedVersionIsImmutable(t *testing.T) {
	f := newRegFixture(t)
	v := readVersionFile(t, f.base, "demo", "0.1.0")
	head := f.head(t)
	v.Deprecated = &deprecation{Reason: map[string]string{"zh": "请升级", "en": "Upgrade"}}
	writeVersionFile(t, head, "demo", v)
	if res := f.check(t, head, "alice", true); res.NeedsManualReview {
		t.Fatalf("deprecating: %+v", res)
	}
	v.Size++
	writeVersionFile(t, head, "demo", v)
	if res := f.check(t, head, "alice", false); !strings.Contains(joined(res.Errors), "may only add deprecated") {
		t.Fatalf("size change: %+v", res)
	}
	if err := os.Remove(filepath.Join(head, "plugins", "demo", "versions", "0.1.0.json")); err != nil {
		t.Fatal(err)
	}
	if res := f.check(t, head, "alice", true); !res.NeedsManualReview || !strings.Contains(joined(res.ManualReview), "version 0.1.0 deleted") {
		t.Fatalf("delete: %+v", res)
	}
}

// manualCase is a pull request that passes but needs manual review.
type manualCase struct {
	edit   func(t *testing.T, head string)
	author string
	want   string
}

func manualReviewCases() map[string]manualCase {
	return map[string]manualCase{
		"plugin.json": {func(t *testing.T, head string) {
			p := filepath.Join(head, "plugins", "demo", "plugin.json")
			raw, _ := os.ReadFile(p)
			writeFile(t, p, strings.Replace(string(raw), "A demo", "The demo", 1))
		}, "alice", "plugins/demo/plugin.json modified"},
		"publisher file": {func(t *testing.T, head string) {
			writeFile(t, filepath.Join(head, "publishers", "other.json"), otherPublisher)
		}, "carol", "publishers/other.json: publisher file added"},
		"workflow": {func(t *testing.T, head string) {
			writeFile(t, filepath.Join(head, ".github", "workflows", "x.yml"), "on: push")
		}, "alice", ".github/workflows/x.yml: added outside"},
		"new plugin": {func(t *testing.T, head string) {
			raw, _ := os.ReadFile(filepath.Join(head, "plugins", "demo", "plugin.json"))
			writeFile(t, filepath.Join(head, "plugins", "demo_two", "plugin.json"), strings.Replace(string(raw), `"demo"`, `"demo_two"`, 1))
		}, "alice", "plugins/demo_two: new plugin"},
	}
}

func TestRegistryCheckManualReview(t *testing.T) {
	f := newRegFixture(t)
	for name, tc := range manualReviewCases() {
		t.Run(name, func(t *testing.T) {
			head := f.head(t)
			tc.edit(t, head)
			res := f.check(t, head, tc.author, true)
			if !res.NeedsManualReview || !strings.Contains(joined(res.ManualReview), tc.want) {
				t.Fatalf("manual review = %q, want %q", res.ManualReview, tc.want)
			}
		})
	}
}

func TestRegistryCheckOnePublisherPerPullRequest(t *testing.T) {
	f := newRegFixture(t)
	head := f.head(t)
	runOK(t, "registry", "add-version", "--registry", head, "--plugin", "demo")
	writeFile(t, filepath.Join(head, "publishers", "other.json"), otherPublisher)
	if res := f.check(t, head, "alice", false); !strings.Contains(joined(res.Errors), "one publisher only; this one changes other, tester") {
		t.Fatalf("errors = %q", res.Errors)
	}
	head = f.head(t)
	writeFile(t, filepath.Join(head, "plugins", "demo", "plugin.json"), `{"key": "demo"}`)
	if res := f.check(t, head, "alice", false); !strings.Contains(joined(res.Errors), "plugins/demo/plugin.json") {
		t.Fatalf("invalid head: %q", res.Errors)
	}
}

func TestRegistryIndexAndCodeowners(t *testing.T) {
	f := newRegFixture(t)
	runOK(t, "registry", "add-version", "--registry", f.base, "--plugin", "demo")
	writeFile(t, filepath.Join(f.base, "publishers", "other.json"), otherPublisher)
	var idx map[string]any
	if err := json.Unmarshal([]byte(runOK(t, "index", "--registry", f.base)), &idx); err != nil {
		t.Fatal(err)
	}
	pubs, plugins := idx["publishers"].([]any), idx["plugins"].([]any)
	versions := plugins[0].(map[string]any)["versions"].([]any)
	if idx["version"] != float64(2) || len(pubs) != 2 || pubs[0].(map[string]any)["id"] != "other" ||
		pubs[1].(map[string]any)["github"] != nil || versions[0].(map[string]any)["version"] != "0.2.0" ||
		plugins[0].(map[string]any)["repo"] != "acme/demo" || plugins[0].(map[string]any)["deprecated"] != nil {
		t.Fatalf("index = %v", idx)
	}
	want := "* @touwaeriol\n/publishers/other.json @carol\n/publishers/tester.json @alice\n/plugins/demo/ @alice\n"
	if got := runOK(t, "registry", "codeowners", "--registry", f.base); got != want {
		t.Fatalf("codeowners =\n%s", got)
	}
	out := filepath.Join(t.TempDir(), "index.json")
	writeFile(t, filepath.Join(f.base, "plugins", "demo", "README.md"), "x")
	if msg := runFail(t, "index", "--registry", f.base, "--out", out); !strings.Contains(msg, `unexpected entry "README.md"`) {
		t.Fatalf("stray file: %s", msg)
	}
}

func TestRegistryImport(t *testing.T) {
	dir, pkgs, reg := demoPlugin(t), t.TempDir(), t.TempDir()
	p1 := strings.TrimSpace(runOK(t, "pack", "--dir", dir, "--out-dir", pkgs))
	p2 := strings.TrimSpace(runOK(t, "pack", "--dir", dir, "--overlay", "testdata/v2", "--out-dir", pkgs))
	runOK(t, "registry", "import", "--registry", reg, "--repo", "acme/demo", "--trust", "official", p1, p2)
	runFail(t, "registry", "import", "--registry", reg, "--repo", "acme/demo", p1)
	if v := readVersionFile(t, reg, "demo", "0.2.0"); v.URL != "demo-0.2.0.s2plugin" {
		t.Fatalf("url = %q", v.URL)
	}
	out := filepath.Join(t.TempDir(), "index.json")
	if msg := runFail(t, "index", "--registry", reg, "--out", out); !strings.Contains(msg, `url "demo-0.1.0.s2plugin" must be https://github.com/acme/demo/releases/download/`) {
		t.Fatalf("relative url without --dev: %s", msg)
	}
	runOK(t, "index", "--registry", reg, "--out", out, "--dev")
	raw, _ := os.ReadFile(out)
	var idx marketIndex
	if err := json.Unmarshal(raw, &idx); err != nil {
		t.Fatal(err)
	}
	p := idx.Plugins[0]
	if idx.Publishers[0].Trust != "official" || p.Name["zh"] != "演示" || p.Description["zh"] != "demo" || len(p.Versions) != 2 {
		t.Fatalf("index = %s", raw)
	}
}

func TestRegistryValidation(t *testing.T) {
	v := regVersion{Version: "1.0.0", URL: "https://github.com/a/b/releases/download/v1/b.s2plugin", SHA256: strings.Repeat("a", 64), Size: 1,
		HostCompat: ">=0.1.0", ReleaseURL: "https://github.com/a/b/releases/tag/v1"}
	if p := checkVersion("1.0.0", &v); len(p) != 0 {
		t.Fatal(p)
	}
	v.URL = "https://github.com/a/b/releases/download/v1/b.zip"
	if p := checkVersion("1.0.0", &v); len(p) != 1 || !strings.Contains(p[0], "url") {
		t.Fatal(p)
	}
	for key, ok := range map[string]bool{"azure_openai": true, "demo": true, "a__b": false, "trailing_": false, "Demo": false, "x": false, "core": false} {
		pl := regPlugin{Key: key}
		if got := !strings.Contains(joined(checkPlugin(key, &pl)), "key "); got != ok {
			t.Errorf("key %q valid = %v, want %v", key, got, ok)
		}
	}
}

func TestCompareManifests(t *testing.T) {
	old := manifestFacts{host: []string{"kv", "log"}, egress: []string{"a.example"}}
	cur := manifestFacts{host: []string{"kv", "net", "ledger.debit", "fs.data"}, egress: []string{"b.example"}}
	c := compareManifests(old, cur)
	if joined(c.HostPermissions.Added) != "fs.data\nledger.debit\nnet" || joined(c.HostPermissions.Removed) != "log" ||
		joined(c.HighRiskAdded) != "ledger.debit (critical)\nnet (high)" || joined(c.EgressDomains.Removed) != "a.example" {
		t.Fatalf("changes = %+v", c)
	}
	var b strings.Builder
	writeVersionReport(&b, versionReport{Plugin: "demo", Version: "1.0.0", Previous: "0.9.0", Changes: c})
	if !strings.Contains(b.String(), "**High-risk host permissions added: `ledger.debit (critical)`, `net (high)`**") ||
		!strings.Contains(b.String(), "- User permissions: no change") {
		t.Fatalf("markdown:\n%s", b.String())
	}
}
