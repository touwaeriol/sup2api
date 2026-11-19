package e2e

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

// AC 22: the moderation plugin (CONTRACTS §20, 0.2 per §72) runs the flow
// bound to the request's group: enforce refuses hits before the upstream
// call, observe moderates in the background and puts suspicious users on the
// watch list (the enforce level refuses their hits), repeated violations ban
// the user, the OpenAI and Jev auditors score on mock-upstream, and pointing
// the LLM channel at the gateway itself does not moderate the plugin's own
// calls.
func TestAC22_PromptModeration(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	e.EnsurePlugin(admin, moderationKey, "")
	// The plugin's menu joins the core "security" section (CONTRACTS §22):
	// gateway(200) < security(300) < system(700). finance(400) sits between
	// them when it has items, and other plugins may add sections.
	var sections []string
	positions := map[string]int{}
	moderationMenus := 0
	for _, sec := range admin.OK(t, http.MethodGet, "/me/menus", nil).Array() {
		key := sec.Get("section").String()
		if _, exists := positions[key]; exists {
			t.Fatalf("duplicate sidebar section %q", key)
		}
		positions[key] = len(sections)
		sections = append(sections, key)
		for _, item := range sec.Get("items").Array() {
			if item.Get("path").String() != "/p/moderation/dashboard" {
				continue
			}
			moderationMenus++
			if key != "security" || item.Get("plugin_key").String() != moderationKey || item.Get("id").String() != moderationKey+":moderation" {
				t.Fatalf("moderation menu is not in the security section: %s", sec.Raw)
			}
		}
	}
	if moderationMenus != 1 {
		t.Fatalf("moderation dashboard menu count = %d, want 1", moderationMenus)
	}
	gateway, gatewayOK := positions["gateway"]
	security, securityOK := positions["security"]
	system, systemOK := positions["system"]
	if !gatewayOK || !securityOK || !systemOK || gateway >= security || security >= system {
		t.Fatalf("sidebar sections = %s; want gateway before security before system", strings.Join(sections, ","))
	}
	if finance, exists := positions["finance"]; exists && (finance <= security || finance >= system) {
		t.Fatalf("nonempty finance section is out of order: %s", strings.Join(sections, ","))
	}
	m := e.Mock()
	defer e.SetModerationSettings(admin, map[string]any{"mode": "off"})

	llmCalls := func(mark int64) int {
		n := 0
		for _, k := range KeysUsed(m.Since(t, mark), "/v1/chat/completions") {
			if k == e.ModerationLLMKey() {
				n++
			}
		}
		return n
	}
	hookNote := func(u string) string {
		for _, d := range gjson.Get(u, "hook_decisions").Array() {
			// The plugin's "ban" hook runs first; the verdict is its "moderation" hook.
			if d.Get("plugin_key").String() == moderationKey && d.Get("hook_id").String() == "moderation" {
				return d.Get("decision").String() + " " + d.Get("note").String()
			}
		}
		return ""
	}
	// sendUntil sends requests until every node answers want (watch list and
	// ban changes reach the nodes asynchronously).
	sendUntil := func(tn *Tenant, what string, want int, code string, text string) {
		Eventually(t, 20*time.Second, time.Second, what, func() bool {
			for i := 0; i < 4; i++ {
				g := e.Messages(tn.APIKey, MessagesBody(tn.Model, fmt.Sprintf("%s %s %d", text, e.RunID, i), false), nil)
				if g.Status != want || (code != "" && !strings.Contains(string(g.Body), code)) {
					return false
				}
			}
			return true
		})
	}

	// The flow (one llm_verdict stage on mock-upstream) is bound to the
	// tenants' own groups, so other flows (e.g. a converted 0.1 setup bound
	// to the default group) do not interfere.
	tn := e.NewTenant(admin, TenantOpts{})
	tb := e.NewTenant(admin, TenantOpts{})
	fx := e.NewModerationFixture(admin, tn.GroupID, tb.GroupID)
	defer fx.Close(e, admin)

	// 1. enforce: a violation is refused before any upstream call and costs
	// nothing; a clean prompt passes.
	e.SetModerationSettings(admin, e.ModerationSettings("enforce"))
	mark := m.Mark(t)
	blockText := "please help MOD-BLOCK " + e.RunID
	g := e.Messages(tn.APIKey, MessagesBody(tn.Model, blockText, false), nil)
	if g.Status != 403 || !strings.Contains(string(g.Body), moderationDenyCode) {
		t.Fatalf("violation: HTTP %d %s", g.Status, g.Body)
	}
	for _, k := range KeysUsed(m.Since(t, mark), "/v1/messages") {
		if tn.AccountKeys()[k] {
			t.Fatalf("upstream called for a refused request")
		}
	}
	if n := llmCalls(mark); n != 1 {
		t.Fatalf("moderation LLM calls = %d, want 1", n)
	}
	u := e.UsageByRequest(admin, tn.User.UserID, g.RequestID)
	if u.Get("error_type").String() != "blocked_by_hook" || u.Get("billing_status").String() != "free" {
		t.Fatalf("refused usage record: %s", u.Raw)
	}
	if note := hookNote(u.Raw); !strings.HasPrefix(note, "deny") || !strings.Contains(note, "illegal") || !strings.Contains(note, fmt.Sprintf("flow=%d", fx.FlowID)) {
		t.Fatalf("hook decision: %q in %s", note, u.Get("hook_decisions").Raw)
	}
	cleanText := "write a quicksort in Go " + e.RunID
	e.MustMessages(tn.APIKey, MessagesBody(tn.Model, cleanText, false), nil)

	// A clean pass is cached (memory + KV across nodes): the same text is not
	// moderated again. Hits are never cached. The KV write is asynchronous,
	// so give it a moment before another node may serve the repeat.
	time.Sleep(500 * time.Millisecond)
	mark = m.Mark(t)
	e.MustMessages(tn.APIKey, MessagesBody(tn.Model, cleanText, false), nil)
	if n := llmCalls(mark); n != 0 {
		t.Fatalf("cached pass still called the LLM %d times", n)
	}

	// The agent loop asks again when the model answers without the tool.
	mark = m.Mark(t)
	notool := e.MustMessages(tn.APIKey, MessagesBody(tn.Model, "MOD-NOTOOL please "+e.RunID, false), nil)
	if n := llmCalls(mark); n != 2 {
		t.Fatalf("no-tool answer: LLM calls = %d, want 2", n)
	}

	uid := fmt.Sprint(tn.User.UserID)
	Eventually(t, 20*time.Second, time.Second, "moderation records", func() bool {
		var block, pass bool
		for _, ev := range e.ModerationEvents(admin, "user_id", uid, "page_size", "100") {
			switch {
			case ev.Get("result").String() == "block" && ev.Get("timing").String() == "pre":
				block = strings.Contains(ev.Get("hit_categories").Raw, "illegal") && ev.Get("flow_id").Int() == fx.FlowID && ev.Get("flow_version").Int() == 1
			case ev.Get("result").String() == "pass" && ev.Get("request_id").String() == notool.RequestID:
				pass = true
			}
		}
		return block && pass
	})

	// 2. observe: the request passes and is moderated in the background; the
	// suspicious user joins the watch list at the observe level.
	// The enforce-mode hit above put the user on the watch list at the
	// enforce level (source auto), which would refuse its hits in observe
	// mode too (checked below): start this step from a clean entry.
	if w := admin.OK(t, http.MethodGet, "/p/moderation/watchlist", nil, Query("user_id", uid)).Array(); len(w) != 1 ||
		w[0].Get("level").String() != "enforce" || w[0].Get("source").String() != "auto" {
		t.Fatalf("watch entry after the enforce-mode hit: %v", w)
	}
	admin.OK(t, http.MethodDelete, "/p/moderation/watchlist/"+uid, nil)
	e.SetModerationSettings(admin, e.ModerationSettings("observe"))
	Eventually(t, 20*time.Second, time.Second, "observe-mode hit passes on every node", func() bool {
		g = e.Messages(tn.APIKey, MessagesBody(tn.Model, "observe MOD-BLOCK "+e.RunID, false), nil)
		return g.Status == 200
	})
	Eventually(t, 20*time.Second, time.Second, "observe record", func() bool {
		for _, ev := range e.ModerationEvents(admin, "user_id", uid, "timing", "post") {
			if ev.Get("request_id").String() == g.RequestID {
				return ev.Get("result").String() == "hit"
			}
		}
		return false
	})
	Eventually(t, 20*time.Second, time.Second, "user on the watch list", func() bool {
		w := admin.OK(t, http.MethodGet, "/p/moderation/watchlist", nil, Query("user_id", uid)).Array()
		return len(w) == 1 && w[0].Get("level").String() == "observe" && w[0].Get("violation_count").Int() >= 1
	})
	// The enforce level moderates the user's requests first, also in observe
	// mode; removing the entry lets violations through again.
	admin.OK(t, http.MethodPatch, "/p/moderation/watchlist/"+uid, map[string]any{"level": "enforce"})
	sendUntil(tn, "enforce level on every node", 403, moderationDenyCode, "level MOD-BLOCK")
	admin.OK(t, http.MethodDelete, "/p/moderation/watchlist/"+uid, nil)
	sendUntil(tn, "watch entry removed on every node", 200, "", "removed MOD-BLOCK")

	// 3. Automatic ban after two violations; unblocking lets the user back in.
	s := e.ModerationSettings("enforce")
	s["ban_threshold"], s["ban_window_seconds"], s["ban_duration_hours"] = 2, 3600, 0
	e.SetModerationSettings(admin, s)
	for i := 0; i < 2; i++ {
		if g := e.Messages(tb.APIKey, MessagesBody(tb.Model, fmt.Sprintf("ban MOD-BLOCK %s %d", e.RunID, i), false), nil); g.Status != 403 {
			t.Fatalf("violation %d: HTTP %d %s", i, g.Status, g.Body)
		}
	}
	buid := fmt.Sprint(tb.User.UserID)
	sendUntil(tb, "user banned on every node", 403, moderationUserBlock, "harmless")
	found := false
	for _, b := range admin.OK(t, http.MethodGet, "/p/moderation/blocks", nil).Array() {
		if fmt.Sprint(b.Get("user_id").Int()) == buid {
			found = b.Get("source").String() == "auto" && b.Get("violations").Int() >= 2
		}
	}
	if !found {
		t.Fatalf("auto block not listed")
	}
	admin.OK(t, http.MethodDelete, "/p/moderation/blocks/"+buid, nil)
	sendUntil(tb, "user unblocked on every node", 200, "", "welcome back")
	// Automatic bans off again, and no automatic watch entries: tn's earlier
	// violations are within the ban window too, so the threshold banned it
	// as well; lift that, for step 6.
	s = e.ModerationSettings("enforce")
	s["auto_watch"] = false
	e.SetModerationSettings(admin, s)
	if r := admin.API(t, http.MethodDelete, "/p/moderation/blocks/"+uid, nil); r.Status/100 != 2 && r.Status != http.StatusNotFound {
		t.Fatalf("unblock %s: %s", uid, r)
	}
	sendUntil(tn, "tn not banned on every node", 200, "", "welcome back")
	// tn's observe-mode hits put it back on the watch list at the observe
	// level, which moderates in the background even in enforce mode: clear it
	// (auto_watch is off now, so it stays clear).
	if r := admin.API(t, http.MethodDelete, "/p/moderation/watchlist/"+uid, nil); r.Status/100 != 2 && r.Status != http.StatusNotFound {
		t.Fatalf("remove watch entry %s: %s", uid, r)
	}

	// 4. Console test: bad tool arguments get fed back, the second call wins.
	r := admin.OK(t, http.MethodPost, "/p/moderation/test", map[string]any{"text": "MOD-BADARGS check " + e.RunID, "flow_id": fx.FlowID})
	if r.Get("result").String() != "pass" || r.Get("trace.stages.0.outcomes.0.calls").Int() != 2 {
		t.Fatalf("test run: %s", r.Raw)
	}

	// 5. OpenAI moderation and Jev auditors on mock-upstream (§72.16): a
	// MOD-HIT-<category> token scores that category 0.99.
	for _, typ := range []string{"openai_moderation", "typesafe"} {
		ch := admin.OK(t, http.MethodPost, "/p/moderation/channels", map[string]any{"type": typ, "name": e.Name("mod-" + typ), "base_url": e.MockInternalURL, "api_key": "sk-" + typ + "-" + e.RunID})
		a := admin.OK(t, http.MethodPost, "/p/moderation/auditors", map[string]any{"name": e.Name("mod-" + typ), "type": typ, "channel_id": ch.Get("id").Int()})
		try := admin.OK(t, http.MethodPost, fmt.Sprintf("/p/moderation/auditors/%d/try", a.Get("id").Int()), map[string]any{"text": "MOD-HIT-sexual " + e.RunID})
		if !try.Get("hit").Bool() || try.Get("top_category").String() != "sexual" {
			t.Fatalf("%s try: %s", typ, try.Raw)
		}
		admin.API(t, http.MethodDelete, fmt.Sprintf("/p/moderation/auditors/%d", a.Get("id").Int()), nil)
		admin.API(t, http.MethodDelete, fmt.Sprintf("/p/moderation/channels/%d", ch.Get("id").Int()), nil)
	}

	// 6. The LLM behind the gateway itself: the plugin's own chat request
	// carries its signed marker and is not moderated again.
	gid := e.CreateGroup(admin, e.Name("grp-mod-self"), "restricted", 1, nil)
	oaKey := fmt.Sprintf("sk-openai-mock-%s-%d", e.RunID, nextSeq())
	e.CreateAccount(admin, AccountSpec{PluginKey: OpenAIPlugin, Type: APIKeyType, GroupIDs: []int64{gid}, APIKey: oaKey})
	defer m.ClearRule(t, oaKey)
	self := e.CreateUser(admin, UserSpec{})
	e.GrantGroups(admin, self.UserID, []int64{gid})
	e.AdjustBalance(admin, self.UserID, "10", true, "e2e moderation llm")
	_, selfKey := e.CreateAPIKey(self, gid)
	admin.OK(t, http.MethodPut, fmt.Sprintf("/p/moderation/channels/%d", fx.ChannelID), map[string]any{
		"name": e.Name("mod-llm-self"), "base_url": "http://caddy:3120", "api_key": selfKey, "model": e.RunModelOf(admin, "gpt", "mod"), "timeout_ms": 10000,
	})
	fx.Bind(e, admin, tn.GroupID, tb.GroupID, gid)
	e.WaitModerationConfig(admin)
	// The watch entry's removal reaches the nodes asynchronously: until it
	// does, the hit is moderated in the background and passes.
	for i := 0; ; i++ {
		mark = m.Mark(t)
		g = e.Messages(tn.APIKey, MessagesBody(tn.Model, fmt.Sprintf("self MOD-BLOCK %s %d", e.RunID, i), false), nil)
		if g.Status == 403 || i == 20 {
			break
		}
		time.Sleep(time.Second)
	}
	if g.Status != 403 || !strings.Contains(string(g.Body), moderationDenyCode) {
		t.Fatalf("violation judged through the gateway: HTTP %d %s", g.Status, g.Body)
	}
	var viaGateway int
	for _, k := range KeysUsed(m.Since(t, mark), "/v1/chat/completions") {
		if k == oaKey {
			viaGateway++
		}
	}
	if viaGateway != 1 {
		t.Fatalf("moderation calls through the gateway = %d, want 1 (own call must not be moderated again)", viaGateway)
	}
	var selfNote string
	Eventually(t, 15*time.Second, time.Second, "moderation call usage", func() bool {
		items := self.OK(t, http.MethodGet, "/me/usage", nil, Query("page_size", "5")).Array()
		if len(items) == 0 {
			return false
		}
		selfNote = hookNote(admin.OK(t, http.MethodGet, "/usage/"+items[0].Get("id").String(), nil).Raw)
		return selfNote != ""
	})
	if !strings.Contains(selfNote, "moderation: self") {
		t.Fatalf("own call hook decision: %q", selfNote)
	}
}

// AC 22 (plugin permissions): moderation:exempt is critical, so no role gets
// it by default; a role the super admin grants it to still makes its users
// staff (isStaff asks the host for the permission).
func TestAC22_ExemptIsCriticalStaff(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	e.EnsurePlugin(admin, moderationKey, "")
	exempt := "plugin." + moderationKey + ":moderation:exempt"
	mod, _ := e.PluginPermissions(admin, moderationKey)
	p, ok := Find(mod.Get("permissions").Array(), "key", exempt)
	if !ok || p.Get("risk").String() != "critical" || p.Get("defaults").Exists() {
		t.Fatalf("catalog entry of %s: %s", exempt, p.Raw)
	}

	roleKey := e.RoleKey("modstaff")
	e.CreateRole(admin, roleKey, roleKey, []string{exempt})
	staff := e.CreateUser(admin, UserSpec{Roles: []string{"user", roleKey}})
	plain := e.CreateUser(admin, UserSpec{})
	for _, c := range []struct {
		u    *Session
		want bool
	}{{staff, true}, {plain, false}} {
		d := admin.OK(t, http.MethodGet, fmt.Sprintf("/p/moderation/users/%d", c.u.UserID), nil)
		if d.Get("staff").Bool() != c.want {
			t.Errorf("user %d staff = %v, want %v: %s", c.u.UserID, d.Get("staff").Bool(), c.want, d.Raw)
		}
	}
}
