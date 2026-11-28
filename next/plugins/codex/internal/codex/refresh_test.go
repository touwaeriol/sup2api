package codex

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

func TestBuildRefreshRequest(t *testing.T) {
	h := startPlugin(t, newTestPlugin(DefaultEndpoints))
	ctx := context.Background()

	// Both types refresh as the Codex CLI does: no scope, a refresh reason.
	for _, typ := range accountTypes {
		r, err := h.Platform.BuildRefreshRequest(ctx, &pluginv1.BuildRefreshRequestRequest{Account: account(typ, deviceCreds)})
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]string
		mustJSON(t, []byte(r.GetBodyJson()), &body)
		if r.GetMethod() != "POST" || r.GetUrl() != "https://auth.openai.com/oauth/token" || r.GetHeaders()["Content-Type"] != "application/json" ||
			r.GetHeaders()["User-Agent"] != UserAgent {
			t.Fatalf("%s refresh = %v", typ, r)
		}
		if body["client_id"] != CodexClientID || body["grant_type"] != "refresh_token" || body["refresh_token"] != "rt-device" ||
			body["refresh_reason"] != "scheduled" || len(body) != 4 {
			t.Fatalf("%s body = %v", typ, body)
		}
	}

	for _, tc := range []struct {
		acc  *pluginv1.Account
		code codes.Code
	}{
		{account(TypeDevice, `{"access_token":"x"}`), codes.FailedPrecondition},
		{account(TypeOAuth, `{"access_token":"x"}`), codes.FailedPrecondition},
		{account("apikey", deviceCreds), codes.InvalidArgument},
		{account(TypeDevice, `{`), codes.InvalidArgument},
	} {
		if _, err := h.Platform.BuildRefreshRequest(ctx, &pluginv1.BuildRefreshRequestRequest{Account: tc.acc}); status.Code(err) != tc.code {
			t.Errorf("%s %s: %v, want %v", tc.acc.GetType(), tc.acc.GetCredentialsJson(), err, tc.code)
		}
	}
}

func TestParseRefreshResponse(t *testing.T) {
	h := startPlugin(t, newTestPlugin(DefaultEndpoints))
	ctx := context.Background()
	parse := func(acc *pluginv1.Account, st int32, body, transport string) *pluginv1.RefreshResult {
		t.Helper()
		r, err := h.Platform.ParseRefreshResponse(ctx, &pluginv1.ParseRefreshResponseRequest{
			Account: acc, Status: st, Body: []byte(body), TransportError: transport,
		})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	// Rotated refresh token, new id_token and identity fields, for both types.
	tok := idToken("me@example.com", "acct-2", "pro")
	for _, typ := range accountTypes {
		r := parse(account(typ, deviceCreds), 200,
			`{"access_token":"at-new","refresh_token":"rt-new","id_token":"`+tok+`","expires_in":3600,"scope":"openid"}`, "")
		if r.GetErrorType() != pluginv1.RefreshResult_ERROR_TYPE_UNSPECIFIED {
			t.Fatalf("%s result = %v", typ, r)
		}
		var patch map[string]any
		mustJSON(t, []byte(r.GetCredentialsPatchJson()), &patch)
		if patch["access_token"] != "at-new" || patch["refresh_token"] != "rt-new" || patch["id_token"] != tok ||
			patch["expires_at"] != float64(testNow.Unix()+3600) || patch["email"] != "me@example.com" ||
			patch["chatgpt_account_id"] != "acct-2" || patch["plan_type"] != "pro" || len(patch) != 7 {
			t.Fatalf("%s patch = %v", typ, patch)
		}
	}

	// No rotation: expiry and account id from the access token.
	at := fakeJWT(map[string]any{"exp": 1800009999, authClaim: map[string]any{"chatgpt_account_id": "acct-x"}})
	r := parse(account(TypeOAuth, deviceCreds), 200, `{"access_token":"`+at+`"}`, "")
	var patch map[string]any
	mustJSON(t, []byte(r.GetCredentialsPatchJson()), &patch)
	if patch["access_token"] != at || patch["expires_at"] != float64(1800009999) || patch["chatgpt_account_id"] != "acct-x" || patch["refresh_token"] != nil {
		t.Fatalf("oauth patch = %v", patch)
	}

	rejected := pluginv1.RefreshResult_ERROR_TYPE_AUTH_REJECTED
	transient := pluginv1.RefreshResult_ERROR_TYPE_TRANSIENT
	for _, tc := range []struct {
		name      string
		typ       string
		status    int32
		body      string
		transport string
		want      pluginv1.RefreshResult_ErrorType
		msg       string
	}{
		{"oauth invalid_grant", TypeOAuth, 400, `{"error":"invalid_grant","error_description":"revoked"}`, "", rejected, "authorize the account again"},
		{"openai refresh_token_reused", TypeDevice, 401, `{"error":{"code":"refresh_token_reused","message":"already used"}}`, "", rejected, "refresh_token_reused"},
		{"refresh_token_expired", TypeDevice, 400, `{"error":{"message":"x","type":"invalid_request_error","code":"refresh_token_expired"}}`, "", rejected, ""},
		{"refresh_token_invalidated", TypeOAuth, 400, `{"error":"refresh_token_invalidated"}`, "", rejected, ""},
		{"top-level code", TypeDevice, 400, `{"code":"refresh_token_expired"}`, "", rejected, "refresh_token_expired"},
		{"invalid_grant not 400", TypeOAuth, 403, `{"error":"invalid_grant"}`, "", transient, ""},
		{"invalid_refresh_token", TypeOAuth, 400, `{"error":"invalid_refresh_token"}`, "", transient, ""},
		{"invalid_client 401", TypeOAuth, 401, `{"error":"invalid_client"}`, "", rejected, "invalid_client"},
		{"plain 401", TypeDevice, 401, `{"detail":"nope"}`, "", rejected, ""},
		{"server error", TypeDevice, 503, `upstream down`, "", transient, "503"},
		{"rate limited", TypeOAuth, 429, `{"error":"slow_down"}`, "", transient, ""},
		{"400 other", TypeOAuth, 400, `{"error":"invalid_request"}`, "", transient, ""},
		{"transport", TypeDevice, 0, ``, "dial tcp: timeout", transient, "timeout"},
		{"unreadable", TypeDevice, 200, `not json`, "", transient, "unreadable"},
		{"no access token", TypeDevice, 200, `{"refresh_token":"r"}`, "", transient, "no access_token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := parse(account(tc.typ, deviceCreds), tc.status, tc.body, tc.transport)
			if r.GetErrorType() != tc.want || r.GetCredentialsPatchJson() != "" || !strings.Contains(r.GetErrorMessage(), tc.msg) {
				t.Fatalf("result = %v, want %v containing %q", r, tc.want, tc.msg)
			}
			// The message is shown on the account: English / 中文.
			if !strings.Contains(r.GetErrorMessage(), " / ") {
				t.Fatalf("message %q is not bilingual", r.GetErrorMessage())
			}
		})
	}
}
