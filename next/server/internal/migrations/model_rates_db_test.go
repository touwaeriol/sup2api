package migrations_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const migration0124 = "0124_model_rates.sql"

// newRates0124 seeds account groups g1 (base rate 2), g2 (1), g3 (3.3333,
// whose rates round) and g4 (999999.9999, whose rates are capped); the
// user groups vip (discount 0.8, granted g2 without a rate), plain (1), own
// (0.5, rate 3 in g1) and a personal one (0.5); a multiplier rule in each
// account group and one custom price.
func newRates0124(t *testing.T) *migrationDB {
	m := newMigrationDB(t, migration0124)
	m.exec(`INSERT INTO groups (name, rate_multiplier) VALUES ('g1', 2), ('g2', 1), ('g3', 3.3333), ('g4', 999999.9999);
		INSERT INTO users (email, password_hash) VALUES ('solo@x', '');
		INSERT INTO segments (name, discount) VALUES ('vip', 0.8), ('plain', 1), ('own', 0.5);
		INSERT INTO segments (kind, user_id, discount) SELECT 'personal', id, 0.5 FROM users WHERE email = 'solo@x';
		INSERT INTO segment_groups (segment_id, group_id, granted, rate_override)
			SELECT s.id, g.id, true, NULL FROM segments s, groups g WHERE (s.name, g.name) = ('vip', 'g2')
			UNION ALL SELECT s.id, g.id, false, 3 FROM segments s, groups g WHERE (s.name, g.name) = ('own', 'g1');
		INSERT INTO price_rules (group_id, model, kind, multiplier)
			SELECT id, 'm' || substr(name, 2), 'multiplier', CASE name WHEN 'g2' THEN 0 WHEN 'g4' THEN 2 ELSE 1.5 END FROM groups;
		INSERT INTO price_rules (group_id, model, kind, mode, config, expression, expr_hash)
			SELECT id, 'c1', 'custom', 'expression', '{}', 'flat(1)', 'h' FROM groups WHERE name = 'g1'`)
	return m
}

// segmentName names a user group in rows: "solo" for the personal one.
const segmentName = `coalesce(nullif(s.name, ''), 'solo')`

// A discount becomes the user group's rate in each account group it has
// none in (base rate × discount, rounded to 4 decimals and capped), personal
// ones too; a rate it has stays, and so does the grant.
func TestMigration0124Discounts(t *testing.T) {
	m := newRates0124(t)
	m.migrateFirst()
	require.Equal(t, "own g1 3.0000 f, own g2 0.5000 f, own g3 1.6667 f, own g4 500000.0000 f, "+
		"solo g1 1.0000 f, solo g2 0.5000 f, solo g3 1.6667 f, solo g4 500000.0000 f, "+
		"vip g1 1.6000 f, vip g2 0.8000 t, vip g3 2.6666 f, vip g4 799999.9999 f",
		m.rows(`SELECT concat_ws(' ', `+segmentName+`, g.name, sg.rate_override, sg.granted) FROM segment_groups sg
			JOIN segments s ON s.id = sg.segment_id JOIN groups g ON g.id = sg.group_id`))
	require.Empty(t, m.rows(`SELECT column_name::text FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'segments' AND column_name = 'discount'`))
}

// A multiplier rule becomes the account group's rate for the model (N ×
// base rate) and that of each user group with a rate there (N × that rate,
// a converted discount's too), rounded and capped; price_rules keeps the
// custom prices.
func TestMigration0124MultiplierRules(t *testing.T) {
	m := newRates0124(t)
	m.migrateFirst()
	require.Equal(t, "g1 m1 3.0000, g2 m2 0.0000, g3 m3 5.0000, g4 m4 999999.9999",
		m.rows(`SELECT concat_ws(' ', g.name, r.model, r.rate) FROM group_model_rates r JOIN groups g ON g.id = r.group_id`))
	require.Equal(t, "own m1 4.5000, own m2 0.0000, own m3 2.5001, own m4 999999.9999, "+
		"solo m1 1.5000, solo m2 0.0000, solo m3 2.5001, solo m4 999999.9999, "+
		"vip m1 2.4000, vip m2 0.0000, vip m3 3.9999, vip m4 999999.9999",
		m.rows(`SELECT concat_ws(' ', `+segmentName+`, r.model, r.rate) FROM segment_model_rates r JOIN segments s ON s.id = r.segment_id`))
	require.Equal(t, "g1 c1 flat(1) custom", m.rows(`SELECT concat_ws(' ', g.name, r.model, r.expression, r.kind)
		FROM price_rules r JOIN groups g ON g.id = r.group_id`))
	require.Empty(t, m.rows(`SELECT column_name::text FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'price_rules' AND column_name = 'multiplier'`))
}

// After 0124 price_rules takes custom prices only, with a mode, a config
// and an expression, and a model rate is never negative.
func TestMigration0124Constraints(t *testing.T) {
	m := newRates0124(t)
	m.migrateFirst()
	for _, sql := range []string{
		`INSERT INTO price_rules (group_id, model, kind, mode, config, expression) SELECT id, 'x', 'multiplier', 'expression', '{}', 'flat(1)' FROM groups LIMIT 1`,
		`INSERT INTO price_rules (group_id, model, config, expression) SELECT id, 'x', '{}', 'flat(1)' FROM groups LIMIT 1`,
		`INSERT INTO price_rules (group_id, model, mode, expression) SELECT id, 'x', 'expression', 'flat(1)' FROM groups LIMIT 1`,
		`INSERT INTO price_rules (group_id, model, mode, config) SELECT id, 'x', 'expression', '{}' FROM groups LIMIT 1`,
		`INSERT INTO group_model_rates (group_id, model, rate) SELECT id, 'x', -1 FROM groups LIMIT 1`,
		`INSERT INTO segment_model_rates (segment_id, group_id, model, rate) SELECT s.id, g.id, 'x', -1 FROM segments s, groups g LIMIT 1`,
	} {
		require.Error(t, m.try(sql), sql)
	}
	require.Equal(t, "price_rules_expression, price_rules_kind", m.rows(`SELECT conname::text FROM pg_constraint
		WHERE conrelid = 'price_rules'::regclass AND contype = 'c' AND conname NOT LIKE '%mode%'`))
}

// A rerun writes no row.
func TestMigration0124Rerun(t *testing.T) {
	m := newRates0124(t)
	m.migrateFirst()
	snapshot := func() string {
		return m.rows(`SELECT t || ': ' || x FROM (
			SELECT 'segment_groups', (s.*, s.xmin)::text FROM segment_groups s
			UNION ALL SELECT 'group_model_rates', (r.*, r.xmin)::text FROM group_model_rates r
			UNION ALL SELECT 'segment_model_rates', (r.*, r.xmin)::text FROM segment_model_rates r
			UNION ALL SELECT 'price_rules', (r.*, r.xmin)::text FROM price_rules r) u(t, x)`)
	}
	before := snapshot()
	m.rerun()
	require.Equal(t, before, snapshot())
}
