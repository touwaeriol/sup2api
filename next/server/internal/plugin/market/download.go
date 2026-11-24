package market

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/install"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
)

// Download downloads publisher's key@version from channel sourceID (0: the
// default one) and checks its size and sha256 against the index. Its
// publisher must be listed active (403 otherwise,
// pkg.IndexedPublisher.Check). The options are the ones to hand it to the
// installer with (install.Upload, ReviewPackage, InstallWithConsent): its
// trust level is the index's, not its signature's (CONTRACTS §136).
func (s *Service) Download(ctx context.Context, sourceID int64, publisher, key, version string) ([]byte, install.UploadOptions, error) {
	e, err := s.findEntry(ctx, sourceID, publisher, key, version)
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
	return data, install.UploadOptions{ExpectID: manifest.PluginID(publisher, key), ExpectVersion: version, Source: fmt.Sprintf("market:%d", e.src.ID),
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

// findEntry finds publisher's key@version in a fresh copy of the index of
// source sourceID: an install goes by what the market lists now.
func (s *Service) findEntry(ctx context.Context, sourceID int64, publisher, key, version string) (*entry, error) {
	idx, src, err := s.Index(ctx, sourceID, true)
	if err != nil {
		return nil, err
	}
	p, v, ok := idx.find(publisher, key, version)
	if !ok {
		return nil, core.ErrNotFound.WithMessage(fmt.Sprintf("plugin %s %s not found in market index", manifest.DisplayID(manifest.PluginID(publisher, key)), version))
	}
	return s.newEntry(src, idx, p, v)
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
	if !schemeAllowed(dl, s.opt.AllowHTTP) {
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
