package billing

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

// Ledger kinds.
const (
	KindUsage        = "usage"
	KindAdminAdjust  = "admin_adjust"
	KindPluginCredit = "plugin_credit"
	KindPluginDebit  = "plugin_debit"
	KindRefund       = "refund"
	// Online top-up, its promo bonus and redeem codes (CONTRACTS §138).
	KindTopup      = "topup"
	KindPromoBonus = "promo_bonus"
	KindRedeem     = "redeem"
)

var validKinds = map[string]bool{KindUsage: true, KindAdminAdjust: true, KindPluginCredit: true, KindPluginDebit: true, KindRefund: true,
	KindTopup: true, KindPromoBonus: true, KindRedeem: true}

const balanceCacheTTL = 10 * time.Minute

func balanceKey(userID int64) string { return "balance:" + x.Itoa64(userID) }

// setBalanceScript stores "<ledger_id>:<balance>" unless the cache already
// holds a newer ledger id, so concurrent writers never regress the value.
var setBalanceScript = redis.NewScript(`
local cur = redis.call('GET', KEYS[1])
if cur then
  local id = tonumber(string.match(cur, '^(%d+):'))
  if id and id > tonumber(ARGV[1]) then return 0 end
end
redis.call('SET', KEYS[1], ARGV[1] .. ':' .. ARGV[2], 'EX', tonumber(ARGV[3]))
return 1
`)

func validateChange(ch *core.LedgerChange) error {
	var fields []core.FieldError
	if ch.UserID <= 0 {
		fields = append(fields, core.FieldError{Field: "user_id", Code: "required", Message: "user id is required"})
	}
	if ch.Amount.Sign() <= 0 {
		fields = append(fields, core.FieldError{Field: "amount", Code: "invalid", Message: "amount must be positive"})
	} else if !ch.Amount.Equal(ch.Amount.Round(8)) {
		fields = append(fields, core.FieldError{Field: "amount", Code: "invalid", Message: "at most 8 decimal places"})
	}
	if !validKinds[ch.Kind] {
		fields = append(fields, core.FieldError{Field: "kind", Code: "invalid", Message: "unknown ledger kind"})
	}
	if ch.IdempotencyKey == "" || len(ch.IdempotencyKey) > 150 {
		fields = append(fields, core.FieldError{Field: "idempotency_key", Code: "required", Message: "1-150 characters"})
	}
	if len(fields) > 0 {
		return core.InvalidFields(fields...)
	}
	return nil
}

// Apply implements core.Ledger: one transaction for balance, ledger row and
// balance.changed event; then the Redis cache is refreshed.
func (s *Service) Apply(ctx context.Context, ch core.LedgerChange) (*core.LedgerResult, error) {
	var res *core.LedgerResult
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		var err error
		res, err = s.ApplyTx(ctx, tx, ch)
		return err
	})
	if err != nil {
		return nil, err
	}
	if !res.Duplicate {
		s.CacheBalance(ctx, ch.UserID, res.LedgerID, res.BalanceAfter)
	}
	return res, nil
}

// ApplyTx applies a change inside the caller's transaction (used by usage
// settlement to update the usage row atomically). The caller must call
// CacheBalance after committing when the result is not a duplicate.
//
// One statement (applySQL: find the key or lock the row, write the ledger
// row and the balance) and one event: one round trip under the user's
// balance lock before COMMIT instead of six (CONTRACTS §63.7). For a user
// without a balance row the statement finds no row and writes nothing;
// ensureBalanceSQL then creates the row and the statement runs again
// (runLedger).
func (s *Service) ApplyTx(ctx context.Context, tx *gorm.DB, ch core.LedgerChange) (*core.LedgerResult, error) {
	if err := store.RequireTx(tx); err != nil {
		return nil, err
	}
	if err := validateChange(&ch); err != nil {
		return nil, err
	}
	ch.Amount = ch.Amount.Round(8)
	a, err := applyChange(store.G(ctx, tx), ch)
	if err != nil {
		return nil, err
	}
	res, err := a.resolve(ctx, tx, ch)
	if err != nil || res.Duplicate {
		return res, err
	}
	if s.events != nil {
		delta := ch.Amount
		if !ch.Credit {
			delta = delta.Neg()
		}
		if err := s.events.Emit(ctx, tx, balanceChanged(ch.UserID, res, delta, ch.Kind)); err != nil {
			return nil, fmt.Errorf("emit balance.changed: %w", err)
		}
	}
	return res, nil
}

func findLedger(ctx context.Context, q *gorm.DB, key string) (*core.LedgerResult, error) {
	var row model.BalanceLedger
	err := store.G(ctx, q).Model(&model.BalanceLedger{}).Select("id", "balance_after", "user_id", "delta", "kind").
		Where("idempotency_key = ?", key).Take(&row).Error
	if store.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &core.LedgerResult{Duplicate: true, LedgerID: row.ID, BalanceAfter: row.BalanceAfter,
		UserID: row.UserID, Delta: row.Delta, Kind: row.Kind}, nil
}

// CacheBalance stores a balance observed at ledgerID in Redis (best effort;
// older observations never overwrite newer ones).
func (s *Service) CacheBalance(ctx context.Context, userID, ledgerID int64, balance decimal.Decimal) {
	if s.rdb == nil {
		return
	}
	err := setBalanceScript.Run(context.WithoutCancel(ctx), s.rdb, []string{balanceKey(userID)},
		ledgerID, balance.String(), int(balanceCacheTTL.Seconds())).Err()
	if err != nil {
		slog.WarnContext(ctx, "billing: cache balance", "user_id", userID, "err", err)
		// A stale entry is worse than none.
		_ = s.rdb.Del(context.WithoutCancel(ctx), balanceKey(userID)).Err()
	}
}

// balanceSQL reads a user's balance and the ledger id it was observed at
// (the user id twice).
func balanceRead(db *gorm.DB, userID int64) *gorm.DB {
	sub := db.Session(&gorm.Session{NewDB: true})
	return store.OneRow(db).Select("COALESCE((?), 0), COALESCE((?), 0)",
		sub.Table("user_balances").Select("balance").Where("user_id = ?", userID),
		sub.Table("balance_ledger").Select("max(id)").Where("user_id = ?", userID))
}

// Balance returns a user's balance, served from Redis when cached.
func (s *Service) Balance(ctx context.Context, userID int64) (decimal.Decimal, error) {
	if s.rdb != nil {
		v, err := s.rdb.Get(ctx, balanceKey(userID)).Result()
		if err == nil {
			if _, bal, ok := strings.Cut(v, ":"); ok {
				if d, err := decimal.NewFromString(bal); err == nil {
					return d, nil
				}
			}
		} else if err != redis.Nil {
			slog.WarnContext(ctx, "billing: read balance cache", "user_id", userID, "err", err)
		}
	}
	var bal decimal.Decimal
	var lastID int64
	// Behind the Redis cache: only a miss reads PostgreSQL.
	err := balanceRead(store.G(ctx, s.db.Gorm), userID).Row().Scan(&bal, &lastID)
	if err != nil {
		return decimal.Zero, err
	}
	s.CacheBalance(ctx, userID, lastID, bal)
	return bal, nil
}

// CheckBalance implements core.BalanceGate: the balance must exceed
// settings.min_balance.
func (s *Service) CheckBalance(ctx context.Context, userID int64) error {
	bal, err := s.Balance(ctx, userID)
	if err != nil {
		return err
	}
	st, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	if !meetsMin(bal, st.MinBalance) {
		return core.ErrInsufficientBalance.WithDetails(map[string]any{"balance": bal.String(), "min_balance": st.MinBalance.String()})
	}
	return nil
}

// meetsMin checks if balance meets the minimum threshold: balance > min.
func meetsMin(balance, min decimal.Decimal) bool {
	return balance.GreaterThan(min)
}
