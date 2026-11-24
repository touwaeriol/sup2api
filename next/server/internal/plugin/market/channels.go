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
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// The plugin store (CONTRACTS §137.6) is one GitHub repository, the
// official registry, fixed. Its channels are branches: CI publishes a
// channel's index to the branch index-<branch>, served under the store's
// base. The market_sources rows are the channels; one is the default, which
// a listing, review or install without a channel reads.
const (
	OfficialRepo = "touwaeriol/sup2api-plugin"
	OfficialBase = "https://raw.githubusercontent.com/" + OfficialRepo
)

// The channels migration 0125 seeds; EnsureChannels brings back the
// default one if a database lost it.
const (
	DefaultChannelName   = "正式"
	DefaultChannelBranch = "main"
)

// Source is a channel of the store: a market_sources row, and the store's
// repository.
type Source struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Branch    string    `json:"branch"`
	URL       string    `json:"url"` // the channel's index, derived from the base and Branch
	Enabled   bool      `json:"enabled"`
	IsDefault bool      `json:"is_default"`
	Repo      string    `json:"repo" gorm:"-"`
	CreatedAt time.Time `json:"created_at"`
}

// branchRe is the rule of a channel's branch: one git ref name segment.
var branchRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// maxChannelName is the width of market_sources.name.
const maxChannelName = 100

// storeBase is the store's base: base (SUB2API_MARKET_BASE_URL), or
// OfficialBase when it is empty, without a trailing "/".
func storeBase(base string) string {
	if base = strings.TrimSpace(base); base == "" {
		base = OfficialBase
	}
	return strings.TrimSuffix(base, "/")
}

// channelURL is the index URL of the channel of branch.
func (s *Service) channelURL(branch string) string {
	return s.base + "/index-" + branch + "/index.json"
}

// ValidateBase checks the store's base (Options.BaseURL) as a market URL:
// https, or http with Options.AllowHTTP. Every node checks it at start;
// only the node that may write the channels calls EnsureChannels.
func (s *Service) ValidateBase() error {
	if _, err := s.indexURL(s.channelURL(DefaultChannelBranch)); err != nil {
		return fmt.Errorf("market base url: %w", err)
	}
	return nil
}

// EnsureChannels brings the channels in line with this core at start: each
// row's url follows the base (a changed SUB2API_MARKET_BASE_URL takes
// effect), and a database without a default channel gets the main one
// back as the default.
func (s *Service) EnsureChannels(ctx context.Context) error {
	if err := s.ValidateBase(); err != nil {
		return err
	}
	return s.db.Tx(ctx, func(tx *gorm.DB) error {
		g := store.G(ctx, tx)
		if err := staleURLs(g, s.base).Update("url", channelURLExpr(s.base)).Error; err != nil {
			return err
		}
		return ensureDefault(g, s.channelURL(DefaultChannelBranch))
	})
}

// channelURLExpr is channelURL of each row's branch, in SQL.
func channelURLExpr(base string) any {
	return gorm.Expr("? || branch || ?", base+"/index-", "/index.json")
}

// staleURLs selects the channels whose url does not follow base.
func staleURLs(db *gorm.DB, base string) *gorm.DB {
	return db.Model(&model.MarketSource{}).Where("url <> ? || branch || ?", base+"/index-", "/index.json")
}

// ensureDefault makes the main channel (at url) the default, enabled,
// unless a channel is the default.
func ensureDefault(g *gorm.DB, url string) error {
	ok, err := gormpg.Exists(g, defaultChannel(g))
	if err != nil || ok {
		return err
	}
	return upsertDefault(g, url).Error
}

// defaultChannel selects the default channel.
func defaultChannel(db *gorm.DB) *gorm.DB {
	return db.Model(&model.MarketSource{}).Where("is_default")
}

// upsertDefault adds the main channel (at url) as the default, or makes it
// the default, enabled.
func upsertDefault(db *gorm.DB, url string) *gorm.DB {
	set := clause.Assignments(map[string]any{"enabled": true, "is_default": true})
	return db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "branch"}}, DoUpdates: set}).
		Create(&model.MarketSource{Name: DefaultChannelName, Branch: DefaultChannelBranch, URL: url, IsDefault: true})
}

// sources selects market_sources rows as Source, whose fields are its
// columns.
func sources(db *gorm.DB) *gorm.DB {
	return db.Table("market_sources").Select("id, name, branch, url, enabled, is_default, created_at")
}

// ListSources returns the channels, the default first.
func (s *Service) ListSources(ctx context.Context) ([]Source, error) {
	out := []Source{}
	if err := sources(store.G(ctx, s.db.Gorm)).Order("is_default DESC, id").Find(&out).Error; err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Repo = OfficialRepo
	}
	return out, nil
}

// source is the enabled channel id, the default one for 0.
func (s *Service) source(ctx context.Context, id int64) (*Source, error) {
	q := sources(store.G(ctx, s.db.Gorm))
	if id == 0 {
		return takeSource(q.Where("is_default"))
	}
	return takeSource(q.Where("id = ?", id))
}

// takeSource reads the channel q selects: 404 when it is missing or
// disabled.
func takeSource(q *gorm.DB) (*Source, error) {
	var src Source
	err := q.Take(&src).Error
	if store.IsNoRows(err) {
		return nil, core.ErrNotFound.WithMessage("market channel not found")
	}
	if err != nil {
		return nil, err
	}
	if !src.Enabled {
		return nil, core.ErrNotFound.WithMessage("market channel is disabled")
	}
	src.Repo = OfficialRepo
	return &src, nil
}

// ListsPublisher reports whether the index of an enabled channel, fetched
// afresh, lists publisher id (install.StorePublishers): a publisher of the
// test channel reaches production when its branch is merged.
func (s *Service) ListsPublisher(ctx context.Context, id string) (bool, error) {
	srcs, err := s.ListSources(ctx)
	if err != nil {
		return false, err
	}
	for _, src := range srcs {
		if !src.Enabled {
			continue
		}
		idx, _, err := s.Index(ctx, src.ID, true)
		if err != nil {
			return false, err
		}
		if _, ok := idx.publisher(id); ok {
			return true, nil
		}
	}
	return false, nil
}

// ChannelInput is the body of POST /market/sources.
type ChannelInput struct {
	Name   string `json:"name"`
	Branch string `json:"branch"`
}

// ChannelPatch is the body of PATCH /market/sources/:id: the fields it
// changes. is_default true makes the channel the default; the default
// stays one until another channel takes its place.
type ChannelPatch struct {
	Name      *string `json:"name"`
	Enabled   *bool   `json:"enabled"`
	IsDefault *bool   `json:"is_default"`
}

// CreateChannel adds an enabled channel of the store.
func (s *Service) CreateChannel(ctx context.Context, in ChannelInput, actorID int64) (*Source, error) {
	in.Name = strings.TrimSpace(in.Name)
	errs := nameErrors(in.Name)
	if !validBranch(in.Branch) {
		errs = append(errs, core.FieldError{Field: "branch", Code: "invalid_format", Message: "branch must match " + branchRe.String() + " without \"..\""})
	}
	if len(errs) > 0 {
		return nil, core.InvalidFields(errs...)
	}
	row := model.MarketSource{Name: in.Name, Branch: in.Branch, URL: s.channelURL(in.Branch)}
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		if err := store.G(ctx, tx).Create(&row).Error; err != nil {
			return err
		}
		return audit.Audit(ctx, tx, actorID, "market.channel.create", "market_channel", strconv.FormatInt(row.ID, 10), in)
	})
	if err != nil {
		return nil, channelError(err)
	}
	return s.channel(ctx, row.ID)
}

// UpdateChannel renames, enables or disables channel id or makes it the
// default. The default channel cannot be disabled, nor a disabled one be
// the default.
func (s *Service) UpdateChannel(ctx context.Context, id int64, p ChannelPatch, actorID int64) (*Source, error) {
	set, err := p.columns()
	if err != nil {
		return nil, err
	}
	err = s.db.Tx(ctx, func(tx *gorm.DB) error {
		if err := applyPatch(store.G(ctx, tx), id, p, set); err != nil {
			return err
		}
		return audit.Audit(ctx, tx, actorID, "market.channel.update", "market_channel", strconv.FormatInt(id, 10), set)
	})
	if err != nil {
		return nil, channelError(err)
	}
	return s.channel(ctx, id)
}

// applyPatch sets the columns set of p on channel id, which it locks
// first; a new default takes the place of the old one.
func applyPatch(g *gorm.DB, id int64, p ChannelPatch, set map[string]any) error {
	cur, err := lockChannel(g, id)
	if err != nil {
		return err
	}
	if err := p.check(cur); err != nil || len(set) == 0 {
		return err
	}
	if p.makesDefault() {
		if err := otherDefault(g, id).Update("is_default", false).Error; err != nil {
			return err
		}
	}
	return g.Model(&model.MarketSource{}).Where("id = ?", id).Updates(set).Error
}

// DeleteChannel deletes channel id, which may not be the default.
func (s *Service) DeleteChannel(ctx context.Context, id int64, actorID int64) error {
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		g := store.G(ctx, tx)
		cur, err := lockChannel(g, id)
		if err != nil {
			return err
		}
		if cur.IsDefault {
			return core.ErrConflict.WithMessage("the default channel cannot be deleted; make another channel the default first")
		}
		if err := g.Where("id = ?", id).Delete(&model.MarketSource{}).Error; err != nil {
			return err
		}
		return audit.Audit(ctx, tx, actorID, "market.channel.delete", "market_channel", strconv.FormatInt(id, 10), map[string]any{"branch": cur.Branch})
	})
	if err == nil {
		s.mu.Lock()
		delete(s.cache, id)
		s.mu.Unlock()
	}
	return err
}

// channel reads channel id, enabled or not.
func (s *Service) channel(ctx context.Context, id int64) (*Source, error) {
	var src Source
	if err := sources(store.G(ctx, s.db.Gorm)).Where("id = ?", id).Take(&src).Error; err != nil {
		return nil, err
	}
	src.Repo = OfficialRepo
	return &src, nil
}

// lockChannel reads and locks channel id: 404 when there is none.
func lockChannel(g *gorm.DB, id int64) (*model.MarketSource, error) {
	if err := store.RequireTx(g); err != nil {
		return nil, err
	}
	var row model.MarketSource
	err := g.Where("id = ?", id).Clauses(store.ForUpdate()).Take(&row).Error
	if store.IsNoRows(err) {
		return nil, core.ErrNotFound.WithMessage("market channel not found")
	}
	return &row, err
}

// otherDefault selects the default channel unless it is id.
func otherDefault(db *gorm.DB, id int64) *gorm.DB {
	return db.Model(&model.MarketSource{}).Where("is_default AND id <> ?", id)
}

// columns are the columns p sets.
func (p ChannelPatch) columns() (map[string]any, error) {
	set := map[string]any{}
	if p.Name != nil {
		name := strings.TrimSpace(*p.Name)
		if errs := nameErrors(name); len(errs) > 0 {
			return nil, core.InvalidFields(errs...)
		}
		set["name"] = name
	}
	if p.Enabled != nil {
		set["enabled"] = *p.Enabled
	}
	if p.makesDefault() {
		set["is_default"] = true
	}
	if len(set) == 0 && p.IsDefault == nil {
		return nil, core.ErrInvalidArgument.WithMessage("name, enabled or is_default is required")
	}
	return set, nil
}

func (p ChannelPatch) makesDefault() bool { return p.IsDefault != nil && *p.IsDefault }

// check refuses what p would leave without an enabled default: disabling
// the default, unsetting it, or making a channel the default while it ends
// up disabled.
func (p ChannelPatch) check(cur *model.MarketSource) error {
	isDefault := cur.IsDefault || p.makesDefault()
	enabled := core.ValueOr(cur.Enabled, false)
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	switch {
	case p.IsDefault != nil && !*p.IsDefault && cur.IsDefault:
		return core.ErrConflict.WithMessage("the default channel stays the default until another channel is made the default")
	case isDefault && !enabled:
		return core.ErrConflict.WithMessage("the default channel must be enabled")
	}
	return nil
}

// nameErrors are the problems of a channel's display name.
func nameErrors(name string) []core.FieldError {
	if name == "" || utf8.RuneCountInString(name) > maxChannelName {
		return []core.FieldError{{Field: "name", Code: "invalid", Message: fmt.Sprintf("name must be 1-%d characters", maxChannelName)}}
	}
	return nil
}

// validBranch reports whether branch may name a channel: one git ref name
// segment, which "index-<branch>" stays.
func validBranch(branch string) bool {
	return branchRe.MatchString(branch) && !strings.Contains(branch, "..") && !strings.HasPrefix(branch, ".") && !strings.HasSuffix(branch, ".lock")
}

// channelError is err of a channel write, a conflict for a name or branch
// another channel has.
func channelError(err error) error {
	if store.IsUniqueViolation(err, "") {
		return core.ErrConflict.WithMessage("another channel has this name or branch")
	}
	return err
}
