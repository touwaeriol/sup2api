package dbschema_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/dbschema"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

func randKey() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "ds" + hex.EncodeToString(b)
}

// The schema belongs to the core's user, the migrations run as it, and the
// DSN logs in as it with the plugin's schema as search_path.
func TestSchemaMigrateDSNDrop(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	key := randKey()
	if _, err := db.SQL.ExecContext(ctx, `INSERT INTO plugins (key, name, status) VALUES ($1, '{"en":"x"}', 'installed')`, key); err != nil {
		t.Fatal(err)
	}
	m := dbschema.New(db, testutil.DSN(db), nil)
	t.Cleanup(func() { _ = m.Drop(context.Background(), key) })

	if err := m.Ensure(ctx, key); err != nil {
		t.Fatal(err)
	}
	// Idempotent.
	if err := m.Ensure(ctx, key); err != nil {
		t.Fatal(err)
	}

	fsys := fstest.MapFS{
		"0001_init.sql": {Data: []byte(`CREATE TABLE items (id int PRIMARY KEY); INSERT INTO items VALUES (1);`)},
	}
	applied, err := m.Migrate(ctx, key, fsys)
	if err != nil || len(applied) != 1 {
		t.Fatalf("migrate: %v %v", applied, err)
	}
	if again, err := m.Migrate(ctx, key, fsys); err != nil || len(again) != 0 {
		t.Fatalf("second migrate: %v %v", again, err)
	}
	pending, err := m.Pending(ctx, key, []string{"0001_init.sql"})
	if err != nil || pending {
		t.Fatalf("pending = %v %v", pending, err)
	}
	if pending, _ := m.Pending(ctx, key, []string{"0001_init.sql", "0002.sql"}); !pending {
		t.Fatal("0002 should be pending")
	}
	var owner, schemaOwner, core string
	if err := db.SQL.QueryRowContext(ctx, `SELECT t.tableowner, n.nspowner::regrole::text, current_user FROM pg_tables t
		JOIN pg_namespace n ON n.nspname = t.schemaname WHERE t.schemaname = $1 AND t.tablename = 'items'`, "plg_"+key).Scan(&owner, &schemaOwner, &core); err != nil {
		t.Fatal(err)
	}
	if owner != core || schemaOwner != core {
		t.Fatalf("items owner = %s, schema owner = %s, core user = %s", owner, schemaOwner, core)
	}
	// A script changed after it was applied runs again (scripts are
	// idempotent) and its record takes the new checksum.
	fsys["0001_init.sql"] = &fstest.MapFile{Data: []byte(`CREATE TABLE IF NOT EXISTS items (id int PRIMARY KEY);
		INSERT INTO items VALUES (1), (2) ON CONFLICT DO NOTHING;`)}
	again, err := m.Migrate(ctx, key, fsys)
	if err != nil || len(again) != 1 || again[0] != "0001_init.sql" {
		t.Fatalf("changed script: %v %v", again, err)
	}
	if again, err := m.Migrate(ctx, key, fsys); err != nil || len(again) != 0 {
		t.Fatalf("the new checksum was not recorded: %v %v", again, err)
	}

	// The DSN logs in as the core's user, in the plugin's schema.
	dsn, schema, err := m.DSN(ctx, key, false)
	if err != nil || schema != "plg_"+key {
		t.Fatalf("dsn: %v %s", err, schema)
	}
	conn, done, err := testutil.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	var who, sp string
	var n int
	if err := conn.QueryRowContext(ctx, `SELECT current_user, current_setting('search_path'), (SELECT count(*) FROM items)`).Scan(&who, &sp, &n); err != nil {
		t.Fatal(err)
	}
	if who != core || sp != "plg_"+key || n != 2 {
		t.Fatalf("who=%s search_path=%s n=%d", who, sp, n)
	}
	done()

	if err := m.Drop(ctx, key); err != nil {
		t.Fatal(err)
	}
	var exists bool
	_ = db.SQL.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)
		OR EXISTS (SELECT 1 FROM plugin_migrations WHERE plugin_key = $2)`, "plg_"+key, key).Scan(&exists)
	if exists {
		t.Fatal("schema or migration records left after Drop")
	}
	// Ensure after Drop creates the schema again (the cache was reset).
	if err := m.Ensure(ctx, key); err != nil {
		t.Fatal(err)
	}
	_ = db.SQL.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, "plg_"+key).Scan(&exists)
	if !exists {
		t.Fatal("schema not recreated after Drop")
	}
}

// Every SQL migration shipped with an official plugin (and its test
// overlays) is idempotent: applied twice in order, the second pass changes
// nothing and fails nowhere. The core records each file once, but a lost
// record or a corrected script runs it again.
func TestOfficialPluginMigrationsAreIdempotent(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	root := filepath.Join("..", "..", "..", "..", "plugins")
	dirs, err := filepath.Glob(filepath.Join(root, "*", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	overlays, _ := filepath.Glob(filepath.Join(root, "*", "testdata", "*", "migrations"))
	if len(dirs) < 4 {
		t.Fatalf("found only %v under %s", dirs, root)
	}
	for _, dir := range dirs {
		plugin := filepath.Base(filepath.Dir(dir))
		sets := [][]string{}
		base, _ := filepath.Glob(filepath.Join(dir, "*.sql"))
		sort.Strings(base)
		sets = append(sets, base)
		for _, o := range overlays {
			if strings.Contains(o, string(filepath.Separator)+plugin+string(filepath.Separator)) {
				extra, _ := filepath.Glob(filepath.Join(o, "*.sql"))
				sort.Strings(extra)
				sets = append(sets, append(append([]string(nil), base...), extra...))
			}
		}
		for _, files := range sets {
			t.Run(plugin+"/"+strings.Join(names(files), ","), func(t *testing.T) {
				tx, err := testutil.Begin(ctx, db)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				schema := store.QuoteIdent("idem_" + randKey())
				if _, err = store.SQLTx(tx).ExecContext(ctx, "CREATE SCHEMA "+schema+"; SET LOCAL search_path TO "+schema); err != nil {
					t.Fatal(err)
				}
				for pass := 1; pass <= 2; pass++ {
					for _, f := range files {
						body, err := os.ReadFile(f)
						if err != nil {
							t.Fatal(err)
						}
						if _, err = store.SQLTx(tx).ExecContext(ctx, string(body)); err != nil {
							t.Fatalf("pass %d of %s: %v", pass, filepath.Base(f), err)
						}
					}
				}
			})
		}
	}
}

func names(files []string) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = filepath.Base(f)
	}
	return out
}
