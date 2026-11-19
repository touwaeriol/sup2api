package store_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

func TestAccountBalanceRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}
	ctx := t.Context()
	db := testutil.DB(t)
	id := createTestAccount(t, db)

	b, err := db.AccountBalance(ctx, id)
	require.NoError(t, err)
	require.Nil(t, b, "no snapshot yet")

	// A first read that fails: no amount, no updated_at.
	at := time.Now().UTC().Truncate(time.Microsecond)
	require.NoError(t, db.UpsertBalance(ctx, &store.BalanceSnapshot{AccountID: id, Error: "transient: 503", ErrorAt: &at}))
	b, err = db.AccountBalance(ctx, id)
	require.NoError(t, err)
	require.Nil(t, b.UpdatedAt, "a NULL timestamp reads as nil")
	require.Equal(t, "transient: 503", b.Error)
	require.Equal(t, "", b.Currency)

	require.NoError(t, db.UpsertBalance(ctx, &store.BalanceSnapshot{AccountID: id, AmountMicros: 5, Currency: "USD", UpdatedAt: &at}))
	b, err = db.AccountBalance(ctx, id)
	require.NoError(t, err)
	require.Equal(t, int64(5), b.AmountMicros)
	require.True(t, b.UpdatedAt.Equal(at))
	require.Nil(t, b.ErrorAt)
	require.Equal(t, "", b.Error)

	require.NoError(t, db.UpsertBalance(ctx, &store.BalanceSnapshot{AccountID: id, Unlimited: true, UpdatedAt: &at, Error: "boom", ErrorAt: &at}))
	all, err := db.AccountBalances(ctx, []int64{id, id + 1})
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.True(t, all[id].Unlimited)
	require.Equal(t, "boom", all[id].Error)
	require.True(t, all[id].ErrorAt.Equal(at))

	require.NoError(t, store.DeleteBalance(ctx, db.Gorm, id))
	b, err = db.AccountBalance(ctx, id)
	require.NoError(t, err)
	require.Nil(t, b)
}
