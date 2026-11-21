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
// long as it is installed. Read from the node's own copy
// of the immutable package rather than a process cache: rollout cleanup may
// close that cache concurrently, and a joining node may never have run this
// version, in which case the package is fetched like any other.
func (s *Packages) ReadVersionAsset(ctx context.Context, key, vh, name string) (core.PluginInfo, []byte, string, error) {
	var version, sum, url string
	err := servedVersion(store.G(ctx, s.db.Gorm), key, vh, name).Row().Scan(&version, &sum, &url)
	if store.IsNoRows(err) {
		return core.PluginInfo{}, nil, "", ErrAssetNotFound
	}
	if err != nil {
		return core.PluginInfo{}, nil, "", err
	}
	sum = strings.ToLower(sum)
	file := filepath.Join(s.dataDir, key, vh, "package.s2plugin")
	raw, err := os.ReadFile(file)
	if err != nil || !strings.EqualFold(pluginpkg.SHA256Hex(raw), sum) {
		local := &Package{Key: key, Version: version, SHA256: sum, Dir: filepath.Dir(file)}
		if err = s.materialize(ctx, local, func() ([]byte, error) { return s.source.Fetch(ctx, sum, url) }); err != nil {
			return core.PluginInfo{}, nil, "", err
		}
		// This package is not owned by the cache. Release its file handle
		// after this read so version cleanup can remove it on Windows too.
		defer local.Close()
		if raw, err = os.ReadFile(file); err != nil {
			return core.PluginInfo{}, nil, "", err
		}
	}
	if !strings.EqualFold(pluginpkg.SHA256Hex(raw), sum) {
		return core.PluginInfo{}, nil, "", fmt.Errorf("package checksum mismatch")
	}
	if s.verifier != nil {
		if err := s.verifier(ctx, key, version, raw); err != nil {
			return core.PluginInfo{}, nil, "", err
		}
	}
	p, err := pluginpkg.Open(raw, pluginpkg.Limits{MaxPackageBytes: int64(len(raw)) + 1})
	if err != nil {
		return core.PluginInfo{}, nil, "", err
	}
	if p.Manifest.Key != key || p.Manifest.Version != version {
		return core.PluginInfo{}, nil, "", fmt.Errorf("package identity mismatch")
	}
	data, ok := p.Files[name]
	if !ok {
		return core.PluginInfo{}, nil, "", ErrAssetNotFound
	}
	return core.PluginInfo{Key: key, Version: version, Manifest: p.Manifest, AssetBase: core.PluginAssetBase(key, vh)}, data, ContentType(name), nil
}

// servedVersion reads the version, package sha256 and package URL of the
// version of key whose version hash is vh, if it may serve name: any asset
// while the plugin runs, only its icon while it is installed but not running
// (the plugins list shows those too).
func servedVersion(db *gorm.DB, key, vh, name string) *gorm.DB {
	uninstalling := db.Model(&model.PluginUninstall{}).Select("1").Where("plugin_uninstalls.plugin_key = p.key")
	return db.Table("plugin_versions AS v").Select("v.version, v.package_sha256, v.package_url").
		Joins("JOIN plugins p ON p.key = v.plugin_key").
		Where("p.key = ? AND v.version || '-' || left(v.package_sha256, 8) = ?", key, vh).
		Where("v.consent_status = 'approved' AND v.signature_status <> 'revoked'").
		Where("p.status IN ('enabled', 'enabling', 'upgrading') OR v.manifest->>'icon' = ?", name).
		Where("NOT EXISTS (?)", uninstalling)
}
