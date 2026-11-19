package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/usagerules"
)

// ---------------------------------------------------------------- usage sums

func TestUsagePathSum(t *testing.T) {
	doc := []byte(`{"a":3,"b":4,"n":null,"s":"x","f":1.5}`)
	cases := []struct {
		spec   string
		exists bool
		want   float64
	}{
		{"a", true, 3},
		{"a+b", true, 7},
		{"a + b", true, 7},
		{"a+missing", true, 3},
		{"missing+b", true, 4},
		{"a+n", true, 3},
		{"a+f", true, 4.5},
		{"missing+other", false, 0},
		{"n+missing", false, 0},
		{"missing", false, 0},
	}
	for _, tc := range cases {
		r := usagerules.Path(doc, tc.spec)
		if r.Exists() != tc.exists || (tc.exists && r.Float() != tc.want) {
			t.Errorf("%q: exists=%v value=%v", tc.spec, r.Exists(), r.Float())
		}
	}
	if r := usagerules.Path(doc, "a+b"); r.Int() != 7 || r.String() != "7" {
		t.Fatalf("sum as int/string: %d %q", r.Int(), r.String())
	}

	// Maps apply sums; an all-missing sum leaves the earlier value alone.
	rules := manifest.UsageRules{
		SSE: []manifest.SSEUsageMap{{Map: map[string]string{
			manifest.UsageOutputTokens: "u.out+u.think",
		}}},
		JSON: &manifest.UsageMap{Map: map[string]string{
			manifest.UsageInputTokens:  "u.in",
			manifest.UsageOutputTokens: "u.out+u.think",
		}},
		Facts: map[string]manifest.UsageFact{"total": {Type: "number", Path: "u.in+u.out+u.think"}},
	}
	u := newUsageAcc(rules)
	u.ApplySSE("", []byte(`{"u":{"out":2}}`))
	u.ApplySSE("", []byte(`{"u":{"out":5,"think":6}}`))
	u.ApplySSE("", []byte(`{"other":1}`))
	if u.Tokens().Output != 11 {
		t.Fatalf("sse sum %+v", u.Tokens())
	}
	u = newUsageAcc(rules)
	u.ApplyJSON([]byte(`{"u":{"in":10,"out":5,"think":6}}`))
	if u.Tokens() != (core.UsageTokens{Input: 10, Output: 11}) || u.Metrics["total"] != 21.0 {
		t.Fatalf("json sum %+v %v", u.Tokens(), u.Metrics)
	}
}

// Gemini thinking tokens are billed as output (CONTRACTS §14.1), for both
// the JSON and the SSE rules; the thoughts_tokens fact is kept.
func TestGeminiThoughtsCountAsOutput(t *testing.T) {
	gem := builtinPlatform(t, "gemini")
	chunk := `{"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":5,"thoughtsTokenCount":40},"modelVersion":"gemini-2.5-flash"}`
	u := newUsageAcc(gem.Usage)
	u.ApplyJSON([]byte(chunk))
	if u.Tokens() != (core.UsageTokens{Input: 12, Output: 45}) || u.Metrics["thoughts_tokens"] != 40.0 {
		t.Fatalf("json %+v %v", u.Tokens(), u.Metrics)
	}
	u = newUsageAcc(gem.Usage)
	u.ApplySSE("", []byte(chunk))
	if u.Tokens().Output != 45 || u.Metrics["thoughts_tokens"] != 40.0 {
		t.Fatalf("sse %+v %v", u.Tokens(), u.Metrics)
	}
	// Without thinking the output is the candidates count alone.
	u = newUsageAcc(gem.Usage)
	u.ApplyJSON([]byte(`{"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":5}}`))
	if u.Tokens().Output != 5 {
		t.Fatalf("no thoughts %+v", u.Tokens())
	}
	for _, p := range []string{gem.Usage.JSON.Map[manifest.UsageOutputTokens], gem.Usage.SSE[0].Map[manifest.UsageOutputTokens]} {
		if p != "usageMetadata.candidatesTokenCount+usageMetadata.thoughtsTokenCount" {
			t.Fatalf("gemini output_tokens mapping %q", p)
		}
	}
}

// ---------------------------------------------------------------- client request id

func TestClientRequestIDNormalized(t *testing.T) {
	if got := clientRequestID("  abc-1  "); got != "abc-1" {
		t.Fatalf("trim %q", got)
	}
	long := strings.Repeat("界", 200)
	if got := clientRequestID(long); len([]rune(got)) != maxClientRequestID || got != strings.Repeat("界", maxClientRequestID) {
		t.Fatalf("runes %d", len([]rune(got)))
	}
	if got := clientRequestID("a\xffb"); got != "ab" {
		t.Fatalf("invalid utf8 %q", got)
	}
}

func TestClientRequestIDRecorded(t *testing.T) {
	e := newEnv(t)
	res := e.do("/v1/messages", body(testModel, false), map[string]string{"X-Request-Id": "client-req-42"})
	if res.status != 200 {
		t.Fatalf("status %d %s", res.status, res.body)
	}
	rec := e.record()
	if rec.ClientRequestID != "client-req-42" {
		t.Fatalf("client request id %q", rec.ClientRequestID)
	}
	// The response carries the server-generated id, never the client's.
	if rid := res.header.Get("X-Request-Id"); rid == "" || rid == "client-req-42" || rid != rec.RequestID {
		t.Fatalf("response request id %q (record %q)", rid, rec.RequestID)
	}

	e.do("/v1/messages", body(testModel, false), map[string]string{"X-Request-Id": strings.Repeat("x", 300)})
	if rec := e.record(); len(rec.ClientRequestID) != 128 {
		t.Fatalf("truncated to %d", len(rec.ClientRequestID))
	}
	e.messages(body(testModel, false))
	if rec := e.record(); rec.ClientRequestID != "" {
		t.Fatalf("absent header recorded as %q", rec.ClientRequestID)
	}
}

// ---------------------------------------------------------------- self-fencing

type healthNode struct {
	fakeNode
	ok atomic.Bool
}

func (n *healthNode) Healthy() bool { return n.ok.Load() }

func TestFencedNodeReturns503(t *testing.T) {
	e := newEnv(t)
	var healthy atomic.Bool
	e.gw.health = healthy.Load

	res := e.messages(body(testModel, false))
	if res.status != http.StatusServiceUnavailable || res.json().Get("type").String() != "error" ||
		res.json().Get("error.code").String() != "unavailable" {
		t.Fatalf("anthropic format: %d %s", res.status, res.body)
	}
	if len(e.up.keys()) != 0 {
		t.Fatal("upstream called while fenced")
	}
	e.noRecord()

	// Other endpoints answer in their own error format.
	gem := e.do("/v1beta/models/gemini-2.5-pro:generateContent", map[string]any{}, map[string]string{"x-goog-api-key": testKey})
	if gem.status != 503 || gem.json().Get("error.status").String() != "UNAVAILABLE" ||
		gem.json().Get("error.details.0.reason").String() != "unavailable" {
		t.Fatalf("gemini format: %d %s", gem.status, gem.body)
	}
	oai := e.do("/v1/chat/completions", map[string]any{"model": "gpt-4o"}, map[string]string{"Authorization": "Bearer " + testKey})
	if oai.status != 503 || oai.json().Get("error.code").String() != "unavailable" {
		t.Fatalf("openai format: %d %s", oai.status, oai.body)
	}
	// Non-gateway paths are untouched.
	if res := e.do("/api/v1/whatever", nil, nil); res.status != 404 {
		t.Fatalf("core path %d", res.status)
	}

	healthy.Store(true)
	if res := e.messages(body(testModel, false)); res.status != 200 {
		t.Fatalf("healthy again: %d %s", res.status, res.body)
	}
	e.record()
}

// Deps.Node implementing Healthy() (core.NodeRegistry) is used when
// Deps.Health is nil; Deps.Health wins when set.
func TestHealthSourceFromDeps(t *testing.T) {
	n := &healthNode{}
	g := New(Deps{Node: n})
	defer g.Close()
	if g.healthy() {
		t.Fatal("node reports unhealthy")
	}
	n.ok.Store(true)
	if !g.healthy() {
		t.Fatal("node reports healthy")
	}
	g2 := New(Deps{Node: n, Health: func() bool { return false }})
	defer g2.Close()
	if g2.healthy() {
		t.Fatal("Deps.Health must win")
	}
	g3 := New(Deps{Node: fakeNode{}})
	defer g3.Close()
	if !g3.healthy() {
		t.Fatal("no health source means healthy")
	}
}

// ---------------------------------------------------------------- /settings/gateway

type memGatewaySettings struct {
	mu    sync.Mutex
	v     *GatewaySettings
	by    int64
	saves int
}

func (m *memGatewaySettings) load(context.Context) (GatewaySettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.v == nil {
		return defaultGatewaySettings(), nil
	}
	return *m.v, nil
}

func (m *memGatewaySettings) update(_ context.Context, in gatewaySettingsInput, by int64) (GatewaySettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v := defaultGatewaySettings()
	if m.v != nil {
		v = *m.v
	}
	in.applyTo(&v)
	m.v, m.by = &v, by
	m.saves++
	return v, nil
}

type recBus struct {
	mu   sync.Mutex
	msgs []string
}

func (b *recBus) Publish(_ context.Context, channel string, payload []byte) error {
	b.mu.Lock()
	b.msgs = append(b.msgs, channel+" "+string(payload))
	b.mu.Unlock()
	return nil
}

func (b *recBus) Subscribe(string, func([]byte)) func() { return func() {} }

func TestGatewaySettingsValidation(t *testing.T) {
	i := func(v int) *int { return &v }
	ok := []gatewaySettingsInput{
		{},
		{MaxAttempts: i(1), PlatformCallTimeoutMs: i(100), DefaultHookTimeoutMs: i(50)},
		{MaxAttempts: i(10), PlatformCallTimeoutMs: i(30000), DefaultHookTimeoutMs: i(2000)},
	}
	for _, in := range ok {
		if fe := in.validate(context.Background()); len(fe) != 0 {
			t.Errorf("%+v rejected: %+v", in, fe)
		}
	}
	bad := gatewaySettingsInput{MaxAttempts: i(0), PlatformCallTimeoutMs: i(99), DefaultHookTimeoutMs: i(2001)}
	fe := bad.validate(context.Background())
	if len(fe) != 3 || fe[0].Field != "max_attempts" || fe[1].Field != "platform_call_timeout_ms" ||
		fe[2].Field != "default_hook_timeout_ms" || fe[0].Code != "out_of_range" {
		t.Fatalf("fields %+v", fe)
	}
	for _, in := range []gatewaySettingsInput{{MaxAttempts: i(11)}, {PlatformCallTimeoutMs: i(30001)},
		{DefaultHookTimeoutMs: i(49)}, {PlatformHotpathTimeoutMs: i(2001)}, {PlatformHotpathTimeoutMs: i(49)}} {
		if len(in.validate(context.Background())) != 1 {
			t.Errorf("%+v accepted", in)
		}
	}
	// Stored values outside the ranges are clamped on read.
	n := GatewaySettings{MaxAttempts: 50, PlatformCallTimeoutMs: 10, DefaultHookTimeoutMs: 9000,
		PlatformHotpathTimeoutMs: 9000}.normalized()
	if !reflect.DeepEqual(n, GatewaySettings{MaxAttempts: 10, PlatformCallTimeoutMs: 100, DefaultHookTimeoutMs: 2000,
		PlatformHotpathTimeoutMs: 2000, ClaudeMaxTokens: ClaudeMaxTokensRule{Models: map[string]int{"default": 8192}}, MediaDownloadMaxMB: 64,
		MaxRequestBodyMB: 128}) {
		t.Fatalf("normalized %+v", n)
	}
	// The queue numbers are the exception: 0 turns a queue off, and a stored
	// row lacking them reads as the defaults because it decodes over them.
	if !reflect.DeepEqual((GatewaySettings{}).normalized(), (func() GatewaySettings {
		d := defaultGatewaySettings()
		d.QueueStickyMaxWaiting, d.QueueStickyWaitSeconds, d.QueueFallbackMaxWaiting, d.QueueFallbackWaitSecond = 0, 0, 0, 0
		return d.normalized()
	})()) {
		t.Fatal("zero value must yield the defaults, with the queues off")
	}
}

func TestGatewaySettingsAPI(t *testing.T) {
	bus := &recBus{}
	g := New(Deps{Bus: bus})
	t.Cleanup(g.Close)
	store := &memGatewaySettings{}
	g.gwStore = store

	engine := gin.New()
	engine.Use(httpapi.RequestContext())
	auth := allowAll{uid: 7}
	g.RegisterRoutes(httpapi.NewRouter(engine, auth, auth))
	srv := httptest.NewServer(engine)
	t.Cleanup(srv.Close)
	api := func(method string, body any) (int, gjson.Result) {
		t.Helper()
		var rd io.Reader = strings.NewReader("")
		if body != nil {
			b, _ := json.Marshal(body)
			rd = strings.NewReader(string(b))
		}
		req, _ := http.NewRequest(method, srv.URL+"/api/v1/settings/gateway", rd)
		req.Header.Set("Authorization", "Bearer x")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, gjson.ParseBytes(b)
	}

	code, res := api("GET", nil)
	if code != 200 || res.Get("data.max_attempts").Int() != 3 || res.Get("data.platform_call_timeout_ms").Int() != 2000 ||
		res.Get("data.default_hook_timeout_ms").Int() != 300 {
		t.Fatalf("defaults: %d %s", code, res.Raw)
	}

	// Warm the cache so the invalidation on PUT is observable.
	if gw := g.settings.get(context.Background()).gateway; gw.MaxAttempts != 3 {
		t.Fatalf("cache %+v", gw)
	}
	g.settings.mu.Lock()
	g.settings.snap.gateway.MaxAttempts = 9 // stale marker
	g.settings.mu.Unlock()

	code, res = api("PUT", map[string]any{"max_attempts": 0, "platform_call_timeout_ms": 50000, "default_hook_timeout_ms": 10})
	if code != 400 || res.Get("error.code").String() != "invalid_argument" || len(res.Get("error.details.fields").Array()) != 3 ||
		res.Get("error.details.fields.0.field").String() != "max_attempts" {
		t.Fatalf("invalid: %d %s", code, res.Raw)
	}
	if store.saves != 0 {
		t.Fatal("invalid input saved")
	}
	if code, _ := api("PUT", map[string]any{"max_attempts": "five"}); code != 400 {
		t.Fatalf("wrong type: %d", code)
	}

	code, res = api("PUT", map[string]any{"max_attempts": 5, "default_hook_timeout_ms": 800})
	if code != 200 || res.Get("data.max_attempts").Int() != 5 || res.Get("data.platform_call_timeout_ms").Int() != 2000 ||
		res.Get("data.default_hook_timeout_ms").Int() != 800 ||
		// The hot-path timeout is its own setting: widening the hook default
		// does not move it (CONTRACTS §25.3).
		res.Get("data.platform_hotpath_timeout_ms").Int() != 300 {
		t.Fatalf("put: %d %s", code, res.Raw)
	}
	if store.v == nil || !reflect.DeepEqual(*store.v, GatewaySettings{MaxAttempts: 5, PlatformCallTimeoutMs: 2000, DefaultHookTimeoutMs: 800,
		PlatformHotpathTimeoutMs: 300, QueueStickyMaxWaiting: 8, QueueStickyWaitSeconds: 120,
		QueueFallbackMaxWaiting: 100, QueueFallbackWaitSecond: 30, MediaDownloadMaxMB: 64, MaxRequestBodyMB: 128}) || store.by != 7 {
		t.Fatalf("stored %+v by %d", store.v, store.by)
	}
	g.settings.mu.Lock()
	snap := g.settings.snap
	g.settings.mu.Unlock()
	if snap != nil {
		t.Fatal("local settings cache not invalidated")
	}
	bus.mu.Lock()
	msgs := append([]string(nil), bus.msgs...)
	bus.mu.Unlock()
	if len(msgs) != 1 || msgs[0] != core.ChannelConfigChanged+` {"key":"settings.gateway"}` {
		t.Fatalf("broadcast %v", msgs)
	}

	code, res = api("GET", nil)
	if code != 200 || res.Get("data.max_attempts").Int() != 5 || res.Get("data.default_hook_timeout_ms").Int() != 800 {
		t.Fatalf("read back: %d %s", code, res.Raw)
	}

	// Without a database the endpoints answer 503.
	g.gwStore = dbGatewaySettings{}
	if code, res := api("GET", nil); code != 503 || res.Get("error.code").String() != "unavailable" {
		t.Fatalf("no db: %d %s", code, res.Raw)
	}
}

// The conversion settings (CONVERT-MATRIX §6.1): claude_max_tokens is off
// by default and gives nothing then, media_download_max_mb keeps its range.
func TestConversionGatewaySettings(t *testing.T) {
	n := GatewaySettings{ClaudeMaxTokens: ClaudeMaxTokensRule{Models: map[string]int{"claude-x": 64000, "neg": -5, "big": 1 << 30}},
		MediaDownloadMaxMB: 5000}.normalized()
	if !reflect.DeepEqual(n.ClaudeMaxTokens.Models, map[string]int{"claude-x": 64000, "big": 1 << 20}) || n.MediaDownloadMaxMB != 1024 {
		t.Fatalf("normalized %+v", n)
	}
	if n.ClaudeMaxTokens.maxTokens("claude-x") != 0 || n.ClaudeMaxTokens.overrideFor(claudeMaxTokensProtocol) != nil || n.mediaDownloadMaxBytes() != 1024<<20 {
		t.Fatal("a rule that is off must give nothing")
	}
	n.ClaudeMaxTokens.Enabled = true
	n = n.normalized()
	if n.ClaudeMaxTokens.maxTokens("claude-x") != 64000 || n.ClaudeMaxTokens.maxTokens("other") != 0 {
		t.Fatal("lookups without a default")
	}
	if n.ClaudeMaxTokens.overrideFor(claudeMaxTokensProtocol) == nil || n.ClaudeMaxTokens.overrideFor("openai.chat") != nil ||
		n.ClaudeMaxTokens.overrideFor("anthropic.count_tokens") != nil {
		t.Fatal("the override applies to anthropic.messages only, not count_tokens")
	}
	if d := (GatewaySettings{}).normalized().ClaudeMaxTokens; d.Enabled || !reflect.DeepEqual(d.Models, map[string]int{"default": 8192}) {
		t.Fatalf("a row without the rule: %+v", d)
	}
	rule := func(enabled bool, v map[string]int) *ClaudeMaxTokensRule {
		return &ClaudeMaxTokensRule{Enabled: enabled, Models: v}
	}
	i := func(v int) *int { return &v }
	tooMany, tooLarge := map[string]int{}, map[string]int{}
	for j := range maxClaudeMaxTokensModels + 1 {
		tooMany[fmt.Sprint("m", j)] = 1
	}
	// Names of 200 characters are allowed one by one (counted as
	// characters, not bytes); 100 of them in three-byte characters
	// compile past the override's size cap.
	long := strings.Repeat("模", maxClaudeMaxTokensName-3)
	for j := range maxClaudeMaxTokensModels {
		tooLarge[fmt.Sprintf("%s%03d", long, j)] = 1
	}
	for _, tc := range []struct {
		in   gatewaySettingsInput
		code string
	}{
		{gatewaySettingsInput{MediaDownloadMaxMB: i(0)}, "out_of_range"},
		{gatewaySettingsInput{MediaDownloadMaxMB: i(1025)}, "out_of_range"},
		{gatewaySettingsInput{ClaudeMaxTokens: rule(true, map[string]int{"default": 0})}, "out_of_range"},
		{gatewaySettingsInput{ClaudeMaxTokens: rule(true, map[string]int{" x": 1})}, "invalid_model"},
		{gatewaySettingsInput{ClaudeMaxTokens: rule(false, tooMany)}, "too_many"},
		{gatewaySettingsInput{ClaudeMaxTokens: rule(true, map[string]int{strings.Repeat("模", maxClaudeMaxTokensName): 1})}, ""},
		{gatewaySettingsInput{ClaudeMaxTokens: rule(true, map[string]int{strings.Repeat("模", maxClaudeMaxTokensName+1): 1})}, "invalid_model"},
		{gatewaySettingsInput{ClaudeMaxTokens: rule(true, tooLarge)}, "too_large"},
		{gatewaySettingsInput{ClaudeMaxTokens: rule(true, map[string]int{"x": 1}), MediaDownloadMaxMB: i(1)}, ""},
		{gatewaySettingsInput{ClaudeMaxTokens: rule(false, map[string]int{})}, ""},
	} {
		fe := tc.in.validate(context.Background())
		if tc.code == "" && len(fe) != 0 || tc.code != "" && (len(fe) != 1 || fe[0].Code != tc.code) {
			t.Errorf("%+v: %+v", tc.in, fe)
		}
		// The console matches the rule's errors by this exact field name.
		if tc.in.ClaudeMaxTokens != nil && len(fe) == 1 && fe[0].Field != "claude_max_tokens" {
			t.Errorf("field of %+v: %q", tc.in, fe[0].Field)
		}
	}
	v := defaultGatewaySettings()
	(&gatewaySettingsInput{ClaudeMaxTokens: rule(true, map[string]int{"x": 1})}).applyTo(&v)
	if got := v.normalized().ClaudeMaxTokens; !got.Enabled || !reflect.DeepEqual(got.Models, map[string]int{"x": 1}) {
		t.Fatalf("replaced rule %+v", got)
	}
	// Emptied by the administrator: stays empty, unlike a row without it.
	(&gatewaySettingsInput{ClaudeMaxTokens: rule(true, map[string]int{})}).applyTo(&v)
	if got := v.normalized().ClaudeMaxTokens; len(got.Models) != 0 || got.override != nil {
		t.Fatalf("emptied rule %+v", got)
	}
}
