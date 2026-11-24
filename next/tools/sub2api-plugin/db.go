package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pgdb/pgdbtool"
	"github.com/Sub2API-Devs/sup2api/next/sdk/testpg"
)

// modelDir is where a plugin keeps its GORM models: a package that exports
// `func All() []any`, and `func Unmanaged() []string` when its migrations
// create what no model describes (the convention of pgdbtool.DriftMain).
const modelDir = "internal/model"

var (
	// errNoModels is a plugin without a model package, i.e. not on GORM yet.
	errNoModels = errors.New("no " + modelDir + " package")
	// errNoDatabase is a plugin whose manifest declares no database.
	errNoDatabase = errors.New("the manifest declares no database")
)

// nothingToCheck reports whether a db command failed only because the
// plugin has no models or no database to compare.
func nothingToCheck(err error) bool {
	return errors.Is(err, errNoModels) || errors.Is(err, errNoDatabase)
}

// errDrift is a db check that found the models and migrations apart.
var errDrift = errors.New("the GORM models and migrations/ disagree; run `sub2api-plugin db diff`")

// cmdDB runs `db diff` and `db check`.
func cmdDB(args []string, stdout, stderr io.Writer) error {
	sub := ""
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "diff":
		return cmdDBDiff(args, stdout, stderr)
	case "check":
		return cmdDBCheck(args, stdout, stderr)
	}
	return fmt.Errorf("usage: sub2api-plugin db diff|check --dir <plugin dir>")
}

// cmdDBCheck fails while the schema the migrations build and the one the
// models describe differ (pgdbtool.Drift).
func cmdDBCheck(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("db check", stderr)
	dir := fs.String("dir", ".", "plugin directory")
	overlay := fs.String("overlay", "", "overlay directory of --dir (see pack): its migrations replace or join the plugin's")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if err := dbCheck(*dir, *overlay, stderr); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "the GORM models match migrations/")
	return nil
}

// dbCheck is db check without its output on success (pack calls it).
func dbCheck(dir, overlay string, stderr io.Writer) error {
	p, err := loadDBPlugin(dir, overlay)
	if err != nil {
		return err
	}
	files, err := p.migrationFiles()
	if err != nil {
		return err
	}
	dsn, release, err := comparisonServer()
	if err != nil {
		return err
	}
	defer release()
	report, err := p.drift(dsn, files, stderr)
	if err != nil {
		return err
	}
	if report.Clean() {
		return nil
	}
	printReport(stderr, report, "")
	return errDrift
}

// printReport prints what report found; after is appended to the titles.
func printReport(w io.Writer, report *pgdbtool.DriftReport, after string) {
	printStatements(w, "DDL the models still need"+after+":", report.Statements)
	printStatements(w, "defined differently (migrations; models)"+after+":", report.Differences)
	printStatements(w, "in the schema, in no model"+after+":", report.Unmapped)
	printStatements(w, refusedTitle+after+":", report.Refused)
}

// refusedTitle heads DriftReport.Refused.
const refusedTitle = "refused by the host (only relations and types named with the plugin's prefix, names at most 63 bytes)"

// comparisonServer is the PostgreSQL db diff and check compare on:
// TEST_DATABASE_URL, or the local testpg server, which release stops again
// when this command started it and nothing else uses it (testpg.Acquire).
func comparisonServer() (dsn string, release func(), err error) {
	dsn, release, err = testpg.Acquire(context.Background())
	if err != nil {
		return "", nil, fmt.Errorf("a PostgreSQL to compare on: %w", err)
	}
	return dsn, release, nil
}

// cmdDBDiff writes the DDL the models need on top of migrations/ as the next
// migration file. Changes that can lose data or fail on existing rows are
// only printed.
func cmdDBDiff(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("db diff", stderr)
	dir := fs.String("dir", ".", "plugin directory")
	name := fs.String("name", "models", "migration name: the file is NNNN_<name>.sql")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if !migrationName.MatchString(*name) {
		return fmt.Errorf("--name %q: use lower case letters, digits and _", *name)
	}
	p, err := loadDBPlugin(*dir, "")
	if err != nil {
		return err
	}
	// A new plugin's first migration may be the one written here.
	if err := os.MkdirAll(p.migrations, 0o755); err != nil {
		return err
	}
	files, err := p.migrationFiles()
	if err != nil {
		return err
	}
	dsn, release, err := comparisonServer()
	if err != nil {
		return err
	}
	defer release()
	report, err := p.drift(dsn, files, stderr)
	if err != nil {
		return err
	}
	next, err := newMigration(p.migrations, p.id, *name, report)
	switch {
	case errors.Is(err, errManual):
		printStatements(stderr, "DDL the models need (! can lose data, rewrite a table or fail on existing rows):", markManual(report.Statements))
		printStatements(stderr, "defined differently (migrations; models), which this DDL may not change:", report.Differences)
		printStatements(stderr, "in the schema, in no model (drop it, map it again, or list it in Unmanaged):", report.Unmapped)
		printStatements(stderr, refusedTitle+" (change it in its migration):", report.Refused)
		return err
	case err != nil:
		return err
	case next == nil:
		fmt.Fprintln(stderr, "no changes: migrations/ already matches the models")
		return nil
	}
	if err := p.verify(dsn, files, next, stderr); err != nil {
		return err
	}
	path, err := next.write(p.migrations)
	if err != nil {
		return err
	}
	fmt.Fprintln(stderr, "review the new migration before committing it")
	fmt.Fprintln(stdout, path)
	return nil
}

func printStatements(w io.Writer, title string, lines []string) {
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(w, title)
	for _, l := range lines {
		fmt.Fprintln(w, "  "+l)
	}
}

// dbPlugin is what db diff and check need of a plugin.
type dbPlugin struct {
	dir        string // absolute
	module     string // Go module path
	id         string // the plugin id (manifest.PluginID): its table prefix
	access     bool   // the manifest asks for tables outside the plugin (database.access)
	migrations string // absolute migrations directory
	overlay    string // the overlay's migrations directory, "" for none
}

func loadDBPlugin(dir, overlay string) (*dbPlugin, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(filepath.Join(abs, filepath.FromSlash(modelDir))); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("%s: %w", abs, errNoModels)
	}
	if overlay, err = resolveOverlay(abs, overlay); err != nil {
		return nil, err
	}
	_, m, err := effectiveManifest(abs, overlay)
	if err != nil {
		return nil, err
	}
	if m.Database == nil {
		return nil, fmt.Errorf("%s: %w", abs, errNoDatabase)
	}
	gomod, err := os.ReadFile(filepath.Join(abs, "go.mod"))
	if err != nil {
		return nil, err
	}
	module := modfile.ModulePath(gomod)
	if module == "" {
		return nil, fmt.Errorf("%s/go.mod: no module path", abs)
	}
	p := &dbPlugin{dir: abs, module: module, id: m.ID(), access: len(m.Database.Access) > 0,
		migrations: filepath.Join(abs, filepath.FromSlash(m.Database.Migrations))}
	// Overlay files land in the package at their own path (collectPackage),
	// so the overlay's copy of the migrations directory joins the plugin's.
	if overlay != "" {
		if st, err := os.Stat(filepath.Join(overlay, filepath.FromSlash(m.Database.Migrations))); err == nil && st.IsDir() {
			p.overlay = filepath.Join(overlay, filepath.FromSlash(m.Database.Migrations))
		}
	}
	return p, nil
}

// sqlFile is one migration by name and content.
type sqlFile struct {
	name string
	text []byte
}

// migrationFiles reads the migrations the package will hold: the plugin's,
// with the overlay's replacing or joining them.
func (p *dbPlugin) migrationFiles() ([]sqlFile, error) {
	dirs := []string{p.migrations}
	if p.overlay != "" {
		dirs = append(dirs, p.overlay)
	}
	if _, err := os.Stat(p.migrations); errors.Is(err, os.ErrNotExist) {
		dirs = dirs[1:] // a plugin without migrations yet
	}
	migrations, err := pgdbtool.ReadMigrations(dirs...)
	if err != nil {
		return nil, err
	}
	out := make([]sqlFile, 0, len(migrations))
	for _, m := range migrations {
		b, err := os.ReadFile(m.Path)
		if err != nil {
			return nil, err
		}
		out = append(out, sqlFile{m.Name, b})
	}
	return out, nil
}

// driftMain is the program run inside the plugin module: only code of that
// module may import its internal/model package.
const driftMain = `// Code generated by sub2api-plugin db; DO NOT EDIT.
package main

import (
	"github.com/Sub2API-Devs/sup2api/next/sdk/pgdb/pgdbtool"

	model %q
)

func main() { pgdbtool.DriftMain(model.All()%s) }
`

// declaresUnmanaged reports whether the model package in dir declares
// `func Unmanaged() []string`.
func declaresUnmanaged(dir string) (bool, error) {
	pkgs, err := parser.ParseDir(token.NewFileSet(), dir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.SkipObjectResolution)
	if err != nil {
		return false, err
	}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, d := range f.Decls {
				if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "Unmanaged" {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

// drift runs pgdbtool.Drift on the plugin's models and files, on the
// PostgreSQL at dsn.
func (p *dbPlugin) drift(dsn string, files []sqlFile, stderr io.Writer) (*pgdbtool.DriftReport, error) {
	// "_" keeps ./... and the packaging away from it while it exists.
	tmp, err := os.MkdirTemp(p.dir, "_dbdrift")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	unmanaged, err := declaresUnmanaged(filepath.Join(p.dir, filepath.FromSlash(modelDir)))
	if err != nil {
		return nil, err
	}
	arg := ""
	if unmanaged {
		arg = ", model.Unmanaged()..."
	}
	src := fmt.Sprintf(driftMain, p.module+"/"+modelDir, arg)
	if err := os.WriteFile(filepath.Join(tmp, "main.go"), []byte(src), 0o600); err != nil {
		return nil, err
	}
	// The files DriftMain runs, in one directory: the plugin's migrations
	// as packed, or those plus a new one (verify).
	migrations := filepath.Join(tmp, "migrations")
	if err := os.Mkdir(migrations, 0o700); err != nil {
		return nil, err
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(migrations, f.name), f.text, 0o600); err != nil {
			return nil, err
		}
	}
	var out, errOut bytes.Buffer
	cmd := exec.Command("go", "run", "./"+filepath.Base(tmp), "-key", p.id, migrations)
	cmd.Dir = p.dir
	cmd.Env = append(os.Environ(), pgdbtool.DriftDSNEnv+"="+dsn)
	cmd.Stdout, cmd.Stderr = &out, io.MultiWriter(stderr, &errOut)
	if err := cmd.Run(); err != nil {
		if p.access && strings.Contains(errOut.String(), undefinedTable) {
			return nil, fmt.Errorf("compare the models with migrations/: %w; %s", err, accessHint)
		}
		return nil, fmt.Errorf("compare the models with migrations/: %w", err)
	}
	var report pgdbtool.DriftReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		return nil, fmt.Errorf("read the comparison: %w", err)
	}
	return &report, nil
}

// undefinedTable is PostgreSQL's SQLSTATE for a relation that does not
// exist, as the driver's error text carries it.
const undefinedTable = "SQLSTATE 42P01"

// accessHint explains a missing relation in the migrations of a plugin that
// asks for core tables: db check cannot check migrations that use them.
const accessHint = "the migrations name a relation that does not exist; db check runs them in an empty schema, without the core's tables, " +
	"so migrations that use the tables of database.access cannot be checked (see docs/PLUGIN-DEVELOPMENT.md 6.1)"

// verify runs next after files twice (the host may run a migration again)
// and fails unless both runs succeed and leave nothing to change: a
// migration GORM's DDL was rewritten into must still bring the schema to
// the models, and stay put when it runs again.
func (p *dbPlugin) verify(dsn string, files []sqlFile, next *migration, stderr io.Writer) error {
	// The copy sorts right after the original: "." < "_".
	again := sqlFile{strings.TrimSuffix(next.name, ".sql") + "_again.sql", next.text}
	report, err := p.drift(dsn, append(slices.Clip(files), sqlFile{next.name, next.text}, again), stderr)
	if err != nil {
		return fmt.Errorf("the generated %s does not run twice; not written: %w", next.name, err)
	}
	if !report.Clean() {
		printReport(stderr, report, " after it")
		if slices.ContainsFunc(report.Refused, func(r string) bool { return strings.Contains(r, ": index "+gormUniquePrefix) }) {
			return fmt.Errorf("the generated %s does not bring the schema to the models; not written: %s", next.name, uniqueHint(p.id))
		}
		return fmt.Errorf("the generated %s does not bring the schema to the models; not written", next.name)
	}
	return nil
}

// gormUniquePrefix starts the name GORM gives the unique constraint of a
// field tagged unique: uni_<table>_<column>.
const gormUniquePrefix = "uni_"

// uniqueHint explains a refused uni_ index of plugin id: the name of a
// unique tag lacks the prefix, a named uniqueIndex has it.
func uniqueHint(id string) string {
	return "GORM names the unique constraint of a field tagged unique " + gormUniquePrefix + "<table>_<column>, without the plugin's prefix; " +
		"tag the field uniqueIndex:" + manifest.TablePrefix(id) + "<name> instead"
}

var (
	migrationName = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
	// migrationFile is a migration's file name; the number orders them.
	migrationFile = regexp.MustCompile(`^(\d+)_.*\.sql$`)
	// destructiveDDL changes a column's type or drops something: data can be
	// lost or a table rewritten.
	destructiveDDL = regexp.MustCompile(`(?i)\bALTER COLUMN \S+ TYPE\b|\bDROP (TABLE|COLUMN|CONSTRAINT|INDEX|SEQUENCE)\b`)
	// setNotNull fails on a table with a NULL in the column.
	setNotNull = regexp.MustCompile(`(?i)\bSET NOT NULL\b`)
	// addNotNull adds a column (GORM writes ADD "column") that is NOT NULL;
	// without a DEFAULT it fails on a table with rows.
	addNotNull = regexp.MustCompile(`(?i)^ALTER TABLE \S+ ADD "[^"]*" .*\bNOT NULL\b`)
	hasDefault = regexp.MustCompile(`(?i)\bDEFAULT\b`)
	// createTable and newRule name the table a statement creates, or the
	// table a new constraint or unique index checks: a CHECK, UNIQUE or
	// FOREIGN KEY added to a table that already has rows fails on a row
	// that breaks it.
	createTable = regexp.MustCompile(`(?i)^CREATE TABLE (?:IF NOT EXISTS )?("[^"]+"|\S+)`)
	newRule     = regexp.MustCompile(`(?i)^(?:ALTER TABLE ("[^"]+"|\S+) ADD CONSTRAINT\b|CREATE UNIQUE INDEX .*? ON ("[^"]+"|\S+))`)
)

// errManual is a diff that needs a hand-written migration.
var errManual = errors.New("not generated: write this migration by hand")

// manual reports, for each of stmts, whether it can lose data, rewrite a
// table or fail on the rows of a production table: a person writes that
// migration (backfill first, then SET NOT NULL; a column type with its
// USING; clean the rows before a new CHECK or UNIQUE; ...). A constraint or
// unique index on a table stmts create themselves has no rows to fail on.
func manual(stmts []string) []bool {
	created := map[string]bool{}
	for _, s := range stmts {
		if m := createTable.FindStringSubmatch(s); m != nil {
			created[m[1]] = true
		}
	}
	out := make([]bool, len(stmts))
	for i, s := range stmts {
		out[i] = isManual(s)
		if m := newRule.FindStringSubmatch(s); m != nil && !created[m[1]+m[2]] {
			out[i] = true
		}
	}
	return out
}

// isManual reports a statement that is manual whatever else the migration
// does (see manual).
func isManual(stmt string) bool {
	return destructiveDDL.MatchString(stmt) || setNotNull.MatchString(stmt) ||
		(addNotNull.MatchString(stmt) && !hasDefault.MatchString(stmt))
}

// markManual prefixes the statements manual flags with "! ".
func markManual(stmts []string) []string {
	out := slices.Clone(stmts)
	for i, m := range manual(stmts) {
		if m {
			out[i] = "! " + out[i]
		}
	}
	return out
}

// migration is a generated migration file.
type migration struct {
	name string // NNNN_<name>.sql
	text []byte
}

// newMigration renders report's statements as the next migration file of
// dir; nil when there is nothing to write. It renders nothing (errManual)
// when a statement needs a person, something is unmapped or refused, or the schemas
// differ in what no statement changes (AutoMigrate never tightens NOT NULL,
// changes a primary key or redefines an index it finds by name): a partial
// file would hide what is left. verify catches a difference the statements
// leave.
func newMigration(dir, id, name string, report *pgdbtool.DriftReport) (*migration, error) {
	if slices.Contains(manual(report.Statements), true) || len(report.Unmapped) > 0 || len(report.Refused) > 0 ||
		(len(report.Statements) == 0 && len(report.Differences) > 0) {
		return nil, errManual
	}
	if len(report.Statements) == 0 {
		return nil, nil
	}
	number, err := nextMigrationNumber(dir)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "-- %s %s: written by `sub2api-plugin db diff` from the GORM models (%s).\n", manifest.DisplayID(id), number, modelDir)
	fmt.Fprintf(&b, "-- Runs as the plugin's database identity in the core's schema: every name starts with %s.\n", manifest.TablePrefix(id))
	b.WriteString("-- Idempotent: every statement can run again.\n\n")
	for _, s := range report.Statements {
		b.WriteString(idempotent(s) + ";\n")
	}
	return &migration{name: number + "_" + name + ".sql", text: []byte(b.String())}, nil
}

// write creates the file in dir and returns its path.
func (m *migration) write(dir string) (string, error) {
	path := filepath.Join(dir, m.name)
	// O_EXCL: never overwrite a migration, even one written meanwhile.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(m.text); err != nil {
		f.Close()
		return "", err
	}
	return path, f.Close()
}

// nextMigrationNumber is one more than the highest migration number in dir,
// zero padded like that migration's (4 digits in an empty dir). The host
// runs migrations in file name order, so a number that needs more digits
// than the others would run first: that is an error.
func nextMigrationNumber(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	highest, width := 0, 4
	for _, e := range entries {
		if m := migrationFile.FindStringSubmatch(e.Name()); m != nil && !e.IsDir() {
			if n, err := strconv.Atoi(m[1]); err == nil && n >= highest {
				highest, width = n, len(m[1])
			}
		}
	}
	next := fmt.Sprintf("%0*d", width, highest+1)
	if len(next) > width {
		return "", fmt.Errorf("migration %s would sort before the %d-digit names in %s (the host runs them in file name order): rename them with more digits first", next, width, dir)
	}
	return next, nil
}

// idempotentDDL rewrites GORM's DDL so that it can run again: plugin
// migrations must be idempotent (the host may retry one), and GORM only
// writes IF NOT EXISTS for indexes.
var idempotentDDL = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`^CREATE TABLE `), "CREATE TABLE IF NOT EXISTS "},
	// GORM writes ADD "column"; ADD CONSTRAINT is wrapped below.
	{regexp.MustCompile(`^(ALTER TABLE \S+ ADD) (")`), "$1 COLUMN IF NOT EXISTS $2"},
	// PostgreSQL has no IF NOT EXISTS for constraints. A second CHECK or
	// FOREIGN KEY of the name raises duplicate_object; a second UNIQUE
	// fails on its index name first, with duplicate_table.
	{regexp.MustCompile(`^(ALTER TABLE \S+ ADD CONSTRAINT .*)$`), "DO $$$$ BEGIN $1; EXCEPTION WHEN duplicate_object OR duplicate_table THEN NULL; END $$$$"},
}

func idempotent(stmt string) string {
	for _, r := range idempotentDDL {
		if r.re.MatchString(stmt) {
			return r.re.ReplaceAllString(stmt, r.repl)
		}
	}
	return stmt
}
