package market

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// A plugin store (CONTRACTS §137.6) is a GitHub repository of a plugin
// registry, read at its base https://raw.githubusercontent.com/<repo>. Its
// channels are branches: CI publishes a channel's index to the branch
// index-<branch>. The official store is seeded and stays; an administrator
// adds others, whose packages install with at most community trust and
// never as the first-party publisher.
const (
	OfficialRepo      = "touwaeriol/sup2api-plugin"
	OfficialStoreName = "官方商店"
	githubRaw         = "https://raw.githubusercontent.com/"
	OfficialBase      = githubRaw + OfficialRepo
)

// Store is a market_stores row.
type Store struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Repo      string    `json:"repo"`
	Official  bool      `json:"official"`
	CreatedAt time.Time `json:"created_at"`
}

// repoRe is the rule of a store's repository: owner/name on GitHub.
var repoRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9._-]{1,100}$`)

// maxName is the width of market_stores.name and market_sources.name.
const maxName = 100

// officialBase is the official store's base: base (SUB2API_MARKET_BASE_URL),
// or OfficialBase when it is empty, without a trailing "/".
func officialBase(base string) string {
	if base = strings.TrimSpace(base); base == "" {
		base = OfficialBase
	}
	return strings.TrimSuffix(base, "/")
}

// storeBase is the base of the store of repo: the official store's
// (Options.BaseURL) when official.
func (s *Service) storeBase(official bool, repo string) string {
	if official {
		return s.base
	}
	return githubRaw + repo
}

// channelURL is the index URL of the channel of branch under base.
func channelURL(base, branch string) string {
	return base + "/index-" + branch + "/index.json"
}

// ValidateBase checks the official store's base (Options.BaseURL) as a
// market URL: https, or http with Options.AllowHTTP. Every node checks it at
// start; only the node that may write the stores calls EnsureStores.
func (s *Service) ValidateBase() error {
	if _, err := s.indexURL(channelURL(s.base, DefaultChannelBranch)); err != nil {
		return fmt.Errorf("market base url: %w", err)
	}
	return nil
}

// EnsureStores brings the stores in line with this core at start: the
// official store exists with a default channel (main unless it has one),
// and each channel's url follows its store's base (a changed
// SUB2API_MARKET_BASE_URL takes effect).
func (s *Service) EnsureStores(ctx context.Context) error {
	if err := s.ValidateBase(); err != nil {
		return err
	}
	return s.db.Tx(ctx, func(tx *gorm.DB) error {
		g := store.G(ctx, tx)
		id, err := ensureOfficial(g)
		if err != nil {
			return err
		}
		if err := ensureDefault(g, id, channelURL(s.base, DefaultChannelBranch)); err != nil {
			return err
		}
		return s.refreshURLs(g)
	})
}

// ensureOfficial adds the official store unless it is there and returns
// its id.
func ensureOfficial(g *gorm.DB) (int64, error) {
	set := clause.Assignments(map[string]any{"official": true})
	err := g.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "repo"}}, DoUpdates: set}).
		Create(&model.MarketStore{Name: OfficialStoreName, Repo: OfficialRepo, Official: true}).Error
	if err != nil {
		return 0, err
	}
	var id int64
	err = g.Model(&model.MarketStore{}).Where("official").Select("id").Scan(&id).Error
	return id, err
}

// refreshURLs rewrites the url of each channel that does not follow its
// store's base.
func (s *Service) refreshURLs(g *gorm.DB) error {
	var stores []model.MarketStore
	if err := g.Select("id", "repo", "official").Find(&stores).Error; err != nil {
		return err
	}
	for _, st := range stores {
		base := s.storeBase(st.Official, st.Repo)
		if err := staleURLs(g, st.ID, base).Update("url", channelURLExpr(base)).Error; err != nil {
			return err
		}
	}
	return nil
}

// channelURLExpr is channelURL of each row's branch under base, in SQL.
func channelURLExpr(base string) any {
	return gorm.Expr("? || branch || ?", base+"/index-", "/index.json")
}

// staleURLs selects the channels of store id whose url does not follow base.
func staleURLs(db *gorm.DB, id int64, base string) *gorm.DB {
	return db.Model(&model.MarketSource{}).Where("store_id = ? AND url <> ? || branch || ?", id, base+"/index-", "/index.json")
}

// ensureDefault makes the main channel (at url) of store id its default,
// enabled, unless the store has a default channel.
func ensureDefault(g *gorm.DB, id int64, url string) error {
	ok, err := gormpg.Exists(g, g.Model(&model.MarketSource{}).Where("store_id = ? AND is_default", id))
	if err != nil || ok {
		return err
	}
	set := clause.Assignments(map[string]any{"enabled": true, "is_default": true})
	return g.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "store_id"}, {Name: "branch"}}, DoUpdates: set}).
		Create(&model.MarketSource{StoreID: id, Name: DefaultChannelName, Branch: DefaultChannelBranch, URL: url, IsDefault: true}).Error
}

// ListStores returns the stores, the official one first.
func (s *Service) ListStores(ctx context.Context) ([]Store, error) {
	out := []Store{}
	if err := storeRows(store.G(ctx, s.db.Gorm)).Order("official DESC, id").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// storeRows selects market_stores rows as Store.
func storeRows(db *gorm.DB) *gorm.DB {
	return db.Model(&model.MarketStore{}).Select("id, name, repo, official, created_at")
}

// store reads store id: 404 when there is none.
func (s *Service) store(ctx context.Context, id int64) (*Store, error) {
	var st Store
	err := storeRows(store.G(ctx, s.db.Gorm)).Where("id = ?", id).Take(&st).Error
	if store.IsNoRows(err) {
		return nil, core.ErrNotFound.WithMessage("plugin store not found")
	}
	return &st, err
}

// StoreInput is the body of POST /market/stores: the store and the branch
// of its default channel.
type StoreInput struct {
	Name   string `json:"name"`
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
}

// StorePatch is the body of PATCH /market/stores/:id. A store's repository
// does not change; another repository is another store.
type StorePatch struct {
	Name *string `json:"name"`
}

// CreateStore adds a third-party store with its default channel.
func (s *Service) CreateStore(ctx context.Context, in StoreInput, actorID int64) (*Store, error) {
	if err := in.check(); err != nil {
		return nil, err
	}
	row := model.MarketStore{Name: in.Name, Repo: in.Repo}
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		g := store.G(ctx, tx)
		if err := g.Create(&row).Error; err != nil {
			return err
		}
		ch := model.MarketSource{StoreID: row.ID, Name: DefaultChannelName, Branch: in.Branch,
			URL: channelURL(s.storeBase(false, in.Repo), in.Branch), IsDefault: true}
		if err := g.Create(&ch).Error; err != nil {
			return err
		}
		return audit.Audit(ctx, tx, actorID, "market.store.create", "market_store", strconv.FormatInt(row.ID, 10), in)
	})
	if err != nil {
		return nil, storeError(err)
	}
	return s.store(ctx, row.ID)
}

// check trims in's name and checks its fields.
func (in *StoreInput) check() error {
	in.Name, in.Repo = strings.TrimSpace(in.Name), strings.TrimSpace(in.Repo)
	errs := nameErrors(in.Name)
	if !validRepo(in.Repo) {
		errs = append(errs, core.FieldError{Field: "repo", Code: "invalid_format", Message: "repo must be owner/name of a GitHub repository"})
	}
	if !validBranch(in.Branch) {
		errs = append(errs, branchError())
	}
	if len(errs) > 0 {
		return core.InvalidFields(errs...)
	}
	return nil
}

// UpdateStore renames store id.
func (s *Service) UpdateStore(ctx context.Context, id int64, p StorePatch, actorID int64) (*Store, error) {
	if p.Name == nil {
		return nil, core.ErrInvalidArgument.WithMessage("name is required")
	}
	name := strings.TrimSpace(*p.Name)
	if errs := nameErrors(name); len(errs) > 0 {
		return nil, core.InvalidFields(errs...)
	}
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		res := store.G(ctx, tx).Model(&model.MarketStore{}).Where("id = ?", id).Update("name", name)
		if res.Error == nil && res.RowsAffected == 0 {
			return core.ErrNotFound.WithMessage("plugin store not found")
		}
		if res.Error != nil {
			return res.Error
		}
		return audit.Audit(ctx, tx, actorID, "market.store.update", "market_store", strconv.FormatInt(id, 10), map[string]any{"name": name})
	})
	if err != nil {
		return nil, err
	}
	return s.store(ctx, id)
}

// DeleteStore deletes third-party store id with its channels. The
// publishers it registered stay, bound to no store.
func (s *Service) DeleteStore(ctx context.Context, id int64, actorID int64) error {
	var channels []int64
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		g := store.G(ctx, tx)
		st, err := lockStore(g, id)
		if err != nil {
			return err
		}
		if st.Official {
			return core.ErrConflict.WithMessage("the official store cannot be deleted")
		}
		if err := g.Model(&model.MarketSource{}).Where("store_id = ?", id).Pluck("id", &channels).Error; err != nil {
			return err
		}
		if err := g.Where("id = ?", id).Delete(&model.MarketStore{}).Error; err != nil {
			return err
		}
		return audit.Audit(ctx, tx, actorID, "market.store.delete", "market_store", strconv.FormatInt(id, 10), map[string]any{"repo": st.Repo})
	})
	if err == nil {
		s.forget(channels...)
	}
	return err
}

// lockStore reads and locks store id: 404 when there is none.
func lockStore(g *gorm.DB, id int64) (*model.MarketStore, error) {
	if err := store.RequireTx(g); err != nil {
		return nil, err
	}
	var row model.MarketStore
	err := g.Where("id = ?", id).Clauses(store.ForUpdate()).Take(&row).Error
	if store.IsNoRows(err) {
		return nil, core.ErrNotFound.WithMessage("plugin store not found")
	}
	return &row, err
}

// forget drops the cached indexes of channels ids.
func (s *Service) forget(ids ...int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		delete(s.cache, id)
	}
}

// capTrust is the trust level a store gives a publisher its index rates
// trust: the index's for the official store, at most community for another
// (CONTRACTS §137.6), which keeps it off the first-party publisher too.
func capTrust(trust string, official bool) string {
	if official || trust == "" {
		return trust
	}
	return pkg.TrustCommunity
}

// nameErrors are the problems of a store's or channel's display name.
func nameErrors(name string) []core.FieldError {
	if name == "" || utf8.RuneCountInString(name) > maxName {
		return []core.FieldError{{Field: "name", Code: "invalid", Message: fmt.Sprintf("name must be 1-%d characters", maxName)}}
	}
	return nil
}

// validRepo reports whether repo may name a store: owner/name on GitHub.
func validRepo(repo string) bool {
	_, name, _ := strings.Cut(repo, "/")
	return repoRe.MatchString(repo) && name != "." && name != ".." && !strings.HasSuffix(name, ".git")
}

// storeError is err of a store write, a conflict for a repository another
// store has.
func storeError(err error) error {
	if store.IsUniqueViolation(err, "") {
		return core.ErrConflict.WithMessage("another store has this repository")
	}
	return err
}
