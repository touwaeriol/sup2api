package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	runtimecontract "github.com/Sub2API-Devs/sup2api/next/runtime-contract"
	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/secret"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/observation"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/account"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/apikey"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/authz"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/background"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/cluster"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/environment"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/event"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/event/delivery"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/gateway"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/gateway/convert"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/group"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/iam"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/iam/tokenstore"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/job"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/lookup"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/migrations"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/notify"
	observer "github.com/Sub2API-Devs/sup2api/next/server/internal/observation"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/api"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/blobs"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/dbschema"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/egress"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/grpcruntime"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/install"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/market"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/rollout"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/routes"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/sandbox"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/proxy"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/ratelimit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/segment"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/task"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/updater"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/usage"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/webui"
	"github.com/Sub2API-Devs/sup2api/next/server/web"
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
	if b.managed == nil {
		applied, err := store.Migrate(b.ctx, db, migrations.FS, store.CoreTracker{}, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
		if err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
		b.log.Info("migrations applied", "files", applied)
	} else {
		applied, err := prepareCoreSchema(b.ctx, db, b.managed.prepare)
		if err != nil {
			return fmt.Errorf("core schema preparation: %w", err)
		}
		b.log.Info("managed core schema verified", "migrations_applied", applied)
	}

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

	cipher, err := secret.New(b.cfg.MasterKey)
	if err != nil {
		return err
	}
	b.cipher = cipher
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

// buildCoreServices builds and starts the modules the request path depends on:
// billing, usage settlement, authz/iam and the account and proxy
// directories. The trust store is returned for the plugin runtime
// phase instead of being kept on the boot.
func (b *boot) buildCoreServices() (*pkg.TrustStore, error) {
	cfg := b.cfg
	b.events = event.NewPublisher(b.db)
	// One converter registry for the gateway (conversion), the account
	// module (endpoints an account type can serve) and the plugin registry
	// (adapted endpoints need one), ARCHITECTURE 6.6.
	b.converters = convert.Default()
	b.reg = registry.New(registry.WithConverters(b.converters))
	trust, err := pkg.NewTrustStore(cfg.Plugins.OfficialRootKeys, cfg.Plugins.AllowUnsigned)
	if err != nil {
		return nil, fmt.Errorf("plugin trust: %w", err)
	}
	if !cfg.Plugins.VerifySignatures {
		trust.SetVerifySignatures(false)
		b.log.Warn("plugin signature verification is disabled (SUB2API_PLUGIN_VERIFY_SIGNATURES=false)")
	}
	// Package bytes are not in PostgreSQL. Under the shell every node keeps
	// the packages it stored or used and fetches a missing one from any online
	// node over the node network; market packages are downloaded by each
	// node. Without a shell only one node is supported.
	packageStore := blobs.Store(blobs.Dir(filepath.Join(cfg.Plugins.DataDir, "blobs")))
	if cfg.Managed.UpdaterSocket != "" {
		packageStore = blobs.NewShell(cfg.Managed.UpdaterSocket, cfg.Managed.UpdaterToken, cfg.Plugins.MaxPackageBytes)
	}
	b.marketClient = &http.Client{Timeout: 60 * time.Second}
	// Package downloads get more time than index requests.
	packageHTTP := &http.Client{Timeout: runtimecontract.PluginPackageTransferTimeout}
	b.packageSrc = &blobs.Source{Store: packageStore, MaxBytes: cfg.Plugins.MaxPackageBytes,
		Download: func(ctx context.Context, url string, limit int64) ([]byte, error) {
			return market.Fetch(ctx, packageHTTP, url, limit)
		}}
	// Nodes re-verify package signatures before unpacking, so revoked keys
	// and publishers stop loading everywhere.
	pkgs := registry.NewPackages(b.db, cfg.Plugins.DataDir, registry.WithSource(b.packageSrc), registry.WithVerifier(
		registry.TrustVerifier(trust, b.db.Gorm, pkg.Limits{MaxPackageBytes: cfg.Plugins.MaxPackageBytes})))
	b.pkgs = pkgs
	b.stopper.add("packages", func(context.Context) { pkgs.Close() })

	bill := billing.New(b.db, b.rdb, b.cl.Bus, b.events, b.reg)
	b.bill = bill
	b.stopper.add("billing", func(context.Context) { bill.Close() })
	b.work = background.New(8)
	if b.managed != nil {
		b.canWork = b.managed.backgroundAllowed
		b.canCoordinate = b.managed.coordinateAllowed
		b.work.Admitted = b.canWork
	}
	if err := b.buildTasks(); err != nil {
		return nil, err
	}
	b.env = environment.New(b.db, b.cl.Bus, b.cfg.Environment)
	b.env.Events = b.events
	b.stopper.add("environment", b.env.Close)
	b.settler = usage.New(b.db, bill, bill, b.events, usage.Options{CanRetry: b.canWork, Environment: b.env})
	b.settler.Start(b.ctx)
	b.stopper.add("settler", b.settler.Stop)
	if err := b.tasks.Register(b.settler.RetryTaskSpec()); err != nil {
		return nil, fmt.Errorf("settlement retry task: %w", err)
	}
	if err := b.tasks.Register(b.settler.RollupTaskSpec()); err != nil {
		return nil, fmt.Errorf("usage rollup task: %w", err)
	}
	b.ntf = notify.New(b.db, b.reg, notify.Options{CanRun: b.canWork})
	if err := b.tasks.Register(b.ntf.PurgeTaskSpec()); err != nil {
		return nil, fmt.Errorf("notify purge task: %w", err)
	}
	b.stopper.add("notifications", b.ntf.Stop)

	az := authz.New(authz.Deps{DB: b.db, Bus: b.cl.Bus, Plugins: b.reg})
	if err := az.Start(b.ctx); err != nil {
		return nil, fmt.Errorf("authz: %w", err)
	}
	if err := az.RegisterTasks(b.tasks); err != nil {
		return nil, fmt.Errorf("authz tasks: %w", err)
	}
	b.az = az
	b.settler.Authz = az
	b.bill.Authz = az
	b.bill.Users = az
	b.idm = iam.New(iam.Deps{DB: b.db, Redis: b.rdb, SessionRedis: b.sessRdb, Config: cfg, Events: b.events, Authz: az, Owners: az, Bus: b.cl.Bus,
		Cipher: b.cipher, PriceRulesChanged: b.bill.PriceRulesChanged})
	if b.managed == nil || b.managed.prepare.Bootstrap {
		if err := b.idm.Bootstrap(b.ctx); err != nil {
			return nil, fmt.Errorf("bootstrap admin: %w", err)
		}
	}

	b.grp = group.New(b.db, b.rdb, b.cl.Bus, b.reg, az)
	b.keys = apikey.New(b.db, b.rdb, az, b.reg)
	b.keys.Cipher = b.cipher
	// Per-key caps the owners set (CONTRACTS §60): minute windows and slots.
	b.keys.Windows, b.keys.Slots = ratelimit.NewMinute(b.rdb, "apikey"), b.cl.Slots
	b.keys.Balances = b.bill
	b.prx = proxy.New(b.db, b.cipher, b.cl.Bus, proxy.Options{AllowPrivate: cfg.AllowPrivateUpstream, Registry: b.reg, Events: b.events, ScopeGrants: az})
	// Per-account rpm/tpm limits (CONTRACTS §18).
	b.limiter = account.NewLimiter(b.rdb)
	b.stats = account.NewStats(b.rdb, b.db, b.cl.Slots, b.cl.Bus)
	b.stats.Start()
	b.stopper.add("account-stats", b.stats.Close)
	if err := b.startEgress(); err != nil {
		return nil, err
	}
	b.acc = account.New(account.Deps{
		DB: b.db, Redis: b.rdb, Cipher: b.cipher, Registry: b.reg, Proxies: b.prx, NetworkAccess: b.egress, Events: b.events,
		Slots: b.cl.Slots, Limiter: b.limiter, Stats: b.stats, Bus: b.cl.Bus, AllowPrivateUpstream: cfg.AllowPrivateUpstream, Converters: b.converters,
		// Ownership checks and proxy_url resolution (CONTRACTS §21).
		Authorizer: az, Resolver: b.prx, ScopeGrants: az,
		// Credential refresh: one renewal per account cluster-wide, one
		// sweeping node, only once admitted (CONTRACTS §48).
		Locker: b.cl.Locker, CanWork: b.canWork,
	})
	// The group page lists the protocols the accounts of a group serve.
	b.grp.Protocols = b.acc
	// Price sync sources (upstream API keys encrypted) and GET /key/prices for
	// downstream sup2api instances (CONTRACTS §17).
	b.bill.SetSyncDeps(billing.SyncDeps{Cipher: b.cipher, Keys: b.keys})
	go b.keys.Run(b.ctx)
	go b.prx.Run(b.ctx)
	if err := b.acc.RegisterTasks(b.tasks); err != nil {
		return nil, fmt.Errorf("account tasks: %w", err)
	}
	go b.acc.Run(b.ctx)

	// The reconcile loop closes pre-charged usage rows (CONTRACTS §25.4). It
	// starts here rather than next to settler.Start because it needs the
	// account and proxy directories: the core sends the reconcile request
	// itself, through the account's proxy, so that a plugin with
	// asynchronous work needs neither network access nor account access of
	// its own. The sweep is core.usage.reconcile (one node at a time).
	b.settler.StartReconcile(b.ctx, usage.ReconcileDeps{
		Executor: b.work,
		Registry: b.reg, Accounts: b.acc,
		Slots: b.cl.Slots, Limiter: b.limiter,
		AllowPrivateUpstream: cfg.AllowPrivateUpstream, NodeID: cfg.NodeID, Logger: b.log,
	})
	if err := b.tasks.Register(b.settler.ReconcileTaskSpec()); err != nil {
		return nil, fmt.Errorf("usage reconcile task: %w", err)
	}
	return trust, nil
}

// startEgress builds the plugin database schemas and the egress tunnel. It
// runs before the account module is built: the administrator's plugin
// network access, which the tunnel serves, rules the requests the account
// module, the gateway and the runtime send for plugins as well (CONTRACTS
// §113).
func (b *boot) startEgress() error {
	cfg := b.cfg
	pgAddr, err := databaseAddr(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	// Plugins reach PostgreSQL only through the address GetDSN hands out
	// (and only with db.schema); Redis and every private address are never
	// reachable through the tunnel (audit 2026-10-09 P0-3).
	var coreAddrs []string
	if ra, ok := redisAddr(cfg.RedisURL); ok {
		coreAddrs = append(coreAddrs, ra)
	}
	if cfg.Plugins.EgressAllowPrivate {
		b.log.Warn("plugins may reach private network addresses (SUB2API_PLUGIN_EGRESS_ALLOW_PRIVATE=true); tests and development only")
	}
	schemas := dbschema.New(b.db, cfg.DatabaseURL, b.log)
	// Roles of the per-plugin database user model (removed) are taken back once
	// at start; Ensure retries the ones left (docs/DESIGN-2026-11-18-PLUGIN-DB-CORE-USER.md).
	schemas.ConvertLegacyRoles(b.ctx)
	egress := egress.New(b.db, egress.Options{NodeID: cfg.NodeID, DatabaseAddrs: []string{pgAddr}, DatabaseProxy: schemas.ServeTunnel, CoreAddrs: coreAddrs,
		AllowPrivate: cfg.Plugins.EgressAllowPrivate, Events: b.events, Bus: b.cl.Bus, Logger: b.log})
	b.schemas, b.egress = schemas, egress
	b.stopper.add("egress", func(context.Context) { egress.Close() })
	if err := b.tasks.Register(egress.LogsPruneTaskSpec()); err != nil {
		return fmt.Errorf("egress log prune task: %w", err)
	}
	return nil
}

// startPluginRuntime builds the plugin runtime (on the egress of
// startEgress), the resource stores, the gateway and the install/rollout
// services, and starts their loops. trust comes from buildCoreServices: it
// is not boot state.
func (b *boot) startPluginRuntime(trust *pkg.TrustStore) error {
	cfg := b.cfg
	schemas, egress := b.schemas, b.egress
	launcher := sandbox.NewLauncher(sandbox.LauncherOptions{DisableWrap: cfg.Plugins.DevMode, Landlock: cfg.Plugins.Landlock, Logger: b.log})
	settings, err := settingsReaders(b.bill.SettingsReaders(), gateway.SettingsReaders(b.db), b.env.SettingsReaders())
	if err != nil {
		return err
	}

	rt, err := grpcruntime.New(grpcruntime.Options{
		DB: b.db, Redis: b.rdb, Bus: b.cl.Bus, Locker: b.cl.Locker, Cipher: b.cipher, Node: b.cl.Registry, Launcher: launcher,
		Egress: egress, NetworkAccess: egress, Authorizer: b.az, Ledger: b.bill, Prices: b.bill, Schemas: schemas, Accounts: b.acc, Sticky: gateway.NewStickyClearer(b.rdb), Environment: b.env, Notifier: b.ntf, Users: b.idm,
		Settings: settings, Tasks: b.jobBridge(),
		HostVersion: b.version, DataDir: cfg.Plugins.DataDir, RunDir: cfg.Plugins.RunDir,
		StrictNetwork: cfg.Plugins.StrictNetwork, Seccomp: cfg.Plugins.Seccomp, MaxMemoryMB: cfg.Plugins.MaxMemoryMB,
		Logger: b.log,
	})
	if err != nil {
		return fmt.Errorf("plugin runtime: %w", err)
	}

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
		Accounts:    b.acc, NetworkAccess: egress, Settler: b.settler, Observations: observations, Tasks: b.settler, Limiter: b.limiter, KeyWindows: b.keys.Windows, KeyUsage: b.keys, Quota: b.acc, Config: cfg, Converters: b.converters,
		Draining: b.gate.isDraining,
	})

	mutations := &cluster.PluginMutations{DB: b.db, Locker: b.cl.Locker}
	if b.managed != nil {
		mutations.AllowBootstrap = b.managed.bootstrapAllowed
	}
	defaults := install.NewDefaultsApplier(b.az)
	ctl, err := rollout.New(rollout.Options{Mutations: mutations,
		CanCoordinate: b.canCoordinate,
		DB:            b.db, Node: b.cl.Registry, Bus: b.cl.Bus, Packages: b.pkgs, Registry: b.reg,
		Runtime: rollout.FromGRPC(rt), Schemas: schemas, Defaults: defaults, Perms: b.az, Events: b.events, Logger: b.log,
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

	b.inst = install.New(install.Deps{Mutations: mutations,
		DB: b.db, Trust: trust, Authz: b.az, Permissions: b.az, Defaults: defaults,
		Rollout: ctl, Schemas: schemas, Bus: b.cl.Bus, Accounts: b.acc, KV: rt, Nodes: b.cl.Registry, Packages: b.packageSrc,
	}, install.Options{HostVersion: b.version, Plugins: cfg.Plugins})
	b.mkt = market.New(b.db, b.inst, b.marketClient, cfg.Plugins.MaxPackageBytes)
	if b.managed == nil || b.managed.prepare.Bootstrap {
		if err := b.mkt.SeedSources(b.ctx, cfg.Plugins.MarketSourcesJSON); err != nil {
			return fmt.Errorf("market sources: %w", err)
		}
	}
	return b.startBuiltins()
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

// registerRoutes mounts every module's console API and the gateway.
func (b *boot) registerRoutes(r *httpapi.Router) {
	cfg := b.cfg
	r.AuthedRead("/system/version", systemVersionHandler(b.version, cfg.Managed.Enabled, cfg.NodeID, b.cl.Registry.BootID()))
	updater.RegisterRoutes(r, cfg.Managed.UpdaterSocket, cfg.Managed.UpdaterToken, b.db)
	audit.RegisterRoutes(r, b.db)
	b.idm.RegisterRoutes(r)
	b.az.RegisterRoutes(r)
	b.grp.RegisterRoutes(r)
	segment.New(segment.Deps{DB: b.db, Authz: b.az, Users: b.az, Platforms: b.grp.Platforms, PriceRulesChanged: b.bill.PriceRulesChanged,
		RuleCounts: b.grp.SegmentRuleCounts}).RegisterRoutes(r)
	b.env.RegisterRoutes(r)
	b.ntf.RegisterRoutes(r)
	b.keys.RegisterRoutes(r)
	b.prx.RegisterRoutes(r)
	b.acc.RegisterRoutes(r)
	b.bill.RegisterRoutes(r)
	lookup.New(b.db, b.az).RegisterRoutes(r)
	b.settler.RegisterRoutes(r)
	b.tasks.RegisterRoutes(r, b.cl.Registry)
	api.New(api.Deps{
		DB: b.db, Install: b.inst, Market: b.mkt, Rollout: b.ctl, Nodes: b.cl.Registry, Registry: b.reg,
		Authz: b.az, Cipher: b.cipher, Bus: b.cl.Bus, Jobs: b.jobs, HookStats: b.gw, Plugins: cfg.Plugins,
	}).RegisterRoutes(r)
	writes := &audit.PluginWrites{DB: b.db}
	if err := b.tasks.Register(writes.PurgeTask()); err != nil {
		b.log.Error("register plugin audit purge task", "err", err)
	}
	writesCtx, stopWrites := context.WithCancel(b.ctx)
	writesDone := make(chan struct{})
	go func() {
		defer close(writesDone)
		writes.Run(writesCtx)
	}()
	b.stopper.add("plugin-writes", func(wait context.Context) {
		stopWrites()
		select {
		case <-writesDone:
		case <-wait.Done():
		}
	})
	pr := routes.New(b.reg, b.idm, b.az, routes.WithHealth(b.cl.Registry), routes.WithVersionAssets(b.pkgs), routes.WithAudit(writes),
		routes.WithAccountAccess(b.acc))
	pr.RegisterRoutes(r)
	pr.RegisterAssets(b.engine)
	b.gw.RegisterRoutes(r)
	b.egress.RegisterRoutes(r)
}

// buildEngine creates the gin engine with the shared middleware and the health
// endpoint; module routes are mounted by registerRoutes.
func (b *boot) buildEngine() error {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	// Client IPs (login rate limiting, usage records) come from
	// X-Forwarded-For only when the request arrives from a trusted proxy.
	if err := engine.SetTrustedProxies(b.cfg.TrustedProxies); err != nil {
		return fmt.Errorf("trusted proxies: %w", err)
	}
	// The core owns exactly the routes of manifest.CoreRoutePrefixes (/api/v1
	// through httpapi.Router, /plugin-ui, /healthz) plus the bare /api;
	// manifest/check rejects plugin endpoints that would shadow them
	// (manifest.ReservedPath) and the console fallback answers them with a
	// JSON 404. Other /api/... paths (/api/v3) are gateway paths.
	engine.GET("/"+manifest.RouteHealthz, func(c *gin.Context) {
		status, text := http.StatusOK, "ok"
		if b.gate.isDraining() || !b.cl.Registry.Healthy() || !b.builtinReady.Load() || !b.sharedAssets.Ready() {
			status, text = http.StatusServiceUnavailable, "unavailable"
		}
		c.JSON(status, gin.H{"status": text, "version": b.version, "node": b.cfg.NodeID, "boot_id": b.cl.Registry.BootID()})
	})
	b.engine = engine
	b.router = httpapi.NewRouter(engine, b.idm, b.az)
	return nil
}

// startHTTP builds the console assets, routes and the HTTP server, and blocks
// until ctx ends or the listener fails.
func (b *boot) startHTTP() error {
	cfg := b.cfg
	sharedAssets, err := webui.NewSharedAssets(b.db, web.Dist, webui.SharedOptions{Logger: b.log})
	if err != nil {
		return fmt.Errorf("console asset manifest: %w", err)
	}
	b.sharedAssets = sharedAssets
	// Registered before the routes: readiness waits for its first run.
	if err := sharedAssets.RegisterTasks(b.tasks); err != nil {
		return fmt.Errorf("console asset lease: %w", err)
	}
	if err := b.buildEngine(); err != nil {
		return err
	}
	b.registerRoutes(b.router)
	if err := checkRoutes(b.router, b.az); err != nil {
		return err
	}
	ui, err := webui.New(web.Dist, webui.WithSharedAssets(sharedAssets))
	if err != nil {
		return fmt.Errorf("console assets: %w", err)
	}
	// Plugin-declared gateway endpoints are dynamic, so they are dispatched
	// before the console fallback instead of being registered as routes.
	b.engine.NoRoute(b.gw.Middleware(), ui.Serve)

	requestCtx, cancelRequestContexts := context.WithCancel(context.Background())
	// Requests whose client already left are no longer tied to their context:
	// the gateway is told separately to stop reading their upstreams.
	cancelRequests := func() { b.gw.AbandonReads(); cancelRequestContexts() }
	defer cancelRequests()
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: b.gate.wrap(b.engine), ReadHeaderTimeout: 10 * time.Second,
		BaseContext: func(net.Listener) context.Context { return requestCtx }}
	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("http listen: %w", err)
	}
	defer listener.Close()
	errc := make(chan error, 1)
	go func() {
		b.log.Info("listening", "addr", cfg.HTTPAddr, "boot_id", b.cl.Registry.BootID(), "version", b.version)
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()
	b.publishManaged()
	select {
	case <-b.ctx.Done():
	case err := <-errc:
		return fmt.Errorf("http: %w", err)
	}
	// Expose non-readiness before closing the listener, so health-based
	// balancers can remove this node while existing requests are still alive.
	// A managed core drained by its shell skips the delay (the shell already
	// routes around it) and waits for requests in flight only as long as the
	// shell's drain request allows: a graceful drain, sent once new requests
	// go elsewhere, gets up to maxDrainGrace; any other drain cancels them at
	// once. Canceled requests still finish their deferred usage Submit before
	// billing stops (shutdownHTTP waits for every handler).
	b.gate.stop()
	delay, grace := cfg.ShutdownDelay, 30*time.Second
	if b.managed != nil && b.managed.draining() {
		delay, grace = 0, b.managed.shutdownGrace()
	}
	time.Sleep(delay)
	return shutdownHTTP(srv, b.gate, cancelRequests, grace)
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
// plugins reach this address through the egress tunnel.
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
