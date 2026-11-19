package gateway

import (
	"context"
	"strings"
	"testing"
	"time"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// ---------------------------------------------------------------- environment

// resolveEnv adds a plugin "rm" declaring platform "rm" with one endpoint at
// POST /rm/v1/tasks and an account type serving it (account 9, priority 0).
// req is the endpoint's request declaration; withClient controls whether the
// platform binding carries the plugin's PlatformService, as a plugin without
// platform.adapter.v1 would not.
func resolveEnv(t *testing.T, req manifest.EndpointRequest, withClient bool) (*env, *fakePlatform) {
	t.Helper()
	rp := &fakePlatform{}
	e := newEnv(t, func(e *env) {
		rp.base = e.up.srv.URL
		ep := manifest.Endpoint{ID: "gen", Method: "POST", Path: "/rm/v1/tasks", Protocol: "rm.gen", Kind: "proxy",
			Auth:     manifest.EndpointAuth{Headers: []string{"x-api-key"}, Query: "key"},
			Request:  req,
			Response: manifest.EndpointResp{NonStream: "json"}, ErrorFormat: "plain", Billing: "usage", BillingTypes: []string{"per_request", "per_token", "expression"}}
		pf := manifest.Platform{ID: "rm", Usage: builtinPlatform(t, "anthropic").Usage,
			PassHeaders: []string{"x-rm-hint"},
			Endpoints:   []manifest.Endpoint{ep}}
		info := e.addAccountType("rm", "rm_key", rp, manifest.AccountPlatform{Platform: "rm"})
		if withClient {
			e.gen.addPlatform(info, pf, rp)
		} else {
			e.gen.addPlatform(info, pf)
		}
		e.accounts.addTyped(testGroup, 9, 100, "rm-9", "rm", "rm_key")
		e.accounts.groups[testGroup] = []int64{9}
	})
	return e, rp
}

// pluginModelRequest is the endpoint declaration under test: the model is not
// in the request at all, and two query parameters are declared.
func pluginModelRequest() manifest.EndpointRequest {
	return manifest.EndpointRequest{ModelSource: manifest.ModelSourcePlugin, QueryParams: []string{"alt", "page"}}
}

// taskBody carries no model anywhere: only the plugin can supply one.
func taskBody() map[string]any {
	return map[string]any{"task": "t-1", "max_tokens": 64,
		"messages": []any{map[string]any{"role": "user", "content": "hello there"}}}
}

// ---------------------------------------------------------------- ResolveModel

// An endpoint with request.modelSource "plugin" gets its model (and the
// stream flag) from the plugin declaring the platform, before scheduling, and
// the request is then priced, limited and recorded on that model like any
// other.
func TestResolveModelFromPlugin(t *testing.T) {
	e, rp := resolveEnv(t, pluginModelRequest(), true)
	rp.resolve = func(_ context.Context, in *pluginv1.ResolveModelRequest) (*pluginv1.ResolveModelResponse, error) {
		return &pluginv1.ResolveModelResponse{Model: testModel}, nil
	}
	r := e.do("/rm/v1/tasks?alt=SSE&page=2&secret=x&Key=sk-leak", taskBody(), map[string]string{"x-rm-hint": "h"})
	if r.status != 200 {
		t.Fatalf("status %d %s", r.status, r.body)
	}
	if rp.resolveCount() != 1 {
		t.Fatalf("ResolveModel called %d times", rp.resolveCount())
	}
	in := rp.resolves[0]
	// The model is what this call answers, so meta.model is still empty.
	if in.GetMeta().GetModel() != "" || in.GetMeta().GetProtocol() != "rm.gen" {
		t.Fatalf("meta %+v", in.GetMeta())
	}
	if in.GetInboundHeaders()["x-rm-hint"] != "h" {
		t.Fatalf("inbound headers %v", in.GetInboundHeaders())
	}
	// Only the declared parameters, matched case-insensitively; auth.query
	// ("key") is excluded whatever its case, and "secret" was never declared.
	q := in.GetMeta().GetQuery()
	if len(q) != 2 || q["alt"] != "SSE" || q["page"] != "2" {
		t.Fatalf("query %v", q)
	}
	// The whole pipeline saw the resolved model.
	rec := e.record()
	if rec.Model != testModel || !rec.Success || rec.Price == nil || rec.Price.ID != 9 || !rec.Billable || rec.Stream {
		t.Fatalf("record %+v", rec)
	}
	if e.pricer.calls != 1 {
		t.Fatalf("price resolved %d times", e.pricer.calls)
	}
	// The scheduled account had to pass the model filter, so it is the one the
	// plugin's model allowed.
	if rec.AccountID == nil || *rec.AccountID != 9 {
		t.Fatalf("account %+v", rec.AccountID)
	}
}

// The plugin also decides whether the response streams; request.streamPath
// may not be declared next to modelSource "plugin" (manifest validation), so
// this is the only source.
func TestResolveModelSetsStream(t *testing.T) {
	e, rp := resolveEnv(t, pluginModelRequest(), true)
	rp.resolve = func(_ context.Context, in *pluginv1.ResolveModelRequest) (*pluginv1.ResolveModelResponse, error) {
		return &pluginv1.ResolveModelResponse{Model: testModel, Stream: true}, nil
	}
	// The fake upstream answers JSON; only the recorded flag matters here.
	e.do("/rm/v1/tasks", taskBody(), nil)
	if rec := e.record(); !rec.Stream || rec.Model != testModel {
		t.Fatalf("record %+v", rec)
	}
}

// request.stream ("this endpoint always streams") is an endpoint-level fact
// and wins over the plugin: a plugin answering false would have the host read
// an SSE response as JSON.
func TestResolveModelCannotTurnStreamOff(t *testing.T) {
	req := pluginModelRequest()
	req.Stream = true
	e, rp := resolveEnv(t, req, true)
	rp.resolve = func(_ context.Context, in *pluginv1.ResolveModelRequest) (*pluginv1.ResolveModelResponse, error) {
		return &pluginv1.ResolveModelResponse{Model: testModel, Stream: false}, nil
	}
	e.do("/rm/v1/tasks", taskBody(), nil)
	if rec := e.record(); !rec.Stream {
		t.Fatalf("record %+v", rec)
	}
}

// Every way of not getting a model ends the request with 400: without one the
// host cannot price or limit it, so serving it would serve it for free.
func TestResolveModelFailuresAre400(t *testing.T) {
	cases := []struct {
		name string
		fn   func(ctx context.Context, in *pluginv1.ResolveModelRequest) (*pluginv1.ResolveModelResponse, error)
	}{
		{"empty model", func(context.Context, *pluginv1.ResolveModelRequest) (*pluginv1.ResolveModelResponse, error) {
			return &pluginv1.ResolveModelResponse{}, nil
		}},
		{"error", func(context.Context, *pluginv1.ResolveModelRequest) (*pluginv1.ResolveModelResponse, error) {
			return nil, context.Canceled
		}},
		{"nil response", func(context.Context, *pluginv1.ResolveModelRequest) (*pluginv1.ResolveModelResponse, error) {
			return nil, nil
		}},
		// nil resolve = UNIMPLEMENTED, a plugin that never wrote the method.
		{"unimplemented", nil},
		{"timeout", func(ctx context.Context, _ *pluginv1.ResolveModelRequest) (*pluginv1.ResolveModelResponse, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, rp := resolveEnv(t, pluginModelRequest(), true)
			e.setSettings(GatewaySettings{MaxAttempts: 3, PlatformCallTimeoutMs: 2000, DefaultHookTimeoutMs: 50},
				StickySettings{})
			rp.resolve = tc.fn
			start := time.Now()
			r := e.do("/rm/v1/tasks", taskBody(), nil)
			if r.status != 400 || !strings.Contains(r.json().Get("error").String(), "model is required") {
				t.Fatalf("status %d %s", r.status, r.body)
			}
			if tc.name == "timeout" && time.Since(start) > 3*time.Second {
				t.Fatalf("no deadline on ResolveModel: %v", time.Since(start))
			}
			if rp.buildCount() != 0 {
				t.Fatal("upstream request built without a model")
			}
			rec := e.record()
			if rec.Success || rec.StatusCode != 400 || rec.ErrorType != errTypeInvalidRequest || rec.Model != "" {
				t.Fatalf("record %+v", rec)
			}
			// Nothing was priced: there was no model to price.
			if e.pricer.calls != 0 {
				t.Fatalf("priced %d times", e.pricer.calls)
			}
		})
	}
}

// A plugin may legally declare a platform without platform.adapter.v1
// (CONTRACTS §13), which leaves PlatformBinding.Client nil. Install-time
// validation rejects that combination since B, but a package installed
// earlier can still reach here: that is a host-side misconfiguration, so it is
// a 500, not a client error.
func TestResolveModelWithoutClientIs500(t *testing.T) {
	e, rp := resolveEnv(t, pluginModelRequest(), false)
	r := e.do("/rm/v1/tasks", taskBody(), nil)
	if r.status != 500 {
		t.Fatalf("status %d %s", r.status, r.body)
	}
	if rp.resolveCount() != 0 {
		t.Fatal("ResolveModel called on a nil client")
	}
	if rec := e.record(); rec.Success || rec.ErrorType != errTypeInternal {
		t.Fatalf("record %+v", rec)
	}
}

// An endpoint that reads the model out of the request never costs a
// ResolveModel call, even when its platform plugin implements one.
func TestResolveModelNotCalledWithoutDeclaration(t *testing.T) {
	e, rp := resolveEnv(t, manifest.EndpointRequest{ModelPath: "model", StreamPath: "stream"}, true)
	rp.resolve = func(context.Context, *pluginv1.ResolveModelRequest) (*pluginv1.ResolveModelResponse, error) {
		t.Error("ResolveModel called for an endpoint without request.modelSource")
		return &pluginv1.ResolveModelResponse{Model: "other"}, nil
	}
	b := taskBody()
	b["model"] = testModel
	if r := e.do("/rm/v1/tasks", b, nil); r.status != 200 {
		t.Fatalf("status %d %s", r.status, r.body)
	}
	if rp.resolveCount() != 0 {
		t.Fatalf("ResolveModel called %d times", rp.resolveCount())
	}
	if rec := e.record(); rec.Model != testModel {
		t.Fatalf("record %+v", rec)
	}
	// And the built-in endpoints do not call it either.
	e2 := newEnv(t)
	if r := e2.messages(body(testModel, false)); r.status != 200 {
		t.Fatalf("builtin status %d", r.status)
	}
	if e2.plat.resolveCount() != 0 {
		t.Fatalf("ResolveModel called %d times for a built-in endpoint", e2.plat.resolveCount())
	}
}

// A hook that patches the body invalidates the resolved model: the plugin is
// asked again, exactly once, because the answer may depend on what the hook
// changed. Without a patch the first answer is reused.
func TestResolveModelReaskedOnlyWhenTheBodyChanged(t *testing.T) {
	for _, patch := range []bool{false, true} {
		name := "unchanged body"
		if patch {
			name = "hook patched the body"
		}
		t.Run(name, func(t *testing.T) {
			e, rp := resolveEnv(t, pluginModelRequest(), true)
			rp.resolve = func(context.Context, *pluginv1.ResolveModelRequest) (*pluginv1.ResolveModelResponse, error) {
				return &pluginv1.ResolveModelResponse{Model: testModel}, nil
			}
			h := &fakeHook{fn: func(context.Context, *pluginv1.GatewayRequestHookRequest) (*pluginv1.GatewayRequestHookResponse, error) {
				resp := &pluginv1.GatewayRequestHookResponse{Decision: pluginv1.GatewayRequestHookResponse_DECISION_ALLOW}
				if patch {
					resp.Patches = []*pluginv1.BodyPatch{{Op: pluginv1.BodyPatch_OP_SET, Path: "task", ValueJson: `"t-2"`}}
				}
				return resp, nil
			}}
			e.addHook(manifest.Hook{ID: "h", Point: "gateway.request", Needs: []string{"task"}}, []string{"task"}, h)
			if r := e.do("/rm/v1/tasks", taskBody(), nil); r.status != 200 {
				t.Fatalf("status %d %s", r.status, r.body)
			}
			want := 1
			if patch {
				want = 2
			}
			if rp.resolveCount() != want {
				t.Fatalf("ResolveModel called %d times, want %d", rp.resolveCount(), want)
			}
			e.record()
		})
	}
}
