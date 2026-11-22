package apikey

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

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

// keyCreate is the body of POST /me/api-keys.
type keyCreate struct {
	Name       string     `json:"name"`
	GroupID    int64      `json:"group_id"`
	ExpiresAt  *time.Time `json:"expires_at"`
	AllowedIPs []string   `json:"allowed_ips"`
	core.KeyLimits
	QuotaLimit decimal.Decimal `json:"quota_limit"`
}

func (s *Service) createMine(c *gin.Context) {
	httpapi.NoStore(c)
	ctx := c.Request.Context()
	uid, _ := core.UserID(ctx)
	var in keyCreate
	if !httpapi.BindJSON(c, &in) {
		return
	}
	k, raw, err := in.newKey(ctx, uid)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if err := s.db.Tx(ctx, func(tx *gorm.DB) error { return s.insertKey(ctx, tx, k, raw) }); err != nil {
		httpapi.Fail(c, err)
		return
	}
	// A negative lookup of this hash cannot be cached (the key is new), but
	// drop it anyway to be safe.
	s.dropCache(ctx, k.KeyHash)
	out, err := s.ownKey(ctx, k.ID, uid)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	out.Key = raw
	httpapi.Created(c, out)
}

// newKey checks the body and generates the key: its row for the owner uid
// and the raw key.
func (in *keyCreate) newKey(ctx context.Context, uid int64) (*model.APIKey, string, error) {
	name, allowedIPs, err := in.check(ctx)
	if err != nil {
		return nil, "", err
	}
	raw, err := generateKey()
	if err != nil {
		return nil, "", err
	}
	return &model.APIKey{UserID: uid, GroupID: &in.GroupID, Name: name, KeyPrefix: raw[:displayPrefix], KeyHash: HashKey(raw),
		ExpiresAt: in.ExpiresAt, AllowedIPs: allowedIPs, RpmLimit: in.RPM, TpmLimit: in.TPM,
		MaxConcurrency: in.Concurrency, QuotaLimit: in.QuotaLimit}, raw, nil
}

// check validates a new key's caps, name, expiry and IP allowlist, and
// returns the name and the allowlist to store.
func (in *keyCreate) check(ctx context.Context) (string, []string, error) {
	if err := validCaps(ctx, in.KeyLimits, in.QuotaLimit); err != nil {
		return "", nil, err
	}
	name, err := validName(ctx, in.Name)
	if err != nil {
		return "", nil, err
	}
	if err := futureExpiry(ctx, in.ExpiresAt); err != nil {
		return "", nil, err
	}
	allowedIPs, err := normalizeAllowedIPs(ctx, in.AllowedIPs)
	return name, allowedIPs, err
}

// insertKey writes the new key k of raw in its group, which the owner must
// be able to use, and seals raw for reveal when a cipher is set.
func (s *Service) insertKey(ctx context.Context, tx *gorm.DB, k *model.APIKey, raw string) error {
	if err := requireGroup(ctx, tx, k.UserID, *k.GroupID); err != nil {
		return err
	}
	// The caps and allowed_ips have database defaults: GORM leaves them
	// out of the INSERT when zero, and those defaults (0, '{}') are the
	// zero values.
	g := store.G(ctx, tx)
	if err := g.Create(k).Error; err != nil {
		return err
	}
	if s.Cipher == nil {
		return nil
	}
	sealed, err := s.Cipher.Encrypt([]byte(raw), []byte("apikey:"+strconv.FormatInt(k.ID, 10)))
	if err != nil {
		return err
	}
	return g.Model(&model.APIKey{}).Where("id = ?", k.ID).Update("key_cipher", sealed).Error
}

// ownKey is the owner's view of the key id: without the owner's email.
func (s *Service) ownKey(ctx context.Context, id, ownerID int64) (*APIKey, error) {
	k, err := s.loadKey(ctx, id, ownerID)
	if err != nil {
		return nil, err
	}
	k.UserEmail = ""
	return k, nil
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
	in, ok := bindUpdate(ctx, c)
	if !ok {
		return
	}
	var hash string
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		var err error
		hash, err = in.apply(ctx, tx, id, uid)
		return err
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.dropCache(ctx, hash)
	k, err := s.ownKey(ctx, id, uid)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, k)
}

// bindUpdate reads and normalizes the body of PATCH /me/api-keys/:id;
// false when it has answered the request with the error.
func bindUpdate(ctx context.Context, c *gin.Context) (*keyUpdate, bool) {
	var in keyUpdate
	if !httpapi.BindJSON(c, &in) {
		return nil, false
	}
	if err := in.normalize(ctx); err != nil {
		httpapi.Fail(c, err)
		return nil, false
	}
	return &in, true
}

// apply writes the update to the key id of uid, in a group uid may use,
// audits it and returns the key's hash.
func (u *keyUpdate) apply(ctx context.Context, tx *gorm.DB, id, uid int64) (string, error) {
	if u.GroupID != nil {
		if err := requireGroup(ctx, tx, uid, *u.GroupID); err != nil {
			return "", err
		}
	}
	hash, err := updateOwned(ctx, tx, id, uid, u.changes())
	if err != nil {
		return "", err
	}
	return hash, audit.Audit(ctx, tx, uid, "apikey.update", "apikey", strconv.FormatInt(id, 10), u)
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
	raw, sealed, err := s.sealedKey(id)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	var oldHash string
	err = s.db.Tx(ctx, func(tx *gorm.DB) error {
		var err error
		oldHash, err = rotateKey(ctx, tx, id, ownerID, raw, sealed)
		return err
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.dropCache(ctx, oldHash)
	s.dropCache(ctx, HashKey(raw))
	k, err := s.ownKey(ctx, id, ownerID)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	k.Key = raw
	httpapi.OK(c, k)
}

// sealedKey generates a new raw key for the key id and seals it for reveal.
func (s *Service) sealedKey(id int64) (string, []byte, error) {
	if s.Cipher == nil {
		return "", nil, core.ErrUnavailable
	}
	raw, err := generateKey()
	if err != nil {
		return "", nil, err
	}
	sealed, err := s.Cipher.Encrypt([]byte(raw), []byte("apikey:"+strconv.FormatInt(id, 10)))
	return raw, sealed, err
}

// rotateKey replaces the locked key id of ownerID by raw and returns the
// hash it had.
func rotateKey(ctx context.Context, tx *gorm.DB, id, ownerID int64, raw string, sealed []byte) (string, error) {
	if err := store.RequireTx(tx); err != nil {
		return "", err
	}
	g := store.G(ctx, tx)
	var oldHash string
	if err := owned(g, id, ownerID).Select("key_hash").Clauses(store.ForUpdate()).Take(&oldHash).Error; err != nil {
		if store.IsNoRows(err) {
			return "", notFound(ctx)
		}
		return "", err
	}
	if err := g.Model(&model.APIKey{}).Where("id = ?", id).
		Updates(map[string]any{"key_hash": HashKey(raw), "key_prefix": raw[:displayPrefix], "key_cipher": sealed}).Error; err != nil {
		return "", err
	}
	uid, _ := core.UserID(ctx)
	return oldHash, audit.Audit(ctx, tx, uid, "apikey.rotate", "apikey", strconv.FormatInt(id, 10), nil)
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
	raw, err := s.unseal(ctx, id, ownerID)
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

// unseal is the raw key id of ownerID, sealed when it was created or
// rotated.
func (s *Service) unseal(ctx context.Context, id, ownerID int64) ([]byte, error) {
	var k model.APIKey
	err := owned(store.G(ctx, s.db.Gorm), id, ownerID).Select("key_cipher").Take(&k).Error
	if store.IsNoRows(err) {
		return nil, notFound(ctx)
	}
	if err != nil {
		return nil, err
	}
	if len(k.KeyCipher) == 0 || s.Cipher == nil {
		return nil, core.ErrConflict.WithMessage(t(ctx, "This legacy key cannot be recovered. Create a new key to enable copying.", "此旧密钥无法恢复，请新建密钥以启用复制。"))
	}
	return s.Cipher.Decrypt(k.KeyCipher, []byte("apikey:"+strconv.FormatInt(id, 10)))
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

// Upper bounds of the caps (0 = unlimited): far above any real use, they only
// keep the numbers sane.
const (
	maxRPM         = 1_000_000
	maxTPM         = 1_000_000_000_000
	maxConcurrency = 100_000
)
