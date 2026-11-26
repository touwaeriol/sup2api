package app

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	runtimecontract "github.com/Sub2API-Devs/sup2api/next/runtime-contract"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/account"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/apikey"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/authz"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/background"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/environment"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/event"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/gateway/convert"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/group"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/iam"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/notify"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/blobs"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/market"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/proxy"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/ratelimit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/segment"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/usage"
)

// buildCoreServices builds and starts the modules the request path depends on:
// billing, usage settlement, authz/iam and the account and proxy
// directories. The trust store is returned for the plugin runtime
// phase instead of being kept on the boot.
func (b *boot) buildCoreServices() (*pkg.TrustStore, error) {
	b.events = event.NewPublisher(b.db)
	// One converter registry for the gateway (conversion), the account
	// module (endpoints an account type can serve) and the plugin registry
	// (adapted endpoints need one), ARCHITECTURE 6.6.
	b.converters = convert.Default()
	b.reg = registry.New(registry.WithConverters(b.converters))
	trust, err := b.trustStore()
	if err != nil {
		return nil, err
	}
	b.buildPackages(trust)
	for _, step := range []func() error{b.buildBilling, b.buildSettlement, b.buildIdentity, b.buildKeys, b.buildAccounts, b.startReconcile, b.buildPayment} {
		if err := step(); err != nil {
			return nil, err
		}
	}
	return trust, nil
}

// trustStore is what plugin packages are verified against.
func (b *boot) trustStore() (*pkg.TrustStore, error) {
	trust, err := pkg.NewTrustStore(b.cfg.Plugins.OfficialRootKeys, b.cfg.Plugins.AllowUnsigned)
	if err != nil {
		return nil, fmt.Errorf("plugin trust: %w", err)
	}
	if !b.cfg.Plugins.VerifySignatures {
		trust.SetVerifySignatures(false)
		b.log.Warn("plugin signature verification is disabled (SUB2API_PLUGIN_VERIFY_SIGNATURES=false)")
	}
	return trust, nil
}

// buildPackages builds the plugin package store and the market client.
func (b *boot) buildPackages(trust *pkg.TrustStore) {
	cfg := b.cfg
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
	packageHTTP := &http.Client{Timeout: runtimecontract.PluginPackageTransferTimeout, CheckRedirect: market.CheckRedirect(cfg.Plugins.MarketAllowHTTP)}
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
}

// buildBilling builds billing, the background executor and the tasks.
func (b *boot) buildBilling() error {
	bill := billing.New(b.db, b.rdb, b.cl.Bus, b.events, b.reg)
	b.bill = bill
	b.stopper.add("billing", func(context.Context) { bill.Close() })
	b.work = background.New(8)
	if b.managed != nil {
		b.canWork = b.managed.backgroundAllowed
		b.canCoordinate = b.managed.coordinateAllowed
		b.work.Admitted = b.canWork
	}
	return b.buildTasks()
}

// buildSettlement builds the environment, usage settlement and
// notifications, and registers their tasks.
func (b *boot) buildSettlement() error {
	b.env = environment.New(b.db, b.cl.Bus, b.cfg.Environment)
	b.env.Events = b.events
	b.stopper.add("environment", b.env.Close)
	b.settler = usage.New(b.db, b.bill, b.bill, b.events, usage.Options{CanRetry: b.canWork, Environment: b.env})
	b.settler.Start(b.ctx)
	b.stopper.add("settler", b.settler.Stop)
	if err := b.tasks.Register(b.settler.RetryTaskSpec()); err != nil {
		return fmt.Errorf("settlement retry task: %w", err)
	}
	if err := b.tasks.Register(b.settler.RollupTaskSpec()); err != nil {
		return fmt.Errorf("usage rollup task: %w", err)
	}
	b.ntf = notify.New(b.db, b.reg, notify.Options{CanRun: b.canWork})
	if err := b.tasks.Register(b.ntf.PurgeTaskSpec()); err != nil {
		return fmt.Errorf("notify purge task: %w", err)
	}
	b.stopper.add("notifications", b.ntf.Stop)
	return nil
}

// buildIdentity builds authz and iam, and bootstraps the administrator.
func (b *boot) buildIdentity() error {
	az := authz.New(authz.Deps{DB: b.db, Bus: b.cl.Bus, Plugins: b.reg})
	if err := az.Start(b.ctx); err != nil {
		return fmt.Errorf("authz: %w", err)
	}
	if err := az.RegisterTasks(b.tasks); err != nil {
		return fmt.Errorf("authz tasks: %w", err)
	}
	b.az = az
	b.settler.Authz = az
	b.bill.Authz = az
	b.bill.Users = az
	b.idm = iam.New(iam.Deps{DB: b.db, Redis: b.rdb, SessionRedis: b.sessRdb, Config: b.cfg, Events: b.events, Authz: az, Owners: az, Bus: b.cl.Bus,
		Cipher: b.cipher})
	if b.managed == nil || b.managed.prepare.Bootstrap {
		if err := b.idm.Bootstrap(b.ctx); err != nil {
			return fmt.Errorf("bootstrap admin: %w", err)
		}
	}
	return nil
}

// buildKeys builds the groups, the API keys and the proxies.
func (b *boot) buildKeys() error {
	b.buildGroups()
	b.keys = apikey.New(b.db, b.rdb, b.az, b.reg)
	b.keys.Cipher = b.cipher
	// Per-key caps the owners set (CONTRACTS §60): minute windows and slots.
	b.keys.Windows, b.keys.Slots = ratelimit.NewMinute(b.rdb, "apikey"), b.cl.Slots
	b.keys.Balances = b.bill
	b.prx = proxy.New(b.db, b.cipher, b.cl.Bus, proxy.Options{AllowPrivate: b.cfg.AllowPrivateUpstream, Registry: b.reg, Events: b.events, ScopeGrants: b.az})
	// Per-account rpm/tpm limits (CONTRACTS §18).
	b.limiter = account.NewLimiter(b.rdb)
	b.stats = account.NewStats(b.rdb, b.db, b.cl.Slots, b.cl.Bus)
	b.stats.Start()
	b.stopper.add("account-stats", b.stats.Close)
	return b.startEgress()
}

// buildGroups builds the groups and the user groups. The segment service
// and billing write their parts of POST/PATCH /groups (the segments'
// rates, the model prices and rates) in the group's transaction
// (CONTRACTS §97).
func (b *boot) buildGroups() {
	b.grp = group.New(b.db, b.rdb, b.cl.Bus, b.reg, b.az, b.bill)
	b.seg = segment.New(segment.Deps{DB: b.db, Authz: b.az, Users: b.az, Platforms: b.grp.Platforms, RatesChanged: b.bill.RatesChanged})
	b.grp.Parts = []core.GroupWritePart{b.seg, b.bill}
}

// buildAccounts builds the account module and starts the key, proxy and
// account loops.
func (b *boot) buildAccounts() error {
	b.acc = account.New(account.Deps{
		DB: b.db, Redis: b.rdb, Cipher: b.cipher, Registry: b.reg, Proxies: b.prx, NetworkAccess: b.egress, Events: b.events,
		Slots: b.cl.Slots, Limiter: b.limiter, Stats: b.stats, Bus: b.cl.Bus, AllowPrivateUpstream: b.cfg.AllowPrivateUpstream, Converters: b.converters,
		// Ownership checks and proxy_url resolution (CONTRACTS §21).
		Authorizer: b.az, Resolver: b.prx, ScopeGrants: b.az,
		// Credential refresh: one renewal per account cluster-wide, one
		// sweeping node, only once admitted (CONTRACTS §48).
		Locker: b.cl.Locker, CanWork: b.canWork,
		// A test warns about a model without a price (CONTRACTS §122.5).
		Priced: b.bill,
	})
	// The group page lists the protocols the accounts of a group serve.
	b.grp.Protocols = b.acc
	// Price sync sources (upstream API keys encrypted) and GET /key/prices for
	// downstream sup2api instances (CONTRACTS §17).
	b.bill.SetSyncDeps(billing.SyncDeps{Cipher: b.cipher, Keys: b.keys})
	go b.keys.Run(b.ctx)
	go b.prx.Run(b.ctx)
	if err := b.acc.RegisterTasks(b.tasks); err != nil {
		return fmt.Errorf("account tasks: %w", err)
	}
	go b.acc.Run(b.ctx)
	return nil
}

// startReconcile starts the reconcile loop that closes pre-charged usage
// rows (CONTRACTS §25.4). It starts here rather than next to settler.Start
// because it needs the account and proxy directories: the core sends the
// reconcile request itself, through the account's proxy, so that a plugin
// with asynchronous work needs neither network access nor account access of
// its own. The sweep is core.usage.reconcile (one node at a time).
func (b *boot) startReconcile() error {
	b.settler.StartReconcile(b.ctx, usage.ReconcileDeps{
		Executor: b.work,
		Registry: b.reg, Accounts: b.acc,
		Slots: b.cl.Slots, Limiter: b.limiter,
		AllowPrivateUpstream: b.cfg.AllowPrivateUpstream, NodeID: b.cfg.NodeID, Logger: b.log,
	})
	if err := b.tasks.Register(b.settler.ReconcileTaskSpec()); err != nil {
		return fmt.Errorf("usage reconcile task: %w", err)
	}
	return nil
}
