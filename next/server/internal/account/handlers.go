package account

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg"
	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/segment"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

// row is one accounts row.
type row struct {
	ID        int64
	Name      string
	PluginKey string
	Type      string
	// CredEnc is nil on rows of the account list (listSelectRow): never
	// decrypt or write back a list row.
	CredEnc           []byte
	Settings          []byte
	ProxyID           *int64
	Status            string
	StatusReason      string
	AutoDisabledUntil *time.Time
	Schedulable       bool
	AutoDisable       bool
	Priority          int
	Weight            int
	MaxConcurrency    int
	Models            []string
	ModelMapping      []byte
	RPMLimit          int
	TPMLimit          int64
	SPMLimit          int
	SPMWindowSeconds  int
	ModelLimits       []byte
	Environment       core.EnvironmentValues
	HeaderPassthrough []string
	ParamOverride     []byte
	HeaderOverride    []byte
	LossPolicy        *string
	LastUsedAt        *time.Time
	CreatedBy         *int64
	CreatedByEmail    *string
	OwnerID           *int64
	OwnerEmail        *string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	// Last console test (CONTRACTS §50); all NULL until the first test.
	LastTestAt          *time.Time
	LastTestOK          *bool
	LastTestLatencyMs   *int64
	LastTestModel       *string
	LastTestMessage     *string
	LastTestStatus      *int
	LastTestErrorCode   *string
	LastTestErrorSource *string
	LastTestProtocol    *string
	// AutoDisabledSource says who wrote an automatic disable (0070):
	// usageLimitSource or '' (plugins and failure policies).
	AutoDisabledSource string
	// AdminLocked: disabled by a caller with the full scope who does not
	// own the account (migration 0097); see checkAdminLock.
	AdminLocked bool
	// Protocols (nil = all of the type's own platforms) and ModelProtocols
	// are the account's protocol binding (protocols.go).
	Protocols      []string
	ModelProtocols []byte
}

// rowColumns are the columns of a row, in scanRow's order. Every statement
// reading them joins the creator's and the owner's email (rowJoins, also for
// soft-deleted users) and must qualify account columns with "a.".
const rowColumns = `a.id, a.name, a.plugin_key, a.type, a.credentials_enc, a.settings, a.proxy_id,
	a.status, a.status_reason, a.schedulable, a.auto_disable, a.priority, a.weight, a.max_concurrency,
	a.models, a.model_mapping, a.rpm_limit, a.tpm_limit, a.spm_limit, a.spm_window_seconds, a.model_limits, a.environment, a.last_used_at,
	a.created_by, u.email, a.owner_id, o.email, a.created_at, a.updated_at,
	a.last_test_at, a.last_test_ok, a.last_test_latency_ms::bigint, a.last_test_model, a.last_test_message, a.auto_disabled_until,
	a.last_test_status,a.last_test_error_code,a.last_test_error_source,a.auto_disabled_source,a.header_passthrough,a.param_override,a.conversion_loss_policy,a.admin_locked,a.header_override,
	a.protocols, a.model_protocols, a.last_test_protocol`

// rowJoins join the users whose emails rowColumns read: u the creator, o
// the owner.
const rowJoins = "LEFT JOIN users u ON u.id = a.created_by LEFT JOIN users o ON o.id = a.owner_id"

// selectRows reads rowColumns, for scanRow over its Row().
func selectRows(db *gorm.DB) *gorm.DB {
	return db.Table("accounts a").Select(rowColumns).Joins(rowJoins)
}

// scanRow reads one row of rowColumns, from GORM's Row() or Rows() (a
// *sql.Row or *sql.Rows). The arrays and the environment go through store
// types: database/sql hands arrays over as text and jsonb as its document.
func scanRow(r interface{ Scan(dest ...any) error }) (*row, error) {
	var a row
	var env store.JSON[core.EnvironmentValues]
	err := r.Scan(&a.ID, &a.Name, &a.PluginKey, &a.Type, &a.CredEnc, &a.Settings, &a.ProxyID,
		&a.Status, &a.StatusReason, &a.Schedulable, &a.AutoDisable, &a.Priority, &a.Weight, &a.MaxConcurrency,
		(*store.Strings)(&a.Models), &a.ModelMapping, &a.RPMLimit, &a.TPMLimit, &a.SPMLimit, &a.SPMWindowSeconds, &a.ModelLimits, &env, &a.LastUsedAt,
		&a.CreatedBy, &a.CreatedByEmail, &a.OwnerID, &a.OwnerEmail, &a.CreatedAt, &a.UpdatedAt,
		&a.LastTestAt, &a.LastTestOK, &a.LastTestLatencyMs, &a.LastTestModel, &a.LastTestMessage, &a.AutoDisabledUntil,
		&a.LastTestStatus, &a.LastTestErrorCode, &a.LastTestErrorSource, &a.AutoDisabledSource, (*store.Strings)(&a.HeaderPassthrough), &a.ParamOverride, &a.LossPolicy, &a.AdminLocked, &a.HeaderOverride,
		(*store.Strings)(&a.Protocols), &a.ModelProtocols, &a.LastTestProtocol)
	a.Environment = env.V
	return &a, err
}

// lastTest is the last_test view of the row; nil when never tested.
func (a *row) lastTest() *LastTest {
	if a.LastTestAt == nil {
		return nil
	}
	lt := &LastTest{At: *a.LastTestAt, Status: a.LastTestStatus}
	if a.LastTestErrorCode != nil {
		lt.ErrorCode = *a.LastTestErrorCode
	}
	if a.LastTestErrorSource != nil {
		lt.ErrorSource = *a.LastTestErrorSource
	}
	if a.LastTestOK != nil {
		lt.OK = *a.LastTestOK
	}
	if a.LastTestLatencyMs != nil {
		lt.LatencyMs = *a.LastTestLatencyMs
	}
	if a.LastTestModel != nil {
		lt.Model = *a.LastTestModel
	}
	if a.LastTestMessage != nil {
		lt.Message = *a.LastTestMessage
	}
	if a.LastTestProtocol != nil {
		lt.Protocol = *a.LastTestProtocol
	}
	return lt
}

// mapping decodes the model_mapping column.
func (a *row) mapping() map[string]string {
	m := map[string]string{}
	if len(a.ModelMapping) > 0 {
		_ = json.Unmarshal(a.ModelMapping, &m)
	}
	return m
}

// testModels are the models a test that names none chooses from, in order:
// the account's model list, else the request models of its mapping in
// lexical order; none when it lists neither (the plugin picks its default,
// CONTRACTS §15.9).
func (a *row) testModels() []string {
	if len(a.Models) > 0 {
		return a.Models
	}
	return slices.Sorted(maps.Keys(a.mapping()))
}

func (a *row) modelLimits() []core.ModelLimit { return parseModelLimits(a.ModelLimits) }

// coreAccount is the account with the given plaintext credentials, as the
// gateway sees it.
func (a *row) coreAccount(plain []byte) core.Account {
	settings := a.Settings
	if len(settings) == 0 {
		settings = []byte("{}")
	}
	return core.Account{
		AccountRef: core.AccountRef{ID: a.ID, Name: a.Name, PluginKey: a.PluginKey, Type: a.Type,
			Priority: a.Priority, Weight: a.Weight, MaxConcurrency: a.MaxConcurrency, ProxyID: a.ProxyID,
			Models: a.Models, ModelMapping: a.mapping(), RPMLimit: a.RPMLimit, TPMLimit: a.TPMLimit, SPMLimit: a.SPMLimit, SPMWindowSeconds: a.SPMWindowSeconds, ModelLimits: a.modelLimits(),
			Environment: a.Environment, ConversionLossPolicy: core.ValueOr(a.LossPolicy, ""),
			ProtocolRules: a.protocolBinding().compile()},
		Status:            a.Status,
		Schedulable:       a.Schedulable,
		AutoDisable:       a.AutoDisable,
		Credentials:       json.RawMessage(plain),
		Settings:          json.RawMessage(settings),
		HeaderPassthrough: a.HeaderPassthrough,
		ParamOverride:     core.LoadParamOverride(a.ParamOverride),
		HeaderOverride:    core.LoadHeaderOverride(a.HeaderOverride),
	}
}

// pluginAccount is the account as a plugin sees it, with the given plaintext
// credentials and proxy, the one its requests of the call go out through
// (proxyEgress of the row's proxy_id; nil = direct).
func (a *row) pluginAccount(credentials []byte, proxy *core.ProxyRef) *pluginv1.Account {
	acc := a.coreAccount(credentials)
	return acc.PluginAccount("", proxy)
}

func notFound(ctx context.Context) error {
	return core.ErrNotFound.WithMessage(t(ctx, "account not found", "账号不存在"))
}

// scopeOf is the accounts the caller reaches with action (core.Scope,
// CONTRACTS §21). A scope that cannot be read is an error (503), never no
// accounts. Every scoped statement carries core.AccountRows() with it.
func (s *Service) scopeOf(ctx context.Context, action perm.Key) (core.DataScope, error) {
	return core.Scope(ctx, s.d.Authorizer, action)
}

// loadIn is loadRow of account id, unlocked, in the caller's scope of
// action.
func (s *Service) loadIn(ctx context.Context, id int64, action perm.Key) (*row, error) {
	scope, err := s.scopeOf(ctx, action)
	if err != nil {
		return nil, err
	}
	return s.loadRow(ctx, s.d.DB.Gorm, id, scope, false)
}

// inScope reports whether account id is in scope now, its groups as they
// are in q (a transaction's included).
func inScope(ctx context.Context, q *gorm.DB, id int64, scope core.DataScope) (bool, error) {
	if scope.All {
		return true, nil
	}
	db := store.G(ctx, q)
	return gormpg.Exists(db, scopedAccount(db, id, scope))
}

// scopedAccount is account id (live or not) if it is in scope.
func scopedAccount(db *gorm.DB, id int64, scope core.DataScope) *gorm.DB {
	return db.Table("accounts a").Where("a.id = ?", id).Scopes(core.AccountRows().Scope(scope))
}

// loadRow returns one live account in scope; rows outside the scope are
// 404 like missing ones.
func (s *Service) loadRow(ctx context.Context, q *gorm.DB, id int64, scope core.DataScope, lock bool) (*row, error) {
	db := selectRows(store.G(ctx, q)).Where("a.id = ? AND a.deleted_at IS NULL", id).Scopes(core.AccountRows().Scope(scope))
	if lock {
		if err := store.RequireTx(q); err != nil {
			return nil, err
		}
		db = db.Clauses(store.ForUpdate().Of("a")) // the accounts row (alias a) of the join
	}
	a, err := scanRow(db.Row())
	if store.IsNoRows(err) {
		return nil, notFound(ctx)
	}
	return a, err
}

// GroupRef names a group an account belongs to.
type GroupRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// View is the API representation of an account.
type View struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	PluginKey string `json:"plugin_key"`
	Type      string `json:"type"`
	// TypeLabel is the account type label; null when the type is no longer
	// registered (plugin disabled or uninstalled).
	TypeLabel    manifest.LocalizedText `json:"type_label"`
	GroupIDs     []int64                `json:"group_ids"`
	Groups       []GroupRef             `json:"groups"`
	ProxyID      *int64                 `json:"proxy_id"`
	Status       string                 `json:"status"`
	StatusReason string                 `json:"status_reason"`
	// AdminLocked: an administrator disabled the account; its owner cannot
	// enable it or reset its status until an administrator does.
	AdminLocked       bool       `json:"admin_locked"`
	AutoDisabledUntil *time.Time `json:"auto_disabled_until"`
	Schedulable       bool       `json:"schedulable"`
	// AutoDisable lets the gateway disable the account on credential or quota
	// failures; false only cools it down (CONTRACTS §42).
	AutoDisable    bool `json:"auto_disable"`
	Priority       int  `json:"priority"`
	Weight         int  `json:"weight"`
	MaxConcurrency int  `json:"max_concurrency"`
	// Models the account serves (empty = all) and the client → upstream
	// model mapping (CONTRACTS §18).
	Models       []string          `json:"models"`
	ModelMapping map[string]string `json:"model_mapping"`
	RPMLimit     int               `json:"rpm_limit"`
	TPMLimit     int64             `json:"tpm_limit"`
	SPMLimit     int               `json:"spm_limit"`
	// SPMWindowSeconds is the window spm_limit counts sessions over
	// (CONTRACTS §71.1).
	SPMWindowSeconds int               `json:"spm_window_seconds"`
	ModelLimits      []core.ModelLimit `json:"model_limits"`
	// Environment is the account layer of the environment variables (§74.3).
	Environment core.EnvironmentValues `json:"environment"`
	// HeaderPassthrough lists the client headers the account copies onto its
	// upstream requests (CONTRACTS §81).
	HeaderPassthrough []string `json:"header_passthrough"`
	// ParamOverride is the account's request parameter override (CONTRACTS
	// §84); null = none.
	ParamOverride json.RawMessage `json:"param_override"`
	// HeaderOverride is the account's request header override (CONTRACTS
	// §108); null = none.
	HeaderOverride json.RawMessage `json:"header_override"`
	// Protocols are the inbound protocols of the type's own platforms the
	// account serves, null = all of them; ModelProtocols the protocols each
	// listed model is served on, conversions included (protocols.go).
	Protocols      []string                 `json:"protocols"`
	ModelProtocols []core.ModelProtocolRule `json:"model_protocols"`
	// ServedProtocols is what the account serves now, derived from the two
	// and its type; only present on single-account responses.
	ServedProtocols []ServedProtocol `json:"served_protocols,omitzero"`
	// ConversionLossPolicy is the account's conversion loss policy (§88);
	// null inherits the group's.
	ConversionLossPolicy *string        `json:"conversion_loss_policy"`
	RateUsage            core.RateUsage `json:"rate_usage"`
	// Outcomes are the successful requests and failed attempts of the last 60
	// seconds behind the success-rate column (CONTRACTS §73); null when the
	// statistics are unavailable.
	Outcomes *Outcomes `json:"outcomes"`
	// Usage is the core's usage record of the account and its usage limit
	// (CONTRACTS §73); null when it cannot be read.
	Usage *UsageView `json:"usage"`
	InUse int        `json:"in_use"`
	// Waiting counts the requests queueing for a free slot of the account.
	Waiting        int        `json:"waiting"`
	CooldownUntil  *time.Time `json:"cooldown_until"`
	CooldownReason string     `json:"cooldown_reason,omitempty"`
	// Quota is the subscription quota snapshot (CONTRACTS §44), read from
	// the database only; null for account types without quota (API keys).
	Quota *QuotaSnapshot `json:"quota"`
	// Balance is the account balance snapshot (CONTRACTS §52); absent for
	// account types without balance support.
	Balance *BalanceView `json:"balance,omitempty"`
	// Refresh is the credential refresh state (CONTRACTS §48); null for
	// account types whose credentials do not expire.
	Refresh *RefreshView `json:"refresh"`
	// limitDisabled: the usage limit auto-disabled the account (Usage.LimitDisabled).
	limitDisabled bool
	// LastTest is the last console test (CONTRACTS §50); null until tested.
	LastTest *LastTest `json:"last_test"`
	// Orphaned is true when the plugin declaring the account type is not
	// enabled (disabled or uninstalled).
	Orphaned bool            `json:"orphaned"`
	Settings json.RawMessage `json:"settings"`
	// Credentials (settings + secret fields, sensitive ones masked) is only
	// present on single-account responses.
	Credentials json.RawMessage `json:"credentials,omitempty"`
	// CreatedBy is the creator (null for rows created before ownership
	// existed or by the system); CreatedByEmail is kept for soft-deleted users.
	CreatedBy      *int64  `json:"created_by"`
	CreatedByEmail *string `json:"created_by_email"`
	// OwnerID is the account's 所属, whose own and team scopes reach it
	// (CONTRACTS §103): the creator unless an administrator moved it; null
	// as created_by. OwnerEmail is kept for soft-deleted users.
	OwnerID    *int64  `json:"owner_id"`
	OwnerEmail *string `json:"owner_email"`
	// ProxyCreated is only present on the create/update responses: whether
	// proxy_url led to a new proxy row (CONTRACTS §21.4).
	ProxyCreated *bool      `json:"proxy_created,omitempty"`
	LastUsedAt   *time.Time `json:"last_used_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// views builds list views, filling groups, in_use, cooldown and orphaned.
func (s *Service) views(ctx context.Context, rows []*row) ([]*View, error) {
	out := make([]*View, len(rows))
	ids := make([]int64, len(rows))
	byID := map[int64]*View{}
	for i, a := range rows {
		st := a.Settings
		if len(st) == 0 {
			st = json.RawMessage("{}")
		}
		models := a.Models
		if models == nil {
			models = []string{}
		}
		passthrough := a.HeaderPassthrough
		if passthrough == nil {
			passthrough = []string{}
		}
		v := &View{
			ID: a.ID, Name: a.Name, PluginKey: a.PluginKey, Type: a.Type, TypeLabel: s.typeLabel(a.PluginKey, a.Type),
			GroupIDs: []int64{}, Groups: []GroupRef{}, ProxyID: a.ProxyID, Status: a.Status,
			StatusReason: a.StatusReason, AdminLocked: a.AdminLocked, AutoDisabledUntil: a.AutoDisabledUntil, Schedulable: a.Schedulable, AutoDisable: a.AutoDisable, Priority: a.Priority, Weight: a.Weight,
			MaxConcurrency: a.MaxConcurrency, Models: models, ModelMapping: a.mapping(),
			RPMLimit: a.RPMLimit, TPMLimit: a.TPMLimit, SPMLimit: a.SPMLimit, SPMWindowSeconds: a.SPMWindowSeconds, ModelLimits: a.modelLimits(), Environment: a.Environment,
			HeaderPassthrough: passthrough, ParamOverride: a.ParamOverride, HeaderOverride: a.HeaderOverride, ConversionLossPolicy: a.LossPolicy,
			Protocols: a.Protocols, ModelProtocols: parseModelProtocols(a.ModelProtocols),
			Orphaned: !s.pluginActive(a.PluginKey), Settings: st,
			CreatedBy: a.CreatedBy, CreatedByEmail: a.CreatedByEmail, OwnerID: a.OwnerID, OwnerEmail: a.OwnerEmail,
			LastUsedAt: a.LastUsedAt, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
			LastTest:      a.lastTest(),
			limitDisabled: limitDisabled(a.Status, a.AutoDisabledSource),
		}
		out[i], ids[i], byID[a.ID] = v, a.ID, v
	}
	if len(rows) == 0 {
		return out, nil
	}
	// Every read below fills its own View fields from read-only inputs (rows,
	// ids, byID and the fields set above), so they run concurrently: the page
	// waits for the slowest read instead of the sum of all round trips. Only
	// the group read fails the request; the others log and leave their
	// column empty.
	var groupsErr error
	tasks := []func(){
		func() { s.fillCooldowns(ctx, ids, out) },
		func() {
			if s.d.Slots == nil {
				return
			}
			inUse, err := s.d.Slots.InUseMany(ctx, "account", ids)
			if err != nil {
				slog.WarnContext(ctx, "account: read slots", "err", err)
				return
			}
			waiting, err := s.d.Slots.InUseMany(ctx, core.AccountWaitSlots, ids)
			if err != nil {
				slog.WarnContext(ctx, "account: read queue", "err", err)
			}
			for _, v := range out {
				v.InUse = inUse[v.ID]
				v.Waiting = waiting[v.ID]
			}
		},
		// fillModelInUse writes into RateUsage.Models: same task, after it.
		func() {
			s.fillRateUsage(ctx, out)
			s.fillModelInUse(ctx, out)
		},
		func() { s.fillOutcomes(ctx, out) },
		func() { s.fillQuota(ctx, rows, out) },
		func() { s.fillBalance(ctx, rows, out) },
	}
	// The database-bound reads share a bound of their own (viewsPGParallel
	// connections per page; quota and balance read PG only on a cache miss).
	pgTasks := []func(){
		func() {
			groupsErr = errViewPanicked
			groupsErr = s.fillGroups(ctx, ids, byID)
		},
		func() { s.fillUsage(ctx, out) },
		func() { s.fillRefresh(ctx, rows, out) },
	}
	tasks = append(tasks, func() { runParallel(ctx, viewsPGParallel, pgTasks...) })
	runParallel(ctx, 0, tasks...)
	if groupsErr != nil {
		return nil, groupsErr
	}
	return out, nil
}

// errViewPanicked is the error of a list read that panicked (the panic is
// recovered and logged by runParallel).
var errViewPanicked = errors.New("account: list read failed")

// fillGroups sets GroupIDs and Groups (ordered by group id).
func (s *Service) fillGroups(ctx context.Context, ids []int64, byID map[int64]*View) error {
	var refs []accountGroupRef
	if err := store.G(ctx, s.d.DB.Gorm).Table("account_groups ag").Select("ag.account_id, g.id, g.name").
		Joins("JOIN groups g ON g.id = ag.group_id").Where("ag.account_id = ANY(?)", store.Int64s(ids)).
		Order("g.id").Scan(&refs).Error; err != nil {
		return err
	}
	for _, r := range refs {
		v := byID[r.AccountID]
		v.GroupIDs = append(v.GroupIDs, r.ID)
		v.Groups = append(v.Groups, GroupRef{ID: r.ID, Name: r.Name})
	}
	return nil
}

// accountGroupRef is one group of an account, as fillGroups reads it.
type accountGroupRef struct {
	AccountID int64
	ID        int64
	Name      string
}

// fillCooldowns sets CooldownUntil and CooldownReason from Redis.
func (s *Service) fillCooldowns(ctx context.Context, ids []int64, out []*View) {
	if s.d.Redis == nil {
		return
	}
	now := time.Now().UTC()
	pipe := s.d.Redis.Pipeline()
	gets := make([]*redis.StringCmd, len(ids))
	ttls := make([]*redis.DurationCmd, len(ids))
	for i, id := range ids {
		gets[i] = pipe.Get(ctx, cooldownKey(id))
		ttls[i] = pipe.PTTL(ctx, cooldownKey(id))
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		slog.WarnContext(ctx, "account: read cooldowns", "err", err)
	}
	for i, v := range out {
		if reason, err := gets[i].Result(); err == nil {
			if ttl, err := ttls[i].Result(); err == nil && ttl > 0 {
				until := now.Add(ttl).Truncate(time.Second)
				v.CooldownUntil, v.CooldownReason = &until, reason
			}
		}
	}
}

// fillRateUsage sets RateUsage (rpm/tpm of the last minute, sessions of
// each session window).
func (s *Service) fillRateUsage(ctx context.Context, out []*View) {
	if s.d.Limiter == nil {
		return
	}
	refs := make([]core.AccountRef, len(out))
	for i, v := range out {
		refs[i] = core.AccountRef{ID: v.ID, SPMWindowSeconds: v.SPMWindowSeconds, ModelLimits: v.ModelLimits}
	}
	usage, err := s.d.Limiter.Usage(ctx, refs)
	if err != nil {
		slog.WarnContext(ctx, "account: read rate usage", "err", err)
	}
	for _, v := range out {
		v.RateUsage = usage[v.ID]
	}
}

// credentialView returns the merged credentials with sensitive values masked.
func (s *Service) credentialView(a *row) (json.RawMessage, error) {
	plain, err := s.decrypt(a.PluginKey, a.CredEnc)
	if err != nil {
		return nil, fmt.Errorf("decrypt account %d: %w", a.ID, err)
	}
	bt, ok := s.accountType(a.PluginKey, a.Type)
	if !ok {
		sec, err := parseObject(plain)
		if err != nil {
			return nil, err
		}
		all, err := merge(mustJSON(maskAll(sec)), a.Settings)
		if err != nil {
			return nil, err
		}
		return mustJSON(all), nil
	}
	all, err := merge(plain, a.Settings)
	if err != nil {
		return nil, err
	}
	return mask(mustJSON(all), bt.Type.SensitiveFields), nil
}

// fullView is the single-account view (with credentials) of an account
// visible in scope.
func (s *Service) fullView(ctx context.Context, id int64, scope core.DataScope) (*View, error) {
	a, err := s.loadRow(ctx, s.d.DB.Gorm, id, scope, false)
	if err != nil {
		return nil, err
	}
	vs, err := s.views(ctx, []*row{a})
	if err != nil {
		return nil, err
	}
	if vs[0].Credentials, err = s.credentialView(a); err != nil {
		return nil, err
	}
	vs[0].ServedProtocols = s.accountServedProtocols(a)
	return vs[0], nil
}

// ---------------------------------------------------------------- list / get

func (s *Service) list(c *gin.Context) {
	ctx := c.Request.Context()
	page, size := httpapi.Pagination(c)
	var conds store.Conds
	conds.And("a.deleted_at IS NULL")
	// Visible range: the caller's account:read scope (CONTRACTS §21, its
	// team §103); within it mine=true narrows to the caller's own accounts,
	// subordinates=true to those of the users below it (none without a
	// team scope), owner_id=<id> and created_by=<id> to that owner's and
	// that creator's.
	scope, err := s.scopeOf(ctx, perm.AccountRead)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	conds = append(conds, core.AccountRows().Scope(scope))
	uid, _ := core.UserID(ctx)
	if mine, _ := strconv.ParseBool(c.Query("mine")); mine {
		conds.And("a.owner_id = ?", uid)
	}
	if sub, _ := strconv.ParseBool(c.Query("subordinates")); sub {
		conds.And("a.owner_id IN (?)", core.UserTree().Below(store.G(ctx, s.d.DB.Gorm), scope.Teams...))
	}
	createdBy, ok := userIDQuery(c, "created_by")
	if !ok {
		return
	}
	if createdBy != 0 {
		conds.And("a.created_by = ?", createdBy)
	}
	ownerID, ok := userIDQuery(c, "owner_id")
	if !ok {
		return
	}
	if ownerID != 0 {
		conds.And("a.owner_id = ?", ownerID)
	}
	// Accounts of a disabled or uninstalled plugin are hidden by default
	// (their rows stay); orphaned=true lists only them, orphaned=all both.
	if keys := s.activePluginKeys(); keys != nil {
		switch c.Query("orphaned") {
		case "true":
			conds.And("NOT (a.plugin_key = ANY(?))", store.Strings(keys))
		case "all":
		default:
			conds.And("a.plugin_key = ANY(?)", store.Strings(keys))
		}
	}
	// An account type is identified by (plugin key, type id): two plugins
	// may both declare "apikey".
	if c.Query("type") != "" && c.Query("plugin_key") == "" {
		httpapi.Fail(c, core.InvalidFields(core.FieldError{Field: "type", Code: "requires_plugin_key",
			Message: "type filters by (plugin_key, type); pass plugin_key too"}))
		return
	}
	if v := c.Query("plugin_key"); v != "" {
		conds.And("a.plugin_key = ?", v)
	}
	if v := c.Query("type"); v != "" {
		conds.And("a.type = ?", v)
	}
	if v := c.Query("status"); v != "" {
		switch v {
		case "active":
			conds.And("(a.status = ? AND a.schedulable)", v)
		case "auto_disabled": // the console's three statuses: an active account out of scheduling reads as auto-disabled
			conds.And("(a.status = ? OR (a.status = 'active' AND NOT a.schedulable))", v)
		default:
			conds.And("a.status = ?", v)
		}
	}
	if v := c.Query("group_id"); v != "" {
		gid, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			httpapi.Fail(c, core.ErrInvalidArgument.WithMessage("invalid group_id"))
			return
		}
		conds.And("EXISTS (SELECT 1 FROM account_groups ag WHERE ag.account_id = a.id AND ag.group_id = ?)", gid)
	}
	// ids=1,2,3 narrows to the given accounts (a plugin toolbar slot hands
	// the console a set to show); at most as many as one bulk request takes.
	if v := c.Query("ids"); v != "" {
		parts := strings.Split(v, ",")
		if len(parts) > manifest.MaxAccountBatch {
			httpapi.Fail(c, core.ErrInvalidArgument.WithMessage(fmt.Sprintf("ids accepts at most %d account ids", manifest.MaxAccountBatch)))
			return
		}
		ids := make([]int64, 0, len(parts))
		for _, p := range parts {
			id, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64)
			if err != nil {
				httpapi.Fail(c, core.ErrInvalidArgument.WithMessage("ids must be comma-separated account ids"))
				return
			}
			ids = append(ids, id)
		}
		conds.And("a.id = ANY(?)", store.Int64s(ids))
	}
	if v := strings.TrimSpace(c.Query("q")); v != "" {
		conds.And("a.name ILIKE '%' || ? || '%'", v)
	}
	if v := strings.TrimSpace(c.Query("model")); v != "" {
		conds.And("(a.models = '{}' OR ? = ANY(a.models))", v)
	}
	total, rows, err := s.listPage(ctx, conds, page, size)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	vs, err := s.views(ctx, rows)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.List(c, vs, httpapi.Page{Page: page, PageSize: size, Total: total})
}

// userIDQuery reads the user id of query parameter name, 0 when absent; a
// value that is no user id fails the request (false).
func userIDQuery(c *gin.Context, name string) (int64, bool) {
	v := c.Query(name)
	if v == "" {
		return 0, true
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil || id <= 0 {
		ctx := c.Request.Context()
		httpapi.Fail(c, core.InvalidFields(core.FieldError{Field: name, Code: "invalid",
			Message: t(ctx, name+" must be a user id", name+" 必须是用户 ID")}))
		return 0, false
	}
	return id, true
}

// listRowColumns are rowColumns without the encrypted credentials, which no
// list column needs: CredEnc stays nil on rows read with them, so such rows
// must not be decrypted or written back (fullView and every write path use
// loadRow).
var listRowColumns = strings.Replace(rowColumns, "a.credentials_enc", "NULL::bytea", 1)

// listPage reads the total and one page of the accounts conds keep, in one
// statement: each row of the page carries the total (listRows). A page past
// the end has no row to carry it, so only then is it counted on its own (on
// the first page no row means none). Newest accounts come first; editing
// priority or weight never moves a row, so the console's auto-refresh
// keeps every account in place.
func (s *Service) listPage(ctx context.Context, conds store.Conds, page, size int) (int64, []*row, error) {
	db := store.G(ctx, s.d.DB.Gorm)
	var total int64
	rows, err := store.CollectRows(listRows(db, conds, page, size), func(rs *sql.Rows) (*row, error) {
		return scanRow(withTotal{rs, &total})
	})
	if err != nil {
		return 0, nil, err
	}
	if len(rows) == 0 && page > 1 {
		err = listCount(db, conds).Count(&total).Error
	}
	return total, rows, err
}

// withTotal scans a row of listRows: scanRow's columns, then the total.
type withTotal struct {
	rs    *sql.Rows
	total *int64
}

func (w withTotal) Scan(dest ...any) error { return w.rs.Scan(append(dest, w.total)...) }

// listCount counts the accounts conds keep.
func listCount(db *gorm.DB, conds store.Conds) *gorm.DB {
	return db.Table("accounts a").Scopes(conds.Scope())
}

// listRows selects one page of the accounts conds keep, in listRowColumns,
// each row followed by the number of accounts conds keep (listCount as a
// subquery, which PostgreSQL runs once, in the page's snapshot).
func listRows(db *gorm.DB, conds store.Conds, page, size int) *gorm.DB {
	total := listCount(db.Session(&gorm.Session{NewDB: true}), conds).Select("count(*)")
	return db.Table("accounts a").Select(listRowColumns+", (?)", total).Joins(rowJoins).
		Scopes(conds.Scope()).Order("a.created_at DESC, a.id DESC").Limit(size).Offset((page - 1) * size)
}

func (s *Service) get(c *gin.Context) {
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	ctx := c.Request.Context()
	scope, err := s.scopeOf(ctx, perm.AccountRead)
	var v *View
	if err == nil {
		v, err = s.fullView(ctx, id, scope)
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, v)
}

// ---------------------------------------------------------------- create / update

// nullable distinguishes an absent JSON field from an explicit null.
type nullable[T any] struct {
	Set   bool
	Valid bool
	V     T
}

func (n *nullable[T]) UnmarshalJSON(b []byte) error {
	n.Set = true
	if string(b) == "null" {
		n.Valid = false
		return nil
	}
	n.Valid = true
	return json.Unmarshal(b, &n.V)
}

type input struct {
	Name      *string         `json:"name"`
	PluginKey string          `json:"plugin_key"`
	Type      string          `json:"type"`
	GroupIDs  *[]int64        `json:"group_ids"`
	ProxyID   nullable[int64] `json:"proxy_id"`
	// ProxyURL names a proxy by address instead of id (CONTRACTS §21.4); it
	// is looked up or created when the account is saved. Blank means absent.
	ProxyURL       *string            `json:"proxy_url"`
	Priority       *int               `json:"priority"`
	Weight         *int               `json:"weight"`
	MaxConcurrency *int               `json:"max_concurrency"`
	Schedulable    *bool              `json:"schedulable"`
	AutoDisable    *bool              `json:"auto_disable"`
	Status         *string            `json:"status"`
	Models         *[]string          `json:"models"`
	ModelMapping   *map[string]string `json:"model_mapping"`
	RPMLimit       *int               `json:"rpm_limit"`
	TPMLimit       *int64             `json:"tpm_limit"`
	SPMLimit       *int               `json:"spm_limit"`
	// SPMWindowSeconds is the window of spm_limit, 60-86400 (§71.1).
	SPMWindowSeconds *int               `json:"spm_window_seconds"`
	ModelLimits      *[]core.ModelLimit `json:"model_limits"`
	// Environment replaces the account layer as a whole (§74.3).
	Environment *core.EnvironmentValues `json:"environment"`
	// HeaderPassthrough replaces the account's header passthrough rules (§81).
	HeaderPassthrough *[]string `json:"header_passthrough"`
	// ParamOverride replaces the request parameter override (§84); null or
	// {} clears it, absent keeps it.
	ParamOverride json.RawMessage `json:"param_override"`
	// HeaderOverride replaces the request header override (§108); null or
	// {} clears it, absent keeps it.
	HeaderOverride json.RawMessage `json:"header_override"`
	// Protocols replaces the protocols of the type's own platforms the
	// account serves: null opens all of them, [] none, absent keeps them.
	Protocols nullable[[]string] `json:"protocols"`
	// ModelProtocols replaces the protocols of listed models as a whole;
	// absent keeps them.
	ModelProtocols *[]core.ModelProtocolRule `json:"model_protocols"`
	// ConversionLossPolicy sets (allow, safe, strict) or, with null, clears
	// the conversion loss policy (§88); absent keeps it.
	ConversionLossPolicy core.LossPolicyInput `json:"conversion_loss_policy"`
	Credentials          json.RawMessage      `json:"credentials"`
	Settings             json.RawMessage      `json:"settings"`
}

// hasCredentials reports whether the request carries a credentials object
// (null counts as absent on PATCH).
func (in *input) hasCredentials() bool {
	creds := strings.TrimSpace(string(in.Credentials))
	return creds != "" && creds != "null"
}

// overrideColumn is the column value of a validated override field
// (param_override, header_override): nil (NULL) when it is absent or cleared.
func overrideColumn(raw json.RawMessage) *string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	s := string(raw)
	return &s
}

// columns are the account columns a PATCH writes: the fields present (false
// and 0 included) and what the update derived from them (the settings, the
// status reason, the JSON documents, admin_locked), with updated_at. A
// present list that is nil keeps the column. proxy_id and credentials_enc
// are the caller's to add.
func (in *input) columns(settings, reason, mapping, limits *string, adminLocked *bool) map[string]any {
	set := map[string]any{"updated_at": gorm.Expr("clock_timestamp()")}
	store.SetIf(set, "name", in.Name)
	store.SetIf(set, "priority", in.Priority)
	store.SetIf(set, "max_concurrency", in.MaxConcurrency)
	store.SetIf(set, "schedulable", in.Schedulable)
	if in.Status != nil {
		set["status"], set["auto_disabled_until"] = *in.Status, nil
	}
	store.SetIf(set, "status_reason", reason)
	store.SetIf(set, "settings", settings)
	store.SetIf(set, "weight", in.Weight)
	setList(set, "models", in.Models)
	store.SetIf(set, "model_mapping", mapping)
	store.SetIf(set, "rpm_limit", in.RPMLimit)
	store.SetIf(set, "tpm_limit", in.TPMLimit)
	store.SetIf(set, "auto_disable", in.AutoDisable)
	store.SetIf(set, "model_limits", limits)
	store.SetIf(set, "spm_limit", in.SPMLimit)
	if in.Environment != nil && *in.Environment != nil {
		set["environment"] = store.JSON[core.EnvironmentValues]{V: *in.Environment}
	}
	setList(set, "header_passthrough", in.HeaderPassthrough)
	if len(in.ParamOverride) > 0 {
		set["param_override"] = overrideColumn(in.ParamOverride)
	}
	if len(in.HeaderOverride) > 0 {
		set["header_override"] = overrideColumn(in.HeaderOverride)
	}
	if in.ConversionLossPolicy.Set {
		set["conversion_loss_policy"] = in.ConversionLossPolicy.Stored()
	}
	if in.Protocols.Set {
		set["protocols"] = store.NullStrings(in.Protocols.V)
	}
	if in.ModelProtocols != nil {
		set["model_protocols"] = modelProtocolsJSON(*in.ModelProtocols)
	}
	store.SetIf(set, "spm_window_seconds", in.SPMWindowSeconds)
	store.SetIf(set, "admin_locked", adminLocked)
	return set
}

// setList puts the text[] *v into set unless v or *v is nil.
func setList(set map[string]any, column string, v *[]string) {
	if v != nil && *v != nil {
		set[column] = store.Strings(*v)
	}
}

// changedFields lists the request fields present in a PATCH for the audit
// detail (names only, never values; credentials is one name).
func (in *input) changedFields() []string {
	out := []string{}
	set := func(name string, present bool) {
		if present {
			out = append(out, name)
		}
	}
	set("name", in.Name != nil)
	set("group_ids", in.GroupIDs != nil)
	set("proxy_id", in.ProxyID.Set)
	set("proxy_url", in.ProxyURL != nil)
	set("priority", in.Priority != nil)
	set("weight", in.Weight != nil)
	set("max_concurrency", in.MaxConcurrency != nil)
	set("schedulable", in.Schedulable != nil)
	set("auto_disable", in.AutoDisable != nil)
	set("status", in.Status != nil)
	set("models", in.Models != nil)
	set("model_mapping", in.ModelMapping != nil)
	set("rpm_limit", in.RPMLimit != nil)
	set("tpm_limit", in.TPMLimit != nil)
	set("spm_limit", in.SPMLimit != nil)
	set("spm_window_seconds", in.SPMWindowSeconds != nil)
	set("model_limits", in.ModelLimits != nil)
	set("environment", in.Environment != nil)
	set("header_passthrough", in.HeaderPassthrough != nil)
	set("param_override", len(in.ParamOverride) > 0)
	set("header_override", len(in.HeaderOverride) > 0)
	set("conversion_loss_policy", in.ConversionLossPolicy.Set)
	set("protocols", in.Protocols.Set)
	set("model_protocols", in.ModelProtocols != nil)
	set("credentials", in.hasCredentials())
	set("settings", len(in.Settings) > 0)
	return out
}

const (
	maxModels      = manifest.MaxAccountModels
	maxRPMLimit    = manifest.MaxAccountRateLimit
	maxTokenLimit  = manifest.MaxAccountTokenLimit
	maxWeight      = manifest.MaxAccountWeight
	maxPriority    = manifest.MaxAccountPriority
	maxConcurrency = manifest.MaxAccountConcurrency
	// ownMaxConcurrency is the cap an own-level user may set (H3.2) and the
	// default of their new accounts; other new accounts default to unlimited.
	ownMaxConcurrency     = 1000
	ownMaxPriority        = 100
	ownMaxWeight          = 100
	ownDefaultConcurrency = 10
)

// defaultConcurrency is the max concurrency of a new account that does not
// set one: unlimited (0), or ownDefaultConcurrency for an own-level caller,
// who cannot set unlimited.
func defaultConcurrency(scope core.DataScope) int {
	if scope.Restricted() {
		return ownDefaultConcurrency
	}
	return 0
}

func (in *input) validate(ctx context.Context, create bool) []core.FieldError {
	var fe []core.FieldError
	add := func(field, code, en, zh string) {
		fe = append(fe, core.FieldError{Field: field, Code: code, Message: t(ctx, en, zh)})
	}
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		in.Name = &n
		if n == "" || utf8.RuneCountInString(n) > 100 {
			add("name", "invalid", "name must be 1-100 characters", "名称长度须为 1-100 个字符")
		}
	} else if create {
		add("name", "required", "name is required", "名称必填")
	}
	if create && (in.PluginKey == "" || in.Type == "") {
		add("type", "required", "plugin_key and type are required", "账号类型（plugin_key 和 type）必填")
	}
	fe = append(fe, in.validateScheduling(ctx)...)
	if in.Models != nil {
		models, errs := normalizeModels(ctx, *in.Models)
		fe = append(fe, errs...)
		in.Models = &models
	}
	if in.ModelMapping != nil {
		m, errs := normalizeMapping(ctx, *in.ModelMapping)
		fe = append(fe, errs...)
		in.ModelMapping = &m
	}
	if in.Status != nil && *in.Status != "active" && *in.Status != "disabled" {
		add("status", "invalid", "status must be active or disabled", "状态必须为 active 或 disabled")
	}
	if in.ProxyID.Valid && in.ProxyID.V <= 0 {
		add("proxy_id", "invalid", "invalid proxy", "代理无效")
	}
	if in.ProxyURL != nil {
		// A blank proxy_url is the same as not sending it.
		u := strings.TrimSpace(*in.ProxyURL)
		if u == "" {
			in.ProxyURL = nil
		} else {
			in.ProxyURL = &u
			if in.ProxyID.Set {
				add("proxy_url", "conflict", "proxy_url and proxy_id cannot both be given", "proxy_url 与 proxy_id 不能同时给出")
			}
		}
	}
	if create && len(in.Credentials) == 0 {
		add("credentials", "required", "credentials are required", "凭证必填")
	}
	return fe
}

// validateScheduling checks the scheduling and limit fields present in the
// input (CONTRACTS §68).
func (in *input) validateScheduling(ctx context.Context) []core.FieldError {
	var fe []core.FieldError
	add := func(field, code, en, zh string) {
		fe = append(fe, core.FieldError{Field: field, Code: code, Message: t(ctx, en, zh)})
	}
	if in.Priority != nil && (*in.Priority < 0 || *in.Priority > maxPriority) {
		add("priority", "invalid", "priority must be 0-1000000", "优先级必须为 0-1000000")
	}
	if in.Weight != nil && (*in.Weight < 1 || *in.Weight > maxWeight) {
		add("weight", "invalid", "weight must be 1-1000", "权重必须为 1-1000")
	}
	if in.MaxConcurrency != nil && (*in.MaxConcurrency < 0 || *in.MaxConcurrency > maxConcurrency) {
		add("max_concurrency", "invalid", "max concurrency must be 0-100000 (0 = unlimited)", "最大并发必须为 0-100000（0 表示不限）")
	}
	if in.RPMLimit != nil && (*in.RPMLimit < 0 || *in.RPMLimit > maxRPMLimit) {
		add("rpm_limit", "invalid", "rpm limit must be 0-10000000 (0 = unlimited)", "每分钟请求数上限必须为 0-10000000（0 表示不限）")
	}
	if in.TPMLimit != nil && (*in.TPMLimit < 0 || *in.TPMLimit > maxTokenLimit) {
		add("tpm_limit", "invalid", "tpm limit must be 0-1000000000000 (0 = unlimited)", "每分钟 token 上限必须为 0-1000000000000（0 表示不限）")
	}
	if in.SPMLimit != nil && (*in.SPMLimit < 0 || *in.SPMLimit > maxRPMLimit) {
		add("spm_limit", "invalid", spmLimitEN, spmLimitZH)
	}
	if in.SPMWindowSeconds != nil && !validSPMWindow(*in.SPMWindowSeconds) {
		add("spm_window_seconds", "invalid", spmWindowEN, spmWindowZH)
	}
	if in.Environment != nil {
		values, errs := core.ValidateEnvironmentValues(ctx, core.EnvLayerAccount, "environment", *in.Environment)
		fe = append(fe, errs...)
		in.Environment = &values
	}
	if in.HeaderPassthrough != nil {
		rules, errs := core.NormalizeHeaderPassthrough(ctx, "header_passthrough", *in.HeaderPassthrough)
		fe = append(fe, errs...)
		in.HeaderPassthrough = &rules
	}
	if len(in.ParamOverride) > 0 {
		p, errs := core.CompileParamOverride(ctx, "param_override", in.ParamOverride)
		fe = append(fe, errs...)
		in.ParamOverride = p.Raw()
	}
	if len(in.HeaderOverride) > 0 {
		m, errs := core.CompileHeaderOverride(ctx, "header_override", in.HeaderOverride)
		fe = append(fe, errs...)
		in.HeaderOverride = m.Raw()
	}
	fe = append(fe, in.ConversionLossPolicy.Validate(ctx, "conversion_loss_policy")...)
	if in.ModelLimits != nil {
		limits, errs := normalizeModelLimits(ctx, *in.ModelLimits)
		fe = append(fe, errs...)
		in.ModelLimits = &limits
	}
	return fe
}

// normalizeModels trims and de-duplicates the model list; every entry must
// be a complete model id (no wildcards, CONTRACTS §16/§18).
func normalizeModels(ctx context.Context, in []string) ([]string, []core.FieldError) {
	var fe []core.FieldError
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for i, m := range in {
		m = strings.TrimSpace(m)
		field := "models[" + strconv.Itoa(i) + "]"
		switch {
		case !manifest.ValidModelID(m):
			fe = append(fe, core.FieldError{Field: field, Code: "invalid",
				Message: t(ctx, "not a complete model id (wildcards are not allowed)", "不是完整的模型 ID（不允许通配符）")})
		case seen[m]:
			fe = append(fe, core.FieldError{Field: field, Code: "duplicate",
				Message: t(ctx, "duplicate model", "模型重复")})
		default:
			seen[m] = true
			out = append(out, m)
		}
	}
	if len(out) > maxModels {
		fe = append(fe, core.FieldError{Field: "models", Code: "too_many",
			Message: t(ctx, "at most 500 models", "最多 500 个模型")})
	}
	return out, fe
}

// normalizeMapping validates the client → upstream model mapping.
func normalizeMapping(ctx context.Context, in map[string]string) (map[string]string, []core.FieldError) {
	var fe []core.FieldError
	out := make(map[string]string, len(in))
	for from, to := range in {
		from, to = strings.TrimSpace(from), strings.TrimSpace(to)
		if !manifest.ValidModelID(from) || !manifest.ValidModelID(to) {
			fe = append(fe, core.FieldError{Field: "model_mapping." + from, Code: "invalid",
				Message: t(ctx, "both models must be complete model ids (wildcards are not allowed)", "两边都必须是完整的模型 ID（不允许通配符）")})
			continue
		}
		out[from] = to
	}
	if len(out) > maxModels {
		fe = append(fe, core.FieldError{Field: "model_mapping", Code: "too_many",
			Message: t(ctx, "at most 500 mappings", "最多 500 条映射")})
	}
	return out, fe
}

// checkProxyRef verifies that proxyID (nil: none) is a proxy the caller
// writing an account under action may reference (core.ProxyRefScope,
// §21.4). A proxy outside that range is reported as not found, like a
// missing one; a range that cannot be read fails the write (503), never
// reaching no proxy (CONTRACTS §103.2).
func (s *Service) checkProxyRef(ctx context.Context, proxyID *int64, action perm.Key) error {
	if proxyID == nil {
		return nil
	}
	refs, err := core.ProxyRefScope(ctx, s.d.Authorizer, action)
	if err != nil {
		return err
	}
	var ok bool
	err = store.G(ctx, s.d.DB.Gorm).Table("proxies p").Select("true").Where("p.id = ?", *proxyID).
		Scopes(core.ProxyRows().Scope(refs)).Take(&ok).Error
	if store.IsNoRows(err) {
		return core.InvalidFields(core.FieldError{Field: "proxy_id", Code: "not_found",
			Message: t(ctx, "proxy not found", "代理不存在")})
	}
	return err
}

// groupDiff is how a write changes an account's groups.
type groupDiff struct{ added, removed, unbindable []int64 }

// groupBinding is one group loadGroupDiff reads: whether the account is in
// it, and whether the caller may bind it.
type groupBinding struct {
	ID       int64
	Bound    bool
	Bindable bool
}

func (d groupDiff) changed() bool { return len(d.added)+len(d.removed) > 0 }

// outside is d without the groups scope reaches as a whole (group/G,
// group/*): a group-range caller binds and unbinds those without
// account:group:bind (CONTRACTS §21.0); whether the account stays in
// range is checked after the write (stillInScope).
func (d groupDiff) outside(scope core.DataScope) groupDiff {
	keep := func(ids []int64) []int64 { return slices.DeleteFunc(slices.Clone(ids), scope.HasGroup) }
	return groupDiff{added: keep(d.added), removed: keep(d.removed), unbindable: keep(d.unbindable)}
}

// errNotInScope: a restricted caller creates an account outside its
// account:create scope (no own part, and none of its groups).
func errNotInScope(ctx context.Context) error {
	return core.InvalidFields(core.FieldError{Field: "group_ids", Code: "not_in_scope",
		Message: t(ctx, "bind the account to a group you manage", "账号必须绑定到你管理的分组")})
}

// createInScope fails unless a new account bound to groupIDs is in scope:
// every row, own (the caller creates it), or one of its groups.
func createInScope(ctx context.Context, scope core.DataScope, groupIDs []int64) error {
	if !scope.Restricted() || scope.Owned() || slices.ContainsFunc(groupIDs, scope.HasGroup) {
		return nil
	}
	return errNotInScope(ctx)
}

// errWouldLeaveScope: a restricted caller's update would move the account
// out of its own account:update scope.
var errWouldLeaveScope = core.NewError(403, "would_leave_scope", "")

// stillInScope fails, inside the update's transaction, when the account
// left the caller's scope.
func stillInScope(ctx context.Context, tx *gorm.DB, id int64, scope core.DataScope) error {
	ok, err := inScope(ctx, tx, id, scope)
	if err == nil && !ok {
		err = errWouldLeaveScope.WithMessage(t(ctx, "the change would move the account out of your range", "修改后账号将不在你的管理范围内"))
	}
	return err
}

// loadGroupDiff compares want (deduplicated; nil = not sent) with the groups
// of accountID (0 on create) and marks the added groups uid may not bind
// (segment.UsableGroup). Groups of want that do not exist fail group_ids
// not_found.
func loadGroupDiff(ctx context.Context, q *gorm.DB, accountID int64, want []int64, uid int64) (groupDiff, error) {
	var d groupDiff
	if want == nil {
		return d, nil
	}
	var groups []groupBinding
	if err := store.G(ctx, q).Table("groups g").Select("g.id, ag.account_id IS NOT NULL AS bound, "+segment.UsableGroup+" AS bindable", uid).
		Joins("LEFT JOIN account_groups ag ON ag.group_id = g.id AND ag.account_id = ?", accountID).
		Where("g.id = ANY(?) OR ag.account_id IS NOT NULL", store.Int64s(want)).Scan(&groups).Error; err != nil {
		return d, err
	}
	found := 0
	for _, g := range groups {
		switch {
		case !slices.Contains(want, g.ID): // bound, not wanted
			d.removed = append(d.removed, g.ID)
			continue
		case !g.Bound:
			d.added = append(d.added, g.ID)
			if !g.Bindable {
				d.unbindable = append(d.unbindable, g.ID)
			}
		}
		found++
	}
	if found != len(want) {
		return d, core.InvalidFields(core.FieldError{Field: "group_ids", Code: "not_found",
			Message: t(ctx, "some groups do not exist", "部分分组不存在")})
	}
	return d, nil
}

// ownChange is what a write by an own-level caller changes; cur is nil on
// create.
type ownChange struct {
	bt     core.AccountTypeBinding
	cur    *row
	in     *input
	groups groupDiff
	// upstreamChanged: the write moves an upstream address field (prepare).
	upstreamChanged bool
}

// checkOwnChange enforces the own-level restrictions (SEC-H3) on what the
// write changes, so values an administrator set do not block saving the
// form as is: group changes (G1, M10), capped fields (W2-G4), a new or
// moved custom upstream (G2) and enabling an account an administrator
// disabled (W1-F10).
func (s *Service) checkOwnChange(ctx context.Context, scope core.DataScope, ch ownChange) error {
	if !scope.Restricted() {
		return nil
	}
	if err := s.checkGroupBind(ctx, ch.groups.outside(scope)); err != nil {
		return err
	}
	for _, f := range ownCapped {
		if err := f.restricted(ctx, ch.in, ch.cur); err != nil {
			return err
		}
	}
	if ch.cur == nil || ch.upstreamChanged {
		if err := s.checkCustomUpstream(ctx, scope, ch.bt); err != nil {
			return err
		}
	}
	return checkAdminLock(ctx, ch.cur, ch.in.Status)
}

// errAdminLocked answers an own-level caller who tries to bring back an
// account an administrator disabled.
var errAdminLocked = core.NewError(403, "account_locked_by_admin", "the account was disabled by an administrator")

// checkAdminLock refuses an own-level caller moving a locked account (cur,
// nil on create) to active. The lock is set with disabled and every way the
// full scope brings the account back clears it (automatic states never
// apply to a disabled account), so a locked account is a disabled one.
func checkAdminLock(ctx context.Context, cur *row, status *string) error {
	if cur == nil || !cur.AdminLocked || status == nil || *status != "active" {
		return nil
	}
	return errAdminLocked.WithMessage(t(ctx,
		"this account was disabled by an administrator; only an administrator can enable it",
		"该账号已被管理员禁用，只有管理员可以重新启用"))
}

// adminLockAfter is what a requested status does to the lock, nil for no
// change: the full scope locks when it disables an account somebody else
// owns and unlocks when it enables. A restricted caller never changes
// it (checkAdminLock already refused enabling a locked account).
func adminLockAfter(scope core.DataScope, uid int64, cur *row, status string) *bool {
	switch {
	case scope.Restricted():
		return nil
	case status == "active":
		return new(false)
	case status == "disabled" && (cur.OwnerID == nil || *cur.OwnerID != uid):
		return new(true)
	}
	return nil
}

// disabledReason is the status_reason of a manual disable.
func disabledReason(scope core.DataScope) string {
	if scope.Restricted() {
		return "disabled by owner"
	}
	return "disabled by administrator"
}

// checkGroupBind lets a changed group list (unbinding included) through
// with group:manage, or with account:group:bind when every added group is
// one the caller can use.
func (s *Service) checkGroupBind(ctx context.Context, d groupDiff) error {
	if !d.changed() {
		return nil
	}
	if manage, err := core.Can(ctx, s.d.Authorizer, "group:manage"); manage || err != nil {
		return err
	}
	bind, err := core.Can(ctx, s.d.Authorizer, "account:group:bind")
	if err != nil {
		return err
	}
	if !bind {
		return core.ErrPermissionDenied.WithMessage(t(ctx,
			"changing the groups of an account requires group:manage or account:group:bind permission",
			"修改账号的分组需要 group:manage 或 account:group:bind 权限")).
			WithDetails(map[string]any{"required_permission": "group:manage or account:group:bind"})
	}
	if len(d.unbindable) > 0 {
		return core.InvalidFields(core.FieldError{Field: "group_ids", Code: "not_bindable",
			Message: t(ctx, "only groups you can use can be bound", "只能绑定你自己可用的分组")})
	}
	return nil
}

// checkCustomUpstream refuses an own-level caller without account:relay a
// type whose upstream address can be set freely (G2).
func (s *Service) checkCustomUpstream(ctx context.Context, scope core.DataScope, bt core.AccountTypeBinding) error {
	if !scope.Restricted() || !bt.Type.CustomUpstream() {
		return nil
	}
	if relay, err := core.Can(ctx, s.d.Authorizer, "account:relay"); relay || err != nil {
		return err
	}
	return core.ErrPermissionDenied.WithMessage(t(ctx,
		"accounts with a custom upstream address require account:relay permission",
		"自定义上游地址的账号需要 account:relay 权限")).
		WithDetails(map[string]any{"required_permission": "account:relay"})
}

func uniqueIDs(in []int64) []int64 { return x.Unique(in) }

func setGroups(ctx context.Context, tx *gorm.DB, id int64, groupIDs []int64) error {
	g := store.G(ctx, tx)
	if err := g.Where("account_id = ? AND NOT (group_id = ANY(?))", id, store.Int64s(groupIDs)).
		Delete(&model.AccountGroup{}).Error; err != nil {
		return err
	}
	if len(groupIDs) == 0 {
		return nil
	}
	rows := make([]model.AccountGroup, len(groupIDs))
	for i, gid := range groupIDs {
		rows[i] = model.AccountGroup{AccountID: id, GroupID: gid}
	}
	return g.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
}

// accountInsertColumns are the columns a create writes; the others take
// their defaults. Those with a non-zero default (status, schedulable,
// priority, max_concurrency, weight, auto_disable, spm_window_seconds) are
// pointers in model.Account, always set by the create, so a false or 0 the
// caller chose is written as such.
var accountInsertColumns = []string{"name", "plugin_key", "type", "credentials_enc", "settings",
	"proxy_id", "status", "schedulable", "priority", "max_concurrency", "created_by", "owner_id",
	"weight", "models", "model_mapping", "rpm_limit", "tpm_limit", "auto_disable", "model_limits", "spm_limit", "environment", "header_passthrough", "param_override",
	"header_override", "conversion_loss_policy", "spm_window_seconds", "protocols", "model_protocols"}

// nonZero is &v, or nil (NULL) for the zero value: NULLIF(v, zero).
func nonZero[T comparable](v T) *T {
	var zero T
	if v == zero {
		return nil
	}
	return &v
}

// nullStrings is the nullable text[] value of a: nil (NULL) for a nil slice.
func nullStrings(a []string) *store.Strings {
	if a == nil {
		return nil
	}
	return new(store.Strings(a))
}

// rawJSON is the jsonb value of a document, nil (NULL) for none.
func rawJSON(doc *string) json.RawMessage {
	if doc == nil {
		return nil
	}
	return json.RawMessage(*doc)
}

// environmentJSON is the environment column of v; nil is the empty object,
// the column's default.
func environmentJSON(v core.EnvironmentValues) json.RawMessage {
	if v == nil {
		return json.RawMessage(`{}`)
	}
	b, _ := json.Marshal(v)
	return b
}

// basicPayload is the payload of account.created/updated/deleted (CONTRACTS §12).
func basicPayload(id int64, pluginKey, typ, name string) map[string]any {
	return map[string]any{"account_id": id, "plugin_key": pluginKey, "type": typ, "name": name}
}

// statusPayload is the payload of account.status_changed.
func statusPayload(id int64, pluginKey, typ, name, status, reason string, until *time.Time) map[string]any {
	p := basicPayload(id, pluginKey, typ, name)
	p["status"], p["reason"] = status, reason
	if until != nil {
		p["cooldown_until"] = until.UTC().Format(time.RFC3339)
	}
	return p
}

// proxyByURL is a proxy_url the caller may use: the parsed address and the
// caller's proxy write range, which FindOrCreate matches in and which
// decides allow_private of a row it creates (CONTRACTS §21.4).
type proxyByURL struct {
	spec  core.ProxySpec
	scope core.DataScope
}

// parseProxyURL checks that the caller may link proxies by address (any
// proxy:manage scope; a new proxy needs every row or own, FindOrCreate) and
// parses raw. Errors are 403 permission_denied (details.permission =
// proxy:manage:own) or the
// invalid_argument of core.ParseProxyURL (never echoing the address).
func (s *Service) parseProxyURL(ctx context.Context, raw string) (*proxyByURL, error) {
	scope, err := s.scopeOf(ctx, perm.ProxyManage)
	if err != nil {
		return nil, err
	}
	if scope.Empty() {
		return nil, core.ErrPermissionDenied.WithDetails(map[string]any{"permission": "proxy:manage:own"})
	}
	spec, err := core.ParseProxyURL(raw)
	if err != nil {
		return nil, err
	}
	return &proxyByURL{spec: spec, scope: scope}, nil
}

// resolveProxy runs FindOrCreate for a proxy_url inside the account's
// transaction; the returned id becomes the account's proxy_id.
func (s *Service) resolveProxy(ctx context.Context, tx *gorm.DB, p *proxyByURL, uid int64) (id int64, created bool, err error) {
	return s.d.Resolver.FindOrCreate(ctx, tx, p.spec, uid, p.scope)
}

// auditProxy records the automatic creation once the account id is known
// (same transaction). AuditAutoCreate also broadcasts config:changed for the
// new proxy; nothing else is needed after the commit (no node can have
// cached a proxy that did not exist before this request).
func (s *Service) auditProxy(ctx context.Context, tx *gorm.DB, created bool, proxyID, uid, accountID int64) error {
	if !created {
		return nil
	}
	return s.d.Resolver.AuditAutoCreate(ctx, tx, proxyID, uid, accountID)
}

// resolveWriteRefs normalizes the group list and proxy references shared by
// create and update: deduplicate groups (checked by loadGroupDiff), check the
// caller may reference the proxy, and resolve a proxy_url to a spec (created
// later inside the transaction).
func (s *Service) resolveWriteRefs(ctx context.Context, in *input, action perm.Key) (groupIDs []int64, proxyID *int64, byURL *proxyByURL, err error) {
	if in.GroupIDs != nil {
		groupIDs = uniqueIDs(*in.GroupIDs)
	}
	if in.ProxyID.Valid {
		proxyID = &in.ProxyID.V
	}
	// A restricted caller references only proxies it can see (§21.2).
	if err = s.checkProxyRef(ctx, proxyID, action); err != nil {
		return nil, nil, nil, err
	}
	if in.ProxyURL != nil {
		if s.d.Resolver == nil {
			return nil, nil, nil, core.ErrUnsupported.WithMessage("proxy_url is not supported")
		}
		if byURL, err = s.parseProxyURL(ctx, *in.ProxyURL); err != nil {
			return nil, nil, nil, err
		}
	}
	return groupIDs, proxyID, byURL, nil
}

func (s *Service) create(c *gin.Context) {
	ctx := audit.Context(c)
	var in input
	if !httpapi.BindJSON(c, &in) {
		return
	}
	if fe := in.validate(ctx, true); len(fe) > 0 {
		httpapi.Fail(c, core.InvalidFields(fe...))
		return
	}
	bt, ok := s.accountType(in.PluginKey, in.Type)
	if !ok {
		httpapi.Fail(c, core.InvalidFields(core.FieldError{Field: "type", Code: "unknown",
			Message: t(ctx, "unknown account type (is its plugin enabled?)", "未知的账号类型（插件是否已启用？）")}))
		return
	}
	scope, err := s.scopeOf(ctx, perm.AccountCreate)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	controls, err := planControls(ctx, bt.Type)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	fields := controls.check(ctx, &in, nil, scope)
	if in.setsProtocols() {
		fields = append(fields, in.checkProtocols(ctx, typeProtocols(s.gen(), bt, s.d.Converters), nil)...)
	}
	if len(fields) > 0 {
		httpapi.Fail(c, core.InvalidFields(fields...))
		return
	}
	groupIDs, proxyID, byURL, err := s.resolveWriteRefs(ctx, &in, perm.AccountCreate)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	uid, _ := core.UserID(ctx)
	groups, err := loadGroupDiff(ctx, s.d.DB.Gorm, 0, groupIDs, uid)
	if err == nil {
		err = createInScope(ctx, scope, groupIDs)
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	p, err := s.prepare(ctx, bt, in.Credentials, nil)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if p.settings, err = s.mergeExtensions(bt, p.settings, in.Settings); err != nil {
		httpapi.Fail(c, err)
		return
	}
	controls.clampOwnDefaults(&in, bt.Type.DefaultLimits, scope)
	if err := s.checkOwnChange(ctx, scope, ownChange{bt: bt, in: &in, groups: groups}); err != nil {
		httpapi.Fail(c, err)
		return
	}
	// Declared defaults apply after the own-level restrictions: the
	// declaration, not the caller, chose them.
	controls.fillDefaults(&in, scope)
	status := core.ValueOr(in.Status, "active")
	sched, autoDisable := *in.Schedulable, *in.AutoDisable
	prio, maxc, weight := *in.Priority, *in.MaxConcurrency, *in.Weight
	models := core.ValueOr(in.Models, []string{})
	mapping := *in.ModelMapping
	rpm, tpm, spm, spmWindow := *in.RPMLimit, *in.TPMLimit, *in.SPMLimit, *in.SPMWindowSeconds
	modelLimits, environment, headerPassthrough := *in.ModelLimits, *in.Environment, *in.HeaderPassthrough
	var id int64
	proxyCreated := false
	err = s.d.DB.Tx(ctx, func(tx *gorm.DB) error {
		if byURL != nil {
			// The proxy row must exist before the account references it.
			pid, created, err := s.resolveProxy(ctx, tx, byURL, uid)
			if err != nil {
				return err
			}
			proxyID, proxyCreated = &pid, created
		}
		created := model.Account{Name: *in.Name, PluginKey: bt.Plugin.Key, Type: bt.Type.ID, CredentialsEnc: p.enc,
			Settings: json.RawMessage(p.settings), ProxyID: proxyID, Status: &status, Schedulable: &sched, Priority: &prio,
			MaxConcurrency: &maxc, CreatedBy: nonZero(uid), OwnerID: nonZero(uid), Weight: &weight, Models: store.Strings(models),
			ModelMapping: json.RawMessage(mappingJSON(mapping)), RpmLimit: rpm, TpmLimit: tpm, AutoDisable: &autoDisable,
			ModelLimits: json.RawMessage(modelLimitsJSON(modelLimits)), SpmLimit: spm, Environment: environmentJSON(environment),
			HeaderPassthrough: store.Strings(headerPassthrough), ParamOverride: rawJSON(overrideColumn(in.ParamOverride)),
			HeaderOverride:       rawJSON(overrideColumn(in.HeaderOverride)),
			ConversionLossPolicy: in.ConversionLossPolicy.Stored(), SpmWindowSeconds: &spmWindow,
			Protocols:      nullStrings(in.Protocols.V),
			ModelProtocols: json.RawMessage(modelProtocolsJSON(core.ValueOr(in.ModelProtocols, nil)))}
		if err := store.G(ctx, tx).Select(accountInsertColumns).Create(&created).Error; err != nil {
			return err
		}
		id = created.ID
		if err := setGroups(ctx, tx, id, groupIDs); err != nil {
			return err
		}
		if controls.usageLimit != nil {
			if err := saveUsageLimit(ctx, tx, id, *controls.usageLimit); err != nil {
				return err
			}
		}
		if _, err := s.initializeQuota(ctx, tx, id, bt); err != nil {
			return err
		}
		if proxyID != nil {
			if err := s.auditProxy(ctx, tx, proxyCreated, *proxyID, uid, id); err != nil {
				return err
			}
		}
		if err := audit.Audit(ctx, tx, uid, "account.create", "account", itoa(id),
			map[string]any{"name": *in.Name, "plugin_key": bt.Plugin.Key, "type": bt.Type.ID}); err != nil {
			return err
		}
		return s.d.Events.Emit(ctx, tx, core.Event{Type: core.EventAccountCreated,
			Payload: basicPayload(id, bt.Plugin.Key, bt.Type.ID, *in.Name)})
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.changed(ctx, id)
	s.refreshSaved(ctx, id, bt, p.secret)
	// The row was just created by the caller: no scope needed.
	v, err := s.fullView(ctx, id, core.AllScope)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	v.ProxyCreated = &proxyCreated
	httpapi.Created(c, v)
}

func (s *Service) update(c *gin.Context) {
	ctx := audit.Context(c)
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	var in input
	if !httpapi.BindJSON(c, &in) {
		return
	}
	if fe := in.validate(ctx, false); len(fe) > 0 {
		httpapi.Fail(c, core.InvalidFields(fe...))
		return
	}
	// Explicitly enabling an account restores scheduling as well. Scheduling
	// alone can still be changed without changing the manual enabled state.
	if in.Status != nil && *in.Status == "active" && in.Schedulable == nil {
		enabled := true
		in.Schedulable = &enabled
	}
	scope, err := s.scopeOf(ctx, perm.AccountUpdate)
	var cur *row
	if err == nil {
		cur, err = s.loadRow(ctx, s.d.DB.Gorm, id, scope, false)
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	groupIDs, proxyID, byURL, err := s.resolveWriteRefs(ctx, &in, perm.AccountUpdate)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	// Validate credentials (may call the plugin) before opening the transaction.
	var p *prepared
	if in.hasCredentials() {
		bt, ok := s.accountType(cur.PluginKey, cur.Type)
		if !ok {
			httpapi.Fail(c, core.ErrPluginUnavailable.WithMessage(t(ctx,
				"the plugin providing this account type is not enabled; credentials cannot be changed",
				"提供该账号类型的插件未启用，无法修改凭证")))
			return
		}
		base, err := s.credBase(ctx, cur)
		if err != nil {
			httpapi.Fail(c, err)
			return
		}
		if p, err = s.prepare(ctx, bt, in.Credentials, base); err != nil {
			httpapi.Fail(c, err)
			return
		}
		// Unchanged credentials keep their ciphertext: a fresh nonce would
		// look like a credential change to whatever compares the stored bytes.
		if stored, err := parseObject(base.secret); err == nil && bytes.Equal(mustJSON(stored), p.secret) {
			p.enc = cur.CredEnc
		}
	}
	bt, registered := s.accountType(cur.PluginKey, cur.Type)
	// What the type serves decides which protocols the account can be bound to.
	var served []typeProtocol
	if in.setsProtocols() {
		if !registered {
			httpapi.Fail(c, core.ErrPluginUnavailable.WithMessage(t(ctx,
				"the plugin providing this account type is not enabled; its protocols cannot be changed",
				"提供该账号类型的插件未启用，无法修改协议")))
			return
		}
		served = typeProtocols(s.gen(), bt, s.d.Converters)
	}
	controls, err := planControls(ctx, bt.Type)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	// The controls judge what the caller asked for, before converge fills
	// in the locked fields.
	requested := in
	var extensionSettings *string
	if len(in.Settings) > 0 {
		base := cur.Settings
		if p != nil {
			base = p.settings
		}
		merged, err := s.mergeExtensions(bt, base, in.Settings)
		if err != nil {
			httpapi.Fail(c, err)
			return
		}
		value := string(merged)
		extensionSettings = &value
	}
	controls.converge(&in, cur)
	uid, _ := core.UserID(ctx)
	setProxy, proxyCreated, usageLimitConverged := in.ProxyID.Set, false, false
	err = s.d.DB.Tx(ctx, func(tx *gorm.DB) error {
		locked, err := s.loadRow(ctx, tx, id, scope, true)
		if err != nil {
			return err
		}
		statusChanged := in.Status != nil && *in.Status != locked.Status
		// Judged against the locked row, in the transaction that saves the
		// groups.
		groups, err := loadGroupDiff(ctx, tx, id, groupIDs, uid)
		if err != nil {
			return err
		}
		if err := s.checkOwnChange(ctx, scope, ownChange{bt: bt, cur: locked, in: &requested, groups: groups,
			upstreamChanged: p != nil && p.addressChanged}); err != nil {
			return err
		}
		fields := controls.check(ctx, &requested, locked, scope)
		// Against the locked row: the field the request leaves out decides
		// with the one it sends whether the account still serves a protocol.
		if fields = append(fields, in.checkProtocols(ctx, served, locked)...); len(fields) > 0 {
			return core.InvalidFields(fields...)
		}
		if usageLimitConverged, err = controls.convergeUsageLimit(ctx, tx, id); err != nil {
			return err
		}
		if (p != nil || extensionSettings != nil) && !locked.UpdatedAt.Equal(cur.UpdatedAt) {
			return core.ErrConflict.WithMessage(t(ctx, "the account was modified concurrently, please retry",
				"账号已被其他操作修改，请重试"))
		}
		if byURL != nil {
			pid, created, err := s.resolveProxy(ctx, tx, byURL, uid)
			if err != nil {
				return err
			}
			proxyID, setProxy, proxyCreated = &pid, true, created
		}
		var enc []byte
		var settings *string
		if p != nil {
			enc = p.enc
			st := string(p.settings)
			settings = &st
		}
		if extensionSettings != nil {
			settings = extensionSettings
		}
		var reason *string
		if statusChanged {
			r := ""
			if *in.Status == "disabled" {
				r = disabledReason(scope)
			}
			reason = &r
		}
		// Judged on the requested status, changed or not: an administrator
		// disabling an account its owner already disabled takes it over.
		var adminLocked *bool
		if in.Status != nil {
			adminLocked = adminLockAfter(scope, uid, locked, *in.Status)
		}
		if reason == nil && adminLocked != nil && *adminLocked && !locked.AdminLocked {
			reason = new(disabledReason(scope))
		}
		var mapping, limits *string
		if in.ModelMapping != nil {
			m := mappingJSON(*in.ModelMapping)
			mapping = &m
		}
		if in.ModelLimits != nil {
			l := modelLimitsJSON(*in.ModelLimits)
			limits = &l
		}
		set := in.columns(settings, reason, mapping, limits, adminLocked)
		if setProxy {
			set["proxy_id"] = proxyID
		}
		if enc != nil {
			set["credentials_enc"] = enc
		}
		if err := scopedAccount(store.G(ctx, tx), id, scope).Updates(set).Error; err != nil {
			return err
		}
		if statusChanged && *in.Status == "active" && limitDisabled(locked.Status, locked.AutoDisabledSource) {
			if err := waiveUsageLimit(ctx, tx, id, locked.AutoDisabledUntil); err != nil { // enabled by hand (§73.4)
				return err
			}
		}
		if in.GroupIDs != nil {
			if err := setGroups(ctx, tx, id, groupIDs); err != nil {
				return err
			}
			if err := stillInScope(ctx, tx, id, scope); err != nil {
				return err
			}
		}
		if proxyID != nil {
			if err := s.auditProxy(ctx, tx, proxyCreated, *proxyID, uid, id); err != nil {
				return err
			}
		}
		if err := audit.Audit(ctx, tx, uid, "account.update", "account", itoa(id),
			map[string]any{"fields": in.changedFields()}); err != nil {
			return err
		}
		name := locked.Name
		if in.Name != nil {
			name = *in.Name
		}
		// Another key, address or proxy may read another balance: the next
		// read starts afresh (CONTRACTS §52.3).
		if p != nil || extensionSettings != nil || setProxy {
			if err := store.DeleteBalance(ctx, tx, id); err != nil {
				return err
			}
		}
		payload := basicPayload(id, locked.PluginKey, locked.Type, name)
		payload["changed_fields"] = in.changedFields()
		evs := []core.Event{{Type: core.EventAccountUpdated, Payload: payload}}
		if statusChanged {
			evs = append(evs, core.Event{Type: core.EventAccountStatusChanged,
				Payload: statusPayload(id, locked.PluginKey, locked.Type, name, *in.Status, *reason, nil)})
		}
		return s.d.Events.Emit(ctx, tx, evs...)
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if p != nil || extensionSettings != nil || setProxy {
		s.quota.forget(id)
		s.evictQuotaCache(ctx, id)
		s.evictBalanceCache(ctx, id)
	}
	if usageLimitConverged {
		if err := s.EnforceUsageLimits(ctx, []int64{id}); err != nil {
			slog.WarnContext(ctx, "account: apply usage limit", "account", id, "err", err)
		}
	}
	s.changed(ctx, id)
	if p != nil {
		s.refreshSaved(ctx, id, bt, p.secret)
	}
	v, err := s.fullView(ctx, id, scope)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	v.ProxyCreated = &proxyCreated
	httpapi.OK(c, v)
}

// ---------------------------------------------------------------- reset status

// resetStatus is POST /accounts/:id/reset-status: an explicit recovery of
// the account status, its reason and the Redis cooldown the gateway sets
// on rate limits, overloads and failures. Disabled and error accounts become
// active again, while the administrator's schedulable switch is preserved.
// The quota snapshot remains an upstream observation, and credentials,
// configured limits and live concurrency leases are never reset here.
func (s *Service) resetStatus(c *gin.Context) {
	ctx := audit.Context(c)
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	scope, err := s.scopeOf(ctx, perm.AccountUpdate)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	uid, _ := core.UserID(ctx)
	var a *row
	err = s.d.DB.Tx(ctx, func(tx *gorm.DB) error {
		loaded, err := s.loadRow(ctx, tx, id, scope, true)
		if err != nil {
			return err
		}
		a = loaded
		// Resetting brings the account back: refused like enabling it.
		if scope.Restricted() {
			if err := checkAdminLock(ctx, a, new("active")); err != nil {
				return err
			}
		}
		cleared, err := s.clearCooldown(ctx, id)
		if err != nil {
			return err
		}
		statusReset := a.Status != "active" || a.StatusReason != "" || a.AutoDisabledUntil != nil
		if statusReset {
			// A reset unlocks (an own-level reset of a locked account was
			// refused above).
			if err := resetStatus(store.G(ctx, tx), id).Error; err != nil {
				return err
			}
			// Brought back by hand: the usage limit leaves it running for
			// the rest of the period (CONTRACTS §73.4).
			if limitDisabled(a.Status, a.AutoDisabledSource) {
				if err := waiveUsageLimit(ctx, tx, id, a.AutoDisabledUntil); err != nil {
					return err
				}
			}
		}
		if err := audit.Audit(ctx, tx, uid, "account.reset_status", "account", itoa(id),
			map[string]any{"cooldown_cleared": cleared, "status_reset": statusReset, "previous_status": a.Status}); err != nil {
			return err
		}
		if !cleared && !statusReset {
			return nil
		}
		// The account leaves a disabled, error or cooldown state; report
		// the transition using the same account.status_changed event.
		return s.d.Events.Emit(ctx, tx, core.Event{Type: core.EventAccountStatusChanged,
			Payload: statusPayload(id, a.PluginKey, a.Type, a.Name, "active", "status reset by administrator", nil)})
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.evictQuotaCache(ctx, id)
	s.evictBalanceCache(ctx, id)
	s.changed(ctx, id)
	v, err := s.fullView(ctx, id, scope)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, v)
}

// resetStatus brings account id back to active, unlocked.
func resetStatus(db *gorm.DB, id int64) *gorm.DB {
	return db.Model(&model.Account{}).Where("id = ?", id).Updates(map[string]any{"status": "active", "status_reason": "",
		"auto_disabled_until": nil, "admin_locked": false, "updated_at": gorm.Expr("clock_timestamp()")})
}

// clearCooldown deletes the account's runtime scheduling block (the Redis
// cooldown key) and reports whether there was one.
func (s *Service) clearCooldown(ctx context.Context, id int64) (bool, error) {
	if s.d.Redis == nil {
		return false, nil
	}
	n, err := s.d.Redis.Del(ctx, cooldownKey(id)).Result()
	if err != nil {
		return false, core.ErrUnavailable.WithCause(err)
	}
	return n > 0, nil
}

func (s *Service) delete(c *gin.Context) {
	ctx := audit.Context(c)
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	scope, err := s.scopeOf(ctx, perm.AccountDelete)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	uid, _ := core.UserID(ctx)
	err = s.d.DB.Tx(ctx, func(tx *gorm.DB) error {
		var deleted model.Account
		res := store.G(ctx, tx).Model(&deleted).Table("accounts a").
			Clauses(clause.Returning{Columns: []clause.Column{{Name: "plugin_key"}, {Name: "type"}, {Name: "name"}}}).
			Where("a.id = ? AND a.deleted_at IS NULL", id).Scopes(core.AccountRows().Scope(scope)).
			Updates(map[string]any{"deleted_at": gorm.Expr("now()"), "updated_at": gorm.Expr("now()")})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return notFound(ctx)
		}
		pk, typ, name := deleted.PluginKey, deleted.Type, deleted.Name
		if err := store.G(ctx, tx).Where("account_id = ?", id).Delete(&model.AccountGroup{}).Error; err != nil {
			return err
		}
		if _, err := s.d.ScopeGrants.ClearScopeGrants(ctx, tx, uid, core.ScopeRefAccount, []int64{id}); err != nil {
			return err
		}
		if err := audit.Audit(ctx, tx, uid, "account.delete", "account", itoa(id), map[string]any{"name": name}); err != nil {
			return err
		}
		return s.d.Events.Emit(ctx, tx, core.Event{Type: core.EventAccountDeleted, Payload: basicPayload(id, pk, typ, name)})
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if s.d.Redis != nil {
		_ = s.d.Redis.Del(ctx, cooldownKey(id)).Err()
	}
	s.evictQuotaCache(ctx, id)
	s.evictBalanceCache(ctx, id)
	s.changed(ctx, id)
	httpapi.NoContent(c)
}

// ---------------------------------------------------------------- owner

// ownerInput is the body of PUT /accounts/:id/owner.
type ownerInput struct {
	OwnerID int64 `json:"owner_id" binding:"required,gt=0"`
}

// OwnerView answers PUT /accounts/:id/owner: the account's owner now.
type OwnerView struct {
	ID         int64  `json:"id"`
	OwnerID    int64  `json:"owner_id"`
	OwnerEmail string `json:"owner_email"`
}

// putOwner moves an account to another owner (所属, CONTRACTS §103): a
// user that exists and is not deleted. The owner is row data, as
// created_by is: no authz version moves. A move is audited as
// account.owner (from, to); naming the current owner changes nothing.
func (s *Service) putOwner(c *gin.Context) {
	ctx := audit.Context(c)
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	var in ownerInput
	if !httpapi.BindJSON(c, &in) {
		return
	}
	uid, _ := core.UserID(ctx)
	out := OwnerView{ID: id, OwnerID: in.OwnerID}
	err := s.d.DB.Tx(ctx, func(tx *gorm.DB) error {
		cur, err := s.loadRow(ctx, tx, id, core.AllScope, true)
		if err != nil {
			return err
		}
		db := store.G(ctx, tx)
		err = db.Model(&model.User{}).Select("email").Where("id = ? AND deleted_at IS NULL", in.OwnerID).Take(&out.OwnerEmail).Error
		switch {
		case store.IsNoRows(err):
			return core.InvalidFields(core.FieldError{Field: "owner_id", Code: "not_found",
				Message: t(ctx, "owner_id must be a user that is not deleted", "owner_id 必须是未删除的用户")})
		case err != nil:
			return err
		case cur.OwnerID != nil && *cur.OwnerID == in.OwnerID:
			return nil
		}
		err = db.Model(&model.Account{}).Where("id = ?", id).
			Updates(map[string]any{"owner_id": in.OwnerID, "updated_at": gorm.Expr("now()")}).Error
		if err != nil {
			return err
		}
		return audit.Audit(ctx, tx, uid, "account.owner", "account", itoa(id), map[string]any{"from": cur.OwnerID, "to": in.OwnerID})
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, out)
}

// ---------------------------------------------------------------- reveal

func (s *Service) reveal(c *gin.Context) {
	ctx := audit.Context(c)
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	a, err := s.loadIn(ctx, id, perm.AccountCredentialView)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	plain, err := s.decrypt(a.PluginKey, a.CredEnc)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	all, err := merge(plain, a.Settings)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	uid, _ := core.UserID(ctx)
	if err := audit.Audit(ctx, s.d.DB.Gorm, uid, "account.credentials.reveal", "account", itoa(id), nil); err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, gin.H{"credentials": json.RawMessage(mustJSON(all))})
}
