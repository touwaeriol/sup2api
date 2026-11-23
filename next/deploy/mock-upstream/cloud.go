package main

// The cloud vendors' front doors to the same models: Azure OpenAI (v1 and
// per-deployment paths), AWS Bedrock (InvokeModel for Claude, answering
// streams as an AWS event stream) and Vertex AI (Gemini under a project and
// location). Each reuses the handler of the API it fronts.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const formatBedrock = "bedrock"

func (s *server) cloudRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /openai/v1/chat/completions", s.chatCompletions)
	mux.HandleFunc("POST /openai/v1/responses", s.responses)
	mux.HandleFunc("POST /openai/v1/embeddings", s.embeddings)
	mux.HandleFunc("POST /openai/deployments/{deployment}/chat/completions", azureDeployment(s.chatCompletions))
	mux.HandleFunc("POST /openai/deployments/{deployment}/embeddings", azureDeployment(s.embeddings))
	mux.HandleFunc("POST /model/{model}/invoke", s.bedrockInvoke)
	mux.HandleFunc("POST /model/{model}/invoke-with-response-stream", s.bedrockInvoke)
	// Vertex AI v1 and v1beta1 (a Gemini client's /v1beta goes to v1beta1).
	for _, version := range []string{"v1", "v1beta1"} {
		mux.HandleFunc("POST /"+version+"/projects/{project}/locations/{location}/publishers/google/models/{spec}", s.gemini)
		mux.HandleFunc("POST /"+version+"/publishers/google/models/{spec}", s.gemini)
	}
}

// ------------------------------------------------------------------ Azure OpenAI

var azureAPIVersion = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}(-preview)?$`)

// azureDeployment serves a per-deployment path with the handler of the
// OpenAI endpoint: it needs an api-version, and the deployment is the model
// when the body names none.
func azureDeployment(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !azureAPIVersion.MatchString(r.URL.Query().Get("api-version")) {
			writeJSON(w, 404, map[string]any{"error": map[string]any{"code": "404", "message": "Resource not found"}})
			return
		}
		body := readBody(r)
		var fields map[string]json.RawMessage
		if json.Unmarshal(body, &fields) == nil && fields["model"] == nil {
			fields["model"], _ = json.Marshal(r.PathValue("deployment"))
			body, _ = json.Marshal(fields)
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		next(w, r)
	}
}

// ------------------------------------------------------------------ AWS Bedrock

const bedrockAnthropicVersion = "bedrock-2023-05-31"

// bedrockInvoke serves InvokeModel and InvokeModelWithResponseStream for a
// Claude model: the Messages API body without "model" and "stream" (the path
// carries both) and with anthropic_version.
func (s *server) bedrockInvoke(w http.ResponseWriter, r *http.Request) {
	body := readBody(r)
	model := r.PathValue("model")
	stream := strings.HasSuffix(r.URL.Path, "/invoke-with-response-stream")
	var req messagesRequest
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &req) != nil || json.Unmarshal(body, &fields) != nil {
		id := s.record(r, body, model, stream, "")
		s.setStatus(id, 400)
		bedrockError(w, 400, "ValidationException", "mock: malformed input request")
		return
	}
	id, b, ok := s.begin(w, r, body, model, stream, req.Metadata, formatBedrock)
	if !ok {
		return
	}
	if status, typ, msg := bedrockReject(r, body, fields); status != 0 {
		s.setStatus(id, status)
		bedrockError(w, status, typ, msg)
		return
	}
	s.setStatus(id, 200)
	msgID := fmt.Sprintf("msg_bdrk_mock_%d_%06d", id, rand.IntN(1000000))
	w.Header().Set("x-amzn-requestid", fmt.Sprintf("mock-%d", id))
	w.Header().Set("x-mock-request-id", strconv.FormatInt(id, 10))
	if !stream {
		writeJSON(w, 200, map[string]any{
			"id": msgID, "type": "message", "role": "assistant", "model": model,
			"content":       []any{map[string]any{"type": "text", "text": b.text}},
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
			"usage":         usageJSON(b.usage, b.usage.OutputTokens),
		})
		return
	}
	es := startEventStream(w, r, b)
	messageEvents(es.send, model, msgID, b)
}

// bedrockReject checks what Bedrock checks before it runs the model: the
// signature of a SigV4 request and the shape of the body.
func bedrockReject(r *http.Request, body []byte, fields map[string]json.RawMessage) (status int, typ, msg string) {
	if err := verifySigV4(r, body); err != nil {
		return 403, "InvalidSignatureException", "mock: " + err.Error()
	}
	for _, key := range []string{"model", "stream"} {
		if fields[key] != nil {
			return 400, "ValidationException", "mock: extraneous key [" + key + "] is not permitted"
		}
	}
	var version string
	if json.Unmarshal(fields["anthropic_version"], &version) != nil || version != bedrockAnthropicVersion {
		return 400, "ValidationException", "mock: anthropic_version must be " + bedrockAnthropicVersion
	}
	return 0, "", ""
}

// bedrockError answers in the shape of the Bedrock runtime's errors.
func bedrockError(w http.ResponseWriter, status int, typ, msg string) {
	w.Header().Set("x-amzn-errortype", typ)
	writeJSON(w, status, map[string]any{"message": msg})
}

func writeBedrockMockError(w http.ResponseWriter, status int) {
	switch status {
	case 400:
		bedrockError(w, 400, "ValidationException", "mock: validation failed")
	case 403:
		bedrockError(w, 403, "AccessDeniedException", "mock: you don't have access to the model with the specified model ID")
	case 404:
		bedrockError(w, 404, "ResourceNotFoundException", "mock: model not found")
	case 408:
		bedrockError(w, 408, "ModelTimeoutException", "mock: model timed out")
	case 424:
		bedrockError(w, 424, "ModelErrorException", "mock: model error")
	case 429:
		bedrockError(w, 429, "ThrottlingException", "mock: too many requests")
	case 503:
		bedrockError(w, 503, "ServiceUnavailableException", "mock: service unavailable")
	default:
		bedrockError(w, status, "InternalServerException", "mock: internal error")
	}
}

// eventStreamWriter writes model events as the chunk messages of an AWS
// event stream, with the behaviour's chunk delay.
type eventStreamWriter struct {
	w     http.ResponseWriter
	r     *http.Request
	fl    http.Flusher
	usage Usage
	d     time.Duration
}

func startEventStream(w http.ResponseWriter, r *http.Request, b behaviour) *eventStreamWriter {
	fl, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
	w.Header().Set("x-amzn-bedrock-content-type", "application/json")
	w.WriteHeader(200)
	return &eventStreamWriter{w: w, r: r, fl: fl, usage: b.usage, d: b.chunkDelay}
}

// send writes one event; like Bedrock, the last one carries the invocation
// metrics. It reports whether to continue.
func (es *eventStreamWriter) send(event string, data any) bool {
	if m, ok := data.(map[string]any); ok && event == "message_stop" {
		m["amazon-bedrock-invocationMetrics"] = map[string]any{
			"inputTokenCount": es.usage.InputTokens, "outputTokenCount": es.usage.OutputTokens,
			"invocationLatency": 100, "firstByteLatency": 10,
		}
	}
	raw, _ := json.Marshal(data)
	payload, _ := json.Marshal(map[string]string{"bytes": base64.StdEncoding.EncodeToString(raw)})
	frame := appendEventStreamMessage(nil, [][2]string{
		{":event-type", "chunk"}, {":content-type", "application/json"}, {":message-type", "event"},
	}, payload)
	if _, err := es.w.Write(frame); err != nil {
		return false
	}
	if es.fl != nil {
		es.fl.Flush()
	}
	return sleep(es.r, es.d)
}
