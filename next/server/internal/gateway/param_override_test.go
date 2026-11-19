package gateway

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

func setParamOverride(t *testing.T, e *env, id int64, raw string) {
	t.Helper()
	p, errs := core.CompileParamOverride(context.Background(), "param_override", []byte(raw))
	require.Empty(t, errs)
	e.accounts.mu.Lock()
	e.accounts.accounts[id].ParamOverride = p
	e.accounts.mu.Unlock()
}

// An account's request parameter override (CONTRACTS §84) rewrites the body
// before the plugin builds and sets or removes headers over the
// passthrough's; the plugin's own headers (here the anthropic-beta it
// forwards) and the account key stay the plugin's (CONTRACTS §86.5).
func TestAccountParamOverrideReachesUpstream(t *testing.T) {
	const override = `{"metadata":{"src":"legacy"},"operations":[
		{"mode":"set","path":"temperature","value":0.3},
		{"mode":"set","path":"model","value":"claude-override","conditions":[{"path":"original_model","mode":"full","value":"` + testModel + `"}]},
		{"mode":"append","path":"messages.-1.content","value":"!"},
		{"mode":"set_header","path":"x-custom","value":"v1","conditions":{"request_headers.x-tier":"gold"}},
		{"mode":"set_header","path":"anthropic-beta","value":{"beta-a":null,"$append":["beta-c"]}},
		{"mode":"delete_header","path":"x-trace"}]}`
	for _, availability := range []bool{false, true} {
		e := newEnv(t)
		if availability {
			enableAvailability(e, 0)
		}
		setParamOverride(t, e, 1, override)
		e.accounts.mu.Lock()
		e.accounts.accounts[1].HeaderPassthrough = []string{"*"}
		e.accounts.mu.Unlock()

		r := e.do("/v1/messages", body(testModel, false), map[string]string{"X-Tier": "gold", "X-Trace": "t1", "Anthropic-Beta": "beta-a,beta-b"})
		require.Equal(t, 200, r.status, string(r.body))
		got := e.up.last()
		assert.Equal(t, "acc-1", got.key, "availability=%v", availability)
		up := gjson.ParseBytes(got.body)
		assert.Equal(t, "claude-override", up.Get("model").String())
		assert.Equal(t, 0.3, up.Get("temperature").Float())
		assert.Equal(t, "legacy", up.Get("metadata.src").String())
		assert.Equal(t, "hello there!", up.Get("messages.0.content").String())
		assert.Equal(t, "acc-1", got.header.Get("x-api-key"))
		assert.Equal(t, "v1", got.header.Get("X-Custom"))
		assert.Equal(t, "beta-a,beta-b", got.header.Get("Anthropic-Beta"), "the plugin's header wins over the override")
		assert.Empty(t, got.header.Get("X-Trace"), "the passthrough's header is removed")
		assert.Equal(t, "gold", got.header.Get("X-Tier"))

		rec := e.record()
		assert.Equal(t, testModel, rec.Model, "billing keeps the client model")
		assert.Equal(t, "claude-override", rec.UpstreamModel)
	}
}

// return_error answers the client without an upstream call (skip_retry) or
// moves to the next account; an operation that cannot run fails over. None
// of them cools the account down.
func TestAccountParamOverrideStops(t *testing.T) {
	t.Run("return error", func(t *testing.T) {
		e := newEnv(t)
		setParamOverride(t, e, 1, `{"operations":[{"mode":"return_error","value":{"message":"model retired","status_code":403,"type":"permission_error"}}]}`)
		r := e.messages(body(testModel, false))
		require.Equal(t, 403, r.status, string(r.body))
		assert.Equal(t, "model retired", r.json().Get("error.message").String())
		assert.Equal(t, "permission_error", r.json().Get("error.type").String())
		assert.Empty(t, e.up.keys())
		assert.Empty(t, e.accounts.cooldowns)
	})
	for name, override := range map[string]string{
		"return error with retry": `{"operations":[{"mode":"return_error","value":{"message":"not here","skip_retry":false}}]}`,
		"failed operation":        `{"operations":[{"mode":"trim_prefix","path":"max_tokens","value":"x"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			setParamOverride(t, e, 1, override)
			r := e.messages(body(testModel, false))
			require.Equal(t, 200, r.status, string(r.body))
			assert.Equal(t, []string{"acc-2"}, e.up.keys())
			assert.Empty(t, e.accounts.cooldowns)
		})
	}
}

// The plugin is handed the overridden body, with the override's model, and
// what it relays goes upstream as it is: here its own rewrite of a field the
// override set wins, and the override's headers still apply.
func TestAccountParamOverrideBeforeThePlugin(t *testing.T) {
	e := newEnv(t)
	e.plat.patches = []*pluginv1.BodyPatch{{Op: pluginv1.BodyPatch_OP_SET, Path: "temperature", ValueJson: "0.9"}}
	setParamOverride(t, e, 1, `{"operations":[{"mode":"set","path":"temperature","value":0.3},{"mode":"set","path":"top_k","value":4},
		{"mode":"set_header","path":"x-custom","value":"v1"},{"mode":"set","path":"model","value":"claude-override"}]}`)
	require.Equal(t, 200, e.messages(body(testModel, false)).status)
	got := e.up.last()
	assert.Equal(t, 0.9, gjson.GetBytes(got.body, "temperature").Float(), "the plugin's rewrite wins over the override")
	assert.Equal(t, int64(4), gjson.GetBytes(got.body, "top_k").Int())
	assert.Equal(t, "claude-override", gjson.GetBytes(got.body, "model").String())
	assert.Equal(t, "v1", got.header.Get("X-Custom"))
	b := e.plat.builds[0]
	assert.Equal(t, "claude-override", b.GetMeta().GetModel())
	assert.Equal(t, 0.3, gjson.GetBytes(b.GetBody(), "temperature").Float(), "the plugin is handed the overridden body")
	assert.Equal(t, "claude-override", gjson.GetBytes(b.GetBody(), "model").String())
}

// The built-in claude_max_tokens override (CONTRACTS §84.1): off, nothing is
// added; on, a body without max_tokens gets its client model's value, else
// the default's; the client's own value is kept, and the account's override
// runs after it.
func TestClaudeMaxTokensBuiltinOverride(t *testing.T) {
	e := newEnv(t)
	noLimit := body(testModel, false)
	delete(noLimit, "max_tokens")
	maxTokens := func(b map[string]any) gjson.Result {
		t.Helper()
		r := e.messages(b)
		require.Equal(t, 200, r.status, string(r.body))
		e.record()
		return gjson.GetBytes(e.up.last().body, "max_tokens")
	}

	assert.False(t, maxTokens(noLimit).Exists(), "off by default")

	gw := noQueueSettings()
	gw.ClaudeMaxTokens = ClaudeMaxTokensRule{Enabled: true, Models: map[string]int{"default": 100, testModel: 200}}
	e.setSettings(gw, defaultStickySettings())
	assert.Equal(t, int64(200), maxTokens(noLimit).Int(), "the client model's value")
	assert.Equal(t, int64(64), maxTokens(body(testModel, false)).Int(), "the client's own value is kept")

	gw.ClaudeMaxTokens.Models = map[string]int{"default": 100}
	e.setSettings(gw, defaultStickySettings())
	assert.Equal(t, int64(100), maxTokens(noLimit).Int(), "the default's value")

	setParamOverride(t, e, 1, `{"operations":[{"mode":"set","path":"max_tokens","value":5}]}`)
	assert.Equal(t, int64(5), maxTokens(noLimit).Int(), "the account's override runs after")
}

func setHeaderOverride(t *testing.T, e *env, id int64, raw string) {
	t.Helper()
	m, errs := core.CompileHeaderOverride(context.Background(), "header_override", []byte(raw))
	require.Empty(t, errs)
	e.accounts.mu.Lock()
	e.accounts.accounts[id].HeaderOverride = m
	e.accounts.mu.Unlock()
}

// An account's header override (CONTRACTS §108): its passthrough keys join
// the account's passthrough, its values and {unset} apply over the
// passthrough, and it is the initial header state of the parameter
// override; the plugin's headers and the account key stay the plugin's.
func TestAccountHeaderOverrideReachesUpstream(t *testing.T) {
	e := newEnv(t)
	setHeaderOverride(t, e, 1, `{"re:^x-pass-":"","X-Client":"my-client/1.0","x-trace-id":"{client_header:x-request-id}",
		"x-beta":"{client_header:x-beta:extra}","x-tier":"{unset}","anthropic-beta":"from-override"}`)
	setParamOverride(t, e, 1, `{"operations":[{"mode":"set_header","path":"x-client","value":"op","keep_origin":true},
		{"mode":"set_header","path":"x-seen","value":"yes","conditions":{"header_override.x-client":"my-client/1.0"}}]}`)
	e.accounts.mu.Lock()
	e.accounts.accounts[1].HeaderPassthrough = []string{"x-tier"}
	e.accounts.mu.Unlock()

	r := e.do("/v1/messages", body(testModel, false), map[string]string{"X-Pass-One": "p1", "X-Request-Id": "r1", "X-Beta": "b",
		"X-Tier": "gold", "X-Other": "o", "Anthropic-Beta": "beta-a"})
	require.Equal(t, 200, r.status, string(r.body))
	got := e.up.last().header
	assert.Equal(t, "acc-1", got.Get("x-api-key"))
	assert.Equal(t, "p1", got.Get("X-Pass-One"))
	assert.Equal(t, "my-client/1.0", got.Get("X-Client"))
	assert.Equal(t, "r1", got.Get("X-Trace-Id"))
	assert.Equal(t, "b,extra", got.Get("X-Beta"))
	assert.Equal(t, "yes", got.Get("X-Seen"))
	assert.Empty(t, got.Get("X-Tier"), "{unset} removes the passthrough's header")
	assert.Empty(t, got.Get("X-Other"))
	assert.Equal(t, "beta-a", got.Get("Anthropic-Beta"), "the plugin's header wins over the override")

	// Without a parameter override the header override applies alone.
	setParamOverride(t, e, 1, `{}`)
	r = e.do("/v1/messages", body(testModel, false), map[string]string{"X-Request-Id": "r2"})
	require.Equal(t, 200, r.status, string(r.body))
	got = e.up.last().header
	assert.Equal(t, "r2", got.Get("X-Trace-Id"))
	assert.Equal(t, "my-client/1.0", got.Get("X-Client"))
}

// A stored header override that no longer compiles fails the account's
// attempts over to the next account without cooling it down.
func TestAccountHeaderOverrideBroken(t *testing.T) {
	e := newEnv(t)
	e.accounts.mu.Lock()
	e.accounts.accounts[1].HeaderOverride = core.LoadHeaderOverride([]byte(`{"Authorization":"x"}`))
	e.accounts.mu.Unlock()
	r := e.messages(body(testModel, false))
	require.Equal(t, 200, r.status, string(r.body))
	assert.Equal(t, []string{"acc-2"}, e.up.keys())
	assert.Empty(t, e.accounts.cooldowns)
}

// 模拟上游实际调用: one request through the gateway for an account with a
// header override and a parameter override; the fake upstream receives
// exactly these headers and this body.
func TestAccountOverridesExactUpstreamRequest(t *testing.T) {
	e := newEnv(t)
	setHeaderOverride(t, e, 1, `{"re:^x-pass-":"","X-Static":"static-1","x-beta":"{client_header:x-beta:extra}",
		"x-only":"{client_header:x-missing:only}","x-tier":"{unset}","x-keep":"from-ho","":"ignored"}`)
	setParamOverride(t, e, 1, `{"operations":[
		{"mode":"set","path":"temperature","value":0.2},
		{"mode":"set_header","path":"x-keep","value":"op","keep_origin":true},
		{"mode":"set_header","path":"x-new","value":"op","keep_origin":true},
		{"mode":"set_header","path":"x-static","value":"static-2","conditions":{"header_override.x-static":"static-1"}},
		{"mode":"sync_fields","from":"json:metadata.user_id","to":"header:x-user"},
		{"mode":"sync_fields","from":"header:x-session","to":"json:metadata.session"}]}`)
	e.accounts.mu.Lock()
	e.accounts.accounts[1].HeaderPassthrough = []string{"x-tier"}
	e.accounts.mu.Unlock()

	r := e.do("/v1/messages", withSession(body(testModel, false), "u-1"), map[string]string{"X-Pass-One": "p1", "X-Beta": "b",
		"X-Tier": "gold", "X-Session": "s-1", "X-Other": "o"})
	require.Equal(t, 200, r.status, string(r.body))
	got := e.up.last()
	assert.JSONEq(t, `{"model":"`+testModel+`","max_tokens":64,"stream":false,"temperature":0.2,
		"messages":[{"role":"user","content":"hello there"}],"metadata":{"user_id":"u-1","session":"s-1"}}`, string(got.body))
	assert.Equal(t, http.Header{
		// The plugin's and the transport's.
		"X-Api-Key": {"acc-1"}, "Content-Type": {"application/json"}, "Anthropic-Version": {"2023-06-01"},
		"Content-Length": {strconv.Itoa(len(got.body))}, "Accept-Encoding": {"gzip"}, "User-Agent": {"Go-http-client/1.1"},
		// The header override's passthrough key (X-Other is not passed,
		// X-Tier is passed by the account's rules and removed by {unset}).
		"X-Pass-One": {"p1"},
		// Its values, then the parameter override over them: keep_origin
		// keeps x-keep, a condition reads x-static and changes it, and
		// sync_fields writes x-user from the body.
		"X-Beta": {"b,extra"}, "X-Only": {"only"}, "X-Keep": {"from-ho"}, "X-New": {"op"}, "X-Static": {"static-2"}, "X-User": {"u-1"},
	}, got.header)
}

// sync_fields writing body content that cannot be a header value (here a
// newline) fails the attempt over neutrally instead of sending a request
// the transport rejects, which would cool the account down.
func TestAccountParamOverrideHeaderControlCharacters(t *testing.T) {
	e := newEnv(t)
	setParamOverride(t, e, 1, `{"operations":[{"mode":"sync_fields","from":"json:metadata.user_id","to":"header:x-user"}]}`)
	r := e.messages(withSession(body(testModel, false), "u-1\nx-injected: 1"))
	require.Equal(t, 200, r.status, string(r.body))
	assert.Equal(t, []string{"acc-2"}, e.up.keys())
	assert.Empty(t, e.up.last().header.Get("X-User"))
	assert.Empty(t, e.accounts.cooldowns)
}

// The WebSocket handshake carries the account's header override and the
// first message's parameter override (no passthrough, §81); the plugin's
// headers come after them.
func TestWebSocketHandshakeHeaderOverride(t *testing.T) {
	e := newWSEnv(t)
	setHeaderOverride(t, e.env, 11, `{"X-Static":"static-1","x-beta":"{client_header:x-beta:extra}","x-keep":"from-ho",
		"x-gone":"{unset}","openai-beta":"from-ho","*":""}`)
	setParamOverride(t, e.env, 11, `{"operations":[{"mode":"set_header","path":"x-keep","value":"op","keep_origin":true},
		{"mode":"sync_fields","from":"json:input","to":"header:x-input"}]}`)
	c, _, err := e.dial(http.Header{"Authorization": {"Bearer " + testKey}, "X-Beta": {"b"}, "X-Gone": {"g"}, "X-Other": {"o"}})
	require.NoError(t, err)
	c.send(create(wsModel, "hello"))
	c.until("response.completed")
	e.record()
	e.wsUp.mu.Lock()
	h := e.wsUp.headers[0]
	e.wsUp.mu.Unlock()
	assert.Equal(t, "static-1", h.Get("X-Static"))
	assert.Equal(t, "b,extra", h.Get("X-Beta"))
	assert.Equal(t, "from-ho", h.Get("X-Keep"))
	assert.Equal(t, "hello", h.Get("X-Input"))
	assert.Equal(t, "responses_websockets=2026-02-06", h.Get("Openai-Beta"), "the plugin's header wins")
	assert.Equal(t, "Bearer oa-11", h.Get("Authorization"))
	assert.Empty(t, h.Get("X-Gone"))
	assert.Empty(t, h.Get("X-Other"), "no passthrough on a WebSocket")
}

// On a WebSocket the first turn's rewrites run before its Execute, as on
// HTTP: when they fail on an account the turn moves to the next one without
// blaming it, and the handshake made there carries none of the failed
// account's headers. A stored header override that no longer compiles fails
// the account over too.
func TestWebSocketHeaderOverrideIndependentOfTheBody(t *testing.T) {
	t.Run("rewrite fails", func(t *testing.T) {
		e := newWSEnv(t)
		setHeaderOverride(t, e.env, 11, `{"X-Static":"static-1"}`)
		setParamOverride(t, e.env, 11, `{"operations":[{"mode":"trim_prefix","path":"store","value":"x","conditions":{"input":"first"}},
			{"mode":"set_header","path":"x-op","value":"op"}]}`)
		c := e.mustDial()
		c.send(create(wsModel, "first"))
		c.until("response.completed")
		r := e.record()
		require.NotNil(t, r.AccountID)
		assert.NotEqual(t, int64(11), *r.AccountID)
		assert.Empty(t, e.accounts.cooldowns)
		e.wsUp.mu.Lock()
		headers := e.wsUp.headers
		e.wsUp.mu.Unlock()
		require.Len(t, headers, 1)
		assert.Empty(t, headers[0].Get("X-Static"))
		assert.Empty(t, headers[0].Get("X-Op"))
	})
	t.Run("broken", func(t *testing.T) {
		e := newWSEnv(t)
		e.accounts.mu.Lock()
		e.accounts.accounts[11].HeaderOverride = core.LoadHeaderOverride([]byte(`{"Authorization":"x"}`))
		e.accounts.mu.Unlock()
		c := e.mustDial()
		c.send(create(wsModel, "hello"))
		c.until("response.completed")
		r := e.record()
		require.NotNil(t, r.AccountID)
		assert.Equal(t, int64(12), *r.AccountID)
		assert.Empty(t, e.accounts.cooldowns)
	})
}
