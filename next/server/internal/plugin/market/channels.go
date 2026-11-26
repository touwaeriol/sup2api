package market

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// The market_sources rows are the channels of the stores (CONTRACTS
// §137.6). Each store has one default channel, which a listing, review or
// install of the store without a channel reads; without a store either, the
// official store's.

// The default channel of a new store, and the official store's that
// migration 0125 seeds (EnsureStores brings it back if a database lost it).
const (
	DefaultChannelName   = "正式"
	DefaultChannelBranch = "main"
)

// Source is a channel of a store: a market_sources row with its store.
// URL is not stored but derived (Service.withURL).
type Source struct {
	ID        int64     `json:"id"`
	StoreID   int64     `json:"store_id"`
	StoreName string    `json:"store_name"`
	Name      string    `json:"name"`
	Branch    string    `json:"branch"`
	URL       string    `json:"url" gorm:"-"` // the channel's index
	Enabled   bool      `json:"enabled"`
	IsDefault bool      `json:"is_default"`
	CreatedAt time.Time `json:"created_at"`
	Official  bool      `json:"-"` // the store's
	Repo      string    `json:"-"` // the store's
	// Warning is set on a channel just created whose index cannot be read
	// yet (WarningNoIndex); the channel is saved anyway.
	Warning string `json:"warning,omitempty" gorm:"-"`
}

// WarningNoIndex is the warning of a new channel whose branch has
// no readable index yet: CI publishes index-<branch> after its first push.
const WarningNoIndex = "no_index"

// probe fetches the index at url once: "" when it reads, WarningNoIndex
// otherwise.
func (s *Service) probe(ctx context.Context, url string) string {
	u, err := s.indexURL(url)
	if err == nil {
		_, err = s.fetchIndex(ctx, u.String())
	}
	if err != nil {
		return WarningNoIndex
	}
	return ""
}

// branchRe is the rule of a channel's branch: one git ref name segment.
var branchRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// sources selects market_sources rows with their store as Source.
func sources(db *gorm.DB) *gorm.DB {
	return db.Table("market_sources AS c").Joins("JOIN market_stores AS st ON st.id = c.store_id").
		Select("c.id, c.store_id, st.name AS store_name, c.name, c.branch, c.enabled, c.is_default, c.created_at, st.official, st.repo")
}

// ListSources returns the channels of store storeID (every store's for
// 0): the official store's first, each store's default first.
func (s *Service) ListSources(ctx context.Context, storeID int64) ([]Source, error) {
	q := sources(store.G(ctx, s.db.Gorm)).Order("st.official DESC, c.store_id, c.is_default DESC, c.id")
	if storeID > 0 {
		q = q.Where("c.store_id = ?", storeID)
	}
	out := []Source{}
	if err := q.Find(&out).Error; err != nil {
		return nil, err
	}
	for i := range out {
		s.withURL(&out[i])
	}
	return out, nil
}

// source is the enabled channel id, the official store's default one for 0.
func (s *Service) source(ctx context.Context, id int64) (*Source, error) {
	q := sources(store.G(ctx, s.db.Gorm)).Where("c.id = ?", id)
	if id == 0 {
		q = sources(store.G(ctx, s.db.Gorm)).Where("st.official AND c.is_default")
	}
	src, err := takeSource(q)
	if err != nil {
		return nil, err
	}
	return s.withURL(src), nil
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
	return &src, nil
}

// ListsPublisher reports whether the index of an enabled channel of the
// official store, fetched afresh, lists publisher id
// (install.StorePublishers): a publisher of the test channel reaches
// production when its branch is merged. A channel whose index is not
// published (404) lists no one; any other failure to read one refuses.
func (s *Service) ListsPublisher(ctx context.Context, id string) (bool, error) {
	var ids []int64
	g := store.G(ctx, s.db.Gorm)
	official := g.Model(&model.MarketStore{}).Select("id").Where("official")
	err := g.Model(&model.MarketSource{}).Where("enabled AND store_id IN (?)", official).Pluck("id", &ids).Error
	if err != nil {
		return false, err
	}
	for _, src := range ids {
		idx, _, err := s.Index(ctx, src, true)
		switch {
		case errors.Is(err, ErrNotPublished):
			continue // a channel without an index yet lists no one
		case err != nil:
			return false, err
		}
		if _, ok := idx.publisher(id); ok {
			return true, nil
		}
	}
	return false, nil
}

// ChannelInput is the body of POST /market/stores/:id/sources.
type ChannelInput struct {
	Name   string `json:"name"`
	Branch string `json:"branch"`
}

// ChannelPatch is the body of PATCH /market/sources/:id: the fields it
// changes. is_default true makes the channel its store's default; the
// default stays one until another channel takes its place.
type ChannelPatch struct {
	Name      *string `json:"name"`
	Enabled   *bool   `json:"enabled"`
	IsDefault *bool   `json:"is_default"`
}

// CreateChannel adds an enabled channel to store storeID.
func (s *Service) CreateChannel(ctx context.Context, storeID int64, in ChannelInput, actorID int64) (*Source, error) {
	in.Name = strings.TrimSpace(in.Name)
	errs := nameErrors(in.Name)
	if !validBranch(in.Branch) {
		errs = append(errs, branchError())
	}
	if len(errs) > 0 {
		return nil, core.InvalidFields(errs...)
	}
	st, err := s.store(ctx, storeID)
	if err != nil {
		return nil, err
	}
	row := model.MarketSource{StoreID: st.ID, Name: in.Name, Branch: in.Branch}
	err = s.db.Tx(ctx, func(tx *gorm.DB) error {
		if err := store.G(ctx, tx).Create(&row).Error; err != nil {
			return err
		}
		return audit.Audit(ctx, tx, actorID, "market.channel.create", "market_channel", strconv.FormatInt(row.ID, 10), row)
	})
	if err != nil {
		return nil, channelError(err)
	}
	src, err := s.channel(ctx, row.ID)
	if err == nil {
		src.Warning = s.probe(ctx, src.URL)
	}
	return src, err
}

// UpdateChannel renames, enables or disables channel id or makes it its
// store's default. The default channel cannot be disabled, nor a disabled
// one be the default.
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

// applyPatch sets the columns set of p on channel id, which it locks; a new
// default takes the place of its store's old one, under the lock of the
// store so that concurrent new defaults take turns.
func applyPatch(g *gorm.DB, id int64, p ChannelPatch, set map[string]any) error {
	if p.makesDefault() {
		if err := lockStoreOf(g, id); err != nil {
			return err
		}
	}
	cur, err := lockRow[model.MarketSource](g, id, "market channel")
	if err != nil {
		return err
	}
	if err := p.check(cur); err != nil || len(set) == 0 {
		return err
	}
	if p.makesDefault() {
		if err := otherDefault(g, cur).Update("is_default", false).Error; err != nil {
			return err
		}
	}
	return g.Model(&model.MarketSource{}).Where("id = ?", id).Updates(set).Error
}

// lockStoreOf locks the store of channel id before the channel itself.
// Taking the channel first deadlocked: one transaction held the store and
// waited for the old default's row, which another making that row the
// default held while it waited for the store.
func lockStoreOf(g *gorm.DB, id int64) error {
	var storeID int64
	if err := g.Model(&model.MarketSource{}).Where("id = ?", id).Select("store_id").Scan(&storeID).Error; err != nil {
		return err
	}
	if storeID == 0 {
		return nil // the channel lock that follows reports it missing
	}
	_, err := lockRow[model.MarketStore](g, storeID, "plugin store")
	return err
}

// DeleteChannel deletes channel id, which may not be its store's default.
func (s *Service) DeleteChannel(ctx context.Context, id int64, actorID int64) error {
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		g := store.G(ctx, tx)
		cur, err := lockRow[model.MarketSource](g, id, "market channel")
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
		s.forget(id)
	}
	return err
}

// channel reads channel id, enabled or not.
func (s *Service) channel(ctx context.Context, id int64) (*Source, error) {
	var src Source
	if err := sources(store.G(ctx, s.db.Gorm)).Where("c.id = ?", id).Take(&src).Error; err != nil {
		return nil, err
	}
	return s.withURL(&src), nil
}

// otherDefault selects the default channel of cur's store unless it is cur.
func otherDefault(db *gorm.DB, cur *model.MarketSource) *gorm.DB {
	return db.Model(&model.MarketSource{}).Where("is_default AND store_id = ? AND id <> ?", cur.StoreID, cur.ID)
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

// validBranch reports whether branch may name a channel: one git ref name
// segment, which "index-<branch>" stays.
func validBranch(branch string) bool {
	return branchRe.MatchString(branch) && !strings.Contains(branch, "..") && !strings.HasPrefix(branch, ".") && !strings.HasSuffix(branch, ".lock")
}

// branchError is the field error of an invalid branch.
func branchError() core.FieldError {
	return core.FieldError{Field: "branch", Code: "invalid_format", Message: "branch must match " + branchRe.String() + " without \"..\""}
}

// channelError is err of a channel write, a conflict for a name or branch
// another channel of the store has.
func channelError(err error) error {
	if store.IsUniqueViolation(err, "") {
		return core.ErrConflict.WithMessage("another channel of the store has this name or branch")
	}
	return err
}
