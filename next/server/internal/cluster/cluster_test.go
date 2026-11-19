package cluster

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Now()} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}

func newReg(rdb redis.UniversalClient, c *clock, node, boot string) *Registry {
	return NewRegistry(rdb, nil, RegistryOptions{NodeID: node, BootID: boot, Addr: node + ":8080", HostVersion: "0.1.0", Now: c.Now})
}

func TestRegistryHeartbeatAndLiveNodes(t *testing.T) {
	ctx := context.Background()
	mr, rdb := newRedis(t)
	c := newClock()
	sandbox := &core.PluginSandboxStatus{Wrapped: true, Landlock: true, ABI: 6, Active: []string{"fs", "scope"}, Missing: []string{"unix"}}
	a := NewRegistry(rdb, nil, RegistryOptions{NodeID: "node-a", BootID: "boot-a", Addr: "node-a:8080", HostVersion: "0.1.0", Now: c.Now, PluginSandbox: sandbox})
	b := newReg(rdb, c, "node-b", "boot-b")
	if a.NodeID() != "node-a" || a.BootID() != "boot-a" {
		t.Fatal("identity")
	}
	if NewRegistry(rdb, nil, RegistryOptions{}).BootID() == "" {
		t.Fatal("boot id must be generated")
	}
	if a.Healthy() {
		t.Fatal("healthy before first heartbeat")
	}
	if err := a.ReportPlugin(ctx, "guard", `{"state":"active"}`); err != nil {
		t.Fatal(err)
	}
	for _, r := range []*Registry{a, b} {
		if err := r.Heartbeat(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if !a.Healthy() {
		t.Fatal("expected healthy")
	}
	if ttl := mr.TTL("node:info:boot-a"); ttl <= 0 || ttl > 15*time.Second {
		t.Fatalf("info ttl %v", ttl)
	}
	if ttl := mr.TTL("node:plugins:boot-a"); ttl <= 0 {
		t.Fatalf("plugins ttl %v", ttl)
	}
	nodes, err := a.LiveNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("want 2 nodes, got %+v", nodes)
	}
	for _, n := range nodes {
		if n.BootID == "boot-a" {
			if n.NodeID != "node-a" || n.Addr != "node-a:8080" || n.HostVersion != "0.1.0" || n.StartedAt.IsZero() {
				t.Fatalf("bad node a: %+v", n)
			}
			if n.Plugins["guard"] != `{"state":"active"}` {
				t.Fatalf("plugins: %+v", n.Plugins)
			}
			if !reflect.DeepEqual(n.PluginSandbox, sandbox) {
				t.Fatalf("plugin sandbox: %+v", n.PluginSandbox)
			}
		} else if n.PluginSandbox != nil {
			t.Fatalf("node b reported a sandbox: %+v", n.PluginSandbox)
		}
	}
	// Removing a plugin report.
	if err := a.ReportPlugin(ctx, "guard", ""); err != nil {
		t.Fatal(err)
	}
	if mr.Exists("node:plugins:boot-a") {
		if v := mr.HGet("node:plugins:boot-a", "guard"); v != "" {
			t.Fatal("plugin state not removed")
		}
	}

	// b stops heartbeating; after 16 s it is dead and a's heartbeat prunes it.
	c.Add(16 * time.Second)
	mr.FastForward(16 * time.Second)
	if err := a.Heartbeat(ctx); err != nil {
		t.Fatal(err)
	}
	alive, err := a.IsAlive(ctx, "boot-b")
	if err != nil || alive {
		t.Fatalf("boot-b alive=%v err=%v", alive, err)
	}
	alive, _ = a.IsAlive(ctx, "boot-a")
	if !alive {
		t.Fatal("boot-a should be alive")
	}
	if _, err := rdb.ZScore(ctx, "node:live", "boot-b").Result(); err != redis.Nil {
		t.Fatalf("expired member not pruned: %v", err)
	}
	nodes, _ = a.LiveNodes(ctx)
	if len(nodes) != 1 || nodes[0].BootID != "boot-a" {
		t.Fatalf("live nodes %+v", nodes)
	}

	// Redis goes away: healthy turns false after the window.
	mr.Close()
	if err := a.Heartbeat(ctx); err == nil {
		t.Fatal("expected heartbeat error")
	}
	if !a.Healthy() {
		t.Fatal("still inside window")
	}
	c.Add(16 * time.Second)
	if a.Healthy() {
		t.Fatal("expected unhealthy after 15 s without redis")
	}
}

func TestRegistryLeave(t *testing.T) {
	ctx := context.Background()
	_, rdb := newRedis(t)
	c := newClock()
	a := newReg(rdb, c, "n", "boot-x")
	_ = a.Heartbeat(ctx)
	a.Leave(ctx)
	if ok, _ := a.IsAlive(ctx, "boot-x"); ok {
		t.Fatal("still alive after leave")
	}
}

func TestRegistryHealthyWithPG(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	_, rdb := newRedis(t)
	c := newClock()
	r := NewRegistry(rdb, db, RegistryOptions{NodeID: "n", Now: c.Now})
	if err := r.Heartbeat(ctx); err != nil {
		t.Fatal(err)
	}
	if !r.Healthy() {
		t.Fatal("expected healthy")
	}
	db.Close()
	c.Add(10 * time.Second)
	_ = r.Heartbeat(ctx) // redis ok, pg fails
	c.Add(6 * time.Second)
	if r.Healthy() {
		t.Fatal("expected unhealthy when PG is unreachable for 15 s")
	}
}

func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestBusPublishSubscribeReconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mr, rdb := newRedis(t)
	bus := NewBus(rdb, nil)
	var h1, h2 atomic.Int32
	var last atomic.Value
	c1 := bus.Subscribe("config:changed", func(p []byte) { h1.Add(1); last.Store(string(p)) })
	c2 := bus.Subscribe("config:changed", func(p []byte) { h2.Add(1) })
	panicky := bus.Subscribe("other", func([]byte) { panic("boom") })
	defer panicky()
	var reconnects atomic.Int32
	stopReconnects := bus.OnReconnect(func() { reconnects.Add(1) })
	defer stopReconnects()
	done := make(chan struct{})
	go func() { bus.Run(ctx); close(done) }()

	waitFor(t, 3*time.Second, func() bool {
		_ = bus.Publish(ctx, "config:changed", []byte("v1"))
		return h1.Load() > 0 && h2.Load() > 0
	})
	if last.Load() != "v1" {
		t.Fatal("payload")
	}
	if reconnects.Load() != 0 {
		t.Fatal("reconnect reported without a lost connection")
	}
	_ = bus.Publish(ctx, "other", []byte("x")) // panic is recovered
	c2()
	c2()
	n2 := h2.Load()

	// Redis restarts: the bus must reconnect and resubscribe. (Restart alone
	// does not drop the connections of a running server.)
	mr.Close()
	if err := mr.Restart(); err != nil {
		t.Fatal(err)
	}
	h1.Store(0)
	waitFor(t, 10*time.Second, func() bool {
		_ = bus.Publish(ctx, "config:changed", []byte("v2"))
		return h1.Load() > 0
	})
	if last.Load() != "v2" {
		t.Fatal("payload after reconnect")
	}
	if h2.Load() != n2 {
		t.Fatal("cancelled handler still called")
	}
	// Messages published while disconnected are lost: subscribers are told.
	if reconnects.Load() == 0 {
		t.Fatal("reconnect not reported")
	}
	c1()
	cancel()
	<-done
}

func TestSlotsAcquireRelease(t *testing.T) {
	ctx := context.Background()
	_, rdb := newRedis(t)
	c := newClock()
	reg := newReg(rdb, c, "a", "boot-a")
	s := NewSlots(rdb, reg, SlotOptions{Now: c.Now, TTL: time.Minute})
	r1, ok, err := s.Acquire(ctx, "account", 7, 2, "req1")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	r2, ok, _ := s.Acquire(ctx, "account", 7, 2, "req2")
	if !ok {
		t.Fatal("second slot")
	}
	if _, ok, _ := s.Acquire(ctx, "account", 7, 2, "req3"); ok {
		t.Fatal("limit exceeded")
	}
	if n, _ := s.InUse(ctx, "account", 7); n != 2 {
		t.Fatalf("in use %d", n)
	}
	members, _ := rdb.ZRange(ctx, "slot:account:7", 0, -1).Result()
	found := false
	for _, m := range members {
		if strings.HasPrefix(m, "boot-a:req1:") {
			found = true
		}
	}
	if !found {
		t.Fatal("member must preserve boot/request identity and add a lease token")
	}
	r1()
	r1() // idempotent: must not release req2 or anything else
	if n, _ := s.InUse(ctx, "account", 7); n != 1 {
		t.Fatalf("in use after release %d", n)
	}
	// Unlimited.
	for i := range 5 {
		if _, ok, _ := s.Acquire(ctx, "user", 1, 0, ""); !ok {
			t.Fatalf("unlimited acquire %d", i)
		}
	}
	// Expired members stop counting and are dropped on the next acquire.
	c.Add(2 * time.Minute)
	if n, _ := s.InUse(ctx, "account", 7); n != 0 {
		t.Fatalf("expired still counted: %d", n)
	}
	if _, ok, _ := s.Acquire(ctx, "account", 7, 1, "req4"); !ok {
		t.Fatal("acquire after expiry")
	}
	r2()
}

func TestSlotsRefreshKeepsHeldSlots(t *testing.T) {
	ctx := context.Background()
	_, rdb := newRedis(t)
	c := newClock()
	reg := newReg(rdb, c, "a", "boot-a")
	s := NewSlots(rdb, reg, SlotOptions{Now: c.Now, TTL: time.Minute})
	rel, _, _ := s.Acquire(ctx, "account", 1, 1, "long-stream")
	c.Add(50 * time.Second)
	if err := s.refresh(ctx); err != nil {
		t.Fatal(err)
	}
	c.Add(50 * time.Second) // 100 s after acquire, but refreshed at 50 s
	if n, _ := s.InUse(ctx, "account", 1); n != 1 {
		t.Fatalf("refreshed slot expired: %d", n)
	}
	rel()
	if err := s.refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.InUse(ctx, "account", 1); n != 0 {
		t.Fatalf("released slot came back: %d", n)
	}
}

// TestSlotsReclaimOnlyDeadNodes is the regression test for the old system's
// bug where a starting node wiped the slots of every other node.
func TestSlotsReclaimOnlyDeadNodes(t *testing.T) {
	ctx := context.Background()
	_, rdb := newRedis(t)
	c := newClock()
	regA := newReg(rdb, c, "a", "boot-a")
	regB := newReg(rdb, c, "b", "boot-b")
	regDead := newReg(rdb, c, "c", "boot-dead")
	sA := NewSlots(rdb, regA, SlotOptions{Now: c.Now})
	sB := NewSlots(rdb, regB, SlotOptions{Now: c.Now})
	sDead := NewSlots(rdb, regDead, SlotOptions{Now: c.Now})
	// The dead node heartbeated once, took slots and then vanished.
	_ = regDead.Heartbeat(ctx)
	for _, id := range []string{"d1", "d2"} {
		if _, ok, _ := sDead.Acquire(ctx, "account", 1, 0, id); !ok {
			t.Fatal("dead acquire")
		}
	}
	if _, ok, _ := sDead.Acquire(ctx, "user", 9, 0, "d3"); !ok {
		t.Fatal("dead acquire")
	}
	c.Add(20 * time.Second)

	// A and B are alive and hold slots (B's request id contains ':').
	_ = regA.Heartbeat(ctx)
	_ = regB.Heartbeat(ctx)
	if _, ok, _ := sA.Acquire(ctx, "account", 1, 0, "a1"); !ok {
		t.Fatal("a acquire")
	}
	if _, ok, _ := sB.Acquire(ctx, "account", 1, 0, "b:1"); !ok {
		t.Fatal("b acquire")
	}
	if _, ok, _ := sB.Acquire(ctx, "user", 9, 0, "b2"); !ok {
		t.Fatal("b acquire")
	}

	// Node A starts up / runs its periodic reclaim.
	n, err := sA.Reclaim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("want 3 reclaimed, got %d", n)
	}
	acc, _ := rdb.ZRange(ctx, "slot:account:1", 0, -1).Result()
	usr, _ := rdb.ZRange(ctx, "slot:user:9", 0, -1).Result()
	if len(acc) != 2 || len(usr) != 1 || !strings.HasPrefix(acc[0], "boot-a:a1:") || !strings.HasPrefix(acc[1], "boot-b:b:1:") || !strings.HasPrefix(usr[0], "boot-b:b2:") {
		t.Fatalf("live slots touched: account=%v user=%v", acc, usr)
	}
	// Running again is a no-op.
	if n, _ := sA.Reclaim(ctx); n != 0 {
		t.Fatalf("second reclaim removed %d", n)
	}
}

func TestSlotsReclaimSkipsWhenSelfNotAlive(t *testing.T) {
	ctx := context.Background()
	_, rdb := newRedis(t)
	c := newClock()
	regA := newReg(rdb, c, "a", "boot-a")
	regB := newReg(rdb, c, "b", "boot-b")
	sA := NewSlots(rdb, regA, SlotOptions{Now: c.Now})
	sB := NewSlots(rdb, regB, SlotOptions{Now: c.Now})
	_ = regB.Heartbeat(ctx)
	_, _, _ = sB.Acquire(ctx, "account", 1, 0, "b1")
	// node:live was wiped (e.g. Redis flushed): nobody looks alive.
	_ = rdb.Del(ctx, "node:live").Err()
	n, err := sA.Reclaim(ctx)
	if err != nil || n != 0 {
		t.Fatalf("reclaim without own heartbeat: n=%d err=%v", n, err)
	}
	if cnt, _ := sB.InUse(ctx, "account", 1); cnt != 1 {
		t.Fatal("slot of live node removed")
	}
}

func TestClusterStartClose(t *testing.T) {
	ctx := context.Background()
	_, rdb := newRedis(t)
	cl := New(rdb, nil, Options{NodeID: "n1", HostVersion: "0.1.0"})
	if err := cl.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, _ := cl.Registry.IsAlive(ctx, cl.Registry.BootID()); !ok {
		t.Fatal("not alive after start")
	}
	got := make(chan string, 1)
	cancel := cl.Bus.Subscribe("authz:changed", func(p []byte) {
		select {
		case got <- string(p):
		default:
		}
	})
	defer cancel()
	waitFor(t, 3*time.Second, func() bool {
		_ = cl.Bus.Publish(ctx, "authz:changed", []byte("1"))
		select {
		case <-got:
			return true
		default:
			return false
		}
	})
	cl.Close()
	if ok, _ := cl.Registry.IsAlive(ctx, cl.Registry.BootID()); ok {
		t.Fatal("alive after close")
	}
}
