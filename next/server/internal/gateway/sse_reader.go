package gateway

import (
	"bufio"
	"bytes"
	"io"
	"strings"
)

// sseReader reads an event stream one event at a time. With keep it also
// keeps every byte it read, for a caller that relays them afterwards.
type sseReader struct {
	br    *bufio.Reader
	keep  bool
	read  []byte
	event string
	data  []byte
}

// newSSEReader reads r through a buffer of size bytes (a buffer, not a
// limit: readLine gathers longer lines).
func newSSEReader(r io.Reader, size int, keep bool) *sseReader {
	return &sseReader{br: bufio.NewReaderSize(r, size), keep: keep}
}

// next returns the next event that has a name or data; comments and empty
// blocks are skipped. The data is valid until the next call. The last event
// may lack its blank line: it is returned at EOF, and io.EOF on the call
// after. Any other read error drops the event in progress.
func (r *sseReader) next() (string, []byte, error) {
	for {
		line, err := readLine(r.br)
		if r.keep {
			r.read = append(r.read, line...)
		}
		t := bytes.TrimRight(line, "\r\n")
		if len(line) > 0 && len(t) == 0 && r.pending() {
			return r.take()
		}
		r.field(t)
		if err == io.EOF && r.pending() {
			return r.take()
		}
		if err != nil {
			return "", nil, err
		}
	}
}

func (r *sseReader) pending() bool { return r.event != "" || len(r.data) > 0 }

func (r *sseReader) take() (string, []byte, error) {
	event, data := r.event, r.data
	r.event, r.data = "", r.data[:0]
	return event, data, nil
}

// field records an event: or data: line; other fields are ignored.
func (r *sseReader) field(t []byte) {
	switch {
	case bytes.HasPrefix(t, []byte("event:")):
		r.event = strings.TrimSpace(string(t[len("event:"):]))
	case bytes.HasPrefix(t, []byte("data:")):
		d := t[len("data:"):]
		if len(d) > 0 && d[0] == ' ' {
			d = d[1:]
		}
		if len(r.data) > 0 {
			r.data = append(r.data, '\n')
		}
		r.data = append(r.data, d...)
	}
}

// rest is the stream from the first byte next has not returned: the bytes
// kept so far (with keep) and then whatever the reader still holds.
func (r *sseReader) rest() io.Reader {
	return io.MultiReader(bytes.NewReader(r.read), r.br)
}

// unfinished names a stream that ended (io.EOF) before the event its reader
// was waiting for; other read errors stay as they are.
func unfinished(err, ended error) error {
	if err == io.EOF {
		return ended
	}
	return err
}
