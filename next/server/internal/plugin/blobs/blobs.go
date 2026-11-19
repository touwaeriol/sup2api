// Package blobs keeps plugin package bytes outside PostgreSQL. PostgreSQL
// records each version's sha256 and, for market installs, its download URL;
// the bytes come from the market or from any other node that holds them.
package blobs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	rc "github.com/Sub2API-Devs/sup2api/next/runtime-contract"
)

// ErrNotFound reports that no reachable store holds the package; under the
// shell, that no node it could reach does (the wrapped text gives each
// node's answer).
var ErrNotFound = errors.New("plugin package is not available on this node")

// Store holds packages by lower-case hex sha256. Put returns once the
// package is durable on this node, which other nodes fetch it from. Has
// reports whether Get would find the package, without transferring it.
type Store interface {
	Put(ctx context.Context, sum string, data []byte) error
	Get(ctx context.Context, sum string) ([]byte, error)
	Has(ctx context.Context, sum string) (bool, error)
}

func validSum(sum string) bool {
	if len(sum) != 64 {
		return false
	}
	_, err := hex.DecodeString(sum)
	return err == nil && strings.ToLower(sum) == sum
}

func check(sum string, data []byte) error {
	h := sha256.Sum256(data)
	if hex.EncodeToString(h[:]) != sum {
		return fmt.Errorf("plugin package sha256 mismatch for %s", sum)
	}
	return nil
}

// Dir is a local directory store. It serves one node only: a deployment of
// several nodes runs under the shell and uses Shell.
type Dir string

func (d Dir) path(sum string) string { return filepath.Join(string(d), sum[:2], sum) }

func (d Dir) Put(_ context.Context, sum string, data []byte) error {
	if !validSum(sum) {
		return fmt.Errorf("invalid plugin package sha256 %q", sum)
	}
	if err := check(sum, data); err != nil {
		return err
	}
	file := d.path(sum)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".put-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err == nil {
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

func (d Dir) Get(_ context.Context, sum string) ([]byte, error) {
	if !validSum(sum) {
		return nil, fmt.Errorf("invalid plugin package sha256 %q", sum)
	}
	data, err := os.ReadFile(d.path(sum))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err = check(sum, data); err != nil {
		return nil, err
	}
	return data, nil
}

func (d Dir) Has(_ context.Context, sum string) (bool, error) {
	if !validSum(sum) {
		return false, fmt.Errorf("invalid plugin package sha256 %q", sum)
	}
	_, err := os.Stat(d.path(sum))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// Shell stores packages through the local shell's management socket. The
// shell keeps them on this node; a Get of a package this node lacks is
// fetched from any other enabled node that holds it over the authenticated node
// network.
type Shell struct {
	socket string
	token  string // management socket token
	client *http.Client
	max    int64
}

// NewShell talks to the shell's management socket. token (config
// Managed.UpdaterToken) is sent as "Authorization: Bearer <token>".
func NewShell(socket, token string, maxBytes int64) *Shell {
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}
	s := newShell(dial, token, maxBytes)
	s.socket = socket
	return s
}

func newShell(dial func(ctx context.Context, network, addr string) (net.Conn, error), token string, maxBytes int64) *Shell {
	if maxBytes <= 0 {
		maxBytes = rc.PluginPackageMaxBytes
	}
	return &Shell{token: token, max: maxBytes, client: &http.Client{Timeout: rc.PluginPackageTransferTimeout, Transport: &http.Transport{DialContext: dial}}}
}

func (s *Shell) url(sum string) string { return "http://shell/system/plugin-blobs/" + sum }

func (s *Shell) authorize(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+s.token)
}

func (s *Shell) Put(ctx context.Context, sum string, data []byte) error {
	if !validSum(sum) {
		return fmt.Errorf("invalid plugin package sha256 %q", sum)
	}
	if err := check(sum, data); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.url(sum), bytes.NewReader(data))
	if err != nil {
		return err
	}
	s.authorize(req)
	res, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("store plugin package through the shell: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("store plugin package through the shell: HTTP %d %s", res.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

func (s *Shell) Get(ctx context.Context, sum string) ([]byte, error) {
	if !validSum(sum) {
		return nil, fmt.Errorf("invalid plugin package sha256 %q", sum)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url(sum), nil)
	if err != nil {
		return nil, err
	}
	s.authorize(req)
	res, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch plugin package through the shell: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		if res.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, strings.TrimSpace(string(msg)))
		}
		return nil, fmt.Errorf("fetch plugin package through the shell: HTTP %d %s", res.StatusCode, strings.TrimSpace(string(msg)))
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, s.max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > s.max {
		return nil, fmt.Errorf("plugin package exceeds %d bytes", s.max)
	}
	if err = check(sum, data); err != nil {
		return nil, err
	}
	return data, nil
}

// Has asks for the first byte only (the shell serves packages with range
// support and fetches a missing one from the other nodes first, as Get
// does).
func (s *Shell) Has(ctx context.Context, sum string) (bool, error) {
	if !validSum(sum) {
		return false, fmt.Errorf("invalid plugin package sha256 %q", sum)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url(sum), nil)
	if err != nil {
		return false, err
	}
	s.authorize(req)
	req.Header.Set("Range", "bytes=0-0")
	res, err := s.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("check plugin package through the shell: %w", err)
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK, http.StatusPartialContent:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	}
	return false, fmt.Errorf("check plugin package through the shell: HTTP %d", res.StatusCode)
}

// Source resolves the bytes of one plugin version. A market version is
// downloaded by each node from its market URL; anything else, and a market
// download that fails, comes from Store.
type Source struct {
	Store    Store
	Download func(ctx context.Context, url string, limit int64) ([]byte, error)
	MaxBytes int64
}

func (s *Source) Fetch(ctx context.Context, sum, url string) ([]byte, error) {
	if s == nil || s.Store == nil {
		return nil, errors.New("no plugin package store configured")
	}
	sum = strings.ToLower(sum)
	if url != "" && s.Download != nil {
		limit := s.MaxBytes
		if limit <= 0 {
			limit = rc.PluginPackageMaxBytes
		}
		if data, err := s.Download(ctx, url, limit); err == nil && check(sum, data) == nil {
			return data, nil
		}
	}
	data, err := s.Store.Get(ctx, sum)
	if err != nil {
		return nil, err
	}
	return data, check(sum, data)
}

// Has reports whether Store still holds sum: a package removed from it is
// gone unless a market download serves it again (Fetch).
func (s *Source) Has(ctx context.Context, sum string) (bool, error) {
	if s == nil || s.Store == nil {
		return false, errors.New("no plugin package store configured")
	}
	return s.Store.Has(ctx, strings.ToLower(sum))
}
