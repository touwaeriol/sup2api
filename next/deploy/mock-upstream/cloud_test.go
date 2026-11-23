package main

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func (s *server) lastRecorded() recorded {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.log[len(s.log)-1]
}

func TestAzureOpenAIV1(t *testing.T) {
	s, srv := newTestServer(t)
	key := map[string]string{"api-key": "azure-key-1", "x-mock-chunk-delay-ms": "1"}

	m := decode(t, post(t, srv, "/openai/v1/chat/completions", key, `{"model":"my-deployment","messages":[{"role":"user","content":"hi"}]}`))
	if get(m, "model") != "my-deployment" || num(get(m, "usage", "prompt_tokens")) != 120 || num(get(m, "usage", "completion_tokens")) != 42 {
		t.Fatalf("chat = %v", m)
	}
	if rec := s.lastRecorded(); rec.APIKey != "azure-key-1" || rec.Path != "/openai/v1/chat/completions" || rec.Status != 200 {
		t.Fatalf("recorded %+v", rec)
	}
	_, data := sseData(t, post(t, srv, "/openai/v1/chat/completions", key, `{"model":"my-deployment","stream":true,"stream_options":{"include_usage":true},"messages":[]}`))
	if data[len(data)-1] != "[DONE]" || !strings.Contains(data[len(data)-2], `"prompt_tokens":120`) {
		t.Fatalf("stream: %v", data)
	}
	m = decode(t, post(t, srv, "/openai/v1/responses", key, `{"model":"my-deployment","input":"hi"}`))
	if num(get(m, "usage", "input_tokens")) != 120 {
		t.Fatalf("responses = %v", m)
	}
	m = decode(t, post(t, srv, "/openai/v1/embeddings", key, `{"model":"my-embedding","input":"hi"}`))
	if num(get(m, "usage", "prompt_tokens")) == 0 {
		t.Fatalf("embeddings = %v", m)
	}
	// A key marker injects an error in OpenAI's format, as on /v1.
	resp := post(t, srv, "/openai/v1/chat/completions", map[string]string{"api-key": "azure-status-429"}, `{"model":"d","messages":[]}`)
	if m = decode(t, resp); resp.StatusCode != 429 || get(m, "error", "message") == nil {
		t.Fatalf("injected: %d %v", resp.StatusCode, m)
	}
}

func TestAzureOpenAIDeployments(t *testing.T) {
	s, srv := newTestServer(t)
	key := map[string]string{"api-key": "azure-key-2", "x-mock-chunk-delay-ms": "1"}
	const chat = "/openai/deployments/gpt4o-prod/chat/completions"

	// The deployment is the model; a body may still carry one.
	m := decode(t, post(t, srv, chat+"?api-version=2024-10-21", key, `{"messages":[{"role":"user","content":"hi"}]}`))
	if get(m, "model") != "gpt4o-prod" || num(get(m, "usage", "completion_tokens")) != 42 {
		t.Fatalf("chat = %v", m)
	}
	if rec := s.lastRecorded(); rec.Model != "gpt4o-prod" || rec.Query != "api-version=2024-10-21" || rec.APIKey != "azure-key-2" {
		t.Fatalf("recorded %+v", rec)
	}
	m = decode(t, post(t, srv, chat+"?api-version=2025-01-01-preview", key, `{"model":"client-name","messages":[]}`))
	if get(m, "model") != "client-name" {
		t.Fatalf("chat with a model = %v", m)
	}
	_, data := sseData(t, post(t, srv, chat+"?api-version=2024-10-21", key, `{"stream":true,"stream_options":{"include_usage":true},"messages":[]}`))
	if data[len(data)-1] != "[DONE]" || !strings.Contains(data[len(data)-2], `"completion_tokens":42`) {
		t.Fatalf("stream: %v", data)
	}
	m = decode(t, post(t, srv, "/openai/deployments/embed-prod/embeddings?api-version=2024-10-21", key, `{"input":["a","b"]}`))
	if get(m, "model") != "embed-prod" || len(get(m, "data").([]any)) != 2 {
		t.Fatalf("embeddings = %v", m)
	}
	// Without a valid api-version Azure does not know the resource.
	for _, q := range []string{"", "?api-version=latest", "?api-version=2024-10"} {
		resp := post(t, srv, chat+q, key, `{"messages":[]}`)
		if m = decode(t, resp); resp.StatusCode != 404 || get(m, "error", "code") != "404" {
			t.Fatalf("%q: %d %v", q, resp.StatusCode, m)
		}
	}
	// The classic paths have no responses endpoint.
	if resp := post(t, srv, "/openai/deployments/gpt4o-prod/responses?api-version=2024-10-21", key, `{}`); resp.StatusCode != 404 {
		t.Fatalf("responses: %d", resp.StatusCode)
	}
}

const bedrockBody = `{"anthropic_version":"bedrock-2023-05-31","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`

func TestBedrockInvoke(t *testing.T) {
	s, srv := newTestServer(t)
	key := map[string]string{"Authorization": "Bearer bedrock-api-key-1"}
	const path = "/model/us.anthropic.claude-sonnet-4-5-20250929-v1%3A0/invoke"

	resp := post(t, srv, path, key, bedrockBody)
	m := decode(t, resp)
	if resp.StatusCode != 200 || get(m, "model") != "us.anthropic.claude-sonnet-4-5-20250929-v1:0" || get(m, "type") != "message" ||
		num(get(m, "usage", "input_tokens")) != 120 || num(get(m, "usage", "output_tokens")) != 42 || resp.Header.Get("x-amzn-requestid") == "" {
		t.Fatalf("invoke: %d %v", resp.StatusCode, m)
	}
	if rec := s.lastRecorded(); rec.APIKey != "bedrock-api-key-1" || rec.Model != "us.anthropic.claude-sonnet-4-5-20250929-v1:0" || rec.Stream || rec.Status != 200 {
		t.Fatalf("recorded %+v", rec)
	}
	// An ARN is one path segment.
	resp = post(t, srv, "/model/arn%3Aaws%3Abedrock%3Aus-west-2%3A123456789012%3Aapplication-inference-profile%2Fabc/invoke", key, bedrockBody)
	if m = decode(t, resp); resp.StatusCode != 200 || get(m, "model") != "arn:aws:bedrock:us-west-2:123456789012:application-inference-profile/abc" {
		t.Fatalf("arn: %d %v", resp.StatusCode, m)
	}

	// What Bedrock rejects, in Bedrock's error shape.
	for name, tc := range map[string]struct {
		body, want string
	}{
		"model in the body":    {`{"model":"x","anthropic_version":"bedrock-2023-05-31","messages":[]}`, "extraneous key [model]"},
		"stream in the body":   {`{"stream":true,"anthropic_version":"bedrock-2023-05-31","messages":[]}`, "extraneous key [stream]"},
		"no anthropic_version": {`{"messages":[]}`, "anthropic_version"},
		"wrong version":        {`{"anthropic_version":"2023-06-01","messages":[]}`, "anthropic_version"},
		"not JSON":             {`{`, "malformed"},
	} {
		resp := post(t, srv, path, key, tc.body)
		m := decode(t, resp)
		msg, _ := get(m, "message").(string)
		if resp.StatusCode != 400 || resp.Header.Get("x-amzn-errortype") != "ValidationException" || !strings.Contains(msg, tc.want) {
			t.Errorf("%s: %d %s %v", name, resp.StatusCode, resp.Header.Get("x-amzn-errortype"), m)
		}
	}
	// Injected statuses come as the exceptions of that status.
	for status, typ := range map[string]string{"429": "ThrottlingException", "403": "AccessDeniedException", "503": "ServiceUnavailableException", "500": "InternalServerException"} {
		resp := post(t, srv, path, map[string]string{"Authorization": "Bearer bedrock-status-" + status}, bedrockBody)
		if m := decode(t, resp); resp.Header.Get("x-amzn-errortype") != typ || get(m, "message") == nil {
			t.Errorf("status %s: %d %s %v", status, resp.StatusCode, resp.Header.Get("x-amzn-errortype"), m)
		}
	}
}

func TestBedrockInvokeWithResponseStream(t *testing.T) {
	s, srv := newTestServer(t)
	resp := post(t, srv, "/model/anthropic.claude-sonnet-4-5-20250929-v1%3A0/invoke-with-response-stream",
		map[string]string{"Authorization": "Bearer bedrock-api-key-1", "x-mock-chunk-delay-ms": "1"}, bedrockBody)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/vnd.amazon.eventstream" {
		t.Fatalf("stream: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	var events []map[string]any
	for _, msg := range readEventStream(t, raw) {
		if msg.headers[":message-type"] != "event" || msg.headers[":event-type"] != "chunk" || msg.headers[":content-type"] != "application/json" {
			t.Fatalf("headers %v", msg.headers)
		}
		var chunk struct {
			Bytes string `json:"bytes"`
		}
		if err := json.Unmarshal(msg.payload, &chunk); err != nil {
			t.Fatal(err)
		}
		inner, err := base64.StdEncoding.DecodeString(chunk.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		var ev map[string]any
		if err := json.Unmarshal(inner, &ev); err != nil {
			t.Fatal(err)
		}
		typ, _ := ev["type"].(string)
		types, events = append(types, typ), append(events, ev)
	}
	joined := strings.Join(types, ",")
	if !strings.HasPrefix(joined, "message_start,content_block_start,ping,content_block_delta") ||
		!strings.HasSuffix(joined, "content_block_stop,message_delta,message_stop") {
		t.Fatalf("events %s", joined)
	}
	first, last := events[0], events[len(events)-1]
	if num(get(first, "message", "usage", "input_tokens")) != 120 || get(first, "message", "model") != "anthropic.claude-sonnet-4-5-20250929-v1:0" ||
		num(get(events[len(events)-2], "usage", "output_tokens")) != 42 ||
		num(get(last, "amazon-bedrock-invocationMetrics", "inputTokenCount")) != 120 || num(get(last, "amazon-bedrock-invocationMetrics", "outputTokenCount")) != 42 {
		t.Fatalf("usage: %v … %v", first, last)
	}
	if rec := s.lastRecorded(); !rec.Stream || rec.Status != 200 {
		t.Fatalf("recorded %+v", rec)
	}
}

// signForMock signs req for the credential scope region/service the way a
// client holding the mock's secret for accessKey would (the algorithm is
// pinned against the AWS SDK in awswire_test.go).
func signForMock(req *http.Request, body, accessKey, secret, token, region, service string) {
	amzDate := time.Now().UTC().Format("20060102T150405Z")
	req.Header.Set("X-Amz-Date", amzDate)
	signed := "content-type;host;x-amz-date"
	if token != "" {
		req.Header.Set("X-Amz-Security-Token", token)
		signed += ";x-amz-security-token"
	}
	sig, err := sigV4Signature(req, []byte(body), secret, amzDate, region, service, signed)
	if err != nil {
		panic(err)
	}
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+accessKey+"/"+amzDate[:8]+"/"+region+"/"+service+"/aws4_request, SignedHeaders="+signed+", Signature="+sig)
}

func TestBedrockSigV4(t *testing.T) {
	s, srv := newTestServer(t)
	// The credential is scoped to region and service, empty for the right ones.
	do := func(secret, token, region, service string, tamper func(*http.Request)) (*http.Response, map[string]any) {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/model/anthropic.claude-v1%3A0/invoke", strings.NewReader(bedrockBody))
		req.Host = "bedrock-runtime.us-east-1.amazonaws.com"
		req.Header.Set("Content-Type", "application/json")
		signForMock(req, bedrockBody, "AKIAMOCK1", secret, token, cmp.Or(region, "us-east-1"), cmp.Or(service, "bedrock"))
		if tamper != nil {
			tamper(req)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp, decode(t, resp)
	}
	for _, token := range []string{"", "session-token-1"} {
		if resp, m := do(mockSecretAccessKey("AKIAMOCK1"), token, "", "", nil); resp.StatusCode != 200 || get(m, "type") != "message" {
			t.Fatalf("signed (token %q): %d %v", token, resp.StatusCode, m)
		}
		if rec := s.lastRecorded(); rec.APIKey != "AKIAMOCK1" || rec.Headers["x-amz-security-token"] != token {
			t.Fatalf("recorded %+v", rec)
		}
	}
	for name, tc := range map[string]struct {
		secret, region, service string
		tamper                  func(*http.Request)
	}{
		"another service": {secret: mockSecretAccessKey("AKIAMOCK1"), service: "bedrock-runtime"},
		"another region":  {secret: mockSecretAccessKey("AKIAMOCK1"), region: "us-west-2"},
		"another secret":  {secret: "not-the-mock-secret"},
		"another body": {secret: mockSecretAccessKey("AKIAMOCK1"), tamper: func(r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader(bedrockBody + " "))
			r.ContentLength++
		}},
		"an unsigned session token": {secret: mockSecretAccessKey("AKIAMOCK1"), tamper: func(r *http.Request) { r.Header.Set("X-Amz-Security-Token", "t") }},
		"an old date":               {secret: mockSecretAccessKey("AKIAMOCK1"), tamper: func(r *http.Request) { r.Header.Set("X-Amz-Date", "20200101T000000Z") }},
	} {
		resp, m := do(tc.secret, "", tc.region, tc.service, tc.tamper)
		if resp.StatusCode != 403 || resp.Header.Get("x-amzn-errortype") != "InvalidSignatureException" || get(m, "message") == nil {
			t.Errorf("%s: %d %s %v", name, resp.StatusCode, resp.Header.Get("x-amzn-errortype"), m)
		}
	}
}

func TestVertexGemini(t *testing.T) {
	s, srv := newTestServer(t)
	token := map[string]string{"Authorization": "Bearer ya29.mock-token", "x-mock-chunk-delay-ms": "1"}
	const body = `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
	const project = "/v1/projects/my-project/locations/us-central1/publishers/google/models/"

	m := decode(t, post(t, srv, project+"gemini-2.5-flash:generateContent", token, body))
	if get(m, "modelVersion") != "gemini-2.5-flash" || num(get(m, "usageMetadata", "promptTokenCount")) != 120 {
		t.Fatalf("generateContent = %v", m)
	}
	if rec := s.lastRecorded(); rec.APIKey != "ya29.mock-token" || rec.Model != "gemini-2.5-flash" || !strings.HasPrefix(rec.Path, "/v1/projects/my-project/locations/us-central1/") {
		t.Fatalf("recorded %+v", rec)
	}
	_, data := sseData(t, post(t, srv, project+"gemini-2.5-flash:streamGenerateContent?alt=sse", token, body))
	if len(data) == 0 || !strings.Contains(data[len(data)-1], `"finishReason":"STOP"`) || !strings.Contains(data[len(data)-1], `"candidatesTokenCount":42`) {
		t.Fatalf("stream: %v", data)
	}
	m = decode(t, post(t, srv, project+"gemini-2.5-flash:countTokens", token, body))
	if num(get(m, "totalTokens")) == 0 {
		t.Fatalf("countTokens = %v", m)
	}

	// Express mode: no project, an API key.
	m = decode(t, post(t, srv, "/v1/publishers/google/models/gemini-2.5-flash:generateContent", map[string]string{"x-goog-api-key": "vertex-key-1"}, body))
	if get(m, "modelVersion") != "gemini-2.5-flash" {
		t.Fatalf("express = %v", m)
	}
	if rec := s.lastRecorded(); rec.APIKey != "vertex-key-1" {
		t.Fatalf("recorded %+v", rec)
	}
	// Vertex's beta, v1beta1, the same.
	m = decode(t, post(t, srv, "/v1beta1/projects/my-project/locations/us-central1/publishers/google/models/gemini-2.5-flash:generateContent", token, body))
	if get(m, "modelVersion") != "gemini-2.5-flash" {
		t.Fatalf("v1beta1 = %v", m)
	}
	// Errors keep Google's shape.
	resp := post(t, srv, project+"gemini-2.5-flash:generateContent", map[string]string{"Authorization": "Bearer tok-status-401"}, body)
	if m = decode(t, resp); resp.StatusCode != 401 || get(m, "error", "status") != "UNAUTHENTICATED" {
		t.Fatalf("injected: %d %v", resp.StatusCode, m)
	}
	resp = post(t, srv, project+"gemini-2.5-flash:predict", token, body)
	if resp.StatusCode != 404 {
		t.Fatalf("unknown action: %d", resp.StatusCode)
	}
}
