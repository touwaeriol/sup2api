// Package cluster implements the multi-node primitives of the core: node
// registry and heartbeats, distributed locks, pub/sub bus and concurrency
// slots. Everything is backed by a single Redis-protocol server, Valkey by
// default (multi-key Lua scripts assume a non-cluster deployment);
// PostgreSQL is only pinged for health.
package cluster

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// Redis keys (docs/CONTRACTS.md §7).
const (
	keyNodeLive       = "node:live"
	keyNodeInfoPrefix = "node:info:"
	keyNodePlugins    = "node:plugins:"
	keySlotPrefix     = "slot:"
	keyLockPrefix     = "lock:"
)

// Default timings (docs/ARCHITECTURE.md §2.3).
const (
	DefaultHeartbeatInterval = 5 * time.Second
	DefaultNodeTTL           = 15 * time.Second
	DefaultReclaimInterval   = 30 * time.Second
	DefaultSlotTTL           = 5 * time.Minute
)

// OpenRedis parses a Redis-protocol URL (ParseRedisURL) and pings the server.
func OpenRedis(ctx context.Context, url string) (*redis.Client, error) {
	opt, err := ParseRedisURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	c := redis.NewClient(opt)
	if err := c.Ping(ctx).Err(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return c, nil
}

// ParseRedisURL parses the cache server URL. The cache is any server speaking
// the Redis protocol - Valkey by default, Redis 7+ works the same - so besides
// go-redis's redis://, rediss:// and unix:// it accepts valkey:// and
// valkeys:// (TLS), which mean exactly redis:// and rediss://.
func ParseRedisURL(raw string) (*redis.Options, error) {
	raw = strings.TrimSpace(raw)
	if scheme, rest, ok := strings.Cut(raw, "://"); ok {
		switch strings.ToLower(scheme) {
		case "valkey":
			raw = "redis://" + rest
		case "valkeys":
			raw = "rediss://" + rest
		}
	}
	return redis.ParseURL(raw)
}

// Options configures a Cluster.
type Options struct {
	Managed     bool
	CoreBootID  string
	NodeID      string // config.NodeID
	Addr        string // advertised address, informational (e.g. public URL)
	HostVersion string
	Logger      *slog.Logger
	// PluginSandbox is what the plugin sandbox enforces on this node.
	PluginSandbox *core.PluginSandboxStatus
}

// Cluster bundles the four cluster services that share one Redis client.
type Cluster struct {
	Registry *Registry
	Locker   *Locker
	Bus      *Bus
	Slots    *Slots

	cancel context.CancelFunc
	done   chan struct{}
}

var (
	_ core.NodeRegistry  = (*Registry)(nil)
	_ core.Locker        = (*Locker)(nil)
	_ core.Bus           = (*Bus)(nil)
	_ core.BusReconnects = (*Bus)(nil)
	_ core.Slots         = (*Slots)(nil)
)

// New builds all cluster services. db may be nil (no PG health tracking).
// Call Start before use and Close on exit.
func New(rdb redis.UniversalClient, db *store.DB, opts Options) *Cluster {
	var pinger Pinger
	if db != nil {
		pinger = db
	}
	reg := NewRegistry(rdb, pinger, RegistryOptions{
		NodeID: opts.NodeID, Addr: opts.Addr, HostVersion: opts.HostVersion, Logger: opts.Logger, Managed: opts.Managed, CoreBootID: opts.CoreBootID,
		PluginSandbox: opts.PluginSandbox,
	})
	if opts.Managed {
		reg.opts.BootID = opts.CoreBootID
	}
	return &Cluster{
		Registry: reg,
		Locker:   NewLocker(rdb, opts.Logger),
		Bus:      NewBus(rdb, opts.Logger),
		Slots:    NewSlots(rdb, reg, SlotOptions{Logger: opts.Logger}),
	}
}

// Start sends the first heartbeat synchronously (so this node is alive
// before it takes any slot), reclaims slots of dead nodes and starts the
// background loops.
func (c *Cluster) Start(ctx context.Context) error {
	if err := c.Registry.Heartbeat(ctx); err != nil {
		return fmt.Errorf("first heartbeat: %w", err)
	}
	if _, err := c.Slots.Reclaim(ctx); err != nil {
		c.Registry.log.Warn("initial slot reclaim failed", "err", err)
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	c.cancel = cancel
	c.done = make(chan struct{})
	go func() {
		defer close(c.done)
		done := make(chan struct{}, 3)
		go func() { c.Registry.Run(runCtx); done <- struct{}{} }()
		go func() { c.Slots.Run(runCtx); done <- struct{}{} }()
		go func() { c.Bus.Run(runCtx); done <- struct{}{} }()
		for range 3 {
			<-done
		}
	}()
	return nil
}

// Close stops the loops and removes this node from node:live.
func (c *Cluster) Close() {
	if c.cancel != nil {
		c.cancel()
		<-c.done
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c.Registry.Leave(ctx)
}

func orDefault(l *slog.Logger) *slog.Logger {
	if l == nil {
		return slog.Default()
	}
	return l
}
