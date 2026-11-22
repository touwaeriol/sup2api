package billing

import (
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
)

// KeyPrices is the response of GET /key/prices.
type KeyPrices struct {
	Prices         []syncPrice     `json:"prices"`
	RateMultiplier decimal.Decimal `json:"rate_multiplier"`
	Group          struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"group"`
}

// scalePrice multiplies a price by m: the tier prices of a visual config
// are scaled in place (rule multipliers and tier conditions stay), an
// expression price becomes "(expression) * m". Only a visual price needs its
// config to be an object.
func scalePrice(p *syncPrice, m decimal.Decimal) error {
	if p.Mode != expr.ModeVisual {
		return scaleAsExpression(p, m)
	}
	var cfg map[string]any
	dec := json.NewDecoder(strings.NewReader(string(p.Config)))
	dec.UseNumber()
	_ = dec.Decode(&cfg)
	if cfg == nil { // nil unless the config is an object
		return errors.New("price config is not an object")
	}
	tiers, _ := cfg["tiers"].([]any)
	for _, t := range tiers {
		if tier, ok := t.(map[string]any); ok {
			scaleTier(tier, m)
		}
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	p.Config, p.Expression = b, ""
	return nil
}

// scaleTier multiplies the prices of a tier (expr.PriceKeys) by m; a value
// that is not a number stays.
func scaleTier(tier map[string]any, m decimal.Decimal) {
	for _, k := range expr.PriceKeys {
		v, ok := tier[k]
		if !ok {
			continue
		}
		var d decimal.Decimal
		var err error
		switch x := v.(type) {
		case json.Number:
			d, err = decimal.NewFromString(string(x))
		case string: // expr.Num takes numeric strings
			d, err = decimal.NewFromString(strings.TrimSpace(x))
		default:
			continue
		}
		if err == nil {
			tier[k] = json.Number(d.Mul(m).String())
		}
	}
}

// scaleAsExpression rewrites a price as the expression price
// "(expression) * m".
func scaleAsExpression(p *syncPrice, m decimal.Decimal) error {
	src, err := expr.Times(p.Expression, m.String())
	if err != nil {
		return err
	}
	p.Mode, p.Config, p.Expression = expr.ModeExpression, json.RawMessage("{}"), src
	return nil
}

// keyPrices lists the enabled prices for the holder of an API key (another
// sup2api syncing from this one): only models the key's group allows, at the
// group's list price from the price snapshot requests are billed by, plus
// the rate the key's owner pays there. The key goes in Authorization: Bearer
// or x-api-key.
func (s *Service) keyPrices(c *gin.Context) {
	ctx := c.Request.Context()
	pr, err := s.keyPrincipal(c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	snap, err := s.snapshot(ctx)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	models := slices.DeleteFunc(snap.bases.models(), func(model string) bool { return !pr.Group.AllowsModel(model) })
	out := KeyPrices{Prices: []syncPrice{}, RateMultiplier: pr.Group.RateMultiplier}
	out.Group.ID, out.Group.Name = pr.Group.ID, pr.Group.Name
	snap.each(pr.Group.ID, models, func(model string, base PriceDef, rule *storedRule) {
		p, err := exportPrice(model, base, rule)
		if err != nil {
			// Left out rather than listed at a price that is not the owner's.
			slog.WarnContext(ctx, "billing: key prices: model left out", "model", model, "group_id", pr.Group.ID, "err", err)
			return
		}
		out.Prices = append(out.Prices, p)
	})
	httpapi.OK(c, out)
}

// keyPrincipal authenticates the API key of a GET /key/prices request.
func (s *Service) keyPrincipal(c *gin.Context) (*core.APIKeyPrincipal, error) {
	if s.sync.Keys == nil {
		return nil, core.ErrUnavailable.WithMessage("API key authentication unavailable")
	}
	raw := strings.TrimSpace(c.GetHeader("x-api-key"))
	if raw == "" {
		raw = strings.TrimSpace(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
	}
	if raw == "" {
		return nil, core.ErrUnauthenticated.WithMessage("missing API key")
	}
	return s.sync.Keys.Authenticate(core.WithClientIP(c.Request.Context(), c.ClientIP()), raw)
}

// exportPrice returns the list price of a model for a downstream sup2api,
// which sees no rule beside the price: here, and nowhere else, the multiplier
// of a rule goes into the price (scalePrice). The expression is always there.
// An error means the price cannot carry the multiplier.
func exportPrice(model string, base PriceDef, rule *storedRule) (syncPrice, error) {
	price := listPrice(base, rule)
	p := syncPrice{Model: model, Mode: price.Mode, Config: price.Config, Expression: price.Expression}
	if price.Multiplier == nil || price.Multiplier.Equal(decimal.NewFromInt(1)) {
		return p, nil
	}
	scaled := p
	if err := scalePrice(&scaled, *price.Multiplier); err != nil || scaled.Expression != "" {
		return scaled, err
	}
	// A scaled config: its expression is generated, as on import.
	var err error
	scaled.Expression, err = expressionFor(scaled.Mode, scaled.Config, "")
	return scaled, err
}
