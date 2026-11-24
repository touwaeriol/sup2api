package main

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRegistryPluginIdentity(t *testing.T) {
	base := regPlugin{Name: map[string]string{"zh": "x", "en": "x"}, Description: map[string]string{"zh": "x", "en": "x"}, Repo: "acme/demo"}
	cases := map[string]struct {
		dir, publisher, key, want string
	}{
		"official":         {"sub2api/codex", "sub2api", "codex", ""},
		"third party":      {"erio/codex", "erio", "codex", ""},
		"other directory":  {"erio/codex", "acme", "codex", "must equal the directory plugins/erio/codex"},
		"bad publisher":    {"a-b/codex", "a-b", "codex", "publisher id"},
		"double underline": {"a__b/codex", "a__b", "codex", "publisher id"},
		"id too long":      {"publisher_one/plugin_key_twelve", "publisher_one", "plugin_key_twelve", "longer than 30 bytes"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			pl := base
			pl.Publisher, pl.Key = tc.publisher, tc.key
			got := joined(checkPlugin(tc.dir, &pl))
			if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
				t.Fatalf("problems %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRegistryLayout(t *testing.T) {
	f := newRegFixture(t)
	writeFile(t, filepath.Join(f.base, "plugins", "tester", "README.md"), "x")
	if msg := runFail(t, "index", "--registry", f.base); !strings.Contains(msg, "plugins/tester/README.md: not a directory") {
		t.Fatalf("stray file in a publisher directory: %s", msg)
	}
	head := f.head(t)
	writeFile(t, filepath.Join(head, "plugins", "other", "demo", "plugin.json"), `{"key": "demo", "publisher": "tester",
  "name": {"zh": "演示", "en": "Demo"}, "description": {"zh": "演示插件", "en": "A demo"}, "repo": "acme/demo"}`)
	if msg := runFail(t, "index", "--registry", head); !strings.Contains(msg, `publisher "tester" and key "demo" must equal the directory plugins/other/demo`) {
		t.Fatalf("plugin under another publisher: %s", msg)
	}
}

func TestRegistryPluginOrder(t *testing.T) {
	reg := &registry{plugins: map[string]*regEntry{}}
	for _, ref := range []string{"acme/codex", "sub2api/zeta", "erio/alpha", "sub2api/codex", "acme/beta"} {
		pub, key, _ := strings.Cut(ref, refSep)
		reg.plugins[ref] = &regEntry{plugin: regPlugin{Publisher: pub, Key: key}}
	}
	var got []string
	for _, e := range reg.sortedPlugins() {
		got = append(got, e.ref())
	}
	want := []string{"sub2api/codex", "sub2api/zeta", "acme/beta", "acme/codex", "erio/alpha"}
	if !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestRegistryCheckPromotion(t *testing.T) {
	f := newRegFixture(t)
	head := f.head(t)
	runOK(t, "registry", "add-version", "--registry", head, "--plugin", demoRef)
	writeFile(t, filepath.Join(head, "publishers", "other.json"), otherPublisher)
	f.gh.pkgs = nil // a promotion downloads nothing
	res := f.check(t, head, "touwaeriol", true, "--promotion")
	if len(res.Versions) != 0 || !strings.Contains(joined(res.ManualReview), "plugins/tester/demo/versions/0.2.0.json: version 0.2.0 promoted from develop") {
		t.Fatalf("promotion: %+v", res)
	}
	v := readVersionFile(t, head, demoRef, "0.1.0")
	v.Size++
	writeVersionFile(t, head, demoRef, v)
	if res := f.check(t, head, "touwaeriol", false, "--promotion"); !strings.Contains(joined(res.Errors), "may only add deprecated") {
		t.Fatalf("promotion changing a published version: %+v", res)
	}
}

// addAcmeDemo registers publisher acme (GitHub login carol) with its own
// plugin keyed demo, next to tester/demo.
func addAcmeDemo(t *testing.T, root string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "publishers", "acme.json"), `{"id": "acme", "name": {"zh": "Acme", "en": "Acme"},
  "github": ["carol"], "trust": "community", "status": "active"}`)
	writeFile(t, filepath.Join(root, "plugins", "acme", "demo", "plugin.json"), `{"key": "demo", "publisher": "acme",
  "name": {"zh": "另一个演示", "en": "Another demo"}, "description": {"zh": "同名插件", "en": "Same key"}, "repo": "acme/demo"}`)
}

func TestRegistrySameKeyTwoPublishers(t *testing.T) {
	f := newRegFixture(t)
	head := f.head(t)
	addAcmeDemo(t, head)
	res := f.check(t, head, "carol", true)
	if res.Publisher != "acme" || !strings.Contains(joined(res.ManualReview), "plugins/acme/demo: new plugin") {
		t.Fatalf("new acme/demo: %+v", res)
	}
	addAcmeDemo(t, f.base)
	head = f.head(t)
	runOK(t, "registry", "add-version", "--registry", head, "--plugin", demoRef)
	res = f.check(t, head, "alice", true)
	if res.NeedsManualReview || res.Publisher != "tester" || len(res.Versions) != 1 || res.Versions[0].Plugin != demoRef {
		t.Fatalf("tester/demo version next to acme/demo: %+v", res)
	}
	var idx marketIndex
	if err := json.Unmarshal([]byte(runOK(t, "index", "--registry", head)), &idx); err != nil {
		t.Fatal(err)
	}
	if len(idx.Plugins) != 2 || idx.Plugins[0].Publisher != "acme" || len(idx.Plugins[0].Versions) != 0 ||
		idx.Plugins[1].Publisher != "tester" || len(idx.Plugins[1].Versions) != 2 {
		t.Fatalf("index plugins = %+v", idx.Plugins)
	}
	if got := runOK(t, "registry", "codeowners", "--registry", head); !strings.Contains(got, "/plugins/acme/ @carol\n") {
		t.Fatalf("codeowners:\n%s", got)
	}
}

// stripSource removes source.json from the package released under tag, as
// packed before source digests, and updates the release's asset digest.
func stripSource(t *testing.T, f *regFixture, tag string) {
	t.Helper()
	p := f.gh.pkgs[tag]
	files, err := readPackage(p)
	if err != nil {
		t.Fatal(err)
	}
	delete(files, sourceFile)
	if err := writePackage(p, files); err != nil {
		t.Fatal(err)
	}
	sum, _, err := fileSHA256(p)
	if err != nil {
		t.Fatal(err)
	}
	f.gh.releases[tag].Assets[1].Digest = "sha256:" + sum
}

func TestRegistryCheckSourceDigestWithoutSourceJSON(t *testing.T) {
	f := newRegFixture(t)
	stripSource(t, f, "demo-v0.2.0")
	head := f.head(t)
	runOK(t, "registry", "add-version", "--registry", head, "--plugin", demoRef)
	v := readVersionFile(t, head, demoRef, "0.2.0")
	if v.SourceDigest != "" {
		t.Fatalf("a package without source.json recorded %q", v.SourceDigest)
	}
	f.check(t, head, "alice", true)
	v.SourceDigest = "sha256:" + strings.Repeat("0", 64)
	writeVersionFile(t, head, demoRef, v)
	if res := f.check(t, head, "alice", false); !strings.Contains(joined(res.Errors), `source_digest is "sha256:`) ||
		!strings.Contains(joined(res.Errors), `expected ""`) {
		t.Fatalf("errors = %q", res.Errors)
	}
}
