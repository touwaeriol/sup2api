package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/usagerules"
)

// Holding the head of a stream (CONTRACTS §138).
//
// Some upstreams answer 200, open the event stream and only then fail: the
// ChatGPT backends send response.created, response.in_progress, keepalive and
// response.metadata and, seconds later, an error event (type
// service_unavailable_error, code server_is_overloaded). Relayed as it comes,
// the client has its 200 before the error and the attempt can no longer move
// to another account. A streaming answer of a protocol listed here is
// therefore held while it sends only what comes before its answer. An error
// event meanwhile is an upstream HTTP 500 answer whatever it names (the
// owner's choice: the plugin classifies it, the failure policy retries or
// returns it); a stream that breaks meanwhile is a transport error. Nothing
// bounds the hold but the administrator's stream idle timeout and relay
// timeout (§112.16).

const protocolResponses = manifest.PlatformOpenAI + ".responses"

// streamHead tells, for one upstream protocol, the events a stream sends
// before its answer from the answer itself: any event of the protocol's
// family is the answer, except the ones listed. Events outside the family
// (keepalive, vendor notices), comments and empty blocks come before it too,
// so an upstream that adds a notice is still held, and one that skips an
// announcement (a delta without response.output_item.added) is not.
type streamHead struct {
	family string
	before map[string]bool
}

// answers reports whether event name starts the answer.
func (h streamHead) answers(name string) bool {
	return strings.HasPrefix(name, h.family) && !h.before[name]
}

// streamHeads are the protocols whose streams are held, with their head.
var streamHeads = map[string]streamHead{
	protocolResponses: {family: "response.", before: map[string]bool{
		"response.created": true, "response.in_progress": true, "response.queued": true, "response.metadata": true}},
}

// headBufferSize is the read buffer of a held head: it stays under the
// relayed stream, and a longer line is gathered anyway (readLine).
const headBufferSize = 4 << 10

var errStreamHeadUnfinished = errors.New("upstream stream ended before any content")

// streamFailure is what a stream read before anything reached the client
// amounts to when it is not an answer: an error event (the event as sent,
// the HTTP status it stands for and an OpenAI style {"error": {...}} body) or
// a broken stream.
type streamFailure struct {
	event   string
	data    []byte
	status  int
	errBody []byte
	broken  error
}

// streamErrorEvent reports whether an event of the stream names a failure.
func streamErrorEvent(name string, data []byte) bool {
	return usagerules.ErrorEvent(name, data) || name == "response.failed"
}

// errorEventFailure is the failure of error event name with data, with the
// status the event carries (streamOnlyError).
func errorEventFailure(name string, data []byte) streamFailure {
	status, body := streamOnlyError(name, data)
	return streamFailure{event: name, data: bytes.Clone(data), status: status, errBody: body}
}

// holdStreamHead reads the head of a streaming answer whose upstream
// protocol is listed in streamHeads. failed reports an attempt that ended
// there; otherwise the bytes read are put back in front of resp.Body.
func (c *call) holdStreamHead(ctx, uctx context.Context, x *execution, resp *http.Response) (attemptResult, bool) {
	head, held := streamHeads[x.rt.upstream]
	if !held || !c.stream || !isSSE(resp.Header.Get("Content-Type")) {
		return attemptResult{}, false
	}
	c.drain.arm()
	r := newSSEReader(resp.Body, headBufferSize, true)
	if res, failed := c.failStream(ctx, uctx, x, resp, readStreamHead(r, head)); failed {
		return res, true
	}
	resp.Body = struct {
		io.Reader
		io.Closer
	}{r.rest(), resp.Body}
	return attemptResult{}, false
}

// readStreamHead reads r up to the event that starts the answer or an error
// event, and reports the failure the head amounts to, if any.
func readStreamHead(r *sseReader, head streamHead) streamFailure {
	for {
		event, data, err := r.next()
		if err != nil {
			return streamFailure{broken: unfinished(err, errStreamHeadUnfinished)}
		}
		name := usagerules.EventName(event, data)
		if streamErrorEvent(name, data) {
			f := errorEventFailure(name, data)
			f.status = http.StatusInternalServerError
			return f
		}
		if head.answers(name) {
			return streamFailure{}
		}
	}
}

// failStream turns f into the attempt's result; failed is false when f is no
// failure. An error event is logged as sent and classified like an HTTP
// error answer.
func (c *call) failStream(ctx, uctx context.Context, x *execution, resp *http.Response, f streamFailure) (attemptResult, bool) {
	switch {
	case f.broken != nil:
		if ctx.Err() != nil {
			c.upstreamResponded = true
			return attemptResult{kind: attemptCanceled, err: canceledErr()}, true
		}
		msg := f.broken.Error()
		if cause := context.Cause(uctx); errors.Is(cause, errRelayTimeout) || errors.Is(cause, errStreamIdleTimeout) {
			msg = cause.Error()
		}
		return c.classifyUpstream(ctx, x, 0, nil, nil, msg), true
	case f.errBody != nil:
		c.warnStreamError(ctx, x.rt, x.acc.ID, f.event, f.data)
		return c.upstreamFailure(ctx, x, f.status, jsonHeader(resp.Header), &upstreamErrorBody{prefix: f.errBody}), true
	}
	return attemptResult{}, false
}

// warnStreamError logs an error event of an upstream stream whole, as the
// upstream sent it, so its real type and code stay on record.
func (c *call) warnStreamError(ctx context.Context, rt *typeRoute, accountID int64, event string, data []byte) {
	slog.WarnContext(ctx, "gateway: upstream stream error event", "request_id", c.rid,
		"plugin", rt.binding.Plugin.Key, "account", accountID, "event", event, "data", string(data))
}

// jsonHeader is h for a JSON body made from a stream.
func jsonHeader(h http.Header) http.Header {
	h = h.Clone()
	h.Set("Content-Type", "application/json")
	h.Del("Content-Length")
	return h
}
