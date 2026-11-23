package market

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/install"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
)

// Download downloads key@version from market source sourceID and checks
// its size and sha256 against the index. Its publisher must be listed
// active (403 otherwise, pkg.IndexedPublisher.Check). The options are the
// ones to hand it to the installer with (install.Upload, ReviewPackage,
// InstallWithConsent): its trust level is the index's, not its signature's
// (CONTRACTS §136).
func (s *Service) Download(ctx context.Context, sourceID int64, key, version string) ([]byte, install.UploadOptions, error) {
	e, err := s.findEntry(ctx, sourceID, key, version)
	if err != nil {
		return nil, install.UploadOptions{}, err
	}
	if err := e.publisher.Check(); err != nil {
		return nil, install.UploadOptions{}, err
	}
	dl, err := s.downloadURL(e)
	if err != nil {
		return nil, install.UploadOptions{}, err
	}
	data, err := s.fetchChecked(ctx, dl, e.version)
	if err != nil {
		return nil, install.UploadOptions{}, err
	}
	return data, install.UploadOptions{ExpectKey: key, ExpectVersion: version, Source: fmt.Sprintf("market:%d", e.src.ID),
		PackageURL: dl, Indexed: &e.publisher}, nil
}

// entry is a version of the index of src and what the index says of its
// publisher.
type entry struct {
	src       *Source
	base      *url.URL // the index URL
	version   *IndexVersion
	publisher pkg.IndexedPublisher
}

// findEntry finds key@version in the index of source sourceID. The cached
// index may be stale: a miss retries once with a fresh copy.
func (s *Service) findEntry(ctx context.Context, sourceID int64, key, version string) (*entry, error) {
	for _, refresh := range []bool{false, true} {
		idx, src, err := s.Index(ctx, sourceID, refresh)
		if err != nil {
			return nil, err
		}
		if p, v, ok := idx.find(key, version); ok {
			return s.newEntry(src, idx, p, v)
		}
	}
	return nil, core.ErrNotFound.WithMessage(fmt.Sprintf("plugin %s %s not found in market index", key, version))
}

// newEntry is the entry of version v of plugin p, listed by idx of src.
func (s *Service) newEntry(src *Source, idx *Index, p *IndexPlugin, v *IndexVersion) (*entry, error) {
	base, err := s.indexURL(src.URL)
	if err != nil {
		return nil, core.ErrInvalidArgument.WithMessage(err.Error())
	}
	return &entry{src: src, base: base, version: v, publisher: idx.indexed(p.Publisher)}, nil
}

// downloadURL is the package URL of e resolved against its index URL:
// https (http with AllowHTTP), for a version whose size is within the
// download limit.
func (s *Service) downloadURL(e *entry) (string, error) {
	ref, err := url.Parse(e.version.URL)
	if err != nil {
		return "", core.ErrUnavailable.WithMessage("market entry has an invalid url")
	}
	dl := e.base.ResolveReference(ref)
	if !s.schemeAllowed(dl) {
		return "", core.ErrUnavailable.WithMessage("market entry url must be https")
	}
	if e.version.Size > s.opt.MaxPackageBytes {
		return "", core.ErrInvalidArgument.WithMessage(fmt.Sprintf("package size %d exceeds the limit of %d bytes", e.version.Size, s.opt.MaxPackageBytes))
	}
	return dl.String(), nil
}

// fetchChecked downloads the package at dl and checks it against the size
// and sha256 of v.
func (s *Service) fetchChecked(ctx context.Context, dl string, v *IndexVersion) ([]byte, error) {
	data, err := Fetch(ctx, s.pkg, dl, s.opt.MaxPackageBytes)
	if err != nil {
		return nil, err
	}
	if v.Size > 0 && int64(len(data)) != v.Size {
		return nil, core.ErrInvalidArgument.WithMessage("downloaded package size does not match the index")
	}
	if !strings.EqualFold(pkg.SHA256Hex(data), v.SHA256) {
		return nil, core.ErrInvalidArgument.WithMessage("downloaded package sha256 does not match the index")
	}
	return data, nil
}
