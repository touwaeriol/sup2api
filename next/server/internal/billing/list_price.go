package billing

import (
	"encoding/json"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// PriceDef is a price as the pricing endpoints return it: a global model
// price, or a group's own price of a model (CONTRACTS §97).
type PriceDef struct {
	Mode       string          `json:"mode"`
	Config     json.RawMessage `json:"config"`
	Expression string          `json:"expression"`
}

// listPrice returns the list price of a model, as the pricing endpoints
// show it, and where it comes from: the group's own price of it (rule, nil
// = none), else its global price (global, nil = none); nil when it has
// neither and cannot be billed. It is the one place a rule becomes a price
// to show.
func listPrice(global *PriceDef, rule *storedRule) (*PriceDef, string) {
	if rule != nil {
		return &PriceDef{Mode: rule.Mode, Config: rule.Config, Expression: rule.Expression}, core.PriceSourceGroup
	}
	return global, core.PriceSourceGlobal
}
