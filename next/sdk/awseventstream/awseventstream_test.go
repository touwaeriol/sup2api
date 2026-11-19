package awseventstream

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"math/rand/v2"
	"os"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

type sdkVector struct {
	Name    string            `json:"name"`
	Headers map[string]string `json:"string_headers"`
	Payload string            `json:"payload"`
	Hex     string            `json:"hex"`
	raw     []byte
}

func sdkVectors(t testing.TB) map[string]sdkVector {
	t.Helper()
	data, err := os.ReadFile("testdata/aws-sdk-go-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var list []sdkVector
	if err := json.Unmarshal(data, &list); err != nil || len(list) < 7 {
		t.Fatalf("vectors: %d %v", len(list), err)
	}
	out := map[string]sdkVector{}
	for _, v := range list {
		if v.raw, err = hex.DecodeString(v.Hex); err != nil {
			t.Fatal(err)
		}
		out[v.Name] = v
	}
	return out
}

// Value types of a header, for the frames built by hand.
const (
	typeInt32     = 4
	typeBytes     = 6
	typeString    = 7
	typeTimestamp = 8
	typeUUID      = 9
)

// rawFrame builds a frame by hand around an arbitrary header block, with
// correct checksums: independent of AppendMessage, and able to carry header
// blocks AppendMessage refuses to write.
func rawFrame(headers, payload []byte) []byte {
	b := binary.BigEndian.AppendUint32(nil, uint32(16+len(headers)+len(payload)))
	b = binary.BigEndian.AppendUint32(b, uint32(len(headers)))
	b = binary.BigEndian.AppendUint32(b, crc32.ChecksumIEEE(b))
	b = append(append(b, headers...), payload...)
	return binary.BigEndian.AppendUint32(b, crc32.ChecksumIEEE(b))
}

// rawPrelude is a prelude with a correct checksum for any pair of lengths.
func rawPrelude(total, headers uint32) []byte {
	b := binary.BigEndian.AppendUint32(nil, total)
	b = binary.BigEndian.AppendUint32(b, headers)
	return binary.BigEndian.AppendUint32(b, crc32.ChecksumIEEE(b))
}

func mustAppend(t testing.TB, dst []byte, headers []Header, payload []byte) []byte {
	t.Helper()
	out, err := AppendMessage(dst, headers, payload)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func eventHeaders() []Header {
	return []Header{{HeaderEventType, "chunk"}, {HeaderContentType, "application/json"}, {HeaderMessageType, MessageTypeEvent}}
}

func chunkPayload(event string) []byte {
	return []byte(`{"bytes":"` + base64.StdEncoding.EncodeToString([]byte(event)) + `"}`)
}

func chunkFrame(t testing.TB, event string) []byte {
	return mustAppend(t, nil, eventHeaders(), chunkPayload(event))
}

// The decoder reads the frames recorded from the official SDK, including
// header value types it does not return, and the encoder writes the same
// bytes: the recording pins the wire format across upgrades of the library
// the package now decodes and encodes with.
func TestSDKRecordedFrames(t *testing.T) {
	for name, v := range sdkVectors(t) {
		t.Run(name, func(t *testing.T) {
			d := newDecoder(bytes.NewReader(v.raw))
			msg, err := d.Decode()
			if err != nil {
				t.Fatal(err)
			}
			if string(msg.Payload) != v.Payload {
				t.Errorf("payload %q, want %q", msg.Payload, v.Payload)
			}
			if len(msg.Headers) != len(v.Headers) {
				t.Errorf("headers %v, want %v", msg.Headers, v.Headers)
			}
			for _, h := range msg.Headers {
				if v.Headers[h.Name] != h.Value || msg.Header(h.Name) != h.Value {
					t.Errorf("header %s = %q, want %q", h.Name, h.Value, v.Headers[h.Name])
				}
			}
			if msg.Header("absent") != "" {
				t.Error("absent header has a value")
			}
			if _, err := d.Decode(); err != io.EOF {
				t.Errorf("after the message: %v", err)
			}
			if name == "mixed-header-types" {
				return // carries non-string headers the encoder does not write
			}
			if got := mustAppend(t, nil, msg.Headers, msg.Payload); !bytes.Equal(got, v.raw) {
				t.Errorf("encoded\n got %x\nwant %x", got, v.raw)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	big := bytes.Repeat([]byte("x"), 1<<20)
	msgs := []eventMessage{
		{Headers: eventHeaders(), Payload: chunkPayload(`{"type":"ping"}`)},
		{},
		{Payload: big},
		{Headers: []Header{{"a", ""}, {strings.Repeat("n", 255), strings.Repeat("v", 0x7fff)}}},
	}
	var stream []byte
	for _, m := range msgs {
		stream = mustAppend(t, stream, m.Headers, m.Payload)
	}
	d := newDecoder(iotest.OneByteReader(bytes.NewReader(stream)))
	for i, want := range msgs {
		got, err := d.Decode()
		if err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
		if !bytes.Equal(got.Payload, want.Payload) || len(got.Headers) != len(want.Headers) {
			t.Fatalf("message %d differs", i)
		}
		for j, h := range want.Headers {
			if got.Headers[j] != h {
				t.Fatalf("message %d header %d: %v", i, j, got.Headers[j])
			}
		}
	}
	if _, err := d.Decode(); err != io.EOF {
		t.Fatalf("end: %v", err)
	}
}

func TestAppendMessageLimits(t *testing.T) {
	prefix := []byte("kept")
	for name, tc := range map[string]struct {
		headers []Header
		payload int
	}{
		"empty header name":  {headers: []Header{{"", "v"}}},
		"long header name":   {headers: []Header{{strings.Repeat("n", 256), "v"}}},
		"long header value":  {headers: []Header{{"n", strings.Repeat("v", 0x8000)}}},
		"header block":       {headers: []Header{{"a", strings.Repeat("v", 0x7fff)}, {"b", strings.Repeat("v", 0x7fff)}, {"c", strings.Repeat("v", 0x7fff)}, {"d", strings.Repeat("v", 0x7fff)}}},
		"message over limit": {payload: maxMessageSize - minMessageSize + 1},
	} {
		out, err := AppendMessage(prefix, tc.headers, make([]byte, tc.payload))
		if err == nil || !bytes.Equal(out, prefix) {
			t.Errorf("%s: %v, %d bytes", name, err, len(out))
		}
	}
	// The largest message is written and read back.
	out := mustAppend(t, nil, nil, make([]byte, maxMessageSize-minMessageSize))
	if msg, err := newDecoder(bytes.NewReader(out)).Decode(); err != nil || len(msg.Payload) != maxMessageSize-minMessageSize {
		t.Fatalf("largest message: %v", err)
	}
}

// Any single corrupted byte is caught by one of the two checksums.
func TestEveryCorruptedByteIsRejected(t *testing.T) {
	frame := sdkVectors(t)["chunk-message-start"].raw
	for i := range frame {
		for _, bit := range []byte{0x01, 0x80} {
			bad := bytes.Clone(frame)
			bad[i] ^= bit
			_, err := newDecoder(bytes.NewReader(bad)).Decode()
			if err == nil {
				t.Fatalf("byte %d bit %#x: accepted", i, bit)
			}
			// A corrupted length fails the prelude checksum before the
			// length is used, so this is never a mere "unexpected EOF".
			if !errors.Is(err, errCorrupt) {
				t.Fatalf("byte %d bit %#x: %v", i, bit, err)
			}
		}
	}
}

func TestChecksums(t *testing.T) {
	frame := sdkVectors(t)["chunk-message-start"].raw
	prelude := bytes.Clone(frame)
	prelude[9]++
	if _, err := newDecoder(bytes.NewReader(prelude)).Decode(); !errors.Is(err, errCorrupt) || !strings.Contains(err.Error(), "prelude checksum") {
		t.Fatalf("prelude: %v", err)
	}
	message := bytes.Clone(frame)
	message[len(message)-1]++
	if _, err := newDecoder(bytes.NewReader(message)).Decode(); !errors.Is(err, errCorrupt) || !strings.Contains(err.Error(), "message checksum") {
		t.Fatalf("message: %v", err)
	}
	payload := bytes.Clone(frame)
	payload[len(payload)-10]++
	if _, err := newDecoder(bytes.NewReader(payload)).Decode(); !errors.Is(err, errCorrupt) || !strings.Contains(err.Error(), "message checksum") {
		t.Fatalf("payload: %v", err)
	}
}

// A stream cut anywhere yields the complete messages before the cut, then
// io.EOF on a message boundary and io.ErrUnexpectedEOF inside a message.
func TestTruncation(t *testing.T) {
	first, second := chunkFrame(t, `{"type":"a"}`), chunkFrame(t, `{"type":"b"}`)
	stream := append(bytes.Clone(first), second...)
	for cut := 0; cut <= len(stream); cut++ {
		d := newDecoder(bytes.NewReader(stream[:cut]))
		complete := 0
		var err error
		for {
			if _, err = d.Decode(); err != nil {
				break
			}
			complete++
		}
		wantComplete, wantErr := 0, io.ErrUnexpectedEOF
		if cut >= len(first) {
			wantComplete = 1
		}
		if cut == len(stream) {
			wantComplete = 2
		}
		if cut == 0 || cut == len(first) || cut == len(stream) {
			wantErr = io.EOF
		}
		if complete != wantComplete || err != wantErr {
			t.Fatalf("cut at %d: %d messages, %v; want %d, %v", cut, complete, err, wantComplete, wantErr)
		}
	}
}

// Lengths are bounded before anything is read or allocated for them.
func TestLengthLimits(t *testing.T) {
	endless := func(prelude []byte) io.Reader {
		return io.MultiReader(bytes.NewReader(prelude), iotest.ErrReader(errors.New("read past the prelude")))
	}
	for name, prelude := range map[string][]byte{
		"shorter than a message": rawPrelude(minMessageSize-1, 0),
		"zero":                   rawPrelude(0, 0),
		"over 16 MiB":            rawPrelude(maxMessageSize+1, 0),
		"4 GiB":                  rawPrelude(0xffffffff, 0),
		"headers past the end":   rawPrelude(100, 100-minMessageSize+1),
		"headers over 128 KiB":   rawPrelude(maxMessageSize, maxHeadersSize+1),
	} {
		d := newDecoder(endless(prelude))
		if _, err := d.Decode(); !errors.Is(err, errCorrupt) {
			t.Errorf("%s: %v", name, err)
		}
		if d.buf != nil {
			t.Errorf("%s: allocated %d bytes", name, cap(d.buf))
		}
	}
}

// allocated is the number of bytes f allocates, whether or not they are
// still in use.
func allocated(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// A prelude announcing a large message reserves nothing: what is allocated
// follows the bytes that really arrive, and a buffer that grew large is not
// kept.
func TestBufferFollowsTheData(t *testing.T) {
	for _, sent := range []int{0, 1000, 64 << 10} {
		cut := append(rawPrelude(maxMessageSize, 0), make([]byte, sent)...)
		var err error
		got := allocated(func() { _, err = newDecoder(bytes.NewReader(cut)).Decode() })
		if err != io.ErrUnexpectedEOF {
			t.Fatalf("cut message: %v", err)
		}
		// The decoder's own read buffer, and the payload buffer doubling up
		// to what was received (the race detector doubles what a growth
		// step allocates): far below the 16 MiB announced.
		if limit := uint64(256<<10 + 12*sent); got > limit {
			t.Fatalf("%d bytes allocated for %d received of %d announced", got, sent, maxMessageSize)
		}
	}

	big := mustAppend(t, nil, nil, bytes.Repeat([]byte("x"), 3<<20))
	// A large message is decoded whole, and its buffer goes with the next.
	small := mustAppend(t, nil, eventHeaders(), []byte("small"))
	d := newDecoder(bytes.NewReader(append(bytes.Clone(big), append(bytes.Clone(small), small...)...)))
	if msg, err := d.Decode(); err != nil || len(msg.Payload) != 3<<20 {
		t.Fatalf("large message: %v", err)
	}
	var kept *byte
	for i := 0; i < 2; i++ {
		msg, err := d.Decode()
		if err != nil || string(msg.Payload) != "small" {
			t.Fatalf("message after the large one: %q %v", msg.Payload, err)
		}
		if cap(d.buf) > retainSize {
			t.Fatalf("a buffer of %d bytes is kept", cap(d.buf))
		}
		// An ordinary buffer is reused, not allocated per message.
		if i == 1 && kept != &d.buf[0] {
			t.Fatal("the small buffer was not reused")
		}
		kept = &d.buf[0]
	}
}

// Header blocks with correct checksums and broken contents.
func TestMalformedHeaders(t *testing.T) {
	str := func(name, value string) []byte {
		b := append([]byte{byte(len(name))}, name...)
		b = append(b, typeString)
		b = binary.BigEndian.AppendUint16(b, uint16(len(value)))
		return append(b, value...)
	}
	good := str(":message-type", "event")
	for name, headers := range map[string][]byte{
		"name past the end":      {5, 'a'},
		"no value type":          {1, 'a'},
		"unknown value type":     {1, 'a', 10},
		"string length cut":      {1, 'a', typeString, 0},
		"string past the end":    {1, 'a', typeString, 0, 9, 'x'},
		"int32 cut":              {1, 'a', typeInt32, 0, 0},
		"uuid cut":               append([]byte{1, 'a', typeUUID}, make([]byte, 15)...),
		"second header is bad":   append(bytes.Clone(good), 1, 'a', 77),
		"trailing byte":          append(bytes.Clone(good), 0),
		"bytes value past end":   {1, 'a', typeBytes, 0xff, 0xff},
		"timestamp cut":          {1, 'a', typeTimestamp, 1, 2, 3},
		"name fills whole block": {3, 'a', 'b', 'c'},
	} {
		if _, err := newDecoder(bytes.NewReader(rawFrame(headers, []byte("{}")))).Decode(); !errors.Is(err, errCorrupt) {
			t.Errorf("%s: %v", name, err)
		}
	}
	msg, err := newDecoder(bytes.NewReader(rawFrame(append(str("a", ""), good...), nil))).Decode()
	if err != nil || len(msg.Headers) != 2 || msg.Header(HeaderMessageType) != "event" || len(msg.Payload) != 0 {
		t.Fatalf("good block: %v %v", msg, err)
	}
	// What the official decoder accepts although no encoder writes it, and
	// stays accepted: a header without a name.
	msg, err = newDecoder(bytes.NewReader(rawFrame(append(str("", "x"), str("a", "1")...), nil))).Decode()
	if err != nil || len(msg.Headers) != 2 || msg.Header("") != "x" || msg.Header("a") != "1" {
		t.Fatalf("lenient block: %v %v", msg, err)
	}
}

// A message repeating a header name is corrupt, whatever the value types and
// wherever the repeat stands: the official decoder would keep the last value
// only, and the headers that route a message must not be replaceable.
func TestRepeatedHeaders(t *testing.T) {
	str := func(name, value string) []byte {
		b := append([]byte{byte(len(name))}, name...)
		b = append(b, typeString)
		b = binary.BigEndian.AppendUint16(b, uint16(len(value)))
		return append(b, value...)
	}
	typed := func(name string, typ byte, value ...byte) []byte {
		return append(append(append([]byte{byte(len(name))}, name...), typ), value...)
	}
	join := func(headers ...[]byte) []byte { return bytes.Join(headers, nil) }
	event, chunk := str(HeaderMessageType, MessageTypeEvent), str(HeaderEventType, "chunk")

	for name, headers := range map[string][]byte{
		"string then string":         join(event, chunk, str(HeaderMessageType, messageTypeException)),
		"same string twice":          join(event, event),
		"string then int32":          join(event, chunk, typed(HeaderMessageType, typeInt32, 0, 0, 0, 1)),
		"string then bool":           join(event, typed(HeaderMessageType, 0)),
		"int32 then string":          join(typed(HeaderMessageType, typeInt32, 0, 0, 0, 1), event),
		"bool then bool":             join(event, typed("flag", 0), typed("flag", 1)),
		"event type repeated":        join(event, chunk, str(HeaderEventType, "other")),
		"event type then uuid":       join(event, chunk, typed(HeaderEventType, typeUUID, make([]byte, 16)...)),
		"exception type repeated":    join(str(HeaderMessageType, messageTypeException), str(headerExceptionType, "a"), str(headerExceptionType, "b")),
		"exception type then bytes":  join(str(headerExceptionType, "a"), typed(headerExceptionType, typeBytes, 0, 1, 'x')),
		"an unrelated header":        join(event, str("x", "1"), chunk, str("x", "2")),
		"an empty name":              join(str("", "1"), event, str("", "2")),
		"three of a name":            join(event, event, event),
		"value over the limit":       join(event, typed("big", typeString, append([]byte{0x80, 0x00}, make([]byte, 1<<15)...)...)),
		"bytes value over the limit": join(event, typed("big", typeBytes, append([]byte{0xff, 0xff}, make([]byte, 1<<16-1)...)...)),
	} {
		frame := rawFrame(headers, chunkPayload(evStart))
		if msg, err := newDecoder(bytes.NewReader(frame)).Decode(); !errors.Is(err, errCorrupt) || len(msg.Headers) != 0 || msg.Payload != nil {
			t.Errorf("%s: decoded %v, err %v", name, msg, err)
		}
		// The SSE view delivers what came before the frame and then fails:
		// the frame is neither skipped nor read as an event or an exception.
		stream := append(chunkFrame(t, evStop), frame...)
		got, err := io.ReadAll(NewChunkSSEReader(bytes.NewReader(stream)))
		if want := "event: message_stop\ndata: " + evStop + "\n\n"; !errors.Is(err, errCorrupt) || string(got) != want {
			t.Errorf("%s: sse %q, err %v", name, got, err)
		}
	}

	// Headers of every type under distinct names, at the format's limits,
	// are read as before.
	block := join(event, chunk, typed("b", 0), typed("B", 1), typed("i", typeInt32, 0, 0, 0, 1), typed("u", typeUUID, make([]byte, 16)...),
		typed("t", typeTimestamp, make([]byte, 8)...), typed("y", typeBytes, append([]byte{0x7f, 0xff}, make([]byte, 1<<15-1)...)...))
	msg, err := newDecoder(bytes.NewReader(rawFrame(block, nil))).Decode()
	if err != nil || len(msg.Headers) != 2 || msg.Header(HeaderMessageType) != MessageTypeEvent || msg.Header(HeaderEventType) != "chunk" {
		t.Fatalf("distinct names: %v %v", msg.Headers, err)
	}
}

func FuzzDecoder(f *testing.F) {
	for _, v := range sdkVectors(f) {
		f.Add(v.raw)
		f.Add(v.raw[:len(v.raw)/2])
	}
	f.Add(rawPrelude(maxMessageSize, 0))
	f.Add(rawFrame([]byte{1, 'a', 10}, nil))
	f.Add(rawFrame([]byte{1, 'a', 0, 1, 'a', 1}, nil))
	f.Fuzz(func(t *testing.T, data []byte) {
		d := newDecoder(bytes.NewReader(data))
		read := 0
		for {
			msg, err := d.Decode()
			if err != nil {
				if err != io.EOF && err != io.ErrUnexpectedEOF && !errors.Is(err, errCorrupt) {
					t.Fatalf("unexpected error %v", err)
				}
				break
			}
			// Messages never claim more bytes than the input holds.
			read += minMessageSize + len(msg.Payload)
			if read > len(data) {
				t.Fatalf("decoded more than the input holds")
			}
		}
		// The SSE view never panics and ends the same way.
		if _, err := io.Copy(io.Discard, NewChunkSSEReader(bytes.NewReader(data))); err != nil &&
			err != io.ErrUnexpectedEOF && !errors.Is(err, errCorrupt) {
			t.Fatalf("sse: %v", err)
		}
	})
}

// ---------------------------------------------------------------- SSE

const (
	evStart = `{"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":25,"output_tokens":1}}}`
	evDelta = `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi\nthere"}}`
	evStop  = `{"type":"message_stop","amazon-bedrock-invocationMetrics":{"inputTokenCount":25,"outputTokenCount":12}}`
)

func sseStream(t testing.TB) (stream []byte, want string) {
	stream = chunkFrame(t, evStart)
	// Messages that are not model events are skipped.
	stream = mustAppend(t, stream, []Header{{HeaderEventType, "initial-response"}, {HeaderMessageType, MessageTypeEvent}}, []byte(`{}`))
	stream = mustAppend(t, stream, []Header{{HeaderMessageType, "something-new"}}, chunkPayload(evDelta))
	stream = mustAppend(t, stream, nil, chunkPayload(evDelta))
	stream = mustAppend(t, stream, eventHeaders(), []byte(`not json`))
	stream = mustAppend(t, stream, eventHeaders(), []byte(`{"bytes":""}`))
	stream = append(stream, chunkFrame(t, evDelta)...)
	// No type: only the data line.
	stream = append(stream, chunkFrame(t, `{"completion":"x"}`)...)
	stream = append(stream, chunkFrame(t, evStop)...)
	want = "event: message_start\ndata: " + evStart + "\n\n" +
		"event: content_block_delta\ndata: " + evDelta + "\n\n" +
		"data: {\"completion\":\"x\"}\n\n" +
		"event: message_stop\ndata: " + evStop + "\n\n"
	return stream, want
}

func TestChunkSSEReader(t *testing.T) {
	if FormatChunk != "aws-eventstream-chunk" {
		t.Fatalf("the format name is part of the plugin protocol: %q", FormatChunk)
	}
	// The largest message the format allows passes; other event shapes come
	// out empty.
	largest := mustAppend(t, nil, nil, make([]byte, maxMessageSize-minMessageSize))
	converse := mustAppend(t, largest, []Header{{HeaderMessageType, MessageTypeEvent}, {HeaderEventType, "contentBlockDelta"}}, []byte(`{"contentBlockIndex":0,"delta":{"text":"x"}}`))
	if got, err := io.ReadAll(NewChunkSSEReader(bytes.NewReader(converse))); err != nil || len(got) != 0 {
		t.Fatalf("messages without chunk bytes: %q %v", got, err)
	}
	stream, want := sseStream(t)
	got, err := io.ReadAll(NewChunkSSEReader(bytes.NewReader(stream)))
	if err != nil || string(got) != want {
		t.Fatalf("err %v\n got %q\nwant %q", err, got, want)
	}
	if got, err := io.ReadAll(NewChunkSSEReader(bytes.NewReader(nil))); err != nil || len(got) != 0 {
		t.Fatalf("empty stream: %q %v", got, err)
	}
}

// The output does not depend on how the bytes arrive or how they are read.
func TestChunkSSEReaderChunkBoundaries(t *testing.T) {
	stream, want := sseStream(t)
	readers := map[string]func() io.Reader{
		"one byte at a time": func() io.Reader { return iotest.OneByteReader(bytes.NewReader(stream)) },
		"half reads":         func() io.Reader { return iotest.HalfReader(bytes.NewReader(stream)) },
		"data with errors":   func() io.Reader { return iotest.DataErrReader(bytes.NewReader(stream)) },
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 50; i++ {
		readers["random splits "+string(rune('a'+i%26))+string(rune('a'+i/26))] = func() io.Reader {
			return &splitReader{data: stream, rng: rng}
		}
	}
	for name, mk := range readers {
		got, err := io.ReadAll(NewChunkSSEReader(mk()))
		if err != nil || string(got) != want {
			t.Errorf("%s: err %v, got %q", name, err, got)
		}
	}
	// A caller reading into a tiny buffer gets the same bytes.
	var out []byte
	r, p := NewChunkSSEReader(bytes.NewReader(stream)), make([]byte, 3)
	for {
		n, err := r.Read(p)
		out = append(out, p[:n]...)
		if err != nil {
			if err != io.EOF {
				t.Fatal(err)
			}
			break
		}
	}
	if string(out) != want {
		t.Errorf("small buffer: %q", out)
	}
	if n, err := r.Read(nil); n != 0 || err != nil {
		t.Errorf("empty read: %d %v", n, err)
	}
}

// splitReader returns the data in pieces of random size.
type splitReader struct {
	data []byte
	rng  *rand.Rand
}

func (s *splitReader) Read(p []byte) (int, error) {
	if len(s.data) == 0 {
		return 0, io.EOF
	}
	n := min(1+s.rng.IntN(40), len(s.data), len(p))
	copy(p, s.data[:n])
	s.data = s.data[n:]
	return n, nil
}

func TestChunkSSEReaderExceptions(t *testing.T) {
	v := sdkVectors(t)
	after := chunkFrame(t, evStop) // never delivered: the error ends the stream
	for name, tc := range map[string]struct {
		frame []byte
		want  string
	}{
		"exception": {v["exception-throttling"].raw,
			`{"type":"error","error":{"type":"throttlingException","message":"Too many requests, please wait before trying again."}}`},
		"error": {v["error-internal"].raw,
			`{"type":"error","error":{"type":"InternalFailure","message":"An internal error occurred"}}`},
		"exception with a plain payload": {mustAppend(t, nil, []Header{{HeaderMessageType, messageTypeException}, {headerExceptionType, "modelStreamErrorException"}}, []byte(" boom \"quoted\"\n")),
			`{"type":"error","error":{"type":"modelStreamErrorException","message":"boom \"quoted\""}}`},
		"exception without details": {mustAppend(t, nil, []Header{{HeaderMessageType, messageTypeException}}, nil),
			`{"type":"error","error":{"type":"upstream_error","message":"upstream_error"}}`},
		"error without details": {mustAppend(t, nil, []Header{{HeaderMessageType, messageTypeError}}, nil),
			`{"type":"error","error":{"type":"upstream_error","message":"upstream_error"}}`},
	} {
		stream := append(append(chunkFrame(t, evStart), tc.frame...), after...)
		got, err := io.ReadAll(NewChunkSSEReader(bytes.NewReader(stream)))
		want := "event: message_start\ndata: " + evStart + "\n\nevent: error\ndata: " + tc.want + "\n\n"
		if err != nil || string(got) != want {
			t.Errorf("%s: err %v\n got %q\nwant %q", name, err, got, want)
		}
	}
}

// Data never spans lines: JSON is compacted, other text gets a data line
// per line, and an event name cannot inject a field.
func TestChunkSSEReaderLineBreaks(t *testing.T) {
	stream := chunkFrame(t, "{\n  \"type\": \"ping\",\r\n  \"n\": 1\n}")
	stream = append(stream, chunkFrame(t, "plain\ntext\r\n\r\nend")...)
	stream = append(stream, chunkFrame(t, `{"type":"a\nevent: forged","x":1}`)...)
	got, err := io.ReadAll(NewChunkSSEReader(bytes.NewReader(stream)))
	want := "event: ping\ndata: {\"type\":\"ping\",\"n\":1}\n\n" +
		"data: plain\ndata: text\ndata: end\n\n" +
		"data: {\"type\":\"a\\nevent: forged\",\"x\":1}\n\n"
	if err != nil || string(got) != want {
		t.Fatalf("err %v\n got %q\nwant %q", err, got, want)
	}
}

// A damaged stream delivers what came before the damage and then fails.
func TestChunkSSEReaderCorruptAndTruncated(t *testing.T) {
	good, bad := chunkFrame(t, evStart), chunkFrame(t, evDelta)
	want := "event: message_start\ndata: " + evStart + "\n\n"
	corrupt := bytes.Clone(bad)
	corrupt[len(corrupt)-6] ^= 0x10
	for name, tc := range map[string]struct {
		tail []byte
		is   error
	}{
		"corrupt payload":      {corrupt, errCorrupt},
		"cut in the prelude":   {bad[:5], io.ErrUnexpectedEOF},
		"cut in the message":   {bad[:len(bad)-1], io.ErrUnexpectedEOF},
		"oversized":            {rawPrelude(maxMessageSize+1, 0), errCorrupt},
		"prelude checksum":     {append(rawPrelude(64, 0)[:11], 0), errCorrupt},
		"headers past the end": {rawPrelude(64, 64), errCorrupt},
		"garbage":              {[]byte("HTTP/1.1 500 Internal Server Error\r\n\r\n"), errCorrupt},
	} {
		r := NewChunkSSEReader(bytes.NewReader(append(bytes.Clone(good), tc.tail...)))
		got, err := io.ReadAll(r)
		if !errors.Is(err, tc.is) || string(got) != want {
			t.Errorf("%s: err %v, got %q", name, err, got)
		}
		// The failure is sticky.
		if n, again := r.Read(make([]byte, 8)); n != 0 || !errors.Is(again, tc.is) {
			t.Errorf("%s: second read %d %v", name, n, again)
		}
	}
}

// A chunk event whose message is larger than any fixed cap would allow
// (here 7 MiB, the adapter used to cut a stream at 4 MiB) comes out whole:
// only the format's own 16 MiB bounds a message.
func TestChunkSSEReaderLargeEvent(t *testing.T) {
	text := strings.Repeat("x", 5<<20)
	event := `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"` + text + `"}}`
	frame := chunkFrame(t, event)
	if len(frame) <= 4<<20 || len(frame) > maxMessageSize {
		t.Fatalf("frame of %d bytes", len(frame))
	}
	stream := append(frame, chunkFrame(t, evStop)...)
	got, err := io.ReadAll(NewChunkSSEReader(iotest.HalfReader(bytes.NewReader(stream))))
	want := "event: content_block_delta\ndata: " + event + "\n\nevent: message_stop\ndata: " + evStop + "\n\n"
	if err != nil || string(got) != want {
		t.Fatalf("err %v, %d bytes, want %d", err, len(got), len(want))
	}
}

// An event is readable as soon as its message is complete, while the stream
// stays open and nothing else arrives.
func TestChunkSSEReaderDeliversEachMessageAtOnce(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	r := NewChunkSSEReader(pr)
	type read struct {
		data string
		err  error
	}
	next := func() read {
		done := make(chan read, 1)
		go func() {
			p := make([]byte, 4096)
			n, err := r.Read(p)
			done <- read{string(p[:n]), err}
		}()
		select {
		case got := <-done:
			return got
		case <-time.After(5 * time.Second):
			t.Fatal("read blocked although a complete message was written")
			return read{}
		}
	}
	for _, ev := range []string{evStart, evDelta} {
		frame := chunkFrame(t, ev)
		// The message arrives in two writes; only the second completes it.
		go func() {
			_, _ = pw.Write(frame[:7])
			_, _ = pw.Write(frame[7:])
		}()
		got := next()
		if got.err != nil || !strings.HasSuffix(got.data, "data: "+ev+"\n\n") {
			t.Fatalf("%q %v", got.data, got.err)
		}
	}
	// Cancelling or closing the source ends the read with the source's error.
	pw.CloseWithError(context.Canceled)
	if got := next(); !errors.Is(got.err, context.Canceled) || got.data != "" {
		t.Fatalf("after cancel: %q %v", got.data, got.err)
	}
}

// A reader failing between messages or inside one is reported as it is.
func TestChunkSSEReaderPropagatesReaderErrors(t *testing.T) {
	frame := chunkFrame(t, evStart)
	boom := errors.New("connection reset")
	for name, src := range map[string]io.Reader{
		"between messages": io.MultiReader(bytes.NewReader(frame), iotest.ErrReader(boom)),
		"inside a message": io.MultiReader(bytes.NewReader(frame), bytes.NewReader(frame[:20]), iotest.ErrReader(boom)),
		"timeout":          io.MultiReader(bytes.NewReader(frame), iotest.TimeoutReader(bytes.NewReader(frame))),
	} {
		got, err := io.ReadAll(NewChunkSSEReader(src))
		if err == nil || (!errors.Is(err, boom) && !errors.Is(err, iotest.ErrTimeout)) {
			t.Errorf("%s: %v", name, err)
		}
		if !strings.HasPrefix(string(got), "event: message_start\n") {
			t.Errorf("%s: %q", name, got)
		}
	}
}
