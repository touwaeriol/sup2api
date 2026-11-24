package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// UploadOptions constrain an upload (market installs pin plugin id and
// version).
type UploadOptions struct {
	ExpectID      string // the plugin id (manifest.PluginID)
	ExpectVersion string
	Source        string // "upload" | "market:<source id>"
	// PackageURL is the market download URL; every node fetches the package
	// from it. Empty for uploads, which nodes fetch from a node holding them.
	PackageURL string
	// Indexed is what the market index says of the package's publisher: a
	// market install takes its trust level from it, not from the package
	// signature (TrustStore.VerifyIndexed, CONTRACTS §136). Nil otherwise.
	Indexed *pkg.IndexedPublisher
}

// Upload validates a package, stores it and returns the review. New plugins
// start in awaiting_consent; new versions of existing plugins wait for
// consent without touching the running version, unless they request no new
// or wider host permissions, in which case consent is carried over.
func (s *Service) Upload(ctx context.Context, data []byte, actorID int64, opt UploadOptions) (*Review, error) {
	res, err := s.upload(ctx, data, actorID, opt, nil)
	if res == nil {
		return nil, err
	}
	return res.Review, err
}

// upload is Upload; with c, the operator's consent c follows instead of
// carryOverConsent (InstallWithConsent).
func (s *Service) upload(ctx context.Context, data []byte, actorID int64, opt UploadOptions, c *InstallConsent) (*InstallResult, error) {
	p, err := s.preparePackage(ctx, data, opt, c)
	if err != nil {
		return nil, err
	}
	ctx, release, err := core.BeginPluginMutation(ctx, s.d.Mutations)
	if err != nil {
		return nil, err
	}
	defer release()
	rec, err := s.recordUpload(ctx, p, actorID, opt)
	if err != nil {
		return nil, err
	}
	r, err := s.reviewUpload(ctx, p, &rec.uploadState, actorID)
	if err != nil {
		return nil, err
	}
	r.ConsentStatus, r.UploadedAt = rec.consent, rec.uploadedAt
	if c != nil {
		return s.consentUpload(ctx, r, rec, c, actorID)
	}
	return &InstallResult{Review: r}, s.carryOverConsent(ctx, r, p.Manifest.ID(), p.Manifest.Version, actorID)
}

// uploadRecord is what recordUpload leaves for the review: the state it
// read and what it wrote.
type uploadRecord struct {
	uploadState
	consent    string
	uploadedAt time.Time
	// inserted: the upload inserted the version row (not an identical
	// re-upload), which undoUpload removes again.
	inserted bool
}

// preparePackage opens and checks data (openAndValidate), checks that it is
// the package c was given for (c may be nil) and stores it: the bytes must
// be retrievable before any row names them. A package rejected afterwards
// stays unreferenced and is collected by the shell.
func (s *Service) preparePackage(ctx context.Context, data []byte, opt UploadOptions, c *InstallConsent) (*pkg.Package, error) {
	p, err := s.openAndValidate(ctx, data, opt)
	if err != nil {
		return nil, err
	}
	if err := c.matches(p); err != nil {
		return nil, err
	}
	if s.d.Packages == nil || s.d.Packages.Store == nil {
		return nil, core.ErrUnavailable.WithMessage("plugin package store is not configured")
	}
	if err := s.d.Packages.Store.Put(ctx, p.SHA256, p.Raw); err != nil {
		return nil, core.ErrUnavailable.WithMessage("store the plugin package on this node").WithCause(err)
	}
	return p, nil
}

// openAndValidate unpacks the package and checks the caller's id/version
// expectations and its plugin contract. Unpacking and hashing a large package depends on nothing
// shared, so it runs before the short cluster change lock.
func (s *Service) openAndValidate(ctx context.Context, data []byte, opt UploadOptions) (*pkg.Package, error) {
	p, err := pkg.Open(data, s.Limits())
	if err != nil {
		return nil, err
	}
	m := p.Manifest
	if opt.ExpectID != "" && m.ID() != opt.ExpectID {
		return nil, core.ErrInvalidArgument.WithMessage(fmt.Sprintf("package plugin %q does not match %q", manifest.DisplayID(m.ID()), manifest.DisplayID(opt.ExpectID)))
	}
	if opt.ExpectVersion != "" && m.Version != opt.ExpectVersion {
		return nil, core.ErrInvalidArgument.WithMessage(fmt.Sprintf("package version %q does not match %q", m.Version, opt.ExpectVersion))
	}
	// Even a package stored before (whose validation is skipped below) is
	// refused when it was built for an older plugin contract.
	if err := pkg.CheckContract(m, p.Files, s.opt.HostVersion); err != nil {
		return nil, err
	}
	return p, nil
}

// checkContent validates p (validatePackage) unless this exact package is
// stored already: the core that took it validated it, and checks added
// since (the job limits of 0.1.238) must not stop it from being re-uploaded
// or installed again from the market.
func (s *Service) checkContent(ctx context.Context, p *pkg.Package, opt UploadOptions) error {
	stored, err := s.storedIdentical(ctx, p)
	if err != nil || stored {
		return err
	}
	return s.validatePackage(ctx, p, opt)
}

// storedIdentical reports whether this exact package is stored as its key and
// version (and was not rejected).
func (s *Service) storedIdentical(ctx context.Context, p *pkg.Package) (bool, error) {
	g := store.G(ctx, s.d.DB.Gorm)
	var ok bool
	err := gormpg.ExistsQuery(g, versionRow(g, p.Manifest.ID(), p.Manifest.Version).
		Where("package_sha256 = ? AND consent_status <> ?", p.SHA256, ConsentRejected)).Scan(&ok).Error
	return ok, err
}

// validatePackage runs the manifest validation against the other installed
// plugins' exclusive resources.
func (s *Service) validatePackage(ctx context.Context, p *pkg.Package, opt UploadOptions) error {
	m := p.Manifest
	otherPlatforms, otherEndpoints, otherChannels, err := s.otherResources(ctx, s.d.DB.Gorm, m.ID())
	if err != nil {
		return err
	}
	if err := pkg.Validate(m, p.Files, pkg.ValidateOptions{
		HostVersion:    s.opt.HostVersion,
		DevMode:        s.opt.Plugins.DevMode,
		GOOS:           s.opt.GOOS,
		GOARCH:         s.opt.GOARCH,
		MaxMemoryMB:    s.opt.Plugins.MaxMemoryMB,
		OtherEndpoints: otherEndpoints,
		OtherPlatforms: otherPlatforms,
		OtherChannels:  otherChannels,
	}); err != nil {
		return err
	}
	// The tables and columns outside the plugin it asks for must be the
	// core's (CONTRACTS §130).
	if m.Database == nil || len(m.Database.Access) == 0 || s.d.Schemas == nil {
		return nil
	}
	errs, err := s.d.Schemas.CheckAccess(ctx, m.Database.Access)
	if err != nil || len(errs) == 0 {
		return err
	}
	return core.InvalidFields(errs...).WithMessage("database.access names tables or columns the core does not have")
}

// recordUpload checks the content of p (checkContent), then writes the
// plugin row, the version row and the audit entry in one transaction.
func (s *Service) recordUpload(ctx context.Context, p *pkg.Package, actorID int64, opt UploadOptions) (*uploadRecord, error) {
	if err := s.checkContent(ctx, p, opt); err != nil {
		return nil, err
	}
	rec := &uploadRecord{consent: ConsentAwaiting}
	err := s.d.DB.Tx(ctx, func(tx *gorm.DB) error {
		st, err := s.readUploadState(ctx, tx, p, opt, true)
		if err != nil {
			return err
		}
		rec.uploadState = *st
		return writeUpload(ctx, tx, p, actorID, opt, rec)
	})
	if store.IsUniqueViolation(err, "") {
		// Another first upload of the plugin created its row meanwhile: one
		// that does not take the change lock (a node's first install of a
		// built-in plugin). It is another cluster change being submitted, and
		// the same retry succeeds.
		return nil, core.ErrClusterChangeBusy.WithMessage("the plugin is being uploaded concurrently; retry").WithCause(err)
	}
	if err != nil {
		return nil, err
	}
	return rec, nil
}

// writeUpload records p in tx after readUploadState (rec): the plugins row
// of a first upload, the version row unless an identical one exists (whose
// consent status rec then keeps) and the audit entry.
func writeUpload(ctx context.Context, tx *gorm.DB, p *pkg.Package, actorID int64, opt UploadOptions, rec *uploadRecord) error {
	m, ver, g := p.Manifest, rec.verification, store.G(ctx, tx)
	if !rec.existing {
		if err := g.Create(newPlugin(m, ver.PublisherID, actorID)).Error; err != nil {
			return err
		}
	}
	inserted, priorConsent, err := insertVersionRow(ctx, tx, p, ver, opt, actorID)
	if err != nil {
		return err
	}
	if rec.inserted = inserted; !inserted {
		rec.consent = priorConsent // idempotent re-upload keeps its status
	}
	if err := versionRow(g, m.ID(), m.Version).Select("uploaded_at").Take(&rec.uploadedAt).Error; err != nil {
		return err
	}
	return audit.Audit(ctx, tx, actorID, "plugin.upload", "plugin", m.ID(), map[string]any{
		"version": m.Version, "sha256": p.SHA256, "trust": ver.Trust, "signature_status": ver.SignatureStatus,
		"source": opt.Source,
	})
}

// insertVersionRow inserts the version unless it already exists with the
// same content. inserted is false for an identical re-upload, whose existing
// consent status is returned so the review can report it unchanged.
func insertVersionRow(ctx context.Context, tx *gorm.DB, p *pkg.Package, ver *pkg.Verification, opt UploadOptions, actorID int64) (inserted bool, priorConsent string, err error) {
	g := store.G(ctx, tx)
	prior, err := priorVersion(g, p)
	switch {
	case err != nil:
		return false, "", err
	case prior.rejected:
		// A rejected upload may be retried (possibly with other content).
		if err := versionRow(g, p.Manifest.ID(), p.Manifest.Version).Delete(&model.PluginVersion{}).Error; err != nil {
			return false, "", err
		}
	case prior.found:
		return false, prior.consent, nil // idempotent re-upload
	}
	return true, "", g.Create(newVersion(p, ver, opt, actorID)).Error
}

// priorUpload is the version row an upload finds (priorVersion).
type priorUpload struct {
	found    bool   // the same content: an idempotent re-upload
	consent  string // its consent status
	rejected bool   // rejected: a retry, with any content, replaces it
}

// priorVersion reads the version row of the key and version of p, a
// conflict when it holds other content and was not rejected.
func priorVersion(g *gorm.DB, p *pkg.Package) (priorUpload, error) {
	m := p.Manifest
	var old model.PluginVersion
	err := versionRow(g, m.ID(), m.Version).Select("package_sha256, consent_status").Take(&old).Error
	consent := core.ValueOr(old.ConsentStatus, "")
	switch {
	case store.IsNoRows(err):
		return priorUpload{}, nil
	case err != nil:
		return priorUpload{}, err
	case consent == ConsentRejected:
		return priorUpload{rejected: true}, nil
	case old.PackageSha256 == p.SHA256:
		return priorUpload{found: true, consent: consent}, nil
	}
	return priorUpload{}, core.ErrConflict.WithMessage(fmt.Sprintf("version %s of %q was already uploaded with different content", m.Version, m.ID()))
}

// newPlugin is the plugins row of a first upload of m, awaiting consent.
func newPlugin(m *manifest.Manifest, publisherID *int64, actorID int64) *model.Plugin {
	name, _ := json.Marshal(m.Name)
	return &model.Plugin{Key: m.ID(), Name: name, PublisherID: publisherID, Status: StatusAwaitingConsent, InstalledBy: nullID(actorID)}
}

// newVersion is the plugin_versions row of an upload of p, awaiting consent.
func newVersion(p *pkg.Package, ver *pkg.Verification, opt UploadOptions, actorID int64) *model.PluginVersion {
	m, consent := p.Manifest, ConsentAwaiting
	return &model.PluginVersion{
		PluginKey: m.ID(), Version: m.Version, Manifest: p.ManifestRaw, ManifestHash: p.ManifestHash,
		PackageSha256: p.SHA256, PackageURL: opt.PackageURL, PackageSize: int64(len(p.Raw)),
		PublisherID: ver.PublisherID, KeyID: nullStr(ver.KeyID), SignatureStatus: ver.SignatureStatus,
		TrustLevel: versionTrust(ver), ConsentStatus: &consent, UploadedBy: nullID(actorID),
	}
}

// versionTrust is the trust level a version records of its own: the index's
// for a market install (SigIndexed), nil (its publisher's) otherwise.
func versionTrust(ver *pkg.Verification) *string {
	if ver.SignatureStatus != pkg.SigIndexed {
		return nil
	}
	return &ver.Trust
}

// carryOverConsent approves an upgrade that neither adds nor widens host
// permissions (ARCHITECTURE §5.5): every grant is carried over automatically,
// unless a permission the version creates goes to a role in a way that needs
// consent (RoleGrantsNeedConsent, checked again in the consent transaction),
// which leaves the version awaiting consent like a first install, as does a
// revoked host permission it asks for (only an operator restores one).
func (s *Service) carryOverConsent(ctx context.Context, r *Review, key, version string, actorID int64) error {
	revoked := slices.ContainsFunc(r.HostPermissions, func(hp ReviewHostPerm) bool { return hp.Revoked })
	mode := consentModeOf(ctx, true)
	if r.UpgradeFrom == "" || r.ConsentStatus != ConsentAwaiting || r.Diff == nil || revoked ||
		len(r.Diff.Added) != 0 || len(r.Diff.Widened) != 0 || (r.RoleGrantsNeedConsent && mode == modeAuto) {
		return nil
	}
	_, err := s.consent(ctx, key, version, ConsentRequest{}, actorID, mode)
	switch {
	case errors.Is(err, errRoleGrantsNeedConsent):
		return nil
	case err != nil:
		return fmt.Errorf("carry over consent: %w", err)
	}
	r.ConsentStatus = ConsentApproved
	return nil
}

// otherResources lists the platforms, gateway endpoints and notification
// channels declared by every approved version of other plugins. A version awaiting approval holds no
// exclusive resource (audit 2026-10-09 P1-5): it cannot block a later upload,
// a built-in plugin's above all, and Consent checks it again before it is
// approved.
func (s *Service) otherResources(ctx context.Context, q *gorm.DB, key string) ([]pkg.PlatformOwner, []pkg.EndpointOwner, []pkg.ChannelOwner, error) {
	var rows []resourceRow
	if err := approvedResources(store.G(ctx, q), key).Find(&rows).Error; err != nil {
		return nil, nil, nil, err
	}
	var ms []*manifest.Manifest
	for _, r := range rows {
		publisher, key, _ := manifest.ParsePluginID(r.PluginKey)
		m := &manifest.Manifest{Publisher: publisher, Key: key}
		if r.Platforms != nil && json.Unmarshal(r.Platforms, &m.Platforms) != nil ||
			r.Notifications != nil && json.Unmarshal(r.Notifications, &m.Notifications) != nil {
			continue
		}
		ms = append(ms, m)
	}
	pfs, eps := pkg.OthersFromManifests(ms)
	return pfs, eps, pkg.ChannelsFromManifests(ms), nil
}

// resourceRow is what approvedResources reads of a version's manifest.
type resourceRow struct {
	PluginKey     string          `gorm:"column:plugin_key"`
	Platforms     json.RawMessage `gorm:"column:platforms"`
	Notifications json.RawMessage `gorm:"column:notifications"`
}

// approvedResources selects the platforms and notification channels of the
// approved versions of plugins other than key.
func approvedResources(db *gorm.DB, key string) *gorm.DB {
	return db.Model(&model.PluginVersion{}).
		Select("plugin_key, manifest->'platforms' AS platforms, manifest->'notifications' AS notifications").
		Where(`plugin_key <> ? AND consent_status = 'approved'
			AND (jsonb_typeof(manifest->'platforms') = 'array' OR jsonb_typeof(manifest->'notifications') = 'object')`, key).
		Order("plugin_key, version")
}

func sameID(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func nullID(id int64) *int64 {
	if id <= 0 {
		return nil
	}
	return &id
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
