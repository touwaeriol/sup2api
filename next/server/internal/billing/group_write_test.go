package billing

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// groupWriteCase is a group write with one wrong item, in list, and the
// field error it gets.
type groupWriteCase struct {
	name, list string
	item       map[string]any
	field      string
	message    string
}

// groupWriteCases are the wrong items a group write can have.
func groupWriteCases() []groupWriteCase {
	return []groupWriteCase{
		{"price without a global price", "model_prices", patch("unpriced", custom("visual", requestTier(1))), "model_prices.1.model", "no global price"},
		{"price on a model nobody has", "model_prices", patch("nobody-has-it", written("p")), "model_prices.1.model", "no global price"},
		{"multiplier rule", "model_prices", patch("claude-haiku-4-5", map[string]any{"kind": "multiplier", "multiplier": "2"}), "model_prices.1.rule", "a model price is a custom price"},
		{"price in an old mode", "model_prices", patch("claude-haiku-4-5", custom("per_request", map[string]any{"price": 1})), "model_prices.1.rule", "visual or expression"},
		{"expression that does not compile", "model_prices", patch("claude-haiku-4-5", written(`tier("base", p*`)), "model_prices.1.rule", "invalid custom price"},
		{"expression of an unknown variable", "model_prices", patch("claude-haiku-4-5", written(`tier("base", nope*2)`)), "model_prices.1.rule", "invalid custom price"},
		{"negative price", "model_prices", patch("claude-haiku-4-5", custom("visual", oneTierConfig(map[string]any{"p": 1, "c": -2}))), "model_prices.1.rule", "config.tiers[0].c"},
		{"old flat prices", "model_prices", patch("claude-haiku-4-5", custom("visual", map[string]any{"p": 1, "c": 2})), "model_prices.1.rule", "unknown field"},
		{"rate without a global price", "model_rates", rated("unpriced", "1"), "model_rates.1.model", "no global price"},
		{"negative rate", "model_rates", rated("claude-haiku-4-5", "-1"), "model_rates.1.rate", "must be 0 or more"},
		{"rate too large", "model_rates", rated("claude-haiku-4-5", "1000000"), "model_rates.1.rate", "below 1000000"},
		{"rate too fine", "model_rates", rated("claude-haiku-4-5", "0.00001"), "model_rates.1.rate", "at most 4 decimals"},
		{"rate that is no number", "model_rates", rated("claude-haiku-4-5", "much"), "model_rates.1.rate", "must be 0 or more"},
		{"rate as a JSON number", "model_rates", rated("claude-haiku-4-5", 1.5), "model_rates", "must be a list of model rates"},
		{"prices that are no list", "model_prices", nil, "model_prices", "must be a list of model prices"},
	}
}

// What a group write takes of model prices and rates is checked as one: any
// item that is wrong fails the whole write, and its field error names it by
// its position.
func TestGroupWriteValidation(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	validPrice, validRate := patch("web-search", custom("visual", requestTier(1))), rated("web-search", "3")
	for _, tc := range groupWriteCases() {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"model_prices": []any{validPrice, tc.item}, "model_rates": []any{validRate}}
			if tc.list == "model_rates" {
				body = map[string]any{"model_prices": []any{validPrice}, "model_rates": []any{validRate, tc.item}}
			}
			if tc.item == nil {
				body[tc.list] = map[string]any{"web-search": validPrice}
			}
			fields := e.writeGroup(e.open, body)
			require.Len(t, fields, 1, "%v", fields)
			require.Equal(t, tc.field, fields[0].Field)
			require.Contains(t, fields[0].Message, tc.message)
		})
	}
	require.Zero(t, e.one(`SELECT count(*) FROM price_rules`)+e.one(`SELECT count(*) FROM group_model_rates`))
	checkEveryWrongItemNamed(t, e, validRate)
	checkRemovalsAndPlainRates(t, e)
}

// Every wrong item of both lists of a group write is named.
func checkEveryWrongItemNamed(t *testing.T, e *pricingEnv, validRate map[string]any) {
	fields := e.writeGroup(e.open, map[string]any{"model_prices": []any{patch("unpriced", written("p"))},
		"model_rates": []any{validRate, rated("claude-haiku-4-5", "-1"), rated("unpriced", "1")}})
	var named []string
	for _, f := range fields {
		named = append(named, f.Field)
	}
	require.Equal(t, []string{"model_prices.0.model", "model_rates.1.rate", "model_rates.2.model"}, named)
}

// Removing always can; 0 and 1 are rates like any other.
func checkRemovalsAndPlainRates(t *testing.T, e *pricingEnv) {
	require.Nil(t, e.writeGroup(e.open, map[string]any{"model_prices": []any{patch("unpriced", nil)},
		"model_rates": []any{rated("unpriced", nil), rated("claude-haiku-4-5", "0"), rated("web-search", "1")}}))
	_, rows := e.modelPrices(e.open)
	require.Equal(t, []any{"0", "1"}, []any{rows["claude-haiku-4-5"]["rate"], rows["web-search"]["rate"]})
}

// A write without model prices or rates is none of billing's; one is
// written as given, a later item on a model replacing an earlier one, also
// to a group created in the same request (prepared for id 0).
func TestGroupWriteApplies(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	w, fields, err := e.svc.PrepareGroupWrite(context.Background(), e.open, map[string]json.RawMessage{"name": json.RawMessage(`"x"`)})
	require.NoError(t, err)
	require.Nil(t, fields)
	require.Equal(t, core.GroupWrite{}, w)

	ctx := core.WithUserID(context.Background(), e.admin)
	raw := map[string]json.RawMessage{
		"model_prices": json.RawMessage(`[{"model": "web-search", "rule": {"kind": "custom", "mode": "expression", "expression": "flat(2)"}},
			{"model": "claude-haiku-4-5", "rule": {"kind": "custom", "mode": "expression", "expression": "flat(1)"}},
			{"model": "web-search", "rule": null}]`),
		"model_rates": json.RawMessage(`[{"model": "claude-haiku-4-5", "rate": "1"}, {"model": "claude-haiku-4-5", "rate": "2.5"}]`)}
	w, fields, err = e.svc.PrepareGroupWrite(ctx, 0, raw)
	require.NoError(t, err)
	require.Nil(t, fields)
	created := e.one(`INSERT INTO groups (name) VALUES ('created') RETURNING id`)
	require.NoError(t, e.db.Tx(ctx, func(tx *gorm.DB) error { return w.Apply(ctx, tx, created) }))
	w.AfterCommit(ctx, created)

	_, rows := e.modelPrices(created)
	require.Equal(t, []any{nil, "2.5"}, []any{rows["web-search"]["price"], rows["claude-haiku-4-5"]["rate"]})
	require.Equal(t, "flat(1)", rows["claude-haiku-4-5"]["price"].(map[string]any)["expression"])
}

// Model rates price requests from the snapshot: the group's and a segment's
// in it, as written here or, for a segment's, announced by RatesChanged.
func TestModelRatesFollowWrites(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	ctx := context.Background()
	q := core.PriceQuery{Model: "claude-haiku-4-5", GroupID: e.open}
	levels := func(segment int64) []string {
		t.Helper()
		got, err := e.svc.ModelRates(ctx, q, segment, core.RateLevels{Segment: dec("1.2"), Group: *dec("1.5")})
		require.NoError(t, err)
		rate, source := got.Rate()
		return []string{text(got.SegmentModel), text(got.GroupModel), rate.String(), source}
	}
	require.Equal(t, []string{"-", "-", "1.2", core.RateSourceSegment}, levels(e.vip))

	require.Nil(t, e.writeGroup(e.open, map[string]any{"model_rates": []any{rated(q.Model, "0.9")}}))
	require.Equal(t, []string{"-", "0.9", "1.2", core.RateSourceSegment}, levels(e.vip))

	// A segment's rate for the model, written elsewhere, applies once announced.
	e.exec(`INSERT INTO segment_model_rates (segment_id, group_id, model, rate) VALUES ($1, $2, $3, 0.7)`, e.vip, e.open, q.Model)
	require.Equal(t, []string{"-", "0.9", "1.2", core.RateSourceSegment}, levels(e.vip))
	e.svc.RatesChanged(ctx)
	require.Contains(t, e.bus.sent, core.ChannelConfigChanged+` {"key":"model_rates"}`)
	require.Equal(t, []string{"0.7", "0.9", "0.7", core.RateSourceSegmentModel}, levels(e.vip))
	// Another segment, or another group, has none of them.
	require.Equal(t, []string{"-", "0.9", "1.2", core.RateSourceSegment}, levels(e.defaultSegment))
	q.GroupID = e.private
	require.Equal(t, []string{"-", "-", "1.2", core.RateSourceSegment}, levels(e.vip))
}

// text is d as a string, "-" for nil.
func text(d *decimal.Decimal) string {
	if d == nil {
		return "-"
	}
	return d.String()
}
