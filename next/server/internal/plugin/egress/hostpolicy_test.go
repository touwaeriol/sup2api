package egress

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

func TestNormalizeDomain(t *testing.T) {
	ok := map[string]string{
		"example.com":             "example.com",
		"  API.Example.COM. ":     "api.example.com",
		"localhost":               "localhost",
		"例子.测试":                   "xn--fsqu00a.xn--0zwm56d",
		"xn--fsqu00a.xn--0zwm56d": "xn--fsqu00a.xn--0zwm56d",
		"93.184.216.34":           "93.184.216.34",
		"2001:DB8::1":             "[2001:db8::1]",
		"[2001:db8::1]":           "[2001:db8::1]",
		"a-b.c":                   "a-b.c",
	}
	for in, want := range ok {
		if got, valid := NormalizeDomain(in); !valid || got != want {
			t.Errorf("%q: got %q %v, want %q", in, got, valid, want)
		}
	}
	for _, in := range []string{"", "  ", "https://example.com", "example.com/path", "example.com:443", "*.example.com", "*",
		"exa mple.com", "-bad.com", "bad-.com", "a..b", "under_score.com", "user@example.com", "fe80::1%eth0",
		strings.Repeat("a", 64) + ".com", strings.Repeat("abcdefghi.", 26) + "com"} {
		if got, valid := NormalizeDomain(in); valid {
			t.Errorf("%q accepted as %q", in, got)
		}
	}
}

func TestHostPolicyRefusal(t *testing.T) {
	h := HostPolicy{Mode: HostAllowAll, BlockedDomains: []string{"example.com", "[2001:db8::1]", "93.184.216.34"}, AllowedDomains: []string{"other.org"}}
	refused := map[string]bool{
		"example.com": true, "api.example.com": true, "a.b.example.com": true, "badexample.com": false,
		"other.org": false, "2001:db8::1": true, "93.184.216.34": true, "93.184.216.35": false,
	}
	for host, want := range refused {
		if msg := h.refusal(host); (msg != "") != want || (want && !strings.Contains(msg, "blocked list")) {
			t.Errorf("allow_all %q: %q", host, msg)
		}
	}
	h.Mode = HostBlockAll
	refused = map[string]bool{"other.org": false, "x.other.org": false, "example.com": true, "notother.org": true}
	for host, want := range refused {
		if msg := h.refusal(host); (msg != "") != want || (want && !strings.Contains(msg, "not on the allowed list")) {
			t.Errorf("block_all %q: %q", host, msg)
		}
	}
	// An empty allowed list in block_all reaches nothing.
	if (HostPolicy{Mode: HostBlockAll}).refusal("x.com") == "" {
		t.Fatal("empty allowed list")
	}
	// The default and an unknown mode allow everything.
	if DefaultHostPolicy().refusal("x.com") != "" || (HostPolicy{Mode: "bogus"}).normalized().refusal("x.com") != "" {
		t.Fatal("default")
	}
}

// hostBox is a settings row the tests change; loads counts the reads.
type hostBox struct {
	mu    sync.Mutex
	h     HostPolicy
	err   error
	loads int
}

func (b *hostBox) load(context.Context) (HostPolicy, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.loads++
	return b.h, b.err
}

func (b *hostBox) set(h HostPolicy, err error) {
	b.mu.Lock()
	b.h, b.err = h, err
	b.mu.Unlock()
}

func (b *hostBox) reads() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.loads
}

// subBus records the config:changed subscriber so a test can deliver a
// broadcast from "another node".
type subBus struct {
	mu       sync.Mutex
	handlers []func([]byte)
}

func (b *subBus) Publish(_ context.Context, _ string, p []byte) error {
	b.mu.Lock()
	hs := append([]func([]byte){}, b.handlers...)
	b.mu.Unlock()
	for _, h := range hs {
		h(p)
	}
	return nil
}

func (b *subBus) Subscribe(ch string, h func([]byte)) func() {
	if ch != core.ChannelConfigChanged {
		return func() {}
	}
	b.mu.Lock()
	b.handlers = append(b.handlers, h)
	b.mu.Unlock()
	return func() {}
}

// hostEnv is a tunnel whose plugin network access is box.
func hostEnv(t *testing.T, h HostPolicy, st logStore) (*env, *hostBox, *subBus, string) {
	t.Helper()
	box, bus := &hostBox{h: h}, &subBus{}
	e := newEnvStore(t, st, Options{LoadHostPolicy: box.load, Bus: bus})
	_, port, _ := net.SplitHostPort(echoTCP(t))
	return e, box, bus, port
}

func dialHost(host, port string) error { return dialErr(net.JoinHostPort(host, port)) }

func TestHostPolicyDefaultAllows(t *testing.T) {
	// No database, no loader: every host passes (what the tunnel did before).
	newEnv(t, nil, Options{})
	_, port, _ := net.SplitHostPort(echoTCP(t))
	if err := dialHost("anything.test", port); err != nil {
		t.Fatal(err)
	}
	// The default row allows everything too.
	_, _, _, port = hostEnv(t, DefaultHostPolicy(), nil)
	if err := dialHost("anything.test", port); err != nil {
		t.Fatal(err)
	}
}

func TestHostPolicyAllowAllBlocksListedDomains(t *testing.T) {
	st := newMemStore()
	_, _, _, port := hostEnv(t, HostPolicy{Mode: HostAllowAll, BlockedDomains: []string{"evil.test"}}, st)
	for _, host := range []string{"evil.test", "api.evil.test"} {
		if err := dialHost(host, port); err == nil || !strings.Contains(err.Error(), "blocked list of plugin network access") {
			t.Fatalf("%s: %v", host, err)
		}
	}
	for _, host := range []string{"good.test", "notevil.test"} {
		if err := dialHost(host, port); err != nil {
			t.Fatalf("%s: %v", host, err)
		}
	}
	// DNS queries for a blocked name are refused as well.
	if m := rawDNS(t, "api.evil.test.", dnsmessage.TypeA); m.Header.RCode != dnsmessage.RCodeRefused {
		t.Fatalf("dns rcode %v", m.Header.RCode)
	}
	if m := rawDNS(t, "good.test.", dnsmessage.TypeA); m.Header.RCode != dnsmessage.RCodeSuccess || len(m.Answers) != 1 {
		t.Fatalf("dns allowed %+v", m)
	}
	// The refusals are logged with their reason.
	waitLogged(t, st, "api.evil.test", "tcp", "api.evil.test is on the blocked list of plugin network access")
	waitLogged(t, st, "api.evil.test", "dns", "api.evil.test is on the blocked list of plugin network access")
}

func TestHostPolicyBlockAllAllowsListedDomains(t *testing.T) {
	st := newMemStore()
	_, _, _, port := hostEnv(t, HostPolicy{Mode: HostBlockAll, AllowedDomains: []string{"good.test"}, BlockedDomains: []string{"good.test"}}, st)
	// The blocked list is kept but not used in block_all.
	for _, host := range []string{"good.test", "api.good.test"} {
		if err := dialHost(host, port); err != nil {
			t.Fatalf("%s: %v", host, err)
		}
	}
	for _, host := range []string{"other.test", "notgood.test"} {
		if err := dialHost(host, port); err == nil || !strings.Contains(err.Error(), "not on the allowed list of plugin network access") {
			t.Fatalf("%s: %v", host, err)
		}
	}
	waitLogged(t, st, "other.test", "tcp", "other.test is not on the allowed list of plugin network access")
}

// The administrator's lists come on top of the plugin's own grant and
// policy: they never widen what a plugin may reach.
func TestHostPolicyDoesNotWidenThePluginGrant(t *testing.T) {
	e, _, _, port := hostEnv(t, HostPolicy{Mode: HostBlockAll, AllowedDomains: []string{"good.test"}}, nil)
	// Without the net grant nothing is reachable, listed or not.
	e.policy.set(core.EgressPolicy{Mode: PolicyAllowAll})
	if err := dialHost("api.good.test", port); err == nil || !strings.Contains(err.Error(), "no net permission") {
		t.Fatalf("no grant: %v", err)
	}
	// A plugin restricted to its granted domains stays restricted: the
	// administrator's allowed list does not add api.good.test to its grant.
	e.policy.set(core.EgressPolicy{Mode: PolicyAllowlist, AllowedDomains: []string{"smtp.good.test"}, Net: true})
	if err := dialHost("api.good.test", port); err == nil || !strings.Contains(err.Error(), "plugin egress policy") {
		t.Fatalf("outside the grant: %v", err)
	}
	if err := dialHost("smtp.good.test", port); err != nil {
		t.Fatalf("grant and list: %v", err)
	}
	// Granted but not on the administrator's list: refused.
	e.policy.set(core.EgressPolicy{Mode: PolicyAllowlist, AllowedDomains: []string{"*"}, Net: true})
	if err := dialHost("other.test", port); err == nil || !strings.Contains(err.Error(), "not on the allowed list") {
		t.Fatalf("outside the list: %v", err)
	}
}

// A save on any node reaches this one through config:changed, without a
// restart; until then the cached copy serves.
func TestHostPolicyChangeTakesEffectWithoutRestart(t *testing.T) {
	_, box, bus, port := hostEnv(t, DefaultHostPolicy(), nil)
	if err := dialHost("evil.test", port); err != nil {
		t.Fatal(err)
	}
	box.set(HostPolicy{Mode: HostAllowAll, BlockedDomains: []string{"evil.test"}}, nil)
	if err := dialHost("evil.test", port); err != nil {
		t.Fatalf("cached copy within the refresh interval: %v", err)
	}
	// Another key does not drop the cache.
	_ = bus.Publish(context.Background(), core.ChannelConfigChanged, []byte(`{"key":"settings.gateway"}`))
	if err := dialHost("evil.test", port); err != nil {
		t.Fatalf("unrelated broadcast: %v", err)
	}
	_ = bus.Publish(context.Background(), core.ChannelConfigChanged, []byte(`{"key":"settings.plugin_egress"}`))
	if err := dialHost("evil.test", port); err == nil {
		t.Fatal("blocked after the broadcast: dialled")
	}
	// Switching the mode keeps both lists: block_all with the allowed list
	// saved earlier, and back.
	box.set(HostPolicy{Mode: HostBlockAll, BlockedDomains: []string{"evil.test"}, AllowedDomains: []string{"good.test"}}, nil)
	_ = bus.Publish(context.Background(), core.ChannelConfigChanged, []byte(`{"key":"settings.plugin_egress"}`))
	if dialHost("good.test", port) != nil || dialHost("other.test", port) == nil {
		t.Fatal("block_all")
	}
	box.set(HostPolicy{Mode: HostAllowAll, BlockedDomains: []string{"evil.test"}, AllowedDomains: []string{"good.test"}}, nil)
	_ = bus.Publish(context.Background(), core.ChannelConfigChanged, []byte(`{"key":"settings.plugin_egress"}`))
	if dialHost("other.test", port) != nil || dialHost("evil.test", port) == nil {
		t.Fatal("back to allow_all")
	}
}

func TestHostPoliciesCache(t *testing.T) {
	box := &hostBox{h: HostPolicy{Mode: HostBlockAll, AllowedDomains: []string{"a.com"}}}
	c := newHostPolicies(box.load, slog.New(slog.DiscardHandler))
	ctx := context.Background()
	// Never read and the read fails: refused, not allowed by default.
	box.set(HostPolicy{}, errors.New("db down"))
	if _, err := c.get(ctx); !errors.Is(err, errHostPolicyUnread) {
		t.Fatalf("unread: %v", err)
	}
	box.set(HostPolicy{Mode: HostBlockAll, AllowedDomains: []string{"a.com"}}, nil)
	h, err := c.get(ctx)
	if err != nil || h.Mode != HostBlockAll || len(h.BlockedDomains) != 0 || h.BlockedDomains == nil {
		t.Fatalf("read: %+v %v", h, err)
	}
	n := box.reads()
	if _, err := c.get(ctx); err != nil || box.reads() != n {
		t.Fatalf("fresh copy read again: %d %d", box.reads(), n)
	}
	// After a save the next caller waits for a read; a failing read keeps
	// the last setting.
	box.set(HostPolicy{}, errors.New("db down"))
	c.invalidate()
	if h, err := c.get(ctx); err != nil || h.Mode != HostBlockAll {
		t.Fatalf("kept: %+v %v", h, err)
	}
	// Stale: served at once, refreshed in the background.
	box.set(HostPolicy{Mode: HostAllowAll}, nil)
	c.mu.Lock()
	c.lastAt = time.Now().Add(-2 * hostPolicyTTL)
	c.mu.Unlock()
	if h, _ := c.get(ctx); h.Mode != HostBlockAll {
		t.Fatalf("stale copy not served: %+v", h)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if h, _ := c.get(ctx); h.Mode == HostAllowAll {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background refresh")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitLogged waits for a denied log row of host on network with msg.
func waitLogged(t *testing.T, st *memStore, host, network, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, r := range st.find(host, network) {
			if r.Result == ResultDenied && r.Error == msg {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no denied %s row for %s with %q: %+v", network, host, msg, st.find(host, network))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The requests the core sends for a plugin are decided by the same cached
// setting as the tunnel (CONTRACTS §113): a refusal is a denied row of the
// plugin's egress log, an allowed request is not logged, and no request
// reads the setting again while the cached copy is fresh.
func TestPluginRequestRefusal(t *testing.T) {
	st := newMemStore()
	box, bus := &hostBox{h: DefaultHostPolicy()}, &subBus{}
	p := newProvider(st, Options{NodeID: "node-1", LoadHostPolicy: box.load, Bus: bus, FlushInterval: 20 * time.Millisecond, Logger: discardLogger()})
	t.Cleanup(p.Close)
	ctx := context.Background()
	refusal := func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return p.PluginRequestRefusal(ctx, "demo", u)
	}
	if msg := refusal("https://api.upstream.test/v1/messages"); msg != "" {
		t.Fatalf("default: %q", msg)
	}
	box.set(HostPolicy{Mode: HostAllowAll, BlockedDomains: []string{"upstream.test"}, AllowedDomains: []string{"upstream.test"}}, nil)
	_ = bus.Publish(ctx, core.ChannelConfigChanged, []byte(`{"key":"settings.plugin_egress"}`))
	reads := box.reads()
	for _, raw := range []string{"https://API.upstream.test/v1/messages", "http://upstream.test:8080/x"} {
		if msg := refusal(raw); !strings.Contains(msg, "is on the blocked list of plugin network access") {
			t.Fatalf("%s: %q", raw, msg)
		}
	}
	if msg := refusal("https://notupstream.test/"); msg != "" {
		t.Fatalf("other host: %q", msg)
	}
	// block_all: only the allowed list (subdomains included) passes.
	box.set(HostPolicy{Mode: HostBlockAll, AllowedDomains: []string{"upstream.test"}}, nil)
	_ = bus.Publish(ctx, core.ChannelConfigChanged, []byte(`{"key":"settings.plugin_egress"}`))
	if refusal("https://api.upstream.test/") != "" || !strings.Contains(refusal("https://relay.example.test/"), "not on the allowed list") {
		t.Fatal("block_all")
	}
	if got := box.reads() - reads; got != 2 {
		t.Fatalf("setting read %d times for five requests and two saves, want 2", got)
	}
	waitLogged(t, st, "api.upstream.test", "https", "api.upstream.test is on the blocked list of plugin network access")
	waitLogged(t, st, "upstream.test", "http", "upstream.test is on the blocked list of plugin network access")
	waitLogged(t, st, "relay.example.test", "https", "relay.example.test is not on the allowed list of plugin network access")
	for _, r := range st.find("api.upstream.test", "https") {
		if r.Port != 443 || r.PluginKey != "demo" || r.NodeID != "node-1" {
			t.Fatalf("row %+v", r)
		}
	}
	if rows := st.find("upstream.test", "http"); len(rows) != 1 || rows[0].Port != 8080 {
		t.Fatalf("http rows %+v", rows)
	}
	for _, host := range []string{"notupstream.test", "api.upstream.test"} {
		for _, r := range st.find(host, "https") {
			if r.Result != ResultDenied {
				t.Fatalf("an allowed request was logged: %+v", r)
			}
		}
	}
	// Requests are not external hosts of the domain list.
	if d := st.domain("relay.example.test"); d != nil {
		t.Fatalf("domain list: %+v", d)
	}
}
