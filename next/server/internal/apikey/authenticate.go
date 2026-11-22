package apikey

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/segment"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// cached holds one authoritative database read. Authentication and scheduling
// facts are deliberately not cached: a concurrent revoke must not be undone
// by a reader refilling a Redis entry after invalidation.
type cached struct {
	Found           bool
	KeyID           int64
	UserID          int64
	KeyStatus       string
	ExpiresAt       *time.Time
	UserActive      bool
	GroupID         int64
	GroupName       string
	GroupStatus     string
	RateMultiplier  decimal.Decimal // the group's base rate
	ModelAllowlist  []string
	ModelFilterMode string
	// GroupEnvironment is the group layer of the environment (§74.3).
	GroupEnvironment core.EnvironmentValues
	GroupAvailable   bool
	AllowedIPs       []string
	KeyLimits        core.KeyLimits
	KeyQuota         core.KeyQuota
	// GroupLossPolicy is the group's conversion loss policy (§88).
	GroupLossPolicy string
	// SegmentID is the owner's segment and RateOverride its own rate in the
	// group (nil = none).
	SegmentID    int64
	RateOverride *decimal.Decimal
}

// Authenticate resolves a raw client key into a principal.
func (s *Service) Authenticate(ctx context.Context, rawKey string) (*core.APIKeyPrincipal, error) {
	rawKey = strings.TrimSpace(rawKey)
	if len(rawKey) != keyLen || !strings.HasPrefix(rawKey, KeyPrefix) {
		return nil, core.ErrUnauthenticated.WithMessage("invalid api key")
	}
	e, err := s.lookup(ctx, HashKey(rawKey))
	if err != nil {
		return nil, err
	}
	if err := usable(ctx, e); err != nil {
		return nil, err
	}
	ok, err := s.authz.Can(ctx, e.UserID, "gateway:use")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, core.ErrPermissionDenied.WithDetails(map[string]any{"permission": "gateway:use"})
	}
	s.touch(e.KeyID)
	return e.principal(), nil
}

// usable refuses a key that is unknown, not active or expired, of a disabled
// user, called from an IP it does not allow, or whose group cannot serve it.
func usable(ctx context.Context, e *cached) error {
	switch {
	case !e.Found || e.KeyStatus != "active":
		return core.ErrUnauthenticated.WithMessage("invalid api key")
	case e.ExpiresAt != nil && !time.Now().Before(*e.ExpiresAt):
		return core.ErrUnauthenticated.WithMessage("api key expired")
	case !e.UserActive:
		return core.ErrUnauthenticated.WithMessage("user disabled")
	case !ipAllowed(core.ClientIP(ctx), e.AllowedIPs):
		return core.NewError(403, "ip_not_allowed", t(ctx, "client IP is not allowed for this API key", "调用 IP 不在此 API Key 的白名单内"))
	}
	return groupError(ctx, e)
}

// principal is what the gateway knows of the caller of a usable key.
func (e *cached) principal() *core.APIKeyPrincipal {
	allow := e.ModelAllowlist
	if allow == nil {
		allow = []string{}
	}
	return &core.APIKeyPrincipal{
		KeyID:        e.KeyID,
		UserID:       e.UserID,
		SegmentID:    e.SegmentID,
		RateOverride: e.RateOverride,
		KeyLimits:    e.KeyLimits,
		KeyQuota:     e.KeyQuota,
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
	}
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
		u.segment_id, sg.rate_override`

// lookupFrom is lookupQuery's FROM: the joins as one text, which renders
// as Joins would and allocates less on this path.
const lookupFrom = "api_keys k" +
	" JOIN users u ON u.id = k.user_id" +
	" LEFT JOIN groups g ON g.id = k.group_id" +
	" LEFT JOIN segment_groups sg ON sg.segment_id = u.segment_id AND sg.group_id = g.id"

// lookupQuery reads lookupColumns of the key hash in one statement. It runs
// on every gateway request: its row is scanned through Row into local
// variables, the GORM form that allocates least (BenchmarkHotKeyLookup;
// internal/store/gormtest/BASELINE.md).
func lookupQuery(db *gorm.DB, hash string) *gorm.DB {
	return db.Table(lookupFrom).Select(lookupColumns).Where("k.key_hash = ? AND k.deleted_at IS NULL", hash)
}

func (s *Service) lookup(ctx context.Context, hash string) (*cached, error) {
	e := &cached{}
	// database/sql hands jsonb over as its document: JSON decodes it.
	var allow store.JSON[[]string]
	var env store.JSON[core.EnvironmentValues]
	err := lookupQuery(store.G(ctx, s.db.Gorm), hash).Row().Scan(
		&e.KeyID, &e.UserID, &e.KeyStatus, &e.ExpiresAt, &e.UserActive,
		&e.GroupID, &e.GroupName, &e.GroupStatus, &e.RateMultiplier, &allow, &e.ModelFilterMode, &env, &e.GroupAvailable, (*store.Strings)(&e.AllowedIPs),
		&e.KeyLimits.RPM, &e.KeyLimits.TPM, &e.KeyLimits.Concurrency, &e.KeyQuota.Limit, &e.KeyQuota.Used, &e.GroupLossPolicy,
		&e.SegmentID, &e.RateOverride)
	e.ModelAllowlist, e.GroupEnvironment = allow.V, env.V
	switch {
	case store.IsNoRows(err):
		e = &cached{Found: false}
	case err != nil:
		return nil, err
	default:
		e.Found = true
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
