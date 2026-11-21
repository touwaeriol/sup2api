package registry

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	pluginpkg "github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// ReadVersionAsset serves approved versioned assets for as long as the plugin
// remains enabled and its package is retained, and the plugin's icon for as
// long as it is installed.
func (s *Packages) ReadVersionAsset(ctx context.Context, key, vh, name string) (core.PluginInfo, []byte, string, error) {
	var version, sum, url string
	var icon bool
	err := servedVersion(store.G(ctx, s.db.Gorm), key, vh, name).Row().Scan(&version, &sum, &url, &icon)
	if store.IsNoRows(err) {
		return core.PluginInfo{}, nil, "", ErrAssetNotFound
	}
	if err != nil {
		return core.PluginInfo{}, nil, "", err
	}
	v := servedPackage{key: key, version: version, vh: vh, sum: strings.ToLower(sum), url: url}
	if !icon {
		a, err := s.readPackageAsset(ctx, v, name)
		return a.info, a.data, a.contentType, err
	}
	// The plugins list asks for the icon of every plugin that does not run,
	// and each read loads a whole package: one at a time, once per package.
	s.iconMu.Lock()
	defer s.iconMu.Unlock()
	a, ok := s.icons[v.sum]
	if !ok {
		if a, err = s.readPackageAsset(ctx, v, name); err != nil {
			return core.PluginInfo{}, nil, "", err
		}
		s.icons[v.sum] = a
	}
	return a.info, a.data, a.contentType, nil
}

// servedPackage is the package of a version servedVersion lets through.
type servedPackage struct{ key, version, vh, sum, url string }

// versionAsset is one file of a package and the version it belongs to.
type versionAsset struct {
	info        core.PluginInfo
	data        []byte
	contentType string
}

// readPackageAsset reads name from the node's own copy of the immutable
// package rather than a process cache: rollout cleanup may close that cache
// concurrently, and a joining node may never have run this version, in which
// case the package is fetched like any other.
func (s *Packages) readPackageAsset(ctx context.Context, v servedPackage, name string) (versionAsset, error) {
	file := filepath.Join(s.dataDir, v.key, v.vh, "package.s2plugin")
	raw, err := os.ReadFile(file)
	if err != nil || !strings.EqualFold(pluginpkg.SHA256Hex(raw), v.sum) {
		local := &Package{Key: v.key, Version: v.version, SHA256: v.sum, Dir: filepath.Dir(file)}
		if err = s.materialize(ctx, local, func() ([]byte, error) { return s.source.Fetch(ctx, v.sum, v.url) }); err != nil {
			return versionAsset{}, err
		}
		// This package is not owned by the cache. Release its file handle
		// after this read so version cleanup can remove it on Windows too.
		defer local.Close()
		if raw, err = os.ReadFile(file); err != nil {
			return versionAsset{}, err
		}
	}
	if !strings.EqualFold(pluginpkg.SHA256Hex(raw), v.sum) {
		return versionAsset{}, fmt.Errorf("package checksum mismatch")
	}
	if s.verifier != nil {
		if err := s.verifier(ctx, v.key, v.version, raw); err != nil {
			return versionAsset{}, err
		}
	}
	p, err := pluginpkg.Open(raw, pluginpkg.Limits{MaxPackageBytes: int64(len(raw)) + 1})
	if err != nil {
		return versionAsset{}, err
	}
	if p.Manifest.Key != v.key || p.Manifest.Version != v.version {
		return versionAsset{}, fmt.Errorf("package identity mismatch")
	}
	data, ok := p.Files[name]
	if !ok {
		return versionAsset{}, ErrAssetNotFound
	}
	info := core.PluginInfo{Key: v.key, Version: v.version, Manifest: p.Manifest, AssetBase: core.PluginAssetBase(v.key, v.vh)}
	return versionAsset{info: info, data: data, contentType: ContentType(name)}, nil
}

// servedVersion reads the version, package sha256 and package URL of the
// version of key whose version hash is vh, and whether name is its icon, if
// it may serve name: any asset while the plugin runs, only its icon while it
// is installed but not running (the plugins list shows those too).
func servedVersion(db *gorm.DB, key, vh, name string) *gorm.DB {
	uninstalling := db.Model(&model.PluginUninstall{}).Select("1").Where("plugin_uninstalls.plugin_key = p.key")
	return db.Table("plugin_versions AS v").
		Select("v.version, v.package_sha256, v.package_url, COALESCE(v.manifest->>'icon' = ?, false)", name).
		Joins("JOIN plugins p ON p.key = v.plugin_key").
		Where("p.key = ? AND v.version || '-' || left(v.package_sha256, 8) = ?", key, vh).
		Where("v.consent_status = 'approved' AND v.signature_status <> 'revoked'").
		Where("p.status IN ('enabled', 'enabling', 'upgrading') OR v.manifest->>'icon' = ?", name).
		Where("NOT EXISTS (?)", uninstalling)
}
