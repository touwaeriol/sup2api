// Package apikey implements user API keys: owner-only self-service endpoints,
// and the gateway authenticator (core.APIKeyAuthenticator).
package apikey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/secret"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/segment"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

const (
	// KeyPrefix starts every client key.
	KeyPrefix     = "sk-s2a-"
	keyRandomLen  = 40
	keyLen        = len(KeyPrefix) + keyRandomLen
	displayPrefix = 12
	flushInterval = 10 * time.Second
	base62        = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

// Service serves API key endpoints and authenticates gateway requests.
type Service struct {
	Cipher *secret.Cipher
	// Windows and Slots hold the live counts of the owners' caps; the key
	// list reports them when set.
	Windows core.RateWindows
	Slots   core.Slots
	// Balances gives GET /v1/usage the owner's balance.
	Balances Balances
	db       *store.DB
	rdb      redis.UniversalClient // optional cache
	authz    core.Authorizer
	reg      core.PluginRegistry // optional: platforms are [] without it

	mu      sync.Mutex
	touched map[int64]time.Time
}

var _ core.APIKeyAuthenticator = (*Service)(nil)

// New builds the service. rdb may be nil (no cache); reg may be nil (keys
// then list no platforms). Call Run to persist last_used_at in the background.
func New(db *store.DB, rdb redis.UniversalClient, authz core.Authorizer, reg core.PluginRegistry) *Service {
	return &Service{db: db, rdb: rdb, authz: authz, reg: reg, touched: map[int64]time.Time{}}
}

// RegisterRoutes mounts the API key endpoints.
func (s *Service) RegisterRoutes(r *httpapi.Router) {
	r.Perm("GET", "/me/api-keys", "apikey:self:manage", s.listMine)
	r.Perm("POST", "/me/api-keys", "apikey:self:manage", s.createMine)
	r.Perm("PATCH", "/me/api-keys/:id", "apikey:self:manage", s.updateMine)
	r.Perm("PUT", "/me/api-keys/:id/ip-allowlist", "apikey:self:manage", s.updateIPAllowlist)
	// Reveal returns an existing key of the caller in plain text, audited;
	// no step-up proof (user decision 2026-11-17: copying a key should not
	// ask for verification).
	r.Perm("POST", "/me/api-keys/:id/reveal", "apikey:self:manage", s.revealMine)
	r.Perm("POST", "/me/api-keys/:id/rotate", "apikey:self:manage", s.rotateMine)
	r.Perm("DELETE", "/me/api-keys/:id", "apikey:self:manage", s.deleteMine)
}

// Run flushes last_used_at every 10 s until ctx is done, then flushes once more.
func (s *Service) Run(ctx context.Context) {
	tk := time.NewTicker(flushInterval)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := s.FlushLastUsed(fctx); err != nil {
				slog.Warn("apikey: flush last_used_at", "err", err)
			}
			cancel()
			return
		case <-tk.C:
			if err := s.FlushLastUsed(ctx); err != nil {
				slog.WarnContext(ctx, "apikey: flush last_used_at", "err", err)
			}
		}
	}
}

// FlushLastUsed writes pending last_used_at updates in one statement.
func (s *Service) FlushLastUsed(ctx context.Context) error {
	s.mu.Lock()
	if len(s.touched) == 0 {
		s.mu.Unlock()
		return nil
	}
	pending := s.touched
	s.touched = map[int64]time.Time{}
	s.mu.Unlock()
	ids := make([]int64, 0, len(pending))
	ts := make([]time.Time, 0, len(pending))
	for id, at := range pending {
		ids = append(ids, id)
		ts = append(ts, at)
	}
	// last_used_at only moves forward.
	err := store.G(ctx, s.db.Gorm).Table("api_keys AS k").
		Clauses(store.UpdateFrom("(SELECT unnest(?::bigint[]) AS id, unnest(?::timestamptz[]) AS t) v", store.Int64s(ids), store.Times(ts))).
		Where("k.id = v.id AND (k.last_used_at IS NULL OR k.last_used_at < v.t)").Update("last_used_at", gorm.Expr("v.t")).Error
	if err != nil {
		// Put them back so the next tick retries.
		s.mu.Lock()
		for id, at := range pending {
			if cur, ok := s.touched[id]; !ok || cur.Before(at) {
				s.touched[id] = at
			}
		}
		s.mu.Unlock()
	}
	return err
}

func (s *Service) touch(id int64) {
	s.mu.Lock()
	s.touched[id] = time.Now().UTC()
	s.mu.Unlock()
}

// ---------------------------------------------------------------- key material

// generateKey returns a new client key: "sk-s2a-" + 40 base62 characters.
func generateKey() (string, error) {
	b := make([]byte, keyLen)
	copy(b, KeyPrefix)
	max := big.NewInt(int64(len(base62)))
	for i := len(KeyPrefix); i < keyLen; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = base62[n.Int64()]
	}
	return string(b), nil
}

// HashKey returns the sha256 hex stored in api_keys.key_hash.
func HashKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func cacheKey(hash string) string { return "apikey:" + hash }

// ---------------------------------------------------------------- authenticator

// cached holds one authoritative database read. Authentication and scheduling
// facts are deliberately not cached: a concurrent revoke must not be undone
// by a reader refilling a Redis entry after invalidation.
type cached struct {
	Found           bool            `json:"found"`
	KeyID           int64           `json:"key_id,omitempty"`
	UserID          int64           `json:"user_id,omitempty"`
	KeyStatus       string          `json:"key_status,omitempty"`
	ExpiresAt       *time.Time      `json:"expires_at,omitempty"`
	UserActive      bool            `json:"user_active,omitempty"`
	GroupID         int64           `json:"group_id,omitempty"`
	GroupName       string          `json:"group_name,omitempty"`
	GroupStatus     string          `json:"group_status,omitempty"`
	RateMultiplier  decimal.Decimal `json:"rate_multiplier"`
	ModelAllowlist  []string        `json:"model_allowlist,omitempty"`
	ModelFilterMode string          `json:"model_filter_mode,omitempty"`
	// GroupEnvironment is the group layer of the environment (§74.3).
	GroupEnvironment core.EnvironmentValues `json:"group_environment,omitempty"`
	GroupAvailable   bool                   `json:"group_available,omitempty"`
	AllowedIPs       []string               `json:"allowed_ips"`
	KeyLimits        core.KeyLimits
	KeyQuota         core.KeyQuota
	// GroupLossPolicy is the group's conversion loss policy (§88).
	GroupLossPolicy string `json:"group_loss_policy,omitempty"`
	// SegmentID is the owner's segment. RateMultiplier is the rate the segment
	// pays in the group and RateSource where it comes from (core.SegmentRate).
	SegmentID  int64  `json:"segment_id,omitempty"`
	RateSource string `json:"rate_source,omitempty"`
}

// Authenticate resolves a raw client key into a principal.
func (s *Service) Authenticate(ctx context.Context, rawKey string) (*core.APIKeyPrincipal, error) {
	rawKey = strings.TrimSpace(rawKey)
	if len(rawKey) != keyLen || !strings.HasPrefix(rawKey, KeyPrefix) {
		return nil, core.ErrUnauthenticated.WithMessage("invalid api key")
	}
	hash := HashKey(rawKey)
	e, err := s.lookup(ctx, hash)
	if err != nil {
		return nil, err
	}
	if !e.Found || e.KeyStatus != "active" {
		return nil, core.ErrUnauthenticated.WithMessage("invalid api key")
	}
	if e.ExpiresAt != nil && !time.Now().Before(*e.ExpiresAt) {
		return nil, core.ErrUnauthenticated.WithMessage("api key expired")
	}
	if !e.UserActive {
		return nil, core.ErrUnauthenticated.WithMessage("user disabled")
	}
	if !ipAllowed(core.ClientIP(ctx), e.AllowedIPs) {
		return nil, core.NewError(403, "ip_not_allowed", t(ctx, "client IP is not allowed for this API key", "调用 IP 不在此 API Key 的白名单内"))
	}
	if err := groupError(ctx, e); err != nil {
		return nil, err
	}
	ok, err := s.authz.Can(ctx, e.UserID, "gateway:use")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, core.ErrPermissionDenied.WithDetails(map[string]any{"permission": "gateway:use"})
	}
	allow := e.ModelAllowlist
	if allow == nil {
		allow = []string{}
	}
	s.touch(e.KeyID)
	return &core.APIKeyPrincipal{
		KeyID:      e.KeyID,
		UserID:     e.UserID,
		SegmentID:  e.SegmentID,
		RateSource: e.RateSource,
		KeyLimits:  e.KeyLimits,
		KeyQuota:   e.KeyQuota,
		Group: core.GroupInfo{
			ID:                   e.GroupID,
			Name:                 e.GroupName,
			Status:               e.GroupStatus,
			RateMultiplier:       e.RateMultiplier,
			ModelAllowlist:       allow,
			ModelFilterMode:      e.ModelFilterMode,
			Environment:          e.GroupEnvironment,
			ConversionLossPolicy: e.GroupLossPolicy,
		},
	}, nil
}

// lookupColumns are what authenticating a key needs (lookupQuery): the key,
// its owner and the owner's segment, the key's group and what the segment
// gets there. Whether the owner may use the group is
// segment.OpenToKeyOwner, as for the key endpoints and /me/groups.
const lookupColumns = `k.id, k.user_id, k.status, k.expires_at,
		(u.status = 'active' AND u.deleted_at IS NULL),
		COALESCE(g.id, 0), COALESCE(g.name, ''), COALESCE(g.status, 'deleted'), COALESCE(g.rate_multiplier, 1),
		COALESCE(g.model_allowlist, '[]'), COALESCE(g.model_filter_mode, 'blacklist'), COALESCE(g.environment, '{}'),
		COALESCE(` + segment.OpenToKeyOwner + `, false), k.allowed_ips,
		k.rpm_limit, k.tpm_limit, k.max_concurrency, k.quota_limit, k.quota_used, COALESCE(g.conversion_loss_policy, ''),
		s.id, s.discount, sg.rate_override`

// lookupFrom is lookupQuery's FROM: the joins as one text, which renders
// as Joins would and allocates less on this path.
const lookupFrom = "api_keys k" +
	" JOIN users u ON u.id = k.user_id" +
	" JOIN segments s ON s.id = u.segment_id" +
	" LEFT JOIN groups g ON g.id = k.group_id" +
	" LEFT JOIN segment_groups sg ON sg.segment_id = s.id AND sg.group_id = g.id"

// lookupQuery reads lookupColumns of the key hash in one statement. It runs
// on every gateway request: its row is scanned through Row into local
// variables, the GORM form that allocates least (BenchmarkHotKeyLookup;
// internal/store/gormtest/BASELINE.md).
func lookupQuery(db *gorm.DB, hash string) *gorm.DB {
	return db.Table(lookupFrom).Select(lookupColumns).Where("k.key_hash = ? AND k.deleted_at IS NULL", hash)
}

func (s *Service) lookup(ctx context.Context, hash string) (*cached, error) {
	e := &cached{}
	// The group's default rate, the segment's discount and its own rate in
	// the group (NULL = none).
	var groupRate, discount decimal.Decimal
	var override *decimal.Decimal
	// database/sql hands jsonb over as its document: JSON decodes it.
	var allow store.JSON[[]string]
	var env store.JSON[core.EnvironmentValues]
	err := lookupQuery(store.G(ctx, s.db.Gorm), hash).Row().Scan(
		&e.KeyID, &e.UserID, &e.KeyStatus, &e.ExpiresAt, &e.UserActive,
		&e.GroupID, &e.GroupName, &e.GroupStatus, &groupRate, &allow, &e.ModelFilterMode, &env, &e.GroupAvailable, (*store.Strings)(&e.AllowedIPs),
		&e.KeyLimits.RPM, &e.KeyLimits.TPM, &e.KeyLimits.Concurrency, &e.KeyQuota.Limit, &e.KeyQuota.Used, &e.GroupLossPolicy,
		&e.SegmentID, &discount, &override)
	e.ModelAllowlist, e.GroupEnvironment = allow.V, env.V
	switch {
	case store.IsNoRows(err):
		e = &cached{Found: false}
	case err != nil:
		return nil, err
	default:
		e.Found = true
		e.RateMultiplier, e.RateSource = core.SegmentRate(groupRate, discount, override)
	}
	return e, nil
}

// Group errors of a key, each with its own code so clients and the console
// can tell "pick another group" from "the platform has no account" (sub2api
// answered GROUP_DELETED / GROUP_DISABLED / GROUP_NOT_ALLOWED alike).
const (
	CodeGroupDeleted    = "group_deleted"
	CodeGroupDisabled   = "group_disabled"
	CodeGroupNotAllowed = "group_not_allowed"
)

// groupError tells why the key's group cannot serve it, or nil.
func groupError(ctx context.Context, e *cached) error {
	switch {
	case e.GroupID == 0:
		return core.NewError(403, CodeGroupDeleted, t(ctx,
			"the group of this API key has been deleted; pick another group for the key in the console",
			"API Key 所属分组已被删除，请在控制台为该 Key 重新选择分组"))
	case e.GroupStatus != "active":
		return core.NewError(403, CodeGroupDisabled, t(ctx,
			"the group of this API key is disabled",
			"API Key 所属分组已停用"))
	case !e.GroupAvailable:
		return core.NewError(403, CodeGroupNotAllowed, t(ctx,
			"the group of this API key is no longer available to its owner",
			"API Key 所属分组已不再对当前用户开放"))
	}
	return nil
}

func (s *Service) dropCache(ctx context.Context, hash string) {
	if s.rdb == nil || hash == "" {
		return
	}
	if err := s.rdb.Del(ctx, cacheKey(hash)).Err(); err != nil {
		slog.WarnContext(ctx, "apikey: cache drop", "err", err)
	}
}

// ---------------------------------------------------------------- HTTP

// APIKey is the API view of an api_keys row.
type APIKey struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"user_id"`
	UserEmail string `json:"user_email,omitempty"`
	Name      string `json:"name"`
	KeyPrefix string `json:"key_prefix"`
	Copyable  bool   `json:"copyable"`
	// GroupID is null once the key's group was deleted (keys are only
	// loosely bound to groups); GroupStatus says whether the group serves the
	// key: active | disabled | unavailable (not open to the owner) | deleted.
	GroupID     *int64   `json:"group_id"`
	GroupName   string   `json:"group_name"`
	GroupStatus string   `json:"group_status"`
	AllowedIPs  []string `json:"allowed_ips"`
	// Platforms the key can reach: those of its group (CONTRACTS §13).
	Platforms []string `json:"platforms"`
	// The owner's caps and, when the service counts them, the current use.
	core.KeyLimits
	core.KeyQuota
	Live       *KeyLive   `json:"live,omitempty"`
	Status     string     `json:"status"`
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
	// Key is plaintext, returned only by create and explicit authorized reveal.
	Key string `json:"key,omitempty"`
}

// selectKey are the columns of keyRow, read by keyView.
const selectKey = `k.id, k.user_id, u.email AS user_email, k.name, k.key_prefix, g.id AS group_id, COALESCE(g.name, '') AS group_name,
		CASE WHEN g.id IS NULL THEN 'deleted'
			WHEN g.status <> 'active' THEN 'disabled'
			WHEN ` + segment.OpenToKeyOwner + ` THEN 'active'
			ELSE 'unavailable' END AS group_status,
		k.status, k.expires_at, k.last_used_at, k.created_at, k.key_cipher IS NOT NULL AS copyable, k.allowed_ips,
		k.rpm_limit, k.tpm_limit, k.max_concurrency, k.quota_limit, k.quota_used`

// keyView selects keyRow rows from api_keys k, with the owner u and the
// group g.
func keyView(db *gorm.DB) *gorm.DB {
	return keysWithOwner(db).Select(selectKey).Joins("LEFT JOIN groups g ON g.id = k.group_id")
}

// keysWithOwner is api_keys k joined with its owner u, what the filters of
// the key list read.
func keysWithOwner(db *gorm.DB) *gorm.DB {
	return db.Table("api_keys k").Joins("JOIN users u ON u.id = k.user_id")
}

// keyRow is a row of keyView.
type keyRow struct {
	ID, UserID                     int64
	UserEmail, Name, KeyPrefix     string
	GroupID                        *int64
	GroupName, GroupStatus, Status string
	ExpiresAt, LastUsedAt          *time.Time
	CreatedAt                      time.Time
	Copyable                       bool
	AllowedIPs                     store.Strings
	RpmLimit                       int
	TpmLimit                       int64
	MaxConcurrency                 int
	QuotaLimit, QuotaUsed          decimal.Decimal
}

func (r *keyRow) key() *APIKey {
	return &APIKey{ID: r.ID, UserID: r.UserID, UserEmail: r.UserEmail, Name: r.Name, KeyPrefix: r.KeyPrefix,
		GroupID: r.GroupID, GroupName: r.GroupName, GroupStatus: r.GroupStatus,
		Status: r.Status, ExpiresAt: r.ExpiresAt, LastUsedAt: r.LastUsedAt, CreatedAt: r.CreatedAt,
		Copyable: r.Copyable, AllowedIPs: r.AllowedIPs,
		KeyLimits: core.KeyLimits{RPM: r.RpmLimit, TPM: r.TpmLimit, Concurrency: r.MaxConcurrency},
		KeyQuota:  core.KeyQuota{Limit: r.QuotaLimit, Used: r.QuotaUsed}}
}

func t(ctx context.Context, en, zh string) string { return core.Text(ctx, en, zh) }

func notFound(ctx context.Context) error {
	return core.ErrNotFound.WithMessage(t(ctx, "api key not found", "API Key 不存在"))
}

func (s *Service) query(c *gin.Context, filter func(*gorm.DB) *gorm.DB, hideEmail bool) {
	ctx := c.Request.Context()
	page, size := httpapi.Pagination(c)
	g := store.G(ctx, s.db.Gorm)
	var total int64
	if err := keysWithOwner(g).Scopes(filter).Count(&total).Error; err != nil {
		httpapi.Fail(c, err)
		return
	}
	var rows []keyRow
	if err := keyView(g).Scopes(filter).Order("k.id DESC").Limit(size).Offset((page - 1) * size).Find(&rows).Error; err != nil {
		httpapi.Fail(c, err)
		return
	}
	items := make([]*APIKey, len(rows))
	for i := range rows {
		items[i] = rows[i].key()
		if hideEmail {
			items[i].UserEmail = ""
		}
	}
	if err := s.fill(ctx, items); err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.List(c, items, httpapi.Page{Page: page, PageSize: size, Total: total})
}

func (s *Service) listMine(c *gin.Context) {
	uid, _ := core.UserID(c.Request.Context())
	f, err := mineFilter(c, uid)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.query(c, f, true)
}

// keyStatusFilters are the conditions of the status= filter of GET
// /me/api-keys (CONTRACTS §92.1). They split the keys the way the console's
// status badge does: a key that is switched off is "disabled" even past its
// expiry, and "expired" is a key Authenticate rejects for its expiry alone.
var keyStatusFilters = map[string]string{
	"active":   "k.status = 'active' AND (k.expires_at IS NULL OR k.expires_at > now())",
	"disabled": "k.status <> 'active'",
	"expired":  "k.status = 'active' AND k.expires_at <= now()",
}

// mineFilter builds the WHERE clause of GET /me/api-keys over api_keys k:
// the caller's keys, narrowed by q (name or key prefix), status and
// group_id.
func mineFilter(c *gin.Context, userID int64) (func(*gorm.DB) *gorm.DB, error) {
	ctx := c.Request.Context()
	var f store.Conds
	f.And("k.deleted_at IS NULL AND k.user_id = ?", userID)
	if q := strings.TrimSpace(c.Query("q")); q != "" {
		// PostgreSQL takes neither as text.
		if !utf8.ValidString(q) || strings.ContainsRune(q, 0) {
			return nil, core.InvalidFields(core.FieldError{Field: "q", Code: "invalid",
				Message: t(ctx, "q must be valid UTF-8 text", "q 须为合法的 UTF-8 文本")})
		}
		cond, args := "k.name ILIKE ?", []any{"%" + store.Like(q) + "%"}
		if prefix := searchedPrefix(q); prefix != "" {
			cond, args = "(k.name ILIKE ? OR k.key_prefix ILIKE ?)", append(args, store.Like(prefix)+"%")
		}
		f.And(cond, args...)
	}
	if v := c.Query("status"); v != "" {
		cond, ok := keyStatusFilters[v]
		if !ok {
			return nil, core.InvalidFields(core.FieldError{Field: "status", Code: "invalid",
				Message: t(ctx, "status must be active, disabled or expired", "状态只能是 active、disabled 或 expired")})
		}
		f.And(cond)
	}
	if v := c.Query("group_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			return nil, core.InvalidFields(core.FieldError{Field: "group_id", Code: "invalid",
				Message: t(ctx, "group_id must be a positive integer", "group_id 须为正整数")})
		}
		f.And("k.group_id = ?", id)
	}
	return f.Scope(), nil
}

// searchedPrefix is the key prefix the search text q asks for, or "" when q
// is not the start of a key: KeyPrefix or less starts every key, so it would
// filter nothing. A pasted key is found by the prefix stored of it: the
// console sends no more than that, so the key itself stays out of the URL.
func searchedPrefix(q string) string {
	if len(q) <= len(KeyPrefix) || !strings.EqualFold(q[:len(KeyPrefix)], KeyPrefix) {
		return ""
	}
	// A cut inside a character is not a key and would not be valid text.
	if len(q) > displayPrefix && utf8.RuneStart(q[displayPrefix]) {
		return q[:displayPrefix]
	}
	return q
}

func (s *Service) loadKey(ctx context.Context, id, ownerID int64) (*APIKey, error) {
	var r keyRow
	err := keyView(store.G(ctx, s.db.Gorm)).Scopes(ownedKey("k", id, ownerID)).Take(&r).Error
	if store.IsNoRows(err) {
		return nil, notFound(ctx)
	}
	if err != nil {
		return nil, err
	}
	k := r.key()
	if err := s.fill(ctx, []*APIKey{k}); err != nil {
		return nil, err
	}
	return k, nil
}

// groupAvailable reports whether group is active and usable by user. Inside a
// transaction the row stays key-share locked, so a concurrent group delete
// waits for the key write (and then unbinds it) instead of turning it into a
// foreign key violation.
func groupAvailable(ctx context.Context, q *gorm.DB, userID, groupID int64) (bool, error) {
	if err := store.RequireTx(q); err != nil {
		return false, err
	}
	var ok bool
	err := usableGroup(store.G(ctx, q), userID, groupID).Take(&ok).Error
	if store.IsNoRows(err) {
		return false, nil
	}
	return ok, err
}

// usableGroup selects true for group when user may use it, key-share
// locking the group row.
func usableGroup(db *gorm.DB, userID, groupID int64) *gorm.DB {
	return db.Table("groups g").Select("true").
		Where("g.id = ?", groupID).
		Where(segment.UsableGroup, userID).
		Clauses(store.Lock(store.LockingStrengthKeyShare).Of("g"))
}

// requireGroup fails with a group_id field error unless the group is usable by user.
func requireGroup(ctx context.Context, q *gorm.DB, userID, groupID int64) error {
	ok, err := groupAvailable(ctx, q, userID, groupID)
	if err != nil || ok {
		return err
	}
	return core.InvalidFields(core.FieldError{Field: "group_id", Code: "not_available",
		Message: t(ctx, "the group does not exist or is not available to the user", "分组不存在或用户无权使用")})
}

func validName(ctx context.Context, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 100 {
		return "", core.InvalidFields(core.FieldError{Field: "name", Code: "invalid",
			Message: t(ctx, "name must be 1-100 characters", "名称长度须为 1-100 个字符")})
	}
	return name, nil
}

func futureExpiry(ctx context.Context, at *time.Time) error {
	if at != nil && !at.After(time.Now()) {
		return core.InvalidFields(core.FieldError{Field: "expires_at", Code: "invalid",
			Message: t(ctx, "expiry must be in the future", "过期时间必须晚于当前时间")})
	}
	return nil
}

func (s *Service) createMine(c *gin.Context) {
	httpapi.NoStore(c)
	ctx := c.Request.Context()
	uid, _ := core.UserID(ctx)
	var in struct {
		Name       string     `json:"name"`
		GroupID    int64      `json:"group_id"`
		ExpiresAt  *time.Time `json:"expires_at"`
		AllowedIPs []string   `json:"allowed_ips"`
		core.KeyLimits
		QuotaLimit decimal.Decimal `json:"quota_limit"`
	}
	if !httpapi.BindJSON(c, &in) {
		return
	}
	if err := validCaps(ctx, in.KeyLimits, in.QuotaLimit); err != nil {
		httpapi.Fail(c, err)
		return
	}
	name, err := validName(ctx, in.Name)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if err := futureExpiry(ctx, in.ExpiresAt); err != nil {
		httpapi.Fail(c, err)
		return
	}
	allowedIPs, err := normalizeAllowedIPs(ctx, in.AllowedIPs)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	raw, err := generateKey()
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	hash := HashKey(raw)
	var id int64
	err = s.db.Tx(ctx, func(tx *gorm.DB) error {
		if err := requireGroup(ctx, tx, uid, in.GroupID); err != nil {
			return err
		}
		// The caps and allowed_ips have database defaults: GORM leaves them
		// out of the INSERT when zero, and those defaults (0, '{}') are the
		// zero values.
		k := model.APIKey{UserID: uid, GroupID: &in.GroupID, Name: name, KeyPrefix: raw[:displayPrefix], KeyHash: hash,
			ExpiresAt: in.ExpiresAt, AllowedIPs: allowedIPs, RpmLimit: in.RPM, TpmLimit: in.TPM,
			MaxConcurrency: in.Concurrency, QuotaLimit: in.QuotaLimit}
		g := store.G(ctx, tx)
		if err := g.Create(&k).Error; err != nil {
			return err
		}
		id = k.ID
		if s.Cipher == nil {
			return nil
		}
		sealed, err := s.Cipher.Encrypt([]byte(raw), []byte("apikey:"+strconv.FormatInt(id, 10)))
		if err != nil {
			return err
		}
		return g.Model(&model.APIKey{}).Where("id = ?", id).Update("key_cipher", sealed).Error
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	// A negative lookup of this hash cannot be cached (the key is new), but
	// drop it anyway to be safe.
	s.dropCache(ctx, hash)
	k, err := s.loadKey(ctx, id, uid)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	k.UserEmail = ""
	k.Key = raw
	httpapi.Created(c, k)
}

// updateMine edits the owner's key: its name, expiry and IP allowlist, its
// status (active | disabled), its group (one the owner may use), its caps,
// and resets the quota it used. Authentication
// reads them on every request, so the next call already sees the change.
func (s *Service) updateMine(c *gin.Context) {
	ctx := audit.Context(c)
	uid, _ := core.UserID(ctx)
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	var in keyUpdate
	if !httpapi.BindJSON(c, &in) {
		return
	}
	if err := in.normalize(ctx); err != nil {
		httpapi.Fail(c, err)
		return
	}
	var hash string
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		if in.GroupID != nil {
			if err := requireGroup(ctx, tx, uid, *in.GroupID); err != nil {
				return err
			}
		}
		var err error
		if hash, err = updateOwned(ctx, tx, id, uid, in.changes()); err != nil {
			return err
		}
		return audit.Audit(ctx, tx, uid, "apikey.update", "apikey", strconv.FormatInt(id, 10), in)
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.dropCache(ctx, hash)
	k, err := s.loadKey(ctx, id, uid)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	k.UserEmail = ""
	httpapi.OK(c, k)
}

// keyUpdate is the body of PATCH /me/api-keys/:id (absent fields stay) and
// its audit detail. expires_at null removes the expiry.
type keyUpdate struct {
	Name        *string          `json:"name,omitempty"`
	ExpiresAt   json.RawMessage  `json:"expires_at,omitempty"`
	AllowedIPs  *[]string        `json:"allowed_ips,omitempty"`
	Status      *string          `json:"status,omitempty"`
	GroupID     *int64           `json:"group_id,omitempty"`
	RPM         *int             `json:"rpm_limit,omitempty"`
	TPM         *int64           `json:"tpm_limit,omitempty"`
	Concurrency *int             `json:"max_concurrency,omitempty"`
	QuotaLimit  *decimal.Decimal `json:"quota_limit,omitempty"`
	// ResetQuota sets quota_used back to 0.
	ResetQuota bool `json:"reset_quota,omitempty"`

	expiry *time.Time // ExpiresAt decoded
}

// changes are the columns the update writes: the fields present, quota_used
// back to 0 on ResetQuota, and expires_at (NULL included) when given.
func (u *keyUpdate) changes() map[string]any {
	set := map[string]any{}
	store.SetIf(set, "status", u.Status)
	store.SetIf(set, "group_id", u.GroupID)
	store.SetIf(set, "rpm_limit", u.RPM)
	store.SetIf(set, "tpm_limit", u.TPM)
	store.SetIf(set, "max_concurrency", u.Concurrency)
	store.SetIf(set, "quota_limit", u.QuotaLimit)
	if u.ResetQuota {
		set["quota_used"] = decimal.Zero
	}
	store.SetIf(set, "name", u.Name)
	if u.AllowedIPs != nil {
		set["allowed_ips"] = store.Strings(*u.AllowedIPs)
	}
	if u.ExpiresAt != nil {
		set["expires_at"] = u.expiry
	}
	return set
}

// updateOwned writes set to the owner's live key id and returns the key's
// hash; notFound when there is no such key.
func updateOwned(ctx context.Context, q *gorm.DB, id, ownerID int64, set map[string]any) (string, error) {
	var k model.APIKey
	res := ownedUpdate(store.G(ctx, q), &k, id, ownerID, set)
	switch {
	case res.Error != nil:
		return "", res.Error
	case res.RowsAffected == 0:
		return "", notFound(ctx)
	}
	return k.KeyHash, nil
}

// ownedUpdate writes set to the owner's live key id, returning its hash into
// k.
func ownedUpdate(db *gorm.DB, k *model.APIKey, id, ownerID int64, set map[string]any) *gorm.DB {
	return owned(db, id, ownerID).Model(k).Clauses(clause.Returning{Columns: []clause.Column{{Name: "key_hash"}}}).Updates(set)
}

// normalize checks the fields given and puts the name and allowlist in their
// stored form.
func (u *keyUpdate) normalize(ctx context.Context) error {
	if reflect.ValueOf(*u).IsZero() {
		return core.ErrInvalidArgument.WithMessage(t(ctx, "nothing to update", "没有要修改的内容"))
	}
	if u.Name != nil {
		name, err := validName(ctx, *u.Name)
		if err != nil {
			return err
		}
		u.Name = &name
	}
	if u.ExpiresAt != nil {
		if err := json.Unmarshal(u.ExpiresAt, &u.expiry); err != nil {
			return core.InvalidFields(core.FieldError{Field: "expires_at", Code: "invalid", Message: t(ctx, "expiry must be an RFC 3339 time or null", "过期时间须为 RFC 3339 时间或 null")})
		}
		if err := futureExpiry(ctx, u.expiry); err != nil {
			return err
		}
	}
	if u.AllowedIPs != nil {
		entries, err := normalizeAllowedIPs(ctx, *u.AllowedIPs)
		if err != nil {
			return err
		}
		u.AllowedIPs = &entries
	}
	if u.Status != nil && *u.Status != "active" && *u.Status != "disabled" {
		return core.InvalidFields(core.FieldError{Field: "status", Code: "invalid",
			Message: t(ctx, "status must be active or disabled", "状态只能是 active 或 disabled")})
	}
	return validCaps(ctx, core.KeyLimits{RPM: deref(u.RPM), TPM: deref(u.TPM), Concurrency: deref(u.Concurrency)}, deref(u.QuotaLimit))
}

func deref[T any](p *T) (v T) {
	if p != nil {
		v = *p
	}
	return v
}

// Upper bounds of the caps (0 = unlimited): far above any real use, they only
// keep the numbers sane.
const (
	maxRPM         = 1_000_000
	maxTPM         = 1_000_000_000_000
	maxConcurrency = 100_000
)

// maxQuota bounds a key's quota in USD (numeric(20,8) holds 12 integer digits).
var maxQuota = decimal.NewFromInt(1_000_000_000)

// validCaps checks the caps and the quota (USD, at most 8 decimals).
func validCaps(ctx context.Context, l core.KeyLimits, quota decimal.Decimal) error {
	var bad []core.FieldError
	if quota.IsNegative() || quota.GreaterThan(maxQuota) || !quota.Equal(quota.Round(8)) {
		bad = append(bad, core.FieldError{Field: "quota_limit", Code: "invalid",
			Message: t(ctx, "must be between 0 (unlimited) and 1000000000 USD with at most 8 decimals", "须在 0（不限）到 1000000000 美元之间，最多 8 位小数")})
	}
	for _, c := range []struct {
		field  string
		v, max int64
	}{{"rpm_limit", int64(l.RPM), maxRPM}, {"tpm_limit", l.TPM, maxTPM}, {"max_concurrency", int64(l.Concurrency), maxConcurrency}} {
		if c.v < 0 || c.v > c.max {
			n := strconv.FormatInt(c.max, 10)
			bad = append(bad, core.FieldError{Field: c.field, Code: "invalid",
				Message: t(ctx, "must be between 0 (unlimited) and "+n, "须在 0（不限）到 "+n+" 之间")})
		}
	}
	if len(bad) > 0 {
		return core.InvalidFields(bad...)
	}
	return nil
}

func (s *Service) revealMine(c *gin.Context) {
	uid, _ := core.UserID(c.Request.Context())
	s.reveal(c, uid)
}

func (s *Service) rotateMine(c *gin.Context) {
	uid, _ := core.UserID(c.Request.Context())
	s.rotate(c, uid)
}

func (s *Service) rotate(c *gin.Context, ownerID int64) {
	httpapi.NoStore(c)
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	ctx := audit.Context(c)
	if s.Cipher == nil {
		httpapi.Fail(c, core.ErrUnavailable)
		return
	}
	raw, err := generateKey()
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	sealed, err := s.Cipher.Encrypt([]byte(raw), []byte("apikey:"+strconv.FormatInt(id, 10)))
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	var oldHash string
	err = s.db.Tx(ctx, func(tx *gorm.DB) error {
		g := store.G(ctx, tx)
		if err := owned(g, id, ownerID).Select("key_hash").Clauses(store.ForUpdate()).Take(&oldHash).Error; err != nil {
			if store.IsNoRows(err) {
				return notFound(ctx)
			}
			return err
		}
		if err := g.Model(&model.APIKey{}).Where("id = ?", id).
			Updates(map[string]any{"key_hash": HashKey(raw), "key_prefix": raw[:displayPrefix], "key_cipher": sealed}).Error; err != nil {
			return err
		}
		uid, _ := core.UserID(ctx)
		return audit.Audit(ctx, tx, uid, "apikey.rotate", "apikey", strconv.FormatInt(id, 10), nil)
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.dropCache(ctx, oldHash)
	s.dropCache(ctx, HashKey(raw))
	k, err := s.loadKey(ctx, id, ownerID)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	k.UserEmail = ""
	k.Key = raw
	httpapi.OK(c, k)
}

// owned narrows api_keys to the owner's live key id.
func owned(db *gorm.DB, id, ownerID int64) *gorm.DB {
	return db.Model(&model.APIKey{}).Scopes(ownedKey("api_keys", id, ownerID))
}

// ownedKey narrows the api_keys rows named table (the table itself, or
// keyView's "k") to the owner's live key id.
func ownedKey(table string, id, ownerID int64) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		return db.Where(table+".id = ? AND "+table+".user_id = ? AND "+table+".deleted_at IS NULL", id, ownerID)
	}
}

func (s *Service) reveal(c *gin.Context, ownerID int64) {
	httpapi.NoStore(c)
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	ctx := audit.Context(c)
	var k model.APIKey
	err := owned(store.G(ctx, s.db.Gorm), id, ownerID).Select("key_cipher").Take(&k).Error
	sealed := k.KeyCipher
	if store.IsNoRows(err) {
		httpapi.Fail(c, notFound(ctx))
		return
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if len(sealed) == 0 || s.Cipher == nil {
		httpapi.Fail(c, core.ErrConflict.WithMessage(t(ctx, "This legacy key cannot be recovered. Create a new key to enable copying.", "此旧密钥无法恢复，请新建密钥以启用复制。")))
		return
	}
	raw, err := s.Cipher.Decrypt(sealed, []byte("apikey:"+strconv.FormatInt(id, 10)))
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	uid, _ := core.UserID(ctx)
	if err := audit.Audit(ctx, s.db.Gorm, uid, "apikey.reveal", "apikey", strconv.FormatInt(id, 10), nil); err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, gin.H{"key": string(raw)})
}

func (s *Service) softDelete(ctx context.Context, id int64, ownerID int64) error {
	hash, err := updateOwned(ctx, s.db.Gorm, id, ownerID, map[string]any{"deleted_at": gorm.Expr("now()")})
	if err != nil {
		return err
	}
	s.dropCache(ctx, hash)
	return nil
}

func (s *Service) deleteMine(c *gin.Context) {
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	uid, _ := core.UserID(c.Request.Context())
	if err := s.softDelete(c.Request.Context(), id, uid); err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.NoContent(c)
}
