package gateway

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/tidwall/gjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// ---------------------------------------------------------------- OpenAI / Gemini-like upstream

type apiCall struct {
	key, path, query string
	body             []byte
}

// apiUpstream answers OpenAI (chat, responses, embeddings) and Gemini
// (generateContent, streamGenerateContent with alt=sse, countTokens,
// embedContent, batchEmbedContents) requests with canned bodies carrying
// usage. status[key] forces an error.
type apiUpstream struct {
	srv    *httptest.Server
	mu     sync.Mutex
	calls  []apiCall
	status map[string]int
}

func newAPIUpstream(t *testing.T) *apiUpstream {
	u := &apiUpstream{status: map[string]int{}}
	u.srv = httptest.NewServer(http.HandlerFunc(u.handle))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *apiUpstream) keys() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	var out []string
	for _, c := range u.calls {
		out = append(out, c.key)
	}
	return out
}

func (u *apiUpstream) last() apiCall {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.calls[len(u.calls)-1]
}

func sseData(w http.ResponseWriter, event, data string) {
	if event != "" {
		_, _ = fmt.Fprintf(w, "event: %s\n", event)
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	w.(http.Flusher).Flush()
}

const (
	gemChunk1 = `{"candidates":[{"content":{"role":"model","parts":[{"text":"he"}]}}],"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":1,"totalTokenCount":21},"modelVersion":"gemini-2.5-pro-002"}`
	gemChunk2 = `{"candidates":[{"content":{"role":"model","parts":[{"text":"llo"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":6,"cachedContentTokenCount":4,"totalTokenCount":26},"modelVersion":"gemini-2.5-pro-002"}`
)

func (u *apiUpstream) handle(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("x-api-key")
	body, _ := io.ReadAll(r.Body)
	u.mu.Lock()
	u.calls = append(u.calls, apiCall{key: key, path: r.URL.Path, query: r.URL.RawQuery, body: body})
	status := u.status[key]
	u.mu.Unlock()
	if status != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"error":{"message":"mock status %d","type":"server_error"}}`, status)
		return
	}
	stream := gjson.GetBytes(body, "stream").Bool()
	switch {
	case r.URL.Path == "/v1/chat/completions" && !stream:
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","object":"chat.completion","model":"gpt-4o-2024-08-06","choices":[{"index":0,"message":{"role":"assistant","content":"hello"}}],` +
			`"usage":{"prompt_tokens":30,"completion_tokens":7,"total_tokens":37,"prompt_tokens_details":{"cached_tokens":12}}}`))
	case r.URL.Path == "/v1/chat/completions":
		w.Header().Set("Content-Type", "text/event-stream")
		sseData(w, "", `{"id":"c1","object":"chat.completion.chunk","model":"gpt-4o-2024-08-06","choices":[{"index":0,"delta":{"content":"he"}}],"usage":null}`)
		sseData(w, "", `{"id":"c1","object":"chat.completion.chunk","model":"gpt-4o-2024-08-06","choices":[{"index":0,"delta":{"content":"llo"}}],"usage":null}`)
		sseData(w, "", `{"id":"c1","object":"chat.completion.chunk","model":"gpt-4o-2024-08-06","choices":[],"usage":{"prompt_tokens":30,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":12}}}`)
		sseData(w, "", `[DONE]`)
	case r.URL.Path == "/v1/responses" && stream:
		w.Header().Set("Content-Type", "text/event-stream")
		sseData(w, "response.created", `{"type":"response.created","response":{"model":"gpt-5","usage":null}}`)
		sseData(w, "response.output_text.delta", `{"type":"response.output_text.delta","delta":"hello"}`)
		sseData(w, "response.completed", `{"type":"response.completed","response":{"model":"gpt-5","usage":{"input_tokens":40,"output_tokens":9,"input_tokens_details":{"cached_tokens":8}}}}`)
	case r.URL.Path == "/v1/images/generations" || r.URL.Path == "/v1/images/edits":
		imageAnswer(w, r.URL.Path, stream)
	case r.URL.Path == "/v1/embeddings":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"embedding":[0.1]}],"model":"text-embedding-3-small","usage":{"prompt_tokens":5,"total_tokens":5}}`))
	case strings.HasSuffix(r.URL.Path, ":generateContent"):
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(gemChunk2))
	case strings.HasSuffix(r.URL.Path, ":streamGenerateContent"):
		// The Gemini account plugin always asks for SSE upstream.
		if r.URL.Query().Get("alt") != "sse" {
			http.Error(w, "test upstream expects alt=sse", 400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		sseData(w, "", gemChunk1)
		sseData(w, "", gemChunk2)
	case strings.HasSuffix(r.URL.Path, ":countTokens"):
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"totalTokens":31}`))
	case strings.HasSuffix(r.URL.Path, ":embedContent") || strings.HasSuffix(r.URL.Path, ":batchEmbedContents"):
		geminiEmbedAnswer(w, r.URL.Path)
	default:
		http.Error(w, "unexpected "+r.URL.Path, 404)
	}
}

// apiPlatform is an account type plugin for the openai/gemini upstream:
// OpenAI protocols map to their path, Gemini ones to
// /v1beta/models/{meta.model}:{method} (streaming with alt=sse).
func apiPlatform(u *apiUpstream) *fakePlatform {
	return &fakePlatform{route: func(in *pluginv1.ExecuteRequest) string {
		m := in.GetMeta()
		switch m.GetProtocol() {
		case "openai.chat":
			return u.srv.URL + "/v1/chat/completions"
		case "openai.responses":
			return u.srv.URL + "/v1/responses"
		case "openai.embeddings":
			return u.srv.URL + "/v1/embeddings"
		case "openai.images":
			return u.srv.URL + "/v1/images/generations"
		case "openai.images_edit":
			return u.srv.URL + "/v1/images/edits"
		case "gemini.generate":
			return u.srv.URL + "/v1beta/models/" + m.GetModel() + ":generateContent"
		case "gemini.stream_generate":
			return u.srv.URL + "/v1beta/models/" + m.GetModel() + ":streamGenerateContent?alt=sse"
		case "gemini.count_tokens":
			return u.srv.URL + "/v1beta/models/" + m.GetModel() + ":countTokens"
		case "gemini.embed":
			return u.srv.URL + geminiTestPath(m, ":embedContent")
		case "gemini.batch_embed":
			return u.srv.URL + geminiTestPath(m, ":batchEmbedContents")
		}
		return u.srv.URL + "/unknown"
	}}
}

// apiEnv adds an "openai" plugin (type apikey → openai, account 11) and a
// "gemini" plugin (type apikey → gemini, account 21) to the test group.
func apiEnv(t *testing.T) (*env, *apiUpstream, *fakePlatform, *fakePlatform) {
	u := newAPIUpstream(t)
	op, gp := apiPlatform(u), apiPlatform(u)
	e := newEnv(t, func(e *env) {
		e.addAccountType("openai", "apikey", op, manifest.AccountPlatform{Platform: "openai"})
		e.addAccountType("gemini", "apikey", gp, manifest.AccountPlatform{Platform: "gemini"})
		e.accounts.addTyped(testGroup, 11, 99, "oai-11", "openai", "apikey")
		e.accounts.addTyped(testGroup, 21, 99, "gem-21", "gemini", "apikey")
		for _, m := range []string{"gpt-4o", "gpt-5", "text-embedding-3-small", "gemini-2.5-pro"} {
			e.pricer.rules[m] = &core.PriceRule{ID: 10, Model: m, Mode: "visual"}
		}
	})
	return e, u, op, gp
}

func (e *env) post(path string, b any, headers map[string]string) result {
	e.t.Helper()
	h := map[string]string{"x-api-key": "", "anthropic-version": ""}
	for k, v := range headers {
		h[k] = v
	}
	return e.do(path, b, h)
}

func bearer() map[string]string { return map[string]string{"Authorization": "Bearer " + testKey} }

func chatBody(stream bool) map[string]any {
	return map[string]any{"model": "gpt-4o", "stream": stream,
		"messages": []any{map[string]any{"role": "user", "content": "hello there"}}}
}

// ---------------------------------------------------------------- OpenAI

func TestOpenAIChatUsage(t *testing.T) {
	e, u, op, _ := apiEnv(t)

	// Non-stream, Bearer auth.
	h := bearer()
	h["OpenAI-Organization"] = "org-1"
	r := e.post("/v1/chat/completions", chatBody(false), h)
	if r.status != 200 || r.json().Get("choices.0.message.content").String() != "hello" {
		t.Fatalf("chat: %d %s", r.status, r.body)
	}
	if c := u.last(); c.key != "oai-11" || c.path != "/v1/chat/completions" || gjson.GetBytes(c.body, "model").String() != "gpt-4o" {
		t.Fatalf("upstream call %+v", c)
	}
	b := op.builds[0]
	if b.GetMeta().GetProtocol() != "openai.chat" || b.GetMeta().GetModel() != "gpt-4o" || b.GetMeta().GetStream() ||
		b.GetAccount().GetPlatform() != "openai" || gjson.GetBytes(b.GetBody(), "model").String() != "gpt-4o" ||
		b.GetInboundHeaders()["openai-organization"] != "org-1" {
		t.Fatalf("build %+v", b)
	}
	rec := e.record()
	if !rec.Success || rec.Platform != "openai" || rec.Protocol != "openai.chat" || rec.UpstreamProtocol != "openai.chat" ||
		rec.PluginKey != "openai" || rec.AccountType != "apikey" || *rec.AccountID != 11 || rec.Model != "gpt-4o" ||
		rec.UpstreamModel != "gpt-4o-2024-08-06" || rec.UsageSemantics != "inclusive" || rec.Stream || !rec.Billable ||
		rec.Endpoint != "/v1/chat/completions" {
		t.Fatalf("chat record %+v", rec)
	}
	if rec.Tokens != (core.UsageTokens{Input: 30, Output: 7, CacheRead: 12}) {
		t.Fatalf("chat tokens %+v", rec.Tokens)
	}
	// Only openai types were asked for.
	if !hasKey(e.accounts.lastTypes, "openai", "apikey") || hasKey(e.accounts.lastTypes, "anthropic", "apikey") ||
		hasKey(e.accounts.lastTypes, "gemini", "apikey") {
		t.Fatalf("candidate types %v", e.accounts.lastTypes)
	}

	// Stream: chunks pass through verbatim (with [DONE]); usage from the
	// last chunk.
	r = e.post("/v1/chat/completions", chatBody(true), bearer())
	if r.status != 200 || !strings.HasPrefix(r.header.Get("Content-Type"), "text/event-stream") ||
		!strings.Contains(string(r.body), "data: [DONE]") || strings.Count(string(r.body), "data: ") != 4 {
		t.Fatalf("chat stream: %d %s", r.status, r.body)
	}
	rec = e.record()
	if !rec.Success || !rec.Stream || rec.FirstTokenMs <= 0 || rec.Tokens != (core.UsageTokens{Input: 30, Output: 7, CacheRead: 12}) {
		t.Fatalf("chat stream record %+v", rec)
	}

	// Responses stream: usage from response.completed.
	r = e.post("/v1/responses", map[string]any{"model": "gpt-5", "stream": true, "input": "hi"}, bearer())
	if r.status != 200 || !strings.Contains(string(r.body), "event: response.completed") {
		t.Fatalf("responses stream: %d %s", r.status, r.body)
	}
	if rec = e.record(); rec.Protocol != "openai.responses" || rec.Tokens != (core.UsageTokens{Input: 40, Output: 9, CacheRead: 8}) {
		t.Fatalf("responses record %+v", rec)
	}

	// Embeddings.
	r = e.post("/v1/embeddings", map[string]any{"model": "text-embedding-3-small", "input": "abc"}, bearer())
	if r.status != 200 {
		t.Fatalf("embeddings: %d %s", r.status, r.body)
	}
	if rec = e.record(); rec.Protocol != "openai.embeddings" || rec.Tokens != (core.UsageTokens{Input: 5}) || !rec.Billable {
		t.Fatalf("embeddings record %+v", rec)
	}

	// The OpenAI endpoints read the key from Authorization only; errors use
	// the OpenAI error shape.
	r = e.post("/v1/chat/completions", chatBody(false), map[string]string{"x-api-key": testKey})
	j := r.json()
	if r.status != 401 || j.Get("error.type").String() != "authentication_error" || j.Get("error.code").String() != "unauthenticated" ||
		j.Get("error.message").String() == "" || j.Get("type").Exists() {
		t.Fatalf("openai auth error: %d %s", r.status, r.body)
	}
	r = e.post("/v1/chat/completions", map[string]any{"messages": []any{}}, bearer())
	if r.status != 400 || r.json().Get("error.type").String() != "invalid_request_error" {
		t.Fatalf("missing model: %d %s", r.status, r.body)
	}
	e.record()
}

// Accounts of account types from different plugins, in one group, serve a
// built-in endpoint together (priority, failover across types).
func TestMixedPluginTypesServeBuiltinEndpoint(t *testing.T) {
	e, u, op, _ := apiEnv(t)
	relay := apiPlatform(u)
	openaiRoute := relay.route
	relay.route = func(in *pluginv1.ExecuteRequest) string {
		if in.GetMeta().GetProtocol() == "anthropic.messages" {
			return e.up.srv.URL + "/v1/messages"
		}
		return openaiRoute(in)
	}
	e.addAccountType("relay", "relay_key", relay,
		manifest.AccountPlatform{Platform: "anthropic"}, manifest.AccountPlatform{Platform: "openai"})
	e.accounts.addTyped(testGroup, 12, 100, "relay-12", "relay", "relay_key")

	r := e.post("/v1/chat/completions", chatBody(false), bearer())
	if r.status != 200 || strings.Join(u.keys(), ",") != "relay-12" {
		t.Fatalf("relay first: %d %v %s", r.status, u.keys(), r.body)
	}
	if !hasKey(e.accounts.lastTypes, "openai", "apikey") || !hasKey(e.accounts.lastTypes, "relay", "relay_key") ||
		hasKey(e.accounts.lastTypes, "anthropic", "apikey") {
		t.Fatalf("candidate types %v", e.accounts.lastTypes)
	}
	if rec := e.record(); rec.PluginKey != "relay" || rec.Platform != "openai" || rec.Tokens.Input != 30 {
		t.Fatalf("relay record %+v", rec)
	}
	if b := relay.builds[0]; b.GetAccount().GetPlatform() != "openai" || b.GetAccount().GetType() != "relay_key" {
		t.Fatalf("relay build %+v", b)
	}

	// Relay fails with 500: failover to the openai plugin's account.
	u.status["relay-12"] = 500
	if r := e.post("/v1/chat/completions", chatBody(false), bearer()); r.status != 200 {
		t.Fatalf("failover: %d %s", r.status, r.body)
	}
	if k := strings.Join(u.keys(), ","); k != "relay-12,relay-12,oai-11" {
		t.Fatalf("upstream order %s", k)
	}
	if rec := e.record(); rec.Attempts != 2 || rec.PluginKey != "openai" || *rec.AccountID != 11 {
		t.Fatalf("failover record %+v", rec)
	}
	if len(op.builds) != 1 {
		t.Fatalf("openai plugin builds %d", len(op.builds))
	}

	// The relay type also serves the anthropic endpoint, next to the
	// anthropic plugin's accounts (the relay account is cooling down now).
	delete(u.status, "relay-12")
	e.accounts.mu.Lock()
	delete(e.accounts.cooldown, 12)
	e.accounts.mu.Unlock()
	if r := e.messages(body(testModel, false)); r.status != 200 {
		t.Fatalf("anthropic endpoint: %d %s", r.status, r.body)
	}
	if !hasKey(e.accounts.lastTypes, "relay", "relay_key") || !hasKey(e.accounts.lastTypes, "anthropic", "apikey") ||
		hasKey(e.accounts.lastTypes, "openai", "apikey") {
		t.Fatalf("anthropic candidate types %v", e.accounts.lastTypes)
	}
	e.record()
}

// ---------------------------------------------------------------- Gemini

func TestGeminiEndpoints(t *testing.T) {
	e, u, _, gp := apiEnv(t)
	gen := map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}}}}

	// generateContent: model from the path, key from ?key=.
	r := e.post("/v1beta/models/gemini-2.5-pro:generateContent?key="+testKey, gen, nil)
	if r.status != 200 || r.json().Get("candidates.0.content.parts.0.text").String() != "llo" {
		t.Fatalf("generate: %d %s", r.status, r.body)
	}
	if c := u.last(); c.key != "gem-21" || c.path != "/v1beta/models/gemini-2.5-pro:generateContent" || strings.Contains(c.query, testKey) {
		t.Fatalf("upstream call %+v", c)
	}
	if b := gp.builds[0]; b.GetMeta().GetModel() != "gemini-2.5-pro" || b.GetMeta().GetStream() ||
		b.GetMeta().GetProtocol() != "gemini.generate" || b.GetAccount().GetPlatform() != "gemini" {
		t.Fatalf("build %+v", b)
	}
	rec := e.record()
	if !rec.Success || rec.Model != "gemini-2.5-pro" || rec.Stream || rec.Platform != "gemini" || rec.Protocol != "gemini.generate" ||
		rec.UpstreamModel != "gemini-2.5-pro-002" || rec.UsageSemantics != "inclusive" ||
		rec.Tokens != (core.UsageTokens{Input: 20, Output: 6, CacheRead: 4}) || !rec.Billable {
		t.Fatalf("generate record %+v", rec)
	}

	// streamGenerateContent with alt=sse and the x-goog-api-key header:
	// always streaming, SSE passes through, usage from the last event.
	r = e.post("/v1beta/models/gemini-2.5-pro:streamGenerateContent?alt=sse", gen, map[string]string{"x-goog-api-key": testKey})
	if r.status != 200 || !strings.HasPrefix(r.header.Get("Content-Type"), "text/event-stream") ||
		strings.Count(string(r.body), "data: ") != 2 {
		t.Fatalf("stream sse: %d %s", r.status, r.body)
	}
	if b := gp.builds[1]; !b.GetMeta().GetStream() || b.GetMeta().GetProtocol() != "gemini.stream_generate" {
		t.Fatalf("stream build %+v", b.GetMeta())
	}
	rec = e.record()
	if !rec.Stream || rec.Protocol != "gemini.stream_generate" || rec.Tokens != (core.UsageTokens{Input: 20, Output: 6, CacheRead: 4}) {
		t.Fatalf("stream record %+v", rec)
	}

	// Without alt=sse the client gets Google's JSON array stream.
	r = e.post("/v1beta/models/gemini-2.5-pro:streamGenerateContent?key="+testKey, gen, nil)
	arr := r.json()
	if r.status != 200 || !strings.HasPrefix(r.header.Get("Content-Type"), "application/json") || !arr.IsArray() ||
		len(arr.Array()) != 2 || arr.Get("1.usageMetadata.candidatesTokenCount").Int() != 6 {
		t.Fatalf("stream array: %d %s", r.status, r.body)
	}
	if rec = e.record(); rec.Tokens.Output != 6 || !rec.Success {
		t.Fatalf("array record %+v", rec)
	}

	// countTokens is free: never priced or billed.
	calls := e.pricer.calls
	r = e.post("/v1beta/models/gemini-2.5-pro:countTokens", gen, map[string]string{"x-goog-api-key": testKey})
	if r.status != 200 || r.json().Get("totalTokens").Int() != 31 || e.pricer.calls != calls {
		t.Fatalf("count tokens: %d %s", r.status, r.body)
	}
	if rec = e.record(); rec.Billable || rec.Protocol != "gemini.count_tokens" || rec.Model != "gemini-2.5-pro" {
		t.Fatalf("count record %+v", rec)
	}

	// Errors use the Gemini shape; the Authorization header is not a Gemini
	// key source.
	r = e.post("/v1beta/models/gemini-2.5-pro:generateContent", gen, bearer())
	if j := r.json(); r.status != 401 || j.Get("error.code").Int() != 401 || j.Get("error.status").String() != "UNAUTHENTICATED" ||
		j.Get("error.message").String() == "" {
		t.Fatalf("gemini auth error: %d %s", r.status, r.body)
	}
	// The group allowlist applies to the path model.
	e.auth.keys[testKey].Group.ModelAllowlist = []string{"gemini-2.5-flash"}
	r = e.post("/v1beta/models/gemini-2.5-pro:generateContent", gen, map[string]string{"x-goog-api-key": testKey})
	if j := r.json(); r.status != 404 || j.Get("error.status").String() != "NOT_FOUND" {
		t.Fatalf("allowlist: %d %s", r.status, r.body)
	}
	if rec = e.record(); rec.ErrorType != errTypeModelNotAllowed || rec.Model != "gemini-2.5-pro" {
		t.Fatalf("allowlist record %+v", rec)
	}
}

// The JSON array framing also holds when events arrive one by one: each
// element is written as soon as its event is complete.
func TestGeminiArrayStreamIncremental(t *testing.T) {
	e, _, _, _ := apiEnv(t)
	raw, _ := json.Marshal(map[string]any{"contents": []any{}})
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/v1beta/models/gemini-2.5-pro:streamGenerateContent", strings.NewReader(string(raw)))
	req.Header.Set("x-goog-api-key", testKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	first, err := br.ReadString('\n')
	if err != nil || !strings.HasPrefix(first, "[{") || !strings.HasSuffix(first, ",\r\n") {
		t.Fatalf("first element %q %v", first, err)
	}
	rest, _ := io.ReadAll(br)
	if !strings.HasSuffix(string(rest), "}]") {
		t.Fatalf("rest %q", rest)
	}
	e.record()
}
