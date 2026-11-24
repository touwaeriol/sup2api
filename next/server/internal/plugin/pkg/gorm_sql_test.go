package pkg

import (
	"testing"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/gormtest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// The SQL of the trust store statements built with GORM (see package
// gormtest).

func TestSQLTrustSigningKey(t *testing.T) {
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
		var pub string
		return signingKey(db, "official-2026").Scan(&pub)
	})
}

// registerOfficial's inserts leave status and created_at to their defaults
// and do nothing on a concurrent first registration.
func TestSQLTrustRegisterOfficial(t *testing.T) {
	t.Run("publisher", func(t *testing.T) {
		gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
			return db.Clauses(onConflictNothing("name")).Create(&model.Publisher{Name: "sub2api", TrustLevel: TrustOfficial})
		})
	})
	t.Run("key", func(t *testing.T) {
		gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
			return db.Clauses(onConflictNothing("key_id")).Create(&model.PublisherKey{KeyID: "official-2026", PublisherID: 1, PublicKey: "b64"})
		})
	})
	// publishers has no updated_at: only trust_level moves.
	t.Run("promote", func(t *testing.T) {
		gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
			return db.Model(&model.Publisher{}).Where("id = ?", 1).Update("trust_level", TrustOfficial)
		})
	})
}

// A market install registers its publisher as community, bound to the
// index's store, unless it is registered.
func TestSQLTrustRegisterIndexed(t *testing.T) {
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
		return registerIndexed(db, IndexedPublisher{Name: "acme", StoreID: 3})
	})
}

// A version's trust: its own level, else its publisher's.
func TestSQLVersionTrust(t *testing.T) {
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
		var trust *string
		return VersionTrust(db, "demo", "1.2.0").Scan(&trust)
	})
}

func TestSQLTrustRecordedVersion(t *testing.T) {
	gormtest.Assert(t, func(db *gorm.DB) *gorm.DB {
		var v model.PluginVersion
		return recordedVersion(db, "demo", "1.2.0").Take(&v)
	})
}
