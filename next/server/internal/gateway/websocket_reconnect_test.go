package gateway

import (
	"encoding/json"
	"testing"
)

// connectionLimitEvent is the Responses WebSocket error at the upstream's
// 60-minute connection limit (codex-rs
// codex-api/src/endpoint/responses_websocket.rs).
var connectionLimitEvent = map[string]any{"type": "error", "status": 400, "error": map[string]any{
	"type": "invalid_request_error", "code": "websocket_connection_limit_reached",
	"message": "Responses websocket connection limit reached (60 minutes). Create a new websocket connection to continue."}}

// An upstream connection ending at its limit ends only itself: the turn's
// error event reaches the client unchanged, the client connection stays and
// the next turn opens a new upstream connection; nothing is sent again on
// the client's behalf (CONTRACTS §142).
func TestWebSocketConnectionLimitReconnectsNextTurn(t *testing.T) {
	e := newWSEnv(t)
	c := e.mustDial()
	c.send(create(wsModel, "ok"))
	c.until("response.completed")
	e.record()
	c.send(create(wsModel, "connection-limit"))
	ev := c.until("error")
	if want, _ := json.Marshal(connectionLimitEvent); ev[0].Raw != string(want) {
		t.Fatalf("error event = %s, want %s", ev[0].Raw, want)
	}
	if r := e.record(); r.Success || r.StatusCode != 400 || r.ErrorType != errTypeUpstream {
		t.Fatalf("turn at the limit: %+v", r)
	}
	waitFor(t, func() bool { return e.wsUp.open.Load() == 0 })
	c.send(create(wsModel, "after"))
	c.until("response.completed")
	if r := e.record(); !r.Success || r.AccountID == nil || *r.AccountID != 11 {
		t.Fatalf("turn after the limit: %+v", r)
	}
	keys, msgs := e.wsUp.snapshot()
	if len(keys) != 2 || len(msgs) != 3 {
		t.Fatalf("handshakes %v, messages %d: want a second handshake and no message sent again", keys, len(msgs))
	}
}

// An upstream connection dropping during a turn fails the turn with an error
// event; the client connection stays and the next turn connects again.
func TestWebSocketUpstreamDropFailsOnlyTheTurn(t *testing.T) {
	e := newWSEnv(t)
	c := e.mustDial()
	c.send(create(wsModel, "drop"))
	if ev := c.until("error"); ev[0].Get("status").Int() != 502 || ev[0].Get("error.code").String() != codeUpstreamError {
		t.Fatalf("turn of a dropped upstream: %v", ev)
	}
	if r := e.record(); r.Success || r.ErrorType != errTypeUpstream {
		t.Fatalf("dropped turn: %+v", r)
	}
	c.send(create(wsModel, "ok"))
	c.until("response.completed")
	if r := e.record(); !r.Success {
		t.Fatalf("turn after the upstream dropped: %+v", r)
	}
	if keys, _ := e.wsUp.snapshot(); len(keys) != 2 {
		t.Fatalf("handshakes = %v, want a second one after the drop", keys)
	}
}
