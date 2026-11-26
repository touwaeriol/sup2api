package redeem

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/ratelimit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

type fixture struct {
	t    *testing.T
	db   *store.DB
	bill *billing.Service
	rdb  *redis.Client
	mr   *miniredis.Miniredis
	svc  *Service
}

// newFixture is a real database and ledger, a miniredis for the limits, and
// the service over ledger (nil: the billing service itself).
func newFixture(t *testing.T, wrap func(Ledger) Ledger) *fixture {
	t.Helper()
	f := &fixture{t: t, db: testutil.DB(t), mr: miniredis.RunT(t)}
	f.rdb = redis.NewClient(&redis.Options{Addr: f.mr.Addr()})
	t.Cleanup(func() { _ = f.rdb.Close() })
	f.bill = billing.New(f.db, f.rdb, nil, nil, nil)
	var ledger Ledger = f.bill
	if wrap != nil {
		ledger = wrap(f.bill)
	}
	f.svc = New(f.db, ledger, f.rdb, ratelimit.NewMinute(f.rdb, "redeem"))
	return f
}

// seed inserts an unused code of value and returns it as a user would type it.
func (f *fixture) seed(value int64, expires *time.Time) string {
	f.t.Helper()
	code, err := newCode()
	require.NoError(f.t, err)
	row := model.RedeemCode{CodeHash: hashCode(code), CodeTail: codeTail(code), Value: decimal.NewFromInt(value), ExpiresAt: expires}
	require.NoError(f.t, f.db.Gorm.Create(&row).Error)
	return displayCode(code)
}

func (f *fixture) balance(user int64) decimal.Decimal {
	f.t.Helper()
	var b decimal.Decimal
	require.NoError(f.t, f.db.Gorm.Model(&model.UserBalance{}).Select("COALESCE(MAX(balance), 0)").Where("user_id = ?", user).Scan(&b).Error)
	return b
}

func (f *fixture) status(code string) string {
	f.t.Helper()
	var s string
	require.NoError(f.t, f.db.Gorm.Model(&model.RedeemCode{}).Select("status").Where("code_hash = ?", hashCode(normalize(code))).Scan(&s).Error)
	return s
}

func requireInvalid(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	require.Equal(t, "invalid_redeem_code", core.AsError(err).Code, err.Error())
}

func TestRedeemCredits(t *testing.T) {
	f := newFixture(t, nil)
	user := testutil.InsertUser(t, f.db, "rd-user@x.test")
	code := f.seed(50, nil)

	// Case, spaces and hyphens do not matter.
	out, err := f.svc.Redeem(t.Context(), user, " "+normalize(code)[:8]+" - "+strings.ToLower(normalize(code)[8:]))
	require.NoError(t, err)
	require.True(t, out.Amount.Equal(decimal.NewFromInt(50)))
	require.True(t, out.Balance.Equal(decimal.NewFromInt(50)))
	require.True(t, f.balance(user).Equal(decimal.NewFromInt(50)))
	require.Equal(t, statusUsed, f.status(code))

	var l model.BalanceLedger
	require.NoError(t, f.db.Gorm.Where("user_id = ?", user).Take(&l).Error)
	require.Equal(t, "redeem", l.Kind)
	require.Equal(t, "redeem:"+hashCode(normalize(code)), l.IdempotencyKey)

	// Used is the same answer as unknown.
	_, err = f.svc.Redeem(t.Context(), user, code)
	requireInvalid(t, err)
}

func TestRedeemInvalidCodesShareOneAnswer(t *testing.T) {
	f := newFixture(t, nil)
	user := testutil.InsertUser(t, f.db, "rd-inv@x.test")
	past := time.Now().Add(-time.Minute)
	expired := f.seed(10, &past)
	voided := f.seed(10, nil)
	require.NoError(t, f.svc.Void(t.Context(), user, f.codeID(voided)))
	unknown, err := newCode()
	require.NoError(t, err)

	for _, code := range []string{expired, voided, unknown, "not-a-code"} {
		_, err := f.svc.Redeem(t.Context(), user, code)
		requireInvalid(t, err)
	}
	require.True(t, f.balance(user).IsZero())
	require.Equal(t, statusUnused, f.status(expired))
}

func (f *fixture) codeID(code string) int64 {
	f.t.Helper()
	var id int64
	require.NoError(f.t, f.db.Gorm.Model(&model.RedeemCode{}).Select("id").Where("code_hash = ?", hashCode(normalize(code))).Scan(&id).Error)
	return id
}

func TestRedeemConcurrentOnce(t *testing.T) {
	f := newFixture(t, nil)
	f.svc.windows = nil // 20 users at once; the per-minute cap is not under test
	code := f.seed(25, nil)
	users := make([]int64, 20)
	for i := range users {
		users[i] = testutil.InsertUser(t, f.db, fmt.Sprintf("rd-race-%d@x.test", i))
	}
	var wg sync.WaitGroup
	errs := make([]error, len(users))
	for i, u := range users {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.svc.Redeem(context.Background(), u, code)
		}()
	}
	wg.Wait()
	won := 0
	for _, err := range errs {
		if err == nil {
			won++
			continue
		}
		requireInvalid(t, err)
	}
	require.Equal(t, 1, won)
	var credited int64
	require.NoError(t, f.db.Gorm.Model(&model.BalanceLedger{}).Where("kind = ?", "redeem").Count(&credited).Error)
	require.EqualValues(t, 1, credited)
}

// failingLedger writes the credit and then fails, as a ledger error after
// its statement would: the whole transaction must roll back.
type failingLedger struct{ Ledger }

func (l failingLedger) ApplyTx(ctx context.Context, tx *gorm.DB, ch core.LedgerChange) (*core.LedgerResult, error) {
	if _, err := l.Ledger.ApplyTx(ctx, tx, ch); err != nil {
		return nil, err
	}
	return nil, errors.New("ledger failed")
}

func TestRedeemLedgerFailureKeepsCode(t *testing.T) {
	f := newFixture(t, func(l Ledger) Ledger { return failingLedger{l} })
	user := testutil.InsertUser(t, f.db, "rd-fail@x.test")
	code := f.seed(30, nil)

	_, err := f.svc.Redeem(t.Context(), user, code)
	require.Error(t, err)
	require.Equal(t, "internal", core.AsError(err).Code)
	require.Equal(t, statusUnused, f.status(code))
	require.True(t, f.balance(user).IsZero())
	var n int64
	require.NoError(t, f.db.Gorm.Model(&model.BalanceLedger{}).Where("user_id = ?", user).Count(&n).Error)
	require.Zero(t, n)

	// A ledger error is not a wrong code: it does not count towards the lock.
	require.False(t, f.mr.Exists(failKey(user)))
}
