package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
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
// under root; all problems are reported together, with what did load. Unless
// dev, a version file must also name a GitHub release of its plugin's repo
// (releaseConsistent); dev allows the relative urls of a local market.
func loadRegistry(root string, dev bool) (*registry, error) {
	reg := &registry{publishers: map[string]*regPublisher{}, plugins: map[string]*regEntry{}}
	var errs []error
	for _, name := range jsonFiles(root, regPublishersDir, &errs) {
		p := &regPublisher{}
		if addErr(&errs, readRegistryFile(root, path.Join(regPublishersDir, name), p, checkPublisher)) {
			reg.publishers[p.ID] = p
		}
	}
	dirs, err := readDirIfExists(root, regPluginsDir)
	addErr(&errs, err)
	for _, d := range dirs {
		e, perrs := loadPluginDir(root, d, dev)
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
func loadPluginDir(root string, d fs.DirEntry, dev bool) (*regEntry, []error) {
	rel := path.Join(regPluginsDir, d.Name())
	if !d.IsDir() {
		return nil, []error{fmt.Errorf("%s: not a directory", rel)}
	}
	var errs []error
	addErr(&errs, onlyEntries(root, rel, []string{regPluginFile, regIconFile}, []string{regVersionsDir}))
	e := &regEntry{versions: map[string]*regVersion{}}
	if !addErr(&errs, readRegistryFile(root, path.Join(rel, regPluginFile), &e.plugin, checkPlugin)) {
		return nil, errs
	}
	vdir := path.Join(rel, regVersionsDir)
	for _, name := range jsonFiles(root, vdir, &errs) {
		v := &regVersion{}
		file := path.Join(vdir, name)
		if !addErr(&errs, readRegistryFile(root, file, v, checkVersion)) {
			continue
		}
		if dev || addErr(&errs, prefixErr(file, releaseConsistent(&e.plugin, v))) {
			e.versions[v.Version] = v
		}
	}
	return e, errs
}

// readRegistryFile decodes rel strictly into v and validates it against its
// file name (the stem of rel, or its directory for plugin.json).
func readRegistryFile[T any](root, rel string, v *T, check func(name string, v *T) []string) error {
	raw, err := readRegular(root, rel)
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

// prefixErr prefixes a non-nil err with the file it is about.
func prefixErr(file string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", file, err)
}

// addErr appends a non-nil err and reports whether err was nil.
func addErr(errs *[]error, err error) bool {
	if err != nil {
		*errs = append(*errs, err)
	}
	return err == nil
}
