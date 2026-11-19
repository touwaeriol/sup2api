//go:build linux

package control

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	mrand "math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/gateway/internal/peer"
	"github.com/Sub2API-Devs/sup2api/next/gateway/internal/proxy"
	"github.com/Sub2API-Devs/sup2api/next/gateway/internal/release"
	"github.com/Sub2API-Devs/sup2api/next/gateway/internal/supervisor"
	rc "github.com/Sub2API-Devs/sup2api/next/runtime-contract"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// The real-core harness runs every shell in this test process but with its
// own state root, supervisor, HTTPS listeners, Redis registration and real
// core process group. Starting a shell repeats what sub2api-gateway serve does,
// so a restart is a new shell boot over the same state root.

type bundledFile struct {
	name string
	data []byte
}

type realNode struct {
	id, root        string
	engine          *Engine
	runtime         *LocalRuntime
	peer            *peer.Manager
	public, private *httptest.Server
	management      *http.Server
	supervisor      *supervisor.Manager
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	// forwards counts forwarded requests arriving at the private listener.
	forwards *atomic.Int64
	// cpu is the CPU load the engine reports; negative is unmeasured.
	cpu *atomic.Int64
}

type realOptions struct {
	// peerTTL shortens Redis registrations so tests can cross several of them.
	peerTTL time.Duration
	// coreEnv is appended to every core's environment.
	coreEnv []string
	// standInCores lets a test route nodes to its own loopback servers instead
	// of supervised cores: a local route is then ready without a core.
	standInCores bool
}

type realCluster struct {
	t              *testing.T
	ctx            context.Context
	store          *Store
	db             *gorm.DB
	rdb            *redis.Client // through the proxy, like every shell and core
	direct         *redis.Client // test observation only, bypasses the proxy
	redis          *tcpProxy
	dbname         string
	coreDSN        string
	redisURL       string // proxied URL handed to shells and cores
	cert           tls.Certificate
	ca             *x509.CertPool
	clientTLS      *tls.Config
	artifactClient *http.Client
	s1, s2         string
	old, target    Release
	broken         Release
	bundled        []bundledFile
	// bundledNext replaces same-key packages in R2's bundle (TEST_BUILTIN_NEXT_DIR).
	bundledNext    []bundledFile
	options        realOptions
	configuredKeys map[string]string
	// wrap, when set, observes the runtime of every shell started afterwards.
	wrap func(*realNode) Runtime

	mu    sync.RWMutex
	nodes []*realNode
	roots map[string]string
	boots map[string]int
}

// newRealCluster prepares PG, Redis, releases R1/R2 and a broken
// candidate. It skips unless the real-core environment is configured.
func newRealCluster(t *testing.T, ctx context.Context, options realOptions) *realCluster {
	t.Helper()
	v1, v2 := os.Getenv("TEST_CORE_V1"), os.Getenv("TEST_CORE_V2")
	dsn, redisURL := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_REDIS_URL")
	if v1 == "" || v2 == "" || dsn == "" || redisURL == "" {
		t.Skip("requires TEST_CORE_V1, TEST_CORE_V2, TEST_DATABASE_URL and TEST_REDIS_URL")
	}
	b1, err := os.ReadFile(v1)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := os.ReadFile(v2)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(b1, b2) {
		t.Fatal("R1/R2 must be genuinely different separately built core binaries")
	}
	output := func(path, arg string) string {
		b, e := exec.Command(path, arg).Output()
		if e != nil {
			t.Fatal(e)
		}
		return strings.TrimSpace(string(b))
	}
	c := &realCluster{t: t, ctx: ctx, options: options, configuredKeys: map[string]string{"b": strings.Repeat("b", 32)}, roots: map[string]string{}, boots: map[string]int{}}
	c.s1, c.s2 = output(v1, "schema-contract"), output(v2, "schema-contract")
	version1, version2 := output(v1, "version"), output(v2, "version")
	if c.s1 == "" || c.s2 == "" {
		t.Fatal("both releases require non-empty embedded schema contracts")
	}
	admin := openTestDB(t, dsn)
	c.dbname = fmt.Sprintf("shell_real_%d", time.Now().UnixNano())
	quoted := quoteIdent(c.dbname)
	if err = admin.WithContext(ctx).Exec("CREATE DATABASE " + quoted).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 20*time.Second)
		defer done()
		admin.WithContext(cleanup).Exec("DROP DATABASE " + quoted + " WITH (FORCE)")
	})
	c.coreDSN = dsn + " dbname=" + c.dbname
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, e := url.Parse(dsn)
		if e != nil {
			t.Fatal(e)
		}
		u.Path = "/" + c.dbname
		c.coreDSN = u.String()
	}
	c.db = openTestDB(t, c.coreDSN)
	// Every shell and core reaches Redis through one proxy the test can cut.
	ru, err := url.Parse(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	c.redis = newTCPProxy(t, ru.Host)
	ru.Host = c.redis.Addr()
	c.redisURL = ru.String()
	opt, err := redis.ParseURL(c.redisURL)
	if err != nil {
		t.Fatal(err)
	}
	c.rdb = redis.NewClient(opt)
	t.Cleanup(func() { _ = c.rdb.Close() })
	directOpt, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	c.direct = redis.NewClient(directOpt)
	t.Cleanup(func() { _ = c.direct.Close() })
	c.store = &Store{DB: c.db, Cluster: c.dbname, Locks: NewRedisLocks(c.rdb), Redis: c.rdb}
	if err = c.store.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	c.cert, c.ca = realTestCertificate(t)
	c.clientTLS = &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: c.ca}
	transport := &http.Transport{TLSClientConfig: c.clientTLS}
	t.Cleanup(transport.CloseIdleConnections)
	c.artifactClient = &http.Client{Transport: transport, Timeout: time.Minute}
	var blobsMu sync.Mutex
	blobs := map[string][]byte{}
	source := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		blobsMu.Lock()
		b, ok := blobs[r.URL.Path]
		blobsMu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	source.TLS = proxy.ServerTLS(c.cert, c.ca)
	source.StartTLS()
	t.Cleanup(source.Close)
	if dir := os.Getenv("TEST_BUILTIN_DIR"); dir != "" {
		if os.Getenv("TEST_BUILTIN_KEY") == "" {
			t.Fatal("TEST_BUILTIN_KEY is required with TEST_BUILTIN_DIR")
		}
		paths, e := filepath.Glob(filepath.Join(dir, "*.s2plugin"))
		if e != nil {
			t.Fatal(e)
		}
		if len(paths) == 0 {
			t.Fatal("TEST_BUILTIN_DIR contains no signed packages")
		}
		for _, path := range paths {
			data, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			c.bundled = append(c.bundled, bundledFile{"builtin/" + filepath.Base(path), data})
		}
	}
	// TEST_BUILTIN_NEXT_DIR holds newer versions of some bundled plugins that
	// only R2 carries, so updating the core also updates them.
	if dir := os.Getenv("TEST_BUILTIN_NEXT_DIR"); dir != "" {
		paths, e := filepath.Glob(filepath.Join(dir, "*.s2plugin"))
		if e != nil || len(paths) == 0 {
			t.Fatalf("TEST_BUILTIN_NEXT_DIR has no packages: %v", e)
		}
		for _, path := range paths {
			data, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			c.bundledNext = append(c.bundledNext, bundledFile{"builtin/" + filepath.Base(path), data})
		}
	}
	makeRelease := func(id, version string, core []byte) Release {
		var buffer bytes.Buffer
		gz := gzip.NewWriter(&buffer)
		tw := tar.NewWriter(gz)
		if err := tw.WriteHeader(&tar.Header{Name: "bin/sub2api", Mode: 0755, Size: int64(len(core))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(core); err != nil {
			t.Fatal(err)
		}
		files := []rc.File{{Path: "bin/sub2api", SHA256: release.Digest(core), Size: int64(len(core)), Mode: 0755}}
		bundledFiles := c.bundled
		if id == "r2" && len(c.bundledNext) > 0 {
			bundledFiles = bundleWith(c.bundled, c.bundledNext)
		}
		for _, file := range bundledFiles {
			if err := tw.WriteHeader(&tar.Header{Name: file.name, Mode: 0644, Size: int64(len(file.data))}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write(file.data); err != nil {
				t.Fatal(err)
			}
			files = append(files, rc.File{Path: file.name, SHA256: release.Digest(file.data), Size: int64(len(file.data)), Mode: 0644})
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		bundle := append([]byte(nil), buffer.Bytes()...)
		digest := release.Digest(bundle)
		after, before := c.s2, c.s1
		if id == "r1" {
			after = c.s1
		}
		if id == "broken-candidate" {
			before = c.s2
		}
		m := rc.Manifest{CoreVersion: version, ManifestVersion: 1, ReleaseID: id, BuildID: release.Digest(core), SourceCommit: id, CreatedAt: time.Now(), Platforms: []rc.Platform{{OS: "linux", Arch: runtime.GOARCH, RuntimeABI: "test", BundleDigest: digest, BundleBytes: int64(len(bundle)), Files: files}}, ShellProtocol: rc.Range{Min: rc.Protocol, Max: rc.Protocol}, CoreControlProtocol: rc.Range{Min: rc.Protocol, Max: rc.Protocol}, ClusterProtocol: rc.Range{Min: 1, Max: 1}, TaskProtocol: rc.Range{Min: 1, Max: 1}, SchemaBefore: before, SchemaAfter: after, Strategy: "maintenance", HostAPIVersion: rc.HostAPIVersion}
		raw, e := json.Marshal(m)
		if e != nil {
			t.Fatal(e)
		}
		blobsMu.Lock()
		blobs["/blobs/"+digest+".tar.gz"] = bundle
		blobsMu.Unlock()
		return Release{Digest: release.Digest(raw), Manifest: m, Raw: raw, BundleBase: source.URL + "/blobs"}
	}
	c.old, c.target = makeRelease("r1", version1, b1), makeRelease("r2", version2, b2)
	c.broken = makeRelease("broken-candidate", version2, []byte("#!/bin/sh\nexit 23\n"))
	for _, r := range []Release{c.old, c.target, c.broken} {
		if err = c.store.PutRelease(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if err = c.store.InitCluster(ctx, "a", c.old.Digest); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, n := range c.list() {
			c.stopShell(n)
		}
		c.mu.RLock()
		defer c.mu.RUnlock()
		for _, root := range c.roots {
			_ = os.RemoveAll(root)
		}
	})
	return c
}

func (c *realCluster) list() []*realNode {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]*realNode(nil), c.nodes...)
}

func (c *realCluster) node(id string) *realNode {
	for _, n := range c.list() {
		if n.id == id {
			return n
		}
	}
	return nil
}

// startShell starts a new shell boot for id, reusing its state root. Like
// sub2api-gateway serve it registers and keeps the registration alive but does
// not run the engine until run is called.
func (c *realCluster) startShell(id string) *realNode {
	t := c.t
	t.Helper()
	c.mu.Lock()
	c.boots[id]++
	boot := fmt.Sprintf("%s-shell-%d", id, c.boots[id])
	root := c.roots[id]
	c.mu.Unlock()
	if root == "" {
		var err error
		if root, err = os.MkdirTemp("", "s2-real-"); err != nil {
			t.Fatal(err)
		}
		c.mu.Lock()
		c.roots[id] = root
		c.mu.Unlock()
	}
	mgr, err := supervisor.New(filepath.Join(root, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	store := c.store
	coreAddr := freeAddress(t)
	rm := &release.Manager{Root: root, OS: "linux", Arch: runtime.GOARCH, RuntimeABI: "test", Client: c.artifactClient}
	env := []string{
		"DATABASE_URL=" + c.coreDSN, "REDIS_URL=" + c.redisURL, "SUB2API_MASTER_KEY=" + base64.StdEncoding.EncodeToString(make([]byte, 32)), "SUB2API_PLUGIN_DIR=" + filepath.Join(root, "plugins"), "SUB2API_LOG_LEVEL=error",
	}
	if key := os.Getenv("TEST_BUILTIN_KEY"); key != "" {
		env = append(env, "SUB2API_BUILTIN_TRUST_KEY="+key)
	}
	if dev := os.Getenv("TEST_PLUGIN_DEV_MODE"); dev != "" {
		env = append(env, "SUB2API_PLUGIN_DEV_MODE="+dev)
	}
	env = append(env, c.options.coreEnv...)
	rt := &LocalRuntime{Releases: rm, Supervisor: mgr, NodeID: id, PrimaryNode: "a", CoreSocket: filepath.Join(root, "core.sock"), ManagementSocket: filepath.Join(root, "shell.sock"), ManagementTokenFile: filepath.Join(root, "shell.token"), CoreURL: "http://" + coreAddr, Root: root, Env: env}
	ownNode := Node{ID: id, ShellBootID: boot, OS: "linux", Arch: runtime.GOARCH, RuntimeABI: "test", PeerProtocol: PeerProtocol, Strategy: PrimaryFirst}
	pm, err := peer.New(peer.Config{Redis: c.rdb, Cluster: c.dbname, NodeID: id, BootID: boot, ConfiguredKey: c.configuredKeys[id], TTL: c.options.peerTTL, Validate: func(ctx context.Context, p peer.Identity) error { return store.ValidateNode(ctx, p.NodeID, p.BootID) }, Register: func(ctx context.Context) error { return store.RegisterLocked(ctx, ownNode) }, WithRegistration: func(ctx context.Context, fn func(context.Context) error) error {
		return store.WithRegistration(ctx, id, fn)
	}})
	if err != nil {
		t.Fatal(err)
	}
	var router *proxy.Router
	peerTransport := pm.WrapTransport(proxy.PeerTransport(c.clientTLS), func(ctx context.Context, u *url.URL) error {
		return store.AuthorizeTarget(ctx, id, u, router != nil && router.Offloading())
	})
	rt.Peer = pm
	rt.PeerArtifactClient = peer.NewClient(peerTransport, time.Minute)
	// As under sub2api-gateway serve, package fetches use their own client,
	// which may reach any enabled node (the release client only the primary).
	packageClient := peer.NewClient(pm.WrapTransport(proxy.PeerTransport(c.clientTLS), func(ctx context.Context, u *url.URL) error {
		return store.AuthorizeAnyNodeTarget(ctx, id, u)
	}), time.Minute)
	blobs := PluginBlobs(store, id, root, packageClient, 0, 0)
	rt.PluginBlobs = blobs.Peer()
	// The core reaches its plugin packages through this socket, as under
	// sub2api-gateway serve, with a per-boot token.
	managementToken, err := NewManagementToken()
	if err != nil {
		t.Fatal(err)
	}
	if err = WriteManagementToken(rt.ManagementTokenFile, managementToken); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(rt.ManagementSocket)
	managementListener, err := net.Listen("unix", rt.ManagementSocket)
	if err != nil {
		t.Fatal(err)
	}
	management := &http.Server{Handler: RequireManagementToken(managementToken, ManagementHandler(store, blobs, nil)), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = management.Serve(managementListener) }()
	rt.AuthorizePeer = func(ctx context.Context, p peer.Identity, scope, digest string) error {
		return store.AuthorizePeer(ctx, p.NodeID, p.BootID, id, scope, digest)
	}
	rt.OnStopped = func(ctx context.Context, coreBoot string) error {
		return store.ConfirmStoppedCore(ctx, id, boot, coreBoot)
	}
	router = proxy.New(proxy.Config{NodeID: id, PeerTLS: c.clientTLS, PeerTransport: peerTransport, LocalReady: func() bool {
		if c.options.standInCores {
			return true
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		st, _, e := rt.Status(ctx)
		return e == nil && st.Ready
	}})
	rt.Router = router
	forwards := new(atomic.Int64)
	privateHandler := rt.PrivateHandler()
	private := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if strings.HasPrefix(q.URL.Path, "/internal/forward/") {
			forwards.Add(1)
		}
		privateHandler.ServeHTTP(w, q)
	}))
	private.TLS = proxy.ServerTLS(c.cert, c.ca)
	private.StartTLS()
	public := httptest.NewServer(router.Public())
	ownNode.PeerURL = private.URL
	n := &realNode{id: id, root: root, runtime: rt, peer: pm, public: public, private: private, management: management, supervisor: mgr, forwards: forwards, cpu: new(atomic.Int64)}
	n.cpu.Store(10)
	n.engine = &Engine{Store: store, Locks: store.Locks, Runtime: rt, Node: ownNode, PeerMaintain: pm.Maintain, PeerCheck: pm.Check, CPU: func() (float64, bool) { v := n.cpu.Load(); return float64(v), v >= 0 }}
	if c.wrap != nil {
		n.engine.Runtime = c.wrap(n)
	}
	n.ctx, n.cancel = context.WithCancel(c.ctx)
	// Registration can briefly wait for the cluster change lock.
	deadline := time.Now().Add(time.Minute)
	for {
		if err = pm.Maintain(n.ctx); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("shell %s did not register: %v", boot, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	n.wg.Add(1)
	go func() { defer n.wg.Done(); pm.Run(n.ctx) }()
	c.mu.Lock()
	replaced := false
	for i := range c.nodes {
		if c.nodes[i].id == id {
			c.nodes[i], replaced = n, true
		}
	}
	if !replaced {
		c.nodes = append(c.nodes, n)
	}
	c.mu.Unlock()
	return n
}

// run starts the engine loop of a started shell.
func (c *realCluster) run(n *realNode) {
	n.wg.Add(1)
	go func() { defer n.wg.Done(); _ = n.engine.Run(n.ctx) }()
}

// stopShell repeats the shell's own shutdown: close admission, terminate the
// supervised core with SIGTERM, then the listeners and the registration.
func (c *realCluster) stopShell(n *realNode) {
	n.cancel()
	n.wg.Wait()
	_ = n.runtime.Router.SetRoute(proxy.Route{Mode: "maintenance", Revision: time.Now().UnixNano()})
	stop, done := context.WithTimeout(context.Background(), 30*time.Second)
	defer done()
	_ = n.supervisor.Terminate(stop)
	n.public.CloseClientConnections()
	n.public.Close()
	n.private.CloseClientConnections()
	n.private.Close()
	_ = n.peer.Unregister(stop)
	_ = n.management.Close()
	_ = n.supervisor.Close()
}

// restartShell replaces a shell by a new boot over the same state root and
// reconciles like sub2api-gateway serve before starting the engine loop.
func (c *realCluster) restartShell(id string) *realNode {
	c.t.Helper()
	if old := c.node(id); old != nil {
		c.stopShell(old)
	}
	n := c.startShell(id)
	recover, cancel := context.WithTimeout(n.ctx, 10*time.Minute)
	defer cancel()
	_, err := n.engine.Locks.WithLock(recover, "system:upgrade-node:"+c.dbname+":"+id, 10*time.Minute, func(ctx context.Context) error { return n.engine.Recover(ctx, false) })
	if err != nil {
		c.t.Logf("restarted shell %s reconciliation paused: %v", n.engine.Node.ShellBootID, err)
	}
	c.run(n)
	return n
}

// tcpProxy forwards to target until cut. A cut drops every connection and
// refuses new ones, as a partition between this host and Redis would.
type tcpProxy struct {
	target    string
	ln        net.Listener
	mu        sync.Mutex
	conns     map[net.Conn]struct{}
	downUntil time.Time
}

func newTCPProxy(t *testing.T, target string) *tcpProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &tcpProxy{target: target, ln: ln, conns: map[net.Conn]struct{}{}}
	go p.serve()
	t.Cleanup(func() { _ = ln.Close(); p.dropAll() })
	return p
}

func (p *tcpProxy) Addr() string { return p.ln.Addr().String() }

func (p *tcpProxy) serve() {
	for {
		in, err := p.ln.Accept()
		if err != nil {
			return
		}
		p.mu.Lock()
		down := time.Now().Before(p.downUntil)
		p.mu.Unlock()
		if down {
			_ = in.Close()
			continue
		}
		out, err := net.DialTimeout("tcp", p.target, 5*time.Second)
		if err != nil {
			_ = in.Close()
			continue
		}
		p.mu.Lock()
		p.conns[in], p.conns[out] = struct{}{}, struct{}{}
		p.mu.Unlock()
		pipe := func(dst, src net.Conn) {
			_, _ = io.Copy(dst, src)
			_ = dst.Close()
			_ = src.Close()
			p.mu.Lock()
			delete(p.conns, dst)
			delete(p.conns, src)
			p.mu.Unlock()
		}
		go pipe(out, in)
		go pipe(in, out)
	}
}

func (p *tcpProxy) dropAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for conn := range p.conns {
		_ = conn.Close()
	}
	p.conns = map[net.Conn]struct{}{}
}

// Cut drops all connections and refuses new ones for d.
func (p *tcpProxy) Cut(d time.Duration) {
	p.mu.Lock()
	p.downUntil = time.Now().Add(d)
	p.mu.Unlock()
	p.dropAll()
}

// These checks wrap actual operations; they do not replace the process/runtime.
type observedPrimaryFirstRuntime struct {
	*LocalRuntime
	beforeMaintenance func(context.Context) error
	beforeStart       func(context.Context, string, rc.PrepareRequest) error
}

func (r *observedPrimaryFirstRuntime) Maintenance(ctx context.Context) error {
	if r.beforeMaintenance != nil {
		if err := r.beforeMaintenance(ctx); err != nil {
			return err
		}
	}
	return r.LocalRuntime.Maintenance(ctx)
}
func (r *observedPrimaryFirstRuntime) Start(ctx context.Context, digest string, options rc.PrepareRequest) (rc.Status, error) {
	if r.beforeStart != nil {
		if err := r.beforeStart(ctx, digest, options); err != nil {
			return rc.Status{}, err
		}
	}
	return r.LocalRuntime.Start(ctx, digest, options)
}

// handedOut holds the ports freeAddress returned in this process.
var handedOut sync.Map

// freeAddress returns a loopback address a core listens on later. The port
// lies below the kernel's ephemeral range (Linux starts at 32768): a port
// from 127.0.0.1:0 is free only until one of the test's many outgoing
// connections takes it as its local port, and the core then fails to bind.
func freeAddress(t *testing.T) string {
	t.Helper()
	for range 1000 {
		port := 20000 + mrand.IntN(12000)
		if _, taken := handedOut.LoadOrStore(port, true); taken {
			continue
		}
		l, e := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if e != nil {
			continue
		}
		addr := l.Addr().String()
		_ = l.Close()
		return addr
	}
	t.Fatal("no free loopback port below the ephemeral range")
	return ""
}
func realTestCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-node"}, DNSNames: []string{"test-node"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, template, template, pub, key)
	if e != nil {
		t.Fatal(e)
	}
	pk, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk})
	cert, e := tls.X509KeyPair(certPEM, keyPEM)
	if e != nil {
		t.Fatal(e)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(certPEM)
	return cert, pool
}

// bundleWith replaces the packages of base whose plugin key ("<key>-<version>
// .s2plugin") one of next carries.
func bundleWith(base, next []bundledFile) []bundledFile {
	key := func(f bundledFile) string {
		name := strings.TrimSuffix(filepath.Base(f.name), ".s2plugin")
		if i := strings.Index(name, "-"); i > 0 {
			return name[:i]
		}
		return name
	}
	replaced := map[string]bool{}
	for _, f := range next {
		replaced[key(f)] = true
	}
	out := append([]bundledFile(nil), next...)
	for _, f := range base {
		if !replaced[key(f)] {
			out = append(out, f)
		}
	}
	return out
}
