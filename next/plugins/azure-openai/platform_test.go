package main

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest/conformance"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/openai"

	"github.com/Sub2API-Devs/sup2api/next/plugins/azure-openai/internal/azureopenai"
)

// azure_openai has no golden file: these tests are its acceptance, run
// against the platform the plugin serves.

const (
	typeV1         = azureopenai.TypeV1
	typeDeployment = azureopenai.TypeDeployment
	resource       = "https://res.openai.azure.com"
	apiKey         = "0123456789abcdef0123456789abcdef" // an Azure key has no prefix
)

// platform is the plugin's platform, shared by the tests (it is stateless).
var platform = azureopenai.New()

// TestRequests covers every route of both types: the URL on the account's
// resource endpoint, the key in api-key (never Authorization), the client
// headers that are forwarded (never OpenAI-Organization / OpenAI-Project,
// which the manifest does not pass), the stream usage of chat, and the
// protocols a type does not serve.
func TestRequests(t *testing.T) {
	const deployment = "prod/gpt 4o" // a deployment name is escaped as one path segment
	for _, tc := range []struct {
		accountType, protocol string
		url                   string // "" = the type has no route for the protocol
	}{
		{typeV1, openai.ProtocolChat, resource + "/openai/v1/chat/completions"},
		{typeV1, openai.ProtocolResponses, resource + "/openai/v1/responses"},
		{typeV1, openai.ProtocolEmbeddings, resource + "/openai/v1/embeddings"},
		{typeV1, openai.ProtocolResponsesWS, ""},
		{typeDeployment, openai.ProtocolChat, resource + "/openai/deployments/prod%2Fgpt%204o/chat/completions?api-version=2025-04-01-preview"},
		{typeDeployment, openai.ProtocolEmbeddings, resource + "/openai/deployments/prod%2Fgpt%204o/embeddings?api-version=2025-04-01-preview"},
		{typeDeployment, openai.ProtocolResponses, ""},
		{typeDeployment, openai.ProtocolResponsesWS, ""},
	} {
		for _, stream := range []bool{false, true} {
			body := `{"messages":[]}`
			out, err := platform.BuildUpstream(context.Background(), &pluginv1.ExecuteRequest{
				Meta:    &pluginv1.RequestMeta{Protocol: tc.protocol, Model: deployment, Stream: stream},
				Account: account(tc.accountType, resource, `"api_version":"2025-04-01-preview"`),
				InboundHeaders: coreFiltered(t, tc.accountType, map[string]string{
					"user-agent": "cli/1", "openai-beta": "assistants=v2", "x-stainless-lang": "python",
					"openai-organization": "org-client", "openai-project": "proj_client", "authorization": "Bearer sk-client",
				}),
				Body: []byte(body),
			})
			if tc.url == "" {
				if err == nil {
					t.Errorf("%s %s: built %s, want no route", tc.accountType, tc.protocol, out.Url)
				}
				continue
			}
			if err != nil {
				t.Errorf("%s %s: %v", tc.accountType, tc.protocol, err)
				continue
			}
			if out.Method != "POST" || out.Url != tc.url || out.UpstreamModel != deployment {
				t.Errorf("%s %s: %s %s model %q, want POST %s", tc.accountType, tc.protocol, out.Method, out.Url, out.UpstreamModel, tc.url)
			}
			wantDefault := map[string]string{"content-type": "application/json", "user-agent": "cli/1", "openai-beta": "assistants=v2", "x-stainless-lang": "python"}
			if !reflect.DeepEqual(out.Headers, map[string]string{"api-key": apiKey}) || !reflect.DeepEqual(out.DefaultHeaders, wantDefault) {
				t.Errorf("%s %s: headers %v default %v", tc.accountType, tc.protocol, out.Headers, out.DefaultHeaders)
			}
			if stream && tc.protocol == openai.ProtocolChat {
				body = `{"messages":[],"stream_options":{"include_usage":true}}`
			}
			if string(out.Body) != body {
				t.Errorf("%s %s stream=%v: body %s, want %s", tc.accountType, tc.protocol, stream, out.Body, body)
			}
		}
	}
}

// TestBodyFilter pins the request fields removed before a request is sent:
// the openai preset's (service_tier and safety_identifier, which belong to
// the client's own OpenAI account). The plugin removes them after the
// operator's rewrites (design §12.5), so a parameter override that sets one
// loses it too. Everything else passes byte for byte.
func TestBodyFilter(t *testing.T) {
	for _, accountType := range []string{typeV1, typeDeployment} {
		out, err := platform.BuildUpstream(context.Background(), &pluginv1.ExecuteRequest{
			Meta:    &pluginv1.RequestMeta{Protocol: openai.ProtocolEmbeddings, Model: "d"},
			Account: account(accountType, resource, `"api_version":"2024-10-21"`),
			Body:    []byte(`{"model":"d","service_tier":"priority","safety_identifier":"u1","user":"u1","messages":[]}`),
		})
		if err != nil || string(out.Body) != `{"model":"d","user":"u1","messages":[]}` {
			t.Errorf("%s: body %s, %v", accountType, out.GetBody(), err)
		}
	}
}

// A classic route names the deployment in its path, so a request without a
// model is refused rather than sent to /openai/deployments//...
func TestDeploymentModelRequired(t *testing.T) {
	_, err := platform.BuildUpstream(context.Background(), &pluginv1.ExecuteRequest{
		Meta:    &pluginv1.RequestMeta{Protocol: openai.ProtocolChat},
		Account: account(typeDeployment, resource, `"api_version":"2024-10-21"`),
		Body:    []byte(`{}`),
	})
	if err == nil || !strings.Contains(err.Error(), "model is required") {
		t.Fatalf("error %v, want model is required", err)
	}
}

// Resource endpoints of the other two Azure address forms.
const (
	cognitive = "https://res.cognitiveservices.azure.com"
	project   = "https://res.services.ai.azure.com/api/projects/p"
)

// endpoint is what an accepted base_url input becomes: the value saved
// (normalized: trimmed, the scheme lower-cased, no trailing "/", the path as
// written) and the root every route's full path is appended to (the saved
// value without a trailing /openai/v1 or /openai, Spec.Versions).
type endpoint struct{ saved, root string }

// acceptedEndpoints are the base_url inputs the types accept, each with
// what it becomes: the inputs with a trailing "/" after /openai or a space
// only once normalized (the forms refuse them as typed). The roots, and so
// the request URLs, are those of the endpoint without the suffix.
var acceptedEndpoints = map[string]endpoint{
	resource:                           {resource, resource},
	resource + "/":                     {resource, resource},
	resource + "/openai":               {resource + "/openai", resource},
	resource + "/openai/":              {resource + "/openai", resource},
	resource + "/openai/v1":            {resource + "/openai/v1", resource},
	resource + "/openai/v1/":           {resource + "/openai/v1", resource},
	cognitive:                          {cognitive, cognitive},
	cognitive + "/openai/v1":           {cognitive + "/openai/v1", cognitive},
	project:                            {project, project},
	project + "/":                      {project, project},
	"https://RES.OpenAI.Azure.com":     {"https://RES.OpenAI.Azure.com", "https://RES.OpenAI.Azure.com"},
	"http://mock-upstream:8080":        {"http://mock-upstream:8080", "http://mock-upstream:8080"},
	"http://mock-upstream:8080/openai": {"http://mock-upstream:8080/openai", "http://mock-upstream:8080"},
	resource + "/openai/v1//":          {resource + "/openai/v1", resource},
	"HTTPS://res.openai.azure.com":     {resource, resource},
	" " + resource:                     {resource, resource},
	resource + " ":                     {resource, resource},
	// A project name with an escaped "/" stays one segment, in the saved
	// value and in every request URL.
	"https://res.services.ai.azure.com/api/projects/a%2Fb": {"https://res.services.ai.azure.com/api/projects/a%2Fb", "https://res.services.ai.azure.com/api/projects/a%2Fb"},
	// A project named openai: its name is taken for the /openai suffix, so
	// requests go to .../api/projects/openai/v1/... without the project's
	// own segment, as before the base URL was saved as written. Accepted as
	// it is (design Q11): telling the two apart needs an azure-only
	// exception.
	"https://res.services.ai.azure.com/api/projects/openai": {"https://res.services.ai.azure.com/api/projects/openai", "https://res.services.ai.azure.com/api/projects"},
}

// TestResourceEndpoint covers the account's base_url: it is required (there
// is no official address to fall back to), saved as written once
// normalized, and requests are built on its root.
func TestResourceEndpoint(t *testing.T) {
	ctx := context.Background()
	for _, accountType := range []string{typeV1, typeDeployment} {
		for in, e := range acceptedEndpoints {
			assertEndpoint(t, accountType, in, e)
		}

		acc := account(accountType, "", formFields(accountType)...)
		res, _ := platform.ValidateCredentials(ctx, &pluginv1.ValidateCredentialsRequest{AccountType: accountType, CredentialsJson: acc.CredentialsJson, SettingsJson: acc.SettingsJson})
		if len(res.GetErrors()) != 1 || res.Errors[0].Field != "base_url" || res.Errors[0].Code != "required" || res.Errors[0].Message != "Resource endpoint is required / 请填写资源地址" {
			t.Errorf("%s without base_url: validate errors %v", accountType, res.GetErrors())
		}
		if _, err := platform.BuildUpstream(ctx, &pluginv1.ExecuteRequest{Meta: &pluginv1.RequestMeta{Protocol: openai.ProtocolChat, Model: "d"}, Account: acc}); err == nil {
			t.Errorf("%s without base_url: built a request", accountType)
		}
	}
}

// testPaths are the account test's paths (its default deployment) on each
// type, appended to the root.
var testPaths = map[string]string{
	typeV1:         "/openai/v1/chat/completions",
	typeDeployment: "/openai/deployments/gpt-4o-mini/chat/completions?api-version=2024-10-21",
}

// assertEndpoint checks that base_url in validates without errors, is saved
// as e.saved and that the account saved so gives its account test request
// on e.root.
func assertEndpoint(t *testing.T, accountType, in string, e endpoint) {
	t.Helper()
	ctx := context.Background()
	acc := account(accountType, in, formFields(accountType)...)
	res, err := platform.ValidateCredentials(ctx, &pluginv1.ValidateCredentialsRequest{AccountType: accountType, CredentialsJson: acc.CredentialsJson, SettingsJson: acc.SettingsJson})
	var saved struct {
		BaseURL string `json:"base_url"`
	}
	_ = json.Unmarshal([]byte(res.GetNormalizedSettingsJson()), &saved)
	if err != nil || len(res.GetErrors()) != 0 || saved.BaseURL != e.saved {
		t.Errorf("%s %q: validate %v %v, saved %q, want %s", accountType, in, err, res.GetErrors(), saved.BaseURL, e.saved)
	}
	out, err := platform.BuildTestRequest(ctx, &pluginv1.BuildTestRequestRequest{Account: account(accountType, saved.BaseURL, formFields(accountType)...)})
	if want := e.root + testPaths[accountType]; err != nil || out.Url != want {
		t.Errorf("%s %q: test request %s (%v), want %s", accountType, in, out.GetUrl(), err, want)
	}
}

// TestAccountTest covers the console's account test (the chat route with
// the tested deployment, gpt-4o-mini when none is named) and the model
// listing the types do not have.
func TestAccountTest(t *testing.T) {
	ctx := context.Background()
	for accountType, want := range map[string]map[string]string{
		typeV1:         {"": resource + "/openai/v1/chat/completions", "my-gpt": resource + "/openai/v1/chat/completions"},
		typeDeployment: {"": resource + "/openai/deployments/gpt-4o-mini/chat/completions?api-version=2024-10-21", "my-gpt": resource + "/openai/deployments/my-gpt/chat/completions?api-version=2024-10-21"},
	} {
		acc := account(accountType, resource, `"api_version":"2024-10-21"`)
		for model, url := range want {
			out, err := platform.BuildTestRequest(ctx, &pluginv1.BuildTestRequestRequest{Account: acc, Model: model})
			if err != nil {
				t.Fatalf("%s %q: %v", accountType, model, err)
			}
			wantHeaders := map[string]string{"api-key": apiKey, "content-type": "application/json"}
			if out.Method != "POST" || out.Url != url || !reflect.DeepEqual(out.Headers, wantHeaders) || out.UsageProtocol != openai.ProtocolChat {
				t.Errorf("%s %q: %s %s %v usage %s, want POST %s", accountType, model, out.Method, out.Url, out.Headers, out.UsageProtocol, url)
			}
		}
		if _, err := platform.BuildModelsRequest(ctx, &pluginv1.BuildModelsRequestRequest{Account: acc}); err == nil {
			t.Errorf("%s: a model listing request was built", accountType)
		}
	}
}

// TestClassify pins the openai policy as it is, with no Azure rules.
func TestClassify(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	const (
		toClient = pluginv1.ClassifyErrorResponse_ACTION_RETURN_TO_CLIENT
		failover = pluginv1.ClassifyErrorResponse_ACTION_FAILOVER
		none     = pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_UNSPECIFIED
		cooldown = pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_COOLDOWN
		disable  = pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_DISABLE
	)
	for _, tc := range []struct {
		name    string
		status  int32
		headers map[string]string
		body    string
		action  pluginv1.ClassifyErrorResponse_Action
		effect  pluginv1.ClassifyErrorResponse_AccountEffect
		seconds int64 // cooldown from now
	}{
		{"content filter", 400, nil, `{"error":{"code":"content_filter","message":"The response was filtered"}}`, toClient, none, 0},
		{"bad request", 400, nil, `{"error":{"code":"BadRequest","message":"bad"}}`, toClient, none, 0},
		{"deployment not found", 404, nil, `{"error":{"code":"DeploymentNotFound","message":"no such deployment"}}`, toClient, none, 0},
		{"bad key", 401, nil, `{"error":{"code":"401","message":"Access denied due to invalid subscription key"}}`, failover, disable, 0},
		{"forbidden", 403, nil, `{"error":{"code":"403","message":"forbidden"}}`, failover, disable, 0},
		{"rate limit, retry-after-ms", 429, map[string]string{"retry-after-ms": "30000", "retry-after": "31"}, `{"error":{"code":"429","message":"slow down"}}`, failover, cooldown, 30},
		{"rate limit, retry-after", 429, map[string]string{"retry-after": "7"}, `{"error":{"code":"429","message":"slow down"}}`, failover, cooldown, 7},
		{"rate limit, no header", 429, nil, `{"error":{"code":"429","message":"slow down"}}`, failover, cooldown, 60},
		{"timeout", 408, nil, ``, failover, cooldown, 10},
		{"server error", 500, nil, `{"error":{"code":"InternalServerError","message":"oops"}}`, failover, cooldown, 10},
		{"unavailable", 503, nil, ``, failover, cooldown, 10},
	} {
		for _, accountType := range []string{typeV1, typeDeployment} {
			out := platform.ClassifyAt(now, &pluginv1.ClassifyErrorRequest{
				Meta:    &pluginv1.RequestMeta{Protocol: openai.ProtocolChat},
				Account: &pluginv1.Account{Type: accountType},
				Status:  tc.status, Headers: tc.headers, BodyPrefix: []byte(tc.body),
			})
			until := int64(0)
			if tc.seconds > 0 {
				until = now.Unix() + tc.seconds
			}
			if out.Action != tc.action || out.AccountEffect != tc.effect || out.CooldownUntilUnix != until {
				t.Errorf("%s %s: %v %v until %d, want %v %v until %d", accountType, tc.name, out.Action, out.AccountEffect, out.CooldownUntilUnix, tc.action, tc.effect, until)
			}
		}
	}
}

// TestConformance runs the platform conformance suite with the embedded
// manifest: both types, each protocol a type serves in each form the core
// sends it (embeddings never stream). The key in api-key is the only
// authoritative header.
func TestConformance(t *testing.T) {
	var cases []conformance.Case
	served := map[string][]string{
		typeV1:         {openai.ProtocolChat, openai.ProtocolResponses, openai.ProtocolEmbeddings},
		typeDeployment: {openai.ProtocolChat, openai.ProtocolEmbeddings},
	}
	for _, accountType := range []string{typeV1, typeDeployment} {
		protocols := served[accountType]
		acc := account(accountType, resource, `"api_version":"2024-10-21"`)
		for _, protocol := range protocols {
			for _, stream := range []bool{false, true} {
				if stream && protocol == openai.ProtocolEmbeddings {
					continue
				}
				body := `{"model":"gpt-4o","input":"hi","messages":[{"role":"user","content":"hi"}]}`
				name := accountType + " " + protocol + " non-stream"
				if stream {
					body = `{"model":"gpt-4o","stream":true,"input":"hi","messages":[{"role":"user","content":"hi"}]}`
					name = accountType + " " + protocol + " stream"
				}
				cases = append(cases, conformance.Case{
					Name:    name,
					Account: acc,
					Meta:    &pluginv1.RequestMeta{Protocol: protocol, Model: "gpt-4o", Stream: stream},
					Body:    []byte(body),
				})
			}
		}
	}
	conformance.Run(t, conformance.Plugin{
		Platform:    platform,
		Manifest:    manifestJSON,
		Protocols:   []string{openai.ProtocolChat, openai.ProtocolResponses, openai.ProtocolEmbeddings},
		Credentials: []string{"api-key"},
	}, cases...)
}

// coreFiltered is what the core hands the plugin of the client's headers:
// those the manifest passes for accountType.
func coreFiltered(t *testing.T, accountType string, client map[string]string) map[string]string {
	t.Helper()
	m := load(t)
	i := slices.IndexFunc(m.AccountTypes, func(at manifest.AccountType) bool { return at.ID == accountType })
	out := map[string]string{}
	for _, name := range m.AccountTypes[i].Platforms[0].PassHeaders {
		if value, ok := client[name]; ok {
			out[name] = value
		}
	}
	return out
}

// formFields are the account form's fields of the type besides api_key and
// base_url (the type refuses any other).
func formFields(accountType string) []string {
	if accountType == typeDeployment {
		return []string{`"api_version":"2024-10-21"`}
	}
	return nil
}

// account is an account of accountType on baseURL ("" = not set) with
// extra settings members.
func account(accountType, baseURL string, settings ...string) *pluginv1.Account {
	if baseURL != "" {
		b, _ := json.Marshal(baseURL)
		settings = append(settings, `"base_url":`+string(b))
	}
	return &pluginv1.Account{
		Id: 1, Type: accountType,
		CredentialsJson: `{"api_key":"` + apiKey + `"}`,
		SettingsJson:    "{" + strings.Join(settings, ",") + "}",
	}
}
