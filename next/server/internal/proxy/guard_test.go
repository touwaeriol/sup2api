package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestDirectClientGuard: the direct client refuses a loopback upstream unless
// AllowPrivate is set.
func TestDirectClientGuard(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()
	ctx := context.Background()

	guarded, err := New(nil, nil, nil, Options{}).HTTPClient(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if _, err := guarded.Client.Do(req); err == nil {
		t.Fatal("loopback upstream reached through the guarded direct client")
	}

	open, err := New(nil, nil, nil, Options{AllowPrivate: true}).HTTPClient(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := open.Client.Do(req)
	if err != nil {
		t.Fatalf("AllowPrivate: %v", err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(b) != "ok" {
		t.Fatalf("body %q", b)
	}
}
