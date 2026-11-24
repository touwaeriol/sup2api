package migrations_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const migration0125 = "0125_market_sources_drop_public_key.sql"

// channelRows lists the channels as "branch name".
const channelRows = `SELECT branch || ' ' || name FROM market_sources`

// 0125 makes the official source the official store's default channel 正式
// on main, keeping its id, adds 测试 on develop and drops any other source;
// a rerun changes nothing and stores no url.
func TestMigration0125Stores(t *testing.T) {
	m := newMigrationDB(t, migration0125)
	m.exec(`INSERT INTO market_sources (id, name, url, public_key) VALUES (7, 'official', 'https://x/index.json', 'k'), (8, 'mirror', 'https://y/index.json', 'k')`)
	m.migrateFirst()
	const channels = `SELECT concat_ws(' ', c.id, c.name, c.branch, c.is_default, s.name, s.repo, s.official) FROM market_sources c JOIN market_stores s ON s.id = c.store_id`
	require.Equal(t, "7 正式 main t 官方商店 touwaeriol/sup2api-plugin t", m.rows(channels+` WHERE c.is_default`))
	require.Equal(t, "develop 测试, main 正式", m.rows(channelRows))
	require.Equal(t, "0", m.read(`SELECT count(*) FROM information_schema.columns WHERE table_name = 'market_sources' AND column_name = 'url'`))
	m.rerun()
	require.Equal(t, "develop 测试, main 正式", m.rows(channelRows))
	require.Equal(t, "1", m.read(`SELECT count(*) FROM market_stores`))
}

// Without sources 0125 seeds 正式 on main as the default and 测试 on
// develop; a rerun leaves a deleted channel deleted and another default as
// it is.
func TestMigration0125Seed(t *testing.T) {
	m := newMigrationDB(t, migration0125)
	m.migrateFirst()
	require.Equal(t, "develop 测试, main 正式", m.rows(channelRows))
	m.exec(`UPDATE market_sources SET is_default = false; UPDATE market_sources SET is_default = true WHERE branch = 'develop'`)
	m.exec(`DELETE FROM market_sources WHERE branch = 'main'`)
	m.rerun()
	require.Equal(t, "develop 测试", m.rows(channelRows))
}

// insertVersion records version 1.0.0 of plugin key whose manifest names
// publisher.
func insertVersion(m *migrationDB, key, publisher string) {
	m.exec(`INSERT INTO plugins (key, name, status) VALUES ('` + key + `', '{}', 'installed');
		INSERT INTO plugin_versions (plugin_key, version, manifest, manifest_hash, package_sha256, package_size, signature_status)
		VALUES ('` + key + `', '1.0.0', '{"key":"` + key + `","publisher":"` + publisher + `"}', 'h', 's', 1, 'unsigned')`)
}

// A plugin of another publisher than sub2api stops the upgrade before any
// change, naming it and its publisher; first-party plugins pass.
func TestMigration0125GuardPlugins(t *testing.T) {
	m := newMigrationDB(t, migration0125)
	insertVersion(m, "codex", "sub2api")
	insertVersion(m, "tool", "acme")
	_, err := m.migrateErr()
	require.ErrorContains(t, err, "tool (publisher acme)")
	require.NotContains(t, err.Error(), "codex")
	require.Equal(t, "0", m.read(`SELECT count(*) FROM information_schema.tables WHERE table_name = 'market_stores'`))
	m.exec(`DELETE FROM plugins WHERE key = 'tool'`)
	m.migrateFirst()
	require.Equal(t, "codex", m.rows(`SELECT key FROM plugins`))
}

// A key retired by another publisher than sub2api stops the upgrade; one
// retired by sub2api or without a publisher passes.
func TestMigration0125GuardRetirements(t *testing.T) {
	m := newMigrationDB(t, migration0125)
	m.exec(`INSERT INTO publishers (id, name, trust_level) VALUES (1, 'sub2api', 'official'), (2, 'acme', 'community');
		INSERT INTO plugin_key_retirements (plugin_key, publisher_id) VALUES ('old', 1), ('free', NULL), ('tool', 2)`)
	_, err := m.migrateErr()
	require.ErrorContains(t, err, "tool (retired key of publisher acme)")
	require.NotContains(t, err.Error(), "old")
	m.exec(`DELETE FROM plugin_key_retirements WHERE plugin_key = 'tool'`)
	m.migrateFirst()
	require.Equal(t, "free, old", m.rows(`SELECT plugin_key FROM plugin_key_retirements`))
}
