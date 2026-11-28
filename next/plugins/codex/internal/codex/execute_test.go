package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

// clientHeaders are the client headers the core hands over: only the ones
// of the manifest's passHeaders, the Codex backend's turn state. They go on
// as default headers; every other header is the plugin's own.
var clientHeaders = map[string]string{"x-codex-turn-state": "turn-1"}

// userHello is userMessage("hello") as it goes upstream.
const userHello = `[{"content":[{"text":"hello","type":"input_text"}],"role":"user","type":"message"}]`

// executeCase is one request as the core hands it to Execute.
type executeCase struct {
	name string
	in   *pluginv1.ExecuteRequest
}

// executeCases are the standard inputs of the request path: both account
// types, both upstream protocols. TestExecuteCases checks the invariants of
// the single path on them; the conformance suite
// (sdk/pluginsdk/pluginsdktest/conformance) takes the same table.
func executeCases() []executeCase {
	var out []executeCase
	for _, typ := range accountTypes {
		out = append(out,
			executeCase{typ + "/http", execRequest(typ, ProtocolResponses,
				`{"model":"gpt-5.4","stream":true,"input":"hello","temperature":0.2,"prompt_cache_key":"conv-1"}`)},
			executeCase{typ + "/websocket", execRequest(typ, ProtocolResponsesWS,
				`{"type":"response.create","model":"gpt-5.4","input":[{"role":"system","content":"s"}],"stream_options":{"include_usage":true}}`)},
		)
	}
	fedramp := execRequest(TypeDevice, ProtocolResponses, `{"model":"gpt-5.4","stream":true,"input":"hello"}`)
	fedramp.Account = account(TypeDevice, `{"access_token":"`+fakeJWT(map[string]any{authClaim: map[string]any{"chatgpt_account_is_fedramp": true}})+`"}`)
	return append(out, executeCase{"codex_device/http fedramp", fedramp})
}

// keptAbove is why each non-credential header is authoritative.
const keptAbove = "the plugin's own header, written above the operator's header settings"

// authoritativeHeaders are the headers the plugin writes authoritatively,
// name -> reason: the credential headers, and every other header of the
// plugin, all of which the operator's header settings leave as the plugin
// writes them. The conformance suite takes this table with executeCases.
var authoritativeHeaders = map[string]string{
	"authorization":      "credential",
	"chatgpt-account-id": "credential: the ChatGPT account the token acts for",
	"originator":         keptAbove + "; the account's client headers (agent.go)",
	"user-agent":         keptAbove + "; the account's client headers (agent.go)",
	"accept":             keptAbove,
	"content-type":       keptAbove,
	"openai-beta":        keptAbove,
	"session-id":          keptAbove + "; the upstream session (upstreamSession)",
	"thread-id":           keptAbove + "; the upstream session (upstreamSession)",
	"x-client-request-id": keptAbove + "; the upstream session (upstreamSession)",
	"x-openai-fedramp":    keptAbove + "; a FedRAMP account's marker",
}

// execRequest is a request of an account of type typ as the core hands it
// over: an HTTP one already streams (streamOnly), a WebSocket one carries
// the turn's message.
func execRequest(typ, protocol, body string) *pluginv1.ExecuteRequest {
	return &pluginv1.ExecuteRequest{
		Meta:           &pluginv1.RequestMeta{Protocol: protocol, Model: "gpt-5.4", Stream: protocol == ProtocolResponses, ApiKeyId: 7},
		Account:        account(typ, deviceCreds),
		Body:           []byte(body),
		InboundHeaders: clientHeaders,
	}
}

// failDial is the transport of everything that could reach the network
// from the plugin process during the request path tests: it fails the test.
type failDial struct{ t testing.TB }

func (f failDial) RoundTrip(r *http.Request) (*http.Response, error) {
	f.t.Errorf("the request path opened a connection to %s", r.URL)
	return nil, errors.New("no connections on the request path")
}

// startExecuting runs the plugin in the fake host with the HTTP client of
// the process made to fail the test when it dials (the plugin has no other).
func startExecuting(t *testing.T, ep Endpoints) *pluginsdktest.Harness {
	t.Helper()
	p := mustPlugin(ep)
	saved := http.DefaultTransport
	http.DefaultTransport = failDial{t}
	t.Cleanup(func() { http.DefaultTransport = saved })
	return startPlugin(t, p)
}

// relayed runs one Execute and returns the request it relayed, after
// checking the form of the single path: exactly one relay, without
// waiting, answered with host_usage.
func relayed(t *testing.T, h *pluginsdktest.Harness, in *pluginv1.ExecuteRequest) *pluginv1.UpstreamRequest {
	t.Helper()
	ex := h.Execute(context.Background(), in)
	r := ex.Relayed(t)
	if r.Wait || !proto.Equal(ex.Response, pluginsdk.ReportHostUsage()) {
		t.Fatalf("wait = %v, response = %v; want no wait and host_usage", r.Wait, ex.Response)
	}
	return r.Request
}

func TestExecuteCases(t *testing.T) {
	h := startExecuting(t, DefaultEndpoints)
	for _, tc := range executeCases() {
		r := relayed(t, h, tc.in)
		body := r.GetBody()
		if !json.Valid(body) || gjson.GetBytes(body, "model").String() != gjson.GetBytes(tc.in.GetBody(), "model").String() {
			t.Errorf("%s: body = %s", tc.name, body)
		}
		if tc.in.GetMeta().GetStream() && !gjson.GetBytes(body, "stream").Bool() {
			t.Errorf("%s: the stream flag was lost: %s", tc.name, body)
		}
		if !maps.Equal(r.GetDefaultHeaders(), clientHeaders) {
			t.Errorf("%s: default headers %v; only the passed client headers are", tc.name, r.GetDefaultHeaders())
		}
		for name := range r.GetHeaders() {
			if _, ok := authoritativeHeaders[name]; !ok {
				t.Errorf("%s: %s is authoritative without a reason", tc.name, name)
			}
		}
		// Deterministic, and every attempt is built again from what it is
		// handed (no cache per request).
		if again := relayed(t, h, tc.in); !proto.Equal(again, r) {
			t.Errorf("%s: two builds differ:\n%v\n%v", tc.name, r, again)
		}
		retry := proto.Clone(tc.in).(*pluginv1.ExecuteRequest)
		retry.Attempt = 1
		retry.Account = account(tc.in.GetAccount().GetType(), `{"access_token":"at-other","chatgpt_account_id":"acct-2"}`)
		other := relayed(t, h, retry)
		if other.GetHeaders()["authorization"] != "Bearer at-other" || other.GetHeaders()["chatgpt-account-id"] != "acct-2" || string(other.GetBody()) != string(body) {
			t.Errorf("%s: attempt 1 on another account = %v", tc.name, other)
		}
	}
}

func TestExecuteHTTP(t *testing.T) {
	h := startExecuting(t, DefaultEndpoints)
	in := execRequest(TypeDevice, ProtocolResponses,
		`{"model":"gpt-5.4", "stream":true,"input":"hello","temperature":0.2,"metadata":{"a":1},"tools":[ {"type":"function","name":"f"} ]}`)
	in.Meta.ClientProtocol = "openai.chat"
	r := relayed(t, h, in)
	if r.GetMethod() != http.MethodPost || r.GetUrl() != "https://chatgpt.com/backend-api/codex/responses" || r.GetUpstreamModel() != "gpt-5.4" ||
		r.GetResponseStream() != "" {
		t.Fatalf("request = %s %s %s %q", r.GetMethod(), r.GetUrl(), r.GetUpstreamModel(), r.GetResponseStream())
	}
	wantAuth := map[string]string{
		"authorization":      "Bearer at-device",
		"chatgpt-account-id": "acct-1",
		"originator":         "sup2api",
		"user-agent":         "sup2api-codex/" + Version,
		"accept":             "text/event-stream",
		"content-type":       "application/json",
	}
	if !maps.Equal(r.GetHeaders(), wantAuth) || !maps.Equal(r.GetDefaultHeaders(), clientHeaders) {
		t.Fatalf("headers = %v defaults = %v", r.GetHeaders(), r.GetDefaultHeaders())
	}
	// The edits land in place; every other byte is the client's.
	want := `{"model":"gpt-5.4", "stream":true,"input":` + userHello + `,"tools":[ {"type":"function","name":"f"} ],"store":false,"instructions":""}`
	if string(r.GetBody()) != want {
		t.Fatalf("body =\n%s\nwant\n%s", r.GetBody(), want)
	}

	// Instructions present, input already an array, store set, no account
	// id: only store changes, and no account header is sent.
	in = execRequest(TypeDevice, ProtocolResponses, `{"model":"gpt-5.1-codex","instructions":"be brief","input":[{"role":"user","content":"hi"}],"store":true,"stream":true}`)
	in.Account = account(TypeDevice, `{"access_token":"at"}`)
	r = relayed(t, h, in)
	if want := `{"model":"gpt-5.1-codex","instructions":"be brief","input":[{"role":"user","content":"hi"}],"store":false,"stream":true}`; string(r.GetBody()) != want {
		t.Fatalf("body = %s", r.GetBody())
	}
	if _, ok := r.GetHeaders()["chatgpt-account-id"]; ok || r.GetUpstreamModel() != "gpt-5.1-codex" {
		t.Errorf("headers = %v model = %s", r.GetHeaders(), r.GetUpstreamModel())
	}
}

// An image request reaches the plugin converted to Responses with the chat
// model driving the image tool at the top: the plugin reports no upstream
// model, so the core records the image model the response echoes
// (CONTRACTS §121.4). The body is edited as any other.
func TestExecuteImagesReportNoModel(t *testing.T) {
	h := startExecuting(t, DefaultEndpoints)
	const body = `{"model":"gpt-6-luna","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"a dot"}]}],` +
		`"tools":[{"type":"image_generation","model":"gpt-image-2","action":"generate"}],"tool_choice":{"type":"image_generation"},"stream":true}`
	for _, client := range []string{ProtocolImages, ProtocolImagesEdit} {
		in := execRequest(TypeDevice, ProtocolResponses, body)
		in.Meta.ClientProtocol = client
		r := relayed(t, h, in)
		if r.GetUpstreamModel() != "" {
			t.Errorf("%s: upstream model = %q", client, r.GetUpstreamModel())
		}
		if gjson.GetBytes(r.GetBody(), "store").Raw != "false" || gjson.GetBytes(r.GetBody(), "instructions").Raw != `""` ||
			gjson.GetBytes(r.GetBody(), "tools.0.model").Str != "gpt-image-2" {
			t.Errorf("%s: body = %s", client, r.GetBody())
		}
	}
}

// A Sign in with Codex account holds the same tokens: its request is the
// device code account's.
func TestExecuteOAuthIsDevice(t *testing.T) {
	h := startExecuting(t, DefaultEndpoints)
	for _, protocol := range []string{ProtocolResponses, ProtocolResponsesWS} {
		body := `{"model":"gpt-5.4","input":"  ","prompt_cache_key":"k"}`
		device, oauth := relayed(t, h, execRequest(TypeDevice, protocol, body)), relayed(t, h, execRequest(TypeOAuth, protocol, body))
		if !proto.Equal(device, oauth) {
			t.Fatalf("%s: codex_oauth = %v, want the device request %v", protocol, oauth, device)
		}
		if gjson.GetBytes(oauth.GetBody(), "input").Raw != "[]" {
			t.Fatalf("%s: blank input = %s", protocol, oauth.GetBody())
		}
	}
}

// A WebSocket turn is one Execute: the handshake (URL and headers) and the
// turn's message, edited like an HTTP body except that stream settings stay.
func TestExecuteWebSocket(t *testing.T) {
	h := startExecuting(t, DefaultEndpoints)
	turns := []string{
		`{"type":"response.create","model":"gpt-5.4","input":"hello","stream_options":{"include_usage":true},"temperature":1,"prompt_cache_key":"conv-1"}`,
		`{"type":"response.create","model":"gpt-5.4","previous_response_id":"resp_1","input":[{"role":"user","content":"more"}],"prompt_cache_key":"conv-1"}`,
	}
	want := []string{
		`{"type":"response.create","model":"gpt-5.4","input":` + userHello + `,"stream_options":{"include_usage":true},"prompt_cache_key":"conv-1","store":false,"instructions":""}`,
		`{"type":"response.create","model":"gpt-5.4","previous_response_id":"resp_1","input":[{"role":"user","content":"more"}],"prompt_cache_key":"conv-1","store":false,"instructions":""}`,
	}
	session := upstreamSessionID(7, 7, "conv-1")
	for _, typ := range accountTypes {
		for i, turn := range turns {
			r := relayed(t, h, execRequest(typ, ProtocolResponsesWS, turn))
			wantHeaders := map[string]string{
				"authorization": "Bearer at-device", "chatgpt-account-id": "acct-1", "originator": Originator, "user-agent": UserAgent,
				"openai-beta": WebSocketBeta, "session-id": session, "thread-id": session, "x-client-request-id": session,
			}
			if r.GetMethod() != http.MethodGet || r.GetUrl() != "wss://chatgpt.com/backend-api/codex/responses" ||
				!maps.Equal(r.GetHeaders(), wantHeaders) || !maps.Equal(r.GetDefaultHeaders(), clientHeaders) || r.GetResponseStream() != "" {
				t.Fatalf("%s turn %d handshake = %s %s %v %v", typ, i, r.GetMethod(), r.GetUrl(), r.GetHeaders(), r.GetDefaultHeaders())
			}
			if string(r.GetBody()) != want[i] {
				t.Fatalf("%s turn %d message =\n%s\nwant\n%s", typ, i, r.GetBody(), want[i])
			}
		}
	}
	// A test server root keeps its scheme family.
	h = startExecuting(t, testEndpoints("http://127.0.0.1:9"))
	h.Host.ExternalServices = []string{"127.0.0.1"}
	if r := relayed(t, h, execRequest(TypeOAuth, ProtocolResponsesWS, turns[1])); r.GetUrl() != "ws://127.0.0.1:9/backend-api/codex/responses" {
		t.Fatalf("ws url = %s", r.GetUrl())
	}
}

// The upstream session follows the conversation's prompt_cache_key, isolated
// per API key, over HTTP and WebSocket alike; without a usable key none is
// sent, and the client's own session header is never used.
func TestExecuteSession(t *testing.T) {
	h := startExecuting(t, DefaultEndpoints)
	build := func(protocol string, apiKeyID int64, body string) map[string]string {
		in := execRequest(TypeDevice, protocol, body)
		in.Meta.ApiKeyId = apiKeyID
		r := relayed(t, h, in)
		if _, ok := r.GetDefaultHeaders()["session-id"]; ok {
			t.Fatalf("session is a default header: %v", r.GetDefaultHeaders())
		}
		return r.GetHeaders()
	}
	conv := `{"model":"gpt-5.4","prompt_cache_key":"conv-1"}`
	first := build(ProtocolResponses, 7, conv)
	id := first["session-id"]
	if len(id) != 36 || id[14] != '8' || first["thread-id"] != id || first["x-client-request-id"] != id || strings.Contains(id, "conv-1") {
		t.Fatalf("session headers = %v", first)
	}
	if again := build(ProtocolResponses, 7, conv); again["session-id"] != id {
		t.Fatalf("same conversation, other session: %q vs %q", again["session-id"], id)
	}
	if ws := build(ProtocolResponsesWS, 7, conv); ws["session-id"] != id || ws["thread-id"] != id {
		t.Fatalf("ws session headers = %v, want %q", ws, id)
	}
	if other := build(ProtocolResponses, 8, conv); other["session-id"] == id {
		t.Fatal("two API keys share an upstream session")
	}
	if next := build(ProtocolResponses, 7, `{"prompt_cache_key":"conv-2"}`); next["session-id"] == id {
		t.Fatal("two conversations share an upstream session")
	}
	// The account id salts the session: another account gets another one,
	// and neither is the unsalted hash of before.
	in := execRequest(TypeDevice, ProtocolResponses, conv)
	in.Account.Id = 8
	r := relayed(t, h, in)
	if other := r.GetHeaders()["session-id"]; other == id || other != upstreamSessionID(8, 7, "conv-1") || id != upstreamSessionID(7, 7, "conv-1") {
		t.Fatalf("account 8 session %q, account 7 %q", other, id)
	}
	// The formula, computed here: SHA-256 of "<account id>:<api key id>:<key>",
	// version 8 and variant bits set, laid out as a UUID.
	want := sha256.Sum256([]byte("7:7:conv-1"))
	want[6] = want[6]&0x0f | 0x80
	want[8] = want[8]&0x3f | 0x80
	if h := hex.EncodeToString(want[:16]); id != h[0:8]+"-"+h[8:12]+"-"+h[12:16]+"-"+h[16:20]+"-"+h[20:32] {
		t.Fatalf("session %q, want the formula's %s", id, h)
	}
	unsalted := sha256.Sum256([]byte("7:conv-1"))
	if strings.HasPrefix(strings.ReplaceAll(id, "-", ""), fmt.Sprintf("%x", unsalted[0:4])) {
		t.Fatal("the session is the unsalted hash")
	}
	// The account id is only a hash input: it appears nowhere in the request.
	in.Account.Id = 987654321
	r = relayed(t, h, in)
	for k, v := range r.GetHeaders() {
		if strings.Contains(k+v, "987654321") {
			t.Fatalf("header %s carries the account id", k)
		}
	}
	if strings.Contains(string(r.GetBody())+r.GetUrl(), "987654321") {
		t.Fatal("the request carries the account id")
	}
	for _, body := range []string{`{}`, `{"prompt_cache_key":""}`, `{"prompt_cache_key":123}`} {
		hd := build(ProtocolResponses, 7, body)
		for _, name := range sessionHeaders {
			if _, ok := hd[name]; ok {
				t.Errorf("%s: %s sent: %v", body, name, hd)
			}
		}
	}
}

// A request the plugin cannot build fails before anything is relayed.
func TestExecuteErrors(t *testing.T) {
	h := startExecuting(t, DefaultEndpoints)
	for _, tc := range []struct {
		name     string
		protocol string
		acc      *pluginv1.Account
		body     string
		code     codes.Code
	}{
		{"chat protocol", "openai.chat", account(TypeDevice, deviceCreds), `{}`, codes.InvalidArgument},
		{"embeddings", "openai.embeddings", account(TypeOAuth, deviceCreds), `{}`, codes.InvalidArgument},
		{"foreign type", ProtocolResponses, account("apikey", deviceCreds), `{}`, codes.FailedPrecondition},
		{"no token", ProtocolResponses, account(TypeDevice, `{"refresh_token":"x"}`), `{}`, codes.FailedPrecondition},
		{"bad credentials", ProtocolResponses, account(TypeDevice, `{`), `{}`, codes.InvalidArgument},
		{"array body", ProtocolResponses, account(TypeDevice, deviceCreds), `[1]`, codes.InvalidArgument},
		{"no body", ProtocolResponsesWS, account(TypeDevice, deviceCreds), ``, codes.InvalidArgument},
	} {
		in := execRequest(TypeDevice, tc.protocol, tc.body)
		in.Account = tc.acc
		ex := h.Execute(context.Background(), in)
		if status.Code(ex.Err) != tc.code || len(ex.Relays()) != 0 || len(ex.RelayErrors()) != 0 {
			t.Errorf("%s: %v (relays %d), want %v and no relay", tc.name, ex.Err, len(ex.Relays()), tc.code)
		}
	}
}

// A body at a large size goes through the whole path and comes back with
// only the edits applied.
func TestExecuteLargeBody(t *testing.T) {
	h := startExecuting(t, DefaultEndpoints)
	big := strings.Repeat("x", 24<<20)
	head := `{"model":"gpt-5.4","stream":true,"input":[{"role":"system","content":"s"},{"role":"user","content":"`
	r := relayed(t, h, execRequest(TypeDevice, ProtocolResponses, head+big+`"}],"temperature":1,"instructions":""}`))
	want := `{"model":"gpt-5.4","stream":true,"input":[{"role":"developer","content":"s"},{"role":"user","content":"` + big + `"}],"instructions":"","store":false}`
	if string(r.GetBody()) != want {
		t.Fatalf("large body differs (len %d, want %d)", len(r.GetBody()), len(want))
	}
}
