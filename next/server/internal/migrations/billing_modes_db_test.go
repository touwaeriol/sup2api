package migrations_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/migrations"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// 0121 leaves per_request, per_token and expression prices (CONTRACTS
// §131): flat per_token configs get one tier, visual expression configs that
// fit become per_token, every other expression config is {}, and request
// rules are written as factors, "A ||| B" as "(A) * (B)", with the hash Go
// computes; old history rows stay under their hashes. A rerun changes
// nothing.
func TestMigration0121BillingModes(t *testing.T) {
	const id = "0121_billing_modes.sql"
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

	const (
		rules    = `tier("base", p*1 + c*2) ||| has(header("anthropic-beta"), "fast") ? 2 : 1 ||| param("n") == 4 ? 3 : 1 ||| has(param("model"), "mini") ? 0.5 : 1 ||| (hour("UTC") >= 22 || hour("UTC") < 6) ? 0.8 : 1`
		ranged   = `tier("base", p*1 + c*0) ||| (param("max_tokens") ?? 0) >= 1000 ? 1.1 : 1`
		literal  = `tier("x", p) ||| header("x-tag") == "a|||b" ? 2 : 1`
		comment  = "tier(\"x\", p) // the \"base\"\n||| param(\"a\") == 1 ? 2 : 1"
		twoTiers = `len <= 200000 ? tier("standard", p*3 + c*15 + cr*0.3) : tier("long_context", p*6 + c*22.5 + cr*0.6)`
		video    = `tier("video", c * (u("video_input") == true ? 4.3 : 7))`
	)
	prices := []struct{ model, mode, config, expression string }{
		{"pt-flat", "per_token", `{"p": 3, "c": 15, "cr": 0.3, "cc": 3.75, "cc1h": 6}`, `tier("base", p*3 + c*15 + cr*0.3 + cc*3.75 + cc1h*6)`},
		{"pt-strings", "per_token", `{"p": "0.15", "c": "0.6"}`, `tier("base", p*0.15 + c*0.6)`},
		{"pr", "per_request", `{"price": 0.04}`, `tier("base", flat(0.04))`},
		{"vis-synced", "expression", `{"tiers": [{"name": "standard", "max_len": 200000, "p": 3, "c": 15, "cr": 0.3},
			{"name": "long_context", "max_len": null, "p": 6, "c": 22.5, "cr": 0.6}]}`, "v1:" + twoTiers},
		{"vis-rules", "expression", `{"tiers": [{"name": "base", "prices": {"p": 1, "c": 2}, "p": 9, "flat": 0}], "rules": [
			{"source": "header", "name": "Anthropic-Beta", "value": "fast", "multiplier": 2},
			{"kind": "param", "name": "n", "value": 4, "multiplier": 3},
			{"source": "param", "path": "model", "op": "contains", "value": "mini", "multiplier": "0.5"},
			{"source": "time", "from_hour": 22, "to_hour": 6, "multiplier": 0.8}]}`, rules},
		{"vis-one", "expression", `{"tiers": [{"name": "only", "max_len": 1000, "p": 1}]}`, `tier("only", p*1 + c*0)`},
		{"vis-flat", "expression", `{"tiers": [{"name": "s", "p": 1, "c": 2, "flat": 0.001}]}`, `tier("s", flat(0.001) + p*1 + c*2)`},
		{"vis-range", "expression", `{"tiers": [{"name": "base", "p": 1}], "rules": [
			{"source": "param", "path": "max_tokens", "op": "range", "value": [1000, null], "multiplier": 1.1}]}`, ranged},
		{"video", "expression", `{"video": {"price_per_million_tokens": 7, "video_input_price_per_million_tokens": 4.3}}`, video},
		{"src-literal", "expression", `{}`, literal},
		{"src-comment", "expression", `{}`, comment},
	}
	for _, p := range prices {
		exec(`INSERT INTO model_prices (model, mode, config, expression, expr_hash, source) VALUES ($1, $2, $3, $4, $5, 'manual')`,
			p.model, p.mode, p.config, p.expression, expr.Hash(p.expression))
		exec(`INSERT INTO model_price_history (expr_hash, expression, expr_version) VALUES ($1, $2, 1) ON CONFLICT DO NOTHING`,
			expr.Hash(p.expression), "v1:"+strings.TrimPrefix(p.expression, "v1:"))
	}
	exec(`INSERT INTO groups (name) VALUES ('modes')`)
	exec(`INSERT INTO price_rules (group_id, model, kind, mode, config, expression, expr_hash)
		SELECT id, 'pt-flat', 'custom', 'per_token', '{"p": 1, "c": 2, "cr": null, "cc": null, "cc1h": null}', 'tier("base", p*1 + c*2)', $1 FROM groups`,
		expr.Hash(`tier("base", p*1 + c*2)`))
	exec(`INSERT INTO price_rules (group_id, model, kind, mode, config, expression, expr_hash)
		SELECT id, 'pr', 'custom', 'per_request', '{"price": 0.5}', 'tier("base", flat(0.5))', $1 FROM groups`, expr.Hash(`tier("base", flat(0.5))`))
	exec(`INSERT INTO price_rules (group_id, model, kind, multiplier) SELECT id, 'vis-rules', 'multiplier', 1.5 FROM groups`)
	pending, _ := json.Marshal(map[string]any{"inputs": map[string]any{
		"semantics": "exclusive",
		"pricing":   map[string]any{"base": map[string]any{"expression": ranged, "expr_hash": expr.Hash(ranged)}},
		"additional": []any{
			map[string]any{"Kind": "advisor", "Price": map[string]any{"Expression": literal, "ExprHash": expr.Hash(literal),
				"Base": map[string]any{"expression": rules, "expr_hash": expr.Hash(rules)}}},
			map[string]any{"Kind": "free", "Free": true},
		},
	}})
	usage := func(request, status string) {
		exec(`INSERT INTO usage_logs (request_id, user_id, api_key_id, group_id, protocol, endpoint, status_code, success, billing_status, expr_hash, billing_detail)
			VALUES ($1, 1, 1, 1, 'p', '/e', 200, true, $2, $3, $4)`, request, status, expr.Hash(rules), string(pending))
	}
	usage("pending", "pending")
	usage("billed", "billed")

	require.Equal(t, migrationsAfter(t, migrationBefore(t, id)), migrate())
	type row struct {
		Model, Mode, Expression, ExprHash string
		Config                            json.RawMessage
	}
	read := func() map[string]row {
		t.Helper()
		var rows []row
		require.NoError(t, db.Gorm.WithContext(ctx).Table("model_prices").Select("model, mode, config, expression, expr_hash").Scan(&rows).Error)
		out := map[string]row{}
		for _, r := range rows {
			out[r.Model] = r
		}
		return out
	}
	got := read()
	product := func(parts ...string) string {
		out := "(" + parts[0] + ")"
		for _, p := range parts[1:] {
			out += " * (" + p + ")"
		}
		return out
	}
	rulesAfter := product(`tier("base", p*1 + c*2)`, `has(header("anthropic-beta"), "fast") ? 2 : 1`, `param("n") == 4 ? 3 : 1`,
		`has(param("model"), "mini") ? 0.5 : 1`, `(hour("UTC") >= 22 || hour("UTC") < 6) ? 0.8 : 1`)
	want := map[string]struct{ mode, config, expression string }{
		"pt-flat":    {"per_token", `{"tiers": [{"c": 15, "p": 3, "cc": 3.75, "cr": 0.3, "cc1h": 6, "name": "base", "unit": "token", "max_len": null}], "rules": []}`, prices[0].expression},
		"pt-strings": {"per_token", `{"tiers": [{"c": "0.6", "p": "0.15", "name": "base", "unit": "token", "max_len": null}], "rules": []}`, prices[1].expression},
		"pr":         {"per_request", `{"price": 0.04}`, prices[2].expression},
		"vis-synced": {"per_token", `{"tiers": [{"c": 15, "p": 3, "cr": 0.3, "name": "standard", "unit": "token", "max_len": 200000},
			{"c": 22.5, "p": 6, "cr": 0.6, "name": "long_context", "unit": "token", "max_len": null}], "rules": []}`, "v1:" + twoTiers},
		"vis-rules": {"per_token", `{"tiers": [{"c": 2, "p": 1, "name": "base", "unit": "token", "max_len": null}], "rules": [
			{"conds": [{"op": "contains", "key": "Anthropic-Beta", "value": "fast", "source": "header"}], "multiplier": 2},
			{"conds": [{"op": "eq", "key": "n", "value": 4, "source": "param"}], "multiplier": 3},
			{"conds": [{"op": "contains", "key": "model", "value": "mini", "source": "param"}], "multiplier": "0.5"},
			{"conds": [{"source": "time", "to_hour": 6, "from_hour": 22}], "multiplier": 0.8}]}`, rulesAfter},
		"vis-one":     {"per_token", `{"tiers": [{"p": 1, "name": "only", "unit": "token", "max_len": null}], "rules": []}`, prices[5].expression},
		"vis-flat":    {"expression", `{}`, prices[6].expression},
		"vis-range":   {"expression", `{}`, product(`tier("base", p*1 + c*0)`, `(param("max_tokens") ?? 0) >= 1000 ? 1.1 : 1`)},
		"video":       {"expression", `{}`, video},
		"src-literal": {"expression", `{}`, product(`tier("x", p)`, `header("x-tag") == "a|||b" ? 2 : 1`)},
		"src-comment": {"expression", `{}`, "(tier(\"x\", p) // the \"base\"\n) * (param(\"a\") == 1 ? 2 : 1)"},
	}
	for model, w := range want {
		r := got[model]
		require.Equal(t, w.mode, r.Mode, model)
		require.JSONEq(t, w.config, string(r.Config), model)
		require.Equal(t, w.expression, r.Expression, model)
		require.Equal(t, expr.Hash(r.Expression), r.ExprHash, "%s: the hash computed in SQL is Go's", model)
		prog := mustCompile(t, r.Expression)
		if r.Mode != "expression" {
			gen, err := expr.Generate(r.Mode, r.Config)
			require.NoError(t, err, model)
			require.Equal(t, prog.Hash(), expr.Hash(gen), "%s: the config generates the stored expression", model)
		}
	}
	// Prices without a separator kept their expression and hash.
	for _, p := range prices {
		if r := got[p.model]; r.Expression == p.expression {
			require.Equal(t, expr.Hash(p.expression), r.ExprHash, p.model)
		}
	}
	// Same cost as the old engine's base times the matched multipliers.
	res, err := mustCompile(t, got["vis-rules"].Expression).Eval(expr.Input{Vars: expr.Vars{P: 1e6},
		Headers: map[string]string{"anthropic-beta": "fast-mode"}, Params: map[string]string{"n": "4"}})
	require.NoError(t, err)
	require.Equal(t, "6", res.Cost.String())
	require.Len(t, res.Rules, 4)

	var custom []struct {
		Mode, Expression, ExprHash string
		Config                     json.RawMessage
	}
	require.NoError(t, db.Gorm.WithContext(ctx).Table("price_rules").Where("kind = 'custom'").Order("model DESC").
		Select("mode, config, expression, expr_hash").Scan(&custom).Error)
	require.Len(t, custom, 2)
	require.JSONEq(t, `{"tiers": [{"c": 2, "p": 1, "name": "base", "unit": "token", "max_len": null}], "rules": []}`, string(custom[0].Config))
	require.JSONEq(t, `{"price": 0.5}`, string(custom[1].Config))
	for _, r := range custom {
		gen, err := expr.Generate(r.Mode, r.Config)
		require.NoError(t, err)
		require.Equal(t, r.Expression, gen)
		require.Equal(t, expr.Hash(gen), r.ExprHash)
	}

	// History: the new hashes are there, and an old hash still resolves to
	// an expression that compiles to the same price.
	history := func(hash string) string {
		t.Helper()
		var text string
		require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT expression FROM model_price_history WHERE expr_hash = $1`, hash).Scan(&text))
		return text
	}
	for _, model := range []string{"vis-rules", "vis-range", "src-literal", "src-comment"} {
		r := got[model]
		require.Equal(t, mustCompile(t, r.Expression).Expression(), history(r.ExprHash), model)
	}
	require.Equal(t, "v1:"+rulesAfter, history(expr.Hash(rules)))
	require.Equal(t, mustCompile(t, twoTiers).Expression(), history(expr.Hash(twoTiers)))

	// Unsettled usage carries the new expressions; settled usage is history.
	detail := func(request string) string {
		t.Helper()
		var d string
		require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT billing_detail::text FROM usage_logs WHERE request_id = $1`, request).Scan(&d))
		return d
	}
	require.JSONEq(t, string(pending), detail("billed"))
	var after struct {
		Inputs struct {
			Pricing struct {
				Base struct {
					Expression string `json:"expression"`
					ExprHash   string `json:"expr_hash"`
				} `json:"base"`
			} `json:"pricing"`
			Additional []struct {
				Kind  string
				Free  bool
				Price *struct {
					Expression, ExprHash string
					Base                 struct {
						Expression string `json:"expression"`
						ExprHash   string `json:"expr_hash"`
					}
				}
			} `json:"additional"`
		} `json:"inputs"`
	}
	require.NoError(t, json.Unmarshal([]byte(detail("pending")), &after))
	in := after.Inputs
	require.Equal(t, got["vis-range"].Expression, in.Pricing.Base.Expression)
	require.Equal(t, got["vis-range"].ExprHash, in.Pricing.Base.ExprHash)
	require.Len(t, in.Additional, 2)
	require.Equal(t, got["src-literal"].Expression, in.Additional[0].Price.Expression)
	require.Equal(t, got["src-literal"].ExprHash, in.Additional[0].Price.ExprHash)
	require.Equal(t, rulesAfter, in.Additional[0].Price.Base.Expression)
	require.Equal(t, expr.Hash(rulesAfter), in.Additional[0].Price.Base.ExprHash)
	require.True(t, in.Additional[1].Free)
	require.Nil(t, in.Additional[1].Price)

	before := read()
	exec(`DELETE FROM schema_migrations WHERE id = '` + id + `'`)
	require.Equal(t, []string{id}, migrate())
	require.Equal(t, before, read())
}

func mustCompile(t *testing.T, src string) *expr.Program {
	t.Helper()
	p, err := expr.Compile(src)
	require.NoError(t, err, src)
	return p
}
