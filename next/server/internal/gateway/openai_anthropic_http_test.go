package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/tidwall/gjson"
)

func openAIConversionInput(protocol string, stream bool) (string, map[string]any) {
	b := map[string]any{"model": testModel, "stream": stream}
	if protocol == "openai.chat" {
		b["max_completion_tokens"] = 37
		b["messages"] = []any{map[string]any{"role": "system", "content": "keep this system"}, map[string]any{"role": "user", "content": "hello"}}
		if stream {
			b["stream_options"] = map[string]any{"include_usage": true}
		}
		return "/v1/chat/completions", b
	}
	b["max_output_tokens"], b["input"], b["instructions"] = 37, "hello", "keep this system"
	return "/v1/responses", b
}

func conversionFixture(w http.ResponseWriter, stream bool, reason string, truncate bool) {
	usage := map[string]any{"input_tokens": 10, "output_tokens": 4, "cache_read_input_tokens": 20, "cache_creation_input_tokens": 30}
	message := map[string]any{"id": "msg-converted", "type": "message", "role": "assistant", "model": "mapped-upstream",
		"content": []any{map[string]any{"type": "text", "text": "hello"}}, "stop_reason": reason, "stop_sequence": nil, "usage": usage}
	if !stream {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(message)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	emit := func(typ string, body map[string]any) {
		body["type"] = typ
		raw, _ := json.Marshal(body)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ, raw)
		w.(http.Flusher).Flush()
	}
	message["content"], message["stop_reason"] = []any{}, nil
	emit("message_start", map[string]any{"message": message})
	emit("content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
	for _, delta := range []string{"he", "llo"} {
		emit("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "text_delta", "text": delta}})
	}
	// message_delta carries the stop reason, the end of an Anthropic reply
	// (CONVERT-MATRIX §3.1): a truncated stream stops before it.
	if truncate {
		return
	}
	emit("content_block_stop", map[string]any{"index": 0})
	emit("message_delta", map[string]any{"delta": map[string]any{"stop_reason": reason, "stop_sequence": nil}, "usage": usage})
	emit("message_stop", map[string]any{})
}

func TestOpenAIAnthropicHTTPConversion(t *testing.T) {
	for _, protocol := range []string{"openai.chat", "openai.responses"} {
		for _, stream := range []bool{false, true} {
			for _, reason := range []string{"end_turn", "refusal", "max_tokens"} {
				t.Run(fmt.Sprintf("%s/%t/%s", protocol, stream, reason), func(t *testing.T) {
					var mu sync.Mutex
					var wire []byte
					up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						raw, _ := io.ReadAll(r.Body)
						mu.Lock()
						wire = raw
						mu.Unlock()
						conversionFixture(w, stream, reason, false)
					}))
					t.Cleanup(up.Close)
					e := newEnv(t, openAIClients, func(e *env) { e.plat.base = up.URL })
					e.accounts.set(1, func(a *core.Account) { a.ModelMapping = map[string]string{testModel: "mapped-upstream"} })
					path, input := openAIConversionInput(protocol, stream)
					res := e.do(path, input, map[string]string{"authorization": "Bearer " + testKey})
					if res.status != 200 {
						t.Fatalf("HTTP%d %s", res.status, res.body)
					}
					mu.Lock()
					actual := gjson.ParseBytes(wire)
					mu.Unlock()
					if actual.Get("model").String() != "mapped-upstream" || actual.Get("max_tokens").Int() != 37 ||
						actual.Get("stream").Bool() != stream || actual.Get("thinking").Exists() || actual.Get("output_config.effort").Exists() ||
						!strings.Contains(actual.Get("system").Raw, "keep this system") {
						t.Fatalf("conversion or model mapping changed request: %s", actual.Raw)
					}
					// A refusal is a content filter finish; its text stays text
					// (CONVERT-MATRIX §9.5).
					if !stream && reason == "refusal" {
						field, want := "choices.0.finish_reason", "content_filter"
						if protocol == "openai.responses" {
							field = "incomplete_details.reason"
						}
						if res.json().Get(field).String() != want {
							t.Fatalf("refusal lost: %s", res.body)
						}
					}
					if stream {
						terminal := "data: [DONE]"
						if protocol == "openai.responses" {
							terminal = "event: response.completed"
							if reason != "end_turn" {
								terminal = "event: response.incomplete"
							}
						}
						if strings.Count(string(res.body), terminal) != 1 {
							t.Fatalf("bad terminal: %s", res.body)
						}
					}
					rec := e.record()
					if !rec.Success || rec.Protocol != protocol || rec.UpstreamProtocol != "anthropic.messages" || rec.Model != testModel ||
						rec.UpstreamModel != "mapped-upstream" || rec.Tokens != (core.UsageTokens{Input: 10, Output: 4, CacheRead: 20, CacheCreation: 30}) {
						t.Fatalf("source usage/model accounting changed: %+v", rec)
					}
				})
			}
		}
	}
}

// An unknown member is an "other" loss: allowed by default, refused before
// any provider under the strict policy (CONVERT-MATRIX D1, §2.4).
func TestOpenAIConversionRejectsSemanticLossBeforeProvider(t *testing.T) {
	for _, protocol := range []string{"openai.chat", "openai.responses"} {
		e := newEnv(t, openAIClients)
		path, input := openAIConversionInput(protocol, false)
		input["future_generation_feature"] = true
		if res := e.do(path, input, map[string]string{"authorization": "Bearer " + testKey}); res.status != 200 {
			t.Fatalf("allow: %d %s", res.status, res.body)
		}
		if rec := e.record(); rec.Conversion == nil || len(rec.Conversion.Lost["other"]) == 0 {
			t.Fatalf("loss not recorded: %+v", rec.Conversion)
		}
		e.auth.keys[testKey].Group.ConversionLossPolicy = core.LossPolicyStrict
		builds := e.plat.buildCount()
		res := e.do(path, input, map[string]string{"authorization": "Bearer " + testKey})
		if res.status != 400 || res.json().Get("error.code").String() != "unsupported_conversion" || e.plat.buildCount() != builds {
			t.Fatalf("strict: unknown generation requirement silently ignored: %d %s", res.status, res.body)
		}
		if rec := e.record(); rec.Attempts != 0 || rec.Success {
			t.Fatalf("admission retried: %+v", rec)
		}
	}
}

func TestOpenAIConvertedStreamEOFDoesNotInventSuccess(t *testing.T) {
	for _, protocol := range []string{"openai.chat", "openai.responses"} {
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { conversionFixture(w, true, "end_turn", true) }))
		t.Cleanup(up.Close)
		e := newEnv(t, openAIClients, func(e *env) { e.plat.base = up.URL })
		path, input := openAIConversionInput(protocol, true)
		res := e.do(path, input, map[string]string{"authorization": "Bearer " + testKey})
		if res.status != 200 || strings.Contains(string(res.body), "data: [DONE]") || strings.Contains(string(res.body), "event: response.completed") {
			t.Fatalf("unexpected success for incomplete source: %d %s", res.status, res.body)
		}
		if rec := e.record(); rec.Success || rec.ErrorType != errTypeUpstream || rec.Tokens.Input != 10 {
			t.Fatalf("broken stream accounting: %+v", rec)
		}
	}
}
