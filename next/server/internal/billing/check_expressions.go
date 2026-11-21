package billing

import (
	"context"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// ExpressionProblem is a stored expression the engine does not compile, or
// (after migration 0121) a config that does not generate its expression.
type ExpressionProblem struct {
	Table string // model_prices, price_rules or model_price_history
	Ref   string // the model, "group/model" or the hash
	Error string
}

// CheckStoredExpressions compiles every expression of model_prices,
// price_rules and model_price_history, rewritten as core migration 0121
// rewrites it (expr.LegacyProduct) when that has not run yet, and returns
// those that fail. After 0121 it also generates the expression of every
// visual price (per_request and per_token before 0122, converted as that
// migration converts them) from its config and names those whose
// config fails or does not generate the stored expression (expr_hash). It
// only reads, so it runs before an upgrade against the old schema as well
// as after it (`sub2api admin check-prices`, CONTRACTS §131.8).
func CheckStoredExpressions(ctx context.Context, db *gorm.DB) ([]ExpressionProblem, error) {
	var prices []model.ModelPrice
	var rules []model.PriceRule
	var history []model.ModelPriceHistory
	g := store.G(ctx, db)
	if err := g.Select("model", "mode", "config", "expression", "expr_hash").Find(&prices).Error; err != nil {
		return nil, err
	}
	if err := g.Select("group_id", "model", "mode", "config", "expression", "expr_hash").Where("kind = ?", core.PriceRuleCustom).Find(&rules).Error; err != nil {
		return nil, err
	}
	if err := g.Select("expr_hash", "expression").Find(&history).Error; err != nil {
		return nil, err
	}
	legacy, err := beforeBillingModes(ctx, db)
	if err != nil {
		return nil, err
	}
	var out []ExpressionProblem
	fail := func(table, ref, msg string) { out = append(out, ExpressionProblem{Table: table, Ref: ref, Error: msg}) }
	check := func(table, ref, src string) {
		if product, ok := expr.LegacyProduct(src); ok && legacy {
			src = product
		}
		if _, err := expr.Compile(src); err != nil {
			fail(table, ref, err.Error())
		}
	}
	// generated: the config of a visual price generates the stored
	// expression; before 0121 configs are in the old shape, before 0122 they
	// are read as that migration converts them.
	generated := func(table, ref, mode string, config json.RawMessage, hash string) {
		if legacy {
			return
		}
		if cfg, ok := visualFromLegacy(mode, config); ok {
			mode, config = expr.ModeVisual, cfg
		}
		if mode != expr.ModeVisual {
			return
		}
		if gen, err := expr.Generate(mode, config); err != nil {
			fail(table, ref, "config: "+err.Error())
		} else if expr.Hash(gen) != hash {
			fail(table, ref, "the config does not generate the stored expression")
		}
	}
	for _, p := range prices {
		check("model_prices", p.Model, p.Expression)
		generated("model_prices", p.Model, p.Mode, p.Config, p.ExprHash)
	}
	for _, r := range rules {
		ref := fmt.Sprintf("%d/%s", r.GroupID, r.Model)
		check("price_rules", ref, r.Expression)
		if r.Mode != nil {
			generated("price_rules", ref, *r.Mode, r.Config, r.ExprHash)
		}
	}
	for _, h := range history {
		check("model_price_history", h.ExprHash, h.Expression)
	}
	return out, nil
}

// billingModesMigration is the core migration that rewrote "|||" rules.
const billingModesMigration = "0121_billing_modes.sql"

// beforeBillingModes reports whether the database has not run core
// migration 0121 yet: its expressions are still in the old rule syntax.
func beforeBillingModes(ctx context.Context, db *gorm.DB) (bool, error) {
	var n int64
	err := store.G(ctx, db).Table("schema_migrations").Where("id = ?", billingModesMigration).Count(&n).Error
	return n == 0, err
}
