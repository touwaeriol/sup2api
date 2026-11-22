package billing

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// negativeRule is a stored expression with a negative rule multiplier.
const negativeRule = `tier("x", p) * (param("a") == 1 ? -1 : 1)`

// The release check compiles every stored expression, the old rule form as
// migration 0121 rewrites it, and names those that fail.
func TestCheckStoredExpressions(t *testing.T) {
	db := testutil.DB(t)
	seedGlobalExpressions(t, db)
	group := seedRuleExpressions(t, db)
	rule := fmt.Sprintf("%d/ok ", group)
	// After migration 0121 an expression is checked as it is: "|||" no longer compiles.
	require.Equal(t, map[string]string{"model_prices": "drift negative old-form old-tiers-drift ", "price_rules": rule, "model_price_history": "old "}, storedProblems(t, db))
	// Before it, an expression is checked as the migration will rewrite it
	// (the database is this test's own clone).
	_, err := db.SQL.ExecContext(t.Context(), `DELETE FROM schema_migrations WHERE id = $1`, billingModesMigration)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"model_prices": "negative ", "price_rules": rule, "model_price_history": "old "}, storedProblems(t, db))
}

// execOn runs sql on db, which must succeed.
func execOn(t *testing.T, db *store.DB, sql string, args ...any) {
	t.Helper()
	_, err := db.SQL.ExecContext(t.Context(), sql, args...)
	require.NoError(t, err)
}

// seedGlobalExpressions stores global prices that compile and that do not,
// in the modes of now and of before migration 0122.
func seedGlobalExpressions(t *testing.T, db *store.DB) {
	for model, src := range map[string]string{"ok": `tier("base", p*1)`, "old-form": `tier("x", p) ||| param("a") == 1 ? 2 : 1`, "negative": negativeRule} {
		execOn(t, db, `INSERT INTO model_prices (model, mode, config, expression, expr_hash, source) VALUES ($1, 'expression', '{}', $2, $3, 'manual')`, model, src, model)
	}
	// A config that no longer generates its expression.
	execOn(t, db, `INSERT INTO model_prices (model, mode, config, expression, expr_hash, source) VALUES ('drift', 'visual', $1, $2, $3, 'manual')`,
		`{"tiers": [{"name": "base", "unit": "token", "p": 1}], "rules": []}`, `tier("base", p*2 + c*0)`, expr.Hash(`tier("base", p*2 + c*0)`))
	// Prices in the modes before migration 0122 are read as it converts them:
	// these generate their expressions, the last one does not.
	tiered := `len <= 1000 ? tier("s", p*1 + c*0) : tier("l", p*2 + c*0)`
	for model, row := range map[string][3]string{
		"old-tiers":       {"per_token", `{"tiers": [{"name": "l", "max_len": null, "p": 2}, {"name": "s", "max_len": 1000, "p": 1}], "rules": []}`, tiered},
		"old-request":     {"per_request", `{"price": 0.5}`, `tier("base", flat(0.5))`},
		"old-tiers-drift": {"per_token", `{"tiers": [{"name": "s", "max_len": 1000, "p": 1}, {"name": "l", "max_len": null, "p": 3}], "rules": []}`, tiered},
	} {
		execOn(t, db, `INSERT INTO model_prices (model, mode, config, expression, expr_hash, source) VALUES ($1, $2, $3, $4, $5, 'manual')`,
			model, row[0], row[1], row[2], expr.Hash(row[2]))
	}
}

// seedRuleExpressions stores a model price whose expression does not
// compile, in a new group it returns, and an old history row.
func seedRuleExpressions(t *testing.T, db *store.DB) (group int64) {
	require.NoError(t, db.SQL.QueryRowContext(t.Context(), `INSERT INTO groups (name) VALUES ('check') RETURNING id`).Scan(&group))
	execOn(t, db, `INSERT INTO price_rules (group_id, model, mode, config, expression, expr_hash)
		VALUES ($1, 'ok', 'visual', '{"tiers": [{"name": "base", "unit": "request", "price": 1}]}', '$env.p', $2)`, group, expr.Hash(`tier("base", flat(1))`))
	execOn(t, db, `INSERT INTO model_price_history (expr_hash, expression, expr_version) VALUES ('old', 'v1:' || $1, 1)`, negativeRule)
	return group
}

// storedProblems are the refs CheckStoredExpressions names, by table.
func storedProblems(t *testing.T, db *store.DB) map[string]string {
	got, err := CheckStoredExpressions(t.Context(), db.Gorm)
	require.NoError(t, err)
	tables := map[string]string{}
	for _, p := range got {
		require.NotEmpty(t, p.Error)
		tables[p.Table] += p.Ref + " "
	}
	return sortedRefs(tables)
}

// sortedRefs sorts the space-separated refs of each table.
func sortedRefs(tables map[string]string) map[string]string {
	for k, v := range tables {
		refs := strings.Fields(v)
		sort.Strings(refs)
		tables[k] = strings.Join(refs, " ") + " "
	}
	return tables
}
