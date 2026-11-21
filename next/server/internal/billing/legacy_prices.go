package billing

import (
	"encoding/json"
	"sort"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
)

// Price modes before core migration 0122.
const (
	legacyPerRequest = "per_request"
	legacyPerToken   = "per_token"
)

// visualFromLegacy converts a price of a mode before core migration 0122 to
// a visual config, as that migration converts stored prices (CONTRACTS
// §131.6): per_request {price, rules?} becomes one request tier with the
// same rules; per_token tiers by max_len become tiers with "when": len <=
// max_len, ordered by max_len with the rest last; a flat per_token config
// (before 0121) becomes one token tier. The configs generate the expressions
// the old modes did. ok is false for any other mode, or a config that is not
// an object.
//
// Transitional: it serves upstream sup2api instances (sync) and databases
// (check-prices) older than 0122. Remove it once no upstream or database to
// upgrade predates that migration.
func visualFromLegacy(mode string, config json.RawMessage) (json.RawMessage, bool) {
	var cfg map[string]json.RawMessage
	if (mode != legacyPerRequest && mode != legacyPerToken) || json.Unmarshal(config, &cfg) != nil || cfg == nil {
		return nil, false
	}
	var tiers []map[string]json.RawMessage
	switch {
	case mode == legacyPerRequest:
		tiers = []map[string]json.RawMessage{{"name": json.RawMessage(`"base"`), "unit": json.RawMessage(`"request"`), "price": cfg["price"]}}
		delete(cfg, "price")
	case cfg["tiers"] == nil:
		tier := map[string]json.RawMessage{"name": json.RawMessage(`"base"`), "unit": json.RawMessage(`"token"`)}
		for _, k := range []string{"p", "c", "cr", "cc", "cc1h"} {
			if v, ok := cfg[k]; ok && string(v) != "null" {
				tier[k] = v
			}
		}
		tiers, cfg = []map[string]json.RawMessage{tier}, map[string]json.RawMessage{}
	default:
		if json.Unmarshal(cfg["tiers"], &tiers) != nil {
			return nil, false
		}
		whenByLen(tiers)
	}
	cfg["tiers"], _ = json.Marshal(tiers)
	if cfg["rules"] == nil {
		cfg["rules"] = json.RawMessage(`[]`)
	}
	out, err := json.Marshal(cfg)
	return out, err == nil
}

// whenByLen orders tiers by max_len, those without one last (the old
// generator's order), and turns each max_len into "when": len <= max_len.
func whenByLen(tiers []map[string]json.RawMessage) {
	maxLen := func(t map[string]json.RawMessage) (float64, bool) {
		var n expr.Num
		raw := t["max_len"]
		ok := raw != nil && string(raw) != "null" && json.Unmarshal(raw, &n) == nil
		return float64(n), ok
	}
	sort.SliceStable(tiers, func(i, j int) bool {
		a, aok := maxLen(tiers[i])
		b, bok := maxLen(tiers[j])
		if !aok || !bok {
			return aok && !bok
		}
		return a < b
	})
	for _, t := range tiers {
		if _, ok := maxLen(t); ok {
			t["when"], _ = json.Marshal(lenAtMost(t["max_len"]))
		}
		delete(t, "max_len")
	}
}

// lenAtMost is the tier condition len <= n.
func lenAtMost(n any) map[string]any {
	return map[string]any{"var": "len", "op": "<=", "value": n}
}
