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
		return scaleAsExpression(p, m.String())
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
// "(expression) * factor"; factor is a constant expression.
func scaleAsExpression(p *syncPrice, factor string) error {
	src, err := expr.Times(p.Expression, factor)
	if err != nil {
		return err
	}
	p.Mode, p.Config, p.Expression = expr.ModeExpression, json.RawMessage("{}"), src
	return nil
}

// keyPrices lists the enabled prices for the holder of an API key (another
// sup2api syncing from this one): only models the key's group allows and
// prices (a global price or the group's own), at the group's list price from
// the price snapshot requests are billed by, plus the rate the key's owner
// pays there for a model with no rate of its own.
// The key goes in Authorization: Bearer or x-api-key.
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
	rate, _ := pr.Rates().Rate()
	out := KeyPrices{Prices: []syncPrice{}, RateMultiplier: rate}
	out.Group.ID, out.Group.Name = pr.Group.ID, pr.Group.Name
	models := slices.DeleteFunc(snap.priced(pr.Group.ID), func(model string) bool { return !pr.Group.AllowsModel(model) })
	snap.each(pr.Group.ID, models, func(model string, price PriceDef, _ string) {
		modelRate, _ := snap.rates.levels(core.PriceQuery{Model: model, GroupID: pr.Group.ID}, pr.SegmentID, pr.Rates()).Rate()
		p, err := exportPrice(model, price, rate, modelRate)
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
// which applies one rate, that of the key, to every price: here, and nowhere
// else, a model paid at another rate has the ratio of the two folded into
// its price (scalePrice). A ratio with no finite decimal (1 / 3) is not
// rounded: the price becomes the expression "(expression) * modelRate /
// keyRate", which the key's rate turns back into the model's (CONTRACTS
// §97.6). The expression is always there. An error means the price cannot
// carry the ratio.
func exportPrice(model string, price PriceDef, keyRate, modelRate decimal.Decimal) (syncPrice, error) {
	p := syncPrice{Model: model, Mode: price.Mode, Config: price.Config, Expression: price.Expression}
	if modelRate.Equal(keyRate) {
		return p, nil
	}
	if keyRate.IsZero() {
		return p, errors.New("the model has a rate of its own in a group the key pays nothing in")
	}
	ratio := modelRate.Div(keyRate)
	if !ratio.Mul(keyRate).Equal(modelRate) {
		return p, scaleAsExpression(&p, modelRate.String()+" / "+keyRate.String())
	}
	if err := scalePrice(&p, ratio); err != nil || p.Expression != "" {
		return p, err
	}
	// A scaled config: its expression is generated, as on import.
	var err error
	p.Expression, err = expressionFor(p.Mode, p.Config, "")
	return p, err
}
