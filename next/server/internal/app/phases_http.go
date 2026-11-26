package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/lookup"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/api"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/routes"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/ratelimit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/redeem"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/updater"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/webui"
	"github.com/Sub2API-Devs/sup2api/next/server/web"
)

// registerRoutes mounts every module's console API and the gateway.
func (b *boot) registerRoutes(r *httpapi.Router) {
	cfg := b.cfg
	r.AuthedRead("/system/version", systemVersionHandler(b.version, cfg.Managed.Enabled, cfg.NodeID, b.cl.Registry.BootID()))
	updater.RegisterRoutes(r, cfg.Managed.UpdaterSocket, cfg.Managed.UpdaterToken, b.db)
	audit.RegisterRoutes(r, b.db)
	b.idm.RegisterRoutes(r)
	b.az.RegisterRoutes(r)
	b.grp.RegisterRoutes(r)
	b.seg.RegisterRoutes(r)
	b.env.RegisterRoutes(r)
	b.ntf.RegisterRoutes(r)
	b.keys.RegisterRoutes(r)
	b.prx.RegisterRoutes(r)
	b.acc.RegisterRoutes(r)
	b.registerMoneyRoutes(r)
	lookup.New(b.db, b.az, b.bill).RegisterRoutes(r)
	b.settler.RegisterRoutes(r)
	b.tasks.RegisterRoutes(r, b.cl.Registry)
	api.New(api.Deps{
		DB: b.db, Install: b.inst, Market: b.mkt, Rollout: b.ctl, Nodes: b.cl.Registry, Registry: b.reg,
		Authz: b.az, Cipher: b.cipher, Bus: b.cl.Bus, Jobs: b.jobs, HookStats: b.gw, Plugins: cfg.Plugins,
	}).RegisterRoutes(r)
	writes := b.startPluginWrites()
	pr := routes.New(b.reg, b.idm, b.az, routes.WithHealth(b.cl.Registry), routes.WithVersionAssets(b.pkgs), routes.WithAudit(writes),
		routes.WithAccountAccess(b.acc))
	pr.RegisterRoutes(r)
	pr.RegisterAssets(b.engine)
	b.gw.RegisterRoutes(r)
	b.egress.RegisterRoutes(r)
}

// registerMoneyRoutes mounts the balance ledger, online top-up and redeem
// codes (CONTRACTS §140).
func (b *boot) registerMoneyRoutes(r *httpapi.Router) {
	b.bill.RegisterRoutes(r)
	b.pay.RegisterRoutes(r)
	redeem.New(b.db, b.bill, b.rdb, ratelimit.NewMinute(b.rdb, "redeem")).RegisterRoutes(r)
}

// startPluginWrites starts the audit log of plugin writes and registers its
// purge task.
func (b *boot) startPluginWrites() *audit.PluginWrites {
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
	return writes
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
	if err := b.mountHTTP(); err != nil {
		return err
	}
	requestCtx, cancelRequestContexts := context.WithCancel(context.Background())
	// Requests whose client already left are no longer tied to their context:
	// the gateway is told separately to stop reading their upstreams.
	cancelRequests := func() { b.gw.AbandonReads(); cancelRequestContexts() }
	defer cancelRequests()
	srv := &http.Server{Addr: b.cfg.HTTPAddr, Handler: b.gate.wrap(b.engine), ReadHeaderTimeout: 10 * time.Second,
		BaseContext: func(net.Listener) context.Context { return requestCtx }}
	listener, err := net.Listen("tcp", b.cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("http listen: %w", err)
	}
	defer listener.Close()
	errc := b.serveHTTP(srv, listener)
	b.publishManaged()
	select {
	case <-b.ctx.Done():
	case err := <-errc:
		return fmt.Errorf("http: %w", err)
	}
	return b.stopHTTP(srv, cancelRequests)
}

// serveHTTP serves srv on listener in the background. The channel gets the
// error that stopped it, unless that was the shutdown.
func (b *boot) serveHTTP(srv *http.Server, listener net.Listener) <-chan error {
	errc := make(chan error, 1)
	go func() {
		b.log.Info("listening", "addr", b.cfg.HTTPAddr, "boot_id", b.cl.Registry.BootID(), "version", b.version)
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()
	return errc
}

// mountHTTP builds the console assets, the engine and every route.
func (b *boot) mountHTTP() error {
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
	return nil
}

// stopHTTP exposes non-readiness before closing the listener, so
// health-based balancers can remove this node while existing requests are
// still alive. A managed core drained by its shell skips the delay (the
// shell already routes around it) and waits for requests in flight only as
// long as the shell's drain request allows: a graceful drain, sent once new
// requests go elsewhere, gets up to maxDrainGrace; any other drain cancels
// them at once. Canceled requests still finish their deferred usage Submit
// before billing stops (shutdownHTTP waits for every handler).
func (b *boot) stopHTTP(srv *http.Server, cancelRequests func()) error {
	b.gate.stop()
	delay, grace := b.cfg.ShutdownDelay, 30*time.Second
	if b.managed != nil && b.managed.draining() {
		delay, grace = 0, b.managed.shutdownGrace()
	}
	time.Sleep(delay)
	return shutdownHTTP(srv, b.gate, cancelRequests, grace)
}
