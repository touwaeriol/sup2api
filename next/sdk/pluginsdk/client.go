package pluginsdk

import (
	"context"
	"math"
	"sync/atomic"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

// maxMessageBytes is gRPC's hard limit of one message, the receive limit of
// every plugin server and of the calls back to the host. Plugins set no
// size limit: the gateway's request body limit is the host's to enforce,
// and it can change while the plugin runs (design §8.2).
const maxMessageBytes = math.MaxInt32

const (
	kindExecute = "Execute"
	kindMonitor = "Monitor"
	kindHTTP    = "HandleHTTP"
)

// execution is the authority of one Execute, Monitor or console HandleHTTP
// call: the host's
// token and what the call may still do with it. It lives in the call's
// context and is void once the call returns (the context is cancelled).
type execution struct {
	client pluginv1.HostServiceClient
	token  string
	kind   string
	// mayWait: the request set extract_usage or task_submission, the only
	// executions the host lets relay and wait.
	mayWait bool
	relayed atomic.Bool
	waited  atomic.Bool
}

type executionKey struct{}

func withExecution(ctx context.Context, ex *execution) context.Context {
	return context.WithValue(ctx, executionKey{}, ex)
}

// scopedExecution returns the execution of ctx when it is one of kinds.
func scopedExecution(ctx context.Context, operation string, kinds ...string) (*execution, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	ex, _ := ctx.Value(executionKey{}).(*execution)
	if ex == nil || ex.client == nil || ex.token == "" {
		return nil, status.Errorf(codes.FailedPrecondition, "%s needs the context of the Execute, Monitor or console route call it is made in", operation)
	}
	for _, kind := range kinds {
		if ex.kind == kind {
			return ex, nil
		}
	}
	return nil, status.Errorf(codes.FailedPrecondition, "%s is not available in %s", operation, ex.kind)
}

// Relay hands the final upstream request of this execution to the host
// without waiting. The host only validates it and answers at once; it sends
// the request, relays the answer to the client and reads the usage by the
// declared rules after Execute has returned host_usage (ReportHostUsage). An
// Execute that fails after Relay leaves the request unsent and the host
// moves to another account.
//
// ctx is the context of the Execute call or a child of it. An execution
// relays at most once, in either form; a request the host refuses
// (INVALID_ARGUMENT) does not count. request is sent as it is and not
// copied: do not change it while the call runs.
func Relay(ctx context.Context, request *pluginv1.UpstreamRequest) error {
	_, err := relay(ctx, "Relay", request, false)
	return err
}

// RelayAndWait hands the final upstream request to the host, which sends it
// now, relays the answer to the client and returns when the exchange has
// ended. The answer holds exactly one of: observation, what the host read
// of a 2xx answer, which the plugin reads into ReportUsage or ReportTask; error, the
// facts of a failed exchange the plugin classifies (ReportClassification). Only for
// executions whose request sets extract_usage or task_submission; the rest
// is as for Relay.
func RelayAndWait(ctx context.Context, request *pluginv1.UpstreamRequest) (*pluginv1.RelayUpstreamResponse, error) {
	return relay(ctx, "RelayAndWait", request, true)
}

func relay(ctx context.Context, operation string, request *pluginv1.UpstreamRequest, wait bool) (*pluginv1.RelayUpstreamResponse, error) {
	ex, err := scopedExecution(ctx, operation, kindExecute)
	if err != nil {
		return nil, err
	}
	if wait && !ex.mayWait {
		return nil, status.Error(codes.FailedPrecondition, "RelayAndWait is only for executions with extract_usage or task_submission; use Relay and return ReportHostUsage")
	}
	if err := ValidateUpstreamRequest(request); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if !ex.relayed.CompareAndSwap(false, true) {
		return nil, status.Error(codes.FailedPrecondition, "an execution relays at most once: this one already has")
	}
	out, err := ex.client.RelayUpstream(ctx, &pluginv1.RelayUpstreamRequest{ExecutionToken: ex.token, Request: request, Wait: wait}, grpc.MaxCallRecvMsgSize(maxMessageBytes))
	if err != nil {
		if status.Code(err) == codes.InvalidArgument {
			ex.relayed.Store(false) // refused by the host: not relayed
		}
		return nil, err
	}
	if !wait {
		return &pluginv1.RelayUpstreamResponse{}, nil
	}
	ex.waited.Store(true)
	if (out.GetObservation() == nil) == (out.GetError() == nil) {
		return nil, status.Error(codes.Internal, "host answered a relay that waited with neither or both of observation and error")
	}
	return out, nil
}

// Fetch makes one non-streaming HTTP exchange through the host and
// returns the answer to the plugin: the auxiliary requests of an Execute
// (token exchange, upload, preflight) and the upstream query of a Monitor.
// The host sends it through the account's proxy behind its SSRF guard,
// without the operator's header settings and without following redirects.
// The URL's host must be one of the manifest's externalServices (or a
// subdomain of one) or the host of one of the call's account's own upstream
// addresses, the values of its type's upstreamFields
// (manifest.Manifest.UpstreamHostAllowed). The host neither counts the fetches
// of a call nor cuts their answers: the answer body comes back whole. A
// transport failure is not an error: it comes back in
// transport_error with status 0. No net permission is needed.
//
// In a console route call (a handler of a route of a signed-in user) Fetch
// serves the authorization of an account not saved yet (CONTRACTS §117):
// there is no account, so only externalServices are reachable, and request
// must carry the ProxyTicket the console issued for the form's proxy
// choice; the host sends the request through that proxy (or directly when
// the operator chose none). Execute and Monitor fetches carry no ticket.
//
// ctx is the context of the Execute, Monitor or route call or a child of
// it. The SDK supplies the execution token; one set on request is ignored.
func Fetch(ctx context.Context, request *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
	ex, err := scopedExecution(ctx, "Fetch", kindExecute, kindMonitor, kindHTTP)
	if err != nil {
		return nil, err
	}
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "HTTP request is required")
	}
	// A shallow copy: the caller's message keeps its token field and a large
	// body is not duplicated.
	return ex.client.ExecuteHTTP(ctx, &pluginv1.ExecutionHTTPRequest{
		ExecutionToken: ex.token,
		Method:         request.GetMethod(),
		Url:            request.GetUrl(),
		Headers:        request.GetHeaders(),
		Body:           request.GetBody(),
		ProxyTicket:    request.GetProxyTicket(),
	}, grpc.MaxCallRecvMsgSize(maxMessageBytes))
}
