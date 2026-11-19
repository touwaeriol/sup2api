package rollout

import (
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/gormtest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// The SQL snapshots of the rollout statements (see package gormtest). Every
// UPDATE of a table with updated_at is here: it writes updated_at exactly
// where the raw SQL did.

var sqlRollout = &rolloutRow{id: 42, key: "demo", action: "upgrade", rowVersion: 7}

// --- plugins ---------------------------------------------------------------

func TestSQLLockPlugin(t *testing.T) {
	gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB {
		return selectPluginForUpdate(tx, "demo", "status", "active_version", "row_version").Take(&model.Plugin{})
	})
}

func TestSQLSelectEpochForShare(t *testing.T) {
	gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB { return selectEpochForShare(tx, "demo").Take(&model.Plugin{}) })
}

func TestSQLSelectPlugins(t *testing.T) {
	gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB { return selectPlugins(tx).Scan(&[]pluginRecord{}) })
}

// The column maps of every plugins UPDATE of the state machine; updatePlugin
// adds updated_at and the row_version bump to each.
func TestSQLUpdatePlugin(t *testing.T) {
	for name, set := range map[string]func() map[string]any{
		"enable": func() map[string]any {
			return map[string]any{"status": "enabling", "status_reason": "", "desired_version": "1.1.0"}
		},
		"upgrade": func() map[string]any { return map[string]any{"status": "upgrading", "desired_version": "1.1.0"} },
		"disable": func() map[string]any {
			return map[string]any{"status": "disabled", "status_reason": "why", "desired_version": nil}
		},
		"fail_upgrade": func() map[string]any {
			return map[string]any{"status": "enabled", "desired_version": gorm.Expr("active_version")}
		},
		"commit":   func() map[string]any { return map[string]any{"active_version": "1.1.0", "desired_version": "1.1.0"} },
		"complete": func() map[string]any { return map[string]any{"status": "enabled"} },
		"fail_otherwise": func() map[string]any {
			return map[string]any{
				"status":          gorm.Expr("CASE WHEN active_version IS NULL THEN 'installed' ELSE 'disabled' END"),
				"desired_version": nil, "status_reason": "why",
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB { return updatePlugin(tx, "demo", set()) })
		})
	}
}

func TestSQLReferencedVersions(t *testing.T) {
	gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB { return referencedVersions(tx).Scan(&[]model.PluginVersion{}) })
}

// --- plugin_rollouts --------------------------------------------------------

func TestSQLLoadRollout(t *testing.T) {
	gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB {
		return selectRollouts(tx).Where("id = ?", int64(42)).Take(&rolloutRecord{})
	})
}

func TestSQLOpenRolloutExists(t *testing.T) {
	gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB {
		var ok bool
		return tx.Raw("SELECT EXISTS (?)", openRollouts(tx, "demo").Select("1")).Scan(&ok)
	})
}

// The fence: with fenced the row version and a live lease of this boot.
func TestSQLFailRollout(t *testing.T) {
	gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB {
		return failRollout(tx, sqlRollout, PhaseCancelled, "cancelled by user 1", true, "boot-a")
	})
}

func TestSQLAdvancePhase(t *testing.T) {
	gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB {
		return advancePhaseQuery(tx, sqlRollout, PhasePreparing, PhaseActivating, "boot-a")
	})
}

// Taking over and releasing a lease move row_version but not updated_at;
// renewing moves neither.
func TestSQLClaimLease(t *testing.T) {
	gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB { return claimLease(tx, 42, "node-a", "boot-a", 30*time.Second) })
}

func TestSQLRenewLease(t *testing.T) {
	gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB { return renewLease(tx, 42, "boot-a", 30*time.Second) })
}

func TestSQLReleaseLease(t *testing.T) {
	gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB { return releaseLease(tx, 42, "boot-a") })
}

func TestSQLRecordDataMigration(t *testing.T) {
	gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB {
		return recordDataMigration(tx, "demo", "data:rollout-42:1.0.0->1.1.0")
	})
}

// --- nodes, cleanup, uninstalls ------------------------------------------------

func TestSQLUpsertRolloutNode(t *testing.T) {
	gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB {
		return upsertRolloutNode(tx, 42, "node-a", "boot-a", NodeFailed, "not ready", "outdated_contract")
	})
}

func TestSQLMarkCleanup(t *testing.T) {
	gormtest.AssertRun(t, func(tx *gorm.DB) error { return (&Controller{}).markCleanup(tx.Statement.Context, tx, sqlRollout) })
}

func TestSQLAckCleanup(t *testing.T) {
	gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB { return ackCleanup(tx, "demo", "boot-a", 7) })
}

func TestSQLCleanBootCleanup(t *testing.T) {
	gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB { return cleanBootCleanup(tx, "boot-a") })
}

func TestSQLStopBootRuntimes(t *testing.T) {
	gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB { return stopBootRuntimes(tx, "boot-a") })
}

func TestSQLStopRuntime(t *testing.T) {
	gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB { return stopRuntime(tx, "demo", "boot-a") })
}

func TestSQLUpsertRuntimeNode(t *testing.T) {
	gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB { return upsertRuntimeNode(tx, "demo", "boot-a") })
}

func TestSQLAckBootUninstalls(t *testing.T) {
	gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB { return ackBootUninstalls(tx, "boot-a") })
}

func TestSQLAckUninstallStop(t *testing.T) {
	gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB { return ackUninstallStop(tx, "demo", "boot-a", 7) })
}

// --- plugin_history ----------------------------------------------------------

// Rollout 0 is written as NULL; the empty defaulted columns are left to
// their ” defaults.
func TestSQLRecordHistory(t *testing.T) {
	gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB {
		return tx.Create(historyRow("demo", core.PluginNodeState{State: "active", Serving: "1.1.0"}, "node-a", "boot-a", `{"error":""}`))
	})
}

func TestSQLMarkBuiltinHistory(t *testing.T) {
	gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB { return markBuiltinHistory(tx, 42) })
}

func TestSQLPruneHistory(t *testing.T) {
	gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB { return pruneHistory(tx) })
}
