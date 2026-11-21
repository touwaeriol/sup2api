package usage

import (
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// The account of a gateway.attempt hook's decision is shown with the record's
// account only: to an operator with account:read (M11).
func TestDetailHidesHookDecisionAccounts(t *testing.T) {
	f := newFixture(t)
	account := int64(42)
	r := f.record("hook-accounts", true)
	r.AccountID = &account
	r.HookDecisions = []core.HookDecision{
		{PluginKey: "guard", HookID: "request", Decision: "allow", LatencyMs: 3},
		{PluginKey: "guard", HookID: "attempt", Decision: "deny", Note: "busy", AccountID: account},
	}
	f.svc.process(t.Context(), []*core.UsageRecord{r})
	id := f.scalar(`SELECT id::text FROM usage_logs WHERE request_id = 'hook-accounts'`)
	hooks := func(path string) []any {
		t.Helper()
		return f.get(f.user, path, 200)["data"].(map[string]any)["hook_decisions"].([]any)
	}
	redacted := []any{
		map[string]any{"plugin_key": "guard", "hook_id": "request", "decision": "allow", "latency_ms": float64(3)},
		map[string]any{"plugin_key": "guard", "hook_id": "attempt", "decision": "deny", "latency_ms": float64(0), "note": "busy"},
	}

	require.Equal(t, float64(account), hooks("/usage/" + id)[1].(map[string]any)["account_id"], "account:read keeps it")
	require.Equal(t, redacted, hooks("/me/usage/"+id))

	f.svc.Authz = denyPerms{keys: []string{"account:read"}}
	require.Equal(t, redacted, hooks("/usage/"+id))
}

func TestWithoutAccountIDs(t *testing.T) {
	for in, want := range map[string]string{
		``:     ``,
		`null`: `null`,
		`[]`:   `[]`,
		`[{"hook_id":"a","account_id":7},{"hook_id":"b"},{"account_id":8,"note":"n"}]`: `[{"hook_id":"a"},{"hook_id":"b"},{"note":"n"}]`,
		// Not the array it should be: nothing is shown rather than unredacted.
		`{"account_id":7}`:        ``,
		`[{"account_id":7}`:       ``,
		`null [{"account_id":7}]`: ``,
	} {
		require.Equal(t, want, string(withoutAccountIDs(json.RawMessage(in))), in)
	}
}

func TestWithoutExpressions(t *testing.T) {
	for in, want := range map[string]string{
		``:     ``,
		`null`: `null`,
		`{}`:   `{}`,
		`{"tier":"standard","inputs":{"params":{"a":"b"},"pricing":{"rate_source":"segment","base":{"expression":"p*1","expr_hash":"h"}}}}`: `{"tier":"standard","inputs":{"params":{"a":"b"},"pricing":{"rate_source":"segment","base":{}}}}`,
		`{"additional":[{"kind":"k","model":"m","expression_hash":"h","detail":{"tier":"t"}}],"replacement":[{"expression_hash":"h"},{"model":"m"}],` +
			`"inputs":{"additional":[{"Kind":"k","Price":{"Expression":"p*2","ExprHash":"h"},"Tokens":{"Input":1},"Metrics":{"x":1}}],"replacement":[{"Price":{"ID":3}},{"Tokens":{}}]}}`: `{"additional":[{"kind":"k","model":"m","detail":{"tier":"t"}}],"replacement":[{},{"model":"m"}],` +
			`"inputs":{"additional":[{"Kind":"k","Tokens":{"Input":1},"Metrics":{"x":1}}],"replacement":[{},{"Tokens":{}}]}}`,
		// The detail of a component priced by a custom rule has the global price behind it too.
		`{"additional":[{"detail":{"tier":"t","inputs":{"pricing":{"price_source":"group","base":{"expression":"p*3","expr_hash":"g"}}}}}],"replacement":[{"detail":{"inputs":{"pricing":{"base":{"expression":"p*4"}}}}}]}`: `{"additional":[{"detail":{"tier":"t","inputs":{"pricing":{"price_source":"group","base":{}}}}}],"replacement":[{"detail":{"inputs":{"pricing":{"base":{}}}}}]}`,
		// Nor the upstream model of a segment billed at the requested model.
		`{"inputs":{"requested_model_segments":[{"part":"additional","index":0,"model":"up"}]}}`: `{"inputs":{"requested_model_segments":[{"part":"additional","index":0}]}}`,
		// Not JSON: nothing is shown rather than unredacted.
		`{"inputs":`: ``,
	} {
		require.Equal(t, want, string(withoutExpressions(json.RawMessage(in))), in)
	}
}

// GET /me/usage/:id tells the owner what they asked, how it went and what it
// cost; the upstream side and the host's internals are not theirs to see.
func TestSelfDetailHidesInternals(t *testing.T) {
	f := newFixture(t)
	account := int64(42)
	r := f.record("self-internals", true)
	r.AccountID = &account
	r.PluginVersion, r.StickyRule, r.NodeID = "1.2.3", "header:x-session", "node-a"
	r.Metrics = map[string]any{"web_search_requests": 2}
	f.svc.process(t.Context(), []*core.UsageRecord{r})
	id := f.scalar(`SELECT id::text FROM usage_logs WHERE request_id = 'self-internals'`)

	d := f.get(f.user, "/usage/"+id, 200)["data"].(map[string]any)
	require.Equal(t, "1.2.3", d["plugin_version"])
	require.Equal(t, "header:x-session", d["sticky_rule"])
	require.Equal(t, "node-a", d["node_id"])
	require.NotEmpty(t, d["expr_hash"])

	self := f.get(f.user, "/me/usage/"+id, 200)["data"].(map[string]any)
	for _, field := range []string{"plugin_version", "sticky_rule", "expr_hash", "node_id", "account_type", "upstream_protocol", "upstream_model"} {
		require.Empty(t, self[field], field)
	}
	require.Nil(t, self["account_id"])
	for _, field := range []string{"account_name", "plugin_detail", "anomalies", "settlement", "request_log_account_id"} {
		require.NotContains(t, self, field)
	}
	// What the user's own page shows stays (web UsageDetail.vue).
	require.Equal(t, map[string]any{"web_search_requests": float64(2)}, self["metrics"])
	require.Equal(t, d["billing_detail"], self["billing_detail"], "nothing to hide in an ordinary detail")
	require.Nil(t, self["conversion"])
	require.Equal(t, d["total_cost"], self["total_cost"])
	require.Equal(t, d["price_id"], self["price_id"])
	require.Equal(t, d["ledger_id"], self["ledger_id"])
	require.Len(t, self["hook_decisions"], 1)
}

// A component priced by a group's custom rule keeps the global price behind
// it in its own detail; the owner of the request does not see it.
func TestSelfDetailHidesComponentGlobalPrice(t *testing.T) {
	f := newFixture(t)
	global := `tier("global-advisor-price", p*7)`
	r := f.record("self-component-price", true)
	r.Additional = []core.PricedUsage{{Kind: "advisor", Model: "other-model", UsageSemantics: "exclusive", Tokens: core.UsageTokens{Input: 1_000_000}, RateMultiplier: decimal.NewFromInt(1),
		Price: &core.PriceRule{ID: 9, Expression: `tier("group-advisor-price", p*5)`, Mode: "expression", Source: core.PriceSourceGroup, Base: &core.PriceBase{Expression: global, ExprHash: "global-advisor-hash"}}}}
	f.svc.process(t.Context(), []*core.UsageRecord{r})
	id := f.scalar(`SELECT id::text FROM usage_logs WHERE request_id = 'self-component-price'`)
	detail := func(path string) string {
		t.Helper()
		out, err := json.Marshal(f.get(f.user, path, 200)["data"].(map[string]any)["billing_detail"])
		require.NoError(t, err)
		return string(out)
	}

	require.Contains(t, detail("/usage/"+id), "global-advisor-price", "the operator view keeps it")
	self := detail("/me/usage/" + id)
	// The component's own breakdown (its tier and terms) stays, as the primary usage's does.
	for _, hidden := range []string{"global-advisor-price", "global-advisor-hash"} {
		require.NotContains(t, self, hidden)
	}
	require.Contains(t, self, `"total_cost"`, "what the component cost stays")
}

// redactForSelf also clears what the self branch never loads, so a later
// change that loads it for everyone does not leak it.
func TestRedactForSelfClearsEveryInternalField(t *testing.T) {
	account := int64(7)
	d := Detail{
		Record:        Record{AccountID: &account, AccountName: "a", AccountType: "oauth", UpstreamProtocol: "p", UpstreamModel: "m", Conversion: json.RawMessage(`{}`)},
		PluginVersion: "1", StickyRule: "r", ExprHash: "h", NodeID: "n",
		HookDecisions: json.RawMessage(`[{"hook_id":"a","account_id":7}]`),
		BillingDetail: json.RawMessage(`{"inputs":{"pricing":{"base":{"expression":"p","expr_hash":"h"}}}}`),
		PluginDetail:  json.RawMessage(`{}`), Anomalies: json.RawMessage(`{}`),
		Settlement: &Settlement{AccountID: &account},
	}
	d.redactForSelf()
	require.Equal(t, Detail{HookDecisions: json.RawMessage(`[{"hook_id":"a"}]`),
		BillingDetail: json.RawMessage(`{"inputs":{"pricing":{"base":{}}}}`)}, d)
}
