package billing

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// ExpressionProblem is a stored expression the engine does not compile.
type ExpressionProblem struct {
	Table string // model_prices, price_rules or model_price_history
	Ref   string // the model, "group/segment/model" or the hash
	Error string
}

// CheckStoredExpressions compiles every expression of model_prices,
// price_rules and model_price_history, rewritten as core migration 0121
// rewrites it (expr.LegacyProduct) when that has not run yet, and returns
// those that fail. It only reads, so it runs before an upgrade against the
// old schema as well as after it (`sub2api admin check-prices`, CONTRACTS
// §131.8).
func CheckStoredExpressions(ctx context.Context, db *gorm.DB) ([]ExpressionProblem, error) {
	var prices []model.ModelPrice
	var rules []model.PriceRule
	var history []model.ModelPriceHistory
	g := store.G(ctx, db)
	if err := g.Select("model", "expression").Find(&prices).Error; err != nil {
		return nil, err
	}
	if err := g.Select("group_id", "segment_id", "model", "expression").Where("kind = ?", core.PriceRuleCustom).Find(&rules).Error; err != nil {
		return nil, err
	}
	if err := g.Select("expr_hash", "expression").Find(&history).Error; err != nil {
		return nil, err
	}
	var out []ExpressionProblem
	check := func(table, ref, src string) {
		if product, ok := expr.LegacyProduct(src); ok {
			src = product
		}
		if _, err := expr.Compile(src); err != nil {
			out = append(out, ExpressionProblem{Table: table, Ref: ref, Error: err.Error()})
		}
	}
	for _, p := range prices {
		check("model_prices", p.Model, p.Expression)
	}
	for _, r := range rules {
		segment := "-"
		if r.SegmentID != nil {
			segment = fmt.Sprint(*r.SegmentID)
		}
		check("price_rules", fmt.Sprintf("%d/%s/%s", r.GroupID, segment, r.Model), r.Expression)
	}
	for _, h := range history {
		check("model_price_history", h.ExprHash, h.Expression)
	}
	return out, nil
}
