package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"gorm.io/gorm"

	runtimecontract "github.com/Sub2API-Devs/sup2api/next/runtime-contract"
	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/migrations"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/grants"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

type coreMigration struct {
	name, checksum string
	body           []byte
}

func migrationInventory(fsys fs.FS) ([]coreMigration, error) {
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var out []coreMigration
	for _, name := range names {
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		out = append(out, coreMigration{name: name, checksum: store.MigrationChecksum(b), body: b})
	}
	return out, nil
}

func inventoryContract(inventory []coreMigration) string {
	h := sha256.New()
	for _, m := range inventory {
		fmt.Fprintf(h, "%s\x00%d\x00", m.name, len(m.body))
		_, _ = h.Write(m.body)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// appliedPrefix refuses unknown, changed, or non-contiguous migration history.
// An older binary must not interpret a database after a newer schema change.
func appliedPrefix(inventory []coreMigration, applied map[string]string) (int, error) {
	n := 0
	for _, m := range inventory {
		checksum, ok := applied[m.name]
		if !ok {
			continue
		}
		if checksum != m.checksum {
			return 0, fmt.Errorf("migration %s has a different checksum", m.name)
		}
		if n >= len(inventory) || inventory[n].name != m.name {
			return 0, fmt.Errorf("migration history has a gap before %s", m.name)
		}
		n++
	}
	if n != len(applied) {
		return 0, fmt.Errorf("database includes migrations unknown to this core")
	}
	return n, nil
}

func validateMigrationPermit(inventory []coreMigration, applied map[string]string, p runtimecontract.PrepareRequest) error {
	if p.ExpectedSchemaAfter != "" && p.ExpectedSchemaAfter != inventoryContract(inventory) {
		return fmt.Errorf("target schema differs from embedded migrations")
	}
	n, err := appliedPrefix(inventory, applied)
	if err != nil {
		return err
	}
	if p.ExpectedSchemaBefore == "" {
		if p.Bootstrap {
			return nil
		}
		return fmt.Errorf("non-bootstrap migration requires expected_schema_before")
	}
	before := -1
	for i := range len(inventory) + 1 {
		if inventoryContract(inventory[:i]) == p.ExpectedSchemaBefore {
			before = i
			break
		}
	}
	if before < 0 {
		return fmt.Errorf("approved source schema is not a compatible prefix of embedded migrations")
	}
	if n < before {
		return fmt.Errorf("database schema is older than the approved source schema")
	}
	return nil
}

type permittedCoreTracker struct {
	store.CoreTracker
	inventory []coreMigration
	permit    runtimecontract.PrepareRequest
}

func (t permittedCoreTracker) Applied(ctx context.Context, q *gorm.DB) (map[string]string, error) {
	applied, err := t.CoreTracker.Applied(ctx, q)
	if err != nil {
		return nil, err
	}
	// Store.Migrate calls Applied after taking its existing database migration
	// lock, so another migration cannot change the precondition in between.
	if err = validateMigrationPermit(t.inventory, applied, t.permit); err != nil {
		return nil, err
	}
	return applied, nil
}

func prepareCoreSchema(ctx context.Context, db *store.DB, p runtimecontract.PrepareRequest) ([]string, error) {
	inventory, err := migrationInventory(migrations.FS)
	if err != nil {
		return nil, err
	}
	if p.ExpectedSchemaAfter != "" && p.ExpectedSchemaAfter != inventoryContract(inventory) {
		return nil, fmt.Errorf("target schema differs from embedded migrations")
	}
	if !p.AllowMigration {
		return nil, VerifyCoreSchema(ctx, db)
	}
	applied, err := (store.CoreTracker{}).Applied(ctx, db.Gorm)
	if err != nil {
		if !p.Bootstrap || store.SQLState(err) != "42P01" { // undefined_table
			return nil, err
		}
		applied = map[string]string{}
	}
	if err = validateMigrationPermit(inventory, applied, p); err != nil {
		return nil, err
	}
	tracker := permittedCoreTracker{inventory: inventory, permit: p}
	names, err := store.Migrate(ctx, db, migrations.FS, tracker, store.MigrateOptions{LockKey: store.CoreMigrationLockKey})
	if err != nil {
		return names, err
	}
	return names, VerifyCoreSchema(ctx, db)
}

// SchemaContract identifies the exact embedded migration inventory. It is
// available before any configuration, database connection or initialization.
func SchemaContract() (string, error) {
	inventory, err := migrationInventory(migrations.FS)
	if err != nil {
		return "", err
	}
	return inventoryContract(inventory), nil
}

// VerifyCoreSchema fails unless the database holds exactly this core's
// migrations; it never creates a tracker or runs SQL migrations. A prepared
// candidate and the server CLI need it. Once a maintenance upgrade changes
// the database, an older binary cannot safely infer compatibility.
func VerifyCoreSchema(ctx context.Context, db *store.DB) error {
	applied, err := (store.CoreTracker{}).Applied(ctx, db.Gorm)
	if err != nil {
		return err
	}
	inventory, err := migrationInventory(migrations.FS)
	if err != nil {
		return err
	}
	n, err := appliedPrefix(inventory, applied)
	if err != nil {
		return err
	}
	if n != len(inventory) {
		return fmt.Errorf("core schema is missing %d migrations; explicit migration approval required", len(inventory)-n)
	}
	return nil
}

// errAdmissionRevoked is PostgreSQL's definite answer that this boot may not
// serve, as opposed to a query error.
var errAdmissionRevoked = errors.New("node admission revoked")

func verifyAdmission(ctx context.Context, db *store.DB, node string, a runtimecontract.Admission) error {
	// Whether the updater approved this boot of node with these roles (the
	// managed shell's table, outside the core schema).
	g := store.G(ctx, db.Gorm)
	approved, err := gormpg.Exists(g, g.Table("updater.node_admissions").
		Where("node_id = ? AND core_boot_id = ? AND release_digest = ? AND revision = ?", node, a.BootID, a.ReleaseDigest, a.Revision).
		Where("serve_http = ? AND claim_background = ? AND coordinate_plugins = ?", a.ServeHTTP, a.ClaimBackground, a.CoordinatePlugins))
	if err != nil {
		return err
	}
	if !approved {
		return fmt.Errorf("%w: admission not approved", errAdmissionRevoked)
	}
	return nil
}

// pluginState is a plugin row with the versions of a live rollout ("" when
// none).
type pluginState struct {
	Key, Status string
	Active      string `gorm:"column:active"`
	From        string `gorm:"column:from_version"`
	Target      string `gorm:"column:target_version"`
	Revision    int64  `gorm:"column:row_version"`
}

// pluginStates selects every plugin as a pluginState, by key.
func pluginStates(db *gorm.DB) *gorm.DB {
	return db.Table("plugins p").
		Select("p.key, p.status, COALESCE(p.active_version,'') AS active, p.row_version, COALESCE(r.from_version,'') AS from_version, COALESCE(r.target_version,'') AS target_version").
		Joins("LEFT JOIN plugin_rollouts r ON r.plugin_key=p.key AND r.phase IN ('preparing','activating')").
		Order("p.key")
}

// Readiness follows the actual approved plugin catalog, including third-party
// plugins. Bundled versions do not impose a minimum or resurrect removed rows.
// During a live rollout either approved generation is allowed to keep serving.
func approvedPluginsReady(ctx context.Context, db *store.DB, reg core.PluginRegistry, hostVersion string) (string, error) {
	gen := reg.Current()
	g := store.G(ctx, db.Gorm)
	var plugins []pluginState
	if err := pluginStates(g).Find(&plugins).Error; err != nil {
		return "", err
	}
	var evidence []string
	for _, p := range plugins {
		evidence = append(evidence, fmt.Sprintf("%s:%d", p.Key, p.Revision))
		if p.Status != "enabled" && p.Status != "upgrading" {
			continue
		}
		if gen == nil {
			return "", fmt.Errorf("plugin %s has not loaded", manifest.DisplayID(p.Key))
		}
		local, ok := gen.Plugin(p.Key)
		if !ok || (local.Version != p.Active && local.Version != p.From && local.Version != p.Target) {
			return "", fmt.Errorf("plugin %s has not converged to an approved version", manifest.DisplayID(p.Key))
		}
		approved, err := gormpg.Exists(g, g.Model(&model.PluginVersion{}).
			Where("plugin_key = ? AND version = ? AND consent_status = 'approved'", p.Key, local.Version))
		if err != nil {
			return "", err
		}
		if !approved {
			return "", fmt.Errorf("plugin %s approval is missing", manifest.DisplayID(p.Key))
		}
		if local.Manifest == nil {
			return "", fmt.Errorf("plugin %s manifest is missing", manifest.DisplayID(p.Key))
		}
		granted, err := grants.Granted(ctx, db.Gorm, p.Key, local.Version, local.Manifest.HostPermissions)
		if err != nil {
			return "", err
		}
		for _, permission := range local.GrantedPermissions {
			if _, ok := granted[permission]; !ok {
				return "", fmt.Errorf("plugin %s permission revocation has not converged", manifest.DisplayID(p.Key))
			}
		}
		for _, permission := range local.Manifest.HostPermissions {
			if _, ok := granted[permission.ID]; !ok && !permission.Optional {
				return "", fmt.Errorf("plugin %s required permission %s is not granted", manifest.DisplayID(p.Key), permission.ID)
			}
		}
		compatible, err := check.HostCompatible(local.Manifest.HostCompat, hostVersion)
		if err != nil || !compatible {
			return "", fmt.Errorf("plugin %s is incompatible with core %s", manifest.DisplayID(p.Key), hostVersion)
		}
	}
	sort.Strings(evidence)
	sum := sha256.Sum256([]byte(strings.Join(evidence, "\n")))
	return hex.EncodeToString(sum[:]), nil
}
