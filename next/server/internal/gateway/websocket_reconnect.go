package gateway

import (
	"context"
	"net/http"
	"sync/atomic"

	"github.com/coder/websocket"
)

// An upstream WebSocket connection may end before its client's (Responses
// WebSocket: the upstream's 60-minute connection limit, CONTRACTS §142). The
// session then forgets it and keeps the client connection: the next turn
// opens a new upstream connection the way the first did (connect: scheduling,
// sticky binding, handshake). The core never sends a turn again on the
// client's behalf; whatever the upstream said about the turn (its error
// event) has been relayed like any other message.

// dropUpstream closes the session's upstream connection, if any, and forgets
// it; the next turn connects again.
func (s *wsSession) dropUpstream() {
	if s.up != nil {
		_ = s.up.CloseNow()
	}
	s.up, s.upIn, s.upEnded = nil, nil, nil
}

// upstreamLost handles the end of the upstream connection: a turn still in
// progress fails as an upstream failure and the client is told so in an
// error event; the client connection stays open for the next turn.
func (s *wsSession) upstreamLost(ctx context.Context, turn *wsTurn, err error) {
	s.dropUpstream()
	if turn == nil {
		return
	}
	s.finish(ctx, turn, wsOutcome{status: http.StatusBadGateway, errType: errTypeUpstream,
		msg: "upstream connection closed: " + truncateUTF8(err.Error(), 300)})
	s.sendError(ctx, &gwError{Status: http.StatusBadGateway, Code: codeUpstreamError,
		Message: "upstream connection closed; send the request again"})
}

// watchUpstream starts reading the session's new upstream connection up into
// s.upIn and pinging it. upEnded is set as soon as a read fails, before the
// session loop hears of it, so that a turn starting in between connects
// again instead of writing to a connection that has ended.
func (s *wsSession) watchUpstream(ctx context.Context, up *websocket.Conn) {
	ended := &atomic.Bool{}
	s.upIn, s.upEnded = make(chan wsMessage, 1), ended
	go s.read(ctx, up, s.upIn, func() { ended.Store(true) })
	go s.heartbeat(ctx, up)
}

// upstreamOpen reports whether the session has an upstream connection a
// turn can go out on; one that has ended is dropped first.
func (s *wsSession) upstreamOpen() bool {
	if s.up != nil && s.upEnded.Load() {
		s.dropUpstream()
	}
	return s.up != nil
}
