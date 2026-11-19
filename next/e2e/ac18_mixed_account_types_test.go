package e2e

import (
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"
)

// AC 18: account types declare the platforms they serve (ARCHITECTURE 6.6,
// CONTRACTS 13). The market plugin relay declares only an account type
// (relay_key) serving the core's built-in anthropic platform; its accounts
// share a group with anthropic/apikey accounts (the built-in anthropic
// plugin's type, same platform) and together serve /v1/messages, including
// failover across account types. The group, and the API key bound to it,
// serve the anthropic platform. Usage records carry the account type's
// plugin and type; prices are global per model. Disabling relay leaves only
// the anthropic accounts; re-enabling restores the mix.
func TestAC18_MixedAccountTypesServeOneEndpoint(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	m := e.Mock()

	// relay comes from the market (not built in); make sure it ends enabled.
	e.EnsurePlugin(admin, RelayPlugin, "")
	t.Cleanup(func() {
		if d, ok := e.Plugin(admin, RelayPlugin); ok && d.Get("status").String() != "enabled" {
			e.Enable(admin, RelayPlugin)
		}
	})
	d, _ := e.Plugin(admin, RelayPlugin)
	if d.Get("builtin").Bool() {
		t.Fatalf("relay must be a market plugin, not built in: %s", d.Raw)
	}
	// Review: no platform of its own, one account type serving anthropic.
	rev := admin.OK(t, http.MethodGet, "/plugins/"+RelayPlugin+"/versions/"+d.Get("active_version").String()+"/review", nil)
	if len(rev.Get("platforms").Array()) != 0 || len(rev.Get("gateway_endpoints").Array()) != 0 {
		t.Fatalf("relay review: no platforms or endpoints expected: %s", rev.Raw)
	}
	if rat, ok := Find(rev.Get("account_types").Array(), "id", RelayKey); !ok || !slices.Equal(PlatformIDs(rat.Get("platforms")), []string{"anthropic"}) {
		t.Fatalf("relay review account types: %s", rev.Get("account_types").Raw)
	}

	// The registry offers relay/relay_key serving the built-in anthropic
	// platform, hence its endpoints (natively), and its plugin-provided form.
	types := admin.OK(t, http.MethodGet, "/account-types", nil).Array()
	rt, ok := FindAccountType(types, RelayPlugin, RelayKey)
	if !ok {
		t.Fatalf("relay/relay_key not offered: %v", types)
	}
	if p, ok := Find(rt.Get("platforms").Array(), "id", "anthropic"); !ok || !p.Get("builtin").Bool() || !p.Get("available").Bool() || len(rt.Get("platforms").Array()) != 1 {
		t.Fatalf("relay_key platforms: %s", rt.Get("platforms").Raw)
	}
	for _, path := range []string{"/v1/messages", "/v1/messages/count_tokens"} {
		ep, ok := AccountTypeEndpoint(rt, http.MethodPost, path)
		if !ok || !ep.Get("native").Bool() || ep.Get("platform").String() != "anthropic" {
			t.Fatalf("relay_key endpoint %s: %s", path, rt.Get("endpoints").Raw)
		}
	}
	sensitive := false
	for _, f := range rt.Get("sensitive_fields").Array() {
		sensitive = sensitive || f.String() == "api_key"
	}
	if !sensitive {
		t.Fatalf("relay_key api_key must be sensitive: %s", rt.Raw)
	}
	ExpectNativeForm(t, admin.Client, rt, "RelayKeyForm")

	// One group, two account types: anthropic/apikey (priority 50) and
	// relay/relay_key (priority 1), both backed by the mock upstream.
	tn := e.NewTenant(admin, TenantOpts{Accounts: 1, Priorities: []int{50}, Balance: "20"})
	anth := tn.Accounts[0]
	relayKey := fmt.Sprintf("sk-relay-mock-%s-%d", e.RunID, nextSeq())
	relayID := e.CreateAccount(admin, AccountSpec{
		PluginKey: RelayPlugin, Type: RelayKey, GroupIDs: []int64{tn.GroupID},
		APIKey: relayKey, BaseURL: e.MockInternalURL, Priority: 1,
	})
	defer m.ClearRule(t, anth.Key)
	defer m.ClearRule(t, relayKey)

	accts := admin.ListAll(t, "/accounts", "group_id", fmt.Sprint(tn.GroupID))
	for _, want := range []struct {
		id        int64
		plugin, t string
	}{{anth.ID, AnthropicPlugin, AnthropicAPIKey}, {relayID, RelayPlugin, RelayKey}} {
		a, ok := Find(accts, "id", want.id)
		if !ok || a.Get("plugin_key").String() != want.plugin || a.Get("type").String() != want.t {
			t.Fatalf("group accounts: account %d (%s/%s) missing or wrong: %v", want.id, want.plugin, want.t, accts)
		}
	}
	// Both types serve the anthropic platform: so do the group and its key,
	// and /platforms lists both types under anthropic.
	if ids := e.GroupPlatforms(admin, tn.GroupID); !slices.Equal(ids, []string{"anthropic"}) {
		t.Fatalf("group platforms = %v, want [anthropic]", ids)
	}
	if ids := e.APIKeyPlatforms(tn.User, tn.KeyID); !slices.Equal(ids, []string{"anthropic"}) {
		t.Fatalf("api key platforms = %v, want [anthropic]", ids)
	}
	anthropicTypes := func() (anth, relay bool) {
		t.Helper()
		p, ok := Find(e.Platforms(admin), "id", "anthropic")
		if !ok || !p.Get("builtin").Bool() {
			t.Fatalf("/platforms lacks the built-in anthropic platform: %s", p.Raw)
		}
		_, anth = PlatformAccountType(p, AnthropicPlugin, AnthropicAPIKey)
		_, relay = PlatformAccountType(p, RelayPlugin, RelayKey)
		return anth, relay
	}
	if a, r := anthropicTypes(); !a || !r {
		t.Fatalf("/platforms anthropic account types: anthropic/apikey %v, relay/relay_key %v", a, r)
	}

	// ours keeps the mock requests of this tenant's two accounts on path.
	ours := func(mark int64, path string) []string {
		var out []string
		for _, k := range KeysUsed(m.Since(t, mark), path) {
			if k == anth.Key || k == relayKey {
				out = append(out, k)
			}
		}
		return out
	}
	name := map[string]string{anth.Key: "anthropic", relayKey: "relay"}
	names := func(keys []string) []string {
		out := make([]string, len(keys))
		for i, k := range keys {
			out[i] = name[k]
		}
		return out
	}
	wantCost := ExpectedTokenCost(RunPrice, MockInputTokens, MockOutputTokens, MockCacheReadTokens, MockCacheCreationTokens, 0, 1)
	// checkUsage asserts the usage record of a request served by accountID
	// of (plugin, typ): the endpoint stays anthropic's, the price is global.
	checkUsage := func(g *GatewayResult, accountID int64, plugin, typ string) {
		t.Helper()
		u := e.UsageByRequest(admin, tn.User.UserID, g.RequestID)
		if u.Get("account_id").Int() != accountID || u.Get("plugin_key").String() != plugin || u.Get("account_type").String() != typ {
			t.Fatalf("usage account: want %d %s/%s: %s", accountID, plugin, typ, u.Raw)
		}
		if u.Get("platform").String() != "anthropic" || u.Get("protocol").String() != "anthropic.messages" ||
			u.Get("upstream_protocol").String() != "anthropic.messages" || !u.Get("success").Bool() {
			t.Fatalf("usage endpoint/protocols: %s", u.Raw)
		}
		// Usage extracted with the anthropic platform's default rules.
		if u.Get("input_tokens").Int() != MockInputTokens || u.Get("output_tokens").Int() != MockOutputTokens {
			t.Fatalf("usage tokens: %s", u.Raw)
		}
		if u.Get("billing_status").String() != "billed" {
			t.Fatalf("billing_status: %s", u.Raw)
		}
		AssertMoney(t, "usage.total_cost ("+plugin+"/"+typ+")", u.Get("total_cost").String(), wantCost)
	}

	waitCooldownOver := func(id int64) {
		t.Helper()
		Eventually(t, 15*time.Second, 500*time.Millisecond, fmt.Sprintf("account %d cooldown over", id), func() bool {
			return admin.OK(t, http.MethodGet, fmt.Sprintf("/accounts/%d", id), nil).Get("cooldown_until").String() == ""
		})
	}
	// viaFailover makes the anthropic account answer 429 once, so the
	// request fails over to the relay account (another plugin's account
	// type) in the same group, and checks the upstream calls on path.
	viaFailover := func(path string, call func() *GatewayResult) *GatewayResult {
		t.Helper()
		waitCooldownOver(anth.ID)
		m.SetRule(t, MockRule{APIKey: anth.Key, Status: 429, Remaining: 1})
		mark := m.Mark(t)
		g := call()
		if used := ours(mark, path); len(used) != 2 || used[0] != anth.Key || used[1] != relayKey {
			t.Fatalf("%s failover: upstream %v, want [anthropic(429) relay]", path, names(used))
		}
		return g
	}

	// 1. The higher-priority anthropic account serves first.
	mark := m.Mark(t)
	g := e.MustMessages(tn.APIKey, MessagesBody(tn.Model, "mixed 1", false), nil)
	if used := ours(mark, "/v1/messages"); len(used) != 1 || used[0] != anth.Key {
		t.Fatalf("first request: upstream %v, want [anthropic]", names(used))
	}
	checkUsage(g, anth.ID, AnthropicPlugin, AnthropicAPIKey)

	// 2. anthropic answers 429: the request fails over to the relay account.
	g = viaFailover("/v1/messages", func() *GatewayResult {
		return e.MustMessages(tn.APIKey, MessagesBody(tn.Model, "mixed failover", false), nil)
	})
	checkUsage(g, relayID, RelayPlugin, RelayKey)

	// 3. The relay account also serves streams and count_tokens (its upstream
	// path follows meta.protocol).
	g = viaFailover("/v1/messages", func() *GatewayResult {
		return e.MustMessages(tn.APIKey, MessagesBody(tn.Model, "mixed stream", true), nil)
	})
	if g.Events[0].Data.Get("message.usage.input_tokens").Int() != MockInputTokens {
		t.Fatalf("relay stream message_start usage: %s", g.Events[0].Data.Raw)
	}
	u := e.UsageByRequest(admin, tn.User.UserID, g.RequestID)
	if u.Get("account_id").Int() != relayID || u.Get("plugin_key").String() != RelayPlugin ||
		u.Get("account_type").String() != RelayKey || !u.Get("stream").Bool() || u.Get("input_tokens").Int() != MockInputTokens {
		t.Fatalf("relay stream usage: %s", u.Raw)
	}
	ct := viaFailover("/v1/messages/count_tokens", func() *GatewayResult {
		return e.Gateway(t, e.BaseURL, "/v1/messages/count_tokens", tn.APIKey, MessagesBody(tn.Model, "count me", false), nil)
	})
	if ct.Status != 200 || ct.JSON().Get("input_tokens").Int() <= 0 {
		t.Fatalf("count_tokens via relay: %d %s", ct.Status, ct.Body)
	}
	// Both account types have served /v1/messages.

	// 4. relay disabled: its account type no longer serves, its accounts
	// stay. A 429 on anthropic can no longer fail over to relay.
	waitCooldownOver(anth.ID)
	e.Disable(admin, RelayPlugin)
	if _, ok := FindAccountType(admin.OK(t, http.MethodGet, "/account-types", nil).Array(), RelayPlugin, RelayKey); ok {
		t.Fatal("relay_key still offered while relay is disabled")
	}
	if a := admin.OK(t, http.MethodGet, fmt.Sprintf("/accounts/%d", relayID), nil); a.Get("id").Int() != relayID || !a.Get("orphaned").Bool() {
		t.Fatalf("relay account gone after disable: %s", a.Raw)
	}
	// The row stays but the list hides accounts of a disabled plugin unless
	// asked for (?orphaned=all / true).
	listHas := func(q ...string) bool {
		_, ok := Find(admin.OK(t, http.MethodGet, "/accounts", nil, Query(append([]string{"group_id", fmt.Sprint(tn.GroupID), "page_size", "200"}, q...)...)).Array(), "id", relayID)
		return ok
	}
	if listHas() || !listHas("orphaned", "all") || !listHas("orphaned", "true") || listHas("orphaned", "true", "plugin_key", AnthropicPlugin) {
		t.Fatal("orphaned relay account must be hidden by default and listed with orphaned=all|true")
	}
	if a, r := anthropicTypes(); !a || r {
		t.Fatalf("/platforms anthropic account types with relay disabled: anthropic/apikey %v, relay/relay_key %v", a, r)
	}
	// The anthropic account still serves the platform.
	if ids := e.GroupPlatforms(admin, tn.GroupID); !slices.Equal(ids, []string{"anthropic"}) {
		t.Fatalf("group platforms with relay disabled = %v, want [anthropic]", ids)
	}
	mark = m.Mark(t)
	for i := 0; i < 3; i++ {
		e.MustMessages(tn.APIKey, MessagesBody(tn.Model, fmt.Sprintf("relay disabled %d", i), i%2 == 1), nil)
	}
	checkUsage(e.MustMessages(tn.APIKey, MessagesBody(tn.Model, "relay disabled usage", false), nil), anth.ID, AnthropicPlugin, AnthropicAPIKey)
	m.SetRule(t, MockRule{APIKey: anth.Key, Status: 429, Remaining: 1})
	if g := e.Messages(tn.APIKey, MessagesBody(tn.Model, "no relay to fail over to", false), nil); g.Status == 200 {
		t.Fatalf("anthropic 429 with relay disabled should not succeed: %s", g.Body)
	}
	if used := ours(mark, ""); len(used) != 5 || slices.Contains(used, relayKey) {
		t.Fatalf("relay disabled: upstream %v, want 5 x anthropic", names(used))
	}

	// 5. Re-enabled: the relay account serves again.
	e.Enable(admin, RelayPlugin)
	if _, ok := FindAccountType(admin.OK(t, http.MethodGet, "/account-types", nil).Array(), RelayPlugin, RelayKey); !ok {
		t.Fatal("relay_key not offered after re-enable")
	}
	g = viaFailover("/v1/messages", func() *GatewayResult {
		return e.MustMessages(tn.APIKey, MessagesBody(tn.Model, "relay restored", false), nil)
	})
	checkUsage(g, relayID, RelayPlugin, RelayKey)
	waitCooldownOver(anth.ID)
	if !listHas() {
		t.Fatal("relay account not listed again after re-enable")
	}

	// 6. Uninstall with purge_accounts: the relay account is soft-deleted
	// (gone even from orphaned=all), the anthropic one untouched; the
	// plugin is reinstalled from the market for the tests that follow.
	e.Disable(admin, RelayPlugin)
	e.UninstallPurgeAccounts(admin, RelayPlugin)
	if r := admin.API(t, http.MethodGet, fmt.Sprintf("/accounts/%d", relayID), nil); r.Status != 404 {
		t.Fatalf("relay account after uninstall with purge_accounts: %s", r)
	}
	if listHas("orphaned", "all") {
		t.Fatal("purged relay account still listed")
	}
	if a := admin.OK(t, http.MethodGet, fmt.Sprintf("/accounts/%d", anth.ID), nil); a.Get("orphaned").Bool() {
		t.Fatalf("anthropic account touched by relay uninstall: %s", a.Raw)
	}
	e.EnsurePlugin(admin, RelayPlugin, "")
}
