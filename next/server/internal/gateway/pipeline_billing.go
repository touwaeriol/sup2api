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

// prepareBilling resolves the model's global price (ARCHITECTURE 7.3),
// captures the price inputs and checks the balance. Free endpoints skip it.
func (c *call) prepareBilling(ctx context.Context) *gwError {
	if strings.EqualFold(c.ep.Billing, "free") {
		return nil
	}
	rule, e := c.resolvePrice(ctx)
	if e != nil {
		return e
	}
	c.price = rule
	c.rec.Price = rule
	c.capturePriceInputs(c.price)
	if c.rec.RateMultiplier, c.rec.RateSource, e = c.modelRate(ctx, c.model); e != nil {
		return e
	}
	if e := c.prepareReferencedPrices(ctx); e != nil {
		return e
	}
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

// resolvePrice is the list price of the request's model, which the endpoint
// must support the billing type of.
func (c *call) resolvePrice(ctx context.Context) (*core.PriceRule, *gwError) {
	rule, err := c.g.d.Pricer.Resolve(ctx, c.priceQuery(c.model))
	if err != nil {
		return nil, typedError(err, core.ErrPriceNotConfigured.Code, errTypePriceNotConfigured)
	}
	if kind := rule.BillingType(); !c.ep.SupportsBillingType(kind) {
		return nil, &gwError{Status: http.StatusBadRequest, Code: "billing_type_not_supported",
			Message:    "endpoint " + c.ep.Protocol + " does not support " + kind + " pricing for model " + c.model,
			RecordType: errTypeInvalidRequest}
	}
	return rule, nil
}

// modelRate is the rate the key's owner pays for model in the key's group
// and where it comes from (core.RateLevels).
func (c *call) modelRate(ctx context.Context, model string) (decimal.Decimal, string, *gwError) {
	levels, err := c.g.d.Pricer.ModelRates(ctx, c.priceQuery(model), c.principal.SegmentID, c.principal.Rates())
	if err != nil {
		return decimal.Zero, "", fromCore(core.AsError(err), errTypeInternal)
	}
	rate, source := levels.Rate()
	return rate, source, nil
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
