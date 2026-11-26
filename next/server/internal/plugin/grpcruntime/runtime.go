// Package grpcruntime runs plugins as go-plugin gRPC processes. It is the only
// package that imports go-plugin; the rest of the server sees plugins through
// the core capability interfaces.
package grpcruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/secret"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/grants"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/rows"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// SchemaDSN hands out plugin DSNs (implemented by dbschema.Manager): the
// virtual database address of the egress tunnel, without credentials, and
// the core's schema (CONTRACTS §130).
type SchemaDSN interface {
	DSN(ctx context.Context, pluginKey string) (dsn, schema string, err error)
}

// Options configures the runtime. DB, Redis, Cipher, Node and Launcher are
// required; the other dependencies are optional (the matching HostService
// calls fail with UNAVAILABLE when they are nil).
type Options struct {
	DB       *store.DB
	Redis    redis.UniversalClient
	Cipher   *secret.Cipher
	Node     core.Node
	Launcher core.PluginLauncher

	Egress core.EgressProvider
	// NetworkAccess is the administrator's plugin network access (CONTRACTS
	// §113), which a fetch's host must pass as well (requireFetchHost); nil
	// = no such setting (tests).
	NetworkAccess core.PluginNetworkAccess
	Authorizer    core.Authorizer
	Ledger        core.Ledger
	// Prices serves HostService.PriceUsage; nil makes it fail with UNAVAILABLE.
	Prices  UsagePricer
	Schemas SchemaDSN
	// Accounts serves HostService.ListAccounts / GetAccount: the
	// accounts of the calling plugin's own account types.
	// Nil makes both calls fail with UNAVAILABLE.
	Accounts core.PluginAccountReader
	// Bus carries plugin cluster broadcasts (HostService.Publish).
	Bus core.Bus
	// Locker serves the plugin cluster locks (HostService.LockAcquire /
	// LockRenew / LockRelease). Nil makes the three calls fail with
	// UNAVAILABLE.
	Locker core.TokenLocker
	// Sticky serves HostService.ClearStickyBindings; nil makes it fail with
	// UNAVAILABLE.
	Sticky core.StickyBindingClearer
	// Tasks serves the runtime task calls of HostService (SyncTasks ...
	// EnqueueTask); nil makes them fail with UNAVAILABLE.
	Tasks core.PluginTasks
	// Environment serves HostService.GetEnvironment; nil makes it fail with
	// UNAVAILABLE.
	Environment core.EnvironmentResolver
	// Settings serves HostService.GetSettings: the reader of each section
	// (CONTRACTS §107). A section without one fails with UNAVAILABLE.
	Settings map[string]core.SettingsReader
	// Notifier serves HostService.ListNotificationChannels and Notify; nil
	// makes them fail with UNAVAILABLE.
	Notifier core.Notifier
	// Users serves HostService.GetUsers; nil makes it fail with UNAVAILABLE.
	Users core.UserDirectory

	HostVersion   string
	DataDir       string // plugin work directories live under DataDir/<key>/work
	StrictNetwork bool
	Seccomp       bool
	MaxMemoryMB   int // node cap on per-plugin memory (0 = none, the default)
	// RunDir holds one private 0700 directory per plugin process (go-plugin
	// sockets, the plugin's TMPDIR); default DataDir/.run. Keep it short:
	// unix socket paths are limited to ~107 bytes.
	RunDir string

	// Concurrency bounds concurrent calls per plugin instance, separately for
	// each call class (CONTRACTS §43.1). Zero fields use the defaults.
	Concurrency Concurrency
	Logger      *slog.Logger

	// Timing knobs; zero values use the production defaults.
	HealthInterval time.Duration // 10s
	StartTimeout   time.Duration // 30s
	RestartWindow  time.Duration // 10m
	MaxRestarts    int           // 5 per window
	BackoffBase    time.Duration // 1s
	BackoffMax     time.Duration // 60s
	DrainTimeout   time.Duration // 30s
	// DrainGrace keeps a draining instance accepting new calls for a moment:
	// requests that captured the previous generation before the switch still
	// reach it instead of failing (2s).
	DrainGrace time.Duration
}

// Concurrency is the per-instance limit of each call class. A slow class
// cannot starve another: a console route or a job waits for its own slots,
// never for the slots of the request path.
type Concurrency struct {
	// Hot: RPCs on the gateway request path (resolve, estimate, hooks,
	// scheduler, classify). Default 64.
	Hot int
	// Console: credential checks, test and model requests, plugin HTTP
	// routes, payment creation and notifications. Default 16.
	Console int
	// Background: jobs, events, broadcasts, monitors, refresh, data
	// migrations and payment status queries (the top-up sweep). Default 8.
	Background int
	// Platform Execute calls have no class limit: the account and API key
	// slots of the core bound their concurrency. They are still counted in
	// flight, so a drain waits for them.
}

func (c Concurrency) withDefaults() Concurrency {
	def := func(v *int, d int) {
		if *v <= 0 {
			*v = d
		}
	}
	def(&c.Hot, 64)
	def(&c.Console, 16)
	def(&c.Background, 8)
	return c
}

// Runtime starts plugin instances.
type Runtime struct {
	o   Options
	log *slog.Logger
}

func New(o Options) (*Runtime, error) {
	if o.DB == nil || o.Redis == nil || o.Cipher == nil || o.Node == nil || o.Launcher == nil {
		return nil, errors.New("grpcruntime: DB, Redis, Cipher, Node and Launcher are required")
	}
	o.Concurrency = o.Concurrency.withDefaults()
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	def(&o.HealthInterval, 10*time.Second)
	def(&o.StartTimeout, 30*time.Second)
	def(&o.RestartWindow, 10*time.Minute)
	def(&o.BackoffBase, time.Second)
	def(&o.BackoffMax, 60*time.Second)
	def(&o.DrainTimeout, 30*time.Second)
	def(&o.DrainGrace, 2*time.Second)
	if o.MaxRestarts <= 0 {
		o.MaxRestarts = 5
	}
	if o.RunDir == "" {
		o.RunDir = filepath.Join(o.DataDir, ".run")
	}
	return &Runtime{o: o, log: o.Logger.With("component", "plugin-runtime")}, nil
}

// Load extracts the binary of pkg, starts the plugin process, performs the
// handshake (GetInfo, InitHost, Configure, Health) and starts supervision.
// The returned instance serves calls until Drain or Stop.
func (r *Runtime) Load(ctx context.Context, pkg *registry.Package) (*Instance, error) {
	// Validate before ExtractBinary/Init: an incompatible plugin may already
	// have side effects by the time a post-start health check rejects it.
	if err := r.checkContract(pkg); err != nil {
		return nil, err
	}
	if err := r.checkHostCompat(pkg); err != nil {
		return nil, err
	}
	binPath, sum, err := pkg.ExtractBinary(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return nil, fmt.Errorf("plugin %s@%s: %w", manifest.DisplayID(pkg.Key), pkg.Version, err)
	}
	st, err := r.loadSettings(ctx, pkg)
	if err != nil {
		return nil, fmt.Errorf("plugin %s@%s: load settings: %w", manifest.DisplayID(pkg.Key), pkg.Version, err)
	}
	inst := newInstance(r, pkg, binPath, sum, st)
	if err := inst.startFirst(ctx); err != nil {
		inst.Stop()
		return nil, err
	}
	go inst.supervise()
	return inst, nil
}

// ErrOutdatedContract marks the refusal of a package built for an older
// plugin contract; the error's text is protocol.OutdatedContractMessage.
var ErrOutdatedContract = errors.New("plugin built for an older plugin contract")

// handshakeVersionMismatch is how go-plugin words a handshake with another
// protocol version (Client.checkProtoVersion); it has no error value for it.
const handshakeVersionMismatch = "incompatible API version with plugin"

type outdatedContractError struct {
	msg   string
	cause error
}

func (e *outdatedContractError) Error() string        { return e.msg }
func (e *outdatedContractError) Unwrap() error        { return e.cause }
func (e *outdatedContractError) Is(target error) bool { return target == ErrOutdatedContract }

// outdatedContract is the one answer to a package of an older plugin
// contract, whatever gave it away (cause): its manifest, the handshake of
// its binary or the protocol version it reports. It names the plugin as
// people see it, as the manifest check does (check.OutdatedPackage).
func (r *Runtime) outdatedContract(pkg *registry.Package, cause error) error {
	return &outdatedContractError{msg: protocol.OutdatedContractMessage(manifest.DisplayID(pkg.Key), pkg.Version, r.o.HostVersion), cause: cause}
}

// checkContract rejects a package whose manifest is of an older plugin
// contract (check.OutdatedPackage), before anything of it runs. Other
// manifest problems are not this check's: the package passed them when it
// was installed.
func (r *Runtime) checkContract(pkg *registry.Package) error {
	if pkg.Manifest == nil {
		return nil
	}
	raw, _ := pkg.ReadFile("manifest.json")
	if msg, outdated := check.OutdatedPackage(pkg.Manifest, raw, r.o.HostVersion); outdated {
		return &outdatedContractError{msg: msg}
	}
	return nil
}

// checkHostCompat rejects a package whose hostCompat excludes this host.
func (r *Runtime) checkHostCompat(pkg *registry.Package) error {
	if pkg.Manifest == nil || r.o.HostVersion == "" {
		return nil
	}
	if compatible, err := check.HostCompatible(pkg.Manifest.HostCompat, r.o.HostVersion); err != nil || !compatible {
		return fmt.Errorf("plugin %s@%s is incompatible with host %s", manifest.DisplayID(pkg.Key), pkg.Version, r.o.HostVersion)
	}
	return nil
}

// settings is the host-side configuration of one plugin, re-read on refresh.
type settings struct {
	configJSON  string
	grants      registry.Grants
	egress      string
	limits      resourceLimits
	fingerprint string
}

type resourceLimits struct {
	MemoryMB     int     `json:"memory_mb"`
	CPU          float64 `json:"cpu"`
	MaxThreads   int     `json:"max_threads"`
	MaxOpenFiles int     `json:"max_open_files"`
}

// loadSettings reads the settings an instance of pkg runs with; its grants
// are those of its own version (grants.Granted).
func (r *Runtime) loadSettings(ctx context.Context, pkg *registry.Package) (*settings, error) {
	key := pkg.Key
	var row model.Plugin
	err := pluginSettings(store.G(ctx, r.o.DB.Gorm), key).Take(&row).Error
	if err != nil {
		return nil, err
	}
	enc, limits := row.ConfigEnc, []byte(row.ResourceLimits)
	st := &settings{configJSON: "{}", egress: core.ValueOr(row.EgressPolicy, "")}
	if len(enc) > 0 {
		plain, err := r.o.Cipher.Decrypt(enc, rows.ConfigAAD(key))
		if err != nil {
			return nil, fmt.Errorf("decrypt plugin config: %w", err)
		}
		if len(plain) > 0 {
			st.configJSON = string(plain)
		}
	}
	if len(limits) > 0 {
		_ = json.Unmarshal(limits, &st.limits)
	}
	var declared []manifest.HostPermission
	if pkg.Manifest != nil {
		declared = pkg.Manifest.HostPermissions
	}
	if st.grants, err = grants.Granted(ctx, r.o.DB.Gorm, key, pkg.Version, declared); err != nil {
		return nil, err
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00", st.configJSON, st.egress, limits)
	perms := make([]string, 0, len(st.grants))
	for p := range st.grants {
		perms = append(perms, p)
	}
	sort.Strings(perms)
	for _, p := range perms {
		fmt.Fprintf(h, "%s=%s\x00", p, st.grants[p])
	}
	st.fingerprint = hex.EncodeToString(h.Sum(nil))
	return st, nil
}

// pluginSettings reads the settings columns of plugin key.
func pluginSettings(db *gorm.DB, key string) *gorm.DB {
	return db.Select("config_enc", "egress_policy", "resource_limits").Where("key = ?", key)
}
