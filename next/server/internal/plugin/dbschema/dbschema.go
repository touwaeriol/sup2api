// Package dbschema owns the database side of plugins (CONTRACTS §130):
// each plugin's database identity (a login role whose password the core
// derives from the master key), the plugin's objects in the core's schema
// under the plugin's prefix (manifest.TablePrefix), its SQL migrations and
// their records, the grants on tables outside the plugin it was given, the
// DSN handed to it and the proxy that logs in as its identity (tunnel.go).
// legacy.go holds the transitional conversion of earlier layouts.
package dbschema

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg"
	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg/pgproxy"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// validKey checks a plugin id (manifest.PluginID, CONTRACTS §137): a valid
// publisher and key that make exactly id, at most manifest.PluginIDMaxLen
// bytes, so that no plugin's prefix or identity starts another's.
func validKey(id string) error {
	publisher, key, ok := manifest.ParsePluginID(id)
	if !ok || check.ValidatePluginID(publisher, key) != nil || manifest.PluginID(publisher, key) != id {
		return fmt.Errorf("invalid plugin id %q", id)
	}
	return nil
}

// ErrIsolationUnavailable: the core's database user may not create roles,
// so a plugin with a database cannot get its own identity. Such a plugin
// does not start; there is no fallback to the core's user.
var ErrIsolationUnavailable = core.NewError(http.StatusConflict, "plugin_db_isolation_unavailable",
	"the core's database user cannot create roles (CREATEROLE), which every plugin with a database needs for its own database identity; grant it CREATEROLE")

// Manager implements core.PluginSchemaManager and the runtime's side of
// plugin databases.
type Manager struct {
	db          *store.DB
	databaseURL string
	masterKey   []byte
	schema      string // the core's schema, current_schema() of its connections
	dbOID       string // this database's OID, in identity names
	dbName      string // this database's name
	log         *slog.Logger

	mu      sync.Mutex
	ensured map[string]bool
	// busy holds the legacy roles left in place because they still had
	// sessions, so that each is logged once (legacy.go).
	busy map[string]bool

	proxyOnce sync.Once
	proxy     *pgproxy.Proxy
	proxyErr  error
}

var _ core.PluginSchemaManager = (*Manager)(nil)

// New reads the core's schema and the database's OID and makes sure PUBLIC
// cannot create objects in that schema (databases created by PostgreSQL 14
// and older allow it in public): it revokes that right, and fails when it
// cannot. databaseURL is the core's DSN (host and TLS of the plugins'
// connections); masterKey derives the identities' passwords.
func New(ctx context.Context, db *store.DB, databaseURL string, masterKey []byte, log *slog.Logger) (*Manager, error) {
	if log == nil {
		log = slog.Default()
	}
	m := &Manager{db: db, databaseURL: databaseURL, masterKey: masterKey, log: log, ensured: map[string]bool{}, busy: map[string]bool{}}
	g := store.G(ctx, db.Gorm)
	if err := g.Table("pg_database").Select("current_schema(), oid::text, datname").Where("datname = current_database()").
		Row().Scan(&m.schema, &m.dbOID, &m.dbName); err != nil {
		return nil, fmt.Errorf("read the core's schema: %w", err)
	}
	if err := m.closePublicCreate(g); err != nil {
		return nil, err
	}
	if err := m.closePublicTemp(g); err != nil {
		return nil, err
	}
	if err := m.revokeLeftCreate(ctx); err != nil {
		return nil, err
	}
	return m, nil
}

// closePublicCreate takes back the right of PUBLIC to create objects in the
// core's schema, which databases created by PostgreSQL 14 and older give.
func (m *Manager) closePublicCreate(g *gorm.DB) error {
	open, err := m.publicCreates(g)
	if err != nil || !open {
		return err
	}
	if err := store.DDL(g, "REVOKE CREATE ON SCHEMA %s FROM PUBLIC", store.Ident(m.schema)); err != nil {
		return err
	}
	if open, err = m.publicCreates(g); err != nil {
		return err
	}
	if open {
		return fmt.Errorf("every role may create objects in schema %s and the core's user cannot revoke it; run REVOKE CREATE ON SCHEMA %s FROM PUBLIC as its owner", m.schema, m.schema)
	}
	m.log.Info("revoked the right of every role to create objects in the core's schema", "schema", m.schema)
	return nil
}

// closePublicTemp takes back the right of PUBLIC to create temporary
// objects in the database, which PostgreSQL gives by default: a plugin's
// identity could otherwise create a function in pg_temp at run time, and
// a trigger or policy of its tables calling it would run with the rights
// of whoever fires it, the core included (CONTRACTS §130.3). The core's
// user keeps the right as the database's owner.
func (m *Manager) closePublicTemp(g *gorm.DB) error {
	open, err := publicTemps(g)
	if err != nil || !open {
		return err
	}
	err = store.DDL(g, "REVOKE TEMPORARY ON DATABASE %s FROM PUBLIC", store.Ident(m.dbName))
	if err == nil {
		open, err = publicTemps(g)
	}
	if err != nil || open {
		return errors.Join(fmt.Errorf("every role may create temporary objects in database %s and the core's user cannot revoke it; run REVOKE TEMPORARY ON DATABASE %s FROM PUBLIC as its owner", m.dbName, m.dbName), err)
	}
	m.log.Info("revoked the right of every role to create temporary objects in the core's database", "database", m.dbName)
	return nil
}

// publicTemps reports whether PUBLIC may create temporary objects in the
// core's database.
func publicTemps(g *gorm.DB) (bool, error) {
	var open bool
	err := g.Table("pg_database").Select("has_database_privilege('public', oid, 'TEMPORARY')").Where("datname = current_database()").Row().Scan(&open)
	return open, err
}

// revokeLeftCreate takes back CREATE on the core's schema from every
// plugin identity of this database that still has it: a core that stopped
// in the middle of a migration left it. Each waits for a migration of its
// plugin that is running on another node.
func (m *Manager) revokeLeftCreate(ctx context.Context) error {
	var roles []string
	if err := store.G(ctx, m.db.Gorm).Table("pg_roles").
		Where("starts_with(rolname, ?) AND has_schema_privilege(oid, ?::regnamespace, 'CREATE')", m.identityPrefix(), m.schema).
		Pluck("rolname", &roles).Error; err != nil {
		return err
	}
	for _, role := range roles {
		key := strings.TrimPrefix(role, m.identityPrefix())
		err := m.db.Tx(ctx, func(tx *gorm.DB) error {
			if err := store.XactLockID(ctx, tx, store.PluginMigrationLockKey(key)); err != nil {
				return err
			}
			return m.setSchemaCreate(ctx, tx, role, false)
		})
		if err != nil {
			return err
		}
		m.log.Warn("took back CREATE on the core's schema left with a plugin's database identity", "role", role)
	}
	return nil
}

// publicCreates reports whether PUBLIC may create objects in the core's
// schema.
func (m *Manager) publicCreates(g *gorm.DB) (bool, error) {
	var open bool
	err := g.Table("pg_namespace").Select("has_schema_privilege('public', oid, 'CREATE')").Where("nspname = ?", m.schema).Row().Scan(&open)
	return open, err
}

// Schema is the core's schema, where the plugins' objects live.
func (m *Manager) Schema() string { return m.schema }

// Identity is the name of plugin pluginKey's database identity,
// "plg<database OID>_<plugin id>". Roles belong to the whole PostgreSQL
// cluster; the OID keeps the plugins of two databases apart.
func (m *Manager) Identity(pluginKey string) string { return m.identityPrefix() + pluginKey }

// identityPrefix starts the name of every plugin identity of this database.
func (m *Manager) identityPrefix() string { return "plg" + m.dbOID + "_" }

// recordsTable is the plugin's migration records table.
func recordsTable(pluginKey string) string {
	return manifest.TablePrefix(pluginKey) + "schema_migrations"
}

// schemaLock (store.XactLock) serializes Ensure, Drop and the conversions
// of one plugin across the nodes. Ensure and Drop take the plugin's
// migration lock (store.PluginMigrationLockKey) after it, so neither runs
// while a migration holds the identity's CREATE.
func schemaLock(pluginKey string) string { return "plugin-schema:" + pluginKey }

// aclLock (store.XactLock) serializes the changes of objects every plugin
// shares: the core's schema (its ACL), the grants on the core's tables and
// what a legacy role's DROP OWNED revokes. Concurrent ACL changes of one
// catalog row fail with "tuple concurrently updated". Always taken last.
const aclLock = "plugin-database-acl"

// The statements below read the system catalogs (by the builder) and
// change roles, owners and grants (store.DDL, which quotes the names and
// literals into the text).

// Ensure brings plugin pluginKey's database side into its state
// (idempotent, safe on every node, once per process unless something
// changes it; another node's uninstall drops the identity, and the next
// Ensure here goes through again): converts an earlier layout (legacy.go), creates the
// identity when missing and sets its password, makes the core a member of
// it (WITH SET, INHERIT: REASSIGN OWNED, DROP OWNED and ALTER ... OWNER
// need both), hands the objects of the plugin's prefix back to it (a
// restore without owners, a reinstall after an uninstall that kept the
// data), drops its strays, takes back every grant on its objects but
// the owner's, takes back a CREATE on the core's schema and sets the grants on
// tables outside the plugin to what its db.core_read grant holds.
func (m *Manager) Ensure(ctx context.Context, pluginKey string) error {
	return m.ensure(ctx, pluginKey, false)
}

func (m *Manager) ensure(ctx context.Context, pluginKey string, force bool) error {
	if err := validKey(pluginKey); err != nil {
		return err
	}
	m.mu.Lock()
	done := m.ensured[pluginKey]
	m.mu.Unlock()
	// Another node's uninstall drops the identity (DropTx) behind this
	// process's cache: it holds only while the identity exists.
	if done && !force {
		if exists, err := m.identityExists(store.G(ctx, m.db.Gorm), pluginKey); err != nil || exists {
			return err
		}
	}
	if err := m.canCreateRoles(ctx); err != nil {
		return err
	}
	var left bool
	err := m.db.Tx(ctx, func(tx *gorm.DB) error {
		if err := lockPlugin(ctx, tx, pluginKey); err != nil {
			return err
		}
		var err error
		if left, err = m.convertLegacy(ctx, tx, pluginKey); err != nil {
			return err
		}
		return m.ensureTx(ctx, tx, pluginKey)
	})
	if err != nil {
		return fmt.Errorf("ensure the database of plugin %s: %w", pluginKey, err)
	}
	// A legacy role left in place is tried again at the next Ensure.
	m.mu.Lock()
	m.ensured[pluginKey] = !left
	m.mu.Unlock()
	return nil
}

// lockPlugin takes the plugin's locks, in their order.
func lockPlugin(ctx context.Context, tx *gorm.DB, pluginKey string) error {
	if err := store.XactLock(ctx, tx, schemaLock(pluginKey)); err != nil {
		return err
	}
	return store.XactLockID(ctx, tx, store.PluginMigrationLockKey(pluginKey))
}

// canCreateRoles fails with ErrIsolationUnavailable unless the core's user
// may create roles.
func (m *Manager) canCreateRoles(ctx context.Context) error {
	var ok bool
	if err := store.G(ctx, m.db.Gorm).Table("pg_roles").Select("rolcreaterole OR rolsuper").Where("rolname = current_user").Row().Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return ErrIsolationUnavailable
	}
	return nil
}

// identityExists reports whether plugin pluginKey's identity exists.
func (m *Manager) identityExists(g *gorm.DB, pluginKey string) (bool, error) {
	return gormpg.Exists(g, g.Table("pg_roles").Where("rolname = ?", m.Identity(pluginKey)))
}

func (m *Manager) ensureTx(ctx context.Context, tx *gorm.DB, pluginKey string) error {
	g := store.G(ctx, tx)
	role := m.Identity(pluginKey)
	rid := store.Ident(role)
	exists, err := m.identityExists(g, pluginKey)
	if err != nil {
		return err
	}
	verifier, err := scramVerifier(m.password(pluginKey))
	if err != nil {
		return err
	}
	if exists {
		err = store.DDL(g, "ALTER ROLE %s WITH LOGIN PASSWORD %s", rid, store.Literal(verifier))
	} else {
		err = store.DDL(g, "CREATE ROLE %s WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS PASSWORD %s", rid, store.Literal(verifier))
	}
	if err != nil {
		return fmt.Errorf("set up role %s: %w", role, err)
	}
	if err := store.DDL(g, "GRANT %s TO CURRENT_USER WITH SET TRUE, INHERIT TRUE", rid); err != nil {
		return fmt.Errorf("make the core a member of role %s: %w", role, err)
	}
	if err := m.handBack(ctx, tx, pluginKey); err != nil {
		return err
	}
	if err := m.removeStrays(ctx, tx, pluginKey); err != nil {
		return err
	}
	if err := m.resetGrants(ctx, tx, pluginKey); err != nil {
		return err
	}
	if err := m.setSchemaCreate(ctx, tx, role, false); err != nil {
		return err
	}
	return m.syncAccess(ctx, tx, pluginKey)
}

// prefixed selects, as c, the relations of the plugin's prefix in the
// core's schema that have an owner of their own: tables (partitions
// included), views, sequences other than a column's and composite types;
// not indexes, which go with their tables.
func (m *Manager) prefixed(g *gorm.DB, pluginKey string) *gorm.DB {
	return g.Table("pg_class AS c").
		Where("c.relnamespace = ?::regnamespace AND starts_with(c.relname, ?) AND c.relkind IN ('r', 'p', 'v', 'm', 'f', 'S', 'c') AND NOT (c.relkind = 'S' AND EXISTS (?))",
			m.schema, manifest.TablePrefix(pluginKey), columnOwned(g))
}

// columnOwned selects a row when relation c belongs to a column of
// another (a serial's or an identity's sequence), which goes with its table.
func columnOwned(g *gorm.DB) *gorm.DB {
	return g.Table("pg_depend AS d").Select("1").
		Where("d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.refclassid = 'pg_class'::regclass AND d.deptype IN ('a', 'i')")
}

// setSchemaCreate grants (or takes back) role's CREATE on the core's
// schema, under aclLock, unless it already has (or lacks) it. In a
// transaction (RequireTx); a grant meant to last runs in one of its own.
func (m *Manager) setSchemaCreate(ctx context.Context, tx *gorm.DB, role string, grant bool) error {
	if err := store.RequireTx(tx); err != nil {
		return err
	}
	g := store.G(ctx, tx)
	has := func() (bool, error) {
		var ok bool
		err := g.Table("pg_roles").Select("has_schema_privilege(oid, ?::regnamespace, 'CREATE')", m.schema).Where("rolname = ?", role).Row().Scan(&ok)
		return ok, err
	}
	if ok, err := has(); err != nil || ok == grant {
		return err
	}
	if err := store.XactLock(ctx, tx, aclLock); err != nil {
		return err
	}
	rid, sid := store.Ident(role), store.Ident(m.schema)
	if grant {
		return store.DDL(g, "GRANT CREATE ON SCHEMA %s TO %s", sid, rid)
	}
	if err := store.DDL(g, "REVOKE CREATE ON SCHEMA %s FROM %s", sid, rid); err != nil {
		return err
	}
	// Granted through PUBLIC or another role it cannot be taken back here.
	ok, err := has()
	if err != nil {
		return err
	}
	if ok {
		return fmt.Errorf("role %s may still create objects in schema %s", role, m.schema)
	}
	return nil
}

// Migrate applies the plugin's SQL migrations from fsys (the package's
// migrations directory) as the plugin's identity and returns the applied
// file names. It ensures the identity (forced: an install or upgrade may
// change what it is granted), then, holding the plugin's migration lock,
// grants it CREATE on the core's schema and runs the files on a connection
// logged in as it (not SET ROLE, which a file could RESET). Each file runs
// in a transaction that fails when it leaves a stray (prefixCheck). The
// records are the plugin's own table (recordsTable). However the run ends,
// the identity's strays go and the CREATE is taken back (ensureTx): a file
// can commit its transaction and leave objects behind its failed check.
func (m *Manager) Migrate(ctx context.Context, pluginKey string, fsys fs.FS) ([]string, error) {
	if err := m.ensure(ctx, pluginKey, true); err != nil {
		return nil, err
	}
	if fsys == nil {
		return nil, nil
	}
	var done []string
	err := m.db.WithConn(ctx, func(conn *gorm.DB) error {
		return store.WithSessionLock(ctx, conn, store.PluginMigrationLockKey(pluginKey), func() (err error) {
			done, err = m.migrateLocked(ctx, pluginKey, fsys)
			return err
		})
	})
	return done, err
}

// migrateLocked is Migrate's run under the plugin's migration lock (which
// Ensure and Drop also take, so ensureTx runs here without the others).
func (m *Manager) migrateLocked(ctx context.Context, pluginKey string, fsys fs.FS) (done []string, err error) {
	role := m.Identity(pluginKey)
	if err := m.db.Tx(ctx, func(tx *gorm.DB) error { return m.setSchemaCreate(ctx, tx, role, true) }); err != nil {
		return nil, err
	}
	defer func() {
		bg := context.WithoutCancel(ctx)
		if clean := m.db.Tx(bg, func(tx *gorm.DB) error { return m.ensureTx(bg, tx, pluginKey) }); clean != nil {
			err = errors.Join(err, fmt.Errorf("clean up after the migrations of plugin %s: %w", pluginKey, clean))
		}
	}()
	err = m.asIdentity(ctx, pluginKey, func(pool *store.DB) error {
		if err := m.adoptRelocated(ctx, pool, pluginKey, fsys); err != nil {
			return err
		}
		// Plugin scripts are idempotent by contract: a script whose content
		// changed after it was applied runs again and its record is updated.
		opt := store.MigrateOptions{SearchPath: m.schema, RerunChanged: true, Check: m.prefixCheck(pluginKey)}
		var err error
		done, err = store.MigrateLocked(ctx, pool, fsys, m.tracker(pluginKey), opt)
		return err
	})
	return done, err
}

// asIdentity runs fn on a connection logged in as plugin pluginKey's
// identity. The core reads and writes the plugin's records table only so:
// the plugin owns it and could make it run its code (a view, a trigger),
// which then runs as the identity, not as the core.
func (m *Manager) asIdentity(ctx context.Context, pluginKey string, fn func(*store.DB) error) error {
	role := m.Identity(pluginKey)
	pool, err := store.OpenAs(ctx, m.databaseURL, role, m.password(pluginKey), 1)
	if err != nil {
		return fmt.Errorf("connect as role %s: %w", role, err)
	}
	defer pool.Close()
	return fn(pool)
}

// tracker is the plugin's records table.
func (m *Manager) tracker(pluginKey string) Tracker {
	return Tracker{Schema: m.schema, Table: recordsTable(pluginKey)}
}

// Pending reports whether any of ids has not been applied yet.
func (m *Manager) Pending(ctx context.Context, pluginKey string, ids []string) (bool, error) {
	if len(ids) == 0 {
		return false, nil
	}
	applied, err := m.AppliedMigrations(ctx, pluginKey)
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		if _, ok := applied[id]; !ok {
			return true, nil
		}
	}
	return false, nil
}

// AppliedMigrations reads the plugin's migration records, by file name
// (none before its first migration), as the identity (asIdentity), after
// ensuring it: the records of an uninstall that kept the data or of a
// relocation are the core's until Ensure hands them back.
func (m *Manager) AppliedMigrations(ctx context.Context, pluginKey string) (map[string]core.PluginMigrationRecord, error) {
	if err := validKey(pluginKey); err != nil {
		return nil, err
	}
	exists, err := gormpg.RelationExists(store.G(ctx, m.db.Gorm), m.tracker(pluginKey).table())
	if err != nil || !exists {
		return map[string]core.PluginMigrationRecord{}, err
	}
	if err := m.Ensure(ctx, pluginKey); err != nil {
		return nil, err
	}
	var rows []migrationRecord
	err = m.asIdentity(ctx, pluginKey, func(pool *store.DB) error {
		return store.G(ctx, pool.Gorm).Table(m.tracker(pluginKey).table()).Select("migration_id, checksum, applied_at").Scan(&rows).Error
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]core.PluginMigrationRecord, len(rows))
	for _, r := range rows {
		out[r.MigrationID] = core.PluginMigrationRecord{Checksum: r.Checksum, AppliedAt: r.AppliedAt}
	}
	return out, nil
}

// DSN returns the connection string a plugin uses and the schema its
// tables are in, after ensuring its database side. The DSN is the virtual
// database address of the egress tunnel without credentials: the core logs
// in as the plugin's identity for it (ServeTunnel).
func (m *Manager) DSN(ctx context.Context, pluginKey string) (dsn, schema string, err error) {
	if err := m.Ensure(ctx, pluginKey); err != nil {
		return "", "", err
	}
	return tunnelDSN(m.schema), m.schema, nil
}

// Drop is DropTx in a transaction of its own.
func (m *Manager) Drop(ctx context.Context, pluginKey string, purge bool) error {
	return m.db.Tx(ctx, func(tx *gorm.DB) error { return m.DropTx(ctx, tx, pluginKey, purge) })
}

// DropTx drops the plugin's identity in the lifecycle transaction, holding
// the plugin's locks until it commits. With purge everything of the
// plugin's goes: the objects of its prefix (whoever owns them: after an
// uninstall that kept them, the core), whatever else the identity owns and
// its grants (DROP OWNED), and the core's records of its data migrations.
// Without, only its data stays, owned by the core: its strays and the
// code of its prefix (views, rules, ...: dropCode), which would run with
// the core's rights, go first, then REASSIGN OWNED, then every grant on
// what stays but the owner's (resetGrants), then DROP OWNED (the other
// order would drop the tables); a reinstall hands them back (Ensure). An earlier layout is converted
// first. The local cache is reset even on rollback; Ensure rebuilds it.
func (m *Manager) DropTx(ctx context.Context, tx *gorm.DB, pluginKey string, purge bool) error {
	if err := validKey(pluginKey); err != nil {
		return err
	}
	err := m.dropTx(ctx, tx, pluginKey, purge)
	m.mu.Lock()
	delete(m.ensured, pluginKey)
	m.mu.Unlock()
	if err != nil {
		return fmt.Errorf("drop the database of plugin %s: %w", pluginKey, err)
	}
	return nil
}

func (m *Manager) dropTx(ctx context.Context, tx *gorm.DB, pluginKey string, purge bool) error {
	if err := lockPlugin(ctx, tx, pluginKey); err != nil {
		return err
	}
	if _, err := m.convertLegacy(ctx, tx, pluginKey); err != nil {
		return err
	}
	g := store.G(ctx, tx)
	rid := store.Ident(m.Identity(pluginKey))
	exists, err := m.identityExists(g, pluginKey)
	if err != nil {
		return err
	}
	if err := store.XactLock(ctx, tx, aclLock); err != nil {
		return err
	}
	if purge {
		mine, err := m.ownedObjects(g, pluginKey, true)
		if err != nil {
			return err
		}
		others, err := m.ownedObjects(g, pluginKey, false)
		if err != nil {
			return err
		}
		for _, o := range append(mine, others...) {
			if err := dropObject(g, o.Kind, store.Ident(m.schema), store.Ident(o.Name)); err != nil {
				return err
			}
		}
		if err := dataMigrations(g, pluginKey).Delete(nil).Error; err != nil {
			return err
		}
	} else {
		// Only the data stays: what would run with the core's rights
		// once it owns it goes first (dropCode), the plugin's grants on
		// it after (resetGrants).
		if exists {
			if err := m.removeStrays(ctx, tx, pluginKey); err != nil {
				return err
			}
		}
		if err := m.dropCode(ctx, tx, pluginKey); err != nil {
			return err
		}
		if exists {
			if err := store.DDL(g, "REASSIGN OWNED BY %s TO CURRENT_USER", rid); err != nil {
				return err
			}
		}
		if err := m.resetGrants(ctx, tx, pluginKey); err != nil {
			return err
		}
	}
	if !exists {
		return nil
	}
	if err := store.DDL(g, "DROP OWNED BY %s", rid); err != nil {
		return err
	}
	return store.DDL(g, "DROP ROLE %s", rid)
}

// dropRelation drops relation schema.name of pg_class kind relkind and
// what depends on it (a view of it, a foreign key to it). A relation an
// earlier drop took with it is skipped.
func dropRelation(g *gorm.DB, relkind string, schema, name store.Ident) error {
	switch relkind {
	case "v":
		return store.DDL(g, "DROP VIEW IF EXISTS %s.%s CASCADE", schema, name)
	case "m":
		return store.DDL(g, "DROP MATERIALIZED VIEW IF EXISTS %s.%s CASCADE", schema, name)
	case "f":
		return store.DDL(g, "DROP FOREIGN TABLE IF EXISTS %s.%s CASCADE", schema, name)
	case "S":
		return store.DDL(g, "DROP SEQUENCE IF EXISTS %s.%s CASCADE", schema, name)
	case "i", "I":
		return store.DDL(g, "DROP INDEX IF EXISTS %s.%s CASCADE", schema, name)
	case "c":
		return store.DDL(g, "DROP TYPE IF EXISTS %s.%s CASCADE", schema, name)
	default:
		return store.DDL(g, "DROP TABLE IF EXISTS %s.%s CASCADE", schema, name)
	}
}

// Tracker records a plugin's migrations in its own table Schema.Table,
// written as the plugin's identity in each file's transaction.
type Tracker struct {
	Schema, Table string
}

// Ensure creates the table; the identity has CREATE while it migrates.
func (t Tracker) Ensure(ctx context.Context, q *gorm.DB) error {
	return store.DDL(store.G(ctx, q), `CREATE TABLE IF NOT EXISTS %s.%s (
		migration_id text PRIMARY KEY,
		checksum text NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now())`, store.Ident(t.Schema), store.Ident(t.Table))
}

func (t Tracker) table() string { return store.QuoteIdent(t.Schema, t.Table) }

// migrationRecord is a row of a plugin's records table.
type migrationRecord struct {
	MigrationID string    `gorm:"column:migration_id"`
	Checksum    string    `gorm:"column:checksum"`
	AppliedAt   time.Time `gorm:"column:applied_at"`
}

func (t Tracker) Applied(ctx context.Context, q *gorm.DB) (map[string]string, error) {
	var rows []migrationRecord
	if err := store.G(ctx, q).Table(t.table()).Select("migration_id, checksum").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.MigrationID] = r.Checksum
	}
	return out, nil
}

func (t Tracker) Record(ctx context.Context, tx *gorm.DB, id, checksum string) error {
	return t.recordQuery(store.G(ctx, tx), id, checksum).Error
}

// recordQuery upserts an applied migration.
func (t Tracker) recordQuery(g *gorm.DB, id, checksum string) *gorm.DB {
	return g.Table(t.table()).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "migration_id"}},
		DoUpdates: append(clause.AssignmentColumns([]string{"checksum"}),
			clause.Assignment{Column: clause.Column{Name: "applied_at"}, Value: gorm.Expr("now()")}),
	}).Create(&appliedRecord{MigrationID: id, Checksum: checksum})
}

// appliedRecord is what recordQuery inserts; applied_at is the default.
type appliedRecord struct {
	MigrationID string `gorm:"column:migration_id"`
	Checksum    string `gorm:"column:checksum"`
}

// AppliedMigration is a public.plugin_migrations row, which now records
// only the plugin data migrations the core ran (rollout's MigrateData;
// store's migration runner owns the table, so it has no model). applied_at
// is the default, and nothing is read back.
type AppliedMigration struct {
	PluginKey   string `gorm:"column:plugin_key"`
	MigrationID string `gorm:"column:migration_id"`
	Checksum    string `gorm:"column:checksum"`
}

func (AppliedMigration) TableName() string { return "plugin_migrations" }

// dataMigrations filters the core's records of the plugin's data
// migrations.
func dataMigrations(g *gorm.DB, pluginKey string) *gorm.DB {
	return g.Table("plugin_migrations").Where("plugin_key = ?", pluginKey)
}
