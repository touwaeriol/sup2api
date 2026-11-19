package gateway

import (
	"context"
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// blockedHosts is the administrator's plugin network access (CONTRACTS
// §113) in allow_all mode: the listed hosts and their subdomains are
// refused. asked records the plugin and host of every question.
type blockedHosts struct {
	mu    sync.Mutex
	hosts []string
	asked []string
}

func (b *blockedHosts) PluginRequestRefusal(_ context.Context, pluginKey string, target *url.URL) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.asked = append(b.asked, pluginKey+" "+target.Hostname())
	if manifest.ExternalServiceHost(b.hosts, target.Hostname()) {
		return target.Hostname() + " is on the blocked list of plugin network access"
	}
	return ""
}

func (b *blockedHosts) set(hosts ...string) {
	b.mu.Lock()
	b.hosts, b.asked = hosts, nil
	b.mu.Unlock()
}

func (b *blockedHosts) questions() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.asked...)
}

// A relay to a host the administrator's plugin network access refuses sends
// nothing, even when the host is the account's own upstream address: the
// attempt fails over as the plugin's failure under its own reason, and no
// account is cooled down or disabled. The default setting changes nothing.
func TestRelayToBlockedHostSendsNothing(t *testing.T) {
	toLocalhost := func(raw string) string { return strings.Replace(raw, "127.0.0.1", "localhost", 1) }
	x := newExtEnv(t)
	blocked := &blockedHosts{}
	x.gw.d.NetworkAccess = blocked
	// Account 1 relays to its own upstream address, localhost.
	x.man.AccountTypes[0].UpstreamFields = []string{"base_url"}
	editAccount(x.env, 1, func(a *core.Account) {
		a.Settings = json.RawMessage(`{"base_url":` + strconvQuote(toLocalhost(x.up.srv.URL)) + `}`)
	})
	x.plat.amend = func(in *pluginv1.ExecuteRequest, out *pluginv1.UpstreamRequest) {
		if in.GetAccount().GetId() == 1 {
			out.Url = toLocalhost(out.Url)
		}
	}

	r := x.messages(body(testModel, false))
	require.Equal(t, 200, r.status, string(r.body))
	require.Len(t, x.up.all(), 1)
	assert.Equal(t, "AK1", x.up.all()[0].accessKey, "nothing blocked by default")
	x.record()

	logs := captureWarnings(t)
	blocked.set("localhost")
	r = x.messages(body(testModel, false))
	require.Equal(t, 200, r.status, string(r.body))
	calls := x.up.all()
	require.Len(t, calls, 2, "the blocked relay sent nothing")
	assert.Equal(t, "AK2", calls[1].accessKey)
	rec := x.record()
	require.NotNil(t, rec.AttemptTrail)
	step := rec.AttemptTrail.Steps[0]
	assert.Equal(t, errTypePluginUnavailable, step.ErrorType)
	assert.Contains(t, step.Message, "localhost is on the blocked list of plugin network access")
	assert.Empty(t, x.accounts.cooldowns)
	assert.Empty(t, x.accounts.disabled)
	assert.Contains(t, logs.String(), "upstream host not allowed")
	assert.Contains(t, logs.String(), "reason="+core.ReasonUpstreamHostBlocked)
	assert.Contains(t, logs.String(), "host=localhost")
	assert.Contains(t, blocked.questions(), "anthropic localhost")

	// Every account refused: the client gets what it gets for any request
	// no account could serve, without the refused host.
	blocked.set("localhost", "127.0.0.1")
	r = x.messages(body(testModel, false))
	assert.GreaterOrEqual(t, r.status, 500, string(r.body))
	assert.Len(t, x.up.all(), 2, "nothing more sent")
	assert.NotContains(t, string(r.body), "localhost")
	assert.NotContains(t, string(r.body), "127.0.0.1")
	assert.Contains(t, string(r.body), core.ErrNoAvailableAccount.Code)
	assert.Empty(t, x.accounts.cooldowns)
	assert.Empty(t, x.accounts.disabled)

	// The manifest's own rule still comes first: an undeclared host is
	// refused as not allowed, without asking the setting.
	blocked.set()
	x.plat.amend = func(_ *pluginv1.ExecuteRequest, out *pluginv1.UpstreamRequest) {
		out.Url = strings.Replace(out.Url, "127.0.0.1", "undeclared.invalid", 1)
	}
	logs.Reset()
	r = x.messages(body(testModel, false))
	assert.GreaterOrEqual(t, r.status, 500, string(r.body))
	assert.Contains(t, logs.String(), "reason="+core.ReasonUpstreamHostNotAllowed)
	assert.Empty(t, blocked.questions())
}

// A WebSocket handshake to a blocked host is refused like any relay: the
// turn fails over to the next account, nothing reaches the blocked host.
func TestWebSocketHandshakeToBlockedHostFailsOver(t *testing.T) {
	e := newWSEnv(t)
	m := e.gen.accountTypes[len(e.gen.accountTypes)-1].Plugin.Manifest
	m.ExternalServices = append(m.ExternalServices, "localhost")
	e.gw.d.NetworkAccess = &blockedHosts{hosts: []string{"localhost"}}
	e.wsPlat.amend = func(_ int, in *pluginv1.ExecuteRequest, out *pluginv1.UpstreamRequest) error {
		if in.GetAccount().GetId() == 11 {
			out.Url = strings.Replace(out.Url, testUpstreamHost, "localhost", 1)
		}
		return nil
	}
	c := e.mustDial()
	c.send(create(wsModel, "hello"))
	c.until("response.completed")
	r := e.record()
	if !r.Success || r.AccountID == nil || *r.AccountID != 12 || r.Attempts != 2 {
		t.Fatalf("record: %+v", r)
	}
	if keys, _ := e.wsUp.snapshot(); !reflect.DeepEqual(keys, []string{"oa-12"}) {
		t.Fatalf("handshakes: %v", keys)
	}
	if len(e.accounts.cooldowns) != 0 || len(e.accounts.disabled) != 0 {
		t.Fatalf("cooldowns %v disabled %v", e.accounts.cooldowns, e.accounts.disabled)
	}
}
