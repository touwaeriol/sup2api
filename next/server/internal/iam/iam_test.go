package iam

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/secret"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/authz"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/config"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/iam/tokenstore"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// ------------------------------------------------------------ fakes & setup

type memBus struct {
	mu   sync.Mutex
	subs map[string][]func([]byte)
}

func (b *memBus) Publish(_ context.Context, ch string, p []byte) error {
	b.mu.Lock()
	subs := append([]func([]byte){}, b.subs[ch]...)
	b.mu.Unlock()
	for _, fn := range subs {
		fn(p)
	}
	return nil
}

func (b *memBus) Subscribe(ch string, fn func([]byte)) func() {
	b.mu.Lock()
	if b.subs == nil {
		b.subs = map[string][]func([]byte){}
	}
	b.subs[ch] = append(b.subs[ch], fn)
	b.mu.Unlock()
	return func() {}
}

type fakeEvents struct {
	mu     sync.Mutex
	events []core.Event
}

func (f *fakeEvents) Emit(_ context.Context, tx *gorm.DB, events ...core.Event) error {
	if tx == nil {
		return errors.New("Emit needs a transaction")
	}
	f.mu.Lock()
	f.events = append(f.events, events...)
	f.mu.Unlock()
	return nil
}

func (f *fakeEvents) types() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, e := range f.events {
		out = append(out, e.Type)
	}
	return out
}

// fakeOwners wraps authz's SetOwner (tested in authz): it records the
// calls and fails with err when set, else answers as guard does.
type fakeOwners struct {
	guard core.OwnerGuard
	mu    sync.Mutex
	calls []ownerCall
	err   error
}

type ownerCall struct {
	actor, user int64
	create      bool
	owner       *int64
}

func (f *fakeOwners) SetOwner(ctx context.Context, tx *gorm.DB, actorID, userID int64, create bool, owner *int64) (int64, error) {
	if tx == nil {
		return 0, errors.New("SetOwner needs a transaction")
	}
	f.mu.Lock()
	f.calls = append(f.calls, ownerCall{actorID, userID, create, owner})
	err := f.err
	f.mu.Unlock()
	if err != nil {
		return 0, err
	}
	return f.guard.SetOwner(ctx, tx, actorID, userID, create, owner)
}

type env struct {
	svc    *Service
	bus    *memBus
	authz  *authz.Service
	owners *fakeOwners
	redis  *miniredis.Miniredis
	events *fakeEvents
	cfg    *config.Config
}

const adminEmail, adminPassword = "root@example.com", "root-password-1"

const testIP = "192.0.2.1"

// setup is the service on a fresh database with its authz service.
func setup(t *testing.T) *env {
	t.Helper()
	db := testutil.DB(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	bus := &memBus{}
	az := authz.New(authz.Deps{DB: db, Bus: bus})
	if err := az.Start(ctx); err != nil {
		t.Fatal(err)
	}
	mr := miniredis.RunT(t)
	cfg := &config.Config{
		SessionIdleTTL:         7 * 24 * time.Hour,
		SessionMaxTTL:          30 * 24 * time.Hour,
		BootstrapAdminEmail:    adminEmail,
		BootstrapAdminPassword: adminPassword,
	}
	ev := &fakeEvents{}
	cipher, err := secret.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	owners := &fakeOwners{guard: az}
	svc := New(Deps{DB: db, Redis: redis.NewClient(&redis.Options{Addr: mr.Addr()}), Config: cfg, Events: ev, Authz: az, Owners: owners, Bus: bus, Cipher: cipher, BcryptCost: bcrypt.MinCost})
	return &env{svc: svc, bus: bus, authz: az, owners: owners, redis: mr, events: ev, cfg: cfg}
}

func isCode(err error, code string) bool {
	var e *core.Error
	return errors.As(err, &e) && e.Code == code
}

// mustLogin signs in: a session token, or the second step of an account
// with two-factor.
func mustLogin(t *testing.T, s *Service, email, password string) *LoginResult {
	t.Helper()
	p, err := s.Login(context.Background(), email, password, testIP)
	if err != nil {
		t.Fatalf("login %s: %v", email, err)
	}
	if !p.TwoFactorRequired && !strings.HasPrefix(p.AccessToken, tokenstore.SessionPrefix) {
		t.Fatalf("login %s gave %+v", email, p)
	}
	return p
}

// ------------------------------------------------------------ tests

func TestBootstrapLoginLogout(t *testing.T) {
	t.Parallel()
	e := setup(t)
	s, ctx := e.svc, context.Background()

	// Without credentials nothing is created.
	e.cfg.BootstrapAdminPassword = ""
	if err := s.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	if _, total, _ := s.ListUsers(ctx, ListUsersFilter{Scope: core.AllScope, Page: 1, PageSize: 20}); total != 0 {
		t.Fatalf("users = %d", total)
	}
	e.cfg.BootstrapAdminPassword = adminPassword
	if err := s.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Bootstrap(ctx); err != nil { // idempotent
		t.Fatal(err)
	}
	if _, total, _ := s.ListUsers(ctx, ListUsersFilter{Scope: core.AllScope, Page: 1, PageSize: 20}); total != 1 {
		t.Fatalf("users after bootstrap = %d", total)
	}

	if _, err := s.Login(ctx, adminEmail, "wrong-password", testIP); !isCode(err, "unauthenticated") {
		t.Fatalf("bad password: %v", err)
	}
	if _, err := s.Login(ctx, "nobody@example.com", adminPassword, testIP); !isCode(err, "unauthenticated") {
		t.Fatalf("unknown user: %v", err)
	}
	p := mustLogin(t, s, "ROOT@example.com ", adminPassword)
	if p.ExpiresIn != int64(e.cfg.SessionIdleTTL/time.Second) || p.User.Email != adminEmail || p.User.Roles[0] != authz.RoleSuperAdmin || p.User.LastLoginAt == nil {
		t.Fatalf("result: %+v user %+v", p, p.User)
	}
	uid, _, err := s.VerifySession(ctx, p.AccessToken)
	if err != nil || uid != p.User.ID {
		t.Fatalf("verify: %d %v", uid, err)
	}
	if _, _, err := s.VerifySession(ctx, p.AccessToken+"x"); err == nil {
		t.Fatal("tampered token accepted")
	}
	me, err := s.GetMe(ctx, uid)
	if err != nil || !me.Superuser || len(me.Permissions) != len(authz.CorePermissions())+len(authz.SelfPermissionKeys()) {
		t.Fatalf("me: %+v %v", me, err)
	}

	// Logging out ends that session.
	_, id, err := s.VerifySession(ctx, p.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Logout(core.WithSession(ctx, id), uid); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.VerifySession(ctx, p.AccessToken); !isCode(err, "unauthenticated") {
		t.Fatalf("session after logout: %v", err)
	}

	// A password change ends the sessions (the route verifies the caller).
	p3 := mustLogin(t, s, adminEmail, adminPassword)
	if err := s.ChangePassword(ctx, uid, "", "short"); !isCode(err, "invalid_argument") {
		t.Fatalf("short password: %v", err)
	}
	if err := s.ChangePassword(ctx, uid, "", "new-password-1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.VerifySession(ctx, p3.AccessToken); err == nil {
		t.Fatal("session survived password change")
	}
	mustLogin(t, s, adminEmail, "new-password-1")
}

func TestUserLifecycle(t *testing.T) {
	t.Parallel()
	e := setup(t)
	s, ctx := e.svc, context.Background()
	if err := s.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	root := mustLogin(t, s, adminEmail, adminPassword).User.ID

	if _, err := s.CreateUser(ctx, root, CreateUserInput{Email: "bad", Password: "x"}); !isCode(err, "invalid_argument") {
		t.Fatalf("invalid input: %v", err)
	}
	u, err := s.CreateUser(ctx, root, CreateUserInput{Email: "Alice@Example.com", DisplayName: "Alice", Password: "password-1"})
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "alice@example.com" || len(u.Roles) != 1 || u.Roles[0] != authz.RoleUser || u.Status != StatusActive {
		t.Fatalf("created: %+v", u)
	}
	if ok, _ := e.authz.Can(ctx, u.ID, "gateway:use"); !ok {
		t.Fatal("default role not effective")
	}
	if _, err := s.CreateUser(ctx, root, CreateUserInput{Email: "alice@example.com", Password: "password-1"}); !isCode(err, "conflict") {
		t.Fatalf("duplicate: %v", err)
	}
	admin, err := s.CreateUser(ctx, root, CreateUserInput{Email: "admin@example.com", Password: "password-1", RoleKeys: []string{authz.RoleAdmin}})
	if err != nil {
		t.Fatal(err)
	}

	list, total, err := s.ListUsers(ctx, ListUsersFilter{Scope: core.AllScope, Query: "ALICE", Page: 1, PageSize: 20})
	if err != nil || total != 1 || list[0].ID != u.ID {
		t.Fatalf("search: %d %v", total, err)
	}
	if _, total, _ = s.ListUsers(ctx, ListUsersFilter{Scope: core.AllScope, Role: authz.RoleAdmin, Page: 1, PageSize: 20}); total != 1 {
		t.Fatalf("role filter total = %d", total)
	}
	if list, total, _ = s.ListUsers(ctx, ListUsersFilter{Scope: core.AllScope, Page: 2, PageSize: 2}); total != 3 || len(list) != 1 {
		t.Fatalf("paging: total=%d len=%d", total, len(list))
	}

	// Disable: login and access tokens stop working.
	result := mustLogin(t, s, "alice@example.com", "password-1")
	disabled := StatusDisabled
	if u, err = s.UpdateUser(ctx, root, u.ID, core.AllScope, UpdateUserInput{Status: &disabled}); err != nil || u.Status != StatusDisabled {
		t.Fatalf("disable: %+v %v", u, err)
	}
	if _, err := s.Login(ctx, "alice@example.com", "password-1", testIP); !isCode(err, "permission_denied") {
		t.Fatalf("login disabled: %v", err)
	}
	if _, _, err := s.VerifySession(ctx, result.AccessToken); err == nil {
		t.Fatal("disabled user's token accepted")
	}
	if ok, _ := e.authz.Can(ctx, u.ID, "gateway:use"); ok {
		t.Fatal("disabled user keeps permissions")
	}
	active := StatusActive
	name := "Alice B"
	if u, err = s.UpdateUser(ctx, root, u.ID, core.AllScope, UpdateUserInput{Status: &active, DisplayName: &name}); err != nil || u.DisplayName != "Alice B" {
		t.Fatalf("enable: %+v %v", u, err)
	}
	mustLogin(t, s, "alice@example.com", "password-1")

	// Super admin protections.
	if _, err := s.UpdateUser(ctx, root, root, core.AllScope, UpdateUserInput{Status: &disabled}); !isCode(err, "conflict") {
		t.Fatalf("self disable: %v", err)
	}
	if err := s.DeleteUser(ctx, root, root); !isCode(err, "conflict") {
		t.Fatalf("self delete: %v", err)
	}
	if err := s.DeleteUser(ctx, admin.ID, root); !isCode(err, "permission_denied") {
		t.Fatalf("admin deletes root: %v", err)
	}
	pw := "hijacked-password"
	if _, err := s.UpdateUser(ctx, admin.ID, root, core.AllScope, UpdateUserInput{Password: &pw}); !isCode(err, "permission_denied") {
		t.Fatalf("admin resets root password: %v", err)
	}
	if _, err := s.SetUserRoles(ctx, admin.ID, admin.ID, []string{authz.RoleSuperAdmin}); !isCode(err, "permission_denied") {
		t.Fatalf("admin self-promotion: %v", err)
	}
	if _, err := s.SetUserRoles(ctx, root, root, []string{authz.RoleAdmin}); !isCode(err, "permission_denied") {
		t.Fatalf("revoke own super admin: %v", err)
	}
	if err := s.DeleteUser(ctx, 0, root); !isCode(err, "conflict") {
		t.Fatalf("delete last super admin: %v", err)
	}
	if _, err := s.SetUserRoles(ctx, root, u.ID, []string{authz.RoleUser, authz.RoleAdmin}); err != nil {
		t.Fatal(err)
	}

	// Soft delete frees the email.
	if err := s.DeleteUser(ctx, root, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetUser(ctx, u.ID); !isCode(err, "not_found") {
		t.Fatalf("get deleted: %v", err)
	}
	// HostService.GetUsers: deleted and unknown ids are left out, the rest
	// come once each in the order asked.
	if found, err := s.LookupUsers(ctx, []int64{admin.ID, u.ID, 999999, root, admin.ID}); err != nil || len(found) != 2 ||
		found[0] != (core.UserSummary{ID: admin.ID, DisplayName: admin.DisplayName, Status: StatusActive}) || found[1].ID != root {
		t.Fatalf("lookup: %v %v", found, err)
	}
	if _, err := s.CreateUser(ctx, root, CreateUserInput{Email: "alice@example.com", Password: "password-1"}); err != nil {
		t.Fatalf("recreate: %v", err)
	}

	got := e.events.types()
	want := map[string]int{"user.created": 4, "user.updated": 4}
	counts := map[string]int{}
	for _, g := range got {
		counts[g]++
	}
	for k, n := range want {
		if counts[k] != n {
			t.Fatalf("events %v: %s = %d, want %d", got, k, counts[k], n)
		}
	}
}

// ------------------------------------------------------------ HTTP

type client struct {
	t      *testing.T
	engine *gin.Engine
}

func (c client) do(method, path, token string, body any) (int, map[string]any) {
	c.t.Helper()
	return c.doProof(method, path, token, "", body)
}

// proof verifies for scope and operation with the fields of how (method,
// password, code...) and returns the proof.
func (c client) proof(token, scope, operation string, how map[string]any) string {
	c.t.Helper()
	body := map[string]any{"scope": scope, "context": operation}
	maps.Copy(body, how)
	code, out := c.do("POST", "/me/verify", token, body)
	if code != 200 {
		c.t.Fatalf("verify %s: %d %v", scope, code, out)
	}
	return out["data"].(map[string]any)["proof"].(string)
}

// doProof is do with a step-up proof (none when empty).
func (c client) doProof(method, path, token, proof string, body any) (int, map[string]any) {
	c.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, "/api/v1"+path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", "zh-CN")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if proof != "" {
		req.Header.Set(core.HeaderVerifyProof, proof)
	}
	w := httptest.NewRecorder()
	c.engine.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func errCode(out map[string]any) string {
	e, _ := out["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

func TestHTTP(t *testing.T) {
	t.Parallel()
	e := setup(t)
	ctx := context.Background()
	if err := e.svc.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	r := httpapi.NewRouter(engine, e.svc, e.authz)
	e.svc.RegisterRoutes(r)
	e.authz.RegisterRoutes(r)
	// The other modules' routes are not mounted here: every menu stands on a
	// key the super admin holds (app binds the real route table).
	if err := e.authz.BindMenuRoutes(func(string) ([]string, bool) { return []string{"user:read"}, true }); err != nil {
		t.Fatal(err)
	}
	c := client{t, engine}

	code, out := c.do("POST", "/auth/login", "", map[string]any{"email": adminEmail, "password": adminPassword})
	if code != 200 {
		t.Fatalf("login: %d %v", code, out)
	}
	data := out["data"].(map[string]any)
	tok := data["access_token"].(string)

	if code, out = c.do("GET", "/me", "", nil); code != 401 {
		t.Fatalf("me without token: %d", code)
	}
	if code, out = c.do("GET", "/me", tok, nil); code != 200 || out["data"].(map[string]any)["superuser"] != true {
		t.Fatalf("me: %d %v", code, out)
	}
	// A superuser sees every core section that has items. security has no
	// core item (only plugins join it, CONTRACTS §90) and empty sections are
	// dropped; finance has the balance ledger.
	if code, out = c.do("GET", "/me/menus", tok, nil); code != 200 || sectionKeys(out["data"]) != "overview,gateway,finance,topup,users,plugins,system,me" {
		t.Fatalf("menus: %d %v", code, out)
	}
	if code, out = c.do("GET", "/permissions", tok, nil); code != 200 {
		t.Fatalf("permissions: %d %v", code, out)
	}

	// Password confirmation is gone (CONTRACTS §3.3): the endpoint no longer
	// exists and choosing roles at creation needs only the permissions.
	if code, _ = c.do("POST", "/auth/step-up", tok, map[string]any{"password": adminPassword}); code != 404 {
		t.Fatalf("step-up endpoint still mounted: %d", code)
	}
	newUser := map[string]any{"email": "ops@example.com", "password": "password-1", "role_keys": []string{"admin"}}
	if code, out = c.do("POST", "/users", tok, newUser); code != 201 {
		t.Fatalf("create: %d %v", code, out)
	}
	opsID := int64(out["data"].(map[string]any)["id"].(float64))
	if _, ok := out["data"].(map[string]any)["balance"]; !ok {
		t.Fatalf("superuser should see balance: %v", out)
	}

	// Plain user: no admin access, own menu only.
	if code, out = c.do("POST", "/users", tok, map[string]any{"email": "joe@example.com", "password": "password-1"}); code != 201 {
		t.Fatalf("create default: %d %v", code, out)
	}
	joeID := int64(out["data"].(map[string]any)["id"].(float64))
	_, out = c.do("POST", "/auth/login", "", map[string]any{"email": "joe@example.com", "password": "password-1"})
	joe := out["data"].(map[string]any)["access_token"].(string)
	if code, out = c.do("GET", "/users", joe, nil); code != 403 || errCode(out) != "permission_denied" {
		t.Fatalf("joe lists users: %d %v", code, out)
	}
	if code, out = c.do("GET", "/me/menus", joe, nil); code != 200 || len(out["data"].([]any)) != 2 {
		t.Fatalf("joe menus: %d %v", code, out)
	}

	// Admin (not superuser) lists users; balance hidden? admin has balance:all:read.
	_, out = c.do("POST", "/auth/login", "", map[string]any{"email": "ops@example.com", "password": "password-1"})
	ops := out["data"].(map[string]any)["access_token"].(string)
	if code, out = c.do("GET", "/users?q=example&page_size=2", ops, nil); code != 200 || out["page"].(map[string]any)["total"].(float64) != 3 {
		t.Fatalf("ops lists users: %d %v", code, out)
	}
	// Delete needs only user:delete.
	joePath := "/users/" + strconv.FormatInt(joeID, 10)
	if code, out = c.do("DELETE", joePath, ops, nil); code != 403 || errCode(out) != "verification_required" {
		t.Fatalf("delete without a proof: %d %v", code, out)
	}
	proof := c.proof(ops, core.StepUpUserDelete, "DELETE /api/v1"+joePath, map[string]any{"method": "password", "password": "password-1"})
	if code, out = c.doProof("DELETE", joePath, ops, proof, nil); code != 204 {
		t.Fatalf("delete: %d %v", code, out)
	}
	if code, _ = c.do("GET", "/me", joe, nil); code != 401 {
		t.Fatalf("deleted user's token: %d", code)
	}

	// Roles endpoints.
	if code, out = c.do("PUT", "/users/"+strconv.FormatInt(opsID, 10)+"/roles", ops, map[string]any{"role_keys": []string{"super_admin"}}); code != 403 || errCode(out) != "permission_denied" {
		t.Fatalf("ops self-promotion: %d %v", code, out)
	}
	if code, out = c.do("POST", "/roles", ops, map[string]any{"key": "viewer", "name": map[string]string{"en": "Viewer", "zh": "只读"}, "permission_keys": []string{"user:read"}}); code != 201 {
		t.Fatalf("create role: %d %v", code, out)
	}
	if code, out = c.do("GET", "/roles", ops, nil); code != 200 || len(out["data"].([]any)) != 7 { // six built-in roles and viewer
		t.Fatalf("list roles: %d %v", code, out)
	}

	// A session has nothing to renew it with (CONTRACTS §100).
	if code, _ = c.do("POST", "/auth/refresh", "", map[string]any{"refresh_token": "x"}); code != 404 {
		t.Fatalf("refresh endpoint still mounted: %d", code)
	}
	// Logout ends that session only.
	_, out = c.do("POST", "/auth/login", "", map[string]any{"email": adminEmail, "password": adminPassword})
	other := out["data"].(map[string]any)["access_token"].(string)
	if code, _ = c.do("POST", "/auth/logout", other, nil); code != 204 {
		t.Fatalf("logout: %d", code)
	}
	if code, _ = c.do("GET", "/me", other, nil); code != 401 {
		t.Fatalf("session after its logout: %d", code)
	}
	if code, _ = c.do("GET", "/me", tok, nil); code != 200 {
		t.Fatalf("session after another one's logout: %d", code)
	}
	// Changing the password keeps the caller's own session only.
	_, out = c.do("POST", "/auth/login", "", map[string]any{"email": adminEmail, "password": adminPassword})
	other = out["data"].(map[string]any)["access_token"].(string)
	if code, out = c.do("PUT", "/me/password", tok, map[string]any{"new_password": "another-pass-1"}); code != 403 || errCode(out) != "verification_required" {
		t.Fatalf("change password without a proof: %d %v", code, out)
	}
	proof = c.proof(tok, core.StepUpSelfPassword, "PUT /api/v1/me/password", map[string]any{"method": "password", "password": adminPassword})
	if code, out = c.doProof("PUT", "/me/password", tok, proof, map[string]any{"new_password": "short"}); code != 400 {
		t.Fatalf("short password: %d %v", code, out)
	}
	if code, out = c.doProof("PUT", "/me/password", tok, proof, map[string]any{"new_password": "another-pass-1"}); code != 204 {
		t.Fatalf("change password: %d %v", code, out)
	}
	if code, _ = c.do("GET", "/me", other, nil); code != 401 {
		t.Fatalf("another session after the password change: %d", code)
	}
	if code, _ = c.do("GET", "/me", tok, nil); code != 200 {
		t.Fatalf("the caller's session after its password change: %d", code)
	}
}

// sectionKeys lists the section keys of a /me/menus response, in order.
func sectionKeys(data any) string {
	var keys []string
	for _, sec := range data.([]any) {
		keys = append(keys, sec.(map[string]any)["section"].(string))
	}
	return strings.Join(keys, ",")
}

// ListUsers keeps the page and size within httpapi.Pagination's bounds: a
// size of 0 or less, or a page before the first, would otherwise reach
// GORM, which leaves such a LIMIT or OFFSET out and reads every user.
func TestListUsersPageBounds(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if _, err := e.svc.db.SQL.ExecContext(ctx, `INSERT INTO users (email, password_hash)
		SELECT 'user' || n || '@example.com', 'x' FROM generate_series(1, 25) n`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ page, size, want int }{
		{1, 0, 20},  // no size: the default
		{0, -5, 20}, // a negative size: the default
		{-3, 2, 2},  // a page before the first: the first
		{1, 1000, 25},
	} {
		list, total, err := e.svc.ListUsers(ctx, ListUsersFilter{Scope: core.AllScope, Page: c.page, PageSize: c.size})
		if err != nil || total != 25 || len(list) != c.want {
			t.Errorf("page %d size %d: %d users of %d, %v; want %d of 25", c.page, c.size, len(list), total, err, c.want)
		}
	}
	first, _, err := e.svc.ListUsers(ctx, ListUsersFilter{Scope: core.AllScope, Page: 1, PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	before, _, _ := e.svc.ListUsers(ctx, ListUsersFilter{Scope: core.AllScope, Page: -3, PageSize: 2})
	if len(before) != 2 || before[0].ID != first[0].ID || before[1].ID != first[1].ID {
		t.Errorf("a page before the first lists %v, want the first page", before)
	}
}
