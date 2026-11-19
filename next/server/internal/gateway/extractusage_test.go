package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// Plugin-reported usage: an endpoint declaring usage.source "plugin" is
// served in the wait form of Execute (CONTRACTS §25.3, design §4.3). The
// plugin relays through ExecutionClient.Relay, which sends the request,
// relays the answer and hands the plugin the bounded observation; the plugin
// reads the usage from it inside Execute (here fakeExecute calls the fake's
// ExtractUsage) and returns it. The gateway commits those facts through
// core.ExecutionRecords (the committed record lands on e.settler.ch like a
// submitted one) before a buffered answer is published. A plugin that cannot
// read the usage answers host_usage: the declarative rules bill it, marked
// usage_extract=fallback. The other rows of the wait form are in
// zz_b_waited_test.go.

// ---------------------------------------------------------------- environment

// pluginUsageRules are the usage rules under test: one fact is declared so a
// price expression has something to read. There is deliberately no sse/json
// map - that is the whole reason an endpoint picks the plugin source, and it
// makes the fallback visible when the plugin fails. Who reads the usage is
// declared on the endpoint (pluginUsageEndpoint), not here.
func pluginUsageRules() manifest.UsageRules {
	return manifest.UsageRules{
		Semantics: "exclusive",
		Facts: map[string]manifest.UsageFact{
			"images": {Type: "number"},
			"tier":   {Type: "enum", Enum: []string{"fast", "slow"}},
		},
	}
}

// pluginUsageEndpoint is the endpoint under test: the plugin reports the
// usage and the host collects only "message_delta" from a stream.
func pluginUsageEndpoint() manifest.Endpoint {
	return manifest.Endpoint{ID: "chat", Method: "POST", Path: "/pu/v1/chat", Protocol: "pu.chat", Kind: "proxy",
		Auth:     manifest.EndpointAuth{Headers: []string{"x-api-key"}},
		Request:  manifest.EndpointRequest{ModelPath: "model", StreamPath: "stream"},
		Response: manifest.EndpointResp{Stream: "sse", NonStream: "json"}, ErrorFormat: "plain", Billing: "usage", BillingTypes: []string{"per_request", "per_token", "expression"},
		UsageSource:       manifest.UsageSourcePlugin,
		UsageStreamEvents: []string{"message_delta"},
	}
}

// pluginUsageRoute is the route the endpoint above produces, for the unit
// tests of the capture that do not go through the gateway.
func pluginUsageRoute() *typeRoute {
	ep := pluginUsageEndpoint()
	return &typeRoute{usage: pluginUsageRules(), pluginUsage: ep.PluginUsage(),
		usageEvents: ep.UsageStreamEvents}
}

// usageEnv adds a plugin "pu" declaring platform "pu" with one endpoint at
// POST /pu/v1/chat whose usage comes from the plugin, plus an account type of
// the same plugin serving it (account 9).
func usageEnv(t *testing.T, rules manifest.UsageRules) (*env, *fakePlatform) {
	t.Helper()
	return usageEnvEP(t, rules, nil)
}

// usageEnvEP is usageEnv with a chance to change the endpoint declaration.
func usageEnvEP(t *testing.T, rules manifest.UsageRules, mut func(*manifest.Endpoint)) (*env, *fakePlatform) {
	t.Helper()
	pp := &fakePlatform{}
	e := newEnv(t, func(e *env) {
		pp.base = e.up.srv.URL
		ep := pluginUsageEndpoint()
		if mut != nil {
			mut(&ep)
		}
		pf := manifest.Platform{ID: "pu", Usage: rules, Endpoints: []manifest.Endpoint{ep}}
		info := e.addAccountType("pu", "pu_key", pp, manifest.AccountPlatform{Platform: "pu"})
		e.gen.addPlatform(info, pf, pp)
		e.accounts.addTyped(testGroup, 9, 100, "pu-9", "pu", "pu_key")
		e.accounts.groups[testGroup] = []int64{9}
	})
	return e, pp
}

// report is a UsageReport with the given token counts.
func report(in, out int64) *pluginv1.UsageReport {
	return &pluginv1.UsageReport{Tokens: &pluginv1.UsageTokens{InputTokens: in, OutputTokens: out}}
}

// ---------------------------------------------------------------- non-streaming

// A non-streaming endpoint hands the whole body to the plugin and bills what
// the plugin reports, not what the (absent) declarative rules would find.
func TestPluginUsageNonStream(t *testing.T) {
	e, pp := usageEnv(t, pluginUsageRules())
	pp.extract = func(_ context.Context, in *pluginv1.ExtractUsageRequest) (*pluginv1.UsageReport, error) {
		r := report(100, 200)
		r.Tokens.CacheReadTokens = 9
		r.Tokens.CacheCreationTokens = 12 // total, including the 1h part
		r.Tokens.CacheCreation_1HTokens = 4
		r.Facts = map[string]string{"images": "3", "tier": "fast"}
		r.UpstreamModel = "pu-upstream-1"
		r.DetailJson = `{"job":"j-1"}`
		return r, nil
	}
	res := e.do("/pu/v1/chat", body(testModel, false), nil)
	if res.status != 200 {
		t.Fatalf("status %d %s", res.status, res.body)
	}
	// The record is committed only once Execute returned the plugin's
	// facts, so waiting for it is what makes the call observable.
	rec := e.record()
	if pp.extractCount() != 1 {
		t.Fatalf("ExtractUsage called %d times", pp.extractCount())
	}
	in := pp.lastExtract()
	if in.GetStatus() != 200 || len(in.GetEvents()) != 0 {
		t.Fatalf("request %+v", in)
	}
	if !strings.Contains(string(in.GetBody()), upModel) {
		t.Fatalf("body %q", in.GetBody())
	}
	if ct := in.GetHeaders()["content-type"]; !strings.Contains(ct, "json") {
		t.Fatalf("headers %v", in.GetHeaders())
	}
	// The account is the one Execute was handed (CONTRACTS §124).
	if a := in.GetAccount(); a.GetId() != 9 || a.GetCredentialsJson() != `{"api_key":"pu-9"}` || a.GetType() != "pu_key" {
		t.Fatalf("account %+v", in.GetAccount())
	}
	if in.GetMeta().GetRequestId() == "" || in.GetMeta().GetProtocol() != "pu.chat" {
		t.Fatalf("meta %+v", in.GetMeta())
	}
	// No usageRequestFields declared: not one request field travels.
	if len(in.GetFields()) != 0 {
		t.Fatalf("request fields handed over without a declaration: %v", in.GetFields())
	}

	// cache_creation_tokens is the total, so the 5 minute figure is 12-4.
	want := core.UsageTokens{Input: 100, Output: 200, CacheRead: 9, CacheCreation: 8, CacheCreation1h: 4}
	if rec.Tokens != want {
		t.Fatalf("tokens %+v", rec.Tokens)
	}
	if rec.Metrics["images"] != float64(3) || rec.Metrics["tier"] != "fast" || len(rec.Metrics) != 2 {
		t.Fatalf("metrics %+v", rec.Metrics)
	}
	if rec.UpstreamModel != "pu-upstream-1" {
		t.Fatalf("upstream model %q", rec.UpstreamModel)
	}
	if string(rec.PluginDetail) != `{"job":"j-1"}` {
		t.Fatalf("plugin detail %s", rec.PluginDetail)
	}
	if rec.UsageExtract != core.UsageExtractPlugin {
		t.Fatalf("usage extract %q", rec.UsageExtract)
	}
	// What the plugin reported is priced like any other usage.
	if !rec.Billable || rec.Price == nil || rec.Price.ID != 9 {
		t.Fatalf("billing %+v %+v", rec.Billable, rec.Price)
	}
	// The facts were committed through the execution records.
	if st := e.settler.executionStates(); len(st) != 1 || st[0] != core.ExecutionStateCommitted {
		t.Fatalf("execution states %v", st)
	}
}

// ---------------------------------------------------------------- streaming

// A streaming response is relayed as it arrives; the host keeps only the
// events the endpoint named and hands those to the plugin in the
// observation once the stream is over. The upstream sends four events here and exactly one is collected,
// which is the assertion that matters: the stream is not buffered.
func TestPluginUsageStreamCollectsOnlyDeclaredEvents(t *testing.T) {
	e, pp := usageEnv(t, pluginUsageRules())
	pp.extract = func(_ context.Context, in *pluginv1.ExtractUsageRequest) (*pluginv1.UsageReport, error) {
		return report(int64(len(in.GetEvents())), 77), nil
	}
	res := e.do("/pu/v1/chat", body(testModel, true), nil)
	if res.status != 200 || !strings.Contains(string(res.body), "message_stop") {
		t.Fatalf("status %d %s", res.status, res.body)
	}
	// input_tokens echoes how many events the host handed over; the record
	// is committed only after Execute returned, so waiting for it orders this.
	rec := e.record()
	in := pp.lastExtract()
	if len(in.GetEvents()) != 1 {
		t.Fatalf("collected %d events, want only the declared one: %+v", len(in.GetEvents()), in.GetEvents())
	}
	ev := in.GetEvents()[0]
	if ev.GetName() != "message_delta" || !strings.Contains(string(ev.GetData()), `"output_tokens":22`) {
		t.Fatalf("event %s %s", ev.GetName(), ev.GetData())
	}
	if len(in.GetBody()) != 0 {
		t.Fatalf("body %q", in.GetBody())
	}
	if rec.Tokens.Input != 1 || rec.Tokens.Output != 77 {
		t.Fatalf("tokens %+v", rec.Tokens)
	}
}

// Every listed event is collected, however many and however large, and so
// is a non-streaming body of any size.
func TestUsageCaptureUnbounded(t *testing.T) {
	cap := newUsageCapture(pluginUsageRoute())
	for i := range 1000 {
		cap.addEvent("message_delta", fmt.Appendf(nil, `{"i":%d}`, i))
	}
	cap.addEvent("message_delta", []byte(strings.Repeat("x", 2<<20)))
	if len(cap.events) != 1001 {
		t.Fatalf("collected %d events", len(cap.events))
	}
	body := newUsageCapture(pluginUsageRoute())
	body.setBody([]byte(strings.Repeat("x", 2<<20)))
	if len(body.body) != 2<<20 {
		t.Fatalf("body: %d bytes", len(body.body))
	}
	// Undeclared events are never collected.
	cap2 := newUsageCapture(pluginUsageRoute())
	cap2.addEvent("content_block_delta", []byte(`{"a":1}`))
	if len(cap2.events) != 0 {
		t.Fatalf("collected %+v", cap2.events)
	}
	// An endpoint on the declarative rules captures nothing at all.
	if newUsageCapture(&typeRoute{usage: manifest.UsageRules{Semantics: "exclusive"}}) != nil {
		t.Fatal("capture built for an endpoint that did not ask for one")
	}
}

// The event data handed over is the event's own bytes, not a slice of the
// forwarding loop's reusable buffer.
func TestUsageCaptureCopiesEventData(t *testing.T) {
	cap := newUsageCapture(pluginUsageRoute())
	buf := []byte(`{"type":"message_delta","n":1}`)
	cap.addEvent("message_delta", buf)
	for i := range buf {
		buf[i] = 'z'
	}
	if string(cap.events[0].GetData()) != `{"type":"message_delta","n":1}` {
		t.Fatalf("data aliased the caller's buffer: %s", cap.events[0].GetData())
	}
}

// ---------------------------------------------------------------- request fields

// An endpoint that only starts a job answers {"id": ...}; the size of the job
// is in the request. usageRequestFields hands ExtractUsage exactly the
// declared paths in the observation - as raw JSON, keyed by path - and
// nothing else of the body. Every value travels whole, whatever its size.
func TestPluginUsageRequestFields(t *testing.T) {
	e, pp := usageEnvEP(t, pluginUsageRules(), func(ep *manifest.Endpoint) {
		ep.UsageRequestFields = []string{"resolution", "duration", "options.ratio", "image", "absent"}
	})
	pp.extract = func(context.Context, *pluginv1.ExtractUsageRequest) (*pluginv1.UsageReport, error) {
		return report(1, 1), nil
	}
	huge := strings.Repeat("A", 64<<10) // base64-ish, far over the 4 KiB cap it once had
	b := body(testModel, false)
	b["resolution"] = "1080p"
	b["duration"] = 5
	b["options"] = map[string]any{"ratio": "16:9", "seed": 42}
	b["image"] = huge
	if res := e.do("/pu/v1/chat", b, nil); res.status != 200 {
		t.Fatalf("status %d %s", res.status, res.body)
	}
	e.record()
	in := pp.lastExtract()
	want := map[string]string{"resolution": `"1080p"`, "duration": `5`, "options.ratio": `"16:9"`, "image": `"` + huge + `"`}
	if got := in.GetFields(); len(got) != len(want) || got["resolution"] != want["resolution"] ||
		got["duration"] != want["duration"] || got["options.ratio"] != want["options.ratio"] || got["image"] != want["image"] {
		t.Fatalf("fields = %d entries, want %d", len(got), len(want))
	}
	// The body itself never travels: not under a declared key, not anywhere.
	for k, v := range in.GetFields() {
		if strings.Contains(v, "hello there") || k == "messages" {
			t.Fatalf("request body leaked into fields: %s=%s", k, v)
		}
	}
}

// No total budget: every declared path the client sent travels, whatever
// they add up to.
func TestUsageRequestFieldsNoBudget(t *testing.T) {
	const each = 3 << 10 // twelve of them are over the 32 KiB total it once had
	var paths []string
	doc := map[string]string{}
	for i := range 12 {
		p := fmt.Sprintf("f%02d", i)
		paths = append(paths, p)
		doc[p] = strings.Repeat("x", each-2) // quoted: exactly `each` bytes of raw JSON
	}
	raw, _ := json.Marshal(doc)
	fields := usageRequestFields(paths, raw)
	if len(fields) != len(paths) {
		t.Fatalf("%d fields carried", len(fields))
	}
	for _, p := range paths {
		if len(fields[p]) != each {
			t.Fatalf("field %s missing or resized: %d bytes", p, len(fields[p]))
		}
	}
	// Nothing declared, nothing read - not even an allocation's worth.
	if f := usageRequestFields(nil, raw); f != nil {
		t.Fatalf("read without a declaration: %v", f)
	}
	// A path the client did not send is not carried.
	if f := usageRequestFields([]string{"nope"}, raw); f != nil {
		t.Fatalf("absent path reported: %v", f)
	}
}

// A body with invalid UTF-8 inside a string is not JSON (RFC 8259 §8.1), and
// gjson does not check that. Left in, the bytes would be copied into
// RequestMeta.model, where proto.Marshal refuses
// it: every plugin call of the request fails, the client gets a 5xx and the
// usage row is refused by PostgreSQL too. It is refused here, as the 400 it
// is, before anything is asked.
func TestInvalidUTF8BodyIsRejectedAsInvalidJSON(t *testing.T) {
	e := newEnv(t)
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/v1/messages",
		strings.NewReader("{\"model\":\""+testModel+"\xff\",\"max_tokens\":1,\"messages\":[]}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", testKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d", resp.StatusCode)
	}
	rec := e.record()
	if rec.ErrorType != errTypeInvalidRequest || rec.StatusCode != 400 {
		t.Fatalf("record %+v", rec)
	}
	if e.plat.buildCount() != 0 {
		t.Fatalf("a plugin was called %d times with a body it cannot be handed", e.plat.buildCount())
	}
}

// ---------------------------------------------------------------- the boundary

// A plugin states upstream facts and nothing else. It has no field for
// attribution or cost in UsageReport, and the facts map is not a back door:
// keys the manifest never declared are dropped, and so are values that do not
// fit the declared type. What the record carries for user, key, group,
// account, multiplier and price is what the core put there.
func TestPluginUsageCannotForgeAttributionOrCost(t *testing.T) {
	e, pp := usageEnv(t, pluginUsageRules())
	pp.extract = func(context.Context, *pluginv1.ExtractUsageRequest) (*pluginv1.UsageReport, error) {
		r := report(5, 5)
		r.Facts = map[string]string{
			// Attribution and money, in the only map a plugin can fill.
			"user_id": "999", "api_key_id": "999", "group_id": "999", "account_id": "999",
			"total_cost": "0", "rate_multiplier": "0", "price_id": "1",
			// A declared fact with a value outside its declared enum.
			"tier": "free",
			// A declared fact of the wrong type.
			"images": "many",
		}
		r.DetailJson = `{"user_id":999,"total_cost":0,"account_id":999}`
		return r, nil
	}
	e.do("/pu/v1/chat", body(testModel, false), nil)
	rec := e.record()
	if rec.UserID != testUser || rec.APIKeyID != 5 || rec.GroupID != testGroup {
		t.Fatalf("attribution %d %d %d", rec.UserID, rec.APIKeyID, rec.GroupID)
	}
	if rec.AccountID == nil || *rec.AccountID != 9 {
		t.Fatalf("account %v", rec.AccountID)
	}
	if rec.RateMultiplier.String() != "1.5" || rec.Price == nil || rec.Price.ID != 9 {
		t.Fatalf("pricing %s %+v", rec.RateMultiplier, rec.Price)
	}
	if rec.PluginKey != "pu" || rec.Platform != "pu" || rec.Endpoint != "/pu/v1/chat" || rec.NodeID != "node-test" {
		t.Fatalf("core fields %+v", rec)
	}
	// Every fact was refused: undeclared keys, a value outside the enum, a
	// non-number for a number.
	if len(rec.Metrics) != 0 {
		t.Fatalf("metrics %+v", rec.Metrics)
	}
	// detail_json is stored verbatim but is never read by anything: it says
	// nothing about the record's own fields.
	if string(rec.PluginDetail) != `{"user_id":999,"total_cost":0,"account_id":999}` {
		t.Fatalf("plugin detail %s", rec.PluginDetail)
	}
}

// ---------------------------------------------------------------- detail_json

func TestPluginUsageDetailJSONLimits(t *testing.T) {
	for _, tc := range []struct {
		name, detail, want string
	}{
		{"object", `{"a":1}`, `{"a":1}`},
		{"empty", "", ""},
		// Any size is stored as it is.
		{"large", `{"pad":"` + strings.Repeat("x", 64<<10) + `"}`, `{"pad":"` + strings.Repeat("x", 64<<10) + `"}`},
		{"not_an_object", `[1,2,3]`, ""},
		{"invalid", `{nope`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, pp := usageEnv(t, pluginUsageRules())
			pp.extract = func(context.Context, *pluginv1.ExtractUsageRequest) (*pluginv1.UsageReport, error) {
				r := report(1, 1)
				r.DetailJson = tc.detail
				return r, nil
			}
			e.do("/pu/v1/chat", body(testModel, false), nil)
			rec := e.record()
			got := string(rec.PluginDetail)
			if got != tc.want {
				t.Fatalf("plugin detail %q, want %q", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------- failure

// A plugin that cannot read the usage answers host_usage (the SDK's
// ExecuteDefault does so when ExtractUsage fails or reports nothing). When
// the host's reading holds no usage either - these rules declare no json
// map - there are no facts and the core makes none up: the execution is
// closed unreported like one that stated nothing (a free usage_unreported
// record, nothing billed, the held-back answer not published). A reading
// that holds usage is billed (TestPluginUsageFallbackUsesTheDeclarativeRules).
func TestPluginUsageFallsBackWhenThePluginFails(t *testing.T) {
	cases := map[string]func(*fakePlatform){
		"unimplemented": func(p *fakePlatform) { p.extract = nil },
		"error": func(p *fakePlatform) {
			p.extract = func(context.Context, *pluginv1.ExtractUsageRequest) (*pluginv1.UsageReport, error) {
				return nil, fmt.Errorf("boom")
			}
		},
		"nil_answer": func(p *fakePlatform) {
			p.extract = func(context.Context, *pluginv1.ExtractUsageRequest) (*pluginv1.UsageReport, error) {
				return nil, nil
			}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			e, pp := usageEnv(t, pluginUsageRules())
			setup(pp)
			res := e.do("/pu/v1/chat", body(testModel, false), nil)
			if res.status != 503 || strings.Contains(string(res.body), "hello") {
				t.Fatalf("held-back answer published: %d %s", res.status, res.body)
			}
			assertUnreported(t, e)
		})
	}
}

// A relay that did not wait, on an endpoint whose usage the plugin reads,
// whose host reading holds no usage: the client keeps the answer it was
// sent, and the record is the free unreported one, logged at ERROR.
func TestNoWaitEmptyHostReadingIsUnreported(t *testing.T) {
	for _, stream := range []bool{false, true} {
		logs := captureWarnings(t)
		e, _ := scriptedUsageEnv(t, func(ctx context.Context, p *fakePlatform, in *pluginv1.ExecuteRequest, client core.ExecutionClient) (core.ExecutionResult, error) {
			req, err := p.buildUpstream(ctx, in)
			if err != nil {
				return core.ExecutionResult{}, err
			}
			if err := client.Check(ctx, req); err != nil {
				return core.ExecutionResult{}, err
			}
			return core.ExecutionResult{Response: &pluginv1.ExecuteResponse{Report: &pluginv1.ExecuteResponse_HostUsage{HostUsage: &pluginv1.HostUsage{}}}, Relay: req}, nil
		})
		res := e.do("/pu/v1/chat", body(testModel, stream), nil)
		if res.status != 200 || len(res.body) == 0 {
			t.Fatalf("stream %v: the answer did not reach the client: %d %s", stream, res.status, res.body)
		}
		rec := e.record()
		if rec.Success || rec.Billable || rec.ErrorType != core.UsageErrorUnreported || rec.UsageExtract != core.UsageExtractUnreported ||
			rec.Tokens != (core.UsageTokens{}) || rec.StatusCode != 200 {
			t.Fatalf("stream %v: free record %+v", stream, rec)
		}
		if st := e.settler.executionStates(); len(st) != 0 {
			t.Fatalf("stream %v: execution states %v", stream, st)
		}
		if !strings.Contains(logs.String(), "level=ERROR") || !strings.Contains(logs.String(), "holds no usage") {
			t.Fatalf("stream %v: logs %s", stream, logs)
		}
	}
}

// Only a relay that waits keeps a copy of the answer for the plugin's
// observation: one that does not wait is read by no plugin, so forward
// captures nothing for it even on an endpoint whose usage the plugin reads.
func TestForwardCapturesOnlyForAWaitingRelay(t *testing.T) {
	const answer = `{"ok":true}`
	rt := &typeRoute{platform: "pu", pluginUsage: true}
	for _, waits := range []bool{false, true} {
		w := httptest.NewRecorder()
		gc, _ := gin.CreateTestContext(w)
		gc.Request = httptest.NewRequest(http.MethodPost, "/pu/v1/chat", nil)
		c := &call{g: &Gateway{}, c: gc, gen: newFakeGen(), rec: &core.UsageRecord{}}
		var capture *usageCapture
		if waits {
			capture = newUsageCapture(rt)
		}
		resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(answer)), Request: httptest.NewRequest(http.MethodPost, "/", nil)}
		if res := c.forward(context.Background(), rt, &pluginv1.Account{Id: 1}, resp, nil, capture); res.kind != attemptDone || w.Body.String() != answer {
			t.Fatalf("waits %v: forward %+v, client got %q", waits, res, w.Body.String())
		}
		switch {
		case !waits && c.usage != nil:
			t.Fatalf("a relay that does not wait captured its answer: %+v", c.usage)
		case waits && (c.usage == nil || string(c.usage.cap.body) != answer):
			t.Fatalf("a relay that waits kept no observation: %+v", c.usage)
		}
	}
}

// The fallback is the declarative rules' answer, not zero by definition: an
// endpoint that keeps its json map gets billed from it when the plugin fails.
func TestPluginUsageFallbackUsesTheDeclarativeRules(t *testing.T) {
	rules := pluginUsageRules()
	rules.JSON = &manifest.UsageMap{Map: map[string]string{
		manifest.UsageInputTokens:  "usage.input_tokens",
		manifest.UsageOutputTokens: "usage.output_tokens",
	}}
	logs := captureWarnings(t)
	e, _ := usageEnv(t, rules)
	e.do("/pu/v1/chat", body(testModel, false), nil)
	rec := e.record()
	if rec.UsageExtract != core.UsageExtractFallback {
		t.Fatalf("usage extract %q", rec.UsageExtract)
	}
	if rec.Tokens.Input != upInput || rec.Tokens.Output != upOutput || !rec.Billable {
		t.Fatalf("tokens %+v billable %v", rec.Tokens, rec.Billable)
	}
	// The plugin's choice of the host's reading is no failure: no warning.
	if strings.Contains(logs.String(), "host's reading") || strings.Contains(logs.String(), "usage extraction failed") {
		t.Fatalf("a deliberate host_usage warned: %s", logs)
	}
}

// ---------------------------------------------------------------- not declared

// Endpoints that say nothing about usage.source keep the behaviour they had
// before this existed: the declarative rules, and not one call.
func TestExtractUsageNotCalledWithoutDeclaration(t *testing.T) {
	e := newEnv(t)
	for _, stream := range []bool{false, true} {
		if r := e.messages(body(testModel, stream)); r.status != 200 {
			t.Fatalf("status %d %s", r.status, r.body)
		}
		rec := e.record()
		if rec.Tokens.Input != upInput || rec.Tokens.Output != upOutput {
			t.Fatalf("tokens %+v", rec.Tokens)
		}
		if rec.UsageExtract != "" || rec.PluginDetail != nil {
			t.Fatalf("record carries plugin usage state: %q %s", rec.UsageExtract, rec.PluginDetail)
		}
	}
	if e.plat.extractCount() != 0 {
		t.Fatalf("ExtractUsage called %d times", e.plat.extractCount())
	}
}
