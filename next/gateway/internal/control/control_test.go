package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/gateway/internal/peer"
	"github.com/Sub2API-Devs/sup2api/next/gateway/internal/updatesource"
	rc "github.com/Sub2API-Devs/sup2api/next/runtime-contract"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestCompatibilityRejectsUnsafeTransitions(t *testing.T) {
	old := testRelease("old")
	good := testRelease("new")
	if b := Compatibility(old, good); len(b) != 0 {
		t.Fatal(b)
	}
	cases := []struct {
		name   string
		mutate func(*Release)
	}{
		{"schema", func(r *Release) { r.Manifest.SchemaAfter = "contract-2" }},
		{"before", func(r *Release) { r.Manifest.SchemaBefore = "wrong" }},
		{"control", func(r *Release) { r.Manifest.CoreControlProtocol = rc.Range{Min: 1, Max: 1} }},
		{"tasks", func(r *Release) { r.Manifest.TaskProtocol.Min = 2; r.Manifest.TaskProtocol.Max = 2 }},
		{"host", func(r *Release) { r.Manifest.HostAPIVersion++ }},
		{"range expansion", func(r *Release) { r.Manifest.TaskProtocol.Max++ }},
		{"same build", func(r *Release) { r.Manifest.BuildID = old.Manifest.BuildID }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := good
			tc.mutate(&r)
			if len(Compatibility(old, r)) == 0 {
				t.Fatal("unsafe update accepted")
			}
		})
	}
}
func testRelease(id string) Release {
	return Release{Digest: fmt.Sprintf("%064s", id), Manifest: rc.Manifest{CoreVersion: "0.1.0-dev", ShellProtocol: rc.Range{Min: rc.Protocol, Max: rc.Protocol}, CoreControlProtocol: rc.Range{Min: rc.Protocol, Max: rc.Protocol}, ManifestVersion: 1, ReleaseID: id, BuildID: id, SchemaBefore: "contract-1", SchemaAfter: "contract-1", Strategy: "rolling", HostAPIVersion: rc.HostAPIVersion, TaskProtocol: rc.Range{Min: 1, Max: 1}, ClusterProtocol: rc.Range{Min: 1, Max: 1}, Platforms: []rc.Platform{{OS: "linux", Arch: "amd64", RuntimeABI: "test"}}}}
}

type fakeRuntime struct {
	mu       sync.Mutex
	status   rc.Status
	mode     string
	calls    []string
	fail     string
	revision int64
	stopped  bool
	options  []rc.PrepareRequest
	// The first Prepare takes prepareTook (prepareCut counts those cut short);
	// every DrainStop takes stopTook. Both honour ctx.
	prepareTook, stopTook time.Duration
	prepareCut            int
}

// take waits d unless ctx ends first.
func take(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

func (r *fakeRuntime) call(action string) error {
	r.calls = append(r.calls, action)
	if r.fail == action {
		return errors.New("injected " + action + " failure")
	}
	return nil
}
func (r *fakeRuntime) Prepare(ctx context.Context, _ Release, _ string) error {
	r.mu.Lock()
	d := r.prepareTook
	r.mu.Unlock()
	if err := take(ctx, d); err != nil {
		r.mu.Lock()
		r.prepareCut++
		r.mu.Unlock()
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prepareTook = 0
	return r.call("prepare")
}
func (r *fakeRuntime) Redirect(ctx context.Context, n Node) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.call("redirect:" + n.ID); err != nil {
		return err
	}
	r.mode = "forward"
	return nil
}
func (r *fakeRuntime) DrainStop(ctx context.Context, _ string) error {
	if err := take(ctx, r.stopTook); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.call("stop"); err != nil {
		return err
	}
	r.status.Ready = false
	r.stopped = true
	return nil
}
func (r *fakeRuntime) Start(ctx context.Context, digest string, opts rc.PrepareRequest) (rc.Status, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.call("start"); err != nil {
		return r.status, err
	}
	r.stopped = false
	r.options = append(r.options, opts)
	r.status = rc.Status{Hello: rc.Hello{Protocol: rc.Protocol, ClusterProtocol: rc.Range{Min: 1, Max: 1}, TaskProtocol: rc.Range{Min: 1, Max: 1}, HostAPIVersion: rc.HostAPIVersion, CoreVersion: "0.1.0-dev", BootID: "new-boot", ReleaseDigest: digest}, Mode: "prepared", SchemaContract: opts.ExpectedSchemaAfter}
	return r.status, nil
}
func (r *fakeRuntime) Admit(ctx context.Context, a rc.Admission) (rc.Status, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.call("admit"); err != nil {
		return r.status, err
	}
	r.status.Ready = true
	return r.status, nil
}
func (r *fakeRuntime) Local(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.call("local"); err != nil {
		return err
	}
	r.mode = "local"
	r.revision++
	return nil
}
func (r *fakeRuntime) Status(context.Context) (rc.Status, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status, r.mode, nil
}
func (r *fakeRuntime) RouteRevision() int64 { r.mu.Lock(); defer r.mu.Unlock(); return r.revision }

func integrationStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db := openTestDB(t, dsn)
	s := &Store{DB: db, Cluster: fmt.Sprintf("updater-test-%d", time.Now().UnixNano())}
	if err := s.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, q := range []string{
			`DELETE FROM updater.events WHERE upgrade_id IN (SELECT id FROM updater.upgrades WHERE cluster_id=?)`,
			`DELETE FROM updater.stop_confirmations WHERE cluster_id=?`,
			`DELETE FROM updater.steps WHERE upgrade_id IN (SELECT id FROM updater.upgrades WHERE cluster_id=?)`,
			`DELETE FROM updater.upgrades WHERE cluster_id=?`,
			`DELETE FROM updater.node_admissions WHERE cluster_id=?`,
			`DELETE FROM updater.update_checks WHERE cluster_id=?`,
			`DELETE FROM updater.nodes WHERE cluster_id=?`,
			`DELETE FROM updater.clusters WHERE cluster_id=?`,
		} {
			db.Exec(q, s.Cluster)
		}
	})
	return s
}

// openTestDB opens a GORM pool on dsn that the test closes when it ends.
func openTestDB(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{SkipDefaultTransaction: true, DisableAutomaticPing: true, Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

// quoteIdent quotes an SQL identifier: in double quotes, with each double
// quote inside doubled.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
func setupEngines(t *testing.T) (*Store, *Engine, *Engine, Release) {
	t.Helper()
	return setupEnginesWith(t, false)
}

// setupEnginesWith registers two nodes, a (primary) and b, serving the old
// release. rolling declares both releases rolling; otherwise they are declared
// maintenance and upgrade primary-first.
func setupEnginesWith(t *testing.T, rolling bool) (*Store, *Engine, *Engine, Release) {
	t.Helper()
	s := integrationStore(t)
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })
	s.Locks = NewRedisLocks(redisClient)
	s.Redis = redisClient
	ctx := context.Background()
	old, target := testRelease(s.Cluster+"old"), testRelease(s.Cluster+"new")
	if !rolling {
		old.Manifest.Strategy, target.Manifest.Strategy = "maintenance", "maintenance"
	}
	for _, r := range []Release{old, target} {
		r.Raw, _ = json.Marshal(r.Manifest)
		if err := s.PutRelease(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	aid, bid := s.Cluster+"a", s.Cluster+"b"
	if err := s.InitCluster(ctx, aid, old.Digest); err != nil {
		t.Fatal(err)
	}
	mk := func(id string) *Engine {
		n := Node{Enabled: true, PeerProtocol: PeerProtocol, Strategy: PrimaryFirst, ID: id, PeerURL: "https://" + id, ShellBootID: "boot", OS: "linux", Arch: "amd64", RuntimeABI: "test"}
		if err := s.Register(ctx, n); err != nil {
			t.Fatal(err)
		}
		e := &Engine{Store: s, Node: n, Runtime: &fakeRuntime{status: rc.Status{Hello: rc.Hello{BootID: "old-" + id, ReleaseDigest: old.Digest}, Ready: true}, mode: "local", revision: 1}}
		if err := e.Heartbeat(ctx); err != nil {
			t.Fatal(err)
		}
		return e
	}
	return s, mk(aid), mk(bid), target
}

func TestPostgresRollingPlanPauseResumeAndIdempotency(t *testing.T) {
	s, a, b, target := setupEngines(t)
	ctx := context.Background()
	pf, err := s.Preflight(ctx, target.Digest)
	if err != nil || len(pf.Blockers) != 0 {
		t.Fatalf("preflight: %+v %v", pf, err)
	}
	p, err := s.Create(ctx, target.Digest, pf.ExpectedRevision, "same-key", "admin")
	if err != nil {
		t.Fatal(err)
	}
	same, err := s.Create(ctx, target.Digest, pf.ExpectedRevision, "same-key", "admin")
	if err != nil || same.ID != p.ID {
		t.Fatalf("idempotency: %s %v", same.ID, err)
	}
	if _, err = s.Create(ctx, target.Digest, pf.ExpectedRevision+1, "same-key", "admin"); !errors.Is(err, ErrConflict) {
		t.Fatalf("key conflict: %v", err)
	}
	ar := a.Runtime.(*fakeRuntime)
	ar.fail = "admit"
	for i := 0; i < 60; i++ {
		if err = a.Coordinate(ctx); err != nil {
			t.Fatal(err)
		}
		_ = a.Work(ctx)
		_ = b.Work(ctx)
		p, err = s.Plan(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if p.Status == "paused" {
			break
		}
	}
	if p.Status != "paused" || !strings.Contains(p.Error, "injected admit") {
		t.Fatalf("expected durable pause: %+v", p)
	}
	before := len(ar.calls)
	if err = a.Work(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(ar.calls[before:], ","), "start") || strings.Contains(strings.Join(ar.calls[before:], ","), "admit") {
		t.Fatal("paused worker executed")
	}
	if _, err = s.Action(ctx, p.ID, "cancel", "admin"); err == nil {
		t.Fatal("cancel accepted after disruption")
	}
	ar.fail = ""
	if _, err = s.Action(ctx, p.ID, "resume", "admin"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if err = a.Coordinate(ctx); err != nil {
			t.Fatal(err)
		}
		if err = a.Work(ctx); err != nil {
			t.Fatal(err)
		}
		if err = b.Work(ctx); err != nil {
			t.Fatal(err)
		}
		p, err = s.Plan(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if p.Status == "completed" {
			break
		}
	}
	if p.Status != "completed" {
		t.Fatalf("not complete: %+v", p)
	}
	expected := Steps(p)
	if len(p.Steps) != len(expected) {
		t.Fatal("missing step ACKs")
	}
	for i, st := range p.Steps {
		if st.Action != expected[i].Action || st.NodeID != expected[i].NodeID || st.Status != "done" {
			t.Fatalf("step %d: %+v", i, st)
		}
	}
	_, base, _, err := s.ClusterState(ctx)
	if err != nil || base != target.Digest {
		t.Fatalf("baseline not committed: %s %v", base, err)
	}
	events, err := s.Events(ctx, p.ID, 0)
	if err != nil || len(events) < len(p.Steps) {
		t.Fatalf("events missing %d %v", len(events), err)
	}
}
func TestPostgresOfflineNodeBlocksAndRestartRecoversPlan(t *testing.T) {
	s, a, b, target := setupEngines(t)
	ctx := context.Background()
	// Modern shells use expiring Redis observations; their PG snapshot is
	// intentionally unchanged while idle and must not prove liveness.
	err := s.Redis.Del(ctx, s.telemetryKey(b.Node)).Err()
	if err != nil {
		t.Fatal(err)
	}
	pf, err := s.Preflight(ctx, target.Digest)
	if err != nil || len(pf.Blockers) == 0 {
		t.Fatalf("offline preflight accepted: %+v %v", pf, err)
	}
	_ = b.Heartbeat(ctx)
	pf, err = s.Preflight(ctx, target.Digest)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Create(ctx, target.Digest, pf.ExpectedRevision, "recovery", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Action(ctx, p.ID, "pause", "admin"); err != nil {
		t.Fatal(err)
	}
	// A new controller instance reads the same paused plan without replaying work.
	recovered := &Engine{Store: s, Node: a.Node, Runtime: a.Runtime}
	if err = recovered.Coordinate(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := s.Plan(ctx, p.ID)
	if err != nil || len(got.Steps) != 0 {
		t.Fatalf("paused restart advanced: %+v %v", got, err)
	}
	if _, err = s.Action(ctx, p.ID, "cancel", "admin"); err != nil {
		t.Fatal(err)
	}
}
func TestRedisMutualExclusionRenewalAndLoss(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	locks := NewRedisLocks(client)
	locks.TTL = 100 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := locks.WithLock(ctx, "test", time.Second, func(ctx context.Context) error { close(entered); <-ctx.Done(); return ctx.Err() })
		done <- err
	}()
	<-entered
	ran, err := locks.WithLock(ctx, "test", time.Second, func(context.Context) error { t.Error("two holders"); return nil })
	if ran || err != nil {
		t.Fatalf("contention: %v %v", ran, err)
	}
	time.Sleep(180 * time.Millisecond)
	if !server.Exists("lock:test") {
		t.Fatal("lock did not renew")
	}
	server.Del("lock:test")
	server.Set("lock:test", "new-owner")
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("loss not returned")
		}
	case <-time.After(time.Second):
		t.Fatal("loss did not cancel")
	}
	value, _ := server.Get("lock:test")
	if value != "new-owner" {
		t.Fatal("old owner deleted successor")
	}
}
func TestStepSequenceDoesNotCreateProxyLoop(t *testing.T) {
	st := Steps(Plan{Strategy: PrimaryFirst, ID: "p", Nodes: []string{"a", "b", "c"}})
	var actions []string
	for _, s := range st {
		actions = append(actions, s.NodeID+":"+s.Action)
	}
	want := []string{"a:prepare", "b:prepare", "c:prepare", "b:redirect", "b:stop", "c:redirect", "c:stop", "a:maintenance", "a:stop", "a:start-primary", "a:admit", "a:local", "b:redirect", "b:start", "b:admit", "b:local", "c:redirect", "c:start", "c:admit", "c:local"}
	if !reflect.DeepEqual(actions, want) {
		t.Fatal(actions)
	}
}

type bootstrapRecorder struct {
	*fakeRuntime
	flags []bool
}

func (r *bootstrapRecorder) Start(ctx context.Context, digest string, opts rc.PrepareRequest) (rc.Status, error) {
	r.flags = append(r.flags, opts.Bootstrap)
	return r.fakeRuntime.Start(ctx, digest, opts)
}

func TestPostgresBootstrapFlagCannotReimportPluginsAfterFirstAdmission(t *testing.T) {
	_, a, _, _ := setupEngines(t)
	ctx := context.Background()
	runtime := &bootstrapRecorder{fakeRuntime: a.Runtime.(*fakeRuntime)}
	a.Runtime = runtime
	if err := a.Recover(ctx, true); err != nil {
		t.Fatal("first bootstrap", err)
	}
	if err := a.Recover(ctx, true); err != nil {
		t.Fatal("retained bootstrap flag", err)
	}
	if !reflect.DeepEqual(runtime.flags, []bool{true, false}) {
		t.Fatalf("bootstrap permissions across restart: %v", runtime.flags)
	}
}

func drivePlan(t *testing.T, s *Store, a, b *Engine, id, want string) Plan {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 150; i++ {
		if err := a.Coordinate(ctx); err != nil {
			t.Fatal(err)
		}
		_ = a.Work(ctx)
		_ = b.Work(ctx)
		p, err := s.Plan(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if p.Status == want {
			return p
		}
	}
	p, _ := s.Plan(ctx, id)
	t.Fatalf("plan did not reach %s: %+v", want, p)
	return p
}

func TestPostgresRollbackUsesHealthyAnchorAndPreservesInheritedRoute(t *testing.T) {
	s, a, b, target := setupEngines(t)
	ctx := context.Background()
	_, baseline, _, _ := s.ClusterState(ctx)
	pf, err := s.Preflight(ctx, target.Digest)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Create(ctx, target.Digest, pf.ExpectedRevision, "bad-follower", "admin")
	if err != nil {
		t.Fatal(err)
	}
	b.Runtime.(*fakeRuntime).fail = "admit"
	drivePlan(t, s, a, b, p.ID, "paused")
	// A still carries traffic; B must recover before A can be drained.
	recovery, err := s.Rollback(ctx, p.ID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if recovery.ReleaseDigest != baseline || !reflect.DeepEqual(recovery.Nodes, []string{a.Node.ID, b.Node.ID}) {
		t.Fatalf("unsafe recovery order: %+v", recovery)
	}
	again, err := s.Rollback(ctx, p.ID, "admin")
	if err != nil || again.ID != recovery.ID {
		t.Fatalf("rollback not idempotent: %+v %v", again, err)
	}
	old, err := s.Plan(ctx, p.ID)
	if err != nil || old.Status != "superseded" {
		t.Fatal("old plan was not superseded", err)
	}
	br := b.Runtime.(*fakeRuntime)
	br.calls = nil
	if err = b.Recover(ctx, false); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(br.calls, []string{"redirect:" + a.Node.ID, "stop"}) {
		t.Fatalf("restart lost inherited forward route: %v", br.calls)
	}
	br.fail = ""
	drivePlan(t, s, a, b, recovery.ID, "completed")
	pf, err = s.Preflight(ctx, target.Digest)
	if err != nil || len(pf.Blockers) > 0 {
		t.Fatalf("new upgrades remain blocked after recovery: %+v %v", pf, err)
	}
	if _, err = s.Create(ctx, target.Digest, pf.ExpectedRevision, "after-recovery", "admin"); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresRollbackWaitsForOldNodeWork(t *testing.T) {
	s, a, b, target := setupEngines(t)
	ctx := context.Background()
	pf, err := s.Preflight(ctx, target.Digest)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Create(ctx, target.Digest, pf.ExpectedRevision, "busy-rollback", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Action(ctx, p.ID, "pause", "admin"); err != nil {
		t.Fatal(err)
	}
	held := make(chan struct{})
	releaseLock := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, e := s.Locks.WithLock(ctx, "system:upgrade-node:"+s.Cluster+":"+b.Node.ID, time.Second, func(context.Context) error { close(held); <-releaseLock; return nil })
		done <- e
	}()
	<-held
	if _, err = s.Rollback(ctx, p.ID, "admin"); err == nil {
		t.Fatal("rollback started while old node work held its lock")
	}
	current, err := s.Plan(ctx, p.ID)
	if err != nil || current.Status != "paused" {
		t.Fatal("busy rollback changed old plan", err)
	}
	close(releaseLock)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if _, err = s.Rollback(ctx, p.ID, "admin"); err != nil {
		t.Fatal("rollback after old worker drained", err)
	}
	_ = a
}

type delayedFailureRuntime struct {
	*fakeRuntime
	entered, release chan struct{}
}

func (r *delayedFailureRuntime) Admit(context.Context, rc.Admission) (rc.Status, error) {
	close(r.entered)
	<-r.release
	return rc.Status{}, errors.New("delayed old worker failure")
}

func TestPostgresLateOldWorkerCannotResurrectSupersededPlan(t *testing.T) {
	s, a, b, target := setupEngines(t)
	ctx := context.Background()
	pf, err := s.Preflight(ctx, target.Digest)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Create(ctx, target.Digest, pf.ExpectedRevision, "late-worker", "admin")
	if err != nil {
		t.Fatal(err)
	}
	base := a.Runtime.(*fakeRuntime)
	base.fail = "admit"
	drivePlan(t, s, a, b, p.ID, "paused")
	base.fail = ""
	delayed := &delayedFailureRuntime{fakeRuntime: base, entered: make(chan struct{}), release: make(chan struct{})}
	a.Runtime = delayed
	if _, err = s.Action(ctx, p.ID, "resume", "admin"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Work(ctx) }()
	<-delayed.entered
	if _, err = s.Action(ctx, p.ID, "pause", "admin"); err != nil {
		t.Fatal(err)
	}
	// Bypass the lease in this fault-injection worker to model a late result
	// after ownership was lost; normal workers are blocked by the lock barrier.
	recovery, err := s.Rollback(ctx, p.ID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	beforeLate, err := s.Plan(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	close(delayed.release)
	if err = <-done; err == nil {
		t.Fatal("fault injection did not fail")
	}
	old, err := s.Plan(ctx, p.ID)
	if err != nil || old.Status != "superseded" {
		t.Fatalf("late worker resurrected old plan: %+v %v", old, err)
	}
	if !reflect.DeepEqual(beforeLate.Steps, old.Steps) {
		t.Fatalf("late worker rewrote superseded step history: before=%+v after=%+v", beforeLate.Steps, old.Steps)
	}
	current, err := s.Plan(ctx, recovery.ID)
	if err != nil || current.Status != "running" {
		t.Fatalf("recovery plan was disturbed: %+v %v", current, err)
	}
}
func (r *fakeRuntime) Maintenance(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mode = "maintenance"
	return r.call("maintenance")
}
func (r *fakeRuntime) Stopped(context.Context) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopped, nil
}

func TestMaintenanceCompatibility(t *testing.T) {
	old, target := testRelease("old"), testRelease("target")
	target.Manifest.Strategy = "maintenance"
	target.Manifest.SchemaAfter = "contract-2"
	if blockers := Compatibility(old, target); len(blockers) > 0 {
		t.Fatal(blockers)
	}
	if len(rollbackCompatibility(target, old)) == 0 {
		t.Fatal("cross-schema rollback accepted")
	}
}

func TestPostgresMaintenanceStopBarrierAndRecovery(t *testing.T) {
	s, a, b, target := setupEngines(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pf, err := s.Preflight(ctx, target.Digest)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Create(ctx, target.Digest, pf.ExpectedRevision, "barrier", "admin")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		if err = a.Coordinate(ctx); err != nil {
			t.Fatal(err)
		}
		_ = a.Work(ctx)
		_ = b.Work(ctx)
		p, err = s.Plan(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Steps) > 0 && p.Steps[len(p.Steps)-1].Action == "stop" && p.Steps[len(p.Steps)-1].NodeID == b.Node.ID && p.Steps[len(p.Steps)-1].Status == "done" {
			break
		}
	}
	br := b.Runtime.(*fakeRuntime)
	before := len(br.options)
	if err = b.Recover(ctx, false); err != nil {
		t.Fatal(err)
	}
	if len(br.options) != before || !br.stopped {
		t.Fatal("stopped follower restarted old core")
	}
	if err = s.Redis.Del(ctx, s.telemetryKey(b.Node)).Err(); err != nil {
		t.Fatal(err)
	}
	if err = a.Coordinate(ctx); err != nil {
		t.Fatal(err)
	} // advance completed stop cursor
	if err = a.Coordinate(ctx); err == nil {
		t.Fatal("stale stop ACK permitted primary shutdown")
	}
	if ctx.Err() != nil {
		t.Fatal("coordinator blocked on its own plan row lock:", ctx.Err())
	}
	var blockedReason string
	if err = s.db(ctx).Raw(`SELECT blocked_reason FROM updater.upgrades WHERE id=?`, p.ID).Row().Scan(&blockedReason); err != nil || !strings.Contains(blockedReason, "waiting for followers to stop") {
		t.Fatalf("blocked reason was not persisted: %q %v", blockedReason, err)
	}
	if err = b.Heartbeat(ctx); err != nil {
		t.Fatal(err)
	}
	// An unplanned joining node must stop instead of restoring the baseline.
	n := Node{Enabled: true, PeerProtocol: PeerProtocol, Strategy: PrimaryFirst, ID: s.Cluster + "joining", ShellBootID: "join", PeerURL: "https://join", OS: "linux", Arch: "amd64", RuntimeABI: "test"}
	if err = s.Register(ctx, n); err != nil {
		t.Fatal(err)
	}
	joinRuntime := &fakeRuntime{mode: "maintenance"}
	join := &Engine{Store: s, Node: n, Runtime: joinRuntime}
	if err = join.Recover(ctx, false); err != nil {
		t.Fatal(err)
	}
	if len(joinRuntime.options) != 0 || !joinRuntime.stopped {
		t.Fatal("joining node started during maintenance")
	}
	drivePlan(t, s, a, b, p.ID, "completed")
	ar := a.Runtime.(*fakeRuntime)
	if len(ar.options) == 0 || !ar.options[0].AllowMigration || ar.options[0].Bootstrap || !ar.options[0].CoordinatePlugins || ar.options[0].ExpectedSchemaBefore != "contract-1" {
		t.Fatalf("primary permissions: %+v", ar.options)
	}
	for _, opts := range br.options {
		if opts.AllowMigration || opts.Bootstrap || opts.CoordinatePlugins {
			t.Fatalf("follower permissions: %+v", opts)
		}
	}
}

func TestPostgresCrossSchemaRollbackIsRejected(t *testing.T) {
	s, _, _, target := setupEngines(t)
	ctx := context.Background()
	pf, _ := s.Preflight(ctx, target.Digest)
	p, err := s.Create(ctx, target.Digest, pf.ExpectedRevision, "cross-schema", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Action(ctx, p.ID, "pause", "admin"); err != nil {
		t.Fatal(err)
	}
	crossSchema := target.Manifest
	crossSchema.SchemaAfter = "contract-2"
	raw, _ := json.Marshal(crossSchema)
	if err = releaseRow(s.db(ctx), updatesource.LineCore, target.Digest).Update("manifest", raw).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = s.Rollback(ctx, p.ID, "admin"); err == nil {
		t.Fatal("cross-schema recovery accepted")
	}
}

func TestPostgresNodeRegistrationDisableAndBootValidation(t *testing.T) {
	s, a, _, _ := setupEngines(t)
	ctx := context.Background()
	revoked := false
	s.RevokePeer = func(context.Context, string) error { revoked = true; return nil }
	if err := s.ValidateNode(ctx, a.Node.ID, a.Node.ShellBootID); err != nil {
		t.Fatal(err)
	}
	if err := s.DisableNode(ctx, a.Node.ID); err != nil || !revoked {
		t.Fatalf("disable: %v %v", err, revoked)
	}
	if err := s.Register(ctx, a.Node); err == nil {
		t.Fatal("disabled node registered")
	}
	if err := s.ValidateNode(ctx, a.Node.ID, a.Node.ShellBootID); err == nil {
		t.Fatal("disabled identity accepted")
	}
	if err := s.EnableNode(ctx, a.Node.ID); err != nil {
		t.Fatal(err)
	}
	nodes, _ := s.Nodes(ctx)
	for _, n := range nodes {
		if n.ID == a.Node.ID && n.Ready {
			t.Fatal("enable granted service admission")
		}
	}
	if err := s.Register(ctx, a.Node); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateNode(ctx, a.Node.ID, "different-boot"); err == nil {
		t.Fatal("old boot accepted")
	}
}

func TestPostgresUnknownCoreAndOldShellBlockUpgrade(t *testing.T) {
	s, a, _, target := setupEngines(t)
	ctx := context.Background()
	if err := s.Redis.ZAdd(ctx, "node:live", redis.Z{Score: 1, Member: "unmanaged-test"}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := s.Redis.HSet(ctx, "node:info:unmanaged-test", "node_id", "old-external").Err(); err != nil {
		t.Fatal(err)
	}
	pf, err := s.Preflight(ctx, target.Digest)
	if err != nil || !strings.Contains(strings.Join(pf.Blockers, ","), "unmanaged") {
		t.Fatalf("unmanaged accepted: %+v %v", pf, err)
	}
	_ = s.Redis.Del(ctx, "node:info:unmanaged-test").Err()
	if err = s.db(ctx).Exec(`UPDATE updater.nodes SET peer_protocol=0 WHERE node_id=?`, a.Node.ID).Error; err != nil {
		t.Fatal(err)
	}
	pf, err = s.Preflight(ctx, target.Digest)
	if err != nil || !strings.Contains(strings.Join(pf.Blockers, ","), "capabilities") {
		t.Fatalf("old shell accepted: %+v %v", pf, err)
	}
}

// The heartbeat's node snapshot is taken before the node writes the boot of
// its restarted core, so it still names the old boot; the unmanaged-core
// check reads PG and accepts the new one.
func TestPostgresUnmanagedBlockersReadTheBootFromPG(t *testing.T) {
	s, a, b, _ := setupEngines(t)
	ctx := context.Background()
	rt := a.Runtime.(*fakeRuntime)
	rt.mu.Lock()
	rt.status.Hello.BootID = "new-a"
	rt.mu.Unlock()
	if err := a.Heartbeat(ctx); err != nil {
		t.Fatal(err)
	}
	oldBoot := "old-" + a.Node.ID
	stale := false
	for _, n := range s.NodeDirectory() {
		stale = stale || (n.ID == a.Node.ID && n.CoreBootID == oldBoot)
	}
	if !stale {
		t.Fatalf("the snapshot does not hold the old boot: %+v", s.NodeDirectory())
	}
	register := func(node, managed, boot string) {
		t.Helper()
		if err := s.Redis.ZAdd(ctx, "node:live", redis.Z{Score: 1, Member: "live-" + node}).Err(); err != nil {
			t.Fatal(err)
		}
		if err := s.Redis.HSet(ctx, "node:info:live-"+node, "node_id", node, "managed", managed, "core_boot_id", boot).Err(); err != nil {
			t.Fatal(err)
		}
	}
	register(b.Node.ID, "true", "old-"+b.Node.ID)
	for _, tc := range []struct {
		name, managed, boot string
		blocked             bool
	}{
		{"the boot PG records", "true", "new-a", false},
		{"the snapshot's old boot", "true", oldBoot, true},
		{"an unmanaged core", "false", "new-a", true},
		{"no boot", "true", "", true},
	} {
		register(a.Node.ID, tc.managed, tc.boot)
		blockers, err := s.UnmanagedBlockers(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"unmanaged or unrecognized active core: " + a.Node.ID}; tc.blocked != (len(blockers) > 0) || (tc.blocked && !reflect.DeepEqual(blockers, want)) {
			t.Errorf("%s: %v", tc.name, blockers)
		}
	}
}

func TestPostgresPausedCandidateCannotSelfAdmit(t *testing.T) {
	s, a, b, target := setupEngines(t)
	ctx := context.Background()
	pf, _ := s.Preflight(ctx, target.Digest)
	p, err := s.Create(ctx, target.Digest, pf.ExpectedRevision, "paused-candidate", "admin")
	if err != nil {
		t.Fatal(err)
	}
	a.Runtime.(*fakeRuntime).fail = "admit"
	drivePlan(t, s, a, b, p.ID, "paused")
	ar := a.Runtime.(*fakeRuntime)
	ar.fail = ""
	before := len(ar.options)
	if err = a.Recover(ctx, false); err != nil {
		t.Fatal(err)
	}
	if len(ar.options) != before || ar.status.Ready {
		t.Fatal("paused candidate autonomously started or admitted")
	}
	if err = a.Admit(ctx, target.Digest); err == nil {
		t.Fatal("direct admit bypassed paused plan")
	}
	// A new shell boot cannot reuse the stopped follower's prior confirmation.
	n := b.Node
	n.ShellBootID = "replacement-boot"
	if err = s.Register(ctx, n); err != nil {
		t.Fatal(err)
	}
	if err = a.followersStopped(ctx); err == nil {
		t.Fatal("old boot stop ACK was reused")
	}
}

// statusHookRuntime runs hook once on the Status call Admit makes after its
// authorization precheck, modelling an operator action in that window.
type statusHookRuntime struct {
	*fakeRuntime
	hook func()
}

func (r *statusHookRuntime) Status(ctx context.Context) (rc.Status, string, error) {
	if hook := r.hook; hook != nil {
		r.hook = nil
		hook()
	}
	return r.fakeRuntime.Status(ctx)
}

func TestPostgresPauseBetweenAdmissionCheckAndCommitWins(t *testing.T) {
	s, a, b, target := setupEngines(t)
	ctx := context.Background()
	pf, _ := s.Preflight(ctx, target.Digest)
	p, err := s.Create(ctx, target.Digest, pf.ExpectedRevision, "pause-admit", "admin")
	if err != nil {
		t.Fatal(err)
	}
	ar := a.Runtime.(*fakeRuntime)
	ar.fail = "admit"
	drivePlan(t, s, a, b, p.ID, "paused")
	ar.fail = ""
	if _, err = s.Action(ctx, p.ID, "resume", "admin"); err != nil {
		t.Fatal(err)
	}
	admitsBefore := strings.Count(strings.Join(ar.calls, ","), "admit")
	// The earlier failed attempt already committed one admission row; only
	// its revision can show whether another admission slipped through.
	admission := func() (rev int64) {
		_ = s.db(ctx).Raw(`SELECT COALESCE((SELECT revision FROM updater.node_admissions WHERE cluster_id=? AND node_id=?),0)`, s.Cluster, a.Node.ID).Row().Scan(&rev)
		return rev
	}
	revisionBefore := admission()
	a.Runtime = &statusHookRuntime{fakeRuntime: ar, hook: func() {
		if _, err := s.Action(ctx, p.ID, "pause", "admin"); err != nil {
			t.Error(err)
		}
	}}
	if err = a.Admit(ctx, target.Digest); err == nil {
		t.Fatal("admission committed after the plan was paused")
	}
	if rev := admission(); rev != revisionBefore || strings.Count(strings.Join(ar.calls, ","), "admit") != admitsBefore {
		t.Fatalf("paused plan admitted the candidate: revision %d->%d calls=%v", revisionBefore, rev, ar.calls)
	}
	// Once resumed, the same candidate is admitted through the normal path.
	if _, err = s.Action(ctx, p.ID, "resume", "admin"); err != nil {
		t.Fatal(err)
	}
	if err = a.Admit(ctx, target.Digest); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresPlanCreatedBeforeBaselineAdmissionCommitWins(t *testing.T) {
	s, a, _, target := setupEngines(t)
	ctx := context.Background()
	_, baseline, _, err := s.ClusterState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ar := a.Runtime.(*fakeRuntime)
	ar.status = rc.Status{Hello: rc.Hello{Protocol: rc.Protocol, ClusterProtocol: rc.Range{Min: 1, Max: 1}, TaskProtocol: rc.Range{Min: 1, Max: 1}, HostAPIVersion: rc.HostAPIVersion, CoreVersion: "0.1.0-dev", BootID: "restarted", ReleaseDigest: baseline}, SchemaContract: "contract-1"}
	a.Runtime = &statusHookRuntime{fakeRuntime: ar, hook: func() {
		pf, err := s.Preflight(ctx, target.Digest)
		if err == nil {
			_, err = s.Create(ctx, target.Digest, pf.ExpectedRevision, "baseline-race", "admin")
		}
		if err != nil {
			t.Error(err)
		}
	}}
	if err = a.Admit(ctx, baseline); err == nil {
		t.Fatal("baseline admission committed after a plan became active")
	}
}

func TestPostgresPeerAuthorizationClassifiesFailures(t *testing.T) {
	s, a, b, target := setupEngines(t)
	ctx := context.Background()
	_, baseline, _, err := s.ClusterState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name                         string
		source, boot, dest, scope, d string
		want                         error
	}{
		{"follower forward", b.Node.ID, "boot", a.Node.ID, "forward", "", nil},
		{"baseline artifact", b.Node.ID, "boot", a.Node.ID, "core-artifact", baseline, nil},
		{"stale boot", b.Node.ID, "old-boot", a.Node.ID, "forward", "", peer.ErrUnauthorized},
		{"unknown node", "ghost", "boot", a.Node.ID, "forward", "", peer.ErrUnauthorized},
		{"primary to follower", a.Node.ID, "boot", b.Node.ID, "forward", "", peer.ErrForbidden},
		{"unapproved artifact", b.Node.ID, "boot", a.Node.ID, "core-artifact", target.Digest, peer.ErrForbidden},
		{"unknown scope", b.Node.ID, "boot", a.Node.ID, "admin", "", peer.ErrForbidden},
		{"no node pushes packages to another", b.Node.ID, "boot", a.Node.ID, "plugin-upload", target.Digest, peer.ErrForbidden},
		{"unreferenced plugin package", b.Node.ID, "boot", a.Node.ID, "plugin-artifact", target.Digest, peer.ErrForbidden},
	} {
		err := s.AuthorizePeer(ctx, c.source, c.boot, c.dest, c.scope, c.d)
		if (c.want == nil) != (err == nil) || (c.want != nil && !errors.Is(err, c.want)) {
			t.Errorf("%s: got %v want %v", c.name, err, c.want)
		}
	}
}

func TestPostgresDisabledWorkerStillStopsAndAcknowledges(t *testing.T) {
	s, a, b, target := setupEngines(t)
	ctx := context.Background()
	pf, _ := s.Preflight(ctx, target.Digest)
	p, err := s.Create(ctx, target.Digest, pf.ExpectedRevision, "disabled-stop", "admin")
	if err != nil {
		t.Fatal(err)
	}
	// Issue the follower stop but disable it before ordinary worker execution.
	for i := 0; i < 20; i++ {
		if err = a.Coordinate(ctx); err != nil {
			t.Fatal(err)
		}
		p, _ = s.Plan(ctx, p.ID)
		if len(p.Steps) > 0 && p.Steps[len(p.Steps)-1].Action == "stop" {
			break
		}
		_ = a.Work(ctx)
		_ = b.Work(ctx)
	}
	s.RevokePeer = func(context.Context, string) error { return nil }
	if err = s.DisableNode(ctx, b.Node.ID); err != nil {
		t.Fatal(err)
	}
	b.PeerCheck = func(context.Context) error { return errors.New("revoked Redis credentials") }
	if err = b.Work(ctx); err == nil {
		t.Fatal("disabled worker got ordinary permission")
	}
	if !b.Runtime.(*fakeRuntime).stopped {
		t.Fatal("disabled core survived")
	}
	var ack bool
	if err = s.db(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM updater.stop_confirmations WHERE upgrade_id=? AND node_id=? AND shell_boot_id=?)`, p.ID, b.Node.ID, b.Node.ShellBootID).Row().Scan(&ack); err != nil || !ack {
		t.Fatalf("safety stop was not persisted: %v", err)
	}
	// The safety report must match the persisted runtime identity, not just the
	// shell's static startup identity, so the coordinator can acknowledge it.
	if err = a.Coordinate(ctx); err != nil {
		t.Fatal(err)
	}
	p, err = s.Plan(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range p.Steps {
		if step.NodeID == b.Node.ID && step.Action == "stop" {
			if step.Status != "done" {
				t.Fatalf("disabled safety stop was not acknowledged: %+v", step)
			}
			if err = a.followersStopped(ctx); err != nil {
				t.Fatalf("fresh disabled stop report rejected: %v", err)
			}
			return
		}
	}
	t.Fatal("follower stop step missing")
}

func TestPostgresFollowerResumesForwardingWhenPrimaryServes(t *testing.T) {
	s, a, b, _ := setupEngines(t)
	ctx := context.Background()
	br := b.Runtime.(*fakeRuntime)
	redirects := func() int { return strings.Count(strings.Join(br.calls, ","), "redirect:"+a.Node.ID) }
	if err := s.db(ctx).Exec(`UPDATE updater.nodes SET ready=false WHERE node_id=?`, a.Node.ID).Error; err != nil {
		t.Fatal(err)
	}
	br.mode = "maintenance"
	_ = b.Heartbeat(ctx)
	if redirects() != 0 || br.mode != "maintenance" {
		t.Fatal("follower forwarded to an unavailable primary")
	}
	// The direct edit stands in for a primary that was not ready; its next
	// heartbeat with a change (here: the periodic rewrite) repairs the row.
	a.lastWrittenAt = time.Time{}
	if err := a.Heartbeat(ctx); err != nil {
		t.Fatal(err)
	}
	_ = b.Heartbeat(ctx)
	if redirects() != 1 || br.mode != "forward" {
		t.Fatalf("follower stayed in maintenance while the primary serves: %v", br.calls)
	}
	// The primary never forwards, and a disabled follower stays closed.
	ar := a.Runtime.(*fakeRuntime)
	ar.mode = "maintenance"
	_ = a.Heartbeat(ctx)
	if ar.mode != "maintenance" {
		t.Fatal("primary left maintenance by forwarding")
	}
	ar.mode = "local"
	_ = a.Heartbeat(ctx)
	s.RevokePeer = func(context.Context, string) error { return nil }
	if err := s.DisableNode(ctx, b.Node.ID); err != nil {
		t.Fatal(err)
	}
	br.mode = "maintenance"
	_ = b.Heartbeat(ctx)
	if redirects() != 1 || br.mode != "maintenance" {
		t.Fatal("disabled follower resumed forwarding")
	}
}
