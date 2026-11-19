package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

// AC 39: account balances (CONTRACTS §52) of the two upstream site plugins
// (§119). The plugin describes the requests, the core sends them to the
// account's own address (through the account's proxy, past the plugin
// network access setting), the plugin reads the answers:
//   - a refresh (GET /accounts/:id/balance?force=true) shows the number
//     and unit the site answers, unconverted - New API's quota points from
//     /api/usage/token/ (no unit; /api/status is not asked), Sub2API's
//     /v1/usage amount and unit - or no limit, and never a source field;
//   - a new account nobody refreshes gets its balance from the cluster task
//     core.account.balance_sweep (every minute; an account never read is
//     due at once);
//   - a site refusing the key is a failed read (auth_rejected) that leaves
//     the account active and schedulable;
//   - with the mock's host on the plugin network access blocked list the
//     read fails before anything is sent, a denied row in the plugin's
//     egress log.
func TestAC39_AccountBalance(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	m := e.Mock()
	e.EnsurePlugin(admin, NewAPIPlugin, "")
	e.EnsurePlugin(admin, Sub2APIPlugin, "")

	// account creates an account of plugin on a new upstream key, with
	// the mock's rule for the key set first (nil = the default answers):
	// the sweep may read a new account at once.
	account := func(t *testing.T, plugin string, rule *MockRule, extra map[string]any) (int64, string) {
		t.Helper()
		e := e.With(t)
		k := fmt.Sprintf("sk-bal-%s-%d", e.RunID, nextSeq())
		if rule != nil {
			rule.APIKey = k
			m.SetRule(t, *rule)
			t.Cleanup(func() { m.ClearRule(t, k) })
		}
		return e.CreateAccount(admin, AccountSpec{PluginKey: plugin, Type: APIKeyType, APIKey: k, Extra: extra}), k
	}

	cases := []struct {
		name, plugin, balance string
		want                  balanceWant
	}{
		// The site's quota points as they are: no unit, no conversion.
		{name: "newapi limited", plugin: NewAPIPlugin, want: balanceWant{amount: "1250000.000000"}},
		{name: "newapi limited, user quota lower", plugin: NewAPIPlugin,
			balance: `{"code":true,"data":{"total_available":1250000,"unlimited_quota":false,"user_quota":{"total_available":250000}}}`, want: balanceWant{amount: "250000.000000"}},
		{name: "newapi unlimited", plugin: NewAPIPlugin, balance: `{"code":true,"data":{"total_available":0,"unlimited_quota":true}}`, want: balanceWant{unlimited: true}},
		{name: "sub2api legacy quota", plugin: Sub2APIPlugin, want: balanceWant{amount: "7.500000", currency: "USD"}},
		{name: "sub2api without unit", plugin: Sub2APIPlugin, balance: `{"isValid":true,"remaining":7.5}`, want: balanceWant{amount: "7.500000"}},
		{name: "sub2api any unit", plugin: Sub2APIPlugin, balance: `{"isValid":true,"remaining":12,"unit":"site credits"}`, want: balanceWant{amount: "12.000000", currency: "site credits"}},
		{name: "sub2api subscription without limits", plugin: Sub2APIPlugin, balance: `{"isValid":true,"remaining":-1,"unit":"USD"}`, want: balanceWant{unlimited: true}},
		{name: "sub2api wallet", plugin: Sub2APIPlugin, balance: `{"isValid":true,"remaining":3.25,"balance":3.25,"unit":"USD"}`, want: balanceWant{amount: "3.250000", currency: "USD"}},
		{name: "sub2api key invalid", plugin: Sub2APIPlugin, balance: `{"isValid":false,"remaining":0,"unit":"USD"}`, want: balanceWant{errPrefix: "auth_rejected: "}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := e.With(t)
			mark := m.Mark(t)
			var rule *MockRule
			if c.balance != "" {
				rule = &MockRule{Balance: json.RawMessage(c.balance)}
			}
			id, k := account(t, c.plugin, rule, nil)
			b := e.AccountBalance(admin.Client, id, true)
			expectBalance(t, b, c.want)
			e.expectBalanceReads(m, mark, c.plugin, k, false)
		})
	}

	t.Run("sweep", func(t *testing.T) {
		id, _ := account(t, NewAPIPlugin, nil, nil)
		// GET /accounts/:id shows the stored balance and never reads one.
		Eventually(t, 3*time.Minute, 5*time.Second, "balance_sweep reads the new account's balance", func() bool {
			b := admin.OK(t, http.MethodGet, fmt.Sprintf("/accounts/%d", id), nil).Get("balance")
			return b.Get("updated_at").Exists() && b.Get("amount").String() == newAPIDefault.amount
		})
	})

	t.Run("site refuses the key", func(t *testing.T) {
		e := e.With(t)
		id, _ := account(t, NewAPIPlugin, &MockRule{Status: http.StatusUnauthorized}, nil)
		b := e.AccountBalance(admin.Client, id, true)
		expectBalance(t, b, balanceWant{errPrefix: "auth_rejected: "})
		a := admin.OK(t, http.MethodGet, fmt.Sprintf("/accounts/%d", id), nil)
		if a.Get("status").String() != "active" || !a.Get("schedulable").Bool() {
			t.Fatalf("a refused balance read changed the account: %s", a.Raw)
		}
	})

	t.Run("through the account's proxy", func(t *testing.T) {
		e := e.With(t)
		mark := m.Mark(t)
		id, k := account(t, NewAPIPlugin, nil, map[string]any{"proxy_url": e.MockInternalURL})
		expectBalance(t, e.AccountBalance(admin.Client, id, true), newAPIDefault)
		e.expectBalanceReads(m, mark, NewAPIPlugin, k, true)
	})

	t.Run("blocked by plugin network access", func(t *testing.T) {
		e := e.With(t)
		host := e.mockHost()
		node, setAccess := e.nodeOneNetworkAccess(admin)
		// Read once while allowed: the account is not due for the sweep
		// (600 s) while blocked, so only the refresh below asks.
		id, k := account(t, NewAPIPlugin, nil, nil)
		expectBalance(t, e.AccountBalance(node, id, true), newAPIDefault)
		setAccess(map[string]any{"blocked_domains": []string{host}})
		mark, since := m.Mark(t), time.Now().Add(-time.Second)
		b := e.AccountBalance(node, id, true)
		reason := host + " is on the blocked list of plugin network access"
		if !strings.HasPrefix(b.Get("error").String(), "transient: plugin "+NewAPIPlugin+": "+reason) || b.Get("amount").String() != newAPIDefault.amount {
			t.Fatalf("blocked read: want the transient refusal over the last amount: %s", b.Raw)
		}
		for _, r := range m.Since(t, mark) {
			if r.Key() == k {
				t.Fatalf("a blocked balance read reached the mock: %+v", r)
			}
		}
		Eventually(t, 30*time.Second, time.Second, "denied in the newapi egress log", func() bool {
			return e.deniedInEgressLog(admin, NewAPIPlugin, host, reason, since)
		})
	})
}

// balanceWant is an expected balance view: amount ("" = none; six
// decimals) and currency (the unit as the site gave it, "" = no currency
// field), no limit, or a failure starting with errPrefix.
type balanceWant struct {
	amount, currency, errPrefix string
	unlimited                   bool
}

// newAPIDefault is the mock's default New API answer: 1250000 quota points.
var newAPIDefault = balanceWant{amount: "1250000.000000"}

// expectBalance checks a balance view against want: supported, never a
// source field.
func expectBalance(t testing.TB, b gjson.Result, want balanceWant) {
	t.Helper()
	ok := b.Get("supported").Bool() && !b.Get("source").Exists() && b.Get("amount").String() == want.amount &&
		b.Get("unlimited").Bool() == want.unlimited && b.Get("currency").String() == want.currency && b.Get("currency").Exists() == (want.currency != "") &&
		strings.HasPrefix(b.Get("error").String(), want.errPrefix)
	if want.errPrefix != "" {
		ok = ok && b.Get("error").String() != "" && b.Get("error_at").Exists()
	} else {
		ok = ok && b.Get("updated_at").Exists() && !b.Get("error").Exists()
	}
	if !ok {
		t.Fatalf("balance: want %+v: %s", want, b.Raw)
	}
}

// expectBalanceReads checks the balance requests of key k since mark: each
// with the key as Bearer, to the site's own address (through the account's
// proxy when proxied); nothing asks New API's /api/status.
func (e *Env) expectBalanceReads(m *Mock, mark int64, plugin, k string, proxied bool) {
	e.T.Helper()
	path := map[string]string{NewAPIPlugin: "/api/usage/token/", Sub2APIPlugin: "/v1/usage"}[plugin]
	var reads []MockRequest
	for _, r := range callsTo(e.T, m, mark, path) {
		if r.Key() == k {
			reads = append(reads, r)
		}
	}
	if len(reads) == 0 {
		e.T.Fatalf("no %s read with the account's key", path)
	}
	for _, r := range reads {
		if r.Method != http.MethodGet || r.Header("authorization") != "Bearer "+k || r.Proxied != proxied {
			e.T.Fatalf("balance read %s: want GET with Bearer, proxied %v: %+v", path, proxied, r)
		}
	}
	if status := callsTo(e.T, m, mark, "/api/status"); len(status) != 0 {
		e.T.Fatalf("a balance read asked /api/status: %+v", status)
	}
}
