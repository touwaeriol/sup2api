package e2e

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
)

// AC 43: a plugin receives the accounts of its own account types without a
// grant (CONTRACTS §124). A codex account behind the mock's proxy: the reset
// credit list route reads it with HostService.GetAccount, which writes a
// plugin.account.read audit row naming no value, and the Codex backend
// request the plugin describes is sent by the core through the account's
// proxy. A package still asking for accounts.credentials or accounts.read is
// refused at upload with the outdated-contract message.
func TestAC43_PluginAccountContext(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	m := e.Mock()

	t.Run("reset credits read the account and send through its proxy", func(t *testing.T) {
		e := e.With(t)
		e.RequireDocker()
		e.EnsurePlugin(admin, "codex", "")
		proxyID := createMockProxy(t, e, admin)
		t.Cleanup(func() { admin.API(t, http.MethodDelete, fmt.Sprintf("/proxies/%d", proxyID), nil) })
		token := e.Name("codex-token")
		id := e.CreateAccount(admin, AccountSpec{PluginKey: "codex", Type: "codex_oauth", Credentials: codexCreds(token),
			Extra: map[string]any{"proxy_id": proxyID}})
		t.Cleanup(func() { admin.API(t, http.MethodDelete, fmt.Sprintf("/accounts/%d", id), nil) })

		mark := m.Mark(t)
		r := admin.API(t, http.MethodPost, "/p/codex/reset-credits/list", map[string]any{"account_id": id})
		if r.Status != http.StatusOK {
			t.Fatalf("reset credits: %s", r)
		}
		calls := callsTo(t, m, mark, "/backend-api/wham/rate-limit-reset-credits")
		if len(calls) != 1 || !calls[0].Proxied || calls[0].Host != "chatgpt.com" || calls[0].APIKey != token {
			t.Fatalf("Codex backend requests through the proxy: %+v", calls)
		}

		rows := e.SQL(fmt.Sprintf(`SELECT user_id IS NULL, detail::text FROM audit_logs WHERE action = 'plugin.account.read' AND target_type = 'account' AND target_id = %s ORDER BY id`, sqlText(itoa(id))))
		if len(rows) != 1 {
			t.Fatalf("plugin.account.read rows = %v, want one", rows)
		}
		for _, want := range []string{`"plugin_key": "codex"`, `"account_type": "codex_oauth"`, `"via": "host.GetAccount"`} {
			if !strings.Contains(rows[0], want) {
				t.Fatalf("audit row %q lacks %s", rows[0], want)
			}
		}
		for _, secret := range []string{token, "proxy"} {
			if strings.Contains(rows[0], secret) {
				t.Fatalf("audit row %q names a value (%s)", rows[0], secret)
			}
		}
	})

	t.Run("account grants refused at upload", func(t *testing.T) {
		hostVersion := e.NodeHealth(1).Get("version").String()
		for _, grant := range []map[string]any{
			{"id": "accounts.credentials", "scope": map[string]any{"types": "own"}},
			{"id": "accounts.read"},
		} {
			e := e.With(t)
			dp := e.NewDerivedPlugin(admin, "e2eacct")
			pkg := dp.Package(e, func(m map[string]any) {
				hp, _ := m["hostPermissions"].([]any)
				m["hostPermissions"] = append(hp, grant)
			})
			version := fmt.Sprint(pkg.Manifest(t)["version"])
			r := e.UploadPlugin(admin, dp.Key+".s2plugin", pkg.Bytes(t))
			if want := protocol.OutdatedContractMessage(manifest.DisplayID(dp.ID), version, hostVersion); r.Status != http.StatusBadRequest || r.JSON().Get("error.message").String() != want {
				t.Fatalf("upload with %v: %s\nwant 400 with %q", grant["id"], r, want)
			}
		}
	})
}
