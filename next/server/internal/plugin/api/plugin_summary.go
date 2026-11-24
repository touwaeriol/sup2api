package api

import (
	"context"
	"encoding/json"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/install"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// The rows of GET /plugins: the plugin columns (pluginSummaries) and what
// fillSummary adds of the current version and this node.

// pluginSummaries selects the PluginSummary columns of the plugins (as p),
// in scanSummary's order.
func pluginSummaries(db *gorm.DB) *gorm.DB {
	pending := db.Model(&model.PluginVersion{}).Select("array_agg(version ORDER BY uploaded_at)").
		Where("plugin_key = p.key AND consent_status = 'awaiting_consent'")
	return db.Table("plugins AS p").
		Select(`p.key, p.name, p.status, p.status_reason, p.active_version, p.desired_version,
			COALESCE(pub.trust_level, 'unsigned'), p.egress_policy, p.installed_at, p.updated_at, p.builtin,
			COALESCE((?), '{}')`, pending).
		Joins("LEFT JOIN publishers pub ON pub.id = p.publisher_id")
}

func scanSummary(row store.RowScanner) (*PluginSummary, error) {
	var s PluginSummary
	var name []byte
	if err := row.Scan(&s.Key, &name, &s.Status, &s.StatusReason, &s.ActiveVersion, &s.DesiredVersion,
		&s.Trust, &s.EgressPolicy, &s.InstalledAt, &s.UpdatedAt, &s.Builtin, &s.PendingVersions); err != nil {
		return nil, err
	}
	s.Identity = pkg.IdentityOf(s.Key)
	_ = json.Unmarshal(name, &s.Name)
	return &s, nil
}

// fillSummary adds current version, signature status, trust level, icon
// and the resource conflicts of the current generation.
func (a *API) fillSummary(ctx context.Context, s *PluginSummary) error {
	s.ResourceConflicts = []core.ResourceConflict{}
	if a.d.Registry != nil {
		if g := a.d.Registry.Current(); g != nil {
			if info, ok := g.Plugin(s.Key); ok && len(info.Conflicts) > 0 {
				s.ResourceConflicts = info.Conflicts
			}
		}
	}
	viewer, _ := core.UserID(ctx)
	pending, err := a.d.Install.RoleGrantsToReview(ctx, a.d.DB.Gorm, s.Key, viewer)
	if err != nil {
		return err
	}
	s.RoleGrantsToReview = append([]install.RoleGrant{}, pending...)
	v, err := install.CurrentVersion(ctx, a.d.DB.Gorm, s.Key)
	if err != nil || v == "" {
		return err
	}
	s.CurrentVersion = v
	return a.fillCurrentVersion(ctx, s)
}

// fillCurrentVersion adds what the summary shows of s.CurrentVersion: its
// signature status, trust level (pkg.VersionTrust: a market install's is
// its own), settings, icon and asset base.
func (a *API) fillCurrentVersion(ctx context.Context, s *PluginSummary) error {
	var sum string
	db := store.G(ctx, a.d.DB.Gorm)
	err := currentVersionSummary(db, s.Key, s.CurrentVersion).
		Row().Scan(&s.SignatureStatus, &s.HasSettings, &s.Icon, &sum, &s.Trust)
	if err != nil {
		return err
	}
	s.AssetBase = core.PluginAssetBase(s.Key, core.PluginVersionHash(s.CurrentVersion, sum))
	return nil
}

// currentVersionSummary selects, in fillCurrentVersion's order, what the
// summary shows of version of key.
func currentVersionSummary(db *gorm.DB, key, version string) *gorm.DB {
	return db.Model(&model.PluginVersion{}).
		Select(`signature_status, COALESCE(manifest->'ui'->'settings'->>'mode' = 'native', false),
			COALESCE(manifest->>'icon', ''), package_sha256, COALESCE((?), 'unsigned')`,
			pkg.VersionTrust(db, key, version)).
		Where("plugin_key = ? AND version = ?", key, version)
}
