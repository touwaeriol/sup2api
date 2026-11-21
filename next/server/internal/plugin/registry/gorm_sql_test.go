package registry

import (
	"testing"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/gormtest"
)

// The SQL of the registry statements built with GORM (see package
// gormtest).

func TestSQLRegistryVersionRecord(t *testing.T) {
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
		var manifest []byte
		return versionRecord(db, "demo", "1.2.0").Scan(&manifest)
	})
}

func TestSQLRegistryServedVersion(t *testing.T) {
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
		var version string
		return servedVersion(db, "demo", "1.2.0-0123abcd", "assets/icon.svg").Scan(&version)
	})
}
