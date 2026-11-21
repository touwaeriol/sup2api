package billing

import (
	"encoding/json"

	"github.com/shopspring/decimal"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// PriceDef is a price as the pricing endpoints return it: a global model
// price, or the list price a price rule makes of one (CONTRACTS §97).
type PriceDef struct {
	Mode       string          `json:"mode"`
	Config     json.RawMessage `json:"config"`
	Expression string          `json:"expression"`
	// Multiplier is there, 0 and 1 included, on the list price of a multiplier
	// rule: the list price is this price times it. A global price and a custom
	// price have none.
	Multiplier *decimal.Decimal `json:"multiplier,omitempty"`
}

// listPrice returns the list price rule makes of the global price base, as
// the pricing endpoints show it. A custom rule is a price of its own. A
// multiplier goes beside the global price, which stays as it is: no config is
// scaled and no expression rewritten. It is the one place a rule becomes a
// price to show.
func listPrice(base PriceDef, rule *storedRule) PriceDef {
	switch {
	case rule == nil:
		return base
	case rule.Kind == core.PriceRuleCustom:
		return PriceDef{Mode: rule.Mode, Config: rule.Config, Expression: rule.Expression}
	}
	scale := rule.scale
	base.Multiplier = &scale
	return base
}

// listPrice returns the list price the first rule that applies makes of the
// global price base, and where it comes from.
func (l ruleLevels) listPrice(base PriceDef) (PriceDef, string) {
	rule, source := l.applied()
	return listPrice(base, rule), source
}
