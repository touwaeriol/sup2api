package grpcruntime

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/execfetch"
)

// checkUpstreamRequest is the part of validating a relay that needs nothing
// of the gateway: the request is there and leaves the transport's headers
// alone. Its body has no bound here: the client's body was admitted by the
// gateway's request body limit, and what the plugin makes of it is its own.
func checkUpstreamRequest(req *pluginv1.UpstreamRequest) error {
	if req == nil || req.GetUrl() == "" {
		return status.Error(codes.InvalidArgument, "request with a url is required")
	}
	for _, headers := range []map[string]string{req.GetHeaders(), req.GetDefaultHeaders()} {
		for name := range headers {
			if execfetch.RelayTransportHeaders[strings.ToLower(name)] {
				return status.Errorf(codes.InvalidArgument, "header %q is set by the host's transport", name)
			}
		}
	}
	return nil
}

// RelayUpstream takes the final upstream request of an execution. Without
// wait it is only checked and kept: platformAdapter.Execute hands it to the
// gateway once the plugin returned host_usage, so nothing is sent while the
// plugin is still in its call. With wait the gateway sends and relays now
// and the plugin gets the observation.
func (h *hostServer) RelayUpstream(ctx context.Context, in *pluginv1.RelayUpstreamRequest) (out *pluginv1.RelayUpstreamResponse, err error) {
	sc, err := h.enter(in.GetExecutionToken())
	if err != nil {
		return nil, err
	}
	defer func() {
		if recover() != nil {
			out, err = nil, status.Error(codes.Internal, "relay failed internally")
		}
		sc.leave()
	}()
	switch {
	case !sc.relay:
		return nil, status.Error(codes.PermissionDenied, "RelayUpstream is for Execute calls")
	case sc.relayed:
		return nil, status.Error(codes.FailedPrecondition, "upstream already relayed")
	case in.GetWait() && (!sc.wait || sc.client.Relay == nil):
		return nil, status.Error(codes.FailedPrecondition, "a waiting relay needs extract_usage or task_submission")
	}
	req := in.GetRequest()
	if err := checkUpstreamRequest(req); err != nil {
		return nil, err
	}
	cctx, cancel := sc.hostContext(ctx)
	defer cancel()
	if sc.client.Check != nil {
		if err := sc.client.Check(cctx, req); err != nil {
			return nil, status.Error(codes.InvalidArgument, refusal(err))
		}
	}
	sc.relayed = true
	if !in.GetWait() {
		sc.parked = req
		return &pluginv1.RelayUpstreamResponse{}, nil
	}
	out, err = sc.client.Relay(cctx, req)
	if err != nil {
		return nil, status.Error(codes.Unavailable, refusal(err))
	}
	if (out.GetObservation() == nil) == (out.GetError() == nil) {
		return nil, status.Error(codes.Internal, "relay ended without exactly one of observation and error")
	}
	return out, nil
}

// refusal is the text of a host refusal for the plugin: the message of a
// core error, without its internal cause.
func refusal(err error) string {
	var ce *core.Error
	if errors.As(err, &ce) {
		return ce.Message
	}
	return err.Error()
}

// ExecuteHTTP is the fetch form: one bounded exchange for the plugin, during
// an Execute, a Monitor or a console route call (CONTRACTS §117).
func (h *hostServer) ExecuteHTTP(ctx context.Context, in *pluginv1.ExecutionHTTPRequest) (out *pluginv1.ExecutionHTTPResponse, err error) {
	sc, err := h.enter(in.GetExecutionToken())
	if err != nil {
		return nil, err
	}
	defer func() {
		if recover() != nil {
			out, err = nil, status.Error(codes.Internal, "execution HTTP failed internally")
		}
		sc.leave()
	}()
	if sc.client.Fetch == nil {
		return nil, status.Error(codes.FailedPrecondition, "this call has no fetch capability")
	}
	if err := h.requireFetchHost(ctx, sc, in.GetUrl()); err != nil {
		return nil, err
	}
	if (in.GetProxyTicket() != "") != sc.route {
		return nil, status.Error(codes.PermissionDenied, "a fetch carries a proxy ticket in a console route call, and only there")
	}
	cctx, cancel := sc.hostContext(ctx)
	defer cancel()
	out, err = sc.client.Fetch(cctx, in)
	if err != nil {
		return nil, toStatus(err)
	}
	if out == nil {
		return nil, status.Error(codes.Internal, "fetch returned no response")
	}
	if cctx.Err() == nil && out.GetStatus() >= 200 && out.GetStatus() <= 599 && out.GetTransportError() == "" {
		sc.evidence = true
	}
	return out, nil
}

// requireFetchHost accepts the http(s) URL of a fetch whose host the
// manifest allows for the account (its externalServices or the account's own
// upstream addresses; a route call has no account, so only externalServices)
// and the administrator's plugin network access lets the plugin reach
// (core.CheckPluginUpstream, CONTRACTS §112.17, §113). Whether the address
// may be reached at all (the SSRF policy, the account's proxy) is the
// fetch's to decide, not this check's.
func (h *hostServer) requireFetchHost(ctx context.Context, sc *callScope, raw string) error {
	if _, err := httpHost(raw); err != nil {
		return err
	}
	if ce := core.CheckPluginUpstream(ctx, h.i.rt.o.NetworkAccess, h.key(), h.i.pkg.Manifest, raw,
		sc.account.GetType(), sc.account.GetSettingsJson(), sc.account.GetCredentialsJson()); ce != nil {
		return toStatus(ce)
	}
	return nil
}

// Execute runs PlatformService.Execute for one attempt (core.ExecutePlugin).
func (a platformAdapter) Execute(ctx context.Context, in *pluginv1.ExecuteRequest, client core.ExecutionClient) (res core.ExecutionResult, err error) {
	if in == nil {
		return res, errors.New("Execute requires a request")
	}
	// Execute may span a whole upstream exchange (a relay that waits), so
	// it takes no call slot and has no timeout at all: long streams never
	// hold the slots of the request-path RPCs (CONTRACTS §43.1), and the
	// plugin's own work is not timed either. A hung plugin ends with ctx
	// (the client leaving, the upstream's timeouts on a relay) or with its
	// process.
	err = a.i.call(ctx, classNoSlot, 0, func(ctx context.Context, p *proc) error {
		sc := &callScope{client: client, account: in.GetAccount(), relay: true, wait: in.GetExtractUsage() || in.GetTaskSubmission()}
		out, err := withScope(ctx, p, sc, func(ctx context.Context, token string) (*pluginv1.ExecuteResponse, error) {
			req := shallowClone(in)
			req.ExecutionToken = token
			return p.platform.Execute(ctx, req)
		})
		if err != nil {
			return err
		}
		if out == nil {
			return errors.New("Execute returned no response")
		}
		res.Response = out
		// A parked request leaves the runtime only with host_usage: any
		// other answer, like any error above, leaves it unsent.
		if out.GetHostUsage() != nil {
			res.Relay = sc.parked
		}
		return nil
	})
	return res, err
}

var _ core.ExecutePlugin = platformAdapter{}
