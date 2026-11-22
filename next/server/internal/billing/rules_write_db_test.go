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
		on, r := ch.on, ch.rule
		if r == nil {
			if _, err := store.SQLTx(tx).ExecContext(ctx, `DELETE FROM price_rules WHERE group_id = $1 AND model = $2`,
				on.GroupID, on.Model); err != nil {
				return err
			}
			continue
		}
		prog, err := expr.CompileCached(r.Expression)
		if err != nil {
			return err
		}
		if _, err := store.SQLTx(tx).ExecContext(ctx, `INSERT INTO model_price_history (expr_hash, expression, expr_version) VALUES ($1, $2, $3)
			ON CONFLICT (expr_hash) DO NOTHING`, prog.Hash(), prog.Expression(), prog.Version()); err != nil {
			return err
		}
		if _, err := store.SQLTx(tx).ExecContext(ctx, `
			INSERT INTO price_rules (group_id, model, mode, config, expression, expr_hash, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (group_id, model) DO UPDATE SET mode = EXCLUDED.mode, config = EXCLUDED.config,
				expression = EXCLUDED.expression, expr_hash = EXCLUDED.expr_hash, updated_by = EXCLUDED.updated_by, updated_at = now()`,
			on.GroupID, on.Model, r.Mode, r.Config, r.Expression, r.exprHash, nullID(updatedBy)); err != nil {
			return err
		}
	}
	return nil
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

// writeRules leaves what the statements of the changes left one after
// the other: the rules (kept or new rows), their values, updated_by and
// updated_at, and the history, also when a request changes one rule twice.
func TestWriteRulesMatchesSequential(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	ctx := context.Background()
	price := func(p float64) *storedRule { return requestPrice(t, p) }
	g, p := e.open, e.private
	groups := []int64{g, p}
	on := func(group int64, model string) core.PriceQuery {
		return core.PriceQuery{GroupID: group, Model: model}
	}
	// The rules there before each case.
	seed := []ruleChange{
		{on(g, "claude-haiku-4-5"), price(1.1)},
		{on(p, "claude-sonnet-4-5"), price(0.3)},
		{on(p, "web-search"), price(2)},
		{on(g, "web-search"), price(0.02)},
	}
	require.NoError(t, e.db.Tx(ctx, func(tx *gorm.DB) error { return applyChanges(ctx, tx, slices.Clone(seed), e.admin) }))
	initial := map[int64]bool{}
	rows, err := e.db.SQL.QueryContext(ctx, `SELECT id FROM price_rules`)
	require.NoError(t, err)
	ids, err := testutil.Column[int64](rows)
	require.NoError(t, err)
	for _, id := range ids {
		initial[id] = true
	}
	require.Len(t, initial, len(seed))

	cases := map[string][]ruleChange{
		"mixed": {
			{on(p, "claude-haiku-4-5"), price(0.4)}, {on(g, "claude-haiku-4-5"), price(1.3)},
			{on(p, "claude-sonnet-4-5"), nil}, {on(g, "unpriced"), nil}, {on(g, "web-search"), price(1.75)},
		},
		"removed then saved": {{on(g, "claude-haiku-4-5"), nil}, {on(g, "claude-haiku-4-5"), price(1.5)}},
		"saved then removed": {{on(p, "web-search"), price(3)}, {on(p, "web-search"), nil}},
		"saved twice":        {{on(p, "claude-sonnet-4-5"), price(0.5)}, {on(p, "claude-sonnet-4-5"), price(1.2)}},
		"new, removed, saved": {
			{on(g, "claude-sonnet-4-5"), price(2)}, {on(g, "claude-sonnet-4-5"), nil}, {on(g, "claude-sonnet-4-5"), price(0.7)},
		},
		"same expression twice": {{on(g, "claude-sonnet-4-5"), price(0.9)}, {on(p, "claude-haiku-4-5"), price(0.9)}},
		"nothing":               {},
	}
	for name, changes := range cases {
		t.Run(name, func(t *testing.T) {
			var want, got []string
			for i, apply := range []func(context.Context, *gorm.DB, []ruleChange, int64) error{sequentialRuleChanges, applyChanges} {
				err := e.db.Tx(ctx, func(tx *gorm.DB) error {
					if err := apply(ctx, tx, slices.Clone(changes), e.member); err != nil {
						return err
					}
					state := ruleState(t, ctx, tx, groups, initial)
					if i == 0 {
						want = state
					} else {
						got = state
					}
					return errRollback
				})
				require.ErrorIs(t, err, errRollback)
			}
			require.Equal(t, want, got)
		})
	}
}

var errRollback = errors.New("rollback")

// requestPrice is a stored model price of p per request.
func requestPrice(t *testing.T, p float64) *storedRule {
	t.Helper()
	config := json.RawMessage(fmt.Sprintf(`{"tiers": [{"name": "base", "unit": "request", "price": %v}]}`, p))
	r, err := checkRule(context.Background(), Rule{Kind: ruleCustom, Mode: "visual", Config: config}, nil)
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
