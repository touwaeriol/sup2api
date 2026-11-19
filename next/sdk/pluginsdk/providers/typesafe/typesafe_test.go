package typesafe_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/typesafe"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard"
)

// skeleton is a minimal package declaring the platform and a type on it.
const skeleton = `{
  "apiVersion": 1, "key": "typesafe_check", "name": {"en": "TypeSafe"}, "version": "0.1.0", "publisher": "test",
  "icon": "text:T", "runtime": "grpc", "entry": {"grpc": {"binaries": "runtimes/{os}-{arch}/plugin"}}, "hostCompat": ">=0.1.304 <0.2.0",
  "hostUICompat": "^4.0", "ui": {"native": {"entry": "ui/native/entry.js"}},
  "capabilities": [{"id": "platform.adapter.v1"}],
  "externalServices": ["api.typesafe.ai"],
  "accountTypes": [{"id": "apikey", "label": {"en": "API key"}, "upstreamFields": ["base_url"],
    "form": {"mode": "native", "component": "TypeSafeKeyForm"},
    "sensitiveFields": ["api_key"], "settingsFields": ["base_url"],
    "platforms": [{"platform": "typesafe"}]}],
  "hostPermissions": [
    {"id": "platform.register", "reason": {"en": "r"}},
    {"id": "gateway.endpoint", "reason": {"en": "r"}},
    {"id": "ui.native", "reason": {"en": "r"}}
  ]
}`

// The platform passes the checks the core runs on a package that declares
// it, and is what the design states: one non-streaming JSON endpoint whose
// usage the host reads (so another plugin's type may serve it).
func TestPlatform(t *testing.T) {
	var m manifest.Manifest
	if err := json.Unmarshal([]byte(skeleton), &m); err != nil {
		t.Fatal(err)
	}
	m.Platforms = []manifest.Platform{typesafe.Platform()}
	if err := check.Validate(&m, nil, check.ValidateOptions{Tooling: true}); err != nil {
		t.Fatal(err)
	}
	p := typesafe.Platform()
	if p.ID != typesafe.PlatformID || len(p.Endpoints) != 1 {
		t.Fatalf("platform %+v", p)
	}
	e := p.Endpoints[0]
	if e.Protocol != typesafe.ProtocolSystemOne || e.Path != "/v1/systemone" || e.PluginUsage() || e.TaskSubmit() || e.Request.StreamPath != "" {
		t.Errorf("endpoint %+v", e)
	}
	if !reflect.DeepEqual(p.PassHeaders, []string{"typesafe-danger-zone-images", "typesafe-danger-zone-long-context"}) {
		t.Errorf("passHeaders %v", p.PassHeaders)
	}
	// Each call is the caller's own copy.
	p.Endpoints[0].Path = "/changed"
	if typesafe.Platform().Endpoints[0].Path != "/v1/systemone" {
		t.Error("Platform shares its result")
	}
}

// The request bytes: the body as it is, Bearer, content-type, the client's
// danger-zone headers forwarded; the test and the model listing.
func TestAPIKey(t *testing.T) {
	p := standard.New(typesafe.APIKey("apikey"))
	acc := &pluginv1.Account{Id: 1, Type: "apikey", CredentialsJson: `{"api_key":"ts-0123456789"}`}
	body := `{"model":"jev-latest","state":"x","questions":{"q":{"type":"choice","choices":["a","b"]}},"assets":[{"data_base64":"AA=="}]}`
	out, err := p.BuildUpstream(context.Background(), &pluginv1.ExecuteRequest{
		Account: acc, Meta: &pluginv1.RequestMeta{Protocol: typesafe.ProtocolSystemOne, Model: "jev-latest"},
		InboundHeaders: map[string]string{"typesafe-danger-zone-images": "true"}, Body: []byte(body),
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Method != "POST" || out.Url != "https://api.typesafe.ai/v1/systemone" || string(out.Body) != body || out.UpstreamModel != "jev-latest" ||
		!reflect.DeepEqual(out.Headers, map[string]string{"authorization": "Bearer ts-0123456789"}) ||
		!reflect.DeepEqual(out.DefaultHeaders, map[string]string{"content-type": "application/json", "typesafe-danger-zone-images": "true"}) {
		t.Errorf("upstream %v", out)
	}
	pasted := &pluginv1.Account{Id: 1, Type: "apikey", CredentialsJson: `{"api_key":"ts-0123456789"}`, SettingsJson: `{"base_url":"https://ts.example.com/v1/"}`}
	test, err := p.BuildTestRequest(context.Background(), &pluginv1.BuildTestRequestRequest{Account: pasted})
	if err != nil || test.Url != "https://ts.example.com/v1/systemone" || test.Model != "jev-latest" || test.UsageProtocol != typesafe.ProtocolSystemOne ||
		test.BodyJson != `{"model":"jev-latest","questions":{"ping":{"instructions":"Is this text a greeting?","type":"noul"}},"state":"ping"}` {
		t.Errorf("test %v %v", test, err)
	}
	models, err := p.BuildModelsRequest(context.Background(), &pluginv1.BuildModelsRequestRequest{Account: acc})
	if err != nil || models.Url != "https://api.typesafe.ai/v1/models" || models.IdsPath != "models.#.name" ||
		!reflect.DeepEqual(models.Headers, map[string]string{"authorization": "Bearer ts-0123456789"}) {
		t.Errorf("models %v %v", models, err)
	}
	if _, err := p.BuildBalanceRequest(context.Background(), &pluginv1.BuildBalanceRequestRequest{Account: acc}); err == nil {
		t.Error("a balance request: TypeSafe has none")
	}
}

func TestPolicy(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	for name, c := range map[string]struct {
		status  int32
		headers map[string]string
		body    string
		action  pluginv1.ClassifyErrorResponse_Action
		effect  pluginv1.ClassifyErrorResponse_AccountEffect
		until   time.Duration
		reason  string
	}{
		"401":                  {401, nil, `{"detail":"Invalid API key"}`, pluginv1.ClassifyErrorResponse_ACTION_FAILOVER, pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_DISABLE, 0, "upstream rejected credentials (401): Invalid API key"},
		"429 retry-after":      {429, map[string]string{"Retry-After": "7"}, `{"detail":{"message":"too many"}}`, pluginv1.ClassifyErrorResponse_ACTION_FAILOVER, pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_COOLDOWN, 7 * time.Second, "rate limited until 2026-11-18T12:00:07Z (retry-after): too many"},
		"429 retry-after-ms":   {429, map[string]string{"retry-after-ms": "2500"}, ``, pluginv1.ClassifyErrorResponse_ACTION_FAILOVER, pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_COOLDOWN, 2 * time.Second, ""},
		"429 without a header": {429, nil, `{"detail":"slow"}`, pluginv1.ClassifyErrorResponse_ACTION_FAILOVER, pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_COOLDOWN, 60 * time.Second, ""},
		"529":                  {529, nil, `{"detail":"overloaded"}`, pluginv1.ClassifyErrorResponse_ACTION_FAILOVER, pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_COOLDOWN, 10 * time.Second, "upstream server error (529): overloaded"},
		"400":                  {400, nil, `{"detail":{"type":"max_tokens_exceeded","message":"too long"}}`, pluginv1.ClassifyErrorResponse_ACTION_RETURN_TO_CLIENT, 0, 0, ""},
		"422":                  {422, nil, `{"detail":[{"loc":["body","model"],"msg":"field required","type":"missing"}]}`, pluginv1.ClassifyErrorResponse_ACTION_RETURN_TO_CLIENT, 0, 0, ""},
	} {
		got := typesafe.Policy.Classify(now, &pluginv1.ClassifyErrorRequest{Status: c.status, Headers: c.headers, BodyPrefix: []byte(c.body)})
		if got.Action != c.action || got.AccountEffect != c.effect || (c.until > 0 && got.CooldownUntilUnix != now.Add(c.until).Unix()) || (c.reason != "" && got.Reason != c.reason) {
			t.Errorf("%s: %v", name, got)
		}
		// The upstream's body goes back as it is: no message or type of
		// the policy's own.
		if got.ClientMessage != "" || got.ClientErrorType != "" {
			t.Errorf("%s: client message %q type %q", name, got.ClientMessage, got.ClientErrorType)
		}
	}
}
