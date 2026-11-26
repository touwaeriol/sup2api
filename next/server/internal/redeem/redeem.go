// Package redeem holds the core redeem codes (CONTRACTS §140): admins
// generate them in batches, users redeem one into their own balance. Only
// the SHA-256 of a code and its last four characters are stored; the plain
// code is in the generate response once and nowhere else.
package redeem

import (
	"context"
	"net/http"

	"github.com/redis/go-redis/v9"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/ratelimit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// Code statuses as stored; expired is reported for an unused code past its
// expires_at and never stored.
const (
	statusUnused  = "unused"
	statusUsed    = "used"
	statusVoided  = "voided"
	statusExpired = "expired"
)

// The two states of an unused code, on redeem_codes (qualified, so that they
// hold in a join too): only a redeemable code can be redeemed.
const (
	redeemable = "redeem_codes.status = '" + statusUnused + "' AND (redeem_codes.expires_at IS NULL OR redeem_codes.expires_at > now())"
	expired    = "redeem_codes.status = '" + statusUnused + "' AND redeem_codes.expires_at <= now()"
)

// Service serves redeem codes.
type Service struct {
	db     *store.DB
	ledger core.CachingLedger
	// windows caps redemptions per user per minute (nil: no cap).
	windows core.Admitter
	fails   ratelimit.Lockout
}

// New builds the service. windows counts redemptions per user (the app
// gives it a ratelimit.Minute of scope "redeem"); rdb holds the failure
// lockout. Without Redis neither limits anything, like the login limits.
func New(db *store.DB, ledger core.CachingLedger, rdb redis.Cmdable, windows core.Admitter) *Service {
	return &Service{db: db, ledger: ledger, windows: windows, fails: ratelimit.Lockout{
		Name: "redeem failures", RDB: rdb, Window: lockFor, Max: maxFailures, RestartAtMax: true}}
}

// codeInvalid is the code of the one answer for a code that does not exist,
// is used, voided or expired: it never tells which.
const codeInvalid = "invalid_redeem_code"

func errInvalidCode(ctx context.Context) *core.Error {
	return core.NewError(http.StatusBadRequest, codeInvalid, core.Text(ctx, "invalid or used redeem code", "兑换码无效或已使用"))
}
