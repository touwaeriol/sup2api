package core

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRateLimitedForRoundsUp(t *testing.T) {
	for _, c := range []struct {
		wait time.Duration
		want int64
	}{{1500 * time.Millisecond, 2}, {time.Millisecond, 1}, {0, 1}, {-time.Second, 1}, {15 * time.Minute, 900}} {
		e := RateLimitedFor("wait", c.wait)
		if e.Code != "rate_limited" || e.Status != 429 || e.Message != "wait" || e.Details[RetryAfterSeconds] != c.want {
			t.Errorf("%v: %+v, want %d seconds", c.wait, e, c.want)
		}
	}
}

type admitter struct {
	ok    bool
	retry time.Duration
	err   error
}

func (a admitter) Admit(context.Context, int64, int, int64) (bool, time.Duration, error) {
	return a.ok, a.retry, a.err
}

func TestAdmit(t *testing.T) {
	ctx := context.Background()
	for _, a := range []Admitter{nil, admitter{ok: true}, admitter{err: errors.New("redis down")}} {
		if err := Admit(ctx, a, 1, 5, "slow"); err != nil {
			t.Errorf("%v: %v", a, err)
		}
	}
	err := Admit(ctx, admitter{retry: 3 * time.Second}, 1, 5, "slow")
	if e := AsError(err); e.Code != "rate_limited" || e.Details[RetryAfterSeconds] != int64(3) {
		t.Errorf("refused: %v", err)
	}
}
