package codex

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

// Every request of both logins is a fetch the core sends through the
// account form's proxy (CONTRACTS §117): each carries the ticket of its own
// route call, so a proxy the operator changed after the login started
// carries the next request, and the method, URL, headers and body are
// exactly those the plugin sent itself before.
func TestAuthRequestsCarryTheTicket(t *testing.T) {
	iss := newFakeIssuer(t)
	h := startPlugin(t, newTestPlugin(testEndpoints(iss.srv.URL)))
	var mu sync.Mutex
	var fetches []*pluginv1.ExecutionHTTPRequest
	h.Host.ExecuteHTTPFunc = func(ctx context.Context, in *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
		mu.Lock()
		fetches = append(fetches, in)
		mu.Unlock()
		return sendFetch(ctx, in)
	}
	tok := idToken("me@example.com", "acct-1", "plus")
	iss.on(pathUserCode, reply(200, `{"device_auth_id":"dev-1","user_code":"ABCD","interval":"5"}`))
	iss.on(pathDeviceToken, reply(200, `{"authorization_code":"code-1","code_verifier":"ver-1"}`))
	iss.on(pathOAuthToken, reply(200, `{"access_token":"at-1","refresh_token":"rt-1","id_token":"`+tok+`","expires_in":60}`))

	var device DeviceStartResponse
	mustData(t, h.Do("POST", "/auth/device/start", nil, startBody).GetBody(), &device)
	var poll DevicePollResponse
	// The operator picked another proxy after starting: the poll's ticket.
	mustData(t, h.Do("POST", "/auth/device/poll", nil, map[string]string{"proxy_ticket": "poll-ticket", "session_id": device.SessionID}).GetBody(), &poll)
	if poll.Status != pollDone {
		t.Fatalf("device login = %+v", poll)
	}
	var codex CodexStartResponse
	mustData(t, h.Do("POST", "/auth/codex/start", nil, map[string]string{"proxy_ticket": "another-ticket"}).GetBody(), &codex)
	u, _ := url.Parse(codex.AuthorizeURL)
	r := h.Do("POST", "/auth/codex/exchange", nil, map[string]string{"proxy_ticket": "exchange-ticket", "session_id": codex.SessionID, "callback_url": "?code=code-2&state=" + u.Query().Get("state")})
	if r.GetStatus() != 200 {
		t.Fatalf("codex login = %d %s", r.GetStatus(), r.GetBody())
	}

	headers := func(contentType string) string {
		return fmt.Sprint(map[string]string{"Accept": "application/json", "Content-Type": contentType, "Originator": "sup2api", "User-Agent": "sup2api-codex/" + Version})
	}
	form := func(code, verifier, redirect string) string {
		return url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "client_id": {CodexClientID}, "code_verifier": {verifier}}.Encode()
	}
	want := []struct{ ticket, url, headers, body string }{
		{testTicket, iss.srv.URL + pathUserCode, headers("application/json"), `{"client_id":"` + CodexClientID + `"}`},
		{"poll-ticket", iss.srv.URL + pathDeviceToken, headers("application/json"), `{"device_auth_id":"dev-1","user_code":"ABCD"}`},
		{"poll-ticket", iss.srv.URL + pathOAuthToken, headers("application/x-www-form-urlencoded"), form("code-1", "ver-1", iss.srv.URL+"/deviceauth/callback")},
		{"exchange-ticket", iss.srv.URL + pathOAuthToken, headers("application/x-www-form-urlencoded"), ""},
	}
	if len(fetches) != len(want) {
		t.Fatalf("%d fetches, want %d", len(fetches), len(want))
	}
	for i, w := range want {
		f := fetches[i]
		if f.GetMethod() != "POST" || f.GetProxyTicket() != w.ticket || f.GetUrl() != w.url || fmt.Sprint(f.GetHeaders()) != w.headers ||
			(w.body != "" && string(f.GetBody()) != w.body) {
			t.Errorf("fetch %d = %v %s %s %v %s, want %+v", i, f.GetMethod(), f.GetProxyTicket(), f.GetUrl(), f.GetHeaders(), f.GetBody(), w)
		}
	}
	// The PKCE verifier of the Codex login is the session's, the form the same.
	sent, _ := url.ParseQuery(string(fetches[3].GetBody()))
	if string(fetches[3].GetBody()) != form("code-2", sent.Get("code_verifier"), CodexRedirectURI) || pkceChallenge(sent.Get("code_verifier")) != u.Query().Get("code_challenge") {
		t.Errorf("codex exchange body = %s", fetches[3].GetBody())
	}
	// What reached OpenAI: the plugin's headers and nothing of the proxy.
	for _, path := range []string{pathUserCode, pathOAuthToken} {
		got := iss.headers[path]
		if got.Get("User-Agent") != UserAgent || got.Get("Originator") != Originator || got.Get("Accept") != "application/json" || got.Get("Proxy-Authorization") != "" {
			t.Errorf("%s headers = %v", path, got)
		}
	}
}

// Every route of a login needs the proxy ticket of its call, a start route
// a caller too. A request the core refuses to send keeps the login (a start
// or an exchange answers proxy_refused, a poll "pending" with the reason):
// nothing reached OpenAI, so once the operator fixes the proxy the next
// call, with its fresh ticket, completes it.
func TestAuthProxyRefused(t *testing.T) {
	iss := newFakeIssuer(t)
	clk := &clock{t: testNow}
	p := newTestPlugin(testEndpoints(iss.srv.URL))
	p.now = clk.now
	h := startPlugin(t, p)
	for _, path := range []string{"/auth/device/start", "/auth/codex/start", "/auth/device/poll", "/auth/codex/exchange"} {
		r := h.Do("POST", path, nil, map[string]string{"proxy_ticket": " ", "session_id": "s", "callback_url": "?code=c"})
		if e := decodeError(t, r); r.GetStatus() != 400 || len(e.Error.Details.Fields) != 1 || e.Error.Details.Fields[0].Field != "proxy_ticket" {
			t.Errorf("%s without a ticket = %d %s", path, r.GetStatus(), r.GetBody())
		}
	}

	iss.on(pathUserCode, reply(200, `{"device_auth_id":"dev-1","user_code":"ABCD","interval":"5"}`))
	var device DeviceStartResponse
	mustData(t, h.Do("POST", "/auth/device/start", nil, startBody).GetBody(), &device)
	var codex CodexStartResponse
	mustData(t, h.Do("POST", "/auth/codex/start", nil, startBody).GetBody(), &codex)

	h.Host.ExecuteHTTPFunc = func(context.Context, *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
		return nil, status.Error(codes.PermissionDenied, "the proxy ticket was not issued to the caller of this route for this plugin")
	}
	r := h.Do("POST", "/auth/device/start", nil, startBody)
	if r.GetStatus() != 400 || decodeError(t, r).Error.Code != "proxy_refused" || !strings.Contains(string(r.GetBody()), "not issued to the caller") {
		t.Fatalf("refused start = %d %s", r.GetStatus(), r.GetBody())
	}
	var poll DevicePollResponse
	mustData(t, h.Do("POST", "/auth/device/poll", nil, map[string]string{"proxy_ticket": testTicket, "session_id": device.SessionID}).GetBody(), &poll)
	if poll.Status != pollPending || !strings.Contains(poll.Message, "not issued to the caller") {
		t.Fatalf("refused poll = %+v", poll)
	}
	u, _ := url.Parse(codex.AuthorizeURL)
	exchange := map[string]string{"proxy_ticket": testTicket, "session_id": codex.SessionID, "callback_url": "?code=c&state=" + u.Query().Get("state")}
	r = h.Do("POST", "/auth/codex/exchange", nil, exchange)
	if r.GetStatus() != 400 || decodeError(t, r).Error.Code != "proxy_refused" {
		t.Fatalf("refused exchange = %d %s", r.GetStatus(), r.GetBody())
	}
	if n := iss.count(pathDeviceToken) + iss.count(pathOAuthToken); n != 0 {
		t.Fatalf("%d refused requests reached OpenAI", n)
	}

	// The proxy is fixed: the same sessions complete.
	h.Host.ExecuteHTTPFunc = sendFetch
	iss.on(pathDeviceToken, reply(200, `{"authorization_code":"code-1","code_verifier":"ver-1"}`))
	iss.on(pathOAuthToken, reply(200, `{"access_token":"at-1","refresh_token":"rt-1","id_token":"`+idToken("me@example.com", "acct-1", "plus")+`","expires_in":60}`))
	clk.add(5 * time.Second)
	mustData(t, h.Do("POST", "/auth/device/poll", nil, map[string]string{"proxy_ticket": testTicket, "session_id": device.SessionID}).GetBody(), &poll)
	if poll.Status != pollDone {
		t.Fatalf("poll after a refusal = %+v", poll)
	}
	if r = h.Do("POST", "/auth/codex/exchange", nil, exchange); r.GetStatus() != 200 {
		t.Fatalf("exchange after a refusal = %d %s", r.GetStatus(), r.GetBody())
	}
}
