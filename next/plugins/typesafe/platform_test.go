package main

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/plugins/typesafe/internal/typesafe"
	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	preset "github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/typesafe"
)

// TypeSafe has no recorded golden file: these tests pin the request bytes
// against the official API (POST {base}/v1/systemone, Authorization:
// Bearer, the body as it is), run on the platform the plugin serves.

const apiKey = "ts-0123456789abcdef"

var platform = typesafe.New()

func account(settings string) *pluginv1.Account {
	return &pluginv1.Account{Id: 1, Type: typesafe.TypeAPIKey, CredentialsJson: `{"api_key":"` + apiKey + `"}`, SettingsJson: settings}
}

// coreFiltered are the client headers the core hands over: the ones the
// manifest passes for the type's platform.
func coreFiltered(t *testing.T, client map[string]string) map[string]string {
	t.Helper()
	m := load(t)
	out := map[string]string{}
	for _, name := range m.AccountTypes[0].Platforms[0].ClientHeaders(&m.Platforms[0]) {
		if value, ok := client[name]; ok {
			out[name] = value
		}
	}
	return out
}

// TestRequest: every question type and an inline image go through as they
// are, the client's two danger-zone headers too (never its authorization
// or anything else), to the base URL less the one /v1 it may end in: a
// pasted /v1 is not doubled, an address ending in a /v1 of its own is
// reached at .../v1/v1, and an escaped "/" goes out as it was written.
func TestRequest(t *testing.T) {
	body := `{"model":"jev-latest","state":{"text":"hi"},"questions":{` +
		`"a":{"type":"noul","instructions":"Greeting?"},` +
		`"b":{"type":"choice","instructions":"Tone","criteria":{"warm":"w","cold":"c"}},` +
		`"c":{"type":"score","instructions":"Rate","criteria":["bad","good"]},` +
		`"d":{"type":"box","instructions":"Face"}},` +
		`"assets":[{"type":"image","format":"png","data_base64":"iVBORw0KGgo="}]}`
	inbound := coreFiltered(t, map[string]string{
		"typesafe-danger-zone-images": "true", "typesafe-danger-zone-long-context": "true",
		"authorization": "Bearer client", "user-agent": "cli/1",
	})
	for settings, url := range map[string]string{
		`{}`: "https://api.typesafe.ai/v1/systemone",
		`{"base_url":"https://api.typesafe.ai/v1/"}`:    "https://api.typesafe.ai/v1/systemone",
		`{"base_url":"https://ts.example.com/v1"}`:      "https://ts.example.com/v1/systemone",
		`{"base_url":"https://ts.example.com/v1/v1"}`:   "https://ts.example.com/v1/v1/systemone",
		`{"base_url":"https://ts.example.com/p/a%2Fb"}`: "https://ts.example.com/p/a%2Fb/v1/systemone",
	} {
		out, err := platform.BuildUpstream(context.Background(), &pluginv1.ExecuteRequest{
			Account: account(settings), Meta: &pluginv1.RequestMeta{Protocol: preset.ProtocolSystemOne, Model: "jev-latest"},
			InboundHeaders: inbound, Body: []byte(body),
		})
		if err != nil {
			t.Fatalf("%s: %v", settings, err)
		}
		if out.Method != "POST" || out.Url != url || string(out.Body) != body || out.UpstreamModel != "jev-latest" {
			t.Errorf("%s: %s %s model %q body %s", settings, out.Method, out.Url, out.UpstreamModel, out.Body)
		}
		wantDefault := map[string]string{"content-type": "application/json",
			"typesafe-danger-zone-images": "true", "typesafe-danger-zone-long-context": "true"}
		if !reflect.DeepEqual(out.Headers, map[string]string{"authorization": "Bearer " + apiKey}) || !reflect.DeepEqual(out.DefaultHeaders, wantDefault) {
			t.Errorf("%s: headers %v default %v", settings, out.Headers, out.DefaultHeaders)
		}
	}
}

// TestAccountRequests: the account test is one systemone question on
// jev-latest, the model listing GET /v1/models read at models.#.name, and
// there is no balance request.
func TestAccountRequests(t *testing.T) {
	test, err := platform.BuildTestRequest(context.Background(), &pluginv1.BuildTestRequestRequest{Account: account(`{}`)})
	if err != nil || test.Url != "https://api.typesafe.ai/v1/systemone" || test.Model != "jev-latest" || test.UsageProtocol != preset.ProtocolSystemOne ||
		test.BodyJson != `{"model":"jev-latest","questions":{"ping":{"instructions":"Is this text a greeting?","type":"noul"}},"state":"ping"}` ||
		test.Headers["authorization"] != "Bearer "+apiKey || test.Headers["content-type"] != "application/json" {
		t.Errorf("test = %v, %v", test, err)
	}
	models, err := platform.BuildModelsRequest(context.Background(), &pluginv1.BuildModelsRequestRequest{Account: account(`{"base_url":"https://api.typesafe.ai/v1"}`)})
	if err != nil || models.Url != "https://api.typesafe.ai/v1/models" || models.IdsPath != "models.#.name" ||
		!reflect.DeepEqual(models.Headers, map[string]string{"authorization": "Bearer " + apiKey}) {
		t.Errorf("models = %v, %v", models, err)
	}
	if _, err := platform.BuildBalanceRequest(context.Background(), &pluginv1.BuildBalanceRequestRequest{Account: account(`{}`)}); err == nil {
		t.Error("a balance request: TypeSafe has none")
	}
}

// TestClassify: a rejected key disables the account, a 429 cools it down
// until Retry-After, an overload or server error fails over, and a bad
// request goes back to the client with the account untouched.
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
		{"bad key", 401, nil, `{"detail":"Invalid API key"}`, failover, disable, 0},
		{"forbidden", 403, nil, `{"detail":"Forbidden"}`, failover, disable, 0},
		{"rate limit, retry-after", 429, map[string]string{"retry-after": "7"}, `{"detail":"slow down"}`, failover, cooldown, 7},
		{"rate limit, no header", 429, nil, `{"detail":"slow down"}`, failover, cooldown, 60},
		{"overloaded", 529, nil, `{"detail":"overloaded"}`, failover, cooldown, 10},
		{"server error", 500, nil, ``, failover, cooldown, 10},
		{"context exceeded", 400, nil, `{"detail":{"type":"max_tokens_exceeded","message":"too long"}}`, toClient, none, 0},
		{"too large", 413, nil, `{"detail":"Request entity too large"}`, toClient, none, 0},
		{"media type", 415, nil, `{"detail":"Unsupported media type"}`, toClient, none, 0},
		{"validation", 422, nil, `{"detail":[{"loc":["body","model"],"msg":"field required","type":"missing"}]}`, toClient, none, 0},
	} {
		out := platform.ClassifyAt(now, &pluginv1.ClassifyErrorRequest{
			Meta: &pluginv1.RequestMeta{Protocol: preset.ProtocolSystemOne}, Account: account(`{}`),
			Status: tc.status, Headers: tc.headers, BodyPrefix: []byte(tc.body),
		})
		until := int64(0)
		if tc.seconds > 0 {
			until = now.Unix() + tc.seconds
		}
		if out.Action != tc.action || out.AccountEffect != tc.effect || out.CooldownUntilUnix != until || out.ClientMessage != "" {
			t.Errorf("%s: %v %v until %d message %q, want %v %v until %d", tc.name, out.Action, out.AccountEffect, out.CooldownUntilUnix, out.ClientMessage, tc.action, tc.effect, until)
		}
	}
}
