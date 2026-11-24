package dbschema_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/dbschema"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// The schema objects are keyed by plugin id (CONTRACTS §137): a first-party
// plugin k and another publisher k's plugin codex (_k__codex) coexist with
// their own tables, neither's objects are strays of the other, and a purge
// of one leaves the other's. Ids that are no plugin id are refused.
func TestPluginIDsCoexist(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	host, dsn, _ := coreUser(t, db, true)
	m := newManager(t, db, host, dsn, nil)
	k := randKey()
	ids := []string{k, manifest.PluginID(k, "codex")}
	for _, id := range ids {
		withItems(t, db, m, dsn, id)
	}
	for _, id := range ids {
		if strays, err := m.Strays(ctx, id); err != nil || len(strays) > 0 {
			t.Fatalf("strays of %s: %v (%v)", id, strays, err)
		}
	}
	if err := m.Drop(ctx, ids[1], true); err != nil {
		t.Fatal(err)
	}
	wantTables(t, db, k, manifest.TablePrefix(k)+"items", manifest.TablePrefix(k)+"schema_migrations")
	for _, bad := range []string{"_sub2api__" + k, "_" + k + "__Codex", "_" + k + "_codex"} {
		if err := m.Ensure(ctx, bad); err == nil {
			t.Fatalf("Ensure(%q) accepted", bad)
		}
	}
}

// withItems installs plugin id with a table items that it writes to.
func withItems(t *testing.T, db *store.DB, m *dbschema.Manager, dsn, id string) {
	t.Helper()
	install(t, db, id, nil, false)
	if _, err := m.Migrate(context.Background(), id, scripts("0001.sql", `CREATE TABLE `+manifest.TablePrefix(id)+`items (id int)`)); err != nil {
		t.Fatalf("%s: %v", id, err)
	}
	if err := queryErr(asPlugin(t, m, dsn, id), `INSERT INTO `+manifest.TablePrefix(id)+`items VALUES (1)`); err != nil {
		t.Fatalf("%s writes its table: %v", id, err)
	}
}

// wantTables fails unless the public tables whose name holds k are want.
func wantTables(t *testing.T, db *store.DB, k string, want ...string) {
	t.Helper()
	var got string
	if err := db.SQL.QueryRowContext(context.Background(), `SELECT COALESCE(string_agg(relname, ' ' ORDER BY relname), '') FROM pg_class
		WHERE relkind = 'r' AND relnamespace = 'public'::regnamespace AND strpos(relname, $1) > 0`, k).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != strings.Join(want, " ") {
		t.Fatalf("tables = %q, want %v", got, want)
	}
}
