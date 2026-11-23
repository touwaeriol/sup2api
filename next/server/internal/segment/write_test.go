package segment

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSegmentGroupRates(t *testing.T) {
	f := newSettingsFixture(t)
	patch := func(body map[string]any) response {
		return f.do(f.admin, "PATCH", path("/segments/%d", f.vip), body)
	}
	rates := func(items ...map[string]any) response { return patch(map[string]any{"group_rates": items}) }
	// granted left out stays, null follows the group, a value sets the rate.
	rates(
		map[string]any{"group_id": f.granted, "granted": false, "rate": "0.5"},
		map[string]any{"group_id": f.other, "granted": true, "rate": "0.2500"},
		map[string]any{"group_id": f.public, "rate": "0"},
	).ok(200)
	require.Equal(t, "false 0.5000", f.pairOf(f.vip, f.granted))
	require.Equal(t, "true 0.2500", f.pairOf(f.vip, f.other))
	require.Equal(t, "false 0.0000", f.pairOf(f.vip, f.public))

	rates(map[string]any{"group_id": f.other, "rate": nil}).ok(200)
	require.Equal(t, "true null", f.pairOf(f.vip, f.other))
	// A pair that says nothing is not kept.
	rates(map[string]any{"group_id": f.other, "granted": false, "rate": nil}, map[string]any{"group_id": f.granted, "rate": nil}).ok(200)
	require.Equal(t, "none", f.pairOf(f.vip, f.other))
	require.Equal(t, "none", f.pairOf(f.vip, f.granted))
	// The items apply in order: the last word on a pair wins.
	rates(map[string]any{"group_id": f.other, "granted": true, "rate": "1"}, map[string]any{"group_id": f.other, "rate": "3"}).ok(200)
	require.Equal(t, "true 3.0000", f.pairOf(f.vip, f.other))
	rates().ok(200)
}

// PATCH /segments/:id is checked as a whole: any wrong item writes
// nothing, the name included.
func TestSegmentGroupRatesAllOrNone(t *testing.T) {
	f := newSettingsFixture(t)
	f.pair(f.vip, f.public, false, "0")
	patch := func(body map[string]any) response {
		return f.do(f.admin, "PATCH", path("/segments/%d", f.vip), body)
	}
	rates := func(items ...map[string]any) response { return patch(map[string]any{"group_rates": items}) }
	for _, bad := range []any{"-1", "0.12345", "1000000", "x", ""} {
		patch(map[string]any{"name": "gold", "group_rates": []any{map[string]any{"group_id": f.public, "granted": true, "rate": nil},
			map[string]any{"group_id": f.other, "rate": bad}}}).invalid("group_rates.1.rate")
	}
	rates(map[string]any{"group_id": f.public, "granted": true}).invalid("group_rates.0.rate")
	rates(map[string]any{"group_id": f.public, "rate": nil}, map[string]any{"group_id": 999999, "rate": nil}).invalid("group_rates.1.group_id")
	require.Equal(t, "false 0.0000", f.pairOf(f.vip, f.public))
	require.Equal(t, "vip", f.do(f.admin, "GET", path("/segments/%d", f.vip), nil).ok(200).data()["name"])

	f.do(f.admin, "PATCH", "/segments/999999", map[string]any{"group_rates": []any{}}).failed(404, "not_found")
	f.do(f.plain, "PATCH", path("/segments/%d", f.vip), map[string]any{"group_rates": []any{}}).failed(403, "permission_denied")
}

// modelRateOf returns the stored rate of a segment for a model in a group,
// "none" when there is none.
func (f settingsFixture) modelRateOf(segment, group int64, model string) string {
	f.t.Helper()
	var out string
	err := f.db.SQL.QueryRowContext(f.t.Context(), `SELECT COALESCE((SELECT rate::text FROM segment_model_rates
		WHERE segment_id = $1 AND group_id = $2 AND model = $3), 'none')`, segment, group, model).Scan(&out)
	require.NoError(f.t, err)
	return out
}

func TestSegmentModelRates(t *testing.T) {
	f := newSettingsFixture(t)
	rates := func(items ...map[string]any) response {
		return f.do(f.admin, "PATCH", path("/segments/%d", f.vip), map[string]any{"model_rates": items})
	}
	rates(map[string]any{"group_id": f.public, "model": " gpt-4o ", "rate": "0.85"},
		map[string]any{"group_id": f.public, "model": "claude", "rate": "0.9"}).ok(200)
	require.Equal(t, "0.8500", f.modelRateOf(f.vip, f.public, "gpt-4o"), "the model is trimmed")
	require.Equal(t, int32(1), f.ratesChanged.Load(), "billing hears of written model rates")

	row := f.do(f.admin, "GET", path("/segments/%d/groups", f.vip), nil).ok(200).rows()[0]
	require.Equal(t, []any{map[string]any{"model": "claude", "rate": "0.9"}, map[string]any{"model": "gpt-4o", "rate": "0.85"}},
		row["model_rates"], "by model")
	require.Equal(t, rate("1.5", "group", "1.5"), row["rate"], "the rate of the group level leaves the models out")

	rates(map[string]any{"group_id": f.public, "model": "gpt-4o", "rate": "0.5"}, map[string]any{"group_id": f.public, "model": "gpt-4o", "rate": nil}).ok(200)
	require.Equal(t, "none", f.modelRateOf(f.vip, f.public, "gpt-4o"), "null removes it; the last word wins")

	rates(map[string]any{"segment_id": f.granted, "group_id": f.public, "model": "m", "rate": "0.6"}).ok(200)
	require.Equal(t, "0.6000", f.modelRateOf(f.vip, f.public, "m"), "an item's segment_id is taken, not read")
	rates(map[string]any{"group_id": 999999, "model": "m", "rate": "1"}).invalid("model_rates.0.group_id")
	rates(map[string]any{"group_id": f.public, "model": " ", "rate": "1"}).invalid("model_rates.0.model")
	rates(map[string]any{"group_id": f.public, "model": "m", "rate": "-1"}).invalid("model_rates.0.rate")
	rates(map[string]any{"group_id": f.public, "model": "m"}).invalid("model_rates.0.rate")
	f.do(f.admin, "PATCH", path("/segments/%d", f.vip), map[string]any{"name": "gold"}).ok(200)
	require.Equal(t, int32(3), f.ratesChanged.Load(), "a write without model rates says nothing")
}

// A new segment comes with its rates, in one transaction.
func TestCreateSegmentWithRates(t *testing.T) {
	f := newSettingsFixture(t)
	created := f.do(f.admin, "POST", "/segments", map[string]any{"name": "team",
		"group_rates": []any{map[string]any{"group_id": f.granted, "granted": true, "rate": nil}},
		"model_rates": []any{map[string]any{"group_id": f.granted, "model": "m", "rate": "0.7"}}}).ok(201).data()
	id := idOf(created)
	require.Equal(t, "true null", f.pairOf(id, f.granted))
	require.Equal(t, "0.7000", f.modelRateOf(id, f.granted, "m"))
	require.Equal(t, []any{map[string]any{"id": float64(f.granted), "name": "granted"}}, created["granted_groups"])

	f.do(f.admin, "POST", "/segments", map[string]any{"name": "other",
		"model_rates": []any{map[string]any{"group_id": 999999, "model": "m", "rate": "1"}}}).invalid("model_rates.0.group_id")
	require.Equal(t, int64(0), f.id(`SELECT count(*) FROM segments WHERE name = 'other'`), "a wrong rate creates nothing")
}

// A segment's model rate takes any complete model id, with a price or not:
// it prices once the model has one. Removing one always can; a model that
// is no model id is refused, in both languages.
func TestSegmentModelRatesAnyModel(t *testing.T) {
	f := newSettingsFixture(t)
	rates := func(items ...map[string]any) response {
		return f.do(f.admin, "PATCH", path("/segments/%d", f.vip), map[string]any{"model_rates": items})
	}
	rates(map[string]any{"group_id": f.public, "model": "m", "rate": "1"},
		map[string]any{"group_id": f.public, "model": " unpriced ", "rate": "0.5"}).ok(200)
	require.Equal(t, "0.5000", f.modelRateOf(f.vip, f.public, "unpriced"), "the model is trimmed")
	rates(map[string]any{"group_id": f.public, "model": "unpriced", "rate": nil}).ok(200)
	require.Equal(t, "none", f.modelRateOf(f.vip, f.public, "unpriced"))
	f.exec(`UPDATE model_prices SET enabled = false WHERE model = 'm'`)
	rates(map[string]any{"group_id": f.public, "model": "m", "rate": "2"}).ok(200)
	require.Equal(t, "2.0000", f.modelRateOf(f.vip, f.public, "m"))

	fe := rates(map[string]any{"group_id": f.public, "model": "gpt-*", "rate": "1"}).invalid("model_rates.0.model")
	require.Contains(t, fe["message"], "a complete model id")
	require.Equal(t, "2.0000", f.modelRateOf(f.vip, f.public, "m"))
}

// POST/PATCH /segments refuse fields they do not have, such as the removed
// discount; a rate that is no string is the field error of its item.
func TestSegmentWriteStrict(t *testing.T) {
	f := newSettingsFixture(t)
	f.do(f.admin, "PATCH", path("/segments/%d", f.vip), map[string]any{"discount": "0.9"}).failed(400, "invalid_argument")
	f.do(f.admin, "POST", "/segments", map[string]any{"name": "gold", "discount": "0.9"}).failed(400, "invalid_argument")
	require.Equal(t, int64(0), f.id(`SELECT count(*) FROM segments WHERE name = 'gold'`))
	f.do(f.admin, "PATCH", path("/segments/%d", f.vip), map[string]any{"group_rates": []any{
		map[string]any{"group_id": f.public, "rate": "1"}, map[string]any{"group_id": f.other, "rate": 2}}}).invalid("group_rates.1.rate")
}

// A group_rates item with remove drops the pair and the segment's model
// rates in that group.
func TestSegmentGroupRatesRemove(t *testing.T) {
	f := newSettingsFixture(t)
	f.exec(`INSERT INTO segment_model_rates (segment_id, group_id, model, rate) VALUES ($1, $2, 'm', 0.5)`, f.vip, f.granted)
	f.do(f.admin, "PATCH", path("/segments/%d", f.vip), map[string]any{"group_rates": []any{
		map[string]any{"group_id": f.granted, "remove": true}}}).ok(200)
	require.Equal(t, "none", f.pairOf(f.vip, f.granted))
	require.Equal(t, "none", f.modelRateOf(f.vip, f.granted, "m"))
	require.Equal(t, int32(1), f.ratesChanged.Load())
}
