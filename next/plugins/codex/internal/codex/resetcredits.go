package codex

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/textutil"
)

// Official usage resets ("rate-limit reset credits", docs/PLUGIN-CODEX.md
// §10): a ChatGPT plan may grant a few credits, each of which resets the
// account's used-up Codex usage windows once. Two admin routes (permission
// codex:reset) list and spend them for ONE account of the plugin (either
// type: both hold Codex tokens).
//
// The plugin describes each request; the core sends it through the account's
// proxy (pluginsdk.SendAccountRequest), with the account's client headers
// (agent.go) like every other request. The account list reads cached counts instead
// (resetcache.go). Answers follow the authorization routes (oauth.go):
// {"data": ...} on success, {"error":{"code","message"}} otherwise, messages
// "English / 中文".

const (
	resetCreditsPath = "/backend-api/wham/rate-limit-reset-credits"
	resetConsumePath = resetCreditsPath + "/consume"

	// accountRequestTimeout is the core's bound on one SendAccountRequest
	// (CONTRACTS §91.1).
	accountRequestTimeout = 30 * time.Second
	// resetLockTTL covers one consume under its lock: the request and its
	// single retry, then CLEAR and the cache drop. It must outlast both sends,
	// or a second click could start a consume with a new redeem_request_id
	// while the retry is still in flight.
	resetLockTTL = 2*accountRequestTimeout + 30*time.Second
	// maxSnippet bounds the upstream text an error message carries.
	maxSnippet = 200
)

// Consume outcomes (the backend's "code").
const (
	outcomeReset           = "reset"
	outcomeAlreadyRedeemed = "already_redeemed"
	outcomeNothingToReset  = "nothing_to_reset"
	outcomeNoCredit        = "no_credit"
)

// ResetCreditsRequest is the body of POST /reset-credits/list.
type ResetCreditsRequest struct {
	AccountID int64 `json:"account_id"`
}

// ResetCredit is one credit of the list answer. Times are Unix seconds:
// GrantedAt 0 = unknown, ExpiresAt nil = never or unknown.
type ResetCredit struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	GrantedAt   int64  `json:"granted_at"`
	ExpiresAt   *int64 `json:"expires_at"`
}

// ResetCreditsResponse answers POST /reset-credits/list; credits are ordered
// by expiry, the soonest first and those without one last.
type ResetCreditsResponse struct {
	AvailableCount int           `json:"available_count"`
	Credits        []ResetCredit `json:"credits"`
}

// ResetConsumeRequest is the body of POST /reset-credits/consume: the
// console's row action shape, which must name exactly one account here.
// CreditID is optional (the backend then picks the next credit).
type ResetConsumeRequest struct {
	AccountIDs []int64 `json:"account_ids"`
	CreditID   string  `json:"credit_id"`
}

// ResetConsumeResponse answers a consume that spent (or had already spent)
// the credit. AccountRestored reports whether the core cleared the account's
// automatic state (the disable or cool-down of a used-up window).
type ResetConsumeResponse struct {
	Outcome         string `json:"outcome"`
	WindowsReset    int    `json:"windows_reset"`
	AccountRestored bool   `json:"account_restored"`
}

// consumeBody is the backend's consume request; the same redeem_request_id
// makes a retry idempotent.
type consumeBody struct {
	RedeemRequestID string `json:"redeem_request_id"`
	CreditID        string `json:"credit_id,omitempty"`
}

// handleResetList answers the account's credits (one request).
func (p *Plugin) handleResetList(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	var in ResetCreditsRequest
	if err := decodeOptional(req, &in); err != nil {
		return errInvalidJSON(), nil
	}
	if in.AccountID <= 0 {
		return pluginsdk.BadRequest("account_id", "required", "account_id is required / 缺少 account_id"), nil
	}
	c, bad := p.resetAccount(ctx, in.AccountID)
	if bad != nil {
		return bad, nil
	}
	r, bad := p.sendBackend(ctx, in.AccountID, c, http.MethodGet, resetCreditsPath, nil)
	if bad != nil {
		return bad, nil
	}
	list, err := parseResetCredits(r.GetBody())
	if err != nil {
		return upstreamError("upstream_error", "unreadable reset credits answer / 无法解析重置次数响应: "+err.Error()), nil
	}
	p.storeResetCount(ctx, in.AccountID, list.AvailableCount)
	return pluginsdk.DataResponse(list), nil
}

// handleResetConsume spends one credit of one account, under a cluster lock
// per account so a double click or two nodes cannot spend two credits.
func (p *Plugin) handleResetConsume(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	var in ResetConsumeRequest
	if err := decodeOptional(req, &in); err != nil {
		return errInvalidJSON(), nil
	}
	if len(in.AccountIDs) != 1 || in.AccountIDs[0] <= 0 {
		return pluginsdk.BadRequest("account_ids", "count", "name exactly one account: a reset is used one account at a time / 只能指定一个账号：重置次数一次只对一个账号使用"), nil
	}
	id := in.AccountIDs[0]
	c, bad := p.resetAccount(ctx, id)
	if bad != nil {
		return bad, nil
	}
	lock, ok, err := p.host.Locks().TryAcquire(ctx, resetLockName(id), resetLockTTL)
	if err != nil {
		p.log.Warn("codex: lock the account for a reset", "account_id", id, "error", err.Error())
		return pluginsdk.Unavailable("lock the account / 锁定账号失败: " + err.Error()), nil
	}
	if !ok {
		return pluginsdk.ErrorResponse(http.StatusConflict, "busy", "a reset of this account is already running; wait for it / 该账号正在使用重置次数，请等待其完成"), nil
	}
	defer p.releaseLock(ctx, lock)

	outcome, windows, bad := p.consumeReset(ctx, id, c, strings.TrimSpace(in.CreditID))
	if bad != nil {
		return bad, nil
	}
	switch outcome {
	case outcomeNothingToReset:
		return pluginsdk.ErrorResponse(http.StatusConflict, outcomeNothingToReset, "No usage window can be reset right now; no reset was used / 当前没有可重置的用量窗口，未消耗次数"), nil
	case outcomeNoCredit:
		return pluginsdk.ErrorResponse(http.StatusConflict, outcomeNoCredit, "The account has no reset left / 账号没有剩余重置次数"), nil
	}
	// A credit was (or, for an unknown code, may have been) spent: the
	// cached count is stale until the next list writes it.
	p.deleteCached(ctx, kvResetCount, resetCountKey(id))
	if outcome != outcomeReset && outcome != outcomeAlreadyRedeemed {
		return upstreamError("upstream_error", "unknown reset outcome / 未知的重置结果: "+strconv.Quote(textutil.Clip(outcome, 64))), nil
	}
	return pluginsdk.DataResponse(ResetConsumeResponse{Outcome: outcome, WindowsReset: windows, AccountRestored: p.restoreAccount(ctx, id)}), nil
}

// consumeReset sends the consume request. A transport failure (status 0)
// is retried once with the same redeem_request_id, so the backend spends at
// most one credit whichever attempt reached it.
func (p *Plugin) consumeReset(ctx context.Context, id int64, c credentials, creditID string) (outcome string, windows int, bad *pluginv1.HTTPResponse) {
	body, _ := json.Marshal(consumeBody{RedeemRequestID: newUUID(), CreditID: creditID})
	r, bad := p.sendBackend(ctx, id, c, http.MethodPost, resetConsumePath, body)
	if r != nil && r.GetStatus() == 0 {
		p.log.Warn("codex: reset consume transport failure, retrying once", "account_id", id, "error", r.GetTransportError())
		r, bad = p.sendBackend(ctx, id, c, http.MethodPost, resetConsumePath, body)
	}
	if bad != nil {
		return "", 0, bad
	}
	res := gjson.ParseBytes(r.GetBody())
	return res.Get("code").String(), int(res.Get("windows_reset").Int()), nil
}

// restoreAccount clears the account's automatic state (the disable or
// cool-down a used-up window set). A failure is only logged: the reset has
// been spent either way, and the state lapses at the window's reset.
func (p *Plugin) restoreAccount(ctx context.Context, id int64) bool {
	_, err := pluginsdk.SetAccountAutomaticState(ctx, p.host, &pluginv1.SetAccountAutomaticStateRequest{
		AccountId: id,
		State:     &pluginv1.AccountAutomaticState{Action: pluginv1.AccountAutomaticState_ACTION_CLEAR},
	})
	if err != nil {
		p.log.Warn("codex: clear the account state after a reset", "account_id", id, "error", err.Error())
		return false
	}
	return true
}

// resetLockName is the reset lock of one account.
func resetLockName(id int64) string { return "codex-reset:" + strconv.FormatInt(id, 10) }

// resetAccount reads an account's credentials for the reset routes; bad is
// the route answer when it cannot be used.
func (p *Plugin) resetAccount(ctx context.Context, id int64) (c credentials, bad *pluginv1.HTTPResponse) {
	if p.host == nil {
		return c, errNotReady()
	}
	acc, err := p.host.Account(ctx, id)
	if status.Code(err) == codes.NotFound {
		return c, pluginsdk.ErrorResponse(http.StatusNotFound, "account_not_found", "no such ChatGPT (Codex) account / 找不到该 ChatGPT（Codex）账号")
	}
	if err != nil {
		return c, pluginsdk.Unavailable("read the account / 读取账号失败: " + err.Error())
	}
	c, err = parseCredentials(acc.GetCredentialsJson())
	if err != nil || c.AccessToken == "" {
		return c, pluginsdk.ErrorResponse(http.StatusConflict, "not_authorized", "the account has no usable access_token; authorize it again / 账号没有可用的 access_token，请重新授权")
	}
	return c, nil
}

// backendHeaders are the headers of a request to the ChatGPT backend's JSON
// endpoints (reset credits).
func (p *Plugin) backendHeaders(c credentials) map[string]string {
	h := p.accountHeaders(c)
	h["accept"] = "application/json"
	return h
}

// sendBackend has the core send one request to the ChatGPT backend for the
// account, with backendHeaders. r is the backend's answer; bad is the route
// answer when it is not a usable 2xx (r is still returned then).
func (p *Plugin) sendBackend(ctx context.Context, id int64, c credentials, method, path string, body []byte) (r *pluginv1.ExecutionHTTPResponse, bad *pluginv1.HTTPResponse) {
	h := p.backendHeaders(c)
	if body != nil {
		h["content-type"] = "application/json"
	}
	r, err := pluginsdk.SendAccountRequest(ctx, p.host, &pluginv1.SendAccountRequestRequest{
		AccountId: id, Method: method, Url: p.ep.ChatGPT + path, Headers: h, Body: body,
	})
	if err != nil {
		p.log.Warn("codex: send an account request", "account_id", id, "path", path, "error", err.Error())
		return nil, pluginsdk.Unavailable("the core could not send the request / 核心无法发送请求: " + err.Error())
	}
	return r, backendFailure(r, c.AccessToken)
}

// backendFailure maps an answer that is not a usable 2xx to the route
// answer; nil for a usable one. The quoted upstream text never carries the
// account's token.
func backendFailure(r *pluginv1.ExecutionHTTPResponse, token string) *pluginv1.HTTPResponse {
	st := r.GetStatus()
	switch {
	case st == 0:
		return upstreamError("upstream_error", "cannot reach ChatGPT / 无法连接 ChatGPT: "+snippet(r.GetTransportError(), token))
	case st == http.StatusUnauthorized || st == http.StatusForbidden:
		return pluginsdk.ErrorResponse(http.StatusConflict, "auth_rejected", fmt.Sprintf("ChatGPT rejected the account's token (status %d); authorize the account again / ChatGPT 拒绝了账号令牌（状态 %d），请重新授权", st, st))
	case st < 200 || st > 299:
		return upstreamError("upstream_error", fmt.Sprintf("ChatGPT answered status %d / ChatGPT 返回状态 %d: %s", st, st, snippet(string(r.GetBody()), token)))
	}
	return nil
}

// snippet is a short, token-free excerpt of upstream text for a message.
func snippet(s, token string) string {
	if token != "" {
		s = strings.ReplaceAll(s, token, "[redacted]")
	}
	return textutil.Clip(strings.TrimSpace(s), maxSnippet)
}

// parseResetCredits reads the list answer leniently: unknown statuses and
// reset types pass through, and an unreadable time is 0 (granted_at) or
// null (expires_at). Without available_count the count is the credits whose
// status is available, so the cached count never drops to 0 by omission.
func parseResetCredits(body []byte) (ResetCreditsResponse, error) {
	if !gjson.ValidBytes(body) {
		return ResetCreditsResponse{}, errors.New("not JSON / 不是 JSON")
	}
	res := gjson.ParseBytes(body)
	out := ResetCreditsResponse{Credits: []ResetCredit{}}
	res.Get("credits").ForEach(func(_, c gjson.Result) bool {
		granted, _ := parseTime(c.Get("granted_at").String())
		cr := ResetCredit{
			ID: c.Get("id").String(), Title: c.Get("title").String(), Description: c.Get("description").String(),
			Status: c.Get("status").String(), GrantedAt: granted,
		}
		if exp, ok := parseTime(c.Get("expires_at").String()); ok {
			cr.ExpiresAt = &exp
		}
		out.Credits = append(out.Credits, cr)
		return true
	})
	slices.SortStableFunc(out.Credits, byExpiry)
	if n := res.Get("available_count"); n.Exists() {
		out.AvailableCount = int(n.Int())
	} else {
		for _, c := range out.Credits {
			if c.Status == "available" {
				out.AvailableCount++
			}
		}
	}
	return out, nil
}

// byExpiry orders credits by expiry, those without one last.
func byExpiry(a, b ResetCredit) int {
	switch {
	case a.ExpiresAt == nil && b.ExpiresAt == nil:
		return 0
	case a.ExpiresAt == nil:
		return 1
	case b.ExpiresAt == nil:
		return -1
	}
	return cmp.Compare(*a.ExpiresAt, *b.ExpiresAt)
}

// parseTime reads an RFC 3339 time as Unix seconds.
func parseTime(s string) (int64, bool) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return 0, false
	}
	return t.Unix(), true
}
