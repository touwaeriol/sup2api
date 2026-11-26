package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/netguard"
)

// TestAccountModelsAndLimits covers the core account attributes of CONTRACTS
// §18: model list, model mapping, weight and rate limits.
func TestAccountModelsAndLimits(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	g1 := e.exec1(`INSERT INTO groups (name) VALUES ('default') RETURNING id`)
	creds := map[string]any{"api_key": "sk-good-key-123"}
	mk := func(extra map[string]any) map[string]any {
		m := map[string]any{"name": "acc", "plugin_key": "anthropic", "type": "apikey",
			"group_ids": []int64{g1}, "credentials": creds}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	fields := func(out map[string]any) string {
		f := fieldsOf(out)
		sort.Strings(f)
		return fmt.Sprint(f)
	}

	// Validation.
	code, out := e.do("POST", "/accounts", mk(map[string]any{
		"models":        []string{"claude-sonnet-4-5", "claude-*", "claude-sonnet-4-5", ""},
		"model_mapping": map[string]string{"gpt-4o": "gpt-4.1", "bad*": "x", "ok": ""},
		"weight":        0, "rpm_limit": -1, "tpm_limit": -5,
	}))
	if code != 400 || fields(out) != "[model_mapping.bad*:invalid model_mapping.ok:invalid models[1]:invalid models[2]:duplicate models[3]:invalid rpm_limit:invalid tpm_limit:invalid weight:invalid]" {
		t.Fatalf("validation: %d %s", code, fields(out))
	}
	if code, out = e.do("POST", "/accounts", mk(map[string]any{"weight": 1001})); code != 400 || fields(out) != "[weight:invalid]" {
		t.Fatalf("weight max: %d %s", code, fields(out))
	}

	// Defaults.
	code, out = e.do("POST", "/accounts", mk(nil))
	if code != 201 {
		t.Fatalf("create: %d %v", code, out)
	}
	acc := out["data"].(map[string]any)
	if acc["weight"] != 1.0 || len(acc["models"].([]any)) != 0 || len(acc["model_mapping"].(map[string]any)) != 0 ||
		acc["rpm_limit"] != 0.0 || acc["tpm_limit"] != 0.0 {
		t.Fatalf("defaults: %v", acc)
	}
	if ru := acc["rate_usage"].(map[string]any); ru["rpm"] != 0.0 || ru["tpm"] != 0.0 {
		t.Fatalf("rate_usage: %v", ru)
	}
	id1 := int64(acc["id"].(float64))

	// Full set.
	code, out = e.do("POST", "/accounts", mk(map[string]any{
		"name": "limited", "priority": 1, "weight": 7,
		"models":        []string{" claude-sonnet-4-5 ", "claude-opus-4-1"},
		"model_mapping": map[string]string{"claude-sonnet-4-5": "claude-sonnet-4-5-20250929"},
		"rpm_limit":     60, "tpm_limit": 100000,
	}))
	if code != 201 {
		t.Fatalf("create limited: %d %v", code, out)
	}
	acc = out["data"].(map[string]any)
	id2 := int64(acc["id"].(float64))
	if fmt.Sprint(acc["models"]) != "[claude-sonnet-4-5 claude-opus-4-1]" || acc["weight"] != 7.0 ||
		acc["model_mapping"].(map[string]any)["claude-sonnet-4-5"] != "claude-sonnet-4-5-20250929" ||
		acc["rpm_limit"] != 60.0 || acc["tpm_limit"] != 100000.0 {
		t.Fatalf("limited view: %v", acc)
	}
	// settings never carries the mapping.
	if _, ok := acc["settings"].(map[string]any)["model_mapping"]; ok {
		t.Fatalf("settings must not contain model_mapping: %v", acc["settings"])
	}

	// List order: newest first, whatever the priority; ?model= filter.
	_, out = e.do("GET", "/accounts", nil)
	items := out["data"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["id"] != float64(id2) {
		t.Fatalf("order: %v", items)
	}
	_, out = e.do("GET", "/accounts?model=claude-opus-4-1", nil)
	if items = out["data"].([]any); len(items) != 2 {
		t.Fatalf("model filter (serves all + listed): %d", len(items))
	}
	_, out = e.do("GET", "/accounts?model=gpt-4o", nil)
	if items = out["data"].([]any); len(items) != 1 || int64(items[0].(map[string]any)["id"].(float64)) != id1 {
		t.Fatalf("model filter (only the unrestricted account): %v", items)
	}

	// The gateway snapshot carries the new attributes.
	refs, err := e.svc.Candidates(ctx, g1, nil)
	if err != nil || len(refs) != 2 {
		t.Fatalf("candidates: %v %v", refs, err)
	}
	var lim core.AccountRef
	for _, r := range refs {
		if r.ID == id2 {
			lim = r
		}
	}
	if lim.Weight != 7 || lim.RPMLimit != 60 || lim.TPMLimit != 100000 ||
		!lim.ServesModel("claude-opus-4-1") || lim.ServesModel("gpt-4o") ||
		lim.MapModel("claude-sonnet-4-5") != "claude-sonnet-4-5-20250929" || lim.MapModel("claude-opus-4-1") != "claude-opus-4-1" {
		t.Fatalf("ref: %+v", lim)
	}
	if a, _ := e.svc.Load(ctx, id2); a == nil || a.Weight != 7 || len(a.Models) != 2 {
		t.Fatalf("load: %+v", a)
	}

	// PATCH replaces models / mapping wholesale and keeps the rest.
	code, out = e.do("PATCH", fmt.Sprintf("/accounts/%d", id2), map[string]any{
		"models": []string{}, "model_mapping": map[string]string{"a": "b"}, "weight": 2})
	if code != 200 {
		t.Fatalf("patch: %d %v", code, out)
	}
	acc = out["data"].(map[string]any)
	if len(acc["models"].([]any)) != 0 || fmt.Sprint(acc["model_mapping"]) != "map[a:b]" ||
		acc["weight"] != 2.0 || acc["rpm_limit"] != 60.0 {
		t.Fatalf("patched: %v", acc)
	}
	if code, out = e.do("PATCH", fmt.Sprintf("/accounts/%d", id2), map[string]any{"model_mapping": map[string]string{"x y": "z"}}); code != 400 ||
		fields(out) != "[model_mapping.x y:invalid]" {
		t.Fatalf("patch invalid mapping: %d %v", code, fields(out))
	}

	// Model lists (CONTRACTS §68): trimmed, repeats and empty lists dropped,
	// a model in one list only. Session caps (§71) on both levels, each over
	// its own window (60 when not given).
	if acc["spm_window_seconds"] != 60.0 {
		t.Fatalf("default session window: %v", acc)
	}
	code, out = e.do("PATCH", fmt.Sprintf("/accounts/%d", id2), map[string]any{"spm_limit": 5, "spm_window_seconds": 300, "model_limits": []map[string]any{
		{"models": []string{" m-a ", "m-a", "m-b"}, "rpm_limit": 3, "max_concurrency": 2, "spm_limit": 4, "spm_window_seconds": 86400}, {"models": []string{}},
		{"models": []string{"m-d"}}}})
	if code != 200 {
		t.Fatalf("patch model limits: %d %v", code, out)
	}
	limits := out["data"].(map[string]any)["model_limits"].([]any)
	if len(limits) != 2 || fmt.Sprint(limits[0].(map[string]any)["models"]) != "[m-a m-b]" || limits[0].(map[string]any)["rpm_limit"] != 3.0 ||
		limits[0].(map[string]any)["spm_limit"] != 4.0 || out["data"].(map[string]any)["spm_limit"] != 5.0 ||
		limits[0].(map[string]any)["spm_window_seconds"] != 86400.0 || limits[1].(map[string]any)["spm_window_seconds"] != 60.0 ||
		out["data"].(map[string]any)["spm_window_seconds"] != 300.0 {
		t.Fatalf("model limits saved as %v", out["data"])
	}
	if a, _ := e.svc.Load(ctx, id2); a == nil || a.SPMLimit != 5 || a.ModelLimits[0].SPMLimit != 4 || a.SPMWindowSeconds != 300 || a.ModelLimits[0].SPMWindowSeconds != 86400 {
		t.Fatalf("load: %+v", a)
	}
	if code, out = e.do("PATCH", fmt.Sprintf("/accounts/%d", id2), map[string]any{"spm_limit": -1, "spm_window_seconds": 59, "model_limits": []map[string]any{
		{"models": []string{"m-a"}}, {"models": []string{"m-c", "m-a"}, "tpm_limit": -1, "spm_limit": 10_000_001, "spm_window_seconds": 86401}}}); code != 400 ||
		fields(out) != "[model_limits[1].models[1]:duplicate model_limits[1].spm_limit:invalid model_limits[1].spm_window_seconds:invalid model_limits[1].tpm_limit:invalid spm_limit:invalid spm_window_seconds:invalid]" {
		t.Fatalf("patch invalid model limits: %d %v", code, fields(out))
	}

	// Rate usage is read from the limiter, with one entry per model list,
	// sessions over the account's window.
	if ok, err := e.svc.d.Limiter.TryHit(ctx, core.AccountRef{ID: id2, SPMWindowSeconds: 300}, "", "session-1"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	e.svc.d.Limiter.AddTokens(ctx, id2, "", 1234)
	_, out = e.do("GET", fmt.Sprintf("/accounts/%d", id2), nil)
	raw, _ := json.Marshal(out)
	if gjson.GetBytes(raw, "data.rate_usage.rpm").Int() != 1 || gjson.GetBytes(raw, "data.rate_usage.tpm").Int() != 1234 ||
		gjson.GetBytes(raw, "data.rate_usage.sessions").Int() != 1 {
		t.Fatalf("rate usage: %s", gjson.GetBytes(raw, "data.rate_usage").Raw)
	}
	if m := gjson.GetBytes(raw, "data.rate_usage.models"); len(m.Array()) != 2 || m.Get("0.models.1").String() != "m-b" || !m.Get("0.in_use").Exists() ||
		!m.Get("0.sessions").Exists() {
		t.Fatalf("model list usage: %s", m.Raw)
	}
	if gjson.GetBytes(raw, "data.tpd_limit").Exists() || gjson.GetBytes(raw, "data.rate_usage.tpd").Exists() {
		t.Fatal("removed rate limits remain exposed", string(raw))
	}

	// The test action maps the model before the plugin builds the request.
	_, _ = e.do("PATCH", fmt.Sprintf("/accounts/%d", id2), map[string]any{"model_mapping": map[string]string{"claude-x": "claude-y"}})
	code, out = e.do("POST", fmt.Sprintf("/accounts/%d/test", id2), map[string]any{"model": "claude-x"})
	if code != 200 {
		t.Fatalf("test: %d %v", code, out)
	}
	e.plat.mu.Lock()
	got := e.plat.tests[len(e.plat.tests)-1].GetModel()
	e.plat.mu.Unlock()
	if got != "claude-y" {
		t.Fatalf("test model sent %q, want claude-y", got)
	}
}

// TestFetchModels covers the "fetch models from the upstream" endpoints
// (CONTRACTS §19).
func TestFetchModels(t *testing.T) {
	e := setup(t)
	g1 := e.exec1(`INSERT INTO groups (name) VALUES ('default') RETURNING id`)

	// Before the account exists: credentials from the form.
	code, out := e.do("POST", "/account-types/anthropic/apikey/models/fetch", map[string]any{
		"credentials": map[string]any{"api_key": "sk-good-key-123"}})
	if code != 200 || fmt.Sprint(out["data"].(map[string]any)["models"]) != "[claude-opus-4-1 claude-sonnet-4-5]" ||
		out["data"].(map[string]any)["skipped"] != 2.0 {
		t.Fatalf("type fetch: %d %v", code, out)
	}
	// Credentials the plugin refuses fail before any upstream call.
	if code, out = e.do("POST", "/account-types/anthropic/apikey/models/fetch", map[string]any{
		"credentials": map[string]any{"api_key": "sk-bad-key"}}); code != 400 || fmt.Sprint(fieldsOf(out)) != "[credentials.api_key:rejected]" {
		t.Fatalf("type fetch validation: %d %v", code, out)
	}
	// Bad key: the upstream answers 401 -> 503 unavailable with the status.
	if code, out = e.do("POST", "/account-types/anthropic/apikey/models/fetch", map[string]any{
		"credentials": map[string]any{"api_key": "sk-wrong-key-123"}}); code != 503 ||
		out["error"].(map[string]any)["details"].(map[string]any)["status"] != 401.0 {
		t.Fatalf("type fetch upstream 401: %d %v", code, out)
	}
	// A type whose plugin does not list models: 501 unsupported.
	if code, out = e.do("POST", "/account-types/video/vkey/models/fetch", map[string]any{
		"credentials": map[string]any{"api_key": "sk-good-key-123"}}); code != 501 || out["error"].(map[string]any)["code"] != "unsupported" {
		t.Fatalf("type fetch unsupported: %d %v", code, out)
	}

	// Saved account: stored credentials, optionally overridden with masked values.
	code, out = e.do("POST", "/accounts", map[string]any{"name": "a", "plugin_key": "anthropic", "type": "apikey",
		"group_ids": []int64{g1}, "credentials": map[string]any{"api_key": "sk-good-key-123"}})
	if code != 201 {
		t.Fatalf("create: %d %v", code, out)
	}
	id := int64(out["data"].(map[string]any)["id"].(float64))
	if code, out = e.do("POST", fmt.Sprintf("/accounts/%d/models/fetch", id), nil); code != 200 ||
		len(out["data"].(map[string]any)["models"].([]any)) != 2 {
		t.Fatalf("account fetch: %d %v", code, out)
	}
	if code, out = e.do("POST", fmt.Sprintf("/accounts/%d/models/fetch", id), map[string]any{
		"credentials": map[string]any{"api_key": core.SecretMask, "base_url": "https://x.example.com"}}); code != 200 ||
		len(out["data"].(map[string]any)["models"].([]any)) != 2 {
		t.Fatalf("account fetch with mask: %d %v", code, out)
	}
	if code, _ = e.do("POST", fmt.Sprintf("/accounts/%d/models/fetch", id), map[string]any{
		"credentials": map[string]any{"api_key": "sk-wrong-key-123"}}); code != 503 {
		t.Fatalf("account fetch with wrong key: %d", code)
	}
}

// Before the account exists, the plugin's Account.proxy is the proxy the
// form chose and the models request goes out through (a saved one by id, a
// pasted URL with id 0), unset when the form chose none and the request goes
// out directly; a chosen proxy that cannot be had fails before the plugin is
// called, never going out directly.
func TestTypeModelsFetchCarriesTheFormProxy(t *testing.T) {
	e := setup(t)
	host, port, hits := forwardProxy(t)
	pid := e.exec1(`INSERT INTO proxies (name, protocol, host, port, username) VALUES ('p', 'http', $1, $2, 'bob') RETURNING id`, host, port)
	const password = "form-proxy-pass"
	built := func() []*pluginv1.BuildModelsRequestRequest {
		e.plat.mu.Lock()
		defer e.plat.mu.Unlock()
		return slices.Clone(e.plat.models)
	}
	fetch := func(choice map[string]any) (int, *pluginv1.AccountProxy) {
		t.Helper()
		body := map[string]any{"credentials": map[string]any{"api_key": "sk-good-key-123"}}
		maps.Copy(body, choice)
		before := len(built())
		code, raw := e.doRaw(e.uid, "POST", "/account-types/anthropic/apikey/models/fetch", body)
		if strings.Contains(string(raw), password) {
			t.Fatal("the response shows the proxy password")
		}
		calls := built()
		if code != http.StatusOK {
			if len(calls) != before {
				t.Fatalf("plugin called although the fetch failed (%d)", code)
			}
			return code, nil
		}
		if len(calls) != before+1 || len(gjson.GetBytes(raw, "data.models").Array()) != 2 {
			t.Fatalf("fetch %v: %d %s (builds %d -> %d)", choice, code, raw, before, len(calls))
		}
		return code, calls[len(calls)-1].GetAccount().GetProxy()
	}

	if _, p := fetch(nil); p != nil || hits.Load() != 0 {
		t.Fatalf("no proxy chosen: plugin proxy set %t, proxy hits %d", p != nil, hits.Load())
	}
	if _, p := fetch(map[string]any{"proxy_id": pid}); p.GetId() != pid || p.GetProtocol() != "http" || p.GetHost() != host ||
		int(p.GetPort()) != port || p.GetUsername() != "bob" || p.GetPassword() != "" || hits.Load() != 1 {
		t.Fatalf("saved proxy chosen: plugin proxy id=%d %s:%d user=%q, proxy hits %d", p.GetId(), p.GetHost(), p.GetPort(), p.GetUsername(), hits.Load())
	}
	pasted := fmt.Sprintf("http://alice:%s@%s:%d", password, host, port)
	if _, p := fetch(map[string]any{"proxy_url": pasted}); p == nil || p.GetId() != 0 || p.GetProtocol() != "http" || p.GetHost() != host ||
		int(p.GetPort()) != port || p.GetUsername() != "alice" || p.GetPassword() != password || hits.Load() != 2 {
		t.Fatalf("pasted proxy chosen: plugin proxy id=%d %s:%d user=%q password matches %t, proxy hits %d",
			p.GetId(), p.GetHost(), p.GetPort(), p.GetUsername(), p.GetPassword() == password, hits.Load())
	}

	if code, _ := fetch(map[string]any{"proxy_url": "ftp://x:1"}); code == http.StatusOK || hits.Load() != 2 {
		t.Fatalf("invalid pasted proxy: %d, proxy hits %d", code, hits.Load())
	}
	if code, _ := fetch(map[string]any{"proxy_id": pid + 1000}); code == http.StatusOK || hits.Load() != 2 {
		t.Fatalf("missing proxy_id: %d, proxy hits %d", code, hits.Load())
	}

	if _, err := e.db.SQL.ExecContext(t.Context(), `UPDATE proxies SET status = 'disabled', updated_at = clock_timestamp() WHERE id = $1`, pid); err != nil {
		t.Fatal(err)
	}
	e.prx.Invalidate(pid)
	if code, _ := fetch(map[string]any{"proxy_id": pid}); code == http.StatusOK || hits.Load() != 2 {
		t.Fatalf("disabled proxy chosen: %d, proxy hits %d", code, hits.Load())
	}
}

// A dial the private-address guard refused does not echo the address the
// proxy name resolved to (M3): models fetch, tests and quota probes all render
// transport failures through transportError.
func TestTransportErrorHidesTheRefusedAddress(t *testing.T) {
	proxyURL, _ := url.Parse("http://localhost:9/")
	hc := &http.Client{Transport: &http.Transport{
		Proxy:       http.ProxyURL(proxyURL),
		DialContext: netguard.Dialer(time.Second, false).DialContext,
	}}
	u, _ := url.Parse("https://upstream.example.com/v1/models?key=secret")
	req, _ := http.NewRequest(http.MethodGet, u.String(), nil)
	_, err := hc.Do(req)
	if !errors.Is(err, netguard.ErrPrivate) {
		t.Fatalf("dial not refused: %v", err)
	}
	got := transportError(upstreamAddr(u), err)
	if want := "Get https://upstream.example.com/v1/models: " + netguard.ErrPrivate.Error(); got != want {
		t.Fatalf("transportError = %q, want %q", got, want)
	}
	if got = transportError("", errors.New("boom")); got != "boom" {
		t.Fatalf("plain error = %q", got)
	}
}
