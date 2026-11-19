package dbschema

import (
	"testing"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/gormtest"
)

// The SQL snapshots of the dbschema statements (see package gormtest).

// The marker comes first: the arguments follow the placeholders' order.
func TestSQLRoleOwned(t *testing.T) {
	gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB {
		var owned []bool
		return roleOwnedQuery(tx, "plg_demo", "plg_demo", "sub2api plugin role; database=app; plugin=demo").Find(&owned)
	})
}

func TestSQLPending(t *testing.T) {
	gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB {
		var n int64
		return pluginMigrations(tx, "demo").Where("migration_id = ANY(?)", store.Strings{"0001_init.sql"}).Count(&n)
	})
}

func TestSQLDropMigrationRecords(t *testing.T) {
	gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB { return dropMigrationRecords(tx, "demo") })
}

func TestSQLApplied(t *testing.T) {
	gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB {
		return Tracker{PluginKey: "demo"}.appliedQuery(tx).Scan(&[]migrationRecord{})
	})
}

func TestSQLRecord(t *testing.T) {
	gormtest.Assert(t, func(tx *gorm.DB) *gorm.DB {
		return Tracker{PluginKey: "demo"}.recordQuery(tx, "0001_init.sql", "abc")
	})
}
