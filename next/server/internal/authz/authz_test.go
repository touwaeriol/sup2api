package authz

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/install"
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

// reconnBus is a memBus whose subscription can be re-established.
type reconnBus struct {
	*memBus
	rmu        sync.Mutex
	reconnects []func()
}

func (b *reconnBus) OnReconnect(fn func()) func() {
	b.rmu.Lock()
	b.reconnects = append(b.reconnects, fn)
	b.rmu.Unlock()
	return func() {}
}

func (b *reconnBus) reconnect() {
	b.rmu.Lock()
	fns := append([]func(){}, b.reconnects...)
	b.rmu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

type fakeGen struct {
	core.Generation
	plugins []core.PluginInfo
}

func (g fakeGen) Plugins() []core.PluginInfo { return g.plugins }

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
	if err := s.BindMenuRoutes(testMenuRoutes); err != nil {
		t.Fatal(err)
	}
	return s
}

func addUser(t *testing.T, s *Service, email string, roles ...string) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	if err := s.db.SQL.QueryRowContext(ctx, `INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	var v int64
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
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
	return setRolesAs(context.Background(), s, actor, user, roles...)
}

// setRolesAs is setRoles in ctx, which may carry an access key.
func setRolesAs(ctx context.Context, s *Service, actor, user int64, roles ...string) error {
	var v int64
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
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
	if err := s.db.SQL.QueryRowContext(context.Background(), `SELECT id FROM roles WHERE key = $1`, key).Scan(&id); err != nil {
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

// fieldCode is the code of field's error in an invalid_argument err, "" when
// it has none.
func fieldCode(err error, field string) string {
	var e *core.Error
	if !errors.As(err, &e) {
		return ""
	}
	fields, _ := e.Details["fields"].([]core.FieldError)
	for _, f := range fields {
		if f.Field == field {
			return f.Code
		}
	}
	return ""
}

// operatorDefaults pins the built-in operator role's default permissions
// (decision D3), in catalog order: users without delete or password reset,
// assigning roles without defining them, groups, user groups, accounts
// without other people's credentials or the guarded settings, proxies, and
// read access to prices, balances, usage, nodes, audit logs and tasks (which
// it may also run), next to what a user has; with the credentials of the
// accounts it created itself, it covers what a supplier has.
var operatorDefaults = []string{
	"user:read", "user:create", "user:update",
	"role:read", "role:assign",
	"apikey:self:manage",
	"group:read", "group:manage",
	"segment:read", "segment:manage",
	"account:read", "account:create", "account:update", "account:delete", "account:test",
	"account:credential:view:own",
	"proxy:read", "proxy:manage",
	"price:read",
	"balance:self:read", "balance:all:read",
	"usage:self:read", "usage:all:read", "usage:account:read",
	"node:read",
	"audit:read",
	"task:read", "task:run",
	"gateway:use",
}

// ------------------------------------------------------------ tests

func TestSyncCoreSeedsCatalogAndRoles(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	s := newService(t, db, newMemBus(), nil)
	ctx := context.Background()

	var n int
	if err := db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM permissions WHERE source = 'core' AND status = 'active'`).Scan(&n); err != nil {
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
	// The admin holds every core permission but those reserved for the
	// super admin (decision D2).
	superOnly := superOnlyKeys()
	if got := byKey[RoleAdmin].PermissionKeys; len(got) != n-len(superOnly) || slices.ContainsFunc(superOnly, func(k string) bool { return slices.Contains(got, k) }) {
		t.Fatalf("admin perms = %d of %d: %v", len(got), n, got)
	}
	if len(byKey[RoleUser].PermissionKeys) != len(userRolePermissions) {
		t.Fatalf("user perms = %v", byKey[RoleUser].PermissionKeys)
	}
	// The supplier role: its own accounts, proxies and usage and that of
	// its accounts, nothing of plugins, no group binding (decision 7), and
	// nothing to assign.
	sup := byKey[RoleSupplier]
	wantSup := []string{
		"account:create:own", "account:credential:view:own", "account:delete:own", "account:read:own", "account:test:own", "account:update:own",
		"proxy:manage:own", "proxy:read:own", "usage:account:read:own", "usage:self:read",
	}
	if sup == nil || !sup.Builtin || sup.Superuser || !slices.Equal(slices.Sorted(slices.Values(sup.PermissionKeys)), wantSup) ||
		len(sup.AssignableRoleKeys) != 0 || sup.AssignCustom {
		t.Fatalf("supplier: %+v", sup)
	}
	// The supplier agent (CONTRACTS §103): the user's and the supplier's,
	// its own reads widened to its team's accounts and account usage, and
	// its team's users (D8); suppliers and agents to assign (D2).
	agent := byKey[RoleSupplierAgent]
	wantAgent := slices.Sorted(slices.Values(append(slices.DeleteFunc(slices.Clone(wantSup), func(k string) bool {
		return k == "account:read:own" || k == "usage:account:read:own" || k == "usage:self:read"
	}), "apikey:self:manage", "balance:self:read", "usage:self:read", "gateway:use",
		"account:read:team", "usage:account:read:team", "user:read:team", "user:create:team", "user:update:team", "role:read", "role:assign")))
	if agent == nil || !agent.Builtin || agent.Superuser || !slices.Equal(slices.Sorted(slices.Values(agent.PermissionKeys)), wantAgent) || agent.AssignCustom {
		t.Fatalf("supplier agent: %+v", agent)
	}
	for role, want := range map[string][]string{
		RoleAdmin: {RoleOperator, RoleSupplier, RoleSupplierAgent, RoleUser}, RoleOperator: {RoleSupplier, RoleUser},
		RoleSupplierAgent: {RoleSupplier, RoleSupplierAgent},
	} {
		if got := byKey[role].AssignableRoleKeys; !slices.Equal(got, want) {
			t.Fatalf("%s assigns %v, want %v", role, got, want)
		}
	}
	// The operator holds exactly its whitelist (decision D3), in catalog order.
	op := byKey[RoleOperator]
	if op == nil || !op.Builtin || op.Superuser || !slices.Equal(op.PermissionKeys, operatorDefaults) {
		t.Fatalf("operator: %+v\nwant %v", op, operatorDefaults)
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
	if _, err := db.SQL.ExecContext(ctx, `INSERT INTO permissions (key, module, label, source) VALUES ('legacy:thing', 'legacy', '{"en":"x"}', 'core')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.ExecContext(ctx, `DELETE FROM permissions WHERE key = 'node:read'`); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncCore(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	_ = db.SQL.QueryRowContext(ctx, `SELECT status FROM permissions WHERE key = 'legacy:thing'`).Scan(&status)
	if status != "removed" {
		t.Fatalf("legacy status = %q", status)
	}
	admin := addUser(t, s, "a@x.com", RoleAdmin)
	mustCan(t, s, admin, "node:read", true)
	mustCan(t, s, admin, "legacy:thing", false)
	operator := addUser(t, s, "o@x.com", RoleOperator)
	mustCan(t, s, operator, "node:read", true)

	// A new settings permission reaches admin but not operator.
	if _, err := db.SQL.ExecContext(ctx, `DELETE FROM permissions WHERE key = 'settings:manage'`); err != nil {
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
	if _, err := db.SQL.ExecContext(ctx, `INSERT INTO plugins (key, name, status) VALUES ('guard', '{"en":"Guard"}', 'installed')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Tx(ctx, func(tx *gorm.DB) error {
		return s.SyncPlugin(ctx, tx, "guard", withRoles([]core.PermissionDef{{Key: "stats:read"}}, RoleOperator))
	}); err != nil {
		t.Fatal(err)
	}
	opID := roleID(t, s, RoleOperator)
	orig, err := s.GetRole(ctx, opID)
	if err != nil {
		t.Fatal(err)
	}

	// Edit: rename, drop a core permission, add a settings one.
	if _, err := s.UpdateRole(ctx, root, opID, UpdateRoleInput{Name: lt("Night shift", "夜班")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetRolePermissions(ctx, root, opID, []string{"user:read", "settings:manage", "plugin.guard:stats:read"}, nil); err != nil {
		t.Fatal(err)
	}
	mustCan(t, s, opUser, "settings:manage", true)
	mustCan(t, s, opUser, "account:update", false)

	// The operator holds less than the admin role: it cannot reset it.
	if _, err := s.ResetRole(ctx, opUser, roleID(t, s, RoleAdmin), nil); !isCode(err, "permission_denied") {
		t.Fatalf("operator resets admin: %v", err)
	}

	r, err := s.ResetRole(ctx, root, opID, nil)
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

	// Custom roles cannot be reset.
	custom, err := s.CreateRole(ctx, root, CreateRoleInput{Key: "viewer", Name: lt("Viewer", "只读")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResetRole(ctx, root, custom.ID, nil); !isCode(err, "conflict") {
		t.Fatalf("reset custom: %v", err)
	}
	if _, err := s.ResetRole(ctx, root, 999999, nil); !isCode(err, "not_found") {
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
	root := addUser(t, a, "root@x.com", RoleSuperAdmin)
	mustCan(t, a, uid, "gateway:use", true)
	mustCan(t, b, uid, "gateway:use", true)
	mustCan(t, b, uid, "user:read", false) // cached on b

	// Mutation on node a is visible on node b through the bus.
	if _, err := a.SetRolePermissions(ctx, root, roleID(t, a, RoleUser), append(append([]string{}, userRolePermissions...), "user:read"), nil); err != nil {
		t.Fatal(err)
	}
	mustCan(t, a, uid, "user:read", true)
	mustCan(t, b, uid, "user:read", true)

	// Without a broadcast the version poll task catches up.
	c := newService(t, db, nil, nil)
	var poll core.TaskSpec
	if err := c.RegisterTasks(taskRecorder(func(spec core.TaskSpec) {
		if spec.Name == VersionPollTask {
			poll = spec
		}
	})); err != nil {
		t.Fatal(err)
	}
	if poll.Name != VersionPollTask || poll.Scope != core.TaskNode || poll.RequireWork || poll.Every != 100*time.Millisecond {
		t.Fatalf("version poll spec: %+v", poll)
	}
	mustCan(t, c, uid, "user:read", true)
	if _, err := a.SetRolePermissions(ctx, root, roleID(t, a, RoleUser), userRolePermissions, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := poll.Handler(ctx, core.TaskRun{Name: poll.Name}); err != nil {
		t.Fatal(err)
	}
	mustCan(t, c, uid, "user:read", false)

	// Disabled users have no permissions.
	su := root
	mustCan(t, a, su, "anything:at:all", true)
	a.Committed(ctx, bumpWith(t, a, `UPDATE users SET status = 'disabled' WHERE id = ?`, su))
	mustCan(t, a, su, "anything:at:all", false)
}

func bump(t *testing.T, s *Service) int64 { return bumpWith(t, s, "") }

// bumpWith moves the authz version on in a transaction that also runs sql
// (with args; "" = none): a write the version records, as the service's
// own writes are. A raw write outside one leaves the caches stale at the
// same version.
func bumpWith(t *testing.T, s *Service, sql string, args ...any) int64 {
	var v int64
	err := s.db.Tx(context.Background(), func(tx *gorm.DB) error {
		var err error
		if v, err = s.Bump(context.Background(), tx); err != nil || sql == "" {
			return err
		}
		return tx.Exec(sql, args...).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestBusReconnectRefreshesVersion(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	bus := &reconnBus{memBus: newMemBus()}
	s := newService(t, db, bus, nil)
	changed := make(chan struct{}, 1)
	s.OnChange(func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})

	// A change whose broadcast this node missed while disconnected.
	var v int64
	if err := db.SQL.QueryRowContext(context.Background(), `UPDATE authz_meta SET version = version + 1 WHERE id = 1 RETURNING version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	minVersion := func() int64 {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.minVersion
	}
	if minVersion() >= v {
		t.Fatalf("minVersion %d already at %d before reconnect", minVersion(), v)
	}
	bus.reconnect()
	eventually(t, func() bool { return minVersion() == v })
	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("OnChange listener not called after reconnect")
	}
}

func TestBumpDropsStaleCache(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	a := newService(t, db, nil, nil) // no bus: a misses b's changes
	b := newService(t, db, nil, nil)
	ctx := context.Background()

	uid := addUser(t, b, "u@x.com", RoleUser)
	root := addUser(t, b, "root@x.com", RoleSuperAdmin)
	mustCan(t, a, uid, "user:read", false) // cached on a
	if _, err := b.SetRolePermissions(ctx, root, roleID(t, b, RoleUser), append(slices.Clone(userRolePermissions), "user:read"), nil); err != nil {
		t.Fatal(err)
	}
	mustCan(t, a, uid, "user:read", false) // stale on a

	// Under the authz lock a no longer trusts what it cached before.
	errRollback := errors.New("rollback")
	var got bool
	err := db.Tx(ctx, func(tx *gorm.DB) error {
		if _, err := a.Bump(ctx, tx); err != nil {
			return err
		}
		var err error
		if got, err = a.Can(ctx, uid, "user:read"); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("tx: %v", err)
	}
	if !got {
		t.Fatal("Can inside the bumping tx used the stale cache")
	}
}

func TestRoleManagement(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	s := newService(t, db, newMemBus(), nil)
	ctx := context.Background()
	root := addUser(t, s, "root@x.com", RoleSuperAdmin)
	admin := addUser(t, s, "admin@x.com", RoleAdmin)

	if err := s.DeleteRole(ctx, root, roleID(t, s, RoleAdmin)); !isCode(err, "conflict") {
		t.Fatalf("delete builtin: %v", err)
	}
	if _, err := s.SetRolePermissions(ctx, admin, roleID(t, s, RoleSuperAdmin), []string{"user:read"}, nil); !isCode(err, "conflict") {
		t.Fatalf("super_admin perms: %v", err)
	}
	if _, err := s.CreateRole(ctx, root, CreateRoleInput{Key: "ops", Name: lt("Ops", "运营"), PermissionKeys: []string{"nope:x"}}); !isCode(err, "invalid_argument") {
		t.Fatalf("unknown perm: %v", err)
	}
	if _, err := s.CreateRole(ctx, admin, CreateRoleInput{Key: "Bad Key", Name: lt("x", "x")}); !isCode(err, "invalid_argument") {
		t.Fatalf("bad key: %v", err)
	}
	// Built-in role keys are reserved, supplier included.
	for _, key := range []string{RoleSuperAdmin, RoleAdmin, RoleOperator, RoleSupplier, RoleUser} {
		_, err := s.CreateRole(ctx, root, CreateRoleInput{Key: key, Name: lt("x", "x")})
		if !isCode(err, "invalid_argument") || fieldCode(err, "key") != "reserved" {
			t.Fatalf("create role %q: %v", key, err)
		}
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

	r, err = s.UpdateRole(ctx, admin, r.ID, UpdateRoleInput{Name: lt("Operators", "运营人员")})
	if err != nil || r.Name.Get("zh") != "运营人员" || r.UserCount != 1 {
		t.Fatalf("update: %+v %v", r, err)
	}
	if err := s.DeleteRole(ctx, admin, r.ID); err != nil {
		t.Fatal(err)
	}
	mustCan(t, s, uid, "account:test", false)
	if _, err := s.GetRole(ctx, r.ID); !isCode(err, "not_found") {
		t.Fatalf("get deleted: %v", err)
	}
}

// An actor with role:define or role:assign can only hand out permissions it
// holds itself, whether by creating a role or by assigning one (to anyone,
// itself included).
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
	if err := db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM roles WHERE key = 'installer'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("installer roles = %d %v", n, err)
	}
	// ...but a role within its own permissions is fine.
	if _, err := s.CreateRole(ctx, operator, CreateRoleInput{Key: "readers", Name: lt("Readers", "只读"),
		PermissionKeys: []string{"user:read", "account:read"}}); err != nil {
		t.Fatalf("operator creates role: %v", err)
	}
	// The super admin is not limited; the admin holds every core permission
	// but those reserved for the super admin.
	if _, err := s.CreateRole(ctx, root, CreateRoleInput{Key: "installer", Name: lt("Installer", "安装"),
		PermissionKeys: []string{"plugin:install", "settings:manage"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRole(ctx, admin, CreateRoleInput{Key: "updater", Name: lt("Updater", "升级"),
		PermissionKeys: []string{"system:update:read", "price:manage"}}); err != nil {
		t.Fatal(err)
	}

	// Assigning roles: let the operator assign both custom roles, so only
	// their permissions stand in its way. It cannot give itself admin (not
	// assignable) or installer (beyond its permissions).
	letAssign(t, s, RoleOperator, "readers", "installer")
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
	// Roles within its permissions are fine for others; not for itself,
	// which it does not manage (operator is not on its list, FU6).
	if err := setRoles(s, operator, alice, RoleUser, "readers"); err != nil {
		t.Fatalf("operator assigns readers: %v", err)
	}
	mustCan(t, s, alice, "account:read", true)
	if err := setRoles(s, operator, operator, RoleOperator, "readers"); !isCode(err, "permission_denied") || detail(err, "role") != RoleOperator {
		t.Fatalf("operator self-assigns readers: %v", err)
	}
	// Once the admin gives alice "installer", she holds more than the
	// operator, which can no longer change her roles at all.
	if err := setRoles(s, admin, alice, "installer"); err != nil {
		t.Fatalf("admin assigns installer: %v", err)
	}
	mustCan(t, s, alice, "plugin:install", true)
	if err := setRoles(s, operator, alice, "installer", RoleUser); !isCode(err, "permission_denied") {
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
	// Nobody assigns super_admin.
	if err := setRoles(s, admin, bob, RoleSuperAdmin); !isCode(err, "permission_denied") {
		t.Fatalf("admin grants super_admin: %v", err)
	}
	if err := setRoles(s, 0, bob, RoleOperator); err != nil {
		t.Fatalf("system: %v", err)
	}
}

// The super admin role changes hands only by transfer: nobody, the super
// admin itself and its access keys included, assigns or revokes it, and the
// system only grants the initial one (bootstrap).
func TestSuperAdminRules(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	s := newService(t, db, newMemBus(), nil)
	ctx := context.Background()

	root := addUser(t, s, "root@x.com", RoleSuperAdmin) // as bootstrap: actor 0
	if su, err := s.IsSuperuser(ctx, db.Gorm, root); err != nil || !su {
		t.Fatalf("bootstrap super admin: %v %v", su, err)
	}
	admin := addUser(t, s, "admin@x.com", RoleAdmin)
	keyed := core.WithAccessKey(ctx, core.AccessKeyIdentity{UserID: root, Permissions: []string{"role:assign"}})
	denied := []struct {
		name        string
		ctx         context.Context
		actor, user int64
		roles       []string
	}{
		{"revoke own", ctx, root, root, []string{RoleAdmin}},
		{"grant to another", ctx, root, admin, []string{RoleSuperAdmin}},
		{"key revokes own", keyed, root, root, []string{RoleAdmin}},
		{"key grants to another", keyed, root, admin, []string{RoleSuperAdmin}},
		{"self-promotion", ctx, admin, admin, []string{RoleSuperAdmin}},
		{"system revokes", ctx, 0, root, []string{RoleAdmin}},
	}
	for _, tt := range denied {
		if err := setRolesAs(tt.ctx, s, tt.actor, tt.user, tt.roles...); !isCode(err, "permission_denied") {
			t.Errorf("%s: %v", tt.name, err)
		}
	}
	if su, err := s.IsSuperuser(ctx, db.Gorm, admin); err != nil || su {
		t.Fatalf("admin promoted: %v %v", su, err)
	}
	if err := setRoles(s, 0, admin, "nope"); !isCode(err, "invalid_argument") {
		t.Fatalf("unknown role: %v", err)
	}
	err := db.Tx(ctx, func(tx *gorm.DB) error {
		if _, err := s.Bump(ctx, tx); err != nil {
			return err
		}
		return s.EnsureNotLastSuperAdmin(ctx, tx, root)
	})
	if !isCode(err, "conflict") {
		t.Fatalf("ensure last: %v", err)
	}
}

// M6: grants are compared in the grant view, where a disabled plugin's
// permissions still count, on the actor's side and on the granted roles'.
func TestGrantViewCountsDisabledPermissions(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	s := newService(t, db, newMemBus(), nil)
	ctx := context.Background()
	if _, err := db.SQL.ExecContext(ctx, `INSERT INTO plugins (key, name, status) VALUES ('guard', '{"en":"Guard","zh":"守卫"}', 'installed')`); err != nil {
		t.Fatal(err)
	}
	root := addUser(t, s, "root@x.com", RoleSuperAdmin)
	admin := addUser(t, s, "admin@x.com", RoleAdmin)
	operator := addUser(t, s, "op@x.com", RoleOperator)
	alice := addUser(t, s, "alice@x.com", RoleUser)
	tx := func(fn func(tx *gorm.DB) error) {
		t.Helper()
		if err := db.Tx(ctx, fn); err != nil {
			t.Fatal(err)
		}
	}
	tx(func(tx *gorm.DB) error {
		return s.SyncPlugin(ctx, tx, "guard", withRoles([]core.PermissionDef{{Key: "stats:read", Label: lt("View stats", "查看统计")}}, RoleAdmin))
	})
	if _, err := s.CreateRole(ctx, root, CreateRoleInput{Key: "guardian", Name: lt("Guardian", "守卫"),
		PermissionKeys: []string{"plugin.guard:stats:read"}}); err != nil {
		t.Fatal(err)
	}
	tx(func(tx *gorm.DB) error { return s.SetPluginActive(ctx, tx, "guard", false) })
	// Both may assign guardian, so only its permission decides.
	letAssign(t, s, RoleOperator, "guardian")
	eventually(t, func() bool { ok, _ := s.Can(ctx, admin, "plugin.guard:stats:read"); return !ok })

	// The operator lacks the disabled permission and cannot hand it out.
	err := setRoles(s, operator, alice, RoleUser, "guardian")
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != "permission_denied" || !slices.Equal(ce.Details["missing"].([]string), []string{"plugin.guard:stats:read"}) {
		t.Fatalf("operator grants a disabled permission it lacks: %v", err)
	}
	// The admin's role carries it, disabled or not.
	if err := s.CanGrant(ctx, admin, []string{"plugin.guard:stats:read"}); err != nil {
		t.Fatalf("admin grants its disabled permission: %v", err)
	}
	if err := setRoles(s, admin, alice, RoleUser, "guardian"); err != nil {
		t.Fatalf("admin assigns guardian: %v", err)
	}
	// Alice now holds what the operator lacks, so it cannot act on her.
	if err := s.CanActOnUser(ctx, operator, core.ActOnIdentity, alice); !isCode(err, "permission_denied") {
		t.Fatalf("operator acts on a holder of a disabled permission: %v", err)
	}
}

// C-L4: grant checks read the database, not this node's cache, which may lag
// a change made on another node.
func TestGrantChecksIgnoreStaleCache(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	a := newService(t, db, nil, nil) // no bus: a misses b's changes
	b := newService(t, db, nil, nil)
	ctx := context.Background()

	root := addUser(t, b, "root@x.com", RoleSuperAdmin)
	granter, err := b.CreateRole(ctx, root, CreateRoleInput{Key: "granter", Name: lt("Granter", "授权"),
		PermissionKeys: []string{"user:read", "account:read"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.CreateRole(ctx, root, CreateRoleInput{Key: "reader", Name: lt("Reader", "只读"),
		PermissionKeys: []string{"account:read"}}); err != nil {
		t.Fatal(err)
	}
	letAssign(t, b, "granter", "reader")
	actor := addUser(t, b, "g@x.com", "granter")
	alice := addUser(t, b, "alice@x.com")
	mustCan(t, a, actor, "account:read", true) // cached on a
	if _, err := b.SetRolePermissions(ctx, root, granter.ID, []string{"user:read"}, nil); err != nil {
		t.Fatal(err)
	}
	mustCan(t, a, actor, "account:read", true) // stale on a

	if err := a.CanGrant(ctx, actor, []string{"account:read"}); !isCode(err, "permission_denied") {
		t.Fatalf("CanGrant used the stale cache: %v", err)
	}
	var ce *core.Error
	if err := setRoles(a, actor, alice, "reader"); !errors.As(err, &ce) || !slices.Equal(ce.Details["missing"].([]string), []string{"account:read"}) {
		t.Fatalf("SetUserRoles used the stale cache: %v", err)
	}
}

func TestAuditHelper(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	s := newService(t, db, nil, nil)
	ctx := context.Background()
	root := addUser(t, s, "root@x.com", RoleSuperAdmin)
	if err := s.audit(ctx, db.Gorm, root, "role.test", "role", int64(42), map[string]any{"k": "v"}); err != nil {
		t.Fatal(err)
	}
	var uid int64
	var target, detail string
	if err := db.SQL.QueryRowContext(ctx, `SELECT user_id, target_id, detail::text FROM audit_logs WHERE action = 'role.test' AND target_type = 'role'`).Scan(&uid, &target, &detail); err != nil {
		t.Fatal(err)
	}
	if uid != root || target != "42" || detail != `{"k": "v"}` {
		t.Fatalf("audit row: user %d target %q detail %s", uid, target, detail)
	}
}

func TestPluginCatalog(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	s := newService(t, db, newMemBus(), nil)
	ctx := context.Background()
	if _, err := db.SQL.ExecContext(ctx, `INSERT INTO plugins (key, name, status) VALUES ('guard', '{"en":"Guard","zh":"守卫"}', 'installed')`); err != nil {
		t.Fatal(err)
	}
	admin := addUser(t, s, "a@x.com", RoleAdmin)
	user := addUser(t, s, "u@x.com", RoleUser)
	tx := func(fn func(tx *gorm.DB) error) {
		t.Helper()
		if err := db.Tx(ctx, fn); err != nil {
			t.Fatal(err)
		}
	}
	defs := []core.PermissionDef{
		{Key: "stats:read", Label: lt("View stats", "查看统计")},
		{Key: "rules:manage", Label: lt("Manage rules", "管理规则"), Risk: manifest.RiskSensitive},
	}
	// First install: the plugin is not running, so its keys start disabled
	// and enabling turns them on.
	tx(func(tx *gorm.DB) error { return s.SyncPlugin(ctx, tx, "guard", withRoles(defs, RoleAdmin)) })
	if got := permissionStatus(t, db, "plugin.guard:stats:read"); got != "disabled" {
		t.Fatalf("permission of an installed plugin: %s", got)
	}
	mustCan(t, s, admin, "plugin.guard:stats:read", false)
	tx(func(tx *gorm.DB) error {
		if _, err := store.SQLTx(tx).ExecContext(ctx, `UPDATE plugins SET status = 'enabled' WHERE key = 'guard'`); err != nil {
			return err
		}
		return s.SetPluginActive(ctx, tx, "guard", true)
	})
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
	tx(func(tx *gorm.DB) error { return s.SetPluginActive(ctx, tx, "guard", false) })
	// Both may assign guardian, so only its permission decides.
	letAssign(t, s, RoleOperator, "guardian")
	eventually(t, func() bool { ok, _ := s.Can(ctx, admin, "plugin.guard:stats:read"); return !ok })
	tx(func(tx *gorm.DB) error { return s.SetPluginActive(ctx, tx, "guard", true) })
	eventually(t, func() bool { ok, _ := s.Can(ctx, admin, "plugin.guard:stats:read"); return ok })

	// Upgrade drops rules:manage, adds audit:read (granted to user only).
	defs = []core.PermissionDef{{Key: "stats:read", Label: lt("View stats", "查看统计")}, {Key: "plugin.guard:audit:read"}}
	tx(func(tx *gorm.DB) error { return s.SyncPlugin(ctx, tx, "guard", withRoles(defs, RoleUser)) })
	eventually(t, func() bool { ok, _ := s.Can(ctx, user, "plugin.guard:audit:read"); return ok })
	mustCan(t, s, admin, "plugin.guard:stats:read", true) // kept grant
	mustCan(t, s, admin, "plugin.guard:audit:read", false)
	if got := permissionStatus(t, db, "plugin.guard:rules:manage"); got != "removed" {
		t.Fatalf("undeclared permission: %s", got)
	}

	tx(func(tx *gorm.DB) error { return s.DeletePlugin(ctx, tx, "guard") })
	eventually(t, func() bool { ok, _ := s.Can(ctx, admin, "plugin.guard:stats:read"); return !ok })
}

func permissionStatus(t *testing.T, db *store.DB, key string) string {
	t.Helper()
	var status string
	if err := db.SQL.QueryRowContext(context.Background(), `SELECT status FROM permissions WHERE key = $1`, key).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

// A new plugin permission is active only while the plugin runs; a row which
// already exists keeps its status. The statuses come from the install package
// so the SQL list in SyncPlugin cannot drift from them.
func TestSyncPluginNewPermissionFollowsPluginStatus(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	s := newService(t, db, newMemBus(), nil)
	ctx := context.Background()
	running := []string{install.StatusEnabled, install.StatusUpgrading}
	sync := func(key string, defs ...core.PermissionDef) {
		t.Helper()
		if err := db.Tx(ctx, func(tx *gorm.DB) error { return s.SyncPlugin(ctx, tx, key, defs) }); err != nil {
			t.Fatal(err)
		}
	}
	for i, st := range []string{install.StatusAwaitingConsent, install.StatusInstalled, install.StatusEnabling,
		install.StatusEnabled, install.StatusUpgrading, install.StatusDisabled} {
		key := "st" + string(rune('a'+i))
		if _, err := db.SQL.ExecContext(ctx, `INSERT INTO plugins (key, name, status) VALUES ($1, '{"en":"P"}', $2)`, key, st); err != nil {
			t.Fatal(err)
		}
		sync(key, core.PermissionDef{Key: "a:read"})
		want := "disabled"
		if slices.Contains(running, st) {
			want = "active"
		}
		if got := permissionStatus(t, db, manifest.PermissionKey(key, "a:read")); got != want {
			t.Errorf("plugin status %s: new permission %s, want %s", st, got, want)
		}
	}

	// A version approved while the plugin is disabled: the new key waits for
	// the next enable, the existing one is left as it is.
	if _, err := db.SQL.ExecContext(ctx, `INSERT INTO plugins (key, name, status) VALUES ('guard', '{"en":"Guard"}', 'enabled')`); err != nil {
		t.Fatal(err)
	}
	sync("guard", core.PermissionDef{Key: "a:read"})
	if _, err := db.SQL.ExecContext(ctx, `UPDATE plugins SET status = 'disabled' WHERE key = 'guard'`); err != nil {
		t.Fatal(err)
	}
	sync("guard", core.PermissionDef{Key: "a:read"}, core.PermissionDef{Key: "b:read"})
	if a, b := permissionStatus(t, db, "plugin.guard:a:read"), permissionStatus(t, db, "plugin.guard:b:read"); a != "active" || b != "disabled" {
		t.Fatalf("sync while disabled: existing %s, new %s", a, b)
	}
	// An upgrade of a running plugin: the new key works at once.
	if _, err := db.SQL.ExecContext(ctx, `UPDATE plugins SET status = 'upgrading' WHERE key = 'guard'`); err != nil {
		t.Fatal(err)
	}
	sync("guard", core.PermissionDef{Key: "a:read"}, core.PermissionDef{Key: "b:read"}, core.PermissionDef{Key: "c:read"})
	if b, c := permissionStatus(t, db, "plugin.guard:b:read"), permissionStatus(t, db, "plugin.guard:c:read"); b != "disabled" || c != "active" {
		t.Fatalf("sync while upgrading: existing %s, new %s", b, c)
	}
}

// SyncPlugin stores the sort it is given (the manifest order install.
// PermissionDefs numbers from 10) and corrects rows synced before.
func TestSyncPluginSort(t *testing.T) {
	t.Parallel()
	db := testutil.DB(t)
	s := newService(t, db, newMemBus(), nil)
	ctx := context.Background()
	if _, err := db.SQL.ExecContext(ctx, `INSERT INTO plugins (key, name, status) VALUES ('guard', '{"en":"Guard"}', 'enabled')`); err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{Publisher: manifest.OfficialPublisher, Key: "guard", UserPermissions: []manifest.UserPermission{{Key: "stats:read"}, {Key: "rules:manage"}}}
	sorts := func() string {
		t.Helper()
		var got string
		if err := db.SQL.QueryRowContext(ctx, `SELECT string_agg(key || '=' || sort, ',' ORDER BY sort, key) FROM permissions WHERE plugin_key = 'guard'`).Scan(&got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	// Rows as the old numbering left them: the first permission at 0 was
	// taken for unset and given 10, the second kept its 1.
	if err := db.Tx(ctx, func(tx *gorm.DB) error {
		return s.SyncPlugin(ctx, tx, "guard", []core.PermissionDef{{Key: "stats:read", Sort: 10}, {Key: "rules:manage", Sort: 1}})
	}); err != nil {
		t.Fatal(err)
	}
	if got := sorts(); got != "plugin.guard:rules:manage=1,plugin.guard:stats:read=10" {
		t.Fatalf("old numbering: %s", got)
	}
	if err := db.Tx(ctx, func(tx *gorm.DB) error { return s.SyncPlugin(ctx, tx, "guard", install.PermissionDefs(m, nil)) }); err != nil {
		t.Fatal(err)
	}
	if got := sorts(); got != "plugin.guard:stats:read=10,plugin.guard:rules:manage=20" {
		t.Fatalf("after the next sync: %s", got)
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

// TestOwnershipCatalog pins the CONTRACTS §21.1 keys: the scopable actions
// with their resource, labels in both languages, credential views and
// deletion are sensitive, the user role is unchanged and the account/proxy
// menus accept any scope.
func TestOwnershipCatalog(t *testing.T) {
	t.Parallel()
	defs := map[string]core.PermissionDef{}
	for _, d := range CorePermissions() {
		defs[d.Key] = d
	}
	want := map[string]bool{ // key -> sensitive
		"account:read": false, "account:create": false, "account:update": false,
		"account:delete": true, "account:test": false, "account:credential:view": true,
		"account:settings:custom": false, "proxy:read": false, "proxy:manage": true,
	}
	for key, sensitive := range want {
		d, ok := defs[key]
		if !ok {
			t.Fatalf("%s missing from catalog", key)
		}
		if got := d.Risk != manifest.RiskLow; got != sensitive {
			t.Fatalf("%s risk = %s, want sensitive %v", key, d.Risk, sensitive)
		}
		if d.Label.Get("en") == "" || d.Label.Get("zh") == "" || d.Label.Get("en") == d.Label.Get("zh") {
			t.Fatalf("%s label = %v", key, d.Label)
		}
		mod := strings.SplitN(key, ":", 2)[0]
		if d.Module != mod {
			t.Fatalf("%s module = %s", key, d.Module)
		}
		if scopable := key != "account:settings:custom"; scopable != (d.ScopeType == mod) || !scopable && d.ScopeType != "" {
			t.Fatalf("%s scope type = %q", key, d.ScopeType)
		}
	}
	if join(userRolePermissions) != "apikey:self:manage,balance:self:read,usage:self:read,gateway:use" {
		t.Fatalf("user role changed: %v", userRolePermissions)
	}
	menuKeys := map[string]string{}
	for _, sec := range coreMenus {
		for _, it := range sec.items {
			menuKeys[it.id] = join(it.apis)
		}
	}
	if menuKeys["accounts"] != "GET /accounts" || menuKeys["proxies"] != "GET /proxies" {
		t.Fatalf("menus: %v", menuKeys)
	}
	set := core.PermissionSetOf("proxy:read:own")
	if !hasAny(set, []string{"proxy:read"}) || hasAny(set, []string{"proxy:manage"}) || set.Has("proxy:read") {
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
func (taskRecorder) EnqueueTx(context.Context, *gorm.DB, string, any, core.EnqueueOpts) error {
	return nil
}

// withRoles grants every def to roles, every row.
func withRoles(defs []core.PermissionDef, roles ...string) []core.PermissionDef {
	out := slices.Clone(defs)
	for i := range out {
		out[i].GrantRoles = unscoped(roles...)
	}
	return out
}

// unscoped is GrantRoles granting roles every row.
func unscoped(roles ...string) map[string][]string {
	out := make(map[string][]string, len(roles))
	for _, r := range roles {
		out[r] = []string{""}
	}
	return out
}

// scopeRows creates the groups, accounts and proxies the scopes of keys
// name, with those ids (checkScopeRefs refuses a grant naming a row that
// does not exist).
func scopeRows(t *testing.T, db *store.DB, keys ...string) {
	t.Helper()
	insert := map[string]string{
		core.ScopeRefGroup:   `INSERT INTO groups (id, name) VALUES ($1::bigint, 'g' || $1::bigint) ON CONFLICT DO NOTHING`,
		core.ScopeRefAccount: `INSERT INTO accounts (id, name, plugin_key, type, credentials_enc) VALUES ($1::bigint, 'a' || $1::bigint, 'anthropic', 'apikey', '\x00') ON CONFLICT DO NOTHING`,
		core.ScopeRefProxy:   `INSERT INTO proxies (id, name, protocol, host, port) VALUES ($1::bigint, 'p' || $1::bigint, 'http', 'proxy.example', 3128) ON CONFLICT DO NOTHING`,
	}
	for _, k := range keys {
		p, err := perm.Parse(k)
		if err != nil {
			t.Fatal(k, err)
		}
		for kind, id := range scopeRefsOf(p) {
			if _, err := db.SQL.Exec(insert[kind], id); err != nil {
				t.Fatal(kind, id, err)
			}
		}
	}
}
