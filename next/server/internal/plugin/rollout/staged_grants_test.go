package rollout_test

import (
	"context"
	"errors"
	goruntime "runtime"
	"sync"
	"testing"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/secret"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/config"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/grpcruntime"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/install"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg/pkgtest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry/registrytest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/rollout"
)

// allowAll lets the consenting operator grant anything.
type allowAll struct{}

func (allowAll) Can(context.Context, int64, string) (bool, error) { return true, nil }
func (allowAll) PermissionSet(context.Context, int64) (core.PermissionSet, error) {
	return core.PermissionSet{}, nil
}
func (allowAll) CanGrant(context.Context, int64, []string) error { return nil }

// heldRuntime serves the test plugin for real, except that a version it
// fails never loads and a version it holds never gets ready.
type heldRuntime struct {
	rollout.Runtime
	mu         sync.Mutex
	fail, hold map[string]bool
}

type notReady struct{ rollout.Instance }

func (notReady) State() (string, string) { return grpcruntime.StateStarting, "" }

func (r *heldRuntime) set(version string, fail, hold bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fail[version], r.hold[version] = fail, hold
}

func (r *heldRuntime) Load(ctx context.Context, p *registry.Package) (rollout.Instance, error) {
	r.mu.Lock()
	fail, hold := r.fail[p.Version], r.hold[p.Version]
	r.mu.Unlock()
	if fail {
		return nil, errors.New("test: load fails")
	}
	inst, err := r.Runtime.Load(ctx, p)
	if err != nil || !hold {
		return inst, err
	}
	return notReady{inst}, nil
}

// stagedEnv is one node running the test plugin with the real install
// service, controller and runtime: what an instance started for a version
// reads as its grants is what a plugin process of that version gets.
type stagedEnv struct {
	h    *harness
	bin  []byte
	svc  *install.Service
	grt  *grpcruntime.Runtime
	rt   *heldRuntime
	ctl  *rollout.Controller
	reg  *registry.Registry
	pkgs *registry.Packages
	root pkgtest.Key
}

func newStagedEnv(t *testing.T) *stagedEnv {
	h := newHarness(t)
	ctx := context.Background()
	e := &stagedEnv{h: h, bin: registrytest.TestPlugin(t), reg: registry.New(), root: pkgtest.NewKey("root-1")}
	ts, err := pkg.NewTrustStore([]string{e.root.ID + "=" + e.root.PubB64()}, false)
	if err != nil {
		t.Fatal(err)
	}
	defaults := install.NewDefaultsApplier(h.rec)
	e.svc = install.New(install.Deps{DB: h.db, Trust: ts, Authz: allowAll{}, Permissions: h.rec, Defaults: defaults,
		Packages: registrytest.Source()},
		install.Options{HostVersion: "0.1.290", Plugins: config.PluginConfig{MaxPackageBytes: 100 << 20, MaxMemoryMB: 1024}})
	cipher, _ := secret.New(make([]byte, 32))
	nd := &registrytest.Node{RDB: h.rdb, ID: "solo", Boot: "boot-solo"}
	nd.Heartbeat(ctx)
	if e.grt, err = grpcruntime.New(grpcruntime.Options{DB: h.db, Redis: h.rdb, Cipher: cipher, Node: nd,
		Launcher: &registrytest.Launcher{}, DataDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	e.rt = &heldRuntime{Runtime: rollout.FromGRPC(e.grt), fail: map[string]bool{}, hold: map[string]bool{}}
	e.pkgs = registry.NewPackages(h.db, t.TempDir(), registry.WithSource(registrytest.Source()))
	t.Cleanup(e.pkgs.Close)
	if e.ctl, err = rollout.New(rollout.Options{DB: h.db, Node: nd, Bus: registrytest.Bus{RDB: h.rdb}, Packages: e.pkgs,
		Registry: e.reg, Runtime: e.rt, Defaults: defaults, Perms: h.rec, Events: h.rec,
		ReconcileInterval: 150 * time.Millisecond, LeaseTTL: 8 * time.Second, LeaseRenew: time.Second,
		CoordinatorTick: 100 * time.Millisecond, PrepareTimeout: 60 * time.Second, DrainTimeout: time.Second}); err != nil {
		t.Fatal(err)
	}
	e.ctl.Start(ctx)
	t.Cleanup(func() { e.ctl.Stop(context.Background()) })
	return e
}

// manifest is a version asking for kv and the extra host permissions.
func (e *stagedEnv) manifest(version string, extra ...string) *manifest.Manifest {
	m := pkgtest.Minimal(e.h.key, version, "sub2api")
	for _, id := range extra {
		m.HostPermissions = append(m.HostPermissions, manifest.HostPermission{ID: id})
	}
	return m
}

// build signs a package of m whose binary for this platform is the test
// plugin.
func (e *stagedEnv) build(m *manifest.Manifest) []byte {
	files := pkgtest.Files(m)
	files["runtimes/"+goruntime.GOOS+"-"+goruntime.GOARCH+"/plugin"] = e.bin
	return pkgtest.Zip(pkgtest.Sign(files, m.Publisher, e.root))
}

// approve uploads the version and consents to every permission it asks for.
func (e *stagedEnv) approve(version string, extra ...string) {
	e.h.t.Helper()
	ctx := context.Background()
	m := e.manifest(version, extra...)
	if _, err := e.svc.Upload(ctx, e.build(m), 0, install.UploadOptions{}); err != nil {
		e.h.t.Fatalf("upload %s: %v", version, err)
	}
	e.consent(version, extra...)
}

func (e *stagedEnv) consent(version string, extra ...string) {
	e.h.t.Helper()
	req := install.ConsentRequest{Grants: []install.GrantInput{{Permission: "kv"}}}
	for _, id := range extra {
		req.Grants = append(req.Grants, install.GrantInput{Permission: id})
	}
	if _, err := e.svc.Consent(context.Background(), e.h.key, version, req, 0); err != nil {
		e.h.t.Fatalf("consent %s: %v", version, err)
	}
}

// grants are what an instance of the version started now is granted.
func (e *stagedEnv) grants(version string) registry.Grants {
	e.h.t.Helper()
	ctx := context.Background()
	// Held: the controller sweeps the packages nothing holds.
	p, done, err := e.pkgs.Acquire(ctx, e.h.key, version)
	if err != nil {
		e.h.t.Fatal(err)
	}
	defer done()
	inst, err := e.grt.Load(ctx, p)
	if err != nil {
		e.h.t.Fatal(err)
	}
	defer inst.Stop()
	return inst.Grants()
}

func (e *stagedEnv) served(version string, perm string) func() bool {
	return func() bool {
		s, act, _ := e.h.plugin()
		p, ok := e.reg.Current().Plugin(e.h.key)
		if !ok || s != "enabled" || act != version || p.Version != version {
			return false
		}
		for _, g := range p.GrantedPermissions {
			if g == perm {
				return true
			}
		}
		return false
	}
}

func (e *stagedEnv) consentStatus(version string) (status string) {
	e.h.t.Helper()
	if err := e.h.db.SQL.QueryRowContext(context.Background(), `SELECT consent_status FROM plugin_versions WHERE plugin_key = $1 AND version = $2`,
		e.h.key, version).Scan(&status); err != nil {
		e.h.t.Fatal(err)
	}
	return status
}

// A consent to a version not active yet reaches that version only: the
// running one, restarted meanwhile, keeps what was granted to it (the
// permission it carried over included) and gets nothing it does not
// declare, before the upgrade or after it.
func TestUpgradeConsentWaitsForTheActivation(t *testing.T) {
	e := newStagedEnv(t)
	ctx := context.Background()
	e.approve("0.1.0")
	if _, err := e.ctl.Enable(ctx, e.h.key, 0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "0.1.0 served", e.served("0.1.0", "kv"))

	e.approve("0.2.0", "lock")
	if g := e.grants("0.1.0"); g.Has("lock") || !g.Has("kv") {
		t.Fatalf("0.1.0 restarted after the consent to 0.2.0 is granted %v", g)
	}
	if g := e.grants("0.2.0"); !g.Has("lock") || !g.Has("kv") {
		t.Fatalf("0.2.0 is granted %v before its activation", g)
	}

	if _, err := e.ctl.Upgrade(ctx, e.h.key, "0.2.0", 0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "0.2.0 served with lock", e.served("0.2.0", "lock"))
	if g := e.grants("0.1.0"); g.Has("lock") || !g.Has("kv") {
		t.Fatalf("0.1.0 is granted %v after 0.2.0 was activated", g)
	}
}

// An upgrade that fails or is cancelled takes back what was approved for
// it: the target version is granted nothing more than the running one had,
// waits for a new consent, and the withdrawal is audited.
func TestAbandonedUpgradeWithdrawsItsConsent(t *testing.T) {
	e := newStagedEnv(t)
	ctx := context.Background()
	e.approve("0.1.0")
	if _, err := e.ctl.Enable(ctx, e.h.key, 0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "0.1.0 served", e.served("0.1.0", "kv"))
	e.approve("0.2.0", "lock")

	withdrawn := func(what string, want int) {
		t.Helper()
		if g := e.grants("0.2.0"); g.Has("lock") || !g.Has("kv") {
			t.Fatalf("%s: 0.2.0 is granted %v", what, g)
		}
		if g := e.grants("0.1.0"); g.Has("lock") || !g.Has("kv") {
			t.Fatalf("%s: 0.1.0 is granted %v", what, g)
		}
		if s := e.consentStatus("0.2.0"); s != install.ConsentAwaiting {
			t.Fatalf("%s: 0.2.0 is %s", what, s)
		}
		var n int
		if err := e.h.db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM audit_logs WHERE action = 'plugin.grants.withdraw'
			AND target_id = $1 AND detail->>'version' = '0.2.0' AND detail->'permissions' ? 'lock'`, e.h.key).Scan(&n); err != nil || n != want {
			t.Fatalf("%s: withdrawal audits = %d, %v", what, n, err)
		}
	}

	e.rt.set("0.2.0", true, false)
	r, err := e.ctl.Upgrade(ctx, e.h.key, "0.2.0", 0)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the upgrade fails", func() bool { phase, _, _ := e.h.rolloutRow(r.ID); return phase == rollout.PhaseFailed })
	withdrawn("failed", 1)

	e.rt.set("0.2.0", false, true)
	e.consent("0.2.0", "lock")
	if r, err = e.ctl.Upgrade(ctx, e.h.key, "0.2.0", 0); err != nil {
		t.Fatal(err)
	}
	if err := e.ctl.Cancel(ctx, e.h.key, r.ID, 0); err != nil {
		t.Fatal(err)
	}
	withdrawn("cancelled", 2)

	// Consented again, it upgrades.
	e.rt.set("0.2.0", false, false)
	e.consent("0.2.0", "lock")
	if _, err := e.ctl.Upgrade(ctx, e.h.key, "0.2.0", 0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "0.2.0 served with lock", e.served("0.2.0", "lock"))
}

// A consent given while an enable prepares another version (nothing active
// yet) waits for its own version too: the version being enabled is not
// handed what was approved for the next one, and that one gets it when it
// is enabled in turn.
func TestConsentDuringAnEnableWaitsForItsVersion(t *testing.T) {
	e := newStagedEnv(t)
	ctx := context.Background()
	upload := func(version string) {
		t.Helper()
		m := e.manifest(version)
		m.HostPermissions = append(m.HostPermissions, manifest.HostPermission{ID: "lock", Optional: true})
		if _, err := e.svc.Upload(ctx, e.build(m), 0, install.UploadOptions{}); err != nil {
			t.Fatalf("upload %s: %v", version, err)
		}
	}
	upload("0.1.0")
	if _, err := e.svc.Consent(ctx, e.h.key, "0.1.0", install.ConsentRequest{
		Grants: []install.GrantInput{{Permission: "kv"}}, Denied: []string{"lock"}}, 0); err != nil {
		t.Fatal(err)
	}
	e.rt.set("0.1.0", false, true)
	r, err := e.ctl.Enable(ctx, e.h.key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, active, desired := e.h.plugin(); active != "" || desired != "0.1.0" {
		t.Fatalf("enabling: active %q desired %q", active, desired)
	}

	upload("0.2.0")
	e.consent("0.2.0", "lock")
	if g := e.grants("0.1.0"); g.Has("lock") || !g.Has("kv") {
		t.Fatalf("0.1.0 being enabled is granted %v after the consent to 0.2.0", g)
	}
	if g := e.grants("0.2.0"); !g.Has("lock") {
		t.Fatalf("0.2.0 is granted %v", g)
	}

	if err := e.ctl.Cancel(ctx, e.h.key, r.ID, 0); err != nil {
		t.Fatal(err)
	}
	if s := e.consentStatus("0.2.0"); s != install.ConsentApproved {
		t.Fatalf("0.2.0 after the cancelled enable of 0.1.0 is %s", s)
	}
	// Enable takes the newest approved version, 0.2.0, with its own consent.
	if _, err := e.ctl.Enable(ctx, e.h.key, 0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "0.2.0 served with lock", e.served("0.2.0", "lock"))
}
