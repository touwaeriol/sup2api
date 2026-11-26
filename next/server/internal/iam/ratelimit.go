package iam

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/ratelimit"
)

// Login rate limit (CONTRACTS §14.2): failures are counted per email+IP and
// per IP in fixed windows that start at the first failure.
const (
	loginFailWindow     = 15 * time.Minute
	loginFailMaxEmailIP = 5
	loginFailMaxIP      = 20
)

// passkeyBeginLimit bounds how many anonymous passkey logins an address starts
// per window: each one stores a flow until it expires.
const (
	passkeyBeginWindow = time.Minute
	passkeyBeginMax    = 30
)

// loginLimiter counts failed logins and started passkey logins in Redis.
// Redis errors never block a login (ratelimit.Lockout).
type loginLimiter struct {
	emailIP, ip, passkeyBegin ratelimit.Lockout
}

func newLoginLimiter(rdb redis.Cmdable) *loginLimiter {
	return &loginLimiter{
		emailIP:      ratelimit.Lockout{Name: "login email+ip", RDB: rdb, Window: loginFailWindow, Max: loginFailMaxEmailIP},
		ip:           ratelimit.Lockout{Name: "login ip", RDB: rdb, Window: loginFailWindow, Max: loginFailMaxIP},
		passkeyBegin: ratelimit.Lockout{Name: "passkey login begin", RDB: rdb, Window: passkeyBeginWindow, Max: passkeyBeginMax},
	}
}

func limiterEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func loginEmailKey(email, ip string) string {
	h := sha256.Sum256([]byte(limiterEmail(email) + "|" + ip))
	return "login:fail:e:" + hex.EncodeToString(h[:])
}

func loginIPKey(ip string) string { return "login:fail:ip:" + ip }

// check returns how long the caller must wait, or 0 when a login attempt is
// allowed; both counters are read in one round trip.
func (l *loginLimiter) check(ctx context.Context, email, ip string) time.Duration {
	if l == nil {
		return 0
	}
	return ratelimit.LockedFor(ctx, l.emailIP.On(loginEmailKey(email, ip)), l.ip.On(loginIPKey(ip)))
}

// fail records a failed attempt for email+IP and IP.
func (l *loginLimiter) fail(ctx context.Context, email, ip string) {
	if l == nil {
		return
	}
	l.emailIP.Fail(ctx, loginEmailKey(email, ip))
	l.ip.Fail(ctx, loginIPKey(ip))
}

// reset clears the email+IP counter after a successful login. The IP counter
// is kept so one valid account cannot be used to reset a spraying budget.
func (l *loginLimiter) reset(ctx context.Context, email, ip string) {
	if l != nil {
		l.emailIP.Reset(ctx, loginEmailKey(email, ip))
	}
}

// allowPasskeyBegin counts a started passkey login and returns how long the
// address must wait once it started more than passkeyBeginMax in a window.
func (l *loginLimiter) allowPasskeyBegin(ctx context.Context, ip string) time.Duration {
	if l == nil || l.passkeyBegin.Count(ctx, "login:passkey:ip:"+ip) <= passkeyBeginMax {
		return 0
	}
	return passkeyBeginWindow
}

// loginLocked is the 429 returned while a client is locked out.
func loginLocked(ctx context.Context, wait time.Duration) error {
	return core.RateLimitedFor(t(ctx, "too many failed login attempts, try again later", "登录失败次数过多，请稍后再试"), wait)
}

func t(ctx context.Context, en, zh string) string { return core.Text(ctx, en, zh) }
