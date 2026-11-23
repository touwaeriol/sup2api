// Command mock-upstream simulates the Anthropic, OpenAI and Gemini APIs for
// the sub2api-next test environment (docs/CONTRACTS.md §11.4, §14.1).
//
// Endpoints:
//
//	POST /v1/messages               streaming (SSE) and non-streaming responses with usage
//	POST /v1/messages/count_tokens  {"input_tokens": N}
//	POST /v1/chat/completions       OpenAI chat; streams send the usage chunk only when
//	                                stream_options.include_usage is true; a non-streaming
//	                                request offering the tool "submit_verdict" gets a
//	                                simulated moderation verdict (moderation.go)
//	POST /v1/responses              OpenAI Responses (usage in response.completed); with the image_generation
//	                                tool, its tools echo and tool_usage.image_gen (sites.go)
//	GET  /v1/responses              Responses WebSocket mode (ws.go)
//	POST /v1/embeddings             OpenAI embeddings (usage.prompt_tokens)
//	POST /v1/moderations            OpenAI moderation scores; MOD-HIT-<category> scores 0.99 (moderation_api.go)
//	POST /v1/systemone              TypeSafe System One noul answers, same MOD-HIT rule (moderation_api.go)
//	POST /{v1,v1beta}/models/{m}:generateContent   Gemini, usageMetadata incl. thoughtsTokenCount
//	POST /{v1,v1beta}/models/{m}:streamGenerateContent  ?alt=sse -> SSE, otherwise a JSON array
//	POST /{v1,v1beta}/models/{m}:countTokens       {"totalTokens": N}
//	POST /{v1,v1beta}/models/{m}:embedContent      {"embedding":{"values"}}, usageMetadata.promptTokenCount
//	POST /{v1,v1beta}/models/{m}:batchEmbedContents  {"embeddings":[{"values"}]}, one per request, the same usage
//	POST /openai/v1/{chat/completions,responses,embeddings}
//	                                Azure OpenAI v1: the OpenAI endpoints above, key in the api-key header
//	POST /openai/deployments/{d}/{chat/completions,embeddings}?api-version=YYYY-MM-DD[-preview]
//	                                Azure OpenAI classic: the deployment is the model (cloud.go)
//	POST /model/{id}/invoke         Bedrock InvokeModel for Claude: Anthropic JSON
//	POST /model/{id}/invoke-with-response-stream
//	                                the same as an AWS event stream (application/vnd.amazon.eventstream);
//	                                a SigV4 request must be signed with the secret "mock-secret-<access key id>",
//	                                scoped to the service bedrock and the region of bedrock-runtime.<region>.amazonaws.com
//	POST /{v1,v1beta1}/projects/{p}/locations/{l}/publishers/google/models/{m}:{action}
//	POST /{v1,v1beta1}/publishers/google/models/{m}:{action}
//	                                Vertex AI: the Gemini actions above
//	POST /backend-api/codex/responses  the Codex backend: OpenAI Responses, refused (400 {"detail"}) unless
//	                                stream is true and store is false (GET = WebSocket mode)
//	POST /api/accounts/deviceauth/{usercode,token}, POST /oauth/token
//	                                the OpenAI authorization server of the codex logins (openaiauth.go)
//	CONNECT host:port               HTTPS through the mock as a forward proxy (connect.go)
//	GET  /v1/models                 Anthropic/OpenAI style list {"data":[{"id":...}]}
//	GET  /v1beta/models             Gemini style list {"models":[{"name":"models/..."}]}
//	GET  /api/usage/token/, /v1/usage
//	                                New API and Sub2API balances (sites.go)
//	POST /v1/images/{generations,edits}  OpenAI image generation, JSON or SSE (sites.go)
//	GET  /__requests[?since=ID]     last 100 recorded requests (newest last)
//	DELETE /__requests              clear the request log
//	GET/POST/DELETE /__control      per-API-key behaviour rules (see controlRule)
//	ANY  /__webhook/*               accepts and records anything (plugin webhook target)
//	GET  /healthz
//
// MOCK_TLS_ADDR adds a TLS listener for vendor host names (tls.go); a
// request offering tools with the marker MOCK-TOOL-CALL gets a tool call
// (tools.go); the request log records the Host, TLS, and whether the request
// came as a forward proxy (an account's proxy pointing at the mock: an
// absolute URI, or inside a CONNECT tunnel).
//
// The upstream API key is read from x-api-key, api-key, Authorization (Bearer,
// or the access key id of a SigV4 credential), x-goog-api-key or ?key= (in
// that order) and recorded as "api_key".
//
// Behaviour for a request is resolved in this order (first match wins):
//  1. request headers x-mock-status / x-mock-delay-ms / x-mock-usage
//  2. a /__control rule for the request's API key (optionally limited to N uses)
//  3. markers inside the API key itself: "...-status-429", "...-delay-500"
//  4. body metadata.mock_status / metadata.mock_delay_ms
//
// Errors use the format of the endpoint's API (Anthropic, OpenAI or Google).
// The core only forwards whitelisted client headers to the upstream, so tests
// that go through the gateway use (2) or (3); (1) is for direct calls.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxLog = 100

// maxRecordedBody is how much of a request body the log keeps.
const maxRecordedBody = 64 << 10

// Usage is the token usage reported by the mock. The same numbers are
// reported in each API's own shape:
//   - Anthropic (exclusive): input_tokens, cache_read_input_tokens,
//     cache_creation_input_tokens (+ ephemeral_1h), output_tokens;
//   - OpenAI (inclusive): prompt_tokens / input_tokens = InputTokens, of
//     which cached_tokens = CacheReadTokens; completion_tokens /
//     output_tokens = OutputTokens (reasoning_tokens = ThoughtsTokens is a
//     detail included in it); cache creation is not reported;
//   - Gemini (inclusive): promptTokenCount = InputTokens, of which
//     cachedContentTokenCount = CacheReadTokens; candidatesTokenCount =
//     OutputTokens and, separately, thoughtsTokenCount = ThoughtsTokens.
type Usage struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_input_tokens"` // 5m + 1h
	CacheCreation1h     int64 `json:"cache_creation_1h_input_tokens"`
	ThoughtsTokens      int64 `json:"thoughts_tokens"`
}

// DefaultUsage is returned unless a rule or header overrides it. Tests assert
// billing against these numbers.
var DefaultUsage = Usage{InputTokens: 120, OutputTokens: 42, CacheReadTokens: 50, CacheCreationTokens: 30, CacheCreation1h: 0, ThoughtsTokens: 30}

// controlRule changes the behaviour for one API key.
type controlRule struct {
	APIKey  string `json:"api_key"`
	Status  int    `json:"status,omitempty"`   // error status to return (429|401|403|529|500|400...)
	DelayMS int    `json:"delay_ms,omitempty"` // delay before the response starts
	// ChunkDelayMS is the pause between SSE events (default 20ms).
	ChunkDelayMS int `json:"chunk_delay_ms,omitempty"`
	// Remaining limits how many requests the rule applies to; 0 = unlimited.
	Remaining int    `json:"remaining,omitempty"`
	Usage     *Usage `json:"usage,omitempty"`
	// Text overrides the assistant text.
	Text string `json:"text,omitempty"`
	// Balance replaces the answer of the balance endpoints (sites.go).
	Balance json.RawMessage `json:"balance,omitempty"`
}

// recorded is one entry of GET /__requests.
type recorded struct {
	ID             int64             `json:"id"`
	Time           time.Time         `json:"time"`
	Method         string            `json:"method"`
	Path           string            `json:"path"`
	Query          string            `json:"query,omitempty"`
	XAPIKey        string            `json:"x_api_key"`
	Authorization  string            `json:"authorization,omitempty"`
	APIKey         string            `json:"api_key"` // from whichever auth header/query carried it
	Headers        map[string]string `json:"headers"`
	Model          string            `json:"model,omitempty"`
	Stream         bool              `json:"stream"`
	MetadataUserID string            `json:"metadata_user_id,omitempty"`
	Body           string            `json:"body"`
	BodyTruncated  bool              `json:"body_truncated,omitempty"`
	Status         int               `json:"status"`
	// Host is the Host the request was sent to; TLS whether it came over TLS
	// (the TLS listener or a CONNECT tunnel); Proxied whether it reached the
	// mock as an HTTP forward proxy (absolute request URI, or inside a
	// CONNECT tunnel), i.e. through an account's proxy.
	Host    string `json:"host"`
	TLS     bool   `json:"tls,omitempty"`
	Proxied bool   `json:"proxied,omitempty"`
}

type server struct {
	mu     sync.Mutex
	nextID int64
	log    []recorded
	rules  map[string]*controlRule
	// devicePolled holds the device codes polled once (openaiauth.go).
	devicePolled map[string]bool
	// tunnels is where CONNECT hands its connections (connect.go).
	tunnels *tunnelListener
}

func main() {
	addr := os.Getenv("MOCK_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("mock-upstream listening on %s", addr)
	s := newServer()
	if os.Getenv("MOCK_TLS_ADDR") != "" {
		s.tunnels = newTunnelListener()
	}
	h := s.handler()
	go func() {
		if err := serveTLS(h, s.tunnels); err != nil {
			log.Fatalf("mock-upstream TLS: %v", err)
		}
	}()
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

func newServer() *server {
	return &server{rules: map[string]*controlRule{}, devicePolled: map[string]bool{}}
}

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("POST /v1/messages", s.messages)
	mux.HandleFunc("POST /v1/messages/count_tokens", s.countTokens)
	mux.HandleFunc("POST /v1/chat/completions", s.chatCompletions)
	mux.HandleFunc("POST /v1/responses", s.responses)
	mux.HandleFunc("GET /v1/responses", s.responsesWebSocket)
	mux.HandleFunc("POST /v1/embeddings", s.embeddings)
	mux.HandleFunc("POST /v1/moderations", s.moderations)
	mux.HandleFunc("POST /v1/systemone", s.systemOne)
	mux.HandleFunc("POST /v1beta/models/{spec}", s.gemini)
	mux.HandleFunc("POST /v1/models/{spec}", s.gemini)
	// The Codex backend (ChatGPT subscription accounts): the Responses API,
	// streamed only, and its WebSocket mode.
	mux.HandleFunc("POST /backend-api/codex/responses", s.responses)
	mux.HandleFunc("GET /backend-api/codex/responses", s.responsesWebSocket)
	s.cloudRoutes(mux)
	mux.HandleFunc("GET /v1/models", s.listModels)
	mux.HandleFunc("GET /v1beta/models", s.listGeminiModels)
	s.siteRoutes(mux)
	mux.HandleFunc("GET /__requests", s.listRequests)
	mux.HandleFunc("DELETE /__requests", s.clearRequests)
	mux.HandleFunc("GET /__control", s.listRules)
	mux.HandleFunc("POST /__control", s.setRule)
	mux.HandleFunc("DELETE /__control", s.clearRules)
	mux.HandleFunc("/__webhook/", s.webhook)
	s.openAIAuthRoutes(mux)
	s.codexRoutes(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			s.connect(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// requestKey returns the upstream API key of r: x-api-key (Anthropic),
// api-key (Azure OpenAI), Authorization: Bearer (OpenAI) or the access key
// id of a SigV4 Authorization (AWS), x-goog-api-key or ?key= (Gemini).
func requestKey(r *http.Request) string {
	if k := r.Header.Get("x-api-key"); k != "" {
		return k
	}
	if k := r.Header.Get("api-key"); k != "" {
		return k
	}
	if a := r.Header.Get("Authorization"); a != "" {
		if m := sigV4Authorization.FindStringSubmatch(a); m != nil {
			return m[1]
		}
		return strings.TrimSpace(strings.TrimPrefix(a, "Bearer "))
	}
	if k := r.Header.Get("x-goog-api-key"); k != "" {
		return k
	}
	return r.URL.Query().Get("key")
}

// ------------------------------------------------------------------ recording

func (s *server) record(r *http.Request, body []byte, model string, stream bool, userID string) int64 {
	h := map[string]string{}
	for k, v := range r.Header {
		h[strings.ToLower(k)] = strings.Join(v, ", ")
	}
	b := body
	trunc := false
	if len(b) > maxRecordedBody {
		b, trunc = b[:maxRecordedBody], true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	s.log = append(s.log, recorded{
		ID: s.nextID, Time: time.Now().UTC(), Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery,
		XAPIKey: r.Header.Get("x-api-key"), Authorization: r.Header.Get("Authorization"), APIKey: requestKey(r), Headers: h,
		Model: model, Stream: stream, MetadataUserID: userID, Body: string(b), BodyTruncated: trunc,
		Host: r.Host, TLS: r.TLS != nil, Proxied: tunneled(r) || strings.HasPrefix(r.RequestURI, "http://") || strings.HasPrefix(r.RequestURI, "https://"),
	})
	if len(s.log) > maxLog {
		s.log = s.log[len(s.log)-maxLog:]
	}
	return s.nextID
}

func (s *server) setStatus(id int64, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.log) - 1; i >= 0; i-- {
		if s.log[i].ID == id {
			s.log[i].Status = status
			return
		}
	}
}

func (s *server) listRequests(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	s.mu.Lock()
	out := make([]recorded, 0, len(s.log))
	for _, e := range s.log {
		if e.ID > since {
			out = append(out, e)
		}
	}
	last := s.nextID
	s.mu.Unlock()
	writeJSON(w, 200, map[string]any{"data": out, "last_id": last})
}

func (s *server) clearRequests(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.log = nil
	last := s.nextID
	s.mu.Unlock()
	writeJSON(w, 200, map[string]any{"last_id": last})
}

// ------------------------------------------------------------------ control

func (s *server) listRules(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	out := make([]controlRule, 0, len(s.rules))
	for _, r := range s.rules {
		out = append(out, *r)
	}
	s.mu.Unlock()
	writeJSON(w, 200, map[string]any{"data": out})
}

func (s *server) setRule(w http.ResponseWriter, r *http.Request) {
	var rule controlRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil || rule.APIKey == "" {
		writeJSON(w, 400, map[string]any{"error": "body must be a rule with api_key"})
		return
	}
	s.mu.Lock()
	s.rules[rule.APIKey] = &rule
	s.mu.Unlock()
	writeJSON(w, 200, map[string]any{"data": rule})
}

func (s *server) clearRules(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if k := r.URL.Query().Get("api_key"); k != "" {
		delete(s.rules, k)
	} else {
		s.rules = map[string]*controlRule{}
	}
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// behaviour is the resolved per-request behaviour.
type behaviour struct {
	status     int
	delay      time.Duration
	chunkDelay time.Duration
	usage      Usage
	text       string
	balance    json.RawMessage
}

var keyMarker = regexp.MustCompile(`-(status|delay)-(\d+)`)

func (s *server) resolve(r *http.Request, meta map[string]any) behaviour {
	b := behaviour{usage: DefaultUsage, chunkDelay: 20 * time.Millisecond, text: "Hello from mock-upstream."}
	key := requestKey(r)
	// Precedence, lowest applied first: body metadata < key markers < control
	// rule < explicit headers.
	applyBodyMeta(meta, &b)
	applyKeyMarkers(key, &b)
	s.mu.Lock()
	applyRule(s.rules, key, &b)
	s.mu.Unlock()
	applyHeaderOverrides(r, &b)
	return b
}

// applyBodyMeta applies the lowest-precedence overrides from body metadata.
func applyBodyMeta(meta map[string]any, b *behaviour) {
	if v, ok := meta["mock_status"].(float64); ok {
		b.status = int(v)
	}
	if v, ok := meta["mock_delay_ms"].(float64); ok {
		b.delay = time.Duration(v) * time.Millisecond
	}
}

// applyKeyMarkers applies status/delay markers embedded in the API key, e.g.
// "sk-x-status-500-delay-100".
func applyKeyMarkers(key string, b *behaviour) {
	for _, m := range keyMarker.FindAllStringSubmatch(key, -1) {
		n, _ := strconv.Atoi(m[2])
		if m[1] == "status" {
			b.status = n
		} else {
			b.delay = time.Duration(n) * time.Millisecond
		}
	}
}

// applyRule applies the control rule stored for key, consuming one use of a
// rule that has a remaining count. The caller holds the server lock.
func applyRule(rules map[string]*controlRule, key string, b *behaviour) {
	rule := rules[key]
	if rule == nil {
		return
	}
	if rule.Status != 0 {
		b.status = rule.Status
	}
	if rule.DelayMS != 0 {
		b.delay = time.Duration(rule.DelayMS) * time.Millisecond
	}
	if rule.ChunkDelayMS != 0 {
		b.chunkDelay = time.Duration(rule.ChunkDelayMS) * time.Millisecond
	}
	if rule.Usage != nil {
		b.usage = *rule.Usage
	}
	if rule.Text != "" {
		b.text = rule.Text
	}
	if len(rule.Balance) > 0 {
		b.balance = rule.Balance
	}
	if rule.Remaining > 0 {
		rule.Remaining--
		if rule.Remaining == 0 {
			delete(rules, key)
		}
	}
}

// applyHeaderOverrides applies the highest-precedence x-mock-* headers.
func applyHeaderOverrides(r *http.Request, b *behaviour) {
	if v := r.Header.Get("x-mock-status"); v != "" {
		b.status, _ = strconv.Atoi(v)
	}
	if v := r.Header.Get("x-mock-delay-ms"); v != "" {
		n, _ := strconv.Atoi(v)
		b.delay = time.Duration(n) * time.Millisecond
	}
	if v := r.Header.Get("x-mock-chunk-delay-ms"); v != "" {
		n, _ := strconv.Atoi(v)
		b.chunkDelay = time.Duration(n) * time.Millisecond
	}
	if v := r.Header.Get("x-mock-usage"); v != "" {
		// "input=1,output=2,cache_read=3,cache_creation=4,cache_creation_1h=5,thoughts=6"
		for _, kv := range strings.Split(v, ",") {
			k, val, _ := strings.Cut(strings.TrimSpace(kv), "=")
			n, _ := strconv.ParseInt(val, 10, 64)
			switch k {
			case "input":
				b.usage.InputTokens = n
			case "output":
				b.usage.OutputTokens = n
			case "cache_read":
				b.usage.CacheReadTokens = n
			case "cache_creation":
				b.usage.CacheCreationTokens = n
			case "cache_creation_1h":
				b.usage.CacheCreation1h = n
			case "thoughts":
				b.usage.ThoughtsTokens = n
			}
		}
	}
}

// ------------------------------------------------------------------ API

type messagesRequest struct {
	Model    string         `json:"model"`
	Stream   bool           `json:"stream"`
	Metadata map[string]any `json:"metadata"`
	Messages []any          `json:"messages"`
	System   any            `json:"system"`
}

func (s *server) messages(w http.ResponseWriter, r *http.Request) {
	body := readBody(r)
	var req messagesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		// begin assumes the body parsed, so the unparsed case records on its own.
		id := s.record(r, body, "", false, "")
		s.setStatus(id, 400)
		anthropicError(w, 400, "invalid_request_error", "invalid JSON body")
		return
	}
	id, b, ok := s.begin(w, r, body, req.Model, req.Stream, req.Metadata, formatAnthropic)
	if !ok {
		return
	}
	if req.Model == "" {
		s.setStatus(id, 400)
		anthropicError(w, 400, "invalid_request_error", "model: field required")
		return
	}
	s.setStatus(id, 200)
	msgID := fmt.Sprintf("msg_mock_%d_%06d", id, rand.IntN(1000000))
	w.Header().Set("request-id", fmt.Sprintf("req_mock_%d", id))
	w.Header().Set("x-mock-request-id", strconv.FormatInt(id, 10))
	tool, isTool := toolTurnOf(body)
	if isTool && tool.resultSeen {
		b.text = "TOOL-RESULT-SEEN " + b.text
	}
	if isTool && !tool.resultSeen {
		if req.Stream {
			messageToolEvents(startSSE(w, r, b.chunkDelay).send, req.Model, msgID, b, tool)
			return
		}
		writeJSON(w, 200, map[string]any{
			"id": msgID, "type": "message", "role": "assistant", "model": req.Model,
			"content":     []any{anthropicToolUse("toolu_"+msgID, tool.name, json.RawMessage(toolInput()))},
			"stop_reason": "tool_use", "stop_sequence": nil,
			"usage": usageJSON(b.usage, b.usage.OutputTokens),
		})
		return
	}
	if req.Stream {
		s.stream(w, r, req.Model, msgID, b)
		return
	}
	writeJSON(w, 200, map[string]any{
		"id": msgID, "type": "message", "role": "assistant", "model": req.Model,
		"content":       []any{map[string]any{"type": "text", "text": b.text}},
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"usage":         usageJSON(b.usage, b.usage.OutputTokens),
	})
}

func usageJSON(u Usage, output int64) map[string]any {
	return map[string]any{
		"input_tokens":                u.InputTokens,
		"cache_creation_input_tokens": u.CacheCreationTokens,
		"cache_read_input_tokens":     u.CacheReadTokens,
		"cache_creation": map[string]any{
			"ephemeral_5m_input_tokens": u.CacheCreationTokens - u.CacheCreation1h,
			"ephemeral_1h_input_tokens": u.CacheCreation1h,
		},
		"output_tokens": output,
	}
}

func (s *server) stream(w http.ResponseWriter, r *http.Request, model, msgID string, b behaviour) {
	messageEvents(startSSE(w, r, b.chunkDelay).send, model, msgID, b)
}

// messageEvents sends the events of a streamed Anthropic message through
// send, which reports whether to continue.
func messageEvents(send func(event string, data any) bool, model, msgID string, b behaviour) {
	if !send("message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": msgID, "type": "message", "role": "assistant", "model": model, "content": []any{},
		"stop_reason": nil, "stop_sequence": nil, "usage": usageJSON(b.usage, 1),
	}}) {
		return
	}
	if !send("content_block_start", map[string]any{"type": "content_block_start", "index": 0,
		"content_block": map[string]any{"type": "text", "text": ""}}) {
		return
	}
	if !send("ping", map[string]any{"type": "ping"}) {
		return
	}
	for _, part := range chunks(b.text) {
		if !send("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": part}}) {
			return
		}
	}
	if !send("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0}) {
		return
	}
	if !send("message_delta", map[string]any{"type": "message_delta",
		"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": b.usage.OutputTokens}}) {
		return
	}
	send("message_stop", map[string]any{"type": "message_stop"})
}

// chunks splits text into word-sized deltas (at least one).
func chunks(text string) []string {
	words := strings.SplitAfter(text, " ")
	out := make([]string, 0, len(words))
	for _, w := range words {
		if w != "" {
			out = append(out, w)
		}
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

func (s *server) countTokens(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	var req messagesRequest
	_ = json.Unmarshal(body, &req)
	id, _, ok := s.begin(w, r, body, req.Model, false, req.Metadata, formatAnthropic)
	if !ok {
		return
	}
	s.setStatus(id, 200)
	// Rough estimate: 1 token per 4 bytes of messages+system.
	n := int64(len(body)/4) + 1
	writeJSON(w, 200, map[string]any{"input_tokens": n})
}

// MockModels are the ids returned by the model list endpoints.
var MockModels = []string{"claude-sonnet-4-5", "claude-opus-4-1", "gpt-4.1", "gemini-2.5-pro"}

// listModels answers GET /v1/models (Anthropic and OpenAI share the shape;
// TypeSafe's host gets its own). A rule or key marker with a 4xx/5xx status
// applies, so tests can check how the console reports upstream failures.
func (s *server) listModels(w http.ResponseWriter, r *http.Request) {
	if isTypeSafe(r) {
		s.typesafeModels(w, r)
		return
	}
	id := s.record(r, nil, "", false, "")
	b := s.resolve(r, nil)
	if b.status >= 400 {
		s.setStatus(id, b.status)
		writeMockError(w, b.status)
		return
	}
	s.setStatus(id, 200)
	data := make([]map[string]any, 0, len(MockModels))
	for _, m := range MockModels {
		data = append(data, map[string]any{"id": m, "type": "model", "object": "model"})
	}
	writeJSON(w, 200, map[string]any{"data": data, "object": "list", "has_more": false})
}

// listGeminiModels answers GET /v1beta/models with resource names.
func (s *server) listGeminiModels(w http.ResponseWriter, r *http.Request) {
	id := s.record(r, nil, "", false, "")
	b := s.resolve(r, nil)
	if b.status >= 400 {
		s.setStatus(id, b.status)
		writeMockError(w, b.status)
		return
	}
	s.setStatus(id, 200)
	models := make([]map[string]any, 0, len(MockModels))
	for _, m := range MockModels {
		models = append(models, map[string]any{"name": "models/" + m, "displayName": m})
	}
	writeJSON(w, 200, map[string]any{"models": models})
}

func (s *server) webhook(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	id := s.record(r, body, "", false, "")
	s.setStatus(id, 200)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ------------------------------------------------------------------ helpers

func writeMockError(w http.ResponseWriter, status int) {
	switch status {
	case 400:
		anthropicError(w, 400, "invalid_request_error", "mock: invalid request")
	case 401:
		anthropicError(w, 401, "authentication_error", "mock: invalid x-api-key")
	case 403:
		anthropicError(w, 403, "permission_error", "mock: permission denied")
	case 429:
		w.Header().Set("retry-after", "2")
		anthropicError(w, 429, "rate_limit_error", "mock: rate limited")
	case 529:
		anthropicError(w, 529, "overloaded_error", "mock: overloaded")
	default:
		anthropicError(w, status, "api_error", "mock: internal error")
	}
}

func anthropicError(w http.ResponseWriter, status int, typ, msg string) {
	writeJSON(w, status, map[string]any{"type": "error", "error": map[string]any{"type": typ, "message": msg}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// sleep waits d or until the client goes away; it reports whether to continue.
func sleep(r *http.Request, d time.Duration) bool {
	if d <= 0 {
		return r.Context().Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-r.Context().Done():
		return false
	}
}
