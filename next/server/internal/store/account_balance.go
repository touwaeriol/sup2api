package store

import (
	"context"
	"database/sql"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Account balance snapshots (table account_balances, CONTRACTS §52).

// BalanceSnapshot is one account_balances row. The JSON form is the one
// the account service caches in Redis (CONTRACTS §52.4).
type BalanceSnapshot struct {
	AccountID    int64 `json:"account_id"`
	AmountMicros int64 `json:"amount_micros"`
	// Currency is the unit the upstream gave with the amount, as it gave
	// it ('' for none): not necessarily a currency code (CONTRACTS §52).
	Currency string `json:"currency"`
	// Unlimited: the upstream reports no limit; amount and currency unset.
	Unlimited bool `json:"unlimited"`
	// UpdatedAt is the last successful read, nil before the first.
	UpdatedAt *time.Time `json:"updated_at"`
	// Error is the last failed read, cleared by a successful one.
	Error   string     `json:"error"`
	ErrorAt *time.Time `json:"error_at"`
}

// balanceColumns are the account_balances columns scanBalance reads, in
// order.
const balanceColumns = "account_id, amount_micros, currency, unlimited, updated_at, error, error_at"

// balances selects balanceColumns of account_balances.
func balances(db *gorm.DB) *gorm.DB { return db.Table(balanceTable).Select(balanceColumns) }

const balanceTable = "account_balances"

func scanBalance(r RowScanner) (*BalanceSnapshot, error) {
	var b BalanceSnapshot
	if err := r.Scan(&b.AccountID, &b.AmountMicros, &b.Currency, &b.Unlimited, &b.UpdatedAt, &b.Error, &b.ErrorAt); err != nil {
		return nil, err
	}
	return &b, nil
}

// AccountBalance returns the snapshot of one account, nil when it has none.
func (d *DB) AccountBalance(ctx context.Context, accountID int64) (*BalanceSnapshot, error) {
	b, err := scanBalance(balances(G(ctx, d.Gorm)).Where("account_id = ?", accountID).Row())
	if IsNoRows(err) {
		return nil, nil
	}
	return b, err
}

// AccountBalances returns the snapshots of the given accounts in one query;
// accounts without one are absent from the map.
func (d *DB) AccountBalances(ctx context.Context, accountIDs []int64) (map[int64]*BalanceSnapshot, error) {
	out := map[int64]*BalanceSnapshot{}
	if len(accountIDs) == 0 {
		return out, nil
	}
	err := EachRow(balances(G(ctx, d.Gorm)).Where("account_id = ANY(?)", Int64s(accountIDs)), func(rows *sql.Rows) error {
		b, err := scanBalance(rows)
		if err == nil {
			out[b.AccountID] = b
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// UpsertBalance writes one balance snapshot.
func (d *DB) UpsertBalance(ctx context.Context, b *BalanceSnapshot) error {
	return upsertBalance(G(ctx, d.Gorm), b).Error
}

func upsertBalance(db *gorm.DB, b *BalanceSnapshot) *gorm.DB {
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "account_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"amount_micros", "currency", "unlimited", "updated_at", "error", "error_at"}),
	}).Create(&balanceRow{AccountID: b.AccountID, AmountMicros: b.AmountMicros, Currency: b.Currency, Unlimited: b.Unlimited,
		UpdatedAt: b.UpdatedAt, Error: b.Error, ErrorAt: b.ErrorAt})
}

// balanceRow is the account_balances row of a snapshot, every column
// written as given. updated_at is a pointer: a time.Time updated_at would
// be stamped (and read back) by store when zero.
type balanceRow struct {
	AccountID    int64      `gorm:"column:account_id"`
	AmountMicros int64      `gorm:"column:amount_micros"`
	Currency     string     `gorm:"column:currency"`
	Unlimited    bool       `gorm:"column:unlimited"`
	UpdatedAt    *time.Time `gorm:"column:updated_at;autoUpdateTime:false"`
	Error        string     `gorm:"column:error"`
	ErrorAt      *time.Time `gorm:"column:error_at"`
}

func (balanceRow) TableName() string { return balanceTable }

// DeleteBalance removes the balance snapshot of one account in db (a
// transaction or the pool): the next read asks the upstream again.
func DeleteBalance(ctx context.Context, db *gorm.DB, accountID int64) error {
	return G(ctx, db).Table(balanceTable).Where("account_id = ?", accountID).Delete(nil).Error
}
