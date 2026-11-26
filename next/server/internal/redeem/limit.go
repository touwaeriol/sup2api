package redeem

import (
	"context"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

// Brute-force limits (CONTRACTS §140): redemptions per user per minute, and
// failures per user per window that lock redeeming for lockFor. Neither
// blocks when Redis fails: a 128-bit code cannot be guessed, the limits only
// slow probing down.
const (
	perMinute   = 5
	maxFailures = 10
	lockFor     = time.Hour
)

func failKey(userID int64) string { return "redeem:fail:" + x.Itoa64(userID) }

// admit checks the lockout, then counts the attempt in the user's minute.
func (s *Service) admit(ctx context.Context, userID int64) error {
	if wait := s.fails.LockedFor(ctx, failKey(userID)); wait > 0 {
		return core.RateLimitedFor(core.Text(ctx, "too many failed redemptions, try again later", "兑换失败次数过多，请稍后再试"), wait)
	}
	return core.Admit(ctx, s.windows, userID, perMinute, core.Text(ctx, "too many redemptions, try again later", "兑换过于频繁，请稍后再试"))
}
