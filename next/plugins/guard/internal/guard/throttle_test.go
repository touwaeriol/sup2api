package guard

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

// sink is a webhook receiver recording the alert payloads.
type sink struct {
	mu  sync.Mutex
	got []map[string]any
	srv *httptest.Server
}

func newSink(t *testing.T) *sink {
	s := &sink{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		s.mu.Lock()
		s.got = append(s.got, m)
		s.mu.Unlock()
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *sink) alerts() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.got...)
}

// clock is a settable clock for Plugin.now (the node-local cooldown).
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Now()} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// node is one guard instance with webhook alerts on.
type node struct {
	p     *Plugin
	h     *pluginsdktest.Harness
	clock *clock
	sent  int // blocked requests that may have produced an alert
}

func startNode(t *testing.T, host *pluginsdktest.FakeHost, url string, cooldownSec int) *node {
	t.Helper()
	n := &node{p: New(), clock: newClock()}
	n.p.now = n.clock.now
	n.h = pluginsdktest.Start(t, n.p, pluginsdktest.Options{Host: host, SDK: sdkOpts(),
		Config: Settings{WebhookURL: url, AlertCooldownSec: cooldownSec}})
	setRules(t, n.p,
		Rule{ID: 7, Name: "seven", Kind: KindKeyword, Pattern: "word7", Enabled: true},
		Rule{ID: 8, Name: "eight", Kind: KindKeyword, Pattern: "word8", Enabled: true},
	)
	return n
}

// block sends count requests matching rule 7 or 8.
func (n *node) block(t *testing.T, rule int, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		r, err := n.h.Hook.OnGatewayRequest(context.Background(), hookReq("say word"+string(rune('0'+rule))))
		if err != nil || r.GetDecision() != pluginv1.GatewayRequestHookResponse_DECISION_DENY {
			t.Fatalf("rule %d: %v %v", rule, r, err)
		}
		n.sent++
	}
}

func (n *node) throttled() int64 { return n.p.stats.alertsThrottled.Load() }

// settle waits until every blocked request has been sent, throttled or
// dropped, then checks the number of alerts received.
func settle(t *testing.T, s *sink, want int, nodes ...*node) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		total, done := 0, 0
		for _, n := range nodes {
			total += n.sent
			done += int(n.throttled() + n.p.stats.droppedAlerts.Load() + n.p.stats.alertErrors.Load())
		}
		done += len(s.alerts())
		if done >= total || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// A late extra alert would show up here.
	time.Sleep(50 * time.Millisecond)
	if got := len(s.alerts()); got != want {
		t.Fatalf("alerts received = %d, want %d: %v", got, want, s.alerts())
	}
}

func ruleIDs(alerts []map[string]any) []int {
	out := make([]int, 0, len(alerts))
	for _, a := range alerts {
		id, _ := a["rule_id"].(float64)
		out = append(out, int(id))
	}
	return out
}

func throttleLogs(h *pluginsdktest.FakeHost) (perNode, cluster int) {
	for _, l := range h.Logs() {
		switch {
		case strings.Contains(l.Message, "throttled per node"):
			perNode++
		case strings.Contains(l.Message, "cluster-wide again"):
			cluster++
		}
	}
	return
}

// Two nodes share one lock table: per rule, one alert per cooldown in total.
func TestAlertThrottleAcrossNodes(t *testing.T) {
	s := newSink(t)
	host1, host2 := pluginsdktest.NewFakeHost(), pluginsdktest.NewFakeHost()
	host2.Locks = host1.Locks
	n1 := startNode(t, host1, s.srv.URL, 60)
	n2 := startNode(t, host2, s.srv.URL, 60)

	n1.block(t, 7, 1)
	settle(t, s, 1, n1, n2)
	if !host1.Locks.Held(alertLockName(7)) {
		t.Fatal("the alert must leave the rule's lock held (the cooldown window)")
	}
	n1.block(t, 7, 2) // node 1 knows its own window: dropped in the hook
	n2.block(t, 7, 3) // node 2 finds the lock taken
	settle(t, s, 1, n1, n2)
	if n1.throttled() != 2 || n2.throttled() != 3 {
		t.Fatalf("throttled = %d, %d", n1.throttled(), n2.throttled())
	}

	// Another rule has its own window.
	n2.block(t, 8, 2)
	settle(t, s, 2, n1, n2)
	if n2.throttled() != 4 {
		t.Fatalf("node 2 throttled = %d", n2.throttled())
	}

	// Window over (lock lapsed): the next alert goes out, from either node.
	host1.Locks.Expire(alertLockName(7))
	n2.block(t, 7, 1)
	settle(t, s, 3, n1, n2)
	// Node 2 holds the new window; node 1, past its own, still may not send.
	n1.clock.add(61 * time.Second)
	n1.block(t, 7, 1)
	settle(t, s, 3, n1, n2)
	if n1.throttled() != 3 {
		t.Fatalf("node 1 throttled = %d", n1.throttled())
	}
	host1.Locks.Expire(alertLockName(7))
	n1.block(t, 7, 1)
	settle(t, s, 4, n1, n2)

	if got := ruleIDs(s.alerts()); len(got) != 4 || got[0] != 7 || got[1] != 8 || got[2] != 7 || got[3] != 7 {
		t.Fatalf("alerted rules = %v", got)
	}
	for _, a := range s.alerts() {
		if a["cooldown_sec"] != float64(60) {
			t.Fatalf("payload = %v", a)
		}
	}
	health, err := n2.h.Plugin.Health(context.Background(), &pluginv1.HealthRequest{})
	if err != nil || health.GetMetrics()["alerts_throttled"] != 4 {
		t.Fatalf("health = %v %v", health, err)
	}
	if p, c := throttleLogs(host1); p != 0 || c != 0 {
		t.Fatalf("no state change expected, logs = %d/%d", p, c)
	}
}

// alert_cooldown_sec=0: an alert for every block, no locks.
func TestAlertThrottleOff(t *testing.T) {
	s := newSink(t)
	host := pluginsdktest.NewFakeHost()
	host.SetLockErr(status.Error(codes.Internal, "locks must not be used")) // would show as a state change log
	n := startNode(t, host, s.srv.URL, 0)
	n.block(t, 7, 3)
	settle(t, s, 3, n)
	for _, a := range s.alerts() {
		if a["cooldown_sec"] != float64(0) || a["rule_id"] != float64(7) {
			t.Fatalf("payload = %v", a)
		}
	}
	if n.throttled() != 0 || host.Locks.Held(alertLockName(7)) {
		t.Fatal("cooldown 0 must not throttle")
	}
	if p, c := throttleLogs(host); p != 0 || c != 0 {
		t.Fatalf("logs = %d/%d", p, c)
	}
}

// Without the lock (no grant, Redis down) each node throttles on its own,
// and the switch is logged once per change.
func TestAlertThrottleFallback(t *testing.T) {
	s := newSink(t)
	host := pluginsdktest.NewFakeHost()
	host.SetLockErr(status.Error(codes.PermissionDenied, "lock not granted"))
	n := startNode(t, host, s.srv.URL, 60)

	n.block(t, 7, 1)
	settle(t, s, 1, n)
	n.block(t, 7, 2)
	n.block(t, 8, 1)
	settle(t, s, 2, n)
	if n.throttled() != 2 {
		t.Fatalf("throttled = %d", n.throttled())
	}
	if p, c := throttleLogs(host); p != 1 || c != 0 {
		t.Fatalf("logs = %d/%d, want one per-node warning", p, c)
	}

	// The node's own window ends: send again, no new log (same state).
	n.clock.add(61 * time.Second)
	n.block(t, 7, 1)
	settle(t, s, 3, n)
	if p, _ := throttleLogs(host); p != 1 {
		t.Fatalf("per-node logs = %d", p)
	}

	// Redis down (outcome unknown): rather send than miss - the node's own
	// cooldown decides. A different reason is a state change: logged.
	host.SetLockErr(status.Error(codes.Unavailable, "locks unavailable"))
	n.clock.add(61 * time.Second)
	n.block(t, 7, 1)
	settle(t, s, 4, n)
	n.block(t, 7, 1)
	settle(t, s, 4, n)
	if p, _ := throttleLogs(host); p != 2 {
		t.Fatalf("per-node logs = %d", p)
	}

	// Locks back: cluster-wide again, logged once.
	host.SetLockErr(nil)
	n.clock.add(61 * time.Second)
	n.block(t, 7, 1)
	settle(t, s, 5, n)
	n.block(t, 8, 1) // rule 8's local window ended long ago
	settle(t, s, 6, n)
	if p, c := throttleLogs(host); p != 2 || c != 1 {
		t.Fatalf("logs = %d/%d", p, c)
	}
	if !host.Locks.Held(alertLockName(7)) || !host.Locks.Held(alertLockName(8)) {
		t.Fatal("cluster mode must take the locks")
	}
	if n.throttled() != 3 {
		t.Fatalf("throttled = %d", n.throttled())
	}
}

func TestConfigureCooldown(t *testing.T) {
	p := New()
	h := pluginsdktest.Start(t, p, pluginsdktest.Options{SDK: sdkOpts()})
	if got := p.settings.Load().AlertCooldownSec; got != defaultAlertCooldownSec {
		t.Fatalf("default cooldown = %d", got)
	}
	for _, tc := range []struct {
		cfg, code string
	}{
		{`{"alert_cooldown_sec":-1}`, "minimum"},
		{`{"alert_cooldown_sec":301}`, "maximum"},
		{`{"alert_cooldown_sec":86400}`, "maximum"},
		{`{"alert_cooldown_sec":1.5}`, "type"},
		{`{"alert_cooldown_sec":"60"}`, "type"},
	} {
		errs := h.Configure(tc.cfg, nil)
		if len(errs) != 1 || errs[0].GetField() != "alert_cooldown_sec" || errs[0].GetCode() != tc.code {
			t.Fatalf("%s: errs = %v", tc.cfg, errs)
		}
	}
	for cfg, want := range map[string]int{
		`{"alert_cooldown_sec":0}`:   0,
		`{"alert_cooldown_sec":300}`: 300,
		`{"webhook_url":""}`:         defaultAlertCooldownSec,
	} {
		if errs := h.Configure(cfg, nil); len(errs) != 0 {
			t.Fatalf("%s: errs = %v", cfg, errs)
		}
		if got := p.settings.Load().AlertCooldownSec; got != want {
			t.Fatalf("%s: cooldown = %d", cfg, got)
		}
	}
}

// TestValidateSettings: every value the settings JSON Schema refused (one
// counterexample per constraint, written out here) is still refused, at its
// field, by ValidateSettings and Configure alike; the plugin's own URL check
// too; the default set and real settings pass. The console form
// (ui/native GuardSettings) uses the same bounds: 0-300 s, default 60.
func TestValidateSettings(t *testing.T) {
	if maxAlertCooldownSec != 300 || defaultAlertCooldownSec != 60 {
		t.Fatalf("cooldown bounds %d, default %d: change the settings form too", maxAlertCooldownSec, defaultAlertCooldownSec)
	}
	h := pluginsdktest.Start(t, New(), pluginsdktest.Options{SDK: sdkOpts()})
	refused := []struct{ settings, field, code string }{
		{`{"webhook_url":"ftp://hooks.example.com"}`, "webhook_url", "pattern"},
		{`{"webhook_url":"https://hooks.example.com/a b"}`, "webhook_url", "pattern"},
		{`{"webhook_url":7}`, "webhook_url", "type"},
		{`{"record_snippets":"true"}`, "record_snippets", "type"},
		{`{"alert_cooldown_sec":-1}`, "alert_cooldown_sec", "minimum"},
		{`{"alert_cooldown_sec":301}`, "alert_cooldown_sec", "maximum"},
		{`{"alert_cooldown_sec":1.5}`, "alert_cooldown_sec", "type"},
		{`{"webhook":"https://hooks.example.com"}`, "webhook", "unknown"},
		{`[]`, "", "invalid_json"},
		{`{"webhook_url":"https://"}`, "webhook_url", "pattern"},
		{`{"webhook_url":"http:///x"}`, "webhook_url", "format"},
	}
	for _, c := range refused {
		resp, err := h.ValidateSettings(c.settings, nil)
		if err != nil {
			t.Fatalf("%s: %v", c.settings, err)
		}
		if errs := resp.GetErrors(); len(errs) != 1 || errs[0].GetField() != c.field || errs[0].GetCode() != c.code {
			t.Errorf("%s: errors = %v, want %s %s", c.settings, errs, c.field, c.code)
		}
		if _, ok := pluginsdk.AsFieldErrors(New().Configure(context.Background(), pluginsdk.Config{JSON: []byte(c.settings)})); !ok {
			t.Errorf("%s: Configure accepted it", c.settings)
		}
	}
	for _, ok := range []string{
		`{}`,
		// The set the console form saves untouched (its defaults).
		`{"webhook_url":"","record_snippets":false,"alert_cooldown_sec":60}`,
		`{"webhook_url":"https://hooks.example.com/guard?token=x","record_snippets":true,"alert_cooldown_sec":0}`,
		`{"webhook_url":"http://10.0.0.5:8080/hook","alert_cooldown_sec":300}`,
	} {
		resp, err := h.ValidateSettings(ok, nil)
		if err != nil || len(resp.GetErrors()) != 0 || resp.GetNormalizedConfigJson() != "" {
			t.Errorf("%s: %v %v", ok, resp.GetErrors(), err)
		}
	}
}
