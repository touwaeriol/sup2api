package volcengine

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

// startExecution runs the plugin on the fake host with every way out of the
// process closed: the egress tunnel and the default HTTP transport. The request path (Relay, RelayAndWait, Fetch) must
// not need any of them.
func startExecution(t *testing.T, configure func(*pluginsdktest.FakeHost)) *pluginsdktest.Harness {
	t.Helper()
	denyDial := func(context.Context, string, string) (net.Conn, error) {
		t.Error("the request path dialled out")
		return nil, errors.New("dialling is forbidden on the request path")
	}
	fh := pluginsdktest.NewFakeHost()
	if configure != nil {
		configure(fh)
	}
	fh.DialFunc = denyDial
	p := New()
	p.now = func() time.Time { return time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC) }
	defaultTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("the request path used the default HTTP transport")
		return nil, errors.New("the default HTTP transport is forbidden on the request path")
	})
	t.Cleanup(func() { http.DefaultTransport = defaultTransport })
	h := pluginsdktest.Start(t, p, pluginsdktest.Options{
		Host: fh, Manifest: manifestFile(t), SDK: []pluginsdk.Option{pluginsdk.WithInfo("volcengine", "0.13.0")},
	})
	t.Cleanup(func() {
		if dials := fh.Dials(); len(dials) != 0 {
			t.Errorf("the request path used the egress tunnel: %v", dials)
		}
	})
	return h
}

// manifestFile is the plugin's manifest.json, which the fake host needs to
// know the hosts a fetch may reach (externalServices and the account's
// upstreamFields) as the core does.
func manifestFile(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../../manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const (
	videoModel  = "doubao-seedance-2-0-260128"
	submittedID = "cgt-20261118-abcde"
)

// executeCase is one endpoint class: the request the host hands to Execute
// and the exchange a conforming plugin answers it with.
type executeCase struct {
	name string
	in   *pluginv1.ExecuteRequest
	// The relay: method, URL, credential header, form.
	method, url, authName, authValue string
	wait                             bool
	// task: the report is the task (else host_usage).
	task bool
}

// keptAboveOperator is the reason of every authoritative header that is
// neither the credential nor signed.
const keptAboveOperator = "written above operator settings before forwarding v2; behaviour kept"

// authoritativeHeaders is the plugin's authoritative header set, name ->
// reason, for the conformance suite: every request header this plugin
// writes is authoritative, none is a default.
var authoritativeHeaders = map[string]string{
	"authorization":     "credential (Bearer key)",
	"x-api-key":         "credential (official Anthropic Messages surface)",
	"content-type":      keptAboveOperator,
	"user-agent":        keptAboveOperator + " (forwarded client header)",
	"anthropic-version": keptAboveOperator + " (forwarded, or the default when the client sent none)",
	"anthropic-beta":    keptAboveOperator + " (forwarded client header)",
}

// executeCases lists every endpoint class the plugin serves, on both account
// types where they differ. The conformance suite
// (sdk/pluginsdk/pluginsdktest/conformance) takes the same inputs.
func executeCases() []executeCase {
	const bearer = "Bearer 11111111-2222-3333-4444-555555555555"
	official, relay := account(testKey, ""), relayAccount("/v1", "/doubao/api/v3")
	in := func(acc *pluginv1.Account, protocol, model string, stream bool, body string) *pluginv1.ExecuteRequest {
		// What the core hands over: the manifest's passHeaders of the entry
		// the protocol is routed to.
		inbound := map[string]string{"user-agent": "client/1.0"}
		if protocol == ProtocolMessages {
			inbound["anthropic-beta"] = "beta-1"
		}
		return &pluginv1.ExecuteRequest{
			Meta:           &pluginv1.RequestMeta{Protocol: protocol, Model: model, Stream: stream, RequestId: "req-1"},
			Account:        acc,
			Body:           []byte(body),
			InboundHeaders: inbound,
		}
	}
	submit := in(official, ProtocolVideoSubmit, videoModel, false,
		`{"model":"`+videoModel+`","content":[{"type":"text","text":"a cat yawning"}],"resolution":"720p","ratio":"16:9","duration":5}`)
	submit.ExtractUsage, submit.TaskSubmission = true, true
	return []executeCase{
		{name: "chat stream", in: in(official, ProtocolChat, "doubao-seed-1-6-250615", true, string(chatBody("doubao-seed-1-6-250615", true))),
			method: "POST", url: DefaultBaseURL + "/api/v3/chat/completions", authName: "authorization", authValue: bearer},
		{name: "chat", in: in(relay, ProtocolChat, "doubao-seed-1-6-250615", false, string(chatBody("doubao-seed-1-6-250615", false))),
			method: "POST", url: "https://relay.test/v1/chat/completions", authName: "authorization", authValue: bearer},
		{name: "responses", in: in(official, ProtocolResponses, "doubao-seed-1-6-250615", true, `{"model":"doubao-seed-1-6-250615","input":"hi","stream":true}`),
			method: "POST", url: DefaultBaseURL + "/api/v3/responses", authName: "authorization", authValue: bearer},
		{name: "embeddings", in: in(official, ProtocolEmbeddings, "doubao-embedding-text-240715", false, `{"model":"doubao-embedding-text-240715","input":["a"]}`),
			method: "POST", url: DefaultBaseURL + "/api/v3/embeddings", authName: "authorization", authValue: bearer},
		{name: "messages official", in: in(official, ProtocolMessages, "doubao-seed-1-6-250615", true, `{"model":"doubao-seed-1-6-250615","max_tokens":8,"messages":[],"stream":true}`),
			method: "POST", url: DefaultBaseURL + "/api/compatible/v1/messages", authName: "x-api-key", authValue: "11111111-2222-3333-4444-555555555555"},
		{name: "messages relay", in: in(relay, ProtocolMessages, "doubao-seed-1-6-250615", false, `{"model":"doubao-seed-1-6-250615","max_tokens":8,"messages":[]}`),
			method: "POST", url: "https://relay.test/v1/messages", authName: "authorization", authValue: bearer},
		{name: "images", in: in(official, ProtocolImages, "doubao-seedream-4-0-250828", false, `{"model":"doubao-seedream-4-0-250828","prompt":"a fox"}`),
			method: "POST", url: DefaultBaseURL + "/api/v3/images/generations", authName: "authorization", authValue: bearer},
		{name: "video submit", in: submit,
			method: "POST", url: DefaultBaseURL + "/api/v3/contents/generations/tasks", authName: "authorization", authValue: bearer, wait: true, task: true},
	}
}

// submitted answers a waited relay the way the host does for an accepted
// video submission: the answer and the declared request fields.
func submitted(body string) func(context.Context, *pluginv1.UpstreamRequest) (*pluginv1.RelayUpstreamResponse, error) {
	return func(context.Context, *pluginv1.UpstreamRequest) (*pluginv1.RelayUpstreamResponse, error) {
		out := pluginsdktest.Observed(200, []byte(body))
		out.Observation.Fields = requested(Res720, Ratio169, 5)
		return out, nil
	}
}

func execute(t *testing.T, h *pluginsdktest.Harness, in *pluginv1.ExecuteRequest) *pluginsdktest.Execution {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return h.Execute(ctx, in)
}

// TestExecuteEndpointClasses: every class relays exactly once, in the form its
// flags call for, and answers the report that form owes: host_usage after a
// relay that did not wait, the task (with its estimate) after the submission.
// Every header is authoritative and listed in authoritativeHeaders; the body
// is the one handed in, but for the chat stream's include_usage.
func TestExecuteEndpointClasses(t *testing.T) {
	for _, tc := range executeCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := startExecution(t, func(fh *pluginsdktest.FakeHost) { fh.RelayFunc = submitted(`{"id":"` + submittedID + `"}`) })
			ex := execute(t, h, tc.in)
			relay := ex.Relayed(t)
			r := relay.Request
			if relay.Wait != tc.wait || r.GetMethod() != tc.method || r.GetUrl() != tc.url {
				t.Fatalf("relay = wait %v %s %s", relay.Wait, r.GetMethod(), r.GetUrl())
			}
			hd := r.GetHeaders()
			if hd[tc.authName] != tc.authValue || hd["user-agent"] != "client/1.0" || hd["content-type"] != "application/json" || len(r.GetDefaultHeaders()) != 0 {
				t.Fatalf("headers = %v / default %v", hd, r.GetDefaultHeaders())
			}
			for name := range hd {
				if authoritativeHeaders[name] == "" {
					t.Fatalf("authoritative header %q has no stated reason", name)
				}
			}
			if anthropic := tc.in.GetMeta().GetProtocol() == ProtocolMessages; (hd["anthropic-beta"] == "beta-1") != anthropic || (hd["anthropic-version"] != "") != anthropic {
				t.Fatalf("anthropic headers on %s: %v", tc.name, hd)
			}
			if r.GetUpstreamModel() != pluginsdk.RequestModel(tc.in) {
				t.Fatalf("upstream model = %q", r.GetUpstreamModel())
			}
			want, err := New().BuildUpstream(context.Background(), tc.in)
			if err != nil || !proto.Equal(r, want) {
				t.Fatalf("relayed %v, built %v (%v)", r, want, err)
			}
			if !tc.task {
				if !proto.Equal(ex.Response, pluginsdk.ReportHostUsage()) {
					t.Fatalf("report = %v, want host_usage", ex.Response)
				}
				return
			}
			task := ex.Response.GetTask()
			est := estimateVideo(videoModel, readVideoSpec(requested(Res720, Ratio169, 5)))
			if task.GetUpstreamRefId() != submittedID || task.GetUsage().GetReserve().GetRefId() != submittedID ||
				task.GetUsage().GetReserve().GetTokens().GetOutputTokens() != est.Tokens ||
				task.GetUsage().GetTokens().GetOutputTokens() != est.Tokens || task.GetNextCheckAfterSec() != firstCheckSec {
				t.Fatalf("task = %v, want %s with the estimate %d", task, submittedID, est.Tokens)
			}
		})
	}
}

// TestExecuteIsDeterministicAndRebuildsPerAttempt: the same input builds the
// same request, and a later attempt on another account is built from scratch
// for that account rather than reusing the first one.
func TestExecuteIsDeterministicAndRebuildsPerAttempt(t *testing.T) {
	h := startExecution(t, func(fh *pluginsdktest.FakeHost) { fh.RelayFunc = submitted(`{"id":"` + submittedID + `"}`) })
	for _, tc := range executeCases() {
		first := execute(t, h, tc.in).Relayed(t).Request
		if again := execute(t, h, tc.in).Relayed(t).Request; !proto.Equal(first, again) {
			t.Fatalf("%s: the same input built %v then %v", tc.name, first, again)
		}
		retry := proto.Clone(tc.in).(*pluginv1.ExecuteRequest)
		retry.Attempt = 1
		retry.Account = account(`{"api_key":"other-key-123456"}`, `{"base_url":"`+BytePlusBaseURL+`"}`)
		r := execute(t, h, retry).Relayed(t).Request
		if r.GetHeaders()[tc.authName] == tc.authValue || !strings.HasPrefix(r.GetUrl(), BytePlusBaseURL+"/") {
			t.Fatalf("%s: attempt 1 on another account reused attempt 0: %v", tc.name, r)
		}
	}
}

// TestExecuteVideoSubmissionOutcomes: the submission's report comes only from
// the observation; nothing falls back to host_usage once a task may exist.
func TestExecuteVideoSubmissionOutcomes(t *testing.T) {
	var submit *pluginv1.ExecuteRequest
	for _, tc := range executeCases() {
		if tc.task {
			submit = tc.in
		}
	}
	for _, tc := range []struct {
		name     string
		relay    func(context.Context, *pluginv1.UpstreamRequest) (*pluginv1.RelayUpstreamResponse, error)
		wantCode codes.Code
		check    func(*testing.T, *pluginv1.ExecuteResponse)
	}{
		{name: "wrapped id", relay: submitted(`{"data":{"id":"` + submittedID + `"}}`), check: func(t *testing.T, r *pluginv1.ExecuteResponse) {
			if r.GetTask().GetUpstreamRefId() != submittedID {
				t.Fatalf("task = %v", r.GetTask())
			}
		}},
		{name: "no task id", relay: submitted(`{}`), wantCode: codes.DataLoss},
		{name: "upstream refused", relay: func(context.Context, *pluginv1.UpstreamRequest) (*pluginv1.RelayUpstreamResponse, error) {
			return pluginsdktest.UpstreamFailed(429, []byte(`{"error":{"code":"ServerOverloaded","message":"busy"}}`), ""), nil
		}, check: func(t *testing.T, r *pluginv1.ExecuteResponse) {
			if r.GetReport() != nil || r.GetClassification().GetAction() != pluginv1.ClassifyErrorResponse_ACTION_FAILOVER {
				t.Fatalf("report = %v, want only a failover classification", r)
			}
		}},
		{name: "relay failed", relay: func(context.Context, *pluginv1.UpstreamRequest) (*pluginv1.RelayUpstreamResponse, error) {
			return nil, status.Error(codes.Unavailable, "host gave up")
		}, wantCode: codes.Unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := startExecution(t, func(fh *pluginsdktest.FakeHost) { fh.RelayFunc = tc.relay })
			ex := execute(t, h, submit)
			// One relay, never a second: a failed exchange is reported, not retried.
			if status.Code(ex.Err) != tc.wantCode || len(ex.Relays()) != 1 {
				t.Fatalf("err = %v, relays %d, want %s after exactly one relay", ex.Err, len(ex.Relays()), tc.wantCode)
			}
			if tc.check != nil {
				tc.check(t, ex.Response)
			}
		})
	}
}

// TestExecuteRelaysNothingItCannotBuild: a request the plugin refuses is never
// handed to the host, so the host moves on to another account with nothing
// sent.
func TestExecuteRelaysNothingItCannotBuild(t *testing.T) {
	h := startExecution(t, nil)
	for _, in := range []*pluginv1.ExecuteRequest{
		{Meta: &pluginv1.RequestMeta{Protocol: "anthropic.count_tokens", Model: "m"}, Account: relayAccount("/v1", ""), Body: modelBody("m")},
		{Meta: &pluginv1.RequestMeta{Protocol: ProtocolChat, Model: "m"}, Account: account(`{}`, ""), Body: modelBody("m")},
	} {
		if ex := execute(t, h, in); ex.Err == nil || len(ex.Relays())+len(ex.RelayErrors()) != 0 {
			t.Fatalf("%s: err = %v, relays %v", in.GetMeta().GetProtocol(), ex.Err, ex.Relays())
		}
	}
}
