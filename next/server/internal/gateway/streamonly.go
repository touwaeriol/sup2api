package gateway

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/usagerules"
)

// Upstreams that only accept streaming requests (CONTRACTS §83).
//
// An account type lists such upstream protocols in
// AccountPlatform.streamOnly. A non-streaming request routed to one of them is
// sent upstream with the endpoint's streamPath set to true (attempt, before
// BuildUpstreamRequest, and again after the plugin's patches in forwardBuilt).
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
// a JSON response body, an upstream error event, or a broken stream.
type streamOnlyAnswer struct {
	body []byte
	// status and errBody describe an error event: the HTTP status the event
	// carries (502 when none) and an OpenAI style {"error": {...}} body.
	status  int
	errBody []byte
	// broken is a stream that failed or ended without a terminal event.
	broken error
	// tooLarge is an answer beyond what the gateway keeps in memory.
	tooLarge bool
}

var errStreamOnlyUnfinished = errors.New("upstream stream ended before response.completed")

// readStreamOnly reads the SSE answer of a stream-only upstream. Only the
// terminal event's response object and the output_item.done items are kept,
// never the whole stream.
func readStreamOnly(r io.Reader) streamOnlyAnswer {
	br := bufio.NewReaderSize(r, 64<<10)
	var event string
	var data []byte
	items := map[int64][]byte{}
	kept := 0
	var ans streamOnlyAnswer
	for {
		line, rerr := readLine(br, maxSSELine)
		t := bytes.TrimRight(line, "\r\n")
		switch {
		case len(line) > 0 && len(t) == 0:
			if streamOnlyEvent(event, data, items, &kept, &ans) {
				return finishStreamOnly(ans, items)
			}
			event, data = "", data[:0]
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
		if rerr != nil {
			// The last event may lack its blank line.
			if rerr == io.EOF && streamOnlyEvent(event, data, items, &kept, &ans) {
				return finishStreamOnly(ans, items)
			}
			if rerr == io.EOF {
				rerr = errStreamOnlyUnfinished
			}
			return streamOnlyAnswer{broken: rerr}
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
func streamOnlyEvent(event string, data []byte, items map[int64][]byte, kept *int, ans *streamOnlyAnswer) bool {
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
		if *kept += len(item.Raw); *kept > maxUsageJSONBuf {
			ans.tooLarge = true
			return true
		}
		items[idx.Int()] = []byte(item.Raw)
	case streamOnlyTerminal[name]:
		resp := gjson.GetBytes(data, "response")
		if !resp.IsObject() {
			ans.status, ans.errBody = http.StatusBadGateway, []byte(`{"error":{"message":"upstream terminal event carries no response object"}}`)
			return true
		}
		if *kept += len(resp.Raw); *kept > maxUsageJSONBuf {
			ans.tooLarge = true
			return true
		}
		ans.body = []byte(resp.Raw)
		return true
	case name == "response.failed" || name == "error":
		ans.status, ans.errBody = streamOnlyError(name, data)
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

// streamOnlyError turns an error event into the HTTP status it carries and
// the body a non-streaming upstream would have answered with:
// {"error": <the event's error object>}. Without a status the event is a 502,
// unless it is a request error (streamOnlyRequestError): a 400.
func streamOnlyError(name string, data []byte) (int, []byte) {
	path := "error"
	if name == "response.failed" {
		path = "response.error"
	}
	detail := gjson.GetBytes(data, path)
	status := http.StatusBadGateway
	if s := gjson.GetBytes(data, "status"); s.Type == gjson.Number && s.Int() >= 400 && s.Int() <= 599 {
		status = int(s.Int())
	} else if streamOnlyRequestError(data, detail) {
		status = http.StatusBadRequest
	}
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

// streamOnlyAccountCodes are error codes OpenAI sends with the type
// invalid_request_error that blame the credential or the account, not the
// request (over HTTP they come with a 401 or 403): such an event stays a 502
// and the attempt moves to another account.
var streamOnlyAccountCodes = map[string]bool{
	"invalid_api_key": true, "account_deactivated": true, "invalid_organization": true,
	"organization_deactivated": true, "unsupported_country_region_territory": true,
}

// streamOnlyRequestError reports whether an error event without a status
// rejects the request itself: its type (error.type, response.error.type or
// the event's own type) is invalid_request_error and its code does not name
// the account. Upstreams answer such a request the same way on every account
// (for example text.format json_object without the word "json" in the
// input), so it goes back to the client as a 400 instead of failing over.
func streamOnlyRequestError(data []byte, detail gjson.Result) bool {
	typ, code := gjson.GetBytes(data, "type"), gjson.GetBytes(data, "code")
	if detail.IsObject() {
		typ, code = detail.Get("type"), detail.Get("code")
	}
	return typ.Str == "invalid_request_error" && !streamOnlyAccountCodes[code.Str]
}

// collectStreamOnly reads the SSE answer of a stream-only upstream and either
// replaces resp with the JSON answer for the non-streaming path (failed
// false) or returns the attempt's result (failed true). An upstream that
// answered JSON after all is left untouched.
func (c *call) collectStreamOnly(ctx, uctx context.Context, rt *typeRoute, acc *core.Account, pacct *pluginv1.Account,
	execution *gatewayExecution, resp *http.Response) (res attemptResult, failed bool) {
	if !isSSE(resp.Header.Get("Content-Type")) {
		return attemptResult{}, false
	}
	ans := readStreamOnly(resp.Body)
	switch {
	case ans.broken != nil:
		if ctx.Err() != nil {
			return attemptResult{kind: attemptCanceled, err: canceledErr()}, true
		}
		msg := ans.broken.Error()
		if cause := context.Cause(uctx); errors.Is(cause, errRelayTimeout) || errors.Is(cause, errStreamIdleTimeout) {
			msg = cause.Error()
		}
		if execution != nil {
			return execution.classify(ctx, 0, nil, nil, msg), true
		}
		return c.classify(ctx, rt, pacct, 0, nil, nil, msg), true
	case ans.tooLarge:
		e := &gwError{Status: http.StatusBadGateway, Code: codeUpstreamError, RecordType: errTypeUpstream,
			Message: "upstream response too large"}
		if execution != nil {
			execution.unclassified = e
		}
		return attemptResult{kind: attemptReturn, err: e}, true
	case ans.errBody != nil:
		h := resp.Header.Clone()
		h.Set("Content-Type", "application/json")
		h.Del("Content-Length")
		return c.upstreamFailure(ctx, rt, acc, pacct, execution, ans.status, h, &upstreamErrorBody{prefix: ans.errBody}), true
	}
	h := resp.Header.Clone()
	h.Set("Content-Type", "application/json")
	h.Del("Content-Length")
	resp.Header = h
	resp.ContentLength = int64(len(ans.body))
	resp.Body = io.NopCloser(bytes.NewReader(ans.body))
	return attemptResult{}, false
}
