package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pgdb"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pgdb/pgdbtool"
	"github.com/Sub2API-Devs/sup2api/next/sdk/testpg"
)

func TestIdempotentDDL(t *testing.T) {
	for in, want := range map[string]string{
		`CREATE TABLE "t" ("id" bigserial,PRIMARY KEY ("id"))`: `CREATE TABLE IF NOT EXISTS "t" ("id" bigserial,PRIMARY KEY ("id"))`,
		`ALTER TABLE "t" ADD "n" bigint NOT NULL DEFAULT 0`:    `ALTER TABLE "t" ADD COLUMN IF NOT EXISTS "n" bigint NOT NULL DEFAULT 0`,
		`ALTER TABLE "t" ADD CONSTRAINT "c" CHECK (n >= 0)`:    `DO $$ BEGIN ALTER TABLE "t" ADD CONSTRAINT "c" CHECK (n >= 0); EXCEPTION WHEN duplicate_object OR duplicate_table THEN NULL; END $$`,
		`CREATE INDEX IF NOT EXISTS "i" ON "t" ("n")`:          `CREATE INDEX IF NOT EXISTS "i" ON "t" ("n")`,
		`ALTER TABLE "t" ALTER COLUMN "n" SET NOT NULL`:        `ALTER TABLE "t" ALTER COLUMN "n" SET NOT NULL`,
	} {
		if got := idempotent(in); got != want {
			t.Errorf("idempotent(%s)\n got %s\nwant %s", in, got, want)
		}
	}
}

// Every constraint statement GORM writes runs twice once rewritten: a
// second UNIQUE fails on its index (duplicate_table), not duplicate_object.
func TestIdempotentConstraintsRunTwice(t *testing.T) {
	db, err := pgdb.Open(testpg.URL(t), pgdb.Options{MaxConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer pgdb.Close(db) //nolint:errcheck
	if err := db.Exec(`CREATE TEMPORARY TABLE p (id bigint PRIMARY KEY); CREATE TEMPORARY TABLE c (id bigint, n bigint)`).Error; err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`ALTER TABLE "c" ADD CONSTRAINT "uni_c_n" UNIQUE ("n")`,
		`ALTER TABLE "c" ADD CONSTRAINT "chk_c_n" CHECK (n >= 0)`,
		`ALTER TABLE "c" ADD CONSTRAINT "fk_c_p" FOREIGN KEY ("id") REFERENCES "p"("id")`,
	} {
		for range 2 {
			if err := db.Exec(idempotent(stmt)).Error; err != nil {
				t.Fatalf("%s: %v", stmt, err)
			}
		}
	}
}

func TestIsManual(t *testing.T) {
	for stmt, want := range map[string]bool{
		`ALTER TABLE "t" ADD "n" bigint`:                                           false,
		`ALTER TABLE "t" ADD "n" bigint NOT NULL DEFAULT 0`:                        false,
		`ALTER TABLE "t" ADD "n" bigint NOT NULL`:                                  true,
		`ALTER TABLE "t" ALTER COLUMN "n" SET NOT NULL`:                            true,
		`ALTER TABLE "t" ALTER COLUMN "n" DROP NOT NULL`:                           false,
		`ALTER TABLE "t" ALTER COLUMN "s" TYPE varchar(9) USING "s"::varchar(9)`:   true,
		`ALTER TABLE "t" DROP CONSTRAINT "uni_t_s"`:                                true,
		`CREATE TABLE "t" ("id" bigserial,"n" bigint NOT NULL,PRIMARY KEY ("id"))`: false,
	} {
		if got := isManual(stmt); got != want {
			t.Errorf("isManual(%s) = %v", stmt, got)
		}
	}
}

func TestManualRules(t *testing.T) {
	stmts := []string{
		`CREATE TABLE "n" ("id" bigserial,"code" text,PRIMARY KEY ("id"))`,
		`ALTER TABLE "n" ADD CONSTRAINT "uni_n_code" UNIQUE ("code")`,
		`CREATE UNIQUE INDEX IF NOT EXISTS "idx_n_code" ON "n" ("code")`,
		`ALTER TABLE "old" ADD CONSTRAINT "chk_old_n" CHECK (n >= 0)`,
		`ALTER TABLE "old" ADD CONSTRAINT "fk_old_p" FOREIGN KEY ("p") REFERENCES "n"("id")`,
		`CREATE UNIQUE INDEX IF NOT EXISTS "idx_old_code" ON "old" ("code")`,
		`CREATE INDEX IF NOT EXISTS "idx_old_n" ON "old" ("n")`,
	}
	want := []bool{false, false, false, true, true, true, false}
	if got := manual(stmts); !slices.Equal(got, want) {
		t.Fatalf("manual = %v, want %v", got, want)
	}
}

func TestNewMigration(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"0001_init.sql", "0003_more.sql", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if m, err := newMigration(dir, "x", "models", &pgdbtool.DriftReport{}); m != nil || err != nil {
		t.Fatalf("no change rendered %v %v", m, err)
	}
	for _, r := range []pgdbtool.DriftReport{
		{Statements: []string{`ALTER TABLE "t" ADD "n" bigint`, `ALTER TABLE "t" ALTER COLUMN "s" TYPE varchar(9) USING "s"::varchar(9)`}},
		{Statements: []string{`ALTER TABLE "t" ADD "n" bigint NOT NULL`}},
		{Statements: []string{`ALTER TABLE "t" ADD "n" bigint`}, Unmapped: []string{"column t.old"}},
		{Statements: []string{`ALTER TABLE "t" ADD "n" bigint`}, Refused: []string{"0001_init.sql: table t (not named plg_x__...)"}},
		// What AutoMigrate does not change, e.g. a NOT NULL it never adds.
		{Differences: []string{"column t.n: migrations: bigint; models: bigint NOT NULL"}},
	} {
		if m, err := newMigration(dir, "x", "models", &r); !errors.Is(err, errManual) || m != nil {
			t.Fatalf("%+v rendered %v %v", r, m, err)
		}
	}
	m, err := newMigration(dir, "x", "add_n", &pgdbtool.DriftReport{Statements: []string{`ALTER TABLE "t" ADD "n" bigint`}})
	if err != nil || m.name != "0004_add_n.sql" {
		t.Fatalf("rendered %v %v", m, err)
	}
	path, err := m.write(dir)
	if err != nil || filepath.Base(path) != "0004_add_n.sql" {
		t.Fatalf("wrote %q %v", path, err)
	}
	if _, err := m.write(dir); err == nil {
		t.Fatal("overwrote a migration")
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.HasPrefix(string(b), "-- x 0004:") || !strings.Contains(string(b), "every name starts with plg_x__.") ||
		!strings.HasSuffix(string(b), "\n\nALTER TABLE \"t\" ADD COLUMN IF NOT EXISTS \"n\" bigint;\n") {
		t.Fatalf("migration:\n%s %v", b, err)
	}
}

// The host runs migrations in file name order: a new number keeps the
// width of the existing ones, and one that would need more digits fails.
func TestNextMigrationNumberWidth(t *testing.T) {
	for _, tc := range []struct {
		files []string
		want  string
	}{
		{nil, "0001"},
		{[]string{"001_init.sql", "002_more.sql"}, "003"},
		{[]string{"9_a.sql"}, ""},
		{[]string{"001_init.sql", "999_last.sql"}, ""},
		{[]string{"0009_a.sql", "README.md"}, "0010"},
	} {
		dir := t.TempDir()
		for _, f := range tc.files {
			if err := os.WriteFile(filepath.Join(dir, f), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		got, err := nextMigrationNumber(dir)
		if got != tc.want || (err != nil) != (tc.want == "") {
			t.Errorf("%v: %q %v, want %q", tc.files, got, err, tc.want)
		}
	}
}

// Every built-in plugin on GORM models has models that are its migrations,
// exactly. The image build packs without db check: this is where it runs.
func TestDBCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go run on plugin modules and needs PostgreSQL")
	}
	t.Setenv("TEST_DATABASE_URL", testpg.URL(t))
	models, err := filepath.Glob(filepath.Join("..", "..", "plugins", "*", filepath.FromSlash(modelDir)))
	if err != nil || len(models) == 0 {
		t.Fatalf("no plugin with %s: %v", modelDir, err)
	}
	for _, m := range models {
		dir := filepath.Dir(filepath.Dir(m))
		if out := runOK(t, "db", "check", "--dir", dir); !strings.Contains(out, "match") {
			t.Errorf("db check %s: %s", dir, out)
		}
	}
	if out := runFail(t, "db", "check", "--dir", filepath.Join("..", "..", "plugins", "mailer")); !strings.Contains(out, errNoModels.Error()) {
		t.Fatalf("db check without models: %s", out)
	}
}

// gormPlugin is demoPlugin as its own Go module with GORM models: its
// migration creates t, the model has more columns than that.
func gormPlugin(t *testing.T) string {
	t.Helper()
	dir := demoPlugin(t)
	sdk, err := filepath.Abs(filepath.Join("..", "..", "sdk"))
	if err != nil {
		t.Fatal(err)
	}
	sum, err := os.ReadFile(filepath.Join(sdk, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	// The SDK opens its handles with runtime-contract/gormpg: a plugin
	// module outside the workspace replaces both.
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n\ngo 1.27\n\n"+
		"require github.com/Sub2API-Devs/sup2api/next/sdk v0.0.0\n\n"+
		"replace github.com/Sub2API-Devs/sup2api/next/sdk => "+sdk+"\n\n"+
		"replace github.com/Sub2API-Devs/sup2api/next/runtime-contract => "+filepath.Join(sdk, "..", "runtime-contract")+"\n")
	writeFile(t, filepath.Join(dir, "go.sum"), string(sum))
	writeFile(t, filepath.Join(dir, "migrations", "0001_init.sql"), `CREATE TABLE plg__tester__demo__t (id bigserial PRIMARY KEY);`)
	writeFile(t, filepath.Join(dir, filepath.FromSlash(modelDir), "model.go"), demoModel(""))
	// Outside the workspace, offline: the module cache has what the SDK
	// needs, and go.mod may take the requirements it is missing.
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "-mod=mod")
	t.Setenv("GOPROXY", "off")
	t.Setenv("TEST_DATABASE_URL", testpg.URL(t))
	return dir
}

func demoModel(extra string) string {
	return `package model

type T struct {
	ID   int64  ` + "`gorm:\"primaryKey\"`" + `
	N    int64  ` + "`gorm:\"not null;default:0\"`" + `
	Code string ` + "`gorm:\"not null;default:''\"`" + `
	` + extra + `
}

func (T) TableName() string { return "plg__tester__demo__t" }

func All() []any { return []any{&T{}} }
`
}

// db diff writes the migration the models need, after running it twice on
// a scratch schema; then db check passes and diff has nothing to do. The
// overlay's migrations count; a NOT NULL column without a default is left
// to a person.
func TestDBDiff(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go run on a plugin module and needs PostgreSQL")
	}
	dir := gormPlugin(t)
	runFail(t, "db", "check", "--dir", dir)
	path := strings.TrimSpace(runOK(t, "db", "diff", "--dir", dir, "--name", "add_n"))
	b, err := os.ReadFile(path)
	if err != nil || filepath.Base(path) != "0002_add_n.sql" || !strings.Contains(string(b), `ADD COLUMN IF NOT EXISTS "code" text NOT NULL DEFAULT ''`) {
		t.Fatalf("%s: %s %v", path, b, err)
	}
	runOK(t, "db", "check", "--dir", dir)
	var out, errb strings.Builder
	if err := cmdDBDiff([]string{"--dir", dir}, &out, &errb); err != nil || !strings.Contains(errb.String(), "no changes") {
		t.Fatalf("second diff: %v %s", err, errb.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "migrations", "0003_models.sql")); err == nil {
		t.Fatal("second diff wrote a migration")
	}

	// testdata/v2 adds column x to t, which no model maps.
	if msg := runFail(t, "db", "check", "--dir", dir, "--overlay", "testdata/v2"); !strings.Contains(msg, "column plg__tester__demo__t.x") {
		t.Fatalf("check with overlay: %s", msg)
	}
	if msg := runFail(t, "pack", "--dir", dir, "--overlay", "testdata/v2", "--out-dir", t.TempDir()); !strings.Contains(msg, "column plg__tester__demo__t.x") {
		t.Fatalf("pack with overlay: %s", msg)
	}

	writeFile(t, filepath.Join(dir, filepath.FromSlash(modelDir), "model.go"), demoModel("M int64 `gorm:\"not null\"`"))
	if msg := runFail(t, "db", "diff", "--dir", dir); !strings.Contains(msg, `! ALTER TABLE "plg__tester__demo__t" ADD "m" bigint NOT NULL`) {
		t.Fatalf("diff with a NOT NULL column: %s", msg)
	}
	// A UNIQUE on a table that has rows fails on duplicates: left to a person.
	writeFile(t, filepath.Join(dir, filepath.FromSlash(modelDir), "model.go"), demoModel("U string `gorm:\"unique\"`"))
	if msg := runFail(t, "db", "diff", "--dir", dir); !strings.Contains(msg, `! ALTER TABLE "plg__tester__demo__t" ADD CONSTRAINT "uni_plg__tester__demo__t_u" UNIQUE ("u")`) {
		t.Fatalf("diff with a new UNIQUE on an existing table: %s", msg)
	}
}

// A plugin with models and no database packs: there is nothing to check.
func TestPackModelsWithoutDatabase(t *testing.T) {
	dir := demoPlugin(t)
	m := strings.Replace(demoManifest, `"database": {"migrations": "migrations/"},`, "", 1)
	m = strings.Replace(m, `{"id": "db.schema"}, `, "", 1)
	writeFile(t, filepath.Join(dir, "manifest.json"), m)
	writeFile(t, filepath.Join(dir, filepath.FromSlash(modelDir), "model.go"), "package model\n")
	runOK(t, "pack", "--dir", dir, "--out-dir", t.TempDir())
	if msg := runFail(t, "db", "check", "--dir", dir); !strings.Contains(msg, errNoDatabase.Error()) {
		t.Fatalf("db check without database: %s", msg)
	}
}

// What AutoMigrate leaves alone fails check, and diff writes nothing for it
// (review 1116 F1): here a NOT NULL the migration lacks.
func TestDBDiffDifferences(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go run on a plugin module and needs PostgreSQL")
	}
	dir := gormPlugin(t)
	writeFile(t, filepath.Join(dir, "migrations", "0001_init.sql"),
		`CREATE TABLE plg__tester__demo__t (id bigserial PRIMARY KEY, n bigint NOT NULL DEFAULT 0, code text DEFAULT '');`)
	want := "column plg__tester__demo__t.code: migrations: text DEFAULT ''::text; models: text NOT NULL DEFAULT ''::text"
	if msg := runFail(t, "db", "check", "--dir", dir); !strings.Contains(msg, want) {
		t.Fatalf("check: %s", msg)
	}
	if msg := runFail(t, "db", "diff", "--dir", dir); !strings.Contains(msg, want) || !strings.Contains(msg, errManual.Error()) {
		t.Fatalf("diff: %s", msg)
	}
}

// Views, sequences and types that the migrations create are declared by
// the model package's Unmanaged (review 1116 F4); a function is refused.
func TestDBCheckUnmanaged(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go run on a plugin module and needs PostgreSQL")
	}
	dir := gormPlugin(t)
	writeFile(t, filepath.Join(dir, "migrations", "0001_init.sql"),
		`CREATE TABLE plg__tester__demo__t (id bigserial PRIMARY KEY, n bigint NOT NULL DEFAULT 0, code text NOT NULL DEFAULT '');
CREATE VIEW plg__tester__demo__t_count AS SELECT count(*) FROM plg__tester__demo__t;`)
	if msg := runFail(t, "db", "check", "--dir", dir); !strings.Contains(msg, "view plg__tester__demo__t_count") {
		t.Fatalf("check: %s", msg)
	}
	writeFile(t, filepath.Join(dir, filepath.FromSlash(modelDir), "unmanaged.go"),
		"package model\n\nfunc Unmanaged() []string { return []string{\"view plg__tester__demo__t_count\"} }\n")
	runOK(t, "db", "check", "--dir", dir)
	writeFile(t, filepath.Join(dir, "migrations", "0002_count.sql"),
		`CREATE FUNCTION plg__tester__demo__count() RETURNS bigint LANGUAGE sql AS 'SELECT count(*) FROM plg__tester__demo__t';`)
	if msg := runFail(t, "db", "check", "--dir", dir); !strings.Contains(msg, "0002_count.sql: function plg__tester__demo__count (not allowed)") {
		t.Fatalf("check with a function: %s", msg)
	}
}

// The plugin's tables share the core's schema: check and diff fail on a
// migration that names a table, or PostgreSQL an index or sequence of it,
// without the plugin's prefix, and on a model table without it.
func TestDBCheckPrefix(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go run on a plugin module and needs PostgreSQL")
	}
	dir := gormPlugin(t)
	writeFile(t, filepath.Join(dir, "migrations", "0001_init.sql"),
		`CREATE TABLE plg__tester__demo__t (id bigserial PRIMARY KEY, n bigint NOT NULL DEFAULT 0, code text NOT NULL DEFAULT '');
CREATE TABLE extra (id serial PRIMARY KEY);`)
	for _, args := range [][]string{{"db", "check"}, {"db", "diff"}} {
		msg := runFail(t, append(args, "--dir", dir)...)
		for _, want := range []string{refusedTitle, "0001_init.sql: table extra (not named plg__tester__demo__...)",
			"0001_init.sql: index extra_pkey (not named plg__tester__demo__...)", "0001_init.sql: sequence extra_id_seq (not named plg__tester__demo__...)"} {
			if !strings.Contains(msg, want) {
				t.Errorf("%v: no %q in %s", args, want, msg)
			}
		}
	}
	writeFile(t, filepath.Join(dir, "migrations", "0001_init.sql"),
		`CREATE TABLE plg__tester__demo__t (id bigserial PRIMARY KEY, n bigint NOT NULL DEFAULT 0, code text NOT NULL DEFAULT '');`)
	runOK(t, "db", "check", "--dir", dir)
	writeFile(t, filepath.Join(dir, filepath.FromSlash(modelDir), "model.go"),
		strings.Replace(demoModel(""), `return "plg__tester__demo__t"`, `return "t"`, 1))
	if msg := runFail(t, "db", "check", "--dir", dir); !strings.Contains(msg, `is not named with the plugin's prefix "plg__tester__demo__"`) {
		t.Fatalf("check with an unprefixed model table: %s", msg)
	}
}

// Migrations that use the core tables of database.access cannot be checked:
// the scratch schema has none of them. check says so.
func TestDBCheckAccessHint(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go run on a plugin module and needs PostgreSQL")
	}
	dir := gormPlugin(t)
	writeFile(t, filepath.Join(dir, "migrations", "0001_init.sql"),
		`CREATE TABLE plg__tester__demo__t (id bigserial PRIMARY KEY, n bigint NOT NULL DEFAULT 0, code text NOT NULL DEFAULT '');
CREATE VIEW plg__tester__demo__names AS SELECT id FROM users;`)
	if msg := runFail(t, "db", "check", "--dir", dir); strings.Contains(msg, accessHint) {
		t.Fatalf("hint without database.access: %s", msg)
	}
	m := strings.Replace(demoManifest, `"database": {"migrations": "migrations/"},`,
		`"database": {"migrations": "migrations/", "access": [{"table": "users", "columns": ["id"], "privileges": ["select"]}]},`, 1)
	m = strings.Replace(m, `{"id": "db.schema"}, `, `{"id": "db.schema"}, {"id": "db.core_read"}, `, 1)
	writeFile(t, filepath.Join(dir, "manifest.json"), m)
	if msg := runFail(t, "db", "check", "--dir", dir); !strings.Contains(msg, accessHint) {
		t.Fatalf("no hint with database.access: %s", msg)
	}
}

// A field tagged unique on a new table gets GORM's constraint name
// uni_<table>_<column>, without the prefix: diff refuses the migration and
// says what to tag instead.
func TestDBDiffUniqueHint(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go run on a plugin module and needs PostgreSQL")
	}
	dir := gormPlugin(t)
	writeFile(t, filepath.Join(dir, "migrations", "0001_init.sql"),
		`CREATE TABLE plg__tester__demo__t (id bigserial PRIMARY KEY, n bigint NOT NULL DEFAULT 0, code text NOT NULL DEFAULT '');`)
	writeFile(t, filepath.Join(dir, filepath.FromSlash(modelDir), "model.go"), strings.Replace(demoModel(""),
		"func All() []any { return []any{&T{}} }", `type U struct {
	ID   int64  `+"`gorm:\"primaryKey\"`"+`
	Code string `+"`gorm:\"unique\"`"+`
}

func (U) TableName() string { return "plg__tester__demo__u" }

func All() []any { return []any{&T{}, &U{}} }`, 1))
	if msg := runFail(t, "db", "diff", "--dir", dir); !strings.Contains(msg, "index uni_plg__tester__demo__u_code (not named plg__tester__demo__...)") ||
		!strings.Contains(msg, uniqueHint("_tester__demo")) {
		t.Fatalf("diff with a unique tag on a new table: %s", msg)
	}
}
