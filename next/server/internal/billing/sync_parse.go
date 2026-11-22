package billing

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
)

// perMillion converts a USD-per-token price to USD per million tokens.
func perMillion(v *expr.Num) *decimal.Decimal {
	if v == nil {
		return nil
	}
	d := decimal.NewFromFloat(float64(*v)).Shift(6)
	return &d
}

// tokenSet is one price level in USD per million tokens (nil = not billed).
type tokenSet struct{ p, c, cr, cc, cc1h *decimal.Decimal }

func (t tokenSet) config() map[string]any {
	m := map[string]any{}
	put := func(k string, v *decimal.Decimal) {
		if v != nil {
			m[k] = json.Number(v.String())
		}
	}
	put("p", t.p)
	put("c", t.c)
	put("cr", t.cr)
	put("cc", t.cc)
	put("cc1h", t.cc1h)
	return m
}

// or fills the unset prices of t from base.
func (t tokenSet) or(base tokenSet) tokenSet {
	pick := func(a, b *decimal.Decimal) *decimal.Decimal {
		if a != nil {
			return a
		}
		return b
	}
	return tokenSet{pick(t.p, base.p), pick(t.c, base.c), pick(t.cr, base.cr), pick(t.cc, base.cc), pick(t.cc1h, base.cc1h)}
}

func (t tokenSet) empty() bool {
	return t.p == nil && t.c == nil && t.cr == nil && t.cc == nil && t.cc1h == nil
}

// lenAtMost is the tier condition len <= n.
func lenAtMost(n any) map[string]any {
	return map[string]any{"var": "len", "op": "<=", "value": n}
}

// levelsPrice builds a visual price, with context tiers when there are
// several levels: levels[0] applies up to sizes[0] tokens of prompt
// ("when": len <= sizes[0]), levels[i] above sizes[i-1], the last one to the
// rest. The standard level must set input or output.
func levelsPrice(model string, levels []tokenSet, sizes []int64) *syncPrice {
	if len(levels) == 0 || levels[0].p == nil && levels[0].c == nil {
		return nil
	}
	zero := decimal.Zero
	if levels[0].p == nil {
		levels[0].p = &zero
	}
	if levels[0].c == nil {
		levels[0].c = &zero
	}
	tiers := make([]map[string]any, 0, len(levels))
	for i, l := range levels {
		t := l.or(levels[0]).config()
		t["name"], t["unit"] = levelName(i, len(levels), sizes), expr.UnitToken
		if i < len(sizes) {
			t["when"] = lenAtMost(sizes[i])
		}
		tiers = append(tiers, t)
	}
	cfg, _ := json.Marshal(map[string]any{"tiers": tiers, "rules": []any{}})
	return &syncPrice{Model: model, Mode: expr.ModeVisual, Config: cfg}
}

// levelName names level i of n: base alone; standard and long_context for
// two; standard and over_<previous size> for more.
func levelName(i, n int, sizes []int64) string {
	switch {
	case n == 1:
		return "base"
	case i == 0:
		return "standard"
	case n == 2:
		return "long_context"
	}
	return fmt.Sprintf("over_%d", sizes[i-1])
}

// litellmEntry is the part of a LiteLLM catalog entry used here (USD per token).
type litellmEntry struct {
	Provider string    `json:"litellm_provider"`
	Mode     string    `json:"mode"`
	In       *expr.Num `json:"input_cost_per_token"`
	Out      *expr.Num `json:"output_cost_per_token"`
	CR       *expr.Num `json:"cache_read_input_token_cost"`
	CC       *expr.Num `json:"cache_creation_input_token_cost"`
	CC1h     *expr.Num `json:"cache_creation_input_token_cost_above_1hr"`
	In200    *expr.Num `json:"input_cost_per_token_above_200k_tokens"`
	Out200   *expr.Num `json:"output_cost_per_token_above_200k_tokens"`
	CR200    *expr.Num `json:"cache_read_input_token_cost_above_200k_tokens"`
	CC200    *expr.Num `json:"cache_creation_input_token_cost_above_200k_tokens"`
	CC1h200  *expr.Num `json:"cache_creation_input_token_cost_above_1hr_above_200k_tokens"`
}

var litellmModes = map[string]bool{"chat": true, "completion": true, "responses": true, "embedding": true}

// parseLiteLLM reads model_prices_and_context_window.json. Keys are native
// model ids, sometimes prefixed with "<provider>/"; entries of providers not
// listed are ignored, and the earlier provider wins when two give the same id.
func parseLiteLLM(body []byte, providers []string) (*fetched, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("not a LiteLLM price catalog: %w", err)
	}
	rank := map[string]int{}
	for i, p := range providers {
		rank[p] = i + 1
	}
	out := &fetched{prices: map[string]*syncPrice{}}
	best := map[string]int{}
	for _, k := range slices.Sorted(maps.Keys(raw)) {
		var e litellmEntry
		if json.Unmarshal(raw[k], &e) != nil || rank[e.Provider] == 0 || !litellmModes[e.Mode] {
			continue
		}
		model, p := litellmPrice(k, e)
		if p == nil {
			out.skipped++
			continue
		}
		if r, ok := best[model]; ok && r <= rank[e.Provider] {
			continue
		}
		best[model] = rank[e.Provider]
		out.prices[model] = p
	}
	return out, nil
}

// litellmPrice is the model id of the entry e under key k and its price,
// nil when the id or the prices cannot be imported.
func litellmPrice(k string, e litellmEntry) (string, *syncPrice) {
	model := strings.TrimPrefix(k, e.Provider+"/")
	if !manifest.ValidModelID(model) || strings.Contains(model, "/") {
		return model, nil
	}
	std := tokenSet{perMillion(e.In), perMillion(e.Out), perMillion(e.CR), perMillion(e.CC), perMillion(e.CC1h)}
	long := tokenSet{perMillion(e.In200), perMillion(e.Out200), perMillion(e.CR200), perMillion(e.CC200), perMillion(e.CC1h200)}
	levels, sizes := []tokenSet{std}, []int64{}
	if !long.empty() {
		levels, sizes = append(levels, long), []int64{longContextLen}
	}
	return model, levelsPrice(model, levels, sizes)
}

// modelsDevCost is models.dev's cost object (USD per million tokens).
type modelsDevCost struct {
	Input     *expr.Num `json:"input"`
	Output    *expr.Num `json:"output"`
	CacheRead *expr.Num `json:"cache_read"`
	CacheW    *expr.Num `json:"cache_write"`
	Tiers     []struct {
		Input     *expr.Num `json:"input"`
		Output    *expr.Num `json:"output"`
		CacheRead *expr.Num `json:"cache_read"`
		CacheW    *expr.Num `json:"cache_write"`
		Tier      struct {
			Type string `json:"type"`
			Size int64  `json:"size"`
		} `json:"tier"`
	} `json:"tiers"`
}

func num(v *expr.Num) *decimal.Decimal {
	if v == nil {
		return nil
	}
	d := decimal.NewFromFloat(float64(*v))
	return &d
}

// parseModelsDev reads models.dev api.json: {provider: {models: {id: {cost}}}}.
// Only the listed providers are read; the earlier provider wins on an id
// clash. models.dev has no 1-hour cache write price.
func parseModelsDev(body []byte, providers []string) (*fetched, error) {
	var raw map[string]struct {
		Models map[string]struct {
			Cost *modelsDevCost `json:"cost"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("not a models.dev catalog: %w", err)
	}
	out := &fetched{prices: map[string]*syncPrice{}}
	for _, prov := range providers {
		ms := raw[prov].Models
		for _, id := range slices.Sorted(maps.Keys(ms)) {
			c := ms[id].Cost
			if _, dup := out.prices[id]; dup || c == nil {
				continue
			}
			if p := modelsDevPrice(id, c); p != nil {
				out.prices[id] = p
			} else {
				out.skipped++
			}
		}
	}
	return out, nil
}

// modelsDevPrice is the price of the model id at cost c, with its context
// tiers; nil when the id or the prices cannot be imported.
func modelsDevPrice(id string, c *modelsDevCost) *syncPrice {
	if !manifest.ValidModelID(id) {
		return nil
	}
	levels := []tokenSet{{p: num(c.Input), c: num(c.Output), cr: num(c.CacheRead), cc: num(c.CacheW)}}
	var sizes []int64
	tiers := c.Tiers
	sort.SliceStable(tiers, func(i, j int) bool { return tiers[i].Tier.Size < tiers[j].Tier.Size })
	for _, t := range tiers {
		if t.Tier.Type != "context" || t.Tier.Size <= 0 {
			continue
		}
		sizes = append(sizes, t.Tier.Size)
		levels = append(levels, tokenSet{p: num(t.Input), c: num(t.Output), cr: num(t.CacheRead), cc: num(t.CacheW)})
	}
	return levelsPrice(id, levels, sizes)
}

// parseSup2API reads another sup2api's GET /key/prices. With apply, prices
// are multiplied by the upstream group's rate multiplier so they are what
// this instance pays upstream.
func parseSup2API(body []byte, apply bool) (*fetched, error) {
	var env struct {
		Data *KeyPrices `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil || env.Data == nil {
		if err == nil {
			err = errors.New("missing data")
		}
		return nil, fmt.Errorf("not a sup2api price list: %w", err)
	}
	kp := env.Data
	m := decimal.NewFromInt(1)
	if apply && kp.RateMultiplier.IsPositive() {
		m = kp.RateMultiplier
	}
	out := &fetched{prices: map[string]*syncPrice{}}
	for i := range kp.Prices {
		p := kp.Prices[i]
		if !fromUpstream(&p, m) {
			out.skipped++
			continue
		}
		out.prices[p.Model] = &p
	}
	return out, nil
}

// fromUpstream brings a price of an upstream sup2api to this instance at
// the multiplier m; false when it cannot be imported. Prices with a config
// are generated locally; an expression price is its expression.
func fromUpstream(p *syncPrice, m decimal.Decimal) bool {
	upgradeLegacy(p)
	if p.Mode != expr.ModeExpression {
		p.Expression = ""
	}
	if !manifest.ValidModelID(p.Model) {
		return false
	}
	return m.Equal(decimal.NewFromInt(1)) || scalePrice(p, m) == nil
}

// upgradeLegacy brings a price of an upstream older than core migration
// 0122 to the current shapes, as migrations 0121 and 0122 did to stored
// prices: per_token and per_request prices become visual
// (visualFromLegacy), an expression price has config {} (a visual or video
// config there is gone; the expression is the price) and "A ||| B" rules
// become "(A) * (B)". An expression this engine compiles is already of a
// current upstream and stays as it is (a "|||" in a comment).
func upgradeLegacy(p *syncPrice) {
	if cfg, ok := visualFromLegacy(p.Mode, p.Config); ok {
		p.Mode, p.Config = expr.ModeVisual, cfg
		return
	}
	if p.Mode != expr.ModeExpression {
		return
	}
	p.Config = json.RawMessage("{}")
	if _, err := expr.CompileCached(p.Expression); err == nil {
		return
	}
	if src, ok := expr.LegacyProduct(p.Expression); ok {
		p.Expression = src
	}
}
