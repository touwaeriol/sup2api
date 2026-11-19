package job

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/background"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/task"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

func init() {
	// A run on a node without the plugin goes to whichever node fetches it
	// next, so the number of put-offs before it reaches the plugin's node
	// has no bound: make each one short. It never changes after.
	notLoadedRetry = 300 * time.Millisecond
}

// ---------------------------------------------------------------- fakes

type fakeGen struct {
	core.Generation
	plugins []core.PluginInfo
	jobs    []core.JobBinding
	app     core.AppPlugin
}

func (g *fakeGen) Plugins() []core.PluginInfo { return g.plugins }
func (g *fakeGen) Jobs() []core.JobBinding    { return g.jobs }
func (g *fakeGen) JobRunner(key string) (core.AppPlugin, bool) {
	if _, ok := g.Plugin(key); ok {
		return g.app, g.app != nil
	}
	return nil, false
}
func (g *fakeGen) SettingsValidator(string) (core.SettingsValidator, bool) { return nil, false }
func (g *fakeGen) Plugin(key string) (core.PluginInfo, bool) {
	for _, p := range g.plugins {
		if p.Key == key {
			return p, true
		}
	}
	return core.PluginInfo{}, false
}

type fakeRegistry struct {
	mu  sync.Mutex
	gen core.Generation
	fns []func(core.Generation)
}

func (r *fakeRegistry) Current() core.Generation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.gen
}

func (r *fakeRegistry) OnChange(fn func(core.Generation)) func() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fns = append(r.fns, fn)
	return func() {}
}

// set switches to a generation running plugin p (none when p is nil) with
// the jobs of its manifest.
func (r *fakeRegistry) set(p *core.PluginInfo, app core.AppPlugin) {
	gen := &fakeGen{app: app}
	if p != nil {
		gen.plugins = []core.PluginInfo{*p}
		for _, j := range p.Manifest.Jobs {
			gen.jobs = append(gen.jobs, core.JobBinding{Plugin: *p, Job: j, Client: app})
		}
	}
	r.mu.Lock()
	r.gen = gen
	fns := append([]func(core.Generation){}, r.fns...)
	r.mu.Unlock()
	for _, fn := range fns {
		fn(gen)
	}
}

type memBus struct {
	mu   sync.Mutex
	subs map[string][]func([]byte)
}

func (b *memBus) Publish(_ context.Context, channel string, payload []byte) error {
	b.mu.Lock()
	hs := append([]func([]byte){}, b.subs[channel]...)
	b.mu.Unlock()
	for _, h := range hs {
		h(payload)
	}
	return nil
}

func (b *memBus) Subscribe(channel string, h func([]byte)) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subs == nil {
		b.subs = map[string][]func([]byte){}
	}
	b.subs[channel] = append(b.subs[channel], h)
	return func() {}
}

type fakeApp struct {
	mu    sync.Mutex
	calls []*pluginv1.RunJobRequest
	run   func(ctx context.Context, in *pluginv1.RunJobRequest) (*pluginv1.RunJobResponse, error)
}

func (a *fakeApp) OnEvents(context.Context, *pluginv1.OnEventsRequest) (*pluginv1.OnEventsResponse, error) {
	return nil, errors.New("not used")
}

func (a *fakeApp) RunJob(ctx context.Context, in *pluginv1.RunJobRequest) (*pluginv1.RunJobResponse, error) {
	a.mu.Lock()
	a.calls = append(a.calls, in)
	run := a.run
	a.mu.Unlock()
	if run != nil {
		return run(ctx, in)
	}
	return &pluginv1.RunJobResponse{Message: "ok", Count: 2}, nil
}

func (a *fakeApp) snapshot() []*pluginv1.RunJobRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]*pluginv1.RunJobRequest(nil), a.calls...)
}

// ---------------------------------------------------------------- helpers

type env struct {
	db  *store.DB
	bus *memBus
}

func newEnv(t *testing.T) env { return env{db: testutil.DB(t), bus: &memBus{}} }

// plugin records plugin key as enabled at version and returns its info.
func (e env) plugin(t *testing.T, key, version string, jobs ...manifest.Job) *core.PluginInfo {
	t.Helper()
	_, err := e.db.SQL.ExecContext(context.Background(), `INSERT INTO plugins (key, name, status, active_version)
		VALUES ($1, '{"en":"Demo","zh":"演示"}', 'enabled', $2)
		ON CONFLICT (key) DO UPDATE SET status = 'enabled', active_version = EXCLUDED.active_version`, key, version)
	require.NoError(t, err)
	return &core.PluginInfo{Key: key, Version: version,
		Manifest: &manifest.Manifest{Key: key, Version: version, Name: manifest.LocalizedText{"en": "Demo", "zh": "演示"}, Jobs: jobs}}
}

type node struct {
	tasks  *task.Service
	bridge *Bridge
	reg    *fakeRegistry
}

// node starts a core node: the task service (both backends) and the bridge.
func (e env) node(t *testing.T, id string, p *core.PluginInfo, app core.AppPlugin) node {
	t.Helper()
	reg := &fakeRegistry{}
	reg.set(p, app)
	tasks, err := task.New(task.Options{NodeID: id, Version: "test", Bus: e.bus, DB: e.db, Executor: background.New(8)})
	require.NoError(t, err)
	require.NoError(t, tasks.StartNode(t.Context()))
	require.NoError(t, tasks.StartCluster(t.Context()))
	b := New(e.db, reg, e.bus, nil, id, Options{Version: "test", Resync: time.Hour})
	require.NoError(t, b.Start(t.Context(), tasks))
	t.Cleanup(func() {
		_ = b.Stop(context.Background())
		tasks.Stop(context.Background())
	})
	return node{tasks: tasks, bridge: b, reg: reg}
}

func waitFor(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

type runRow struct {
	Scope, Node, Status, Message string
	Manual                       bool
	ScheduledAt                  time.Time
}

func runRows(t *testing.T, db *store.DB, key, jobID string) []runRow {
	t.Helper()
	rows, err := db.SQL.QueryContext(context.Background(), `SELECT scope, node_id, status, message, manual, scheduled_at
		FROM plugin_job_runs WHERE plugin_key = $1 AND job_id = $2 ORDER BY id`, key, jobID)
	require.NoError(t, err)
	defer rows.Close()
	var out []runRow
	for rows.Next() {
		var r runRow
		require.NoError(t, rows.Scan(&r.Scope, &r.Node, &r.Status, &r.Message, &r.Manual, &r.ScheduledAt))
		out = append(out, r)
	}
	require.NoError(t, rows.Err())
	return out
}

type scheduledRow struct {
	Source, Schedule string
	TimeoutMS        int64
	MaxAttempts      int
}

func readTask(t *testing.T, db *store.DB, name string) (scheduledRow, bool) {
	t.Helper()
	var r scheduledRow
	err := db.SQL.QueryRowContext(context.Background(), `SELECT source, schedule, timeout_ms, max_attempts
		FROM scheduled_tasks WHERE name = $1`, name).Scan(&r.Source, &r.Schedule, &r.TimeoutMS, &r.MaxAttempts)
	if store.IsNoRows(err) {
		return r, false
	}
	require.NoError(t, err)
	return r, true
}

// requireCode checks the API error code of err.
func requireCode(t *testing.T, err error, want *core.Error) {
	t.Helper()
	var got *core.Error
	require.ErrorAs(t, err, &got)
	require.Equal(t, want.Code, got.Code, err.Error())
}

func registered(n node, name string) bool {
	_, ok := n.tasks.Registry().Get(name)
	return ok
}

// ---------------------------------------------------------------- tests

// A cluster job becomes the cluster task plugin.<key>.<id> with its row, runs
// once per slot on one node and is recorded for the plugin page.
func TestClusterJobRunsOncePerSlot(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	p := e.plugin(t, "demo", "1.0.0", manifest.Job{ID: "sync", Schedule: "@every 5s", TimeoutSec: 30})
	appA, appB := &fakeApp{}, &fakeApp{}
	e.node(t, "node-a", p, appA)
	e.node(t, "node-b", p, appB)

	row, ok := readTask(t, e.db, "plugin.demo.sync")
	require.True(t, ok)
	require.Equal(t, scheduledRow{Source: "plugin_manifest", Schedule: "@every 5s", TimeoutMS: 30000, MaxAttempts: 1}, row)

	waitFor(t, "two slots", 25*time.Second, func() bool {
		n := 0
		for _, r := range runRows(t, e.db, "demo", "sync") {
			if r.Status == StatusSucceeded {
				n++
			}
		}
		return n >= 2
	})
	slots := map[time.Time]bool{}
	for _, r := range runRows(t, e.db, "demo", "sync") {
		require.Equal(t, "cluster", r.Scope)
		require.False(t, r.Manual)
		require.True(t, r.ScheduledAt.Equal(r.ScheduledAt.Truncate(5*time.Second)), "slot %s is not aligned", r.ScheduledAt)
		require.False(t, slots[r.ScheduledAt], "slot %s ran twice", r.ScheduledAt)
		slots[r.ScheduledAt] = true
	}
	calls := append(appA.snapshot(), appB.snapshot()...)
	require.NotEmpty(t, calls)
	for _, c := range calls {
		require.Equal(t, "sync", c.GetJobId())
		require.Equal(t, "cluster", c.GetScope())
		require.EqualValues(t, 1, c.GetAttempt())
		require.NotEmpty(t, c.GetRunId())
	}
}

// A slot a core before 0.1.237 already ran (it schedules jobs itself and
// records the slot) is skipped.
func TestClusterJobSkipsASlotRecordedElsewhere(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	p := e.plugin(t, "demo", "1.0.0", manifest.Job{ID: "sync", Schedule: "@every 1h"})
	app := &fakeApp{}
	n := e.node(t, "node-a", p, app)
	slot := time.Now().UTC().Truncate(time.Hour)
	_, err := e.db.SQL.ExecContext(context.Background(), `INSERT INTO plugin_job_runs (plugin_key, job_id, node_id, scheduled_at, started_at, status, manual)
		VALUES ('demo', 'sync', 'old-core', $1, now(), 'succeeded', false)`, slot)
	require.NoError(t, err)
	_, err = n.bridge.run(context.Background(), "demo", "sync", core.TaskCluster, time.Minute, false,
		core.TaskRun{Name: "plugin.demo.sync", ScheduledAt: slot, Attempt: 1, QueuedAt: time.Now()})
	var skip *core.TaskSkippedError
	require.ErrorAs(t, err, &skip)
	require.Empty(t, app.snapshot())
}

func TestManualRunAndPluginResult(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	p := e.plugin(t, "demo", "1.0.0", manifest.Job{ID: "report", Schedule: "0 3 * * *", TimeoutSec: 60})
	app := &fakeApp{run: func(context.Context, *pluginv1.RunJobRequest) (*pluginv1.RunJobResponse, error) {
		return &pluginv1.RunJobResponse{Message: "3 reports", Count: 3}, nil
	}}
	n := e.node(t, "node-a", p, app)
	require.NoError(t, n.bridge.RunNow(context.Background(), "demo", "report", 7))
	waitFor(t, "the manual run", 10*time.Second, func() bool {
		rows := runRows(t, e.db, "demo", "report")
		return len(rows) == 1 && rows[0].Status == StatusSucceeded
	})
	r := runRows(t, e.db, "demo", "report")[0]
	require.True(t, r.Manual)
	require.Equal(t, "3 reports", r.Message)
	require.True(t, app.snapshot()[0].GetManual())
	waitFor(t, "the task row", 5*time.Second, func() bool {
		var status *string
		var count *int
		require.NoError(t, e.db.SQL.QueryRowContext(context.Background(), `SELECT last_status, last_count FROM scheduled_tasks
			WHERE name = 'plugin.demo.report'`).Scan(&status, &count))
		return status != nil && *status == "succeeded" && count != nil && *count == 3
	})

	requireCode(t, n.bridge.RunNow(context.Background(), "demo", "missing", 7), core.ErrNotFound)
	requireCode(t, n.bridge.RunNow(context.Background(), "nope", "report", 7), core.ErrNotFound)
}

// A node job runs on every node running the plugin, each on its own
// instance; it has no scheduled_tasks row.
func TestNodeJobRunsOnEveryNode(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	p := e.plugin(t, "demo", "1.0.0", manifest.Job{ID: "warm", Schedule: "@every 1h", TimeoutSec: 10, Scope: manifest.JobScopeNode})
	appA, appB := &fakeApp{}, &fakeApp{}
	a := e.node(t, "node-a", p, appA)
	b := e.node(t, "node-b", p, appB)
	require.True(t, registered(a, "plugin.demo.warm"))
	require.True(t, registered(b, "plugin.demo.warm"))
	_, hasRow := readTask(t, e.db, "plugin.demo.warm")
	require.False(t, hasRow)

	// The node tasks need background admission; these services always have it.
	require.NoError(t, a.bridge.RunNow(context.Background(), "demo", "warm", 1))
	waitFor(t, "a run on both nodes", 10*time.Second, func() bool {
		return len(appA.snapshot()) == 1 && len(appB.snapshot()) == 1
	})
	require.Equal(t, "node", appA.snapshot()[0].GetScope())
	waitFor(t, "two run rows", 5*time.Second, func() bool {
		rows := runRows(t, e.db, "demo", "warm")
		return len(rows) == 2 && rows[0].Status == StatusSucceeded && rows[1].Status == StatusSucceeded
	})
	nodes := map[string]bool{}
	for _, r := range runRows(t, e.db, "demo", "warm") {
		require.Equal(t, "node", r.Scope)
		nodes[r.Node] = true
	}
	require.Equal(t, map[string]bool{"node-a": true, "node-b": true}, nodes)
}

// The River leader may not run the plugin: every node registers the cluster
// jobs from the rows, and a node without the plugin puts its run off.
func TestNodeWithoutThePlugin(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	p := e.plugin(t, "demo", "1.0.0", manifest.Job{ID: "sync", Schedule: "@every 1h", TimeoutSec: 30})
	e.node(t, "node-a", p, &fakeApp{})
	b := e.node(t, "node-b", nil, nil)
	require.True(t, registered(b, "plugin.demo.sync"), "registered from the row")

	ctx := context.Background()
	run := core.TaskRun{Name: "plugin.demo.sync", ScheduledAt: time.Now(), Attempt: 1, QueuedAt: time.Now()}
	_, err := b.bridge.run(ctx, "demo", "sync", core.TaskCluster, 30*time.Second, false, run)
	var retry *core.TaskRetryAfterError
	require.ErrorAs(t, err, &retry)

	run.QueuedAt = time.Now().Add(-time.Minute)
	_, err = b.bridge.run(ctx, "demo", "sync", core.TaskCluster, 30*time.Second, false, run)
	require.Error(t, err)
	require.False(t, errors.As(err, &retry))

	_, err = e.db.SQL.ExecContext(ctx, `UPDATE plugins SET status = 'disabled' WHERE key = 'demo'`)
	require.NoError(t, err)
	_, err = b.bridge.run(ctx, "demo", "sync", core.TaskCluster, 30*time.Second, false, run)
	var skip *core.TaskSkippedError
	require.ErrorAs(t, err, &skip)
}

// Disabling the plugin removes its rows and registrations; enabling it again
// brings them back. A new version's jobs replace the old ones.
func TestPluginChangesFollowTheActiveVersion(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	app := &fakeApp{}
	p := e.plugin(t, "demo", "1.0.0", manifest.Job{ID: "sync", Schedule: "@every 1h"}, manifest.Job{ID: "old", Schedule: "@every 1h"})
	n := e.node(t, "node-a", p, app)
	require.True(t, registered(n, "plugin.demo.old"))

	_, err := e.db.SQL.ExecContext(ctx, `UPDATE plugins SET status = 'disabled' WHERE key = 'demo'`)
	require.NoError(t, err)
	require.NoError(t, n.bridge.reconcile(ctx))
	_, ok := readTask(t, e.db, "plugin.demo.sync")
	require.False(t, ok)
	require.False(t, registered(n, "plugin.demo.sync"))

	// Version 2.0.0: "old" is gone, "sync" runs every 30 minutes, and a
	// lagging node still on 1.0.0 does not write its rows back.
	_, err = e.db.SQL.ExecContext(ctx, `UPDATE plugins SET status = 'enabled', active_version = '2.0.0' WHERE key = 'demo'`)
	require.NoError(t, err)
	require.NoError(t, n.bridge.reconcile(ctx))
	_, ok = readTask(t, e.db, "plugin.demo.sync")
	require.False(t, ok, "a node on 1.0.0 must not write the rows of 2.0.0")

	v2 := e.plugin(t, "demo", "2.0.0", manifest.Job{ID: "sync", Schedule: "*/30 * * * *", Title: manifest.LocalizedText{"en": "Sync", "zh": "同步"}})
	n.reg.set(v2, app)
	require.NoError(t, n.bridge.reconcile(ctx))
	row, ok := readTask(t, e.db, "plugin.demo.sync")
	require.True(t, ok)
	require.Equal(t, "*/30 * * * *", row.Schedule)
	require.Equal(t, 5, row.MaxAttempts)
	_, ok = readTask(t, e.db, "plugin.demo.old")
	require.False(t, ok)
	require.False(t, registered(n, "plugin.demo.old"))
	got, _ := n.tasks.Registry().Get("plugin.demo.sync")
	require.Equal(t, core.LocalizedText{"en": "Sync", "zh": "同步"}, got.Title)
}

// Older manifests may declare a timeout or interval the framework does not
// run; they are cut to its limits.
func TestJobLimits(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	p := e.plugin(t, "demo", "1.0.0",
		manifest.Job{ID: "long", Schedule: "0 3 * * *", TimeoutSec: 7200},
		manifest.Job{ID: "fast", Schedule: "@every 1s"},
	)
	n := e.node(t, "node-a", p, &fakeApp{})
	long, _ := readTask(t, e.db, "plugin.demo.long")
	require.EqualValues(t, time.Hour.Milliseconds(), long.TimeoutMS)
	fast, _ := readTask(t, e.db, "plugin.demo.fast")
	require.Equal(t, "@every 5s", fast.Schedule)
	require.EqualValues(t, 60000, fast.TimeoutMS) // timeoutSec 0 takes the default
	got, _ := n.tasks.Registry().Get("plugin.demo.long")
	require.Equal(t, core.LocalizedText{"en": "Demo · long", "zh": "演示 · long"}, got.Title)
}

// A plugin that has not done the work yet asks for a later run: a cluster job
// is put off without counting as a failure.
func TestPluginRetryAfter(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	p := e.plugin(t, "demo", "1.0.0", manifest.Job{ID: "sync", Schedule: "@every 1h"})
	app := &fakeApp{run: func(context.Context, *pluginv1.RunJobRequest) (*pluginv1.RunJobResponse, error) {
		return &pluginv1.RunJobResponse{RetryAfterMs: 2000}, nil
	}}
	n := e.node(t, "node-a", p, app)
	_, err := n.bridge.run(context.Background(), "demo", "sync", core.TaskCluster, time.Minute, false,
		core.TaskRun{Name: "plugin.demo.sync", ScheduledAt: time.Now(), Attempt: 1, Manual: true, QueuedAt: time.Now()})
	var retry *core.TaskRetryAfterError
	require.ErrorAs(t, err, &retry)
	require.Equal(t, 2*time.Second, retry.Delay)
	rows := runRows(t, e.db, "demo", "sync")
	require.Len(t, rows, 1)
	require.Equal(t, StatusSkipped, rows[0].Status)
}

// A run of a plugin error or timeout is recorded with its message.
func TestFailedAndTimedOutRuns(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	p := e.plugin(t, "demo", "1.0.0", manifest.Job{ID: "sync", Schedule: "@every 1h"})
	app := &fakeApp{run: func(ctx context.Context, in *pluginv1.RunJobRequest) (*pluginv1.RunJobResponse, error) {
		if in.GetManual() {
			return nil, errors.New("upstream down")
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	n := e.node(t, "node-a", p, app)
	run := core.TaskRun{Name: "plugin.demo.sync", ScheduledAt: time.Now(), Attempt: 1, Manual: true, QueuedAt: time.Now()}
	_, err := n.bridge.run(context.Background(), "demo", "sync", core.TaskCluster, time.Minute, false, run)
	require.ErrorContains(t, err, "upstream down")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	run.Manual = false
	run.ScheduledAt = time.Now().UTC().Truncate(time.Second)
	_, err = n.bridge.run(ctx, "demo", "sync", core.TaskCluster, time.Minute, false, run)
	require.Error(t, err)
	rows := runRows(t, e.db, "demo", "sync")
	require.Len(t, rows, 2)
	require.Equal(t, StatusFailed, rows[0].Status)
	require.Equal(t, "upstream down", rows[0].Message)
	require.Equal(t, StatusTimeout, rows[1].Status)
}

func TestStopUnregisters(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	p := e.plugin(t, "demo", "1.0.0", manifest.Job{ID: "sync", Schedule: "@every 1h"},
		manifest.Job{ID: "warm", Schedule: "@every 1h", Scope: manifest.JobScopeNode})
	n := e.node(t, "node-a", p, &fakeApp{})
	require.True(t, registered(n, "plugin.demo.sync"))
	require.True(t, registered(n, "plugin.demo.warm"))
	require.NoError(t, n.bridge.Stop(context.Background()))
	require.False(t, registered(n, "plugin.demo.sync"))
	require.False(t, registered(n, "plugin.demo.warm"))
	requireCode(t, n.bridge.RunNow(context.Background(), "demo", "sync", 1), core.ErrUnavailable)
}

// The manifest limits (checked at upload) are the framework's limits.
func TestManifestLimitsMatchTheFramework(t *testing.T) {
	require.Equal(t, task.MaxClusterTimeout, time.Duration(manifest.MaxClusterJobTimeoutSec)*time.Second)
	require.Equal(t, task.MaxNodeTimeout, time.Duration(manifest.MaxNodeJobTimeoutSec)*time.Second)
	require.GreaterOrEqual(t, manifest.MinJobEvery, task.MinClusterEvery)
	require.Greater(t, (&Options{}).withDefaults().StaleAfter, task.MaxClusterTimeout)
}

func TestEffectiveJob(t *testing.T) {
	e, err := EffectiveJob(manifest.Job{ID: "a", Schedule: "@every 1s", TimeoutSec: 7200}, 0)
	require.NoError(t, err)
	require.Equal(t, time.Hour, e.Timeout)
	require.Equal(t, "@every 5s", e.Schedule)
	require.Len(t, e.Adjusted, 2)
	e, err = EffectiveJob(manifest.Job{ID: "a", Schedule: "0 3 * * *"}, 0)
	require.NoError(t, err)
	require.Equal(t, time.Minute, e.Timeout)
	require.Equal(t, "0 3 * * *", e.Schedule)
	require.Empty(t, e.Adjusted)
	e, err = EffectiveJob(manifest.Job{ID: "a", Schedule: "@every 1m", TimeoutSec: 900, Scope: manifest.JobScopeNode}, 0)
	require.NoError(t, err)
	require.Equal(t, 10*time.Minute, e.Timeout)
	require.Equal(t, "@every 1m0s", e.Schedule)
}

// A plugin that did part of the work asks to continue: the run is
// "continued" and the next part runs soon.
func TestPluginContinueAfter(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	p := e.plugin(t, "demo", "1.0.0", manifest.Job{ID: "purge", Schedule: "0 3 * * *"})
	var calls int
	var mu sync.Mutex
	app := &fakeApp{run: func(context.Context, *pluginv1.RunJobRequest) (*pluginv1.RunJobResponse, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return &pluginv1.RunJobResponse{Count: 5000, ContinueAfterMs: 1000}, nil
		}
		return &pluginv1.RunJobResponse{Message: "done"}, nil
	}}
	n := e.node(t, "node-a", p, app)
	require.NoError(t, n.bridge.RunNow(context.Background(), "demo", "purge", 1))
	waitFor(t, "two parts", 15*time.Second, func() bool {
		rows := runRows(t, e.db, "demo", "purge")
		return len(rows) == 2 && rows[1].Status == StatusSucceeded
	})
	rows := runRows(t, e.db, "demo", "purge")
	require.Equal(t, StatusContinued, rows[0].Status)
	require.Equal(t, "continues in 1s", rows[0].Message)
	require.True(t, rows[0].Manual)
	require.False(t, rows[1].Manual)
}
