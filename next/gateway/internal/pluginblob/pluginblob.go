// Package pluginblob keeps plugin packages for the cores of a cluster. Each
// node's shell keeps the packages its core stored or used; a package a node
// lacks is fetched from any other enabled node that holds it over the
// authenticated node network. Packages are addressed by sha256 and verified
// on every write.
package pluginblob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	rc "github.com/Sub2API-Devs/sup2api/next/runtime-contract"
)

func validSum(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// DefaultMaxTotalBytes bounds the whole store unless configured: a node
// may upload packages before any row references them, and GC only reclaims
// unreferenced ones after a grace period, so the directory needs a ceiling.
const DefaultMaxTotalBytes = 16 << 30

// Store is a content-addressed directory.
type Store struct {
	Dir           string
	MaxBytes      int64
	MaxTotalBytes int64

	writes sync.Mutex // serializes the used-space check with the write
}

func (s *Store) path(sum string) string { return filepath.Join(s.Dir, sum[:2], sum) }

func (s *Store) max() int64 {
	if s.MaxBytes > 0 {
		return s.MaxBytes
	}
	return rc.PluginPackageMaxBytes
}
func (s *Store) maxTotal() int64 {
	if s.MaxTotalBytes > 0 {
		return s.MaxTotalBytes
	}
	return DefaultMaxTotalBytes
}

// used sums the stored packages and the temporary files of writes in flight.
func (s *Store) used() (int64, error) {
	var total int64
	err := filepath.WalkDir(s.Dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// ErrStoreFull reports that a write would exceed the store's total size.
var ErrStoreFull = errors.New("plugin package store is full")

// Has reports whether the package is stored.
func (s *Store) Has(sum string) bool {
	if !validSum(sum) {
		return false
	}
	_, err := os.Stat(s.path(sum))
	return err == nil
}

// Write stores r under sum after verifying its digest and size.
func (s *Store) Write(sum string, r io.Reader) error {
	if !validSum(sum) {
		return fmt.Errorf("invalid package digest")
	}
	file := s.path(sum)
	if _, err := os.Stat(file); err == nil {
		// Already stored under its digest: nothing to write, no space needed.
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	// The space check and the reservation of a temporary file happen under
	// one lock; the copy itself runs concurrently with other writes, each
	// bounded by the room it was granted.
	s.writes.Lock()
	used, err := s.used()
	if err != nil {
		s.writes.Unlock()
		return err
	}
	room := s.maxTotal() - used
	if room <= 0 {
		s.writes.Unlock()
		return ErrStoreFull
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".write-")
	s.writes.Unlock()
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	limit := s.max()
	if room < limit {
		limit = room
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, limit+1))
	if err == nil && n > limit {
		if limit < s.max() {
			err = ErrStoreFull
		} else {
			err = fmt.Errorf("package exceeds %d bytes", s.max())
		}
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != sum {
		err = errors.New("package digest mismatch")
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), file)
}

func (s *Store) serve(w http.ResponseWriter, r *http.Request, sum string) {
	f, err := os.Open(s.path(sum))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.Error(w, "package unreadable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// GC removes packages no plugin version references, once older than grace;
// the grace period covers uploads stored before their row commits.
func (s *Store) GC(referenced map[string]bool, grace time.Duration) (int, error) {
	removed := 0
	err := filepath.WalkDir(s.Dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || !validSum(d.Name()) || referenced[d.Name()] {
			return nil
		}
		info, err := d.Info()
		if err != nil || time.Since(info.ModTime()) < grace {
			return nil
		}
		if os.Remove(path) == nil {
			removed++
		}
		return nil
	})
	return removed, err
}

// Service answers the local core and the other nodes. A package is kept by
// every node that stored or fetched it; no node is special.
type Service struct {
	Store *Store
	// Peers lists the other enabled nodes, each marked online when its
	// heartbeat is fresh; a missing package is asked from all of them, the
	// online ones first.
	Peers func(context.Context) ([]NodeRef, error)
	// Client is the authenticated node client; it may reach any enabled node.
	Client *http.Client
}

// NodeRef identifies a peer node a package may be fetched from.
type NodeRef struct {
	ID      string
	PeerURL string
	// Online reports a fresh heartbeat; it orders the nodes asked, it does
	// not exclude any.
	Online bool
}

func sumFrom(path, prefix string) (string, bool) {
	sum := strings.TrimPrefix(path, prefix)
	return sum, strings.HasPrefix(path, prefix) && validSum(sum)
}

// Local serves GET and PUT /system/plugin-blobs/<sha256> on the management
// socket for the core of this node.
func (s *Service) Local() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sum, ok := sumFrom(r.URL.Path, "/system/plugin-blobs/")
		if !ok {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			if !s.Store.Has(sum) {
				if err := s.pull(r.Context(), sum); err != nil {
					if errors.Is(err, errNotFound) {
						http.Error(w, err.Error(), http.StatusNotFound)
						return
					}
					http.Error(w, err.Error(), http.StatusBadGateway)
					return
				}
			}
			s.Store.serve(w, r, sum)
		case http.MethodPut:
			if err := s.Store.Write(sum, r.Body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}

// Peer serves GET /internal/plugin-blobs/<sha256> to the other nodes after
// node authentication and authorization; it serves only this node's store.
func (s *Service) Peer() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sum, ok := sumFrom(r.URL.Path, "/internal/plugin-blobs/")
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s.Store.serve(w, r, sum)
	})
}

var errNotFound = errors.New("no reachable node holds the plugin package")

// errUnreachable marks a peer the request never reached.
var errUnreachable = errors.New("unreachable")

// pull fetches a package this node lacks from the other enabled nodes, one
// after the other, until one serves bytes matching its digest. Nodes with a
// fresh heartbeat are asked first; a stale heartbeat does not exclude a node,
// since one that is starting, or has not run its heartbeat yet, still holds
// its packages. It reports errNotFound, naming each node and its answer,
// when every node either does not hold the package or cannot be reached; a
// node that answers with anything else (bytes that fail the digest, an error
// status) fails the pull.
func (s *Service) pull(ctx context.Context, sum string) error {
	nodes, err := s.Peers(ctx)
	if err != nil {
		return fmt.Errorf("list the nodes to fetch the package from: %w", err)
	}
	slices.SortStableFunc(nodes, func(a, b NodeRef) int {
		if a.Online == b.Online {
			return 0
		}
		if a.Online {
			return -1
		}
		return 1
	})
	var answers []string
	var failures []error
	for _, node := range nodes {
		found, err := s.fetch(ctx, node.PeerURL, sum)
		switch {
		case err == nil && found:
			return nil
		case errors.Is(err, errUnreachable):
			answers = append(answers, node.ID+": "+err.Error())
		case err != nil:
			failures = append(failures, fmt.Errorf("node %s: %w", node.ID, err))
		default:
			answers = append(answers, node.ID+": not held")
		}
	}
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	if len(answers) == 0 {
		answers = []string{"no other node to ask"}
	}
	return fmt.Errorf("%w (%s)", errNotFound, strings.Join(answers, "; "))
}

// fetch downloads the package from one peer and stores it. A peer that does
// not hold it returns found=false, err=nil; one the request does not reach
// returns errUnreachable; bytes that fail the digest or size checks, and any
// other failure, return the error.
func (s *Service) fetch(ctx context.Context, origin, sum string) (found bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/internal/plugin-blobs/"+sum, nil)
	if err != nil {
		return false, err
	}
	res, err := s.Client.Do(req)
	if err != nil {
		return false, fmt.Errorf("%w: fetch package from %s: %w", errUnreachable, origin, err)
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusOK:
		return true, s.Store.Write(sum, res.Body)
	case res.StatusCode == http.StatusNotFound && res.Header.Get("X-Sub2api-Peer-Error") == "":
		return false, nil
	default:
		return false, fmt.Errorf("fetch package from %s: HTTP %d", origin, res.StatusCode)
	}
}
