package grpcruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry"
)

// Default call timeouts. The caller's context deadline applies when shorter.
const (
	TimeoutPlatformHot     = 2 * time.Second  // ClassifyError, ResolveModel, EstimateUsage
	TimeoutPlatformConsole = 10 * time.Second // ValidateCredentials, BuildTestRequest, ValidateSettings
	TimeoutHookDefault     = 300 * time.Millisecond
	TimeoutHookMax         = 30 * time.Second // CONTRACTS §20.1
	TimeoutHTTP            = 30 * time.Second
	TimeoutEvents          = 30 * time.Second
	TimeoutJobDefault      = 60 * time.Second
	TimeoutScheduler       = 200 * time.Millisecond
	TimeoutRankDefault     = 200 * time.Millisecond // manifest scheduler.rank.timeoutMs = 0
	TimeoutRankMax         = time.Second            // pkg.MaxRankTimeout
	TimeoutPoll            = 30 * time.Second
	TimeoutMigrateData     = 10 * time.Minute
)

// callClass selects the semaphore a call waits for (Options.Concurrency).
type callClass int

const (
	classHot callClass = iota
	classConsole
	classBackground
	// classNoSlot takes no slot: Execute, whose calls are not limited, and
	// the calls the SDK answers itself without plugin code
	// (SettingsChanged), which plugin work filling the other classes must
	// never hold up.
	classNoSlot
	numClasses
)

func newSemaphores(c Concurrency) [numClasses]chan struct{} {
	return [numClasses]chan struct{}{
		classHot:        make(chan struct{}, c.Hot),
		classConsole:    make(chan struct{}, c.Console),
		classBackground: make(chan struct{}, c.Background),
	}
}

// call runs fn against the current process with the concurrency limit of its
// class, the timeout and in-flight accounting. It fails fast with
// core.ErrPluginUnavailable when the instance cannot serve calls.
func (i *Instance) call(ctx context.Context, class callClass, timeout time.Duration, fn func(ctx context.Context, p *proc) error) error {
	if i.draining.Load() {
		return i.unavailable("draining")
	}
	i.inflight.Add(1)
	defer i.finish()
	if i.draining.Load() {
		return i.unavailable("draining")
	}
	p := i.proc.Load()
	if p == nil {
		s, _ := i.State()
		return i.unavailable(s)
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if sem := i.sems[class]; sem != nil {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return fmt.Errorf("plugin %s: waiting for a free call slot: %w", i.pkg.Key, ctx.Err())
		}
		defer func() { <-sem }()
	}
	return i.mapErr(fn(ctx, p))
}

// invoke is call for one unary RPC of a service client of the process.
func invoke[C, Req, Resp any](i *Instance, ctx context.Context, class callClass, timeout time.Duration,
	client func(*proc) C, rpc func(C, context.Context, Req, ...grpc.CallOption) (Resp, error), in Req) (out Resp, err error) {
	err = i.call(ctx, class, timeout, func(ctx context.Context, p *proc) (e error) {
		out, e = rpc(client(p), ctx, in)
		return
	})
	return
}

func pluginOf(p *proc) pluginv1.PluginServiceClient     { return p.plugin }
func platformOf(p *proc) pluginv1.PlatformServiceClient { return p.platform }
func hookOf(p *proc) pluginv1.HookServiceClient         { return p.hook }
func appOf(p *proc) pluginv1.AppServiceClient           { return p.app }
func httpOf(p *proc) pluginv1.HTTPServiceClient         { return p.http }
func schedOf(p *proc) pluginv1.SchedulerServiceClient   { return p.sched }

func (i *Instance) finish() {
	if i.inflight.Add(-1) == 0 && i.draining.Load() {
		i.idleOnce.Do(func() { close(i.idle) })
	}
}

func (i *Instance) unavailable(reason string) error {
	return core.ErrPluginUnavailable.WithDetails(map[string]any{"plugin_key": i.pkg.Key}).
		WithCause(fmt.Errorf("plugin %s@%s: %s", i.pkg.Key, i.pkg.Version, reason))
}

// mapErr converts gRPC status errors into core errors. Deadline and
// cancellation keep wrapping the context errors so callers can use errors.Is.
func (i *Instance) mapErr(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	msg := st.Message()
	switch st.Code() {
	case codes.DeadlineExceeded:
		return fmt.Errorf("plugin %s: %w", i.pkg.Key, context.DeadlineExceeded)
	case codes.Canceled:
		return fmt.Errorf("plugin %s: %w", i.pkg.Key, context.Canceled)
	case codes.Unavailable:
		return core.ErrPluginUnavailable.WithDetails(map[string]any{"plugin_key": i.pkg.Key}).WithCause(err)
	case codes.Unimplemented:
		return core.ErrPluginUnavailable.WithDetails(map[string]any{"plugin_key": i.pkg.Key}).
			WithMessage("plugin does not implement this call").WithCause(err)
	case codes.InvalidArgument, codes.OutOfRange:
		return core.ErrInvalidArgument.WithMessage(msg).WithCause(err)
	case codes.NotFound:
		return core.ErrNotFound.WithMessage(msg).WithCause(err)
	case codes.AlreadyExists, codes.Aborted:
		return core.ErrConflict.WithMessage(msg).WithCause(err)
	case codes.PermissionDenied:
		return core.ErrPermissionDenied.WithMessage(msg).WithCause(err)
	case codes.Unauthenticated:
		return core.ErrUnauthenticated.WithMessage(msg).WithCause(err)
	case codes.ResourceExhausted:
		return core.ErrRateLimited.WithMessage(msg).WithCause(err)
	}
	return core.ErrInternal.WithCause(fmt.Errorf("plugin %s: %s: %s", i.pkg.Key, st.Code(), msg))
}

// ------------------------------------------------------------------ capability accessors (registry.Extension)

func (i *Instance) has(c string) bool { return i.caps[c] }

func (i *Instance) Platform() core.PlatformPlugin {
	if !i.has(manifest.CapPlatformAdapter) {
		return nil
	}
	return platformAdapter{i}
}

func (i *Instance) Hook() core.HookPlugin {
	if !i.has(manifest.CapGatewayHook) {
		return nil
	}
	return hookAdapter{i}
}

func (i *Instance) App() core.AppPlugin {
	if !i.has(manifest.CapAppJobs) && !i.has(manifest.CapAppEvents) {
		return nil
	}
	return appAdapter{i}
}

func (i *Instance) HTTP() core.HTTPPlugin {
	if !i.has(manifest.CapHTTPRoutes) {
		return nil
	}
	return httpAdapter{i}
}

func (i *Instance) Scheduler() core.SchedulerPlugin {
	if !i.has(manifest.CapSchedulerRank) {
		return nil
	}
	return schedAdapter{i}
}

var _ registry.Extension = (*Instance)(nil)

type platformAdapter struct{ i *Instance }

func (a platformAdapter) ValidateCredentials(ctx context.Context, in *pluginv1.ValidateCredentialsRequest) (*pluginv1.ValidateCredentialsResponse, error) {
	return invoke(a.i, ctx, classConsole, TimeoutPlatformConsole, platformOf, pluginv1.PlatformServiceClient.ValidateCredentials, in)
}

func (a platformAdapter) ClassifyError(ctx context.Context, in *pluginv1.ClassifyErrorRequest) (*pluginv1.ClassifyErrorResponse, error) {
	return invoke(a.i, ctx, classHot, TimeoutPlatformHot, platformOf, pluginv1.PlatformServiceClient.ClassifyError, in)
}

func (a platformAdapter) BuildTestRequest(ctx context.Context, in *pluginv1.BuildTestRequestRequest) (*pluginv1.BuildTestRequestResponse, error) {
	return invoke(a.i, ctx, classConsole, TimeoutPlatformConsole, platformOf, pluginv1.PlatformServiceClient.BuildTestRequest, in)
}

func (a platformAdapter) BuildModelsRequest(ctx context.Context, in *pluginv1.BuildModelsRequestRequest) (*pluginv1.BuildModelsRequestResponse, error) {
	return invoke(a.i, ctx, classConsole, TimeoutPlatformConsole, platformOf, pluginv1.PlatformServiceClient.BuildModelsRequest, in)
}

// ResolveModel runs on the gateway hot path (before scheduling), so it uses
// the hot-path timeout like ClassifyError; the gateway applies its own,
// shorter deadline on top.
func (a platformAdapter) ResolveModel(ctx context.Context, in *pluginv1.ResolveModelRequest) (*pluginv1.ResolveModelResponse, error) {
	return invoke(a.i, ctx, classHot, TimeoutPlatformHot, platformOf, pluginv1.PlatformServiceClient.ResolveModel, in)
}

// EstimateUsage runs before the pre-charge of every priced request.
func (a platformAdapter) EstimateUsage(ctx context.Context, in *pluginv1.EstimateUsageRequest) (*pluginv1.UsageReport, error) {
	return invoke(a.i, ctx, classHot, TimeoutPlatformHot, platformOf, pluginv1.PlatformServiceClient.EstimateUsage, in)
}

// BuildQuotaRequest and ParseQuotaResponse run in the account module when
// the console asks for a stale quota snapshot (CONTRACTS §44): somebody is
// waiting, but not a gateway client, so they get the console budget.
func (a platformAdapter) BuildQuotaRequest(ctx context.Context, in *pluginv1.BuildQuotaRequestRequest) (*pluginv1.BuildQuotaRequestResponse, error) {
	return invoke(a.i, ctx, classConsole, TimeoutPlatformConsole, platformOf, pluginv1.PlatformServiceClient.BuildQuotaRequest, in)
}

func (a platformAdapter) CheckModelAvailability(ctx context.Context, in *pluginv1.CheckModelAvailabilityRequest) (*pluginv1.CheckModelAvailabilityResponse, error) {
	return invoke(a.i, ctx, classHot, TimeoutPlatformHot, platformOf, pluginv1.PlatformServiceClient.CheckModelAvailability, in)
}

var _ core.AccountModelAvailabilityPlugin = platformAdapter{}

func (a platformAdapter) ParseQuotaResponse(ctx context.Context, in *pluginv1.ParseQuotaResponseRequest) (*pluginv1.QuotaResult, error) {
	return invoke(a.i, ctx, classConsole, TimeoutPlatformConsole, platformOf, pluginv1.PlatformServiceClient.ParseQuotaResponse, in)
}

// BuildBalanceRequest and ParseBalanceResponse run in the account module
// when the console asks for a balance or the balance sweep finds it due
// (CONTRACTS §52): the console budget, as the quota query.
func (a platformAdapter) BuildBalanceRequest(ctx context.Context, in *pluginv1.BuildBalanceRequestRequest) (*pluginv1.BuildBalanceRequestResponse, error) {
	return invoke(a.i, ctx, classConsole, TimeoutPlatformConsole, platformOf, pluginv1.PlatformServiceClient.BuildBalanceRequest, in)
}

func (a platformAdapter) ParseBalanceResponse(ctx context.Context, in *pluginv1.ParseBalanceResponseRequest) (*pluginv1.ParseBalanceResponseResponse, error) {
	return invoke(a.i, ctx, classConsole, TimeoutPlatformConsole, platformOf, pluginv1.PlatformServiceClient.ParseBalanceResponse, in)
}

// BuildRefreshRequest and ParseRefreshResponse run in the account module's
// refresh sweep or on an administrator's request (CONTRACTS §48), never on
// the request path.
func (a platformAdapter) BuildRefreshRequest(ctx context.Context, in *pluginv1.BuildRefreshRequestRequest) (*pluginv1.BuildRefreshRequestResponse, error) {
	return invoke(a.i, ctx, classBackground, TimeoutPlatformConsole, platformOf, pluginv1.PlatformServiceClient.BuildRefreshRequest, in)
}

func (a platformAdapter) ParseRefreshResponse(ctx context.Context, in *pluginv1.ParseRefreshResponseRequest) (*pluginv1.RefreshResult, error) {
	return invoke(a.i, ctx, classBackground, TimeoutPlatformConsole, platformOf, pluginv1.PlatformServiceClient.ParseRefreshResponse, in)
}

type hookAdapter struct{ i *Instance }

func (a hookAdapter) OnGatewayRequest(ctx context.Context, in *pluginv1.GatewayRequestHookRequest) (*pluginv1.GatewayRequestHookResponse, error) {
	timeout := TimeoutHookDefault
	if h, ok := registry.HookByID(a.i.pkg.Manifest, in.GetHookId()); ok && h.TimeoutMs > 0 {
		timeout = time.Duration(h.TimeoutMs) * time.Millisecond
	}
	return invoke(a.i, ctx, classHot, min(timeout, TimeoutHookMax), hookOf, pluginv1.HookServiceClient.OnGatewayRequest, in)
}

type appAdapter struct{ i *Instance }

// RunJob is bounded by ctx when it has a deadline: the task framework sets
// the run's effective timeout there, which is the only one a runtime task
// (no manifest job) has and may be shorter than an old manifest's. Without a
// deadline the manifest timeout (default 60 s) applies.
func (a appAdapter) RunJob(ctx context.Context, in *pluginv1.RunJobRequest) (*pluginv1.RunJobResponse, error) {
	timeout := time.Duration(0)
	if _, ok := ctx.Deadline(); !ok {
		timeout = TimeoutJobDefault
		for _, j := range a.i.pkg.Manifest.Jobs {
			if j.ID == in.GetJobId() && j.TimeoutSec > 0 {
				timeout = time.Duration(j.TimeoutSec) * time.Second
			}
		}
	}
	return invoke(a.i, ctx, classBackground, timeout, appOf, pluginv1.AppServiceClient.RunJob, in)
}

func (a appAdapter) OnEvents(ctx context.Context, in *pluginv1.OnEventsRequest) (*pluginv1.OnEventsResponse, error) {
	return invoke(a.i, ctx, classBackground, TimeoutEvents, appOf, pluginv1.AppServiceClient.OnEvents, in)
}

type httpAdapter struct{ i *Instance }

// HandleHTTP records the caller, and its access key, for as long as the
// plugin serves the request: AccountScopeCheck only answers about a user the
// plugin is serving, and both checks are narrowed by the key (callerSet).
// A call by a user also gets an execution token for its fetches, which
// carry the proxy ticket of an account being authorized (CONTRACTS §117).
func (a httpAdapter) HandleHTTP(ctx context.Context, in *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	uid := in.GetCaller().GetUserId()
	defer a.i.callers.enter(ctx, uid)()
	if uid <= 0 {
		return invoke(a.i, ctx, classConsole, TimeoutHTTP, httpOf, pluginv1.HTTPServiceClient.HandleHTTP, in)
	}
	var out *pluginv1.HTTPResponse
	err := a.i.call(ctx, classConsole, TimeoutHTTP, func(ctx context.Context, p *proc) (err error) {
		sc := &callScope{client: core.ExecutionClient{Fetch: a.fetchWithProxyTicket}, route: true}
		out, err = withScope(ctx, p, sc, func(ctx context.Context, token string) (*pluginv1.HTTPResponse, error) {
			req := shallowClone(in)
			req.ExecutionToken = token
			return p.http.HandleHTTP(ctx, req)
		})
		return err
	})
	return out, err
}

// fetchWithProxyTicket is the fetch of a route call: the account module
// opens the ticket for the call's user (ctx) and sends through its proxy.
func (a httpAdapter) fetchWithProxyTicket(ctx context.Context, in *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
	f, ok := a.i.rt.o.Accounts.(core.ProxyTicketFetcher)
	if !ok {
		return nil, core.ErrUnavailable.WithMessage("proxy tickets are unavailable")
	}
	return f.FetchWithProxyTicket(ctx, a.i.pkg.Key, in)
}

type schedAdapter struct{ i *Instance }

func (a schedAdapter) RankAccounts(ctx context.Context, in *pluginv1.RankAccountsRequest) (*pluginv1.RankAccountsResponse, error) {
	timeout := TimeoutRankDefault
	if s := a.i.pkg.Manifest.Scheduler; s != nil && s.Rank != nil && s.Rank.TimeoutMs > 0 {
		timeout = time.Duration(s.Rank.TimeoutMs) * time.Millisecond
	}
	return invoke(a.i, ctx, classHot, min(timeout, TimeoutRankMax), schedOf, pluginv1.SchedulerServiceClient.RankAccounts, in)
}

// ------------------------------------------------------------------ helpers

func goos() string   { return runtime.GOOS }
func goarch() string { return runtime.GOARCH }

func fileSum(name string) (string, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(aa), filepath.Clean(bb))
	}
	return filepath.Clean(aa) == filepath.Clean(bb)
}
