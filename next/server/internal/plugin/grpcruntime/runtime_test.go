package grpcruntime_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/secret"
	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/cluster"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/dbschema"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/grpcruntime"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry/registrytest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

type fakeAuthz struct{ allow string }

func (f fakeAuthz) Can(_ context.Context, uid int64, perm string) (bool, error) {
	return uid == 7 && perm == f.allow, nil
}
func (fakeAuthz) PermissionSet(context.Context, int64) (core.PermissionSet, error) {
	return core.PermissionSet{}, nil
}
func (fakeAuthz) CanGrant(context.Context, int64, []string) error { return nil }

type fakeLedger struct {
	mu    sync.Mutex
	seen  map[string]int64
	total decimal.Decimal
	last  core.LedgerChange
}

func (l *fakeLedger) Apply(_ context.Context, ch core.LedgerChange) (*core.LedgerResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil {
		l.seen = map[string]int64{}
	}
	l.last = ch
	if id, ok := l.seen[ch.IdempotencyKey]; ok {
		return &core.LedgerResult{LedgerID: id, BalanceAfter: l.total, Duplicate: true}, nil
	}
	l.total = l.total.Add(ch.Amount)
	id := int64(len(l.seen) + 1)
	l.seen[ch.IdempotencyKey] = id
	return &core.LedgerResult{LedgerID: id, BalanceAfter: l.total}, nil
}

func (l *fakeLedger) ApplyTx(ctx context.Context, _ *gorm.DB, ch core.LedgerChange) (*core.LedgerResult, error) {
	return l.Apply(ctx, ch)
}

func randKey(prefix string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

type env struct {
	db       *store.DB
	rdb      *redis.Client
	mr       *miniredis.Miniredis
	cipher   *secret.Cipher
	launcher *registrytest.Launcher
	ledger   *fakeLedger
	pkgs     *registry.Packages
	schemas  *dbschema.Manager
	key      string
}

func setup(t *testing.T, grants map[string]string) *env {
	t.Helper()
	return setupPackage(t, grants, registrytest.TestPlugin(t), nil)
}

// setupPackage is setup with the plugin binary bin and the manifest changed
// by mod.
func setupPackage(t *testing.T, grants map[string]string, bin []byte, mod func(*manifest.Manifest)) *env {
	t.Helper()
	db := testutil.DB(t)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	mk := make([]byte, 32)
	_, _ = rand.Read(mk)
	c, err := secret.New(mk)
	if err != nil {
		t.Fatal(err)
	}
	key := randKey("rt")
	m := registrytest.Manifest(key, "1.0.0")
	m.HostPermissions = registrytest.Declare(grants)
	if mod != nil {
		mod(m)
	}
	pkg := registrytest.Package(t, m, bin, map[string][]byte{
		"migrations/0001_init.sql": []byte(`CREATE TABLE items (id int PRIMARY KEY);`),
	})
	registrytest.Install(t, db, m, pkg, grants, "enabled")
	schemas := dbschema.New(db, testutil.DSN(db), nil)
	t.Cleanup(func() { _ = schemas.Drop(context.Background(), key) })
	pkgs := registry.NewPackages(db, t.TempDir(), registry.WithSource(registrytest.Source()))
	t.Cleanup(pkgs.Close)
	return &env{db: db, rdb: rdb, mr: mr, cipher: c, launcher: &registrytest.Launcher{}, ledger: &fakeLedger{},
		pkgs: pkgs, schemas: schemas, key: key}
}

func (e *env) setConfig(t *testing.T, cfg string) {
	t.Helper()
	enc, err := e.cipher.Encrypt([]byte(cfg), grpcruntime.ConfigAAD(e.key))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.SQL.ExecContext(context.Background(), `UPDATE plugins SET config_enc = $2 WHERE key = $1`, e.key, enc); err != nil {
		t.Fatal(err)
	}
}

func (e *env) runtime(t *testing.T, mod func(*grpcruntime.Options)) *grpcruntime.Runtime {
	t.Helper()
	o := grpcruntime.Options{
		DB: e.db, Redis: e.rdb, Cipher: e.cipher,
		Node:           &registrytest.Node{RDB: e.rdb, ID: "n1", Boot: "b1"},
		Launcher:       e.launcher,
		Authorizer:     fakeAuthz{allow: "plugin." + e.key + ":rules:read"},
		Ledger:         e.ledger,
		Schemas:        e.schemas,
		Locker:         cluster.NewLocker(e.rdb, nil),
		DataDir:        t.TempDir(),
		HostVersion:    "0.1.0-test",
		HealthInterval: 200 * time.Millisecond,
		BackoffBase:    50 * time.Millisecond,
		DrainTimeout:   5 * time.Second,
		// Shorter than the slow calls the drain tests keep in flight, so the
		// instance is still draining - not already stopped - when they look.
		DrainGrace: 50 * time.Millisecond,
	}
	if mod != nil {
		mod(&o)
	}
	rt, err := grpcruntime.New(o)
	if err != nil {
		t.Fatal(err)
	}
	return rt
}

func (e *env) load(t *testing.T, rt *grpcruntime.Runtime) *grpcruntime.Instance {
	t.Helper()
	ctx := context.Background()
	pkg, err := e.pkgs.Open(ctx, e.key, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	inst, err := rt.Load(ctx, pkg)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	t.Cleanup(inst.Stop)
	return inst
}

func waitFor(t *testing.T, what string, d time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// build runs one Execute of the test plugin's default flow and returns the
// request it relayed without waiting.
func build(t *testing.T, inst *grpcruntime.Instance) *pluginv1.UpstreamRequest {
	t.Helper()
	res, err := execute(inst, &pluginv1.ExecuteRequest{}, core.ExecutionClient{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Relay == nil {
		t.Fatalf("Execute relayed nothing: %v", res.Response)
	}
	return res.Relay
}

func execute(inst *grpcruntime.Instance, in *pluginv1.ExecuteRequest, client core.ExecutionClient) (core.ExecutionResult, error) {
	return inst.Platform().Execute(context.Background(), in, client)
}

func httpCall(t *testing.T, inst *grpcruntime.Instance, req *pluginv1.HTTPRequest) (map[string]any, error) {
	t.Helper()
	resp, err := inst.HTTP().HandleHTTP(context.Background(), req)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := json.Unmarshal(resp.GetBody(), &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

func TestRuntimeLifecycle(t *testing.T) {
	grants := registrytest.DefaultGrants()
	grants["ledger.credit"] = `{"maxPerTx":"5","maxPerDay":8}`
	e := setup(t, grants)
	e.setConfig(t, `{"greeting":"hi"}`)
	rt := e.runtime(t, nil)
	inst := e.load(t, rt)
	ctx := context.Background()

	if s, _ := inst.State(); s != grpcruntime.StateReady {
		t.Fatalf("state = %s", s)
	}

	// Platform call with HostService KV/Log callbacks.
	r1 := build(t, inst)
	if r1.Headers["x-calls"] != "1" || r1.Headers["x-greeting"] != "hi" {
		t.Fatalf("unexpected headers %v", r1.Headers)
	}
	if r2 := build(t, inst); r2.Headers["x-calls"] != "2" {
		t.Fatalf("x-calls = %s", r2.Headers["x-calls"])
	}
	if v, _ := e.mr.Get("plugin:kv:" + e.key + ":t:calls"); v != "2" {
		t.Fatalf("kv value = %q", v)
	}

	// HTTP route + AuthzCheck (local key qualified with plugin.<key>:).
	out, err := httpCall(t, inst, &pluginv1.HTTPRequest{Caller: &pluginv1.Caller{UserId: 7}, Method: "GET", Path: "/echo/1"})
	if err != nil || out["allowed"] != true {
		t.Fatalf("authz via host: %v %v", out, err)
	}

	// GetDSN through dbschema.
	out, err = httpCall(t, inst, &pluginv1.HTTPRequest{Method: "GET", Path: "/dsn"})
	if err != nil {
		t.Fatal(err)
	}
	if out["schema"] != "plg_"+e.key || out["dsn"] == "" {
		t.Fatalf("dsn: %v", out)
	}

	// Ledger with grant scope limits and mandatory idempotency.
	credit := func(amount, idem string) (map[string]any, error) {
		return httpCall(t, inst, &pluginv1.HTTPRequest{Method: "POST", Path: "/credit", Body: []byte(amount),
			Query: map[string]*pluginv1.HeaderValues{"idem": {Values: []string{idem}}}})
	}
	if out, err := credit("3", "a"); err != nil || out["balance_after"] != "3.00000000" {
		t.Fatalf("credit: %v %v", out, err)
	}
	if e.ledger.last.Kind != "plugin_credit" || e.ledger.last.IdempotencyKey != "plugin:"+e.key+":a" || e.ledger.last.PluginKey != e.key {
		t.Fatalf("ledger change: %+v", e.ledger.last)
	}
	if out, err := credit("3", "a"); err != nil || out["duplicate"] != true {
		t.Fatalf("duplicate: %v %v", out, err)
	}
	if _, err := credit("6", "b"); !errors.Is(err, core.ErrPermissionDenied) && core.AsError(err).Code != "permission_denied" {
		t.Fatalf("maxPerTx not enforced: %v", err)
	}
	if _, err := credit("4", "c"); err != nil {
		t.Fatalf("within daily cap: %v", err)
	}
	if _, err := credit("2", "d"); core.AsError(err).Code != "permission_denied" {
		t.Fatalf("maxPerDay not enforced: %v", err)
	}
	if _, err := credit("1", ""); core.AsError(err).Code != "invalid_argument" {
		t.Fatalf("missing idempotency key accepted: %v", err)
	}

	// Hook call.
	hr, err := inst.Hook().OnGatewayRequest(ctx, &pluginv1.GatewayRequestHookRequest{HookId: "0",
		Fields: map[string]string{"prompt_text": "a bad prompt"}})
	if err != nil || hr.GetDecision() != pluginv1.GatewayRequestHookResponse_DECISION_DENY {
		t.Fatalf("hook: %v %v", hr, err)
	}

	// Settings change is pushed with Configure on Refresh.
	e.setConfig(t, `{"greeting":"hello"}`)
	if err := inst.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if r := build(t, inst); r.Headers["x-greeting"] != "hello" {
		t.Fatalf("greeting after refresh = %q", r.Headers["x-greeting"])
	}

	// Crash -> automatic restart; KV state survives.
	pid := inst.PID()
	_, err = inst.Platform().ValidateCredentials(ctx, &pluginv1.ValidateCredentialsRequest{CredentialsJson: `"crash"`})
	if err == nil {
		t.Fatal("expected error from crashing call")
	}
	waitFor(t, "restart after crash", 10*time.Second, func() bool {
		s, _ := inst.State()
		return s == grpcruntime.StateReady && inst.PID() != 0 && inst.PID() != pid
	})
	if inst.Restarts() != 1 {
		t.Fatalf("restarts = %d", inst.Restarts())
	}
	if r := build(t, inst); r.Headers["x-calls"] != "4" || r.Headers["x-greeting"] != "hello" {
		t.Fatalf("after restart: %v", r.Headers)
	}

	// memory_exceeded from the watchdog triggers a restart.
	pid = inst.PID()
	if !e.launcher.Fire(pid, core.ResourceEvent{Kind: "memory_exceeded", PluginKey: e.key}) {
		t.Fatal("no watcher registered for plugin pid")
	}
	waitFor(t, "restart after memory event", 10*time.Second, func() bool {
		s, _ := inst.State()
		return s == grpcruntime.StateReady && inst.PID() != pid && inst.PID() != 0
	})

	// Drain: in-flight call completes, new calls are refused, then stop.
	slowDone := make(chan error, 1)
	go func() {
		_, err := inst.Platform().BuildTestRequest(ctx, &pluginv1.BuildTestRequestRequest{Model: "sleep:700"})
		slowDone <- err
	}()
	waitFor(t, "slow call in flight", 5*time.Second, func() bool { return inst.InFlight() == 1 })
	drained := make(chan struct{})
	go func() { inst.Drain(5 * time.Second); close(drained) }()
	waitFor(t, "draining", 5*time.Second, func() bool { s, _ := inst.State(); return s == grpcruntime.StateDraining })
	if _, err := inst.Platform().ClassifyError(ctx, &pluginv1.ClassifyErrorRequest{}); core.AsError(err).Code != "plugin_unavailable" {
		t.Fatalf("call during drain: %v", err)
	}
	if err := <-slowDone; err != nil {
		t.Fatalf("in-flight call failed during drain: %v", err)
	}
	select {
	case <-drained:
	case <-time.After(10 * time.Second):
		t.Fatal("drain did not finish")
	}
	if s, _ := inst.State(); s != grpcruntime.StateStopped {
		t.Fatalf("state after drain = %s", s)
	}
	if _, err := inst.Platform().ClassifyError(ctx, &pluginv1.ClassifyErrorRequest{}); core.AsError(err).Code != "plugin_unavailable" {
		t.Fatalf("call after stop: %v", err)
	}
}

func TestHostServiceRequiresGrants(t *testing.T) {
	grants := registrytest.DefaultGrants()
	delete(grants, "kv")
	delete(grants, "db.schema")
	delete(grants, "lock")
	e := setup(t, grants)
	inst := e.load(t, e.runtime(t, nil))

	_, err := execute(inst, &pluginv1.ExecuteRequest{}, core.ExecutionClient{})
	if core.AsError(err).Code != "permission_denied" {
		t.Fatalf("kv without grant: %v", err)
	}
	if _, err := httpCall(t, inst, &pluginv1.HTTPRequest{Method: "GET", Path: "/dsn"}); core.AsError(err).Code != "permission_denied" {
		t.Fatalf("GetDSN without grant: %v", err)
	}
	if _, err := httpCall(t, inst, &pluginv1.HTTPRequest{Method: "POST", Path: "/credit", Body: []byte("1"),
		Query: map[string]*pluginv1.HeaderValues{"idem": {Values: []string{"x"}}}}); core.AsError(err).Code != "permission_denied" {
		t.Fatalf("ledger without grant: %v", err)
	}
	if out, err := httpCall(t, inst, lockReq("acquire", "x", lockTokA, 1000)); err != nil || out["code"] != "PermissionDenied" {
		t.Fatalf("lock without grant: %v %v", out, err)
	}
}

const (
	lockTokA = "token-a-0123456789abcdef"
	lockTokB = "token-b-0123456789abcdef"
)

func lockReq(op, name, token string, ttlMs int64) *pluginv1.HTTPRequest {
	b, _ := json.Marshal(map[string]any{"op": op, "name": name, "token": token, "ttl_ms": ttlMs})
	return &pluginv1.HTTPRequest{Method: "POST", Path: "/lock", Body: b}
}

// TestLockRealPlugin: the lock calls from a real plugin process, on the
// cluster locker over (mini)Redis, under lock:plugin:{key}:{name} with the
// plugin's own token.
func TestLockRealPlugin(t *testing.T) {
	grants := registrytest.DefaultGrants()
	delete(grants, "kv") // the lock grant stands on its own
	e := setup(t, grants)
	inst := e.load(t, e.runtime(t, nil))
	call := func(op, name, token string, ttlMs int64) map[string]any {
		t.Helper()
		out, err := httpCall(t, inst, lockReq(op, name, token, ttlMs))
		if err != nil {
			t.Fatalf("%s %s: %v", op, name, err)
		}
		return out
	}
	rkey := "lock:plugin:" + e.key + ":jobs/sync:1"

	out := call("acquire", "jobs/sync:1", lockTokA, 10000)
	if out["code"] != "OK" || out["acquired"] != true {
		t.Fatalf("acquire: %v", out)
	}
	if v, _ := out["valid_ms"].(float64); v <= 9000 || v > 10000 {
		t.Fatalf("valid_ms = %v", out["valid_ms"])
	}
	if got, _ := e.mr.Get(rkey); got != lockTokA {
		t.Fatalf("redis %s = %q, want the plugin's token", rkey, got)
	}
	if out := call("acquire", "jobs/sync:1", lockTokB, 10000); out["code"] != "OK" || out["acquired"] != false {
		t.Fatalf("contended acquire: %v", out)
	}
	if out := call("renew", "jobs/sync:1", lockTokA, 20000); out["code"] != "OK" || out["held"] != true {
		t.Fatalf("renew: %v", out)
	}
	if ttl := e.mr.TTL(rkey); ttl != 20*time.Second {
		t.Fatalf("ttl after renew = %v", ttl)
	}
	if out := call("release", "jobs/sync:1", lockTokB, 0); out["code"] != "OK" {
		t.Fatalf("release with another token: %v", out)
	}
	if !e.mr.Exists(rkey) {
		t.Fatal("release with another token freed the lock")
	}
	if out := call("release", "jobs/sync:1", lockTokA, 0); out["code"] != "OK" || e.mr.Exists(rkey) {
		t.Fatalf("release: %v exists=%v", out, e.mr.Exists(rkey))
	}
	if out := call("renew", "jobs/sync:1", lockTokA, 10000); out["code"] != "OK" || out["held"] != false {
		t.Fatalf("renew after release: %v", out)
	}
	for _, bad := range [][3]any{{"bad name", lockTokA, int64(1000)}, {"x", "short", int64(1000)}, {"x", lockTokA, int64(999)}} {
		if out := call("acquire", bad[0].(string), bad[1].(string), bad[2].(int64)); out["code"] != "InvalidArgument" {
			t.Fatalf("acquire %v: %v", bad, out)
		}
	}
}

func TestRestartBudgetMarksFailed(t *testing.T) {
	e := setup(t, registrytest.DefaultGrants())
	inst := e.load(t, e.runtime(t, func(o *grpcruntime.Options) {
		o.MaxRestarts = 2
		o.BackoffBase = 10 * time.Millisecond
	}))
	for n := 0; n < 3; n++ {
		pid := inst.PID()
		_, _ = inst.Platform().ValidateCredentials(context.Background(), &pluginv1.ValidateCredentialsRequest{CredentialsJson: `"crash"`})
		waitFor(t, "restart or failure", 10*time.Second, func() bool {
			s, _ := inst.State()
			return s == grpcruntime.StateFailed || (s == grpcruntime.StateReady && inst.PID() != pid && inst.PID() != 0)
		})
	}
	s, msg := inst.State()
	if s != grpcruntime.StateFailed {
		t.Fatalf("state = %s (%s)", s, msg)
	}
	if _, err := inst.Platform().ClassifyError(context.Background(), &pluginv1.ClassifyErrorRequest{}); core.AsError(err).Code != "plugin_unavailable" {
		t.Fatalf("call on failed instance: %v", err)
	}
}

func TestLoadRejectsConfig(t *testing.T) {
	e := setup(t, registrytest.DefaultGrants())
	e.setConfig(t, `{"reject":true}`)
	rt := e.runtime(t, nil)
	pkg, err := e.pkgs.Open(context.Background(), e.key, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Load(context.Background(), pkg); err == nil {
		t.Fatal("expected Configure rejection to fail Load")
	}
}

// TestBroadcastRealPlugin: Publish from one instance reaches the bus; a
// message from another node is delivered through AppService.OnBroadcast.
func TestBroadcastRealPlugin(t *testing.T) {
	e := setup(t, registrytest.DefaultGrants())
	bus := &registrytest.MemBus{}
	rt := e.runtime(t, func(o *grpcruntime.Options) { o.Bus = bus })
	inst := e.load(t, rt)

	if _, err := httpCall(t, inst, &pluginv1.HTTPRequest{Method: "POST", Path: "/publish", Body: []byte("v2")}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	msgs := bus.Messages(grpcruntime.BroadcastChannel(e.key))
	if len(msgs) != 1 {
		t.Fatalf("bus messages %d", len(msgs))
	}
	var m grpcruntime.BroadcastMessage
	_ = json.Unmarshal(msgs[0].Payload, &m)
	if m.Topic != "t.ping" || string(m.Payload) != "v2" || m.SourceBootID != "b1" {
		t.Fatalf("message %+v", m)
	}

	if !inst.HandlesBroadcast() {
		t.Fatal("test plugin declares app.broadcast.v1")
	}
	m.SourceNodeID = "n2"
	if err := inst.OnBroadcast(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if v, _ := e.rdb.Get(context.Background(), "plugin:kv:"+e.key+":t:broadcast").Result(); v != "t.ping:v2:n2" {
		t.Fatalf("plugin received %q", v)
	}
}

// TestLimitsStaleAfterRefresh: changing plugins.resource_limits marks the
// running process stale after Refresh; a new instance starts with them.
func TestLimitsStaleAfterRefresh(t *testing.T) {
	e := setup(t, registrytest.DefaultGrants())
	rt := e.runtime(t, nil)
	inst := e.load(t, rt)
	ctx := context.Background()
	if inst.LimitsStale() {
		t.Fatal("fresh instance stale")
	}
	if _, err := e.db.SQL.ExecContext(ctx, `UPDATE plugins SET resource_limits = '{"memory_mb": 77}' WHERE key = $1`, e.key); err != nil {
		t.Fatal(err)
	}
	if err := inst.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if !inst.LimitsStale() {
		t.Fatal("limit change not detected")
	}
	fresh := e.load(t, rt)
	if fresh.LimitsStale() {
		t.Fatal("new instance stale")
	}
}
