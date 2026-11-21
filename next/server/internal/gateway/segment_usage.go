package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/usagerules"
)

// recordSegments records the usage billed apart from the top-level usage:
// the additional usage and the attempts that replace it, each at the price
// of the model it was admitted under. A segment reported under a model
// without an admitted price is billed at the requested model's price and
// noted in RequestedModelSegments. Only segments that cannot be read are
// dropped, all of them: the request is then billed on its top-level usage
// at the requested model, noted in SegmentsDropped (CONTRACTS §120.4).
func (c *call) recordSegments(ctx context.Context, u *usagerules.Acc) {
	c.rec.Additional, c.rec.Replacement = nil, nil
	c.rec.RequestedModelSegments, c.rec.SegmentsDropped = nil, ""
	additional, err := c.additionalUsage(u)
	if err != nil {
		c.dropSegments(ctx, core.SegmentAdditional, err)
		return
	}
	replacement, upModel, err := c.replacementUsage(u)
	if err != nil {
		c.dropSegments(ctx, core.SegmentReplacement, err)
		return
	}
	c.rec.Additional, c.rec.Replacement = additional, replacement
	if upModel != "" {
		c.rec.UpstreamModel = upModel
	}
}

// dropSegments drops every segment because those of part cannot be read.
func (c *call) dropSegments(ctx context.Context, part string, err error) {
	c.rec.RequestedModelSegments = nil
	c.rec.SegmentsDropped = part + ": " + err.Error()
	slog.WarnContext(ctx, "gateway: usage segments cannot be read; billing the top-level usage at the requested model",
		"request_id", c.rid, "failed", part, "model", c.model, "err", err)
}

// requestedModelPrice is the requested model's frozen price for a segment of
// kind.
func (c *call) requestedModelPrice(kind string) core.PricedUsage {
	return core.PricedUsage{Kind: kind, Model: c.model, Price: c.rec.Price.Clone(),
		PriceParams: c.rec.PriceParams, PriceHeaders: c.rec.PriceHeaders, RateMultiplier: c.rec.RateMultiplier}
}

// unpricedSegment is requestedModelPrice for the segment at index of part,
// which the response reported under upModel, a model without an admitted
// price; the record notes it.
func (c *call) unpricedSegment(part string, index int, kind, upModel string) core.PricedUsage {
	c.rec.RequestedModelSegments = append(c.rec.RequestedModelSegments,
		core.RequestedModelSegment{Part: part, Index: index, Model: upModel})
	return c.requestedModelPrice(kind)
}

// additionalUsage prices the usage billed on top of the top-level usage. A
// referenced model's price comes first: it carries the reference's own
// price parameters.
func (c *call) additionalUsage(u *usagerules.Acc) ([]core.PricedUsage, error) {
	if u.AdditionalError != "" {
		return nil, errors.New(u.AdditionalError)
	}
	var items []core.PricedUsage
	for _, actual := range u.Additional() {
		priced, ok := c.upstreamRefs[actual.Kind+"\x00"+actual.Model]
		switch {
		case ok:
		case actual.Model == c.upstreamPrimaryModel:
			priced = c.requestedModelPrice(actual.Kind)
		default:
			priced = c.unpricedSegment(core.SegmentAdditional, len(items), actual.Kind, actual.Model)
		}
		priced.Tokens, priced.UsageSemantics, priced.Metrics = actual.Tokens, actual.UsageSemantics, actual.Metrics
		items = append(items, priced)
	}
	return items, nil
}

// replacementUsage prices the attempts that replace the top-level usage,
// and names the upstream model of the last one; nil when the response has
// none. An attempt at the primary model is the request itself.
func (c *call) replacementUsage(u *usagerules.Acc) ([]core.PricedUsage, string, error) {
	actual, err := u.Replacement()
	if err != nil || len(actual) == 0 {
		return nil, "", err
	}
	items := make([]core.PricedUsage, 0, len(actual))
	for _, attempt := range actual {
		priced, ok := c.upstreamRefs[attempt.Kind+"\x00"+attempt.Model]
		switch {
		case attempt.Model == c.upstreamPrimaryModel:
			priced = c.requestedModelPrice(attempt.Kind)
		case !ok:
			priced = c.unpricedSegment(core.SegmentReplacement, len(items), attempt.Kind, attempt.Model)
		}
		priced.Tokens, priced.UsageSemantics, priced.Metrics = attempt.Tokens, attempt.UsageSemantics, attempt.Metrics
		priced.Free, priced.BillingReason = attempt.Free, attempt.BillingReason
		if err := core.ValidatePricedUsage(priced); err != nil {
			return nil, "", err
		}
		if err := validateAttemptPriceFacts(priced); err != nil {
			return nil, "", err
		}
		items = append(items, priced)
	}
	return items, actual[len(actual)-1].Model, nil
}

// A request parameter is not evidence of a served tier or speed. In particular,
// top-level final usage cannot supply facts for an earlier declined attempt.
// The price evaluator's general missing-metric default must not invent them.
func validateAttemptPriceFacts(item core.PricedUsage) error {
	if item.Free || item.Price == nil {
		return nil
	}
	program, err := expr.CompileCached(item.Price.Expression)
	if err != nil {
		return fmt.Errorf("invalid frozen attempt price expression: %w", err)
	}
	for _, key := range program.Facts() {
		if value, ok := item.Metrics[key]; !ok || value == nil {
			return fmt.Errorf("attempt usage lacks price fact %q for %q", key, item.Model)
		}
	}
	return nil
}
