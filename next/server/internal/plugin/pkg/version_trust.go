package pkg

import (
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// VersionTrust selects, as one column, the trust level version of plugin
// key runs with (CONTRACTS §136.2): the version's own trust_level, which a
// market install takes from its index, else its publisher's; NULL for an
// unsigned version (no publisher). key and version are values or, in a
// correlated subquery, column references (gorm.Expr). Every reader of a
// version's trust uses it.
func VersionTrust(db *gorm.DB, key, version any) *gorm.DB {
	return db.Table("plugin_versions AS tv").Select("COALESCE(tv.trust_level, tp.trust_level)").
		Joins("LEFT JOIN publishers tp ON tp.id = tv.publisher_id").
		Where("tv.plugin_key = ? AND tv.version = ?", key, version)
}

// ReadVersionTrust reads the trust level of version of key (VersionTrust),
// TrustUnsigned when it has none.
func ReadVersionTrust(g *gorm.DB, key, version string) (string, error) {
	var trust *string
	err := VersionTrust(g, key, version).Row().Scan(&trust)
	if err != nil && !store.IsNoRows(err) {
		return "", err
	}
	if trust == nil {
		return TrustUnsigned, nil
	}
	return *trust, nil
}
