package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry/registrytest"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/secret"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	authzsvc "github.com/Sub2API-Devs/sup2api/next/server/internal/authz"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/config"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/install"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg/pkgtest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/task"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// ---------------------------------------------------------------- fakes

type tokens struct{ testutil.StepUpPassed }

func (tokens) VerifySession(_ context.Context, tok string) (int64, string, error) {
	switch tok {
	case "admin":
		return 1, "", nil
	case "viewer":
		return 2, "", nil
	case "nobody":
		return 3, "", nil
	case "manager":
		return 4, "", nil
	case "runner":
		return 5, "", nil
	}
	return 0, "", core.ErrUnauthenticated
}

// authz: user 1 is superuser; user 2 only has plugin.guard:stats:read;
// user 3 has nothing; user 4 manages plugins; user 5 only runs tasks.
type authz struct{}

func (a authz) Can(ctx context.Context, uid int64, p string) (bool, error) {
	ps, err := a.PermissionSet(ctx, uid)
	return ps.Has(p), err
}

// VerifyAccessKey: sak_<perm>,<perm>... is the viewer's (user 2) key with
// those permissions.
func (tokens) VerifyAccessKey(_ context.Context, raw, _ string) (core.AccessKeyIdentity, error) {
	return core.AccessKeyIdentity{UserID: 2, KeyID: 1, Permissions: strings.Split(strings.TrimPrefix(raw, core.AccessKeyPrefix), ",")}, nil
}

// Every signed-in user holds the self permissions, as authz gives them; an
// access key narrows them as authz does.
func (a authz) PermissionSet(ctx context.Context, uid int64) (core.PermissionSet, error) {
	set, err := a.userSet(uid)
	return core.RestrictToAccessKey(ctx, uid, set), err
}

func (authz) userSet(uid int64) (core.PermissionSet, error) {
	var keys []string
	switch uid {
	case 1:
		return core.PermissionSet{Superuser: true}, nil
	case 2:
		keys = []string{"plugin.guard:stats:read"}
	case 4:
		keys = []string{PermRead, PermManage}
	case 5:
		keys = []string{task.PermRun}
	}
	return core.PermissionSetOf(append(keys, PermSelfUI)...), nil
}
func (authz) CanGrant(context.Context, int64, []string) error { return nil }

type noopPerms struct{}

func (noopPerms) SyncPlugin(context.Context, *gorm.DB, string, []core.PermissionDef) error {
	return nil
}
func (noopPerms) CanGrantToRoles(context.Context, *gorm.DB, int64, []string, string) error {
	return nil
}

// The catalog's reads come from the tables the tests fill.
func (noopPerms) PluginPermissions(ctx context.Context, q *gorm.DB, key string) ([]core.PluginPermission, error) {
	return authzsvc.New(authzsvc.Deps{}).PluginPermissions(ctx, q, key)
}
func (noopPerms) PermissionHolders(ctx context.Context, q *gorm.DB, keys []string) (map[string][]core.PermissionHolder, error) {
	return authzsvc.New(authzsvc.Deps{}).PermissionHolders(ctx, q, keys)
}
func (noopPerms) PluginGrantees(context.Context, *gorm.DB, int64, []string) (core.PluginGrantees, error) {
	return core.PluginGrantees{}, nil
}
func (noopPerms) GrantPluginPermission(context.Context, *gorm.DB, string, map[string][]string) error {
	return nil
}
func (noopPerms) SetPluginActive(context.Context, *gorm.DB, string, bool) error { return nil }
func (noopPerms) DiscardStaged(context.Context, *gorm.DB, string) error         { return nil }
func (noopPerms) DeletePlugin(context.Context, *gorm.DB, string) error          { return nil }

type rollout struct {
	db *store.DB
}

func (r rollout) Enable(ctx context.Context, key string, _ int64) (*core.Rollout, error) {
	_, err := r.db.SQL.ExecContext(ctx, `UPDATE plugins SET status = 'enabled', active_version = '0.1.0' WHERE key = $1`, key)
	return &core.Rollout{ID: 7, PluginKey: key, Action: "enable", Phase: "active", TargetVersion: "0.1.0"}, err
}
func (r rollout) Upgrade(_ context.Context, key, v string, _ int64) (*core.Rollout, error) {
	return &core.Rollout{ID: 8, PluginKey: key, Action: "upgrade", Phase: "preparing", TargetVersion: v}, nil
}
func (r rollout) Disable(ctx context.Context, key string, _ int64, _ string) (*core.Rollout, error) {
	_, err := r.db.SQL.ExecContext(ctx, `UPDATE plugins SET status = 'disabled' WHERE key = $1`, key)
	return &core.Rollout{ID: 9, PluginKey: key, Action: "disable", Phase: "active"}, err
}
func (r rollout) Current(context.Context, string) (*core.Rollout, error) { return nil, nil }
func (r rollout) Cancel(context.Context, string, int64, int64) error     { return nil }

type nodes struct{}

// Lifecycle fixtures have no launched processes; the UI node-report fake
// below is intentionally separate and must not create uninstall targets.
type emptyLifecycleNodes struct{ core.NodeRegistry }

func (emptyLifecycleNodes) LiveNodes(context.Context) ([]core.NodeStatus, error) {
	return []core.NodeStatus{}, nil
}

func (nodes) NodeID() string { return "node-1" }
func (nodes) BootID() string { return "boot-1" }
func (nodes) LiveNodes(context.Context) ([]core.NodeStatus, error) {
	return []core.NodeStatus{
		{NodeID: "node-1", BootID: "boot-1", Plugins: map[string]string{"guard": `{"state":"running","version":"0.1.0","memory_mb":42}`}},
		{NodeID: "node-2", BootID: "boot-2", Plugins: map[string]string{}},
	}, nil
}
func (nodes) IsAlive(context.Context, string) (bool, error)      { return true, nil }
func (nodes) ReportPlugin(context.Context, string, string) error { return nil }
func (nodes) Healthy() bool                                      { return true }

type gen struct {
	plugins  []core.PluginInfo
	settings map[string]core.SettingsValidator
}

func (g gen) SettingsValidator(k string) (core.SettingsValidator, bool) {
	v, ok := g.settings[k]
	return v, ok
}

func (g gen) Number() uint64             { return 1 }
func (g gen) Plugins() []core.PluginInfo { return g.plugins }
func (g gen) Plugin(k string) (core.PluginInfo, bool) {
	for _, p := range g.plugins {
		if p.Key == k {
			return p, true
		}
	}
	return core.PluginInfo{}, false
}
func (gen) Endpoints() []core.EndpointBinding            { return nil }
func (gen) Platforms() []core.PlatformBinding            { return nil }
func (gen) Platform(string) (core.PlatformBinding, bool) { return core.PlatformBinding{}, false }
func (gen) PlatformForProtocol(string) (core.PlatformBinding, bool) {
	return core.PlatformBinding{}, false
}
func (gen) AccountTypes() []core.AccountTypeBinding { return nil }
func (gen) AccountType(string, string) (core.AccountTypeBinding, bool) {
	return core.AccountTypeBinding{}, false
}
func (gen) AccountTypesForPlatform(string) []core.AccountTypeBinding { return nil }
func (gen) Hooks(string) []core.HookBinding                          { return nil }
func (gen) Scheduler(string) (core.SchedulerPlugin, bool)            { return nil, false }
func (gen) AccountRankers() []core.AccountRankerBinding              { return nil }
func (gen) Routes(string) []core.RouteBinding                        { return nil }
func (gen) Jobs() []core.JobBinding                                  { return nil }
func (gen) JobRunner(string) (core.AppPlugin, bool)                  { return nil, false }
func (gen) Subscriptions() []core.SubscriptionBinding                { return nil }
func (gen) ReadAsset(string, string) ([]byte, string, error)         { return nil, "", errors.New("n/a") }

type registry struct{ g gen }

func (r registry) Current() core.Generation                     { return r.g }
func (registry) OnChange(func(core.Generation)) (cancel func()) { return func() {} }

// ---------------------------------------------------------------- harness

type harness struct {
	t      *testing.T
	engine *gin.Engine
	db     *store.DB
	guard  *manifest.Manifest // the registry's copy; tests mutate it in place
	api    *API
	// validators are the settings checks of the registry's generation.
	validators map[string]core.SettingsValidator
}

// jobRuns records manual job runs as "plugin/job by actor".
type jobRuns []string

func (j *jobRuns) RunNow(_ context.Context, pluginKey, jobID string, actorID int64) error {
	*j = append(*j, fmt.Sprintf("%s/%s by %d", pluginKey, jobID, actorID))
	return nil
}

func (h *harness) do(method, path, token string, body any) (int, map[string]any) {
	h.t.Helper()
	var rdr *bytes.Reader
	ct := "application/json"
	switch b := body.(type) {
	case nil:
		rdr = bytes.NewReader(nil)
	case *bytes.Buffer:
		rdr = bytes.NewReader(b.Bytes())
	case multipartBody:
		rdr = bytes.NewReader(b.buf.Bytes())
		ct = b.ct
	default:
		raw, _ := json.Marshal(b)
		rdr = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, "/api/v1"+path, rdr)
	req.Header.Set("Content-Type", ct)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.engine.ServeHTTP(w, req)
	out := map[string]any{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

type multipartBody struct {
	buf *bytes.Buffer
	ct  string
}

func fileBody(t *testing.T, data []byte) multipartBody {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "guard.s2plugin")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(data)
	_ = mw.Close()
	return multipartBody{buf: &buf, ct: mw.FormDataContentType()}
}

func newHarness(t *testing.T, guardConflicts ...core.ResourceConflict) (*harness, pkgtest.Key) {
	db := testutil.DB(t)
	ctx := context.Background()
	for _, email := range []string{"admin@x", "viewer@x"} {
		if _, err := db.SQL.ExecContext(ctx, `INSERT INTO users (email, password_hash) VALUES ($1, 'x')`, email); err != nil {
			t.Fatal(err)
		}
	}
	root := pkgtest.NewKey("root-1")
	ts, err := pkg.NewTrustStore([]string{root.ID + "=" + root.PubB64()}, false)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.PluginConfig{MaxPackageBytes: 10 << 20, MaxMemoryMB: 1024}
	svc := install.New(install.Deps{DB: db, Trust: ts, Authz: authz{}, Permissions: noopPerms{},
		Defaults: install.NewDefaultsApplier(noopPerms{}), Rollout: rollout{db}, Nodes: emptyLifecycleNodes{}, Packages: registrytest.Source()},
		install.Options{HostVersion: testHostVersion, Plugins: cfg})
	cipher, _ := secret.New(bytes.Repeat([]byte{7}, 32))
	m := pkgtest.Guard("guard", "0.1.0", "sub2api")
	validators := map[string]core.SettingsValidator{}
	reg := registry{gen{plugins: []core.PluginInfo{{Key: "guard", Version: "0.1.0", Manifest: m, Trust: "official", AssetBase: "/plugin-ui/guard/0.1.0-abc",
		Conflicts: guardConflicts}}, settings: validators}}
	a := New(Deps{DB: db, Install: svc, Rollout: rollout{db}, Nodes: nodes{}, Registry: reg, Authz: authz{}, Cipher: cipher, Plugins: cfg})
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	r := httpapi.NewRouter(engine, tokens{}, authz{})
	a.RegisterRoutes(r)
	return &harness{t: t, engine: engine, db: db, guard: m, api: a, validators: validators}, root
}

func data(out map[string]any) map[string]any {
	d, _ := out["data"].(map[string]any)
	return d
}

func TestAPIFlow(t *testing.T) {
	h, root := newHarness(t)
	ctx := context.Background()
	guard := pkgtest.Guard("guard", "0.1.0", "sub2api")
	guard.UI.Settings.Permission = "stats:read"
	pkgBytes := pkgtest.Build(guard, root)

	if code, _ := h.do("POST", "/plugins/upload", "viewer", fileBody(t, pkgBytes)); code != http.StatusForbidden {
		t.Fatalf("viewer upload = %d", code)
	}
	code, out := h.do("POST", "/plugins/upload", "admin", fileBody(t, pkgBytes))
	if code != 200 || data(out)["plugin_key"] != "guard" || data(out)["trust"] != "official" {
		t.Fatalf("upload = %d %v", code, out)
	}
	if code, out = h.do("GET", "/plugins/guard/versions/0.1.0/review", "admin", nil); code != 200 {
		t.Fatalf("review = %d %v", code, out)
	}
	if code, _ = h.do("POST", "/plugins/guard/enable", "admin", nil); code != http.StatusConflict {
		t.Fatalf("enable before consent = %d", code)
	}
	consent := install.ConsentRequest{Grants: []install.GrantInput{{Permission: "db.schema"}, {Permission: "gateway.hook"},
		{Permission: "events"}, {Permission: "jobs"}, {Permission: "routes.admin"}, {Permission: "ui.menu"}, {Permission: "ui.native"}}}
	if code, out = h.do("POST", "/plugins/guard/versions/0.1.0/consent", "admin", consent); code != 200 || data(out)["status"] != "installed" {
		t.Fatalf("consent = %d %v", code, out)
	}

	code, out = h.do("GET", "/plugins", "admin", nil)
	list, _ := out["data"].([]any)
	if code != 200 || len(list) != 1 {
		t.Fatalf("list = %d %v", code, out)
	}
	item := list[0].(map[string]any)
	if item["current_version"] != "0.1.0" || item["signature_status"] != "valid" {
		t.Fatalf("item = %v", item)
	}
	if rc, ok := item["resource_conflicts"].([]any); !ok || len(rc) != 0 {
		t.Fatalf("resource conflicts = %v", item["resource_conflicts"])
	}
	// The list and the detail both carry the summary as node_summary.
	if _, ok := item["node_summary"].(map[string]any); !ok || item["nodes"] != nil {
		t.Fatalf("list node summary = %v, nodes = %v", item["node_summary"], item["nodes"])
	}

	if code, out = h.do("POST", "/plugins/guard/enable", "admin", nil); code != 200 || data(out)["id"] != float64(7) {
		t.Fatalf("enable = %d %v", code, out)
	}

	// Settings: secret masking, keep-on-mask.
	if code, out = h.do("PUT", "/plugins/guard/settings", "admin", map[string]any{"values": map[string]any{"threshold": 3, "webhook_secret": "s3cret"}}); code != 200 {
		t.Fatalf("put settings = %d %v", code, out)
	}
	vals := data(out)["values"].(map[string]any)
	if vals["webhook_secret"] != Mask || vals["threshold"] != float64(3) || data(out)["mode"] != "native" {
		t.Fatalf("settings = %v", data(out))
	}
	// ui.settings.permission lets its holders read and edit the settings
	// without plugin:read / plugin:manage (CONTRACTS §75.1).
	if code, out = h.do("GET", "/plugins/guard/settings", "nobody", nil); code != http.StatusForbidden {
		t.Fatalf("settings without permission = %d %v", code, out)
	}
	if code, out = h.do("GET", "/plugins/guard/settings", "viewer", nil); code != 200 || data(out)["permission"] != "plugin.guard:stats:read" {
		t.Fatalf("settings through the plugin permission = %d %v", code, out)
	}
	// An access key needs the plugin pages' self permission and the
	// settings permission both (期 4 S4).
	for perms, want := range map[string]int{
		"plugin.guard:stats:read,self:plugin_ui": 200,
		"plugin.guard:stats:read":                http.StatusForbidden,
		"self:plugin_ui":                         http.StatusForbidden,
	} {
		if code, out := h.do("GET", "/plugins/guard/settings", core.AccessKeyPrefix+perms, nil); code != want {
			t.Fatalf("settings through a key with %s = %d %v, want %d", perms, code, out, want)
		}
	}
	// Unknown plugin: 403 without permission (no existence leak), 404 with it.
	if code, out = h.do("PUT", "/plugins/ghost/settings", "nobody", map[string]any{"values": map[string]any{}}); code != http.StatusForbidden {
		t.Fatalf("unknown plugin without permission = %d %v", code, out)
	}
	if code, out = h.do("GET", "/plugins/ghost/settings", "admin", nil); code != http.StatusNotFound {
		t.Fatalf("unknown plugin = %d %v", code, out)
	}
	// Only through the plugin permission, a masked secret is kept only while
	// nothing else changes; changing another field needs the secret again.
	code, out = h.do("PUT", "/plugins/guard/settings", "viewer", map[string]any{"values": map[string]any{"threshold": 4, "webhook_secret": Mask}})
	require.Equal(t, http.StatusBadRequest, code, out)
	fields, _ := out["error"].(map[string]any)["details"].(map[string]any)["fields"].([]any)
	require.Len(t, fields, 1, out)
	assert.Equal(t, "values/webhook_secret", fields[0].(map[string]any)["field"])
	code, out = h.do("PUT", "/plugins/guard/settings", "viewer", map[string]any{"values": map[string]any{"threshold": 3, "webhook_secret": Mask}})
	require.Equal(t, http.StatusOK, code, out)
	code, out = h.do("PUT", "/plugins/guard/settings", "admin", map[string]any{"values": map[string]any{"threshold": 5, "webhook_secret": Mask}})
	require.Equal(t, http.StatusOK, code, out)
	code, out = h.do("PUT", "/plugins/guard/settings", "viewer", map[string]any{"values": map[string]any{"threshold": 4, "webhook_secret": "s3cret"}})
	require.Equal(t, http.StatusOK, code, out)
	var who int64
	if err := h.db.SQL.QueryRowContext(ctx, `SELECT user_id FROM audit_logs WHERE action = 'plugin.settings.update' ORDER BY id DESC LIMIT 1`).Scan(&who); err != nil || who != 2 {
		t.Fatalf("settings audit by %d: %v", who, err)
	}
	var enc []byte
	_ = h.db.SQL.QueryRowContext(ctx, `SELECT config_enc FROM plugins WHERE key = 'guard'`).Scan(&enc)
	cipher, _ := secret.New(bytes.Repeat([]byte{7}, 32))
	plain, err := cipher.Decrypt(enc, ConfigAAD("guard"))
	if err != nil || !bytes.Contains(plain, []byte(`"webhook_secret":"s3cret"`)) || !bytes.Contains(plain, []byte(`"threshold":4`)) {
		t.Fatalf("stored settings = %s %v", plain, err)
	}

	// Resources and egress policy.
	if code, _ = h.do("PUT", "/plugins/guard/resources", "admin", map[string]any{"memory_mb": 99999}); code != 400 {
		t.Fatalf("resources over cap = %d", code)
	}
	if code, out = h.do("PUT", "/plugins/guard/resources", "admin", map[string]any{"memory_mb": 256}); code != 200 ||
		data(out)["effective"].(map[string]any)["memory_mb"] != float64(256) || data(out)["restart_notified"] != false ||
		data(out)["message"].(map[string]any)["zh"] == "" {
		t.Fatalf("resources = %d %v", code, out)
	}
	if code, _ = h.do("PUT", "/plugins/guard/egress-policy", "admin", map[string]any{"policy": "allowlist"}); code != 200 {
		t.Fatalf("egress policy = %d", code)
	}

	// Seed runtime data.
	now := time.Now().UTC()
	mustExec(t, h.db, `INSERT INTO events (type, payload) VALUES ('usage.recorded', '{}'), ('usage.recorded', '{}'), ('user.created', '{}')`)
	mustExec(t, h.db, `INSERT INTO plugin_event_cursors (plugin_key, last_event_id) VALUES ('guard', 1)`)
	mustExec(t, h.db, `INSERT INTO plugin_job_runs (plugin_key, job_id, node_id, scheduled_at, started_at, status) VALUES ('guard', 'rollup', 'node-1', $1, $1, 'succeeded')`, now)
	mustExec(t, h.db, `INSERT INTO plugin_egress_logs (plugin_key, node_id, network, host, port, started_at, duration_ms, bytes_in, bytes_out, result)
		VALUES ('guard', 'node-1', 'tcp', 'hooks.example.com', 443, $1, 5, 10, 20, 'ok'), ('guard', 'node-1', 'tcp', 'hooks.example.com', 443, $1, 5, 1, 2, 'denied'),
		       ('guard', 'node-1', 'tcp', 'hooks.example.com', 443, $1, 0, 0, 0, 'open')`, now)
	mustExec(t, h.db, `INSERT INTO plugin_egress_domains (plugin_key, host, first_seen_at, last_seen_at, connections)
		VALUES ('guard', 'hooks.example.com', $1, $1, 3), ('guard', 'old.example.com', $2, $2, 1)`, now, now.Add(-48*time.Hour))

	code, out = h.do("GET", "/plugins/guard", "admin", nil)
	d := data(out)
	if code != 200 || d["status"] != "enabled" || d["egress_policy"] != "allowlist" {
		t.Fatalf("detail = %d %v", code, out)
	}
	if ns, _ := d["nodes"].([]any); len(ns) != 1 {
		t.Fatalf("nodes = %v", d["nodes"])
	}
	if sum, _ := d["node_summary"].(map[string]any); sum["total"] != float64(2) {
		t.Fatalf("node summary = %v", d["node_summary"])
	}
	ev := d["events"].(map[string]any)
	if ev["backlog"] != float64(1) {
		t.Fatalf("events = %v", ev)
	}
	jobs := d["jobs"].([]any)
	if len(jobs) != 2 || jobs[0].(map[string]any)["next_run_at"] == nil {
		t.Fatalf("jobs = %v", jobs)
	}
	if code, out = h.do("GET", "/plugins/guard/egress", "admin", nil); code != 200 {
		t.Fatalf("egress = %d %v", code, out)
	}
	sum := data(out)["summary"].([]any)[0].(map[string]any)
	if sum["count"] != float64(3) || sum["denied"] != float64(1) || sum["open"] != float64(1) || sum["errors"] != float64(0) || sum["bytes_out"] != float64(22) {
		t.Fatalf("egress summary = %v", sum)
	}
	doms := data(out)["domains"].([]any)
	if len(doms) != 2 || doms[0].(map[string]any)["host"] != "hooks.example.com" || doms[0].(map[string]any)["new"] != true ||
		doms[0].(map[string]any)["connections"] != float64(3) || doms[1].(map[string]any)["new"] != false {
		t.Fatalf("egress domains = %v", doms)
	}
	openRows := 0
	for _, it := range data(out)["items"].([]any) {
		if row := it.(map[string]any); row["result"] == "open" {
			openRows++
			if row["closed_at"] != nil {
				t.Fatalf("open row closed_at = %v", row["closed_at"])
			}
		}
	}
	if openRows != 1 {
		t.Fatalf("open egress rows = %d", openRows)
	}
	if code, _ = h.do("POST", "/plugins/guard/jobs/rollup/run", "admin", nil); code != http.StatusServiceUnavailable {
		t.Fatalf("run job without runner = %d", code)
	}
	// A manual run is a task run: it needs task:run as POST
	// /system/tasks/:name/run does, plugin:manage alone is not enough.
	var runs jobRuns
	h.api.d.Jobs = &runs
	code, out = h.do("POST", "/plugins/guard/jobs/rollup/run", "manager", nil)
	if code != http.StatusForbidden || out["error"].(map[string]any)["details"].(map[string]any)["permission"] != task.PermRun {
		t.Fatalf("run job with plugin:manage only = %d %v", code, out)
	}
	if code, out = h.do("POST", "/plugins/guard/jobs/rollup/run", "runner", nil); code != http.StatusNoContent {
		t.Fatalf("run job with task:run = %d %v", code, out)
	}
	var auditAction string
	_ = h.db.SQL.QueryRowContext(ctx, `SELECT action FROM audit_logs WHERE user_id = 5 ORDER BY id DESC LIMIT 1`).Scan(&auditAction)
	if len(runs) != 1 || runs[0] != "guard/rollup by 5" || auditAction != "plugin.job.run" {
		t.Fatalf("job runs = %v, audit action = %q", runs, auditAction)
	}

	// UI extensions filtered by permission.
	code, out = h.do("GET", "/ui/plugins", "viewer", nil)
	ui, _ := out["data"].([]any)
	if code != 200 || len(ui) != 1 {
		t.Fatalf("ui = %d %v", code, out)
	}
	u := ui[0].(map[string]any)
	if len(u["menus"].([]any)) != 1 || u["native_entry"] != "ui/native/entry.js" || u["host_ui_compat"] != "^4.0" {
		t.Fatalf("ui item = %v", u)
	}

	// Publishers.
	k := pkgtest.NewKey("acme-1")
	if code, out = h.do("POST", "/publishers", "admin", map[string]any{"name": "acme", "trust_level": "verified",
		"keys": []any{map[string]any{"key_id": k.ID, "public_key": k.PubB64()}}}); code != 201 {
		t.Fatalf("create publisher = %d %v", code, out)
	}
	if code, out = h.do("GET", "/publishers", "admin", nil); code != 200 || len(out["data"].([]any)) != 2 {
		t.Fatalf("publishers = %d %v", code, out)
	}
	if code, out = h.do("POST", "/publisher-keys/acme-1/revoke", "admin", map[string]any{"reason": "test"}); code != 200 {
		t.Fatalf("revoke key = %d %v", code, out)
	}

	// Uninstall (disables first).
	if code, out = h.do("DELETE", "/plugins/guard?purge=true", "admin", nil); code != 200 || out["data"].(map[string]any)["accounts_deleted"] != float64(0) {
		t.Fatalf("uninstall = %d %v", code, out)
	}
	if code, _ = h.do("GET", "/plugins/guard", "admin", nil); code != 404 {
		t.Fatalf("after uninstall = %d", code)
	}
	var n int
	_ = h.db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM audit_logs WHERE action LIKE 'plugin.%'`).Scan(&n)
	if n < 7 {
		t.Fatalf("audit rows = %d", n)
	}
}

// ui.accountActions are filtered by the action's permission like slots,
// and alone keep a plugin in the listing (CONTRACTS §15.8, UI 3.6).
func TestUIPluginsAccountActions(t *testing.T) {
	h, _ := newHarness(t)
	reset := manifest.AccountAction{ID: "reset", Label: manifest.LocalizedText{"en": "Reset"}, Permission: "rules:manage", Route: "POST /reset", Bulk: true}
	stats := manifest.AccountAction{ID: "stats", Label: manifest.LocalizedText{"en": "Stats"}, Permission: "stats:read", Route: "POST /stats", VisibleFor: "all"}
	h.guard.UI = &manifest.UI{AccountActions: []manifest.AccountAction{reset, stats}}

	actions := func(token string) []any {
		code, out := h.do("GET", "/ui/plugins", token, nil)
		ui, _ := out["data"].([]any)
		if code != 200 {
			t.Fatalf("ui = %d %v", code, out)
		}
		if len(ui) == 0 {
			return nil
		}
		return ui[0].(map[string]any)["account_actions"].([]any)
	}
	// The declaration is sent as is: visibleFor only when the plugin set it.
	if got := actions("admin"); len(got) != 2 || got[0].(map[string]any)["route"] != "POST /reset" || got[0].(map[string]any)["bulk"] != true ||
		got[1].(map[string]any)["visibleFor"] != "all" {
		t.Fatalf("admin actions = %v", got)
	} else if _, has := got[0].(map[string]any)["visibleFor"]; has {
		t.Fatalf("an unset visibleFor must be left out: %v", got[0])
	}
	// viewer holds only stats:read.
	if got := actions("viewer"); len(got) != 1 || got[0].(map[string]any)["id"] != "stats" {
		t.Fatalf("viewer actions = %v", got)
	}
	// accountTypes (UI 3.8, CONTRACTS §91b) go down as declared, on actions,
	// detail tabs and quota column decorations alike; the console matches them.
	stats.AccountTypes = []string{"device", "web"}
	h.guard.UI.AccountActions = []manifest.AccountAction{reset, stats}
	h.guard.UI.Slots = []manifest.Slot{
		{Slot: manifest.SlotAccountDetailTabs, Component: "Quota", Permission: "stats:read", AccountTypes: []string{"device"}},
		{Slot: manifest.SlotAccountListQuota, Component: "QuotaBadge", AccountTypes: []string{"web"}},
	}
	if got := actions("admin"); !reflect.DeepEqual(got[1].(map[string]any)["accountTypes"], []any{"device", "web"}) {
		t.Fatalf("typed action = %v", got[1])
	} else if _, has := got[0].(map[string]any)["accountTypes"]; has {
		t.Fatalf("an unset accountTypes must be left out: %v", got[0])
	}
	_, out := h.do("GET", "/ui/plugins", "viewer", nil)
	if slots := out["data"].([]any)[0].(map[string]any)["slots"].([]any); len(slots) != 2 ||
		!reflect.DeepEqual(slots[0].(map[string]any)["accountTypes"], []any{"device"}) ||
		!reflect.DeepEqual(slots[1].(map[string]any)["accountTypes"], []any{"web"}) {
		t.Fatalf("typed slots = %v", slots)
	}
	h.guard.UI.Slots = nil
	// A plugin whose only extension the caller may use is an action is still
	// listed; one with nothing left for the caller is not.
	h.guard.UI.AccountActions = []manifest.AccountAction{reset}
	if got := actions("admin"); len(got) != 1 {
		t.Fatalf("admin actions = %v", got)
	}
	if code, out := h.do("GET", "/ui/plugins", "viewer", nil); code != 200 || len(out["data"].([]any)) != 0 {
		t.Fatalf("viewer with nothing usable = %d %v", code, out)
	}
}

// A plugin whose only console extension is its native entry (an account
// form, form.mode "native", that no menu, page or slot names) is still
// listed, so the console loads the entry; one without a native entry and
// nothing else is not (CONTRACTS §15.8).
func TestUIPluginsNativeEntryOnly(t *testing.T) {
	h, _ := newHarness(t)
	h.guard.UI = &manifest.UI{Native: &manifest.NativeUI{Entry: "ui/native/entry.js"}}

	code, out := h.do("GET", "/ui/plugins", "viewer", nil)
	ui, _ := out["data"].([]any)
	if code != 200 || len(ui) != 1 {
		t.Fatalf("native-only plugin = %d %v", code, out)
	}
	u := ui[0].(map[string]any)
	if u["key"] != "guard" || u["native_entry"] != "ui/native/entry.js" || len(u["menus"].([]any)) != 0 || len(u["pages"].(map[string]any)) != 0 {
		t.Fatalf("native-only item = %v", u)
	}

	h.guard.UI.Native = nil
	if code, out := h.do("GET", "/ui/plugins", "viewer", nil); code != 200 || len(out["data"].([]any)) != 0 {
		t.Fatalf("plugin with no extension = %d %v", code, out)
	}
}

// GET /ui/plugins shows a plugin's menu and slot to a holder of any scope
// of their permission.
func TestUIPluginsScopedPermission(t *testing.T) {
	h, _ := newHarness(t)
	menus := func(token string) []any {
		code, out := h.do("GET", "/ui/plugins", token, nil)
		require.Equal(t, 200, code, out)
		ui, _ := out["data"].([]any)
		if len(ui) == 0 {
			return nil
		}
		return ui[0].(map[string]any)["menus"].([]any)
	}
	// The viewer holds stats:read; its key narrows that to one value.
	assert.Len(t, menus(core.AccessKeyPrefix+"plugin.guard:stats:read:value/a,"+PermSelfUI), 1)
	assert.Empty(t, menus(core.AccessKeyPrefix+"plugin.guard:other:read:value/a,"+PermSelfUI))
}

func mustExec(t *testing.T, db *store.DB, sql string, args ...any) {
	t.Helper()
	if _, err := db.SQL.ExecContext(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

// A plugin the generation could not give a resource is reported with it, so
// the console shows the plugin as abnormal (audit 2026-10-09 P2-2).
func TestResourceConflictsInSummary(t *testing.T) {
	conflict := core.ResourceConflict{Resource: "platform", ID: "guardp", Reason: "platform id \"guardp\" is already registered by plugin \"other\""}
	h, root := newHarness(t, conflict)
	if code, out := h.do("POST", "/plugins/upload", "admin", fileBody(t, pkgtest.Build(pkgtest.Guard("guard", "0.1.0", "sub2api"), root))); code != 200 {
		t.Fatalf("upload = %d %v", code, out)
	}
	code, out := h.do("GET", "/plugins/guard", "admin", nil)
	rc, _ := data(out)["resource_conflicts"].([]any)
	if code != 200 || len(rc) != 1 || rc[0].(map[string]any)["id"] != "guardp" || rc[0].(map[string]any)["resource"] != "platform" {
		t.Fatalf("detail = %d %v", code, data(out)["resource_conflicts"])
	}
}

// Enable and upgrade check the version's resources again: a newer core may
// have reserved a path since it was approved (audit 2026-10-09 P2-2).
func TestEnableAndUpgradeRecheckResources(t *testing.T) {
	h, root := newHarness(t)
	ctx := context.Background()
	for _, v := range []string{"0.1.0", "0.2.0"} {
		if code, out := h.do("POST", "/plugins/upload", "admin", fileBody(t, pkgtest.Build(pkgtest.Platform("video", v, "sub2api"), root))); code != 200 {
			t.Fatalf("upload %s = %d %v", v, code, out)
		}
		if v != "0.1.0" {
			continue // an upgrade asking for nothing new is approved on upload
		}
		consent := install.ConsentRequest{Grants: []install.GrantInput{{Permission: "gateway.endpoint"}, {Permission: "platform.register"}, {Permission: "ui.native"}}}
		if code, out := h.do("POST", "/plugins/video/versions/"+v+"/consent", "admin", consent); code != 200 {
			t.Fatalf("consent %s = %d %v", v, code, out)
		}
	}
	var approved int
	if err := h.db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM plugin_versions WHERE plugin_key = 'video' AND consent_status = 'approved'`).Scan(&approved); err != nil || approved != 2 {
		t.Fatalf("approved versions = %d %v", approved, err)
	}
	// What a newer core reserving the path of the status endpoint looks like
	// to the stored manifests.
	if _, err := h.db.SQL.ExecContext(ctx, `UPDATE plugin_versions SET manifest = jsonb_set(manifest, '{platforms,0,endpoints,1,path}', '"/dashboard/video"')
		WHERE plugin_key = 'video'`); err != nil {
		t.Fatal(err)
	}
	for _, call := range []struct {
		path string
		body any
	}{{"/plugins/video/enable", nil}, {"/plugins/video/upgrade", map[string]any{"version": "0.2.0"}}} {
		code, out := h.do("POST", call.path, "admin", call.body)
		e, _ := out["error"].(map[string]any)
		if code != http.StatusConflict || e["code"] != "plugin_resource_conflict" {
			t.Fatalf("%s = %d %v", call.path, code, out)
		}
	}
}

// An installed package from before code-only forms (DESIGN-2026-11-19-
// PLUGIN-UI-CODE-ONLY 7.1) keeps working: its settings in mode schema, or
// native with the schema and no secrets key (moderation's old manifest), are
// read as nothing saying which settings are secret, so every one is masked;
// no schema is served, none is checked, and masks keep the stored values.
func TestOldSchemaSettingsPackage(t *testing.T) {
	for name, settings := range map[string]string{
		"schema":        `{"mode":"schema","schema":"forms/settings.schema.json","uiSchema":"forms/settings.ui.json"}`,
		"native+schema": `{"mode":"native","page":"dashboard","schema":"forms/settings.schema.json"}`,
	} {
		t.Run(name, func(t *testing.T) {
			h, root := newHarness(t)
			installNativeSettings(t, h, root, guardSecrets)
			code, out := h.do("PUT", "/plugins/guard/settings", "admin", map[string]any{"values": map[string]any{"threshold": 3, "webhook_secret": "s3cret"}})
			require.Equal(t, http.StatusOK, code, out)
			_, err := h.db.SQL.ExecContext(context.Background(),
				`UPDATE plugin_versions SET manifest = jsonb_set(manifest, '{ui,settings}', $1::jsonb) WHERE plugin_key = 'guard'`, settings)
			require.NoError(t, err)

			code, out = h.do("GET", "/plugins/guard/settings", "admin", nil)
			require.Equal(t, http.StatusOK, code, out)
			assert.Equal(t, map[string]any{"threshold": Mask, "webhook_secret": Mask}, data(out)["values"])
			assert.Equal(t, []any{"threshold", "webhook_secret"}, data(out)["secret_fields"])
			assert.NotContains(t, data(out), "schema")
			assert.NotContains(t, data(out), "ui_schema")

			code, out = h.do("PUT", "/plugins/guard/settings", "admin", map[string]any{"values": map[string]any{"threshold": Mask, "webhook_secret": Mask, "note": "n"}})
			require.Equal(t, http.StatusOK, code, out)
			assert.JSONEq(t, `{"threshold":3,"webhook_secret":"s3cret","note":"n"}`, h.storedSettings())
		})
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

// A plugin without ui.settings may still hold stored values: nothing says
// which are secret, so every top-level value is masked, and a mask sent back
// keeps the stored value.
func TestSettingsWithoutFormAreAllMasked(t *testing.T) {
	h, root := newHarness(t)
	guard := pkgtest.Guard("guard", "0.1.0", "sub2api")
	guard.UI.Settings = nil
	code, out := h.do("POST", "/plugins/upload", "admin", fileBody(t, pkgtest.Build(guard, root)))
	require.Equal(t, http.StatusOK, code, out)
	consent := install.ConsentRequest{Grants: []install.GrantInput{{Permission: "db.schema"}, {Permission: "gateway.hook"},
		{Permission: "events"}, {Permission: "jobs"}, {Permission: "routes.admin"}, {Permission: "ui.menu"}, {Permission: "ui.native"}}}
	code, out = h.do("POST", "/plugins/guard/versions/0.1.0/consent", "admin", consent)
	require.Equal(t, http.StatusOK, code, out)

	code, out = h.do("PUT", "/plugins/guard/settings", "admin", map[string]any{"values": map[string]any{
		"api_key": "k-1", "upstream": map[string]any{"url": "https://u.example", "token": "t-1"}, "note": ""}})
	require.Equal(t, http.StatusOK, code, out)
	masked := map[string]any{"api_key": Mask, "upstream": Mask, "note": ""}
	assert.Equal(t, masked, data(out)["values"])
	assert.ElementsMatch(t, []any{"api_key", "note", "upstream"}, data(out)["secret_fields"])

	code, out = h.do("GET", "/plugins/guard/settings", "admin", nil)
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, "none", data(out)["mode"])
	assert.Equal(t, masked, data(out)["values"])
	assert.NotContains(t, string(mustJSON(t, out)), "k-1")
	assert.NotContains(t, string(mustJSON(t, out)), "t-1")

	// Masks keep the stored values; a new value replaces its own key only.
	code, out = h.do("PUT", "/plugins/guard/settings", "admin", map[string]any{"values": map[string]any{
		"api_key": Mask, "upstream": Mask, "note": "n"}})
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, map[string]any{"api_key": Mask, "upstream": Mask, "note": Mask}, data(out)["values"])
	var enc []byte
	require.NoError(t, h.db.SQL.QueryRowContext(context.Background(), `SELECT config_enc FROM plugins WHERE key = 'guard'`).Scan(&enc))
	cipher, _ := secret.New(bytes.Repeat([]byte{7}, 32))
	plain, err := cipher.Decrypt(enc, ConfigAAD("guard"))
	require.NoError(t, err)
	assert.JSONEq(t, `{"api_key":"k-1","upstream":{"url":"https://u.example","token":"t-1"},"note":"n"}`, string(plain))
}

// The plugin page lists the role grants an automatic upgrade withheld
// (role_grants_to_review); plugin:install rejects (or confirms) them, which
// clears the list.
func TestAPIRoleGrantsToReview(t *testing.T) {
	h, root := newHarness(t)
	ctx := context.Background()
	if code, out := h.do("POST", "/plugins/upload", "admin", fileBody(t, pkgtest.Build(pkgtest.Guard("guard", "0.1.0", "sub2api"), root))); code != 200 {
		t.Fatalf("upload = %d %v", code, out)
	}
	if _, err := h.db.SQL.ExecContext(ctx, `
INSERT INTO roles (key, name, builtin) VALUES ('user', '{}', true) ON CONFLICT (key) DO NOTHING;
INSERT INTO permissions (key, module, label, source, plugin_key, status) VALUES ('plugin.guard:rules:export', 'plugin.guard', '{}', 'plugin', 'guard', 'active');
INSERT INTO audit_logs (action, target_type, target_id, detail) VALUES ('plugin.consent.auto', 'plugin', 'guard',
  '{"version":"0.1.0","granted":[],"pending":[{"permission":"plugin.guard:rules:export","role":"user","sensitive":false,"via":"default","needs_consent":true}],"review_required":true}')`); err != nil {
		t.Fatal(err)
	}
	pending := func() []any {
		t.Helper()
		code, out := h.do("GET", "/plugins/guard", "admin", nil)
		list, ok := data(out)["role_grants_to_review"].([]any)
		if code != 200 || !ok {
			t.Fatalf("get = %d %v", code, out)
		}
		return list
	}
	if got := pending(); len(got) != 1 || got[0].(map[string]any)["role"] != "user" || got[0].(map[string]any)["version"] != "0.1.0" {
		t.Fatalf("to review %v", got)
	}
	if code, _ := h.do("POST", "/plugins/guard/versions/0.1.0/role-grants/reject", "viewer", nil); code != http.StatusForbidden {
		t.Fatalf("viewer reject = %d", code)
	}
	if code, out := h.do("POST", "/plugins/guard/versions/0.1.0/role-grants/reject", "admin", nil); code != 200 || data(out)["confirmed"] != false {
		t.Fatalf("reject = %d %v", code, out)
	}
	if got := pending(); len(got) != 0 {
		t.Fatalf("after reject %v", got)
	}
	if code, _ := h.do("POST", "/plugins/guard/versions/0.1.0/role-grants/confirm", "admin", nil); code != http.StatusNotFound {
		t.Fatalf("confirm with nothing pending = %d", code)
	}
}

// PUT /plugins/:key/grants/:permission spends the consent's step-up and
// registers the grant rights its handler checks (install.SetGrant).
func TestSetGrantRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := httpapi.NewRouter(gin.New(), tokens{}, authz{})
	New(Deps{}).RegisterRoutes(r)
	i := slices.IndexFunc(r.Routes(), func(s httpapi.RouteSpec) bool { return s.ID() == "PUT /plugins/:key/grants/:permission" })
	require.GreaterOrEqual(t, i, 0)
	spec := r.Routes()[i]
	assert.Equal(t, core.StepUpPluginGrant, spec.StepUp)
	assert.Equal(t, []perm.Key{PermManage}, spec.Keys)
	assert.ElementsMatch(t, []perm.Key{perm.PluginInstall, perm.PluginGrantHigh, perm.PluginGrantCritical, perm.PluginGrantDBSchemaUnconfined}, spec.Extra)
}

// plugin:manage narrows a grant, also in a chunked body (no
// Content-Length); granting a denied or revoked permission again takes
// plugin:install, even a low-risk one, and plugin:grant:high for a
// high-risk one; an empty body grants the requested scope.
func TestAPISetGrant(t *testing.T) {
	h, root := newHarness(t)
	ctx := context.Background()
	if _, err := h.db.SQL.ExecContext(ctx, `INSERT INTO users (email, password_hash) VALUES ('nobody@x', 'x'), ('manager@x', 'x')`); err != nil {
		t.Fatal(err)
	}
	if code, out := h.do("POST", "/plugins/upload", "admin", fileBody(t, pkgtest.Build(pkgtest.Guard("guard", "0.1.0", "sub2api"), root))); code != 200 {
		t.Fatalf("upload = %d %v", code, out)
	}
	consent := install.ConsentRequest{Grants: []install.GrantInput{{Permission: "db.schema"}, {Permission: "gateway.hook"},
		{Permission: "events"}, {Permission: "jobs"}, {Permission: "routes.admin"}, {Permission: "ui.menu"}, {Permission: "ui.native"}}}
	if code, out := h.do("POST", "/plugins/guard/versions/0.1.0/consent", "admin", consent); code != 200 {
		t.Fatalf("consent = %d %v", code, out)
	}

	narrower := map[string]any{"scope": map[string]any{"points": []any{"gateway.request"}, "fields": []any{"model"}}}
	raw, _ := json.Marshal(narrower)
	req := httptest.NewRequest("PUT", "/api/v1/plugins/guard/grants/gateway.hook", bytes.NewReader(raw))
	req.ContentLength = -1
	req.Header.Set("Authorization", "Bearer manager")
	w := httptest.NewRecorder()
	h.engine.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code, w.Body.String())
	out := map[string]any{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, "granted", data(out)["status"])
	assert.Equal(t, narrower["scope"], data(out)["scope"])
	code, out := h.do("PUT", "/plugins/guard/grants/gateway.hook", "manager", map[string]any{"scope": "x"})
	require.Equal(t, http.StatusBadRequest, code, out)

	code, out = h.do("PUT", "/plugins/guard/grants/net", "manager", nil)
	require.Equal(t, http.StatusForbidden, code, out)
	code, _ = h.do("DELETE", "/plugins/guard/grants/events", "manager", nil)
	require.Equal(t, http.StatusNoContent, code)
	code, out = h.do("PUT", "/plugins/guard/grants/events", "manager", nil)
	require.Equal(t, http.StatusForbidden, code, out)
	assert.Contains(t, fmt.Sprint(out["error"]), "plugin:install")
	code, out = h.do("PUT", "/plugins/guard/grants/net", "admin", nil)
	require.Equal(t, 200, code, out)
	assert.Equal(t, "granted", data(out)["status"])
	assert.Equal(t, map[string]any{"domains": []any{"hooks.example.com"}}, data(out)["scope"])
}
