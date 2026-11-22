package billing

import (
	"context"
	"encoding/json"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// The lists of POST /groups and PATCH /groups/:id that billing writes
// (CONTRACTS §97): the group's model prices and model rates.
const (
	modelPricesField = "model_prices"
	modelRatesField  = "model_rates"
)

var _ core.GroupWritePart = (*Service)(nil)

// rulePatch is an item of model_prices: a null rule removes the one there.
type rulePatch struct {
	Model string `json:"model"`
	Rule  *Rule  `json:"rule"`
}

// ratePatch is an item of model_rates: a null or missing rate removes the
// one there.
type ratePatch struct {
	Model string         `json:"model"`
	Rate  core.RateInput `json:"rate"`
}

// groupPricing is a checked write of a group's model prices and rates,
// against the global prices.
type groupPricing struct {
	prices catalog
	rules  []ruleChange
	rates  rateChanges
	// fields are the problems found, each naming its item.
	fields []core.FieldError
}

// PrepareGroupWrite implements core.GroupWritePart for model_prices and
// model_rates. Removing a price or a rate always can; setting one takes a
// model with a global price, whether the group provides it or not.
func (s *Service) PrepareGroupWrite(ctx context.Context, _ int64, body map[string]json.RawMessage) (core.GroupWrite, []core.FieldError, error) {
	rawPrices, hasPrices := body[modelPricesField]
	rawRates, hasRates := body[modelRatesField]
	if !hasPrices && !hasRates {
		return core.GroupWrite{}, nil, nil
	}
	prices, err := loadCatalog(ctx, s.db.Gorm)
	if err != nil {
		return core.GroupWrite{}, nil, err
	}
	w := &groupPricing{prices: prices, rates: rateChanges{}}
	if hasPrices {
		w.checkPrices(ctx, rawPrices, s.factsFor())
	}
	if hasRates {
		w.checkRates(ctx, rawRates)
	}
	if len(w.fields) > 0 {
		return core.GroupWrite{}, w.fields, nil
	}
	return core.GroupWrite{Apply: w.apply, AfterCommit: func(ctx context.Context, _ int64) { s.changed(ctx, "price_rules") }}, nil, nil
}

// apply writes the prices and rates as the group's in tx.
func (w *groupPricing) apply(ctx context.Context, tx *gorm.DB, groupID int64) error {
	uid, _ := core.UserID(ctx)
	for i := range w.rules {
		w.rules[i].on.GroupID = groupID
	}
	if err := applyChanges(ctx, tx, w.rules, uid); err != nil {
		return err
	}
	return w.rates.write(ctx, tx, groupID, uid)
}

// checkPrices reads and checks the items of model_prices.
func (w *groupPricing) checkPrices(ctx context.Context, raw json.RawMessage, facts map[string]bool) {
	items, fe := core.DecodeList[rulePatch](ctx, raw, modelPricesField, "model prices", "模型价格")
	w.fields = append(w.fields, fe...)
	for i, item := range items {
		change := ruleChange{on: core.PriceQuery{Model: item.Model}}
		if item.Rule != nil && w.priced(ctx, modelPricesField, i, item.Model) {
			rule, err := checkRule(ctx, *item.Rule, facts)
			if err != nil {
				w.fields = append(w.fields, core.FieldError{Field: core.ItemField(modelPricesField, i, "rule"), Code: "invalid", Message: err.Error()})
			}
			change.rule = rule
		}
		w.rules = append(w.rules, change)
	}
}

// checkRates reads and checks the items of model_rates; a later item on a
// model replaces an earlier one.
func (w *groupPricing) checkRates(ctx context.Context, raw json.RawMessage) {
	items, fe := core.DecodeList[ratePatch](ctx, raw, modelRatesField, "model rates", "模型倍率")
	w.fields = append(w.fields, fe...)
	for i, item := range items {
		if item.Rate.Value != nil && w.priced(ctx, modelRatesField, i, item.Model) {
			w.fields = append(w.fields, item.Rate.Errors(ctx, core.ItemField(modelRatesField, i, "rate"))...)
		}
		w.rates[item.Model] = item.Rate.Value
	}
}

// priced reports whether model has a global price, or fails item i of list.
func (w *groupPricing) priced(ctx context.Context, list string, i int, model string) bool {
	if w.prices.base(model) != nil {
		return true
	}
	w.fields = append(w.fields, core.NewFieldError(ctx, core.ItemField(list, i, "model"), "invalid", "the model has no global price", "该模型没有全局价格"))
	return false
}
