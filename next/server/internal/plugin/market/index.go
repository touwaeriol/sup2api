package market

import (
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
)

// indexFormat is the index.json version this core reads (CONTRACTS §11.1).
const indexFormat = 2

// Index is index.json v2.
type Index struct {
	Version     int              `json:"version"`
	GeneratedAt time.Time        `json:"generated_at"`
	Publishers  []IndexPublisher `json:"publishers"`
	Plugins     []IndexPlugin    `json:"plugins"`

	fetched time.Time // when this core fetched it
}

// IndexPublisher is a publisher of the index: the market vouches for its
// plugins with its trust level while it is active.
type IndexPublisher struct {
	ID       string             `json:"id"`
	Name     core.LocalizedText `json:"name"`
	Homepage string             `json:"homepage,omitempty"`
	GitHub   []string           `json:"github,omitempty"` // logins that may register its plugins
	Trust    string             `json:"trust"`            // official | verified | community
	Status   string             `json:"status"`           // active | revoked
}

// Deprecation marks a plugin or a version as deprecated: still listed and
// installable, never the latest version.
type Deprecation struct {
	Reason core.LocalizedText `json:"reason"`
}

// IndexPlugin is a plugin of the index: publisher and key identify it.
type IndexPlugin struct {
	Key         string             `json:"key"`
	Publisher   string             `json:"publisher"`
	Name        core.LocalizedText `json:"name"`
	Description core.LocalizedText `json:"description"`
	Repo        string             `json:"repo"`
	Source      string             `json:"source,omitempty"`
	Builtin     bool               `json:"builtin,omitempty"`
	Deprecated  *Deprecation       `json:"deprecated,omitempty"`
	Versions    []IndexVersion     `json:"versions"`
}

// ID is the plugin id of p (manifest.PluginID).
func (p *IndexPlugin) ID() string { return manifest.PluginID(p.Publisher, p.Key) }

type IndexVersion struct {
	Version    string `json:"version"`
	URL        string `json:"url"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	HostCompat string `json:"host_compat"`
	// ReleaseURL is the GitHub release page of the version (its notes).
	ReleaseURL string       `json:"release_url"`
	Deprecated *Deprecation `json:"deprecated,omitempty"`
}

// parseIndex parses index.json v2, checks each publisher (an invalid one
// invalidates the index) and drops the links that are no allowed URL
// (dropLinks).
func parseIndex(raw []byte, allowHTTP bool) (*Index, error) {
	var idx Index
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil, fmt.Errorf("index.json: %w", err)
	}
	if idx.Version != indexFormat {
		return nil, fmt.Errorf("unsupported index version %d (want %d)", idx.Version, indexFormat)
	}
	for _, p := range idx.Publishers {
		if err := checkPublisher(p); err != nil {
			return nil, err
		}
	}
	if err := idx.checkPlugins(); err != nil {
		return nil, err
	}
	idx.dropLinks(allowHTTP)
	return &idx, nil
}

// checkPlugins fails when two plugins of the index have one plugin id
// (manifest.PluginID): a publisher lists a key once (CONTRACTS §11.1).
func (idx *Index) checkPlugins() error {
	seen := make(map[string]bool, len(idx.Plugins))
	for _, p := range idx.Plugins {
		id := p.ID()
		if seen[id] {
			return fmt.Errorf("plugin %q of publisher %q is listed twice", p.Key, p.Publisher)
		}
		seen[id] = true
	}
	return nil
}

// checkPublisher fails for an id, trust level or status the index format
// does not have.
func checkPublisher(p IndexPublisher) error {
	if err := check.ValidatePublisherID(p.ID); err != nil {
		return err
	}
	switch p.Trust {
	case pkg.TrustOfficial, pkg.TrustVerified, pkg.TrustCommunity:
	default:
		return fmt.Errorf("publisher %q: invalid trust %q", p.ID, p.Trust)
	}
	if p.Status != pkg.PublisherActive && p.Status != pkg.PublisherRevoked {
		return fmt.Errorf("publisher %q: invalid status %q", p.ID, p.Status)
	}
	return nil
}

// dropLinks empties the links the console opens as they are (a publisher's
// homepage, a plugin's source, a version's release_url) unless they are an
// absolute https URL (http too with allowHTTP), so that no other scheme
// reaches a page.
func (idx *Index) dropLinks(allowHTTP bool) {
	keep := func(link *string) {
		if u, err := url.Parse(*link); err != nil || u.Host == "" || !schemeAllowed(u, allowHTTP) {
			*link = ""
		}
	}
	for i := range idx.Publishers {
		keep(&idx.Publishers[i].Homepage)
	}
	for i := range idx.Plugins {
		p := &idx.Plugins[i]
		keep(&p.Source)
		for j := range p.Versions {
			keep(&p.Versions[j].ReleaseURL)
		}
	}
}

// publisher is the publisher id of the index; ok is false when it is not
// listed.
func (idx *Index) publisher(id string) (p IndexPublisher, ok bool) {
	for _, p := range idx.Publishers {
		if p.ID == id {
			return p, true
		}
	}
	return IndexPublisher{}, false
}

// indexed is what the index says of publisher id, for the installer
// (status "" when it is not listed).
func (idx *Index) indexed(id string) pkg.IndexedPublisher {
	p, _ := idx.publisher(id)
	return pkg.IndexedPublisher{Name: id, Trust: p.Trust, Status: p.Status}
}

// find finds publisher's key@version in idx: the plugin and the version,
// ok false when it is not listed.
func (idx *Index) find(publisher, key, version string) (p *IndexPlugin, v *IndexVersion, ok bool) {
	for i := range idx.Plugins {
		if p = &idx.Plugins[i]; p.Publisher != publisher || p.Key != key {
			continue
		}
		for j := range p.Versions {
			if p.Versions[j].Version == version {
				return p, &p.Versions[j], true
			}
		}
	}
	return nil, nil, false
}
