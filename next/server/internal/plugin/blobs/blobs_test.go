package blobs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func sumOf(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func TestDirRejectsWrongBytesAndReportsMissing(t *testing.T) {
	ctx := context.Background()
	d := Dir(t.TempDir())
	data := []byte("package")
	if err := d.Put(ctx, sumOf([]byte("other")), data); err == nil {
		t.Fatal("bytes stored under another digest")
	}
	if err := d.Put(ctx, sumOf(data), data); err != nil {
		t.Fatal(err)
	}
	if got, err := d.Get(ctx, sumOf(data)); err != nil || string(got) != "package" {
		t.Fatalf("get: %q %v", got, err)
	}
	if _, err := d.Get(ctx, sumOf([]byte("absent"))); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing package: %v", err)
	}
	if _, err := d.Get(ctx, "../../etc/passwd"); err == nil {
		t.Fatal("non-digest name accepted")
	}
	if ok, err := d.Has(ctx, sumOf(data)); !ok || err != nil {
		t.Fatalf("has stored: %v %v", ok, err)
	}
	if ok, err := d.Has(ctx, sumOf([]byte("absent"))); ok || err != nil {
		t.Fatalf("has missing: %v %v", ok, err)
	}
	if _, err := d.Has(ctx, "../../etc/passwd"); err == nil {
		t.Fatal("non-digest name accepted by Has")
	}
}

func TestSourcePrefersMarketAndFallsBackToStore(t *testing.T) {
	ctx := context.Background()
	data := []byte("market package")
	sum := sumOf(data)
	store := Dir(t.TempDir())
	var downloads int
	market := map[string][]byte{"https://market/ok": data, "https://market/tampered": []byte("evil")}
	src := &Source{Store: store, MaxBytes: 1 << 20, Download: func(_ context.Context, url string, _ int64) ([]byte, error) {
		downloads++
		if b, ok := market[url]; ok {
			return b, nil
		}
		return nil, errors.New("market unreachable")
	}}
	if got, err := src.Fetch(ctx, sum, "https://market/ok"); err != nil || string(got) != string(data) || downloads != 1 {
		t.Fatalf("market download: %q %v %d", got, err, downloads)
	}
	// Unreachable or tampered market content falls back to the store.
	for _, url := range []string{"https://market/down", "https://market/tampered"} {
		if _, err := src.Fetch(ctx, sum, url); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s without a stored copy: %v", url, err)
		}
	}
	if err := store.Put(ctx, sum, data); err != nil {
		t.Fatal(err)
	}
	if got, err := src.Fetch(ctx, sum, "https://market/down"); err != nil || string(got) != string(data) {
		t.Fatalf("store fallback: %q %v", got, err)
	}
	// Uploads have no URL and never touch the market.
	before := downloads
	if _, err := src.Fetch(ctx, sum, ""); err != nil || downloads != before {
		t.Fatalf("upload fetch: %v downloads=%d", err, downloads-before)
	}
}

func TestShellStoreSpeaksTheManagementSocket(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "shell.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Skip("unix sockets unavailable:", err)
	}
	var (
		mu     sync.Mutex
		ranges []string
	)
	stored := map[string][]byte{}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sum := strings.TrimPrefix(r.URL.Path, "/system/plugin-blobs/")
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			stored[sum] = b
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			b, ok := stored[sum]
			if !ok {
				http.Error(w, "no reachable node holds the plugin package (b: not held; a: unreachable)", http.StatusNotFound)
				return
			}
			ranges = append(ranges, r.Header.Get("Range"))
			http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(b)) // as the shell serves it
		}
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { _ = srv.Close() })
	ctx := context.Background()
	s := NewShell(socket, "", 1<<20)
	data := []byte("uploaded package")
	if err = s.Put(ctx, sumOf(data), data); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get(ctx, sumOf(data)); err != nil || string(got) != string(data) {
		t.Fatalf("get: %q %v", got, err)
	}
	// A package no reachable node holds is ErrNotFound with the shell's reason.
	if _, err = s.Get(ctx, sumOf([]byte("absent"))); !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "a: unreachable") {
		t.Fatalf("missing: %v", err)
	}
	// Has asks for one byte, not the package.
	if ok, err := s.Has(ctx, sumOf(data)); !ok || err != nil {
		t.Fatalf("has stored: %v %v", ok, err)
	}
	if ok, err := s.Has(ctx, sumOf([]byte("absent"))); ok || err != nil {
		t.Fatalf("has missing: %v %v", ok, err)
	}
	mu.Lock()
	if last := ranges[len(ranges)-1]; last != "bytes=0-0" {
		t.Fatalf("Has asked for range %q", last)
	}
	mu.Unlock()
	// A shell answering with other bytes is rejected.
	mu.Lock()
	stored[sumOf(data)] = []byte("swapped")
	mu.Unlock()
	if _, err = s.Get(ctx, sumOf(data)); err == nil {
		t.Fatal("swapped bytes accepted")
	}
}
