package rollout

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/grpcruntime"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// slot is the local state of one plugin on this node.
type slot struct {
	key     string
	mu      sync.Mutex
	entries map[string]*entry // version -> entry
	serving string            // version that should be in the generation
}

type entry struct {
	version string
	inst    Instance // nil when loading failed
	epoch   int64    // plugins.row_version when (re)created
	loadErr string
	// loadCode classifies loadErr for the console (core.PluginInstanceState.Code).
	loadCode string
	failAt   time.Time
	// Last failed attempt to replace inst with new resource limits.
	restartErr    string
	restartFailAt time.Time
}

type pluginRow struct {
	key          string
	status       string
	active       *string
	desired      *string
	rowVersion   int64
	uninstalling bool
	ro           *rolloutRow
}

func (c *Controller) slotFor(key string) *slot {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.slots[key]
	if !ok {
		s = &slot{key: key, entries: map[string]*entry{}}
		c.slots[key] = s
	}
	return s
}

// localInstance returns the local instance of key@version, if running.
func (c *Controller) localInstance(key, version string) Instance {
	c.mu.Lock()
	s := c.slots[key]
	c.mu.Unlock()
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.entries[version]; e != nil {
		return e.inst
	}
	return nil
}

// pluginRecord is the scan target of loadPlugins' plugins statement.
type pluginRecord struct {
	Key            string  `gorm:"column:key"`
	Status         string  `gorm:"column:status"`
	ActiveVersion  *string `gorm:"column:active_version"`
	DesiredVersion *string `gorm:"column:desired_version"`
	RowVersion     int64   `gorm:"column:row_version"`
	Uninstalling   bool    `gorm:"column:uninstalling"`
}

// selectPlugins selects every plugin with its uninstall marker, by key.
func selectPlugins(g *gorm.DB) *gorm.DB {
	return g.Model(&model.Plugin{}).Select(`key, status, active_version, desired_version, row_version,
		EXISTS (SELECT 1 FROM plugin_uninstalls u WHERE u.plugin_key = plugins.key) AS uninstalling`).Order("key")
}

func (c *Controller) loadPlugins(ctx context.Context) ([]*pluginRow, error) {
	g := store.G(ctx, c.o.DB.Gorm)
	var recs []pluginRecord
	if err := selectPlugins(g).Scan(&recs).Error; err != nil {
		return nil, err
	}
	var out []*pluginRow
	byKey := map[string]*pluginRow{}
	for _, r := range recs {
		p := &pluginRow{key: r.Key, status: r.Status, active: r.ActiveVersion, desired: r.DesiredVersion,
			rowVersion: r.RowVersion, uninstalling: r.Uninstalling}
		out = append(out, p)
		byKey[p.key] = p
	}
	var open []rolloutRecord
	if err := selectRollouts(g).Where("phase IN ('preparing','activating')").Scan(&open).Error; err != nil {
		return nil, err
	}
	for i := range open {
		if p := byKey[open[i].PluginKey]; p != nil {
			p.ro = open[i].row()
		}
	}
	return out, nil
}

// Reconcile runs one pass: take over orphaned rollouts, bring local
// instances in line with the desired state, publish the generation and
// report per-plugin node state.
func (c *Controller) Reconcile(ctx context.Context) {
	c.recMu.Lock()
	defer c.recMu.Unlock()
	if ctx.Err() != nil {
		return
	}
	rows, err := c.loadPlugins(ctx)
	if err != nil {
		c.log.Warn("reconcile: load plugins failed", "err", err)
		return
	}
	c.setReferenced(rows)
	for _, p := range rows {
		if p.ro != nil {
			c.takeOver(ctx, p.ro)
		}
	}
	seen := map[string]bool{}
	var wg sync.WaitGroup
	reports := make([]core.PluginNodeState, len(rows))
	for idx, p := range rows {
		seen[p.key] = true
		s := c.slotFor(p.key)
		wg.Add(1)
		go func(idx int, p *pluginRow) {
			defer wg.Done()
			reports[idx] = c.reconcileKey(ctx, s, p)
		}(idx, p)
	}
	wg.Wait()

	// Plugins removed from the database (uninstalled).
	c.mu.Lock()
	var gone []*slot
	for key, s := range c.slots {
		if !seen[key] {
			gone = append(gone, s)
			delete(c.slots, key)
		}
	}
	c.mu.Unlock()
	for _, s := range gone {
		s.mu.Lock()
		for v, e := range s.entries {
			if e.inst != nil {
				c.drainAsync(s.key, v, e.inst, false)
			}
		}
		s.entries = map[string]*entry{}
		s.mu.Unlock()
		_ = c.o.Node.ReportPlugin(ctx, s.key, "{}")
		c.recordNodeHistory(ctx, s.key, core.PluginNodeState{State: "uninstalled"})
	}

	c.republish()
	c.syncBroadcast()
	c.sweepPackages()

	for idx, p := range rows {
		c.acknowledgeCleanup(ctx, p)
		g, boot := store.G(ctx, c.o.DB.Gorm), c.o.Node.BootID()
		if reports[idx].State == "stopped" {
			_ = stopRuntime(g, p.key, boot).Error
		}
		if p.uninstalling && reports[idx].State == "stopped" {
			if err := ackUninstallStop(g, p.key, boot, p.rowVersion).Error; err != nil {
				c.log.Warn("acknowledge plugin stop failed", "plugin", p.key, "err", err)
			}
		}
		b, _ := json.Marshal(reports[idx])
		c.recordNodeHistory(ctx, p.key, reports[idx])
		if err := c.o.Node.ReportPlugin(ctx, p.key, string(b)); err != nil {
			c.log.Warn("report plugin state failed", "plugin", p.key, "err", err)
		}
	}
	c.cleanHistory(ctx)
}

// stopRuntime marks the plugin's runtime on this boot stopped.
func stopRuntime(g *gorm.DB, key, bootID string) *gorm.DB {
	return g.Model(&model.PluginRuntimeNode{}).Where("plugin_key = ? AND boot_id = ?", key, bootID).
		Updates(map[string]any{"stopped": true, "updated_at": sqlNow})
}

// ackUninstallStop adds this boot to the stopped boots of an uninstall that
// the plugin generation epoch already covers.
func ackUninstallStop(g *gorm.DB, key, bootID string, epoch int64) *gorm.DB {
	return g.Model(&model.PluginUninstall{}).
		Where("plugin_key = ? AND epoch <= ? AND NOT (? = ANY(stopped_boot_ids))", key, epoch, bootID).
		Update("stopped_boot_ids", gorm.Expr("array_append(stopped_boot_ids, ?)", bootID))
}

// desiredVersions is the rollout's answer to "which versions should this node
// run": the one it serves, the candidate that may start next to it, and the
// old one that must stay until the rollout activates.
func desiredVersions(p *pluginRow) (serving, standby, keep string) {
	ro := p.ro
	switch {
	case ro != nil && ro.phase == PhasePreparing:
		serving = deref(ro.from)
		if t := deref(ro.target); t != "" && t != serving {
			standby = t
		}
	case ro != nil && ro.phase == PhaseActivating:
		serving = deref(ro.target)
		keep = deref(ro.from) // old process stays until the rollout is active
	default:
		if (p.status == "enabled" || p.status == "upgrading") && p.active != nil {
			serving = *p.active
		}
	}
	return serving, standby, keep
}

func (c *Controller) reconcileKey(ctx context.Context, s *slot, p *pluginRow) core.PluginNodeState {
	ro := p.ro
	serving, standby, keep := desiredVersions(p)

	// A standby only starts once the target's SQL migrations are applied.
	standbyBlocked := ""
	if standby != "" {
		if pending, err := c.migrationsPending(ctx, p.key, standby); err != nil {
			standbyBlocked = err.Error()
		} else if pending {
			standbyBlocked = "waiting for migrations"
		}
	}

	needed := map[string]bool{}
	for _, v := range []string{serving, keep} {
		if v != "" {
			needed[v] = true
		}
	}
	if standby != "" && standbyBlocked == "" {
		needed[standby] = true
	}
	var wg sync.WaitGroup
	for v := range needed {
		wg.Add(1)
		go func(v string) {
			defer wg.Done()
			c.ensure(ctx, s, v, p.rowVersion)
		}(v)
	}
	wg.Wait()

	s.mu.Lock()
	// Each node switches on its own: while the version it should serve is
	// still starting or failed here, a version it already runs keeps serving,
	// and the wanted one is retried. No node waits for another.
	want, fallback := serving, ""
	if serving != "" && (ro == nil || ro.phase != PhasePreparing) && !entryReady(s.entries[serving]) {
		for v, e := range s.entries {
			if v != serving && entryReady(e) && (fallback == "" || v == keep) {
				fallback = v
			}
		}
		if fallback != "" {
			needed[fallback] = true
			serving = fallback
		}
	}
	for v, e := range s.entries {
		if !needed[v] {
			delete(s.entries, v)
			if e.inst != nil {
				c.drainAsync(s.key, v, e.inst, false)
			}
		}
	}
	s.serving = serving
	st := core.PluginNodeState{Serving: serving, Standby: standby, Fallback: fallback}
	var running []Instance
	var retryOK []bool
	st.Instances, running, retryOK = collectInstances(s, c.o.LoadRetry)
	if ro != nil {
		st.RolloutID = ro.id
		s.rolloutNodeState(ro, &st, want, standby, standbyBlocked)
	}
	switch {
	case st.Rollout != "":
		st.State = st.Rollout
	case serving == "":
		st.State = "stopped"
	default:
		state, msg := s.entryState(want)
		if state == NodeReady {
			state = NodeActive
		}
		st.State = state
		if st.Error == "" {
			st.Error = msg
		}
	}
	s.mu.Unlock()

	stale := false
	for idx, inst := range running {
		if err := inst.Refresh(ctx); err != nil {
			c.log.Debug("plugin refresh failed", "plugin", p.key, "err", err)
		}
		// Resource limits changed but the "resources" broadcast was missed
		// (or an earlier restart failed): replace the instance now.
		if la, ok := inst.(limitsAware); ok && retryOK[idx] && la.LimitsStale() {
			if state, _ := inst.State(); state == stateReady {
				stale = true
			}
		}
	}
	if stale {
		c.requestRestart(p.key)
	}
	// A generation switch alone does not prove the old processes have exited.
	// A purge acknowledgement is sent only after all asynchronous drains end.
	if st.State == "stopped" {
		c.mu.Lock()
		if c.restarting[p.key] {
			st.State = "draining"
		}
		c.mu.Unlock()
		c.drainMu.Lock()
		for id, count := range c.draining {
			if count > 0 && strings.HasPrefix(id, p.key+"@") {
				st.State = "draining"
				break
			}
		}
		c.drainMu.Unlock()
	}
	return st
}

// collectInstances returns the per-version instance states of a slot (sorted
// by version), the running instances and, per running instance, whether a
// resource restart may be tried now.
func collectInstances(s *slot, loadRetry time.Duration) (instances []core.PluginInstanceState, running []Instance, retryOK []bool) {
	versions := make([]string, 0, len(s.entries))
	for v := range s.entries {
		versions = append(versions, v)
	}
	sort.Strings(versions)
	for _, v := range versions {
		e := s.entries[v]
		is := core.PluginInstanceState{Version: v}
		if e.inst != nil {
			is.State, is.Error = e.inst.State()
			is.Restarts = e.inst.Restarts()
			if is.Error == "" && e.restartErr != "" {
				is.Error = e.restartErr
			}
			running = append(running, e.inst)
			retryOK = append(retryOK, e.restartErr == "" || time.Since(e.restartFailAt) >= loadRetry)
		} else {
			is.State, is.Error, is.Code = stateFailed, e.loadErr, e.loadCode
		}
		instances = append(instances, is)
	}
	return instances, running, retryOK
}

// entryState maps the local entry of one version to the node state.
func (s *slot) entryState(v string) (string, string) {
	e := s.entries[v]
	if e == nil {
		return NodePending, ""
	}
	if e.inst == nil {
		return NodeFailed, e.loadErr
	}
	switch state, msg := e.inst.State(); state {
	case stateReady, stateDraining:
		return NodeReady, ""
	case stateFailed, stateStopped:
		return NodeFailed, msg
	default:
		return NodePending, msg
	}
}

// rolloutNodeState fills st.Rollout and st.Error for an open rollout. A node
// reports itself ready for the preparing phase once the standby runs (or
// there is none), and active only when it serves the target version.
func (s *slot) rolloutNodeState(ro *rolloutRow, st *core.PluginNodeState, want, standby, standbyBlocked string) {
	switch ro.phase {
	case PhasePreparing:
		switch {
		case standby == "":
			st.Rollout = NodeReady
		case standbyBlocked != "":
			st.Rollout, st.Error = NodePending, standbyBlocked
		default:
			st.Rollout, st.Error = s.entryState(standby)
		}
	case PhaseActivating:
		if want == "" {
			st.Rollout = NodeActive
		} else {
			state, msg := s.entryState(want)
			if state == NodeReady {
				state = NodeActive
			}
			st.Rollout, st.Error = state, msg
		}
	}
}

func (c *Controller) migrationsPending(ctx context.Context, key, version string) (bool, error) {
	pkg, err := c.o.Packages.Open(ctx, key, version)
	if err != nil {
		return false, err
	}
	if pkg.Manifest.Database == nil || c.o.Schemas == nil {
		return false, nil
	}
	ids, err := pkg.MigrationIDs()
	if err != nil {
		return false, err
	}
	return c.o.Schemas.Pending(ctx, key, ids)
}

// ensure starts key@version unless it is running (or failed recently in the
// same desired-state epoch).
func (c *Controller) ensure(ctx context.Context, s *slot, version string, epoch int64) {
	s.mu.Lock()
	e := s.entries[version]
	if e != nil {
		if e.inst != nil {
			state, _ := e.inst.State()
			if (state != stateFailed && state != stateStopped) || e.epoch == epoch {
				s.mu.Unlock()
				return
			}
			// Failed instance and the desired state changed: replace it.
			old := e.inst
			delete(s.entries, version)
			c.drainAsync(s.key, version, old, true)
		} else if e.epoch == epoch && time.Since(e.failAt) < c.o.LoadRetry {
			s.mu.Unlock()
			return
		}
	}
	s.mu.Unlock()

	pkg, done, err := c.o.Packages.Acquire(ctx, s.key, version)
	if err == nil {
		defer done() // until the entry below keeps the package
	}
	var inst Instance
	if err == nil {
		err = c.registerRuntime(ctx, s.key, pVersionEpoch{version, epoch})
	}
	if errors.Is(err, errStaleGeneration) {
		// Pending, not failed: a rollout must not fail on an outdated read.
		c.log.Debug("plugin instance start deferred to the next pass", "plugin", s.key, "version", version)
		return
	}
	if err == nil {
		inst, err = c.o.Runtime.Load(ctx, pkg)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		c.log.Error("plugin instance failed to start", "plugin", s.key, "version", version, "err", err)
		s.entries[version] = &entry{version: version, epoch: epoch, loadErr: truncate(err.Error(), 500), loadCode: loadCode(err), failAt: time.Now()}
		return
	}
	c.log.Info("plugin instance started", "plugin", s.key, "version", version)
	s.entries[version] = &entry{version: version, inst: inst, epoch: epoch}
}

// registerRuntime shares the plugin row lock with uninstall's durable marker.
// It prevents a stale reconcile snapshot from starting a process after a purge
// barrier, and remembers disconnected instances beyond Redis's liveness TTL.
type pVersionEpoch struct {
	version string
	epoch   int64
}

// errStaleGeneration means the reconcile pass read an older plugins row than
// the one now committed, for instance the plugin row before and the new
// rollout after an Enable. It says nothing about the instance: ensure records
// no failure and the next pass starts it with the current generation.
var errStaleGeneration = core.ErrConflict.WithMessage("plugin generation changed before instance start")

func (c *Controller) registerRuntime(ctx context.Context, key string, expected ...pVersionEpoch) error {
	return c.o.DB.Tx(ctx, func(tx *gorm.DB) error {
		g := store.G(ctx, tx)
		var p model.Plugin
		if err := selectEpochForShare(g, key).Take(&p).Error; err != nil {
			return err
		}
		if len(expected) > 0 && p.RowVersion != expected[0].epoch {
			return errStaleGeneration
		}
		pending, err := gormpg.Exists(g, uninstallPending(g, key))
		if err != nil {
			return err
		}
		if pending {
			return core.ErrConflict.WithMessage("plugin is uninstalling")
		}
		return upsertRuntimeNode(g, key, c.o.Node.BootID()).Error
	})
}

// selectEpochForShare reads the plugin's row_version under a share lock, so
// no rollout or uninstall moves it until the registration commits.
func selectEpochForShare(g *gorm.DB, key string) *gorm.DB {
	return g.Model(&model.Plugin{}).Select("row_version").Where("key = ?", key).
		Clauses(store.Lock(clause.LockingStrengthShare))
}

// upsertRuntimeNode records that this boot runs the plugin; the upsert
// turns a stopped row back on.
func upsertRuntimeNode(g *gorm.DB, key, bootID string) *gorm.DB {
	return g.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "plugin_key"}, {Name: "boot_id"}},
		DoUpdates: clause.Set{{Column: clause.Column{Name: "stopped"}, Value: false},
			{Column: clause.Column{Name: "updated_at"}, Value: gorm.Expr("now()")}},
	}).Create(&runningBoot{PluginKey: key, BootID: bootID})
}

// runningBoot is a new plugin_runtime_nodes row; the other columns are
// their defaults, and nothing is read back.
type runningBoot struct {
	PluginKey string `gorm:"column:plugin_key"`
	BootID    string `gorm:"column:boot_id"`
}

func (runningBoot) TableName() string { return model.PluginRuntimeNode{}.TableName() }

// Only a reconciliation of the current PG generation may acknowledge cleanup.
// Its removed instances must have completed Drain; stale passes cannot clear a
// barrier written by a concurrently failed or committed rollout.
func (c *Controller) acknowledgeCleanup(ctx context.Context, p *pluginRow) {
	if p.ro != nil {
		return
	}
	c.mu.Lock()
	restarting := c.restarting[p.key]
	c.mu.Unlock()
	if restarting {
		return
	}
	c.drainMu.Lock()
	defer c.drainMu.Unlock()
	for id, n := range c.draining {
		if n > 0 && strings.HasPrefix(id, p.key+"@") {
			return
		}
	}
	if err := ackCleanup(store.G(ctx, c.o.DB.Gorm), p.key, c.o.Node.BootID(), p.rowVersion).Error; err != nil {
		c.log.Warn("acknowledge rollout cleanup failed", "plugin", p.key, "err", err)
	}
}

// ackCleanup marks this boot's cleanup of the plugin's finished rollouts
// done, while the plugin is still at generation epoch.
func ackCleanup(g *gorm.DB, key, bootID string, epoch int64) *gorm.DB {
	finished := g.Table("plugin_rollouts AS r").Select("r.id").Joins("JOIN plugins p ON p.key = r.plugin_key").
		Where("p.key = ? AND p.row_version = ? AND r.phase NOT IN ('preparing', 'activating')", key, epoch)
	return g.Model(&model.PluginRolloutCleanup{}).
		Where("rollout_id IN (?) AND boot_id = ? AND state = 'cleanup_pending'", finished, bootID).
		Updates(map[string]any{"state": "cleaned", "updated_at": gorm.Expr("now()")})
}

// republish switches the registry when the serving set (instances
// included: a resource restart replaces the instance of the same version)
// or grants changed.
func (c *Controller) republish() {
	c.mu.Lock()
	keys := make([]string, 0, len(c.slots))
	for k := range c.slots {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var exts []Instance
	var sig strings.Builder
	for _, k := range keys {
		s := c.slots[k]
		s.mu.Lock()
		if e := s.entries[s.serving]; s.serving != "" && e != nil && e.inst != nil {
			exts = append(exts, e.inst)
			g, _ := json.Marshal(e.inst.Grants())
			fmt.Fprintf(&sig, "%s@%s#%s#%p#%s\n", k, s.serving, e.inst.Package().SHA256, e.inst, g)
		}
		s.mu.Unlock()
	}
	changed := sig.String() != c.sig
	if changed {
		c.sig = sig.String()
	}
	c.mu.Unlock()
	if !changed {
		return
	}
	list := make([]registry.Extension, 0, len(exts))
	for _, e := range exts {
		list = append(list, e)
	}
	g := c.o.Registry.Publish(list)
	c.log.Info("plugin generation published", "generation", g.Number(), "plugins", len(list))
}

func truncate(s string, n int) string { return x.Truncate(s, n) }

// loadCode is the code of a failed load the console acts on:
// check.CodeOutdatedContract for the runtime's refusal of a package built
// for an older plugin contract, however the error was wrapped; "" else.
func loadCode(err error) string {
	if errors.Is(err, grpcruntime.ErrOutdatedContract) {
		return check.CodeOutdatedContract
	}
	return ""
}

// entryReady reports a running instance that can serve.
func entryReady(e *entry) bool {
	if e == nil || e.inst == nil {
		return false
	}
	state, _ := e.inst.State()
	return state == stateReady
}
