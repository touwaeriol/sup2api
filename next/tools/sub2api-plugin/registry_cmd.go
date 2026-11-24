package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

const registryUsage = "usage: sub2api-plugin registry check|add-version|codeowners|import --registry <dir> ..."

// cmdRegistry runs the registry subcommands (CONTRACTS §135).
func cmdRegistry(args []string, stdout, stderr io.Writer) error {
	sub := ""
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	cmds := map[string]func([]string, io.Writer, io.Writer) error{
		"check":       cmdRegistryCheck,
		"add-version": cmdRegistryAddVersion,
		"codeowners":  cmdRegistryCodeowners,
		"import":      cmdRegistryImport,
	}
	if fn, ok := cmds[sub]; ok {
		return fn(args, stdout, stderr)
	}
	return errors.New(registryUsage)
}

// cmdRegistryAddVersion writes versions/<version>.json for a plugin's
// GitHub release asset.
func cmdRegistryAddVersion(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("registry add-version", stderr)
	root := fs.String("registry", "", "registry checkout (required)")
	plugin := fs.String("plugin", "", "plugin as <publisher>/<key>, or a bare key of publisher sub2api (required)")
	tag := fs.String("tag", "", "release tag (default: the latest release)")
	if err := parseNoArgs(fs, args); err != nil {
		return err
	}
	if *root == "" || *plugin == "" {
		return errors.New("--registry and --plugin are required")
	}
	written, err := addVersion(*root, parseRegRef(*plugin), *tag, newRemote())
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, written)
	return nil
}

func addVersion(root, ref, tag string, r *remote) (string, error) {
	reg, err := loadRegistry(root, false)
	if err != nil {
		return "", err
	}
	e, ok := reg.plugins[ref]
	if !ok {
		return "", fmt.Errorf("no %s/%s", pluginDir(ref), regPluginFile)
	}
	_, v, err := r.releaseVersion(&e.plugin, tag)
	if err != nil {
		return "", err
	}
	return writeVersion(root, ref, v)
}

// cmdRegistryCodeowners prints a CODEOWNERS file: each active publisher's
// GitHub logins own its publisher file and its plugins directory.
func cmdRegistryCodeowners(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("registry codeowners", stderr)
	root := fs.String("registry", "", "registry checkout (required)")
	def := fs.String("default-owner", "touwaeriol", "GitHub login owning everything else")
	if err := parseNoArgs(fs, args); err != nil {
		return err
	}
	if *root == "" {
		return errors.New("--registry is required")
	}
	reg, err := loadRegistry(*root, false)
	if err != nil {
		return err
	}
	_, err = io.WriteString(stdout, codeowners(reg, *def))
	return err
}

func codeowners(reg *registry, def string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "* @%s\n", def)
	for _, id := range slices.Sorted(maps.Keys(reg.publishers)) {
		if owners := ownersOf(reg.publishers[id]); owners != "" {
			fmt.Fprintf(&b, "/%s/%s%s %s\n", regPublishersDir, id, regJSONExt, owners)
			fmt.Fprintf(&b, "/%s/%s/ %s\n", regPluginsDir, id, owners)
		}
	}
	return b.String()
}

func ownersOf(p *regPublisher) string {
	if p == nil || p.Status != "active" || len(p.GitHub) == 0 {
		return ""
	}
	return "@" + strings.Join(p.GitHub, " @")
}

// importLogin is the GitHub login of a publisher file `registry import`
// creates: a local market has no publisher on GitHub, and the list may not
// be empty.
const importLogin = "local-dev"

// importOptions configure `registry import`, which registers local
// packages for a development market (no download, no GitHub).
type importOptions struct {
	root, repo, trust, urlPrefix string
}

func cmdRegistryImport(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("registry import", stderr)
	var o importOptions
	fs.StringVar(&o.root, "registry", "", "registry directory to add to (required)")
	fs.StringVar(&o.repo, "repo", "", "owner/name written to a new plugin.json (required)")
	fs.StringVar(&o.trust, "trust", "community", "trust of a new publisher file: official, verified or community")
	fs.StringVar(&o.urlPrefix, "url-prefix", "", "prefix of each version url (default: the package file name, relative to index.json)")
	pkgs, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if o.root == "" || o.repo == "" || len(pkgs) == 0 {
		return errors.New("usage: registry import --registry <dir> --repo <owner/name> [--trust <level>] [--url-prefix <url>] <file.s2plugin>...")
	}
	for _, pkg := range pkgs {
		written, err := importPackage(o, pkg)
		if err != nil {
			return fmt.Errorf("%s: %w", pkg, err)
		}
		fmt.Fprintln(stdout, written)
	}
	return nil
}

// importPackage registers one package, creating its publisher and plugin
// files when missing.
func importPackage(o importOptions, pkg string) (string, error) {
	info, err := inspectPackage(pkg)
	if err != nil {
		return "", err
	}
	m := info.m
	pub := regPublisher{ID: m.Publisher, Name: bilingual(nil, m.Publisher), Trust: o.trust, Status: "active", GitHub: []string{importLogin}}
	if err := writeIfMissing(filepath.Join(o.root, regPublishersDir, m.Publisher+regJSONExt), m.Publisher, &pub, checkPublisher); err != nil {
		return "", err
	}
	ref := regRef(m.Publisher, m.Key)
	pl := regPlugin{Key: m.Key, Publisher: m.Publisher, Name: bilingual(m.Name, m.Key), Description: bilingual(m.Description, m.Key), Repo: o.repo}
	if err := writeIfMissing(filepath.Join(o.root, filepath.FromSlash(pluginDir(ref)), regPluginFile), ref, &pl, checkPlugin); err != nil {
		return "", err
	}
	v := info.version(o.urlPrefix + url.PathEscape(filepath.Base(pkg)))
	v.ReleaseURL = githubURL(o.repo, "releases") // no release behind a local package
	return writeVersion(o.root, ref, v)
}

// bilingual fills every registry language from t, falling back to the other
// language and then to fallback.
func bilingual(t manifest.LocalizedText, fallback string) manifest.LocalizedText {
	out := manifest.LocalizedText{}
	for _, lang := range regLanguages {
		for _, v := range []string{t[lang], t["en"], t["zh"], fallback} {
			if strings.TrimSpace(v) != "" {
				out[lang] = v
				break
			}
		}
	}
	return out
}

// writeVersion creates plugins/<ref>/versions/<version>.json; an existing
// file is an error.
func writeVersion(root, ref string, v regVersion) (string, error) {
	p := filepath.Join(root, filepath.FromSlash(pluginDir(ref)), regVersionsDir, v.Version+regJSONExt)
	return p, writeRegistryFile(p, v.Version, &v, checkVersion)
}

func writeIfMissing[T any](p, name string, v *T, check func(string, *T) []string) error {
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	return writeRegistryFile(p, name, v, check)
}

// writeRegistryFile validates v and creates p with it; an existing file is
// an error.
func writeRegistryFile[T any](p, name string, v *T, check func(string, *T) []string) error {
	if problems := check(name, v); len(problems) > 0 {
		return fmt.Errorf("%s: %s", filepath.ToSlash(p), strings.Join(problems, "; "))
	}
	raw, err := encodeJSON(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s already exists", filepath.ToSlash(p))
	}
	if err != nil {
		return err
	}
	_, err = f.Write(raw)
	return errors.Join(err, f.Close())
}
