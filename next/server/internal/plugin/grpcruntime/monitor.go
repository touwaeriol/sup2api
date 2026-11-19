package grpcruntime

import (
	"context"
	"errors"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// Monitor runs PlatformService.Monitor once (core.MonitorPlugin): the plugin
// queries the upstream through fetch and the result is its return value.
func (a platformAdapter) Monitor(ctx context.Context, in *pluginv1.PollRequest, fetch core.ExecutionHTTP) (result *pluginv1.ReconcileResult, err error) {
	if !a.i.has(manifest.CapPlatformMonitor) || in == nil || fetch == nil {
		return nil, errors.New("Monitor requires platform.monitor.v1, an input and a fetch capability")
	}
	err = a.i.call(ctx, classBackground, TimeoutPoll, func(ctx context.Context, p *proc) error {
		sc := &callScope{client: core.ExecutionClient{Fetch: fetch}, account: in.GetAccount()}
		// A fetch finishing after Monitor returned cannot back its result:
		// that result was produced before its network evidence (withScope).
		out, err := withScope(ctx, p, sc, func(ctx context.Context, token string) (*pluginv1.MonitorResponse, error) {
			req := shallowClone(in)
			req.ExecutionToken = token
			return p.platform.Monitor(ctx, req)
		})
		if err != nil {
			return err
		}
		result = out.GetResult()
		switch {
		case result == nil:
			return errors.New("Monitor returned no result")
		case terminal(result) && !sc.evidence:
			result = nil
			return errors.New("Monitor returned a terminal result without a complete, successful execution HTTP response")
		}
		return nil
	})
	return result, err
}

// terminal reports whether result ends the observation of its task.
func terminal(result *pluginv1.ReconcileResult) bool {
	return result.GetState() != pluginv1.ReconcileResult_PENDING && result.GetState() != pluginv1.ReconcileResult_POLL_FAILED
}

var _ core.MonitorPlugin = platformAdapter{}
