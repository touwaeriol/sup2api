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

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
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
	// view v", "sequence s", "type x", and anything they create outside the
	// core's schema. A field or model removed from the code shows up here.
	Unmapped []string `json:"unmapped"`
	// Differences is what the two schemas define differently: a column's
	// type, NOT NULL or default, a table's primary key, an index or
	// constraint of the same name, and what only the models declare.
	Differences []string `json:"differences"`
	// Refused is what the host refuses in a migration file, which shares
	// the core's schema with the core and every plugin (CONTRACTS §130), as
	// "<file>: <what>": a relation (table, index, sequence, view) or type
	// whose name lacks the plugin's prefix, PostgreSQL's own names included
	// ("index t_pkey", "sequence t_id_seq"); any other kind of object
	// ("function f", "trigger t.g", "operator", ...), prefixed or not; and
	// "identifier <name>: N bytes" for a name longer than the 63 bytes
	// PostgreSQL keeps of it. An object is reported for the first file
	// after which it exists, as the host checks after each file.
	Refused []string `json:"refused"`
}

// Clean reports whether the models and the migrations agree.
func (r *DriftReport) Clean() bool {
	return len(r.Statements) == 0 && len(r.Unmapped) == 0 && len(r.Differences) == 0 && len(r.Refused) == 0
}

// unmanagedKinds are what a model package may declare unmanaged: objects no
// GORM model describes, which the migrations create by hand. Functions,
// triggers and other kinds are refused (DriftReport.Refused).
var unmanagedKinds = []string{"view ", "materialized view ", "sequence ", "type "}

// errRollback ends Drift's transaction: nothing it did is kept.
var errRollback = errors.New("pgdbtool: rollback")

// Drift compares migrations with models on the PostgreSQL server at dsn (a
// role that may create roles and schemas), in one transaction that it rolls
// back: an interrupted run leaves nothing behind. prefix is the plugin's
// manifest.TablePrefix, which every name of its tables, indexes, sequences
// and other objects in the core's schema starts with. It
//
//   - refuses models whose tables or indexes are not named with prefix or
//     are longer than PostgreSQL keeps;
//   - runs migrations in a scratch schema as a scratch role with search_path
//     on that schema, standing in for the core's schema and the plugin's
//     database identity the host runs them as (CONTRACTS §130): the role
//     may create in the schema and do nothing else, so a statement that
//     needs more (ALTER ... OWNER TO, CREATE SCHEMA, CREATE PUBLICATION)
//     fails here as on install; it lists what the role owns outside the
//     schema, and after each file what the host refuses in it
//     (DriftReport.Refused);
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
func Drift(ctx context.Context, dsn, prefix string, migrations []Migration, models []any, unmanaged []string) (*DriftReport, error) {
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
	if err := checkTableNames(db, prefix, models); err != nil {
		return nil, err
	}
	report := &DriftReport{}
	if err := db.Callback().Raw().After("gorm:raw").Register("pgdbtool:record", record); err != nil {
		return nil, err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		s, err := newScratch(tx)
		if err != nil {
			return err
		}
		if report.Refused, err = s.migrate(tx, prefix, migrations); err != nil {
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

// checkTableNames refuses a model whose table names a schema (AutoMigrate
// would create it there, outside the scratch schemas, and a migration
// written from it would put the plugin's table in another schema), or
// whose table or index is not named with prefix or is longer than
// PostgreSQL keeps. An index declared without a name (index, uniqueIndex)
// is named idx_<table>_<column> by GORM, without the prefix.
func checkTableNames(db *gorm.DB, prefix string, models []any) error {
	for _, model := range models {
		stmt := &gorm.Statement{DB: db}
		if err := stmt.Parse(model); err != nil {
			return err
		}
		names := [][2]string{{"table", stmt.Schema.Table}}
		for _, rel := range stmt.Schema.Relationships.Relations {
			if rel.JoinTable != nil {
				names = append(names, [2]string{"table", rel.JoinTable.Table})
			}
		}
		for _, idx := range stmt.Schema.ParseIndexes() {
			names = append(names, [2]string{"index", idx.Name})
		}
		for _, n := range names {
			kind, name := n[0], n[1]
			switch {
			case strings.Contains(name, "."):
				return fmt.Errorf("pgdbtool: model %s: %s %q names a schema; a plugin's tables are in the core's schema: name them without one", stmt.Schema.Name, kind, name)
			case !strings.HasPrefix(name, prefix):
				return fmt.Errorf("pgdbtool: model %s: %s %q is not named with the plugin's prefix %q", stmt.Schema.Name, kind, name, prefix)
			case len(name) > maxIdentifier:
				return fmt.Errorf("pgdbtool: model %s: %s %q is %d bytes, PostgreSQL keeps %d", stmt.Schema.Name, kind, name, len(name), maxIdentifier)
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
	role     string // runs the migrations, like the plugin's identity
	migrated string // the migrations' schema, where role may create
	models   string // AutoMigrate's schema
}

func newScratch(tx *gorm.DB) (*scratch, error) {
	base := fmt.Sprintf("sub2api_drift_%d_%d", os.Getpid(), time.Now().UnixNano())
	s := &scratch{role: base, migrated: base + "_m", models: base + "_g"}
	role, migrated := clause.Table{Name: s.role}, clause.Table{Name: s.migrated}
	// A new role has none of SUPERUSER, CREATEDB, CREATEROLE, REPLICATION
	// and BYPASSRLS, like the plugin's identity, and gets USAGE and CREATE
	// on the schema, as the host grants them for the migrations. Membership
	// lets a comparison role that is no superuser SET ROLE to it and alter
	// what it creates (PostgreSQL 16 does not grant SET on the roles one
	// creates).
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{"CREATE ROLE ? NOLOGIN", []any{role}},
		{"GRANT ? TO CURRENT_USER", []any{role}},
		{"CREATE SCHEMA ?", []any{migrated}},
		{"GRANT USAGE, CREATE ON SCHEMA ? TO ?", []any{migrated, role}},
		{"CREATE SCHEMA ?", []any{clause.Table{Name: s.models}}},
	} {
		if err := tx.Exec(q.sql, q.args...).Error; err != nil {
			return nil, fmt.Errorf("pgdbtool: scratch schemas (the comparison server's role must be able to create roles and schemas): %w", err)
		}
	}
	return s, nil
}

// migrate runs migrations in s.migrated as s.role, with search_path on that
// schema alone: what the host does in the core's schema as the plugin's
// identity. After each file it lists, as "<file>: <what>", what the host
// refuses (DriftReport.Refused) that no earlier file left.
func (s *scratch) migrate(tx *gorm.DB, prefix string, migrations []Migration) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, m := range migrations {
		text, err := os.ReadFile(m.Path)
		if err != nil {
			return nil, fmt.Errorf("pgdbtool: read %s: %w", m.Name, err)
		}
		err = tx.Exec("SET LOCAL ROLE ?", clause.Table{Name: s.role}).Error
		if err == nil {
			err = searchPath(tx, s.migrated)
		}
		if err == nil {
			// No arguments: the driver sends the file as one simple query,
			// so it may hold several statements.
			err = tx.Exec(string(text)).Error
		}
		if err == nil {
			err = tx.Exec("RESET ROLE").Error
		}
		if err != nil {
			return nil, fmt.Errorf("pgdbtool: migration %s (run as a role that may only create in the schema, like the host runs it): %w", m.Name, err)
		}
		refused, err := s.refused(tx, prefix)
		if err != nil {
			return nil, err
		}
		for _, r := range append(longNames(string(text)), refused...) {
			if !seen[r] {
				seen[r] = true
				out = append(out, m.Name+": "+r)
			}
		}
	}
	return out, nil
}

// outside lists what s.role owns outside s.migrated, found by owner as the
// host finds its identity's objects (CONTRACTS §130.3), so a PL/pgSQL
// EXCEPTION block (a subtransaction) hides nothing: the relations (TOAST
// tables aside; temporary ones included, which the host's identity cannot
// create) and the enum, domain and range types by their owner columns, and
// every object of another catalog by its pg_shdepend row. The role can
// create there only where PUBLIC may (public on PostgreSQL 14 and older,
// pg_temp while PUBLIC holds TEMPORARY).
func (s *scratch) outside(tx *gorm.DB) ([]string, error) {
	var out []string
	err := tx.Raw(`WITH s AS (SELECT ?::regrole AS role, ?::regnamespace AS schema)
		SELECT 'relation ' || c.oid::regclass::text FROM pg_class c, s
			WHERE c.relowner = s.role AND c.relnamespace <> s.schema AND c.relkind <> 't'
				AND NOT starts_with(c.relnamespace::regnamespace::text, 'pg_toast')
		UNION ALL SELECT 'type ' || t.oid::regtype::text FROM pg_type t, s
			WHERE t.typowner = s.role AND t.typnamespace <> s.schema AND t.typtype IN ('e', 'd', 'r')
		UNION ALL SELECT o.type || ' ' || o.identity FROM pg_shdepend d CROSS JOIN LATERAL pg_identify_object(d.classid, d.objid, d.objsubid) o, s
			WHERE d.deptype = 'o' AND d.refclassid = 'pg_authid'::regclass AND d.refobjid = s.role
				AND d.classid NOT IN ('pg_class'::regclass, 'pg_type'::regclass)
				AND o.schema IS DISTINCT FROM s.schema::text`, s.role, s.migrated).Scan(&out).Error
	for i := range out {
		out[i] += " (outside the core's schema)"
	}
	return out, err
}

// refused lists what the host refuses in s.migrated, sorted: a relation
// of an allowed kind (table, index, sequence, view, materialized view,
// composite type) or an enum, domain or range type without prefix, which
// covers PostgreSQL's names for a primary key's or unique constraint's
// index and a serial column's sequence; a relation of any other kind; and
// every object of other kinds: functions (but those PostgreSQL makes for a
// range type), operators, operator classes and families, collations,
// conversions, text search configurations and dictionaries, statistics
// objects, and the triggers, rules and policies on its tables.
func (s *scratch) refused(tx *gorm.DB, prefix string) ([]string, error) {
	var out []string
	err := tx.Raw(`WITH s AS (SELECT ?::regnamespace AS n, ?::text AS prefix)
		SELECT CASE WHEN c.relkind NOT IN ('r', 'p', 'i', 'I', 'S', 'v', 'm', 'c') THEN 'relation ' || c.relname || ' (not allowed)'
				ELSE CASE c.relkind WHEN 'r' THEN 'table ' WHEN 'p' THEN 'table ' WHEN 'i' THEN 'index ' WHEN 'I' THEN 'index '
					WHEN 'S' THEN 'sequence ' WHEN 'v' THEN 'view ' WHEN 'm' THEN 'materialized view ' ELSE 'type ' END
					|| c.relname || ' (not named ' || s.prefix || '...)' END
			FROM pg_class c, s WHERE c.relnamespace = s.n
				AND (c.relkind NOT IN ('r', 'p', 'i', 'I', 'S', 'v', 'm', 'c') OR NOT starts_with(c.relname, s.prefix))
		UNION ALL SELECT 'type ' || t.typname || ' (not named ' || s.prefix || '...)' FROM pg_type t, s
			WHERE t.typnamespace = s.n AND t.typtype IN ('e', 'd', 'r', 'm') AND NOT starts_with(t.typname, s.prefix)
		UNION ALL SELECT 'function ' || p.proname || ' (not allowed)' FROM pg_proc p, s
			WHERE p.pronamespace = s.n AND NOT EXISTS (SELECT 1 FROM pg_depend d
				WHERE d.classid = 'pg_proc'::regclass AND d.objid = p.oid AND d.refclassid = 'pg_type'::regclass AND d.deptype = 'i')
		UNION ALL SELECT 'operator ' || o.oprname || ' (not allowed)' FROM pg_operator o, s WHERE o.oprnamespace = s.n
		UNION ALL SELECT 'operator class ' || o.opcname || ' (not allowed)' FROM pg_opclass o, s WHERE o.opcnamespace = s.n
		UNION ALL SELECT 'operator family ' || o.opfname || ' (not allowed)' FROM pg_opfamily o, s WHERE o.opfnamespace = s.n
		UNION ALL SELECT 'collation ' || o.collname || ' (not allowed)' FROM pg_collation o, s WHERE o.collnamespace = s.n
		UNION ALL SELECT 'conversion ' || o.conname || ' (not allowed)' FROM pg_conversion o, s WHERE o.connamespace = s.n
		UNION ALL SELECT 'text search configuration ' || o.cfgname || ' (not allowed)' FROM pg_ts_config o, s WHERE o.cfgnamespace = s.n
		UNION ALL SELECT 'text search dictionary ' || o.dictname || ' (not allowed)' FROM pg_ts_dict o, s WHERE o.dictnamespace = s.n
		UNION ALL SELECT 'statistics ' || o.stxname || ' (not allowed)' FROM pg_statistic_ext o, s WHERE o.stxnamespace = s.n
		UNION ALL SELECT 'trigger ' || c.relname || '.' || g.tgname || ' (not allowed)' FROM pg_trigger g JOIN pg_class c ON c.oid = g.tgrelid, s
			WHERE c.relnamespace = s.n AND NOT g.tgisinternal
		UNION ALL SELECT 'rule ' || c.relname || '.' || r.rulename || ' (not allowed)' FROM pg_rewrite r JOIN pg_class c ON c.oid = r.ev_class, s
			WHERE c.relnamespace = s.n AND r.rulename <> '_RETURN'
		UNION ALL SELECT 'policy ' || c.relname || '.' || p.polname || ' (not allowed)' FROM pg_policy p JOIN pg_class c ON c.oid = p.polrelid, s
			WHERE c.relnamespace = s.n
		ORDER BY 1`, s.migrated, prefix).Scan(&out).Error
	return out, err
}

// DriftMain is the main of the program sub2api-plugin db generates inside a
// plugin module (its internal/model package is out of reach elsewhere):
//
//	func main() { pgdbtool.DriftMain(model.All(), model.Unmanaged()...) }
//
// (Unmanaged when the package declares it). It reads the server DSN from
// DriftDSNEnv, the plugin id (manifest.PluginID) from -key and the
// migrations directory from its only argument, runs Drift with the id's
// manifest.TablePrefix and writes the DriftReport as JSON to stdout.
func DriftMain(models []any, unmanaged ...string) {
	if err := driftMain(os.Args[1:], os.Stdout, models, unmanaged); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func driftMain(args []string, stdout io.Writer, models []any, unmanaged []string) error {
	fs := flag.NewFlagSet("drift", flag.ContinueOnError)
	key := fs.String("key", "", "plugin id (manifest.PluginID)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dsn := os.Getenv(DriftDSNEnv)
	if dsn == "" || *key == "" || fs.NArg() != 1 {
		return errors.New("usage: " + DriftDSNEnv + "=<dsn> drift -key <plugin id> <migrations dir>")
	}
	migrations, err := ReadMigrations(fs.Arg(0))
	if err != nil {
		return err
	}
	report, err := Drift(context.Background(), dsn, manifest.TablePrefix(*key), migrations, models, unmanaged)
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(report)
}
