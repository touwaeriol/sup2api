package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

// Codex logins (docs/PLUGIN-CODEX.md §19, CONTRACTS §117): the console asks
// the core for a proxy ticket of the account form's proxy choice, hands it to
// the plugin's start route, and every request of the login reaches
// auth.openai.com (the mock) as a fetch the core sends through that proxy.
// The mock is the proxy too: a request through it is recorded proxied.

const codexAuthHost = "auth.openai.com"

// codexProxyTicket is POST /account-types/codex/:typ/proxy-ticket for the
// form's proxy choice ({proxy_id}, {proxy_url} or {} for none).
func codexProxyTicket(t testing.TB, admin *Session, typ string, choice map[string]any) string {
	t.Helper()
	tk := admin.OK(t, http.MethodPost, "/account-types/codex/"+typ+"/proxy-ticket", choice).Get("ticket").String()
	if tk == "" {
		t.Fatalf("proxy-ticket %s %v: no ticket", typ, choice)
	}
	return tk
}

// codexDeviceLogin runs the device code login as the console does: start,
// then poll at the interval the plugin answers until it is no longer
// pending, each call with a fresh ticket of the choice (the session's own lifetime ends a login that never
// finishes). It returns the credentials.
func codexDeviceLogin(t testing.TB, admin *Session, choice map[string]any) gjson.Result {
	t.Helper()
	start := admin.OK(t, http.MethodPost, "/p/codex/auth/device/start",
		map[string]any{"proxy_ticket": codexProxyTicket(t, admin, "codex_device", choice)})
	sid, interval := start.Get("session_id").String(), start.Get("interval").Int()
	for {
		time.Sleep(time.Duration(interval) * time.Second)
		p := admin.OK(t, http.MethodPost, "/p/codex/auth/device/poll",
			map[string]any{"session_id": sid, "proxy_ticket": codexProxyTicket(t, admin, "codex_device", choice)})
		switch p.Get("status").String() {
		case "pending":
			if n := p.Get("interval").Int(); n > 0 {
				interval = n
			}
		case "done":
			return p.Get("credentials")
		default:
			t.Fatalf("device code poll: %s", p.Raw)
		}
	}
}

// expectAuthCalls requires the requests to the authorization server since
// mark to be paths, in order, the plugin's own (Originator), over TLS, and
// all through the proxy or all direct.
func expectAuthCalls(t testing.TB, m *Mock, mark int64, proxied bool, paths ...string) []MockRequest {
	t.Helper()
	var calls []MockRequest
	for _, r := range m.Since(t, mark) {
		if r.Host == codexAuthHost {
			calls = append(calls, r)
		}
	}
	if len(calls) != len(paths) {
		t.Fatalf("requests to %s = %d, want %v: %+v", codexAuthHost, len(calls), paths, calls)
	}
	for i, r := range calls {
		if r.Path != paths[i] || r.Method != http.MethodPost || !r.TLS || r.Proxied != proxied || r.Header("originator") != "sup2api" {
			t.Fatalf("request %d to %s: %s %s tls=%v proxied=%v originator=%q, want POST %s proxied=%v",
				i, codexAuthHost, r.Method, r.Path, r.TLS, r.Proxied, r.Header("originator"), paths[i], proxied)
		}
	}
	return calls
}

// createMockProxy saves the mock as an http proxy record.
func createMockProxy(t testing.TB, e *Env, admin *Session) int64 {
	t.Helper()
	u, err := url.Parse(e.MockInternalURL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("mock address %s has no port", e.MockInternalURL)
	}
	id := admin.OK(t, http.MethodPost, "/proxies", map[string]any{
		"name": e.Name("mock-proxy"), "protocol": "http", "host": u.Hostname(), "port": port}).Get("id").Int()
	if id == 0 {
		t.Fatal("POST /proxies returned no id")
	}
	return id
}

var deviceLoginPaths = []string{"/api/accounts/deviceauth/usercode", "/api/accounts/deviceauth/token", "/api/accounts/deviceauth/token", "/oauth/token"}

// AC 38: the codex logins go through the account form's proxy. With a saved
// proxy, a pasted proxy URL and no proxy, the device code login (start, a
// pending poll, the approving poll, the token exchange) reaches the
// authorization server all through the proxy or all directly, and a pasted
// URL adds no proxy record. The tokens make a working account through
// POST /accounts (same proxy as the form): a request through the gateway
// reaches the Codex backend with them and is billed. Sign in with Codex
// exchanges its code through the saved proxy.
func TestAC38_CodexAuthorizationThroughFormProxy(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	e.EnsurePlugin(admin, "codex", "")
	m := e.Mock()
	cl := &OwnershipCleanup{}
	defer cl.Run(e, admin)
	proxyID := createMockProxy(t, e, admin)
	cl.Proxies = append(cl.Proxies, proxyID)

	cases := []struct {
		name    string
		choice  map[string]any
		proxied bool
		// account is the proxy part of the account made from the tokens;
		// nil makes none (a pasted URL would save it as a proxy record).
		account map[string]any
	}{
		{"saved proxy", map[string]any{"proxy_id": proxyID}, true, map[string]any{"proxy_id": proxyID}},
		{"pasted proxy url", map[string]any{"proxy_url": e.MockInternalURL}, true, nil},
		{"no proxy", map[string]any{}, false, map[string]any{}},
	}
	for _, c := range cases {
		t.Run("device code/"+c.name, func(t *testing.T) {
			e := e.With(t)
			proxies := IDSet(admin.ListAll(t, "/proxies"))
			mark := m.Mark(t)
			creds := codexDeviceLogin(t, admin, c.choice)
			calls := expectAuthCalls(t, m, mark, c.proxied, deviceLoginPaths...)
			code := "code-" + gjson.Get(calls[1].Body, "device_auth_id").String()
			at := creds.Get("access_token").String()
			if at != "at-"+code || !strings.Contains(calls[3].Body, "code="+code) || creds.Get("chatgpt_account_id").String() != "acct-e2e" {
				t.Fatalf("credentials %s from the exchange %s", creds.Raw, calls[3].Body)
			}
			if after := IDSet(admin.ListAll(t, "/proxies")); len(after) != len(proxies) {
				t.Fatalf("proxy records %v after the login, %v before", after, proxies)
			}
			if c.account != nil {
				useCodexAccount(t, e, admin, m, cl, creds, c.account, c.proxied)
			}
		})
	}

	t.Run("sign in with codex/saved proxy", func(t *testing.T) {
		e := e.With(t)
		start := admin.OK(t, http.MethodPost, "/p/codex/auth/codex/start",
			map[string]any{"proxy_ticket": codexProxyTicket(t, admin, "codex_oauth", map[string]any{"proxy_id": proxyID})})
		authorize, err := url.Parse(start.Get("authorize_url").String())
		if err != nil || authorize.Host != codexAuthHost {
			t.Fatalf("authorize_url: %s", start.Raw)
		}
		code := "signin-" + e.Name("c")
		callback := "http://localhost:1455/auth/callback?" + url.Values{"code": {code}, "state": {authorize.Query().Get("state")}}.Encode()
		mark := m.Mark(t)
		x := admin.OK(t, http.MethodPost, "/p/codex/auth/codex/exchange",
			map[string]any{"session_id": start.Get("session_id").String(), "callback_url": callback,
				"proxy_ticket": codexProxyTicket(t, admin, "codex_oauth", map[string]any{"proxy_id": proxyID})})
		calls := expectAuthCalls(t, m, mark, true, "/oauth/token")
		if x.Get("credentials.access_token").String() != "at-"+code || !strings.Contains(calls[0].Body, "code="+code) {
			t.Fatalf("credentials %s from the exchange %s", x.Raw, calls[0].Body)
		}
	})
}

// useCodexAccount makes a codex_device account of creds with the proxy part
// account and sends one request through the gateway: it reaches the Codex
// backend with the access token, through the proxy or directly, and is
// billed.
func useCodexAccount(t *testing.T, e *Env, admin *Session, m *Mock, cl *OwnershipCleanup, creds gjson.Result, account map[string]any, proxied bool) {
	t.Helper()
	at := creds.Get("access_token").String()
	spec := AccountSpec{PluginKey: "codex", Type: "codex_device", Credentials: creds.Value().(map[string]any), Extra: account}
	tn := e.singleAccount(admin, "grp-cxauth", spec, at, "gpt", fmt.Sprintf("cxauth-%d", nextSeq()))
	cl.Accounts = append(cl.Accounts, tn.Accounts[0].ID)
	mark := m.Mark(t)
	g := responsesCall(e, tn.APIKey, tn.Model, "codex login "+e.Name("p"), false)
	if g.Status != http.StatusOK {
		t.Fatalf("request with the new account: HTTP %d %s", g.Status, g.Body)
	}
	r := oneMockCall(t, m, mark, at, "codex account")
	if r.Host != "chatgpt.com" || r.Path != "/backend-api/codex/responses" || r.Proxied != proxied || r.Header("chatgpt-account-id") != "acct-e2e" {
		t.Fatalf("Codex backend request %s %s proxied=%v (want %v): %s", r.Host, r.Path, r.Proxied, proxied, r.Headers.Raw)
	}
	e.expectBilled(admin, tn.User.UserID, g.RequestID, tn.Accounts[0].ID, usageOpenAI, false)
}
