package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

func fallbackHTTPBody(stream bool) map[string]any {
	b := body(testModel, stream)
	b["speed"] = "standard"
	b["output_config"] = map[string]any{"effort": "medium"}
	b["fallbacks"] = []any{map[string]any{"model": advisorModel, "speed": "fast", "max_tokens": 99, "output_config": nil}}
	return b
}

func TestFallbackReplacementGatewayUsageAndPriceSnapshots(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			e := pricedAdvisorEnv(t)
			e.pricer.params = []string{"model", "speed", "max_tokens", "output_config"}
			e.pricer.rules[advisorModel].Expression = `(p*7+c*11) * (u("speed") == "fast" ? 2 : 1)`
			lim := newFakeLimiter()
			e.gw.d.Limiter = lim
			e.accounts.set(1, func(a *core.Account) {
				a.ModelMapping = map[string]string{testModel: "up-primary", advisorModel: "up-fallback"}
			})
			usage := map[string]any{"input_tokens": 20, "output_tokens": 3, "speed": "fast", "iterations": []any{
				map[string]any{"type": "message", "model": "up-primary", "input_tokens": 10, "output_tokens": 0},
				map[string]any{"type": "fallback_message", "model": "up-fallback", "input_tokens": 20, "output_tokens": 3},
			}}
			boundary := map[string]any{"type": "fallback", "from": map[string]any{"model": "up-primary"}, "to": map[string]any{"model": "up-fallback"}, "trigger": map[string]any{"type": "refusal", "category": "cyber"}}
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !stream {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{"id": "msg-fallback", "type": "message", "model": "up-fallback", "role": "assistant", "content": []any{boundary}, "stop_reason": "end_turn", "usage": usage})
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				emit := func(typ string, event map[string]any) {
					event["type"] = typ
					raw, _ := json.Marshal(event)
					fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ, raw)
				}
				emit("message_start", map[string]any{"message": map[string]any{"id": "msg-fallback", "model": "up-primary", "usage": map[string]any{"input_tokens": 10, "output_tokens": 0}}})
				emit("content_block_start", map[string]any{"index": 0, "content_block": boundary})
				emit("content_block_stop", map[string]any{"index": 0})
				for range 2 {
					emit("message_delta", map[string]any{"delta": map[string]any{"stop_reason": "end_turn"}, "usage": usage})
				}
				emit("message_stop", map[string]any{})
			}))
			t.Cleanup(up.Close)
			e.plat.base = up.URL
			response := e.messages(fallbackHTTPBody(stream))
			rec := e.record()
			if response.status != 200 || !rec.Success || len(rec.Replacement) != 2 || len(rec.Additional) != 0 {
				t.Fatalf("fallback replacement failed: HTTP%d %+v", response.status, rec)
			}
			primary, fallback := rec.Replacement[0], rec.Replacement[1]
			if !primary.Free || fallback.Free || primary.Price.ID != 9 || fallback.Price.ID != 19 || primary.Model != testModel || fallback.Model != advisorModel || rec.UpstreamModel != "up-fallback" {
				t.Fatalf("wrong source identity/prices: %+v", rec.Replacement)
			}
			if primary.PriceParams["speed"] != `"standard"` || fallback.PriceParams["speed"] != `"fast"` || fallback.PriceParams["max_tokens"] != "99" || fallback.PriceParams["output_config"] != "null" || fallback.PriceParams["model"] != `"`+advisorModel+`"` {
				t.Fatalf("candidate overrides not frozen: %+v", rec.Replacement)
			}
			if rec.Tokens != (core.UsageTokens{Input: 20, Output: 3}) || lim.tokens[1] != 33 {
				t.Fatalf("final counters or attempt rate count doubled: %+v %v", rec.Tokens, lim.tokens)
			}
			if primary.Metrics["speed"] != nil || fallback.Metrics["speed"] != "fast" {
				t.Fatalf("final facts copied to wrong attempt: %+v", rec.Replacement)
			}
		})
	}
}

// fallbackResponse is a non-streamed fallback from primary to fallback: the
// primary refused before any output and the fallback answered.
func fallbackResponse(primary, fallback string) string {
	raw, _ := json.Marshal(map[string]any{"id": "msg-fallback", "type": "message", "role": "assistant", "model": fallback, "stop_reason": "end_turn",
		"content": []any{map[string]any{"type": "fallback", "from": map[string]any{"model": primary}, "to": map[string]any{"model": fallback}, "trigger": map[string]any{"type": "refusal", "category": "cyber"}}},
		"usage": map[string]any{"input_tokens": 20, "output_tokens": 3, "iterations": []any{
			map[string]any{"type": "message", "model": primary, "input_tokens": 10, "output_tokens": 0},
			map[string]any{"type": "fallback_message", "model": fallback, "input_tokens": 20, "output_tokens": 3},
		}}})
	return string(raw)
}

func TestAttemptPriceRequiresAttributedFacts(t *testing.T) {
	price := &core.PriceRule{Expression: `(p+c) * (u("speed") == "fast" ? 2 : 1)`}
	for _, tc := range []struct {
		name      string
		item      core.PricedUsage
		wantError bool
	}{
		{"missing", core.PricedUsage{Price: price}, true},
		{"null", core.PricedUsage{Price: price, Metrics: map[string]any{"speed": nil}}, true},
		{"request is not actual", core.PricedUsage{Price: price, PriceParams: map[string]string{"speed": `"fast"`}}, true},
		{"actual", core.PricedUsage{Price: price, Metrics: map[string]any{"speed": "fast"}}, false},
		{"proven free", core.PricedUsage{Price: price, Free: true, BillingReason: "refusal_before_output_free_cyber"}, false},
		{"free pricing policy", core.PricedUsage{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateAttemptPriceFacts(tc.item); (err != nil) != tc.wantError {
				t.Fatalf("price fact validation = %v", err)
			}
		})
	}
}

func TestFallbackGatewayAdmissionAndAttemptContract(t *testing.T) {
	for _, mode := range []string{"unauthorized", "missing-price", "missing-contract", "empty-contract-gate", "mapping-collision"} {
		t.Run(mode, func(t *testing.T) {
			e := pricedAdvisorEnv(t)
			switch mode {
			case "unauthorized":
				e.auth.keys[testKey].Group.ModelAllowlist = []string{testModel}
			case "missing-price":
				delete(e.pricer.rules, advisorModel)
			case "missing-contract":
				meterNothing(t, e, "anthropic", "anthropic.messages")
			case "empty-contract-gate":
				// The account's own attempt rule names no RequiredBy.
				meterNothing(t, e, "anthropic", "anthropic.messages")
				for i := range e.gen.accountTypes {
					for j := range e.gen.accountTypes[i].Type.Platforms {
						rules := e.gen.accountTypes[i].Type.Platforms[j].Usage["anthropic.messages"]
						rules.Attempts = &manifest.AttemptUsageRule{Name: "fallback", Adapter: manifest.AttemptAdapterAnthropicFallback}
						e.gen.accountTypes[i].Type.Platforms[j].Usage["anthropic.messages"] = rules
					}
				}
			case "mapping-collision":
				for _, id := range []int64{1, 2, 3} {
					e.accounts.set(id, func(a *core.Account) { a.ModelMapping = map[string]string{testModel: "same", advisorModel: "same"} })
				}
			}
			primary, fallback := testModel, advisorModel
			if mode == "mapping-collision" {
				primary, fallback = "same", "same"
			}
			e.up.set("acc-1", &upstreamRule{status: 200, body: fallbackResponse(primary, fallback)})
			res := e.messages(fallbackHTTPBody(false))
			rec := e.record()
			if mode == "unauthorized" || mode == "missing-price" {
				// The group's admission of the referenced model still holds.
				if res.status == 200 || len(e.up.keys()) > 0 || rec.Success {
					t.Fatalf("admission bypass: %s HTTP%d %+v", mode, res.status, rec)
				}
				return
			}
			if res.status != 200 || len(e.up.keys()) != 1 || !rec.Billable || rec.Price == nil || rec.Price.ID != 9 || rec.Tokens != (core.UsageTokens{Input: 20, Output: 3}) {
				t.Fatalf("%s: HTTP%d upstream %v %+v", mode, res.status, e.up.keys(), rec)
			}
			// The account's own attempt rule meters the chain without naming
			// RequiredBy: the host's declaration activates it. An account that
			// cannot meter it, or a chain whose attempts the mapping makes one
			// model, is billed on its top-level usage at the requested model.
			if want := map[bool]int{true: 2, false: 0}[mode == "empty-contract-gate"]; len(rec.Replacement) != want {
				t.Fatalf("%s: %d replacement items, want %d: %+v", mode, len(rec.Replacement), want, rec.Replacement)
			}
		})
	}
}

func TestFallbackUnknownModelBillsRequestedModel(t *testing.T) {
	e := pricedAdvisorEnv(t)
	fixture := `{"id":"msg-sticky","type":"message","role":"assistant","model":"undeclared","content":[],"stop_reason":"end_turn","usage":{"input_tokens":20,"output_tokens":3,"iterations":[{"type":"fallback_message","model":"undeclared","input_tokens":20,"output_tokens":3}]}}`
	e.up.set("acc-1", &upstreamRule{status: 200, body: fixture})
	res := e.messages(fallbackHTTPBody(false))
	rec := e.record()
	if res.status != 200 || !rec.Success || !rec.Billable || len(rec.Replacement) != 0 || rec.Price == nil || rec.Price.ID != 9 || rec.Tokens != (core.UsageTokens{Input: 20, Output: 3}) {
		t.Fatalf("unknown model not billed at the requested model: %d %+v", res.status, rec)
	}
}

// What a request asks to be metered apart is not part of scheduling: every
// account that serves the model and protocol is eligible.
func TestSchedulingIgnoresRequestedUsage(t *testing.T) {
	c, refs := schedulingCandidates(t, 3, false, noRules)
	c.route(&refs[0]).usage = manifest.UsageRules{Semantics: "exclusive"} // records no attempts
	b := body(testModel, false)
	b["fallbacks"] = []any{map[string]any{"model": advisorModel}}
	raw, _ := json.Marshal(b)
	c.setBody(raw)
	for i := range refs {
		if reason, detail, ok := c.ineligibleReason(&refs[i]); !ok {
			t.Fatalf("account %d left out for %s:%s", refs[i].ID, reason, detail)
		}
	}
}
