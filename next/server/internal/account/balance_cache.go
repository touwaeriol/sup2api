package account

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// Redis cache of balance snapshots (CONTRACTS §52.4), parallel to the quota
// cache: PG is the source of truth, every write goes through to Redis, a
// read falls back to PG for missing keys and when Redis is down.

const balanceCacheTTL = 10 * time.Minute

func (s *Service) writeBalanceCache(ctx context.Context, snap *store.BalanceSnapshot) {
	if s.d.Redis == nil {
		return
	}
	data, err := json.Marshal(snap)
	if err != nil {
		slog.WarnContext(ctx, "account: marshal balance cache", "account", snap.AccountID, "err", err)
		return
	}
	if err := s.d.Redis.Set(ctx, balanceCacheKey(snap.AccountID), data, balanceCacheTTL).Err(); err != nil {
		slog.WarnContext(ctx, "account: write balance cache", "account", snap.AccountID, "err", err)
	}
}

func (s *Service) evictBalanceCache(ctx context.Context, ids ...int64) {
	if len(ids) == 0 || s.d.Redis == nil {
		return
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = balanceCacheKey(id)
	}
	if err := s.d.Redis.Del(ctx, keys...).Err(); err != nil {
		slog.WarnContext(ctx, "account: redis DEL balance", "err", err)
	}
}

func balanceCacheKey(accountID int64) string {
	return fmt.Sprintf("account:balance:%d", accountID)
}

// cachedAccountBalances reads balances of multiple accounts with Redis cache.
func (s *Service) cachedAccountBalances(ctx context.Context, ids []int64) (map[int64]*store.BalanceSnapshot, error) {
	if len(ids) == 0 {
		return map[int64]*store.BalanceSnapshot{}, nil
	}

	// Try Redis first
	cached := map[int64]*store.BalanceSnapshot{}
	missing := []int64{}
	redisOK := s.d.Redis != nil

	if redisOK {
		keys := make([]string, len(ids))
		for i, id := range ids {
			keys[i] = balanceCacheKey(id)
		}
		vals, err := s.d.Redis.MGet(ctx, keys...).Result()
		if err != nil {
			slog.WarnContext(ctx, "account: redis MGET balance fallback to PG", "err", err)
			redisOK = false
		} else {
			for i, val := range vals {
				if val == nil {
					missing = append(missing, ids[i])
					continue
				}
				str, ok := val.(string)
				if !ok {
					missing = append(missing, ids[i])
					continue
				}
				var snap store.BalanceSnapshot
				if err := json.Unmarshal([]byte(str), &snap); err != nil {
					slog.WarnContext(ctx, "account: corrupt balance cache", "account", ids[i])
					missing = append(missing, ids[i])
					continue
				}
				cached[ids[i]] = &snap
			}
		}
	}

	// Fallback to PG for missing or all ids
	needPG := missing
	if !redisOK {
		needPG = ids
	}

	if len(needPG) == 0 {
		return cached, nil
	}

	pgSnaps, err := s.d.DB.AccountBalances(ctx, needPG)
	if err != nil {
		return nil, err
	}

	// Merge PG results and write back to Redis synchronously
	for id, snap := range pgSnaps {
		cached[id] = snap
		if redisOK {
			s.writeBalanceCache(ctx, snap)
		}
	}

	return cached, nil
}
