package core

import (
	"fmt"
	"maps"
	"strings"
)

// MaxUsageTokens is the most tokens of one kind a usage may count.
const MaxUsageTokens = 1_000_000_000_000

// ValidTokenCount reports whether n tokens of one kind is a usage that can
// be priced: 0 to MaxUsageTokens. It is the one place the bound is checked.
func ValidTokenCount[N int64 | float64](n N) bool {
	return n >= 0 && n <= MaxUsageTokens
}

func ValidatePricedUsage(item PricedUsage) error {
	if len(item.BillingReason) > 256 || strings.ContainsAny(item.BillingReason, "\r\n\x00") || item.Free && item.BillingReason == "" {
		return fmt.Errorf("invalid usage billing disposition")
	}
	if item.Kind == "" || len(item.Kind) > 64 || item.Model == "" || strings.ContainsAny(item.Model+item.Kind, "\r\n\x00") {
		return fmt.Errorf("invalid additional usage identity")
	}
	if item.UsageSemantics != "inclusive" && item.UsageSemantics != "exclusive" {
		return fmt.Errorf("invalid additional usage semantics")
	}
	if item.RateMultiplier.IsNegative() {
		return fmt.Errorf("invalid additional usage rate")
	}
	t := item.Tokens
	for _, n := range []int64{t.Input, t.Output, t.CacheRead, t.CacheCreation, t.CacheCreation1h, t.ImageInput, t.ImageOutput} {
		if !ValidTokenCount(n) {
			return fmt.Errorf("invalid additional token count")
		}
	}
	return nil
}

func ClonePricedUsage(items []PricedUsage) []PricedUsage {
	if items == nil {
		return nil
	}
	out := append([]PricedUsage(nil), items...)
	for i := range out {
		out[i].Price = out[i].Price.Clone()
		out[i].Metrics = CloneUsageMetrics(out[i].Metrics)
		out[i].PriceParams = maps.Clone(out[i].PriceParams)
		out[i].PriceHeaders = maps.Clone(out[i].PriceHeaders)
	}
	return out
}
