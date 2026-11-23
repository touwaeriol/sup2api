package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// API error formats of the simulated upstreams.
const (
	formatAnthropic = "anthropic"
	formatOpenAI    = "openai"
	formatGemini    = "gemini"
)

// begin records the request, resolves its behaviour, waits for the delay
// and writes an injected error. It reports whether the handler should go on
// with a normal response.
func (s *server) begin(w http.ResponseWriter, r *http.Request, body []byte, model string, stream bool, meta map[string]any, format string) (int64, behaviour, bool) {
	uid, _ := meta["user_id"].(string)
	id := s.record(r, body, model, stream, uid)
	b := s.resolve(r, meta)
	if !sleep(r, b.delay) {
		s.setStatus(id, 499)
		return id, b, false
	}
	if b.status >= 400 {
		s.setStatus(id, b.status)
		writeErrorFormat(w, format, b.status)
		return id, b, false
	}
	return id, b, true
}

// writeErrorFormat writes an injected error in the API's own format.
func writeErrorFormat(w http.ResponseWriter, format string, status int) {
	switch format {
	case formatOpenAI:
		writeOpenAIMockError(w, status)
	case formatGemini:
		writeGeminiMockError(w, status)
	case formatBedrock:
		writeBedrockMockError(w, status)
	default:
		writeMockError(w, status)
	}
}

// sseWriter writes server-sent events with the behaviour's chunk delay.
type sseWriter struct {
	w  http.ResponseWriter
	r  *http.Request
	fl http.Flusher
	d  time.Duration
}

func startSSE(w http.ResponseWriter, r *http.Request, d time.Duration) *sseWriter {
	fl, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(200)
	return &sseWriter{w: w, r: r, fl: fl, d: d}
}

// send writes one event (event "" = data only); data is JSON-encoded unless
// it is a string. It reports whether to continue.
func (sw *sseWriter) send(event string, data any) bool {
	var raw []byte
	if s, ok := data.(string); ok {
		raw = []byte(s)
	} else {
		raw, _ = json.Marshal(data)
	}
	var err error
	if event != "" {
		_, err = fmt.Fprintf(sw.w, "event: %s\ndata: %s\n\n", event, raw)
	} else {
		_, err = fmt.Fprintf(sw.w, "data: %s\n\n", raw)
	}
	if err != nil {
		return false
	}
	if sw.fl != nil {
		sw.fl.Flush()
	}
	return sleep(sw.r, sw.d)
}

func readBody(r *http.Request) []byte {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	return body
}

// ------------------------------------------------------------------ OpenAI

type openaiRequest struct {
	Model         string         `json:"model"`
	Stream        bool           `json:"stream"`
	Store         *bool          `json:"store"`
	Metadata      map[string]any `json:"metadata"`
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
	Input any `json:"input"`
}

// openaiChatUsage is the chat completions / embeddings usage object
// (inclusive: prompt_tokens includes cached_tokens).
func openaiChatUsage(u Usage) map[string]any {
	return map[string]any{
		"prompt_tokens":             u.InputTokens,
		"completion_tokens":         u.OutputTokens,
		"total_tokens":              u.InputTokens + u.OutputTokens,
		"prompt_tokens_details":     map[string]any{"cached_tokens": u.CacheReadTokens},
		"completion_tokens_details": map[string]any{"reasoning_tokens": u.ThoughtsTokens},
	}
}

// openaiResponsesUsage is the Responses API usage object.
func openaiResponsesUsage(u Usage) map[string]any {
	return map[string]any{
		"input_tokens":          u.InputTokens,
		"input_tokens_details":  map[string]any{"cached_tokens": u.CacheReadTokens},
		"output_tokens":         u.OutputTokens,
		"output_tokens_details": map[string]any{"reasoning_tokens": u.ThoughtsTokens},
		"total_tokens":          u.InputTokens + u.OutputTokens,
	}
}

// parseOpenAI decodes the body; on bad JSON it records the request and
// answers 400.
func (s *server) parseOpenAI(w http.ResponseWriter, r *http.Request, body []byte) (*openaiRequest, bool) {
	var req openaiRequest
	if err := json.Unmarshal(body, &req); err != nil {
		id := s.record(r, body, "", false, "")
		s.setStatus(id, 400)
		openaiError(w, 400, "invalid_request_error", "", "mock: invalid JSON body")
		return nil, false
	}
	return &req, true
}

func (s *server) chatCompletions(w http.ResponseWriter, r *http.Request) {
	body := readBody(r)
	req, ok := s.parseOpenAI(w, r, body)
	if !ok {
		return
	}
	id, b, ok := s.begin(w, r, body, req.Model, req.Stream, req.Metadata, formatOpenAI)
	if !ok {
		return
	}
	if req.Model == "" {
		s.setStatus(id, 400)
		openaiError(w, 400, "invalid_request_error", "", "mock: you must provide a model parameter")
		return
	}
	s.setStatus(id, 200)
	chatID := fmt.Sprintf("chatcmpl-mock-%d-%06d", id, rand.IntN(1000000))
	created := time.Now().Unix()
	w.Header().Set("x-request-id", fmt.Sprintf("req_mock_%d", id))
	w.Header().Set("x-mock-request-id", strconv.FormatInt(id, 10))
	tool, isTool := toolTurnOf(body)
	if isTool && tool.resultSeen {
		b.text = "TOOL-RESULT-SEEN " + b.text
	}
	callID := fmt.Sprintf("call_mock_%d", id)
	if !req.Stream && isTool && !tool.resultSeen {
		writeJSON(w, 200, map[string]any{
			"id": chatID, "object": "chat.completion", "created": created, "model": req.Model,
			"choices": []any{map[string]any{
				"index": 0, "finish_reason": "tool_calls", "logprobs": nil,
				"message": map[string]any{"role": "assistant", "content": nil, "refusal": nil,
					"tool_calls": []any{chatToolCall(callID, tool.name, toolInput())}},
			}},
			"usage": openaiChatUsage(b.usage),
		})
		return
	}
	if !req.Stream {
		if rep, ok := moderationFor(body); ok {
			writeModeration(w, id, chatID, created, req.Model, rep, b.usage)
			return
		}
		writeJSON(w, 200, map[string]any{
			"id": chatID, "object": "chat.completion", "created": created, "model": req.Model,
			"choices": []any{map[string]any{
				"index": 0, "finish_reason": "stop", "logprobs": nil,
				"message": map[string]any{"role": "assistant", "content": b.text, "refusal": nil},
			}},
			"usage": openaiChatUsage(b.usage),
		})
		return
	}
	// Like OpenAI: usage only with stream_options.include_usage, as a last
	// chunk with empty choices; other chunks then carry "usage": null.
	includeUsage := req.StreamOptions != nil && req.StreamOptions.IncludeUsage
	sw := startSSE(w, r, b.chunkDelay)
	chunk := func(choices []any) map[string]any {
		c := map[string]any{"id": chatID, "object": "chat.completion.chunk", "created": created, "model": req.Model, "choices": choices}
		if includeUsage {
			c["usage"] = nil
		}
		return c
	}
	// The choice's deltas (the tool call or the text), then one tail.
	var deltas []map[string]any
	reason := "stop"
	if isTool && !tool.resultSeen {
		deltas = append(deltas, map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{"index": 0, "id": callID, "type": "function",
			"function": map[string]any{"name": tool.name, "arguments": ""}}}})
		for _, frag := range toolArguments {
			deltas = append(deltas, map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": frag}}}})
		}
		reason = "tool_calls"
	} else {
		deltas = append(deltas, map[string]any{"role": "assistant", "content": ""})
		for _, part := range chunks(b.text) {
			deltas = append(deltas, map[string]any{"content": part})
		}
	}
	for _, d := range deltas {
		if !sw.send("", chunk([]any{map[string]any{"index": 0, "delta": d, "finish_reason": nil}})) {
			return
		}
	}
	if !sw.send("", chunk([]any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": reason}})) {
		return
	}
	if includeUsage {
		last := chunk([]any{})
		last["usage"] = openaiChatUsage(b.usage)
		if !sw.send("", last) {
			return
		}
	}
	sw.send("", "[DONE]")
}

func (s *server) responses(w http.ResponseWriter, r *http.Request) {
	body := readBody(r)
	req, ok := s.parseOpenAI(w, r, body)
	if !ok {
		return
	}
	id, b, ok := s.begin(w, r, body, req.Model, req.Stream, req.Metadata, formatOpenAI)
	if !ok {
		return
	}
	if req.Model == "" {
		s.setStatus(id, 400)
		openaiError(w, 400, "invalid_request_error", "model", "mock: missing required parameter: 'model'")
		return
	}
	if msg := codexReject(r, req); msg != "" {
		s.setStatus(id, 400)
		writeJSON(w, 400, map[string]any{"detail": msg})
		return
	}
	s.setStatus(id, 200)
	respID := fmt.Sprintf("resp_mock_%d_%06d", id, rand.IntN(1000000))
	msgID := fmt.Sprintf("msg_mock_%d", id)
	created := time.Now().Unix()
	w.Header().Set("x-request-id", fmt.Sprintf("req_mock_%d", id))
	w.Header().Set("x-mock-request-id", strconv.FormatInt(id, 10))
	message := responsesMessageItem(msgID, "completed", []any{responsesTextPart(b.text)})
	// With the image_generation tool the image call comes first, the
	// assistant's message after it (the Codex backend's order).
	tools, image := imageToolOf(body, "ig_"+msgID)
	output, msgIndex := []any{message}, 0
	if image != nil {
		output, msgIndex = []any{image, message}, 1
	}
	final := responsesEnvelope(respID, created, req.Model, "completed", output, openaiResponsesUsage(b.usage))
	if tools != nil {
		withImageTool(final, tools, image)
	}
	if !req.Stream {
		writeJSON(w, 200, final)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/backend-api/") {
		// The Codex backend's completed response lists no output: the items
		// are only in the output_item.done events.
		final["output"] = []any{}
	}
	sw := startSSE(w, r, b.chunkDelay)
	seq := 0
	ev := func(typ string, fields map[string]any) bool {
		fields["type"] = typ
		fields["sequence_number"] = seq
		seq++
		return sw.send(typ, fields)
	}
	if !ev("response.created", map[string]any{"response": responsesEnvelope(respID, created, req.Model, "in_progress", []any{}, nil)}) ||
		!ev("response.in_progress", map[string]any{"response": responsesEnvelope(respID, created, req.Model, "in_progress", []any{}, nil)}) {
		return
	}
	if image != nil {
		callID := image["id"]
		if !ev("response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"type": "image_generation_call", "id": callID, "status": "in_progress"}}) ||
			!ev("response.image_generation_call.in_progress", map[string]any{"output_index": 0, "item_id": callID}) ||
			!ev("response.image_generation_call.generating", map[string]any{"output_index": 0, "item_id": callID}) ||
			!ev("response.image_generation_call.completed", map[string]any{"output_index": 0, "item_id": callID}) ||
			!ev("response.output_item.done", map[string]any{"output_index": 0, "item": image}) {
			return
		}
	}
	if !ev("response.output_item.added", map[string]any{"output_index": msgIndex, "item": responsesMessageItem(msgID, "in_progress", []any{})}) ||
		!ev("response.content_part.added", map[string]any{"item_id": msgID, "output_index": msgIndex, "content_index": 0, "part": responsesTextPart("")}) {
		return
	}
	for _, part := range chunks(b.text) {
		if !ev("response.output_text.delta", map[string]any{"item_id": msgID, "output_index": msgIndex, "content_index": 0, "delta": part}) {
			return
		}
	}
	if !ev("response.output_text.done", map[string]any{"item_id": msgID, "output_index": msgIndex, "content_index": 0, "text": b.text}) ||
		!ev("response.content_part.done", map[string]any{"item_id": msgID, "output_index": msgIndex, "content_index": 0, "part": responsesTextPart(b.text)}) ||
		!ev("response.output_item.done", map[string]any{"output_index": msgIndex, "item": message}) {
		return
	}
	ev("response.completed", map[string]any{"response": final})
}

// codexReject is why the Codex backend (paths under /backend-api/) refuses a
// Responses request, "" when it does not: it stores nothing and only
// streams. It answers ChatGPT's {"detail": ...} errors.
func codexReject(r *http.Request, req *openaiRequest) string {
	switch {
	case !strings.HasPrefix(r.URL.Path, "/backend-api/"):
		return ""
	case req.Store == nil || *req.Store:
		return "Store must be set to false"
	case !req.Stream:
		return "Stream must be set to true"
	}
	return ""
}

// responsesTextPart is the output_text part shared by the SSE and WebSocket
// Responses mocks.
func responsesTextPart(text string) map[string]any {
	return map[string]any{"type": "output_text", "text": text, "annotations": []any{}}
}

// responsesMessageItem is the assistant message item shared by both mocks.
func responsesMessageItem(id, status string, content []any) map[string]any {
	return map[string]any{"type": "message", "id": id, "status": status, "role": "assistant", "content": content}
}

// responsesEnvelope is the response object shared by both mocks.
func responsesEnvelope(id string, created int64, model, status string, output []any, usage any) map[string]any {
	return map[string]any{
		"id": id, "object": "response", "created_at": created, "status": status, "model": model,
		"output": output, "usage": usage,
	}
}

func (s *server) embeddings(w http.ResponseWriter, r *http.Request) {
	body := readBody(r)
	req, ok := s.parseOpenAI(w, r, body)
	if !ok {
		return
	}
	id, b, ok := s.begin(w, r, body, req.Model, false, req.Metadata, formatOpenAI)
	if !ok {
		return
	}
	n := 1
	switch in := req.Input.(type) {
	case nil:
		s.setStatus(id, 400)
		openaiError(w, 400, "invalid_request_error", "input", "mock: 'input' is a required property")
		return
	case []any:
		if len(in) > 0 {
			if _, isNum := in[0].(float64); !isNum { // array of strings or token arrays
				n = len(in)
			}
		}
	}
	if req.Model == "" {
		s.setStatus(id, 400)
		openaiError(w, 400, "invalid_request_error", "model", "mock: you must provide a model parameter")
		return
	}
	s.setStatus(id, 200)
	data := make([]any, n)
	for i := range data {
		data[i] = map[string]any{"object": "embedding", "index": i, "embedding": mockVector(i)}
	}
	w.Header().Set("x-mock-request-id", strconv.FormatInt(id, 10))
	writeJSON(w, 200, map[string]any{
		"object": "list", "data": data, "model": req.Model,
		"usage": map[string]any{"prompt_tokens": b.usage.InputTokens, "total_tokens": b.usage.InputTokens},
	})
}

func openaiError(w http.ResponseWriter, status int, typ, param, msg string) {
	var p any
	if param != "" {
		p = param
	}
	writeJSON(w, status, map[string]any{"error": map[string]any{"message": msg, "type": typ, "param": p, "code": nil}})
}

func writeOpenAIMockError(w http.ResponseWriter, status int) {
	e := map[string]any{"message": "mock: internal error", "type": "server_error", "param": nil, "code": nil}
	switch status {
	case 400:
		e["message"], e["type"] = "mock: invalid request", "invalid_request_error"
	case 401:
		e["message"], e["type"], e["code"] = "mock: incorrect API key provided", "invalid_request_error", "invalid_api_key"
	case 403:
		e["message"], e["type"], e["code"] = "mock: permission denied", "invalid_request_error", "unsupported_country_region_territory"
	case 404:
		e["message"], e["type"], e["code"] = "mock: model not found", "invalid_request_error", "model_not_found"
	case 429:
		w.Header().Set("retry-after", "2")
		w.Header().Set("x-ratelimit-remaining-requests", "0")
		w.Header().Set("x-ratelimit-reset-requests", "2s")
		e["message"], e["type"], e["code"] = "mock: rate limit reached", "requests", "rate_limit_exceeded"
	case 503:
		e["message"] = "mock: the engine is currently overloaded"
	}
	writeJSON(w, status, map[string]any{"error": e})
}

// ------------------------------------------------------------------ Gemini

type geminiRequest struct {
	Contents []any `json:"contents"`
}

// geminiUsage is usageMetadata (inclusive: promptTokenCount includes
// cachedContentTokenCount; thoughtsTokenCount is separate from
// candidatesTokenCount). output is the candidates count so far.
func geminiUsage(u Usage, output, thoughts int64) map[string]any {
	m := map[string]any{
		"promptTokenCount":     u.InputTokens,
		"candidatesTokenCount": output,
		"totalTokenCount":      u.InputTokens + output + thoughts,
	}
	if thoughts > 0 {
		m["thoughtsTokenCount"] = thoughts
	}
	if u.CacheReadTokens > 0 {
		m["cachedContentTokenCount"] = u.CacheReadTokens
	}
	return m
}

// gemini serves /v1beta/models/{model}:{action} and the same under /v1
// (the embedding methods in gemini_embed.go).
func (s *server) gemini(w http.ResponseWriter, r *http.Request) {
	body := readBody(r)
	spec := r.PathValue("spec")
	i := strings.LastIndex(spec, ":")
	if i <= 0 {
		id := s.record(r, body, "", false, "")
		s.setStatus(id, 404)
		geminiError(w, 404, "NOT_FOUND", "mock: expected /{version}/models/{model}:{action}")
		return
	}
	model, act := spec[:i], spec[i+1:]
	stream := act == "streamGenerateContent"
	var req geminiRequest
	if err := json.Unmarshal(body, &req); err != nil {
		id := s.record(r, body, model, stream, "")
		s.setStatus(id, 400)
		geminiError(w, 400, "INVALID_ARGUMENT", "mock: invalid JSON payload received")
		return
	}
	id, b, ok := s.begin(w, r, body, model, stream, nil, formatGemini)
	if !ok {
		return
	}
	switch act {
	case "generateContent", "streamGenerateContent", "countTokens":
	case "embedContent", "batchEmbedContents":
		s.geminiEmbed(w, id, b, act, body)
		return
	default:
		s.setStatus(id, 404)
		geminiError(w, 404, "NOT_FOUND", "mock: unknown method "+act)
		return
	}
	if act != "countTokens" && len(req.Contents) == 0 {
		s.setStatus(id, 400)
		geminiError(w, 400, "INVALID_ARGUMENT", "mock: contents is not specified")
		return
	}
	s.setStatus(id, 200)
	w.Header().Set("x-mock-request-id", strconv.FormatInt(id, 10))
	if act == "countTokens" {
		writeJSON(w, 200, map[string]any{"totalTokens": int64(len(body)/4) + 1})
		return
	}
	respID := fmt.Sprintf("mock-%d-%06d", id, rand.IntN(1000000))
	if !stream {
		writeJSON(w, 200, geminiJSON(model, b, respID))
		return
	}
	// Chunks carry cumulative usageMetadata; the last one is complete.
	parts := chunks(b.text)
	events := make([]map[string]any, len(parts))
	for i, part := range parts {
		out, thoughts, finish := b.usage.OutputTokens*int64(i+1)/int64(len(parts)), int64(0), ""
		if i == len(parts)-1 {
			out, thoughts, finish = b.usage.OutputTokens, b.usage.ThoughtsTokens, "STOP"
		}
		events[i] = map[string]any{
			"candidates": geminiCandidate(part, finish), "usageMetadata": geminiUsage(b.usage, out, thoughts),
			"modelVersion": model, "responseId": respID,
		}
	}
	if r.URL.Query().Get("alt") == "sse" {
		sw := startSSE(w, r, b.chunkDelay)
		for _, ev := range events {
			if !sw.send("", ev) {
				return
			}
		}
		return
	}
	geminiStreamJSON(w, r, events, b.chunkDelay)
}

// geminiJSON builds the single (non-streaming) generateContent response.
func geminiJSON(model string, b behaviour, respID string) map[string]any {
	return map[string]any{
		"candidates":    geminiCandidate(b.text, "STOP"),
		"usageMetadata": geminiUsage(b.usage, b.usage.OutputTokens, b.usage.ThoughtsTokens),
		"modelVersion":  model, "responseId": respID,
	}
}

// geminiCandidate builds the single candidate array of a Gemini response.
func geminiCandidate(text, finish string) []any {
	c := map[string]any{"content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": text}}}, "index": 0}
	if finish != "" {
		c["finishReason"] = finish
	}
	return []any{c}
}

// geminiStreamJSON writes the streaming events as one JSON array, which is how
// Google streams without alt=sse.
func geminiStreamJSON(w http.ResponseWriter, r *http.Request, events []map[string]any, chunkDelay time.Duration) {
	fl, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	for i, ev := range events {
		raw, _ := json.Marshal(ev)
		sep := ",\r\n"
		if i == 0 {
			sep = "["
		}
		if _, err := fmt.Fprintf(w, "%s%s", sep, raw); err != nil {
			return
		}
		if fl != nil {
			fl.Flush()
		}
		if !sleep(r, chunkDelay) {
			return
		}
	}
	_, _ = io.WriteString(w, "]")
}

func geminiError(w http.ResponseWriter, code int, status, msg string, details ...any) {
	if details == nil {
		details = []any{}
	}
	writeJSON(w, code, map[string]any{"error": map[string]any{"code": code, "message": msg, "status": status, "details": details}})
}

func writeGeminiMockError(w http.ResponseWriter, status int) {
	switch status {
	case 400:
		geminiError(w, 400, "INVALID_ARGUMENT", "mock: invalid argument")
	case 401:
		geminiError(w, 401, "UNAUTHENTICATED", "mock: request had invalid authentication credentials")
	case 403:
		geminiError(w, 403, "PERMISSION_DENIED", "mock: permission denied")
	case 404:
		geminiError(w, 404, "NOT_FOUND", "mock: model not found")
	case 429:
		w.Header().Set("retry-after", "2")
		geminiError(w, 429, "RESOURCE_EXHAUSTED", "mock: resource has been exhausted",
			map[string]any{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "2s"})
	case 503:
		geminiError(w, 503, "UNAVAILABLE", "mock: the model is overloaded")
	case 504:
		geminiError(w, 504, "DEADLINE_EXCEEDED", "mock: deadline exceeded")
	default:
		geminiError(w, status, "INTERNAL", "mock: internal error")
	}
}
