package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

// check is one rule of DESIGN-2026-11-18-PLUGIN-FORWARDING-V2 §14.1: a
// subtest of its name, made of items (usually one per case).
type check struct {
	name, rule string
	items      func(*suite) []item
}

type item struct {
	name string
	fn   func(testing.TB)
}

const design = "DESIGN-2026-11-18-PLUGIN-FORWARDING-V2 "

// checks in the order they run. 08_no_sockets runs last: it judges the
// whole suite.
var checks = []check{
	{"01_one_relay", "every declared upstream protocol, in each form the core sends it (its endpoint's request.stream / request.streamPath), completes Execute with exactly one relay (" + design + "§14.1.1, §4.2, §4.7)", oneRelayItems},
	{"02_form_and_report", "an ordinary endpoint relays without waiting and returns host_usage; a plugin-usage endpoint waits and returns usage; a task submission waits and returns task; returning nothing is not allowed (" + design + "§14.1.2, §4.2, §4.3)", perCase(checkForm)},
	{"03_body", "the relayed body is valid JSON without duplicate keys, keeps the model reference (or the declared mapping) and a true stream flag (" + design + "§14.1.3, §4.5)", perCase(checkBody)},
	{"04_headers", "credential and signed headers are authoritative, any other authoritative header is declared with a reason, the rest are default headers; no transport header; the client headers the core hands over (the manifest's passHeaders) reach the upstream request (" + design + "§14.1.4, §4.5, §5.4)", headerItems},
	{"05_no_retry", "after a failed relay or fetch the plugin neither relays nor fetches again and returns an error or a classification (" + design + "§14.1.5, §4.7, §5.4)", perCase(checkNoRetry)},
	{"06_deterministic", "the same input with a fixed clock builds the same request (" + design + "§14.1.6, §4.6)", perCase(checkDeterministic)},
	{"07_failover", "an attempt > 0 is built from scratch for its account, never reused (" + design + "§14.1.7, §4.2)", perCase(checkFailover)},
	{"09_cancellation", "after its context is cancelled Execute returns promptly, within the suite's cancelWait (" + design + "§14.1.9, §8.4)", perCase(checkCancellation)},
	{"10_large_body", "a large body (largeBodySize) completes with plugin-side allocation within 4x the body (" + design + "§14.1.10, §8.3, §10.1)", perCase(checkLargeBody)},
	{"11_websocket", "a plugin declaring platform.websocket.v1 relays a turn once without waiting, with the handshake URL, handshake headers and the final message, and no response_stream (" + design + "§14.1.11, §4.8)", webSocketItems},
	{"12_balance", "an account type declaring manifest balance reads it (pluginsdk.BalanceReader) with requests to hosts the core sends to, and an answer it cannot read (a transport error, an empty body) is an error result; a type declaring none answers UNIMPLEMENTED (DESIGN-2026-11-18-UPSTREAM-PLUGINS-AND-BALANCE §3.1.7)", balanceItems},
	{"08_no_sockets", "the plugin opens no socket: every exchange goes through Relay, RelayAndWait or Fetch (" + design + "§14.1.8, §5.1, §5.4)", noSocketItems},
}

func (s *suite) check(t testing.TB, c check) {
	for _, it := range c.items(s) {
		s.judge(t, c, it)
	}
}

// perCase makes one item of fn per case.
func perCase(fn func(*suite, testing.TB, *Case)) func(*suite) []item {
	return func(s *suite) []item {
		var out []item
		for _, c := range s.cases {
			out = append(out, item{c.Name, func(t testing.TB) { fn(s, t, c) }})
		}
		return out
	}
}

// --- 1 ---

func oneRelayItems(s *suite) []item {
	out := perCase(func(s *suite, t testing.TB, c *Case) { s.once(t, c).Relayed(t) })(s)
	for _, protocol := range s.p.Protocols {
		for _, stream := range []bool{false, true} {
			if !s.declared.forms(protocol).has(stream) {
				continue
			}
			out = append(out, item{"coverage: " + protocol + formName(stream), func(t testing.TB) {
				if !slices.ContainsFunc(s.cases, func(c *Case) bool {
					return !c.WebSocket && c.Meta.GetProtocol() == protocol && c.Meta.GetStream() == stream
				}) {
					t.Errorf("no HTTP case covers it: add one")
				}
			}})
		}
	}
	for _, c := range s.cases {
		if msg := s.caseForm(c); msg != "" {
			out = append(out, item{"coverage: " + c.Name, func(t testing.TB) { t.Errorf("%s", msg) }})
		}
	}
	return out
}

func formName(stream bool) string {
	return map[bool]string{false: " non-stream", true: " stream"}[stream]
}

// caseForm says what is wrong with the form of c, "" for nothing: a turn
// of a protocol without a websocket endpoint, an HTTP case of a protocol
// not in Plugin.Protocols, or of a form the core never sends it in.
func (s *suite) caseForm(c *Case) string {
	protocol := c.Meta.GetProtocol()
	f := s.declared.forms(protocol)
	switch {
	case c.WebSocket && !f.websocket:
		return fmt.Sprintf("a WebSocket turn of protocol %q, which has no websocket endpoint", protocol)
	case c.WebSocket:
		return ""
	case !slices.Contains(s.p.Protocols, protocol):
		return fmt.Sprintf("protocol %q is not in Plugin.Protocols", protocol)
	case !f.has(c.Meta.GetStream()):
		return fmt.Sprintf("the core never sends protocol %q as a%s request (its endpoint's request.stream / request.streamPath): remove the case", protocol, formName(c.Meta.GetStream()))
	}
	return ""
}

// --- 2 ---

var forms = map[Endpoint]struct {
	wait   bool
	report string
}{Ordinary: {false, "host_usage"}, PluginUsage: {true, "usage"}, TaskSubmission: {true, "task"}}

func checkForm(s *suite, t testing.TB, c *Case) {
	ex := s.once(t, c)
	relay := ex.Relayed(t)
	want := forms[c.Endpoint]
	if relay.Wait != want.wait {
		t.Errorf("a %s endpoint relayed with wait=%v, want wait=%v", c.Endpoint, relay.Wait, want.wait)
	}
	if got := reportKind(ex); got != want.report {
		t.Errorf("a %s endpoint returned %s, want %s", c.Endpoint, got, want.report)
	}
}

// reportKind names what an execution returned.
func reportKind(ex *pluginsdktest.Execution) string {
	if ex.Err != nil {
		return "the error " + ex.Err.Error()
	}
	switch ex.Response.GetReport().(type) {
	case *pluginv1.ExecuteResponse_HostUsage:
		return "host_usage"
	case *pluginv1.ExecuteResponse_Usage:
		return "usage"
	case *pluginv1.ExecuteResponse_Task:
		return "task"
	}
	if ex.Response.GetClassification() != nil {
		return "classification"
	}
	return "nothing (unreported, not billed)"
}

// --- 3 ---

func checkBody(s *suite, t testing.TB, c *Case) {
	r := s.once(t, c).Relayed(t).Request
	body := r.GetBody()
	if !json.Valid(body) {
		t.Fatalf("the relayed body is not valid JSON: %s", clip(string(body)))
	}
	if path := duplicateKey(body); path != "" {
		t.Errorf("key %q repeats within one object of the relayed body; edit with pluginsdk/jsonedit instead of appending", path)
	}
	if want := expectedModel(c); want != "" {
		if msg := modelKept(r, want); msg != "" {
			t.Errorf("%s; declare a mapping in Case.UpstreamModel if it is intended", msg)
		}
	}
	if gjson.GetBytes(c.Body, "stream").Bool() && !gjson.GetBytes(body, "stream").Bool() {
		t.Errorf("the incoming body has stream: true, the relayed body does not")
	}
}

// --- 4 ---

func headerItems(s *suite) []item {
	seen := map[string]bool{} // authoritative names relayed on some case
	out := perCase(func(s *suite, t testing.TB, c *Case) { s.checkHeaders(t, c, seen) })(s)
	return append(out, item{"declared authoritative headers", func(t testing.TB) {
		for _, name := range s.declaredAuthoritative() {
			if !seen[name] {
				t.Errorf("header %q is declared authoritative but no case relays it as authoritative: remove it from Plugin.Credentials or Plugin.Authoritative, or add the case that sends it", name)
			}
		}
	}})
}

func (s *suite) declaredAuthoritative() []string {
	names := slices.Clone(s.p.Credentials)
	for name := range s.p.Authoritative {
		names = append(names, name)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

func (s *suite) checkHeaders(t testing.TB, c *Case, seen map[string]bool) {
	r := s.once(t, c).Relayed(t).Request
	declared := s.declaredAuthoritative()
	for name := range r.GetHeaders() {
		seen[name] = true
		if !slices.Contains(declared, name) {
			t.Errorf("authoritative header %q is not declared: make it a default header, or list it in Plugin.Authoritative with the reason", name)
		}
	}
	for _, name := range s.p.Credentials {
		if _, ok := r.GetDefaultHeaders()[name]; ok {
			t.Errorf("credential or signed header %q is a default header: an operator setting could replace it, make it authoritative", name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(c.inbound)) {
		_, isDefault := r.GetDefaultHeaders()[name]
		_, isAuthoritative := r.GetHeaders()[name]
		if !isDefault && !(isAuthoritative && slices.Contains(declared, name)) {
			t.Errorf("client header %q, which the manifest passes and the core hands over, does not reach the upstream request: forward the inbound headers as default headers (the core has filtered them already)", name)
		}
	}
	secrets := credentialValues(c.Account)
	for kind, headers := range map[string]map[string]string{"authoritative": r.GetHeaders(), "default": r.GetDefaultHeaders()} {
		for name, value := range headers {
			switch {
			case slices.Contains(transportHeaders, name):
				t.Errorf("%s header %q belongs to the host's transport", kind, name)
			case kind == "default" && slices.ContainsFunc(secrets, func(v string) bool { return strings.Contains(value, v) }):
				t.Errorf("default header %q carries a credential: make it authoritative", name)
			}
		}
	}
}

// --- 5 ---

func checkNoRetry(s *suite, t testing.TB, c *Case) {
	fetched := len(s.once(t, c).Fetches())
	type scenario struct {
		name     string
		u        upstream
		scripted int // relay errors the script itself causes
		maxFetch int
	}
	scenarios := []scenario{{"the relay call fails", upstream{relay: failRelay}, 1, fetched}}
	if c.Endpoint != Ordinary {
		scenarios = append(scenarios, scenario{"the upstream exchange fails", upstream{relay: failExchange}, 0, fetched})
	}
	if fetched > 0 {
		scenarios = append(scenarios, scenario{"a fetch fails", upstream{fetch: failFetch}, 0, 1})
	}
	for _, sc := range scenarios {
		ex := s.run(s.start(t, c, sc.u), c, nil)
		if extra := len(ex.RelayErrors()) - sc.scripted; extra > 0 {
			t.Errorf("%s: the plugin relayed %d more time(s)", sc.name, extra)
		}
		if n := len(ex.Fetches()); n > sc.maxFetch {
			t.Errorf("%s: the plugin fetched %d times, at most %d without retrying", sc.name, n, sc.maxFetch)
		}
		if ex.Response.GetReport() != nil || (ex.Err == nil && ex.Response.GetClassification() == nil) {
			t.Errorf("%s: the plugin returned %s, want an error or a classification", sc.name, reportKind(ex))
		}
	}
}

func failRelay(context.Context, *pluginv1.UpstreamRequest) (*pluginv1.RelayUpstreamResponse, error) {
	return nil, errUnavailable("conformance: the relay call failed")
}

func failExchange(context.Context, *pluginv1.UpstreamRequest) (*pluginv1.RelayUpstreamResponse, error) {
	return pluginsdktest.UpstreamFailed(503, []byte(`{"error":"conformance: upstream unavailable"}`), ""), nil
}

func failFetch(context.Context, *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
	return &pluginv1.ExecutionHTTPResponse{TransportError: "conformance: the fetch failed"}, nil
}

// --- 6 ---

// determinismRuns is how often rule 6 builds each case.
const determinismRuns = 5

func checkDeterministic(s *suite, t testing.TB, c *Case) {
	h := s.start(t, c, upstream{})
	first := s.run(h, c, nil).Relayed(t).Request
	for i := 2; i <= determinismRuns; i++ {
		if d := differ(first, s.run(h, c, nil).Relayed(t).Request); d != "" {
			t.Fatalf("build %d differs from build 1 in %s; take the time from a clock FixClock sets and never range over a map to build a value", i, d)
		}
	}
}

// --- 7 ---

func checkFailover(s *suite, t testing.TB, c *Case) {
	other, err := failoverAccount(c)
	if err != nil {
		t.Fatalf("derive the failover account: %v; set Case.Failover", err)
	}
	on := func(a *pluginv1.Account, requestID string, attempt int32) func(*pluginv1.ExecuteRequest) {
		return func(in *pluginv1.ExecuteRequest) {
			in.Account, in.Meta.RequestId, in.Attempt = a, requestID, attempt
			in.Meta.AccountId = a.GetId()
		}
	}
	h := s.start(t, c, upstream{})
	fresh := s.run(h, c, on(other, "conformance-failover-fresh", 0)).Relayed(t).Request
	first := s.run(h, c, on(c.Account, "conformance-failover", 0)).Relayed(t).Request
	if differ(fresh, first) == "" {
		t.Fatalf("the failover account builds the same request as the first one, so reuse cannot be told apart: set Case.Failover (or make Plugin.Fetch answer per account)")
	}
	again := s.run(h, c, on(other, "conformance-failover", 1)).Relayed(t).Request
	if d := differ(fresh, again); d != "" {
		t.Errorf("attempt 1 on the failover account is not what a fresh build for that account gives (%s); build every attempt from scratch, keep no per-request cache", d)
	}
}

// --- 9 ---

func checkCancellation(s *suite, t testing.TB, c *Case) {
	call := newBlockedCall()
	defer call.release()
	h := s.start(t, c, upstream{relay: call.relay, fetch: call.fetch, server: call.server()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.execute(ctx, h, c, nil)
	select {
	case <-call.entered:
	case <-call.returned:
		t.Fatalf("Execute returned without calling Relay, RelayAndWait or Fetch")
	case <-time.After(executeTimeout):
		t.Fatalf("Execute called no Relay, RelayAndWait or Fetch within %v", executeTimeout)
	}
	cancel()
	select {
	case <-call.returned:
	case <-time.After(cancelWait):
		t.Errorf("Execute was still running %v after its context was cancelled; pass the call's ctx to Relay, RelayAndWait and Fetch and wait on nothing else", cancelWait)
	}
}

// --- 10 ---

func checkLargeBody(s *suite, t testing.TB, c *Case) {
	if raceEnabled {
		t.Skip("skipped under -race: the race detector changes what is allocated, so the 4x bound is measured in a build without it")
	}
	body, err := pad(c.Body, largeBodySize)
	if err != nil {
		t.Fatalf("%v", err)
	}
	large := *c
	large.Body = body
	// The rule measures what the plugin allocates for the body against a
	// pass-through plugin; neither the fake host nor the core bounds the
	// relayed body.
	got, ex := allocated(s, s.start(t, &large, upstream{}), &large)
	ex.Relayed(t)
	base, _ := allocated(s, s.serve(t, reference{}, nil, []pluginsdk.Option{pluginsdk.WithInfo("conformance-reference", "0.0.0")}, nil, upstream{relay: observe(&large)}), &large)
	extra := got - base
	t.Logf("%.1f MiB body: %.1f MiB per Execute, %.1f MiB for a pass-through plugin", mib(len(body)), mib(int(got)), mib(int(base)))
	if extra > int64(4*len(body)) {
		t.Errorf("Execute allocated %.1f MiB beyond a pass-through plugin for a %.1f MiB body, more than 4x; edit with pluginsdk/jsonedit (one pass) instead of decoding the body or copying it per edit", mib(int(extra)), mib(len(body)))
	}
}

// --- 11 ---

func webSocketItems(s *suite) []item {
	var out []item
	for _, c := range s.cases {
		if c.WebSocket {
			out = append(out, item{c.Name, func(t testing.TB) { checkTurn(s, t, c) }})
		}
	}
	return append(out, item{"declaration", func(t testing.TB) {
		declared := slices.Contains(s.start(t, &Case{}, upstream{}).Info.GetCapabilities(), manifest.CapPlatformWebSocket)
		turns := slices.ContainsFunc(s.cases, func(c *Case) bool { return c.WebSocket })
		switch {
		case declared && !turns:
			t.Errorf("the plugin declares %s but no case is a WebSocket turn: add one", manifest.CapPlatformWebSocket)
		case !declared && turns:
			t.Errorf("a case is a WebSocket turn but the plugin does not declare %s", manifest.CapPlatformWebSocket)
		}
	}})
}

func checkTurn(s *suite, t testing.TB, c *Case) {
	ex := s.once(t, c)
	relay := ex.Relayed(t)
	r := relay.Request
	if relay.Wait || reportKind(ex) != "host_usage" {
		t.Errorf("a turn relays without waiting and returns host_usage; got wait=%v and %s", relay.Wait, reportKind(ex))
	}
	if !hasScheme(r.GetUrl(), "ws", "wss", "http", "https") {
		t.Errorf("the handshake URL %q is not an absolute ws(s) or http(s) URL", r.GetUrl())
	}
	if len(r.GetHeaders())+len(r.GetDefaultHeaders()) == 0 {
		t.Errorf("the turn carries no handshake header")
	}
	if !json.Valid(r.GetBody()) {
		t.Errorf("the final message is not valid JSON: %s", clip(string(r.GetBody())))
	}
	if r.GetResponseStream() != "" {
		t.Errorf("response_stream %q is set; a WebSocket turn has none", r.GetResponseStream())
	}
}

// --- 8 ---

func noSocketItems(s *suite) []item {
	return []item{{"suite", func(t testing.TB) {
		for _, c := range s.cases {
			s.once(t, c)
		}
		if dials := s.sockets.opened(); len(dials) > 0 {
			t.Errorf("the plugin dialled %v; use Relay, RelayAndWait or Fetch, never http.DefaultClient, an own transport or a vendor SDK client", dials)
		}
	}}}
}
