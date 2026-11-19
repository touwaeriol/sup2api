package e2e

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

// AC 20: OpenAI and Gemini API keys (CONTRACTS 14.1). The built-in plugins
// openai and gemini are installed and enabled at startup (like anthropic)
// and register an "apikey" account type for the built-in openai / gemini
// platforms. A group holding one openai/apikey and one gemini/apikey
// account (base_url = mock-upstream) serves every openai and gemini
// endpoint, streaming and not; usage records carry the right account type,
// protocols and tokens: the openai plugin forces
// stream_options.include_usage so streamed chat completions are billed, and
// Gemini output tokens include the thinking tokens (candidatesTokenCount +
// thoughtsTokenCount). A key whose group only has anthropic accounts gets
// 404 from the openai endpoints: protocol conversion is off until an
// account enables it for the model (CONTRACTS 110).
func TestAC20_OpenAIGeminiAPIKeys(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	m := e.Mock()

	// 1. Built in, enabled, account types registered for their platforms.
	types := admin.OK(t, http.MethodGet, "/account-types", nil).Array()
	platforms := e.Platforms(admin)
	for _, pl := range []struct{ plugin, platform, endpoint, form string }{
		{OpenAIPlugin, "openai", "/v1/chat/completions", "OpenAIKeyForm"},
		{GeminiPlugin, "gemini", "generateContent", "GeminiKeyForm"},
	} {
		d := e.WaitPlugin(admin, pl.plugin, "enabled", "")
		if !d.Get("builtin").Bool() {
			t.Fatalf("%s must be a built-in plugin: %s", pl.plugin, d.Raw)
		}
		at, ok := FindAccountType(types, pl.plugin, APIKeyType)
		if !ok {
			t.Fatalf("%s/%s not offered: %v", pl.plugin, APIKeyType, types)
		}
		if ids := PlatformIDs(at.Get("platforms")); !slices.Equal(ids, []string{pl.platform}) {
			t.Fatalf("%s/%s platforms = %v, want [%s]", pl.plugin, APIKeyType, ids, pl.platform)
		}
		found := false
		for _, ep := range at.Get("endpoints").Array() {
			found = found || ep.Get("native").Bool() && ep.Get("platform").String() == pl.platform && strings.Contains(ep.Get("path").String(), pl.endpoint)
		}
		if !found {
			t.Fatalf("%s/%s lacks native endpoint %s: %s", pl.plugin, APIKeyType, pl.endpoint, at.Get("endpoints").Raw)
		}
		ExpectNativeForm(t, admin.Client, at, pl.form)
		p, _ := Find(platforms, "id", pl.platform)
		if _, ok := PlatformAccountType(p, pl.plugin, APIKeyType); !ok {
			t.Fatalf("/platforms %s lacks %s/%s: %s", pl.platform, pl.plugin, APIKeyType, p.Get("account_types").Raw)
		}
	}

	// 2. A key whose group only has anthropic accounts: openai endpoints
	// exist (built in) but nothing can serve them.
	tn := e.NewTenant(admin, TenantOpts{Balance: "20"})
	mark := m.Mark(t)
	expectNotServed(t, e.OpenAI(tn.APIKey, "/v1/chat/completions", ChatBody(tn.Model, "anthropic only", false)), "openai")
	for _, k := range KeysUsed(m.Since(t, mark), "") {
		if tn.AccountKeys()[k] {
			t.Fatalf("an upstream was called with anthropic account key %s", k)
		}
	}

	// 3. A group with one openai and one gemini account.
	gid := e.CreateGroup(admin, e.Name("grp-oa-gm"), "restricted", 1, nil)
	openaiKey := fmt.Sprintf("sk-openai-mock-%s-%d", e.RunID, nextSeq())
	geminiKey := fmt.Sprintf("AIza-gemini-mock-%s-%d", e.RunID, nextSeq())
	openaiID := e.CreateAccount(admin, AccountSpec{PluginKey: OpenAIPlugin, Type: APIKeyType, GroupIDs: []int64{gid}, APIKey: openaiKey})
	geminiID := e.CreateAccount(admin, AccountSpec{PluginKey: GeminiPlugin, Type: APIKeyType, GroupIDs: []int64{gid}, APIKey: geminiKey})
	defer m.ClearRule(t, openaiKey)
	defer m.ClearRule(t, geminiKey)
	e.GrantGroups(admin, tn.User.UserID, []int64{tn.GroupID, gid})
	keyID, key := e.CreateAPIKey(tn.User, gid)
	if ids := e.GroupPlatforms(admin, gid); !slices.Equal(ids, []string{"gemini", "openai"}) {
		t.Fatalf("group platforms = %v, want [gemini openai]", ids)
	}
	if ids := e.APIKeyPlatforms(tn.User, keyID); !slices.Equal(ids, []string{"gemini", "openai"}) {
		t.Fatalf("api key platforms = %v, want [gemini openai]", ids)
	}
	gptModel := e.RunModelOf(admin, "gpt", "chat")
	embedModel := e.RunModelOf(admin, "gpt", "embed")
	gemModel := e.RunModelOf(admin, "gemini", "flash")

	// upstream returns the single mock request of this group's accounts
	// since mark, checking its path and key.
	upstream := func(mark int64, wantKey, wantPath string) MockRequest {
		t.Helper()
		var ours []MockRequest
		for _, r := range m.Since(t, mark) {
			if r.Key() == openaiKey || r.Key() == geminiKey {
				ours = append(ours, r)
			}
		}
		if len(ours) != 1 || ours[0].Key() != wantKey || ours[0].Path != wantPath {
			t.Fatalf("upstream calls = %+v, want one to %s with %s", ours, wantPath, wantKey)
		}
		return ours[0]
	}
	type wantUsage struct {
		accountID                int64
		plugin, protocol         string
		stream                   bool
		input, output, cacheRead int64
		platform                 string
	}
	checkUsage := func(g *GatewayResult, w wantUsage) {
		t.Helper()
		u := e.UsageByRequest(admin, tn.User.UserID, g.RequestID)
		if u.Get("account_id").Int() != w.accountID || u.Get("plugin_key").String() != w.plugin || u.Get("account_type").String() != APIKeyType {
			t.Fatalf("usage account: want %d %s/%s: %s", w.accountID, w.plugin, APIKeyType, u.Raw)
		}
		if u.Get("platform").String() != w.platform || u.Get("protocol").String() != w.protocol || u.Get("upstream_protocol").String() != w.protocol ||
			u.Get("stream").Bool() != w.stream || !u.Get("success").Bool() {
			t.Fatalf("usage endpoint/protocol/stream: %s", u.Raw)
		}
		if u.Get("input_tokens").Int() != w.input || u.Get("output_tokens").Int() != w.output || u.Get("cache_read_tokens").Int() != w.cacheRead {
			t.Fatalf("usage tokens: want in %d out %d cache_read %d: %s", w.input, w.output, w.cacheRead, u.Raw)
		}
		if u.Get("billing_status").String() != "billed" {
			t.Fatalf("billing_status: %s", u.Raw)
		}
		// Inclusive semantics: p = prompt - cached.
		want := ExpectedTokenCost(RunPrice, w.input-w.cacheRead, w.output, w.cacheRead, 0, 0, 1)
		AssertMoney(t, "usage.total_cost ("+w.protocol+")", u.Get("total_cost").String(), want)
	}

	// 4. OpenAI chat completions, non-stream.
	mark = m.Mark(t)
	g := e.OpenAI(key, "/v1/chat/completions", ChatBody(gptModel, "hello openai", false))
	if g.Status != 200 || g.JSON().Get("choices.0.message.content").String() == "" || g.JSON().Get("usage.prompt_tokens").Int() != MockInputTokens {
		t.Fatalf("chat: HTTP %d %s", g.Status, g.Body)
	}
	r := upstream(mark, openaiKey, "/v1/chat/completions")
	if r.Headers.Get("authorization").String() != "Bearer "+openaiKey || r.Model != gptModel {
		t.Fatalf("chat upstream auth/model: %s %s", r.Headers.Raw, r.Model)
	}
	oa := wantUsage{accountID: openaiID, plugin: OpenAIPlugin, platform: "openai", protocol: "openai.chat",
		input: MockInputTokens, output: MockOutputTokens, cacheRead: MockCacheReadTokens}
	checkUsage(g, oa)

	// 5. Streamed chat completions without stream_options: the plugin asks
	// the upstream for usage, so the stream is billed.
	mark = m.Mark(t)
	g = e.OpenAI(key, "/v1/chat/completions", ChatBody(gptModel, "hello stream", true))
	if g.Status != 200 || len(g.Events) < 3 {
		t.Fatalf("chat stream: HTTP %d %s events=%d", g.Status, g.Body, len(g.Events))
	}
	text := ""
	for _, ev := range g.Events {
		text += ev.Data.Get("choices.0.delta.content").String()
	}
	if text == "" {
		t.Fatalf("chat stream without content: %v", g.Events)
	}
	r = upstream(mark, openaiKey, "/v1/chat/completions")
	if !r.Stream || !gjson.Get(r.Body, "stream_options.include_usage").Bool() {
		t.Fatalf("stream upstream body lacks stream_options.include_usage=true: %s", r.Body)
	}
	oa.stream = true
	checkUsage(g, oa)

	// 6. Responses, non-stream and stream (usage in response.completed).
	mark = m.Mark(t)
	g = e.OpenAI(key, "/v1/responses", map[string]any{"model": gptModel, "input": "hello responses"})
	if g.Status != 200 || g.JSON().Get("usage.output_tokens").Int() != MockOutputTokens {
		t.Fatalf("responses: HTTP %d %s", g.Status, g.Body)
	}
	upstream(mark, openaiKey, "/v1/responses")
	oa.stream, oa.protocol = false, "openai.responses"
	checkUsage(g, oa)
	g = e.OpenAI(key, "/v1/responses", map[string]any{"model": gptModel, "input": "hello responses", "stream": true})
	if g.Status != 200 || len(g.Events) == 0 || g.Events[len(g.Events)-1].Event != "response.completed" {
		t.Fatalf("responses stream: HTTP %d %s %v", g.Status, g.Body, g.EventTypes())
	}
	oa.stream = true
	checkUsage(g, oa)

	// 7. Embeddings: input tokens only.
	mark = m.Mark(t)
	g = e.OpenAI(key, "/v1/embeddings", map[string]any{"model": embedModel, "input": []string{"a", "b"}})
	if g.Status != 200 || len(g.JSON().Get("data").Array()) != 2 {
		t.Fatalf("embeddings: HTTP %d %s", g.Status, g.Body)
	}
	upstream(mark, openaiKey, "/v1/embeddings")
	checkUsage(g, wantUsage{accountID: openaiID, plugin: OpenAIPlugin, platform: "openai", protocol: "openai.embeddings", input: MockInputTokens})

	// 8. Gemini generateContent: model from the path, key in
	// x-goog-api-key; output tokens include the thinking tokens.
	gm := wantUsage{accountID: geminiID, plugin: GeminiPlugin, platform: "gemini", protocol: "gemini.generate",
		input: MockInputTokens, output: MockOutputTokens + MockThoughtsTokens, cacheRead: MockCacheReadTokens}
	mark = m.Mark(t)
	g = e.Gemini(key, gemModel, "generateContent", "", GeminiBody("hello gemini"))
	if g.Status != 200 || g.JSON().Get("candidates.0.content.parts.0.text").String() == "" ||
		g.JSON().Get("usageMetadata.thoughtsTokenCount").Int() != MockThoughtsTokens {
		t.Fatalf("generateContent: HTTP %d %s", g.Status, g.Body)
	}
	r = upstream(mark, geminiKey, "/v1beta/models/"+gemModel+":generateContent")
	if r.Headers.Get("x-goog-api-key").String() != geminiKey || strings.Contains(r.Query, "key=") {
		t.Fatalf("gemini upstream auth: %s query %q", r.Headers.Raw, r.Query)
	}
	checkUsage(g, gm)

	// The client may authenticate with ?key= as well.
	if g := e.Gemini("", gemModel, "generateContent", "key="+key, GeminiBody("query key")); g.Status != 200 {
		t.Fatalf("generateContent with ?key=: HTTP %d %s", g.Status, g.Body)
	}

	// 9. streamGenerateContent?alt=sse: SSE through, upstream always alt=sse.
	mark = m.Mark(t)
	g = e.Gemini(key, gemModel, "streamGenerateContent", "alt=sse", GeminiBody("hello gemini stream"))
	if g.Status != 200 || len(g.Events) < 2 || g.Events[len(g.Events)-1].Data.Get("usageMetadata.thoughtsTokenCount").Int() != MockThoughtsTokens {
		t.Fatalf("streamGenerateContent sse: HTTP %d %s events %d", g.Status, g.Body, len(g.Events))
	}
	r = upstream(mark, geminiKey, "/v1beta/models/"+gemModel+":streamGenerateContent")
	if r.Query != "alt=sse" || !r.Stream {
		t.Fatalf("gemini stream upstream query = %q", r.Query)
	}
	gm.stream, gm.protocol = true, "gemini.stream_generate"
	checkUsage(g, gm)

	// Without alt=sse the client gets Google's JSON array; the upstream call
	// still uses alt=sse (the gateway re-frames).
	mark = m.Mark(t)
	g = e.Gemini(key, gemModel, "streamGenerateContent", "", GeminiBody("hello gemini array"))
	arr := g.JSON()
	n := len(arr.Array())
	if g.Status != 200 || !arr.IsArray() || n < 2 || arr.Get(fmt.Sprintf("%d.usageMetadata.thoughtsTokenCount", n-1)).Int() != MockThoughtsTokens {
		t.Fatalf("streamGenerateContent array: HTTP %d %s", g.Status, g.Body)
	}
	if r := upstream(mark, geminiKey, "/v1beta/models/"+gemModel+":streamGenerateContent"); r.Query != "alt=sse" {
		t.Fatalf("gemini array stream upstream query = %q", r.Query)
	}
	checkUsage(g, gm)

	// 10. countTokens is free.
	mark = m.Mark(t)
	g = e.Gemini(key, gemModel, "countTokens", "", GeminiBody("count me"))
	if g.Status != 200 || g.JSON().Get("totalTokens").Int() <= 0 {
		t.Fatalf("countTokens: HTTP %d %s", g.Status, g.Body)
	}
	upstream(mark, geminiKey, "/v1beta/models/"+gemModel+":countTokens")
	if u := e.UsageByRequest(admin, tn.User.UserID, g.RequestID); u.Get("billing_status").String() != "free" || Money(t, u.Get("total_cost").String()).Sign() != 0 {
		t.Fatalf("countTokens usage: %s", u.Raw)
	}

	// 11. The anthropic endpoint is not served by this group.
	expectNotServed(t, e.Messages(key, MessagesBody(tn.Model, "no anthropic here", false), nil), "anthropic")

	// 12. Upstream errors in each API's format: an OpenAI 400 is returned
	// as-is; a Gemini 429 cools the account down and, with no other
	// account, reaches the client as RESOURCE_EXHAUSTED.
	m.SetRule(t, MockRule{APIKey: openaiKey, Status: 400, Remaining: 1})
	g = e.OpenAI(key, "/v1/chat/completions", ChatBody(gptModel, "bad request", false))
	if g.Status != 400 || g.JSON().Get("error.type").String() != "invalid_request_error" || g.JSON().Get("type").Exists() {
		t.Fatalf("openai 400: HTTP %d %s", g.Status, g.Body)
	}
	m.SetRule(t, MockRule{APIKey: geminiKey, Status: 429, Remaining: 1})
	g = e.Gemini(key, gemModel, "generateContent", "", GeminiBody("rate limited"))
	if g.Status != 429 || g.JSON().Get("error.status").String() != "RESOURCE_EXHAUSTED" {
		t.Fatalf("gemini 429: HTTP %d %s", g.Status, g.Body)
	}
	if a := admin.OK(t, http.MethodGet, fmt.Sprintf("/accounts/%d", geminiID), nil); a.Get("cooldown_until").String() == "" {
		t.Fatalf("gemini account not cooling down after 429: %s", a.Raw)
	}
	Eventually(t, 15*time.Second, 500*time.Millisecond, "gemini cooldown over", func() bool {
		return admin.OK(t, http.MethodGet, fmt.Sprintf("/accounts/%d", geminiID), nil).Get("cooldown_until").String() == ""
	})
}

// AC 20 (broadcast): guard rules changed through one node are enforced by
// the other node at once (app.broadcast.v1 "rules.changed", CONTRACTS
// 14.3), well before guard's 5 s polling fallback.
func TestAC20_GuardRulesBroadcast(t *testing.T) {
	e := Setup(t)
	if len(e.NodeURLs) < 2 {
		t.Skip("needs two node URLs (E2E_NODE_URLS)")
	}
	admin := e.Admin()
	tn := e.NewTenant(admin, TenantOpts{})
	e.EnsurePlugin(admin, "guard", "")
	e.SetGuardRules(admin)
	defer e.SetGuardRules(admin)

	word := "E2E-BROADCAST-" + e.RunID
	putRules := func(node int, rules []GuardRule) {
		t.Helper()
		if rules == nil {
			rules = []GuardRule{}
		}
		r := e.PluginRoute(admin, e.NodeURLs[node], http.MethodPut, "guard", "/rules", map[string]any{"rules": rules})
		if r.Status != 200 {
			t.Fatalf("PUT /rules on node %d: %s", node+1, r)
		}
	}
	// within polls node's gateway until the forbidden prompt gets want, and
	// requires it within 2 s of the rule change.
	within := func(node int, want int) {
		t.Helper()
		start := time.Now()
		for {
			g, err := GatewayTry(e.NodeURLs[node], "/v1/messages", tn.APIKey, MessagesBody(tn.Model, "say "+word, false), nil)
			if err != nil {
				t.Fatal(err)
			}
			if g.Status == want {
				t.Logf("node %d answered %d after %s", node+1, want, time.Since(start).Round(time.Millisecond))
				return
			}
			if time.Since(start) > 2*time.Second {
				t.Fatalf("node %d still answers %d (%s) %s after the rule change on the other node; want %d within 2 s", node+1, g.Status, g.Body, time.Since(start).Round(time.Millisecond), want)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	// Baseline: allowed on node 2.
	within(1, http.StatusOK)
	// Add the rule on node 1: node 2 blocks within 2 s.
	putRules(0, []GuardRule{{Name: "e2e-broadcast-" + e.RunID, Kind: "keyword", Pattern: word, Enabled: true}})
	within(1, http.StatusForbidden)
	// Remove it on node 2: node 1 allows again within 2 s.
	putRules(1, nil)
	within(0, http.StatusOK)
}
