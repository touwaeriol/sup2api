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
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// A renewal attempt counts when it is later than the one before the wait.
func TestLastAttemptAdvanced(t *testing.T) {
	t1, t2 := time.Unix(100, 0), time.Unix(200, 0)
	for _, tc := range []struct {
		name        string
		before, now *time.Time
		want        bool
	}{
		{"none either time", nil, nil, false},
		{"first attempt meanwhile", nil, &t2, true},
		{"later attempt meanwhile", &t1, &t2, true},
		{"no attempt meanwhile", &t1, &t1, false},
	} {
		if got := (&lastAttempt{At: tc.before}).advancedTo(&lastAttempt{At: tc.now}); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

// awaitRefreshLock waits for a renewal holding the lock, cluster lock or
// node mutex, and gives up when the caller leaves.
func TestAwaitRefreshLock(t *testing.T) {
	for _, cluster := range []bool{true, false} {
		t.Run(fmt.Sprintf("cluster=%v", cluster), func(t *testing.T) {
			s := setup(t).svc
			if cluster {
				s.d.Locker = testutil.NewMemLocker()
			}
			expectAwait(t, s)
		})
	}
}

// expectAwait checks awaitRefreshLock on s: free at once, after a release,
// abandoned when the caller leaves.
func expectAwait(t *testing.T, s *Service) {
	ctx := context.Background()
	release, before, err := s.awaitRefreshLock(ctx, 7)
	if err != nil || release == nil || before != nil {
		t.Fatalf("free lock: release %v before %v err %v", release != nil, before, err)
	}
	time.AfterFunc(150*time.Millisecond, release)
	start := time.Now()
	again, before, err := s.awaitRefreshLock(ctx, 7)
	if err != nil || again == nil || before == nil || time.Since(start) < 100*time.Millisecond {
		t.Fatalf("held lock: release %v before %v err %v after %v", again != nil, before, err, time.Since(start))
	}
	cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if r, _, err := s.awaitRefreshLock(cctx, 7); r != nil || err != nil {
		t.Fatalf("caller left: release %v err %v", r != nil, err)
	}
	again()
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

// A holder of the lock is waited for. When it renewed the credentials they
// are retried as they are; when it attempted a renewal of them that failed,
// the recovery gives up; when it only held the lock (read them, found them
// not due), they are renewed now.
func TestRecoverCredentialsWaitsForTheLock(t *testing.T) {
	e, up, id := recoveryFixture(t)
	if !e.recoverWhileHeld(id, func() {}) || up.calls.Load() != 1 {
		t.Fatalf("credentials nobody renewed: upstream calls %d", up.calls.Load())
	}
	failed := func() {
		st := store.RefreshState{AccountID: id, ErrorType: store.RefreshTransient, Error: "down"}
		if err := store.RecordAccountRefresh(context.Background(), e.db.Gorm, st); err != nil {
			t.Error(err)
		}
	}
	if e.recoverWhileHeld(id, failed) {
		t.Fatal("credentials a renewal just failed on were renewed again")
	}
	renewed := e.creds(id)
	renewed["access_token"] = "at-elsewhere"
	if !e.recoverWhileHeld(id, func() { e.setCreds(id, renewed) }) {
		t.Fatal("credentials renewed by the lock holder were not retried")
	}
	if up.calls.Load() != 1 {
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
