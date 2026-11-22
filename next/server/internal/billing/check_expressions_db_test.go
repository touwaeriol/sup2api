package billing

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// The release check compiles every stored expression, the old rule form as
// migration 0121 rewrites it, and names those that fail.
func TestCheckStoredExpressions(t *testing.T) {
	db := testutil.DB(t)
	ctx := t.Context()
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := db.SQL.ExecContext(ctx, sql, args...)
		require.NoError(t, err)
	}
	const negative = `tier("x", p) * (param("a") == 1 ? -1 : 1)`
	for model, src := range map[string]string{
		"ok":       `tier("base", p*1)`,
		"old-form": `tier("x", p) ||| param("a") == 1 ? 2 : 1`,
		"negative": negative,
	} {
		exec(`INSERT INTO model_prices (model, mode, config, expression, expr_hash, source) VALUES ($1, 'expression', '{}', $2, $3, 'manual')`, model, src, model)
	}
	// A config that no longer generates its expression.
	exec(`INSERT INTO model_prices (model, mode, config, expression, expr_hash, source) VALUES ('drift', 'visual', $1, $2, $3, 'manual')`,
		`{"tiers": [{"name": "base", "unit": "token", "p": 1}], "rules": []}`, `tier("base", p*2 + c*0)`, expr.Hash(`tier("base", p*2 + c*0)`))
	// Prices in the modes before migration 0122 are read as it converts them:
	// these generate their expressions, the last one does not.
	tiered := `len <= 1000 ? tier("s", p*1 + c*0) : tier("l", p*2 + c*0)`
	for model, row := range map[string][2]string{
		"old-tiers":       {`{"tiers": [{"name": "l", "max_len": null, "p": 2}, {"name": "s", "max_len": 1000, "p": 1}], "rules": []}`, tiered},
		"old-request":     {`{"price": 0.5}`, `tier("base", flat(0.5))`},
		"old-tiers-drift": {`{"tiers": [{"name": "s", "max_len": 1000, "p": 1}, {"name": "l", "max_len": null, "p": 3}], "rules": []}`, tiered},
	} {
		mode := "per_token"
		if model == "old-request" {
			mode = "per_request"
		}
		exec(`INSERT INTO model_prices (model, mode, config, expression, expr_hash, source) VALUES ($1, $2, $3, $4, $5, 'manual')`,
			model, mode, row[0], row[1], expr.Hash(row[1]))
	}
	exec(`INSERT INTO groups (name) VALUES ('check')`)
	exec(`INSERT INTO price_rules (group_id, model, mode, config, expression, expr_hash)
		SELECT id, 'ok', 'visual', '{"tiers": [{"name": "base", "unit": "request", "price": 1}]}', '$env.p', $1 FROM groups WHERE name = 'check'`, expr.Hash(`tier("base", flat(1))`))
	exec(`INSERT INTO model_price_history (expr_hash, expression, expr_version) VALUES ('old', 'v1:' || $1, 1)`, negative)

	problems := func() map[string]string {
		got, err := CheckStoredExpressions(ctx, db.Gorm)
		require.NoError(t, err)
		tables := map[string]string{}
		for _, p := range got {
			require.NotEmpty(t, p.Error)
			tables[p.Table] += p.Ref + " "
		}
		return tables
	}
	var group int64
	require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT id FROM groups WHERE name = 'check'`).Scan(&group))
	rule := fmt.Sprintf("%d/ok ", group)
	// After migration 0121 an expression is checked as it is: "|||" no longer compiles.
	require.Equal(t, map[string]string{"model_prices": "drift negative old-form old-tiers-drift ", "price_rules": rule, "model_price_history": "old "}, sortedRefs(problems()))
	// Before it, an expression is checked as the migration will rewrite it
	// (the database is this test's own clone).
	exec(`DELETE FROM schema_migrations WHERE id = $1`, billingModesMigration)
	require.Equal(t, map[string]string{"model_prices": "negative ", "price_rules": rule, "model_price_history": "old "}, sortedRefs(problems()))
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
