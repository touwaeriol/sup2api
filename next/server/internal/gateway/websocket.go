package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
	"github.com/tidwall/gjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/observation"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/execfetch"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
)

// WebSocket endpoints (manifest kind "websocket", CONTRACTS §35,
// docs/OPENAI-RESPONSES-WEBSOCKET.md). The client upgrades once; every
// message carrying a model at request.modelPath is a turn, served on the one
// upstream WebSocket and account the first turn chose, metered with the
// usage.sse rules (matched by the message "type") and recorded and billed on
// its own, exactly like a streaming HTTP request.
//
// Every turn's message, after the hooks, the model mappings and the
// overrides as on HTTP, goes through the account type's Execute (CONTRACTS
// §83.6, design §4.8): the plugin relays the turn's final message without
// waiting. The first turn's relay also opens the upstream connection (its
// URL and handshake headers); a later turn's only gives its message, sent
// as it is on the open connection.

// wsLimits are the session limits. They are fields only so tests can
// shorten them. A session has no idle timeout: it stays open, on its account
// and upstream connection, until the client disconnects (or the node drains,
// the upstream closes or a ping goes unanswered); keepalive pings on both
// connections keep proxies from dropping it and find dead peers.
type wsLimits struct {
	ping        time.Duration // keepalive ping interval on both connections; 0 = none
	pingTimeout time.Duration // a ping unanswered this long closes that connection
	slotWait    time.Duration // later turns wait this long for the account
	auth        time.Duration // re-authenticating the key before a turn
	drainGrace  time.Duration // a turn in progress when draining starts
	drainPoll   time.Duration
}

func defaultWSLimits() wsLimits {
	return wsLimits{ping: 30 * time.Second, pingTimeout: 30 * time.Second, slotWait: 10 * time.Second, auth: 10 * time.Second,
		drainGrace: 30 * time.Second,
		drainPoll:  500 * time.Millisecond}
}

// Close codes of the client connection.
const (
	wsCloseRestart = websocket.StatusCode(1012) // Service Restart: reconnect
)

// terminalEvents end a turn.
var terminalEvents = map[string]bool{
	"response.completed": true, "response.incomplete": true, "response.failed": true, "error": true,
}

// serveWebSocket authenticates, upgrades and runs one session. Failures
// before the upgrade are ordinary HTTP errors in the endpoint's format.
func (g *Gateway) serveWebSocket(ctx context.Context, cl *call) {
	if !strings.EqualFold(cl.c.GetHeader("Upgrade"), "websocket") {
		writeError(cl.c, cl.format, &gwError{Status: http.StatusUpgradeRequired, Code: core.ErrInvalidArgument.Code,
			Message: "this endpoint requires a WebSocket upgrade"})
		return
	}
	key := cl.apiKey()
	if key == "" {
		writeError(cl.c, cl.format, fromCore(core.ErrUnauthenticated.WithMessage("missing api key"), ""))
		return
	}
	ip := cl.c.ClientIP()
	p, err := g.d.Auth.Authenticate(core.WithClientIP(ctx, ip), key)
	if err != nil {
		writeError(cl.c, cl.format, fromCore(core.AsError(err), ""))
		return
	}
	cl.principal = p
	if g.draining() {
		writeError(cl.c, cl.format, fromCore(core.ErrUnavailable.WithMessage("node is draining"), ""))
		return
	}
	// The open sessions of a key are counted cluster-wide, without a limit.
	ctx, release, _, err := core.AcquireSlot(ctx, g.d.Slots, "ws", p.KeyID, 0, cl.rid)
	if err != nil {
		writeError(cl.c, cl.format, fromCore(core.ErrUnavailable.WithCause(err), ""))
		return
	}
	defer release()
	conn, err := websocket.Accept(cl.c.Writer, cl.c.Request, &websocket.AcceptOptions{CompressionMode: websocket.CompressionContextTakeover})
	if err != nil {
		slog.InfoContext(ctx, "gateway: websocket upgrade failed", "request_id", cl.rid, "err", err)
		return
	}
	conn.SetReadLimit(cl.bodyLimit())
	s := &wsSession{g: g, base: cl, client: conn, id: cl.rid, key: key, ip: ip}
	s.run(ctx)
}

func (g *Gateway) draining() bool { return g.d.Draining != nil && g.d.Draining() }

// wsSession is one client connection.
type wsSession struct {
	g      *Gateway
	base   *call // the upgrade request: endpoint, headers; its principal is refreshed every turn
	client *websocket.Conn
	// present ends when the client is gone. What a turn only waits for on
	// the client's behalf (the upstream handshake) ends with it; recording
	// the turn goes on under the session's context.
	present context.Context
	id      string
	// key and ip are the upgrade request's API key and client address; every
	// turn authenticates with them again (refresh).
	key, ip string

	// The upstream connection and the account the first turn chose; every
	// later turn of the session uses them. pacct is the account as the
	// plugin was handed it when the connection opened: its proxy is the one
	// the connection goes through, so every later turn is handed it too
	// (CONTRACTS §124).
	up              *websocket.Conn
	ref             *core.AccountRef
	acc             *core.Account
	rt              *typeRoute
	pacct           *pluginv1.Account
	routes          map[core.AccountTypeKey]*typeRoute
	session         string
	upIn            chan wsMessage
	quotaGeneration int64
	// opening is the first turn's message as the plugin relayed it with the
	// handshake; that turn sends it, later turns ask the plugin again.
	opening []byte
}

type wsMessage struct {
	typ  websocket.MessageType
	data []byte
	err  error
}

// wsTurn is one response in progress.
type wsTurn struct {
	cl      *call
	usage   *usageAcc
	lease   context.Context // the account slot lease
	release func()          // account and user slots
}

func (s *wsSession) run(ctx context.Context) {
	sctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	present, left := context.WithCancel(sctx)
	defer left()
	s.present = present
	in := make(chan wsMessage, 1)
	go s.read(sctx, s.client, in, left)
	go s.heartbeat(sctx, s.client)
	defer func() {
		if s.up != nil {
			_ = s.up.CloseNow()
		}
		_ = s.client.CloseNow()
	}()

	var turn *wsTurn
	defer func() {
		if turn != nil {
			turn.release()
		}
	}()
	poll := time.NewTicker(s.g.ws.drainPoll)
	defer poll.Stop()
	var drainDeadline time.Time
	// upstreamIdle bounds the silence of a turn in progress, as a stream's idle
	// timeout does on HTTP, so a stuck turn cannot hold its slots forever.
	upstreamIdle := time.NewTimer(time.Hour)
	upstreamIdle.Stop()
	defer upstreamIdle.Stop()
	end := func(code websocket.StatusCode, reason string) {
		_ = s.client.Close(code, reason)
	}
	for {
		var upIn chan wsMessage
		var lease <-chan struct{}
		if turn != nil {
			upIn = s.upIn
			lease = turn.lease.Done()
		} else if s.up != nil {
			upIn = s.upIn
		}
		select {
		case m := <-in:
			if m.err != nil {
				if turn != nil {
					s.finish(sctx, turn, wsOutcome{status: statusClientClosed, errType: errTypeClientCanceled, msg: "client canceled"})
				}
				return
			}
			if turn != nil {
				s.sendError(sctx, &gwError{Status: http.StatusConflict, Code: "response_in_progress", Type: "invalid_request_error",
					Message: "a response is already in progress on this connection; wait for it to finish"})
				continue
			}
			if s.g.draining() {
				end(wsCloseRestart, "node is draining; reconnect")
				return
			}
			// The message is checked first: one that cannot start a turn
			// costs no key lookup.
			e, code := wsMessageError(m.data), websocket.StatusCode(0)
			if e == nil {
				e, code = s.refresh(sctx)
			}
			if e != nil {
				s.sendError(sctx, e)
				if code == 0 {
					continue
				}
				end(code, truncateUTF8(e.Message, 120))
				return
			}
			var fatal bool
			if turn, fatal = s.begin(sctx, m); fatal {
				end(websocket.StatusInternalError, "upstream connection failed; reconnect")
				return
			}
			if turn != nil {
				upstreamIdle.Reset(turn.cl.streamIdle())
			}
		case m := <-upIn:
			if m.err != nil {
				if turn != nil {
					s.finish(sctx, turn, wsOutcome{status: http.StatusBadGateway, errType: errTypeUpstream,
						msg: "upstream connection closed: " + truncateUTF8(m.err.Error(), 300)})
				}
				end(websocket.StatusInternalError, "upstream connection closed; reconnect")
				return
			}
			if err := s.write(sctx, m.typ, m.data); err != nil {
				if turn != nil {
					s.finish(sctx, turn, wsOutcome{status: statusClientClosed, errType: errTypeClientCanceled, msg: "client canceled"})
				}
				return
			}
			if turn == nil {
				continue
			}
			upstreamIdle.Reset(turn.cl.streamIdle())
			if done, out := s.observe(sctx, turn, m.data); done {
				s.finish(sctx, turn, out)
				turn = nil
				upstreamIdle.Stop()
				if !drainDeadline.IsZero() {
					end(wsCloseRestart, "node is draining; reconnect")
					return
				}
			}
		case <-lease:
			s.finish(sctx, turn, wsOutcome{status: http.StatusServiceUnavailable, errType: errTypeInternal, msg: "account lease lost"})
			end(websocket.StatusTryAgainLater, "account lease lost; reconnect")
			return
		case <-upstreamIdle.C:
			if turn == nil {
				continue
			}
			s.finish(sctx, turn, wsOutcome{status: http.StatusGatewayTimeout, errType: errTypeUpstream, msg: errStreamIdleTimeout.Error()})
			end(websocket.StatusTryAgainLater, "upstream went silent; reconnect")
			return
		case <-poll.C:
			if !s.g.draining() && ctx.Err() == nil {
				continue
			}
			if turn == nil {
				end(wsCloseRestart, "node is draining; reconnect")
				return
			}
			if drainDeadline.IsZero() {
				drainDeadline = time.Now().Add(s.g.ws.drainGrace)
			} else if time.Now().After(drainDeadline) {
				s.finish(sctx, turn, wsOutcome{status: http.StatusServiceUnavailable, errType: errTypeInternal, msg: "node drained before the response finished"})
				end(wsCloseRestart, "node is draining; reconnect")
				return
			}
		}
	}
}

// read pumps one connection into ch until it fails; failed is called as soon
// as it does, before the session loop gets to hear of it.
func (s *wsSession) read(ctx context.Context, conn *websocket.Conn, ch chan<- wsMessage, failed func()) {
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			failed()
		}
		select {
		case ch <- wsMessage{typ: typ, data: data, err: err}:
		case <-ctx.Done():
			return
		}
		if err != nil {
			return
		}
	}
}

// heartbeat pings c every ping interval until the session ends, on a
// goroutine of its own: a session loop blocked writing to a peer that stopped
// reading does not answer for that peer. A ping that fails, or is not written
// and answered within pingTimeout, closes c; its reader and any write to it
// then report the error and the session ends the way it does for a
// disconnect. A ping waits for the message being written, so pingTimeout is
// also how long a peer may take to read the rest of one message.
func (s *wsSession) heartbeat(ctx context.Context, c *websocket.Conn) {
	if s.g.ws.ping <= 0 {
		return
	}
	t := time.NewTicker(s.g.ws.ping)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		pctx, cancel := context.WithTimeout(ctx, s.g.ws.pingTimeout)
		err := ping(pctx, c)
		cancel()
		if err != nil {
			if ctx.Err() == nil {
				_ = c.CloseNow()
			}
			return
		}
	}
}

// ping pings c and waits for the pong until ctx ends. The library gives a
// control frame five seconds to be written, the wait for the message being
// written included: a ping that ran out of them while ctx has time left is
// sent again, so a peer slowly reading one large message has all of ctx, as
// one slow to answer has.
func ping(ctx context.Context, c *websocket.Conn) error {
	for {
		err := c.Ping(ctx)
		if err == nil || ctx.Err() != nil || !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
	}
}

func (s *wsSession) write(ctx context.Context, typ websocket.MessageType, data []byte) error {
	return s.client.Write(ctx, typ, data)
}

// sendError tells the client that a turn could not run, in the Responses
// error event shape; the connection stays open.
func (s *wsSession) sendError(ctx context.Context, e *gwError) {
	status := e.Status
	if status <= 0 || status == statusClientClosed {
		status = http.StatusInternalServerError
	}
	t := e.Type
	if t == "" {
		t = openaiType(status)
	}
	code := e.Code
	if code == "" {
		code = defaultCode(status)
	}
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(status)
	}
	body, _ := json.Marshal(map[string]any{"type": "error", "status": status,
		"error": map[string]any{"type": t, "code": code, "message": msg, "param": nil}})
	_ = s.write(ctx, websocket.MessageText, body)
}

// refresh authenticates the session's API key again before a turn, so the
// turn runs under the key's present state: a key revoked, disabled or
// refused since the upgrade ends the session, new limits and quota apply at
// once. A non-zero closeCode ends the session with it; an error without one
// fails only this turn (the key store may be back for the next). No usage is
// recorded for a turn refused here, as for a request that fails to
// authenticate on HTTP.
func (s *wsSession) refresh(ctx context.Context) (e *gwError, closeCode websocket.StatusCode) {
	// The session context has no deadline; a key store that hangs must not
	// stop the session loop (pings, drain, the client closing).
	actx, cancel := context.WithTimeout(core.WithClientIP(ctx, s.ip), s.g.ws.auth)
	p, err := s.g.d.Auth.Authenticate(actx, s.key)
	cancel()
	if err != nil {
		e = fromCore(core.AsError(err), "")
		if e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden {
			return e, websocket.StatusPolicyViolation
		}
		slog.WarnContext(ctx, "gateway: websocket turn authentication failed", "request_id", s.id, "err", err)
		return e, 0
	}
	// The upstream connection belongs to an account of the group the first
	// turn ran in; a key moved to another group reconnects to be scheduled there.
	if s.up != nil && p.Group.ID != s.base.principal.Group.ID {
		return &gwError{Status: http.StatusConflict, Code: "api_key_group_changed", Type: "invalid_request_error",
			Message: "the API key moved to another group; reconnect"}, wsCloseRestart
	}
	s.base.principal = p
	return nil, 0
}

// newCall is the per-turn request: a fresh request id (the usage and ledger
// idempotency key), the upgrade request's endpoint and headers, the principal
// as refresh last saw it, and the message as the body.
func (s *wsSession) newCall(body []byte) *call {
	b := s.base
	cl := &call{g: s.g, c: b.c, gen: b.gen, ep: b.ep, plugin: b.plugin, platform: b.platform, pf: b.pf, params: b.params,
		format: b.format, rid: httpapi.NewRequestID(), clientRID: b.clientRID, start: s.g.now(), gw: b.gw, stickyCfg: b.stickyCfg,
		autoDisable: b.autoDisable, scheduling: b.scheduling, failurePolicy: b.failurePolicy, principal: b.principal, body: body, stream: true}
	cl.rec = cl.newRecord()
	cl.rec.Stream = true
	return cl
}

// wsMessageError answers why a client message cannot start a turn: it is not
// a JSON object within the shape limits, or not a response.create.
func wsMessageError(data []byte) *gwError {
	shape := scanJSONShape(data)
	if shape.tooDeep {
		return jsonShapeError(shape, "message")
	}
	if !utf8.Valid(data) || !gjson.ValidBytes(data) || !gjson.ParseBytes(data).IsObject() {
		return &gwError{Status: http.StatusBadRequest, Code: core.ErrInvalidArgument.Code, Message: "message is not a JSON object"}
	}
	if e := jsonShapeError(shape, "message"); e != nil {
		return e
	}
	if typ := gjson.GetBytes(data, "type").String(); typ != "response.create" {
		return &gwError{Status: http.StatusBadRequest, Code: core.ErrInvalidArgument.Code,
			Message: "unsupported message type " + strconvQuote(typ) + ": only response.create is accepted"}
	}
	return nil
}

// begin starts a turn for a message wsMessageError accepted, or answers why
// it cannot run and records that.
// fatal means the upstream connection broke and the session must end.
func (s *wsSession) begin(ctx context.Context, m wsMessage) (_ *wsTurn, fatal bool) {
	reject := func(cl *call, e *gwError) (*wsTurn, bool) {
		if cl != nil {
			cl.fail(e)
			cl.submit()
		}
		s.sendError(ctx, e)
		return nil, false
	}
	cl := s.newCall(m.data)
	if e := cl.checkModel(ctx); e != nil {
		return reject(cl, e)
	}
	p := cl.principal
	// Each turn is one request of the API key (CONTRACTS §60), capped
	// before the hooks run, as on HTTP.
	tokens := int64(0)
	if p.KeyLimits.TPM > 0 {
		tokens = cl.rateTokens(ctx)
	}
	_, releaseKey, keyHold, ke := s.g.admitKey(ctx, p, cl.rid, tokens)
	if ke != nil {
		return reject(cl, ke)
	}
	cl.keyHold = keyHold
	var releaseAccount func()
	handedOff := false
	defer func() {
		if !handedOff {
			cl.releaseHolds(ctx)
			if releaseAccount != nil {
				releaseAccount()
			}
			releaseKey()
		}
	}()
	cl.sessionFP = cl.sessionFingerprint(ctx)
	if e := cl.runHooks(ctx); e != nil {
		return reject(cl, e)
	}
	if e := cl.checkModel(ctx); e != nil {
		return reject(cl, e)
	}
	if e := cl.prepareBilling(ctx); e != nil {
		return reject(cl, e)
	}
	var e *gwError
	opensUpstream := s.up == nil // this turn opens the upstream connection
	if opensUpstream {
		releaseAccount, e = s.connect(ctx, cl)
	} else {
		releaseAccount, e = s.reuse(ctx, cl)
	}
	if e != nil {
		releaseKey()
		return reject(cl, e)
	}
	// The turn goes to the connection's account (CONTRACTS §85).
	if e := cl.runAttemptHooks(ctx, s.ref); e != nil {
		return reject(cl, e)
	}
	// Quota observations of this turn go under the connection's generation
	// only; a connection opened without one never writes any.
	cl.quotaPinned, cl.quotaPin = true, s.quotaGeneration
	t := &wsTurn{cl: cl, lease: cl.slotCtx, release: func() { releaseAccount(); releaseKey() }}
	if t.lease == nil {
		t.lease = ctx
	}
	t.usage = newUsageAcc(s.rt.usage).WithLog("request_id", cl.rid, "plugin", s.rt.binding.Plugin.Key,
		"platform", s.rt.platform, "protocol", s.rt.upstream)
	// The message the plugin relayed for this turn: the handshake's for the
	// turn that opened the connection, a fresh Execute for any other. A turn
	// it cannot be had for fails on its own.
	payload := s.opening
	s.opening = nil
	if !opensUpstream {
		_, req, _, res := s.executeTurn(ctx, cl, s.ref, s.acc, s.rt)
		if req == nil {
			return reject(cl, res.err)
		}
		payload = req.GetBody()
	}
	err := s.up.Write(ctx, websocket.MessageText, payload)
	if err != nil {
		s.finish(ctx, t, wsOutcome{status: http.StatusBadGateway, errType: errTypeUpstream, msg: "upstream write failed: " + err.Error()})
		s.sendError(ctx, &gwError{Status: http.StatusBadGateway, Code: codeUpstreamError, Message: "upstream connection failed"})
		return nil, true
	}
	handedOff = true
	return t, false
}

// connect is the first turn: schedule like an HTTP request (candidates,
// sticky binding, rankers, rate limits, account slots) and open the upstream
// WebSocket, failing over on handshake errors the plugin classifies so.
func (s *wsSession) connect(ctx context.Context, cl *call) (func(), *gwError) {
	cl.planRoutes()
	if len(cl.routeKeys) == 0 {
		return nil, errEndpointNotServed
	}
	all, err := s.g.d.Accounts.Candidates(ctx, cl.principal.Group.ID, cl.routeKeys)
	if err != nil {
		return nil, fromCore(core.ErrUnavailable.WithCause(err), errTypeInternal)
	}
	cands, closed := cl.eligibleAccounts(all)
	if e := cl.protocolNotServed(ctx, closed); e != nil {
		return nil, e
	}
	eligible := cands
	cands, availabilityErr := cl.filterAvailability(ctx, cands)
	cl.noteDropped(eligible, cands, core.SchedAvailabilityBlocked)
	cl.sched.eligible = len(cands)
	if availabilityErr != nil {
		return nil, availabilityErr
	}
	cl.sticky = cl.resolveSticky(ctx)
	if cl.failureState.stickyErr != nil {
		return nil, fromCore(core.ErrUnavailable.WithMessage("sticky store unavailable"), errTypeInternal)
	}
	cl.session = cl.sessionIdentity(core.ConnectionSession)
	excluded := map[int64]bool{}
	var last *gwError
	var pendingRelease func()
	defer func() {
		if pendingRelease != nil {
			pendingRelease()
		}
	}()
	for cl.rec.Attempts < cl.attemptLimit() {
		available, err := cl.policyCandidates(ctx, cands)
		if err != nil {
			return nil, fromCore(core.ErrUnavailable.WithMessage("account selection unavailable"), errTypeInternal)
		}
		ref, release, busy := cl.pick(ctx, available, excluded)
		if ref == nil {
			cl.endFailurePolicy("no_eligible_account")
			if last == nil {
				last = fromCore(core.ErrNoAvailableAccount, errTypeNoAccount)
				if busy {
					last = fromCore(core.ErrRateLimited.WithMessage("all accounts are busy or rate limited, please retry later"), errTypeNoAccount)
				}
			}
			break
		}
		pendingRelease = release
		if s.g.d.Limiter != nil {
			admitted, err := cl.admitRate(ref)
			if err != nil || !admitted {
				release()
				excluded[ref.ID] = true
				cl.skipAccount(ref.ID, core.SchedRateLimited, "")
				last = fromCore(core.ErrRateLimited.WithMessage("all accounts are busy or rate limited, please retry later"), errTypeNoAccount)
				continue
			}
		}
		cl.rec.Attempts++
		stickyAttempt := cl.sticky != nil && cl.sticky.hit && cl.sticky.bound == ref.ID
		cl.startObservationAttempt(ref)
		cl.beginFailureAttempt(ref)
		begun := cl.g.now()
		cl.rec.ErrorMessage = "" // each attempt states its own failure (noteAttempt)
		res := s.dial(ctx, cl, ref)
		cl.noteAttempt(ref.ID, res.err, begun)
		if res.kind == attemptDone {
			s.ref = ref
			s.routes = cl.routes
			s.session = cl.session
			s.quotaGeneration = cl.availability[ref.ID].generation()
			cl.finishSticky(ctx, ref.ID, true)
			pendingRelease = nil
			return release, nil
		}
		release()
		res = cl.planFailure(ctx, ref, res, cl.rec.Attempts, excluded)
		cl.observeAttemptResult(ref, res)
		switch res.kind {
		case attemptFailover:
			last = res.err
			if cl.failOver(ctx, ref, res, excluded, stickyAttempt) {
				cl.finishSticky(ctx, ref.ID, false)
				return nil, last
			}
		default:
			cl.finishSticky(ctx, ref.ID, false)
			return nil, res.err
		}
	}
	if last == nil || (last.RecordType != errTypeUpstream && last.RecordType != errTypeInvalidRequest && last.RecordType != errTypeModelAvailability &&
		last.Status != http.StatusTooManyRequests) {
		// As on HTTP (dispatch): upstream answers and requests no account
		// can serve (a return_error that let the attempt move on) are
		// relayed; plugin or account problems are "no available account".
		last = fromCore(core.ErrNoAvailableAccount, errTypeNoAccount)
	}
	cl.finishSticky(ctx, 0, false)
	return nil, last
}

// dial asks the account type's plugin for the upstream handshake and opens
// it through the account's proxy.
func (s *wsSession) dial(ctx context.Context, cl *call, ref *core.AccountRef) attemptResult {
	unavailable := func(msg string, err error) attemptResult {
		slog.WarnContext(ctx, "gateway: websocket account unavailable", "account", ref.ID, "reason", msg, "err", err)
		return attemptResult{kind: attemptFailover, err: fromCore(core.ErrNoAvailableAccount.WithCause(err), errTypeNoAccount)}
	}
	acc, err := s.g.d.Accounts.Load(ctx, ref.ID)
	if err != nil {
		return unavailable("load account", err)
	}
	rt := cl.route(ref)
	if rt == nil || rt.binding.Client == nil {
		return unavailable("account type not enabled", errors.New(ref.PluginKey+"/"+ref.Type))
	}
	s.recordAccount(cl, acc.ID, rt)
	cl.env = cl.environment(&acc.AccountRef)
	x, req, target, res := s.executeTurn(ctx, cl, ref, acc, rt)
	if req == nil {
		return res
	}
	client, pacct := x.egress.Client, x.acct
	// The handshake's headers in the layers of an HTTP request
	// (layerHeaders) without the passthrough: the plugin's defaults, the
	// account's header override (§108) and parameter override, the plugin's
	// authoritative headers. The handshake ends with the client leaving
	// (present) and has no deadline of its own; its dial context is released
	// once it is done, so the connection itself lives as long as the session.
	header := http.Header{}
	layerHeaders(header, req, cl.paramOverride.headers.Apply, handshakeOwned)
	dctx, dcancel := context.WithCancel(s.present)
	up, resp, err := websocket.Dial(dctx, target.String(), &websocket.DialOptions{HTTPClient: execfetch.NoRedirects(client), HTTPHeader: header,
		CompressionMode: websocket.CompressionContextTakeover})
	dcancel()
	if resp != nil {
		s.g.observeQuota(rt, acc.ID, resp.StatusCode, resp.Header)
	}
	if err != nil {
		if s.present.Err() != nil {
			return attemptResult{kind: attemptCanceled, err: canceledErr()}
		}
		if resp != nil && resp.StatusCode != http.StatusSwitchingProtocols {
			// The rejection is the upstream's answer; an accepted
			// handshake leaves it to the turn's first message.
			started := *cl.rec
			started.StatusCode = resp.StatusCode
			cl.observeRequest(observation.ResponseStarted, &started)
			var raw []byte
			if resp.Body != nil {
				raw, _ = io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
			}
			return cl.classify(ctx, rt, pacct, resp.StatusCode, resp.Header, raw, "")
		}
		return cl.classify(ctx, rt, pacct, 0, nil, nil, err.Error())
	}
	up.SetReadLimit(-1) // an upstream message of any size (the library's default is 32 KiB)
	s.up, s.acc, s.rt, s.pacct = up, acc, rt, pacct
	s.opening = req.GetBody()
	s.upIn = make(chan wsMessage, 1)
	go s.read(ctx, up, s.upIn, func() {})
	go s.heartbeat(ctx, up)
	return attemptResult{kind: attemptDone}
}

// executeTurn asks the session's plugin for the turn of cl on ref: the
// message after the rewrites of an HTTP body (upstreamBody), the
// availability guard, then Execute. It returns the execution, whose egress
// the handshake goes out by, and the relay with the address check accepted
// for it (the handshake's, on the turn opening the connection), or, without
// one, the result saying why.
func (s *wsSession) executeTurn(ctx context.Context, cl *call, ref *core.AccountRef, acc *core.Account, rt *typeRoute) (*execution, *pluginv1.UpstreamRequest, *url.URL, attemptResult) {
	n := max(0, cl.rec.Attempts-1)
	body, upModel, failure := cl.upstreamBody(ctx, acc, ref, rt, cl.body, n)
	if failure != nil {
		return nil, nil, nil, *failure
	}
	if e := cl.guardAvailability(ctx, ref); e != nil {
		return nil, nil, nil, attemptResult{kind: attemptFailover, err: e}
	}
	x, failed := cl.newExecution(ctx, rt, acc, body)
	if failed != nil {
		return nil, nil, nil, *failed
	}
	x.turn, x.opens = true, s.up == nil
	if !x.opens {
		// The turn's frame goes out over the session's connection: the
		// plugin is shown the account as it was handed when that opened,
		// with the proxy the connection goes through. The turn's fetches
		// still take the account's egress as it stands now (§123).
		x.acct = s.pacct
	}
	res, err := x.run(ctx, upModel, n)
	if err != nil || res.Relay == nil {
		return x, nil, nil, x.unrelayed(ctx, res, err)
	}
	target, refused := x.targetOf(ctx, res.Relay)
	if refused != nil {
		return x, nil, nil, attemptResult{kind: attemptFailover, err: refused}
	}
	if m := res.Relay.GetUpstreamModel(); m != "" && m != cl.model {
		cl.rec.UpstreamModel = m
	}
	return x, res.Relay, target, attemptResult{}
}

// reuse admits a later turn on the session's account: it must still serve
// the turn's model, and its slot and rate limits must allow the turn, after
// waiting a little for them.
func (s *wsSession) reuse(ctx context.Context, cl *call) (func(), *gwError) {
	cl.routes = s.routes
	cl.session = s.session
	cl.rec.Attempts = 1
	cl.env = cl.environment(s.ref)
	s.recordAccount(cl, s.ref.ID, s.rt)
	if s.rt.binding.Type.ModelAvailability {
		refs, err := s.g.d.Accounts.Candidates(ctx, cl.principal.Group.ID, []core.AccountTypeKey{{PluginKey: s.ref.PluginKey, Type: s.ref.Type}})
		if err != nil {
			return nil, fromCore(core.ErrUnavailable.WithCause(err), errTypeNoAccount)
		}
		var current *core.AccountRef
		for i := range refs {
			if refs[i].ID == s.ref.ID {
				current = &refs[i]
				break
			}
		}
		if current == nil {
			return nil, fromCore(core.ErrNoAvailableAccount, errTypeNoAccount)
		}
		s.ref = current
	}
	if !cl.candidateEligible(s.ref) {
		return nil, &gwError{Status: http.StatusBadRequest, Code: core.ErrInvalidArgument.Code, RecordType: errTypeInvalidRequest,
			Message: "the account of this connection does not serve model " + strconvQuote(cl.model) + "; open a new connection for it"}
	}
	if _, failure := cl.filterAvailability(ctx, []core.AccountRef{*s.ref}); failure != nil {
		return nil, failure
	}
	// The connection stays on the identity it was opened with: a changed
	// generation is a reauthorized account (CONTRACTS §44.7). An unknown one
	// on either side is not adopted later either (applyQuotaObservation).
	if !sameQuotaGeneration(s.quotaGeneration, cl.availability[s.ref.ID].generation()) {
		return nil, cl.guardBlocked(ctx, s.ref, availReasonIdentity, "", cl.model, 0, availabilityFailure(false))
	}
	deadline := time.Now().Add(s.g.ws.slotWait)
	var pendingRelease func()
	defer func() {
		if pendingRelease != nil {
			pendingRelease()
		}
	}()
	for {
		release, ok := cl.acquireAccount(ctx, s.ref)
		if ok {
			pendingRelease = release
			if s.g.d.Limiter != nil {
				admitted, err := cl.admitRate(s.ref)
				if err != nil || !admitted {
					release()
					ok = false
				}
			}
		}
		if ok {
			cl.startObservationAttempt(s.ref)
			pendingRelease = nil
			return release, nil
		}
		if time.Now().After(deadline) {
			return nil, fromCore(core.ErrRateLimited.WithMessage("the account of this connection is busy or rate limited, please retry later"), errTypeRateLimited)
		}
		select {
		case <-ctx.Done():
			return nil, canceledErr()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (s *wsSession) recordAccount(cl *call, id int64, rt *typeRoute) {
	cl.rec.AccountID = &id
	cl.rec.PluginKey, cl.rec.PluginVersion = rt.binding.Plugin.Key, rt.binding.Plugin.Version
	cl.rec.AccountType = rt.binding.Type.ID
	cl.rec.UpstreamProtocol = rt.upstream
	cl.rec.UsageSemantics = rt.usage.Semantics
	if cl.rec.UsageSemantics == "" {
		cl.rec.UsageSemantics = "exclusive"
	}
}

// observe meters one upstream message of a turn and reports whether it ended
// the turn.
func (s *wsSession) observe(ctx context.Context, t *wsTurn, data []byte) (bool, wsOutcome) {
	cl := t.cl
	name := gjson.GetBytes(data, "type").String()
	// An error event's HTTP status (rate limit, quota, auth) says something
	// about the account.
	var status int
	if name == "error" {
		status = int(gjson.GetBytes(data, "status").Int())
	}
	if cl.rec.FirstTokenMs == 0 {
		cl.upstreamResponded = true
		cl.rec.FirstTokenMs = max(1, int(s.g.now().Sub(cl.start)/time.Millisecond))
		// A turn has no status of its own: its first message is the
		// upstream's answer.
		started := *cl.rec
		started.StatusCode = http.StatusOK
		if status >= 400 {
			started.StatusCode = status
		}
		cl.observeRequest(observation.ResponseStarted, &started)
		cl.observeRequest(observation.FirstStreamEvent, cl.rec)
	}
	t.usage.ApplySSE("", data)
	if !terminalEvents[name] {
		return false, wsOutcome{}
	}
	switch name {
	case "response.completed", "response.incomplete":
		return true, wsOutcome{ok: true}
	case "response.failed":
		msg := gjson.GetBytes(data, "response.error.message").String()
		if msg == "" {
			msg = "response failed"
		}
		return true, wsOutcome{status: http.StatusBadGateway, errType: errTypeUpstream, msg: "upstream response failed: " + msg}
	}
	// An error event; the plugin decides what its status means.
	local := false
	if status >= 400 {
		header := errorEventHeaders(data)
		if header != nil {
			s.g.observeQuota(s.rt, s.acc.ID, status, header)
		}
		res := cl.classify(ctx, s.rt, s.pacct, status, header, data, "")
		local = res.err != nil && res.err.LocalFailure
		if cl.failurePolicy != nil {
			cl.finishFailureDecision(s.ref, cl.rec.Attempts, "response_committed")
		}
	} else {
		cl.recordStreamFailure(ctx, s.pacct.GetId(), string(data))
		status = http.StatusBadGateway
	}
	msg := gjson.GetBytes(data, "error.message").String()
	if msg == "" {
		msg = "upstream error event"
	}
	return true, wsOutcome{status: status, errType: errTypeUpstream, local: local, msg: "upstream stream error: " + msg}
}

// errorEventHeaders reads the top-level "headers" object of an error event
// (the Codex backend wraps the upstream response headers there) as the
// error's response headers; nil when the event carries none. String and
// number values are kept, as their text.
func errorEventHeaders(data []byte) http.Header {
	obj := gjson.GetBytes(data, "headers")
	if !obj.IsObject() {
		return nil
	}
	h := http.Header{}
	obj.ForEach(func(k, v gjson.Result) bool {
		if v.Type == gjson.String || v.Type == gjson.Number {
			h.Set(k.String(), v.String())
		}
		return true
	})
	return h
}

type wsOutcome struct {
	ok      bool
	status  int
	errType string
	local   bool // ClassifyError's local_failure
	msg     string
}

// finish records and bills a turn and releases its slots.
func (s *wsSession) finish(ctx context.Context, t *wsTurn, out wsOutcome) {
	defer t.release()
	cl := t.cl
	rec := cl.rec
	rec.Tokens = t.usage.Tokens()
	if len(t.usage.Metrics) > 0 {
		rec.Metrics = t.usage.Metrics
	}
	if rec.UpstreamModel == "" && t.usage.Model != "" && t.usage.Model != cl.model {
		rec.UpstreamModel = t.usage.Model
	}
	if out.ok {
		rec.Success, rec.StatusCode, rec.ErrorType, rec.ErrorMessage = true, http.StatusOK, "", ""
	} else {
		rec.Success, rec.StatusCode, rec.ErrorType = false, out.status, out.errType
		if rec.ErrorMessage == "" {
			rec.ErrorMessage = truncateUTF8(out.msg, 1000)
		}
	}
	t.release()
	id := s.ref
	if id != nil {
		// Before the observation, as sendParked does: an unreported turn is
		// observed as the failure it is recorded as.
		cl.endUnreported(ctx, id.ID, cl.readingEmpty())
	}
	cl.publishObservation(observation.AttemptFinished, rec, !rec.Success && isLocalFailure(out.local, rec.ErrorType))
	// Only a turn the client abandoned counts the prompt when no usage came
	// back; an upstream failure consumed nothing we know of.
	if out.errType != errTypeClientCanceled {
		cl.rateEstimate.prompt = 0
	}
	defer cl.releaseHolds(ctx)
	if id != nil {
		cl.countTokens(ctx, id.ID)
		if rec.Success {
			s.g.d.Accounts.TouchLastUsed(context.WithoutCancel(ctx), id.ID)
		}
	}
	cl.submit()
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(truncateUTF8(s, 100))
	return string(bytes.TrimSpace(b))
}

// handshakeOwned reports the headers of a WebSocket handshake the transport
// writes (framing and the WebSocket handshake headers): a plugin's are left
// out.
func handshakeOwned(name string) bool {
	switch strings.ToLower(name) {
	case "content-length", "content-type", "accept-encoding", "connection", "transfer-encoding", "upgrade",
		"sec-websocket-key", "sec-websocket-version", "sec-websocket-extensions", "sec-websocket-accept":
		return true
	}
	return false
}
