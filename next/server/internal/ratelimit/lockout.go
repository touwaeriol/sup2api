package ratelimit

import (
	"context"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// countEvent increments an event counter. Its window starts at the first
// event, and again at the count ARGV[2] (0: never), so that the event
// reaching the limit locks for a whole window. A key that lost its TTL gets
// one again, so a counter never locks anyone out forever.
var countEvent = redis.NewScript(`
local n = redis.call('INCR', KEYS[1])
if n == 1 or n == tonumber(ARGV[2]) or redis.call('PTTL', KEYS[1]) < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return n`)

// Lockout counts failures per key in Redis over a fixed Window that starts
// at the first failure; at Max failures the key is locked for the rest of
// the window. It only slows guessing down, so Redis errors never lock
// anyone: they are logged and the attempt goes through, and without a Redis
// client nothing is counted.
type Lockout struct {
	// Name tells the lockout apart in logs.
	Name   string
	RDB    redis.Cmdable
	Window time.Duration
	Max    int64
	// RestartAtMax starts the window again at the failure that reaches Max,
	// so that it locks for a whole Window rather than what is left of it.
	RestartAtMax bool
}

// Check is a key of a lockout, for LockedFor.
type Check struct {
	lockout Lockout
	key     string
}

// On is the check of key.
func (l Lockout) On(key string) Check { return Check{l, key} }

// LockedFor is how long key is locked out: 0 below Max, else the rest of its
// window (the whole window for a counter that lost its TTL).
func (l Lockout) LockedFor(ctx context.Context, key string) time.Duration {
	return LockedFor(ctx, l.On(key))
}

// LockedFor is the longest lockout of checks, read in one round trip on the
// Redis of the first (lockouts checked together share one).
func LockedFor(ctx context.Context, checks ...Check) time.Duration {
	if len(checks) == 0 || checks[0].lockout.RDB == nil {
		return 0
	}
	n := make([]*redis.StringCmd, len(checks))
	ttl := make([]*redis.DurationCmd, len(checks))
	_, err := checks[0].lockout.RDB.Pipelined(ctx, func(p redis.Pipeliner) error {
		for i, c := range checks {
			n[i], ttl[i] = p.Get(ctx, c.key), p.PTTL(ctx, c.key)
		}
		return nil
	})
	if err != nil && err != redis.Nil {
		slog.WarnContext(ctx, "ratelimit: lockout check failed, allowing attempt", "lockout", checks[0].lockout.Name, "err", err)
		return 0
	}
	var wait time.Duration
	for i, c := range checks {
		wait = max(wait, c.lockout.lockedFor(n[i], ttl[i]))
	}
	return wait
}

// lockedFor is how long a counter at n with ttl left locks its key out.
func (l Lockout) lockedFor(n *redis.StringCmd, ttl *redis.DurationCmd) time.Duration {
	if c, err := n.Int64(); err != nil || c < l.Max {
		return 0
	}
	if d := ttl.Val(); d > 0 {
		return d
	}
	return l.Window
}

// Count counts one event of key (a failure, or an attempt for a lockout
// that caps attempts) and returns the count so far in its window, 0 when it
// could not be counted. It is counted even when the request was cancelled
// meanwhile: the event happened.
func (l Lockout) Count(ctx context.Context, key string) int64 {
	if l.RDB == nil {
		return 0
	}
	restart := int64(0)
	if l.RestartAtMax {
		restart = l.Max
	}
	n, err := countEvent.Run(context.WithoutCancel(ctx), l.RDB, []string{key}, l.Window.Milliseconds(), restart).Int64()
	if err != nil {
		slog.WarnContext(ctx, "ratelimit: count event", "lockout", l.Name, "err", err)
	}
	return n
}

// Fail counts a failure of key: Count, named for the lockouts that count
// failures.
func (l Lockout) Fail(ctx context.Context, key string) int64 { return l.Count(ctx, key) }

// Reset clears the failures of key.
func (l Lockout) Reset(ctx context.Context, key string) {
	if l.RDB == nil {
		return
	}
	if err := l.RDB.Del(ctx, key).Err(); err != nil {
		slog.WarnContext(ctx, "ratelimit: clear failures", "lockout", l.Name, "err", err)
	}
}
