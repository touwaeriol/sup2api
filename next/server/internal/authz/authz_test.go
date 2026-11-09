package authz

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// ------------------------------------------------------------ fakes

type memBus struct {
	mu   sync.Mutex
	subs map[string][]func([]byte)
}

func newMemBus() *memBus { return &memBus{subs: map[string][]func([]byte){}} }

func (b *memBus) Publish(_ context.Context, ch string, payload []byte) error {
	b.mu.Lock()
	subs := append([]func([]byte){}, b.subs[ch]...)
	b.mu.Unlock()
	for _, fn := range subs {
		fn(payload)
	}
	return nil
}

func (b *memBus) Subscribe(ch string, fn func([]byte)) func() {
	b.mu.Lock()
	b.subs[ch] = append(b.subs[ch], fn)
	b.mu.Unlock()
	return func() {}
}

type fakeGen struct {
	core.Generation
	plugins []core.PluginInfo
}

func (g fakeGen) Plugins() []core.PluginInfo { return g.plugins }

func (g fakeGen) Plugin(key string) (core.PluginInfo, bool) {
	for _, p := range g.plugins {
		if p.Key == key {
			return p, true
		}
	}
	return core.PluginInfo{}, false
}

type fakeRegistry struct{ gen core.Generation }

func (r fakeRegistry) Current() core.Generation                       { return r.gen }
func (r fakeRegistry) OnChange(func(core.Generation)) (cancel func()) { return func() {} }

// ------------------------------------------------------------ helpers

func newService(t *testing.T, db *store.DB, bus core.Bus, reg core.PluginRegistry) *Service {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := New(Deps{DB: db, Bus: bus, Plugins: reg, PollInterval: 100 * time.Millisecond})
	if err := s.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	return s
}

func addUser(t *testing.T, s *Service, email string, roles ...string) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	if err := s.db.Pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	var v int64
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		v, err = s.SetUserRoles(ctx, tx, 0, id, roles)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Committed(ctx, v)
	return id
}

func setRoles(s *Service, actor, user int64, roles ...string) error {
	ctx := context.Background()
	var v int64
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		v, err = s.SetUserRoles(ctx, tx, actor, user, roles)
		return err
	})
	if err == nil {
		s.Committed(ctx, v)
	}
	return err
}

func roleID(t *testing.T, s *Service, key string) int64 {
	t.Helper()
	var id int64
	if err := s.db.Pool.QueryRow(context.Background(), `SELECT id FROM roles WHERE key = $1`, key).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func mustCan(t *testing.T, s *Service, uid int64, perm string, want bool) {
	t.Helper()
	got, err := s.Can(context.Background(), uid, perm)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Can(%d, %q) = %v, want %v", uid, perm, got, want)
	}
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func isCode(err error, code string) bool {
	var e *core.Error
	return errors.As(err, &e) && e.Code == code
}

// ------------------------------------------------------------ tests

func TestSyncCoreSeedsCatalogAndRoles(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	s := newService(t, db, newMemBus(), nil)
	ctx := context.Background()

	var n int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM permissions WHERE source = 'core' AND status = 'active'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != len(CorePermissions()) {
		t.Fatalf("core permissions = %d, want %d", n, len(CorePermissions()))
	}
	roles, err := s.ListRoles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]*Role{}
	for _, r := range roles {
		byKey[r.Key] = r
	}
	if !byKey[RoleSuperAdmin].Superuser || !byKey[RoleSuperAdmin].Builtin {
		t.Fatalf("super_admin: %+v", byKey[RoleSuperAdmin])
	}
	if len(byKey[RoleAdmin].PermissionKeys) != n {
		t.Fatalf("admin perms = %d, want %d", len(byKey[RoleAdmin].PermissionKeys), n)
	}
	if len(byKey[RoleUser].PermissionKeys) != len(userRolePermissions) {
		t.Fatalf("user perms = %v", byKey[RoleUser].PermissionKeys)
	}
	op := byKey[RoleOperator]
	if op == nil || !op.Builtin || op.Superuser || len(op.PermissionKeys) == 0 || len(op.PermissionKeys) >= n {
		t.Fatalf("operator: %+v", op)
	}
	for _, k := range op.PermissionKeys {
		if strings.HasPrefix(k, "settings:") || strings.HasPrefix(k, "sticky:") || strings.HasPrefix(k, "plugin:") || strings.HasPrefix(k, "publisher:") {
			t.Fatalf("operator has %s", k)
		}
	}
	if !slices.Contains(op.PermissionKeys, "account:update") || !slices.Contains(op.PermissionKeys, "system:update:execute") {
		t.Fatalf("operator perms = %v", op.PermissionKeys)
	}

	// Idempotent: a second sync does not bump the version.
	v1, _ := s.currentVersion(ctx)
	if err := s.SyncCore(ctx); err != nil {
		t.Fatal(err)
	}
	v2, _ := s.currentVersion(ctx)
	if v1 != v2 {
		t.Fatalf("version bumped on no-op sync: %d -> %d", v1, v2)
	}

	// A core permission dropped from code is marked removed; a new one is
	// granted to admin.
	if _, err := db.Pool.Exec(ctx, `INSERT INTO permissions (key, module, label, source) VALUES ('legacy:thing', 'legacy', '{"en":"x"}', 'core')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM permissions WHERE key = 'node:read'`); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncCore(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	_ = db.Pool.QueryRow(ctx, `SELECT status FROM permissions WHERE key = 'legacy:thing'`).Scan(&status)
	if status != "removed" {
		t.Fatalf("legacy status = %q", status)
	}
	admin := addUser(t, s, "a@x.com", RoleAdmin)
	mustCan(t, s, admin, "node:read", true)
	mustCan(t, s, admin, "legacy:thing", false)
	operator := addUser(t, s, "o@x.com", RoleOperator)
	mustCan(t, s, operator, "node:read", true)

	// A new settings permission reaches admin but not operator.
	if _, err := db.Pool.Exec(ctx, `DELETE FROM permissions WHERE key = 'settings:manage'`); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncCore(ctx); err != nil {
		t.Fatal(err)
	}
	mustCan(t, s, admin, "settings:manage", true)
	mustCan(t, s, operator, "settings:manage", false)
}

func TestResetBuiltinRole(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	s := newService(t, db, newMemBus(), nil)
	ctx := context.Background()
	root := addUser(t, s, "root@x.com", RoleSuperAdmin)
	opUser := addUser(t, s, "o@x.com", RoleOperator)
	if _, err := db.Pool.Exec(ctx, `INSERT INTO plugins (key, name, status) VALUES ('guard', '{"en":"Guard"}', 'installed')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Tx(ctx, func(tx pgx.Tx) error {
		return s.SyncPlugin(ctx, tx, "guard", []core.PermissionDef{{Key: "stats:read"}}, []string{RoleOperator})
	}); err != nil {
		t.Fatal(err)
	}
	opID := roleID(t, s, RoleOperator)
	orig, err := s.GetRole(ctx, opID)
	if err != nil {
		t.Fatal(err)
	}

	// Edit: rename, drop a core permission, add a settings one.
	if _, err := s.UpdateRole(ctx, opID, UpdateRoleInput{Name: lt("Night shift", "夜班")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetRolePermissions(ctx, root, opID, []string{"user:read", "settings:manage", "plugin.guard:stats:read"}); err != nil {
		t.Fatal(err)
	}
	mustCan(t, s, opUser, "settings:manage", true)
	mustCan(t, s, opUser, "account:update", false)

	// The operator itself lacks settings and plugin permissions: it cannot
	// reset the admin role to defaults that include them.
	if _, err := s.ResetRole(ctx, opUser, roleID(t, s, RoleAdmin)); !isCode(err, "permission_denied") {
		t.Fatalf("operator resets admin: %v", err)
	}

	r, err := s.ResetRole(ctx, root, opID)
	if err != nil {
		t.Fatal(err)
	}
	want := orig.PermissionKeys
	if r.Name.Get("zh") != "运维" || strings.Join(r.PermissionKeys, ",") != strings.Join(want, ",") {
		t.Fatalf("reset: name=%v perms=%v want %v", r.Name, r.PermissionKeys, want)
	}
	if !slices.Contains(r.PermissionKeys, "plugin.guard:stats:read") {
		t.Fatalf("plugin permission dropped: %v", r.PermissionKeys)
	}
	mustCan(t, s, opUser, "settings:manage", false)
	mustCan(t, s, opUser, "account:update", true)

	// super_admin: only name/description; custom roles cannot be reset.
	suID := roleID(t, s, RoleSuperAdmin)
	if _, err := s.UpdateRole(ctx, suID, UpdateRoleInput{Name: lt("Root", "根")}); err != nil {
		t.Fatal(err)
	}
	if r, err := s.ResetRole(ctx, root, suID); err != nil || r.Name.Get("zh") != "超级管理员" || !r.Superuser {
		t.Fatalf("reset super_admin: %+v %v", r, err)
	}
	custom, err := s.CreateRole(ctx, root, CreateRoleInput{Key: "viewer", Name: lt("Viewer", "只读")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResetRole(ctx, root, custom.ID); !isCode(err, "conflict") {
		t.Fatalf("reset custom: %v", err)
	}
	if _, err := s.ResetRole(ctx, root, 999999); !isCode(err, "not_found") {
		t.Fatalf("reset missing: %v", err)
	}
}

func TestAuthorizerCacheInvalidation(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	bus := newMemBus()
	a := newService(t, db, bus, nil)
	b := newService(t, db, bus, nil)
	ctx := context.Background()

	uid := addUser(t, a, "u@x.com", RoleUser)
	admin := addUser(t, a, "admin@x.com", RoleAdmin)
	mustCan(t, a, uid, "gateway:use", true)
	mustCan(t, b, uid, "gateway:use", true)
	mustCan(t, b, uid, "user:read", false) // cached on b

	// Mutation on node a is visible on node b through the bus.
	if _, err := a.SetRolePermissions(ctx, admin, roleID(t, a, RoleUser), append(append([]string{}, userRolePermissions...), "user:read")); err != nil {
		t.Fatal(err)
	}
	mustCan(t, a, uid, "user:read", true)
	mustCan(t, b, uid, "user:read", true)

	// Without a broadcast the version poll task catches up.
	c := newService(t, db, nil, nil)
	var poll core.TaskSpec
	if err := c.RegisterTasks(taskRecorder(func(spec core.TaskSpec) { poll = spec })); err != nil {
		t.Fatal(err)
	}
	if poll.Name != VersionPollTask || poll.Scope != core.TaskNode || poll.RequireWork || poll.Every != 100*time.Millisecond {
		t.Fatalf("version poll spec: %+v", poll)
	}
	mustCan(t, c, uid, "user:read", true)
	if _, err := a.SetRolePermissions(ctx, admin, roleID(t, a, RoleUser), userRolePermissions); err != nil {
		t.Fatal(err)
	}
	if _, err := poll.Handler(ctx, core.TaskRun{Name: poll.Name}); err != nil {
		t.Fatal(err)
	}
	mustCan(t, c, uid, "user:read", false)

	// Disabled users have no permissions.
	su := addUser(t, a, "s@x.com", RoleSuperAdmin)
	mustCan(t, a, su, "anything:at:all", true)
	v := bump(t, a)
	if _, err := db.Pool.Exec(ctx, `UPDATE users SET status = 'disabled' WHERE id = $1`, su); err != nil {
		t.Fatal(err)
	}
	a.Committed(ctx, v)
	mustCan(t, a, su, "anything:at:all", false)
}

func bump(t *testing.T, s *Service) int64 {
	var v int64
	err := s.db.Tx(context.Background(), func(tx pgx.Tx) error {
		var err error
		v, err = s.Bump(context.Background(), tx)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestRoleManagement(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	s := newService(t, db, newMemBus(), nil)
	ctx := context.Background()
	root := addUser(t, s, "root@x.com", RoleSuperAdmin)
	admin := addUser(t, s, "admin@x.com", RoleAdmin)

	if err := s.DeleteRole(ctx, roleID(t, s, RoleAdmin)); !isCode(err, "conflict") {
		t.Fatalf("delete builtin: %v", err)
	}
	if _, err := s.SetRolePermissions(ctx, admin, roleID(t, s, RoleSuperAdmin), []string{"user:read"}); !isCode(err, "conflict") {
		t.Fatalf("super_admin perms: %v", err)
	}
	if _, err := s.CreateRole(ctx, root, CreateRoleInput{Key: "ops", Name: lt("Ops", "运营"), PermissionKeys: []string{"nope:x"}}); !isCode(err, "invalid_argument") {
		t.Fatalf("unknown perm: %v", err)
	}
	if _, err := s.CreateRole(ctx, admin, CreateRoleInput{Key: "Bad Key", Name: lt("x", "x")}); !isCode(err, "invalid_argument") {
		t.Fatalf("bad key: %v", err)
	}
	r, err := s.CreateRole(ctx, admin, CreateRoleInput{Key: "ops", Name: lt("Ops", "运营"), PermissionKeys: []string{"account:read", "account:test"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRole(ctx, admin, CreateRoleInput{Key: "ops", Name: lt("Ops", "运营")}); !isCode(err, "conflict") {
		t.Fatalf("dup key: %v", err)
	}
	uid := addUser(t, s, "o@x.com", "ops")
	mustCan(t, s, uid, "account:test", true)
	mustCan(t, s, uid, "account:delete", false)

	r, err = s.UpdateRole(ctx, r.ID, UpdateRoleInput{Name: lt("Operators", "运营人员")})
	if err != nil || r.Name.Get("zh") != "运营人员" || r.UserCount != 1 {
		t.Fatalf("update: %+v %v", r, err)
	}
	if err := s.DeleteRole(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	mustCan(t, s, uid, "account:test", false)
	if _, err := s.GetRole(ctx, r.ID); !isCode(err, "not_found") {
		t.Fatalf("get deleted: %v", err)
	}
}

// An actor with role:manage can only hand out permissions it holds itself,
// whether by creating a role or by assigning one (to anyone, itself included).
func TestRoleGrantEscalation(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	s := newService(t, db, newMemBus(), nil)
	ctx := context.Background()
	root := addUser(t, s, "root@x.com", RoleSuperAdmin)
	admin := addUser(t, s, "admin@x.com", RoleAdmin)
	operator := addUser(t, s, "op@x.com", RoleOperator)
	alice := addUser(t, s, "alice@x.com", RoleUser)
	mustCan(t, s, operator, "plugin:install", false)

	// Creating a role: the operator cannot put permissions it lacks on it.
	_, err := s.CreateRole(ctx, operator, CreateRoleInput{Key: "installer", Name: lt("Installer", "安装"),
		PermissionKeys: []string{"user:read", "plugin:install", "settings:manage"}})
	if !isCode(err, "permission_denied") {
		t.Fatalf("operator creates escalating role: %v", err)
	}
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Details == nil {
		t.Fatalf("details missing: %+v", err)
	}
	missing, _ := ce.Details["missing"].([]string)
	if strings.Join(missing, ",") != "plugin:install,settings:manage" {
		t.Fatalf("missing = %v", ce.Details["missing"])
	}
	if _, err := s.GetRole(ctx, 0); !isCode(err, "not_found") {
		t.Fatalf("role must not be created: %v", err)
	}
	var n int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM roles WHERE key = 'installer'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("installer roles = %d %v", n, err)
	}
	// ...but a role within its own permissions is fine.
	if _, err := s.CreateRole(ctx, operator, CreateRoleInput{Key: "readers", Name: lt("Readers", "只读"),
		PermissionKeys: []string{"user:read", "account:read"}}); err != nil {
		t.Fatalf("operator creates role: %v", err)
	}
	// The super admin is not limited; the admin holds every core permission.
	if _, err := s.CreateRole(ctx, root, CreateRoleInput{Key: "installer", Name: lt("Installer", "安装"),
		PermissionKeys: []string{"plugin:install", "settings:manage", "system:update:execute"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRole(ctx, admin, CreateRoleInput{Key: "updater", Name: lt("Updater", "升级"),
		PermissionKeys: []string{"system:update:execute"}}); err != nil {
		t.Fatal(err)
	}

	// Assigning roles: the operator cannot give itself admin or installer.
	if err := setRoles(s, operator, operator, RoleOperator, RoleAdmin); !isCode(err, "permission_denied") {
		t.Fatalf("operator self-assigns admin: %v", err)
	}
	if err := setRoles(s, operator, operator, RoleOperator, "installer"); !isCode(err, "permission_denied") {
		t.Fatalf("operator self-assigns installer: %v", err)
	}
	mustCan(t, s, operator, "plugin:install", false)
	// Nor give them to someone else.
	if err := setRoles(s, operator, alice, "installer"); !isCode(err, "permission_denied") {
		t.Fatalf("operator assigns installer: %v", err)
	}
	mustCan(t, s, alice, "plugin:install", false)
	// Roles within its permissions are fine, for itself and for others.
	if err := setRoles(s, operator, alice, RoleUser, "readers"); err != nil {
		t.Fatalf("operator assigns readers: %v", err)
	}
	mustCan(t, s, alice, "account:read", true)
	if err := setRoles(s, operator, operator, RoleOperator, "readers"); err != nil {
		t.Fatalf("operator self-assigns readers: %v", err)
	}
	// Re-saving a role the user already holds is not a grant: the admin
	// gives alice "installer", and the operator (who cannot grant it) can
	// still keep it while changing the rest.
	if err := setRoles(s, admin, alice, "installer"); err != nil {
		t.Fatalf("admin assigns installer: %v", err)
	}
	mustCan(t, s, alice, "plugin:install", true)
	if err := setRoles(s, operator, alice, "installer"); !isCode(err, "permission_denied") {
		t.Fatalf("operator acts on a higher user: %v", err)
	}
	// Admin can assign operator; the super admin can assign anything.
	bob := addUser(t, s, "bob@x.com", RoleUser)
	if err := setRoles(s, admin, bob, RoleOperator); err != nil {
		t.Fatalf("admin assigns operator: %v", err)
	}
	mustCan(t, s, bob, "account:update", true)
	if err := setRoles(s, root, bob, RoleAdmin, "installer"); err != nil {
		t.Fatalf("root assigns: %v", err)
	}
	mustCan(t, s, bob, "plugin:install", true)
	// Only a super admin may grant super_admin, even to an admin.
	if err := setRoles(s, admin, bob, RoleSuperAdmin); !isCode(err, "permission_denied") {
		t.Fatalf("admin grants super_admin: %v", err)
	}
	if err := setRoles(s, 0, bob, RoleOperator); err != nil {
		t.Fatalf("system: %v", err)
	}
}

func TestSuperAdminRules(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	s := newService(t, db, newMemBus(), nil)

	root := addUser(t, s, "root@x.com", RoleSuperAdmin)
	admin := addUser(t, s, "admin@x.com", RoleAdmin)

	// Cannot revoke own super_admin, nor remove the last one.
	if err := setRoles(s, root, root, RoleAdmin); !isCode(err, "conflict") {
		t.Fatalf("self revoke: %v", err)
	}
	if err := setRoles(s, 0, root, RoleAdmin); !isCode(err, "conflict") {
		t.Fatalf("last super admin: %v", err)
	}
	// A non-superuser cannot grant super_admin.
	if err := setRoles(s, admin, admin, RoleSuperAdmin); !isCode(err, "permission_denied") {
		t.Fatalf("escalation: %v", err)
	}
	// Root can promote admin; then root can be demoted by the new super admin.
	if err := setRoles(s, root, admin, RoleSuperAdmin); err != nil {
		t.Fatal(err)
	}
	if err := setRoles(s, admin, root, RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := setRoles(s, 0, admin, "nope"); !isCode(err, "invalid_argument") {
		t.Fatalf("unknown role: %v", err)
	}
	ctx := context.Background()
	err := db.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := s.Bump(ctx, tx); err != nil {
			return err
		}
		return s.EnsureNotLastSuperAdmin(ctx, tx, admin)
	})
	if !isCode(err, "conflict") {
		t.Fatalf("ensure last: %v", err)
	}
}

func TestPluginCatalog(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	s := newService(t, db, newMemBus(), nil)
	ctx := context.Background()
	if _, err := db.Pool.Exec(ctx, `INSERT INTO plugins (key, name, status) VALUES ('guard', '{"en":"Guard","zh":"守卫"}', 'installed')`); err != nil {
		t.Fatal(err)
	}
	admin := addUser(t, s, "a@x.com", RoleAdmin)
	user := addUser(t, s, "u@x.com", RoleUser)
	tx := func(fn func(tx pgx.Tx) error) {
		t.Helper()
		if err := db.Tx(ctx, fn); err != nil {
			t.Fatal(err)
		}
	}
	defs := []core.PermissionDef{
		{Key: "stats:read", Label: lt("View stats", "查看统计")},
		{Key: "rules:manage", Label: lt("Manage rules", "管理规则"), Sensitive: true},
	}
	tx(func(tx pgx.Tx) error { return s.SyncPlugin(ctx, tx, "guard", defs, []string{RoleAdmin}) })
	eventually(t, func() bool { ok, _ := s.Can(ctx, admin, "plugin.guard:stats:read"); return ok })
	mustCan(t, s, user, "plugin.guard:stats:read", false)

	mods, err := s.ListPermissions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	last := mods[len(mods)-1]
	if mods[0].Module != "user" || last.Module != "plugin.guard" || last.Label.Get("zh") != "守卫" || len(last.Permissions) != 2 || *last.PluginKey != "guard" {
		t.Fatalf("modules: first=%s last=%+v", mods[0].Module, last)
	}

	// Disable -> permission treated as absent; enable restores it.
	tx(func(tx pgx.Tx) error { return s.SetPluginActive(ctx, tx, "guard", false) })
	eventually(t, func() bool { ok, _ := s.Can(ctx, admin, "plugin.guard:stats:read"); return !ok })
	tx(func(tx pgx.Tx) error { return s.SetPluginActive(ctx, tx, "guard", true) })
	eventually(t, func() bool { ok, _ := s.Can(ctx, admin, "plugin.guard:stats:read"); return ok })

	// Upgrade drops rules:manage, adds audit:read (granted to user only).
	defs = []core.PermissionDef{{Key: "stats:read", Label: lt("View stats", "查看统计")}, {Key: "plugin.guard:audit:read"}}
	tx(func(tx pgx.Tx) error { return s.SyncPlugin(ctx, tx, "guard", defs, []string{RoleUser}) })
	eventually(t, func() bool { ok, _ := s.Can(ctx, user, "plugin.guard:audit:read"); return ok })
	mustCan(t, s, admin, "plugin.guard:stats:read", true) // kept grant
	mustCan(t, s, admin, "plugin.guard:audit:read", false)
	var n int
	_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM permissions WHERE key = 'plugin.guard:rules:manage'`).Scan(&n)
	if n != 0 {
		t.Fatal("undeclared permission not deleted")
	}

	tx(func(tx pgx.Tx) error { return s.DeletePlugin(ctx, tx, "guard") })
	eventually(t, func() bool { ok, _ := s.Can(ctx, admin, "plugin.guard:stats:read"); return !ok })
}

func TestMenus(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	plugins := []core.PluginInfo{{
		Key: "guard",
		Manifest: &manifest.Manifest{UI: &manifest.UI{Menus: []manifest.Menu{
			{ID: "dash", Section: "plugins", Label: manifest.LocalizedText{"en": "Guard", "zh": "请求守卫"}, Page: "dashboard", Permission: "stats:read", Order: 2},
			{ID: "public", Section: "plugins", Label: manifest.LocalizedText{"en": "Info"}, Page: "info", Order: 1},
		}}},
	}, {
		// A plugin with its own sidebar section (order 350: between security
		// and finance), an item joining a core section and one joining
		// security.
		Key: "mod",
		Manifest: &manifest.Manifest{UI: &manifest.UI{
			Sections: []manifest.MenuSection{{ID: "safety", Label: manifest.LocalizedText{"en": "Safety", "zh": "安全"}, Order: 350}},
			Menus: []manifest.Menu{
				{ID: "dash", Section: "safety", Label: manifest.LocalizedText{"en": "Moderation"}, Page: "dashboard", Permission: "mod:read"},
				{ID: "rules", Section: "gateway", Label: manifest.LocalizedText{"en": "Mod rules"}, Page: "rules", Permission: "mod:read"},
				{ID: "audit", Section: "security", Label: manifest.LocalizedText{"en": "Mod audit"}, Page: "audit", Permission: "mod:read"},
				{ID: "weights", Section: "system", Label: manifest.LocalizedText{"en": "Mod weights"}, Page: "weights", Permission: "mod:read"},
			}}},
	}}
	// The registry is read on every call, so the test swaps generations.
	reg := &fakeRegistry{gen: fakeGen{plugins: plugins}}
	s := newService(t, db, newMemBus(), reg)
	ctx := context.Background()
	if _, err := db.Pool.Exec(ctx, `INSERT INTO plugins (key, name, status) VALUES ('guard', '{"en":"Guard"}', 'enabled'), ('mod', '{"en":"Mod"}', 'enabled')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Tx(ctx, func(tx pgx.Tx) error {
		if err := s.SyncPlugin(ctx, tx, "guard", []core.PermissionDef{{Key: "stats:read"}}, []string{RoleAdmin}); err != nil {
			return err
		}
		return s.SyncPlugin(ctx, tx, "mod", []core.PermissionDef{{Key: "mod:read"}}, []string{RoleAdmin})
	}); err != nil {
		t.Fatal(err)
	}

	user := addUser(t, s, "u@x.com", RoleUser)
	menus, err := s.Menus(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	var sections []string
	for _, m := range menus {
		sections = append(sections, m.Section)
	}
	// The server orders plugins (600) before personal (800); the console
	// shows overview and personal first and the other sections after them
	// (CONTRACTS §90).
	if got := join(sections); got != "overview,plugins,me" {
		t.Fatalf("user sections = %s", got)
	}
	// The console of a user: its own keys, models and usage, no site-wide usage.
	if got := menuPaths(menus[0]); got != "/dashboard,/me/api-keys,/me/models,/me/usage/overview,/me/usage" {
		t.Fatalf("user console = %s", got)
	}
	if got := menuPaths(menus[2]); got != "/me/wallet,/me/profile" {
		t.Fatalf("user personal = %s", got)
	}
	if p := menus[1].Items; len(p) != 1 || p[0].Path != "/p/guard/info" || p[0].PluginKey != "guard" {
		t.Fatalf("user plugin menus = %+v", p)
	}

	admin := addUser(t, s, "a@x.com", RoleAdmin)
	eventually(t, func() bool { ok, _ := s.Can(ctx, admin, "plugin.guard:stats:read"); return ok })
	menus, err = s.Menus(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	sections = nil
	for _, m := range menus {
		sections = append(sections, m.Section)
	}
	if got := join(sections); got != "overview,gateway,security,mod:safety,finance,users,plugins,system,me" {
		t.Fatalf("admin sections = %s", got)
	}
	byKey := map[string]MenuSection{}
	for _, m := range menus {
		byKey[m.Section] = m
	}
	// Plugin management first, then the plugin menus by order.
	if got := menuPaths(byKey["plugins"]); got != "/plugins,/market,/publishers,/p/guard/info,/p/guard/dashboard" {
		t.Fatalf("admin plugin menus = %s", got)
	}
	if p := byKey["plugins"].Items; p[4].Label.Get("zh") != "请求守卫" {
		t.Fatalf("admin plugin menus = %+v", p)
	}
	if sec := byKey["mod:safety"]; sec.Label.Get("zh") != "安全" || len(sec.Items) != 1 || sec.Items[0].Path != "/p/mod/dashboard" {
		t.Fatalf("plugin section = %+v", sec)
	}
	if sec := byKey["security"]; sec.Label.Get("zh") != "安全" || len(sec.Items) != 1 || sec.Items[0].Path != "/p/mod/audit" {
		t.Fatalf("security section = %+v", sec)
	}
	// Site-wide usage sits in the console beside the caller's own.
	if got := menuPaths(byKey["overview"]); got != "/dashboard,/me/api-keys,/me/models,/me/usage/overview,/me/usage,/usage/overview,/usage" {
		t.Fatalf("admin console = %s", got)
	}
	// No /system/ccgateway: the ccgateway plugin is not in the generation.
	if got := menuPaths(byKey["gateway"]); got != "/accounts,/groups,/platforms,/proxies,/prices,/p/mod/rules" {
		t.Fatalf("core section with plugin item = %s", got)
	}
	if sec := byKey["users"]; menuPaths(sec) != "/users,/roles" || sec.Label.Get("zh") != "用户" {
		t.Fatalf("users = %+v", sec)
	}
	// Operations pages, then system settings as the last core item; a plugin
	// that still declares section "system" comes after it.
	if sec := byKey["system"]; menuPaths(sec) != "/nodes,/system/tasks,/system/upgrades,/system/audit,/settings,/p/mod/weights" || sec.Label.Get("zh") != "系统" {
		t.Fatalf("system = %+v", sec)
	}
	if f := byKey["finance"].Items; len(f) != 1 || f[0].Path != "/ledger" {
		t.Fatalf("finance = %+v", f)
	}

	// A caller who only manages its own accounts sees them with platforms and
	// proxies in the gateway section, and nothing of system.
	role, err := s.CreateRole(ctx, admin, CreateRoleInput{Key: "vendor", Name: core.LocalizedText{"en": "Vendor"},
		PermissionKeys: []string{"account:own:read", "proxy:own:read", "proxy:own:manage"}})
	if err != nil {
		t.Fatal(err)
	}
	vendor := addUser(t, s, "v@x.com", role.Key)
	menus, err = s.Menus(ctx, vendor)
	if err != nil {
		t.Fatal(err)
	}
	byKey = map[string]MenuSection{}
	for _, m := range menus {
		byKey[m.Section] = m
	}
	if got := menuPaths(byKey["gateway"]) + " | " + menuPaths(byKey["system"]); got != "/accounts,/platforms,/proxies | " {
		t.Fatalf("vendor menus = %s", got)
	}

	// Sticky sessions alone open the settings page and nothing else of system.
	role, err = s.CreateRole(ctx, admin, CreateRoleInput{Key: "sticky", Name: core.LocalizedText{"en": "Sticky"}, PermissionKeys: []string{"sticky:read"}})
	if err != nil {
		t.Fatal(err)
	}
	sticky := addUser(t, s, "st@x.com", role.Key)
	if got := sectionPaths(t, s, sticky, "system"); got != "/settings" {
		t.Fatalf("sticky-only system = %s", got)
	}

	// The CCGateway page needs both the enabled plugin and settings:read.
	reg.gen = fakeGen{plugins: append(plugins, core.PluginInfo{Key: "ccgateway"})}
	if got := sectionPaths(t, s, admin, "gateway"); got != "/accounts,/groups,/platforms,/proxies,/prices,/system/ccgateway,/p/mod/rules" {
		t.Fatalf("gateway with ccgateway enabled = %s", got)
	}
	if got := sectionPaths(t, s, vendor, "gateway"); got != "/accounts,/platforms,/proxies" {
		t.Fatalf("gateway without settings:read = %s", got)
	}
	// No generation is no enabled plugin, not "every plugin".
	reg.gen = nil
	if got := sectionPaths(t, s, admin, "gateway"); got != "/accounts,/groups,/platforms,/proxies,/prices" {
		t.Fatalf("gateway without a generation = %s", got)
	}
}

// sectionPaths lists the paths of one section of the user's sidebar ("" when
// the section is not returned).
func sectionPaths(t *testing.T, s *Service, userID int64, section string) string {
	t.Helper()
	menus, err := s.Menus(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range menus {
		if m.Section == section {
			return menuPaths(m)
		}
	}
	return ""
}

func menuPaths(sec MenuSection) string {
	paths := make([]string, len(sec.Items))
	for i, it := range sec.Items {
		paths[i] = it.Path
	}
	return join(paths)
}

func join(s []string) string {
	out := ""
	for i, x := range s {
		if i > 0 {
			out += ","
		}
		out += x
	}
	return out
}

// TestOwnershipCatalog pins the CONTRACTS §21.1 keys: labels in both
// languages, only the credential views are sensitive, the user role is
// unchanged and the account/proxy menus accept the own keys.
func TestOwnershipCatalog(t *testing.T) {
	t.Parallel()
	defs := map[string]core.PermissionDef{}
	for _, d := range CorePermissions() {
		defs[d.Key] = d
	}
	want := map[string]bool{ // key -> sensitive
		"account:own:read": false, "account:own:create": false, "account:own:update": false,
		"account:own:delete": false, "account:own:test": false, "account:own:credential:view": true,
		"account:settings:custom": false, "proxy:own:read": false, "proxy:own:manage": false,
	}
	for key, sensitive := range want {
		d, ok := defs[key]
		if !ok {
			t.Fatalf("%s missing from catalog", key)
		}
		if d.Sensitive != sensitive {
			t.Fatalf("%s sensitive = %v, want %v", key, d.Sensitive, sensitive)
		}
		if d.Label.Get("en") == "" || d.Label.Get("zh") == "" || d.Label.Get("en") == d.Label.Get("zh") {
			t.Fatalf("%s label = %v", key, d.Label)
		}
		if mod := strings.SplitN(key, ":", 2)[0]; d.Module != mod {
			t.Fatalf("%s module = %s", key, d.Module)
		}
	}
	if join(userRolePermissions) != "apikey:self:manage,balance:self:read,usage:self:read,gateway:use" {
		t.Fatalf("user role changed: %v", userRolePermissions)
	}
	menuKeys := map[string]string{}
	for _, sec := range coreMenus {
		for _, it := range sec.items {
			menuKeys[it.id] = join(it.anyOf)
		}
	}
	if menuKeys["accounts"] != "account:read,account:own:read,account:own:create" ||
		menuKeys["platforms"] != "account:read,account:own:read,account:own:create" ||
		menuKeys["proxies"] != "proxy:read,proxy:own:read,proxy:own:manage" {
		t.Fatalf("menus: %v", menuKeys)
	}
	set := core.PermissionSet{Keys: map[string]struct{}{"proxy:own:manage": {}}}
	if !hasAny(set, []string{"proxy:read", "proxy:own:read", "proxy:own:manage"}) || hasAny(set, []string{"proxy:read"}) {
		t.Fatal("hasAny")
	}
}

// The core sidebar addresses icons by the same names a manifest does, but from
// Go string literals that nothing validated. A wrong name there fails exactly
// as silently as it used to in a manifest (CONTRACTS §26.8), and the icon set
// is now readable from Go, so hold the core's own menus to it too - including
// the "puzzle" fallback pluginMenus() uses for a menu with no icon.
func TestCoreMenuIconsExist(t *testing.T) {
	t.Parallel()
	for _, sec := range coreMenus {
		for _, it := range sec.items {
			if !check.KnownIcon(it.icon) {
				t.Errorf("core menu %q uses icon %q, which is not in %s", it.id, it.icon, check.IconSource())
			}
		}
	}
	if !check.KnownIcon("puzzle") {
		t.Error(`the fallback icon "puzzle" is not in the icon set`)
	}
}

// The core sidebar sections come from the SDK's list (which manifest
// validation also reads); coreMenus only adds their labels and items, so it
// must name the same sections in the same order.
func TestCoreMenuSectionsMatchManifest(t *testing.T) {
	t.Parallel()
	var got, want []string
	for _, sec := range coreMenus {
		got = append(got, sec.id)
	}
	last := 0
	for _, sec := range manifest.CoreMenuSections() {
		want = append(want, sec.ID)
		if sec.Order <= last {
			t.Errorf("core section %q order %d is not after %d", sec.ID, sec.Order, last)
		}
		last = sec.Order
	}
	if !slices.Equal(got, want) {
		t.Fatalf("coreMenus sections %v, manifest.CoreMenuSections %v", got, want)
	}
}

// taskRecorder is a core.Tasks that hands each registered spec to fn.
type taskRecorder func(core.TaskSpec)

func (f taskRecorder) Register(spec core.TaskSpec) error                          { f(spec); return nil }
func (taskRecorder) Trigger(context.Context, string) error                        { return nil }
func (taskRecorder) Enqueue(context.Context, string, any, core.EnqueueOpts) error { return nil }
func (taskRecorder) EnqueueTx(context.Context, pgx.Tx, string, any, core.EnqueueOpts) error {
	return nil
}
