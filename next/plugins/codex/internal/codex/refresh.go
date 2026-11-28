package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/textutil"
)

// Credential refresh (CONTRACTS §48): the plugin describes the request and
// reads the answer; the core decides when, sends it through the account's
// proxy under a cluster lock and saves the patch (the refresh token rotates).

// permanentRefreshCodes are the error codes after which the refresh token
// can never work again, as the Codex CLI classifies a refresh failure
// (codex-rs login/src/auth/manager.rs classify_refresh_token_failure).
var permanentRefreshCodes = map[string]bool{
	"refresh_token_expired":     true,
	"refresh_token_reused":      true,
	"refresh_token_invalidated": true,
}

// refreshPermanent reports a failed refresh the Codex CLI treats as
// permanent: a 401, a permanent code, or a 400 invalid_grant. Every other
// failure is temporary.
func refreshPermanent(status int32, code string) bool {
	code = strings.ToLower(code)
	return status == http.StatusUnauthorized || permanentRefreshCodes[code] ||
		(status == http.StatusBadRequest && code == "invalid_grant")
}

// refreshReason is the refresh_reason of a refresh the core schedules: the
// Codex CLI sends one of proactive_expiry, scheduled, unauthorized_recovery
// and forced.
const refreshReason = "scheduled"

// BuildRefreshRequest implements pluginsdk.CredentialRefresher: POST
// <auth>/oauth/token, JSON {grant_type, client_id (Codex public client),
// refresh_token, refresh_reason}, for both account types; no scope, as the
// Codex CLI sends it (codex-rs login/src/oauth/client.rs refresh).
//
// Credentials without a refresh token answer FAILED_PRECONDITION, which the
// core records like a refusal until the account is authorized again.
func (p *Plugin) BuildRefreshRequest(_ context.Context, in *pluginv1.BuildRefreshRequestRequest) (*pluginv1.BuildRefreshRequestResponse, error) {
	acc := in.GetAccount()
	if typ := acc.GetType(); !knownType(typ) {
		return nil, status.Errorf(codes.InvalidArgument, "unsupported account type %q", typ)
	}
	c, err := parseCredentials(acc.GetCredentialsJson())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "cannot read the credentials / 无法解析凭证: "+err.Error())
	}
	if c.RefreshToken == "" {
		return nil, status.Error(codes.FailedPrecondition, "the account has no refresh_token; authorize it again / 账号没有 refresh_token，请重新授权")
	}
	body, _ := json.Marshal(map[string]string{
		"grant_type": "refresh_token", "client_id": CodexClientID, "refresh_token": c.RefreshToken, "refresh_reason": refreshReason,
	})
	h := p.agent(c).setHeaders(map[string]string{"Accept": "application/json", "Content-Type": "application/json"})
	return &pluginv1.BuildRefreshRequestResponse{Method: http.MethodPost, Url: p.ep.Auth + "/oauth/token", Headers: h, BodyJson: string(body)}, nil
}

// tokenResponse is the answer of the token endpoint.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// expiresAt is now + expires_in, else the access token's exp claim, else 0.
func (p *Plugin) expiresAt(tr tokenResponse) int64 {
	if tr.ExpiresIn > 0 {
		return p.now().Unix() + tr.ExpiresIn
	}
	return tokenExpiry(tr.AccessToken)
}

// ParseRefreshResponse implements pluginsdk.CredentialRefresher. A success
// saves access_token and expires_at, plus refresh_token (rotated), id_token
// and the identity fields read from it, when the answer carries them. A
// permanent failure (refreshPermanent) is AUTH_REJECTED (authorize again);
// anything else is TRANSIENT.
func (p *Plugin) ParseRefreshResponse(_ context.Context, in *pluginv1.ParseRefreshResponseRequest) (*pluginv1.RefreshResult, error) {
	st := in.GetStatus()
	switch {
	case in.GetTransportError() != "":
		return refreshError(pluginv1.RefreshResult_ERROR_TYPE_TRANSIENT, "cannot reach the token endpoint / 无法连接令牌端点: "+in.GetTransportError()), nil
	case st < 200 || st > 299:
		return refreshFailure(st, in.GetBody()), nil
	}
	var tr tokenResponse
	if err := json.Unmarshal(in.GetBody(), &tr); err != nil {
		return refreshError(pluginv1.RefreshResult_ERROR_TYPE_TRANSIENT, "unreadable token response / 无法解析令牌响应: "+err.Error()), nil
	}
	tr.AccessToken = strings.TrimSpace(tr.AccessToken)
	if tr.AccessToken == "" {
		return refreshError(pluginv1.RefreshResult_ERROR_TYPE_TRANSIENT, "token response has no access_token / 令牌响应中没有 access_token"), nil
	}
	patch := map[string]any{"access_token": tr.AccessToken}
	if exp := p.expiresAt(tr); exp > 0 {
		patch["expires_at"] = exp
	}
	if rt := strings.TrimSpace(tr.RefreshToken); rt != "" {
		patch["refresh_token"] = rt
	}
	if idt := strings.TrimSpace(tr.IDToken); idt != "" {
		patch["id_token"] = idt
	}
	id := parseIdentity(tr.IDToken, tr.AccessToken)
	if id.Email != "" {
		patch["email"] = id.Email
	}
	if id.PlanType != "" {
		patch["plan_type"] = id.PlanType
	}
	if id.AccountID != "" {
		patch["chatgpt_account_id"] = id.AccountID
	}
	b, _ := json.Marshal(patch)
	return &pluginv1.RefreshResult{CredentialsPatchJson: string(b)}, nil
}

// refreshFailure is the result of a refusal of the token endpoint.
func refreshFailure(st int32, body []byte) *pluginv1.RefreshResult {
	code := oauthErrorCode(body)
	msg := fmt.Sprintf("token endpoint status %d / 令牌端点返回状态 %d: %s", st, st, textutil.Clip(string(body), 300))
	if refreshPermanent(st, code) {
		return refreshError(pluginv1.RefreshResult_ERROR_TYPE_AUTH_REJECTED, "refresh token refused ("+code+"); authorize the account again / 刷新令牌被拒绝，需重新授权: "+msg)
	}
	return refreshError(pluginv1.RefreshResult_ERROR_TYPE_TRANSIENT, msg)
}

func refreshError(t pluginv1.RefreshResult_ErrorType, msg string) *pluginv1.RefreshResult {
	return &pluginv1.RefreshResult{ErrorType: t, ErrorMessage: textutil.Clip(msg, 500)}
}

// oauthErrorCode reads the error code of a token endpoint answer where the
// Codex CLI reads it (codex-rs login/src/oauth/error.rs): the OAuth
// {"error":"invalid_grant"} form, then {"error":{"code"}}, then a top-level
// code. Empty when there is none.
func oauthErrorCode(body []byte) string {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ""
	}
	for _, path := range []string{"error", "error.code", "code"} {
		if r := gjson.GetBytes(body, path); r.Type == gjson.String {
			if s := strings.TrimSpace(r.Str); s != "" {
				return s
			}
		}
	}
	return ""
}
