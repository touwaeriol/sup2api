package codex

import (
	"bytes"
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
	"strconv"
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
	// lockReleaseTimeout bounds giving the poll lock back.
	lockReleaseTimeout = 5 * time.Second
)

// ---------------------------------------------------------------- helpers

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// pkceChallenge is the S256 code challenge of verifier.
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// newUUID returns a random UUIDv4 (a reset's redeem_request_id).
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
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

// post has the core send one request of call to the authorization server
// through the proxy of its ticket (pluginsdk.Fetch). The method, headers and
// body are what the plugin sent itself before.
func (p *Plugin) post(ctx context.Context, call routeCall, rawURL, contentType string, body []byte) (authAnswer, error) {
	out, err := pluginsdk.Fetch(ctx, &pluginv1.ExecutionHTTPRequest{
		Method: http.MethodPost, Url: rawURL, Body: body, ProxyTicket: call.ticket,
		Headers: map[string]string{"Content-Type": contentType, "Accept": "application/json", "User-Agent": call.agent.UserAgent, "Originator": call.agent.Originator},
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

// sendFailed answers a request post could not make: 502 upstream_unavailable
// when it failed in transport, 400 proxy_refused when the core refused it.
func sendFailed(err error) *pluginv1.HTTPResponse {
	if errors.Is(err, errUnreachable) {
		return upstreamError("upstream_unavailable", err.Error())
	}
	return flowError("proxy_refused", refusedMessage(err))
}

// refusedMessage is the text of a request that was not sent (the core
// refused it, or the call to the core failed). Nothing reached OpenAI: the
// next call, with a fresh ticket of the form's proxy, may go through.
func refusedMessage(err error) string {
	return "the request was not sent; check the proxy in the account form and try again / 这次请求没有发出，请检查账号表单里的代理后重试: " + status.Convert(err).Message()
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

func (p *Plugin) kv() (pluginsdk.KV, error) {
	if p.host == nil {
		return nil, pluginsdk.ErrHostNotReady
	}
	return p.host.KV(), nil
}

// Authorization sessions are caller sessions (pluginsdk.SaveCallerSession):
// only the console user who started one can continue it, and to anyone else
// it looks expired.

// loadSession reads session id of req's caller.
func (p *Plugin) loadSession(ctx context.Context, ns, id string, req *pluginv1.HTTPRequest, v any) (bool, error) {
	kv, err := p.kv()
	if err != nil {
		return false, err
	}
	return pluginsdk.LoadCallerSession(ctx, kv, ns, id, req, v)
}

// newSession stores v as a new session of req's caller and returns its id.
func (p *Plugin) newSession(ctx context.Context, ns string, req *pluginv1.HTTPRequest, v any, ttl time.Duration) (string, error) {
	kv, err := p.kv()
	if err != nil {
		return "", err
	}
	return pluginsdk.SaveCallerSession(ctx, kv, ns, req, v, ttl)
}

// sessionNotStored answers a failed newSession: 401 without a caller, else
// the store is unavailable.
func sessionNotStored(err error) *pluginv1.HTTPResponse {
	if errors.Is(err, pluginsdk.ErrNoCaller) {
		return pluginsdk.Unauthenticated()
	}
	return pluginsdk.Unavailable("store the session / 保存会话失败: " + err.Error())
}

// saveSession rewrites the session loadSession returned for req.
func (p *Plugin) saveSession(ctx context.Context, ns, id string, req *pluginv1.HTTPRequest, v any, ttl time.Duration) error {
	kv, err := p.kv()
	if err != nil {
		return err
	}
	return pluginsdk.UpdateCallerSession(ctx, kv, ns, id, req, v, ttl)
}

// loadCached and saveCached keep plugin-wide JSON in the KV (the reset count
// cache): unlike a session it belongs to no caller.
func (p *Plugin) loadCached(ctx context.Context, ns, id string, v any) (bool, error) {
	kv, err := p.kv()
	if err != nil {
		return false, err
	}
	raw, found, err := kv.Get(ctx, ns, id)
	if err != nil || !found {
		return false, err
	}
	return true, json.Unmarshal(raw, v)
}

func (p *Plugin) saveCached(ctx context.Context, ns, id string, v any, ttl time.Duration) error {
	kv, err := p.kv()
	if err != nil {
		return err
	}
	b, _ := json.Marshal(v)
	return kv.Set(ctx, ns, id, b, ttl)
}

// deleteSession deletes a KV entry (an authorization session or a cached
// reset count). A failure is only logged: the entry expires with its ttl,
// every code is single-use on OpenAI's side, and a stale count is only shown.
func (p *Plugin) deleteSession(ctx context.Context, ns, id string) {
	kv, err := p.kv()
	if err == nil {
		err = kv.Delete(ctx, ns, id)
	}
	if err != nil {
		p.log.Warn("codex: delete a KV entry", "namespace", ns, "error", err.Error())
	}
}

// decodeOptional reads an optional JSON body ("" or {} are both fine).
func decodeOptional(req *pluginv1.HTTPRequest, v any) error {
	if len(bytes.TrimSpace(req.GetBody())) == 0 {
		return nil
	}
	return json.Unmarshal(req.GetBody(), v)
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

// errInvalidJSON and errNoSession are malformed route requests
// (pluginsdk.BadRequest: 400 invalid_argument with details.fields).
func errInvalidJSON() *pluginv1.HTTPResponse {
	return pluginsdk.BadRequest("body", "invalid_json", "the request body is not valid JSON / 请求体不是合法 JSON")
}

func errNoSession() *pluginv1.HTTPResponse {
	return pluginsdk.BadRequest("session_id", "required", "session_id is required / 缺少 session_id")
}

// errNotReady is a 503 for a route called before Init.
func errNotReady() *pluginv1.HTTPResponse {
	return pluginsdk.Unavailable("the plugin is not ready / 插件尚未就绪: " + pluginsdk.ErrHostNotReady.Error())
}

// flowError is a 400 refusal of the authorization flow with its own code.
func flowError(code, msg string) *pluginv1.HTTPResponse {
	return pluginsdk.ErrorResponse(http.StatusBadRequest, code, msg)
}

func upstreamError(code, msg string) *pluginv1.HTTPResponse {
	return pluginsdk.ErrorResponse(http.StatusBadGateway, code, msg)
}

// internalError is a 500 for a local failure; msg is "English / 中文".
func internalError(msg string, err error) *pluginv1.HTTPResponse {
	return pluginsdk.ErrorResponse(http.StatusInternalServerError, "internal", msg+": "+err.Error())
}

const msgRandom = "cannot generate random values / 无法生成随机数"

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
// (upstream_unavailable, token_exchange_failed) and an "English / 中文"
// message.
type redeemFailure struct{ code, msg string }

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
	if errors.Is(err, errUnreachable) {
		p.log.Warn("codex: "+login+" token exchange failed", "error", err.Error())
		return Credentials{}, &redeemFailure{"upstream_unavailable", "start again / 请重新开始: " + err.Error()}
	}
	if err != nil {
		p.log.Warn("codex: "+login+" token exchange refused by the core", "error", status.Convert(err).Message())
		return Credentials{}, &redeemFailure{"proxy_refused", refusedMessage(err)}
	}
	if !a.ok() {
		p.log.Warn("codex: "+login+" token exchange refused", "status", a.status, "oauth_error", oauthErrorCode(a.body))
		return Credentials{}, &redeemFailure{"token_exchange_failed", "token exchange failed, start again / 换取令牌失败，请重新开始 (" + a.describe() + ")"}
	}
	tr, err := readToken(a)
	if err != nil {
		p.log.Warn("codex: "+login+" token answer unusable", "error", err.Error())
		return Credentials{}, &redeemFailure{"token_exchange_failed", err.Error()}
	}
	return p.credentialsFrom(tr), nil
}

// ---------------------------------------------------------------- device code

// deviceSession is the KV state of one device code login. It keeps no proxy
// ticket or client headers: each request of the login goes out as its route
// call says (routeCall), the form's choice at that moment.
type deviceSession struct {
	DeviceAuthID string `json:"device_auth_id"`
	UserCode     string `json:"user_code"`
	Interval     int    `json:"interval"`
	ExpiresAt    int64  `json:"expires_at"` // Unix seconds
	// LastPollMs is when the plugin last asked the issuer (Unix ms).
	LastPollMs int64 `json:"last_poll_ms,omitempty"`
}

// DeviceStartResponse answers POST /auth/device/start.
type DeviceStartResponse struct {
	SessionID       string `json:"session_id"`
	VerificationURL string `json:"verification_url"`
	UserCode        string `json:"user_code"`
	Interval        int    `json:"interval"`
	ExpiresIn       int    `json:"expires_in"`
}

// DevicePollResponse answers POST /auth/device/poll.
type DevicePollResponse struct {
	Status      string       `json:"status"` // pending | done | expired | error
	Credentials *Credentials `json:"credentials,omitempty"`
	Message     string       `json:"message,omitempty"`
	// Interval is the poll interval in seconds, on pending answers.
	Interval int `json:"interval,omitempty"`
}

// parseInterval reads the issuer's interval (a string of seconds, or a number).
func parseInterval(r gjson.Result) int {
	n := 0
	switch r.Type {
	case gjson.Number:
		n = int(r.Int())
	case gjson.String:
		n, _ = strconv.Atoi(strings.TrimSpace(r.String()))
	}
	if n <= 0 {
		return defaultInterval
	}
	return min(n, maxInterval)
}

// handleDeviceStart asks the issuer for a user code (one request).
func (p *Plugin) handleDeviceStart(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	call, refused := p.startCall(req)
	if refused != nil {
		return refused, nil
	}
	a, err := p.postJSON(ctx, call, p.ep.Auth+"/api/accounts/deviceauth/usercode", map[string]string{"client_id": CodexClientID})
	if err != nil {
		return sendFailed(err), nil
	}
	if a.status == http.StatusNotFound {
		return flowError("device_auth_disabled", "device code login is not enabled for this ChatGPT account or workspace: turn on Device code authorization in ChatGPT Settings → Security (a workspace admin must allow it) / 该 ChatGPT 账号或工作区未开启设备码登录：请在 ChatGPT 设置 → 安全 中开启“设备码授权”（企业工作区需管理员开启）"), nil
	}
	if !a.ok() {
		return upstreamError("upstream_error", "device code request failed / 申请设备码失败 ("+a.describe()+")"), nil
	}
	deviceAuthID := strings.TrimSpace(gjson.GetBytes(a.body, "device_auth_id").String())
	userCode := strings.TrimSpace(gjson.GetBytes(a.body, "user_code").String())
	if userCode == "" {
		userCode = strings.TrimSpace(gjson.GetBytes(a.body, "usercode").String())
	}
	if deviceAuthID == "" || userCode == "" {
		return upstreamError("upstream_error", "the device code answer has no device_auth_id or user_code / 设备码响应缺少 device_auth_id 或 user_code"), nil
	}
	s := deviceSession{
		DeviceAuthID: deviceAuthID, UserCode: userCode,
		Interval:  parseInterval(gjson.GetBytes(a.body, "interval")),
		ExpiresAt: p.now().Add(deviceTTL).Unix(),
	}
	sessionID, err := p.newSession(ctx, kvDevice, req, s, deviceTTL)
	if err != nil {
		return sessionNotStored(err), nil
	}
	return pluginsdk.DataResponse(DeviceStartResponse{
		SessionID: sessionID, VerificationURL: p.ep.Auth + "/codex/device", UserCode: userCode,
		Interval: s.Interval, ExpiresIn: int(deviceTTL / time.Second),
	}), nil
}

type sessionRequest struct {
	SessionID string `json:"session_id"`
}

// handleDevicePoll asks the issuer ONCE whether the user has approved the
// code. A poll that comes sooner than the interval after the previous one is
// answered "pending" without asking. On approval it exchanges the code for
// tokens (a second request, once) and ends the session.
//
// The session's read-modify-write runs under a cluster lock per session
// (Host.Locks), so two polls racing (two tabs, two nodes) cannot both see the
// approval and both spend the single-use code: the one that finds the lock
// taken answers "pending" and polls again later.
func (p *Plugin) handleDevicePoll(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	var in sessionRequest
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
	if p.host == nil {
		return errNotReady(), nil
	}
	// Whose session it is comes before its lock: someone else's id must
	// neither answer "pending" while its owner polls nor hold the owner up.
	if found, err := p.loadSession(ctx, kvDevice, id, req, new(deviceSession)); err != nil {
		return sessionUnreadable(err), nil
	} else if !found {
		return deviceExpired(), nil
	}
	lock, ok, err := p.host.Locks().TryAcquire(ctx, pollLockName(id), pollLockTTL)
	if err != nil {
		p.log.Warn("codex: lock the device code session", "error", err.Error())
		return pluginsdk.Unavailable("lock the session / 锁定会话失败: " + err.Error()), nil
	}
	if !ok {
		return pollAnswer(DevicePollResponse{Status: pollPending}), nil
	}
	defer p.releaseLock(ctx, lock)
	return p.pollDevice(ctx, req, id, call), nil
}

// Device code poll states (DevicePollResponse.Status).
const (
	pollPending = "pending"
	pollDone    = "done"
	pollExpired = "expired"
	pollError   = "error"
)

func pollAnswer(r DevicePollResponse) *pluginv1.HTTPResponse { return pluginsdk.DataResponse(r) }

func pollFailed(msg string) *pluginv1.HTTPResponse {
	return pollAnswer(DevicePollResponse{Status: pollError, Message: msg})
}

// pollLockName is the lock of one device code session. The session id is
// hashed: it is a secret, and base64url ids carry "_", which lock names do
// not allow.
func pollLockName(sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	return "device_poll:" + hex.EncodeToString(sum[:16])
}

// releaseLock gives a lock back, even when ctx has ended; a failure is
// logged and the lock lapses after its ttl.
func (p *Plugin) releaseLock(ctx context.Context, l *pluginsdk.Lock) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lockReleaseTimeout)
	defer cancel()
	if err := l.Release(rctx); err != nil {
		p.log.Warn("codex: release a lock", "lock", l.Name(), "error", err.Error())
	}
}

// deviceExpired is the poll answer of a session that is over, was never there
// or belongs to someone else.
func deviceExpired() *pluginv1.HTTPResponse {
	return pollAnswer(DevicePollResponse{Status: pollExpired, Message: "the device code expired; start again / 设备码已过期，请重新开始"})
}

func sessionUnreadable(err error) *pluginv1.HTTPResponse {
	return pluginsdk.Unavailable("read the session / 读取会话失败: " + err.Error())
}

// pollDevice is one poll of session id of req's caller, under its lock, sent
// as call (this poll's).
func (p *Plugin) pollDevice(ctx context.Context, req *pluginv1.HTTPRequest, id string, call routeCall) *pluginv1.HTTPResponse {
	var s deviceSession
	found, err := p.loadSession(ctx, kvDevice, id, req, &s)
	if err != nil {
		return sessionUnreadable(err)
	}
	now := p.now()
	if !found || now.Unix() >= s.ExpiresAt {
		if found {
			p.deleteSession(ctx, kvDevice, id)
		}
		return deviceExpired()
	}
	pending := func(msg string) *pluginv1.HTTPResponse {
		return pollAnswer(DevicePollResponse{Status: pollPending, Interval: s.Interval, Message: msg})
	}
	// One second of slack for timer jitter in the browser.
	if s.LastPollMs > 0 && now.UnixMilli()-s.LastPollMs < int64(s.Interval-1)*1000 {
		return pending("")
	}
	s.LastPollMs = now.UnixMilli()
	ttl := max(time.Unix(s.ExpiresAt, 0).Sub(now), time.Second)
	if err := p.saveSession(ctx, kvDevice, id, req, s, ttl); err != nil {
		return pluginsdk.Unavailable("store the session / 保存会话失败: " + err.Error())
	}

	a, err := p.postJSON(ctx, call, p.ep.Auth+"/api/accounts/deviceauth/token", map[string]string{"device_auth_id": s.DeviceAuthID, "user_code": s.UserCode})
	if errors.Is(err, errUnreachable) {
		// A network blip must not end the login: the next poll asks again.
		p.log.Warn("codex: device code status request failed", "error", err.Error())
		return pending("cannot reach the OpenAI authorization server, retrying / 暂时无法连接 OpenAI 授权服务器，稍后重试")
	}
	if err != nil {
		// The core would not send it (the form's proxy, the host): nothing
		// reached OpenAI and the device code is unspent, so the login goes
		// on; the next poll carries a fresh ticket of the form's proxy.
		p.log.Warn("codex: device code status request refused by the core", "error", status.Convert(err).Message())
		return pending(refusedMessage(err))
	}
	switch {
	case a.status == http.StatusForbidden || a.status == http.StatusNotFound:
		return pending("")
	case a.status == http.StatusTooManyRequests:
		s.Interval = min(s.Interval+slowDownStep, maxInterval)
		if err := p.saveSession(ctx, kvDevice, id, req, s, ttl); err != nil {
			p.log.Warn("codex: store the slowed-down device code session", "error", err.Error())
		}
		return pending("")
	case !a.ok():
		p.deleteSession(ctx, kvDevice, id)
		p.log.Warn("codex: device code login refused", "status", a.status, "oauth_error", oauthErrorCode(a.body))
		return pollFailed("device code login failed / 设备码登录失败 (" + a.describe() + ")")
	}
	// The code is single-use: whatever happens next, this session is done.
	p.deleteSession(ctx, kvDevice, id)
	return p.exchangeDeviceCode(ctx, call, a)
}

// exchangeDeviceCode turns an approval answer into credentials (one request,
// sent as call).
func (p *Plugin) exchangeDeviceCode(ctx context.Context, call routeCall, approval authAnswer) *pluginv1.HTTPResponse {
	code := strings.TrimSpace(gjson.GetBytes(approval.body, "authorization_code").String())
	verifier := strings.TrimSpace(gjson.GetBytes(approval.body, "code_verifier").String())
	if code == "" || verifier == "" {
		p.log.Warn("codex: device code approval without authorization_code or code_verifier")
		return pollFailed("the approval answer has no authorization_code or code_verifier / 授权响应缺少 authorization_code 或 code_verifier")
	}
	c, f := p.redeemCode(ctx, "device code", call, code, verifier, p.ep.Auth+"/deviceauth/callback")
	if f != nil {
		return pollFailed(f.msg)
	}
	return pollAnswer(DevicePollResponse{Status: pollDone, Credentials: &c})
}

// ---------------------------------------------------------------- Sign in with Codex

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
	if _, refused := p.startCall(req); refused != nil {
		return refused, nil
	}
	var s codexSession
	var err error
	if s.State, err = randomToken(24); err != nil {
		return internalError(msgRandom, err), nil
	}
	if s.Verifier, err = randomToken(48); err != nil { // 64 characters
		return internalError(msgRandom, err), nil
	}
	sessionID, err := p.newSession(ctx, kvCodex, req, s, codexTTL)
	if err != nil {
		return sessionNotStored(err), nil
	}
	q := url.Values{}
	q.Set("client_id", CodexClientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", CodexRedirectURI)
	q.Set("scope", CodexScope)
	q.Set("state", s.State)
	q.Set("code_challenge_method", "S256")
	q.Set("code_challenge", pkceChallenge(s.Verifier))
	// As the Codex CLI asks: the workspaces in the id_token, and the
	// ChatGPT-only consent without the API organization setup.
	q.Set("id_token_add_organizations", "true")
	q.Set("codex_cli_simplified_flow", "true")
	return pluginsdk.DataResponse(CodexStartResponse{
		SessionID: sessionID, AuthorizeURL: p.ep.Auth + "/oauth/authorize?" + q.Encode(),
	}), nil
}

// callbackParams are the parameters of the redirect to the local callback.
type callbackParams struct {
	Code, State             string
	Error, ErrorDescription string
}

// parseCallback reads what the operator pasted: the whole address of the
// page the browser could not open (http://localhost:1455/auth/callback?...),
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
	if f != nil && f.code == "proxy_refused" {
		// Nothing reached OpenAI and the code is unspent: the session stays,
		// and the next call carries a fresh ticket of the form's proxy.
		return flowError(f.code, f.msg), nil
	}
	if f != nil {
		return upstreamError(f.code, f.msg), nil
	}
	p.deleteSession(ctx, kvCodex, id)
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
		p.deleteSession(ctx, kvCodex, id)
		return flowError("access_denied", "authorization was denied / 授权被拒绝 ("+msg+")")
	}
	return flowError("authorization_failed", "authorization failed / 授权失败 ("+msg+")")
}
