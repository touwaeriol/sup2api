package bedrock

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"maps"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tidwall/gjson"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/Sub2API-Devs/sup2api/next/sdk/awseventstream"
	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

// fakeBedrock is a Bedrock runtime: InvokeModel for Claude, called as a
// handler (nothing dials). It accepts the test API key and SigV4 requests
// whose signature it recomputes itself (verifySigV4), and refuses what the
// real service refuses: a body with "model", "stream" or another field
// InvokeModel does not know, without anthropic_version or max_tokens, with
// context_management and no beta feature for it, or with what Claude Code
// adds deep inside (a tool's "custom", a cache_control "scope").
type fakeBedrock struct {
	mux *http.ServeMux
	// expired makes the fake treat every session token as expired.
	expired bool

	mu     sync.Mutex
	served []servedRequest
}

// servedRequest is one request the fake answered with 200.
type servedRequest struct {
	// Path is the path as it came over the wire, still escaped.
	Path string
	// Model is the model the request invoked.
	Model string
	// Signed tells how it was authorized.
	Signed bool
	Body   string
}

func newFakeBedrock() *fakeBedrock {
	f := &fakeBedrock{mux: http.NewServeMux()}
	f.mux.HandleFunc("POST /model/{model}/invoke", f.invoke)
	f.mux.HandleFunc("POST /model/{model}/invoke-with-response-stream", f.invoke)
	return f
}

func (f *fakeBedrock) requests() []servedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.served)
}

func (f *fakeBedrock) record(r *http.Request, model string, signed bool, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.served = append(f.served, servedRequest{Path: r.URL.EscapedPath(), Model: model, Signed: signed, Body: string(body)})
}

func awsFailure(w http.ResponseWriter, status int, typ, message string) {
	w.Header().Set("x-amzn-errortype", typ+":http://internal.amazon.com/coral/com.amazon.bedrock/")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(mustJSON(map[string]string{"message": message})))
}

// authorize checks the credential of r. It answers the failure itself and
// reports whether the request may go on, and whether it was signed.
func (f *fakeBedrock) authorize(w http.ResponseWriter, r *http.Request, body []byte) (ok, signed bool) {
	auth := r.Header.Get("Authorization")
	if auth == "Bearer "+testAPIKey {
		return true, false
	}
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 ") {
		awsFailure(w, http.StatusForbidden, "AccessDeniedException", "Invalid API Key format: Must start with pre-defined prefix")
		return false, false
	}
	sig, err := verifySigV4(r, body, testNow)
	switch token := r.Header.Get("X-Amz-Security-Token"); {
	case err != nil:
		awsFailure(w, http.StatusForbidden, "InvalidSignatureException", err.Error())
	case sig.Service != SigningService || sig.Region != "us-west-2":
		awsFailure(w, http.StatusForbidden, "InvalidSignatureException", "Credential should be scoped to a valid region and service.")
	case sig.AccessKeyID == testSTSKeyID && token != testSessionToken, sig.AccessKeyID == testAccessKeyID && token != "":
		awsFailure(w, http.StatusForbidden, "UnrecognizedClientException", "The security token included in the request is invalid.")
	case token != "" && f.expired:
		awsFailure(w, http.StatusForbidden, "ExpiredTokenException", "The security token included in the request is expired")
	default:
		return true, true
	}
	return false, false
}

// claudeEvents are the events of a streamed Claude answer.
var claudeEvents = []string{
	`{"type":"message_start","message":{"id":"msg_bdrk_01","type":"message","role":"assistant","model":"claude-haiku-4-5-20251001","content":[],"usage":{"input_tokens":9,"output_tokens":1}}}`,
	`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
	`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"pong"}}`,
	`{"type":"content_block_stop","index":0}`,
	`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`,
	`{"type":"message_stop","amazon-bedrock-invocationMetrics":{"inputTokenCount":9,"outputTokenCount":3,"invocationLatency":100,"firstByteLatency":10}}`,
}

func (f *fakeBedrock) invoke(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	ok, signed := f.authorize(w, r, body)
	if !ok {
		return
	}
	fields := gjson.ParseBytes(body)
	switch {
	case !gjson.ValidBytes(body):
		awsFailure(w, http.StatusBadRequest, "ValidationException", "Malformed input request")
		return
	case fields.Get("model").Exists(), fields.Get("stream").Exists(), fields.Get("metadata").Exists(), fields.Get("provider").Exists():
		awsFailure(w, http.StatusBadRequest, "ValidationException", "Malformed input request: #: extraneous key is not permitted")
		return
	case fields.Get("context_management").Exists() && !strings.Contains(fields.Get("anthropic_beta").Raw, betaContextManagement):
		awsFailure(w, http.StatusBadRequest, "ValidationException", "context_management: Extra inputs are not permitted")
		return
	case strings.Contains(fields.Get("anthropic_beta").Raw, "claude-code-20250219"):
		awsFailure(w, http.StatusBadRequest, "ValidationException", "invalid beta flag")
		return
	case len(fields.Get("tools.#.custom").Array()) > 0:
		awsFailure(w, http.StatusBadRequest, "ValidationException", "tools.0.custom: Extra inputs are not permitted")
		return
	case bytes.Contains(body, []byte(`"scope":`)):
		awsFailure(w, http.StatusBadRequest, "ValidationException", "cache_control.scope: Extra inputs are not permitted")
		return
	case !fields.Get("max_tokens").Exists():
		awsFailure(w, http.StatusBadRequest, "ValidationException", "max_tokens: Field required")
		return
	case fields.Get("anthropic_version").String() != AnthropicVersion:
		awsFailure(w, http.StatusBadRequest, "ValidationException", "anthropic_version must be "+AnthropicVersion)
		return
	case r.Header.Get("Anthropic-Beta") != "":
		awsFailure(w, http.StatusBadRequest, "ValidationException", "beta features go into anthropic_beta")
		return
	case strings.HasPrefix(r.PathValue("model"), "anthropic.claude-opus"):
		awsFailure(w, http.StatusForbidden, "AccessDeniedException", "You don't have access to the model with the specified model ID.")
		return
	}
	f.record(r, r.PathValue("model"), signed, body)
	if !strings.HasSuffix(r.URL.Path, "/invoke-with-response-stream") {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_bdrk_01","type":"message","role":"assistant","model":"claude-haiku-4-5-20251001","content":[{"type":"text","text":"pong"}],"stop_reason":"end_turn","usage":{"input_tokens":9,"output_tokens":3}}`)
		return
	}
	w.Header().Set("Content-Type", awseventstream.ContentType)
	headers := []awseventstream.Header{
		{Name: awseventstream.HeaderEventType, Value: "chunk"},
		{Name: awseventstream.HeaderContentType, Value: "application/json"},
		{Name: awseventstream.HeaderMessageType, Value: awseventstream.MessageTypeEvent},
	}
	for _, event := range claudeEvents {
		payload := mustJSON(map[string]string{"bytes": base64.StdEncoding.EncodeToString([]byte(event))})
		frame, err := awseventstream.AppendMessage(nil, headers, []byte(payload))
		if err != nil {
			panic(err)
		}
		_, _ = w.Write(frame)
	}
}

// ---------------------------------------------------------------- the core's part

// answer is an upstream answer as the core relays it.
type answer struct {
	Status      int
	ContentType string
	Headers     map[string]string
	Body        []byte
}

// coreSend does the core's part of an attempt once Execute returned
// (DESIGN-2026-11-18-PLUGIN-FORWARDING-V2 §4.2 step 8, §4.5): parse the URL
// again, write the plugin's default headers, then the operator's settings
// (a value sets, "" removes), then the plugin's authoritative headers, send
// the body as it is, and decode the answer's framing into SSE when the
// plugin named one.
func coreSend(t *testing.T, f *fakeBedrock, up *pluginv1.UpstreamRequest, operator map[string]string) answer {
	t.Helper()
	req := httptest.NewRequest(up.GetMethod(), up.GetUrl(), bytes.NewReader(up.GetBody()))
	if req.URL.String() != up.GetUrl() {
		t.Fatalf("the URL does not survive a parse: %s -> %s", up.GetUrl(), req.URL)
	}
	for _, layer := range []map[string]string{up.GetDefaultHeaders(), operator, up.GetHeaders()} {
		for name, value := range layer {
			if value == "" {
				req.Header.Del(name)
				continue
			}
			req.Header.Set(name, value)
		}
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	resp := rec.Result()
	a := answer{Status: resp.StatusCode, Headers: map[string]string{}}
	for name := range resp.Header {
		a.Headers[strings.ToLower(name)] = resp.Header.Get(name)
	}
	a.ContentType, _, _ = mime.ParseMediaType(resp.Header.Get("Content-Type"))
	var stream io.Reader = resp.Body
	if up.GetResponseStream() == ResponseStream && resp.StatusCode/100 == 2 && a.ContentType == awseventstream.ContentType {
		stream, a.ContentType = awseventstream.NewChunkSSEReader(resp.Body), "text/event-stream"
	}
	var err error
	if a.Body, err = io.ReadAll(stream); err != nil {
		t.Fatalf("read the answer: %v", err)
	}
	return a
}

// sseEvents are the "event:" names of an SSE body, in order.
func sseEvents(body []byte) []string {
	var out []string
	for _, line := range strings.Split(string(body), "\n") {
		if name, ok := strings.CutPrefix(line, "event: "); ok {
			out = append(out, name)
		}
	}
	return out
}

// startPlugin runs p in the SDK harness (over gRPC, with the fake host's
// checks of a relay).
func startPlugin(t *testing.T, p *Plugin) *pluginsdktest.Harness {
	t.Helper()
	m, err := os.ReadFile("../../manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	// The fake host knows the manifest: it relays only to the hosts the
	// core would.
	return pluginsdktest.Start(t, p, pluginsdktest.Options{Manifest: m, SDK: []pluginsdk.Option{pluginsdk.WithInfo("bedrock", Version)}})
}

// save runs a submitted form through ValidateCredentials and returns the
// account the core would store.
func save(t *testing.T, h *pluginsdktest.Harness, typ string, creds, settings map[string]any) *pluginv1.Account {
	t.Helper()
	resp, err := h.Platform.ValidateCredentials(context.Background(), &pluginv1.ValidateCredentialsRequest{
		AccountType: typ, CredentialsJson: mustJSON(creds), SettingsJson: mustJSON(settings),
	})
	if err != nil || len(resp.GetErrors()) > 0 {
		t.Fatalf("save: %v %v", fieldErrors(resp), err)
	}
	return stored(typ, normalized(t, resp.GetNormalizedCredentialsJson(), creds), normalized(t, resp.GetNormalizedSettingsJson(), settings))
}

// execute is one attempt as the core makes it: Execute, which must relay
// exactly once without waiting and answer host_usage, and the relayed
// request.
func execute(t *testing.T, h *pluginsdktest.Harness, in *pluginv1.ExecuteRequest) *pluginv1.UpstreamRequest {
	t.Helper()
	ex := h.Execute(context.Background(), in)
	relay := ex.Relayed(t)
	if relay.Wait || !proto.Equal(ex.Response, pluginsdk.ReportHostUsage()) {
		t.Fatalf("relay waited: %v, response %v", relay.Wait, ex.Response)
	}
	return relay.Request
}

// classifyAnswer hands a failed answer to the plugin, as the core does.
func classifyAnswer(t *testing.T, h *pluginsdktest.Harness, acc *pluginv1.Account, a answer) *pluginv1.ClassifyErrorResponse {
	t.Helper()
	cls, err := h.Platform.ClassifyError(context.Background(), &pluginv1.ClassifyErrorRequest{
		Meta: &pluginv1.RequestMeta{Protocol: ProtocolMessages}, Account: acc, Status: int32(a.Status), Headers: a.Headers, BodyPrefix: a.Body,
	})
	if err != nil {
		t.Fatalf("ClassifyError: %v", err)
	}
	return cls
}

// accountTypes are the plugin's types in a fixed order.
var accountTypes = []string{TypeAPIKey, TypeAccessKey, TypeSTS}

// claudeCodeBody is claudeBody with what Claude Code puts deep inside a
// request and InvokeModel refuses.
func claudeCodeBody(model string, stream bool) string {
	return strings.Replace(claudeBody(model, stream), `"messages":[{"role":"user","content":"ping"}]`,
		`"messages":[{"role":"user","content":[{"type":"text","text":"ping","cache_control":{"type":"ephemeral","scope":"global"}}]}],`+
			`"tools":[{"name":"Read","input_schema":{"type":"object"},"custom":{"defer_loading":true}}]`, 1)
}

// executeCase is one standard input of the plugin: a request for an
// account, as the core hands it to Execute.
type executeCase struct {
	name string
	in   *pluginv1.ExecuteRequest
}

// executeCases are the plugin's standard inputs: every account type,
// streamed or not, a model id and an ARN, with a Claude Code body and the
// client's beta header. The accounts are in us-west-2, the region the fake
// Bedrock serves. The conformance suite (sdk/pluginsdk/pluginsdktest/
// conformance) is to be run over the same table.
func executeCases() []executeCase {
	var out []executeCase
	for _, typ := range accountTypes {
		for _, stream := range []bool{false, true} {
			for _, model := range []string{testHaiku, testARN} {
				in := executeRequest(stored(typ, typeCreds[typ], settingsOf("us-west-2", "us")), model, stream, claudeCodeBody(model, stream))
				in.InboundHeaders = map[string]string{"anthropic-beta": "claude-code-20250219,context-1m-2025-08-07"}
				out = append(out, executeCase{name: typ + "/" + model + "/stream=" + mustJSON(stream), in: in})
			}
		}
	}
	return out
}

// TestExecute runs every standard input through Execute and sends the
// relayed request to the fake Bedrock: one relay without waiting, host
// usage, and a request InvokeModel accepts, plain and streamed (the event
// stream arrives as SSE).
func TestExecute(t *testing.T) {
	f := newFakeBedrock()
	h := startPlugin(t, newTestPlugin(Endpoints{}))
	for _, c := range executeCases() {
		t.Run(c.name, func(t *testing.T) {
			up := execute(t, h, c.in)
			a := coreSend(t, f, up, nil)
			if a.Status != http.StatusOK {
				t.Fatalf("answer: %d %s", a.Status, a.Body)
			}
			if c.in.GetMeta().GetStream() {
				want := []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
				if a.ContentType != "text/event-stream" || !slices.Equal(sseEvents(a.Body), want) || !bytes.Contains(a.Body, []byte(`"usage":{"output_tokens":3}`)) {
					t.Fatalf("stream: %s\n%s", a.ContentType, a.Body)
				}
			} else if a.ContentType != "application/json" || gjson.GetBytes(a.Body, "usage.output_tokens").Int() != 3 {
				t.Fatalf("invoke: %s %s", a.ContentType, a.Body)
			}
			// What InvokeModel received: the client's body for Bedrock.
			served := f.requests()
			body := gjson.Parse(served[len(served)-1].Body)
			if body.Get("anthropic_beta").Raw != `["context-1m-2025-08-07","compact-2026-01-12"]` || body.Get("max_tokens").Raw != "64" ||
				body.Get("output_config").Raw != `{"effort":"low"}` || body.Get("tools.0").Raw != `{"name":"Read","input_schema":{"type":"object"}}` ||
				body.Get("messages.0.content.0.cache_control").Raw != `{"type":"ephemeral"}` {
				t.Errorf("InvokeModel body = %s", body.Raw)
			}
		})
	}
	// The model is one path segment on the wire, signed as sent.
	want := map[string]string{"us." + testHaiku: "/model/us." + haikuSegment, testARN: "/model/" + arnSegment}
	served := f.requests()
	for _, r := range served {
		if !strings.HasPrefix(r.Path, want[r.Model]+"/invoke") {
			t.Errorf("served %s for model %s", r.Path, r.Model)
		}
	}
	if len(served) != len(executeCases()) {
		t.Errorf("served %d requests", len(served))
	}
}

// TestExecuteHeaders: the authorization is authoritative (and with a
// signature the Content-Type it covers), the rest default, so the
// operator's header settings change what they may and break no signature.
func TestExecuteHeaders(t *testing.T) {
	f := newFakeBedrock()
	h := startPlugin(t, newTestPlugin(Endpoints{}))
	for _, tc := range []struct {
		typ                 string
		headers, defaults   []string
		signedHeaders, akid string
	}{
		// Without a session token the plugin removes X-Amz-Security-Token
		// (an authoritative empty value).
		{TypeAPIKey, []string{"authorization", "x-amz-security-token"}, []string{"accept", "content-type"}, "", ""},
		{TypeAccessKey, []string{"authorization", "content-type", "x-amz-date", "x-amz-security-token"}, []string{"accept", "content-type"}, "content-type;host;x-amz-date", testAccessKeyID},
		{TypeSTS, []string{"authorization", "content-type", "x-amz-date", "x-amz-security-token"}, []string{"accept", "content-type"},
			"content-type;host;x-amz-date;x-amz-security-token", testSTSKeyID},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			in := executeRequest(stored(tc.typ, typeCreds[tc.typ], settingsOf("us-west-2", "")), testHaiku, true, claudeBody(testHaiku, true))
			in.InboundHeaders = map[string]string{"anthropic-beta": "context-1m-2025-08-07", "user-agent": "claude-cli/2.1.90"}
			up := execute(t, h, in)
			if got := slices.Sorted(maps.Keys(up.GetHeaders())); !slices.Equal(got, tc.headers) {
				t.Fatalf("authoritative headers = %v, want %v", got, tc.headers)
			}
			// No client header goes on, the beta header included: its
			// features are in the body.
			if got := slices.Sorted(maps.Keys(up.GetDefaultHeaders())); !slices.Equal(got, tc.defaults) {
				t.Fatalf("default headers = %v, want %v", got, tc.defaults)
			}
			if up.GetDefaultHeaders()["content-type"] != "application/json" || up.GetDefaultHeaders()["accept"] != "application/json" {
				t.Fatalf("default headers = %v", up.GetDefaultHeaders())
			}
			if tc.typ == TypeAPIKey {
				if up.GetHeaders()["authorization"] != "Bearer "+testAPIKey {
					t.Fatalf("authorization = %q", up.GetHeaders()["authorization"])
				}
			} else {
				req := httptest.NewRequest(up.GetMethod(), up.GetUrl(), nil)
				for name, value := range up.GetHeaders() {
					if value != "" {
						req.Header.Set(name, value)
					}
				}
				sig, err := verifySigV4(req, up.GetBody(), testNow)
				if err != nil || sig.SignedHeaders != tc.signedHeaders || sig.AccessKeyID != tc.akid || sig.Region != "us-west-2" || sig.Service != "bedrock" {
					t.Fatalf("signature %+v: %v", sig, err)
				}
			}
			// The operator replaces or removes what it may; the
			// authorization and what the signature covers stay, and a
			// session token the operator adds reaches AWS only where the
			// signature carries one (it would fail any other signature).
			operator := map[string]string{"accept": "*/*", "user-agent": "operator", "content-type": "text/plain", "authorization": "Bearer other", "x-amz-date": "",
				"x-amz-security-token": "operator-token"}
			if a := coreSend(t, f, up, operator); a.Status != http.StatusOK {
				t.Fatalf("with the operator's settings: %d %s", a.Status, a.Body)
			}
		})
	}
}

// TestExecuteRebuilds: the same input gives the same request with a fixed
// clock, and every attempt is built afresh: a later attempt is signed at
// its own time.
func TestExecuteRebuilds(t *testing.T) {
	var clock atomic.Int64
	clock.Store(testNow.Unix())
	p := newTestPlugin(Endpoints{})
	p.now = func() time.Time { return time.Unix(clock.Load(), 0) }
	h := startPlugin(t, p)
	for _, c := range executeCases() {
		t.Run(c.name, func(t *testing.T) {
			clock.Store(testNow.Unix())
			first := execute(t, h, c.in)
			if again := execute(t, h, c.in); !proto.Equal(first, again) {
				t.Fatalf("the same input built\n%v\nand\n%v", first, again)
			}
			retry := proto.Clone(c.in).(*pluginv1.ExecuteRequest)
			retry.Attempt = 1
			clock.Store(testNow.Add(time.Minute).Unix())
			second := execute(t, h, retry)
			signed := c.in.GetAccount().GetType() != TypeAPIKey
			if signed == proto.Equal(first, second) {
				t.Fatalf("attempt 1, a minute later: same request = %v for a signed account = %v", proto.Equal(first, second), signed)
			}
			if !bytes.Equal(first.GetBody(), second.GetBody()) || first.GetUrl() != second.GetUrl() {
				t.Fatal("attempt 1 changed the body or the URL")
			}
		})
	}
}

// TestExecuteLargeBody: a large Claude Code request goes through Execute
// whole, and the request relayed is the one InvokeModel accepts.
func TestExecuteLargeBody(t *testing.T) {
	f := newFakeBedrock()
	h := startPlugin(t, newTestPlugin(Endpoints{}))
	body := largeBody(16 << 20)
	for _, typ := range []string{TypeAPIKey, TypeSTS} {
		up := execute(t, h, executeRequest(stored(typ, typeCreds[typ], settingsOf("us-west-2", "")), "claude-opus-4-7", true, string(body)))
		want, err := invokeBody(body, "", "global.claude-opus-4-7")
		if err != nil || !bytes.Equal(up.GetBody(), want) {
			t.Fatalf("%s: relayed %d bytes, want %d (%v)", typ, len(up.GetBody()), len(want), err)
		}
		if a := coreSend(t, f, up, nil); a.Status != http.StatusOK {
			t.Fatalf("%s: %d %s", typ, a.Status, a.Body)
		}
	}
}

// TestExecuteRefuses: what cannot be built is refused before anything is
// relayed, so the core moves on safely.
func TestExecuteRefuses(t *testing.T) {
	h := startPlugin(t, newTestPlugin(Endpoints{}))
	good := stored(TypeAccessKey, akskCreds, nil)
	for name, tc := range map[string]struct {
		in   *pluginv1.ExecuteRequest
		want codes.Code
	}{
		"another protocol":       {&pluginv1.ExecuteRequest{Account: good, Meta: &pluginv1.RequestMeta{Protocol: "openai.chat"}, Body: []byte(`{"model":"m"}`)}, codes.InvalidArgument},
		"a body that is no JSON": {executeRequest(good, testHaiku, false, `{"model":`), codes.InvalidArgument},
		"no model":               {executeRequest(good, "", false, `{"max_tokens":1}`), codes.InvalidArgument},
		"a broken account":       {executeRequest(&pluginv1.Account{Type: TypeAccessKey}, testHaiku, false, claudeBody(testHaiku, false)), codes.FailedPrecondition},
	} {
		ex := h.Execute(context.Background(), tc.in)
		if status.Code(ex.Err) != tc.want || len(ex.Relays()) != 0 || len(ex.RelayErrors()) != 0 {
			t.Errorf("%s: %v, %d relays", name, ex.Err, len(ex.Relays()))
		}
	}
}

// TestTestRequestRoundTrip sends the account test the way the host does:
// exactly as the plugin described it, signature included.
func TestTestRequestRoundTrip(t *testing.T) {
	for _, typ := range accountTypes {
		t.Run(typ, func(t *testing.T) {
			f := newFakeBedrock()
			h := startPlugin(t, newTestPlugin(Endpoints{}))
			acc := save(t, h, typ, typeCreds[typ], settingsOf("us-west-2", "global"))
			tr, err := h.Platform.BuildTestRequest(context.Background(), &pluginv1.BuildTestRequestRequest{Account: acc})
			if err != nil {
				t.Fatalf("BuildTestRequest: %v", err)
			}
			a := coreSend(t, f, &pluginv1.UpstreamRequest{Method: tr.GetMethod(), Url: tr.GetUrl(), Headers: tr.GetHeaders(), Body: []byte(tr.GetBodyJson())}, nil)
			if a.Status != http.StatusOK || gjson.GetBytes(a.Body, "usage.input_tokens").Int() != 9 {
				t.Fatalf("test request: %d %s", a.Status, a.Body)
			}
			if got := f.requests(); len(got) != 1 || got[0].Model != DefaultTestModel || got[0].Signed != (typ != TypeAPIKey) {
				t.Fatalf("served = %+v", got)
			}
		})
	}
}

// TestFailureRoundTrip classifies what the upstream really answers.
func TestFailureRoundTrip(t *testing.T) {
	const (
		failover = pluginv1.ClassifyErrorResponse_ACTION_FAILOVER
		none     = pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_UNSPECIFIED
		cooldown = pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_COOLDOWN
		disable  = pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_DISABLE
	)
	for _, tc := range []struct {
		name     string
		typ      string
		creds    map[string]any
		model    string
		expired  bool
		wantType string
		want     pluginv1.ClassifyErrorResponse_AccountEffect
	}{
		{"wrong secret", TypeAccessKey, with(akskCreds, map[string]any{fieldSecretAccessKey: "not-the-secret"}), testHaiku, false, "InvalidSignatureException", disable},
		{"wrong session token", TypeSTS, with(stsCreds, map[string]any{fieldSessionToken: "AnotherToken"}), testHaiku, false, "UnrecognizedClientException", disable},
		{"expired temporary credentials", TypeSTS, stsCreds, testHaiku, true, "ExpiredTokenException", disable},
		// How Bedrock refuses an unknown API key is not documented; the fake
		// answers a 403 without proof against the key, which only cools down.
		{"unknown API key", TypeAPIKey, map[string]any{fieldAPIKey: "ABSKanotherKey00"}, testHaiku, false, "AccessDeniedException", cooldown},
		{"model without access", TypeAccessKey, akskCreds, "anthropic.claude-opus-4-6-v1", false, "AccessDeniedException", cooldown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeBedrock()
			f.expired = tc.expired
			h := startPlugin(t, newTestPlugin(Endpoints{}))
			acc := save(t, h, tc.typ, tc.creds, settingsOf("us-west-2", NoProfilePrefix))
			a := coreSend(t, f, execute(t, h, executeRequest(acc, tc.model, true, claudeBody(tc.model, true))), nil)
			// A refused request is not an event stream: it stays as it is.
			if a.Status != http.StatusForbidden || a.ContentType != "application/json" || !strings.HasPrefix(a.Headers["x-amzn-errortype"], tc.wantType+":") {
				t.Fatalf("answer: %d %s %s %s", a.Status, a.ContentType, a.Headers["x-amzn-errortype"], a.Body)
			}
			cls := classifyAnswer(t, h, acc, a)
			if cls.GetAction() != failover || cls.GetAccountEffect() != tc.want || !strings.Contains(cls.GetReason(), "["+tc.wantType+"]") {
				t.Fatalf("classified %s / %s: %s", cls.GetAction(), cls.GetAccountEffect(), cls.GetReason())
			}
			if len(f.requests()) != 0 {
				t.Fatalf("a refused request was served")
			}
		})
	}

	// A request the upstream rejects for its body goes back to the client.
	f := newFakeBedrock()
	h := startPlugin(t, newTestPlugin(Endpoints{}))
	acc := save(t, h, TypeAccessKey, akskCreds, settingsOf("us-west-2", ""))
	a := coreSend(t, f, execute(t, h, executeRequest(acc, testHaiku, false, `{"model":"claude-haiku-4-5","messages":[]}`)), nil)
	cls := classifyAnswer(t, h, acc, a)
	if a.Status != http.StatusBadRequest || cls.GetAction() != pluginv1.ClassifyErrorResponse_ACTION_RETURN_TO_CLIENT || cls.GetAccountEffect() != none ||
		cls.GetClientErrorType() != "invalid_request_error" || cls.GetClientMessage() != "max_tokens: Field required" {
		t.Fatalf("400: %d %+v", a.Status, cls)
	}
}

// A clock that is off: the signature is refused for its time, and that is
// not held against the account.
func TestClockSkewRoundTrip(t *testing.T) {
	f := newFakeBedrock()
	p := newTestPlugin(Endpoints{})
	p.now = func() time.Time { return testNow.Add(20 * time.Minute) }
	h := startPlugin(t, p)
	acc := save(t, h, TypeAccessKey, akskCreds, settingsOf("us-west-2", ""))
	a := coreSend(t, f, execute(t, h, executeRequest(acc, testHaiku, false, claudeBody(testHaiku, false))), nil)
	cls := classifyAnswer(t, h, acc, a)
	if a.Status != http.StatusForbidden || cls.GetAction() != pluginv1.ClassifyErrorResponse_ACTION_FAILOVER ||
		cls.GetAccountEffect() != pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_UNSPECIFIED || !strings.Contains(cls.GetReason(), "clock") {
		t.Fatalf("skewed clock: %d %s / %s: %s", a.Status, cls.GetAction(), cls.GetAccountEffect(), cls.GetReason())
	}
}
