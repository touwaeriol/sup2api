package gateway

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
)

// spoolMemoryLimit is how much of an answer stays in memory; the rest
// spills to a temp file. Nothing caps the answer's size.
const spoolMemoryLimit = 1 << 20

// errSpool marks a failure of the spool itself (its temp file): a local
// error, never the client going away.
var errSpool = errors.New("response spool failed")

// bufferedResponse keeps a successful non-streaming answer private until its
// accounting receipt commits. Large answers spill to an owner-only temp file.
type bufferedResponse struct {
	gin.ResponseWriter
	header       http.Header
	status, size int
	mem          bytes.Buffer
	file         *os.File
	err          error
	written      bool
}

func newBufferedResponse(w gin.ResponseWriter) *bufferedResponse {
	return &bufferedResponse{ResponseWriter: w, header: w.Header().Clone(), status: 200}
}
func (w *bufferedResponse) Header() http.Header { return w.header }
func (w *bufferedResponse) Status() int         { return w.status }
func (w *bufferedResponse) Size() int {
	if !w.written {
		return -1
	}
	return w.size
}
func (w *bufferedResponse) Written() bool { return w.written }
func (w *bufferedResponse) WriteHeader(code int) {
	if !w.written {
		w.status = code
	}
}
func (w *bufferedResponse) WriteHeaderNow()                   { w.written = true }
func (w *bufferedResponse) Flush()                            { w.written = true }
func (w *bufferedResponse) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *bufferedResponse) Write(p []byte) (int, error) {
	w.written = true
	if w.err != nil {
		return 0, w.err
	}
	n, err := w.write(p)
	if err != nil {
		w.err = fmt.Errorf("%w: %w", errSpool, err)
	}
	return n, w.err
}

func (w *bufferedResponse) write(p []byte) (int, error) {
	if w.file == nil && w.size+len(p) > spoolMemoryLimit {
		f, err := os.CreateTemp("", "sup2api-response-*")
		if err != nil {
			return 0, err
		}
		w.file = f
		if _, err := f.Write(w.mem.Bytes()); err != nil {
			return 0, err
		}
		w.mem.Reset()
	}
	var n int
	var err error
	if w.file == nil {
		n, err = w.mem.Write(p)
	} else {
		n, err = w.file.Write(p)
	}
	w.size += n
	return n, err
}

// close removes the temp file, if any; calling it again does nothing.
func (w *bufferedResponse) close() {
	if w.file != nil {
		name := w.file.Name()
		_ = w.file.Close()
		_ = os.Remove(name)
		w.file = nil
	}
}
func (w *bufferedResponse) publish() error {
	if w.err != nil {
		return w.err
	}
	for k, v := range w.header {
		w.ResponseWriter.Header()[k] = v
	}
	w.ResponseWriter.WriteHeader(w.status)
	var r io.Reader = &w.mem
	if w.file != nil {
		if _, err := w.file.Seek(0, 0); err != nil {
			return err
		}
		r = w.file
	}
	_, err := io.Copy(w.ResponseWriter, r)
	return err
}
