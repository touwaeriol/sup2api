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
// its size and sha256 against the index. The options are the ones to hand
// it to the installer with (install.Upload, ReviewPackage,
// InstallWithConsent), which checks its signature, trust and manifest.
func (s *Service) Download(ctx context.Context, sourceID int64, key, version string) ([]byte, install.UploadOptions, error) {
	entry, src, err := s.findEntry(ctx, sourceID, key, version)
	if err != nil {
		return nil, install.UploadOptions{}, err
	}
	dl, err := downloadURL(src, entry, s.maxBytes)
	if err != nil {
		return nil, install.UploadOptions{}, err
	}
	data, err := s.fetchChecked(ctx, dl, entry)
	if err != nil {
		return nil, install.UploadOptions{}, err
	}
	return data, install.UploadOptions{ExpectKey: key, ExpectVersion: version, Source: fmt.Sprintf("market:%d", src.ID), PackageURL: dl}, nil
}

// findEntry finds key@version in the index of source sourceID. The cached
// index may be stale: a miss retries once with a fresh copy.
func (s *Service) findEntry(ctx context.Context, sourceID int64, key, version string) (*IndexVersion, *Source, error) {
	idx, src, err := s.Index(ctx, sourceID, false)
	if err != nil {
		return nil, nil, err
	}
	if entry, err := findVersion(idx, key, version); err == nil {
		return entry, src, nil
	}
	if idx, src, err = s.Index(ctx, sourceID, true); err != nil {
		return nil, nil, err
	}
	entry, err := findVersion(idx, key, version)
	return entry, src, err
}

// downloadURL is the package URL of entry resolved against the index URL of
// src: http(s) only, for an entry whose size is within maxBytes.
func downloadURL(src *Source, entry *IndexVersion, maxBytes int64) (string, error) {
	base, _ := indexURL(src.URL)
	ref, err := url.Parse(entry.URL)
	if err != nil {
		return "", core.ErrUnavailable.WithMessage("market entry has an invalid url")
	}
	dl := base.ResolveReference(ref)
	if dl.Scheme != "http" && dl.Scheme != "https" {
		return "", core.ErrUnavailable.WithMessage("market entry url must be http(s)")
	}
	if entry.Size > maxBytes {
		return "", core.ErrInvalidArgument.WithMessage(fmt.Sprintf("package size %d exceeds the limit of %d bytes", entry.Size, maxBytes))
	}
	return dl.String(), nil
}

// fetchChecked downloads the package at dl and checks it against the size
// and sha256 of entry.
func (s *Service) fetchChecked(ctx context.Context, dl string, entry *IndexVersion) ([]byte, error) {
	data, err := Fetch(ctx, s.pkg, dl, s.maxBytes)
	if err != nil {
		return nil, err
	}
	if entry.Size > 0 && int64(len(data)) != entry.Size {
		return nil, core.ErrInvalidArgument.WithMessage("downloaded package size does not match the index")
	}
	if !strings.EqualFold(pkg.SHA256Hex(data), entry.SHA256) {
		return nil, core.ErrInvalidArgument.WithMessage("downloaded package sha256 does not match the index")
	}
	return data, nil
}
