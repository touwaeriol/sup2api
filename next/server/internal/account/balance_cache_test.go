package account

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

func testAccountForBalanceCache(t *testing.T, db *store.DB) int64 {
	t.Helper()
	var id int64
	err := db.SQL.QueryRowContext(context.Background(), `INSERT INTO accounts (name, plugin_key, type, credentials_enc)
		VALUES ($1, 'codex', 'codex_oauth', $2) RETURNING id`,
		"balance-cache-test-"+time.Now().Format("150405.000000"), []byte("x")).Scan(&id)
	require.NoError(t, err)
	return id
}

func TestBalanceCache(t *testing.T) {
	ctx := context.Background()
	db := testutil.DB(t)
	mr := miniredis.RunT(t)
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	s := &Service{d: Deps{DB: db, Redis: rdb}}

	id := testAccountForBalanceCache(t, db)

	now := time.Now().UTC()
	snap := &store.BalanceSnapshot{
		AccountID:    id,
		AmountMicros: 1234500,
		Currency:     "USD",
		UpdatedAt:    &now,
	}
	err := db.UpsertBalance(ctx, snap)
	require.NoError(t, err)

	// Read should hit Redis
	cached, err := s.cachedAccountBalances(ctx, []int64{id})
	require.NoError(t, err)
	require.Len(t, cached, 1)
	require.Equal(t, snap.AmountMicros, cached[id].AmountMicros)
	require.Equal(t, snap.Currency, cached[id].Currency)

	// Redis should have the key
	val, err := mr.Get(fmt.Sprintf("account:balance:%d", id))
	require.NoError(t, err)
	require.NotEmpty(t, val)
}

// Every snapshot is cached as stored: a first failed read (no updated_at)
// and an unlimited balance alike, so the list does not read PG for them.
func TestBalanceCacheKeepsErrorsAndUnlimited(t *testing.T) {
	ctx := context.Background()
	db := testutil.DB(t)
	mr := miniredis.RunT(t)
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	s := &Service{d: Deps{DB: db, Redis: rdb}}

	failed, unlimited := testAccountForBalanceCache(t, db), testAccountForBalanceCache(t, db)
	now := time.Now().UTC()
	require.NoError(t, db.UpsertBalance(ctx, &store.BalanceSnapshot{AccountID: failed, Error: "transient: 503", ErrorAt: &now}))
	require.NoError(t, db.UpsertBalance(ctx, &store.BalanceSnapshot{AccountID: unlimited, Unlimited: true, UpdatedAt: &now}))
	_, err := s.cachedAccountBalances(ctx, []int64{failed, unlimited})
	require.NoError(t, err)
	require.True(t, mr.Exists(balanceCacheKey(failed)))

	// Served from Redis now: PG rows gone, the cached ones still read.
	require.NoError(t, store.DeleteBalance(ctx, db.Gorm, failed))
	require.NoError(t, store.DeleteBalance(ctx, db.Gorm, unlimited))
	got, err := s.cachedAccountBalances(ctx, []int64{failed, unlimited})
	require.NoError(t, err)
	require.Equal(t, "transient: 503", got[failed].Error)
	require.Nil(t, got[failed].UpdatedAt)
	require.True(t, got[unlimited].Unlimited)

	var raw map[string]any
	val, _ := mr.Get(balanceCacheKey(unlimited))
	require.NoError(t, json.Unmarshal([]byte(val), &raw))
	require.Equal(t, true, raw["unlimited"])
}

func TestBalanceCacheEvictOnResetAndReauth(t *testing.T) {
	ctx := context.Background()
	db := testutil.DB(t)
	mr := miniredis.RunT(t)
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	s := &Service{d: Deps{DB: db, Redis: rdb}}

	id := testAccountForBalanceCache(t, db)

	now := time.Now().UTC()
	snap := &store.BalanceSnapshot{
		AccountID:    id,
		AmountMicros: 1500000,
		Currency:     "USD",
		UpdatedAt:    &now,
	}
	err := db.UpsertBalance(ctx, snap)
	require.NoError(t, err)

	// Populate cache
	_, err = s.cachedAccountBalances(ctx, []int64{id})
	require.NoError(t, err)
	require.True(t, mr.Exists(fmt.Sprintf("account:balance:%d", id)))

	// Evict should remove from Redis
	s.evictBalanceCache(ctx, id)
	require.False(t, mr.Exists(fmt.Sprintf("account:balance:%d", id)))
}

func TestBalanceCacheCorruptEntry(t *testing.T) {
	ctx := context.Background()
	db := testutil.DB(t)
	mr := miniredis.RunT(t)
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	s := &Service{d: Deps{DB: db, Redis: rdb}}

	id := testAccountForBalanceCache(t, db)

	now := time.Now().UTC()
	snap := &store.BalanceSnapshot{
		AccountID:    id,
		AmountMicros: 3000000,
		Currency:     "USD",
		UpdatedAt:    &now,
	}
	err := db.UpsertBalance(ctx, snap)
	require.NoError(t, err)

	// Put corrupt data in Redis
	mr.Set(fmt.Sprintf("account:balance:%d", id), "{invalid json")

	// Should fall back to PG
	result, err := s.cachedAccountBalances(ctx, []int64{id})
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, snap.AmountMicros, result[id].AmountMicros)
}
