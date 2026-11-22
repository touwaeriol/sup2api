package migrations_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/migrations"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// 0124 makes rates replace each other (CONTRACTS §97) and keeps what every
// call paid: a discount becomes the user group's rate in each account group
// it has none in (base rate × discount), a rate it has stays; a multiplier
// rule becomes the account group's rate for the model (N × base rate) and
// each user group's with a rate there (N × that rate); price_rules keeps the
// custom prices (kind always 'custom') and loses multiplier, segments loses
// discount. A
// rerun writes no row.
func TestMigration0124ModelRates(t *testing.T) {
	const id = "0124_model_rates.sql"
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
	exec(`INSERT INTO groups (name, rate_multiplier) VALUES ('g1', 2), ('g2', 1);
		INSERT INTO segments (name, discount) VALUES ('vip', 0.8), ('plain', 1), ('own', 0.5);
		INSERT INTO segment_groups (segment_id, group_id, granted, rate_override)
			SELECT s.id, g.id, true, NULL FROM segments s, groups g WHERE (s.name, g.name) = ('vip', 'g2')
			UNION ALL SELECT s.id, g.id, false, 3 FROM segments s, groups g WHERE (s.name, g.name) = ('own', 'g1');
		INSERT INTO price_rules (group_id, model, kind, multiplier)
			SELECT id, 'm1', 'multiplier', 1.5 FROM groups WHERE name = 'g1'
			UNION ALL SELECT id, 'm2', 'multiplier', 0 FROM groups WHERE name = 'g2';
		INSERT INTO price_rules (group_id, model, kind, mode, config, expression, expr_hash)
			SELECT id, 'c1', 'custom', 'expression', '{}', 'flat(1)', 'h' FROM groups WHERE name = 'g1'`)

	require.Equal(t, migrationsAfter(t, migrationBefore(t, id)), migrate())
	rows := func(sql string) string { return read(`SELECT string_agg(x, ', ' ORDER BY x) FROM (` + sql + `) r(x)`) }
	require.Equal(t, "own g1 3.0000 f, own g2 0.5000 f, vip g1 1.6000 f, vip g2 0.8000 t",
		rows(`SELECT concat_ws(' ', s.name, g.name, sg.rate_override, sg.granted) FROM segment_groups sg
			JOIN segments s ON s.id = sg.segment_id JOIN groups g ON g.id = sg.group_id`))
	require.Equal(t, "g1 m1 3.0000, g2 m2 0.0000",
		rows(`SELECT concat_ws(' ', g.name, r.model, r.rate) FROM group_model_rates r JOIN groups g ON g.id = r.group_id`))
	require.Equal(t, "own g1 m1 4.5000, own g2 m2 0.0000, vip g1 m1 2.4000, vip g2 m2 0.0000",
		rows(`SELECT concat_ws(' ', s.name, g.name, r.model, r.rate) FROM segment_model_rates r
			JOIN segments s ON s.id = r.segment_id JOIN groups g ON g.id = r.group_id`))
	require.Equal(t, "g1 c1 flat(1)", rows(`SELECT concat_ws(' ', g.name, r.model, r.expression) FROM price_rules r JOIN groups g ON g.id = r.group_id`))
	require.Equal(t, "0", read(`SELECT count(*)::text FROM information_schema.columns WHERE table_schema = current_schema()
		AND (table_name, column_name) IN (('price_rules', 'multiplier'), ('segments', 'discount'))`))
	require.Equal(t, "custom", rows(`SELECT DISTINCT kind FROM price_rules`))

	snapshot := func() string {
		t.Helper()
		return rows(`SELECT t || ': ' || x FROM (
			SELECT 'segment_groups', (s.*, s.xmin)::text FROM segment_groups s
			UNION ALL SELECT 'group_model_rates', (r.*, r.xmin)::text FROM group_model_rates r
			UNION ALL SELECT 'segment_model_rates', (r.*, r.xmin)::text FROM segment_model_rates r
			UNION ALL SELECT 'price_rules', (r.*, r.xmin)::text FROM price_rules r) u(t, x)`)
	}
	before := snapshot()
	exec(`DELETE FROM schema_migrations WHERE id = '` + id + `'`)
	require.Equal(t, []string{id}, migrate())
	require.Equal(t, before, snapshot())
}
