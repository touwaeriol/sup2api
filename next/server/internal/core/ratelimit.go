package core

import (
	"context"
	"log/slog"
	"time"
)

// Admitter counts requests of a subject over the last minute (a
// ratelimit.Minute scope): Admit counts one when the subject is under rpm
// and tpm (0 = unlimited), else returns the time until it would pass.
type Admitter interface {
	Admit(ctx context.Context, id int64, rpm int, tpm int64) (ok bool, retry time.Duration, err error)
}

// RetryAfterSeconds is the details key of a rate_limited error: the whole
// seconds to wait, which httpapi.Fail also sends as Retry-After.
const RetryAfterSeconds = "retry_after_seconds"

// RateLimitedFor is the 429 rate_limited of a client that may try again
// after wait, rounded up to whole seconds and at least one: a client told 0
// would retry at once.
func RateLimitedFor(msg string, wait time.Duration) *Error {
	secs := max(int64((wait+time.Second-1)/time.Second), 1)
	return ErrRateLimited.WithMessage(msg).WithDetails(map[string]any{RetryAfterSeconds: secs})
}

// Admit counts one request of id against rpm on a, refusing with
// RateLimitedFor(msg). A nil a admits everything. A limiter that fails
// admits too (logged): the per-user and per-address limits only slow abuse
// down, so a Redis outage must not take the endpoints behind them with it.
func Admit(ctx context.Context, a Admitter, id int64, rpm int, msg string) error {
	if a == nil {
		return nil
	}
	ok, retry, err := a.Admit(ctx, id, rpm, 0)
	if err != nil {
		slog.WarnContext(ctx, "rate limit unavailable, admitting", "err", err)
		return nil
	}
	if !ok {
		return RateLimitedFor(msg, retry)
	}
	return nil
}
