package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
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

// referencePriceBody is the request as the referenced model's price reads
// it: the reference's model and parameter overrides in place.
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

func (c *call) prepareReferencedPrices(ctx context.Context) *gwError {
	for i := range c.modelRefs {
		ref := &c.modelRefs[i]
		rule, err := c.g.d.Pricer.Resolve(ctx, c.priceQuery(ref.model))
		if err != nil {
			return fromCore(core.AsError(err), errTypePriceNotConfigured)
		}
		if rule.VideoOnly || !c.ep.SupportsBillingType(rule.BillingType()) {
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

// mapReferencedModels maps the models the request references (for example
// its fallbacks) with the account's model mapping, and keeps the price of
// each by the usage identity the response reports it under. A converted
// request does not carry them: the conversion drops what the upstream
// protocol has no place for, so nothing is mapped and the response is billed
// at the requested model. Two references the mapping makes one upstream
// model cannot be told apart in the response: their usage is billed at the
// requested model (recordSegments).
func (c *call) mapReferencedModels(body []byte, account *core.AccountRef, rt *typeRoute) ([]byte, error) {
	c.upstreamPrimaryModel = account.MapModel(c.model)
	c.upstreamRefs = nil
	if rt.conv != nil {
		refs, err := modelReferenceSnapshot(body, rt.modelReferences)
		if err != nil {
			return nil, err
		}
		if len(refs) != 0 {
			return nil, errors.New("conversion introduced model invocations without source authorization")
		}
	}
	if len(c.modelRefs) == 0 || rt.upstream != c.ep.Protocol {
		return body, nil
	}
	c.upstreamRefs = map[string]core.PricedUsage{}
	ambiguous := map[string]bool{}
	for _, ref := range c.modelRefs {
		upModel := account.MapModel(ref.model)
		if ref.identityOnly && upModel != ref.model {
			return nil, errors.New("signed model reference requires identity mapping")
		}
		key := ref.kind + "\x00" + upModel
		if old, exists := c.upstreamRefs[key]; ambiguous[key] || exists && old.Model != ref.model {
			ambiguous[key] = true
			delete(c.upstreamRefs, key)
		} else {
			c.upstreamRefs[key] = ref.priced
		}
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
				return nil, errors.New("plugin changed model references")
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
