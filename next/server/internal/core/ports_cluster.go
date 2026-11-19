package core

import (
	"context"
	"encoding/json"
	"time"
)

// ============================================================ cluster (owner: D sandbox-network)

// Node identifies this process in the cluster.
type Node interface {
	NodeID() string // stable: NODE_ID env or hostname
	BootID() string // random per process start
}

// NodeStatus is one live node as seen in Redis.
type NodeStatus struct {
	NodeID        string            `json:"node_id"`
	BootID        string            `json:"boot_id"`
	Addr          string            `json:"addr"`
	HostVersion   string            `json:"host_version"`
	StartedAt     time.Time         `json:"started_at"`
	LastHeartbeat time.Time         `json:"last_heartbeat"`
	Plugins       map[string]string `json:"plugins"` // plugin key -> JSON PluginNodeState
	// PluginSandbox is what the node's plugin sandbox enforces (nil: a core
	// that does not report it).
	PluginSandbox *PluginSandboxStatus `json:"plugin_sandbox,omitempty"`
}

// NodeRegistry manages heartbeats and per-node plugin state.
type NodeRegistry interface {
	Node
	LiveNodes(ctx context.Context) ([]NodeStatus, error)
	IsAlive(ctx context.Context, bootID string) (bool, error)
	// ReportPlugin publishes this node's actual state for one plugin
	// (value is a JSON document owned by the plugin runtime).
	ReportPlugin(ctx context.Context, pluginKey string, stateJSON string) error
	// Healthy reports whether this node talked to Redis and PG within the
	// self-fencing window (15 s).
	Healthy() bool
}

// Locker provides short distributed locks on Redis (redsync: SET NX PX with
// an owner token). There is no fallback: a node that cannot reach Redis
// takes no locks.
//
// The locks are NOT fenced. A holder that stalls past Until cannot tell
// that another node has taken the lock since, and keeps going. So a lock
// only keeps nodes from doing the same work at the same time; work that must
// never run twice needs its own guard in the database as well - a
// conditional UPDATE, a unique index, an idempotency key.
type Locker interface {
	// TryLock makes one attempt to take lock:{key} for ttl, without waiting.
	// ok=false with a nil error means another process holds it. The Lock is
	// never nil; when ok is false its methods are no-ops.
	TryLock(ctx context.Context, key string, ttl time.Duration) (lock Lock, ok bool, err error)
}

// TokenLocker is a Locker whose locks can be resumed by token, so a holder in
// another process (a plugin, over HostService) can extend and release them.
type TokenLocker interface {
	Locker
	// TryLockToken is TryLock with the owner token chosen by the caller
	// instead of generated, so a caller that never learns the outcome (a lost
	// reply) can still release the lock with ReleaseToken. token must be
	// non-empty and fresh for every attempt: an attempt with the token of a
	// lock already held with it fails, and the cleanup of the failed attempt
	// (a compare-and-delete with the same token) deletes that lock - the call
	// reports ok=false and nobody holds the lock afterwards.
	TryLockToken(ctx context.Context, key, token string, ttl time.Duration) (lock Lock, ok bool, err error)
	// Resume returns a handle on lock:{key} as held with token. It does not
	// talk to Redis: Extend and Release on the handle only act if the lock
	// is still held with that token.
	Resume(key, token string, ttl time.Duration) Lock
	// ReleaseToken deletes lock:{key} if it is held with token, within ctx.
	// Unlike Lock.Release it reports failure: nil means the lock is not held
	// with token any more (released now, or before, or never taken); an error
	// means the outcome is unknown.
	ReleaseToken(ctx context.Context, key, token string) error
}

// Lock is one lock taken by TryLock.
type Lock interface {
	// Token is the owner token stored in Redis ("" for a lock not taken).
	Token() string
	// Until is when this process stops considering itself the holder: its
	// local clock, minus a drift margin. Zero when not held, and for a
	// resumed lock until its first successful Extend.
	Until() time.Time
	// Extend pushes the expiry to ttl from now. false with a nil error means
	// the lock is no longer held with this token - it expired, and another
	// process may have it - so the guarded work must stop. An error means
	// the outcome is unknown (Redis did not answer).
	Extend(ctx context.Context) (bool, error)
	// Release gives the lock up if it is still held with this token. It is
	// idempotent and never deletes a lock another process holds.
	Release()
}

// Bus is Redis pub/sub for cache invalidation and coordination. Messages are
// best-effort: every consumer must also reconcile periodically.
type Bus interface {
	Publish(ctx context.Context, channel string, payload []byte) error
	Subscribe(channel string, handler func(payload []byte)) (cancel func())
}

// Broadcast channels.
const (
	ChannelPluginEvents = "plugin:events"
	// ChannelAuthzChanged carries what an authz version changed (JSON:
	// the version, the policy version of a policy write, the users of a
	// user write; authz.Service).
	ChannelAuthzChanged   = "authz:changed"
	ChannelAccountChanged = "account:changed"
	ChannelConfigChanged  = "config:changed"
	// ChannelUserSessionsChanged carries a user id whose sessions were ended
	// (token_version moved): nodes drop that user's cached token status.
	ChannelUserSessionsChanged = "iam:user:changed"
)

// PublishAccountChanged announces on account:changed that account id
// changed ({"account_id": id}); a nil bus announces nothing. A message
// without an account id (a bulk change) means every account.
func PublishAccountChanged(ctx context.Context, bus Bus, id int64) error {
	if bus == nil {
		return nil
	}
	payload, _ := json.Marshal(map[string]int64{"account_id": id})
	return bus.Publish(ctx, ChannelAccountChanged, payload)
}

// AccountChangeQuota is the kind of an account:changed message that only
// announces a written quota snapshot: subscribers drop what they keep of the
// account's quota and nothing else (MODEL-AVAILABILITY-PRECHECK-DESIGN §7).
// Older nodes ignore the kind and drop everything, which is only slower.
const AccountChangeQuota = "quota"

// PublishAccountQuotaChanged announces on account:changed that the quota
// snapshot of account id was written ({"account_id": id, "kind": "quota"});
// a nil bus announces nothing.
func PublishAccountQuotaChanged(ctx context.Context, bus Bus, id int64) error {
	if bus == nil {
		return nil
	}
	payload, _ := json.Marshal(struct {
		AccountID int64  `json:"account_id"`
		Kind      string `json:"kind"`
	}{id, AccountChangeQuota})
	return bus.Publish(ctx, ChannelAccountChanged, payload)
}

// ParseAccountChanged reads an account:changed payload: the account id (0
// when absent or unreadable, meaning every account) and the kind ("" for a
// change of the account itself).
func ParseAccountChanged(payload []byte) (id int64, kind string) {
	var m struct {
		AccountID int64  `json:"account_id"`
		Kind      string `json:"kind"`
	}
	if json.Unmarshal(payload, &m) != nil || m.AccountID < 0 {
		return 0, ""
	}
	return m.AccountID, m.Kind
}

// BusReconnects is implemented by a Bus that can tell when its subscription
// was re-established after a lost connection: messages published in between
// are lost, so caches kept up to date by the bus must be dropped.
type BusReconnects interface {
	// OnReconnect registers fn, called on the bus's receive goroutine after
	// every re-subscription; it must not block.
	OnReconnect(fn func()) (cancel func())
}

// Slots is the distributed concurrency limiter. Members are prefixed with
// the boot id so slots of dead nodes can be reclaimed safely.
type Slots interface {
	// Acquire takes one slot of kind ("account"|"apikey"|"ws") for id if fewer than
	// limit are held (limit <= 0 = unlimited). release is idempotent.
	Acquire(ctx context.Context, kind string, id int64, limit int, requestID string) (release func(), ok bool, err error)
	InUse(ctx context.Context, kind string, id int64) (int, error)
	InUseMany(ctx context.Context, kind string, ids []int64) (map[int64]int, error)
}

// LeasedSlots additionally returns a context that ends when the slot is
// released. Callers must use it for all work admitted by the slot, and release
// it before another attempt. A Redis failure must not silently admit uncounted
// work, but a slot lost later never stops the work it admitted: the slot is
// counted again (docs/GATEWAY-CONCURRENCY-DESIGN.md §4).
type LeasedSlots interface {
	Slots
	AcquireLease(ctx context.Context, kind string, id int64, limit int, requestID string) (context.Context, func(), bool, error)
}

// AcquireSlot keeps simple test limiters compatible while production uses
// leases. The returned context always covers the admitted operation.
func AcquireSlot(ctx context.Context, slots Slots, kind string, id int64, limit int, requestID string) (context.Context, func(), bool, error) {
	if leased, ok := slots.(LeasedSlots); ok {
		return leased.AcquireLease(ctx, kind, id, limit, requestID)
	}
	release, ok, err := slots.Acquire(ctx, kind, id, limit, requestID)
	return ctx, release, ok, err
}
