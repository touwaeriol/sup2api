package codex

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/textutil"
)

// Sign in with Codex, the Codex CLI's browser login (docs/PLUGIN-CODEX.md
// §14): routes /auth/codex/start and /auth/codex/exchange.

// codexSession is the KV state of one Sign in with Codex login.
type codexSession struct {
	State    string `json:"state"`
	Verifier string `json:"code_verifier"`
}

// CodexStartResponse answers POST /auth/codex/start.
type CodexStartResponse struct {
	SessionID    string `json:"session_id"`
	AuthorizeURL string `json:"authorize_url"`
}

// CodexExchangeRequest is the body of POST /auth/codex/exchange, with the
// route call's proxy ticket and client headers (readCall).
type CodexExchangeRequest struct {
	SessionID   string `json:"session_id"`
	CallbackURL string `json:"callback_url"`
}

// CodexExchangeResponse answers POST /auth/codex/exchange.
type CodexExchangeResponse struct {
	Credentials Credentials `json:"credentials"`
}

// handleCodexStart creates the PKCE session and returns the Codex CLI's
// authorize URL. It sends nothing to OpenAI; it still reads the route call,
// as every route of a login does.
func (p *Plugin) handleCodexStart(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	call, refused := p.startCall(req)
	if refused != nil {
		return refused, nil
	}
	s := codexSession{State: randomToken(24), Verifier: randomToken(48)} // the verifier has 64 characters
	sessionID, err := p.newSession(ctx, kvCodex, req, s, codexTTL)
	if err != nil {
		return sessionNotStored(err), nil
	}
	return pluginsdk.DataResponse(CodexStartResponse{SessionID: sessionID, AuthorizeURL: p.authorizeURL(s, call.agent.Originator)}), nil
}

// authorizeURL is the Codex CLI's authorize URL for the PKCE session s
// (codex-rs login/src/server.rs build_authorize_url): beside the OAuth
// parameters, the workspaces in the id_token, the ChatGPT-only consent
// without the API organization setup, and the client's originator (the
// account form's, agent.go).
func (p *Plugin) authorizeURL(s codexSession, originator string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", CodexClientID)
	q.Set("redirect_uri", CodexRedirectURI)
	q.Set("code_challenge", pkceChallenge(s.Verifier))
	q.Set("code_challenge_method", "S256")
	q.Set("state", s.State)
	q.Set("scope", CodexScope)
	q.Set("id_token_add_organizations", "true")
	q.Set("codex_cli_simplified_flow", "true")
	q.Set("originator", originator)
	return p.ep.Auth + "/oauth/authorize?" + q.Encode()
}

// callbackParams are the parameters of the redirect to the local callback.
type callbackParams struct {
	Code, State             string
	Error, ErrorDescription string
}

// parseCallback reads what the operator pasted: the whole address of the
// page the browser could not open (http://127.0.0.1:1455/auth/callback?...),
// the address without its scheme, or only the query ("?code=..." or
// "code=..."). Parameters in a fragment are read when the query has none.
// HTML-escaped ampersands and surrounding quotes or angle brackets are
// tolerated. The first value of a repeated parameter counts.
func parseCallback(raw string) (callbackParams, error) {
	s := strings.TrimSpace(raw)
	s = strings.Trim(s, "\"'<>` \t\r\n")
	s = strings.ReplaceAll(s, "&amp;", "&")
	if s == "" {
		return callbackParams{}, errors.New("the callback address is empty / 回调网址为空")
	}
	var query, fragment string
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s, fragment = s[:i], s[i+1:]
	}
	switch i := strings.IndexByte(s, '?'); {
	case i >= 0:
		query = s[i+1:]
	case strings.Contains(s, "://") || (!strings.Contains(s, "=") && strings.Contains(s, "/")):
		query = "" // an address without a query
	default:
		query = s
	}
	values, err := url.ParseQuery(query)
	if err != nil {
		return callbackParams{}, fmt.Errorf("cannot read the callback address / 无法解析回调网址: %w", err)
	}
	if values.Get("code") == "" && values.Get("error") == "" && fragment != "" {
		if fv, ferr := url.ParseQuery(fragment); ferr == nil {
			values = fv
		}
	}
	cb := callbackParams{
		Code: strings.TrimSpace(values.Get("code")), State: strings.TrimSpace(values.Get("state")),
		Error: strings.TrimSpace(values.Get("error")), ErrorDescription: strings.TrimSpace(values.Get("error_description")),
	}
	if cb.Code == "" && cb.Error == "" {
		return cb, errors.New("the callback address has no code: paste the whole address of the page that failed to open / 回调网址中没有 code：请粘贴打不开的那个页面的完整地址")
	}
	return cb, nil
}

// handleCodexExchange checks the pasted callback against the session and
// exchanges the code for tokens (one request, sent as the route call).
func (p *Plugin) handleCodexExchange(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	var in CodexExchangeRequest
	if err := decodeOptional(req, &in); err != nil {
		return errInvalidJSON(), nil
	}
	id := strings.TrimSpace(in.SessionID)
	if id == "" {
		return errNoSession(), nil
	}
	call, refused := p.readCall(req)
	if refused != nil {
		return refused, nil
	}
	cb, err := parseCallback(in.CallbackURL)
	if err != nil {
		return flowError("invalid_callback", err.Error()), nil
	}
	var s codexSession
	found, err := p.loadSession(ctx, kvCodex, id, req, &s)
	if err != nil {
		return sessionUnreadable(err), nil
	}
	if !found {
		return pluginsdk.ErrorResponse(http.StatusNotFound, "session_expired", "the authorization session expired; generate a new link / 授权会话已过期，请重新生成链接"), nil
	}
	if cb.State != s.State {
		return flowError("state_mismatch", "the callback does not belong to this authorization (state mismatch); paste the address from the latest link / 回调与本次授权不匹配（state 不一致），请粘贴最新链接的回调地址"), nil
	}
	if cb.Error != "" {
		return p.callbackRefused(ctx, id, cb), nil
	}
	c, f := p.redeemCode(ctx, "Sign in with Codex", call, cb.Code, s.Verifier, CodexRedirectURI)
	if f != nil && f.code == codeProxyRefused {
		// Nothing reached OpenAI and the code is unspent: the session stays,
		// and the next call carries a fresh ticket of the form's proxy.
		return flowError(f.code, refusedMessage(f.msg)), nil
	}
	if f != nil {
		return upstreamError(f.code, f.msg), nil
	}
	p.deleteEntry(ctx, kvCodex, id)
	return pluginsdk.DataResponse(CodexExchangeResponse{Credentials: c}), nil
}

// callbackRefused answers a callback carrying an OAuth error; a denied
// consent also ends the session.
func (p *Plugin) callbackRefused(ctx context.Context, id string, cb callbackParams) *pluginv1.HTTPResponse {
	msg := cb.Error
	if cb.ErrorDescription != "" {
		msg += ": " + cb.ErrorDescription
	}
	p.log.Warn("codex: Sign in with Codex authorization failed", "oauth_error", textutil.Clip(cb.Error, 64))
	if cb.Error == "access_denied" {
		p.deleteEntry(ctx, kvCodex, id)
		return flowError("access_denied", "authorization was denied / 授权被拒绝 ("+msg+")")
	}
	return flowError("authorization_failed", "authorization failed / 授权失败 ("+msg+")")
}
