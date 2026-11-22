// Package apikey implements user API keys: owner-only self-service endpoints,
// and the gateway authenticator (core.APIKeyAuthenticator).
package apikey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/secret"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/segment"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
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
		cond, args, err := searchCond(ctx, q)
		if err != nil {
			return nil, err
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

// searchCond is the condition of the search text q: the name, or for the
// start of a key also the key prefix.
func searchCond(ctx context.Context, q string) (string, []any, error) {
	// PostgreSQL takes neither as text.
	if !utf8.ValidString(q) || strings.ContainsRune(q, 0) {
		return "", nil, core.InvalidFields(core.FieldError{Field: "q", Code: "invalid",
			Message: t(ctx, "q must be valid UTF-8 text", "q 须为合法的 UTF-8 文本")})
	}
	cond, args := "k.name ILIKE ?", []any{"%" + store.Like(q) + "%"}
	if prefix := searchedPrefix(q); prefix != "" {
		cond, args = "(k.name ILIKE ? OR k.key_prefix ILIKE ?)", append(args, store.Like(prefix)+"%")
	}
	return cond, args, nil
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
