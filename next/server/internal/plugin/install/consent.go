package install

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/dbschema"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/grants"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/rows"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// ConsentRequest is the body of POST /plugins/:key/versions/:version/consent.
type ConsentRequest struct {
	Grants []GrantInput `json:"grants"`
	Denied []string     `json:"denied"`
	// NewPermissionRoles overrides, per local userPermission key, the grants
	// of a permission created now: role -> true (every row) or scope values
	// ("*", "own", an enum key of scope.values or its canonical
	// "value/<key>", a runtime id "flow/3"). A key present replaces the
	// defaults for the roles the actor may name: those it leaves out get
	// nothing, {} grants none; a default to a role the actor may not name
	// still goes to review (D2). Keys absent keep the manifest defaults;
	// inheritFrom applies either way.
	NewPermissionRoles map[string]map[string]manifest.RoleDefault `json:"new_permission_roles,omitempty"`
}

// GrantInput approves one host permission, optionally with a narrower scope.
type GrantInput struct {
	Permission string         `json:"permission"`
	Scope      map[string]any `json:"scope,omitempty"`
}

// ConsentResult is returned after consent.
type ConsentResult struct {
	PluginKey string  `json:"plugin_key"`
	Version   string  `json:"version"`
	Status    string  `json:"status"`
	Upgrade   bool    `json:"upgrade"`
	Grants    []Grant `json:"grants"`
	// Withheld are the role grants this consent left for review (D2).
	Withheld []RoleGrant `json:"withheld"`
}

type decision struct {
	permission string
	scope      map[string]any
	status     string
	carried    bool // kept from a previous consent without re-approval
}

// decide resolves the consent request against the requested permissions.
// Rules: low-risk permissions are granted automatically; optional ones not
// mentioned are denied; on upgrades a permission whose current grant already
// covers what is granted now is carried over, whether the request names it
// or not (it was approved before, so granting it again takes no grant
// right); every other required permission must be granted explicitly.
// A revoked permission (grants.Revoked) is granted only when the request
// names it: required, it must be; optional, it stays revoked, denied or not.
// Granted scopes may be narrower than requested, never wider.
func decide(m *manifest.Manifest, req ConsentRequest, current map[string]Grant, revoked map[string]bool, upgrade bool) ([]decision, []core.FieldError) {
	var errs []core.FieldError
	requested := map[string]manifest.HostPermission{}
	for _, hp := range m.HostPermissions {
		requested[hp.ID] = hp
	}
	grants := map[string]GrantInput{}
	for i, g := range req.Grants {
		f := fmt.Sprintf("grants[%d]", i)
		if _, ok := requested[g.Permission]; !ok {
			errs = append(errs, core.FieldError{Field: f, Code: "not_requested", Message: fmt.Sprintf("permission %q is not requested by the plugin", g.Permission)})
			continue
		}
		if _, dup := grants[g.Permission]; dup {
			errs = append(errs, core.FieldError{Field: f, Code: "duplicate", Message: fmt.Sprintf("permission %q listed twice", g.Permission)})
		}
		grants[g.Permission] = g
	}
	denied := map[string]bool{}
	for i, d := range req.Denied {
		f := fmt.Sprintf("denied[%d]", i)
		if _, ok := requested[d]; !ok {
			errs = append(errs, core.FieldError{Field: f, Code: "not_requested", Message: fmt.Sprintf("permission %q is not requested by the plugin", d)})
			continue
		}
		if _, both := grants[d]; both {
			errs = append(errs, core.FieldError{Field: f, Code: "conflict", Message: fmt.Sprintf("permission %q is both granted and denied", d)})
		}
		denied[d] = true
	}

	// carried reports whether the current grant of id covers scope.
	carried := func(id string, scope map[string]any) bool {
		cur, ok := current[id]
		return upgrade && ok && cur.Status == GrantGranted && pkg.ScopeWithin(scope, cur.Scope)
	}
	var out []decision
	for _, hp := range m.HostPermissions {
		reqScope := requestedScope(m, hp)
		risk := riskOf(hp.ID)
		f := "permission:" + hp.ID
		if g, ok := grants[hp.ID]; ok {
			scope := reqScope
			if g.Scope != nil {
				scope = pkg.NormalizeScope(g.Scope)
				if !pkg.ScopeWithin(scope, reqScope) {
					errs = append(errs, core.FieldError{Field: f, Code: "scope_too_wide", Message: fmt.Sprintf("granted scope of %q is wider than requested", hp.ID)})
					continue
				}
			}
			out = append(out, decision{permission: hp.ID, scope: scope, status: GrantGranted, carried: carried(hp.ID, scope)})
			continue
		}
		if denied[hp.ID] || revoked[hp.ID] {
			switch {
			case hp.Optional && revoked[hp.ID]:
				out = append(out, decision{permission: hp.ID, scope: reqScope, status: GrantRevoked})
			case hp.Optional:
				out = append(out, decision{permission: hp.ID, scope: reqScope, status: GrantDenied})
			case denied[hp.ID]:
				errs = append(errs, core.FieldError{Field: f, Code: "required", Message: fmt.Sprintf("permission %q is required and cannot be denied", hp.ID)})
			default:
				errs = append(errs, core.FieldError{Field: f, Code: "consent_required", Message: fmt.Sprintf("permission %q was revoked and must be granted explicitly", hp.ID)})
			}
			continue
		}
		if risk == manifest.RiskLow {
			out = append(out, decision{permission: hp.ID, scope: reqScope, status: GrantGranted})
			continue
		}
		if carried(hp.ID, reqScope) {
			out = append(out, decision{permission: hp.ID, scope: reqScope, status: GrantGranted, carried: true})
			continue
		}
		if hp.Optional {
			out = append(out, decision{permission: hp.ID, scope: reqScope, status: GrantDenied})
			continue
		}
		errs = append(errs, core.FieldError{Field: f, Code: "consent_required", Message: fmt.Sprintf("permission %q must be granted explicitly", hp.ID)})
	}
	return out, errs
}

// overrideGrants normalizes the consent's overrides to role -> canonical
// scope suffixes per local key ("" every row, which makes the rest
// redundant; a role given false or [] is left out). Field errors name
// new_permission_roles.<local> for an undeclared key (unknown) and
// new_permission_roles.<local>.<role> for a scope value the permission does
// not take (invalid, as the catalog's own check).
func overrideGrants(m *manifest.Manifest, in map[string]map[string]manifest.RoleDefault) (map[string]map[string][]string, []core.FieldError) {
	out := make(map[string]map[string][]string, len(in))
	var errs []core.FieldError
	for _, local := range slices.Sorted(maps.Keys(in)) {
		up, ok := m.FindUserPermission(local)
		if !ok {
			errs = append(errs, core.FieldError{Field: "new_permission_roles." + local, Code: "unknown", Message: fmt.Sprintf("permission %q is not declared by the plugin", local)})
			continue
		}
		key := manifest.PermissionKey(m.ID(), local)
		roles := map[string][]string{}
		for _, role := range slices.Sorted(maps.Keys(in[local])) {
			suffixes, bad := in[local][role].Suffixes(func(v string) (string, bool) { return scopeSuffix(key, up.Scope, v) })
			for _, v := range bad {
				errs = append(errs, core.FieldError{Field: "new_permission_roles." + local + "." + role, Code: "invalid",
					Message: fmt.Sprintf("permission %q does not take the scope %q", local, v)})
			}
			if suffixes != nil {
				roles[role] = suffixes
			}
		}
		out[local] = roles
	}
	return out, errs
}

// scopeSuffix is the canonical suffix of v, one scope value a consent names
// for the permission key of scope s: the manifest spelling ("*", "own", a
// values key: GrantSuffix) or a canonical suffix of the resource s ranges
// over ("value/input", "flow/3"), so the console may send either.
func scopeSuffix(key string, s *manifest.PermissionScope, v string) (string, bool) {
	if v == "*" {
		return "", true
	}
	if s == nil {
		return "", false
	}
	if sfx, ok := s.GrantSuffix(v); ok {
		return sfx, true
	}
	sfx, ok := core.PluginScopeSuffix(key, v)
	return sfx, ok && s.Takes(sfx)
}

// consentable is the locked state Consent decides on.
type consentable struct {
	pluginStatus  string
	activeVersion *string
	publisherID   *int64
	manifestHash  string
	trust         string
	manifest      *manifest.Manifest
	upgrade       bool
	current       map[string]Grant
	revoked       map[string]bool
	staging       bool
}

// staged reports that the decisions wait for the version's activation:
// another version runs (or an enable is preparing it), and it keeps running
// with its own grants.
func (c *consentable) staged() bool { return c.staging }

// grantDecisions are the decisions as grant rows.
func grantDecisions(ds []decision) []grants.Decision {
	out := make([]grants.Decision, len(ds))
	for i, d := range ds {
		scope, _ := json.Marshal(d.scope)
		out[i] = grants.Decision{Permission: d.permission, Scope: scope, Status: d.status}
	}
	return out
}

// decideAndApply records the operator's decisions and moves the plugin into
// its post-consent state.
func (s *Service) decideAndApply(ctx context.Context, tx *gorm.DB, key, version string, req ConsentRequest, actorID int64, c *consentable, mode consentMode) (*ConsentResult, error) {
	m := c.manifest
	decisions, ferrs := decide(m, req, c.current, c.revoked, c.upgrade)
	overrides, oerrs := overrideGrants(m, req.NewPermissionRoles)
	if ferrs = append(ferrs, oerrs...); len(ferrs) > 0 {
		return nil, core.InvalidFields(ferrs...).WithMessage("consent does not match the requested permissions")
	}
	if err := s.checkGrantRights(ctx, actorID, decisions, c.trust, c.publisherID != nil); err != nil {
		return nil, err
	}
	// What an earlier consent of a version never activated staged is
	// superseded: its keys are new to this one, which plans their grants
	// (tx holds the plugin row, so no other consent changes the catalog
	// meanwhile).
	cat, err := s.catalog()
	if err != nil {
		return nil, err
	}
	if err := cat.DiscardStaged(ctx, tx, m.ID()); err != nil {
		return nil, err
	}
	existing, err := loadCatalogPerms(ctx, cat, tx, m.ID())
	if err != nil {
		return nil, err
	}
	if mode != modeSystem {
		if err := s.checkRoleGrantRights(ctx, tx, actorID, m, req, overrides, existing); err != nil {
			return nil, err
		}
	}
	st, err := s.settleRoleGrants(ctx, tx, actorID, c, existing, roleGrants(m, overrides, existing), overrides, mode)
	if err != nil {
		return nil, err
	}
	if err := grants.Record(ctx, tx, key, version, c.manifestHash, nullID(actorID), c.staged(), grantDecisions(decisions)); err != nil {
		return nil, err
	}
	if err := s.syncAccess(ctx, tx, key, dbschema.AccessPermission); err != nil {
		return nil, err
	}
	if err := versionRow(store.G(ctx, tx), key, version).Update("consent_status", ConsentApproved).Error; err != nil {
		return nil, err
	}

	status := c.pluginStatus
	switch {
	case !c.upgrade:
		if err := s.d.Defaults.ApplyDefaults(ctx, tx, m, st.roles); err != nil {
			return nil, err
		}
		name, _ := json.Marshal(m.Name)
		if err := markInstalled(store.G(ctx, tx), key, name, actorID).Error; err != nil {
			return nil, err
		}
		status = StatusInstalled
	case c.activeVersion == nil:
		// Nothing is running: the newest approved version becomes the
		// one to enable, so its defaults apply now.
		if err := s.d.Defaults.ApplyDefaults(ctx, tx, m, st.roles); err != nil {
			return nil, err
		}
	default:
		// Register the new version's permissions next to the running
		// ones so role grants chosen now exist before activation, staged
		// (stageDefs): they mean nothing until then. The runtime's
		// ApplyDefaults at activation switches to the new definitions,
		// pruning scopes and marking stale keys removed.
		if s.d.Permissions != nil {
			old, err := LoadManifest(ctx, tx, key, *c.activeVersion)
			if err != nil {
				return nil, err
			}
			defs := stageDefs(PermissionDefs(m, st.roles), PermissionDefs(old, nil))
			if err := s.d.Permissions.SyncPlugin(ctx, tx, key, defs); err != nil {
				return nil, err
			}
		}
	}

	granted, err := LoadVersionGrants(ctx, tx, key, version)
	if err != nil {
		return nil, err
	}
	res := &ConsentResult{PluginKey: key, Version: version, Status: status, Upgrade: c.upgrade, Grants: SortedGrants(granted), Withheld: st.pending}
	summary := make([]map[string]any, 0, len(decisions))
	for _, d := range decisions {
		summary = append(summary, map[string]any{"permission": d.permission, "status": d.status, "scope": d.scope, "carried": d.carried})
	}
	// role_grants is the plan, made and withheld, whoever consented: what
	// activation reads back (consentDecisions).
	detail := map[string]any{"version": version, "upgrade": c.upgrade, "decisions": summary, "new_permission_roles": req.NewPermissionRoles,
		"role_grants": st.plan}
	if mode != modeOperator {
		// A consent nobody gave: what it made and withheld is audited apart,
		// where the review queue reads them too.
		if err := auditAutoGrants(ctx, tx, actorID, key, version, st); err != nil {
			return nil, err
		}
	} else {
		detail["pending"], detail["inherit_skipped"] = st.pending, st.skipped
	}
	return res, audit.Audit(ctx, tx, actorID, AuditConsent, "plugin", key, detail)
}

// Consent approves a version. First installs apply defaults and move the
// plugin to installed; upgrades only approve the version (the runtime
// applies defaults when it activates).
func (s *Service) Consent(ctx context.Context, key, version string, req ConsentRequest, actorID int64) (*ConsentResult, error) {
	return s.consent(ctx, key, version, req, actorID, consentModeOf(ctx, false))
}

// consentMode is who a consent comes from, which decides how its role
// grants are settled (settleRoleGrants, markPlan).
type consentMode int

const (
	modeOperator consentMode = iota // someone on the consent page
	modeAuto                        // an upgrade carried over (carryOverConsent), nobody deciding
	modeSystem                      // a built-in plugin, approved by the system (isSystem)
)

// consentModeOf is the mode of a consent in ctx; auto: carried over.
func consentModeOf(ctx context.Context, auto bool) consentMode {
	switch {
	case isSystem(ctx):
		return modeSystem
	case auto:
		return modeAuto
	}
	return modeOperator
}

// settlement is how a consent's role grants are settled: the grants to
// sync with, the plan, the grants withheld for review and the inheritFrom
// sources left out.
type settlement struct {
	roles   map[string]map[string][]string
	plan    []RoleGrant
	pending []RoleGrant
	skipped []InheritSkip
}

// settleRoleGrants plans the role grants of the consent (planRoleGrants),
// marks what the actor may name and what waits for review (markPlan) and
// checks the rest; an auto consent with a grant that needs consent fails
// with errRoleGrantsNeedConsent instead. Overrides were checked already
// (checkRoleGrantRights, 403). On an overridden key the override decides
// every role the actor may name (C2): a default or inherited grant to one
// it left out is dropped, to an unnameable role withheld.
func (s *Service) settleRoleGrants(ctx context.Context, tx *gorm.DB, actorID int64, c *consentable,
	existing catalogPerms, roles, overrides map[string]map[string][]string, mode consentMode,
) (*settlement, error) {
	m := c.manifest
	plan, skipped, err := s.planRoleGrants(ctx, tx, m, existing, roles, overrides, c.upgrade)
	if err != nil {
		return nil, err
	}
	if mode == modeAuto && needConsent(plan) {
		return nil, errRoleGrantsNeedConsent
	}
	gs, canDefine, err := s.grantees(ctx, tx, actorID, rolesOf(plan), mode)
	if err != nil {
		return nil, err
	}
	markPlan(plan, gs, canDefine, mode)
	plan = slices.DeleteFunc(plan, func(g RoleGrant) bool {
		local, _ := manifest.LocalPermission(m.ID(), g.Permission)
		return g.Via != viaOverride && g.Nameable && overrides[local] != nil
	})
	if mode != modeSystem {
		made := slices.DeleteFunc(slices.Clone(plan), func(g RoleGrant) bool { return g.Withheld || g.Via == viaOverride })
		if err := s.checkGrantable(ctx, tx, actorID, m.ID(), made); err != nil {
			return nil, err
		}
	}
	st := &settlement{plan: plan, skipped: skipped}
	st.roles, st.pending = withholdGrants(m, existing, plan)
	if st.plan == nil {
		st.plan = []RoleGrant{}
	}
	if st.skipped == nil {
		st.skipped = []InheritSkip{}
	}
	return st, nil
}

// nameable is the rule of whether the consenting actor may name role for a
// new permission of risk (D2, D3), and why not: role:define, critical only
// by a superuser, and the catalog's pluginGrantee and editableRole
// (PluginGrantees).
func nameable(gs core.PluginGrantees, canDefine bool, role, risk string) (bool, string) {
	switch {
	case !canDefine:
		return false, core.GrantDenyRoleDefine
	case risk == manifest.RiskCritical && !gs.Superuser:
		return false, core.GrantDenyCritical
	}
	if reason, denied := gs.Denied[role]; denied {
		return false, reason
	}
	return true, ""
}

// markPlan marks each grant of plan nameable by the consenting actor
// (gs, canDefine: grantees) and whether it waits for review instead: for
// the system, the grants that need consent (rolegrants.go); for anyone
// else, the default and inherited grants to a role the actor may not name
// (D2), an override having been checked to be nameable. A consent and its
// preview (resolveRoleGrants) both mark by it.
func markPlan(plan []RoleGrant, gs core.PluginGrantees, canDefine bool, mode consentMode) {
	for i := range plan {
		g := &plan[i]
		g.Nameable, _ = nameable(gs, canDefine, g.Role, g.Risk)
		if mode == modeSystem {
			g.Withheld = g.NeedsConsent
		} else {
			g.Withheld = g.Via != viaOverride && !g.Nameable
		}
	}
}

// grantees reads what nameable needs of actorID for roles: its
// PluginGrantees and whether it may define roles. The system may name
// every role.
func (s *Service) grantees(ctx context.Context, tx *gorm.DB, actorID int64, roles []string, mode consentMode) (core.PluginGrantees, bool, error) {
	if mode == modeSystem {
		return core.PluginGrantees{Superuser: true}, true, nil
	}
	canDefine, err := s.canDefineRoles(ctx, actorID)
	if err != nil || !canDefine {
		return core.PluginGrantees{}, false, err
	}
	cat, err := s.catalog()
	if err != nil {
		return core.PluginGrantees{}, false, err
	}
	gs, err := cat.PluginGrantees(ctx, tx, actorID, roles)
	return gs, true, err
}

// canDefineRoles reports whether actorID has role:define, which naming
// roles for plugin permissions takes besides the catalog's rules.
func (s *Service) canDefineRoles(ctx context.Context, actorID int64) (bool, error) {
	return s.d.Authz.Can(ctx, actorID, string(perm.RoleDefine))
}

// catalog is the permission catalog, an internal error when none is wired.
func (s *Service) catalog() (core.PermissionCatalog, error) {
	if s.d.Permissions == nil {
		return nil, core.ErrInternal.WithMessage("the permission catalog is not available")
	}
	return s.d.Permissions, nil
}

func (s *Service) consent(ctx context.Context, key, version string, req ConsentRequest, actorID int64, mode consentMode) (*ConsentResult, error) {
	ctx, release, guardErr := core.BeginPluginMutation(ctx, s.d.Mutations)
	if guardErr != nil {
		return nil, guardErr
	}
	defer release()
	if err := s.checkPackage(ctx, key, version); err != nil {
		return nil, err
	}

	var res *ConsentResult
	err := s.d.DB.Tx(ctx, func(tx *gorm.DB) error {
		c, err := s.loadConsentable(ctx, tx, key, version)
		if err != nil {
			return err
		}
		res, err = s.decideAndApply(ctx, tx, key, version, req, actorID, c, mode)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.Notify(ctx, key)
	return res, nil
}

// dbSchemaUnconfined reports that granting db.schema takes
// plugin:grant:db_schema_unconfined: plugin databases are managed and the
// plugin is not from an official publisher. Under CONTRACTS §130 the plugin
// reaches no table outside its own prefix (that is db.core_read), but
// db.schema still gives non-official code a database identity of its own,
// owning its tables in the core's schema, and its own connections to the
// core's database; the owner keeps that superadmin-only (2026-11-20). The
// consent check and the review (Review.NeedsDBSchemaUnconfined) both ask it.
func (s *Service) dbSchemaUnconfined(official bool) bool {
	return s.d.Schemas != nil && !official
}

// checkGrantRights verifies the operator may approve every newly granted
// high/critical permission (carried-over grants were approved before).
// For db.schema: non-official plugins are rejected unless the operator
// explicitly has plugin:grant:db_schema_unconfined.
func (s *Service) checkGrantRights(ctx context.Context, actorID int64, ds []decision, trust string, hasPub bool) error {
	if isSystem(ctx) {
		return nil // built-in plugins ship with the image
	}
	need := map[string][]string{}
	var needsDBSchema bool
	for _, d := range ds {
		if d.status != GrantGranted || d.carried {
			continue
		}
		if d.permission == "db.schema" {
			needsDBSchema = true
		}
		if p := RequiredGrantPermission(riskOf(d.permission)); p != "" {
			need[p] = append(need[p], d.permission)
		}
	}

	// PL-P0-5: db.schema requires official trust OR the explicit override
	if needsDBSchema {
		if s.dbSchemaUnconfined(hasPub && trust == pkg.TrustOfficial) {
			// Require explicit override permission
			ok, err := s.d.Authz.Can(ctx, actorID, "plugin:grant:db_schema_unconfined")
			if err != nil {
				return err
			}
			if !ok {
				return core.ErrPermissionDenied.WithMessage("granting db.schema to non-official plugins requires plugin:grant:db_schema_unconfined")
			}
		}
	}

	perms := make([]string, 0, len(need))
	for p := range need {
		perms = append(perms, p)
	}
	sort.Strings(perms)
	for _, p := range perms {
		ok, err := s.d.Authz.Can(ctx, actorID, p)
		if err != nil {
			return err
		}
		if !ok {
			return core.ErrPermissionDenied.WithMessage(fmt.Sprintf("granting %v requires %s", need[p], p)).
				WithDetails(map[string]any{"permission": p, "host_permissions": need[p]})
		}
	}
	return nil
}

// checkRoleGrantRights verifies the operator may hand the permissions this
// version adds to the roles the request names (SEC-H2): role:define, and
// every such role must be one the operator may change (CanGrantToRoles:
// within the operator's own rights, on its assignable list and not held by
// it unless it is the super admin, never a super admin role; built-in roles
// alike; a critical permission by a superuser only, D3). The new
// permissions themselves need not be held. Defaults and inheritance are
// settled apart (settleRoleGrants: withheld when unnameable). Only
// permissions the catalog does not have yet are handed out (authz
// SyncPlugin), so an upgrade adding none needs nothing even with roles
// named - carrying a running plugin forward must not require role:define.
func (s *Service) checkRoleGrantRights(ctx context.Context, tx *gorm.DB, actorID int64, m *manifest.Manifest, req ConsentRequest,
	overrides map[string]map[string][]string, existing catalogPerms,
) error {
	if err := checkNamedRolesExist(ctx, tx, req.NewPermissionRoles); err != nil {
		return err
	}
	var named []RoleGrant
	for _, up := range m.UserPermissions {
		key := manifest.PermissionKey(m.ID(), up.Key)
		if existing.has(key) {
			continue
		}
		for role := range overrides[up.Key] {
			named = append(named, RoleGrant{Permission: key, Role: role, Risk: up.RiskLevel(), Via: viaOverride})
		}
	}
	return s.checkGrantable(ctx, tx, actorID, m.ID(), named)
}

// checkGrantable verifies that the operator may make the grants of gs,
// permissions of plugin pluginKey (none needs nothing): role:define, then
// one locking CanGrantToRoles of all their roles at their highest risk,
// whose critical refusal also names the critical local keys
// (details.permissions).
func (s *Service) checkGrantable(ctx context.Context, tx *gorm.DB, actorID int64, pluginKey string, gs []RoleGrant) error {
	if len(gs) == 0 {
		return nil
	}
	ok, err := s.canDefineRoles(ctx, actorID)
	if err != nil {
		return err
	}
	if !ok {
		return core.ErrPermissionDenied.WithMessage("granting permissions to roles requires role:define").
			WithDetails(map[string]any{"permission": string(perm.RoleDefine)})
	}
	cat, err := s.catalog()
	if err != nil {
		return err
	}
	risk := manifest.RiskLow
	var criticalKeys []string
	for _, g := range gs {
		if g.Risk == manifest.RiskCritical {
			risk = manifest.RiskCritical
			local, _ := manifest.LocalPermission(pluginKey, g.Permission)
			criticalKeys = append(criticalKeys, local)
		}
	}
	err = cat.CanGrantToRoles(ctx, tx, actorID, rolesOf(gs), risk)
	if ce := core.AsError(err); ce != nil && ce.Details["reason"] == core.GrantDenyCritical {
		slices.Sort(criticalKeys)
		return ce.WithDetails(map[string]any{"permissions": slices.Compact(criticalKeys)})
	}
	return err
}

// checkNamedRolesExist fails (400) when the request names a role that does
// not exist, under new_permission_roles.<local>, whether or not the
// permission it is named for is created now.
func checkNamedRolesExist(ctx context.Context, q *gorm.DB, named map[string]map[string]manifest.RoleDefault) error {
	var roles []string
	for _, rs := range named {
		roles = append(roles, slices.Collect(maps.Keys(rs))...)
	}
	if len(roles) == 0 {
		return nil
	}
	var found []string
	if err := store.G(ctx, q).Model(&model.Role{}).Where("key = ANY(?)", store.Strings(roles)).Pluck("key", &found).Error; err != nil {
		return err
	}
	var errs []core.FieldError
	for _, local := range slices.Sorted(maps.Keys(named)) {
		for _, r := range slices.Sorted(maps.Keys(named[local])) {
			if !slices.Contains(found, r) {
				errs = append(errs, core.FieldError{Field: "new_permission_roles." + local, Code: "unknown", Message: fmt.Sprintf("role %q does not exist", r)})
			}
		}
	}
	if len(errs) > 0 {
		return core.InvalidFields(errs...).WithMessage("the consent names unknown roles")
	}
	return nil
}

// catalogPerms is what the permission catalog holds of a plugin's
// permissions, by qualified key, removed and staged ones included
// (loadCatalogPerms).
type catalogPerms map[string]core.PluginPermission

// has reports whether the catalog has key, removed or not: a key declared
// again is not new (D6). A staged key is new still: a consent discards it
// (DiscardStaged) and plans it afresh.
func (c catalogPerms) has(key string) bool {
	p, ok := c[key]
	return ok && p.Status != permStatusStaged
}

// removed reports whether key is removed now: a version declaring it
// revives it.
func (c catalogPerms) removed(key string) bool { return c[key].Status == permStatusRemoved }

// Catalog statuses: removed, a key no version declares now (D6); staged,
// one only a version approved while another runs declares
// (core.PermissionDef.Staged).
const (
	permStatusRemoved = "removed"
	permStatusStaged  = "staged"
)

// loadCatalogPerms reads cat's permissions of plugin pluginKey.
func loadCatalogPerms(ctx context.Context, cat core.PermissionCatalog, q *gorm.DB, pluginKey string) (catalogPerms, error) {
	ps, err := cat.PluginPermissions(ctx, q, pluginKey)
	if err != nil {
		return nil, err
	}
	out := make(catalogPerms, len(ps))
	for _, p := range ps {
		out[p.Key] = p
	}
	return out, nil
}

// Reject marks a version rejected. A plugin that was never installed and
// has no other pending version is removed.
func (s *Service) Reject(ctx context.Context, key, version string, actorID int64) error {
	ctx, release, guardErr := core.BeginPluginMutation(ctx, s.d.Mutations)
	if guardErr != nil {
		return guardErr
	}
	defer release()
	return s.d.DB.Tx(ctx, func(tx *gorm.DB) error {
		g := store.G(ctx, tx)
		var status string
		err := lockedPlugin(g, key).Select("status").Take(&status).Error
		if store.IsNoRows(err) {
			return core.ErrNotFound.WithMessage("plugin not found")
		}
		if err != nil {
			return err
		}
		res := versionRow(g, key, version).Where("consent_status = ?", ConsentAwaiting).Update("consent_status", ConsentRejected)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return core.ErrConflict.WithMessage("version is not awaiting consent")
		}
		removed, err := s.removeAbandoned(ctx, tx, key, status)
		if err != nil {
			return err
		}
		return audit.Audit(ctx, tx, actorID, "plugin.reject", "plugin", key, map[string]any{"version": version, "plugin_removed": removed})
	})
}

// RevokeGrant marks one host permission grant revoked, staged ones included.
func (s *Service) RevokeGrant(ctx context.Context, key, permission string, actorID int64) error {
	err := s.d.DB.Tx(ctx, func(tx *gorm.DB) error {
		// The plugin row first, as consent and the rollout commit lock it
		// before the grant rows they write (no lock-order deadlock).
		if err := bumpPlugin(store.G(ctx, tx), key).Error; err != nil {
			return err
		}
		revoked, err := grants.Revoke(ctx, tx, key, permission, nullID(actorID))
		if err != nil {
			return err
		}
		if !revoked {
			return core.ErrNotFound.WithMessage("grant not found")
		}
		if err := s.syncAccess(ctx, tx, key, permission); err != nil {
			return err
		}
		return audit.Audit(ctx, tx, actorID, "plugin.grant.revoke", "plugin", key, map[string]any{"permission": permission})
	})
	if err == nil {
		s.Notify(ctx, key)
	}
	return err
}

// syncAccess carries a change of the db.core_read grant into the
// database in tx (core.PluginSchemaManager.SyncAccessTx): the plugin's
// identity loses (or gets) its grants on tables outside the plugin when tx
// commits.
func (s *Service) syncAccess(ctx context.Context, tx *gorm.DB, key, permission string) error {
	if permission != dbschema.AccessPermission || s.d.Schemas == nil {
		return nil
	}
	return s.d.Schemas.SyncAccessTx(ctx, tx, key)
}

// consentablePlugin is what loadConsentable reads of the plugin row. Live
// is the version instances run or prepare: the active one, else the one an
// enable rollout is preparing.
type consentablePlugin struct {
	Status        string  `gorm:"column:status"`
	ActiveVersion *string `gorm:"column:active_version"`
	Live          *string `gorm:"column:live"`
	PublisherID   *int64  `gorm:"column:publisher_id"`
}

const consentablePluginColumns = "status, active_version, COALESCE(active_version, desired_version) AS live, publisher_id"

// markInstalled moves a first install's plugin row to installed, recording
// its name and who installed it (unless already recorded).
func markInstalled(db *gorm.DB, key string, name []byte, actorID int64) *gorm.DB {
	return pluginRow(db, key).Updates(rows.Change(map[string]any{
		"status":       StatusInstalled,
		"name":         name,
		"installed_by": gorm.Expr("COALESCE(installed_by, ?)", nullID(actorID)),
		"installed_at": gorm.Expr("now()"),
	}))
}

// bumpPlugin moves the plugin row's version, which also locks it: the
// first write of a grant change.
func bumpPlugin(db *gorm.DB, key string) *gorm.DB {
	return pluginRow(db, key).Updates(rows.Change(map[string]any{}))
}
