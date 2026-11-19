package app

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Masterminds/semver/v3"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/install"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// fakeBuiltins answers BuiltinsReady with running and BuiltinsInstalled
// with installed, counts EnsureBuiltin rounds and hands every loop pass's
// BuiltinsInstalled call the rounds run before it.
type fakeBuiltins struct {
	running, installed atomic.Bool
	ensured            atomic.Int64
	passes             chan int64
}

func (f *fakeBuiltins) BuiltinRequirements(string) (map[string]install.BuiltinRequirement, error) {
	return map[string]install.BuiltinRequirement{"volcengine": {Version: semver.MustParse("1.0.0")}}, nil
}

func (f *fakeBuiltins) BuiltinsReady(context.Context, map[string]install.BuiltinRequirement, core.Generation) (bool, error) {
	return f.running.Load(), nil
}

func (f *fakeBuiltins) BuiltinsInstalled(context.Context, map[string]install.BuiltinRequirement) (bool, error) {
	installed := f.installed.Load()
	f.passes <- f.ensured.Load()
	return installed, nil
}

func (f *fakeBuiltins) EnsureBuiltin(context.Context, string, *slog.Logger) error {
	f.ensured.Add(1)
	return nil
}

type noRegistry struct{}

func (noRegistry) Current() core.Generation                       { return nil }
func (noRegistry) OnChange(func(core.Generation)) (cancel func()) { return func() {} }

// The bundled plugins gate a node's readiness at start only, and
// EnsureBuiltin runs only while the bundle is not installed. Once they ran,
// a rollout of one of them (an install-only plugin being enabled reads as
// not running on every node at once, from the shared plugin rows) must leave
// the node ready and start no install round: /healthz answering 503 on both
// nodes together made the load balancer refuse every request, and the
// round's uploads made an operator's plugin change answer 409.
func TestBuiltinConvergenceDuringLaterRollouts(t *testing.T) {
	f := &fakeBuiltins{passes: make(chan int64)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready, stop := builtinConvergence(ctx, f, testutil.NewMemLocker(), noRegistry{}, "builtin", slog.New(slog.NewTextHandler(io.Discard, nil)), false)
	defer stop(context.Background())

	// next waits for the loop's next pass, which has read its state, and
	// returns the install rounds run before it. A state set after one pass is
	// read by the next, so check measures the rounds of that pass.
	next := func() int64 {
		t.Helper()
		select {
		case n := <-f.passes:
			return n
		case <-time.After(30 * time.Second):
			t.Fatal("the loop made no pass")
		}
		return 0
	}
	check := func(what string, rounds int64, wantReady bool) {
		t.Helper()
		before := next()
		if after := next(); after-before != rounds || ready.Load() != wantReady {
			t.Fatalf("%s: %d install rounds, ready %v; want %d, %v", what, after-before, ready.Load(), rounds, wantReady)
		}
	}
	// Nothing installed yet: not ready, and the loop installs.
	check("before installation", 1, false)
	f.installed.Store(true)
	f.running.Store(true)
	check("installed and running", 0, true)
	// A rollout of a bundled plugin is in progress: still installed, not
	// running everywhere. The node stays ready, nothing is installed.
	f.running.Store(false)
	check("during a rollout", 0, true)
	// The bundle needs installing again (a failed first enable left a
	// bundled plugin installed and never enabled): the loop installs, the
	// node stays ready.
	f.installed.Store(false)
	check("not installed after start", 1, true)
}
