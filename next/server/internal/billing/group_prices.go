package billing

import (
	"cmp"
	"context"
	"maps"
	"slices"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/group"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/segment"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

func (s *Service) registerPricingRoutes(r *httpapi.Router) {
	r.Perm("GET", "/pricing/catalog", "group:read", s.showCatalog)
	r.Perm("GET", "/groups/:id/model-prices", "group:read", s.showGroupPrices)
	r.Perm("POST", "/pricing/quote", "price:read", s.quotePricing).Uses(perm.GroupRead, perm.UserRead, perm.SegmentRead)
}

// pricedGroup is what pricing reads of a group.
type pricedGroup struct {
	core.GroupInfo
	public, active bool
}

func (g pricedGroup) ref() core.GroupRef { return core.GroupRef{ID: g.ID, Name: g.Name} }

// pricedGroupRow is a row of pricedGroups.
type pricedGroupRow struct {
	ID              int64
	Name            string
	RateMultiplier  decimal.Decimal
	ModelAllowlist  store.JSON[[]string]
	ModelFilterMode string
	Public, Active  bool
}

// loadGroups reads the groups (alias g) cond selects, by id.
func loadGroups(ctx context.Context, q *gorm.DB, cond string, args ...any) ([]pricedGroup, error) {
	var rows []pricedGroupRow
	if err := store.G(ctx, q).Table("groups g").Select(`g.id, g.name, g.rate_multiplier, g.model_allowlist, g.model_filter_mode,
		g.visibility = 'public' AS public, g.status = 'active' AS active`).Where(cond, args...).Order("g.id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]pricedGroup, len(rows))
	for i, r := range rows {
		out[i] = pricedGroup{public: r.Public, active: r.Active, GroupInfo: core.GroupInfo{ID: r.ID, Name: r.Name,
			RateMultiplier: r.RateMultiplier, ModelAllowlist: r.ModelAllowlist.V, ModelFilterMode: r.ModelFilterMode}}
	}
	return out, nil
}

// loadGroup reads the one group cond selects; not_found without one.
func loadGroup(ctx context.Context, q *gorm.DB, cond string, args ...any) (pricedGroup, error) {
	groups, err := loadGroups(ctx, q, cond, args...)
	if err != nil {
		return pricedGroup{}, err
	}
	if len(groups) == 0 {
		return pricedGroup{}, core.ErrNotFound.WithMessage(core.Text(ctx, "group not found", "分组不存在"))
	}
	return groups[0], nil
}

// catalog is the enabled global prices by model.
type catalog map[string]PriceDef

// enabledPrices selects columns of the enabled global prices.
func enabledPrices(db *gorm.DB, columns ...string) *gorm.DB {
	return db.Model(&model.ModelPrice{}).Select(columns).Where("enabled")
}

// models returns the priced models, sorted.
func (c catalog) models() []string { return slices.Sorted(maps.Keys(c)) }

// base returns the global price of model; nil when it has none.
func (c catalog) base(model string) *PriceDef {
	p, ok := c[model]
	if !ok {
		return nil
	}
	return &p
}

// CatalogPrice is a row of GET /pricing/catalog: a model with its global price.
type CatalogPrice struct {
	Model string   `json:"model"`
	Price PriceDef `json:"price"`
}

// GET /pricing/catalog: the enabled global prices, by model, from the price
// snapshot requests are billed by. A group's pricing table adds its models
// from it, also before the group exists.
func (s *Service) showCatalog(c *gin.Context) {
	snap, err := s.snapshot(c.Request.Context())
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	out := make([]CatalogPrice, 0, len(snap.bases))
	for _, model := range snap.bases.models() {
		out = append(out, CatalogPrice{Model: model, Price: snap.bases[model]})
	}
	httpapi.OK(c, out)
}

// offeredModels returns the models each of the groups provides
// (group.OfferedModels) against the models with a list price there in snap,
// by group id.
func offeredModels(ctx context.Context, q *gorm.DB, groups []pricedGroup, snap *priceSnapshot) (map[int64][]string, error) {
	infos := make([]core.GroupInfo, len(groups))
	for i, g := range groups {
		infos[i] = g.GroupInfo
	}
	return group.OfferedModels(ctx, q, infos, snap.priced)
}

// groupPrices is a group with the models it provides (sorted), worked out
// over the models that have a list price in the group.
type groupPrices struct {
	group  pricedGroup
	models []string
}

// loadGroupPrices reads the one group cond selects and the models it
// provides at the prices of snap; not_found without one.
func loadGroupPrices(ctx context.Context, q *gorm.DB, snap *priceSnapshot, cond string, args ...any) (*groupPrices, error) {
	g, err := loadGroup(ctx, q, cond, args...)
	if err != nil {
		return nil, err
	}
	offered, err := offeredModels(ctx, q, []pricedGroup{g}, snap)
	if err != nil {
		return nil, err
	}
	return &groupPrices{group: g, models: offered[g.ID]}, nil
}

// each calls add with the list price, and where it comes from, of each of
// models that has one in the group (listPrice). It is the one place the
// list prices of a group's models are read; the caller says which models
// and what to make of each.
func (snap *priceSnapshot) each(groupID int64, models []string, add func(model string, price PriceDef, source string)) {
	for _, model := range models {
		price, source := listPrice(snap.bases.base(model), snap.rules.of(core.PriceQuery{Model: model, GroupID: groupID}))
		if price != nil {
			add(model, *price, source)
		}
	}
}

// ModelPriceRow is a model of a group's pricing table: one the group
// provides, or sets a price or a rate on, or a user group's rate in it.
type ModelPriceRow struct {
	Model string `json:"model"`
	// Offered is false on a model the group, or a user group in it, sets a
	// price or a rate on but the group does not provide now (Models.Offered):
	// no request reaches it there yet.
	Offered bool `json:"offered"`
	// Base is the global price; null = the model has none: it is billed at
	// the group's price, and not at all without one.
	Base *PriceDef `json:"base"`
	// Price is the group's own price of the model; null = the global price.
	Price *Rule `json:"price"`
	// Rate is the group's rate for the model; null = the group's base rate.
	Rate *decimal.Decimal `json:"rate"`
}

// GroupModelPrices is the response of GET /groups/:id/model-prices.
type GroupModelPrices struct {
	Group core.GroupRef `json:"group"`
	// Rate is the group's base rate.
	Rate   segment.EffectiveRate `json:"rate"`
	Models []ModelPriceRow       `json:"models"`
}

// table is the pricing table of the group under the snapshot: a row for
// every model it provides or sets a price or a rate on, or a user group
// sets a rate on in it, by model.
func (gp *groupPrices) table(snap *priceSnapshot) *GroupModelPrices {
	g := gp.group
	ruled, rated := snap.rules[g.ID], snap.rates.groups[g.ID]
	models := slices.Concat(gp.models, slices.Collect(maps.Keys(ruled)), slices.Collect(maps.Keys(rated)), snap.rates.segmentModels(g.ID))
	slices.Sort(models)
	models = slices.Compact(models)
	out := &GroupModelPrices{Group: g.ref(), Rate: segment.RateOf(core.RateLevels{Group: g.RateMultiplier}), Models: make([]ModelPriceRow, len(models))}
	for i, model := range models {
		_, offered := slices.BinarySearch(gp.models, model)
		out.Models[i] = ModelPriceRow{Model: model, Offered: offered, Base: snap.bases.base(model),
			Price: ruled[model].view(), Rate: rateOf(rated, model)}
	}
	return out
}

// groupPriceTable is the pricing table of a group from the price snapshot
// requests are billed by on this node.
func (s *Service) groupPriceTable(ctx context.Context, groupID int64) (*GroupModelPrices, error) {
	snap, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	gp, err := loadGroupPrices(ctx, s.db.Gorm, snap, "g.id = ?", groupID)
	if err != nil {
		return nil, err
	}
	return gp.table(snap), nil
}

// GET /groups/:id/model-prices
func (s *Service) showGroupPrices(c *gin.Context) {
	groupID, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	out, err := s.groupPriceTable(c.Request.Context(), groupID)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, out)
}

// ruleChange is a checked rulePatch: the rule to store on on, nil to remove.
type ruleChange struct {
	on   core.PriceQuery
	rule *storedRule
}

// applyChanges writes the changes in tx (writeRules). They go in the order
// of what they are on, whatever order the request gave them in: two
// requests that change the same rules then save them in the same order.
func applyChanges(ctx context.Context, tx *gorm.DB, changes []ruleChange, updatedBy int64) error {
	if len(changes) == 0 {
		return nil
	}
	slices.SortStableFunc(changes, func(a, b ruleChange) int {
		return cmp.Or(cmp.Compare(a.on.GroupID, b.on.GroupID), cmp.Compare(a.on.Model, b.on.Model))
	})
	w, err := collapseChanges(changes, updatedBy)
	if err != nil {
		return err
	}
	return writeRules(ctx, tx, w)
}

// collapseChanges turns sorted changes into writeRules' writes by
// updatedBy, one per rule, leaving what the changes of the rule (adjacent
// once sorted) left one after the other: the rule removed when any of them
// removes it, saved as the last one when that saves it (removed then saved:
// a new row), and the expression of every custom price in the history.
func collapseChanges(changes []ruleChange, updatedBy int64) (*ruleWrites, error) {
	w := &ruleWrites{}
	for len(changes) > 0 {
		n := 1
		for n < len(changes) && changes[n].on == changes[0].on {
			n++
		}
		removed := false
		for _, ch := range changes[:n] {
			if ch.rule == nil {
				removed = true
			} else if err := w.addHistory(ch.rule); err != nil {
				return nil, err
			}
		}
		last := changes[n-1]
		w.addRule(last.on, removed, last.rule, updatedBy)
		changes = changes[n:]
	}
	return w, nil
}
