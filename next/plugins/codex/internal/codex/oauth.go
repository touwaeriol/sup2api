package codex

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/textutil"
)

// Authorization routes (manifest routes, scope admin, permission
// codex:manage). The console's CodexAuthForm drives both logins (device code
// and Sign in with Codex); every route call sends at most two requests to
// OpenAI and never waits in a loop.
//
// The plugin opens no connection of its own: every request to OpenAI is a
// fetch the core sends (pluginsdk.Fetch) through the proxy the operator
// chose in the account form. Every route call of a login carries that
// choice as a proxy ticket the console got from the core for this call
// (CONTRACTS §117), and the requests of the call carry it, so each leaves
// through the proxy the form names at that moment; the session keeps no
// ticket and the plugin never learns the proxy. The call carries the form's
// User-Agent and originator the same way (routeCall).
//
// Sessions live in the plugin KV (shared by every node), keyed by a random
// session id. Answers follow CONTRACTS §3.1: a success is {"data": ...}
// (pluginsdk.DataResponse); a malformed request is 400 invalid_argument with
// details.fields (pluginsdk.BadRequest); a refusal of the flow itself keeps
// its own code ({"error":{"code","message"}}, e.g. device_auth_disabled,
// state_mismatch). Every message is "English / 中文".

const (
	kvDevice = "device_session"
	kvCodex  = "codex_session"

	// deviceTTL is how long a device code stays valid (Codex: 15 minutes).
	deviceTTL = 15 * time.Minute
	// codexTTL is how long a Sign in with Codex session waits for the
	// pasted callback.
	codexTTL = 30 * time.Minute
	// defaultInterval / maxInterval bound the device code poll interval.
	defaultInterval = 5
	maxInterval     = 60
	// slowDownStep is added to the interval when the issuer answers 429.
	slowDownStep = 5
	// pollLockTTL covers one poll under its lock: the status request and the
	// token exchange, which the core's bound on the route call ends sooner.
	pollLockTTL = 50 * time.Second
)

// ---------------------------------------------------------------- helpers

// randomToken returns n random bytes as unpadded URL-safe base64.
// crypto/rand.Read never fails (it crashes the program instead).
func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// pkceChallenge is the S256 code challenge of verifier.
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// newUUID returns a random UUIDv4 (a reset's redeem_request_id).
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// authAnswer is one response of the authorization server.
type authAnswer struct {
	status int
	body   []byte
}

// errUnreachable is a request the core sent that failed in transport: a
// poll asks again. Any other error of post is the core refusing to send it
// (the proxy ticket, its proxy, the host).
var errUnreachable = errors.New("cannot reach the OpenAI authorization server / 无法连接 OpenAI 授权服务器")

// routeCall is what the requests of one route call of a login go out with:
// the proxy ticket and the client headers of the account form at that
// moment. Neither is kept in a session.
type routeCall struct {
	ticket string
	agent  agent
}

// readCall reads the route call of req: its proxy ticket
// (pluginsdk.RouteProxyTicket) and the form's user_agent / originator ("" or
// absent for none), checked as ValidateCredentials checks the account's and
// resolved as an account's (agent).
func (p *Plugin) readCall(req *pluginv1.HTTPRequest) (routeCall, *pluginv1.HTTPResponse) {
	ticket, refused := pluginsdk.RouteProxyTicket(req)
	if refused != nil {
		return routeCall{}, refused
	}
	var m credMap
	if err := decodeOptional(req, &m); err != nil {
		return routeCall{}, errInvalidJSON()
	}
	m.trimStrings()
	if k, code, msg := m.agentError(); k != "" {
		return routeCall{}, pluginsdk.BadRequest(k, code, msg)
	}
	ua, _ := m.str("user_agent")
	originator, _ := m.str("originator")
	return routeCall{ticket: ticket, agent: p.agent(credentials{UserAgent: ua, Originator: originator})}, nil
}

// startCall reads the route call a start route needs; the response is set
// when the request cannot start a login (no caller, no ticket, a client
// header that cannot be sent).
func (p *Plugin) startCall(req *pluginv1.HTTPRequest) (routeCall, *pluginv1.HTTPResponse) {
	if req.GetCaller().GetUserId() <= 0 {
		return routeCall{}, pluginsdk.Unauthenticated()
	}
	return p.readCall(req)
}

// post has the core send one request of call to the authorization server
// through the proxy of its ticket (pluginsdk.Fetch): a POST with the
// content type, Accept: application/json and the call's client headers.
func (p *Plugin) post(ctx context.Context, call routeCall, rawURL, contentType string, body []byte) (authAnswer, error) {
	out, err := pluginsdk.Fetch(ctx, &pluginv1.ExecutionHTTPRequest{
		Method: http.MethodPost, Url: rawURL, Body: body, ProxyTicket: call.ticket,
		Headers: call.agent.setHeaders(map[string]string{"Content-Type": contentType, "Accept": "application/json"}),
	})
	if err != nil {
		return authAnswer{}, err
	}
	if out.GetTransportError() != "" {
		return authAnswer{}, fmt.Errorf("%w: %s", errUnreachable, out.GetTransportError())
	}
	return authAnswer{status: int(out.GetStatus()), body: out.GetBody()}, nil
}

func (p *Plugin) postJSON(ctx context.Context, call routeCall, rawURL string, v any) (authAnswer, error) {
	b, _ := json.Marshal(v)
	return p.post(ctx, call, rawURL, "application/json", b)
}

func (p *Plugin) postForm(ctx context.Context, call routeCall, rawURL string, form url.Values) (authAnswer, error) {
	return p.post(ctx, call, rawURL, "application/x-www-form-urlencoded", []byte(form.Encode()))
}

// Route error codes of a request post could not make.
const (
	// codeUpstreamUnavailable: the core sent it and it failed in transport.
	codeUpstreamUnavailable = "upstream_unavailable"
	// codeProxyRefused: the core refused to send it (the proxy ticket, its
	// proxy, the host), or the call to the core failed; nothing reached
	// OpenAI.
	codeProxyRefused = "proxy_refused"
)

// sendFailure classifies an error of post: its route error code and the
// detail of a message, the transport error for codeUpstreamUnavailable and
// the core's reason for codeProxyRefused.
func sendFailure(err error) (code, detail string) {
	if errors.Is(err, errUnreachable) {
		return codeUpstreamUnavailable, err.Error()
	}
	return codeProxyRefused, status.Convert(err).Message()
}

// sendFailed answers a request post could not make: 502
// upstream_unavailable, or 400 proxy_refused.
func sendFailed(err error) *pluginv1.HTTPResponse {
	code, detail := sendFailure(err)
	if code == codeProxyRefused {
		return flowError(code, refusedMessage(detail))
	}
	return upstreamError(code, detail)
}

// refusedMessage is the text of a request the core did not send, with its
// reason. The login goes on: the next call, with a fresh ticket of the
// form's proxy, may go through.
func refusedMessage(reason string) string {
	return "the request was not sent; check the proxy in the account form and try again / 这次请求没有发出，请检查账号表单里的代理后重试: " + reason
}

// refusedRestartMessage is the text of a refused request whose login cannot
// go on: the device code login's token exchange, whose session ended with
// the approval.
func refusedRestartMessage(reason string) string {
	return "the request was not sent; check the proxy in the account form and start again / 这次请求没有发出，请检查账号表单里的代理后重新开始: " + reason
}

func (a authAnswer) ok() bool { return a.status >= 200 && a.status <= 299 }

// describe is a short "status N: <error>" for messages.
func (a authAnswer) describe() string {
	detail := oauthErrorCode(a.body)
	if d := gjson.GetBytes(a.body, "error_description").String(); d != "" {
		detail += ": " + d
	} else if m := gjson.GetBytes(a.body, "error.message").String(); m != "" {
		detail += ": " + m
	}
	if strings.TrimSpace(detail) == "" {
		detail = textutil.Clip(strings.TrimSpace(string(a.body)), 200)
	}
	return fmt.Sprintf("status %d: %s", a.status, textutil.Clip(detail, 300))
}

// ---------------------------------------------------------------- credentials

// Credentials is the credential object both logins return: the fields the
// console stores on the account (docs/PLUGIN-CODEX.md §4).
type Credentials struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token,omitempty"`
	IDToken          string `json:"id_token,omitempty"`
	ExpiresAt        int64  `json:"expires_at,omitempty"`
	Email            string `json:"email,omitempty"`
	PlanType         string `json:"plan_type,omitempty"`
	ChatGPTAccountID string `json:"chatgpt_account_id,omitempty"`
}

func (p *Plugin) credentialsFrom(tr tokenResponse) Credentials {
	id := parseIdentity(tr.IDToken, tr.AccessToken)
	return Credentials{
		AccessToken:      strings.TrimSpace(tr.AccessToken),
		RefreshToken:     strings.TrimSpace(tr.RefreshToken),
		IDToken:          strings.TrimSpace(tr.IDToken),
		ExpiresAt:        p.expiresAt(tr),
		Email:            id.Email,
		PlanType:         id.PlanType,
		ChatGPTAccountID: id.AccountID,
	}
}

func readToken(a authAnswer) (tokenResponse, error) {
	var tr tokenResponse
	if err := json.Unmarshal(a.body, &tr); err != nil {
		return tr, fmt.Errorf("unreadable token response / 无法解析令牌响应: %w", err)
	}
	if strings.TrimSpace(tr.AccessToken) == "" {
		return tr, errors.New("token response has no access_token / 令牌响应中没有 access_token")
	}
	return tr, nil
}

// redeemFailure is why redeemCode got no tokens: the route error code
// (codeProxyRefused, codeUpstreamUnavailable, token_exchange_failed) and an
// "English / 中文" message; for codeProxyRefused msg is the core's reason,
// which the caller words as its login can go on or not.
type redeemFailure struct{ code, msg string }

// redeemNotSent is the failure of a token exchange post could not make.
func (p *Plugin) redeemNotSent(login string, err error) *redeemFailure {
	code, detail := sendFailure(err)
	if code == codeProxyRefused {
		p.log.Warn("codex: "+login+" token exchange refused by the core", "error", detail)
		return &redeemFailure{code: code, msg: detail}
	}
	p.log.Warn("codex: "+login+" token exchange failed", "error", detail)
	return &redeemFailure{code: code, msg: "start again / 请重新开始: " + detail}
}

// redeemCode exchanges an authorization code of the Codex client for its
// tokens (one form POST to the token endpoint, sent as call), the last step
// of both logins; login names the flow in the log.
func (p *Plugin) redeemCode(ctx context.Context, login string, call routeCall, code, verifier, redirectURI string) (Credentials, *redeemFailure) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", CodexClientID)
	form.Set("code_verifier", verifier)
	a, err := p.postForm(ctx, call, p.ep.Auth+"/oauth/token", form)
	if err != nil {
		return Credentials{}, p.redeemNotSent(login, err)
	}
	if !a.ok() {
		p.log.Warn("codex: "+login+" token exchange refused", "status", a.status, "oauth_error", oauthErrorCode(a.body))
		return Credentials{}, &redeemFailure{code: "token_exchange_failed", msg: "token exchange failed, start again / 换取令牌失败，请重新开始 (" + a.describe() + ")"}
	}
	tr, err := readToken(a)
	if err != nil {
		p.log.Warn("codex: "+login+" token answer unusable", "error", err.Error())
		return Credentials{}, &redeemFailure{code: "token_exchange_failed", msg: err.Error()}
	}
	return p.credentialsFrom(tr), nil
}
