package market

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/install"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// PublisherMissing is the publisher_status of a plugin whose publisher the
// index does not list.
const PublisherMissing = "missing"

// MarketPlugin is one listing row.
type MarketPlugin struct {
	SourceID   int64  `json:"source_id"`
	SourceName string `json:"source_name"`
	// ID is the plugin id (manifest.PluginID) of Publisher's Key, the key
	// of its row once installed; DisplayID is how people see it.
	ID                string             `json:"id"`
	DisplayID         string             `json:"display_id"`
	Key               string             `json:"key"`
	Name              core.LocalizedText `json:"name"`
	Description       core.LocalizedText `json:"description"`
	Publisher         string             `json:"publisher"`
	PublisherName     core.LocalizedText `json:"publisher_name,omitempty"`
	PublisherHomepage string             `json:"publisher_homepage,omitempty"`
	// PublisherGitHub are the GitHub logins the index lists for the
	// publisher (who may register its plugins).
	PublisherGitHub []string `json:"publisher_github,omitempty"`
	// Trust is the publisher's trust level in the index, "" when the index
	// does not list it.
	Trust string `json:"trust"`
	// PublisherStatus is active, revoked (in the index or on this core) or
	// missing (not in the index). Only an active publisher's plugins install.
	PublisherStatus  string          `json:"publisher_status"`
	Repo             string          `json:"repo,omitempty"`
	SourceURL        string          `json:"source_url,omitempty"`
	Builtin          bool            `json:"builtin"`
	Deprecated       *Deprecation    `json:"deprecated,omitempty"`
	LatestVersion    string          `json:"latest_version"`
	Versions         []MarketVersion `json:"versions"`
	InstalledVersion string          `json:"installed_version,omitempty"`
	InstalledStatus  string          `json:"installed_status,omitempty"`
	UpdateAvailable  bool            `json:"update_available"`
}

type MarketVersion struct {
	Version    string `json:"version"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	HostCompat string `json:"host_compat"`
	// Compatible reports whether this host could install the version:
	// host_compat is matched against the core version with any
	// pre-release suffix ignored, exactly as install-time validation does
	// (an empty or malformed range is incompatible, since install rejects
	// it).
	Compatible bool         `json:"compatible"`
	ReleaseURL string       `json:"release_url"`
	Deprecated *Deprecation `json:"deprecated,omitempty"`
}

// Compatible reports whether a market version's host_compat range accepts
// hostVersion, using the same rule as manifest validation at install time.
func Compatible(hostCompat, hostVersion string) bool {
	if strings.TrimSpace(hostCompat) == "" {
		return false
	}
	ok, err := pkg.HostCompatible(hostCompat, hostVersion)
	return err == nil && ok
}

// Listing is the listing of a channel: its plugins, when CI generated its
// index and when this core fetched it.
type Listing struct {
	Plugins     []MarketPlugin
	GeneratedAt time.Time
	FetchedAt   time.Time
}

// ListPlugins lists the plugins of channel sourceID (0: the default one),
// annotated with their publisher, install state and update availability.
func (s *Service) ListPlugins(ctx context.Context, sourceID int64, refresh bool) (*Listing, error) {
	idx, src, err := s.Index(ctx, sourceID, refresh)
	if err != nil {
		return nil, err
	}
	st, err := s.listState(ctx)
	if err != nil {
		return nil, err
	}
	out := &Listing{Plugins: make([]MarketPlugin, 0, len(idx.Plugins)), GeneratedAt: idx.GeneratedAt, FetchedAt: idx.fetched}
	for _, p := range idx.Plugins {
		out.Plugins = append(out.Plugins, st.plugin(src, idx, p))
	}
	return out, nil
}

// listState is what a listing annotates the indexes with.
type listState struct {
	hostVersion string
	installed   map[string][2]string // plugin id -> [current version, status]
	revoked     map[string]bool      // publishers revoked on this core
}

func (s *Service) listState(ctx context.Context) (*listState, error) {
	st := &listState{hostVersion: s.inst.HostVersion()}
	var err error
	if st.installed, err = s.installed(ctx); err != nil {
		return nil, err
	}
	var names []string
	if err := revokedPublishers(store.G(ctx, s.db.Gorm)).Pluck("name", &names).Error; err != nil {
		return nil, err
	}
	st.revoked = map[string]bool{}
	for _, n := range names {
		st.revoked[n] = true
	}
	return st, nil
}

// plugin is the listing row of p, listed by idx of src.
func (st *listState) plugin(src *Source, idx *Index, p IndexPlugin) MarketPlugin {
	id := p.ID()
	mp := MarketPlugin{SourceID: src.ID, SourceName: src.Name, ID: id, DisplayID: manifest.DisplayID(id), Key: p.Key,
		Name: p.Name, Description: p.Description, Repo: p.Repo, SourceURL: p.Source, Builtin: p.Builtin, Deprecated: p.Deprecated,
		Versions: []MarketVersion{}}
	st.setPublisher(&mp, idx, p.Publisher)
	latest := st.setVersions(&mp, p.Versions)
	st.setInstalled(&mp, latest)
	return mp
}

// setPublisher fills in the publisher id of mp as idx lists it.
func (st *listState) setPublisher(mp *MarketPlugin, idx *Index, id string) {
	pub, ok := idx.publisher(id)
	mp.Publisher, mp.PublisherName, mp.PublisherHomepage, mp.PublisherGitHub = id, pub.Name, pub.Homepage, pub.GitHub
	mp.Trust, mp.PublisherStatus = pub.Trust, pub.Status
	switch {
	case !ok:
		mp.PublisherStatus = PublisherMissing
	case st.revoked[id]:
		mp.PublisherStatus = pkg.PublisherRevoked
	}
}

// setVersions lists vs in mp, newest first, and returns its latest
// version: the newest compatible one that is not deprecated.
func (st *listState) setVersions(mp *MarketPlugin, vs []IndexVersion) *semver.Version {
	var latest *semver.Version
	for _, v := range sortedVersions(vs) {
		ok := Compatible(v.HostCompat, st.hostVersion)
		mp.Versions = append(mp.Versions, MarketVersion{Version: v.Version, SHA256: v.SHA256, Size: v.Size, HostCompat: v.HostCompat, Compatible: ok,
			ReleaseURL: v.ReleaseURL, Deprecated: v.Deprecated})
		sv, err := semver.NewVersion(v.Version)
		if err == nil && ok && v.Deprecated == nil && (latest == nil || sv.GreaterThan(latest)) {
			latest, mp.LatestVersion = sv, v.Version
		}
	}
	return latest
}

// setInstalled fills in the installed version of mp and whether latest
// is newer.
func (st *listState) setInstalled(mp *MarketPlugin, latest *semver.Version) {
	inst, ok := st.installed[mp.ID]
	if !ok {
		return
	}
	mp.InstalledVersion, mp.InstalledStatus = inst[0], inst[1]
	cur, err := semver.NewVersion(inst[0])
	mp.UpdateAvailable = err == nil && latest != nil && latest.GreaterThan(cur)
}

func sortedVersions(vs []IndexVersion) []IndexVersion {
	out := append([]IndexVersion(nil), vs...)
	sort.SliceStable(out, func(i, j int) bool {
		a, e1 := semver.NewVersion(out[i].Version)
		b, e2 := semver.NewVersion(out[j].Version)
		if e1 != nil || e2 != nil {
			return out[i].Version > out[j].Version
		}
		return a.GreaterThan(b)
	})
	return out
}

// installed maps plugin id -> [current version, status].
func (s *Service) installed(ctx context.Context) (map[string][2]string, error) {
	var plugins []model.Plugin
	if err := store.G(ctx, s.db.Gorm).Select("key", "status").Find(&plugins).Error; err != nil {
		return nil, err
	}
	out := map[string][2]string{}
	for _, p := range plugins {
		v, err := install.CurrentVersion(ctx, s.db.Gorm, p.Key)
		if err != nil {
			return nil, err
		}
		out[p.Key] = [2]string{v, p.Status}
	}
	return out, nil
}

// revokedPublishers selects the publishers revoked on this core.
func revokedPublishers(db *gorm.DB) *gorm.DB {
	return db.Model(&model.Publisher{}).Where("status <> ?", pkg.PublisherActive)
}
