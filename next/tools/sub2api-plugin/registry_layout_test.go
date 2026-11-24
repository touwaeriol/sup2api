package main

import (
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
