package iam

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/config"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

func newTestLimiter(t *testing.T) (*loginLimiter, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	return newLoginLimiter(redis.NewClient(&redis.Options{Addr: mr.Addr()})), mr
}

func TestLoginLimiterEmailIP(t *testing.T) {
	t.Parallel()
	l, mr := newTestLimiter(t)
	ctx := context.Background()
	const ip = "198.51.100.7"
	for i := 0; i < loginFailMaxEmailIP; i++ {
		if w := l.check(ctx, "A@example.com", ip); w != 0 {
			t.Fatalf("attempt %d blocked early (%v)", i, w)
		}
		// Case and spaces do not create separate counters.
		l.fail(ctx, " a@EXAMPLE.com ", ip)
	}
	w := l.check(ctx, "a@example.com", ip)
	if w <= 0 || w > loginFailWindow {
		t.Fatalf("wait after %d failures = %v", loginFailMaxEmailIP, w)
	}
	if !mr.Exists(loginEmailKey("a@example.com", ip)) || !strings.HasPrefix(loginEmailKey("a@example.com", ip), "login:fail:e:") {
		t.Fatal("email+ip key")
	}
	if got := mr.TTL(loginEmailKey("a@example.com", ip)); got <= 0 || got > loginFailWindow {
		t.Fatalf("ttl = %v", got)
	}
	// Other emails from the same IP and the same email from another IP pass.
	if w := l.check(ctx, "b@example.com", ip); w != 0 {
		t.Fatalf("other email blocked: %v", w)
	}
	if w := l.check(ctx, "a@example.com", "198.51.100.8"); w != 0 {
		t.Fatalf("other ip blocked: %v", w)
	}
	// Success clears email+IP but keeps the IP counter.
	l.reset(ctx, "a@example.com", ip)
	if w := l.check(ctx, "a@example.com", ip); w != 0 {
		t.Fatalf("after reset: %v", w)
	}
	if n, _ := mr.Get(loginIPKey(ip)); n != "5" {
		t.Fatalf("ip counter = %q", n)
	}
	// The window expires.
	for i := 0; i < loginFailMaxEmailIP; i++ {
		l.fail(ctx, "a@example.com", ip)
	}
	if l.check(ctx, "a@example.com", ip) == 0 {
		t.Fatal("not blocked again")
	}
	mr.FastForward(loginFailWindow + time.Second)
	if w := l.check(ctx, "a@example.com", ip); w != 0 {
		t.Fatalf("after window: %v", w)
	}
}

func TestLoginLimiterIP(t *testing.T) {
	t.Parallel()
	l, mr := newTestLimiter(t)
	ctx := context.Background()
	const ip = "2001:db8::1"
	for i := 0; i < loginFailMaxIP; i++ {
		l.fail(ctx, "user"+string(rune('a'+i))+"@example.com", ip)
	}
	if w := l.check(ctx, "fresh@example.com", ip); w <= 0 {
		t.Fatal("ip limit not applied")
	}
	if w := l.check(ctx, "fresh@example.com", "2001:db8::2"); w != 0 {
		t.Fatalf("other ip: %v", w)
	}
	// A key that lost its TTL gets one on the next failure.
	mr.Set(loginIPKey("203.0.113.9"), "3")
	l.fail(ctx, "x@example.com", "203.0.113.9")
	if got := mr.TTL(loginIPKey("203.0.113.9")); got <= 0 {
		t.Fatalf("ttl not restored: %v", got)
	}
}

func TestLoginLimiterRedisDown(t *testing.T) {
	t.Parallel()
	l, mr := newTestLimiter(t)
	ctx := context.Background()
	for i := 0; i < loginFailMaxIP; i++ {
		l.fail(ctx, "a@example.com", "192.0.2.50")
	}
	mr.Close()
	// Fails open: no block, no panic.
	if w := l.check(ctx, "a@example.com", "192.0.2.50"); w != 0 {
		t.Fatalf("redis down blocked: %v", w)
	}
	l.fail(ctx, "a@example.com", "192.0.2.50")
	l.reset(ctx, "a@example.com", "192.0.2.50")
	var nilLimiter *loginLimiter
	if nilLimiter.check(ctx, "a", "b") != 0 {
		t.Fatal("nil limiter")
	}
}

func TestLoginLocked(t *testing.T) {
	t.Parallel()
	err := loginLocked(context.Background(), 1500*time.Millisecond)
	if !isCode(err, "rate_limited") || core.AsError(err).Details["retry_after_seconds"] != int64(2) {
		t.Fatalf("locked: %v", err)
	}
	zh := loginLocked(core.WithLocale(context.Background(), "zh"), time.Second)
	if !strings.Contains(core.AsError(zh).Message, "登录") {
		t.Fatalf("zh message: %q", core.AsError(zh).Message)
	}
}

// TestLoginLockedHTTP drives POST /auth/login while locked out: the handler
// answers 429 before touching the database.
func TestLoginLockedHTTP(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	svc := New(Deps{Redis: redis.NewClient(&redis.Options{Addr: mr.Addr()}), Config: &config.Config{}, BcryptCost: bcrypt.MinCost})
	const ip = "192.0.2.77"
	mr.Set(loginEmailKey("root@example.com", ip), "5")
	mr.SetTTL(loginEmailKey("root@example.com", ip), 90*time.Second)

	r := gin.New()
	r.POST("/auth/login", svc.handleLogin)
	req := httptest.NewRequest("POST", "/auth/login", strings.NewReader(`{"email":"Root@example.com","password":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = ip + ":40000"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 429 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if w.Header().Get("Retry-After") != "90" {
		t.Fatalf("Retry-After = %q", w.Header().Get("Retry-After"))
	}
	var body struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "rate_limited" || body.Error.Details["retry_after_seconds"] != float64(90) {
		t.Fatalf("body: %s", w.Body)
	}
}

// ------------------------------------------------------------ database

func TestLoginRateLimitDB(t *testing.T) {
	t.Parallel()
	e := setup(t)
	s, ctx := e.svc, context.Background()
	if err := s.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	const ip = "192.0.2.10"
	for i := 0; i < loginFailMaxEmailIP; i++ {
		if _, err := s.Login(ctx, adminEmail, "wrong-password", ip); !isCode(err, "unauthenticated") {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	// Locked out even with the right password.
	if _, err := s.Login(ctx, adminEmail, adminPassword, ip); !isCode(err, "rate_limited") {
		t.Fatalf("locked: %v", err)
	}
	// Another IP still works, and a success there does not unlock ip.
	if _, err := s.Login(ctx, adminEmail, adminPassword, "192.0.2.11"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Login(ctx, adminEmail, adminPassword, ip); !isCode(err, "rate_limited") {
		t.Fatalf("still locked: %v", err)
	}
	e.redis.FastForward(loginFailWindow + time.Second)
	if _, err := s.Login(ctx, adminEmail, adminPassword, ip); err != nil {
		t.Fatalf("after window: %v", err)
	}
}
