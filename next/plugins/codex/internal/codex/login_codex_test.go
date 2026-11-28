package codex

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

// The authorize URL carries the originator the account form sets.
func TestCodexStartOriginator(t *testing.T) {
	h := startPlugin(t, newTestPlugin(testEndpoints("https://auth.example")))
	r := h.Do("POST", "/auth/codex/start", nil, `{"proxy_ticket":"`+testTicket+`","originator":"my-app"}`)
	var s CodexStartResponse
	mustData(t, r.GetBody(), &s)
	if u, err := url.Parse(s.AuthorizeURL); err != nil || u.Query().Get("originator") != "my-app" {
		t.Fatalf("authorize url = %s", s.AuthorizeURL)
	}
}

func TestCodexStart(t *testing.T) {
	h := startPlugin(t, newTestPlugin(testEndpoints("https://auth.example")))
	r := h.Do("POST", "/auth/codex/start", nil, startBody)
	if r.GetStatus() != 200 {
		t.Fatalf("start = %d %s", r.GetStatus(), r.GetBody())
	}
	var s CodexStartResponse
	mustData(t, r.GetBody(), &s)
	u, err := url.Parse(s.AuthorizeURL)
	if err != nil || s.SessionID == "" || u.Scheme+"://"+u.Host+u.Path != "https://auth.example/oauth/authorize" {
		t.Fatalf("start = %+v", s)
	}
	// The Codex CLI's own browser login.
	q := u.Query()
	want := map[string]string{
		"client_id": CodexClientID, "response_type": "code", "redirect_uri": "http://127.0.0.1:1455/auth/callback",
		"scope": "openid profile email offline_access api.connectors.read api.connectors.invoke", "code_challenge_method": "S256",
		"id_token_add_organizations": "true", "codex_cli_simplified_flow": "true", "originator": Originator,
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	if len(q) != len(want)+2 || q.Get("state") == "" || len(q.Get("code_challenge")) != 43 {
		t.Errorf("query = %v", q)
	}
	// The stored verifier matches the challenge; the session is bound to
	// its caller (Harness.Do is user 1).
	kv, _ := h.Host.KVGet(context.Background(), &pluginv1.KVGetRequest{Namespace: kvCodex, Key: s.SessionID})
	if !kv.GetFound() {
		t.Fatal("no session stored")
	}
	var sess codexSession
	stored := struct {
		UserID  int64         `json:"uid"`
		Session *codexSession `json:"v"`
	}{Session: &sess}
	mustJSON(t, kv.GetValue(), &stored)
	if stored.UserID != 1 || pkceChallenge(sess.Verifier) != q.Get("code_challenge") || sess.State != q.Get("state") || len(sess.Verifier) != 64 {
		t.Fatalf("session = %d %+v", stored.UserID, sess)
	}
}

func TestCodexExchange(t *testing.T) {
	iss := newFakeIssuer(t)
	h := startPlugin(t, newTestPlugin(testEndpoints(iss.srv.URL)))
	start := func() (string, url.Values) {
		t.Helper()
		var s CodexStartResponse
		mustData(t, h.Do("POST", "/auth/codex/start", nil, startBody).GetBody(), &s)
		u, _ := url.Parse(s.AuthorizeURL)
		return s.SessionID, u.Query()
	}
	exchange := func(id, cb string) *pluginv1.HTTPResponse {
		return h.Do("POST", "/auth/codex/exchange", nil, map[string]string{"proxy_ticket": testTicket, "session_id": id, "callback_url": cb})
	}
	tok := idToken("me@example.com", "acct-1", "plus")
	iss.on(pathOAuthToken, func(w http.ResponseWriter, body []byte) {
		form, _ := url.ParseQuery(string(body))
		if form.Get("grant_type") != "authorization_code" || form.Get("client_id") != CodexClientID || form.Get("code") != "code-1" ||
			form.Get("redirect_uri") != CodexRedirectURI || form.Get("code_verifier") == "" || len(form) != 5 {
			t.Errorf("token form = %v", form)
		}
		reply(200, `{"access_token":"at-1","refresh_token":"rt-1","id_token":"`+tok+`","expires_in":3600,"scope":"openid profile email offline_access"}`)(w, body)
	})

	// The whole address pasted: the same credentials as a device code login.
	id, q := start()
	cb := CodexRedirectURI + "?code=code-1&scope=openid+profile+email+offline_access&state=" + q.Get("state")
	r := exchange(id, cb)
	if r.GetStatus() != 200 {
		t.Fatalf("exchange = %d %s", r.GetStatus(), r.GetBody())
	}
	var out CodexExchangeResponse
	mustData(t, r.GetBody(), &out)
	want := Credentials{AccessToken: "at-1", RefreshToken: "rt-1", IDToken: tok, ExpiresAt: testNow.Unix() + 3600,
		Email: "me@example.com", PlanType: "plus", ChatGPTAccountID: "acct-1"}
	if out.Credentials != want {
		t.Fatalf("credentials = %+v", out.Credentials)
	}
	// The verifier sent is the one behind the challenge.
	form, _ := url.ParseQuery(iss.lastBody(pathOAuthToken))
	if pkceChallenge(form.Get("code_verifier")) != q.Get("code_challenge") {
		t.Fatal("code_verifier does not match the challenge")
	}
	// The session is used up.
	if r := exchange(id, cb); r.GetStatus() != 404 || decodeError(t, r).Error.Code != "session_expired" {
		t.Fatalf("reused session = %d %s", r.GetStatus(), r.GetBody())
	}

	// Only the query pasted works as well.
	id, q = start()
	if r := exchange(id, "code=code-1&state="+q.Get("state")); r.GetStatus() != 200 {
		t.Fatalf("query only = %d %s", r.GetStatus(), r.GetBody())
	}

	for _, tc := range []struct {
		name, cb, code string
		status         int32
	}{
		{"state mismatch", "?code=code-1&state=other", "state_mismatch", 400},
		{"access denied", "?error=access_denied&error_description=user+said+no&state=%s", "access_denied", 400},
		{"other error", "?error=server_error&state=%s", "authorization_failed", 400},
		{"error of another authorization", "?error=access_denied&state=other", "state_mismatch", 400},
		{"no code", "http://127.0.0.1:1455/auth/callback", "invalid_callback", 400},
		{"empty", "  ", "invalid_callback", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, q := start()
			r := exchange(id, strings.Replace(tc.cb, "%s", q.Get("state"), 1))
			if r.GetStatus() != tc.status || decodeError(t, r).Error.Code != tc.code {
				t.Fatalf("%d %s, want %d %s", r.GetStatus(), r.GetBody(), tc.status, tc.code)
			}
		})
	}

	// A wrong state (even on a denial) or another OAuth error keeps the
	// session: the right address still works. A denied consent ends it.
	id, q = start()
	for _, cb := range []string{"?code=code-1&state=wrong", "?error=access_denied&state=wrong", "?error=server_error&state=" + q.Get("state")} {
		if r := exchange(id, cb); r.GetStatus() != 400 {
			t.Fatalf("%s accepted", cb)
		}
	}
	if r := exchange(id, "?code=code-1&state="+q.Get("state")); r.GetStatus() != 200 {
		t.Fatalf("retry = %d %s", r.GetStatus(), r.GetBody())
	}
	id, q = start()
	exchange(id, "?error=access_denied&state="+q.Get("state"))
	if r := exchange(id, "?code=code-1&state="+q.Get("state")); r.GetStatus() != 404 {
		t.Fatalf("after denial = %d %s", r.GetStatus(), r.GetBody())
	}

	// The token endpoint refuses the code: the answer says why.
	iss.on(pathOAuthToken, reply(400, `{"error":"invalid_grant","error_description":"code already used"}`))
	id, q = start()
	r = exchange(id, "?code=c&state="+q.Get("state"))
	if r.GetStatus() != 502 || decodeError(t, r).Error.Code != "token_exchange_failed" || !strings.Contains(string(r.GetBody()), "code already used") {
		t.Fatalf("token refused = %d %s", r.GetStatus(), r.GetBody())
	}
	// An answer without an access token.
	iss.on(pathOAuthToken, reply(200, `{"refresh_token":"r"}`))
	id, q = start()
	if r := exchange(id, "?code=c&state="+q.Get("state")); r.GetStatus() != 502 || decodeError(t, r).Error.Code != "token_exchange_failed" {
		t.Fatalf("no access token = %d %s", r.GetStatus(), r.GetBody())
	}
	// The issuer cannot be reached.
	id, q = start()
	iss.srv.Close()
	if r := exchange(id, "?code=c&state="+q.Get("state")); r.GetStatus() != 502 || decodeError(t, r).Error.Code != "upstream_unavailable" {
		t.Fatalf("down = %d %s", r.GetStatus(), r.GetBody())
	}
}

func TestParseCallback(t *testing.T) {
	for _, tc := range []struct {
		in, code, state, errCode string
	}{
		{in: "http://127.0.0.1:1455/auth/callback?code=abc&scope=openid+profile&state=s1", code: "abc", state: "s1"},
		{in: "  \"http://127.0.0.1:1455/auth/callback?code=abc&amp;state=s1\"\n", code: "abc", state: "s1"},
		{in: "127.0.0.1:1455/auth/callback?state=s1&code=a%2Fb", code: "a/b", state: "s1"},
		{in: "?code=abc&state=s1", code: "abc", state: "s1"},
		{in: "code=abc&state=s1&code=second", code: "abc", state: "s1"},
		{in: "http://127.0.0.1:1455/auth/callback#code=abc&state=s1", code: "abc", state: "s1"},
		{in: "http://127.0.0.1:1455/auth/callback?code=abc&state=s1#frag", code: "abc", state: "s1"},
		{in: "<http://127.0.0.1:1455/auth/callback?error=access_denied&state=s1>", state: "s1", errCode: "access_denied"},
	} {
		cb, err := parseCallback(tc.in)
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if cb.Code != tc.code || cb.State != tc.state || cb.Error != tc.errCode {
			t.Errorf("%q = %+v", tc.in, cb)
		}
	}
	for _, in := range []string{"", "   ", "http://127.0.0.1:1455/auth/callback", "http://127.0.0.1:1455/auth/callback?state=s1", "hello world", "%zz=1"} {
		if _, err := parseCallback(in); err == nil {
			t.Errorf("%q: want an error", in)
		}
	}
}
