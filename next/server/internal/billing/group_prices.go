package billing

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"strconv"

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
	r.Perm("PUT", "/groups/:id/model-prices", "group:manage", s.saveGroupPrices)
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

func loadCatalog(ctx context.Context, q *gorm.DB) (catalog, error) {
	var rows []model.ModelPrice
	if err := enabledPrices(store.G(ctx, q), "model", "mode", "config", "expression").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(catalog, len(rows))
	for _, r := range rows {
		out[r.Model] = PriceDef{Mode: r.Mode, Config: r.Config, Expression: r.Expression}
	}
	return out, nil
}

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
// (group.OfferedModels), by group id.
func offeredModels(ctx context.Context, q *gorm.DB, groups []pricedGroup, prices catalog) (map[int64][]string, error) {
	infos := make([]core.GroupInfo, len(groups))
	for i, g := range groups {
		infos[i] = g.GroupInfo
	}
	return group.OfferedModels(ctx, q, infos, prices.models())
}

// groupPrices is a group with the models it provides (sorted), worked out
// over the models that have a global price.
type groupPrices struct {
	group  pricedGroup
	models []string
}

// loadGroupPrices reads the one group cond selects and the models it
// provides at prices; not_found without one.
func loadGroupPrices(ctx context.Context, q *gorm.DB, prices catalog, cond string, args ...any) (*groupPrices, error) {
	g, err := loadGroup(ctx, q, cond, args...)
	if err != nil {
		return nil, err
	}
	offered, err := offeredModels(ctx, q, []pricedGroup{g}, prices)
	if err != nil {
		return nil, err
	}
	return &groupPrices{group: g, models: offered[g.ID]}, nil
}

// each calls add with the global price of each of models that has one and
// the rule of the group on it (nil = none). It is the one place the list
// prices of a group's models are read; the caller says which models and
// what to make of each.
func (snap *priceSnapshot) each(groupID int64, models []string, add func(model string, base PriceDef, rule *storedRule)) {
	for _, model := range models {
		if base, ok := snap.bases[model]; ok {
			add(model, base, snap.rules.of(core.PriceQuery{Model: model, GroupID: groupID}))
		}
	}
}

// ModelPriceRow is a model of a group's pricing table: one the group
// provides or has a rule on.
type ModelPriceRow struct {
	Model string `json:"model"`
	// Offered is false on a model the group has a rule on but does not
	// provide now (Models.Offered): no request reaches it there yet.
	Offered bool `json:"offered"`
	// Base is the global price; null = the model has none and cannot be billed.
	Base      *PriceDef `json:"base"`
	GroupRule *Rule     `json:"group_rule"`
	// ListPrice is the price after the rule; null without a global price.
	ListPrice *PriceDef `json:"list_price"`
	Source    string    `json:"source"`
}

// tableRow prices model under the group's rule (nil = none): its global
// price (nil = none), the rule as configured and the list price after it.
// Without a global price no rule applies.
func tableRow(model string, base *PriceDef, rule *storedRule) ModelPriceRow {
	row := ModelPriceRow{Model: model, Base: base, GroupRule: rule.view(), Source: core.PriceSourceGlobal}
	if base == nil {
		return row
	}
	price := listPrice(*base, rule)
	row.ListPrice, row.Source = &price, rule.source()
	return row
}

// GroupModelPrices is the response of GET and PUT /groups/:id/model-prices.
type GroupModelPrices struct {
	Group core.GroupRef `json:"group"`
	// Rate is the group's default rate.
	Rate   segment.EffectiveRate `json:"rate"`
	Models []ModelPriceRow       `json:"models"`
}

// table is the pricing table of the group under rules: a row for every
// model it provides or has a rule on, by model.
func (gp *groupPrices) table(prices catalog, rules ruleSet) *GroupModelPrices {
	g, one := gp.group, decimal.NewFromInt(1)
	rate, source := core.SegmentRate(g.RateMultiplier, one, nil)
	out := &GroupModelPrices{Group: g.ref(), Rate: segment.EffectiveRate{Value: rate, Source: source, GroupDefault: g.RateMultiplier, Discount: one}}
	ruled := rules[g.ID]
	models := append(slices.Clone(gp.models), slices.Collect(maps.Keys(ruled))...)
	slices.Sort(models)
	models = slices.Compact(models)
	out.Models = make([]ModelPriceRow, len(models))
	for i, model := range models {
		out.Models[i] = tableRow(model, prices.base(model), ruled[model])
		_, out.Models[i].Offered = slices.BinarySearch(gp.models, model)
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
	gp, err := loadGroupPrices(ctx, s.db.Gorm, snap.bases, "g.id = ?", groupID)
	if err != nil {
		return nil, err
	}
	return gp.table(snap.bases, snap.rules), nil
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

// rulePatch is an item of PUT /groups/:id/model-prices: a null rule removes
// the one there.
type rulePatch struct {
	Model string `json:"model"`
	Rule  *Rule  `json:"rule"`
}

// ruleChange is a checked rulePatch: the rule to store on on, nil to remove.
type ruleChange struct {
	on   core.PriceQuery
	rule *storedRule
}

// checkPatches validates the items of a PUT on the group, all or none,
// over the global prices: an item that cannot be saved is a field error on
// items.<index>. Removing a rule always can; a rule needs a global price of
// its model, whether the group provides the model or not. facts are the
// usage facts an expression may read.
func checkPatches(ctx context.Context, groupID int64, prices catalog, items []rulePatch, facts map[string]bool) ([]ruleChange, error) {
	changes := make([]ruleChange, len(items))
	for i, item := range items {
		changes[i].on = core.PriceQuery{Model: item.Model, GroupID: groupID}
		if item.Rule == nil {
			continue
		}
		rule, err := checkRule(ctx, *item.Rule, prices.base(item.Model), facts)
		if err != nil {
			return nil, core.InvalidFields(core.FieldError{Field: "items." + strconv.Itoa(i), Code: "invalid", Message: err.Error()})
		}
		changes[i].rule = rule
	}
	return changes, nil
}

// PUT /groups/:id/model-prices {items: [{model, rule}]}
func (s *Service) saveGroupPrices(c *gin.Context) {
	ctx := c.Request.Context()
	groupID, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	// User groups no longer price models: a segment_id of the old console
	// must fail, not price the whole group.
	if _, set := c.GetQuery("segment_id"); set {
		httpapi.Fail(c, core.ErrInvalidArgument.WithMessage(core.Text(c.Request.Context(), "segment_id is not accepted: user groups set rates only", "不接受 segment_id：用户组只能设倍率")))
		return
	}
	var in struct {
		Items []rulePatch `json:"items"`
	}
	if !httpapi.BindStrictJSON(c, &in) {
		return
	}
	if err := s.writeGroupPrices(ctx, groupID, in.Items); err != nil {
		httpapi.Fail(c, err)
		return
	}
	// Dropped here before the table is read: it shows what was saved.
	s.changed(ctx, "price_rules")
	out, err := s.groupPriceTable(ctx, groupID)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, out)
}

// writeGroupPrices checks the items against the global prices and saves
// them as the rules of the group, all or none, in one transaction.
func (s *Service) writeGroupPrices(ctx context.Context, groupID int64, items []rulePatch) error {
	uid, _ := core.UserID(ctx)
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		if _, err := loadGroup(ctx, tx, "g.id = ?", groupID); err != nil {
			return err
		}
		prices, err := loadCatalog(ctx, tx)
		if err != nil {
			return err
		}
		changes, err := checkPatches(ctx, groupID, prices, items, s.factsFor())
		if err != nil {
			return err
		}
		return applyChanges(ctx, tx, changes, uid)
	})
	// The group went away while its rules were stored.
	if store.IsForeignKeyViolation(err) {
		return core.ErrConflict.WithMessage(core.Text(ctx, "the data changed meanwhile, try again", "数据已被其他操作修改，请重试")).WithCause(err)
	}
	return err
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
