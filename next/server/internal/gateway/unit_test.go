package gateway

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"google.golang.org/protobuf/proto"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

func newTestContext(headers map[string]string) (*gin.Context, *http.Request) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	c.Request = req
	return c, req
}

func mustRegexp(s string) *regexp.Regexp { return regexp.MustCompile(s) }

func TestErrorFormats(t *testing.T) {
	render := func(format string, e *gwError) (int, gjson.Result, string) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		writeError(c, format, e)
		return w.Code, gjson.Parse(w.Body.String()), w.Header().Get("Content-Type")
	}
	e := fromCore(core.ErrInsufficientBalance, "")
	code, b, _ := render(FormatAnthropic, e)
	if code != 402 || b.Get("type").String() != "error" || b.Get("error.type").String() != "billing_error" ||
		b.Get("error.message").String() != "insufficient balance" || b.Get("error.code").String() != "insufficient_balance" {
		t.Fatalf("anthropic: %d %s", code, b.Raw)
	}
	code, b, _ = render(FormatOpenAI, fromCore(core.ErrRateLimited, ""))
	if code != 429 || b.Get("error.type").String() != "rate_limit_error" || b.Get("error.code").String() != "rate_limited" ||
		!b.Get("error.param").Exists() {
		t.Fatalf("openai: %d %s", code, b.Raw)
	}
	code, b, _ = render(FormatGemini, fromCore(core.ErrUnauthenticated, ""))
	if code != 401 || b.Get("error.code").Int() != 401 || b.Get("error.status").String() != "UNAUTHENTICATED" ||
		b.Get("error.details.0.reason").String() != "unauthenticated" {
		t.Fatalf("gemini: %d %s", code, b.Raw)
	}
	code, b, _ = render(FormatPlain, fromCore(core.ErrPluginUnavailable, ""))
	if code != 503 || b.Get("error.code").String() != "plugin_unavailable" || b.Get("error.message").String() == "" {
		t.Fatalf("plain: %d %s", code, b.Raw)
	}
	// Explicit protocol type wins; raw upstream JSON passes through.
	_, b, _ = render(FormatAnthropic, &gwError{Status: 429, Type: "rate_limit_error", Message: "slow down"})
	if b.Get("error.type").String() != "rate_limit_error" || b.Get("error.message").String() != "slow down" {
		t.Fatalf("typed: %s", b.Raw)
	}
	code, b, ct := render(FormatAnthropic, &gwError{Status: 418, Raw: []byte(`{"x":1}`), ContentType: "application/json; charset=utf-8"})
	if code != 418 || b.Get("x").Int() != 1 || !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("raw: %d %s %s", code, b.Raw, ct)
	}
}

func TestRouteMatching(t *testing.T) {
	gen := &fakeGen{}
	add := func(method, path string) {
		gen.endpoints = append(gen.endpoints, core.EndpointBinding{Plugin: core.PluginInfo{Key: "p"}, Platform: "p",
			Endpoint: manifest.Endpoint{Method: method, Path: path}})
	}
	add("POST", "/v1/messages")
	add("POST", "/v1/messages/:id")
	add("POST", "/v1/messages/count_tokens")
	add("GET", "/v1beta/models/*rest")
	add("POST", "/v1beta/models/:model:generateContent")
	add("POST", "/v1beta/models/:model:streamGenerateContent")
	add("POST", "/v1beta/models/:model")
	add("POST", "/v1/messages")        // duplicate ignored
	add("POST", "/v1/messages/:other") // same pattern, other name: ignored
	add("POST", "/v1beta/models/:m:countTokens")
	tb := buildRouteTable(gen)
	cases := []struct {
		in, want string
		params   map[string]string
	}{
		{"POST /v1/messages", "/v1/messages", nil},
		{"POST /v1/messages/", "/v1/messages", nil},
		{"POST /v1/messages/count_tokens", "/v1/messages/count_tokens", nil},
		{"POST /v1/messages/abc", "/v1/messages/:id", map[string]string{"id": "abc"}},
		{"GET /v1beta/models/a/b", "/v1beta/models/*rest", map[string]string{"rest": "a/b"}},
		{"POST /v1beta/models/gemini-2.5-pro:generateContent", "/v1beta/models/:model:generateContent",
			map[string]string{"model": "gemini-2.5-pro"}},
		{"POST /v1beta/models/gemini-2.5-flash:streamGenerateContent", "/v1beta/models/:model:streamGenerateContent",
			map[string]string{"model": "gemini-2.5-flash"}},
		{"POST /v1beta/models/g:countTokens", "/v1beta/models/:m:countTokens", map[string]string{"m": "g"}},
		// A value is required before the suffix; other suffixes fall back
		// to the plain parameter.
		{"POST /v1beta/models/:generateContent", "/v1beta/models/:model", map[string]string{"model": ":generateContent"}},
		{"POST /v1beta/models/gemini:embedContent", "/v1beta/models/:model", map[string]string{"model": "gemini:embedContent"}},
		{"POST /v1beta/models/a/b:generateContent", "", nil},
		{"GET /v1/messages", "", nil},
		{"POST /v1/messages/a/b", "", nil},
		{"POST /v2/messages", "", nil},
	}
	for _, tc := range cases {
		parts := strings.SplitN(tc.in, " ", 2)
		r, params := tb.match(parts[0], parts[1])
		got := ""
		if r != nil {
			got = r.binding.Endpoint.Path
		}
		if got != tc.want {
			t.Errorf("%s -> %q, want %q", tc.in, got, tc.want)
			continue
		}
		if len(params) != len(tc.params) {
			t.Errorf("%s params %v, want %v", tc.in, params, tc.params)
		}
		for k, v := range tc.params {
			if params[k] != v {
				t.Errorf("%s params %v, want %v", tc.in, params, tc.params)
			}
		}
	}
	if len(tb.routes) != 8 {
		t.Fatalf("duplicates not dropped: %d routes", len(tb.routes))
	}
	if r, _ := buildRouteTable(nil).match("POST", "/v1/messages"); r != nil {
		t.Fatal("nil generation matched")
	}

	// A built-in endpoint wins over a conflicting plugin endpoint whatever
	// the order the registry lists them in.
	gen = &fakeGen{endpoints: []core.EndpointBinding{
		{Plugin: core.PluginInfo{Key: "rogue"}, Platform: "rogue", Endpoint: manifest.Endpoint{Method: "POST", Path: "/v1/chat/completions"}},
		{Platform: "openai", Endpoint: manifest.Endpoint{Method: "POST", Path: "/v1/chat/completions"}},
	}}
	if r, _ := buildRouteTable(gen).match("POST", "/v1/chat/completions"); r == nil || r.binding.Platform != "openai" {
		t.Fatalf("builtin must win: %+v", r)
	}

	// The built-in platforms route as declared.
	tb = buildRouteTable(newFakeGen())
	for path, want := range map[string]string{
		"/v1/messages":              "anthropic.messages",
		"/v1/messages/count_tokens": "anthropic.count_tokens",
		"/v1/chat/completions":      "openai.chat",
		"/v1/responses":             "openai.responses",
		"/v1/embeddings":            "openai.embeddings",
		"/v1beta/models/gemini-2.5-pro:generateContent":       "gemini.generate",
		"/v1beta/models/gemini-2.5-pro:streamGenerateContent": "gemini.stream_generate",
		"/v1beta/models/gemini-2.5-pro:countTokens":           "gemini.count_tokens",
	} {
		r, _ := tb.match("POST", path)
		if r == nil || r.binding.Endpoint.Protocol != want || !strings.HasPrefix(want, r.binding.Platform+".") {
			t.Errorf("%s -> %+v, want %s", path, r, want)
		}
	}
}

func TestExtractPromptText(t *testing.T) {
	b := []byte(`{
	  "system": [{"type":"text","text":"sys A"},{"type":"text","text":"sys B"}],
	  "messages": [
	    {"role":"user","content":"plain string"},
	    {"role":"assistant","content":[{"type":"text","text":"answer"},{"type":"tool_use","input":{"q":"x"}}]},
	    {"role":"user","content":[{"type":"tool_result","content":[{"type":"text","text":"tool out"}]},{"type":"image","source":{}}]}
	  ]}`)
	got := extractPromptText(b, []string{"system", "messages.#.content"}, 0)
	want := "sys A\nsys B\nplain string\nanswer\ntool out"
	if got != want {
		t.Fatalf("prompt text %q, want %q", got, want)
	}
	if s := extractPromptText(b, []string{"system", "messages.#.content"}, 7); s != "sys A\ns" {
		t.Fatalf("truncated %q", s)
	}
	// Multi-byte runes are never split.
	if s := truncateUTF8("héllo", 2); s != "h" {
		t.Fatalf("utf8 truncate %q", s)
	}
}

func TestUsageExtraction(t *testing.T) {
	anth := builtinPlatform(t, "anthropic").Usage
	u := newUsageAcc(anth)
	u.ApplySSE("message_start", []byte(`{"type":"message_start","message":{"model":"claude-x","usage":{"input_tokens":100,"output_tokens":1,"cache_read_input_tokens":20,"cache_creation_input_tokens":30,"cache_creation":{"ephemeral_1h_input_tokens":10}}}}`))
	u.ApplySSE("content_block_delta", []byte(`{"type":"content_block_delta","delta":{"text":"hi"}}`))
	u.ApplySSE("message_delta", []byte(`{"type":"message_delta","usage":{"output_tokens":55}}`))
	got := u.Tokens()
	want := core.UsageTokens{Input: 100, Output: 55, CacheRead: 20, CacheCreation: 20, CacheCreation1h: 10}
	if got != want || u.Model != "claude-x" {
		t.Fatalf("sse usage %+v model %s", got, u.Model)
	}
	// Events without an "event:" line fall back to the data "type".
	u = newUsageAcc(anth)
	u.ApplySSE("", []byte(`{"type":"message_delta","usage":{"output_tokens":9}}`))
	if u.Tokens().Output != 9 {
		t.Fatalf("type fallback: %d", u.Tokens().Output)
	}
	// 1h larger than total never goes negative.
	u = newUsageAcc(anth)
	u.ApplyJSON([]byte(`{"usage":{"input_tokens":1,"cache_creation_input_tokens":2,"cache_creation":{"ephemeral_1h_input_tokens":5}}}`))
	if tk := u.Tokens(); tk.CacheCreation != 0 || tk.CacheCreation1h != 5 {
		t.Fatalf("clamp %+v", tk)
	}
	// Facts and extra map keys become metrics.
	rules := manifest.UsageRules{JSON: &manifest.UsageMap{Map: map[string]string{"images": "usage.images"}},
		Facts: map[string]manifest.UsageFact{"seconds": {Type: "number", Path: "usage.seconds"}, "hd": {Type: "boolean", Path: "hd"}}}
	u = newUsageAcc(rules)
	u.ApplyJSON([]byte(`{"hd":true,"usage":{"images":2,"seconds":1.5}}`))
	if u.Metrics["images"] != 2.0 || u.Metrics["seconds"] != 1.5 || u.Metrics["hd"] != true {
		t.Fatalf("metrics %v", u.Metrics)
	}
	// Stream error events are noticed, named or not.
	u = newUsageAcc(anth)
	u.ApplySSE("error", []byte(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`))
	if !strings.Contains(u.StreamError, "Overloaded") {
		t.Fatalf("stream error %q", u.StreamError)
	}
	u = newUsageAcc(builtinPlatform(t, "openai").Usage)
	u.ApplySSE("", []byte(`{"error":{"message":"server busy","type":"server_error"}}`))
	if !strings.Contains(u.StreamError, "server busy") {
		t.Fatalf("unnamed stream error %q", u.StreamError)
	}
}

// The built-in platforms' usage rules against real response shapes.
func TestBuiltinUsageRules(t *testing.T) {
	oai := builtinPlatform(t, "openai")
	gem := builtinPlatform(t, "gemini")
	tokens := func(u *usageAcc) core.UsageTokens { return u.Tokens() }

	// OpenAI chat: JSON, and SSE where only the last chunk carries usage.
	u := newUsageAcc(oai.Usage)
	u.ApplyJSON([]byte(`{"model":"gpt-4o-2024","usage":{"prompt_tokens":30,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":12}}}`))
	if tokens(u) != (core.UsageTokens{Input: 30, Output: 7, CacheRead: 12}) || u.Model != "gpt-4o-2024" {
		t.Fatalf("chat json %+v %s", tokens(u), u.Model)
	}
	u = newUsageAcc(oai.Usage)
	for _, d := range []string{
		`{"model":"gpt-4o-2024","choices":[{"delta":{"content":"he"}}],"usage":null}`,
		`{"model":"gpt-4o-2024","choices":[{"delta":{"content":"llo"}}],"usage":null}`,
		`{"model":"gpt-4o-2024","choices":[],"usage":{"prompt_tokens":30,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":0}}}`,
		`[DONE]`,
	} {
		u.ApplySSE("", []byte(d))
	}
	if tokens(u) != (core.UsageTokens{Input: 30, Output: 7}) {
		t.Fatalf("chat sse %+v", tokens(u))
	}

	// OpenAI responses (endpoint rules). output_tokens already includes
	// output_tokens_details.reasoning_tokens (as chat completion_tokens does),
	// so no sum is needed.
	resp := *endpointOf(t, oai, "openai.responses").Usage
	u = newUsageAcc(resp)
	u.ApplySSE("response.created", []byte(`{"type":"response.created","response":{"model":"gpt-5","usage":null}}`))
	u.ApplySSE("response.completed", []byte(`{"type":"response.completed","response":{"model":"gpt-5","usage":{"input_tokens":40,"output_tokens":9,"input_tokens_details":{"cached_tokens":8},"output_tokens_details":{"reasoning_tokens":5}}}}`))
	if tokens(u) != (core.UsageTokens{Input: 40, Output: 9, CacheRead: 8}) || u.Model != "gpt-5" {
		t.Fatalf("responses sse %+v", tokens(u))
	}
	u = newUsageAcc(resp)
	u.ApplyJSON([]byte(`{"model":"gpt-5","usage":{"input_tokens":40,"output_tokens":9,"input_tokens_details":{"cached_tokens":8}}}`))
	if tokens(u) != (core.UsageTokens{Input: 40, Output: 9, CacheRead: 8}) {
		t.Fatalf("responses json %+v", tokens(u))
	}
	// Embeddings.
	u = newUsageAcc(*endpointOf(t, oai, "openai.embeddings").Usage)
	u.ApplyJSON([]byte(`{"object":"list","model":"text-embedding-3-small","usage":{"prompt_tokens":5,"total_tokens":5}}`))
	if tokens(u) != (core.UsageTokens{Input: 5}) || u.Model != "text-embedding-3-small" {
		t.Fatalf("embeddings %+v", tokens(u))
	}

	// Gemini: JSON, SSE (last usageMetadata wins), JSON array stream.
	g1 := `{"candidates":[{"content":{"parts":[{"text":"he"}]}}],"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":1},"modelVersion":"gemini-2.5-pro"}`
	g2 := `{"candidates":[{"content":{"parts":[{"text":"llo"}]}}],"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":6,"cachedContentTokenCount":4,"thoughtsTokenCount":3},"modelVersion":"gemini-2.5-pro"}`
	// output_tokens = candidatesTokenCount + thoughtsTokenCount (CONTRACTS §14.1).
	want := core.UsageTokens{Input: 20, Output: 9, CacheRead: 4}
	u = newUsageAcc(gem.Usage)
	u.ApplyJSON([]byte(g2))
	if tokens(u) != want || u.Model != "gemini-2.5-pro" || u.Metrics["thoughts_tokens"] != 3.0 {
		t.Fatalf("gemini json %+v %v", tokens(u), u.Metrics)
	}
	u = newUsageAcc(gem.Usage)
	u.ApplySSE("", []byte(g1))
	u.ApplySSE("", []byte(g2))
	if tokens(u) != want {
		t.Fatalf("gemini sse %+v", tokens(u))
	}
	u = newUsageAcc(gem.Usage)
	u.ApplyJSON([]byte("[" + g1 + ",\r\n" + g2 + "]"))
	if tokens(u) != want {
		t.Fatalf("gemini json array %+v", tokens(u))
	}
}

func TestBuiltinPromptText(t *testing.T) {
	oai, gem := builtinPlatform(t, "openai"), builtinPlatform(t, "gemini")
	chat := `{"messages":[{"role":"system","content":"be nice"},{"role":"user","content":[{"type":"text","text":"hi"},{"type":"image_url","image_url":{"url":"x"}}]}]}`
	if s := extractPromptText([]byte(chat), endpointOf(t, oai, "openai.chat").Request.PromptTextPaths, 0); s != "be nice\nhi" {
		t.Fatalf("chat prompt %q", s)
	}
	responses := `{"instructions":"sys","input":[{"role":"user","content":[{"type":"input_text","text":"q1"}]},{"role":"user","content":"q2"}]}`
	if s := extractPromptText([]byte(responses), endpointOf(t, oai, "openai.responses").Request.PromptTextPaths, 0); s != "sys\nq1\nq2" {
		t.Fatalf("responses prompt %q", s)
	}
	if s := extractPromptText([]byte(`{"input":"plain"}`), endpointOf(t, oai, "openai.responses").Request.PromptTextPaths, 0); s != "plain" {
		t.Fatalf("responses string input %q", s)
	}
	gb := `{"systemInstruction":{"parts":[{"text":"sys"}]},"contents":[{"role":"user","parts":[{"text":"a"},{"inlineData":{}}]},{"role":"model","parts":[{"text":"b"}]}]}`
	if s := extractPromptText([]byte(gb), endpointOf(t, gem, "gemini.generate").Request.PromptTextPaths, 0); s != "sys\na\nb" {
		t.Fatalf("gemini prompt %q", s)
	}
}

func TestSSRFCheck(t *testing.T) {
	g := &Gateway{lookupIP: func(_ context.Context, host string) ([]net.IP, error) {
		switch host {
		case "internal.example":
			return []net.IP{net.ParseIP("10.1.2.3")}, nil
		case "mixed.example":
			return []net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("169.254.169.254")}, nil
		}
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	}}
	ctx := context.Background()
	bad := []string{"ftp://api.example/x", "http://127.0.0.1:8080/v1", "http://[::1]/x", "http://localhost/x",
		"http://169.254.169.254/latest", "http://internal.example/x", "http://mixed.example/x", "http://user:pw@api.example/x",
		"http://[::ffff:10.0.0.1]/x", "http://100.64.1.1/x", "/relative", ""}
	for _, u := range bad {
		if _, err := g.checkUpstream(ctx, u, false); err == nil {
			t.Errorf("%q accepted", u)
		}
	}
	for _, u := range []string{"https://api.anthropic.com/v1/messages", "http://8.8.8.8/x"} {
		if _, err := g.checkUpstream(ctx, u, false); err != nil {
			t.Errorf("%q rejected: %v", u, err)
		}
	}
	g.allowPrivate = true
	if _, err := g.checkUpstream(ctx, "http://127.0.0.1:1/x", false); err != nil {
		t.Fatalf("allow private: %v", err)
	}
	if _, err := g.checkUpstream(ctx, "file:///etc/passwd", false); err == nil {
		t.Fatal("scheme must still be checked")
	}
}

// A relay, handshake or fetch of an account with a proxy is resolved by the
// proxy: the name is not resolved here, so one only the proxy can resolve
// (or with the node's DNS down) passes, while a literal private address is
// refused. An account without a proxy is checked as before.
func TestSSRFCheckFollowsTheAccountProxy(t *testing.T) {
	var lookups atomic.Int32
	g := &Gateway{lookupIP: func(context.Context, string) ([]net.IP, error) {
		lookups.Add(1)
		return nil, errors.New("no such host")
	}}
	c := &call{g: g}
	proxied := &execution{c: c, egress: core.Egress{Proxied: true}}
	direct := &execution{c: c}
	ctx := context.Background()
	if _, err := proxied.checkURL(ctx, "https://upstream.proxy-side.example/v1"); err != nil || lookups.Load() != 0 {
		t.Fatalf("proxied name: %v, %d local lookups", err, lookups.Load())
	}
	for _, u := range []string{"http://127.0.0.1:8080/v1", "http://10.1.2.3/x", "http://localhost/x", "http://[::1]/x"} {
		if _, err := proxied.checkURL(ctx, u); err == nil {
			t.Errorf("proxied %q accepted", u)
		}
	}
	if _, err := direct.checkURL(ctx, "https://upstream.proxy-side.example/v1"); err == nil || lookups.Load() != 1 {
		t.Fatalf("direct unresolvable name: %v, %d lookups", err, lookups.Load())
	}
}

func TestGlobAndLists(t *testing.T) {
	if !globMatch("claude-*", "claude-sonnet-4") || globMatch("claude-*", "gpt-4") || !globMatch("a?c", "abc") ||
		!globMatch("*/*", "openai/gpt") {
		t.Fatal("glob")
	}
	if !matchList(nil, "x") || !matchList([]string{"*"}, "x") || matchList([]string{"a"}, "b") || !matchList([]string{"vip"}, "3", "vip") {
		t.Fatal("matchList")
	}
	if !pathCovered("metadata.user_id", []string{"metadata"}) || pathCovered("metadatax", []string{"metadata"}) ||
		pathCovered("model", nil) {
		t.Fatal("pathCovered")
	}
}

func TestP99(t *testing.T) {
	b := make([]int64, len(latencyBuckets)+1)
	b[2] = 98 // <= 5ms
	b[6] = 2  // <= 100ms
	if p := p99(b, 100); p != 100 {
		t.Fatalf("p99 = %d", p)
	}
	if p99(b, 0) != 0 {
		t.Fatal("empty")
	}
}

// metaCall builds a call for meta() with the given endpoint, path parameters
// and raw query string.
func metaCall(ep manifest.Endpoint, params map[string]string, rawQuery string) *call {
	w := httptest.NewRecorder()
	gc, _ := gin.CreateTestContext(w)
	target := "/v1beta/models/gemini-2.5-pro:generateContent"
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	gc.Request = httptest.NewRequest(http.MethodPost, target, nil)
	return &call{g: &Gateway{}, c: gc, ep: ep, params: params, rid: "rid-1", model: "gemini-2.5-pro"}
}

// RequestMeta carries the matched path parameters so plugins can read the
// model (and anything else) out of the URL.
func TestMetaPathParams(t *testing.T) {
	ep := manifest.Endpoint{Protocol: "gemini.generate", Request: manifest.EndpointRequest{ModelParam: "model"}}
	m := metaCall(ep, map[string]string{"model": "gemini-2.5-pro"}, "").meta()
	if got := m.GetPathParams(); len(got) != 1 || got["model"] != "gemini-2.5-pro" {
		t.Fatalf("path params = %v", got)
	}
}

// path_params carries what the client sent, every entry of any size (only
// invalid UTF-8 is dropped), and is nil (not an empty map) when the pattern
// has no parameters.
func TestMetaMapsWhole(t *testing.T) {
	params := map[string]string{}
	for i := 0; i < 40; i++ {
		params[fmt.Sprintf("p%02d", i)] = "v"
	}
	pp := metaCall(manifest.Endpoint{Protocol: "p.x"}, params, "").meta().GetPathParams()
	if len(pp) != len(params) {
		t.Fatalf("size = %d", len(pp))
	}
	longKey, longVal := strings.Repeat("é", 100), strings.Repeat("ü", 600)
	got := metaCall(manifest.Endpoint{Protocol: "p.x"}, map[string]string{longKey: longVal, "bad\xff": "v\xffw"}, "").meta().GetPathParams()
	if len(got) != 2 || got[longKey] != longVal || got["bad"] != "vw" {
		t.Fatalf("long and invalid: %v", got)
	}
	// No path parameters: the map stays nil.
	if m := metaCall(manifest.Endpoint{Protocol: "p.x"}, nil, "").meta(); m.GetPathParams() != nil {
		t.Fatalf("empty path params = %v", m.GetPathParams())
	}
}

// RequestMeta.query carries the parameters the endpoint declared in
// request.queryParams and nothing else. The query string can carry
// credentials — the endpoint's auth.query parameter, and whatever an upstream
// invents — so the host never ships the whole thing and never guesses which
// names are secret.
func TestMetaQueryIsDeclaredOnly(t *testing.T) {
	ep := manifest.Endpoint{Protocol: "gemini.generate", Auth: manifest.EndpointAuth{Query: "key"},
		Request: manifest.EndpointRequest{QueryParams: []string{"alt", "page"}}}
	q := metaCall(ep, map[string]string{"model": "x"}, "key=sk-secret&alt=sse&page=2&tracking=t").meta().GetQuery()
	if len(q) != 2 || q["alt"] != "sse" || q["page"] != "2" {
		t.Fatalf("query = %v", q)
	}

	// Names are matched case-insensitively in both directions: a declared
	// "alt" picks up "?Alt", and auth.query "key" still excludes "?Key"
	// (url.Values is a case-sensitive map, so an exact delete would leak it).
	q = metaCall(ep, nil, "Alt=sse&Key=sk-secret&KEY=sk-2").meta().GetQuery()
	if len(q) != 1 || q["Alt"] != "sse" {
		t.Fatalf("case-insensitive query = %v", q)
	}

	// An endpoint declaring nothing gets nothing, not even harmless-looking
	// parameters, and the map is nil rather than empty.
	if q := metaCall(manifest.Endpoint{Protocol: "p.x"}, nil, "alt=sse").meta().GetQuery(); q != nil {
		t.Fatalf("undeclared query = %v", q)
	}
	// Declared but absent: still nil.
	if q := metaCall(ep, nil, "other=1").meta().GetQuery(); q != nil {
		t.Fatalf("absent query = %v", q)
	}
	// Only the first value of a repeated parameter travels.
	if q := metaCall(ep, nil, "alt=a&alt=b").meta().GetQuery(); len(q) != 1 || q["alt"] != "a" {
		t.Fatalf("repeated query = %v", q)
	}
	// Two spellings of the same declared name: the choice is stable.
	for i := 0; i < 20; i++ {
		if q := metaCall(ep, nil, "Alt=upper&alt=lower").meta().GetQuery(); len(q) != 1 || q["Alt"] != "upper" {
			t.Fatalf("mixed-case query = %v", q)
		}
	}
}

// A client can put arbitrary bytes in the path. proto3 string fields must be
// valid UTF-8, so invalid bytes are dropped before they can break the
// marshalling of every plugin call of the request.
func TestMetaMapsDropInvalidUTF8(t *testing.T) {
	m := metaCall(manifest.Endpoint{Protocol: "p.x"},
		map[string]string{"model": "a\xffb", "\xff": "v"}, "").meta()
	if got := m.GetPathParams(); len(got) != 1 || got["model"] != "ab" {
		t.Fatalf("path params = %q", got)
	}
	if _, err := proto.Marshal(m); err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// The query goes through the same sanitiser: it also comes straight from
	// the URL, so "?alt=%FF" would otherwise make proto.Marshal fail and turn
	// one curl into a reliable 500 on every plugin call of the request.
	ep := manifest.Endpoint{Protocol: "p.x", Request: manifest.EndpointRequest{QueryParams: []string{"alt", "bad"}}}
	m = metaCall(ep, nil, "alt=a%FFb&bad=%FF").meta()
	q := m.GetQuery()
	if len(q) != 2 || q["alt"] != "ab" || q["bad"] != "" {
		t.Fatalf("query = %q", q)
	}
	if _, err := proto.Marshal(m); err != nil {
		t.Fatalf("marshal: %v", err)
	}
}

// The query map carries the declared parameters whole, like path_params.
func TestMetaQueryWhole(t *testing.T) {
	names := make([]string, 0, 40)
	var raw []string
	for i := 0; i < 40; i++ {
		n := fmt.Sprintf("p%02d", i)
		names = append(names, n)
		raw = append(raw, n+"=v")
	}
	ep := manifest.Endpoint{Protocol: "p.x", Request: manifest.EndpointRequest{QueryParams: names}}
	q := metaCall(ep, nil, strings.Join(raw, "&")).meta().GetQuery()
	if len(q) != len(names) {
		t.Fatalf("size = %d", len(q))
	}
	longVal := strings.Repeat("ü", 600)
	ep2 := manifest.Endpoint{Protocol: "p.x", Request: manifest.EndpointRequest{QueryParams: []string{"alt"}}}
	q = metaCall(ep2, nil, "alt="+url.QueryEscape(longVal)).meta().GetQuery()
	if len(q) != 1 || q["alt"] != longVal {
		t.Fatalf("value %d bytes", len(q["alt"]))
	}
}

// An SSE line of any length is read whole, far beyond the reader's buffer.
func TestReadLineAnyLength(t *testing.T) {
	long := strings.Repeat("x", 17<<20)
	br := bufio.NewReaderSize(strings.NewReader("data: "+long+"\nnext\n"), 64<<10)
	line, err := readLine(br)
	if err != nil || string(line) != "data: "+long+"\n" {
		t.Fatalf("long line: %d bytes, %v", len(line), err)
	}
	if line, err = readLine(br); err != nil || string(line) != "next\n" {
		t.Fatalf("next line: %q %v", line, err)
	}
}
