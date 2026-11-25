package usagerules

import (
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// The last error event is kept as sent, independent of the caller's buffer;
// a response.failed is kept for the log without marking the stream failed.
func TestStreamErrorEventKept(t *testing.T) {
	u := New(manifest.UsageRules{})
	buf := []byte(`{"type":"error","code":"server_is_overloaded","message":"busy"}`)
	u.ApplySSE("error", buf)
	buf[2] = 'X'
	if u.StreamError != "upstream stream error: busy" || u.StreamErrorEvent != "error" ||
		string(u.StreamErrorData) != `{"type":"error","code":"server_is_overloaded","message":"busy"}` {
		t.Fatalf("error event: %q %q %s", u.StreamError, u.StreamErrorEvent, u.StreamErrorData)
	}

	f := New(manifest.UsageRules{})
	f.ApplySSE("response.failed", []byte(`{"type":"response.failed","response":{"error":{"message":"melted"}}}`))
	if f.StreamError != "" || f.StreamErrorEvent != "response.failed" || len(f.StreamErrorData) == 0 {
		t.Fatalf("response.failed: %q %q %s", f.StreamError, f.StreamErrorEvent, f.StreamErrorData)
	}
}
