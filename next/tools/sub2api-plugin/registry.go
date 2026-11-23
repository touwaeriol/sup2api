package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// The plugin store's index registry (CONTRACTS §135): one JSON file per
// publisher, per plugin and per plugin version, from which `index` builds
// index.json and `registry check` reviews pull requests.
const (
	regPublishersDir = "publishers"
	regPluginsDir    = "plugins"
	regPluginFile    = "plugin.json"
	regIconFile      = "icon.svg"
	regVersionsDir   = "versions"
	regJSONExt       = ".json"
)

// deprecation marks a plugin or a version as deprecated.
type deprecation struct {
	Reason manifest.LocalizedText `json:"reason"`
}

// indexPublisher is a publisher as index.json lists it.
type indexPublisher struct {
	ID       string                 `json:"id"`
	Name     manifest.LocalizedText `json:"name"`
	Homepage string                 `json:"homepage,omitempty"`
	Trust    string                 `json:"trust"`
	Status   string                 `json:"status"`
}

// regPublisher is publishers/<id>.json: the index fields plus the GitHub
// logins allowed to publish for it (CI only, not in the index).
type regPublisher struct {
	indexPublisher
	GitHub []string `json:"github"`
}

// regPlugin is plugins/<key>/plugin.json.
type regPlugin struct {
	Key         string                 `json:"key"`
	Publisher   string                 `json:"publisher"`
	Name        manifest.LocalizedText `json:"name"`
	Description manifest.LocalizedText `json:"description"`
	Repo        string                 `json:"repo"`
	Source      string                 `json:"source,omitempty"`
	Builtin     bool                   `json:"builtin,omitempty"`
	Deprecated  *deprecation           `json:"deprecated,omitempty"`
}

// regVersion is plugins/<key>/versions/<version>.json.
type regVersion struct {
	Version    string       `json:"version"`
	URL        string       `json:"url"`
	SHA256     string       `json:"sha256"`
	Size       int64        `json:"size"`
	HostCompat string       `json:"host_compat"`
	ReleaseURL string       `json:"release_url"` // the GitHub release page, the version's release notes
	Deprecated *deprecation `json:"deprecated,omitempty"`
}

// regEntry is one plugin directory.
type regEntry struct {
	plugin   regPlugin
	versions map[string]*regVersion
}

// registry is a loaded and validated registry checkout.
type registry struct {
	publishers map[string]*regPublisher
	plugins    map[string]*regEntry
}

// loadRegistry reads and validates every publisher, plugin and version file
// under root. All problems are reported together.
func loadRegistry(root string) (*registry, error) {
	reg := &registry{publishers: map[string]*regPublisher{}, plugins: map[string]*regEntry{}}
	var errs []error
	for _, name := range jsonFiles(filepath.Join(root, regPublishersDir), &errs) {
		p := &regPublisher{}
		if addErr(&errs, readRegistryFile(root, path.Join(regPublishersDir, name), p, checkPublisher)) {
			reg.publishers[p.ID] = p
		}
	}
	dirs, err := readDirIfExists(filepath.Join(root, regPluginsDir))
	addErr(&errs, err)
	for _, d := range dirs {
		e, perrs := loadPluginDir(root, d)
		errs = append(errs, perrs...)
		if e != nil {
			reg.plugins[e.plugin.Key] = e
		}
	}
	for _, e := range reg.plugins {
		if _, ok := reg.publishers[e.plugin.Publisher]; !ok {
			errs = append(errs, fmt.Errorf("plugins/%s/plugin.json: publisher %q has no publishers/%[2]s.json", e.plugin.Key, e.plugin.Publisher))
		}
	}
	return reg, errors.Join(errs...)
}

// loadPluginDir reads plugins/<key>/: plugin.json, an optional icon.svg and
// versions/*.json, and nothing else.
func loadPluginDir(root string, d fs.DirEntry) (*regEntry, []error) {
	rel := path.Join(regPluginsDir, d.Name())
	if !d.IsDir() {
		return nil, []error{fmt.Errorf("%s: not a plugin directory", rel)}
	}
	var errs []error
	addErr(&errs, onlyEntries(filepath.Join(root, rel), regPluginFile, regIconFile, regVersionsDir))
	e := &regEntry{versions: map[string]*regVersion{}}
	if !addErr(&errs, readRegistryFile(root, path.Join(rel, regPluginFile), &e.plugin, checkPlugin)) {
		return nil, errs
	}
	vdir := path.Join(rel, regVersionsDir)
	for _, name := range jsonFiles(filepath.Join(root, vdir), &errs) {
		v := &regVersion{}
		if addErr(&errs, readRegistryFile(root, path.Join(vdir, name), v, checkVersion)) {
			e.versions[v.Version] = v
		}
	}
	return e, errs
}

// readRegistryFile decodes rel strictly into v and validates it against its
// file name (the stem of rel, or its directory for plugin.json).
func readRegistryFile[T any](root, rel string, v *T, check func(name string, v *T) []string) error {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	name := strings.TrimSuffix(path.Base(rel), regJSONExt)
	if name == strings.TrimSuffix(regPluginFile, regJSONExt) {
		name = path.Base(path.Dir(rel))
	}
	if problems := check(name, v); len(problems) > 0 {
		return fmt.Errorf("%s: %s", rel, strings.Join(problems, "; "))
	}
	return nil
}

// jsonFiles lists the *.json files of dir (none when dir is missing); any
// other entry is a problem.
func jsonFiles(dir string, errs *[]error) []string {
	entries, err := readDirIfExists(dir)
	addErr(errs, err)
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), regJSONExt) {
			*errs = append(*errs, fmt.Errorf("%s: unexpected entry %q", filepath.ToSlash(dir), e.Name()))
			continue
		}
		names = append(names, e.Name())
	}
	return names
}

// onlyEntries fails when dir holds an entry not named in allowed.
func onlyEntries(dir string, allowed ...string) error {
	entries, err := readDirIfExists(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !slices.Contains(allowed, e.Name()) {
			return fmt.Errorf("%s: unexpected entry %q", filepath.ToSlash(dir), e.Name())
		}
	}
	return nil
}

func readDirIfExists(dir string) ([]fs.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return entries, err
}

// addErr appends a non-nil err and reports whether err was nil.
func addErr(errs *[]error, err error) bool {
	if err != nil {
		*errs = append(*errs, err)
	}
	return err == nil
}

// fileChange is one file that differs between two checkouts.
type fileChange struct {
	path string // slash-separated, relative to the checkout
	kind string // changeAdded, changeModified or changeDeleted
}

const (
	changeAdded    = "added"
	changeModified = "modified"
	changeDeleted  = "deleted"
)

// diffTrees lists the files that differ between two checkouts, by path.
func diffTrees(baseRoot, headRoot string) ([]fileChange, error) {
	base, err := treeFiles(baseRoot)
	if err != nil {
		return nil, err
	}
	head, err := treeFiles(headRoot)
	if err != nil {
		return nil, err
	}
	var out []fileChange
	for _, p := range slices.Sorted(maps.Keys(mergeKeys(base, head))) {
		old, inBase := base[p]
		cur, inHead := head[p]
		switch {
		case !inBase:
			out = append(out, fileChange{p, changeAdded})
		case !inHead:
			out = append(out, fileChange{p, changeDeleted})
		case old != cur:
			out = append(out, fileChange{p, changeModified})
		}
	}
	return out, nil
}

func mergeKeys(a, b map[string]string) map[string]string {
	out := maps.Clone(a)
	maps.Copy(out, b)
	return out
}

// treeFiles maps every file below root (except .git) to its content; a
// symbolic link maps to its target, so changing either shows.
func treeFiles(root string) (map[string]string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		content, err := fileContent(p, d)
		files[filepath.ToSlash(rel)] = content
		return err
	})
	return files, err
}

func fileContent(p string, d fs.DirEntry) (string, error) {
	if d.Type()&fs.ModeSymlink != 0 {
		target, err := os.Readlink(p)
		return "symlink:" + target, err
	}
	b, err := os.ReadFile(p)
	return string(b), err
}
