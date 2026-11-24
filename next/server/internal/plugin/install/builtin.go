package install

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// Built-in plugins ship inside the image (config Plugins.BuiltinDir). The
// core installs them at startup with every requested host permission granted,
// enables them on first install (unless listed in InstallOnlyFile) and
// upgrades enabled ones when the image carries a newer version.
// Administrators can disable them but not uninstall them.

// InstallOnlyFile, in the built-in directory, lists the keys (one per line,
// "#" comments) of built-in plugins that are installed but left disabled on
// first install: they need per-deployment configuration before they can do
// anything, so an operator enables them on purpose. Written by build-go.sh
// from BUILTIN_PLUGINS_INSTALL_ONLY. It only governs a plugin that was never
// enabled: once an operator enabled or disabled one, that choice stands.
const InstallOnlyFile = "install-only.txt"

type systemKey struct{}

// withSystem marks work done by the core itself (no operator involved).
func withSystem(ctx context.Context) context.Context {
	return context.WithValue(ctx, systemKey{}, true)
}

func isSystem(ctx context.Context) bool { v, _ := ctx.Value(systemKey{}).(bool); return v }

// ErrBuiltin is returned when an operation is not allowed on a built-in plugin.
var ErrBuiltin = core.ErrPermissionDenied.WithMessage("built-in plugins cannot be uninstalled; disable them instead").
	WithDetails(map[string]any{"reason": "builtin"})

// IsBuiltin reports whether key is a built-in plugin.
func IsBuiltin(ctx context.Context, q *gorm.DB, key string) (bool, error) {
	var b bool
	err := pluginRow(store.G(ctx, q), key).Select("builtin").Take(&b).Error
	if store.IsNoRows(err) {
		return false, nil
	}
	return b, err
}

type builtinPkg struct {
	path    string
	key     string // the plugin id
	version *semver.Version
	data    []byte
}

// readInstallOnly reads InstallOnlyFile from dir; a missing file lists nothing.
func readInstallOnly(dir string) (map[string]bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, InstallOnlyFile))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	keys := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		keys[line] = true
	}
	return keys, sc.Err()
}

// EnsureBuiltin installs, marks and enables (or upgrades) every package in
// dir. Errors of one package are logged and do not stop the others. Run it on
// one node at a time (cluster lock); it is idempotent.
func (s *Service) EnsureBuiltin(ctx context.Context, dir string, log *slog.Logger) error {
	ctx = audit.WithSource(ctx, "builtin")
	if dir == "" {
		return nil
	}
	// Unreadable is not "empty": treating it so would enable, on every
	// deployment, the plugins that were meant to stay off.
	installOnly, err := readInstallOnly(dir)
	if err != nil {
		log.Error("builtin plugins: read "+InstallOnlyFile+"; no built-in plugin is installed", "err", err)
		return err
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.s2plugin"))
	if err != nil {
		return err
	}
	// Newest version per plugin id.
	latest := map[string]*builtinPkg{}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			log.Error("builtin plugin: read", "path", p, "err", err)
			continue
		}
		pk, err := pkg.Open(data, s.Limits())
		if err != nil {
			log.Error("builtin plugin: invalid package", "path", p, "err", err)
			continue
		}
		v, err := semver.NewVersion(pk.Manifest.Version)
		if err != nil {
			log.Error("builtin plugin: invalid version", "path", p, "version", pk.Manifest.Version)
			continue
		}
		if cur := latest[pk.Manifest.ID()]; cur == nil || v.GreaterThan(cur.version) {
			latest[pk.Manifest.ID()] = &builtinPkg{path: p, key: pk.Manifest.ID(), version: v, data: data}
		}
	}
	keys := make([]string, 0, len(latest))
	for k := range latest {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var failures []error
	for _, k := range keys {
		if err := s.ensureBuiltin(withSystem(ctx), latest[k], installOnly[k], log); err != nil {
			log.Error("builtin plugin", "plugin", k, "version", latest[k].version.Original(), "err", err)
			failures = append(failures, fmt.Errorf("%s: %w", k, err))
		}
	}
	return errors.Join(failures...)
}

func (s *Service) ensureBuiltin(ctx context.Context, b *builtinPkg, installOnly bool, log *slog.Logger) error {
	version := b.version.Original()
	_, err := s.Upload(ctx, b.data, 0, UploadOptions{ExpectID: b.key, ExpectVersion: version, Source: "builtin"})
	var ce *core.Error
	switch {
	case err == nil:
	case errors.As(err, &ce) && ce.Code == core.ErrConflict.Code && strings.Contains(ce.Message, "different content"):
		// The same version was stored from an earlier image build; keep it.
		log.Warn("builtin plugin: version already stored with other content, keeping the stored one",
			"plugin", b.key, "version", version)
	default:
		return fmt.Errorf("upload %s: %w", filepath.Base(b.path), err)
	}

	g := store.G(ctx, s.d.DB.Gorm)
	var consent string
	if err := versionRow(g, b.key, version).Select("consent_status").Take(&consent).Error; err != nil {
		return err
	}
	if consent == ConsentAwaiting {
		m, err := LoadManifest(ctx, s.d.DB.Gorm, b.key, version)
		if err != nil {
			return err
		}
		var req ConsentRequest // new permissions go to their manifest defaults
		for _, hp := range m.HostPermissions {
			req.Grants = append(req.Grants, GrantInput{Permission: hp.ID})
		}
		if _, err := s.Consent(ctx, b.key, version, req, 0); err != nil {
			return fmt.Errorf("consent: %w", err)
		}
		log.Info("builtin plugin approved", "plugin", b.key, "version", version)
	}
	if err := markBuiltin(g, b.key).Error; err != nil {
		return err
	}

	if s.d.Rollout == nil {
		return nil
	}
	if ro, err := s.d.Rollout.Current(ctx, b.key); err != nil || ro != nil {
		return err // a rollout is already running; the next start re-checks
	}
	var p model.Plugin
	if err := pluginRow(g, b.key).Select("status, active_version").Take(&p).Error; err != nil {
		return err
	}
	status, active := p.Status, p.ActiveVersion
	switch {
	case status == StatusInstalled && active == nil && installOnly:
		// Never enabled and install-only: an operator enables it once it is
		// configured; Enable then picks the newest approved version.
		log.Info("builtin plugin installed, left disabled (install-only)", "plugin", b.key, "version", version)
	case status == StatusInstalled && active == nil:
		// Never enabled: built-in plugins are on by default. A disabled one
		// stays disabled (the operator chose so).
		if _, err := s.d.Rollout.Enable(ctx, b.key, 0); err != nil {
			return fmt.Errorf("enable: %w", err)
		}
		log.Info("builtin plugin enabling", "plugin", b.key, "version", version)
	case status == StatusEnabled && active != nil && *active != version:
		cur, err := semver.NewVersion(*active)
		if err == nil && cur.LessThan(b.version) {
			if _, err := s.d.Rollout.Upgrade(ctx, b.key, version, 0); err != nil {
				return fmt.Errorf("upgrade: %w", err)
			}
			log.Info("builtin plugin upgrading", "plugin", b.key, "from", *active, "to", version)
		}
	}
	return nil
}

// markBuiltin marks key built-in, moving updated_at only when it was not.
func markBuiltin(db *gorm.DB, key string) *gorm.DB {
	return pluginRow(db, key).Where("NOT builtin").
		Updates(map[string]any{"builtin": true, "updated_at": gorm.Expr("now()")})
}
