package usagerules

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/tidwall/gjson"
)

func mergeUsageSnapshot(before, after gjson.Result) gjson.Result {
	if !after.Exists() {
		return before
	}
	if !after.IsObject() {
		return after
	}
	m := map[string]json.RawMessage{}
	if before.IsObject() {
		before.ForEach(func(k, v gjson.Result) bool { m[k.Str] = json.RawMessage(v.Raw); return true })
	}
	after.ForEach(func(k, v gjson.Result) bool { m[k.Str] = json.RawMessage(v.Raw); return true })
	raw, _ := json.Marshal(m)
	return gjson.ParseBytes(raw)
}

type attemptGroup struct {
	model      string
	start, end int
}

// attemptChain tracks the contiguous runs of one model in the sampling
// iterations, rejecting a model that reappears after a handoff.
type attemptChain struct {
	groups       []attemptGroup
	fallbackSeen bool
}

func (c *attemptChain) add(model string, i int) error {
	if len(c.groups) == 0 || c.groups[len(c.groups)-1].model != model {
		for _, g := range c.groups {
			if g.model == model {
				return fmt.Errorf("fallback model reappeared in attempt chain")
			}
		}
		c.groups = append(c.groups, attemptGroup{model: model, start: i, end: i})
		return nil
	}
	c.groups[len(c.groups)-1].end = i
	return nil
}

// attemptEntryModel resolves the model of one sampling iteration. An absent
// model may default to the primary only when the response has no fallback
// boundary at all; anything else must carry a valid identity.
func attemptEntryModel(entry gjson.Result, primary string, defaultPrimary bool) (string, error) {
	model := entry.Get("model")
	if (!model.Exists() || model.Type == gjson.Null) && defaultPrimary {
		model = gjson.Result{Type: gjson.String, Str: primary}
	}
	if model.Type != gjson.String || model.Str == "" || !utf8.ValidString(model.Str) || strings.ContainsAny(model.Str, "\r\n\x00") {
		return "", fmt.Errorf("missing or invalid attempt model")
	}
	return model.Str, nil
}

func (m *attemptMeter) anthropicFallback(kind, primary string) ([]core.AttemptUsage, error) {
	if !m.usage.IsObject() || m.reason == "" {
		return nil, fmt.Errorf("attempt response lacks usage/stop reason")
	}
	if len(m.boundaries) > 3 {
		return nil, fmt.Errorf("too many fallback transitions")
	}
	array := m.usage.Get("iterations")
	var entries []gjson.Result
	if array.Exists() && array.Type != gjson.Null {
		if !array.IsArray() || len(array.Array()) > 128 {
			return nil, fmt.Errorf("invalid attempt usage snapshot")
		}
		entries = array.Array()
	}
	var selected []gjson.Result
	for _, entry := range entries {
		switch entry.Get("type").Str {
		case "message", "fallback_message":
			selected = append(selected, entry)
		case "advisor_message": // Independently metered by Additional rules.
		case "compaction":
			return nil, fmt.Errorf("fallback compaction lacks an admitted per-attempt metering contract")
		default:
			return nil, fmt.Errorf("unknown iteration type in attempt usage")
		}
	}
	if len(selected) == 0 {
		if len(m.boundaries) > 0 || m.model != primary {
			return nil, fmt.Errorf("fallback served without per-attempt usage")
		}
		// No model handoff: a normal single primary completion may omit iterations.
		tokens, err := attemptTokens(m.usage)
		if err != nil {
			return nil, err
		}
		item := core.AttemptUsage{Kind: kind, Model: primary, Metrics: iterationCacheMetrics(m.usage), UsageSemantics: "exclusive", Tokens: tokens, BillingReason: "completed_attempt"}
		if m.reason == "refusal" {
			if err = refusalDisposition(&item, m.details); err != nil {
				return nil, err
			}
		}
		return []core.AttemptUsage{item}, nil
	}
	items := make([]core.AttemptUsage, 0, len(selected))
	chain := attemptChain{}
	defaultPrimary := len(m.boundaries) == 0 && !hasFallbackTerminal(selected)
	for i, entry := range selected {
		model, err := attemptEntryModel(entry, primary, defaultPrimary)
		if err != nil {
			return nil, err
		}
		tokens, err := attemptTokens(entry)
		if err != nil {
			return nil, err
		}
		items = append(items, core.AttemptUsage{Kind: kind, Model: model, Metrics: iterationCacheMetrics(entry), UsageSemantics: "exclusive", Tokens: tokens, BillingReason: "completed_attempt"})
		if err := chain.add(model, i); err != nil {
			return nil, err
		}
		if entry.Get("type").Str == "fallback_message" {
			if chain.fallbackSeen || i != len(selected)-1 {
				return nil, fmt.Errorf("fallback terminal must be final sampling iteration")
			}
			chain.fallbackSeen = true
		}
	}
	groups, fallbackSeen := chain.groups, chain.fallbackSeen

	if len(groups) > 4 || len(m.boundaries) != len(groups)-1 {
		return nil, fmt.Errorf("fallback transition/usage groups disagree")
	}
	if len(groups) > 1 && !fallbackSeen {
		return nil, fmt.Errorf("fallback response lacks terminal iteration")
	}
	if !fallbackSeen && groups[0].model != primary {
		return nil, fmt.Errorf("nonfallback attempt changed primary model")
	}
	for i, b := range m.boundaries {
		if b.Get("from.model").Str != groups[i].model || b.Get("to.model").Str != groups[i+1].model {
			return nil, fmt.Errorf("fallback transition identities do not match attempts")
		}
		if err := refusalDisposition(&items[groups[i].end], b.Get("trigger")); err != nil {
			return nil, err
		}
	}
	if m.reason == "refusal" {
		if err := refusalDisposition(&items[len(items)-1], m.details); err != nil {
			return nil, err
		}
	}
	// A streamed start can retain the primary model after a mid-output handoff.
	// The final usage identity, validated against boundaries above, is authoritative.
	if m.model == "" || m.model != groups[0].model && m.model != items[len(items)-1].Model {
		return nil, fmt.Errorf("response model conflicts with attempts")
	}
	return items, nil
}
func hasFallbackTerminal(entries []gjson.Result) bool {
	for _, e := range entries {
		if e.Get("type").Str == "fallback_message" {
			return true
		}
	}
	return false
}
func attemptTokens(v gjson.Result) (core.UsageTokens, error) {
	fields := []string{"input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens", "cache_creation.ephemeral_1h_input_tokens"}
	values := make([]int64, len(fields))
	for i, path := range fields {
		value := v.Get(path)
		if !value.Exists() || value.Type == gjson.Null {
			if i < 2 {
				return core.UsageTokens{}, fmt.Errorf("missing attempt token counter")
			}
			continue
		}
		n, e := strconv.ParseInt(value.Raw, 10, 64)
		if value.Type != gjson.Number || e != nil || n < 0 || n > 1e12 {
			return core.UsageTokens{}, fmt.Errorf("invalid attempt token counter")
		}
		values[i] = n
	}
	if values[4] > values[3] {
		return core.UsageTokens{}, fmt.Errorf("attempt cache subdivision exceeds total")
	}
	return Tokens(values[0], values[1], values[2], values[3], values[4]), nil
}
func refusalDisposition(item *core.AttemptUsage, details gjson.Result) error {
	if item.Tokens.Output > 0 {
		item.BillingReason = "refusal_after_output"
		return nil
	}
	if !details.IsObject() || details.Get("type").Str != "refusal" {
		return fmt.Errorf("zero-output refusal lacks classification evidence")
	}
	category := details.Get("category")
	if !category.Exists() {
		return fmt.Errorf("zero-output refusal lacks category")
	}
	if category.Type == gjson.Null {
		item.Free = true
		item.BillingReason = "refusal_before_output_unclassified"
		return nil
	}
	if category.Type != gjson.String {
		return fmt.Errorf("invalid refusal category")
	}
	switch category.Str {
	case "bio", "frontier_llm", "reasoning_extraction":
		item.BillingReason = "refusal_before_output_billed_" + category.Str
	case "cyber", "general_harms":
		item.Free = true
		item.BillingReason = "refusal_before_output_free_" + category.Str
	default:
		return fmt.Errorf("unknown refusal billing category")
	}
	return nil
}
