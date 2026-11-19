package pluginblob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// testNode is one node's package service with its private endpoint.
type testNode struct {
	id     string
	svc    *Service
	srv    *httptest.Server
	online bool
	asked  atomic.Int64 // requests its private endpoint received
}

// cluster wires nodes the way the shells are wired: each node's Peers lists
// the others, marked online as their heartbeat would, and its client reaches
// their private package endpoints.
func cluster(t *testing.T, ids ...string) map[string]*testNode {
	t.Helper()
	nodes := map[string]*testNode{}
	for _, id := range ids {
		n := &testNode{id: id, online: true, svc: &Service{Store: &Store{Dir: t.TempDir()}, Client: http.DefaultClient}}
		peer := n.svc.Peer()
		n.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n.asked.Add(1)
			peer.ServeHTTP(w, r)
		}))
		t.Cleanup(n.srv.Close)
		nodes[id] = n
	}
	for _, id := range ids {
		self := id
		nodes[id].svc.Peers = func(context.Context) ([]NodeRef, error) {
			var refs []NodeRef
			for _, other := range ids {
				if other != self {
					n := nodes[other]
					refs = append(refs, NodeRef{ID: n.id, PeerURL: n.srv.URL, Online: n.online})
				}
			}
			return refs, nil
		}
	}
	return nodes
}

func call(t *testing.T, h http.Handler, method, sum string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, "/system/plugin-blobs/"+sum, bytes.NewReader(body)))
	return w
}

// plant stores raw bytes under sum on a node, bypassing the digest check.
func plant(t *testing.T, s *Store, sum string, raw []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(s.Dir, sum[:2]), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.path(sum), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestUploadStaysOnTheReceivingNode(t *testing.T) {
	c := cluster(t, "a", "b", "c")
	data := []byte("uploaded on b")
	if w := call(t, c["b"].svc.Local(), http.MethodPut, digest(data), data); w.Code != http.StatusNoContent {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	if !c["b"].svc.Store.Has(digest(data)) || c["a"].svc.Store.Has(digest(data)) || c["c"].svc.Store.Has(digest(data)) {
		t.Fatal("an upload is stored on the node that took it, and only there")
	}
	other := digest([]byte("never uploaded"))
	if w := call(t, c["b"].svc.Local(), http.MethodPut, other, []byte("other bytes")); w.Code != http.StatusBadRequest || c["b"].svc.Store.Has(other) {
		t.Fatalf("mismatched upload: %d", w.Code)
	}
}

// The primary (a) is down: a node lacking the package (c, new or without
// its cache) fetches it from another node that holds it and keeps a copy.
func TestFetchFromAnotherNodeWhileThePrimaryIsDown(t *testing.T) {
	c := cluster(t, "a", "b", "c")
	data := []byte("installed everywhere")
	sum := digest(data)
	for _, id := range []string{"a", "b"} {
		if err := c[id].svc.Store.Write(sum, bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
	}
	c["a"].srv.Close()
	c["a"].online = false
	w := call(t, c["c"].svc.Local(), http.MethodGet, sum, nil)
	if w.Code != 200 || w.Body.String() != string(data) || !c["c"].svc.Store.Has(sum) {
		t.Fatalf("fetch with the primary down: %d %q", w.Code, w.Body)
	}
	// A node whose heartbeat is still fresh but that just died is asked,
	// fails, and the next node serves the package.
	other := []byte("held by b only")
	if err := c["b"].svc.Store.Write(digest(other), bytes.NewReader(other)); err != nil {
		t.Fatal(err)
	}
	c["a"].online = true
	if w := call(t, c["c"].svc.Local(), http.MethodGet, digest(other), nil); w.Code != 200 || w.Body.String() != string(other) {
		t.Fatalf("fetch past a dead node: %d %q", w.Code, w.Body)
	}
}

// A holder whose heartbeat is stale (starting, restarted, or not yet run)
// is still asked: freshness orders the nodes, it excludes none.
func TestStaleHolderIsStillAsked(t *testing.T) {
	c := cluster(t, "a", "b", "c")
	data := []byte("held by a node without a fresh heartbeat")
	sum := digest(data)
	if err := c["a"].svc.Store.Write(sum, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	c["a"].online = false
	if w := call(t, c["c"].svc.Local(), http.MethodGet, sum, nil); w.Code != 200 || w.Body.String() != string(data) || !c["c"].svc.Store.Has(sum) {
		t.Fatalf("fetch from a stale holder: %d %q", w.Code, w.Body)
	}
}

// Nodes with a fresh heartbeat are asked before the stale ones, so a node
// that is down does not delay a package a live node holds.
func TestFreshNodesAreAskedFirst(t *testing.T) {
	c := cluster(t, "a", "b", "c")
	data := []byte("held by a and b")
	sum := digest(data)
	for _, id := range []string{"a", "b"} {
		if err := c[id].svc.Store.Write(sum, bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
	}
	c["a"].online = false
	if w := call(t, c["c"].svc.Local(), http.MethodGet, sum, nil); w.Code != 200 || w.Body.String() != string(data) {
		t.Fatalf("fetch: %d %q", w.Code, w.Body)
	}
	if a, b := c["a"].asked.Load(), c["b"].asked.Load(); a != 0 || b != 1 {
		t.Fatalf("stale a asked %d times, fresh b %d; want 0 and 1", a, b)
	}
}

func TestNoNodeHoldsThePackage(t *testing.T) {
	c := cluster(t, "a", "b", "c")
	data := []byte("held by a node that is down")
	sum := digest(data)
	if err := c["a"].svc.Store.Write(sum, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	c["a"].srv.Close()
	c["a"].online = false
	// Every enabled node is asked: b does not hold it, a cannot be reached.
	w := call(t, c["c"].svc.Local(), http.MethodGet, sum, nil)
	body := w.Body.String()
	if w.Code != http.StatusNotFound || !strings.Contains(body, "no reachable node holds the plugin package") ||
		!strings.Contains(body, "b: not held") || !strings.Contains(body, "a: unreachable") || strings.Index(body, "b: not held") > strings.Index(body, "a: unreachable") {
		t.Fatalf("no holder reachable: %d %q", w.Code, body)
	}
	// The probe a core uses (Range 0-0) gets the same answer.
	q := httptest.NewRequest(http.MethodGet, "/system/plugin-blobs/"+sum, nil)
	q.Header.Set("Range", "bytes=0-0")
	rec := httptest.NewRecorder()
	c["b"].svc.Local().ServeHTTP(rec, q)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("probe: %d", rec.Code)
	}
	// A single node has nobody to ask.
	alone := cluster(t, "x")
	if w := call(t, alone["x"].svc.Local(), http.MethodGet, sum, nil); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "no other node to ask") {
		t.Fatalf("single node: %d %q", w.Code, w.Body)
	}
	if w := call(t, alone["x"].svc.Local(), http.MethodGet, "../../etc/passwd", nil); w.Code != http.StatusNotFound {
		t.Fatalf("path escape: %d", w.Code)
	}
}

func TestFetchedBytesThatFailTheDigestAreRejected(t *testing.T) {
	c := cluster(t, "a", "b", "c")
	data := []byte("real")
	sum := digest(data)
	plant(t, c["a"].svc.Store, sum, []byte("fake"))
	if w := call(t, c["c"].svc.Local(), http.MethodGet, sum, nil); w.Code != http.StatusBadGateway ||
		!strings.Contains(w.Body.String(), "digest mismatch") || c["c"].svc.Store.Has(sum) {
		t.Fatalf("tampered copy accepted: %d %q", w.Code, w.Body)
	}
	if entries, _ := os.ReadDir(filepath.Join(c["c"].svc.Store.Dir, sum[:2])); len(entries) != 0 {
		t.Fatal("a rejected copy left a file")
	}
	// Another node with the genuine bytes still serves it.
	if err := c["b"].svc.Store.Write(sum, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if w := call(t, c["c"].svc.Local(), http.MethodGet, sum, nil); w.Code != 200 || w.Body.String() != string(data) {
		t.Fatalf("genuine copy after a tampered one: %d %q", w.Code, w.Body)
	}
}

func TestPeerEndpointOnlyReads(t *testing.T) {
	c := cluster(t, "a")
	data := []byte("pushed")
	w := httptest.NewRecorder()
	c["a"].svc.Peer().ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/internal/plugin-blobs/"+digest(data), bytes.NewReader(data)))
	if w.Code != http.StatusMethodNotAllowed || c["a"].svc.Store.Has(digest(data)) {
		t.Fatalf("peer upload: %d", w.Code)
	}
}

func TestStoreLimitsSizeAndTotal(t *testing.T) {
	s := &Store{Dir: t.TempDir(), MaxBytes: 16, MaxTotalBytes: 20}
	if err := s.Write(digest([]byte("this package is too large")), strings.NewReader("this package is too large")); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized package: %v", err)
	}
	first := []byte("0123456789ab") // 12 bytes
	if err := s.Write(digest(first), bytes.NewReader(first)); err != nil {
		t.Fatal(err)
	}
	// Storing the same package again needs no room.
	if err := s.Write(digest(first), bytes.NewReader(first)); err != nil {
		t.Fatal(err)
	}
	second := []byte("second-pkg") // 10 bytes: 22 > 20 total
	if err := s.Write(digest(second), bytes.NewReader(second)); !errors.Is(err, ErrStoreFull) || s.Has(digest(second)) {
		t.Fatalf("store full: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(s.Dir, digest(second)[:2])); len(entries) != 0 {
		t.Fatal("refused write left a temporary file")
	}
	small := []byte("tiny") // 4 bytes: fits in the remaining 8
	if err := s.Write(digest(small), bytes.NewReader(small)); err != nil {
		t.Fatal(err)
	}
}

func TestStoreCollectsUnreferenced(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	keep, drop, fresh := []byte("keep"), []byte("drop"), []byte("new")
	for _, b := range [][]byte{keep, drop, fresh} {
		if err := s.Write(digest(b), bytes.NewReader(b)); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	for _, b := range [][]byte{keep, drop} {
		if err := os.Chtimes(s.path(digest(b)), old, old); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.GC(map[string]bool{digest(keep): true}, time.Hour)
	if err != nil || n != 1 || !s.Has(digest(keep)) || s.Has(digest(drop)) || !s.Has(digest(fresh)) {
		t.Fatalf("gc removed %d (%v): keep=%v drop=%v fresh=%v", n, err, s.Has(digest(keep)), s.Has(digest(drop)), s.Has(digest(fresh)))
	}
}
