package gateway

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// The task submission platform "task-draft" (submit and query endpoints of
// one plugin) and an in-memory task store, for the tests of a task submitted
// through a relay that waits.

type taskDraftPlatform struct {
	*fakePlatform
	parseCalls atomic.Int64
	parseError error
	// noTask answers host_usage instead of the task; noWait relays the
	// submission without waiting.
	noTask, noWait bool
}

// Execute serves the attempt as ExecuteDefault does, with this platform's
// task parser.
func (p *taskDraftPlatform) Execute(ctx context.Context, in *pluginv1.ExecuteRequest, client core.ExecutionClient) (core.ExecutionResult, error) {
	if p.noWait {
		in = proto.Clone(in).(*pluginv1.ExecuteRequest)
		in.TaskSubmission = false
	}
	res, err := fakeExecute(ctx, p, in, client)
	if p.noTask && res.Response != nil && res.Response.GetTask() != nil {
		res.Response.Report = &pluginv1.ExecuteResponse_HostUsage{HostUsage: &pluginv1.HostUsage{}}
	}
	return res, err
}

func (p *taskDraftPlatform) ParseTaskSubmission(_ context.Context, in *pluginv1.ExtractUsageRequest) (*pluginv1.TaskSubmission, error) {
	p.parseCalls.Add(1)
	if p.parseError != nil {
		return nil, p.parseError
	}
	return &pluginv1.TaskSubmission{UpstreamRefId: "raw-video-id",
		SnapshotJson:      `{"id":"raw-video-id","data":{"id":"raw-video-id"},"status":"queued","opaque_number":9007199254740993}`,
		NextCheckAfterSec: 20, DeadlineSec: 3600}, nil
}

func taskDraftEnv(t *testing.T, tasks core.AsyncTasks, noAccounts bool) (*env, *taskDraftPlatform) {
	t.Helper()
	pp := &taskDraftPlatform{fakePlatform: &fakePlatform{}}
	e := newEnv(t, func(e *env) {
		pp.base = e.up.srv.URL
		submit := manifest.Endpoint{ID: "submit", Method: "POST", Path: "/task-draft/videos", Protocol: "task-draft.submit", Kind: "proxy",
			Auth: manifest.EndpointAuth{Headers: []string{"x-api-key"}}, Request: manifest.EndpointRequest{ModelPath: "model"},
			Response: manifest.EndpointResp{NonStream: "json"}, Billing: "free", ErrorFormat: "plain",
			Task: &manifest.AsyncTaskEndpoint{Action: manifest.TaskActionSubmit, Kind: "video", IDPaths: []string{"id", "data.id"}}}
		query := manifest.Endpoint{ID: "query", Method: "GET", Path: "/task-draft/videos/:task_id", Protocol: "task-draft.query", Kind: "proxy",
			Auth: manifest.EndpointAuth{Headers: []string{"x-api-key"}}, Response: manifest.EndpointResp{NonStream: "json"}, Billing: "free", ErrorFormat: "plain",
			Task: &manifest.AsyncTaskEndpoint{Action: manifest.TaskActionQuery, Kind: "video", IDParam: "task_id", IDPaths: []string{"id", "data.id"}}}
		pf := manifest.Platform{ID: "task-draft", Endpoints: []manifest.Endpoint{submit, query}}
		info := e.addAccountType("task-draft", "apikey", pp, manifest.AccountPlatform{Platform: "task-draft"})
		e.gen.addPlatform(info, pf, pp)
		e.accounts.addTyped(testGroup, 9, 100, "task-account-9", "task-draft", "apikey")
		e.accounts.addTyped(testGroup, 10, 99, "task-account-10", "task-draft", "apikey")
		e.accounts.groups[testGroup] = []int64{9, 10}
		if noAccounts {
			e.accounts.groups[testGroup] = nil
		}
		for key, change := range map[string]func(*core.APIKeyPrincipal){
			"same-owner-other-key": func(p *core.APIKeyPrincipal) { p.KeyID++ },
			"other-user":           func(p *core.APIKeyPrincipal) { p.UserID++ },
			"other-group":          func(p *core.APIKeyPrincipal) { p.Group.ID++ },
			"model-denied":         func(p *core.APIKeyPrincipal) { p.Group.ModelAllowlist = []string{"unrelated-model"} },
		} {
			principal := *e.auth.keys[testKey]
			change(&principal)
			e.auth.keys[key] = &principal
		}
	})
	e.gw.d.Tasks = tasks
	e.up.set("task-account-9", &upstreamRule{status: 200, body: `{"id":"raw-video-id","data":{"id":"raw-video-id"}}`})
	e.up.set("task-account-10", &upstreamRule{status: 200, body: `{"id":"raw-video-id","data":{"id":"raw-video-id"}}`})
	return e, pp
}

func taskDraftGet(t *testing.T, e *env, id, key string) result {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, e.srv.URL+"/task-draft/videos/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("x-api-key", key)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return result{status: resp.StatusCode, header: resp.Header, body: body}
}

// fakeTasks is the gateway's side of the task store in memory: the
// submission intents and receipts; the registration itself arrives with the
// commit of the execution (fakeSettler).
type fakeTasks struct {
	mu       sync.Mutex
	next     int
	intents  map[string]core.TaskIntent // by public id
	receipts map[string]string          // request id -> failure reason ("" = received)
}

func newFakeTasks() *fakeTasks {
	return &fakeTasks{intents: map[string]core.TaskIntent{}, receipts: map[string]string{}}
}

func (s *fakeTasks) BeginTask(_ context.Context, in core.TaskIntent) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	id := fmt.Sprintf("s2task_%d", s.next)
	s.intents[id] = in
	return id, nil
}

func (s *fakeTasks) ReceiveTask(_ context.Context, requestID string, _ int, _ []byte, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.receipts[requestID] = reason
	return nil
}

func (s *fakeTasks) FindTask(context.Context, core.TaskQuery) (*core.TaskSnapshot, error) {
	return nil, core.ErrNotFound
}
