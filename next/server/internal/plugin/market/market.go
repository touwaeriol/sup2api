// Package market is the plugin market client: static index.json v2 files
// read over HTTPS (CONTRACTS §11.1, §136), listing and one-click install.
package market

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	rc "github.com/Sub2API-Devs/sup2api/next/runtime-contract"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

const (
	cacheTTL      = 5 * time.Minute
	maxIndexBytes = 8 << 20
)

// Source is a market_sources row.
type Source struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

// Installer is the part of install.Service the market needs: the host
// version the index entries are checked against. Downloaded packages go to
// the installer through the console API (Download).
type Installer interface {
	HostVersion() string
}

// Options configure New.
type Options struct {
	// MaxPackageBytes bounds package downloads (config MaxPackageBytes;
	// rc.PluginPackageMaxBytes when 0).
	MaxPackageBytes int64
	// AllowHTTP accepts http:// source and package URLs besides https://
	// (SUB2API_MARKET_ALLOW_HTTP: local markets of test stacks and development).
	AllowHTTP bool
}

// Service fetches and caches market indexes.
type Service struct {
	db     *store.DB
	inst   Installer
	client *http.Client
	pkg    *http.Client // client with at least rc.PluginPackageTransferTimeout
	opt    Options
	now    func() time.Time

	mu    sync.Mutex
	cache map[int64]cached
}

type cached struct {
	at       time.Time
	indexURL string
	idx      *Index
}

// New builds the market client. client nil uses a 60 s timeout client;
// package downloads use a copy of it with at least
// rc.PluginPackageTransferTimeout. Both copies follow a redirect only to an
// allowed URL (CheckRedirect).
func New(db *store.DB, inst Installer, client *http.Client, opt Options) *Service {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	if opt.MaxPackageBytes <= 0 {
		opt.MaxPackageBytes = rc.PluginPackageMaxBytes
	}
	s := &Service{db: db, inst: inst, opt: opt, now: time.Now, cache: map[int64]cached{}}
	idxClient, pkgClient := *client, *client
	idxClient.CheckRedirect = CheckRedirect(opt.AllowHTTP)
	pkgClient.CheckRedirect = idxClient.CheckRedirect
	if pkgClient.Timeout != 0 && pkgClient.Timeout < rc.PluginPackageTransferTimeout {
		pkgClient.Timeout = rc.PluginPackageTransferTimeout
	}
	s.client, s.pkg = &idxClient, &pkgClient
	return s
}

// maxRedirects is net/http's default limit on redirects.
const maxRedirects = 10

// CheckRedirect is the redirect policy of every market download (the
// market client's, and each node's download of a market package): a
// redirect is followed only to a URL whose scheme a market URL may have
// (schemeAllowed), so a market served over https cannot hand the core a
// plain http URL.
func CheckRedirect(allowHTTP bool) func(req *http.Request, via []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if !schemeAllowed(req.URL, allowHTTP) {
			return fmt.Errorf("market redirect to a %s URL refused", req.URL.Scheme)
		}
		if len(via) >= maxRedirects {
			return fmt.Errorf("stopped after %d redirects", maxRedirects)
		}
		return nil
	}
}

// The official plugin store (CONTRACTS §136): the only market source. Its
// row keeps an id so that a configurable store address can be added later.
const (
	OfficialName = "official"
	OfficialURL  = "https://raw.githubusercontent.com/touwaeriol/sup2api-plugin/index/index.json"
)

// EnsureOfficial makes the official store the only market source at start:
// its row takes indexURL ("" = OfficialURL; SUB2API_MARKET_URL points it at
// the local market of a test stack or development) and every other row is
// deleted.
func (s *Service) EnsureOfficial(ctx context.Context, indexURL string) error {
	if indexURL == "" {
		indexURL = OfficialURL
	}
	if _, err := s.indexURL(indexURL); err != nil {
		return fmt.Errorf("market url: %w", err)
	}
	return s.db.Tx(ctx, func(tx *gorm.DB) error {
		g := store.G(ctx, tx)
		if err := seedSource(g, OfficialName, indexURL).Error; err != nil {
			return err
		}
		return otherSources(g).Delete(&model.MarketSource{}).Error
	})
}

// otherSources selects the market sources besides the official one.
func otherSources(db *gorm.DB) *gorm.DB {
	return db.Where("name <> ?", OfficialName)
}

// seedSource upserts the source name: an existing one takes addr.
func seedSource(db *gorm.DB, name, addr string) *gorm.DB {
	return db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "name"}}, DoUpdates: clause.AssignmentColumns([]string{"url"})}).
		Create(&model.MarketSource{Name: name, URL: addr})
}

// sources selects market_sources rows as Source, whose fields are its
// columns.
func sources(db *gorm.DB) *gorm.DB {
	return db.Table("market_sources").Select("id, name, url, enabled, created_at")
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

// schemeAllowed reports whether a market URL may use the scheme of u:
// https, or http with allowHTTP (Options.AllowHTTP).
func schemeAllowed(u *url.URL, allowHTTP bool) bool {
	return u.Scheme == "https" || allowHTTP && u.Scheme == "http"
}

// indexURL is the index.json URL of a source URL: absolute, https (or
// http with AllowHTTP); a directory (ending in "/") gets index.json.
func (s *Service) indexURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !schemeAllowed(u, s.opt.AllowHTTP) || u.Host == "" {
		return nil, errors.New("url must be an absolute https URL (http only with SUB2API_MARKET_ALLOW_HTTP)")
	}
	if strings.HasSuffix(u.Path, "/") || u.Path == "" {
		u.Path = strings.TrimSuffix(u.Path, "/") + "/index.json"
	}
	return u, nil
}

// Index returns the index of a source (cached for 5 minutes).
func (s *Service) Index(ctx context.Context, sourceID int64, refresh bool) (*Index, *Source, error) {
	src, err := s.source(ctx, sourceID)
	if err != nil {
		return nil, nil, err
	}
	u, err := s.indexURL(src.URL)
	if err != nil {
		return nil, nil, core.ErrInvalidArgument.WithMessage(err.Error())
	}
	if idx := s.cached(src.ID, u.String()); idx != nil && !refresh {
		return idx, src, nil
	}
	idx, err := s.fetchIndex(ctx, u.String())
	if err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	s.cache[src.ID] = cached{at: s.now(), indexURL: u.String(), idx: idx}
	s.mu.Unlock()
	return idx, src, nil
}

// cached is the index of source id read from indexURL less than cacheTTL
// ago, nil when there is none.
func (s *Service) cached(id int64, indexURL string) *Index {
	s.mu.Lock()
	c, ok := s.cache[id]
	s.mu.Unlock()
	if !ok || c.indexURL != indexURL || s.now().Sub(c.at) >= cacheTTL {
		return nil
	}
	return c.idx
}

// fetchIndex downloads and parses the index at u.
func (s *Service) fetchIndex(ctx context.Context, u string) (*Index, error) {
	raw, err := Fetch(ctx, s.client, u, maxIndexBytes)
	if err != nil {
		return nil, err
	}
	idx, err := parseIndex(raw, s.opt.AllowHTTP)
	if err != nil {
		return nil, core.ErrUnavailable.WithMessage("market index is invalid: " + err.Error()).WithCause(err)
	}
	return idx, nil
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
