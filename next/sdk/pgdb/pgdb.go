// Package pgdb opens GORM handles on PostgreSQL for plugins and their
// tests. Running migrations and comparing them with the models is package
// pgdbtool's, which only tests and sub2api-plugin import.
//
// No file of the SDK imports pgx: the driver configuration (where the
// dial hook of strict network mode's egress tunnel goes) and the array type
// come from runtime-contract/gormpg, which opens the core's and the
// gateway's handles too. Everything else, the plugins included, speaks GORM
// (TestNoPgxImports).
package pgdb

import (
	"context"
	"fmt"
	"net"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg"
)

// DefaultMaxConns is the connection limit of a handle opened without one,
// per plugin and node. The core opens each of them as its own database user
// (CONTRACTS §115), so they count against the database's limits beside the
// core's pool. pluginsdk.WithDBPoolSize sets another.
const DefaultMaxConns = 4

// Options tune Open. The zero value dials directly with the defaults.
type Options struct {
	// SearchPath is set as search_path unless the DSN already sets one.
	SearchPath string
	// MaxConns caps the open connections (0 = DefaultMaxConns).
	MaxConns int
	// Dial opens the TCP connections (nil = a net.Dialer).
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// DialByName hands host names to Dial unresolved: the egress tunnel
	// resolves them on the host, where the plugin's DNS cannot.
	DialByName bool
}

// Open returns a GORM handle on dsn. It does no I/O: connections are opened
// on first use, so a database that is down
// at Init does not fail the plugin's start. Its errors have the driver's
// text (driverWording).
func Open(dsn string, o Options) (*gorm.DB, error) {
	// The handle is opened as the core and the gateway open theirs
	// (runtime-contract/gormpg): pgx parses the DSN and pins DateStyle and
	// IntervalStyle, the formats Array.Scan parses array text in;
	// database/sql pools the connections, sized by Options; GORM runs on
	// sqlpool.
	cfg, err := gormpg.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("pgdb: parse dsn: %w", err)
	}
	if o.Dial != nil {
		cfg.DialFunc = o.Dial
	} else {
		d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		cfg.DialFunc = d.DialContext
	}
	if o.DialByName {
		cfg.LookupFunc = func(_ context.Context, host string) ([]string, error) { return []string{host}, nil }
	}
	if _, set := cfg.RuntimeParams["search_path"]; !set && o.SearchPath != "" {
		cfg.RuntimeParams["search_path"] = o.SearchPath
	}
	conns := o.MaxConns
	if conns <= 0 {
		conns = DefaultMaxConns
	}
	db, _, err := gormpg.Open(cfg, gormpg.Options{
		Pool: gormpg.Settings{MaxConns: conns, MaxConnLifetime: time.Hour, MaxConnIdleTime: 30 * time.Minute},
		Gorm: &gorm.Config{
			// The caller owns transactions; a single statement needs none.
			SkipDefaultTransaction: true,
			DisableAutomaticPing:   true,
			// Errors go back to the caller; the SQL text could carry values.
			Logger: logger.Discard,
			// gorm.ErrDuplicatedKey and friends, wrapping the driver error.
			TranslateError: true,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("pgdb: %w", err)
	}
	// After gorm.Open, which applied the postgres dialector's own options
	// (identifier length): a wrapper would hide them.
	db.Dialector = driverWording{db.Dialector}
	return db, nil
}

// Close closes the connections of a handle Open returned.
func Close(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// IsUniqueViolation reports whether err is or wraps a unique violation
// (SQLSTATE 23505). Unlike errors.Is(err, gorm.ErrDuplicatedKey), it also
// holds for the errors of Raw(...).Row() and Rows(), which GORM does not
// translate.
func IsUniqueViolation(err error) bool { return gormpg.IsUniqueViolation(err, "") }

// Array is a PostgreSQL array (text[], bigint[], ...) as a model field or a
// query parameter. GORM expands a plain slice parameter into a list, one
// placeholder per element, and cannot scan an array column into one; an
// Array goes to the driver as one array value and scans back from one. nil
// is NULL. A model column still names its type (`gorm:"type:text[]"`): that
// is the DDL sub2api-plugin db writes. The core and the gateway use the
// same type (runtime-contract/gormpg).
type Array[T any] = gormpg.Array[T]
