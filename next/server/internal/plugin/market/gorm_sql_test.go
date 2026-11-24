package market

import (
	"testing"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/gormtest"
)

// The SQL of the market statements built with GORM (see package gormtest).

// EnsureChannels points each channel at the base and brings back the
// default one.
func TestSQLMarketEnsureChannels(t *testing.T) {
	const base = "https://market.example/m"
	t.Run("urls", func(t *testing.T) {
		gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
			return staleURLs(db, base).Update("url", channelURLExpr(base))
		})
	})
	t.Run("default", func(t *testing.T) {
		gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
			return upsertDefault(db, base+"/index-main/index.json")
		})
	})
}

func TestSQLMarketSources(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
			var out []Source
			return sources(db).Order("is_default DESC, id").Find(&out)
		})
	})
	t.Run("one", func(t *testing.T) {
		gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
			var src Source
			return sources(db).Where("id = ?", 5).Take(&src)
		})
	})
	t.Run("default", func(t *testing.T) {
		gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
			var src Source
			return sources(db).Where("is_default").Take(&src)
		})
	})
}

func TestSQLMarketRevokedPublishers(t *testing.T) {
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
		var names []string
		return revokedPublishers(db).Pluck("name", &names)
	})
}
