package install

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// Role grants of a consent (DECISIONS "插件升级授权" C, D2, D3): the
// permissions a version creates go to the roles of their manifest defaults,
// of the consent's overrides and to the holders of their inheritFrom keys.
// Each grant goes to review instead (withheld) when the consenting actor may
// not name its role (nameable), and on an upgrade nobody consented to when
// it needs consent: an upgrade is approved without anyone only when every
// such grant is of a low risk permission to the built-in admin, operator,
// supplier or supplier_agent role (autoGrantRoles); otherwise someone
// decides on the consent page. Built-in plugins upgrade with the core and
// are approved by the system all the same (node readiness needs it), so
// their grants that need consent are withheld and listed for review
// instead.

// autoGrantRoles are the built-in roles an upgrade may hand low risk new
// permissions without consent. A supplier agent takes the defaults of
// users and suppliers (manifest.RoleDefaultsFrom) without consent even
// when the users' grant waits for it, so that it keeps holding what the
// users it manages may be given (CONTRACTS §103.1).
var autoGrantRoles = []string{"admin", "operator", "supplier", "supplier_agent"}

// Audit actions of consents and the role grants they withheld.
const (
	AuditConsent           = "plugin.consent"
	AuditConsentAuto       = "plugin.consent.auto"
	AuditRoleGrantsConfirm = "plugin.role_grants.confirm"
	AuditRoleGrantsReject  = "plugin.role_grants.reject"
	// AuditRoleGrantsCarry lists for review what renamed keys' holders of
	// after the consent would inherit (carryRenamedHolders).
	AuditRoleGrantsCarry = "plugin.role_grants.carry"
)

// errRoleGrantsNeedConsent stops an automatic consent whose role grants
// need someone's decision; the version stays awaiting consent.
var errRoleGrantsNeedConsent = errors.New("the version's role grants need consent")

// RoleGrant is one (role, scope) a permission created by a consent goes to.
type RoleGrant struct {
	// Version is the plugin version whose consent withheld the grant (the
	// review list only).
	Version string `json:"version,omitempty"`
	// Permission is the qualified permission key.
	Permission string `json:"permission"`
	Role       string `json:"role"`
	// Scope is the canonical suffix: "" every row, or "own", "value/input",
	// "flow/3" (casbin_rule's scope column, CONTRACTS §99).
	Scope string `json:"scope"`
	Risk  string `json:"risk"` // manifest.RiskLow | RiskSensitive | RiskCritical
	// Via is "default" (manifest defaults), "override" (the consent named
	// it) or "inherit" (a holder of an inheritFrom key).
	Via          string `json:"via"`
	NeedsConsent bool   `json:"needs_consent"` // the upgrade rule (autoGrantRoles)
	Nameable     bool   `json:"nameable"`      // the consenting actor may name the role (D2, D3)
	Withheld     bool   `json:"withheld"`      // waits for review instead of being granted
}

const (
	viaDefault  = "default"
	viaOverride = "override"
	viaInherit  = "inherit"
)

// InheritSkip is an inheritFrom source planRoleGrants left out for a
// permission (Reason).
type InheritSkip struct {
	Permission string `json:"permission"`
	From       string `json:"from"`
	Reason     string `json:"reason"`
}

// Why planRoleGrants left an inheritFrom source out (InheritSkip.Reason).
const (
	skipRisk      = "risk"       // a key not critical, for a critical permission
	skipScopeType = "scope_type" // a scoped grant of a key of another scope_type
	skipScope     = "scope"      // a scoped grant the permission's scope does not take (own, a value)
)

// planRoleGrants lists the role grants a consent makes for every
// userPermission the catalog does not have yet (existing): its roles
// (roleGrants; via override for the roles overrides names) and the roles
// holding one of its inheritFrom keys now (inheritGrants), so only grants
// made before this consent count; the inheritFrom sources left out are
// returned. On a first install nothing needs consent: someone (or the
// system) consents anyway.
func (s *Service) planRoleGrants(ctx context.Context, q *gorm.DB, m *manifest.Manifest, existing catalogPerms,
	roles, overrides map[string]map[string][]string, upgrade bool,
) ([]RoleGrant, []InheritSkip, error) {
	holders, err := s.inheritHolders(ctx, q, m, existing)
	if err != nil {
		return nil, nil, err
	}
	var out []RoleGrant
	var skipped []InheritSkip
	for _, up := range m.UserPermissions {
		key := manifest.PermissionKey(m.ID(), up.Key)
		if existing.has(key) {
			continue
		}
		var gs []RoleGrant
		for _, role := range slices.Sorted(maps.Keys(roles[up.Key])) {
			via := viaDefault
			if _, named := overrides[up.Key][role]; named {
				via = viaOverride
			}
			for _, scope := range roles[up.Key][role] {
				gs = append(gs, RoleGrant{Permission: key, Role: role, Scope: scope, Risk: up.RiskLevel(), Via: via})
			}
		}
		inherited, skips := inheritGrants(m, up, existing, holders)
		skipped = append(skipped, skips...)
		for _, g := range append(gs, inherited...) {
			if slices.ContainsFunc(out, func(o RoleGrant) bool { return o.Permission == g.Permission && o.Role == g.Role && o.Scope == g.Scope }) {
				continue
			}
			g.NeedsConsent = upgrade && (g.Risk != manifest.RiskLow || !slices.Contains(autoGrantRoles, g.Role))
			out = append(out, g)
		}
	}
	return out, skipped, nil
}

// inheritHolders reads the grants of the inheritFrom keys of m's
// userPermissions that the catalog has (existing).
func (s *Service) inheritHolders(ctx context.Context, q *gorm.DB, m *manifest.Manifest, existing catalogPerms) (map[string][]core.PermissionHolder, error) {
	var from []string
	for _, up := range m.UserPermissions {
		for _, k := range up.InheritFrom {
			if key := manifest.PermissionKey(m.ID(), k); existing.has(key) {
				from = append(from, key)
			}
		}
	}
	if len(from) == 0 {
		return nil, nil
	}
	cat, err := s.catalog()
	if err != nil {
		return nil, err
	}
	return cat.PermissionHolders(ctx, q, from)
}

// inheritGrants is what up, a permission of m created now, inherits from
// holders, the grants of its inheritFrom keys (existing: the catalog's
// entries of those keys). A scoped grant is kept only when the old key
// ranges over the same resource (scope_type) and up's scope takes it
// (manifest.PermissionScope.Takes: own allowed, the value declared), and a
// critical permission inherits from critical keys only (the manifest check
// cannot see the risk of a key no longer declared); the sources left out
// are returned with why. Super admin roles hold no grants (holders).
func inheritGrants(m *manifest.Manifest, up manifest.UserPermission, existing catalogPerms, holders map[string][]core.PermissionHolder) ([]RoleGrant, []InheritSkip) {
	key := manifest.PermissionKey(m.ID(), up.Key)
	var scope manifest.PermissionScope
	if up.Scope != nil {
		scope = *up.Scope
	}
	scopeType := core.PluginScopeType(up.Scope)
	var gs []RoleGrant
	var skipped []InheritSkip
	for _, k := range up.InheritFrom {
		old := manifest.PermissionKey(m.ID(), k)
		src := existing[old]
		for _, h := range holders[old] {
			var skip InheritSkip
			switch {
			case up.RiskLevel() == manifest.RiskCritical && src.Risk != manifest.RiskCritical:
				skip = InheritSkip{Permission: key, From: old, Reason: skipRisk}
			case h.Scope != "" && src.ScopeType != scopeType:
				skip = InheritSkip{Permission: key, From: old, Reason: skipScopeType}
			case !scope.Takes(h.Scope):
				skip = InheritSkip{Permission: key, From: old, Reason: skipScope}
			default:
				gs = append(gs, RoleGrant{Permission: key, Role: h.Role, Scope: h.Scope, Risk: up.RiskLevel(), Via: viaInherit})
				continue
			}
			if !slices.Contains(skipped, skip) {
				skipped = append(skipped, skip)
			}
		}
	}
	return gs, skipped
}

// holds reports whether hs, the grants of a permission, give role the
// scope sfx: every row, or that scope.
func holds(hs []core.PermissionHolder, role, sfx string) bool {
	return slices.ContainsFunc(hs, func(h core.PermissionHolder) bool { return h.Role == role && (h.Scope == "" || h.Scope == sfx) })
}

// needConsent reports whether any grant of gs needs consent.
func needConsent(gs []RoleGrant) bool {
	return slices.ContainsFunc(gs, func(g RoleGrant) bool { return g.NeedsConsent })
}

// rolesOf lists the roles of gs, sorted and deduplicated.
func rolesOf(gs []RoleGrant) []string {
	out := make([]string, 0, len(gs))
	for _, g := range gs {
		out = append(out, g.Role)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// withholdGrants splits plan into the grants made now and the ones withheld
// for review (pending), and returns the grants the consent syncs with:
// every permission created now goes to exactly its planned (role, scope)
// pairs not withheld, so that what is granted is what was planned
// (inheritance resolved now cannot pick up a holder added meanwhile).
func withholdGrants(m *manifest.Manifest, existing catalogPerms, plan []RoleGrant) (map[string]map[string][]string, []RoleGrant) {
	roles := map[string]map[string][]string{}
	pending := []RoleGrant{}
	for _, up := range m.UserPermissions {
		if !existing.has(manifest.PermissionKey(m.ID(), up.Key)) {
			roles[up.Key] = map[string][]string{}
		}
	}
	for _, g := range plan {
		if g.Withheld {
			pending = append(pending, g)
			continue
		}
		local, _ := manifest.LocalPermission(m.ID(), g.Permission)
		roles[local][g.Role] = append(roles[local][g.Role], g.Scope)
	}
	return roles, pending
}

// auditAutoGrants records the role grants of a consent nobody gave (an
// upgrade carried over, by its uploader; a built-in plugin, by the system)
// and the ones withheld for review.
// Nothing is written when there are none.
func auditAutoGrants(ctx context.Context, tx *gorm.DB, actorID int64, key, version string, st *settlement) error {
	if len(st.plan) == 0 {
		return nil
	}
	granted := []RoleGrant{}
	for _, g := range st.plan {
		if !g.Withheld {
			granted = append(granted, g)
		}
	}
	return audit.Audit(ctx, tx, actorID, AuditConsentAuto, "plugin", key, map[string]any{
		"version": version, "granted": granted, "pending": st.pending, "review_required": len(st.pending) > 0,
		"inherit_skipped": st.skipped,
	})
}

// PendingRoleGrants lists the role grants consents of key withheld for
// review (version "" for every version) since it was installed, and those
// an activation found renamed keys' late holders to inherit
// (carryRenamedHolders), that nobody confirmed or rejected since and that
// still mean something: the permission is one of the plugin's and not
// removed, its scope as now declared takes the grant's (an upgrade may have
// dropped own or a value), the role exists and holds neither every row of
// it nor the grant's scope. A confirm or reject clears its version's list;
// a reject also clears the same (permission, role) under every version
// whose consent was recorded before it (earlier or later versions alike),
// since the list shows each grant once (under its earliest version). Risk
// is the catalog's; a grant audited before scopes existed is of every row.
func (s *Service) PendingRoleGrants(ctx context.Context, q *gorm.DB, key, version string) ([]RoleGrant, error) {
	var rows []pendingRoleGrantRow
	if err := pendingRoleGrants(store.G(ctx, q), key, version).Scan(&rows).Error; err != nil || len(rows) == 0 {
		return []RoleGrant{}, err
	}
	listed := make([]RoleGrant, len(rows))
	for i, r := range rows {
		listed[i] = RoleGrant{Version: r.Version, Permission: r.Permission, Role: r.Role, Scope: r.Scope, Via: r.Via,
			NeedsConsent: r.NeedsConsent, Withheld: true}
	}
	cat, err := s.catalog()
	if err != nil {
		return nil, err
	}
	perms, err := loadCatalogPerms(ctx, cat, q, key)
	if err != nil {
		return nil, err
	}
	keys := make([]string, len(listed))
	for i, g := range listed {
		keys[i] = g.Permission
	}
	holders, err := cat.PermissionHolders(ctx, q, keys)
	if err != nil {
		return nil, err
	}
	gs := []RoleGrant{}
	for _, g := range listed {
		p, ok := perms[g.Permission]
		if !ok || perms.removed(g.Permission) || !p.Scope.Takes(g.Scope) || holds(holders[g.Permission], g.Role, g.Scope) ||
			slices.ContainsFunc(gs, func(o RoleGrant) bool { return o.Permission == g.Permission && o.Role == g.Role && o.Scope == g.Scope }) {
			continue
		}
		g.Risk = p.Risk
		gs = append(gs, g)
	}
	return gs, nil
}

// RoleGrantsToReview is PendingRoleGrants of every version as actorID may
// see them: an inherited grant, consented or carried, names a holder of the
// old key, which only who may see holders sees (seesHolders, C4).
func (s *Service) RoleGrantsToReview(ctx context.Context, q *gorm.DB, key string, actorID int64) ([]RoleGrant, error) {
	gs, err := s.PendingRoleGrants(ctx, q, key, "")
	if err != nil {
		return nil, err
	}
	see, err := s.seesHolders(ctx, actorID)
	if err != nil || see {
		return gs, err
	}
	return slices.DeleteFunc(gs, func(g RoleGrant) bool { return g.Via == viaInherit }), nil
}

// seesHolders reports whether actorID may see which roles hold a
// permission (C4): role:read's to see, and the system's.
func (s *Service) seesHolders(ctx context.Context, actorID int64) (bool, error) {
	if isSystem(ctx) {
		return true, nil
	}
	return s.d.Authz.Can(ctx, actorID, string(perm.RoleRead))
}

// ReviewRoleGrants confirms (grants) or rejects the role grants the consent
// of version withheld for review. Either needs what naming those roles on
// the consent page needs (checkGrantable: role:define and CanGrantToRoles,
// critical ones by a superuser), and clears the version's list, audited;
// rejecting grants nothing and also clears the rejected grants other
// versions withheld (PendingRoleGrants).
func (s *Service) ReviewRoleGrants(ctx context.Context, key, version string, confirm bool, actorID int64) ([]RoleGrant, error) {
	ctx, release, guardErr := core.BeginPluginMutation(ctx, s.d.Mutations)
	if guardErr != nil {
		return nil, guardErr
	}
	defer release()
	var gs []RoleGrant
	err := s.d.DB.Tx(ctx, func(tx *gorm.DB) error {
		if err := lockPlugin(store.G(ctx, tx), key); err != nil {
			return err
		}
		var err error
		if gs, err = s.PendingRoleGrants(ctx, tx, key, version); err != nil {
			return err
		}
		if len(gs) == 0 {
			return core.ErrNotFound.WithMessage("no role grants of this version await review")
		}
		if err := s.checkGrantable(ctx, tx, actorID, key, gs); err != nil {
			return err
		}
		action := AuditRoleGrantsReject
		if confirm {
			action = AuditRoleGrantsConfirm
			if err := s.grantReviewed(ctx, tx, key, gs); err != nil {
				return err
			}
		}
		return audit.Audit(ctx, tx, actorID, action, "plugin", key, map[string]any{"version": version, "grants": gs})
	})
	if err != nil {
		return nil, err
	}
	if confirm {
		s.Notify(ctx, key)
	}
	return gs, nil
}

// grantReviewed grants gs, permissions of plugin pluginKey the operator
// confirmed (checked by the caller); a grant of another plugin's permission
// is refused, whatever wrote it to the review list.
func (s *Service) grantReviewed(ctx context.Context, tx *gorm.DB, pluginKey string, gs []RoleGrant) error {
	cat, err := s.catalog()
	if err != nil {
		return err
	}
	byKey := map[string]map[string][]string{}
	for _, g := range gs {
		if _, ok := manifest.LocalPermission(pluginKey, g.Permission); !ok {
			return core.ErrInternal.WithMessage("a reviewed grant names " + g.Permission + ", not a permission of plugin " + pluginKey)
		}
		if byKey[g.Permission] == nil {
			byKey[g.Permission] = map[string][]string{}
		}
		byKey[g.Permission][g.Role] = append(byKey[g.Permission][g.Role], g.Scope)
	}
	for _, k := range slices.Sorted(maps.Keys(byKey)) {
		if err := cat.GrantPluginPermission(ctx, tx, k, byKey[k]); err != nil {
			return err
		}
	}
	return nil
}

// carryRenamedHolders lists for review (AuditRoleGrantsCarry, by the
// system) the grants that holders of an inheritFrom key this activation of
// m retires gained after m's consent would inherit by inheritGrants: a
// consent made while another version runs resolves inheritance then, and
// the old key's grants stop counting at activation (D6), so a holder added
// in between would otherwise lose the feature silently. Only keys live
// before the activation (before, the catalog then) are retired by it: one
// an earlier activation retired was carried then, and its holders since
// are no one's to inherit, so a grant revoked or rejected later never comes
// back with the next upgrade. Grants that consent planned (made or
// withheld), every permission whose roles it overrode (C2: the override
// decided them) and grants a role holds already are left out; nobody
// consented to the rest, hence review rather than a grant. It runs at
// activation, after the catalog sync.
func carryRenamedHolders(ctx context.Context, cat core.PermissionCatalog, tx *gorm.DB, m *manifest.Manifest, before catalogPerms) error {
	var keys []string
	retires := map[string]bool{} // by local key: inherits from a key retired now
	for _, up := range m.UserPermissions {
		for _, from := range up.InheritFrom {
			if old := manifest.PermissionKey(m.ID(), from); before.has(old) && !before.removed(old) {
				keys, retires[up.Key] = append(keys, old), true
			}
		}
		if retires[up.Key] {
			keys = append(keys, manifest.PermissionKey(m.ID(), up.Key))
		}
	}
	if len(keys) == 0 {
		return nil
	}
	perms, err := loadCatalogPerms(ctx, cat, tx, m.ID())
	if err != nil {
		return err
	}
	holders, err := cat.PermissionHolders(ctx, tx, keys)
	if err != nil {
		return err
	}
	planned, overridden, err := consentDecisions(ctx, tx, m.ID(), m.Version)
	if err != nil {
		return err
	}
	pending := []RoleGrant{}
	for _, up := range m.UserPermissions {
		key := manifest.PermissionKey(m.ID(), up.Key)
		if !retires[up.Key] || perms.removed(key) || overridden[up.Key] {
			continue
		}
		// holders has the retired keys alone among up's inheritFrom.
		gs, _ := inheritGrants(m, up, perms, holders)
		for _, g := range gs {
			g.NeedsConsent, g.Withheld = true, true
			if planned[[3]string{g.Permission, g.Role, g.Scope}] || holds(holders[key], g.Role, g.Scope) || slices.Contains(pending, g) {
				continue
			}
			pending = append(pending, g)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	return audit.Audit(ctx, tx, 0, AuditRoleGrantsCarry, "plugin", m.ID(), map[string]any{"version": m.Version, "pending": pending})
}

// consentDecisions reads what the latest consent of key@version decided on
// the role grants of the permissions it created: the (permission, role,
// scope) it planned, made or withheld (role_grants, written by every
// consent), and the local keys its overrides named. None for a version with
// no consent recorded.
func consentDecisions(ctx context.Context, q *gorm.DB, key, version string) (map[[3]string]bool, map[string]bool, error) {
	var overrides store.Strings
	var raw []byte
	err := latestConsentDecisions(store.G(ctx, q), key, version).Row().Scan(&overrides, &raw)
	if store.IsNoRows(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var gs []*RoleGrant // nil entries for a JSON null
	if err := json.Unmarshal(raw, &gs); err != nil {
		return nil, nil, err
	}
	planned := map[[3]string]bool{}
	for _, g := range gs {
		if g != nil {
			planned[[3]string{g.Permission, g.Role, g.Scope}] = true
		}
	}
	overridden := map[string]bool{}
	for _, local := range overrides {
		overridden[local] = true
	}
	return planned, overridden, nil
}

// pendingRoleGrantRow is a row of pendingRoleGrants.
type pendingRoleGrantRow struct {
	Version      string `gorm:"column:version"`
	Permission   string `gorm:"column:permission"`
	Role         string `gorm:"column:role"`
	Scope        string `gorm:"column:scope"`
	Via          string `gorm:"column:via"`
	NeedsConsent bool   `gorm:"column:needs_consent"`
}

// pendingRoleGrants selects the role grants of key withheld for review and
// not cleared since (PendingRoleGrants), of version ("" for every version):
// the pending grants of each listing audit row (g), unless a later row
// cleared its version or rejected that grant, and only of roles that are
// not super admins.
func pendingRoleGrants(db *gorm.DB, key, version string) *gorm.DB {
	cleared := db.Table("audit_logs AS b").Select("1").
		Where("b.target_type = 'plugin' AND b.target_id = a.target_id AND b.action = ANY(?) AND b.id > a.id",
			store.Strings{AuditRoleGrantsConfirm, AuditRoleGrantsReject}).
		Where(`b.detail->>'version' = a.detail->>'version'
			OR (b.action = ? AND b.detail->'grants' @> jsonb_build_array(jsonb_build_object('permission', g->>'permission', 'role', g->>'role')))`,
			AuditRoleGrantsReject)
	q := db.Table("audit_logs AS a").
		Select(`a.detail->>'version' AS version, g->>'permission' AS permission, g->>'role' AS role,
			COALESCE(g->>'scope', '') AS scope, COALESCE(g->>'via', '') AS via,
			COALESCE((g->>'needs_consent')::boolean, true) AS needs_consent`).
		Joins("CROSS JOIN LATERAL jsonb_array_elements(a.detail->'pending') g").
		Where("a.action = ANY(?) AND a.target_type = 'plugin' AND a.target_id = ?",
			store.Strings{AuditConsentAuto, AuditConsent, AuditRoleGrantsCarry}, key).
		Where("jsonb_typeof(a.detail->'pending') = 'array'").
		Where("a.created_at >= (?)", db.Model(&model.Plugin{}).Select("installed_at").Where("key = ?", key)).
		Where("NOT EXISTS (?)", cleared).
		Where("EXISTS (?)", db.Model(&model.Role{}).Select("1").Where("roles.key = g->>'role' AND NOT roles.superuser")).
		Order("a.id, 2, 3, 4")
	if version != "" {
		q = q.Where("a.detail->>'version' = ?", version)
	}
	return q
}

// latestConsentDecisions selects the override keys and the role_grants plan
// of the latest consent of key@version.
func latestConsentDecisions(db *gorm.DB, key, version string) *gorm.DB {
	return db.Table("audit_logs AS a").
		Select(`COALESCE((SELECT array_agg(k) FROM jsonb_object_keys(CASE WHEN jsonb_typeof(a.detail->'new_permission_roles') = 'object'
				THEN a.detail->'new_permission_roles' ELSE '{}' END) k), '{}'),
			COALESCE(a.detail->'role_grants', '[]')`).
		Where("a.action = ? AND a.target_type = 'plugin' AND a.target_id = ? AND a.detail->>'version' = ?", AuditConsent, key, version).
		Order("a.id DESC").Limit(1)
}
