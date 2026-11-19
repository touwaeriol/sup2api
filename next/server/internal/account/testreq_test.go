package account

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tidwall/gjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// anthropicUsage mirrors the JSON usage rules of the built-in anthropic
// platform (non-streaming responses).
var anthropicUsage = manifest.UsageRules{Semantics: "exclusive",
	JSON: &manifest.UsageMap{Map: map[string]string{
		manifest.UsageModel:               "model",
		manifest.UsageInputTokens:         "usage.input_tokens",
		manifest.UsageOutputTokens:        "usage.output_tokens",
		manifest.UsageCacheReadTokens:     "usage.cache_read_input_tokens",
		manifest.UsageCacheCreationTokens: "usage.cache_creation_input_tokens",
		manifest.UsageCacheCreation1h:     "usage.cache_creation.ephemeral_1h_input_tokens",
	}}}

// testResponse is the body of a successful fake test call.
const testResponse = `{"model":"claude-x","usage":{"input_tokens":5,"output_tokens":7,` +
	`"cache_read_input_tokens":2,"cache_creation_input_tokens":9,"cache_creation":{"ephemeral_1h_input_tokens":4}}}`

// setPlatformUsage installs usage rules on a platform of the fake generation.
func (e *env) setPlatformUsage(id string, rules manifest.UsageRules) {
	e.t.Helper()
	for i := range e.gen.plats {
		if e.gen.plats[i].Platform.ID == id {
			e.gen.plats[i].Platform.Usage = rules
			return
		}
	}
	e.t.Fatalf("platform %s is not in the fake generation", id)
}

// setTypeUsage overrides the usage rules of the anthropic/apikey type for one
// protocol (nil rules clear the override).
func (e *env) setTypeUsage(protocol string, rules *manifest.UsageRules) {
	e.t.Helper()
	ap := &e.gen.types[0].Type.Platforms[0]
	if rules == nil {
		ap.Usage = nil
		return
	}
	ap.Usage = map[string]manifest.UsageRules{protocol: *rules}
}

// mkAccount creates an anthropic/apikey account named after its key.
func (e *env) mkAccount(key string) int64 {
	e.t.Helper()
	code, out := e.do("POST", "/accounts", map[string]any{"name": key, "plugin_key": "anthropic", "type": "apikey",
		"credentials": map[string]any{"api_key": key}})
	if code != 201 {
		e.t.Fatalf("create: %d %v", code, out)
	}
	return int64(out["data"].(map[string]any)["id"].(float64))
}

// testUpstream answers the fake test requests: the response body with usage
// for a good key, 401 for anything else and an oversized body on /long.
func testUpstream(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "sk-good-key-123" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"type":"authentication_error","message":"invalid x-api-key"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/long" {
			// 12 KiB of multi-byte text: more than the host reads.
			_, _ = w.Write([]byte(strings.Repeat("上游响应片段", 700)))
			return
		}
		_, _ = w.Write([]byte(testResponse))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestAccountTestResultDetails covers the visible test result: the model and
// upstream address reached, the response body, and the token usage read with
// the platform (or account type) usage rules.
func TestAccountTestResultDetails(t *testing.T) {
	e := setup(t)
	up := testUpstream(t)
	id := e.mkAccount("sk-good-key-123")

	// A Gemini style key in the query must not come back in the result.
	e.plat.testURL = up.URL + "/v1/messages?key=secret-value&alt=json"
	e.plat.usageProtocol = "anthropic.messages"
	e.setPlatformUsage("anthropic", anthropicUsage)

	code, raw := e.doRaw(e.uid, "POST", fmt.Sprintf("/accounts/%d/test", id), map[string]any{"model": "claude-x"})
	if code != 200 {
		t.Fatalf("test: %d %s", code, raw)
	}
	if bytes.Contains(raw, []byte("secret-value")) {
		t.Fatalf("query leaked into the result: %s", raw)
	}
	d := gjson.GetBytes(raw, "data")
	if !d.Get("ok").Bool() || d.Get("status").Int() != 200 || d.Get("message").String() != "" {
		t.Fatalf("result: %s", d)
	}
	if d.Get("model").String() != "claude-x" || d.Get("upstream").String() != up.URL+"/v1/messages" {
		t.Fatalf("model/upstream: %s", d)
	}
	if !strings.Contains(d.Get("body").String(), `"input_tokens":5`) {
		t.Fatalf("body: %s", d.Get("body"))
	}
	// cache_creation_tokens is the total cache write (5 minute + 1 hour).
	u := d.Get("usage")
	if u.Get("input_tokens").Int() != 5 || u.Get("output_tokens").Int() != 7 ||
		u.Get("cache_read_tokens").Int() != 2 || u.Get("cache_creation_tokens").Int() != 9 {
		t.Fatalf("usage: %s", u)
	}

	// The account type's per-protocol override wins over the platform rules.
	e.setTypeUsage("anthropic.messages", &manifest.UsageRules{JSON: &manifest.UsageMap{
		Map: map[string]string{manifest.UsageInputTokens: "usage.output_tokens"}}})
	_, raw = e.doRaw(e.uid, "POST", fmt.Sprintf("/accounts/%d/test", id), nil)
	u = gjson.GetBytes(raw, "data.usage")
	if u.Get("input_tokens").Int() != 7 || u.Get("output_tokens").Exists() || u.Get("cache_read_tokens").Exists() {
		t.Fatalf("type override: %s", u)
	}
	e.setTypeUsage("anthropic.messages", nil)

	// No model asked for: the model the plugin reports is what was tested.
	e.plat.testModel = "claude-haiku-4-5"
	_, raw = e.doRaw(e.uid, "POST", fmt.Sprintf("/accounts/%d/test", id), nil)
	if got := gjson.GetBytes(raw, "data.model").String(); got != "claude-haiku-4-5" {
		t.Fatalf("plugin model: %q", got)
	}
	e.plat.testModel = ""

	// No usage protocol, or one no platform declares: no usage, test still ok.
	for _, proto := range []string{"", "ghost.embed"} {
		e.plat.usageProtocol = proto
		_, raw = e.doRaw(e.uid, "POST", fmt.Sprintf("/accounts/%d/test", id), nil)
		d = gjson.GetBytes(raw, "data")
		if !d.Get("ok").Bool() || d.Get("usage").Exists() {
			t.Fatalf("usage_protocol %q: %s", proto, d)
		}
	}
	e.plat.usageProtocol = "anthropic.messages"

	// An oversized body is cut to 4 KiB and stays valid UTF-8; it is no
	// longer JSON, so no usage is reported.
	e.plat.testURL = up.URL + "/long"
	_, raw = e.doRaw(e.uid, "POST", fmt.Sprintf("/accounts/%d/test", id), nil)
	d = gjson.GetBytes(raw, "data")
	body := d.Get("body").String()
	if !d.Get("ok").Bool() || d.Get("usage").Exists() {
		t.Fatalf("long body result: %s", d)
	}
	if !utf8.ValidString(body) || !strings.HasSuffix(body, "…") || len(body) > maxTestBodyOut+len("…") ||
		len(body) < maxTestBodyOut-8 {
		t.Fatalf("truncation: %d bytes, valid=%v", len(body), utf8.ValidString(body))
	}
}

func TestAccountTestPreservesUpstreamErrorCode(t *testing.T) {
	t.Run("actual upstream 401", func(t *testing.T) {
		e := setup(t)
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"type":"authentication_error","code":"token_revoked","message":"token revoked"}}`))
		}))
		defer up.Close()
		e.plat.testURL = up.URL + "/v1/messages"
		id := e.mkAccount("sk-good-key-123")
		_, raw := e.doRaw(e.uid, "POST", "/accounts/"+itoa(id)+"/test", nil)
		d := gjson.GetBytes(raw, "data")
		if d.Get("ok").Bool() || d.Get("status").Int() != 401 || d.Get("error_code").String() != "token_revoked" || d.Get("error_source").String() != "response" {
			t.Fatalf("upstream diagnostic: %s", raw)
		}
		_, raw = e.doRaw(e.uid, "GET", "/accounts/"+itoa(id), nil)
		if gjson.GetBytes(raw, "data.last_test.status").Int() != 401 || gjson.GetBytes(raw, "data.last_test.error_code").String() != "token_revoked" {
			t.Fatalf("persisted upstream diagnostic: %s", raw)
		}
	})
}

func TestAccountTestPreserves429WithoutInferredReason(t *testing.T) {
	for _, tc := range []struct{ name, retryAfter, body, code string }{
		{"raw error", "", `{"type":"error","error":{"type":"rate_limit_error","message":"Error"}}`, "rate_limit_error"},
		{"retry header", "7", `{"type":"error","error":{"type":"rate_limit_error","message":"Error"}}`, "rate_limit_error"},
		{"status only", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := setup(t)
			e.plat.classify = &pluginv1.ClassifyErrorResponse{Reason: "rate limited until tomorrow (default)", AccountEffect: pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_DISABLE}
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Request-Id", "req-fixture")
				w.Header().Set("Set-Cookie", "private")
				w.Header().Set("Authorization", "private")
				if tc.retryAfter != "" {
					w.Header().Set("Retry-After", tc.retryAfter)
				}
				w.WriteHeader(429)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer up.Close()
			e.plat.testURL = up.URL + "/v1/messages"
			id := e.mkAccount("sk-good-key-123")
			_, raw := e.doRaw(e.uid, "POST", "/accounts/"+itoa(id)+"/test", nil)
			d := gjson.GetBytes(raw, "data")
			if d.Get("status").Int() != 429 || d.Get("error_code").String() != tc.code || d.Get("response_headers.request-id").String() != "req-fixture" || d.Get("response_headers.retry-after").String() != tc.retryAfter {
				t.Fatal(string(raw))
			}
			if d.Get("reason").Exists() || d.Get("effect").Exists() || d.Get("body").String() != tc.body {
				t.Fatal("test must report the response without inferred causes", string(raw))
			}
			_, saved := e.doRaw(e.uid, "GET", "/accounts/"+itoa(id), nil)
			if gjson.GetBytes(saved, "data.last_test.message").String() != d.Get("message").String() {
				t.Fatal("last test must preserve the upstream message", string(saved))
			}
			if d.Get("response_headers.authorization").Exists() || d.Get("response_headers.set-cookie").Exists() {
				t.Fatal("credential header exposed")
			}
		})
	}
}

// Classification determines origin, but its inferred reason and scheduling
// recommendation must not replace a real HTTP error or alter the account.
func TestAccountTestFailureIsClassifiedButHarmless(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	up := testUpstream(t)
	e.plat.testURL = up.URL + "/v1/messages"
	e.plat.usageProtocol = "anthropic.messages"
	e.setPlatformUsage("anthropic", anthropicUsage)
	id := e.mkAccount("sk-other-key-1")

	for _, tc := range []struct {
		effect pluginv1.ClassifyErrorResponse_AccountEffect
		want   string
	}{
		{pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_DISABLE, "disable"},
		{pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_COOLDOWN, "cooldown"},
		{pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_UNSPECIFIED, ""},
	} {
		e.plat.classify = &pluginv1.ClassifyErrorResponse{AccountEffect: tc.effect,
			Reason: "upstream rejected credentials (401)", CooldownUntilUnix: 1 << 40}
		_, raw := e.doRaw(e.uid, "POST", fmt.Sprintf("/accounts/%d/test", id), nil)
		d := gjson.GetBytes(raw, "data")
		if d.Get("ok").Bool() || d.Get("status").Int() != 401 || d.Get("usage").Exists() {
			t.Fatalf("%s: %s", tc.want, d)
		}
		if !strings.Contains(d.Get("body").String(), "invalid x-api-key") ||
			!strings.Contains(d.Get("message").String(), "invalid x-api-key") {
			t.Fatalf("%s body/message: %s", tc.want, d)
		}
		if d.Get("reason").Exists() || d.Get("effect").Exists() {
			t.Fatalf("%s classification: %s", tc.want, d)
		}
	}
	// What the plugin was asked: the status, lower-cased headers and a body
	// prefix (as the gateway does).
	last := e.plat.classes[len(e.plat.classes)-1]
	if last.GetStatus() != 401 || last.GetHeaders()["content-type"] != "application/json" ||
		!bytes.Contains(last.GetBodyPrefix(), []byte("invalid x-api-key")) || last.GetTransportError() != "" {
		t.Fatalf("classify request: %+v", last)
	}

	// The account is untouched: still active, no cooldown, no status event.
	var status, reason string
	if err := e.db.SQL.QueryRowContext(ctx,
		`SELECT status, status_reason FROM accounts WHERE id = $1`, id).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "active" || reason != "" {
		t.Fatalf("account changed by a test: %s %q", status, reason)
	}
	if e.mr.Exists(cooldownKey(id)) {
		t.Fatal("test put the account into cooldown")
	}
	for _, ev := range e.eventTypes() {
		if ev == "account.status_changed" {
			t.Fatal("test emitted account.status_changed")
		}
	}

	// A plugin without a usable ClassifyError leaves reason and effect empty.
	e.plat.classify = nil
	_, raw := e.doRaw(e.uid, "POST", fmt.Sprintf("/accounts/%d/test", id), nil)
	d := gjson.GetBytes(raw, "data")
	if d.Get("reason").Exists() || d.Get("effect").Exists() {
		t.Fatalf("classification without a plugin answer: %s", d)
	}

	// A transport failure is reported with the credential-free address, and
	// the plugin classifying it is given the same text.
	e.plat.classify = &pluginv1.ClassifyErrorResponse{
		AccountEffect: pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_COOLDOWN, Reason: "transport error"}
	up.Close()
	e.plat.testURL = up.URL + "/v1/messages?key=secret-value"
	_, raw = e.doRaw(e.uid, "POST", fmt.Sprintf("/accounts/%d/test", id), nil)
	d = gjson.GetBytes(raw, "data")
	if bytes.Contains(raw, []byte("secret-value")) {
		t.Fatalf("query leaked into a transport failure: %s", raw)
	}
	if d.Get("ok").Bool() || d.Get("status").Int() != 0 || !strings.Contains(d.Get("message").String(), up.URL+"/v1/messages") ||
		d.Get("effect").String() != "cooldown" {
		t.Fatalf("transport failure: %s", d)
	}
	last = e.plat.classes[len(e.plat.classes)-1]
	if last.GetStatus() != 0 || !strings.Contains(last.GetTransportError(), up.URL+"/v1/messages") ||
		strings.Contains(last.GetTransportError(), "secret-value") {
		t.Fatalf("transport classify request: %+v", last)
	}
	if e.mr.Exists(cooldownKey(id)) {
		t.Fatal("transport failure put the account into cooldown")
	}
}

// TestAccountTestRecordsLastTest checks last_test (CONTRACTS §50): null until
// the first test, then the outcome of every test, without touching what the
// gateway uses (updated_at stays).
func TestAccountTestRecordsLastTest(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	up := testUpstream(t)
	e.plat.testURL = up.URL + "/v1/messages"
	id := e.mkAccount("sk-good-key-123")
	if code, out := e.do("PATCH", fmt.Sprintf("/accounts/%d", id), map[string]any{"model_mapping": map[string]string{"claude-x": "claude-y"}}); code != 200 {
		t.Fatalf("patch: %d %v", code, out)
	}
	lastTest := func() gjson.Result {
		t.Helper()
		_, raw := e.doRaw(e.uid, "GET", fmt.Sprintf("/accounts/%d", id), nil)
		one := gjson.GetBytes(raw, "data.last_test")
		_, raw = e.doRaw(e.uid, "GET", "/accounts", nil)
		for _, v := range gjson.GetBytes(raw, "data").Array() {
			if v.Get("id").Int() == id && v.Get("last_test").Raw != one.Raw {
				t.Fatalf("list and detail differ: %s vs %s", v.Get("last_test").Raw, one.Raw)
			}
		}
		return one
	}
	if lt := lastTest(); !lt.Exists() || lt.Type != gjson.Null {
		t.Fatalf("untested account: %s", lt.Raw)
	}
	var updated time.Time
	if err := e.db.SQL.QueryRowContext(ctx, `SELECT updated_at FROM accounts WHERE id = $1`, id).Scan(&updated); err != nil {
		t.Fatal(err)
	}

	// Success: requested_model is the caller's choice, model the mapped one.
	_, raw := e.doRaw(e.uid, "POST", fmt.Sprintf("/accounts/%d/test", id), map[string]any{"model": "claude-x"})
	d := gjson.GetBytes(raw, "data")
	if !d.Get("ok").Bool() || d.Get("requested_model").String() != "claude-x" || d.Get("model").String() != "claude-y" {
		t.Fatalf("result: %s", d)
	}
	lt := lastTest()
	if !lt.Get("ok").Bool() || lt.Get("model").String() != "claude-y" || lt.Get("message").String() != "" ||
		lt.Get("latency_ms").Int() < 0 || lt.Get("latency_ms").Int() != d.Get("latency_ms").Int() || lt.Get("at").String() == "" {
		t.Fatalf("last_test after success: %s", lt.Raw)
	}
	// No model given: requested_model is present and empty.
	_, raw = e.doRaw(e.uid, "POST", fmt.Sprintf("/accounts/%d/test", id), nil)
	if r := gjson.GetBytes(raw, "data.requested_model"); !r.Exists() || r.String() != "" {
		t.Fatalf("requested_model without a model: %s", raw)
	}

	// Failure: retain the actual HTTP message, even if classification invents
	// a long reason. The saved record remains bounded and valid UTF-8.
	bad := e.mkAccount("sk-other-key-1")
	e.plat.classify = &pluginv1.ClassifyErrorResponse{Reason: strings.Repeat("凭证无效", 100)}
	_, raw = e.doRaw(e.uid, "POST", fmt.Sprintf("/accounts/%d/test", bad), nil)
	if gjson.GetBytes(raw, "data.ok").Bool() {
		t.Fatalf("bad key passed: %s", raw)
	}
	var ok bool
	var msg string
	if err := e.db.SQL.QueryRowContext(ctx, `SELECT last_test_ok, last_test_message FROM accounts WHERE id = $1`, bad).Scan(&ok, &msg); err != nil {
		t.Fatal(err)
	}
	if ok || len(msg) > maxLastTestMessage || !utf8.ValidString(msg) || msg != gjson.GetBytes(raw, "data.message").String() {
		t.Fatalf("failure record: ok=%v %d bytes %q", ok, len(msg), msg)
	}
	// A short classification reason must not be prepended either.
	e.plat.classify = &pluginv1.ClassifyErrorResponse{Reason: "bad key"}
	_, _ = e.doRaw(e.uid, "POST", fmt.Sprintf("/accounts/%d/test", bad), nil)
	if err := e.db.SQL.QueryRowContext(ctx, `SELECT last_test_message FROM accounts WHERE id = $1`, bad).Scan(&msg); err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(msg, "bad key: ") || !strings.Contains(msg, "invalid x-api-key") {
		t.Fatalf("reason and upstream message: %q", msg)
	}
	// Without a reason the upstream message is kept.
	e.plat.classify = nil
	_, _ = e.doRaw(e.uid, "POST", fmt.Sprintf("/accounts/%d/test", bad), nil)
	if err := e.db.SQL.QueryRowContext(ctx, `SELECT last_test_message FROM accounts WHERE id = $1`, bad).Scan(&msg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "invalid x-api-key") {
		t.Fatalf("upstream message not recorded: %q", msg)
	}

	var after time.Time
	if err := e.db.SQL.QueryRowContext(ctx, `SELECT updated_at FROM accounts WHERE id = $1`, id).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !after.Equal(updated) {
		t.Fatalf("a test changed updated_at: %v -> %v", updated, after)
	}
}

// TestUpstreamAddrStripsCredentials checks the address shown in the console.
func TestUpstreamAddrStripsCredentials(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://host/v1/messages?key=secret#frag", "https://host/v1/messages"},
		{"https://user:pass@host:8443/v1/x", "https://host:8443/v1/x"},
		{"http://host", "http://host"},
	} {
		u, err := url.Parse(tc.in)
		if err != nil {
			t.Fatal(err)
		}
		if got := upstreamAddr(u); got != tc.want {
			t.Fatalf("%s: got %s want %s", tc.in, got, tc.want)
		}
	}
}

// TestAccountTestDefaultModel checks the model of a test that names none:
// the account's own list first, then its mapping, the plugin's default only
// when the account lists neither.
func TestAccountTestDefaultModel(t *testing.T) {
	e := setup(t)
	up := testUpstream(t)
	e.plat.testURL = up.URL + "/v1/messages"
	id := e.mkAccount("sk-good-key-123")
	asked := func() string {
		t.Helper()
		e.plat.mu.Lock()
		e.plat.tests = nil
		e.plat.mu.Unlock()
		if code, raw := e.doRaw(e.uid, "POST", fmt.Sprintf("/accounts/%d/test", id), nil); code != 200 ||
			gjson.GetBytes(raw, "data.requested_model").String() != "" {
			t.Fatalf("test: %d %s", code, raw)
		}
		e.plat.mu.Lock()
		defer e.plat.mu.Unlock()
		return e.plat.tests[len(e.plat.tests)-1].GetModel()
	}
	patch := func(body map[string]any) {
		t.Helper()
		if code, out := e.do("PATCH", fmt.Sprintf("/accounts/%d", id), body); code != 200 {
			t.Fatalf("patch: %d %v", code, out)
		}
	}
	if got := asked(); got != "" {
		t.Fatalf("no list: plugin default expected, got %q", got)
	}
	patch(map[string]any{"model_mapping": map[string]string{"claude-z": "up-z", "claude-b": "up-b"}})
	if got := asked(); got != "up-b" {
		t.Fatalf("mapping only: %q", got)
	}
	patch(map[string]any{"models": []string{"claude-sonnet-4-5", "claude-b"}})
	if got := asked(); got != "claude-sonnet-4-5" {
		t.Fatalf("model list: %q", got)
	}
}

// A test is offered the protocols the account serves natively on its own
// platforms, most preferred first and without WebSocket and token-counting
// protocols; its protocol binding and, for the tested model, its model
// protocols narrow them. A failed test is classified under the protocol
// the plugin used.
func TestAccountTestProtocols(t *testing.T) {
	e := setup(t)
	at := &e.gen.types[0]
	at.Type.Platforms = []manifest.AccountPlatform{{Platform: "anthropic"}, {Platform: "openai"}}
	openai := &e.gen.plats[1].Platform
	ws := ep("GET", "/v1/responses", "openai.responses_ws", "usage")
	ws.Kind = manifest.EndpointKindWebSocket
	openai.Endpoints = append(openai.Endpoints, ws)
	e.plat.usageProtocol = "openai.chat"
	e.plat.classify = &pluginv1.ClassifyErrorResponse{Reason: "rejected"}
	id := e.mkAccount("sk-wrong-key-1")
	offered := func(model string) string {
		t.Helper()
		if code, raw := e.doRaw(e.uid, "POST", fmt.Sprintf("/accounts/%d/test", id), map[string]any{"model": model}); code != 200 ||
			gjson.GetBytes(raw, "data.ok").Bool() {
			t.Fatalf("test: %d %s", code, raw)
		}
		e.plat.mu.Lock()
		defer e.plat.mu.Unlock()
		return strings.Join(e.plat.tests[len(e.plat.tests)-1].GetProtocols(), ",")
	}
	patch := func(body map[string]any) {
		t.Helper()
		if code, out := e.do("PATCH", fmt.Sprintf("/accounts/%d", id), body); code != 200 {
			t.Fatalf("patch: %d %v", code, out)
		}
	}
	if got := offered(""); got != "anthropic.messages,openai.chat,openai.embeddings" {
		t.Fatalf("default: %s", got)
	}
	e.plat.mu.Lock()
	meta := e.plat.classes[len(e.plat.classes)-1].GetMeta()
	e.plat.mu.Unlock()
	if meta.GetProtocol() != "openai.chat" {
		t.Fatalf("classified under %q", meta.GetProtocol())
	}
	patch(map[string]any{"protocols": []string{"openai.embeddings", "openai.chat"}})
	if got := offered(""); got != "openai.chat,openai.embeddings" {
		t.Fatalf("bound: %s", got)
	}
	patch(map[string]any{"model_protocols": []map[string]any{{"models": []string{"m-embed"}, "protocols": []string{"openai.embeddings"}}}})
	if got := offered("m-embed"); got != "openai.embeddings" {
		t.Fatalf("model bound: %s", got)
	}
	if got := offered("m-other"); got != "openai.chat,openai.embeddings" {
		t.Fatalf("other model: %s", got)
	}
}

// An image generation (endpoint test "explicit") is only offered when the
// console names the model, an edit ("never") not at all; a model's
// model_protocols entry orders its protocols; a test naming no model skips
// the models left with nothing to test and a model with nothing to test is
// refused (CONTRACTS §122.2); the result and last_test say which
// protocol the plugin used (CONTRACTS §122).
func TestAccountTestProtocolsByEndpointTest(t *testing.T) {
	e := setup(t)
	at := &e.gen.types[0]
	at.Type.Platforms = []manifest.AccountPlatform{{Platform: "anthropic"}, {Platform: "openai"}}
	openai := &e.gen.plats[1].Platform
	images := ep("POST", "/v1/images/generations", "openai.images", "usage")
	images.Test = manifest.EndpointTestExplicit
	edits := ep("POST", "/v1/images/edits", "openai.images_edit", "usage")
	edits.Test = manifest.EndpointTestNever
	openai.Endpoints = append(openai.Endpoints, images, edits)
	e.plat.usageProtocol = "openai.images"
	id := e.mkAccount("sk-wrong-key-1")
	var last []byte
	test := func(model string) (protocols, asked string) {
		t.Helper()
		code, raw := e.doRaw(e.uid, "POST", fmt.Sprintf("/accounts/%d/test", id), map[string]any{"model": model})
		if code != 200 {
			t.Fatalf("test: %d %s", code, raw)
		}
		last = raw
		e.plat.mu.Lock()
		defer e.plat.mu.Unlock()
		tr := e.plat.tests[len(e.plat.tests)-1]
		return strings.Join(tr.GetProtocols(), ","), tr.GetModel()
	}
	patch := func(body map[string]any) {
		t.Helper()
		if code, out := e.do("PATCH", fmt.Sprintf("/accounts/%d", id), body); code != 200 {
			t.Fatalf("patch: %d %v", code, out)
		}
	}
	if got, _ := test(""); got != "anthropic.messages,openai.chat,openai.embeddings" {
		t.Fatalf("no model: %s", got)
	}
	if got, _ := test("m-any"); got != "anthropic.messages,openai.chat,openai.embeddings,openai.images" {
		t.Fatalf("named model: %s", got)
	}
	if p := gjson.GetBytes(last, "data.protocol").String(); p != "openai.images" {
		t.Fatalf("result protocol %q: %s", p, last)
	}
	_, detail := e.doRaw(e.uid, "GET", fmt.Sprintf("/accounts/%d", id), nil)
	if p := gjson.GetBytes(detail, "data.last_test.protocol").String(); p != "openai.images" {
		t.Fatalf("last_test protocol %q: %s", p, detail)
	}
	patch(map[string]any{"models": []string{"m-img", "m-order", "m-chat"}, "model_protocols": []map[string]any{
		{"models": []string{"m-img"}, "protocols": []string{"openai.images", "openai.images_edit"}},
		{"models": []string{"m-order"}, "protocols": []string{"openai.embeddings", "anthropic.messages", "openai.chat"}},
	}})
	if got, _ := test("m-img"); got != "openai.images" {
		t.Fatalf("image model: %s", got)
	}
	if got, _ := test("m-order"); got != "openai.embeddings,anthropic.messages,openai.chat" {
		t.Fatalf("entry order: %s", got)
	}
	// m-img has only an explicit protocol: the default model is the next.
	if got, model := test(""); model != "m-order" || got != "openai.embeddings,anthropic.messages,openai.chat" {
		t.Fatalf("default model %q: %s", model, got)
	}
	// A model with no protocol to test (an edit only; an image generation
	// when the model is not named) is refused, never tested with the
	// plugin's default request: nothing reaches the plugin.
	refused := func(what string, body map[string]any) {
		t.Helper()
		e.plat.mu.Lock()
		before := len(e.plat.tests)
		e.plat.mu.Unlock()
		code, raw := e.doRaw(e.uid, "POST", fmt.Sprintf("/accounts/%d/test", id), body)
		if code != http.StatusBadRequest || gjson.GetBytes(raw, "error.code").String() != "no_testable_protocol" {
			t.Fatalf("%s: %d %s", what, code, raw)
		}
		e.plat.mu.Lock()
		after := len(e.plat.tests)
		e.plat.mu.Unlock()
		if after != before {
			t.Fatalf("%s: the plugin built a test request", what)
		}
		_, raw = e.doRaw(e.uid, "GET", fmt.Sprintf("/accounts/%d", id), nil)
		if last := gjson.GetBytes(raw, "data.last_test"); last.Get("ok").Bool() || last.Get("error_source").String() != "gateway" ||
			last.Get("error_code").String() != "no_testable_protocol" {
			t.Fatalf("%s: last test %s", what, last)
		}
	}
	patch(map[string]any{"models": []string{"m-img", "m-edit"}, "model_protocols": []map[string]any{
		{"models": []string{"m-img"}, "protocols": []string{"openai.images"}},
		{"models": []string{"m-edit"}, "protocols": []string{"openai.images_edit"}},
	}})
	refused("an edit only, named", map[string]any{"model": "m-edit"})
	refused("every model image only, none named", nil)
}

// A type serving one protocol of a platform (codex: Responses only) is
// offered that protocol, whatever the model; the image endpoints of the
// platform are not its own.
func TestAccountTestProtocolsSubset(t *testing.T) {
	e := setup(t)
	openai := &e.gen.plats[1].Platform
	responses := ep("POST", "/v1/responses", "openai.responses", "usage")
	responses.ID = "responses"
	images := ep("POST", "/v1/images/generations", "openai.images", "usage")
	images.ID, images.Test = "images_generations", manifest.EndpointTestExplicit
	openai.Endpoints = append(openai.Endpoints, responses, images)
	e.gen.types[0].Type.Platforms = []manifest.AccountPlatform{{Platform: "openai", Endpoints: []string{"responses"}}}
	id := e.mkAccount("sk-wrong-key-1")
	for _, model := range []string{"", "gpt-5", "gpt-image-2"} {
		if code, raw := e.doRaw(e.uid, "POST", fmt.Sprintf("/accounts/%d/test", id), map[string]any{"model": model}); code != 200 {
			t.Fatalf("test %q: %d %s", model, code, raw)
		}
		e.plat.mu.Lock()
		got := strings.Join(e.plat.tests[len(e.plat.tests)-1].GetProtocols(), ",")
		e.plat.mu.Unlock()
		if got != "openai.responses" {
			t.Errorf("model %q: %s", model, got)
		}
	}
}

// A protocol the type serves adapted is tested after the native ones, by
// the plugin (the test does not go through the conversion): an image
// generation adapted to Responses is offered when the test names the
// model (CONTRACTS §122.2).
func TestAccountTestProtocolsAdapted(t *testing.T) {
	e := setup(t)
	openai := &e.gen.plats[1].Platform
	responses := ep("POST", "/v1/responses", "openai.responses", "usage")
	responses.ID = "responses"
	images := ep("POST", "/v1/images/generations", "openai.images", "usage")
	images.ID, images.Test = "images_generations", manifest.EndpointTestExplicit
	openai.Endpoints = append(openai.Endpoints, responses, images)
	e.gen.types[0].Type.Platforms = []manifest.AccountPlatform{{Platform: "openai", Endpoints: []string{"responses"},
		Adapted: map[string]manifest.AdaptOptions{"images_generations": {Model: "gpt-6-luna"}}}}
	id := e.mkAccount("sk-wrong-key-1")
	for model, want := range map[string]string{"": "openai.responses", "gpt-image-2": "openai.responses,openai.images"} {
		if code, raw := e.doRaw(e.uid, "POST", fmt.Sprintf("/accounts/%d/test", id), map[string]any{"model": model}); code != 200 {
			t.Fatalf("test %q: %d %s", model, code, raw)
		}
		e.plat.mu.Lock()
		got := strings.Join(e.plat.tests[len(e.plat.tests)-1].GetProtocols(), ",")
		e.plat.mu.Unlock()
		if got != want {
			t.Errorf("model %q: %s, want %s", model, got, want)
		}
	}
}

// An account with neither models nor a mapping whose type serves only
// protocols a test without a model may not use (an image generation, an
// edit) is refused too: no empty protocol list goes to the plugin's
// default test (CONTRACTS §122.2).
func TestAccountTestNoModelsNoTestableProtocol(t *testing.T) {
	e := setup(t)
	openai := &e.gen.plats[1].Platform
	images := ep("POST", "/v1/images/generations", "openai.images", "usage")
	images.ID, images.Test = "images_generations", manifest.EndpointTestExplicit
	edits := ep("POST", "/v1/images/edits", "openai.images_edit", "usage")
	edits.ID, edits.Test = "images_edits", manifest.EndpointTestNever
	openai.Endpoints = append(openai.Endpoints, images, edits)
	e.gen.types[0].Type.Platforms = []manifest.AccountPlatform{{Platform: "openai", Endpoints: []string{"images_generations", "images_edits"}}}
	id := e.mkAccount("sk-wrong-key-1")
	code, raw := e.doRaw(e.uid, "POST", fmt.Sprintf("/accounts/%d/test", id), nil)
	if code != http.StatusBadRequest || gjson.GetBytes(raw, "error.code").String() != "no_testable_protocol" {
		t.Fatalf("test: %d %s", code, raw)
	}
	e.plat.mu.Lock()
	defer e.plat.mu.Unlock()
	if len(e.plat.tests) != 0 {
		t.Fatalf("the plugin built a test request: %v", e.plat.tests)
	}
}

// The test request's plugin is handed the account with the proxy the test
// goes out through; with the proxy disabled nothing is built or sent and the
// result says why (CONTRACTS §124).
func TestAccountTestCarriesTheProxy(t *testing.T) {
	e := setup(t)
	up := testUpstream(t)
	e.plat.testURL = up.URL + "/v1/messages"
	id := e.mkAccount("sk-good-key-123")
	host, port, hits := forwardProxy(t)
	pid := e.exec1(`INSERT INTO proxies (name, protocol, host, port, username) VALUES ('p', 'http', $1, $2, 'bob') RETURNING id`, host, port)
	if code, out := e.do("PATCH", fmt.Sprintf("/accounts/%d", id), map[string]any{"proxy_id": pid}); code != http.StatusOK {
		t.Fatalf("set proxy: %d %v", code, out)
	}
	built := func() []*pluginv1.BuildTestRequestRequest {
		e.plat.mu.Lock()
		defer e.plat.mu.Unlock()
		return slices.Clone(e.plat.tests)
	}
	code, raw := e.doRaw(e.uid, "POST", fmt.Sprintf("/accounts/%d/test", id), map[string]any{"model": "claude-x"})
	tests := built()
	if code != 200 || !gjson.GetBytes(raw, "data.ok").Bool() || len(tests) != 1 || hits.Load() != 1 {
		t.Fatalf("test through the proxy: %d %s (builds %d, proxy hits %d)", code, raw, len(tests), hits.Load())
	}
	if p := tests[0].GetAccount().GetProxy(); p.GetId() != pid || p.GetHost() != host || int(p.GetPort()) != port || p.GetUsername() != "bob" {
		t.Fatalf("proxy handed to the plugin: %v", p)
	}

	if _, err := e.db.SQL.ExecContext(t.Context(), `UPDATE proxies SET status = 'disabled', updated_at = clock_timestamp() WHERE id = $1`, pid); err != nil {
		t.Fatal(err)
	}
	e.prx.Invalidate(pid)
	code, raw = e.doRaw(e.uid, "POST", fmt.Sprintf("/accounts/%d/test", id), map[string]any{"model": "claude-x"})
	d := gjson.GetBytes(raw, "data")
	if code != 200 || d.Get("ok").Bool() || d.Get("message").String() != "proxy disabled" || len(built()) != 1 || hits.Load() != 1 {
		t.Fatalf("test with the proxy disabled: %d %s (builds %d, proxy hits %d)", code, raw, len(built()), hits.Load())
	}
}
