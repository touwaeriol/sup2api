package e2e

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

// AC 37: plugin network access (CONTRACTS §113). The administrator's
// blocked list (allow all hosts, the default) and allowed list (block all
// hosts) decide which hosts guard's webhook reaches through the egress
// tunnel; a refused connection is in the plugin's egress log as denied with
// the list as the reason. The setting is the default again afterwards. The
// setting is saved on node 1 and the alerts are raised there: the node that
// saves it drops its cached copy before answering, the other one on a
// broadcast a moment later.
func TestAC37_PluginNetworkAccess(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	tn := e.NewTenant(admin, TenantOpts{})
	e.EnsurePlugin(admin, "guard", "")

	host := e.mockHost()
	hookPath := "/__webhook/guard-netaccess-" + e.RunID
	node, setAccess := e.nodeOneNetworkAccess(admin)
	t.Cleanup(func() {
		e.SetGuardRules(admin)
		e.SetGuardSettings(admin, e.MockInternalURL+hookPath) // the default alert cooldown again
	})
	// Every blocked request alerts: the default cooldown (60 s, used up by a
	// refused alert too) would hold back the alerts of the next phase.
	admin.OK(t, http.MethodPut, "/plugins/guard/settings", map[string]any{
		"values": map[string]any{"webhook_url": e.MockInternalURL + hookPath, "record_snippets": false, "alert_cooldown_sec": 0},
	})
	e.SetGuardRules(admin, e.ForbiddenRule())

	m := e.Mock()
	// trigger has guard block a request, which sends the webhook alert.
	trigger := func() {
		t.Helper()
		if g := e.Gateway(t, node.Base, "/v1/messages", tn.APIKey, MessagesBody(tn.Model, "trigger "+e.ForbiddenWord(), false), nil); g.Status != 403 {
			t.Fatalf("rule hit: HTTP %d", g.Status)
		}
	}
	delivered := func(mark int64) bool {
		for _, r := range m.Since(t, mark) {
			if strings.HasPrefix(r.Path, hookPath) {
				return true
			}
		}
		return false
	}
	// rows lists guard's egress log rows for the mock's host since from.
	rows := func(from time.Time) []gjson.Result {
		d := admin.OK(t, http.MethodGet, "/plugins/guard/egress", nil, Query("from", from.UTC().Format(time.RFC3339),
			"to", time.Now().Add(time.Minute).UTC().Format(time.RFC3339), "page_size", "200"))
		var out []gjson.Result
		for _, r := range d.Get("items").Array() {
			if r.Get("host").String() == host {
				out = append(out, r)
			}
		}
		return out
	}
	// allowed: the alert reaches the mock.
	allowed := func(what string) {
		t.Helper()
		mark := m.Mark(t)
		Eventually(t, 30*time.Second, 2*time.Second, what+": webhook delivered", func() bool {
			trigger()
			return delivered(mark)
		})
	}
	// denied: the alert is refused with reason and never reaches the mock.
	denied := func(what, reason string) {
		t.Helper()
		mark, since := m.Mark(t), time.Now().Add(-time.Second)
		Eventually(t, 30*time.Second, 2*time.Second, what+": denied in the egress log", func() bool {
			trigger()
			for _, r := range rows(since) {
				if r.Get("result").String() == "denied" && strings.Contains(r.Get("error").String(), reason) {
					return true
				}
			}
			return false
		})
		if delivered(mark) {
			t.Fatalf("%s: the webhook reached the mock", what)
		}
	}

	allowed("default setting")
	// The setting is checked on new connections: wait for guard's
	// keep-alive connection to the mock to close (its log row leaves
	// "open"), so the next alert dials again. Idle connections close after
	// 90 s.
	Eventually(t, 150*time.Second, 3*time.Second, "guard's connections to "+host+" closed", func() bool {
		closed := false
		for _, r := range rows(time.Now().Add(-time.Hour)) {
			if r.Get("network").String() == "dns" {
				continue
			}
			if r.Get("result").String() == "open" {
				return false
			}
			closed = true
		}
		return closed
	})

	setAccess(map[string]any{"blocked_domains": []string{host}})
	denied("blocked list", host+" is on the blocked list of plugin network access")

	setAccess(map[string]any{"mode": "block_all", "allowed_domains": []string{}})
	denied("block all, not allowed", host+" is not on the allowed list of plugin network access")

	setAccess(map[string]any{"allowed_domains": []string{host}})
	allowed("block all, allowed")
}

// AC 37 (continued): plugin network access also rules the requests the core
// sends for a platform plugin (CONTRACTS §113). With the mock's host - the
// account's own upstream address - on the blocked list, or missing from the
// allowed list, a request through the gateway never reaches the mock: the
// attempt is refused before anything is sent, the client gets the answer of
// a request no account could serve (without the host), and the refusal is
// a denied row of the anthropic plugin's egress log with the list as the
// reason. Taken off the list, requests reach the mock again. Saving and
// sending both go to node 1 (the other node learns of a save from a
// broadcast a moment later); the setting is the default again afterwards.
func TestAC37_PluginNetworkAccessCoversUpstreamRequests(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	tn := e.NewTenant(admin, TenantOpts{})
	host := e.mockHost()
	node, setAccess := e.nodeOneNetworkAccess(admin)

	m := e.Mock()
	key := tn.Accounts[0].Key
	reached := func(mark int64) bool {
		for _, r := range m.Since(t, mark) {
			if r.APIKey == key {
				return true
			}
		}
		return false
	}
	send := func() *GatewayResult {
		t.Helper()
		return e.Gateway(t, node.Base, "/v1/messages", tn.APIKey, MessagesBody(tn.Model, "network access "+e.RunID, false), nil)
	}
	served := func(what string) {
		t.Helper()
		mark := m.Mark(t)
		if g := send(); g.Status != 200 || !reached(mark) {
			t.Fatalf("%s: HTTP %d %s, reached the mock %v", what, g.Status, g.Body, reached(mark))
		}
	}
	refused := func(what, reason string) {
		t.Helper()
		mark, since := m.Mark(t), time.Now().Add(-time.Second)
		g := send()
		if g.Status < 500 || strings.Contains(string(g.Body), host) {
			t.Fatalf("%s: HTTP %d %s", what, g.Status, g.Body)
		}
		if reached(mark) {
			t.Fatalf("%s: the request reached the mock", what)
		}
		Eventually(t, 30*time.Second, time.Second, what+": denied in the anthropic egress log", func() bool {
			return e.deniedInEgressLog(admin, AnthropicPlugin, host, reason, since)
		})
	}

	served("default setting")
	setAccess(map[string]any{"blocked_domains": []string{host}})
	refused("blocked list", host+" is on the blocked list of plugin network access")
	setAccess(map[string]any{"blocked_domains": []string{}})
	served("off the blocked list")
	setAccess(map[string]any{"mode": "block_all", "allowed_domains": []string{"api.anthropic.example"}})
	refused("block all, the account's address not allowed", host+" is not on the allowed list of plugin network access")
	setAccess(map[string]any{"allowed_domains": []string{host}})
	served("block all, allowed")
}
