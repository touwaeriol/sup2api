package account

import (
	"context"
	"errors"
	"log/slog"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// sweepDueBalances is core.account.balance_sweep: for every account type
// that reads balances, it reads one page of the due balances, the oldest
// first; the rest are still due on the next run. It returns how many were
// read.
func (s *Service) sweepDueBalances(ctx context.Context) (int, error) {
	g := s.gen()
	if g == nil {
		return 0, nil
	}
	n := 0
	for _, bt := range g.AccountTypes() {
		if _, ok := s.balanceType(bt.Plugin.Key, bt.Type.ID); !ok {
			continue
		}
		var ids []int64
		if err := dueBalances(store.G(ctx, s.d.DB.Gorm), bt, quotaSweepPage).Pluck("a.id", &ids).Error; err != nil {
			return n, err
		}
		for _, id := range ids {
			if ctx.Err() != nil {
				return n, ctx.Err()
			}
			a, err := s.loadRow(ctx, s.d.DB.Gorm, id, core.AllScope, false)
			if err == nil {
				_, err = s.readBalance(ctx, bt, a)
			}
			if err != nil && !errors.Is(err, errBalanceUnimplemented) {
				slog.WarnContext(ctx, "account: read balance", "account", id, "err", err)
			}
			n++
		}
	}
	return n, nil
}

// dueBalances selects up to limit live accounts of type bt whose balance is
// due (balanceDue, on the database clock): the schedulable ones and those
// never read, a disabled account being read only on an administrator's
// refresh. The longest unread first.
func dueBalances(db *gorm.DB, bt core.AccountTypeBinding, limit int) *gorm.DB {
	return db.Table("accounts a").Joins("LEFT JOIN account_balances b ON b.account_id = a.id").
		Where("a.deleted_at IS NULL AND a.plugin_key = ? AND a.type = ? AND (a.schedulable OR b.account_id IS NULL)", bt.Plugin.Key, bt.Type.ID).
		Where("b.account_id IS NULL OR GREATEST(b.updated_at, b.error_at) <= now() - make_interval(secs => ?::float8)", balanceInterval(bt).Seconds()).
		Order("GREATEST(b.updated_at, b.error_at) NULLS FIRST, a.id").Limit(limit)
}
