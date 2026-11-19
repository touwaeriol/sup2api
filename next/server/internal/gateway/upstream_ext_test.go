package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/Sub2API-Devs/sup2api/next/sdk/awseventstream"
	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// ---------------------------------------------------------------- fakes

const (
	extInput  = 25
	extOutput = 12
)

// extPlatform is a plugin that answers like a Bedrock plugin would: the
// model in the path, a body without model and stream, its own credential
// header (a plugin signs its requests itself) and, for a streaming client
// request, the event stream framing in response_stream.
type extPlatform struct {
	*fakePlatform
	// amend edits the built request; nil leaves it as the default below.
	amend func(in *pluginv1.ExecuteRequest, out *pluginv1.UpstreamRequest)
}

func (p *extPlatform) Execute(ctx context.Context, in *pluginv1.ExecuteRequest, client core.ExecutionClient) (core.ExecutionResult, error) {
	return fakeExecute(ctx, p, in, client)
}

func (p *extPlatform) buildUpstream(ctx context.Context, in *pluginv1.ExecuteRequest) (*pluginv1.UpstreamRequest, error) {
	// The embedded fake records the call and runs its hook.
	if _, err := p.fakePlatform.buildUpstream(ctx, in); err != nil {
		return nil, err
	}
	stream := gjson.GetBytes(in.GetBody(), "stream").Bool()
	body, _ := sjson.DeleteBytes(in.GetBody(), "model")
	body, _ = sjson.DeleteBytes(body, "stream")
	body, _ = sjson.SetBytes(body, "anthropic_version", "bedrock-2023-05-31")
	action := "invoke"
	out := &pluginv1.UpstreamRequest{Method: "POST", Body: body,
		DefaultHeaders: map[string]string{"accept": "application/json"},
		Headers: map[string]string{"content-type": "application/json",
			"authorization": "Bearer " + gjson.Get(in.GetAccount().GetCredentialsJson(), "access_key_id").String()}}
	if stream {
		action, out.ResponseStream = "invoke-with-response-stream", awseventstream.FormatChunk
	}
	// The model id carries ":" as %3A on the wire, as Bedrock's do.
	out.Url = p.base + "/model/anthropic." + in.GetMeta().GetModel() + "-v1%3A0/" + action
	if p.amend != nil {
		p.amend(in, out)
	}
	return out, nil
}

// extCall is one request the upstream received.
type extCall struct {
	accessKey string
	path      string
	header    http.Header
	body      []byte
}

// extUpstream answers InvokeModel with JSON and InvokeModelWithResponseStream
// with an AWS event stream.
type extUpstream struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls []extCall
	// status answers the access key with an error status.
	status map[string]int
	// exception ends a stream with an exception frame after message_start.
	exception bool
	// plainSSE answers a stream request with ordinary SSE.
	plainSSE bool
}

func newExtUpstream(t *testing.T) *extUpstream {
	u := &extUpstream{status: map[string]int{}}
	u.srv = httptest.NewServer(http.HandlerFunc(u.handle))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *extUpstream) all() []extCall {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]extCall(nil), u.calls...)
}

func (u *extUpstream) last(t *testing.T) extCall {
	t.Helper()
	calls := u.all()
	require.NotEmpty(t, calls, "the upstream received nothing")
	return calls[len(calls)-1]
}

func extEvents() []string {
	return []string{
		fmt.Sprintf(`{"type":"message_start","message":{"id":"msg_bdrk_1","type":"message","role":"assistant","model":"claude-up","content":[],"usage":{"input_tokens":%d,"output_tokens":1}}}`, extInput),
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
		`{"type":"content_block_stop","index":0}`,
		fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":%d}}`, extOutput),
		fmt.Sprintf(`{"type":"message_stop","amazon-bedrock-invocationMetrics":{"inputTokenCount":%d,"outputTokenCount":%d}}`, extInput, extOutput),
	}
}

func extFrame(headers []awseventstream.Header, payload []byte) []byte {
	frame, err := awseventstream.AppendMessage(nil, headers, payload)
	if err != nil {
		panic(err)
	}
	return frame
}

func extChunk(event string) []byte {
	return extFrame([]awseventstream.Header{{Name: ":event-type", Value: "chunk"}, {Name: ":content-type", Value: "application/json"}, {Name: ":message-type", Value: "event"}},
		[]byte(`{"bytes":"`+base64.StdEncoding.EncodeToString([]byte(event))+`"}`))
}

func (u *extUpstream) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	accessKey := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	u.mu.Lock()
	u.calls = append(u.calls, extCall{accessKey: accessKey, path: r.URL.EscapedPath(), header: r.Header.Clone(), body: body})
	status, exception, plainSSE := u.status[accessKey], u.exception, u.plainSSE
	u.mu.Unlock()
	switch {
	case status != 0:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"message":"mock status %d"}`, status)
	case !strings.HasSuffix(r.URL.Path, "/invoke-with-response-stream"):
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"msg_bdrk_1","type":"message","role":"assistant","model":"claude-up","content":[{"type":"text","text":"hello"}],`+
			`"stop_reason":"end_turn","usage":{"input_tokens":%d,"output_tokens":%d}}`, extInput, extOutput)
	case plainSSE:
		w.Header().Set("Content-Type", "text/event-stream")
		for _, ev := range extEvents() {
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", gjson.Get(ev, "type").String(), ev)
		}
	default:
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		events := extEvents()
		if exception {
			events = events[:1]
		}
		for _, ev := range events {
			_, _ = w.Write(extChunk(ev))
			w.(http.Flusher).Flush()
		}
		if exception {
			_, _ = w.Write(extFrame([]awseventstream.Header{{Name: ":exception-type", Value: "modelStreamErrorException"}, {Name: ":message-type", Value: "exception"}},
				[]byte(`{"message":"the model stream broke"}`)))
		}
	}
}

// extEnv is an environment whose anthropic accounts are served by an
// extPlatform in front of an extUpstream.
type extEnv struct {
	*env
	up   *extUpstream
	plat *extPlatform
}

func newExtEnv(t *testing.T, opts ...envOpt) *extEnv {
	t.Helper()
	x := &extEnv{env: newEnv(t, opts...), up: newExtUpstream(t)}
	x.plat = &extPlatform{fakePlatform: &fakePlatform{base: x.up.srv.URL}}
	x.gen.accountTypes[0].Client = x.plat
	for id := int64(1); id <= 3; id++ {
		raw, _ := json.Marshal(map[string]string{"access_key_id": fmt.Sprintf("AK%d", id)})
		editAccount(x.env, id, func(a *core.Account) { a.Credentials = raw })
	}
	return x
}

// editAccount changes the stored account id.
func editAccount(e *env, id int64, fn func(a *core.Account)) {
	e.accounts.mu.Lock()
	defer e.accounts.mu.Unlock()
	fn(e.accounts.accounts[id])
}

// withParamOverride gives account id the request parameter override raw
// (CONTRACTS §84).
func withParamOverride(t *testing.T, e *env, id int64, raw string) {
	t.Helper()
	p, errs := core.CompileParamOverride(context.Background(), "param_override", []byte(raw))
	require.Empty(t, errs)
	editAccount(e, id, func(a *core.Account) { a.ParamOverride = p })
}

// withHeaderOverride gives account id the request header override raw
// (CONTRACTS §108).
func withHeaderOverride(t *testing.T, e *env, id int64, raw string) {
	t.Helper()
	h, errs := core.CompileHeaderOverride(context.Background(), "header_override", []byte(raw))
	require.Empty(t, errs)
	editAccount(e, id, func(a *core.Account) { a.HeaderOverride = h })
}

// ---------------------------------------------------------------- the plugin's bytes

// The plugin is handed the body after the core's rewrites (the account's
// parameter override, the model mapping in meta.model) and the upstream
// receives exactly the bytes it returned: the core neither inspects nor
// edits them, and an event stream it did not announce stays binary.
func TestRelayedBodyIsThePluginsBytes(t *testing.T) {
	x := newExtEnv(t)
	withParamOverride(t, x.env, 1, `{"operations":[{"mode":"set","path":"temperature","value":0.3},
		{"mode":"append","path":"messages.-1.content","value":"!"}]}`)
	editAccount(x.env, 1, func(a *core.Account) { a.ModelMapping = map[string]string{testModel: "claude-mapped"} })
	var sent []byte
	x.plat.amend = func(in *pluginv1.ExecuteRequest, out *pluginv1.UpstreamRequest) {
		assert.Equal(t, 0.3, gjson.GetBytes(in.GetBody(), "temperature").Float(), "handed after the override")
		assert.Equal(t, "hello there!", gjson.GetBytes(in.GetBody(), "messages.0.content").String())
		assert.Equal(t, "claude-mapped", gjson.GetBytes(in.GetBody(), "model").String(), "handed after the model mapping")
		// Bytes no JSON encoder would write: they must arrive as they are.
		out.Body = append([]byte("  "), append(out.Body, "\n\t"...)...)
		sent = out.Body
	}
	r := x.messages(body(testModel, false))
	require.Equal(t, 200, r.status, string(r.body))
	got := x.up.last(t)
	assert.Equal(t, string(sent), string(got.body))
	assert.Equal(t, "/model/anthropic.claude-mapped-v1%3A0/invoke", got.path)
	assert.Equal(t, "AK1", got.accessKey)
	assert.Empty(t, got.header.Get("x-api-key"), "the client's key does not reach the upstream")
	assert.Equal(t, "hello", r.json().Get("content.0.text").String())
	rec := x.record()
	assert.True(t, rec.Success)
	assert.Equal(t, "claude-mapped", rec.UpstreamModel)
	assert.Equal(t, core.UsageTokens{Input: extInput, Output: extOutput}, rec.Tokens)

	// An event stream the plugin did not announce stays binary.
	x.plat.amend = func(_ *pluginv1.ExecuteRequest, out *pluginv1.UpstreamRequest) { out.ResponseStream = "" }
	r = x.messages(body(testModel, true))
	assert.False(t, isSSE(r.header.Get("Content-Type")), "%d %s", r.status, r.header.Get("Content-Type"))
	assert.NotContains(t, string(r.body), "event: message_start")
}

// A request that fails over is executed again for the next account, with
// that account's credentials.
func TestFailoverExecutesAgainPerAttempt(t *testing.T) {
	x := newExtEnv(t)
	x.up.status["AK1"] = 500
	r := x.messages(body(testModel, false))
	require.Equal(t, 200, r.status, string(r.body))
	calls := x.up.all()
	require.Len(t, calls, 2)
	assert.Equal(t, "AK1", calls[0].accessKey)
	assert.Equal(t, "AK2", calls[1].accessKey)
	builds := x.plat.buildCalls()
	require.Len(t, builds, 2)
	for i, want := range []int64{1, 2} {
		assert.Equal(t, want, builds[i].GetAccount().GetId(), "attempt %d", i)
		assert.EqualValues(t, i, builds[i].GetAttempt(), "attempt %d", i)
	}
	rec := x.record()
	assert.True(t, rec.Success)
	assert.EqualValues(t, 2, *rec.AccountID)
	assert.Equal(t, 2, rec.Attempts)
}

// ---------------------------------------------------------------- header layers

// wantHeaders is what the upstream must have received: the given headers
// plus the transport's own (its User-Agent unless h names one).
func wantHeaders(got extCall, h http.Header) http.Header {
	h.Set("Content-Length", strconv.Itoa(len(got.body)))
	h.Set("Accept-Encoding", "gzip")
	if h.Get("User-Agent") == "" {
		h.Set("User-Agent", "Go-http-client/1.1")
	}
	return h
}

// The plugin's default headers yield to the operator: the account's header
// override replaces or removes them, the passthrough only fills headers
// that are not set (CONTRACTS §86.5).
func TestDefaultHeadersYieldToTheOperator(t *testing.T) {
	x := newExtEnv(t)
	x.plat.amend = func(_ *pluginv1.ExecuteRequest, out *pluginv1.UpstreamRequest) {
		out.DefaultHeaders = map[string]string{"accept": "application/json", "x-default-a": "plugin-a", "x-default-b": "plugin-b",
			"x-default-c": "plugin-c", "x-default-d": "plugin-d", "user-agent": "plugin-ua/1"}
	}
	withHeaderOverride(t, x.env, 1, `{"x-default-a":"op-a","x-default-d":"{unset}","user-agent":"op-ua/2"}`)
	editAccount(x.env, 1, func(a *core.Account) { a.HeaderPassthrough = []string{"x-default-b", "x-client-only"} })

	r := x.do("/v1/messages", body(testModel, false), map[string]string{"X-Default-A": "client-a", "X-Default-B": "client-b",
		"X-Default-C": "client-c", "X-Client-Only": "c1"})
	require.Equal(t, 200, r.status, string(r.body))
	got := x.up.last(t)
	assert.Equal(t, wantHeaders(got, http.Header{
		// Authoritative.
		"Authorization": {"Bearer AK1"}, "Content-Type": {"application/json"},
		// Defaults: replaced by the override, kept over a passed-through
		// client value, kept when nothing touches them.
		"Accept": {"application/json"}, "X-Default-A": {"op-a"}, "X-Default-B": {"plugin-b"}, "X-Default-C": {"plugin-c"},
		"User-Agent": {"op-ua/2"},
		// Filled by the passthrough where the plugin set nothing.
		"X-Client-Only": {"c1"},
	}), got.header)
	assert.Empty(t, got.header.Values("X-Default-D"), "{unset} removes a default header")
}

// A parameter override's header operations replace and remove default
// headers like the header override does.
func TestParamOverrideReplacesDefaultHeaders(t *testing.T) {
	x := newExtEnv(t)
	x.plat.amend = func(_ *pluginv1.ExecuteRequest, out *pluginv1.UpstreamRequest) {
		out.DefaultHeaders = map[string]string{"accept": "application/json", "x-default-a": "plugin-a", "x-default-b": "plugin-b"}
	}
	withParamOverride(t, x.env, 1, `{"operations":[{"mode":"set_header","path":"x-default-a","value":"op-a"},
		{"mode":"delete_header","path":"x-default-b"},{"mode":"set_header","path":"x-new","value":"op-new"}]}`)
	r := x.messages(body(testModel, false))
	require.Equal(t, 200, r.status, string(r.body))
	got := x.up.last(t)
	assert.Equal(t, wantHeaders(got, http.Header{
		"Authorization": {"Bearer AK1"}, "Content-Type": {"application/json"}, "Accept": {"application/json"},
		"X-Default-A": {"op-a"}, "X-New": {"op-new"},
	}), got.header)
}

// The plugin's authoritative headers reach the upstream as built: neither
// the header override nor the parameter override's header operations nor a
// passed-through client header change or remove them.
func TestAuthoritativeHeadersCannotBeOverridden(t *testing.T) {
	x := newExtEnv(t)
	x.plat.amend = func(_ *pluginv1.ExecuteRequest, out *pluginv1.UpstreamRequest) {
		out.Headers["x-amz-date"] = "20261118T000000Z"
		out.Headers["x-plugin-sig"] = "sig-1"
		out.Headers["anthropic-version"] = "plugin-version"
		// A name in both maps takes the authoritative value.
		out.DefaultHeaders["x-plugin-sig"] = "default-sig"
	}
	withHeaderOverride(t, x.env, 1, `{"x-plugin-sig":"op-sig","x-amz-date":"{unset}","x-extra":"op-extra"}`)
	withParamOverride(t, x.env, 1, `{"operations":[{"mode":"delete_header","path":"anthropic-version"},
		{"mode":"set_header","path":"x-amz-date","value":"19700101T000000Z"},
		{"mode":"set_header","path":"content-type","value":"text/plain"}]}`)
	editAccount(x.env, 1, func(a *core.Account) { a.HeaderPassthrough = []string{"*"} })

	r := x.do("/v1/messages", body(testModel, false), map[string]string{"X-Plugin-Sig": "client-sig", "X-Amz-Date": "client-date"})
	require.Equal(t, 200, r.status, string(r.body))
	got := x.up.last(t)
	assert.Equal(t, "Bearer AK1", got.header.Get("Authorization"))
	assert.Equal(t, []string{"sig-1"}, got.header.Values("X-Plugin-Sig"))
	assert.Equal(t, []string{"20261118T000000Z"}, got.header.Values("X-Amz-Date"))
	assert.Equal(t, []string{"plugin-version"}, got.header.Values("Anthropic-Version"))
	assert.Equal(t, []string{"application/json"}, got.header.Values("Content-Type"))
	assert.Equal(t, "op-extra", got.header.Get("X-Extra"), "the override still applies to other headers")
	assert.True(t, x.record().Success)
}

// An authoritative header with an empty value is not sent at all: it
// removes what a lower layer set - a default header, a passed-through client
// header, the operator's header override (CONTRACTS §112.17).
func TestEmptyAuthoritativeHeaderRemovesIt(t *testing.T) {
	x := newExtEnv(t)
	x.plat.amend = func(_ *pluginv1.ExecuteRequest, out *pluginv1.UpstreamRequest) {
		out.DefaultHeaders["x-default-z"] = "plugin-z"
		for _, name := range []string{"x-amz-security-token", "x-default-z", "x-client-z", "x-never-set"} {
			out.Headers[name] = ""
		}
	}
	withHeaderOverride(t, x.env, 1, `{"x-amz-security-token":"op-token","x-extra":"op-extra"}`)
	editAccount(x.env, 1, func(a *core.Account) { a.HeaderPassthrough = []string{"*"} })

	r := x.do("/v1/messages", body(testModel, false), map[string]string{"X-Client-Z": "client-z"})
	require.Equal(t, 200, r.status, string(r.body))
	got := x.up.last(t)
	for _, name := range []string{"X-Amz-Security-Token", "X-Default-Z", "X-Client-Z", "X-Never-Set"} {
		_, sent := got.header[name]
		assert.False(t, sent, "%s was sent: %q", name, got.header.Values(name))
	}
	assert.Equal(t, "op-extra", got.header.Get("X-Extra"), "the override still applies to other headers")
}

// ---------------------------------------------------------------- refused relays

// The core relays only to a host the plugin declared (externalServices) or
// the account names among its upstream addresses (CONTRACTS §112.17). A
// relay elsewhere sends nothing: the attempt fails over as the plugin's
// failure, the account untouched, and the log names the plugin and the
// host. The test manifest declares 127.0.0.1; localhost reaches the same
// server under a name it does not declare.
func TestRelayToUndeclaredHostSendsNothing(t *testing.T) {
	toLocalhost := func(raw string) string { return strings.Replace(raw, "127.0.0.1", "localhost", 1) }
	t.Run("neither declared nor configured", func(t *testing.T) {
		logs := captureWarnings(t)
		x := newExtEnv(t)
		x.plat.amend = func(in *pluginv1.ExecuteRequest, out *pluginv1.UpstreamRequest) {
			if in.GetAccount().GetId() == 1 {
				out.Url = toLocalhost(out.Url)
			}
		}
		r := x.messages(body(testModel, false))
		require.Equal(t, 200, r.status, string(r.body))
		calls := x.up.all()
		require.Len(t, calls, 1, "the refused relay sent nothing")
		assert.Equal(t, "AK2", calls[0].accessKey)
		rec := x.record()
		assert.EqualValues(t, 2, *rec.AccountID)
		require.NotNil(t, rec.AttemptTrail)
		assert.Equal(t, errTypePluginUnavailable, rec.AttemptTrail.Steps[0].ErrorType)
		assert.Empty(t, x.accounts.cooldowns)
		assert.Empty(t, x.accounts.disabled)
		assert.Contains(t, logs.String(), "upstream host not allowed")
		assert.Contains(t, logs.String(), "plugin=anthropic")
		assert.Contains(t, logs.String(), "host=localhost")
	})
	t.Run("the account's upstream address", func(t *testing.T) {
		x := newExtEnv(t)
		x.man.AccountTypes[0].UpstreamFields = []string{"base_url"}
		x.plat.amend = func(_ *pluginv1.ExecuteRequest, out *pluginv1.UpstreamRequest) { out.Url = toLocalhost(out.Url) }
		editAccount(x.env, 1, func(a *core.Account) {
			a.Settings = json.RawMessage(`{"base_url":` + strconvQuote(toLocalhost(x.up.srv.URL)) + `}`)
		})
		r := x.messages(body(testModel, false))
		require.Equal(t, 200, r.status, string(r.body))
		calls := x.up.all()
		require.Len(t, calls, 1)
		assert.Equal(t, "AK1", calls[0].accessKey, "the account naming the host is served")
		assert.EqualValues(t, 1, *x.record().AccountID)

		// Another account's address does not open the host to this one.
		x.accounts.groups[testGroup] = []int64{2}
		r = x.messages(body(testModel, false))
		assert.GreaterOrEqual(t, r.status, 500, string(r.body))
		assert.Len(t, x.up.all(), 1, "nothing more sent")
		assert.Empty(t, x.accounts.cooldowns)
	})
}

// A relay to an address the SSRF guard refuses sends nothing: the attempt
// fails over as the plugin's failure, and no account is cooled down or
// disabled.
func TestRelayToRefusedAddressSendsNothing(t *testing.T) {
	t.Run("scheme", func(t *testing.T) {
		x := newExtEnv(t)
		x.plat.amend = func(in *pluginv1.ExecuteRequest, out *pluginv1.UpstreamRequest) {
			if in.GetAccount().GetId() == 1 {
				out.Url = "file:///etc/passwd"
			}
		}
		r := x.messages(body(testModel, false))
		require.Equal(t, 200, r.status, string(r.body))
		calls := x.up.all()
		require.Len(t, calls, 1, "the refused relay sent nothing")
		assert.Equal(t, "AK2", calls[0].accessKey)
		rec := x.record()
		assert.EqualValues(t, 2, *rec.AccountID)
		assert.Equal(t, 2, rec.Attempts)
		assert.Empty(t, x.accounts.cooldowns)
		assert.Empty(t, x.accounts.disabled)
	})
	t.Run("private address", func(t *testing.T) {
		x := newExtEnv(t)
		x.gw.allowPrivate = false // the mock upstream is on loopback
		r := x.messages(body(testModel, false))
		assert.GreaterOrEqual(t, r.status, 500, string(r.body))
		assert.Empty(t, x.up.all(), "nothing reached the private address")
		assert.Len(t, x.plat.buildCalls(), 3, "every account was tried")
		rec := x.record()
		assert.False(t, rec.Success)
		assert.Equal(t, 3, rec.Attempts)
		assert.Empty(t, x.accounts.cooldowns)
		assert.Empty(t, x.accounts.disabled)
	})
}

// An unknown response_stream name is refused before anything is sent: the
// attempt fails over as the plugin's failure, the account untouched.
func TestUnknownResponseStreamSendsNothing(t *testing.T) {
	x := newExtEnv(t)
	// Account 1 (first by priority) gets the bad answer.
	x.plat.amend = func(in *pluginv1.ExecuteRequest, out *pluginv1.UpstreamRequest) {
		if in.GetAccount().GetId() == 1 {
			out.ResponseStream = "smithy-v7"
		}
	}
	r := x.messages(body(testModel, true))
	require.Equal(t, 200, r.status, string(r.body))
	calls := x.up.all()
	require.Len(t, calls, 1, "the first attempt sent nothing")
	assert.Equal(t, "AK2", calls[0].accessKey)
	rec := x.record()
	assert.EqualValues(t, 2, *rec.AccountID)
	assert.Empty(t, x.accounts.disabled, "a plugin failure does not touch the account")
	assert.Empty(t, x.accounts.cooldowns)

	// With no other account the client gets the plugin failure.
	x.accounts.groups[testGroup] = []int64{1}
	r = x.messages(body(testModel, true))
	assert.GreaterOrEqual(t, r.status, 500, string(r.body))
	assert.Len(t, x.up.all(), 1, "still nothing sent")
	rec = x.record()
	assert.False(t, rec.Success)
	assert.Empty(t, x.accounts.cooldowns)
}

// checkResponseStream accepts no name and the known ones, and refuses an
// unknown name and any name on a WebSocket turn, as the plugin's failure.
func TestCheckResponseStream(t *testing.T) {
	for name, tc := range map[string]struct {
		format string
		turn   bool
		ok     bool
	}{
		"none":              {"", false, true},
		"known":             {awseventstream.FormatChunk, false, true},
		"unknown":           {"x", false, false},
		"old name":          {"aws-eventstream", false, false},
		"none on a turn":    {"", true, true},
		"known on a turn":   {awseventstream.FormatChunk, true, false},
		"unknown on a turn": {"x", true, false},
	} {
		e := (&execution{turn: tc.turn}).checkResponseStream(tc.format)
		if tc.ok {
			assert.Nil(t, e, name)
			continue
		}
		require.NotNil(t, e, name)
		assert.Equal(t, errTypePluginUnavailable, e.RecordType, name)
	}
}

// ---------------------------------------------------------------- response stream

// A Bedrock-style relay - a body without "stream", response_stream set - on
// a streaming client request is relayed as SSE: whether the request streams
// is the client's request, not the plugin's body. The answer is metered by
// the platform's ordinary usage rules.
func TestEventStreamIsRelayedAsSSEAndBilled(t *testing.T) {
	x := newExtEnv(t)
	r := x.messages(body(testModel, true))
	require.Equal(t, 200, r.status, string(r.body))
	assert.True(t, isSSE(r.header.Get("Content-Type")), r.header.Get("Content-Type"))
	var want strings.Builder
	for _, ev := range extEvents() {
		fmt.Fprintf(&want, "event: %s\ndata: %s\n\n", gjson.Get(ev, "type").String(), ev)
	}
	assert.Equal(t, want.String(), string(r.body))
	got := x.up.last(t)
	assert.Equal(t, "/model/anthropic."+testModel+"-v1%3A0/invoke-with-response-stream", got.path)
	assert.False(t, gjson.GetBytes(got.body, "stream").Exists(), "the plugin's body has no stream flag: %s", got.body)
	rec := x.record()
	assert.True(t, rec.Success)
	assert.True(t, rec.Stream)
	assert.Equal(t, core.UsageTokens{Input: extInput, Output: extOutput}, rec.Tokens)
	assert.NotZero(t, rec.FirstTokenMs)
	assert.True(t, rec.Billable)
}

// A client speaking another protocol gets the converted stream: conversion
// sees ordinary Anthropic SSE.
func TestEventStreamOnAConvertedRoute(t *testing.T) {
	x := newExtEnv(t, openAIClients)
	path, input := openAIConversionInput("openai.chat", true)
	r := x.do(path, input, map[string]string{"authorization": "Bearer " + testKey})
	require.Equal(t, 200, r.status, string(r.body))
	assert.True(t, isSSE(r.header.Get("Content-Type")))
	assert.Contains(t, string(r.body), `"content":"hello"`)
	assert.Equal(t, 1, strings.Count(string(r.body), "data: [DONE]"), string(r.body))
	assert.True(t, strings.HasSuffix(x.up.last(t).path, "/invoke-with-response-stream"))
	rec := x.record()
	assert.True(t, rec.Success)
	assert.Equal(t, "openai.chat", rec.Protocol)
	assert.Equal(t, "anthropic.messages", rec.UpstreamProtocol)
	assert.Equal(t, core.UsageTokens{Input: extInput, Output: extOutput}, rec.Tokens)
}

// An exception frame in mid-stream reaches the client as an error event and
// is recorded as an upstream stream error.
func TestEventStreamExceptionBecomesAnErrorEvent(t *testing.T) {
	x := newExtEnv(t)
	x.up.exception = true
	r := x.messages(body(testModel, true))
	require.Equal(t, 200, r.status, string(r.body))
	assert.Contains(t, string(r.body), "event: message_start\n")
	assert.True(t, strings.HasSuffix(string(r.body),
		"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"modelStreamErrorException\",\"message\":\"the model stream broke\"}}\n\n"), string(r.body))
	rec := x.record()
	assert.False(t, rec.Success)
	assert.Contains(t, rec.ErrorMessage, "the model stream broke")
	assert.Equal(t, int64(extInput), rec.Tokens.Input)
}

// Answers that are not in the named framing are left alone: a JSON answer
// to a non-streaming request, and plain SSE.
func TestResponseStreamOnlyAdaptsItsOwnContentType(t *testing.T) {
	x := newExtEnv(t)
	x.plat.amend = func(_ *pluginv1.ExecuteRequest, out *pluginv1.UpstreamRequest) {
		out.ResponseStream = awseventstream.FormatChunk // also on the non-streaming request
	}
	r := x.messages(body(testModel, false))
	require.Equal(t, 200, r.status, string(r.body))
	assert.Equal(t, "hello", r.json().Get("content.0.text").String())
	assert.Equal(t, core.UsageTokens{Input: extInput, Output: extOutput}, x.record().Tokens)

	x.up.plainSSE = true
	r = x.messages(body(testModel, true))
	require.Equal(t, 200, r.status, string(r.body))
	assert.True(t, strings.HasPrefix(string(r.body), "event: message_start\ndata: {"), string(r.body))
	assert.Equal(t, core.UsageTokens{Input: extInput, Output: extOutput}, x.record().Tokens)
}

func TestAdaptResponseStream(t *testing.T) {
	frames := extChunk(`{"type":"ping"}`)
	for name, tc := range map[string]struct {
		format, contentType string
		adapted             bool
	}{
		"announced":               {awseventstream.FormatChunk, "application/vnd.amazon.eventstream", true},
		"announced, with a param": {awseventstream.FormatChunk, "Application/VND.amazon.eventstream; charset=binary", true},
		"not announced":           {"", "application/vnd.amazon.eventstream", false},
		"announced, JSON answer":  {awseventstream.FormatChunk, "application/json", false},
		"announced, SSE answer":   {awseventstream.FormatChunk, "text/event-stream", false},
		"announced, no type":      {awseventstream.FormatChunk, "", false},
		"unknown format":          {"x", "application/vnd.amazon.eventstream", false},
	} {
		closed := false
		resp := &http.Response{Header: http.Header{"Content-Length": {"99"}}, ContentLength: 99,
			Body: closeSpy{bytes.NewReader(frames), &closed}}
		if tc.contentType != "" {
			resp.Header.Set("Content-Type", tc.contentType)
		}
		adaptResponseStream(resp, tc.format)
		got, err := io.ReadAll(resp.Body)
		require.NoError(t, err, name)
		require.NoError(t, resp.Body.Close(), name)
		assert.True(t, closed, "%s: closing reaches the upstream body", name)
		if !tc.adapted {
			assert.Equal(t, frames, got, name)
			assert.Equal(t, tc.contentType, resp.Header.Get("Content-Type"), name)
			assert.Equal(t, "99", resp.Header.Get("Content-Length"), name)
			continue
		}
		assert.Equal(t, "event: ping\ndata: {\"type\":\"ping\"}\n\n", string(got), name)
		assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"), name)
		assert.Empty(t, resp.Header.Get("Content-Length"), name)
		assert.EqualValues(t, -1, resp.ContentLength, name)
	}
}

type closeSpy struct {
	io.Reader
	closed *bool
}

func (c closeSpy) Close() error { *c.closed = true; return nil }
