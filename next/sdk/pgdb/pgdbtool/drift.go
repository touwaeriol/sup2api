package pgdbtool

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pgdb"
)

// DriftDSNEnv names the variable that hands DriftMain the server DSN; it is
// not an argument so that the password stays out of the process list.
const DriftDSNEnv = "SUB2API_DRIFT_DSN"

// maxAutoMigrate bounds the AutoMigrate runs of converge.
const maxAutoMigrate = 4

// DriftReport is how a plugin's GORM models differ from its migrations.
type DriftReport struct {
	// Statements is the DDL AutoMigrate runs to bring the migrated schema to
	// the models, in order: what `sub2api-plugin db diff` writes. It is not
	// the whole difference: AutoMigrate never tightens NOT NULL, changes a
	// primary key or redefines an index or constraint it finds by name.
	Statements []string `json:"statements"`
	// Unmapped is what the migrations create and no model maps: "table t",
	// "column t.c", "index t.i", "constraint t.c <definition>" (a check,
	// foreign key, exclusion or unique constraint), "view v", "materialized
	// view v", "sequence s", "function f(args)", "trigger t.g", "type x",
	// and anything they create outside the plugin schema. A field or model
	// removed from the code shows up here.
	Unmapped []string `json:"unmapped"`
	// Differences is what the two schemas define differently: a column's
	// type, NOT NULL or default, a table's primary key, an index or
	// constraint of the same name, and what only the models declare.
	Differences []string `json:"differences"`
}

// Clean reports whether the models and the migrations agree.
func (r *DriftReport) Clean() bool {
	return len(r.Statements) == 0 && len(r.Unmapped) == 0 && len(r.Differences) == 0
}

// unmanagedKinds are what a model package may declare unmanaged: objects no
// GORM model describes, which the migrations create by hand.
var unmanagedKinds = []string{"function ", "view ", "materialized view ", "sequence ", "trigger ", "type "}

// errRollback ends Drift's transaction: nothing it did is kept.
var errRollback = errors.New("pgdbtool: rollback")

// Drift compares migrations with models on the PostgreSQL server at dsn (a
// role that may create schemas), in one transaction that it rolls back: an
// interrupted run leaves nothing behind. It
//
//   - runs migrations in a scratch schema as the connecting user with
//     search_path on that schema, like the host runs them as the core's user
//     (CONTRACTS §115), and lists what they created or changed outside it;
//   - lets AutoMigrate build models in a second, empty schema;
//   - compares the two schemas' catalogs: tables, each column's type, NOT
//     NULL and default (by value, so that 0 and '0'::numeric agree), primary
//     keys, and indexes and constraints by definition (the names GORM picks
//     for unique constraints, foreign keys and checks need not be the
//     migrations');
//   - lists the views, sequences, functions, triggers and types the
//     migrations create, except those unmanaged names (as "function f()",
//     "view v", ...: see unmanagedKinds);
//   - runs AutoMigrate on the migrated schema for the DDL a new migration
//     needs.
func Drift(ctx context.Context, dsn string, migrations []Migration, models []any, unmanaged []string) (*DriftReport, error) {
	for _, u := range unmanaged {
		if !slices.ContainsFunc(unmanagedKinds, func(k string) bool { return strings.HasPrefix(u, k) }) {
			return nil, fmt.Errorf("pgdbtool: unmanaged %q: start it with one of %q", u, unmanagedKinds)
		}
	}
	// One connection is enough: everything below runs in turn.
	db, err := pgdb.Open(dsn, pgdb.Options{MaxConns: 1})
	if err != nil {
		return nil, err
	}
	defer pgdb.Close(db) //nolint:errcheck // nothing left to flush
	if err := wrapDialector(db); err != nil {
		return nil, err
	}
	if err := checkTableNames(db, models); err != nil {
		return nil, err
	}
	if err := db.Callback().Raw().After("gorm:raw").Register("pgdbtool:record", record); err != nil {
		return nil, err
	}
	report := &DriftReport{}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		s, err := newScratch(tx)
		if err != nil {
			return err
		}
		if err := s.migrate(tx, migrations); err != nil {
			return err
		}
		if report.Unmapped, err = s.outside(tx); err != nil {
			return err
		}
		if _, err := converge(tx, s.models, models); err != nil {
			return err
		}
		want, err := readCatalog(tx, s.models)
		if err != nil {
			return err
		}
		have, err := readCatalog(tx, s.migrated)
		if err != nil {
			return err
		}
		others, err := readOthers(tx, s.migrated)
		if err != nil {
			return err
		}
		c := comparer{tx: tx, unmapped: report.Unmapped}
		c.compare(have, want)
		c.others(others, unmanaged)
		report.Unmapped, report.Differences = c.unmapped, c.differences
		report.Statements, err = converge(tx, s.migrated, models)
		if err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		return nil, err
	}
	slices.Sort(report.Unmapped)
	slices.Sort(report.Differences)
	return report, nil
}

// checkTableNames refuses a model whose table names a schema: AutoMigrate
// would create it there, outside the scratch schemas, and a migration
// written from it would put the plugin's table in another schema.
func checkTableNames(db *gorm.DB, models []any) error {
	for _, model := range models {
		stmt := &gorm.Statement{DB: db}
		if err := stmt.Parse(model); err != nil {
			return err
		}
		tables := []string{stmt.Schema.Table}
		for _, rel := range stmt.Schema.Relationships.Relations {
			if rel.JoinTable != nil {
				tables = append(tables, rel.JoinTable.Table)
			}
		}
		for _, t := range tables {
			if strings.Contains(t, ".") {
				return fmt.Errorf("pgdbtool: model %s: table %q names a schema; a plugin's tables are in its own schema: name them without one", stmt.Schema.Name, t)
			}
		}
	}
	return nil
}

// recorder keys the context of the statements record collects: a
// *[]string, nil for none.
type recorder struct{}

// record collects the statements that succeeded under a recorder. Every
// Exec of the migrator is DDL (its reads go through Raw().Scan or Row), so
// they are exactly the changes it made.
func record(tx *gorm.DB) {
	if ddl, _ := tx.Statement.Context.Value(recorder{}).(*[]string); ddl != nil && tx.Error == nil {
		*ddl = append(*ddl, tx.Dialector.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...))
	}
}

// converge runs AutoMigrate for models in schema until it changes nothing
// and returns the DDL it ran. One run need not reach the models: a column
// it adds gets its unique constraint on the next run only.
func converge(tx *gorm.DB, schema string, models []any) ([]string, error) {
	if err := searchPath(tx, schema); err != nil {
		return nil, err
	}
	ddl := []string{}
	tx = tx.WithContext(context.WithValue(tx.Statement.Context, recorder{}, &ddl))
	for pass := 0; ; pass++ {
		ran := len(ddl)
		if err := tx.AutoMigrate(models...); err != nil {
			return nil, fmt.Errorf("pgdbtool: AutoMigrate: %w", err)
		}
		if len(ddl) == ran {
			return ddl, nil
		}
		if pass == maxAutoMigrate {
			return nil, fmt.Errorf("pgdbtool: AutoMigrate still changes the schema after %d runs: %q", pass+1, ddl[ran:])
		}
	}
}

func searchPath(tx *gorm.DB, schema string) error {
	return tx.Exec("SET LOCAL search_path TO ?", clause.Table{Name: schema}).Error
}

// scratch names what Drift creates in its transaction.
type scratch struct {
	migrated string // the migrations' schema
	models   string // AutoMigrate's schema
}

func newScratch(tx *gorm.DB) (*scratch, error) {
	base := fmt.Sprintf("sub2api_drift_%d_%d", os.Getpid(), time.Now().UnixNano())
	s := &scratch{migrated: base + "_m", models: base + "_g"}
	for _, schema := range []string{s.migrated, s.models} {
		if err := tx.Exec("CREATE SCHEMA ?", clause.Table{Name: schema}).Error; err != nil {
			return nil, fmt.Errorf("pgdbtool: scratch schemas (the comparison server's role must be able to create schemas): %w", err)
		}
	}
	return s, nil
}

// migrate runs migrations in s.migrated as the connecting user, with
// search_path on that schema alone: what the host does with the core's user.
func (s *scratch) migrate(tx *gorm.DB, migrations []Migration) error {
	for _, m := range migrations {
		text, err := os.ReadFile(m.Path)
		if err != nil {
			return fmt.Errorf("pgdbtool: read %s: %w", m.Name, err)
		}
		err = searchPath(tx, s.migrated)
		if err == nil {
			// No arguments: the driver sends the file as one simple query,
			// so it may hold several statements.
			err = tx.Exec(string(text)).Error
		}
		if err != nil {
			return fmt.Errorf("pgdbtool: migration %s: %w", m.Name, err)
		}
	}
	return nil
}

// outside lists what the migrations created or changed outside their schema
// (its tables' TOAST tables aside): the catalog rows this transaction wrote
// in another schema, other schemas than the scratch ones, and extensions.
// The transaction has written nothing else yet.
func (s *scratch) outside(tx *gorm.DB) ([]string, error) {
	var out []string
	err := tx.Raw(`WITH s AS (SELECT pg_current_xact_id()::xid AS x, ?::regnamespace AS schema, ?::regnamespace AS models)
		SELECT 'relation ' || c.oid::regclass::text FROM pg_class c, s
			WHERE c.xmin = s.x AND c.relnamespace NOT IN (s.schema, s.models, 'pg_toast'::regnamespace) AND c.relpersistence <> 't'
		UNION ALL SELECT 'function ' || p.oid::regprocedure::text FROM pg_proc p, s
			WHERE p.xmin = s.x AND p.pronamespace NOT IN (s.schema, s.models)
		UNION ALL SELECT 'type ' || t.oid::regtype::text FROM pg_type t, s
			WHERE t.xmin = s.x AND t.typnamespace NOT IN (s.schema, s.models) AND t.typtype IN ('e', 'd', 'r')
		UNION ALL SELECT 'schema ' || n.nspname FROM pg_namespace n, s
			WHERE n.xmin = s.x AND n.oid NOT IN (s.schema, s.models)
		UNION ALL SELECT 'extension ' || e.extname FROM pg_extension e, s
			WHERE e.xmin = s.x`, s.migrated, s.models).Scan(&out).Error
	for i := range out {
		out[i] += " (outside the plugin schema)"
	}
	return out, err
}

// DriftMain is the main of the program sub2api-plugin db generates inside a
// plugin module (its internal/model package is out of reach elsewhere):
//
//	func main() { pgdbtool.DriftMain(model.All(), model.Unmanaged()...) }
//
// (Unmanaged when the package declares it). It reads the server DSN from
// DriftDSNEnv and the migrations directory from its only argument, runs
// Drift and writes the DriftReport as JSON to stdout.
func DriftMain(models []any, unmanaged ...string) {
	if err := driftMain(os.Args[1:], os.Stdout, models, unmanaged); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func driftMain(args []string, stdout io.Writer, models []any, unmanaged []string) error {
	fs := flag.NewFlagSet("drift", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	dsn := os.Getenv(DriftDSNEnv)
	if dsn == "" || fs.NArg() != 1 {
		return errors.New("usage: " + DriftDSNEnv + "=<dsn> drift <migrations dir>")
	}
	migrations, err := ReadMigrations(fs.Arg(0))
	if err != nil {
		return err
	}
	report, err := Drift(context.Background(), dsn, migrations, models, unmanaged)
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(report)
}
