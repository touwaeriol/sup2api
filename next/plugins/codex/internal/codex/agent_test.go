package codex

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"testing"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

// agentCase is one combination of the plugin settings and the account's own
// client headers, and the headers it sends.
type agentCase struct {
	name     string
	settings map[string]string
	account  string // extra credential fields, JSON members
	want     agent
}

var agentCases = []agentCase{
	{"built-in", nil, ``, agent{UserAgent, Originator}},
	{"empty settings", map[string]string{"user_agent": " ", "originator": ""}, `,"user_agent":"","originator":" "`, agent{UserAgent, Originator}},
	{"settings", map[string]string{"user_agent": "ua-plugin", "originator": "or-plugin"}, ``, agent{"ua-plugin", "or-plugin"}},
	{"account over settings", map[string]string{"user_agent": "ua-plugin", "originator": "or-plugin"}, `,"user_agent":"ua-acct","originator":"or-acct"`, agent{"ua-acct", "or-acct"}},
	{"each header on its own", map[string]string{"user_agent": "ua-plugin"}, `,"originator":"or-acct"`, agent{"ua-plugin", "or-acct"}},
	{"account only", nil, `,"user_agent":" ua-acct "`, agent{"ua-acct", Originator}},
}

// configure applies the case's plugin settings.
func (c agentCase) configure(t *testing.T, h *pluginsdktest.Harness) {
	t.Helper()
	if errs := h.Configure(c.settings, nil); len(errs) > 0 {
		t.Fatalf("%s: configure: %v", c.name, errs)
	}
}

// creds are the account's credentials with the case's client headers.
func (c agentCase) creds() string {
	return `{"access_token":"at-device","refresh_token":"rt-device","chatgpt_account_id":"acct-1"` + c.account + `}`
}

func checkAgent(t *testing.T, what string, c agentCase, ua, originator string) {
	t.Helper()
	if ua != c.want.UserAgent || originator != c.want.Originator {
		t.Errorf("%s %s: user-agent %q originator %q, want %+v", c.name, what, ua, originator, c.want)
	}
}

// The upstream (HTTP and WebSocket), the account test and the token refresh
// send the resolved client headers.
func TestAgentAccountRequests(t *testing.T) {
	h := startExecuting(t, DefaultEndpoints)
	ctx := context.Background()
	for _, c := range agentCases {
		c.configure(t, h)
		for _, protocol := range []string{ProtocolResponses, ProtocolResponsesWS} {
			in := execRequest(TypeDevice, protocol, `{"model":"gpt-5.4"}`)
			in.Account = account(TypeOAuth, c.creds())
			hd := relayed(t, h, in).GetHeaders()
			checkAgent(t, protocol, c, hd["user-agent"], hd["originator"])
		}
		tr, err := h.Platform.BuildTestRequest(ctx, &pluginv1.BuildTestRequestRequest{Account: account(TypeDevice, c.creds())})
		if err != nil {
			t.Fatal(err)
		}
		checkAgent(t, "test", c, tr.GetHeaders()["user-agent"], tr.GetHeaders()["originator"])
		rr, err := h.Platform.BuildRefreshRequest(ctx, &pluginv1.BuildRefreshRequestRequest{Account: account(TypeDevice, c.creds())})
		if err != nil {
			t.Fatal(err)
		}
		checkAgent(t, "refresh", c, rr.GetHeaders()["User-Agent"], rr.GetHeaders()["Originator"])
	}
}

// The usage reset requests send the account's resolved client headers.
func TestAgentResetRequests(t *testing.T) {
	e := newResetEnv(t)
	e.backend = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"available_count":0,"credits":[]}`)
	}
	for _, c := range agentCases {
		c.configure(t, e.h)
		e.host.accounts[resetDeviceID] = resetAccountRow(resetDeviceID, TypeDevice, `{"access_token":"`+resetToken+`"`+c.account+`}`)
		before := len(e.client.requests())
		if r := e.h.Do("POST", "/reset-credits/list", nil, map[string]int64{"account_id": resetDeviceID}); r.GetStatus() != 200 {
			t.Fatalf("%s: list = %d %s", c.name, r.GetStatus(), r.GetBody())
		}
		sent := e.client.requests()
		if len(sent) != before+1 {
			t.Fatalf("%s: %d requests", c.name, len(sent)-before)
		}
		hd := sent[len(sent)-1].headers
		checkAgent(t, "reset", c, hd["user-agent"], hd["originator"])
	}
}

// An authorization runs before the account is saved: every route call
// carries the form's own client headers, and its requests (the device code,
// each poll, both token exchanges) send them resolved as an account's.
func TestAgentAuthorization(t *testing.T) {
	iss := newFakeIssuer(t)
	h := startPlugin(t, newTestPlugin(testEndpoints(iss.srv.URL)))
	iss.on(pathUserCode, reply(200, `{"device_auth_id":"dev-1","user_code":"ABCD","interval":"5"}`))
	iss.on(pathDeviceToken, reply(200, `{"authorization_code":"code-1","code_verifier":"ver-1"}`))
	iss.on(pathOAuthToken, reply(200, `{"access_token":"at-1","refresh_token":"rt-1","expires_in":60}`))
	// call is a route call's body: the ticket, the form's client headers and members.
	call := func(c agentCase, members string) string {
		return `{"proxy_ticket":"` + testTicket + `"` + c.account + members + `}`
	}
	sent := func(c agentCase, what, path string) {
		t.Helper()
		got := iss.headers[path]
		checkAgent(t, what, c, got.Get("User-Agent"), got.Get("Originator"))
	}
	for _, c := range agentCases {
		c.configure(t, h)
		var device DeviceStartResponse
		mustData(t, h.Do("POST", "/auth/device/start", nil, call(c, ``)).GetBody(), &device)
		sent(c, "device code", pathUserCode)
		var poll DevicePollResponse
		mustData(t, h.Do("POST", "/auth/device/poll", nil, call(c, `,"session_id":"`+device.SessionID+`"`)).GetBody(), &poll)
		if poll.Status != pollDone {
			t.Fatalf("%s: poll = %+v", c.name, poll)
		}
		sent(c, "poll", pathDeviceToken)
		sent(c, "device code exchange", pathOAuthToken)

		var codex CodexStartResponse
		mustData(t, h.Do("POST", "/auth/codex/start", nil, call(c, ``)).GetBody(), &codex)
		u, _ := url.Parse(codex.AuthorizeURL)
		cb := `,"session_id":"` + codex.SessionID + `","callback_url":"?code=code-2&state=` + u.Query().Get("state") + `"`
		if r := h.Do("POST", "/auth/codex/exchange", nil, call(c, cb)); r.GetStatus() != 200 {
			t.Fatalf("%s: exchange = %d %s", c.name, r.GetStatus(), r.GetBody())
		}
		sent(c, "Sign in with Codex exchange", pathOAuthToken)
	}
}

// The form's client headers are checked as the account's are: a value that
// is not a string or cannot be a header value refuses the call, nothing sent.
func TestAgentAuthorizationRefusesBadValues(t *testing.T) {
	iss := newFakeIssuer(t)
	h := startPlugin(t, newTestPlugin(testEndpoints(iss.srv.URL)))
	for _, bad := range []struct{ field, value string }{{"user_agent", `"ua\nx"`}, {"originator", `7`}} {
		for _, path := range []string{"/auth/device/start", "/auth/device/poll", "/auth/codex/start", "/auth/codex/exchange"} {
			r := h.Do("POST", path, nil, `{"proxy_ticket":"`+testTicket+`","session_id":"s","callback_url":"?code=c","`+bad.field+`":`+bad.value+`}`)
			if e := decodeError(t, r); r.GetStatus() != 400 || len(e.Error.Details.Fields) != 1 || e.Error.Details.Fields[0].Field != bad.field {
				t.Errorf("%s with %s %s = %d %s", path, bad.field, bad.value, r.GetStatus(), r.GetBody())
			}
		}
	}
	if n := iss.count(pathUserCode) + iss.count(pathDeviceToken) + iss.count(pathOAuthToken); n != 0 {
		t.Fatalf("%d refused calls reached OpenAI", n)
	}
}

// TestValidateSettings: every value the settings JSON Schema refused (one
// counterexample per constraint, written out here) is still refused, at its
// field, by ValidateSettings and Configure alike; the default set (both
// empty) and real values pass.
func TestValidateSettings(t *testing.T) {
	h := startPlugin(t, mustPlugin(DefaultEndpoints))
	refused := []struct{ settings, field, code string }{
		{`{"user_agent":"a\nb"}`, "user_agent", "pattern"},
		{`{"user_agent":"a\u0000"}`, "user_agent", "pattern"},
		{`{"originator":"x\u007f"}`, "originator", "pattern"},
		{`{"originator":"x\u001f"}`, "originator", "pattern"},
		{`{"user_agent":1}`, "user_agent", "type"},
		{`{"originator":null}`, "originator", "type"},
		{`{"user-agent":"x"}`, "user-agent", "unknown"},
		{`[1]`, "", "invalid_json"},
	}
	for _, c := range refused {
		resp, err := h.ValidateSettings(c.settings, nil)
		if err != nil {
			t.Fatalf("%s: %v", c.settings, err)
		}
		if errs := resp.GetErrors(); len(errs) != 1 || errs[0].GetField() != c.field || errs[0].GetCode() != c.code {
			t.Errorf("%s: errors = %v, want %s %s", c.settings, errs, c.field, c.code)
		}
		if _, ok := pluginsdk.AsFieldErrors(mustPlugin(DefaultEndpoints).Configure(context.Background(), pluginsdk.Config{JSON: []byte(c.settings)})); !ok {
			t.Errorf("%s: Configure accepted it", c.settings)
		}
	}
	for _, ok := range []string{`{}`, `{"user_agent":"","originator":""}`, `{"user_agent":"sup2api-codex/0.2.0\tx","originator":"codex_cli_rs"}`} {
		if resp, err := h.ValidateSettings(ok, nil); err != nil || len(resp.GetErrors()) != 0 || resp.GetNormalizedConfigJson() != "" {
			t.Errorf("%s: %v %v", ok, resp.GetErrors(), err)
		}
	}
}
