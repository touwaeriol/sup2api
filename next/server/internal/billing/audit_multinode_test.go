package billing

import (
	"context"
	"testing"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

func auditBillingWaitLocks(t *testing.T, ctx context.Context, db *store.DB, want int) {
	t.Helper()
	for {
		var n int
		if err := db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n >= want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("waiting for %d locks: %v", want, ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestAuditConcurrentSettingsPatchesKeepBothFields(t *testing.T) {
	for _, existing := range []bool{true, false} {
		t.Run(map[bool]string{true: "existing_row", false: "first_insert"}[existing], func(t *testing.T) {
			e := newEnv(t)
			background := context.Background()
			admin := e.user("settings-admin@example.com")
			if existing {
				e.exec(`INSERT INTO settings(key,value) VALUES('billing','{"min_balance":"0","big_cost_warning_usd":"1"}')`)
			} else {
				e.exec(`CREATE FUNCTION audit_setting_insert_barrier() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(78190002); RETURN NEW; END $$;
			CREATE TRIGGER audit_setting_insert_barrier BEFORE INSERT ON settings FOR EACH ROW EXECUTE FUNCTION audit_setting_insert_barrier()`)
			}
			ctx, cancel := context.WithTimeout(background, 60*time.Second)
			defer cancel()
			barrier, err := testutil.Begin(ctx, e.db)
			if err != nil {
				t.Fatal(err)
			}
			defer barrier.Rollback()
			if existing {
				var key string
				err = store.SQLTx(barrier).QueryRowContext(ctx, `SELECT key FROM settings WHERE key='billing' FOR UPDATE`).Scan(&key)
			} else {
				_, err = store.SQLTx(barrier).ExecContext(ctx, `SELECT pg_advisory_xact_lock(78190002)`)
			}
			if err != nil {
				t.Fatal(err)
			}
			type result struct {
				code int
				body map[string]any
			}
			done := make(chan result, 2)
			put := func(patch map[string]any) {
				code, body := e.call(admin, "PUT", "/settings/billing", patch)
				done <- result{code, body}
			}
			go put(map[string]any{"min_balance": "1.5"})
			auditBillingWaitLocks(t, ctx, e.db, 1)
			go put(map[string]any{"big_cost_warning_usd": "2"})
			auditBillingWaitLocks(t, ctx, e.db, 2)
			if err := barrier.Commit().Error; err != nil {
				t.Fatal(err)
			}
			for range 2 {
				r := <-done
				if r.code != 200 {
					t.Fatalf("update status=%d body=%v", r.code, r.body)
				}
			}
			var min, warning string
			if err := e.db.SQL.QueryRowContext(ctx, `SELECT value->>'min_balance',value->>'big_cost_warning_usd' FROM settings WHERE key='billing'`).Scan(&min, &warning); err != nil {
				t.Fatal(err)
			}
			if min != "1.5" || warning != "2" {
				t.Fatalf("partial updates lost data: min=%s warning=%s", min, warning)
			}
		})
	}
}

// The view calls a blocking function after selecting the old row value. This
// fixes the race at old query -> invalidation -> old reply, rather than merely
// testing the mutex or incrementing the implementation's epoch by hand.
func TestAuditInvalidatedSettingsCannotBeRefilledByOldQuery(t *testing.T) {
	e := newEnv(t)
	background := context.Background()
	e.exec(`INSERT INTO settings(key,value) VALUES('billing','{"min_balance":"1"}');
	ALTER TABLE settings RENAME TO audit_settings_source;
	CREATE FUNCTION audit_setting_read_barrier(jsonb) RETURNS jsonb LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(78190003); RETURN $1; END $$;
	CREATE VIEW settings AS SELECT key,audit_setting_read_barrier(value) AS value,updated_by,updated_at FROM audit_settings_source`)
	ctx, cancel := context.WithTimeout(background, 60*time.Second)
	defer cancel()
	barrier, err := testutil.Begin(ctx, e.db)
	if err != nil {
		t.Fatal(err)
	}
	defer barrier.Rollback()
	if _, err := store.SQLTx(barrier).ExecContext(ctx, `SELECT pg_advisory_xact_lock(78190003)`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := e.svc.Settings(ctx); done <- err }()
	auditBillingWaitLocks(t, ctx, e.db, 1)
	e.exec(`UPDATE audit_settings_source SET value='{"min_balance":"2"}' WHERE key='billing'`)
	e.svc.invalidate()
	if err := barrier.Commit().Error; err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	current, err := e.svc.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current.MinBalance.String() != "2" {
		t.Fatalf("stale query refilled invalidated cache: %+v", current)
	}
}
