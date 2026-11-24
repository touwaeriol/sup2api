package migrations_test

import (
	"context"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/migrations"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

func TestAPIKeyIPAllowlistMigrationPreservesKeys(t *testing.T) {
	db := testutil.DB(t)
	ctx := t.Context()
	_, err := db.SQL.ExecContext(ctx, `ALTER TABLE api_keys DROP COLUMN allowed_ips;
		DROP INDEX usage_logs_client_ip_created_idx, usage_logs_api_key_created_idx;
		DELETE FROM schema_migrations WHERE id='0052_apikey_ip_allowlist.sql';
		INSERT INTO users(email,password_hash) VALUES ('legacy-ip@example.com','test');
		INSERT INTO groups(name) VALUES ('legacy-ip');
		INSERT INTO api_keys(user_id,group_id,name,key_prefix,key_hash,status)
		SELECT u.id,g.id,'legacy','masked','test-hash','active' FROM users u,groups g WHERE u.email='legacy-ip@example.com' AND g.name='legacy-ip'`)
	require.NoError(t, err)
	var before, after string
	require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT jsonb_agg(to_jsonb(k) ORDER BY id)::text FROM api_keys k`).Scan(&before))
	for range 2 {
		_, err := store.Migrate(ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
		require.NoError(t, err)
		require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT jsonb_agg(to_jsonb(k)-'allowed_ips' ORDER BY id)::text FROM api_keys k`).Scan(&after))
		require.Equal(t, before, after)
		var ips []string
		require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT allowed_ips FROM api_keys WHERE name='legacy'`).Scan(&ips))
		require.Empty(t, ips)
	}
}

func TestAutomaticStateMigrationPreservesLegacyAccounts(t *testing.T) {
	db := testutil.DB(t)
	ctx := t.Context()
	_, err := db.SQL.ExecContext(ctx, `ALTER TABLE accounts DROP COLUMN auto_disabled_until,
		DROP COLUMN last_test_status,DROP COLUMN last_test_error_code,DROP COLUMN last_test_error_source;
		DELETE FROM schema_migrations WHERE id IN ('0049_account_automatic_state.sql','0050_account_test_diagnostics.sql');
		INSERT INTO accounts(name,plugin_key,type,credentials_enc,status,status_reason,schedulable) VALUES
		('manual','fixture','key','','disabled','disabled by administrator',false),
		('legacy','fixture','key','','disabled','upstream rejected credentials (401)',true),
		('ready','fixture','key','','active','',true)`)
	require.NoError(t, err)
	var before, after string
	require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT jsonb_agg(to_jsonb(a) ORDER BY id)::text FROM accounts a`).Scan(&before))
	for i := range 2 {
		done, err := store.Migrate(ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
		require.NoError(t, err)
		if i == 0 {
			require.Equal(t, []string{"0049_account_automatic_state.sql", "0050_account_test_diagnostics.sql"}, done)
		} else {
			require.Empty(t, done)
		}
		require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT jsonb_agg(to_jsonb(a)-'auto_disabled_until'-'last_test_status'-'last_test_error_code'-'last_test_error_source' ORDER BY id)::text FROM accounts a`).Scan(&after))
		require.Equal(t, before, after)
		var indexExists, noDeadlines bool
		require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE tablename='accounts' AND indexname='accounts_auto_disabled_due_idx'),
			NOT EXISTS(SELECT 1 FROM accounts WHERE auto_disabled_until IS NOT NULL OR last_test_status IS NOT NULL OR last_test_error_code IS NOT NULL OR last_test_error_source IS NOT NULL)`).Scan(&indexExists, &noDeadlines))
		require.True(t, indexExists)
		require.True(t, noDeadlines)
	}
}

func TestLegacyProxyRealIPReplayPreservesDataAndChecksum(t *testing.T) {
	const id = "0046_proxy_real_ip.sql"
	const checksum = "1b0719bb3a07caa74ca6ad5d92e8c605e9c4f0ebea3097b96a1226ef99c4897f"
	for _, missing := range []string{"none", "real_ip", "real_ip_updated_at", "both"} {
		t.Run(missing, func(t *testing.T) {
			db := replayDB(t)
			ctx := context.Background()
			ip := "198.51.100.46"
			stamp := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
			var proxyID int64
			err := db.SQL.QueryRowContext(ctx, `INSERT INTO proxies (name, protocol, host, port, real_ip, real_ip_updated_at)
				VALUES ('replay-probe','http','proxy.example',8080,$1,$2) RETURNING id`, ip, stamp).Scan(&proxyID)
			require.NoError(t, err)
			if missing == "real_ip" || missing == "both" {
				_, err = db.SQL.ExecContext(ctx, `ALTER TABLE proxies DROP COLUMN real_ip`)
				require.NoError(t, err)
			}
			if missing == "real_ip_updated_at" || missing == "both" {
				_, err = db.SQL.ExecContext(ctx, `ALTER TABLE proxies DROP COLUMN real_ip_updated_at`)
				require.NoError(t, err)
			}
			_, err = db.SQL.ExecContext(ctx, `DELETE FROM schema_migrations WHERE id >= $1`, id)
			require.NoError(t, err)
			done, err := store.Migrate(ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
			require.NoError(t, err)
			require.Contains(t, done, id)
			var gotIP *string
			var gotTime *time.Time
			require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT real_ip, real_ip_updated_at FROM proxies WHERE id=$1`, proxyID).Scan(&gotIP, &gotTime))
			if missing == "real_ip" || missing == "both" {
				require.Nil(t, gotIP)
			} else {
				require.NotNil(t, gotIP)
				require.Equal(t, ip, *gotIP)
			}
			if missing == "real_ip_updated_at" || missing == "both" {
				require.Nil(t, gotTime)
			} else {
				require.NotNil(t, gotTime)
				require.True(t, stamp.Equal(*gotTime))
			}
			var recorded string
			require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT checksum FROM schema_migrations WHERE id=$1`, id).Scan(&recorded))
			require.Equal(t, checksum, recorded)
			done, err = store.Migrate(ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
			require.NoError(t, err)
			require.Empty(t, done)
		})
	}
}

func TestLegacyProxyRealIPCompatibilityRequiresExactIdentity(t *testing.T) {
	const original = "0046_proxy_real_ip.sql"
	for _, changed := range []string{"name", "body", "applied_body"} {
		t.Run(changed, func(t *testing.T) {
			db := testutil.DB(t)
			ctx := context.Background()
			body, err := migrations.FS.ReadFile(original)
			require.NoError(t, err)
			name := original
			if changed == "name" {
				name = "9999_proxy_real_ip.sql"
			} else {
				body = append(body, '\n')
				if changed == "body" {
					_, err = db.SQL.ExecContext(ctx, `DELETE FROM schema_migrations WHERE id=$1`, original)
					require.NoError(t, err)
				}
			}
			_, err = store.Migrate(ctx, db, fstest.MapFS{name: {Data: body}}, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
			if changed == "applied_body" {
				require.ErrorContains(t, err, "modified after being applied")
			} else {
				require.ErrorContains(t, err, "already exists")
			}
			var recorded bool
			require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE id=$1)`, name).Scan(&recorded))
			require.Equal(t, changed == "applied_body", recorded)
		})
	}
}

// replayDB is a fresh database for the tests that forget applied
// migrations and run them again: through 0099, since 0100 drops the role
// tables and permissions.sensitive the migrations before it write. None of those runs again after
// 0100 outside a test: a migration is recorded in its own transaction.
func replayDB(t *testing.T) *store.DB {
	t.Helper()
	return testutil.DBThrough(t, beforeCasbin)
}

// Every migration from FirstIdempotentMigration on must survive a second
// run on a database that already has it: forget them and migrate again.
// Those before 0100 run again on the schema before it (replayDB), 0100 and
// later on the full one.
func TestMigrationsFromFirstIdempotentRunTwice(t *testing.T) {
	for _, c := range []struct {
		name, from string
		db         func(*testing.T) *store.DB
	}{
		{"before casbin_rule", migrations.FirstIdempotentMigration, replayDB},
		{"from casbin_rule", casbinRule, func(t *testing.T) *store.DB { return testutil.DB(t) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			db := c.db(t)
			ctx := context.Background()
			tag, err := db.SQL.ExecContext(ctx, `DELETE FROM schema_migrations WHERE id >= $1`, c.from)
			require.NoError(t, err)
			forgot, err := tag.RowsAffected()
			require.NoError(t, err)
			require.NotZero(t, forgot, "%s is not among the applied migrations", c.from)
			again, err := store.Migrate(ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
			require.NoError(t, err, "second run")
			// Through 0099, the second run applies 0100 and later for the
			// first time too.
			require.Equal(t, migrationsAfter(t, migrationBefore(t, c.from)), again)
		})
	}
}

// migrationBefore is the core migration applied just before id.
func migrationBefore(t *testing.T, id string) string {
	t.Helper()
	names, err := fs.Glob(migrations.FS, "*.sql")
	require.NoError(t, err)
	slices.Sort(names)
	i, found := slices.BinarySearch(names, id)
	require.True(t, found && i > 0, "no migration before %s", id)
	return names[i-1]
}

// 0048 removes what plugins brought into sticky sessions and keeps it out:
// only built-in and admin rules, never a plugin-computed session value.
func TestStickyRulesCoreOnly(t *testing.T) {
	db := replayDB(t)
	ctx := context.Background()
	if _, err := db.SQL.ExecContext(ctx, `
		ALTER TABLE sticky_rules DROP CONSTRAINT sticky_rules_core_only;
		INSERT INTO plugins (key, name, status) VALUES ('acme_sticky', '{"en":"x"}', 'enabled');
		INSERT INTO sticky_rules (name, source, plugin_key, key_sources) VALUES
			('from-plugin', 'plugin_default', 'acme_sticky', '[{"type":"header","name":"x"}]'),
			('plugin-value', 'admin', NULL, '[{"type":"user"},{"type":"plugin"}]'),
			('kept', 'admin', NULL, '[{"type":"header","name":"x-session"}]');
		DELETE FROM schema_migrations WHERE id >= '0048'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Migrate(ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey}); err != nil {
		t.Fatal(err)
	}
	var names []string
	rows, err := db.SQL.QueryContext(ctx, `SELECT name FROM sticky_rules WHERE source = 'admin' OR plugin_key IS NOT NULL ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		names = append(names, n)
	}
	rows.Close()
	if len(names) != 1 || names[0] != "kept" {
		t.Fatalf("rules after 0048: %v", names)
	}
	for _, bad := range []string{
		`INSERT INTO sticky_rules (name, source, key_sources) VALUES ('x', 'plugin_default', '[{"type":"user"}]')`,
		`INSERT INTO sticky_rules (name, source, plugin_key, key_sources) VALUES ('x', 'admin', 'acme_sticky', '[{"type":"user"}]')`,
		`INSERT INTO sticky_rules (name, source, key_sources) VALUES ('x', 'admin', '[{"type":"plugin"}]')`,
	} {
		if _, err := db.SQL.ExecContext(ctx, bad); err == nil {
			t.Fatalf("accepted: %s", bad)
		}
	}
}

// 0027 keeps proxies that existed before it working, once: a re-run must not
// let proxies saved since reach private addresses.
func TestSecurityHardeningDoesNotReopenPrivateProxies(t *testing.T) {
	db := replayDB(t)
	ctx := context.Background()
	var id int64
	if err := db.SQL.QueryRowContext(ctx, `INSERT INTO proxies (name, protocol, host, port, allow_private)
		VALUES ('later', 'http', '10.0.0.1', 8080, false) RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.ExecContext(ctx, `DELETE FROM schema_migrations WHERE id >= '0027'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Migrate(ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey}); err != nil {
		t.Fatal(err)
	}
	var allow bool
	if err := db.SQL.QueryRowContext(ctx, `SELECT allow_private FROM proxies WHERE id = $1`, id).Scan(&allow); err != nil {
		t.Fatal(err)
	}
	if allow {
		t.Fatal("re-running 0027 opened private addresses to a proxy saved after it")
	}
}

// quota_used starts from what each key was charged, then follows every charge
// through the usage_logs trigger (CONTRACTS §61); rerunning the migration
// keeps a reset, and deleting usage rows gives nothing back.
func TestAPIKeyQuotaMigrationAndTrigger(t *testing.T) {
	db := testutil.DB(t)
	ctx := t.Context()
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := db.SQL.ExecContext(ctx, sql, args...)
		require.NoError(t, err)
	}
	used := func() string {
		t.Helper()
		var v string
		require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT quota_used::text FROM api_keys WHERE name='quota'`).Scan(&v))
		return v
	}
	migrate := func() {
		t.Helper()
		_, err := store.Migrate(ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
		require.NoError(t, err)
	}
	usage := func(rid, status, cost string) {
		t.Helper()
		exec(`INSERT INTO usage_logs (request_id, user_id, api_key_id, group_id, protocol, endpoint, status_code, success, total_cost, billing_status)
			SELECT $1, k.user_id, k.id, k.group_id, 'anthropic.messages', '/v1/messages', 200, true, $3::numeric, $2 FROM api_keys k WHERE k.name='quota'`, rid, status, cost)
	}
	exec(`DROP TRIGGER apikey_quota_track ON usage_logs; DROP FUNCTION apikey_quota_track(); DROP FUNCTION apikey_quota_charge(text, numeric);
		ALTER TABLE api_keys DROP COLUMN quota_limit, DROP COLUMN quota_used;
		DELETE FROM schema_migrations WHERE id='0059_apikey_quota.sql';
		INSERT INTO users(email,password_hash) VALUES ('quota@example.com','test');
		INSERT INTO groups(name) VALUES ('quota');
		INSERT INTO api_keys(user_id,group_id,name,key_prefix,key_hash)
		SELECT u.id,g.id,'quota','masked','quota-hash' FROM users u,groups g WHERE u.email='quota@example.com' AND g.name='quota'`)
	for i, r := range [][2]string{{"billed", "1.5"}, {"reserved", "0.5"}, {"free", "2"}, {"failed", "3"}, {"pending", "4"}} {
		usage(fmt.Sprint("old-", i), r[0], r[1])
	}
	migrate()
	require.Equal(t, "2.00000000", used(), "billed and reserved rows so far")

	usage("new", "pending", "0")
	exec(`UPDATE usage_logs SET total_cost = 0.25, billing_status = 'billed' WHERE request_id = 'new'`)
	require.Equal(t, "2.25000000", used(), "settled")
	exec(`UPDATE usage_logs SET total_cost = 0.75 WHERE request_id = 'old-1'`)
	require.Equal(t, "2.50000000", used(), "reconciled")
	exec(`UPDATE usage_logs SET total_cost = 0, billing_status = 'free' WHERE request_id = 'old-0'`)
	require.Equal(t, "1.00000000", used(), "refunded")
	exec(`DELETE FROM usage_logs WHERE request_id = 'new'`)
	require.Equal(t, "1.00000000", used(), "deleting history gives nothing back")

	exec(`UPDATE api_keys SET quota_used = 0 WHERE name='quota'`)
	exec(`DELETE FROM schema_migrations WHERE id='0059_apikey_quota.sql'`)
	migrate()
	require.Equal(t, "0.00000000", used(), "a rerun keeps the reset")
}

// 0063 brings back accounts.spm_limit (CONTRACTS §71): values left from the
// SPM removed in 2026-10 are cleared once, values set afterwards survive a
// re-run, and the column keeps its range.
func TestAccountSPMLimitMigration(t *testing.T) {
	db := testutil.DB(t)
	ctx := t.Context()
	exec := func(sql string) error {
		_, err := db.SQL.ExecContext(ctx, sql)
		return err
	}
	spm := func() (v int) {
		t.Helper()
		require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT spm_limit FROM accounts WHERE name='spm'`).Scan(&v))
		return v
	}
	migrate := func() []string {
		t.Helper()
		done, err := store.Migrate(ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
		require.NoError(t, err)
		return done
	}
	const forget = `DELETE FROM schema_migrations WHERE id='0063_account_spm_limit.sql'`
	require.NoError(t, exec(`ALTER TABLE accounts DROP CONSTRAINT accounts_spm_limit_chk;
		COMMENT ON COLUMN accounts.spm_limit IS NULL;
		INSERT INTO accounts(name,plugin_key,type,credentials_enc,spm_limit) VALUES('spm','fixture','apikey','',7);
		`+forget))
	require.Equal(t, []string{"0063_account_spm_limit.sql"}, migrate())
	require.Equal(t, 0, spm(), "a cap from the removed SPM came back")

	require.NoError(t, exec(`UPDATE accounts SET spm_limit = 3 WHERE name='spm'; `+forget))
	migrate()
	require.Equal(t, 3, spm(), "a rerun cleared a cap set after the migration")
	require.Error(t, exec(`UPDATE accounts SET spm_limit = 10000001 WHERE name='spm'`))
}

// 0094 adds accounts.spm_window_seconds (CONTRACTS §71.1): existing accounts
// count sessions over 60 seconds as before, a re-run keeps what was set, and
// the column keeps its range.
func TestAccountSPMWindowMigration(t *testing.T) {
	db := testutil.DB(t)
	ctx := t.Context()
	exec := func(sql string) error {
		_, err := db.SQL.ExecContext(ctx, sql)
		return err
	}
	window := func() (v int) {
		t.Helper()
		require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT spm_window_seconds FROM accounts WHERE name='window'`).Scan(&v))
		return v
	}
	migrate := func() []string {
		t.Helper()
		done, err := store.Migrate(ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
		require.NoError(t, err)
		return done
	}
	const forget = `DELETE FROM schema_migrations WHERE id='0094_account_spm_window.sql'`
	require.NoError(t, exec(`ALTER TABLE accounts DROP COLUMN spm_window_seconds;
		INSERT INTO accounts(name,plugin_key,type,credentials_enc) VALUES('window','fixture','apikey','');
		`+forget))
	require.Equal(t, []string{"0094_account_spm_window.sql"}, migrate())
	require.Equal(t, 60, window(), "an existing account changed window")

	require.NoError(t, exec(`UPDATE accounts SET spm_window_seconds = 86400 WHERE name='window'; `+forget))
	migrate()
	require.Equal(t, 86400, window(), "a rerun reset the window")
	require.Error(t, exec(`UPDATE accounts SET spm_window_seconds = 59 WHERE name='window'`))
	require.Error(t, exec(`UPDATE accounts SET spm_window_seconds = 86401 WHERE name='window'`))
}

// 0099 gives permissions a risk level (docs/plugin-permissions ch.2):
// sensitive rows start sensitive, the others low, scope_type takes "plugin:"
// and a 30-character resource, a re-run keeps a level a sync wrote since,
// and only the three levels are accepted.
func TestPluginPermissionMetaMigration(t *testing.T) {
	db := testutil.DBThrough(t, "0098_plugin_staged_grants.sql")
	ctx := t.Context()
	exec := func(sql string) error {
		_, err := db.SQL.ExecContext(ctx, sql)
		return err
	}
	risks := func() (v string) {
		t.Helper()
		require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT string_agg(key || '=' || risk, ' ' ORDER BY key) FROM permissions`).Scan(&v))
		return v
	}
	migrate := func() []string { // 0099 alone: 0100 drops sensitive
		t.Helper()
		done, err := store.Migrate(ctx, db, testutil.MigrationsThrough(t, beforeCasbin), store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
		require.NoError(t, err)
		return done
	}
	const forget = `DELETE FROM schema_migrations WHERE id='0099_plugin_permission_meta.sql'`
	require.NoError(t, exec(`INSERT INTO permissions (key, module, label, source, sensitive) VALUES
		('a:read','a','{}','core',false), ('a:secret','a','{}','core',true), ('b:secret','b','{}','core',true)`))
	require.Equal(t, []string{beforeCasbin}, migrate())
	require.Equal(t, "a:read=low a:secret=sensitive b:secret=sensitive", risks())

	require.NoError(t, exec(`UPDATE permissions SET risk = 'critical' WHERE key = 'a:secret';
		UPDATE permissions SET scope_type = 'plugin:' || repeat('r', 30) WHERE key = 'a:read'; `+forget))
	require.Equal(t, []string{"0099_plugin_permission_meta.sql"}, migrate())
	require.Equal(t, "a:read=low a:secret=critical b:secret=sensitive", risks(), "a rerun reset a level set after the migration")
	require.Error(t, exec(`UPDATE permissions SET risk = 'high' WHERE key = 'a:read'`))
}

// 0101 drops the JWT session tables (CONTRACTS §100.6), and a rerun
// changes nothing.
func TestMigration0101DropsJWTSessions(t *testing.T) {
	const id = "0101_drop_jwt_sessions.sql"
	db := testutil.DBThrough(t, migrationBefore(t, id))
	ctx := t.Context()
	migrate := func() []string {
		t.Helper()
		done, err := store.Migrate(ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
		require.NoError(t, err)
		return done
	}
	tables := func() (tables string) {
		t.Helper()
		require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT concat_ws(' ', to_regclass('refresh_tokens'), to_regclass('login_sessions'), to_regclass('auth_flows'))`).Scan(&tables))
		return tables
	}
	require.Equal(t, "refresh_tokens login_sessions auth_flows", tables())

	require.Equal(t, migrationsAfter(t, migrationBefore(t, id)), migrate())
	require.Empty(t, tables(), "tables left")

	_, err := db.SQL.ExecContext(ctx, `DELETE FROM schema_migrations WHERE id = $1`, id)
	require.NoError(t, err)
	require.Equal(t, []string{id}, migrate())
	require.Empty(t, tables(), "a rerun recreated tables")
}

// 0102 removes CCGateway (CONTRACTS §101.5): its tables, columns, settings
// and tasks, the account lifecycle contract, and the plugin as an uninstall
// with purge would, its permissions and their grants included. Its accounts,
// their usage and other plugins stay. It also drops what only the cores
// before 0.1.290 used: the scheduling mode (normalizing the row) and
// async_tasks.legacy_imported. A rerun changes nothing.
func TestMigration0102DropsCCGatewayAndLegacy(t *testing.T) {
	const id = "0102_drop_ccgateway_and_legacy.sql"
	db := testutil.DBThrough(t, migrationBefore(t, id))
	ctx := t.Context()
	exec := func(sql string) {
		t.Helper()
		_, err := db.SQL.ExecContext(ctx, sql)
		require.NoError(t, err)
	}
	read := func(sql string) (v string) {
		t.Helper()
		require.NoError(t, db.SQL.QueryRowContext(ctx, sql).Scan(&v))
		return v
	}
	migrate := func() []string {
		t.Helper()
		done, err := store.Migrate(ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
		require.NoError(t, err)
		return done
	}
	exec(`INSERT INTO users (email, password_hash) VALUES ('u@x', '');
		INSERT INTO groups (name) VALUES ('g');
		INSERT INTO roles (key, name, superuser) VALUES ('ops', '{}', false), ('other', '{}', false);
		INSERT INTO plugins (key, name, status, builtin) VALUES ('ccgateway', '{"en":"CCGateway"}', 'enabled', true), ('guard', '{"en":"Guard"}', 'enabled', false);
		INSERT INTO permissions (key, module, label, source, plugin_key) VALUES
			('ccgateway:x', 'ccgateway', '{}', 'plugin', 'ccgateway'), ('guard:x', 'guard', '{}', 'plugin', 'guard');
		INSERT INTO casbin_rule (ptype, v0, v1, v2) VALUES ('p', 'r:ops', 'ccgateway:x', '*'), ('p', 'r:other', 'guard:x', '*');
		INSERT INTO plugin_egress_domains (plugin_key, host) VALUES ('ccgateway', 'example.com');
		INSERT INTO accounts (name, plugin_key, type, credentials_enc, status, schedulable, rate_limit_tier, subscription_type, auto_models, deleted_at) VALUES
			('gone', 'ccgateway', 'managed', '', 'disabled', false, 'default_claude_max_20x', 'max', false, now()),
			('live', 'ccgateway', 'managed', '', 'active', true, NULL, NULL, NULL, NULL);
		INSERT INTO account_lifecycle_sync (account_id, status) SELECT id, 'applied' FROM accounts;
		INSERT INTO account_preparations (key, plugin_key, account_type, created_by) SELECT 'p1', 'ccgateway', 'managed', id FROM users;
		INSERT INTO ccgateway_runtimes (key, account_id) SELECT id::text, id FROM accounts;
		INSERT INTO ccgateway_account_feature_policies (account_id, mode) SELECT id, 'follow' FROM accounts WHERE name = 'live';
		INSERT INTO provider_fallback_credits (token_hash, user_id, group_id, account_id, principal_id, generation, plugin_key, source_model, prompt_digests, observed_at, expires_at)
			SELECT 'h', u.id, g.id, 1, 'p', 'g', 'ccgateway', 'm', '[]', now(), now() + interval '1 minute' FROM users u, groups g;
		INSERT INTO settings (key, value) VALUES ('ccgateway_remote', '{}'), ('kept', '{}'),
			('scheduling', '{"mode":"default","metric":"tpm","limit_load":false,"model_limits":true}');
		INSERT INTO scheduled_tasks (name, source, timeout_ms) VALUES ('core.ccgateway.runtime_sync', 'core', 1000), ('core.helper.usage_replay', 'core', 1000),
			('core.account.lifecycle_status', 'core', 1000), ('core.account.preparation_cleanup', 'core', 1000), ('core.usage.rollup', 'core', 1000)`)
	revisions := `SELECT string_agg(key || '=' || revision, ' ' ORDER BY key) FROM roles WHERE key IN ('ops', 'other')`
	before, version := read(revisions), read(`SELECT policy_version::text FROM authz_meta`)

	require.Equal(t, migrationsAfter(t, migrationBefore(t, id)), migrate())
	require.Empty(t, read(`SELECT concat_ws(' ', to_regclass('ccgateway_runtimes'), to_regclass('ccgateway_account_feature_policies'),
		to_regclass('ccgateway_account_request_logs'), to_regclass('provider_resources'), to_regclass('provider_fallback_credits'),
		to_regclass('provider_helper_records'), to_regclass('provider_helper_usage_receipts'), to_regclass('provider_diagnostic_messages'),
		to_regclass('account_preparations'), to_regclass('account_lifecycle_sync'))`), "tables left")
	require.Equal(t, "0", read(`SELECT count(*)::text FROM information_schema.columns WHERE table_name = 'accounts'
		AND column_name IN ('status_changed_at', 'rate_limit_tier', 'subscription_type', 'auto_models')`))
	require.Equal(t, "0", read(`SELECT count(*)::text FROM information_schema.columns WHERE table_name = 'async_tasks' AND column_name = 'legacy_imported'`))
	// A row of the former default mode with the limit load off had the metric
	// and model limits greyed out: they take the defaults.
	scheduling := `SELECT value::text FROM settings WHERE key = 'scheduling'`
	require.Equal(t, `{"metric": "sessions", "limit_load": false, "model_limits": false}`, read(scheduling))
	require.Equal(t, "guard", read(`SELECT string_agg(key, ' ') FROM plugins`))
	require.Equal(t, "guard:x", read(`SELECT string_agg(key, ' ') FROM permissions WHERE plugin_key IS NOT NULL`))
	require.Equal(t, "r:other guard:x", read(`SELECT string_agg(v0 || ' ' || v1, ' ') FROM casbin_rule`))
	after := read(revisions)
	require.NotEqual(t, before, after, "the role that held the permission keeps its revision")
	require.Equal(t, strings.Split(before, " ")[1], strings.Split(after, " ")[1], "another role moved")
	require.NotEqual(t, version, read(`SELECT policy_version::text FROM authz_meta`))
	require.Equal(t, "ccgateway", read(`SELECT string_agg(plugin_key, ' ') FROM plugin_key_retirements`))
	require.Equal(t, "0", read(`SELECT count(*)::text FROM plugin_egress_domains`))
	require.Equal(t, "kept", read(`SELECT string_agg(key, ' ') FROM settings WHERE key IN ('ccgateway_remote', 'kept')`))
	require.Equal(t, "core.usage.rollup", read(`SELECT string_agg(name, ' ') FROM scheduled_tasks`))
	require.Equal(t, "gone live", read(`SELECT string_agg(name, ' ' ORDER BY name) FROM accounts WHERE plugin_key = 'ccgateway'`), "accounts are kept")

	exec(`UPDATE settings SET value = '{"metric":"rpm","limit_load":false,"model_limits":true}' WHERE key = 'scheduling'`)
	exec(`DELETE FROM schema_migrations WHERE id = '` + id + `'`)
	require.Equal(t, []string{id}, migrate())
	require.Equal(t, "ccgateway", read(`SELECT string_agg(plugin_key, ' ') FROM plugin_key_retirements`))
	require.Equal(t, `{"metric": "rpm", "limit_load": false, "model_limits": true}`, read(scheduling), "a rerun reset a saved row")
}

// 0103 adds the owners of CONTRACTS §103: an account's owner starts as its
// creator, a user cannot own itself or a missing user, its owner path ends
// with its owner and is computed from owner_id (again on a rerun), a user
// that owns others is not hard-deleted, a hard delete of an account's
// owner clears it, and a rerun keeps an account owner moved since.
func TestMigration0103UserAndAccountOwners(t *testing.T) {
	const id = "0103_user_owner.sql"
	db := testutil.DBThrough(t, migrationBefore(t, id))
	ctx := t.Context()
	exec := func(sql string) error {
		t.Helper()
		_, err := db.SQL.ExecContext(ctx, sql)
		return err
	}
	read := func(sql string) (v string) {
		t.Helper()
		require.NoError(t, db.SQL.QueryRowContext(ctx, sql).Scan(&v))
		return v
	}
	migrate := func() []string {
		t.Helper()
		done, err := store.Migrate(ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
		require.NoError(t, err)
		return done
	}
	require.NoError(t, exec(`INSERT INTO users (email, password_hash) VALUES ('a@x', ''), ('b@x', '');
		INSERT INTO accounts (name, plugin_key, type, credentials_enc, created_by) SELECT 'mine', 'p', 't', '', id FROM users WHERE email = 'a@x';
		INSERT INTO accounts (name, plugin_key, type, credentials_enc) VALUES ('nobody', 'p', 't', '')`))
	// The backfill fires the accounts triggers (0053, 0070): an account
	// auto-disabled by a usage limit with a deferred disable, and its quota
	// snapshot, come out as they went in, owner_id aside.
	require.NoError(t, exec(`SET sup2api.auto_disabled_source = 'written';
		INSERT INTO accounts (name, plugin_key, type, credentials_enc, created_by, status, auto_disabled_source, auto_disabled_until,
			deferred_auto_disable, deferred_auto_disable_reason, deferred_auto_disabled_until)
		SELECT 'limited', 'p', 't', '', id, 'auto_disabled', 'usage_limit', now() + interval '1 hour', true, 'limit', now() + interval '1 hour'
		FROM users WHERE email = 'a@x';
		INSERT INTO account_quota_snapshots (account_id, windows, updated_at, auto_refresh) SELECT id, '{"5h": {}}', now(), true FROM accounts;
		RESET sup2api.auto_disabled_source`))
	// Each row afterwards contains what it held before, owner_id aside, so
	// the columns later migrations add do not count.
	rows := `SELECT jsonb_agg(jsonb_build_object('a', to_jsonb(a) - 'owner_id', 'q', to_jsonb(q)) ORDER BY a.id)
		FROM accounts a JOIN account_quota_snapshots q ON q.account_id = a.id`
	before := read(rows)
	unchanged := func() bool {
		t.Helper()
		var ok bool
		require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT (`+rows+`) @> $1::jsonb`, before).Scan(&ok))
		return ok
	}
	owners := `SELECT string_agg(a.name || '=' || COALESCE(u.email, '-'), ' ' ORDER BY a.name) FROM accounts a LEFT JOIN users u ON u.id = a.owner_id`

	require.Equal(t, migrationsAfter(t, migrationBefore(t, id)), migrate())
	require.Equal(t, "limited=a@x mine=a@x nobody=-", read(owners))
	require.True(t, unchanged(), "the backfill changed an account or its quota snapshot")
	require.Equal(t, "3", read(`SELECT count(*)::text FROM pg_indexes WHERE indexname IN ('users_owner_idx', 'users_owner_path_idx', 'accounts_owner_idx')`))
	require.ErrorContains(t, exec(`UPDATE users SET owner_id = id, owner_path = ARRAY[id] WHERE email = 'a@x'`), "users_owner_not_self")
	require.ErrorContains(t, exec(`UPDATE users SET owner_id = 999999, owner_path = '{999999}' WHERE email = 'a@x'`), "users_owner_fk")
	require.ErrorContains(t, exec(`UPDATE users SET owner_id = (SELECT id FROM users WHERE email = 'b@x') WHERE email = 'a@x'`), "users_owner_path_ck")
	require.ErrorContains(t, exec(`UPDATE accounts SET owner_id = 999999`), "accounts_owner_fk")

	// A rerun computes every path from owner_id (here with the paths
	// emptied and their check dropped), the same ones again.
	paths := `SELECT string_agg(email || '=' || owner_path::text, ' ' ORDER BY email) FROM users`
	require.NoError(t, exec(`INSERT INTO users (email, password_hash) VALUES ('c@x', '');
		UPDATE accounts SET owner_id = (SELECT id FROM users WHERE email = 'b@x') WHERE name = 'mine';
		ALTER TABLE users DROP CONSTRAINT users_owner_path_ck;
		UPDATE users SET owner_id = (SELECT id FROM users WHERE email = 'a@x') WHERE email = 'b@x';
		UPDATE users SET owner_id = (SELECT id FROM users WHERE email = 'b@x') WHERE email = 'c@x';
		DELETE FROM schema_migrations WHERE id = '`+id+`'`))
	require.Equal(t, []string{id}, migrate())
	require.Equal(t, "limited=a@x mine=b@x nobody=-", read(owners), "a rerun moved an owner back")
	want := read(`SELECT format('a@x={} b@x={%s} c@x={%s,%s}', a.id, a.id, b.id) FROM users a, users b WHERE a.email = 'a@x' AND b.email = 'b@x'`)
	require.Equal(t, want, read(paths))
	require.NoError(t, exec(`DELETE FROM schema_migrations WHERE id = '`+id+`'`))
	require.Equal(t, []string{id}, migrate())
	require.Equal(t, want, read(paths), "a second rerun")

	// A user that owns others is not deleted: its id is in their paths.
	require.ErrorContains(t, exec(`DELETE FROM users WHERE email = 'b@x'`), "users_owner_fk")
	require.NoError(t, exec(`DELETE FROM users WHERE email = 'c@x'; DELETE FROM users WHERE email = 'b@x'`))
	require.Equal(t, "limited=a@x mine=- nobody=-", read(owners))
	require.Equal(t, "0", read(`SELECT count(*)::text FROM users WHERE owner_id IS NOT NULL`))
}

// 0064 and 0068 sum an account's usage (CONTRACTS §73): existing rows are
// backfilled once, then the trigger journals settlement, reconcile, refunds
// and a moved account, and account_usage_fold sums the journal; deleting
// history takes nothing back and a re-run counts nothing twice.
func TestAccountUsageMigrationAndTrigger(t *testing.T) {
	db := testutil.DB(t)
	ctx := t.Context()
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := db.SQL.ExecContext(ctx, sql, args...)
		require.NoError(t, err)
	}
	fold := func() {
		t.Helper()
		exec(`SELECT account_usage_fold(2)`) // a small batch: folding takes several statements
		exec(`SELECT account_usage_fold(1000)`)
	}
	totals := func(name string) string {
		t.Helper()
		fold()
		var v string
		err := db.SQL.QueryRowContext(ctx, `SELECT coalesce((SELECT cost::text||' '||raw_cost::text||' '||requests||' '||tokens
			FROM account_usage_totals WHERE account_id=(SELECT id FROM accounts WHERE name=$1)),'none')`, name).Scan(&v)
		require.NoError(t, err)
		return v
	}
	hourly := func(name string) string {
		t.Helper()
		fold()
		var v string
		require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT coalesce(sum(cost),0)::text||' '||count(*) FROM account_usage_hourly
			WHERE account_id=(SELECT id FROM accounts WHERE name=$1)`, name).Scan(&v))
		return v
	}
	migrate := func() {
		t.Helper()
		_, err := store.Migrate(ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
		require.NoError(t, err)
	}
	usage := func(rid, account, status, cost, detail, rate string, success bool, at string) {
		t.Helper()
		exec(`INSERT INTO usage_logs (request_id, user_id, api_key_id, group_id, account_id, protocol, endpoint, status_code, success,
			total_cost, billing_status, billing_detail, rate_multiplier, input_tokens, output_tokens, cache_read_tokens, created_at)
			VALUES ($1, 1, 1, 1, (SELECT id FROM accounts WHERE name=$2), 'anthropic.messages', '/v1/messages', 200, $7,
			$4::numeric, $3, $5::jsonb, $6::numeric, 10, 5, 100, $8::timestamptz)`, rid, account, status, cost, detail, rate, success, at)
	}
	exec(`DROP TRIGGER account_usage_track ON usage_logs; DROP FUNCTION account_usage_track(); DROP FUNCTION account_usage_add(bigint, timestamptz, numeric, numeric, bigint, bigint);
		DROP FUNCTION account_usage_fold(int);
		DROP TABLE account_usage_totals, account_usage_hourly, account_usage_limits, account_usage_deltas;
		DELETE FROM schema_migrations WHERE id IN ('0064_account_usage.sql', '0068_account_usage_journal.sql');
		INSERT INTO accounts(name,plugin_key,type,credentials_enc) VALUES ('a','fixture','apikey',''), ('b','fixture','apikey','')`)
	usage("old-billed", "a", "billed", "1.2", `{"cost":"1.0"}`, "1.2", true, "2026-10-01T10:15:00Z")
	usage("old-no-detail", "a", "billed", "0.6", `{}`, "1.5", true, "2026-10-01T10:45:00Z")
	usage("old-free", "a", "free", "0", `{"cost":"9"}`, "1", true, "2026-10-01T11:00:00Z")
	usage("old-failed", "a", "failed", "0", `{}`, "1", false, "2026-10-01T11:30:00Z")
	migrate()
	require.Equal(t, "1.80000000 1.40000000 3 460", totals("a"), "billed rows cost; raw from billing_detail.cost or cost / multiplier; successful rows count")
	require.Equal(t, "1.80000000 2", hourly("a"), "two UTC hours")

	usage("new", "a", "pending", "0", `{}`, "2", true, "2026-10-02T00:05:00Z")
	require.Equal(t, "1.80000000 1.40000000 4 575", totals("a"), "pending: request and tokens, no cost yet")
	exec(`UPDATE usage_logs SET total_cost = 0.5, billing_status = 'billed', billing_detail = '{"cost":"0.25"}' WHERE request_id = 'new'`)
	require.Equal(t, "2.30000000 1.65000000 4 575", totals("a"), "settled")
	exec(`UPDATE usage_logs SET total_cost = 0, billing_status = 'free', success = false WHERE request_id = 'old-billed'`)
	require.Equal(t, "1.10000000 0.65000000 3 575", totals("a"), "refunded")
	exec(`UPDATE usage_logs SET account_id = (SELECT id FROM accounts WHERE name='b') WHERE request_id = 'new'`)
	require.Equal(t, "0.60000000 0.40000000 2 460", totals("a"), "moved away")
	require.Equal(t, "0.50000000 0.25000000 1 115", totals("b"), "moved here")
	exec(`DELETE FROM usage_logs WHERE request_id = 'old-no-detail'`)
	require.Equal(t, "0.60000000 0.40000000 2 460", totals("a"), "deleting history takes nothing back")
	usage("tiny", "b", "billed", "0", `{"cost":"0.000000525"}`, "0", true, "2026-10-02T01:00:00Z")
	require.Equal(t, "0.50000000 0.25000053 2 230", totals("b"), "billing_detail.cost with more than 8 decimals, rounded; a free group still has raw cost")

	// Two settler batches writing the same accounts in opposite orders: the
	// journal takes no row locks, so neither waits on the other (0064's
	// in-place sums deadlocked here).
	tx1, err := testutil.Begin(ctx, db)
	require.NoError(t, err)
	defer func() { _ = tx1.Rollback().Error }()
	tx2, err := testutil.Begin(ctx, db)
	require.NoError(t, err)
	defer func() { _ = tx2.Rollback().Error }()
	insert := func(tx *gorm.DB, rid, account string) {
		t.Helper()
		_, err := store.SQLTx(tx).ExecContext(ctx, `INSERT INTO usage_logs (request_id, user_id, api_key_id, group_id, account_id, protocol, endpoint, status_code, success,
			total_cost, billing_status, rate_multiplier, input_tokens, output_tokens)
			VALUES ($1, 1, 1, 1, (SELECT id FROM accounts WHERE name=$2), 'anthropic.messages', '/v1/messages', 200, true, 1, 'billed', 1, 1, 1)`, rid, account)
		require.NoError(t, err)
	}
	insert(tx1, "batch1-a", "a")
	insert(tx2, "batch2-b", "b")
	insert(tx1, "batch1-b", "b")
	insert(tx2, "batch2-a", "a")
	require.NoError(t, tx1.Commit().Error)
	require.NoError(t, tx2.Commit().Error)
	require.Equal(t, "2.60000000 2.40000000 4 464", totals("a"))

	exec(`DELETE FROM schema_migrations WHERE id IN ('0064_account_usage.sql', '0068_account_usage_journal.sql')`)
	migrate()
	require.Equal(t, "2.60000000 2.40000000 4 464", totals("a"), "a re-run does not backfill again")
}

// 0095 adds segments (CONTRACTS §97): every user joins the default one,
// except those who were granted private groups one by one, who become
// personal and keep exactly those grants. A re-run does not undo what an
// administrator changed since, and a user inserted without a segment joins
// the default one.
func TestSegmentsMigrationBackfill(t *testing.T) {
	db := testutil.DB(t)
	ctx := t.Context()
	exec := func(sql string) {
		t.Helper()
		_, err := db.SQL.ExecContext(ctx, sql)
		require.NoError(t, err)
	}
	text := func(sql string) (v string) {
		t.Helper()
		require.NoError(t, db.SQL.QueryRowContext(ctx, sql).Scan(&v))
		return v
	}
	migrate := func() []string {
		t.Helper()
		done, err := store.Migrate(ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
		require.NoError(t, err)
		return done
	}
	// segmentOf is "default", "personal" or the name of a named segment.
	segmentOf := func(email string) string {
		return text(`SELECT CASE WHEN s.is_default THEN 'default' WHEN s.kind = 'personal' THEN 'personal' ELSE s.name END
			FROM users u JOIN segments s ON s.id = u.segment_id WHERE u.email = '` + email + `'`)
	}
	// grantsOf lists the private groups granted to the personal segment of a user.
	grantsOf := func(email string) string {
		return text(`SELECT string_agg(g.name, ',' ORDER BY g.name) FROM segment_groups sg
			JOIN groups g ON g.id = sg.group_id JOIN segments s ON s.id = sg.segment_id
			JOIN users u ON u.id = s.user_id WHERE u.email = '` + email + `' AND sg.granted`)
	}
	const forget = `DELETE FROM schema_migrations WHERE id='0095_segments.sql'`
	exec(`ALTER TABLE users DROP COLUMN segment_id;
		DROP TABLE segment_model_rates, price_rules, segment_groups, segments;
		DROP FUNCTION default_segment_id();
		INSERT INTO groups(name, visibility) VALUES ('seg-private', 'restricted'), ('seg-private-2', 'restricted'), ('seg-public', 'public');
		INSERT INTO users(email, password_hash) VALUES ('granted@seg', ''), ('other@seg', ''), ('public-only@seg', ''), ('plain@seg', '');
		INSERT INTO users(email, password_hash, deleted_at) VALUES ('deleted@seg', '', now());
		INSERT INTO user_groups(user_id, group_id)
		SELECT u.id, g.id FROM users u JOIN groups g ON (u.email, g.name) IN (
			('granted@seg', 'seg-private'), ('granted@seg', 'seg-private-2'), ('granted@seg', 'seg-public'),
			('other@seg', 'seg-private-2'),
			('public-only@seg', 'seg-public'), ('deleted@seg', 'seg-private'));
		` + forget)
	require.Equal(t, []string{"0095_segments.sql"}, migrate())

	require.Equal(t, "personal", segmentOf("granted@seg"))
	require.Equal(t, "personal", segmentOf("other@seg"))
	require.Equal(t, "default", segmentOf("public-only@seg"), "a grant of a public group made a user personal")
	require.Equal(t, "default", segmentOf("plain@seg"))
	require.Equal(t, "default", segmentOf("deleted@seg"), "a deleted user got a personal segment")
	require.Equal(t, "seg-private,seg-private-2", grantsOf("granted@seg"), "the personal segment keeps every private grant, and those only")
	require.Equal(t, "seg-private-2", grantsOf("other@seg"), "a personal segment got the grants of another user")
	require.Equal(t, "3", text(`SELECT count(*)::text FROM segment_groups`))
	require.Equal(t, "default", text(`SELECT string_agg(DISTINCT CASE WHEN o.is_default THEN 'default' ELSE o.name END, ',') FROM segments s
		JOIN segments o ON o.id = s.origin_id WHERE s.kind = 'personal'`))

	// An insert that does not name the column, as an older binary makes it.
	exec(`INSERT INTO users(email, password_hash) VALUES ('later@seg', '')`)
	require.Equal(t, "default", segmentOf("later@seg"))

	// Moved back to the default segment since: a re-run leaves the user there.
	exec(`UPDATE users SET segment_id = default_segment_id() WHERE email IN ('granted@seg', 'other@seg');
		DELETE FROM segments WHERE kind = 'personal'; ` + forget)
	migrate()
	require.Equal(t, "default", segmentOf("granted@seg"), "a rerun made the user personal again")
	require.Equal(t, "default", segmentOf("other@seg"), "a rerun made the user personal again")
	require.Equal(t, "1", text(`SELECT count(*)::text FROM segments`))

	_, err := db.SQL.ExecContext(ctx, `INSERT INTO segments(kind, name) VALUES ('named', '默认')`)
	require.Error(t, err, "two segments share a name")
	_, err = db.SQL.ExecContext(ctx, `INSERT INTO segments(kind, name) VALUES ('personal', '')`)
	require.Error(t, err, "a personal segment without its user")
	_, err = db.SQL.ExecContext(ctx, `UPDATE segments SET discount = 0 WHERE is_default`)
	require.Error(t, err, "a discount of 0")
}

// Two transactions each adding a different holder and committing together:
// the advisory lock makes the later commit count the earlier holder, so
// exactly one commits. Both commits are held at the lock until both wait.
// 0097's trigger on user_roles, which 0100 replaces with
// casbin_rule_one_superuser (TestCasbinRuleOneSuperuserConcurrent).
func TestOneSuperAdminConcurrentCommits(t *testing.T) {
	db := testutil.DBThrough(t, beforeCasbin)
	ctx := t.Context()
	_, err := db.SQL.ExecContext(ctx, `INSERT INTO roles (key, name, builtin, superuser) VALUES ('super_admin', '{}', true, true);
		INSERT INTO users (email, password_hash) VALUES ('a@su', ''), ('b@su', '')`)
	require.NoError(t, err)
	const lock = `hashtext('user_roles_one_superuser')`
	gate, done, err := testutil.Connect(ctx, testutil.DSN(db))
	require.NoError(t, err)
	defer done()
	_, err = gate.ExecContext(ctx, `SELECT pg_advisory_lock(`+lock+`)`)
	require.NoError(t, err)

	errs := make(chan error, 2)
	for _, email := range []string{"a@su", "b@su"} {
		tx, err := testutil.Begin(ctx, db)
		require.NoError(t, err)
		_, err = store.SQLTx(tx).ExecContext(ctx, `INSERT INTO user_roles (user_id, role_id) SELECT u.id, r.id FROM users u, roles r WHERE r.superuser AND u.email = $1`, email)
		require.NoError(t, err)
		go func() { errs <- tx.Commit().Error }()
	}
	require.Eventually(t, func() bool {
		var n int
		return gate.QueryRowContext(ctx, `SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted
			AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`).Scan(&n) == nil && n == 2
	}, 10*time.Second, 20*time.Millisecond, "both commits wait on the lock")
	_, err = gate.ExecContext(ctx, `SELECT pg_advisory_unlock(`+lock+`)`)
	require.NoError(t, err)

	first, second := <-errs, <-errs
	if first != nil {
		first, second = second, first
	}
	require.NoError(t, first)
	require.True(t, store.IsUniqueViolation(second, "user_roles_one_superuser"), "the second holder: %v", second)
}

// migrationsAfter lists the core migrations after id, in the order
// store.Migrate applies them: what migrating a database through id runs.
func migrationsAfter(t *testing.T, id string) []string {
	t.Helper()
	names, err := fs.Glob(migrations.FS, "*.sql")
	require.NoError(t, err)
	slices.Sort(names)
	i, found := slices.BinarySearch(names, id)
	require.True(t, found, "no migration %s", id)
	return names[i+1:]
}

// 0104 removes the built-in plugin custom (CONTRACTS §106) as an uninstall
// with purge would: the plugin row with what cascades from it and its egress
// domains go, the key is retired; its accounts and other plugins stay. A
// rerun changes nothing.
func TestMigration0104DropsCustomPlugin(t *testing.T) {
	const id = "0104_drop_custom_plugin.sql"
	db := testutil.DBThrough(t, migrationBefore(t, id))
	ctx := t.Context()
	exec := func(sql string) {
		t.Helper()
		_, err := db.SQL.ExecContext(ctx, sql)
		require.NoError(t, err)
	}
	read := func(sql string) (v string) {
		t.Helper()
		require.NoError(t, db.SQL.QueryRowContext(ctx, sql).Scan(&v))
		return v
	}
	migrate := func() []string {
		t.Helper()
		done, err := store.Migrate(ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
		require.NoError(t, err)
		return done
	}
	exec(`INSERT INTO plugins (key, name, status, builtin) VALUES ('custom', '{"en":"Custom upstream"}', 'enabled', true), ('guard', '{"en":"Guard"}', 'enabled', false);
		INSERT INTO plugin_versions (plugin_key, version, manifest, manifest_hash, package_sha256, package_size, signature_status)
			VALUES ('custom', '0.1.1', '{"publisher":"sub2api"}', 'x', 'x', 1, 'valid'), ('guard', '0.2.10', '{"publisher":"sub2api"}', 'y', 'y', 1, 'valid');
		INSERT INTO plugin_egress_domains (plugin_key, host) VALUES ('custom', 'example.com'), ('guard', 'example.com');
		INSERT INTO accounts (name, plugin_key, type, credentials_enc) VALUES ('orphan', 'custom', 't', '')`)

	require.Equal(t, migrationsAfter(t, migrationBefore(t, id)), migrate())
	require.Equal(t, "guard", read(`SELECT string_agg(key, ' ') FROM plugins`))
	require.Equal(t, "guard", read(`SELECT string_agg(plugin_key, ' ') FROM plugin_versions`))
	require.Equal(t, "guard", read(`SELECT string_agg(plugin_key, ' ') FROM plugin_egress_domains`))
	require.Equal(t, "custom", read(`SELECT string_agg(plugin_key, ' ') FROM plugin_key_retirements`))
	require.Equal(t, "orphan", read(`SELECT string_agg(name, ' ') FROM accounts WHERE plugin_key = 'custom'`), "accounts are kept")

	exec(`DELETE FROM schema_migrations WHERE id = '` + id + `'`)
	require.Equal(t, []string{id}, migrate())
	require.Equal(t, "custom", read(`SELECT string_agg(plugin_key, ' ') FROM plugin_key_retirements`))
	require.Equal(t, "guard", read(`SELECT string_agg(key, ' ') FROM plugins`))
}

// 0107 turns the environment variable max_request_body_mb into the gateway
// setting of the same name (CONTRACTS §111): a valid value of the global
// layer moves into the gateway row, keeping its other fields (or creating
// the row), and the key leaves the global layer, the groups and the
// accounts, every other key staying. A value the variable would not have
// accepted is dropped, not moved. A rerun changes nothing.
func TestMigration0107RequestBodyLimitSetting(t *testing.T) {
	const id = "0107_request_body_limit_setting.sql"
	const layers = `INSERT INTO groups (name, environment) VALUES
			('both', '{"max_request_body_mb": 32, "relay_timeout_seconds": 90}'), ('only', '{"max_request_body_mb": 8}'), ('none', '{"task_timeout_minutes": 5}');
		INSERT INTO accounts (name, plugin_key, type, credentials_enc, environment) VALUES
			('stale', 'p', 't', '', '{"max_request_body_mb": 10, "relay_timeout_seconds": 60}'), ('plain', 'p', 't', '', '{}')`
	const layersAfter = ` | both={"relay_timeout_seconds": 90} none={"task_timeout_minutes": 5} only={}` +
		` | plain={} stale={"relay_timeout_seconds": 60}`
	for _, tc := range []struct {
		name, settings, want string
	}{
		{"moved into the gateway row", `('environment', '{"max_request_body_mb": 256, "relay_timeout_seconds": 600}'), ('gateway', '{"max_attempts": 4}')`,
			`environment={"relay_timeout_seconds": 600} gateway={"max_attempts": 4, "max_request_body_mb": 256}`},
		{"gateway row created", `('environment', '{"max_request_body_mb": 1024}')`,
			`environment={} gateway={"max_request_body_mb": 1024}`},
		{"out of range dropped", `('environment', '{"max_request_body_mb": 4096}'), ('gateway', '{"max_attempts": 4}')`,
			`environment={} gateway={"max_attempts": 4}`},
		{"fraction dropped", `('environment', '{"max_request_body_mb": 64.5}')`, `environment={}`},
		{"not a number dropped", `('environment', '{"max_request_body_mb": "64", "relay_timeout_seconds": 600}')`,
			`environment={"relay_timeout_seconds": 600}`},
		{"nothing set", `('environment', '{"relay_timeout_seconds": 600}'), ('gateway', '{"max_attempts": 4}')`,
			`environment={"relay_timeout_seconds": 600} gateway={"max_attempts": 4}`},
		{"no global layer", `('gateway', '{"max_attempts": 4}')`, `gateway={"max_attempts": 4}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := testutil.DBThrough(t, migrationBefore(t, id))
			ctx := t.Context()
			exec := func(sql string) {
				t.Helper()
				_, err := db.SQL.ExecContext(ctx, sql)
				require.NoError(t, err)
			}
			migrate := func() []string {
				t.Helper()
				done, err := store.Migrate(ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
				require.NoError(t, err)
				return done
			}
			state := func() (v string) {
				t.Helper()
				require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT concat_ws(' | ',
					(SELECT string_agg(key || '=' || value::text, ' ' ORDER BY key) FROM settings WHERE key IN ('environment', 'gateway')),
					(SELECT string_agg(name || '=' || environment::text, ' ' ORDER BY name) FROM groups),
					(SELECT string_agg(name || '=' || environment::text, ' ' ORDER BY name) FROM accounts))`).Scan(&v))
				return v
			}
			exec(`DELETE FROM settings WHERE key IN ('environment', 'gateway'); INSERT INTO settings (key, value) VALUES ` + tc.settings + `; ` + layers)

			require.Equal(t, migrationsAfter(t, migrationBefore(t, id)), migrate())
			require.Equal(t, tc.want+layersAfter, state())

			exec(`DELETE FROM schema_migrations WHERE id = '` + id + `'`)
			require.Equal(t, []string{id}, migrate())
			require.Equal(t, tc.want+layersAfter, state())
		})
	}
}

// 0108 makes every column that stores a model name text (no length), keeps
// the character set of a price's model id, and recreates the rollup trigger
// that names the model columns. A rerun changes nothing.
func TestMigration0108ModelNameText(t *testing.T) {
	const id = "0108_model_name_text.sql"
	db := testutil.DBThrough(t, migrationBefore(t, id))
	ctx := t.Context()
	migrate := func() []string {
		t.Helper()
		done, err := store.Migrate(ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
		require.NoError(t, err)
		return done
	}
	read := func(sql string) (v string) {
		t.Helper()
		require.NoError(t, db.SQL.QueryRowContext(ctx, sql).Scan(&v))
		return v
	}
	state := func() string {
		return read(`SELECT string_agg(table_name || '.' || column_name || '=' || data_type, ' ' ORDER BY table_name, column_name)
			FROM information_schema.columns WHERE table_schema = current_schema()
			AND (table_name, column_name) IN (('usage_logs', 'model'), ('usage_logs', 'upstream_model'), ('usage_hourly', 'model'),
				('usage_hourly', 'upstream_model'), ('price_rules', 'model'), ('model_prices', 'model'))`) + " | " +
			read(`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'model_prices_model_id_chk'`) + " | " +
			read(`SELECT count(*)::text FROM pg_trigger WHERE tgname = 'usage_hourly_mark_update'`)
	}
	const want = "model_prices.model=text price_rules.model=text usage_hourly.model=text usage_hourly.upstream_model=text" +
		" usage_logs.model=text usage_logs.upstream_model=text | CHECK ((model ~ '^[A-Za-z0-9._:/@+-]+$'::text)) | 1"
	require.Equal(t, migrationsAfter(t, migrationBefore(t, id)), migrate())
	require.Equal(t, want, state())
	_, err := db.SQL.ExecContext(ctx, `DELETE FROM schema_migrations WHERE id = '`+id+`'`)
	require.NoError(t, err)
	require.Equal(t, []string{id}, migrate())
	require.Equal(t, want, state())
}

// 0112 adds the two image token columns of usage_logs (CONTRACTS §118):
// bigint, NOT NULL, 0 on a row written before them; a rerun changes nothing.
func TestMigration0112UsageImageTokens(t *testing.T) {
	const id = "0112_usage_image_tokens.sql"
	db := testutil.DBThrough(t, migrationBefore(t, id))
	ctx := t.Context()
	migrate := func() []string {
		t.Helper()
		done, err := store.Migrate(ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
		require.NoError(t, err)
		return done
	}
	_, err := db.SQL.ExecContext(ctx, `INSERT INTO usage_logs (request_id, user_id, api_key_id, group_id, protocol, endpoint, status_code, success, input_tokens, output_tokens)
		VALUES ('before-0112', 1, 1, 1, 'openai.images', '/v1/images/generations', 200, true, 70, 900)`)
	require.NoError(t, err)
	state := func() (cols, row string) {
		t.Helper()
		require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT string_agg(column_name || '=' || data_type || ',' || is_nullable || ',' || column_default, ' ' ORDER BY column_name)
			FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'usage_logs'
			AND column_name IN ('image_input_tokens', 'image_output_tokens')`).Scan(&cols))
		require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT input_tokens || ' ' || output_tokens || ' ' || image_input_tokens || ' ' || image_output_tokens
			FROM usage_logs WHERE request_id = 'before-0112'`).Scan(&row))
		return cols, row
	}
	const wantCols = "image_input_tokens=bigint,NO,0 image_output_tokens=bigint,NO,0"
	require.Equal(t, migrationsAfter(t, migrationBefore(t, id)), migrate())
	cols, row := state()
	require.Equal(t, wantCols, cols)
	require.Equal(t, "70 900 0 0", row)
	_, err = db.SQL.ExecContext(ctx, `DELETE FROM schema_migrations WHERE id = '`+id+`'`)
	require.NoError(t, err)
	require.Equal(t, []string{id}, migrate())
	cols, row = state()
	require.Equal(t, wantCols, cols)
	require.Equal(t, "70 900 0 0", row)
}

// 0111 removes the claude_oauth plugin (CONTRACTS §116) as an uninstall
// with purge would: its row (cascading the versions and rollouts), its
// permission and the grants of it, its egress domains, job tasks and a
// hand-made schema. Its accounts and the other plugins stay; a rerun
// changes nothing.
func TestMigration0111DropsClaudeOAuthPlugin(t *testing.T) {
	const id = "0111_drop_claude_oauth_plugin.sql"
	db := testutil.DBThrough(t, migrationBefore(t, id))
	ctx := t.Context()
	exec := func(sql string) {
		t.Helper()
		_, err := db.SQL.ExecContext(ctx, sql)
		require.NoError(t, err)
	}
	read := func(sql string) (v string) {
		t.Helper()
		require.NoError(t, db.SQL.QueryRowContext(ctx, sql).Scan(&v))
		return v
	}
	migrate := func() []string {
		t.Helper()
		done, err := store.Migrate(ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
		require.NoError(t, err)
		return done
	}
	exec(`INSERT INTO roles (key, name, superuser) VALUES ('ops', '{}', false), ('other', '{}', false);
		INSERT INTO plugins (key, name, status, builtin) VALUES ('claude_oauth', '{"en":"Claude OAuth Accounts"}', 'enabled', false), ('guard', '{"en":"Guard"}', 'enabled', false);
		INSERT INTO plugin_versions (plugin_key, version, manifest, manifest_hash, package_sha256, package_size, signature_status)
			VALUES ('claude_oauth', '0.2.5', '{"publisher":"sub2api"}', 'x', 'x', 1, 'valid'), ('guard', '0.2.10', '{"publisher":"sub2api"}', 'y', 'y', 1, 'valid');
		INSERT INTO plugin_rollouts (plugin_key, action, target_version, phase) VALUES ('claude_oauth', 'enable', '0.2.5', 'active');
		INSERT INTO permissions (key, module, label, source, plugin_key) VALUES
			('plugin.claude_oauth:claude_oauth:manage', 'claude_oauth', '{}', 'plugin', 'claude_oauth'), ('guard:x', 'guard', '{}', 'plugin', 'guard');
		INSERT INTO casbin_rule (ptype, v0, v1, v2) VALUES ('p', 'r:ops', 'plugin.claude_oauth:claude_oauth:manage', '*'), ('p', 'r:other', 'guard:x', '*');
		INSERT INTO plugin_egress_domains (plugin_key, host) VALUES ('claude_oauth', 'platform.claude.com'), ('guard', 'example.com');
		INSERT INTO scheduled_tasks (name, source, timeout_ms, plugin_key) VALUES ('plugin.claude_oauth.refresh_tokens', 'plugin_manifest', 1000, 'claude_oauth'),
			('core.usage.rollup', 'core', 1000, NULL);
		INSERT INTO accounts (name, plugin_key, type, credentials_enc) VALUES ('orphan', 'claude_oauth', 'claude_setup_token', '');
		CREATE SCHEMA plg_claude_oauth; CREATE TABLE plg_claude_oauth.t (id int)`)
	revisions := `SELECT string_agg(key || '=' || revision, ' ' ORDER BY key) FROM roles WHERE key IN ('ops', 'other')`
	before, version := read(revisions), read(`SELECT policy_version::text FROM authz_meta`)

	state := func() string {
		return read(`SELECT concat_ws(' | ',
			(SELECT string_agg(key, ' ') FROM plugins),
			(SELECT string_agg(plugin_key, ' ') FROM plugin_versions),
			(SELECT count(*)::text FROM plugin_rollouts),
			(SELECT string_agg(key, ' ') FROM permissions WHERE plugin_key IS NOT NULL),
			(SELECT string_agg(v0 || ' ' || v1, ' ') FROM casbin_rule),
			(SELECT string_agg(plugin_key, ' ') FROM plugin_egress_domains),
			(SELECT string_agg(name, ' ') FROM scheduled_tasks),
			(SELECT string_agg(plugin_key, ' ') FROM plugin_key_retirements),
			(SELECT count(*)::text FROM pg_namespace WHERE nspname = 'plg_claude_oauth'),
			(SELECT string_agg(name, ' ') FROM accounts WHERE plugin_key = 'claude_oauth'))`)
	}
	const want = "guard | guard | 0 | guard:x | r:other guard:x | guard | core.usage.rollup | claude_oauth | 0 | orphan"
	require.Equal(t, migrationsAfter(t, migrationBefore(t, id)), migrate())
	require.Equal(t, want, state())
	after := read(revisions)
	require.NotEqual(t, before, after, "the role that held the permission keeps its revision")
	require.Equal(t, strings.Split(before, " ")[1], strings.Split(after, " ")[1], "another role moved")
	require.NotEqual(t, version, read(`SELECT policy_version::text FROM authz_meta`))

	exec(`DELETE FROM schema_migrations WHERE id = '` + id + `'`)
	version = read(`SELECT policy_version::text FROM authz_meta`)
	require.Equal(t, []string{id}, migrate())
	require.Equal(t, want, state())
	require.Equal(t, version, read(`SELECT policy_version::text FROM authz_meta`), "a rerun moved the policy")
}

// 0116 deletes every grant of accounts.credentials and accounts.read, granted,
// revoked or staged, and keeps the others (CONTRACTS §124); a rerun changes
// nothing.
func TestMigration0116DropsAccountGrants(t *testing.T) {
	const id = "0116_drop_account_grants.sql"
	db := testutil.DBThrough(t, migrationBefore(t, id))
	ctx := t.Context()
	exec := func(sql string) {
		t.Helper()
		_, err := db.SQL.ExecContext(ctx, sql)
		require.NoError(t, err)
	}
	read := func(sql string) (v string) {
		t.Helper()
		require.NoError(t, db.SQL.QueryRowContext(ctx, sql).Scan(&v))
		return v
	}
	migrate := func() []string {
		t.Helper()
		done, err := store.Migrate(ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
		require.NoError(t, err)
		return done
	}
	exec(`INSERT INTO plugins (key, name, status) VALUES ('codex', '{"en":"c"}', 'enabled');
		INSERT INTO plugin_versions (plugin_key, version, manifest, manifest_hash, package_sha256, package_size, signature_status)
			VALUES ('codex', '0.2.0', '{"publisher":"sub2api"}', 'x', 'x', 1, 'valid');
		INSERT INTO plugin_permission_grants (plugin_key, permission, scope, status, plugin_version, manifest_hash) VALUES
			('codex', 'accounts.credentials', '{"types":"own"}', 'granted', '0.1.11', 'x'),
			('codex', 'accounts.read', '{}', 'revoked', '0.1.11', 'x'),
			('codex', 'platform.register', '{}', 'granted', '0.1.11', 'x');
		INSERT INTO plugin_staged_grants (plugin_key, version, permission, scope, status, manifest_hash) VALUES
			('codex', '0.2.0', 'accounts.credentials', '{"types":"own"}', 'granted', 'x'),
			('codex', '0.2.0', 'db.schema', '{}', 'granted', 'x')`)
	state := func() string {
		return read(`SELECT concat_ws(' | ',
			(SELECT string_agg(permission, ' ' ORDER BY permission) FROM plugin_permission_grants),
			(SELECT string_agg(permission, ' ' ORDER BY permission) FROM plugin_staged_grants))`)
	}
	const want = "platform.register | db.schema"
	require.Equal(t, migrationsAfter(t, migrationBefore(t, id)), migrate())
	require.Equal(t, want, state())
	exec(`DELETE FROM schema_migrations WHERE id = '` + id + `'`)
	require.Equal(t, []string{id}, migrate())
	require.Equal(t, want, state())
}

// 0113 lets a balance snapshot have no successful read yet (updated_at
// NULL), adds unlimited (false for the rows there) and drops source; a
// rerun changes nothing (CONTRACTS §52.3).
func TestMigration0113AccountBalanceUnlimited(t *testing.T) {
	const id = "0113_account_balance_unlimited.sql"
	db := testutil.DBThrough(t, migrationBefore(t, id))
	ctx := t.Context()
	migrate := func() []string {
		t.Helper()
		done, err := store.Migrate(ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
		require.NoError(t, err)
		return done
	}
	read := func(sql string) (v string) {
		t.Helper()
		require.NoError(t, db.SQL.QueryRowContext(ctx, sql).Scan(&v))
		return v
	}
	_, err := db.SQL.ExecContext(ctx, `INSERT INTO accounts (name, plugin_key, type, credentials_enc) VALUES ('a', 'p', 't', '');
		INSERT INTO account_balances (account_id, amount_micros, currency, source, updated_at)
			SELECT id, 5, 'USD', 'active', now() FROM accounts WHERE name = 'a'`)
	require.NoError(t, err)
	state := func() string {
		return read(`SELECT string_agg(column_name || '=' || is_nullable, ' ' ORDER BY column_name) FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = 'account_balances'`) + " | " +
			read(`SELECT amount_micros || ' ' || currency || ' ' || unlimited FROM account_balances`)
	}
	const want = "account_id=NO amount_micros=NO currency=NO error=YES error_at=YES unlimited=NO updated_at=YES | 5 USD false"
	require.Equal(t, migrationsAfter(t, migrationBefore(t, id)), migrate())
	require.Equal(t, want, state())
	_, err = db.SQL.ExecContext(ctx, `DELETE FROM schema_migrations WHERE id = '`+id+`'`)
	require.NoError(t, err)
	require.Equal(t, []string{id}, migrate())
	require.Equal(t, want, state())
}

// 0114 widens account_balances.currency to text, keeping the rows there,
// so a unit of any length is stored as the upstream gave it; a rerun
// changes nothing (CONTRACTS §52.3).
func TestMigration0114AccountBalanceUnitText(t *testing.T) {
	const id = "0114_account_balance_unit_text.sql"
	db := testutil.DBThrough(t, migrationBefore(t, id))
	ctx := t.Context()
	migrate := func() []string {
		t.Helper()
		done, err := store.Migrate(ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
		require.NoError(t, err)
		return done
	}
	read := func(sql string) (v string) {
		t.Helper()
		require.NoError(t, db.SQL.QueryRowContext(ctx, sql).Scan(&v))
		return v
	}
	_, err := db.SQL.ExecContext(ctx, `INSERT INTO accounts (name, plugin_key, type, credentials_enc) VALUES ('a', 'p', 't', '');
		INSERT INTO account_balances (account_id, amount_micros, currency, updated_at) SELECT id, 5, 'USD', now() FROM accounts WHERE name = 'a'`)
	require.NoError(t, err)
	const unit = "usd credits / 额度"
	state := func() string {
		return read(`SELECT data_type FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = 'account_balances' AND column_name = 'currency'`) + " | " +
			read(`SELECT string_agg(amount_micros || ' ' || currency, ',' ORDER BY amount_micros) FROM account_balances`)
	}
	require.Equal(t, migrationsAfter(t, migrationBefore(t, id)), migrate())
	require.Equal(t, "text | 5 USD", state())
	_, err = db.SQL.ExecContext(ctx, `WITH a AS (INSERT INTO accounts (name, plugin_key, type, credentials_enc) VALUES ('b', 'p', 't', '') RETURNING id)
		INSERT INTO account_balances (account_id, amount_micros, currency, updated_at) SELECT id, 7, $1, now() FROM a`, unit)
	require.NoError(t, err)
	_, err = db.SQL.ExecContext(ctx, `DELETE FROM schema_migrations WHERE id = '`+id+`'`)
	require.NoError(t, err)
	require.Equal(t, []string{id}, migrate())
	require.Equal(t, "text | 5 USD,7 "+unit, state())
}

// 0117 removes the growth plugin (CONTRACTS §125) as an uninstall with
// purge would: its row (cascading the versions and rollouts), its migration
// records, its permissions (removed ones included) and their grants, its
// egress domains, job tasks and its schema. The other plugins stay; a rerun
// changes nothing.
func TestMigration0117DropsGrowthPlugin(t *testing.T) {
	const id = "0117_drop_growth_plugin.sql"
	db := testutil.DBThrough(t, migrationBefore(t, id))
	ctx := t.Context()
	exec := func(sql string) {
		t.Helper()
		_, err := db.SQL.ExecContext(ctx, sql)
		require.NoError(t, err)
	}
	read := func(sql string) (v string) {
		t.Helper()
		require.NoError(t, db.SQL.QueryRowContext(ctx, sql).Scan(&v))
		return v
	}
	migrate := func() []string {
		t.Helper()
		done, err := store.Migrate(ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
		require.NoError(t, err)
		return done
	}
	exec(`INSERT INTO roles (key, name, superuser) VALUES ('ops', '{}', false), ('other', '{}', false);
		INSERT INTO plugins (key, name, status, builtin) VALUES ('growth', '{"en":"Growth & Engagement"}', 'enabled', false), ('guard', '{"en":"Guard"}', 'enabled', false);
		INSERT INTO plugin_versions (plugin_key, version, manifest, manifest_hash, package_sha256, package_size, signature_status)
			VALUES ('growth', '0.2.2', '{"publisher":"sub2api"}', 'x', 'x', 1, 'valid'), ('guard', '0.2.10', '{"publisher":"sub2api"}', 'y', 'y', 1, 'valid');
		INSERT INTO plugin_rollouts (plugin_key, action, target_version, phase) VALUES ('growth', 'enable', '0.2.2', 'active');
		INSERT INTO plugin_migrations (plugin_key, migration_id, checksum) VALUES ('growth', '0001_init.sql', 'c');
		INSERT INTO permissions (key, module, label, source, plugin_key) VALUES
			('plugin.growth:growth:use', 'growth', '{}', 'plugin', 'growth'), ('plugin.growth:growth:read', 'growth', '{}', 'plugin', 'growth'),
			('guard:x', 'guard', '{}', 'plugin', 'guard');
		UPDATE permissions SET status = 'removed' WHERE key = 'plugin.growth:growth:read';
		INSERT INTO casbin_rule (ptype, v0, v1, v2) VALUES ('p', 'r:ops', 'plugin.growth:growth:use', '*'), ('p', 'r:ops', 'plugin.growth:growth:read', '*'),
			('p', 'r:other', 'guard:x', '*');
		INSERT INTO plugin_egress_domains (plugin_key, host) VALUES ('growth', 'example.org'), ('guard', 'example.com');
		INSERT INTO scheduled_tasks (name, source, timeout_ms, plugin_key) VALUES ('plugin.growth.cleanup', 'plugin_manifest', 1000, 'growth'),
			('core.usage.rollup', 'core', 1000, NULL);
		CREATE SCHEMA plg_growth; CREATE TABLE plg_growth.checkins (id int)`)
	revisions := `SELECT string_agg(key || '=' || revision, ' ' ORDER BY key) FROM roles WHERE key IN ('ops', 'other')`
	before, version := read(revisions), read(`SELECT policy_version::text FROM authz_meta`)

	state := func() string {
		return read(`SELECT concat_ws(' | ',
			(SELECT string_agg(key, ' ') FROM plugins),
			(SELECT string_agg(plugin_key, ' ') FROM plugin_versions),
			(SELECT count(*)::text FROM plugin_rollouts),
			(SELECT count(*)::text FROM plugin_migrations),
			(SELECT string_agg(key, ' ') FROM permissions WHERE plugin_key IS NOT NULL),
			(SELECT string_agg(v0 || ' ' || v1, ' ') FROM casbin_rule),
			(SELECT string_agg(plugin_key, ' ') FROM plugin_egress_domains),
			(SELECT string_agg(name, ' ') FROM scheduled_tasks),
			(SELECT string_agg(plugin_key, ' ') FROM plugin_key_retirements),
			(SELECT count(*)::text FROM pg_namespace WHERE nspname = 'plg_growth'))`)
	}
	const want = "guard | guard | 0 | 0 | guard:x | r:other guard:x | guard | core.usage.rollup | growth | 0"
	require.Equal(t, migrationsAfter(t, migrationBefore(t, id)), migrate())
	require.Equal(t, want, state())
	after := read(revisions)
	require.NotEqual(t, before, after, "the role that held the permissions keeps its revision")
	require.Equal(t, strings.Split(before, " ")[1], strings.Split(after, " ")[1], "another role moved")
	require.NotEqual(t, version, read(`SELECT policy_version::text FROM authz_meta`))

	exec(`DELETE FROM schema_migrations WHERE id = '` + id + `'`)
	version = read(`SELECT policy_version::text FROM authz_meta`)
	require.Equal(t, []string{id}, migrate())
	require.Equal(t, want, state())
	require.Equal(t, version, read(`SELECT policy_version::text FROM authz_meta`), "a rerun moved the policy")
}

// 0118 drops plugin_monitor_receipts, which nothing reads any more; a rerun
// changes nothing.
func TestMigration0118DropsMonitorReceipts(t *testing.T) {
	const id = "0118_drop_plugin_monitor_receipts.sql"
	db := testutil.DBThrough(t, migrationBefore(t, id))
	ctx := t.Context()
	migrate := func() []string {
		t.Helper()
		done, err := store.Migrate(ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
		require.NoError(t, err)
		return done
	}
	exists := func() bool {
		var ok bool
		require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT to_regclass('plugin_monitor_receipts') IS NOT NULL`).Scan(&ok))
		return ok
	}
	_, err := db.SQL.ExecContext(ctx, `INSERT INTO plugin_monitor_receipts (id, digest) VALUES ('entry:1:x', 'd')`)
	require.NoError(t, err)
	require.Equal(t, migrationsAfter(t, migrationBefore(t, id)), migrate())
	require.False(t, exists())
	_, err = db.SQL.ExecContext(ctx, `DELETE FROM schema_migrations WHERE id = '`+id+`'`)
	require.NoError(t, err)
	require.Equal(t, []string{id}, migrate())
	require.False(t, exists())
}

// 0119 removes the volcengine plugin (CONTRACTS §128) as an uninstall with
// purge would: its row (cascading the versions and rollouts), its migration
// records, its permissions and their grants, its egress domains and its
// schema. Its accounts and the other plugins stay; a rerun changes nothing.
func TestMigration0119DropsVolcenginePlugin(t *testing.T) {
	const id = "0119_drop_volcengine_plugin.sql"
	db := testutil.DBThrough(t, migrationBefore(t, id))
	ctx := t.Context()
	exec := func(sql string) {
		t.Helper()
		_, err := db.SQL.ExecContext(ctx, sql)
		require.NoError(t, err)
	}
	read := func(sql string) (v string) {
		t.Helper()
		require.NoError(t, db.SQL.QueryRowContext(ctx, sql).Scan(&v))
		return v
	}
	migrate := func() []string {
		t.Helper()
		done, err := store.Migrate(ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
		require.NoError(t, err)
		return done
	}
	exec(`INSERT INTO roles (key, name, superuser) VALUES ('ops', '{}', false), ('other', '{}', false);
		INSERT INTO plugins (key, name, status, builtin) VALUES ('volcengine', '{"en":"Volcengine Ark"}', 'enabled', true), ('guard', '{"en":"Guard"}', 'enabled', false);
		INSERT INTO plugin_versions (plugin_key, version, manifest, manifest_hash, package_sha256, package_size, signature_status)
			VALUES ('volcengine', '0.12.8', '{"publisher":"sub2api"}', 'x', 'x', 1, 'valid'), ('guard', '0.2.10', '{"publisher":"sub2api"}', 'y', 'y', 1, 'valid');
		INSERT INTO plugin_rollouts (plugin_key, action, target_version, phase) VALUES ('volcengine', 'enable', '0.12.8', 'active');
		INSERT INTO plugin_migrations (plugin_key, migration_id, checksum) VALUES ('volcengine', '0001_init.sql', 'c'), ('guard', '0001_init.sql', 'g');
		INSERT INTO permissions (key, module, label, source, plugin_key) VALUES
			('plugin.volcengine:assets:read', 'volcengine', '{}', 'plugin', 'volcengine'), ('plugin.volcengine:assets:manage', 'volcengine', '{}', 'plugin', 'volcengine'),
			('guard:x', 'guard', '{}', 'plugin', 'guard');
		INSERT INTO casbin_rule (ptype, v0, v1, v2) VALUES ('p', 'r:ops', 'plugin.volcengine:assets:read', '*'), ('p', 'r:ops', 'plugin.volcengine:assets:manage', '*'),
			('p', 'r:other', 'guard:x', '*');
		INSERT INTO plugin_egress_domains (plugin_key, host) VALUES ('volcengine', 'ark.cn-beijing.volces.com'), ('guard', 'example.com');
		INSERT INTO scheduled_tasks (name, source, timeout_ms, plugin_key) VALUES ('core.usage.rollup', 'core', 1000, NULL);
		INSERT INTO accounts (name, plugin_key, type, credentials_enc) VALUES ('ark', 'volcengine', 'apikey', ''), ('doubao', 'volcengine', 'relay', '');
		CREATE SCHEMA plg_volcengine; CREATE TABLE plg_volcengine.assets (id int)`)
	revisions := `SELECT string_agg(key || '=' || revision, ' ' ORDER BY key) FROM roles WHERE key IN ('ops', 'other')`
	before, version := read(revisions), read(`SELECT policy_version::text FROM authz_meta`)

	state := func() string {
		return read(`SELECT concat_ws(' | ',
			(SELECT string_agg(key, ' ') FROM plugins),
			(SELECT string_agg(plugin_key, ' ') FROM plugin_versions),
			(SELECT count(*)::text FROM plugin_rollouts),
			(SELECT string_agg(plugin_key, ' ') FROM plugin_migrations),
			(SELECT string_agg(key, ' ') FROM permissions WHERE plugin_key IS NOT NULL),
			(SELECT string_agg(v0 || ' ' || v1, ' ') FROM casbin_rule),
			(SELECT string_agg(plugin_key, ' ') FROM plugin_egress_domains),
			(SELECT string_agg(name, ' ') FROM scheduled_tasks),
			(SELECT string_agg(plugin_key, ' ') FROM plugin_key_retirements),
			(SELECT count(*)::text FROM pg_namespace WHERE nspname = 'plg_volcengine'),
			(SELECT string_agg(name, ' ' ORDER BY name) FROM accounts WHERE plugin_key = 'volcengine'))`)
	}
	const want = "guard | guard | 0 | guard | guard:x | r:other guard:x | guard | core.usage.rollup | volcengine | 0 | ark doubao"
	require.Equal(t, migrationsAfter(t, migrationBefore(t, id)), migrate())
	require.Equal(t, want, state())
	after := read(revisions)
	require.NotEqual(t, before, after, "the role that held the permissions keeps its revision")
	require.Equal(t, strings.Split(before, " ")[1], strings.Split(after, " ")[1], "another role moved")
	require.NotEqual(t, version, read(`SELECT policy_version::text FROM authz_meta`))

	exec(`DELETE FROM schema_migrations WHERE id = '` + id + `'`)
	version = read(`SELECT policy_version::text FROM authz_meta`)
	require.Equal(t, []string{id}, migrate())
	require.Equal(t, want, state())
	require.Equal(t, version, read(`SELECT policy_version::text FROM authz_meta`), "a rerun moved the policy")
}

// 0120 removes the model catalog of the built-in plugin anthropic (CONTRACTS
// §129): its permission and the grants of it, its schema and its migration
// records go; the plugin itself, other permissions and other plugins stay.
// A rerun changes nothing.
func TestMigration0120DropsAnthropicModelCatalog(t *testing.T) {
	const id = "0120_drop_anthropic_model_catalog.sql"
	db := testutil.DBThrough(t, migrationBefore(t, id))
	ctx := t.Context()
	exec := func(sql string) {
		t.Helper()
		_, err := db.SQL.ExecContext(ctx, sql)
		require.NoError(t, err)
	}
	read := func(sql string) (v string) {
		t.Helper()
		require.NoError(t, db.SQL.QueryRowContext(ctx, sql).Scan(&v))
		return v
	}
	migrate := func() []string {
		t.Helper()
		done, err := store.Migrate(ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
		require.NoError(t, err)
		return done
	}
	exec(`INSERT INTO roles (key, name, superuser) VALUES ('ops', '{}', false), ('other', '{}', false);
		INSERT INTO plugins (key, name, status, builtin) VALUES ('anthropic', '{"en":"Anthropic"}', 'enabled', true), ('guard', '{"en":"Guard"}', 'enabled', false);
		INSERT INTO plugin_migrations (plugin_key, migration_id, checksum) VALUES ('anthropic', '0001_init.sql', 'c'), ('guard', '0001_init.sql', 'g');
		INSERT INTO permissions (key, module, label, source, plugin_key) VALUES
			('plugin.anthropic:model_catalog:read', 'anthropic', '{}', 'plugin', 'anthropic'), ('guard:x', 'guard', '{}', 'plugin', 'guard');
		INSERT INTO casbin_rule (ptype, v0, v1, v2) VALUES ('p', 'r:ops', 'plugin.anthropic:model_catalog:read', '*'), ('p', 'r:other', 'guard:x', '*');
		CREATE SCHEMA plg_anthropic; CREATE TABLE plg_anthropic.model_catalog (id int)`)
	revisions := `SELECT string_agg(key || '=' || revision, ' ' ORDER BY key) FROM roles WHERE key IN ('ops', 'other')`
	before, version := read(revisions), read(`SELECT policy_version::text FROM authz_meta`)

	state := func() string {
		return read(`SELECT concat_ws(' | ',
			(SELECT string_agg(key, ' ' ORDER BY key) FROM plugins),
			(SELECT string_agg(plugin_key, ' ') FROM plugin_migrations),
			(SELECT string_agg(key, ' ') FROM permissions WHERE plugin_key IS NOT NULL),
			(SELECT string_agg(v0 || ' ' || v1, ' ') FROM casbin_rule),
			(SELECT count(*)::text FROM pg_namespace WHERE nspname = 'plg_anthropic'))`)
	}
	const want = "anthropic guard | guard | guard:x | r:other guard:x | 0"
	require.Equal(t, migrationsAfter(t, migrationBefore(t, id)), migrate())
	require.Equal(t, want, state())
	after := read(revisions)
	require.NotEqual(t, before, after, "the role that held the permission keeps its revision")
	require.Equal(t, strings.Split(before, " ")[1], strings.Split(after, " ")[1], "another role moved")
	require.NotEqual(t, version, read(`SELECT policy_version::text FROM authz_meta`))

	exec(`DELETE FROM schema_migrations WHERE id = '` + id + `'`)
	version = read(`SELECT policy_version::text FROM authz_meta`)
	require.Equal(t, []string{id}, migrate())
	require.Equal(t, want, state())
	require.Equal(t, version, read(`SELECT policy_version::text FROM authz_meta`), "a rerun moved the policy")
}

// 0123 deletes the model prices of user groups and drops price_rules.
// segment_id with its indexes (CONTRACTS §97): the group's own rules stay
// (0124 makes them the group's model rates), a rule is unique by group and
// model, deleting a segment no longer touches price_rules. A rerun changes
// nothing.
func TestMigration0123DropsSegmentPriceRules(t *testing.T) {
	m := newMigrationDB(t, "0123_drop_segment_price_rules.sql")
	m.exec(`INSERT INTO groups (name) VALUES ('g1'), ('g2');
		INSERT INTO segments (name) VALUES ('vip');
		INSERT INTO price_rules (group_id, segment_id, model, kind, multiplier)
			SELECT g.id, NULL::bigint, 'm1', 'multiplier', 1.2 FROM groups g WHERE g.name = 'g1'
			UNION ALL SELECT g.id, NULL, 'm2', 'multiplier', 0 FROM groups g WHERE g.name = 'g2'
			UNION ALL SELECT g.id, s.id, m, 'multiplier', 0.5 FROM groups g, segments s, unnest(ARRAY['m1', 'm3']) m WHERE s.name = 'vip'`)
	require.Equal(t, "6", m.read(`SELECT count(*)::text FROM price_rules`))
	const want = "g1 m1 1.2000, g2 m2 0.0000 | 0 | 0 | " +
		"CREATE UNIQUE INDEX price_rules_group_model_uq ON price_rules USING btree (group_id, model)"
	m.migrateFirst()
	require.Equal(t, want, segmentRulesState(m))

	// One rule per group and model; a segment goes without price rules.
	require.ErrorContains(t, m.try(`INSERT INTO price_rules (group_id, model, mode, config, expression, expr_hash)
		SELECT id, 'm1', 'expression', '{}', 'flat(1)', 'h' FROM groups, generate_series(1, 2) WHERE name = 'g1'`), "price_rules_group_model_uq")
	m.exec(`DELETE FROM segments WHERE name = 'vip'`)

	m.rerun()
	require.Equal(t, want, segmentRulesState(m))
}

// segmentRulesState is what 0123 and 0124 make of the rules: the model
// rates of groups, how many of segments, whether price_rules.segment_id is
// there, and the indexes of price_rules.
func segmentRulesState(m *migrationDB) string {
	return m.read(`SELECT concat_ws(' | ',
		(SELECT string_agg(g.name || ' ' || r.model || ' ' || r.rate, ', ' ORDER BY g.name, r.model)
			FROM group_model_rates r JOIN groups g ON g.id = r.group_id),
		(SELECT count(*)::text FROM segment_model_rates),
		(SELECT count(*)::text FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = 'price_rules' AND column_name = 'segment_id'),
		(SELECT string_agg(replace(indexdef, schemaname || '.', ''), '; ' ORDER BY indexname) FROM pg_indexes
			WHERE schemaname = current_schema() AND tablename = 'price_rules' AND indexname <> 'price_rules_pkey'))`)
}
