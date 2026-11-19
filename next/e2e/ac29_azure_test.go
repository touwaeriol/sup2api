package e2e

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/tidwall/gjson"
)

// The market plugin azure_openai and its two account types
// (docs/DESIGN-2026-11-17-CLOUD-PROVIDERS.md §6).
const (
	AzurePlugin         = "azure_openai"
	AzureV1Type         = "azure_v1"
	AzureDeploymentType = "azure_deployment"
	azureAPIVersion     = "2024-10-21"
)

// AC 29: Azure OpenAI accounts. azure_openai is a market code plugin
// (its own process, forwarding through Execute) adding two account types to the built-in openai platform: the same
// resource endpoint and key, served through the v1 API (azure_v1) or the
// classic per-deployment API (azure_deployment). With the mock upstream as
// the resource, /v1/chat/completions is answered through both, streaming
// and not: the request goes to the type's Azure path with the key in
// api-key, the upstream model is the deployment the account maps the
// client's model to, the client's OpenAI-Organization / OpenAI-Project and
// service_tier are not sent, and the usage is billed at the price of the
// client's model. The resource endpoint is saved as written; a trailing
// version segment (/openai/v1, /openai) is dropped per request, so the
// request path does not repeat it (CONTRACTS §127). A pasted request URL is
// refused as the resource endpoint.
func TestAC29_AzureOpenAI(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	m := e.Mock()

	// 1. From the market, not built in; both types serve the openai platform.
	e.EnsurePlugin(admin, AzurePlugin, "")
	if d, _ := e.Plugin(admin, AzurePlugin); d.Get("builtin").Bool() {
		t.Fatalf("%s must be a market plugin, not built in: %s", AzurePlugin, d.Raw)
	}
	types := admin.OK(t, http.MethodGet, "/account-types", nil).Array()
	for _, typ := range []string{AzureV1Type, AzureDeploymentType} {
		at, ok := FindAccountType(types, AzurePlugin, typ)
		if !ok {
			t.Fatalf("%s/%s not offered: %v", AzurePlugin, typ, types)
		}
		if ids := PlatformIDs(at.Get("platforms")); !slices.Equal(ids, []string{"openai"}) {
			t.Fatalf("%s/%s platforms = %v, want [openai]", AzurePlugin, typ, ids)
		}
		if ep, ok := AccountTypeEndpoint(at, http.MethodPost, "/v1/chat/completions"); !ok || ep.Get("platform").String() != "openai" {
			t.Fatalf("%s/%s lacks /v1/chat/completions: %s", AzurePlugin, typ, at.Get("endpoints").Raw)
		}
	}

	// 2. One group and key per type; the client's model maps to a deployment.
	tn := e.NewTenant(admin, TenantOpts{Balance: "20"})
	model := e.RunModelOf(admin, "gpt", "azure")
	type variant struct {
		// baseURL is what the account form sends, saved what GET
		// /accounts/:id returns.
		typ, baseURL, saved string
		settings            map[string]any
		// path and query are where a chat request for deployment goes.
		path  func(deployment string) string
		query string

		deployment, upstreamKey, apiKey string
		accountID                       int64
	}
	variants := []*variant{
		// Saved with its /openai/v1; the request does not repeat it.
		{typ: AzureV1Type, baseURL: e.MockInternalURL + "/openai/v1", saved: e.MockInternalURL + "/openai/v1",
			path: func(string) string { return "/openai/v1/chat/completions" }},
		// Saved without the trailing slash, with its /openai; the request
		// does not repeat it either.
		{typ: AzureDeploymentType, baseURL: e.MockInternalURL + "/openai/", saved: e.MockInternalURL + "/openai", settings: map[string]any{"api_version": azureAPIVersion},
			path:  func(deployment string) string { return "/openai/deployments/" + deployment + "/chat/completions" },
			query: "api-version=" + azureAPIVersion},
	}
	var groups []int64
	for _, v := range variants {
		// An Azure key has no vendor prefix.
		v.upstreamKey = fmt.Sprintf("azure%s%d0123456789abcdef", e.RunID, nextSeq())
		v.deployment = fmt.Sprintf("deploy-%s-%d", e.RunID, nextSeq())
		gid := e.CreateGroup(admin, e.Name("grp-azure"), "restricted", 1, nil)
		groups = append(groups, gid)
		v.accountID = e.CreateAccount(admin, AccountSpec{
			PluginKey: AzurePlugin, Type: v.typ, GroupIDs: []int64{gid}, APIKey: v.upstreamKey,
			BaseURL: v.baseURL, Settings: v.settings, ModelMapping: map[string]string{model: v.deployment},
		})
		if got := admin.OK(t, http.MethodGet, fmt.Sprintf("/accounts/%d", v.accountID), nil).Get("credentials.base_url").String(); got != v.saved {
			t.Fatalf("%s: saved base_url %q, want %s", v.typ, got, v.saved)
		}
	}
	e.GrantGroups(admin, tn.User.UserID, append([]int64{tn.GroupID}, groups...))
	for i, v := range variants {
		_, v.apiKey = e.CreateAPIKey(tn.User, groups[i])
	}

	// 3. A request URL pasted as the resource endpoint is refused: a path
	// that is no endpoint by the plugin's pattern, an address with a query
	// by the shared base URL rule before that (CONTRACTS §127.2).
	for pasted, code := range map[string]string{
		"/openai/deployments/gpt-4o": "pattern",
		"/openai/deployments/gpt-4o/chat/completions?api-version=" + azureAPIVersion: "format",
		"/openai/v1/chat/completions": "pattern",
	} {
		r := admin.API(t, http.MethodPost, "/accounts", map[string]any{
			"name": e.Name("acct-azure-bad"), "plugin_key": AzurePlugin, "type": AzureV1Type, "group_ids": groups[:1],
			"priority": 10, "max_concurrency": 5, "schedulable": true,
			"credentials": map[string]any{"api_key": variants[0].upstreamKey, "base_url": e.MockInternalURL + pasted},
		})
		ExpectFieldError(t, r, "credentials.base_url", code)
	}

	// 4. Chat completions through each type, non-stream then stream.
	for _, v := range variants {
		for _, stream := range []bool{false, true} {
			name := fmt.Sprintf("%s stream=%v", v.typ, stream)
			body := ChatBody(model, "hello azure", stream)
			body["service_tier"] = "priority"
			mark := m.Mark(t)
			g := e.Gateway(t, e.BaseURL, "/v1/chat/completions", "", body, map[string]string{
				"Authorization":       "Bearer " + v.apiKey,
				"OpenAI-Organization": "org-e2e-client",
				"OpenAI-Project":      "proj_e2e_client",
			})
			if g.Status != 200 {
				t.Fatalf("%s: HTTP %d %s", name, g.Status, g.Body)
			}
			if !stream && (g.JSON().Get("choices.0.message.content").String() == "" || g.JSON().Get("usage.prompt_tokens").Int() != MockInputTokens) {
				t.Fatalf("%s: %s", name, g.Body)
			}
			if stream && len(g.Events) < 3 {
				t.Fatalf("%s: events = %d %s", name, len(g.Events), g.Body)
			}

			var ours []MockRequest
			for _, r := range m.Since(t, mark) {
				if r.Key() == v.upstreamKey {
					ours = append(ours, r)
				}
			}
			if len(ours) != 1 {
				t.Fatalf("%s: upstream calls with the account's key = %+v, want one", name, ours)
			}
			r := ours[0]
			if r.Path != v.path(v.deployment) || r.Query != v.query || r.Model != v.deployment || r.Stream != stream {
				t.Fatalf("%s: upstream %s?%s model %s stream %v, want %s?%s model %s", name, r.Path, r.Query, r.Model, r.Stream, v.path(v.deployment), v.query, v.deployment)
			}
			// The key travels in api-key only; nothing of the client's own
			// OpenAI account reaches Azure.
			if r.Headers.Get("api-key").String() != v.upstreamKey {
				t.Fatalf("%s: upstream api-key: %s", name, r.Headers.Raw)
			}
			for _, h := range []string{"authorization", "openai-organization", "openai-project"} {
				if r.Headers.Get(h).Exists() {
					t.Fatalf("%s: upstream request carries %s: %s", name, h, r.Headers.Raw)
				}
			}
			if gjson.Get(r.Body, "service_tier").Exists() {
				t.Fatalf("%s: upstream body carries service_tier: %s", name, r.Body)
			}
			// A stream is asked for its usage, or it could not be billed.
			if stream && !gjson.Get(r.Body, "stream_options.include_usage").Bool() {
				t.Fatalf("%s: upstream body lacks stream_options.include_usage=true: %s", name, r.Body)
			}

			u := e.UsageByRequest(admin, tn.User.UserID, g.RequestID)
			if u.Get("account_id").Int() != v.accountID || u.Get("plugin_key").String() != AzurePlugin || u.Get("account_type").String() != v.typ {
				t.Fatalf("%s: usage account, want %d %s/%s: %s", name, v.accountID, AzurePlugin, v.typ, u.Raw)
			}
			if u.Get("platform").String() != "openai" || u.Get("protocol").String() != "openai.chat" || u.Get("upstream_protocol").String() != "openai.chat" ||
				u.Get("stream").Bool() != stream || !u.Get("success").Bool() {
				t.Fatalf("%s: usage endpoint/protocol/stream: %s", name, u.Raw)
			}
			if u.Get("model").String() != model || u.Get("upstream_model").String() != v.deployment {
				t.Fatalf("%s: usage model/upstream_model, want %s/%s: %s", name, model, v.deployment, u.Raw)
			}
			if u.Get("input_tokens").Int() != MockInputTokens || u.Get("output_tokens").Int() != MockOutputTokens || u.Get("cache_read_tokens").Int() != MockCacheReadTokens {
				t.Fatalf("%s: usage tokens: %s", name, u.Raw)
			}
			if u.Get("billing_status").String() != "billed" {
				t.Fatalf("%s: billing_status: %s", name, u.Raw)
			}
			// Inclusive semantics: p = prompt - cached.
			want := ExpectedTokenCost(RunPrice, MockInputTokens-MockCacheReadTokens, MockOutputTokens, MockCacheReadTokens, 0, 0, 1)
			AssertMoney(t, "usage.total_cost ("+name+")", u.Get("total_cost").String(), want)
		}
	}
}
