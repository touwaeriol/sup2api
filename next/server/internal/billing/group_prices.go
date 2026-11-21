package billing

import (
	"cmp"
	"context"
	"errors"
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
	r.Perm("GET", "/groups/:id/model-prices", "group:read", s.showGroupPrices)
	r.Perm("PUT", "/groups/:id/model-prices", "group:manage", s.saveGroupPrices)
	r.Perm("POST", "/pricing/quote", "price:read", s.quotePricing).Uses(perm.GroupRead, perm.UserRead, perm.SegmentRead)
}

// pricedGroup is what pricing reads of a group.
type pricedGroup struct {
	core.GroupInfo
	public bool
}

func (g pricedGroup) ref() core.GroupRef { return core.GroupRef{ID: g.ID, Name: g.Name} }

// pricedGroupRow is a row of pricedGroups.
type pricedGroupRow struct {
	ID              int64
	Name            string
	RateMultiplier  decimal.Decimal
	ModelAllowlist  store.JSON[[]string]
	ModelFilterMode string
	Public          bool
}

// loadGroups reads the groups (alias g) cond selects, by id.
func loadGroups(ctx context.Context, q *gorm.DB, cond string, args ...any) ([]pricedGroup, error) {
	var rows []pricedGroupRow
	if err := store.G(ctx, q).Table("groups g").Select(`g.id, g.name, g.rate_multiplier, g.model_allowlist, g.model_filter_mode,
		g.visibility = 'public' AS public`).Where(cond, args...).Order("g.id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]pricedGroup, len(rows))
	for i, r := range rows {
		out[i] = pricedGroup{public: r.Public, GroupInfo: core.GroupInfo{ID: r.ID, Name: r.Name, RateMultiplier: r.RateMultiplier,
			ModelAllowlist: r.ModelAllowlist.V, ModelFilterMode: r.ModelFilterMode}}
	}
	return out, nil
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

// offeredModels returns the models each of the groups provides
// (group.OfferedModels), by group id.
func offeredModels(ctx context.Context, q *gorm.DB, groups []pricedGroup, prices catalog) (map[int64][]string, error) {
	infos := make([]core.GroupInfo, len(groups))
	for i, g := range groups {
		infos[i] = g.GroupInfo
	}
	return group.OfferedModels(ctx, q, infos, prices.models())
}

// groupPrices is what the models of one group are priced against: the group,
// the global prices and the models the group provides.
type groupPrices struct {
	group  pricedGroup
	prices catalog
	models []string
}

// loadGroupPrices reads the one group cond selects; not_found without one.
func loadGroupPrices(ctx context.Context, q *gorm.DB, cond string, args ...any) (*groupPrices, error) {
	groups, err := loadGroups(ctx, q, cond, args...)
	if err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return nil, core.ErrNotFound.WithMessage(core.Text(ctx, "group not found", "分组不存在"))
	}
	gp := &groupPrices{group: groups[0]}
	if gp.prices, err = loadCatalog(ctx, q); err != nil {
		return nil, err
	}
	offered, err := offeredModels(ctx, q, groups, gp.prices)
	if err != nil {
		return nil, err
	}
	gp.models = offered[gp.group.ID]
	return gp, nil
}

// eachPrice calls add with the global price of each of models that has one
// and the rules that price it for segmentID (0 = none) in groupID. It is the
// one place the list prices of a group's models for one segment are read;
// the caller says which models and what to make of each.
func eachPrice(ctx context.Context, q *gorm.DB, prices catalog, models []string, groupID, segmentID int64, add func(model string, base PriceDef, levels ruleLevels)) error {
	on := core.PriceQuery{GroupID: groupID, SegmentID: segmentID}
	rules, err := loadRules(ctx, q, rulesFor, on.GroupID, on.SegmentID, on.SegmentID)
	if err != nil {
		return err
	}
	for _, model := range models {
		base, ok := prices[model]
		if !ok {
			continue
		}
		on.Model = model
		add(model, base, rules.levels(on))
	}
	return nil
}

// ModelPriceRow is one model a group provides, with the rules on it.
type ModelPriceRow struct {
	Model string `json:"model"`
	// Base is the global price; null = the model has none and cannot be billed.
	Base      *PriceDef `json:"base"`
	GroupRule *Rule     `json:"group_rule"`
	// SegmentRule is there, null without a rule, when a segment was asked
	// for; SegmentRuleCount, the segments with a rule of their own on the
	// model, when none was.
	SegmentRule      **Rule `json:"segment_rule,omitempty"`
	SegmentRuleCount *int   `json:"segment_rule_count,omitempty"`
	// ListPrice is the price after the first rule that applies; null without
	// a global price.
	ListPrice *PriceDef `json:"list_price"`
	Source    string    `json:"source"`
}

// row prices model for the segment of the levels: its global price (nil =
// none), the group's rule as configured and the list price after the first
// rule that applies. Without a global price no rule applies. What it says of
// the segments is the caller's to add.
func (l ruleLevels) row(model string, base *PriceDef) ModelPriceRow {
	row := ModelPriceRow{Model: model, Base: base, GroupRule: l.group.view(), Source: core.PriceSourceGlobal}
	if base == nil {
		return row
	}
	price, source := l.listPrice(*base)
	row.ListPrice, row.Source = &price, source
	return row
}

// GroupModelPrices is the response of GET and PUT /groups/:id/model-prices.
type GroupModelPrices struct {
	Group   core.GroupRef    `json:"group"`
	Segment *core.SegmentRef `json:"segment,omitempty"`
	// Rate is the rate of the segment asked for, else the group's default.
	Rate   segment.EffectiveRate `json:"rate"`
	Models []ModelPriceRow       `json:"models"`
}

// scope starts the answer on the group's model prices: the group, the
// segment asked for (0 = none) and the rate that applies.
func (gp *groupPrices) scope(ctx context.Context, q *gorm.DB, segmentID int64, showUsers bool) (*GroupModelPrices, error) {
	g := gp.group
	out := &GroupModelPrices{Group: g.ref()}
	if segmentID == 0 {
		one := decimal.NewFromInt(1)
		rate, source := core.SegmentRate(g.RateMultiplier, one, nil)
		out.Rate = segment.EffectiveRate{Value: rate, Source: source, GroupDefault: g.RateMultiplier, Discount: one}
		return out, nil
	}
	terms, err := segment.SegmentTerms(ctx, q, segmentID, []int64{g.ID})
	if err != nil {
		return nil, err
	}
	t, ok := terms[g.ID]
	if !ok {
		return nil, segment.NotFound(ctx)
	}
	ref, err := segment.Ref(ctx, q, segmentID, showUsers)
	if err != nil {
		return nil, err
	}
	out.Segment, out.Rate = &ref, t.EffectiveRate()
	return out, nil
}

// fill adds a row for every model the group provides, priced for the segment
// of out.
func (gp *groupPrices) fill(ctx context.Context, q *gorm.DB, out *GroupModelPrices) error {
	rules, err := loadRules(ctx, q, "group_id = ?", gp.group.ID)
	if err != nil {
		return err
	}
	ofSegments := map[string]int{}
	for key := range rules {
		if key.SegmentID != 0 {
			ofSegments[key.Model]++
		}
	}
	on := core.PriceQuery{GroupID: gp.group.ID}
	if out.Segment != nil {
		on.SegmentID = out.Segment.ID
	}
	out.Models = make([]ModelPriceRow, 0, len(gp.models))
	for _, model := range gp.models {
		on.Model = model
		base, levels := gp.prices.base(model), rules.levels(on)
		row := levels.row(model, base)
		if out.Segment == nil {
			count := ofSegments[model]
			row.SegmentRuleCount = &count
		} else {
			rule := levels.segment.view()
			row.SegmentRule = &rule
		}
		out.Models = append(out.Models, row)
	}
	return nil
}

// groupPricesQuery reads :id and ?segment_id= (0 = none) of
// /groups/:id/model-prices, or renders invalid_argument.
func groupPricesQuery(c *gin.Context) (groupID, segmentID int64, ok bool) {
	if groupID, ok = httpapi.PathID(c, "id"); !ok {
		return 0, 0, false
	}
	raw := c.Query("segment_id")
	if raw == "" {
		return groupID, 0, true
	}
	segmentID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || segmentID <= 0 {
		httpapi.Fail(c, core.InvalidFields(core.FieldError{Field: "segment_id", Code: "invalid", Message: "must be a positive integer"}))
		return 0, 0, false
	}
	return groupID, segmentID, true
}

// GET /groups/:id/model-prices?segment_id=
func (s *Service) showGroupPrices(c *gin.Context) {
	ctx := c.Request.Context()
	groupID, segmentID, ok := groupPricesQuery(c)
	if !ok {
		return
	}
	showUsers, err := segment.ShowUsers(ctx, s.Authz)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	gp, err := loadGroupPrices(ctx, s.db.Gorm, "g.id = ?", groupID)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	out, err := gp.scope(ctx, s.db.Gorm, segmentID, showUsers)
	if err == nil {
		err = gp.fill(ctx, s.db.Gorm, out)
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, out)
}

// rulePatch is an item of PUT /groups/:id/model-prices: a null rule removes
// the one there; a null segment_id is the group's own rule.
type rulePatch struct {
	SegmentID *int64 `json:"segment_id"`
	Model     string `json:"model"`
	Rule      *Rule  `json:"rule"`
}

// ruleChange is a checked rulePatch: the rule to store on on, nil to remove.
type ruleChange struct {
	on   core.PriceQuery
	rule *storedRule
}

// check validates the items of a PUT, all or none: an item that cannot be
// saved is a field error on items.<index>. Removing a rule always can.
// facts are the usage facts an expression may read.
func (gp *groupPrices) check(ctx context.Context, q *gorm.DB, items []rulePatch, facts map[string]bool) ([]ruleChange, error) {
	segmentIDs := []int64{}
	for _, item := range items {
		segmentIDs = append(segmentIDs, deref(item.SegmentID))
	}
	var known []int64
	if err := store.G(ctx, q).Model(&model.Segment{}).Where("id = ANY(?)", store.Int64s(segmentIDs)).
		Pluck("id", &known).Error; err != nil {
		return nil, err
	}
	changes := make([]ruleChange, 0, len(items))
	for i, item := range items {
		rule, err := gp.checkPatch(ctx, known, item, facts)
		if err != nil {
			return nil, core.InvalidFields(core.FieldError{Field: "items." + strconv.Itoa(i), Code: "invalid", Message: err.Error()})
		}
		on := core.PriceQuery{Model: item.Model, GroupID: gp.group.ID, SegmentID: deref(item.SegmentID)}
		changes = append(changes, ruleChange{on: on, rule: rule})
	}
	return changes, nil
}

// checkPatch returns the rule an item stores, nil to remove the one there.
// The error says why the item cannot be saved; known are the segments that
// exist among those the items name, facts the usage facts an expression may
// read.
func (gp *groupPrices) checkPatch(ctx context.Context, known []int64, item rulePatch, facts map[string]bool) (*storedRule, error) {
	switch {
	case item.SegmentID != nil && !slices.Contains(known, *item.SegmentID):
		return nil, errors.New(segment.UnknownMessage(ctx))
	case item.Rule == nil:
		return nil, nil
	case !slices.Contains(gp.models, item.Model):
		return nil, errors.New(core.Text(ctx, "the group does not provide this model", "该分组不提供此模型"))
	}
	return checkRule(ctx, *item.Rule, gp.prices.base(item.Model), facts)
}

// PUT /groups/:id/model-prices?segment_id= {items: [{segment_id, model, rule}]}
func (s *Service) saveGroupPrices(c *gin.Context) {
	ctx := c.Request.Context()
	groupID, segmentID, ok := groupPricesQuery(c)
	if !ok {
		return
	}
	var in struct {
		Items []rulePatch `json:"items"`
	}
	if !httpapi.BindJSON(c, &in) {
		return
	}
	uid, _ := core.UserID(ctx)
	showUsers, err := segment.ShowUsers(ctx, s.Authz)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	var out *GroupModelPrices
	err = s.db.Tx(ctx, func(tx *gorm.DB) error {
		gp, err := loadGroupPrices(ctx, tx, "g.id = ?", groupID)
		if err != nil {
			return err
		}
		if out, err = gp.scope(ctx, tx, segmentID, showUsers); err != nil {
			return err
		}
		changes, err := gp.check(ctx, tx, in.Items, s.factsFor())
		if err != nil {
			return err
		}
		if err := segment.GuardOwnSegments(ctx, tx, core.GuardOrDeny(s.Users), segmentsOf(changes)); err != nil {
			return err
		}
		if err := applyChanges(ctx, tx, changes, uid); err != nil {
			return err
		}
		return gp.fill(ctx, tx, out)
	})
	// The group or a segment went away while its rules were stored.
	if store.IsForeignKeyViolation(err) {
		err = core.ErrConflict.WithMessage(core.Text(ctx, "the data changed meanwhile, try again", "数据已被其他操作修改，请重试")).WithCause(err)
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.changed(ctx, "price_rules")
	httpapi.OK(c, out)
}

// segmentsOf is the segments the changes give their own prices (0, the
// group's own price, is none).
func segmentsOf(changes []ruleChange) []int64 {
	ids := []int64{}
	for _, c := range changes {
		if c.on.SegmentID != 0 {
			ids = append(ids, c.on.SegmentID)
		}
	}
	return ids
}

// applyChanges writes the changes in tx (writeRules). They go in the order
// of what they are on, whatever order the request gave them in: two
// requests that change the same rules then save them in the same order.
func applyChanges(ctx context.Context, tx *gorm.DB, changes []ruleChange, updatedBy int64) error {
	if len(changes) == 0 {
		return nil
	}
	slices.SortStableFunc(changes, func(a, b ruleChange) int {
		return cmp.Or(cmp.Compare(a.on.GroupID, b.on.GroupID), cmp.Compare(a.on.SegmentID, b.on.SegmentID), cmp.Compare(a.on.Model, b.on.Model))
	})
	w, err := collapseChanges(changes, updatedBy)
	if err != nil {
		return err
	}
	return writeRules(ctx, tx, w)
}

// collapseChanges turns sorted changes into writeRules' writes by
// updatedBy, one per rule, leaving what the changes of the rule (adjacent
// once sorted) left one after the other: the rule removed when any of them removes it, saved
// as the last one when that saves it (removed then saved: a new row), and
// the expression of every custom price in the history.
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
		if err := w.addRule(last.on, removed, last.rule, updatedBy); err != nil {
			return nil, err
		}
		changes = changes[n:]
	}
	return w, nil
}
