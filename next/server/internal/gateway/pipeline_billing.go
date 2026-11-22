package gateway

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// priceQuery asks for model's list price in the key's group.
func (c *call) priceQuery(model string) core.PriceQuery {
	return core.PriceQuery{Model: model, GroupID: c.principal.Group.ID}
}

// prepareBilling resolves the model's list price (ARCHITECTURE 7.3) and
// rate, captures the price inputs and checks the balance. Free endpoints
// skip it.
func (c *call) prepareBilling(ctx context.Context) *gwError {
	if strings.EqualFold(c.ep.Billing, "free") {
		return nil
	}
	p, e := c.resolvePrice(ctx)
	if e != nil {
		return e
	}
	c.price, c.rec.Price = p.rule, p.rule
	c.rec.RateMultiplier, c.rec.RateSource = p.rate, p.source
	c.capturePriceInputs(c.price)
	if e := c.prepareReferencedPrices(ctx); e != nil {
		return e
	}
	return c.admitCharge(ctx)
}

// admitCharge checks the key's quota and the owner's balance, and
// precharges the request.
func (c *call) admitCharge(ctx context.Context) *gwError {
	if c.g.d.Balance == nil {
		return fromCore(core.AsError(errors.New("billing precharger unavailable")), errTypeInternal)
	}
	if c.principal.KeyQuota.Exhausted() {
		return fromCore(core.ErrKeyQuotaExhausted, errTypeKeyQuotaExhausted)
	}
	if err := c.g.d.Balance.CheckBalance(ctx, c.principal.UserID); err != nil {
		return typedError(err, core.ErrInsufficientBalance.Code, errTypeInsufficientBalance)
	}
	if err := c.precharge(ctx); err != nil {
		return typedError(err, core.ErrInsufficientBalance.Code, errTypeInsufficientBalance)
	}
	return nil
}

// modelPrice is what the key's owner pays for a model in the key's group:
// its list price and the rate with where it comes from (core.RateLevels).
type modelPrice struct {
	rule   *core.PriceRule
	rate   decimal.Decimal
	source string
}

// priceOf resolves the price and the rate of model from one price snapshot
// (core.Pricer.Resolve).
func (c *call) priceOf(ctx context.Context, model string) (modelPrice, error) {
	rule, levels, err := c.g.d.Pricer.Resolve(ctx, c.priceQuery(model), c.principal.SegmentID, c.principal.Rates())
	if err != nil {
		return modelPrice{}, err
	}
	rate, source := levels.Rate()
	return modelPrice{rule: rule, rate: rate, source: source}, nil
}

// resolvePrice is the price of the request's model, which the endpoint
// must support the billing type of.
func (c *call) resolvePrice(ctx context.Context) (modelPrice, *gwError) {
	p, err := c.priceOf(ctx, c.model)
	if err != nil {
		return p, typedError(err, core.ErrPriceNotConfigured.Code, errTypePriceNotConfigured)
	}
	if kind := p.rule.BillingType(); !c.ep.SupportsBillingType(kind) {
		return p, &gwError{Status: http.StatusBadRequest, Code: "billing_type_not_supported",
			Message:    "endpoint " + c.ep.Protocol + " does not support " + kind + " pricing for model " + c.model,
			RecordType: errTypeInvalidRequest}
	}
	return p, nil
}

// typedError is the gateway error of err, recorded as recordType when its
// code is code and as internal otherwise.
func typedError(err error, code, recordType string) *gwError {
	e := core.AsError(err)
	if e.Code != code {
		recordType = errTypeInternal
	}
	return fromCore(e, recordType)
}

// capturePriceInputs records the param()/header() values the price
// expression reads, from the final (post-hook) request.
func (c *call) capturePriceInputs(rule *core.PriceRule) {
	c.rec.PriceParams, c.rec.PriceHeaders = c.priceInputs(rule)
}

func (c *call) priceInputs(rule *core.PriceRule) (params map[string]string, values map[string]string) {
	return c.priceInputsFrom(rule, c.body)
}

func (c *call) priceInputsFrom(rule *core.PriceRule, body []byte) (params map[string]string, values map[string]string) {
	if rule == nil {
		return nil, nil
	}
	paths, headers := c.g.d.Pricer.Inputs(rule)
	if len(paths) > 0 {
		params = map[string]string{}
		for _, p := range paths {
			if r := gjson.GetBytes(body, p); r.Exists() {
				params[p] = r.Raw
			}
		}
	}
	if len(headers) > 0 {
		values = map[string]string{}
		for _, h := range headers {
			if v := c.c.GetHeader(h); v != "" {
				values[headerLower(h)] = v
			}
		}
	}
	return params, values
}
