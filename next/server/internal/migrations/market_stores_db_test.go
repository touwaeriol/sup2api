package migrations_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 0125 makes the official source the official store's default channel 正式
// on main, keeping its id, adds 测试 on develop and drops any other source;
// a rerun changes nothing.
func TestMigration0125Stores(t *testing.T) {
	m := newMigrationDB(t, "0125_market_sources_drop_public_key.sql")
	m.exec(`INSERT INTO market_sources (id, name, url, public_key) VALUES (7, 'official', 'https://x/index.json', 'k'), (8, 'mirror', 'https://y/index.json', 'k')`)
	m.migrateFirst()
	const channels = `SELECT concat_ws(' ', c.id, c.name, c.branch, c.is_default, s.name, s.repo, s.official, c.url) FROM market_sources c JOIN market_stores s ON s.id = c.store_id`
	want := "7 正式 main t 官方商店 touwaeriol/sup2api-plugin t https://raw.githubusercontent.com/touwaeriol/sup2api-plugin/index-main/index.json"
	require.Equal(t, want, m.rows(channels+` WHERE c.is_default`))
	require.Equal(t, "develop 测试, main 正式", m.rows(`SELECT branch || ' ' || name FROM market_sources`))
	m.rerun()
	require.Equal(t, "develop 测试, main 正式", m.rows(`SELECT branch || ' ' || name FROM market_sources`))
	require.Equal(t, "1", m.read(`SELECT count(*) FROM market_stores`))
}
