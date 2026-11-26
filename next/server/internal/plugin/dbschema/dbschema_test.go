package dbschema_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/dbschema"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// A plugin's migrations run as its identity, in the core's schema, under
// its prefix; afterwards the identity reads and writes its own tables and
// nothing else, and creates nothing.
func TestIsolation(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	host, dsn, core := coreUser(t, db, true)
	m := newManager(t, db, host, dsn, nil)
	key, other := randKey(), randKey()
	install(t, db, key, nil, false)
	install(t, db, other, nil, false)
	p := manifest.TablePrefix(key)
	op := manifest.TablePrefix(other)

	applied, err := m.Migrate(ctx, key, scripts(
		"0001_init.sql", `CREATE TABLE `+p+`items (id serial PRIMARY KEY, v text); INSERT INTO `+p+`items (v) VALUES ('a');
			SELECT current_user;`,
	))
	if err != nil || len(applied) != 1 {
		t.Fatalf("migrate: %v %v", applied, err)
	}
	if _, err := m.Migrate(ctx, other, scripts("0001_init.sql", `CREATE TABLE `+op+`secrets (v text); INSERT INTO `+op+`secrets VALUES ('s')`)); err != nil {
		t.Fatal(err)
	}
	if again, err := m.Migrate(ctx, key, scripts("0001_init.sql", `CREATE TABLE `+p+`items (id serial PRIMARY KEY, v text); INSERT INTO `+p+`items (v) VALUES ('a');
			SELECT current_user;`)); err != nil || len(again) != 0 {
		t.Fatalf("second migrate: %v %v", again, err)
	}
	if pending, err := m.Pending(ctx, key, []string{"0001_init.sql"}); err != nil || pending {
		t.Fatalf("pending = %v %v", pending, err)
	}
	if pending, _ := m.Pending(ctx, key, []string{"0001_init.sql", "0002.sql"}); !pending {
		t.Fatal("0002 should be pending")
	}

	// The tables, their sequence and the records belong to the identity;
	// it has no CREATE after the migration; the records are its own table,
	// not plugin_migrations.
	role := m.Identity(key)
	var owners []string
	rows, err := db.SQL.QueryContext(ctx, `SELECT DISTINCT relowner::regrole::text FROM pg_class WHERE relnamespace = 'public'::regnamespace AND starts_with(relname, $1)`, p)
	if err != nil {
		t.Fatal(err)
	}
	if owners, err = testutil.Column[string](rows); err != nil || len(owners) != 1 || owners[0] != role {
		t.Fatalf("owners of the plugin's objects: %v %v (identity %s)", owners, err, role)
	}
	var canCreate, records bool
	if err := db.SQL.QueryRowContext(ctx, `SELECT has_schema_privilege($1, 'public', 'CREATE'),
		EXISTS (SELECT 1 FROM plugin_migrations WHERE plugin_key = $2)`, role, key).Scan(&canCreate, &records); err != nil || canCreate || records {
		t.Fatalf("CREATE after the migration %v, records in plugin_migrations %v: %v", canCreate, records, err)
	}

	pl := asPlugin(t, m, dsn, key)
	var who, sp string
	var n int
	if err := pl.SQL.QueryRowContext(ctx, `INSERT INTO `+p+`items (v) VALUES ('b') RETURNING current_user, current_setting('search_path'), (SELECT count(*) FROM `+p+`items)`).Scan(&who, &sp, &n); err != nil || who != role {
		t.Fatalf("own table as %s: %v", who, err)
	}
	for _, q := range []string{
		`SELECT * FROM users`,
		`UPDATE users SET status = status`,
		`INSERT INTO plugins (key, name, status) VALUES ('x', '{}', 'installed')`,
		`SELECT * FROM ` + op + `secrets`,
		`DELETE FROM ` + op + `secrets`,
		`CREATE TABLE ` + p + `later (id int)`,
		`CREATE TEMP TABLE scratch (id int) ON COMMIT DROP; CREATE TABLE public.` + p + `x (id int)`,
		`ALTER TABLE users ADD COLUMN x int`,
		`SET ROLE "` + core + `"`,
	} {
		if err := queryErr(pl, q); err == nil {
			t.Fatalf("the plugin's identity ran %s", q)
		}
	}
}

// A script that misses the prefix on a core table is refused by the
// database; one that leaves anything but prefixed relations and types - an
// object without the prefix (also in a subtransaction), a function, an
// operator, a collation, a text search configuration, a trigger - fails,
// and nothing of it is left, also when it commits its transaction halfway.
// CREATE is taken back either way.
func TestMigratePrefixCheck(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	host, dsn, _ := coreUser(t, db, true)
	m := newManager(t, db, host, dsn, nil)
	key := randKey()
	install(t, db, key, nil, false)
	p := manifest.TablePrefix(key)
	const refused = "plugin migrations may create only"
	for _, c := range []struct{ name, sql, want string }{
		{"core table", `CREATE TABLE ` + p + `a (id int); ALTER TABLE users ADD COLUMN x int`, "must be owner of table users"},
		{"no prefix", `CREATE TABLE ` + p + `a (id int); CREATE TABLE items (id int)`, "table items"},
		{"index", `CREATE TABLE ` + p + `a (id int); CREATE INDEX a_id ON ` + p + `a (id)`, "index a_id"},
		{"primary key", `CREATE TABLE ` + p + `a (id int, CONSTRAINT a_pk PRIMARY KEY (id))`, "constraint a_pk"},
		{"subtransaction", `CREATE TABLE ` + p + `a (id int); DO $$ BEGIN CREATE TABLE sneaky (id int); EXCEPTION WHEN others THEN NULL; END $$`, "sneaky"},
		{"function", `CREATE TABLE ` + p + `a (id int); CREATE FUNCTION f() RETURNS int LANGUAGE sql AS 'SELECT 1'`, "function f()"},
		{"prefixed function", `CREATE FUNCTION ` + p + `one() RETURNS int LANGUAGE sql AS 'SELECT 1'`, refused},
		{"operator", `CREATE OPERATOR === (leftarg = text, rightarg = text, function = texteq)`, "operator ===(text,text)"},
		{"collation", `CREATE COLLATION ` + p + `c (locale = 'C')`, "collation"},
		{"text search", `CREATE TEXT SEARCH CONFIGURATION ` + p + `ts (copy = simple)`, "text search configuration"},
		{"trigger", `CREATE TABLE ` + p + `a (id int); CREATE TRIGGER ` + p + `t BEFORE UPDATE ON ` + p + `a FOR EACH ROW EXECUTE FUNCTION suppress_redundant_updates_trigger()`, "trigger"},
		{"type", `CREATE TABLE ` + p + `a (id int); CREATE TYPE mood AS ENUM ('a')`, "type mood"},
		{"rename", `CREATE TABLE ` + p + `a (id int); ALTER TABLE ` + p + `a RENAME TO a`, "table a"},
		{"commit escape", `CREATE TABLE ` + p + `a (id int); COMMIT; CREATE OPERATOR === (leftarg = text, rightarg = text, function = texteq);
			CREATE TABLE items (id int); BEGIN`, refused},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := m.Migrate(ctx, key, scripts("0001.sql", c.sql))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("migrate: %v, want %q", err, c.want)
			}
			strays, err := m.Strays(ctx, key)
			if err != nil || len(strays) > 0 {
				t.Fatalf("left %v (%v)", strays, err)
			}
			var left int
			var canCreate bool
			if err := db.SQL.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM pg_class WHERE relname IN ('items', 'sneaky', 'a', 'a_id', 'a_pk'))
				+ (SELECT count(*) FROM pg_proc WHERE proname IN ('f', $1 || 'one')) + (SELECT count(*) FROM pg_type WHERE typname = 'mood')
				+ (SELECT count(*) FROM pg_operator WHERE oprname = '==='),
				has_schema_privilege($2, 'public', 'CREATE')`, p, m.Identity(key)).Scan(&left, &canCreate); err != nil {
				t.Fatal(err)
			}
			if left != 0 || canCreate {
				t.Fatalf("left %d objects, CREATE %v", left, canCreate)
			}
		})
	}
	// Prefixed relations and types of every kind pass; the records table
	// carries the prefix too. The table a script committed before its
	// failure is still there, and the next run goes on from it.
	if _, err := m.Migrate(ctx, key, scripts("0001.sql", `CREATE TABLE IF NOT EXISTS `+p+`a (id int);
		ALTER TABLE `+p+`a ADD COLUMN IF NOT EXISTS v text CHECK (v <> ''), ADD PRIMARY KEY (id);
		CREATE INDEX `+p+`a_v ON `+p+`a (v); CREATE VIEW `+p+`names AS SELECT v FROM `+p+`a;
		CREATE TYPE `+p+`mood AS ENUM ('a'); CREATE TYPE `+p+`span AS RANGE (subtype = int4);
		CREATE TYPE `+p+`pair AS (a int, b int); CREATE DOMAIN `+p+`pos AS int CHECK (VALUE > 0);
		CREATE TABLE `+p+`parts (id int, m `+p+`mood) PARTITION BY RANGE (id);
		CREATE TABLE `+p+`parts_1 PARTITION OF `+p+`parts FOR VALUES FROM (0) TO (10)`)); err != nil {
		t.Fatal(err)
	}
	if strays, err := m.Strays(ctx, key); err != nil || len(strays) > 0 {
		t.Fatalf("strays after a valid migration: %v (%v)", strays, err)
	}
}

// What a plugin process adds to its own tables at run time (a trigger, a
// policy) goes at its next Ensure.
func TestEnsureRemovesStrays(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	host, dsn, _ := coreUser(t, db, true)
	m := newManager(t, db, host, dsn, nil)
	key := randKey()
	install(t, db, key, nil, false)
	p := manifest.TablePrefix(key)
	if _, err := m.Migrate(ctx, key, scripts("0001.sql", `CREATE TABLE `+p+`a (id int)`)); err != nil {
		t.Fatal(err)
	}
	pl := asPlugin(t, m, dsn, key)
	for _, q := range []string{
		`CREATE TRIGGER t BEFORE UPDATE ON ` + p + `a FOR EACH ROW EXECUTE FUNCTION suppress_redundant_updates_trigger()`,
		`CREATE POLICY pol ON ` + p + `a USING (true)`,
	} {
		if err := queryErr(pl, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if strays, _ := m.Strays(ctx, key); len(strays) != 2 {
		t.Fatalf("strays = %v", strays)
	}
	if err := m.EnsureAgain(ctx, key); err != nil {
		t.Fatal(err)
	}
	if strays, err := m.Strays(ctx, key); err != nil || len(strays) > 0 {
		t.Fatalf("strays after Ensure: %v (%v)", strays, err)
	}
	if err := queryErr(pl, `INSERT INTO `+p+`a VALUES (1)`); err != nil {
		t.Fatalf("the plugin's table after Ensure: %v", err)
	}
}

// The tables and columns a plugin was granted (db.core_read) are readable
// as asked, insert included (with the sequence nextval needs); the rest is
// not. A revocation of the grant takes effect in its transaction.
func TestAccess(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	host, dsn, _ := coreUser(t, db, true)
	m := newManager(t, db, host, dsn, nil)
	key := randKey()
	install(t, db, key, []manifest.DatabaseAccess{
		{Table: "users", Columns: []string{"id", "email"}, Privileges: []string{"select"}},
		{Table: "audit_logs", Privileges: []string{"select", "insert"}},
	}, true)
	if errs, err := m.CheckAccess(ctx, []manifest.DatabaseAccess{
		{Table: "users", Columns: []string{"id", "nope"}}, {Table: "nope"}, {Table: "groups"},
	}); err != nil || len(errs) != 2 || errs[0].Field != "database.access[0].columns[1]" || errs[1].Field != "database.access[1].table" {
		t.Fatalf("CheckAccess: %+v %v", errs, err)
	}
	if err := m.Ensure(ctx, key); err != nil {
		t.Fatal(err)
	}
	pl := asPlugin(t, m, dsn, key)
	for _, q := range []string{
		`SELECT id, email FROM users`,
		`SELECT * FROM audit_logs`,
		`INSERT INTO audit_logs (action, target_type, target_id) VALUES ('x', 'y', 'z')`,
	} {
		if err := queryErr(pl, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, q := range []string{
		`SELECT password_hash FROM users`,
		`SELECT * FROM users`,
		`UPDATE users SET email = email`,
		`DELETE FROM audit_logs`,
		`SELECT * FROM groups`,
	} {
		if err := queryErr(pl, q); err == nil {
			t.Fatalf("the plugin ran %s", q)
		}
	}
	// Idempotent: nothing to change, nothing written.
	if _, err := m.Migrate(ctx, key, nil); err != nil {
		t.Fatal(err)
	}
	// The revocation of the grant (install.RevokeGrant) syncs in its
	// transaction.
	if err := host.Tx(ctx, func(tx *gorm.DB) error {
		if err := tx.Exec(`UPDATE plugin_permission_grants SET status = 'revoked' WHERE plugin_key = ?`, key).Error; err != nil {
			return err
		}
		return m.SyncAccessTx(ctx, tx, key)
	}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`SELECT id FROM users`, `SELECT * FROM audit_logs`} {
		if err := queryErr(pl, q); err == nil {
			t.Fatalf("after the revocation the plugin ran %s", q)
		}
	}
}

// Ensure brings a damaged database side back: a dropped identity is created
// again and gets its tables back, a table handed to the core (a restore
// without owners) returns to the identity.
func TestEnsureRecovers(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	host, dsn, core := coreUser(t, db, true)
	m := newManager(t, db, host, dsn, nil)
	key := randKey()
	install(t, db, key, nil, false)
	p := manifest.TablePrefix(key)
	if _, err := m.Migrate(ctx, key, scripts("0001.sql", `CREATE TABLE `+p+`items (id serial PRIMARY KEY, v text); INSERT INTO `+p+`items (v) VALUES ('a')`)); err != nil {
		t.Fatal(err)
	}
	role := store.QuoteIdent(m.Identity(key))
	owner := func() string {
		var o string
		if err := db.SQL.QueryRowContext(ctx, `SELECT relowner::regrole::text FROM pg_class WHERE relname = $1`, p+"items").Scan(&o); err != nil {
			t.Fatal(err)
		}
		return o
	}
	check := func() {
		t.Helper()
		if _, err := m.Migrate(ctx, key, nil); err != nil {
			t.Fatal(err)
		}
		if o := owner(); o != m.Identity(key) {
			t.Fatalf("owner %s", o)
		}
		var n int
		if err := asPlugin(t, m, dsn, key).SQL.QueryRowContext(ctx, `SELECT count(*) FROM `+p+`items`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("read as the identity: %d %v", n, err)
		}
	}
	// The table handed to the core.
	if _, err := db.SQL.ExecContext(ctx, `ALTER TABLE `+p+`items OWNER TO "`+core+`"`); err != nil {
		t.Fatal(err)
	}
	check()
	// The identity dropped, its tables kept.
	if _, err := db.SQL.ExecContext(ctx, `REASSIGN OWNED BY `+role+` TO "`+core+`"; DROP OWNED BY `+role+`; DROP ROLE `+role); err != nil {
		t.Fatal(err)
	}
	check()
}

// Without CREATEROLE the core cannot give a plugin its identity: the plugin
// does not get a database.
func TestNoCreateRole(t *testing.T) {
	db := testutil.DB(t)
	host, dsn, _ := coreUser(t, db, false)
	m := newManager(t, db, host, dsn, nil)
	key := randKey()
	if _, _, err := m.DSN(context.Background(), key); !errors.Is(err, dbschema.ErrIsolationUnavailable) {
		t.Fatalf("DSN: %v", err)
	}
	if _, err := m.Migrate(context.Background(), key, scripts("0001.sql", "SELECT 1")); !errors.Is(err, dbschema.ErrIsolationUnavailable) {
		t.Fatalf("Migrate: %v", err)
	}
}

// Uninstall keeping the data leaves the tables to the core and drops the
// identity; a reinstall gets them back with their records. Uninstall with
// purge drops everything of the plugin's, also after a keeping one.
func TestDrop(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	host, dsn, core := coreUser(t, db, true)
	m := newManager(t, db, host, dsn, nil)
	key := randKey()
	install(t, db, key, []manifest.DatabaseAccess{{Table: "users", Columns: []string{"id"}, Privileges: []string{"select"}}}, true)
	p := manifest.TablePrefix(key)
	fsys := scripts("0001.sql", `CREATE TYPE `+p+`mood AS ENUM ('a', 'b');
		CREATE TABLE `+p+`items (id serial PRIMARY KEY, v text, m `+p+`mood NOT NULL DEFAULT 'a'); INSERT INTO `+p+`items (v) VALUES ('a');
		CREATE TABLE `+p+`parts (id int) PARTITION BY RANGE (id); CREATE TABLE `+p+`parts_1 PARTITION OF `+p+`parts FOR VALUES FROM (0) TO (10)`)
	if _, err := m.Migrate(ctx, key, fsys); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.ExecContext(ctx, `INSERT INTO plugin_migrations (plugin_key, migration_id, checksum) VALUES ($1, 'data:x', 'data')`, key); err != nil {
		t.Fatal(err)
	}
	// owner is the owner of the table, the enum type and the partition,
	// when they agree.
	state := func() (role bool, objects int, owner string, records int) {
		t.Helper()
		if err := db.SQL.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1),
			(SELECT count(*) FROM pg_class WHERE relnamespace = 'public'::regnamespace AND starts_with(relname, $2))
				+ (SELECT count(*) FROM pg_type WHERE typname = $2 || 'mood'),
			COALESCE((SELECT CASE WHEN count(DISTINCT o) = 1 THEN min(o) ELSE 'mixed' END FROM (
				SELECT relowner::regrole::text AS o FROM pg_class WHERE relname IN ($2 || 'items', $2 || 'parts_1')
				UNION ALL SELECT typowner::regrole::text FROM pg_type WHERE typname = $2 || 'mood') AS x), ''),
			(SELECT count(*) FROM plugin_migrations WHERE plugin_key = $3)`, m.Identity(key), p, key).Scan(&role, &objects, &owner, &records); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := m.Drop(ctx, key, false); err != nil {
		t.Fatal(err)
	}
	if role, objects, owner, records := state(); role || objects == 0 || owner != core || records != 1 {
		t.Fatalf("kept: role %v, %d objects, owner %s, %d core records", role, objects, owner, records)
	}
	// Reinstall: the records say the script ran, the table is the identity's.
	applied, err := m.Migrate(ctx, key, fsys)
	if err != nil || len(applied) != 0 {
		t.Fatalf("reinstall: %v %v", applied, err)
	}
	if role, _, owner, _ := state(); !role || owner != m.Identity(key) {
		t.Fatalf("reinstalled: role %v owner %s", role, owner)
	}
	var n int
	pl := asPlugin(t, m, dsn, key)
	if err := pl.SQL.QueryRowContext(ctx, `SELECT count(*) FROM `+p+`items, users`).Scan(&n); err != nil {
		t.Fatalf("read after the reinstall: %v", err)
	}
	if err := queryErr(pl, `INSERT INTO `+p+`items (v, m) VALUES ('b', 'b'); INSERT INTO `+p+`parts VALUES (1)`); err != nil {
		t.Fatalf("write after the reinstall: %v", err)
	}
	// Purge after a keeping uninstall: the tables are the core's then.
	if err := m.Drop(ctx, key, false); err != nil {
		t.Fatal(err)
	}
	if err := m.Drop(ctx, key, true); err != nil {
		t.Fatal(err)
	}
	if role, objects, _, records := state(); role || objects != 0 || records != 0 {
		t.Fatalf("purged: role %v, %d objects, %d core records", role, objects, records)
	}
	// Purge of an installed plugin: the identity's grants on core tables go.
	if _, err := m.Migrate(ctx, key, fsys); err != nil {
		t.Fatal(err)
	}
	if err := m.Drop(ctx, key, true); err != nil {
		t.Fatal(err)
	}
	if role, objects, _, _ := state(); role || objects != 0 {
		t.Fatalf("purged: role %v, %d objects", role, objects)
	}
}

// An uninstall that keeps the data, on another node, drops the identity
// this node has ensured: the reinstall's reads of the records (upload,
// review) and the plugin's DSN on this node still get the identity, with
// the kept tables handed back to it.
func TestKeepDataReinstallOnAnotherNode(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	host, dsn, _ := coreUser(t, db, true)
	a := newManager(t, db, host, dsn, nil)
	b, err := a.Peer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	key := randKey()
	install(t, db, key, nil, false)
	p := manifest.TablePrefix(key)
	if _, err := a.Migrate(ctx, key, scripts("0001.sql", `CREATE TABLE `+p+`items (v text)`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.DSN(ctx, key); err != nil {
		t.Fatal(err)
	}
	writes := func(step string) {
		t.Helper()
		if err := queryErr(asPlugin(t, b, dsn, key), `INSERT INTO `+p+`items VALUES ('x')`); err != nil {
			t.Fatalf("%s: write as the identity: %v", step, err)
		}
	}
	if err := a.Drop(ctx, key, false); err != nil {
		t.Fatal(err)
	}
	if applied, err := b.AppliedMigrations(ctx, key); err != nil || len(applied) != 1 {
		t.Fatalf("records on the other node: %v %v", applied, err)
	}
	writes("after reading the records")
	if err := a.Drop(ctx, key, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.DSN(ctx, key); err != nil {
		t.Fatalf("DSN on the other node: %v", err)
	}
	writes("after the DSN")
}

// An uninstall that keeps the data keeps only the data: a view or a rule
// of the plugin's that the core owned would run with the core's rights,
// and the plugin's grants on it would lend them to every role. Ensure
// takes the plugin's grants back too.
func TestKeepDataKeepsOnlyData(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	host, dsn, _ := coreUser(t, db, true)
	m := newManager(t, db, host, dsn, nil)
	if _, err := host.SQL.ExecContext(ctx, `CREATE TABLE core_secret (v text); INSERT INTO core_secret VALUES ('secret')`); err != nil {
		t.Fatal(err)
	}
	a, b := randKey(), randKey()
	install(t, db, a, nil, false)
	install(t, db, b, nil, false)
	p := manifest.TablePrefix(a)
	fsys := scripts("0001.sql", `CREATE TABLE `+p+`items (id int PRIMARY KEY, v text); INSERT INTO `+p+`items VALUES (1, 'x');
		CREATE VIEW `+p+`leak AS SELECT v FROM core_secret`)
	if _, err := m.Migrate(ctx, a, fsys); err != nil {
		t.Fatal(err)
	}
	pa := asPlugin(t, m, dsn, a)
	// What plugin a can do to its own objects at run time.
	leave := func() {
		t.Helper()
		if err := queryErr(pa, `GRANT ALL ON `+p+`leak, `+p+`items TO PUBLIC; GRANT SELECT (v) ON `+p+`items TO PUBLIC;
			CREATE RULE `+p+`r AS ON UPDATE TO `+p+`items DO ALSO UPDATE core_secret SET v = 'pwned';
			ALTER TABLE `+p+`items ENABLE ROW LEVEL SECURITY; CREATE POLICY `+p+`p ON `+p+`items USING (true)`); err != nil {
			t.Fatal(err)
		}
	}
	// left counts the privileges on a's objects that their owner does
	// not hold and the rules, policies and views of a's prefix.
	left := func() (grants, code int) {
		t.Helper()
		if err := db.SQL.QueryRowContext(ctx, `SELECT
			(SELECT count(*) FROM pg_class c, aclexplode(c.relacl) a WHERE starts_with(c.relname, $1) AND a.grantee <> c.relowner)
			+ (SELECT count(*) FROM pg_attribute t JOIN pg_class c ON c.oid = t.attrelid, aclexplode(t.attacl) a
				WHERE starts_with(c.relname, $1) AND a.grantee <> c.relowner),
			(SELECT count(*) FROM pg_rewrite r JOIN pg_class c ON c.oid = r.ev_class WHERE starts_with(c.relname, $1) AND r.rulename <> '_RETURN')
			+ (SELECT count(*) FROM pg_policy o JOIN pg_class c ON c.oid = o.polrelid WHERE starts_with(c.relname, $1))
			+ (SELECT count(*) FROM pg_class c WHERE starts_with(c.relname, $1) AND c.relkind IN ('v', 'm'))`, p).Scan(&grants, &code); err != nil {
			t.Fatal(err)
		}
		return
	}
	leave()
	if err := m.EnsureAgain(ctx, a); err != nil {
		t.Fatal(err)
	}
	if grants, code := left(); grants != 0 || code != 1 { // the view stays the plugin's
		t.Fatalf("after Ensure: %d grants, %d rules, policies and views", grants, code)
	}
	// Between two Ensures the plugin can do it again; the uninstall comes.
	leave()
	if err := m.Drop(ctx, a, false); err != nil {
		t.Fatal(err)
	}
	if grants, code := left(); grants != 0 || code != 0 {
		t.Fatalf("kept: %d grants, %d rules, policies and views", grants, code)
	}
	if err := m.Ensure(ctx, b); err != nil {
		t.Fatal(err)
	}
	pb := asPlugin(t, m, dsn, b)
	var v string
	if err := pb.SQL.QueryRowContext(ctx, `SELECT v FROM `+p+`leak`).Scan(&v); err == nil {
		t.Fatalf("plugin b reads core_secret through a's view: %s", v)
	}
	for _, q := range []string{`SELECT v FROM ` + p + `items`, `SELECT v FROM ` + p + `items WHERE id = 1`, `UPDATE ` + p + `items SET v = 'y'`} {
		if err := queryErr(pb, q); err == nil {
			t.Fatalf("plugin b ran %q on a's table", q)
		}
	}
	if err := db.SQL.QueryRowContext(ctx, `SELECT v FROM core_secret`).Scan(&v); err != nil || v != "secret" {
		t.Fatalf("core_secret: %q %v", v, err)
	}
	// The reinstall gets the data back, and only the data.
	if applied, err := m.Migrate(ctx, a, fsys); err != nil || len(applied) != 0 {
		t.Fatalf("reinstall: %v %v", applied, err)
	}
	pa = asPlugin(t, m, dsn, a)
	if err := pa.SQL.QueryRowContext(ctx, `SELECT v FROM `+p+`items WHERE id = 1`).Scan(&v); err != nil || v != "x" {
		t.Fatalf("read after the reinstall: %q %v", v, err)
	}
	if err := queryErr(pa, `UPDATE `+p+`items SET v = 'y'; INSERT INTO `+p+`items VALUES (2, 'z')`); err != nil {
		t.Fatalf("write after the reinstall: %v", err)
	}
	if err := db.SQL.QueryRowContext(ctx, `SELECT v FROM core_secret`).Scan(&v); err != nil || v != "secret" {
		t.Fatalf("core_secret after the reinstall: %q %v", v, err)
	}
}

// PUBLIC may create temporary objects by default; the manager takes that
// back at start, so that a plugin's identity cannot create a function in
// pg_temp for a trigger or policy of its tables to call with the rights
// of whoever fires it. The core's user, the database's owner, keeps it.
func TestPublicTempRevoked(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	host, dsn, _ := coreUser(t, db, true)
	if _, err := db.SQL.ExecContext(ctx, `DO $$ BEGIN EXECUTE format('GRANT TEMPORARY ON DATABASE %I TO PUBLIC', current_database()); END $$`); err != nil {
		t.Fatal(err)
	}
	m := newManager(t, db, host, dsn, nil)
	var open bool
	if err := db.SQL.QueryRowContext(ctx, `SELECT has_database_privilege('public', current_database(), 'TEMPORARY')`).Scan(&open); err != nil || open {
		t.Fatalf("PUBLIC may still create temporary objects: %v %v", open, err)
	}
	key := randKey()
	install(t, db, key, nil, false)
	if err := m.Ensure(ctx, key); err != nil {
		t.Fatal(err)
	}
	pl := asPlugin(t, m, dsn, key)
	for _, q := range []string{`CREATE TEMP TABLE t (id int)`, `CREATE FUNCTION pg_temp.f() RETURNS int LANGUAGE sql AS 'SELECT 1'`} {
		if err := queryErr(pl, q); err == nil {
			t.Fatalf("the identity ran %q", q)
		}
	}
	if err := queryErr(host, `BEGIN; CREATE TEMP TABLE t (id int); ROLLBACK`); err != nil {
		t.Fatalf("the core lost TEMPORARY: %v", err)
	}
}

// A core that does not own its database cannot take TEMPORARY back from
// PUBLIC and does not start.
func TestPublicTempNotOwner(t *testing.T) {
	db := testutil.DB(t)
	host, dsn, _ := coreUser(t, db, true)
	if _, err := db.SQL.ExecContext(context.Background(), `DO $$ BEGIN
		EXECUTE format('ALTER DATABASE %I OWNER TO CURRENT_USER', current_database());
		EXECUTE format('GRANT TEMPORARY ON DATABASE %I TO PUBLIC', current_database()); END $$`); err != nil {
		t.Fatal(err)
	}
	_, err := dbschema.New(context.Background(), host, dsn, make([]byte, 32), nil)
	if err == nil || !strings.Contains(err.Error(), "REVOKE TEMPORARY ON DATABASE") {
		t.Fatalf("New: %v", err)
	}
}

// A database created by PostgreSQL 14 or older lets every role create
// tables in public: the manager takes that back at start.
func TestPublicCreateRevoked(t *testing.T) {
	db := testutil.DB(t)
	host, dsn, _ := coreUser(t, db, true)
	if _, err := db.SQL.ExecContext(context.Background(), `GRANT CREATE ON SCHEMA public TO PUBLIC`); err != nil {
		t.Fatal(err)
	}
	newManager(t, db, host, dsn, nil)
	var open bool
	if err := db.SQL.QueryRowContext(context.Background(), `SELECT has_schema_privilege('public', 'public', 'CREATE')`).Scan(&open); err != nil || open {
		t.Fatalf("PUBLIC may still create: %v %v", open, err)
	}
}

// A core that stopped between the grant of CREATE and its revocation left
// the identity able to create objects; the next start takes it back.
func TestStartRevokesLeftCreate(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	host, dsn, _ := coreUser(t, db, true)
	m := newManager(t, db, host, dsn, nil)
	key := randKey()
	install(t, db, key, nil, false)
	if err := m.Ensure(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := host.SQL.ExecContext(ctx, `GRANT CREATE ON SCHEMA public TO `+store.QuoteIdent(m.Identity(key))); err != nil {
		t.Fatal(err)
	}
	newManager(t, db, host, dsn, nil)
	var can bool
	if err := db.SQL.QueryRowContext(ctx, `SELECT has_schema_privilege($1, 'public', 'CREATE')`, m.Identity(key)).Scan(&can); err != nil || can {
		t.Fatalf("the identity may still create: %v %v", can, err)
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
	// dynweight, guard and moderation ship migrations (payment's moved into
	// the core with migration 0126).
	if len(dirs) < 3 {
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

// The core reads a plugin's records only logged in as its identity: a
// view put in place of the records table runs as the plugin, not as the
// core.
func TestRecordsReadAsIdentity(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	host, dsn, _ := coreUser(t, db, true)
	m := newManager(t, db, host, dsn, nil)
	key := randKey()
	install(t, db, key, nil, false)
	p := manifest.TablePrefix(key)
	if _, err := m.Migrate(ctx, key, scripts("0001.sql", `CREATE TABLE `+p+`a (id int)`)); err != nil {
		t.Fatal(err)
	}
	table := store.QuoteIdent(p + "schema_migrations")
	if _, err := db.SQL.ExecContext(ctx, `DROP TABLE `+table+`; CREATE VIEW `+table+` AS
		SELECT '0001.sql'::text AS migration_id, current_user::text AS checksum, now() AS applied_at;
		ALTER VIEW `+table+` OWNER TO `+store.QuoteIdent(m.Identity(key))); err != nil {
		t.Fatal(err)
	}
	applied, err := m.AppliedMigrations(ctx, key)
	if err != nil || applied["0001.sql"].Checksum != m.Identity(key) {
		t.Fatalf("records read as %+v (%v), want as %s", applied, err, m.Identity(key))
	}
}
