package standard_test

import (
	"context"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"testing"
	"time"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/classify"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/anthropic"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/gemini"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/openai"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/typesafe"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// relaySpec is the credential rule of a relay site: its own base URL,
// required, any key.
var relaySpec = apikey.Spec{RequireBaseURL: true, Versions: []string{"/v1beta", "/v1"}}

// relaySite is a relay site in the shape of the New API plugin (design
// §3.5.2): one type serving the OpenAI, Anthropic, Gemini and TypeSafe
// protocols, each with its vendor's credential header.
func relaySite() standard.Type {
	return standard.Merge("apikey", relaySpec, openai.APIKey(""), anthropic.APIKey(""), gemini.APIKey(""), typesafe.APIKey(""))
}

var relayAccount = &pluginv1.Account{Id: 5, Type: "apikey", CredentialsJson: `{"api_key":"site-key-0123"}`, SettingsJson: `{"base_url":"https://relay.example.com/v1"}`}

// Each protocol of a merged type goes out with its own preset's credential
// header and headers, on the account's base URL.
func TestMergeRoutes(t *testing.T) {
	p := standard.New(relaySite())
	for protocol, want := range map[string]struct {
		url     string
		auth    map[string]string
		headers map[string]string
	}{
		openai.ProtocolChat:           {"https://relay.example.com/v1/chat/completions", map[string]string{"authorization": "Bearer site-key-0123"}, map[string]string{"content-type": "application/json"}},
		anthropic.ProtocolMessages:    {"https://relay.example.com/v1/messages", map[string]string{"x-api-key": "site-key-0123"}, map[string]string{"content-type": "application/json", "anthropic-version": anthropic.DefaultAPIVersion}},
		gemini.ProtocolGenerate:       {"https://relay.example.com/v1beta/models/g-1:generateContent", map[string]string{"x-goog-api-key": "site-key-0123"}, map[string]string{"content-type": "application/json"}},
		typesafe.ProtocolSystemOne:    {"https://relay.example.com/v1/systemone", map[string]string{"authorization": "Bearer site-key-0123"}, map[string]string{"content-type": "application/json"}},
		openai.ProtocolResponsesWS:    {"wss://relay.example.com/v1/responses", map[string]string{"authorization": "Bearer site-key-0123"}, map[string]string{"openai-beta": openai.ResponsesWebSocketBeta}},
		anthropic.ProtocolCountTokens: {"https://relay.example.com/v1/messages/count_tokens", map[string]string{"x-api-key": "site-key-0123"}, map[string]string{"content-type": "application/json", "anthropic-version": anthropic.DefaultAPIVersion}},
	} {
		out, err := p.BuildUpstream(context.Background(), &pluginv1.ExecuteRequest{
			Account: relayAccount, Meta: &pluginv1.RequestMeta{Protocol: protocol, Model: "g-1", Stream: true},
			Body: []byte(`{"model":"m-1","stream":true}`),
		})
		if err != nil {
			t.Fatalf("%s: %v", protocol, err)
		}
		if out.Url != want.url || !reflect.DeepEqual(out.Headers, want.auth) || !reflect.DeepEqual(out.DefaultHeaders, want.headers) {
			t.Errorf("%s: %s %v %v, want %s %v %v", protocol, out.Url, out.Headers, out.DefaultHeaders, want.url, want.auth, want.headers)
		}
		// The chat preset's edit is kept on its route only.
		if wantBody := `{"model":"m-1","stream":true}`; protocol != openai.ProtocolChat && string(out.Body) != wantBody {
			t.Errorf("%s: body %s", protocol, out.Body)
		}
	}
	// No default protocol: a gateway request always names one.
	if _, err := p.BuildUpstream(context.Background(), &pluginv1.ExecuteRequest{Account: relayAccount, Body: []byte(`{}`)}); err == nil {
		t.Error("a request without a protocol was built")
	}
}

// What a merged type takes from its first part, and what it collects.
func TestMergeShape(t *testing.T) {
	m := relaySite()
	o := openai.APIKey("")
	if m.ID != "apikey" || !reflect.DeepEqual(m.Spec, relaySpec) || m.Auth != o.Auth || len(m.Headers) != 0 || m.DefaultProtocol != "" ||
		m.Test.Protocol != openai.ProtocolChat || m.Models.Path != "/v1/models" || m.Balance != nil {
		t.Fatalf("merged type %+v", m)
	}
	var tests []string
	for _, x := range m.Tests {
		tests = append(tests, x.Protocol)
	}
	slices.Sort(tests)
	if want := []string{anthropic.ProtocolMessages, gemini.ProtocolGenerate, gemini.ProtocolStreamGenerate, openai.ProtocolChat,
		openai.ProtocolEmbeddings, openai.ProtocolImages, openai.ProtocolResponses, typesafe.ProtocolSystemOne}; !slices.Equal(tests, want) {
		t.Errorf("tests %v, want %v", tests, want)
	}
	// openai: chat, responses, responses_ws, embeddings, images, images_edit.
	if got, want := slices.Sorted(maps.Keys(m.Routes)), 6+2+3+1; len(got) != want {
		t.Errorf("routes %v", got)
	}
	// A merged type merges again: its routes keep their auth and tests are
	// not repeated.
	twice := standard.Merge("again", relaySpec, m)
	if err := standard.Check(twice); err != nil || len(twice.Tests) != len(m.Tests) || *twice.Routes[gemini.ProtocolGenerate].Auth != (standard.Auth{Header: "x-goog-api-key"}) {
		t.Errorf("merged twice: %v, %d tests", err, len(twice.Tests))
	}
	for name, parts := range map[string][]standard.Type{
		"no part":          nil,
		"a protocol twice": {openai.APIKey(""), openai.APIKey("")},
		"filtered fields":  {func() standard.Type { o := openai.APIKey(""); o.FilterFields = []string{"x"}; return o }()},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: Merge did not panic", name)
				}
			}()
			standard.Merge("x", relaySpec, parts...)
		}()
	}
}

// A merged type classifies each protocol's errors with its preset's policy:
// a 529 is Anthropic's overload (30 s), an OpenAI 429 with
// insufficient_quota disables, a TypeSafe 429 waits for Retry-After; a
// request without a known protocol gets the first part's.
func TestMergeClassify(t *testing.T) {
	p := standard.New(relaySite())
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	classifyAs := func(protocol string, status int32, headers map[string]string, body string) *pluginv1.ClassifyErrorResponse {
		return p.ClassifyAt(now, &pluginv1.ClassifyErrorRequest{Account: relayAccount, Meta: &pluginv1.RequestMeta{Protocol: protocol},
			Status: status, Headers: headers, BodyPrefix: []byte(body)})
	}
	if got := classifyAs(anthropic.ProtocolMessages, anthropic.StatusOverloaded, nil, ""); got.CooldownUntilUnix != now.Add(30*time.Second).Unix() {
		t.Errorf("anthropic 529: %v", got)
	}
	if got := classifyAs(openai.ProtocolChat, 429, nil, `{"error":{"code":"insufficient_quota"}}`); got.AccountEffect != pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_DISABLE {
		t.Errorf("openai insufficient_quota: %v", got)
	}
	if got := classifyAs(typesafe.ProtocolSystemOne, 429, map[string]string{"retry-after": "5"}, `{"detail":"slow down"}`); got.CooldownUntilUnix != now.Add(5*time.Second).Unix() {
		t.Errorf("typesafe 429: %v", got)
	}
	if got, want := classifyAs("", 404, nil, `{"error":{"message":"m"}}`), openai.Policy.Classify(now, &pluginv1.ClassifyErrorRequest{Status: 404, BodyPrefix: []byte(`{"error":{"message":"m"}}`)}); got.ClientErrorType != want.ClientErrorType || got.ClientMessage != "m" {
		t.Errorf("no protocol: %v, want %v", got, want)
	}
}

// A route's own Auth and Classify override the type's on that route only;
// the model listing keeps the type's auth.
func TestRouteAuthAndClassify(t *testing.T) {
	ty := openai.APIKey("apikey")
	r := ty.Routes[openai.ProtocolEmbeddings]
	r.Auth = &standard.Auth{Header: "api-key"}
	r.Classify = &classify.Policy{Rules: []classify.Rule{{When: classify.Status(http.StatusTeapot), Effect: classify.Disable, Reason: "teapot"}}}
	ty.Routes[openai.ProtocolEmbeddings] = r
	p := standard.New(ty)
	acc := &pluginv1.Account{Type: "apikey", CredentialsJson: `{"api_key":"sk-0123456789"}`}
	build := func(protocol string) map[string]string {
		out, err := p.BuildUpstream(context.Background(), &pluginv1.ExecuteRequest{Account: acc, Meta: &pluginv1.RequestMeta{Protocol: protocol}, Body: []byte(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		return out.Headers
	}
	if got := build(openai.ProtocolEmbeddings); !reflect.DeepEqual(got, map[string]string{"api-key": "sk-0123456789"}) {
		t.Errorf("embeddings: %v", got)
	}
	if got := build(openai.ProtocolChat); !reflect.DeepEqual(got, map[string]string{"authorization": "Bearer sk-0123456789"}) {
		t.Errorf("chat: %v", got)
	}
	teapot := func(protocol string) pluginv1.ClassifyErrorResponse_AccountEffect {
		return p.ClassifyAt(time.Now(), &pluginv1.ClassifyErrorRequest{Account: acc, Meta: &pluginv1.RequestMeta{Protocol: protocol}, Status: http.StatusTeapot}).AccountEffect
	}
	if teapot(openai.ProtocolEmbeddings) != pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_DISABLE || teapot(openai.ProtocolChat) != pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_UNSPECIFIED {
		t.Error("the route's policy is not the embeddings route's only")
	}
	models, err := p.BuildModelsRequest(context.Background(), &pluginv1.BuildModelsRequestRequest{Account: acc})
	if err != nil || !reflect.DeepEqual(models.Headers, map[string]string{"authorization": "Bearer sk-0123456789"}) {
		t.Errorf("models: %v %v", models.GetHeaders(), err)
	}
	for name, edit := range map[string]func(*standard.Route){
		"upper-case route auth": func(r *standard.Route) { r.Auth = &standard.Auth{Header: "Api-Key"} },
		"blocked route auth":    func(r *standard.Route) { r.Auth = &standard.Auth{Header: "host"} },
	} {
		bad := openai.APIKey("apikey")
		r := bad.Routes[openai.ProtocolChat]
		edit(&r)
		bad.Routes[openai.ProtocolChat] = r
		if err := standard.Check(bad); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// The console's test follows the protocols the core lists: the first one
// with a test, else Test; usage_protocol says which. Every test of every
// preset is pinned here, with what goes to the upstream: method, address,
// credential header and body (CONTRACTS §122).
func TestTestsByProtocol(t *testing.T) {
	p := standard.New(relaySite())
	const (
		bearer = "authorization"
		chat   = `{"max_tokens":1,"messages":[{"content":"ping","role":"user"}],"model":"gpt-4o-mini"}`
		gen    = `{"contents":[{"parts":[{"text":"ping"}],"role":"user"}],"generationConfig":{"maxOutputTokens":1}}`
	)
	for _, c := range []struct {
		protocols []string
		model     string
		want      string
		url       string
		header    string
		body      string
	}{
		{nil, "", openai.ProtocolChat, "https://relay.example.com/v1/chat/completions", bearer, chat},
		{[]string{openai.ProtocolChat, anthropic.ProtocolMessages}, "", openai.ProtocolChat, "https://relay.example.com/v1/chat/completions", bearer, chat},
		{[]string{openai.ProtocolResponses, openai.ProtocolChat}, "", openai.ProtocolResponses, "https://relay.example.com/v1/responses", bearer,
			`{"input":"ping","max_output_tokens":16,"model":"gpt-4o-mini"}`},
		{[]string{openai.ProtocolEmbeddings}, "", openai.ProtocolEmbeddings, "https://relay.example.com/v1/embeddings", bearer,
			`{"input":"ping","model":"text-embedding-3-small"}`},
		{[]string{openai.ProtocolImages}, "gpt-image-2", openai.ProtocolImages, "https://relay.example.com/v1/images/generations", bearer,
			`{"model":"gpt-image-2","n":1,"prompt":"a small white dot","quality":"low","size":"1024x1024"}`},
		{[]string{anthropic.ProtocolMessages}, "", anthropic.ProtocolMessages, "https://relay.example.com/v1/messages", "x-api-key",
			`{"max_tokens":1,"messages":[{"content":"ping","role":"user"}],"model":"claude-haiku-4-5"}`},
		{[]string{gemini.ProtocolGenerate, gemini.ProtocolStreamGenerate}, "models/g-2", gemini.ProtocolGenerate, "https://relay.example.com/v1beta/models/g-2:generateContent", "x-goog-api-key", gen},
		{[]string{gemini.ProtocolStreamGenerate}, "", gemini.ProtocolStreamGenerate,
			"https://relay.example.com/v1beta/models/gemini-2.5-flash-lite:streamGenerateContent?alt=sse", "x-goog-api-key", gen},
		{[]string{typesafe.ProtocolSystemOne}, "", typesafe.ProtocolSystemOne, "https://relay.example.com/v1/systemone", bearer, typesafe.PingBody("jev-latest")},
	} {
		out, err := p.BuildTestRequest(context.Background(), &pluginv1.BuildTestRequestRequest{Account: relayAccount, Model: c.model, Protocols: c.protocols})
		if err != nil {
			t.Fatal(err)
		}
		key := "site-key-0123"
		if c.header == bearer {
			key = "Bearer " + key
		}
		if out.UsageProtocol != c.want || out.Method != http.MethodPost || out.Url != c.url || out.Headers[c.header] != key ||
			out.Headers["content-type"] != "application/json" || out.BodyJson != c.body || out.Model == "" {
			t.Errorf("%v: %s %s %s %v %s model %q", c.protocols, out.UsageProtocol, out.Method, out.Url, out.Headers, out.BodyJson, out.Model)
		}
	}
	// Protocols none of which has a test (an edit) fail: never the default
	// test instead (CONTRACTS §122.3).
	if _, err := p.BuildTestRequest(context.Background(), &pluginv1.BuildTestRequestRequest{Account: relayAccount,
		Protocols: []string{openai.ProtocolImagesEdit}}); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("an edit only: %v", err)
	}
	for name, edit := range map[string]func(*standard.Type){
		"tests without Test":      func(t *standard.Type) { t.Test = nil },
		"a protocol tested twice": func(t *standard.Type) { t.Tests = append(t.Tests, t.Tests[0]) },
		"a test without a route": func(t *standard.Type) {
			t.Tests = append(t.Tests, standard.Test{Protocol: "x", Model: "m", Body: typesafe.PingBody})
		},
	} {
		ty := relaySite()
		edit(&ty)
		if err := standard.Check(ty); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
