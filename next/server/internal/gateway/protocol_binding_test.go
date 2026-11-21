package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/gateway/convert"
	"github.com/tidwall/gjson"
)

// The protocol binding of an account (DESIGN-2026-11-17-PROTOCOL-BINDING):
// which protocols of its type's own platforms it serves, which protocols
// each model is served on, and the cross-platform conversions it enables,
// none by default.

const (
	messagesProtocol = "anthropic.messages"
	notServedMessage = "no account in this group serves this model on this endpoint"
)

// ---------------------------------------------------------------- routes

// plannedCall plans the routes of a request to the endpoint of a built-in
// platform speaking protocol, over types and the built-in converters.
func plannedCall(t testing.TB, platform, protocol string, types ...core.AccountTypeBinding) *call {
	t.Helper()
	gen := newFakeGen()
	gen.accountTypes = types
	pf := builtinPlatform(t, platform)
	c := &call{g: &Gateway{conv: convert.Default()}, gen: gen, ep: endpointOf(t, pf, protocol), platform: platform, pf: pf}
	c.planRoutes()
	return c
}

// typeOn is account type t/id declaring platforms.
func typeOn(id string, platforms ...manifest.AccountPlatform) core.AccountTypeBinding {
	return core.AccountTypeBinding{Plugin: core.PluginInfo{Key: "t"}, Client: &fakePlatform{},
		Type: manifest.AccountType{ID: id, Platforms: platforms}}
}

// A route is cross-platform exactly when the account type does not declare
// the platform of the client endpoint (design §2): native and adapted routes
// are not, whatever they send upstream, and a count_tokens request answered
// locally follows the same rule.
func TestRouteCrossPlatform(t *testing.T) {
	onOpenAI, onAnthropic := manifest.AccountPlatform{Platform: "openai"}, manifest.AccountPlatform{Platform: "anthropic"}
	types := []core.AccountTypeBinding{
		typeOn("openai", onOpenAI),
		typeOn("codex", manifest.AccountPlatform{Platform: "openai", Endpoints: []string{"responses"},
			Adapted: map[string]manifest.AdaptOptions{"chat_completions": {}}}),
		typeOn("claude", onAnthropic),
		typeOn("messages", manifest.AccountPlatform{Platform: "anthropic", Endpoints: []string{"messages"}}),
		typeOn("both", onOpenAI, onAnthropic),
	}
	for _, tc := range []struct {
		platform, protocol, typ, upstream string
		cross                             bool
	}{
		{"openai", "openai.chat", "openai", "openai.chat", false},
		{"openai", "openai.chat", "codex", "openai.responses", false}, // adapted within the platform
		{"openai", "openai.chat", "claude", messagesProtocol, true},
		{"openai", "openai.chat", "both", "openai.chat", false},
		{"anthropic", messagesProtocol, "claude", messagesProtocol, false},
		{"anthropic", messagesProtocol, "openai", "openai.chat", true},
		{"anthropic", messagesProtocol, "codex", "openai.responses", true},
		{"anthropic", messagesProtocol, "both", messagesProtocol, false},
		{"anthropic", "anthropic.count_tokens", "claude", "anthropic.count_tokens", false},
		{"anthropic", "anthropic.count_tokens", "messages", upstreamLocal, false},
		{"anthropic", "anthropic.count_tokens", "openai", upstreamLocal, true},
		{"anthropic", "anthropic.count_tokens", "both", "anthropic.count_tokens", false},
	} {
		c := plannedCall(t, tc.platform, tc.protocol, types...)
		rt := c.routes[core.AccountTypeKey{PluginKey: "t", Type: tc.typ}]
		if rt == nil || rt.upstream != tc.upstream || rt.cross != tc.cross {
			t.Errorf("%s by %s: route %+v, want upstream %s cross %v", tc.protocol, tc.typ, rt, tc.upstream, tc.cross)
		}
	}
}

// ---------------------------------------------------------------- eligibility

var (
	ownType   = core.AccountTypeKey{PluginKey: "own", Type: "t"}
	crossType = core.AccountTypeKey{PluginKey: "cross", Type: "t"}
)

// boundCall is a request for model "m" on protocol whose routes reach the
// accounts of ownType on their own platform and those of crossType through
// a cross-platform conversion. refs are the models the request references.
func boundCall(protocol string, refs ...string) *call {
	c := &call{model: "m", ep: manifest.Endpoint{Protocol: protocol, Billing: "free"},
		routes: map[core.AccountTypeKey]*typeRoute{ownType: {}, crossType: {cross: true}}}
	for _, m := range refs {
		c.modelRefs = append(c.modelRefs, modelReference{kind: "advisor", model: m})
	}
	return c
}

func accountOf(id int64, key core.AccountTypeKey, rules *core.ProtocolRules) core.AccountRef {
	return core.AccountRef{ID: id, PluginKey: key.PluginKey, Type: key.Type, ProtocolRules: rules}
}

func TestIneligibleReasonProtocolBinding(t *testing.T) {
	rules := func(protocols []string, r ...core.ModelProtocolRule) *core.ProtocolRules {
		return core.NewProtocolRules(protocols, r)
	}
	const chat, responses, count = "openai.chat", "openai.responses", "anthropic.count_tokens"
	for _, tc := range []struct {
		name     string
		protocol string
		refs     []string
		typ      core.AccountTypeKey
		models   []string
		rules    *core.ProtocolRules
		reason   string
		detail   string
	}{
		{name: "unbound account on its own platform", protocol: chat, typ: ownType},
		{name: "unbound account is never converted for", protocol: messagesProtocol, typ: crossType,
			reason: core.SchedConversionNotEnabled, detail: "m"},
		{name: "protocol in the binding", protocol: chat, typ: ownType, rules: rules([]string{chat})},
		{name: "protocol left out of the binding", protocol: responses, typ: ownType, rules: rules([]string{chat}),
			reason: core.SchedProtocolClosed, detail: responses},
		{name: "every own protocol closed", protocol: chat, typ: ownType,
			rules:  rules([]string{}, protocolRule([]string{messagesProtocol}, "x")),
			reason: core.SchedProtocolClosed, detail: chat},
		{name: "conversion enabled for the model", protocol: messagesProtocol, typ: crossType,
			rules: rules(nil, protocolRule([]string{messagesProtocol}, "m"))},
		{name: "conversion enabled for another model", protocol: messagesProtocol, typ: crossType,
			rules:  rules(nil, protocolRule([]string{messagesProtocol}, "x")),
			reason: core.SchedConversionNotEnabled, detail: "m"},
		{name: "model served on another protocol only", protocol: chat, typ: ownType,
			rules:  rules(nil, protocolRule([]string{responses}, "m")),
			reason: core.SchedModelProtocolClosed, detail: "m"},
		{name: "model converted from another protocol only", protocol: messagesProtocol, typ: crossType,
			rules:  rules(nil, protocolRule([]string{"gemini.generate"}, "m")),
			reason: core.SchedModelProtocolClosed, detail: "m"},
		{name: "other models converted", protocol: messagesProtocol, typ: crossType,
			rules: rules(nil, protocolRule([]string{messagesProtocol}, core.OtherModels))},
		{name: "other models limited to a protocol", protocol: chat, typ: ownType,
			rules:  rules(nil, protocolRule([]string{responses}, core.OtherModels)),
			reason: core.SchedModelProtocolClosed, detail: "m"},
		{name: "a rule naming the model wins over the other models", protocol: messagesProtocol, typ: crossType,
			rules:  rules(nil, protocolRule([]string{messagesProtocol}, core.OtherModels), protocolRule([]string{chat}, "m")),
			reason: core.SchedModelProtocolClosed, detail: "m"},
		{name: "own protocol a rule lists but the account closes", protocol: responses, typ: ownType,
			rules:  rules([]string{chat}, protocolRule([]string{responses}, "m")),
			reason: core.SchedProtocolClosed, detail: responses},
		{name: "count_tokens follows messages, converted", protocol: count, typ: crossType,
			rules: rules(nil, protocolRule([]string{messagesProtocol}, "m"))},
		{name: "count_tokens follows messages, not converted", protocol: count, typ: crossType,
			reason: core.SchedConversionNotEnabled, detail: "m"},
		{name: "count_tokens follows messages, own platform", protocol: count, typ: ownType,
			rules: rules([]string{messagesProtocol})},
		{name: "count_tokens follows messages, closed", protocol: count, typ: ownType,
			rules:  rules([]string{}, protocolRule([]string{chat}, "x")),
			reason: core.SchedProtocolClosed, detail: count},
		{name: "referenced model not converted for", protocol: messagesProtocol, refs: []string{"advisor"}, typ: crossType,
			rules:  rules(nil, protocolRule([]string{messagesProtocol}, "m")),
			reason: core.SchedConversionNotEnabled, detail: "advisor"},
		{name: "referenced model converted for", protocol: messagesProtocol, refs: []string{"advisor"}, typ: crossType,
			rules: rules(nil, protocolRule([]string{messagesProtocol}, "m", "advisor"))},
		{name: "referenced model served on another protocol only", protocol: messagesProtocol, refs: []string{"x", "advisor"},
			typ: ownType, rules: rules(nil, protocolRule([]string{chat}, "advisor")),
			reason: core.SchedModelProtocolClosed, detail: "advisor"},
		{name: "the binding is judged before the model list", protocol: messagesProtocol, typ: crossType, models: []string{"x"},
			reason: core.SchedConversionNotEnabled, detail: "m"},
		{name: "the model list still applies", protocol: chat, typ: ownType, models: []string{"x"},
			rules: rules([]string{chat}), reason: core.SchedModelNotServed, detail: "m"},
		{name: "no route", protocol: chat, typ: core.AccountTypeKey{PluginKey: "none", Type: "t"},
			reason: core.SchedNoRoute, detail: "none/t"},
	} {
		c := boundCall(tc.protocol, tc.refs...)
		ref := accountOf(1, tc.typ, tc.rules)
		ref.Models = tc.models
		reason, detail, ok := c.ineligibleReason(&ref)
		if reason != tc.reason || detail != tc.detail || ok != (tc.reason == "") {
			t.Errorf("%s: %q %q %v, want %q %q", tc.name, reason, detail, ok, tc.reason, tc.detail)
		}
		// A refreshed snapshot and a later WebSocket turn ask the same test.
		if c.candidateEligible(&ref) != ok {
			t.Errorf("%s: candidateEligible differs", tc.name)
		}
	}
}

// readsAccounts counts the reads of the accounts a group does not offer:
// those resting, which decide a 404 and fail with err, and those the record
// lists.
type readsAccounts struct {
	*fakeAccounts
	resting, unschedulable int
	err                    error
}

func (a *readsAccounts) Resting(ctx context.Context, group int64, types []core.AccountTypeKey) ([]core.AccountRef, error) {
	a.resting++
	if a.err != nil {
		return nil, a.err
	}
	return a.fakeAccounts.Resting(ctx, group, types)
}

func (a *readsAccounts) Unschedulable(ctx context.Context, group int64) ([]core.UnschedulableAccount, error) {
	a.unschedulable++
	return a.fakeAccounts.Unschedulable(ctx, group)
}

// The request is not served (404) only when accounts were offered and each
// one is closed by its protocol binding; an account left out for anything
// else, or one that is only out for a while, keeps the answer it had.
func TestProtocolNotServedOnlyWhenEveryAccountIsClosed(t *testing.T) {
	notConverted := accountOf(1, crossType, nil)
	protocolClosed := accountOf(2, ownType, core.NewProtocolRules([]string{"openai.chat"}, nil))
	modelClosed := accountOf(3, ownType, core.NewProtocolRules(nil, []core.ModelProtocolRule{protocolRule([]string{"openai.chat"}, "m")}))
	serving := accountOf(4, ownType, nil)
	otherModels := accountOf(5, ownType, nil)
	otherModels.Models = []string{"x"}

	for _, tc := range []struct {
		name     string
		offered  []core.AccountRef
		eligible int
		closed   bool
	}{
		{"no account offered", nil, 0, false},
		{"every account closed", []core.AccountRef{notConverted, protocolClosed, modelClosed}, 0, true},
		{"an account serves", []core.AccountRef{notConverted, serving}, 1, false},
		{"an account is left out for its model list", []core.AccountRef{notConverted, otherModels}, 0, false},
	} {
		c := boundCall(messagesProtocol)
		cands, closed := c.eligibleAccounts(tc.offered)
		if len(cands) != tc.eligible || closed != tc.closed || c.sched.candidates != len(tc.offered) {
			t.Errorf("%s: %d eligible of %d, closed %v", tc.name, len(cands), c.sched.candidates, closed)
		}
		if len(c.sched.skips) != len(tc.offered)-tc.eligible {
			t.Errorf("%s: skips %+v", tc.name, c.sched.skips)
		}
	}

	// The accounts of the group that are out for a while are judged like the
	// offered ones: account 7 is of ownType, account 8 of crossType.
	cooling := func(id int64) func(a *readsAccounts) {
		return func(a *readsAccounts) { a.cooldown[id] = time.Now().Add(time.Minute) }
	}
	for _, tc := range []struct {
		name   string
		closed bool
		setup  func(a *readsAccounts)
		status int // 0: the request goes on
	}{
		{name: "an account can serve", closed: false, setup: cooling(7)},
		{name: "every account closed", closed: true, status: 404},
		{name: "the rest of the group is disabled", closed: true, status: 404,
			setup: func(a *readsAccounts) { a.disabled[7] = "admin" }},
		{name: "an account that serves the request is cooling down", closed: true, setup: cooling(7)},
		{name: "a cooling account is closed by its binding too", closed: true, status: 404, setup: cooling(8)},
		{name: "a cooling account is closed, another serves", closed: true,
			setup: func(a *readsAccounts) { cooling(7)(a); cooling(8)(a) }},
		{name: "a cooling account is out for its model list", closed: true,
			setup: func(a *readsAccounts) {
				cooling(7)(a)
				a.set(7, func(acc *core.Account) { acc.Models = []string{"x"} })
			}},
		{name: "a cooling account closed the protocol", closed: true, status: 404,
			setup: func(a *readsAccounts) {
				cooling(7)(a)
				a.bind(7, []string{"openai.chat"})
			}},
		{name: "the resting accounts cannot be read", closed: true,
			setup: func(a *readsAccounts) { a.err = errors.New("redis down") }},
	} {
		dir := &readsAccounts{fakeAccounts: newFakeAccounts()}
		dir.addTyped(testGroup, 7, 1, "k7", ownType.PluginKey, ownType.Type)
		dir.addTyped(testGroup, 8, 1, "k8", crossType.PluginKey, crossType.Type)
		if tc.setup != nil {
			tc.setup(dir)
		}
		c := boundCall(messagesProtocol)
		c.routeKeys = []core.AccountTypeKey{ownType, crossType}
		c.g, c.principal = &Gateway{d: Deps{Accounts: dir}}, &core.APIKeyPrincipal{Group: core.GroupInfo{ID: testGroup}}
		e := c.protocolNotServed(context.Background(), tc.closed)
		if want := map[bool]int{true: 1}[tc.closed]; dir.resting != want || dir.unschedulable != 0 {
			t.Errorf("%s: %d reads of the resting accounts, %d of the unschedulable", tc.name, dir.resting, dir.unschedulable)
		}
		if (e == nil) != (tc.status == 0) || e != nil && (e.Status != tc.status || e.Message != notServedMessage || e.RecordType != errTypeNoAccount) {
			t.Errorf("%s: %+v", tc.name, e)
		}
		// Only the record of a request that found no account reads what the
		// group holds from the database.
		c.rec = &core.UsageRecord{ErrorType: errTypeNoAccount, StatusCode: 404}
		c.concludeScheduling(context.Background())
		if dir.unschedulable != 1 {
			t.Errorf("%s: the group was read %d times for the record", tc.name, dir.unschedulable)
		}
	}
}

// ---------------------------------------------------------------- end to end

// bindingEnv is the fixture of the end-to-end tests: the built-in
// converters and, alone in the test group, account 11 of an OpenAI account
// type serving chat, responses and embeddings (priority 99, like the first
// Anthropic account of newEnv, which a test adds back to the group). chat
// and embeddings go to api, responses to resp.
type bindingEnv struct {
	*env
	api  *apiUpstream
	resp *responsesUpstream
	plat *fakePlatform
}

const otherModel = "claude-opus-4-1"

func newBindingEnv(t *testing.T) *bindingEnv {
	t.Helper()
	api, resp := newAPIUpstream(t), newResponsesUpstream(t)
	resp.streamOnly = false
	plat := apiPlatform(api)
	byPath := plat.route
	plat.route = func(in *pluginv1.ExecuteRequest) string {
		if in.GetMeta().GetProtocol() == "openai.responses" {
			return resp.srv.URL + "/v1/responses"
		}
		return byPath(in)
	}
	e := newEnv(t, func(e *env) {
		e.conv = convert.Default()
		e.addAccountType("openai", "apikey", plat, manifest.AccountPlatform{Platform: manifest.PlatformOpenAI})
		e.accounts.addTyped(testGroup, 11, 99, "oai-11", "openai", "apikey")
		e.accounts.groups[testGroup] = []int64{11}
		for _, m := range []string{"gpt-4o", "gpt-5", "text-embedding-3-small", otherModel} {
			e.pricer.rules[m] = &core.PriceRule{ID: 10, Model: m, Mode: "per_token"}
		}
	})
	return &bindingEnv{env: e, api: api, resp: resp, plat: plat}
}

// sent is how many requests reached an upstream.
func (b *bindingEnv) sent() int {
	b.resp.mu.Lock()
	defer b.resp.mu.Unlock()
	return len(b.api.keys()) + len(b.resp.bodies) + len(b.up.keys())
}

func (b *bindingEnv) chatFor(model string) result {
	b.t.Helper()
	return b.post("/v1/chat/completions", map[string]any{"model": model,
		"messages": []any{map[string]any{"role": "user", "content": "hello there"}}}, bearer())
}

func (b *bindingEnv) responsesFor(model string) result {
	b.t.Helper()
	return b.post("/v1/responses", map[string]any{"model": model, "input": "hi"}, bearer())
}

func (b *bindingEnv) messagesFor(model string) result {
	b.t.Helper()
	return b.messages(body(model, false))
}

func (b *bindingEnv) countFor(model string) result {
	b.t.Helper()
	return b.do("/v1/messages/count_tokens", body(model, false), nil)
}

// served checks a request an account served: 200 and the record of account
// id sending upstream on the given protocol.
func (b *bindingEnv) served(r result, id int64, upstream string) *core.UsageRecord {
	b.t.Helper()
	if r.status != 200 {
		b.t.Fatalf("%d %s", r.status, r.body)
	}
	rec := b.record()
	if !rec.Success || rec.AccountID == nil || *rec.AccountID != id || rec.UpstreamProtocol != upstream || rec.Attempts != 1 {
		b.t.Fatalf("served by %v on %s in %d attempts: %+v", rec.AccountID, rec.UpstreamProtocol, rec.Attempts, rec)
	}
	return rec
}

// notServed checks a request every account offered is closed to: 404 in the
// endpoint's error format, nothing converted, built or sent, no attempt, and
// a scheduling trace naming why account id was left out.
func (b *bindingEnv) notServed(do func() result, id int64, reason, detail string) *core.UsageRecord {
	b.t.Helper()
	sent, built := b.sent(), b.plat.buildCount()+b.env.plat.buildCount()
	r := do()
	if r.status != 404 || r.json().Get("error.message").String() != notServedMessage {
		b.t.Fatalf("%d %s", r.status, r.body)
	}
	if n := b.sent(); n != sent {
		b.t.Fatalf("%d requests reached an upstream", n-sent)
	}
	if n := b.plat.buildCount() + b.env.plat.buildCount(); n != built {
		b.t.Fatalf("%d upstream requests built", n-built)
	}
	rec := b.record()
	if rec.Success || rec.StatusCode != 404 || rec.ErrorType != errTypeNoAccount || rec.Attempts != 0 || rec.AccountID != nil ||
		rec.Conversion != nil || rec.UpstreamProtocol != "" || rec.AttemptTrail != nil {
		b.t.Fatalf("record %+v conversion %+v", rec, rec.Conversion)
	}
	tr := rec.Scheduling
	if tr == nil || tr.Outcome != core.SchedulingNone || tr.Eligible != 0 || tr.Counts[reason] == 0 {
		b.t.Fatalf("trace %+v", tr)
	}
	if s := skipOf(tr, id, reason); s == nil || s.Detail != detail {
		b.t.Fatalf("skips %+v, want %d %s %s", tr.Skipped, id, reason, detail)
	}
	// The usage record keeps the trace under anomalies.scheduling.
	var stored core.SchedulingTrace
	if raw := rec.Anomalies()[core.AnomalyScheduling]; json.Unmarshal([]byte(raw), &stored) != nil || skipOf(&stored, id, reason) == nil {
		b.t.Fatalf("anomalies %q", raw)
	}
	return rec
}

// (a) By default an account is not converted for: a group of OpenAI accounts
// answers /v1/messages with 404, and the request is neither converted nor
// sent.
func TestProtocolBindingConversionIsOffByDefault(t *testing.T) {
	b := newBindingEnv(t)
	rec := b.notServed(func() result { return b.messagesFor(testModel) }, 11, core.SchedConversionNotEnabled, testModel)
	if rec.Protocol != messagesProtocol || rec.Scheduling.Candidates != 1 {
		t.Fatalf("record %+v trace %+v", rec, rec.Scheduling)
	}
	// An Anthropic client reads a 404, not the overload it retries.
	r := b.messagesFor(testModel)
	if r.status != 404 || r.json().Get("type").String() != "error" || r.json().Get("error.type").String() != "not_found_error" {
		t.Fatalf("%d %s", r.status, r.body)
	}
	b.record()
	// The same holds for a streaming request and for a Gemini client.
	b.notServed(func() result { return b.messages(body(testModel, true)) }, 11, core.SchedConversionNotEnabled, testModel)
	rec = b.notServed(func() result {
		return b.post("/v1beta/models/gpt-4o:generateContent", map[string]any{"contents": []any{
			map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hello there"}}}}}, map[string]string{"x-goog-api-key": testKey})
	}, 11, core.SchedConversionNotEnabled, "gpt-4o")
	if rec.Protocol != "gemini.generate" {
		t.Fatalf("record %+v", rec)
	}

	// The converter is not even asked: the account is left out before the
	// request is prepared for its type.
	f := &fakeConv{}
	e, q, qp := convEnv(t, f)
	e.accounts.bind(6, nil)
	disableNative(e)
	if r := e.messages(body(testModel, false)); r.status != 404 || r.json().Get("error.message").String() != notServedMessage {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if rec := e.record(); f.prepares != 0 || qp.buildCount() != 0 || len(q.bodies) != 0 || rec.Conversion != nil || rec.Attempts != 0 {
		t.Fatalf("prepares %d builds %d sent %d record %+v", f.prepares, qp.buildCount(), len(q.bodies), rec)
	}
}

// (b) (c) A conversion is enabled per model: the model whose rule lists the
// Anthropic protocol is converted, another model of the same account is not.
func TestProtocolBindingConversionEnabledPerModel(t *testing.T) {
	b := newBindingEnv(t)
	b.accounts.bind(11, nil, protocolRule([]string{messagesProtocol}, testModel))

	r := b.messagesFor(testModel)
	if j := r.json(); j.Get("type").String() != "message" || j.Get("content.0.text").String() != "hello" {
		t.Fatalf("%d %s", r.status, r.body)
	}
	rec := b.served(r, 11, "openai.chat")
	if rec.Protocol != messagesProtocol || rec.Model != testModel || rec.Conversion == nil || rec.Conversion.Grade == "native" ||
		len(rec.Conversion.Hops) != 2 || rec.Scheduling != nil {
		t.Fatalf("record %+v conversion %+v", rec, rec.Conversion)
	}
	if b.sent() != 1 || len(b.api.keys()) != 1 {
		t.Fatalf("%d upstream requests", b.sent())
	}
	if m := b.plat.builds[0].GetMeta(); m.GetProtocol() != "openai.chat" || m.GetClientProtocol() != messagesProtocol {
		t.Fatalf("build meta %+v", m)
	}
	// The plugin's Execute is handed the converted body, not the client's:
	// the converter writes the chat request without the Messages "stream".
	if in := gjson.ParseBytes(b.plat.builds[0].GetBody()); in.Get("stream").Exists() || in.Get("model").String() != testModel ||
		in.Get("messages.0.content").String() != "hello there" {
		t.Fatalf("body handed to the plugin %s", b.plat.builds[0].GetBody())
	}

	// The same account, a model the rule does not list.
	b.notServed(func() result { return b.messagesFor(otherModel) }, 11, core.SchedConversionNotEnabled, otherModel)
	// The listed model is served on the listed protocol only: not on chat,
	// although chat is a protocol of the account's own platform.
	b.notServed(func() result { return b.chatFor(testModel) }, 11, core.SchedModelProtocolClosed, testModel)
	// Other models keep every protocol of the platform.
	b.served(b.chatFor("gpt-4o"), 11, "openai.chat")
}

// (d) protocols limits the protocols of the type's own platforms the
// account serves.
func TestProtocolBindingOwnProtocols(t *testing.T) {
	b := newBindingEnv(t)
	b.accounts.bind(11, []string{"openai.chat"})
	b.served(b.chatFor("gpt-4o"), 11, "openai.chat")
	b.notServed(func() result { return b.responsesFor("gpt-5") }, 11, core.SchedProtocolClosed, "openai.responses")
	b.notServed(func() result {
		return b.post("/v1/embeddings", map[string]any{"model": "text-embedding-3-small", "input": "abc"}, bearer())
	}, 11, core.SchedProtocolClosed, "openai.embeddings")
	// Limiting the own protocols enables no conversion.
	b.notServed(func() result { return b.messagesFor(testModel) }, 11, core.SchedConversionNotEnabled, testModel)

	// An empty list closes them all; the conversions of the rules remain.
	b.accounts.bind(11, []string{}, protocolRule([]string{messagesProtocol}, testModel))
	b.notServed(func() result { return b.chatFor("gpt-4o") }, 11, core.SchedProtocolClosed, "openai.chat")
	b.served(b.messagesFor(testModel), 11, "openai.chat")
}

// (e) A model a rule lists is served on the rule's protocols only; an own
// protocol the rule lists still has to be one the account serves.
func TestProtocolBindingModelProtocols(t *testing.T) {
	b := newBindingEnv(t)
	b.accounts.bind(11, nil, protocolRule([]string{"openai.responses"}, "gpt-5"))
	b.served(b.responsesFor("gpt-5"), 11, "openai.responses")
	b.notServed(func() result { return b.chatFor("gpt-5") }, 11, core.SchedModelProtocolClosed, "gpt-5")
	// A model no rule lists is served on every own protocol.
	b.served(b.chatFor("gpt-4o"), 11, "openai.chat")
	b.served(b.responsesFor("gpt-4o"), 11, "openai.responses")

	b.accounts.bind(11, []string{"openai.chat"}, protocolRule([]string{"openai.responses"}, "gpt-5"))
	b.notServed(func() result { return b.responsesFor("gpt-5") }, 11, core.SchedProtocolClosed, "openai.responses")
}

// (f) The "*" rule covers every model no other rule lists.
func TestProtocolBindingOtherModelsRule(t *testing.T) {
	b := newBindingEnv(t)
	b.accounts.bind(11, nil, protocolRule([]string{"openai.chat", messagesProtocol}, core.OtherModels),
		protocolRule([]string{"openai.responses"}, "gpt-5"))
	// Any model is converted for, and served on chat.
	b.served(b.messagesFor(otherModel), 11, "openai.chat")
	b.served(b.messagesFor(testModel), 11, "openai.chat")
	b.served(b.chatFor("gpt-4o"), 11, "openai.chat")
	// ... and on nothing else.
	b.notServed(func() result { return b.responsesFor("gpt-4o") }, 11, core.SchedModelProtocolClosed, "gpt-4o")
	// The rule naming a model decides for it.
	b.served(b.responsesFor("gpt-5"), 11, "openai.responses")
	b.notServed(func() result { return b.chatFor("gpt-5") }, 11, core.SchedModelProtocolClosed, "gpt-5")
	b.notServed(func() result { return b.messagesFor("gpt-5") }, 11, core.SchedModelProtocolClosed, "gpt-5")
}

// (g) count_tokens is not bound by itself: it is served for the models the
// account serves on the generation protocol of its family.
func TestProtocolBindingCountTokensFollowsGeneration(t *testing.T) {
	t.Run("estimated for a converting account", func(t *testing.T) {
		b := newBindingEnv(t)
		b.notServed(func() result { return b.countFor(testModel) }, 11, core.SchedConversionNotEnabled, testModel)

		b.accounts.bind(11, nil, protocolRule([]string{messagesProtocol}, testModel))
		r := b.countFor(testModel)
		if want := fmt.Sprintf(`{"input_tokens":%d}`, tokens(t, "user", "hello there")); string(r.body) != want {
			t.Fatalf("%d %s, want %s", r.status, r.body, want)
		}
		rec := b.record()
		if !rec.Success || *rec.AccountID != 11 || rec.UpstreamProtocol != upstreamLocal || b.sent() != 0 {
			t.Fatalf("record %+v, %d requests sent", rec, b.sent())
		}
		b.notServed(func() result { return b.countFor(otherModel) }, 11, core.SchedConversionNotEnabled, otherModel)
	})
	t.Run("forwarded by a native account", func(t *testing.T) {
		b := newBindingEnv(t)
		b.accounts.groups[testGroup] = []int64{1}
		// The binding lists the generation protocol alone.
		b.accounts.bind(1, []string{messagesProtocol})
		r := b.countFor(testModel)
		if string(r.body) != `{"input_tokens":42}` {
			t.Fatalf("%d %s", r.status, r.body)
		}
		b.served(r, 1, "anthropic.count_tokens")
		b.served(b.messagesFor(testModel), 1, messagesProtocol)

		// The model is served on another protocol only: neither generation
		// nor counting.
		b.accounts.bind(1, nil, protocolRule([]string{"openai.chat"}, testModel))
		b.notServed(func() result { return b.countFor(testModel) }, 1, core.SchedModelProtocolClosed, testModel)
		b.notServed(func() result { return b.messagesFor(testModel) }, 1, core.SchedModelProtocolClosed, testModel)

		// Every own protocol closed closes counting too.
		b.accounts.bind(1, []string{}, protocolRule([]string{"openai.chat"}, otherModel))
		b.notServed(func() result { return b.countFor(testModel) }, 1, core.SchedProtocolClosed, "anthropic.count_tokens")
	})
}

// (h) An account without a binding serves the protocols of its type's own
// platforms as before, adapted ones included (chat on a type serving the
// Responses endpoint only), and records no scheduling trace.
func TestProtocolBindingUnboundAccountsUnchanged(t *testing.T) {
	b := newBindingEnv(t)
	for _, tc := range []struct {
		protocol string
		do       func() result
	}{
		{"openai.chat", func() result { return b.chatFor("gpt-4o") }},
		{"openai.responses", func() result { return b.responsesFor("gpt-5") }},
		{"openai.embeddings", func() result {
			return b.post("/v1/embeddings", map[string]any{"model": "text-embedding-3-small", "input": "abc"}, bearer())
		}},
	} {
		rec := b.served(tc.do(), 11, tc.protocol)
		if rec.Protocol != tc.protocol || rec.Scheduling != nil || rec.Conversion != nil {
			t.Fatalf("%s: record %+v", tc.protocol, rec)
		}
	}

	e, plat, up := sameChatEnv(t, openAIStreamOnly())
	r := e.chat(false)
	if r.status != 200 || r.json().Get("choices.0.message.content").String() != "hello" {
		t.Fatalf("adapted chat: %d %s", r.status, r.body)
	}
	rec := e.record()
	if !rec.Success || *rec.AccountID != 10 || rec.Protocol != "openai.chat" || rec.UpstreamProtocol != "openai.responses" || rec.Scheduling != nil {
		t.Fatalf("adapted chat record %+v", rec)
	}
	// The adaptation is one of the type's own protocols: the binding closes
	// it like any other, and it is never a conversion to enable.
	e.accounts.bind(10, []string{"openai.responses"})
	builds, sent := plat.buildCount(), len(up.bodies)
	if r := e.chat(false); r.status != 404 || r.json().Get("error.message").String() != notServedMessage {
		t.Fatalf("closed chat: %d %s", r.status, r.body)
	}
	rec = e.record()
	if s := skipOf(rec.Scheduling, 10, core.SchedProtocolClosed); s == nil || s.Detail != "openai.chat" || rec.Attempts != 0 ||
		plat.buildCount() != builds || len(up.bodies) != sent {
		t.Fatalf("closed chat record %+v trace %+v", rec, rec.Scheduling)
	}
	if r := e.responses(false); r.status != 200 {
		t.Fatalf("responses: %d %s", r.status, r.body)
	}
	e.record()
}

// (i) A native account of the group serves the request: the converting
// account is neither needed nor, without the conversion enabled, in the way.
func TestProtocolBindingNativeAccountServes(t *testing.T) {
	b := newBindingEnv(t)
	b.accounts.groups[testGroup] = []int64{11, 1} // equal priorities
	rec := b.served(b.messagesFor(testModel), 1, messagesProtocol)
	if rec.Scheduling != nil || rec.Conversion != nil || b.plat.buildCount() != 0 {
		t.Fatalf("record %+v trace %+v", rec, rec.Scheduling)
	}

	// With the conversion enabled the native account still comes first, and
	// the converting one takes over when it fails.
	b.accounts.bind(11, nil, protocolRule([]string{messagesProtocol}, testModel))
	b.served(b.messagesFor(testModel), 1, messagesProtocol)
	if b.plat.buildCount() != 0 {
		t.Fatal("the request was built for the converting account")
	}
	b.up.set("acc-1", &upstreamRule{status: 500})
	r := b.messagesFor(testModel)
	if r.status != 200 {
		t.Fatalf("failover: %d %s", r.status, r.body)
	}
	if rec := b.record(); *rec.AccountID != 11 || rec.UpstreamProtocol != "openai.chat" || rec.Attempts != 2 {
		t.Fatalf("failover record %+v", rec)
	}
}

// Only a group none of whose accounts will serve the request answers 404:
// an account left out for another reason, busy or only out for a while keeps
// the answer of a passing shortage, which clients retry.
func TestProtocolBindingNotFoundOnlyWhenNoAccountServes(t *testing.T) {
	overloaded := func(b *bindingEnv, status int) *core.UsageRecord {
		t.Helper()
		sent := b.sent()
		if r := b.messagesFor(testModel); r.status != status {
			t.Fatalf("%d %s", r.status, r.body)
		}
		rec := b.record()
		if rec.ErrorType != errTypeNoAccount || rec.Attempts != 0 || b.sent() != sent ||
			skipOf(rec.Scheduling, 11, core.SchedConversionNotEnabled) == nil {
			t.Fatalf("record %+v trace %+v", rec, rec.Scheduling)
		}
		return rec
	}
	withNative := func() *bindingEnv {
		b := newBindingEnv(t)
		b.accounts.groups[testGroup] = []int64{11, 1}
		return b
	}

	t.Run("an account does not list the model", func(t *testing.T) {
		b := withNative()
		b.accounts.set(1, func(a *core.Account) { a.Models = []string{otherModel} })
		if rec := overloaded(b, 529); skipOf(rec.Scheduling, 1, core.SchedModelNotServed) == nil {
			t.Fatalf("trace %+v", rec.Scheduling)
		}
	})
	t.Run("an account is rate limited", func(t *testing.T) {
		b := withNative()
		lim := newFakeLimiter()
		b.gw.d.Limiter = lim
		b.accounts.set(1, func(a *core.Account) { a.RPMLimit = 1 })
		lim.exhausted[1] = true
		if rec := overloaded(b, 429); rec.Scheduling.Outcome != core.SchedulingBusy {
			t.Fatalf("trace %+v", rec.Scheduling)
		}
	})
	t.Run("an account that serves is cooling down", func(t *testing.T) {
		b := withNative()
		b.accounts.cooldown[1] = time.Now().Add(time.Minute)
		if rec := overloaded(b, 529); skipOf(rec.Scheduling, 1, core.SchedNotSchedulable) == nil {
			t.Fatalf("trace %+v", rec.Scheduling)
		}
		// Disabled for good, it will not serve: the request is not served.
		b.accounts.cooldown[1] = time.Time{}
		b.accounts.disabled[1] = "admin"
		rec := b.notServed(func() result { return b.messagesFor(testModel) }, 11, core.SchedConversionNotEnabled, testModel)
		if s := skipOf(rec.Scheduling, 1, core.SchedNotSchedulable); s == nil || s.Detail != "disabled" {
			t.Fatalf("trace %+v", rec.Scheduling)
		}
	})
	// A cooling account that is not converted for either will not serve the
	// request once back: the answer is the 404 it is without the cooldown.
	t.Run("an account closed by its binding is cooling down", func(t *testing.T) {
		b := newBindingEnv(t)
		b.accounts.addTyped(testGroup, 12, 98, "oai-12", "openai", "apikey")
		b.accounts.cooldown[12] = time.Now().Add(time.Minute)
		rec := b.notServed(func() result { return b.messagesFor(testModel) }, 11, core.SchedConversionNotEnabled, testModel)
		if s := skipOf(rec.Scheduling, 12, core.SchedNotSchedulable); s == nil || s.Detail != "cooldown" {
			t.Fatalf("trace %+v", rec.Scheduling)
		}
		// Converted for, it will serve once back: a passing shortage.
		b.accounts.serveOn(12, messagesProtocol)
		overloaded(b, 529)
	})
}

// A session bound to an account that stops serving its protocol moves to
// the next account, as it does when the bound account leaves the group.
func TestProtocolBindingStickySessionLeavesAClosedAccount(t *testing.T) {
	b := newBindingEnv(t)
	b.enableSticky(onFailureFailover)
	b.accounts.groups[testGroup] = []int64{11, 1}
	b.accounts.set(11, func(a *core.Account) { a.Priority = 100 })
	b.accounts.bind(11, nil, protocolRule([]string{messagesProtocol}, testModel))
	session := withSession(body(testModel, false), "bound-session")
	key := stickyKey(b.gw.rules.override[0], testGroup, testModel, "bound-session")

	b.served(b.messages(session), 11, "openai.chat")
	if v, _ := b.mr.Get(key); v != "11" {
		t.Fatalf("binding %q", v)
	}
	rec := b.served(b.messages(session), 11, "openai.chat")
	if !rec.StickyHit {
		t.Fatalf("second request not a sticky hit: %+v", rec)
	}

	// The operator takes the conversion back.
	b.accounts.bind(11, nil)
	builds := b.plat.buildCount()
	rec = b.served(b.messages(session), 1, messagesProtocol)
	if v, _ := b.mr.Get(key); v != "1" || rec.StickyHit || b.plat.buildCount() != builds {
		t.Fatalf("binding %q after the account closed, record %+v", v, rec)
	}
}

// The WebSocket endpoint is a protocol of the binding like the others, at
// the handshake and for the model of every later turn.
func TestProtocolBindingWebSocket(t *testing.T) {
	const ws, chatOnly = "openai.responses_ws", "gpt-5-chat"
	e := newWSEnv(t)
	e.pricer.rules[chatOnly] = &core.PriceRule{ID: 11, Model: chatOnly, Mode: "per_token"}
	for _, id := range []int64{11, 12} {
		e.accounts.bind(id, []string{"openai.chat", "openai.responses"})
	}
	c := e.mustDial()
	c.send(create(wsModel, "hi"))
	if ev := c.until("error"); ev[0].Get("status").Int() != 404 || ev[0].Get("error.message").String() != notServedMessage {
		t.Fatalf("closed endpoint: %v", ev)
	}
	rec := e.record()
	if s := skipOf(rec.Scheduling, 12, core.SchedProtocolClosed); s == nil || s.Detail != ws || rec.ErrorType != errTypeNoAccount ||
		rec.StatusCode != 404 || rec.Attempts != 0 || rec.AccountID != nil || rec.Scheduling.Counts[core.SchedProtocolClosed] != 2 {
		t.Fatalf("record %+v trace %+v", rec, rec.Scheduling)
	}
	if n := len(e.wsPlat.buildCalls()); n != 0 {
		t.Fatalf("%d handshakes built", n)
	}

	// The account that serves the endpoint takes the connection, whatever
	// its priority; one of its models is served on chat only.
	e.accounts.bind(12, []string{ws}, protocolRule([]string{"openai.chat"}, chatOnly))
	c = e.mustDial()
	c.send(create(wsModel, "hi"))
	c.until("response.completed")
	if rec := e.record(); !rec.Success || *rec.AccountID != 12 || rec.Protocol != ws {
		t.Fatalf("record %+v", rec)
	}
	turns := e.wsPlat.buildCalls()
	if n := len(turns); n == 0 || turns[n-1].GetAccount().GetId() != 12 {
		t.Fatalf("the served turn was not executed for account 12: %d calls", n)
	}
	c.send(create(chatOnly, "hi"))
	if ev := c.until("error"); ev[0].Get("status").Int() != 400 || !strings.Contains(ev[0].Get("error.message").String(), "does not serve model") {
		t.Fatalf("model closed on the endpoint: %v", ev)
	}
	if rec := e.record(); rec.Success || rec.ErrorType != errTypeInvalidRequest {
		t.Fatalf("record %+v", rec)
	}
	if _, msgs := e.wsUp.snapshot(); len(msgs) != 1 {
		t.Fatalf("the refused turn reached the upstream: %v", msgs)
	}
	if n := len(e.wsPlat.buildCalls()); n != len(turns) {
		t.Fatalf("the refused turn was handed to the plugin: %d calls, want %d", n, len(turns))
	}
	// A new connection for that model finds no account.
	c = e.mustDial()
	c.send(create(chatOnly, "hi"))
	if ev := c.until("error"); ev[0].Get("status").Int() != 404 {
		t.Fatalf("model closed on the endpoint, new connection: %v", ev)
	}
	if rec := e.record(); skipOf(rec.Scheduling, 12, core.SchedModelProtocolClosed) == nil || skipOf(rec.Scheduling, 11, core.SchedProtocolClosed) == nil {
		t.Fatalf("trace %+v", rec.Scheduling)
	}
}

// ---------------------------------------------------------------- hot path

// schedulingCandidates returns a planned anthropic.messages request for
// testModel and n accounts to judge for it, each bound by what rules
// returns: Anthropic accounts (their own platform) or, with converted,
// OpenAI ones.
func schedulingCandidates(t testing.TB, n int, converted bool, rules func() *core.ProtocolRules) (*call, []core.AccountRef) {
	t.Helper()
	c := plannedCall(t, "anthropic", messagesProtocol,
		typeOn("claude", manifest.AccountPlatform{Platform: "anthropic"}), typeOn("openai", manifest.AccountPlatform{Platform: "openai"}))
	c.model = testModel
	c.body, _ = json.Marshal(body(testModel, false))
	refs := make([]core.AccountRef, n)
	for i := range refs {
		refs[i] = core.AccountRef{ID: int64(i + 1), PluginKey: "t", Type: "claude", ProtocolRules: rules()}
		if converted {
			refs[i].Type = "openai"
		}
	}
	return c, refs
}

// fiftyRules is the largest binding an account holds in practice: its own
// protocols limited and 50 rules of 20 models each, the last of them
// serving model on protocols.
func fiftyRules(model string, protocols ...string) func() *core.ProtocolRules {
	return func() *core.ProtocolRules {
		rules := make([]core.ModelProtocolRule, 50)
		for i := range rules {
			models := make([]string, 20)
			for j := range models {
				models[j] = fmt.Sprintf("model-%d-%d", i, j)
			}
			rules[i] = protocolRule([]string{"openai.chat", "openai.responses"}, models...)
		}
		rules[len(rules)-1] = protocolRule(protocols, model)
		return core.NewProtocolRules([]string{messagesProtocol, "openai.chat", "openai.responses"}, rules)
	}
}

func noRules() *core.ProtocolRules { return nil }

// schedulingCases are the candidate sets of the hot path test and
// benchmark: 500 accounts, without a binding or with fiftyRules, that end up
// serving the request or left out.
var schedulingCases = []struct {
	name      string
	converted bool
	rules     func() *core.ProtocolRules
	served    bool
}{
	{"unbound", false, noRules, true},
	{"50_rules", false, fiftyRules(testModel, messagesProtocol), true},
	{"50_rules_closed", false, fiftyRules(testModel, "openai.chat"), false},
	{"unbound_not_converted", true, noRules, false},
	{"50_rules_converted", true, fiftyRules(testModel, messagesProtocol), true},
	{"50_rules_not_converted", true, fiftyRules("another-model", messagesProtocol), false},
}

// Judging a candidate against its binding allocates nothing, with or
// without rules, whether it ends up serving or left out (design §3.5). Nor
// does the rest of ineligibleReason once its route has a usage verdict for
// the body (the first round of AllocsPerRun leaves one): a candidate costs
// no allocation at all.
func TestIneligibleReasonBindingDoesNotAllocate(t *testing.T) {
	const n = 500
	// The least of a few rounds: what other goroutines of the test binary
	// allocate meanwhile is counted too.
	measure := func(refs []core.AccountRef, judge func(ref *core.AccountRef) bool) (served int, allocs float64) {
		allocs = math.Inf(1)
		for range 5 {
			allocs = min(allocs, testing.AllocsPerRun(10, func() {
				served = 0
				for i := range refs {
					if judge(&refs[i]) {
						served++
					}
				}
			}))
		}
		return served, allocs
	}
	eligible := func(c *call) func(*core.AccountRef) bool {
		return func(ref *core.AccountRef) bool { _, _, ok := c.ineligibleReason(ref); return ok }
	}
	for _, tc := range schedulingCases {
		c, refs := schedulingCandidates(t, n, tc.converted, tc.rules)
		want := 0
		if tc.served {
			want = n
		}
		served, allocs := measure(refs, func(ref *core.AccountRef) bool {
			_, _, ok := c.protocolClosedReason(ref, c.route(ref))
			return ok
		})
		if served != want || allocs != 0 {
			t.Errorf("%s: the binding serves %d of %d (want %d) in %.1f allocations", tc.name, served, n, want, allocs)
		}
		served, allocs = measure(refs, eligible(c))
		if served != want || allocs != 0 {
			t.Errorf("%s: %d of %d eligible (want %d) in %.1f allocations", tc.name, served, n, want, allocs)
		}
	}
}

// BenchmarkIneligibleReason judges 500 candidates per iteration (design
// §3.5): go test -run '^$' -bench IneligibleReason -benchmem ./internal/gateway/
func BenchmarkIneligibleReason(b *testing.B) {
	for _, bc := range schedulingCases {
		b.Run(bc.name, func(b *testing.B) {
			c, refs := schedulingCandidates(b, 500, bc.converted, bc.rules)
			b.ReportAllocs()
			for b.Loop() {
				for i := range refs {
					_, _, _ = c.ineligibleReason(&refs[i])
				}
			}
		})
	}
}
