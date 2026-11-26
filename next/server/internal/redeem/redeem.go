// Package redeem holds the core redeem codes (CONTRACTS §140): admins
// generate them in batches, users redeem one into their own balance. Only
// the SHA-256 of a code and its last four characters are stored; the plain
// code is in the generate response once and nowhere else.
package redeem

import (
	"context"
	"net/http"

	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
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

// kindRedeem is billing.KindRedeem; modules do not import billing.
const kindRedeem = "redeem"

// maxValue is the ledger's cap on one credit (the old payment plugin's
// ledger.credit maxPerTx): a code is one credit, so it cannot be worth more.
var maxValue = decimal.NewFromInt(100000)

// Ledger is core.Ledger plus the balance cache refresh billing needs after a
// transaction that called ApplyTx commits (as usage.TxLedger);
// *billing.Service implements it.
type Ledger interface {
	core.Ledger
	CacheBalance(ctx context.Context, userID, ledgerID int64, balance decimal.Decimal)
}

// Service serves redeem codes.
type Service struct {
	db     *store.DB
	ledger Ledger
	// windows caps redemptions per user per minute (nil: no cap).
	windows core.RateWindows
	fails   failLock
}

// New builds the service. windows counts redemptions per user (the app
// gives it a ratelimit.Minute of scope "redeem"); rdb holds the failure
// lockout. Without Redis neither limits anything, like the login limits.
func New(db *store.DB, ledger Ledger, rdb redis.Cmdable, windows core.RateWindows) *Service {
	return &Service{db: db, ledger: ledger, windows: windows, fails: failLock{rdb: rdb}}
}

// errInvalidCode is the one answer for a code that does not exist, is used,
// voided or expired: it never tells which.
func errInvalidCode(ctx context.Context) *core.Error {
	return core.NewError(http.StatusBadRequest, "invalid_redeem_code",
		core.Text(ctx, "invalid or used redeem code", "兑换码无效或已使用"))
}
