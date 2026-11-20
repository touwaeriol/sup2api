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

// terminalRefreshCodes mean the refresh token can never work again: the
// account has to be authorized again.
var terminalRefreshCodes = map[string]bool{
	"invalid_grant":             true,
	"invalid_refresh_token":     true,
	"token_expired":             true,
	"refresh_token_expired":     true,
	"refresh_token_invalidated": true,
	"refresh_token_reused":      true,
}

// BuildRefreshRequest implements pluginsdk.CredentialRefresher: POST
// <auth>/oauth/token, JSON {client_id (Codex public client), grant_type,
// refresh_token, scope "openid profile email"}, for both account types.
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
		"client_id": CodexClientID, "grant_type": "refresh_token", "refresh_token": c.RefreshToken, "scope": RefreshScope,
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
// terminal OAuth error or a 401 is AUTH_REJECTED (authorize again);
// invalid_client is a client configuration error that retrying cannot fix
// either; anything else is TRANSIENT.
func (p *Plugin) ParseRefreshResponse(_ context.Context, in *pluginv1.ParseRefreshResponseRequest) (*pluginv1.RefreshResult, error) {
	st := in.GetStatus()
	switch {
	case in.GetTransportError() != "":
		return refreshError(pluginv1.RefreshResult_ERROR_TYPE_TRANSIENT, "cannot reach the token endpoint / 无法连接令牌端点: "+in.GetTransportError()), nil
	case st < 200 || st > 299:
		code := oauthErrorCode(in.GetBody())
		msg := fmt.Sprintf("token endpoint status %d / 令牌端点返回状态 %d: %s", st, st, textutil.Clip(string(in.GetBody()), 300))
		switch {
		case terminalRefreshCodes[code]:
			return refreshError(pluginv1.RefreshResult_ERROR_TYPE_AUTH_REJECTED, "refresh token refused ("+code+"); authorize the account again / 刷新令牌被拒绝，需重新授权: "+msg), nil
		case code == "invalid_client":
			return refreshError(pluginv1.RefreshResult_ERROR_TYPE_AUTH_REJECTED, "OAuth client refused (invalid_client): configuration error; authorize the account again / 客户端配置错误，请重新授权: "+msg), nil
		case st == http.StatusUnauthorized:
			return refreshError(pluginv1.RefreshResult_ERROR_TYPE_AUTH_REJECTED, "refresh token refused; authorize the account again / 刷新令牌被拒绝，需重新授权: "+msg), nil
		}
		return refreshError(pluginv1.RefreshResult_ERROR_TYPE_TRANSIENT, msg), nil
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

func refreshError(t pluginv1.RefreshResult_ErrorType, msg string) *pluginv1.RefreshResult {
	return &pluginv1.RefreshResult{ErrorType: t, ErrorMessage: textutil.Clip(msg, 500)}
}

// oauthErrorCode reads the error code of a token endpoint answer: the OAuth
// {"error":"invalid_grant"} form, OpenAI's {"error":{"code"|"type"}} form,
// or a top-level code / error_code. Empty when there is none.
func oauthErrorCode(body []byte) string {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ""
	}
	e := gjson.GetBytes(body, "error")
	if e.Type == gjson.String {
		return strings.TrimSpace(e.String())
	}
	for _, r := range []gjson.Result{e.Get("code"), e.Get("type"), gjson.GetBytes(body, "code"), gjson.GetBytes(body, "error_code")} {
		if s := strings.TrimSpace(r.String()); s != "" && (terminalRefreshCodes[s] || s == "invalid_client") {
			return s
		}
	}
	for _, r := range []gjson.Result{e.Get("code"), gjson.GetBytes(body, "code"), gjson.GetBytes(body, "error_code"), e.Get("type")} {
		if s := strings.TrimSpace(r.String()); s != "" {
			return s
		}
	}
	return ""
}
