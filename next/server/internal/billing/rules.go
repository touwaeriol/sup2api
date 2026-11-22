package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// Rule is a model price of a group as the API reads and writes it (CONTRACTS
// §97): a price of its own, as a global price is written (visual or
// expression), whatever the global price is. Expression is read of an
// expression price only; of a visual one it is generated from Config, and it
// is always there on output. The API has no kind: a model price is only
// ever a price of its own (price_rules.kind keeps the column default
// "custom").
type Rule struct {
	Mode       string          `json:"mode"`
	Config     json.RawMessage `json:"config,omitempty"`
	Expression string          `json:"expression,omitempty"`
}

// storedRule is a price_rules row: the rule and what its expression tells
// (priceTraits).
type storedRule struct {
	Rule
	exprHash    string
	exprVersion int
	traits      priceTraits
}

// view returns the rule as the API shows it (nil = the model has none).
func (r *storedRule) view() *Rule {
	if r == nil {
		return nil
	}
	out := r.Rule
	return &out
}

// price returns the list price the rule makes of the global one: its own
// expression, and what it tells, with the global one kept as Base. ID is
// always the global price's. A nil rule is the global price as it is, its
// Source left empty (core.PriceRule.Source).
func (r *storedRule) price(global core.PriceRule) *core.PriceRule {
	if r == nil {
		return &global
	}
	global.Source = core.PriceSourceGroup
	global.Base = &core.PriceBase{Expression: global.Expression, ExprHash: global.ExprHash}
	global.Mode, global.Expression, global.ExprHash, global.ExprVersion = r.Mode, r.Expression, r.exprHash, r.exprVersion
	r.traits.apply(&global)
	return &global
}

// source is where the list price comes from under the rule (nil = none).
func (r *storedRule) source() string {
	if r == nil {
		return core.PriceSourceGlobal
	}
	return core.PriceSourceGroup
}

// ruleSet is the price rules of groups, by group id and then model.
type ruleSet map[int64]map[string]*storedRule

// of returns the rule on the model of q in its group; nil without one.
func (rs ruleSet) of(q core.PriceQuery) *storedRule { return rs[q.GroupID][q.Model] }

// loadRules reads every price rule.
func loadRules(ctx context.Context, q *gorm.DB) (ruleSet, error) {
	var rows []model.PriceRule
	if err := store.G(ctx, q).Select("group_id", "model", "mode", "config", "expression", "expr_hash").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := ruleSet{}
	for _, row := range rows {
		r := &storedRule{Rule: Rule{Mode: row.Mode, Config: row.Config, Expression: row.Expression}, exprHash: row.ExprHash}
		if prog, err := expr.CompileCached(r.Expression); err == nil {
			r.exprVersion, r.traits = prog.Version(), traitsOf(r.Mode, prog)
		}
		if out[row.GroupID] == nil {
			out[row.GroupID] = map[string]*storedRule{}
		}
		out[row.GroupID][row.Model] = r
	}
	return out, nil
}

// checkRule validates a model price as a global price is validated (visual
// or expression, any shape) and generates its expression; the global price
// does not matter, the rule replaces it. facts are the usage facts an
// expression may read (Service.factsFor). The error says why it cannot be
// saved.
func checkRule(ctx context.Context, in Rule, facts map[string]bool) (*storedRule, error) {
	if in.Mode != expr.ModeVisual && in.Mode != expr.ModeExpression {
		return nil, errors.New(core.Text(ctx, "mode must be visual or expression", "mode 必须为可视化（visual）或表达式（expression）"))
	}
	config := priceConfig(in.Config)
	rep, err := checkPriceDef(in.Mode, config, in.Expression, expr.ValidateOptions{Facts: facts})
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
	r := &storedRule{Rule: Rule{Mode: in.Mode, Config: config, Expression: rep.Expression},
		exprHash: prog.Hash(), exprVersion: prog.Version(), traits: traitsOf(in.Mode, prog)}
	return r, nil
}

// ruleWrites are the writes of rule changes (collapseChanges), one per
// changed rule, in the order of the changes: the expressions for the
// history, the key of every changed rule and of those removed (a delete
// came before the last change or is the last one), and the rows of those
// saved (the last change saves). A key is what ruleKeys matches: group and
// model.
type ruleWrites struct {
	history          []*expr.Program
	changed, removed []any
	saved            []ruleRecord
}

// ruleRecord is the columns of price_rules a saved rule writes: id and
// updated_at take their defaults, and nothing is read back.
type ruleRecord struct {
	GroupID    int64
	Model      string
	Mode       string
	Config     store.Bytes
	Expression string
	ExprHash   string
	UpdatedBy  *int64
}

func (ruleRecord) TableName() string { return model.PriceRule{}.TableName() }

// addHistory adds the expression of r to the history.
func (w *ruleWrites) addHistory(r *storedRule) error {
	prog, err := expr.CompileCached(r.Expression)
	if err != nil {
		return err
	}
	w.history = append(w.history, prog)
	return nil
}

// addRule adds the rule on on: removed first when removed, then saved as r
// by updatedBy when r is not nil.
func (w *ruleWrites) addRule(on core.PriceQuery, removed bool, r *storedRule, updatedBy int64) {
	key := []any{on.GroupID, on.Model}
	w.changed = append(w.changed, key)
	if removed {
		w.removed = append(w.removed, key)
	}
	if r == nil {
		return
	}
	w.saved = append(w.saved, ruleRecord{GroupID: on.GroupID, Model: on.Model, Mode: r.Mode, Config: store.Bytes(r.Config),
		Expression: r.Expression, ExprHash: r.exprHash, UpdatedBy: nullID(updatedBy)})
}

// ruleKeys matches the rules of keys (ruleWrites).
func ruleKeys(keys []any) clause.IN {
	return clause.IN{Column: []clause.Column{{Name: "group_id"}, {Name: "model"}}, Values: keys}
}

// groupModelKeys are the unique columns of a row of a group's model.
var groupModelKeys = []string{"group_id", "model"}

// writeRules writes w in tx, as the statements of each change did one after
// the other: a rule removed is deleted where it is, a rule saved is upserted
// on its group and model, and the expression of a custom price also goes to
// model_price_history, as a global price's does: a settlement retry reads it
// back by expr_hash on any node. Deleting and then inserting a rule leaves a
// new row.
//
// The history goes in the order of its hashes, and the existing rows of
// every changed rule are locked FOR UPDATE in the order of their keys
// before anything is deleted or saved; the saved rows go in the order of
// the changes (that of applyChanges). Two requests that change the same
// rules then lock them and insert the same new ones in one order, and
// neither waits for the other in a circle.
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
	if err := g.Model(&model.PriceRule{}).Where(ruleKeys(w.changed)).Order("group_id, model").
		Clauses(store.ForUpdate()).Pluck("id", &locked).Error; err != nil {
		return err
	}
	if len(w.removed) > 0 {
		if err := g.Where(ruleKeys(w.removed)).Delete(&model.PriceRule{}).Error; err != nil {
			return err
		}
	}
	if len(w.saved) == 0 {
		return nil
	}
	return g.Clauses(store.UpsertOn(groupModelKeys, "mode", "config", "expression", "expr_hash", "updated_by")).Create(&w.saved).Error
}
