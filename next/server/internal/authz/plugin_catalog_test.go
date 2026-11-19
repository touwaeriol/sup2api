package authz

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/authz/casbinrule"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/install"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// The roles a plugin permission may name in defaults are exactly the
// built-in roles but the super admin.
func TestManifestBuiltinRoleKeysMatchBuiltinRoles(t *testing.T) {
	var keys []string
	for _, r := range builtinRoles {
		if !r.superuser {
			keys = append(keys, r.key)
		}
	}
	slices.Sort(keys)
	want := slices.Sorted(slices.Values(manifest.BuiltinRoleKeys))
	if !slices.Equal(keys, want) {
		t.Fatalf("built-in roles %v, manifest.BuiltinRoleKeys %v", keys, want)
	}
}

// permissionHolders lists the roles holding every row of key.
func permissionHolders(t *testing.T, db *store.DB, key string) []string {
	t.Helper()
	var roles []string
	require.NoError(t, db.SQL.QueryRowContext(context.Background(), `
SELECT COALESCE(array_agg(role ORDER BY role), '{}') FROM (`+casbinrule.Grants+`) g WHERE action = $1 AND scope = ''`, key).Scan(&roles))
	return roles
}

// A permission the catalog creates goes to its GrantRoles (never a super
// admin role), each grant audited, and nothing else: the consent resolves
// inheritance into GrantRoles. A key no longer declared is marked removed,
// and an existing permission keeps its grants.
func TestSyncPluginGrantRoles(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	s := newService(t, db, newMemBus(), nil)
	ctx := context.Background()
	if _, err := db.SQL.ExecContext(ctx, `INSERT INTO plugins (key, name, status) VALUES ('guard', '{"en":"Guard"}', 'enabled')`); err != nil {
		t.Fatal(err)
	}
	sync := func(defs ...core.PermissionDef) {
		t.Helper()
		if err := db.Tx(ctx, func(tx *gorm.DB) error { return s.SyncPlugin(ctx, tx, "guard", defs) }); err != nil {
			t.Fatal(err)
		}
	}
	const read, view, manage = "plugin.guard:stats:read", "plugin.guard:stats:view", "plugin.guard:rules:manage"
	sync(core.PermissionDef{Key: "stats:read", GrantRoles: unscoped(RoleAdmin, RoleSuperAdmin, RoleOperator, RoleOperator)},
		core.PermissionDef{Key: "rules:manage"})
	if got := permissionHolders(t, db, read); !slices.Equal(got, []string{RoleAdmin, RoleOperator}) {
		t.Fatalf("holders of %s: %v", read, got)
	}
	if got := permissionHolders(t, db, manage); len(got) != 0 {
		t.Fatalf("holders of %s: %v", manage, got)
	}
	if n := auditCount(t, db, "authz.sync.grant", read, "default"); n != 2 {
		t.Fatalf("default grant audits: %d", n)
	}

	// stats:read is renamed stats:view: the new key gets its GrantRoles
	// only, the old one is removed with its grants kept.
	sync(core.PermissionDef{Key: "stats:view", GrantRoles: unscoped(RoleUser)},
		core.PermissionDef{Key: "rules:manage", GrantRoles: unscoped(RoleAdmin)})
	if got := permissionHolders(t, db, view); !slices.Equal(got, []string{RoleUser}) {
		t.Fatalf("holders of %s: %v", view, got)
	}
	if got := permissionHolders(t, db, manage); len(got) != 0 {
		t.Fatalf("existing %s widened: %v", manage, got)
	}
	if got := permissionStatus(t, db, read); got != "removed" {
		t.Fatalf("old key %s: %s", read, got)
	}
	if got := permissionHolders(t, db, read); !slices.Equal(got, []string{RoleAdmin, RoleOperator}) {
		t.Fatalf("grants of the removed %s: %v", read, got)
	}
}

// Handing new plugin permissions to a role is a change of that role
// (editableRole) on the actor's assignable list (T59): a custom role within
// the actor and on its list only, never one it holds itself (wildcard or
// listed by name) unless it is the super admin, a built-in role within the
// actor and on its list (role:define is the caller's check, install
// consent), super admin roles never; unknown roles are invalid.
func TestCanGrantToRoles(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	s := newService(t, db, newMemBus(), nil)
	ctx := context.Background()
	root := addUser(t, s, "root@x.com", RoleSuperAdmin)
	ops := addUser(t, s, "ops@x.com", RoleOperator)
	installerPerms := []string{"user:read", "role:define", "plugin:install"}
	for _, in := range []CreateRoleInput{
		{Key: "small", PermissionKeys: []string{"user:read"}},
		{Key: "other", PermissionKeys: []string{"user:read"}},
		{Key: "big", PermissionKeys: append([]string{"user:update"}, installerPerms...)},
		// installer may assign small, big and the built-in user role;
		// crew may assign every custom role but its own.
		{Key: "installer", PermissionKeys: installerPerms, AssignableRoleKeys: []string{"small", "big", RoleUser}},
		{Key: "crew", PermissionKeys: installerPerms, AssignCustom: true},
		// lead's list names lead; pair's names held, which pair holds too.
		{Key: "lead", PermissionKeys: installerPerms},
		{Key: "held", PermissionKeys: []string{"user:read"}},
		{Key: "pair", PermissionKeys: installerPerms, AssignableRoleKeys: []string{"held"}},
	} {
		in.Name = lt(in.Key, in.Key)
		r, err := s.CreateRole(ctx, root, in)
		if err != nil {
			t.Fatal(err)
		}
		if in.Key == "lead" {
			if _, err := s.SetRoleAssignable(ctx, root, r.ID, AssignableInput{RoleKeys: []string{"lead"}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	installer := addUser(t, s, "installer@x.com", "installer")
	crew := addUser(t, s, "crew@x.com", "crew")
	lead := addUser(t, s, "lead@x.com", "lead")
	pair := addUser(t, s, "pair@x.com", "pair", "held")
	if err := db.Tx(ctx, func(tx *gorm.DB) error {
		_, err := s.SetUserRoles(ctx, tx, 0, root, []string{RoleSuperAdmin, "small"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	can := func(actor int64, roles ...string) string {
		t.Helper()
		var code string
		_ = db.Tx(ctx, func(tx *gorm.DB) error {
			if err := s.CanGrantToRoles(ctx, tx, actor, roles, manifest.RiskLow); err != nil {
				code = core.AsError(err).Code
			}
			return nil
		})
		return code
	}
	for _, c := range []struct {
		name  string
		actor int64
		roles []string
		want  string
	}{
		{"custom within and assignable", installer, []string{"small", "small"}, ""},
		{"custom not assignable", installer, []string{"small", "other"}, "permission_denied"},
		{"custom wider", installer, []string{"small", "big"}, "permission_denied"},
		{"built-in assignable but wider", installer, []string{RoleUser}, "permission_denied"},
		{"own role", installer, []string{"installer"}, "permission_denied"},
		{"every custom role but its own", crew, []string{"small", "other"}, ""},
		{"own role under every custom role", crew, []string{"crew"}, "permission_denied"},
		{"own role listed by name", lead, []string{"lead"}, "permission_denied"},
		{"another held role listed by name", pair, []string{"held"}, "permission_denied"},
		{"custom without assignable list", ops, []string{"small"}, "permission_denied"},
		{"built-in within and assignable", ops, []string{RoleUser, RoleSupplier}, ""},
		{"built-in not assignable", ops, []string{RoleOperator}, "permission_denied"},
		{"super admin role", ops, []string{RoleSuperAdmin}, "permission_denied"},
		{"unknown", ops, []string{"nope"}, "invalid_argument"},
		{"none", ops, nil, ""},
		{"super admin on built-in and custom", root, []string{RoleAdmin, RoleUser, "big", "installer", "crew"}, ""},
		{"super admin on a role it also holds", root, []string{"small"}, ""},
		{"super admin on super admin role", root, []string{RoleSuperAdmin}, "permission_denied"},
	} {
		if got := can(c.actor, c.roles...); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// Syncing manifests over their old keys with what a consent decided
// (activation: nothing): the real moderation manifest splits its keys, and a
// manifest renames its keys with inheritFrom (as growth did before its
// removal, CONTRACTS §125). The split and renamed keys get no holders from
// the catalog, whatever their defaults (the consent resolves inheritance and
// defaults), and the old keys are removed with their grants kept (D6).
func TestSyncPluginSplitsAndRenamesKeys(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	s := newService(t, db, newMemBus(), nil)
	ctx := context.Background()
	root := addUser(t, s, "root@x.com", RoleSuperAdmin)
	if _, err := s.CreateRole(ctx, root, CreateRoleInput{Key: "moderator", Name: lt("Moderator", "审核员"), PermissionKeys: []string{"user:read"}}); err != nil {
		t.Fatal(err)
	}
	sync := func(key string, defs []core.PermissionDef) {
		t.Helper()
		if err := db.Tx(ctx, func(tx *gorm.DB) error { return s.SyncPlugin(ctx, tx, key, defs) }); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "plugins", "moderation", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var moderation manifest.Manifest
	if err := json.Unmarshal(raw, &moderation); err != nil {
		t.Fatal(err)
	}
	all := manifest.RoleDefault{Unscoped: true}
	renamed := &manifest.Manifest{Key: "referral", UserPermissions: []manifest.UserPermission{
		{Key: "referral:use", Defaults: map[string]manifest.RoleDefault{"user": all, "admin": all}, InheritFrom: []string{"referral:read"}},
		{Key: "referral:stats:read", Risk: manifest.RiskSensitive, Defaults: map[string]manifest.RoleDefault{"admin": all}, InheritFrom: []string{"referral:manage"}},
	}}
	for _, key := range []string{"referral", "moderation"} {
		if _, err := db.SQL.ExecContext(ctx, `INSERT INTO plugins (key, name, status) VALUES ($1, '{"en":"x"}', 'enabled')`, key); err != nil {
			t.Fatal(err)
		}
	}
	sync("referral", []core.PermissionDef{{Key: "referral:read", GrantRoles: unscoped(RoleUser, RoleAdmin)}, {Key: "referral:manage", GrantRoles: unscoped(RoleAdmin)}})
	sync("moderation", []core.PermissionDef{{Key: "moderation:read", GrantRoles: unscoped(RoleAdmin, "moderator")}, {Key: "moderation:manage", GrantRoles: unscoped(RoleAdmin)}})

	sync("referral", install.PermissionDefs(renamed, nil))
	sync("moderation", install.PermissionDefs(&moderation, nil))
	for key, want := range map[string][]string{
		"plugin.referral:referral:use":        {},
		"plugin.referral:referral:stats:read": {},
		"plugin.referral:referral:read":       {RoleAdmin, RoleUser},
		"plugin.referral:referral:manage":     {RoleAdmin},
		"plugin.moderation:moderation:exempt": {},
		"plugin.moderation:moderation:read":   {RoleAdmin, "moderator"},
	} {
		if got := permissionHolders(t, db, key); !slices.Equal(got, want) {
			t.Errorf("holders of %s: %v, want %v", key, got, want)
		}
	}
	for _, key := range []string{"plugin.referral:referral:read", "plugin.referral:referral:manage"} {
		if got := permissionStatus(t, db, key); got != "removed" {
			t.Errorf("%s: %s", key, got)
		}
	}
}

// GrantPluginPermission grants an existing plugin permission to the named
// roles but super admin ones, each grant audited (via review) and the authz
// version moved; a core permission is never granted this way.
func TestGrantPluginPermission(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	s := newService(t, db, newMemBus(), nil)
	ctx := context.Background()
	if _, err := db.SQL.ExecContext(ctx, `INSERT INTO plugins (key, name, status) VALUES ('guard', '{"en":"Guard"}', 'enabled')`); err != nil {
		t.Fatal(err)
	}
	const key = "plugin.guard:rules:export"
	grant := func(key string, roles ...string) {
		t.Helper()
		if err := db.Tx(ctx, func(tx *gorm.DB) error {
			if err := s.SyncPlugin(ctx, tx, "guard", []core.PermissionDef{{Key: "rules:export"}}); err != nil {
				return err
			}
			return s.GrantPluginPermission(ctx, tx, key, unscoped(roles...))
		}); err != nil {
			t.Fatal(err)
		}
	}
	before := s.Version()
	grant(key, RoleUser, RoleSuperAdmin, RoleUser)
	if got := permissionHolders(t, db, key); !slices.Equal(got, []string{RoleUser}) {
		t.Fatalf("holders %v", got)
	}
	if n := auditCount(t, db, "authz.sync.grant", key, "review"); n != 1 {
		t.Fatalf("%d audits", n)
	}
	if s.Version() <= before {
		t.Fatal("authz version did not move")
	}
	grant("user:read", RoleUser)
	if got := permissionHolders(t, db, "user:read"); slices.Contains(got, RoleUser) {
		t.Fatalf("a core permission was granted: %v", got)
	}

	// Scopes the permission takes are granted; others are refused, and a
	// removed key grants nothing.
	const run = "plugin.guard:flow:run"
	flowRun := core.PermissionDef{Key: "flow:run", ScopeType: "plugin:flow", Scope: &manifest.PermissionScope{Resource: "flow", Own: true}}
	grantScoped := func(declared bool, roles map[string][]string) error {
		defs := []core.PermissionDef{{Key: "rules:export"}}
		if declared {
			defs = append(defs, flowRun)
		}
		return db.Tx(ctx, func(tx *gorm.DB) error {
			if err := s.SyncPlugin(ctx, tx, "guard", defs); err != nil {
				return err
			}
			return s.GrantPluginPermission(ctx, tx, run, roles)
		})
	}
	if err := grantScoped(true, map[string][]string{RoleOperator: {"own"}, RoleUser: {"proxy/1"}}); core.AsError(err).Code != "invalid_argument" {
		t.Fatalf("a scope the permission does not take: %v", err)
	}
	if err := grantScoped(true, map[string][]string{RoleOperator: {"own", "flow/2"}}); err != nil {
		t.Fatal(err)
	}
	if got := scopedHolders(t, db, run); !slices.Equal(got, []string{"operator:flow/2", "operator:own"}) {
		t.Fatalf("scoped holders %v", got)
	}
	if err := grantScoped(false, map[string][]string{RoleUser: {""}}); err != nil {
		t.Fatal(err)
	}
	if got := permissionHolders(t, db, run); len(got) != 0 {
		t.Fatalf("a removed key was granted: %v", got)
	}
}

// syncer syncs the plugin key's permissions in a transaction of its own.
func syncer(t *testing.T, s *Service, key string) func(defs ...core.PermissionDef) error {
	t.Helper()
	ctx := context.Background()
	if _, err := s.db.SQL.ExecContext(ctx, `INSERT INTO plugins (key, name, status) VALUES ($1, '{"en":"P"}', 'enabled')`, key); err != nil {
		t.Fatal(err)
	}
	return func(defs ...core.PermissionDef) error {
		return s.db.Tx(ctx, func(tx *gorm.DB) error { return s.SyncPlugin(ctx, tx, key, defs) })
	}
}

// scopedHolders lists the scope grants of key as "role:scope".
func scopedHolders(t *testing.T, db *store.DB, key string) []string {
	t.Helper()
	var out []string
	require.NoError(t, db.SQL.QueryRowContext(context.Background(), `
SELECT COALESCE(array_agg(role || ':' || scope ORDER BY role, scope), '{}') FROM (`+casbinrule.Grants+`) g WHERE action = $1 AND scope <> ''`, key).Scan(&out))
	return out
}

// auditCount counts the audits of action naming key with via.
func auditCount(t *testing.T, db *store.DB, action, key, via string) int {
	t.Helper()
	var n int
	require.NoError(t, db.SQL.QueryRowContext(context.Background(), `SELECT count(*) FROM audit_logs
WHERE action = $1 AND detail->>'key' = $2 AND detail->>'via' = $3`, action, key, via).Scan(&n))
	return n
}

// statusAudits lists the details of the system's action audits of plugin
// guard, oldest first.
func statusAudits(t *testing.T, db *store.DB, action string) []string {
	t.Helper()
	var out []string
	require.NoError(t, db.SQL.QueryRowContext(context.Background(), `SELECT COALESCE(array_agg(detail::text ORDER BY id), '{}') FROM audit_logs
WHERE action = $1 AND target_type = 'plugin' AND target_id = 'guard' AND user_id IS NULL`, action).Scan(&out))
	return out
}

// A key a plugin no longer declares is marked removed, its grants kept and
// granting nothing; declared again it revives with the plugin's status and
// the grants it then has, never its GrantRoles (D6). Enabling or disabling
// the plugin leaves it removed. Both changes are audited with the roles
// whose grants stop or start counting.
func TestSyncPluginRemovesAndRevives(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	s := newService(t, db, newMemBus(), nil)
	ctx := context.Background()
	sync := syncer(t, s, "guard")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	admin := addUser(t, s, "a@x.com", RoleAdmin)
	const a, b = "plugin.guard:a:read", "plugin.guard:b:read"
	must(sync(core.PermissionDef{Key: "a:read"}, core.PermissionDef{Key: "b:read", GrantRoles: unscoped(RoleAdmin, RoleOperator)}))
	eventually(t, func() bool { ok, _ := s.Can(ctx, admin, b); return ok })

	must(sync(core.PermissionDef{Key: "a:read"}))
	if got := permissionStatus(t, db, b); got != "removed" {
		t.Fatalf("undeclared %s: %s", b, got)
	}
	if got := permissionHolders(t, db, b); !slices.Equal(got, []string{RoleAdmin, RoleOperator}) {
		t.Fatalf("grants of a removed key: %v", got)
	}
	removed := statusAudits(t, db, "authz.sync.remove")
	require.Len(t, removed, 1)
	assert.JSONEq(t, `{"keys":["plugin.guard:b:read"],"holders":{"plugin.guard:b:read":["admin","operator"]}}`, removed[0])
	eventually(t, func() bool { ok, _ := s.Can(ctx, admin, b); return !ok })
	if set, err := s.RolesPermissions(ctx, db.Gorm, []string{RoleAdmin}); err != nil || set.Has(b) {
		t.Fatalf("grant view counts a removed key: %v", err)
	}

	// Enabling or disabling the plugin never brings it back.
	for _, active := range []bool{true, false} {
		must(db.Tx(ctx, func(tx *gorm.DB) error { return s.SetPluginActive(ctx, tx, "guard", active) }))
		if got := permissionStatus(t, db, b); got != "removed" {
			t.Fatalf("SetPluginActive(%v): %s", active, got)
		}
	}
	must(db.Tx(ctx, func(tx *gorm.DB) error { return s.SetPluginActive(ctx, tx, "guard", true) }))

	// Revoked while removed: stays revoked; the revival grants nothing new.
	testutil.Revoke(t, db, RoleOperator, b)
	must(sync(core.PermissionDef{Key: "a:read"}, core.PermissionDef{Key: "b:read", GrantRoles: unscoped(RoleUser)}))
	if got := permissionStatus(t, db, b); got != "active" {
		t.Fatalf("revived %s: %s", b, got)
	}
	if got := permissionHolders(t, db, b); !slices.Equal(got, []string{RoleAdmin}) {
		t.Fatalf("revived grants: %v", got)
	}
	revived := statusAudits(t, db, "authz.sync.revive")
	require.Len(t, revived, 1)
	assert.JSONEq(t, `{"keys":["plugin.guard:b:read"],"holders":{"plugin.guard:b:read":["admin"]}}`, revived[0])
	assert.Len(t, statusAudits(t, db, "authz.sync.remove"), 1, "a sync removing nothing writes nothing")
	if n := auditCount(t, db, "authz.sync.grant", b, "default"); n != 2 {
		t.Fatalf("default grant audits: %d", n)
	}
	eventually(t, func() bool { ok, _ := s.Can(ctx, admin, b); return ok })

	// Revived while the plugin is not running: disabled, as a new key.
	must(sync(core.PermissionDef{Key: "a:read"}))
	if _, err := db.SQL.ExecContext(ctx, `UPDATE plugins SET status = 'disabled' WHERE key = 'guard'`); err != nil {
		t.Fatal(err)
	}
	must(sync(core.PermissionDef{Key: "a:read"}, core.PermissionDef{Key: "b:read"}))
	if got := permissionStatus(t, db, b); got != "disabled" {
		t.Fatalf("revived under a disabled plugin: %s", got)
	}
	if got := permissionStatus(t, db, a); got != "active" {
		t.Fatalf("existing %s: %s", a, got)
	}
}

// A new plugin permission stores its risk, scope, group, requires and
// defaults, and is granted its GrantRoles scopes and all: every row as the
// grant of scope * (making the role's other scopes redundant), the others
// as scoped grants, audited with the scoped key. Scopes the permission
// does not take, a risk out of the three levels and a critical permission
// with defaults are refused.
func TestSyncPluginScopedGrants(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	s := newService(t, db, newMemBus(), nil)
	ctx := context.Background()
	sync := syncer(t, s, "mod")
	flow := &manifest.PermissionScope{Resource: "flow", Own: true, Options: "/scopes/flow"}
	const key = "plugin.mod:flow:manage"
	if err := sync(core.PermissionDef{Key: "flow:manage", ScopeType: "plugin:flow", Scope: flow, Risk: manifest.RiskSensitive,
		Group: "flow", GroupLabel: lt("Flows", "流程"), GroupSort: 10, Requires: []string{"plugin.mod:flow:read"},
		Defaults:   map[string][]string{RoleOperator: {"own"}},
		GrantRoles: map[string][]string{RoleAdmin: {""}, RoleOperator: {"own", "flow/3", "own"}, RoleUser: {"own", ""}},
	}); err != nil {
		t.Fatal(err)
	}
	if got := permissionHolders(t, db, key); !slices.Equal(got, []string{RoleAdmin, RoleUser}) {
		t.Fatalf("holders %v", got)
	}
	if got := scopedHolders(t, db, key); !slices.Equal(got, []string{"operator:flow/3", "operator:own"}) {
		t.Fatalf("scoped holders %v", got)
	}
	if n := auditCount(t, db, "authz.sync.grant", key+":own", "default"); n != 1 {
		t.Fatalf("audits of the own grant: %d", n)
	}
	var risk, scopeType, resource, group, defaults string
	var groupSort int
	var requires []string
	if err := db.SQL.QueryRowContext(ctx, `SELECT risk, scope_type, scope->>'resource', group_key, group_sort, requires, defaults::text
FROM permissions WHERE key = $1`, key).Scan(&risk, &scopeType, &resource, &group, &groupSort, &requires, &defaults); err != nil {
		t.Fatal(err)
	}
	if risk != manifest.RiskSensitive || scopeType != "plugin:flow" || resource != "flow" || group != "flow" || groupSort != 10 ||
		!slices.Equal(requires, []string{"plugin.mod:flow:read"}) || defaults != `{"operator": ["own"]}` {
		t.Fatalf("stored %s %s %s %s %d %v %s", risk, scopeType, resource, group, groupSort, requires, defaults)
	}

	enum := &manifest.PermissionScope{Resource: manifest.ScopeValueResource, Values: []manifest.ScopeValue{{Key: "input"}, {Key: "output"}}}
	if err := sync(core.PermissionDef{Key: "flow:manage", ScopeType: "plugin:flow", Scope: flow},
		core.PermissionDef{Key: "stage:run", ScopeType: "plugin:value", Scope: enum, GrantRoles: map[string][]string{RoleAdmin: {"value/input"}}}); err != nil {
		t.Fatal(err)
	}
	if got := scopedHolders(t, db, "plugin.mod:stage:run"); !slices.Equal(got, []string{"admin:value/input"}) {
		t.Fatalf("enum holders %v", got)
	}
	for _, c := range []struct {
		name string
		def  core.PermissionDef
	}{
		{"core scope", core.PermissionDef{Key: "x:a", ScopeType: "plugin:flow", Scope: flow, GrantRoles: map[string][]string{RoleAdmin: {"proxy/3"}}}},
		{"own not taken", core.PermissionDef{Key: "x:b", ScopeType: "plugin:flow", Scope: &manifest.PermissionScope{Resource: "flow"}, GrantRoles: map[string][]string{RoleAdmin: {"own"}}}},
		{"value not listed", core.PermissionDef{Key: "x:c", ScopeType: "plugin:value", Scope: enum, GrantRoles: map[string][]string{RoleAdmin: {"value/x"}}}},
		{"unscoped permission", core.PermissionDef{Key: "x:d", GrantRoles: map[string][]string{RoleAdmin: {"flow/3"}}}},
		{"default not taken", core.PermissionDef{Key: "x:e", Defaults: map[string][]string{RoleAdmin: {"own"}}}},
		{"unknown risk", core.PermissionDef{Key: "x:f", Risk: "high"}},
		{"critical with defaults", core.PermissionDef{Key: "x:g", Risk: manifest.RiskCritical, Defaults: map[string][]string{RoleAdmin: {""}}}},
	} {
		if err := sync(c.def); core.AsError(err).Code != "invalid_argument" {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

// Scope grants the declared scope no longer takes are deleted on the next
// sync, each audited (via scope_changed); grants of every row stay.
func TestSyncPluginPrunesScopesOnScopeChange(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	s := newService(t, db, newMemBus(), nil)
	sync := syncer(t, s, "mod")
	const key = "plugin.mod:flow:manage"
	def := func(scopeType string, scope *manifest.PermissionScope) core.PermissionDef {
		return core.PermissionDef{Key: "flow:manage", ScopeType: scopeType, Scope: scope}
	}
	first := def("plugin:flow", &manifest.PermissionScope{Resource: "flow", Own: true})
	first.GrantRoles = map[string][]string{RoleAdmin: {""}, RoleOperator: {"own", "flow/3"}}
	for _, d := range []core.PermissionDef{first, def(first.ScopeType, first.Scope)} { // unchanged: nothing goes
		if err := sync(d); err != nil {
			t.Fatal(err)
		}
	}
	if got := scopedHolders(t, db, key); !slices.Equal(got, []string{"operator:flow/3", "operator:own"}) {
		t.Fatalf("scoped holders %v", got)
	}
	if err := sync(def("plugin:flow", &manifest.PermissionScope{Resource: "flow"})); err != nil {
		t.Fatal(err)
	}
	if got := scopedHolders(t, db, key); !slices.Equal(got, []string{"operator:flow/3"}) {
		t.Fatalf("own no longer taken: %v", got)
	}
	if n := auditCount(t, db, "authz.sync.revoke", key+":own", "scope_changed"); n != 1 {
		t.Fatalf("revoke audits: %d", n)
	}
	if err := sync(def("plugin:ticket", &manifest.PermissionScope{Resource: "ticket"})); err != nil {
		t.Fatal(err)
	}
	if got := scopedHolders(t, db, key); len(got) != 0 {
		t.Fatalf("another resource: %v", got)
	}
	if got := permissionHolders(t, db, key); !slices.Equal(got, []string{RoleAdmin}) {
		t.Fatalf("grants of every row: %v", got)
	}
}

// PluginGrantees answers CanGrantToRoles's question per role without
// failing or moving the authz version.
func TestPluginGrantees(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	s := newService(t, db, newMemBus(), nil)
	ctx := context.Background()
	root := addUser(t, s, "root@x.com", RoleSuperAdmin)
	// lead may assign small and mine, and holds mine; big grants more than
	// lead holds; other has a holder and is not on lead's list.
	for _, in := range []CreateRoleInput{
		{Key: "big", PermissionKeys: []string{"user:read", "balance:adjust:any"}},
		{Key: "small", PermissionKeys: []string{"user:read"}},
		{Key: "mine", PermissionKeys: []string{"user:read"}},
		{Key: "other", PermissionKeys: []string{"user:read"}},
		{Key: "lead", PermissionKeys: []string{"user:read", "role:define"}, AssignableRoleKeys: []string{"small", "mine"}},
	} {
		in.Name = lt(in.Key, in.Key)
		if _, err := s.CreateRole(ctx, root, in); err != nil {
			t.Fatal(err)
		}
	}
	lead := addUser(t, s, "lead@x.com", "lead", "mine")
	addUser(t, s, "other@x.com", "other")
	grantees := func(actor int64, roles ...string) (core.PluginGrantees, error) {
		var out core.PluginGrantees
		err := db.Tx(ctx, func(tx *gorm.DB) error {
			var err error
			out, err = s.PluginGrantees(ctx, tx, actor, roles)
			return err
		})
		return out, err
	}
	before := s.Version()
	all := []string{RoleSuperAdmin, RoleAdmin, "big", "small", "mine", "other"}
	got, err := grantees(lead, all...)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{RoleSuperAdmin: "superuser_role", RoleAdmin: "not_editable", "big": "not_editable", "other": "not_assignable", "mine": "held"}
	if got.Superuser || !maps.Equal(got.Denied, want) {
		t.Fatalf("lead: %+v", got)
	}
	if got, err := grantees(root, all...); err != nil || !got.Superuser || !maps.Equal(got.Denied, map[string]string{RoleSuperAdmin: "superuser_role"}) {
		t.Fatalf("super admin: %+v %v", got, err)
	}
	if _, err := grantees(lead, "nope"); core.AsError(err).Code != "invalid_argument" {
		t.Fatalf("unknown role: %v", err)
	}
	if s.Version() != before {
		t.Fatal("PluginGrantees moved the authz version")
	}
}

// A critical plugin permission is granted by a superuser only: never by
// another actor, nor by the super admin through an access key (D3).
func TestCanGrantToRolesCritical(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	s := newService(t, db, newMemBus(), nil)
	ctx := context.Background()
	root := addUser(t, s, "root@x.com", RoleSuperAdmin)
	admin := addUser(t, s, "admin@x.com", RoleAdmin)
	rootKey := core.WithAccessKey(ctx, core.AccessKeyIdentity{UserID: root, Permissions: []string{"role:define", "role:read", "user:read"}})
	for _, c := range []struct {
		name  string
		ctx   context.Context
		actor int64
		want  string
	}{
		{"admin", ctx, admin, "critical"},
		{"super admin through an access key", rootKey, root, "critical"},
		{"super admin", ctx, root, ""},
		{"system", ctx, 0, ""},
	} {
		var reason string
		_ = db.Tx(c.ctx, func(tx *gorm.DB) error {
			if err := s.CanGrantToRoles(c.ctx, tx, c.actor, []string{RoleOperator}, manifest.RiskCritical); err != nil {
				reason, _ = core.AsError(err).Details["reason"].(string)
				if reason == "" {
					reason = err.Error()
				}
			}
			return nil
		})
		if reason != c.want {
			t.Errorf("%s: %q, want %q", c.name, reason, c.want)
		}
	}
}
