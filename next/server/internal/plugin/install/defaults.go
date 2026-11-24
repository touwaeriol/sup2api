package install

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// DefaultsApplier implements core.PluginDefaultsApplier. It has no
// dependency on the rollout controller so the runtime (C2) can be built
// with it before the install service exists.
type DefaultsApplier struct {
	perms core.PermissionCatalog
}

var _ core.PluginDefaultsApplier = (*DefaultsApplier)(nil)

// NewDefaultsApplier wires the permission catalog. A nil catalog is skipped.
func NewDefaultsApplier(perms core.PermissionCatalog) *DefaultsApplier {
	return &DefaultsApplier{perms: perms}
}

// ApplyDefaults syncs the user permissions of m, granting newly created ones
// what grants holds for their local key (roleGrants, settled by the
// consent), and drops host permission grants the manifest no longer
// requests, except revocations: a version asking for the permission again
// does not get it by default (grants.Revoked). An activation (grants nil)
// also lists for review what holders the old keys it retires gained since
// the consent would inherit (carryRenamedHolders). It runs inside the
// caller's transaction.
// Plugins bring no sticky rules (CONTRACTS §5.6).
func (a *DefaultsApplier) ApplyDefaults(ctx context.Context, tx *gorm.DB, m *manifest.Manifest, grants map[string]map[string][]string) error {
	if a.perms != nil {
		before, err := loadCatalogPerms(ctx, a.perms, tx, m.ID())
		if err != nil {
			return err
		}
		if err := a.perms.SyncPlugin(ctx, tx, m.ID(), PermissionDefs(m, grants)); err != nil {
			return fmt.Errorf("sync plugin permissions: %w", err)
		}
		if grants == nil {
			if err := carryRenamedHolders(ctx, a.perms, tx, m, before); err != nil {
				return fmt.Errorf("carry renamed holders: %w", err)
			}
		}
	}
	ids := make([]string, 0, len(m.HostPermissions))
	for _, hp := range m.HostPermissions {
		ids = append(ids, hp.ID)
	}
	if err := pruneGrants(store.G(ctx, tx), m.ID(), ids).Error; err != nil {
		return fmt.Errorf("prune grants: %w", err)
	}
	return nil
}

// pruneGrants deletes the grants of plugin key to host permissions other
// than requested, revocations aside.
func pruneGrants(db *gorm.DB, key string, requested []string) *gorm.DB {
	return db.Where("plugin_key = ? AND NOT (permission = ANY(?)) AND status <> 'revoked'", key, store.Strings(requested)).
		Delete(&model.PluginPermissionGrant{})
}

// PermissionDefs converts manifest userPermissions into catalog entries,
// sorted in manifest order (10, 20, ...), their groups likewise. A
// permission created now is granted what grants holds for its local key,
// role -> scope suffixes (nothing when absent): the consent resolved its
// defaults, overrides and inheritance into it. A nil grants (activation: no
// consent is behind a permission created then, one deleted since the
// consent) grants nothing, so a role never gets a permission nobody
// reviewed (P4C).
func PermissionDefs(m *manifest.Manifest, grants map[string]map[string][]string) []core.PermissionDef {
	defs := make([]core.PermissionDef, 0, len(m.UserPermissions))
	for i, up := range m.UserPermissions {
		d := core.PermissionDef{
			Key:         manifest.PermissionKey(m.ID(), up.Key),
			Module:      manifest.PermissionModule(m.ID()),
			Label:       core.LocalizedText(up.Label),
			Description: core.LocalizedText(up.Description),
			Risk:        up.RiskLevel(),
			Sort:        (i + 1) * 10,
			Group:       up.Group,
			Defaults:    up.DefaultGrants(),
			GrantRoles:  grants[up.Key],
		}
		if g := slices.IndexFunc(m.PermissionGroups, func(pg manifest.PermissionGroup) bool { return pg.Key == up.Group }); g >= 0 {
			d.GroupLabel, d.GroupSort = core.LocalizedText(m.PermissionGroups[g].Label), (g+1)*10
		}
		for _, r := range up.Requires {
			d.Requires = append(d.Requires, manifest.PermissionKey(m.ID(), r))
		}
		d.Scope, d.ScopeType = core.StoredPluginScope(up.Scope), core.PluginScopeType(up.Scope)
		defs = append(defs, d)
	}
	return defs
}

// stageDefs is what a consent made while the running version's
// definitions (running) serve syncs the catalog with, the new version's
// (next) in its order, then the keys only the running one declares:
//   - a key only next declares is Staged: created disabled, a removed one
//     left removed, until activation (logic #4);
//   - a key both declare keeps the running scope, scope_type and defaults
//     (the defaults name scopes of that scope), so no scope grant the
//     running version serves is pruned before activation, and takes the
//     higher of the two risks, never lowering it meanwhile (an old manifest
//     may declare no risk at all); the rest (label, group, sort, requires)
//     is next's.
//
// Activation's ApplyDefaults syncs next's definitions alone.
func stageDefs(next, running []core.PermissionDef) []core.PermissionDef {
	idx := map[string]int{}
	out := make([]core.PermissionDef, 0, len(next)+len(running))
	for _, d := range next {
		d.Staged = true
		idx[d.Key] = len(out)
		out = append(out, d)
	}
	for _, r := range running {
		i, ok := idx[r.Key]
		if !ok {
			out = append(out, r)
			continue
		}
		d := &out[i]
		d.Staged = false
		d.Scope, d.ScopeType, d.Defaults = r.Scope, r.ScopeType, r.Defaults
		if riskRank(r.Risk) > riskRank(d.Risk) {
			d.Risk = r.Risk
		}
	}
	return out
}

// riskRank orders user permission risks, low (or none) first.
func riskRank(risk string) int {
	return slices.Index(manifest.UserPermissionRisks, cmp.Or(risk, manifest.RiskLow))
}

// roleGrants resolves, per local userPermission key, the role -> scope
// suffixes a permission created now is planned with: nothing for a key the
// catalog has (existing, loadCatalogPerms: removed rows included, so a key
// declared again gets no defaults, D6); else the manifest defaults
// (DefaultGrants), none for a rename or split, which inherits from an old
// key the catalog has and so only keeps that key's holders
// (planRoleGrants); the roles the consent's override names replace theirs
// (overrideGrants). Defaults to roles the override leaves out stay: the
// consent's settlement drops or withholds them (settleRoleGrants).
func roleGrants(m *manifest.Manifest, overrides map[string]map[string][]string, existing catalogPerms) map[string]map[string][]string {
	out := make(map[string]map[string][]string, len(m.UserPermissions))
	for _, up := range m.UserPermissions {
		roles := map[string][]string{}
		out[up.Key] = roles
		if existing.has(manifest.PermissionKey(m.ID(), up.Key)) {
			continue
		}
		if !renamed(m, up, existing) {
			maps.Copy(roles, up.DefaultGrants())
		}
		maps.Copy(roles, overrides[up.Key])
	}
	return out
}

// renamed reports that up inherits from a key the catalog has (existing):
// a rename or split, which keeps the old key's holders instead of getting
// the manifest defaults.
func renamed(m *manifest.Manifest, up manifest.UserPermission, existing catalogPerms) bool {
	return slices.ContainsFunc(up.InheritFrom, func(from string) bool { return existing.has(manifest.PermissionKey(m.ID(), from)) })
}
