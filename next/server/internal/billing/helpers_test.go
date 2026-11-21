package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/event"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// memBus is an in-process core.Bus.
type memBus struct {
	mu   sync.Mutex
	subs map[string][]func([]byte)
	sent []string
}

func (b *memBus) Publish(_ context.Context, ch string, payload []byte) error {
	b.mu.Lock()
	subs := append([]func([]byte){}, b.subs[ch]...)
	b.sent = append(b.sent, ch+" "+string(payload))
	b.mu.Unlock()
	for _, f := range subs {
		f(payload)
	}
	return nil
}

func (b *memBus) Subscribe(ch string, h func([]byte)) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subs == nil {
		b.subs = map[string][]func([]byte){}
	}
	b.subs[ch] = append(b.subs[ch], h)
	return func() {}
}

// allowAll authenticates "Bearer <user id>" and grants every permission.
type allowAll struct{}

func (allowAll) VerifySession(_ context.Context, token string) (int64, string, error) {
	return testutil.SessionUser(token, "")
}
func (allowAll) Can(context.Context, int64, string) (bool, error) { return true, nil }
func (allowAll) PermissionSet(context.Context, int64) (core.PermissionSet, error) {
	return core.PermissionSet{Superuser: true}, nil
}
func (allowAll) CanGrant(context.Context, int64, []string) error { return nil }
func (allowAll) CanActOnUsers(context.Context, int64, core.UserAction, ...int64) error {
	return nil
}

type env struct {
	t   *testing.T
	db  *store.DB
	mr  *miniredis.Miniredis
	rdb *redis.Client
	bus *memBus
	svc *Service
	h   http.Handler
}

func newEnv(t *testing.T) *env { return newEnvAs(t, allowAll{}) }

// limited is an authorizer under which the users listed hold only the
// permissions given; everyone else holds all.
type limited struct {
	allowAll
	perms map[int64][]string
}

func (l limited) Can(_ context.Context, uid int64, permission string) (bool, error) {
	perms, ok := l.perms[uid]
	return !ok || slices.Contains(perms, permission), nil
}

// newEnvAs is newEnv with authz deciding who holds which permission.
func newEnvAs(t *testing.T, authz core.Authorizer) *env {
	t.Helper()
	db := testutil.DB(t)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	bus := &memBus{}
	svc := New(db, rdb, bus, event.NewPublisher(db), nil)
	svc.Authz = authz
	svc.Users = allowAll{}
	t.Cleanup(svc.Close)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	r := httpapi.NewRouter(engine, allowAll{}, authz)
	svc.RegisterRoutes(r)
	return &env{t: t, db: db, mr: mr, rdb: rdb, bus: bus, svc: svc, h: engine}
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.db.SQL.ExecContext(context.Background(), sql, args...); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
}

func (e *env) user(email string) int64 {
	e.t.Helper()
	var id int64
	err := e.db.SQL.QueryRowContext(context.Background(),
		`INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id`, email).Scan(&id)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

// call performs a request as user uid and decodes the JSON response.
func (e *env) call(uid int64, method, path string, body any, headers ...string) (int, map[string]any) {
	e.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, "/api/v1"+path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strconv.FormatInt(uid, 10))
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	var out map[string]any
	if w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			e.t.Fatalf("%s %s: bad JSON %q", method, path, w.Body.String())
		}
	}
	return w.Code, out
}

func (e *env) mustCall(uid int64, want int, method, path string, body any, headers ...string) map[string]any {
	e.t.Helper()
	code, out := e.call(uid, method, path, body, headers...)
	if code != want {
		e.t.Fatalf("%s %s: status %d, want %d: %v", method, path, code, want, out)
	}
	return out
}

func data(m map[string]any) map[string]any {
	d, _ := m["data"].(map[string]any)
	return d
}

func str(v any) string { return fmt.Sprint(v) }

func errCode(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	return str(e["code"])
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// oneTierConfig is a per_token config of one tier with prices.
func oneTierConfig(prices map[string]any) map[string]any {
	tier := map[string]any{"name": "base"}
	maps.Copy(tier, prices)
	return map[string]any{"tiers": []any{tier}, "rules": []any{}}
}
