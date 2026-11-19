package pluginsdk_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

const taskManifest = `{
  "apiVersion": 1, "key": "plat", "version": "0.1.0", "publisher": "test", "runtime": "grpc",
  "capabilities": [{"id": "platform.adapter.v1"}, {"id": "platform.monitor.v1"}, {"id": "platform.tasks.v1"}],
  "externalServices": ["up.example.com"]
}`

// scripted is a platform whose every step a test can replace. Its Execute is
// ExecuteDefault unless execute is set.
type scripted struct {
	minimalPlatform
	execute  func(context.Context, *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error)
	build    func(context.Context, *pluginv1.ExecuteRequest) (*pluginv1.UpstreamRequest, error)
	classify func(*pluginv1.ClassifyErrorRequest) (*pluginv1.ClassifyErrorResponse, error)
	extract  func(*pluginv1.ExtractUsageRequest) (*pluginv1.UsageReport, error)
	parse    func(*pluginv1.ExtractUsageRequest) (*pluginv1.TaskSubmission, error)
	monitor  func(context.Context, *pluginv1.PollRequest) (*pluginv1.ReconcileResult, error)
}

func (p *scripted) Execute(ctx context.Context, in *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
	if p.execute != nil {
		return p.execute(ctx, in)
	}
	return pluginsdk.ExecuteDefault(ctx, p, in)
}

func (p *scripted) BuildUpstream(ctx context.Context, in *pluginv1.ExecuteRequest) (*pluginv1.UpstreamRequest, error) {
	if p.build != nil {
		return p.build(ctx, in)
	}
	return upstreamOf(in)
}

func (p *scripted) ClassifyError(_ context.Context, in *pluginv1.ClassifyErrorRequest) (*pluginv1.ClassifyErrorResponse, error) {
	return p.classify(in)
}

func (p *scripted) ExtractUsage(_ context.Context, in *pluginv1.ExtractUsageRequest) (*pluginv1.UsageReport, error) {
	return p.extract(in)
}

func (p *scripted) ParseTaskSubmission(_ context.Context, in *pluginv1.ExtractUsageRequest) (*pluginv1.TaskSubmission, error) {
	return p.parse(in)
}

func (p *scripted) Monitor(ctx context.Context, in *pluginv1.PollRequest) (*pluginv1.ReconcileResult, error) {
	return p.monitor(ctx, in)
}

func upstreamOf(in *pluginv1.ExecuteRequest) (*pluginv1.UpstreamRequest, error) {
	return pluginsdk.NewUpstream("", "https://up.example.com/v1/x").
		Header("authorization", "Bearer "+in.GetAccount().GetCredentialsJson()).
		DefaultHeader("x-attempt", string(rune('0'+in.GetAttempt()))).
		Body(in.GetBody()).Model(in.GetMeta().GetModel()).Build()
}

func startScripted(t *testing.T, p *scripted) *pluginsdktest.Harness {
	t.Helper()
	return pluginsdktest.Start(t, p, pluginsdktest.Options{Manifest: []byte(taskManifest)})
}

func executeRequest() *pluginv1.ExecuteRequest {
	return &pluginv1.ExecuteRequest{
		ExecutionToken: "caller-cannot-choose",
		Meta:           &pluginv1.RequestMeta{Model: "m", Protocol: "x.chat"},
		Account:        &pluginv1.Account{Id: 7, CredentialsJson: "secret"},
		Body:           []byte(`{"model":"m","stream":true}`),
	}
}

// The two request flags decide the form of the relay and the kind of report:
// one case per report kind, plus the documented fallback and the upstream
// error of a relay that waited.
func TestExecuteDefaultFormsAndReports(t *testing.T) {
	report := &pluginv1.UsageReport{Tokens: &pluginv1.UsageTokens{OutputTokens: 3}}
	task := &pluginv1.TaskSubmission{UpstreamRefId: "job-1"}
	classified := &pluginv1.ClassifyErrorResponse{ClientStatus: 429, Reason: "rate limited"}
	for name, tc := range map[string]struct {
		extractUsage, taskSubmission bool
		upstream                     *pluginv1.RelayUpstreamResponse
		extractErr                   error
		wantWait                     bool
		want                         *pluginv1.ExecuteResponse
	}{
		"no flag: relay without waiting, host_usage": {
			want: pluginsdk.ReportHostUsage(),
		},
		"extract_usage: wait, usage": {
			extractUsage: true, upstream: pluginsdktest.Observed(200, []byte(`{"usage":3}`)),
			wantWait: true, want: pluginsdk.ReportUsage(report),
		},
		"task_submission: wait, task": {
			taskSubmission: true, upstream: pluginsdktest.Observed(200, []byte(`{"id":"job-1"}`)),
			wantWait: true, want: pluginsdk.ReportTask(task),
		},
		"both flags: the task wins": {
			extractUsage: true, taskSubmission: true, upstream: pluginsdktest.Observed(200, []byte(`{"id":"job-1"}`)),
			wantWait: true, want: pluginsdk.ReportTask(task),
		},
		"extraction fails: host_usage fallback": {
			extractUsage: true, upstream: pluginsdktest.Observed(200, []byte(`garbage`)), extractErr: errors.New("no usage in body"),
			wantWait: true, want: pluginsdk.ReportHostUsage(),
		},
		"upstream error of a waited relay: classification": {
			extractUsage: true, upstream: pluginsdktest.UpstreamFailed(429, []byte(`{"error":"slow down"}`), ""),
			wantWait: true, want: pluginsdk.ReportClassification(classified),
		},
		"transport error of a waited task submission: classification": {
			taskSubmission: true, upstream: pluginsdktest.UpstreamFailed(0, nil, "connection reset"),
			wantWait: true, want: pluginsdk.ReportClassification(classified),
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := &scripted{
				classify: func(in *pluginv1.ClassifyErrorRequest) (*pluginv1.ClassifyErrorResponse, error) {
					if in.GetStatus() != tc.upstream.GetError().GetStatus() || in.GetTransportError() != tc.upstream.GetError().GetTransportError() {
						t.Errorf("classified other facts than the host observed: %v", in)
					}
					return classified, nil
				},
				extract: func(in *pluginv1.ExtractUsageRequest) (*pluginv1.UsageReport, error) {
					if in.GetMeta().GetModel() != "m" || in.GetAccount().GetId() != 7 || in.GetAccount().GetCredentialsJson() != "secret" {
						t.Errorf("observation lacks the request's meta or account (as in ExecuteRequest.account): %v", in)
					}
					return report, tc.extractErr
				},
				parse: func(in *pluginv1.ExtractUsageRequest) (*pluginv1.TaskSubmission, error) {
					if string(in.GetBody()) != `{"id":"job-1"}` {
						t.Errorf("parsed another observation: %s", in.GetBody())
					}
					return task, nil
				},
			}
			h := startScripted(t, p)
			h.Host.RelayFunc = func(context.Context, *pluginv1.UpstreamRequest) (*pluginv1.RelayUpstreamResponse, error) {
				return tc.upstream, nil
			}
			in := executeRequest()
			in.ExtractUsage, in.TaskSubmission = tc.extractUsage, tc.taskSubmission
			ex := h.Execute(context.Background(), in)
			relay := ex.Relayed(t)
			if relay.Wait != tc.wantWait {
				t.Errorf("relay waited = %v, want %v", relay.Wait, tc.wantWait)
			}
			if !proto.Equal(ex.Response, tc.want) {
				t.Errorf("response %v, want %v", ex.Response, tc.want)
			}
			wantRequest, _ := upstreamOf(in)
			if !proto.Equal(relay.Request, wantRequest) {
				t.Errorf("relayed %v, want %v", relay.Request, wantRequest)
			}
			if len(ex.Fetches()) != 0 {
				t.Errorf("unexpected fetches: %v", ex.Fetches())
			}
		})
	}
}

// What fails before the relay leaves nothing sent.
func TestExecuteDefaultSendsNothingWhenBuildingFails(t *testing.T) {
	classified := &pluginv1.ClassifyErrorResponse{Reason: "token exchange refused", ClientStatus: 401}
	for name, tc := range map[string]struct {
		p        *scripted
		flag     bool
		wantCode codes.Code
		want     *pluginv1.ExecuteResponse
	}{
		"build error": {
			p: &scripted{build: func(context.Context, *pluginv1.ExecuteRequest) (*pluginv1.UpstreamRequest, error) {
				return nil, status.Error(codes.Unavailable, "no token")
			}},
			wantCode: codes.Unavailable,
		},
		"classified build error": {
			p: &scripted{build: func(context.Context, *pluginv1.ExecuteRequest) (*pluginv1.UpstreamRequest, error) {
				return nil, pluginsdk.Classified(classified)
			}},
			want: pluginsdk.ReportClassification(classified),
		},
		"request the host would refuse": {
			p: &scripted{build: func(context.Context, *pluginv1.ExecuteRequest) (*pluginv1.UpstreamRequest, error) {
				return &pluginv1.UpstreamRequest{Url: "https://up.example.com", Headers: map[string]string{"Content-Length": "1"}}, nil
			}},
			wantCode: codes.InvalidArgument,
		},
	} {
		t.Run(name, func(t *testing.T) {
			ex := startScripted(t, tc.p).Execute(context.Background(), executeRequest())
			if status.Code(ex.Err) != tc.wantCode || !proto.Equal(ex.Response, tc.want) {
				t.Fatalf("got %v, %v", ex.Response, ex.Err)
			}
			if len(ex.Relays()) != 0 || len(ex.RelayErrors()) != 0 {
				t.Fatalf("the host was asked to relay: %v %v", ex.Relays(), ex.RelayErrors())
			}
		})
	}
}

// A task submission needs its parser before anything is built or sent.
func TestExecuteDefaultTaskNeedsParser(t *testing.T) {
	built := false
	h := pluginsdktest.Start(t, builderOnly{&built}, pluginsdktest.Options{Manifest: []byte(platformManifest)})
	in := executeRequest()
	in.TaskSubmission = true
	ex := h.Execute(context.Background(), in)
	if status.Code(ex.Err) != codes.FailedPrecondition || built || len(ex.Relays()) != 0 {
		t.Fatalf("err=%v built=%v relays=%v", ex.Err, built, ex.Relays())
	}
	// Without a UsageExtractor an extract_usage execution falls back.
	h.Host.RelayFunc = func(context.Context, *pluginv1.UpstreamRequest) (*pluginv1.RelayUpstreamResponse, error) {
		return pluginsdktest.Observed(200, nil), nil
	}
	in.TaskSubmission, in.ExtractUsage = false, true
	ex = h.Execute(context.Background(), in)
	if !ex.Relayed(t).Wait || !proto.Equal(ex.Response, pluginsdk.ReportHostUsage()) {
		t.Fatalf("fallback: %v", ex.Response)
	}
}

// A Platform needs a way to execute: an UpstreamBuilder (run with
// ExecuteDefault) or an Executor of its own. One with neither is refused
// before it serves.
func TestPlatformWithoutBuilderOrExecutorIsRefused(t *testing.T) {
	err := pluginsdk.Register(grpc.NewServer(), neitherPlatform{}, nil, pluginsdk.WithInfo("neither", "1.0.0"))
	if err == nil || !strings.Contains(err.Error(), "UpstreamBuilder or Executor") {
		t.Fatalf("err = %v", err)
	}
}

type neitherPlatform struct{}

func (neitherPlatform) ValidateCredentials(context.Context, *pluginv1.ValidateCredentialsRequest) (*pluginv1.ValidateCredentialsResponse, error) {
	return &pluginv1.ValidateCredentialsResponse{}, nil
}
func (neitherPlatform) ClassifyError(context.Context, *pluginv1.ClassifyErrorRequest) (*pluginv1.ClassifyErrorResponse, error) {
	return &pluginv1.ClassifyErrorResponse{}, nil
}
func (neitherPlatform) BuildTestRequest(context.Context, *pluginv1.BuildTestRequestRequest) (*pluginv1.BuildTestRequestResponse, error) {
	return &pluginv1.BuildTestRequestResponse{}, nil
}

type builderOnly struct{ built *bool }

func (builderOnly) ValidateCredentials(context.Context, *pluginv1.ValidateCredentialsRequest) (*pluginv1.ValidateCredentialsResponse, error) {
	return &pluginv1.ValidateCredentialsResponse{}, nil
}
func (builderOnly) ClassifyError(context.Context, *pluginv1.ClassifyErrorRequest) (*pluginv1.ClassifyErrorResponse, error) {
	return &pluginv1.ClassifyErrorResponse{}, nil
}
func (builderOnly) BuildTestRequest(context.Context, *pluginv1.BuildTestRequestRequest) (*pluginv1.BuildTestRequestResponse, error) {
	return &pluginv1.BuildTestRequestResponse{}, nil
}
func (b builderOnly) BuildUpstream(_ context.Context, in *pluginv1.ExecuteRequest) (*pluginv1.UpstreamRequest, error) {
	*b.built = true
	return upstreamOf(in)
}

// An execution relays at most once; a request the host refused does not
// count; waiting needs one of the two flags.
func TestRelayRules(t *testing.T) {
	request, _ := upstreamOf(executeRequest())
	t.Run("second relay", func(t *testing.T) {
		var second error
		p := &scripted{execute: func(ctx context.Context, in *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
			if err := pluginsdk.Relay(ctx, request); err != nil {
				return nil, err
			}
			second = pluginsdk.Relay(ctx, request)
			return pluginsdk.ReportHostUsage(), nil
		}}
		ex := startScripted(t, p).Execute(context.Background(), executeRequest())
		if status.Code(second) != codes.FailedPrecondition || !strings.Contains(second.Error(), "at most once") {
			t.Fatalf("second relay: %v", second)
		}
		ex.Relayed(t) // the second never reached the host
	})
	t.Run("refused by the host does not count", func(t *testing.T) {
		var first error
		p := &scripted{execute: func(ctx context.Context, in *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
			first = pluginsdk.Relay(ctx, request)
			return pluginsdk.ReportHostUsage(), pluginsdk.Relay(ctx, request)
		}}
		h := startScripted(t, p)
		refusals := 1
		h.Host.RelayFunc = func(context.Context, *pluginv1.UpstreamRequest) (*pluginv1.RelayUpstreamResponse, error) {
			if refusals > 0 {
				refusals--
				return nil, status.Error(codes.InvalidArgument, "the SSRF guard refuses this target")
			}
			return nil, nil
		}
		ex := h.Execute(context.Background(), executeRequest())
		if status.Code(first) != codes.InvalidArgument || ex.Err != nil || len(ex.Relays()) != 1 || len(ex.RelayErrors()) != 1 {
			t.Fatalf("first=%v err=%v relays=%d errors=%v", first, ex.Err, len(ex.Relays()), ex.RelayErrors())
		}
	})
	t.Run("wait without a flag", func(t *testing.T) {
		p := &scripted{execute: func(ctx context.Context, in *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
			_, err := pluginsdk.RelayAndWait(ctx, request)
			return nil, err
		}}
		ex := startScripted(t, p).Execute(context.Background(), executeRequest())
		if status.Code(ex.Err) != codes.FailedPrecondition || len(ex.Relays())+len(ex.RelayErrors()) != 0 {
			t.Fatalf("err=%v relays=%v errors=%v", ex.Err, ex.Relays(), ex.RelayErrors())
		}
	})
	t.Run("malformed answer of a waited relay", func(t *testing.T) {
		h := startScripted(t, &scripted{})
		h.Host.RelayFunc = func(context.Context, *pluginv1.UpstreamRequest) (*pluginv1.RelayUpstreamResponse, error) {
			return &pluginv1.RelayUpstreamResponse{}, nil
		}
		in := executeRequest()
		in.ExtractUsage = true
		if ex := h.Execute(context.Background(), in); status.Code(ex.Err) != codes.Internal {
			t.Fatalf("err=%v", ex.Err)
		}
	})
	t.Run("relay fails: no second attempt", func(t *testing.T) {
		h := startScripted(t, &scripted{})
		h.Host.RelayFunc = func(context.Context, *pluginv1.UpstreamRequest) (*pluginv1.RelayUpstreamResponse, error) {
			return nil, status.Error(codes.Unavailable, "host is draining")
		}
		ex := h.Execute(context.Background(), executeRequest())
		if status.Code(ex.Err) != codes.Unavailable || len(ex.Relays()) != 1 || len(ex.RelayErrors()) != 1 {
			t.Fatalf("err=%v relays=%d errors=%v", ex.Err, len(ex.Relays()), ex.RelayErrors())
		}
	})
}

// A report that contradicts what the execution did is an error of the
// plugin, named as such, instead of an unsent or unbilled request.
func TestExecuteReportMustMatchTheRelay(t *testing.T) {
	request, _ := upstreamOf(executeRequest())
	for name, tc := range map[string]struct {
		execute func(context.Context, *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error)
		want    codes.Code
	}{
		"host_usage without a relay": {func(context.Context, *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
			return pluginsdk.ReportHostUsage(), nil
		}, codes.Internal},
		"usage after a relay that did not wait": {func(ctx context.Context, _ *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
			return pluginsdk.ReportUsage(&pluginv1.UsageReport{}), pluginsdk.Relay(ctx, request)
		}, codes.Internal},
		"task without a relay": {func(context.Context, *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
			return pluginsdk.ReportTask(&pluginv1.TaskSubmission{}), nil
		}, codes.Internal},
		"a classification needs no relay": {func(context.Context, *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
			return pluginsdk.ReportClassification(&pluginv1.ClassifyErrorResponse{}), nil
		}, codes.OK},
	} {
		in := executeRequest()
		in.ExtractUsage = true
		if ex := startScripted(t, &scripted{execute: tc.execute}).Execute(context.Background(), in); status.Code(ex.Err) != tc.want {
			t.Errorf("%s: %v", name, ex.Err)
		}
	}
}

// Monitor returns the result it read with one fetch; it cannot relay.
func TestMonitorReturnsTheResult(t *testing.T) {
	result := &pluginv1.ReconcileResult{State: pluginv1.ReconcileResult_SETTLED, Reason: "done"}
	var relayErr error
	p := &scripted{monitor: func(ctx context.Context, in *pluginv1.PollRequest) (*pluginv1.ReconcileResult, error) {
		relayErr = pluginsdk.Relay(ctx, &pluginv1.UpstreamRequest{Url: "https://up.example.com"})
		answer, err := pluginsdk.Fetch(ctx, &pluginv1.ExecutionHTTPRequest{Url: "https://up.example.com/tasks/" + in.GetEntry().GetRefId(), ExecutionToken: "forged"})
		if err != nil {
			return nil, err
		}
		if early := pluginsdk.ClassifyPollResponse(answer.GetStatus(), answer.GetTransportError()); early != nil {
			return early, nil
		}
		return result, nil
	}}
	h := startScripted(t, p)
	answer := &pluginv1.ExecutionHTTPResponse{Status: 200, Body: []byte(`{"status":"succeeded"}`)}
	h.Host.ExecuteHTTPFunc = func(context.Context, *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
		return answer, nil
	}
	in := &pluginv1.PollRequest{Entry: &pluginv1.ReconcileEntry{RefId: "job-1"}}
	ex := h.Monitor(context.Background(), in)
	if ex.Err != nil || !proto.Equal(ex.MonitorResponse.GetResult(), result) {
		t.Fatalf("monitor: %v %v", ex.MonitorResponse, ex.Err)
	}
	if status.Code(relayErr) != codes.FailedPrecondition || len(ex.Relays())+len(ex.RelayErrors()) != 0 {
		t.Fatalf("Monitor could relay: %v", relayErr)
	}
	fetches := ex.Fetches()
	if len(fetches) != 1 || fetches[0].GetUrl() != "https://up.example.com/tasks/job-1" || fetches[0].GetExecutionToken() != ex.Token() {
		t.Fatalf("fetches: %v", fetches)
	}
	// A terminal result needs a complete, successful answer in the call,
	// as the host requires.
	p.monitor = func(context.Context, *pluginv1.PollRequest) (*pluginv1.ReconcileResult, error) { return result, nil }
	if ex = h.Monitor(context.Background(), in); status.Code(ex.Err) != codes.FailedPrecondition {
		t.Fatalf("terminal result without a fetch: %v", ex.Err)
	}
	p.monitor = func(ctx context.Context, in *pluginv1.PollRequest) (*pluginv1.ReconcileResult, error) {
		_, err := pluginsdk.Fetch(ctx, &pluginv1.ExecutionHTTPRequest{Url: "https://up.example.com/tasks/1"})
		return result, err
	}
	if ex = h.Monitor(context.Background(), in); ex.Err != nil {
		t.Fatalf("terminal result after a complete answer: %v", ex.Err)
	}
	// No result is one failed query, not an error.
	p.monitor = func(context.Context, *pluginv1.PollRequest) (*pluginv1.ReconcileResult, error) { return nil, nil }
	if ex = h.Monitor(context.Background(), in); ex.Err != nil || ex.MonitorResponse.GetResult() != nil {
		t.Fatalf("empty monitor: %v %v", ex.MonitorResponse, ex.Err)
	}
}

// The authority of a call is its own, ends with it, and cannot be used
// outside one.
func TestExecutionAuthorityIsScopedToTheCall(t *testing.T) {
	var mu sync.Mutex
	contexts := map[string]context.Context{}
	p := &scripted{execute: func(ctx context.Context, in *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
		mu.Lock()
		contexts[in.GetMeta().GetModel()] = ctx
		mu.Unlock()
		if _, err := pluginsdk.Fetch(ctx, &pluginv1.ExecutionHTTPRequest{Url: "https://up.example.com/" + in.GetMeta().GetModel()}); err != nil {
			return nil, err
		}
		request, _ := upstreamOf(in)
		return pluginsdk.ReportHostUsage(), pluginsdk.Relay(ctx, request)
	}}
	h := startScripted(t, p)
	h.Host.ExecuteHTTPFunc = func(context.Context, *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
		return &pluginv1.ExecutionHTTPResponse{Status: 200}, nil
	}
	executions := map[string]*pluginsdktest.Execution{}
	var wg sync.WaitGroup
	for _, id := range []string{"one", "two"} {
		wg.Go(func() {
			in := executeRequest()
			in.Meta.Model = id
			ex := h.Execute(context.Background(), in)
			mu.Lock()
			executions[id] = ex
			mu.Unlock()
		})
	}
	wg.Wait()
	for id, ex := range executions {
		relay := ex.Relayed(t)
		if relay.Request.GetUpstreamModel() != id || len(ex.Fetches()) != 1 || !strings.HasSuffix(ex.Fetches()[0].GetUrl(), id) || ex.Fetches()[0].GetExecutionToken() != ex.Token() {
			t.Errorf("%s: calls crossed executions: %v %v", id, relay.Request, ex.Fetches())
		}
		if _, err := pluginsdk.Fetch(contexts[id], &pluginv1.ExecutionHTTPRequest{Url: "https://up.example.com"}); status.Code(err) != codes.Canceled {
			t.Errorf("%s: authority outlived the call: %v", id, err)
		}
	}
	if executions["one"].Token() == executions["two"].Token() {
		t.Error("two calls shared a token")
	}
	if _, err := pluginsdk.Fetch(context.Background(), &pluginv1.ExecutionHTTPRequest{}); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("fetch outside a call: %v", err)
	}
	if err := pluginsdk.Relay(context.Background(), &pluginv1.UpstreamRequest{}); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("relay outside a call: %v", err)
	}
}

// The fetch form: hosts outside the manifest are refused, the host neither
// counts fetches nor cuts their answers, and a token it has voided ends the
// call's access.
func TestFetchHostsAndExpiry(t *testing.T) {
	const fetches = 9
	const answerBytes = 9 << 20
	var errs []error
	var large *pluginv1.ExecutionHTTPResponse
	p := &scripted{execute: func(ctx context.Context, in *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
		_, err := pluginsdk.Fetch(ctx, &pluginv1.ExecutionHTTPRequest{Url: "https://evil.example.net/x"})
		errs = append(errs, err)
		large, _ = pluginsdk.Fetch(ctx, &pluginv1.ExecutionHTTPRequest{Url: "https://auth.up.example.com/big"})
		for range fetches - 1 {
			_, err = pluginsdk.Fetch(ctx, &pluginv1.ExecutionHTTPRequest{Method: "POST", Url: "https://up.example.com/token"})
		}
		errs = append(errs, err)
		return pluginsdk.ReportClassification(nil), nil
	}}
	h := startScripted(t, p)
	h.Host.ExecuteHTTPFunc = func(_ context.Context, in *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
		if strings.HasSuffix(in.GetUrl(), "/big") {
			return &pluginv1.ExecutionHTTPResponse{Status: 200, Body: bytes.Repeat([]byte("x"), answerBytes)}, nil
		}
		return &pluginv1.ExecutionHTTPResponse{Status: 200}, nil
	}
	ex := h.Execute(context.Background(), executeRequest())
	if ex.Err != nil || len(ex.Fetches()) != fetches {
		t.Fatalf("err=%v fetches=%d", ex.Err, len(ex.Fetches()))
	}
	if status.Code(errs[0]) != codes.PermissionDenied {
		t.Errorf("host outside externalServices: %v", errs[0])
	}
	if len(large.GetBody()) != answerBytes {
		t.Errorf("large answer: len=%d", len(large.GetBody()))
	}
	if errs[1] != nil {
		t.Errorf("fetch %d: %v", fetches, errs[1])
	}

	// The host gives up on the call while the plugin still works.
	h.Host.ExecuteHTTPFunc = func(context.Context, *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
		h.Host.ExpireExecutions()
		return &pluginv1.ExecutionHTTPResponse{Status: 200}, nil
	}
	p.execute = func(ctx context.Context, in *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
		if _, err := pluginsdk.Fetch(ctx, &pluginv1.ExecutionHTTPRequest{Url: "https://up.example.com/token"}); err != nil {
			return nil, err
		}
		request, _ := upstreamOf(in)
		return pluginsdk.ReportHostUsage(), pluginsdk.Relay(ctx, request)
	}
	if ex = h.Execute(context.Background(), executeRequest()); status.Code(ex.Err) != codes.PermissionDenied || len(ex.Relays()) != 0 {
		t.Fatalf("expired token: err=%v relays=%v", ex.Err, ex.Relays())
	}
}

// A plugin takes a body far over gRPC's default 4 MiB and hands it back to
// the host, which bounds neither.
func TestPlatformCarriesLargeBodies(t *testing.T) {
	body := bytes.Repeat([]byte("a"), 9<<20)
	h := startScripted(t, &scripted{})
	in := executeRequest()
	in.Body = body
	ex := h.Execute(context.Background(), in)
	if relay := ex.Relayed(t); !bytes.Equal(relay.Request.GetBody(), body) {
		t.Fatalf("relayed %d bytes", len(relay.Request.GetBody()))
	}
	// The host does not bound a relayed body: one grown beyond what the
	// plugin was handed goes through too.
	grown := &scripted{build: func(_ context.Context, in *pluginv1.ExecuteRequest) (*pluginv1.UpstreamRequest, error) {
		r, err := upstreamOf(in)
		r.Body = append(bytes.Clone(r.GetBody()), bytes.Repeat([]byte(" "), 2<<20)...)
		return r, err
	}}
	h = startScripted(t, grown)
	if ex = h.Execute(context.Background(), in); ex.Err != nil || len(ex.Relays()) != 1 || len(ex.Relays()[0].Request.GetBody()) != len(body)+2<<20 {
		t.Fatalf("grown body: err=%v relays=%d", ex.Err, len(ex.Relays()))
	}
}
