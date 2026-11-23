package api

import (
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/gormtest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// The SQL of the plugin API statements built with GORM (see package
// gormtest).

var snapTime = time.Date(2026, 11, 15, 8, 0, 0, 0, time.UTC)

// A settings change writes its column, moves updated_at to now() and bumps
// row_version, as the raw UPDATE did.
func TestSQLPluginUpdate(t *testing.T) {
	for name, value := range map[string]any{
		"config_enc":      []byte{1, 2, 3},
		"resource_limits": []byte(`{"memory_mb":256}`),
		"egress_policy":   "allowlist",
	} {
		t.Run(name, func(t *testing.T) {
			gormtest.Assert(t, func(db *gorm.DB) *gorm.DB { return updatePlugin(db, "demo", name, value) })
		})
	}
}

func TestSQLPluginVersions(t *testing.T) {
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
		var rows []model.PluginVersion
		return versionRows(db, "demo").Find(&rows)
	})
}

func TestSQLPluginEvents(t *testing.T) {
	t.Run("backlog", func(t *testing.T) {
		gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
			var n int64
			return backlog(db, 41, []string{"usage.created"}, []string{"account.%"}).Scan(&n)
		})
	})
	t.Run("deadletters", func(t *testing.T) {
		gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
			var rows []Deadletter
			return deadletters(db, "demo").Select("id, first_event_id, last_event_id, error, created_at").Order("id DESC").Limit(50).Scan(&rows)
		})
	})
}

func TestSQLPluginJobs(t *testing.T) {
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
		var runs []JobRun
		return lastRuns(db, "demo").Scan(&runs)
	})
}

func TestSQLPluginEgress(t *testing.T) {
	t.Run("logs", func(t *testing.T) {
		gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
			var items []EgressLog
			return egressLogs(db, "demo", snapTime.Add(-time.Hour), snapTime).
				Select("id, node_id, network, host, port, started_at, closed_at, duration_ms, bytes_in, bytes_out, result, error").
				Order("started_at DESC, id DESC").Limit(20).Offset(40).Scan(&items)
		})
	})
	t.Run("domains", func(t *testing.T) {
		gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
			var out []EgressDomain
			return egressDomainRows(db, "demo").Scan(&out)
		})
	})
}

func TestSQLPluginRecordedRefusals(t *testing.T) {
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
		var rows []struct{ Key, Version, Error string }
		return recordedRefusalRows(db, []string{"demo", "video"}).Scan(&rows)
	})
}

func TestSQLPluginRollouts(t *testing.T) {
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
		var raw []byte
		return rolloutPage(db, "demo", &snapTime, 20, 0).Scan(&raw)
	})
}

// The summary's current version reads its trust level as pkg.VersionTrust.
func TestSQLPluginCurrentVersionSummary(t *testing.T) {
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
		var sig string
		return currentVersionSummary(db, "demo", "1.2.0").Scan(&sig)
	})
}
