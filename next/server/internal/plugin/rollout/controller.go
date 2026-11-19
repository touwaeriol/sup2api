package rollout

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/grants"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// Options wires the controller. DB, Node, Packages, Registry and Runtime are
// required; Bus, Schemas, Defaults, Perms and Events are optional.
type Options struct {
	Mutations core.PluginMutationGate
	// CanCoordinate gates global rollout ownership independently from local
	// convergence, which candidates need before receiving traffic.
	CanCoordinate func() bool
	DB            *store.DB
	Node          core.NodeRegistry
	Bus           core.Bus
	Packages      *registry.Packages
	Registry      *registry.Registry
	Runtime       Runtime
	Schemas       Schemas
	Defaults      core.PluginDefaultsApplier
	Perms         core.PermissionCatalog
	Events        core.EventPublisher
	Logger        *slog.Logger

	// Timing knobs; zero values use the production defaults.
	ReconcileInterval time.Duration // 5s
	LeaseTTL          time.Duration // 15s
	LeaseRenew        time.Duration // 5s
	PrepareTimeout    time.Duration // 120s
	ActivateTimeout   time.Duration // 60s
	CoordinatorTick   time.Duration // 1s
	DrainTimeout      time.Duration // 30s
	LoadRetry         time.Duration // 30s: retry a failed instance load
}

// Controller implements core.RolloutController and runs the node reconciler.
type Controller struct {
	o   Options
	log *slog.Logger

	kickCh chan struct{}
	wakeMu sync.Mutex
	wakers map[int64]chan struct{} // coordinator wake-ups by rollout id

	recMu          sync.Mutex              // one reconcile pass at a time
	history        map[string]historyState // guarded by recMu; updated only after persistence
	historyCleaned time.Time
	mu             sync.Mutex
	slots          map[string]*slot
	sig            string // signature of the last published generation
	// referenced: versions per plugin named by active/desired or an open
	// rollout (from/target) in the last reconcile; their packages are kept.
	referenced map[string]map[string]bool
	restarting map[string]bool // key -> resource restart running
	again      map[string]bool // key -> restart requested while running
	stopped    bool

	drainMu  sync.Mutex     // leaf lock (may be taken under mu or slot.mu)
	draining map[string]int // key@version -> instances still draining

	hub *broadcastHub

	coordMu      sync.Mutex
	coordinating map[int64]bool
	// handedOff lists rollouts this boot gave up coordinating because its own
	// new instance failed; it does not take them over again.
	handedOff map[int64]bool

	stopOnce sync.Once
	ctx      context.Context // cancelled by Stop
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	unsubs   []func()
}

var _ core.RolloutController = (*Controller)(nil)

func New(o Options) (*Controller, error) {
	if o.DB == nil || o.Node == nil || o.Packages == nil || o.Registry == nil || o.Runtime == nil {
		return nil, errors.New("rollout: DB, Node, Packages, Registry and Runtime are required")
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	def(&o.ReconcileInterval, 5*time.Second)
	def(&o.LeaseTTL, 15*time.Second)
	def(&o.LeaseRenew, 5*time.Second)
	def(&o.PrepareTimeout, 120*time.Second)
	def(&o.ActivateTimeout, 60*time.Second)
	def(&o.CoordinatorTick, time.Second)
	def(&o.DrainTimeout, 30*time.Second)
	def(&o.LoadRetry, 30*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	c := &Controller{
		o:            o,
		log:          o.Logger.With("component", "plugin-rollout", "boot_id", o.Node.BootID()),
		kickCh:       make(chan struct{}, 1),
		wakers:       map[int64]chan struct{}{},
		slots:        map[string]*slot{},
		referenced:   map[string]map[string]bool{},
		draining:     map[string]int{},
		restarting:   map[string]bool{},
		again:        map[string]bool{},
		coordinating: map[int64]bool{},
		handedOff:    map[int64]bool{},
		ctx:          ctx,
		cancel:       cancel,
	}
	c.hub = newBroadcastHub(o.Bus, o.Node.BootID(), c.servingInstance, c.log)
	return c, nil
}

// Start runs the reconciler (every ReconcileInterval and on broadcasts)
// until Stop. It removes orphaned package directories and performs one
// synchronous reconcile first so plugins are serving when Start returns.
func (c *Controller) Start(context.Context) {
	ctx := c.ctx
	if c.o.Bus != nil {
		c.unsubs = append(c.unsubs, c.o.Bus.Subscribe(core.ChannelPluginEvents, c.onPluginEvent),
			c.o.Bus.Subscribe(core.ChannelConfigChanged, c.onConfigChanged))
	}
	c.pruneOrphans(ctx)
	c.Reconcile(ctx)
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		t := time.NewTicker(c.o.ReconcileInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			case <-c.kickCh:
			}
			c.Reconcile(ctx)
		}
	}()
}

// Stop ends the loops, drains every local instance and publishes an empty
// generation. Coordinated rollouts are left to be taken over by other nodes.
func (c *Controller) Stop(ctx context.Context) {
	c.stopOnce.Do(func() {
		for _, unsub := range c.unsubs {
			unsub()
		}
		c.hub.close()
		c.mu.Lock()
		c.stopped = true
		c.mu.Unlock()
		c.cancel()
		c.wg.Wait()
		c.recMu.Lock()
		defer c.recMu.Unlock()
		c.o.Registry.Publish(nil)
		c.mu.Lock()
		var all []Instance
		for _, s := range c.slots {
			for _, e := range s.entries {
				if e.inst != nil {
					all = append(all, e.inst)
				}
			}
		}
		c.slots = map[string]*slot{}
		c.mu.Unlock()
		var wg sync.WaitGroup
		for _, inst := range all {
			wg.Add(1)
			go func(inst Instance) {
				defer wg.Done()
				inst.Drain(c.o.DrainTimeout)
			}(inst)
		}
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
			// Persist stop acknowledgement before Redis liveness disappears.
			// Uninstall can then distinguish a clean shutdown from a partition.
			g, boot := store.G(ctx, c.o.DB.Gorm), c.o.Node.BootID()
			_ = stopBootRuntimes(g, boot).Error
			_ = cleanBootCleanup(g, boot).Error
			_ = ackBootUninstalls(g, boot).Error
		case <-ctx.Done():
		}
	})
}

// stopBootRuntimes marks every plugin runtime of this boot stopped.
func stopBootRuntimes(g *gorm.DB, bootID string) *gorm.DB {
	return g.Model(&model.PluginRuntimeNode{}).Where("boot_id = ?", bootID).
		Updates(map[string]any{"stopped": true, "updated_at": sqlNow})
}

// cleanBootCleanup acknowledges every pending rollout cleanup of this boot:
// nothing of it runs any more.
func cleanBootCleanup(g *gorm.DB, bootID string) *gorm.DB {
	return g.Model(&model.PluginRolloutCleanup{}).Where("boot_id = ? AND state = 'cleanup_pending'", bootID).
		Updates(map[string]any{"state": "cleaned", "updated_at": sqlNow})
}

// ackBootUninstalls adds this boot to the stopped boots of every uninstall
// that waits for it.
func ackBootUninstalls(g *gorm.DB, bootID string) *gorm.DB {
	return g.Model(&model.PluginUninstall{}).Where("? = ANY(target_boot_ids) AND NOT (? = ANY(stopped_boot_ids))", bootID, bootID).
		Update("stopped_boot_ids", gorm.Expr("array_append(stopped_boot_ids, ?)", bootID))
}

// onPluginEvent handles core.ChannelPluginEvents: "resources" restarts the
// plugin's local instances, anything else triggers a reconcile.
func (c *Controller) onPluginEvent(payload []byte) {
	var m Message
	_ = json.Unmarshal(payload, &m)
	if m.Type == MessageResources && m.PluginKey != "" {
		c.requestRestart(m.PluginKey)
		return
	}
	c.kick()
	c.wakeCoordinators()
}

func (c *Controller) kick() {
	select {
	case c.kickCh <- struct{}{}:
	default:
	}
}

func (c *Controller) wakeCoordinators() {
	c.wakeMu.Lock()
	defer c.wakeMu.Unlock()
	for _, ch := range c.wakers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (c *Controller) publish(ctx context.Context, key string, rolloutID int64) {
	if c.o.Bus == nil {
		return
	}
	b, _ := json.Marshal(Message{Type: "rollout", PluginKey: key, RolloutID: rolloutID})
	if err := c.o.Bus.Publish(ctx, core.ChannelPluginEvents, b); err != nil {
		c.log.Warn("publish plugin event failed", "err", err)
	}
}

// ------------------------------------------------------------------ core.RolloutController

var (
	errNoPlugin = core.ErrNotFound.WithMessage("plugin not found")
	errOpen     = core.ErrConflict.WithMessage("a rollout is already in progress for this plugin").
			WithDetails(map[string]any{"reason": "rollout_in_progress"})
)

type pluginLock struct {
	status     string
	active     *string
	rowVersion int64
}

func lockPlugin(ctx context.Context, tx *gorm.DB, key string) (*pluginLock, error) {
	if err := store.RequireTx(tx); err != nil {
		return nil, err
	}
	g := store.G(ctx, tx)
	var p model.Plugin
	err := selectPluginForUpdate(g, key, "status", "active_version", "row_version").Take(&p).Error
	if store.IsNoRows(err) {
		return nil, errNoPlugin
	}
	if err != nil {
		return nil, err
	}
	uninstalling, err := gormpg.Exists(g, uninstallPending(g, key))
	if err != nil {
		return nil, err
	}
	if uninstalling {
		return nil, core.ErrConflict.WithMessage("plugin uninstall is waiting for nodes to stop")
	}
	open, err := gormpg.Exists(g, openRollouts(g, key).Select("1"))
	if err != nil {
		return nil, err
	}
	if open {
		return nil, errOpen
	}
	return &pluginLock{status: p.Status, active: p.ActiveVersion, rowVersion: p.RowVersion}, nil
}

// selectPluginForUpdate selects the columns cols of the plugin row and locks
// it.
func selectPluginForUpdate(g *gorm.DB, key string, cols ...string) *gorm.DB {
	return g.Model(&model.Plugin{}).Select(cols).Where("key = ?", key).
		Clauses(store.ForUpdate())
}

func approved(ctx context.Context, tx *gorm.DB, key, version string) (bool, error) {
	g := store.G(ctx, tx)
	return gormpg.Exists(g, g.Model(&model.PluginVersion{}).Select("1").
		Where("plugin_key = ? AND version = ? AND consent_status = 'approved'", key, version))
}

func actor(id int64) *int64 {
	if id <= 0 {
		return nil
	}
	return &id
}

// newRollout is a new plugin_rollouts row, its lease from the database's
// clock; the other columns are their defaults, and the id is read back.
type newRollout struct {
	ID                    int64           `gorm:"column:id;->"`
	PluginKey             string          `gorm:"column:plugin_key"`
	Action                string          `gorm:"column:action"`
	FromVersion           *string         `gorm:"column:from_version"`
	TargetVersion         *string         `gorm:"column:target_version"`
	Phase                 string          `gorm:"column:phase"`
	CoordinatorNodeID     string          `gorm:"column:coordinator_node_id"`
	CoordinatorBootID     string          `gorm:"column:coordinator_boot_id"`
	CoordinatorLeaseUntil store.ExprValue `gorm:"column:coordinator_lease_until"`
	CreatedBy             *int64          `gorm:"column:created_by"`
}

func (newRollout) TableName() string { return model.PluginRollout{}.TableName() }

func (c *Controller) insertRollout(ctx context.Context, tx *gorm.DB, key, action string, from, target *string, phase string, actorID int64) (int64, error) {
	g := store.G(ctx, tx)
	row := &newRollout{PluginKey: key, Action: action, FromVersion: from, TargetVersion: target, Phase: phase,
		CoordinatorNodeID: c.o.Node.NodeID(), CoordinatorBootID: c.o.Node.BootID(),
		CoordinatorLeaseUntil: store.ExprValue{Expr: gorm.Expr(leaseFromNow, c.o.LeaseTTL.Milliseconds())}, CreatedBy: actor(actorID)}
	err := g.Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(row).Error
	id := row.ID
	if store.IsUniqueViolation(err, "") {
		return 0, errOpen
	}
	if err == nil && audit.Source(ctx) == "builtin" {
		err = audit.Audit(ctx, tx, actorID, "plugin."+action, "plugin", key, map[string]any{"source": "builtin", "from": from, "version": target, "rollout_id": id})
		if err == nil {
			err = markBuiltinHistory(g, id).Error
		}
	}
	return id, err
}

// markBuiltinHistory tags the "created" history row of a rollout that a
// builtin plugin source started.
func markBuiltinHistory(g *gorm.DB, rolloutID int64) *gorm.DB {
	return g.Model(&model.PluginHistory{}).Where("rollout_id = ? AND node_id = '' AND state = 'created'", rolloutID).
		Update("message", gorm.Expr(`(message::jsonb || '{"source":"builtin"}'::jsonb)::text`))
}

// Enable starts a rollout of the plugin's active (or newest approved) version.
func (c *Controller) Enable(ctx context.Context, pluginKey string, actorID int64) (*core.Rollout, error) {
	ctx, release, guardErr := core.BeginPluginMutation(ctx, c.o.Mutations)
	if guardErr != nil {
		return nil, guardErr
	}
	defer release()

	var id int64
	err := c.o.DB.Tx(ctx, func(tx *gorm.DB) error {
		p, err := lockPlugin(ctx, tx, pluginKey)
		if err != nil {
			return err
		}
		switch p.status {
		case "installed", "disabled":
		case "enabled":
			return core.ErrConflict.WithMessage("plugin is already enabled")
		default:
			return core.ErrConflict.WithMessage("plugin cannot be enabled in status " + p.status)
		}
		var target string
		if p.active != nil {
			if ok, err := approved(ctx, tx, pluginKey, *p.active); err != nil {
				return err
			} else if ok {
				target = *p.active
			}
		}
		if target == "" {
			err := store.G(ctx, tx).Model(&model.PluginVersion{}).Select("version").
				Where("plugin_key = ? AND consent_status = 'approved'", pluginKey).Order("uploaded_at DESC").Take(&target).Error
			if store.IsNoRows(err) {
				return core.ErrConflict.WithMessage("plugin has no approved version")
			}
			if err != nil {
				return err
			}
		}
		if id, err = c.insertRollout(ctx, tx, pluginKey, "enable", nil, &target, PhasePreparing, actorID); err != nil {
			return err
		}
		return updatePlugin(store.G(ctx, tx), pluginKey, map[string]any{
			"status": "enabling", "status_reason": "", "desired_version": target,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return c.started(ctx, pluginKey, id)
}

// Upgrade starts a rollout from the active version to version.
func (c *Controller) Upgrade(ctx context.Context, pluginKey, version string, actorID int64) (*core.Rollout, error) {
	ctx, release, guardErr := core.BeginPluginMutation(ctx, c.o.Mutations)
	if guardErr != nil {
		return nil, guardErr
	}
	defer release()

	var id int64
	err := c.o.DB.Tx(ctx, func(tx *gorm.DB) error {
		p, err := lockPlugin(ctx, tx, pluginKey)
		if err != nil {
			return err
		}
		if p.status != "enabled" || p.active == nil {
			return core.ErrConflict.WithMessage("only an enabled plugin can be upgraded; enable it instead")
		}
		if *p.active == version {
			return core.ErrConflict.WithMessage("version is already active")
		}
		ok, err := approved(ctx, tx, pluginKey, version)
		if err != nil {
			return err
		}
		if !ok {
			return core.ErrConflict.WithMessage("version is not installed or not approved")
		}
		if id, err = c.insertRollout(ctx, tx, pluginKey, "upgrade", p.active, &version, PhasePreparing, actorID); err != nil {
			return err
		}
		return updatePlugin(store.G(ctx, tx), pluginKey, map[string]any{"status": "upgrading", "desired_version": version}).Error
	})
	if err != nil {
		return nil, err
	}
	return c.started(ctx, pluginKey, id)
}

// Disable publishes "no version". Nothing needs preparing, so the rollout is
// committed immediately: when Disable returns, plugins.status is "disabled"
// and every node stops serving within one reconcile; the rollout row
// completes once all nodes report it.
func (c *Controller) Disable(ctx context.Context, pluginKey string, actorID int64, reason string) (*core.Rollout, error) {
	if !core.IsEmergencyRevocation(ctx) {
		work, release, err := core.BeginPluginMutation(ctx, c.o.Mutations)
		if err != nil {
			return nil, err
		}
		defer release()
		ctx = work
	}

	var id int64
	err := c.o.DB.Tx(ctx, func(tx *gorm.DB) error {
		p, err := lockPlugin(ctx, tx, pluginKey)
		if err != nil {
			return err
		}
		if p.status != "enabled" {
			return core.ErrConflict.WithMessage("plugin is not enabled")
		}
		if id, err = c.insertRollout(ctx, tx, pluginKey, "disable", p.active, nil, PhaseActivating, actorID); err != nil {
			return err
		}
		if err := updatePlugin(store.G(ctx, tx), pluginKey, map[string]any{
			"status": "disabled", "status_reason": reason, "desired_version": nil,
		}).Error; err != nil {
			return err
		}
		if c.o.Perms != nil {
			if err := c.o.Perms.SetPluginActive(ctx, tx, pluginKey, false); err != nil {
				return err
			}
		}
		if c.o.Events != nil {
			ver := ""
			if p.active != nil {
				ver = *p.active
			}
			return c.o.Events.Emit(ctx, tx, core.Event{Type: core.EventPluginDisabled,
				Payload: map[string]any{"plugin_key": pluginKey, "version": ver}})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return c.started(ctx, pluginKey, id)
}

func (c *Controller) started(ctx context.Context, key string, id int64) (*core.Rollout, error) {
	c.startCoordinator(id)
	c.publish(ctx, key, id)
	c.kick()
	return c.get(ctx, key, id)
}

// Current returns the open rollout of a plugin with live node states.
func (c *Controller) Current(ctx context.Context, pluginKey string) (*core.Rollout, error) {
	var id int64
	err := openRollouts(store.G(ctx, c.o.DB.Gorm), pluginKey).Select("id").Take(&id).Error
	if store.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return c.get(ctx, pluginKey, id)
}

func (c *Controller) get(ctx context.Context, key string, id int64) (*core.Rollout, error) {
	r, err := c.loadRollout(ctx, id)
	if err != nil {
		return nil, err
	}
	if r == nil || r.key != key {
		return nil, core.ErrNotFound.WithMessage("rollout not found")
	}
	out := &core.Rollout{
		ID: r.id, PluginKey: r.key, Action: r.action, FromVersion: deref(r.from), TargetVersion: deref(r.target),
		Phase: r.phase, Coordinator: r.coordNode, Error: r.err, Nodes: []core.RolloutNodeState{},
	}
	if r.phase == PhasePreparing || r.phase == PhaseActivating {
		states, _, _ := c.nodeStates(ctx, r)
		for _, n := range states {
			out.Nodes = append(out.Nodes, n.RolloutNodeState)
		}
	} else if err := store.G(ctx, c.o.DB.Gorm).Model(&model.PluginRolloutNode{}).Select("node_id, boot_id, state, error").
		Where("rollout_id = ?", id).Order("node_id, boot_id").Scan(&out.Nodes).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// Cancel aborts a rollout that has not reached its commit point.
func (c *Controller) Cancel(ctx context.Context, pluginKey string, rolloutID, actorID int64) error {
	r, err := c.loadRollout(ctx, rolloutID)
	if err != nil {
		return err
	}
	if r == nil || r.key != pluginKey {
		return core.ErrNotFound.WithMessage("rollout not found")
	}
	if r.phase != PhasePreparing {
		return core.ErrConflict.WithMessage("only a preparing rollout can be cancelled")
	}
	ok, err := c.finishFailed(ctx, r, PhaseCancelled, fmt.Sprintf("cancelled by user %d", actorID), false)
	if err != nil {
		return err
	}
	if !ok {
		return core.ErrConflict.WithMessage("only a preparing rollout can be cancelled")
	}
	c.publish(ctx, pluginKey, rolloutID)
	c.kick()
	return nil
}

// ------------------------------------------------------------------ rollout rows

type rolloutRow struct {
	id          int64
	key         string
	action      string
	from        *string
	target      *string
	phase       string
	coordNode   string
	coordBoot   string
	leaseExpire bool
	err         string
	rowVersion  int64
	ageSec      float64 // since created_at (DB clock)
	phaseAgeSec float64 // since updated_at (last phase change)
}

func (c *Controller) loadRollout(ctx context.Context, id int64) (*rolloutRow, error) {
	var rec rolloutRecord
	err := selectRollouts(store.G(ctx, c.o.DB.Gorm)).Where("id = ?", id).Take(&rec).Error
	if store.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return rec.row(), nil
}

// finishFailed moves a preparing rollout to failed/cancelled and reverts the
// plugin status. The phase check alone guards against the commit point (the
// commit CAS fails once the phase changed). Returns false when the rollout
// is no longer preparing.
func (c *Controller) finishFailed(ctx context.Context, r *rolloutRow, phase, msg string, fenced bool) (bool, error) {
	ok := false
	err := c.o.DB.Tx(ctx, func(tx *gorm.DB) error {
		g := store.G(ctx, tx)
		res := failRollout(g, r, phase, msg, fenced, c.o.Node.BootID())
		if res.Error != nil || res.RowsAffected == 0 {
			return res.Error
		}
		ok = true
		var err error
		if r.action == "upgrade" {
			// The permission catalog is left alone: keys the target version
			// registered at consent stay until another version is activated
			// (commit syncs the catalog with the manifest which then runs).
			// Nothing of the running version checks them, and role grants
			// chosen at consent survive the new consent a retry needs (grants.Withdraw).
			err = updatePlugin(g, r.key, map[string]any{"status": "enabled", "desired_version": gorm.Expr("active_version")}).Error
		} else {
			err = updatePlugin(g, r.key, map[string]any{
				"status":          gorm.Expr("CASE WHEN active_version IS NULL THEN 'installed' ELSE 'disabled' END"),
				"desired_version": nil, "status_reason": msg,
			}).Error
		}
		if err != nil {
			return err
		}
		// What was approved for the target stays with it only until it is
		// activated: an abandoned upgrade takes it back.
		if err := grants.Withdraw(ctx, tx, r.key, deref(r.target), phase); err != nil {
			return err
		}
		if err := c.writeNodes(ctx, tx, r); err != nil {
			return err
		}
		return c.markCleanup(ctx, tx, r)
	})
	return ok, err
}

// failRollout moves a preparing rollout to phase. fenced also requires the
// coordinator's view of it to be current: the row version it read and a
// lease this boot still holds.
func failRollout(g *gorm.DB, r *rolloutRow, phase, msg string, fenced bool, bootID string) *gorm.DB {
	return g.Model(&model.PluginRollout{}).
		Where(`id = ? AND phase = 'preparing'
			AND (NOT ?::boolean OR (row_version = ? AND coordinator_boot_id = ?
				AND coordinator_lease_until > clock_timestamp()))`, r.id, fenced, r.rowVersion, bootID).
		Updates(map[string]any{"phase": phase, "error": msg, "updated_at": sqlNow, "row_version": bumpRowVersion})
}

// Mark every boot which could own an instance, including disconnected boots.
// Liveness expiry is not evidence that an old version has stopped. One
// statement: a cleanup row for each boot running the plugin, pending again
// if it was there.
func (c *Controller) markCleanup(ctx context.Context, tx *gorm.DB, r *rolloutRow) error {
	g := store.G(ctx, tx)
	return gormpg.InsertSelect(g, model.PluginRolloutCleanup{}.TableName(), []string{"rollout_id", "boot_id", "state"},
		g.Model(&model.PluginRuntimeNode{}).Select("?::bigint, boot_id, 'cleanup_pending'", r.id).Where("plugin_key = ? AND NOT stopped", r.key),
		clause.OnConflict{Columns: []clause.Column{{Name: "rollout_id"}, {Name: "boot_id"}},
			DoUpdates: clause.Set{{Column: clause.Column{Name: "state"}, Value: "cleanup_pending"},
				{Column: clause.Column{Name: "updated_at"}, Value: gorm.Expr("now()")}}}).Error
}

// writeNodes records the per-node outcome from the live node reports.
func (c *Controller) writeNodes(ctx context.Context, tx *gorm.DB, r *rolloutRow) error {
	states, _, err := c.nodeStates(ctx, r)
	if err != nil {
		c.log.Warn("read node states failed", "err", err)
		return nil
	}
	g := store.G(ctx, tx)
	for _, n := range states {
		st := n.State
		if st == NodePending {
			st = NodeFailed
			if n.Error == "" {
				n.Error = "not ready"
			}
		}
		if err := upsertRolloutNode(g, r.id, n.NodeID, n.BootID, st, n.Error, n.code).Error; err != nil {
			return err
		}
	}
	return nil
}

// upsertRolloutNode records one node's outcome.
func upsertRolloutNode(g *gorm.DB, rolloutID int64, nodeID, bootID, state, msg, code string) *gorm.DB {
	return g.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "rollout_id"}, {Name: "boot_id"}},
		DoUpdates: append(clause.AssignmentColumns([]string{"state", "error", "code"}),
			clause.Assignment{Column: clause.Column{Name: "updated_at"}, Value: gorm.Expr("now()")}),
	}).Create(&rolloutNode{RolloutID: rolloutID, NodeID: nodeID, BootID: bootID, State: state, Error: msg, Code: code})
}

// rolloutNode is a plugin_rollout_nodes row, every column written as given;
// the timestamps are their defaults, and nothing is read back.
type rolloutNode struct {
	RolloutID int64  `gorm:"column:rollout_id"`
	NodeID    string `gorm:"column:node_id"`
	BootID    string `gorm:"column:boot_id"`
	State     string `gorm:"column:state"`
	Error     string `gorm:"column:error"`
	Code      string `gorm:"column:code"`
}

func (rolloutNode) TableName() string { return model.PluginRolloutNode{}.TableName() }

// nodeState is one live node's report for a rollout, with the code of its
// failed load of the target version (core.PluginInstanceState.Code), which the node's
// outcome row keeps once the rollout ends and the instance is gone.
type nodeState struct {
	core.RolloutNodeState
	code string
}

// nodeStates reads every live node's report for the rollout. selfSeen
// tells whether this node is among the live nodes (guards against an empty
// node list when Redis is unreachable).
func (c *Controller) nodeStates(ctx context.Context, r *rolloutRow) ([]nodeState, bool, error) {
	nodes, err := c.o.Node.LiveNodes(ctx)
	if err != nil {
		return nil, false, err
	}
	selfSeen := false
	out := make([]nodeState, 0, len(nodes))
	for _, n := range nodes {
		if n.BootID == c.o.Node.BootID() {
			selfSeen = true
		}
		ns := nodeState{RolloutNodeState: core.RolloutNodeState{NodeID: n.NodeID, BootID: n.BootID, State: NodePending}}
		if st, ok := core.ParsePluginNodeState(n.Plugins[r.key]); ok && st.RolloutID == r.id && st.Rollout != "" {
			ns.State = st.Rollout
			ns.Error = st.Error
			ns.code = st.InstanceCode(deref(r.target))
		}
		out = append(out, ns)
	}
	return out, selfSeen, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
