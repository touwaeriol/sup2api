package gateway

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

func (e *env) setSlots(key string, inUse, limit int) {
	e.slots.mu.Lock()
	defer e.slots.mu.Unlock()
	e.slots.inUse[key] = inUse
	if limit > 0 {
		if e.slots.limits == nil {
			e.slots.limits = map[string]int{}
		}
		e.slots.limits[key] = limit
	}
}

func (e *env) slotCount(key string) int {
	e.slots.mu.Lock()
	defer e.slots.mu.Unlock()
	return e.slots.inUse[key]
}

func (e *env) awaitSlots(t *testing.T, key string, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for e.slotCount(key) != n {
		if time.Now().After(deadline) {
			t.Fatalf("%s stayed at %d, want %d", key, e.slotCount(key), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// bindSession serves one request of the session and returns the id and key of
// the account it was bound to.
func bindSession(t *testing.T, e *env, session string) (string, string) {
	t.Helper()
	if r := e.messages(withSession(body(testModel, false), session)); r.status != 200 {
		t.Fatalf("binding request: %d %s", r.status, r.body)
	}
	e.record()
	k := e.up.keys()
	key := k[len(k)-1]
	return strings.TrimPrefix(key, "acc-"), key
}

func queueSettings(mutate func(*GatewaySettings)) GatewaySettings {
	gw := defaultGatewaySettings()
	if mutate != nil {
		mutate(&gw)
	}
	return gw
}

func TestStickyRequestWaitsForItsAccount(t *testing.T) {
	e := newEnv(t)
	e.enableSticky(onFailureFailover)
	e.setSettings(queueSettings(nil), StickySettings{Enabled: true, DefaultTTLSeconds: 3600})
	id, key := bindSession(t, e, "s1")
	e.setSlots("account:"+id, 1, 1)
	done := make(chan result, 1)
	go func() { done <- e.messages(withSession(body(testModel, false), "s1")) }()
	e.awaitSlots(t, core.AccountWaitSlots+":"+id, 1)
	if len(e.up.keys()) != 1 {
		t.Fatal("request went upstream while queueing")
	}
	e.setSlots("account:"+id, 0, 0)
	r := <-done
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if k := e.up.keys(); k[len(k)-1] != key {
		t.Fatalf("served by %v, want %s", k, key)
	}
	e.awaitSlots(t, core.AccountWaitSlots+":"+id, 0)
}

func TestStickyQueueTimeoutMovesOn(t *testing.T) {
	e := newEnv(t)
	e.enableSticky(onFailureFailover)
	e.setSettings(queueSettings(func(g *GatewaySettings) { g.QueueStickyWaitSeconds = 1 }), StickySettings{Enabled: true, DefaultTTLSeconds: 3600})
	id, key := bindSession(t, e, "s1")
	e.setSlots("account:"+id, 1, 1)
	started := time.Now()
	r := e.messages(withSession(body(testModel, false), "s1"))
	if r.status != 200 || time.Since(started) < time.Second {
		t.Fatalf("%d after %v: %s", r.status, time.Since(started), r.body)
	}
	if k := e.up.keys(); k[len(k)-1] == key {
		t.Fatalf("served by the full account: %v", k)
	}
}

func TestStickyQueueFullMovesOnAtOnce(t *testing.T) {
	e := newEnv(t)
	e.enableSticky(onFailureFailover)
	e.setSettings(queueSettings(func(g *GatewaySettings) { g.QueueStickyMaxWaiting = 1 }), StickySettings{Enabled: true, DefaultTTLSeconds: 3600})
	id, key := bindSession(t, e, "s1")
	e.setSlots("account:"+id, 1, 1)
	e.setSlots(core.AccountWaitSlots+":"+id, 1, 0)
	started := time.Now()
	r := e.messages(withSession(body(testModel, false), "s1"))
	if r.status != 200 || time.Since(started) > 2*time.Second {
		t.Fatalf("%d after %v: %s", r.status, time.Since(started), r.body)
	}
	if k := e.up.keys(); k[len(k)-1] == key {
		t.Fatalf("served by the full account: %v", k)
	}
}

// CONTRACTS §18.1: a stick rule answers 429 rather than leaving a bound account
// that has no room, whether it is full or at a rate limit.
func TestStickRuleAnswers429WithoutRoom(t *testing.T) {
	t.Run("full", func(t *testing.T) {
		e := newEnv(t)
		e.enableSticky(onFailureStick)
		id, _ := bindSession(t, e, "s1")
		e.setSlots("account:"+id, 1, 1)
		if r := e.messages(withSession(body(testModel, false), "s1")); r.status != 429 {
			t.Fatalf("%d %s", r.status, r.body)
		}
		if n := len(e.up.keys()); n != 1 {
			t.Fatalf("upstream calls %d", n)
		}
	})
	t.Run("rate limited", func(t *testing.T) {
		lim := newFakeLimiter()
		e := newEnv(t)
		e.gw.d.Limiter = lim
		e.enableSticky(onFailureStick)
		id, _ := bindSession(t, e, "s1")
		n := int64(0)
		for _, c := range id {
			n = n*10 + int64(c-'0')
		}
		lim.exhausted[n] = true
		e.accounts.set(n, func(acc *core.Account) { acc.RPMLimit = 1 })
		if r := e.messages(withSession(body(testModel, false), "s1")); r.status != 429 {
			t.Fatalf("%d %s", r.status, r.body)
		}
		if n := len(e.up.keys()); n != 1 {
			t.Fatalf("upstream calls %d", n)
		}
	})
}

func fillAll(e *env, inUse, limit int) {
	for _, id := range []string{"1", "2", "3"} {
		e.setSlots("account:"+id, inUse, limit)
	}
}

func TestAllFullQueuesOnTheLeastLoaded(t *testing.T) {
	e := newEnv(t)
	e.setSettings(queueSettings(nil), StickySettings{})
	for id, c := range map[int64]int{1: 2, 2: 1, 3: 2} {
		e.accounts.set(id, func(acc *core.Account) { acc.MaxConcurrency, acc.Priority = c, 99 })
		e.setSlots("account:"+itoa(id), c, 0)
	}
	// Account 1 and 3 have someone waiting; all are at their cap.
	e.setSlots(core.AccountWaitSlots+":1", 1, 0)
	e.setSlots(core.AccountWaitSlots+":3", 1, 0)
	done := make(chan result, 1)
	go func() { done <- e.messages(body(testModel, false)) }()
	e.awaitSlots(t, core.AccountWaitSlots+":2", 1)
	e.setSlots("account:2", 0, 0)
	r := <-done
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if k := e.up.keys(); len(k) != 1 || k[0] != "acc-2" {
		t.Fatalf("served by %v", k)
	}
	e.awaitSlots(t, core.AccountWaitSlots+":2", 0)
}

func TestAllFullGivesUp(t *testing.T) {
	t.Run("after the wait", func(t *testing.T) {
		e := newEnv(t)
		e.setSettings(queueSettings(func(g *GatewaySettings) { g.QueueFallbackWaitSecond = 1 }), StickySettings{})
		fillAll(e, 1, 1)
		started := time.Now()
		if r := e.messages(body(testModel, false)); r.status != 429 || time.Since(started) < time.Second {
			t.Fatalf("%d after %v: %s", r.status, time.Since(started), r.body)
		}
	})
	t.Run("queue full", func(t *testing.T) {
		e := newEnv(t)
		e.setSettings(queueSettings(func(g *GatewaySettings) { g.QueueFallbackMaxWaiting = 1 }), StickySettings{})
		fillAll(e, 1, 1)
		for _, id := range []string{"1", "2", "3"} {
			e.setSlots(core.AccountWaitSlots+":"+id, 1, 0)
		}
		started := time.Now()
		if r := e.messages(body(testModel, false)); r.status != 429 || time.Since(started) > 2*time.Second {
			t.Fatalf("%d after %v: %s", r.status, time.Since(started), r.body)
		}
	})
	t.Run("queue off", func(t *testing.T) {
		e := newEnv(t)
		e.setSettings(noQueueSettings(), StickySettings{})
		fillAll(e, 1, 1)
		started := time.Now()
		if r := e.messages(body(testModel, false)); r.status != 429 || time.Since(started) > 2*time.Second {
			t.Fatalf("%d after %v: %s", r.status, time.Since(started), r.body)
		}
	})
}

func TestRateLimitedAccountsDoNotQueue(t *testing.T) {
	lim := newFakeLimiter()
	e := newEnv(t)
	e.gw.d.Limiter = lim
	e.setSettings(queueSettings(nil), StickySettings{})
	for _, id := range []int64{1, 2, 3} {
		e.accounts.set(id, func(acc *core.Account) { acc.RPMLimit = 1 })
		lim.exhausted[id] = true
	}
	started := time.Now()
	if r := e.messages(body(testModel, false)); r.status != 429 || time.Since(started) > 2*time.Second {
		t.Fatalf("%d after %v: %s", r.status, time.Since(started), r.body)
	}
}

func TestLeavingTheQueueWhenTheClientGoes(t *testing.T) {
	e := newEnv(t)
	e.setSlots("account:7", 1, 1)
	c := &call{g: e.gw, gw: defaultGatewaySettings(), rid: "r"}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	go func() {
		_, ok := c.waitForAccount(ctx, &core.AccountRef{ID: 7, MaxConcurrency: 1}, 3, time.Minute)
		done <- ok
	}()
	e.awaitSlots(t, core.AccountWaitSlots+":7", 1)
	cancel()
	if <-done {
		t.Fatal("acquired after the client left")
	}
	e.awaitSlots(t, core.AccountWaitSlots+":7", 0)
}
