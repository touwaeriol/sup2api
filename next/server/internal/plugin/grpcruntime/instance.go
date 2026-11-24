package grpcruntime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry"
)

// Instance states.
const (
	StateStarting   = "starting"
	StateReady      = "ready"
	StateRestarting = "restarting"
	StateFailed     = "failed" // restart budget exhausted on this node
	StateDraining   = "draining"
	StateStopped    = "stopped"
)

// Instance is one running plugin version on this node.
type Instance struct {
	rt      *Runtime
	pkg     *registry.Package
	binPath string
	binSum  string
	log     *slog.Logger
	caps    map[string]bool

	sems     [numClasses]chan struct{}
	inflight atomic.Int64
	callers  callerSet     // console users of the HTTP requests in flight
	draining atomic.Bool   // no new calls are admitted
	idle     chan struct{} // closed once draining and no call in flight
	idleOnce sync.Once

	settings  atomic.Pointer[settings]
	proc      atomic.Pointer[proc]
	refreshMu sync.Mutex // serialize DB reads and Configure acknowledgements

	mu        sync.Mutex
	state     string
	lastErr   string
	restarts  []time.Time
	restartsN int // total restarts since load

	restartReq chan string
	stopCh     chan struct{}
	stopOnce   sync.Once
	done       chan struct{} // supervisor exited
}

// proc is one OS process of an instance; replaced on restart.
type proc struct {
	client     *plugin.Client
	conn       *grpc.ClientConn
	broker     *plugin.GRPCBroker
	plugin     pluginv1.PluginServiceClient
	platform   pluginv1.PlatformServiceClient
	hook       pluginv1.HookServiceClient
	app        pluginv1.AppServiceClient
	observer   pluginv1.ObservationServiceClient
	http       pluginv1.HTTPServiceClient
	sched      pluginv1.SchedulerServiceClient
	migration  pluginv1.MigrationServiceClient
	notifier   pluginv1.NotificationServiceClient
	pid        int
	runDir     string // private run directory (sockets, TMPDIR); removed with the process
	stopWatch  func()
	limits     specLimits               // resource limits the process was started with
	failures   int                      // consecutive health failures
	configured atomic.Pointer[settings] // last configuration acknowledged by this process
	scopes     callScopes               // the Execute and Monitor calls in flight
}

// specLimits are the fields of a LaunchSpec fixed for the life of a
// process: resource limits and what the sandbox grants (a changed or
// revoked "fs.data" or "net" grant restarts the process, LimitsStale).
type specLimits struct {
	MemoryMB     int
	CPU          float64
	MaxThreads   int
	MaxOpenFiles int
	DataDir      string
	Net          bool
}

func limitsOf(s core.LaunchSpec) specLimits {
	return specLimits{MemoryMB: s.MemoryMB, CPU: s.CPU, MaxThreads: s.MaxThreads, MaxOpenFiles: s.MaxOpenFiles, DataDir: s.DataDir, Net: s.Net}
}

func newInstance(rt *Runtime, pkg *registry.Package, binPath, sum string, st *settings) *Instance {
	i := &Instance{
		rt:         rt,
		pkg:        pkg,
		binPath:    binPath,
		binSum:     sum,
		log:        rt.log.With("plugin", pkg.Key, "version", pkg.Version),
		caps:       map[string]bool{},
		sems:       newSemaphores(rt.o.Concurrency),
		idle:       make(chan struct{}),
		state:      StateStarting,
		restartReq: make(chan string, 1),
		stopCh:     make(chan struct{}),
		done:       make(chan struct{}),
	}
	for _, c := range pkg.Manifest.Capabilities {
		i.caps[c.ID] = true
	}
	i.settings.Store(st)
	return i
}

// ------------------------------------------------------------------ accessors

func (i *Instance) Key() string                { return i.pkg.Key }
func (i *Instance) Version() string            { return i.pkg.Version }
func (i *Instance) Package() *registry.Package { return i.pkg }
func (i *Instance) Info() core.PluginInfo      { return registry.Info(i.pkg) }
func (i *Instance) Grants() registry.Grants    { return i.settings.Load().grants }

// State returns the lifecycle state and the last error message.
func (i *Instance) State() (state, lastErr string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.state, i.lastErr
}

// Restarts returns the number of restarts since the instance was loaded.
func (i *Instance) Restarts() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.restartsN
}

// InFlight returns the number of calls currently running.
func (i *Instance) InFlight() int64 { return i.inflight.Load() }

// PID of the current process (0 when not running).
func (i *Instance) PID() int {
	if p := i.proc.Load(); p != nil {
		return p.pid
	}
	return 0
}

// RunDir is the private run directory of the current process ("" when not
// running): go-plugin's sockets and the plugin's TMPDIR are below it.
func (i *Instance) RunDir() string {
	if p := i.proc.Load(); p != nil {
		return p.runDir
	}
	return ""
}

func (i *Instance) setState(s, errMsg string) {
	i.mu.Lock()
	i.state = s
	i.lastErr = errMsg
	i.mu.Unlock()
}

func (i *Instance) isStopped() bool {
	select {
	case <-i.stopCh:
		return true
	default:
		return false
	}
}

// ------------------------------------------------------------------ process lifecycle

func (i *Instance) launchSpec() core.LaunchSpec {
	m := i.pkg.Manifest
	st := i.settings.Load()
	spec := core.LaunchSpec{
		PluginKey:     i.pkg.Key,
		Version:       i.pkg.Version,
		BinaryPath:    i.binPath,
		Net:           st.grants.Has("net"),
		StrictNetwork: i.rt.o.StrictNetwork,
		Seccomp:       i.rt.o.Seccomp,
	}
	// Only a plugin granted "fs.data" gets a data directory (CONTRACTS §43.8).
	if st.grants.Has("fs.data") {
		spec.DataDir = filepath.Join(i.rt.o.DataDir, i.pkg.Key, "work")
	}
	// The manifest's memory figure is what the plugin says it needs, not a
	// limit: memory is limited only by the operator (core.EffectiveMemoryMB).
	if m.Resources != nil {
		spec.CPU = m.Resources.CPU
		spec.MaxThreads = m.Resources.MaxThreads
		spec.MaxOpenFiles = m.Resources.MaxOpenFiles
	}
	mergeLimits(&spec, st.limits)
	spec.MemoryMB = core.EffectiveMemoryMB(st.limits.MemoryMB, i.rt.o.MaxMemoryMB)
	strict := "0"
	if spec.StrictNetwork {
		strict = "1"
	}
	spec.Env = []string{
		protocol.EnvPluginKey + "=" + i.pkg.Key,
		protocol.EnvPluginVersion + "=" + i.pkg.Version,
		protocol.EnvStrictNetwork + "=" + strict,
	}
	return spec
}

// mergeLimits applies the operator's per-plugin overrides on top of the
// manifest limits; zero means "keep the manifest value". Memory is
// core.EffectiveMemoryMB's.
func mergeLimits(spec *core.LaunchSpec, l resourceLimits) {
	if l.CPU > 0 {
		spec.CPU = l.CPU
	}
	if l.MaxThreads > 0 {
		spec.MaxThreads = l.MaxThreads
	}
	if l.MaxOpenFiles > 0 {
		spec.MaxOpenFiles = l.MaxOpenFiles
	}
}

func (i *Instance) startFirst(ctx context.Context) error {
	p, err := i.startProc(ctx)
	if err != nil {
		i.setState(StateFailed, err.Error())
		return err
	}
	i.proc.Store(p)
	i.setState(StateReady, "")
	return nil
}

// startProc launches one process and runs the handshake.
func (i *Instance) startProc(ctx context.Context) (_ *proc, err error) {
	ctx, cancel := context.WithTimeout(ctx, i.rt.o.StartTimeout)
	defer cancel()

	if err := i.reverifyBinary(); err != nil {
		return nil, err
	}
	spec := i.launchSpec()
	if spec.DataDir != "" {
		if err := os.MkdirAll(spec.DataDir, 0o700); err != nil {
			return nil, err
		}
	}
	runDir, err := i.prepareRunDir()
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(runDir)
		}
	}()
	spec.RunDir = runDir
	spec.Env = append(spec.Env, runDirEnv(runDir)...)

	p, err := i.newPluginClient(ctx, spec, runDir)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			i.killProc(p, false)
		}
	}()
	i.dispenseClients(p)
	if err := i.verifyInfo(ctx, p); err != nil {
		return nil, err
	}
	if err := i.serveHostBroker(ctx, p); err != nil {
		return nil, err
	}
	if err := i.configure(ctx, p, i.settings.Load()); err != nil {
		return nil, err
	}
	if err := verifyHealth(ctx, p); err != nil {
		return nil, err
	}
	if p.pid > 0 {
		p.stopWatch = i.rt.o.Launcher.Watch(spec, p.pid, i.onResourceEvent)
	}
	return p, nil
}

// reverifyBinary re-extracts the binary when the extracted copy no longer
// matches the package checksum.
func (i *Instance) reverifyBinary() error {
	if sum, err := fileSum(i.binPath); err == nil && sum == i.binSum {
		return nil
	}
	if _, _, err := i.pkg.ExtractBinary(goos(), goarch()); err != nil {
		return fmt.Errorf("re-extract binary: %w", err)
	}
	return nil
}

// prepareRunDir creates the private run directory of this process.
func (i *Instance) prepareRunDir() (string, error) {
	runDir, fallback, err := newRunDir(i.rt.o.RunDir, i.pkg.Key)
	if err != nil {
		return "", err
	}
	if fallback {
		i.log.Warn("plugin run directory is too long for unix sockets; using a private directory under /tmp (set SUB2API_PLUGIN_RUN_DIR)",
			"configured", i.rt.o.RunDir, "dir", runDir)
	}
	return runDir, nil
}

// newPluginClient builds the go-plugin client and starts the process.
func (i *Instance) newPluginClient(ctx context.Context, spec core.LaunchSpec, runDir string) (_ *proc, err error) {
	cmd, err := i.rt.o.Launcher.Command(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("launcher: %w", err)
	}
	// go-plugin can only checksum the executable it starts; when the
	// launcher wraps the plugin (sandbox), the binary was verified above.
	var verify func() error
	if samePath(cmd.Path, i.binPath) {
		verify = func() error {
			if sum, err := fileSum(cmd.Path); err != nil || sum != i.binSum {
				return plugin.ErrChecksumsDoNotMatch
			}
			return nil
		}
	}
	var run *cmdRunner
	cfg := &plugin.ClientConfig{
		HandshakeConfig:  protocol.Handshake,
		Plugins:          protocol.PluginMap(&protocol.GRPCPlugin{}),
		AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC},
		SkipHostEnv:      true,
		StartTimeout:     i.rt.o.StartTimeout,
		Logger:           newHCLogger(i.log),
		// Every connection between the core and this process - the plugin's
		// server and the core's broker serving HostService/EgressService -
		// is mutual TLS with one-time certificates of this process pair, so
		// no other process (another plugin included) can connect to either
		// side or impersonate this plugin to HostService.
		AutoMTLS: true,
		// Both unix sockets go to a directory go-plugin creates inside the
		// private run directory (not the shared TMPDIR); it is passed to the
		// plugin as PLUGIN_UNIX_SOCKET_DIR. go-plugin only does this for a
		// RunnerFunc, hence cmdRunner.
		UnixSocketConfig: &plugin.UnixSocketConfig{TempDir: runDir},
		RunnerFunc:       runnerFunc(cmd, verify, &run),
	}
	client := plugin.NewClient(cfg)
	p := &proc{client: client, limits: limitsOf(spec), runDir: runDir}
	defer func() {
		if err != nil {
			i.killProc(p, false)
		}
	}()
	rpc, err := client.Client()
	if err != nil {
		if strings.Contains(err.Error(), handshakeVersionMismatch) {
			return nil, i.rt.outdatedContract(i.pkg, err)
		}
		return nil, fmt.Errorf("start plugin process: %w", err)
	}
	raw, err := rpc.Dispense(protocol.PluginName)
	if err != nil {
		return nil, fmt.Errorf("dispense: %w", err)
	}
	pc, ok := raw.(*protocol.Client)
	if !ok || pc.Conn == nil {
		return nil, errors.New("dispense: unexpected client type")
	}
	p.conn = pc.Conn
	p.broker = pc.Broker
	if run != nil {
		p.pid = run.pid
	}
	return p, nil
}

// dispenseClients wires the eight service clients of the shared connection.
// p.conn is never nil here: newPluginClient only returns a proc after Dispense
// handed it a client with a connection.
func (i *Instance) dispenseClients(p *proc) {
	var conn grpc.ClientConnInterface = p.conn
	p.plugin = pluginv1.NewPluginServiceClient(conn)
	p.platform = pluginv1.NewPlatformServiceClient(conn)
	p.hook = pluginv1.NewHookServiceClient(conn)
	p.app = pluginv1.NewAppServiceClient(conn)
	p.observer = pluginv1.NewObservationServiceClient(conn)
	p.http = pluginv1.NewHTTPServiceClient(conn)
	p.sched = pluginv1.NewSchedulerServiceClient(conn)
	p.migration = pluginv1.NewMigrationServiceClient(conn)
	p.notifier = pluginv1.NewNotificationServiceClient(conn)
}

// verifyInfo checks GetInfo against the package identity and protocol.
func (i *Instance) verifyInfo(ctx context.Context, p *proc) error {
	info, err := p.plugin.GetInfo(ctx, &pluginv1.GetInfoRequest{})
	if err != nil {
		return fmt.Errorf("GetInfo: %w", err)
	}
	if info.GetPluginKey() != i.pkg.Manifest.Key || info.GetVersion() != i.pkg.Version {
		return fmt.Errorf("GetInfo: plugin reports %s@%s, package is %s@%s",
			info.GetPluginKey(), info.GetVersion(), i.pkg.Manifest.Key, i.pkg.Version)
	}
	if info.GetProtocolVersion() != protocol.ProtocolVersion {
		return i.rt.outdatedContract(i.pkg, fmt.Errorf("GetInfo: protocol version %d, host supports %d", info.GetProtocolVersion(), protocol.ProtocolVersion))
	}
	reported := map[string]bool{}
	for _, c := range info.GetCapabilities() {
		reported[c] = true
	}
	for c := range i.caps {
		if len(reported) > 0 && !reported[c] {
			i.log.Warn("plugin does not report a capability declared in its manifest", "capability", c)
		}
	}
	return nil
}

// serveHostBroker accepts the plugin's HostService and EgressService calls
// on the broker connection, then runs InitHost.
func (i *Instance) serveHostBroker(ctx context.Context, p *proc) error {
	// The tunnel is only registered for a plugin that may use it (the net
	// grant, or its own database); the policy is still checked on every
	// connection, so a later revocation applies at once.
	host := &hostServer{i: i, p: p}
	var egress pluginv1.EgressServiceServer = pluginv1.UnimplementedEgressServiceServer{}
	if pol := i.egressPolicy(); i.rt.o.Egress != nil && (pol.Net || pol.Database) {
		egress = i.rt.o.Egress.ServerFor(i.pkg.Key, i.egressPolicy)
	}
	brokerID := p.broker.NextId()
	go p.broker.AcceptAndServe(brokerID, func(opts []grpc.ServerOption) *grpc.Server {
		s := grpc.NewServer(append(opts, hostServerOptions()...)...)
		pluginv1.RegisterHostServiceServer(s, host)
		pluginv1.RegisterEgressServiceServer(s, egress)
		return s
	})
	node := i.rt.o.Node
	initResp, err := p.plugin.InitHost(ctx, &pluginv1.InitHostRequest{
		HostBrokerId:   brokerID,
		HostApiVersion: protocol.HostAPIVersion,
		NodeId:         node.NodeID(),
		BootId:         node.BootID(),
		HostVersion:    i.rt.o.HostVersion,
	})
	if err != nil {
		return fmt.Errorf("InitHost: %w", err)
	}
	if !initResp.GetReady() {
		return fmt.Errorf("InitHost: plugin not ready: %s", initResp.GetMessage())
	}
	return nil
}

// hostServerOptions are the options of the broker server besides go-plugin's
// own: a receive limit of math.MaxInt32, gRPC's own ceiling for one
// message, not a limit of ours.
func hostServerOptions() []grpc.ServerOption {
	return []grpc.ServerOption{grpc.MaxRecvMsgSize(math.MaxInt32)}
}

// verifyHealth requires a healthy plugin before it is used.
func verifyHealth(ctx context.Context, p *proc) error {
	h, err := p.plugin.Health(ctx, &pluginv1.HealthRequest{})
	if err != nil {
		return fmt.Errorf("Health: %w", err)
	}
	if !h.GetHealthy() {
		return fmt.Errorf("Health: unhealthy: %s", h.GetMessage())
	}
	return nil
}

// protoGrants are the grants as the plugin receives them (Configure,
// ValidateSettings).
func (st *settings) protoGrants() []*pluginv1.Grant {
	out := make([]*pluginv1.Grant, 0, len(st.grants))
	for perm, scope := range st.grants {
		out = append(out, &pluginv1.Grant{Permission: perm, ScopeJson: string(scope)})
	}
	return out
}

func (i *Instance) configure(ctx context.Context, p *proc, st *settings) error {
	req := &pluginv1.ConfigureRequest{ConfigJson: st.configJSON, Grants: st.protoGrants()}
	resp, err := p.plugin.Configure(ctx, req)
	if err != nil {
		return fmt.Errorf("Configure: %w", err)
	}
	if errs := resp.GetErrors(); len(errs) > 0 {
		return fmt.Errorf("Configure rejected: %s: %s", errs[0].GetField(), errs[0].GetMessage())
	}
	p.configured.Store(st)
	return nil
}

func (i *Instance) egressPolicy() core.EgressPolicy {
	st := i.settings.Load()
	pol := core.EgressPolicy{
		Mode: st.egress,
		// Without "net" the tunnel refuses every target but the database.
		Net:      st.grants.Has("net"),
		Database: st.grants.Has("db.schema") && i.pkg.Manifest != nil && i.pkg.Manifest.Database != nil,
	}
	if pol.Mode == "" {
		pol.Mode = "allow_all"
	}
	if pol.Net {
		pol.AllowedDomains, _ = st.grants.List("net", "domains")
	}
	return pol
}

func (i *Instance) onResourceEvent(ev core.ResourceEvent) {
	switch ev.Kind {
	case "memory_exceeded":
		i.log.Warn("plugin exceeded its memory limit, restarting", "rss", ev.RSSBytes, "msg", ev.Message)
		i.requestRestart("memory_exceeded")
	case "threads_exceeded":
		i.log.Warn("plugin exceeded its thread limit", "threads", ev.Threads)
	}
}

func (i *Instance) requestRestart(reason string) {
	select {
	case i.restartReq <- reason:
	default:
	}
}

// killProc shuts a process down (graceful Shutdown first when asked).
func (i *Instance) killProc(p *proc, graceful bool) {
	if p == nil {
		return
	}
	if p.stopWatch != nil {
		p.stopWatch()
	}
	if graceful && p.plugin != nil && !p.client.Exited() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, _ = p.plugin.Shutdown(ctx, &pluginv1.ShutdownRequest{GraceSeconds: 2})
		cancel()
	}
	p.client.Kill()
	if p.runDir != "" {
		_ = os.RemoveAll(p.runDir)
	}
}

// supervise watches the process: exit detection every second, health checks
// every HealthInterval, restart requests from the resource watchdog.
func (i *Instance) supervise() {
	defer close(i.done)
	exitTick := time.NewTicker(time.Second)
	defer exitTick.Stop()
	healthTick := time.NewTicker(i.rt.o.HealthInterval)
	defer healthTick.Stop()
	for {
		select {
		case <-i.stopCh:
			return
		case reason := <-i.restartReq:
			i.restart(reason)
		case <-exitTick.C:
			if p := i.proc.Load(); p != nil && p.client.Exited() {
				i.restart("process exited")
			}
		case <-healthTick.C:
			i.checkHealth()
		}
		if s, _ := i.State(); s == StateFailed {
			// Budget exhausted: stay down until the instance is replaced.
			<-i.stopCh
			return
		}
	}
}

func (i *Instance) checkHealth() {
	p := i.proc.Load()
	if p == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h, err := p.plugin.Health(ctx, &pluginv1.HealthRequest{})
	if err == nil && h.GetHealthy() {
		p.failures = 0
		return
	}
	p.failures++
	msg := "unhealthy"
	if err != nil {
		msg = err.Error()
	} else if h.GetMessage() != "" {
		msg = h.GetMessage()
	}
	i.log.Warn("plugin health check failed", "failures", p.failures, "err", msg)
	if p.failures >= 3 {
		i.restart("health check failed: " + msg)
	}
}

// restart replaces the process with exponential backoff. More than
// MaxRestarts restarts within RestartWindow marks the instance failed.
func (i *Instance) restart(reason string) {
	for {
		if i.isStopped() {
			return
		}
		now := time.Now()
		i.mu.Lock()
		kept := i.restarts[:0]
		for _, t := range i.restarts {
			if now.Sub(t) < i.rt.o.RestartWindow {
				kept = append(kept, t)
			}
		}
		i.restarts = append(kept, now)
		n := len(i.restarts)
		i.restartsN++
		i.mu.Unlock()

		old := i.proc.Swap(nil)
		i.killProc(old, false)
		if n > i.rt.o.MaxRestarts {
			msg := fmt.Sprintf("restarted more than %d times within %s; last reason: %s", i.rt.o.MaxRestarts, i.rt.o.RestartWindow, reason)
			i.log.Error("plugin marked unavailable on this node", "reason", msg)
			i.setState(StateFailed, msg)
			return
		}
		i.setState(StateRestarting, reason)
		delay := i.rt.o.BackoffBase << (n - 1)
		if delay > i.rt.o.BackoffMax || delay <= 0 {
			delay = i.rt.o.BackoffMax
		}
		i.log.Warn("restarting plugin", "reason", reason, "attempt", n, "delay", delay)
		select {
		case <-i.stopCh:
			return
		case <-time.After(delay):
		}
		p, err := i.startProc(context.Background())
		if err != nil {
			reason = err.Error()
			i.log.Error("plugin restart failed", "err", err)
			continue
		}
		i.mu.Lock()
		if i.isStopped() {
			i.mu.Unlock()
			i.killProc(p, true)
			return
		}
		i.proc.Store(p)
		i.mu.Unlock()
		state := StateReady
		if i.draining.Load() {
			state = StateDraining
		}
		i.setState(state, "")
		return
	}
}

// Refresh re-reads settings and grants; when they changed, the plugin is
// reconfigured in place. Resource limit changes need a new process: see
// LimitsStale (the rollout controller replaces the instance).
func (i *Instance) Refresh(ctx context.Context) error {
	i.refreshMu.Lock()
	defer i.refreshMu.Unlock()
	st, err := i.rt.loadSettings(ctx, i.pkg)
	if err != nil {
		return err
	}
	// Host authorization changes (especially revocation) apply immediately;
	// a plugin cannot delay them by failing Configure. Its acknowledgement is
	// tracked separately so a transient RPC error is retried on the next pass.
	i.settings.Store(st)
	p := i.proc.Load()
	if p == nil {
		return nil
	}
	if applied := p.configured.Load(); applied != nil && applied.fingerprint == st.fingerprint {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := i.configure(cctx, p, st); err != nil {
		i.log.Warn("plugin reconfigure failed", "err", err)
		return err
	}
	return nil
}

// LimitsStale reports whether the running process was started with other
// resource limits or sandbox grants than the current settings (as of the
// last Refresh).
func (i *Instance) LimitsStale() bool {
	p := i.proc.Load()
	if p == nil {
		return false
	}
	return p.limits != limitsOf(i.launchSpec())
}

// MigrateData calls MigrationService.MigrateData when the plugin declares
// capability migration.data.v1; otherwise it is a no-op.
func (i *Instance) MigrateData(ctx context.Context, from, to string) error {
	if !i.caps[manifest.CapMigrationData] {
		return nil
	}
	return i.call(ctx, classBackground, TimeoutMigrateData, func(ctx context.Context, p *proc) error {
		_, err := p.migration.MigrateData(ctx, &pluginv1.MigrateDataRequest{FromVersion: from, ToVersion: to})
		return err
	})
}

// Drain retires the instance. The caller has already taken it out of the
// published generation; requests that captured the previous generation may
// still call in for DrainGrace, after which new calls are refused. Drain then
// waits for in-flight calls - Execute included, i.e. whole upstream streams -
// up to timeout (0 = runtime default) and stops the process.
func (i *Instance) Drain(timeout time.Duration) {
	if timeout <= 0 {
		timeout = i.rt.o.DrainTimeout
	}
	select {
	case <-time.After(i.rt.o.DrainGrace):
	case <-i.stopCh:
	}
	i.draining.Store(true)
	if s, _ := i.State(); s == StateReady {
		i.setState(StateDraining, "")
	}
	if i.inflight.Load() == 0 {
		i.idleOnce.Do(func() { close(i.idle) })
	}
	select {
	case <-i.idle:
	case <-time.After(timeout):
		i.log.Warn("plugin drain timed out", "in_flight", i.inflight.Load(), "timeout", timeout)
	}
	i.Stop()
}

// Stop terminates the instance immediately.
func (i *Instance) Stop() {
	i.stopOnce.Do(func() {
		i.draining.Store(true)
		i.mu.Lock()
		close(i.stopCh)
		i.mu.Unlock()
		p := i.proc.Swap(nil)
		i.killProc(p, true)
		i.setState(StateStopped, "")
	})
}

// Kill terminates the current process without stopping supervision (tests,
// crash simulation).
func (i *Instance) Kill() {
	if p := i.proc.Load(); p != nil {
		p.client.Kill()
	}
}
