package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/gateway/convert"
)

// ---------------------------------------------------------------- stream-only upstream (CONTRACTS §83)

const (
	soInput  = 12
	soOutput = 5
	soCached = 3
)

// responsesUpstream is a Responses API upstream. With streamOnly it refuses
// non-streaming requests like the ChatGPT backends do.
type responsesUpstream struct {
	srv *httptest.Server
	mu  sync.Mutex
	// bodies are the request bodies received, in order.
	bodies     [][]byte
	streamOnly bool
	// emptyOutput sends response.completed with "output": [] and two
	// output_item.done events out of order.
	emptyOutput bool
	// terminal replaces the last event: "incomplete", "failed", "error429",
	// "error_flat", "none" (the stream just ends).
	terminal string
	// linger, when set, keeps the stream open after the last event until the
	// request is canceled, which it is then told.
	linger chan struct{}
	// early streams of the next ones end right after response.created, with
	// earlyEvent when it is set (streamhead_test.go).
	early      int
	earlyEvent map[string]any
}

func newResponsesUpstream(t *testing.T) *responsesUpstream {
	u := &responsesUpstream{streamOnly: true}
	u.srv = httptest.NewServer(http.HandlerFunc(u.handle))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *responsesUpstream) last() []byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.bodies[len(u.bodies)-1]
}

func soMessage(id, text string) map[string]any {
	return map[string]any{"id": id, "type": "message", "role": "assistant", "status": "completed",
		"content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}}}
}

func soResponse(status string, output []any) map[string]any {
	return map[string]any{"id": "resp_1", "object": "response", "model": "gpt-up", "status": status, "output": output,
		"usage": map[string]any{"input_tokens": soInput, "output_tokens": soOutput, "total_tokens": soInput + soOutput,
			"input_tokens_details": map[string]any{"cached_tokens": soCached}}}
}

func (u *responsesUpstream) handle(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	u.mu.Lock()
	u.bodies = append(u.bodies, raw)
	streamOnly, empty, terminal, linger := u.streamOnly, u.emptyOutput, u.terminal, u.linger
	early, earlyEvent := u.early > 0 && gjson.GetBytes(raw, "stream").Bool(), u.earlyEvent
	if early {
		u.early--
	}
	u.mu.Unlock()
	if linger != nil {
		defer func() {
			<-r.Context().Done()
			linger <- struct{}{}
		}()
	}
	if !gjson.GetBytes(raw, "stream").Bool() {
		if streamOnly {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"error":{"message":"Stream must be set to true","type":"invalid_request_error"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(soResponse("completed", []any{soMessage("msg_1", "hello")}))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(v map[string]any) {
		b, _ := json.Marshal(v)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", v["type"], b)
		w.(http.Flusher).Flush()
	}
	send(map[string]any{"type": "response.created", "response": soResponse("in_progress", []any{})})
	if early {
		// What the ChatGPT backends send before an overload error.
		send(map[string]any{"type": "response.in_progress", "response": soResponse("in_progress", []any{})})
		send(map[string]any{"type": "keepalive"})
		send(map[string]any{"type": "response.metadata"})
		if earlyEvent != nil {
			send(earlyEvent)
		}
		return
	}
	send(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"id": "msg_1", "type": "message", "role": "assistant", "content": []any{}}})
	send(map[string]any{"type": "response.output_text.delta", "output_index": 0, "content_index": 0, "delta": "hello"})
	output := []any{soMessage("msg_1", "hello")}
	if empty {
		// Out of order on purpose: output_index decides the position.
		send(map[string]any{"type": "response.output_item.done", "output_index": 1, "item": soMessage("msg_2", "second")})
		send(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": soMessage("msg_1", "hello")})
		output = []any{}
	} else {
		send(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": soMessage("msg_1", "hello")})
	}
	switch terminal {
	case "none":
	case "incomplete":
		resp := soResponse("incomplete", output)
		resp["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
		send(map[string]any{"type": "response.incomplete", "response": resp})
	case "failed":
		resp := soResponse("failed", output)
		resp["error"] = map[string]any{"code": "server_error", "message": "the model melted"}
		send(map[string]any{"type": "response.failed", "response": resp})
	case "error429":
		send(map[string]any{"type": "error", "status": 429, "error": map[string]any{"type": "usage_limit_reached", "message": "limit reached", "resets_at": 1999999999}})
	case "error_flat":
		send(map[string]any{"type": "error", "code": "bad_thing", "message": "flat error", "param": nil, "sequence_number": 4})
	default:
		send(map[string]any{"type": "response.completed", "response": soResponse("completed", output)})
	}
}

// streamOnlyEnv adds the "codexlike/chatgpt" account type serving the
// built-in openai responses endpoint, account 10 of the test group, and
// returns the type's plugin and upstream. sp is the type's openai entry.
func streamOnlyEnv(t *testing.T, sp manifest.AccountPlatform, opts ...envOpt) (*env, *fakePlatform, *responsesUpstream) {
	t.Helper()
	up := newResponsesUpstream(t)
	e := newEnv(t, opts...)
	plat := &fakePlatform{route: func(*pluginv1.ExecuteRequest) string { return up.srv.URL + "/v1/responses" }}
	e.addAccountType("codexlike", "chatgpt", plat, sp)
	e.accounts.addTyped(testGroup, 10, 100, "codex-10", "codexlike", "chatgpt")
	return e, plat, up
}

func openAIStreamOnly() manifest.AccountPlatform {
	return manifest.AccountPlatform{Platform: manifest.PlatformOpenAI, Endpoints: []string{"responses"},
		StreamOnly: []string{"openai.responses"}, Adapted: map[string]manifest.AdaptOptions{"chat_completions": {}}}
}

func (e *env) responses(stream bool) result {
	e.t.Helper()
	return e.do("/v1/responses", map[string]any{"model": testModel, "input": "hi", "stream": stream},
		map[string]string{"authorization": "Bearer " + testKey, "x-api-key": ""})
}

func TestStreamOnlyResponsesNonStream(t *testing.T) {
	e, plat, up := streamOnlyEnv(t, openAIStreamOnly())
	r := e.responses(false)
	if r.status != 200 || !strings.HasPrefix(r.header.Get("Content-Type"), "application/json") {
		t.Fatalf("response: %d %s %s", r.status, r.header.Get("Content-Type"), r.body)
	}
	j := r.json()
	if j.Get("id").String() != "resp_1" || j.Get("status").String() != "completed" ||
		j.Get("output.0.content.0.text").String() != "hello" || j.Get("usage.output_tokens").Int() != soOutput {
		t.Fatalf("body %s", r.body)
	}
	if got := gjson.GetBytes(up.last(), "stream"); got.Type != gjson.True {
		t.Fatalf("upstream stream = %s", got.Raw)
	}
	// The plugin builds from the body that goes upstream, stream forced
	// already; its meta keeps the client's stream flag.
	if b := plat.builds[0]; b.GetMeta().GetStream() || gjson.GetBytes(b.GetBody(), "model").String() == "" ||
		gjson.GetBytes(b.GetBody(), "stream").Type != gjson.True {
		t.Fatalf("build meta stream must stay the client's: %+v %s", b.GetMeta(), b.GetBody())
	}
	rec := e.record()
	if !rec.Success || rec.Stream || rec.StatusCode != 200 || rec.Tokens.Output != soOutput || rec.Tokens.CacheRead != soCached ||
		rec.UpstreamProtocol != "openai.responses" || rec.FirstTokenMs != 0 || rec.ResponseMismatch != "" || !rec.Billable {
		t.Fatalf("record %+v", rec)
	}
}

func TestStreamOnlyIncomplete(t *testing.T) {
	e, _, up := streamOnlyEnv(t, openAIStreamOnly())
	up.terminal = "incomplete"
	r := e.responses(false)
	if r.status != 200 || r.json().Get("status").String() != "incomplete" ||
		r.json().Get("incomplete_details.reason").String() != "max_output_tokens" {
		t.Fatalf("response: %d %s", r.status, r.body)
	}
	if rec := e.record(); !rec.Success || rec.Tokens.Output != soOutput {
		t.Fatalf("record %+v", rec)
	}
}

// An empty output is filled from the output_item.done items, by output_index.
func TestStreamOnlyFillsEmptyOutput(t *testing.T) {
	e, _, up := streamOnlyEnv(t, openAIStreamOnly())
	up.emptyOutput = true
	r := e.responses(false)
	j := r.json()
	if r.status != 200 || j.Get("output.#").Int() != 2 || j.Get("output.0.id").String() != "msg_1" ||
		j.Get("output.1.id").String() != "msg_2" || j.Get("output.1.content.0.text").String() != "second" {
		t.Fatalf("response: %d %s", r.status, r.body)
	}
	e.record()
}

// A streaming client is relayed as before.
func TestStreamOnlyStreamingClientUnaffected(t *testing.T) {
	e, _, up := streamOnlyEnv(t, openAIStreamOnly())
	r := e.responses(true)
	if r.status != 200 || !strings.HasPrefix(r.header.Get("Content-Type"), "text/event-stream") ||
		!strings.Contains(string(r.body), "event: response.completed") || !strings.Contains(string(r.body), "response.output_text.delta") {
		t.Fatalf("response: %d %s %s", r.status, r.header.Get("Content-Type"), r.body)
	}
	if !gjson.GetBytes(up.last(), "stream").Bool() {
		t.Fatal("stream flag lost")
	}
	if rec := e.record(); !rec.Success || !rec.Stream || rec.Tokens.Output != soOutput || rec.FirstTokenMs == 0 {
		t.Fatalf("record %+v", rec)
	}
}

// Without the declaration the client's stream flag goes upstream unchanged.
func TestStreamOnlyUndeclaredTypeUnchanged(t *testing.T) {
	sp := openAIStreamOnly()
	sp.StreamOnly = nil
	e, _, up := streamOnlyEnv(t, sp)
	up.streamOnly = false
	r := e.responses(false)
	if r.status != 200 || r.json().Get("output.0.content.0.text").String() != "hello" {
		t.Fatalf("response: %d %s", r.status, r.body)
	}
	if got := gjson.GetBytes(up.last(), "stream"); got.Type != gjson.False {
		t.Fatalf("upstream stream = %s", got.Raw)
	}
	if rec := e.record(); !rec.Success || rec.Stream || rec.Tokens.Output != soOutput {
		t.Fatalf("record %+v", rec)
	}
	// And a stream-only upstream then refuses it, as it would without §83.
	up.streamOnly = true
	if r := e.responses(false); r.status != 400 {
		t.Fatalf("undeclared stream-only upstream: %d %s", r.status, r.body)
	}
	e.record()
}

// The declaration names protocols: another protocol of the type is unchanged.
func TestStreamOnlyOtherProtocolUnchanged(t *testing.T) {
	sp := openAIStreamOnly()
	sp.Endpoints = nil
	sp.StreamOnly = []string{"openai.chat"}
	e, _, up := streamOnlyEnv(t, sp)
	up.streamOnly = false
	if r := e.responses(false); r.status != 200 {
		t.Fatalf("response: %d %s", r.status, r.body)
	}
	if got := gjson.GetBytes(up.last(), "stream"); got.Type != gjson.False {
		t.Fatalf("upstream stream = %s", got.Raw)
	}
	e.record()
}

// response.failed is an upstream error: the plugin classifies it with the
// event's status (502 when it has none) and the error object as the body.
func TestStreamOnlyResponseFailed(t *testing.T) {
	e, plat, up := streamOnlyEnv(t, openAIStreamOnly())
	e.accounts.groups[testGroup] = []int64{10}
	up.terminal = "failed"
	r := e.responses(false)
	if r.status != 502 || r.json().Get("error.code").String() != "server_error" || r.json().Get("error.message").String() != "the model melted" {
		t.Fatalf("response: %d %s", r.status, r.body)
	}
	if len(plat.classify) != 1 || plat.classify[0].GetStatus() != 502 ||
		gjson.GetBytes(plat.classify[0].GetBodyPrefix(), "error.message").String() != "the model melted" ||
		plat.classify[0].GetMeta().GetProtocol() != "openai.responses" {
		t.Fatalf("classify %+v", plat.classify)
	}
	// The fake plugin cools 5xx accounts down.
	if len(e.accounts.cooldowns) != 1 || e.accounts.cooldowns[0] != 10 {
		t.Fatalf("cooldowns %v", e.accounts.cooldowns)
	}
	rec := e.record()
	if rec.Success || rec.Stream || rec.StatusCode != 502 || rec.ErrorType != errTypeUpstream || rec.ErrorMessage != "the model melted" {
		t.Fatalf("record %+v", rec)
	}
}

// An error event carrying a status is classified with that status.
func TestStreamOnlyErrorEventStatus(t *testing.T) {
	e, plat, up := streamOnlyEnv(t, openAIStreamOnly())
	up.terminal = "error429"
	r := e.responses(false)
	if r.status != 429 || r.json().Get("error.type").String() != "usage_limit_reached" {
		t.Fatalf("response: %d %s", r.status, r.body)
	}
	if len(plat.classify) != 1 || plat.classify[0].GetStatus() != 429 ||
		gjson.GetBytes(plat.classify[0].GetBodyPrefix(), "error.resets_at").Int() != 1999999999 {
		t.Fatalf("classify %+v", plat.classify)
	}
	if rec := e.record(); rec.Success || rec.StatusCode != 429 {
		t.Fatalf("record %+v", rec)
	}

	// A flat Responses error event without a status: 502, code and message kept.
	up.terminal = "error_flat"
	e.accounts.cooldown = map[int64]time.Time{}
	r = e.responses(false)
	if r.status != 502 || r.json().Get("error.code").String() != "bad_thing" || r.json().Get("error.message").String() != "flat error" ||
		r.json().Get("error.type").Exists() {
		t.Fatalf("flat error: %d %s", r.status, r.body)
	}
	e.record()
}

// A stream that ends without a terminal event is a broken upstream answer:
// classified like a transport error.
func TestStreamOnlyUnfinishedStream(t *testing.T) {
	e, plat, up := streamOnlyEnv(t, openAIStreamOnly())
	e.accounts.groups[testGroup] = []int64{10}
	up.terminal = "none"
	r := e.responses(false)
	if r.status != 502 {
		t.Fatalf("response: %d %s", r.status, r.body)
	}
	if len(plat.classify) != 1 || plat.classify[0].GetStatus() != 0 ||
		!strings.Contains(plat.classify[0].GetTransportError(), "response.completed") {
		t.Fatalf("classify %+v", plat.classify)
	}
	e.record()
}

// ---------------------------------------------------------------- converted route

// chatToRelay converts openai.chat to the "relayx.responses" protocol of a
// plugin platform, a minimal stand-in for a chat to Responses conversion.
type chatToRelay struct{}

func (chatToRelay) From() string         { return "openai.chat" }
func (chatToRelay) To() string           { return "relayx.responses" }
func (chatToRelay) Grade() convert.Grade { return convert.GradeGood }
func (c chatToRelay) Prepare(body []byte, _ convert.Options) (convert.Converter, []byte, error) {
	out := []byte(`{}`)
	out, _ = sjson.SetBytes(out, "model", gjson.GetBytes(body, "model").String())
	out, _ = sjson.SetBytes(out, "input", gjson.GetBytes(body, "messages.0.content").String())
	out, _ = sjson.SetBytes(out, "stream", gjson.GetBytes(body, "stream").Bool())
	return c, out, nil
}
func (chatToRelay) Response(body []byte) ([]byte, error) {
	var text string
	for _, item := range gjson.GetBytes(body, "output").Array() {
		for _, part := range item.Get("content").Array() {
			text += part.Get("text").String()
		}
	}
	out := []byte(`{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant"},"finish_reason":"stop"}]}`)
	out, _ = sjson.SetBytes(out, "choices.0.message.content", text)
	out, _ = sjson.SetBytes(out, "usage.prompt_tokens", gjson.GetBytes(body, "usage.input_tokens").Int())
	out, _ = sjson.SetBytes(out, "usage.completion_tokens", gjson.GetBytes(body, "usage.output_tokens").Int())
	return out, nil
}
func (chatToRelay) NewStream() convert.StreamConverter { return relayStream{} }

type relayStream struct{}

func (relayStream) Event(e convert.Event) ([]convert.Event, error) {
	if gjson.GetBytes(e.Data, "type").String() != "response.output_text.delta" {
		return nil, nil
	}
	out, _ := sjson.SetBytes([]byte(`{"object":"chat.completion.chunk"}`), "choices.0.delta.content", gjson.GetBytes(e.Data, "delta").String())
	return []convert.Event{{Data: out}}, nil
}
func (relayStream) Flush() ([]convert.Event, error) {
	return []convert.Event{{Data: []byte("[DONE]")}}, nil
}

// relayPlatform is a plugin platform whose only endpoint speaks a
// Responses-like protocol with the built-in Responses usage rules.
func relayPlatform(t *testing.T) manifest.Platform {
	ep := endpointOf(t, builtinPlatform(t, manifest.PlatformOpenAI), "openai.responses")
	ep.ID, ep.Path, ep.Protocol = "responses", "/relayx/v1/responses", "relayx.responses"
	return manifest.Platform{ID: "relayx", Endpoints: []manifest.Endpoint{ep}, Usage: manifest.UsageRules{Semantics: "inclusive"}}
}

// The upstream protocol after conversion decides: a non-streaming chat
// request converted to a stream-only protocol streams upstream and the
// collected answer is converted back to chat JSON.
func TestStreamOnlyConvertedRoute(t *testing.T) {
	up := newResponsesUpstream(t)
	e := newEnv(t, func(e *env) { e.conv = convert.NewRegistry(chatToRelay{}) })
	relayInfo := core.PluginInfo{Key: "relayx", Version: "1.0.0", Manifest: &manifest.Manifest{Key: "relayx", ExternalServices: []string{testUpstreamHost}}}
	e.gen.plugins = append(e.gen.plugins, relayInfo)
	e.gen.addPlatform(relayInfo, relayPlatform(t))
	plat := &fakePlatform{route: func(*pluginv1.ExecuteRequest) string { return up.srv.URL + "/relayx/v1/responses" }}
	e.addAccountType("codexlike", "chatgpt", plat, manifest.AccountPlatform{Platform: "relayx", StreamOnly: []string{"relayx.responses"}})
	e.accounts.addTyped(testGroup, 10, 100, "codex-10", "codexlike", "chatgpt")
	e.accounts.serveOn(10, "openai.chat") // relayx is not the platform of the chat endpoint
	chat := func(stream bool) result {
		return e.do("/v1/chat/completions", map[string]any{"model": testModel, "stream": stream,
			"messages": []any{map[string]any{"role": "user", "content": "hi"}}},
			map[string]string{"authorization": "Bearer " + testKey, "x-api-key": ""})
	}

	r := chat(false)
	if r.status != 200 || r.json().Get("object").String() != "chat.completion" ||
		r.json().Get("choices.0.message.content").String() != "hello" || r.json().Get("usage.completion_tokens").Int() != soOutput {
		t.Fatalf("response: %d %s", r.status, r.body)
	}
	if got := gjson.GetBytes(up.last(), "stream"); got.Type != gjson.True || gjson.GetBytes(up.last(), "input").String() != "hi" {
		t.Fatalf("upstream body %s", up.last())
	}
	rec := e.record()
	if !rec.Success || rec.Stream || rec.Protocol != "openai.chat" || rec.UpstreamProtocol != "relayx.responses" ||
		rec.Tokens.Output != soOutput || rec.Tokens.CacheRead != soCached {
		t.Fatalf("record %+v", rec)
	}

	// A streaming chat client still gets the converted stream.
	r = chat(true)
	if r.status != 200 || !strings.Contains(string(r.body), "chat.completion.chunk") || !strings.Contains(string(r.body), "[DONE]") {
		t.Fatalf("stream: %d %s", r.status, r.body)
	}
	if rec := e.record(); !rec.Stream || rec.Tokens.Output != soOutput {
		t.Fatalf("stream record %+v", rec)
	}
}

// ---------------------------------------------------------------- execute

// Every request goes through Execute: on a stream-only route the plugin is
// handed the body with stream: true already set, and the upstream receives
// exactly the bytes it returned. An error event of the collected stream goes
// to the plugin's ClassifyError.
func TestStreamOnlyExecutePath(t *testing.T) {
	e, plat, up := streamOnlyEnv(t, openAIStreamOnly())
	plat.patches = []*pluginv1.BodyPatch{{Op: pluginv1.BodyPatch_OP_SET, Path: "instructions", ValueJson: `"from the plugin"`}}

	r := e.responses(false)
	if r.status != 200 || r.json().Get("output.0.content.0.text").String() != "hello" ||
		!strings.HasPrefix(r.header.Get("Content-Type"), "application/json") {
		t.Fatalf("response: %d %s", r.status, r.body)
	}
	builds := plat.buildCalls()
	if len(builds) != 1 {
		t.Fatalf("execute calls %d", len(builds))
	}
	handed := builds[0].GetBody()
	if gjson.GetBytes(handed, "stream").Type != gjson.True || builds[0].GetMeta().GetStream() {
		t.Fatalf("handed body %s, meta %+v", handed, builds[0].GetMeta())
	}
	want, err := applyPatches(handed, plat.patches)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(up.last(), want) {
		t.Fatalf("upstream got %s, the plugin returned %s", up.last(), want)
	}
	if rec := e.record(); !rec.Success || rec.Stream || rec.Tokens.Output != soOutput || !rec.Billable {
		t.Fatalf("record %+v", rec)
	}

	up.terminal = "error429"
	e.accounts.groups[testGroup] = []int64{10}
	r = e.responses(false)
	if r.status != 429 || r.json().Get("error.type").String() != "usage_limit_reached" {
		t.Fatalf("execute error: %d %s", r.status, r.body)
	}
	plat.mu.Lock()
	classified := append([]*pluginv1.ClassifyErrorRequest(nil), plat.classify...)
	plat.mu.Unlock()
	if len(classified) != 1 || classified[0].GetStatus() != 429 {
		t.Fatalf("classify %+v", classified)
	}
	e.record()
}

// buildCalls are the ExecuteRequests p was handed so far.
func (p *fakePlatform) buildCalls() []*pluginv1.ExecuteRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*pluginv1.ExecuteRequest(nil), p.builds...)
}

// A stream-only answer whose items and response together pass 32 MiB (the
// accumulation cap it once had) is kept whole.
func TestReadStreamOnlyLarge(t *testing.T) {
	big := strings.Repeat("x", 17<<20)
	in := "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"id\":\"a\",\"t\":\"" + big + "\"}}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"output_index\":1,\"item\":{\"id\":\"b\",\"t\":\"" + big + "\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"output\":[]}}\n\n"
	ans := readStreamOnly(strings.NewReader(in))
	if ans.broken != nil || ans.errBody != nil || gjson.GetBytes(ans.body, "output.#").Int() != 2 || len(gjson.GetBytes(ans.body, "output.1.t").String()) != len(big) {
		t.Fatalf("answer: broken=%v status=%d %d bytes", ans.broken, ans.status, len(ans.body))
	}
}

func TestReadStreamOnlyFraming(t *testing.T) {
	// Events named only by their "type", CRLF lines, a split data field and
	// a terminal event without its blank line.
	in := "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\r\ndata: \"item\":{\"id\":\"a\"}}\r\n\r\n" +
		": keep-alive\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"output\":null}}"
	ans := readStreamOnly(strings.NewReader(in))
	if ans.broken != nil || ans.errBody != nil || gjson.GetBytes(ans.body, "output.0.id").String() != "a" || gjson.GetBytes(ans.body, "id").String() != "r" {
		t.Fatalf("answer %+v %s", ans, ans.body)
	}
	// A full output is kept as the upstream sent it.
	in = "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"id\":\"a\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"id\":\"b\"}]}}\n\n"
	if ans := readStreamOnly(strings.NewReader(in)); gjson.GetBytes(ans.body, "output.#").Int() != 1 || gjson.GetBytes(ans.body, "output.0.id").String() != "b" {
		t.Fatalf("full output rewritten: %s", ans.body)
	}
	// No terminal event.
	if ans := readStreamOnly(strings.NewReader("data: {\"type\":\"response.created\"}\n\n")); ans.broken == nil {
		t.Fatalf("unfinished stream accepted: %+v", ans)
	}
	// A terminal event without a response object is an upstream error.
	if ans := readStreamOnly(strings.NewReader("event: response.completed\ndata: {}\n\n")); ans.status != 502 || ans.errBody == nil {
		t.Fatalf("empty terminal: %+v", ans)
	}
}

func TestStreamOnlyErrorShapes(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		status     int
		body       string
	}{
		{"response.failed", `{"type":"response.failed","response":{"error":{"code":"x","message":"m"}}}`, 502, `{"error":{"code":"x","message":"m"}}`},
		{"response.failed", `{"type":"response.failed","response":{}}`, 502, `{"error":{"message":"upstream response failed"}}`},
		{"error", `{"type":"error","status":401,"error":{"message":"m"}}`, 401, `{"error":{"message":"m"}}`},
		{"error", `{"type":"error","status":200,"error":"plain"}`, 502, `{"error":{"message":"plain"}}`},
		{"error", `{"type":"error","code":"c","message":"m","param":null}`, 502, `{"error":{"code":"c","message":"m","param":null}}`},
	} {
		status, body := streamOnlyError(tc.name, []byte(tc.data))
		if status != tc.status || string(body) != tc.body {
			t.Errorf("%s %s: %d %s", tc.name, tc.data, status, body)
		}
	}
}

// ---------------------------------------------------------------- same-platform conversion (CONTRACTS §83.3)

// sameChatEnv is streamOnlyEnv with the built-in converters, the group
// holding only the type's account.
func sameChatEnv(t *testing.T, sp manifest.AccountPlatform) (*env, *fakePlatform, *responsesUpstream) {
	e, plat, up := streamOnlyEnv(t, sp, func(e *env) { e.conv = convert.Default() })
	e.accounts.groups[testGroup] = []int64{10}
	return e, plat, up
}

func (e *env) chat(stream bool, extra ...string) result {
	e.t.Helper()
	b := map[string]any{"model": testModel, "stream": stream,
		"messages": []any{map[string]any{"role": "system", "content": "be brief"}, map[string]any{"role": "user", "content": "hi"}}}
	if stream {
		b["stream_options"] = map[string]any{"include_usage": true}
	}
	for i := 0; i+1 < len(extra); i += 2 {
		b[extra[i]] = extra[i+1]
	}
	return e.do("/v1/chat/completions", b, map[string]string{"authorization": "Bearer " + testKey, "x-api-key": ""})
}

// A type serving only the responses endpoint of openai serves chat through
// the built-in converter: non-streaming chat -> streaming Responses upstream
// -> collected JSON -> chat completion JSON.
func TestSamePlatformChatNonStreamThroughStreamOnly(t *testing.T) {
	e, plat, up := sameChatEnv(t, openAIStreamOnly())
	r := e.chat(false)
	j := r.json()
	if r.status != 200 || j.Get("object").String() != "chat.completion" || j.Get("choices.0.message.content").String() != "hello" ||
		j.Get("choices.0.finish_reason").String() != "stop" || j.Get("usage.prompt_tokens").Int() != soInput ||
		j.Get("usage.completion_tokens").Int() != soOutput || j.Get("usage.prompt_tokens_details.cached_tokens").Int() != soCached {
		t.Fatalf("response: %d %s", r.status, r.body)
	}
	wire := gjson.ParseBytes(up.last())
	if wire.Get("stream").Type != gjson.True || wire.Get("store").Type != gjson.False || wire.Get("instructions").String() != "be brief" ||
		wire.Get("input.0.content.0.text").String() != "hi" || wire.Get("messages").Exists() {
		t.Fatalf("upstream body %s", up.last())
	}
	if b := plat.builds[0]; b.GetMeta().GetProtocol() != "openai.responses" || b.GetMeta().GetClientProtocol() != "openai.chat" {
		t.Fatalf("build meta %+v", b.GetMeta())
	}
	rec := e.record()
	if !rec.Success || rec.Stream || rec.Platform != "openai" || rec.Protocol != "openai.chat" || rec.UpstreamProtocol != "openai.responses" ||
		rec.Tokens.Output != soOutput || rec.Tokens.CacheRead != soCached || rec.Tokens.Input != soInput ||
		rec.UsageSemantics != "inclusive" || !rec.Billable {
		t.Fatalf("record %+v", rec)
	}
}

func TestSamePlatformChatStream(t *testing.T) {
	e, _, up := sameChatEnv(t, openAIStreamOnly())
	r := e.chat(true)
	if r.status != 200 || !strings.HasPrefix(r.header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("response: %d %s", r.status, r.body)
	}
	var content strings.Builder
	var finish string
	var usage gjson.Result
	done := false
	for _, line := range strings.Split(string(r.body), "\n") {
		d, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		if d == "[DONE]" {
			done = true
			continue
		}
		j := gjson.Parse(d)
		if j.Get("object").String() != "chat.completion.chunk" {
			t.Fatalf("chunk %s", d)
		}
		content.WriteString(j.Get("choices.0.delta.content").String())
		if f := j.Get("choices.0.finish_reason").String(); f != "" {
			finish = f
		}
		if u := j.Get("usage"); u.IsObject() {
			usage = u
		}
	}
	if content.String() != "hello" || finish != "stop" || !done || usage.Get("completion_tokens").Int() != soOutput {
		t.Fatalf("stream %q %q %v %s\n%s", content.String(), finish, done, usage.Raw, r.body)
	}
	if !gjson.GetBytes(up.last(), "stream").Bool() {
		t.Fatal("stream flag lost")
	}
	if rec := e.record(); !rec.Success || !rec.Stream || rec.Protocol != "openai.chat" || rec.UpstreamProtocol != "openai.responses" ||
		rec.Tokens.Output != soOutput || !rec.Billable {
		t.Fatalf("record %+v", rec)
	}
}

// Without streamOnly the converted request keeps the client's stream flag
// and the upstream JSON is converted directly.
func TestSamePlatformChatWithoutStreamOnly(t *testing.T) {
	sp := openAIStreamOnly()
	sp.StreamOnly = nil
	e, _, up := sameChatEnv(t, sp)
	up.streamOnly = false
	r := e.chat(false)
	if r.status != 200 || r.json().Get("choices.0.message.content").String() != "hello" {
		t.Fatalf("response: %d %s", r.status, r.body)
	}
	if gjson.GetBytes(up.last(), "stream").Type != gjson.False {
		t.Fatalf("upstream body %s", up.last())
	}
	if rec := e.record(); rec.Stream || rec.UpstreamProtocol != "openai.responses" || rec.Tokens.Output != soOutput {
		t.Fatalf("record %+v", rec)
	}
}

// Chat fields without a Responses equivalent whose absence keeps the
// request correct are left out (CONTRACTS §83.2): non-streaming and
// streaming requests are served and the upstream body has none of them.
func TestSamePlatformChatDropsUnconvertible(t *testing.T) {
	for _, stream := range []bool{false, true} {
		e, _, up := sameChatEnv(t, openAIStreamOnly())
		b := map[string]any{"model": testModel, "stream": stream, "stop": []any{"\n"}, "seed": 7, "presence_penalty": 0.5,
			"max_tokens": 10, "max_completion_tokens": 20, "made_up": true,
			"messages": []any{map[string]any{"role": "user", "name": "bob", "content": "hi"}}}
		r := e.do("/v1/chat/completions", b, map[string]string{"authorization": "Bearer " + testKey, "x-api-key": ""})
		if r.status != 200 || !strings.Contains(string(r.body), "hello") {
			t.Fatalf("stream=%v response: %d %s", stream, r.status, r.body)
		}
		if stream != strings.HasPrefix(r.header.Get("Content-Type"), "text/event-stream") {
			t.Fatalf("stream=%v content type %q", stream, r.header.Get("Content-Type"))
		}
		wire := gjson.ParseBytes(up.last())
		for _, absent := range []string{"stop", "seed", "presence_penalty", "max_tokens", "max_completion_tokens", "made_up", "input.0.name"} {
			if wire.Get(absent).Exists() {
				t.Errorf("stream=%v: %s sent upstream: %s", stream, absent, up.last())
			}
		}
		if wire.Get("max_output_tokens").Int() != 20 || wire.Get("input.0.content.0.text").String() != "hi" || !wire.Get("stream").Bool() {
			t.Errorf("stream=%v upstream body %s", stream, up.last())
		}
		if rec := e.record(); !rec.Success || rec.Stream != stream || rec.UpstreamProtocol != "openai.responses" {
			t.Errorf("stream=%v record %+v", stream, rec)
		}
	}
}

// A chat field without a Responses equivalent is a 400 naming the field;
// nothing goes upstream.
func TestSamePlatformChatRejectsUnconvertible(t *testing.T) {
	e, plat, _ := sameChatEnv(t, openAIStreamOnly())
	r := e.do("/v1/chat/completions", map[string]any{"model": testModel, "n": 2,
		"messages": []any{map[string]any{"role": "user", "content": "hi"}}},
		map[string]string{"authorization": "Bearer " + testKey, "x-api-key": ""})
	if r.status != 400 || r.json().Get("error.code").String() != "unsupported_conversion" || !strings.Contains(r.json().Get("error.message").String(), "n:") {
		t.Fatalf("response: %d %s", r.status, r.body)
	}
	if plat.buildCount() != 0 {
		t.Fatal("unconvertible request reached the plugin")
	}
	e.record()
}

// Native wins: a type serving the chat endpoint itself is never converted,
// and the type's other platforms are not a way around its subset.
func TestSamePlatformConversionScope(t *testing.T) {
	e, _, up := sameChatEnv(t, manifest.AccountPlatform{Platform: manifest.PlatformOpenAI})
	up.streamOnly = false
	chatUp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"c","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"native"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	t.Cleanup(chatUp.Close)
	e.gen.accountTypes[len(e.gen.accountTypes)-1].Client.(*fakePlatform).route = func(*pluginv1.ExecuteRequest) string { return chatUp.URL }
	if r := e.chat(false); r.status != 200 || r.json().Get("choices.0.message.content").String() != "native" {
		t.Fatalf("native: %d %s", r.status, r.body)
	}
	if rec := e.record(); rec.UpstreamProtocol != "openai.chat" {
		t.Fatalf("native record %+v", rec)
	}

	// openai limited to responses, plus anthropic: chat is converted within
	// openai, not to anthropic.messages.
	e2, _, _ := sameChatEnv(t, openAIStreamOnly())
	b := &e2.gen.accountTypes[len(e2.gen.accountTypes)-1]
	b.Type.Platforms = append(b.Type.Platforms, manifest.AccountPlatform{Platform: manifest.PlatformAnthropic})
	if r := e2.chat(false); r.status != 200 || r.json().Get("choices.0.message.content").String() != "hello" {
		t.Fatalf("subset: %d %s", r.status, r.body)
	}
	if rec := e2.record(); rec.UpstreamProtocol != "openai.responses" {
		t.Fatalf("subset record %+v", rec)
	}

	// openai limited to embeddings: no converter, the endpoint is not served.
	e3, _, _ := sameChatEnv(t, manifest.AccountPlatform{Platform: manifest.PlatformOpenAI, Endpoints: []string{"embeddings"}})
	if r := e3.chat(false); r.status != 404 {
		t.Fatalf("embeddings only: %d %s", r.status, r.body)
	}

	// The subset without adapted: the converter exists, but the type does
	// not declare the adaptation, so chat is not served (CONTRACTS §83.3).
	sp := openAIStreamOnly()
	sp.Adapted = nil
	e4, plat, _ := sameChatEnv(t, sp)
	if r := e4.chat(false); r.status != 404 || plat.buildCount() != 0 {
		t.Fatalf("not adapted: %d %s", r.status, r.body)
	}
}

// A non-streaming client of a stream-only upstream that hangs up while the
// stream is collected left an answered request (CONTRACTS §25.8): under
// "continue" the stream is read to its end and its usage billed, under
// "precharge" the upstream stops and the request pays the estimate.
func TestStreamOnlyClientHangUpIsBilled(t *testing.T) {
	for _, policy := range []string{core.DisconnectContinue, core.DisconnectPrecharge} {
		t.Run(policy, func(t *testing.T) {
			started, canceled, finish := make(chan struct{}), make(chan struct{}, 1), make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n")
				w.(http.Flusher).Flush()
				close(started)
				select {
				case <-r.Context().Done():
					canceled <- struct{}{}
				case <-finish:
					fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":11,\"output_tokens\":22}}}\n\n")
				}
			}))
			defer srv.Close()
			defer close(finish)
			e := newEnv(t)
			e.balance.disconnect = policy
			plat := &fakePlatform{route: func(*pluginv1.ExecuteRequest) string { return srv.URL + "/v1/responses" }}
			e.addAccountType("codexlike", "chatgpt", plat, openAIStreamOnly())
			e.accounts.addTyped(testGroup, 10, 100, "codex-10", "codexlike", "chatgpt")

			ctx, hangUp := context.WithCancel(context.Background())
			// Hang up once the core is collecting the stream: the headers
			// reached it right after the first event was flushed.
			go func() { <-started; time.Sleep(100 * time.Millisecond); hangUp() }()
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, e.srv.URL+"/v1/responses",
				strings.NewReader(`{"model":"`+testModel+`","input":"hi","stream":false}`))
			req.Header.Set("authorization", "Bearer "+testKey)
			if resp, err := http.DefaultClient.Do(req); err == nil {
				resp.Body.Close()
				t.Fatal("the client got an answer although it hung up")
			}
			if policy == core.DisconnectContinue {
				select {
				case <-canceled:
					t.Fatal("upstream canceled although the answer is to be read on")
				case <-time.After(300 * time.Millisecond):
				}
				finish <- struct{}{}
				rec := e.record()
				if rec.ErrorType != errTypeClientCanceled || !rec.Billable || rec.UsageExtract == core.UsageExtractPrecharge ||
					rec.Tokens.Input != 11 || rec.Tokens.Output != 22 {
					t.Fatalf("record: type %q billable %v extract %q tokens %+v", rec.ErrorType, rec.Billable, rec.UsageExtract, rec.Tokens)
				}
				return
			}
			select {
			case <-canceled:
			case <-time.After(10 * time.Second):
				t.Fatal("upstream not canceled when the client left")
			}
			if rec := e.record(); rec.ErrorType != errTypeClientCanceled || !rec.Billable || rec.UsageExtract != core.UsageExtractPrecharge {
				t.Fatalf("record: type %q billable %v extract %q", rec.ErrorType, rec.Billable, rec.UsageExtract)
			}
		})
	}
}
