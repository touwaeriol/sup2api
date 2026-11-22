package group

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/authz"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

type fakeTokens struct{}

func (fakeTokens) VerifySession(_ context.Context, tok string) (int64, string, error) {
	return testutil.SessionUser(tok, "u")
}

type fakeAuthz struct{ admin int64 }

func (a fakeAuthz) Can(_ context.Context, uid int64, _ string) (bool, error) {
	return uid == a.admin, nil
}
func (a fakeAuthz) PermissionSet(context.Context, int64) (core.PermissionSet, error) {
	return core.PermissionSet{}, nil
}
func (fakeAuthz) CanGrant(context.Context, int64, []string) error { return nil }

type fakeBus struct {
	mu   sync.Mutex
	msgs []string
}

func (b *fakeBus) Publish(_ context.Context, ch string, p []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.msgs = append(b.msgs, ch+" "+string(p))
	return nil
}
func (b *fakeBus) Subscribe(string, func([]byte)) func() { return func() {} }

// fakeGen implements the parts of core.Generation used for group platforms.
type fakeGen struct {
	core.Generation
	plats []string
	types []core.AccountTypeBinding
}

// Plugins lists the plugins declaring the account types (account_count only
// counts accounts of enabled plugins).
func (g *fakeGen) Plugins() []core.PluginInfo {
	var out []core.PluginInfo
	seen := map[string]bool{}
	for _, t := range g.types {
		if !seen[t.Plugin.Key] {
			seen[t.Plugin.Key] = true
			out = append(out, core.PluginInfo{Key: t.Plugin.Key})
		}
	}
	return out
}

func (g *fakeGen) Platform(id string) (core.PlatformBinding, bool) {
	for _, p := range g.plats {
		if p == id {
			return core.PlatformBinding{Platform: manifest.Platform{ID: id}}, true
		}
	}
	return core.PlatformBinding{}, false
}

func (g *fakeGen) AccountType(pluginKey, typ string) (core.AccountTypeBinding, bool) {
	for _, b := range g.types {
		if b.Plugin.Key == pluginKey && b.Type.ID == typ {
			return b, true
		}
	}
	return core.AccountTypeBinding{}, false
}

type fakeRegistry struct{ gen core.Generation }

func (r fakeRegistry) Current() core.Generation                     { return r.gen }
func (fakeRegistry) OnChange(func(core.Generation)) (cancel func()) { return func() {} }

func accountType(plugin, id string, platforms ...string) core.AccountTypeBinding {
	b := core.AccountTypeBinding{Plugin: core.PluginInfo{Key: plugin}, Type: manifest.AccountType{ID: id}}
	for _, p := range platforms {
		b.Type.Platforms = append(b.Type.Platforms, manifest.AccountPlatform{Platform: p})
	}
	return b
}

// testGen: anthropic/apikey serves anthropic; relay/relay_key serves openai,
// anthropic and the unavailable "ghost"; video/vkey serves aivideo.
func testGen() *fakeGen {
	return &fakeGen{
		plats: []string{"anthropic", "openai", "gemini", "aivideo"},
		types: []core.AccountTypeBinding{
			accountType("anthropic", "apikey", "anthropic"),
			accountType("relay", "relay_key", "openai", "anthropic", "ghost"),
			accountType("video", "vkey", "aivideo"),
		},
	}
}

func TestPlatformResolver(t *testing.T) {
	key := func(p, typ string) core.AccountTypeKey { return core.AccountTypeKey{PluginKey: p, Type: typ} }
	r := newPlatformResolver(testGen())
	cases := []struct {
		types []core.AccountTypeKey
		want  string
	}{
		{nil, "[]"},
		{[]core.AccountTypeKey{key("anthropic", "apikey")}, "[anthropic]"},
		{[]core.AccountTypeKey{key("relay", "relay_key"), key("anthropic", "apikey")}, "[anthropic openai]"},
		{[]core.AccountTypeKey{key("video", "vkey"), key("relay", "relay_key")}, "[aivideo anthropic openai]"},
		// Unregistered type (plugin disabled) and same type id of another plugin.
		{[]core.AccountTypeKey{key("gone", "apikey"), key("video", "apikey")}, "[]"},
	}
	for _, c := range cases {
		got := r.platforms(c.types)
		if got == nil || fmt.Sprint(got) != c.want {
			t.Errorf("platforms(%v) = %v, want %s", c.types, got, c.want)
		}
	}
	var nilResolver *platformResolver
	if got := nilResolver.platforms([]core.AccountTypeKey{key("anthropic", "apikey")}); got == nil || len(got) != 0 {
		t.Fatalf("nil resolver: %v", got)
	}
	if got := newPlatformResolver(nil).platforms([]core.AccountTypeKey{key("anthropic", "apikey")}); got == nil || len(got) != 0 {
		t.Fatalf("nil generation: %v", got)
	}
}

func TestGroupPlatformsWithoutRegistry(t *testing.T) {
	// No registry: no database access, every group gets [].
	s := New(nil, nil, nil, nil, nil)
	ps, err := s.Platforms(context.Background(), []int64{1, 2})
	if err != nil || len(ps) != 2 || ps[1] == nil || len(ps[2]) != 0 {
		t.Fatalf("platforms: %v %v", ps, err)
	}
	b, _ := json.Marshal(MyGroup{Platforms: ps[1]})
	if !strings.Contains(string(b), `"platforms":[]`) {
		t.Fatalf("json: %s", b)
	}
}

type env struct {
	t     *testing.T
	db    *store.DB
	svc   *Service
	h     http.Handler
	mr    *miniredis.Miniredis
	bus   *fakeBus
	admin int64
	user  int64
}

func setup(t *testing.T) *env {
	gin.SetMode(gin.TestMode)
	db := testutil.DB(t)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	e := &env{t: t, db: db, mr: mr, bus: &fakeBus{}}
	e.admin = mkUser(t, db, "admin@x.com")
	e.user = mkUser(t, db, "user@x.com")
	engine := gin.New()
	r := httpapi.NewRouter(engine, fakeTokens{}, fakeAuthz{admin: e.admin})
	e.svc = New(db, rdb, e.bus, fakeRegistry{gen: testGen()}, authz.New(authz.Deps{}))
	e.svc.RegisterRoutes(r)
	e.h = engine
	return e
}

func mkUser(t *testing.T, db *store.DB, email string) int64 {
	var id int64
	if err := db.SQL.QueryRowContext(context.Background(),
		`INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *env) do(uid int64, method, path string, body any) (int, map[string]any) {
	e.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, "/api/v1"+path, rd)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer u%d", uid))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestGroupCRUDAndVisibility(t *testing.T) {
	e := setup(t)
	code, out := e.do(e.admin, "POST", "/groups", map[string]any{"name": "Default"})
	if code != 201 {
		t.Fatalf("create: %d %v", code, out)
	}
	def := out["data"].(map[string]any)
	if def["rate_multiplier"] != "1" || def["visibility"] != "public" || def["model_filter_mode"] != "blacklist" {
		t.Fatalf("defaults: %v", def)
	}
	code, out = e.do(e.admin, "POST", "/groups", map[string]any{"name": "VIP", "visibility": "restricted",
		"rate_multiplier": "0.80", "model_allowlist": []string{"claude-*"}})
	if code != 201 {
		t.Fatalf("create vip: %d %v", code, out)
	}
	vip := out["data"].(map[string]any)
	vipID := int64(vip["id"].(float64))
	if vip["rate_multiplier"] != "0.8" {
		t.Fatalf("rate: %v", vip["rate_multiplier"])
	}
	if code, out = e.do(e.admin, "POST", "/groups", map[string]any{"name": "VIP"}); code != 400 {
		t.Fatalf("dup: %d %v", code, out)
	}
	if code, _ = e.do(e.admin, "POST", "/groups", map[string]any{"name": "Bad", "rate_multiplier": "-1"}); code != 400 {
		t.Fatalf("bad rate: %d", code)
	}
	if code, _ = e.do(e.user, "GET", "/groups", nil); code != 403 {
		t.Fatalf("perm: %d", code)
	}

	_, out = e.do(e.user, "GET", "/me/groups", nil)
	if n := len(out["data"].([]any)); n != 1 {
		t.Fatalf("user sees %d groups", n)
	}
	// Seed a key so the cache drop path is exercised.
	e.mr.Set("apikey:h1", "{}")
	if _, err := e.db.SQL.ExecContext(context.Background(), `INSERT INTO api_keys (user_id, group_id, name, key_prefix, key_hash)
		VALUES ($1, $2, 'k', 'sk-s2a-xxxxx', 'h1')`, e.user, int64(def["id"].(float64))); err != nil {
		t.Fatal(err)
	}
	if code, out = e.do(e.admin, "PATCH", fmt.Sprintf("/groups/%d", int64(def["id"].(float64))), map[string]any{"description": "for everyone"}); code != 200 {
		t.Fatalf("patch default: %d %v", code, out)
	}
	if e.mr.Exists("apikey:h1") {
		t.Fatal("api key cache not dropped")
	}
	// A private group opens to the user once their segment is granted it.
	if _, err := e.db.SQL.ExecContext(context.Background(), `INSERT INTO segment_groups (segment_id, group_id, granted)
		SELECT segment_id, $2, true FROM users WHERE id = $1`, e.user, vipID); err != nil {
		t.Fatal(err)
	}
	_, out = e.do(e.user, "GET", "/me/groups", nil)
	if n := len(out["data"].([]any)); n != 2 {
		t.Fatalf("user sees %d groups after the grant", n)
	}

	code, out = e.do(e.admin, "PATCH", fmt.Sprintf("/groups/%d", vipID), map[string]any{"status": "disabled", "model_allowlist": []string{}})
	if code != 200 || out["data"].(map[string]any)["status"] != "disabled" {
		t.Fatalf("patch: %d %v", code, out)
	}
	_, out = e.do(e.user, "GET", "/me/groups", nil)
	if n := len(out["data"].([]any)); n != 1 {
		t.Fatalf("disabled group visible: %d", n)
	}

	_, out = e.do(e.admin, "GET", "/groups?page_size=1", nil)
	if out["page"].(map[string]any)["total"].(float64) != 2 || len(out["data"].([]any)) != 1 {
		t.Fatalf("list: %v", out)
	}
	defID := int64(def["id"].(float64))
	_, out = e.do(e.admin, "GET", fmt.Sprintf("/groups/%d", defID), nil)
	if out["data"].(map[string]any)["api_key_count"].(float64) != 1 {
		t.Fatalf("key count: %v", out)
	}
	// Keys do not pin their group: deleting it moves them to another group
	// or leaves them unbound.
	keyGroup := func() *int64 {
		var g *int64
		if err := e.db.SQL.QueryRowContext(context.Background(), `SELECT group_id FROM api_keys`).Scan(&g); err != nil {
			t.Fatal(err)
		}
		return g
	}
	for _, bad := range []string{fmt.Sprint(defID), "999999", "x"} {
		if code, _ = e.do(e.admin, "DELETE", fmt.Sprintf("/groups/%d?move_keys_to=%s", defID, bad), nil); code != 400 {
			t.Fatalf("delete moving keys to %s: %d", bad, code)
		}
	}
	if code, _ = e.do(e.admin, "DELETE", fmt.Sprintf("/groups/%d?move_keys_to=%d", defID, vipID), nil); code != 204 {
		t.Fatalf("delete moving keys: %d", code)
	}
	if g := keyGroup(); g == nil || *g != vipID {
		t.Fatalf("key not moved: %v", g)
	}
	if code, _ = e.do(e.admin, "GET", fmt.Sprintf("/groups/%d", defID), nil); code != 404 {
		t.Fatalf("get deleted: %d", code)
	}
	if code, _ = e.do(e.admin, "DELETE", fmt.Sprintf("/groups/%d", vipID), nil); code != 204 {
		t.Fatalf("delete with keys: %d", code)
	}
	if g := keyGroup(); g != nil {
		t.Fatalf("key still bound to the deleted group: %v", *g)
	}
	if len(e.bus.msgs) != 2 || !strings.HasPrefix(e.bus.msgs[0], core.ChannelAccountChanged) {
		t.Fatalf("bus: %v", e.bus.msgs)
	}
}

func TestGroupPlatforms(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mkGroup := func(name string) int64 {
		code, out := e.do(e.admin, "POST", "/groups", map[string]any{"name": name})
		if code != 201 || fmt.Sprint(out["data"].(map[string]any)["platforms"]) != "[]" {
			t.Fatalf("create %s: %d %v", name, code, out)
		}
		return int64(out["data"].(map[string]any)["id"].(float64))
	}
	mixed, video, empty := mkGroup("mixed"), mkGroup("video"), mkGroup("empty")
	mkAccount := func(plugin, typ string, deleted bool, groups ...int64) {
		var id int64
		if err := e.db.SQL.QueryRowContext(ctx, `INSERT INTO accounts (name, plugin_key, type, credentials_enc, deleted_at)
			VALUES ('a', $1, $2, '\x00'::bytea, CASE WHEN $3 THEN now() END) RETURNING id`, plugin, typ, deleted).Scan(&id); err != nil {
			t.Fatal(err)
		}
		for _, g := range groups {
			if _, err := e.db.SQL.ExecContext(ctx, `INSERT INTO account_groups (account_id, group_id) VALUES ($1, $2)`, id, g); err != nil {
				t.Fatal(err)
			}
		}
	}
	mkAccount("anthropic", "apikey", false, mixed)
	mkAccount("anthropic", "apikey", false, mixed) // same type twice
	mkAccount("relay", "relay_key", false, mixed)
	mkAccount("video", "vkey", true, mixed, video) // deleted: ignored
	mkAccount("gone", "apikey", false, video)      // type not registered
	want := map[int64]string{mixed: "[anthropic openai]", video: "[]", empty: "[]"}
	// account_count only counts accounts of enabled plugins: the deleted
	// video account and the "gone" plugin's account are left out.
	wantCount := map[int64]float64{mixed: 3, video: 0, empty: 0}

	_, out := e.do(e.admin, "GET", "/groups", nil)
	for _, it := range out["data"].([]any) {
		g := it.(map[string]any)
		if got := fmt.Sprint(g["platforms"]); got != want[int64(g["id"].(float64))] {
			t.Fatalf("list %v: %s", g["name"], got)
		}
		if got := g["account_count"]; got != wantCount[int64(g["id"].(float64))] {
			t.Fatalf("list %v account_count = %v", g["name"], got)
		}
	}
	_, out = e.do(e.admin, "GET", fmt.Sprintf("/groups/%d", mixed), nil)
	if got := fmt.Sprint(out["data"].(map[string]any)["platforms"]); got != "[anthropic openai]" {
		t.Fatalf("get: %s", got)
	}
	_, out = e.do(e.user, "GET", "/me/groups", nil)
	if len(out["data"].([]any)) != 3 {
		t.Fatalf("me/groups: %v", out)
	}
	for _, it := range out["data"].([]any) {
		g := it.(map[string]any)
		if got := fmt.Sprint(g["platforms"]); got != want[int64(g["id"].(float64))] {
			t.Fatalf("me/groups %v: %s", g["name"], got)
		}
	}
}

func TestGroupModelPolicyCRUD(t *testing.T) {
	e := setup(t)
	code, out := e.do(e.admin, "POST", "/groups", map[string]any{"name": "policy", "model_filter_mode": "blacklist", "model_allowlist": []string{"claude-opus-*"}})
	if code != 201 {
		t.Fatalf("create: %d %v", code, out)
	}
	g := out["data"].(map[string]any)
	id := int64(g["id"].(float64))
	if g["model_filter_mode"] != "blacklist" {
		t.Fatalf("mode: %v", g)
	}
	_, out = e.do(e.user, "GET", "/me/groups", nil)
	if out["data"].([]any)[0].(map[string]any)["model_filter_mode"] != "blacklist" {
		t.Fatalf("my groups: %v", out)
	}
	code, out = e.do(e.admin, "PATCH", fmt.Sprintf("/groups/%d", id), map[string]any{"model_filter_mode": "whitelist"})
	if code != 200 || out["data"].(map[string]any)["model_filter_mode"] != "whitelist" || len(out["data"].(map[string]any)["model_allowlist"].([]any)) != 1 {
		t.Fatalf("patch: %d %v", code, out)
	}
	for _, mode := range []string{"invalid", ""} {
		if code, _ = e.do(e.admin, "PATCH", fmt.Sprintf("/groups/%d", id), map[string]any{"model_filter_mode": mode}); code != 400 {
			t.Fatalf("invalid mode %q: %d", mode, code)
		}
	}
	if code, _ = e.do(e.user, "PATCH", fmt.Sprintf("/groups/%d", id), map[string]any{"model_filter_mode": "blacklist"}); code != 403 {
		t.Fatalf("permission: %d", code)
	}
}

func TestGroupModelIDs(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	_, out := e.do(e.admin, "POST", "/groups", map[string]any{"name": "model-source"})
	id := int64(out["data"].(map[string]any)["id"].(float64))
	for i, models := range [][]string{{"claude-opus-5-5", "gpt-5"}, {"gpt-5"}, {}, {"deleted-model"}} {
		var aid int64
		err := e.db.SQL.QueryRowContext(ctx, `INSERT INTO accounts (name,plugin_key,type,credentials_enc,models,deleted_at) VALUES ('model-source','anthropic','apikey','\x00'::bytea,$1,CASE WHEN $2 THEN now() END) RETURNING id`, models, i == 3).Scan(&aid)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = e.db.SQL.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id) VALUES ($1,$2)`, aid, id); err != nil {
			t.Fatal(err)
		}
	}
	code, out := e.do(e.admin, "GET", fmt.Sprintf("/groups/%d/models", id), nil)
	if code != 200 {
		t.Fatalf("models: %d %v", code, out)
	}
	data := out["data"].(map[string]any)
	if fmt.Sprint(data["models"]) != "[claude-opus-5-5 gpt-5]" || data["unrestricted_accounts"] != float64(1) {
		t.Fatalf("models: %v", data)
	}
	if code, _ = e.do(e.user, "GET", fmt.Sprintf("/groups/%d/models", id), nil); code != 403 {
		t.Fatalf("permission: %d", code)
	}
	if code, _ = e.do(e.admin, "GET", "/groups/999999/models", nil); code != 404 {
		t.Fatalf("missing group: %d", code)
	}
}
