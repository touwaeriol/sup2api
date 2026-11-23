package install

import (
	"context"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// ReviewPackage is the install preview (POST /plugins/review-upload,
// /plugins/review-from-market, CONTRACTS §5.7): the review of the package
// data as Upload would return it for actorID against the current state,
// diff and role grants of an upgrade included, failing as Upload would
// (readUploadState, priorVersion). It records nothing: no row, no audit
// entry, nothing in the package store, and it takes no cluster change
// lock. Its consent_status is that of the same package already stored
// (approved: installing it takes no consent), else empty; it has no
// uploaded_at.
func (s *Service) ReviewPackage(ctx context.Context, data []byte, actorID int64, opt UploadOptions) (*Review, error) {
	p, err := s.openAndValidate(ctx, data, opt)
	if err != nil {
		return nil, err
	}
	if err := s.checkContent(ctx, p, opt); err != nil {
		return nil, err
	}
	st, prior, err := s.previewState(ctx, p, opt)
	if err != nil {
		return nil, err
	}
	r, err := s.reviewUpload(ctx, p, st, actorID)
	if err != nil {
		return nil, err
	}
	r.ConsentStatus = prior.consent
	return r, nil
}

// previewState reads what an upload of p would be checked and reviewed
// against in a read-only transaction, the version row included.
func (s *Service) previewState(ctx context.Context, p *pkg.Package, opt UploadOptions) (*uploadState, priorUpload, error) {
	var st *uploadState
	var prior priorUpload
	err := s.d.DB.TxOptions(ctx, store.ReadOnly(), func(tx *gorm.DB) error {
		var err error
		if st, err = s.readUploadState(ctx, tx, p, opt, false); err != nil {
			return err
		}
		prior, err = priorVersion(store.G(ctx, tx), p)
		return err
	})
	return st, prior, err
}
