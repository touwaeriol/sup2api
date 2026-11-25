package gateway

import (
	"net/http"
	"strings"
	"testing"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

// overloaded529 classifies like the codex plugin does an overload error:
// fail over with the upstream status, answer the client 529.
func overloaded529(_ *pluginv1.ClassifyErrorRequest, r *pluginv1.ClassifyErrorResponse) {
	r.ClientStatus = 529
}

// The plugin's client_status is the reply status of an upstream error answer,
// with or without a failure policy (CONTRACTS §139); the body stays the
// upstream's.
func TestClientStatusIsTheReplyStatus(t *testing.T) {
	for _, policy := range []bool{false, true} {
		e, plat, _ := headEnv(t, 1, overloadedEvent)
		if policy {
			e.setFailurePolicy(testFailurePolicy())
		}
		plat.classifyHook = overloaded529
		r := e.responses(true)
		if r.status != 529 || r.json().Get("error.code").String() != "server_is_overloaded" {
			t.Fatalf("policy=%v response: %d %s", policy, r.status, r.body)
		}
		if rec := e.record(); rec.StatusCode != 529 || rec.Success {
			t.Fatalf("policy=%v record %+v", policy, rec)
		}
	}
}

// The failure policy still matches the upstream status: a 500 the plugin
// answers as 529 moves to another account under a rule for 500 alone.
func TestClientStatusKeepsThePolicyOnTheUpstreamStatus(t *testing.T) {
	e, plat, up := headEnv(t, 1, overloadedEvent)
	p := testFailurePolicy()
	p.RetryRules[0].Match.StatusCodes = "500"
	e.setFailurePolicy(p)
	e.accounts.addTyped(testGroup, 11, 100, "codex-11", "codexlike", "chatgpt")
	plat.classifyHook = overloaded529
	if r := e.responses(true); r.status != 200 || up.count() != 2 {
		t.Fatalf("response: %d %s, upstream requests %d", r.status, r.body, up.count())
	}
	rec := e.record()
	if !rec.Success || len(rec.AttemptTrail.Steps) != 2 || rec.AttemptTrail.Steps[0].Status != 529 ||
		!strings.Contains(rec.FailurePolicy, `"status":500`) {
		t.Fatalf("record %+v trail %+v", rec, rec.AttemptTrail)
	}
}

// Without a client_status the reply keeps the upstream status, also outside
// 400-599 (an unfollowed redirect), as before.
func TestClientStatusAbsentKeepsTheUpstreamStatus(t *testing.T) {
	e := newEnv(t)
	for _, k := range []string{"acc-1", "acc-2", "acc-3"} {
		e.up.set(k, &upstreamRule{status: http.StatusFound, body: `moved`})
	}
	if r := e.messages(body(testModel, false)); r.status != http.StatusFound || string(r.body) != "moved" {
		t.Fatalf("response: %d %s", r.status, r.body)
	}
	if rec := e.record(); rec.StatusCode != http.StatusFound {
		t.Fatalf("record %+v", rec)
	}
}

func TestReplyStatus(t *testing.T) {
	for _, tc := range []struct{ plugin, upstream, want int }{
		{0, 500, 500}, {529, 500, 529}, {302, 500, 500}, {600, 302, 302}, {0, 302, 302},
	} {
		if got := replyStatus(tc.plugin, tc.upstream); got != tc.want {
			t.Fatalf("replyStatus(%d, %d) = %d, want %d", tc.plugin, tc.upstream, got, tc.want)
		}
	}
}
