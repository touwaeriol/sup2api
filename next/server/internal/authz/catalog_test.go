package authz

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/authz/casbinrule"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// superOnlyKeys are the catalog's super-only permissions, sorted.
func superOnlyKeys() []string {
	var keys []string
	for _, m := range coreModules {
		for _, p := range m.perms {
			if p.superOnly {
				keys = append(keys, string(p.key))
			}
		}
	}
	slices.Sort(keys)
	return keys
}

// The module order is the order of the role editor; audit logs have a module
// of their own (U8).
func TestCatalogModuleOrder(t *testing.T) {
	t.Parallel()
	var got []string
	for _, m := range coreModules {
		got = append(got, m.key)
		if m.label.Get("en") == "" || m.label.Get("zh") == "" {
			t.Errorf("module %s has no label", m.key)
		}
	}
	want := []string{"user", "role", "apikey", "group", "segment", "account", "proxy", "price", "balance", "redeem", "topup", "usage", "sticky",
		"plugin", "publisher", "node", "audit", "task", "gateway", "settings", "system_update"}
	if !slices.Equal(got, want) {
		t.Fatalf("modules = %v\nwant %v", got, want)
	}
	for _, d := range CorePermissions() {
		if (d.Key == "audit:read") != (d.Module == "audit") {
			t.Errorf("%s is in module %s", d.Key, d.Module)
		}
	}
}

// Every key of a built-in role's whitelist is canonical, of a catalog
// action, listed once.
func TestBuiltinWhitelistsAreCatalogKeys(t *testing.T) {
	t.Parallel()
	known := map[string]bool{}
	for _, d := range CorePermissions() {
		known[d.Key] = true
	}
	for name, keys := range map[string][]string{"operator": operatorPermissions, "supplier": supplierPermissions, "user": userRolePermissions} {
		seen := map[string]bool{}
		for _, k := range keys {
			p, err := perm.Parse(k)
			if c, _ := perm.Canonical(k); err != nil || !known[string(p.Action)] || seen[k] || string(c) != k {
				t.Errorf("%s whitelist: %s is unknown or listed twice", name, k)
			}
			seen[k] = true
		}
	}
}

// Catalog invariants: a step-up permission is one worth flagging, and a
// super-only one reaches no built-in role by default.
func TestCatalogFlagInvariants(t *testing.T) {
	t.Parallel()
	for _, m := range slices.Concat(coreModules, []coreModule{selfModule}) {
		for _, p := range m.perms {
			if p.stepUp && !p.sensitive {
				t.Errorf("%s is stepUp but not sensitive", p.key)
			}
			if !p.superOnly {
				continue
			}
			for _, r := range builtinRoles {
				if slices.Contains(r.defaultPermissions(), string(p.key)) {
					t.Errorf("%s is superOnly but in the defaults of %s", p.key, r.key)
				}
			}
		}
	}
}

// The flagged sets are pinned: sensitive covers credentials, money, trust
// roots, code execution and identity or authorization (display only);
// stepUp marks the permissions whose routes need a step-up proof
// (CONTRACTS §80.2, display only).
func TestCatalogFlaggedSets(t *testing.T) {
	t.Parallel()
	var sensitive, stepUp []string
	for _, m := range coreModules {
		for _, p := range m.perms {
			if p.sensitive {
				sensitive = append(sensitive, string(p.key))
			}
			if p.stepUp {
				stepUp = append(stepUp, string(p.key))
			}
		}
	}
	wantSensitive := []string{
		"account:credential:view", "account:delete", "account:owner:update", "account:relay",
		"balance:adjust", "balance:adjust:any", "group:manage",
		"plugin:grant:critical", "plugin:grant:db_schema_unconfined", "plugin:grant:high",
		"plugin:install", "plugin:manage", "plugin:uninstall",
		"price:manage", "proxy:manage", "publisher:manage", "redeem:manage",
		"role:assign", "role:define", "settings:manage",
		"system:update:execute", "system:update:recover",
		"task:manage", "task:run", "topup:manage", "usage:account:read", "usage:settle", "user:delete", "user:password:reset",
	}
	wantStepUp := []string{
		"account:credential:view",
		"plugin:grant:critical", "plugin:grant:db_schema_unconfined", "plugin:grant:high",
		"plugin:install", "proxy:manage", "publisher:manage", "settings:manage",
		"system:update:execute", "system:update:recover", "user:delete", "user:password:reset",
	}
	// Decision D2: what only the super admin holds by default.
	wantSuper := []string{"balance:adjust:any", "plugin:grant:critical", "plugin:grant:db_schema_unconfined", "publisher:manage", "system:update:execute", "system:update:recover"}
	slices.Sort(sensitive)
	slices.Sort(stepUp)
	if super := superOnlyKeys(); !slices.Equal(super, wantSuper) {
		t.Errorf("superOnly = %v, want %v", super, wantSuper)
	}
	if !slices.Equal(sensitive, wantSensitive) {
		t.Errorf("sensitive = %v\nwant %v", sensitive, wantSensitive)
	}
	if !slices.Equal(stepUp, wantStepUp) {
		t.Errorf("stepUp = %v, want %v", stepUp, wantStepUp)
	}
}

// A core permission's risk: superOnly ones are critical, other sensitive
// ones sensitive, the rest low; the core sync stores it with the soft
// dependencies.
func TestCatalogRisk(t *testing.T) {
	t.Parallel()
	for _, m := range slices.Concat(coreModules, []coreModule{selfModule}) {
		for _, p := range m.perms {
			want := manifest.RiskLow
			switch {
			case p.superOnly:
				want = manifest.RiskCritical
			case p.sensitive:
				want = manifest.RiskSensitive
			}
			if got := p.risk(); got != want {
				t.Errorf("%s risk = %s, want %s", p.key, got, want)
			}
		}
	}
	db := testutil.DB(t)
	newService(t, db, newMemBus(), nil)
	for key, want := range map[string]struct {
		risk     string
		requires []string
	}{
		"balance:adjust:any": {manifest.RiskCritical, []string{"balance:adjust"}},
		"role:define":        {manifest.RiskSensitive, []string{"role:read"}},
		"user:read":          {manifest.RiskLow, []string{}},
	} {
		var risk string
		var requires []string
		if err := db.SQL.QueryRowContext(context.Background(), `SELECT risk, requires FROM permissions WHERE key = $1`, key).Scan(&risk, &requires); err != nil {
			t.Fatal(err)
		}
		if risk != want.risk || !slices.Equal(requires, want.requires) {
			t.Errorf("%s stored %s %v", key, risk, requires)
		}
	}
}

// Not parallel: it extends the package catalog while it runs. A new core
// permission reaches the built-in roles accepting it by default, once, and
// every such grant to an existing role is audited; other roles do not get it.
func TestSyncCoreAuditsGrants(t *testing.T) {
	db := testutil.DB(t)
	s := newService(t, db, newMemBus(), nil)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.SQL.ExecContext(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO roles (key, name) VALUES ('custom', '{"en":"x"}')`)
	custom := roleID(t, s, "custom")

	saved := coreModules
	defer func() { coreModules = saved }()
	coreModules = slices.Clone(coreModules)
	i := coreModuleOrder["role"]
	probe := corePerm{key: "role:probe", en: "Probe", zh: "探针", sensitive: true, stepUp: true}
	coreModules[i].perms = append(slices.Clone(coreModules[i].perms), probe)

	type grant struct {
		role int64
		via  string
	}
	audited := func() map[grant]int {
		t.Helper()
		rows, err := db.SQL.QueryContext(ctx, `SELECT target_id::bigint, detail->>'via' FROM audit_logs
WHERE action = 'authz.sync.grant' AND target_type = 'role' AND user_id IS NULL AND detail->>'key' = 'role:probe'`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[grant]int{}
		for rows.Next() {
			var g grant
			if err := rows.Scan(&g.role, &g.via); err != nil {
				t.Fatal(err)
			}
			out[g]++
		}
		return out
	}

	want := map[grant]int{}
	for _, r := range builtinRoles {
		if r.accepts(probe) {
			want[grant{roleID(t, s, r.key), "default"}] = 1
		}
	}
	if len(want) == 0 {
		t.Fatal("no built-in role accepts role:probe by default")
	}
	for range 2 {
		if err := s.SyncCore(ctx); err != nil {
			t.Fatal(err)
		}
		got := audited()
		if len(got) != len(want) {
			t.Fatalf("audited = %v, want %v", got, want)
		}
		for g, n := range want {
			if got[g] != n {
				t.Fatalf("audited = %v, want %v", got, want)
			}
		}
	}
	if held := rolePermissionKeys(t, s, custom); slices.Contains(held, "role:probe") {
		t.Fatalf("a custom role got a new core permission: %v", held)
	}

	mods, err := s.ListPermissions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	only := map[string]bool{}
	for _, m := range mods {
		for _, p := range m.Permissions {
			only[p.Key] = p.StepUp
		}
	}
	if !only["role:probe"] || only["role:read"] {
		t.Fatalf("step_up: probe %v, role:read %v", only["role:probe"], only["role:read"])
	}
}

// A custom role holding a built-in role key stops the sync (decision D5): it
// is neither turned into the built-in role nor skipped, and the error names
// the row and how to rename it, with its grants and holders (casbin_rule
// names a role by key). Once renamed, the sync creates the built-in role. The first sync runs before the service starts here, as on an upgrade
// that brings the supplier role to a database with a custom one of that key.
func TestSyncCoreRefusesCustomRoleOnBuiltinKey(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	ctx := context.Background()
	const key = RoleSupplier
	if _, err := db.SQL.ExecContext(ctx, `INSERT INTO roles (key, name) VALUES ($1, '{"en":"x"}')`, key); err != nil {
		t.Fatal(err)
	}
	holder := testutil.InsertUser(t, db, "holder@x.com")
	testutil.AssignRoles(t, db, holder, key)
	if _, err := db.SQL.ExecContext(ctx, `INSERT INTO permissions (key, module, label, source) VALUES ('probe:read', 'probe', '{}', 'core')`); err != nil {
		t.Fatal(err)
	}
	testutil.Grant(t, db, key, "probe:read", "own")
	s := New(Deps{DB: db})
	id := roleID(t, s, key)
	v1, err := s.currentVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}

	err = s.Start(ctx)
	fix := casbinrule.RenameRoleSQL(id, key, "supplier_custom")
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("role id %d key %q", id, key)) || !strings.Contains(err.Error(), fix) {
		t.Fatalf("start with a custom role on a built-in key: %v", err)
	}
	var builtin bool
	if err := db.SQL.QueryRowContext(ctx, `SELECT builtin FROM roles WHERE id = $1`, id).Scan(&builtin); err != nil || builtin {
		t.Fatalf("custom role became built-in: %v, %v", builtin, err)
	}
	if v2, err := s.currentVersion(ctx); err != nil || v2 != v1 {
		t.Fatalf("a refused sync bumped the version: %d -> %d", v1, v2)
	}

	// The fix moves the version itself, so that running nodes drop what
	// they cached of the old key.
	if _, err := db.SQL.ExecContext(ctx, fix); err != nil {
		t.Fatal(err)
	}
	if v2, err := s.currentVersion(ctx); err != nil || v2 != v1+1 {
		t.Fatalf("the rename moved the version %d -> %d, %v", v1, v2, err)
	}
	if err := s.SyncCore(ctx); err != nil {
		t.Fatal(err)
	}
	var keys []string
	if err := db.SQL.QueryRowContext(ctx, `SELECT array_agg(key ORDER BY key) FROM roles WHERE key IN ('supplier', 'supplier_custom') AND builtin`).Scan(&keys); err != nil || !slices.Equal(keys, []string{key}) {
		t.Fatalf("built-in roles after the rename: %v, %v", keys, err)
	}
	if err := db.SQL.QueryRowContext(ctx, `SELECT builtin FROM roles WHERE id = $1`, id).Scan(&builtin); err != nil || builtin {
		t.Fatalf("renamed custom role: built-in %v, %v", builtin, err)
	}
	if got := testutil.GrantKeys(t, db, "supplier_custom"); !slices.Equal(got, []string{"probe:read:own"}) {
		t.Fatalf("renamed role's grants: %v", got)
	}
	if got := testutil.RoleKeys(t, db, holder); !slices.Equal(got, []string{"supplier_custom"}) {
		t.Fatalf("renamed role's holder: %v", got)
	}
}

// The upgrade to 0.1.292 (CONTRACTS §103.1) on a database a 0.1.291 sync
// left behind, rewound here from a current one: no supplier_agent, supplier
// at rev 1, no usage:account:read or account:owner:update; and as in
// production after edits, a custom role on the key supplier_agent, a held
// supplier role that lost a default and got an extra grant. The sync stops
// with the rename and changes nothing; after it, it is the one release
// behaviour: the built-in role as on a new database, supplier filled to
// rev 2 (the default an administrator removed comes back, the extra one
// stays), the new keys granted, and a supplier holder past what the agent
// role carries is one agents do not manage. A second sync changes nothing.
func TestSyncCoreUpgradeTo0292(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fresh := testutil.DB(t)
	if err := New(Deps{DB: fresh}).SyncCore(ctx); err != nil {
		t.Fatal(err)
	}
	db := testutil.DB(t)
	if err := New(Deps{DB: db}).SyncCore(ctx); err != nil {
		t.Fatal(err)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.SQL.ExecContext(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	newKeys := []string{string(perm.UsageAccountRead), string(perm.AccountOwnerUpdate)}
	agent, supplier := casbinrule.RoleSub(RoleSupplierAgent), casbinrule.RoleSub(RoleSupplier)
	exec(`DELETE FROM casbin_rule WHERE v0 = $1 OR v1 = $1 OR v1 = ANY($2)`, agent, newKeys)
	exec(`DELETE FROM roles WHERE key = $1`, RoleSupplierAgent)
	exec(`DELETE FROM permissions WHERE key = ANY($1)`, newKeys)
	exec(`UPDATE roles SET defaults_rev = 1 WHERE key = $1`, RoleSupplier)
	exec(`DELETE FROM casbin_rule WHERE ptype = 'p' AND v0 = $1 AND v1 = 'account:delete'`, supplier)
	testutil.Grant(t, db, RoleSupplier, "account:group:bind")
	exec(`INSERT INTO roles (key, name) VALUES ($1, '{"en":"agents"}')`, RoleSupplierAgent)
	testutil.Grant(t, db, RoleSupplierAgent, "account:read", "own")
	custom := testutil.InsertUser(t, db, "custom@x.com")
	testutil.AssignRoles(t, db, custom, RoleSupplierAgent)
	held := testutil.InsertUser(t, db, "supplier@x.com")
	testutil.AssignRoles(t, db, held, RoleUser, RoleSupplier)
	testutil.BumpAuthz(t, db)

	s := New(Deps{DB: db})
	id := roleID(t, s, RoleSupplierAgent)
	v1, err := s.currentVersion(ctx)
	require.NoError(t, err)
	fix := casbinrule.RenameRoleSQL(id, RoleSupplierAgent, RoleSupplierAgent+"_custom")
	err = s.Start(ctx)
	require.ErrorContains(t, err, fix)
	v2, err := s.currentVersion(ctx)
	require.NoError(t, err)
	require.Equal(t, v1, v2, "a refused sync bumped the version")
	var rev int
	require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT defaults_rev FROM roles WHERE key = $1`, RoleSupplier).Scan(&rev))
	require.Equal(t, 1, rev, "a refused sync moved the supplier on")
	var n int
	require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM permissions WHERE key = ANY($1)`, newKeys).Scan(&n))
	require.Zero(t, n, "a refused sync wrote permissions")

	exec(fix)
	s = newService(t, db, newMemBus(), nil)
	require.Equal(t, testutil.GrantKeys(t, fresh, RoleSupplierAgent), testutil.GrantKeys(t, db, RoleSupplierAgent))
	require.Equal(t, slices.Sorted(slices.Values(append(testutil.GrantKeys(t, fresh, RoleSupplier), "account:group:bind"))), testutil.GrantKeys(t, db, RoleSupplier))
	for _, role := range []string{RoleAdmin, RoleOperator} {
		require.Equal(t, testutil.GrantKeys(t, fresh, role), testutil.GrantKeys(t, db, role), role)
	}
	require.Equal(t, []string{"account:read:own"}, testutil.GrantKeys(t, db, RoleSupplierAgent+"_custom"))
	require.Equal(t, []string{RoleSupplierAgent + "_custom"}, testutil.RoleKeys(t, db, custom))
	got, _ := assignableOf(t, s, RoleAdmin)
	require.Contains(t, got, RoleSupplierAgent)

	agentUser := addUser(t, s, "agent@x.com", RoleSupplierAgent)
	setOwner(t, s, held, agentUser)
	err = s.CanActOnUsers(ctx, agentUser, core.ActOnIdentity, held)
	require.True(t, isCode(err, "permission_denied"), "an agent manages a supplier holding more than the agent role: %v", err)
	require.Equal(t, []string{"account:group:bind"}, detail(err, "missing"))

	v3, err := s.currentVersion(ctx)
	require.NoError(t, err)
	require.NoError(t, s.SyncCore(ctx))
	v4, err := s.currentVersion(ctx)
	require.NoError(t, err)
	require.Equal(t, v3, v4, "a second sync changed something")
}

// Every catalog key (core and self modules) has its perm constant and every
// constant names a catalog key, once each (T66).
func TestKeyConstantsMatchCatalog(t *testing.T) {
	t.Parallel()
	file, err := parser.ParseFile(token.NewFileSet(), "../perm/keys.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	constants := map[string]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			v := spec.(*ast.ValueSpec)
			key, err := strconv.Unquote(v.Values[0].(*ast.BasicLit).Value)
			if err != nil {
				t.Fatal(err)
			}
			if prev, dup := constants[key]; dup {
				t.Errorf("%s and %s both name %s", prev, v.Names[0].Name, key)
			}
			constants[key] = v.Names[0].Name
		}
	}
	catalog := map[string]bool{}
	for _, m := range append(slices.Clone(coreModules), selfModule) {
		for _, p := range m.perms {
			catalog[string(p.key)] = true
			if constants[string(p.key)] == "" {
				t.Errorf("%s has no perm constant", p.key)
			}
		}
	}
	for key, name := range constants {
		if !catalog[key] {
			t.Errorf("perm.%s = %q is not in the catalog", name, key)
		}
	}
}

// Every listed permission explains itself in both languages, and its soft
// dependencies are listed catalog keys: never itself or a self key, and of the same resource when both take a scope (the editor
// suggests the same scope).
func TestCatalogDescriptionsAndRequires(t *testing.T) {
	t.Parallel()
	for _, m := range slices.Concat(coreModules, []coreModule{selfModule}) {
		for _, p := range m.perms {
			if en, zh := p.desc.Get("en"), p.desc.Get("zh"); en == "" || zh == "" || en == zh {
				t.Errorf("%s description = %v", p.key, p.desc)
			}
			for _, k := range p.requires {
				_, ok := corePermByKey(string(k))
				_, self := selfKeys[string(k)]
				switch {
				case !ok || self || k == p.key:
					t.Errorf("%s requires %s, not a listed catalog key", p.key, k)
				case perm.ScopeType(p.key) != "" && perm.ScopeType(k) != "" && perm.ScopeType(p.key) != perm.ScopeType(k):
					t.Errorf("%s requires %s of another resource", p.key, k)
				}
			}
		}
	}
}

// SyncCore writes the catalog descriptions into an existing database, and
// GET /permissions returns them with the soft dependencies.
func TestSyncCoreWritesDescriptions(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	ctx := context.Background()
	s := newService(t, db, newMemBus(), nil) // Start runs the first sync.
	// A database synced by a core without descriptions.
	if _, err := db.SQL.ExecContext(ctx, `UPDATE permissions SET description = '{}' WHERE source = 'core'`); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncCore(ctx); err != nil {
		t.Fatal(err)
	}
	// Synced once, the descriptions change nothing more.
	version := func() (v int64) {
		t.Helper()
		if err := db.SQL.QueryRowContext(ctx, `SELECT version FROM authz_meta WHERE id = 1`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	before := version()
	if err := s.SyncCore(ctx); err != nil {
		t.Fatal(err)
	}
	if after := version(); after != before {
		t.Fatalf("a second sync bumped the version: %d -> %d", before, after)
	}
	mods, err := s.ListPermissions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	items := map[string]PermissionItem{}
	for _, m := range mods {
		for _, p := range m.Permissions {
			items[p.Key] = p
		}
	}
	upd, read := items["account:update"], items["account:read"]
	if !strings.Contains(upd.Description.Get("en"), "scope") || upd.Description.Get("zh") == "" {
		t.Fatalf("account:update description = %v", upd.Description)
	}
	if !slices.Equal(upd.Requires, []string{"account:read"}) || len(read.Requires) != 0 || upd.ScopeType != "account" {
		t.Fatalf("account:update = %+v, account:read requires %v", upd, read.Requires)
	}
	if usage := items["usage:all:read"].Description.Get("en"); !strings.Contains(usage, "email") {
		t.Fatalf("usage:all:read description = %q", usage)
	}
	if del := items["user:delete"]; !del.StepUp || del.Risk != manifest.RiskSensitive || del.ScopeType != "" {
		t.Fatalf("user:delete = %+v", del)
	}
	raw, err := json.Marshal(read)
	if err != nil || strings.Contains(string(raw), "requires") {
		t.Fatalf("requires is omitted when empty: %s, %v", raw, err)
	}
}

// GET /permissions lists a plugin's catalog metadata as stored: groups in
// their order, each permission's group, scope (resource without the
// "plugin:" prefix), requires and defaults expanded to full keys; a core
// permission's risk and requires come from its columns.
func TestListPermissionsPluginMeta(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	s := newService(t, db, newMemBus(), nil)
	flows, actions := lt("Flows", "流程"), lt("Actions", "处置")
	require.NoError(t, syncer(t, s, "mod")(
		core.PermissionDef{Key: "exempt", Risk: manifest.RiskCritical, Group: "action", GroupLabel: actions, GroupSort: 30},
		core.PermissionDef{Key: "flow:manage", ScopeType: "plugin:flow", Scope: &manifest.PermissionScope{Resource: "flow", Own: true, Options: "/scopes/flow"},
			Risk: manifest.RiskSensitive, Group: "flow", GroupLabel: flows, GroupSort: 10, Sort: 2, Requires: []string{"plugin.mod:flow:read"},
			Defaults: map[string][]string{RoleAdmin: {""}, RoleOperator: {"own", "flow/3"}}},
		core.PermissionDef{Key: "flow:read", Group: "flow", GroupLabel: flows, GroupSort: 10, Sort: 1}))
	mods, err := s.ListPermissions(context.Background())
	require.NoError(t, err)
	items := map[string]PermissionItem{}
	var mod *PermissionModule
	for _, m := range mods {
		if m.Module == "plugin.mod" {
			mod = m
		}
		for _, p := range m.Permissions {
			items[p.Key] = p
		}
	}
	require.NotNil(t, mod)
	assert.Equal(t, []PermissionGroup{{Key: "flow", Label: flows, Sort: 10}, {Key: "action", Label: actions, Sort: 30}}, mod.Groups)
	keys := make([]string, 0, len(mod.Permissions))
	for _, p := range mod.Permissions {
		keys = append(keys, p.Key)
	}
	assert.Equal(t, []string{"plugin.mod:flow:read", "plugin.mod:flow:manage", "plugin.mod:exempt"}, keys)

	manage := items["plugin.mod:flow:manage"]
	assert.Equal(t, manifest.RiskSensitive, manage.Risk)
	assert.Equal(t, "plugin:flow", manage.ScopeType)
	assert.Equal(t, &manifest.PermissionScope{Resource: "flow", Own: true, Options: "/scopes/flow"}, manage.Scope)
	assert.Equal(t, "flow", manage.Group)
	assert.Equal(t, []string{"plugin.mod:flow:read"}, manage.Requires)
	assert.Equal(t, map[string][]string{RoleAdmin: {"plugin.mod:flow:manage"},
		RoleOperator: {"plugin.mod:flow:manage:own", "plugin.mod:flow:manage:flow/3"}}, manage.Defaults)
	exempt := items["plugin.mod:exempt"]
	assert.Equal(t, manifest.RiskCritical, exempt.Risk)
	assert.Nil(t, exempt.Scope)
	assert.Nil(t, exempt.Defaults)

	adjust := items["balance:adjust:any"]
	assert.Equal(t, manifest.RiskCritical, adjust.Risk)
	assert.Equal(t, []string{"balance:adjust"}, adjust.Requires)
	assert.Nil(t, items["account:read"].Scope, "a core scope type carries no plugin scope")
}
