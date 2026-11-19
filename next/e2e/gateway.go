package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

// SSEEvent is one server-sent event.
type SSEEvent struct {
	Event string
	Data  gjson.Result
	At    time.Duration // since the request was sent
}

// GatewayResult is the outcome of a gateway call.
type GatewayResult struct {
	Status    int
	Header    http.Header
	RequestID string
	Body      []byte     // non-stream body (or error body)
	Events    []SSEEvent // stream events
	ServedBy  string     // Caddy X-Served-By (node-N:8080) when called via the LB
}

// JSON parses the non-stream body.
func (g *GatewayResult) JSON() gjson.Result { return gjson.ParseBytes(g.Body) }

// EventTypes lists the SSE event names in order.
func (g *GatewayResult) EventTypes() []string {
	out := make([]string, 0, len(g.Events))
	for _, e := range g.Events {
		out = append(out, e.Event)
	}
	return out
}

// Text concatenates the assistant text (stream or non-stream).
func (g *GatewayResult) Text() string {
	if len(g.Events) == 0 {
		return g.JSON().Get("content.0.text").String()
	}
	var b strings.Builder
	for _, e := range g.Events {
		if e.Event == "content_block_delta" {
			b.WriteString(e.Data.Get("delta.text").String())
		}
	}
	return b.String()
}

// MessagesBody builds an Anthropic Messages request body.
func MessagesBody(model, prompt string, stream bool) map[string]any {
	return map[string]any{
		"model":      model,
		"max_tokens": 64,
		"stream":     stream,
		"messages":   []any{map[string]any{"role": "user", "content": prompt}},
	}
}

// WithSession adds metadata.user_id (the default sticky-session key).
func WithSession(body map[string]any, session string) map[string]any {
	body["metadata"] = map[string]any{"user_id": session}
	return body
}

// ChatBody builds an OpenAI chat completions request body.
func ChatBody(model, prompt string, stream bool) map[string]any {
	return map[string]any{
		"model":    model,
		"stream":   stream,
		"messages": []any{map[string]any{"role": "user", "content": prompt}},
	}
}

// GeminiBody builds a Gemini generateContent request body.
func GeminiBody(prompt string) map[string]any {
	return map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": prompt}}}}}
}

// OpenAI calls an openai platform endpoint (path) through the load
// balancer with the API key as Authorization: Bearer.
func (e *Env) OpenAI(apiKey, path string, body any) *GatewayResult {
	e.T.Helper()
	return e.Gateway(e.T, e.BaseURL, path, "", body, map[string]string{"Authorization": "Bearer " + apiKey})
}

// Gemini calls /v1beta/models/{model}:{action}[?query] through the load
// balancer with the API key in x-goog-api-key (apiKey "" = pass it in
// query as key=).
func (e *Env) Gemini(apiKey, model, action, query string, body any) *GatewayResult {
	e.T.Helper()
	path := "/v1beta/models/" + model + ":" + action
	if query != "" {
		path += "?" + query
	}
	var h map[string]string
	if apiKey != "" {
		h = map[string]string{"x-goog-api-key": apiKey}
	}
	return e.Gateway(e.T, e.BaseURL, path, "", body, h)
}

// Gateway calls base+path with the API key in x-api-key. headers are extra
// request headers. The request id is taken from the X-Request-Id response
// header (the gateway assigns it).
func (e *Env) Gateway(t testing.TB, base, path, apiKey string, body any, headers map[string]string) *GatewayResult {
	t.Helper()
	g, err := GatewayTry(base, path, apiKey, body, headers)
	if err != nil {
		t.Fatalf("gateway %s: %v", path, err)
	}
	return g
}

// GatewayTry is Gateway without testing.TB; safe to use from goroutines.
// A []byte body is sent as it is (headers may set its Content-Type), any
// other as JSON.
func GatewayTry(base, path, apiKey string, body any, headers map[string]string) (*GatewayResult, error) {
	raw, ok := body.([]byte)
	if !ok {
		raw, _ = json.Marshal(body)
	}
	req, err := http.NewRequest(http.MethodPost, base+path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if apiKey != "" {
		req.Header.Set("x-api-key", apiKey)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	cl := &http.Client{Timeout: 120 * time.Second}
	start := time.Now()
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	g := &GatewayResult{Status: resp.StatusCode, Header: resp.Header,
		RequestID: resp.Header.Get("X-Request-Id"), ServedBy: resp.Header.Get("X-Served-By")}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		g.Events, err = readSSE(resp.Body, start)
		return g, err
	}
	g.Body, err = io.ReadAll(resp.Body)
	return g, err
}

// Messages calls POST /v1/messages through the load balancer.
func (e *Env) Messages(apiKey string, body any, headers map[string]string) *GatewayResult {
	e.T.Helper()
	return e.Gateway(e.T, e.BaseURL, "/v1/messages", apiKey, body, headers)
}

// MustMessages calls /v1/messages and requires HTTP 200 with a complete
// response (message_stop for streams, usage for JSON).
func (e *Env) MustMessages(apiKey string, body map[string]any, headers map[string]string) *GatewayResult {
	e.T.Helper()
	g := e.Messages(apiKey, body, headers)
	if g.Status != 200 {
		e.T.Fatalf("gateway: HTTP %d %s", g.Status, g.Body)
	}
	if stream, _ := body["stream"].(bool); stream {
		types := g.EventTypes()
		if len(types) == 0 || types[0] != "message_start" || types[len(types)-1] != "message_stop" {
			e.T.Fatalf("incomplete stream: %v", types)
		}
	} else if !g.JSON().Get("usage.output_tokens").Exists() {
		e.T.Fatalf("non-stream response without usage: %s", g.Body)
	}
	if g.RequestID == "" {
		e.T.Fatalf("gateway response has no X-Request-Id")
	}
	return g
}

func readSSE(r io.Reader, start time.Time) ([]SSEEvent, error) {
	return readSSEEvents(r, start, nil)
}

// readSSEEvents optionally observes each fully decoded event before EOF.
func readSSEEvents(r io.Reader, start time.Time, onEvent func(SSEEvent)) ([]SSEEvent, error) {
	var out []SSEEvent
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	var ev string
	var data strings.Builder
	flush := func() {
		if ev == "" && data.Len() == 0 {
			return
		}
		d := gjson.Parse(data.String())
		if ev == "" {
			ev = d.Get("type").String()
		}
		event := SSEEvent{Event: ev, Data: d, At: time.Since(start)}
		out = append(out, event)
		if onEvent != nil {
			onEvent(event)
		}
		ev = ""
		data.Reset()
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "event:"):
			ev = strings.TrimSpace(line[len("event:"):])
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(line[len("data:"):]))
		}
	}
	flush()
	return out, sc.Err()
}
