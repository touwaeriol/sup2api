package billing

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/shopspring/decimal"
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

// ratePatch is an item of model_rates: a null rate removes the one there.
type ratePatch struct {
	Model string  `json:"model"`
	Rate  *string `json:"rate"`
}

// groupPricing is a checked write of a group's model prices and rates.
type groupPricing struct {
	rules []ruleChange
	rates rateChanges
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
	w := &groupPricing{rates: rateChanges{}}
	if hasPrices {
		w.checkPrices(ctx, rawPrices, prices, s.factsFor())
	}
	if hasRates {
		w.checkRates(ctx, rawRates, prices)
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
func (w *groupPricing) checkPrices(ctx context.Context, raw json.RawMessage, prices catalog, facts map[string]bool) {
	var items []rulePatch
	if err := json.Unmarshal(raw, &items); err != nil {
		w.fail(ctx, modelPricesField, "must be a list of model prices", "必须为模型价格的列表")
		return
	}
	for i, item := range items {
		change := ruleChange{on: core.PriceQuery{Model: item.Model}}
		if item.Rule != nil && w.priced(ctx, modelPricesField, i, item.Model, prices) {
			rule, err := checkRule(ctx, *item.Rule, facts)
			if err != nil {
				w.fields = append(w.fields, itemError(modelPricesField, i, "rule", err.Error()))
			}
			change.rule = rule
		}
		w.rules = append(w.rules, change)
	}
}

// checkRates reads and checks the items of model_rates; a later item on a
// model replaces an earlier one.
func (w *groupPricing) checkRates(ctx context.Context, raw json.RawMessage, prices catalog) {
	var items []ratePatch
	if err := json.Unmarshal(raw, &items); err != nil {
		w.fail(ctx, modelRatesField, "must be a list of model rates", "必须为模型倍率的列表")
		return
	}
	for i, item := range items {
		var rate *decimal.Decimal
		if item.Rate != nil && w.priced(ctx, modelRatesField, i, item.Model, prices) {
			r, ok := core.ParseRate(*item.Rate)
			if !ok {
				w.fields = append(w.fields, itemError(modelRatesField, i, "rate", core.Text(ctx,
					"must be 0 or more and below 1000000, with at most 4 decimals", "必须不小于 0、小于 1000000，最多 4 位小数")))
			}
			rate = &r
		}
		w.rates[item.Model] = rate
	}
}

// priced reports whether model has a global price, or fails item i of list.
func (w *groupPricing) priced(ctx context.Context, list string, i int, model string, prices catalog) bool {
	if prices.base(model) != nil {
		return true
	}
	w.fields = append(w.fields, itemError(list, i, "model", core.Text(ctx, "the model has no global price", "该模型没有全局价格")))
	return false
}

// fail adds a field error on the list itself.
func (w *groupPricing) fail(ctx context.Context, list, en, zh string) {
	w.fields = append(w.fields, core.FieldError{Field: list, Code: "invalid", Message: core.Text(ctx, en, zh)})
}

// itemError is the field error on field of item i of list.
func itemError(list string, i int, field, message string) core.FieldError {
	return core.FieldError{Field: list + "." + strconv.Itoa(i) + "." + field, Code: "invalid", Message: message}
}
