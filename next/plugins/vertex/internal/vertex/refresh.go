package vertex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/textutil"
)

// Credential refresh (CONTRACTS §48): the plugin describes the token
// exchange and reads the answer; the core decides when (about every half
// hour for these one-hour tokens), sends it through the account's proxy
// under a cluster lock and merges the patch into the stored credentials.

// defaultTokenLifetime is assumed when the answer names no expires_in:
// Google's access tokens last one hour, and a token without a readable
// expiry would never be renewed.
const defaultTokenLifetime = 3600

// rejectedGrantCodes mean the key itself was refused (deleted or disabled
// service account key, revoked refresh token, unknown OAuth client): only new
// credentials help.
var rejectedGrantCodes = map[string]bool{"invalid_grant": true, "invalid_client": true}

// clockSkewMarkers are how Google words an invalid_grant caused by the
// assertion's iat / exp, i.e. by this node's clock and not by the key
// ("Invalid JWT: Token must be a short-lived token (60 minutes) and in a
// reasonable timeframe. Check your iat and exp values in the JWT claim.").
var clockSkewMarkers = []string{"reasonable timeframe", "short-lived token"}

// BuildRefreshRequest implements pluginsdk.CredentialRefresher: the OAuth
// token request of the account's key file, form encoded. A key file that
// cannot be read answers FAILED_PRECONDITION, which the core records like a
// refusal until the credentials change.
func (p *Plugin) BuildRefreshRequest(_ context.Context, in *pluginv1.BuildRefreshRequestRequest) (*pluginv1.BuildRefreshRequestResponse, error) {
	a, err := readAccount(in.GetAccount())
	if err != nil {
		return nil, err
	}
	key, err := a.keyFile()
	if err != nil {
		return nil, err
	}
	form, err := key.grant(p.now())
	if err != nil {
		return nil, err
	}
	return &pluginv1.BuildRefreshRequestResponse{
		Method:   http.MethodPost,
		Url:      p.ep.Token,
		Headers:  map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Accept": "application/json"},
		BodyJson: form.Encode(),
	}, nil
}

// ParseRefreshResponse implements pluginsdk.CredentialRefresher. A success
// saves the access token, its expiry and the fingerprint of the key it was
// issued for. A refused key (400 invalid_grant / invalid_client, 401, 403)
// is AUTH_REJECTED; anything else, including an invalid_grant that blames
// the assertion's timestamps, is TRANSIENT and retried on the next pass.
func (p *Plugin) ParseRefreshResponse(_ context.Context, in *pluginv1.ParseRefreshResponseRequest) (*pluginv1.RefreshResult, error) {
	st, body := in.GetStatus(), in.GetBody()
	switch {
	case in.GetTransportError() != "":
		return transient("cannot reach the token endpoint / 无法连接令牌端点: " + in.GetTransportError()), nil
	case st < 200 || st > 299:
		return refusal(st, body), nil
	}
	token := strings.TrimSpace(gjson.GetBytes(body, "access_token").String())
	if token == "" {
		return transient("token response has no access_token / 令牌响应中没有 access_token"), nil
	}
	a, err := readAccount(in.GetAccount())
	if err != nil {
		return transient(err.Error()), nil
	}
	key, err := a.keyFile()
	if err != nil {
		return transient(err.Error()), nil
	}
	lifetime := gjson.GetBytes(body, "expires_in").Int()
	if lifetime <= 0 {
		lifetime = defaultTokenLifetime
	}
	patch, _ := json.Marshal(map[string]any{
		fieldAccessToken: token,
		fieldExpiresAt:   p.now().Unix() + lifetime,
		fieldTokenFor:    key.fingerprint(),
	})
	return &pluginv1.RefreshResult{CredentialsPatchJson: string(patch)}, nil
}

// refusal classifies a non-2xx answer of the token endpoint
// ({"error": code, "error_description": text}).
func refusal(st int32, body []byte) *pluginv1.RefreshResult {
	code := gjson.GetBytes(body, "error").String()
	description := gjson.GetBytes(body, "error_description").String()
	msg := fmt.Sprintf("token endpoint status %d / 令牌端点返回状态 %d: %s", st, st, textutil.Clip(string(body), 300))
	switch {
	case st == http.StatusBadRequest && code == "invalid_grant" && clockSkew(description):
		return transient("the assertion's timestamps were refused; check this node's clock / 断言的时间戳被拒绝，请检查节点时钟: " + msg)
	case st == http.StatusBadRequest && rejectedGrantCodes[code], st == http.StatusUnauthorized, st == http.StatusForbidden:
		return &pluginv1.RefreshResult{ErrorType: pluginv1.RefreshResult_ERROR_TYPE_AUTH_REJECTED,
			ErrorMessage: clip("the key was refused; replace the account's credentials / 密钥被拒绝，请更换账号凭证: " + msg)}
	}
	return transient(msg)
}

func clockSkew(description string) bool {
	d := strings.ToLower(description)
	for _, m := range clockSkewMarkers {
		if strings.Contains(d, m) {
			return true
		}
	}
	return false
}

func transient(msg string) *pluginv1.RefreshResult {
	return &pluginv1.RefreshResult{ErrorType: pluginv1.RefreshResult_ERROR_TYPE_TRANSIENT, ErrorMessage: clip(msg)}
}

// clip bounds a message to what the core keeps (512 bytes).
func clip(msg string) string { return textutil.Clip(msg, 500) }
