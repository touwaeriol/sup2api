package testutil

import (
	"context"
	"strconv"
	"testing"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// InsertUser inserts a user with email and returns its id.
func InsertUser(tb testing.TB, db *store.DB, email string) int64 {
	tb.Helper()
	return insertID(tb, db, `INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id`, email)
}

// SetOwner makes owner (0: none) the owner of user as the core moves
// users (core.UserTree), bypassing authz's checks: no permission, no audit,
// no authz version. A test that needs those calls the OwnerGuard.
func SetOwner(tb testing.TB, db *store.DB, user, owner int64) {
	tb.Helper()
	ctx := context.Background()
	if err := db.Tx(ctx, func(tx *gorm.DB) error {
		_, err := core.UserTree().Move(store.G(ctx, tx), user, owner)
		return err
	}); err != nil {
		tb.Fatal(err)
	}
}

// InsertGroup inserts a group named name and returns its id.
func InsertGroup(tb testing.TB, db *store.DB, name string) int64 {
	tb.Helper()
	return insertID(tb, db, `INSERT INTO groups (name) VALUES ($1) RETURNING id`, name)
}

// FundUser credits user with a balance no test or benchmark exhausts,
// through the ledger as an admin adjustment.
func FundUser(tb testing.TB, ledger core.Ledger, user int64) {
	tb.Helper()
	if _, err := ledger.Apply(context.Background(), core.LedgerChange{
		UserID: user, Amount: decimal.NewFromInt(1_000_000_000), Credit: true,
		Kind:           core.LedgerKindAdminAdjust,
		IdempotencyKey: "testutil-fund-" + strconv.FormatInt(user, 10),
	}); err != nil {
		tb.Fatal(err)
	}
}

func insertID(tb testing.TB, db *store.DB, sql string, args ...any) int64 {
	tb.Helper()
	var id int64
	if err := db.SQL.QueryRowContext(context.Background(), sql, args...).Scan(&id); err != nil {
		tb.Fatal(err)
	}
	return id
}
