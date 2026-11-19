package grpcruntime

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry/registrytest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

var lockGrant = registry.Grants{PermLock: json.RawMessage(`{}`)}

const (
	tokA = "token-a-0123456789abcdef"
	tokB = "token-b-0123456789abcdef"
)

// lockEnv is a hostServer for plugin key on locker (nil: no locker), without
// a process or a database, like accountsEnv.
func lockEnv(t *testing.T, key string, grants registry.Grants, locker core.TokenLocker) *hostServer {
	t.Helper()
	m := registrytest.Manifest(key, "1.0.0")
	pkg, err := registry.LoadPackage(t.TempDir(), registrytest.Package(t, m, []byte("bin"), nil), "", "unsigned")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pkg.Close() })
	rt := &Runtime{o: Options{Node: staticNode{"n", "b"}, Concurrency: Concurrency{Hot: 4, Console: 4, Background: 4}, DataDir: t.TempDir(), Locker: locker},
		log: slog.Default()}
	i := newInstance(rt, pkg, "", "", &settings{configJSON: "{}", grants: grants})
	return &hostServer{i: i}
}

func acquire(t *testing.T, h *hostServer, name, token string, ttlMs int64) *pluginv1.LockAcquireResponse {
	t.Helper()
	r, err := h.LockAcquire(context.Background(), &pluginv1.LockAcquireRequest{Name: name, Token: token, TtlMs: ttlMs})
	if err != nil {
		t.Fatalf("acquire %s: %v", name, err)
	}
	return r
}

func renew(t *testing.T, h *hostServer, name, token string, ttlMs int64) *pluginv1.LockRenewResponse {
	t.Helper()
	r, err := h.LockRenew(context.Background(), &pluginv1.LockRenewRequest{Name: name, Token: token, TtlMs: ttlMs})
	if err != nil {
		t.Fatalf("renew %s: %v", name, err)
	}
	return r
}

func release(t *testing.T, h *hostServer, name, token string) {
	t.Helper()
	if _, err := h.LockRelease(context.Background(), &pluginv1.LockReleaseRequest{Name: name, Token: token}); err != nil {
		t.Fatalf("release %s: %v", name, err)
	}
}

// lockCalls runs all three calls with the given arguments and returns their
// status codes.
func lockCalls(h *hostServer, name, token string, ttlMs int64) [3]codes.Code {
	ctx := context.Background()
	_, e1 := h.LockAcquire(ctx, &pluginv1.LockAcquireRequest{Name: name, Token: token, TtlMs: ttlMs})
	_, e2 := h.LockRenew(ctx, &pluginv1.LockRenewRequest{Name: name, Token: token, TtlMs: ttlMs})
	_, e3 := h.LockRelease(ctx, &pluginv1.LockReleaseRequest{Name: name, Token: token})
	return [3]codes.Code{status.Code(e1), status.Code(e2), status.Code(e3)}
}

func TestLockNeedsGrant(t *testing.T) {
	ml := testutil.NewMemLocker()
	h := lockEnv(t, "lk", registry.Grants{"kv": json.RawMessage(`{}`)}, ml)
	for i, c := range lockCalls(h, "x", tokA, 1000) {
		if c != codes.PermissionDenied {
			t.Errorf("call %d without the lock grant: %v", i, c)
		}
	}
	// Refused before validation: a bad request without the grant is still
	// PERMISSION_DENIED.
	if c := lockCalls(h, "", "", 0); c[0] != codes.PermissionDenied {
		t.Errorf("bad request without the grant: %v", c)
	}
	if ml.Holder(LockKey("lk", "x")) != "" {
		t.Fatal("a lock was taken without the grant")
	}
}

func TestLockValidation(t *testing.T) {
	ml := testutil.NewMemLocker()
	h := lockEnv(t, "lk", lockGrant, ml)
	for _, name := range []string{"", "a b", "ü", "a*", strings.Repeat("x", 129)} {
		for i, c := range lockCalls(h, name, tokA, 1000) {
			if c != codes.InvalidArgument {
				t.Errorf("name %q call %d: %v", name, i, c)
			}
		}
	}
	for _, tok := range []string{"", "short", strings.Repeat("a", 15), strings.Repeat("a", 129), "with space 0123456789", "plus+0123456789abcdef", "pad=0123456789abcdef"} {
		for i, c := range lockCalls(h, "x", tok, 1000) {
			if c != codes.InvalidArgument {
				t.Errorf("token %q call %d: %v", tok, i, c)
			}
		}
	}
	// ttl: acquire and renew only (release has none). math.MaxInt64 would
	// overflow into range if converted to a Duration before the check.
	for _, ms := range []int64{0, -1, 999, 300001, math.MaxInt64, math.MaxInt64/int64(time.Millisecond) + 2000} {
		c := lockCalls(h, "x", tokA, ms)
		if c[0] != codes.InvalidArgument || c[1] != codes.InvalidArgument || c[2] != codes.OK {
			t.Errorf("ttl_ms %d: %v", ms, c)
		}
	}
	if ml.Holder(LockKey("lk", "x")) != "" {
		t.Fatal("an invalid request took a lock")
	}
	// Valid edges.
	for _, c := range []struct {
		name string
		ttl  int64
	}{{"a", 1000}, {strings.Repeat("x", 128), 300000}, {"A-z_0.9:/", 1000}} {
		if r := acquire(t, h, c.name, strings.Repeat("Z", 128), c.ttl); !r.GetAcquired() {
			t.Errorf("valid %q/%d not acquired", c.name, c.ttl)
		}
	}
}

func TestLockUnavailable(t *testing.T) {
	h := lockEnv(t, "lk", lockGrant, nil)
	for i, c := range lockCalls(h, "x", tokA, 1000) {
		if c != codes.Unavailable {
			t.Errorf("call %d without a locker: %v", i, c)
		}
	}
	h = lockEnv(t, "lk", lockGrant, failingLocker{})
	for i, c := range lockCalls(h, "x", tokA, 1000) {
		if c != codes.Unavailable {
			t.Errorf("call %d with redis down: %v", i, c)
		}
	}
}

func TestLockAcquireRenewRelease(t *testing.T) {
	ml := testutil.NewMemLocker()
	h := lockEnv(t, "lk", lockGrant, ml)
	key := LockKey("lk", "jobs/sync:1")
	if key != "plugin:lk:jobs/sync:1" {
		t.Fatalf("key = %q", key)
	}

	r := acquire(t, h, "jobs/sync:1", tokA, 10000)
	if !r.GetAcquired() || r.GetValidMs() <= 9000 || r.GetValidMs() > 10000 {
		t.Fatalf("acquire = %v", r)
	}
	if ml.Holder(key) != tokA {
		t.Fatalf("held with %q, want the plugin's token", ml.Holder(key))
	}
	// Contended within the same plugin (another node of it): no, not an error.
	if r := acquire(t, h, "jobs/sync:1", tokB, 10000); r.GetAcquired() || r.GetValidMs() != 0 {
		t.Fatalf("contended acquire = %v", r)
	}

	if r := renew(t, h, "jobs/sync:1", tokA, 20000); !r.GetHeld() || r.GetValidMs() <= 19000 || r.GetValidMs() > 20000 {
		t.Fatalf("renew = %v", r)
	}
	// Renew with another token: definitely not held, and not an error.
	if r := renew(t, h, "jobs/sync:1", tokB, 20000); r.GetHeld() {
		t.Fatalf("renew with another token = %v", r)
	}

	// Release with a wrong token leaves the holder alone.
	release(t, h, "jobs/sync:1", tokB)
	if ml.Holder(key) != tokA {
		t.Fatal("release with a wrong token freed the lock")
	}
	release(t, h, "jobs/sync:1", tokA)
	if ml.Holder(key) != "" {
		t.Fatal("release did not free the lock")
	}
	release(t, h, "jobs/sync:1", tokA) // idempotent
	// Someone else can take it now.
	if r := acquire(t, h, "jobs/sync:1", tokB, 1000); !r.GetAcquired() {
		t.Fatal("not acquirable after release")
	}
}

func TestLockRenewAfterExpiry(t *testing.T) {
	ml := testutil.NewMemLocker()
	h := lockEnv(t, "lk", lockGrant, ml)
	acquire(t, h, "x", tokA, 10000)
	ml.Expire(LockKey("lk", "x"))
	if r := renew(t, h, "x", tokA, 10000); r.GetHeld() || r.GetValidMs() != 0 {
		t.Fatalf("renew after expiry = %v", r)
	}
	if ml.Holder(LockKey("lk", "x")) != "" {
		t.Fatal("renew re-created an expired lock")
	}
}

// Two plugins using the same name never meet: the host prefixes the key.
func TestLockPluginsAreIsolated(t *testing.T) {
	ml := testutil.NewMemLocker()
	a := lockEnv(t, "plga", lockGrant, ml)
	b := lockEnv(t, "plgb", lockGrant, ml)
	if !acquire(t, a, "shared", tokA, 10000).GetAcquired() || !acquire(t, b, "shared", tokB, 10000).GetAcquired() {
		t.Fatal("same name in two plugins must be two locks")
	}
	// Even with the other plugin's token, b cannot touch a's lock.
	if renew(t, b, "shared", tokA, 10000).GetHeld() {
		t.Fatal("plugin b renewed plugin a's lock")
	}
	release(t, b, "shared", tokA)
	if ml.Holder(LockKey("plga", "shared")) != tokA || ml.Holder(LockKey("plgb", "shared")) != tokB {
		t.Fatal("plugin b released plugin a's lock")
	}
	// A name that looks like another plugin's prefix stays inside its own.
	if !acquire(t, a, "plgb:shared", tokA, 10000).GetAcquired() {
		t.Fatal("colon name")
	}
	if ml.Holder(LockKey("plgb", "shared")) != tokB {
		t.Fatal("a colon in the name reached another plugin's lock")
	}
}

// The documented trap of a reused token, at the host: the second acquire
// answers "not acquired" and the lock is gone.
func TestLockAcquireReusedTokenReleases(t *testing.T) {
	ml := testutil.NewMemLocker()
	h := lockEnv(t, "lk", lockGrant, ml)
	acquire(t, h, "x", tokA, 10000)
	if acquire(t, h, "x", tokA, 10000).GetAcquired() {
		t.Fatal("re-acquire with the same token")
	}
	if ml.Holder(LockKey("lk", "x")) != "" {
		t.Fatal("expected the reused token to release the lock (see proto)")
	}
}

// failingLocker is a TokenLocker whose Redis never answers.
type failingLocker struct{}

var errRedisDown = errors.New("redis down")

func (failingLocker) TryLock(context.Context, string, time.Duration) (core.Lock, bool, error) {
	return nil, false, errRedisDown
}
func (failingLocker) TryLockToken(context.Context, string, string, time.Duration) (core.Lock, bool, error) {
	return nil, false, errRedisDown
}
func (failingLocker) Resume(string, string, time.Duration) core.Lock { return failingLock{} }
func (failingLocker) ReleaseToken(context.Context, string, string) error {
	return errRedisDown
}

type failingLock struct{}

func (failingLock) Token() string                        { return "" }
func (failingLock) Until() time.Time                     { return time.Time{} }
func (failingLock) Extend(context.Context) (bool, error) { return false, errRedisDown }
func (failingLock) Release()                             {}
