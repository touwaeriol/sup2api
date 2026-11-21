package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

const advisorModel = "claude-opus-5-5"

func advisorBody(stream bool) map[string]any {
	b := body(testModel, stream)
	b["tools"] = []any{map[string]any{"type": "advisor_20260301", "name": "advisor", "model": advisorModel}}
	return b
}

func pricedAdvisorEnv(t *testing.T) *env {
	return newEnv(t, func(e *env) {
		e.pricer.rules[advisorModel] = &core.PriceRule{ID: 19, Model: advisorModel, Mode: "per_token", Expression: "p*7+c*11", ExprHash: "advisor-price"}
	})
}

func TestReferencedModelAdmissionAndMapping(t *testing.T) {
	t.Run("group denies nested model", func(t *testing.T) {
		e := pricedAdvisorEnv(t)
		e.auth.keys[testKey].Group.ModelAllowlist = []string{testModel}
		r := e.messages(advisorBody(false))
		if r.status != 404 || len(e.up.keys()) != 0 || e.record().Billable {
			t.Fatalf("nested unauthorized model admitted: %d %s", r.status, r.body)
		}
	})
	t.Run("missing nested price", func(t *testing.T) {
		e := newEnv(t)
		r := e.messages(advisorBody(false))
		if r.status != 403 || len(e.up.keys()) != 0 || e.record().Billable {
			t.Fatalf("unpriced model admitted: %d %s", r.status, r.body)
		}
	})
	t.Run("filter and map both models", func(t *testing.T) {
		e := pricedAdvisorEnv(t)
		e.accounts.set(1, func(a *core.Account) { a.Models = []string{testModel} })
		e.accounts.set(2, func(a *core.Account) {
			a.Models = []string{testModel, advisorModel}
			a.ModelMapping = map[string]string{advisorModel: "advisor-upstream"}
		})
		r := e.messages(advisorBody(false))
		if r.status != 200 || strings.Join(e.up.keys(), ",") != "acc-2" {
			t.Fatalf("wrong scheduling %d %s %v", r.status, r.body, e.up.keys())
		}
		if gjson.GetBytes(e.up.last().body, "tools.0.model").String() != "advisor-upstream" {
			t.Fatal("nested model was not mapped")
		}
		if rec := e.record(); rec.Model != testModel || rec.Price.ID != 9 {
			t.Fatal("primary model pricing changed")
		}
	})
	t.Run("hook cannot bypass group", func(t *testing.T) {
		e := pricedAdvisorEnv(t)
		e.auth.keys[testKey].Group.ModelAllowlist = []string{testModel, advisorModel}
		h := &fakeHook{fn: func(context.Context, *pluginv1.GatewayRequestHookRequest) (*pluginv1.GatewayRequestHookResponse, error) {
			return &pluginv1.GatewayRequestHookResponse{Decision: pluginv1.GatewayRequestHookResponse_DECISION_ALLOW,
				Patches: []*pluginv1.BodyPatch{{Op: pluginv1.BodyPatch_OP_SET, Path: "tools", ValueJson: `[{"type":"advisor_20260301","name":"advisor","model":"not-allowed"}]`}}}, nil
		}}
		hk := guardHook(300, "closed")
		hk.Needs = []string{"tools"}
		e.addHook(hk, []string{"tools"}, h)
		r := e.messages(advisorBody(false))
		if r.status != 404 || len(e.up.keys()) != 0 {
			t.Fatalf("hook model escaped admission: %d %s", r.status, r.body)
		}
		e.record()
	})
}

func TestReferencedUsageJSONAndSSE(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			e := pricedAdvisorEnv(t)
			lim := newFakeLimiter()
			e.gw.d.Limiter = lim
			e.accounts.set(1, func(a *core.Account) { a.ModelMapping = map[string]string{advisorModel: "advisor-upstream"} })
			fixture := map[string]any{"id": "msg_advisor", "model": testModel, "type": "message", "role": "assistant",
				"content": []any{map[string]any{"type": "text", "text": "ok"}}, "stop_reason": "end_turn",
				"usage": map[string]any{"input_tokens": 1760, "output_tokens": 531, "cache_read_input_tokens": 412,
					"iterations": []any{
						map[string]any{"type": "message", "input_tokens": 412, "output_tokens": 89},
						map[string]any{"type": "advisor_message", "model": "advisor-upstream", "input_tokens": 823, "output_tokens": 1612},
						map[string]any{"type": "message", "input_tokens": 1348, "output_tokens": 442},
					}}}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				incoming, _ := io.ReadAll(r.Body)
				if gjson.GetBytes(incoming, "tools.0.model").String() != "advisor-upstream" {
					t.Error("wrong nested model sent")
				}
				if !stream {
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(fixture)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\""+testModel+"\",\"usage\":{\"input_tokens\":412,\"output_tokens\":0}}}\n\n")
				data, _ := json.Marshal(map[string]any{"type": "message_delta", "usage": fixture["usage"]})
				for range 2 {
					fmt.Fprintf(w, "event: message_delta\ndata: %s\n\n", data)
				}
				fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			}))
			t.Cleanup(srv.Close)
			e.plat.base = srv.URL
			res := e.messages(advisorBody(stream))
			if res.status != 200 {
				t.Fatalf("HTTP%d %s", res.status, res.body)
			}
			rec := e.record()
			if rec.Tokens != (core.UsageTokens{Input: 1760, Output: 531, CacheRead: 412}) || len(rec.Additional) != 1 {
				t.Fatalf("wrong executor/additional split: %+v", rec)
			}
			item := rec.Additional[0]
			if item.Model != advisorModel || item.Kind != "advisor" || item.Price.ID != 19 || item.Price.Expression != "p*7+c*11" || item.Tokens != (core.UsageTokens{Input: 823, Output: 1612}) || item.RateMultiplier.String() != "1.5" {
				t.Fatalf("bad nested price snapshot %+v", item)
			}
			// Rate limits count the input outside any cache plus the output: the
			// executor's 412 cache-read tokens stay out.
			if lim.tokens[1] != 1760+531+823+1612 {
				t.Fatal("rate limit omitted or double counted advisor", lim.tokens)
			}
		})
	}
}

func TestReferencedUsageDoesNotInventPrice(t *testing.T) {
	for _, raw := range []string{
		`{"type":"advisor_message","model":"unrequested-model","input_tokens":3,"output_tokens":4}`,
		`{"type":"advisor_message","model":"claude-opus-5-5","input_tokens":3,"output_tokens":-1}`,
	} {
		e := pricedAdvisorEnv(t)
		e.up.set("acc-1", &upstreamRule{status: 200, body: `{"type":"message","role":"assistant","model":"` + testModel + `","content":[],"usage":{"input_tokens":1,"output_tokens":0,"iterations":[` + raw + `]}}`})
		res := e.messages(advisorBody(false))
		rec := e.record()
		if res.status != 200 || !rec.Success || !rec.Billable || len(rec.Additional) != 0 || rec.Price == nil || rec.Tokens != (core.UsageTokens{Input: 1}) {
			t.Fatalf("usage that cannot be priced is billed on the top-level usage at the requested model: %+v", rec)
		}
	}
}
