package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// Rule is a model price of a group, or of a segment in a group, as the API
// reads and writes it (CONTRACTS §97).
type Rule struct {
	Kind string `json:"kind"`
	// Multiplier (kind multiplier), a decimal: the list price is the global
	// price times it, and follows the global price when that changes.
	Multiplier *string `json:"multiplier,omitempty"`
	// Mode, Config and Expression (kind custom): a price of its own, as a
	// global price is written (visual or expression), whatever the global
	// price is. Expression is read of an expression price only; of a visual
	// one it is generated from Config, and it is always there on output.
	Mode       string          `json:"mode,omitempty"`
	Config     json.RawMessage `json:"config,omitempty"`
	Expression string          `json:"expression,omitempty"`
}

// storedRule is a price_rules row: the rule and, of a custom price, what its
// expression tells (priceTraits).
type storedRule struct {
	Rule
	scale       decimal.Decimal // Multiplier, as a number
	exprHash    string
	exprVersion int
	traits      priceTraits
}

// setScale makes n the multiplier of the rule.
func (r *storedRule) setScale(n decimal.Decimal) {
	text := n.String()
	r.scale, r.Multiplier = n, &text
}

// view returns the rule as the API shows it (nil = the model has none).
func (r *storedRule) view() *Rule {
	if r == nil {
		return nil
	}
	out := r.Rule
	return &out
}

// price returns the list price the rule makes of the global one, with where
// the rule is from: a multiplier leaves the expression and sets Scale; a
// custom price puts its own expression, and what it tells, in and keeps the
// global one as Base. ID is always the global price's. A nil rule is the
// global price as it is, its Source left empty (core.PriceRule.Source).
func (r *storedRule) price(global core.PriceRule, source string) *core.PriceRule {
	if r == nil {
		return &global
	}
	global.Source = source
	if r.Kind == core.PriceRuleMultiplier {
		scale := r.scale
		global.Scale = &scale
		return &global
	}
	global.Base = &core.PriceBase{Expression: global.Expression, ExprHash: global.ExprHash}
	global.Mode, global.Expression, global.ExprHash, global.ExprVersion = r.Mode, r.Expression, r.exprHash, r.exprVersion
	r.traits.apply(&global)
	return &global
}

// ruleLevels are the rules on one model in one group: that of a segment and
// the group's own.
type ruleLevels struct{ segment, group *storedRule }

// applied returns the first rule there is, the segment's before the
// group's, and where the list price then comes from. It is the one place the
// levels are ordered.
func (l ruleLevels) applied() (*storedRule, string) {
	switch {
	case l.segment != nil:
		return l.segment, core.PriceSourceSegment
	case l.group != nil:
		return l.group, core.PriceSourceGroup
	}
	return nil, core.PriceSourceGlobal
}

// ruleSet is price rules by what they are on; SegmentID 0 is the group's own
// rule on the model.
type ruleSet map[core.PriceQuery]*storedRule

// levels returns the rules that may price q.
func (rs ruleSet) levels(q core.PriceQuery) ruleLevels {
	l := ruleLevels{group: rs[core.PriceQuery{Model: q.Model, GroupID: q.GroupID}]}
	if q.SegmentID != 0 {
		l.segment = rs[q]
	}
	return l
}

// rulesFor selects the rules that may price the models of a group for a
// segment: the segment's own and the group's. It takes the group's id, then
// the segment's twice.
const rulesFor = `group_id = ? AND COALESCE(segment_id, ?) = ?`

// ruleRow is a row of priceRules.
type ruleRow struct {
	GroupID    int64
	SegmentID  int64
	Model      string
	Kind       string
	Multiplier *decimal.Decimal
	Mode       string
	Config     json.RawMessage
	Expression string
	ExprHash   string
}

// loadRules reads the price rules cond selects ("" for all of them).
func loadRules(ctx context.Context, q *gorm.DB, cond string, args ...any) (ruleSet, error) {
	db := store.G(ctx, q).Model(&model.PriceRule{}).Select(`group_id, COALESCE(segment_id, 0) AS segment_id, model, kind, multiplier,
		COALESCE(mode, '') AS mode, config, expression, expr_hash`)
	if cond != "" {
		db = db.Where(cond, args...)
	}
	var rows []ruleRow
	if err := db.Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(ruleSet, len(rows))
	for _, row := range rows {
		on := core.PriceQuery{GroupID: row.GroupID, SegmentID: row.SegmentID, Model: row.Model}
		r := &storedRule{Rule: Rule{Kind: row.Kind, Mode: row.Mode, Config: row.Config}, exprHash: row.ExprHash}
		if row.Multiplier != nil {
			r.setScale(*row.Multiplier)
		}
		out[on] = r
		// Only a custom price has an expression of its own.
		if r.Kind != core.PriceRuleCustom {
			continue
		}
		r.Expression = row.Expression
		if prog, err := expr.CompileCached(r.Expression); err == nil {
			r.exprVersion, r.traits = prog.Version(), traitsOf(r.Mode, prog)
		}
	}
	return out, nil
}

// checkRule validates a rule over the global price of its model (nil = the
// model has none) and returns it as it is stored. facts are the usage facts
// an expression may read (Service.factsFor). The error says why it cannot be
// saved.
func checkRule(ctx context.Context, in Rule, base *PriceDef, facts map[string]bool) (*storedRule, error) {
	if base == nil {
		return nil, errors.New(core.Text(ctx, "the model has no global price", "该模型没有全局价格"))
	}
	switch in.Kind {
	case core.PriceRuleMultiplier:
		scale, ok := core.ParseRate(deref(in.Multiplier))
		if !ok {
			return nil, errors.New(core.Text(ctx, "multiplier must be 0 or more and below 1000000, with at most 4 decimals",
				"倍数必须不小于 0、小于 1000000，最多 4 位小数"))
		}
		r := &storedRule{Rule: Rule{Kind: in.Kind}}
		r.setScale(scale)
		return r, nil
	case core.PriceRuleCustom:
		return checkCustom(ctx, in, facts)
	}
	return nil, errors.New(core.Text(ctx, "rule must be a multiplier or a custom price", "规则必须是倍数或自定义价"))
}

// checkCustom validates a custom price as a global price is validated
// (visual or expression, any shape) and generates its expression. The global
// price does not matter: a custom price replaces it.
func checkCustom(ctx context.Context, in Rule, facts map[string]bool) (*storedRule, error) {
	if in.Mode != expr.ModeVisual && in.Mode != expr.ModeExpression {
		return nil, errors.New(core.Text(ctx, "a custom price is visual or expression", "自定义价的计费方式须是可视化或表达式"))
	}
	config := priceConfig(in.Config)
	rep, err := validatePrice(in.Mode, config, in.Expression, expr.ValidateOptions{Facts: facts})
	if err != nil {
		return nil, fmt.Errorf(core.Text(ctx, "invalid custom price: %v", "自定义价无效：%v"), err)
	}
	if !rep.OK {
		e := rep.Errors[0]
		return nil, fmt.Errorf(core.Text(ctx, "invalid custom price: %s", "自定义价无效：%s"), core.Text(ctx, e.Message["en"], e.Message["zh"]))
	}
	prog, err := expr.CompileCached(rep.Expression)
	if err != nil {
		return nil, err
	}
	r := &storedRule{Rule: Rule{Kind: in.Kind, Mode: in.Mode, Config: config, Expression: rep.Expression},
		exprHash: prog.Hash(), exprVersion: prog.Version(), traits: traitsOf(in.Mode, prog)}
	return r, nil
}

// ruleWrites are the writes of rule changes (collapseChanges), one per
// changed rule, in the order of the changes: the expressions for the
// history, the key of every changed rule and of those removed (a delete
// came before the last change or is the last one), and the rows of those
// saved (the last change saves), the group's own rules and the segments'
// apart. A key is what ruleKeys matches: group, segment (0 = the group's
// own) and model.
type ruleWrites struct {
	history          []*expr.Program
	changed, removed []any
	saved            [2][]ruleRecord // the group's own rules, the segments'
}

// ruleRecord is the columns of price_rules a saved rule writes: id and
// updated_at take their defaults, and nothing is read back.
type ruleRecord struct {
	GroupID    int64
	SegmentID  *int64
	Model      string
	Kind       string
	Multiplier *decimal.Decimal
	Mode       *string
	Config     store.Bytes
	Expression string
	ExprHash   string
	UpdatedBy  *int64
}

func (ruleRecord) TableName() string { return model.PriceRule{}.TableName() }

// addHistory adds the expression of a custom price r to the history.
func (w *ruleWrites) addHistory(r *storedRule) error {
	if r.Kind != core.PriceRuleCustom {
		return nil
	}
	prog, err := expr.CompileCached(r.Expression)
	if err != nil {
		return err
	}
	w.history = append(w.history, prog)
	return nil
}

// addRule adds the rule on on: removed first when removed, then saved as r
// by updatedBy when r is not nil.
func (w *ruleWrites) addRule(on core.PriceQuery, removed bool, r *storedRule, updatedBy int64) error {
	key := []any{on.GroupID, on.SegmentID, on.Model}
	w.changed = append(w.changed, key)
	if removed {
		w.removed = append(w.removed, key)
	}
	if r == nil {
		return nil
	}
	row := ruleRecord{GroupID: on.GroupID, SegmentID: nullID(on.SegmentID), Model: on.Model, Kind: r.Kind,
		Expression: r.Expression, ExprHash: r.exprHash, UpdatedBy: nullID(updatedBy)}
	if r.Multiplier != nil {
		row.Multiplier = &r.scale
	}
	if r.Mode != "" {
		row.Mode = &r.Mode
	}
	if r.Config != nil {
		row.Config = store.Bytes(r.Config)
	}
	level := 0
	if on.SegmentID != 0 {
		level = 1
	}
	w.saved[level] = append(w.saved[level], row)
	return nil
}

// ruleKeys matches the rules of keys (ruleWrites).
func ruleKeys(keys []any) clause.IN {
	return clause.IN{Column: []clause.Column{{Name: "group_id"}, {Name: "COALESCE(segment_id, 0)", Raw: true}, {Name: "model"}}, Values: keys}
}

// ruleConflict is the upsert of the saved rules of a level (0 the group's
// own, 1 a segment's) on the partial unique index of that level: the rule
// over the one there, if any, by its user, updated_at moved.
func ruleConflict(level int) clause.OnConflict {
	target, where := []clause.Column{{Name: "group_id"}, {Name: "model"}}, "segment_id IS NULL"
	if level == 1 {
		target, where = []clause.Column{{Name: "group_id"}, {Name: "segment_id"}, {Name: "model"}}, "segment_id IS NOT NULL"
	}
	set := clause.AssignmentColumns([]string{"kind", "multiplier", "mode", "config", "expression", "expr_hash", "updated_by"})
	return clause.OnConflict{Columns: target, TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: where}}},
		DoUpdates: append(set, clause.Assignment{Column: clause.Column{Name: "updated_at"}, Value: clause.Expr{SQL: "now()"}})}
}

// writeRules writes w in tx, as the statements of each change did one after
// the other: a rule removed is deleted where it is, a rule saved is upserted
// on the partial unique index of its level, and the expression of a custom
// price also goes to model_price_history, as a global price's does: a
// settlement retry reads it back by expr_hash on any node. Deleting and then
// inserting a rule leaves a new row.
//
// The history goes in the order of its hashes, and the existing rows of
// every changed rule are locked FOR UPDATE in the order of their keys
// before anything is deleted or saved; the saved rows go in the order of
// the changes (that of applyChanges), the group's own before the
// segments'. Two requests that change the same rules then lock them and
// insert the same new ones in one order, and neither waits for the other
// in a circle.
func writeRules(ctx context.Context, tx *gorm.DB, w *ruleWrites) error {
	if err := store.RequireTx(tx); err != nil {
		return err
	}
	slices.SortStableFunc(w.history, func(a, b *expr.Program) int { return strings.Compare(a.Hash(), b.Hash()) })
	if err := recordHistory(ctx, tx, w.history...); err != nil {
		return err
	}
	g := store.G(ctx, tx)
	var locked []int64
	if err := g.Model(&model.PriceRule{}).Where(ruleKeys(w.changed)).Order("group_id, segment_id NULLS FIRST, model").
		Clauses(store.ForUpdate()).Pluck("id", &locked).Error; err != nil {
		return err
	}
	if len(w.removed) > 0 {
		if err := g.Where(ruleKeys(w.removed)).Delete(&model.PriceRule{}).Error; err != nil {
			return err
		}
	}
	for level, rows := range w.saved {
		if len(rows) == 0 {
			continue
		}
		if err := g.Clauses(ruleConflict(level)).Create(&rows).Error; err != nil {
			return err
		}
	}
	return nil
}
