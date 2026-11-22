package billing

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// sequentialRuleChanges is the statements of each change one after the
// other, in the order applyChanges sorts the changes in (the reference
// writeRules must match).
func sequentialRuleChanges(ctx context.Context, tx *gorm.DB, changes []ruleChange, updatedBy int64) error {
	slices.SortStableFunc(changes, func(a, b ruleChange) int {
		return cmp.Or(cmp.Compare(a.on.GroupID, b.on.GroupID), cmp.Compare(a.on.Model, b.on.Model))
	})
	for _, ch := range changes {
		var err error
		if ch.rule == nil {
			_, err = store.SQLTx(tx).ExecContext(ctx, `DELETE FROM price_rules WHERE group_id = $1 AND model = $2`, ch.on.GroupID, ch.on.Model)
		} else {
			err = sequentialSave(ctx, tx, ch.on, ch.rule, updatedBy)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// sequentialSave writes r on on, and its expression to the history, by one
// statement each.
func sequentialSave(ctx context.Context, tx *gorm.DB, on core.PriceQuery, r *storedRule, updatedBy int64) error {
	prog, err := expr.CompileCached(r.Expression)
	if err != nil {
		return err
	}
	if _, err := store.SQLTx(tx).ExecContext(ctx, `INSERT INTO model_price_history (expr_hash, expression, expr_version) VALUES ($1, $2, $3)
		ON CONFLICT (expr_hash) DO NOTHING`, prog.Hash(), prog.Expression(), prog.Version()); err != nil {
		return err
	}
	_, err = store.SQLTx(tx).ExecContext(ctx, `
		INSERT INTO price_rules (group_id, model, mode, config, expression, expr_hash, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (group_id, model) DO UPDATE SET mode = EXCLUDED.mode, config = EXCLUDED.config,
			expression = EXCLUDED.expression, expr_hash = EXCLUDED.expr_hash, updated_by = EXCLUDED.updated_by, updated_at = now()`,
		on.GroupID, on.Model, r.Mode, r.Config, r.Expression, r.exprHash, nullID(updatedBy))
	return err
}

// ruleState is the rules of the groups and the history, with each rule's id
// as "kept" (one of initial) or "new".
func ruleState(t *testing.T, ctx context.Context, tx *gorm.DB, groups []int64, initial map[int64]bool) []string {
	t.Helper()
	rows, err := store.SQLTx(tx).QueryContext(ctx, `SELECT id, group_id, model, mode, config::text, expression, expr_hash,
		COALESCE(updated_by, 0), updated_at = now()
		FROM price_rules WHERE group_id = ANY($1) ORDER BY group_id, model`, store.Int64s(groups))
	require.NoError(t, err)
	var state []string
	for rows.Next() {
		var id, group, by int64
		var model, mode, config, expression, hash string
		var touched bool
		require.NoError(t, rows.Scan(&id, &group, &model, &mode, &config, &expression, &hash, &by, &touched))
		identity := "new"
		if initial[id] {
			identity = "kept"
		}
		state = append(state, fmt.Sprintf("%d %s %s %s %s %s by %d touched %v %s", group, model, mode, config, expression, hash, by, touched, identity))
	}
	require.NoError(t, rows.Err())
	rows.Close()
	rows, err = store.SQLTx(tx).QueryContext(ctx, `SELECT expr_hash || ' ' || expression || ' ' || expr_version FROM model_price_history ORDER BY expr_hash`)
	require.NoError(t, err)
	history, err := testutil.Column[string](rows)
	require.NoError(t, err)
	return append(state, history...)
}

// ruleOn is model in group.
func ruleOn(group int64, model string) core.PriceQuery {
	return core.PriceQuery{GroupID: group, Model: model}
}

// seedRules saves the rules there before each case, and returns their ids.
func seedRules(t *testing.T, e *pricingEnv) map[int64]bool {
	g, p := e.open, e.private
	seed := []ruleChange{
		{ruleOn(g, "claude-haiku-4-5"), requestPrice(t, 1.1)},
		{ruleOn(p, "claude-sonnet-4-5"), requestPrice(t, 0.3)},
		{ruleOn(p, "web-search"), requestPrice(t, 2)},
		{ruleOn(g, "web-search"), requestPrice(t, 0.02)},
	}
	ctx := context.Background()
	require.NoError(t, e.db.Tx(ctx, func(tx *gorm.DB) error { return applyChanges(ctx, tx, seed, e.admin) }))
	rows, err := e.db.SQL.QueryContext(ctx, `SELECT id FROM price_rules`)
	require.NoError(t, err)
	ids, err := testutil.Column[int64](rows)
	require.NoError(t, err)
	initial := map[int64]bool{}
	for _, id := range ids {
		initial[id] = true
	}
	require.Len(t, initial, len(seed))
	return initial
}

// ruleWriteCases are the changes of one request, by name.
func ruleWriteCases(t *testing.T, g, p int64) map[string][]ruleChange {
	price := func(v float64) *storedRule { return requestPrice(t, v) }
	return map[string][]ruleChange{
		"mixed": {
			{ruleOn(p, "claude-haiku-4-5"), price(0.4)}, {ruleOn(g, "claude-haiku-4-5"), price(1.3)},
			{ruleOn(p, "claude-sonnet-4-5"), nil}, {ruleOn(g, "unpriced"), nil}, {ruleOn(g, "web-search"), price(1.75)},
		},
		"removed then saved": {{ruleOn(g, "claude-haiku-4-5"), nil}, {ruleOn(g, "claude-haiku-4-5"), price(1.5)}},
		"saved then removed": {{ruleOn(p, "web-search"), price(3)}, {ruleOn(p, "web-search"), nil}},
		"saved twice":        {{ruleOn(p, "claude-sonnet-4-5"), price(0.5)}, {ruleOn(p, "claude-sonnet-4-5"), price(1.2)}},
		"new, removed, saved": {
			{ruleOn(g, "claude-sonnet-4-5"), price(2)}, {ruleOn(g, "claude-sonnet-4-5"), nil}, {ruleOn(g, "claude-sonnet-4-5"), price(0.7)},
		},
		"same expression twice": {{ruleOn(g, "claude-sonnet-4-5"), price(0.9)}, {ruleOn(p, "claude-haiku-4-5"), price(0.9)}},
		"nothing":               {},
	}
}

// writeRules leaves what the statements of the changes left one after
// the other: the rules (kept or new rows), their values, updated_by and
// updated_at, and the history, also when a request changes one rule twice.
func TestWriteRulesMatchesSequential(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	initial := seedRules(t, e)
	groups := []int64{e.open, e.private}
	for name, changes := range ruleWriteCases(t, e.open, e.private) {
		t.Run(name, func(t *testing.T) {
			want := writtenState(t, e, sequentialRuleChanges, changes, groups, initial)
			require.Equal(t, want, writtenState(t, e, applyChanges, changes, groups, initial))
		})
	}
}

// writtenState is the state apply leaves of changes, rolled back after.
func writtenState(t *testing.T, e *pricingEnv, apply func(context.Context, *gorm.DB, []ruleChange, int64) error,
	changes []ruleChange, groups []int64, initial map[int64]bool) (state []string) {
	ctx := context.Background()
	err := e.db.Tx(ctx, func(tx *gorm.DB) error {
		if err := apply(ctx, tx, slices.Clone(changes), e.member); err != nil {
			return err
		}
		state = ruleState(t, ctx, tx, groups, initial)
		return errRollback
	})
	require.ErrorIs(t, err, errRollback)
	return state
}

var errRollback = errors.New("rollback")

// requestPrice is a stored model price of p per request.
func requestPrice(t *testing.T, p float64) *storedRule {
	t.Helper()
	config := json.RawMessage(fmt.Sprintf(`{"tiers": [{"name": "base", "unit": "request", "price": %v}]}`, p))
	r, err := checkRule(context.Background(), Rule{Mode: "visual", Config: config}, nil)
	require.NoError(t, err)
	return r
}

// Two requests changing the same rules in opposite orders lock them in one
// order (writeRules' lock) and never deadlock.
func TestWriteRulesConcurrentNoDeadlock(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	ctx := context.Background()
	price := func(p float64) *storedRule { return requestPrice(t, p) }
	a := core.PriceQuery{GroupID: e.open, Model: "claude-haiku-4-5"}
	b := core.PriceQuery{GroupID: e.open, Model: "web-search"}
	c := core.PriceQuery{GroupID: e.private, Model: "claude-haiku-4-5"}
	require.NoError(t, e.db.Tx(ctx, func(tx *gorm.DB) error {
		return applyChanges(ctx, tx, []ruleChange{{a, price(1)}, {b, price(1)}, {c, price(1)}}, e.admin)
	}))
	requests := [][]ruleChange{
		{{c, price(2)}, {b, nil}, {a, price(2)}},
		{{a, nil}, {b, price(3)}, {c, nil}},
	}
	for range 30 {
		var wg sync.WaitGroup
		errs := make([]error, len(requests))
		for i, changes := range requests {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = e.db.Tx(ctx, func(tx *gorm.DB) error { return applyChanges(ctx, tx, slices.Clone(changes), e.admin) })
			}()
		}
		wg.Wait()
		for _, err := range errs {
			require.NoError(t, err)
		}
	}
}
