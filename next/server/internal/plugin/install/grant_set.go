package install

import (
	"context"
	"fmt"
	"slices"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/grants"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// SetGrant sets the active version's grant of one host permission (nil
// scope: what the manifest requests). A scope within the current grant
// narrows it (plugin:manage, the route's, is enough); anything else (a
// revoked or denied permission, a wider scope) is a grant decision, checked
// like a consent's: plugin:install, the consent route's, and the grant
// rights by risk (checkGrantRights), never wider than the active manifest
// requests. Staged sets get the same narrowing (grants.Narrow), and a staged
// revocation of the permission is lifted when its version requests at least
// the scope (grants.Reinstate). Audited plugin.grant.narrow /
// plugin.grant.regrant.
func (s *Service) SetGrant(ctx context.Context, key, permission string, scope map[string]any, actorID int64) (*Grant, error) {
	var out *Grant
	err := s.d.DB.Tx(ctx, func(tx *gorm.DB) error {
		t, err := s.grantTarget(ctx, tx, key, permission)
		if err != nil {
			return err
		}
		out, err = s.setGrant(ctx, tx, t, scope, actorID)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.Notify(ctx, key)
	return out, nil
}

// grantTarget is the permission SetGrant changes: the plugin, its current
// version and that version's trust level, the scope the version requests
// and the grants held.
type grantTarget struct {
	key, permission, version, trust string
	hasPub                          bool
	requested                       map[string]any
	current                         map[string]Grant
}

// grantTarget locks the plugin row first, as RevokeGrant, consent and the
// rollout commit do before the grant rows (no lock-order deadlock), and
// reads what SetGrant decides on.
func (s *Service) grantTarget(ctx context.Context, tx *gorm.DB, key, permission string) (*grantTarget, error) {
	var bumped bumpedPlugin
	res := bumpPluginReturning(store.G(ctx, tx), key, &bumped)
	switch {
	case res.Error != nil:
		return nil, res.Error
	case res.RowsAffected == 0:
		return nil, core.ErrNotFound.WithMessage("plugin not found")
	case bumped.Status == StatusAwaitingConsent:
		return nil, core.ErrConflict.WithMessage("the plugin is awaiting consent")
	}
	t := &grantTarget{key: key, permission: permission, hasPub: bumped.Published}
	var err error
	if t.version, err = CurrentVersion(ctx, tx, key); err != nil {
		return nil, err
	}
	if t.trust, err = pkg.ReadVersionTrust(store.G(ctx, tx), key, t.version); err != nil {
		return nil, err
	}
	if t.requested, err = requestedOf(ctx, tx, key, t.version, permission); err != nil {
		return nil, err
	}
	t.current, err = LoadGrants(ctx, tx, key)
	return t, err
}

// requestedOf is the scope version of key requests of permission, not
// found when it does not request it.
func requestedOf(ctx context.Context, tx *gorm.DB, key, version, permission string) (map[string]any, error) {
	m, err := LoadManifest(ctx, tx, key, version)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(m.HostPermissions, func(hp manifest.HostPermission) bool { return hp.ID == permission })
	if i < 0 {
		return nil, core.ErrNotFound.WithMessage(fmt.Sprintf("permission %q is not requested by version %s", permission, version))
	}
	return requestedScope(m, m.HostPermissions[i]), nil
}

// setGrant grants t's permission with scope (nil: as requested): a
// narrowing of the grant held, or a grant decision (applyGrant).
func (s *Service) setGrant(ctx context.Context, tx *gorm.DB, t *grantTarget, scope map[string]any, actorID int64) (*Grant, error) {
	if scope == nil {
		scope = t.requested
	}
	scope = pkg.NormalizeScope(scope)
	if !pkg.ScopeWithin(scope, t.requested) {
		return nil, core.InvalidFields(core.FieldError{Field: "scope", Code: "scope_too_wide",
			Message: fmt.Sprintf("scope of %q is wider than requested", t.permission)}).WithMessage("the scope is wider than requested")
	}
	d := decision{permission: t.permission, scope: scope, status: GrantGranted}
	action, err := s.applyGrant(ctx, tx, t, d, actorID)
	if err != nil {
		return nil, err
	}
	return s.recordGrant(ctx, tx, t, d, action, actorID)
}

// applyGrant narrows the grant held when the scope is within it
// (plugin.grant.narrow); anything else is a grant decision, checked like a
// consent's (plugin.grant.regrant).
func (s *Service) applyGrant(ctx context.Context, tx *gorm.DB, t *grantTarget, d decision, actorID int64) (string, error) {
	by := nullID(actorID)
	if prev, held := t.current[t.permission]; held && prev.Status == GrantGranted && pkg.ScopeWithin(d.scope, prev.Scope) {
		return "plugin.grant.narrow", grants.Narrow(ctx, tx, t.key, t.permission, d.scope, by)
	}
	if err := s.requireInstall(ctx, actorID); err != nil {
		return "", err
	}
	if err := s.checkGrantRights(ctx, actorID, []decision{d}, t.trust, t.hasPub); err != nil {
		return "", err
	}
	return "plugin.grant.regrant", grants.Reinstate(ctx, tx, t.key, t.permission, d.scope, by)
}

// recordGrant records decision d on t's version, syncs the access it gives
// and writes the audit entry action; it returns the grant now held.
func (s *Service) recordGrant(ctx context.Context, tx *gorm.DB, t *grantTarget, d decision, action string, actorID int64) (*Grant, error) {
	var hash string
	if err := versionRow(store.G(ctx, tx), t.key, t.version).Select("manifest_hash").Take(&hash).Error; err != nil {
		return nil, err
	}
	if err := grants.Record(ctx, tx, t.key, t.version, hash, nullID(actorID), false, grantDecisions([]decision{d})); err != nil {
		return nil, err
	}
	current, err := LoadGrants(ctx, tx, t.key)
	if err != nil {
		return nil, err
	}
	out := current[t.permission]
	if err := s.syncAccess(ctx, tx, t.key, t.permission); err != nil {
		return nil, err
	}
	detail := map[string]any{"permission": t.permission, "version": t.version, "scope": d.scope}
	if prev, held := t.current[t.permission]; held {
		detail["previous"] = map[string]any{"status": prev.Status, "scope": prev.Scope}
	}
	return &out, audit.Audit(ctx, tx, actorID, action, "plugin", t.key, detail)
}

// requireInstall fails unless the actor holds plugin:install: granting a
// host permission again is a consent decision, which plugin:manage alone
// must not reach (a revoked or denied low-risk permission needs no grant
// right).
func (s *Service) requireInstall(ctx context.Context, actorID int64) error {
	ok, err := s.d.Authz.Can(ctx, actorID, string(perm.PluginInstall))
	if err != nil || ok {
		return err
	}
	return core.ErrPermissionDenied.WithMessage("granting a host permission again requires plugin:install").
		WithDetails(map[string]any{"permission": string(perm.PluginInstall)})
}

// bumpedPlugin is what bumpPluginReturning reads back of the plugin.
type bumpedPlugin struct {
	Status    string `gorm:"column:status;->"`
	Published bool   `gorm:"column:published;->"`
}

func (bumpedPlugin) TableName() string { return model.Plugin{}.TableName() }

// bumpPluginReturning is bumpPlugin reading into dst the plugin's status
// and whether it has a publisher (RowsAffected 0: no plugin).
func bumpPluginReturning(db *gorm.DB, key string, dst *bumpedPlugin) *gorm.DB {
	return db.Model(dst).Clauses(clause.Returning{Columns: []clause.Column{{Name: "status"},
		{Name: "publisher_id IS NOT NULL AS published", Raw: true}}}).
		Where("key = ?", key).Updates(map[string]any{"row_version": gorm.Expr("row_version + 1"), "updated_at": gorm.Expr("now()")})
}
