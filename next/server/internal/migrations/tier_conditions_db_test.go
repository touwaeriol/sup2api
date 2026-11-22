package migrations_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/migrations"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// 0122 leaves visual and expression prices (CONTRACTS §131): per_token
// tiers by max_len get conditions, in the old generator's order, and
// per_request prices become one request tier with their rules, in
// model_prices and in custom price rules; every config generates the stored
// expression (expr_hash unchanged), price_rules takes the modes of a
// global price (a custom price is written as one), and
// a rerun writes nothing.
func TestMigration0122TierConditions(t *testing.T) {
	const id = "0122_tier_conditions.sql"
	db := testutil.DBThrough(t, migrationBefore(t, id))
	ctx := t.Context()
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := db.SQL.ExecContext(ctx, sql, args...)
		require.NoError(t, err)
	}
	migrate := func() []string {
		t.Helper()
		done, err := store.Migrate(ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
		require.NoError(t, err)
		return done
	}

	const rule = `{"multiplier": 2, "conds": [{"source": "param", "key": "a", "op": "exists"}]}`
	// model: mode, config before, expression (the old generator's), config after.
	prices := map[string][4]string{
		"one": {"per_token", `{"tiers": [{"name": "base", "max_len": null, "unit": "token", "p": 3, "c": 15, "cr": 0.3}], "rules": []}`,
			`tier("base", p*3 + c*15 + cr*0.3)`,
			`{"tiers": [{"name": "base", "unit": "token", "p": 3, "c": 15, "cr": 0.3}], "rules": []}`},
		"one-no-key": {"per_token", `{"tiers": [{"name": "base", "unit": "request", "price": 0.1}], "rules": []}`,
			`tier("base", flat(0.1))`,
			`{"tiers": [{"name": "base", "unit": "request", "price": 0.1}], "rules": []}`},
		"two": {"per_token", `{"tiers": [{"name": "standard", "max_len": 200000, "unit": "token", "p": 3, "c": 15},
				{"name": "long_context", "max_len": null, "unit": "token", "p": 6, "c": 22.5}], "rules": []}`,
			`len <= 200000 ? tier("standard", p*3 + c*15) : tier("long_context", p*6 + c*22.5)`,
			`{"tiers": [{"name": "standard", "when": {"var": "len", "op": "<=", "value": 200000}, "unit": "token", "p": 3, "c": 15},
				{"name": "long_context", "unit": "token", "p": 6, "c": 22.5}], "rules": []}`},
		"four-unsorted": {"per_token", `{"tiers": [{"name": "rest", "max_len": null, "p": 4}, {"name": "mid", "max_len": "128000", "p": 2},
				{"name": "short", "max_len": 32000, "p": 1}, {"name": "long", "max_len": 256000, "unit": "request", "price": 0.5}], "rules": [` + rule + `]}`,
			`(len <= 32000 ? tier("short", p*1 + c*0) : (len <= 128000 ? tier("mid", p*2 + c*0) : (len <= 256000 ? tier("long", flat(0.5)) : tier("rest", p*4 + c*0)))) * (param("a") != nil ? 2 : 1)`,
			`{"tiers": [{"name": "short", "when": {"var": "len", "op": "<=", "value": 32000}, "p": 1},
				{"name": "mid", "when": {"var": "len", "op": "<=", "value": "128000"}, "p": 2},
				{"name": "long", "when": {"var": "len", "op": "<=", "value": 256000}, "unit": "request", "price": 0.5},
				{"name": "rest", "p": 4}], "rules": [` + rule + `]}`},
		"request": {"per_request", `{"price": 0.04}`, `tier("base", flat(0.04))`,
			`{"tiers": [{"name": "base", "unit": "request", "price": 0.04}], "rules": []}`},
		"request-rules": {"per_request", `{"price": "0.5", "rules": [` + rule + `]}`, `(tier("base", flat(0.5))) * (param("a") != nil ? 2 : 1)`,
			`{"tiers": [{"name": "base", "unit": "request", "price": "0.5"}], "rules": [` + rule + `]}`},
		"hand-written": {"expression", `{}`, `tier("x", p*2)`, `{}`},
	}
	for model, p := range prices {
		exec(`INSERT INTO model_prices (model, mode, config, expression, expr_hash, source) VALUES ($1, $2, $3, $4, $5, 'manual')`,
			model, p[0], p[1], p[2], expr.Hash(p[2]))
	}
	exec(`INSERT INTO groups (name) VALUES ('tiers')`)
	custom := map[string][4]string{
		"one":     {"per_token", `{"tiers": [{"name": "base", "max_len": null, "unit": "token", "p": 1, "c": 2}], "rules": []}`, `tier("base", p*1 + c*2)`, `{"tiers": [{"name": "base", "unit": "token", "p": 1, "c": 2}], "rules": []}`},
		"request": {"per_request", `{"price": 0.01}`, `tier("base", flat(0.01))`, `{"tiers": [{"name": "base", "unit": "request", "price": 0.01}], "rules": []}`},
	}
	for model, p := range custom {
		exec(`INSERT INTO price_rules (group_id, model, kind, mode, config, expression, expr_hash) SELECT id, $1, 'custom', $2, $3, $4, $5 FROM groups`,
			model, p[0], p[1], p[2], expr.Hash(p[2]))
	}
	exec(`INSERT INTO price_rules (group_id, model, kind, multiplier) SELECT id, 'two', 'multiplier', 1.5 FROM groups`)

	require.Equal(t, migrationsAfter(t, migrationBefore(t, id)), migrate())

	type row struct {
		Model, Mode, Expression, ExprHash string
		Config                            json.RawMessage
	}
	read := func(table string, where string) map[string]row {
		t.Helper()
		var rows []row
		require.NoError(t, db.Gorm.WithContext(ctx).Table(table).Where(where).Select("model, mode, config, expression, expr_hash").Scan(&rows).Error)
		out := map[string]row{}
		for _, r := range rows {
			out[r.Model] = r
		}
		return out
	}
	check := func(table string, want map[string][4]string, got map[string]row) {
		t.Helper()
		require.Len(t, got, len(want), table)
		for model, w := range want {
			r := got[model]
			mode := expr.ModeVisual
			if w[0] == expr.ModeExpression {
				mode = w[0]
			}
			require.Equal(t, mode, r.Mode, "%s %s", table, model)
			require.JSONEq(t, w[3], string(r.Config), "%s %s", table, model)
			require.Equal(t, w[2], r.Expression, "%s %s: the expression stays", table, model)
			require.Equal(t, expr.Hash(w[2]), r.ExprHash, "%s %s: the hash stays", table, model)
			gen, err := expr.Generate(r.Mode, r.Config)
			require.NoError(t, err, "%s %s", table, model)
			if mode == expr.ModeVisual {
				require.Equal(t, r.ExprHash, expr.Hash(gen), "%s %s: the config generates the stored expression", table, model)
			}
		}
	}
	check("model_prices", prices, read("model_prices", "true"))
	check("price_rules", custom, read("price_rules", "true"))

	// price_rules takes the modes of a global price only.
	_, err := db.SQL.ExecContext(ctx, `INSERT INTO price_rules (group_id, model, mode, config, expression, expr_hash)
		SELECT g.id, 'old-mode', 'per_token', '{}', 'tier("x", p)', 'h' FROM groups g`)
	require.ErrorContains(t, err, "price_rules_mode_check")
	for _, mode := range []string{expr.ModeVisual, expr.ModeExpression} {
		exec(`INSERT INTO price_rules (group_id, model, mode, config, expression, expr_hash)
			SELECT id, 'new-mode', '` + mode + `', '{}', 'tier("x", p)', 'h' FROM groups`)
		exec(`DELETE FROM price_rules WHERE model = 'new-mode'`)
	}

	// A rerun writes no row of either table, and leaves no function behind.
	snapshot := func() map[string]string {
		t.Helper()
		out := map[string]string{}
		for _, table := range []string{"model_prices", "price_rules"} {
			var s string
			require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT string_agg(x::text, E'\n' ORDER BY x::text) FROM (SELECT t.*, t.xmin FROM `+table+` t) x`).Scan(&s))
			out[table] = s
		}
		return out
	}
	before := snapshot()
	exec(`DELETE FROM schema_migrations WHERE id = '` + id + `'`)
	require.Equal(t, []string{id}, migrate())
	require.Equal(t, before, snapshot())
	var functions int
	require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM pg_proc WHERE proname LIKE 'tier_conditions_%'`).Scan(&functions))
	require.Zero(t, functions)
}
