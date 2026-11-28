package gateway

import (
	"net/http"
	"slices"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// A handshake the upstream refuses with 401 renews the account's credentials
// and the handshake goes out again on the same account (CONTRACTS §141.2):
// no failover, no penalty, one attempt.
func TestWebSocketHandshakeUnauthorizedRecovery(t *testing.T) {
	e := newWSEnv(t)
	e.gen.accountTypes[len(e.gen.accountTypes)-1].Type.Refresh = &manifest.AccountRefresh{}
	rec := &fakeRecovery{renew: func(id int64) bool { e.accounts.setKey(id, "oa-11b"); return true }}
	e.gw.d.Recovery = rec
	e.wsUp.reject["oa-11"] = http.StatusUnauthorized
	c := e.mustDial()
	c.send(create(wsModel, "hello"))
	c.until("response.completed")
	if got := rec.calls(); !slices.Equal(got, []string{`11:{"api_key":"oa-11"}`}) {
		t.Fatalf("recoveries %v", got)
	}
	if r := e.record(); *r.AccountID != 11 || r.Attempts != 1 || !r.Success {
		t.Fatalf("record: account %d attempts %d success %v", *r.AccountID, r.Attempts, r.Success)
	}
	if _, disabled := e.accounts.disabled[11]; disabled {
		t.Fatal("the renewed account was disabled")
	}
}
