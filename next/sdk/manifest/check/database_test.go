package check

import (
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// Plugin keys hold no "__" and do not end in "_", so that no plugin's table
// prefix (manifest.TablePrefix) starts another's, and fit 30 bytes.
func TestPluginKeyForm(t *testing.T) {
	for _, key := range []string{"ab", "a_b", "demo2", "my_plugin_1", "a" + strings.Repeat("b", 29)} {
		m := minimal()
		m.Key, m.Publisher = key, manifest.OfficialPublisher
		if c := codes(Validate(m, nil, ValidateOptions{Tooling: true})); len(c) > 0 {
			t.Errorf("key %q rejected: %v", key, c)
		}
	}
	for _, key := range []string{"a", "a__b", "ab_", "_ab", "1ab", "Ab", "a-b", "a" + strings.Repeat("b", 30),
		strings.Repeat("a_", 15) + "a"} {
		m := minimal()
		m.Key = key
		if c := codes(Validate(m, nil, ValidateOptions{Tooling: true})); c["key"] != "invalid_format" {
			t.Errorf("key %q: want invalid_format, got %v", key, c)
		}
	}
}

func databaseManifest(access ...manifest.DatabaseAccess) (*manifest.Manifest, map[string][]byte) {
	m := minimal()
	m.HostPermissions = []manifest.HostPermission{{ID: "db.schema"}, {ID: "db.core_read"}}
	m.Database = &manifest.Database{Migrations: "migrations/", Access: access}
	return m, map[string][]byte{"migrations/0001_init.sql": []byte("SELECT 1;")}
}

func databaseCodes(m *manifest.Manifest, files map[string][]byte) map[string]string {
	return codes(Validate(m, files, ValidateOptions{Tooling: true}))
}

func TestDatabaseAccess(t *testing.T) {
	m, files := databaseManifest(
		manifest.DatabaseAccess{Table: "users", Columns: []string{"id", "email"}, Privileges: []string{"select"}},
		manifest.DatabaseAccess{Table: "groups", Privileges: manifest.DatabasePrivileges},
	)
	if c := databaseCodes(m, files); len(c) > 0 {
		t.Fatalf("valid access rejected: %v", c)
	}
	// Without access, db.core_read is not needed.
	m, files = databaseManifest()
	m.HostPermissions = m.HostPermissions[:1]
	if c := databaseCodes(m, files); len(c) > 0 {
		t.Fatalf("a database without access: %v", c)
	}

	users := func(mut func(*manifest.DatabaseAccess)) []manifest.DatabaseAccess {
		a := manifest.DatabaseAccess{Table: "users", Columns: []string{"id"}, Privileges: []string{"select"}}
		mut(&a)
		return []manifest.DatabaseAccess{a}
	}
	cases := []struct {
		name   string
		access []manifest.DatabaseAccess
		field  string
		code   string
	}{
		{"bad table", users(func(a *manifest.DatabaseAccess) { a.Table = "Users" }), "database.access[0].table", "invalid"},
		{"qualified table", users(func(a *manifest.DatabaseAccess) { a.Table = "public.users" }), "database.access[0].table", "invalid"},
		{"empty table", users(func(a *manifest.DatabaseAccess) { a.Table = "" }), "database.access[0].table", "invalid"},
		{"plugin table", users(func(a *manifest.DatabaseAccess) { a.Table = "plg_other__rules" }), "database.access[0].table", "invalid"},
		{"duplicate table", append(users(func(*manifest.DatabaseAccess) {}), users(func(*manifest.DatabaseAccess) {})...),
			"database.access[1].table", "duplicate"},
		{"bad column", users(func(a *manifest.DatabaseAccess) { a.Columns = []string{"id", "e-mail"} }), "database.access[0].columns[1]", "invalid"},
		{"duplicate column", users(func(a *manifest.DatabaseAccess) { a.Columns = []string{"id", "id"} }), "database.access[0].columns[1]", "invalid"},
		{"no privilege", users(func(a *manifest.DatabaseAccess) { a.Privileges = nil }), "database.access[0].privileges", "required"},
		{"unknown privilege", users(func(a *manifest.DatabaseAccess) { a.Privileges = []string{"select", "truncate"} }),
			"database.access[0].privileges[1]", "invalid"},
		{"upper case privilege", users(func(a *manifest.DatabaseAccess) { a.Privileges = []string{"SELECT"} }),
			"database.access[0].privileges[0]", "invalid"},
		{"delete of columns", users(func(a *manifest.DatabaseAccess) { a.Privileges = []string{"select", "delete"} }),
			"database.access[0].privileges", "invalid"},
		{"duplicate privilege", users(func(a *manifest.DatabaseAccess) { a.Privileges = []string{"update", "update"} }),
			"database.access[0].privileges[1]", "invalid"},
	}
	for _, tc := range cases {
		m, files := databaseManifest(tc.access...)
		if c := databaseCodes(m, files); c[tc.field] != tc.code {
			t.Errorf("%s: want %s=%s, got %v", tc.name, tc.field, tc.code, c)
		}
	}

	// Access needs the critical host permission db.core_read.
	m, files = databaseManifest(users(func(*manifest.DatabaseAccess) {})...)
	m.HostPermissions = m.HostPermissions[:1]
	if c := databaseCodes(m, files); c["database.access"] != "missing_host_permission" {
		t.Fatalf("access without db.core_read: %v", c)
	}
	if manifest.HostPermissionRisk["db.core_read"] != manifest.RiskCritical {
		t.Fatal("db.core_read must be critical")
	}
	if got := manifest.TablePrefix("demo"); got != "plg_demo__" {
		t.Fatalf("TablePrefix = %q", got)
	}
}
