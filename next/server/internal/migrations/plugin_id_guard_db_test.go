package migrations_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const migration0126 = "0126_plugin_id_guard.sql"

// insertVersion records version 1.0.0 of plugin key whose manifest names
// publisher.
func insertVersion(m *migrationDB, key, publisher string) {
	m.exec(`INSERT INTO plugins (key, name, status) VALUES ('` + key + `', '{}', 'installed');
		INSERT INTO plugin_versions (plugin_key, version, manifest, manifest_hash, package_sha256, package_size, signature_status)
		VALUES ('` + key + `', '1.0.0', '{"key":"` + key + `","publisher":"` + publisher + `"}', 'h', 's', 1, 'unsigned')`)
}

// A plugin of another publisher than sub2api stops the upgrade, naming it
// and its publisher; first-party plugins pass, and nothing changes.
func TestMigration0126Guard(t *testing.T) {
	m := newMigrationDB(t, migration0126)
	insertVersion(m, "codex", "sub2api")
	insertVersion(m, "tool", "acme")
	_, err := m.migrateErr()
	require.ErrorContains(t, err, "tool (publisher acme)")
	require.NotContains(t, err.Error(), "codex")
	m.exec(`DELETE FROM plugins WHERE key = 'tool'`)
	m.migrateFirst()
	require.Equal(t, "codex", m.rows(`SELECT key FROM plugins`))
	m.rerun()
}
