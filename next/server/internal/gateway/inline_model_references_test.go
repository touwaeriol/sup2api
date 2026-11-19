package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/tidwall/gjson"
)

func inlineAdvisorBody(compacted bool) (map[string]any, string) {
	b := body(testModel, false)
	addition := map[string]any{"type": "tool_addition", "tool": map[string]any{"type": "tool_definition", "definition": map[string]any{"type": "advisor_20260301", "name": "advisor", "model": advisorModel}}}
	role, block, path := "system", addition, "messages.0.content.0.tool.definition.model"
	if compacted {
		role = "assistant"
		block = map[string]any{"type": "compaction", "content": "summary", "signature": "untouched-signature", "tool_changes": []any{addition}}
		path = "messages.0.content.0.tool_changes.0.tool.definition.model"
	}
	b["messages"] = []any{map[string]any{"role": role, "content": []any{block}}, map[string]any{"role": "user", "content": "continue"}}
	return b, path
}

func TestInlineAdvisorCoreAuthorizationPriceAndMapping(t *testing.T) {
	for _, compacted := range []bool{false, true} {
		label := "inline"
		if compacted {
			label = "signed-compaction"
		}
		for _, mode := range []string{"allowed", "group-denied", "missing-price", "account-selection", "mapping"} {
			t.Run(label+"/"+mode, func(t *testing.T) {
				e := pricedAdvisorEnv(t)
				b, path := inlineAdvisorBody(compacted)
				upModel := advisorModel
				switch mode {
				case "group-denied":
					e.auth.keys[testKey].Group.ModelAllowlist = []string{testModel}
				case "missing-price":
					delete(e.pricer.rules, advisorModel)
				case "account-selection":
					e.accounts.set(1, func(a *core.Account) { a.Models = []string{testModel} })
				case "mapping":
					upModel = "advisor-mapped"
					for _, id := range []int64{1, 2, 3} {
						e.accounts.set(id, func(a *core.Account) { a.ModelMapping = map[string]string{advisorModel: upModel} })
					}
				}
				fixture, _ := json.Marshal(map[string]any{"id": "inline-model-test", "type": "message", "role": "assistant", "model": testModel, "content": []any{}, "stop_reason": "end_turn", "usage": map[string]any{"input_tokens": 5, "output_tokens": 1, "iterations": []any{map[string]any{"type": "advisor_message", "model": upModel, "input_tokens": 7, "output_tokens": 2}}}})
				for _, key := range []string{"acc-1", "acc-2", "acc-3"} {
					e.up.set(key, &upstreamRule{status: 200, body: string(fixture)})
				}
				response := e.messages(b)
				r := e.record()
				rejected := mode == "group-denied" || mode == "missing-price" || mode == "mapping" && compacted
				if rejected {
					if response.status == 200 || len(e.up.keys()) > 0 {
						t.Fatalf("unauthorized inline invocation reached provider: %d %v", response.status, e.up.keys())
					}
					return
				}
				if response.status != 200 || !r.Success || r.BillingError != "" || len(r.Additional) != 1 || r.Additional[0].Price.ID != 19 || r.Additional[0].Model != advisorModel {
					t.Fatalf("inline accounting failed: %d %+v", response.status, r)
				}
				if mode == "account-selection" && strings.Join(e.up.keys(), ",") != "acc-2" {
					t.Fatal("selected account without advisor", e.up.keys())
				}
				if gjson.GetBytes(e.up.last().body, path).Str != upModel {
					t.Fatal("model mapping missing")
				}
				if compacted && gjson.GetBytes(e.up.last().body, "messages.0.content.0.signature").Str != "untouched-signature" {
					t.Fatal("signature changed")
				}
			})
		}
	}
}

func TestModelReferenceLocationsDoNotWalkToolData(t *testing.T) {
	e := pricedAdvisorEnv(t)
	e.auth.keys[testKey].Group.ModelAllowlist = []string{testModel}
	b := body(testModel, false)
	b["messages"] = []any{map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "t1", "name": "data", "input": map[string]any{"model": "not-permitted", "tool_changes": []any{map[string]any{"type": "advisor_20260301", "model": "not-permitted"}}}}}}, map[string]any{"role": "user", "content": "ordinary model: not-permitted"}}
	res := e.messages(b)
	if res.status != 200 {
		t.Fatalf("ordinary client tool data treated as model invocation: %d", res.status)
	}
	e.record()
}
