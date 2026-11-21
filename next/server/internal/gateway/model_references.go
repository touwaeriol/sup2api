package gateway

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/usagerules"
)

const maxReferencedModels = 64

type modelReference struct {
	kind, path, model string
	priced            core.PricedUsage
	overrides         map[string]string
	identityOnly      bool
}

// checkReferencedModels runs before and after request hooks, just like the
// primary model check. Only declared model fields are inspected; tool input,
// prompt text and historical model names never grant new model invocations.
func (c *call) checkReferencedModels() *gwError {
	c.modelRefs = nil
	rules := c.ep.Request.ModelReferences
	if len(rules) == 0 {
		return nil // most endpoints declare none: no body is parsed for them
	}
	root := c.bodyRoot()
	for _, rule := range rules {
		locations, err := modelReferenceLocationsIn(root, rule.ArrayPath)
		if err != nil {
			return invalidModelReference(err.Error())
		}
		for _, location := range locations {
			item := location.item
			if !matchesModelReference(item, rule.Match) {
				continue
			}
			value := item.Get(rule.ModelPath)
			path := location.path + "." + rule.ModelPath
			if value.Type != gjson.String || value.Str == "" || strings.TrimSpace(value.Str) != value.Str || strings.ContainsAny(value.Str, "\r\n\x00") {
				return invalidModelReference(path + " must be a nonempty model identifier")
			}
			if !c.principal.Group.AllowsModel(value.Str) {
				return fromCore(core.ErrModelNotFound.WithDetails(map[string]any{"model": value.Str}), errTypeModelNotAllowed)
			}
			if len(c.modelRefs) == maxReferencedModels {
				return invalidModelReference("too many referenced models")
			}
			ref := modelReference{kind: rule.Name, path: path, model: value.Str, overrides: map[string]string{}, identityOnly: rule.IdentityOnly}
			for _, field := range rule.ParameterOverrides {
				if value := item.Get(field); value.Exists() {
					ref.overrides[field] = value.Raw
				}
			}
			c.modelRefs = append(c.modelRefs, ref)
		}
	}
	return nil
}

func matchesModelReference(item gjson.Result, match map[string]string) bool {
	if !item.IsObject() {
		return false
	}
	for path, expected := range match {
		value := item.Get(path)
		if value.Type != gjson.String || value.Str != expected {
			return false
		}
	}
	return true
}

func invalidModelReference(message string) *gwError {
	return &gwError{Status: http.StatusBadRequest, Code: core.ErrInvalidArgument.Code,
		Message: message, RecordType: errTypeInvalidRequest}
}

func (c *call) prepareReferencedPrices(ctx context.Context) *gwError {
	for i := range c.modelRefs {
		ref := &c.modelRefs[i]
		rule, err := c.g.d.Pricer.Resolve(ctx, c.priceQuery(ref.model))
		if err != nil {
			return fromCore(core.AsError(err), errTypePriceNotConfigured)
		}
		if rule.VideoOnly || !c.ep.SupportsBillingType(rule.Mode) {
			return invalidModelReference("referenced model " + ref.model + " has an incompatible billing type")
		}
		ref.priced = core.PricedUsage{Kind: ref.kind, Model: ref.model, Price: rule.Clone(),
			RateMultiplier: c.rec.RateMultiplier}
		body, err := c.referencePriceBody(*ref)
		if err != nil {
			return invalidModelReference("cannot prepare referenced model price inputs")
		}
		ref.priced.PriceParams, ref.priced.PriceHeaders = c.priceInputsFrom(rule, body)
	}
	return nil
}

func (c *call) routeHasRequiredPrimaryUsage(rt *typeRoute, body []byte) bool {
	if rt == nil {
		return false
	}
	rules := append([]manifest.AdditionalUsageRule(nil), c.pf.Usage.Additional...)
	if c.ep.Usage != nil {
		rules = append(rules, c.ep.Usage.Additional...)
	}
	rules = append(rules, rt.requiredUsage...)
	for _, required := range rules {
		if !required.UsePrimaryModel {
			continue
		}
		active := false
		for _, path := range required.RequiredBy {
			value := gjson.GetBytes(body, path)
			active = active || value.Exists() && value.Type != gjson.Null
		}
		if !active {
			continue
		}
		found := false
		for _, actual := range rt.usage.Additional {
			found = found || actual.Name == required.Name && actual.UsePrimaryModel
		}
		if !found {
			return false
		}
	}
	return true
}

func routeAccountsFor(rt *typeRoute, kind string) bool {
	if rt == nil {
		return false
	}
	if rt.usage.Attempts != nil && rt.usage.Attempts.Name == kind {
		return true
	}
	for _, rule := range rt.usage.Additional {
		if rule.Name == kind {
			return true
		}
	}
	return false
}

// mapReferencedModels rewrites only admitted fields. A converted protocol has
// no established location for these invocations, so it cannot silently drop
// or reinterpret them. Different client prices must not collapse to the same
// observed upstream identity within one usage kind.
func (c *call) mapReferencedModels(body []byte, account *core.AccountRef, rt *typeRoute) ([]byte, error) {
	c.upstreamPrimaryModel = account.MapModel(c.model)
	c.upstreamRefs = nil
	if rt.conv != nil {
		refs, err := modelReferenceSnapshot(body, rt.modelReferences)
		if err != nil {
			return nil, err
		}
		if len(refs) != 0 {
			return nil, fmt.Errorf("conversion introduced model invocations without source authorization")
		}
	}
	if len(c.modelRefs) == 0 {
		return body, nil
	}
	if rt.upstream != c.ep.Protocol {
		return nil, fmt.Errorf("referenced model invocations cannot be converted to %s", rt.upstream)
	}
	c.upstreamRefs = map[string]core.PricedUsage{}
	for _, ref := range c.modelRefs {
		upModel := account.MapModel(ref.model)
		if ref.identityOnly && upModel != ref.model {
			return nil, fmt.Errorf("signed model reference requires identity mapping")
		}
		if rt.usage.Attempts != nil && ref.kind == rt.usage.Attempts.Name && upModel == c.upstreamPrimaryModel {
			return nil, fmt.Errorf("attempt candidate maps to the primary model")
		}
		key := ref.kind + "\x00" + upModel
		if old, exists := c.upstreamRefs[key]; exists && old.Model != ref.model {
			return nil, fmt.Errorf("different referenced models map to the same upstream usage identity")
		}
		c.upstreamRefs[key] = ref.priced
		if ref.identityOnly {
			continue
		}
		var err error
		body, err = sjson.SetBytes(body, ref.path, upModel)
		if err != nil {
			return nil, fmt.Errorf("map referenced model: %w", err)
		}
	}
	return body, nil
}

func (c *call) recordAdditionalUsage(u *usagerules.Acc, rt *typeRoute) {
	c.rec.Additional = nil
	c.rec.BillingError = u.AdditionalError
	for _, actual := range u.Additional() {
		priced, ok := c.upstreamRefs[actual.Kind+"\x00"+actual.Model]
		if !ok && actual.Model == c.upstreamPrimaryModel && primaryUsageKind(rt, actual.Kind) {
			priced = core.PricedUsage{Kind: actual.Kind, Model: c.model, Price: c.rec.Price.Clone(),
				PriceParams: c.rec.PriceParams, PriceHeaders: c.rec.PriceHeaders, RateMultiplier: c.rec.RateMultiplier}
			ok = true
		}
		if !ok {
			c.rec.BillingError = "additional usage does not match an admitted model identity"
			continue
		}
		priced.Tokens, priced.UsageSemantics = actual.Tokens, actual.UsageSemantics
		priced.Metrics = actual.Metrics
		c.rec.Additional = append(c.rec.Additional, priced)
	}
}

func primaryUsageKind(rt *typeRoute, kind string) bool {
	for _, rule := range rt.usage.Additional {
		if rule.Name == kind && rule.UsePrimaryModel {
			return true
		}
	}
	return false
}

func (c *call) estimatedAdditional(tokens core.UsageTokens, semantics string) []core.PricedUsage {
	seen := map[string]bool{}
	var out []core.PricedUsage
	for _, ref := range c.modelRefs {
		key := ref.kind + "\x00" + ref.model
		if seen[key] {
			continue
		}
		seen[key] = true
		priced := ref.priced
		priced.Tokens, priced.UsageSemantics = tokens, semantics
		out = append(out, priced)
	}
	return out
}

func modelReferenceSnapshot(body []byte, rules []manifest.RequestModelReference) ([]string, error) {
	if len(rules) == 0 {
		return nil, nil
	}
	return modelReferenceSnapshotIn(gjson.ParseBytes(body), rules)
}

// modelReferenceSnapshotIn is modelReferenceSnapshot over a parsed root.
func modelReferenceSnapshotIn(root gjson.Result, rules []manifest.RequestModelReference) ([]string, error) {
	var refs []string
	modelCount := 0
	for _, rule := range rules {
		locations, err := modelReferenceLocationsIn(root, rule.ArrayPath)
		if err != nil {
			return nil, err
		}
		for _, location := range locations {
			item := location.item
			if !matchesModelReference(item, rule.Match) {
				continue
			}
			model := item.Get(rule.ModelPath)
			if model.Type != gjson.String || modelCount >= maxReferencedModels {
				return nil, fmt.Errorf("plugin changed model references")
			}
			modelCount++
			refs = append(refs, fmt.Sprintf("%s\x00%s.%s\x00%s", rule.Name, location.path, rule.ModelPath, model.Str))
			for _, field := range rule.ParameterOverrides {
				value := item.Get(field)
				refs = append(refs, field+"\x00"+value.Raw)
			}
		}
	}
	return refs, nil
}
