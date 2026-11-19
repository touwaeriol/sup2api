package pluginsdktest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pgdb"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pgdb/pgdbtool"
	"github.com/Sub2API-Devs/sup2api/next/sdk/testpg"
)

var schemaSeq atomic.Int64

// NewSchema creates a fresh schema in the test PostgreSQL (testpg.URL:
// TEST_DATABASE_URL, or a local server started from Go) and returns a DSN
// whose search_path is pinned to it, like the DSN the host hands out. The schema is dropped on cleanup.
func NewSchema(t testing.TB, prefix string) (dsn, schema string) {
	t.Helper()
	base := testpg.URL(t)
	if prefix == "" {
		prefix = "plg_test"
	}
	schema = fmt.Sprintf("%s_%d_%d", prefix, os.Getpid(), schemaSeq.Add(1))
	db := open(t, base)
	if err := pgdbtool.CreateSchema(context.Background(), db, schema); err != nil {
		t.Fatalf("pluginsdktest: create schema: %v", err)
	}
	// Cleanups run last in, first out: db is still open here.
	t.Cleanup(func() { _ = pgdbtool.DropSchema(context.Background(), db, schema) })
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("pluginsdktest: parse TEST_DATABASE_URL: %v", err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String(), schema
}

// ApplyMigrations runs every *.sql file of dirs (merged, sorted by file
// name) in its own transaction with search_path set to schema, like the
// host migration runner (ARCHITECTURE 5.6). Returns the applied file names.
func ApplyMigrations(t testing.TB, dsn, schema string, dirs ...string) []string {
	t.Helper()
	migrations, err := pgdbtool.ReadMigrations(dirs...)
	if err != nil {
		t.Fatalf("pluginsdktest: %v", err)
	}
	if err := pgdbtool.ApplyMigrations(context.Background(), open(t, dsn), schema, migrations); err != nil {
		t.Fatalf("pluginsdktest: %v", err)
	}
	names := make([]string, len(migrations))
	for i, m := range migrations {
		names[i] = m.Name
	}
	return names
}

// open returns a one-connection handle on dsn, closed when t ends.
func open(t testing.TB, dsn string) *gorm.DB {
	t.Helper()
	db, err := pgdb.Open(dsn, pgdb.Options{MaxConns: 1})
	if err != nil {
		t.Fatalf("pluginsdktest: %v", err)
	}
	t.Cleanup(func() { _ = pgdb.Close(db) })
	return db
}
