package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

// responsesWebSocket is the Responses WebSocket mode: GET /v1/responses
// upgraded, then every {"type":"response.create",...} message is answered
// with the same events a streaming POST /v1/responses sends, as WebSocket
// text messages. The handshake and every message are recorded (method GET
// and WS). A behaviour status rejects the handshake when the connection
// opens and answers a turn with an "error" event later; a key rule's
// remaining count is spent by both.
func (s *server) responsesWebSocket(w http.ResponseWriter, r *http.Request) {
	id := s.record(r, nil, "", true, "")
	if b := s.resolve(r, nil); b.status >= 400 {
		s.setStatus(id, b.status)
		writeOpenAIMockError(w, b.status)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionContextTakeover})
	if err != nil {
		s.setStatus(id, 400)
		return
	}
	s.setStatus(id, 101)
	conn.SetReadLimit(32 << 20)
	defer conn.CloseNow()
	ctx := context.Background()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if !s.responsesTurn(ctx, conn, r, data) {
			return
		}
	}
}

func (s *server) responsesTurn(ctx context.Context, conn *websocket.Conn, r *http.Request, data []byte) bool {
	var req openaiRequest
	_ = json.Unmarshal(data, &req)
	turn := r.Clone(ctx)
	turn.Method = "WS"
	id := s.record(turn, data, req.Model, true, "")
	b := s.resolve(r, req.Metadata)
	emit := func(fields map[string]any) bool {
		raw, _ := json.Marshal(fields)
		wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return conn.Write(wctx, websocket.MessageText, raw) == nil
	}
	pause := func(d time.Duration) { time.Sleep(d) }
	pause(b.delay)
	if b.status >= 400 {
		s.setStatus(id, b.status)
		return emit(map[string]any{"type": "error", "status": b.status,
			"error": map[string]any{"type": openaiErrorType(b.status), "code": nil, "message": fmt.Sprintf("mock: status %d", b.status)}})
	}
	if req.Model == "" {
		s.setStatus(id, 400)
		return emit(map[string]any{"type": "error", "status": 400,
			"error": map[string]any{"type": "invalid_request_error", "message": "mock: missing required parameter: 'model'"}})
	}
	s.setStatus(id, 200)
	respID := fmt.Sprintf("resp_mock_%d_%06d", id, rand.IntN(1000000))
	msgID := fmt.Sprintf("msg_mock_%d", id)
	created := time.Now().Unix()
	seq := 0
	ev := func(typ string, fields map[string]any) bool {
		fields["type"] = typ
		fields["sequence_number"] = seq
		seq++
		if !emit(fields) {
			return false
		}
		pause(b.chunkDelay)
		return true
	}
	inProgress := func() map[string]any {
		return responsesEnvelope(respID, created, req.Model, "in_progress", []any{}, nil)
	}
	if !ev("response.created", map[string]any{"response": inProgress()}) ||
		!ev("response.in_progress", map[string]any{"response": inProgress()}) {
		return false
	}
	for _, part := range chunks(b.text) {
		if !ev("response.output_text.delta", map[string]any{"item_id": msgID, "output_index": 0, "content_index": 0, "delta": part}) {
			return false
		}
	}
	output := []any{responsesMessageItem(msgID, "completed", []any{responsesTextPart(b.text)})}
	tools, image := imageToolOf(data, "ig_"+msgID)
	if image != nil {
		output = append(output, image)
		if !ev("response.output_item.done", map[string]any{"output_index": 1, "item": image}) {
			return false
		}
	}
	final := responsesEnvelope(respID, created, req.Model, "completed", output, openaiResponsesUsage(b.usage))
	if tools != nil {
		withImageTool(final, tools, image)
	}
	return ev("response.completed", map[string]any{"response": final})
}

func openaiErrorType(status int) string {
	switch {
	case status == 401:
		return "authentication_error"
	case status == 403:
		return "permission_error"
	case status == 429:
		return "rate_limit_error"
	case status >= 500:
		return "server_error"
	}
	return "invalid_request_error"
}
