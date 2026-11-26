package grpcruntime_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/grpcruntime"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry/registrytest"
)

// These tests run the test plugin as a real process, so the execution token,
// both gRPC directions and their message limits are the real ones. The
// plugin's side is scripted in the request body (testdata/testplugin).

type scriptStep struct {
	Op       string `json:"op"`
	Wait     bool   `json:"wait,omitempty"`
	URL      string `json:"url,omitempty"`
	BodySize int    `json:"body_size,omitempty"`
	Ms       int    `json:"ms,omitempty"`
}

type stepResult struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	Status      int32  `json:"status"`
	BodyBytes   int    `json:"body_bytes"`
	Observation bool   `json:"observation"`
	Error       bool   `json:"error"`
}

func script(ret string, steps ...scriptStep) []byte {
	raw, _ := json.Marshal(map[string]any{"script": steps, "return": ret})
	return raw
}

// steps decodes the plugin's log of what the host answered.
func steps(t *testing.T, log string) []stepResult {
	t.Helper()
	var out []stepResult
	if err := json.Unmarshal([]byte(log), &out); err != nil {
		t.Fatalf("step log %q: %v", log, err)
	}
	return out
}

func relayStep(wait bool, size int) scriptStep {
	return scriptStep{Op: "relay", Wait: wait, URL: "https://upstream.example.com/v1/messages", BodySize: size}
}

func externalService(m *manifest.Manifest) { m.ExternalServices = []string{"api.example.com"} }

const mib = 1 << 20

func TestExecuteRelayWithoutWaitAcrossProcess(t *testing.T) {
	e := setup(t, registrytest.DefaultGrants())
	inst := e.load(t, e.runtime(t, nil))
	var checked, sent atomic.Int32
	client := core.ExecutionClient{
		Check: func(context.Context, *pluginv1.UpstreamRequest) error { checked.Add(1); return nil },
		Relay: func(context.Context, *pluginv1.UpstreamRequest) (*pluginv1.RelayUpstreamResponse, error) {
			sent.Add(1)
			return nil, errors.New("must not be called")
		},
	}
	// A body above the old 4 MiB message limit, in both directions.
	start := time.Now()
	res, err := execute(inst, &pluginv1.ExecuteRequest{Body: script("host_usage", relayStep(false, 5*mib), scriptStep{Op: "sleep", Ms: 200})}, client)
	if err != nil {
		t.Fatal(err)
	}
	log := steps(t, res.Response.GetClassification().GetReason())
	if len(log) != 1 || log[0].Code != "OK" || log[0].Observation || log[0].Error {
		t.Fatalf("relay answer: %+v", log)
	}
	if time.Since(start) < 200*time.Millisecond || checked.Load() != 1 || sent.Load() != 0 {
		t.Fatalf("after %s: checked %d, sent %d", time.Since(start), checked.Load(), sent.Load())
	}
	if res.Relay == nil || len(res.Relay.GetBody()) != 5*mib || res.Response.GetHostUsage() == nil {
		t.Fatalf("parked request: %d bytes, response %v", len(res.Relay.GetBody()), res.Response.GetReport())
	}

	// Parked, then anything but host_usage: nothing to send.
	for _, ret := range []string{"usage", "task", "none"} {
		res, err := execute(inst, &pluginv1.ExecuteRequest{Body: script(ret, relayStep(false, 1))}, client)
		if err != nil || res.Relay != nil {
			t.Fatalf("return %s: relay %v err %v", ret, res.Relay, err)
		}
	}
	if res, err := execute(inst, &pluginv1.ExecuteRequest{Body: script("error", relayStep(false, 1))}, client); err == nil || res.Relay != nil {
		t.Fatalf("failed Execute: relay %v err %v", res.Relay, err)
	}
	// The plugin dies after parking: plugin unavailable, nothing to send.
	res, err = execute(inst, &pluginv1.ExecuteRequest{Body: script("host_usage", relayStep(false, 1), scriptStep{Op: "exit"})}, client)
	if core.AsError(err).Code != core.ErrPluginUnavailable.Code || res.Relay != nil || sent.Load() != 0 {
		t.Fatalf("crash before the return: relay %v err %v sent %d", res.Relay, err, sent.Load())
	}
}

func TestExecuteRelayWaitAcrossProcess(t *testing.T) {
	e := setup(t, registrytest.DefaultGrants())
	inst := e.load(t, e.runtime(t, nil))
	client := core.ExecutionClient{Relay: func(_ context.Context, req *pluginv1.UpstreamRequest) (*pluginv1.RelayUpstreamResponse, error) {
		if req.GetUrl() == "" {
			return &pluginv1.RelayUpstreamResponse{Error: &pluginv1.ClassifyErrorRequest{Status: 429}}, nil
		}
		return &pluginv1.RelayUpstreamResponse{Observation: &pluginv1.ExtractUsageRequest{Status: 200, Body: make([]byte, 321)}}, nil
	}}
	res, err := execute(inst, &pluginv1.ExecuteRequest{ExtractUsage: true, Body: script("usage", relayStep(true, 16))}, client)
	if err != nil {
		t.Fatal(err)
	}
	if log := steps(t, res.Response.GetClassification().GetReason()); len(log) != 1 || log[0].Code != "OK" || !log[0].Observation || log[0].Error {
		t.Fatalf("relay answer: %+v", log)
	}
	if res.Relay != nil || res.Response.GetUsage().GetTokens().GetInputTokens() != 321 {
		t.Fatalf("usage from the observation: %v, relay %v", res.Response.GetUsage(), res.Relay)
	}
	// Without a flag the waiting form is refused and nothing is sent.
	res, err = execute(inst, &pluginv1.ExecuteRequest{Body: script("none", relayStep(true, 16))}, client)
	if err != nil {
		t.Fatal(err)
	}
	if log := steps(t, res.Response.GetClassification().GetReason()); log[0].Code != "FailedPrecondition" {
		t.Fatalf("wait without a flag: %+v", log)
	}
}

// The plugin process dies while its relay waits: the exchange is cut and
// Execute fails, so nothing is reported and nothing billed.
func TestExecutePluginCrashCutsWaitingRelay(t *testing.T) {
	e := setup(t, registrytest.DefaultGrants())
	inst := e.load(t, e.runtime(t, nil))
	started, cut := make(chan struct{}), make(chan struct{})
	client := core.ExecutionClient{Relay: func(ctx context.Context, _ *pluginv1.UpstreamRequest) (*pluginv1.RelayUpstreamResponse, error) {
		close(started)
		<-ctx.Done()
		close(cut)
		return nil, ctx.Err()
	}}
	done := make(chan error, 1)
	go func() {
		res, err := execute(inst, &pluginv1.ExecuteRequest{TaskSubmission: true, Body: script("task", relayStep(true, 1))}, client)
		if res.Response != nil || res.Relay != nil {
			err = errors.New("a crashed Execute left a result")
		}
		done <- err
	}()
	<-started
	inst.Kill()
	select {
	case <-cut:
	case <-time.After(10 * time.Second):
		t.Fatal("the exchange was not cut")
	}
	if err := <-done; core.AsError(err).Code != core.ErrPluginUnavailable.Code {
		t.Fatalf("execute: %v", err)
	}
}

func TestExecuteFetchAndBodiesAcrossProcess(t *testing.T) {
	e := setupPackage(t, registrytest.DefaultGrants(), registrytest.TestPlugin(t), externalService)
	inst := e.load(t, e.runtime(t, nil))
	var fetched atomic.Int32
	client := core.ExecutionClient{Fetch: func(_ context.Context, in *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
		fetched.Add(1)
		if len(in.GetBody()) > 0 {
			return &pluginv1.ExecutionHTTPResponse{Status: 200, Body: make([]byte, 20*mib)}, nil
		}
		return &pluginv1.ExecutionHTTPResponse{Status: 200, Body: []byte("ok")}, nil
	}}
	const ok = "https://api.example.com/v1/files"
	plan := []scriptStep{
		{Op: "fetch", URL: "https://elsewhere.example.org/x"}, // not in externalServices
		{Op: "fetch", URL: ok, BodySize: 3 * mib},             // no bound on a fetch's request body
		{Op: "fetch", URL: ok, BodySize: 2 * mib},             // answered with 20 MiB, whole
	}
	for range 9 { // no count limit: the ninth and later fetches go out too
		plan = append(plan, scriptStep{Op: "fetch", URL: ok})
	}
	plan = append(plan, relayStep(false, 3*mib+1)) // no bound on a relayed body
	res, err := execute(inst, &pluginv1.ExecuteRequest{Body: script("host_usage", plan...)}, client)
	if err != nil {
		t.Fatal(err)
	}
	log := steps(t, res.Response.GetClassification().GetReason())
	want := []string{"PermissionDenied", "OK", "OK", "OK", "OK", "OK", "OK", "OK", "OK", "OK", "OK", "OK", "OK"}
	if len(log) != len(want) {
		t.Fatalf("steps: %+v", log)
	}
	for n, code := range want {
		if log[n].Code != code {
			t.Errorf("step %d: %s (%s), want %s", n, log[n].Code, log[n].Message, code)
		}
	}
	if log[1].BodyBytes != 20*mib || log[2].BodyBytes != 20*mib || log[3].BodyBytes != 2 {
		t.Errorf("answers: %+v %+v %+v", log[1], log[2], log[3])
	}
	if fetched.Load() != 11 || len(res.Relay.GetBody()) != 3*mib+1 {
		t.Errorf("fetches sent %d, relayed %d bytes", fetched.Load(), len(res.Relay.GetBody()))
	}

	// The token of that call is void now.
	out, err := httpCall(t, inst, &pluginv1.HTTPRequest{Method: "POST", Path: "/replay", Body: []byte(ok)})
	if err != nil || out["code"] != "PermissionDenied" || fetched.Load() != 11 {
		t.Fatalf("expired token: %v %v, fetches %d", out, err, fetched.Load())
	}
}

func TestMonitorAcrossProcess(t *testing.T) {
	e := setupPackage(t, registrytest.DefaultGrants(), registrytest.TestPlugin(t), func(m *manifest.Manifest) {
		externalService(m)
		m.Capabilities = append(m.Capabilities, manifest.Capability{ID: manifest.CapPlatformMonitor})
	})
	inst := e.load(t, e.runtime(t, nil))
	monitor := func(body []byte) (*pluginv1.ReconcileResult, error) {
		return inst.Platform().Monitor(context.Background(), &pluginv1.PollRequest{Entry: &pluginv1.ReconcileEntry{RefId: string(body)}},
			func(context.Context, *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
				return &pluginv1.ExecutionHTTPResponse{Status: 200, Body: []byte(`{"status":"done"}`)}, nil
			})
	}
	query := scriptStep{Op: "fetch", URL: "https://api.example.com/tasks/1"}
	if r, err := monitor(script("settled", query)); err != nil || r.GetState() != pluginv1.ReconcileResult_SETTLED || steps(t, r.GetReason())[0].Status != 200 {
		t.Fatalf("settled: %v %v", r, err)
	}
	if r, err := monitor(script("pending")); err != nil || r.GetState() != pluginv1.ReconcileResult_PENDING {
		t.Fatalf("pending: %v %v", r, err)
	}
	if r, err := monitor(script("settled")); err == nil || r != nil {
		t.Fatalf("terminal without a fetch: %v %v", r, err)
	}
	if r, err := monitor(script("none", query)); err == nil || r != nil {
		t.Fatalf("no result: %v %v", r, err)
	}
}

// buildFixture builds the plugin binary of a testdata package with extra
// go build flags.
func buildFixture(pkg string, flags ...string) ([]byte, error) {
	_, file, _, _ := goruntime.Caller(0)
	dir, err := os.MkdirTemp("", "s2p-fixture-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "plugin")
	cmd := exec.Command("go", append(append([]string{"build"}, flags...), "-o", out, pkg)...)
	cmd.Dir = filepath.Dir(file)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("build %s %v: %w\n%s", pkg, flags, err, b)
	}
	return os.ReadFile(out)
}

// testPluginVariant builds the test plugin with ldflags, as a binary of
// another plugin protocol.
func testPluginVariant(t testing.TB, ldflags string) []byte {
	t.Helper()
	bin, err := buildFixture("./testdata/testplugin", "-ldflags", ldflags)
	if err != nil {
		t.Fatal(err)
	}
	return bin
}

// A package of the old plugin contract is refused with the one message,
// whatever gives it away.
func TestOutdatedContractIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name    string
		bin     func(testing.TB) []byte
		mod     func(*manifest.Manifest)
		started bool
	}{
		{"handshake of protocol 1", func(t testing.TB) []byte { return testPluginVariant(t, "-X main.handshakeProtocol=1") }, nil, true},
		{"GetInfo reports protocol 1", func(t testing.TB) []byte { return testPluginVariant(t, "-X main.infoProtocol=1") }, nil, true},
		{"manifest declares platform.execute.v1", registrytest.TestPlugin, func(m *manifest.Manifest) {
			m.Capabilities = append(m.Capabilities, manifest.Capability{ID: "platform.execute.v1"})
		}, false},
		{"manifest of runtime none", registrytest.TestPlugin, func(m *manifest.Manifest) { m.Runtime = "none" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := setupPackage(t, registrytest.DefaultGrants(), tc.bin(t), tc.mod)
			pkg, err := e.pkgs.Open(context.Background(), e.key, "1.0.0")
			if err != nil {
				t.Fatal(err)
			}
			inst, err := e.runtime(t, nil).Load(context.Background(), pkg)
			if err == nil {
				inst.Stop()
				t.Fatal("loaded")
			}
			if want := protocol.OutdatedContractMessage(e.key, "1.0.0", "0.1.0-test"); err.Error() != want || !errors.Is(err, grpcruntime.ErrOutdatedContract) {
				t.Fatalf("refusal:\n got %q\nwant %q", err, want)
			}
			if started := e.launcher.Commands > 0; started != tc.started {
				t.Fatalf("process started: %v", started)
			}
		})
	}
}

// The balance calls cross the process as they are: every request of the
// query and every answer, in order; a plugin without them answers
// Unimplemented, which the account module reads through the runtime's error.
func TestBalanceAcrossProcess(t *testing.T) {
	e := setupPackage(t, registrytest.DefaultGrants(), registrytest.TestPlugin(t), nil)
	p := e.load(t, e.runtime(t, nil)).Platform()
	ctx := context.Background()
	acct := &pluginv1.Account{Id: 7, Type: "apikey", CredentialsJson: "k"}
	built, err := p.BuildBalanceRequest(ctx, &pluginv1.BuildBalanceRequestRequest{Account: acct})
	if err != nil {
		t.Fatal(err)
	}
	reqs := built.GetRequests()
	if len(reqs) != 2 || reqs[0].GetUrl() != "https://upstream.example.com/balance" || reqs[0].GetHeaders()["authorization"] != "Bearer k" ||
		reqs[1].GetMethod() != "POST" || len(reqs[1].GetHeaders()) != 0 || reqs[1].GetBodyJson() != "{}" {
		t.Fatalf("requests: %v", reqs)
	}
	out, err := p.ParseBalanceResponse(ctx, &pluginv1.ParseBalanceResponseRequest{Account: acct, Answers: []*pluginv1.BalanceAnswer{
		{Status: 200, Headers: map[string]string{"x-currency": "USD"}, Body: []byte(`{}`)},
		{Body: []byte("unlimited"), TransportError: "dial tcp: refused"},
	}})
	if r := out.GetResult(); err != nil || r.GetAmountMicros() != 200_000 || r.GetCurrency() != "USD" || !r.GetUnlimited() ||
		r.GetErrorMessage() != "dial tcp: refused" {
		t.Fatalf("result: %v %v", out, err)
	}
	_, err = p.BuildBalanceRequest(ctx, &pluginv1.BuildBalanceRequestRequest{Account: &pluginv1.Account{Type: "none"}})
	if status.Code(err) != codes.Unimplemented || core.AsError(err).Code != core.ErrPluginUnavailable.Code {
		t.Fatalf("unimplemented: %v", err)
	}
}
