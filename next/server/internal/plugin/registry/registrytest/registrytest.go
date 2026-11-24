// Package registrytest provides shared helpers for plugin runtime tests: the
// compiled test plugin, package builder, database fixtures and in-memory
// (Redis-backed) cluster fakes.
package registrytest

import (
	"archive/zip"
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/blobs"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

var (
	buildOnce sync.Once
	buildBin  []byte
	buildErr  error
)

// TestPlugin compiles grpcruntime/testdata/testplugin (once per process)
// and returns the binary.
func TestPlugin(t testing.TB) []byte {
	t.Helper()
	buildOnce.Do(func() {
		_, file, _, _ := goruntime.Caller(0)
		serverDir := filepath.Join(filepath.Dir(file), "..", "..", "..", "..")
		dir, err := os.MkdirTemp("", "s2p-testplugin-")
		if err != nil {
			buildErr = err
			return
		}
		out := filepath.Join(dir, "plugin")
		if goruntime.GOOS == "windows" {
			out += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", out, "./internal/plugin/grpcruntime/testdata/testplugin")
		cmd.Dir = serverDir
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if b, err := cmd.CombinedOutput(); err != nil {
			buildErr = err
			buildBin = b
			return
		}
		buildBin, buildErr = os.ReadFile(out)
		_ = os.RemoveAll(dir)
	})
	if buildErr != nil {
		t.Fatalf("build test plugin: %v\n%s", buildErr, buildBin)
	}
	return buildBin
}

// Manifest returns a manifest for the test plugin.
func Manifest(key, version string) *manifest.Manifest {
	return &manifest.Manifest{
		APIVersion: 1,
		Key:        key,
		Name:       manifest.LocalizedText{"en": "Test " + key, "zh": "测试 " + key},
		Version:    version,
		Publisher:  manifest.OfficialPublisher,
		Runtime:    "grpc",
		Entry:      manifest.Entry{GRPC: &manifest.GRPCEntry{Binaries: "runtimes/{os}-{arch}/plugin"}},
		HostCompat: ">=0.1.0",
		Capabilities: []manifest.Capability{
			{ID: manifest.CapPlatformAdapter}, {ID: manifest.CapHTTPRoutes}, {ID: manifest.CapGatewayHook}, {ID: manifest.CapMigrationData}, {ID: manifest.CapAppBroadcast},
		},
		Platforms: []manifest.Platform{{
			ID:        "p_" + key,
			Endpoints: []manifest.Endpoint{TestEndpoint(key)},
		}},
		AccountTypes: []manifest.AccountType{{
			ID: "apikey", Label: manifest.LocalizedText{"en": "API key", "zh": "API 密钥"},
			Form:      manifest.Form{Mode: "native", Component: "ApiKeyForm"},
			Platforms: []manifest.AccountPlatform{{Platform: "p_" + key}},
		}},
		Hooks:           []manifest.Hook{{Point: "gateway.request", Order: 10, Needs: []string{"model", "prompt_text"}, TimeoutMs: 500}},
		Database:        &manifest.Database{Migrations: "migrations/"},
		UserPermissions: []manifest.UserPermission{{Key: "rules:read", Label: manifest.LocalizedText{"en": "Read rules"}}},
		Routes: []manifest.Route{
			{Method: "GET", Path: "/echo/:id", Scope: "admin", Permission: "rules:read"},
			{Method: "POST", Path: "/hook", Scope: "webhook"},
			{Method: "GET", Path: "/dsn", Scope: "public"},
			{Method: "POST", Path: "/credit", Scope: "public"},
		},
		Icon:            "ui/icon.svg",
		HostPermissions: Declare(DefaultGrants()),
	}
}

// Declare asks for each granted host permission (sorted), as a manifest
// must for its instances to be granted it.
func Declare(grants map[string]string) []manifest.HostPermission {
	out := make([]manifest.HostPermission, 0, len(grants))
	for id := range grants {
		out = append(out, manifest.HostPermission{ID: id})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// TestEndpoint is the endpoint of the test platform "p_<key>": POST
// /p_<key>/v1/test, protocol "p_<key>.test" (unique per plugin key).
func TestEndpoint(key string) manifest.Endpoint {
	return manifest.Endpoint{
		ID: "test", Method: "POST", Path: "/p_" + key + "/v1/test", Protocol: "p_" + key + ".test", Kind: "proxy",
		Auth:     manifest.EndpointAuth{Headers: []string{"authorization"}},
		Request:  manifest.EndpointRequest{ModelPath: "model"},
		Response: manifest.EndpointResp{NonStream: "json"}, ErrorFormat: "plain", Billing: "usage", BillingTypes: []string{"per_request", "per_token", "expression"},
	}
}

// DefaultGrants approves everything the test manifest asks for.
func DefaultGrants() map[string]string {
	return map[string]string{
		"kv":                `{}`,
		"db.schema":         `{}`,
		"gateway.hook":      `{"points":["gateway.request"],"fields":["model"]}`,
		"routes.admin":      `{}`,
		"routes.webhook":    `{}`,
		"routes.public":     `{}`,
		"gateway.endpoint":  `{}`,
		"platform.register": `{}`,
		"broadcast":         `{}`,
		"lock":              `{}`,
	}
}

// Package builds a .s2plugin zip with the test binary for this platform
// and the given extra files (migrations, ui ...).
func Package(t testing.TB, m *manifest.Manifest, bin []byte, extra map[string][]byte) []byte {
	t.Helper()
	files := map[string][]byte{}
	mj, _ := json.Marshal(m)
	files["manifest.json"] = mj
	name := "runtimes/" + goruntime.GOOS + "-" + goruntime.GOARCH + "/plugin"
	if goruntime.GOOS == "windows" {
		name += ".exe"
	}
	files[name] = bin
	files["i18n/en.json"] = []byte(`{"hello":"Hello"}`)
	files["ui/index.html"] = []byte(`<!doctype html><title>t</title>`)
	files["ui/icon.svg"] = []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)
	files["secret.txt"] = []byte("not an asset")
	for k, v := range extra {
		files[k] = v
	}
	names := make([]string, 0, len(files))
	for k := range files {
		names = append(names, k)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(files[n])
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Install inserts the plugins row (status) and an approved version with grants.
func Install(t testing.TB, db *store.DB, m *manifest.Manifest, pkg []byte, grants map[string]string, status string) {
	t.Helper()
	g := store.G(context.Background(), db.Gorm)
	name, _ := json.Marshal(m.Name)
	if err := g.Create(&model.Plugin{Key: m.ID(), Name: name, Status: status}).Error; err != nil {
		t.Fatal(err)
	}
	AddVersion(t, db, m, pkg)
	for perm, scope := range grants {
		if err := g.Create(grantRow(m, perm, scope)).Error; err != nil {
			t.Fatal(err)
		}
	}
}

// grantRow is the grant of perm with scope to the version of m.
func grantRow(m *manifest.Manifest, perm, scope string) *model.PluginPermissionGrant {
	return &model.PluginPermissionGrant{PluginKey: m.ID(), Permission: perm, Scope: json.RawMessage(scope),
		Status: "granted", PluginVersion: m.Version, ManifestHash: "x"}
}

// AddVersion inserts an approved plugin_versions row.
func AddVersion(t testing.TB, db *store.DB, m *manifest.Manifest, pkg []byte) {
	t.Helper()
	mj, _ := json.Marshal(m)
	sum := sha256.Sum256(pkg)
	msum := sha256.Sum256(mj)
	if err := Source().Store.Put(context.Background(), hex.EncodeToString(sum[:]), pkg); err != nil {
		t.Fatal(err)
	}
	if err := store.G(context.Background(), db.Gorm).Create(versionRow(m, mj, msum, sum, len(pkg))).Error; err != nil {
		t.Fatal(err)
	}
}

// versionRow is the approved, unsigned plugin_versions row of m, whose
// manifest is mj.
func versionRow(m *manifest.Manifest, mj []byte, manifestSum, pkgSum [sha256.Size]byte, size int) *model.PluginVersion {
	return &model.PluginVersion{PluginKey: m.ID(), Version: m.Version, Manifest: mj,
		ManifestHash: hex.EncodeToString(manifestSum[:]), PackageSha256: hex.EncodeToString(pkgSum[:]), PackageSize: int64(size),
		SignatureStatus: "unsigned", ConsentStatus: new("approved")}
}

// ------------------------------------------------------------------ launcher

// Launcher execs the binary directly (dev mode) and lets tests fire
// resource events.
type Launcher struct {
	mu       sync.Mutex
	watchers map[int]func(core.ResourceEvent)
	Commands int
}

func (l *Launcher) Command(_ context.Context, spec core.LaunchSpec) (*exec.Cmd, error) {
	l.mu.Lock()
	l.Commands++
	l.mu.Unlock()
	cmd := exec.Command(spec.BinaryPath)
	cmd.Dir = cmp.Or(spec.DataDir, spec.RunDir)
	cmd.Env = append([]string(nil), spec.Env...)
	return cmd, nil
}

func (l *Launcher) Watch(_ core.LaunchSpec, pid int, onEvent func(core.ResourceEvent)) func() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.watchers == nil {
		l.watchers = map[int]func(core.ResourceEvent){}
	}
	l.watchers[pid] = onEvent
	return func() {
		l.mu.Lock()
		delete(l.watchers, pid)
		l.mu.Unlock()
	}
}

// Fire delivers a resource event to the watcher of pid.
func (l *Launcher) Fire(pid int, ev core.ResourceEvent) bool {
	l.mu.Lock()
	fn := l.watchers[pid]
	l.mu.Unlock()
	if fn == nil {
		return false
	}
	ev.PID = pid
	fn(ev)
	return true
}

// ------------------------------------------------------------------ cluster fakes

// Node is a NodeRegistry backed by Redis (use miniredis in tests).
type Node struct {
	RDB  redis.UniversalClient
	ID   string
	Boot string
	mu   sync.Mutex
	dead bool
}

func (n *Node) NodeID() string { return n.ID }
func (n *Node) BootID() string { return n.Boot }
func (n *Node) Healthy() bool  { return true }

// Heartbeat marks the node alive.
func (n *Node) Heartbeat(ctx context.Context) {
	n.mu.Lock()
	dead := n.dead
	n.mu.Unlock()
	if dead {
		return
	}
	n.RDB.ZAdd(ctx, "node:live", redis.Z{Score: float64(time.Now().UnixMilli()), Member: n.Boot})
	n.RDB.HSet(ctx, "node:info:"+n.Boot, "node_id", n.ID)
}

// Kill removes the node from the live set and stops heartbeats.
func (n *Node) Kill(ctx context.Context) {
	n.mu.Lock()
	n.dead = true
	n.mu.Unlock()
	n.RDB.ZRem(ctx, "node:live", n.Boot)
}

func (n *Node) LiveNodes(ctx context.Context) ([]core.NodeStatus, error) {
	boots, err := n.RDB.ZRangeByScore(ctx, "node:live", &redis.ZRangeBy{
		Min: strconv.FormatInt(time.Now().Add(-15*time.Second).UnixMilli(), 10), Max: "+inf"}).Result()
	if err != nil {
		return nil, err
	}
	out := make([]core.NodeStatus, 0, len(boots))
	for _, b := range boots {
		id, _ := n.RDB.HGet(ctx, "node:info:"+b, "node_id").Result()
		plugins, _ := n.RDB.HGetAll(ctx, "node:plugins:"+b).Result()
		out = append(out, core.NodeStatus{NodeID: id, BootID: b, Plugins: plugins})
	}
	return out, nil
}

func (n *Node) IsAlive(ctx context.Context, bootID string) (bool, error) {
	s, err := n.RDB.ZScore(ctx, "node:live", bootID).Result()
	if err == redis.Nil {
		return false, nil
	}
	return err == nil && s > float64(time.Now().Add(-15*time.Second).UnixMilli()), err
}

func (n *Node) ReportPlugin(ctx context.Context, pluginKey, stateJSON string) error {
	n.Heartbeat(ctx)
	n.mu.Lock()
	dead := n.dead
	n.mu.Unlock()
	if dead {
		return nil
	}
	return n.RDB.HSet(ctx, "node:plugins:"+n.Boot, pluginKey, stateJSON).Err()
}

// Bus is a core.Bus over Redis pub/sub.
type Bus struct{ RDB redis.UniversalClient }

func (b Bus) Publish(ctx context.Context, channel string, payload []byte) error {
	return b.RDB.Publish(ctx, channel, payload).Err()
}

func (b Bus) Subscribe(channel string, handler func([]byte)) func() {
	ctx, cancel := context.WithCancel(context.Background())
	ps := b.RDB.Subscribe(ctx, channel)
	go func() {
		ch := ps.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case m, ok := <-ch:
				if !ok {
					return
				}
				handler([]byte(m.Payload))
			}
		}
	}()
	return func() { cancel(); _ = ps.Close() }
}

// MemBus is an in-process core.Bus: Publish calls every handler of the
// channel synchronously. Published messages are also recorded.
type MemBus struct {
	mu       sync.Mutex
	handlers map[string]map[int]func([]byte)
	nextID   int
	sent     []MemMessage
}

// MemMessage is one message published on a MemBus.
type MemMessage struct {
	Channel string
	Payload []byte
}

var _ core.Bus = (*MemBus)(nil)

func (b *MemBus) Publish(_ context.Context, channel string, payload []byte) error {
	b.mu.Lock()
	b.sent = append(b.sent, MemMessage{Channel: channel, Payload: append([]byte(nil), payload...)})
	ids := make([]int, 0, len(b.handlers[channel]))
	for id := range b.handlers[channel] {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	hs := make([]func([]byte), 0, len(ids))
	for _, id := range ids {
		hs = append(hs, b.handlers[channel][id])
	}
	b.mu.Unlock()
	for _, h := range hs {
		h(payload)
	}
	return nil
}

func (b *MemBus) Subscribe(channel string, handler func([]byte)) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.handlers == nil {
		b.handlers = map[string]map[int]func([]byte){}
	}
	if b.handlers[channel] == nil {
		b.handlers[channel] = map[int]func([]byte){}
	}
	b.nextID++
	id := b.nextID
	b.handlers[channel][id] = handler
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			delete(b.handlers[channel], id)
			if len(b.handlers[channel]) == 0 {
				delete(b.handlers, channel)
			}
		})
	}
}

// Subscribed reports whether channel has at least one handler.
func (b *MemBus) Subscribed(channel string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.handlers[channel]) > 0
}

// Messages returns the messages published on channel so far.
func (b *MemBus) Messages(channel string) []MemMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []MemMessage
	for _, m := range b.sent {
		if m.Channel == channel {
			out = append(out, m)
		}
	}
	return out
}

var (
	sourceOnce sync.Once
	source     *blobs.Source
)

// Source is the package source shared by every node of a test process, as
// if every node held every package. AddVersion and uploads put into it.
// Its directory outlives every test, so a package that uses it runs its
// tests through Main, which removes it.
func Source() *blobs.Source {
	sourceOnce.Do(func() {
		dir, err := os.MkdirTemp("", "sub2api-test-blobs-")
		if err != nil {
			panic(err)
		}
		sourceDir = dir
		source = &blobs.Source{Store: blobs.Dir(dir), MaxBytes: 256 << 20}
	})
	return source
}

// sourceDir is the directory of Source, once made.
var sourceDir string

// Main runs a package's tests (TestMain) and then removes Source's
// directory, about 125 MB that each test process would otherwise leave in
// the temporary directory.
func Main(m *testing.M) {
	code := m.Run()
	if sourceDir != "" {
		_ = os.RemoveAll(sourceDir)
	}
	os.Exit(code)
}
