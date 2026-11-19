package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/account"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

func testFailurePolicy() FailurePolicy {
	p := defaultFailurePolicy()
	p.SchemaVersion = 1 // Legacy per-rule plans remain supported until explicitly migrated.
	p.Egress, p.MaxWaitMS, p.RespectRetryAfter = "same_proxy", 5000, true
	p.Revision, p.RetryEnabled, p.BackoffMS, p.RespectRetryAfter = 1, true, 0, false
	p.RetryRules = []FailureRule{{ID: "retry", Name: "Retry HTTP", Enabled: true,
		Match: FailureMatch{Source: "upstream", StatusCodes: "429,500-599"}, Action: failureReset, Egress: "same_proxy"}}
	return p
}

func (e *env) setFailurePolicy(p FailurePolicy) {
	e.t.Helper()
	require.Empty(e.t, p.validate(e.t.Context()))
	e.gw.settings.mu.Lock()
	defer e.gw.settings.mu.Unlock()
	o := *e.gw.settings.override
	o.failurePolicy = &p
	e.gw.settings.override = &o
}

func TestFailurePolicyMatchConditions(t *testing.T) {
	for _, tc := range []struct {
		name, status, text, mode string
		keywords                 []string
		want                     bool
	}{
		{"status only", "429", "anything", "all", nil, true},
		{"blank keywords", "429", "anything", "all", []string{" ", ""}, true},
		{"and mismatch", "429", "anything", "all", []string{"quota"}, false},
		{"and match case", "429", "QUOTA exceeded", "all", []string{" quota "}, true},
		{"or status", "429", "anything", "any", []string{"quota"}, true},
		{"or keyword", "500-599", "quota exceeded", "any", []string{"quota"}, true},
		{"keyword only", "", "quota exceeded", "all", []string{"quota"}, true},
		{"range misses", "500-599", "anything", "all", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := testFailurePolicy()
			p.RetryRules[0].Match.StatusCodes, p.RetryRules[0].Match.Keywords, p.RetryRules[0].Match.Mode = tc.status, tc.keywords, tc.mode
			require.Empty(t, p.validate(t.Context()))
			d := p.decide(FailureFacts{Source: "upstream", Status: 429, Text: tc.text})
			require.Equal(t, tc.want, d.Action == failureReset)
		})
	}
	p := testFailurePolicy()
	p.RetryRules[0].Match.StatusCodes = ""
	require.NotEmpty(t, p.validate(t.Context()))
	p.RetryRules[0].Match.Source = "transport"
	require.Empty(t, p.validate(t.Context()))
	require.Equal(t, failureReset, p.decide(FailureFacts{Source: "transport"}).Action)
	require.Equal(t, failureReturn, p.decide(FailureFacts{Source: "upstream", Status: 502}).Action)
}

func TestFailurePolicyPriorityAndScope(t *testing.T) {
	p := testFailurePolicy()
	exception := p.RetryRules[0]
	exception.ID, exception.Action = "exception", failureReturn
	exception.Match.Groups, exception.Match.Models, exception.Match.Protocols = []int64{7}, []string{"claude-*"}, []string{"anthropic"}
	p.RetryRules = append([]FailureRule{exception}, p.RetryRules...)
	require.Empty(t, p.validate(t.Context()))
	d := p.decide(FailureFacts{Source: "upstream", Status: 503, GroupID: 7, Model: "claude-test", Protocol: "anthropic"})
	require.Equal(t, "exception", d.RetryRule)
	d = p.decide(FailureFacts{Source: "upstream", Status: 503, GroupID: 8, Model: "claude-test", Protocol: "anthropic"})
	require.Equal(t, "retry", d.RetryRule)
}

func TestFailurePolicyCoreControlsRetries(t *testing.T) {
	for _, tc := range []struct {
		name     string
		enabled  bool
		max      int
		action   string
		expected []string
	}{
		{"off", false, 2, failureReset, []string{"acc-1"}},
		{"zero", true, 0, failureReset, []string{"acc-1"}},
		{"switch", true, 2, failureReset, []string{"acc-1", "acc-2"}},
		{"same then switch", true, 2, failureRetry, []string{"acc-1", "acc-1", "acc-2"}},
		{"budget shared", true, 1, failureRetry, []string{"acc-1", "acc-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			p := testFailurePolicy()
			p.RetryEnabled, p.MaxRetries = tc.enabled, tc.max
			p.RetryRules[0].Action, p.RetryRules[0].SameAccountRetries, p.RetryRules[0].ExhaustedAction = tc.action, 1, failureReset
			e.setFailurePolicy(p)
			// The plugin's retry and cooldown suggestions are not the policy's
			// to follow; only a temporary disable is taken (CONTRACTS §69).
			e.plat.classifyHook = func(_ *pluginv1.ClassifyErrorRequest, out *pluginv1.ClassifyErrorResponse) {
				out.Action = pluginv1.ClassifyErrorResponse_ACTION_RETURN_TO_CLIENT
				out.AccountEffect = pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_COOLDOWN
				out.CooldownUntilUnix = time.Now().Add(time.Hour).Unix()
			}
			e.up.set("acc-1", &upstreamRule{status: 503})
			r := e.messages(body(testModel, false))
			require.Equal(t, tc.expected, e.up.keys())
			want := 503
			if tc.expected[len(tc.expected)-1] == "acc-2" {
				want = 200
			}
			require.Equal(t, want, r.status)
			require.NotEmpty(t, e.record().FailurePolicy)
			require.Empty(t, e.accounts.cooldowns, "plugin cooldown applied under a core policy")
		})
	}
}

// With a published failure policy the plugin still decides what an answer
// means for the account: a 429 it disables until the quota reset makes the
// account auto_disabled at once, so the policy's retry and the sticky session
// move to another account (CONTRACTS §69).
func TestFailurePolicyTakesPluginTemporaryDisable(t *testing.T) {
	e := newEnv(t)
	p := testFailurePolicy()
	p.RetryRules[0].Action, p.RetryRules[0].SameAccountRetries, p.RetryRules[0].ExhaustedAction = failureRetry, 2, failureReset
	p.RetryRules[0].Match.StatusCodes = "429"
	p.MaxRetries = 3
	e.enableSticky(onFailureFailover) // replaces the settings: before the policy
	e.setFailurePolicy(p)
	reset := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	e.plat.classifyHook = func(in *pluginv1.ClassifyErrorRequest, out *pluginv1.ClassifyErrorResponse) {
		if in.GetStatus() == 429 {
			out.AutomaticState = &pluginv1.AccountAutomaticState{Action: pluginv1.AccountAutomaticState_ACTION_DISABLE,
				Reason: "rate limited until reset", Release: &pluginv1.AccountAutomaticState_UntilUnix{UntilUnix: reset.Unix()}}
		}
	}
	e.up.set("acc-1", &upstreamRule{status: 429})
	r := e.messages(withSession(body(testModel, false), "sess-A"))
	require.Equal(t, 200, r.status, string(r.body))
	// The same-account retry is skipped: the account is disabled already.
	require.Equal(t, []string{"acc-1", "acc-2"}, e.up.keys())
	require.Equal(t, "rate limited until reset", e.accounts.disabled[1])
	require.Equal(t, reset.Unix(), e.accounts.releases[1].Unix())
	rec := e.record()
	require.Contains(t, rec.FailurePolicy, "disabled_by_plugin", "%+v", rec)
	// The session stays on the account it moved to.
	e.up.set("acc-1", nil)
	e.messages(withSession(body(testModel, false), "sess-A"))
	require.Equal(t, []string{"acc-1", "acc-2", "acc-2"}, e.up.keys())
	e.record()
}

func TestFailurePolicyDisableIsIndependent(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		enabled, optOut, retry bool
		disabled               bool
		attempts               int
	}{
		{"disabled feature", false, false, false, false, 1},
		{"account opts out", true, true, false, false, 1},
		{"disable without retry", true, false, false, true, 1},
		{"retry without disable", false, false, true, false, 2},
		{"both", true, false, true, true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			p := testFailurePolicy()
			p.AccountEnabled, p.RetryEnabled = tc.enabled, tc.retry
			p.AccountRules = []FailureRule{{ID: "disable", Name: "Disable", Enabled: true, Match: FailureMatch{Source: "upstream", StatusCodes: "429"}, Action: "disable"}}
			e.setFailurePolicy(p)
			e.accounts.noAutoDisable[1] = tc.optOut
			e.up.set("acc-1", &upstreamRule{status: 429})
			e.messages(body(testModel, false))
			_, disabled := e.accounts.disabled[1]
			require.Equal(t, tc.disabled, disabled)
			require.Empty(t, e.accounts.cooldowns, "disabled policy must not fall back to cooldown")
			require.Len(t, e.up.keys(), tc.attempts)
		})
	}
}

func TestFailurePolicyStickyResetAndPreserve(t *testing.T) {
	for _, action := range []string{failureSwitch, failureReset} {
		t.Run(action, func(t *testing.T) {
			e := newEnv(t)
			lim := newFakeLimiter()
			e.gw.d.Limiter = lim
			e.enableSticky(onFailureStick)
			p := testFailurePolicy()
			p.RetryRules[0].Action = action
			e.setFailurePolicy(p)
			sess := withSession(body(testModel, false), "user_test_account__session_policy")
			require.Equal(t, 200, e.messages(sess).status)
			e.record()
			var key string
			for _, k := range e.mr.Keys() {
				if strings.HasPrefix(k, "sticky:claude-code-session:") {
					key = k
				}
			}
			require.NotEmpty(t, key)
			before, err := e.rdb.Get(t.Context(), key).Result()
			require.NoError(t, err)
			require.Equal(t, int64(1), bindingAccount(before))
			e.up.set("acc-1", &upstreamRule{status: 503})
			require.Equal(t, 200, e.messages(sess).status)
			e.record()
			after, err := e.rdb.Get(t.Context(), key).Result()
			require.NoError(t, err)
			if action == failureSwitch {
				require.Equal(t, before, after)
				require.Empty(t, lim.released, "a kept binding keeps the session on its account")
			} else {
				require.Equal(t, int64(2), bindingAccount(after))
				// The session moved with its binding: account 1 no longer holds it.
				require.Equal(t, []string{"1:" + key}, lim.released)
			}
		})
	}
}

// A request without a sticky session is a session of its own (CONTRACTS
// §71.2): retried on the same account it is one session there, not one per
// admission, and it leaves the account a retry moves it from, whether or not
// the retry keeps sticky bindings: it has none (§71.3).
func TestFailurePolicyRequestSessionCountsOnce(t *testing.T) {
	for _, exhausted := range []string{failureReset, failureSwitch} {
		t.Run(exhausted, func(t *testing.T) {
			e := newEnv(t)
			e.gw.d.Limiter = account.NewLimiter(e.rdb)
			e.accounts.set(1, func(acc *core.Account) { acc.SPMLimit = 1 })
			p := testFailurePolicy()
			p.MaxRetries = 2
			p.RetryRules[0].Action, p.RetryRules[0].SameAccountRetries, p.RetryRules[0].ExhaustedAction = failureRetry, 1, exhausted
			e.setFailurePolicy(p)
			e.up.set("acc-1", &upstreamRule{status: 503})
			require.Equal(t, 200, e.messages(body(testModel, false)).status)
			e.record()
			// The same-account retry fits account 1's cap of one session: its own.
			require.Equal(t, []string{"acc-1", "acc-1", "acc-2"}, e.up.keys())
			u, err := e.gw.d.Limiter.Usage(t.Context(), []core.AccountRef{{ID: 1}, {ID: 2}})
			require.NoError(t, err)
			require.Equal(t, int64(0), u[1].Sessions, "the retry took the request off account 1")
			require.Equal(t, int64(2), u[1].RPM, "its requests stay counted")
			require.Equal(t, int64(1), u[2].Sessions)
		})
	}
}

// A session has one account: the first claim wins and later ones learn it, and
// a release only takes the session off the account it names.
func TestStickyClaimAndRelease(t *testing.T) {
	e := newEnv(t)
	session := func() *call {
		return &call{g: e.gw, sticky: &stickySession{key: "claim", ttl: time.Hour}}
	}
	first, second := session(), session()
	require.Equal(t, int64(1), first.claimBinding(t.Context(), 1))
	require.Equal(t, int64(1), first.sticky.bound)
	require.Equal(t, int64(1), second.claimBinding(t.Context(), 2), "a concurrent request follows the first")
	require.Equal(t, int64(0), second.sticky.bound)

	// Two requests of the session fail on account 1 at once: both move on.
	require.NoError(t, first.releaseBinding(t.Context(), 1))
	require.Equal(t, int64(0), first.sticky.bound)
	require.Equal(t, int64(2), first.claimBinding(t.Context(), 2))
	second.sticky.bound = 1
	require.NoError(t, second.releaseBinding(t.Context(), 1), "losing the race is no failure")
	require.Equal(t, int64(2), second.sticky.bound, "the later request follows the new binding")
	require.False(t, second.sticky.tried)

	raw, _ := e.rdb.Get(t.Context(), "claim").Result()
	require.Equal(t, int64(2), bindingAccount(raw))

	// A binding written by an older node is understood.
	require.NoError(t, e.rdb.Set(t.Context(), "claim", "v1:gen:7", time.Hour).Err())
	require.Equal(t, int64(7), session().claimBinding(t.Context(), 3))
	old := session()
	require.NoError(t, old.releaseBinding(t.Context(), 7))
	require.Equal(t, int64(3), old.claimBinding(t.Context(), 3))
}

func TestFailurePolicyEgressConstraint(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want []string
	}{
		{"same_proxy", []string{"acc-1"}}, {"proxy_only", []string{"acc-1", "acc-3"}}, {"account_configured", []string{"acc-1", "acc-2"}},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			e := newEnv(t)
			one, two := int64(10), int64(20)
			e.accounts.accounts[1].ProxyID, e.accounts.accounts[3].ProxyID = &one, &two
			p := testFailurePolicy()
			p.RetryRules[0].Egress = tc.mode
			e.setFailurePolicy(p)
			e.up.set("acc-1", &upstreamRule{status: 503})
			e.messages(body(testModel, false))
			require.Equal(t, tc.want, e.up.keys())
		})
	}
}

func TestFailurePolicyWaitAndUnsafeTransport(t *testing.T) {
	t.Run("Retry-After exceeds budget", func(t *testing.T) {
		e := newEnv(t)
		p := testFailurePolicy()
		p.RespectRetryAfter, p.MaxWaitMS = true, 1
		e.setFailurePolicy(p)
		e.up.set("acc-1", &upstreamRule{status: 429})
		require.Equal(t, 429, e.messages(body(testModel, false)).status)
		require.Len(t, e.up.keys(), 1)
		require.Contains(t, e.record().FailurePolicy, "wait_budget")
	})
	t.Run("received request without response is not replayed", func(t *testing.T) {
		var calls atomic.Int32
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
		}))
		defer up.Close()
		e := newEnv(t)
		e.plat.base = up.URL
		p := testFailurePolicy()
		p.RetryRules[0].Match.Source = "transport"
		p.RetryRules[0].Match.StatusCodes = ""
		e.setFailurePolicy(p)
		require.Equal(t, 502, e.messages(body(testModel, false)).status)
		require.Equal(t, int32(1), calls.Load())
		require.Contains(t, e.record().FailurePolicy, "outcome_uncertain")
	})
}

func TestFailurePolicyCommittedAndHardReturn(t *testing.T) {
	for _, committed := range []bool{false, true} {
		e := newEnv(t)
		p := testFailurePolicy()
		require.Empty(t, p.validate(t.Context()))
		w := httptest.NewRecorder()
		gc, _ := gin.CreateTestContext(w)
		gc.Request = httptest.NewRequest("POST", "/", nil)
		if committed {
			gc.Writer.WriteHeaderNow()
		}
		c := &call{g: e.gw, c: gc, failurePolicy: &p, rec: &core.UsageRecord{}}
		res := c.classifyFailure(t.Context(), 1, 503, nil, nil, "", false)
		if !committed {
			res.kind = attemptReturn
		}
		res = c.planFailure(t.Context(), &core.AccountRef{ID: 1}, res, 1, map[int64]bool{})
		require.Equal(t, attemptReturn, res.kind)
	}
}

func TestFailurePolicyAPI(t *testing.T) {
	e := newDBEnv(t)
	code, v := e.api("GET", "/settings/failure-policy", nil)
	require.Equal(t, 200, code)
	require.False(t, v.Get("data.configured").Bool())
	p := testFailurePolicy()
	p.Revision = 0
	p.RetryRules[0].Match.Keywords = []string{"", " "}
	code, v = e.api("PUT", "/settings/failure-policy", publishFailurePolicy{Policy: p})
	require.Equal(t, 200, code, v.Raw)
	require.Equal(t, int64(1), v.Get("data.policy.revision").Int())
	require.Empty(t, v.Get("data.policy.retry_rules.0.match.keywords").Array())
	code, _ = e.api("PUT", "/settings/failure-policy", publishFailurePolicy{Policy: p})
	require.Equal(t, 409, code)
	code, _ = e.api("PUT", "/settings/gateway", map[string]any{"max_attempts": 4})
	require.Equal(t, 409, code)
	code, _ = e.api("PUT", "/settings/gateway", map[string]any{"platform_call_timeout_ms": 3000})
	require.Equal(t, 200, code)
	// What GET returns saves back unchanged, max_attempts included.
	code, gw := e.api("GET", "/settings/gateway", nil)
	require.Equal(t, 200, code)
	code, v = e.api("PUT", "/settings/gateway", json.RawMessage(gw.Get("data").Raw))
	require.Equal(t, 200, code, v.Raw)
	require.Equal(t, gw.Get("data").Raw, v.Get("data").Raw)
	code, _ = e.api("PUT", "/settings/auto-disable", map[string]any{"enabled": false})
	require.Equal(t, 409, code)
	code, v = e.api("POST", "/settings/failure-policy/preview", map[string]any{"policy": p, "facts": FailureFacts{Source: "upstream", Status: 503}})
	require.Equal(t, 200, code)
	require.Equal(t, failureReset, v.Get("data.decision.action").String())
	p.RetryEnabled = false
	code, v = e.api("PUT", "/settings/failure-policy", publishFailurePolicy{ExpectedRevision: 1, Policy: p})
	require.Equal(t, 200, code, v.Raw)
	code, v = e.api("GET", "/settings/failure-policy/history", nil)
	require.Equal(t, 200, code)
	require.Equal(t, int64(1), v.Get("data.policies.0.revision").Int())
	require.False(t, e.gw.settings.get(t.Context()).failurePolicy.RetryEnabled)
	_, err := e.db.SQL.ExecContext(t.Context(), `UPDATE settings SET value='{"revision":3,"schema_version":99}' WHERE key='failure_policy'`)
	require.NoError(t, err)
	e.gw.settings.invalidate()
	require.Error(t, e.gw.settings.get(t.Context()).failurePolicyErr)
}

func TestFailurePolicySnapshotFrozen(t *testing.T) {
	e := newEnv(t)
	p := testFailurePolicy()
	e.setFailurePolicy(p)
	old := e.gw.settings.get(context.Background())
	p.RetryEnabled = false
	p.Revision = 2
	e.setFailurePolicy(p)
	require.True(t, old.failurePolicy.RetryEnabled)
	raw, err := json.Marshal(old.failurePolicy)
	require.NoError(t, err)
	require.Equal(t, int64(1), gjson.GetBytes(raw, "revision").Int())
}

func TestFailurePolicyRechecksChangedCandidateModels(t *testing.T) {
	e := newEnv(t)
	e.setFailurePolicy(testFailurePolicy())
	e.up.set("acc-1", &upstreamRule{status: 503})
	e.plat.buildHook = func(_ context.Context, in *pluginv1.ExecuteRequest) error {
		if in.Account.Id == 1 {
			e.accounts.mu.Lock()
			e.accounts.accounts[2].Models = []string{"another-model"}
			e.accounts.mu.Unlock()
		}
		return nil
	}
	require.Equal(t, 200, e.messages(body(testModel, false)).status)
	require.Equal(t, []string{"acc-1", "acc-3"}, e.up.keys())
}

func TestFailurePolicyRejectsNullBudgets(t *testing.T) {
	for _, field := range []string{"max_retries", "retries_per_account", "backoff_ms", "max_wait_ms"} {
		raw, err := json.Marshal(testFailurePolicy())
		require.NoError(t, err)
		var fields map[string]any
		require.NoError(t, json.Unmarshal(raw, &fields))
		fields[field] = nil
		raw, err = json.Marshal(fields)
		require.NoError(t, err)
		var parsed FailurePolicy
		require.Error(t, json.Unmarshal(raw, &parsed))
	}
}

func TestFailurePolicyStickyAllAccountsFail(t *testing.T) {
	e := newEnv(t)
	e.enableSticky(onFailureStick)
	e.setFailurePolicy(testFailurePolicy())
	body := withSession(body(testModel, false), "user_test_account__session_allfail")
	require.Equal(t, 200, e.messages(body).status)
	e.record()
	for _, key := range []string{"acc-1", "acc-2", "acc-3"} {
		e.up.set(key, &upstreamRule{status: 503})
	}
	require.Equal(t, 503, e.messages(body).status)
	for _, key := range e.mr.Keys() {
		if strings.HasPrefix(key, "sticky:claude-code-session:") {
			raw, _ := e.rdb.Get(t.Context(), key).Result()
			require.Zero(t, bindingAccount(raw))
		}
	}
}

func TestFailurePolicyCannotRelaxEgressInLaterRules(t *testing.T) {
	e := newEnv(t)
	one, two := int64(10), int64(20)
	e.accounts.accounts[1].ProxyID, e.accounts.accounts[2].ProxyID, e.accounts.accounts[3].ProxyID = &one, &one, &two
	p := testFailurePolicy()
	p.RetryRules[0].Match.StatusCodes = "503"
	loose := p.RetryRules[0]
	loose.ID, loose.Match.StatusCodes, loose.Egress = "loose", "429", "account_configured"
	p.RetryRules = append(p.RetryRules, loose)
	e.setFailurePolicy(p)
	e.up.set("acc-1", &upstreamRule{status: 503})
	e.up.set("acc-2", &upstreamRule{status: 429})
	require.Equal(t, 429, e.messages(body(testModel, false)).status)
	require.Equal(t, []string{"acc-1", "acc-2"}, e.up.keys())
}

func TestFailurePolicyOldRequestCannotOverwriteNewBinding(t *testing.T) {
	e := newEnv(t)
	old := &stickySession{rule: &stickyRule{Name: "test"}, key: "policy-old", ttl: time.Hour, bound: 1}
	require.NoError(t, e.rdb.Set(t.Context(), old.key, "2", time.Hour).Err())
	oldCall := &call{g: e.gw, sticky: old, rec: &core.UsageRecord{}}
	oldCall.finishSticky(t.Context(), 1, true)
	oldCall.dropBinding(t.Context(), old)
	raw, _ := e.rdb.Get(t.Context(), old.key).Result()
	require.Equal(t, int64(2), bindingAccount(raw))
}

// A 429 is retried only when another account can help; the bodies are the ones
// production answered with (2026-11-04 request logs).
func TestDefaultRetryRulesOnlyRetryUsefulRateLimits(t *testing.T) {
	p := migrationDraft(GatewaySettings{MaxAttempts: 4}, AutoDisableSettings{})
	require.Empty(t, p.validate(t.Context()))
	for _, tc := range []struct {
		name   string
		status int
		text   string
		rule   string
	}{
		{"quota", 429, `{"type":"error","error":{"type":"rate_limit_error","message":"This request would exceed your account's rate limit. Please try again later."},"request_id":"req_1"}`, "rate-limit"},
		{"long context", 429, `{"type":"error","error":{"type":"rate_limit_error","message":"Usage credits are required for long context requests."}}`, ""},
		{"any other 429", 429, `{"type":"error","error":{"type":"rate_limit_error","message":"some refusal"}}`, ""},
		{"timeout", 408, ``, "http-transient"},
		{"server error", 502, `Connection error.`, "http-transient"},
		{"client error", 400, `{"error":{"message":"exceed your account's rate limit"}}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := p.decide(FailureFacts{Source: "upstream", Status: tc.status, Text: tc.text})
			require.Equal(t, tc.rule, d.RetryRule)
			if tc.rule == "" {
				require.Equal(t, failureReturn, d.Action)
			} else {
				require.Equal(t, failureRetry, d.Action)
			}
		})
	}
}
