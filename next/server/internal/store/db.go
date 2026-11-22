// Package store owns the PostgreSQL pool, transactions and the migration
// runner shared by the core's and the plugins' migrations (the plugins'
// tables live in the core's schema under plg_<key>__, CONTRACTS §130).
// Statements are built and run by GORM over that pool (see gorm.go).
package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg"
)

// DB is the core's connection pool.
type DB struct {
	// Gorm runs every statement: on the pool, or through Tx, TxOptions and
	// WithConn on one transaction or connection of it.
	Gorm *gorm.DB
	// SQL is the same pool as a database/sql handle, for what needs one
	// rather than GORM: River, the tests. Production code goes through Gorm
	// (gormguard [sql-handle]).
	SQL *sql.DB
}

// Open connects to dsn. The pool is sized by SUB2API_PG_MAX_CONNS, else
// the defaults (pool.go).
func Open(ctx context.Context, dsn string) (*DB, error) {
	s := poolDefaults.WithMaxConns(os.Getenv("SUB2API_PG_MAX_CONNS"))
	db, err := open(ctx, dsn, s, "", "")
	if err != nil {
		return nil, err
	}
	slog.Info("database pool opened", "max_conns", s.MaxConns,
		"max_conn_lifetime", s.MaxConnLifetime, "max_conn_idle_time", s.MaxConnIdleTime)
	return db, nil
}

// OpenSized connects to dsn with a pool of at most maxConns connections,
// whatever the environment says: a pool for one job.
func OpenSized(ctx context.Context, dsn string, maxConns int) (*DB, error) {
	return OpenAs(ctx, dsn, "", "", maxConns)
}

// OpenAs is OpenSized logged in as user with password instead of the
// credentials of dsn, whose other settings (host, TLS) stay; an empty user
// keeps them. A plugin's migrations run as the plugin's database identity
// on such a pool (CONTRACTS §130).
func OpenAs(ctx context.Context, dsn, user, password string, maxConns int) (*DB, error) {
	s := poolDefaults
	s.MaxConns = maxConns
	return open(ctx, dsn, s, user, password)
}

// open connects to dsn (as user when given) with a pool of s and pings it.
// gormpg.ParseConfig pins DateStyle ISO, the format Times parses array text
// in (pgtext.go).
func open(ctx context.Context, dsn string, s gormpg.Settings, user, password string) (*DB, error) {
	cfg, err := gormpg.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	if user != "" {
		cfg.User, cfg.Password = user, password
	}
	g, sqlDB, err := gormpg.Open(cfg, gormpg.Options{Pool: s, Gorm: gormConfig(), Setup: registerTimestamps})
	if err != nil {
		return nil, err
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &DB{Gorm: g, SQL: sqlDB}, nil
}

// Close closes the pool.
func (db *DB) Close() { _ = db.SQL.Close() }

// MaxConns is the size of the pool, 0 for a DB without one.
func (db *DB) MaxConns() int {
	if db == nil || db.SQL == nil {
		return 0
	}
	return db.SQL.Stats().MaxOpenConnections
}

// Ping checks that the database answers on a pooled connection.
func (db *DB) Ping(ctx context.Context) error { return db.SQL.PingContext(ctx) }

// Tx runs fn in a transaction, committing on nil error and rolling back on
// an error or a panic, which it then re-raises.
func (db *DB) Tx(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return db.Gorm.WithContext(ctx).Transaction(fn)
}

// TxOptions is Tx with an isolation level or read-only access, such as
// ReadOnly() or Snapshot().
func (db *DB) TxOptions(ctx context.Context, opts sql.TxOptions, fn func(tx *gorm.DB) error) error {
	return db.Gorm.WithContext(ctx).Transaction(fn, &opts)
}

// ReadOnly is a read-only transaction at the default isolation level. A
// function, not a variable: no package can change what every other one
// gets.
func ReadOnly() sql.TxOptions { return sql.TxOptions{ReadOnly: true} }

// Snapshot is a read-only repeatable-read transaction: all its reads see one
// snapshot of the database.
func Snapshot() sql.TxOptions {
	return sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
}

// WithConn runs fn on one connection taken from the pool, for state that
// lives on a session rather than a transaction (a session advisory lock).
// conn's statements all run on that connection. GORM's Transaction on conn
// is package store's own (the migrator's): gormguard [gorm-tx] refuses it
// elsewhere, so code outside store that needs a transaction takes Tx or
// TxOptions instead. The connection goes back to the pool when fn returns.
func (db *DB) WithConn(ctx context.Context, fn func(conn *gorm.DB) error) error {
	c, err := db.SQL.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	// A session of its own: the Statement is a copy, so pointing it at the
	// connection leaves the pool's handle as it is.
	s := db.Gorm.Session(&gorm.Session{NewDB: true, Context: ctx})
	s.Statement.ConnPool = c
	return fn(s)
}

// SQLTx is the database/sql transaction a handle of Tx, TxOptions or a
// WithConn transaction runs on, for a library that takes one (River's
// InsertTx); nil when tx is no transaction.
func SQLTx(tx *gorm.DB) *sql.Tx {
	if tx == nil {
		return nil
	}
	t, _ := tx.Statement.ConnPool.(*sql.Tx)
	return t
}

// The errors of a statement are the driver's; the modules read them with
// these (gormpg's, see there).
var (
	IsNoRows              = gormpg.IsNoRows
	IsUniqueViolation     = gormpg.IsUniqueViolation
	IsForeignKeyViolation = gormpg.IsForeignKeyViolation
	IsDeadlock            = gormpg.IsDeadlock
	SQLState              = gormpg.SQLState
	ConstraintName        = gormpg.ConstraintName
	Hint                  = gormpg.Hint
)

// IsWriteRace reports whether err is a write that lost to a concurrent one:
// a row it refers to was deleted meanwhile (a foreign key violation), or the
// database broke a deadlock with another writer. The same write may succeed
// when sent again (core.ChangedMeanwhile).
func IsWriteRace(err error) bool { return IsForeignKeyViolation(err) || IsDeadlock(err) }

// QuoteIdent quotes a (qualified) identifier for SQL text, for DDL, which
// takes no parameters: QuoteIdent("s", "t") is "s"."t".
func QuoteIdent(parts ...string) string {
	q := make([]string, len(parts))
	for i, p := range parts {
		p = strings.ReplaceAll(p, "\x00", "")
		q[i] = `"` + strings.ReplaceAll(p, `"`, `""`) + `"`
	}
	return strings.Join(q, ".")
}
