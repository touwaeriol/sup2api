package gateway

import (
	"fmt"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/usagerules"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func attemptUsageActive(rule *manifest.AttemptUsageRule, body []byte) bool {
	if rule == nil {
		return false
	}
	for _, path := range rule.RequiredBy {
		value := gjson.GetBytes(body, path)
		if value.Exists() && value.Type != gjson.Null {
			return true
		}
	}
	return false
}

func (c *call) declaredAttemptRules(rt *typeRoute) []*manifest.AttemptUsageRule {
	rules := append([]*manifest.AttemptUsageRule{c.pf.Usage.Attempts}, rt.requiredAttempts...)
	if c.ep.Usage != nil {
		rules = append(rules, c.ep.Usage.Attempts)
	}
	return rules
}

func (c *call) hasRequestedAttemptUsage(rt *typeRoute, body []byte) bool {
	for _, rule := range c.declaredAttemptRules(rt) {
		if attemptUsageActive(rule, body) {
			return true
		}
	}
	return false
}

func (c *call) referencePriceBody(ref modelReference) ([]byte, error) {
	body := c.body
	var err error
	if path := c.ep.Request.ModelPath; path != "" {
		body, err = sjson.SetBytes(body, path, ref.model)
		if err != nil {
			return nil, err
		}
	}
	for path, raw := range ref.overrides {
		body, err = sjson.SetRawBytes(body, path, []byte(raw))
		if err != nil {
			return nil, err
		}
	}
	return body, nil
}

// replacementUsage prices the attempts that replace the top-level usage,
// and names the upstream model of the last one; nil when the response has
// none.
func (c *call) replacementUsage(u *usagerules.Acc, rt *typeRoute) ([]core.PricedUsage, string, error) {
	actual, err := u.Replacement()
	if err != nil || len(actual) == 0 {
		return nil, "", err
	}
	items := make([]core.PricedUsage, 0, len(actual))
	for _, attempt := range actual {
		priced, ok := c.upstreamRefs[attempt.Kind+"\x00"+attempt.Model]
		if attempt.Model == c.upstreamPrimaryModel && rt.usage.Attempts != nil && attempt.Kind == rt.usage.Attempts.Name {
			priced = core.PricedUsage{Kind: attempt.Kind, Model: c.model, Price: c.rec.Price.Clone(), PriceParams: c.rec.PriceParams, PriceHeaders: c.rec.PriceHeaders, RateMultiplier: c.rec.RateMultiplier}
			ok = true
		}
		if !ok {
			return nil, "", fmt.Errorf("attempt usage does not match an admitted model identity")
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
