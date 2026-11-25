package codex

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

// overloadedBody is the error object of the overload error event the ChatGPT
// backends sent on 2026-11-25, as the core hands it to ClassifyError.
const overloadedBody = `{"error":{"message":"Our servers are currently overloaded. Please try again later.",` +
	`"type":"service_unavailable_error","param":null,"code":"server_is_overloaded",` +
	`"headers":{"retry-after":"309","retry-after-ms":"309000"}}}`

func TestClassifyError(t *testing.T) {
	p := newTestPlugin(DefaultEndpoints)
	now := testNow.Unix()
	const (
		ret     = pluginv1.ClassifyErrorResponse_ACTION_RETURN_TO_CLIENT
		fail    = pluginv1.ClassifyErrorResponse_ACTION_FAILOVER
		none    = pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_UNSPECIFIED
		cool    = pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_COOLDOWN
		disable = pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_DISABLE
	)
	full5h := slotHeaders(map[string]string{}, "primary", "300", "100", "1800003600")
	weekAhead := strconv.FormatInt(now+8*24*3600, 10)
	for _, tc := range []struct {
		name         string
		status       int32
		headers      map[string]string
		body         string
		transport    string
		action       pluginv1.ClassifyErrorResponse_Action
		effect       pluginv1.ClassifyErrorResponse_AccountEffect
		until        int64 // cooldown and automatic disable; 0 = no automatic disable
		code         string
		clientStatus int32  // client_status, 0 = the upstream status
		reason       string // substring
	}{
		{name: "429 with a full 5h window", status: 429,
			headers: slotHeaders(slotHeaders(map[string]string{}, "primary", "300", "100", "1800003600"), "secondary", "10080", "30", "1800500000"),
			body:    `{"error":{"type":"usage_limit_reached","message":"The usage limit has been reached"}}`,
			action:  fail, effect: cool, until: 1800003600, code: "usage_limit_reached", reason: "Codex usage limit reached until "},
		{name: "429 with a full weekly window in the primary slot", status: 429,
			headers: slotHeaders(slotHeaders(map[string]string{}, "primary", "10080", "100.0", "1800090000"), "secondary", "300", "12", "1800001000"),
			body:    `{"error":{"message":"slow down"}}`, action: fail, effect: cool, until: 1800090000},
		{name: "429 with both windows full: the latest reset", status: 429,
			headers: slotHeaders(slotHeaders(map[string]string{}, "primary", "300", "100", "1800003600"), "secondary", "10080", "100", "1800500000"),
			action:  fail, effect: cool, until: 1800500000},
		{name: "429 with a full window of another length", status: 429,
			headers: slotHeaders(map[string]string{}, "secondary", "1440", "100", "1800050000"),
			action:  fail, effect: cool, until: 1800050000},
		{name: "429 reset clamped to a week", status: 429,
			headers: slotHeaders(map[string]string{}, "primary", "10080", "100", weekAhead),
			action:  fail, effect: cool, until: now + 7*24*3600},
		{name: "429 full without reset-at goes back", status: 429,
			headers: slotHeaders(map[string]string{"retry-after": "120"}, "primary", "300", "100", ""),
			body:    `{"error":{"type":"usage_limit_reached","resets_in_seconds":600}}`,
			action:  ret, effect: none, code: "usage_limit_reached", reason: "without a used-up usage window"},
		{name: "429 with windows below 100 goes back", status: 429,
			headers: slotHeaders(slotHeaders(map[string]string{}, "primary", "300", "99.5", "1800003600"), "secondary", "10080", "40", "1800500000"),
			body:    `{"error":{"message":"slow down"}}`, action: ret, effect: none},
		{name: "plain 429 goes back", status: 429, body: `{"error":{"message":"slow down","code":"rate_limit_exceeded"}}`,
			action: ret, effect: none, code: "rate_limit_exceeded", reason: "without a used-up usage window"},
		{name: "usage_limit_reached body alone goes back", status: 429,
			body:   `{"error":{"type":"usage_limit_reached","resets_at":1800007200}}`,
			action: ret, effect: none, code: "usage_limit_reached"},
		{name: "websocket usage_limit_reached event goes back", status: 429,
			body:   `{"type":"error","status":429,"error":{"type":"usage_limit_reached","resets_in_seconds":60}}`,
			action: ret, effect: none, code: "usage_limit_reached"},
		{name: "usage_limit_reached on another status goes back", status: 400,
			body:   `{"error":{"type":"usage_limit_reached"}}`,
			action: ret, effect: none, code: "usage_limit_reached"},
		{name: "200 with a full window", status: 200, headers: full5h, action: ret, effect: none},
		{name: "503 with a full window", status: 503, headers: full5h, body: `bad gateway`, action: fail, effect: cool},
		{name: "401 token", status: 401, headers: full5h, body: `{"detail":"Unauthorized"}`,
			action: fail, effect: cool, reason: "authorize the account again"},
		{name: "403 edge page", status: 403, headers: map[string]string{"content-type": "text/html"}, body: `<!DOCTYPE html><title>Just a moment...</title>`,
			action: fail, effect: none},
		{name: "403 json", status: 403, body: `{"error":{"message":"forbidden"}}`,
			action: fail, effect: disable},
		{name: "server error", status: 502, body: `bad gateway`, action: fail, effect: cool},
		{name: "bad request", status: 400, body: `{"error":{"message":"Unsupported parameter: foo","type":"invalid_request_error","code":"unsupported_parameter"}}`,
			action: ret, effect: none, code: "unsupported_parameter"},
		{name: "transport", status: 0, transport: "connection reset", action: fail, effect: cool, clientStatus: 502},
		// The error the ChatGPT backends sent on 2026-11-25: as the core hands
		// over an early stream error (500) and a stream-only one (502).
		{name: "overloaded stream error", status: 500, body: overloadedBody,
			action: fail, effect: cool, code: "server_is_overloaded", clientStatus: 529, reason: "500"},
		{name: "overloaded on a stream-only answer", status: 502, body: overloadedBody,
			action: fail, effect: cool, code: "server_is_overloaded", clientStatus: 529},
		{name: "server_error keeps the upstream status", status: 500,
			body:   `{"error":{"message":"An error occurred while processing your request.","type":"server_error","param":null,"code":"server_error"}}`,
			action: fail, effect: cool, code: "server_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := p.ClassifyError(context.Background(), &pluginv1.ClassifyErrorRequest{
				Meta: &pluginv1.RequestMeta{Protocol: ProtocolResponses}, Account: account(TypeDevice, `{}`),
				Status: tc.status, Headers: tc.headers, BodyPrefix: []byte(tc.body), TransportError: tc.transport,
			})
			if err != nil {
				t.Fatal(err)
			}
			if r.GetAction() != tc.action || r.GetAccountEffect() != tc.effect {
				t.Fatalf("action/effect = %v %v, want %v %v (%s)", r.GetAction(), r.GetAccountEffect(), tc.action, tc.effect, r.GetReason())
			}
			if tc.code != "" && r.GetClientErrorCode() != tc.code {
				t.Fatalf("client code = %q, want %q", r.GetClientErrorCode(), tc.code)
			}
			if r.GetClientStatus() != tc.clientStatus {
				t.Fatalf("client status = %d, want %d", r.GetClientStatus(), tc.clientStatus)
			}
			if !strings.Contains(r.GetReason(), tc.reason) {
				t.Fatalf("reason = %q, want %q", r.GetReason(), tc.reason)
			}
			if r.GetQuota() != nil {
				t.Fatalf("quota = %+v, want none (the core samples the headers)", r.GetQuota())
			}
			st := r.GetAutomaticState()
			if tc.until == 0 {
				if st != nil {
					t.Fatalf("automatic state = %+v, want none", st)
				}
				return
			}
			if st.GetAction() != pluginv1.AccountAutomaticState_ACTION_DISABLE || st.GetUntilUnix() != tc.until || st.GetReason() != r.GetReason() ||
				r.GetCooldownUntilUnix() != tc.until {
				t.Fatalf("automatic state %+v, cooldown until %s, want both %s", st,
					time.Unix(r.GetCooldownUntilUnix(), 0).UTC(), time.Unix(tc.until, 0).UTC())
			}
		})
	}
}
