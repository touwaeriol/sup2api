package volcengine

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey/apikeytest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/classify"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

func start(t *testing.T) *pluginsdktest.Harness {
	t.Helper()
	return pluginsdktest.Start(t, New(), pluginsdktest.Options{SDK: []pluginsdk.Option{pluginsdk.WithInfo("volcengine", "0.10.1")}})
}

func account(creds, settings string) *pluginv1.Account {
	return &pluginv1.Account{Id: 7, Platform: PlatformID, Type: AccountTypeAPIKey, CredentialsJson: creds, SettingsJson: settings}
}

const testKey = `{"api_key":"11111111-2222-3333-4444-555555555555"}`

const (
	transientCooldown        = classify.DefaultTransport
	defaultRateLimitCooldown = classify.DefaultRateLimit
	maxCooldown              = classify.MaxCooldown
)

// TestCapabilities: what the SDK infers from the code alone - the platform
// (Execute) and the task monitor - plus http.routes.v1 for the asset library.
// platform.tasks.v1 comes only from the manifest (TestManifestServes).
func TestCapabilities(t *testing.T) {
	h := start(t)
	if got := strings.Join(h.Info.GetCapabilities(), ","); got != "platform.adapter.v1,platform.monitor.v1,http.routes.v1" {
		t.Fatalf("capabilities = %s", got)
	}
}

func TestValidateCredentials(t *testing.T) {
	h := start(t)
	ctx := context.Background()

	// base_url left empty: normalized to the official Chinese endpoint.
	r, err := h.Platform.ValidateCredentials(ctx, &pluginv1.ValidateCredentialsRequest{
		AccountType: AccountTypeAPIKey, CredentialsJson: `{"api_key":" ark-key-12345678 "}`, SettingsJson: `{"base_url":""}`,
	})
	if err != nil || len(r.GetErrors()) != 0 {
		t.Fatalf("valid: %v %v", r, err)
	}
	if r.GetNormalizedCredentialsJson() != `{"api_key":"ark-key-12345678"}` ||
		r.GetNormalizedSettingsJson() != `{"base_url":"`+DefaultBaseURL+`"}` {
		t.Fatalf("normalized = %s %s", r.GetNormalizedCredentialsJson(), r.GetNormalizedSettingsJson())
	}

	// The base URL is saved as written, only trimmed and without trailing
	// slashes: a pasted Ark SDK base URL keeps its /api/v3 (requests leave
	// it out, TestLayoutFollowsTheAccountType; the core compares it with
	// guardedSettings without it, CONTRACTS §21.3), BytePlus too.
	for in, want := range map[string]string{
		DefaultBaseURL + "/api/v3":  DefaultBaseURL + "/api/v3",
		DefaultBaseURL + "/api/v3/": DefaultBaseURL + "/api/v3",
		DefaultBaseURL + "/":        DefaultBaseURL,
		" " + DefaultBaseURL + " ":  DefaultBaseURL,
		BytePlusBaseURL + "/api/v3": BytePlusBaseURL + "/api/v3",
	} {
		r, err := h.Platform.ValidateCredentials(ctx, &pluginv1.ValidateCredentialsRequest{
			AccountType: AccountTypeAPIKey, CredentialsJson: testKey, SettingsJson: `{"base_url":"` + in + `"}`,
		})
		if err != nil || len(r.GetErrors()) != 0 {
			t.Fatalf("%q: %v %v", in, r, err)
		}
		if r.GetNormalizedSettingsJson() != `{"base_url":"`+want+`"}` {
			t.Fatalf("%q normalized to %s, want %s", in, r.GetNormalizedSettingsJson(), want)
		}
	}

	// Errors: missing key, relative base_url, wrong account type.
	r, _ = h.Platform.ValidateCredentials(ctx, &pluginv1.ValidateCredentialsRequest{AccountType: AccountTypeAPIKey, CredentialsJson: `{}`})
	if len(r.GetErrors()) != 1 || r.GetErrors()[0].GetField() != "api_key" {
		t.Fatalf("missing key: %v", r.GetErrors())
	}
	r, _ = h.Platform.ValidateCredentials(ctx, &pluginv1.ValidateCredentialsRequest{
		AccountType: AccountTypeAPIKey, CredentialsJson: testKey, SettingsJson: `{"base_url":"ark.cn-beijing.volces.com"}`,
	})
	if len(r.GetErrors()) != 1 || r.GetErrors()[0].GetField() != "base_url" {
		t.Fatalf("relative base_url: %v", r.GetErrors())
	}
	r, _ = h.Platform.ValidateCredentials(ctx, &pluginv1.ValidateCredentialsRequest{AccountType: "oauth", CredentialsJson: testKey})
	if len(r.GetErrors()) != 1 || r.GetErrors()[0].GetField() != "account_type" {
		t.Fatalf("wrong type: %v", r.GetErrors())
	}
}

// chatBody is a chat completions request body naming model; stream adds the
// stream flag the way a client sends it.
func chatBody(model string, stream bool) []byte {
	b := `{"model":` + jsonStr(model) + `,"messages":[{"role":"user","content":"hi"}]`
	if stream {
		b += `,"stream":true`
	}
	return []byte(b + `}`)
}

func TestBuildUpstream(t *testing.T) {
	p := New()
	ctx := context.Background()
	build := func(meta *pluginv1.RequestMeta, acc *pluginv1.Account, inbound map[string]string) (*pluginv1.UpstreamRequest, error) {
		return p.BuildUpstream(ctx, &pluginv1.ExecuteRequest{
			Meta: meta, Account: acc, Body: chatBody(meta.GetModel(), meta.GetStream()), InboundHeaders: inbound,
		})
	}

	// Streaming chat on the default base URL: Ark's /api/v3 prefix, bearer
	// key, include_usage forced, the user agent (the one client header the
	// core hands over on this surface) forwarded.
	r, err := build(&pluginv1.RequestMeta{Protocol: ProtocolChat, Model: "doubao-seed-1-6-250615", Stream: true},
		account(testKey, ""),
		map[string]string{"user-agent": "OpenAI/Python 1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if r.GetMethod() != "POST" || r.GetUrl() != DefaultBaseURL+"/api/v3/chat/completions" {
		t.Fatalf("request = %s %s", r.GetMethod(), r.GetUrl())
	}
	// Every header is authoritative, as it was before forwarding v2.
	if want := map[string]string{"authorization": "Bearer 11111111-2222-3333-4444-555555555555",
		"content-type": "application/json", "user-agent": "OpenAI/Python 1.0"}; !maps.Equal(r.GetHeaders(), want) || len(r.GetDefaultHeaders()) != 0 {
		t.Fatalf("headers = %v / default %v, want all of %v authoritative", r.GetHeaders(), r.GetDefaultHeaders(), want)
	}
	if want := `{"model":"doubao-seed-1-6-250615","messages":[{"role":"user","content":"hi"}],"stream":true,"stream_options":{"include_usage":true}}`; string(r.GetBody()) != want ||
		r.GetUpstreamModel() != "doubao-seed-1-6-250615" {
		t.Fatalf("body = %s upstream = %s", r.GetBody(), r.GetUpstreamModel())
	}

	// Non-streaming chat: the body goes up as it came, byte for byte.
	r, err = build(&pluginv1.RequestMeta{Protocol: ProtocolChat, Model: "doubao-seed-1-6-250615"}, account(testKey, ""), nil)
	if err != nil || !bytes.Equal(r.GetBody(), chatBody("doubao-seed-1-6-250615", false)) {
		t.Fatalf("non-stream chat: %v %v", r, err)
	}

	// A "bot" model is an Ark application: it has its own chat path.
	r, err = build(&pluginv1.RequestMeta{Protocol: ProtocolChat, Model: "bot-20250101120000-abcde"}, account(testKey, ""), nil)
	if err != nil || r.GetUrl() != DefaultBaseURL+"/api/v3/bots/chat/completions" {
		t.Fatalf("bot model: %v %v", r, err)
	}
	// Models that merely start with "bot" as part of a word do not: the
	// prefix is the whole discriminator Ark offers, so document the edge.
	r, _ = build(&pluginv1.RequestMeta{Protocol: ProtocolChat, Model: "doubao-bot"}, account(testKey, ""), nil)
	if r.GetUrl() != DefaultBaseURL+"/api/v3/chat/completions" {
		t.Fatalf("model containing bot: %s", r.GetUrl())
	}

	// A custom base URL with a trailing slash and the /api/v3 the operator
	// pasted from the Ark SDK docs; responses never uses the bots path and
	// never gets the chat-only stream_options edit.
	//
	// mock-upstream speaks Ark's own paths while not being an Ark host, which
	// is exactly the case the derived layout gets wrong and api_prefix exists
	// for: without it this account would be taken for a standard relay and
	// asked for /v1/responses.
	acc := account(testKey, `{"base_url":"http://mock-upstream:8080/api/v3/","api_prefix":"/api/v3"}`)
	r, err = build(&pluginv1.RequestMeta{Protocol: ProtocolResponses, Model: "bot-1", Stream: true}, acc, nil)
	if err != nil || r.GetUrl() != "http://mock-upstream:8080/api/v3/responses" || !bytes.Equal(r.GetBody(), chatBody("bot-1", true)) {
		t.Fatalf("responses: %v %v", r, err)
	}

	// Embeddings on the BytePlus address.
	r, err = build(&pluginv1.RequestMeta{Protocol: ProtocolEmbeddings, Model: "doubao-embedding-text-240715"},
		account(testKey, `{"base_url":"`+BytePlusBaseURL+`"}`), nil)
	if err != nil || r.GetUrl() != BytePlusBaseURL+"/api/v3/embeddings" || !bytes.Equal(r.GetBody(), chatBody("doubao-embedding-text-240715", false)) {
		t.Fatalf("embeddings: %v %v", r, err)
	}

	// The body's model wins over meta.model, including for the bots path.
	r, err = p.BuildUpstream(ctx, &pluginv1.ExecuteRequest{
		Meta:    &pluginv1.RequestMeta{Protocol: ProtocolChat, Model: "doubao-seed-1-6-250615"},
		Account: account(testKey, ""), Body: chatBody("bot-7", false),
	})
	if err != nil || r.GetUpstreamModel() != "bot-7" || r.GetUrl() != DefaultBaseURL+"/api/v3/bots/chat/completions" {
		t.Fatalf("body model: %v %v", r, err)
	}

	// Converted request (the client spoke anthropic.messages): the path and
	// the include_usage edit follow meta.protocol.
	r, err = build(&pluginv1.RequestMeta{Protocol: ProtocolChat, ClientProtocol: "anthropic.messages", Model: "doubao-seed-1-6-250615", Stream: true}, account(testKey, ""), nil)
	if err != nil || r.GetUrl() != DefaultBaseURL+"/api/v3/chat/completions" || !gjson.GetBytes(r.GetBody(), "stream_options.include_usage").Bool() {
		t.Fatalf("converted: %v %v", r, err)
	}

	// Errors: missing key, unknown protocol, foreign account type.
	if _, err := build(&pluginv1.RequestMeta{Protocol: ProtocolChat}, account(`{}`, ""), nil); err == nil {
		t.Fatal("expected error for missing api key")
	}
	if _, err := build(&pluginv1.RequestMeta{Protocol: "gemini.generate"}, account(testKey, ""), nil); err == nil {
		t.Fatal("expected error for unknown protocol")
	}
	foreign := account(testKey, "")
	foreign.Type = "relay_key"
	if _, err := build(&pluginv1.RequestMeta{Protocol: ProtocolChat}, foreign, nil); err == nil {
		t.Fatal("expected error for a foreign account type")
	}
}

// TestIncludeUsageIsATargetedEdit: the one body edit touches its own path and
// nothing else - key order, spacing, numbers written as the client wrote
// them, unknown fields and even a duplicate key survive, which a
// decode-and-encode of the document would not keep.
func TestIncludeUsageIsATargetedEdit(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`{"stream":true, "model":"m","n":1.50,"x":{"b":2,"a":1},"x":3}`,
			`{"stream":true, "model":"m","n":1.50,"x":{"b":2,"a":1},"x":3,"stream_options":{"include_usage":true}}`},
		{`{"model":"m","stream_options":{"include_usage":false,"z":1},"stream":true}`,
			`{"model":"m","stream_options":{"include_usage":true,"z":1},"stream":true}`},
		{`{"model":"m","stream_options":{"z":1},"stream":true}`,
			`{"model":"m","stream_options":{"z":1,"include_usage":true},"stream":true}`},
	} {
		r, err := New().BuildUpstream(context.Background(), &pluginv1.ExecuteRequest{
			Meta: &pluginv1.RequestMeta{Protocol: ProtocolChat, Model: "m", Stream: true}, Account: account(testKey, ""), Body: []byte(tc.in),
		})
		if err != nil || string(r.GetBody()) != tc.want {
			t.Errorf("%s\n got %s (%v)\nwant %s", tc.in, r.GetBody(), err, tc.want)
		}
	}
}

// TestBuildUpstreamImages covers the volcengine platform's own endpoint: the
// client calls /api/v3/images/generations (or the /doubao/api/v3 alias), the
// upstream is the account's own prefix.
func TestBuildUpstreamImages(t *testing.T) {
	p := New()
	ctx := context.Background()
	imageBody := func(model string) []byte {
		return []byte(`{"model":` + jsonStr(model) + `,"prompt":"a red fox","size":"2K","stream":true}`)
	}
	build := func(meta *pluginv1.RequestMeta, acc *pluginv1.Account, inbound map[string]string) (*pluginv1.UpstreamRequest, error) {
		return p.BuildUpstream(ctx, &pluginv1.ExecuteRequest{
			Meta: meta, Account: acc, Body: imageBody(meta.GetModel()), InboundHeaders: inbound,
		})
	}

	r, err := build(&pluginv1.RequestMeta{Protocol: ProtocolImages, Model: "doubao-seedream-4-0-250828"},
		account(testKey, ""), map[string]string{"user-agent": "Ark/Python 1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if r.GetMethod() != "POST" || r.GetUrl() != DefaultBaseURL+"/api/v3/images/generations" ||
		r.GetHeaders()["authorization"] != "Bearer 11111111-2222-3333-4444-555555555555" ||
		r.GetHeaders()["content-type"] != "application/json" || r.GetHeaders()["user-agent"] != "Ark/Python 1.0" {
		t.Fatalf("images = %s %s %v", r.GetMethod(), r.GetUrl(), r.GetHeaders())
	}
	if r.GetUpstreamModel() != "doubao-seedream-4-0-250828" {
		t.Fatalf("upstream model = %q", r.GetUpstreamModel())
	}

	// Image-to-image shares the path: Ark decides from the body, not the URL,
	// so there is no second branch to take.
	r, err = build(&pluginv1.RequestMeta{Protocol: ProtocolImages, Model: "doubao-seedream-4-5-251128"},
		account(testKey, `{"base_url":"`+BytePlusBaseURL+`/api/v3/"}`), nil)
	if err != nil || r.GetUrl() != BytePlusBaseURL+"/api/v3/images/generations" {
		t.Fatalf("byteplus images: %v %v", r, err)
	}

	// stream_options.include_usage is a chat-completions field: never added
	// to an image request, even when the client asked for a stream.
	r, err = build(&pluginv1.RequestMeta{Protocol: ProtocolImages, Model: "doubao-seedream-4-0-250828", Stream: true},
		account(testKey, ""), nil)
	if err != nil || !bytes.Equal(r.GetBody(), imageBody("doubao-seedream-4-0-250828")) {
		t.Fatalf("streaming images: %s %v", r.GetBody(), err)
	}

	// A model starting with "bot" does not divert the image path.
	r, _ = build(&pluginv1.RequestMeta{Protocol: ProtocolImages, Model: "bot-1"}, account(testKey, ""), nil)
	if r.GetUrl() != DefaultBaseURL+"/api/v3/images/generations" {
		t.Fatalf("bot image model: %s", r.GetUrl())
	}
}

func TestBuildTestRequest(t *testing.T) {
	h := start(t)
	ctx := context.Background()
	r, err := h.Platform.BuildTestRequest(ctx, &pluginv1.BuildTestRequestRequest{Account: account(testKey, "")})
	if err != nil {
		t.Fatal(err)
	}
	if r.GetUrl() != DefaultBaseURL+"/api/v3/chat/completions" || r.GetHeaders()["authorization"] != "Bearer 11111111-2222-3333-4444-555555555555" {
		t.Fatalf("resp = %v", r)
	}
	var body struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		Messages  []any  `json:"messages"`
	}
	if err := json.Unmarshal([]byte(r.GetBodyJson()), &body); err != nil ||
		body.Model != DefaultTestModel || body.MaxTokens != testMaxTokens || len(body.Messages) != 1 {
		t.Fatalf("body = %s", r.GetBodyJson())
	}
	// The host is told which model was really used and how to read the usage
	// (account/testreq.go testUsage), so the console shows the token counts.
	if r.GetModel() != DefaultTestModel || r.GetUsageProtocol() != ProtocolChat {
		t.Fatalf("model = %q usage protocol = %q", r.GetModel(), r.GetUsageProtocol())
	}

	// An explicit bot id is tested against the bots path.
	r, err = h.Platform.BuildTestRequest(ctx, &pluginv1.BuildTestRequestRequest{
		Account: account(testKey, `{"base_url":"`+BytePlusBaseURL+`"}`), Model: " bot-42 ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.GetUrl() != BytePlusBaseURL+"/api/v3/bots/chat/completions" || r.GetModel() != "bot-42" {
		t.Fatalf("bot test = %s %s", r.GetUrl(), r.GetModel())
	}
}

// TestBuildModelsRequestUnimplemented pins the decision not to implement
// pluginsdk.ModelLister: Ark documents no API-key-authenticated list-models
// endpoint, so the console's "fetch from upstream" reports 501 rather than
// hitting a guessed URL (CONTRACTS §19).
func TestBuildModelsRequestUnimplemented(t *testing.T) {
	h := start(t)
	_, err := h.Platform.BuildModelsRequest(context.Background(), &pluginv1.BuildModelsRequestRequest{Account: account(testKey, "")})
	if err == nil {
		t.Fatal("expected UNIMPLEMENTED")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unimplemented") {
		t.Fatalf("err = %v", err)
	}
}

func TestClassifyError(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	p := New()
	p.now = func() time.Time { return now }
	cls := func(status int32, headers map[string]string, body, transport string) *pluginv1.ClassifyErrorResponse {
		r, err := p.ClassifyError(context.Background(), &pluginv1.ClassifyErrorRequest{
			Status: status, Headers: headers, BodyPrefix: []byte(body), TransportError: transport,
		})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	const (
		ret      = pluginv1.ClassifyErrorResponse_ACTION_RETURN_TO_CLIENT
		failover = pluginv1.ClassifyErrorResponse_ACTION_FAILOVER
		none     = pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_UNSPECIFIED
		cool     = pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_COOLDOWN
		disable  = pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_DISABLE
	)
	check := func(name string, r *pluginv1.ClassifyErrorResponse, action pluginv1.ClassifyErrorResponse_Action,
		effect pluginv1.ClassifyErrorResponse_AccountEffect, cooldown time.Duration, errType string) {
		t.Helper()
		if r.GetAction() != action || r.GetAccountEffect() != effect || r.GetClientErrorType() != errType {
			t.Errorf("%s: got %v/%v/%s, want %v/%v/%s", name, r.GetAction(), r.GetAccountEffect(), r.GetClientErrorType(), action, effect, errType)
		}
		if effect == cool {
			if got := time.Unix(r.GetCooldownUntilUnix(), 0).Sub(now); got != cooldown {
				t.Errorf("%s: cooldown %v, want %v", name, got, cooldown)
			}
		}
		if action == failover && r.GetReason() == "" {
			t.Errorf("%s: empty reason", name)
		}
	}
	arkErr := func(code, msg string) string {
		return `{"error":{"code":"` + code + `","message":"` + msg + `","param":"","type":"x"}}`
	}

	// Client mistakes come back untouched.
	r := cls(400, nil, arkErr("InvalidParameter", "invalid parameter model"), "")
	check("400", r, ret, none, 0, "invalid_request_error")
	if r.GetClientMessage() != "invalid parameter model" {
		t.Errorf("client message = %q", r.GetClientMessage())
	}
	check("404 endpoint", cls(404, nil, arkErr("InvalidEndpoint.NotFound", "endpoint not found"), ""), ret, none, 0, "invalid_request_error")
	check("400 no body", cls(400, nil, "", ""), ret, none, 0, "invalid_request_error")

	// Ark reports the client's error type as an HTTP phrase ("Unauthorized"),
	// which OpenAI SDKs do not know: the status decides the type instead.
	check("401", cls(401, nil, arkErr("AuthenticationError", "The API key doesn't exist"), ""), failover, disable, 0, "authentication_error")
	check("403 denied", cls(403, nil, arkErr("AccessDenied", "no permission"), ""), failover, disable, 0, "permission_error")
	check("403 overdue", cls(403, nil, arkErr("AccountOverdueError", "account is in arrears"), ""), failover, disable, 0, "permission_error")
	if got := cls(403, nil, arkErr("AccountOverdueError", "account is in arrears"), "").GetReason(); !strings.Contains(got, "AccountOverdueError") {
		t.Errorf("reason = %q, want the Ark code in it", got)
	}

	// The model is simply not enabled on this account: try the next one, but
	// leave the account alone.
	check("404 model not open", cls(404, nil, arkErr("ModelNotOpen", "model not open"), ""), failover, none, 0, "invalid_request_error")
	check("400 closed endpoint", cls(400, nil, arkErr("InvalidEndpoint.ClosedEndpoint", "endpoint closed"), ""), failover, none, 0, "invalid_request_error")
	check("404 invalid endpoint or model", cls(404, nil, arkErr("InvalidEndpointOrModel.NotFound", "not found"), ""), failover, none, 0, "invalid_request_error")

	// 429s.
	check("429 quota", cls(429, nil, arkErr("QuotaExceeded", "free trial used up"), ""), failover, disable, 0, "rate_limit_error")
	check("429 overloaded", cls(429, nil, arkErr("ServerOverloaded", "busy"), ""), failover, cool, transientCooldown, "rate_limit_error")
	check("429 loading", cls(429, nil, arkErr("ModelLoadingError", "loading"), ""), failover, cool, transientCooldown, "rate_limit_error")
	check("429 rpm", cls(429, nil, arkErr("RateLimitExceeded.EndpointRPMExceeded", "rpm"), ""), failover, cool, defaultRateLimitCooldown, "rate_limit_error")
	check("429 account tpm", cls(429, nil, arkErr("ModelAccountTpmRateLimitExceeded", "tpm"), ""), failover, cool, defaultRateLimitCooldown, "rate_limit_error")
	check("429 retry-after", cls(429, map[string]string{"retry-after": "2"}, "", ""), failover, cool, 2*time.Second, "rate_limit_error")
	check("429 retry-after-ms", cls(429, map[string]string{"retry-after-ms": "2500", "retry-after": "9"}, "", ""), failover, cool, 2*time.Second, "rate_limit_error")
	check("429 retry-after date", cls(429, map[string]string{"Retry-After": now.Add(90 * time.Second).Format(http.TimeFormat)}, "", ""), failover, cool, 90*time.Second, "rate_limit_error")
	check("429 clamp", cls(429, map[string]string{"retry-after": "99999999"}, "", ""), failover, cool, maxCooldown, "rate_limit_error")

	// Server side and transport.
	check("500", cls(500, nil, arkErr("InternalServiceError", "boom"), ""), failover, cool, transientCooldown, "server_error")
	check("503", cls(503, nil, "", ""), failover, cool, transientCooldown, "server_error")
	check("408", cls(408, nil, "", ""), failover, cool, transientCooldown, "invalid_request_error")
	r = cls(0, nil, "", "dial tcp: connection refused")
	check("transport", r, failover, cool, transientCooldown, "server_error")
	if r.GetClientStatus() != http.StatusBadGateway || r.GetClientMessage() != "upstream connection failed" {
		t.Errorf("transport = %d %q", r.GetClientStatus(), r.GetClientMessage())
	}

	// Compatible relays that answer {"error": "message"}.
	if r := cls(400, nil, `{"error":"bad things"}`, ""); r.GetClientMessage() != "bad things" || r.GetClientErrorType() != "invalid_request_error" {
		t.Errorf("string error body: %v", r)
	}
	// A non-JSON body (an HTML gateway page) must not panic or misclassify.
	check("502 html", cls(502, nil, "<html>502 Bad Gateway</html>", ""), failover, cool, transientCooldown, "server_error")

	// Image generation (volcengine.images) adds no error code of its own that
	// needs a different decision: content moderation is a 400 about this
	// request, so it goes back to the client instead of being replayed —
	// and paid for — on every other account of the group. The Input* /
	// Output* prefixes must not be mistaken for the account-resource codes
	// that do fail over (ModelNotOpen, InvalidEndpointOrModel*).
	for _, code := range []string{
		"InputTextSensitiveContentDetected",
		"InputImageSensitiveContentDetected.PrivacyInformation",
		"OutputImageSensitiveContentDetected",
		"InputImageUrlCannotAccess",
	} {
		check("400 "+code, cls(400, nil, arkErr(code, "sensitive content"), ""), ret, none, 0, "invalid_request_error")
	}
	// The shared decisions still apply on the image endpoint.
	check("images 404 model not open", cls(404, nil, arkErr("ModelNotOpen", "model not open"), ""), failover, none, 0, "invalid_request_error")
	check("images 429 overloaded", cls(429, nil, arkErr("ServerOverloaded", "busy"), ""), failover, cool, transientCooldown, "rate_limit_error")
}

// TestManifestVersions: the versions the core leaves out of a guarded
// base_url before comparing it (manifest guardedSettings[].versions) are the
// ones requests leave out (Spec.Versions), on each type.
func TestManifestVersions(t *testing.T) {
	var m manifest.Manifest
	if err := json.Unmarshal(manifestFile(t), &m); err != nil {
		t.Fatal(err)
	}
	for _, at := range m.AccountTypes {
		sp, err := specFor(at.ID)
		if err != nil {
			t.Fatal(err)
		}
		apikeytest.GuardedVersions(t, at, sp)
	}
}
