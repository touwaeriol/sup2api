package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
)

// fakeIssuer is a scripted auth.openai.com. Each handler answers one
// endpoint; requests are recorded.
type fakeIssuer struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	calls    map[string]int
	bodies   map[string][]string
	headers  map[string]http.Header
	handlers map[string]func(w http.ResponseWriter, body []byte)
}

func newFakeIssuer(t *testing.T) *fakeIssuer {
	f := &fakeIssuer{t: t, calls: map[string]int{}, bodies: map[string][]string{}, headers: map[string]http.Header{},
		handlers: map[string]func(http.ResponseWriter, []byte){}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.calls[r.URL.Path]++
		f.bodies[r.URL.Path] = append(f.bodies[r.URL.Path], string(body))
		f.headers[r.URL.Path] = r.Header.Clone()
		h := f.handlers[r.URL.Path]
		f.mu.Unlock()
		if r.Method != http.MethodPost || h == nil {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusTeapot)
			return
		}
		h(w, body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIssuer) on(path string, h func(w http.ResponseWriter, body []byte)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[path] = h
}

func (f *fakeIssuer) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[path]
}

func (f *fakeIssuer) lastBody(path string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.bodies[path]
	if len(b) == 0 {
		return ""
	}
	return b[len(b)-1]
}

func reply(status int, body string) func(http.ResponseWriter, []byte) {
	return func(w http.ResponseWriter, _ []byte) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

const (
	pathUserCode    = "/api/accounts/deviceauth/usercode"
	pathDeviceToken = "/api/accounts/deviceauth/token"
	pathOAuthToken  = "/oauth/token"
)

// clock is a settable test clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details struct {
			Fields []struct {
				Field   string `json:"field"`
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"fields"`
		} `json:"details"`
	} `json:"error"`
}

func decodeError(t *testing.T, r *pluginv1.HTTPResponse) errorBody {
	t.Helper()
	var e errorBody
	mustJSON(t, r.GetBody(), &e)
	return e
}

func TestDeviceFlow(t *testing.T) {
	iss := newFakeIssuer(t)
	clk := &clock{t: testNow}
	p := mustPlugin(testEndpoints(iss.srv.URL))
	p.now = clk.now
	h := startPlugin(t, p)

	iss.on(pathUserCode, func(w http.ResponseWriter, body []byte) {
		if string(body) != `{"client_id":"`+CodexClientID+`"}` {
			t.Errorf("usercode body = %s", body)
		}
		reply(200, `{"device_auth_id":"dev-1","usercode":"ABCD-1234","interval":"5"}`)(w, body)
	})
	r := h.Do("POST", "/auth/device/start", nil, startBody)
	if r.GetStatus() != 200 {
		t.Fatalf("start = %d %s", r.GetStatus(), r.GetBody())
	}
	var start DeviceStartResponse
	mustData(t, r.GetBody(), &start)
	if start.SessionID == "" || start.UserCode != "ABCD-1234" || start.Interval != 5 || start.ExpiresIn != 900 ||
		start.VerificationURL != iss.srv.URL+"/codex/device" {
		t.Fatalf("start = %+v", start)
	}
	if ua := iss.headers[pathUserCode].Get("User-Agent"); ua != UserAgent {
		t.Fatalf("issuer saw user agent %q", ua)
	}
	poll := func() DevicePollResponse {
		t.Helper()
		r := h.Do("POST", "/auth/device/poll", nil, map[string]string{"proxy_ticket": testTicket, "session_id": start.SessionID})
		if r.GetStatus() != 200 {
			t.Fatalf("poll = %d %s", r.GetStatus(), r.GetBody())
		}
		var out DevicePollResponse
		mustData(t, r.GetBody(), &out)
		return out
	}

	// Not approved yet: 403 and 404 both mean pending.
	iss.on(pathDeviceToken, reply(403, `{"error":"authorization_pending"}`))
	if got := poll(); got.Status != "pending" || got.Interval != 5 {
		t.Fatalf("poll 1 = %+v", got)
	}
	if b := iss.lastBody(pathDeviceToken); b != `{"device_auth_id":"dev-1","user_code":"ABCD-1234"}` {
		t.Fatalf("token poll body = %s", b)
	}
	// Too soon after the previous poll: answered without asking OpenAI.
	clk.add(2 * time.Second)
	if got := poll(); got.Status != "pending" || iss.count(pathDeviceToken) != 1 {
		t.Fatalf("early poll = %+v, issuer calls %d", got, iss.count(pathDeviceToken))
	}
	clk.add(3 * time.Second)
	iss.on(pathDeviceToken, reply(404, ``))
	if got := poll(); got.Status != "pending" || iss.count(pathDeviceToken) != 2 {
		t.Fatalf("poll 404 = %+v (%d calls)", got, iss.count(pathDeviceToken))
	}
	// 429 slows the polling down.
	clk.add(5 * time.Second)
	iss.on(pathDeviceToken, reply(429, `{"error":"slow_down"}`))
	if got := poll(); got.Status != "pending" || got.Interval != 10 {
		t.Fatalf("poll 429 = %+v", got)
	}
	clk.add(5 * time.Second)
	iss.on(pathDeviceToken, reply(403, ``))
	if got := poll(); got.Status != "pending" || got.Interval != 10 || iss.count(pathDeviceToken) != 3 {
		t.Fatalf("poll before the new interval = %+v (%d calls)", got, iss.count(pathDeviceToken))
	}
	clk.add(5 * time.Second)
	if got := poll(); got.Status != "pending" || got.Interval != 10 || iss.count(pathDeviceToken) != 4 {
		t.Fatalf("poll after slow down = %+v (%d calls)", got, iss.count(pathDeviceToken))
	}

	// Approved: the code is exchanged once and the session ends.
	clk.add(10 * time.Second)
	iss.on(pathDeviceToken, reply(200, `{"authorization_code":"code-1","code_challenge":"ch","code_verifier":"ver-1"}`))
	tok := idToken("me@example.com", "acct-1", "plus")
	iss.on(pathOAuthToken, func(w http.ResponseWriter, body []byte) {
		form, _ := url.ParseQuery(string(body))
		if form.Get("grant_type") != "authorization_code" || form.Get("code") != "code-1" || form.Get("code_verifier") != "ver-1" ||
			form.Get("client_id") != CodexClientID || form.Get("redirect_uri") != iss.srv.URL+"/deviceauth/callback" {
			t.Errorf("token form = %v", form)
		}
		reply(200, `{"access_token":"at-1","refresh_token":"rt-1","id_token":"`+tok+`","expires_in":864000}`)(w, body)
	})
	got := poll()
	if got.Status != "done" || got.Credentials == nil {
		t.Fatalf("done = %+v", got)
	}
	c := got.Credentials
	if c.AccessToken != "at-1" || c.RefreshToken != "rt-1" || c.IDToken != tok || c.ExpiresAt != clk.now().Unix()+864000 ||
		c.Email != "me@example.com" || c.ChatGPTAccountID != "acct-1" || c.PlanType != "plus" {
		t.Fatalf("credentials = %+v", c)
	}
	if ct := iss.headers[pathOAuthToken].Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
		t.Fatalf("token content type = %q", ct)
	}
	// The session is gone.
	clk.add(10 * time.Second)
	if got := poll(); got.Status != "expired" {
		t.Fatalf("after done = %+v", got)
	}
}

func TestDeviceStartErrors(t *testing.T) {
	iss := newFakeIssuer(t)
	h := startPlugin(t, newTestPlugin(testEndpoints(iss.srv.URL)))
	iss.on(pathUserCode, reply(404, `{"detail":"Not Found"}`))
	r := h.Do("POST", "/auth/device/start", nil, startBody)
	if r.GetStatus() != 400 || decodeError(t, r).Error.Code != "device_auth_disabled" || !strings.Contains(string(r.GetBody()), "Device code authorization") {
		t.Fatalf("404 = %d %s", r.GetStatus(), r.GetBody())
	}
	iss.on(pathUserCode, reply(500, `{"error":{"message":"boom"}}`))
	if r := h.Do("POST", "/auth/device/start", nil, startBody); r.GetStatus() != 502 || decodeError(t, r).Error.Code != "upstream_error" {
		t.Fatalf("500 = %d %s", r.GetStatus(), r.GetBody())
	}
	iss.on(pathUserCode, reply(200, `{"interval":"5"}`))
	if r := h.Do("POST", "/auth/device/start", nil, startBody); r.GetStatus() != 502 {
		t.Fatalf("incomplete = %d %s", r.GetStatus(), r.GetBody())
	}
	// A numeric interval and user_code are read too, and clamped.
	iss.on(pathUserCode, reply(200, `{"device_auth_id":"d","user_code":"WXYZ","interval":600}`))
	r = h.Do("POST", "/auth/device/start", nil, startBody)
	var start DeviceStartResponse
	mustData(t, r.GetBody(), &start)
	if start.UserCode != "WXYZ" || start.Interval != maxInterval {
		t.Fatalf("start = %+v", start)
	}
	// The issuer cannot be reached.
	iss.srv.Close()
	if r := h.Do("POST", "/auth/device/start", nil, startBody); r.GetStatus() != 502 || decodeError(t, r).Error.Code != "upstream_unavailable" {
		t.Fatalf("down = %d %s", r.GetStatus(), r.GetBody())
	}
}

func TestDevicePollStates(t *testing.T) {
	iss := newFakeIssuer(t)
	clk := &clock{t: testNow}
	p := mustPlugin(testEndpoints(iss.srv.URL))
	p.now = clk.now
	h := startPlugin(t, p)
	iss.on(pathUserCode, reply(200, `{"device_auth_id":"dev-1","user_code":"ABCD","interval":"5"}`))
	start := func() string {
		r := h.Do("POST", "/auth/device/start", nil, startBody)
		var s DeviceStartResponse
		mustData(t, r.GetBody(), &s)
		return s.SessionID
	}
	poll := func(id string) DevicePollResponse {
		t.Helper()
		var out DevicePollResponse
		mustData(t, h.Do("POST", "/auth/device/poll", nil, map[string]string{"proxy_ticket": testTicket, "session_id": id}).GetBody(), &out)
		return out
	}

	if r := h.Do("POST", "/auth/device/poll", nil, map[string]string{}); r.GetStatus() != 400 || decodeError(t, r).Error.Code != "invalid_argument" {
		t.Fatalf("no session id = %d %s", r.GetStatus(), r.GetBody())
	}
	if got := poll("unknown"); got.Status != "expired" {
		t.Fatalf("unknown = %+v", got)
	}
	// Past the 15 minutes: expired without asking OpenAI.
	id := start()
	clk.add(deviceTTL)
	if got := poll(id); got.Status != "expired" || iss.count(pathDeviceToken) != 0 {
		t.Fatalf("expired = %+v", got)
	}
	// Any other status ends the login.
	id = start()
	iss.on(pathDeviceToken, reply(400, `{"error":{"message":"device code declined"}}`))
	if got := poll(id); got.Status != "error" || !strings.Contains(got.Message, "device code declined") {
		t.Fatalf("error = %+v", got)
	}
	clk.add(10 * time.Second)
	if got := poll(id); got.Status != "expired" {
		t.Fatalf("after error = %+v", got)
	}
	// Approved but the exchange fails: error, and the session is gone.
	id = start()
	sessions := []string{id}
	iss.on(pathDeviceToken, reply(200, `{"authorization_code":"secret-code-1","code_verifier":"secret-verifier-1"}`))
	iss.on(pathOAuthToken, reply(400, `{"error":"invalid_grant","error_description":"code expired"}`))
	if got := poll(id); got.Status != "error" || !strings.Contains(got.Message, "invalid_grant: code expired") {
		t.Fatalf("exchange failure = %+v", got)
	}
	// Approval answer without a code.
	id = start()
	iss.on(pathDeviceToken, reply(200, `{}`))
	if got := poll(id); got.Status != "error" {
		t.Fatalf("no code = %+v", got)
	}
	// The issuer is unreachable: still pending, the next poll retries.
	id = start()
	sessions = append(sessions, id)
	iss.srv.Close()
	if got := poll(id); got.Status != "pending" || got.Message == "" {
		t.Fatalf("unreachable = %+v", got)
	}

	// The failures were logged as warnings, without codes, verifiers or
	// session ids.
	logs := h.Host.Logs()
	if len(logs) == 0 {
		t.Fatal("no warning logged")
	}
	for _, l := range logs {
		line := l.Message + fmt.Sprint(l.Attrs)
		for _, secret := range append(sessions, "secret-") {
			if strings.Contains(line, secret) {
				t.Errorf("log %q leaks %q", line, secret)
			}
		}
	}
}

func TestRouteRequestErrors(t *testing.T) {
	h := startPlugin(t, newTestPlugin(testEndpoints("https://auth.example")))
	for _, tc := range []struct {
		path  string
		body  any
		field string
		code  string
	}{
		{"/auth/device/poll", map[string]string{}, "session_id", "required"},
		{"/auth/device/poll", map[string]string{"proxy_ticket": testTicket, "session_id": "  "}, "session_id", "required"},
		{"/auth/device/poll", "{not json", "body", "invalid_json"},
		{"/auth/codex/exchange", map[string]string{"callback_url": "?code=x"}, "session_id", "required"},
		{"/auth/codex/exchange", "{not json", "body", "invalid_json"},
	} {
		r := h.Do("POST", tc.path, nil, tc.body)
		e := decodeError(t, r)
		if r.GetStatus() != 400 || e.Error.Code != "invalid_argument" || len(e.Error.Details.Fields) != 1 ||
			e.Error.Details.Fields[0].Field != tc.field || e.Error.Details.Fields[0].Code != tc.code ||
			!strings.Contains(e.Error.Details.Fields[0].Message, " / ") {
			t.Errorf("%s %v = %d %s, want invalid_argument on %s (%s)", tc.path, tc.body, r.GetStatus(), r.GetBody(), tc.field, tc.code)
		}
	}
}

// Two polls racing must not both spend the code: a poll that finds the
// session locked answers pending without asking OpenAI.
func TestDevicePollLocked(t *testing.T) {
	iss := newFakeIssuer(t)
	p := newTestPlugin(testEndpoints(iss.srv.URL))
	h := startPlugin(t, p)
	iss.on(pathUserCode, reply(200, `{"device_auth_id":"dev-1","user_code":"ABCD","interval":"5"}`))
	iss.on(pathDeviceToken, reply(200, `{"authorization_code":"code-1","code_verifier":"ver-1"}`))
	iss.on(pathOAuthToken, reply(200, `{"access_token":"at-1","refresh_token":"rt-1","expires_in":60}`))
	var start DeviceStartResponse
	mustData(t, h.Do("POST", "/auth/device/start", nil, startBody).GetBody(), &start)
	poll := func() DevicePollResponse {
		t.Helper()
		var out DevicePollResponse
		mustData(t, h.Do("POST", "/auth/device/poll", nil, map[string]string{"proxy_ticket": testTicket, "session_id": start.SessionID}).GetBody(), &out)
		return out
	}

	ctx := context.Background()
	name := pollLockName(start.SessionID)
	if !pluginsdk.ValidLockName(name) {
		t.Fatalf("lock name %q is not valid", name)
	}
	const other = "another-poll-holds-it"
	if r, err := h.Host.LockAcquire(ctx, &pluginv1.LockAcquireRequest{Name: name, TtlMs: 60_000, Token: other}); err != nil || !r.GetAcquired() {
		t.Fatalf("hold the lock: %v %v", r, err)
	}
	if got := poll(); got.Status != "pending" || iss.count(pathDeviceToken) != 0 || iss.count(pathOAuthToken) != 0 {
		t.Fatalf("locked poll = %+v (status calls %d, token calls %d)", got, iss.count(pathDeviceToken), iss.count(pathOAuthToken))
	}
	if _, err := h.Host.LockRelease(ctx, &pluginv1.LockReleaseRequest{Name: name, Token: other}); err != nil {
		t.Fatal(err)
	}
	if got := poll(); got.Status != "done" || got.Credentials == nil || got.Credentials.AccessToken != "at-1" || iss.count(pathOAuthToken) != 1 {
		t.Fatalf("poll after release = %+v", got)
	}
	// The poll gave its lock back.
	if h.Host.Locks.Held(name) {
		t.Fatal("the poll lock is still held")
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
		"client_id": CodexClientID, "response_type": "code", "redirect_uri": "http://localhost:1455/auth/callback",
		"scope": "openid profile email offline_access", "code_challenge_method": "S256",
		"id_token_add_organizations": "true", "codex_cli_simplified_flow": "true",
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
		{"no code", "http://localhost:1455/auth/callback", "invalid_callback", 400},
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
		{in: "http://localhost:1455/auth/callback?code=abc&scope=openid+profile&state=s1", code: "abc", state: "s1"},
		{in: "  \"http://localhost:1455/auth/callback?code=abc&amp;state=s1\"\n", code: "abc", state: "s1"},
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

// Keep the JSON field names the console relies on (CodexAuthForm).
func TestRouteJSONNames(t *testing.T) {
	b, _ := json.Marshal(DevicePollResponse{Status: "done", Credentials: &Credentials{AccessToken: "a", RefreshToken: "r", IDToken: "i", ExpiresAt: 1,
		Email: "e", PlanType: "p", ChatGPTAccountID: "acc"}, Interval: 5})
	want := `{"status":"done","credentials":{"access_token":"a","refresh_token":"r","id_token":"i","expires_at":1,"email":"e","plan_type":"p","chatgpt_account_id":"acc"},"interval":5}`
	if string(b) != want {
		t.Fatalf("poll JSON = %s", b)
	}
	b, _ = json.Marshal(DeviceStartResponse{SessionID: "s", VerificationURL: "u", UserCode: "c", Interval: 5, ExpiresIn: 900})
	if string(b) != `{"session_id":"s","verification_url":"u","user_code":"c","interval":5,"expires_in":900}` {
		t.Fatalf("start JSON = %s", b)
	}
	b, _ = json.Marshal(CodexStartResponse{SessionID: "s", AuthorizeURL: "u"})
	if string(b) != `{"session_id":"s","authorize_url":"u"}` {
		t.Fatalf("codex start JSON = %s", b)
	}
}

// A session belongs to the user who started it: to anyone else it looks
// expired, is not touched, and nothing is asked of OpenAI.
func TestSessionsBelongToTheirCaller(t *testing.T) {
	iss := newFakeIssuer(t)
	h := startPlugin(t, newTestPlugin(testEndpoints(iss.srv.URL)))
	iss.on(pathUserCode, reply(200, `{"device_auth_id":"dev-1","usercode":"ABCD-1234","interval":"5"}`))
	iss.on(pathDeviceToken, func(w http.ResponseWriter, body []byte) {
		t.Error("another user's poll reached the issuer")
		reply(403, `{}`)(w, body)
	})
	var device DeviceStartResponse
	mustData(t, h.Do("POST", "/auth/device/start", nil, startBody).GetBody(), &device)
	var codex CodexStartResponse
	mustData(t, h.Do("POST", "/auth/codex/start", nil, startBody).GetBody(), &codex)
	stored := func(ns, id string) bool {
		kv, _ := h.Host.KVGet(context.Background(), &pluginv1.KVGetRequest{Namespace: ns, Key: id})
		return kv.GetFound()
	}

	for _, uid := range []int64{2, 0} {
		for _, id := range []string{device.SessionID, "no-such-session"} {
			var out DevicePollResponse
			r := h.DoAs(uid, "POST", "/auth/device/poll", nil, map[string]string{"proxy_ticket": testTicket, "session_id": id})
			mustData(t, r.GetBody(), &out)
			if r.GetStatus() != 200 || out.Status != pollExpired {
				t.Fatalf("user %d poll of %q = %d %s, want expired", uid, id, r.GetStatus(), r.GetBody())
			}
		}
		u, _ := url.Parse(codex.AuthorizeURL)
		cb := "?code=code-1&state=" + u.Query().Get("state")
		for _, id := range []string{codex.SessionID, "no-such-session"} {
			r := h.DoAs(uid, "POST", "/auth/codex/exchange", nil, map[string]string{"proxy_ticket": testTicket, "session_id": id, "callback_url": cb})
			if r.GetStatus() != 404 || decodeError(t, r).Error.Code != "session_expired" {
				t.Fatalf("user %d exchange of %q = %d %s, want 404 session_expired", uid, id, r.GetStatus(), r.GetBody())
			}
		}
	}
	if !stored(kvDevice, device.SessionID) || !stored(kvCodex, codex.SessionID) {
		t.Fatal("another user's attempt removed a session")
	}

	// While the owner's poll holds the session lock, someone else's poll
	// still reads expired, not pending.
	ctx, name := context.Background(), pollLockName(device.SessionID)
	if r, err := h.Host.LockAcquire(ctx, &pluginv1.LockAcquireRequest{Name: name, TtlMs: 60_000, Token: "the-owner-is-polling"}); err != nil || !r.GetAcquired() {
		t.Fatalf("hold the lock: %v %v", r, err)
	}
	var locked DevicePollResponse
	mustData(t, h.DoAs(2, "POST", "/auth/device/poll", nil, map[string]string{"proxy_ticket": testTicket, "session_id": device.SessionID}).GetBody(), &locked)
	if locked.Status != pollExpired {
		t.Fatalf("another user's poll of a locked session = %+v, want expired", locked)
	}
	if _, err := h.Host.LockRelease(ctx, &pluginv1.LockReleaseRequest{Name: name, Token: "the-owner-is-polling"}); err != nil {
		t.Fatal(err)
	}

	// A flow cannot be started without a caller.
	for _, path := range []string{"/auth/device/start", "/auth/codex/start"} {
		if r := h.DoAs(0, "POST", path, nil, startBody); r.GetStatus() != 401 {
			t.Fatalf("%s without a caller = %d %s", path, r.GetStatus(), r.GetBody())
		}
	}

	// The owner goes on as if nothing happened.
	iss.on(pathDeviceToken, reply(403, `{"error":"authorization_pending"}`))
	var out DevicePollResponse
	mustData(t, h.Do("POST", "/auth/device/poll", nil, map[string]string{"proxy_ticket": testTicket, "session_id": device.SessionID}).GetBody(), &out)
	if out.Status != pollPending {
		t.Fatalf("owner poll after foreign attempts = %+v", out)
	}
}
