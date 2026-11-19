// Package testutil provides shared test infrastructure.
package testutil

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/testpg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/authz/casbinrule"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/migrations"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

var (
	dbSeq atomic.Int64

	tplOnce sync.Once
	tplName string
	tplErr  error
)

// DB returns a fresh, fully migrated database for one test, created inside
// the test PostgreSQL (testpg.URL: TEST_DATABASE_URL when set, otherwise a
// local server started from Go and shared by every test process).
//
// Databases are cloned from a template (tpl_<hash of core migrations>) that
// is built once and shared by every test process, so each test pays for a
// CREATE DATABASE instead of running all migrations.
func DB(t testing.TB) *store.DB {
	t.Helper()
	base := testpg.URL(t)
	tplOnce.Do(func() { tplName, tplErr = ensureTemplate(context.Background(), base, migrations.FS) })
	if tplErr != nil {
		t.Fatalf("test template database: %v", tplErr)
	}
	return cloneTemplate(t, base, tplName)
}

// DBThrough returns a fresh database migrated through the core migration
// last and no further: the schema a deployed release left, for testing the
// migrations after it. Its template is built once per migration set, like
// DB's.
func DBThrough(t testing.TB, last string) *store.DB {
	t.Helper()
	base := testpg.URL(t)
	name, err := ensureTemplate(context.Background(), base, MigrationsThrough(t, last))
	if err != nil {
		t.Fatalf("test template database through %s: %v", last, err)
	}
	return cloneTemplate(t, base, name)
}

// MigrationsThrough is the core migrations through last and no further.
func MigrationsThrough(t testing.TB, last string) fs.FS {
	t.Helper()
	upTo := fstest.MapFS{}
	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if n > last {
			continue
		}
		b, err := fs.ReadFile(migrations.FS, n)
		if err != nil {
			t.Fatal(err)
		}
		upTo[n] = &fstest.MapFile{Data: b}
	}
	if _, ok := upTo[last]; !ok {
		t.Fatalf("no core migration %s", last)
	}
	return upTo
}

// cloneTemplate creates a database for one test from the template tpl.
func cloneTemplate(t testing.TB, base, tpl string) *store.DB {
	t.Helper()
	ctx := context.Background()
	admin, done, err := Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect TEST_DATABASE_URL: %v", err)
	}
	defer done()

	name := fmt.Sprintf("t_%d_%d", os.Getpid(), dbSeq.Add(1))
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name+" TEMPLATE "+tpl); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() {
		c, done, err := Connect(context.Background(), base)
		if err != nil {
			return
		}
		defer done()
		_, _ = c.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})

	dsn, err := withDatabase(base, name)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	dsns.Store(db, dsn)
	t.Cleanup(func() {
		dsns.Delete(db)
		db.Close()
	})
	// Registered last, so it runs first; on a connection of its own, as a
	// test may have closed db.
	t.Cleanup(func() { checkCasbinOrphans(t, dsn) })
	return db
}

// checkCasbinOrphans fails t when casbin_rule holds a row naming a user,
// role or permission the test left the database dsn without (orphansSQL):
// no foreign key cascades to it, so every delete path clears its rows
// itself, which every DB test thereby looks for. A database without the
// table (DBThrough an older migration) is not checked.
func checkCasbinOrphans(t testing.TB, dsn string) {
	const failed = "%s orphan check: %v"
	ctx := context.Background()
	conn, done, err := Connect(ctx, dsn)
	if err != nil {
		t.Errorf(failed, casbinrule.Table, err)
		return
	}
	defer done()
	var table bool
	if err := conn.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, casbinrule.Table).Scan(&table); err != nil || !table {
		if err != nil {
			t.Errorf(failed, casbinrule.Table, err)
		}
		return
	}
	rows, err := conn.QueryContext(ctx, orphansSQL)
	if err != nil {
		t.Errorf(failed, casbinrule.Table, err)
		return
	}
	orphans, err := Column[string](rows)
	if err != nil {
		t.Errorf(failed, casbinrule.Table, err)
		return
	}
	if len(orphans) > 0 {
		t.Errorf("%s rows name a deleted user, role or permission (a delete path that does not clear them):\n  %s", casbinrule.Table, strings.Join(orphans, "\n  "))
	}
}

// dsns maps the databases of DB and DBThrough to their DSNs.
var dsns sync.Map

// DSN is the connection string of db, a database of DB or DBThrough, for a
// test that connects to it another way (a second pool).
func DSN(db *store.DB) string {
	dsn, ok := dsns.Load(db)
	if !ok {
		panic("testutil.DSN: not a database of testutil.DB")
	}
	return dsn.(string)
}

// Connect opens one connection to dsn, for statements that need a session
// of their own (CREATE DATABASE, a session advisory lock); done closes it.
func Connect(ctx context.Context, dsn string) (conn *sql.Conn, done func(), err error) {
	db, err := store.OpenSized(ctx, dsn, 1)
	if err != nil {
		return nil, nil, err
	}
	if conn, err = db.SQL.Conn(ctx); err != nil {
		db.Close()
		return nil, nil, err
	}
	return conn, func() { _ = conn.Close(); db.Close() }, nil
}

// Column reads the single column of every row of rows into a slice, never
// nil, and closes rows.
func Column[T any](rows *sql.Rows) ([]T, error) {
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var v T
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Begin opens a transaction that the test ends itself (Commit, Rollback),
// for interleaving it with others; store.DB.Tx is the production way.
func Begin(ctx context.Context, db *store.DB) (*gorm.DB, error) {
	tx := db.Gorm.WithContext(ctx).Begin()
	return tx, tx.Error
}

// ensureTemplate builds the template database migrated with fsys unless
// another process already did. A template is only valid once datistemplate
// is set, which happens after migrations succeed; a half-built one is
// rebuilt.
func ensureTemplate(ctx context.Context, base string, fsys fs.FS) (string, error) {
	sum, err := migrationsHash(fsys)
	if err != nil {
		return "", err
	}
	name := "tpl_" + sum[:16]
	lockKey := int64(binary.BigEndian.Uint64([]byte(sum[:8])))

	admin, done, err := Connect(ctx, base)
	if err != nil {
		return "", err
	}
	defer done()
	if _, err := admin.ExecContext(ctx, "SELECT pg_advisory_lock($1)", lockKey); err != nil {
		return "", err
	}
	defer admin.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", lockKey) //nolint:errcheck

	var isTemplate *bool
	err = admin.QueryRowContext(ctx, "SELECT datistemplate FROM pg_database WHERE datname = $1", name).Scan(&isTemplate)
	switch {
	case err == nil && isTemplate != nil && *isTemplate:
		return name, nil
	case err == nil:
		if _, err := admin.ExecContext(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			return "", err
		}
	case !store.IsNoRows(err):
		return "", err
	}

	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		return "", err
	}
	dsn, err := withDatabase(base, name)
	if err != nil {
		return "", err
	}
	db, err := store.Open(ctx, dsn)
	if err != nil {
		return "", err
	}
	_, err = store.Migrate(ctx, db, fsys, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
	db.Close()
	if err != nil {
		return "", fmt.Errorf("migrate template: %w", err)
	}
	if _, err := admin.ExecContext(ctx, "ALTER DATABASE "+name+" IS_TEMPLATE true"); err != nil {
		return "", err
	}
	return name, nil
}

// ExecWithoutTrigger runs sql with trigger on table disabled, to set up a
// state the trigger refuses, as a database from before it may hold.
func ExecWithoutTrigger(t testing.TB, db *store.DB, table, trigger, query string, args ...any) {
	t.Helper()
	ctx := context.Background()
	toggle := func(tx *sql.Tx, state string) error {
		_, err := tx.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s %s TRIGGER %s", table, state, trigger))
		return err
	}
	err := db.Tx(ctx, func(g *gorm.DB) error {
		tx := store.SQLTx(g)
		if err := toggle(tx, "DISABLE"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
		return toggle(tx, "ENABLE")
	})
	if err != nil {
		t.Fatalf("exec without trigger %s: %v", trigger, err)
	}
}

func migrationsHash(fsys fs.FS) (string, error) {
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return "", err
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		b, err := fs.ReadFile(fsys, n)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", n, len(b))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func withDatabase(base, name string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u.Path = "/" + name
	return u.String(), nil
}
