package market

import (
	"testing"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/gormtest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// The SQL of the market statements built with GORM (see package gormtest).

func TestSQLMarketSeedSource(t *testing.T) {
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
		return seedSource(db, "official", "https://market.example/index.json")
	})
}

// EnsureOfficial deletes every source but the official one.
func TestSQLMarketOtherSources(t *testing.T) {
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
		return otherSources(db).Delete(&model.MarketSource{})
	})
}

func TestSQLMarketSources(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
			var out []Source
			return sources(db).Order("id").Find(&out)
		})
	})
	t.Run("one", func(t *testing.T) {
		gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
			var src Source
			return sources(db).Where("id = ?", 5).Take(&src)
		})
	})
}

func TestSQLMarketRevokedPublishers(t *testing.T) {
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
		var names []string
		return revokedPublishers(db).Pluck("name", &names)
	})
}
