// Package market is the plugin market client: signed static indexes
// (CONTRACTS §11.1), listing and one-click install.
package market

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Masterminds/semver/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	rc "github.com/Sub2API-Devs/sup2api/next/runtime-contract"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pkgsig"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/install"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// IndexSignaturePrefix is prepended to sha256hex(index.json) before signing.
const IndexSignaturePrefix = "sub2api-market-index-v1:"

const (
	cacheTTL      = 5 * time.Minute
	maxIndexBytes = 8 << 20
	maxSigBytes   = 4 << 10
)

// Source is a market_sources row.
type Source struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	PublicKey string    `json:"public_key"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

// Index is index.json.
type Index struct {
	Version     int           `json:"version"`
	GeneratedAt time.Time     `json:"generated_at"`
	Plugins     []IndexPlugin `json:"plugins"`
}

type IndexPlugin struct {
	Key         string             `json:"key"`
	Name        core.LocalizedText `json:"name"`
	Description core.LocalizedText `json:"description"`
	Publisher   string             `json:"publisher"`
	Versions    []IndexVersion     `json:"versions"`
}

type IndexVersion struct {
	Version    string `json:"version"`
	URL        string `json:"url"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	HostCompat string `json:"host_compat"`
}

// Installer is the part of install.Service the market needs: the host
// version the index entries are checked against. Downloaded packages go to
// the installer through the console API (Download).
type Installer interface {
	HostVersion() string
}

// Service fetches and caches market indexes.
type Service struct {
	db       *store.DB
	inst     Installer
	client   *http.Client
	pkg      *http.Client // client with at least rc.PluginPackageTransferTimeout
	maxBytes int64
	now      func() time.Time

	mu    sync.Mutex
	cache map[int64]cached
}

type cached struct {
	at       time.Time
	indexURL string
	pubKey   string
	idx      *Index
}

// New builds the market client. client nil uses a 60 s timeout client;
// package downloads use a copy of it with at least rc.PluginPackageTransferTimeout.
// maxPackageBytes bounds downloads (config MaxPackageBytes).
func New(db *store.DB, inst Installer, client *http.Client, maxPackageBytes int64) *Service {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	if maxPackageBytes <= 0 {
		maxPackageBytes = rc.PluginPackageMaxBytes
	}
	pkgClient := *client
	if pkgClient.Timeout != 0 && pkgClient.Timeout < rc.PluginPackageTransferTimeout {
		pkgClient.Timeout = rc.PluginPackageTransferTimeout
	}
	return &Service{db: db, inst: inst, client: client, pkg: &pkgClient, maxBytes: maxPackageBytes, now: time.Now, cache: map[int64]cached{}}
}

// SeedSources upserts sources from SUB2API_MARKET_SOURCES
// ([{name,url,public_key}]); existing sources with the same name are updated.
func (s *Service) SeedSources(ctx context.Context, sourcesJSON string) error {
	if strings.TrimSpace(sourcesJSON) == "" {
		return nil
	}
	var in []struct {
		Name      string `json:"name"`
		URL       string `json:"url"`
		PublicKey string `json:"public_key"`
	}
	if err := json.Unmarshal([]byte(sourcesJSON), &in); err != nil {
		return fmt.Errorf("market sources: %w", err)
	}
	for _, src := range in {
		if src.Name == "" {
			return fmt.Errorf("market sources: name is required")
		}
		if _, err := indexURL(src.URL); err != nil {
			return fmt.Errorf("market source %q: %w", src.Name, err)
		}
		if _, err := pkgsig.ParsePublicKey(src.PublicKey); err != nil {
			return fmt.Errorf("market source %q: %w", src.Name, err)
		}
		if err := seedSource(store.G(ctx, s.db.Gorm), src.Name, src.URL, strings.TrimSpace(src.PublicKey)).Error; err != nil {
			return err
		}
	}
	return nil
}

// seedSource upserts the source name: an existing one takes addr and
// publicKey.
func seedSource(db *gorm.DB, name, addr, publicKey string) *gorm.DB {
	return db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "name"}}, DoUpdates: clause.AssignmentColumns([]string{"url", "public_key"})}).
		Create(&model.MarketSource{Name: name, URL: addr, PublicKey: publicKey})
}

// sources selects market_sources rows as Source, whose fields are its
// columns.
func sources(db *gorm.DB) *gorm.DB {
	return db.Table("market_sources").Select("id, name, url, public_key, enabled, created_at")
}

// ListSources returns all sources.
func (s *Service) ListSources(ctx context.Context) ([]Source, error) {
	out := []Source{}
	if err := sources(store.G(ctx, s.db.Gorm)).Order("id").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) source(ctx context.Context, id int64) (*Source, error) {
	var src Source
	err := sources(store.G(ctx, s.db.Gorm)).Where("id = ?", id).Take(&src).Error
	if store.IsNoRows(err) {
		return nil, core.ErrNotFound.WithMessage("market source not found")
	}
	if err != nil {
		return nil, err
	}
	if !src.Enabled {
		return nil, core.ErrNotFound.WithMessage("market source is disabled")
	}
	return &src, nil
}

func indexURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("url must be an absolute http(s) URL")
	}
	if strings.HasSuffix(u.Path, "/") || u.Path == "" {
		u.Path = strings.TrimSuffix(u.Path, "/") + "/index.json"
	}
	return u, nil
}

// Index returns the verified index of a source (cached for 5 minutes).
func (s *Service) Index(ctx context.Context, sourceID int64, refresh bool) (*Index, *Source, error) {
	src, err := s.source(ctx, sourceID)
	if err != nil {
		return nil, nil, err
	}
	u, err := indexURL(src.URL)
	if err != nil {
		return nil, nil, core.ErrInvalidArgument.WithMessage(err.Error())
	}
	s.mu.Lock()
	c, ok := s.cache[src.ID]
	s.mu.Unlock()
	if ok && !refresh && c.indexURL == u.String() && c.pubKey == src.PublicKey && s.now().Sub(c.at) < cacheTTL {
		return c.idx, src, nil
	}
	raw, err := s.fetch(ctx, u.String(), maxIndexBytes)
	if err != nil {
		return nil, nil, err
	}
	sig, err := s.fetch(ctx, u.String()+".sig", maxSigBytes)
	if err != nil {
		return nil, nil, err
	}
	idx, err := VerifyIndex(raw, sig, src.PublicKey)
	if err != nil {
		return nil, nil, core.ErrUnavailable.WithMessage("market index verification failed: " + err.Error()).WithCause(err)
	}
	s.mu.Lock()
	s.cache[src.ID] = cached{at: s.now(), indexURL: u.String(), pubKey: src.PublicKey, idx: idx}
	s.mu.Unlock()
	return idx, src, nil
}

// VerifyIndex checks index.json.sig and parses index.json.
func VerifyIndex(raw, sig []byte, publicKeyB64 string) (*Index, error) {
	pub, err := pkgsig.ParsePublicKey(publicKeyB64)
	if err != nil {
		return nil, err
	}
	sigRaw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil {
		return nil, fmt.Errorf("index signature is not base64")
	}
	sum := sha256.Sum256(raw)
	if !ed25519.Verify(pub, []byte(IndexSignaturePrefix+hex.EncodeToString(sum[:])), sigRaw) {
		return nil, fmt.Errorf("index signature does not match")
	}
	var idx Index
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil, fmt.Errorf("index.json: %w", err)
	}
	if idx.Version != 1 {
		return nil, fmt.Errorf("unsupported index version %d", idx.Version)
	}
	return &idx, nil
}

// SignIndex produces index.json.sig content (tests and tooling).
func SignIndex(raw []byte, priv ed25519.PrivateKey) []byte {
	sum := sha256.Sum256(raw)
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(IndexSignaturePrefix+hex.EncodeToString(sum[:])))) + "\n")
}

func (s *Service) fetch(ctx context.Context, u string, limit int64) ([]byte, error) {
	return Fetch(ctx, s.client, u, limit)
}

// Fetch downloads at most limit bytes from a market URL. Each node also uses
// it to obtain market-installed packages itself.
func Fetch(ctx context.Context, client *http.Client, u string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, core.ErrInvalidArgument.WithMessage("invalid market URL")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, core.ErrUnavailable.WithMessage("market fetch failed").WithCause(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, core.ErrUnavailable.WithMessage(fmt.Sprintf("market fetch %s: HTTP %d", redact(u), resp.StatusCode))
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, core.ErrUnavailable.WithMessage("market fetch failed").WithCause(err)
	}
	if int64(len(b)) > limit {
		return nil, core.ErrInvalidArgument.WithMessage(fmt.Sprintf("market download exceeds %d bytes", limit))
	}
	return b, nil
}

func redact(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return "url"
	}
	p.User, p.RawQuery = nil, ""
	return p.String()
}

// MarketPlugin is one listing row.
type MarketPlugin struct {
	SourceID         int64              `json:"source_id"`
	SourceName       string             `json:"source_name"`
	Key              string             `json:"key"`
	Name             core.LocalizedText `json:"name"`
	Description      core.LocalizedText `json:"description"`
	Publisher        string             `json:"publisher"`
	Trust            string             `json:"trust"` // registered trust, "unknown" otherwise
	LatestVersion    string             `json:"latest_version"`
	Versions         []MarketVersion    `json:"versions"`
	InstalledVersion string             `json:"installed_version,omitempty"`
	InstalledStatus  string             `json:"installed_status,omitempty"`
	UpdateAvailable  bool               `json:"update_available"`
}

type MarketVersion struct {
	Version    string `json:"version"`
	Size       int64  `json:"size"`
	HostCompat string `json:"host_compat"`
	// Compatible reports whether this host could install the version:
	// host_compat is matched against the core version with any
	// pre-release suffix ignored, exactly as install-time validation does
	// (an empty or malformed range is incompatible, since install rejects
	// it).
	Compatible bool `json:"compatible"`
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

// ListPlugins lists the plugins of one source (sourceID > 0) or of every
// enabled source, annotated with install state and update availability.
func (s *Service) ListPlugins(ctx context.Context, sourceID int64, refresh bool) ([]MarketPlugin, error) {
	var ids []int64
	if sourceID > 0 {
		ids = []int64{sourceID}
	} else {
		srcs, err := s.ListSources(ctx)
		if err != nil {
			return nil, err
		}
		for _, src := range srcs {
			if src.Enabled {
				ids = append(ids, src.ID)
			}
		}
	}
	installed, err := s.installed(ctx)
	if err != nil {
		return nil, err
	}
	trust, err := s.trustLevels(ctx)
	if err != nil {
		return nil, err
	}
	out := []MarketPlugin{}
	for _, id := range ids {
		idx, src, err := s.Index(ctx, id, refresh)
		if err != nil {
			if sourceID > 0 {
				return nil, err
			}
			continue // skip broken sources when listing all
		}
		for _, p := range idx.Plugins {
			mp := MarketPlugin{SourceID: src.ID, SourceName: src.Name, Key: p.Key, Name: p.Name, Description: p.Description,
				Publisher: p.Publisher, Trust: "unknown", Versions: []MarketVersion{}}
			if t, ok := trust[p.Publisher]; ok {
				mp.Trust = t
			}
			var latest *semver.Version
			for _, v := range sortedVersions(p.Versions) {
				ok := Compatible(v.HostCompat, s.inst.HostVersion())
				mp.Versions = append(mp.Versions, MarketVersion{Version: v.Version, Size: v.Size, HostCompat: v.HostCompat, Compatible: ok})
				sv, err := semver.NewVersion(v.Version)
				if err == nil && ok && (latest == nil || sv.GreaterThan(latest)) {
					latest = sv
					mp.LatestVersion = v.Version
				}
			}
			if inst, ok := installed[p.Key]; ok {
				mp.InstalledVersion, mp.InstalledStatus = inst[0], inst[1]
				cur, err := semver.NewVersion(inst[0])
				mp.UpdateAvailable = err == nil && latest != nil && latest.GreaterThan(cur)
			}
			out = append(out, mp)
		}
	}
	return out, nil
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

// installed maps plugin key -> [current version, status].
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

func (s *Service) trustLevels(ctx context.Context) (map[string]string, error) {
	var publishers []model.Publisher
	if err := store.G(ctx, s.db.Gorm).Select("name", "trust_level", "status").Find(&publishers).Error; err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, p := range publishers {
		t := p.TrustLevel
		if core.ValueOr(p.Status, "") != "active" {
			t = "revoked"
		}
		out[p.Name] = t
	}
	return out, nil
}

func findVersion(idx *Index, key, version string) (*IndexVersion, error) {
	for _, p := range idx.Plugins {
		if p.Key != key {
			continue
		}
		for i := range p.Versions {
			if p.Versions[i].Version == version {
				return &p.Versions[i], nil
			}
		}
	}
	return nil, core.ErrNotFound.WithMessage(fmt.Sprintf("plugin %s %s not found in market index", key, version))
}
