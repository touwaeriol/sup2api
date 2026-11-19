package gateway

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/observation"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/gateway/convert"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/usagerules"
)

// convertError is a failure converting the upstream response to the client
// protocol.
type convertError struct{ err error }

func (e *convertError) Error() string { return "response conversion failed: " + e.err.Error() }
func (e *convertError) Unwrap() error { return e.err }

// forward relays a successful upstream response. From here on bytes reach
// the client, so the attempt is final whatever happens. On a converting
// route the response is converted to the endpoint protocol; usage is always
// read from the upstream response with the upstream protocol's rules.
//
// The declarative rules run here in every case. A relay that waits passes
// a capture (newUsageCapture; nil when the endpoint's usage is the core's)
// that keeps what its observation shows the plugin; the plugin reads it in
// the answer to its RelayUpstream call once forward has returned
// (waited.go) - never before. A relay that does not wait passes nil: no
// plugin reads its answer. upBody is the request as it went upstream; only
// its declared usageRequestFields are read from it, at the end, and the
// body itself is not kept.
func (c *call) forward(ctx context.Context, rt *typeRoute, acct *pluginv1.Account, resp *http.Response, upBody []byte, cap *usageCapture) attemptResult {
	core.ApplyProviderHeaders(c.c.Writer.Header(), resp.Header)
	u := newUsageAcc(rt.usage).WithLog("request_id", c.rid, "plugin", rt.binding.Plugin.Key,
		"platform", rt.platform, "protocol", rt.upstream)
	u.WithPrimaryModel(c.upstreamPrimaryModel)
	u.WithAttemptAccounting(c.hasRequestedAttemptUsage(rt, upBody))
	c.upstreamResponded = true
	c.rec.StatusCode = resp.StatusCode
	c.rec.Success = true
	c.rec.ErrorType = ""
	c.rec.ErrorMessage = ""
	var err error
	sse := isSSE(resp.Header.Get("Content-Type"))
	c.checkResponseShape(ctx, rt, resp, sse)
	switch {
	case sse:
		c.drain.arm()
		err = c.forwardSSE(ctx, resp, u, rt.conv, cap)
	case rt.conv != nil:
		c.drain.arm()
		err = c.forwardJSONConverted(resp, u, cap, rt.conv)
	default:
		c.drain.arm()
		err = c.forwardJSON(resp, u, cap)
	}
	switch {
	case c.drain.served() && (err == nil || clientGone(ctx, err) ||
		errors.Is(context.Cause(resp.Request.Context()), errStreamIdleTimeout)):
		// The client had the final usage before it left, or before an
		// upstream that kept the stream open went idle: whatever it missed
		// after that, it was served.
		err = nil
	case err == nil && c.drain.outputDropped():
		// Nobody received the end of it. An upstream that finished has
		// reported its usage in full, which is billed as it stands
		// (settleCanceled); one that ended on an error event has not, and the
		// request falls back to the estimate like any other hang-up.
		c.drained = u.StreamError == ""
		err = errClientGone
	}
	c.rec.Tokens = u.Tokens()
	c.recordAdditionalUsage(u, rt)
	c.recordReplacementUsage(u, rt)
	if len(u.Metrics) > 0 {
		c.rec.Metrics = u.Metrics
	}
	if u.Model != "" && (rt.echoModel || c.rec.UpstreamModel == "" && u.Model != c.model) {
		c.rec.UpstreamModel = u.Model
	}
	var cerr *convertError
	switch {
	case err == nil:
		if u.StreamError != "" {
			c.rec.Success = false
			c.rec.ErrorType = errTypeUpstream
			c.rec.ErrorMessage = truncateUTF8(u.StreamError, 1000)
		}
	case errors.As(err, &cerr):
		c.rec.Success = false
		c.rec.ErrorType = errTypeUpstream
		c.rec.ErrorMessage = truncateUTF8(err.Error(), 1000)
		slog.WarnContext(ctx, "gateway: response conversion failed", "request_id", c.rid,
			"from", rt.upstream, "to", c.ep.Protocol, "err", cerr.err)
		if !c.c.Writer.Written() {
			c.rec.StatusCode = http.StatusBadGateway
			writeError(c.c, c.format, &gwError{Status: http.StatusBadGateway, Code: "upstream_error",
				Message: "upstream response could not be converted"})
		}
	case errors.Is(err, errSpool):
		c.rec.Success = false
		c.rec.ErrorType = errTypeInternal
		c.rec.ErrorMessage = truncateUTF8(err.Error(), 1000)
	case clientGone(ctx, err):
		c.rec.Success = false
		c.rec.ErrorType = errTypeClientCanceled
		c.rec.StatusCode = statusClientClosed
		c.rec.ErrorMessage = "client canceled"
	default:
		// The client is still here: a timeout, or the executing plugin giving
		// up, that canceled the upstream request names itself (dispatch.go
		// cancels it with a cause).
		if cause := context.Cause(resp.Request.Context()); errors.Is(cause, errRelayTimeout) || errors.Is(cause, errStreamIdleTimeout) || errors.Is(cause, errExecutionAborted) {
			err = cause
		}
		c.rec.Success = false
		c.rec.ErrorType = errTypeUpstream
		c.rec.ErrorMessage = truncateUTF8("upstream stream interrupted: "+err.Error(), 1000)
		slog.WarnContext(ctx, "gateway: upstream response interrupted", "request_id", c.rid, "err", err)
	}
	// The answer is written (or, for a relay that waits, held back until
	// its facts commit: waited.go). The plugin reading the usage is handed
	// the observation armed below only after forward returned.
	if u.StreamError != "" {
		c.recordStreamFailure(ctx, acct.GetId(), u.StreamError)
	}
	c.armUsageExtraction(rt, acct, resp, cap, upBody)
	return attemptResult{kind: attemptDone}
}

var errClientGone = errors.New("client connection closed")

// clientGone reports whether err ended a relay because the client left.
func clientGone(ctx context.Context, err error) bool {
	return ctx.Err() != nil || errors.Is(err, errClientGone)
}

func isSSE(ct string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(ct)), "text/event-stream")
}

// sseFields are the field names an event stream can open with (a ':' comment
// is checked apart). A JSON body never starts with one of them.
var sseFields = [][]byte{[]byte("data:"), []byte("event:"), []byte("id:"), []byte("retry:")}

// Verdicts of sseVerdict.
const (
	sseNo      = -1
	sseUnknown = 0
	sseYes     = 1
)

// maxSSESniff bounds how much of a mislabeled body sniffEventStream looks at.
const maxSSESniff = 16

// sniffEventStream relabels the answer to a streaming request as
// text/event-stream when the upstream sent an event stream under another
// Content-Type (Sign in with ChatGPT's Responses API streams with
// application/json). Without it the stream would take the JSON path: held
// back from the client until it ends, and no usage read from it. The bytes
// looked at stay in the body; it only waits for bytes the stream sends anyway.
func sniffEventStream(resp *http.Response) {
	if isSSE(resp.Header.Get("Content-Type")) {
		return
	}
	br := bufio.NewReader(resp.Body)
	resp.Body = struct {
		io.Reader
		io.Closer
	}{br, resp.Body}
	for n := 1; n <= maxSSESniff; n++ {
		b, err := br.Peek(n)
		switch sseVerdict(b) {
		case sseYes:
			resp.Header.Set("Content-Type", "text/event-stream")
			return
		case sseNo:
			return
		}
		if err != nil {
			return
		}
	}
}

// sseVerdict tells from the first bytes of a body whether it is an event
// stream, is not, or cannot tell yet.
func sseVerdict(b []byte) int {
	t := bytes.TrimLeft(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), " \t\r\n")
	if len(t) == 0 {
		return sseUnknown
	}
	if t[0] == ':' {
		return sseYes
	}
	verdict := sseNo
	for _, f := range sseFields {
		switch {
		case bytes.HasPrefix(t, f):
			return sseYes
		case bytes.HasPrefix(f, t):
			verdict = sseUnknown
		}
	}
	return verdict
}

// checkResponseShape compares the shape the upstream actually answered with
// against the one the upstream endpoint promises in endpoint.response, and
// records the disagreement without acting on it.
//
// Two different things are declared per endpoint and they must not be
// conflated:
//
//   - request.stream / request.streamPath is what the *client* asks for - an
//     endpoint that always streams, or the body path carrying the client's
//     "stream": true. The gateway reads it before the request goes out
//     (checkModel) and records it as usage_logs.stream.
//   - response.stream / response.nonStream is the shape the endpoint
//     *promises* to answer with. Nothing forces the upstream to keep that
//     promise, so it is only ever a declaration to check against.
//
// The forwarding mode itself still follows the upstream Content-Type
// (forward): a client whose request works must not be cut off because a
// manifest is incomplete. But the declaration is what the usage rules are
// written for - an endpoint that never mentions response.stream almost never
// carries usage.sse rules either, so an unexpected SSE answer is billed by
// whatever facts happen to apply and its token counts are lost. Recording the
// mismatch is what makes that visible; correcting it (adding the missing
// usage.sse rules) stays with the plugin author.
//
// Nothing here blocks the response or changes the amount billed.
func (c *call) checkResponseShape(ctx context.Context, rt *typeRoute, resp *http.Response, sse bool) {
	if !rt.respDeclared || c.rec == nil {
		return
	}
	var mismatch string
	switch {
	case sse && rt.resp.Stream == "":
		mismatch = core.ResponseMismatchSSENotDeclared
	case !sse && rt.resp.NonStream == "" && rt.resp.Stream != "":
		mismatch = core.ResponseMismatchJSONWhileStream
	default:
		return
	}
	c.rec.ResponseMismatch = mismatch
	slog.WarnContext(ctx, "gateway: upstream response shape not declared by the endpoint",
		"request_id", c.rid, "plugin", rt.binding.Plugin.Key, "platform", rt.platform,
		"protocol", rt.upstream, "endpoint", c.ep.Path, "mismatch", mismatch,
		"content_type", resp.Header.Get("Content-Type"),
		"declared_stream", rt.resp.Stream, "declared_non_stream", rt.resp.NonStream)
}

// forwardJSON relays a JSON answer as it arrives and keeps all of it, so
// the usage is read from the whole answer whatever its size. The answer is
// held once, in buf, until the usage is read; the plugin's capture keeps a
// reference to it.
func (c *call) forwardJSON(resp *http.Response, u *usageAcc, cap *usageCapture) error {
	w := c.relayWriter()
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/json"
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(resp.StatusCode)
	var buf bytes.Buffer
	chunk := make([]byte, 32<<10)
	for {
		n, rerr := resp.Body.Read(chunk)
		if n > 0 {
			if _, werr := w.Write(chunk[:n]); werr != nil {
				return relayWriteError(werr)
			}
			buf.Write(chunk[:n])
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			if c.drain.outputDropped() {
				return errClientGone
			}
			return rerr
		}
	}
	w.Flush()
	u.ApplyJSON(buf.Bytes())
	cap.setBody(buf.Bytes())
	return nil
}

// forwardJSONConverted reads the whole upstream response, extracts usage
// from it (upstream rules) and writes the converted body. Nothing is written
// when reading or converting fails.
func (c *call) forwardJSONConverted(resp *http.Response, u *usageAcc, cap *usageCapture, conv convert.Converter) error {
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	u.ApplyJSON(raw)
	cap.setBody(raw)
	out, err := conv.Response(raw)
	if err != nil {
		return &convertError{err: err}
	}
	w := c.relayWriter()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if _, err := w.Write(out); err != nil {
		return relayWriteError(err)
	}
	w.Flush()
	return nil
}

// forwardSSE relays the event stream, flushing at every event boundary and
// whenever the upstream has nothing buffered, and extracts usage from the
// upstream events on the way. Without a converter lines pass through
// verbatim; with one each upstream event goes through the stream converter
// and its output events are written instead. When the client asked for a
// JSON array stream (jsonArrayStream) the event data are written as the
// elements of one JSON array instead of SSE.
func (c *call) forwardSSE(ctx context.Context, resp *http.Response, u *usageAcc, conv convert.Converter, cap *usageCapture) error {
	arr := c.jsonArrayStream()
	w := c.relayWriter()
	h := w.Header()
	if arr {
		h.Set("Content-Type", "application/json; charset=utf-8")
	} else {
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
	}
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(resp.StatusCode)
	w.Flush()

	var sc convert.StreamConverter
	if conv != nil {
		sc = conv.NewStream()
	}
	passthrough := sc == nil && !arr
	var out []byte
	elements := 0
	emit := func(evs []convert.Event, err error) error {
		if err != nil {
			return &convertError{err: err}
		}
		out = out[:0]
		for _, ev := range evs {
			if !arr {
				out = convert.AppendSSE(out, ev)
				continue
			}
			d := bytes.TrimSpace(ev.Data)
			if len(d) == 0 || string(d) == "[DONE]" {
				continue
			}
			if elements == 0 {
				out = append(out, '[')
			} else {
				out = append(out, ",\r\n"...)
			}
			elements++
			out = append(out, d...)
		}
		if len(out) == 0 {
			return nil
		}
		if _, werr := w.Write(out); werr != nil {
			return errClientGone
		}
		w.Flush()
		return nil
	}
	closeArray := func() error {
		if !arr {
			return nil
		}
		end := "]"
		if elements == 0 {
			end = "[]"
		}
		if _, werr := w.Write([]byte(end)); werr != nil {
			return errClientGone
		}
		return nil
	}

	br := bufio.NewReaderSize(resp.Body, 64<<10)
	var event string
	var data []byte
	dispatch := func() error {
		if event == "" && len(data) == 0 {
			return nil
		}
		c.observeStreamEvent(u, cap, event, data)
		var err error
		switch {
		case sc != nil:
			err = emit(sc.Event(convert.Event{Name: event, Data: data}))
		case arr:
			err = emit([]convert.Event{{Name: event, Data: data}}, nil)
		}
		event, data = "", data[:0]
		return err
	}
	for {
		line, rerr := readLine(br)
		if len(line) > 0 {
			if passthrough {
				if _, werr := w.Write(line); werr != nil {
					return errClientGone
				}
			}
			t := bytes.TrimRight(line, "\r\n")
			switch {
			case len(t) == 0:
				if err := dispatch(); err != nil {
					return err
				}
			case bytes.HasPrefix(t, []byte("event:")):
				event = strings.TrimSpace(string(t[len("event:"):]))
			case bytes.HasPrefix(t, []byte("data:")):
				d := t[len("data:"):]
				if len(d) > 0 && d[0] == ' ' {
					d = d[1:]
				}
				if len(data) > 0 {
					data = append(data, '\n')
				}
				data = append(data, d...)
			}
			if passthrough && (len(t) == 0 || br.Buffered() == 0) {
				w.Flush()
			}
			// An event ends at its blank line, and has been flushed by now
			// in every framing (emit flushes its own output).
			if len(t) == 0 && u.Final() {
				c.drain.serve(ctx)
			}
		}
		if rerr != nil {
			derr := dispatch()
			if sc != nil && derr == nil {
				derr = emit(sc.Flush())
			}
			if derr == nil && rerr == io.EOF {
				// An interrupted stream leaves the array open, so the
				// client notices the truncation.
				derr = closeArray()
			}
			w.Flush()
			if derr != nil {
				return derr
			}
			if rerr == io.EOF {
				return nil
			}
			if ctx.Err() != nil || c.drain.outputDropped() {
				return errClientGone
			}
			return rerr
		}
	}
}

// observeStreamEvent records one relayed event: the first-token time, the
// first stream observation, usage, and the plugin's usage capture.
func (c *call) observeStreamEvent(u *usageAcc, cap *usageCapture, event string, data []byte) {
	if c.rec.FirstTokenMs == 0 && len(data) > 0 {
		c.rec.FirstTokenMs = max(1, int(c.g.now().Sub(c.start)/time.Millisecond))
	}
	if c.g.d.Observations != nil && c.observationFirstStreamAt.IsZero() && len(data) > 0 {
		c.observeRequest(observation.FirstStreamEvent, c.rec)
	}
	u.ApplySSE(event, data)
	// Only the events the endpoint named are kept, and only while the caps
	// allow: the stream itself is relayed and dropped, never accumulated
	// (usageplugin.go).
	if cap != nil && gjson.ValidBytes(data) {
		cap.addEvent(usagerules.EventName(event, data), data)
	}
}

// jsonArrayStream reports whether the client of a streaming Gemini endpoint
// expects Google's default stream framing (one JSON array whose elements
// arrive progressively) rather than SSE (?alt=sse). Upstream events are
// re-framed accordingly.
func (c *call) jsonArrayStream() bool {
	return c.ep.Protocol == protocolGeminiStream && !strings.EqualFold(c.c.Query("alt"), "sse")
}

const protocolGeminiStream = manifest.PlatformGemini + ".stream_generate"

// readLine returns the next line including its '\n' (the last line may lack
// it), of any length: one longer than br's buffer is gathered into a slice
// that grows. The slice is only valid until the next read.
func readLine(br *bufio.Reader) ([]byte, error) {
	line, err := br.ReadSlice('\n')
	if !errors.Is(err, bufio.ErrBufferFull) {
		return line, err
	}
	buf := append([]byte(nil), line...)
	for {
		line, err = br.ReadSlice('\n')
		buf = append(buf, line...)
		if !errors.Is(err, bufio.ErrBufferFull) {
			return buf, err
		}
	}
}

// ---------------------------------------------------------------- usage extraction

// usageAcc applies the platform's declarative usage rules. The extraction
// itself lives in usagerules, shared with the console "test account" action.
type usageAcc = usagerules.Acc

func newUsageAcc(rules manifest.UsageRules) *usageAcc { return usagerules.New(rules) }
