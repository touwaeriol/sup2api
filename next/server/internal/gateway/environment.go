package gateway

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// environment resolves the environment variables (CONTRACTS §74.3) of an
// attempt on ref; a nil ref resolves the request before an account is chosen.
func (c *call) environment(ref *core.AccountRef) core.Environment {
	var global, group, account core.EnvironmentValues
	if c.g.d.Environment != nil {
		global = c.g.d.Environment.GlobalEnvironment(c.c.Request.Context())
	}
	if c.principal != nil {
		group = c.principal.Group.Environment
	}
	if ref != nil {
		account = ref.Environment
	}
	return core.ResolveEnvironment(global, group, account)
}

// The causes an upstream request is canceled with when a timeout fires, so
// the usage record names it.
var (
	errRelayTimeout      = errors.New("upstream request exceeded relay_timeout_seconds")
	errStreamIdleTimeout = errors.New("upstream stream exceeded streaming_idle_timeout_seconds")
	errHeaderTimeout     = errors.New("timeout waiting for upstream response headers")
)

// idleBody cancels the upstream request when a streamed response delivers
// nothing for the idle timeout (streaming_idle_timeout_seconds). The timer
// only runs while a Read waits on the upstream, so a slow client is not
// counted as an idle upstream.
type idleBody struct {
	io.ReadCloser
	timer *time.Timer
	idle  time.Duration
}

// defaultStreamIdle bounds a silent upstream when nothing configures it, so a
// stuck stream cannot hold its concurrency slot forever.
const defaultStreamIdle = 180 * time.Second

// streamIdle is the longest gap between two reads of a streamed response.
func (c *call) streamIdle() time.Duration {
	if idle := c.env.Duration(core.EnvVarStreamingIdleTimeoutSeconds); idle > 0 {
		return idle
	}
	return defaultStreamIdle
}

// upstreamTimeouts are the response header and total timeouts of an
// upstream exchange (0 = none): the environment's, the header one else the
// gateway's wait for a stream or not.
func (c *call) upstreamTimeouts(stream bool) (header, total time.Duration) {
	header = c.g.headerWait(stream)
	if d := c.env.Duration(core.EnvVarResponseHeaderTimeoutSeconds); d > 0 {
		header = d
	}
	return header, c.env.Duration(core.EnvVarRelayTimeoutSeconds)
}

func newIdleBody(body io.ReadCloser, idle time.Duration, cancel context.CancelCauseFunc) *idleBody {
	timer := time.AfterFunc(idle, func() { cancel(errStreamIdleTimeout) })
	timer.Stop()
	return &idleBody{ReadCloser: body, idle: idle, timer: timer}
}

func (b *idleBody) Read(p []byte) (int, error) {
	b.timer.Reset(b.idle)
	defer b.timer.Stop()
	return b.ReadCloser.Read(p)
}

func (b *idleBody) Close() error {
	b.timer.Stop()
	return b.ReadCloser.Close()
}
