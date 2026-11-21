package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/secret"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

const litellmSample = `{
  "sample_spec": {"input_cost_per_token": 0},
  "claude-opus-5-5": {"litellm_provider": "anthropic", "mode": "chat", "input_cost_per_token": 4e-06,
    "output_cost_per_token": 2e-05, "cache_read_input_token_cost": 2e-07, "cache_creation_input_token_cost": 5e-06,
    "cache_creation_input_token_cost_above_1hr": 8e-06},
  "claude-sonnet-4-5": {"litellm_provider": "anthropic", "mode": "chat", "input_cost_per_token": 3e-06,
    "output_cost_per_token": 1.5e-05, "cache_read_input_token_cost": 3e-07,
    "input_cost_per_token_above_200k_tokens": 6e-06, "output_cost_per_token_above_200k_tokens": 2.25e-05},
  "gemini/gemini-2.5-flash": {"litellm_provider": "gemini", "mode": "chat", "input_cost_per_token": 3e-07, "output_cost_per_token": 2.5e-06},
  "vertex_ai/gemini-2.5-flash": {"litellm_provider": "vertex_ai-language-models", "mode": "chat", "input_cost_per_token": 9},
  "dall-e-3": {"litellm_provider": "openai", "mode": "image_generation", "input_cost_per_token": 1},
  "text-embedding-3-small": {"litellm_provider": "openai", "mode": "embedding", "input_cost_per_token": 2e-08, "output_cost_per_token": 0},
  "ft:gpt-4o:*": {"litellm_provider": "openai", "mode": "chat", "input_cost_per_token": 1e-06}
}`

func cfgOf(t *testing.T, p *syncPrice) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(p.Config, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// tierOf is tier i of a per_token price.
func tierOf(t *testing.T, p *syncPrice, i int) map[string]any {
	t.Helper()
	return cfgOf(t, p)["tiers"].([]any)[i].(map[string]any)
}

func TestParseLiteLLM(t *testing.T) {
	f, err := parseLiteLLM([]byte(litellmSample), defaultLiteLLMProviders)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range f.prices {
		if err := p.compile(); err != nil {
			t.Fatalf("%s: %v", p.Model, err)
		}
	}
	if len(f.prices) != 4 || f.skipped != 1 {
		t.Fatalf("prices %d skipped %d: %v", len(f.prices), f.skipped, f.prices)
	}
	opus := f.prices["claude-opus-5-5"]
	if opus.Mode != "per_token" || opus.Expression != `v1:tier("base", p*4 + c*20 + cr*0.2 + cc*5 + cc1h*8)` {
		t.Fatalf("opus: %+v", opus)
	}
	require.JSONEq(t, `{"tiers": [{"name": "base", "max_len": null, "unit": "token", "p": 4, "c": 20, "cr": 0.2, "cc": 5, "cc1h": 8}], "rules": []}`, string(opus.Config))
	sonnet := f.prices["claude-sonnet-4-5"]
	if sonnet.Mode != "per_token" || len(cfgOf(t, sonnet)["tiers"].([]any)) != 2 || !strings.Contains(sonnet.Expression, `len <= 200000 ? tier("standard", p*3 + c*15 + cr*0.3)`) ||
		!strings.Contains(sonnet.Expression, `tier("long_context", p*6 + c*22.5 + cr*0.3)`) {
		t.Fatalf("sonnet: %+v", sonnet)
	}
	if g := f.prices["gemini-2.5-flash"]; g == nil || tierOf(t, g, 0)["p"] != 0.3 {
		t.Fatalf("gemini: %+v", g)
	}
	if e := f.prices["text-embedding-3-small"]; e == nil || tierOf(t, e, 0)["p"] != 0.02 {
		t.Fatalf("embedding: %+v", e)
	}
}

func TestParseModelsDev(t *testing.T) {
	body := `{"anthropic": {"models": {"claude-opus-5-5": {"cost": {"input": 4, "output": 20, "cache_read": 0.2, "cache_write": 5}}}},
		"google": {"models": {
			"gemini-2.5-pro": {"cost": {"input": 1.25, "output": 10, "cache_read": 0.125,
				"tiers": [{"input": 2.5, "output": 15, "cache_read": 0.25, "tier": {"type": "context", "size": 200000}}]}},
			"no-cost": {}}},
		"other": {"models": {"x": {"cost": {"input": 1, "output": 1}}}}}`
	f, err := parseModelsDev([]byte(body), defaultModelsDevProviders)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.prices) != 2 {
		t.Fatalf("prices: %v", f.prices)
	}
	if c := tierOf(t, f.prices["claude-opus-5-5"], 0); c["cc"] != 5.0 || c["cc1h"] != nil {
		t.Fatalf("opus: %v", c)
	}
	pro := f.prices["gemini-2.5-pro"]
	if err := pro.compile(); err != nil || pro.Mode != "per_token" || !strings.Contains(pro.Expression, `tier("long_context", p*2.5 + c*15 + cr*0.25)`) {
		t.Fatalf("pro: %+v %v", pro, err)
	}
}

func TestParseSup2APIAppliesMultiplier(t *testing.T) {
	body := `{"data": {"prices": [
		{"model": "claude-opus-5-5", "mode": "per_token", "config": {"tiers": [{"name": "base", "p": 4, "c": 20, "cr": 0.2}]}, "expression": "x"},
		{"model": "web-search", "mode": "per_request", "config": {"price": 0.01}, "expression": "x"},
		{"model": "custom", "mode": "expression", "config": {}, "expression": "tier(\"base\", p*2)"},
		{"model": "bad*", "mode": "per_token", "config": {"tiers": [{"name": "base", "p": 1}]}, "expression": "x"}],
		"rate_multiplier": "1.5", "group": {"id": 3, "name": "g"}}}`
	f, err := parseSup2API([]byte(body), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.prices) != 3 || f.skipped != 1 {
		t.Fatalf("prices %v skipped %d", f.prices, f.skipped)
	}
	for _, p := range f.prices {
		if err := p.compile(); err != nil {
			t.Fatalf("%s: %v", p.Model, err)
		}
	}
	if e := f.prices["claude-opus-5-5"].Expression; e != `v1:tier("base", p*6 + c*30 + cr*0.3)` {
		t.Fatalf("opus: %s", e)
	}
	if e := f.prices["web-search"].Expression; e != `v1:tier("base", flat(0.015))` {
		t.Fatalf("per request: %s", e)
	}
	if e := f.prices["custom"].Expression; e != `v1:(tier("base", p*2)) * 1.5` {
		t.Fatalf("expression: %s", e)
	}
	f, _ = parseSup2API([]byte(body), false)
	_ = f.prices["claude-opus-5-5"].compile()
	if e := f.prices["claude-opus-5-5"].Expression; e != `v1:tier("base", p*4 + c*20 + cr*0.2)` {
		t.Fatalf("without multiplier: %s", e)
	}
}

// How the upstream rate goes into per_token tiers with rules (their prices,
// not their multipliers) and a request tier.
func TestParseSup2APIMultiplierOnTiers(t *testing.T) {
	body := `{"data": {"prices": [
		{"model": "tiered", "mode": "per_token", "expression": "x", "config": {"tiers": [
			{"name": "standard", "max_len": 200000, "p": 3, "c": 15, "img": 10}, {"name": "long_context", "max_len": null, "unit": "request", "price": 0.5}],
			"rules": [{"multiplier": 2, "conds": [{"source": "param", "key": "fast", "op": "exists"}]}]}}],
		"rate_multiplier": "1.5", "group": {"id": 3, "name": "g"}}}`
	f, err := parseSup2API([]byte(body), true)
	require.NoError(t, err)
	require.Zero(t, f.skipped)
	tiered := f.prices["tiered"]
	require.NoError(t, tiered.compile())
	require.Equal(t, "per_token", tiered.Mode)
	require.JSONEq(t, `{"tiers": [{"name": "standard", "max_len": 200000, "p": 4.5, "c": 22.5, "img": 15}, {"name": "long_context", "max_len": null, "unit": "request", "price": 0.75}],
		"rules": [{"multiplier": 2, "conds": [{"source": "param", "key": "fast", "op": "exists"}]}]}`, string(tiered.Config))
	require.Equal(t, `v1:(len <= 200000 ? tier("standard", p*4.5 + c*22.5 + img*15) : tier("long_context", flat(0.75))) * (param("fast") != nil ? 2 : 1)`, tiered.Expression)
}

// An upstream older than core migration 0121 sends flat per_token configs,
// visual or video expression configs and "|||" rules: they arrive in the
// current shapes, as the migration writes stored prices. Numeric-string
// prices are scaled.
func TestParseSup2APIFromOldUpstream(t *testing.T) {
	body := `{"data": {"prices": [
		{"model": "flat", "mode": "per_token", "config": {"p": "0.15", "c": 0.6, "cr": null}, "expression": "tier(\"base\", p*0.15 + c*0.6)"},
		{"model": "visual", "mode": "expression", "config": {"tiers": [{"name": "base", "prices": {"p": 1}}], "rules": [{"source": "param", "name": "fast", "multiplier": 2}]},
			"expression": "tier(\"base\", p*1 + c*0) ||| param(\"fast\") == true ? 2 : 1"},
		{"model": "video", "mode": "expression", "config": {"video": {"price_per_million_tokens": 7}}, "expression": "tier(\"video\", c*7)"},
		{"model": "per-request", "mode": "per_request", "config": {"price": "0.04"}}],
		"rate_multiplier": "2", "group": {"id": 3, "name": "g"}}}`
	f, err := parseSup2API([]byte(body), false)
	require.NoError(t, err)
	require.Zero(t, f.skipped)
	for model, want := range map[string][3]string{
		"flat": {"per_token", `{"tiers": [{"name": "base", "max_len": null, "unit": "token", "p": "0.15", "c": 0.6}], "rules": []}`,
			`v1:tier("base", p*0.15 + c*0.6)`},
		"visual":      {"expression", `{}`, `v1:(tier("base", p*1 + c*0)) * (param("fast") == true ? 2 : 1)`},
		"video":       {"expression", `{}`, `v1:tier("video", c*7)`},
		"per-request": {"per_request", `{"price": "0.04"}`, `v1:tier("base", flat(0.04))`},
	} {
		p := f.prices[model]
		require.NoError(t, p.compile(), model)
		require.Equal(t, want[0], p.Mode, model)
		require.JSONEq(t, want[1], string(p.Config), model)
		require.Equal(t, want[2], p.Expression, model)
	}

	f, err = parseSup2API([]byte(body), true)
	require.NoError(t, err)
	require.Zero(t, f.skipped)
	require.JSONEq(t, `{"tiers": [{"name": "base", "max_len": null, "unit": "token", "p": 0.3, "c": 1.2}], "rules": []}`, string(f.prices["flat"].Config))
	require.JSONEq(t, `{"price": 0.08}`, string(f.prices["per-request"].Config))
	require.Equal(t, "((tier(\"base\", p*1 + c*0)) * (param(\"fast\") == true ? 2 : 1)) * 2", f.prices["visual"].Expression)
}

func TestParseSup2APISkipsPriceWithoutConfig(t *testing.T) {
	body := `{"data": {"prices": [
		{"model": "no-config", "mode": "per_token", "expression": ""},
		{"model": "null-config", "mode": "per_request", "config": null},
		{"model": "ok", "mode": "per_token", "config": {"tiers": [{"name": "base", "p": 1, "c": 2}]}}],
		"rate_multiplier": "2", "group": {"id": 3, "name": "g"}}}`
	f, err := parseSup2API([]byte(body), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.prices) != 1 || f.skipped != 2 || f.prices["ok"] == nil {
		t.Fatalf("prices %v skipped %d", f.prices, f.skipped)
	}
}

// An expression price needs no config to carry the multiplier: without one,
// null or missing, it is multiplied as before (scalePrice).
func TestParseSup2APIScalesExpressionWithoutConfig(t *testing.T) {
	body := `{"data": {"prices": [
		{"model": "null-config", "mode": "expression", "config": null, "expression": "tier(\"base\", p*2)"},
		{"model": "no-config", "mode": "expression", "expression": "tier(\"base\", p*2)"}],
		"rate_multiplier": "1.5", "group": {"id": 3, "name": "g"}}}`
	f, err := parseSup2API([]byte(body), true)
	require.NoError(t, err)
	require.Zero(t, f.skipped)
	for _, model := range []string{"null-config", "no-config"} {
		p := f.prices[model]
		require.NotNil(t, p, model)
		require.Equal(t, []string{"expression", "{}", `(tier("base", p*2)) * 1.5`},
			[]string{p.Mode, string(p.Config), p.Expression}, model)
	}
}

// fakeKeys authenticates "sk-good" into a group with multiplier 2. Group and
// Segment, when set, are the group of the key and the segment of its owner.
type fakeKeys struct {
	Mode           string
	Group, Segment int64
}

func (f fakeKeys) Authenticate(_ context.Context, raw string) (*core.APIKeyPrincipal, error) {
	if raw != "sk-good" {
		return nil, core.ErrUnauthenticated
	}
	pr := &core.APIKeyPrincipal{KeyID: 1, UserID: 1, SegmentID: f.Segment, Group: core.GroupInfo{ID: 7, Name: "vip",
		RateMultiplier: decimal.NewFromInt(2), ModelAllowlist: []string{"claude-*"}, ModelFilterMode: f.Mode}}
	if f.Group != 0 {
		pr.Group.ID = f.Group
	}
	return pr, nil
}

func TestPriceSourcesSync(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.user("admin@example.com")
	cipher, err := secret.New([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/litellm.json":
			_, _ = w.Write([]byte(litellmSample))
		default:
			// An upstream sup2api: forward to this test instance's /key/prices.
			req := httptest.NewRequest("GET", "/api/v1/key/prices", nil)
			req.Header.Set("Authorization", r.Header.Get("Authorization"))
			e.h.ServeHTTP(w, req)
		}
	}))
	defer upstream.Close()
	e.svc.SetSyncDeps(SyncDeps{Cipher: cipher, Keys: fakeKeys{}, HTTP: upstream.Client()})

	// Seeded sources.
	list := e.mustCall(admin, 200, "GET", "/price-sources", nil)["data"].([]any)
	if len(list) != 2 || list[0].(map[string]any)["kind"] != "litellm" || list[1].(map[string]any)["kind"] != "models_dev" {
		t.Fatalf("seeded: %v", list)
	}
	litellmID := int64(list[0].(map[string]any)["id"].(float64))
	e.mustCall(admin, 200, "PATCH", "/price-sources/"+itoa(litellmID), map[string]any{"url": upstream.URL + "/litellm.json"})

	// A manual price that differs from the source.
	e.mustCall(admin, 201, "POST", "/prices", map[string]any{"model": "claude-opus-5-5", "mode": "per_token", "config": oneTierConfig(map[string]any{"p": 1, "c": 1})})

	prev := data(e.mustCall(admin, 200, "POST", "/price-sources/"+itoa(litellmID)+"/preview", nil))
	actions := map[string]string{}
	for _, it := range prev["items"].([]any) {
		m := it.(map[string]any)
		actions[m["model"].(string)] = m["action"].(string)
	}
	if prev["total"].(float64) != 4 || actions["claude-opus-5-5"] != "manual" || actions["claude-sonnet-4-5"] != "create" {
		t.Fatalf("preview: %v", actions)
	}

	res := data(e.mustCall(admin, 200, "POST", "/price-sources/"+itoa(litellmID)+"/apply",
		map[string]any{"models": []string{"claude-sonnet-4-5", "gemini-2.5-flash", "nope"}}))
	if res["created"].(float64) != 2 || len(res["skipped"].([]any)) != 1 {
		t.Fatalf("apply: %v", res)
	}
	// The manual price was not selected and stays manual.
	r, err := e.svc.Resolve(ctx, core.PriceQuery{Model: "claude-opus-5-5"})
	if err != nil || r.Expression != `v1:tier("base", p*1 + c*1)` && r.Expression != `tier("base", p*1 + c*1)` {
		t.Fatalf("manual kept: %+v %v", r, err)
	}
	res = data(e.mustCall(admin, 200, "POST", "/price-sources/"+itoa(litellmID)+"/apply",
		map[string]any{"models": []string{"claude-opus-5-5", "claude-sonnet-4-5"}}))
	if res["updated"].(float64) != 1 || res["unchanged"].(float64) != 1 {
		t.Fatalf("apply 2: %v", res)
	}
	synced := e.mustCall(admin, 200, "GET", "/prices?source=sync&sync_source_id="+itoa(litellmID), nil)
	if synced["page"].(map[string]any)["total"].(float64) != 3 {
		t.Fatalf("synced prices: %v", synced)
	}
	if r, _ := e.svc.Resolve(ctx, core.PriceQuery{Model: "claude-opus-5-5"}); !strings.Contains(r.Expression, "cc1h*8") {
		t.Fatalf("opus after sync: %+v", r)
	}

	// /key/prices: API key auth, group allowlist, multiplier.
	code, _ := e.call(0, "GET", "/key/prices", nil, "Authorization", "Bearer sk-bad")
	if code != 401 {
		t.Fatalf("bad key: %d", code)
	}
	_, kp := e.call(0, "GET", "/key/prices", nil, "Authorization", "Bearer sk-good")
	d := kp["data"].(map[string]any)
	if d["rate_multiplier"] != "2" || len(d["prices"].([]any)) != 2 {
		t.Fatalf("key prices: %v", d)
	}

	// A sup2api source requires an API key and imports with the multiplier.
	_, resp := e.call(admin, "POST", "/price-sources", map[string]any{"name": "up", "kind": "sup2api", "url": upstream.URL})
	if errCode(resp) != "invalid_argument" {
		t.Fatalf("sup2api without key: %v", resp)
	}
	src := data(e.mustCall(admin, 201, "POST", "/price-sources", map[string]any{"name": "up", "kind": "sup2api",
		"url": upstream.URL, "api_key": "sk-good"}))
	if src["has_api_key"] != true || src["options"].(map[string]any)["apply_multiplier"] != true {
		t.Fatalf("source: %v", src)
	}
	var enc []byte
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT api_key_enc FROM price_sync_sources WHERE name = 'up'`).Scan(&enc)
	if strings.Contains(string(enc), "sk-good") {
		t.Fatal("API key stored in clear")
	}
	upID := itoa(int64(src["id"].(float64)))
	prev = data(e.mustCall(admin, 200, "POST", "/price-sources/"+upID+"/preview", nil))
	for _, it := range prev["items"].([]any) {
		m := it.(map[string]any)
		if m["model"] == "claude-opus-5-5" && (m["action"] != "update" ||
			!strings.Contains(m["incoming"].(map[string]any)["expression"].(string), "p*8 + c*40")) {
			t.Fatalf("upstream opus: %v", m)
		}
	}
	e.mustCall(admin, 409, "POST", "/price-sources", map[string]any{"name": "up", "kind": "litellm", "url": upstream.URL})

	// Fetch failures are reported and recorded.
	e.mustCall(admin, 200, "PATCH", "/price-sources/"+upID, map[string]any{"api_key": "sk-bad"})
	_, resp = e.call(admin, "POST", "/price-sources/"+upID+"/preview", nil)
	if errCode(resp) != "unavailable" {
		t.Fatalf("bad upstream key: %v", resp)
	}
	var lastErr string
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT last_error FROM price_sync_sources WHERE name = 'up'`).Scan(&lastErr)
	if !strings.Contains(lastErr, "401") {
		t.Fatalf("last_error = %q", lastErr)
	}

	// Deleting a source keeps its prices.
	e.mustCall(admin, 204, "DELETE", "/price-sources/"+itoa(litellmID), nil)
	var n int
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM model_prices WHERE source = 'sync' AND sync_source_id IS NULL`).Scan(&n)
	if n != 3 {
		t.Fatalf("orphaned synced prices: %d", n)
	}
}

func TestKeyPriceListBlacklist(t *testing.T) {
	e := newEnv(t)
	admin := e.user("admin-policy@example.com")
	e.svc.SetSyncDeps(SyncDeps{Keys: fakeKeys{Mode: "blacklist"}})
	for _, model := range []string{"claude-opus-5-5", "gpt-5"} {
		e.mustCall(admin, 201, "POST", "/prices", map[string]any{"model": model, "mode": "per_request", "config": map[string]any{"price": 1}})
	}
	code, out := e.call(0, "GET", "/key/prices", nil, "Authorization", "Bearer sk-good")
	if code != 200 {
		t.Fatalf("prices: %d %v", code, out)
	}
	rows := data(out)["prices"].([]any)
	for _, row := range rows {
		if strings.HasPrefix(row.(map[string]any)["model"].(string), "claude-") {
			t.Fatalf("blacklisted model leaked: %v", rows)
		}
	}
	if len(rows) == 0 {
		t.Fatal("allowed prices omitted")
	}
}

// GET /me/prices: every priced model with the caller's groups that provide it,
// or the models of one of those groups at the caller's rate there.
func TestMyPrices(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	uid := e.user("me@example.com")
	var public, restricted, mine int64
	for _, g := range []struct {
		name, visibility, rate string
		allow                  []string
		id                     *int64
	}{
		{"public", "public", "2", []string{"claude-*"}, &public},
		{"restricted", "restricted", "3", []string{}, &restricted},
		{"mine", "restricted", "0.5", []string{}, &mine},
	} {
		if err := e.db.SQL.QueryRowContext(ctx, `INSERT INTO groups (name, visibility, rate_multiplier, model_allowlist, model_filter_mode) VALUES ($1, $2, $3, $4, 'whitelist') RETURNING id`,
			g.name, g.visibility, g.rate, g.allow).Scan(g.id); err != nil {
			t.Fatal(err)
		}
	}
	// The caller's segment is granted "mine"; an account that declares no
	// models makes every group provide every priced model its policy allows.
	e.exec(`INSERT INTO segments (name) VALUES ('granted')`)
	e.exec(`UPDATE users SET segment_id = (SELECT id FROM segments WHERE name = 'granted') WHERE id = $1`, uid)
	e.exec(`INSERT INTO segment_groups (segment_id, group_id, granted) SELECT id, $1, true FROM segments WHERE name = 'granted'`, mine)
	e.exec(`INSERT INTO accounts (name, plugin_key, type, credentials_enc) VALUES ('a', 'anthropic', 'apikey', '\x00'::bytea)`)
	e.exec(`INSERT INTO account_groups (account_id, group_id) SELECT a.id, g.id FROM accounts a, groups g`)
	e.exec(`DELETE FROM model_prices`)
	for _, m := range []string{"claude-opus-5-5", "gemini-3"} {
		e.exec(`INSERT INTO model_prices (model, mode, config, expression, expr_hash, source, enabled) VALUES ($1, 'per_request', '{"price": 1}', 'tier("base", flat(1))', 'h', 'manual', true)`, m)
	}
	e.exec(`INSERT INTO model_prices (model, mode, config, expression, expr_hash, source, enabled) VALUES ('claude-off', 'per_request', '{"price": 1}', 'tier("base", flat(1))', 'h', 'manual', false)`)

	models := func(d map[string]any) string {
		var out []string
		for _, p := range d["prices"].([]any) {
			out = append(out, p.(map[string]any)["model"].(string))
		}
		return strings.Join(out, ",")
	}
	d := data(e.mustCall(uid, 200, "GET", "/me/prices?group_id="+itoa(public), nil))
	if d["rate_multiplier"] != "2" || models(d) != "claude-opus-5-5" {
		t.Fatalf("public group: %v", d)
	}
	d = data(e.mustCall(uid, 200, "GET", "/me/prices?group_id="+itoa(mine), nil))
	if d["rate_multiplier"] != "0.5" || models(d) != "claude-opus-5-5,gemini-3" {
		t.Fatalf("own group: %v", d)
	}
	if code, _ := e.call(uid, "GET", "/me/prices?group_id="+itoa(restricted), nil); code != 404 {
		t.Fatalf("a restricted group of others: %d", code)
	}
	if code, _ := e.call(uid, "GET", "/me/prices?group_id=x", nil); code != 400 {
		t.Fatalf("a bad id: %d", code)
	}
	// Without a group: every priced model at the global price, with the
	// caller's groups that provide it.
	d = data(e.mustCall(uid, 200, "GET", "/me/prices", nil))
	if d["group"] != nil || d["rate_multiplier"] != "1" || models(d) != "claude-opus-5-5,gemini-3" {
		t.Fatalf("every group: %v", d)
	}
	groups := func(row any) string {
		var out []string
		for _, g := range row.(map[string]any)["groups"].([]any) {
			out = append(out, g.(map[string]any)["name"].(string))
		}
		return strings.Join(out, ",")
	}
	rows := d["prices"].([]any)
	if groups(rows[0]) != "public,mine" || groups(rows[1]) != "mine" {
		t.Fatalf("groups of the models: %v", rows)
	}
}
