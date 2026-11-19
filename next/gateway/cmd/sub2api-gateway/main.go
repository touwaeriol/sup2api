// sub2api-gateway owns the public listener and one replaceable core process.
package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/gateway/internal/control"
	"github.com/Sub2API-Devs/sup2api/next/gateway/internal/cpuload"
	"github.com/Sub2API-Devs/sup2api/next/gateway/internal/peer"
	"github.com/Sub2API-Devs/sup2api/next/gateway/internal/pluginblob"
	"github.com/Sub2API-Devs/sup2api/next/gateway/internal/proxy"
	"github.com/Sub2API-Devs/sup2api/next/gateway/internal/release"
	"github.com/Sub2API-Devs/sup2api/next/gateway/internal/supervisor"
	runtimecontract "github.com/Sub2API-Devs/sup2api/next/runtime-contract"
	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg"
	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/secret"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type config struct {
	Root             string   `json:"root"`
	ClusterID        string   `json:"cluster_id"`
	NodeID           string   `json:"node_id"`
	PrimaryNode      string   `json:"primary_node"`
	BaselineDigest   string   `json:"baseline_digest"`
	DatabaseURL      string   `json:"database_url"`
	RedisURL         string   `json:"redis_url"`
	PublicAddr       string   `json:"public_addr"`
	PeerAddr         string   `json:"peer_addr"`
	PeerURL          string   `json:"peer_url"`
	CertFile         string   `json:"cert_file"`
	KeyFile          string   `json:"key_file"`
	CAFile           string   `json:"ca_file"`
	PeerAuthKey      string   `json:"peer_auth_key"`
	TrustedProxies   []string `json:"trusted_proxies"`
	ReleaseOrigin    string   `json:"release_origin"`
	CoreURL          string   `json:"core_url"`
	CoreArgs         []string `json:"core_args"`
	CoreEnv          []string `json:"core_env"`
	RuntimeABI       string   `json:"runtime_abi"`
	ManagementSocket string   `json:"management_socket"`
	CoreSocket       string   `json:"core_socket"`
	// PluginMaxBytes bounds stored plugin packages (default
	// runtimecontract.PluginPackageMaxBytes, 64 MiB).
	PluginMaxBytes int64 `json:"plugin_max_bytes"`
	// CoreEnvInherit names further shell variables a core may inherit,
	// besides the supervisor's allowlist (supervisor.inheritedEnv).
	CoreEnvInherit []string `json:"core_env_inherit"`
	// ManagementTokenFile holds the per-boot bearer token of the management
	// socket (default: shell.token beside the socket).
	ManagementTokenFile string `json:"management_token_file"`
	// PluginStoreMaxBytes bounds the total size of stored plugin packages
	// (default 16 GiB); uploads beyond it are refused until GC frees space.
	PluginStoreMaxBytes int64 `json:"plugin_store_max_bytes"`
	// PluginLandlockDisabled keeps the gateway from turning the plugin
	// sandbox's Landlock rules on for its core. Without Landlock a plugin of
	// the same UID can read the management token and the node state.
	PluginLandlockDisabled bool `json:"plugin_landlock_disabled"`
	// AllowUnauthenticatedRedis accepts a REDIS_URL without a password over
	// TCP. Every reader of that cache can verify node identities and alter
	// locks and registrations, so this is for isolated test setups only.
	AllowUnauthenticatedRedis bool `json:"allow_unauthenticated_redis"`
	// AllowPrivateUpdateSources lets a url update source resolve to private
	// addresses, for a release mirror inside the cluster's network.
	AllowPrivateUpdateSources bool `json:"allow_private_update_sources"`
}

// Version is the gateway's release version, set at build time with
// -ldflags "-X main.Version=<version>".
var Version = "0.0.0-dev"

func main() {
	if err := run(); err != nil {
		slog.Error("gateway stopped", "error", err)
		os.Exit(1)
	}
}

// cliOptions is one parsed invocation.
type cliOptions struct {
	command     string
	configPath  string
	manifestURL string
	socket      string
	tokenFile   string
	upgradeID   string
	node        string
	bootstrap   bool
}

// parseCLI parses argv into the command and its flags.
func parseCLI(args []string) (cliOptions, error) {
	if len(args) < 1 {
		return cliOptions{}, errors.New("usage: sub2api-gateway version|init|import|serve|status|pause|resume|cancel|rollback|enable-node|disable-node|yield|unyield|yield-status|set-primary|remove-node")
	}
	command := args[0]
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	cfgPath := fs.String("config", "/etc/sub2api/shell.json", "gateway configuration")
	manifestURL := fs.String("manifest", "", "release manifest URL at the configured release origin")
	socket := fs.String("socket", "", "local management socket")
	tokenFile := fs.String("token-file", "", "management token file (default: from -config, or shell.token beside -socket)")
	id := fs.String("id", "", "upgrade ID")
	node := fs.String("node", "", "node ID for set-primary or remove-node")
	bootstrap := fs.Bool("bootstrap", false, "explicitly initialize the first core database on the primary")
	if err := fs.Parse(args[1:]); err != nil {
		return cliOptions{}, err
	}
	return cliOptions{
		command:     command,
		configPath:  *cfgPath,
		manifestURL: *manifestURL,
		socket:      *socket,
		tokenFile:   *tokenFile,
		upgradeID:   *id,
		node:        *node,
		bootstrap:   *bootstrap,
	}, nil
}

func run() error {
	opts, err := parseCLI(os.Args[1:])
	if err != nil {
		return err
	}
	if opts.command == "version" {
		fmt.Println(Version)
		return nil
	}
	done, err := dispatchConfigCommand(opts)
	if err != nil || done {
		return err
	}
	return runCommand(opts)
}

// dispatchConfigCommand handles the commands that only need a config file or the
// local management socket. done reports that the command was one of them, in
// which case err is the result to return.
func dispatchConfigCommand(opts cliOptions) (done bool, err error) {
	switch opts.command {
	case "status", "pause", "resume", "cancel", "rollback", "enable-node", "disable-node", "yield", "unyield", "yield-status":
		socket, tokenFile := opts.socket, opts.tokenFile
		if socket == "" {
			c, err := readConfig(opts.configPath)
			if err != nil {
				return true, err
			}
			socket = c.ManagementSocket
			if tokenFile == "" {
				tokenFile = c.ManagementTokenFile
			}
		}
		if tokenFile == "" {
			tokenFile = defaultTokenFile(socket)
		}
		return true, localCommand(opts.command, socket, tokenFile, opts.upgradeID)
	case "set-primary", "remove-node":
		if opts.node == "" {
			return true, fmt.Errorf("%s requires -node", opts.command)
		}
		c, err := readConfig(opts.configPath)
		if err != nil {
			return true, err
		}
		return true, clusterCommand(opts.command, c, opts.node)
	}
	return false, nil
}

// runCommand opens the shared database and release state, then runs init, import
// or serve. Their error order is operator-visible, so the setup stays sequential.
func runCommand(opts cliOptions) error {
	c, err := readConfig(opts.configPath)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if opts.command == "serve" {
		startupHandoff(c.Root, execGateway)
		return serveGateway(ctx, cancel, c, opts.bootstrap)
	}
	s, err := newShell(ctx, c, opts.command)
	if err != nil {
		return err
	}
	defer s.close()
	switch opts.command {
	case "init":
		return s.initCluster(ctx, opts.manifestURL)
	case "import":
		return s.importRelease(ctx, opts.manifestURL)
	}
	return errors.New("unknown command")
}

// shell owns the state every command shares: the database pool, the store and
// the release manager, plus the closers for what has been opened so far.
type shell struct {
	c        config
	db       *gorm.DB
	store    *control.Store
	releases *release.Manager
	closers  []func()
}

// close releases shell resources in reverse creation order.
func (s *shell) close() {
	for i := len(s.closers) - 1; i >= 0; i-- {
		s.closers[i]()
	}
	s.closers = nil
}

// gatewayPool is the gateway's pool. The gateway only serves the control
// plane, so it keeps a fixed 8 connections whatever the host's size.
var gatewayPool = gormpg.Settings{MaxConns: 8, MaxConnLifetime: time.Hour, MaxConnIdleTime: 30 * time.Minute}

// openPool opens the PG pool the way the core does (gormpg), sized by
// SUB2API_GATEWAY_POOL_MAX_CONNS, else gatewayPool. It connects lazily: the
// first statement dials.
func openPool(c config) (*gorm.DB, error) {
	cfg, err := gormpg.ParseConfig(c.DatabaseURL)
	if err != nil {
		return nil, err
	}
	pool := gatewayPool.WithMaxConns(os.Getenv("SUB2API_GATEWAY_POOL_MAX_CONNS"))
	db, _, err := gormpg.Open(cfg, gormpg.Options{Pool: pool, Gorm: &gorm.Config{
		SkipDefaultTransaction: true, // the store opens the transactions it needs
		DisableAutomaticPing:   true,
		// Errors go back to the caller; logged SQL could carry values.
		Logger: logger.Discard,
	}})
	return db, err
}

// closePool closes the pool's connections.
func closePool(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

// openRedis connects to the cache.
func openRedis(c config) (*redis.Client, error) {
	opt, err := parseRedisURL(c.RedisURL)
	if err != nil {
		return nil, err
	}
	return redis.NewClient(opt), nil
}

// newShell opens the pool, store and release manager and applies the schema when
// the command serves or initializes a cluster.
func newShell(ctx context.Context, c config, command string) (*shell, error) {
	s := &shell{c: c}
	db, err := openPool(c)
	if err != nil {
		return nil, err
	}
	s.db = db
	s.closers = append(s.closers, func() { closePool(db) })
	if sqlDB, err := db.DB(); err == nil {
		slog.Info("gateway database pool", "max_conns", sqlDB.Stats().MaxOpenConnections)
	}
	s.store = &control.Store{DB: db, Cluster: c.ClusterID}
	cipher, err := c.masterCipher()
	if err != nil {
		s.close()
		return nil, err
	}
	s.releases = &release.Manager{Root: c.Root, OS: runtime.GOOS, Arch: runtime.GOARCH, RuntimeABI: c.RuntimeABI}
	s.store.Updates = control.NewUpdateService(s.releases, cipher, c.AllowPrivateUpdateSources)
	// The publisher is usually served with a public certificate, but may use
	// the cluster CA. init/import and serve share this client. It never
	// carries node keys.
	if s.releases.Client, err = publisherClient(c.CAFile); err != nil {
		s.close()
		return nil, err
	}
	// A newer gateway adds its columns before serving; the schema is additive and
	// idempotent, so older gateways still running are unaffected.
	if command == "init" || command == "serve" {
		if err = s.store.EnsureSchema(ctx); err != nil {
			s.close()
			return nil, err
		}
	}
	return s, nil
}

// initCluster imports an optional release manifest and records the cluster's
// baseline. It never starts a core.
func (s *shell) initCluster(ctx context.Context, manifestURL string) error {
	c := s.c
	if manifestURL != "" {
		r, err := importRelease(ctx, c, s.releases, manifestURL)
		if err != nil {
			return err
		}
		if err = s.store.PutRelease(ctx, r); err != nil {
			return err
		}
		if c.BaselineDigest == "" {
			c.BaselineDigest = r.Digest
		}
		fmt.Println(r.Digest)
	}
	if !release.ValidDigest(c.BaselineDigest) {
		return errors.New("init requires baseline_digest or -manifest")
	}
	if _, err := s.store.Release(ctx, c.BaselineDigest); err != nil {
		return err
	}
	return s.store.InitCluster(ctx, c.PrimaryNode, c.BaselineDigest)
}

// importRelease records the release named by a manifest without touching
// the cluster baseline.
func (s *shell) importRelease(ctx context.Context, manifestURL string) error {
	if manifestURL == "" {
		return errors.New("import requires -manifest")
	}
	r, err := importRelease(ctx, s.c, s.releases, manifestURL)
	if err != nil {
		return err
	}
	if err = s.store.PutRelease(ctx, r); err != nil {
		return err
	}
	fmt.Println(r.Digest)
	return nil
}

// serveGateway serves until a stop signal or a listener failure, or until a
// gateway plan switched this node's gateway: it then shuts down as on a
// signal and execs the bundled binary, which starts the gateway the node
// selects. Should that exec fail, it serves again with this binary, and the
// plan's gw-confirm step reports the node's unchanged version.
func serveGateway(ctx context.Context, cancel context.CancelFunc, c config, bootstrap bool) error {
	for {
		set, err := newServeSet(ctx, c)
		if err != nil {
			return err
		}
		err = set.serve(ctx, cancel, bootstrap)
		set.close()
		if err != nil || !set.handingOff.Load() || ctx.Err() != nil {
			return err
		}
		err = restartGateway(execGateway)
		slog.Error("gateway restart failed; serving again with this binary", "error", err)
	}
}

// serveSet holds the long-lived objects of a serving gateway, so the lifecycle
// is one set of fields rather than a web of captured closures.
type serveSet struct {
	*shell
	supervisor      *supervisor.Manager
	managementToken string
	cert            tls.Certificate
	ca              *x509.CertPool
	clientTLS       *tls.Config
	ownNode         control.Node
	peerCache       atomic.Value
	router          *proxy.Router
	rt              *control.LocalRuntime
	pluginBlobs     *pluginblob.Service
	engine          *control.Engine
	local           *http.Server
	public          *http.Server
	private         *http.Server
	listener        net.Listener
	cleanups        []func()
	// handingOff is set when a gateway plan restarts this process.
	handingOff atomic.Bool
}

// newServeSet opens the shared shell and builds the serving pieces in dependency
// order. Every error path closes what was already opened.
func newServeSet(ctx context.Context, c config) (*serveSet, error) {
	s, err := newShell(ctx, c, "serve")
	if err != nil {
		return nil, err
	}
	set := &serveSet{shell: s}
	if err := set.build(ctx); err != nil {
		set.close()
		return nil, err
	}
	return set, nil
}

// cleanup registers a closer to run on shutdown, in reverse registration order.
func (s *serveSet) cleanup(fn func()) { s.cleanups = append(s.cleanups, fn) }

// close releases the serving resources and then the shared shell.
func (s *serveSet) close() {
	for i := len(s.cleanups) - 1; i >= 0; i-- {
		s.cleanups[i]()
	}
	s.cleanups = nil
	s.shell.close()
}

// build wires supervisor, identity, cache, peer manager, router, runtime,
// plugins, engine and the three listeners. The order is load-bearing: hardening
// runs before any core, the token is written before any core starts, and the
// listeners are up before recovery so operators can inspect a failed candidate.
func (s *serveSet) build(ctx context.Context) error {
	c := s.c
	// Before any core or plugin exists: processes of the same UID can no
	// longer read this process's environment (it holds every secret) or
	// memory through /proc.
	if err := hardenProcess(); err != nil {
		return err
	}
	sv, err := supervisor.New(filepath.Join(c.Root, "runtime"))
	if err != nil {
		return err
	}
	s.supervisor = sv
	s.cleanup(func() { _ = sv.Close() })
	// A fresh management token per boot, written before any core starts.
	managementToken, err := control.NewManagementToken()
	if err != nil {
		return err
	}
	s.managementToken = managementToken
	if err = control.WriteManagementToken(c.ManagementTokenFile, managementToken); err != nil {
		return fmt.Errorf("write management token: %w", err)
	}
	cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
	if err != nil {
		return err
	}
	s.cert = cert
	ca, err := clusterCAPool(c.CAFile)
	if err != nil {
		return err
	}
	s.ca = ca
	s.clientTLS = &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: ca}
	redisClient, err := openRedis(c)
	if err != nil {
		return err
	}
	s.cleanup(func() { _ = redisClient.Close() })
	if opt := redisClient.Options(); opt.Password == "" && opt.Network != "unix" {
		// Every process on the cluster network that reaches an open cache
		// can rewrite node registrations, locks and upgrade state.
		if !c.AllowUnauthenticatedRedis {
			return errors.New("REDIS_URL has no password; enable requirepass on the cache and put the password in the URL (or set allow_unauthenticated_redis for an isolated test setup)")
		}
		slog.Warn("REDIS_URL has no password and allow_unauthenticated_redis is set")
	}
	boot := make([]byte, 16)
	if _, err = rand.Read(boot); err != nil {
		return err
	}
	s.ownNode = control.Node{ID: c.NodeID, PeerURL: c.PeerURL, ShellBootID: hex.EncodeToString(boot), OS: runtime.GOOS, Arch: runtime.GOARCH, RuntimeABI: c.RuntimeABI, PeerProtocol: peer.Protocol, Strategy: control.PrimaryFirst, GatewayVersion: Version, SelfUpdate: selfUpdatable(s.releases)}
	// Configure structured logging with cluster context
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(handler).With(
		"cluster_id", c.ClusterID,
		"node_id", c.NodeID,
		"shell_boot_id", s.ownNode.ShellBootID,
	)
	slog.SetDefault(logger)
	locks := control.NewRedisLocks(redisClient)
	s.store.Locks = locks
	s.store.Redis = redisClient
	peerManager, err := s.newPeerManager(redisClient)
	if err != nil {
		return err
	}
	// A disabled, conflicting or Redis-less boot stays up in maintenance with its
	// management socket; Run and the engine heartbeat keep retrying registration,
	// and every engine pass refuses work until it succeeds.
	if err = peerManager.Maintain(ctx); err != nil {
		slog.Error("node registration pending", "error", err)
	}
	go peerManager.Run(ctx)
	s.cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		_ = peerManager.Unregister(cleanup)
	})
	if err = s.buildRuntime(ctx, peerManager, locks); err != nil {
		return err
	}
	return s.listen()
}

// newPeerManager builds the Redis-backed peer identity manager.
func (s *serveSet) newPeerManager(redisClient *redis.Client) (*peer.Manager, error) {
	c := s.c
	ownNode := s.ownNode
	manager, err := peer.New(peer.Config{Redis: redisClient, Cluster: c.ClusterID, NodeID: c.NodeID, BootID: ownNode.ShellBootID, ConfiguredKey: c.PeerAuthKey, Validate: func(ctx context.Context, id peer.Identity) error {
		return s.store.ValidateNode(ctx, id.NodeID, id.BootID)
	}, Register: func(ctx context.Context) error { return s.store.RegisterLocked(ctx, ownNode) }, WithRegistration: func(ctx context.Context, fn func(context.Context) error) error {
		return s.store.WithRegistration(ctx, c.NodeID, fn)
	}})
	if err != nil {
		return nil, err
	}
	s.store.RevokePeer = manager.Revoke
	return manager, nil
}

// buildRuntime builds the router, local runtime, plugin packages, CPU sampler and
// engine, and starts the peer cache poller and background collectors.
func (s *serveSet) buildRuntime(ctx context.Context, peerManager *peer.Manager, locks *control.RedisLocks) error {
	c := s.c
	// The node key goes to the primary, or to a serving node while this node
	// offloads its new requests because of CPU load.
	var router *proxy.Router
	authorizeTarget := func(ctx context.Context, u *url.URL) error {
		return s.store.AuthorizeTarget(ctx, c.NodeID, u, router != nil && router.Offloading())
	}
	peerTransport := peerManager.WrapTransport(proxy.PeerTransport(s.clientTLS), authorizeTarget)
	trustedProxies, err := parseTrustedProxies(c.TrustedProxies)
	if err != nil {
		return err
	}
	s.peerCache.Store([]control.Node{})
	go s.pollPeers(ctx)
	var rt *control.LocalRuntime
	// LocalReady is asked on every public request; the core status behind it
	// reads PostgreSQL, so the core is taken as ready until a request fails to
	// reach it, then asked at most every 30 s (proxy.LazyReady).
	localReady := proxy.NewLazyReady(func() bool {
		if rt == nil {
			return false
		}
		statusCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		st, _, err := rt.Status(statusCtx)
		return err == nil && st.Ready
	}, 30*time.Second)
	router = proxy.New(proxy.Config{NodeID: c.NodeID, PeerTLS: s.clientTLS, PeerTransport: peerTransport, TrustedProxies: trustedProxies, PeerReady: func(route proxy.Route) bool {
		for _, n := range s.peerCache.Load().([]control.Node) {
			if n.Enabled && n.PeerURL == route.PeerURL && n.CoreBootID == route.CoreBootID && n.RouteRevision == route.PeerRevision && n.ServingLocal(time.Now()) {
				return true
			}
		}
		return false
	}, LocalReady: localReady.Ready, LocalSucceeded: localReady.Succeeded, LocalFailed: localReady.Failed, LocalServing: localReady.Succeeded})
	s.router = router
	// A core that exited is not ready, whatever the last request said; the
	// next request asks the core instead of being sent to a dead socket.
	s.supervisor.OnExit = func(supervisor.State) { localReady.Failed() }
	rt = &control.LocalRuntime{Releases: s.releases, Supervisor: s.supervisor, Router: router, NodeID: c.NodeID, PrimaryNode: c.PrimaryNode, CoreSocket: c.CoreSocket, ManagementSocket: c.ManagementSocket, ManagementTokenFile: c.ManagementTokenFile, CoreURL: c.CoreURL, Root: c.Root, Args: c.CoreArgs, Env: c.coreEnvironment(), InheritEnv: c.CoreEnvInherit}
	s.rt = rt
	rt.OnStopped = func(ctx context.Context, coreBoot string) error {
		return s.store.ConfirmStoppedCore(ctx, c.NodeID, s.ownNode.ShellBootID, coreBoot)
	}
	rt.Peer = peerManager
	rt.SourceAsset = s.store.OpenSourceAsset
	rt.PeerArtifactClient = peer.NewClient(peerTransport, 30*time.Minute)
	// Package fetches may reach any enabled node: a missing package comes
	// from whichever online node holds it. Only that client is authorized
	// this way.
	packageClient := peer.NewClient(peerManager.WrapTransport(proxy.PeerTransport(s.clientTLS), func(ctx context.Context, u *url.URL) error {
		return s.store.AuthorizeAnyNodeTarget(ctx, c.NodeID, u)
	}), runtimecontract.PluginPackageTransferTimeout)
	pluginBlobs := control.PluginBlobs(s.store, c.NodeID, c.Root, packageClient, c.PluginMaxBytes, c.PluginStoreMaxBytes)
	s.pluginBlobs = pluginBlobs
	rt.PluginBlobs = pluginBlobs.Peer()
	go control.CollectPluginBlobs(ctx, s.store, pluginBlobs, 10*time.Minute)
	rt.AuthorizePeer = func(ctx context.Context, id peer.Identity, scope, digest string) error {
		return s.store.AuthorizePeer(ctx, id.NodeID, id.BootID, c.NodeID, scope, digest)
	}
	cpu := &cpuload.Sampler{Counters: cpuload.Counters(), Window: 10}
	go cpu.Run(ctx, time.Second)
	s.engine = &control.Engine{Store: s.store, Locks: locks, Runtime: rt, Node: s.ownNode, PeerMaintain: peerManager.Maintain, PeerCheck: peerManager.Check, CPU: cpu.Percent, BundledVersion: bundledVersion()}
	if s.ownNode.SelfUpdate {
		s.engine.Gateway = s.releases
	}
	return nil
}

// pollPeers refreshes the node snapshot the router's PeerReady callback reads.
func (s *serveSet) pollPeers(ctx context.Context) {
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	for {
		lookup, cancel := context.WithTimeout(ctx, 2*time.Second)
		nodes, err := s.store.Nodes(lookup)
		cancel()
		if err == nil {
			s.peerCache.Store(nodes)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// listen binds the management socket and constructs the three servers.
func (s *serveSet) listen() error {
	c := s.c
	if err := os.MkdirAll(filepath.Dir(c.ManagementSocket), 0700); err != nil {
		return err
	}
	if info, err := os.Lstat(c.ManagementSocket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return errors.New("management socket path is occupied by a non-socket")
		}
		if err = os.Remove(c.ManagementSocket); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	listener, err := net.Listen("unix", c.ManagementSocket)
	if err != nil {
		return err
	}
	s.listener = listener
	s.cleanup(func() { _ = listener.Close() })
	if err = os.Chmod(c.ManagementSocket, 0600); err != nil {
		return err
	}
	s.local = &http.Server{Handler: control.RequireManagementToken(s.managementToken, control.ManagementHandler(s.store, s.pluginBlobs, s.engine)), ReadHeaderTimeout: 5 * time.Second}
	s.public = &http.Server{Addr: c.PublicAddr, Handler: s.router.Public(), ReadHeaderTimeout: 15 * time.Second}
	s.private = &http.Server{Addr: c.PeerAddr, Handler: s.rt.PrivateHandler(), TLSConfig: proxy.ServerTLS(s.cert, s.ca), ReadHeaderTimeout: 15 * time.Second}
	return nil
}

// serve starts the listeners and the engine, waits for a stop signal or a
// listener failure, then shuts everything down. cancel stops the signal context
// so a failed listener also stops the engine and its background work.
func (s *serveSet) serve(ctx context.Context, cancel context.CancelFunc, bootstrap bool) error {
	c := s.c
	ctx, handoff := context.WithCancel(ctx)
	defer handoff()
	s.engine.Handoff = func() { s.handingOff.Store(true); handoff() }
	errs := make(chan error, 3)
	go func() { errs <- s.local.Serve(s.listener) }()
	go func() { errs <- s.public.ListenAndServe() }()
	go func() { errs <- s.private.ListenAndServeTLS("", "") }()
	// Management and artifact listeners remain available even if a candidate
	// fails startup, so operators can inspect/recover without a public admin port.
	recoveryCtx, recoveryCancel := context.WithTimeout(ctx, 10*time.Minute)
	_, err := s.engine.Locks.WithLock(recoveryCtx, "system:upgrade-node:"+c.ClusterID+":"+c.NodeID, 10*time.Minute, func(ctx context.Context) error { return s.engine.Recover(ctx, bootstrap) })
	recoveryCancel()
	if err != nil {
		slog.Error("startup reconciliation paused", "error", err)
	}
	// This binary reached serving: a later startup may hand off to it again.
	if err = s.releases.ServingGateway(Version); err != nil {
		slog.Warn("gateway handoff count not cleared", "error", err)
	}
	engineDone := make(chan struct{})
	go func() { _ = s.engine.Run(ctx); close(engineDone) }()
	go s.store.RunUpdateChecks(ctx, c.NodeID)
	select {
	case <-ctx.Done():
	case err = <-errs:
		cancel()
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer stopCancel()
	// A handing-off node keeps forwarding to the serving nodes it yielded to
	// until its listeners close.
	if !s.handingOff.Load() {
		_ = s.router.SetRoute(proxy.Route{Mode: "maintenance", Revision: time.Now().UnixNano()})
	}
	// The engine releases its locks before the cache closes, so that the
	// gateway that follows a handoff takes them at once.
	select {
	case <-engineDone:
	case <-stopCtx.Done():
	}
	stopErr := s.supervisor.Terminate(stopCtx)
	_ = s.public.Shutdown(stopCtx)
	_ = s.private.Shutdown(stopCtx)
	_ = s.local.Shutdown(stopCtx)
	if stopErr != nil {
		return stopErr
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// clusterCAPool loads the cluster CA used for peer TLS.
func clusterCAPool(caFile string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, err
	}
	ca := x509.NewCertPool()
	if !ca.AppendCertsFromPEM(pem) {
		return nil, errors.New("invalid cluster CA")
	}
	return ca, nil
}

// parseTrustedProxies parses the configured CIDRs in order.
func parseTrustedProxies(cidrs []string) ([]*net.IPNet, error) {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted_proxies CIDR %q: %w", cidr, err)
		}
		out = append(out, network)
	}
	return out, nil
}

// publisherClient trusts system roots plus the cluster CA when configured. It has
// no total Timeout: release.Manager bounds a manifest by deadline and a bundle by
// download progress, so a slow link can still finish a large bundle.
func publisherClient(caFile string) (*http.Client, error) {
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, err
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("invalid cluster CA")
		}
	}
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}}, nil
}
func readConfig(path string) (config, error) {
	var c config
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	// Compose passes an unset per-node variable as an empty string; only a
	// non-empty value overrides the file, and it must then be valid.
	if value := os.Getenv("SUB2API_PEER_AUTH_KEY"); value != "" {
		if err := peer.ValidateKey(value); err != nil {
			return c, err
		}
		c.PeerAuthKey = value
	}
	if c.PeerAuthKey != "" {
		if err := peer.ValidateKey(c.PeerAuthKey); err != nil {
			return c, err
		}
	}
	if v := os.Getenv("DATABASE_URL"); v != "" {
		c.DatabaseURL = v
	}
	if v := os.Getenv("REDIS_URL"); v != "" {
		c.RedisURL = v
	}
	if c.Root == "" {
		c.Root = "/var/lib/sub2api"
	}
	if c.PublicAddr == "" {
		c.PublicAddr = ":8080"
	}
	if c.PeerAddr == "" {
		c.PeerAddr = ":7443"
	}
	if c.CoreURL == "" {
		c.CoreURL = "http://127.0.0.1:18080"
	}
	if c.ManagementSocket == "" {
		c.ManagementSocket = filepath.Join(c.Root, "runtime", "shell.sock")
	}
	if c.CoreSocket == "" {
		c.CoreSocket = filepath.Join(c.Root, "runtime", "core.sock")
	}
	if c.ManagementTokenFile == "" {
		c.ManagementTokenFile = defaultTokenFile(c.ManagementSocket)
	}
	if c.ClusterID == "" || c.NodeID == "" || c.PrimaryNode == "" || c.RuntimeABI == "" || c.DatabaseURL == "" || c.RedisURL == "" {
		return c, errors.New("cluster_id, node_id, primary_node, runtime_abi, DATABASE_URL and REDIS_URL are required")
	}
	if !filepath.IsAbs(c.Root) || !filepath.IsAbs(c.CoreSocket) || !filepath.IsAbs(c.ManagementSocket) || !filepath.IsAbs(c.ManagementTokenFile) {
		return c, errors.New("root, socket and token paths must be absolute")
	}
	u, err := url.Parse(c.PeerURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" || u.User != nil {
		return c, errors.New("peer_url must be an HTTPS origin")
	}
	return c, nil
}

// The supervised core must use this gateway's approved cluster stores. Append
// these last so neither inherited process variables nor core_env can silently
// point the child at a different database or lock namespace.
//
// On Linux the plugin sandbox's Landlock rules are turned on as well: they
// are what keeps a plugin of the same UID away from the management token and
// the node state. A value the operator set explicitly wins: in core_env, in
// the gateway's own environment (the core inherits it), or by
// plugin_landlock_disabled (which sets it false: cores from 0.1.304 on
// default to true). The explicit true stays while this gateway may start a
// core older than that (default false); drop it with their support.
func (c config) coreEnvironment() []string {
	env := append([]string(nil), c.CoreEnv...)
	_, inherited := os.LookupEnv(pluginLandlockEnv)
	if runtime.GOOS == "linux" && !inherited && !hasEnv(env, pluginLandlockEnv) {
		env = append(env, pluginLandlockEnv+"="+strconv.FormatBool(!c.PluginLandlockDisabled))
	}
	return append(env, "DATABASE_URL="+c.DatabaseURL, "REDIS_URL="+c.RedisURL)
}

const pluginLandlockEnv = "SUB2API_PLUGIN_LANDLOCK"

// masterCipher seals update-source secrets with the cluster master key the
// core gets: core_env wins over the gateway's own environment, as in the
// supervisor. Without a key it is nil, and only secretless sources work.
func (c config) masterCipher() (*secret.Cipher, error) {
	key := os.Getenv(masterKeyEnv)
	for _, kv := range c.CoreEnv {
		if v, ok := strings.CutPrefix(kv, masterKeyEnv+"="); ok {
			key = v
		}
	}
	if key == "" {
		return nil, nil
	}
	b, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(b) != 32 {
		return nil, errors.New(masterKeyEnv + " must be base64 of exactly 32 bytes")
	}
	return secret.New(b)
}

const masterKeyEnv = "SUB2API_MASTER_KEY"

func hasEnv(env []string, key string) bool {
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); k == key {
			return true
		}
	}
	return false
}
func importRelease(ctx context.Context, c config, m *release.Manager, raw string) (control.Release, error) {
	var out control.Release
	u, err := url.Parse(raw)
	if err != nil {
		return out, err
	}
	origin, err := url.Parse(c.ReleaseOrigin)
	if err != nil || origin.Scheme != "https" || origin.Host == "" {
		return out, errors.New("release_origin must be configured as HTTPS")
	}
	if u.Scheme != origin.Scheme || u.Host != origin.Host || u.User != nil || !strings.HasPrefix(u.Path, strings.TrimRight(origin.Path, "/")+"/") {
		return out, errors.New("manifest must come from the configured release origin")
	}
	b, err := m.FetchManifest(ctx, raw)
	if err != nil {
		return out, err
	}
	manifest, digest, err := release.Verify(b)
	if err != nil {
		return out, err
	}
	return control.Release{Digest: digest, Manifest: manifest, Raw: b, BundleBase: strings.TrimRight(c.ReleaseOrigin, "/")}, nil
}

// defaultTokenFile is where the management token lives unless configured:
// beside the management socket, in the shell's private runtime directory.
func defaultTokenFile(socket string) string {
	return filepath.Join(filepath.Dir(socket), "shell.token")
}

func localCommand(command, socket, tokenFile, id string) error {
	method, path := "GET", "/system/upgrades"
	var body io.Reader
	switch command {
	case "status":
	case "yield", "unyield", "yield-status":
		// This node's traffic handover before a shell restart (CONTRACTS
		// §82.3): yield drains too, so an ingress health check moves on.
		path = "/system/nodes/self/yield"
		switch command {
		case "yield":
			method, body = "POST", strings.NewReader(`{"drain":true}`)
		case "unyield":
			method = "DELETE"
		}
	default:
		if id == "" {
			return errors.New("-id is required")
		}
		method = "POST"
		if command == "enable-node" || command == "disable-node" {
			path = "/system/nodes/" + url.PathEscape(id) + "/" + strings.TrimSuffix(command, "-node")
		} else {
			path += "/" + url.PathEscape(id) + "/" + command
		}
	}
	token, err := control.ReadManagementToken(tokenFile)
	if err != nil {
		return fmt.Errorf("read management token (run as the gateway user while the gateway is serving): %w", err)
	}
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	req, _ := http.NewRequest(method, "http://shell"+path, body)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Updater-Actor", "local-operator")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, err = io.Copy(os.Stdout, io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 400 {
		return fmt.Errorf("gateway returned HTTP %d", res.StatusCode)
	}
	return nil
}

func clusterCommand(command string, c config, node string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := openPool(c)
	if err != nil {
		return err
	}
	defer closePool(db)
	redisClient, err := openRedis(c)
	if err != nil {
		return err
	}
	defer redisClient.Close()
	store := &control.Store{
		DB:      db,
		Cluster: c.ClusterID,
		Locks:   control.NewRedisLocks(redisClient),
		Redis:   redisClient,
	}
	if command == "set-primary" {
		return store.SetPrimary(ctx, node)
	}
	if command == "remove-node" {
		return store.RemoveNode(ctx, node)
	}
	return errors.New("unknown cluster command")
}

// parseRedisURL parses the cache server URL: any Redis-protocol server,
// Valkey by default. Besides go-redis's redis://, rediss:// and unix:// it
// accepts valkey:// and valkeys:// (TLS), which mean redis:// and rediss://.
// The URL is handed to the core unchanged, which accepts the same schemes
// (server/internal/cluster.ParseRedisURL).
func parseRedisURL(raw string) (*redis.Options, error) {
	raw = strings.TrimSpace(raw)
	if scheme, rest, ok := strings.Cut(raw, "://"); ok {
		switch strings.ToLower(scheme) {
		case "valkey":
			raw = "redis://" + rest
		case "valkeys":
			raw = "rediss://" + rest
		}
	}
	return redis.ParseURL(raw)
}
