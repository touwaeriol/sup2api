package billing

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// The price check of updatePrice runs in its transaction and reads the
// settings through it on a cache miss: on a pool of one connection it gets
// the stored threshold instead of waiting for a second connection.
func TestPriceCheckReadsSettingsInItsTransaction(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db := testutil.DB(t)
	_, err := db.SQL.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('billing', '{"big_cost_warning_usd":"1000"}')`)
	require.NoError(t, err)
	one, err := store.OpenSized(ctx, testutil.DSN(db), 1)
	require.NoError(t, err)
	t.Cleanup(one.Close)
	s := New(one, nil, nil, nil, nil)

	// A big cost under the default threshold (10 USD), not under the
	// stored one: no confirmation is asked only if the check read it.
	err = one.Tx(ctx, func(tx *gorm.DB) error {
		_, err := s.check(ctx, tx, "visual", json.RawMessage(`{"tiers":[{"name":"base","p":15,"c":75}]}`), "", false)
		return err
	})
	require.NoError(t, err)
}
