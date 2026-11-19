package e2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

// platformCase is one platform plugin's account type, driven through the
// gateway against the mock.
type platformCase struct {
	name, plugin, typ string
	family            string      // model family of the run price ("claude", "gpt", "gemini")
	usage             usageFamily // how the mock's usage is billed
	streamOnly        bool        // the upstream only streams: a non-stream request is sent as a stream
	nonStreamOnly     bool        // the endpoint does not stream: only a non-stream request is sent
	keyPrefix         string      // a vendor prefix the key must start with
	// platformPlugin is a plugin declaring the platform the case calls,
	// enabled first ("" = a built-in platform or the case's own plugin).
	platformPlugin string
	// creds is the account's credentials for upstream key k.
	creds func(e *Env, k string) map[string]any
	// call sends the request for model through the gateway.
	call func(e *Env, apiKey, model, prompt string, stream bool) *GatewayResult
	// check inspects the recorded upstream request.
	check func(t *testing.T, r MockRequest, k, model, prompt string, stream bool)
}

func messagesCall(e *Env, apiKey, model, prompt string, stream bool) *GatewayResult {
	return e.Messages(apiKey, MessagesBody(model, prompt, stream), nil)
}

func chatCall(e *Env, apiKey, model, prompt string, stream bool) *GatewayResult {
	return e.OpenAI(apiKey, "/v1/chat/completions", ChatBody(model, prompt, stream))
}

func responsesCall(e *Env, apiKey, model, prompt string, stream bool) *GatewayResult {
	return e.OpenAI(apiKey, "/v1/responses", map[string]any{"model": model, "input": prompt, "stream": stream})
}

// systemoneBody is the TypeSafe System One request of model and prompt.
func systemoneBody(model, prompt string) map[string]any {
	return map[string]any{"model": model, "state": prompt, "questions": map[string]any{"is_greeting": map[string]any{"type": "noul"}}}
}

// systemoneCall calls POST /v1/systemone (non-stream only) with the danger
// zone header a request with images needs.
func systemoneCall(e *Env, apiKey, model, prompt string, _ bool) *GatewayResult {
	return e.Gateway(e.T, e.BaseURL, "/v1/systemone", "", systemoneBody(model, prompt),
		map[string]string{"Authorization": "Bearer " + apiKey, "TypeSafe-Danger-Zone-Images": "true"})
}

// systemoneCheck requires the systemone request on host ("" = the mock's
// own) with the key as Bearer, the client's body byte for byte (GatewayTry
// encodes it with encoding/json) and the danger zone header passed through.
func systemoneCheck(host string) func(t *testing.T, r MockRequest, k, model, prompt string, stream bool) {
	return func(t *testing.T, r MockRequest, k, model, prompt string, _ bool) {
		expectPath(t, r, host, "/v1/systemone")
		sent, _ := json.Marshal(systemoneBody(model, prompt))
		if r.Header("authorization") != "Bearer "+k || r.Header("typesafe-danger-zone-images") != "true" || r.TLS != (host != "") {
			t.Fatalf("systemone headers: %s", r.Headers.Raw)
		}
		if r.Body != string(sent) {
			t.Fatalf("systemone body changed:\n sent %s\n got  %s", sent, r.Body)
		}
	}
}

func geminiCall(e *Env, apiKey, model, prompt string, stream bool) *GatewayResult {
	if stream {
		return e.Gemini(apiKey, model, "streamGenerateContent", "alt=sse", GeminiBody(prompt))
	}
	return e.Gemini(apiKey, model, "generateContent", "", GeminiBody(prompt))
}

// expectPath requires the recorded path (and Host, "" = the mock's own).
func expectPath(t *testing.T, r MockRequest, host, path string) {
	t.Helper()
	if r.Path != path || (host != "" && r.Host != host) || (host == "" && r.TLS) || r.Proxied {
		t.Fatalf("upstream %s %s (tls=%v proxied=%v), want %s %s direct", r.Host, r.Path, r.TLS, r.Proxied, host, path)
	}
}

// account is the account of the case on upstream key k.
func (pc platformCase) account(e *Env, k string) AccountSpec {
	return AccountSpec{PluginKey: pc.plugin, Type: pc.typ, Credentials: pc.creds(e, k)}
}

// platformCaseNamed returns the case of platformCases named name.
func platformCaseNamed(t testing.TB, name string) platformCase {
	t.Helper()
	for _, pc := range platformCases() {
		if pc.name == name {
			return pc
		}
	}
	t.Fatalf("no platform case %q", name)
	return platformCase{}
}

func platformCases() []platformCase {
	keyURL := func(e *Env, k string) map[string]any {
		return map[string]any{"api_key": k, "base_url": e.MockInternalURL}
	}
	return []platformCase{
		{name: "anthropic", plugin: AnthropicPlugin, typ: AnthropicAPIKey, family: "claude", usage: usageAnthropic,
			creds: keyURL, call: messagesCall,
			check: func(t *testing.T, r MockRequest, k, model, prompt string, stream bool) {
				expectPath(t, r, "", "/v1/messages")
				if r.Header("x-api-key") != k || r.Header("anthropic-version") == "" {
					t.Fatalf("anthropic headers: %s", r.Headers.Raw)
				}
				validJSONBody(t, r, model, prompt, "anthropic")
			}},
		{name: "relay", plugin: RelayPlugin, typ: RelayKey, family: "claude", usage: usageAnthropic,
			creds: keyURL, call: messagesCall,
			check: func(t *testing.T, r MockRequest, k, model, prompt string, stream bool) {
				expectPath(t, r, "", "/v1/messages")
				if r.Header("x-api-key") != k {
					t.Fatalf("relay headers: %s", r.Headers.Raw)
				}
				validJSONBody(t, r, model, prompt, "relay")
			}},
		{name: "openai", plugin: OpenAIPlugin, typ: APIKeyType, family: "gpt", usage: usageOpenAI, keyPrefix: "sk-",
			creds: keyURL, call: chatCall,
			check: func(t *testing.T, r MockRequest, k, model, prompt string, stream bool) {
				expectPath(t, r, "", "/v1/chat/completions")
				if r.Header("authorization") != "Bearer "+k {
					t.Fatalf("openai headers: %s", r.Headers.Raw)
				}
				validJSONBody(t, r, model, prompt, "openai")
				if stream && !gjson.Get(r.Body, "stream_options.include_usage").Bool() {
					t.Fatalf("openai stream without include_usage: %s", r.Body)
				}
			}},
		{name: "gemini", plugin: GeminiPlugin, typ: APIKeyType, family: "gemini", usage: usageGemini,
			creds: keyURL, call: geminiCall,
			check: func(t *testing.T, r MockRequest, k, model, prompt string, stream bool) {
				action := "generateContent"
				if stream {
					action = "streamGenerateContent"
				}
				expectPath(t, r, "", "/v1beta/models/"+model+":"+action)
				if r.Key() != k {
					t.Fatalf("gemini key: %+v", r)
				}
				validJSONBody(t, r, "", prompt, "gemini")
			}},
		{name: "azure_openai", plugin: AzurePlugin, typ: AzureV1Type, family: "gpt", usage: usageOpenAI,
			creds: keyURL, call: chatCall,
			check: func(t *testing.T, r MockRequest, k, model, prompt string, stream bool) {
				expectPath(t, r, "", "/openai/v1/chat/completions")
				if r.Header("api-key") != k || r.Header("authorization") != "" {
					t.Fatalf("azure headers: %s", r.Headers.Raw)
				}
				validJSONBody(t, r, model, prompt, "azure")
			}},
		{name: "volcengine", plugin: "volcengine", typ: "relay", family: "gpt", usage: usageOpenAI,
			creds: keyURL, call: chatCall,
			check: func(t *testing.T, r MockRequest, k, model, prompt string, stream bool) {
				expectPath(t, r, "", "/v1/chat/completions")
				if r.Header("authorization") != "Bearer "+k {
					t.Fatalf("volcengine headers: %s", r.Headers.Raw)
				}
				validJSONBody(t, r, model, prompt, "volcengine")
			}},
		// The upstream sites (CONTRACTS §119): one key type on the built-in
		// platforms, each protocol forwarded as it is with its own auth header.
		{name: "newapi", plugin: NewAPIPlugin, typ: APIKeyType, family: "gpt", usage: usageOpenAI,
			creds: keyURL, call: chatCall,
			check: func(t *testing.T, r MockRequest, k, model, prompt string, stream bool) {
				expectPath(t, r, "", "/v1/chat/completions")
				if r.Header("authorization") != "Bearer "+k {
					t.Fatalf("newapi headers: %s", r.Headers.Raw)
				}
				validJSONBody(t, r, model, prompt, "newapi")
				if stream && !gjson.Get(r.Body, "stream_options.include_usage").Bool() {
					t.Fatalf("newapi stream without include_usage: %s", r.Body)
				}
			}},
		// The route's own auth: Gemini takes the key in x-goog-api-key only.
		{name: "sub2api", plugin: Sub2APIPlugin, typ: APIKeyType, family: "gemini", usage: usageGemini,
			creds: keyURL, call: geminiCall,
			check: func(t *testing.T, r MockRequest, k, model, prompt string, stream bool) {
				action, query := "generateContent", ""
				if stream {
					action, query = "streamGenerateContent", "alt=sse"
				}
				expectPath(t, r, "", "/v1beta/models/"+model+":"+action)
				if r.Header("x-goog-api-key") != k || r.Header("authorization") != "" || r.Query != query {
					t.Fatalf("sub2api gemini headers/query %q: %s", r.Query, r.Headers.Raw)
				}
				validJSONBody(t, r, "", prompt, "sub2api")
			}},
		// typesafe's own platform on its default, official address (the
		// mock under TypeSafe's host name), billed on usage.input_tokens.
		{name: "typesafe", plugin: TypeSafePlugin, typ: APIKeyType, family: "jev", usage: usageInputOnly, nonStreamOnly: true,
			creds: func(_ *Env, k string) map[string]any { return map[string]any{"api_key": k} }, call: systemoneCall,
			check: systemoneCheck("api.typesafe.ai")},
		// A New API key forwards Jev once the typesafe plugin declares the
		// platform: to the site's own address.
		{name: "newapi_jev", plugin: NewAPIPlugin, typ: APIKeyType, family: "jev", usage: usageInputOnly, nonStreamOnly: true,
			platformPlugin: TypeSafePlugin, creds: keyURL, call: systemoneCall, check: systemoneCheck("")},
		// No configurable address: the plugins call the vendor hosts, which
		// the e2e network resolves to the mock (TLS, the mock's CA).
		{name: "vertex", plugin: "vertex", typ: "vertex_apikey", family: "gemini", usage: usageGemini,
			creds: func(_ *Env, k string) map[string]any { return map[string]any{"api_key": k} }, call: geminiCall,
			check: func(t *testing.T, r MockRequest, k, model, prompt string, stream bool) {
				action := "generateContent"
				if stream {
					action = "streamGenerateContent"
				}
				expectPath(t, r, "aiplatform.googleapis.com", "/v1/publishers/google/models/"+model+":"+action)
				if r.Key() != k || !r.TLS {
					t.Fatalf("vertex key/tls: %+v", r)
				}
				validJSONBody(t, r, "", prompt, "vertex")
			}},
		{name: "bedrock", plugin: "bedrock", typ: "bedrock_aksk", family: "claude", usage: usageAnthropic,
			creds: func(_ *Env, k string) map[string]any { return bedrockCreds(k) }, call: messagesCall,
			check: func(t *testing.T, r MockRequest, k, model, prompt string, stream bool) {
				action := "invoke"
				if stream {
					action = "invoke-with-response-stream"
				}
				expectPath(t, r, "bedrock-runtime.us-east-1.amazonaws.com", "/model/"+model+"/"+action)
				if r.Key() != k || !strings.HasPrefix(r.Header("authorization"), "AWS4-HMAC-SHA256") || r.Header("x-amz-date") == "" {
					t.Fatalf("bedrock signature headers: %s", r.Headers.Raw)
				}
				// InvokeModel names the model in the path and the version in the body.
				if !strings.Contains(r.Body, prompt) || gjson.Get(r.Body, "model").Exists() || gjson.Get(r.Body, "anthropic_version").String() == "" {
					t.Fatalf("bedrock body: %s", r.Body)
				}
			}},
		// The Codex backend only streams: a non-stream request is sent as a
		// stream and answered as JSON.
		{name: "codex", plugin: "codex", typ: "codex_oauth", family: "gpt", usage: usageOpenAI, streamOnly: true,
			creds: func(_ *Env, k string) map[string]any { return codexCreds(k) }, call: responsesCall,
			check: func(t *testing.T, r MockRequest, k, model, prompt string, stream bool) {
				expectPath(t, r, "chatgpt.com", "/backend-api/codex/responses")
				if r.Header("authorization") != "Bearer "+k || r.Header("chatgpt-account-id") != "acct-e2e" || !r.TLS {
					t.Fatalf("codex headers: %s", r.Headers.Raw)
				}
				validJSONBody(t, r, model, prompt, "codex")
				if !gjson.Get(r.Body, "stream").Bool() || gjson.Get(r.Body, "store").Bool() {
					t.Fatalf("codex body must stream and not store: %s", r.Body)
				}
			}},
	}
}

// AC 30: every platform plugin forwards through Execute (forwarding v2).
// For each of the twelve plugins (and a New API key on TypeSafe's
// platform), a non-stream and a stream request (non-stream only on
// /v1/systemone) reach the mock as the request the plugin built (address,
// credential header, body), the client gets a complete answer, and the
// request is billed once from the usage the core read under the facts the
// plugin returned (host_usage). The three upstream plugins run with no file
// or network permission under the default sandbox.
func TestAC30_PlatformPluginsForwardThroughExecute(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	m := e.Mock()
	for _, pc := range platformCases() {
		t.Run(pc.name, func(t *testing.T) {
			e := e.With(t)
			if pc.platformPlugin != "" {
				e.EnsurePlugin(admin, pc.platformPlugin, "")
			}
			k := pc.keyPrefix + fmt.Sprintf("AKIAE2E%s%d%s", strings.ToUpper(e.RunID), nextSeq(), "MOCKKEY")
			tn := e.singleAccount(admin, "grp-fwd-"+pc.name, pc.account(e, k), k, pc.family, "fwd-"+strings.ReplaceAll(pc.name, "_", "-"))
			model, accountID := tn.Model, tn.Accounts[0].ID
			for _, stream := range []bool{false, true} {
				if stream && pc.nonStreamOnly {
					continue
				}
				what := fmt.Sprintf("%s stream=%v", pc.name, stream)
				prompt := "fwd-" + e.Name(pc.name)
				mark := m.Mark(t)
				g := pc.call(e, tn.APIKey, model, prompt, stream)
				if g.Status != 200 {
					t.Fatalf("%s: HTTP %d %s", what, g.Status, g.Body)
				}
				if stream && len(g.Events) == 0 {
					t.Fatalf("%s: no stream events: %s", what, g.Body)
				}
				if !stream && (len(g.Events) != 0 || !g.JSON().IsObject()) {
					t.Fatalf("%s: want a JSON answer, got %d events %s", what, len(g.Events), g.Body)
				}
				r := oneMockCall(t, m, mark, k, what)
				pc.check(t, r, k, model, prompt, stream || pc.streamOnly)
				e.expectBilled(admin, tn.User.UserID, g.RequestID, accountID, pc.usage, stream)
			}
		})
	}
}
