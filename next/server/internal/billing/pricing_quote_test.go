package billing

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// rateLevels are the rate levels of a quote: segment x model, segment,
// group x model, group.
func rateLevels(segmentModel, segment, groupModel any, group string) map[string]any {
	return map[string]any{"segment_model": segmentModel, "segment": segment, "group_model": groupModel, "group": group}
}

// quoteFor quotes 1M input and 1M output tokens of model in group for who
// ("user_id" or "segment_id") id.
func (e *pricingEnv) quoteFor(who string, id, group int64, model string) map[string]any {
	e.t.Helper()
	usage := map[string]any{"p": 1_000_000, "c": 1_000_000}
	return data(e.mustCall(e.admin, 200, "POST", "/pricing/quote", map[string]any{who: id, "group_id": group, "model": model, "usage": usage}))
}

func TestPricingQuote(t *testing.T) {
	const reader, outsider = 1001, 1002
	e := newPricingEnv(t, limited{perms: map[int64][]string{reader: {"price:read", "group:read", "segment:read"}, outsider: {"group:read", "user:read"}}})
	require.Nil(t, e.writeGroup(e.private, map[string]any{
		"model_prices": []any{patch("web-search", custom("visual", requestTier(0.008)))},
		"model_rates":  []any{rated("claude-haiku-4-5", "2.4"), rated("claude-sonnet-4-5", "1")}}))
	e.exec(`INSERT INTO segment_model_rates (segment_id, group_id, model, rate) VALUES ($1, $2, 'claude-sonnet-4-5', 0.25)`, e.vip, e.private)
	e.svc.RatesChanged(t.Context())

	checkQuoteOfVIP(t, e)
	checkQuoteOfOthers(t, e)
	checkQuoteUnbilled(t, e)
	checkQuoteSubjects(t, e)

	// price:read, group:read and segment:read quote for a segment; without
	// user:read a personal segment has no email.
	body := map[string]any{"segment_id": e.personal, "group_id": e.open, "model": "web-search"}
	e.mustCall(outsider, 403, "POST", "/pricing/quote", body)
	e.mustCall(outsider, 403, "GET", "/me/prices", nil)
	require.Equal(t, "#"+itoa(e.solo), data(e.mustCall(reader, 200, "POST", "/pricing/quote", body))["segment"].(map[string]any)["name"])
}

// checkQuoteOfVIP quotes for vip and its member in the private group.
func checkQuoteOfVIP(t *testing.T, e *pricingEnv) {
	// A member of vip in the private group: granted, at the global price, at
	// vip's own rate there, which comes before the group's for the model.
	out := e.quoteFor("user_id", e.member, e.private, "claude-haiku-4-5")
	require.Equal(t, map[string]any{"id": float64(e.vip), "kind": "named", "name": "vip"}, out["segment"])
	require.Equal(t, []any{true, "grant", "global"}, []any{out["accessible"], out["access_source"], out["price_source"]})
	require.Equal(t, map[string]any{"group": nil}, out["price_levels"])
	require.Equal(t, map[string]any{"value": "0.5", "source": "segment", "group_default": "2"}, out["rate"])
	require.Equal(t, rateLevels(nil, "0.5", "2.4", "2"), out["rate_levels"])
	require.Equal(t, []any{"6", "3"}, []any{out["list_cost"], out["cost"]})
	require.Equal(t, out["base"], out["list_price"])
	require.NotContains(t, out, "rate_override")

	// vip's own rate for the model comes first of all: 28.5 x 0.25.
	out = e.quoteFor("user_id", e.member, e.private, "claude-sonnet-4-5")
	require.Equal(t, rateLevels("0.25", "0.5", "1", "2"), out["rate_levels"])
	require.Equal(t, []any{"segment_model", "28.5", "7.125"}, []any{out["rate"].(map[string]any)["source"], out["list_cost"], out["cost"]})

	checkQuoteOfGroupPrice(t, e)
}

// checkQuoteOfGroupPrice quotes a model the private group prices.
func checkQuoteOfGroupPrice(t *testing.T, e *pricingEnv) {
	// The same for the segment itself; the group's own price applies per request.
	out := e.quoteFor("segment_id", e.vip, e.private, "web-search")
	require.Equal(t, "group", out["price_source"])
	require.Equal(t, map[string]any{"group": shown(custom("visual", requestTier(0.008)), `tier("base", flat(0.008))`)}, out["price_levels"])
	require.Equal(t, []any{"0.008", "0.004"}, []any{out["list_cost"], out["cost"]})
	require.Equal(t, requestTier(0.01), out["base"].(map[string]any)["config"])
	require.Equal(t, requestTier(0.008), out["list_price"].(map[string]any)["config"])
}

// checkQuoteOfOthers quotes for the default segment and a personal one.
func checkQuoteOfOthers(t *testing.T, e *pricingEnv) {
	// The default segment was not granted the group: quoted all the same, at
	// the group's rate for the model.
	out := e.quoteFor("user_id", e.admin, e.private, "claude-haiku-4-5")
	require.Equal(t, float64(e.defaultSegment), out["segment"].(map[string]any)["id"])
	require.Equal(t, []any{false, "none"}, []any{out["accessible"], out["access_source"]})
	require.Equal(t, map[string]any{"value": "2.4", "source": "group_model", "group_default": "2"}, out["rate"])
	require.Equal(t, rateLevels(nil, nil, "2.4", "2"), out["rate_levels"])
	require.Equal(t, []any{"6", "14.4"}, []any{out["list_cost"], out["cost"]})

	// A public group at vip's own rate; a personal segment at the group's
	// rate for the model.
	out = e.quoteFor("user_id", e.member, e.open, "claude-haiku-4-5")
	require.Equal(t, []any{true, "public", "global", "6", "7.2"},
		[]any{out["accessible"], out["access_source"], out["price_source"], out["list_cost"], out["cost"]})
	out = e.quoteFor("segment_id", e.personal, e.private, "claude-sonnet-4-5")
	require.Equal(t, []any{"group_model", "28.5", "28.5"}, []any{out["rate"].(map[string]any)["source"], out["list_cost"], out["cost"]})
	require.Equal(t, map[string]any{"id": float64(e.personal), "kind": "personal", "name": "solo@example.com", "user_id": float64(e.solo)}, out["segment"])
}

// checkQuoteUnbilled quotes a model without a global price and a disabled
// group.
func checkQuoteUnbilled(t *testing.T, e *pricingEnv) {
	// A model without a global price cannot be billed.
	out := e.quoteFor("user_id", e.member, e.private, "unpriced")
	require.Equal(t, []any{nil, nil, "global", "0", "0"}, []any{out["base"], out["list_price"], out["price_source"], out["list_cost"], out["cost"]})

	// A disabled group serves nobody, as the gateway refuses it
	// (group_disabled); the price is quoted all the same.
	e.exec(`UPDATE groups SET status = 'disabled' WHERE id = $1`, e.private)
	out = e.quoteFor("user_id", e.member, e.private, "claude-haiku-4-5")
	require.Equal(t, []any{false, "disabled", "6", "3"}, []any{out["accessible"], out["access_source"], out["list_cost"], out["cost"]})
	e.exec(`UPDATE groups SET status = 'active' WHERE id = $1`, e.private)
}

// checkQuoteSubjects checks whom a quote is for, and what.
func checkQuoteSubjects(t *testing.T, e *pricingEnv) {
	for name, body := range map[string]map[string]any{
		"neither": {"group_id": e.open, "model": "web-search"},
		"both":    {"user_id": e.member, "segment_id": e.vip, "group_id": e.open, "model": "web-search"},
	} {
		code, resp := e.call(e.admin, "POST", "/pricing/quote", body)
		require.Equal(t, 400, code, name)
		field, _ := fieldError(t, resp)
		require.Equal(t, "user_id", field, name)
	}
	code, resp := e.call(e.admin, "POST", "/pricing/quote", map[string]any{"user_id": e.member, "group_id": e.open, "model": "gpt-5"})
	require.Equal(t, 400, code)
	field, message := fieldError(t, resp)
	require.Equal(t, []string{"model", "the group does not provide this model"}, []string{field, message})
	resp = e.mustCall(e.admin, 404, "POST", "/pricing/quote", map[string]any{"user_id": 999999, "group_id": e.open, "model": "web-search"})
	require.Equal(t, "user not found", resp["error"].(map[string]any)["message"])
	resp = e.mustCall(e.admin, 404, "POST", "/pricing/quote", map[string]any{"segment_id": 999999, "group_id": e.open, "model": "web-search"})
	require.Equal(t, "user group not found", resp["error"].(map[string]any)["message"])
	e.mustCall(e.admin, 404, "POST", "/pricing/quote", map[string]any{"segment_id": e.vip, "group_id": 999999, "model": "web-search"})
}

// A quote needs what the console's entry to it needs: price:read and
// group:read, and user:read for a user or segment:read for a segment. Without
// them it would tell which users exist and where they are priced.
func TestPricingQuotePermissions(t *testing.T) {
	const pricer, grouper, users, segments = 1001, 1002, 1003, 1004
	e := newPricingEnv(t, limited{perms: map[int64][]string{
		pricer:   {"price:read"},
		grouper:  {"price:read", "group:read"},
		users:    {"price:read", "group:read", "user:read"},
		segments: {"price:read", "group:read", "segment:read"},
	}})
	ofUser := map[string]any{"user_id": e.member, "group_id": e.open, "model": "web-search"}
	ofSegment := map[string]any{"segment_id": e.vip, "group_id": e.open, "model": "web-search"}
	denied := func(uid int64, body map[string]any, permission string) {
		t.Helper()
		code, resp := e.call(uid, "POST", "/pricing/quote", body)
		require.Equal(t, 403, code, "%v", resp)
		require.Equal(t, permission, resp["error"].(map[string]any)["details"].(map[string]any)["permission"])
	}
	denied(pricer, ofUser, "group:read")
	denied(pricer, ofSegment, "group:read")
	denied(grouper, ofUser, "user:read")
	denied(grouper, ofSegment, "segment:read")
	denied(segments, ofUser, "user:read")
	denied(users, ofSegment, "segment:read")
	// A user that does not exist is not told apart from one that does.
	denied(grouper, map[string]any{"user_id": 999999, "group_id": e.open, "model": "web-search"}, "user:read")

	require.Equal(t, "vip", data(e.mustCall(users, 200, "POST", "/pricing/quote", ofUser))["segment"].(map[string]any)["name"])
	require.Equal(t, "vip", data(e.mustCall(segments, 200, "POST", "/pricing/quote", ofSegment))["segment"].(map[string]any)["name"])
}

// The usage of a quote is bounded as a request's is: 0 to 1e12 tokens of
// each kind. Beyond that it would be a cost no request can have.
func TestPricingQuoteUsageBounds(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	quote := func(usage map[string]any) (int, map[string]any) {
		return e.call(e.admin, "POST", "/pricing/quote", map[string]any{"user_id": e.member, "group_id": e.open,
			"model": "claude-haiku-4-5", "usage": usage})
	}
	code, resp := quote(map[string]any{"p": 1000, "c": -1, "cr": 1e30, "cc": 1e12 + 1, "cc1h": 0})
	require.Equal(t, 400, code, "%v", resp)
	var fields []string
	for _, f := range resp["error"].(map[string]any)["details"].(map[string]any)["fields"].([]any) {
		fields = append(fields, f.(map[string]any)["field"].(string))
	}
	require.Equal(t, []string{"usage.c", "usage.cr", "usage.cc"}, fields)

	// The bounds themselves are a usage: 1e12 input tokens at 1 per million.
	code, resp = quote(map[string]any{"p": 1e12, "c": 0})
	require.Equal(t, 200, code, "%v", resp)
	require.Equal(t, "1000000", data(resp)["list_cost"])
}

// The prices and rates a quote shows and the costs it gives come from one
// price snapshot: a change this node has not heard of yet (its broadcast was
// lost) is in neither, rather than shown beside costs without it.
func TestPricingQuoteOneSnapshot(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	body := map[string]any{"user_id": e.admin, "group_id": e.open, "model": "claude-haiku-4-5",
		"usage": map[string]any{"p": 1_000_000, "c": 1_000_000}}
	before := data(e.mustCall(e.admin, 200, "POST", "/pricing/quote", body))
	require.Equal(t, []any{"global", "6", "9"}, []any{before["price_source"], before["list_cost"], before["cost"]})

	// Written behind the service's back, as another node would.
	e.exec(`INSERT INTO price_rules (group_id, model, mode, config, expression, expr_hash)
		VALUES ($1, 'claude-haiku-4-5', 'expression', '{}', 'tier("base", p*2 + c*10)', 'h')`, e.open)
	e.exec(`INSERT INTO group_model_rates (group_id, model, rate) VALUES ($1, 'claude-haiku-4-5', 2)`, e.open)
	stale := data(e.mustCall(e.admin, 200, "POST", "/pricing/quote", body))
	require.Equal(t, before, stale)

	// Once the change is heard of, both show it.
	e.svc.invalidate()
	after := data(e.mustCall(e.admin, 200, "POST", "/pricing/quote", body))
	require.Equal(t, []any{"group", "12", "24"}, []any{after["price_source"], after["list_cost"], after["cost"]})
	require.Equal(t, `tier("base", p*2 + c*10)`, after["list_price"].(map[string]any)["expression"])
	require.Equal(t, rateLevels(nil, nil, "2", "1.5"), after["rate_levels"])
}

// GET /me/prices?group_id=: what the group provides and can bill, at the
// caller's list price there and the rate they pay there, model rates aside.
func TestMyPricesInGroup(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	require.Nil(t, e.writeGroup(e.private, map[string]any{
		"model_prices": []any{patch("web-search", custom("visual", requestTier(0.008)))},
		"model_rates":  []any{rated("claude-haiku-4-5", "2.4")}}))
	checkMyPricesInPrivate(t, e)
	checkMyPricesElsewhere(t, e)
}

// pricesByModel are the rows of a /me/prices answer by model.
func pricesByModel(d map[string]any) map[string]map[string]any {
	rows := map[string]map[string]any{}
	for _, row := range d["prices"].([]any) {
		rows[row.(map[string]any)["model"].(string)] = row.(map[string]any)
	}
	return rows
}

// checkMyPricesInPrivate reads the prices of a member of vip in the private
// group.
func checkMyPricesInPrivate(t *testing.T, e *pricingEnv) {
	d := data(e.mustCall(e.member, 200, "GET", "/me/prices?group_id="+itoa(e.private), nil))
	require.Equal(t, map[string]any{"id": float64(e.private), "name": "private"}, d["group"])
	require.Equal(t, []any{"0.5", "segment"}, []any{d["rate_multiplier"], d["rate_source"]})
	rows := pricesByModel(d)
	require.Len(t, rows, 3, "a model without a global price is left out")
	require.Equal(t, map[string]any{"model": "claude-haiku-4-5", "mode": "visual", "source": "global",
		"config":     oneTierConfig(map[string]any{"p": 1.0, "c": 5.0, "cr": 0.1, "cc": 1.25, "cc1h": 2.0}),
		"expression": `tier("base", p*1 + c*5 + cr*0.1 + cc*1.25 + cc1h*2)`}, rows["claude-haiku-4-5"])
	require.Equal(t, map[string]any{"model": "web-search", "mode": "visual", "source": "group",
		"config": requestTier(0.008), "expression": `tier("base", flat(0.008))`}, rows["web-search"])
}

// checkMyPricesElsewhere reads the prices in the public group, without a
// group, and once the private group is disabled.
func checkMyPricesElsewhere(t *testing.T, e *pricingEnv) {
	// The public group at vip's own rate there; the default segment at the
	// group's.
	d := data(e.mustCall(e.member, 200, "GET", "/me/prices?group_id="+itoa(e.open), nil))
	require.Equal(t, []any{"1.2", "segment", "global"}, []any{d["rate_multiplier"], d["rate_source"], pricesByModel(d)["web-search"]["source"]})
	d = data(e.mustCall(e.admin, 200, "GET", "/me/prices?group_id="+itoa(e.open), nil))
	require.Equal(t, []any{"1.5", "group"}, []any{d["rate_multiplier"], d["rate_source"]})

	// Without a group the prices are the global ones and no price of a group
	// shows.
	d = data(e.mustCall(e.member, 200, "GET", "/me/prices", nil))
	require.Nil(t, d["group"])
	require.NotContains(t, d, "rate_source")
	rows := pricesByModel(d)
	require.Len(t, rows, 4)
	both := []any{map[string]any{"id": float64(e.open), "name": "open"}, map[string]any{"id": float64(e.private), "name": "private"}}
	require.Equal(t, map[string]any{"model": "web-search", "mode": "visual", "config": requestTier(0.01),
		"expression": `tier("base", flat(0.01))`, "groups": both}, rows["web-search"])
	require.Equal(t, []any{}, rows["gpt-5"]["groups"])
	// Whoever was not granted the private group does not see it.
	require.Equal(t, both[:1], pricesByModel(data(e.mustCall(e.admin, 200, "GET", "/me/prices", nil)))["web-search"]["groups"])
	e.mustCall(e.admin, 404, "GET", "/me/prices?group_id="+itoa(e.private), nil)

	// A disabled group is nobody's.
	e.exec(`UPDATE groups SET status = 'disabled' WHERE id = $1`, e.private)
	e.mustCall(e.member, 404, "GET", "/me/prices?group_id="+itoa(e.private), nil)
	require.Equal(t, both[:1], pricesByModel(data(e.mustCall(e.member, 200, "GET", "/me/prices", nil)))["web-search"]["groups"])
}

// keyPrices calls GET /key/prices as the holder of a key of keys, and
// returns the rate and what a downstream sup2api makes of the prices, rate
// left aside.
func (e *pricingEnv) keyPrices(keys fakeKeys) (string, map[string]*syncPrice) {
	e.t.Helper()
	e.svc.SetSyncDeps(SyncDeps{Keys: keys})
	req := httptest.NewRequest("GET", "/api/v1/key/prices", nil)
	req.Header.Set("Authorization", "Bearer sk-good")
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	require.Equal(e.t, 200, w.Code, w.Body.String())
	var body struct {
		Data struct {
			RateMultiplier string      `json:"rate_multiplier"`
			Prices         []syncPrice `json:"prices"`
		} `json:"data"`
	}
	require.NoError(e.t, json.Unmarshal(w.Body.Bytes(), &body))
	for _, p := range body.Data.Prices {
		require.NotEmpty(e.t, p.Expression, p.Model)
	}
	f, err := parseSup2API(w.Body.Bytes(), false)
	require.NoError(e.t, err)
	require.Zero(e.t, f.skipped)
	for _, p := range f.prices {
		require.NoError(e.t, p.compile(), p.Model)
	}
	return body.Data.RateMultiplier, f.prices
}

// GET /key/prices: the list prices of the key's group, in a form a
// downstream sup2api compiles to the same prices, and the rate of the key's
// owner there; a model paid at another rate has the ratio folded into its
// price.
func TestKeyPricesFollowRates(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	key := fakeKeys{Group: e.private}
	require.Nil(t, e.writeGroup(e.private, map[string]any{
		"model_prices": []any{patch("claude-haiku-4-5", custom("visual", oneTierConfig(map[string]any{"p": 0.8, "c": 4})))},
		"model_rates":  []any{rated("claude-sonnet-4-5", "1"), rated("web-search", "6")}}))
	require.Nil(t, e.writeGroup(e.open, map[string]any{"model_rates": []any{rated("claude-haiku-4-5", "18")}}))
	checkKeyPricesOfGroup(t, e, key)
	checkKeyPricesOfSegment(t, e, key)
}

// checkKeyPricesOfGroup reads the key prices at the rates of the key's group.
func checkKeyPricesOfGroup(t *testing.T, e *pricingEnv, key fakeKeys) {
	// The rate is the principal's; only models the key's group allows
	// (claude-*), at the prices of that group.
	rate, got := e.keyPrices(key)
	require.Equal(t, "2", rate)
	require.Len(t, got, 2)
	require.Equal(t, `v1:tier("base", p*0.8 + c*4)`, got["claude-haiku-4-5"].Expression)
	require.Equal(t, "visual", got["claude-haiku-4-5"].Mode)
	// A rate on a hand-written expression becomes a rule of it: 1 / 2.
	require.True(t, strings.HasSuffix(got["claude-sonnet-4-5"].Expression, `) * 0.5`), got["claude-sonnet-4-5"].Expression)

	// Free over a hand-written expression: it times 0. A rate on a visual
	// price scales its config; one equal to the key's leaves the price.
	require.Nil(t, e.writeGroup(e.private, map[string]any{"model_prices": []any{patch("claude-haiku-4-5", nil)},
		"model_rates": []any{rated("claude-sonnet-4-5", "0"), rated("claude-haiku-4-5", "2.4")}}))
	_, got = e.keyPrices(key)
	require.Equal(t, "0", costOf(t, got["claude-sonnet-4-5"].Expression))
	require.Equal(t, `v1:tier("base", p*1.2 + c*6 + cr*0.12 + cc*1.5 + cc1h*2.4)`, got["claude-haiku-4-5"].Expression)
	require.Nil(t, e.writeGroup(e.private, map[string]any{"model_rates": []any{rated("claude-haiku-4-5", "2")}}))
	_, got = e.keyPrices(key)
	require.Equal(t, `v1:tier("base", p*1 + c*5 + cr*0.1 + cc*1.25 + cc1h*2)`, got["claude-haiku-4-5"].Expression)
}

// checkKeyPricesOfSegment reads the key prices at the rates of the owner's
// segment, and of an expression that ends in a line comment.
func checkKeyPricesOfSegment(t *testing.T, e *pricingEnv, key fakeKeys) {
	// The owner's segment: its own rate in the group is the key's and comes
	// before the group's for a model; its rate for a model is folded in
	// against it.
	e.exec(`INSERT INTO segment_model_rates (segment_id, group_id, model, rate) VALUES ($1, $2, 'claude-haiku-4-5', 0.25)`, e.vip, e.private)
	e.svc.RatesChanged(t.Context())
	key.Segment, key.Override = e.vip, dec("0.5")
	rate, got := e.keyPrices(key)
	require.Equal(t, "0.5", rate)
	require.Equal(t, `v1:tier("base", p*0.5 + c*2.5 + cr*0.05 + cc*0.625 + cc1h*1)`, got["claude-haiku-4-5"].Expression)
	require.Equal(t, "18", costOf(t, got["claude-sonnet-4-5"].Expression))

	// An expression that ends in a line comment takes the ratio too.
	sonnetID := e.one(`SELECT id FROM model_prices WHERE model = 'claude-sonnet-4-5'`)
	e.mustCall(e.admin, 200, "PATCH", "/prices/"+itoa(sonnetID), map[string]any{"expression": `tier("base", p*3 + c*15) // don't`})
	require.Nil(t, e.writeGroup(e.private, map[string]any{"model_rates": []any{rated("claude-sonnet-4-5", "1")}}))
	_, got = e.keyPrices(fakeKeys{Group: e.private})
	require.Len(t, got, 2)
	require.Equal(t, "9", costOf(t, got["claude-sonnet-4-5"].Expression))
}

// The pricing tables (GET /groups/:id/model-prices, /me/prices?group_id=,
// /key/prices) read the price snapshot requests are billed by: a price or a
// rate this node has not heard of yet shows nowhere, as it prices nothing
// yet.
func TestPricingTablesReadTheSnapshot(t *testing.T) {
	e := newPricingEnv(t, allowAll{})
	sources := func() []any {
		t.Helper()
		_, rows := e.modelPrices(e.open)
		var mine string
		for _, row := range data(e.mustCall(e.member, 200, "GET", "/me/prices?group_id="+itoa(e.open), nil))["prices"].([]any) {
			if row := row.(map[string]any); row["model"] == "claude-haiku-4-5" {
				mine = row["source"].(string)
			}
		}
		_, key := e.keyPrices(fakeKeys{Group: e.open})
		return []any{rows["claude-haiku-4-5"]["price"] != nil, rows["claude-haiku-4-5"]["rate"], mine, key["claude-haiku-4-5"].Expression}
	}
	global := `v1:tier("base", p*1 + c*5 + cr*0.1 + cc*1.25 + cc1h*2)`
	require.Equal(t, []any{false, nil, "global", global}, sources())

	// Written behind the service's back, as another node would.
	e.exec(`INSERT INTO price_rules (group_id, model, mode, config, expression, expr_hash)
		VALUES ($1, 'claude-haiku-4-5', 'visual', '{"tiers": [{"name": "base", "p": 2, "c": 10}]}', 'tier("base", p*2 + c*10)', 'h')`, e.open)
	e.exec(`INSERT INTO group_model_rates (group_id, model, rate) VALUES ($1, 'claude-haiku-4-5', 3)`, e.open)
	require.Equal(t, []any{false, nil, "global", global}, sources())
	e.svc.invalidate()
	require.Equal(t, []any{true, "3", "group", `v1:tier("base", p*3 + c*15)`}, sources())
}
