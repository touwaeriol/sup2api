package awseventstream

import (
	"bytes"
	"encoding/json"
	"io"
)

// FormatChunk names the response stream ChunkSSEReader decodes. It is the
// value a plugin puts in UpstreamRequest.response_stream (pluginsdk
// Upstream.ResponseStream) to have the host run an upstream answer through
// ChunkSSEReader.
const FormatChunk = "aws-eventstream-chunk"

// ChunkSSEReader presents an event stream of "chunk" events - what Bedrock's
// InvokeModelWithResponseStream answers, each event wrapping one of the
// model's native stream events - as server-sent events:
//
//   - an event message with the payload {"bytes":"<base64>"} becomes
//     "event: <type>\ndata: <json>\n\n", where json is the decoded bytes and
//     type its "type" member (without one only the data line is written);
//   - an exception or error message becomes
//     "event: error\ndata: {"type":"error","error":{"type":…,"message":…}}\n\n"
//     and ends the stream. That is the shape of an Anthropic stream error,
//     whatever the model: the models served this way that have an error
//     event of their own in the stream use it, and the host's relaying reads
//     it as the upstream's error;
//   - any other message, and an event without bytes, is skipped;
//   - a corrupt or truncated message fails the read: the stream was cut. A
//     message of any length the format allows is read whole; its buffer
//     grows with the bytes that arrive. A message repeating a header name is
//     corrupt (decoder.Decode), so the type of a message is never ambiguous.
//
// It is not a reader for event streams in general: one whose events carry
// their data in another shape (Bedrock's ConverseStream, for one) has no
// "bytes" and would come out empty. Such a stream needs its own adapter and
// its own format name next to FormatChunk; decoder serves both.
//
// Each Read returns at most the events of one message, as soon as that
// message is complete: nothing waits for a buffer to fill.
type ChunkSSEReader struct {
	dec     *decoder
	pending []byte
	buf     bytes.Buffer
	err     error
}

// NewChunkSSEReader returns a reader of the SSE form of the chunk event
// stream r. Closing and cancelling stay with r: an error of r is returned as
// it is.
func NewChunkSSEReader(r io.Reader) *ChunkSSEReader {
	return &ChunkSSEReader{dec: newDecoder(r)}
}

// Read implements io.Reader.
func (s *ChunkSSEReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(s.pending) == 0 {
		if s.err != nil {
			return 0, s.err
		}
		s.next()
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	return n, nil
}

// next decodes one message into pending, or records why there is none.
func (s *ChunkSSEReader) next() {
	msg, err := s.dec.Decode()
	if err != nil {
		s.err = err
		return
	}
	s.buf.Reset()
	switch msg.Header(HeaderMessageType) {
	case MessageTypeEvent:
		writeEvent(&s.buf, msg.Payload)
	case messageTypeException:
		writeError(&s.buf, msg.Header(headerExceptionType), exceptionMessage(msg.Payload))
		s.err = io.EOF
	case messageTypeError:
		writeError(&s.buf, msg.Header(headerErrorCode), msg.Header(headerErrorMessage))
		s.err = io.EOF
	}
	s.pending = s.buf.Bytes()
}

// writeEvent writes the SSE event of an event message's payload; nothing for
// a payload without bytes.
func writeEvent(w *bytes.Buffer, payload []byte) {
	var chunk struct {
		Bytes []byte `json:"bytes"`
	}
	if json.Unmarshal(payload, &chunk) != nil || len(chunk.Bytes) == 0 {
		return
	}
	var event struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(chunk.Bytes, &event)
	writeSSE(w, event.Type, chunk.Bytes)
}

// writeError writes the terminal error event, in the shape of an Anthropic
// stream error (see ChunkSSEReader).
func writeError(w *bytes.Buffer, typ, message string) {
	if typ == "" {
		typ = "upstream_error"
	}
	if message == "" {
		message = typ
	}
	var body struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	body.Type, body.Error.Type, body.Error.Message = "error", typ, message
	data, _ := json.Marshal(body)
	writeSSE(w, "error", data)
}

// exceptionMessage is the "message" member of an exception's JSON payload,
// or the payload itself when it is not that.
func exceptionMessage(payload []byte) string {
	var body struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(payload, &body) == nil && body.Message != "" {
		return body.Message
	}
	return string(bytes.TrimSpace(payload))
}

// writeSSE writes one event. A line break inside data would end the field,
// so JSON is compacted and anything else is split into one data line each.
func writeSSE(w *bytes.Buffer, event string, data []byte) {
	if event != "" && !bytes.ContainsAny([]byte(event), "\r\n") {
		w.WriteString("event: " + event + "\n")
	}
	if bytes.ContainsAny(data, "\r\n") {
		var compact bytes.Buffer
		if json.Compact(&compact, data) == nil {
			data = compact.Bytes()
		}
	}
	for _, line := range bytes.FieldsFunc(data, func(r rune) bool { return r == '\n' || r == '\r' }) {
		w.WriteString("data: ")
		w.Write(line)
		w.WriteByte('\n')
	}
	w.WriteByte('\n')
}
