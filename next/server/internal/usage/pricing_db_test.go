package usage

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// customExpr is a custom price rule's expression. It leaves cache reads
// unpriced, which the fixture's global price charges for.
const customExpr = `tier("custom", p*1 + c*2)`

// segmentSource is the price source of a record priced by a user group's
// rule, frozen before core migration 0123 removed those rules: such records
// still settle, retry and reconcile by their scale and base.
const segmentSource = "segment"

// discountSource is the rate source of a record priced at a user group's
// discount, frozen before core migration 0124 removed discounts: such
// records settle by the rate they were frozen with.
const discountSource = "discount"

// scaled is the fixture's record under a multiplier rule of the user's
// segment (segmentSource), at the segment's discounted rate. At the global
// price the usage costs 0.708.
func (f *fixture) scaled(id, scale string) *core.UsageRecord {
	rec := f.record(id, true)
	rule := *f.price
	rule.Source, rule.Scale = segmentSource, decPtr(scale)
	rec.Price, rec.SegmentID, rec.RateSource, rec.RateMultiplier = &rule, 12, discountSource, dec("1.2")
	return rec
}

// customPrice is a custom rule of the fixture's group: the rule's own
// expression (saved to the price history, as the billing module does when the
// rule is saved), with the global price as its base.
func (f *fixture) customPrice() *core.PriceRule {
	f.t.Helper()
	hash := expr.Hash(customExpr)
	_, err := f.db.SQL.ExecContext(f.t.Context(), `INSERT INTO model_price_history (expr_hash, expression, expr_version) VALUES ($1, $2, 1)
		ON CONFLICT DO NOTHING`, hash, customExpr)
	require.NoError(f.t, err)
	return &core.PriceRule{ID: f.price.ID, Model: f.price.Model, Mode: "visual", Expression: customExpr, ExprVersion: 1, ExprHash: hash,
		Source: core.PriceSourceGroup, Base: &core.PriceBase{Expression: f.price.Expression, ExprHash: f.price.ExprHash}}
}

// customPriced is the fixture's record under customPrice. The list cost of
// the usage is 0.104.
func (f *fixture) customPriced(id string) *core.UsageRecord {
	f.t.Helper()
	rec := f.record(id, true)
	rec.Price, rec.RateMultiplier = f.customPrice(), dec("2")
	return rec
}

// loseHistory empties the price history, where a row's expression is read
// back from by its hash.
func (f *fixture) loseHistory() {
	f.t.Helper()
	_, err := f.db.SQL.ExecContext(f.t.Context(), `DELETE FROM model_price_history`)
	require.NoError(f.t, err)
}

// moneyColumns reads the row's status, charge, base cost and rate as the
// columns hold them.
func (f *fixture) moneyColumns(requestID string) string {
	f.t.Helper()
	return f.scalar(`SELECT billing_status || ' ' || total_cost::text || ' ' || COALESCE(base_cost::text, 'null') || ' ' || rate_multiplier::text
		FROM usage_logs WHERE request_id = $1`, requestID)
}

func (f *fixture) billingDetail(requestID string) gjson.Result {
	f.t.Helper()
	return gjson.Parse(f.scalar(`SELECT billing_detail::text FROM usage_logs WHERE request_id = $1`, requestID))
}

// A record under a price rule: the columns hold the charge, the base cost and
// the rate without the rule's multiplier; billing_detail holds the three
// amounts and the frozen rule, and the detail API returns it.
func TestSettleUnderPriceRule(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	before := f.balance()
	scaled, custom := f.scaled("rule-scale", "0.5"), f.customPriced("rule-custom")
	f.svc.process(ctx, []*core.UsageRecord{scaled, custom})

	require.Equal(t, "billed 0.42480000 0.70800000 1.2000", f.moneyColumns("rule-scale"))  // 0.708 x 0.5 x 1.2
	require.Equal(t, "billed 0.20800000 0.70800000 2.0000", f.moneyColumns("rule-custom")) // 0.104 x 2
	require.True(t, before.Sub(f.balance()).Equal(dec("0.6328")), "charged %s", before.Sub(f.balance()))
	// The custom rule's expression is the row's; the price is still the global one.
	require.Equal(t, expr.Hash(customExpr)+" visual "+strconv.FormatInt(f.price.ID, 10),
		f.scalar(`SELECT expr_hash || ' ' || billing_mode || ' ' || price_id::text FROM usage_logs WHERE request_id = 'rule-custom'`))

	detail := f.billingDetail("rule-scale")
	for path, want := range map[string]string{
		"cost": "0.708", "list_cost": "0.354", "rate_multiplier": "1.2", "total_cost": "0.4248", "tier": "standard",
		"inputs.pricing": `{"scale": "0.5", "segment_id": 12, "rate_source": "discount", "price_source": "segment"}`,
	} {
		require.Equal(t, want, detail.Get(path).String(), "rule-scale billing_detail.%s", path)
	}
	detail = f.billingDetail("rule-custom")
	for path, want := range map[string]string{
		"cost": "0.708", "list_cost": "0.104", "rate_multiplier": "2", "total_cost": "0.208", "tier": "custom",
		"inputs.pricing.price_source": "group", "inputs.pricing.base.expression": a6Expr, "inputs.pricing.base.expr_hash": f.price.ExprHash,
	} {
		require.Equal(t, want, detail.Get(path).String(), "rule-custom billing_detail.%s", path)
	}
	require.False(t, detail.Get("inputs.pricing.rate_source").Exists()) // the group's own rate

	// GET /usage/:id and /me/usage/:id return the document as stored.
	id := f.scalar(`SELECT id::text FROM usage_logs WHERE request_id = 'rule-scale'`)
	for _, path := range []string{"/usage/" + id, "/me/usage/" + id} {
		got := f.get(f.user, path, 200)["data"].(map[string]any)["billing_detail"].(map[string]any)
		require.Equal(t, "0.354", got["list_cost"], path)
		require.Equal(t, map[string]any{"segment_id": float64(12), "rate_source": "discount", "price_source": "segment", "scale": "0.5"},
			got["inputs"].(map[string]any)["pricing"], path)
	}
}

// A row at the global price and the group's own rate is stored as before
// price rules existed, whether or not the record says so explicitly; only the
// base_cost column is new.
func TestUnruledRowIsStoredAsBefore(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	marked := func(id string) *core.UsageRecord {
		rec := f.record(id, true)
		price := *f.price
		price.Source = core.PriceSourceGlobal
		rec.Price, rec.SegmentID, rec.RateSource = &price, 12, core.RateSourceGroup
		return rec
	}
	_, err := f.svc.insert(ctx, []*core.UsageRecord{f.record("plain-pending", true), marked("marked-pending")})
	require.NoError(t, err)
	f.svc.process(ctx, []*core.UsageRecord{f.record("plain", true), marked("marked")})

	stored := func(id string) string {
		return f.scalar(`SELECT (billing_detail - 'ledger_id')::text FROM usage_logs WHERE request_id = $1`, id)
	}
	const pending = `{"inputs": {"headers": {"anthropic-beta": "fast-mode"}, "semantics": "exclusive"}, "attempts": 0}`
	require.Equal(t, pending, stored("plain-pending"))
	require.Equal(t, pending, stored("marked-pending"))
	require.Equal(t, "pending 0.00000000 null 1.0000", f.moneyColumns("marked-pending"))

	require.Equal(t, stored("plain"), stored("marked"))
	detail := f.billingDetail("marked")
	require.Equal(t, "0.708", detail.Get("cost").String())
	require.False(t, detail.Get("list_cost").Exists())
	require.False(t, detail.Get("inputs.pricing").Exists())
	require.Equal(t, "billed 0.70800000 0.70800000 1.0000", f.moneyColumns("marked"))
}

// A retry prices a row with the rule and rate frozen in it: changing or
// deleting the rule, a model rate or the global price after the request does
// not change the charge.
func TestRetryUsesFrozenPriceRule(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	scaled, custom := f.scaled("retry-scale", "0.5"), f.customPriced("retry-custom")
	scaled.Model, custom.Model = "m-scale", "m-custom"
	_, err := f.db.SQL.ExecContext(ctx, `INSERT INTO group_model_rates (group_id, model, rate) VALUES ($1, 'm-scale', 0.5)`, f.group)
	require.NoError(t, err)
	_, err = f.db.SQL.ExecContext(ctx, `INSERT INTO price_rules (group_id, model, mode, config, expression, expr_hash)
		VALUES ($1, 'm-custom', 'visual', '{}', $2, $3)`, f.group, customExpr, expr.Hash(customExpr))
	require.NoError(t, err)

	// One settlement fails, the other never ran (a crash after the insert).
	f.ledger.fail.Store(1)
	f.svc.process(ctx, []*core.UsageRecord{scaled})
	_, err = f.svc.insert(ctx, []*core.UsageRecord{custom})
	require.NoError(t, err)
	require.Equal(t, "failed 0.00000000 null 1.2000", f.moneyColumns("retry-scale"))
	require.Equal(t, "pending 0.00000000 null 2.0000", f.moneyColumns("retry-custom"))

	changePricesAfterward(t, f, scaled, custom)
	time.Sleep(5 * time.Millisecond)
	checkRetriedAtFrozenPrices(t, f)
}

// changePricesAfterward changes the rate, the rule and the global price the
// records were priced by, and the caller's copies of their prices.
func changePricesAfterward(t *testing.T, f *fixture, scaled, custom *core.UsageRecord) {
	changed := `tier("changed", p*999 + c*999)`
	for _, sql := range []string{
		`UPDATE group_model_rates SET rate = 9 WHERE model = 'm-scale'`,
		`DELETE FROM price_rules WHERE model = 'm-custom'`,
		`UPDATE model_prices SET expression = '` + changed + `', expr_hash = '` + expr.Hash(changed) + `'`,
	} {
		_, err := f.db.SQL.ExecContext(t.Context(), sql)
		require.NoError(t, err, sql)
	}
	// The caller's objects are not the frozen ones either.
	*scaled.Price.Scale, custom.Price.Base.Expression = dec("9"), changed
}

// checkRetriedAtFrozenPrices retries both records: each is billed as it was
// priced.
func checkRetriedAtFrozenPrices(t *testing.T, f *fixture) {
	n, err := f.svc.RetryPending(t.Context())
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.Equal(t, "billed 0.42480000 0.70800000 1.2000", f.moneyColumns("retry-scale"))
	require.Equal(t, "billed 0.20800000 0.70800000 2.0000", f.moneyColumns("retry-custom"))
	require.Equal(t, "0.354", f.billingDetail("retry-scale").Get("list_cost").String())
	require.Equal(t, "0.5", f.billingDetail("retry-scale").Get("inputs.pricing.scale").String())
	detail := f.billingDetail("retry-custom")
	require.Equal(t, "0.104", detail.Get("list_cost").String())
	require.Equal(t, a6Expr, detail.Get("inputs.pricing.base.expression").String())
}

// A row whose expression is gone from the price history is priced at the
// current expression of its price, but not under a custom rule: its price is
// the global one, which is not what the request was priced at. It fails
// until the history has the expression again.
func TestRetryNeedsHistoryOfCustomPrice(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	before := f.balance()
	_, err := f.svc.insert(ctx, []*core.UsageRecord{f.scaled("lost-scale", "0.5"), f.customPriced("lost-custom")})
	require.NoError(t, err)
	f.loseHistory()
	time.Sleep(5 * time.Millisecond)

	n, err := f.svc.RetryPending(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.Equal(t, "billed 0.42480000 0.70800000 1.2000", f.moneyColumns("lost-scale"))
	require.Equal(t, "failed 0.00000000 null 2.0000", f.moneyColumns("lost-custom"))
	require.Equal(t, "price expression not found", f.billingDetail("lost-custom").Get("error").String())
	require.True(t, before.Sub(f.balance()).Equal(dec("0.4248")), "charged %s", before.Sub(f.balance()))

	f.customPrice()
	n, err = f.svc.RetryPending(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, "billed 0.20800000 0.70800000 2.0000", f.moneyColumns("lost-custom"))
}

// With items beside it, a custom price's primary usage still needs its
// expression: without it in the history the row fails, as one without items
// does, instead of charging the items and leaving the primary usage free.
func TestRetryNeedsHistoryOfCustomPriceWithItems(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	before := f.balance()
	rec := f.customPriced("lost-custom-items")
	rec.Additional = []core.PricedUsage{{Kind: "advisor", Model: "other-model", Price: &core.PriceRule{Expression: `tier("extra",p)`, Mode: "expression"},
		UsageSemantics: "exclusive", Tokens: core.UsageTokens{Input: 1_000_000}, RateMultiplier: decimal.NewFromInt(1)}}
	_, err := f.svc.insert(ctx, []*core.UsageRecord{rec})
	require.NoError(t, err)
	f.loseHistory()
	time.Sleep(5 * time.Millisecond)

	n, err := f.svc.RetryPending(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, "failed 0.00000000 null 2.0000", f.moneyColumns("lost-custom-items"))
	require.Equal(t, "price expression not found", f.billingDetail("lost-custom-items").Get("error").String())
	require.True(t, before.Equal(f.balance()), "charged %s", before.Sub(f.balance()))

	// Once the history has it again: the primary usage at the custom price
	// (0.104 x 2) and the item (1).
	f.customPrice()
	n, err = f.svc.RetryPending(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, "billed 1.20800000", f.scalar(`SELECT billing_status || ' ' || total_cost::text FROM usage_logs WHERE request_id = 'lost-custom-items'`))
}

// The inputs a record is first settled by are the inputs a retry or a
// reconcile reads back from billing_detail: one function takes both from the
// record (recordInputs), the price rule's trace included.
func TestStoredInputsAreFirstSettlementInputs(t *testing.T) {
	f := newFixture(t)
	rec := f.scaled("inputs-same", "0.5")
	rec.PriceParams = map[string]string{"speed": `"fast"`}
	rec.Additional = []core.PricedUsage{{Kind: "advisor", Model: "other-model", Price: &core.PriceRule{Expression: `tier("extra",p)`, Mode: "expression"},
		UsageSemantics: "exclusive", Tokens: core.UsageTokens{Input: 1_000_000}, RateMultiplier: decimal.NewFromInt(1)}}
	_, err := f.svc.insert(t.Context(), []*core.UsageRecord{rec})
	require.NoError(t, err)

	var stored pendingDetail
	require.NoError(t, json.Unmarshal([]byte(f.scalar(`SELECT billing_detail::text FROM usage_logs WHERE request_id = 'inputs-same'`)), &stored))
	first, err := json.Marshal(fromRecord(rec).Inputs)
	require.NoError(t, err)
	again, err := json.Marshal(stored.Inputs)
	require.NoError(t, err)
	require.JSONEq(t, string(first), string(again))
	require.Equal(t, "0.5", gjson.GetBytes(again, "pricing.scale").String())
	require.Equal(t, "other-model", gjson.GetBytes(again, "additional.0.Model").String())
}

// A reconcile reprices the real usage with the rule the reservation froze,
// and the base cost follows the real usage.
func TestReconcileUsesFrozenPriceRule(t *testing.T) {
	rf := reconcileFixture(t)
	ctx := context.Background()
	rec := rf.reserved("res-scale", "task-scale", core.UsageTokens{Input: 1000, Output: 100})
	rule := *rf.price
	rule.Source, rule.Scale = core.PriceSourceGroup, decPtr("0.5")
	rec.Price = &rule
	rf.svc.process(ctx, []*core.UsageRecord{rec})
	require.Equal(t, "reserved 0.00450000 0.00900000 1.0000", rf.moneyColumns("res-scale"))

	*rule.Scale = dec("9")
	rf.plugin.parse = func(*monitorCall) (*pluginv1.ReconcileResult, error) {
		return &pluginv1.ReconcileResult{State: pluginv1.ReconcileResult_SETTLED,
			Tokens: &pluginv1.UsageTokens{InputTokens: 4000, OutputTokens: 400}}, nil
	}
	rf.due()
	require.Equal(t, 1, rf.svc.ReconcileDue(ctx))
	require.Equal(t, "billed 0.01800000 0.03600000 1.0000", rf.moneyColumns("res-scale"))
	detail := rf.billingDetail("res-scale")
	require.Equal(t, "0.018", detail.Get("list_cost").String())
	require.Equal(t, "0.5", detail.Get("inputs.pricing.scale").String())
}

// A reconcile does not price the real usage of a custom rule's row at the
// global price either: without the history the estimate stands and the entry
// asks again.
func TestReconcileNeedsHistoryOfCustomPrice(t *testing.T) {
	rf := reconcileFixture(t)
	ctx := context.Background()
	rec := rf.reserved("res-custom", "task-custom", core.UsageTokens{Input: 1000, Output: 100})
	rec.Price = rf.customPrice()
	rf.svc.process(ctx, []*core.UsageRecord{rec})
	const estimate = "reserved 0.00120000 0.00900000 1.0000"
	require.Equal(t, estimate, rf.moneyColumns("res-custom"))

	rf.loseHistory()
	rf.plugin.parse = func(*monitorCall) (*pluginv1.ReconcileResult, error) {
		return &pluginv1.ReconcileResult{State: pluginv1.ReconcileResult_SETTLED,
			Tokens: &pluginv1.UsageTokens{InputTokens: 4000, OutputTokens: 400}}, nil
	}
	rf.due()
	require.Equal(t, 1, rf.svc.ReconcileDue(ctx))
	require.Equal(t, estimate, rf.moneyColumns("res-custom"))
	require.Equal(t, "pending pricing failed: price expression not found",
		rf.scalar(`SELECT state || ' ' || last_error FROM pending_settlements WHERE ref_id = 'task-custom'`))
}

// A reservation the upstream failed and one an administrator refunded cost
// nothing: neither a charge nor a base cost stays in the statistics.
func TestRefundedRowsHaveNoBaseCost(t *testing.T) {
	rf := reconcileFixture(t)
	ctx := context.Background()
	rf.svc.process(ctx, []*core.UsageRecord{
		rf.reserved("res-failed", "task-failed", core.UsageTokens{Input: 3000, Output: 300}),
		rf.reserved("res-refunded", "task-refunded", core.UsageTokens{Input: 2000, Output: 200}),
	})
	require.Equal(t, "reserved 0.02700000 0.02700000 1.0000", rf.moneyColumns("res-failed"))
	require.Equal(t, "reserved 0.01800000 0.01800000 1.0000", rf.moneyColumns("res-refunded"))

	rf.plugin.parse = func(in *monitorCall) (*pluginv1.ReconcileResult, error) {
		if in.GetEntry().GetRefId() == "task-failed" {
			return &pluginv1.ReconcileResult{State: pluginv1.ReconcileResult_FAILED, Reason: "content_filtered"}, nil
		}
		return &pluginv1.ReconcileResult{NextCheckAfterSec: 600}, nil
	}
	_, err := rf.db.SQL.ExecContext(ctx, `UPDATE pending_settlements SET deadline_at = now() - interval '1 minute' WHERE ref_id = 'task-refunded'`)
	require.NoError(t, err)
	rf.due()
	require.Equal(t, 2, rf.svc.ReconcileDue(ctx))
	require.Equal(t, "free 0.00000000 0.00000000 1.0000", rf.moneyColumns("res-failed"))
	// Abandoned: the estimate stands, and so does its base cost.
	require.Equal(t, "billed 0.01800000 0.01800000 1.0000", rf.moneyColumns("res-refunded"))

	rf.post(rf.user, "/usage/"+rf.scalar(`SELECT id::text FROM usage_logs WHERE request_id = 'res-refunded'`)+"/refund", 200)
	require.Equal(t, "free 0.00000000 0.00000000 1.0000", rf.moneyColumns("res-refunded"))
}

// The statistics' standard cost and the export's base price are the base
// cost; a row from before the column has its charge divided by its rate.
func TestStandardCostReadsBaseCost(t *testing.T) {
	f := newFixture(t)
	row := rollupRow{age: time.Minute, user: f.user, model: "m", protocol: "p", platform: "x", success: true, anomalies: `{}`}
	rows := make([]rollupRow, 4)
	for i, c := range []struct{ cost, multiplier, base string }{
		{"1", "0.5", ""},  // before the column: 1 / 0.5
		{"0", "0", ""},    // before the column, a free group: nothing
		{"1", "0.5", "3"}, // a price rule made the charge less than base x rate
		{"0", "0", "4"},   // a free group has a base cost all the same
	} {
		rows[i] = row
		rows[i].cost, rows[i].multiplier, rows[i].base = c.cost, c.multiplier, c.base
	}
	f.insertRollupRows(time.Now(), "std", rows)

	for _, path := range []string{"/usage/summary?group_by=none", "/me/usage/summary?group_by=none"} {
		got := f.get(f.user, path, 200)["data"].([]any)[0].(map[string]any)
		require.True(t, dec("9").Equal(dec(got["standard_cost"].(string))), "%s: standard_cost %v", path, got["standard_cost"])
		require.True(t, dec("2").Equal(dec(got["total_cost"].(string))), "%s: total_cost %v", path, got["total_cost"])
	}
	month := f.get(f.user, "/usage/overview", 200)["data"].(map[string]any)["last_30d"].(map[string]any)
	require.True(t, dec("9").Equal(dec(month["standard_cost"].(string))), "overview: standard_cost %v", month["standard_cost"])

	req := httptest.NewRequest("GET", "/api/v1/usage/export?lang=en", nil)
	req.Header.Set("Authorization", "Bearer "+strconv.FormatInt(f.user, 10))
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code, w.Body.String())
	lines, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(w.Body.String(), "\xef\xbb\xbf"))).ReadAll()
	require.NoError(t, err)
	column := func(name string) int {
		for i, h := range lines[0] {
			if h == name {
				return i
			}
		}
		t.Fatalf("no export column %q", name)
		return -1
	}
	id, base := column("Request ID"), column("Base price")
	got := map[string]string{}
	for _, line := range lines[1:] {
		got[line[id]] = line[base]
	}
	require.Equal(t, map[string]string{"std-0": "2", "std-1": "0", "std-2": "3", "std-3": "4"}, got)
}

// What an account consumed upstream (account_usage_totals.raw_cost) is the
// cost at the global price: a price rule of some group changes what the user
// pays, not that.
func TestAccountRawCostIgnoresPriceRules(t *testing.T) {
	f := newFixture(t)
	plain, scaled, free, custom := f.record("raw-plain", true), f.scaled("raw-scale", "0.5"), f.scaled("raw-free", "0"), f.customPriced("raw-custom")
	charged := map[string]string{"raw-plain": "0.70800000", "raw-scale": "0.42480000", "raw-free": "0.00000000", "raw-custom": "0.20800000"}
	for i, rec := range []*core.UsageRecord{plain, scaled, free, custom} {
		account := int64(100 + i)
		rec.AccountID = &account
	}
	f.svc.process(t.Context(), []*core.UsageRecord{plain, scaled, free, custom})
	f.scalar(`SELECT account_usage_fold(1000)`) // the totals are folded from a journal (0068)
	for i, rec := range []*core.UsageRecord{plain, scaled, free, custom} {
		got := f.scalar(`SELECT cost::text || ' ' || raw_cost::text FROM account_usage_totals WHERE account_id = $1`, 100+i)
		require.Equal(t, charged[rec.RequestID]+" 0.70800000", got, rec.RequestID)
	}
	require.True(t, decimal.Sum(dec("0.708"), dec("0.4248"), dec("0.208")).Equal(dec("10").Sub(f.balance())))
}
