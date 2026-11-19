package vertex

import (
	"context"
	"net/http"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/gemini"
)

// geminiBody is a client's Gemini body as the core hands it over, with
// spacing, key order and an escape the plugin must leave alone.
const geminiBody = "{\"contents\": [{\"role\":\"user\",\"parts\":[{\"text\":\"ping \\u00e9\"}]}],\n \"generationConfig\":{\"temperature\":0.2}}"

// executeRequest is one attempt of a gateway request for acc. It carries a
// client header the gemini platform passes, which reaches the upstream as a
// default header.
func executeRequest(acc *pluginv1.Account, protocol, model string) *pluginv1.ExecuteRequest {
	return &pluginv1.ExecuteRequest{
		Account:        acc,
		Meta:           &pluginv1.RequestMeta{Protocol: protocol, Model: model, Stream: protocol == ProtocolStreamGenerate},
		Body:           []byte(geminiBody),
		InboundHeaders: map[string]string{"user-agent": "client/1.0"},
	}
}

// executeCase is an Execute input and the one request it must relay.
type executeCase struct {
	name string
	in   *pluginv1.ExecuteRequest
	// url and model are the address and model id of the request, auth its
	// authoritative headers.
	url, model string
	auth       map[string]string
}

// want is the request tc must relay: the body unchanged, the credential
// authoritative, the content type and the client header defaults.
func (tc executeCase) want() *pluginv1.UpstreamRequest {
	return &pluginv1.UpstreamRequest{
		Method:         http.MethodPost,
		Url:            tc.url,
		Headers:        tc.auth,
		DefaultHeaders: map[string]string{"content-type": contentType, "user-agent": "client/1.0"},
		Body:           []byte(geminiBody),
		UpstreamModel:  tc.model,
	}
}

const (
	testAPIKey   = "AQ.Ab8RN6-example"
	regionalBase = "https://us-central1-aiplatform.googleapis.com/v1/projects/my-project-123456/locations/us-central1/publishers/google/models/"
	globalBase   = "https://aiplatform.googleapis.com/v1/projects/my-project-123456/locations/global/publishers/google/models/"
	expressBase  = "https://aiplatform.googleapis.com/v1/publishers/google/models/"
)

var globalTarget = map[string]any{fieldProject: testProject, fieldLocation: LocationGlobal}

func userAccount(settings map[string]any) *pluginv1.Account {
	return stored(TypeAuthorizedUser, map[string]any{"authorized_user_json": userJSON(nil), fieldAccessToken: " ya29.user "}, settings)
}

func apiKeyAccount(settings map[string]any) *pluginv1.Account {
	return stored(TypeAPIKey, map[string]any{fieldAPIKey: testAPIKey}, settings)
}

// executeCases is every account type × regional/global/express address ×
// protocol, then the edge cases of the model and the credential. It is the
// input table of the conformance suite as well.
func executeCases() []executeCase {
	const model = "gemini-2.5-flash"
	tokenAuth := map[string]string{"authorization": "Bearer ya29.token"}
	userAuth := map[string]string{"authorization": "Bearer ya29.user", "x-goog-user-project": testProject}
	keyAuth := map[string]string{"x-goog-api-key": testAPIKey}
	accounts := []struct {
		name string
		acc  func(map[string]any) *pluginv1.Account
		auth map[string]string
		// express: the type may leave project and location empty.
		express bool
	}{
		{"service account", saAccount, tokenAuth, false},
		// User credentials belong to no project: the account's project is
		// named as the quota project, or aiplatform answers 403.
		{"user credentials", userAccount, userAuth, false},
		{"api key", apiKeyAccount, keyAuth, true},
	}
	targets := []struct {
		name     string
		settings map[string]any
		base     string
	}{
		{"regional", fullTarget, regionalBase},
		{"global", globalTarget, globalBase},
		{"express", map[string]any{fieldProject: "", fieldLocation: ""}, expressBase},
	}
	var cases []executeCase
	for _, a := range accounts {
		for _, tg := range targets {
			if tg.name == "express" && !a.express {
				continue
			}
			for _, pa := range [][2]string{
				{ProtocolGenerate, "generateContent"},
				{ProtocolStreamGenerate, "streamGenerateContent?alt=sse"},
				{ProtocolCountTokens, "countTokens"},
			} {
				cases = append(cases, executeCase{a.name + "/" + tg.name + "/" + pa[0],
					executeRequest(a.acc(tg.settings), pa[0], model), tg.base + model + ":" + pa[1], model, a.auth})
			}
		}
	}
	return append(cases,
		executeCase{"models/ prefix trimmed", executeRequest(saAccount(fullTarget), ProtocolGenerate, "models/"+model),
			regionalBase + model + ":generateContent", model, tokenAuth},
		executeCase{"model escaped as one segment", executeRequest(saAccount(fullTarget), ProtocolGenerate, "tuned/model?x#y"),
			regionalBase + "tuned%2Fmodel%3Fx%23y:generateContent", "tuned/model?x#y", tokenAuth},
		executeCase{"another region", executeRequest(saAccount(map[string]any{fieldProject: testProject, fieldLocation: "europe-west4"}), ProtocolGenerate, model),
			"https://europe-west4-aiplatform.googleapis.com/v1/projects/my-project-123456/locations/europe-west4/publishers/google/models/" + model + ":generateContent", model, tokenAuth},
		executeCase{"api key express without settings", executeRequest(apiKeyAccount(nil), ProtocolStreamGenerate, "models/"+model),
			expressBase + model + ":streamGenerateContent?alt=sse", model, keyAuth},
		// A token that has not expired yet is sent.
		executeCase{"token still valid", executeRequest(stored(TypeServiceAccount, map[string]any{
			"service_account_json": saJSON(nil), fieldAccessToken: "ya29.token", fieldExpiresAt: testNow.Unix() + 1}, fullTarget), ProtocolGenerate, model),
			regionalBase + model + ":generateContent", model, tokenAuth},
		executeCase{"user credentials, the account's own project", executeRequest(userAccount(map[string]any{fieldProject: "another-project-9", fieldLocation: LocationGlobal}), ProtocolGenerate, model),
			"https://aiplatform.googleapis.com/v1/projects/another-project-9/locations/global/publishers/google/models/" + model + ":generateContent", model,
			map[string]string{"authorization": "Bearer ya29.user", "x-goog-user-project": "another-project-9"}},
		// A token left in an API key account is not sent.
		executeCase{"api key with a stray token", executeRequest(stored(TypeAPIKey, map[string]any{fieldAPIKey: testAPIKey, fieldAccessToken: "ya29.stray"}, fullTarget), ProtocolGenerate, model),
			regionalBase + model + ":generateContent", model, keyAuth},
	)
}

// relayed runs one Execute in the harness and returns the request it
// relayed, failing t unless it relayed exactly once, without waiting and
// without a fetch, and answered host_usage.
func relayed(t *testing.T, h *pluginsdktest.Harness, in *pluginv1.ExecuteRequest) *pluginv1.UpstreamRequest {
	t.Helper()
	ex := h.Execute(context.Background(), in)
	relay := ex.Relayed(t)
	if relay.Wait || len(ex.Fetches()) != 0 || !proto.Equal(ex.Response, pluginsdk.ReportHostUsage()) {
		t.Fatalf("wait %v, fetches %d, response %v", relay.Wait, len(ex.Fetches()), ex.Response)
	}
	return relay.Request
}

func TestExecute(t *testing.T) {
	noDial(t)
	h := startPlugin(t, newTestPlugin(Endpoints{}))
	for _, tc := range executeCases() {
		t.Run(tc.name, func(t *testing.T) {
			if got := relayed(t, h, tc.in); !proto.Equal(got, tc.want()) {
				t.Fatalf("relayed %v\nwant    %v", got, tc.want())
			}
		})
	}
	if dials := h.Host.Dials(); len(dials) != 0 {
		t.Fatalf("egress dials: %v", dials)
	}
}

// Every attempt is built from scratch: the same input gives the same request
// (fixed clock), and an attempt on the next account gives that account's
// request, whatever was built before.
func TestExecuteAttempts(t *testing.T) {
	noDial(t)
	h := startPlugin(t, newTestPlugin(Endpoints{}))
	cases := executeCases()
	for i, tc := range cases {
		previous := cases[(i+1)%len(cases)]
		relayed(t, h, previous.in)
		retry := proto.Clone(tc.in).(*pluginv1.ExecuteRequest)
		retry.Attempt = 1
		first, again := relayed(t, h, retry), relayed(t, h, retry)
		if !proto.Equal(first, tc.want()) || !proto.Equal(again, first) {
			t.Errorf("%s after %s: relayed %v then %v", tc.name, previous.name, first, again)
		}
	}
}

// An account that cannot be served fails the attempt before anything is
// relayed: the gateway moves on to another account.
func TestExecuteRefusals(t *testing.T) {
	noDial(t)
	h := startPlugin(t, newTestPlugin(Endpoints{}))
	newSA := stored(TypeServiceAccount, map[string]any{"service_account_json": saJSON(nil), fieldExpiresAt: expiredAt}, fullTarget)
	for _, tc := range []struct {
		name     string
		acc      *pluginv1.Account
		protocol string
		model    string
		want     codes.Code
	}{
		// Waiting for the first token: the gateway moves on to another
		// account and does not penalize this one.
		{"no token yet", newSA, ProtocolGenerate, "gemini-2.5-flash", codes.FailedPrecondition},
		{"user without token", stored(TypeAuthorizedUser, map[string]any{"authorized_user_json": userJSON(nil)}, fullTarget), ProtocolGenerate, "gemini-2.5-flash", codes.FailedPrecondition},
		// An expired token is not sent either: the account waits for the
		// refresh like one that has no token yet.
		{"token expired", stored(TypeServiceAccount, map[string]any{
			"service_account_json": saJSON(nil), fieldAccessToken: "ya29.old", fieldExpiresAt: testNow.Unix() - 1}, fullTarget), ProtocolGenerate, "gemini-2.5-flash", codes.FailedPrecondition},
		{"token expiring now", stored(TypeAuthorizedUser, map[string]any{
			"authorized_user_json": userJSON(nil), fieldAccessToken: "ya29.old", fieldExpiresAt: testNow.Unix()}, fullTarget), ProtocolStreamGenerate, "gemini-2.5-flash", codes.FailedPrecondition},
		{"token expired, expiry as a string", stored(TypeServiceAccount, map[string]any{
			"service_account_json": saJSON(nil), fieldAccessToken: "ya29.old", fieldExpiresAt: "1700000000"}, fullTarget), ProtocolGenerate, "gemini-2.5-flash", codes.FailedPrecondition},
		{"api key missing", stored(TypeAPIKey, map[string]any{}, nil), ProtocolGenerate, "gemini-2.5-flash", codes.FailedPrecondition},
		{"service account without project", saAccount(nil), ProtocolGenerate, "gemini-2.5-flash", codes.FailedPrecondition},
		{"service account without location", saAccount(map[string]any{fieldProject: testProject}), ProtocolGenerate, "gemini-2.5-flash", codes.FailedPrecondition},
		{"api key with project only", apiKeyAccount(map[string]any{fieldProject: testProject}), ProtocolGenerate, "gemini-2.5-flash", codes.FailedPrecondition},
		// The location becomes part of the host: nothing but a location passes.
		{"location is a host", saAccount(map[string]any{fieldProject: testProject, fieldLocation: "evil.example/x"}), ProtocolGenerate, "gemini-2.5-flash", codes.FailedPrecondition},
		{"location with userinfo", saAccount(map[string]any{fieldProject: testProject, fieldLocation: "a@evil.example"}), ProtocolGenerate, "gemini-2.5-flash", codes.FailedPrecondition},
		{"project with a path", saAccount(map[string]any{fieldProject: "p/../../x", fieldLocation: "global"}), ProtocolGenerate, "gemini-2.5-flash", codes.FailedPrecondition},
		{"unknown type", stored("vertex_other", map[string]any{}, fullTarget), ProtocolGenerate, "gemini-2.5-flash", codes.FailedPrecondition},
		{"unknown protocol", saAccount(fullTarget), "openai.chat", "gemini-2.5-flash", codes.InvalidArgument},
		{"no model", saAccount(fullTarget), ProtocolGenerate, "", codes.InvalidArgument},
		{"only the prefix", saAccount(fullTarget), ProtocolGenerate, "models/", codes.InvalidArgument},
		{"credentials not JSON", &pluginv1.Account{Type: TypeServiceAccount, CredentialsJson: "{", SettingsJson: mustJSON(fullTarget)}, ProtocolGenerate, "gemini-2.5-flash", codes.InvalidArgument},
	} {
		ex := h.Execute(context.Background(), executeRequest(tc.acc, tc.protocol, tc.model))
		if status.Code(ex.Err) != tc.want || ex.Response != nil || len(ex.Relays())+len(ex.RelayErrors())+len(ex.Fetches()) != 0 {
			t.Errorf("%s: %v %v, %d relays, want %s and none", tc.name, ex.Response, ex.Err, len(ex.Relays()), tc.want)
		}
	}
}

func TestBuildTestRequest(t *testing.T) {
	p := newTestPlugin(Endpoints{})
	resp, err := p.BuildTestRequest(context.Background(), &pluginv1.BuildTestRequestRequest{Account: saAccount(fullTarget)})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetMethod() != http.MethodPost || resp.GetUrl() != regionalBase+"gemini-2.5-flash:generateContent" || resp.GetModel() != DefaultTestModel ||
		resp.GetUsageProtocol() != ProtocolGenerate || resp.GetBodyJson() != gemini.GenerateContentPingBody(1) ||
		resp.GetHeaders()["authorization"] != "Bearer ya29.token" || resp.GetHeaders()["content-type"] != contentType {
		t.Fatalf("test request = %+v", resp)
	}

	// The model the operator names, on an express API key account.
	resp, err = p.BuildTestRequest(context.Background(), &pluginv1.BuildTestRequestRequest{
		Account: apiKeyAccount(nil), Model: " models/gemini-2.5-pro ",
	})
	if err != nil || resp.GetUrl() != expressBase+"gemini-2.5-pro:generateContent" ||
		resp.GetModel() != "gemini-2.5-pro" || resp.GetHeaders()["x-goog-api-key"] != testAPIKey {
		t.Fatalf("test request = %+v, err %v", resp, err)
	}

	// No token yet: the test says so instead of sending an unauthorized request.
	_, err = p.BuildTestRequest(context.Background(), &pluginv1.BuildTestRequestRequest{
		Account: stored(TypeServiceAccount, map[string]any{"service_account_json": saJSON(nil), fieldExpiresAt: expiredAt}, fullTarget),
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("test without token: %v", err)
	}
	// The same for an expired one.
	_, err = p.BuildTestRequest(context.Background(), &pluginv1.BuildTestRequestRequest{
		Account: stored(TypeServiceAccount, map[string]any{"service_account_json": saJSON(nil), fieldAccessToken: "ya29.old", fieldExpiresAt: testNow.Unix() - 60}, fullTarget),
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("test with an expired token: %v", err)
	}
}
