package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	runtimecontract "github.com/Sub2API-Devs/sup2api/next/runtime-contract"
	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/secret"
	"github.com/Sub2API-Devs/sup2api/next/sdk/observation"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/account"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/cluster"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/event/delivery"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/gateway"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/iam/tokenstore"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/job"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/migrations"
	observer "github.com/Sub2API-Devs/sup2api/next/server/internal/observation"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/dbschema"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/egress"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/grpcruntime"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/install"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/market"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/rollout"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/sandbox"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/task"
)

// openStorage connects PostgreSQL (running or verifying migrations) and the
// Redis-protocol cache, and builds the credential cipher.
func (b *boot) openStorage() error {
	db, err := store.Open(b.ctx, b.cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	b.db = db
	b.stopper.add("db", func(context.Context) { db.Close() })
	if err := b.migrate(); err != nil {
		return err
	}
	if err := b.openRedis(); err != nil {
		return err
	}
	cipher, err := secret.New(b.cfg.MasterKey)
	if err != nil {
		return err
	}
	b.cipher = cipher
	return nil
}

// migrate runs the core migrations, or under the shell verifies the schema
// the shell prepared.
func (b *boot) migrate() error {
	if b.managed == nil {
		applied, err := store.Migrate(b.ctx, b.db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
		if err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
		b.log.Info("migrations applied", "files", applied)
		return nil
	}
	applied, err := prepareCoreSchema(b.ctx, b.db, b.managed.prepare)
	if err != nil {
		return fmt.Errorf("core schema preparation: %w", err)
	}
	b.log.Info("managed core schema verified", "migrations_applied", applied)
	return nil
}

// openRedis connects the Redis-protocol cache and the console sessions'
// client.
func (b *boot) openRedis() error {
	rdb, err := cluster.OpenRedis(b.ctx, b.cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("redis: %w", err)
	}
	b.rdb = rdb
	b.stopper.add("redis", func(context.Context) { _ = rdb.Close() })
	// The console sessions get their own client, which fails fast: every
	// console request waits on it.
	sessRdb := tokenstore.NewClient(rdb.Options())
	b.sessRdb = sessRdb
	b.stopper.add("session redis", func(context.Context) { _ = sessRdb.Close() })
	return nil
}

// startCluster joins the node registry and heartbeat.
func (b *boot) startCluster() error {
	// What the plugin sandbox enforces on this kernel: logged once and shown
	// on the nodes page (CONTRACTS §43.8).
	pc := b.cfg.Plugins
	sb := sandbox.ProbeStatus(runtime.GOOS == "linux" && !pc.DevMode, pc.Landlock, pc.StrictNetwork)
	sandbox.LogStatus(b.log, sb)
	b.cl = cluster.New(b.rdb, b.db, cluster.Options{NodeID: b.cfg.NodeID, Addr: b.cfg.PublicURL, HostVersion: b.version, Logger: b.log, Managed: b.cfg.Managed.Enabled, CoreBootID: b.cfg.Managed.BootID, PluginSandbox: &sb})
	if err := b.cl.Start(b.ctx); err != nil {
		return fmt.Errorf("cluster: %w", err)
	}
	b.stopper.add("cluster", func(context.Context) { b.cl.Close() })
	return nil
}

// startEgress builds the plugin database schemas and the egress tunnel. It
// runs before the account module is built: the administrator's plugin
// network access, which the tunnel serves, rules the requests the account
// module, the gateway and the runtime send for plugins as well (CONTRACTS
// §113).
func (b *boot) startEgress() error {
	cfg := b.cfg
	addrs, err := coreAddrs(cfg.DatabaseURL, cfg.RedisURL)
	if err != nil {
		return err
	}
	if cfg.Plugins.EgressAllowPrivate {
		b.log.Warn("plugins may reach private network addresses (SUB2API_PLUGIN_EGRESS_ALLOW_PRIVATE=true); tests and development only")
	}
	schemas, err := dbschema.New(b.ctx, b.db, cfg.DatabaseURL, cfg.MasterKey, b.log)
	if err != nil {
		return fmt.Errorf("plugin databases: %w", err)
	}
	// The legacy plugin roles and schemas (cores up to 0.1.303) are
	// converted once at start; Ensure retries the ones left (CONTRACTS §130).
	schemas.ConvertLegacy(b.ctx)
	egress := egress.New(b.db, egress.Options{NodeID: cfg.NodeID, DatabaseProxy: schemas.ServeTunnel, CoreAddrs: addrs,
		AllowPrivate: cfg.Plugins.EgressAllowPrivate, Events: b.events, Bus: b.cl.Bus, Logger: b.log})
	b.schemas, b.egress = schemas, egress
	b.stopper.add("egress", func(context.Context) { egress.Close() })
	if err := b.tasks.Register(egress.LogsPruneTaskSpec()); err != nil {
		return fmt.Errorf("egress log prune task: %w", err)
	}
	return nil
}

// coreAddrs are the addresses of PostgreSQL and Redis. Plugins reach
// PostgreSQL only through the virtual address GetDSN hands out (and only
// with db.schema), as their own database identity; the real one, Redis and
// every private address are never reachable through the tunnel (audit
// 2026-10-09 P0-3, CONTRACTS §130).
func coreAddrs(databaseURL, redisURL string) ([]string, error) {
	pgAddr, err := databaseAddr(databaseURL)
	if err != nil {
		return nil, err
	}
	addrs := []string{pgAddr}
	if ra, ok := redisAddr(redisURL); ok {
		addrs = append(addrs, ra)
	}
	return addrs, nil
}

// startPluginRuntime builds the plugin runtime (on the egress of
// startEgress), the resource stores, the gateway and the install/rollout
// services, and starts their loops. trust comes from buildCoreServices: it
// is not boot state.
func (b *boot) startPluginRuntime(trust *pkg.TrustStore) error {
	rt, err := b.newRuntime()
	if err != nil {
		return err
	}
	b.buildGateway()
	mutations := &cluster.PluginMutations{DB: b.db, Locker: b.cl.Locker}
	if b.managed != nil {
		mutations.AllowBootstrap = b.managed.bootstrapAllowed
	}
	defaults := install.NewDefaultsApplier(b.az)
	if err := b.startRollout(rt, mutations, defaults); err != nil {
		return err
	}
	b.mkt = market.New(b.db, hostVersion(b.version), b.marketClient, market.Options{
		MaxPackageBytes: b.cfg.Plugins.MaxPackageBytes, AllowHTTP: b.cfg.Plugins.MarketAllowHTTP, BaseURL: b.cfg.Plugins.MarketBaseURL})
	b.inst = install.New(install.Deps{Mutations: mutations,
		DB: b.db, Trust: trust, Authz: b.az, Permissions: b.az, Defaults: defaults,
		Rollout: b.ctl, Schemas: b.schemas, Bus: b.cl.Bus, Accounts: b.acc, KV: rt, Nodes: b.cl.Registry, Packages: b.packageSrc,
		Store: b.mkt,
	}, install.Options{HostVersion: b.version, Plugins: b.cfg.Plugins})
	if err := b.ensureMarket(); err != nil {
		return err
	}
	return b.startBuiltins()
}

// hostVersion is the core version as the market's market.Installer, which
// is built before the installer it hands packages to.
type hostVersion string

func (v hostVersion) HostVersion() string { return string(v) }

// ensureMarket checks the store's base (SUB2API_MARKET_BASE_URL) on every
// node, then brings the store's channels in line with it
// (market.EnsureStores) on every start of a core that may coordinate
// plugins: an unmanaged one, a bootstrap or the managed primary, so that an
// upgrade or a changed base takes effect without a bootstrap.
func (b *boot) ensureMarket() error {
	if err := b.mkt.ValidateBase(); err != nil {
		return err
	}
	if m := b.managed; m != nil && !m.coordinateAllowed() {
		return nil
	}
	if err := b.mkt.EnsureStores(b.ctx); err != nil {
		return fmt.Errorf("market: %w", err)
	}
	return nil
}

// newRuntime builds the plugin runtime on the egress of startEgress.
func (b *boot) newRuntime() (*grpcruntime.Runtime, error) {
	cfg := b.cfg
	launcher := sandbox.NewLauncher(sandbox.LauncherOptions{DisableWrap: cfg.Plugins.DevMode, Landlock: cfg.Plugins.Landlock, Logger: b.log})
	settings, err := settingsReaders(b.bill.SettingsReaders(), gateway.SettingsReaders(b.db), b.env.SettingsReaders())
	if err != nil {
		return nil, err
	}
	rt, err := grpcruntime.New(grpcruntime.Options{
		DB: b.db, Redis: b.rdb, Bus: b.cl.Bus, Locker: b.cl.Locker, Cipher: b.cipher, Node: b.cl.Registry, Launcher: launcher,
		Egress: b.egress, NetworkAccess: b.egress, Authorizer: b.az, Ledger: b.bill, Prices: b.bill, Schemas: b.schemas, Accounts: b.acc, Sticky: gateway.NewStickyClearer(b.rdb), Environment: b.env, Notifier: b.ntf, Users: b.idm,
		Settings: settings, Tasks: b.jobBridge(),
		HostVersion: b.version, DataDir: cfg.Plugins.DataDir, RunDir: cfg.Plugins.RunDir,
		StrictNetwork: cfg.Plugins.StrictNetwork, Seccomp: cfg.Plugins.Seccomp, MaxMemoryMB: cfg.Plugins.MaxMemoryMB,
		Logger: b.log,
	})
	if err != nil {
		return nil, fmt.Errorf("plugin runtime: %w", err)
	}
	return rt, nil
}

// buildGateway builds the request gate, the core's observation consumer and
// the gateway.
func (b *boot) buildGateway() {
	// The request gate admits HTTP traffic; WebSocket sessions also watch it to
	// end themselves on drain, which http.Server.Shutdown cannot do for them.
	b.gate = &requestGate{}
	if b.managed != nil {
		b.gate.stop()
	}
	observations := observer.New(b.reg, b.stats, observer.Consumer{Namespace: observation.CoreNamespace, AllAccounts: true,
		Phases: []string{observation.UsageFinalized, observation.AttemptFinished}, Metrics: observation.CoreMetrics, Client: account.StatsConsumer{}})
	b.stopper.add("observations", observations.Close)
	b.gw = gateway.New(gateway.Deps{
		DB: b.db, Redis: b.rdb, Bus: b.cl.Bus, Node: b.cl.Registry, Registry: b.reg, Events: b.events,
		Auth: b.keys, Pricer: b.bill, Balance: b.bill, Slots: b.cl.Slots,
		Environment: b.env,
		Accounts:    b.acc, NetworkAccess: b.egress, Settler: b.settler, Observations: observations, Tasks: b.settler, Limiter: b.limiter, KeyWindows: b.keys.Windows, KeyUsage: b.keys, Models: b.grp, Quota: b.acc, Config: b.cfg, Converters: b.converters,
		Draining: b.gate.isDraining,
	})
}

// startRollout starts the rollout controller on rt, then delivery of
// notifications.
func (b *boot) startRollout(rt *grpcruntime.Runtime, mutations *cluster.PluginMutations, defaults *install.DefaultsApplier) error {
	ctl, err := rollout.New(rollout.Options{Mutations: mutations,
		CanCoordinate: b.canCoordinate,
		DB:            b.db, Node: b.cl.Registry, Bus: b.cl.Bus, Packages: b.pkgs, Registry: b.reg,
		Runtime: rollout.FromGRPC(rt), Schemas: b.schemas, Defaults: defaults, Perms: b.az, Events: b.events, Logger: b.log,
	})
	if err != nil {
		return fmt.Errorf("rollout: %w", err)
	}
	b.ctl = ctl
	ctl.Start(b.ctx)
	b.stopper.add("rollout", ctl.Stop)
	// Polling must stop before plugin instances and their egress. Keep the
	// earlier cleanup too, for initialization errors before this point: the
	// stack moves the re-registration to the end instead of adding a second
	// call, so the close order is unchanged.
	b.stopper.add("settler", b.settler.Stop)
	// Delivery starts once the plugin runtime does, so the providers can
	// bind their channels first. The end of b.ctx only stops claiming; Stop
	// lets the delivery in progress finish (at most notify DrainTimeout, 35s,
	// of the 90s teardown budget) before rollout stops the plugin instances.
	b.ntf.Start(b.ctx)
	b.stopper.add("notifications", b.ntf.Stop)
	return nil
}

// startBuiltins starts the builtin convergence loop, or the bundled-plugin
// upgrade loop for a managed core that was not bootstrapped.
func (b *boot) startBuiltins() error {
	cfg := b.cfg
	log := b.log.With("component", "builtin-plugins")
	b.builtinReady = &atomic.Bool{}
	switch {
	case b.managed == nil:
		ready, stop := startBuiltinConvergence(b.ctx, b.inst, b.cl.Locker, b.reg, cfg.Plugins.BuiltinDir, log)
		b.builtinReady = ready
		b.stopper.add("builtin", stop)
	case b.managed.prepare.Bootstrap:
		ready, stop := startBuiltinBootstrap(b.ctx, b.inst, b.cl.Locker, b.reg, cfg.Plugins.BuiltinDir, log)
		b.builtinReady = ready
		b.stopper.add("builtin", stop)
	default:
		// A managed core that was not bootstrapped does not wait for its bundle
		// to admit the node, but it still brings the bundled plugins up to the
		// versions it carries, once it may coordinate plugins and no core plan
		// blocks plugin changes (CONTRACTS §37).
		b.builtinReady.Store(true)
		allowed := func() bool {
			if b.canCoordinate != nil && !b.canCoordinate() {
				return false
			}
			check, cancel := context.WithTimeout(b.ctx, 5*time.Second)
			defer cancel()
			busy, err := cluster.CoreUpdateBusy(check, b.db.Gorm)
			return err == nil && !busy
		}
		b.stopper.add("builtin", startBuiltinUpgrade(b.ctx, b.inst, b.cl.Locker, cfg.Plugins.BuiltinDir, allowed, log))
	}
	return nil
}

// buildTasks creates the scheduled task service and starts its node backend
// right away (TASK-FRAMEWORK-DESIGN §5.1): node tasks run as soon as a module
// registers them; cluster tasks wait for startBackground. It is idempotent.
func (b *boot) buildTasks() error {
	if b.tasks != nil {
		return nil
	}
	tasks, err := task.New(task.Options{NodeID: b.cfg.NodeID, Version: b.version, Redis: b.rdb, Bus: b.cl.Bus,
		CanWork: b.canWork, Logger: b.log, DB: b.db, Executor: b.work,
		ClientID: b.cfg.NodeID + "-" + b.cl.Registry.BootID()})
	if err != nil {
		return fmt.Errorf("scheduled tasks: %w", err)
	}
	if err := tasks.StartNode(b.ctx); err != nil {
		return fmt.Errorf("scheduled tasks: %w", err)
	}
	b.tasks = tasks
	b.stopper.add("tasks", tasks.Stop)
	return nil
}

// buildBackground constructs the plugin job bridge and the event delivery
// loop and registers their teardown. It is idempotent so the boot path and
// admission share one pair of instances: registerRoutes hands the console the
// bridge built here, and admission starts that same bridge.
func (b *boot) buildBackground() {
	if b.dlv != nil {
		return
	}
	b.jobBridge()
	b.stopper.add("jobs", func(c context.Context) { _ = b.jobs.Stop(c) })
	b.dlv = delivery.New(b.db, b.cl.Locker, b.cl.Bus, b.reg, b.log, b.cfg.NodeID, delivery.Options{CanRun: b.canWork})
	if b.tasks != nil {
		for _, spec := range []core.TaskSpec{b.jobs.RetentionTaskSpec(), b.dlv.RetentionTaskSpec()} {
			if err := b.tasks.Register(spec); err != nil {
				b.log.Error("register retention task", "task", spec.Name, "err", err)
			}
		}
	}
	b.stopper.add("delivery", func(c context.Context) { _ = b.dlv.Stop(c) })
	// Extraction goroutines still call plugins and submit usage after their
	// HTTP handler returns. Join them before stopping either dependency.
	b.stopper.add("gateway", func(context.Context) { b.gw.Close() })
	// Every module that registers scheduled tasks is built by now: moving
	// the task service to the top of the stack stops their runs before the
	// modules themselves (§5.2).
	if b.tasks != nil {
		b.stopper.add("tasks", b.tasks.Stop)
	}
}

// jobBridge returns the plugin job bridge, built on first use: the plugin
// runtime serves the runtime task calls of HostService through it before
// the background starts.
func (b *boot) jobBridge() *job.Bridge {
	if b.jobs == nil {
		b.jobs = job.New(b.db, b.reg, b.cl.Bus, b.log, b.cfg.NodeID, job.Options{Version: b.version})
	}
	return b.jobs
}

// startBackground starts the delivery loop, the cluster tasks and the plugin
// job bridge built by buildBackground, once. It is what admission runs
// (m.start) on a managed core, and what run() calls directly on a standalone
// one.
func (b *boot) startBackground() error {
	b.buildBackground()
	b.startOnce.Do(func() {
		b.startErr = b.dlv.Start(b.ctx)
		if b.startErr == nil {
			if b.startErr = b.buildTasks(); b.startErr == nil {
				// River itself starts and stops with background admission.
				b.startErr = b.tasks.StartCluster(b.ctx)
			}
		}
		if b.startErr == nil {
			b.startErr = b.jobs.Start(b.ctx, b.tasks)
		}
	})
	if b.startErr != nil {
		return fmt.Errorf("background startup: %w", b.startErr)
	}
	return nil
}

// publishManaged hands the control endpoints the live objects they poll. Only
// a managed core has a supervisor to answer.
func (b *boot) publishManaged() {
	m := b.managed
	if m == nil {
		return
	}
	m.mu.Lock()
	m.gate, m.start, m.active = b.gate, b.startBackground, b.work.Active
	m.validate = func(c context.Context, a runtimecontract.Admission) error {
		return verifyAdmission(c, b.db, b.cfg.NodeID, a)
	}
	m.check = func(c context.Context) (string, error) {
		if !b.cl.Registry.Healthy() || !b.sharedAssets.Ready() || !b.builtinReady.Load() {
			return "", errors.New("node dependencies are not ready")
		}
		return approvedPluginsReady(c, b.db, b.reg, b.version)
	}
	if m.mode == "preparing" {
		m.mode = "prepared"
	}
	m.mu.Unlock()
}

// databaseAddr extracts host:port from a PostgreSQL URL or key=value DSN;
// no plugin may reach this address through the egress tunnel.
func databaseAddr(dsn string) (string, error) {
	host, port := "", "5432"
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return "", fmt.Errorf("parse database url: %w", err)
		}
		host = u.Hostname()
		if p := u.Port(); p != "" {
			port = p
		}
	} else {
		for _, f := range strings.Fields(dsn) {
			k, v, _ := strings.Cut(f, "=")
			switch k {
			case "host":
				host = strings.Trim(v, "'")
			case "port":
				port = strings.Trim(v, "'")
			}
		}
	}
	if host == "" {
		host = "localhost"
	}
	return net.JoinHostPort(host, port), nil
}

// redisAddr is the TCP "host:port" of the Redis URL (false for a unix
// socket or an unparsable URL).
func redisAddr(raw string) (string, bool) {
	o, err := cluster.ParseRedisURL(raw)
	if err != nil || o.Network == "unix" || o.Addr == "" {
		return "", false
	}
	return o.Addr, true
}
