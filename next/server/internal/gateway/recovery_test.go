package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// fakeRecovery is core.CredentialRecovery: renew decides each renewal.
type fakeRecovery struct {
	mu    sync.Mutex
	used  []string // the credentials of each call
	renew func(id int64) bool
}

func (r *fakeRecovery) RecoverCredentials(_ context.Context, id int64, used json.RawMessage) (bool, error) {
	r.mu.Lock()
	r.used = append(r.used, fmt.Sprintf("%d:%s", id, used))
	r.mu.Unlock()
	return r.renew(id), nil
}

func (r *fakeRecovery) calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.used)
}

// refreshing declares refresh on the fixture's account type.
func refreshing(e *env) { e.gen.accountTypes[0].Type.Refresh = &manifest.AccountRefresh{} }

// setKey replaces account id's key, as a renewal saving new credentials.
func (a *fakeAccounts) setKey(id int64, key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.accounts[id].Credentials = json.RawMessage(fmt.Sprintf(`{"api_key":%q}`, key))
}

// recoveryEnv is the fixture with refresh declared, account 1 answered 401
// and renewals deciding by renew.
func recoveryEnv(t *testing.T, renew func(e *env, id int64) bool) (*env, *fakeRecovery) {
	e := newEnv(t, refreshing)
	rec := &fakeRecovery{renew: func(id int64) bool { return renew(e, id) }}
	e.gw.d.Recovery = rec
	e.up.set("acc-1", &upstreamRule{status: 401})
	return e, rec
}

func (e *env) expectUpstream(want string) {
	e.t.Helper()
	if k := strings.Join(e.up.keys(), ","); k != want {
		e.t.Fatalf("upstream calls %s, want %s", k, want)
	}
}

// A 401 renews the account's credentials and the same attempt goes out
// again on it with the new ones: no failover, no cooldown, one attempt.
func TestUnauthorizedRecoveryResends(t *testing.T) {
	e, rec := recoveryEnv(t, func(e *env, id int64) bool { e.accounts.setKey(id, "acc-1b"); return true })
	if r := e.messages(body(testModel, false)); r.status != 200 {
		t.Fatalf("status %d %s", r.status, r.body)
	}
	e.expectUpstream("acc-1,acc-1b")
	if got := rec.calls(); !slices.Equal(got, []string{`1:{"api_key":"acc-1"}`}) {
		t.Fatalf("recoveries %v", got)
	}
	if _, disabled := e.accounts.disabled[1]; disabled || len(e.accounts.cooldowns) != 0 {
		t.Fatalf("account penalised: disabled %v cooldowns %v", e.accounts.disabled, e.accounts.cooldowns)
	}
	if r := e.record(); r.Attempts != 1 || *r.AccountID != 1 || !r.Success {
		t.Fatalf("record: attempts %d account %d success %v", r.Attempts, *r.AccountID, r.Success)
	}
}

// A renewal that fails leaves the 401 to the classification as before: the
// fixture's plugin disables the account and the request fails over.
func TestUnauthorizedRecoveryFailureClassifies(t *testing.T) {
	e, rec := recoveryEnv(t, func(*env, int64) bool { return false })
	if r := e.messages(body(testModel, false)); r.status != 200 {
		t.Fatalf("status %d %s", r.status, r.body)
	}
	e.expectUpstream("acc-1,acc-2")
	if len(rec.calls()) != 1 || e.accounts.disabled[1] == "" {
		t.Fatalf("recoveries %v, disabled %v", rec.calls(), e.accounts.disabled)
	}
}

// Each account recovers once per request: a 401 to the renewed credentials
// is classified, and the next account's 401 recovers on its own.
func TestUnauthorizedRecoveryOncePerAccount(t *testing.T) {
	e, rec := recoveryEnv(t, func(e *env, id int64) bool { e.accounts.setKey(id, fmt.Sprintf("acc-%db", id)); return true })
	e.up.set("acc-1b", &upstreamRule{status: 401})
	e.up.set("acc-2", &upstreamRule{status: 401})
	if r := e.messages(body(testModel, false)); r.status != 200 {
		t.Fatalf("status %d %s", r.status, r.body)
	}
	e.expectUpstream("acc-1,acc-1b,acc-2,acc-2b")
	if got := rec.calls(); !slices.Equal(got, []string{`1:{"api_key":"acc-1"}`, `2:{"api_key":"acc-2"}`}) {
		t.Fatalf("recoveries %v", got)
	}
	if e.accounts.disabled[1] == "" {
		t.Fatalf("a second 401 was not classified: disabled %v", e.accounts.disabled)
	}
}

// Without refresh declared, or without a recovery, a 401 is classified at
// once.
func TestUnauthorizedWithoutRefresh(t *testing.T) {
	rec := &fakeRecovery{renew: func(int64) bool { return true }}
	for name, e := range map[string]*env{"no refresh": newEnv(t), "no recovery": newEnv(t, refreshing)} {
		if name == "no refresh" {
			e.gw.d.Recovery = rec
		}
		e.up.set("acc-1", &upstreamRule{status: 401})
		if r := e.messages(body(testModel, false)); r.status != 200 {
			t.Fatalf("%s: status %d %s", name, r.status, r.body)
		}
		e.expectUpstream("acc-1,acc-2")
	}
	if len(rec.calls()) != 0 {
		t.Fatalf("recoveries %v", rec.calls())
	}
}

// The waiting form recovers alike: the plugin's Execute runs again on the
// same account and the answer is committed once.
func TestUnauthorizedRecoveryWaited(t *testing.T) {
	e, _ := waitedEnv(t, core.DisconnectPrecharge, &upstreamRule{status: 401})
	refreshing(e)
	e.gw.d.Recovery = &fakeRecovery{renew: func(id int64) bool { e.accounts.setKey(id, "acc-1b"); return true }}
	if r := e.messages(body(testModel, false)); r.status != 200 {
		t.Fatalf("status %d %s", r.status, r.body)
	}
	e.expectUpstream("acc-1,acc-1b")
	if r := e.record(); r.Attempts != 1 || *r.AccountID != 1 || !r.Success {
		t.Fatalf("record: attempts %d account %d success %v", r.Attempts, *r.AccountID, r.Success)
	}
	e.noRecord()
}

// Only attempts run through recovering recover: a classification outside
// one (a WebSocket handshake not wired yet) is not held back.
func TestUnauthorizedRecoveryNeedsRecovering(t *testing.T) {
	e := newEnv(t, refreshing)
	rec := &fakeRecovery{renew: func(int64) bool { return true }}
	e.gw.d.Recovery = rec
	c := &call{g: e.gw, ep: endpointOf(t, builtinPlatform(t, "anthropic"), "anthropic.messages")}
	rt := &typeRoute{binding: e.gen.accountTypes[0]}
	acct := e.accounts.accounts[1].PluginAccount("anthropic", nil)
	ctx := context.Background()
	if c.recoverUnauthorized(ctx, rt, acct, 401) || len(rec.calls()) != 0 {
		t.Fatal("recovered outside recovering")
	}
	var inside bool
	c.recovering(ctx, func() attemptResult {
		inside = c.recoverUnauthorized(ctx, rt, acct, 401)
		return attemptResult{kind: attemptDone}
	})
	if !inside || len(rec.calls()) != 1 || c.recovery.armed {
		t.Fatalf("inside %v, recoveries %v, armed %v", inside, rec.calls(), c.recovery.armed)
	}
}
