package e2e

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
)

// Ark asset library credentials of the AC43 account (format only).
const (
	ac43AccessKey = "AKLTe2e00000000000000000"
	ac43SecretKey = "ZTJlLXNlY3JldC1rZXktZm9yLWFjNDM="
)

// AC 43: a plugin receives the accounts of its own account types without a
// grant (CONTRACTS §124). A volcengine relay account behind the mock's
// proxy: the asset library route reads it with HostService.GetAccount, which
// writes a plugin.account.read audit row naming no value, and the asset
// request the plugin describes is sent by the core through the account's
// proxy. A package still asking for accounts.credentials or accounts.read is
// refused at upload with the outdated-contract message.
func TestAC43_PluginAccountContext(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	m := e.Mock()

	t.Run("asset library reads the account and sends through its proxy", func(t *testing.T) {
		e := e.With(t)
		e.RequireDocker()
		proxyID := createMockProxy(t, e, admin)
		t.Cleanup(func() { admin.API(t, http.MethodDelete, fmt.Sprintf("/proxies/%d", proxyID), nil) })
		id := e.CreateAccount(admin, AccountSpec{PluginKey: "volcengine", Type: "relay", APIKey: e.Name("ark"),
			Settings: map[string]any{"access_key": ac43AccessKey, "secret_key": ac43SecretKey, "asset_endpoint": "/__ark/asset"},
			Extra:    map[string]any{"proxy_id": proxyID}})
		t.Cleanup(func() { admin.API(t, http.MethodDelete, fmt.Sprintf("/accounts/%d", id), nil) })

		mark := m.Mark(t)
		r := admin.API(t, http.MethodGet, fmt.Sprintf("/p/volcengine/accounts/%d/upstream/asset-groups", id), nil)
		if r.Status != http.StatusOK {
			t.Fatalf("upstream asset groups: %s", r)
		}
		var asset []MockRequest
		for _, x := range m.Since(t, mark) {
			if x.Path == "/__ark/asset" {
				asset = append(asset, x)
			}
		}
		if len(asset) != 1 || !asset[0].Proxied || !strings.Contains(asset[0].Query, "Action=ListAssetGroups") {
			t.Fatalf("asset library requests through the proxy: %+v", asset)
		}

		rows := e.SQL(fmt.Sprintf(`SELECT user_id IS NULL, detail::text FROM audit_logs WHERE action = 'plugin.account.read' AND target_type = 'account' AND target_id = %s ORDER BY id`, sqlText(itoa(id))))
		if len(rows) != 1 {
			t.Fatalf("plugin.account.read rows = %v, want one", rows)
		}
		for _, want := range []string{`"plugin_key": "volcengine"`, `"account_type": "relay"`, `"via": "host.GetAccount"`} {
			if !strings.Contains(rows[0], want) {
				t.Fatalf("audit row %q lacks %s", rows[0], want)
			}
		}
		for _, secret := range []string{ac43AccessKey, ac43SecretKey, "proxy"} {
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
			if want := protocol.OutdatedContractMessage(dp.Key, version, hostVersion); r.Status != http.StatusBadRequest || r.JSON().Get("error.message").String() != want {
				t.Fatalf("upload with %v: %s\nwant 400 with %q", grant["id"], r, want)
			}
		}
	})
}
