package billing

import (
	"context"
	"encoding/json"
	"slices"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// checked is a validated expression ready to store.
type checked struct {
	mode   string
	config json.RawMessage
	src    string
	prog   *expr.Program
}

// check builds and validates the expression for a price. Validation errors
// and an unconfirmed big-cost warning are returned as invalid_argument with
// details {errors, warnings, confirmation_required}.
// q reads the settings: the caller's transaction, or the pool.
func (s *Service) check(ctx context.Context, q *gorm.DB, mode string, config json.RawMessage, expression string, confirm bool) (*checked, error) {
	config = priceConfig(config)
	if !json.Valid(config) {
		return nil, core.InvalidFields(core.FieldError{Field: "config", Code: "invalid", Message: "must be a JSON object"})
	}
	st, _ := s.settingsIn(ctx, q)
	rep, err := checkPriceDef(mode, config, expression, expr.ValidateOptions{Facts: s.factsFor(), BigCostUSD: st.BigCostWarningUSD})
	if err != nil {
		return nil, priceDefError(err)
	}
	if err := reportFailure(rep, confirm); err != nil {
		return nil, err
	}
	prog, err := expr.CompileCached(rep.Expression)
	if err != nil {
		return nil, err
	}
	return &checked{mode: mode, config: config, src: rep.Expression, prog: prog}, nil
}

// priceDefError is the invalid_argument of a price checkPriceDef refuses.
func priceDefError(err error) error {
	ce, ok := err.(*expr.ConfigError)
	if !ok {
		return core.InvalidFields(core.FieldError{Field: "mode", Code: "invalid", Message: err.Error()})
	}
	return core.ErrInvalidArgument.WithMessage(ce.Error()).WithDetails(map[string]any{
		"fields": []core.FieldError{{Field: ce.Field, Code: ce.Issue.Code, Message: ce.Issue.Detail}},
		"errors": []expr.Issue{ce.Issue},
	})
}

// reportFailure refuses an invalid expression and, unless confirmed, one with
// a big-cost warning.
func reportFailure(rep *expr.Report, confirm bool) error {
	if !rep.OK {
		return core.ErrInvalidArgument.WithMessage("invalid price expression").WithDetails(map[string]any{
			"expression": rep.Expression, "errors": rep.Errors, "warnings": rep.Warnings,
		})
	}
	if confirm || !slices.ContainsFunc(rep.Warnings, func(w expr.Issue) bool { return w.Code == expr.CodeBigCost }) {
		return nil
	}
	return core.ErrInvalidArgument.WithMessage("confirmation required").WithDetails(map[string]any{
		"expression": rep.Expression, "errors": []expr.Issue{}, "warnings": rep.Warnings, "confirmation_required": true,
	})
}

// priceConfig is the config a price is checked with: {} when there is none,
// as an expression price has none.
func priceConfig(config json.RawMessage) json.RawMessage {
	if len(config) == 0 || string(config) == "null" {
		return json.RawMessage("{}")
	}
	return config
}

// checkPriceDef is the check a global price and a custom price share: it
// validates the expression the price stands for (expressionFor) as typed, so
// issue positions match the editor; the price stores Report.Expression, the
// text trimmed. The error is a config the generator refuses
// (*expr.ConfigError) or an unknown mode.
func checkPriceDef(mode string, config json.RawMessage, expression string, opts expr.ValidateOptions) (*expr.Report, error) {
	raw, err := expressionFor(mode, config, expression)
	if err != nil {
		return nil, err
	}
	return expr.Validate(raw, opts), nil
}

// expressionFor returns the expression stored for a price: generated from
// the config, or the hand-written expression of an expression price, whose
// config must be {} (an empty expression fails validation).
func expressionFor(mode string, config json.RawMessage, expression string) (string, error) {
	src, err := expr.Generate(mode, config)
	if err == nil && mode == expr.ModeExpression {
		return expression, nil
	}
	return src, err
}

// recordHistory keeps expressions (hash, canonical text, version) on q
// where a settlement retry reads them back, in one INSERT in their order.
func recordHistory(ctx context.Context, q *gorm.DB, progs ...*expr.Program) error {
	if len(progs) == 0 {
		return nil
	}
	rows := make([]model.ModelPriceHistory, len(progs))
	for i, p := range progs {
		rows[i] = model.ModelPriceHistory{ExprHash: p.Hash(), Expression: p.Expression(), ExprVersion: p.Version()}
	}
	return store.G(ctx, q).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "expr_hash"}}, DoNothing: true}).
		Create(&rows).Error
}
