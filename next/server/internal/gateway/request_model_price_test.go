package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tidwall/gjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// A request is pre-charged and charged at the price of the model the client
// asked for (CONTRACTS §17): not at the account's mapping, not at the model
// the upstream echoes, though both have a price.
func TestBilledAtTheRequestedModel(t *testing.T) {
	for _, stream := range []bool{false, true} {
		e := newEnv(t)
		for id, model := range map[int64]string{30: "up-mapped", 31: upModel} {
			e.pricer.rules[model] = &core.PriceRule{ID: id, Model: model, Mode: "visual", Expression: "p*7", ExprHash: model}
		}
		e.accounts.set(1, func(a *core.Account) { a.ModelMapping = map[string]string{testModel: "up-mapped"} })
		if r := e.messages(body(testModel, stream)); r.status != 200 {
			t.Fatalf("stream=%v: %d %s", stream, r.status, r.body)
		}
		if sent := gjson.GetBytes(e.up.last().body, "model").String(); sent != "up-mapped" {
			t.Fatalf("upstream model %q", sent)
		}
		rec := e.record()
		if len(e.balance.precharged) != 1 || e.balance.precharged[0] == nil || e.balance.precharged[0].ID != 9 {
			t.Fatalf("stream=%v: pre-charged at %+v, want the requested model's price", stream, e.balance.precharged)
		}
		if !rec.Billable || rec.Price == nil || rec.Price.ID != 9 || rec.Model != testModel {
			t.Fatalf("stream=%v: charged at %+v (billable %v, model %q)", stream, rec.Price, rec.Billable, rec.Model)
		}
	}
}

// imageToolUpstream answers a Responses request the way the upstreams
// measured on 2026-11-18 do when the image_generation tool ran: the
// conversation's usage, and beside it the image tool's under
// tool_usage.image_gen (counters all 0 when image is false), with tools
// echoing an image model. A stream ends in the terminal event named event.
func imageToolUpstream(t *testing.T, event string, image bool) *httptest.Server {
	in, out := 39, 515
	if !image {
		in, out = 0, 0
	}
	response := map[string]any{"id": "resp_1", "object": "response", "model": "gpt-up", "status": "completed", "output": []any{},
		"tools": []any{map[string]any{"type": "image_generation", "model": "gpt-image-2-codex"}},
		"usage": map[string]any{"input_tokens": 2304, "output_tokens": 52, "input_tokens_details": map[string]any{"cached_tokens": 0}},
		"tool_usage": map[string]any{"image_gen": map[string]any{
			"input_tokens": in, "input_tokens_details": map[string]any{"text_tokens": in, "image_tokens": 0},
			"output_tokens": out, "output_tokens_details": map[string]any{"text_tokens": 0, "image_tokens": out}, "total_tokens": in + out}}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if !gjson.GetBytes(raw, "stream").Bool() {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}
		if event == "response.incomplete" {
			response["status"] = "incomplete"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		b, _ := json.Marshal(map[string]any{"type": event, "response": response})
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The image tool's tokens are the request's own usage, priced at the
// request's one price: input and output include them, the image tokens
// among them are img / img_o. The image model the response echoes has a
// price and is not used. A tool usage whose counters are all 0 adds nothing.
func TestImageGenerationToolIsTheRequestsUsage(t *testing.T) {
	withImage := core.UsageTokens{Input: 2304 + 39, Output: 52 + 515, ImageOutput: 515}
	for name, tc := range map[string]struct {
		stream bool
		event  string
		image  bool
		want   core.UsageTokens
	}{
		"json":                {image: true, want: withImage},
		"completed":           {stream: true, event: "response.completed", image: true, want: withImage},
		"incomplete":          {stream: true, event: "response.incomplete", image: true, want: withImage},
		"zero tool usage":     {image: false, want: core.UsageTokens{Input: 2304, Output: 52}},
		"zero tool usage sse": {stream: true, event: "response.completed", image: false, want: core.UsageTokens{Input: 2304, Output: 52}},
	} {
		srv := imageToolUpstream(t, tc.event, tc.image)
		plat := &fakePlatform{route: func(*pluginv1.ExecuteRequest) string { return srv.URL + "/v1/responses" }}
		e := newEnv(t)
		e.addAccountType("oai", "apikey", plat, manifest.AccountPlatform{Platform: manifest.PlatformOpenAI})
		e.accounts.addTyped(testGroup, 11, 200, "oai-11", "oai", "apikey")
		e.pricer.rules["gpt-5.6"] = &core.PriceRule{ID: 20, Model: "gpt-5.6", Mode: "expression", Expression: "p*1.25 + img_o*30 + c*10", ExprHash: "chat"}
		e.pricer.rules["gpt-image-2-codex"] = &core.PriceRule{ID: 21, Model: "gpt-image-2-codex", Mode: "expression", Expression: "p*5", ExprHash: "img"}
		in := map[string]any{"model": "gpt-5.6", "stream": tc.stream, "input": "draw a fox", "tools": []any{map[string]any{"type": "image_generation"}}}
		if r := e.post("/v1/responses", in, bearer()); r.status != 200 {
			t.Fatalf("%s: %d %s", name, r.status, r.body)
		}
		rec := e.record()
		if !rec.Billable || rec.Price == nil || rec.Price.ID != 20 || len(rec.Additional) != 0 {
			t.Fatalf("%s: record %+v", name, rec)
		}
		if rec.Tokens != tc.want {
			t.Fatalf("%s: tokens %+v, want %+v", name, rec.Tokens, tc.want)
		}
	}
}

// A WebSocket turn reads the image tool's tokens the same way, from
// response.completed and from a response.failed that carries them.
func TestImageGenerationToolWebSocketTurn(t *testing.T) {
	e := newWSEnv(t)
	e.pricer.rules["gpt-image-2-codex"] = &core.PriceRule{ID: 21, Model: "gpt-image-2-codex", Mode: "visual", Expression: "p*5"}
	c := e.mustDial()
	turn := create(wsModel, "image")
	turn["tools"] = []any{map[string]any{"type": "image_generation"}}
	c.send(turn)
	c.until("response.completed")
	r := e.record()
	if !r.Success || !r.Billable || r.Price == nil || r.Price.ID != 10 || len(r.Additional) != 0 ||
		r.Tokens != (core.UsageTokens{Input: wsInput + 39, Output: wsOutput + 515, CacheRead: wsCacheRead, ImageOutput: 515}) {
		t.Fatalf("image turn: %+v", r)
	}
	failed := create(wsModel, "image-fail")
	failed["tools"] = []any{map[string]any{"type": "image_generation"}}
	c.send(failed)
	c.until("response.failed")
	r = e.record()
	if r.Price == nil || r.Price.ID != 10 || len(r.Additional) != 0 ||
		r.Tokens != (core.UsageTokens{Input: wsInput + 39, Output: wsOutput + 515, CacheRead: wsCacheRead, ImageOutput: 515}) {
		t.Fatalf("failed image turn: %+v", r)
	}
}
