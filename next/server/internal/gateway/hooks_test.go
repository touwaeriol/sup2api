package gateway

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

func guardHook(timeoutMs int, failure string) manifest.Hook {
	return manifest.Hook{ID: "prompt-rules", Point: pointGatewayRequest, Order: 100,
		Match:     manifest.HookMatch{Protocols: []string{"anthropic.messages"}, Models: []string{"*"}, Groups: []string{"*"}},
		Needs:     []string{"model", "prompt_text"},
		TimeoutMs: timeoutMs, Failure: failure, MaxPromptBytes: 32768}
}

func denyIf(word string) func(context.Context, *pluginv1.GatewayRequestHookRequest) (*pluginv1.GatewayRequestHookResponse, error) {
	return func(_ context.Context, in *pluginv1.GatewayRequestHookRequest) (*pluginv1.GatewayRequestHookResponse, error) {
		if strings.Contains(in.GetFields()["prompt_text"], word) {
			return &pluginv1.GatewayRequestHookResponse{Decision: pluginv1.GatewayRequestHookResponse_DECISION_DENY,
				DenyCode: "guard_blocked", DenyMessage: "blocked by rule forbidden", Note: "rule forbidden"}, nil
		}
		return &pluginv1.GatewayRequestHookResponse{Decision: pluginv1.GatewayRequestHookResponse_DECISION_ALLOW}, nil
	}
}

func promptBody(prompt string, stream bool) map[string]any {
	b := body(testModel, stream)
	b["system"] = "You are helpful."
	b["messages"] = []any{
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": prompt},
			map[string]any{"type": "image", "source": map[string]any{"data": "AAAA"}},
		}},
	}
	return b
}

func TestHookDenyAndFields(t *testing.T) {
	e := newEnv(t)
	h := &fakeHook{fn: denyIf("forbidden")}
	e.addHook(guardHook(300, "open"), []string{"model", "prompt_text"}, h)

	for _, stream := range []bool{false, true} {
		r := e.messages(promptBody("please say forbidden", stream))
		if r.status != 403 || r.json().Get("type").String() != "error" || r.json().Get("error.type").String() != "permission_error" ||
			r.json().Get("error.code").String() != "guard_blocked" || r.json().Get("error.message").String() != "blocked by rule forbidden" {
			t.Fatalf("deny: %d %s", r.status, r.body)
		}
		rec := e.record()
		if rec.ErrorType != errTypeBlockedByHook || rec.Billable || rec.StatusCode != 403 || len(rec.HookDecisions) != 1 ||
			rec.HookDecisions[0].Decision != decisionDeny || rec.HookDecisions[0].PluginKey != "guard" ||
			rec.HookDecisions[0].HookID != "prompt-rules" || rec.HookDecisions[0].Note != "rule forbidden" {
			t.Fatalf("deny record %+v", rec)
		}
	}
	if len(e.up.keys()) != 0 {
		t.Fatal("upstream called for a denied request")
	}
	// Fields: prompt_text is plain text (not JSON), model is raw JSON, only granted fields.
	in := h.calls[0]
	if in.GetFields()["prompt_text"] != "You are helpful.\nplease say forbidden" {
		t.Fatalf("prompt_text %q", in.GetFields()["prompt_text"])
	}
	if in.GetFields()["model"] != `"`+testModel+`"` || len(in.GetFields()) != 2 {
		t.Fatalf("fields %v", in.GetFields())
	}
	if m := in.GetMeta(); m.GetUserId() != testUser || m.GetGroupId() != testGroup || m.GetRequestId() == "" || m.GetModel() != testModel {
		t.Fatalf("meta %+v", m)
	}
	// Clean prompt passes.
	if r := e.messages(promptBody("a harmless prompt", false)); r.status != 200 {
		t.Fatalf("allow: %d %s", r.status, r.body)
	}
	if rec := e.record(); len(rec.HookDecisions) != 1 || rec.HookDecisions[0].Decision != decisionAllow {
		t.Fatalf("allow record %+v", rec.HookDecisions)
	}
}

func TestHookMatchFilter(t *testing.T) {
	e := newEnv(t)
	other := &fakeHook{fn: denyIf("")}
	h := guardHook(300, "open")
	h.Match.Protocols = []string{"openai.chat"}
	e.addHook(h, []string{"prompt_text"}, other)
	byModel := guardHook(300, "open")
	byModel.ID = "haiku-only"
	byModel.Match.Models = []string{"claude-haiku-*"}
	e.addHook(byModel, []string{"prompt_text"}, other)
	byGroup := guardHook(300, "open")
	byGroup.ID = "vip"
	byGroup.Match.Groups = []string{"vip"}
	e.addHook(byGroup, []string{"prompt_text"}, other)
	if r := e.messages(body(testModel, false)); r.status != 200 {
		t.Fatalf("status %d", r.status)
	}
	if other.count() != 0 {
		t.Fatal("non-matching hook called")
	}
	e.record()
}

func TestHookTimeoutOpenAndClosed(t *testing.T) {
	slow := func(ctx context.Context, _ *pluginv1.GatewayRequestHookRequest) (*pluginv1.GatewayRequestHookResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	t.Run("open", func(t *testing.T) {
		e := newEnv(t)
		e.addHook(guardHook(50, "open"), []string{"prompt_text"}, &fakeHook{fn: slow})
		start := time.Now()
		if r := e.messages(body(testModel, false)); r.status != 200 {
			t.Fatalf("fail open: %d %s", r.status, r.body)
		}
		if time.Since(start) > 2*time.Second {
			t.Fatal("hook timeout not applied")
		}
		rec := e.record()
		if d := rec.HookDecisions; len(d) != 1 || d[0].Decision != decisionErrorOpen || !strings.Contains(d[0].Note, "timeout") {
			t.Fatalf("decisions %+v", d)
		}
	})
	t.Run("closed", func(t *testing.T) {
		e := newEnv(t)
		e.addHook(guardHook(50, "closed"), []string{"prompt_text"}, &fakeHook{fn: slow})
		r := e.messages(body(testModel, false))
		if r.status != 503 || r.json().Get("type").String() != "error" {
			t.Fatalf("fail closed: %d %s", r.status, r.body)
		}
		if len(e.up.keys()) != 0 {
			t.Fatal("upstream called")
		}
		rec := e.record()
		if rec.ErrorType != errTypeBlockedByHook || rec.HookDecisions[0].Decision != decisionErrorClosed {
			t.Fatalf("record %+v", rec)
		}
	})
	// The deadline handed to the hook: the manifest timeoutMs up to the 30 s
	// ceiling (CONTRACTS §20.1). The hooks return at once, nothing sleeps.
	deadlineFor := func(t *testing.T, timeoutMs int) time.Duration {
		e := newEnv(t)
		var deadline time.Duration
		e.addHook(guardHook(timeoutMs, "open"), nil, &fakeHook{fn: func(ctx context.Context, _ *pluginv1.GatewayRequestHookRequest) (*pluginv1.GatewayRequestHookResponse, error) {
			d, _ := ctx.Deadline()
			deadline = time.Until(d)
			return &pluginv1.GatewayRequestHookResponse{}, nil
		}})
		e.messages(body(testModel, false))
		e.record()
		return deadline
	}
	t.Run("timeout capped at 30s", func(t *testing.T) {
		if maxHookTimeout != 30*time.Second {
			t.Fatalf("maxHookTimeout %s", maxHookTimeout)
		}
		if d := deadlineFor(t, 60000); d > maxHookTimeout || d < maxHookTimeout-time.Second {
			t.Fatalf("hook deadline %s", d)
		}
	})
	t.Run("manifest timeout above the old 2s ceiling", func(t *testing.T) {
		if d := deadlineFor(t, 25000); d > 25*time.Second || d < 24*time.Second {
			t.Fatalf("hook deadline %s", d)
		}
	})
	t.Run("admin default applies without timeoutMs", func(t *testing.T) {
		if d := deadlineFor(t, 0); d > 300*time.Millisecond || d <= 0 {
			t.Fatalf("hook deadline %s", d)
		}
	})
}

// TestHookGjsonQueryNeeds: needs may be gjson queries (CONTRACTS §20.1);
// the hook gets the raw JSON of the query result, keyed by the query text.
func TestHookGjsonQueryNeeds(t *testing.T) {
	const (
		lastUserMsgs  = `messages|@reverse|#(role=="user")`
		lastUserInput = `input|@reverse|#(role=="user")`
		stringInput   = `[input]|#(%"*")`
		lastUserParts = `contents|@reverse|#(role=="user")`
	)
	needs := []string{"model", lastUserMsgs, lastUserInput, stringInput, lastUserParts}

	e := newEnv(t)
	h := &fakeHook{}
	hk := guardHook(300, "open")
	hk.ID, hk.Needs = "moderation", needs
	e.addHook(hk, needs, h)
	b := body(testModel, false)
	b["messages"] = []any{
		map[string]any{"role": "user", "content": "first question"},
		map[string]any{"role": "assistant", "content": "an answer"},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": "tool output"},
			map[string]any{"type": "text", "text": "latest question"},
		}},
		map[string]any{"role": "assistant", "content": "prefill"},
	}
	if r := e.messages(b); r.status != 200 {
		t.Fatalf("status %d %s", r.status, r.body)
	}
	e.record()
	if h.count() != 1 {
		t.Fatalf("hook calls %d", h.count())
	}
	f := h.calls[0].GetFields()
	// Only fields present in the body are sent.
	if len(f) != 2 || f["model"] != `"`+testModel+`"` {
		t.Fatalf("fields %v", f)
	}
	raw, ok := f[lastUserMsgs]
	if !ok || !gjson.Valid(raw) {
		t.Fatalf("last user message %q", raw)
	}
	msg := gjson.Parse(raw)
	if msg.Get("role").String() != "user" || msg.Get(`content.#(type=="text").text`).String() != "latest question" ||
		strings.Contains(raw, "first question") || strings.Contains(raw, "prefill") {
		t.Fatalf("last user message %s", raw)
	}

	// The other query shapes, straight through hookFields.
	hb := e.gen.hooks[0]
	for _, tc := range []struct {
		body string
		want map[string]string
	}{
		{`{"model":"m","input":"just a string"}`, map[string]string{"model": `"m"`, stringInput: `"just a string"`}},
		{`{"model":"m","input":[{"role":"user","content":"a"},{"role":"assistant","content":"b"},{"role":"user","content":[{"type":"input_text","text":"c"}]}]}`,
			map[string]string{"model": `"m"`, lastUserInput: `{"role":"user","content":[{"type":"input_text","text":"c"}]}`}},
		{`{"contents":[{"role":"user","parts":[{"text":"x"}]},{"role":"model","parts":[{"text":"y"}]},{"role":"user","parts":[{"text":"z"}]}]}`,
			map[string]string{lastUserParts: `{"role":"user","parts":[{"text":"z"}]}`}},
		{`{"model":"m"}`, map[string]string{"model": `"m"`}},
	} {
		c := &call{body: []byte(tc.body)}
		got := c.hookFields(hb)
		if len(got) != len(tc.want) {
			t.Fatalf("%s: fields %v", tc.body, got)
		}
		for k, v := range tc.want {
			if got[k] != v {
				t.Fatalf("%s: field %s = %q, want %q", tc.body, k, got[k], v)
			}
		}
	}

	// Query fields are read-only: no patch may write through them.
	for _, p := range []string{lastUserMsgs, lastUserMsgs + ".content", stringInput} {
		patch := []*pluginv1.BodyPatch{{Op: pluginv1.BodyPatch_OP_SET, Path: p, ValueJson: `"x"`}}
		if _, err := applyHookPatches([]byte(`{"messages":[{"role":"user","content":"a"}],"input":"b"}`), patch, needs); err == nil {
			t.Fatalf("patch through query field %q accepted", p)
		}
	}
	if isQueryField("metadata.user_id") || isQueryField("model") || !isQueryField(lastUserParts) {
		t.Fatal("isQueryField")
	}
}

func TestHookBreaker(t *testing.T) {
	e := newEnv(t)
	h := &fakeHook{fn: func(context.Context, *pluginv1.GatewayRequestHookRequest) (*pluginv1.GatewayRequestHookResponse, error) {
		return nil, errors.New("plugin unavailable")
	}}
	e.addHook(guardHook(300, "open"), []string{"prompt_text"}, h)
	for i := 0; i < breakerThreshold; i++ {
		if r := e.messages(body(testModel, false)); r.status != 200 {
			t.Fatalf("fail open %d: %d", i, r.status)
		}
		e.record()
	}
	if !e.mr.Exists("hook:breaker:guard:prompt-rules") {
		t.Fatal("breaker not stored in redis")
	}
	if r := e.messages(body(testModel, false)); r.status != 200 {
		t.Fatalf("breaker open: %d", r.status)
	}
	if h.count() != breakerThreshold {
		t.Fatalf("hook called %d times while breaker open", h.count())
	}
	if d := e.record().HookDecisions; d[0].Decision != decisionSkippedBreaker {
		t.Fatalf("decision %+v", d)
	}

	// Other nodes see the shared breaker state.
	other := newHookRuntime(e.rdb)
	if !other.breakerOpen(context.Background(), hookKey{"guard", "prompt-rules"}) {
		t.Fatal("breaker not shared through redis")
	}

	// Stats: 10 calls, 10 errors, breaker open.
	stats, err := e.gw.HookStats(context.Background(), "guard")
	if err != nil || len(stats) != 1 {
		t.Fatalf("stats %v %v", stats, err)
	}
	s := stats[0]
	if s.HookID != "prompt-rules" || s.Point != pointGatewayRequest || s.Calls != 10 || s.Errors != 10 || !s.BreakerOpen || s.P99Ms <= 0 {
		t.Fatalf("stat %+v", s)
	}

	// After the open window the hook is called again (half-open).
	e.gw.hooks.mu.Lock()
	e.gw.hooks.breakers[hookKey{"guard", "prompt-rules"}].openUntil = time.Now().Add(-time.Second)
	e.gw.hooks.breakers[hookKey{"guard", "prompt-rules"}].checkedAt = time.Time{}
	e.gw.hooks.mu.Unlock()
	e.mr.FastForward(breakerOpenFor + time.Second)
	e.messages(body(testModel, false))
	e.record()
	if h.count() != breakerThreshold+1 {
		t.Fatalf("hook not retried after breaker window: %d", h.count())
	}
}

// A hook call the plugin's gRPC server refuses as too large is a failure like
// any other: nothing about the size of a request is left out of the breaker
// (the request body limit of the gateway settings is the only size limit).
func TestHookReceiveLimitFailureCountsTowardBreaker(t *testing.T) {
	e := newEnv(t)
	h := &fakeHook{fn: func(context.Context, *pluginv1.GatewayRequestHookRequest) (*pluginv1.GatewayRequestHookResponse, error) {
		return nil, status.Error(codes.ResourceExhausted, "grpc: received message larger than max (5000000 vs. 4194304)")
	}}
	e.addHook(guardHook(300, "open"), []string{"messages"}, h)
	for i := 0; i < breakerThreshold; i++ {
		b := body(testModel, false)
		b["messages"] = []any{map[string]any{"role": "user", "content": strings.Repeat("x", 5<<20)}}
		require.Equal(t, 200, e.messages(b).status, "fail open")
		assert.Equal(t, decisionErrorOpen, e.record().HookDecisions[0].Decision)
	}
	require.True(t, e.mr.Exists("hook:breaker:guard:prompt-rules"))
	assert.Equal(t, breakerThreshold, h.count())
}

func TestHookStatsCountsDeniesAndTimeouts(t *testing.T) {
	e := newEnv(t)
	calls := 0
	e.addHook(guardHook(30, "open"), []string{"prompt_text"}, &fakeHook{fn: func(ctx context.Context, in *pluginv1.GatewayRequestHookRequest) (*pluginv1.GatewayRequestHookResponse, error) {
		calls++
		switch calls {
		case 1:
			return &pluginv1.GatewayRequestHookResponse{Decision: pluginv1.GatewayRequestHookResponse_DECISION_DENY}, nil
		case 2:
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return &pluginv1.GatewayRequestHookResponse{}, nil
	}})
	for i := 0; i < 3; i++ {
		e.messages(body(testModel, false))
		e.record()
	}
	stats, err := e.gw.HookStats(context.Background(), "guard")
	if err != nil || len(stats) != 1 {
		t.Fatalf("stats %v %v", stats, err)
	}
	if s := stats[0]; s.Calls != 3 || s.Denies != 1 || s.Timeouts != 1 || s.Errors != 0 || s.BreakerOpen {
		t.Fatalf("stat %+v", s)
	}
	// Default deny status/code/message.
	if other, _ := e.gw.HookStats(context.Background(), "nobody"); len(other) != 0 {
		t.Fatalf("stats for unknown plugin: %v", other)
	}
}

func TestHookPatches(t *testing.T) {
	t.Run("granted path applied", func(t *testing.T) {
		e := newEnv(t)
		e.addHook(guardHook(300, "open"), []string{"metadata", "prompt_text"}, &fakeHook{fn: func(context.Context, *pluginv1.GatewayRequestHookRequest) (*pluginv1.GatewayRequestHookResponse, error) {
			return &pluginv1.GatewayRequestHookResponse{Patches: []*pluginv1.BodyPatch{
				{Op: pluginv1.BodyPatch_OP_SET, Path: "metadata.user_id", ValueJson: `"redacted"`}}}, nil
		}})
		e.messages(withSession(body(testModel, false), "s1"))
		e.record()
		if v := gjson.GetBytes(e.up.last().body, "metadata.user_id").String(); v != "redacted" {
			t.Fatalf("patch not applied: %s", e.up.last().body)
		}
	})
	t.Run("patch outside needs rejected", func(t *testing.T) {
		e := newEnv(t)
		e.addHook(guardHook(300, "open"), []string{"model", "prompt_text"}, &fakeHook{fn: func(context.Context, *pluginv1.GatewayRequestHookRequest) (*pluginv1.GatewayRequestHookResponse, error) {
			return &pluginv1.GatewayRequestHookResponse{Patches: []*pluginv1.BodyPatch{
				{Op: pluginv1.BodyPatch_OP_SET, Path: "max_tokens", ValueJson: `99999`},
				{Op: pluginv1.BodyPatch_OP_SET, Path: "prompt_text", ValueJson: `"x"`}}}, nil
		}})
		if r := e.messages(body(testModel, false)); r.status != 200 {
			t.Fatalf("status %d", r.status)
		}
		rec := e.record()
		if v := gjson.GetBytes(e.up.last().body, "max_tokens").Int(); v != 64 {
			t.Fatalf("unauthorized patch applied: max_tokens=%d", v)
		}
		if d := rec.HookDecisions[0]; d.Decision != decisionErrorOpen || !strings.Contains(d.Note, "patch rejected") {
			t.Fatalf("decision %+v", d)
		}
	})
	t.Run("model patch re-checked", func(t *testing.T) {
		e := newEnv(t, func(e *env) { e.auth.keys[testKey].Group.ModelAllowlist = []string{"claude-sonnet-*"} })
		e.addHook(guardHook(300, "open"), []string{"model"}, &fakeHook{fn: func(context.Context, *pluginv1.GatewayRequestHookRequest) (*pluginv1.GatewayRequestHookResponse, error) {
			return &pluginv1.GatewayRequestHookResponse{Patches: []*pluginv1.BodyPatch{
				{Op: pluginv1.BodyPatch_OP_SET, Path: "model", ValueJson: `"claude-opus-5"`}}}, nil
		}})
		if r := e.messages(body(testModel, false)); r.status != 404 {
			t.Fatalf("patched model escaped allowlist: %d", r.status)
		}
		e.record()
	})
}

func TestHookOrder(t *testing.T) {
	e := newEnv(t)
	var order []string
	mk := func(name string) *fakeHook {
		return &fakeHook{fn: func(context.Context, *pluginv1.GatewayRequestHookRequest) (*pluginv1.GatewayRequestHookResponse, error) {
			order = append(order, name)
			return &pluginv1.GatewayRequestHookResponse{}, nil
		}}
	}
	late := guardHook(300, "open")
	late.ID, late.Order = "late", 200
	early := guardHook(300, "open")
	early.ID, early.Order = "early", 10
	e.addHook(late, nil, mk("late"))
	e.addHook(early, nil, mk("early"))
	e.messages(body(testModel, false))
	e.record()
	if strings.Join(order, ",") != "early,late" {
		t.Fatalf("order %v", order)
	}
}
