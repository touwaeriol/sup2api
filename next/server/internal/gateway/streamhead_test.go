package gateway

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

// ---------------------------------------------------------------- holding the head of a stream (CONTRACTS §138)

// overloadedEvent is the error event the ChatGPT backends sent on 2026-11-25.
var overloadedEvent = map[string]any{"type": "error", "sequence_number": 4, "error": map[string]any{
	"type": "service_unavailable_error", "code": "server_is_overloaded", "param": nil,
	"message": "Our servers are currently overloaded. Please try again later."}}

// headEnv is a streaming Responses route on account 10 alone whose next n
// upstream streams send the head of an overloaded ChatGPT backend stream and
// end, with ev when it is set.
func headEnv(t *testing.T, n int, ev map[string]any) (*env, *fakePlatform, *responsesUpstream) {
	t.Helper()
	e, plat, up := streamOnlyEnv(t, openAIStreamOnly())
	e.accounts.groups[testGroup] = []int64{10}
	up.early, up.earlyEvent = n, ev
	return e, plat, up
}

func (u *responsesUpstream) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.bodies)
}

// An error event before any content moves the request to another account;
// the client sees only the answer that worked.
func TestStreamHeadErrorFailsOver(t *testing.T) {
	e, _, up := headEnv(t, 1, overloadedEvent)
	e.accounts.addTyped(testGroup, 11, 100, "codex-11", "codexlike", "chatgpt")
	r := e.responses(true)
	body := string(r.body)
	if r.status != 200 || !strings.Contains(body, "event: response.created") || !strings.Contains(body, "event: response.completed") ||
		strings.Contains(body, "server_is_overloaded") || strings.Count(body, "event: response.created") != 1 {
		t.Fatalf("response: %d %s", r.status, body)
	}
	if up.count() != 2 {
		t.Fatalf("upstream requests %d", up.count())
	}
	rec := e.record()
	if !rec.Success || !rec.Stream || rec.Tokens.Output != soOutput || rec.AttemptTrail == nil ||
		len(rec.AttemptTrail.Steps) != 2 || rec.AttemptTrail.Steps[0].Status != http.StatusInternalServerError {
		t.Fatalf("record %+v trail %+v", rec, rec.AttemptTrail)
	}
}

// With nowhere to go the error event is an HTTP 500 carrying its error.
func TestStreamHeadErrorReturns500(t *testing.T) {
	e, plat, up := headEnv(t, 1, overloadedEvent)
	r := e.responses(true)
	if r.status != 500 || !strings.HasPrefix(r.header.Get("Content-Type"), "application/json") ||
		r.json().Get("error.code").String() != "server_is_overloaded" ||
		!strings.Contains(r.json().Get("error.message").String(), "overloaded") {
		t.Fatalf("response: %d %s %s", r.status, r.header.Get("Content-Type"), r.body)
	}
	if up.count() != 1 || len(plat.classify) != 1 || plat.classify[0].GetStatus() != 500 ||
		gjson.GetBytes(plat.classify[0].GetBodyPrefix(), "error.code").String() != "server_is_overloaded" {
		t.Fatalf("classify %+v", plat.classify)
	}
	rec := e.record()
	if rec.Success || !rec.Stream || rec.StatusCode != 500 || rec.ErrorType != errTypeUpstream ||
		!strings.Contains(rec.ErrorMessage, "overloaded") {
		t.Fatalf("record %+v", rec)
	}
}

// response.failed before any content is a 500 too, whatever it names.
func TestStreamHeadResponseFailed(t *testing.T) {
	failed := soResponse("failed", []any{})
	failed["error"] = map[string]any{"code": "rate_limit_exceeded", "message": "slow down"}
	e, _, _ := headEnv(t, 1, map[string]any{"type": "response.failed", "status": 429, "response": failed})
	r := e.responses(true)
	if r.status != 500 || r.json().Get("error.code").String() != "rate_limit_exceeded" {
		t.Fatalf("response: %d %s", r.status, r.body)
	}
	if rec := e.record(); rec.Success || rec.StatusCode != 500 {
		t.Fatalf("record %+v", rec)
	}
}

// A stream that ends before any content is a broken answer: a transport
// error, never a 200 without an answer.
func TestStreamHeadUnfinished(t *testing.T) {
	e, plat, _ := headEnv(t, 1, nil)
	r := e.responses(true)
	if r.status != 502 {
		t.Fatalf("response: %d %s", r.status, r.body)
	}
	if len(plat.classify) != 1 || plat.classify[0].GetStatus() != 0 ||
		!strings.Contains(plat.classify[0].GetTransportError(), errStreamHeadUnfinished.Error()) {
		t.Fatalf("classify %+v", plat.classify)
	}
	e.record()
}

// The held head is relayed byte for byte ahead of the rest.
func TestStreamHeadRelaysUnchanged(t *testing.T) {
	e, _, up := headEnv(t, 0, nil)
	r := e.responses(true)
	resp, err := http.Post(up.srv.URL+"/v1/responses", "application/json", strings.NewReader(`{"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	direct, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if r.status != 200 || !bytes.Equal(r.body, direct) {
		t.Fatalf("relayed %d:\n%s\nupstream:\n%s", r.status, r.body, direct)
	}
	if rec := e.record(); !rec.Success || rec.FirstTokenMs == 0 {
		t.Fatalf("record %+v", rec)
	}
}

// An error event after content stays in the stream, as before.
func TestStreamHeadLaterErrorStaysInStream(t *testing.T) {
	e, _, up := headEnv(t, 0, nil)
	up.terminal = "error_flat"
	r := e.responses(true)
	if r.status != 200 || !strings.Contains(string(r.body), "flat error") {
		t.Fatalf("response: %d %s", r.status, r.body)
	}
	if rec := e.record(); rec.Success || rec.ErrorMessage != "upstream stream error: flat error" {
		t.Fatalf("record %+v", rec)
	}
}

func TestReadStreamHead(t *testing.T) {
	rule := streamHeads[protocolResponses]
	// The head of a ChatGPT backend stream as it was seen on 2026-11-25.
	const head = ": comment\n\nevent: response.created\ndata: {\"type\":\"response.created\"}\n\n" +
		"data: {\"type\":\"response.in_progress\"}\n\nevent: keepalive\ndata: {\"type\":\"keepalive\"}\n\n" +
		"event: response.metadata\ndata: {\"type\":\"response.metadata\"}\n\n"
	t.Run("content", func(t *testing.T) {
		in := head + "event: response.output_item.added\ndata: {}\n\nevent: response.completed\ndata: {}\n\n"
		r := newSSEReader(strings.NewReader(in), headBufferSize, true)
		if f := readStreamHead(r, rule); f.broken != nil || f.errBody != nil {
			t.Fatalf("failure %+v", f)
		}
		if out, _ := io.ReadAll(r.rest()); string(out) != in {
			t.Fatalf("rest %q", out)
		}
	})
	t.Run("error event", func(t *testing.T) {
		ev := `{"type":"error","error":{"type":"service_unavailable_error","code":"server_is_overloaded","message":"busy","param":null}}`
		f := readStreamHead(newSSEReader(strings.NewReader(head+"event: error\ndata: "+ev), headBufferSize, true), rule)
		if f.status != 500 || f.event != "error" || string(f.data) != ev ||
			gjson.GetBytes(f.errBody, "error.code").String() != "server_is_overloaded" {
			t.Fatalf("failure %+v %s", f, f.errBody)
		}
	})
	t.Run("completed without output", func(t *testing.T) {
		in := head + "event: response.completed\ndata: {}\n\n"
		if f := readStreamHead(newSSEReader(strings.NewReader(in), headBufferSize, true), rule); f.broken != nil || f.errBody != nil {
			t.Fatalf("failure %+v", f)
		}
	})
	t.Run("delta without announcement", func(t *testing.T) {
		in := head + "event: response.output_text.delta\ndata: {}\n\nevent: response.done\ndata: {}\n\n"
		r := newSSEReader(strings.NewReader(in), headBufferSize, true)
		if f := readStreamHead(r, rule); f.broken != nil || f.errBody != nil {
			t.Fatalf("failure %+v", f)
		}
		if out, _ := io.ReadAll(r.rest()); string(out) != in {
			t.Fatalf("rest %q", out)
		}
	})
	t.Run("unnamed error chunk", func(t *testing.T) {
		f := readStreamHead(newSSEReader(strings.NewReader(head+`data: {"error":{"message":"busy"}}`+"\n\n"), headBufferSize, true), rule)
		if f.status != 500 || gjson.GetBytes(f.errBody, "error.message").String() != "busy" {
			t.Fatalf("failure %+v %s", f, f.errBody)
		}
	})
	t.Run("ended", func(t *testing.T) {
		if f := readStreamHead(newSSEReader(strings.NewReader(head), headBufferSize, true), rule); f.broken != errStreamHeadUnfinished {
			t.Fatalf("failure %+v", f)
		}
	})
}

func TestSSEReaderLastEventWithoutBlankLine(t *testing.T) {
	r := newSSEReader(strings.NewReader("event: a\ndata: 1\n\n:c\n\ndata: x\ndata: y"), headBufferSize, false)
	for _, want := range []string{"a|1", "|x\ny"} {
		ev, data, err := r.next()
		if err != nil || ev+"|"+string(data) != want {
			t.Fatalf("got %q %q %v, want %q", ev, data, err, want)
		}
	}
	if _, _, err := r.next(); err != io.EOF {
		t.Fatalf("end: %v", err)
	}
}

// A Chat Completions stream adapted onto a Responses upstream is held the
// same way: an early error event is a 500 with the upstream's error.
func TestStreamHeadAdaptedChat(t *testing.T) {
	e, _, up := sameChatEnv(t, openAIStreamOnly())
	up.early, up.earlyEvent = 1, overloadedEvent
	r := e.chat(true)
	if r.status != 500 || !strings.Contains(r.json().Get("error.message").String(), "overloaded") {
		t.Fatalf("response: %d %s", r.status, r.body)
	}
	if rec := e.record(); rec.Success || rec.StatusCode != 500 || rec.Protocol != "openai.chat" {
		t.Fatalf("record %+v", rec)
	}
}

// A response.failed after content stays in the stream and is no stream error
// of the record (it is only logged).
func TestStreamHeadLaterResponseFailed(t *testing.T) {
	e, _, up := headEnv(t, 0, nil)
	up.terminal = "failed"
	r := e.responses(true)
	if r.status != 200 || !strings.Contains(string(r.body), "the model melted") {
		t.Fatalf("response: %d %s", r.status, r.body)
	}
	if rec := e.record(); rec.StatusCode != 200 || strings.HasPrefix(rec.ErrorMessage, "upstream stream error") {
		t.Fatalf("record %+v", rec)
	}
}

// The hold ends as soon as the answer starts: it does not wait for more of
// the stream.
func TestReadStreamHeadReleasesAtAnswer(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	go func() {
		_, _ = io.WriteString(pw, "event: response.created\ndata: {}\n\nevent: keepalive\ndata: {}\n\n"+
			"event: response.output_item.added\ndata: {}\n\n")
	}()
	done := make(chan streamFailure, 1)
	go func() { done <- readStreamHead(newSSEReader(pr, headBufferSize, true), streamHeads[protocolResponses]) }()
	select {
	case f := <-done:
		if f.broken != nil || f.errBody != nil {
			t.Fatalf("failure %+v", f)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the head was still held after the answer started")
	}
}
