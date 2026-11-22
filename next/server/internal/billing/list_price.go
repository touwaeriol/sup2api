package billing

import (
	"encoding/json"
)

// PriceDef is a price as the pricing endpoints return it: a global model
// price, or a group's own price of a model (CONTRACTS §97).
type PriceDef struct {
	Mode       string          `json:"mode"`
	Config     json.RawMessage `json:"config"`
	Expression string          `json:"expression"`
}

// listPrice returns the list price under the group's model price rule (nil
// = none) of a model whose global price is base, as the pricing endpoints
// show it: the rule's own price, else the global price. It is the one place
// a rule becomes a price to show.
func listPrice(base PriceDef, rule *storedRule) PriceDef {
	if rule == nil {
		return base
	}
	return PriceDef{Mode: rule.Mode, Config: rule.Config, Expression: rule.Expression}
}
