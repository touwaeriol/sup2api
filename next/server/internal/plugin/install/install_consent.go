package install

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// ErrPackageChanged: the package an install with consent carries is not the
// one its review showed (InstallConsent.PackageSHA256). Nothing is
// recorded; the console reviews the package again.
var ErrPackageChanged = core.NewError(http.StatusConflict, "package_changed", "the plugin package is not the one reviewed")

// InstallConsent is the consent an install carries (POST /plugins/upload
// field consent, /plugins/install-from-market body consent, CONTRACTS
// §5.7): the consent page's choices, as POST .../consent takes them, and
// the package_sha256 of the review (ReviewPackage) they were made on.
type InstallConsent struct {
	ConsentRequest
	PackageSHA256 string `json:"package_sha256"`
}

// InstallResult is what an install with consent returns: the review, its
// consent_status approved, and the consent's result.
type InstallResult struct {
	Review  *Review        `json:"review"`
	Consent *ConsentResult `json:"consent"`
}

// InstallWithConsent records the package data as Upload does and approves
// it at once with the operator's consent c, as Consent does (the same
// checks and errors), instead of carrying an earlier consent over. When the
// consent fails, what the upload recorded is undone (undoUpload). It
// neither enables nor upgrades the plugin.
func (s *Service) InstallWithConsent(ctx context.Context, data []byte, actorID int64, opt UploadOptions, c InstallConsent) (*InstallResult, error) {
	return s.upload(ctx, data, actorID, opt, &c)
}

// matches checks that p is the package the consent was given for (a nil
// consent checks nothing): package_sha256 is required, and another package
// is ErrPackageChanged.
func (c *InstallConsent) matches(p *pkg.Package) error {
	switch {
	case c == nil:
		return nil
	case c.PackageSHA256 == "":
		return core.InvalidFields(core.FieldError{Field: "consent.package_sha256", Code: "required",
			Message: "the consent must name the package_sha256 of the review it was given on"})
	case !strings.EqualFold(c.PackageSHA256, p.SHA256):
		return ErrPackageChanged.WithDetails(map[string]any{"package_sha256": p.SHA256})
	}
	return nil
}

// consentUpload gives the operator's consent c to the version the upload
// just recorded (r, rec). When it fails, the upload is undone and the
// consent's error returned.
func (s *Service) consentUpload(ctx context.Context, r *Review, rec *uploadRecord, c *InstallConsent, actorID int64) (*InstallResult, error) {
	res, err := s.Consent(ctx, r.PluginKey, r.Version, c.ConsentRequest, actorID)
	if err != nil {
		if uerr := s.undoUpload(ctx, r.PluginKey, r.Version, rec, actorID); uerr != nil {
			return nil, errors.Join(err, fmt.Errorf("undo the upload: %w", uerr))
		}
		return nil, err
	}
	r.ConsentStatus = ConsentApproved
	return &InstallResult{Review: r, Consent: res}, nil
}

// undoUpload removes what the upload of key@version recorded (rec) when it
// inserted the version row: that row while it still awaits consent and,
// when the upload created the plugin row, the plugin with its permissions
// (removeAbandoned, as Reject). A version or plugin that existed before is
// left as it was. It outlives the request (WithoutCancel): a client gone,
// or the change lock's deadline passed, during the consent must not leave
// the version recorded. The upload's audit entry stays, followed by
// plugin.upload.undo.
func (s *Service) undoUpload(ctx context.Context, key, version string, rec *uploadRecord, actorID int64) error {
	if !rec.inserted {
		return nil
	}
	ctx = context.WithoutCancel(ctx)
	return s.d.DB.Tx(ctx, func(tx *gorm.DB) error {
		if err := lockPlugin(tx, key); err != nil {
			return err
		}
		res := versionRow(store.G(ctx, tx), key, version).Where("consent_status = ?", ConsentAwaiting).Delete(&model.PluginVersion{})
		if res.Error != nil || res.RowsAffected == 0 {
			return res.Error
		}
		removed := false
		if !rec.existing {
			var err error
			if removed, err = s.removeAbandoned(ctx, tx, key, StatusAwaitingConsent); err != nil {
				return err
			}
		}
		return audit.Audit(ctx, tx, actorID, "plugin.upload.undo", "plugin", key, map[string]any{"version": version, "plugin_removed": removed})
	})
}

// removeAbandoned removes plugin key (status) when it was never installed
// and has no version left but rejected ones (Reject, undoUpload); removed
// reports it. The staged permissions' grants go first: the plugin row's
// foreign key cascades to permissions, not to casbin_rule.
func (s *Service) removeAbandoned(ctx context.Context, tx *gorm.DB, key, status string) (removed bool, err error) {
	if status != StatusAwaitingConsent {
		return false, nil
	}
	g := store.G(ctx, tx)
	var pending int64
	if err := g.Model(&model.PluginVersion{}).Where("plugin_key = ? AND consent_status <> ?", key, ConsentRejected).Count(&pending).Error; err != nil || pending > 0 {
		return false, err
	}
	if s.d.Permissions != nil {
		if err := s.d.Permissions.DeletePlugin(ctx, tx, key); err != nil {
			return false, err
		}
	}
	return true, pluginRow(g, key).Delete(&model.Plugin{}).Error
}
