package market

import (
	"testing"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/gormtest"
)

// The SQL of the market statements built with GORM (see package gormtest).

// EnsureStores points each channel at the base and brings back the
// default one.
func TestSQLMarketEnsureStores(t *testing.T) {
	const base = "https://market.example/m"
	t.Run("urls", func(t *testing.T) {
		gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
			return staleURLs(db, 2, base).Update("url", channelURLExpr(base))
		})
	})
	t.Run("stores", func(t *testing.T) {
		gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
			var out []Store
			return storeRows(db).Order("official DESC, id").Find(&out)
		})
	})
}

func TestSQLMarketSources(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
			var out []Source
			return sources(db).Where("c.store_id = ?", 2).Order("st.official DESC, c.store_id, c.is_default DESC, c.id").Find(&out)
		})
	})
	t.Run("one", func(t *testing.T) {
		gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
			var src Source
			return sources(db).Where("c.id = ?", 5).Take(&src)
		})
	})
	t.Run("default", func(t *testing.T) {
		gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
			var src Source
			return sources(db).Where("st.official AND c.is_default").Take(&src)
		})
	})
}

func TestSQLMarketRevokedPublishers(t *testing.T) {
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
		var names []string
		return revokedPublishers(db).Pluck("name", &names)
	})
}
