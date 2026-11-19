package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/tidwall/gjson"
)

// Mock usage defaults (deploy/mock-upstream DefaultUsage). OpenAI and Gemini
// responses report the same numbers inclusively: prompt tokens =
// MockInputTokens of which MockCacheReadTokens are cached; Gemini adds
// thoughtsTokenCount = MockThoughtsTokens next to candidatesTokenCount =
// MockOutputTokens.
const (
	MockInputTokens         = 120
	MockOutputTokens        = 42
	MockCacheReadTokens     = 50
	MockCacheCreationTokens = 30
	MockThoughtsTokens      = 30
)

// MockRequest is one request recorded by mock-upstream.
type MockRequest struct {
	ID             int64
	Path           string
	Query          string
	XAPIKey        string
	APIKey         string // upstream key from x-api-key, Bearer, x-goog-api-key or ?key=
	Model          string
	Stream         bool
	MetadataUserID string
	Status         int
	Headers        gjson.Result
	Body           string
	Method         string
	Host           string // Host the request was sent to
	TLS            bool   // arrived on the mock's TLS listener (vendor host names)
	Proxied        bool   // arrived as a forward-proxy request (an account proxy)
}

// Mock controls mock-upstream through Caddy (/__mock).
type Mock struct {
	c *Client
}

// Mock returns the mock-upstream client.
func (e *Env) Mock() *Mock { return &Mock{c: NewClient(e.MockURL)} }

// Mark returns the id of the latest recorded request; pass it to Since.
func (m *Mock) Mark(t testing.TB) int64 {
	t.Helper()
	r := m.c.Do(t, http.MethodGet, "/__requests", nil, Query("since", "999999999999"))
	if r.Status != 200 {
		t.Fatalf("mock: %s", r)
	}
	return r.JSON().Get("last_id").Int()
}

// Since returns requests recorded after id (at most the last 100).
func (m *Mock) Since(t testing.TB, id int64) []MockRequest {
	t.Helper()
	r := m.c.Do(t, http.MethodGet, "/__requests", nil, Query("since", itoa(id)))
	if r.Status != 200 {
		t.Fatalf("mock: %s", r)
	}
	var out []MockRequest
	for _, x := range r.JSON().Get("data").Array() {
		out = append(out, MockRequest{
			ID: x.Get("id").Int(), Path: x.Get("path").String(), Query: x.Get("query").String(),
			XAPIKey: x.Get("x_api_key").String(), APIKey: x.Get("api_key").String(),
			Model: x.Get("model").String(), Stream: x.Get("stream").Bool(),
			MetadataUserID: x.Get("metadata_user_id").String(), Status: int(x.Get("status").Int()),
			Headers: x.Get("headers"), Body: x.Get("body").String(),
			Method: x.Get("method").String(), Host: x.Get("host").String(),
			TLS: x.Get("tls").Bool(), Proxied: x.Get("proxied").Bool(),
		})
	}
	return out
}

// MockRule mirrors mock-upstream's controlRule.
type MockRule struct {
	APIKey       string          `json:"api_key"`
	Status       int             `json:"status,omitempty"`
	DelayMS      int             `json:"delay_ms,omitempty"`
	ChunkDelayMS int             `json:"chunk_delay_ms,omitempty"`
	Remaining    int             `json:"remaining,omitempty"`
	Usage        *map[string]int `json:"usage,omitempty"`
	Text         string          `json:"text,omitempty"`
	// Balance replaces the answer of the upstream sites' balance reads.
	Balance json.RawMessage `json:"balance,omitempty"`
}

// SetRule installs a behaviour rule for one upstream API key.
func (m *Mock) SetRule(t testing.TB, r MockRule) {
	t.Helper()
	if resp := m.c.Do(t, http.MethodPost, "/__control", r); resp.Status != 200 {
		t.Fatalf("mock rule: %s", resp)
	}
}

// ClearRule removes the rule for apiKey ("" = all rules).
func (m *Mock) ClearRule(t testing.TB, apiKey string) {
	t.Helper()
	var opts []ReqOpt
	if apiKey != "" {
		opts = append(opts, Query("api_key", apiKey))
	}
	m.c.Do(t, http.MethodDelete, "/__control", nil, opts...)
}

// Header returns the recorded request header name (lower case).
func (r MockRequest) Header(name string) string { return r.Headers.Get(gjsonEscape(name)).String() }

// gjsonEscape escapes the gjson path characters of a header name.
func gjsonEscape(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if c := s[i]; c == '.' || c == '*' || c == '?' {
			out = append(out, '\\')
		}
		out = append(out, s[i])
	}
	return string(out)
}

// Key is the upstream API key of the request, whichever header carried it
// (x_api_key for mocks predating the api_key field).
func (r MockRequest) Key() string {
	if r.APIKey != "" {
		return r.APIKey
	}
	return r.XAPIKey
}

// KeysUsed returns the upstream API keys of requests to path ("" = any).
func KeysUsed(reqs []MockRequest, path string) []string {
	var out []string
	for _, r := range reqs {
		if path == "" || r.Path == path {
			out = append(out, r.Key())
		}
	}
	return out
}
