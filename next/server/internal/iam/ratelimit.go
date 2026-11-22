package iam

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// Login rate limit (CONTRACTS §14.2): failures are counted per email+IP and
// per IP in fixed windows that start at the first failure.
const (
	loginFailWindow     = 15 * time.Minute
	loginFailMaxEmailIP = 5
	loginFailMaxIP      = 20
)

// incrWindow increments a counter and starts its window on the first hit.
// A key that lost its TTL (e.g. EXPIRE failed earlier) gets one again, so a
// counter can never lock a client out forever.
var incrWindow = redis.NewScript(`
local n = redis.call('INCR', KEYS[1])
if n == 1 or redis.call('PTTL', KEYS[1]) < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return n`)

// loginLimiter counts failed logins in Redis. Redis errors never block a
// login: the limiter logs a warning and lets the attempt through.
type loginLimiter struct {
	rdb      redis.Cmdable
	window   time.Duration
	maxEmail int64
	maxIP    int64
}

func newLoginLimiter(rdb redis.Cmdable) *loginLimiter {
	return &loginLimiter{rdb: rdb, window: loginFailWindow, maxEmail: loginFailMaxEmailIP, maxIP: loginFailMaxIP}
}

func limiterEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func loginEmailKey(email, ip string) string {
	h := sha256.Sum256([]byte(limiterEmail(email) + "|" + ip))
	return "login:fail:e:" + hex.EncodeToString(h[:])
}

func loginIPKey(ip string) string { return "login:fail:ip:" + ip }

// check returns how long the caller must wait, or 0 when a login attempt is
// allowed.
func (l *loginLimiter) check(ctx context.Context, email, ip string) time.Duration {
	if l == nil || l.rdb == nil {
		return 0
	}
	ek, ik := loginEmailKey(email, ip), loginIPKey(ip)
	var eCnt, iCnt *redis.StringCmd
	var eTTL, iTTL *redis.DurationCmd
	_, err := l.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		eCnt, eTTL = p.Get(ctx, ek), p.PTTL(ctx, ek)
		iCnt, iTTL = p.Get(ctx, ik), p.PTTL(ctx, ik)
		return nil
	})
	if err != nil && err != redis.Nil {
		slog.WarnContext(ctx, "iam: login rate limit check failed, allowing attempt", "err", err)
		return 0
	}
	return max(l.lockedFor(eCnt, eTTL, l.maxEmail), l.lockedFor(iCnt, iTTL, l.maxIP))
}

// lockedFor is how long a counter at cnt, with ttl left of its window,
// locks a client out: 0 below limit, the rest of the window (the whole
// window for a counter without a TTL) at it or above.
func (l *loginLimiter) lockedFor(cnt *redis.StringCmd, ttl *redis.DurationCmd, limit int64) time.Duration {
	n, err := cnt.Int64()
	if err != nil || n < limit {
		return 0
	}
	if d := ttl.Val(); d > 0 {
		return d
	}
	return l.window
}

// fail records a failed attempt for email+IP and IP.
func (l *loginLimiter) fail(ctx context.Context, email, ip string) {
	if l == nil || l.rdb == nil {
		return
	}
	ms := l.window.Milliseconds()
	for _, k := range []string{loginEmailKey(email, ip), loginIPKey(ip)} {
		if err := incrWindow.Run(ctx, l.rdb, []string{k}, ms).Err(); err != nil {
			slog.WarnContext(ctx, "iam: record failed login", "err", err)
			return
		}
	}
}

// reset clears the email+IP counter after a successful login. The IP counter
// is kept so one valid account cannot be used to reset a spraying budget.
func (l *loginLimiter) reset(ctx context.Context, email, ip string) {
	if l == nil || l.rdb == nil {
		return
	}
	if err := l.rdb.Del(ctx, loginEmailKey(email, ip)).Err(); err != nil {
		slog.WarnContext(ctx, "iam: clear failed login counter", "err", err)
	}
}

// passkeyBeginLimit bounds how many anonymous passkey logins an address starts
// per window: each one stores a flow until it expires.
const (
	passkeyBeginWindow = time.Minute
	passkeyBeginMax    = 30
)

// allowPasskeyBegin counts a started passkey login. Redis errors let it through,
// like the other login limits.
func (l *loginLimiter) allowPasskeyBegin(ctx context.Context, ip string) time.Duration {
	if l == nil || l.rdb == nil {
		return 0
	}
	n, err := incrWindow.Run(ctx, l.rdb, []string{"login:passkey:ip:" + ip}, passkeyBeginWindow.Milliseconds()).Int64()
	if err != nil {
		slog.WarnContext(ctx, "iam: passkey login rate limit failed, allowing attempt", "err", err)
		return 0
	}
	if n > passkeyBeginMax {
		return passkeyBeginWindow
	}
	return 0
}

// rateLimitedError is the 429 returned while a client is locked out.
func rateLimitedError(ctx context.Context, wait time.Duration) error {
	secs := int64((wait + time.Second - 1) / time.Second)
	if secs < 1 {
		secs = 1
	}
	return core.ErrRateLimited.WithMessage(t(ctx,
		"too many failed login attempts, try again later",
		"登录失败次数过多，请稍后再试")).WithDetails(map[string]any{"retry_after_seconds": secs})
}

func t(ctx context.Context, en, zh string) string { return core.Text(ctx, en, zh) }
