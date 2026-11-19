package grpcruntime

import (
	"context"
	"errors"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/netguard"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/registry"
)

const mib = 1 << 20

// pluginSide is the plugin's part of one call in these tests: it gets the
// host server the real plugin would reach over the broker.
type pluginSide struct {
	h       *hostServer
	execute func(ctx context.Context, h *hostServer, in *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error)
	monitor func(ctx context.Context, h *hostServer, in *pluginv1.PollRequest) (*pluginv1.MonitorResponse, error)
}

// executionInstance is an instance whose process is played by side. Its
// manifest names api.example.com as its only external service.
func executionInstance(t *testing.T, side *pluginSide) (*Instance, platformAdapter) {
	t.Helper()
	i, _ := testInstance(t, nil, []string{manifest.CapPlatformAdapter, manifest.CapPlatformMonitor}, registry.Grants{})
	i.pkg.Manifest.ExternalServices = []string{"api.example.com"}
	p := &proc{limits: limitsOf(i.launchSpec())}
	side.h = &hostServer{i: i, p: p}
	p.platform = &platformRPC{
		execute: func(ctx context.Context, in *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
			return side.execute(ctx, side.h, in)
		},
		monitor: func(ctx context.Context, in *pluginv1.PollRequest) (*pluginv1.MonitorResponse, error) {
			return side.monitor(ctx, side.h, in)
		},
	}
	i.proc.Store(p)
	return i, platformAdapter{i}
}

var (
	hostUsage = &pluginv1.ExecuteResponse{Report: &pluginv1.ExecuteResponse_HostUsage{HostUsage: &pluginv1.HostUsage{}}}
	usage     = &pluginv1.ExecuteResponse{Report: &pluginv1.ExecuteResponse_Usage{Usage: &pluginv1.UsageReport{}}}
)

func relay(ctx context.Context, h *hostServer, token string, wait bool, body int) (*pluginv1.RelayUpstreamResponse, error) {
	return h.RelayUpstream(ctx, &pluginv1.RelayUpstreamRequest{ExecutionToken: token, Wait: wait,
		Request: &pluginv1.UpstreamRequest{Url: "https://upstream.example.com/v1", Body: make([]byte, body)}})
}

func fetch(ctx context.Context, h *hostServer, token, url string, body int) (*pluginv1.ExecutionHTTPResponse, error) {
	return h.ExecuteHTTP(ctx, &pluginv1.ExecutionHTTPRequest{ExecutionToken: token, Url: url, Body: make([]byte, body)})
}

func ok200(context.Context, *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
	return &pluginv1.ExecutionHTTPResponse{Status: 200}, nil
}

// A relay that does not wait is checked and kept, nothing more: the request
// leaves the runtime only when Execute has returned, and only with
// host_usage.
func TestRelayWithoutWaitSendsNothingBeforeExecuteReturns(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer func() (*pluginv1.ExecuteResponse, error)
		relay  bool
	}{
		{"host_usage", func() (*pluginv1.ExecuteResponse, error) { return hostUsage, nil }, true},
		{"usage", func() (*pluginv1.ExecuteResponse, error) { return usage, nil }, false},
		{"nothing stated", func() (*pluginv1.ExecuteResponse, error) { return &pluginv1.ExecuteResponse{}, nil }, false},
		{"error", func() (*pluginv1.ExecuteResponse, error) { return nil, status.Error(codes.Internal, "boom") }, false},
		{"crash", func() (*pluginv1.ExecuteResponse, error) {
			return nil, status.Error(codes.Unavailable, "transport is closing")
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var events []string
			note := func(e string) { mu.Lock(); events = append(events, e); mu.Unlock() }
			side := &pluginSide{execute: func(ctx context.Context, h *hostServer, in *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
				out, err := relay(ctx, h, in.GetExecutionToken(), false, 10)
				if err != nil || out.GetObservation() != nil || out.GetError() != nil {
					t.Errorf("relay: %v %v", out, err)
				}
				if _, err := relay(ctx, h, in.GetExecutionToken(), false, 10); status.Code(err) != codes.FailedPrecondition {
					t.Errorf("second relay: %v", err)
				}
				time.Sleep(30 * time.Millisecond)
				note("plugin returns")
				return tc.answer()
			}}
			_, a := executionInstance(t, side)
			res, err := a.Execute(context.Background(), &pluginv1.ExecuteRequest{}, core.ExecutionClient{
				Check: func(context.Context, *pluginv1.UpstreamRequest) error { note("check"); return nil },
				Relay: func(context.Context, *pluginv1.UpstreamRequest) (*pluginv1.RelayUpstreamResponse, error) {
					note("send")
					return nil, nil
				},
			})
			note("execute returned")
			if !slices.Equal(events, []string{"check", "plugin returns", "execute returned"}) {
				t.Fatalf("events %v", events)
			}
			if (res.Relay != nil) != tc.relay || (tc.relay && (err != nil || res.Relay.GetUrl() != "https://upstream.example.com/v1" || len(res.Relay.GetBody()) != 10)) {
				t.Fatalf("relay %v err %v", res.Relay, err)
			}
			if tc.name == "crash" && core.AsError(err).Code != core.ErrPluginUnavailable.Code {
				t.Fatalf("crash: %v", err)
			}
		})
	}
}

// A request the gateway's check refuses is INVALID_ARGUMENT and does not
// count as the execution's relay.
func TestRelayRefusedByCheckIsNotARelay(t *testing.T) {
	refuse := true
	side := &pluginSide{execute: func(ctx context.Context, h *hostServer, in *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
		if _, err := relay(ctx, h, in.GetExecutionToken(), false, 1); status.Code(err) != codes.InvalidArgument || status.Convert(err).Message() != "private address" {
			t.Errorf("refused relay: %v", err)
		}
		refuse = false
		if _, err := relay(ctx, h, in.GetExecutionToken(), false, 2); err != nil {
			t.Errorf("relay after a refusal: %v", err)
		}
		return hostUsage, nil
	}}
	_, a := executionInstance(t, side)
	res, err := a.Execute(context.Background(), &pluginv1.ExecuteRequest{}, core.ExecutionClient{Check: func(context.Context, *pluginv1.UpstreamRequest) error {
		if refuse {
			return core.ErrInvalidArgument.WithMessage("private address").WithCause(errors.New("10.0.0.1"))
		}
		return nil
	}})
	if err != nil || len(res.Relay.GetBody()) != 2 {
		t.Fatalf("relay %v err %v", res.Relay, err)
	}
}

// A relay that waits runs the exchange inside the host call and answers with
// its observation; it needs one of the two flags.
func TestRelayWaitReturnsObservation(t *testing.T) {
	var sends atomic.Int32
	client := core.ExecutionClient{Relay: func(_ context.Context, req *pluginv1.UpstreamRequest) (*pluginv1.RelayUpstreamResponse, error) {
		sends.Add(1)
		return &pluginv1.RelayUpstreamResponse{Observation: &pluginv1.ExtractUsageRequest{Status: 200, Body: req.GetBody()}}, nil
	}}
	side := &pluginSide{execute: func(ctx context.Context, h *hostServer, in *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
		out, err := relay(ctx, h, in.GetExecutionToken(), true, 7)
		if !in.GetExtractUsage() && !in.GetTaskSubmission() {
			if status.Code(err) != codes.FailedPrecondition {
				t.Errorf("wait without a flag: %v", err)
			}
			return hostUsage, nil
		}
		if err != nil || out.GetObservation().GetStatus() != 200 || out.GetError() != nil {
			t.Errorf("observation: %v %v", out, err)
		}
		return &pluginv1.ExecuteResponse{Report: &pluginv1.ExecuteResponse_Usage{Usage: &pluginv1.UsageReport{
			Tokens: &pluginv1.UsageTokens{InputTokens: int64(len(out.GetObservation().GetBody()))}}}}, nil
	}}
	_, a := executionInstance(t, side)
	for _, in := range []*pluginv1.ExecuteRequest{{ExtractUsage: true}, {TaskSubmission: true}} {
		res, err := a.Execute(context.Background(), in, client)
		if err != nil || res.Relay != nil || res.Response.GetUsage().GetTokens().GetInputTokens() != 7 {
			t.Fatalf("waited: %v %v", res, err)
		}
	}
	if sends.Load() != 2 {
		t.Fatalf("sends %d", sends.Load())
	}
	res, err := a.Execute(context.Background(), &pluginv1.ExecuteRequest{}, client)
	if err != nil || res.Relay != nil || sends.Load() != 2 {
		t.Fatalf("wait without a flag: %v %v sends %d", res, err, sends.Load())
	}
}

// The body of a relay has no bound of the runtime's, whatever the body the
// plugin was handed; the transport's headers, Host among them (a request
// names the host of its URL), are not the plugin's.
func TestRelayBodyUnboundAndHeaders(t *testing.T) {
	side := &pluginSide{execute: func(ctx context.Context, h *hostServer, in *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
		large := max(mib, len(in.GetBody())) + 2*mib
		for _, name := range []string{"Host", "content-length", "Accept-Encoding", "connection", "transfer-encoding"} {
			for _, req := range []*pluginv1.UpstreamRequest{{Url: "https://u.example.com", Headers: map[string]string{name: "x"}}, {Url: "https://u.example.com", DefaultHeaders: map[string]string{name: "x"}}} {
				if _, err := h.RelayUpstream(ctx, &pluginv1.RelayUpstreamRequest{ExecutionToken: in.GetExecutionToken(), Request: req}); status.Code(err) != codes.InvalidArgument {
					t.Errorf("header %s: %v", name, err)
				}
			}
		}
		for _, req := range []*pluginv1.UpstreamRequest{nil, {}} {
			if _, err := h.RelayUpstream(ctx, &pluginv1.RelayUpstreamRequest{ExecutionToken: in.GetExecutionToken(), Request: req}); status.Code(err) != codes.InvalidArgument {
				t.Errorf("request %v: %v", req, err)
			}
		}
		if _, err := relay(ctx, h, in.GetExecutionToken(), false, large); err != nil {
			t.Errorf("handed %d, relayed %d: %v", len(in.GetBody()), large, err)
		}
		return hostUsage, nil
	}}
	_, a := executionInstance(t, side)
	for _, handed := range []int{0, mib, 3 * mib} {
		res, err := a.Execute(context.Background(), &pluginv1.ExecuteRequest{Body: make([]byte, handed)}, core.ExecutionClient{})
		if err != nil || res.Relay == nil {
			t.Fatalf("handed %d: %v %v", handed, res, err)
		}
	}
}

// Fetch: only to the manifest's external services, with no count, no bound
// on the request body and the answer whole, and no token outside its call
// or its process.
func TestFetchLimits(t *testing.T) {
	var fetches atomic.Int32
	var token string
	side := &pluginSide{execute: func(ctx context.Context, h *hostServer, in *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
		token = in.GetExecutionToken()
		for _, url := range []string{"https://evil.example.org/x", "ftp://api.example.com/x", "https://api.example.com.evil.org/x", "://"} {
			if _, err := fetch(ctx, h, token, url, 0); status.Code(err) != codes.PermissionDenied && status.Code(err) != codes.InvalidArgument {
				t.Errorf("%s: %v", url, err)
			}
		}
		if _, err := fetch(ctx, (&hostServer{i: h.i, p: &proc{}}), token, "https://api.example.com/x", 0); status.Code(err) != codes.PermissionDenied {
			t.Errorf("another process: %v", err)
		}
		if fetches.Load() != 0 {
			t.Errorf("refused requests were sent: %d", fetches.Load())
		}
		out, err := fetch(ctx, h, token, "https://sub.API.example.com:8443/big", 3*mib)
		if err != nil || len(out.GetBody()) != 20*mib {
			t.Errorf("large answer: bytes=%d err=%v", len(out.GetBody()), err)
		}
		for n := 2; n <= 12; n++ { // no count limit
			if out, err := fetch(ctx, h, token, "https://api.example.com/x", 0); err != nil || out.GetStatus() != 200 {
				t.Errorf("fetch %d: %v %v", n, out, err)
			}
		}
		return hostUsage, nil
	}}
	i, a := executionInstance(t, side)
	client := core.ExecutionClient{Fetch: func(_ context.Context, in *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
		fetches.Add(1)
		if len(in.GetBody()) > 0 {
			return &pluginv1.ExecutionHTTPResponse{Status: 200, Body: make([]byte, 20*mib)}, nil
		}
		return &pluginv1.ExecutionHTTPResponse{Status: 200, Body: []byte("ok")}, nil
	}}
	if _, err := a.Execute(context.Background(), &pluginv1.ExecuteRequest{}, client); err != nil {
		t.Fatal(err)
	}
	if fetches.Load() != 12 {
		t.Fatalf("fetches sent: %d", fetches.Load())
	}
	if _, err := fetch(context.Background(), side.h, token, "https://api.example.com/x", 0); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("token after Execute returned: %v", err)
	}
	if _, err := relay(context.Background(), side.h, token, false, 0); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("relay after Execute returned: %v", err)
	}
	if n := len(i.proc.Load().scopes.byToken); n != 0 {
		t.Fatalf("%d execution tokens leaked", n)
	}
	// An execution without a fetch capability refuses every fetch.
	side.execute = func(ctx context.Context, h *hostServer, in *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
		if _, err := fetch(ctx, h, in.GetExecutionToken(), "https://api.example.com/x", 0); status.Code(err) != codes.FailedPrecondition {
			t.Errorf("no capability: %v", err)
		}
		return hostUsage, nil
	}
	if _, err := a.Execute(context.Background(), &pluginv1.ExecuteRequest{}, core.ExecutionClient{}); err != nil {
		t.Fatal(err)
	}
}

// A fetch may also go to the hosts of the account's own upstream addresses -
// the address fields its type declares - and nowhere else. Whether such an
// address may be reached is still the guard's decision: one naming a private
// target passes the host rule and is refused by the SSRF policy.
func TestFetchAccountAddressHosts(t *testing.T) {
	account := &pluginv1.Account{Type: "apikey",
		SettingsJson:    `{"base_url":"https://Relay.Operator.example:8443/v1","note":"https://note.example.org"}`,
		CredentialsJson: `{"video_endpoint":"http://127.0.0.1:9/video","api_key":"https://key.example.org"}`}
	var sent atomic.Int32
	// The fetch of the usage module and the gateway: the URL goes through
	// the SSRF guard before anything is sent.
	public := func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("93.184.216.34")}, nil }
	guarded := func(ctx context.Context, in *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
		if _, err := netguard.CheckURL(ctx, in.GetUrl(), false, public); err != nil {
			return &pluginv1.ExecutionHTTPResponse{TransportError: "execution URL rejected by host policy"}, nil
		}
		sent.Add(1)
		return &pluginv1.ExecutionHTTPResponse{Status: 200}, nil
	}
	try := func(ctx context.Context, h *hostServer, token string) {
		for url, code := range map[string]codes.Code{
			"https://relay.operator.example/v1/tasks/1":     codes.OK, // an address of this account, any port
			"https://api.example.com/x":                     codes.OK, // externalServices, as before
			"https://sub.relay.operator.example/x":          codes.PermissionDenied,
			"https://note.example.org/x":                    codes.PermissionDenied, // not an address field
			"https://key.example.org/x":                     codes.PermissionDenied,
			"https://unrelated.example.net/x":               codes.PermissionDenied,
			"ftp://relay.operator.example/x":                codes.PermissionDenied,
			"https://relay.operator.example.evil.example/x": codes.PermissionDenied,
		} {
			if _, err := fetch(ctx, h, token, url, 0); status.Code(err) != code {
				t.Errorf("%s: %v, want %s", url, err, code)
			}
		}
		before := sent.Load()
		out, err := fetch(ctx, h, token, "http://127.0.0.1:9/video/tasks/1", 0)
		if err != nil || out.GetTransportError() == "" || out.GetStatus() != 0 || sent.Load() != before {
			t.Errorf("account address on a private target: %v %v, sent %d", out, err, sent.Load()-before)
		}
	}
	side := &pluginSide{
		execute: func(ctx context.Context, h *hostServer, in *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
			try(ctx, h, in.GetExecutionToken())
			return hostUsage, nil
		},
		monitor: func(ctx context.Context, h *hostServer, in *pluginv1.PollRequest) (*pluginv1.MonitorResponse, error) {
			try(ctx, h, in.GetExecutionToken())
			return &pluginv1.MonitorResponse{Result: &pluginv1.ReconcileResult{}}, nil
		},
	}
	i, a := executionInstance(t, side)
	i.pkg.Manifest.AccountTypes[0].UpstreamFields = []string{"base_url", "video_endpoint"}
	if _, err := a.Execute(context.Background(), &pluginv1.ExecuteRequest{Account: account}, core.ExecutionClient{Fetch: guarded}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Monitor(context.Background(), &pluginv1.PollRequest{Account: account}, guarded); err != nil {
		t.Fatal(err)
	}
	if sent.Load() != 4 {
		t.Fatalf("sent %d", sent.Load())
	}
	// Another account, or a type that declares no address field, has only
	// the external services.
	i.pkg.Manifest.AccountTypes[0].UpstreamFields = nil
	side.execute = func(ctx context.Context, h *hostServer, in *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
		if _, err := fetch(ctx, h, in.GetExecutionToken(), "https://relay.operator.example/v1", 0); status.Code(err) != codes.PermissionDenied {
			t.Errorf("no address fields: %v", err)
		}
		return hostUsage, nil
	}
	if _, err := a.Execute(context.Background(), &pluginv1.ExecuteRequest{Account: account}, core.ExecutionClient{Fetch: guarded}); err != nil {
		t.Fatal(err)
	}
}

// When the plugin gives up a waiting relay - its Execute returns or fails,
// or its host call is cancelled as for a crashed process - the exchange's
// context ends, and Execute returns only after the relay has ended.
func TestWaitingRelayIsCutAndJoined(t *testing.T) {
	for _, abandon := range []string{"execute returns", "host call cancelled"} {
		t.Run(abandon, func(t *testing.T) {
			started, cut, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
			client := core.ExecutionClient{Relay: func(ctx context.Context, _ *pluginv1.UpstreamRequest) (*pluginv1.RelayUpstreamResponse, error) {
				close(started)
				<-ctx.Done()
				close(cut)
				<-finish
				return nil, ctx.Err()
			}}
			side := &pluginSide{execute: func(ctx context.Context, h *hostServer, in *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
				rpc, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := make(chan struct{})
				go func() { defer close(done); _, _ = relay(rpc, h, in.GetExecutionToken(), true, 0) }()
				<-started
				if _, err := fetch(ctx, h, in.GetExecutionToken(), "https://api.example.com/x", 0); status.Code(err) != codes.FailedPrecondition {
					t.Errorf("host call during another: %v", err)
				}
				if abandon == "host call cancelled" {
					cancel()
					<-done
				}
				return nil, status.Error(codes.Unavailable, "transport is closing")
			}}
			_, a := executionInstance(t, side)
			returned := make(chan error, 1)
			go func() {
				_, err := a.Execute(context.Background(), &pluginv1.ExecuteRequest{ExtractUsage: true}, client)
				returned <- err
			}()
			<-cut
			select {
			case <-returned:
				t.Fatal("Execute returned while the relay still ran")
			case <-time.After(20 * time.Millisecond):
			}
			close(finish)
			if err := <-returned; core.AsError(err).Code != core.ErrPluginUnavailable.Code {
				t.Fatalf("execute: %v", err)
			}
		})
	}
}

// Monitor's result is its return value. A terminal state needs a complete,
// successful, untruncated fetch made during the call.
func TestMonitorResult(t *testing.T) {
	settled, pending := pluginv1.ReconcileResult_SETTLED, pluginv1.ReconcileResult_PENDING
	for _, tc := range []struct {
		name   string
		answer *pluginv1.ExecutionHTTPResponse
		fetch  bool
		state  *pluginv1.ReconcileResult_State
		wantOK bool
	}{
		{"terminal with evidence", &pluginv1.ExecutionHTTPResponse{Status: 200}, true, &settled, true},
		{"terminal on an http error status", &pluginv1.ExecutionHTTPResponse{Status: 404}, true, &settled, true},
		{"terminal on a transport error", &pluginv1.ExecutionHTTPResponse{TransportError: "offline"}, true, &settled, false},
		{"terminal without a fetch", nil, false, &settled, false},
		{"pending without a fetch", nil, false, &pending, true},
		{"no result", &pluginv1.ExecutionHTTPResponse{Status: 200}, true, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			side := &pluginSide{monitor: func(ctx context.Context, h *hostServer, in *pluginv1.PollRequest) (*pluginv1.MonitorResponse, error) {
				if _, err := relay(ctx, h, in.GetExecutionToken(), false, 0); status.Code(err) != codes.PermissionDenied {
					t.Errorf("relay in Monitor: %v", err)
				}
				if tc.fetch {
					if _, err := fetch(ctx, h, in.GetExecutionToken(), "https://api.example.com/task", 0); err != nil {
						t.Errorf("fetch: %v", err)
					}
				}
				if tc.state == nil {
					return &pluginv1.MonitorResponse{}, nil
				}
				return &pluginv1.MonitorResponse{Result: &pluginv1.ReconcileResult{State: *tc.state}}, nil
			}}
			i, a := executionInstance(t, side)
			result, err := a.Monitor(context.Background(), &pluginv1.PollRequest{}, func(context.Context, *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
				return tc.answer, nil
			})
			if (err == nil) != tc.wantOK || (result != nil) != tc.wantOK || (tc.wantOK && result.GetState() != *tc.state) {
				t.Fatalf("result %v err %v", result, err)
			}
			if n := len(i.proc.Load().scopes.byToken); n != 0 {
				t.Fatalf("%d execution tokens leaked", n)
			}
		})
	}
}

// A fetch still running when Monitor returns is cancelled and joined, and
// cannot back the result that was produced before it ended.
func TestMonitorLateFetchIsNoEvidence(t *testing.T) {
	started := make(chan struct{})
	side := &pluginSide{monitor: func(_ context.Context, h *hostServer, in *pluginv1.PollRequest) (*pluginv1.MonitorResponse, error) {
		go func() {
			_, _ = fetch(context.Background(), h, in.GetExecutionToken(), "https://api.example.com/task", 0)
		}()
		<-started
		return &pluginv1.MonitorResponse{Result: &pluginv1.ReconcileResult{State: pluginv1.ReconcileResult_SETTLED}}, nil
	}}
	_, a := executionInstance(t, side)
	var ended atomic.Bool
	result, err := a.Monitor(context.Background(), &pluginv1.PollRequest{}, func(ctx context.Context, _ *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
		close(started)
		<-ctx.Done()
		ended.Store(true)
		return &pluginv1.ExecutionHTTPResponse{Status: 200}, nil
	})
	if err == nil || result != nil || !ended.Load() {
		t.Fatalf("result %v err %v, fetch ended %v", result, err, ended.Load())
	}
}

func TestMonitorNeedsCapabilityAndFetch(t *testing.T) {
	i, a := executionInstance(t, &pluginSide{})
	if _, err := a.Monitor(context.Background(), &pluginv1.PollRequest{}, nil); err == nil {
		t.Fatal("Monitor without a fetch capability")
	}
	delete(i.caps, manifest.CapPlatformMonitor)
	if _, err := a.Monitor(context.Background(), &pluginv1.PollRequest{}, ok200); err == nil {
		t.Fatal("Monitor without platform.monitor.v1")
	}
}

// The token is added to a copy that shares the caller's fields: the caller's
// message stays as it is and the body is not copied.
func TestShallowCloneSharesFields(t *testing.T) {
	in := &pluginv1.ExecuteRequest{Meta: &pluginv1.RequestMeta{RequestId: "r"}, Account: &pluginv1.Account{Id: 3}, Body: []byte("body"),
		InboundHeaders: map[string]string{"a": "b"}, Attempt: 2, ExtractUsage: true, TaskSubmission: true}
	out := shallowClone(in)
	out.ExecutionToken = "token"
	if in.GetExecutionToken() != "" || out.GetMeta() != in.GetMeta() || out.GetAccount() != in.GetAccount() || &out.GetBody()[0] != &in.GetBody()[0] ||
		out.GetInboundHeaders()["a"] != "b" || out.GetAttempt() != 2 || !out.GetExtractUsage() || !out.GetTaskSubmission() {
		t.Fatalf("clone %v of %v", out, in)
	}
}

// Memory is limited only by the operator: the plugin's own limit when one is
// set, exactly, else the node's cap when one is set, else nothing. The
// manifest's figure plays no part.
func TestMemoryLimitOnlyByOperator(t *testing.T) {
	i, _ := executionInstance(t, &pluginSide{})
	spec := func(declared, operator, cap int) int {
		i.pkg.Manifest.Resources = &manifest.Resources{MemoryMB: declared}
		st := *i.settings.Load()
		st.limits = resourceLimits{MemoryMB: operator}
		i.settings.Store(&st)
		i.rt.o.MaxMemoryMB = cap
		return i.launchSpec().MemoryMB
	}
	for _, tc := range []struct{ declared, operator, cap, want int }{
		{0, 0, 0, 0},          // nothing configured: no limit
		{64, 0, 0, 0},         // the manifest's figure is no limit
		{64, 48, 0, 48},       // an operator limit below it, as set
		{64, 4096, 0, 4096},   // and above it, as set
		{64, 0, 300, 300},     // the node's cap when the operator set one
		{0, 512, 1024, 512},   // a plugin's limit under the cap
		{0, 2048, 1024, 1024}, // the cap over a plugin's limit
	} {
		if got := spec(tc.declared, tc.operator, tc.cap); got != tc.want {
			t.Errorf("declared %d, operator %d, cap %d: %d MB, want %d", tc.declared, tc.operator, tc.cap, got, tc.want)
		}
	}
}
