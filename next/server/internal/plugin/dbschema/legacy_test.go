package dbschema_test

import (
	"context"
	"log/slog"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/dbschema"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// coreUser connects to the test database as a fresh login role that is not
// a superuser but may create roles, like the core's user on a managed
// PostgreSQL (Supabase): the legacy plugin roles were created by such a user.
func coreUser(t *testing.T, db *store.DB) (host *store.DB, dsn, name string) {
	t.Helper()
	ctx := context.Background()
	name = "core_" + randKey()
	pw := "pw-" + randKey()
	rid := store.QuoteIdent(name)
	var dbname string
	if err := db.SQL.QueryRowContext(ctx, `SELECT current_database()`).Scan(&dbname); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		"CREATE ROLE " + rid + " LOGIN CREATEROLE NOSUPERUSER PASSWORD '" + pw + "'",
		"GRANT CREATE, CONNECT ON DATABASE " + store.QuoteIdent(dbname) + " TO " + rid,
		"GRANT ALL ON ALL TABLES IN SCHEMA public TO " + rid,
		"GRANT ALL ON ALL SEQUENCES IN SCHEMA public TO " + rid,
	} {
		if _, err := db.SQL.ExecContext(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	u, err := url.Parse(testutil.DSN(db))
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(name, pw)
	host, err = store.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	var super bool
	if err := host.SQL.QueryRowContext(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&super); err != nil || super {
		t.Fatalf("core user is a superuser: %v %v", super, err)
	}
	t.Cleanup(func() {
		host.Close()
		c := context.Background()
		_, _ = db.SQL.ExecContext(c, "DROP OWNED BY "+rid)
		_, _ = db.SQL.ExecContext(c, "DROP ROLE IF EXISTS "+rid)
	})
	return host, u.String(), name
}

// legacyRole sets up plugin key the way earlier cores did, as the core's
// user: a login role marked as this core's, the core a member of it, the
// schema handed to it, and the plugin's tables created by it. It returns
// the role's DSN.
func legacyRole(t *testing.T, db, host *store.DB, hostDSN, key string) string {
	t.Helper()
	ctx := context.Background()
	role := "plg_" + key
	rid := store.QuoteIdent(role)
	var dbname string
	if err := host.SQL.QueryRowContext(ctx, `SELECT current_database()`).Scan(&dbname); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		"CREATE ROLE " + rid + " WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT 20 PASSWORD 'legacy'",
		"COMMENT ON ROLE " + rid + " IS 'sub2api plugin role; database=" + dbname + "; plugin=" + key + "'",
		"GRANT " + rid + " TO CURRENT_USER",
		"CREATE SCHEMA IF NOT EXISTS " + rid,
		"ALTER SCHEMA " + rid + " OWNER TO " + rid,
		"REVOKE ALL ON SCHEMA " + rid + " FROM PUBLIC",
		"GRANT USAGE, CREATE ON SCHEMA " + rid + " TO " + rid,
		"ALTER ROLE " + rid + " SET search_path TO " + rid,
	} {
		if _, err := host.SQL.ExecContext(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.SQL.ExecContext(c, "DROP SCHEMA IF EXISTS "+rid+" CASCADE")
		_, _ = db.SQL.ExecContext(c, "DROP OWNED BY "+rid)
		_, _ = db.SQL.ExecContext(c, "DROP ROLE IF EXISTS "+rid)
	})
	u, err := url.Parse(hostDSN)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(role, "legacy")
	conn, done, err := testutil.Connect(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `CREATE TABLE items (id serial PRIMARY KEY, v text);
		INSERT INTO items (v) VALUES ('a'), ('b');
		CREATE VIEW item_names AS SELECT v FROM items`); err != nil {
		done()
		t.Fatal(err)
	}
	done()
	// A role with a session is left alone by the conversion.
	waitNoSessions(t, host, role)
	return u.String()
}

// countingHandler counts log records by message, across managers.
type countingHandler struct {
	mu sync.Mutex
	n  map[string]int
}

func (h *countingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *countingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	h.n[r.Message]++
	h.mu.Unlock()
	return nil
}
func (h *countingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *countingHandler) WithGroup(string) slog.Handler      { return h }
func (h *countingHandler) count(msg string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.n[msg]
}

const (
	msgConverted = "converted legacy plugin database role"
	msgBusy      = "legacy plugin database role still has sessions; it is converted once they end"
)

func roleExists(t *testing.T, q *store.DB, role string) bool {
	t.Helper()
	var ok bool
	if err := q.SQL.QueryRowContext(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, role).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	return ok
}

// assertConverted checks that everything in the plugin's schema belongs to
// the core's user and that the plugin, with the DSN the core hands out,
// reads and writes its tables and creates new ones.
func assertConverted(t *testing.T, m *dbschema.Manager, host *store.DB, key, core string) {
	t.Helper()
	ctx := context.Background()
	if roleExists(t, host, "plg_"+key) {
		t.Fatalf("role plg_%s left", key)
	}
	var foreign int
	if err := host.SQL.QueryRowContext(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relowner <> (SELECT oid FROM pg_roles WHERE rolname = $2)`, "plg_"+key, core).Scan(&foreign); err != nil {
		t.Fatal(err)
	}
	var schemaOwner string
	if err := host.SQL.QueryRowContext(ctx, `SELECT nspowner::regrole::text FROM pg_namespace WHERE nspname = $1`, "plg_"+key).Scan(&schemaOwner); err != nil {
		t.Fatal(err)
	}
	if foreign != 0 || schemaOwner != core {
		t.Fatalf("objects not owned by the core: %d, schema owner %s", foreign, schemaOwner)
	}
	dsn, _, err := m.DSN(ctx, key, false)
	if err != nil {
		t.Fatal(err)
	}
	conn, done, err := testutil.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	var who string
	var n int
	if _, err := conn.ExecContext(ctx, `INSERT INTO items (v) VALUES ('c'); CREATE TABLE IF NOT EXISTS more (id int)`); err != nil {
		t.Fatalf("write as the core's user: %v", err)
	}
	if err := conn.QueryRowContext(ctx, `SELECT current_user, (SELECT count(*) FROM item_names)`).Scan(&who, &n); err != nil {
		t.Fatal(err)
	}
	if who != core || n != 3 {
		t.Fatalf("who=%s n=%d", who, n)
	}
}

// A database with the legacy roles (production, or a restore with owners):
// the role of an installed plugin is converted when its schema is ensured,
// the role of a plugin no longer installed by the start-up sweep; a
// same-named role of another database is not touched.
func TestLegacyRoleConverted(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	host, hostDSN, core := coreUser(t, db)
	installed, gone, foreign := randKey(), randKey(), randKey()
	legacyRole(t, db, host, hostDSN, installed)
	legacyRole(t, db, host, hostDSN, gone)
	frid := store.QuoteIdent("plg_" + foreign)
	if _, err := db.SQL.ExecContext(ctx, "CREATE ROLE "+frid+" LOGIN; COMMENT ON ROLE "+frid+" IS 'sub2api plugin role; database=other; plugin="+foreign+"'"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.SQL.ExecContext(context.Background(), "DROP ROLE IF EXISTS "+frid) })

	logs := &countingHandler{n: map[string]int{}}
	m := dbschema.New(host, hostDSN, slog.New(logs))
	if err := m.Ensure(ctx, installed); err != nil {
		t.Fatal(err)
	}
	assertConverted(t, m, host, installed, core)
	if !roleExists(t, host, "plg_"+gone) {
		t.Fatal("Ensure of one plugin converted another")
	}
	m.ConvertLegacyRoles(ctx)
	assertConverted(t, m, host, gone, core)
	if !roleExists(t, host, "plg_"+foreign) {
		t.Fatal("a role of another database was dropped")
	}
	if got := logs.count(msgConverted); got != 2 {
		t.Fatalf("converted %d roles, want 2", got)
	}
	// Idempotent.
	m.ConvertLegacyRoles(ctx)
	if got := logs.count(msgConverted); got != 2 {
		t.Fatalf("converted again: %d", got)
	}
}

// A new database, or one restored without owners (everything already owned
// by the core's user, no plugin roles), needs nothing.
func TestLegacyRoleAbsent(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	host, hostDSN, core := coreUser(t, db)
	key := randKey()
	sid := store.QuoteIdent("plg_" + key)
	if _, err := host.SQL.ExecContext(ctx, "CREATE SCHEMA "+sid+"; CREATE TABLE "+sid+".items (id serial PRIMARY KEY, v text); INSERT INTO "+sid+".items (v) VALUES ('a'), ('b'); CREATE VIEW "+sid+".item_names AS SELECT v FROM "+sid+".items"); err != nil {
		t.Fatal(err)
	}
	logs := &countingHandler{n: map[string]int{}}
	m := dbschema.New(host, hostDSN, slog.New(logs))
	t.Cleanup(func() { _ = m.Drop(context.Background(), key) })
	m.ConvertLegacyRoles(ctx)
	if err := m.Ensure(ctx, key); err != nil {
		t.Fatal(err)
	}
	assertConverted(t, m, host, key, core)
	if len(logs.n) != 0 {
		t.Fatalf("logged %v", logs.n)
	}
}

// Nodes starting together convert each role once, without errors.
func TestLegacyRoleConcurrentStarters(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	host, hostDSN, core := coreUser(t, db)
	keys := []string{randKey(), randKey(), randKey()}
	for _, k := range keys {
		legacyRole(t, db, host, hostDSN, k)
	}
	logs := &countingHandler{n: map[string]int{}}
	var failed atomic.Int32
	var wg sync.WaitGroup
	for range 4 {
		m := dbschema.New(host, hostDSN, slog.New(logs))
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.ConvertLegacyRoles(ctx)
			for _, k := range keys {
				if err := m.Ensure(ctx, k); err != nil {
					t.Error(err)
					failed.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if failed.Load() != 0 {
		t.FailNow()
	}
	if got := logs.count(msgConverted); got != len(keys) {
		t.Fatalf("converted %d times, want %d (logs %v)", got, len(keys), logs.n)
	}
	m := dbschema.New(host, hostDSN, nil)
	for _, k := range keys {
		assertConverted(t, m, host, k, core)
	}
}

// A role with an open session (an old plugin process) is left, logged once,
// and converted at the next Ensure after the session ends; meanwhile the old
// process keeps its tables. Purge converts the role and drops the schema.
func TestLegacyRoleBusyThenPurge(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	host, hostDSN, core := coreUser(t, db)
	key := randKey()
	roleDSN := legacyRole(t, db, host, hostDSN, key)
	old, done, err := testutil.Connect(ctx, roleDSN)
	if err != nil {
		t.Fatal(err)
	}
	logs := &countingHandler{n: map[string]int{}}
	m := dbschema.New(host, hostDSN, slog.New(logs))
	for range 2 {
		if err := m.Ensure(ctx, key); err != nil {
			t.Fatal(err)
		}
	}
	m.ConvertLegacyRoles(ctx)
	if !roleExists(t, host, "plg_"+key) || logs.count(msgBusy) != 1 {
		t.Fatalf("busy role: exists=%v logged=%d", roleExists(t, host, "plg_"+key), logs.count(msgBusy))
	}
	var n int
	if err := old.QueryRowContext(ctx, `SELECT count(*) FROM items`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("old process lost its table: %d %v", n, err)
	}
	// The core's user reaches the tables meanwhile, through its membership.
	dsn, _, err := m.DSN(ctx, key, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := readItems(ctx, dsn, &n); err != nil || n != 2 {
		t.Fatalf("core user during the overlap: %d %v", n, err)
	}
	done()
	waitNoSessions(t, host, "plg_"+key)
	if err := m.Ensure(ctx, key); err != nil {
		t.Fatal(err)
	}
	assertConverted(t, m, host, key, core)

	// Purge: a second legacy plugin, purged without an Ensure first.
	other := randKey()
	legacyRole(t, db, host, hostDSN, other)
	if err := dbschema.New(host, hostDSN, nil).Drop(ctx, other); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := host.SQL.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)
		OR EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, "plg_"+other).Scan(&exists); err != nil || exists {
		t.Fatalf("schema or role left after purge: %v %v", exists, err)
	}
}

func readItems(ctx context.Context, dsn string, n *int) error {
	conn, done, err := testutil.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer done()
	return conn.QueryRowContext(ctx, `SELECT count(*) FROM items`).Scan(n)
}

// waitNoSessions waits until the backend of a closed connection is gone
// from pg_stat_activity (closing a client does not wait for it).
func waitNoSessions(t *testing.T, q *store.DB, role string) {
	t.Helper()
	ctx := context.Background()
	for {
		var n int
		if err := q.SQL.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity WHERE usename = $1`, role).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return
		}
		if _, err := q.SQL.ExecContext(ctx, `SELECT pg_sleep(0.05)`); err != nil {
			t.Fatal(err)
		}
	}
}
