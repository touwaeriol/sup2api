package pluginsdk_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
)

// The manifest asks for host permission "lock", which also checks that the
// id is known to the manifest validator.
const lockManifest = `{
  "apiVersion": 1, "key": "lk", "version": "0.1.0", "publisher": "test", "runtime": "grpc",
  "hostPermissions": [{"id": "lock", "reason": {"en": "x", "zh": "x"}}]
}`

type locker struct{ host pluginsdk.Host }

func (l *locker) Init(_ context.Context, h pluginsdk.Host) error { l.host = h; return nil }

// startLocker runs a plugin on fh (a new FakeHost when nil) and returns its
// Locks. validity > 0 makes the fake report short validity so Keep renews
// every validity/2.
func startLocker(t *testing.T, fh *pluginsdktest.FakeHost, validity time.Duration) (pluginsdk.Locks, *pluginsdktest.FakeHost) {
	t.Helper()
	if fh == nil {
		fh = pluginsdktest.NewFakeHost()
	}
	fh.Locks.SetValidity(validity)
	p := &locker{}
	pluginsdktest.Start(t, p, pluginsdktest.Options{Host: fh, Manifest: []byte(lockManifest)})
	return p.host.Locks(), fh
}

func mustAcquire(t *testing.T, ls pluginsdk.Locks, name string) *pluginsdk.Lock {
	t.Helper()
	l, ok, err := ls.TryAcquire(context.Background(), name, time.Second)
	if err != nil || !ok {
		t.Fatalf("TryAcquire(%s) = %v, %v", name, ok, err)
	}
	return l
}

func TestLockTryAcquireRelease(t *testing.T) {
	ls, fh := startLocker(t, nil, 0)
	ctx := context.Background()

	start := time.Now()
	l := mustAcquire(t, ls, "sync/accounts:1")
	if l.Name() != "sync/accounts:1" {
		t.Fatalf("Name = %q", l.Name())
	}
	// Validity is counted from before the request: start+1s <= Until <= now+1s.
	if u := l.Until(); u.Before(start.Add(time.Second)) || u.After(time.Now().Add(time.Second)) {
		t.Fatalf("Until = %v, want about start+1s", u)
	}

	// Held: a second attempt is "no" without an error.
	if l2, ok, err := ls.TryAcquire(ctx, "sync/accounts:1", time.Second); ok || err != nil || l2 != nil {
		t.Fatalf("second TryAcquire = %v, %v, %v", l2, ok, err)
	}
	// Other names are independent.
	mustAcquire(t, ls, "sync/accounts:2")

	if ok, err := l.Renew(ctx); !ok || err != nil {
		t.Fatalf("Renew = %v, %v", ok, err)
	}

	// Release, then idempotent; the lock is free again and no longer ours.
	if err := l.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if err := l.Release(ctx); err != nil {
		t.Fatalf("second Release: %v", err)
	}
	if fh.Locks.Held("sync/accounts:1") {
		t.Fatal("still held after Release")
	}
	if !l.Until().IsZero() {
		t.Fatal("Until must be zero after Release")
	}
	if ok, err := l.Renew(ctx); ok || err != nil {
		t.Fatalf("Renew after Release = %v, %v", ok, err)
	}
	l3 := mustAcquire(t, ls, "sync/accounts:1")
	// The old handle's Release must not free the new holder's lock.
	if err := l.Release(ctx); err != nil || !fh.Locks.Held("sync/accounts:1") {
		t.Fatalf("stale Release: err=%v held=%v", err, fh.Locks.Held("sync/accounts:1"))
	}
	_ = l3.Release(ctx)
}

func TestLockRenewAfterLost(t *testing.T) {
	ls, fh := startLocker(t, nil, 0)
	ctx := context.Background()
	l := mustAcquire(t, ls, "a")

	fh.Locks.Expire("a")
	if ok, err := l.Renew(ctx); ok || err != nil {
		t.Fatalf("Renew after expiry = %v, %v, want false, nil", ok, err)
	}
	if !l.Until().IsZero() {
		t.Fatal("Until must be zero once lost")
	}
	// Someone else takes it; the lost handle stays lost and cannot release it.
	other := mustAcquire(t, ls, "a")
	if ok, err := l.Renew(ctx); ok || err != nil {
		t.Fatalf("Renew of lost lock = %v, %v", ok, err)
	}
	if err := l.Release(ctx); err != nil || !fh.Locks.Held("a") {
		t.Fatalf("Release of lost lock: err=%v held=%v", err, fh.Locks.Held("a"))
	}
	if ok, err := other.Renew(ctx); !ok || err != nil {
		t.Fatalf("new holder Renew = %v, %v", ok, err)
	}
}

func TestLockValidation(t *testing.T) {
	ls, fh := startLocker(t, nil, 0)
	// A host error proves validation happens before any request is sent.
	fh.SetLockErr(status.Error(codes.Unavailable, "redis down"))
	ctx := context.Background()
	for _, name := range []string{"", "a b", "ü", "a*", strings.Repeat("x", 129)} {
		if _, _, err := ls.TryAcquire(ctx, name, time.Second); status.Code(err) != codes.InvalidArgument {
			t.Errorf("name %q: err = %v", name, err)
		}
	}
	for _, ttl := range []time.Duration{0, -time.Second, 999 * time.Millisecond, pluginsdk.MaxLockTTL + time.Millisecond} {
		if _, _, err := ls.TryAcquire(ctx, "ok", ttl); status.Code(err) != codes.InvalidArgument {
			t.Errorf("ttl %s: err = %v", ttl, err)
		}
	}
	ran, err := ls.WithLock(ctx, "bad name", time.Second, func(context.Context) error { return nil })
	if ran || status.Code(err) != codes.InvalidArgument {
		t.Errorf("WithLock bad name = %v, %v", ran, err)
	}
	// Valid edges reach the host.
	for _, name := range []string{"a", strings.Repeat("x", 128), "A-z_0.9:/"} {
		if _, _, err := ls.TryAcquire(ctx, name, pluginsdk.MaxLockTTL); status.Code(err) != codes.Unavailable {
			t.Errorf("name %q: err = %v, want the host's", name, err)
		}
	}
	if !pluginsdk.ValidLockName("jobs/sync:1") || pluginsdk.ValidLockName("") {
		t.Error("ValidLockName")
	}

	// Host errors pass through; WithLock does not run fn.
	fh.SetLockErr(status.Error(codes.PermissionDenied, "no lock grant"))
	if _, _, err := ls.TryAcquire(ctx, "ok", time.Second); status.Code(err) != codes.PermissionDenied {
		t.Errorf("host error: %v", err)
	}
	ran, err = ls.WithLock(ctx, "ok", time.Second, func(context.Context) error { t.Error("fn ran"); return nil })
	if ran || status.Code(err) != codes.PermissionDenied {
		t.Errorf("WithLock host error = %v, %v", ran, err)
	}
}

func TestLockKeepRenews(t *testing.T) {
	ls, fh := startLocker(t, nil, 100*time.Millisecond)
	l := mustAcquire(t, ls, "k")
	first := l.Until()
	kctx, stop := l.Keep(context.Background())
	defer stop()
	time.Sleep(350 * time.Millisecond)
	if kctx.Err() != nil {
		t.Fatalf("Keep cancelled: %v", context.Cause(kctx))
	}
	if !l.Until().After(first.Add(150 * time.Millisecond)) {
		t.Fatalf("Until %v not extended past %v", l.Until(), first)
	}
	if !fh.Locks.Held("k") {
		t.Fatal("lock not held")
	}
	stop()
	if kctx.Err() == nil || errors.Is(context.Cause(kctx), pluginsdk.ErrLockLost) {
		t.Fatalf("after stop: err=%v cause=%v", kctx.Err(), context.Cause(kctx))
	}
}

func TestLockKeepLosesOnExpiry(t *testing.T) {
	ls, fh := startLocker(t, nil, 100*time.Millisecond)
	l := mustAcquire(t, ls, "k")
	kctx, stop := l.Keep(context.Background())
	defer stop()
	fh.Locks.Expire("k")
	waitLost(t, kctx)
}

// Renewals with an unknown outcome are retried until the validity runs out,
// then the lock counts as lost.
func TestLockKeepLosesWhenHostUnavailable(t *testing.T) {
	ls, fh := startLocker(t, nil, 150*time.Millisecond)
	l := mustAcquire(t, ls, "k")
	kctx, stop := l.Keep(context.Background())
	defer stop()
	fh.SetLockErr(status.Error(codes.Unavailable, "redis down"))
	start := time.Now()
	waitLost(t, kctx)
	if time.Since(start) < 50*time.Millisecond {
		t.Fatal("lost before the validity ran out")
	}
}

// Release while Keep runs: Keep notices at its next renewal.
func TestLockReleaseDuringKeep(t *testing.T) {
	ls, fh := startLocker(t, nil, 100*time.Millisecond)
	l := mustAcquire(t, ls, "k")
	kctx, stop := l.Keep(context.Background())
	defer stop()
	if err := l.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitLost(t, kctx)
	if fh.Locks.Held("k") {
		t.Fatal("renewal after Release re-took the lock")
	}
}

func TestLockKeepStopsWithParent(t *testing.T) {
	ls, _ := startLocker(t, nil, 100*time.Millisecond)
	l := mustAcquire(t, ls, "k")
	parent, cancel := context.WithCancel(context.Background())
	kctx, stop := l.Keep(parent)
	defer stop()
	cancel()
	<-kctx.Done()
	if errors.Is(context.Cause(kctx), pluginsdk.ErrLockLost) {
		t.Fatal("parent cancellation reported as lost lock")
	}
	stop() // waits for the renewal goroutine
	before := l.Until()
	time.Sleep(150 * time.Millisecond)
	if !l.Until().Equal(before) {
		t.Fatal("renewed after the parent context ended")
	}
}

func TestWithLock(t *testing.T) {
	ls, fh := startLocker(t, nil, 100*time.Millisecond)
	ctx := context.Background()

	// Runs fn under the lock and releases it.
	ran, err := ls.WithLock(ctx, "w", time.Second, func(ctx context.Context) error {
		if !fh.Locks.Held("w") {
			t.Error("fn runs without the lock")
		}
		// Contended: fn does not run.
		inner, ierr := ls.WithLock(ctx, "w", time.Second, func(context.Context) error { t.Error("nested fn ran"); return nil })
		if inner || ierr != nil {
			t.Errorf("contended WithLock = %v, %v", inner, ierr)
		}
		time.Sleep(200 * time.Millisecond) // a few renewals
		return ctx.Err()
	})
	if !ran || err != nil {
		t.Fatalf("WithLock = %v, %v", ran, err)
	}
	if fh.Locks.Held("w") {
		t.Fatal("not released")
	}

	// fn's error is returned; the lock is released anyway.
	boom := errors.New("boom")
	if ran, err := ls.WithLock(ctx, "w", time.Second, func(context.Context) error { return boom }); !ran || err != boom {
		t.Fatalf("WithLock error = %v, %v", ran, err)
	}
	if fh.Locks.Held("w") {
		t.Fatal("not released after an error")
	}

	// Lost while running: fn's ctx is cancelled with ErrLockLost.
	ran, err = ls.WithLock(ctx, "w", time.Second, func(ctx context.Context) error {
		fh.Locks.Expire("w")
		waitLost(t, ctx)
		return ctx.Err()
	})
	if !ran || !errors.Is(err, context.Canceled) {
		t.Fatalf("lost WithLock = %v, %v", ran, err)
	}
	// fn ignoring the loss and returning nil still learns about it.
	ran, err = ls.WithLock(ctx, "w", time.Second, func(ctx context.Context) error {
		fh.Locks.Expire("w")
		<-ctx.Done()
		return nil
	})
	if !ran || !errors.Is(err, pluginsdk.ErrLockLost) {
		t.Fatalf("lost WithLock with nil fn error = %v, %v", ran, err)
	}

	// The caller's ctx ending cancels fn (not as a lost lock) and the lock is
	// still released.
	pctx, cancel := context.WithCancel(ctx)
	ran, err = ls.WithLock(pctx, "w", time.Second, func(ctx context.Context) error {
		cancel()
		<-ctx.Done()
		if errors.Is(context.Cause(ctx), pluginsdk.ErrLockLost) {
			t.Error("caller cancellation reported as lost lock")
		}
		return ctx.Err()
	})
	if !ran || !errors.Is(err, context.Canceled) || fh.Locks.Held("w") {
		t.Fatalf("cancelled WithLock = %v, %v, held=%v", ran, err, fh.Locks.Held("w"))
	}

	// A panicking fn still releases the lock.
	func() {
		defer func() { _ = recover() }()
		_, _ = ls.WithLock(ctx, "w", time.Second, func(context.Context) error { panic("x") })
	}()
	if fh.Locks.Held("w") {
		t.Fatal("not released after a panic")
	}
}

// Two nodes (two FakeHosts sharing one lock table) never run the guarded
// section at the same time, and the lock moves between them.
func TestWithLockTwoNodes(t *testing.T) {
	lsA, hostA := startLocker(t, nil, 100*time.Millisecond)
	hostB := pluginsdktest.NewFakeHost()
	hostB.Locks = hostA.Locks
	lsB, _ := startLocker(t, hostB, 100*time.Millisecond)

	// A holds it: B cannot take it; after A releases, B can.
	la := mustAcquire(t, lsA, "shared")
	if _, ok, err := lsB.TryAcquire(context.Background(), "shared", time.Second); ok || err != nil {
		t.Fatalf("node B TryAcquire while A holds = %v, %v", ok, err)
	}
	if err := la.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	lb := mustAcquire(t, lsB, "shared")
	_ = lb.Release(context.Background())

	var inside, overlaps, runs atomic.Int32
	var wg sync.WaitGroup
	deadline := time.Now().Add(300 * time.Millisecond)
	for i := range 8 {
		ls := lsA
		if i%2 == 1 {
			ls = lsB
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				ran, err := ls.WithLock(context.Background(), "shared", time.Second, func(context.Context) error {
					if inside.Add(1) != 1 {
						overlaps.Add(1)
					}
					time.Sleep(2 * time.Millisecond)
					inside.Add(-1)
					return nil
				})
				if err != nil {
					t.Error(err)
					return
				}
				if ran {
					runs.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if overlaps.Load() != 0 {
		t.Fatalf("%d overlapping runs", overlaps.Load())
	}
	if runs.Load() == 0 {
		t.Fatal("nothing ran")
	}
}

// The plugin chooses the owner token: fresh and well-formed for every attempt,
// and it is what the host stores.
func TestLockTokenChosenByPlugin(t *testing.T) {
	ls, fh := startLocker(t, nil, 0)
	ctx := context.Background()
	seen := map[string]bool{}
	for range 5 {
		l := mustAcquire(t, ls, "t")
		tok := fh.Locks.Holder("t")
		if !protocol.ValidLockToken(tok) || len(tok) != 22 {
			t.Fatalf("token %q is not 16 bytes of URL-safe base64", tok)
		}
		if seen[tok] {
			t.Fatalf("token %q reused", tok)
		}
		seen[tok] = true
		if err := l.Release(ctx); err != nil || fh.Locks.Held("t") {
			t.Fatalf("Release: err=%v held=%v", err, fh.Locks.Held("t"))
		}
	}
}

// An acquire whose reply is lost took the lock anyway: TryAcquire returns
// the error but releases the lock with its own token, so other nodes are
// not blocked for a ttl.
func TestLockAcquireUnknownOutcomeReleases(t *testing.T) {
	ctx := context.Background()
	for _, c := range []codes.Code{codes.Unavailable, codes.DeadlineExceeded, codes.Internal} {
		ls, fh := startLocker(t, nil, 0)
		other := pluginsdktest.NewFakeHost()
		other.Locks = fh.Locks
		lsOther, _ := startLocker(t, other, 0)

		fh.SetLockAcquireLostReply(status.Error(c, "reply lost"))
		l, ok, err := ls.TryAcquire(ctx, "u", pluginsdk.MaxLockTTL)
		if l != nil || ok || status.Code(err) != c {
			t.Fatalf("%v: TryAcquire = %v, %v, %v", c, l, ok, err)
		}
		if fh.Locks.Held("u") {
			t.Fatalf("%v: the lock taken behind the lost reply was not released", c)
		}
		// Another node can take it right away, not after the 5m ttl.
		lo := mustAcquire(t, lsOther, "u")
		// A second lost acquire (contended this time) must not free the
		// other holder's lock: the undo is a compare-and-delete.
		if _, _, err := ls.TryAcquire(ctx, "u", time.Second); status.Code(err) != c {
			t.Fatalf("%v: contended TryAcquire err = %v", c, err)
		}
		if !fh.Locks.Held("u") {
			t.Fatalf("%v: the undo released another holder's lock", c)
		}
		_ = lo.Release(ctx)
	}

	// Refusals are definite: nothing to undo, and the undo is not attempted
	// (with the grant missing it would be refused too).
	ls, fh := startLocker(t, nil, 0)
	fh.SetLockAcquireLostReply(status.Error(codes.PermissionDenied, "no grant"))
	if _, _, err := ls.TryAcquire(ctx, "p", time.Second); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("refused TryAcquire err = %v", err)
	}
	// The fake did take it (LockAcquireLostReply acts after the table), which
	// shows no release was sent for a definite refusal.
	if !fh.Locks.Held("p") {
		t.Fatal("a definite refusal was followed by a release")
	}
}

// The fake reproduces the host's rules for tokens: format checked, and a
// token reused for a lock it already holds releases that lock.
func TestFakeHostLockTokens(t *testing.T) {
	fh := pluginsdktest.NewFakeHost()
	ctx := context.Background()
	for _, tok := range []string{"", "short", strings.Repeat("a", 129), "has space 0123456789", "plus+slash/0123456789"} {
		if _, err := fh.LockAcquire(ctx, &pluginv1.LockAcquireRequest{Name: "n", TtlMs: 1000, Token: tok}); status.Code(err) != codes.InvalidArgument {
			t.Errorf("acquire token %q: %v", tok, err)
		}
		if _, err := fh.LockRenew(ctx, &pluginv1.LockRenewRequest{Name: "n", TtlMs: 1000, Token: tok}); status.Code(err) != codes.InvalidArgument {
			t.Errorf("renew token %q: %v", tok, err)
		}
		if _, err := fh.LockRelease(ctx, &pluginv1.LockReleaseRequest{Name: "n", Token: tok}); status.Code(err) != codes.InvalidArgument {
			t.Errorf("release token %q: %v", tok, err)
		}
	}
	tok := strings.Repeat("A", 16)
	for _, ms := range []int64{-1, 0, 999, 300001} {
		if _, err := fh.LockAcquire(ctx, &pluginv1.LockAcquireRequest{Name: "n", TtlMs: ms, Token: tok}); status.Code(err) != codes.InvalidArgument {
			t.Errorf("acquire ttl_ms %d: %v", ms, err)
		}
		if _, err := fh.LockRenew(ctx, &pluginv1.LockRenewRequest{Name: "n", TtlMs: ms, Token: tok}); status.Code(err) != codes.InvalidArgument {
			t.Errorf("renew ttl_ms %d: %v", ms, err)
		}
	}
	if r, err := fh.LockAcquire(ctx, &pluginv1.LockAcquireRequest{Name: "n", TtlMs: 1000, Token: tok}); err != nil || !r.GetAcquired() || r.GetValidMs() != 1000 {
		t.Fatalf("acquire = %v, %v", r, err)
	}
	if r, err := fh.LockAcquire(ctx, &pluginv1.LockAcquireRequest{Name: "n", TtlMs: 1000, Token: tok}); err != nil || r.GetAcquired() {
		t.Fatalf("re-acquire with the same token = %v, %v", r, err)
	}
	if fh.Locks.Held("n") {
		t.Fatal("re-acquire with the held token must release it, as on the host")
	}
}

// The fake checks in the host's order: the grant (a PERMISSION_DENIED
// LockErr) before the arguments, availability (any other LockErr) after.
func TestFakeHostLockErrOrder(t *testing.T) {
	fh := pluginsdktest.NewFakeHost()
	ctx := context.Background()
	call := func(name string, ttlMs int64, tok string) []error {
		_, e1 := fh.LockAcquire(ctx, &pluginv1.LockAcquireRequest{Name: name, TtlMs: ttlMs, Token: tok})
		_, e2 := fh.LockRenew(ctx, &pluginv1.LockRenewRequest{Name: name, TtlMs: ttlMs, Token: tok})
		_, e3 := fh.LockRelease(ctx, &pluginv1.LockReleaseRequest{Name: name, Token: tok})
		return []error{e1, e2, e3}
	}
	tok := strings.Repeat("A", 16)
	bad := []struct {
		name  string
		ttlMs int64
		tok   string
	}{{"bad name", 1000, tok}, {"n", 1000, "short"}}
	check := func(label string, errs []error, want codes.Code) {
		t.Helper()
		for i, err := range errs {
			if status.Code(err) != want {
				t.Errorf("%s: call %d err = %v, want %v", label, i, err, want)
			}
		}
	}

	fh.SetLockErr(status.Error(codes.PermissionDenied, "no lock grant"))
	for _, b := range bad {
		check("denied, "+b.name+"/"+b.tok, call(b.name, b.ttlMs, b.tok), codes.PermissionDenied)
	}
	if _, err := fh.LockAcquire(ctx, &pluginv1.LockAcquireRequest{Name: "n", TtlMs: 0, Token: tok}); status.Code(err) != codes.PermissionDenied {
		t.Errorf("denied, bad ttl: %v", err)
	}

	fh.SetLockErr(status.Error(codes.Unavailable, "redis down"))
	for _, b := range bad {
		check("unavailable, "+b.name+"/"+b.tok, call(b.name, b.ttlMs, b.tok), codes.InvalidArgument)
	}
	check("unavailable, valid", call("n", 1000, tok), codes.Unavailable)
}

// The host took the lock but reported valid_ms 0 (its Redis answered after
// the validity ran out): TryAcquire gives it back and reports "not
// acquired", and WithLock does not run fn.
func TestLockAcquireExpiredOnArrival(t *testing.T) {
	ls, fh := startLocker(t, nil, time.Microsecond) // valid_ms is reported as 0
	ctx := context.Background()

	l, ok, err := ls.TryAcquire(ctx, "e", time.Second)
	if l != nil || ok || err != nil {
		t.Fatalf("TryAcquire = %v, %v, %v, want nil, false, nil", l, ok, err)
	}
	// The fake stored it for the full ttl: not held means TryAcquire released
	// it, so other nodes are not blocked for a ttl.
	if fh.Locks.Held("e") {
		t.Fatal("the expired-on-arrival lock was not released")
	}

	ran, err := ls.WithLock(ctx, "e", time.Second, func(context.Context) error { t.Error("fn ran"); return nil })
	if ran || err != nil {
		t.Fatalf("WithLock = %v, %v, want false, nil", ran, err)
	}
	if fh.Locks.Held("e") {
		t.Fatal("WithLock left the lock held")
	}
}

func waitLost(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-ctx.Done():
		if !errors.Is(context.Cause(ctx), pluginsdk.ErrLockLost) {
			t.Fatalf("cause = %v, want ErrLockLost", context.Cause(ctx))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("lock loss not noticed")
	}
}
