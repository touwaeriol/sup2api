package billing

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

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
	exec(`INSERT INTO groups (name) VALUES ('check')`)
	exec(`INSERT INTO price_rules (group_id, model, kind, mode, config, expression, expr_hash)
		SELECT id, 'ok', 'custom', 'per_request', '{"price": 1}', '$env.p', 'h' FROM groups WHERE name = 'check'`)
	exec(`INSERT INTO price_rules (group_id, model, kind, multiplier) SELECT id, 'negative', 'multiplier', 2 FROM groups WHERE name = 'check'`)
	exec(`INSERT INTO model_price_history (expr_hash, expression, expr_version) VALUES ('old', 'v1:' || $1, 1)`, negative)

	got, err := CheckStoredExpressions(ctx, db.Gorm)
	require.NoError(t, err)
	tables := map[string]string{}
	for _, p := range got {
		require.NotEmpty(t, p.Error)
		tables[p.Table] += p.Ref
	}
	var group int64
	require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT id FROM groups WHERE name = 'check'`).Scan(&group))
	require.Equal(t, map[string]string{"model_prices": "negative", "price_rules": fmt.Sprintf("%d/-/ok", group), "model_price_history": "old"}, tables)
}
