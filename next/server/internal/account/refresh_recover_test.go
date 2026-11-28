package account

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

func TestRecoveryStep(t *testing.T) {
	old, renewed := []byte(`{"access_token":"a"}`), []byte(`{"access_token":"b"}`)
	for _, tc := range []struct {
		name          string
		current, used []byte
		waited        bool
		want          recoveryAction
	}{
		{"refused credentials, lock free", old, old, false, recoverRenew},
		{"refused credentials, renewal ran meanwhile", old, old, true, recoverGiveUp},
		{"renewed elsewhere", renewed, old, false, recoverWithCurrent},
		{"renewed by the renewal waited for", renewed, old, true, recoverWithCurrent},
	} {
		if got := recoveryStep(tc.current, tc.used, tc.waited); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

// awaitRefreshLock waits for a renewal holding the lock, cluster lock or
// node mutex, and gives up when the caller leaves.
func TestAwaitRefreshLock(t *testing.T) {
	locker := testutil.NewMemLocker()
	for name, s := range map[string]*Service{"cluster": {d: Deps{Locker: locker}}, "node": {}} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			release, waited, err := s.awaitRefreshLock(ctx, 7)
			if err != nil || release == nil || waited {
				t.Fatalf("free lock: release %v waited %v err %v", release != nil, waited, err)
			}
			time.AfterFunc(150*time.Millisecond, release)
			start := time.Now()
			again, waited, err := s.awaitRefreshLock(ctx, 7)
			if err != nil || again == nil || !waited || time.Since(start) < 100*time.Millisecond {
				t.Fatalf("held lock: release %v waited %v err %v after %v", again != nil, waited, err, time.Since(start))
			}
			cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
			defer cancel()
			if r, waited, err := s.awaitRefreshLock(cctx, 7); r != nil || !waited || err != nil {
				t.Fatalf("caller left: release %v waited %v err %v", r != nil, waited, err)
			}
			again()
		})
	}
}

// storedCreds is account id's credentials as Load reads them, the bytes a
// refused attempt hands RecoverCredentials.
func (e *env) storedCreds(id int64) json.RawMessage {
	e.t.Helper()
	acc, err := e.svc.Load(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return acc.Credentials
}

// recoveryFixture is an account of a type declaring refresh, far from its
// expiry, and the token endpoint its renewals go to.
func recoveryFixture(t *testing.T) (*env, *tokenUpstream, int64) {
	e := setup(t)
	up := newTokenUpstream(t)
	e.plat.refreshURL = up.srv.URL + "/token"
	e.gen.types[0].Type.Refresh = &manifest.AccountRefresh{}
	return e, up, e.mkOAuthAccount("acc", "rt-1", 5*time.Hour)
}

// expectRecover runs RecoverCredentials on id with used and checks its
// answer and the upstream calls made so far.
func (e *env) expectRecover(up *tokenUpstream, id int64, used json.RawMessage, want bool, calls int32) {
	e.t.Helper()
	ok, err := e.svc.RecoverCredentials(context.Background(), id, used)
	if err != nil || ok != want || up.calls.Load() != calls {
		e.t.Fatalf("recover: %v %v (want %v), upstream calls %d (want %d)", ok, err, want, up.calls.Load(), calls)
	}
}

// A 401 renews the credentials whatever their expiry, as a 401 recovery;
// the same refused credentials again (another request's attempt) have
// changed meanwhile and are retried as they are.
func TestRecoverCredentials(t *testing.T) {
	e, up, id := recoveryFixture(t)
	refused := e.storedCreds(id)
	e.expectRecover(up, id, refused, true, 1)
	if c := e.creds(id); c["access_token"] != "at-2" {
		t.Fatalf("credentials after recovery: %v", c)
	}
	if got := e.storedCreds(id); string(got) == string(refused) {
		t.Fatal("Load still serves the refused credentials")
	}
	if !slices.Equal(e.plat.reasons, []pluginv1.BuildRefreshRequestRequest_Reason{reasonUnauthorized}) {
		t.Fatalf("reasons = %v", e.plat.reasons)
	}
	e.expectRecover(up, id, refused, true, 1)
}

// A refusal for good is recorded and answers false; the same credentials
// are not tried again.
func TestRecoverCredentialsRefused(t *testing.T) {
	e, up, id := recoveryFixture(t)
	up.failStatus.Store(http.StatusBadRequest)
	current := e.storedCreds(id)
	e.expectRecover(up, id, current, false, 1)
	e.expectRecover(up, id, current, false, 1)
}

// recoverWhileHeld runs RecoverCredentials on id while the test holds the
// account's refresh lock, calls during, releases the lock and returns the
// answer.
func (e *env) recoverWhileHeld(id int64, during func()) bool {
	e.t.Helper()
	ctx := context.Background()
	release, held, err := e.svc.lockRefresh(ctx, id)
	if err != nil || !held {
		e.t.Fatalf("lock: %v %v", held, err)
	}
	used := e.storedCreds(id)
	done := make(chan bool, 1)
	go func() {
		ok, err := e.svc.RecoverCredentials(ctx, id, used)
		if err != nil {
			e.t.Error(err)
		}
		done <- ok
	}()
	time.Sleep(150 * time.Millisecond)
	during()
	release()
	return <-done
}

// A renewal holding the lock is waited for and not repeated: what it left
// is retried when it changed the credentials, given up when not.
func TestRecoverCredentialsWaitsForTheLock(t *testing.T) {
	e, up, id := recoveryFixture(t)
	if e.recoverWhileHeld(id, func() {}) {
		t.Fatal("a renewal that left the credentials as they were was retried")
	}
	renewed := e.creds(id)
	renewed["access_token"] = "at-elsewhere"
	if !e.recoverWhileHeld(id, func() { e.setCreds(id, renewed) }) {
		t.Fatal("credentials renewed by the lock holder were not retried")
	}
	if up.calls.Load() != 0 {
		t.Fatalf("the refresh was repeated: %d upstream calls", up.calls.Load())
	}
}

// awaitReasons waits until the plugin was told want, in order.
func (e *env) awaitReasons(want ...refreshReason) {
	e.t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		e.plat.mu.Lock()
		got := slices.Clone(e.plat.reasons)
		e.plat.mu.Unlock()
		if slices.Equal(got, want) {
			return
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("reasons = %v, want %v", got, want)
		}
	}
}

// Every renewal tells the plugin why it runs: the sweep, an administrator,
// a save of credentials already due.
func TestRefreshReasons(t *testing.T) {
	e, _, id := recoveryFixture(t)
	due := e.creds(id)
	due["expires_at"] = 1
	e.setCreds(id, due)
	if n, err := e.svc.SweepRefresh(context.Background()); err != nil || n != 1 {
		t.Fatalf("sweep: %d %v", n, err)
	}
	if code, body := e.do("POST", fmt.Sprintf("/accounts/%d/refresh-credentials", id), nil); code != http.StatusOK {
		t.Fatalf("forced refresh: %d %v", code, body)
	}
	saved := e.creds(id)
	saved["expires_at"] = 1 // the renewal re-reads the stored credentials
	e.setCreds(id, saved)
	secret, _ := json.Marshal(saved)
	bt, _ := e.svc.accountType("anthropic", "apikey")
	e.svc.refreshSaved(context.Background(), id, bt, secret)
	e.awaitReasons(reasonScheduled, reasonForced, reasonSaved)
}
