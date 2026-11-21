package billing

import (
	"context"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// seedPrices inserts two manual prices (the old anthropic defaults).
func seedPrices(e *env, admin int64) {
	e.mustCall(admin, 201, "POST", "/prices", map[string]any{"model": "claude-sonnet-4-5", "mode": "expression",
		"expression": `len <= 200000 ? tier("standard", p*3 + c*15 + cr*0.3 + cc*3.75 + cc1h*6) : tier("long_context", p*6 + c*22.5 + cr*0.6 + cc*7.5 + cc1h*12)`})
	e.mustCall(admin, 201, "POST", "/prices", map[string]any{"model": "claude-haiku-4-5", "mode": "per_token",
		"config": oneTierConfig(map[string]any{"p": 1, "c": 5, "cr": 0.1, "cc": 1.25, "cc1h": 2})})
}

func TestPriceAPI(t *testing.T) {
	e := newEnv(t)
	admin := e.user("admin@example.com")
	seedPrices(e, admin)

	// validate: generated expression and analysis; config errors are reported, not failed.
	out := data(e.mustCall(admin, 200, "POST", "/prices/validate", map[string]any{
		"mode": "per_token", "config": oneTierConfig(map[string]any{"p": 1, "c": 5, "cr": 0.1, "cc": 1.25, "cc1h": 2}),
	}))
	if out["ok"] != true || out["expression"] != `tier("base", p*1 + c*5 + cr*0.1 + cc*1.25 + cc1h*2)` {
		t.Fatalf("validate: %v", out)
	}
	out = data(e.mustCall(admin, 200, "POST", "/prices/validate", map[string]any{"mode": "expression", "expression": "p +"}))
	if out["ok"] != false || len(out["errors"].([]any)) == 0 {
		t.Fatalf("validate error: %v", out)
	}
	out = data(e.mustCall(admin, 200, "POST", "/prices/validate", map[string]any{"mode": "per_request", "config": map[string]any{}}))
	if out["ok"] != false {
		t.Fatalf("validate config error: %v", out)
	}

	// create: per_token tiers and rules (A.6); an expression price has no config.
	visual := map[string]any{
		"tiers": []any{
			map[string]any{"name": "standard", "max_len": 200000, "p": 3, "c": 15, "cr": 0.3, "cc": 3.75, "cc1h": 6},
			map[string]any{"name": "long_context", "max_len": nil, "p": 6, "c": 22.5, "cr": 0.6, "cc": 7.5, "cc1h": 12},
		},
		"rules": []any{
			map[string]any{"name": "fast", "multiplier": 2, "conds": []any{map[string]any{"source": "header", "key": "anthropic-beta", "op": "contains", "value": "fast-mode"}}},
			map[string]any{"multiplier": 0.8, "conds": []any{map[string]any{"source": "time", "tz": "Asia/Shanghai", "from_hour": 0, "to_hour": 8}}},
		},
	}
	_, resp := e.call(admin, "POST", "/prices", map[string]any{"model": "claude-sonnet-x", "mode": "expression", "config": visual})
	if errCode(resp) != "invalid_argument" {
		t.Fatalf("expression with a config: %v", resp)
	}
	created := data(e.mustCall(admin, 201, "POST", "/prices", map[string]any{
		"model": "claude-sonnet-x", "mode": "per_token", "config": visual,
	}))
	id := int64(created["id"].(float64))
	if _, has := created["platform"]; created["source"] != "manual" || created["expr_hash"] == "" || has ||
		!contains(str(created["expression"]), `tier("standard"`) {
		t.Fatalf("created %v", created)
	}
	var hist int
	_ = e.db.SQL.QueryRowContext(context.Background(), `SELECT count(*) FROM model_price_history WHERE expr_hash = $1`, created["expr_hash"]).Scan(&hist)
	if hist != 1 {
		t.Fatal("history not recorded")
	}
	e.mustCall(admin, 200, "GET", "/prices/history/"+str(created["expr_hash"]), nil)
	e.mustCall(admin, 404, "GET", "/prices/history/nope", nil)

	code, resp := e.call(admin, "POST", "/prices", map[string]any{
		"model": "big-model", "mode": "per_token", "config": oneTierConfig(map[string]any{"p": 15, "c": 75}),
	})
	if code != 400 || data(map[string]any{"data": resp["error"]})["details"].(map[string]any)["confirmation_required"] != true {
		t.Fatalf("big cost: %d %v", code, resp)
	}
	e.mustCall(admin, 201, "POST", "/prices", map[string]any{
		"model": "big-model", "mode": "per_token", "config": oneTierConfig(map[string]any{"p": 15, "c": 75}), "confirm": true,
	})
	_, resp = e.call(admin, "POST", "/prices", map[string]any{"model": "big-model", "mode": "per_token", "config": oneTierConfig(map[string]any{"p": 1})})
	if errCode(resp) != "conflict" {
		t.Fatalf("duplicate: %v", resp)
	}
	_, resp = e.call(admin, "POST", "/prices", map[string]any{"model": "x", "mode": "expression", "expression": "p * -1"})
	if errCode(resp) != "invalid_argument" {
		t.Fatalf("invalid expression: %v", resp)
	}
	// Prices are keyed by complete model ids: wildcards are rejected.
	for _, m := range []string{"claude-*", "gpt-4?", "gpt 4o"} {
		_, resp = e.call(admin, "POST", "/prices", map[string]any{"model": m, "mode": "per_token", "config": oneTierConfig(map[string]any{"p": 1})})
		if errCode(resp) != "invalid_argument" {
			t.Fatalf("model %q accepted: %v", m, resp)
		}
	}

	// preview by id: A.6 numbers, group multiplier applied.
	var gid int64
	_ = e.db.SQL.QueryRowContext(context.Background(), `INSERT INTO groups (name, rate_multiplier) VALUES ('vip', 1.5) RETURNING id`).Scan(&gid)
	prev := data(e.mustCall(admin, 200, "POST", "/prices/preview", map[string]any{
		"price_id": id,
		"usage":    map[string]any{"p": 100000, "c": 2000, "cr": 80000},
		"headers":  map[string]any{"Anthropic-Beta": "fast-mode"},
		"at":       "2026-09-24T02:00:00Z",
	}))
	if prev["cost"] != "0.708" || prev["tier"] != "standard" {
		t.Fatalf("preview: %v", prev)
	}
	rules := prev["rules"].([]any)
	if rules[0].(map[string]any)["matched"] != true || rules[1].(map[string]any)["matched"] != false {
		t.Fatalf("rules: %v", rules)
	}
	bd := prev["breakdown"].(map[string]any)
	if bd["vars"].(map[string]any)["len"].(float64) != 180000 || bd["subtotal"] != "0.354" {
		t.Fatalf("breakdown: %v", bd)
	}
	prev = data(e.mustCall(admin, 200, "POST", "/prices/preview", map[string]any{
		"mode": "per_token", "config": oneTierConfig(map[string]any{"p": 3, "c": 15}),
		"usage": map[string]any{"p": 1204, "c": 812}, "group_id": gid,
	}))
	if prev["base_cost"] != "0.015792" || prev["cost"] != "0.023688" || prev["rate_multiplier"] != "1.5" {
		t.Fatalf("preview with group: %v", prev)
	}
	// Image tokens (CONTRACTS §118) are entered apart from p and c: priced,
	// at img / img_o; unpriced, at the text rates (here 300*5 + 600*8 +
	// 4000*32 + 200*10, then 900*5 + 4200*10). len counts the image input.
	for src, cost := range map[string]string{`p*5 + img*8 + img_o*32 + c*10`: "0.1363", `p*5 + c*10`: "0.0465"} {
		prev = data(e.mustCall(admin, 200, "POST", "/prices/preview", map[string]any{
			"expression": src, "usage": map[string]any{"p": 300, "c": 200, "img": 600, "img_o": 4000},
		}))
		if prev["cost"] != cost || prev["breakdown"].(map[string]any)["vars"].(map[string]any)["len"].(float64) != 900 {
			t.Fatalf("image preview %s: %v", src, prev)
		}
	}
	e.mustCall(admin, 404, "POST", "/prices/preview", map[string]any{"price_id": 999999})

	// list with filters and analysis.
	list := e.mustCall(admin, 200, "GET", "/prices?source=manual&q=claude", nil)
	items := list["data"].([]any)
	if len(items) != 3 || list["page"].(map[string]any)["total"].(float64) != 3 {
		t.Fatalf("list: %v", list)
	}
	first := items[0].(map[string]any)
	if first["analysis"] == nil || first["sync_source_id"] != nil {
		t.Fatalf("list item: %v", first)
	}
	list = e.mustCall(admin, 200, "GET", "/prices?q=sonnet", nil)
	if len(list["data"].([]any)) != 2 {
		t.Fatalf("search: %v", list)
	}

	// A synced price that is edited becomes manual; enabling alone keeps it synced.
	var srcID, haikuID int64
	_ = e.db.SQL.QueryRowContext(context.Background(), `SELECT id FROM price_sync_sources WHERE kind = 'litellm'`).Scan(&srcID)
	_ = e.db.SQL.QueryRowContext(context.Background(), `UPDATE model_prices SET source = 'sync', sync_source_id = $1, synced_at = now()
		WHERE model = 'claude-haiku-4-5' RETURNING id`, srcID).Scan(&haikuID)
	path := "/prices/" + itoa(haikuID)
	if upd := data(e.mustCall(admin, 200, "PATCH", path, map[string]any{"enabled": false})); upd["enabled"] != false ||
		upd["source"] != "sync" || upd["sync_source_name"] != "LiteLLM" {
		t.Fatalf("disable synced: %v", upd)
	}
	e.mustCall(admin, 200, "PATCH", path, map[string]any{"enabled": true})
	upd := data(e.mustCall(admin, 200, "PATCH", path, map[string]any{"mode": "per_token", "config": oneTierConfig(map[string]any{"p": 2, "c": 4}), "note": "cheaper"}))
	if upd["expression"] != `tier("base", p*2 + c*4)` || upd["note"] != "cheaper" || upd["source"] != "manual" || upd["sync_source_id"] != nil {
		t.Fatalf("patch: %v", upd)
	}
	// Resolution sees the change immediately (local invalidation + bus).
	r, err := e.svc.Resolve(context.Background(), core.PriceQuery{Model: "claude-haiku-4-5"})
	if err != nil || r.Expression != `tier("base", p*2 + c*4)` {
		t.Fatalf("resolve after patch: %+v %v", r, err)
	}
	if len(e.bus.sent) == 0 {
		t.Fatal("config:changed not published")
	}
	e.mustCall(admin, 200, "GET", path, nil)
	e.mustCall(admin, 204, "DELETE", path, nil)
	e.mustCall(admin, 404, "GET", path, nil)
}

func TestBalanceAndSettingsAPI(t *testing.T) {
	e := newEnv(t)
	admin := e.user("admin@example.com")
	u := e.user("user@example.com")

	res := data(e.mustCall(admin, 200, "POST", "/users/"+itoa(u)+"/balance/adjust",
		map[string]any{"amount": "20", "credit": true, "note": "topup"}, "Idempotency-Key", "abc"))
	if res["balance_after"] != "20" || res["duplicate"] != false {
		t.Fatalf("adjust: %v", res)
	}
	res = data(e.mustCall(admin, 200, "POST", "/users/"+itoa(u)+"/balance/adjust",
		map[string]any{"amount": "20", "credit": true}, "Idempotency-Key", "abc"))
	if res["duplicate"] != true {
		t.Fatalf("retry: %v", res)
	}
	e.mustCall(admin, 200, "POST", "/users/"+itoa(u)+"/balance/adjust", map[string]any{"amount": 2.5, "credit": false, "note": "fix"})
	_, resp := e.call(admin, "POST", "/users/"+itoa(u)+"/balance/adjust", map[string]any{"amount": "-1", "credit": true})
	if errCode(resp) != "invalid_argument" {
		t.Fatalf("negative: %v", resp)
	}
	_, resp = e.call(admin, "POST", "/users/999999/balance/adjust", map[string]any{"amount": "1", "credit": true})
	if errCode(resp) != "not_found" {
		t.Fatalf("unknown user: %v", resp)
	}

	if b := data(e.mustCall(u, 200, "GET", "/me/balance", nil)); b["balance"] != "17.5" {
		t.Fatalf("me/balance: %v", b)
	}
	if b := data(e.mustCall(admin, 200, "GET", "/me/balance", nil)); b["balance"] != "0" {
		t.Fatalf("admin balance: %v", b)
	}
	mine := e.mustCall(u, 200, "GET", "/me/ledger", nil)["data"].([]any)
	if len(mine) != 2 || mine[0].(map[string]any)["delta"] != "-2.5" || mine[1].(map[string]any)["kind"] != "admin_adjust" {
		t.Fatalf("me/ledger: %v", mine)
	}
	all := e.mustCall(admin, 200, "GET", "/ledger?user_id="+itoa(u)+"&kind=admin_adjust", nil)
	rows := all["data"].([]any)
	if len(rows) != 2 || rows[0].(map[string]any)["user_email"] != "user@example.com" || rows[0].(map[string]any)["operator_id"].(float64) != float64(admin) {
		t.Fatalf("ledger: %v", all)
	}
	if n := len(e.mustCall(admin, 200, "GET", "/ledger?kind=usage", nil)["data"].([]any)); n != 0 {
		t.Fatalf("kind filter: %d", n)
	}
	e.mustCall(admin, 400, "GET", "/ledger?from=yesterday", nil)

	// settings
	st := data(e.mustCall(admin, 200, "GET", "/settings/billing", nil))
	if _, ok := st["missing_price_policy"]; ok || st["min_balance"] != "0" || st["big_cost_warning_usd"] != "10" {
		t.Fatalf("settings: %v", st)
	}
	st = data(e.mustCall(admin, 200, "PUT", "/settings/billing", map[string]any{"min_balance": "1.5"}))
	if st["min_balance"] != "1.5" || st["big_cost_warning_usd"] != "10" {
		t.Fatalf("put settings: %v", st)
	}
	_, resp = e.call(admin, "PUT", "/settings/billing", map[string]any{"big_cost_warning_usd": "0"})
	if errCode(resp) != "invalid_argument" {
		t.Fatalf("bad warning: %v", resp)
	}
	s, _ := e.svc.Settings(context.Background())
	if s.MinBalance.String() != "1.5" {
		t.Fatalf("service settings: %+v", s)
	}
}
