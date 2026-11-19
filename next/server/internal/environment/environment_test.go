package environment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

type fakeTokens struct{}

func (fakeTokens) VerifySession(_ context.Context, tok string) (int64, string, error) {
	return testutil.SessionUser(tok, "u")
}

type fakeAuthz struct {
	admin int64
	// perms are the keys held by other users.
	perms map[int64][]string
}

func (a fakeAuthz) Can(ctx context.Context, uid int64, key string) (bool, error) {
	set, err := a.PermissionSet(ctx, uid)
	return set.Has(key), err
}
func (a fakeAuthz) PermissionSet(_ context.Context, uid int64) (core.PermissionSet, error) {
	set := core.PermissionSetOf(a.perms[uid]...)
	set.Superuser = uid == a.admin
	return set, nil
}
func (fakeAuthz) CanGrant(context.Context, int64, []string) error { return nil }

type fakeBus struct {
	mu   sync.Mutex
	msgs []string
}

func (b *fakeBus) Publish(_ context.Context, ch string, p []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.msgs = append(b.msgs, ch+" "+string(p))
	return nil
}
func (b *fakeBus) Subscribe(string, func([]byte)) func() { return func() {} }

type env struct {
	t     *testing.T
	db    *store.DB
	svc   *Service
	bus   *fakeBus
	h     http.Handler
	admin int64
	user  int64
	// ownRead holds account:read:own, ownCreate account:create:own only.
	ownRead, ownCreate int64
}

func setup(t *testing.T) *env {
	gin.SetMode(gin.TestMode)
	db := testutil.DB(t)
	e := &env{t: t, db: db, bus: &fakeBus{}}
	e.admin = mkUser(t, db, "admin@x.com")
	e.user = mkUser(t, db, "user@x.com")
	e.ownRead = mkUser(t, db, "own-read@x.com")
	e.ownCreate = mkUser(t, db, "own-create@x.com")
	engine := gin.New()
	e.svc = New(db, e.bus, core.EnvironmentValues{core.EnvVarRelayTimeoutSeconds: 900, core.EnvVarStreamingIdleTimeoutSeconds: 45})
	e.svc.RegisterRoutes(httpapi.NewRouter(engine, fakeTokens{}, fakeAuthz{admin: e.admin, perms: map[int64][]string{
		e.ownRead:   {"account:read:own", "account:update:own"},
		e.ownCreate: {"account:create:own"},
	}}))
	e.h = engine
	return e
}

func mkUser(t *testing.T, db *store.DB, email string) int64 {
	var id int64
	if err := db.SQL.QueryRowContext(context.Background(),
		`INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *env) do(uid int64, method, path string, body any) (int, map[string]any) {
	e.t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(b))
	req.Header.Set("Authorization", fmt.Sprintf("Bearer u%d", uid))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestGlobalLayerRoutes(t *testing.T) {
	e := setup(t)
	// The declarations are for the editors, not for every signed-in user.
	if code, _ := e.do(e.user, "GET", "/environment/variables", nil); code != 403 {
		t.Fatalf("variables perm: %d", code)
	}
	code, out := e.do(e.admin, "GET", "/environment/variables", nil)
	if code != 200 || len(out["data"].(map[string]any)["variables"].([]any)) != len(core.EnvironmentVariables) {
		t.Fatalf("variables: %d %v", code, out)
	}
	// The account editor of a user with the own-account permissions only
	// shows the declarations too (they may edit the account layer).
	for _, uid := range []int64{e.ownRead, e.ownCreate} {
		if code, out := e.do(uid, "GET", "/environment/variables", nil); code != 200 {
			t.Fatalf("variables for own-account user %d: %d %v", uid, code, out)
		}
		if code, _ := e.do(uid, "GET", "/settings/environment", nil); code != 403 {
			t.Fatalf("global layer for own-account user %d: %d", uid, code)
		}
	}
	if code, _ = e.do(e.user, "GET", "/settings/environment", nil); code != 403 {
		t.Fatalf("perm: %d", code)
	}
	_, out = e.do(e.admin, "GET", "/settings/environment", nil)
	if fmt.Sprint(out["data"]) != "map[values:map[]]" {
		t.Fatalf("empty: %v", out)
	}
	// An unknown key is refused.
	if code, out = e.do(e.admin, "PUT", "/settings/environment", map[string]any{"values": map[string]int64{"nope": 1}}); code != 400 {
		t.Fatalf("unknown: %d %v", code, out)
	}
	if got := e.svc.GlobalEnvironment(context.Background()); len(got) != 2 || got[core.EnvVarResponseHeaderTimeoutSeconds] != 0 {
		t.Fatalf("cached before put, the process layer alone: %v", got)
	}
	code, out = e.do(e.admin, "PUT", "/settings/environment", map[string]any{"values": map[string]int64{
		"response_header_timeout_seconds": 64, "relay_timeout_seconds": 0}})
	if code != 200 || fmt.Sprint(out["data"]) != "map[values:map[response_header_timeout_seconds:64]]" {
		t.Fatalf("put: %d %v", code, out)
	}
	if len(e.bus.msgs) != 1 || e.bus.msgs[0] != core.ChannelConfigChanged+` {"key":"settings.environment"}` {
		t.Fatalf("broadcast: %v", e.bus.msgs)
	}
	// The put invalidated the cached (empty) layer.
	if got := e.svc.GlobalEnvironment(context.Background()); got[core.EnvVarResponseHeaderTimeoutSeconds] != 64 {
		t.Fatalf("global: %v", got)
	}
	_, out = e.do(e.admin, "GET", "/settings/environment", nil)
	if fmt.Sprint(out["data"]) != "map[values:map[response_header_timeout_seconds:64]]" {
		t.Fatalf("get: %v", out)
	}
}

func TestResolveEnvironment(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if _, out := e.do(e.admin, "PUT", "/settings/environment", map[string]any{"values": map[string]int64{
		"relay_timeout_seconds": 300, "response_header_timeout_seconds": 50}}); out["data"] == nil {
		t.Fatalf("put: %v", out)
	}
	var groupID, accountID int64
	if err := e.db.SQL.QueryRowContext(ctx, `INSERT INTO groups (name, environment)
		VALUES ('g', '{"relay_timeout_seconds": 120, "task_poll_max_failures": 3}') RETURNING id`).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	if err := e.db.SQL.QueryRowContext(ctx, `INSERT INTO accounts (name, plugin_key, type, credentials_enc, environment)
		VALUES ('a', 'p', 't', ''::bytea, '{"relay_timeout_seconds": 60, "max_request_body_mb": 10}') RETURNING id`).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	got, err := e.svc.ResolveEnvironment(ctx, groupID, accountID)
	if err != nil {
		t.Fatal(err)
	}
	// A key that is not declared (max_request_body_mb was, until the request
	// body limit became a gateway setting) is skipped.
	if _, stale := got["max_request_body_mb"]; stale || got[core.EnvVarRelayTimeoutSeconds] != 60 || got[core.EnvVarTaskPollMaxFailures] != 3 ||
		got[core.EnvVarResponseHeaderTimeoutSeconds] != 50 {
		t.Fatalf("resolved: %v", got)
	}
	got, err = e.svc.ResolveEnvironment(ctx, groupID, 0)
	if err != nil || got[core.EnvVarRelayTimeoutSeconds] != 120 {
		t.Fatalf("group alone: %v %v", got, err)
	}
	// The global layer wins over the process layer, which fills what it leaves unset.
	got, err = e.svc.ResolveEnvironment(ctx, 0, 0)
	if err != nil || got[core.EnvVarRelayTimeoutSeconds] != 300 || got[core.EnvVarStreamingIdleTimeoutSeconds] != 45 {
		t.Fatalf("global alone: %v %v", got, err)
	}
	// The async task variables have no global layer: the reconcile settings are.
	if code, _ := e.do(e.admin, "PUT", "/settings/environment", map[string]any{"values": map[string]int64{"task_poll_max_failures": 9}}); code != 400 {
		t.Fatalf("task variable on the global layer: %d", code)
	}
}

// The refill does not run on the caller's context: a cancelled client must
// not leave the process-only fallback cached for everyone.
func TestGlobalEnvironmentIgnoresCallerCancel(t *testing.T) {
	e := setup(t)
	if code, out := e.do(e.admin, "PUT", "/settings/environment", map[string]any{"values": map[string]int64{
		"response_header_timeout_seconds": 64}}); code != 200 {
		t.Fatalf("put: %d %v", code, out)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := e.svc.GlobalEnvironment(ctx); got[core.EnvVarResponseHeaderTimeoutSeconds] != 64 {
		t.Fatalf("cancelled caller: %v", got)
	}
	if got := e.svc.GlobalEnvironment(context.Background()); got[core.EnvVarResponseHeaderTimeoutSeconds] != 64 {
		t.Fatalf("cached after a cancelled caller: %v", got)
	}
}

// fakeLoader stands in for the database read of the global layer.
type fakeLoader struct {
	mu    sync.Mutex
	value int64 // response_header_timeout_seconds returned
	err   error
	gate  chan struct{} // when set, a read waits for it
	calls chan struct{} // receives one value per read started
}

func newFakeLoader(value int64) *fakeLoader {
	return &fakeLoader{value: value, calls: make(chan struct{}, 100)}
}

func (f *fakeLoader) load(ctx context.Context) (core.EnvironmentValues, error) {
	f.mu.Lock()
	value, err, gate := f.value, f.err, f.gate
	f.mu.Unlock()
	f.calls <- struct{}{}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	return core.EnvironmentValues{core.EnvVarResponseHeaderTimeoutSeconds: value}, nil
}

func (f *fakeLoader) set(value int64, err error, gate chan struct{}) {
	f.mu.Lock()
	f.value, f.err, f.gate = value, err, gate
	f.mu.Unlock()
}

func (f *fakeLoader) waitCall(t *testing.T) {
	t.Helper()
	select {
	case <-f.calls:
	case <-time.After(5 * time.Second):
		t.Fatal("no read of the global layer")
	}
}

func (f *fakeLoader) noCall(t *testing.T) {
	t.Helper()
	select {
	case <-f.calls:
		t.Fatal("unexpected read of the global layer")
	case <-time.After(50 * time.Millisecond):
	}
}

// expire makes the cached layer stale and lifts the hold after a failure.
func expire(s *Service) {
	s.mu.Lock()
	if !s.lastAt.IsZero() {
		s.lastAt = time.Now().Add(-globalTTL - time.Second)
	}
	s.retryAt = time.Time{}
	s.mu.Unlock()
}

func waitBody(t *testing.T, s *Service, want int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := s.GlobalEnvironment(context.Background())[core.EnvVarResponseHeaderTimeoutSeconds]
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("response_header_timeout_seconds = %d, want %d", got, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A failed read past the TTL keeps the last good layer instead of falling
// back to the process layer (audit 2026-10-31: a global value fell back
// to its default for 2 seconds on a database hiccup), and the database is
// left alone for errorTTL.
func TestGlobalEnvironmentKeepsLastGoodOnReadError(t *testing.T) {
	ctx := context.Background()
	s := New(nil, nil, core.EnvironmentValues{core.EnvVarRelayTimeoutSeconds: 900})
	f := newFakeLoader(100)
	s.load = f.load
	if got := s.GlobalEnvironment(ctx); got[core.EnvVarResponseHeaderTimeoutSeconds] != 100 || got[core.EnvVarRelayTimeoutSeconds] != 900 {
		t.Fatalf("first read: %v", got)
	}
	f.waitCall(t)

	f.set(0, errors.New("database hiccup"), nil)
	expire(s)
	if got := s.GlobalEnvironment(ctx); got[core.EnvVarResponseHeaderTimeoutSeconds] != 100 {
		t.Fatalf("stale: %v", got)
	}
	f.waitCall(t) // the background refresh, which fails
	waitRetryHold(t, s)
	for range 3 {
		if got := s.GlobalEnvironment(ctx); got[core.EnvVarResponseHeaderTimeoutSeconds] != 100 || got[core.EnvVarRelayTimeoutSeconds] != 900 {
			t.Fatalf("after a failed read: %v", got)
		}
	}
	f.noCall(t) // held back for errorTTL

	// Once the hold is over, the next stale read refreshes the layer.
	f.set(64, nil, nil)
	expire(s)
	waitBody(t, s, 64)
}

func waitRetryHold(t *testing.T, s *Service) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		held := !s.retryAt.IsZero()
		s.mu.Unlock()
		if held {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the failed read did not hold back the next one")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A stale layer is served at once while one background read refreshes it:
// gateway requests do not wait for the database every globalTTL.
func TestGlobalEnvironmentStaleWhileRevalidate(t *testing.T) {
	ctx := context.Background()
	s := New(nil, nil, nil)
	f := newFakeLoader(100)
	s.load = f.load
	s.GlobalEnvironment(ctx)
	f.waitCall(t)

	gate := make(chan struct{})
	f.set(64, nil, gate)
	expire(s)
	start := time.Now()
	for range 20 {
		if got := s.GlobalEnvironment(ctx); got[core.EnvVarResponseHeaderTimeoutSeconds] != 100 {
			t.Fatalf("stale value while the refresh runs: %v", got)
		}
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("callers waited for the refresh: %v", d)
	}
	f.waitCall(t)
	f.noCall(t) // one refresh for all callers
	close(gate)
	waitBody(t, s, 64)
}

// Before the first read succeeds there is nothing to keep: the process
// layer alone, and a later read is used once it succeeds.
func TestGlobalEnvironmentFirstReadFails(t *testing.T) {
	ctx := context.Background()
	s := New(nil, nil, core.EnvironmentValues{core.EnvVarRelayTimeoutSeconds: 900})
	f := newFakeLoader(0)
	f.set(0, errors.New("down"), nil)
	s.load = f.load
	if got := s.GlobalEnvironment(ctx); len(got) != 1 || got[core.EnvVarRelayTimeoutSeconds] != 900 {
		t.Fatalf("process layer alone: %v", got)
	}
	f.waitCall(t)
	s.GlobalEnvironment(ctx)
	f.noCall(t) // held back for errorTTL
	f.set(64, nil, nil)
	expire(s)
	if got := s.GlobalEnvironment(ctx); got[core.EnvVarResponseHeaderTimeoutSeconds] != 64 {
		t.Fatalf("after recovery: %v", got)
	}
}

// After invalidate (a PUT here or config:changed) callers wait for the new
// layer; if that read fails they keep the last good one.
func TestGlobalEnvironmentInvalidate(t *testing.T) {
	ctx := context.Background()
	s := New(nil, nil, nil)
	f := newFakeLoader(100)
	s.load = f.load
	s.GlobalEnvironment(ctx)
	f.waitCall(t)

	f.set(64, nil, nil)
	s.invalidate()
	if got := s.GlobalEnvironment(ctx); got[core.EnvVarResponseHeaderTimeoutSeconds] != 64 {
		t.Fatalf("read after invalidate: %v", got)
	}
	f.waitCall(t)

	f.set(0, errors.New("down"), nil)
	s.invalidate()
	if got := s.GlobalEnvironment(ctx); got[core.EnvVarResponseHeaderTimeoutSeconds] != 64 {
		t.Fatalf("failed read after invalidate: %v", got)
	}
	f.waitCall(t)
	// Still outdated: the next read after the hold waits again and succeeds.
	f.set(32, nil, nil)
	expire(s)
	if got := s.GlobalEnvironment(ctx); got[core.EnvVarResponseHeaderTimeoutSeconds] != 32 {
		t.Fatalf("read after the hold: %v", got)
	}
}
