package standard_test

import (
	"context"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/gemini"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/openai"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard"
)

// custom covers what no preset uses: account fields in a header and in the
// query, a header from a client header.
func custom() standard.Type {
	t := gemini.APIKey("custom")
	t.Spec.DefaultBaseURL, t.Spec.RequireBaseURL = "", true
	t.Headers = append(t.Headers,
		standard.Set("x-org", standard.Field("org")),
		standard.Set("x-n", standard.Field("n")),
		standard.Set("x-flag", standard.Field("flag")),
		standard.Set("x-none", standard.Field("absent")),
		standard.Default("x-ua", func(r *standard.Request) string { return "s2a/" + r.Client["user-agent"] }))
	t.Routes["custom.versioned"] = standard.Route{
		Path: func(r *standard.Request) string {
			return "/v/" + url.PathEscape(r.Model) + "?v=" + url.QueryEscape(r.Field("version"))
		},
		Model: standard.Model{Required: true},
	}
	return t
}

func TestFields(t *testing.T) {
	p := standard.New(custom())
	acc := &pluginv1.Account{Id: 7, Type: "custom", CredentialsJson: `{"api_key":"k-123456789","org":"o-1","n":3}`, SettingsJson: `{"base_url":"https://g.example.com","flag":true,"version":"a&b"}`}
	for protocol, want := range map[string]string{
		gemini.ProtocolStreamGenerate: "https://g.example.com/v1beta/models/a%20b:streamGenerateContent?alt=sse",
		gemini.ProtocolGenerate:       "https://g.example.com/v1beta/models/a%20b:generateContent",
		"custom.versioned":            "https://g.example.com/v/a%20b?v=a%26b",
	} {
		out, err := p.BuildUpstream(context.Background(), &pluginv1.ExecuteRequest{
			Meta: &pluginv1.RequestMeta{Protocol: protocol, Model: "models/a b"}, Account: acc,
			// What the core hands over goes on as default headers: every
			// header (the core filtered them), lower-case, trimmed, blank
			// ones dropped.
			Body: []byte(`{"model":"a b"}`), InboundHeaders: map[string]string{"user-agent": " cli/1 ", "X-Client": "c", "x-blank": " "},
		})
		if err != nil {
			t.Fatal(err)
		}
		wantHeaders := map[string]string{"content-type": "application/json", "x-org": "o-1", "x-n": "3", "x-flag": "true", "user-agent": "cli/1", "x-client": "c", "x-ua": "s2a/cli/1"}
		if out.Url != want || !reflect.DeepEqual(out.Headers, map[string]string{"x-goog-api-key": "k-123456789"}) || !reflect.DeepEqual(out.DefaultHeaders, wantHeaders) {
			t.Errorf("%s: %s %v %v, want %s %v", protocol, out.Url, out.Headers, out.DefaultHeaders, want, wantHeaders)
		}
	}
	models, err := p.BuildModelsRequest(context.Background(), &pluginv1.BuildModelsRequestRequest{Account: acc})
	if err != nil || models.Url != "https://g.example.com/v1beta/models?pageSize=1000" || models.Headers["content-type"] != "" || models.Headers["x-goog-api-key"] != "k-123456789" {
		t.Fatalf("models %v, %v", models, err)
	}
}

func TestWebSocketHandshake(t *testing.T) {
	ty := openai.APIKey("apikey")
	ty.FilterFields = []string{"service_tier"}
	p := standard.New(ty)
	for inbound, want := range map[string]string{
		"":                                "responses_websockets=2026-02-06",
		"assistants=v2":                   "assistants=v2,responses_websockets=2026-02-06",
		"responses_websockets=2025-01-01": "responses_websockets=2025-01-01",
	} {
		out, err := p.BuildUpstream(context.Background(), &pluginv1.ExecuteRequest{
			Meta:           &pluginv1.RequestMeta{Protocol: openai.ProtocolResponsesWS, Model: "gpt-x", Stream: true},
			Account:        &pluginv1.Account{Type: "apikey", CredentialsJson: `{"api_key":"sk-0123456789"}`},
			InboundHeaders: map[string]string{"openai-beta": inbound},
			Body:           []byte(`{"type":"response.create","service_tier":"flex"}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		if out.Method != "GET" || out.Url != "wss://api.openai.com/v1/responses" || out.DefaultHeaders["openai-beta"] != want ||
			out.DefaultHeaders["content-type"] != "" || out.Headers["authorization"] != "Bearer sk-0123456789" {
			t.Errorf("%q: %s %s %v %v", inbound, out.Method, out.Url, out.Headers, out.DefaultHeaders)
		}
		if string(out.Body) != `{"type":"response.create"}` { // a turn's message is filtered like a body
			t.Errorf("%q: message %s", inbound, out.Body)
		}
	}
}

// Two types: each account is served by its own, an account without a type
// is refused (it could be either), an unknown type is answered by the
// first type's wording.
func TestSeveralTypes(t *testing.T) {
	a, b := openai.APIKey("a"), openai.APIKey("b")
	b.Auth = standard.Auth{Header: "api-key"}
	p := standard.New(a, b)
	build := func(accountType string) (*pluginv1.UpstreamRequest, error) {
		return p.BuildUpstream(context.Background(), &pluginv1.ExecuteRequest{
			Meta:    &pluginv1.RequestMeta{Protocol: openai.ProtocolChat},
			Account: &pluginv1.Account{Id: 3, Type: accountType, CredentialsJson: `{"api_key":"sk-0123456789"}`},
			Body:    []byte(`{}`),
		})
	}
	if out, err := build("b"); err != nil || out.Headers["api-key"] != "sk-0123456789" || out.Headers["authorization"] != "" {
		t.Errorf("b: %v %v", out.GetHeaders(), err)
	}
	if out, err := build("a"); err != nil || out.Headers["authorization"] == "" {
		t.Errorf("a: %v %v", out.GetHeaders(), err)
	}
	for _, typ := range []string{"", "c"} {
		if _, err := build(typ); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), `unsupported account type "`+typ+`"`) {
			t.Errorf("type %q: %v", typ, err)
		}
	}
	res, _ := p.ValidateCredentials(context.Background(), &pluginv1.ValidateCredentialsRequest{AccountType: "c", CredentialsJson: `{"api_key":"sk-0123456789"}`})
	if len(res.Errors) != 1 || res.Errors[0].Message != `unsupported account type "c" / 不支持的账号类型` {
		t.Errorf("validate c: %v", res.Errors)
	}
	res, _ = p.ValidateCredentials(context.Background(), &pluginv1.ValidateCredentialsRequest{AccountType: "b", CredentialsJson: `{"api_key":"no-prefix-key"}`})
	if len(res.Errors) != 0 {
		t.Errorf("validate b: %v", res.Errors)
	}
}

func TestWithoutTestOrModels(t *testing.T) {
	ty := openai.APIKey("apikey")
	ty.Test, ty.Tests, ty.Models = nil, nil, nil
	p := standard.New(ty)
	acc := &pluginv1.Account{Type: "apikey", CredentialsJson: `{"api_key":"sk-0123456789"}`}
	if _, err := p.BuildTestRequest(context.Background(), &pluginv1.BuildTestRequestRequest{Account: acc}); status.Code(err) != codes.Unimplemented {
		t.Errorf("test: %v", err)
	}
	if _, err := p.BuildModelsRequest(context.Background(), &pluginv1.BuildModelsRequestRequest{Account: acc}); status.Code(err) != codes.Unimplemented {
		t.Errorf("models: %v", err)
	}
}

func TestCheck(t *testing.T) {
	for name, edit := range map[string]func(*standard.Type){
		"no id":                  func(t *standard.Type) { t.ID = "" },
		"no base url":            func(t *standard.Type) { t.Spec.DefaultBaseURL = "" },
		"bad default base url":   func(t *standard.Type) { t.Spec.DefaultBaseURL = "api.example.com" },
		"upper-case auth header": func(t *standard.Type) { t.Auth.Header = "Authorization" },
		"blocked auth header":    func(t *standard.Type) { t.Auth.Header = "host" },
		"credential as a header": func(t *standard.Type) { t.Headers = append(t.Headers, standard.Set("x-api-key", standard.Fixed("k"))) },
		"header without value":   func(t *standard.Type) { t.Headers = append(t.Headers, standard.Header{Name: "x-a"}) },
		"route header upper-case": func(t *standard.Type) {
			r := t.Routes[openai.ProtocolChat]
			r.Headers = []standard.Header{standard.Set("X-A", standard.Fixed("1"))}
			t.Routes[openai.ProtocolChat] = r
		},
		"no routes":          func(t *standard.Type) { t.Routes = nil },
		"route without path": func(t *standard.Type) { t.Routes["x"] = standard.Route{} },
		"edit on a websocket": func(t *standard.Type) {
			r := t.Routes[openai.ProtocolResponsesWS]
			r.Edit = openai.IncludeStreamUsage
			t.Routes[openai.ProtocolResponsesWS] = r
		},
		"default protocol unknown": func(t *standard.Type) { t.DefaultProtocol = "x" },
		"empty filter field":       func(t *standard.Type) { t.FilterFields = []string{""} },
		"test on a websocket":      func(t *standard.Type) { t.Test.Protocol = openai.ProtocolResponsesWS },
		"test without model":       func(t *standard.Type) { t.Test.Model = " " },
		"models without ids path":  func(t *standard.Type) { t.Models = &apikey.ModelList{Path: "/m"} },
	} {
		ty := openai.APIKey("apikey")
		edit(&ty)
		if err := standard.Check(ty); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if err := standard.Check(openai.APIKey("a"), openai.APIKey("a")); err == nil {
		t.Error("an id used twice: no error")
	}
	defer func() {
		if recover() == nil {
			t.Error("New accepted an inconsistent type")
		}
	}()
	standard.New(standard.Type{ID: "x"})
}

const manifest = `{
  "apiVersion": 1, "key": "std", "version": "0.1.0", "publisher": "test", "runtime": "grpc",
  "capabilities": [{"id": "platform.adapter.v1"}],
  "hostPermissions": [{"id": "platform.register"}],
  "externalServices": ["api.openai.com"],
  "accountTypes": [{"id": "apikey", "label": {"en": "API key"}, "form": {"mode": "native", "component": "KeyForm"}, "platforms": [{"platform": "openai"}]}]
}`

// Through the fake host: one relay, not waited for, what BuildUpstream
// builds, answered with host_usage.
func TestExecute(t *testing.T) {
	p := standard.New(openai.APIKey("apikey"))
	h := pluginsdktest.Start(t, p, pluginsdktest.Options{Manifest: []byte(manifest)})
	in := &pluginv1.ExecuteRequest{
		Meta:    &pluginv1.RequestMeta{Protocol: openai.ProtocolChat, Model: "gpt-x", Stream: true},
		Account: &pluginv1.Account{Id: 1, Type: "apikey", CredentialsJson: `{"api_key":"sk-0123456789"}`},
		Body:    []byte(`{"model":"gpt-x","stream":true}`),
	}
	ex := h.Execute(context.Background(), in)
	relay := ex.Relayed(t)
	want, err := p.BuildUpstream(context.Background(), in)
	if err != nil || relay.Wait || !proto.Equal(relay.Request, want) || !proto.Equal(ex.Response, pluginsdk.ReportHostUsage()) {
		t.Fatalf("relay %v wait %v, response %v; built %v %v", relay.Request, relay.Wait, ex.Response, want, err)
	}
}
