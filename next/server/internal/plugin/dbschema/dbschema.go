// Package dbschema owns plugin database schemas (plg_<key>), plugin SQL
// migrations, the DSN handed to plugins and the proxy that logs in to the
// database for them (tunnel.go). Plugins use the core's own database user;
// the schema only scopes their search_path
// (docs/DESIGN-2026-11-18-PLUGIN-DB-CORE-USER.md).
package dbschema

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg"
	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg/pgproxy"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

var keyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,29}$`)

// SchemaName returns "plg_<key>".
func SchemaName(pluginKey string) string { return "plg_" + pluginKey }

// Manager implements core.PluginSchemaManager and the runtime-internal
// schema API.
type Manager struct {
	db          *store.DB
	databaseURL string
	log         *slog.Logger

	mu      sync.Mutex
	ensured map[string]bool
	// busy holds the legacy roles left in place because they still had
	// sessions, so that each is logged once.
	busy map[string]bool

	proxyOnce sync.Once
	proxy     *pgproxy.Proxy
	proxyErr  error
}

// New creates the manager. databaseURL is the core's DSN (config
// DatabaseURL); plugins get it with their schema as search_path.
func New(db *store.DB, databaseURL string, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{db: db, databaseURL: databaseURL, log: log, ensured: map[string]bool{}, busy: map[string]bool{}}
}

var _ core.PluginSchemaManager = (*Manager)(nil)

// schemaLock (store.XactLock) serializes Ensure, Drop and the conversion of a
// legacy role of one plugin across the nodes.
func schemaLock(pluginKey string) string { return "plugin-schema:" + pluginKey }

// The statements below read the system catalogs (pg_roles and the other
// views, by the builder) and change roles and schemas (store.DDL, which
// quotes the names and literals into the text). A one-value read uses
// Row().Scan, which fails on no row.

// Ensure creates the plugin's schema, owned by the core's user. A legacy
// login role of the plugin (earlier cores created one per plugin) is
// converted first: its objects are taken back and it is dropped. Idempotent
// and safe to call on every node.
func (m *Manager) Ensure(ctx context.Context, pluginKey string) error {
	if !keyRe.MatchString(pluginKey) {
		return fmt.Errorf("invalid plugin key %q", pluginKey)
	}
	m.mu.Lock()
	done := m.ensured[pluginKey]
	m.mu.Unlock()
	if done {
		return nil
	}
	schema := SchemaName(pluginKey)
	var left bool
	err := m.db.Tx(ctx, func(tx *gorm.DB) error {
		if err := store.XactLock(ctx, tx, schemaLock(pluginKey)); err != nil {
			return err
		}
		var err error
		if left, err = m.convertRole(ctx, tx, pluginKey); err != nil {
			return err
		}
		return store.DDL(store.G(ctx, tx), "CREATE SCHEMA IF NOT EXISTS %s", store.Ident(schema))
	})
	if err != nil {
		return fmt.Errorf("ensure schema %s: %w", schema, err)
	}
	if !left {
		// A role left in place is tried again at the next Ensure.
		m.mu.Lock()
		m.ensured[pluginKey] = true
		m.mu.Unlock()
	}
	return nil
}

// ConvertLegacyRoles converts, once at start, every legacy plugin role this
// core created for this database, the roles of plugins no longer installed
// included (see convertRole). A role that cannot be converted is logged and
// left for the next Ensure of its plugin or the next start; it does not fail
// the start.
func (m *Manager) ConvertLegacyRoles(ctx context.Context) {
	var roles []string
	err := store.G(ctx, m.db.Gorm).Table("pg_roles").Where("rolname LIKE ?", `plg\_%`).Order("rolname").Pluck("rolname", &roles).Error
	if err != nil {
		m.log.Error("list legacy plugin database roles", "err", err)
		return
	}
	for _, role := range roles {
		key := strings.TrimPrefix(role, "plg_")
		if !keyRe.MatchString(key) {
			continue
		}
		err := m.db.Tx(ctx, func(tx *gorm.DB) error {
			if err := store.XactLock(ctx, tx, schemaLock(key)); err != nil {
				return err
			}
			_, err := m.convertRole(ctx, tx, key)
			return err
		})
		if err != nil {
			m.log.Error("convert legacy plugin database role", "role", role, "err", err)
		}
	}
}

// roleMarker is the comment earlier cores put on the plugin roles
// they created. Roles are shared by every database of the PostgreSQL
// cluster, so it names the database: a same-named role of another database
// (another deployment) is not this core's.
func roleMarker(ctx context.Context, q *gorm.DB, pluginKey string) (string, error) {
	dbname, err := store.CurrentDatabase(store.G(ctx, q))
	if err != nil {
		return "", err
	}
	return "sub2api plugin role; database=" + dbname + "; plugin=" + pluginKey, nil
}

// roleOwned reports whether a role named role exists and belongs to this
// plugin of this database: it carries the marker, or (roles created before
// the marker) it owns the plugin's schema here.
func roleOwned(ctx context.Context, q *gorm.DB, role, schema, marker string) (bool, error) {
	var owned bool
	err := roleOwnedQuery(store.G(ctx, q), role, schema, marker).Row().Scan(&owned)
	if store.IsNoRows(err) {
		return false, nil
	}
	return owned, err
}

func roleOwnedQuery(g *gorm.DB, role, schema, marker string) *gorm.DB {
	ownsSchema := g.Table("pg_namespace AS n").Select("1").Where("n.nspname = ? AND n.nspowner = r.oid", schema)
	return g.Table("pg_roles AS r").
		Select("COALESCE(shobj_description(r.oid, 'pg_authid') = ?, false) OR EXISTS (?)", marker, ownsSchema).
		Where("r.rolname = ?", role)
}

// roleConversionLock (store.XactLock) serializes the conversions of
// different plugins' roles: DROP OWNED revokes grants on objects several
// roles may share, and concurrent ACL changes of one object fail with
// "tuple concurrently updated". Always taken after the plugin's own lock.
const roleConversionLock = "plugin-role-conversion"

// convertRole takes back what the legacy role plg_<key> owns in this
// database and drops it, in tx (which holds schemaLock). It needs the
// privileges of the role, which the core has: the core created the role and
// made itself a member. Nothing happens without such a role (a new database,
// a restore without owners). A role that still has sessions - an old plugin
// process on a node not restarted yet - is left in place (left = true),
// logged once, and converted at a later call; dropping it would cut that
// process off its own tables. A same-named role this core did not create is
// not touched.
func (m *Manager) convertRole(ctx context.Context, tx *gorm.DB, pluginKey string) (left bool, err error) {
	role := SchemaName(pluginKey)
	marker, err := roleMarker(ctx, tx, pluginKey)
	if err != nil {
		return false, err
	}
	owned, err := roleOwned(ctx, tx, role, role, marker)
	if err != nil || !owned {
		return false, err
	}
	g := store.G(ctx, tx)
	busy, err := gormpg.Exists(g, g.Table("pg_stat_activity").Where("usename = ?", role))
	if err != nil {
		return false, err
	}
	m.mu.Lock()
	logged := m.busy[role]
	m.busy[role] = busy
	m.mu.Unlock()
	if busy {
		if !logged {
			m.log.Warn("legacy plugin database role still has sessions; it is converted once they end", "role", role)
		}
		return true, nil
	}
	if err := store.XactLock(ctx, tx, roleConversionLock); err != nil {
		return false, err
	}
	rid := store.Ident(role)
	for _, ddl := range []string{"REASSIGN OWNED BY %s TO CURRENT_USER", "DROP OWNED BY %s", "DROP ROLE %s"} {
		if err := store.DDL(g, ddl, rid); err != nil {
			return false, fmt.Errorf("convert legacy role %s: %w", role, err)
		}
	}
	m.log.Info("converted legacy plugin database role", "role", role)
	return false, nil
}

// Migrate ensures the schema and applies the plugin's SQL migrations from
// fsys (the package migrations directory) as the core's user, with the
// schema as search_path. Returns applied file names.
func (m *Manager) Migrate(ctx context.Context, pluginKey string, fsys fs.FS) ([]string, error) {
	if err := m.Ensure(ctx, pluginKey); err != nil {
		return nil, err
	}
	if fsys == nil {
		return nil, nil
	}
	// Plugin scripts are idempotent by contract: a script whose content
	// changed after it was applied runs again and its record is updated.
	opt := store.MigrateOptions{LockKey: store.PluginMigrationLockKey(pluginKey), SearchPath: SchemaName(pluginKey), RerunChanged: true}
	return store.Migrate(ctx, m.db, fsys, Tracker{PluginKey: pluginKey}, opt)
}

// Pending reports whether any of ids has not been applied yet.
func (m *Manager) Pending(ctx context.Context, pluginKey string, ids []string) (bool, error) {
	if len(ids) == 0 {
		return false, nil
	}
	var n int64
	err := pluginMigrations(store.G(ctx, m.db.Gorm), pluginKey).Where("migration_id = ANY(?)", store.Strings(ids)).Count(&n).Error
	return n < int64(len(ids)), err
}

// pluginMigrations filters the migration records of a plugin.
// plugin_migrations belongs to store's migration runner and has no model.
func pluginMigrations(g *gorm.DB, pluginKey string) *gorm.DB {
	return g.Table("plugin_migrations").Where("plugin_key = ?", pluginKey)
}

func dropMigrationRecords(g *gorm.DB, pluginKey string) *gorm.DB {
	return pluginMigrations(g, pluginKey).Delete(nil)
}

// DSN returns the connection string a plugin uses and its schema. A plugin
// behind the egress tunnel (tunnelled, strict network) gets the virtual
// database address without credentials: the core logs in for it
// (ServeTunnel). A plugin without the tunnel (development, no sandbox) dials
// directly and gets the core's DSN with search_path pinned to its schema.
func (m *Manager) DSN(ctx context.Context, pluginKey string, tunnelled bool) (dsn, schema string, err error) {
	if err := m.Ensure(ctx, pluginKey); err != nil {
		return "", "", err
	}
	schema = SchemaName(pluginKey)
	if tunnelled {
		return tunnelDSN(schema), schema, nil
	}
	dsn, err = schemaDSN(m.databaseURL, schema)
	return dsn, schema, err
}

// Drop removes the schema and the migration records.
func (m *Manager) Drop(ctx context.Context, pluginKey string) error {
	return m.db.Tx(ctx, func(tx *gorm.DB) error { return m.DropTx(ctx, tx, pluginKey) })
}

// DropTx holds both schema and migration locks until the lifecycle transaction
// commits. A legacy role of the plugin is converted first (and so dropped),
// unless it still has sessions. Invalidate the local cache even on rollback;
// Ensure rebuilds it.
func (m *Manager) DropTx(ctx context.Context, tx *gorm.DB, pluginKey string) error {
	if !keyRe.MatchString(pluginKey) {
		return fmt.Errorf("invalid plugin key %q", pluginKey)
	}
	schema := SchemaName(pluginKey)
	g := store.G(ctx, tx)
	err := func() error {
		if err := store.XactLock(ctx, tx, schemaLock(pluginKey)); err != nil {
			return err
		}
		if err := store.XactLockID(ctx, tx, store.PluginMigrationLockKey(pluginKey)); err != nil {
			return err
		}
		if _, err := m.convertRole(ctx, tx, pluginKey); err != nil {
			return err
		}
		if err := store.DDL(g, "DROP SCHEMA IF EXISTS %s CASCADE", store.Ident(schema)); err != nil {
			return err
		}
		return dropMigrationRecords(g, pluginKey).Error
	}()
	m.mu.Lock()
	delete(m.ensured, pluginKey)
	m.mu.Unlock()
	if err != nil {
		return fmt.Errorf("drop plugin schema %s: %w", schema, err)
	}
	return nil
}

// Tracker records plugin migrations in public.plugin_migrations.
type Tracker struct {
	PluginKey string
}

func (Tracker) Ensure(context.Context, *gorm.DB) error { return nil }

// migrationRecord is a row Tracker.Applied reads.
type migrationRecord struct {
	MigrationID string `gorm:"column:migration_id"`
	Checksum    string `gorm:"column:checksum"`
}

func (t Tracker) Applied(ctx context.Context, q *gorm.DB) (map[string]string, error) {
	var rows []migrationRecord
	if err := t.appliedQuery(store.G(ctx, q)).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.MigrationID] = r.Checksum
	}
	return out, nil
}

// appliedQuery reads the plugin's records, schema-qualified: the
// connection's search_path is the plugin schema.
func (t Tracker) appliedQuery(g *gorm.DB) *gorm.DB {
	return g.Table("public.plugin_migrations").Select("migration_id, checksum").Where("plugin_key = ?", t.PluginKey)
}

func (t Tracker) Record(ctx context.Context, tx *gorm.DB, id, checksum string) error {
	return t.recordQuery(store.G(ctx, tx), id, checksum).Error
}

// recordQuery upserts an applied migration.
func (t Tracker) recordQuery(g *gorm.DB, id, checksum string) *gorm.DB {
	return g.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "plugin_key"}, {Name: "migration_id"}},
		DoUpdates: append(clause.AssignmentColumns([]string{"checksum"}),
			clause.Assignment{Column: clause.Column{Name: "applied_at"}, Value: gorm.Expr("now()")}),
	}).Create(&AppliedMigration{PluginKey: t.PluginKey, MigrationID: id, Checksum: checksum})
}

// AppliedMigration is a public.plugin_migrations row (store's migration
// runner owns the table, so it has no model); applied_at is the default,
// and nothing is read back.
type AppliedMigration struct {
	PluginKey   string `gorm:"column:plugin_key"`
	MigrationID string `gorm:"column:migration_id"`
	Checksum    string `gorm:"column:checksum"`
}

func (AppliedMigration) TableName() string { return "public.plugin_migrations" }

// schemaDSN pins search_path of dsn (URL or key=value form) to schema; the
// credentials and the other parameters stay.
func schemaDSN(dsn, schema string) (string, error) {
	opt := "-c search_path=" + schema
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return "", fmt.Errorf("parse database url: %w", err)
		}
		q := u.Query()
		q.Set("options", opt)
		// libpq does not decode "+" as a space in URLs.
		u.RawQuery = strings.ReplaceAll(q.Encode(), "+", "%20")
		return u.String(), nil
	}
	// key=value form: later keys override earlier ones.
	return strings.TrimSpace(dsn) + " options=" + kvQuote(opt), nil
}

func kvQuote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}
