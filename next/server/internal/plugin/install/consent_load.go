package install

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/grants"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// loadConsentable locks the plugin and version rows and re-checks every
// barrier an approval must pass: uninstall in progress, consent status,
// signature revocation, publisher revocation, the version's trust level and
// exclusive resources.
func (s *Service) loadConsentable(ctx context.Context, tx *gorm.DB, key, version string) (*consentable, error) {
	c, live, err := lockConsentablePlugin(ctx, tx, key)
	if err != nil {
		return nil, err
	}
	if err := checkUninstall(ctx, tx, key); err != nil {
		return nil, err
	}
	if c.manifestHash, err = lockConsentableVersion(ctx, tx, key, version); err != nil {
		return nil, err
	}
	if c.trust, err = s.consentTrust(store.G(ctx, tx), key, version, c.publisherID); err != nil {
		return nil, err
	}
	if err := s.checkConsentManifest(ctx, tx, key, version, c); err != nil {
		return nil, err
	}
	c.upgrade = c.pluginStatus != StatusAwaitingConsent
	c.staging = live != nil && *live != version
	return c, loadConsentGrants(ctx, tx, key, c)
}

// lockConsentablePlugin locks the plugin row of key: what a consent reads
// of it, and the version it runs or is being enabled with (live).
func lockConsentablePlugin(ctx context.Context, tx *gorm.DB, key string) (*consentable, *string, error) {
	if err := store.RequireTx(tx); err != nil {
		return nil, nil, err
	}
	var p consentablePlugin
	err := lockedPlugin(store.G(ctx, tx), key).Select(consentablePluginColumns).Take(&p).Error
	if store.IsNoRows(err) {
		return nil, nil, core.ErrNotFound.WithMessage("plugin not found")
	}
	if err != nil {
		return nil, nil, err
	}
	return &consentable{pluginStatus: p.Status, activeVersion: p.ActiveVersion, publisherID: p.PublisherID}, p.Live, nil
}

// lockConsentableVersion locks the version row, which must await consent
// and not be revoked, and returns its manifest hash.
func lockConsentableVersion(ctx context.Context, tx *gorm.DB, key, version string) (string, error) {
	if err := store.RequireTx(tx); err != nil {
		return "", err
	}
	var v model.PluginVersion
	err := versionRow(store.G(ctx, tx), key, version).Select("consent_status, signature_status, manifest_hash").Clauses(store.ForUpdate()).Take(&v).Error
	if store.IsNoRows(err) {
		return "", core.ErrNotFound.WithMessage("plugin version not found")
	}
	if err != nil {
		return "", err
	}
	if consent := core.ValueOr(v.ConsentStatus, ""); consent != ConsentAwaiting {
		return "", core.ErrConflict.WithMessage(fmt.Sprintf("version %s is %s", version, consent))
	}
	if v.SignatureStatus == pkg.SigRevoked {
		return "", core.ErrPermissionDenied.WithMessage("the package signature has been revoked")
	}
	return v.ManifestHash, nil
}

// consentTrust is the trust level of the version (pkg.VersionTrust): its
// publisher, if it has one, must be active; one without may only be
// approved where unsigned plugins are allowed.
func (s *Service) consentTrust(g *gorm.DB, key, version string, publisherID *int64) (string, error) {
	if publisherID == nil {
		if !s.d.Trust.AllowUnsigned() {
			return "", core.ErrPermissionDenied.WithMessage("unsigned plugins are not allowed")
		}
		return pkg.TrustUnsigned, nil
	}
	var pub model.Publisher
	if err := g.Model(&model.Publisher{}).Select("status").Where("id = ?", *publisherID).Take(&pub).Error; err != nil {
		return "", err
	}
	if core.ValueOr(pub.Status, "") != "active" {
		return "", core.ErrPermissionDenied.WithMessage("the publisher has been revoked")
	}
	return pkg.ReadVersionTrust(g, key, version)
}

// checkConsentManifest loads the version's manifest into c and checks what
// its trust level allows and the exclusive resources it declares: another
// plugin may have been approved for them since the version was uploaded.
func (s *Service) checkConsentManifest(ctx context.Context, tx *gorm.DB, key, version string, c *consentable) error {
	m, err := LoadManifest(ctx, tx, key, version)
	if err != nil {
		return err
	}
	c.manifest = m
	if err := pkg.CheckTrust(m, c.trust); err != nil {
		return err
	}
	return s.checkResources(ctx, tx, m)
}

// loadConsentGrants reads into c the grants an upgrade keeps and the
// revoked host permissions.
func loadConsentGrants(ctx context.Context, tx *gorm.DB, key string, c *consentable) (err error) {
	c.current = map[string]Grant{}
	if c.upgrade {
		if c.current, err = LoadGrants(ctx, tx, key); err != nil {
			return err
		}
	}
	c.revoked, err = grants.Revoked(ctx, tx, key)
	return err
}
