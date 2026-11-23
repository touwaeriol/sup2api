package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	runtimecontract "github.com/Sub2API-Devs/sup2api/next/runtime-contract"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pkgsig"
)

// remote reaches GitHub: the REST API for releases and release asset
// downloads. Tests replace newRemote.
type remote struct {
	client  *http.Client
	apiBase string // GitHub REST API root
	token   string // GITHUB_TOKEN, sent to the API only
}

var newRemote = func() *remote {
	return &remote{
		client:  &http.Client{Timeout: 10 * time.Minute},
		apiBase: "https://api.github.com",
		token:   os.Getenv("GITHUB_TOKEN"),
	}
}

// get issues a GET and fails on any status but 200.
func (r *remote) get(u string, api bool) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if api {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if r.token != "" {
			req.Header.Set("Authorization", "Bearer "+r.token)
		}
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s %s", u, resp.Status, strings.TrimSpace(string(body)))
	}
	return resp, nil
}

// download saves u to a temporary file within the package size limit; the
// caller removes it.
func (r *remote) download(u string) (string, error) {
	resp, err := r.get(u, false)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.ContentLength > runtimecontract.PluginPackageMaxBytes {
		return "", fmt.Errorf("%s is %d bytes, the package limit is %d", u, resp.ContentLength, runtimecontract.PluginPackageMaxBytes)
	}
	f, err := os.CreateTemp("", "sub2api-registry-*"+PackageExt)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(&limitedWriter{w: f, left: runtimecontract.PluginPackageMaxBytes}, resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("%s: %w", u, err)
	}
	return f.Name(), nil
}

// pkgInfo is a package read for the registry.
type pkgInfo struct {
	sha256 string
	size   int64
	m      *manifest.Manifest
	files  map[string][]byte
}

// inspectPackage reads a package and validates its manifest the way pack
// does.
func inspectPackage(p string) (*pkgInfo, error) {
	files, err := readPackage(p)
	if err != nil {
		return nil, err
	}
	m, err := parseManifest(files[pkgsig.ManifestFile])
	if err != nil {
		return nil, err
	}
	if invalid := validateManifest(m, files); len(invalid) > 0 {
		return nil, fmt.Errorf("invalid manifest: %s", strings.Join(invalid, "; "))
	}
	sum, size, err := fileSHA256(p)
	if err != nil {
		return nil, err
	}
	return &pkgInfo{sha256: sum, size: size, m: m, files: files}, nil
}

// version is the registry version file describing this package at u.
func (i *pkgInfo) version(u string) regVersion {
	return regVersion{Version: i.m.Version, URL: u, SHA256: i.sha256, Size: i.size, HostCompat: i.m.HostCompat}
}

// fetchPackage downloads a release asset of plugin p and checks that the
// package is that plugin's.
func (r *remote) fetchPackage(p *regPlugin, u string) (*pkgInfo, error) {
	if _, err := releaseAssetTag(p.Repo, u); err != nil {
		return nil, err
	}
	tmp, err := r.download(u)
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp)
	info, err := inspectPackage(tmp)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", u, err)
	}
	if info.m.Key != p.Key || info.m.Publisher != p.Publisher {
		return nil, fmt.Errorf("%s: the package is %s by %q, the registry entry is %s by %q", u, info.m.Key, info.m.Publisher, p.Key, p.Publisher)
	}
	return info, nil
}
