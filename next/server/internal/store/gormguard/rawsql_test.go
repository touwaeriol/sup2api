package gormguard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg/sqlcheck"
)

// [raw-sql], [sql-const] and the pool's part of [gorm-instance]: statements
// are built and run by GORM, and SQL text is code, never data (CONTRACTS
// §2, "核心的 SQL"). The rules are
// runtime-contract/gormpg/sqlcheck's, which the gateway, the SDK, the
// plugins and gormpg itself run as well; sqlPolicy is the core's. Keys are
// "package.Decl" relative to the module (a function, Type.Method, or the
// first name of a package-level var or const); an entry that allows or
// matches nothing fails the repository test.
const (
	ruleRawSQL   = sqlcheck.RuleRawSQL
	ruleSQLConst = sqlcheck.RuleSQLConst
)

var sqlPolicy = sqlcheck.Policy{
	Module: fixtureModule,
	// Raw: the declarations that run a statement written out, with the
	// reason. The combinators every module shares (EXISTS, INSERT ...
	// SELECT, nextval, to_regclass, the try-lock) are gormpg's.
	Raw: map[string]string{
		// The ledger: data-modifying CTEs that move money in one statement.
		"internal/billing.prechargeStmt": "ledger CTE: precharge (hold the amount, write the ledger row and the request precharge in one statement)",
		"internal/billing.applyStmt":     "ledger CTE: apply a charge or credit to user_balances and balance_ledger in one statement",
		"internal/billing.releaseStmt":   "ledger CTE: release a request precharge and refund it in one statement",
		"internal/billing.settleStmt":    "ledger CTE: settle (release the precharge, apply the charge) in one statement",
		// store: the runners of those statements and the system statements
		// (system.go, ddl.go).
		"internal/store.Numbered.Raw":            "runs a NewNumbered statement (the ledger CTEs); the text is checked where NewNumbered is called",
		"internal/store.Numbered.Exec":           "runs a NewNumbered statement (the ledger CTEs); the text is checked where NewNumbered is called",
		"internal/store.XactLock":                "pg_advisory_xact_lock: a transaction-scoped advisory lock",
		"internal/store.XactLockID":              "pg_advisory_xact_lock of a lock named by its id",
		"internal/store.SetLocal":                "set_config(name, value, true): a setting for the rest of the transaction",
		"internal/store.DDL":                     "DDL takes no parameters: a constant format with quoted identifiers and literals (its format is checked where DDL is called)",
		"internal/store.CurrentSnapshot":         "pg_current_snapshot(): server state, no table",
		"internal/store.CurrentDatabase":         "current_database(): server state, no table",
		"internal/store.ClockTimestamp":          "clock_timestamp(): the database clock, outside any table",
		"internal/store.Migrate":                 "the migrations runner: session advisory lock and unlock",
		"internal/store.migrateFile":             "the migrations runner: SET LOCAL search_path and the migration files' DDL",
		"internal/store.CoreTracker.Ensure":      "the migrations runner: DDL of schema_migrations",
		"internal/store/modelgen.FromMigrations": "dev tool: CREATE / DROP DATABASE of a scratch database",
		"internal/store/modelgen.readTables":     "dev tool: reads the system catalog (pg_catalog)",
	},
	// Dynamic: the declarations whose SQL text is built from what
	// [sql-const] cannot prove constant, with the reason.
	Dynamic: map[string]string{
		"internal/store.Numbered.Raw":            "the text NewNumbered rewrote; NewNumbered's argument is checked",
		"internal/store.Numbered.Exec":           "the text NewNumbered rewrote; NewNumbered's argument is checked",
		"internal/store.DDL":                     "the format is checked where DDL is called (Text); its arguments are quoted (Ident, Literal)",
		"internal/store.Conds.And":               "the text is checked where Conds.And is called (Text)",
		"internal/store.UpdateFrom":              "the text is checked where UpdateFrom is called (Text)",
		"internal/store/modelgen.FromMigrations": "dev tool: the scratch database's name, generated and quoted",
		"internal/usage.statRows":                "the measure columns are joined from the constant statMeasures table by measureList, whose callback [sql-const] cannot follow",
		"internal/usage.rollupHours":             "the measure columns are joined from the constant statMeasures table by measureList, whose callback [sql-const] cannot follow",
	},
	// Pool: the declarations that reach the connection under a handle
	// ([gorm-instance]: gorm's ConnPool, (*gorm.DB).DB).
	Pool: map[string]string{
		"internal/store.DB.WithConn":        "points a session at the connection it took from the pool",
		"internal/store.SQLTx":              "the database/sql transaction under a handle, for River's InsertTx",
		"internal/store.InTx":               "whether the connection under a handle is a transaction (RequireTx)",
		"internal/store.migrateFile":        "runs a migration file on the transaction's connection, past GORM's placeholder parsing",
		"internal/store/gormtest.AssertRun": "a DryRun session on a connection that stands for a transaction",
		"internal/testutil.Traced":          "a session on a connection pool that logs the statements of a test",
	},
	// Quoting: a quoted identifier, constant text whatever its argument.
	Quoting: map[string]string{"internal/store.QuoteIdent": "a quoted identifier"},
	// Text: store's helpers that take SQL text (NewNumbered runs it).
	Text: map[string]bool{
		"internal/store.NewNumbered": true, "internal/store.DDL": false, "internal/store.Conds.And": false, "internal/store.UpdateFrom": false,
	},
	Generated: map[string]bool{"internal/store/model": true},
}

// TestSQLRules runs sqlcheck with the core's policy on fixtures over the
// real package store: its helpers are SQL text sinks, NewNumbered runs its
// text, QuoteIdent quotes. The rules themselves are tested in sqlcheck.
func TestSQLRules(t *testing.T) {
	root := moduleRoot(t)
	fset := token.NewFileSet()
	imp := sqlcheck.Importer(fset, goList(t, root, "-export", "-deps", "fmt", gormPkg, clausePkg,
		fixtureModule+"/internal/store", fixtureModule+"/internal/store/model"))
	r := rules{module: fixtureModule, sql: sqlcheck.New(sqlPolicy)}
	cases := []struct {
		name, src string
		want      []string
	}{
		{name: "store's helpers, built from constants", src: `
func f(db *gorm.DB, x int, role string) {
	var c store.Conds
	c.And("a = ?", x)
	_ = store.UpdateFrom("unnest(?::bigint[]) AS u(id)", store.Int64s(nil))
	_ = store.DDL(db, "ALTER ROLE %s PASSWORD %s", store.Ident(role), store.Literal(role))
	db.Table(store.QuoteIdent("s", role)).Table(model.Account{}.TableName())
}`},
		{name: "store's helpers, built from values", src: `
func f(db *gorm.DB, col string) {
	var c store.Conds
	c.And(col)
	_ = store.UpdateFrom(col)
	_ = store.DDL(db, "DROP ROLE "+col)
	_ = store.NewNumbered("SELECT 1")
}`, want: []string{ruleSQLConst, ruleSQLConst, ruleSQLConst, ruleRawSQL}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := "package p\n" + fixtureImports + "import \"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model\"\n\nvar _ model.Account\n" + c.src + fixtureUses
			f, err := parser.ParseFile(fset, "f.go", src, parser.SkipObjectResolution|parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			path := fixtureModule + "/internal/billing"
			info, err := sqlcheck.TypeCheck(fset, imp, path, []*ast.File{f})
			if err != nil {
				t.Fatalf("fixture does not type-check: %v", err)
			}
			findings := r.check(fset, path, []*ast.File{f}, info)
			var got []string
			for _, finding := range findings {
				got = append(got, strings.Fields(finding[strings.Index(finding, " [")+1:])[0])
			}
			if !slices.Equal(got, c.want) {
				t.Fatalf("rules %v, want %v\n%s", got, c.want, strings.Join(findings, "\n"))
			}
		})
	}
}
