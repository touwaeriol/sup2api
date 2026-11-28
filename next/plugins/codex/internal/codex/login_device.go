package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
)

// The device code login (docs/PLUGIN-CODEX.md §2.1): routes
// /auth/device/start and /auth/device/poll.

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
// hashed: it is a secret, and lock names are visible to the host.
func pollLockName(sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	return "device_poll:" + hex.EncodeToString(sum[:16])
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
			p.deleteEntry(ctx, kvDevice, id)
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
	if err != nil {
		// Nothing ended the login: a network blip, or a request the core
		// would not send (the device code is unspent). The next poll asks
		// again, with a fresh ticket of the form's proxy.
		code, detail := sendFailure(err)
		if code == codeProxyRefused {
			p.log.Warn("codex: device code status request refused by the core", "error", detail)
			return pending(refusedMessage(detail))
		}
		p.log.Warn("codex: device code status request failed", "error", detail)
		return pending("cannot reach the OpenAI authorization server, retrying / 暂时无法连接 OpenAI 授权服务器，稍后重试")
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
		p.deleteEntry(ctx, kvDevice, id)
		p.log.Warn("codex: device code login refused", "status", a.status, "oauth_error", oauthErrorCode(a.body))
		return pollFailed("device code login failed / 设备码登录失败 (" + a.describe() + ")")
	}
	// The code is single-use: whatever happens next, this session is done.
	p.deleteEntry(ctx, kvDevice, id)
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
	switch {
	case f == nil:
		return pollAnswer(DevicePollResponse{Status: pollDone, Credentials: &c})
	case f.code == codeProxyRefused:
		// The session is gone: another poll would only find it expired.
		return pollFailed(refusedRestartMessage(f.msg))
	}
	return pollFailed(f.msg)
}
