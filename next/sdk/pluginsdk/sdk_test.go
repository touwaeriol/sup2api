package pluginsdk_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

const testManifest = `{
  "apiVersion": 1, "key": "demo", "version": "1.2.3", "publisher": "test", "runtime": "grpc",
  "capabilities": [{"id": "http.routes.v1"}, {"id": "app.jobs.v1"}]
}`

type demo struct {
	*pluginsdk.Router
	host pluginsdk.Host
	cfg  struct {
		Name string `json:"name"`
	}
	grants    []pluginsdk.Grant
	shutdowns int
}

func newDemo() *demo {
	d := &demo{Router: pluginsdk.NewRouter()}
	d.Handle("GET", "/hello", func(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
		d.host.Logger().Info("hello called", "user", req.GetCaller().GetUserId())
		return pluginsdk.DataResponse(map[string]string{"hello": d.cfg.Name, "q": pluginsdk.Query(req, "q")}), nil
	})
	d.Handle("POST", "/kv", func(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
		if err := d.host.KV().Set(ctx, "ns", "k", req.GetBody(), time.Minute); err != nil {
			return nil, err
		}
		v, ok, err := d.host.KV().Get(ctx, "ns", "k")
		if err != nil || !ok {
			return nil, errors.New("kv get failed")
		}
		return pluginsdk.DataResponse(string(v)), nil
	})
	d.Handle("GET", "/panic", func(context.Context, *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
		panic("boom")
	})
	return d
}

func (d *demo) Init(_ context.Context, h pluginsdk.Host) error { d.host = h; return nil }

func (d *demo) Configure(_ context.Context, cfg pluginsdk.Config) error {
	if err := cfg.Decode(&d.cfg); err != nil {
		return err
	}
	if d.cfg.Name == "bad" {
		return pluginsdk.FieldErrors{}.Add("name", "invalid", "bad name").Err()
	}
	d.grants = cfg.Grants
	return nil
}

func (d *demo) RunJob(_ context.Context, in *pluginv1.RunJobRequest) (*pluginv1.RunJobResponse, error) {
	return &pluginv1.RunJobResponse{Message: "ran " + in.GetJobId()}, nil
}

func (d *demo) Shutdown(context.Context) error { d.shutdowns++; return nil }

func TestHarnessLifecycle(t *testing.T) {
	d := newDemo()
	h := pluginsdktest.Start(t, d, pluginsdktest.Options{
		Config:   map[string]string{"name": "world"},
		Grants:   []*pluginv1.Grant{{Permission: "kv", ScopeJson: ""}},
		Manifest: []byte(testManifest),
	})
	if h.Info.GetPluginKey() != "demo" || h.Info.GetVersion() != "1.2.3" {
		t.Fatalf("info = %v", h.Info)
	}
	caps := h.Info.GetCapabilities()
	if len(caps) != 2 || caps[0] != manifest.CapAppJobs || caps[1] != manifest.CapHTTPRoutes {
		t.Fatalf("capabilities = %v", caps)
	}
	if len(d.grants) != 1 || string(d.grants[0].Scope) != "{}" {
		t.Fatalf("grants = %+v", d.grants)
	}

	resp := h.Do("GET", "/hello", map[string]string{"q": "x"}, nil)
	if resp.GetStatus() != http.StatusOK || string(resp.GetBody()) != `{"data":{"hello":"world","q":"x"}}` {
		t.Fatalf("hello = %d %s", resp.GetStatus(), resp.GetBody())
	}
	logs := h.Host.Logs()
	if len(logs) != 1 || logs[0].Message != "hello called" || logs[0].Attrs["user"] != "1" || logs[0].Level != pluginv1.LogRequest_LEVEL_INFO {
		t.Fatalf("logs = %+v", logs)
	}

	resp = h.Do("POST", "/kv", nil, "value-1")
	if string(resp.GetBody()) != `{"data":"value-1"}` {
		t.Fatalf("kv = %s", resp.GetBody())
	}

	resp = h.Do("GET", "/missing", nil, nil)
	if resp.GetStatus() != http.StatusNotFound {
		t.Fatalf("missing = %d", resp.GetStatus())
	}

	if _, err := h.HTTP.HandleHTTP(context.Background(), &pluginv1.HTTPRequest{Method: "GET", RoutePath: "/panic"}); err == nil {
		t.Fatal("panic should become an error")
	}

	job, err := h.App.RunJob(context.Background(), &pluginv1.RunJobRequest{JobId: "j1"})
	if err != nil || job.GetMessage() != "ran j1" {
		t.Fatalf("job = %v %v", job, err)
	}
	if _, err := h.App.OnEvents(context.Background(), &pluginv1.OnEventsRequest{}); err == nil {
		t.Fatal("OnEvents should be unimplemented")
	}

	if errs := h.Configure(map[string]string{"name": "bad"}, nil); len(errs) != 1 || errs[0].GetField() != "name" {
		t.Fatalf("configure errors = %v", errs)
	}

	health, err := h.Plugin.Health(context.Background(), &pluginv1.HealthRequest{})
	if err != nil || !health.GetHealthy() {
		t.Fatalf("health = %v %v", health, err)
	}
}

func TestWithInfoOverridesManifest(t *testing.T) {
	h := pluginsdktest.Start(t, newDemo(), pluginsdktest.Options{
		Manifest: []byte(testManifest), SDK: []pluginsdk.Option{pluginsdk.WithInfo("demo", "2.0.0")},
	})
	if h.Info.GetVersion() != "2.0.0" {
		t.Fatalf("version = %s", h.Info.GetVersion())
	}
}

func TestLoggerGroups(t *testing.T) {
	d := newDemo()
	h := pluginsdktest.Start(t, d, pluginsdktest.Options{SDK: []pluginsdk.Option{pluginsdk.WithInfo("demo", "1.0.0")}})
	d.host.Logger().With("a", 1).WithGroup("g").Warn("msg", "b", 2, slog.Group("sub", "c", 3))
	logs := h.Host.Logs()
	if len(logs) != 1 {
		t.Fatalf("logs = %+v", logs)
	}
	got := logs[0].Attrs
	if got["a"] != "1" || got["g.b"] != "2" || got["g.sub.c"] != "3" || logs[0].Level != pluginv1.LogRequest_LEVEL_WARN {
		t.Fatalf("attrs = %v", got)
	}
}

func TestLedgerAndAuthz(t *testing.T) {
	d := newDemo()
	fh := pluginsdktest.NewFakeHost()
	fh.Authz = func(uid int64, p string) bool { return uid == 7 && p == "rules:manage" }
	pluginsdktest.Start(t, d, pluginsdktest.Options{Host: fh, SDK: []pluginsdk.Option{pluginsdk.WithInfo("demo", "1.0.0")}})
	ctx := context.Background()
	ok, err := d.host.AuthzCheck(ctx, 7, "rules:manage")
	if err != nil || !ok {
		t.Fatalf("authz = %v %v", ok, err)
	}
	ok, _ = d.host.AuthzCheck(ctx, 8, "rules:manage")
	if ok {
		t.Fatal("user 8 should be denied")
	}
	r1, err := d.host.LedgerCredit(ctx, pluginsdk.LedgerChange{UserID: 1, Amount: "1.5", IdempotencyKey: "k1"})
	if err != nil || r1.Duplicate {
		t.Fatalf("credit = %+v %v", r1, err)
	}
	r2, err := d.host.LedgerCredit(ctx, pluginsdk.LedgerChange{UserID: 1, Amount: "1.5", IdempotencyKey: "k1"})
	if err != nil || !r2.Duplicate || r2.LedgerID != r1.LedgerID {
		t.Fatalf("duplicate credit = %+v %v", r2, err)
	}
	if _, err := d.host.DB(ctx); err == nil {
		t.Fatal("DB without DSN should fail")
	}
}

func TestRegisterRequiresInfo(t *testing.T) {
	if err := pluginsdk.Register(nil, newDemo(), nil); err == nil {
		t.Fatal("expected error without key/version")
	}
}

// validating is demo with settings validation: the name must not be "bad",
// and "trim" is stored trimmed.
type validating struct{ *demo }

func (v validating) ValidateSettings(_ context.Context, cfg pluginsdk.Config) (json.RawMessage, error) {
	var in struct {
		Name string `json:"name"`
	}
	if err := cfg.Decode(&in); err != nil {
		return nil, err
	}
	switch in.Name {
	case "bad":
		return nil, pluginsdk.FieldErrors{}.Add("name", "invalid", "bad name").Err()
	case " trim ":
		return json.RawMessage(`{"name":"trim"}`), nil
	case "grants":
		if len(cfg.Grants) != 1 || string(cfg.Grants[0].Scope) != `{"domains":["a.test"]}` {
			return nil, fmt.Errorf("grants = %v", cfg.Grants)
		}
		return json.RawMessage(`{"name":"` + cfg.Grants[0].Permission + `"}`), nil
	}
	return nil, nil
}

func TestValidateSettings(t *testing.T) {
	h := pluginsdktest.Start(t, validating{newDemo()}, pluginsdktest.Options{Manifest: []byte(testManifest)})
	resp, err := h.ValidateSettings(map[string]string{"name": "bad"}, nil)
	if err != nil || len(resp.GetErrors()) != 1 || resp.GetErrors()[0].GetField() != "name" {
		t.Fatalf("field errors = %v %v", resp, err)
	}
	if resp, err = h.ValidateSettings(`not json`, nil); err != nil || len(resp.GetErrors()) != 1 || resp.GetErrors()[0].GetField() != "" {
		t.Fatalf("other errors reject the whole object = %v %v", resp, err)
	}
	if resp, err = h.ValidateSettings(map[string]string{"name": " trim "}, nil); err != nil || resp.GetNormalizedConfigJson() != `{"name":"trim"}` {
		t.Fatalf("normalized = %v %v", resp, err)
	}
	if resp, err = h.ValidateSettings(nil, nil); err != nil || len(resp.GetErrors()) != 0 || resp.GetNormalizedConfigJson() != "" {
		t.Fatalf("accepted = %v %v", resp, err)
	}
	// The grants reach the plugin as Configure gets them.
	grants := []*pluginv1.Grant{{Permission: "net", ScopeJson: `{"domains":["a.test"]}`}}
	if resp, err = h.ValidateSettings(map[string]string{"name": "grants"}, grants); err != nil || resp.GetNormalizedConfigJson() != `{"name":"net"}` {
		t.Fatalf("grants = %v %v", resp, err)
	}

	// Without SettingsValidator the call is UNIMPLEMENTED: the host stores
	// the settings as they are.
	plain := pluginsdktest.Start(t, newDemo(), pluginsdktest.Options{Manifest: []byte(testManifest)})
	if _, err := plain.ValidateSettings(nil, nil); status.Code(err) != codes.Unimplemented {
		t.Fatalf("without a validator = %v", err)
	}
}
