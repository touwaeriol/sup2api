package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/grpcruntime"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg/pkgtest"
	pluginregistry "github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry/registrytest"
	pluginrollout "github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/rollout"
)

// reportingNodes is one live node reporting the given plugin states.
type reportingNodes struct {
	nodes
	host   string
	states map[string]string
}

func (n reportingNodes) LiveNodes(context.Context) ([]core.NodeStatus, error) {
	return []core.NodeStatus{{NodeID: "node-1", BootID: "boot-1", HostVersion: n.host, Plugins: n.states}}, nil
}

// An upload of a package built for an older plugin contract answers the
// runtime's one message: runtime "none", or a removed account grant
// (CONTRACTS §124).
func TestUploadAnswersOutdatedContract(t *testing.T) {
	h, root := newHarness(t)
	for name, outdated := range map[string]func(m *manifest.Manifest){
		"runtime none": func(m *manifest.Manifest) { m.Runtime = "none" },
		"accounts.credentials": func(m *manifest.Manifest) {
			m.HostPermissions = append(m.HostPermissions, manifest.HostPermission{ID: "accounts.credentials", Scope: map[string]any{"types": "own"}})
		},
		"accounts.read": func(m *manifest.Manifest) {
			m.HostPermissions = append(m.HostPermissions, manifest.HostPermission{ID: "accounts.read"})
		},
	} {
		m := pkgtest.Platform("video", "0.1.0", "sub2api")
		outdated(m)
		code, out := h.do("POST", "/plugins/upload", "admin", fileBody(t, pkgtest.Build(m, root)))
		msg, _ := out["error"].(map[string]any)["message"].(string)
		if want := protocol.OutdatedContractMessage("video", "0.1.0", testHostVersion); code != http.StatusBadRequest || msg != want {
			t.Fatalf("%s: upload = %d %v, want the message %q", name, code, out, want)
		}
	}
}

// The review of a plugin declaring account types carries the fixed
// statement that it receives every request in full; other plugins' do not.
func TestReviewStatesRequestAccess(t *testing.T) {
	h, root := newHarness(t)
	code, out := h.do("POST", "/plugins/upload", "admin", fileBody(t, pkgtest.Build(pkgtest.Platform("video", "0.1.0", "sub2api"), root)))
	if code != http.StatusOK {
		t.Fatalf("upload = %d %v", code, out)
	}
	code, out = h.do("GET", "/plugins/video/versions/0.1.0/review", "admin", nil)
	statement, _ := data(out)["request_access"].(map[string]any)
	if code != http.StatusOK || statement["en"] == "" || statement["zh"] == "" || len(statement) != 2 {
		t.Fatalf("review = %d, request_access %v", code, data(out)["request_access"])
	}

	code, out = h.do("POST", "/plugins/upload", "admin", fileBody(t, pkgtest.Build(pkgtest.Guard("guard", "0.1.0", "sub2api"), root)))
	if _, ok := data(out)["request_access"]; code != http.StatusOK || ok {
		t.Fatalf("guard upload = %d, request_access %v", code, data(out)["request_access"])
	}
}

// The plugin list and the plugin show why a node refuses to load the
// plugin when it was built for the older plugin contract, as the node
// recorded it by code, and nothing for another load failure, whatever its
// text.
func TestListShowsOutdatedContractRefusal(t *testing.T) {
	h, root := newHarness(t)
	for _, key := range []string{"video", "other"} {
		if code, out := h.do("POST", "/plugins/upload", "admin", fileBody(t, pkgtest.Build(pkgtest.Platform(key, "0.1.0", "sub2api"), root))); code != http.StatusOK {
			t.Fatalf("upload %s = %d %v", key, code, out)
		}
	}
	msg := "load video@0.1.0: " + protocol.OutdatedContractMessage("video", "0.1.0", testHostVersion)
	state := func(err, code string) string {
		b, _ := json.Marshal(map[string]any{"state": "failed", "instances": []map[string]any{{"version": "0.1.0", "state": "failed", "error": err, "code": code}}})
		return string(b)
	}
	h.api.d.Nodes = reportingNodes{host: testHostVersion, states: map[string]string{
		"video": state(msg, check.CodeOutdatedContract),
		"other": state(protocol.OutdatedContractMessage("other", "0.1.0", testHostVersion), ""),
	}}

	want := map[string]any{"code": check.CodeOutdatedContract, "version": "0.1.0", "host_version": testHostVersion,
		"protocol_version": float64(protocol.ProtocolVersion), "first_core_version": protocol.FirstCoreVersion, "message": msg}
	if got := listRefusals(h); !equalJSON(got["video"], want) || got["other"] != nil {
		t.Fatalf("list refusals = %v", got)
	}
	_, out := h.do("GET", "/plugins/video", "admin", nil)
	if got, _ := data(out)["refusal"].(map[string]any); !equalJSON(got, want) {
		t.Fatalf("plugin refusal = %v", data(out)["refusal"])
	}
}

// A load the runtime refuses for the older plugin contract reaches the
// plugin list as a refusal through what the rollout records on the node,
// however the error is wrapped and however long it is.
func TestRefusalFromRolloutRecording(t *testing.T) {
	h, root := newHarness(t)
	ctx := context.Background()
	if code, out := h.do("POST", "/plugins/upload", "admin", fileBody(t, pkgtest.Build(pkgtest.Platform("video", "0.1.0", "sub2api"), root))); code != http.StatusOK {
		t.Fatalf("upload = %d %v", code, out)
	}
	if _, err := h.db.SQL.ExecContext(ctx, `UPDATE plugins SET status = 'enabled', active_version = '0.1.0' WHERE key = 'video'`); err != nil {
		t.Fatal(err)
	}
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	node := &registrytest.Node{RDB: rdb, ID: "node-1", Boot: "boot-1"}
	pkgs := pluginregistry.NewPackages(h.db, t.TempDir(), pluginregistry.WithSource(registrytest.Source()))
	t.Cleanup(pkgs.Close)
	refused := fmt.Errorf("start video@0.1.0 (%s): %w", strings.Repeat("x", 600), grpcruntime.ErrOutdatedContract)
	ctl, err := pluginrollout.New(pluginrollout.Options{DB: h.db, Node: node, Bus: registrytest.Bus{RDB: rdb}, Packages: pkgs,
		Registry: pluginregistry.New(), Runtime: refusingRuntime{refused}})
	if err != nil {
		t.Fatal(err)
	}
	ctl.Reconcile(ctx)
	h.api.d.Nodes = node

	got := listRefusals(h)["video"]
	if got == nil || got["code"] != check.CodeOutdatedContract || got["version"] != "0.1.0" ||
		!strings.HasPrefix(got["message"].(string), "start video@0.1.0") {
		t.Fatalf("refusal = %v", got)
	}
}

// A package refused for the older plugin contract on its first enable fails
// the rollout in preparation, after which no node reports the failed
// instance any more: the plugin list still shows the refusal, from the code
// the rollout kept with the node's outcome.
func TestRefusalAfterFailedFirstEnable(t *testing.T) {
	h, root := newHarness(t)
	ctx := context.Background()
	if code, out := h.do("POST", "/plugins/upload", "admin", fileBody(t, pkgtest.Build(pkgtest.Platform("video", "0.1.0", "sub2api"), root))); code != http.StatusOK {
		t.Fatalf("upload = %d %v", code, out)
	}
	if _, err := h.db.SQL.ExecContext(ctx, `UPDATE plugin_versions SET consent_status = 'approved' WHERE plugin_key = 'video'; UPDATE plugins SET status = 'installed' WHERE key = 'video'`); err != nil {
		t.Fatal(err)
	}
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	node := &registrytest.Node{RDB: rdb, ID: "node-1", Boot: "boot-1"}
	node.Heartbeat(ctx)
	pkgs := pluginregistry.NewPackages(h.db, t.TempDir(), pluginregistry.WithSource(registrytest.Source()))
	t.Cleanup(pkgs.Close)
	refused := fmt.Errorf("start video@0.1.0: %w", grpcruntime.ErrOutdatedContract)
	ctl, err := pluginrollout.New(pluginrollout.Options{DB: h.db, Node: node, Bus: registrytest.Bus{RDB: rdb}, Packages: pkgs,
		Registry: pluginregistry.New(), Runtime: refusingRuntime{refused},
		ReconcileInterval: 100 * time.Millisecond, CoordinatorTick: 50 * time.Millisecond, LoadRetry: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ctl.Start(ctx)
	t.Cleanup(func() { ctl.Stop(context.Background()) })
	h.api.d.Nodes = node

	r, err := ctl.Enable(ctx, "video", 0)
	if err != nil {
		t.Fatal(err)
	}
	var phase, reason string
	waitUntil(t, "the rollout fails and the node drops the failed instance", func() bool {
		if err := h.db.SQL.QueryRowContext(ctx, `SELECT r.phase, p.status_reason FROM plugin_rollouts r JOIN plugins p ON p.key = r.plugin_key WHERE r.id = $1`, r.ID).
			Scan(&phase, &reason); err != nil {
			t.Fatal(err)
		}
		live, err := node.LiveNodes(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return phase == pluginrollout.PhaseFailed && !strings.Contains(live[0].Plugins["video"], check.CodeOutdatedContract)
	})
	if !strings.Contains(reason, "start video@0.1.0") {
		t.Fatalf("status_reason = %q", reason)
	}
	got := listRefusals(h)["video"]
	if got == nil || got["code"] != check.CodeOutdatedContract || got["version"] != "0.1.0" || got["host_version"] != testHostVersion ||
		got["message"] != refused.Error() {
		t.Fatalf("refusal after a failed first enable = %v", got)
	}
	_, out := h.do("GET", "/plugins/video", "admin", nil)
	if d, _ := data(out)["refusal"].(map[string]any); !equalJSON(d, got) {
		t.Fatalf("plugin refusal = %v", data(out)["refusal"])
	}
}

func waitUntil(t *testing.T, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !fn() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// refusingRuntime fails every load with err.
type refusingRuntime struct{ err error }

func (r refusingRuntime) Load(context.Context, *pluginregistry.Package) (pluginrollout.Instance, error) {
	return nil, r.err
}

// listRefusals is the refusal of every plugin in the list, by key.
func listRefusals(h *harness) map[string]map[string]any {
	_, out := h.do("GET", "/plugins", "admin", nil)
	list, _ := out["data"].([]any)
	refusals := map[string]map[string]any{}
	for _, it := range list {
		item := it.(map[string]any)
		r, _ := item["refusal"].(map[string]any)
		refusals[item["key"].(string)] = r
	}
	return refusals
}

func equalJSON(a, b map[string]any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// The plugin list reads the refusals its rollouts recorded with one query
// for the whole page, however many plugins it shows.
func TestListReadsRecordedRefusalsOnce(t *testing.T) {
	h, root := newHarness(t)
	for _, key := range []string{"video", "audio", "image"} {
		if code, out := h.do("POST", "/plugins/upload", "admin", fileBody(t, pkgtest.Build(pkgtest.Platform(key, "0.1.0", "sub2api"), root))); code != http.StatusOK {
			t.Fatalf("upload %s = %d %v", key, code, out)
		}
	}
	var reads atomic.Int32
	count := func(db *gorm.DB) {
		if strings.Contains(db.Statement.SQL.String(), "plugin_rollout_nodes") {
			reads.Add(1)
		}
	}
	if err := h.db.Gorm.Callback().Row().After("gorm:row").Register("test:count_refusal_reads", count); err != nil {
		t.Fatal(err)
	}
	if got := listRefusals(h); len(got) != 3 {
		t.Fatalf("list = %v", got)
	}
	if n := reads.Load(); n != 1 {
		t.Fatalf("the list read the recorded refusals in %d queries, want 1", n)
	}
}
