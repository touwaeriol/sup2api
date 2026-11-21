package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/tidwall/gjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

type clientIPAuth struct {
	ip string
}

func (a *clientIPAuth) Authenticate(ctx context.Context, _ string) (*core.APIKeyPrincipal, error) {
	a.ip = core.ClientIP(ctx)
	return nil, core.NewError(403, "ip_not_allowed", "client IP not allowed")
}

func TestGatewayAuthenticationReceivesTrustedClientIP(t *testing.T) {
	for _, path := range []string{"/v1/messages", "/v1/responses"} {
		t.Run(path, func(t *testing.T) {
			var e *env
			if path == "/v1/responses" {
				e = newWSEnv(t).env
			} else {
				e = newEnv(t)
			}
			auth := &clientIPAuth{}
			e.gw.d.Auth = auth
			engine := gin.New()
			require.NoError(t, engine.SetTrustedProxies([]string{"10.0.0.1"}))
			engine.Use(e.gw.Middleware())
			for _, test := range []struct{ peer, want string }{{"198.51.100.1:1234", "198.51.100.1"}, {"10.0.0.1:1234", "192.0.2.7"}} {
				method := "POST"
				if path == "/v1/responses" {
					method = "GET"
				}
				req := httptest.NewRequest(method, path, nil)
				req.RemoteAddr = test.peer
				req.Header.Set("Authorization", "Bearer "+testKey)
				req.Header.Set("X-Forwarded-For", "192.0.2.7")
				if path == "/v1/responses" {
					req.Header.Set("Upgrade", "websocket")
				}
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, req)
				require.Equal(t, test.want, auth.ip)
				require.Equal(t, 403, response.Code)
				require.Contains(t, response.Body.String(), "ip_not_allowed")
			}
		})
	}
}

func TestNonStreamSuccessAndUsage(t *testing.T) {
	e := newEnv(t)
	r := e.do("/v1/messages", body(testModel, false), map[string]string{"X-Request-Id": "client-chosen"})
	if r.status != 200 || r.json().Get("usage.output_tokens").Int() != upOutput {
		t.Fatalf("response: %d %s", r.status, r.body)
	}
	rid := r.header.Get("X-Request-Id")
	if rid == "" || rid == "client-chosen" {
		t.Fatalf("request id must be server generated, got %q", rid)
	}
	rec := e.record()
	if rec.RequestID != rid {
		t.Fatalf("record request id %q, header %q", rec.RequestID, rid)
	}
	if !rec.Success || rec.StatusCode != 200 || rec.ErrorType != "" || rec.Attempts != 1 {
		t.Fatalf("record status: %+v", rec)
	}
	if rec.AccountID == nil || *rec.AccountID != 1 || rec.Platform != "anthropic" || rec.PluginKey != "anthropic" ||
		rec.Protocol != "anthropic.messages" || rec.Endpoint != "/v1/messages" || rec.Model != testModel ||
		rec.UpstreamModel != upModel || rec.UserID != testUser || rec.APIKeyID != 5 || rec.GroupID != testGroup {
		t.Fatalf("record identity: %+v", rec)
	}
	want := core.UsageTokens{Input: upInput, Output: upOutput, CacheRead: upCacheRead,
		CacheCreation: upCacheCreation - upCacheCreate1h, CacheCreation1h: upCacheCreate1h}
	if rec.Tokens != want {
		t.Fatalf("tokens = %+v, want %+v", rec.Tokens, want)
	}
	if !rec.Billable || rec.Price == nil || rec.Price.ID != 9 || rec.UsageSemantics != "exclusive" ||
		rec.RateMultiplier.String() != "1.5" || rec.NodeID != "node-test" {
		t.Fatalf("record billing: %+v", rec)
	}
	// The upstream got the account key, never the client's key.
	call := e.up.last()
	if call.key != "acc-1" || call.header.Get("anthropic-version") != "2023-06-01" {
		t.Fatalf("upstream call: key=%s headers=%v", call.key, call.header)
	}
	for _, v := range call.header {
		for _, s := range v {
			if strings.Contains(s, testKey) {
				t.Fatal("client api key leaked upstream")
			}
		}
	}
	if e.accounts.touched[1] != 1 {
		t.Fatal("TouchLastUsed not called")
	}

	// The same client X-Request-Id again yields a different id.
	r2 := e.do("/v1/messages", body(testModel, false), map[string]string{"X-Request-Id": "client-chosen"})
	if rid2 := r2.header.Get("X-Request-Id"); rid2 == rid || e.record().RequestID != rid2 {
		t.Fatalf("second request reused id %q", rid2)
	}
}

func TestStreamFlushesEachEventAndExtractsUsage(t *testing.T) {
	e := newEnv(t)
	hold := make(chan struct{})
	e.up.set("acc-1", &upstreamRule{hold: hold})

	raw, _ := json.Marshal(body(testModel, true))
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/v1/messages", strings.NewReader(string(raw)))
	req.Header.Set("x-api-key", testKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream response %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	br := bufio.NewReader(resp.Body)
	// message_start must arrive while the upstream is still holding the rest.
	got := make(chan string, 1)
	go func() {
		var lines []string
		for len(lines) < 2 {
			l, err := br.ReadString('\n')
			if err != nil {
				break
			}
			lines = append(lines, strings.TrimSpace(l))
		}
		got <- strings.Join(lines, "|")
	}()
	select {
	case s := <-got:
		if !strings.HasPrefix(s, "event: message_start|data: ") {
			t.Fatalf("first event: %q", s)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first SSE event was not flushed before the upstream finished")
	}
	close(hold)
	var events []string
	for {
		l, err := br.ReadString('\n')
		if strings.HasPrefix(l, "event: ") {
			events = append(events, strings.TrimSpace(l[len("event: "):]))
		}
		if err != nil {
			break
		}
	}
	if strings.Join(events, ",") != "content_block_delta,message_delta,message_stop" {
		t.Fatalf("events after hold: %v", events)
	}
	rec := e.record()
	if !rec.Success || !rec.Stream || rec.FirstTokenMs <= 0 || rec.UpstreamModel != upModel {
		t.Fatalf("stream record: %+v", rec)
	}
	want := core.UsageTokens{Input: upInput, Output: upOutput, CacheRead: upCacheRead,
		CacheCreation: upCacheCreation - upCacheCreate1h, CacheCreation1h: upCacheCreate1h}
	if rec.Tokens != want {
		t.Fatalf("stream tokens %+v, want %+v", rec.Tokens, want)
	}
}

func TestFailoverOn429CoolsDown(t *testing.T) {
	e := newEnv(t)
	e.up.set("acc-1", &upstreamRule{status: 429})
	r := e.messages(body(testModel, false))
	if r.status != 200 {
		t.Fatalf("failover: %d %s", r.status, r.body)
	}
	if k := e.up.keys(); strings.Join(k, ",") != "acc-1,acc-2" {
		t.Fatalf("upstream order %v", k)
	}
	if len(e.accounts.cooldowns) != 1 || e.accounts.cooldowns[0] != 1 {
		t.Fatalf("cooldowns %v", e.accounts.cooldowns)
	}
	c := e.plat.classify[0]
	if c.GetStatus() != 429 || c.GetHeaders()["retry-after"] != "2" || !strings.Contains(string(c.GetBodyPrefix()), "mock status 429") {
		t.Fatalf("classify request: %+v", c)
	}
	rec := e.record()
	if rec.Attempts != 2 || *rec.AccountID != 2 || !rec.Success {
		t.Fatalf("record %+v", rec)
	}
	if e.plat.builds[1].GetAttempt() != 1 {
		t.Fatalf("attempt number %d", e.plat.builds[1].GetAttempt())
	}
	// The cooled account is skipped next time.
	e.messages(body(testModel, false))
	if k := e.up.keys(); k[len(k)-1] != "acc-2" || len(k) != 3 {
		t.Fatalf("after cooldown %v", k)
	}
}

func TestFailoverOn401DisablesAccount(t *testing.T) {
	e := newEnv(t)
	e.up.set("acc-1", &upstreamRule{status: 401})
	if r := e.messages(body(testModel, false)); r.status != 200 {
		t.Fatalf("status %d", r.status)
	}
	if e.accounts.disabled[1] == "" {
		t.Fatal("account 1 not disabled")
	}
}

func TestClientErrorReturnedWithoutFailover(t *testing.T) {
	e := newEnv(t)
	e.up.set("acc-1", &upstreamRule{status: 400})
	r := e.messages(body(testModel, false))
	if r.status != 400 || r.json().Get("type").String() != "error" ||
		r.json().Get("error.type").String() != "invalid_request_error" || r.json().Get("error.message").String() != "mock status 400" {
		t.Fatalf("400: %d %s", r.status, r.body)
	}
	if len(e.up.keys()) != 1 {
		t.Fatalf("failed over on 400: %v", e.up.keys())
	}
	rec := e.record()
	if rec.Success || rec.StatusCode != 400 || rec.ErrorType != errTypeUpstream || rec.Billable {
		t.Fatalf("record %+v", rec)
	}
}

func TestAllAttemptsFailReturnsLastUpstreamError(t *testing.T) {
	e := newEnv(t)
	e.setSettings(GatewaySettings{MaxAttempts: 2}, StickySettings{})
	for _, k := range []string{"acc-1", "acc-2", "acc-3"} {
		e.up.set(k, &upstreamRule{status: 529})
	}
	r := e.messages(body(testModel, false))
	if len(e.up.keys()) != 2 {
		t.Fatalf("max_attempts not honoured: %v", e.up.keys())
	}
	if r.status != 529 || r.json().Get("error.type").String() != "overloaded_error" {
		t.Fatalf("last upstream error: %d %s", r.status, r.body)
	}
	rec := e.record()
	if rec.Attempts != 2 || rec.ErrorType != errTypeUpstream || rec.Billable {
		t.Fatalf("record %+v", rec)
	}
}

// No available account reads to an Anthropic client as the API's own
// overload, which its clients retry with backoff; the record keeps the
// host's reason. Disabled and cooling accounts are a passing shortage.
func TestNoAccountIsOverloaded(t *testing.T) {
	e := newEnv(t)
	for _, id := range e.accounts.groups[testGroup] {
		e.accounts.cooldown[id] = time.Now().Add(time.Minute)
	}
	r := e.messages(body(testModel, false))
	j := r.json()
	if r.status != 529 || j.Get("type").String() != "error" || j.Get("error.type").String() != "overloaded_error" ||
		j.Get("error.message").String() != "Overloaded" || j.Get("error.code").String() != "no_available_account" {
		t.Fatalf("no account: %d %s", r.status, r.body)
	}
	if rec := e.record(); rec.ErrorType != errTypeNoAccount || rec.StatusCode != 529 || rec.ErrorMessage != "no available account" || rec.AccountID != nil {
		t.Fatalf("record %+v", rec)
	}
	for _, id := range e.accounts.groups[testGroup] {
		delete(e.accounts.cooldown, id)
		e.accounts.disabled[id] = "fixture"
	}
	if r := e.messages(body(testModel, false)); r.status != 529 {
		t.Fatalf("disabled accounts: %d %s", r.status, r.body)
	}
	e.record()
}

// A group with no account of a type serving the endpoint, in any status,
// cannot serve it: 404, which clients do not retry.
func TestEndpointNoAccountServesIsNotFound(t *testing.T) {
	e := newEnv(t)
	e.accounts.groups[testGroup] = nil
	r := e.messages(body(testModel, false))
	j := r.json()
	if r.status != 404 || j.Get("type").String() != "error" || j.Get("error.type").String() != "not_found_error" ||
		j.Get("error.message").String() != "no account in this group serves this endpoint" {
		t.Fatalf("no serving account: %d %s", r.status, r.body)
	}
	if rec := e.record(); rec.ErrorType != errTypeNoAccount || rec.StatusCode != 404 || rec.AccountID != nil {
		t.Fatalf("record %+v", rec)
	}
}

// Other formats keep 503: 529 is Anthropic's alone.
func TestNoAccountOtherFormats(t *testing.T) {
	for _, format := range []string{FormatOpenAI, FormatGemini, FormatPlain} {
		if got := clientError(format, fromCore(core.ErrNoAvailableAccount, errTypeNoAccount)); got.Status != 503 || got.Message != "no available account" {
			t.Fatalf("%s: %+v", format, got)
		}
	}
	upstream := &gwError{Status: 503, Code: core.ErrNoAvailableAccount.Code, Raw: []byte(`{"type":"error"}`)}
	if clientError(FormatAnthropic, upstream) != upstream {
		t.Fatal("a passed-through body was rewritten")
	}
}

// A plugin down for every account answers 529 no_available_account. (A
// plugin's own work is not timed: a hung one ends with the client.)
func TestPluginDownForEveryAccountIsNoAccount(t *testing.T) {
	e := newEnv(t)
	e.plat.buildHook = func(context.Context, *pluginv1.ExecuteRequest) error {
		return core.ErrPluginUnavailable
	}
	r := e.messages(body(testModel, false))
	if r.status != 529 || r.json().Get("error.code").String() != "no_available_account" {
		t.Fatalf("plugin down: %d %s", r.status, r.body)
	}
}

func TestNoFailoverOnceOutputStarted(t *testing.T) {
	e := newEnv(t)
	e.up.set("acc-1", &upstreamRule{breakStream: true})
	r := e.messages(body(testModel, true))
	if r.status != 200 || !strings.Contains(string(r.body), "message_start") {
		t.Fatalf("partial stream: %d %s", r.status, r.body)
	}
	if k := e.up.keys(); len(k) != 1 {
		t.Fatalf("failed over after output started: %v", k)
	}
	rec := e.record()
	if rec.Success || rec.ErrorType != errTypeUpstream || rec.Tokens.Input != upInput {
		t.Fatalf("interrupted stream record %+v", rec)
	}
	if !rec.Billable {
		t.Fatal("usage produced before the interruption must still be billed")
	}
}

func TestClientDisconnectCancelsUpstream(t *testing.T) {
	e := newEnv(t)
	e.up.set("acc-1", &upstreamRule{waitCancel: true})
	ctx, cancel := context.WithCancel(context.Background())
	raw, _ := json.Marshal(body(testModel, true))
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, e.srv.URL+"/v1/messages", strings.NewReader(string(raw)))
	req.Header.Set("x-api-key", testKey)
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}()
	// Wait until the upstream has the request, then drop the client.
	deadline := time.Now().Add(3 * time.Second)
	for len(e.up.keys()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	select {
	case k := <-e.up.canceled:
		if k != "acc-1" {
			t.Fatalf("canceled %s", k)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("upstream request not canceled after client disconnect")
	}
	rec := e.record()
	if rec.ErrorType != errTypeClientCanceled || len(e.up.keys()) != 1 {
		t.Fatalf("record %+v, calls %v", rec, e.up.keys())
	}
	// Slots are released.
	if n, _ := e.slots.InUse(context.Background(), core.KeySlots, 5); n != 0 {
		t.Fatalf("key slot leaked: %d", n)
	}
	if n, _ := e.slots.InUse(context.Background(), "account", 1); n != 0 {
		t.Fatalf("account slot leaked: %d", n)
	}
}

func TestAuthErrors(t *testing.T) {
	e := newEnv(t)
	r := e.do("/v1/messages", body(testModel, false), map[string]string{"x-api-key": "sk-bad"})
	if r.status != 401 || r.json().Get("type").String() != "error" || r.json().Get("error.type").String() != "authentication_error" {
		t.Fatalf("bad key: %d %s", r.status, r.body)
	}
	r = e.do("/v1/messages", body(testModel, false), map[string]string{"x-api-key": ""})
	if r.status != 401 {
		t.Fatalf("missing key: %d", r.status)
	}
	e.noRecord()
	// Authorization: Bearer works too.
	r = e.do("/v1/messages", body(testModel, false), map[string]string{"x-api-key": "", "Authorization": "Bearer " + testKey})
	if r.status != 200 {
		t.Fatalf("bearer: %d %s", r.status, r.body)
	}
	e.record()
}

func TestModelAllowlist(t *testing.T) {
	e := newEnv(t, func(e *env) {
		e.auth.keys[testKey].Group.ModelAllowlist = []string{"claude-haiku-*"}
	})
	r := e.messages(body(testModel, false))
	if r.status != 404 || r.json().Get("error.code").String() != "model_not_found" || r.json().Get("error.type").String() != "not_found_error" {
		t.Fatalf("allowlist: %d %s", r.status, r.body)
	}
	if len(e.up.keys()) != 0 {
		t.Fatal("upstream called")
	}
	if rec := e.record(); rec.ErrorType != errTypeModelNotAllowed || rec.Billable {
		t.Fatalf("record %+v", rec)
	}
}

func TestInsufficientBalance402(t *testing.T) {
	e := newEnv(t)
	e.balance.broke[testUser] = true
	for _, stream := range []bool{false, true} {
		r := e.messages(body(testModel, stream))
		if r.status != 402 || r.json().Get("type").String() != "error" || r.json().Get("error.code").String() != "insufficient_balance" {
			t.Fatalf("402: %d %s", r.status, r.body)
		}
		if rec := e.record(); rec.ErrorType != errTypeInsufficientBalance || rec.StatusCode != 402 || rec.Billable {
			t.Fatalf("record %+v", rec)
		}
	}
	if len(e.up.keys()) != 0 {
		t.Fatal("upstream called despite insufficient balance")
	}
}

func TestPriceNotConfigured403(t *testing.T) {
	e := newEnv(t)
	r := e.messages(body("claude-unknown-1", false))
	if r.status != 403 || r.json().Get("error.code").String() != "model_price_not_configured" {
		t.Fatalf("price: %d %s", r.status, r.body)
	}
	if rec := e.record(); rec.ErrorType != errTypePriceNotConfigured {
		t.Fatalf("record %+v", rec)
	}
	if len(e.up.keys()) != 0 {
		t.Fatal("upstream called for a model without a price")
	}
}

// A price of 0 is a price: the request is served and billed at it.
func TestZeroPriceServed(t *testing.T) {
	e := newEnv(t)
	zero := &core.PriceRule{ID: 31, Model: "claude-zero-1", Mode: "visual", Expression: `tier("base", p*0)`}
	e.pricer.rules["claude-zero-1"] = zero
	if r := e.messages(body("claude-zero-1", false)); r.status != 200 {
		t.Fatalf("zero price: %d %s", r.status, r.body)
	}
	if rec := e.record(); !rec.Billable || rec.Price != zero {
		t.Fatalf("zero price record %+v", rec)
	}
	if len(e.balance.precharged) != 1 || e.balance.precharged[0] != zero {
		t.Fatalf("zero price precharge %v", e.balance.precharged)
	}
}

func TestFreeEndpointSkipsPricing(t *testing.T) {
	e := newEnv(t)
	e.balance.broke[testUser] = true // not checked for billing=free
	r := e.do("/v1/messages/count_tokens", body("claude-unknown-1", false), nil)
	if r.status != 200 || r.json().Get("input_tokens").Int() != 42 {
		t.Fatalf("count_tokens: %d %s", r.status, r.body)
	}
	if e.pricer.calls != 0 {
		t.Fatal("pricer consulted for a free endpoint")
	}
	if rec := e.record(); rec.Billable || rec.Protocol != "anthropic.count_tokens" || !rec.Success {
		t.Fatalf("record %+v", rec)
	}
}

func TestPriceInputsCaptured(t *testing.T) {
	e := newEnv(t)
	e.pricer.params = []string{"service_tier"}
	e.pricer.headers = []string{"Anthropic-Beta"}
	b := body(testModel, false, "service_tier", "priority")
	e.do("/v1/messages", b, map[string]string{"anthropic-beta": "fast-mode"})
	rec := e.record()
	if rec.PriceParams["service_tier"] != `"priority"` || rec.PriceHeaders["anthropic-beta"] != "fast-mode" {
		t.Fatalf("captured params=%v headers=%v", rec.PriceParams, rec.PriceHeaders)
	}
}

func TestAccountSlotsBusy(t *testing.T) {
	e := newEnv(t)
	for _, id := range []int64{1, 2, 3} {
		e.slots.limits["account:"+itoa(id)] = 1
		e.slots.inUse["account:"+itoa(id)] = 1
	}
	r := e.messages(body(testModel, false))
	if r.status != 429 {
		t.Fatalf("busy accounts: %d %s", r.status, r.body)
	}
	e.record()
	// One free slot on the lowest priority account is used.
	e.slots.inUse["account:3"] = 0
	if r := e.messages(body(testModel, false)); r.status != 200 || e.up.last().key != "acc-3" {
		t.Fatalf("free slot: %d %v", r.status, e.up.keys())
	}
}

func TestInvalidBodyAndLimits(t *testing.T) {
	e := newEnv(t)
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/v1/messages", strings.NewReader("{not json"))
	req.Header.Set("x-api-key", testKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("invalid json: %d", resp.StatusCode)
	}
	e.record()
	r := e.messages(map[string]any{"messages": []any{}})
	if r.status != 400 {
		t.Fatalf("missing model: %d", r.status)
	}
	e.record()
	e.setSettings(GatewaySettings{MaxRequestBodyMB: 1}, defaultStickySettings())
	r = e.messages(body(testModel, false, "pad", strings.Repeat("x", 1<<20)))
	if r.status != 413 {
		t.Fatalf("too large: %d %s", r.status, r.body)
	}
	e.record()
}

func TestSSRFGuard(t *testing.T) {
	e := newEnv(t)
	e.gw.allowPrivate = false
	// httptest listens on 127.0.0.1: rejected, every account, then 529.
	r := e.messages(body(testModel, false))
	if r.status != 529 || len(e.up.keys()) != 0 {
		t.Fatalf("private upstream allowed: %d %v", r.status, e.up.keys())
	}
	e.record()
}

func TestRouteFallthroughAndGenerationSwitch(t *testing.T) {
	e := newEnv(t)
	resp, err := http.Post(e.srv.URL+"/v1/other", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("unknown path: %d", resp.StatusCode)
	}
	// Disable the plugin: its endpoint no longer exists, so 404.
	e.reg.set(&fakeGen{num: 2})
	if r := e.messages(body(testModel, false)); r.status != 404 {
		t.Fatalf("disabled plugin endpoint: %d %s", r.status, r.body)
	}
	e.noRecord()
	// Re-enable.
	e.reg.set(e.gen)
	if r := e.messages(body(testModel, false)); r.status != 200 {
		t.Fatalf("after enable: %d", r.status)
	}
	e.record()
}

// The upstream receives the plugin's bytes; the plugin is handed the whole
// body as the client sent it (no mapping here) and the allowed headers.
func TestUpstreamGetsPluginBodyAndPluginGetsRequest(t *testing.T) {
	e := newEnv(t)
	e.plat.patches = []*pluginv1.BodyPatch{{Op: pluginv1.BodyPatch_OP_SET, Path: "model", ValueJson: `"claude-mapped"`}}
	e.messages(body(testModel, false))
	e.record()
	if m := gjson.GetBytes(e.up.last().body, "model").String(); m != "claude-mapped" {
		t.Fatalf("patched model = %s", m)
	}
	b := e.plat.builds[0]
	if gjson.GetBytes(b.GetBody(), "model").String() != testModel || b.GetInboundHeaders()["anthropic-version"] != "2023-06-01" ||
		b.GetMeta().GetRequestId() == "" || b.GetAccount().GetCredentialsJson() == "" {
		t.Fatalf("build request: %+v", b)
	}
	if _, ok := b.GetInboundHeaders()["x-api-key"]; ok {
		t.Fatal("non-allowlisted header forwarded to the plugin")
	}
}

func TestModelBlacklist(t *testing.T) {
	e := newEnv(t, func(e *env) {
		e.auth.keys[testKey].Group.ModelFilterMode = "blacklist"
		e.auth.keys[testKey].Group.ModelAllowlist = []string{"claude-sonnet-*"}
	})
	r := e.messages(body(testModel, false))
	if r.status != 404 || r.json().Get("error.code").String() != "model_not_found" {
		t.Fatalf("blacklist: %d %s", r.status, r.body)
	}
	if len(e.up.keys()) != 0 {
		t.Fatal("blocked request reached upstream")
	}
	if rec := e.record(); rec.Billable || rec.ErrorType != errTypeModelNotAllowed {
		t.Fatalf("record: %+v", rec)
	}
	e.auth.keys[testKey].Group.ModelAllowlist = []string{"claude-opus-*"}
	r = e.messages(body(testModel, false))
	if r.status != 200 {
		t.Fatalf("unmatched model: %d %s", r.status, r.body)
	}
}

// A non-streaming JSON answer larger than the 32 MiB the usage buffer once
// held, with its usage block at the end: the client gets the whole answer
// and the request is billed from that usage block.
func TestLargeJSONAnswerBilledFromItsUsage(t *testing.T) {
	if testing.Short() {
		t.Skip("relays a 33 MiB answer")
	}
	e := newEnv(t)
	usage, err := json.Marshal(usageJSON(upOutput))
	require.NoError(t, err)
	answer := `{"id":"msg_1","type":"message","role":"assistant","model":"` + upModel +
		`","content":[{"type":"text","text":"` + strings.Repeat("x", 33<<20) + `"}],"usage":` + string(usage) + `}`
	e.up.set("acc-1", &upstreamRule{status: 200, body: answer})
	r := e.messages(body(testModel, false))
	require.Equal(t, 200, r.status)
	require.Equal(t, len(answer), len(r.body), "the whole answer reaches the client")
	rec := e.record()
	require.True(t, rec.Success)
	require.EqualValues(t, upInput, rec.Tokens.Input)
	require.EqualValues(t, upOutput, rec.Tokens.Output)
	require.True(t, rec.Billable)
}
