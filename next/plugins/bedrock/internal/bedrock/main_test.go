package bedrock

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
)

// dials counts the connections something in the test binary tried to open
// through the default transport.
var dials atomic.Int64

// TestMain makes the default transport fail on dial for the whole package:
// the plugin opens no socket, it hands its requests to the core (design
// §14.1 rule 8). The fake Bedrock is called as a handler, without a server.
func TestMain(m *testing.M) {
	refuse := func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, fmt.Errorf("the plugin must not dial")
	}
	http.DefaultTransport = &http.Transport{DialContext: refuse, DialTLSContext: refuse}
	code := m.Run()
	if n := dials.Load(); n > 0 {
		fmt.Fprintf(os.Stderr, "the tests dialed %d time(s) through the default transport\n", n)
		code = 1
	}
	os.Exit(code)
}
