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
	exec(`INSERT INTO model_prices (model, mode, config, expression, expr_hash, source) VALUES ('drift', 'per_token', $1, $2, $3, 'manual')`,
		`{"tiers": [{"name": "base", "max_len": null, "unit": "token", "p": 1}], "rules": []}`, `tier("base", p*2 + c*0)`, expr.Hash(`tier("base", p*2 + c*0)`))
	exec(`INSERT INTO groups (name) VALUES ('check')`)
	exec(`INSERT INTO price_rules (group_id, model, kind, mode, config, expression, expr_hash)
		SELECT id, 'ok', 'custom', 'per_request', '{"price": 1}', '$env.p', $1 FROM groups WHERE name = 'check'`, expr.Hash(`tier("base", flat(1))`))
	exec(`INSERT INTO price_rules (group_id, model, kind, multiplier) SELECT id, 'negative', 'multiplier', 2 FROM groups WHERE name = 'check'`)
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
	rule := fmt.Sprintf("%d/-/ok ", group)
	// After migration 0121 an expression is checked as it is: "|||" no longer compiles.
	require.Equal(t, map[string]string{"model_prices": "drift negative old-form ", "price_rules": rule, "model_price_history": "old "}, sortedRefs(problems()))
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
