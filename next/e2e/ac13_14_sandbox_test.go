package e2e

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// AC 13: guard's webhook alert shows up on the egress page; the test build's
// direct net.Dial fails under strict mode.
func TestAC13_EgressLogAndStrictNetwork(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	tn := e.NewTenant(admin, TenantOpts{})
	e.EnsurePlugin(admin, "guard", "")

	hook := e.MockInternalURL + "/__webhook/guard-" + e.RunID
	e.SetGuardSettings(admin, hook)
	e.SetGuardRules(admin, e.ForbiddenRule())
	m := e.Mock()
	mark := m.Mark(t)
	since := time.Now().Add(-time.Minute)
	if g := e.Messages(tn.APIKey, MessagesBody(tn.Model, "trigger "+e.ForbiddenWord(), false), nil); g.Status != 403 {
		t.Fatalf("rule hit: HTTP %d", g.Status)
	}
	// The webhook reached the mock through the egress tunnel.
	Eventually(t, 20*time.Second, time.Second, "webhook delivered", func() bool {
		for _, r := range m.Since(t, mark) {
			if strings.HasPrefix(r.Path, "/__webhook/guard-"+e.RunID) {
				return true
			}
		}
		return false
	})
	// ... and is listed on the "external access" page. Rows are written when
	// the tunnelled connection closes; HTTP keep-alive holds it up to 90 s.
	host := strings.TrimPrefix(strings.Split(strings.TrimPrefix(e.MockInternalURL, "http://"), ":")[0], "https://")
	Eventually(t, 120*time.Second, 3*time.Second, "egress log for "+host, func() bool {
		d := admin.OK(t, http.MethodGet, "/plugins/guard/egress", nil,
			Query("from", since.UTC().Format(time.RFC3339), "to", time.Now().Add(time.Minute).UTC().Format(time.RFC3339)))
		return strings.Contains(d.Raw, host)
	})
	e.SetGuardRules(admin)

	// Test build: direct dialing is refused by seccomp on every node.
	e.EnsureGuardTestBuild(admin)
	defer e.EnsurePlugin(admin, "guard", "")
	e.OnEachNode(admin, func(n int, c *Client) {
		r := c.API(t, http.MethodPost, "/p/guard/debug/dial", map[string]any{"address": "1.1.1.1:443"})
		// debugDial returns a raw JSON object, not a console data envelope.
		// A missing route, crashed plugin, or ordinary network timeout must not
		// be mistaken for the seccomp policy rejecting socket creation.
		if r.Status != http.StatusOK || r.JSON().Get("ok").Raw != "false" {
			t.Fatalf("node-%d: expected the debug route to report a rejected direct dial: %s", n, r)
		}
		dialError := strings.ToLower(r.JSON().Get("error").String())
		if !strings.Contains(dialError, "not permitted") && !strings.Contains(dialError, "denied") {
			t.Fatalf("node-%d: direct dial did not report EPERM/EACCES: %s", n, r)
		}
	})
}

// AC 14: plugin memory is unlimited unless an administrator limits it
// (DESIGN-2026-11-18-PLUGIN-FORWARDING-V2 D23): with a per-plugin limit set,
// an over-allocating test build is restarted with the reason shown; under
// container memory pressure the plugin (not the core) is killed; a
// manifest's memoryMB is what the plugin says it needs, not a limit, so
// without a node cap a large request is accepted at upload.
func TestAC14_ResourceLimits(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()

	t.Run("over-limit allocation restarts the plugin", func(t *testing.T) {
		e := e.With(t)
		e.EnsureGuardTestBuild(admin)
		// The manifest's 128 MB is no limit: the administrator sets one,
		// then the test build allocates well above 110% of it.
		admin.OK(t, http.MethodPut, "/plugins/guard/resources", map[string]any{"memory_mb": 128, "cpu": 0.25, "max_threads": 64, "max_open_files": 256})
		t.Cleanup(func() {
			admin.OK(t, http.MethodPut, "/plugins/guard/resources", map[string]any{"memory_mb": 0, "cpu": 0.25, "max_threads": 64, "max_open_files": 256})
		})
		e.WaitPlugin(admin, "guard", "enabled", guardTestVersion)
		r := admin.API(t, http.MethodPost, "/p/guard/debug/alloc", nil, Query("mb", "300"))
		t.Logf("alloc: %s", r)
		Eventually(t, 60*time.Second, 2*time.Second, "restart with memory reason", func() bool {
			d, _ := e.Plugin(admin, "guard")
			s := strings.ToLower(d.Raw)
			return strings.Contains(s, "restart") && strings.Contains(s, "memory")
		})
		e.WaitPlugin(admin, "guard", "enabled", guardTestVersion)
	})

	t.Run("container OOM kills the plugin, not the core", func(t *testing.T) {
		e := e.With(t)
		e.RequireDocker()
		before := e.NodeHealth(1)
		bootBefore := e.NodeExec(1, "cat /proc/1/stat | cut -d' ' -f22")
		// Raise the plugin's own limit so the watchdog does not act first,
		// then allocate more than the node container's mem_limit (1g).
		admin.OK(t, http.MethodPut, "/plugins/guard/resources", map[string]any{"memory_mb": 1024, "cpu": 0.25, "max_threads": 64, "max_open_files": 256})
		t.Cleanup(func() {
			admin.OK(t, http.MethodPut, "/plugins/guard/resources", map[string]any{"memory_mb": 0, "cpu": 0.25, "max_threads": 64, "max_open_files": 256})
		})
		c := &Client{Base: e.NodeURLs[0], HTTP: &http.Client{Timeout: 30 * time.Second}, Token: admin.Token}
		c.API(t, http.MethodPost, "/p/guard/debug/alloc", nil, Query("mb", "1500"))
		time.Sleep(5 * time.Second)
		after := e.NodeHealth(1)
		if after.Get("node").String() != before.Get("node").String() {
			t.Fatal("node identity changed")
		}
		if bootAfter := e.NodeExec(1, "cat /proc/1/stat | cut -d' ' -f22"); bootAfter != bootBefore {
			t.Fatalf("core process restarted (start time %s -> %s)", bootBefore, bootAfter)
		}
	})

	t.Run("a large memory request is no limit and is accepted without a node cap", func(t *testing.T) {
		e := e.With(t)
		key := NewSigningKey(t, e.PublisherID(), "e2e-verified-"+e.RunID)
		pubID := e.RegisterPublisher(admin, key, "verified")
		defer admin.API(t, http.MethodPost, fmt.Sprintf("/publishers/%d/revoke", pubID), nil)
		pkg := e.DerivedGuardPackage("e2e_res", key, false)
		man := pkg.Manifest(t)
		man["resources"] = map[string]any{"memoryMB": 64 * 1024, "cpu": 0.25, "maxProcs": 64, "maxOpenFiles": 256}
		pkg.SetManifest(t, man)
		pkg.Sign(t, key)
		// An upload spends a step-up proof (CONTRACTS §80.2), refused without one.
		if r := admin.WithoutStepUp().Upload(t, "/plugins/upload", "file", "e2e_res.s2plugin", pkg.Bytes(t)); r.Status != http.StatusForbidden || r.ErrCode() != "verification_required" {
			t.Fatalf("upload without a step-up proof: %s", r)
		}
		r := e.UploadPlugin(admin, "e2e_res.s2plugin", pkg.Bytes(t))
		if r.Status != 200 {
			t.Fatalf("upload with resources.memoryMB 65536 and no node cap (SUB2API_PLUGIN_MAX_MEMORY_MB unset): %s", r)
		}
		if key := reviewOf(e, r.Data()).Get("plugin_key").String(); key != "" {
			defer e.Uninstall(admin, key, true)
		}
	})
	e.EnsurePlugin(admin, "guard", "")
}
