package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// mangledPlatform is fakePlatform whose relay for account 1 is changed by
// mangle before the core checks it.
type mangledPlatform struct {
	*fakePlatform
	mangle func(*pluginv1.UpstreamRequest)
}

func (p *mangledPlatform) Execute(ctx context.Context, in *pluginv1.ExecuteRequest, client core.ExecutionClient) (core.ExecutionResult, error) {
	return fakeExecute(ctx, p, in, client)
}

func (p *mangledPlatform) buildUpstream(ctx context.Context, in *pluginv1.ExecuteRequest) (*pluginv1.UpstreamRequest, error) {
	req, err := p.fakePlatform.buildUpstream(ctx, in)
	if err == nil && in.GetAccount().GetId() == 1 {
		p.mangle(req)
	}
	return req, err
}

// A plugin that cannot serve an attempt - its Execute fails, or it relays a
// request the core refuses (an invalid method, a refused address) - is the
// plugin's failure, never the account's: the request moves to the next
// account, with or without a published failure policy, and no account rule
// (here one cooling down on any core 503) runs for it.
func TestPluginFailureSwitchesAccount(t *testing.T) {
	cases := map[string]func(*env){
		"execute error": func(e *env) {
			e.plat.buildHook = func(_ context.Context, in *pluginv1.ExecuteRequest) error {
				if in.GetAccount().GetId() == 1 {
					return errors.New("plugin crashed")
				}
				return nil
			}
		},
		"refused relay": func(e *env) {
			e.gen.accountTypes[0].Client = &mangledPlatform{fakePlatform: e.plat, mangle: func(r *pluginv1.UpstreamRequest) { r.Method = "BAD METHOD" }}
		},
		"refused address": func(e *env) {
			e.gen.accountTypes[0].Client = &mangledPlatform{fakePlatform: e.plat, mangle: func(r *pluginv1.UpstreamRequest) { r.Url = "ftp://upstream.example/v1" }}
		},
	}
	for name, setup := range cases {
		for _, policy := range []bool{true, false} {
			t.Run(name+map[bool]string{true: "/policy", false: "/no policy"}[policy], func(t *testing.T) {
				e := newEnv(t)
				setup(e)
				if policy {
					p := testFailurePolicy()
					p.AccountEnabled = true
					p.AccountRules = []FailureRule{{ID: "cool", Name: "Cool", Enabled: true, Action: "cooldown", CooldownSeconds: 60,
						Match: FailureMatch{Source: "core", StatusCodes: "500-599"}}}
					e.setFailurePolicy(p)
				}
				r := e.messages(body(testModel, false))
				require.Equal(t, 200, r.status, string(r.body))
				require.Equal(t, []string{"acc-2"}, e.up.keys())
				require.Empty(t, e.accounts.cooldowns)
				require.Empty(t, e.accounts.disabled)
				rec := e.record()
				require.True(t, rec.Success)
				require.Equal(t, 2, rec.Attempts)
				require.NotNil(t, rec.AccountID)
				require.EqualValues(t, 2, *rec.AccountID)
				if policy {
					require.Contains(t, rec.FailurePolicy, `"account_action":"none"`)
				}
				e.noRecord()
			})
		}
	}
}

// Under a failure policy, a plugin down for every account still ends the
// loop: every account is tried once and excluded, and the client gets
// no_available_account.
func TestPluginDownForEveryAccountUnderFailurePolicy(t *testing.T) {
	e := newEnv(t)
	e.setFailurePolicy(testFailurePolicy())
	e.plat.buildHook = func(context.Context, *pluginv1.ExecuteRequest) error { return core.ErrPluginUnavailable }
	r := e.messages(body(testModel, false))
	require.Equal(t, 529, r.status, string(r.body))
	require.Equal(t, "no_available_account", r.json().Get("error.code").String())
	require.Empty(t, e.up.keys())
	require.Empty(t, e.accounts.cooldowns)
}

// A classification the plugin returns without a relay after the client left
// (its fetch cut by the departure) is not applied: the attempt is canceled
// and the account not cooled down.
func TestPluginClassificationAfterClientLeftIsIgnored(t *testing.T) {
	e := newEnv(t)
	entered := make(chan struct{})
	e.gen.accountTypes[0].Client = &waitedPlugin{fakePlatform: e.plat, exec: func(ctx context.Context, _ *fakePlatform, _ *pluginv1.ExecuteRequest, _ core.ExecutionClient) (core.ExecutionResult, error) {
		close(entered)
		<-ctx.Done()
		return core.ExecutionResult{Response: &pluginv1.ExecuteResponse{Classification: &pluginv1.ClassifyErrorResponse{
			Action: pluginv1.ClassifyErrorResponse_ACTION_FAILOVER, AccountEffect: pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_COOLDOWN,
			Reason: "fetch failed"}}}, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	raw, _ := json.Marshal(body(testModel, false))
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, e.srv.URL+"/v1/messages", bytes.NewReader(raw))
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
	rec := e.record()
	require.Equal(t, errTypeClientCanceled, rec.ErrorType)
	require.Equal(t, 1, rec.Attempts)
	require.Empty(t, e.accounts.cooldowns)
	require.Empty(t, e.up.keys())
}
