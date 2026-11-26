package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newLockout(t *testing.T, restart bool) (Lockout, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return Lockout{Name: "test", RDB: rdb, Window: time.Hour, Max: 3, RestartAtMax: restart}, mr
}

func TestLockoutLocksAtMax(t *testing.T) {
	l, mr := newLockout(t, false)
	ctx := context.Background()
	for i := range l.Max {
		if w := l.LockedFor(ctx, "k"); w != 0 {
			t.Fatalf("failure %d: locked early (%v)", i, w)
		}
		if n := l.Fail(ctx, "k"); n != i+1 {
			t.Fatalf("count %d, want %d", n, i+1)
		}
	}
	mr.FastForward(10 * time.Minute)
	// Without RestartAtMax the lock runs to the end of the first window.
	if w := l.LockedFor(ctx, "k"); w <= 49*time.Minute || w > 50*time.Minute {
		t.Fatalf("locked for %v", w)
	}
	if l.LockedFor(ctx, "other") != 0 {
		t.Fatal("another key is locked")
	}
	l.Reset(ctx, "k")
	if w := l.LockedFor(ctx, "k"); w != 0 {
		t.Fatalf("after reset: %v", w)
	}
}

func TestLockoutRestartAtMax(t *testing.T) {
	l, mr := newLockout(t, true)
	ctx := context.Background()
	l.Fail(ctx, "k")
	mr.FastForward(30 * time.Minute)
	l.Fail(ctx, "k")
	l.Fail(ctx, "k")
	// The failure reaching Max starts a whole window.
	if w := l.LockedFor(ctx, "k"); w <= 59*time.Minute {
		t.Fatalf("locked for %v", w)
	}
	mr.FastForward(time.Hour + time.Second)
	if w := l.LockedFor(ctx, "k"); w != 0 {
		t.Fatalf("after the window: %v", w)
	}
}

func TestLockoutWithoutTTL(t *testing.T) {
	l, mr := newLockout(t, false)
	ctx := context.Background()
	// A counter that lost its TTL locks for a whole window, and the next
	// failure gives it one again.
	if err := mr.Set("k", "5"); err != nil {
		t.Fatal(err)
	}
	if w := l.LockedFor(ctx, "k"); w != l.Window {
		t.Fatalf("locked for %v", w)
	}
	l.Fail(ctx, "k")
	if ttl := mr.TTL("k"); ttl <= 0 {
		t.Fatalf("ttl %v", ttl)
	}
}

func TestLockoutFailsOpen(t *testing.T) {
	l, mr := newLockout(t, false)
	ctx := context.Background()
	for range l.Max {
		l.Fail(ctx, "k")
	}
	// A client that fails at once (no retries) on a Redis that is gone.
	down := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1, DialerRetries: 1, DialerRetryTimeout: time.Millisecond})
	t.Cleanup(func() { _ = down.Close() })
	mr.Close()
	l.RDB = down
	if w := l.LockedFor(ctx, "k"); w != 0 {
		t.Fatalf("redis down locked: %v", w)
	}
	if n := l.Fail(ctx, "k"); n != 0 {
		t.Fatalf("redis down counted %d", n)
	}
	l.Reset(ctx, "k")
	var none Lockout
	if none.LockedFor(ctx, "k") != 0 || none.Fail(ctx, "k") != 0 {
		t.Fatal("a lockout without Redis counted")
	}
	none.Reset(ctx, "k")
}

func TestLockedForReadsSeveralKeys(t *testing.T) {
	few, mr := newLockout(t, false)
	many := few
	many.Max = 10
	ctx := context.Background()
	for range few.Max {
		few.Fail(ctx, "a")
		many.Fail(ctx, "b")
	}
	// "b" is under its own Max although it has as many failures as "a".
	if w := LockedFor(ctx, many.On("b")); w != 0 {
		t.Fatalf("b locked for %v", w)
	}
	mr.FastForward(time.Minute)
	if w := LockedFor(ctx, many.On("b"), few.On("a")); w <= 58*time.Minute || w > 59*time.Minute {
		t.Fatalf("locked for %v", w)
	}
	if LockedFor(ctx) != 0 {
		t.Fatal("no checks locked")
	}
}

func TestCountCapsAttempts(t *testing.T) {
	l, _ := newLockout(t, false)
	ctx := context.Background()
	for i := range l.Max + 2 {
		if n := l.Count(ctx, "k"); n != i+1 {
			t.Fatalf("count %d, want %d", n, i+1)
		}
	}
}
