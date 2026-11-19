package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg/pkgtest"
)

// The effective memory is what the runtime launches the plugin with: the
// operator's override, else the node cap, else 0 (unlimited), and never
// more than a set cap. The manifest's figure is only the plugin's
// suggestion.
func TestResourcesEffectiveMemory(t *testing.T) {
	h, root := newHarness(t)
	ctx := context.Background()
	m := pkgtest.Guard("guard", "0.1.0", "sub2api") // suggests 128 MB
	if code, out := h.do("POST", "/plugins/upload", "admin", fileBody(t, pkgtest.Build(m, root))); code != http.StatusOK {
		t.Fatalf("upload = %d %v", code, out)
	}
	for _, tc := range []struct {
		name          string
		cap, override int
		want          int
	}{
		{"no cap, no override: unlimited", 0, 0, 0},
		{"the node cap", 1024, 0, 1024},
		{"the override", 0, 256, 256},
		{"the override under the cap", 1024, 256, 256},
		{"an override over the cap is capped", 200, 256, 200},
	} {
		h.api.d.Plugins.MaxMemoryMB = tc.cap
		if _, err := h.db.SQL.ExecContext(ctx, `UPDATE plugins SET resource_limits = jsonb_build_object('memory_mb', $1::int) WHERE key = 'guard'`, tc.override); err != nil {
			t.Fatal(err)
		}
		ri, err := h.api.resourcesInfo(ctx, "guard", m)
		if err != nil {
			t.Fatal(err)
		}
		if ri.Effective.MemoryMB != tc.want || ri.Requested.MemoryMB != 128 {
			t.Fatalf("%s: effective %d, requested %d", tc.name, ri.Effective.MemoryMB, ri.Requested.MemoryMB)
		}
	}
}
