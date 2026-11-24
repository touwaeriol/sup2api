package registrytest

import (
	"crypto/sha256"
	"testing"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/gormtest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// The SQL of the fixture inserts built with GORM (see package gormtest).

var snapManifest = &manifest.Manifest{Publisher: manifest.OfficialPublisher, Key: "demo", Version: "1.2.0"}

func TestSQLFixturePlugin(t *testing.T) {
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
		return db.Create(&model.Plugin{Key: "demo", Name: []byte(`{"en":"Demo"}`), Status: "enabled"})
	})
}

// The version is approved: consent_status is written, not defaulted.
func TestSQLFixtureVersion(t *testing.T) {
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
		return db.Create(versionRow(snapManifest, []byte(`{}`), sha256.Sum256(nil), sha256.Sum256([]byte("pkg")), 3))
	})
}

func TestSQLFixtureGrant(t *testing.T) {
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB { return db.Create(grantRow(snapManifest, "kv", `{}`)) })
}
