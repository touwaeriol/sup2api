package volcengine

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tidwall/gjson"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

// startMonitor runs the plugin with fetch answered by answer. Monitor reaches
// the upstream only through the host's fetch, never the asset client or the
// egress tunnel (startExecution denies both).
func startMonitor(t *testing.T, answer func(context.Context, *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error)) *pluginsdktest.Harness {
	t.Helper()
	return startExecution(t, func(fh *pluginsdktest.FakeHost) { fh.ExecuteHTTPFunc = answer })
}

func monitor(t *testing.T, h *pluginsdktest.Harness, in *pluginv1.PollRequest) *pluginsdktest.Execution {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return h.Monitor(ctx, in)
}

func TestMonitorFetchesOnceAndReturnsTheResult(t *testing.T) {
	for _, tc := range []struct {
		name      string
		account   *pluginv1.Account
		wantURL   string
		response  *pluginv1.ExecutionHTTPResponse
		execError error
		wantState pluginv1.ReconcileResult_State
		wantCode  codes.Code
		wantBody  bool
	}{
		{name: "official_success", account: account(testKey, "{}"), wantURL: DefaultBaseURL + "/api/v3/contents/generations/tasks/cgt-1",
			response: &pluginv1.ExecutionHTTPResponse{Status: 200, Body: []byte(`{"status":"succeeded","usage":{"completion_tokens":17},"content":{"video_url":"https://cdn.invalid/a.mp4"}}`)}, wantState: pluginv1.ReconcileResult_SETTLED, wantBody: true},
		{name: "relay_pending", account: relayAccount("/v1", "/doubao/api/v3"), wantURL: "https://relay.test/doubao/api/v3/contents/generations/tasks/cgt-1",
			response: &pluginv1.ExecutionHTTPResponse{Status: 200, Body: []byte(`{"status":"running","progress":0.7}`)}, wantBody: true},
		{name: "failed", response: &pluginv1.ExecutionHTTPResponse{Status: 200, Body: []byte(`{"status":"failed","error":{"message":"rejected"}}`)}, wantState: pluginv1.ReconcileResult_FAILED, wantBody: true},
		{name: "estimate", response: &pluginv1.ExecutionHTTPResponse{Status: 200, Body: []byte(`{"status":"succeeded","content":{"video_url":"https://cdn.invalid/a.mp4"}}`)}, wantState: pluginv1.ReconcileResult_SETTLED_ESTIMATE, wantBody: true},
		{name: "malformed", response: &pluginv1.ExecutionHTTPResponse{Status: 200, Body: []byte(`{"status":"failed"`)}, wantState: pluginv1.ReconcileResult_POLL_FAILED},
		{name: "transport_error", response: &pluginv1.ExecutionHTTPResponse{Status: 200, Body: []byte(`{"status":"failed"}`), TransportError: "unexpected EOF"}, wantState: pluginv1.ReconcileResult_POLL_FAILED},
		{name: "http_error", response: &pluginv1.ExecutionHTTPResponse{Status: 503, Body: []byte(`{"status":"failed"}`)}, wantState: pluginv1.ReconcileResult_POLL_FAILED},
		{name: "not_found", response: &pluginv1.ExecutionHTTPResponse{Status: 404, Body: []byte(`{}`)}, wantState: pluginv1.ReconcileResult_NOT_FOUND},
		{name: "host_rejected", execError: status.Error(codes.PermissionDenied, "execution permission expired"), wantCode: codes.PermissionDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := startMonitor(t, func(ctx context.Context, req *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
				if _, ok := ctx.Deadline(); !ok {
					t.Error("Monitor lost its execution deadline")
				}
				return tc.response, tc.execError
			})
			acc := tc.account
			if acc == nil {
				acc = account(testKey, "{}")
			}
			ex := monitor(t, h, &pluginv1.PollRequest{Entry: &pluginv1.ReconcileEntry{RefId: "cgt-1", TaskKind: "video", Model: "seedance"}, Account: acc})
			if status.Code(ex.Err) != tc.wantCode {
				t.Fatalf("Monitor error = %v, want %s", ex.Err, tc.wantCode)
			}
			fetches := ex.Fetches()
			if len(fetches) != 1 || len(ex.Relays()) != 0 {
				t.Fatalf("Monitor made %d fetches and %d relays, want exactly one fetch", len(fetches), len(ex.Relays()))
			}
			wantURL := tc.wantURL
			if wantURL == "" {
				wantURL = DefaultBaseURL + "/api/v3/contents/generations/tasks/cgt-1"
			}
			req := fetches[0]
			if req.GetMethod() != "GET" || req.GetUrl() != wantURL || len(req.GetBody()) != 0 ||
				len(req.GetHeaders()) != 1 || req.GetHeaders()["authorization"] != "Bearer 11111111-2222-3333-4444-555555555555" {
				t.Fatalf("fetch = %v, want GET %s with the original account's key", req, wantURL)
			}
			if ex.Err != nil {
				return
			}
			r := ex.MonitorResponse.GetResult()
			if r.GetState() != tc.wantState || (r.GetTaskSnapshotJson() != "") != tc.wantBody {
				t.Fatalf("Monitor result = %v", r)
			}
			if tc.wantBody && gjson.Get(r.GetTaskSnapshotJson(), "id").String() != "cgt-1" {
				t.Fatalf("Monitor lost task identity: %s", r.GetTaskSnapshotJson())
			}
			if tc.wantState == pluginv1.ReconcileResult_SETTLED && r.GetTokens().GetOutputTokens() != 17 {
				t.Fatalf("Monitor lost real usage: %v", r)
			}
		})
	}
}

// The official task hosts are the manifest's externalServices, which the host
// checks every fetch against.
func TestMonitorStaysWithinExternalServices(t *testing.T) {
	h := startExecution(t, func(fh *pluginsdktest.FakeHost) {
		fh.ExternalServices = []string{"ark.cn-beijing.volces.com", "ark.ap-southeast.bytepluses.com", "ark.cn-beijing.volcengineapi.com"}
		fh.ExecuteHTTPFunc = func(context.Context, *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
			return &pluginv1.ExecutionHTTPResponse{Status: 200, Body: []byte(`{"status":"running"}`)}, nil
		}
	})
	for _, base := range []string{DefaultBaseURL, BytePlusBaseURL} {
		ex := monitor(t, h, &pluginv1.PollRequest{Entry: &pluginv1.ReconcileEntry{RefId: "cgt-1"}, Account: account(testKey, `{"base_url":"`+base+`"}`)})
		if ex.Err != nil || ex.MonitorResponse.GetResult().GetState() != pluginv1.ReconcileResult_PENDING {
			t.Fatalf("%s: %v %v", base, ex.MonitorResponse, ex.Err)
		}
	}
}

// A relay account's task host is its own base_url (or video_endpoint), which
// no manifest can list. The core allows the hosts of the account's declared
// upstreamFields besides externalServices.
func TestMonitorReachesARelayOutsideExternalServices(t *testing.T) {
	h := startExecution(t, func(fh *pluginsdktest.FakeHost) {
		fh.ExecuteHTTPFunc = func(context.Context, *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
			return &pluginv1.ExecutionHTTPResponse{Status: 200, Body: []byte(`{"status":"running"}`)}, nil
		}
	})
	ex := monitor(t, h, &pluginv1.PollRequest{Entry: &pluginv1.ReconcileEntry{RefId: "cgt-1"}, Account: relayAccount("/v1", "/doubao/api/v3")})
	if ex.Err != nil || ex.MonitorResponse.GetResult().GetState() != pluginv1.ReconcileResult_PENDING ||
		len(ex.Fetches()) != 1 || ex.Fetches()[0].GetUrl() != "https://relay.test/doubao/api/v3/contents/generations/tasks/cgt-1" {
		t.Fatalf("relay Monitor: %v %v", ex.MonitorResponse, ex.Err)
	}
}

func TestMonitorRequiresExecutionContextAndUsableEntry(t *testing.T) {
	h := startMonitor(t, func(context.Context, *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
		return &pluginv1.ExecutionHTTPResponse{Status: 200}, nil
	})
	for _, in := range []*pluginv1.PollRequest{
		{Entry: &pluginv1.ReconcileEntry{}, Account: account(testKey, "{}")},
		{Entry: &pluginv1.ReconcileEntry{RefId: "cgt-1"}, Account: &pluginv1.Account{}},
	} {
		if ex := monitor(t, h, in); ex.Err == nil || len(ex.Fetches()) != 0 {
			t.Fatalf("Monitor accepted a missing task ID or credentials: %v, %d fetches", ex.Err, len(ex.Fetches()))
		}
	}
	// A call outside the host's Monitor RPC has no authority to fetch.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := New().Monitor(ctx, &pluginv1.PollRequest{
		Entry: &pluginv1.ReconcileEntry{RefId: "cgt-1"}, Account: account(testKey, "{}"), ExecutionToken: "invocation-1",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("direct call without scoped context = %v", err)
	}
}

func TestMonitorCancellationStopsTheFetchWithoutRetry(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	h := startMonitor(t, func(ctx context.Context, _ *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-ctx.Done()
		if calls.Load() == 1 {
			close(stopped)
		}
		return nil, status.FromContextError(ctx.Err()).Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- h.Monitor(ctx, &pluginv1.PollRequest{Entry: &pluginv1.ReconcileEntry{RefId: "cgt-1"}, Account: account(testKey, "{}")}).Err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("Monitor did not start its fetch")
	}
	cancel()
	select {
	case err := <-done:
		if status.Code(err) != codes.Canceled {
			t.Fatalf("canceled Monitor = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Monitor ignored cancellation")
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("the fetch continued after Monitor was cancelled")
	}
	if calls.Load() != 1 {
		t.Fatalf("cancelled Monitor retried the fetch: %d calls", calls.Load())
	}
}
