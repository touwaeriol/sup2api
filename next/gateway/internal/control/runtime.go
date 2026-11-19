package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/gateway/internal/localapi"
	"github.com/Sub2API-Devs/sup2api/next/gateway/internal/peer"
	"github.com/Sub2API-Devs/sup2api/next/gateway/internal/proxy"
	"github.com/Sub2API-Devs/sup2api/next/gateway/internal/release"
	"github.com/Sub2API-Devs/sup2api/next/gateway/internal/supervisor"
	rc "github.com/Sub2API-Devs/sup2api/next/runtime-contract"
)

type LocalRuntime struct {
	OnStopped                                           func(context.Context, string) error
	Releases                                            *release.Manager
	Supervisor                                          *supervisor.Manager
	Router                                              *proxy.Router
	NodeID, CoreSocket, ManagementSocket, CoreURL, Root string
	PrimaryNode                                         string
	Peer                                                *peer.Manager
	PeerArtifactClient                                  *http.Client
	AuthorizePeer                                       func(context.Context, peer.Identity, string, string) error
	// PluginBlobs serves /internal/plugin-blobs/ to the other enabled nodes.
	PluginBlobs http.Handler
	// SourceAsset opens an asset of a release imported from the update
	// source (Store.OpenSourceAsset).
	SourceAsset func(ctx context.Context, r Release, name string, max int64) (io.ReadCloser, error)
	Args, Env   []string
	mu          sync.RWMutex
	routeMu     sync.Mutex
	client      *localapi.Client
	prepared    map[string]release.Prepared
	revision    atomic.Int64
	// ManagementTokenFile holds the management socket's bearer token; the
	// core receives only this path (SUB2API_UPDATER_TOKEN_FILE).
	ManagementTokenFile string
	// InheritEnv names further shell variables a core may inherit besides
	// the supervisor's allowlist.
	InheritEnv []string
}

func (r *LocalRuntime) Prepare(ctx context.Context, v Release, base string) error {
	if release.Digest(v.Raw) != v.Digest {
		return errors.New("stored manifest digest mismatch")
	}
	var platform rc.Platform
	for _, p := range v.Manifest.Platforms {
		if p.OS == r.Releases.OS && p.Arch == r.Releases.Arch && p.RuntimeABI == r.Releases.RuntimeABI {
			platform = p
		}
	}
	bundle := platform.BundleDigest
	if bundle == "" {
		return errors.New("release has no matching platform")
	}
	bundleURL, fromPeer := bundleLocation(base, bundle)
	var p release.Prepared
	var err error
	switch {
	case base == "":
		// Imported from the update source: download through it.
		if r.SourceAsset == nil {
			return errors.New("update source downloads are not configured")
		}
		p, err = r.Releases.PrepareFrom(ctx, v.Raw, func(ctx context.Context) (io.ReadCloser, error) {
			return r.SourceAsset(ctx, v, bundle+".tar.gz", platform.BundleBytes)
		})
	case fromPeer:
		if r.PeerArtifactClient == nil {
			return errors.New("peer artifact client not configured")
		}
		p, err = r.Releases.PrepareWithClient(ctx, v.Raw, bundleURL, r.PeerArtifactClient)
	default:
		p, err = r.Releases.Prepare(ctx, v.Raw, bundleURL)
	}
	if err != nil {
		return err
	}
	r.mu.Lock()
	if r.prepared == nil {
		r.prepared = map[string]release.Prepared{}
	}
	r.prepared[v.Digest] = p
	r.mu.Unlock()
	return nil
}

// bundleLocation maps a source to the bundle URL. A publisher origin keeps
// bundles beside the manifest as <digest>.tar.gz; the primary's private blob
// endpoint serves the bare digest and must use the authenticated peer client.
func bundleLocation(base, bundle string) (string, bool) {
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/internal/blobs") {
		return base + "/" + bundle, true
	}
	return base + "/" + bundle + ".tar.gz", false
}
func (r *LocalRuntime) nextRevision() int64 {
	v := time.Now().UnixNano()
	for {
		old := r.revision.Load()
		if v <= old {
			v = old + 1
		}
		if r.revision.CompareAndSwap(old, v) {
			return v
		}
	}
}
func (r *LocalRuntime) RouteRevision() int64 { return r.Router.Route().Revision }

// The approved target node stays fixed; only its new boot/route identity is
// refreshed after that same node has recovered and obtained local admission.
func (r *LocalRuntime) RefreshForward(ctx context.Context, nodes []Node) error {
	r.routeMu.Lock()
	defer r.routeMu.Unlock()
	current := r.Router.Route()
	if current.Mode != "forward-only" {
		return nil
	}
	for _, n := range nodes {
		if n.PeerURL == current.PeerURL && n.ServingLocal(time.Now()) {
			if n.CoreBootID != current.CoreBootID || n.RouteRevision != current.PeerRevision {
				return r.redirectLocked(ctx, n)
			}
		}
	}
	return nil
}
func (r *LocalRuntime) Redirect(ctx context.Context, n Node) error {
	r.routeMu.Lock()
	defer r.routeMu.Unlock()
	return r.redirectLocked(ctx, n)
}

// SetOffload sends new public requests to nodes while this one is
// overloaded; the heartbeat renews it, and it lapses after offloadTTL.
func (r *LocalRuntime) SetOffload(nodes []Node) error {
	targets := make([]proxy.Target, 0, len(nodes))
	for _, n := range nodes {
		targets = append(targets, proxy.Target{PeerURL: n.PeerURL, CoreBootID: n.CoreBootID, Revision: n.RouteRevision})
	}
	return r.Router.SetOffload(targets, offloadTTL)
}

// SetDraining makes the readiness probe answer 503 while new requests still
// go to the offload targets, so an ingress health check moves on before the
// shell is restarted (Engine.Yield).
func (r *LocalRuntime) SetDraining(v bool) { r.Router.SetDraining(v) }
func (r *LocalRuntime) Draining() bool     { return r.Router.Draining() }

// Active is the number of requests this shell is proxying right now.
func (r *LocalRuntime) Active() int64 { return r.Router.Active() }
func (r *LocalRuntime) redirectLocked(ctx context.Context, n Node) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.PrimaryNode != "" {
		if r.NodeID == r.PrimaryNode {
			return errors.New("primary node must never redirect business traffic to a follower")
		}
		if n.ID != r.PrimaryNode {
			return errors.New("followers may redirect only to the configured primary")
		}
	}
	if n.CoreBootID == "" || n.RouteRevision == 0 {
		return errors.New("target has no serving identity")
	}
	return r.Router.SetRoute(proxy.Route{Mode: "forward-only", PeerURL: n.PeerURL, CoreBootID: n.CoreBootID, Revision: r.nextRevision(), PeerRevision: n.RouteRevision})
}
func (r *LocalRuntime) control() (*localapi.Client, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.client == nil {
		return nil, errors.New("core is not started")
	}
	return r.client, nil
}

// DrainStop drains and stops the local core. When this shell already
// forwards new requests to a ready peer (a follower after its redirect step),
// the drain is graceful: requests in flight finish within the deadline. In
// maintenance or while serving locally nobody else takes the traffic, so the
// core cancels them at once as before.
func (r *LocalRuntime) DrainStop(ctx context.Context, operation string) error {
	if !r.Supervisor.Status().Running {
		return r.cleanupStopped(ctx)
	}
	c, err := r.control()
	if err != nil {
		return err
	}
	graceful := r.Router.Diverted()
	slog.Info("draining local core", "operation", operation, "graceful", graceful)
	if err = r.Supervisor.DrainStop(ctx, c, operation, graceful); err != nil {
		return err
	}
	return r.confirmStopped(ctx)
}
func (r *LocalRuntime) Start(ctx context.Context, digest string, options rc.PrepareRequest) (rc.Status, error) {
	r.mu.RLock()
	p, ok := r.prepared[digest]
	r.mu.RUnlock()
	if !ok {
		return rc.Status{}, errors.New("release was not verified by this shell boot")
	}
	if r.Supervisor.Status().Running {
		c, err := r.control()
		if err != nil {
			return rc.Status{}, err
		}
		st, err := c.Status(ctx)
		if err != nil {
			return st, err
		}
		if st.ReleaseDigest != digest {
			return st, errors.New("another core is still running")
		}
		if err = verifyCandidate(st, p.Manifest); err != nil {
			return st, err
		}
		switch st.Mode {
		case "prepared", "serving":
			return st, nil
		case "preparing":
			return r.waitPrepared(ctx, c)
		case "candidate":
			options.BootID = st.BootID
			options.ReleaseDigest = digest
			if err = c.Prepare(ctx, options); err != nil {
				return st, err
			}
			return r.waitPrepared(ctx, c)
		case "failed", "error", "drained":
			if err = r.Supervisor.DrainStop(ctx, c, "restart-failed-candidate", false); err != nil {
				return st, err
			}
		default:
			return st, errors.New("candidate is still draining")
		}
	}
	if err := r.cleanupStopped(ctx); err != nil {
		return rc.Status{}, err
	}
	// Only the supervisor's confirmed-empty old process group makes a stale
	// control socket removable; never follow a symlink or replace a file.
	if info, err := os.Lstat(r.CoreSocket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return rc.Status{}, errors.New("core socket path contains a non-socket")
		}
		if err = os.Remove(r.CoreSocket); err != nil {
			return rc.Status{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return rc.Status{}, err
	}
	if err := r.Releases.Switch(ctx, "start-"+digest, digest); err != nil {
		return rc.Status{}, err
	}
	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		return rc.Status{}, err
	}
	token := hex.EncodeToString(id)
	if _, err := rand.Read(id); err != nil {
		return rc.Status{}, err
	}
	boot := hex.EncodeToString(id)
	env := append([]string{}, r.Env...)
	env = append(env, "PATH="+filepath.Join(p.Directory, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"), "SUB2API_BUILTIN_PLUGIN_DIR="+filepath.Join(p.Directory, "builtin"))
	env = append(env, "SUB2API_MANAGED=true", "SUB2API_CONTROL_SOCKET="+r.CoreSocket, "SUB2API_CONTROL_TOKEN="+token, "SUB2API_CORE_BOOT_ID="+boot, "SUB2API_RELEASE_DIGEST="+digest, "NODE_ID="+r.NodeID, "UPDATER_SOCKET="+r.ManagementSocket, "SUB2API_UPDATER_TOKEN_FILE="+r.ManagementTokenFile, "SUB2API_HTTP_ADDR="+strings.TrimPrefix(r.CoreURL, "http://"))
	c := localapi.New(r.CoreSocket, token)
	r.mu.Lock()
	r.client = c
	r.mu.Unlock()
	_, err := r.Supervisor.Start(ctx, supervisor.Spec{Executable: filepath.Join(p.Directory, "bin", "sub2api"), Args: r.Args, Env: env, InheritEnv: r.InheritEnv, Dir: r.Root, BootID: boot, Stdout: os.Stdout, Stderr: os.Stderr})
	if err != nil {
		return rc.Status{}, err
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		h, err := c.Hello(ctx)
		if err == nil {
			if h.BootID != boot || h.ReleaseDigest != digest || h.Protocol != rc.Protocol {
				return rc.Status{}, errors.New("candidate handshake mismatch")
			}
			break
		}
		if !r.Supervisor.Status().Running {
			return rc.Status{}, errors.New("candidate exited before handshake")
		}
		select {
		case <-ctx.Done():
			return rc.Status{}, ctx.Err()
		case <-tick.C:
		}
	}
	actual, err := c.Status(ctx)
	if err != nil {
		return actual, err
	}
	if actual.BootID != boot || actual.ReleaseDigest != digest {
		return actual, errors.New("candidate status identity mismatch")
	}
	if err = verifyCandidate(actual, p.Manifest); err != nil {
		return actual, err
	}
	options.BootID = boot
	options.ReleaseDigest = digest
	if err = c.Prepare(ctx, options); err != nil {
		return rc.Status{}, err
	}
	st, err := r.waitPrepared(ctx, c)
	if err == nil {
		r.collectReleases(ctx)
	}
	return st, err
}

// releaseGrace keeps releases prepared this recently from the release GC: a
// plan that prepared them has not started them yet.
const releaseGrace = 6 * time.Hour

// collectReleases removes the releases a rollback cannot need once a core is
// up from the current one (release.Manager.GC; the disk of a node otherwise
// fills with every release it ever ran) and forgets them here. A failure is
// logged: the core runs either way.
func (r *LocalRuntime) collectReleases(ctx context.Context) {
	n, err := r.Releases.GC(ctx, releaseGrace)
	if n > 0 || err != nil {
		slog.Info("release cleanup", "removed", n, "error", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for digest, p := range r.prepared {
		if _, err := os.Stat(p.Directory); errors.Is(err, os.ErrNotExist) {
			delete(r.prepared, digest)
		}
	}
}

// Validate embedded capabilities before issuing Prepare, which may migrate the
// shared database. A manifest's declaration is not proof of the binary's protocol.
func verifyCandidate(st rc.Status, m rc.Manifest) error {
	if st.Protocol != rc.Protocol || st.CoreVersion == "" || st.CoreVersion != m.CoreVersion {
		return errors.New("candidate core/control version differs from the release manifest")
	}
	if !st.ClusterProtocol.Contains(m.ClusterProtocol.Min) || !st.ClusterProtocol.Contains(m.ClusterProtocol.Max) || !st.TaskProtocol.Contains(m.TaskProtocol.Min) || !st.TaskProtocol.Contains(m.TaskProtocol.Max) || st.HostAPIVersion != m.HostAPIVersion {
		return errors.New("candidate embedded capabilities differ from the release manifest")
	}
	if st.SchemaContract == "" || st.SchemaContract != m.SchemaAfter {
		return errors.New("candidate embedded schema differs from the release manifest")
	}
	return nil
}

// Maintenance closes both public and private business admission before the
// primary is drained. Artifact distribution remains available on its shell.
func (r *LocalRuntime) Maintenance(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.routeMu.Lock()
	defer r.routeMu.Unlock()
	return r.Router.SetRoute(proxy.Route{Mode: "maintenance", Revision: r.nextRevision()})
}

// Stopped reports an observed empty process group, not merely a dead core PID.
// Heartbeats must stay bounded while children are draining; they never signal
// processes themselves or turn a timeout into a successful stop acknowledgement.
func (r *LocalRuntime) Stopped(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if r.Supervisor.Status().Running {
		return false, nil
	}
	check, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if err := r.Supervisor.Wait(check); err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// A crashed core cannot drain surviving plugin children. Only after the
// supervisor has observed that specific child exit may we request SIGTERM for
// its recorded process group. A live core is never interrupted by this path.
func (r *LocalRuntime) cleanupStopped(ctx context.Context) error {
	if r.Supervisor.Status().Running {
		return errors.New("cannot clean plugins while the core is still alive")
	}
	cleanup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := r.Supervisor.Terminate(cleanup); err != nil {
		return err
	}
	return r.confirmStopped(ctx)
}
func (r *LocalRuntime) waitPrepared(ctx context.Context, c *localapi.Client) (rc.Status, error) {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		st, err := c.Status(ctx)
		if err != nil {
			return st, err
		}
		if st.Mode == "prepared" && len(st.Blockers) == 0 {
			return st, nil
		}
		if st.Mode == "failed" || st.Mode == "error" {
			return st, fmt.Errorf("candidate preparation failed: %v", st.Blockers)
		}
		select {
		case <-ctx.Done():
			return st, ctx.Err()
		case <-tick.C:
		}
	}
}
func (r *LocalRuntime) Admit(ctx context.Context, a rc.Admission) (rc.Status, error) {
	c, err := r.control()
	if err != nil {
		return rc.Status{}, err
	}
	if err = c.Admit(ctx, a); err != nil {
		return rc.Status{}, err
	}
	return c.Status(ctx)
}
func (r *LocalRuntime) Local(ctx context.Context) error {
	c, err := r.control()
	if err != nil {
		return err
	}
	st, err := c.Status(ctx)
	if err != nil {
		return err
	}
	if !st.Ready {
		return errors.New("core has not passed admission")
	}
	r.routeMu.Lock()
	defer r.routeMu.Unlock()
	return r.Router.SetRoute(proxy.Route{Mode: "local-serving", LocalURL: r.CoreURL, CoreBootID: st.BootID, Revision: r.nextRevision()})
}
func (r *LocalRuntime) Status(ctx context.Context) (rc.Status, string, error) {
	mode := r.Router.Route().Mode
	switch mode {
	case "local-serving":
		mode = "local"
	case "forward-only":
		mode = "forward"
	}
	c, err := r.control()
	if err != nil {
		return rc.Status{}, mode, err
	}
	st, err := c.Status(ctx)
	return st, mode, err
}
func (r *LocalRuntime) PrivateHandler() http.Handler {
	if r.Peer == nil || r.AuthorizePeer == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { peer.Failure(w, 503) })
	}
	return r.Peer.Middleware(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		id, ok := peer.FromContext(q.Context())
		if !ok {
			peer.Failure(w, 401)
			return
		}
		scope, digest := "", ""
		escaped := q.URL.EscapedPath()
		switch {
		case strings.HasPrefix(escaped, "/internal/forward/"):
			scope = "forward"
		case strings.HasPrefix(escaped, "/internal/blobs/"):
			scope = "core-artifact"
			digest = strings.TrimPrefix(escaped, "/internal/blobs/")
		case strings.HasPrefix(escaped, "/internal/releases/"):
			scope = "core-artifact"
			digest = strings.TrimPrefix(escaped, "/internal/releases/")
		case strings.HasPrefix(escaped, "/internal/plugin-blobs/") && r.PluginBlobs != nil:
			scope = "plugin-artifact"
			digest = strings.TrimPrefix(escaped, "/internal/plugin-blobs/")
		default:
			peer.Failure(w, 403)
			return
		}
		if scope != "forward" && (!release.ValidDigest(digest) || (q.Method != "GET" && q.Method != "HEAD")) {
			peer.Failure(w, 403)
			return
		}
		if err := r.AuthorizePeer(q.Context(), id, scope, digest); err != nil {
			peer.Failure(w, peer.ErrorStatus(err))
			return
		}
		// No ServeMux: preserve raw business paths without cleaning redirects.
		switch scope {
		case "forward":
			r.Router.Private().ServeHTTP(w, q)
		case "plugin-artifact":
			r.PluginBlobs.ServeHTTP(w, q)
		default:
			r.Releases.BlobHandler().ServeHTTP(w, q)
		}
	}))
}

func (r *LocalRuntime) confirmStopped(ctx context.Context) error {
	st := r.Supervisor.Status()
	if st.Running {
		return errors.New("core still running")
	}
	if st.BootID != "" && r.OnStopped != nil {
		return r.OnStopped(ctx, st.BootID)
	}
	return nil
}
