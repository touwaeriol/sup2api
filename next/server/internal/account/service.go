// Package account implements upstream accounts: account types declared by
// plugins (identified by plugin key + type id), account CRUD with encrypted credentials, the console
// "test account" action, and the gateway's core.AccountDirectory.
package account

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/secret"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// Deps are the collaborators of the account service.
type Deps struct {
	DB       *store.DB
	Redis    redis.UniversalClient
	Cipher   *secret.Cipher
	Registry core.PluginRegistry
	Proxies  core.ProxyDirectory
	// NetworkAccess is the administrator's plugin network access (CONTRACTS
	// §113): the host of every request a plugin describes and the core sends
	// (SendAccountRequest, quota, refresh, test, model list) must pass it;
	// nil = no such setting (tests).
	NetworkAccess core.PluginNetworkAccess
	Events        core.EventPublisher
	Slots         core.Slots // optional: in_use column is 0 without it
	// Limiter reports rpm/tpm usage (rate_usage column); optional.
	Limiter core.AccountLimiter
	Stats   *Stats
	Bus     core.Bus // optional: single node without it
	// Converters reports the protocol pairs the gateway can convert; optional
	// (nil: account types only list the endpoints they serve natively).
	Converters core.ProtocolConverters
	// AllowPrivateUpstream disables the private-address guard of the test
	// action (config SUB2API_GATEWAY_ALLOW_PRIVATE_UPSTREAM).
	AllowPrivateUpstream bool
	// Authorizer answers the permission questions handlers ask beyond the
	// route key (proxy visibility, account:settings:custom; CONTRACTS §21).
	// nil denies everything it is asked.
	Authorizer core.Authorizer
	// ScopeGrants clears the scoped grants naming deleted accounts
	// (CONTRACTS §21); needed to delete or purge accounts.
	ScopeGrants core.ScopeGrantClearer
	// Resolver finds or creates the proxy named by proxy_url when an account
	// is saved (CONTRACTS §21.4); nil rejects proxy_url as unsupported.
	Resolver core.ProxyResolver
	// Locker serialises credential refreshes across nodes (CONTRACTS §48):
	// one lock per account (the sweep is core.account.credential_refresh,
	// one run at a time). nil: this node only (single-node tests).
	Locker core.Locker
	// CanWork admits background quota queries on this node (managed cores
	// run them only once admitted); nil always allows.
	CanWork func() bool
	// Priced tells whether the model of a test has a price in the account's
	// groups: a test of one without warns (CONTRACTS §122.5). nil checks
	// nothing.
	Priced core.PricedModels
}

const (
	snapshotTTL   = 30 * time.Second
	flushInterval = 10 * time.Second
	// Mask replaces sensitive credential values in responses; sending it back
	// in a PATCH keeps the stored value.
	Mask = "******"
)

// Service serves the account endpoints and implements core.AccountDirectory.
type Service struct {
	d Deps

	schemas sync.Map // sha256(extensionsSchema) -> *jsonschema.Schema

	sf       singleflight.Group
	cacheMu  sync.Mutex
	epoch    uint64
	groups   map[int64]groupSnap
	accounts map[int64]accountSnap

	touchMu sync.Mutex
	touched map[int64]time.Time

	// quota throttles passive quota writes; quotaFlight merges concurrent
	// active quota queries of one account (CONTRACTS §44).
	quota           *quotaRecorder
	quotaFlight     singleflight.Group
	quotaSweepMu    sync.Mutex
	quotaSweepAfter int64
	// balanceFlight merges the balance reads of one account on this node:
	// a manual refresh and the sweep (CONTRACTS §52).
	balanceFlight singleflight.Group

	// quotaMirror is this node's copy of the quota snapshots the model
	// availability precheck reads (quota_mirror.go); built on first use.
	quotaMirrorOnce sync.Once
	quotaMirror     *quotaMirror

	// refreshLocal serialises credential refreshes of one account on this
	// node when there is no cluster Locker (CONTRACTS §48).
	refreshLocal sync.Map // account id -> *sync.Mutex

	// tasks registers the quota mirror's node task once it is warm
	// (RegisterTasks; nil without the task framework).
	tasks atomic.Pointer[core.Tasks]
}

type groupSnap struct {
	at   time.Time
	refs []core.AccountRef
	// releases holds the release time of each ref under a timed automatic
	// disable; Candidates skips the ref until then.
	releases map[int64]time.Time
}

type accountSnap struct {
	at  time.Time
	acc core.Account
}

var _ core.AccountDirectory = (*Service)(nil)

// New builds the account service. Call Run to receive account:changed
// broadcasts, persist last_used_at and write passive quota samples.
func New(d Deps) *Service {
	s := &Service{
		d:        d,
		groups:   map[int64]groupSnap{},
		accounts: map[int64]accountSnap{},
		touched:  map[int64]time.Time{},
	}
	s.quota = newQuotaRecorder(func(ctx context.Context, id int64, w map[string]store.QuotaWindow) error {
		if err := s.d.DB.SaveAccountQuota(ctx, id, store.QuotaPassive, w); err != nil {
			return err
		}
		s.evictQuotaCache(ctx, id)
		s.quotaChanged(ctx, id)
		return nil
	})
	return s
}

// RegisterRoutes mounts the account and account type endpoints. Every route
// accepts any scope of its action (CONTRACTS §21.0); handlers narrow their
// SQL with the caller's scope (scopeOf).
func (s *Service) RegisterRoutes(r *httpapi.Router) {
	r.Perm("GET", "/settings/account-stats", "settings:read", s.getStatsSettings)
	r.Perm("PUT", "/settings/account-stats", "settings:manage", s.putStatsSettings)
	r.PermScoped("GET", "/accounts/auto-disable", s.getAutoDisablePolicy, perm.AccountCreate, perm.AccountUpdate)
	r.PermScoped("POST", "/accounts/param-override/preview", s.previewParamOverride, perm.AccountCreate, perm.AccountUpdate)
	r.PermScoped("GET", "/accounts/stats", s.getAccountStats, perm.AccountRead)
	r.PermScoped("GET", "/accounts/summary", s.summary, perm.AccountRead)
	r.PermScoped("GET", "/accounts/:id/stats", s.getAccountStats, perm.AccountRead)
	// Browsing platforms and types: whoever reads or creates some accounts.
	browse := []perm.Key{perm.AccountRead, perm.AccountCreate}
	r.PermScoped("GET", "/platforms", s.listPlatforms, browse...)
	r.AuthedRead("/me/platforms", s.listMyPlatforms)
	r.PermScoped("GET", "/account-types", s.listTypes, browse...)
	r.PermScoped("GET", "/accounts", s.list, perm.AccountRead)
	// Group binding, custom upstreams and guarded settings are checked per
	// field (checkOwnChange, customSettings).
	fieldKeys := []perm.Key{perm.GroupManage, perm.AccountGroupBind, perm.AccountRelay, perm.AccountSettingsCustom}
	r.PermScoped("POST", "/accounts", s.create, perm.AccountCreate).Uses(fieldKeys...)
	r.PermScoped("GET", "/accounts/:id", s.get, perm.AccountRead)
	r.PermScoped("PATCH", "/accounts/:id", s.update, perm.AccountUpdate).Uses(fieldKeys...)
	r.PermScoped("DELETE", "/accounts/:id", s.delete, perm.AccountDelete)
	// 所属: an administrator moves an account to another owner (CONTRACTS §103).
	r.Perm("PUT", "/accounts/:id/owner", perm.AccountOwnerUpdate, s.putOwner)
	r.PermScoped("POST", "/accounts/:id/test", s.test, perm.AccountTest)
	r.PermScoped("POST", "/accounts/:id/models/fetch", s.fetchAccountModels, perm.AccountTest).Uses(perm.AccountRelay)
	r.PermScoped("POST", "/account-types/:plugin_key/:type/models/fetch", s.fetchTypeModels, perm.AccountCreate).Uses(perm.AccountRelay)
	// The proxy of an authorization flow before the account exists (CONTRACTS §117).
	r.PermScoped("POST", "/account-types/:plugin_key/:type/proxy-ticket", s.issueProxyTicket, perm.AccountCreate, perm.AccountUpdate)
	// Plain-text credentials: a step-up proof each time (CONTRACTS §80.2).
	r.StepUp(core.StepUpCredentialReveal).PermScoped("POST", "/accounts/:id/credentials/reveal", s.reveal, perm.AccountCredentialView)
	// Subscription quota and balance (CONTRACTS §44, §52).
	r.PermScoped("GET", "/accounts/:id/quota", s.getQuota, perm.AccountRead)
	r.PermScoped("GET", "/accounts/:id/balance", s.getBalance, perm.AccountRead)
	r.PermScoped("POST", "/accounts/:id/reset-status", s.resetStatus, perm.AccountUpdate)
	// Usage and usage limits (CONTRACTS §73).
	r.PermScoped("GET", "/accounts/:id/usage", s.getUsage, perm.AccountRead)
	// Requests in flight and rates by client model (CONTRACTS §68.7).
	r.PermScoped("GET", "/accounts/:id/rate-models", s.getRateModels, perm.AccountRead)
	r.PermScoped("GET", "/accounts/:id/usage-limit", s.getUsageLimit, perm.AccountRead)
	r.PermScoped("PUT", "/accounts/:id/usage-limit", s.putUsageLimit, perm.AccountUpdate)
	// Credential refresh (CONTRACTS §48).
	r.PermScoped("POST", "/accounts/:id/refresh-credentials", s.refreshCredentials, perm.AccountUpdate)
}

// Run subscribes to account:changed, flushes last_used_at every 10 s, writes
// passive quota samples and keeps the quota mirror loaded until ctx is done.
func (s *Service) Run(ctx context.Context) {
	go s.quota.run(ctx)
	go s.runQuotaMirror(ctx)
	defer s.watchChanges(ctx)()
	tk := time.NewTicker(flushInterval)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := s.FlushLastUsed(fctx); err != nil {
				slog.Warn("account: flush last_used_at", "err", err)
			}
			cancel()
			return
		case <-tk.C:
			if err := s.FlushLastUsed(ctx); err != nil {
				slog.WarnContext(ctx, "account: flush last_used_at", "err", err)
			}
		}
	}
}

// watchChanges subscribes to what invalidates this node's caches:
// account:changed, a reconnected bus (the messages published meanwhile are
// lost) and plugin generation switches. The returned func unsubscribes.
func (s *Service) watchChanges(ctx context.Context) (cancel func()) {
	var cancels []func()
	if s.d.Bus != nil {
		cancels = append(cancels, s.d.Bus.Subscribe(core.ChannelAccountChanged, s.onAccountChanged))
		if r, ok := s.d.Bus.(core.BusReconnects); ok {
			cancels = append(cancels, r.OnReconnect(func() {
				s.invalidate()
				s.invalidateQuota(0)
			}))
		}
	}
	if s.d.Registry != nil {
		cancels = append(cancels, s.d.Registry.OnChange(func(core.Generation) { s.pluginsChanged(ctx) }))
	}
	return func() {
		for _, c := range cancels {
			c()
		}
	}
}

// onAccountChanged serves account:changed: the directory snapshots are
// dropped and the account's (without an id, every account's) mirrored quota
// snapshot is read again. A written quota snapshot (kind "quota") leaves the
// directory snapshots alone: they hold no quota data.
func (s *Service) onAccountChanged(payload []byte) {
	id, kind := core.ParseAccountChanged(payload)
	if kind != core.AccountChangeQuota {
		s.invalidate()
	}
	s.invalidateQuota(id)
}

// pluginsDelay is when a plugin generation switch invalidates the quota
// mirror a second time: the switch may be announced before the plugins row
// whose trigger starts new quota generations is committed.
var pluginsDelay = 2 * time.Second

// pluginsChanged follows a plugin generation switch: the quota generations
// of the accounts of a plugin whose status or version changed are replaced
// by a trigger, without an account:changed message; and the account types
// declaring modelAvailability may have changed (plugins start after Run, so
// this is also what loads the mirror at boot).
func (s *Service) pluginsChanged(ctx context.Context) {
	m := s.quotas()
	m.invalidate(0)
	m.reloadSoon()
	time.AfterFunc(pluginsDelay, func() {
		if ctx.Err() == nil {
			m.invalidate(0)
			m.reloadSoon()
		}
	})
}

// invalidate drops every cached snapshot on this node.
func (s *Service) invalidate() {
	s.cacheMu.Lock()
	s.epoch++
	s.groups = map[int64]groupSnap{}
	s.accounts = map[int64]accountSnap{}
	s.cacheMu.Unlock()
}

// changed invalidates local caches and tells the other nodes.
func (s *Service) changed(ctx context.Context, id int64) {
	s.invalidateNode(id)
	if err := core.PublishAccountChanged(ctx, s.d.Bus, id); err != nil {
		slog.WarnContext(ctx, "account: publish account:changed", "err", err)
	}
}

// quotaChanged follows a written quota snapshot of account id: only the
// quota mirrors are told (account:changed of kind "quota"); the directory
// snapshots hold no quota data.
func (s *Service) quotaChanged(ctx context.Context, id int64) {
	s.invalidateQuota(id)
	if err := core.PublishAccountQuotaChanged(ctx, s.d.Bus, id); err != nil {
		slog.WarnContext(ctx, "account: publish account:changed", "err", err)
	}
}

// invalidateNode drops what this node keeps of account id (0: of every
// account), so the node that changed it does not depend on receiving its
// own account:changed: the directory snapshots and the mirrored quota
// snapshot.
func (s *Service) invalidateNode(id int64) {
	s.invalidate()
	s.invalidateQuota(id)
}

func (s *Service) gen() core.Generation {
	if s.d.Registry == nil {
		return nil
	}
	return s.d.Registry.Current()
}

func (s *Service) accountType(pluginKey, typ string) (core.AccountTypeBinding, bool) {
	g := s.gen()
	if g == nil {
		return core.AccountTypeBinding{}, false
	}
	return g.AccountType(pluginKey, typ)
}

func (s *Service) pluginActive(key string) bool {
	g := s.gen()
	if g == nil {
		return false
	}
	_, ok := g.Plugin(key)
	return ok
}

// activePluginKeys lists the plugins of the current generation; nil before
// the first generation is loaded (then nothing is filtered).
func (s *Service) activePluginKeys() []string { return core.ActivePluginKeys(s.d.Registry) }

// customSettings reports whether the caller may set guarded settings to
// values other than the official ones (account:settings:custom, CONTRACTS
// §21.3).
func (s *Service) customSettings(ctx context.Context) (bool, error) {
	return core.Can(ctx, s.d.Authorizer, "account:settings:custom")
}

var _ core.AccountAccess = (*Service)(nil)

// accessActions are the actions AccountsInScope answers.
var accessActions = []string{"read", "update", "test"}

// AccountsInScope implements core.AccountAccess with the caller's data scope
// of the action (CONTRACTS §21), as loadRow reads it; the narrowed result
// follows the order of ids, deduplicated.
func (s *Service) AccountsInScope(ctx context.Context, userID int64, action string, ids []int64) (bool, []int64, error) {
	if !slices.Contains(accessActions, action) {
		return false, nil, core.InvalidFields(core.FieldError{Field: "action", Code: "invalid",
			Message: t(ctx, "Unknown account action", "未知的账号操作")})
	}
	scope, err := core.Scope(core.WithUserID(ctx, userID), s.d.Authorizer, perm.AccountAction(action))
	switch {
	case err != nil:
		return false, nil, err
	case scope.Empty():
		return false, nil, core.AccountsDenied(action)
	case scope.All:
		return true, nil, nil
	}
	var allowed []int64
	err = liveInScope(store.G(ctx, s.d.DB.Gorm), ids, scope).
		Order(clause.OrderBy{Expression: clause.Expr{SQL: "array_position(?, a.id)", Vars: []any{store.Int64s(ids)}}}).
		Pluck("a.id", &allowed).Error
	return false, allowed, err
}

// liveInScope is the live accounts of ids in scope.
func liveInScope(db *gorm.DB, ids []int64, scope core.DataScope) *gorm.DB {
	return db.Table("accounts a").Where("a.id = ANY(?) AND a.deleted_at IS NULL", store.Int64s(ids)).Scopes(core.AccountRows().Scope(scope))
}

func t(ctx context.Context, en, zh string) string { return core.Text(ctx, en, zh) }

func cooldownKey(id int64) string { return "cooldown:account:" + itoa(id) }
