package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"slices"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/usagerules"
)

// Upstreams that only accept streaming requests (CONTRACTS §83).
//
// An account type lists such upstream protocols in
// AccountPlatform.streamOnly. A non-streaming request routed to one of them is
// sent upstream with the endpoint's streamPath set to true (upstreamBody,
// before the plugin's Execute; the plugin hands the flag back unchanged).
// The SSE answer is read here, before anything reaches the client, and turned
// back into what a non-streaming upstream would have answered: the response
// object of the terminal event becomes the JSON body and the attempt goes on
// through the ordinary non-streaming path (JSON usage rules, conversion to the
// client protocol, the record with stream=false, billing). Error events are
// upstream errors and go to the plugin's ClassifyError like an HTTP error.

// streamOnlyTerminal are the events whose "response" object is the answer.
var streamOnlyTerminal = map[string]bool{"response.completed": true, "response.incomplete": true}

// forcesStream reports whether this request goes upstream as a stream
// although the client did not ask for one.
func (c *call) forcesStream(rt *typeRoute) bool {
	return rt != nil && rt.streamOnly && !c.stream && !c.ep.WebSocket() && !c.ep.TaskSubmit()
}

// forceStreamPath sets the stream flag of body at path to true.
func forceStreamPath(body []byte, path string) ([]byte, error) {
	if gjson.GetBytes(body, path).Type == gjson.True {
		return body, nil
	}
	return sjson.SetBytes(body, path, true)
}

// streamOnlyAnswer is what an SSE answer of a stream-only upstream amounts to:
// a JSON response body, or the failure the stream is instead.
type streamOnlyAnswer struct {
	streamFailure
	body []byte
}

var errStreamOnlyUnfinished = errors.New("upstream stream ended before response.completed")

// readStreamOnly reads the SSE answer of a stream-only upstream. Only the
// terminal event's response object and the output_item.done items are kept,
// never the whole stream.
func readStreamOnly(rd io.Reader) streamOnlyAnswer {
	r := newSSEReader(rd, 64<<10, false)
	items := map[int64][]byte{}
	var ans streamOnlyAnswer
	for {
		event, data, err := r.next()
		if err != nil {
			return streamOnlyAnswer{streamFailure: streamFailure{broken: unfinished(err, errStreamOnlyUnfinished)}}
		}
		if streamOnlyEvent(event, data, items, &ans) {
			return finishStreamOnly(ans, items)
		}
	}
}

func finishStreamOnly(ans streamOnlyAnswer, items map[int64][]byte) streamOnlyAnswer {
	if ans.body != nil {
		ans.body = fillStreamOnlyOutput(ans.body, items)
	}
	return ans
}

// streamOnlyEvent records one event and reports whether it ends the answer.
func streamOnlyEvent(event string, data []byte, items map[int64][]byte, ans *streamOnlyAnswer) bool {
	if event == "" && len(data) == 0 {
		return false
	}
	name := usagerules.EventName(event, data)
	switch {
	case name == "response.output_item.done":
		idx, item := gjson.GetBytes(data, "output_index"), gjson.GetBytes(data, "item")
		if idx.Type != gjson.Number || !item.IsObject() {
			return false
		}
		items[idx.Int()] = []byte(item.Raw)
	case streamOnlyTerminal[name]:
		resp := gjson.GetBytes(data, "response")
		if !resp.IsObject() {
			ans.status, ans.errBody = http.StatusBadGateway, []byte(`{"error":{"message":"upstream terminal event carries no response object"}}`)
			return true
		}
		ans.body = []byte(resp.Raw)
		return true
	case streamErrorEvent(name, data):
		ans.streamFailure = errorEventFailure(name, data)
		return true
	}
	return false
}

// fillStreamOnlyOutput puts the output_item.done items, in output_index
// order, into a response object whose output is missing or empty.
func fillStreamOnlyOutput(body []byte, items map[int64][]byte) []byte {
	if len(items) == 0 {
		return body
	}
	if out := gjson.GetBytes(body, "output"); out.IsArray() && len(out.Array()) > 0 {
		return body
	}
	idxs := make([]int64, 0, len(items))
	for i := range items {
		idxs = append(idxs, i)
	}
	slices.Sort(idxs)
	arr := []byte{'['}
	for n, i := range idxs {
		if n > 0 {
			arr = append(arr, ',')
		}
		arr = append(arr, items[i]...)
	}
	arr = append(arr, ']')
	if filled, err := sjson.SetRawBytes(body, "output", arr); err == nil {
		return filled
	}
	return body
}

// streamOnlyError turns an error event into the HTTP status it carries (502
// when none) and the body a non-streaming upstream would have answered with:
// {"error": <the event's error object>}.
func streamOnlyError(name string, data []byte) (int, []byte) {
	status := http.StatusBadGateway
	if s := gjson.GetBytes(data, "status"); s.Type == gjson.Number && s.Int() >= 400 && s.Int() <= 599 {
		status = int(s.Int())
	}
	path := "error"
	if name == "response.failed" {
		path = "response.error"
	}
	detail := gjson.GetBytes(data, path)
	switch {
	case detail.IsObject():
		return status, append(append([]byte(`{"error":`), detail.Raw...), '}')
	case detail.Type == gjson.String:
		out, _ := sjson.SetBytes([]byte(`{}`), "error.message", detail.Str)
		return status, out
	}
	// A Responses "error" event names code, message and param at the top.
	out := []byte(`{"error":{}}`)
	for _, k := range []string{"type", "code", "message", "param"} {
		v := gjson.GetBytes(data, k)
		if !v.Exists() || (k == "type" && v.Str == name) {
			continue
		}
		out, _ = sjson.SetRawBytes(out, "error."+k, []byte(v.Raw))
	}
	if !gjson.GetBytes(out, "error.message").Exists() {
		msg := "upstream stream error"
		if name == "response.failed" {
			msg = "upstream response failed"
		}
		out, _ = sjson.SetBytes(out, "error.message", msg)
	}
	return status, out
}

// collectStreamOnly reads the SSE answer of a stream-only upstream and either
// replaces resp with the JSON answer for the non-streaming path (failed
// false) or returns the attempt's result (failed true). An upstream that
// answered JSON after all is left untouched.
//
// The upstream answered and is producing while it is read, so a client that
// leaves meanwhile is a hang-up of an answered request (CONTRACTS §25.8):
// under "continue" the stream is read on to its end and the JSON answer
// billed, otherwise the request pays the estimate.
func (c *call) collectStreamOnly(ctx, uctx context.Context, x *execution, resp *http.Response) (res attemptResult, failed bool) {
	if !isSSE(resp.Header.Get("Content-Type")) {
		return attemptResult{}, false
	}
	c.drain.arm()
	ans := readStreamOnly(resp.Body)
	if res, failed := c.failStream(ctx, uctx, x, resp, ans.streamFailure); failed {
		return res, true
	}
	resp.Header = jsonHeader(resp.Header)
	resp.ContentLength = int64(len(ans.body))
	resp.Body = io.NopCloser(bytes.NewReader(ans.body))
	return attemptResult{}, false
}
