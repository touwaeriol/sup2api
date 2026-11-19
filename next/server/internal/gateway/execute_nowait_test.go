package gateway

// The no-wait form of Execute (design §4.2): every endpoint whose usage the
// core reads. The plugin's relay is only checked while Execute runs; the
// gateway sends it after Execute returned host_usage, meters and settles it
// like any request, and writes no execution records.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// executeWith replaces the anthropic plugin's Execute with exec.
func (e *env) executeWith(exec func(context.Context, *fakePlatform, *pluginv1.ExecuteRequest, core.ExecutionClient) (core.ExecutionResult, error)) {
	e.gen.accountTypes[0].Client = &waitedPlugin{fakePlatform: e.plat, exec: exec}
}

// Nothing reaches the upstream while Execute runs: the relay is sent only
// after the plugin returned.
func TestNoWaitSendsOnlyAfterExecuteReturned(t *testing.T) {
	e := newEnv(t)
	entered, release := make(chan struct{}), make(chan struct{})
	e.plat.buildHook = func(context.Context, *pluginv1.ExecuteRequest) error {
		close(entered)
		<-release
		return nil
	}
	done := make(chan result, 1)
	go func() { done <- e.messages(body(testModel, false)) }()
	<-entered
	time.Sleep(100 * time.Millisecond)
	if k := e.up.keys(); len(k) != 0 {
		close(release)
		t.Fatalf("upstream called while Execute ran: %v", k)
	}
	close(release)
	if r := <-done; r.status != 200 || strings.Join(e.up.keys(), ",") != "acc-1" {
		t.Fatalf("after Execute: %d %v %s", r.status, e.up.keys(), r.body)
	}
	e.record()
	// The no-wait path writes no execution records.
	if st := e.settler.executionStates(); len(st) != 0 {
		t.Fatalf("execution records written: %v", st)
	}
}

// A plugin that fails before its relay was sent - an error, an Execute
// without a relay - fails over to the next account
// without blaming the account: nothing was sent for it and it is not cooled
// down.
func TestNoWaitPluginFailureFailsOverWithoutBlame(t *testing.T) {
	cases := map[string]func(e *env){
		"error": func(e *env) {
			e.plat.buildHook = func(_ context.Context, in *pluginv1.ExecuteRequest) error {
				if in.GetAccount().GetId() == 1 {
					return errors.New("plugin broke")
				}
				return nil
			}
		},
		"no_relay": func(e *env) {
			e.executeWith(func(ctx context.Context, p *fakePlatform, in *pluginv1.ExecuteRequest, client core.ExecutionClient) (core.ExecutionResult, error) {
				if in.GetAccount().GetId() == 1 {
					return core.ExecutionResult{Response: &pluginv1.ExecuteResponse{Report: &pluginv1.ExecuteResponse_HostUsage{HostUsage: &pluginv1.HostUsage{}}}}, nil
				}
				return fakeExecute(ctx, p, in, client)
			})
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			setup(e)
			r := e.messages(body(testModel, false))
			if r.status != 200 || strings.Join(e.up.keys(), ",") != "acc-2" {
				t.Fatalf("failover: %d %v %s", r.status, e.up.keys(), r.body)
			}
			rec := e.record()
			if len(e.accounts.cooldowns) != 0 || len(e.accounts.disabled) != 0 {
				t.Fatalf("account blamed: cooldowns %v disabled %v", e.accounts.cooldowns, e.accounts.disabled)
			}
			if rec.Attempts != 2 || *rec.AccountID != 2 || rec.AttemptTrail == nil || len(rec.AttemptTrail.Steps) != 2 {
				t.Fatalf("record %+v trail %+v", rec, rec.AttemptTrail)
			}
			if s := rec.AttemptTrail.Steps[0]; s.AccountID != 1 || s.ErrorType != errTypePluginUnavailable {
				t.Fatalf("first attempt %+v", s)
			}
		})
	}
}

// Every attempt is built anew for its account: after a failover the plugin
// gets attempt 1, the second account's credentials and the body rewritten
// with that account's model mapping.
func TestNoWaitRebuildsEveryAttempt(t *testing.T) {
	e := newEnv(t)
	e.accounts.set(1, func(acc *core.Account) { acc.ModelMapping = map[string]string{testModel: "model-of-1"} })
	e.accounts.set(2, func(acc *core.Account) { acc.ModelMapping = map[string]string{testModel: "model-of-2"} })
	e.up.set("acc-1", &upstreamRule{status: 500})
	if r := e.messages(body(testModel, false)); r.status != 200 {
		t.Fatalf("failover: %d %s", r.status, r.body)
	}
	e.record()
	b := e.plat.builds
	if len(b) != 2 {
		t.Fatalf("%d builds", len(b))
	}
	for i, want := range []struct {
		id    int64
		key   string
		model string
	}{{1, "acc-1", "model-of-1"}, {2, "acc-2", "model-of-2"}} {
		in := b[i]
		if in.GetAttempt() != int32(i) || in.GetAccount().GetId() != want.id ||
			gjson.Get(in.GetAccount().GetCredentialsJson(), "api_key").String() != want.key ||
			gjson.GetBytes(in.GetBody(), "model").String() != want.model || in.GetMeta().GetModel() != want.model {
			t.Fatalf("attempt %d: attempt=%d account=%d creds=%s model=%s meta=%s", i, in.GetAttempt(), in.GetAccount().GetId(),
				in.GetAccount().GetCredentialsJson(), gjson.GetBytes(in.GetBody(), "model"), in.GetMeta().GetModel())
		}
	}
	if m := gjson.GetBytes(e.up.last().body, "model").String(); m != "model-of-2" {
		t.Fatalf("upstream model %s", m)
	}
}

// The plugin is handed the body after the model mapping and the account's
// parameter override, and the upstream receives exactly the bytes the
// plugin returned.
func TestNoWaitPluginGetsRewrittenBodyUpstreamGetsPluginBytes(t *testing.T) {
	e := newEnv(t)
	e.accounts.set(1, func(acc *core.Account) { acc.ModelMapping = map[string]string{testModel: "mapped-model"} })
	p, errs := core.CompileParamOverride(context.Background(), "param_override", []byte(`{"operations":[{"mode":"set","path":"temperature","value":0.3}]}`))
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	e.accounts.set(1, func(acc *core.Account) { acc.ParamOverride = p })
	sent := []byte(`{"model":"plugin-model","stream":false,"plugin":true}`)
	e.executeWith(func(ctx context.Context, fp *fakePlatform, in *pluginv1.ExecuteRequest, client core.ExecutionClient) (core.ExecutionResult, error) {
		req, err := fp.buildUpstream(ctx, in)
		if err != nil {
			return core.ExecutionResult{}, err
		}
		req.Body = sent
		if err := client.Check(ctx, req); err != nil {
			return core.ExecutionResult{}, err
		}
		return core.ExecutionResult{Response: &pluginv1.ExecuteResponse{Report: &pluginv1.ExecuteResponse_HostUsage{HostUsage: &pluginv1.HostUsage{}}}, Relay: req}, nil
	})
	if r := e.messages(body(testModel, false)); r.status != 200 {
		t.Fatalf("status %d %s", r.status, r.body)
	}
	e.record()
	in := e.plat.builds[0]
	if gjson.GetBytes(in.GetBody(), "model").String() != "mapped-model" || gjson.GetBytes(in.GetBody(), "temperature").Float() != 0.3 {
		t.Fatalf("plugin got %s", in.GetBody())
	}
	if got := e.up.last().body; string(got) != string(sent) {
		t.Fatalf("upstream got %s, want the plugin's bytes %s", got, sent)
	}
}

// classifyFailing is the anthropic fake whose ClassifyError fails.
type classifyFailing struct{ *fakePlatform }

func (p *classifyFailing) ClassifyError(context.Context, *pluginv1.ClassifyErrorRequest) (*pluginv1.ClassifyErrorResponse, error) {
	p.mu.Lock()
	p.classify = append(p.classify, nil)
	p.mu.Unlock()
	return nil, errors.New("classifier down")
}

// An upstream 5xx of a relay sent without waiting is classified once by
// ClassifyError and fails over; when ClassifyError fails the default
// classification applies (failover, no account effect).
func TestNoWaitUpstreamErrorClassification(t *testing.T) {
	for _, name := range []string{"plugin", "default"} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			if name == "default" {
				e.gen.accountTypes[0].Client = &classifyFailing{e.plat}
			}
			e.up.set("acc-1", &upstreamRule{status: 503})
			r := e.messages(body(testModel, false))
			if r.status != 200 || strings.Join(e.up.keys(), ",") != "acc-1,acc-2" {
				t.Fatalf("failover: %d %v %s", r.status, e.up.keys(), r.body)
			}
			e.record()
			if n := len(e.plat.classify); n != 1 {
				t.Fatalf("ClassifyError called %d times", n)
			}
			cooled := slices.Contains(e.accounts.cooldowns, 1)
			if name == "plugin" && !cooled || name == "default" && cooled {
				t.Fatalf("cooldowns %v", e.accounts.cooldowns)
			}
		})
	}
}

// A classification Execute returns without a relay is the plugin's verdict
// on its own fetch that failed: it is applied (here a cooldown and a
// failover) and nothing is sent for the account.
func TestNoWaitClassificationWithoutRelayApplies(t *testing.T) {
	e := newEnv(t)
	e.executeWith(func(ctx context.Context, p *fakePlatform, in *pluginv1.ExecuteRequest, client core.ExecutionClient) (core.ExecutionResult, error) {
		if in.GetAccount().GetId() == 1 {
			return core.ExecutionResult{Response: &pluginv1.ExecuteResponse{Classification: &pluginv1.ClassifyErrorResponse{
				Action: pluginv1.ClassifyErrorResponse_ACTION_FAILOVER, AccountEffect: pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_COOLDOWN,
				CooldownUntilUnix: time.Now().Add(time.Minute).Unix(), Reason: "token refresh failed"}}}, nil
		}
		return fakeExecute(ctx, p, in, client)
	})
	r := e.messages(body(testModel, false))
	if r.status != 200 || strings.Join(e.up.keys(), ",") != "acc-2" {
		t.Fatalf("failover: %d %v %s", r.status, e.up.keys(), r.body)
	}
	if rec := e.record(); rec.Attempts != 2 || *rec.AccountID != 2 {
		t.Fatalf("record %+v", rec)
	}
	if !slices.Equal(e.accounts.cooldowns, []int64{1}) {
		t.Fatalf("cooldowns %v", e.accounts.cooldowns)
	}
}
