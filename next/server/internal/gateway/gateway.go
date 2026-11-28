// Package gateway is the client-facing API gateway: platform endpoints
// (built-in and plugin-declared), the proxy pipeline (auth, hooks, billing gate, scheduling,
// forwarding, usage extraction), sticky sessions and the /sticky-rules
// console API. See ARCHITECTURE chapter 6 and CONTRACTS §5.6/§5.9.
package gateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"log/slog"
	"math/rand/v2"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/config"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/gateway/convert"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// Deps are the gateway's collaborators. DB and Redis may be nil in unit
// tests: sticky sessions and hook breaker/stats
// then degrade to no-ops.
type Deps struct {
	DB    *store.DB
	Redis redis.UniversalClient
	Bus   core.Bus
	Node  core.Node
	// Events records settings.changed when a settings section of the
	// gateway is saved (CONTRACTS §107); nil records nothing.
	Events core.EventPublisher
	// Environment supplies the global layer of the environment variables
	// merged over the process layer (GlobalEnvironment, CONTRACTS §74.3);
	// nil drops both and leaves only the group and account layers.
	Environment core.EnvironmentResolver
	Registry    core.PluginRegistry
	Auth        core.APIKeyAuthenticator
	Pricer      core.Pricer
	Balance     core.BalanceGate
	Slots       core.Slots
	Accounts    core.AccountDirectory
	// NetworkAccess is the administrator's plugin network access (CONTRACTS
	// §113), which a relay's host must pass as well (allowHost); nil = no
	// such setting (tests).
	NetworkAccess core.PluginNetworkAccess
	Settler       core.Settler
	Observations  core.ObservationPublisher
	Tasks         core.AsyncTasks
	// Limiter enforces per-account rpm/tpm limits (CONTRACTS §18);
	// nil = no limits.
	Limiter core.AccountLimiter
	// KeyWindows counts and caps the API keys' requests and tokens per
	// minute (CONTRACTS §60); nil = not counted.
	KeyWindows core.RateWindows
	// KeyUsage answers GET /v1/usage (CONTRACTS §62); nil = 404.
	KeyUsage core.KeyUsageReporter
	// Models lists the models of an API key's group for GET /v1/models and
	// /v1beta/models (models_list.go); nil leaves those paths unanswered.
	Models core.GroupModelLister
	// Quota receives the subscription quota headers of upstream responses
	// for account types declaring manifest quota.headers (CONTRACTS §44);
	// nil = not recorded.
	Quota core.QuotaObserver
	// Recovery renews the credentials of an account whose upstream answered
	// 401, for one more try on it (CONTRACTS §141.2); nil = no recovery.
	Recovery core.CredentialRecovery
	Config   *config.Config
	// Converters are the core's protocol converters (ARCHITECTURE 6.6);
	// nil means convert.Default(). Hand the same registry (or the Gateway,
	// see Converters/Conversion) to the account module as
	// core.ProtocolConverters.
	Converters *convert.Registry
	// Health reports whether this node may serve (self-fencing, CONTRACTS
	// §14.3); false answers every gateway endpoint with 503 unavailable.
	// Optional: when nil and Node implements Healthy() bool (as
	// core.NodeRegistry does), Node.Healthy is used; otherwise the node is
	// always considered healthy.
	Health func() bool
	// Draining reports that the node is draining (CONTRACTS §35): WebSocket
	// sessions end their turn in progress and close with 1012 so clients
	// reconnect elsewhere. http.Server.Shutdown does not track upgraded
	// connections, so the sessions watch this themselves. nil = never.
	Draining func() bool
}

// Gateway serves plugin-declared endpoints and owns sticky sessions.
type Gateway struct {
	d Deps

	conv *convert.Registry

	table atomic.Pointer[routeTable]

	settings *settingsCache
	gwStore  gatewaySettingsStore
	rules    *ruleCache
	hooks    *hookRuntime

	allowPrivate bool
	health       func() bool
	// fingerprintKey keys RequestMeta.session_fingerprint: derived from the
	// master key, so fingerprints differ between installations and cannot be
	// recomputed from a guessed session value.
	fingerprintKey []byte

	// Overridable in tests.
	now        func() time.Time
	lookupIP   func(ctx context.Context, host string) ([]net.IP, error)
	headerWait func(stream bool) time.Duration
	// randFloat drives the weighted order inside a priority (CONTRACTS §18).
	randFloat func() float64
	// ws holds the WebSocket session limits (websocket.go).
	ws wsLimits
	// renewInterval replaces core.ExecutionRenewInterval in tests (waited.go).
	renewInterval time.Duration

	// Model availability precheck budgets (availability.go); zero = the
	// package constants. Overridable in tests.
	availabilitySnapshotTimeout      time.Duration
	availabilityPluginTimeout        time.Duration
	availabilityGuardSnapshotTimeout time.Duration
	// availabilityBreakers stops asking a plugin whose availability calls
	// fail; availabilityWarns throttles the precheck's logs.
	availabilityBreakers availabilityBreakers
	availabilityWarns    warnSampler

	stop chan struct{}
	// readsAbandoned ends when AbandonReads is called (disconnect.go).
	readsAbandoned context.Context
	abandonReads   context.CancelFunc
	wg             sync.WaitGroup
	cancels        []func()
	closed         sync.Once
}

var (
	_ core.HookStatsSource    = (*Gateway)(nil)
	_ core.ProtocolConverters = (*Gateway)(nil)
)

// Converters returns the gateway's converter registry.
func (g *Gateway) Converters() *convert.Registry { return g.conv }

// Conversion implements core.ProtocolConverters (for the account module).
func (g *Gateway) Conversion(clientProtocol, upstreamProtocol string) (core.Conversion, bool) {
	return g.conv.Conversion(clientProtocol, upstreamProtocol)
}

// New builds the gateway, subscribes to registry and bus notifications and
// starts its background loop (hook stats flush).
// Call Close on shutdown.
func New(d Deps) *Gateway {
	g := &Gateway{
		d:          d,
		now:        time.Now,
		lookupIP:   defaultLookupIP,
		headerWait: defaultHeaderWait,
		randFloat:  rand.Float64,
		ws:         defaultWSLimits(),
		stop:       make(chan struct{}),
	}
	g.readsAbandoned, g.abandonReads = context.WithCancel(context.Background())
	g.conv = d.Converters
	if g.conv == nil {
		g.conv = convert.Default()
	}
	if d.Config != nil {
		g.allowPrivate = d.Config.AllowPrivateUpstream
		mac := hmac.New(sha256.New, d.Config.MasterKey)
		mac.Write([]byte("session-fingerprint"))
		g.fingerprintKey = mac.Sum(nil)
	}
	g.health = d.Health
	if g.health == nil {
		if h, ok := d.Node.(interface{ Healthy() bool }); ok {
			g.health = h.Healthy
		}
	}
	g.settings = newSettingsCache(d.DB)
	g.gwStore = dbGatewaySettings{db: d.DB, events: d.Events}
	g.rules = newRuleCache(d.DB)
	g.hooks = newHookRuntime(d.Redis)

	if d.DB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := g.SyncBuiltinDefaults(ctx); err != nil {
			slog.Warn("gateway: sync built-in sticky rules", "err", err)
		}
		cancel()
	}

	var gen core.Generation
	if d.Registry != nil {
		gen = d.Registry.Current()
		g.cancels = append(g.cancels, d.Registry.OnChange(func(gen core.Generation) {
			g.table.Store(buildRouteTable(gen))
		}))
	}
	g.table.Store(buildRouteTable(gen))

	if d.Bus != nil {
		g.cancels = append(g.cancels,
			d.Bus.Subscribe(core.ChannelConfigChanged, func(payload []byte) { g.onConfigChanged(payload) }),
		)
	}
	g.wg.Add(1)
	go g.statsLoop()
	return g
}

// Close stops background work and subscriptions and flushes hook stats.
func (g *Gateway) Close() {
	g.closed.Do(func() {
		g.abandonReads()
		for _, c := range g.cancels {
			if c != nil {
				c()
			}
		}
		close(g.stop)
		g.wg.Wait()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		g.hooks.flush(ctx)
	})
}

// Middleware dispatches requests matching an endpoint of a built-in platform
// or of a platform declared by an enabled plugin to the pipeline. Anything
// else (including endpoints of disabled or uninstalled plugins) falls
// through to the next handler, i.e. 404. An openai endpoint is also served
// without its /v1 prefix (POST /responses), the request rewritten to the
// /v1 path. Mount it with engine.Use before the core routes, or in
// engine.NoRoute.
func (g *Gateway) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		method, path := c.Request.Method, c.Request.URL.Path
		if g.serveKeyUsage(c) {
			c.Abort()
			return
		}
		t := g.table.Load()
		rt, params := t.match(method, path)
		if rt == nil {
			var full string
			if rt, params, full = t.matchUnversioned(method, path); rt != nil {
				c.Request.URL.Path = full
			}
		}
		if rt != nil {
			g.serve(c, t.gen, rt.binding, params)
			c.Abort()
			return
		}
		// After the platform endpoints: the model list never takes a
		// request one of them serves.
		if g.serveModels(c) {
			c.Abort()
			return
		}
		c.Next()
	}
}

func (g *Gateway) onConfigChanged(payload []byte) {
	var msg struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(payload, &msg)
	// Cheap to reload; settings, rules and the gateway section are tiny.
	g.settings.invalidate()
	g.rules.invalidate()
}

// changed invalidates local caches and tells the other nodes.
func (g *Gateway) changed(ctx context.Context, key string) {
	g.settings.invalidate()
	g.rules.invalidate()
	if g.d.Bus == nil {
		return
	}
	payload, _ := json.Marshal(map[string]string{"key": key})
	if err := g.d.Bus.Publish(context.WithoutCancel(ctx), core.ChannelConfigChanged, payload); err != nil {
		slog.WarnContext(ctx, "gateway: publish config:changed", "err", err)
	}
}

func (g *Gateway) statsLoop() {
	defer g.wg.Done()
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-g.stop:
			return
		case <-tick.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			g.hooks.flush(ctx)
			cancel()
		}
	}
}

func (g *Gateway) nodeID() string {
	if g.d.Node == nil {
		return ""
	}
	return g.d.Node.NodeID()
}

// healthy reports whether this node may serve gateway requests.
func (g *Gateway) healthy() bool {
	return g.health == nil || g.health()
}

func defaultLookupIP(ctx context.Context, host string) ([]net.IP, error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	out := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.IP)
	}
	return out, nil
}

// defaultHeaderWait bounds the time to the upstream response headers. A
// non-stream upstream only answers after generating the whole response.
func defaultHeaderWait(stream bool) time.Duration {
	if stream {
		return 3 * time.Minute
	}
	return 10 * time.Minute
}
