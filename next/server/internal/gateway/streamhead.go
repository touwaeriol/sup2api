package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/usagerules"
)

// Holding the head of a stream (CONTRACTS §138).
//
// Some upstreams answer 200, open the event stream and only then fail: the
// ChatGPT backends send response.created and, seconds later, an error event
// ("Our servers are currently overloaded"). Relayed as it comes, the client
// has its 200 before the error and the attempt can no longer move to another
// account. A streaming answer of a protocol listed here is therefore held
// until its first event that is not prelude. An error event before it is an
// upstream HTTP 500 answer whatever the event names (the owner's choice: the
// plugin classifies it, the failure policy retries or returns it); a stream
// that breaks before it is a transport error. Nothing bounds the hold but the
// administrator's stream idle timeout and relay timeout (§112.16).

const protocolResponses = manifest.PlatformOpenAI + ".responses"

// streamPreludes are, per upstream protocol, the events a stream opens with
// before it carries anything of the answer.
var streamPreludes = map[string]map[string]bool{
	protocolResponses: {"response.created": true, "response.in_progress": true},
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
func streamErrorEvent(name string) bool { return name == "error" || name == "response.failed" }

// errorEventFailure is the failure of error event name with data, with the
// status the event carries (streamOnlyError).
func errorEventFailure(name string, data []byte) streamFailure {
	status, body := streamOnlyError(name, data)
	return streamFailure{event: name, data: bytes.Clone(data), status: status, errBody: body}
}

// holdStreamHead reads the head of a streaming answer whose upstream protocol
// has a prelude. failed reports an attempt that ended there; otherwise the
// bytes read are put back in front of resp.Body for forward.
func (c *call) holdStreamHead(ctx, uctx context.Context, x *execution, resp *http.Response) (attemptResult, bool) {
	prelude := streamPreludes[x.rt.upstream]
	if prelude == nil || !c.stream || !isSSE(resp.Header.Get("Content-Type")) {
		return attemptResult{}, false
	}
	c.drain.arm()
	r := newSSEReader(resp.Body, headBufferSize, true)
	if res, failed := c.failStream(ctx, uctx, x, resp, readStreamHead(r, prelude)); failed {
		return res, true
	}
	resp.Body = struct {
		io.Reader
		io.Closer
	}{r.rest(), resp.Body}
	return attemptResult{}, false
}

// readStreamHead reads r up to its first event that is neither prelude nor an
// error event, and reports the failure the head amounts to, if any.
func readStreamHead(r *sseReader, prelude map[string]bool) streamFailure {
	for {
		event, data, err := r.next()
		if err != nil {
			return streamFailure{broken: unfinished(err, errStreamHeadUnfinished)}
		}
		name := usagerules.EventName(event, data)
		if streamErrorEvent(name) {
			f := errorEventFailure(name, data)
			f.status = http.StatusInternalServerError
			return f
		}
		if !prelude[name] {
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
