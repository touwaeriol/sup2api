package egress

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

type apiTokens struct{}

func (apiTokens) VerifySession(_ context.Context, tok string) (int64, string, error) {
	return testutil.SessionUser(tok, "u")
}

// apiAuthz: perms per user.
type apiAuthz map[int64][]string

func (a apiAuthz) Can(ctx context.Context, uid int64, key string) (bool, error) {
	set, err := a.PermissionSet(ctx, uid)
	return set.Has(key), err
}
func (a apiAuthz) PermissionSet(_ context.Context, uid int64) (core.PermissionSet, error) {
	return core.PermissionSetOf(a[uid]...), nil
}
func (apiAuthz) CanGrant(context.Context, int64, []string) error { return nil }

type recordingBus struct {
	subBus
	published []string
}

func (b *recordingBus) Publish(ctx context.Context, ch string, p []byte) error {
	b.mu.Lock()
	b.published = append(b.published, ch+" "+string(p))
	b.mu.Unlock()
	return b.subBus.Publish(ctx, ch, p)
}

func TestHostPolicyRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := testutil.DB(t)
	admin := testutil.InsertUser(t, db, "admin@x.com")
	reader := testutil.InsertUser(t, db, "reader@x.com")
	bus := &recordingBus{}
	p := New(db, Options{Bus: bus})
	t.Cleanup(p.Close)
	engine := gin.New()
	p.RegisterRoutes(httpapi.NewRouter(engine, apiTokens{}, apiAuthz{
		admin:  {"settings:read", "settings:manage"},
		reader: {"settings:read"},
	}))
	do := func(uid int64, method string, body any) (int, map[string]any) {
		t.Helper()
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(method, "/api/v1/settings/plugin-egress", bytes.NewReader(b))
		req.Header.Set("Authorization", fmt.Sprintf("Bearer u%d", uid))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept-Language", "en")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	data := func(out map[string]any) string { return fmt.Sprint(out["data"]) }

	// No row: the default.
	code, out := do(reader, http.MethodGet, nil)
	if code != 200 || data(out) != "map[allowed_domains:[] blocked_domains:[] mode:allow_all]" {
		t.Fatalf("default: %d %v", code, out)
	}
	if code, _ := do(reader, http.MethodPut, map[string]any{"mode": "block_all"}); code != 403 {
		t.Fatalf("reader may not save: %d", code)
	}
	// Invalid entries are reported one by one; nothing is saved.
	code, out = do(admin, http.MethodPut, map[string]any{"mode": "deny", "blocked_domains": []string{"ok.com", "https://x.com", "x.com:443", "*.y.com"}})
	if code != 400 {
		t.Fatalf("invalid: %d %v", code, out)
	}
	var fields []string
	for _, f := range out["error"].(map[string]any)["details"].(map[string]any)["fields"].([]any) {
		fields = append(fields, f.(map[string]any)["field"].(string))
	}
	if strings.Join(fields, ",") != "mode,blocked_domains.1,blocked_domains.2,blocked_domains.3" {
		t.Fatalf("fields: %v", out)
	}
	// Saved in their stored form, without duplicates.
	code, out = do(admin, http.MethodPut, map[string]any{"blocked_domains": []string{"Evil.test", "evil.test.", " evil.test", "例子.测试"}})
	if code != 200 || data(out) != "map[allowed_domains:[] blocked_domains:[evil.test xn--fsqu00a.xn--0zwm56d] mode:allow_all]" {
		t.Fatalf("blocked: %d %v", code, out)
	}
	// Switching the mode and setting the other list keeps the first one.
	code, out = do(admin, http.MethodPut, map[string]any{"mode": "block_all", "allowed_domains": []string{"good.test", "2001:db8::1"}})
	if code != 200 || data(out) != "map[allowed_domains:[good.test [2001:db8::1]] blocked_domains:[evil.test xn--fsqu00a.xn--0zwm56d] mode:block_all]" {
		t.Fatalf("block_all: %d %v", code, out)
	}
	code, out = do(admin, http.MethodPut, map[string]any{"mode": "allow_all"})
	if code != 200 || data(out) != "map[allowed_domains:[good.test [2001:db8::1]] blocked_domains:[evil.test xn--fsqu00a.xn--0zwm56d] mode:allow_all]" {
		t.Fatalf("back: %d %v", code, out)
	}
	if _, got := do(reader, http.MethodGet, nil); data(got) != data(out) {
		t.Fatalf("get after save: %v", got)
	}
	// Every save is audited and broadcast.
	var audits []model.AuditLog
	if err := db.Gorm.Where("action = ?", "settings.plugin_egress.update").Order("id").Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if len(audits) != 3 || audits[2].UserID == nil || *audits[2].UserID != admin || audits[2].TargetID != HostSettingsKey ||
		!strings.Contains(string(audits[2].Detail), `"mode": "allow_all"`) {
		t.Fatalf("audits: %+v", audits)
	}
	if len(bus.published) != 3 || bus.published[0] != core.ChannelConfigChanged+` {"key":"settings.plugin_egress"}` {
		t.Fatalf("broadcast: %v", bus.published)
	}

	// The tunnel of this provider reads the saved setting.
	if msg := p.hostRefusal(context.Background(), "a.evil.test"); !strings.Contains(msg, "blocked list") {
		t.Fatalf("tunnel: %q", msg)
	}
	do(admin, http.MethodPut, map[string]any{"blocked_domains": []string{}})
	if msg := p.hostRefusal(context.Background(), "a.evil.test"); msg != "" {
		t.Fatalf("after unblocking: %q", msg)
	}
}
