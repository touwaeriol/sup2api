package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// The plugin store's index registry (CONTRACTS §135): one JSON file per
// publisher, per plugin (plugins/<publisher>/<key>/) and per plugin version, from which `index` builds
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

// regPlugin is plugins/<publisher>/<key>/plugin.json.
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

// regVersion is plugins/<publisher>/<key>/versions/<version>.json.
type regVersion struct {
	Version      string       `json:"version"`
	URL          string       `json:"url"`
	SHA256       string       `json:"sha256"`
	Size         int64        `json:"size"`
	HostCompat   string       `json:"host_compat"`
	ReleaseURL   string       `json:"release_url"`             // the GitHub release page, the version's release notes
	SourceDigest string       `json:"source_digest,omitempty"` // the package's source.json; none in packages packed before it
	Deprecated   *deprecation `json:"deprecated,omitempty"`
}

// regEntry is one plugin directory.
type regEntry struct {
	plugin   regPlugin
	versions map[string]*regVersion
}

// ref is the entry's plugin ref.
func (e *regEntry) ref() string { return regRef(e.plugin.Publisher, e.plugin.Key) }

// registry is a loaded and validated registry checkout.
type registry struct {
	publishers map[string]*regPublisher
	plugins    map[string]*regEntry // by ref
}

// A plugin ref names a plugin in the registry as "<publisher>/<key>", its
// directory below plugins/: two publishers may each have a plugin "codex".
const refSep = "/"

func regRef(publisher, key string) string { return publisher + refSep + key }

// parseRegRef reads a --plugin value: "<publisher>/<key>", or a bare key of
// the official publisher.
func parseRegRef(s string) string {
	if strings.Contains(s, refSep) {
		return s
	}
	return regRef(manifest.OfficialPublisher, s)
}

// pluginDir is the directory of a plugin ref, relative to the checkout.
func pluginDir(ref string) string { return path.Join(regPluginsDir, ref) }

// sortedPlugins lists the plugins by publisher and key, the official
// publisher's first: a core that matches index entries by key alone then
// finds the official plugin first.
func (reg *registry) sortedPlugins() []*regEntry {
	rank := func(e *regEntry) int {
		if e.plugin.Publisher == manifest.OfficialPublisher {
			return 0
		}
		return 1
	}
	out := slices.Collect(maps.Values(reg.plugins))
	slices.SortFunc(out, func(a, b *regEntry) int {
		return cmp.Or(cmp.Compare(rank(a), rank(b)), strings.Compare(a.plugin.Publisher, b.plugin.Publisher), strings.Compare(a.plugin.Key, b.plugin.Key))
	})
	return out
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
		rel := path.Join(regPublishersDir, name)
		if addErr(&errs, readRegistryFile(root, rel, strings.TrimSuffix(name, regJSONExt), p, checkPublisher)) {
			reg.publishers[p.ID] = p
		}
	}
	reg.loadPlugins(root, dev, &errs)
	for ref, e := range reg.plugins {
		if pub := e.plugin.Publisher; reg.publishers[pub] == nil {
			errs = append(errs, fmt.Errorf("%s: publisher %q has no %s", path.Join(pluginDir(ref), regPluginFile), pub, path.Join(regPublishersDir, pub+regJSONExt)))
		}
	}
	return reg, errors.Join(errs...)
}

// loadPlugins reads plugins/<publisher>/<key>/ for every publisher and key
// directory.
func (reg *registry) loadPlugins(root string, dev bool, errs *[]error) {
	pubs, err := readDirIfExists(root, regPluginsDir)
	addErr(errs, err)
	for _, pd := range pubs {
		if !pd.IsDir() {
			*errs = append(*errs, fmt.Errorf("%s: not a directory", path.Join(regPluginsDir, pd.Name())))
			continue
		}
		keys, err := readDirIfExists(root, path.Join(regPluginsDir, pd.Name()))
		addErr(errs, err)
		for _, kd := range keys {
			e, perrs := loadPluginDir(root, regRef(pd.Name(), kd.Name()), kd, dev)
			*errs = append(*errs, perrs...)
			if e != nil {
				reg.plugins[e.ref()] = e
			}
		}
	}
}

// loadPluginDir reads plugins/<ref>/: plugin.json, an optional icon.svg and
// versions/*.json, and nothing else.
func loadPluginDir(root, ref string, d fs.DirEntry, dev bool) (*regEntry, []error) {
	rel := pluginDir(ref)
	if !d.IsDir() {
		return nil, []error{fmt.Errorf("%s: not a directory", rel)}
	}
	var errs []error
	addErr(&errs, onlyEntries(root, rel, []string{regPluginFile, regIconFile}, []string{regVersionsDir}))
	e := &regEntry{versions: map[string]*regVersion{}}
	if !addErr(&errs, readRegistryFile(root, path.Join(rel, regPluginFile), ref, &e.plugin, checkPlugin)) {
		return nil, errs
	}
	vdir := path.Join(rel, regVersionsDir)
	for _, name := range jsonFiles(root, vdir, &errs) {
		v := &regVersion{}
		file := path.Join(vdir, name)
		if !addErr(&errs, readRegistryFile(root, file, strings.TrimSuffix(name, regJSONExt), v, checkVersion)) {
			continue
		}
		if dev || addErr(&errs, prefixErr(file, releaseConsistent(&e.plugin, v))) {
			e.versions[v.Version] = v
		}
	}
	return e, errs
}

// readRegistryFile decodes rel strictly into v and validates it against
// name: the file's stem, or the plugin ref for a plugin.json.
func readRegistryFile[T any](root, rel, name string, v *T, check func(name string, v *T) []string) error {
	raw, err := readRegular(root, rel)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%s: %w", rel, err)
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
