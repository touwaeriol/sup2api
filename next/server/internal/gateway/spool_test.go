package gateway

import (
	"bytes"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestResponseSpoolSpillsAndRemovesPrivateFile(t *testing.T) {
	rr := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rr)
	spool := newBufferedResponse(ctx.Writer)
	data := bytes.Repeat([]byte("s"), 2<<20)
	if _, err := spool.Write(data); err != nil {
		t.Fatal(err)
	}
	if spool.file == nil || rr.Body.Len() != 0 {
		t.Fatal("spool did not isolate response")
	}
	path := spool.file.Name()
	if err := spool.publish(); err != nil {
		t.Fatal(err)
	}
	spool.close()
	if !bytes.Equal(rr.Body.Bytes(), data) {
		t.Fatal("spilled response corrupted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("temporary response retained", err)
	}
}

// A spool failure is a local error: the relay must not take it for the client
// leaving, even with the answer read on for an absent client.
func TestResponseSpoolFailureIsNoHangUp(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	spool := newBufferedResponse(ctx.Writer)
	spool.err = errSpool // the temp file failed
	var canceled error
	d := &upstreamDrain{cancel: func(err error) { canceled = err }, keep: true}
	d.arm()
	w := &clientWriter{ResponseWriter: spool, drain: d}
	_, err := w.Write([]byte("s"))
	if !errors.Is(err, errSpool) || !errors.Is(relayWriteError(err), errSpool) {
		t.Fatalf("spool failure surfaced as %v", err)
	}
	if d.left || d.outputDropped() || canceled != nil {
		t.Fatal("spool failure recorded as a hang-up")
	}
	if !errors.Is(relayWriteError(io.ErrClosedPipe), errClientGone) {
		t.Fatal("client write failure not reported as a hang-up")
	}
}
