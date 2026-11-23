package market

import (
	"net/http"
	"testing"
	"time"

	rc "github.com/Sub2API-Devs/sup2api/next/runtime-contract"
)

// Package downloads get at least rc.PluginPackageTransferTimeout while index requests
// keep their short timeout.
func TestPackageDownloadLimits(t *testing.T) {
	index := &http.Client{Timeout: 60 * time.Second}
	s := New(nil, nil, index, Options{})
	if s.opt.MaxPackageBytes != rc.PluginPackageMaxBytes {
		t.Fatalf("default max: %d", s.opt.MaxPackageBytes)
	}
	if s.client != index || s.client.Timeout != 60*time.Second || s.pkg.Timeout != rc.PluginPackageTransferTimeout {
		t.Fatalf("timeouts: index %s, package %s", s.client.Timeout, s.pkg.Timeout)
	}
	// No timeout (0) stays unlimited; a longer one is kept.
	if s = New(nil, nil, &http.Client{}, Options{}); s.pkg.Timeout != 0 {
		t.Fatalf("unlimited client: %s", s.pkg.Timeout)
	}
	if s = New(nil, nil, &http.Client{Timeout: 2 * time.Hour}, Options{}); s.pkg.Timeout != 2*time.Hour {
		t.Fatalf("longer client: %s", s.pkg.Timeout)
	}
}
