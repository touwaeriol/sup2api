package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// The single request path (execute.go, waited.go) with fake plugins speaking
// the Execute contract (fakeexec_test.go). The no-wait rows of the design's
// gateway matrix (§14.2) that need a held stream are here; the plugin-usage
// rows are in extractusage_test.go, the header and response framing rows in
// upstream_ext_test.go, the WebSocket rows in websocket_test.go.

// deadPlatform serves attempts like fakePlatform until dead is set; after
// that any call into the plugin is a test failure, as for a process gone.
type deadPlatform struct {
	*fakePlatform
	t    *testing.T
	dead atomic.Bool
}

func (p *deadPlatform) Execute(ctx context.Context, in *pluginv1.ExecuteRequest, client core.ExecutionClient) (core.ExecutionResult, error) {
	if p.dead.Load() {
		p.t.Error("Execute called on a dead plugin")
		return core.ExecutionResult{}, core.ErrPluginUnavailable
	}
	return fakeExecute(ctx, p.fakePlatform, in, client)
}

func (p *deadPlatform) ClassifyError(ctx context.Context, in *pluginv1.ClassifyErrorRequest) (*pluginv1.ClassifyErrorResponse, error) {
	if p.dead.Load() {
		p.t.Error("ClassifyError called on a dead plugin")
	}
	return p.fakePlatform.ClassifyError(ctx, in)
}

// No wait: once Execute returned with host_usage the exchange is the core's
// alone. The plugin's process going away mid-stream changes nothing: the
// stream completes, nothing is asked of the plugin, the usage is billed by
// the rules, and no execution record exists.
func TestExecuteNoWaitPluginGoneAfterHandOver(t *testing.T) {
	hold := make(chan struct{})
	var p *deadPlatform
	e := newEnv(t, func(e *env) {
		p = &deadPlatform{fakePlatform: e.plat, t: t}
		e.gen.accountTypes[0].Client = p
	})
	e.up.set("acc-1", &upstreamRule{hold: hold})
	br, hangUp := openStream(t, e, "/v1/messages", body(testModel, true), nil, `"type":"message_start"`)
	defer hangUp()
	p.dead.Store(true)
	close(hold)
	rest, err := io.ReadAll(br)
	require.NoError(t, err)
	require.Contains(t, string(rest), "message_stop")
	rec := e.record()
	require.True(t, rec.Success)
	require.EqualValues(t, upOutput, rec.Tokens.Output)
	require.True(t, rec.Billable)
	require.Empty(t, e.settler.executionStates(), "the no-wait path writes no execution record")
}

// Under a failure policy the HTTP error of a waited relay is classified by
// the plugin inside Execute, as ClassifyError is on the no-wait path: the
// policy still decides retry and failover, and takes the plugin's timed
// disable (CONTRACTS §69).
func TestFailurePolicyWaitedTakesPluginClassification(t *testing.T) {
	e, _ := waitedEnv(t, core.DisconnectPrecharge, &upstreamRule{status: 503})
	e.setFailurePolicy(testFailurePolicy())
	reset := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	e.plat.classifyHook = func(_ *pluginv1.ClassifyErrorRequest, out *pluginv1.ClassifyErrorResponse) {
		out.Action = pluginv1.ClassifyErrorResponse_ACTION_RETURN_TO_CLIENT // not the plugin's to decide
		out.AutomaticState = &pluginv1.AccountAutomaticState{Action: pluginv1.AccountAutomaticState_ACTION_DISABLE,
			Reason: "quota window full", Release: &pluginv1.AccountAutomaticState_UntilUnix{UntilUnix: reset.Unix()}}
	}
	require.Equal(t, 200, e.messages(body(testModel, false)).status)
	require.Equal(t, []string{"acc-1", "acc-2"}, e.up.keys())
	require.Len(t, e.plat.classify, 1)
	require.Equal(t, "quota window full", e.accounts.disabled[1])
}

// A waited execution may span the upstream exchange: its context has no
// deadline at all, whatever relay_timeout_seconds and the platform call
// timeout say.
func TestExecuteWaitedHasNoTimeLimit(t *testing.T) {
	e, p := waitedEnv(t, core.DisconnectPrecharge, &upstreamRule{delay: 300 * time.Millisecond})
	e.gw.d.Environment = fakeEnvironment{global: core.EnvironmentValues{core.EnvVarRelayTimeoutSeconds: 86400}}
	gw := noQueueSettings()
	gw.PlatformCallTimeoutMs = 100
	e.setSettings(gw, StickySettings{})
	r := e.messages(body(testModel, false))
	require.Equal(t, 200, r.status, string(r.body))
	require.Nil(t, p.deadline.Load(), "the waited Execute has a deadline")
	require.True(t, e.record().Success)
}

// A waited answer that is not an event stream is published only once its
// facts are committed.
func TestExecuteWaitedPublishesAfterCommit(t *testing.T) {
	e, _ := waitedEnv(t, core.DisconnectPrecharge, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	e.settler.commitErr = func(core.ExecutionCommit) error { close(entered); <-release; return nil }
	done := make(chan result, 1)
	go func() { done <- e.messages(body(testModel, false)) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the facts were not committed")
	}
	select {
	case r := <-done:
		t.Fatalf("published before the commit: %d %s", r.status, r.body)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	r := <-done
	require.Equal(t, 200, r.status, string(r.body))
	require.NotEmpty(t, r.json().Get("content.0.text").String())
	require.True(t, e.record().Success)
	require.Equal(t, []string{core.ExecutionStateCommitted}, e.settler.executionStates())
	require.Len(t, e.up.keys(), 1)
	e.noRecord()
}

// Facts that never commit leave the execution unreported: the held-back
// answer is not published (503), a streamed one ends abruptly, the request
// is not billed and its slots are released.
func TestExecuteWaitedCommitFailureIsUnreported(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "non-stream", true: "stream"}[stream], func(t *testing.T) {
			e, _ := waitedEnv(t, core.DisconnectPrecharge, nil)
			var commits atomic.Int64
			e.settler.commitErr = func(core.ExecutionCommit) error { commits.Add(1); return errors.New("ledger down") }
			raw, _ := json.Marshal(body(testModel, stream))
			req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/v1/messages", strings.NewReader(string(raw)))
			req.Header.Set("x-api-key", testKey)
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			got, readErr := io.ReadAll(resp.Body)
			if stream {
				require.Error(t, readErr, "a streamed answer without facts must end abruptly: %s", got)
			} else {
				require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode, string(got))
				require.NotContains(t, string(got), "hello", "the held-back answer was published")
			}
			require.Greater(t, commits.Load(), int64(1), "the commit is retried")
			rec := e.record()
			require.False(t, rec.Billable)
			require.Equal(t, core.UsageErrorUnreported, rec.ErrorType)
			require.Equal(t, []string{core.ExecutionStateUnreported}, e.settler.executionStates())
			e.noRecord()
			e.slots.mu.Lock()
			defer e.slots.mu.Unlock()
			for key, n := range e.slots.inUse {
				require.Zero(t, n, "leaked slot %s", key)
			}
		})
	}
}

// An observation that cannot be stored leaves no execution record to own
// the usage: the answer is not published, and the request's own free record
// is submitted - nothing billed, and as a free record it releases the
// precharge at once instead of at its expiry.
func TestExecuteWaitedObservationNotStored(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "non-stream", true: "stream"}[stream], func(t *testing.T) {
			e, _ := waitedEnv(t, core.DisconnectPrecharge, nil)
			e.settler.observeErr = errors.New("database down")
			r := e.messages(body(testModel, stream))
			if !stream {
				require.Equal(t, http.StatusServiceUnavailable, r.status, string(r.body))
				require.NotContains(t, string(r.body), "hello", "the held-back answer was published")
			}
			require.Equal(t, []string{core.ExecutionStateIntent}, e.settler.executionStates())
			rec := e.record()
			require.False(t, rec.Success)
			require.False(t, rec.Billable, "a free record releases the precharge")
			require.Nil(t, rec.Price)
			require.Nil(t, rec.Reservation)
			require.Equal(t, core.UsageTokens{}, rec.Tokens)
			require.Equal(t, core.UsageErrorUnreported, rec.ErrorType)
			require.Equal(t, core.UsageExtractUnreported, rec.UsageExtract)
			e.noRecord()
		})
	}
}

// A waited upstream error is classified by the plugin and fails over like
// any other.
func TestExecuteWaitedUpstreamErrorFailsOver(t *testing.T) {
	e, _ := waitedEnv(t, core.DisconnectPrecharge, &upstreamRule{status: 429})
	r := e.messages(body(testModel, false))
	require.Equal(t, 200, r.status, string(r.body))
	require.Equal(t, []string{"acc-1", "acc-2"}, e.up.keys())
	require.Len(t, e.plat.classify, 1)
	require.Contains(t, e.accounts.cooldowns, int64(1))
	require.True(t, e.record().Success)
	require.ElementsMatch(t, []string{core.ExecutionStateCommitted, core.ExecutionStateIntent}, e.settler.executionStates())
}

// The commit happens inside the attempt, before the gateway settles the
// sticky binding; the committed record still carries the matched rule and
// the hit.
func TestExecuteWaitedCommitCarriesSticky(t *testing.T) {
	e, _ := waitedEnv(t, core.DisconnectPrecharge, nil)
	e.enableSticky(onFailureFailover)
	sess := withSession(body(testModel, false), "user_abc_account__session_exec")
	require.Equal(t, 200, e.messages(sess).status)
	first := e.record()
	require.Equal(t, "claude-code-session", first.StickyRule)
	require.False(t, first.StickyHit)
	require.Equal(t, 200, e.messages(sess).status)
	second := e.record()
	require.Equal(t, "claude-code-session", second.StickyRule)
	require.True(t, second.StickyHit)
	require.Equal(t, 200, e.messages(body(testModel, false)).status)
	require.Empty(t, e.record().StickyRule)
	e.noRecord()
}

// ---------------------------------------------------------------- task submission

// A task submission waits: the plugin's task is committed with the usage
// record in one commit (operation watch), and the answer is published with
// the public id in place of the upstream one.
func TestExecuteTaskCommitsTheTask(t *testing.T) {
	tasks := newFakeTasks()
	e, p := taskDraftEnv(t, tasks, false)
	r := e.do("/task-draft/videos", map[string]any{"model": testModel}, nil)
	public := r.json().Get("id").String()
	require.Equal(t, 200, r.status, string(r.body))
	require.True(t, strings.HasPrefix(public, "s2task_"))
	require.Equal(t, public, r.json().Get("data.id").String())
	require.NotContains(t, string(r.body), "raw-video-id")
	require.EqualValues(t, 1, p.parseCalls.Load())
	require.True(t, e.record().Success)
	e.noRecord()
	e.settler.mu.Lock()
	defer e.settler.mu.Unlock()
	require.Len(t, e.settler.executions, 1)
	for _, x := range e.settler.executions {
		require.Equal(t, core.ExecutionStateCommitted, x.state)
		require.Equal(t, core.ExecutionOpWatch, x.commit.Operation)
		require.Equal(t, "raw-video-id", x.commit.Task.UpstreamID)
		require.Equal(t, public, x.commit.Task.PublicID)
		require.True(t, x.intent.TaskSubmit)
	}
}

// Without a task the submission is not registered: the client gets an
// error, the free record of a task failure is submitted (the execution is
// left observed, never closed), and the request is never sent again.
func TestExecuteTaskWithoutFacts(t *testing.T) {
	for name, fail := range map[string]func(*taskDraftPlatform){
		"parser error": func(p *taskDraftPlatform) { p.parseError = errors.New("unparseable") },
		"no task":      func(p *taskDraftPlatform) { p.noTask = true },
	} {
		t.Run(name, func(t *testing.T) {
			tasks := newFakeTasks()
			e, p := taskDraftEnv(t, tasks, false)
			fail(p)
			r := e.do("/task-draft/videos", map[string]any{"model": testModel}, nil)
			require.Equal(t, http.StatusServiceUnavailable, r.status, string(r.body))
			require.Len(t, e.up.keys(), 1, "a task submission is never retried")
			rec := e.record()
			require.False(t, rec.Billable)
			require.Nil(t, rec.Reservation)
			require.Equal(t, []string{core.ExecutionStateObserved}, e.settler.executionStates())
			e.noRecord()
		})
	}
}

// A task commit whose last try landed after the commit deadline (its answer
// lost) is found committed before the submission is reported as failed: the
// client gets the task. One that never landed is closed unreported: 503,
// the execution's free record, the receipt marked failed.
func TestExecuteTaskCommitTimeoutIsConfirmed(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the commit deadline")
	}
	for _, landed := range []bool{true, false} {
		t.Run(map[bool]string{true: "landed", false: "lost"}[landed], func(t *testing.T) {
			t.Parallel()
			tasks := newFakeTasks()
			e, _ := taskDraftEnv(t, tasks, false)
			var calls atomic.Int32
			e.settler.commitErr = func(core.ExecutionCommit) error {
				if landed && calls.Add(1) == 1 {
					return nil
				}
				return errors.New("database outage")
			}
			e.settler.commitAckLost = func(core.ExecutionCommit) error { return context.DeadlineExceeded }
			r := e.do("/task-draft/videos", map[string]any{"model": testModel}, nil)
			rec := e.record()
			if landed {
				require.Equal(t, 200, r.status, string(r.body))
				require.True(t, strings.HasPrefix(r.json().Get("id").String(), "s2task_"), string(r.body))
				require.True(t, rec.Success)
				require.Equal(t, []string{core.ExecutionStateCommitted}, e.settler.executionStates())
			} else {
				require.Equal(t, http.StatusServiceUnavailable, r.status, string(r.body))
				require.False(t, rec.Billable)
				require.Equal(t, core.UsageErrorUnreported, rec.ErrorType)
				require.Equal(t, []string{core.ExecutionStateUnreported}, e.settler.executionStates())
			}
			e.noRecord()
			tasks.mu.Lock()
			defer tasks.mu.Unlock()
			for _, reason := range tasks.receipts {
				require.Equal(t, !landed, reason != "", "receipt %q", reason)
			}
		})
	}
}

// A task submission relayed without waiting carries no task: it is not
// sent, and no account serves the request.
func TestExecuteTaskRelayedWithoutWaitingIsNotSent(t *testing.T) {
	e, p := taskDraftEnv(t, newFakeTasks(), false)
	p.noWait = true
	r := e.do("/task-draft/videos", map[string]any{"model": testModel}, nil)
	require.NotEqual(t, 200, r.status, string(r.body))
	require.Empty(t, e.up.keys())
	require.Empty(t, e.accounts.cooldowns)
	require.Empty(t, e.settler.executionStates())
}

// The upstream refusing the submission is returned to the client, never
// failed over, with the plugin's classification.
func TestExecuteTaskUpstreamRefusal(t *testing.T) {
	e, p := taskDraftEnv(t, newFakeTasks(), false)
	e.up.set("task-account-9", &upstreamRule{status: 400})
	r := e.do("/task-draft/videos", map[string]any{"model": testModel}, nil)
	require.Equal(t, 400, r.status, string(r.body))
	require.Equal(t, []string{"task-account-9"}, e.up.keys())
	require.Len(t, p.classify, 1)
	rec := e.record()
	require.False(t, rec.Success)
	require.Equal(t, []string{core.ExecutionStateIntent}, e.settler.executionStates())
}
