package conformance

import (
	"context"
	"net/http"
	"net/url"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

func errUnavailable(msg string) error { return status.Error(codes.Unavailable, msg) }

func hasScheme(raw string, schemes ...string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Host != "" && slices.Contains(schemes, u.Scheme)
}

func mib(n int) float64 { return float64(n) / (1 << 20) }

// blockedCall is the host of rule 9: the plugin's first relay or fetch
// blocks until its context ends (or the test releases it), and the plugin
// server reports when Execute returns on the plugin's side.
type blockedCall struct {
	entered, returned, released chan struct{}
	enter, ret, rel             sync.Once
}

func newBlockedCall() *blockedCall {
	return &blockedCall{entered: make(chan struct{}), returned: make(chan struct{}), released: make(chan struct{})}
}

func (b *blockedCall) block(ctx context.Context) error {
	b.enter.Do(func() { close(b.entered) })
	select {
	case <-ctx.Done():
		return status.FromContextError(ctx.Err()).Err()
	case <-b.released:
		return errUnavailable("conformance: released after the cancellation rule failed")
	}
}

func (b *blockedCall) relay(ctx context.Context, _ *pluginv1.UpstreamRequest) (*pluginv1.RelayUpstreamResponse, error) {
	return nil, b.block(ctx)
}

func (b *blockedCall) fetch(ctx context.Context, _ *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
	return nil, b.block(ctx)
}

// server sees Execute return on the plugin's side, which the caller's own
// cancelled call cannot tell.
func (b *blockedCall) server() []grpc.ServerOption {
	return []grpc.ServerOption{grpc.ChainUnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		defer func() {
			if strings.HasSuffix(info.FullMethod, "PlatformService/Execute") {
				b.ret.Do(func() { close(b.returned) })
			}
		}()
		return handler(ctx, req)
	})}
}

// release unblocks the host and waits for Execute to end, so no plugin
// call outlives the item.
func (b *blockedCall) release() {
	b.rel.Do(func() { close(b.released) })
	select {
	case <-b.returned:
	case <-time.After(executeTimeout):
	}
}

// allocationRuns is how many measured Executes rule 10 averages.
const allocationRuns = 2

// allocated is the bytes the process allocates per Execute of c on h.
func allocated(s *suite, h *pluginsdktest.Harness, c *Case) (int64, *pluginsdktest.Execution) {
	var ex *pluginsdktest.Execution
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	testing.AllocsPerRun(allocationRuns, func() { ex = s.run(h, c, nil) }) // plus one warm-up run
	runtime.ReadMemStats(&after)
	return int64(after.TotalAlloc-before.TotalAlloc) / (allocationRuns + 1), ex
}

// reference is the pass-through plugin rule 10 measures against: it relays
// the body it was given, in the form the request asks for.
type reference struct{}

func (reference) BuildUpstream(_ context.Context, in *pluginv1.ExecuteRequest) (*pluginv1.UpstreamRequest, error) {
	return pluginsdk.NewUpstream(http.MethodPost, "https://reference.conformance.invalid/").Body(in.GetBody()).Build()
}

func (reference) ExtractUsage(context.Context, *pluginv1.ExtractUsageRequest) (*pluginv1.UsageReport, error) {
	return &pluginv1.UsageReport{}, nil
}

func (reference) ParseTaskSubmission(context.Context, *pluginv1.ExtractUsageRequest) (*pluginv1.TaskSubmission, error) {
	return &pluginv1.TaskSubmission{UpstreamRefId: "reference"}, nil
}

func (reference) ClassifyError(context.Context, *pluginv1.ClassifyErrorRequest) (*pluginv1.ClassifyErrorResponse, error) {
	return &pluginv1.ClassifyErrorResponse{}, nil
}

func (reference) ValidateCredentials(context.Context, *pluginv1.ValidateCredentialsRequest) (*pluginv1.ValidateCredentialsResponse, error) {
	return &pluginv1.ValidateCredentialsResponse{}, nil
}

func (reference) BuildTestRequest(context.Context, *pluginv1.BuildTestRequestRequest) (*pluginv1.BuildTestRequestResponse, error) {
	return &pluginv1.BuildTestRequestResponse{}, nil
}
