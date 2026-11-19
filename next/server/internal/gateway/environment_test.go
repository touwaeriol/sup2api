package gateway

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

type fakeEnvironment struct{ global core.EnvironmentValues }

func (f fakeEnvironment) GlobalEnvironment(context.Context) core.EnvironmentValues { return f.global }
func (f fakeEnvironment) ResolveEnvironment(context.Context, int64, int64) (core.Environment, error) {
	return core.ResolveEnvironment(f.global, nil, nil), nil
}

func withEnvironment(global, group core.EnvironmentValues) envOpt {
	return func(e *env) {
		p := e.auth.keys[testKey]
		p.Group.Environment = group
		e.auth.keys[testKey] = p
		e.envGlobal = global
	}
}

// Plugins called before an account is chosen see the group and global
// layers; an attempt adds the layer of the account as loaded for it, not as
// it was when the candidates were listed.
func TestEnvironmentReachesPluginMeta(t *testing.T) {
	e := newEnv(t, withEnvironment(core.EnvironmentValues{core.EnvVarRelayTimeoutSeconds: 300},
		core.EnvironmentValues{core.EnvVarTaskPollMaxFailures: 10, core.EnvVarRelayTimeoutSeconds: 120}))
	e.accounts.set(1, func(acc *core.Account) { acc.Environment = core.EnvironmentValues{core.EnvVarRelayTimeoutSeconds: 60} })
	hook := &fakeHook{}
	e.addHook(guardHook(300, "open"), nil, hook)
	ranker := e.addRanker("guard", manifest.SchedulerRank{}, &fakeRanker{fn: func(context.Context, *pluginv1.RankAccountsRequest) ([]*pluginv1.RankedAccount, error) {
		e.accounts.set(1, func(acc *core.Account) { acc.Environment = core.EnvironmentValues{core.EnvVarRelayTimeoutSeconds: 90} })
		return nil, nil
	}})
	r := e.messages(body(testModel, false))
	require.Equal(t, 200, r.status, string(r.body))
	e.record()

	base := map[string]int64{core.EnvVarTaskPollMaxFailures: 10, core.EnvVarRelayTimeoutSeconds: 120}
	require.Equal(t, 1, hook.count())
	assert.Equal(t, base, hook.calls[0].GetMeta().GetEnvironment())
	assert.Equal(t, base, ranker.last().GetMeta().GetEnvironment())
	e.plat.mu.Lock()
	meta := e.plat.builds[0].GetMeta()
	e.plat.mu.Unlock()
	assert.Equal(t, map[string]int64{core.EnvVarTaskPollMaxFailures: 10, core.EnvVarRelayTimeoutSeconds: 90}, meta.GetEnvironment())
}

// The idle timer runs only while a read waits on the upstream: a reader that
// pauses longer than the timeout between reads is not cut.
func TestIdleBodyCountsOnlyUpstreamWaits(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	pr, pw := io.Pipe()
	b := newIdleBody(pr, 50*time.Millisecond, func(err error) { cancel(err); _ = pw.CloseWithError(err) })
	defer func() { _ = b.Close() }()

	go func() { _, _ = pw.Write([]byte("a")) }()
	buf := make([]byte, 1)
	n, err := b.Read(buf)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	time.Sleep(150 * time.Millisecond) // the client is slow, the upstream is not waited on
	require.NoError(t, context.Cause(ctx))

	_, err = b.Read(buf) // the upstream sends nothing
	require.ErrorIs(t, err, errStreamIdleTimeout)
	require.ErrorIs(t, context.Cause(ctx), errStreamIdleTimeout)
}

// A timeout that cuts a stream mid-way is named in the usage record.
func TestEnvironmentTimeoutCutsStreamAndNamesIt(t *testing.T) {
	for _, tc := range []struct {
		key   string
		cause error
	}{
		{core.EnvVarRelayTimeoutSeconds, errRelayTimeout},
		{core.EnvVarStreamingIdleTimeoutSeconds, errStreamIdleTimeout},
	} {
		t.Run(tc.key, func(t *testing.T) {
			e := newEnv(t, withEnvironment(core.EnvironmentValues{tc.key: 1}, nil))
			hold := make(chan struct{})
			defer close(hold)
			e.up.set("acc-1", &upstreamRule{hold: hold})
			start := time.Now()
			e.messages(body(testModel, true))
			rec := e.record()
			require.Less(t, time.Since(start), 5*time.Second)
			require.False(t, rec.Success)
			require.Equal(t, "upstream stream interrupted: "+tc.cause.Error(), rec.ErrorMessage)
			require.Contains(t, rec.ErrorMessage, tc.key)
		})
	}
}

func TestEnvironmentResponseHeaderTimeout(t *testing.T) {
	e := newEnv(t, withEnvironment(core.EnvironmentValues{core.EnvVarResponseHeaderTimeoutSeconds: 1}, nil))
	for _, key := range []string{"acc-1", "acc-2", "acc-3"} {
		e.up.set(key, &upstreamRule{status: 200, delay: 3 * time.Second})
	}
	start := time.Now()
	r := e.messages(body(testModel, false))
	require.NotEqual(t, 200, r.status)
	// Three attempts, each cut after one second instead of waiting three.
	require.Less(t, time.Since(start), 6*time.Second)
}
