// Package rollout implements the two-phase (prepare, then activate) plugin
// rollout across all live nodes, and the per-node reconciler that starts,
// switches and drains plugin instances according to the desired state in
// PostgreSQL.
package rollout

import (
	"context"
	"io/fs"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/grpcruntime"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry"
)

// Instance is one running plugin version as seen by the reconciler.
type Instance interface {
	registry.Extension
	// State returns (state, lastError); see grpcruntime.State* constants.
	State() (string, string)
	Restarts() int
	// Refresh re-reads settings/grants and reconfigures on change.
	Refresh(ctx context.Context) error
	// MigrateData runs the plugin data migration (no-op when unsupported).
	MigrateData(ctx context.Context, from, to string) error
	Drain(timeout time.Duration)
	Stop()
}

// Runtime starts plugin instances of the package's manifest runtime: a
// plugin process for "grpc"; a JS runtime later.
type Runtime interface {
	Load(ctx context.Context, pkg *registry.Package) (Instance, error)
}

// Schemas is the subset of dbschema.Manager used by rollouts.
type Schemas interface {
	Migrate(ctx context.Context, pluginKey string, fsys fs.FS) ([]string, error)
	Pending(ctx context.Context, pluginKey string, ids []string) (bool, error)
}

// FromGRPC adapts a grpcruntime.Runtime to Runtime.
func FromGRPC(rt *grpcruntime.Runtime) Runtime { return grpcAdapter{rt} }

type grpcAdapter struct{ rt *grpcruntime.Runtime }

// Load starts the package's plugin process. The explicit nil keeps a failed
// load from becoming a non-nil Instance holding a nil pointer.
func (a grpcAdapter) Load(ctx context.Context, pkg *registry.Package) (Instance, error) {
	inst, err := a.rt.Load(ctx, pkg)
	if err != nil {
		return nil, err
	}
	return inst, nil
}

// Instance states reported by runtimes (mirrors grpcruntime).
const (
	stateReady    = grpcruntime.StateReady
	stateDraining = grpcruntime.StateDraining
	stateFailed   = grpcruntime.StateFailed
	stateStopped  = grpcruntime.StateStopped
)

// Rollout phases (plugin_rollouts.phase).
const (
	PhasePreparing  = "preparing"
	PhaseActivating = "activating"
	PhaseActive     = "active"
	PhaseFailed     = "failed"
	PhaseCancelled  = "cancelled"
)

// Node rollout states (reported per plugin by every node).
const (
	NodePending = "pending"
	NodeReady   = "ready"
	NodeActive  = "active"
	NodeFailed  = "failed"
)

// Message is published on core.ChannelPluginEvents.
type Message struct {
	Type      string `json:"type"` // rollout | config | resources
	PluginKey string `json:"plugin_key"`
	RolloutID int64  `json:"rollout_id,omitempty"`
}

// MessageResources asks every node to restart the plugin's instances with
// the resource limits now stored in plugins.resource_limits.
const MessageResources = "resources"

// limitsAware is implemented by instances that can tell whether their
// process runs with outdated resource limits (grpcruntime.Instance).
type limitsAware interface {
	LimitsStale() bool
}

// broadcastReceiver is implemented by instances that accept cluster
// broadcasts (grpcruntime.Instance).
type broadcastReceiver interface {
	HandlesBroadcast() bool
	OnBroadcast(ctx context.Context, msg grpcruntime.BroadcastMessage) error
}
