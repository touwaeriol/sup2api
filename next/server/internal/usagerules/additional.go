package usagerules

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/tidwall/gjson"
)

const MaxAdditionalItems = 128
const maxAdditionalTokens int64 = 1_000_000_000_000

// WithPrimaryModel must receive the host-admitted, account-mapped model,
// never an untrusted response's top-level model field.
func (u *Acc) WithPrimaryModel(model string) *Acc { u.primaryModel = model; return u }

func (u *Acc) Additional() []core.AdditionalUsage {
	var out []core.AdditionalUsage
	for _, rule := range u.rules.Additional {
		for _, item := range u.additional[rule.Name] {
			item.Metrics = core.CloneUsageMetrics(item.Metrics)
			out = append(out, item)
		}
	}
	return out
}

func (u *Acc) applyAdditional(event string, body []byte, stream bool) {
	for _, rule := range u.rules.Additional {
		path := rule.JSONPath
		if stream {
			if rule.SSEEvent != "" && rule.SSEEvent != event {
				continue
			}
			path = rule.SSEPath
		}
		if path == "" {
			continue
		}
		array := gjson.GetBytes(body, path)
		if !array.Exists() || array.Type == gjson.Null {
			continue
		}
		fail := func(err error) { u.AdditionalError = fmt.Sprintf("additional usage %s: %v", rule.Name, err) }
		if !array.IsArray() || len(array.Array()) > MaxAdditionalItems {
			fail(fmt.Errorf("invalid or oversized snapshot"))
			continue
		}
		var entries []core.AdditionalUsage
		valid := true
		for _, entry := range array.Array() {
			if entry.Get(rule.TypePath).String() != rule.TypeValue {
				continue
			}
			modelPath := rule.ModelPath
			if modelPath == "" && rule.UsePrimaryModel {
				modelPath = "model"
			}
			model := entry.Get(modelPath)
			if rule.UsePrimaryModel {
				if model.Exists() && (model.Type != gjson.String || model.Str != u.primaryModel) {
					fail(fmt.Errorf("response changed the admitted primary model"))
					valid = false
					break
				}
				model = gjson.Result{Type: gjson.String, Str: u.primaryModel}
			}
			if model.Type != gjson.String || len(model.Str) == 0 || !utf8.ValidString(model.Str) || strings.ContainsAny(model.Str, "\r\n\x00") {
				fail(fmt.Errorf("invalid model"))
				valid = false
				break
			}
			values := map[string]int64{}
			for field, path := range rule.Map {
				value := gjson.Get(entry.Raw, path)
				if !value.Exists() {
					if field == "input_tokens" || field == "output_tokens" {
						fail(fmt.Errorf("missing token counter %s", field))
						valid = false
						break
					}
					continue
				}
				n, err := strconv.ParseInt(value.Raw, 10, 64)
				if value.Type != gjson.Number || err != nil || n < 0 || n > maxAdditionalTokens {
					fail(fmt.Errorf("invalid token counter %s", field))
					valid = false
					break
				}
				values[field] = n
			}
			if !valid {
				break
			}
			if values["cache_creation_1h_tokens"] > values["cache_creation_tokens"] {
				fail(fmt.Errorf("cache creation subdivision exceeds total"))
				valid = false
				break
			}
			entries = append(entries, core.AdditionalUsage{Metrics: additionalCacheMetrics(rule.Map, entry), Kind: rule.Name, Model: model.Str, UsageSemantics: rule.Semantics, Tokens: Tokens(values["input_tokens"], values["output_tokens"], values["cache_read_tokens"], values["cache_creation_tokens"], values["cache_creation_1h_tokens"])})
		}
		if valid {
			if u.additional == nil {
				u.additional = map[string][]core.AdditionalUsage{}
			}
			u.additional[rule.Name] = entries
		}
	}
	if len(u.Additional()) > MaxAdditionalItems {
		u.AdditionalError = "additional usage total exceeds limit"
	}
}

func additionalCacheMetrics(mapping map[string]string, entry gjson.Result) map[string]any {
	if mapping["cache_creation_tokens"] != "cache_creation_input_tokens" || mapping["cache_creation_1h_tokens"] != "cache_creation.ephemeral_1h_input_tokens" {
		return nil
	}
	return iterationCacheMetrics(entry)
}
