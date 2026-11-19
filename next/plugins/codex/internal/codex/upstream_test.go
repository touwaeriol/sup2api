package codex

import (
	"context"
	"testing"

	"github.com/tidwall/gjson"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

const (
	deviceCreds = `{"access_token":"at-device","refresh_token":"rt-device","chatgpt_account_id":"acct-1"}`
)

// accountTypes are the plugin's account types; they behave alike.
var accountTypes = []string{TypeDevice, TypeOAuth}

func TestBuildTestRequest(t *testing.T) {
	h := startPlugin(t, mustPlugin(DefaultEndpoints))
	for _, tc := range []struct {
		typ, model, wantModel string
	}{
		{TypeDevice, "", DefaultTestModel},
		{TypeOAuth, "gpt-5.4", "gpt-5.4"},
	} {
		r, err := h.Platform.BuildTestRequest(context.Background(), &pluginv1.BuildTestRequestRequest{Account: account(tc.typ, deviceCreds), Model: tc.model,
			Protocols: []string{ProtocolResponses, ProtocolImages}})
		if err != nil {
			t.Fatal(err)
		}
		if r.GetMethod() != "POST" || r.GetUrl() != "https://chatgpt.com/backend-api/codex/responses" || r.GetHeaders()["chatgpt-account-id"] != "acct-1" || r.GetModel() != tc.wantModel || r.GetUsageProtocol() != ProtocolResponses ||
			r.GetHeaders()["user-agent"] != UserAgent || r.GetHeaders()["accept"] != "text/event-stream" {
			t.Fatalf("%s: %v", tc.typ, r)
		}
		var body map[string]any
		mustJSON(t, []byte(r.GetBodyJson()), &body)
		if body["model"] != tc.wantModel || body["store"] != false || body["stream"] != true {
			t.Fatalf("%s body = %s", tc.typ, r.GetBodyJson())
		}
		if in, _ := body["input"].([]any); len(in) != 1 {
			t.Fatalf("%s input = %s", tc.typ, r.GetBodyJson())
		}
		if body["instructions"] != "" {
			t.Fatalf("%s instructions = %s", tc.typ, r.GetBodyJson())
		}
	}
}

// The test follows the first offered protocol the plugin tests: chat is
// tested as the Responses request it is converted to; an image model on
// openai.images gets the converted image request, driven by the chat model
// with the image model in the forced tool (CONTRACTS §121.2, §122).
func TestBuildTestRequestByProtocol(t *testing.T) {
	h := startPlugin(t, mustPlugin(DefaultEndpoints))
	build := func(model string, protocols ...string) (*pluginv1.BuildTestRequestResponse, error) {
		return h.Platform.BuildTestRequest(context.Background(), &pluginv1.BuildTestRequestRequest{
			Account: account(TypeDevice, deviceCreds), Model: model, Protocols: protocols})
	}
	r, err := build("gpt-6-sol", ProtocolChat)
	if err != nil || gjson.Get(r.GetBodyJson(), "model").Str != "gpt-6-sol" || gjson.Get(r.GetBodyJson(), "tools").Exists() {
		t.Fatalf("chat: %v %v", r, err)
	}
	r, err = build("gpt-image-2", "openai.images_edit", ProtocolImages, ProtocolResponses)
	if err != nil {
		t.Fatal(err)
	}
	body := gjson.Parse(r.GetBodyJson())
	if body.Get("model").Str != ImageChatModel || body.Get("tools.0.type").Str != "image_generation" || body.Get("tools.0.model").Str != "gpt-image-2" ||
		body.Get("tool_choice.type").Str != "image_generation" || body.Get("stream").Raw != "true" || body.Get("store").Raw != "false" ||
		body.Get("input.0.content.0.type").Str != "input_text" || r.GetModel() != "gpt-image-2" || r.GetUsageProtocol() != ProtocolResponses {
		t.Fatalf("image test = %v", r)
	}
	if _, err := build("gpt-6-sol", "openai.embeddings"); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("no protocol to test: %v", err)
	}
	if r, err := build("gpt-6-sol"); err != nil || gjson.Get(r.GetBodyJson(), "tools").Exists() {
		t.Fatalf("no protocols: %v %v", r, err)
	}
}

// The Codex backend's model list is the Codex CLI's internal endpoint: the
// plugin lists none and the console keeps the manifest defaults.
func TestNoModelList(t *testing.T) {
	h := startPlugin(t, mustPlugin(DefaultEndpoints))
	for _, typ := range accountTypes {
		if _, err := h.Platform.BuildModelsRequest(context.Background(), &pluginv1.BuildModelsRequestRequest{Account: account(typ, deviceCreds)}); status.Code(err) != codes.Unimplemented {
			t.Fatalf("%s models: %v", typ, err)
		}
	}
}

func TestValidateCredentials(t *testing.T) {
	h := startPlugin(t, mustPlugin(DefaultEndpoints))
	validate := func(typ, creds string) *pluginv1.ValidateCredentialsResponse {
		t.Helper()
		r, err := h.Platform.ValidateCredentials(context.Background(), &pluginv1.ValidateCredentialsRequest{AccountType: typ, CredentialsJson: creds})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	// Identity fields come from the id_token; expires_at becomes a number.
	tok := idToken("me@example.com", "acct-7", "plus")
	for _, typ := range accountTypes {
		r := validate(typ, `{"access_token":" at ","id_token":"`+tok+`","expires_at":"1800000123"}`)
		if len(r.GetErrors()) != 0 {
			t.Fatalf("%s errors = %v", typ, r.GetErrors())
		}
		var norm map[string]any
		mustJSON(t, []byte(r.GetNormalizedCredentialsJson()), &norm)
		if norm["access_token"] != "at" || norm["email"] != "me@example.com" || norm["chatgpt_account_id"] != "acct-7" ||
			norm["plan_type"] != "plus" || norm["expires_at"] != float64(1800000123) {
			t.Fatalf("%s normalized = %v", typ, norm)
		}
	}
	// Already normal: nothing to rewrite. Present fields are not overwritten.
	if r := validate(TypeOAuth, deviceCreds); len(r.GetErrors()) != 0 || r.GetNormalizedCredentialsJson() != "" {
		t.Fatalf("normal = %v", r)
	}
	r := validate(TypeOAuth, `{"access_token":"at","email":"kept@example.com","chatgpt_account_id":"kept","id_token":"`+tok+`"}`)
	var norm map[string]any
	mustJSON(t, []byte(r.GetNormalizedCredentialsJson()), &norm)
	if norm["email"] != "kept@example.com" || norm["chatgpt_account_id"] != "kept" || norm["plan_type"] != "plus" {
		t.Fatalf("kept = %v", norm)
	}
	for _, tc := range []struct {
		typ, creds, field, code string
	}{
		{"apikey", `{"access_token":"x"}`, "account_type", "unsupported"},
		{TypeDevice, `{}`, "access_token", "required"},
		{TypeDevice, `{"access_token":"  "}`, "access_token", "required"},
		{TypeDevice, `{"access_token":5}`, "access_token", "pattern"},
		{TypeDevice, `[1]`, "access_token", "invalid"},
		{TypeDevice, `{"access_token":"x","expires_at":"soon"}`, "expires_at", "pattern"},
		{TypeDevice, `{"access_token":"x","refresh_token":1}`, "refresh_token", "pattern"},
		{"chatgpt_siwc", `{"access_token":"x"}`, "account_type", "unsupported"},
		{TypeOAuth, `{"access_token":"x","email":1}`, "email", "pattern"},
		{TypeDevice, `{"access_token":"x","user_agent":1}`, "user_agent", "pattern"},
		{TypeOAuth, `{"access_token":"x","originator":"a\u0001b"}`, "originator", "pattern"},
		{TypeOAuth, `{"access_token":"x","user_agent":"a\nb"}`, "user_agent", "pattern"},
	} {
		r := validate(tc.typ, tc.creds)
		if len(r.GetErrors()) != 1 || r.GetErrors()[0].GetField() != tc.field || r.GetErrors()[0].GetCode() != tc.code {
			t.Errorf("%s %s: errors = %v, want %s/%s", tc.typ, tc.creds, r.GetErrors(), tc.field, tc.code)
		}
	}
}

func TestIdentity(t *testing.T) {
	// Account id and plan fall back to the access token's auth claim.
	at := fakeJWT(map[string]any{"exp": 1800003600, authClaim: map[string]any{"chatgpt_account_id": "acct-at", "chatgpt_plan_type": "pro"}})
	id := parseIdentity(fakeJWT(map[string]any{"email": "a@b.c"}), at)
	if id.Email != "a@b.c" || id.AccountID != "acct-at" || id.PlanType != "pro" {
		t.Fatalf("identity = %+v", id)
	}
	if tokenExpiry(at) != 1800003600 || tokenExpiry("opaque") != 0 {
		t.Fatal("token expiry")
	}
	if parseIdentity("not.a.jwt!", "x") != (identity{}) {
		t.Fatal("garbage token must yield nothing")
	}
}
