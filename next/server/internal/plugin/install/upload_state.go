package install

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// uploadState is what an upload of a package is checked and reviewed
// against (readUploadState): its verification, whether the plugin has a
// row and, for an installed plugin, its grants (nil for a first install),
// its revoked host permissions and the version an upgrade replaces.
type uploadState struct {
	verification *pkg.Verification
	existing     bool
	current      map[string]Grant
	revoked      map[string]bool
	upgradeOf    string
}

// readUploadState verifies p and checks it against the plugin it goes to,
// in tx. write: tx goes on to record the upload (recordUpload), so the
// plugin row is locked, an official publisher registered and a retired key
// claimed; otherwise tx is the install preview's read-only transaction
// (ReviewPackage), which only reads and checks them. Upload and the preview
// share it, so the preview fails as the upload would.
func (s *Service) readUploadState(ctx context.Context, tx *gorm.DB, p *pkg.Package, opt UploadOptions, write bool) (*uploadState, error) {
	ver, err := s.verifyTrust(ctx, tx, p, opt, write)
	if err != nil {
		return nil, err
	}
	plugin, err := readPlugin(ctx, tx, p.Manifest.Key, write)
	if err != nil {
		return nil, err
	}
	if err := s.checkTarget(ctx, tx, p, opt, ver, plugin, write); err != nil {
		return nil, err
	}
	st := &uploadState{verification: ver, existing: plugin != nil}
	if plugin == nil || plugin.Status == StatusAwaitingConsent {
		return st, nil
	}
	if err := s.readPrior(ctx, tx, p.Manifest, st); err != nil {
		return nil, err
	}
	return st, nil
}

// verifyTrust resolves the trust of p (verifyPackage) and checks what its
// trust level allows.
func (s *Service) verifyTrust(ctx context.Context, tx *gorm.DB, p *pkg.Package, opt UploadOptions, write bool) (*pkg.Verification, error) {
	ver, err := s.verifyPackage(ctx, tx, p, opt, write)
	if err != nil {
		return nil, err
	}
	if err := pkg.CheckTrust(p.Manifest, ver.Trust); err != nil {
		return nil, err
	}
	return ver, nil
}

// verifyPackage takes the trust of a market install from its index
// (VerifyIndexed) and verifies the signature of any other package. A write
// registers the publisher (Verify), otherwise it is only read
// (VerifyReadOnly).
func (s *Service) verifyPackage(ctx context.Context, tx *gorm.DB, p *pkg.Package, opt UploadOptions, write bool) (*pkg.Verification, error) {
	if opt.Indexed != nil {
		return s.d.Trust.VerifyIndexed(ctx, tx, p, *opt.Indexed, write)
	}
	verify := s.d.Trust.VerifyReadOnly
	if write {
		verify = s.d.Trust.Verify
	}
	return verify(ctx, tx, p)
}

// readPlugin reads the status and publisher of plugin key, locking its row
// in a write; nil when there is none.
func readPlugin(ctx context.Context, tx *gorm.DB, key string, write bool) (*model.Plugin, error) {
	if err := store.RequireTx(tx); err != nil {
		return nil, err
	}
	q := pluginRow(store.G(ctx, tx), key).Select("status, publisher_id")
	if write {
		q = q.Clauses(store.ForUpdate())
	}
	var plugin model.Plugin
	err := q.Take(&plugin).Error
	if store.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &plugin, nil
}

// checkTarget checks that p (verified as ver) may go to plugin, nil for a
// first upload: reserved keys, a key retired by another publisher (claimed
// in a write, only checked otherwise), the publisher owning the plugin and
// an uninstall in progress.
func (s *Service) checkTarget(ctx context.Context, tx *gorm.DB, p *pkg.Package, opt UploadOptions, ver *pkg.Verification, plugin *model.Plugin, write bool) error {
	m := p.Manifest
	if err := checkReserved(m, opt.Source, ver.Trust, plugin == nil && !s.opt.Plugins.DevMode); err != nil {
		return err
	}
	switch {
	case plugin == nil && write:
		return claimRetiredKey(ctx, tx, m.Key, ver.PublisherID, opt.Source)
	case plugin == nil:
		return checkRetiredKey(ctx, tx, m.Key, ver.PublisherID)
	case !sameID(plugin.PublisherID, ver.PublisherID):
		return core.ErrConflict.WithMessage(fmt.Sprintf("plugin %q is owned by another publisher", m.Key))
	}
	return checkUninstall(ctx, tx, m.Key)
}

// readPrior reads into st what an upgrade of the installed plugin to m is
// reviewed and decided against: its grants, its revoked host permissions
// (loadPriorGrants) and the version it replaces.
func (s *Service) readPrior(ctx context.Context, tx *gorm.DB, m *manifest.Manifest, st *uploadState) (err error) {
	if st.current, st.revoked, err = loadPriorGrants(ctx, tx, m.Key); err != nil {
		return err
	}
	st.upgradeOf, err = s.runningVersion(ctx, tx, m.Key, m.Version)
	return err
}

// reviewUpload renders the review of p uploaded against st (Upload,
// ReviewPackage): the consent matrix as actorID would consent
// (resolveRoleGrants) and the migrations against the plugin's records.
// The consent status and upload time are the caller's.
func (s *Service) reviewUpload(ctx context.Context, p *pkg.Package, st *uploadState, actorID int64) (*Review, error) {
	r := buildReview(p.Manifest, p.Files, st.verification, s.opt.HostVersion, st.current)
	r.noteRevoked(st.revoked)
	if err := s.resolveRoleGrants(ctx, r, p.Manifest, actorID); err != nil {
		return nil, err
	}
	s.noteDBSchema(r, p.Manifest)
	if err := s.noteDatabase(ctx, r); err != nil {
		return nil, err
	}
	r.PackageSHA256, r.PackageSize, r.UpgradeFrom = p.SHA256, int64(len(p.Raw)), st.upgradeOf
	return r, nil
}
