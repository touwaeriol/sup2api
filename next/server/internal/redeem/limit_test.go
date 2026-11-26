package redeem

import (
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/ratelimit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// failBad records n failed redemptions of user with unknown codes.
func (f *fixture) failBad(user int64, n int) {
	f.t.Helper()
	for range n {
		bad, err := newCode()
		require.NoError(f.t, err)
		_, err = f.svc.Redeem(f.t.Context(), user, bad)
		requireInvalid(f.t, err)
	}
}

func TestRedeemAllowsWhenRedisFails(t *testing.T) {
	f := newFixture(t, nil)
	user := testutil.InsertUser(t, f.db, "rd-noredis@x.test")
	// A client that fails at once (no retries) on a Redis that is gone.
	down := redis.NewClient(&redis.Options{Addr: f.mr.Addr(), MaxRetries: -1, DialerRetries: 1, DialerRetryTimeout: time.Millisecond})
	t.Cleanup(func() { _ = down.Close() })
	f.mr.Close()
	f.svc = New(f.db, billing.New(f.db, down, nil, nil, nil), down, ratelimit.NewMinute(down, "redeem"))

	// Neither the per-minute cap nor the failure lock can be read: both let
	// the attempts through (a 128-bit code cannot be guessed).
	f.failBad(user, maxFailures+1)
	for range perMinute + 1 {
		_, err := f.svc.Redeem(t.Context(), user, f.seed(1, nil))
		require.NoError(t, err)
	}
	require.True(t, f.balance(user).Equal(decimal.NewFromInt(perMinute+1)))
}

func TestRedeemTenthFailureRestartsWindow(t *testing.T) {
	f := newFixture(t, nil)
	f.svc.windows = nil
	user := testutil.InsertUser(t, f.db, "rd-window@x.test")
	f.failBad(user, maxFailures-1)
	f.mr.FastForward(30 * time.Minute)
	f.failBad(user, 1)

	// The lock runs a whole hour from the tenth failure, not from the first.
	_, err := f.svc.Redeem(t.Context(), user, f.seed(10, nil))
	e := core.AsError(err)
	require.Equal(t, "rate_limited", e.Code)
	require.InDelta(t, lockFor.Seconds(), e.Details["retry_after_seconds"], 1)

	f.mr.FastForward(lockFor + time.Second)
	out, err := f.svc.Redeem(t.Context(), user, f.seed(10, nil))
	require.NoError(t, err)
	require.True(t, out.Amount.Equal(decimal.NewFromInt(10)))
}

func TestRedeemEmptyCodeNotCounted(t *testing.T) {
	f := newFixture(t, nil)
	user := testutil.InsertUser(t, f.db, "rd-empty@x.test")
	for range perMinute + 1 {
		_, err := f.svc.Redeem(t.Context(), user, " - ")
		e := core.AsError(err)
		require.Equal(t, "invalid_argument", e.Code)
		require.Equal(t, []core.FieldError{{Field: "code", Code: "required", Message: "enter a redeem code"}}, e.Details["fields"])
	}
	require.False(t, f.mr.Exists(failKey(user)))
	require.False(t, f.mr.Exists(ratelimit.Key("redeem", user, "rpmb")))
}

func TestRedeemDuplicateCreditRollsBack(t *testing.T) {
	f := newFixture(t, nil)
	user := testutil.InsertUser(t, f.db, "rd-dup@x.test")
	code := f.seed(40, nil)
	// The same credit already holds the code's key, as if its row had been
	// re-created after it was redeemed: the ledger answers Duplicate.
	_, err := f.bill.Apply(t.Context(), core.LedgerChange{UserID: user, Amount: decimal.NewFromInt(40), Credit: true,
		Kind: kindRedeem, RefType: "redeem_code", RefID: "0", IdempotencyKey: "redeem:" + hashCode(normalize(code))})
	require.NoError(t, err)

	_, err = f.svc.Redeem(t.Context(), user, code)
	require.Equal(t, "internal", core.AsError(err).Code)
	require.Equal(t, statusUnused, f.status(code))
	require.True(t, f.balance(user).Equal(decimal.NewFromInt(40)))
	require.False(t, f.mr.Exists(failKey(user)))
}

func TestGenerateUpperBounds(t *testing.T) {
	f := newFixture(t, nil)
	admin := testutil.InsertUser(t, f.db, "rd-bounds@x.test")
	out, err := f.svc.Generate(t.Context(), admin, GenerateRequest{Count: maxCount, Value: maxValue})
	require.NoError(t, err)
	require.Len(t, out.Codes, maxCount)
	var n int64
	require.NoError(t, f.db.Gorm.Model(&model.RedeemCode{}).Where("value = ?", maxValue).Count(&n).Error)
	require.EqualValues(t, maxCount, n)
}
