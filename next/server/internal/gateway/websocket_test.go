package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/Sub2API-Devs/sup2api/next/sdk/awseventstream"
	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/account"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

const (
	wsModel     = "gpt-5-ws"
	wsInput     = 31
	wsOutput    = 9
	wsCacheRead = 4
)

// wsUpstream is a Responses WebSocket server: every response.create is
// answered with response.created, one delta and response.completed. The
// "input" of a message selects a misbehaviour.
type wsUpstream struct {
	srv *httptest.Server

	mu       sync.Mutex
	reject   map[string]int // api key -> handshake status
	keys     []string       // api key of every accepted handshake
	headers  []http.Header
	messages []string
	hold     chan struct{} // "hold": wait before completing
	open     atomic.Int64
}

func newWSUpstream(t *testing.T) *wsUpstream {
	u := &wsUpstream{reject: map[string]int{}}
	u.srv = httptest.NewServer(http.HandlerFunc(u.handle))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *wsUpstream) handle(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	u.mu.Lock()
	status := u.reject[key]
	u.mu.Unlock()
	if status != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"error":{"type":"invalid_request_error","message":"rejected %d"}}`, status)
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(1 << 20)
	u.mu.Lock()
	u.keys = append(u.keys, key)
	u.headers = append(u.headers, r.Header.Clone())
	u.mu.Unlock()
	u.open.Add(1)
	defer u.open.Add(-1)
	defer conn.CloseNow()
	ctx := context.Background()
	send := func(v map[string]any) {
		b, _ := json.Marshal(v)
		_ = conn.Write(ctx, websocket.MessageText, b)
	}
	for n := 1; ; n++ {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		u.mu.Lock()
		u.messages = append(u.messages, string(data))
		hold := u.hold
		u.mu.Unlock()
		model := gjson.GetBytes(data, "model").String()
		id := fmt.Sprintf("resp_%d", n)
		switch gjson.GetBytes(data, "input").String() {
		case "drop":
			return
		case "rate-limit":
			send(map[string]any{"type": "error", "status": 429, "error": map[string]any{"type": "rate_limit_error", "message": "slow down"}})
			continue
		case "usage-limit":
			send(map[string]any{"type": "error", "status": 429, "error": map[string]any{"type": "usage_limit_reached", "message": "limit"},
				"headers": map[string]any{"X-Codex-Primary-Used-Percent": "100.0", "x-codex-primary-window-minutes": 300, "x-flag": true}})
			continue
		case "image-fail": // a failed turn that still reports the turn's and the image tool's usage
			send(map[string]any{"type": "response.failed", "response": map[string]any{"id": id, "model": model, "status": "failed",
				"error":      map[string]any{"code": "server_error", "message": "boom"},
				"usage":      map[string]any{"input_tokens": wsInput, "output_tokens": wsOutput, "input_tokens_details": map[string]any{"cached_tokens": wsCacheRead}},
				"tool_usage": map[string]any{"image_gen": map[string]any{"input_tokens": 39, "output_tokens": 515, "output_tokens_details": map[string]any{"image_tokens": 515}}}}})
			continue
		case "fail":
			send(map[string]any{"type": "response.failed", "response": map[string]any{"id": id, "model": model, "status": "failed",
				"error": map[string]any{"code": "server_error", "message": "boom"}}})
			continue
		case "hold":
			send(map[string]any{"type": "response.created", "response": map[string]any{"id": id, "model": model}})
			<-hold
		default:
			send(map[string]any{"type": "response.created", "response": map[string]any{"id": id, "model": model}})
		}
		send(map[string]any{"type": "response.output_text.delta", "delta": "hi"})
		completed := map[string]any{"id": id, "model": model, "status": "completed",
			"usage": map[string]any{"input_tokens": wsInput, "output_tokens": wsOutput, "input_tokens_details": map[string]any{"cached_tokens": wsCacheRead}}}
		if gjson.GetBytes(data, "input").String() == "image" { // the image tool's usage beside the turn's
			completed["tools"] = []any{map[string]any{"type": "image_generation", "model": "gpt-image-2-codex"}}
			completed["tool_usage"] = map[string]any{"image_gen": map[string]any{"input_tokens": 39, "output_tokens": 515,
				"output_tokens_details": map[string]any{"image_tokens": 515}}}
		}
		send(map[string]any{"type": "response.completed", "response": completed})
	}
}

func (u *wsUpstream) snapshot() (keys, messages []string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.keys...), append([]string(nil), u.messages...)
}

// wsPlatform is the openai plugin's account type as far as the gateway
// cares: every turn is one Execute whose relay is a GET to a ws:// URL with
// the account's key, carrying the turn's message as it was handed.
type wsPlatform struct {
	*fakePlatform
	up *wsUpstream
	// amend edits the relay of the n-th Execute (from 1) or fails it; nil
	// leaves it as built.
	amend func(n int, in *pluginv1.ExecuteRequest, out *pluginv1.UpstreamRequest) error
	// laterURL, when set, is the URL every call after the first answers.
	laterURL string
}

func (p *wsPlatform) Execute(ctx context.Context, in *pluginv1.ExecuteRequest, client core.ExecutionClient) (core.ExecutionResult, error) {
	return fakeExecute(ctx, p, in, client)
}

func (p *wsPlatform) buildUpstream(_ context.Context, in *pluginv1.ExecuteRequest) (*pluginv1.UpstreamRequest, error) {
	p.mu.Lock()
	p.builds = append(p.builds, in)
	n, amend := len(p.builds), p.amend
	p.mu.Unlock()
	key := gjson.Get(in.GetAccount().GetCredentialsJson(), "api_key").String()
	out := &pluginv1.UpstreamRequest{Method: "GET", Url: "ws" + strings.TrimPrefix(p.up.srv.URL, "http") + "/v1/responses",
		Headers: map[string]string{"authorization": "Bearer " + key, "openai-beta": "responses_websockets=2026-02-06"},
		Body:    in.GetBody()}
	if n > 1 && p.laterURL != "" {
		out.Url = p.laterURL
	}
	if amend != nil {
		if err := amend(n, in, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

type wsEnv struct {
	*env
	wsUp     *wsUpstream
	wsPlat   *wsPlatform
	draining atomic.Bool
}

// newWSEnv adds an "openai" plugin whose apikey type serves the built-in
// openai platform and declares platform.websocket.v1, two of its accounts
// (11 first, then 12) and a price for wsModel.
func newWSEnv(t *testing.T, caps ...string) *wsEnv {
	if caps == nil {
		caps = []string{manifest.CapPlatformAdapter, manifest.CapPlatformWebSocket}
	}
	e := &wsEnv{env: newEnv(t)}
	e.wsUp = newWSUpstream(t)
	m := &manifest.Manifest{Key: "openai", Version: "0.3.0", ExternalServices: []string{testUpstreamHost}, AccountTypes: []manifest.AccountType{{ID: "apikey",
		Platforms: []manifest.AccountPlatform{{Platform: manifest.PlatformOpenAI}}}}}
	for _, c := range caps {
		m.Capabilities = append(m.Capabilities, manifest.Capability{ID: c})
	}
	info := core.PluginInfo{Key: "openai", Version: "0.3.0", Manifest: m}
	e.wsPlat = &wsPlatform{fakePlatform: &fakePlatform{}, up: e.wsUp}
	e.gen.plugins = append(e.gen.plugins, info)
	e.gen.accountTypes = append(e.gen.accountTypes, core.AccountTypeBinding{Plugin: info, Type: m.AccountTypes[0], Client: e.wsPlat})
	e.accounts.addTyped(testGroup, 11, 99, "oa-11", "openai", "apikey")
	e.accounts.addTyped(testGroup, 12, 98, "oa-12", "openai", "apikey")
	e.pricer.rules[wsModel] = &core.PriceRule{ID: 10, Model: wsModel, Mode: "per_token", Expression: "p*3", ExprHash: "w"}
	e.gw.d.Draining = e.draining.Load
	e.gw.ws.drainPoll = 20 * time.Millisecond
	return e
}

type wsClient struct {
	t    *testing.T
	conn *websocket.Conn
}

func (e *wsEnv) dial(header http.Header) (*wsClient, *http.Response, error) {
	if header == nil {
		header = http.Header{"Authorization": {"Bearer " + testKey}}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.srv.URL, "http")+"/v1/responses", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		return nil, resp, err
	}
	e.t.Cleanup(func() { conn.CloseNow() })
	return &wsClient{t: e.t, conn: conn}, resp, nil
}

func (e *wsEnv) mustDial() *wsClient {
	e.t.Helper()
	c, _, err := e.dial(nil)
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

func (c *wsClient) send(v map[string]any) {
	c.t.Helper()
	b, _ := json.Marshal(v)
	if err := c.conn.Write(context.Background(), websocket.MessageText, b); err != nil {
		c.t.Fatal(err)
	}
}

func create(model, input string) map[string]any {
	return map[string]any{"type": "response.create", "model": model, "input": input, "store": false}
}

// next reads one event.
func (c *wsClient) next() (gjson.Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := c.conn.Read(ctx)
	if err != nil {
		return gjson.Result{}, err
	}
	return gjson.ParseBytes(data), nil
}

// until reads events up to and including one of type typ.
func (c *wsClient) until(typ string) []gjson.Result {
	c.t.Helper()
	var out []gjson.Result
	for {
		ev, err := c.next()
		if err != nil {
			c.t.Fatalf("waiting for %s after %d events: %v", typ, len(out), err)
		}
		out = append(out, ev)
		if ev.Get("type").String() == typ {
			return out
		}
	}
}

// closed waits for the server to close the connection and returns the code.
func (c *wsClient) closed() websocket.StatusCode {
	c.t.Helper()
	for {
		if _, err := c.next(); err != nil {
			code := websocket.CloseStatus(err)
			if code == -1 {
				c.t.Fatalf("connection ended without a close frame: %v", err)
			}
			return code
		}
	}
}

func TestWebSocketTurnsShareOneUpstreamAndAreBilledEach(t *testing.T) {
	e := newWSEnv(t)
	c := e.mustDial()
	var ids []string
	for i := 0; i < 2; i++ {
		c.send(create(wsModel, fmt.Sprint("turn ", i)))
		evs := c.until("response.completed")
		if len(evs) != 3 || evs[0].Get("type").String() != "response.created" {
			t.Fatalf("turn %d events: %v", i, evs)
		}
		r := e.record()
		ids = append(ids, r.RequestID)
		if !r.Success || r.StatusCode != 200 || r.Protocol != "openai.responses_ws" || !r.Stream || r.Model != wsModel ||
			r.AccountID == nil || *r.AccountID != 11 || r.PluginKey != "openai" || r.UsageSemantics != "inclusive" {
			t.Fatalf("turn %d record: %+v", i, r)
		}
		if r.Tokens.Input != wsInput || r.Tokens.Output != wsOutput || r.Tokens.CacheRead != wsCacheRead || r.Price == nil || !r.Billable {
			t.Fatalf("turn %d usage: %+v billable %v", i, r.Tokens, r.Billable)
		}
	}
	if ids[0] == ids[1] {
		t.Fatal("turns share a request id, the ledger would bill them once")
	}
	keys, msgs := e.wsUp.snapshot()
	if len(keys) != 1 || keys[0] != "oa-11" || len(msgs) != 2 {
		t.Fatalf("one upstream connection for both turns: keys %v messages %d", keys, len(msgs))
	}
	if h := e.wsUp.headers[0]; h.Get("Openai-Beta") != "responses_websockets=2026-02-06" || h.Get("Authorization") != "Bearer oa-11" {
		t.Fatalf("handshake headers %v", h)
	}
	if gjson.Get(msgs[0], "type").String() != "response.create" || gjson.Get(msgs[0], "store").Bool() {
		t.Fatalf("message not relayed as sent: %s", msgs[0])
	}
	// One Execute per turn: the first opened the connection.
	b := e.wsPlat.buildCalls()
	if len(b) != 2 {
		t.Fatalf("plugin asked %d times: %v", len(b), b)
	}
	for _, in := range b {
		if in.GetMeta().GetProtocol() != "openai.responses_ws" || !in.GetMeta().GetStream() {
			t.Fatalf("plugin call: %v", in)
		}
	}
	// Every slot is free between turns.
	if n, _ := e.slots.InUse(context.Background(), "account", 11); n != 0 {
		t.Fatalf("account slot held between turns: %d", n)
	}
	if n, _ := e.slots.InUse(context.Background(), "ws", 5); n != 1 {
		t.Fatalf("connection slot: %d", n)
	}
	c.conn.Close(websocket.StatusNormalClosure, "")
	waitFor(t, func() bool {
		n, _ := e.slots.InUse(context.Background(), "ws", 5)
		return n == 0 && e.wsUp.open.Load() == 0
	})
}

func TestWebSocketHandshakeFailoverDisablesTheAccount(t *testing.T) {
	e := newWSEnv(t)
	e.wsUp.reject["oa-11"] = http.StatusUnauthorized
	c := e.mustDial()
	c.send(create(wsModel, "hello"))
	c.until("response.completed")
	r := e.record()
	if *r.AccountID != 12 || r.Attempts != 2 || !r.Success {
		t.Fatalf("record after failover: %+v", r)
	}
	if _, disabled := e.accounts.disabled[11]; !disabled {
		t.Fatal("the account whose key was rejected is still enabled")
	}
	// The handshakes are the turn's attempts; the one that opened ends as the turn did.
	if got := trailShape(r.AttemptTrail); !reflect.DeepEqual(got, []trailStep{{11, 401, errTypeUpstream, core.AttemptSwitch}, {12, 200, "", ""}}) {
		t.Fatalf("attempt trail %+v", got)
	}
	// Every account rejected: the turn fails, the connection stays.
	e.wsUp.reject["oa-12"] = http.StatusUnauthorized
	c2 := e.mustDial()
	c2.send(create(wsModel, "hello"))
	if ev := c2.until("error"); ev[0].Get("status").Int() != 401 || ev[0].Get("error.type").String() != "authentication_error" {
		t.Fatalf("every key rejected: %v", ev)
	}
	if r := e.record(); r.Success || r.ErrorType != errTypeUpstream || r.Attempts != 1 || r.AttemptTrail != nil {
		t.Fatalf("failed turn record: %+v", r)
	}
}

func TestWebSocketTurnErrorsKeepTheConnection(t *testing.T) {
	e := newWSEnv(t)
	e.auth.keys[testKey].Group.ModelAllowlist = []string{"gpt-*"}
	c := e.mustDial()
	for _, tc := range []struct {
		msg    map[string]any
		status int64
		record string
	}{
		{map[string]any{"type": "response.append", "model": wsModel}, 400, ""},
		{create("claude-x", "hi"), 404, errTypeModelNotAllowed},
		{create("gpt-unpriced", "hi"), 403, errTypePriceNotConfigured},
	} {
		c.send(tc.msg)
		ev := c.until("error")
		if ev[0].Get("status").Int() != tc.status {
			t.Fatalf("%v: %v", tc.msg, ev)
		}
		if tc.record != "" {
			if r := e.record(); r.ErrorType != tc.record || r.Success {
				t.Fatalf("%v record: %+v", tc.msg, r)
			}
		}
	}
	e.balance.broke[testUser] = true
	c.send(create(wsModel, "hi"))
	if ev := c.until("error"); ev[0].Get("status").Int() != 402 {
		t.Fatalf("broke user: %v", ev)
	}
	e.record()
	e.balance.broke[testUser] = false
	// One response at a time per connection.
	hold := make(chan struct{})
	e.wsUp.hold = hold
	c.send(create(wsModel, "hold"))
	c.until("response.created")
	c.send(create(wsModel, "second"))
	if ev := c.until("error"); ev[0].Get("error.code").String() != "response_in_progress" {
		t.Fatalf("second response.create during a turn: %v", ev)
	}
	close(hold)
	c.until("response.completed")
	if r := e.record(); !r.Success {
		t.Fatalf("held turn: %+v", r)
	}
	if _, msgs := e.wsUp.snapshot(); len(msgs) != 1 {
		t.Fatalf("rejected turns reached the upstream: %v", msgs)
	}
}

func TestWebSocketUpstreamEventsDecideTheTurn(t *testing.T) {
	e := newWSEnv(t)
	c := e.mustDial()
	c.send(create(wsModel, "rate-limit"))
	if ev := c.until("error"); ev[0].Get("error.type").String() != "rate_limit_error" {
		t.Fatalf("upstream error event not relayed: %v", ev)
	}
	r := e.record()
	if r.Success || r.StatusCode != 429 || r.ErrorType != errTypeUpstream {
		t.Fatalf("rate limited turn: %+v", r)
	}
	if !time.Now().Before(e.accounts.cooldown[11]) {
		t.Fatal("rate limited account not cooled down")
	}
	c.send(create(wsModel, "fail"))
	c.until("response.failed")
	if r := e.record(); r.Success || !strings.Contains(r.ErrorMessage, "boom") {
		t.Fatalf("failed response: %+v", r)
	}
	// The session goes on on the same connection.
	c.send(create(wsModel, "ok"))
	c.until("response.completed")
	if r := e.record(); !r.Success {
		t.Fatalf("turn after errors: %+v", r)
	}
	// The upstream connection dropping ends the session.
	c.send(create(wsModel, "drop"))
	if code := c.closed(); code != websocket.StatusInternalError {
		t.Fatalf("close after upstream drop: %d", code)
	}
	if r := e.record(); r.Success || r.ErrorType != errTypeUpstream {
		t.Fatalf("dropped turn: %+v", r)
	}
}

// An error event's "headers" object is the error's response headers: the
// plugin classifies with them and the quota observer samples them, like a
// handshake's; an error event without one is classified without headers.
func TestWebSocketErrorEventHeadersAreResponseHeaders(t *testing.T) {
	e := newWSEnv(t)
	obs := &fakeQuotaObserver{}
	e.gw.d.Quota = obs
	e.gen.accountTypes[len(e.gen.accountTypes)-1].Type.Quota = &manifest.AccountQuota{Headers: []manifest.QuotaHeader{{
		WindowMinutes: "x-codex-primary-window-minutes", Utilization: "x-codex-primary-used-percent"}}}
	// turn waits for the turn's record: an event is relayed before it is
	// classified.
	turn := func(c *wsClient, input, until string) map[string]string {
		c.send(create(wsModel, input))
		c.until(until)
		e.record()
		return e.wsPlat.classify[len(e.wsPlat.classify)-1].GetHeaders()
	}
	c := e.mustDial()
	// The first turn opens the upstream; its handshake is sampled too.
	c.send(create(wsModel, "ok"))
	c.until("response.completed")
	e.record()
	n := len(obs.calls)
	got := turn(c, "usage-limit", "error")
	if len(obs.calls) != n+1 || obs.calls[n] != (quotaCall{11, 1, 429}) {
		t.Fatalf("quota calls = %+v", obs.calls)
	}
	if h := obs.headers[n]; h.Get("X-Codex-Primary-Used-Percent") != "100.0" || h.Get("X-Codex-Primary-Window-Minutes") != "300" || h.Get("X-Flag") != "" {
		t.Fatalf("observed headers = %v", h)
	}
	want := map[string]string{"x-codex-primary-used-percent": "100.0", "x-codex-primary-window-minutes": "300"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("classify headers = %v, want %v", got, want)
	}
	if got := turn(c, "rate-limit", "error"); len(got) != 0 || len(obs.calls) != n+1 {
		t.Fatalf("an error event without headers: classify headers %v, quota calls %+v", got, obs.calls)
	}
}

func TestWebSocketClientLeavingCancelsTheTurn(t *testing.T) {
	e := newWSEnv(t)
	hold := make(chan struct{})
	defer close(hold)
	e.wsUp.hold = hold
	c := e.mustDial()
	c.send(create(wsModel, "hold"))
	c.until("response.created")
	c.conn.CloseNow()
	r := e.record()
	if r.Success || r.ErrorType != errTypeClientCanceled || r.StatusCode != statusClientClosed {
		t.Fatalf("canceled turn: %+v", r)
	}
	waitFor(t, func() bool { n, _ := e.slots.InUse(context.Background(), "account", 11); return n == 0 })
}

func TestWebSocketDrainClosesWithServiceRestart(t *testing.T) {
	e := newWSEnv(t)
	idle := e.mustDial()
	idle.send(create(wsModel, "hi"))
	idle.until("response.completed")
	e.record()
	hold := make(chan struct{})
	e.wsUp.hold = hold
	busy := e.mustDial()
	busy.send(create(wsModel, "hold"))
	busy.until("response.created")

	e.draining.Store(true)
	if code := idle.closed(); code != wsCloseRestart {
		t.Fatalf("idle session closed with %d", code)
	}
	// The turn in progress finishes, then the session closes.
	time.Sleep(100 * time.Millisecond)
	close(hold)
	busy.until("response.completed")
	if code := busy.closed(); code != wsCloseRestart {
		t.Fatalf("busy session closed with %d", code)
	}
	if r := e.record(); !r.Success {
		t.Fatalf("turn finished during drain: %+v", r)
	}
	if _, resp, err := e.dial(nil); err == nil || resp == nil || resp.StatusCode != 503 {
		t.Fatalf("new session while draining: %v %v", resp, err)
	}
}

func TestWebSocketDrainGraceEndsALongTurn(t *testing.T) {
	e := newWSEnv(t)
	e.gw.ws.drainGrace = 200 * time.Millisecond
	hold := make(chan struct{})
	defer close(hold)
	e.wsUp.hold = hold
	c := e.mustDial()
	c.send(create(wsModel, "hold"))
	c.until("response.created")
	e.draining.Store(true)
	if code := c.closed(); code != wsCloseRestart {
		t.Fatalf("long turn closed with %d", code)
	}
	if r := e.record(); r.Success || r.StatusCode != 503 {
		t.Fatalf("turn cut by drain: %+v", r)
	}
}

func TestWebSocketConnectionLimits(t *testing.T) {
	e := newWSEnv(t)
	if _, resp, err := e.dial(http.Header{}); err == nil || resp == nil || resp.StatusCode != 401 {
		t.Fatalf("no key: %v %v", resp, err)
	}
	resp, err := http.Get(e.srv.URL + "/v1/responses")
	if err != nil || resp.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("plain GET: %v %v", resp, err)
	}
	resp.Body.Close()
	e.gw.ws.firstMessage = 200 * time.Millisecond
	// No limit on the sessions of a key: they are only counted.
	var open []*wsClient
	for range 70 {
		open = append(open, e.mustDial())
	}
	waitFor(t, func() bool { n, _ := e.slots.InUse(context.Background(), "ws", 5); return n == 70 })
	for _, c := range open {
		if code := c.closed(); code != websocket.StatusPolicyViolation {
			t.Fatalf("no first message: %d", code)
		}
	}
	waitFor(t, func() bool { n, _ := e.slots.InUse(context.Background(), "ws", 5); return n == 0 })
	e.gw.ws.idle = 200 * time.Millisecond
	c := e.mustDial()
	c.send(create(wsModel, "hi"))
	c.until("response.completed")
	e.record()
	if code := c.closed(); code != websocket.StatusNormalClosure {
		t.Fatalf("idle session: %d", code)
	}
}

func TestWebSocketNeedsAPluginThatBuildsWebSockets(t *testing.T) {
	e := newWSEnv(t, manifest.CapPlatformAdapter)
	c := e.mustDial()
	c.send(create(wsModel, "hi"))
	if ev := c.until("error"); ev[0].Get("status").Int() != 404 {
		t.Fatalf("no websocket-capable account type: %v", ev)
	}
	if r := e.record(); r.ErrorType != errTypeNoAccount {
		t.Fatalf("record: %+v", r)
	}
	if len(e.wsPlat.builds) != 0 {
		t.Fatal("a plugin without platform.websocket.v1 was asked for a handshake")
	}
}

func waitFor(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !fn() {
		if time.Now().After(deadline) {
			t.Fatal(errors.New("condition not reached"))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The upstream connection outlives its handshake's context, and the
// account's model mapping applies to every turn sent on it.
func TestWebSocketUpstreamOutlivesTheHandshakeAndMapsModels(t *testing.T) {
	e := newWSEnv(t)
	e.accounts.mu.Lock()
	e.accounts.accounts[11].ModelMapping = map[string]string{wsModel: "gpt-5-upstream"}
	e.accounts.mu.Unlock()
	c := e.mustDial()
	for i := 0; i < 2; i++ {
		c.send(create(wsModel, "hi"))
		c.until("response.completed")
		if r := e.record(); !r.Success || r.Model != wsModel || r.UpstreamModel != "gpt-5-upstream" {
			t.Fatalf("turn %d: %+v", i, r)
		}
		time.Sleep(300 * time.Millisecond)
	}
	keys, msgs := e.wsUp.snapshot()
	if len(keys) != 1 || len(msgs) != 2 || gjson.Get(msgs[1], "model").String() != "gpt-5-upstream" {
		t.Fatalf("upstream connections %v, messages %v", keys, msgs)
	}
}

// sendRaw sends the message bytes as they are.
func (c *wsClient) sendRaw(msg string) {
	c.t.Helper()
	if err := c.conn.Write(context.Background(), websocket.MessageText, []byte(msg)); err != nil {
		c.t.Fatal(err)
	}
}

// A plugin that returns the message it was handed leaves every message byte
// for byte as the client sent it, although it is asked every turn.
func TestWebSocketMessagesRelayedUnchanged(t *testing.T) {
	e := newWSEnv(t)
	c := e.mustDial()
	sent := []string{
		`{"type":"response.create",  "model":"` + wsModel + `", "input":"one","store":true}`,
		`{ "model":"` + wsModel + `","type":"response.create","input":"two","temperature":0.30}`,
	}
	for _, m := range sent {
		c.sendRaw(m)
		c.until("response.completed")
		if r := e.record(); !r.Success {
			t.Fatalf("turn: %+v", r)
		}
	}
	if _, msgs := e.wsUp.snapshot(); len(msgs) != 2 || msgs[0] != sent[0] || msgs[1] != sent[1] {
		t.Fatalf("messages changed: %q", msgs)
	}
	b := e.wsPlat.buildCalls()
	if len(b) != 2 {
		t.Fatalf("plugin calls: %d", len(b))
	}
	for i, in := range b {
		if string(in.GetBody()) != sent[i] || in.GetMeta().GetProtocol() != "openai.responses_ws" || in.GetExtractUsage() || in.GetTaskSubmission() {
			t.Fatalf("call %d: %v", i, in)
		}
	}
}

// Every turn is one Execute: the plugin is handed the turn's message after
// the account's model mapping and the upstream receives exactly the bytes it
// returned, the first turn's on the connection its relay opened, later ones
// on that connection whatever URL their relay names.
func TestWebSocketPluginRewritesEveryTurn(t *testing.T) {
	e := newWSEnv(t)
	editAccount(e.env, 11, func(a *core.Account) { a.ModelMapping = map[string]string{wsModel: "gpt-5-upstream"} })
	e.wsPlat.laterURL = "ws://127.0.0.1:1/elsewhere"
	var mu sync.Mutex
	var returned []string
	e.wsPlat.amend = func(_ int, in *pluginv1.ExecuteRequest, out *pluginv1.UpstreamRequest) error {
		b, _ := sjson.SetBytes(in.GetBody(), "store", false)
		b, _ = sjson.DeleteBytes(b, "temperature")
		if !gjson.GetBytes(b, "instructions").Exists() {
			b, _ = sjson.SetBytes(b, "instructions", "")
		}
		out.Body = append(b, ' ')
		mu.Lock()
		returned = append(returned, string(out.Body))
		mu.Unlock()
		return nil
	}
	c := e.mustDial()
	c.send(map[string]any{"type": "response.create", "model": wsModel, "input": "one", "store": true, "temperature": 0.5})
	c.until("response.completed")
	c.send(map[string]any{"type": "response.create", "model": wsModel, "input": "two", "store": true, "instructions": "keep"})
	c.until("response.completed")
	for i := 0; i < 2; i++ {
		if r := e.record(); !r.Success || r.UpstreamModel != "gpt-5-upstream" || r.Tokens.Output != wsOutput {
			t.Fatalf("turn %d: %+v", i, r)
		}
	}
	keys, msgs := e.wsUp.snapshot()
	mu.Lock()
	defer mu.Unlock()
	if len(keys) != 1 || len(msgs) != 2 || !reflect.DeepEqual(msgs, returned) {
		t.Fatalf("upstream connections %v, messages %q, plugin returned %q", keys, msgs, returned)
	}
	if gjson.Get(msgs[0], "instructions").Raw != `""` || gjson.Get(msgs[1], "instructions").String() != "keep" {
		t.Fatalf("instructions: %s / %s", msgs[0], msgs[1])
	}
	b := e.wsPlat.buildCalls()
	if len(b) != 2 {
		t.Fatalf("plugin calls: %d", len(b))
	}
	for i, input := range []string{"one", "two"} {
		body := gjson.ParseBytes(b[i].GetBody())
		if body.Get("input").String() != input || body.Get("model").String() != "gpt-5-upstream" || b[i].GetMeta().GetProtocol() != "openai.responses_ws" ||
			b[i].GetMeta().GetModel() != "gpt-5-upstream" || b[i].GetAccount().GetId() != 11 {
			t.Fatalf("call %d: body %s meta %v", i, b[i].GetBody(), b[i].GetMeta())
		}
	}
}

// A later turn whose Execute fails, or whose relay is refused (a response
// stream format on a WebSocket turn), fails on its own with an error event;
// the connection serves the next turn, and only the turns that ran are
// billed.
func TestWebSocketTurnFailureKeepsTheConnection(t *testing.T) {
	e := newWSEnv(t)
	e.wsPlat.amend = func(_ int, in *pluginv1.ExecuteRequest, out *pluginv1.UpstreamRequest) error {
		switch gjson.GetBytes(in.GetBody(), "input").String() {
		case "response-stream":
			out.ResponseStream = awseventstream.FormatChunk
		case "plugin-down":
			return errors.New("plugin crashed")
		}
		return nil
	}
	c := e.mustDial()
	c.send(create(wsModel, "first"))
	c.until("response.completed")
	if r := e.record(); !r.Success || !r.Billable {
		t.Fatalf("first turn: %+v", r)
	}
	for _, input := range []string{"response-stream", "plugin-down"} {
		c.send(create(wsModel, input))
		ev := c.until("error")
		if ev[0].Get("status").Int() != http.StatusServiceUnavailable || ev[0].Get("error.code").String() != "plugin_unavailable" {
			t.Fatalf("%s: %v", input, ev)
		}
		if r := e.record(); r.Success || r.ErrorType != errTypePluginUnavailable || r.AccountID == nil || *r.AccountID != 11 || r.Tokens.Output != 0 {
			t.Fatalf("%s record: %+v", input, r)
		}
	}
	c.send(create(wsModel, "ok"))
	c.until("response.completed")
	if r := e.record(); !r.Success || r.Tokens.Output != wsOutput {
		t.Fatalf("turn after failed turns: %+v", r)
	}
	keys, msgs := e.wsUp.snapshot()
	if len(keys) != 1 || len(msgs) != 2 || gjson.Get(msgs[0], "input").String() != "first" || gjson.Get(msgs[1], "input").String() != "ok" {
		t.Fatalf("upstream connections %v, messages %v", keys, msgs)
	}
	if n, _ := e.slots.InUse(context.Background(), "account", 11); n != 0 {
		t.Fatalf("account slot held after failed turns: %d", n)
	}
	if len(e.accounts.cooldowns) != 0 || len(e.accounts.disabled) != 0 {
		t.Fatalf("a plugin failure touched the account: cooldowns %v disabled %v", e.accounts.cooldowns, e.accounts.disabled)
	}
}

// A relay opening the connection that the gateway refuses - a response
// stream format, a method other than GET, an address of another scheme -
// sends nothing: the turn fails over to the next account as the plugin's
// failure, without blaming the account.
func TestWebSocketRefusedHandshakeFailsOver(t *testing.T) {
	for name, amend := range map[string]func(out *pluginv1.UpstreamRequest){
		"response stream": func(out *pluginv1.UpstreamRequest) { out.ResponseStream = awseventstream.FormatChunk },
		"method":          func(out *pluginv1.UpstreamRequest) { out.Method = "POST" },
		"scheme":          func(out *pluginv1.UpstreamRequest) { out.Url = "ftp" + strings.TrimPrefix(out.Url, "ws") },
		// A host the plugin did not declare and the account does not name
		// (CONTRACTS §112.17): the same server under another name.
		"undeclared host": func(out *pluginv1.UpstreamRequest) {
			out.Url = strings.Replace(out.Url, testUpstreamHost, "localhost", 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newWSEnv(t)
			e.wsPlat.amend = func(_ int, in *pluginv1.ExecuteRequest, out *pluginv1.UpstreamRequest) error {
				if in.GetAccount().GetId() == 11 {
					amend(out)
				}
				return nil
			}
			c := e.mustDial()
			c.send(create(wsModel, "hello"))
			c.until("response.completed")
			r := e.record()
			if !r.Success || r.AccountID == nil || *r.AccountID != 12 || r.Attempts != 2 {
				t.Fatalf("record: %+v", r)
			}
			if keys, _ := e.wsUp.snapshot(); !reflect.DeepEqual(keys, []string{"oa-12"}) {
				t.Fatalf("handshakes: %v", keys)
			}
			if len(e.accounts.cooldowns) != 0 || len(e.accounts.disabled) != 0 {
				t.Fatalf("cooldowns %v disabled %v", e.accounts.cooldowns, e.accounts.disabled)
			}
		})
	}
}

// The first turn's relay opens the upstream connection at a ws:// or an
// http:// address alike, with the handshake headers in layers: the plugin's
// default headers, the account's header override and parameter override
// over them, the plugin's authoritative headers last (an empty one removes
// the header); no client header is passed through.
func TestWebSocketHandshakeHeaderLayers(t *testing.T) {
	for name, scheme := range map[string]string{"ws url": "ws", "http url": "http"} {
		t.Run(name, func(t *testing.T) {
			e := newWSEnv(t)
			e.wsPlat.amend = func(_ int, _ *pluginv1.ExecuteRequest, out *pluginv1.UpstreamRequest) error {
				out.Url = scheme + strings.TrimPrefix(out.Url, "ws")
				out.DefaultHeaders = map[string]string{"x-default-a": "plugin-a", "x-default-b": "plugin-b", "x-default-c": "plugin-c",
					"x-plugin-sig": "default-sig"}
				out.Headers["x-plugin-sig"] = "sig-1"
				out.Headers["x-op-token"] = ""
				return nil
			}
			withHeaderOverride(t, e.env, 11, `{"x-default-a":"op-a","x-default-b":"{unset}","x-plugin-sig":"op-sig","openai-beta":"op-beta","x-extra":"op-extra","x-op-token":"op-token"}`)
			withParamOverride(t, e.env, 11, `{"operations":[{"mode":"set_header","path":"x-default-c","value":"param-c"},
				{"mode":"delete_header","path":"openai-beta"}]}`)
			editAccount(e.env, 11, func(a *core.Account) { a.HeaderPassthrough = []string{"*"} })
			c, _, err := e.dial(http.Header{"Authorization": {"Bearer " + testKey}, "X-Client-Only": {"c1"}, "X-Default-B": {"client-b"}})
			if err != nil {
				t.Fatal(err)
			}
			c.send(create(wsModel, "hello"))
			c.until("response.completed")
			if r := e.record(); !r.Success || *r.AccountID != 11 {
				t.Fatalf("record: %+v", r)
			}
			e.wsUp.mu.Lock()
			h := e.wsUp.headers[0]
			e.wsUp.mu.Unlock()
			for k, want := range map[string]string{
				"Authorization": "Bearer oa-11", "Openai-Beta": "responses_websockets=2026-02-06", "X-Plugin-Sig": "sig-1",
				"X-Default-A": "op-a", "X-Default-C": "param-c", "X-Extra": "op-extra",
				"X-Default-B": "", "X-Client-Only": "", "X-Op-Token": "",
			} {
				if got := strings.Join(h.Values(k), ","); got != want {
					t.Errorf("%s = %q, want %q", k, got, want)
				}
			}
		})
	}
}

// A session has no idle timeout by default: with neither a first message nor
// a message between turns for longer than any old limit, it stays open on its
// connection and serves the next turn. Keepalive pings keep running.
func TestWebSocketSessionHasNoIdleTimeout(t *testing.T) {
	e := newWSEnv(t)
	if d := defaultWSLimits(); d.firstMessage != 0 || d.idle != 0 || d.ping <= 0 || d.pingTimeout <= 0 {
		t.Fatalf("default limits = %+v", d)
	}
	e.gw.ws.ping = 20 * time.Millisecond
	e.gw.ws.pingTimeout = 5 * time.Second
	c := e.mustDial()
	time.Sleep(200 * time.Millisecond) // no first message yet
	c.send(create(wsModel, "one"))
	c.until("response.completed")
	e.record()
	time.Sleep(200 * time.Millisecond) // idle between turns
	c.send(create(wsModel, "two"))
	c.until("response.completed")
	if r := e.record(); !r.Success || r.AccountID == nil || *r.AccountID != 11 {
		t.Fatalf("second turn after idling: %+v", r)
	}
}

// A client that stops answering pings is treated as disconnected: the session
// ends and its per-key connection slot is released.
func TestWebSocketUnansweredPingEndsTheSession(t *testing.T) {
	e := newWSEnv(t)
	e.gw.ws.ping = 20 * time.Millisecond
	e.gw.ws.pingTimeout = 100 * time.Millisecond
	_ = e.mustDial() // never reads, so it never answers a ping
	waitFor(t, func() bool { n, _ := e.slots.InUse(context.Background(), "ws", 5); return n == 1 })
	waitFor(t, func() bool { n, _ := e.slots.InUse(context.Background(), "ws", 5); return n == 0 })
}

// A connection without a sticky session is one session however many turns
// it serves (CONTRACTS §71.3): a later turn passes a cap the connection fills.
func TestWebSocketConnectionCountsOneSession(t *testing.T) {
	e := newWSEnv(t)
	e.gw.d.Limiter = account.NewLimiter(e.rdb)
	e.accounts.set(11, func(a *core.Account) { a.SPMLimit = 1 })
	c := e.mustDial()
	for turn := range 2 {
		c.send(create(wsModel, "hi"))
		c.until("response.completed")
		if r := e.record(); !r.Success || r.AccountID == nil || *r.AccountID != 11 {
			t.Fatalf("turn %d: %+v", turn, r)
		}
	}
	u, err := e.gw.d.Limiter.Usage(t.Context(), []core.AccountRef{{ID: 11}})
	if err != nil || u[11].Sessions != 1 || u[11].RPM != 2 {
		t.Fatal(u, err)
	}
}

// A handshake that fails over leaves no session behind on the account it
// left (CONTRACTS §71.3): only the account that serves the connection holds it.
func TestWebSocketHandshakeFailoverReleasesTheSession(t *testing.T) {
	e := newWSEnv(t)
	e.gw.d.Limiter = account.NewLimiter(e.rdb)
	e.wsUp.reject["oa-11"] = http.StatusUnauthorized
	c := e.mustDial()
	c.send(create(wsModel, "hello"))
	c.until("response.completed")
	if r := e.record(); r.AccountID == nil || *r.AccountID != 12 || r.Attempts != 2 {
		t.Fatalf("record after failover: %+v", r)
	}
	u, err := e.gw.d.Limiter.Usage(t.Context(), []core.AccountRef{{ID: 11}, {ID: 12}})
	if err != nil || u[11].Sessions != 0 || u[12].Sessions != 1 {
		t.Fatal(u, err)
	}
}

// A WebSocket turn is balanced like any other pick (CONTRACTS §89.3).
func TestWebSocketTurnsAreBalanced(t *testing.T) {
	e := newWSEnv(t)
	e.accounts.set(11, func(a *core.Account) {
		a.Priority = 98
		a.RPMLimit = 100
	})
	lim := newFakeLimiter()
	lim.usage = map[int64]core.RateUsage{11: {RPM: 90}}
	e.gw.d.Limiter = lim
	e.setScheduling(SchedulingSettings{Metric: balanceMetricRPM, LimitLoad: true})
	// Weights 1:1 would take oa-11 at 0.4; its load of 90 (0.1:1), then the
	// traffic balance against the idle oa-12, leave it almost nothing.
	e.gw.randFloat = func() float64 { return 0.4 }
	c := e.mustDial()
	c.send(create(wsModel, "hi"))
	c.until("response.completed")
	if r := e.record(); r.AccountID == nil || *r.AccountID != 12 || coreDecision(r) == nil {
		t.Fatalf("record: account %v, decisions %+v", r.AccountID, r.SchedDecisions)
	}
}

// turn runs one successful turn and returns its usage record.
func (e *wsEnv) turn(c *wsClient, input string) *core.UsageRecord {
	e.t.Helper()
	c.send(create(wsModel, input))
	c.until("response.completed")
	return e.record()
}

// A key revoked after the upgrade ends the session at its next turn, which
// is refused like an unauthenticated request: no usage record.
func TestWebSocketRevokedKeyEndsTheSessionAtTheNextTurn(t *testing.T) {
	e := newWSEnv(t)
	c := e.mustDial()
	e.turn(c, "first")
	e.auth.change(func(a *fakeAuth) { delete(a.keys, testKey) })
	c.send(create(wsModel, "second"))
	if ev := c.until("error"); ev[0].Get("status").Int() != 401 {
		t.Fatalf("revoked key: %v", ev)
	}
	if code := c.closed(); code != websocket.StatusPolicyViolation {
		t.Fatalf("revoked key close code: %d", code)
	}
	e.noRecord()
	if _, msgs := e.wsUp.snapshot(); len(msgs) != 1 {
		t.Fatalf("refused turn reached the upstream: %v", msgs)
	}
	waitFor(t, func() bool { n, _ := e.slots.InUse(context.Background(), "ws", 5); return n == 0 })
}

func TestWebSocketPermissionDeniedEndsTheSession(t *testing.T) {
	e := newWSEnv(t)
	c := e.mustDial()
	e.turn(c, "first")
	e.auth.change(func(a *fakeAuth) { a.err = core.ErrPermissionDenied.WithMessage("user is disabled") })
	c.send(create(wsModel, "second"))
	if ev := c.until("error"); ev[0].Get("status").Int() != 403 {
		t.Fatalf("denied key: %v", ev)
	}
	if code := c.closed(); code != websocket.StatusPolicyViolation {
		t.Fatalf("denied key close code: %d", code)
	}
	e.noRecord()
}

// Limits and quota changed during the session apply to its next turn; the
// turn they refuse is recorded and the session goes on.
func TestWebSocketTurnUsesFreshKeyLimits(t *testing.T) {
	e := newWSEnv(t)
	c := e.mustDial()
	e.turn(c, "first")
	e.auth.change(func(a *fakeAuth) {
		a.keys[testKey].KeyQuota = core.KeyQuota{Limit: decimal.NewFromInt(1), Used: decimal.NewFromInt(1)}
	})
	c.send(create(wsModel, "over quota"))
	if ev := c.until("error"); ev[0].Get("status").Int() != 402 || ev[0].Get("error.code").String() != core.ErrKeyQuotaExhausted.Code {
		t.Fatalf("exhausted quota: %v", ev)
	}
	if r := e.record(); r.ErrorType != errTypeKeyQuotaExhausted {
		t.Fatalf("exhausted quota record: %+v", r)
	}
	// One request a minute, and this minute already had the first turn.
	e.auth.change(func(a *fakeAuth) {
		a.keys[testKey].KeyQuota = core.KeyQuota{}
		a.keys[testKey].KeyLimits.RPM = 1
	})
	c.send(create(wsModel, "over rpm"))
	if ev := c.until("error"); ev[0].Get("status").Int() != 429 {
		t.Fatalf("lowered rpm: %v", ev)
	}
	if r := e.record(); r.ErrorType != errTypeRateLimited {
		t.Fatalf("lowered rpm record: %+v", r)
	}
	e.auth.change(func(a *fakeAuth) { a.keys[testKey].KeyLimits.RPM = 0 })
	if r := e.turn(c, "again"); !r.Success {
		t.Fatalf("turn after the limits were lifted: %+v", r)
	}
	if keys, _ := e.wsUp.snapshot(); len(keys) != 1 {
		t.Fatalf("session did not keep its upstream connection: %v", keys)
	}
}

// The upstream connection is an account of the first turn's group; a key
// moved to another group is told to reconnect.
func TestWebSocketGroupChangeAsksToReconnect(t *testing.T) {
	e := newWSEnv(t)
	c := e.mustDial()
	// Before any turn there is no upstream connection to keep: the first
	// turn is scheduled in the group the key is in by then.
	e.accounts.addTyped(testGroup+1, 13, 99, "oa-13", "openai", "apikey")
	e.auth.change(func(a *fakeAuth) { a.keys[testKey].Group.ID = testGroup + 1 })
	if r := e.turn(c, "first"); r.AccountID == nil || *r.AccountID != 13 {
		t.Fatalf("first turn after a group change: %+v", r)
	}
	e.auth.change(func(a *fakeAuth) { a.keys[testKey].Group.ID = testGroup })
	c.send(create(wsModel, "second"))
	if ev := c.until("error"); ev[0].Get("status").Int() != 409 || ev[0].Get("error.code").String() != "api_key_group_changed" {
		t.Fatalf("group change: %v", ev)
	}
	if code := c.closed(); code != wsCloseRestart {
		t.Fatalf("group change close code: %d", code)
	}
	e.noRecord()
}

func TestWebSocketAuthUnavailableFailsOnlyTheTurn(t *testing.T) {
	e := newWSEnv(t)
	e.gw.ws.firstMessage = 300 * time.Millisecond
	c := e.mustDial()
	unavailable := func(input string) {
		t.Helper()
		e.auth.change(func(a *fakeAuth) { a.err = core.ErrUnavailable.WithMessage("key store unavailable") })
		c.send(create(wsModel, input))
		if ev := c.until("error"); ev[0].Get("status").Int() != 503 {
			t.Fatalf("auth unavailable: %v", ev)
		}
		e.noRecord()
		e.auth.change(func(a *fakeAuth) { a.err = nil })
	}
	// The failed message is the session's first: the first-message timer
	// stops with it and does not close the session later.
	unavailable("first")
	time.Sleep(400 * time.Millisecond)
	if r := e.turn(c, "second"); !r.Success {
		t.Fatalf("turn after auth came back: %+v", r)
	}
	unavailable("third")
	if r := e.turn(c, "fourth"); !r.Success {
		t.Fatalf("turn after auth came back: %+v", r)
	}
	if keys, msgs := e.wsUp.snapshot(); len(keys) != 1 || len(msgs) != 2 {
		t.Fatalf("session did not survive: keys %v messages %d", keys, len(msgs))
	}
}

// A message that cannot start a turn is refused before the key is looked up.
func TestWebSocketInvalidMessageSkipsAuthentication(t *testing.T) {
	e := newWSEnv(t)
	c := e.mustDial()
	e.auth.change(func(a *fakeAuth) { a.err = core.ErrUnavailable.WithMessage("key store unavailable") })
	c.send(map[string]any{"type": "session.update"})
	if ev := c.until("error"); ev[0].Get("status").Int() != 400 {
		t.Fatalf("invalid message: %v", ev)
	}
	e.noRecord()
	e.auth.change(func(a *fakeAuth) { a.err = nil })
	if r := e.turn(c, "next"); !r.Success {
		t.Fatalf("turn after the invalid message: %+v", r)
	}
}

// A return_error that lets the attempt move on, on every account, answers
// the first turn with the operator's error as on HTTP, not with "no
// available account".
func TestWebSocketRetryableReturnErrorReachesTheClient(t *testing.T) {
	e := newWSEnv(t)
	for _, id := range []int64{11, 12} {
		setParamOverride(t, e.env, id, `{"operations":[{"mode":"return_error","value":{"message":"not here","status_code":400,"skip_retry":false}}]}`)
	}
	c := e.mustDial()
	c.send(create(wsModel, "hello"))
	ev := c.until("error")
	if ev[0].Get("status").Int() != http.StatusBadRequest || ev[0].Get("error.message").String() != "not here" {
		t.Fatalf("retryable return_error on every account: %v", ev)
	}
	if r := e.record(); r.Success || r.ErrorType != errTypeInvalidRequest || r.Attempts != 2 {
		t.Fatalf("record: %+v", r)
	}
	if keys, _ := e.wsUp.snapshot(); len(keys) != 0 {
		t.Fatalf("handshakes: %v", keys)
	}
}
