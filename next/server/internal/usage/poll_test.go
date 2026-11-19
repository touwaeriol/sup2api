package usage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

type boundPollProxy struct {
	core.ProxyDirectory
	client *http.Client
	id     int64
}

func (p *boundPollProxy) HTTPClient(_ context.Context, id *int64) (core.ProxyClient, error) {
	pc := core.ProxyClient{Client: p.client}
	if id != nil {
		p.id = *id
		pc.Proxy = &core.ProxyRef{ID: *id, ProxySpec: core.ProxySpec{Protocol: "http", Host: "proxy.test", Port: 3128, Password: "pw"}}
	}
	return pc, nil
}

func TestPollHTTPBoundsRedirectsAndPreservesProxyClient(t *testing.T) {
	var destinationHits, admitted atomic.Int32
	dst := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { destinationHits.Add(1) }))
	defer dst.Close()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/large" {
			_, _ = w.Write([]byte(strings.Repeat("x", 20<<20)))
			return
		}
		http.Redirect(w, r, dst.URL, http.StatusFound)
	}))
	defer up.Close()
	var sharedRedirect atomic.Int32
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { sharedRedirect.Add(1); return nil }}
	proxy := &boundPollProxy{client: client}
	proxyID := int64(42)
	acc := &core.Account{AccountRef: core.AccountRef{ID: 7, ProxyID: &proxyID}}
	s := &Service{rec: &reconciler{ReconcileDeps: ReconcileDeps{Accounts: recAccounts{acc: acc, proxies: proxy}, AllowPrivateUpstream: true}}}
	id := acc.ID
	admit := func(context.Context) error { admitted.Add(1); return nil }
	out, err := s.fetchExecutionHTTP(context.Background(), &pluginv1.ExecutionHTTPRequest{Url: up.URL}, id, admit)
	if err != nil || out.GetStatus() != 302 || destinationHits.Load() != 0 || sharedRedirect.Load() != 0 || proxy.id != 42 {
		t.Fatalf("redirect/proxy boundary: %+v %v destination=%d proxy=%d", out, err, destinationHits.Load(), proxy.id)
	}
	if client.CheckRedirect == nil {
		t.Fatal("shared client mutated")
	}
	_ = client.CheckRedirect(nil, nil)
	if sharedRedirect.Load() != 1 {
		t.Fatal("shared redirect policy changed")
	}
	out, err = s.fetchExecutionHTTP(context.Background(), &pluginv1.ExecutionHTTPRequest{Url: up.URL + "/large"}, id, admit)
	if err != nil || len(out.GetBody()) != 20<<20 { // no size limit: whole
		t.Fatalf("large answer: %d %v", len(out.GetBody()), err)
	}
	for _, req := range []*pluginv1.ExecutionHTTPRequest{
		{Url: up.URL, Headers: map[string]string{"Upgrade": "websocket"}},
		{Url: up.URL, Headers: map[string]string{"Proxy-Authorization": "secret"}},
	} {
		out, err := s.fetchExecutionHTTP(context.Background(), req, id, admit)
		if err != nil || out.GetTransportError() == "" {
			t.Fatalf("invalid request accepted: %+v %v", out, err)
		}
	}
	if admitted.Load() != 2 {
		t.Fatalf("invalid request consumed admission: %d", admitted.Load())
	}
	s.rec.AllowPrivateUpstream = false
	out, err = s.fetchExecutionHTTP(context.Background(), &pluginv1.ExecutionHTTPRequest{Url: up.URL}, id, admit)
	if err != nil || out.GetTransportError() == "" || admitted.Load() != 2 {
		t.Fatal("private destination accepted")
	}
}

// Each Monitor fetch goes out through the account's proxy as it stands
// then: a proxy changed between two fetches carries the second.
func TestPollHTTPFollowsTheAccountsCurrentProxy(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer up.Close()
	proxy := &boundPollProxy{client: up.Client()}
	first, second := int64(42), int64(43)
	acc := &core.Account{AccountRef: core.AccountRef{ID: 7, ProxyID: &first}}
	s := &Service{rec: &reconciler{ReconcileDeps: ReconcileDeps{Accounts: recAccounts{acc: acc, proxies: proxy}, AllowPrivateUpstream: true}}}
	for _, want := range []*int64{&first, &second} {
		acc.ProxyID = want
		out, err := s.fetchExecutionHTTP(context.Background(), &pluginv1.ExecutionHTTPRequest{Url: up.URL}, acc.ID, nil)
		if err != nil || out.GetStatus() != http.StatusOK || proxy.id != *want {
			t.Fatalf("fetch through proxy %d: %+v %v, went through %d", *want, out, err, proxy.id)
		}
	}
}

type monitorPlugin struct {
	*recPlugin
	calls int
	run   func(context.Context, *pluginv1.PollRequest, core.ExecutionHTTP) (*pluginv1.ReconcileResult, error)
}

func (p *monitorPlugin) Monitor(ctx context.Context, in *pluginv1.PollRequest, http core.ExecutionHTTP) (*pluginv1.ReconcileResult, error) {
	p.calls++
	return p.run(ctx, in, http)
}

type pollLimiter struct {
	core.AccountLimiter
	allow   bool
	calls   int
	account int64
}

func (l *pollLimiter) TryHit(_ context.Context, a core.AccountRef, _, _ string) (bool, error) {
	l.calls++
	l.account = a.ID
	return l.allow, nil
}

// Monitor runs on the bound account and its proxy; a busy account or a
// fetch its limiter refuses defers the check without spending an attempt,
// whatever the plugin answered.
func TestMonitorUsesBoundAccountAndDefersWithoutBurningAttempts(t *testing.T) {
	proxyID := int64(42)
	acc := &core.Account{AccountRef: core.AccountRef{ID: 7, PluginKey: "vid", Type: "vid_key", ProxyID: &proxyID}, Status: "active"}
	rf := reconcileFixtureWith(t, acc)
	f := rf.fixture
	gen := f.svc.rec.Registry.Current().(*recGen)
	mp := &monitorPlugin{recPlugin: rf.plugin}
	mp.run = func(ctx context.Context, in *pluginv1.PollRequest, fetch core.ExecutionHTTP) (*pluginv1.ReconcileResult, error) {
		// The account comes with the proxy its fetches go out through
		// (CONTRACTS §124).
		if in.GetEntry().GetRefId() != "bound-monitor" || in.GetAccount().GetId() != 7 ||
			in.GetAccount().GetProxy().GetId() != 42 || in.GetAccount().GetProxy().GetPassword() != "pw" {
			t.Errorf("wrong bound identity: %+v", in)
		}
		got, err := fetch(ctx, &pluginv1.ExecutionHTTPRequest{Url: rf.up.URL})
		if err != nil {
			return &pluginv1.ReconcileResult{State: pluginv1.ReconcileResult_POLL_FAILED, Reason: err.Error()}, nil
		}
		return checkedResult(&pluginv1.ReconcileResult{State: pluginv1.ReconcileResult_FAILED, Reason: "upstream failure"}, got)
	}
	gen.client = mp
	proxy := &boundPollProxy{client: rf.up.Client()}
	f.svc.rec.Accounts = recAccounts{acc: acc, proxies: proxy}
	slots := &taskTestSlots{allow: false}
	f.svc.rec.Slots = slots
	limits := &pollLimiter{allow: false}
	f.svc.rec.Limiter = limits
	r := f.reserved("req-bound-monitor", "bound-monitor", core.UsageTokens{Input: 1000, Output: 100})
	f.svc.process(context.Background(), []*core.UsageRecord{r})
	check := func() {
		t.Helper()
		if a := f.scalar(`SELECT attempts::text || ' ' || poll_failures::text FROM pending_settlements WHERE ref_id='bound-monitor'`); a != "0 0" {
			t.Fatalf("deferred attempt consumed: %s", a)
		}
	}
	f.due()
	f.svc.ReconcileDue(context.Background())
	check()
	if mp.calls != 0 || rf.hits.Load() != 0 {
		t.Fatal("busy account was asked")
	}
	slots.allow = true
	f.due()
	f.svc.ReconcileDue(context.Background())
	check()
	if mp.calls != 1 || rf.hits.Load() != 0 || limits.account != 7 {
		t.Fatal("rate-limit boundary not applied")
	}
	limits.allow = true
	f.due()
	f.svc.ReconcileDue(context.Background())
	if mp.calls != 2 || rf.hits.Load() != 1 || proxy.id != 42 {
		t.Fatalf("Monitor routing: calls=%d HTTP=%d proxy=%d", mp.calls, rf.hits.Load(), proxy.id)
	}
	if f.scalar(`SELECT billing_status FROM usage_logs WHERE request_id='req-bound-monitor'`) != StatusFree {
		t.Fatal("Monitor outcome did not settle the original reservation")
	}
	if f.cost("req-bound-monitor").Sign() != 0 {
		t.Fatal("failed task did not refund")
	}
}

// downPollProxy cannot hand out any proxy's client (disabled, gone).
type downPollProxy struct{ core.ProxyDirectory }

func (downPollProxy) HTTPClient(context.Context, *int64) (core.ProxyClient, error) {
	return core.ProxyClient{}, core.ErrUnavailable.WithMessage("proxy disabled")
}

// Without the account's egress Monitor is not called (the plugin is never
// handed the account without its proxy, CONTRACTS §124): the check waits
// without spending an attempt, as for a cooldown.
func TestMonitorWaitsWithoutEgress(t *testing.T) {
	proxyID := int64(42)
	acc := &core.Account{AccountRef: core.AccountRef{ID: 7, PluginKey: "vid", Type: "vid_key", ProxyID: &proxyID}, Status: "active"}
	rf := reconcileFixtureWith(t, acc)
	f := rf.fixture
	gen := f.svc.rec.Registry.Current().(*recGen)
	mp := &monitorPlugin{recPlugin: rf.plugin, run: func(context.Context, *pluginv1.PollRequest, core.ExecutionHTTP) (*pluginv1.ReconcileResult, error) {
		return &pluginv1.ReconcileResult{State: pluginv1.ReconcileResult_PENDING}, nil
	}}
	gen.client = mp
	f.svc.rec.Accounts = recAccounts{acc: acc, proxies: downPollProxy{}}
	f.svc.rec.Slots = &taskTestSlots{allow: true}
	f.svc.rec.Limiter = &pollLimiter{allow: true}
	r := f.reserved("req-no-egress", "no-egress", core.UsageTokens{Input: 1000, Output: 100})
	f.svc.process(context.Background(), []*core.UsageRecord{r})
	f.due()
	f.svc.ReconcileDue(context.Background())
	if mp.calls != 0 || rf.hits.Load() != 0 {
		t.Fatalf("Monitor without an egress: calls %d, upstream hits %d", mp.calls, rf.hits.Load())
	}
	if a := f.scalar(`SELECT attempts::text || ' ' || poll_failures::text FROM pending_settlements WHERE ref_id='no-egress'`); a != "0 0" {
		t.Fatalf("deferred attempt consumed: %s", a)
	}
}
