package pluginsdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"sync"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/egress"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
)

// SDKVersion is reported in GetInfoResponse.sdk_version.
const SDKVersion = "0.2.0"

// Build-time overrides, set by `sub2api-plugin build` with
//
//	-ldflags "-X github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk.buildKey=<key>
//	          -X github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk.buildVersion=<version>"
//
// They take precedence over WithInfo and WithManifest so one source tree can
// produce packages for several manifest versions (e.g. upgrade test builds).
var (
	buildKey     string
	buildVersion string
)

// StrictMode controls when the SDK installs the egress tunnel.
type StrictMode int

const (
	// StrictAuto installs the tunnel when the host sets
	// SUB2API_PLUGIN_STRICT_NETWORK=1 (default).
	StrictAuto StrictMode = iota
	// StrictAlways always installs the tunnel.
	StrictAlways
	// StrictNever never installs the tunnel (tests).
	StrictNever
)

// Option configures Serve and Register.
type Option func(*options)

type options struct {
	manifestRaw []byte
	key         string
	version     string
	strict      StrictMode
	poolSize    int32
	logger      hclog.Logger
}

// WithManifest supplies manifest.json (usually via go:embed); key, version
// and declared capabilities are read from it.
func WithManifest(raw []byte) Option { return func(o *options) { o.manifestRaw = raw } }

// WithInfo sets the plugin key and version explicitly.
func WithInfo(key, version string) Option {
	return func(o *options) { o.key, o.version = key, version }
}

// WithStrictNetwork overrides when the egress tunnel is installed.
func WithStrictNetwork(m StrictMode) Option { return func(o *options) { o.strict = m } }

// WithDBPoolSize sets the maximum number of Host.DB connections (default 4).
func WithDBPoolSize(n int32) Option { return func(o *options) { o.poolSize = n } }

// WithLogger sets the go-plugin logger (stderr JSON by default).
func WithLogger(l hclog.Logger) Option { return func(o *options) { o.logger = l } }

// HostDialer connects to the host services served on a go-plugin broker id.
type HostDialer func(brokerID uint32) (*grpc.ClientConn, error)

// Serve runs the plugin p under go-plugin and never returns. It exits the
// process with status 1 when p or the options are invalid.
func Serve(p any, opts ...Option) {
	o := buildOptions(opts)
	logger := o.logger
	if logger == nil {
		logger = hclog.New(&hclog.LoggerOptions{Output: os.Stderr, JSONFormat: true, Level: hclog.Info})
	}
	if _, err := newRuntime(p, o, nil); err != nil {
		fmt.Fprintln(os.Stderr, "pluginsdk:", err)
		os.Exit(1)
	}
	gp := &protocol.GRPCPlugin{Register: func(s *grpc.Server, broker *plugin.GRPCBroker) error {
		return Register(s, p, broker.Dial, opts...)
	}}
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: protocol.Handshake,
		Plugins:         protocol.PluginMap(gp),
		GRPCServer:      NewGRPCServer,
		Logger:          logger,
	})
}

// NewGRPCServer builds a gRPC server with the SDK interceptors (panic
// recovery) and the receive limit of every plugin: gRPC's maximum. A plugin
// states no size limit of its own. The one limit on what the host hands
// over is the gateway's request body limit, a system setting the host
// enforces before it calls. Serve uses it; tests and custom hosts may too.
func NewGRPCServer(opts []grpc.ServerOption) *grpc.Server {
	opts = append(opts, grpc.ChainUnaryInterceptor(recoverUnary), grpc.MaxRecvMsgSize(maxMessageBytes))
	return grpc.NewServer(opts...)
}

func recoverUnary(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "pluginsdk: panic in %s: %v\n%s", info.FullMethod, r, debug.Stack())
			err = status.Errorf(codes.Internal, "plugin panic: %v", r)
		}
	}()
	return handler(ctx, req)
}

// Register registers PluginService and every capability service p implements
// on s. dial connects to the host broker id announced in InitHost. Serve
// calls it; pluginsdktest calls it with an in-memory dialer.
func Register(s grpc.ServiceRegistrar, p any, dial HostDialer, opts ...Option) error {
	rt, err := newRuntime(p, buildOptions(opts), dial)
	if err != nil {
		return err
	}
	pluginv1.RegisterPluginServiceServer(s, rt)
	if v, ok := p.(Platform); ok {
		pluginv1.RegisterPlatformServiceServer(s, platformServer{impl: v, execute: rt.execute, runtime: rt})
	}
	if v, ok := p.(Hook); ok {
		pluginv1.RegisterHookServiceServer(s, hookServer{impl: v})
	}
	if v, ok := p.(Observer); ok {
		pluginv1.RegisterObservationServiceServer(s, observationServer{impl: v})
	}
	jr, hasJobs := p.(JobRunner)
	eh, hasEvents := p.(EventHandler)
	bh, hasBroadcast := p.(BroadcastHandler)
	if hasJobs || hasEvents || hasBroadcast {
		pluginv1.RegisterAppServiceServer(s, appServer{jobs: jr, events: eh, broadcast: bh})
	}
	if v, ok := p.(HTTP); ok {
		pluginv1.RegisterHTTPServiceServer(s, httpServer{impl: v, runtime: rt})
	}
	if rk, ok := p.(AccountRanker); ok {
		pluginv1.RegisterSchedulerServiceServer(s, schedulerServer{rank: rk})
	}
	if v, ok := p.(Migration); ok {
		pluginv1.RegisterMigrationServiceServer(s, migrationServer{impl: v})
	}
	if v, ok := p.(NotificationChannel); ok {
		pluginv1.RegisterNotificationServiceServer(s, notificationServer{impl: v})
	}
	if v, ok := p.(PaymentGateway); ok {
		pluginv1.RegisterPaymentGatewayServiceServer(s, paymentServer{impl: v})
	}
	return nil
}

func buildOptions(opts []Option) options {
	o := options{poolSize: 4}
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

// Capabilities lists the capability ids p implements. platform.tasks.v1
// and platform.websocket.v1 are not among them: they are promises about
// what Execute does on the endpoints of the manifest, reported when the
// manifest declares them.
func Capabilities(p any) []string {
	var caps []string
	if _, ok := p.(Platform); ok {
		caps = append(caps, manifest.CapPlatformAdapter)
	}
	if _, ok := p.(TaskMonitor); ok {
		caps = append(caps, manifest.CapPlatformMonitor)
	}
	if _, ok := p.(Hook); ok {
		caps = append(caps, manifest.CapGatewayHook)
	}
	if _, ok := p.(Observer); ok {
		caps = append(caps, manifest.CapGatewayObserve)
	}
	if _, ok := p.(JobRunner); ok {
		caps = append(caps, manifest.CapAppJobs)
	}
	if _, ok := p.(EventHandler); ok {
		caps = append(caps, manifest.CapAppEvents)
	}
	if _, ok := p.(BroadcastHandler); ok {
		caps = append(caps, manifest.CapAppBroadcast)
	}
	if _, ok := p.(HTTP); ok {
		caps = append(caps, manifest.CapHTTPRoutes)
	}
	if _, ok := p.(AccountRanker); ok {
		caps = append(caps, manifest.CapSchedulerRank)
	}
	if _, ok := p.(Migration); ok {
		caps = append(caps, manifest.CapMigrationData)
	}
	if _, ok := p.(NotificationChannel); ok {
		caps = append(caps, manifest.CapNotifyChannel)
	}
	if _, ok := p.(PaymentGateway); ok {
		caps = append(caps, manifest.CapPaymentGateway)
	}
	return caps
}

// ---------------------------------------------------------------- PluginService

type runtime struct {
	pluginv1.UnimplementedPluginServiceServer

	p            any
	execute      Executor // of a Platform
	opts         options
	dial         HostDialer
	key, version string
	capabilities []string

	mu     sync.Mutex
	host   *host
	conn   *grpc.ClientConn
	inited bool
}

func newRuntime(p any, o options, dial HostDialer) (*runtime, error) {
	if p == nil {
		return nil, errors.New("plugin is nil")
	}
	rt := &runtime{p: p, opts: o, dial: dial}
	if platform, ok := p.(Platform); ok {
		execute, err := executorOf(platform)
		if err != nil {
			return nil, err
		}
		rt.execute = execute
	}
	declared, err := rt.parseManifest()
	if err != nil {
		return nil, err
	}
	rt.resolveIdentity()
	if rt.key == "" || rt.version == "" {
		return nil, errors.New("plugin key/version unknown: use WithManifest or WithInfo")
	}
	if err := rt.resolveCapabilities(declared); err != nil {
		return nil, err
	}
	return rt, nil
}

// parseManifest reads the embedded manifest: identity, the declarative
// capabilities that shape the runtime, and the checks that a plugin
// implementing broadcast or tasks must agree with its manifest. It returns the
// declared capability set, nil when no manifest was supplied.
func (rt *runtime) parseManifest() (map[string]bool, error) {
	var declared map[string]bool
	o := rt.opts
	if len(o.manifestRaw) == 0 {
		return nil, nil
	}
	var m manifest.Manifest
	if err := json.Unmarshal(o.manifestRaw, &m); err != nil {
		return nil, fmt.Errorf("parse embedded manifest: %w", err)
	}
	rt.key, rt.version = m.Key, m.Version
	declared = map[string]bool{}
	for _, c := range m.Capabilities {
		declared[c.ID] = true
	}
	if err := checkBroadcastManifest(rt.p, &m, declared); err != nil {
		return nil, err
	}
	if err := checkTaskManifest(rt.p, &m, declared); err != nil {
		return nil, err
	}
	return declared, nil
}

// resolveIdentity applies the key/version overrides in precedence order:
// WithInfo and WithManifest, then the -ldflags build identity, which wins so
// one source tree can produce packages for several manifest versions.
func (rt *runtime) resolveIdentity() {
	if rt.opts.key != "" {
		rt.key = rt.opts.key
	}
	if rt.opts.version != "" {
		rt.version = rt.opts.version
	}
	if buildKey != "" {
		rt.key = buildKey
	}
	if buildVersion != "" {
		rt.version = buildVersion
	}
}

// resolveCapabilities collects the capabilities the plugin implements, limited
// to the ones the manifest declares (all of them when it declares none), and
// checks that each of them is backed by the interface it promises.
func (rt *runtime) resolveCapabilities(declared map[string]bool) error {
	_, platform := rt.p.(Platform)
	for _, c := range Capabilities(rt.p) {
		if declared != nil && !declared[c] {
			continue
		}
		if c == manifest.CapPlatformMonitor && !platform {
			return fmt.Errorf("%s requires Platform", c)
		}
		rt.capabilities = append(rt.capabilities, c)
	}
	// Promises about Execute, not services of their own.
	for _, c := range []string{manifest.CapPlatformTasks, manifest.CapPlatformWebSocket} {
		if !declared[c] {
			continue
		}
		if !platform {
			return fmt.Errorf("%s requires Platform", c)
		}
		rt.capabilities = append(rt.capabilities, c)
	}
	return nil
}

func (rt *runtime) GetInfo(context.Context, *pluginv1.GetInfoRequest) (*pluginv1.GetInfoResponse, error) {
	return &pluginv1.GetInfoResponse{
		PluginKey:       rt.key,
		Version:         rt.version,
		SdkVersion:      SDKVersion,
		ProtocolVersion: protocol.ProtocolVersion,
		Capabilities:    rt.capabilities,
	}, nil
}

func (rt *runtime) strictWanted() bool {
	switch rt.opts.strict {
	case StrictAlways:
		return true
	case StrictNever:
		return false
	default:
		return os.Getenv(protocol.EnvStrictNetwork) == "1"
	}
}

func (rt *runtime) InitHost(ctx context.Context, in *pluginv1.InitHostRequest) (*pluginv1.InitHostResponse, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.inited {
		return &pluginv1.InitHostResponse{Ready: true, Message: "already initialised"}, nil
	}
	if rt.dial == nil {
		return nil, status.Error(codes.FailedPrecondition, "no host dialer")
	}
	conn, err := rt.dial(in.GetHostBrokerId())
	if err != nil {
		return &pluginv1.InitHostResponse{Ready: false, Message: "dial host broker: " + err.Error()}, nil
	}
	hc := pluginv1.NewHostServiceClient(conn)
	ec := pluginv1.NewEgressServiceClient(conn)
	installed := false
	if rt.strictWanted() {
		if err := egress.Install(ec); err != nil {
			_ = conn.Close()
			return &pluginv1.InitHostResponse{Ready: false, Message: "install egress: " + err.Error()}, nil
		}
		installed = true
	}
	h := newHost(HostInfo{
		NodeID: in.GetNodeId(), BootID: in.GetBootId(),
		HostVersion: in.GetHostVersion(), HostAPIVersion: in.GetHostApiVersion(),
	}, hc, ec, installed, rt.opts.poolSize)
	if init, ok := rt.p.(Initializer); ok {
		if err := init.Init(ctx, h); err != nil {
			h.closeDB()
			_ = conn.Close()
			return &pluginv1.InitHostResponse{Ready: false, Message: err.Error()}, nil
		}
	}
	rt.host, rt.conn, rt.inited = h, conn, true
	return &pluginv1.InitHostResponse{Ready: true}, nil
}

func (rt *runtime) Configure(ctx context.Context, in *pluginv1.ConfigureRequest) (*pluginv1.ConfigureResponse, error) {
	c, ok := rt.p.(Configurer)
	if !ok {
		return &pluginv1.ConfigureResponse{}, nil
	}
	if err := c.Configure(ctx, configFromProto(in)); err != nil {
		return &pluginv1.ConfigureResponse{Errors: fieldErrorsOf(err)}, nil
	}
	return &pluginv1.ConfigureResponse{}, nil
}

func (rt *runtime) ValidateSettings(ctx context.Context, in *pluginv1.ValidateSettingsRequest) (*pluginv1.ValidateSettingsResponse, error) {
	v, ok := rt.p.(SettingsValidator)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "plugin does not validate its settings")
	}
	normalized, err := v.ValidateSettings(ctx, configFromProto(&pluginv1.ConfigureRequest{ConfigJson: in.GetConfigJson(), Grants: in.GetGrants()}))
	if err != nil {
		return &pluginv1.ValidateSettingsResponse{Errors: fieldErrorsOf(err)}, nil
	}
	return &pluginv1.ValidateSettingsResponse{NormalizedConfigJson: string(normalized)}, nil
}

// fieldErrorsOf is err as field errors: a FieldErrors value field by field,
// any other error on the whole object.
func fieldErrorsOf(err error) []*pluginv1.FieldError {
	if fe, ok := AsFieldErrors(err); ok {
		return fe
	}
	return []*pluginv1.FieldError{{Field: "", Code: "invalid", Message: err.Error()}}
}

func (rt *runtime) Health(ctx context.Context, _ *pluginv1.HealthRequest) (*pluginv1.HealthResponse, error) {
	rt.mu.Lock()
	inited := rt.inited
	rt.mu.Unlock()
	if !inited {
		return &pluginv1.HealthResponse{Healthy: false, Message: "host not initialised"}, nil
	}
	if hc, ok := rt.p.(HealthChecker); ok {
		return hc.Health(ctx)
	}
	return &pluginv1.HealthResponse{Healthy: true}, nil
}

func (rt *runtime) Shutdown(ctx context.Context, _ *pluginv1.ShutdownRequest) (*pluginv1.ShutdownResponse, error) {
	var err error
	if s, ok := rt.p.(Shutdowner); ok {
		err = s.Shutdown(ctx)
	}
	rt.mu.Lock()
	if rt.host != nil {
		rt.host.closeDB()
	}
	rt.mu.Unlock()
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &pluginv1.ShutdownResponse{}, nil
}

// ---------------------------------------------------------------- adapters

type platformServer struct {
	pluginv1.UnimplementedPlatformServiceServer
	impl    Platform
	execute Executor
	runtime *runtime
}

func (s platformServer) ValidateCredentials(ctx context.Context, in *pluginv1.ValidateCredentialsRequest) (*pluginv1.ValidateCredentialsResponse, error) {
	return s.impl.ValidateCredentials(ctx, in)
}
func (s platformServer) ClassifyError(ctx context.Context, in *pluginv1.ClassifyErrorRequest) (*pluginv1.ClassifyErrorResponse, error) {
	return s.impl.ClassifyError(ctx, in)
}
func (s platformServer) BuildTestRequest(ctx context.Context, in *pluginv1.BuildTestRequestRequest) (*pluginv1.BuildTestRequestResponse, error) {
	return s.impl.BuildTestRequest(ctx, in)
}
func (s platformServer) BuildModelsRequest(ctx context.Context, in *pluginv1.BuildModelsRequestRequest) (*pluginv1.BuildModelsRequestResponse, error) {
	if l, ok := s.impl.(ModelLister); ok {
		return l.BuildModelsRequest(ctx, in)
	}
	return nil, status.Error(codes.Unimplemented, "this account type cannot list models")
}

func (s platformServer) ResolveModel(ctx context.Context, in *pluginv1.ResolveModelRequest) (*pluginv1.ResolveModelResponse, error) {
	if r, ok := s.impl.(ModelResolver); ok {
		return r.ResolveModel(ctx, in)
	}
	return nil, status.Error(codes.Unimplemented, "this platform does not resolve models")
}

func (s platformServer) BuildQuotaRequest(ctx context.Context, in *pluginv1.BuildQuotaRequestRequest) (*pluginv1.BuildQuotaRequestResponse, error) {
	if q, ok := s.impl.(QuotaReader); ok {
		return q.BuildQuotaRequest(ctx, in)
	}
	return nil, status.Error(codes.Unimplemented, "this account type cannot query its quota")
}

func (s platformServer) CheckModelAvailability(ctx context.Context, in *pluginv1.CheckModelAvailabilityRequest) (*pluginv1.CheckModelAvailabilityResponse, error) {
	if checker, ok := s.impl.(ModelAvailabilityChecker); ok {
		return checker.CheckModelAvailability(ctx, in)
	}
	return nil, status.Error(codes.Unimplemented, "this account type does not check model availability")
}

func (s platformServer) ParseQuotaResponse(ctx context.Context, in *pluginv1.ParseQuotaResponseRequest) (*pluginv1.QuotaResult, error) {
	if q, ok := s.impl.(QuotaReader); ok {
		return q.ParseQuotaResponse(ctx, in)
	}
	return nil, status.Error(codes.Unimplemented, "this account type cannot query its quota")
}

func (s platformServer) BuildRefreshRequest(ctx context.Context, in *pluginv1.BuildRefreshRequestRequest) (*pluginv1.BuildRefreshRequestResponse, error) {
	if r, ok := s.impl.(CredentialRefresher); ok {
		return r.BuildRefreshRequest(ctx, in)
	}
	return nil, status.Error(codes.Unimplemented, "this account type cannot refresh its credentials")
}

func (s platformServer) ParseRefreshResponse(ctx context.Context, in *pluginv1.ParseRefreshResponseRequest) (*pluginv1.RefreshResult, error) {
	if r, ok := s.impl.(CredentialRefresher); ok {
		return r.ParseRefreshResponse(ctx, in)
	}
	return nil, status.Error(codes.Unimplemented, "this account type cannot refresh its credentials")
}

func (s platformServer) BuildBalanceRequest(ctx context.Context, in *pluginv1.BuildBalanceRequestRequest) (*pluginv1.BuildBalanceRequestResponse, error) {
	if b, ok := s.impl.(BalanceReader); ok {
		return b.BuildBalanceRequest(ctx, in)
	}
	return nil, status.Error(codes.Unimplemented, "this account type cannot read its balance")
}

func (s platformServer) ParseBalanceResponse(ctx context.Context, in *pluginv1.ParseBalanceResponseRequest) (*pluginv1.ParseBalanceResponseResponse, error) {
	if b, ok := s.impl.(BalanceReader); ok {
		return b.ParseBalanceResponse(ctx, in)
	}
	return nil, status.Error(codes.Unimplemented, "this account type cannot read its balance")
}

type hookServer struct {
	pluginv1.UnimplementedHookServiceServer
	impl Hook
}

func (s hookServer) OnGatewayRequest(ctx context.Context, in *pluginv1.GatewayRequestHookRequest) (*pluginv1.GatewayRequestHookResponse, error) {
	return s.impl.OnGatewayRequest(ctx, in)
}

type appServer struct {
	pluginv1.UnimplementedAppServiceServer
	jobs      JobRunner
	events    EventHandler
	broadcast BroadcastHandler
}

func (s appServer) OnBroadcast(ctx context.Context, in *pluginv1.OnBroadcastRequest) (*pluginv1.OnBroadcastResponse, error) {
	if s.broadcast == nil {
		return nil, status.Error(codes.Unimplemented, "plugin does not handle broadcasts")
	}
	return s.broadcast.OnBroadcast(ctx, in)
}

func (s appServer) RunJob(ctx context.Context, in *pluginv1.RunJobRequest) (*pluginv1.RunJobResponse, error) {
	if s.jobs == nil {
		return nil, status.Error(codes.Unimplemented, "plugin has no jobs")
	}
	return s.jobs.RunJob(ctx, in)
}
func (s appServer) OnEvents(ctx context.Context, in *pluginv1.OnEventsRequest) (*pluginv1.OnEventsResponse, error) {
	if s.events == nil {
		return nil, status.Error(codes.Unimplemented, "plugin has no event subscriptions")
	}
	return s.events.OnEvents(ctx, in)
}

type httpServer struct {
	pluginv1.UnimplementedHTTPServiceServer
	impl    HTTP
	runtime *runtime
}

// HandleHTTP binds the execution token of a console call to the handler's
// context, so that Fetch works there (CONTRACTS §117); a call without one
// (public, webhook) is served as it is.
func (s httpServer) HandleHTTP(ctx context.Context, in *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	if in.GetExecutionToken() == "" {
		return s.impl.HandleHTTP(ctx, in)
	}
	ctx, cancel, err := s.runtime.bindExecution(ctx, &execution{token: in.GetExecutionToken(), kind: kindHTTP})
	if err != nil {
		return nil, err
	}
	defer cancel()
	return s.impl.HandleHTTP(ctx, in)
}

type schedulerServer struct {
	pluginv1.UnimplementedSchedulerServiceServer
	rank AccountRanker
}

func (s schedulerServer) RankAccounts(ctx context.Context, in *pluginv1.RankAccountsRequest) (*pluginv1.RankAccountsResponse, error) {
	return s.rank.RankAccounts(ctx, in)
}

type migrationServer struct {
	pluginv1.UnimplementedMigrationServiceServer
	impl Migration
}

func (s migrationServer) MigrateData(ctx context.Context, in *pluginv1.MigrateDataRequest) (*pluginv1.MigrateDataResponse, error) {
	return s.impl.MigrateData(ctx, in)
}

type notificationServer struct {
	pluginv1.UnimplementedNotificationServiceServer
	impl NotificationChannel
}

func (s notificationServer) DeliverNotification(ctx context.Context, in *pluginv1.DeliverNotificationRequest) (*pluginv1.DeliverNotificationResponse, error) {
	return s.impl.DeliverNotification(ctx, in)
}
