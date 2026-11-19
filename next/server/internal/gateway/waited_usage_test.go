package gateway

// The wait form of Execute (design §4.3): an endpoint whose usage the plugin
// reads. Relay sends inside the plugin's call and returns the observation;
// the facts Execute returns are committed through core.ExecutionRecords, or
// the execution is closed unreported (a free record, nothing billed, a
// held-back answer not published).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// waitedPlugin is a fakePlatform whose Execute is the test's: exec gets the
// fake (to build the request) and the client.
type waitedPlugin struct {
	*fakePlatform
	exec func(ctx context.Context, p *fakePlatform, in *pluginv1.ExecuteRequest, client core.ExecutionClient) (core.ExecutionResult, error)
}

func (p *waitedPlugin) Execute(ctx context.Context, in *pluginv1.ExecuteRequest, client core.ExecutionClient) (core.ExecutionResult, error) {
	return p.exec(ctx, p.fakePlatform, in, client)
}

// relayThen builds and relays like ExecuteDefault, then answers what answer
// returns for the relay's outcome.
func relayThen(answer func(out *pluginv1.RelayUpstreamResponse) (*pluginv1.ExecuteResponse, error)) func(context.Context, *fakePlatform, *pluginv1.ExecuteRequest, core.ExecutionClient) (core.ExecutionResult, error) {
	return func(ctx context.Context, p *fakePlatform, in *pluginv1.ExecuteRequest, client core.ExecutionClient) (core.ExecutionResult, error) {
		req, err := p.buildUpstream(ctx, in)
		if err != nil {
			return core.ExecutionResult{}, err
		}
		if err := client.Check(ctx, req); err != nil {
			return core.ExecutionResult{}, err
		}
		out, err := client.Relay(ctx, req)
		if err != nil {
			return core.ExecutionResult{}, err
		}
		res, err := answer(out)
		return core.ExecutionResult{Response: res}, err
	}
}

// scriptedUsageEnv is usageEnv with the account type's plugin replaced by a
// waitedPlugin running exec.
func scriptedUsageEnv(t *testing.T, exec func(context.Context, *fakePlatform, *pluginv1.ExecuteRequest, core.ExecutionClient) (core.ExecutionResult, error)) (*env, *fakePlatform) {
	t.Helper()
	e, pp := usageEnv(t, pluginUsageRules())
	for i := range e.gen.accountTypes {
		if e.gen.accountTypes[i].Plugin.Key == "pu" {
			e.gen.accountTypes[i].Client = &waitedPlugin{fakePlatform: pp, exec: exec}
		}
	}
	return e, pp
}

// assertUnreported checks the record of an execution closed without facts.
func assertUnreported(t *testing.T, e *env) {
	t.Helper()
	rec := e.record()
	if rec.Success || rec.Billable || rec.ErrorType != core.UsageErrorUnreported || rec.UsageExtract != core.UsageExtractUnreported ||
		rec.Tokens != (core.UsageTokens{}) {
		t.Fatalf("free record %+v", rec)
	}
	if st := e.settler.executionStates(); !slices.Equal(st, []string{core.ExecutionStateUnreported}) {
		t.Fatalf("execution states %v", st)
	}
	e.noRecord()
}

// Facts that do not come - no report, a report of the wrong kind, an
// Execute error after the relay - close the execution unreported: the
// non-streamed answer is held back (503, the upstream's body never reaches
// the client) and the record is the free one.
func TestWaitedRelayWithoutFactsIsUnreported(t *testing.T) {
	cases := map[string]func(*pluginv1.RelayUpstreamResponse) (*pluginv1.ExecuteResponse, error){
		"nothing": func(*pluginv1.RelayUpstreamResponse) (*pluginv1.ExecuteResponse, error) {
			return &pluginv1.ExecuteResponse{}, nil
		},
		"task_on_non_task_endpoint": func(*pluginv1.RelayUpstreamResponse) (*pluginv1.ExecuteResponse, error) {
			return &pluginv1.ExecuteResponse{Report: &pluginv1.ExecuteResponse_Task{Task: &pluginv1.TaskSubmission{UpstreamRefId: "t-1"}}}, nil
		},
		"execute_error": func(*pluginv1.RelayUpstreamResponse) (*pluginv1.ExecuteResponse, error) {
			return nil, errors.New("plugin failed after the relay")
		},
	}
	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			e, _ := scriptedUsageEnv(t, relayThen(answer))
			res := e.do("/pu/v1/chat", body(testModel, false), nil)
			if res.status != 503 || strings.Contains(string(res.body), "hello") {
				t.Fatalf("held-back answer published: %d %s", res.status, res.body)
			}
			if n := len(e.up.keys()); n != 1 {
				t.Fatalf("upstream called %d times", n)
			}
			assertUnreported(t, e)
		})
	}
}

// A streamed answer already reached the client when the facts fail to come:
// the record is still the free unreported one.
func TestWaitedRelayStreamWithoutFactsIsUnreported(t *testing.T) {
	e, _ := scriptedUsageEnv(t, relayThen(func(*pluginv1.RelayUpstreamResponse) (*pluginv1.ExecuteResponse, error) {
		return &pluginv1.ExecuteResponse{}, nil
	}))
	e.do("/pu/v1/chat", body(testModel, true), nil)
	assertUnreported(t, e)
}

// A failed commit is retried; the facts are committed once and the answer
// published.
func TestWaitedRelayCommitRetried(t *testing.T) {
	e, pp := usageEnv(t, pluginUsageRules())
	pp.extract = func(context.Context, *pluginv1.ExtractUsageRequest) (*pluginv1.UsageReport, error) {
		return report(3, 4), nil
	}
	var calls atomic.Int32
	e.settler.commitErr = func(core.ExecutionCommit) error {
		if calls.Add(1) == 1 {
			return errors.New("database outage")
		}
		return nil
	}
	res := e.do("/pu/v1/chat", body(testModel, false), nil)
	if res.status != 200 || !strings.Contains(string(res.body), "hello") {
		t.Fatalf("status %d %s", res.status, res.body)
	}
	rec := e.record()
	if !rec.Success || !rec.Billable || rec.Tokens.Input != 3 || rec.Tokens.Output != 4 || rec.UsageExtract != core.UsageExtractPlugin {
		t.Fatalf("record %+v", rec)
	}
	if calls.Load() != 2 {
		t.Fatalf("commit tried %d times", calls.Load())
	}
	if st := e.settler.executionStates(); !slices.Equal(st, []string{core.ExecutionStateCommitted}) {
		t.Fatalf("execution states %v", st)
	}
	e.noRecord()
}

// A commit that is refused for good (a conflict, facts that do not fit the
// execution) or keeps failing until the commit deadline closes the
// execution unreported. Only the outage is retried.
func TestWaitedRelayCommitFailsUnreported(t *testing.T) {
	for _, name := range []string{"conflict", "refused", "outage"} {
		t.Run(name, func(t *testing.T) {
			if name == "outage" {
				if testing.Short() {
					t.Skip("waits out the commit deadline")
				}
				t.Parallel()
			}
			e, pp := usageEnv(t, pluginUsageRules())
			pp.extract = func(context.Context, *pluginv1.ExtractUsageRequest) (*pluginv1.UsageReport, error) {
				return report(3, 4), nil
			}
			var commits atomic.Int32
			e.settler.commitErr = func(core.ExecutionCommit) error {
				commits.Add(1)
				switch name {
				case "conflict":
					return core.ErrConflict
				case "refused":
					return fmt.Errorf("%w: execution identity mismatch", core.ErrExecutionRefused)
				}
				return errors.New("database outage")
			}
			start := time.Now()
			res := e.do("/pu/v1/chat", body(testModel, false), nil)
			if res.status != 503 || strings.Contains(string(res.body), "hello") {
				t.Fatalf("held-back answer published: %d %s", res.status, res.body)
			}
			if name == "outage" && time.Since(start) < commitTimeout-time.Second {
				t.Fatalf("gave up after %s, before the commit deadline", time.Since(start))
			}
			if name != "outage" && (commits.Load() != 1 || time.Since(start) > commitTimeout/2) {
				t.Fatalf("a final refusal was retried: %d commits in %s", commits.Load(), time.Since(start))
			}
			assertUnreported(t, e)
		})
	}
}

// An upstream error in the wait form is classified by the plugin inside
// Execute (ExecuteResponse.classification) and that classification applies:
// here a 500 cools the account down and fails over. Without one the default
// classification applies (a 500 fails over, without an account effect).
func TestWaitedRelayUpstreamErrorClassification(t *testing.T) {
	for _, name := range []string{"plugin", "default"} {
		t.Run(name, func(t *testing.T) {
			var e *env
			var pp *fakePlatform
			if name == "plugin" {
				e, pp = usageEnv(t, pluginUsageRules())
			} else {
				e, pp = scriptedUsageEnv(t, relayThen(func(out *pluginv1.RelayUpstreamResponse) (*pluginv1.ExecuteResponse, error) {
					if out.GetError() != nil {
						return &pluginv1.ExecuteResponse{}, nil
					}
					return &pluginv1.ExecuteResponse{Report: &pluginv1.ExecuteResponse_Usage{Usage: report(1, 2)}}, nil
				}))
			}
			pp.extract = func(context.Context, *pluginv1.ExtractUsageRequest) (*pluginv1.UsageReport, error) {
				return report(1, 2), nil
			}
			e.accounts.addTyped(testGroup, 10, 50, "pu-10", "pu", "pu_key")
			e.up.set("pu-9", &upstreamRule{status: 500})
			res := e.do("/pu/v1/chat", body(testModel, false), nil)
			if res.status != 200 || strings.Join(e.up.keys(), ",") != "pu-9,pu-10" {
				t.Fatalf("failover: %d %v %s", res.status, e.up.keys(), res.body)
			}
			rec := e.record()
			if !rec.Success || rec.Attempts != 2 || *rec.AccountID != 10 || rec.Tokens.Output != 2 {
				t.Fatalf("record %+v", rec)
			}
			pp.mu.Lock()
			classified := len(pp.classify)
			pp.mu.Unlock()
			cooled := slices.Contains(e.accounts.cooldowns, 9)
			if name == "plugin" && (classified != 1 || !cooled) || name == "default" && (classified != 0 || cooled) {
				t.Fatalf("classify calls %d, cooldowns %v", classified, e.accounts.cooldowns)
			}
			if st := e.settler.executionStates(); !slices.Contains(st, core.ExecutionStateCommitted) {
				t.Fatalf("execution states %v", st)
			}
			e.noRecord()
		})
	}
}

// A 4xx the plugin returns to the client reaches it as the upstream sent it,
// without failover.
func TestWaitedRelayUpstreamClientError(t *testing.T) {
	e, _ := usageEnv(t, pluginUsageRules())
	e.accounts.addTyped(testGroup, 10, 50, "pu-10", "pu", "pu_key")
	e.up.set("pu-9", &upstreamRule{status: 400})
	res := e.do("/pu/v1/chat", body(testModel, false), nil)
	if res.status != 400 || strings.Join(e.up.keys(), ",") != "pu-9" {
		t.Fatalf("client error: %d %v %s", res.status, e.up.keys(), res.body)
	}
	if rec := e.record(); rec.Success || rec.Billable || rec.StatusCode != 400 {
		t.Fatalf("record %+v", rec)
	}
}

// The plugin giving up its host call while the upstream still streams (a
// crash, a kill) cuts the upstream exchange; nothing is billed.
func TestWaitedRelayPluginCrashCutsTheUpstream(t *testing.T) {
	var e *env
	e, _ = scriptedUsageEnv(t, func(ctx context.Context, p *fakePlatform, in *pluginv1.ExecuteRequest, client core.ExecutionClient) (core.ExecutionResult, error) {
		req, err := p.buildUpstream(ctx, in)
		if err != nil {
			return core.ExecutionResult{}, err
		}
		if err := client.Check(ctx, req); err != nil {
			return core.ExecutionResult{}, err
		}
		rctx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			// The crash: once the upstream holds the stream.
			for len(e.up.keys()) == 0 {
				time.Sleep(5 * time.Millisecond)
			}
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()
		if _, err := client.Relay(rctx, req); err != nil {
			return core.ExecutionResult{}, err
		}
		return core.ExecutionResult{}, errors.New("plugin process exited")
	})
	hold := make(chan struct{})
	defer close(hold)
	e.up.set("pu-9", &upstreamRule{hold: hold})
	done := make(chan result, 1)
	go func() { done <- e.do("/pu/v1/chat", body(testModel, true), nil) }()
	select {
	case key := <-e.up.canceled:
		if key != "pu-9" {
			t.Fatalf("canceled %s", key)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the upstream exchange was not cut")
	}
	<-done
	assertUnreported(t, e)
}

// A non-streamed answer is published only once the facts are committed.
func TestWaitedRelayPublishesJSONAfterCommit(t *testing.T) {
	e, pp := usageEnv(t, pluginUsageRules())
	release := make(chan struct{})
	pp.extract = func(context.Context, *pluginv1.ExtractUsageRequest) (*pluginv1.UsageReport, error) {
		<-release
		return report(1, 1), nil
	}
	const wait = 300 * time.Millisecond
	time.AfterFunc(wait, func() { close(release) })
	start := time.Now()
	res := e.do("/pu/v1/chat", body(testModel, false), nil)
	if res.status != 200 || !strings.Contains(string(res.body), "hello") {
		t.Fatalf("status %d %s", res.status, res.body)
	}
	if d := time.Since(start); d < wait {
		t.Fatalf("answer published %s in, before the facts", d)
	}
	if rec := e.record(); rec.Tokens.Input != 1 || !rec.Success {
		t.Fatalf("record %+v", rec)
	}
}

// A streamed answer reaches the client as it arrives, while the plugin has
// not read the usage yet: the plugin is released only once the client got
// the stream's last event.
func TestWaitedRelayStreamsBeforeTheFacts(t *testing.T) {
	e, pp := usageEnv(t, pluginUsageRules())
	release := make(chan struct{})
	pp.extract = func(context.Context, *pluginv1.ExtractUsageRequest) (*pluginv1.UsageReport, error) {
		<-release
		return report(1, 1), nil
	}
	raw, _ := json.Marshal(body(testModel, true))
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/pu/v1/chat", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", testKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got := make(chan bool, 1)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if strings.Contains(sc.Text(), "message_stop") {
				got <- true
				return
			}
		}
		got <- false
	}()
	select {
	case ok := <-got:
		close(release)
		if !ok {
			t.Fatal("stream ended without message_stop")
		}
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("the stream waited for the plugin's facts")
	}
	if rec := e.record(); rec.Tokens.Input != 1 || !rec.Success {
		t.Fatalf("record %+v", rec)
	}
}

// An error the plugin reads out of a complete 2xx answer (UsageReport
// error_type) makes the record a failure, billed as reported, and the
// client still gets the upstream's bytes: billed once, delivered once.
func TestWaitedRelayReportedErrorDeliversTheAnswer(t *testing.T) {
	e, _ := scriptedUsageEnv(t, relayThen(func(*pluginv1.RelayUpstreamResponse) (*pluginv1.ExecuteResponse, error) {
		r := report(100, 50)
		r.ErrorType, r.ErrorMessage = "content_filter", "blocked by the upstream"
		return &pluginv1.ExecuteResponse{Report: &pluginv1.ExecuteResponse_Usage{Usage: r}}, nil
	}))
	res := e.do("/pu/v1/chat", body(testModel, false), nil)
	if res.status != 200 || !strings.Contains(string(res.body), "hello") {
		t.Fatalf("the committed answer was held back: %d %s", res.status, res.body)
	}
	rec := e.record()
	if rec.Success || !rec.Billable || rec.ErrorType != "content_filter" || rec.Tokens.Input != 100 || rec.Tokens.Output != 50 {
		t.Fatalf("record %+v", rec)
	}
	if st := e.settler.executionStates(); !slices.Equal(st, []string{core.ExecutionStateCommitted}) {
		t.Fatalf("execution states %v", st)
	}
	e.noRecord()
}

// A non-streamed answer of any size is held back on disk and delivered
// whole once committed; the temp file is gone afterwards.
func TestWaitedRelayLargeAnswerIsDeliveredWhole(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	e, _ := scriptedUsageEnv(t, relayThen(func(*pluginv1.RelayUpstreamResponse) (*pluginv1.ExecuteResponse, error) {
		return &pluginv1.ExecuteResponse{Report: &pluginv1.ExecuteResponse_Usage{Usage: report(100, 50)}}, nil
	}))
	big := `{"text":"` + strings.Repeat("x", 65<<20) + `"}`
	e.up.set("pu-9", &upstreamRule{status: 200, body: big})
	res := e.do("/pu/v1/chat", body(testModel, false), nil)
	if res.status != 200 || len(res.body) != len(big) {
		t.Fatalf("large answer: %d, %d of %d bytes", res.status, len(res.body), len(big))
	}
	// No observation budget: the plugin is shown the whole answer and its
	// report is billed.
	if rec := e.record(); !rec.Success || rec.UsageExtract != core.UsageExtractPlugin || rec.Tokens.Input != 100 {
		t.Fatalf("record %+v", rec)
	}
	if st := e.settler.executionStates(); !slices.Equal(st, []string{core.ExecutionStateCommitted}) {
		t.Fatalf("execution states %v", st)
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

// A spool that fails for real (its temp file cannot be created) is known
// before the commit: the execution is closed unreported, the client gets
// 503 and nothing is billed, whatever the plugin reported.
func TestWaitedRelaySpoolFailureIsUnreported(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	e, _ := scriptedUsageEnv(t, relayThen(func(*pluginv1.RelayUpstreamResponse) (*pluginv1.ExecuteResponse, error) {
		return &pluginv1.ExecuteResponse{Report: &pluginv1.ExecuteResponse_Usage{Usage: report(100, 50)}}, nil
	}))
	e.up.set("pu-9", &upstreamRule{status: 200, body: `{"text":"` + strings.Repeat("x", 2<<20) + `"}`})
	var commits atomic.Int32
	e.settler.commitErr = func(core.ExecutionCommit) error { commits.Add(1); return nil }
	res := e.do("/pu/v1/chat", body(testModel, false), nil)
	if res.status != 503 || len(res.body) > 4096 {
		t.Fatalf("failed spool: %d %d bytes", res.status, len(res.body))
	}
	if commits.Load() != 0 {
		t.Fatalf("committed %d times", commits.Load())
	}
	assertUnreported(t, e)
}

// An observed execution is renewed for as long as Execute runs, however
// long that is, and only that long: the facts commit once and the renewals
// stop with the attempt.
func TestWaitedRelayRenewsWhileAlive(t *testing.T) {
	release := make(chan struct{})
	e, _ := scriptedUsageEnv(t, relayThen(func(*pluginv1.RelayUpstreamResponse) (*pluginv1.ExecuteResponse, error) {
		<-release // the plugin still working after the answer
		return &pluginv1.ExecuteResponse{Report: &pluginv1.ExecuteResponse_Usage{Usage: report(3, 4)}}, nil
	}))
	e.gw.renewInterval = 10 * time.Millisecond
	done := make(chan result, 1)
	go func() { done <- e.do("/pu/v1/chat", body(testModel, false), nil) }()
	deadline := time.Now().Add(5 * time.Second)
	for e.settler.renewals.Load() < 5 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	close(release)
	res := <-done
	if res.status != 200 || e.settler.renewals.Load() < 5 {
		t.Fatalf("status %d, renewals %d", res.status, e.settler.renewals.Load())
	}
	if rec := e.record(); !rec.Billable || rec.Tokens.Input != 3 {
		t.Fatalf("record %+v", rec)
	}
	e.noRecord()
	settled := e.settler.renewals.Load()
	time.Sleep(10 * e.gw.renewInterval)
	if n := e.settler.renewals.Load(); n > settled+1 { // one may have been running at the stop
		t.Fatalf("renewals went on after the attempt: %d -> %d", settled, n)
	}
}

// A renewal that finds the execution no longer observed (committed or
// closed elsewhere) ends the heartbeat without a stop.
func TestHeartbeatStopsOnceNotObserved(t *testing.T) {
	s := &fakeSettler{ch: make(chan *core.UsageRecord, 1)}
	var h heartbeat
	h.start(context.Background(), s, "gone", 5*time.Millisecond)
	deadline := time.Now().Add(5 * time.Second)
	for s.renewals.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if n := s.renewals.Load(); n != 1 {
		t.Fatalf("renewals %d, want exactly 1", n)
	}
	h.mu.Lock()
	stopped := h.stopped
	h.mu.Unlock()
	if !stopped {
		t.Fatal("heartbeat still armed")
	}
}

// A plugin that hangs before it relays is cancelled by the client leaving:
// nothing was sent upstream, so its Execute call ends with the client, the
// attempt is canceled, nothing is billed, no execution is recorded and the
// slots are released. (Once the relay sends, the client's departure no
// longer reaches the call: TestClientDisconnectWaitedRelay.)
func TestWaitedRelayClientLeavesBeforeTheRelay(t *testing.T) {
	entered, canceled := make(chan struct{}), make(chan struct{})
	e, _ := scriptedUsageEnv(t, func(ctx context.Context, _ *fakePlatform, _ *pluginv1.ExecuteRequest, _ core.ExecutionClient) (core.ExecutionResult, error) {
		close(entered)
		<-ctx.Done() // hangs until its call is cancelled
		close(canceled)
		return core.ExecutionResult{}, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	raw, _ := json.Marshal(body(testModel, false))
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, e.srv.URL+"/pu/v1/chat", bytes.NewReader(raw))
	req.Header.Set("x-api-key", testKey)
	go func() {
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Execute was not called")
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("the client leaving did not cancel the Execute call")
	}
	rec := e.record()
	if rec.Billable || rec.ErrorType != errTypeClientCanceled || rec.Tokens != (core.UsageTokens{}) {
		t.Fatalf("record %+v", rec)
	}
	if len(e.up.keys()) != 0 || len(e.settler.executionStates()) != 0 {
		t.Fatalf("sent %v, executions %v", e.up.keys(), e.settler.executionStates())
	}
	e.noRecord()
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.slots.mu.Lock()
		held := 0
		for _, n := range e.slots.inUse {
			held += n
		}
		e.slots.mu.Unlock()
		if held == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("slots still held: %d", held)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A commit whose acknowledgement was lost and whose retries then fail is
// found committed when the execution is closed: the answer is delivered,
// the facts are billed once, and the rate limits count the committed facts,
// not the core's reading taken before the report.
func TestWaitedRelayCommitLandedCountsTheFacts(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the commit deadline")
	}
	e, pp := usageEnv(t, pluginUsageRules())
	lim := newFakeLimiter()
	e.gw.d.Limiter = lim
	pp.extract = func(context.Context, *pluginv1.ExtractUsageRequest) (*pluginv1.UsageReport, error) {
		return report(3, 4), nil
	}
	var calls atomic.Int32
	e.settler.commitErr = func(core.ExecutionCommit) error {
		if calls.Add(1) == 1 {
			return nil
		}
		return errors.New("database outage")
	}
	e.settler.commitAckLost = func(core.ExecutionCommit) error { return context.DeadlineExceeded }
	res := e.do("/pu/v1/chat", body(testModel, false), nil)
	if res.status != 200 || !strings.Contains(string(res.body), "hello") {
		t.Fatalf("status %d %s", res.status, res.body)
	}
	rec := e.record()
	if !rec.Success || !rec.Billable || rec.Tokens.Input != 3 || rec.Tokens.Output != 4 {
		t.Fatalf("committed record %+v", rec)
	}
	if st := e.settler.executionStates(); !slices.Equal(st, []string{core.ExecutionStateCommitted}) {
		t.Fatalf("execution states %v", st)
	}
	e.noRecord()
	want := rec.Tokens.Throughput(rec.UsageSemantics)
	lim.mu.Lock()
	defer lim.mu.Unlock()
	if got := lim.modelTkns["9:"+testModel]; got != want {
		t.Fatalf("rate limits counted %d tokens, the committed facts are %d", got, want)
	}
}
