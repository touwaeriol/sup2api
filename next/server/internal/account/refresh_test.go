package account

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// tokenUpstream is an OAuth token endpoint that rotates refresh tokens:
// refresh token rt-N is accepted once and answered with at-(N+1) / rt-(N+1).
type tokenUpstream struct {
	srv        *httptest.Server
	mu         sync.Mutex
	next       int // the N of the refresh token it accepts
	calls      atomic.Int32
	failStatus atomic.Int32
	// during runs while a request is being answered (after the token was
	// rotated upstream), e.g. to change the account meanwhile.
	during func()
}

func newTokenUpstream(t *testing.T) *tokenUpstream {
	u := &tokenUpstream{next: 1}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.calls.Add(1)
		if s := u.failStatus.Load(); s != 0 {
			w.WriteHeader(int(s))
			return
		}
		var body struct {
			RefreshToken string `json:"refresh_token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		u.mu.Lock()
		want := "rt-" + strconv.Itoa(u.next)
		if body.RefreshToken != want {
			u.mu.Unlock()
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"invalid_grant"}`)
			return
		}
		u.next++
		n, during := u.next, u.during
		u.mu.Unlock()
		if during != nil {
			during()
		}
		fmt.Fprintf(w, `{"access_token":"at-%d","refresh_token":"rt-%d","expires_in":28800}`, n, n)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

// mkOAuthAccount stores an account of the anthropic/apikey harness type
// with OAuth credentials expiring in `in` (no expires_at when in is 0).
func (e *env) mkOAuthAccount(name, refreshToken string, in time.Duration) int64 {
	e.t.Helper()
	creds := map[string]any{"api_key": "sk-good-key-123", "access_token": "at-old", "refresh_token": refreshToken, "scratch": "x"}
	if in != 0 {
		creds["expires_at"] = strconv.FormatInt(time.Now().Add(in).Unix(), 10)
	}
	return e.mkAccountCreds(name, creds)
}

func (e *env) mkAccountCreds(name string, creds map[string]any) int64 {
	e.t.Helper()
	b, _ := json.Marshal(creds)
	enc, err := e.svc.d.Cipher.Encrypt(b, aad("anthropic"))
	if err != nil {
		e.t.Fatal(err)
	}
	return e.exec1(`INSERT INTO accounts (name, plugin_key, type, credentials_enc, settings, status, schedulable)
		VALUES ($1, 'anthropic', 'apikey', $2, '{"base_url":"https://api.anthropic.com"}', 'active', true) RETURNING id`, name, enc)
}

// creds decrypts the stored credentials of an account.
func (e *env) creds(id int64) map[string]any {
	e.t.Helper()
	var enc []byte
	if err := e.db.SQL.QueryRowContext(context.Background(), `SELECT credentials_enc FROM accounts WHERE id = $1`, id).Scan(&enc); err != nil {
		e.t.Fatal(err)
	}
	plain, err := e.svc.d.Cipher.Decrypt(enc, aad("anthropic"))
	if err != nil {
		e.t.Fatal(err)
	}
	m := map[string]any{}
	_ = json.Unmarshal(plain, &m)
	return m
}

func (e *env) setCreds(id int64, creds map[string]any) {
	e.t.Helper()
	b, _ := json.Marshal(creds)
	enc, _ := e.svc.d.Cipher.Encrypt(b, aad("anthropic"))
	if _, err := e.db.SQL.ExecContext(context.Background(), `UPDATE accounts SET credentials_enc = $2 WHERE id = $1`, id, enc); err != nil {
		e.t.Fatal(err)
	}
}

func TestCredentialRefresh(t *testing.T) {
	e := setup(t)
	up := newTokenUpstream(t)
	e.plat.refreshURL = up.srv.URL + "/token"
	ctx := context.Background()
	due := e.mkOAuthAccount("due", "rt-1", 10*time.Minute)
	later := e.mkOAuthAccount("later", "rt-later", 5*time.Hour)
	noExpiry := e.mkOAuthAccount("no-expiry", "rt-none", 0)
	path := fmt.Sprintf("/accounts/%d/refresh-credentials", due)

	// A type without refresh: refused, nothing swept, refresh null in lists.
	if code, _ := e.do("POST", path, nil); code != http.StatusBadRequest {
		t.Fatalf("unsupported type: %d", code)
	}
	if n, err := e.svc.SweepRefresh(ctx); err != nil || n != 0 || up.calls.Load() != 0 {
		t.Fatalf("sweep without declaration: %d %v", n, err)
	}
	e.gen.types[0].Type.Refresh = &manifest.AccountRefresh{} // 30 minutes ahead
	// A disabled account is only kept out of scheduling: its credentials are
	// renewed like any other's.
	if _, err := e.db.SQL.ExecContext(ctx, `UPDATE accounts SET status = 'disabled', schedulable = false WHERE id = $1`, due); err != nil {
		t.Fatal(err)
	}

	// The sweep renews only the account inside the window, merging the
	// patch: renewed fields replaced, null removed, the rest kept.
	n, err := e.svc.SweepRefresh(ctx)
	if err != nil || n != 1 || up.calls.Load() != 1 {
		t.Fatalf("sweep: renewed %d, err %v, upstream calls %d", n, err, up.calls.Load())
	}
	if _, err := e.db.SQL.ExecContext(ctx, `UPDATE accounts SET status = 'active', schedulable = true WHERE id = $1`, due); err != nil {
		t.Fatal(err)
	}
	c := e.creds(due)
	if c["access_token"] != "at-2" || c["refresh_token"] != "rt-2" || c["api_key"] != "sk-good-key-123" {
		t.Fatalf("renewed credentials: %v", c)
	}
	if _, ok := c["scratch"]; ok {
		t.Fatalf("null in the patch did not remove the key: %v", c)
	}
	if exp, _ := c["expires_at"].(float64); time.Until(time.Unix(int64(exp), 0)) < 7*time.Hour {
		t.Fatalf("expires_at: %v", c["expires_at"])
	}
	if c := e.creds(later); c["access_token"] != "at-old" {
		t.Fatal("an account outside the window was renewed")
	}
	if c := e.creds(noExpiry); c["access_token"] != "at-old" {
		t.Fatal("an account without expiry was renewed by the sweep")
	}
	if rows := e.auditRows("account.credentials_refresh"); len(rows) != 1 || rows[0]["target_id"] != strconv.FormatInt(due, 10) {
		t.Fatalf("audit: %v", rows)
	}
	e.bus.mu.Lock()
	sent := strings.Join(e.bus.sent, ",")
	e.bus.mu.Unlock()
	if !strings.Contains(sent, "account:changed") {
		t.Fatalf("account:changed not broadcast: %s", sent)
	}
	// Renewed: not due any more.
	if n, _ := e.svc.SweepRefresh(ctx); n != 0 || up.calls.Load() != 1 {
		t.Fatalf("second sweep renewed %d (calls %d)", n, up.calls.Load())
	}

	// The list shows the state; an administrator can renew at once.
	_, out := e.do("GET", "/accounts", nil)
	for _, it := range out["data"].([]any) {
		a := it.(map[string]any)
		r, _ := a["refresh"].(map[string]any)
		if int64(a["id"].(float64)) == due && (r == nil || r["last_success_at"] == nil || r["error_type"] != "" || r["expires_at"] == nil) {
			t.Fatalf("list refresh state: %v", a["refresh"])
		}
	}
	code, out := e.do("POST", path, nil)
	if d := out["data"].(map[string]any); code != 200 || d["refreshed"] != true || e.creds(due)["refresh_token"] != "rt-3" {
		t.Fatalf("manual refresh: %d %v", code, out)
	}

	// Another renewal of the account in progress: skipped, nothing sent.
	mu, _ := e.svc.refreshLocal.LoadOrStore(due, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	_, out = e.do("POST", path, nil)
	mu.(*sync.Mutex).Unlock()
	if d := out["data"].(map[string]any); d["skipped"] != "in_progress" || up.calls.Load() != 2 {
		t.Fatalf("locked: %v (calls %d)", out, up.calls.Load())
	}

	// A transient failure is recorded and retried by the next sweep.
	e.setCreds(due, map[string]any{"access_token": "at-3", "refresh_token": "rt-3",
		"expires_at": time.Now().Add(time.Minute).Unix()})
	up.failStatus.Store(503)
	if n, _ := e.svc.SweepRefresh(ctx); n != 0 || up.calls.Load() != 3 {
		t.Fatalf("transient: renewed %d, calls %d", n, up.calls.Load())
	}
	states, _ := e.db.AccountRefreshStates(ctx, []int64{due})
	if st := states[due]; st == nil || st.ErrorType != "transient" || !strings.Contains(st.Error, "503") || st.LastSuccessAt == nil {
		t.Fatalf("transient state: %+v", st)
	}
	up.failStatus.Store(0)
	if n, _ := e.svc.SweepRefresh(ctx); n != 1 || up.calls.Load() != 4 {
		t.Fatalf("retry after transient: renewed %d, calls %d", n, up.calls.Load())
	}
	states, _ = e.db.AccountRefreshStates(ctx, []int64{due})
	if st := states[due]; st.ErrorType != "" || st.Error != "" {
		t.Fatalf("success did not clear the error: %+v", st)
	}

	// A refresh token refused for good is not tried again until the
	// credentials change.
	e.setCreds(due, map[string]any{"access_token": "at-x", "refresh_token": "rt-spent",
		"expires_at": time.Now().Add(time.Minute).Unix()})
	if n, _ := e.svc.SweepRefresh(ctx); n != 0 || up.calls.Load() != 5 {
		t.Fatalf("rejected: renewed %d, calls %d", n, up.calls.Load())
	}
	states, _ = e.db.AccountRefreshStates(ctx, []int64{due})
	if st := states[due]; st.ErrorType != "auth_rejected" || len(st.RejectedCredHash) == 0 {
		t.Fatalf("rejected state: %+v", st)
	}
	if n, _ := e.svc.SweepRefresh(ctx); n != 0 || up.calls.Load() != 5 {
		t.Fatalf("rejected credentials tried again: calls %d", up.calls.Load())
	}
	var status string
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT status FROM accounts WHERE id = $1`, due).Scan(&status)
	if status != "active" {
		t.Fatalf("a refused refresh changed the account status: %s", status)
	}
	// Re-authorised by an administrator: tried again.
	up.mu.Lock()
	cur := up.next
	up.mu.Unlock()
	e.setCreds(due, map[string]any{"access_token": "at-y", "refresh_token": "rt-" + strconv.Itoa(cur),
		"expires_at": time.Now().Add(time.Minute).Unix()})
	if n, _ := e.svc.SweepRefresh(ctx); n != 1 || up.calls.Load() != 6 {
		t.Fatalf("after re-authorisation: renewed %d, calls %d", n, up.calls.Load())
	}

	// Credentials changed while the renewal ran: the administrator's save
	// wins and the renewal is discarded.
	edited := map[string]any{"access_token": "at-admin", "refresh_token": "rt-admin", "expires_at": time.Now().Add(time.Hour).Unix()}
	up.mu.Lock()
	up.during = func() { e.setCreds(due, edited) }
	up.mu.Unlock()
	_, out = e.do("POST", path, nil)
	up.mu.Lock()
	up.during = nil
	up.mu.Unlock()
	if d := out["data"].(map[string]any); d["skipped"] != "changed" || e.creds(due)["refresh_token"] != "rt-admin" {
		t.Fatalf("concurrent edit: %v, stored %v", out, e.creds(due))
	}
}

// eventually polls cond until it holds; background work has a few seconds.
func (e *env) eventually(what string, cond func() bool) {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			e.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRefreshDue(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	decl := &manifest.AccountRefresh{} // 30 minutes ahead
	for _, tc := range []struct {
		name  string
		creds map[string]any
		want  bool
	}{
		{"no expiry", map[string]any{"access_token": "at"}, false},
		{"unreadable expiry", map[string]any{"expires_at": "soon"}, false},
		{"zero is not an expiry", map[string]any{"expires_at": json.Number("0")}, false},
		{"far away", map[string]any{"expires_at": json.Number("1800018000")}, false},
		{"just outside the window", map[string]any{"expires_at": json.Number("1800001801")}, false},
		{"on the edge of the window", map[string]any{"expires_at": json.Number("1800001800")}, true},
		{"inside the window", map[string]any{"expires_at": json.Number("1800000060")}, true},
		{"expired", map[string]any{"expires_at": json.Number("1799999999")}, true},
		// What a form without an authorization flow stores to ask for the
		// first token.
		{"expired since 1970", map[string]any{"expires_at": json.Number("1")}, true},
		{"numeric string", map[string]any{"expires_at": "1"}, true},
	} {
		if got := refreshDue(decl, tc.creds, now); got != tc.want {
			t.Errorf("%s: due = %v, want %v", tc.name, got, tc.want)
		}
	}
	if !refreshDue(&manifest.AccountRefresh{BeforeExpirySec: 7200}, map[string]any{"expires_at": json.Number("1800007000")}, now) {
		t.Error("the type's own window is not used")
	}
}

// Saving an account whose credentials are already due renews them at once
// (CONTRACTS §48.3); a save of credentials that are not due, of an account
// without readable expiry, of a type without refresh, or without
// credentials at all sends nothing.
func TestRefreshAfterSave(t *testing.T) {
	e := setup(t)
	up := newTokenUpstream(t)
	e.plat.refreshURL = up.srv.URL + "/token"
	ctx := context.Background()
	oauth := func(refreshToken string, expiresAt any) map[string]any {
		c := map[string]any{"api_key": "sk-good-key-123", "access_token": "at-old", "refresh_token": refreshToken}
		if expiresAt != nil {
			c["expires_at"] = expiresAt
		}
		return c
	}
	create := func(name string, creds map[string]any) int64 {
		t.Helper()
		return int64(e.createAs(e.uid, name, map[string]any{"credentials": creds})["id"].(float64))
	}
	patch := func(id int64, body map[string]any) {
		t.Helper()
		if code, out := e.do("PATCH", fmt.Sprintf("/accounts/%d", id), body); code != http.StatusOK {
			t.Fatalf("update %d: %d %v", id, code, out)
		}
	}
	refreshed := func(id int64, token string) func() bool {
		return func() bool { return e.creds(id)["access_token"] == token }
	}

	// The type does not declare refresh: a due save sends nothing.
	undeclared := create("undeclared", oauth("rt-undeclared", 1))

	e.gen.types[0].Type.Refresh = &manifest.AccountRefresh{} // 30 minutes ahead
	later := create("later", oauth("rt-later", time.Now().Add(5*time.Hour).Unix()))
	noExpiry := create("no-expiry", oauth("rt-none", nil))

	// Created already due, the way a form without an authorization flow
	// asks for its first token: renewed without waiting for the sweep.
	due := create("due", oauth("rt-1", 1))
	e.eventually("the refresh after create", refreshed(due, "at-2"))
	if c := e.creds(due); c["refresh_token"] != "rt-2" || c["api_key"] != "sk-good-key-123" {
		t.Fatalf("renewed credentials: %v", c)
	}
	states, err := e.db.AccountRefreshStates(ctx, []int64{due})
	if st := states[due]; err != nil || st == nil || st.LastSuccessAt == nil || st.ErrorType != "" {
		t.Fatalf("refresh state: %+v %v", st, err)
	}
	if rows := e.auditRows("account.credentials_refresh"); len(rows) != 1 || rows[0]["target_id"] != strconv.FormatInt(due, 10) {
		t.Fatalf("audit: %v", rows)
	}
	if n := up.calls.Load(); n != 1 {
		t.Fatalf("upstream calls after the saves: %d, want 1", n)
	}
	for name, id := range map[string]int64{"undeclared": undeclared, "later": later, "no-expiry": noExpiry} {
		if e.creds(id)["access_token"] != "at-old" {
			t.Fatalf("%s was renewed by its save", name)
		}
	}

	// Updates: only one that stores due credentials renews. A rename of an
	// account whose stored credentials are due is left to the sweep, and
	// so are new credentials that are not due.
	e.setCreds(later, oauth("rt-spent", time.Now().Add(time.Minute).Unix()))
	patch(later, map[string]any{"name": "later-renamed"})
	patch(noExpiry, map[string]any{"credentials": oauth("rt-none", time.Now().Add(5*time.Hour).Unix())})
	patch(due, map[string]any{"credentials": oauth("rt-2", 1)})
	e.eventually("the refresh after update", refreshed(due, "at-3"))
	if n := up.calls.Load(); n != 2 {
		t.Fatalf("upstream calls after the updates: %d, want 2", n)
	}
	if e.creds(later)["access_token"] != "at-old" || e.creds(noExpiry)["access_token"] != "at-old" {
		t.Fatal("an update that stored no due credentials renewed them")
	}

	// A refusal after a save is recorded like the sweep's, and the save
	// itself succeeded.
	refused := create("refused", oauth("rt-wrong", 1))
	e.eventually("the refused refresh", func() bool {
		states, _ := e.db.AccountRefreshStates(ctx, []int64{refused})
		return states[refused] != nil && states[refused].ErrorType == "auth_rejected"
	})
	if e.creds(refused)["access_token"] != "at-old" {
		t.Fatal("a refused refresh changed the credentials")
	}
}

// lockedBuffer is a log sink written by a background goroutine.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// The renewal a save starts runs outside every request's recovery: a panic
// in it is recovered and logged, and does not take the process (here, the
// test binary) down.
func TestRefreshAfterSaveRecoversPanic(t *testing.T) {
	var logs lockedBuffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	s := &Service{} // no database: the renewal panics at its first read
	bt := core.AccountTypeBinding{Type: manifest.AccountType{Refresh: &manifest.AccountRefresh{}}, Client: &fakePlatform{}}
	s.refreshSaved(context.Background(), 1, bt, []byte(`{"refresh_token":"rt-1","expires_at":1}`))

	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(logs.String(), "panicked") {
		if time.Now().After(deadline) {
			t.Fatalf("no recovered panic was logged: %q", logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(logs.String(), "refreshAccount") {
		t.Fatalf("the log does not show where it panicked: %q", logs.String())
	}

	// Credentials that are not due start nothing.
	logs.b.Reset()
	s.refreshSaved(context.Background(), 1, bt, []byte(`{"refresh_token":"rt-1"}`))
	time.Sleep(50 * time.Millisecond)
	if logs.String() != "" {
		t.Fatalf("a save that is not due started a renewal: %q", logs.String())
	}
}

// A renewal goes out through the account's proxy as it stands when it runs:
// a proxy changed in the console carries the next renewal.
func TestCredentialRefreshFollowsTheAccountsProxy(t *testing.T) {
	e := setup(t)
	up := newTokenUpstream(t)
	e.plat.refreshURL = up.srv.URL + "/token"
	e.gen.types[0].Type.Refresh = &manifest.AccountRefresh{}
	id := e.mkOAuthAccount("acc", "rt-1", 10*time.Minute)
	aHost, aPort, aHits := forwardProxy(t)
	bHost, bPort, bHits := forwardProxy(t)
	a := e.exec1(`INSERT INTO proxies (name, protocol, host, port) VALUES ('a', 'http', $1, $2) RETURNING id`, aHost, aPort)
	b := e.exec1(`INSERT INTO proxies (name, protocol, host, port) VALUES ('b', 'http', $1, $2) RETURNING id`, bHost, bPort)
	for i, step := range []struct{ proxy, viaA, viaB int64 }{{a, 1, 0}, {b, 1, 1}} {
		if code, out := e.do("PATCH", fmt.Sprintf("/accounts/%d", id), map[string]any{"proxy_id": step.proxy}); code != http.StatusOK {
			t.Fatalf("set proxy %d: %d %v", step.proxy, code, out)
		}
		if code, out := e.do("POST", fmt.Sprintf("/accounts/%d/refresh-credentials", id), nil); code != http.StatusOK || up.calls.Load() != int32(i+1) {
			t.Fatalf("renewal %d: %d %v (upstream calls %d)", i+1, code, out, up.calls.Load())
		}
		if aHits.Load() != step.viaA || bHits.Load() != step.viaB {
			t.Fatalf("renewal %d through proxy %d: a=%d b=%d", i+1, step.proxy, aHits.Load(), bHits.Load())
		}
	}
	// With the proxy disabled the plugin is not asked and nothing is sent.
	if _, err := e.db.SQL.ExecContext(t.Context(), `UPDATE proxies SET status = 'disabled', updated_at = clock_timestamp() WHERE id = $1`, b); err != nil {
		t.Fatal(err)
	}
	e.prx.Invalidate(b)
	builds, parses := e.plat.called("BuildRefreshRequest"), e.plat.called("ParseRefreshResponse")
	e.do("POST", fmt.Sprintf("/accounts/%d/refresh-credentials", id), nil)
	if up.calls.Load() != 2 || bHits.Load() != 1 || e.plat.called("BuildRefreshRequest") != builds || e.plat.called("ParseRefreshResponse") != parses {
		t.Fatalf("renewal through a disabled proxy: upstream calls %d, proxy hits %d, builds %d -> %d, parses %d -> %d", up.calls.Load(), bHits.Load(),
			builds, e.plat.called("BuildRefreshRequest"), parses, e.plat.called("ParseRefreshResponse"))
	}
}
