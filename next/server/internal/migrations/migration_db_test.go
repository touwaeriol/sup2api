package migrations_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/migrations"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// migrationDB is a database through the core migration before id, to seed
// and then run id (and those after it) on.
type migrationDB struct {
	t   *testing.T
	ctx context.Context
	db  *store.DB
	id  string
}

func newMigrationDB(t *testing.T, id string) *migrationDB {
	return &migrationDB{t: t, ctx: t.Context(), db: testutil.DBThrough(t, migrationBefore(t, id)), id: id}
}

// exec runs sql, which must succeed.
func (m *migrationDB) exec(sql string) {
	m.t.Helper()
	require.NoError(m.t, m.try(sql))
}

// try runs sql and returns its error.
func (m *migrationDB) try(sql string) error {
	_, err := m.db.SQL.ExecContext(m.ctx, sql)
	return err
}

// read is the one value sql selects, as text.
func (m *migrationDB) read(sql string) (v string) {
	m.t.Helper()
	require.NoError(m.t, m.db.SQL.QueryRowContext(m.ctx, sql).Scan(&v))
	return v
}

// rows is the text of each row of sql, sorted and joined by ", ".
func (m *migrationDB) rows(sql string) string {
	m.t.Helper()
	return m.read(`SELECT coalesce(string_agg(x, ', ' ORDER BY x), '') FROM (` + sql + `) r(x)`)
}

// migrate runs the pending migrations and returns those it ran.
func (m *migrationDB) migrate() []string {
	m.t.Helper()
	done, err := store.Migrate(m.ctx, m.db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
	require.NoError(m.t, err)
	return done
}

// migrateFirst runs the migrations from id on for the first time.
func (m *migrationDB) migrateFirst() {
	m.t.Helper()
	require.Equal(m.t, migrationsAfter(m.t, migrationBefore(m.t, m.id)), m.migrate())
}

// rerun forgets id and runs it again, alone.
func (m *migrationDB) rerun() {
	m.t.Helper()
	m.exec(`DELETE FROM schema_migrations WHERE id = '` + m.id + `'`)
	require.Equal(m.t, []string{m.id}, m.migrate())
}
