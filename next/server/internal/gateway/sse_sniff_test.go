package gateway

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

func TestSSEVerdict(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"", 0},
		{"\r\n ", 0},
		{"e", 0},
		{"even", 0},
		{"event: response.created\n", 1},
		{"data: {}", 1},
		{"\xef\xbb\xbfdata:", 1},
		{"\n\nid: 1", 1},
		{"retry: 10", 1},
		{": keep-alive", 1},
		{"{\"id\":1}", -1},
		{"[", -1},
		{"evil", -1},
		{"null", -1},
	} {
		if got := sseVerdict([]byte(tc.in)); got != tc.want {
			t.Errorf("sseVerdict(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestSniffEventStreamKeepsBody(t *testing.T) {
	for _, tc := range []struct {
		ct, body, want string
	}{
		{"application/json", "event: a\ndata: {}\n\n", "text/event-stream"},
		{"application/json", `{"usage":{}}`, "application/json"},
		{"", "da", ""}, // too short to tell: left as it is
		{"text/event-stream; charset=utf-8", "{}", "text/event-stream; charset=utf-8"},
	} {
		resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader(tc.body))}
		if tc.ct != "" {
			resp.Header.Set("Content-Type", tc.ct)
		}
		sniffEventStream(resp)
		got, _ := io.ReadAll(resp.Body)
		if resp.Header.Get("Content-Type") != tc.want || string(got) != tc.body {
			t.Errorf("%q %q: content-type %q, body %q", tc.ct, tc.body, resp.Header.Get("Content-Type"), got)
		}
	}
}

// siwcUpstream answers like Sign in with ChatGPT's Responses API: an event
// stream labeled application/json, whose usage reports cache writes.
func siwcUpstream(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		send := func(typ, data string) {
			_, _ = fmt.Fprintf(w, "event: %s\ndata: {\"type\":%q%s}\n\n", typ, typ, data)
			w.(http.Flusher).Flush()
		}
		send("response.created", `,"response":{"id":"resp_1","status":"in_progress"}`)
		send("response.output_text.delta", `,"delta":"hi"`)
		send("response.completed", `,"response":{"id":"resp_1","model":"gpt-6-luna","status":"completed","usage":{"input_tokens":100,`+
			`"input_tokens_details":{"cached_tokens":40,"cache_write_tokens":30},"output_tokens":7,"total_tokens":107}}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMislabeledEventStreamIsRelayedAndBilled(t *testing.T) {
	up := siwcUpstream(t)
	e := newEnv(t)
	plat := &fakePlatform{route: func(*pluginv1.ExecuteRequest) string { return up.URL + "/v1/responses" }}
	e.addAccountType("codexlike", "chatgpt", plat, manifest.AccountPlatform{Platform: manifest.PlatformOpenAI, Endpoints: []string{"responses"}})
	e.accounts.addTyped(testGroup, 10, 100, "codex-10", "codexlike", "chatgpt")
	e.accounts.groups[testGroup] = []int64{10}

	for _, path := range []string{"/v1/responses", "/responses"} {
		r := e.do(path, map[string]any{"model": testModel, "input": "hi", "stream": true},
			map[string]string{"authorization": "Bearer " + testKey, "x-api-key": ""})
		if r.status != 200 || !isSSE(r.header.Get("Content-Type")) || !bytes.Contains(r.body, []byte("event: response.completed")) {
			t.Fatalf("%s: %d %s %s", path, r.status, r.header.Get("Content-Type"), r.body)
		}
		rec := e.record()
		tok := rec.Tokens
		if tok.Input != 100 || tok.Output != 7 || tok.CacheRead != 40 || tok.CacheCreation != 30 || rec.FirstTokenMs == 0 {
			t.Fatalf("%s: tokens %+v first token %d", path, tok, rec.FirstTokenMs)
		}
		if rec.Endpoint != "/v1/responses" {
			t.Fatalf("%s: endpoint %q", path, rec.Endpoint)
		}
	}
}

func TestUnversionedPathOnlyForOpenAI(t *testing.T) {
	e := newEnv(t)
	// The anthropic messages endpoint is not served without /v1.
	if r := e.do("/messages", body(testModel, false), nil); r.status != http.StatusNotFound {
		t.Fatalf("/messages: %d %s", r.status, r.body)
	}
	e.noRecord()
}
