package redeem

import (
	"context"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

// Brute-force limits (CONTRACTS §140): redemptions per user per minute, and
// failures per user per window that lock redeeming for lockFor.
const (
	perMinute   = 5
	maxFailures = 10
	lockFor     = time.Hour
)

// countFailure increments a user's failure counter. Its window starts at the
// first failure and starts again at the limit, so the failure that reaches
// it locks for a whole lockFor; a key that lost its TTL gets one again, so a
// counter never locks anyone out forever (as the login limiter).
var countFailure = redis.NewScript(`
local n = redis.call('INCR', KEYS[1])
if n == 1 or n == tonumber(ARGV[2]) or redis.call('PTTL', KEYS[1]) < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return n`)

// failLock counts failed redemptions in Redis. Redis errors never block a
// redemption: a 128-bit code cannot be guessed, the limit only slows
// probing down, so the lock logs a warning and lets the attempt through.
type failLock struct{ rdb redis.Cmdable }

func failKey(userID int64) string { return "redeem:fail:" + x.Itoa64(userID) }

// lockedFor is how long the user may not redeem: 0 when not locked.
func (l failLock) lockedFor(ctx context.Context, userID int64) time.Duration {
	if l.rdb == nil {
		return 0
	}
	key := failKey(userID)
	var n *redis.StringCmd
	var ttl *redis.DurationCmd
	_, err := l.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		n, ttl = p.Get(ctx, key), p.PTTL(ctx, key)
		return nil
	})
	if err != nil && err != redis.Nil {
		slog.WarnContext(ctx, "redeem: failure lock check failed, allowing attempt", "err", err)
		return 0
	}
	if c, err := n.Int64(); err != nil || c < maxFailures {
		return 0
	}
	return max(ttl.Val(), time.Second)
}

// fail records a failed redemption.
func (l failLock) fail(ctx context.Context, userID int64) {
	if l.rdb == nil {
		return
	}
	err := countFailure.Run(context.WithoutCancel(ctx), l.rdb, []string{failKey(userID)}, lockFor.Milliseconds(), maxFailures).Err()
	if err != nil {
		slog.WarnContext(ctx, "redeem: record failed redemption", "err", err)
	}
}

// admit checks the lockout, then counts the attempt in the user's minute.
func (s *Service) admit(ctx context.Context, userID int64) error {
	if wait := s.fails.lockedFor(ctx, userID); wait > 0 {
		return rateLimited(ctx, wait, "too many failed redemptions, try again later", "兑换失败次数过多，请稍后再试")
	}
	if s.windows == nil {
		return nil
	}
	ok, retry, err := s.windows.Admit(ctx, userID, perMinute, 0)
	if err != nil {
		slog.WarnContext(ctx, "redeem: rate limit check failed, allowing attempt", "err", err)
		return nil
	}
	if !ok {
		return rateLimited(ctx, retry, "too many redemptions, try again later", "兑换过于频繁，请稍后再试")
	}
	return nil
}

// rateLimited is the 429 with the seconds to wait (at least one).
func rateLimited(ctx context.Context, wait time.Duration, en, zh string) error {
	secs := max(int64((wait+time.Second-1)/time.Second), 1)
	return core.ErrRateLimited.WithMessage(core.Text(ctx, en, zh)).
		WithDetails(map[string]any{"retry_after_seconds": secs})
}
