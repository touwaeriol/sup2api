package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// The account snapshot statements near the gateway's request path, each
// GORM form (gorm) measured next to the same statement on raw database/sql (sql)
// against a real PostgreSQL (TEST_DATABASE_URL), as recorded in
// internal/store/gormtest/BASELINE.md:
//
//	go test -run '^$' -bench Hot -benchmem -benchtime 1000x -count 5 ./internal/store/
//
// ApplyAccountQuotaObservation runs for every upstream answer that carries
// a quota observation, and ReadAccountQuotas after each one that applied
// (the quota mirror reloads the account). AccountBalances only serves the
// console's account list, behind Redis.

// hotFixture is one account of an enabled plugin with a quota snapshot and
// a balance snapshot.
func hotFixture(b *testing.B) (*store.DB, *store.QuotaSnapshot) {
	ctx := context.Background()
	db := testutil.DB(b)
	if _, err := db.SQL.ExecContext(ctx, `INSERT INTO plugins(key,name,status,active_version) VALUES('test','{"en":"Test"}','enabled','v1')`); err != nil {
		b.Fatal(err)
	}
	var id int64
	if err := db.SQL.QueryRowContext(ctx, `INSERT INTO accounts (name, plugin_key, type, credentials_enc)
		VALUES ('bench', 'test', 'test_type', '\x00') RETURNING id`).Scan(&id); err != nil {
		b.Fatal(err)
	}
	if _, err := store.EnsureAccountQuota(ctx, db.Gorm, id, "v1", true); err != nil {
		b.Fatal(err)
	}
	reset := time.Now().Add(time.Hour)
	if err := db.SaveAccountQuota(ctx, id, store.QuotaPassive, map[string]store.QuotaWindow{
		"5h": {Utilization: 42, ResetsAt: &reset, Status: "allowed"}, "7d": {Utilization: 10},
	}); err != nil {
		b.Fatal(err)
	}
	now := time.Now()
	if err := db.UpsertBalance(ctx, &store.BalanceSnapshot{AccountID: id, AmountMicros: 1_000_000, Currency: "USD",
		UpdatedAt: &now}); err != nil {
		b.Fatal(err)
	}
	q, err := db.AccountQuota(ctx, id)
	if err != nil {
		b.Fatal(err)
	}
	return db, q
}

// benchPaths runs the raw database/sql and the GORM form of one statement.
func benchPaths(b *testing.B, sqlPath, gormPath func() error) {
	for _, p := range []struct {
		name string
		run  func() error
	}{{"sql", sqlPath}, {"gorm", gormPath}} {
		b.Run(p.name, func(b *testing.B) {
			if err := p.run(); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if err := p.run(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkHotQuotaObservation(b *testing.B) {
	db, q := hotFixture(b)
	ctx := context.Background()
	windows := map[string]store.QuotaWindow{"5h": {Utilization: 50, Status: "allowed"}}
	benchPaths(b, func() error {
		raw, err := json.Marshal(windows)
		if err != nil {
			return err
		}
		var next *time.Time
		_, err = db.SQL.ExecContext(ctx, `UPDATE account_quota_snapshots q SET windows=q.windows||$6::jsonb,
		source='passive',last_passive_at=clock_timestamp(),revision=q.revision+1,
		next_check_at=CASE WHEN q.auto_refresh AND $7::timestamptz IS NOT NULL THEN LEAST(q.next_check_at,$7::timestamptz) ELSE q.next_check_at END
		WHERE q.account_id=$1 AND q.generation=$2 AND EXISTS(
			SELECT 1 FROM accounts a JOIN plugins p ON p.key=a.plugin_key
			WHERE a.id=q.account_id AND a.plugin_key=$3 AND a.type=$4 AND a.deleted_at IS NULL
			AND p.status IN ('enabled','upgrading') AND p.active_version=$5)`, q.AccountID, q.Generation, "test", "test_type", "v1", string(raw), next)
		return err
	}, func() error {
		_, err := db.ApplyAccountQuotaObservation(ctx, q.AccountID, q.Generation, "test", "test_type", "v1", windows, nil)
		return err
	})
}

func BenchmarkHotReadAccountQuotas(b *testing.B) {
	db, q := hotFixture(b)
	ctx := context.Background()
	ids := []int64{q.AccountID}
	benchPaths(b, func() error {
		rows, err := db.SQL.QueryContext(ctx, `SELECT NULL::bigint, statement_timestamp(), q.account_id, q.windows, COALESCE(q.source, ''), COALESCE(q.error, ''),
		q.updated_at, q.last_passive_at, q.last_active_at, COALESCE(q.revision,0), q.next_check_at, COALESCE(q.refresh_status,'pending'), COALESCE(q.generation,0)
		FROM (SELECT statement_timestamp()) t LEFT JOIN account_quota_snapshots q ON q.account_id = ANY($1)`, ids)
		if err != nil {
			return err
		}
		defer rows.Close()
		read := store.NewQuotaRead(false)
		for rows.Next() {
			if err := store.AddQuotaRow(read, rows); err != nil {
				return err
			}
		}
		return rows.Err()
	}, func() error {
		_, err := db.ReadAccountQuotas(ctx, ids)
		return err
	})
}

func BenchmarkHotAccountBalances(b *testing.B) {
	db, q := hotFixture(b)
	ctx := context.Background()
	ids := []int64{q.AccountID}
	benchPaths(b, func() error {
		rows, err := db.SQL.QueryContext(ctx, `SELECT account_id, amount_micros, currency, unlimited, updated_at, error, error_at
	FROM account_balances WHERE account_id = ANY($1)`, ids)
		if err != nil {
			return err
		}
		defer rows.Close()
		out := map[int64]*store.BalanceSnapshot{}
		for rows.Next() {
			var b store.BalanceSnapshot
			if err := rows.Scan(&b.AccountID, &b.AmountMicros, &b.Currency, &b.Unlimited, &b.UpdatedAt, &b.Error, &b.ErrorAt); err != nil {
				return err
			}
			out[b.AccountID] = &b
		}
		return rows.Err()
	}, func() error {
		_, err := db.AccountBalances(ctx, ids)
		return err
	})
}
