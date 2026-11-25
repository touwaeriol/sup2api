package codex

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/classify"
)

// Cooldowns (docs/PLUGIN-CODEX.md §4).
const (
	// authCooldown keeps an account whose access token was refused out of
	// scheduling while the core renews the token (refresh, CONTRACTS §48).
	authCooldown = 5 * time.Minute
	// serverErrorCooldown is the cooldown of a 408 / 5xx / transport failure.
	serverErrorCooldown = 10 * time.Second
)

const (
	// codeCodexUsageLimit is the error type the Codex backend reports for a
	// usage limit; it is passed on to the client as the error code.
	codeCodexUsageLimit = "usage_limit_reached"
	// usageLimitReason is the reason of a used-up usage window.
	usageLimitReason = "Codex usage limit reached"
	// reauthorizeHint ends the reason of a refused token. Reasons are plain
	// English, like the SDK's own (classify).
	reauthorizeHint = "; refresh the token or authorize the account again"
)

// policy classifies the failures of the plugin's accounts. The first
// matching rule wins; an unmatched 4xx is the client's request and is
// returned as is. For a 429 showing a used-up usage window ClassifyError
// puts usageLimitRule first.
var policy = classify.Policy{
	Parse:     parseError,
	ErrorType: errorType,
	Detail:    classify.DetailCodeMessage,
	Rules: []classify.Rule{
		// A CDN / WAF page is not the account's fault.
		classify.EdgeBlock(),
		// A refused access token: usually expired; the core renews it.
		{When: classify.Status(http.StatusUnauthorized), Effect: classify.Cooldown, Cooldown: authCooldown, Reason: "upstream rejected the access token (%d)" + reauthorizeHint},
		// Other 403s with a vendor error body: the account may not use this.
		{When: classify.Status(http.StatusForbidden), Effect: classify.Disable, Reason: "upstream refused the account (%d)"},
		// Any other 429 goes back to the client as is: no cooldown, no
		// disable (only a used-up usage window takes an account out).
		{When: classify.Status(http.StatusTooManyRequests), Return: true, Reason: "upstream 429 without a used-up usage window"},
		classify.ServerErrors(serverErrorCooldown),
	},
}

// usageLimitRule takes an account out until the latest reset of its used-up
// usage windows; the request moves to another account.
func usageLimitRule(until time.Time) classify.Rule {
	return classify.Rule{When: classify.Status(http.StatusTooManyRequests), Effect: classify.Cooldown, Reason: usageLimitReason,
		Until: func(*classify.Input) (time.Time, string, bool) { return until, "x-codex window reset", true }}
}

// ClassifyError implements pluginsdk.Platform. The upstream error code is
// passed on to the client (client_error_code) when it is a plain code.
//
// A 429 whose headers show a used-up usage window disables the account until
// the latest reset of those windows: automatic_state DISABLE, which the core
// applies with or without a published failure policy (CONTRACTS §69) and
// undoes by itself at that time, and a cooldown to the same time where the
// account may not be disabled. The windows themselves reach the console
// through the core's sampling of the declared headers (CONTRACTS §44.3), not
// through this answer.
func (p *Plugin) ClassifyError(_ context.Context, in *pluginv1.ClassifyErrorRequest) (*pluginv1.ClassifyErrorResponse, error) {
	now := p.now()
	until, full := p.usedUpUntil(in, now)
	if !full {
		return withClientCode(policy.Classify(now, in), in), nil
	}
	resp := policy.With(usageLimitRule(until)).Classify(now, in)
	resp.AutomaticState = &pluginv1.AccountAutomaticState{
		Action:  pluginv1.AccountAutomaticState_ACTION_DISABLE,
		Reason:  resp.GetReason(),
		Release: &pluginv1.AccountAutomaticState_UntilUnix{UntilUnix: resp.GetCooldownUntilUnix()},
	}
	return withClientCode(resp, in), nil
}

// withClientCode passes the upstream error code on to the client as
// client_error_code when it is a plain code (a usage limit reports it as the
// error type), with the status it stands for (clientStatuses).
func withClientCode(resp *pluginv1.ClassifyErrorResponse, in *pluginv1.ClassifyErrorRequest) *pluginv1.ClassifyErrorResponse {
	e := parseError(in.GetBodyPrefix())
	code := e.Code
	if code == "" && e.Type == codeCodexUsageLimit {
		code = e.Type
	}
	if clientCode.MatchString(code) {
		resp.ClientErrorCode = code
	}
	if status, ok := clientStatuses[code]; ok {
		resp.ClientStatus = status
	}
	return resp
}

// clientStatuses are the client's statuses of Codex backend error codes whose
// own status says less: an overloaded backend fails a stream it opened (the
// core answers it as a 500) or answers 5xx, and is 529, the "overloaded"
// status clients retry on (docs/PLUGIN-CODEX.md §24). The classification
// (cooldown, failover) stays the one of the upstream status.
var clientStatuses = map[string]int32{"server_is_overloaded": statusOverloaded}

// statusOverloaded is the non-standard "overloaded" HTTP status.
const statusOverloaded = 529

// clientCode is the shape the host accepts for client_error_code.
var clientCode = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)

// parseError reads an OpenAI-style error body ({"error":{"message","type",
// "code"}}), a WebSocket error event carrying the same object, a plain
// {"error":"..."} or a {"detail":"..."} answer.
func parseError(body []byte) classify.Error {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return classify.Error{}
	}
	e := gjson.GetBytes(body, "error")
	switch {
	case e.IsObject():
		return classify.Error{Message: e.Get("message").String(), Type: e.Get("type").String(), Code: e.Get("code").String()}
	case e.Type == gjson.String:
		return classify.Error{Message: e.String()}
	}
	if d := gjson.GetBytes(body, "detail"); d.Type == gjson.String {
		return classify.Error{Message: d.String()}
	}
	return classify.Error{Message: gjson.GetBytes(body, "message").String(), Code: gjson.GetBytes(body, "code").String()}
}

func errorType(in *classify.Input) string {
	switch {
	case in.Status == http.StatusUnauthorized:
		return "authentication_error"
	case in.Status == http.StatusForbidden:
		return "permission_error"
	case in.Status == http.StatusTooManyRequests:
		return "rate_limit_error"
	case in.Status == 0 || in.Status >= 500:
		return "server_error"
	case in.Error.Type != "" && strings.HasSuffix(in.Error.Type, "_error"):
		return in.Error.Type
	default:
		return "invalid_request_error"
	}
}
