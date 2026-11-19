// Package pluginsdktest runs a plugin in-process for unit tests: the plugin
// services and a FakeHost are served over bufconn, and the harness performs
// the host handshake (GetInfo -> InitHost -> Configure). Harness.Execute and
// Harness.Monitor then drive a platform plugin the way the host does, with
// the FakeHost standing in for the host's HTTP client (execution.go).
package pluginsdktest

import (
	"context"
	"encoding/json"
	"math"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
)

// HostBrokerID is the broker id the harness announces in InitHost.
const HostBrokerID = 1

// Options configures Start.
type Options struct {
	// Host is the fake host; a new one is created when nil.
	Host *FakeHost
	// Config is the plugin settings (any JSON-marshalable value or a
	// json.RawMessage/string holding JSON). nil = "{}".
	Config any
	// Grants passed to Configure.
	Grants []*pluginv1.Grant
	// Manifest is the plugin's manifest.json. The plugin is served with it
	// (pluginsdk.WithManifest) and the fake host knows it (FakeHost.Manifest,
	// unless already set), as the core knows the package's manifest.
	Manifest []byte
	// SDK options passed to pluginsdk.Register, after a default
	// WithStrictNetwork(StrictNever) and WithManifest(Manifest), e.g. WithInfo.
	SDK []pluginsdk.Option
	// SkipHandshake leaves InitHost/Configure to the test.
	SkipHandshake bool
	// PluginServer are extra options of the plugin's gRPC server, e.g. an
	// interceptor that sees when a call returns on the plugin's side.
	PluginServer []grpc.ServerOption
}

// Harness is a running in-process plugin.
type Harness struct {
	T    testing.TB
	Host *FakeHost
	Conn *grpc.ClientConn
	Info *pluginv1.GetInfoResponse

	Plugin    pluginv1.PluginServiceClient
	Platform  pluginv1.PlatformServiceClient
	Hook      pluginv1.HookServiceClient
	App       pluginv1.AppServiceClient
	HTTP      pluginv1.HTTPServiceClient
	Scheduler pluginv1.SchedulerServiceClient
	Migration pluginv1.MigrationServiceClient
	// Notification serves notify.channel.v1.
	Notification pluginv1.NotificationServiceClient
}

// Start serves p and completes the host handshake. Everything is torn down
// with t.Cleanup (Shutdown is called first).
func Start(t testing.TB, p any, opts Options) *Harness {
	t.Helper()
	fh, hostSrv, dialHost := startHost(t, opts)
	plugSrv, conn, h := startPlugin(t, p, opts, fh, dialHost)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = h.Plugin.Shutdown(ctx, &pluginv1.ShutdownRequest{GraceSeconds: 1})
		_ = conn.Close()
		plugSrv.Stop()
		hostSrv.Stop()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	info, err := h.Plugin.GetInfo(ctx, &pluginv1.GetInfoRequest{})
	if err != nil {
		t.Fatalf("pluginsdktest: GetInfo: %v", err)
	}
	h.Info = info
	if opts.SkipHandshake {
		return h
	}
	init, err := h.Plugin.InitHost(ctx, &pluginv1.InitHostRequest{
		HostBrokerId: HostBrokerID, HostApiVersion: protocol.HostAPIVersion, NodeId: "test-node", BootId: "test-boot", HostVersion: "0.1.0-test",
	})
	if err != nil {
		t.Fatalf("pluginsdktest: InitHost: %v", err)
	}
	if !init.GetReady() {
		t.Fatalf("pluginsdktest: InitHost not ready: %s", init.GetMessage())
	}
	if errs := h.Configure(opts.Config, opts.Grants); len(errs) > 0 {
		t.Fatalf("pluginsdktest: Configure rejected: %v", errs)
	}
	return h
}

// startHost serves the FakeHost's HostService and EgressService over an
// in-memory listener and returns it with the dialer the plugin uses.
func startHost(t testing.TB, opts Options) (*FakeHost, *grpc.Server, func(uint32) (*grpc.ClientConn, error)) {
	t.Helper()
	fh := opts.Host
	if fh == nil {
		fh = NewFakeHost()
	}
	if fh.Manifest == nil && opts.Manifest != nil {
		var m manifest.Manifest
		if json.Unmarshal(opts.Manifest, &m) == nil { // an invalid one fails Register
			fh.Manifest = &m
		}
	}
	hostLis := bufconn.Listen(1 << 20)
	// Like the core, the host accepts whole request bodies back from a
	// platform plugin and bounds none of them.
	hostSrv := grpc.NewServer(grpc.MaxRecvMsgSize(math.MaxInt32))
	pluginv1.RegisterHostServiceServer(hostSrv, fh)
	pluginv1.RegisterEgressServiceServer(hostSrv, fh)
	go func() { _ = hostSrv.Serve(hostLis) }()
	return fh, hostSrv, func(uint32) (*grpc.ClientConn, error) { return dialBuf(hostLis) }
}

// startPlugin serves p with the SDK over an in-memory listener and returns the
// server, the client connection and the harness around it. Teardown stays with
// Start so both servers are stopped in one place.
func startPlugin(t testing.TB, p any, opts Options, fh *FakeHost, dialHost func(uint32) (*grpc.ClientConn, error)) (*grpc.Server, *grpc.ClientConn, *Harness) {
	t.Helper()
	sdkOpts := []pluginsdk.Option{pluginsdk.WithStrictNetwork(pluginsdk.StrictNever)}
	if opts.Manifest != nil {
		sdkOpts = append(sdkOpts, pluginsdk.WithManifest(opts.Manifest))
	}
	sdkOpts = append(sdkOpts, opts.SDK...)
	plugLis := bufconn.Listen(1 << 20)
	plugSrv := pluginsdk.NewGRPCServer(opts.PluginServer)
	if err := pluginsdk.Register(plugSrv, p, dialHost, sdkOpts...); err != nil {
		t.Fatalf("pluginsdktest: register: %v", err)
	}
	go func() { _ = plugSrv.Serve(plugLis) }()

	conn, err := dialBuf(plugLis)
	if err != nil {
		t.Fatalf("pluginsdktest: dial plugin: %v", err)
	}
	return plugSrv, conn, &Harness{
		T: t, Host: fh, Conn: conn,
		Plugin:    pluginv1.NewPluginServiceClient(conn),
		Platform:  pluginv1.NewPlatformServiceClient(conn),
		Hook:      pluginv1.NewHookServiceClient(conn),
		App:       pluginv1.NewAppServiceClient(conn),
		HTTP:      pluginv1.NewHTTPServiceClient(conn),
		Scheduler: pluginv1.NewSchedulerServiceClient(conn),
		Migration: pluginv1.NewMigrationServiceClient(conn),

		Notification: pluginv1.NewNotificationServiceClient(conn),
	}
}

// Configure sends a Configure call and returns the field errors.
func (h *Harness) Configure(config any, grants []*pluginv1.Grant) []*pluginv1.FieldError {
	h.T.Helper()
	resp, err := h.Plugin.Configure(context.Background(), &pluginv1.ConfigureRequest{ConfigJson: h.configJSON(config), Grants: grants})
	if err != nil {
		h.T.Fatalf("pluginsdktest: Configure: %v", err)
	}
	return resp.GetErrors()
}

// ValidateSettings sends a ValidateSettings call; config and grants are what
// Configure takes. A plugin without pluginsdk.SettingsValidator answers
// UNIMPLEMENTED.
func (h *Harness) ValidateSettings(config any, grants []*pluginv1.Grant) (*pluginv1.ValidateSettingsResponse, error) {
	h.T.Helper()
	return h.Plugin.ValidateSettings(context.Background(), &pluginv1.ValidateSettingsRequest{ConfigJson: h.configJSON(config), Grants: grants})
}

// configJSON encodes config: nil is "{}", a string, json.RawMessage or
// []byte is taken as written, anything else is marshaled.
func (h *Harness) configJSON(config any) string {
	h.T.Helper()
	switch v := config.(type) {
	case nil:
		return "{}"
	case string:
		return v
	case json.RawMessage:
		return string(v)
	case []byte:
		return string(v)
	}
	b, err := json.Marshal(config)
	if err != nil {
		h.T.Fatalf("pluginsdktest: marshal config: %v", err)
	}
	return string(b)
}

// Do sends an HTTPService request. body may be nil, []byte, string or any
// JSON-marshalable value.
func (h *Harness) Do(method, routePath string, query map[string]string, body any) *pluginv1.HTTPResponse {
	h.T.Helper()
	return h.DoAs(1, method, routePath, query, body)
}

// DoAs is Do as console user userID (Do is user 1; 0 is an unauthenticated
// caller). As the host does, a call by a user carries an execution token
// (BeginRoute), void once the call returns, so the handler may Fetch with a
// proxy ticket.
func (h *Harness) DoAs(userID int64, method, routePath string, query map[string]string, body any) *pluginv1.HTTPResponse {
	h.T.Helper()
	req := &pluginv1.HTTPRequest{
		Caller: &pluginv1.Caller{UserId: userID, RequestId: "test", Locale: "en"},
		Method: method, Path: routePath, RoutePath: routePath,
		Query: map[string]*pluginv1.HeaderValues{},
	}
	for k, v := range query {
		req.Query[k] = &pluginv1.HeaderValues{Values: []string{v}}
	}
	switch b := body.(type) {
	case nil:
	case []byte:
		req.Body = b
	case string:
		req.Body = []byte(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			h.T.Fatalf("pluginsdktest: marshal body: %v", err)
		}
		req.Body = raw
	}
	if userID > 0 {
		defer h.Host.Serve(userID)()
		e := h.Host.BeginRoute()
		defer e.End()
		req.ExecutionToken = e.token
	}
	resp, err := h.HTTP.HandleHTTP(context.Background(), req)
	if err != nil {
		h.T.Fatalf("pluginsdktest: HandleHTTP %s %s: %v", method, routePath, err)
	}
	return resp
}

// Deliver hands a broadcast to the plugin (AppService.OnBroadcast), as the
// host does for a message published on another node. Pair it with
// FakeHost.OnPublish to connect two harnesses.
func (h *Harness) Deliver(topic string, payload []byte, sourceNodeID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := h.App.OnBroadcast(ctx, &pluginv1.OnBroadcastRequest{Topic: topic, Payload: payload, SourceNodeId: sourceNodeID})
	return err
}

func dialBuf(l *bufconn.Listener) (*grpc.ClientConn, error) {
	return grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return l.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
}
