package gateway

import "testing"

// The account-bound headers are handed to attempts on the first attempt's
// account (a retry on it included) and left out on any other.
func TestInboundHeadersAccountBound(t *testing.T) {
	gc, _ := newTestContext(map[string]string{"X-Turn": "t1", "X-Other": "o1"})
	c := &call{c: gc}
	rt := &typeRoute{passHeaders: []string{"x-turn", "x-other"}, boundHeaders: []string{"X-Turn"}}
	for _, tc := range []struct {
		account  int64
		n        int
		wantTurn bool
	}{
		{1, 0, true},  // first attempt
		{1, 1, true},  // same account again
		{2, 2, false}, // another account
		{1, 3, true},  // back on the first account
	} {
		h := c.inboundHeaders(rt, tc.account, tc.n)
		if _, has := h["x-turn"]; has != tc.wantTurn || h["x-other"] != "o1" {
			t.Errorf("attempt %d on %d: %v", tc.n, tc.account, h)
		}
	}
}

// boundTurn has the fixture's account type receive x-turn and declare it
// account-bound.
func boundTurn(e *env) {
	ap := &e.gen.accountTypes[0].Type.Platforms[0]
	ap.PassHeaders, ap.AccountBoundHeaders = []string{"x-turn"}, []string{"x-turn"}
}

// After a failover the plugin is no longer handed the account-bound header.
func TestFailoverDropsAccountBoundHeaders(t *testing.T) {
	e := newEnv(t, boundTurn)
	e.up.set("acc-1", &upstreamRule{status: 500})
	if r := e.do("/v1/messages", body(testModel, false), map[string]string{"x-turn": "turn-1"}); r.status != 200 {
		t.Fatalf("status %d %s", r.status, r.body)
	}
	e.expectUpstream("acc-1,acc-2")
	e.plat.mu.Lock()
	defer e.plat.mu.Unlock()
	if len(e.plat.builds) != 2 || e.plat.builds[0].GetInboundHeaders()["x-turn"] != "turn-1" {
		t.Fatalf("first attempt: %v", e.plat.builds)
	}
	if _, has := e.plat.builds[1].GetInboundHeaders()["x-turn"]; has {
		t.Fatalf("second attempt on another account still has the header: %v", e.plat.builds[1].GetInboundHeaders())
	}
}

// Without the declaration every attempt is handed the same headers.
func TestFailoverKeepsHeadersNotBound(t *testing.T) {
	e := newEnv(t, func(e *env) { e.gen.accountTypes[0].Type.Platforms[0].PassHeaders = []string{"x-turn"} })
	e.up.set("acc-1", &upstreamRule{status: 500})
	e.do("/v1/messages", body(testModel, false), map[string]string{"x-turn": "turn-1"})
	e.plat.mu.Lock()
	defer e.plat.mu.Unlock()
	if len(e.plat.builds) != 2 || e.plat.builds[1].GetInboundHeaders()["x-turn"] != "turn-1" {
		t.Fatalf("builds: %v", e.plat.builds)
	}
}
