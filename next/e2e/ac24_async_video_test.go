package e2e

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// AC24 exercises the real task path against two hosts, Redis and PG:
// Execute relays the submission and waits, returns the task as its result
// (the core registers it and keeps the pre-charge), and Monitor's returned
// result settles it (success) or refunds it (failure). No real provider is
// contacted. The tests intentionally use production polling/lease intervals.
func TestAC24_ManagedVideoAcrossNodes(t *testing.T) {
	e := Setup(t)
	e.RequireDocker()
	if len(e.NodeURLs) != 2 {
		t.Fatal("AC24 requires exactly two nodes")
	}
	admin := e.Admin()
	e.EnsurePlugin(admin, "volcengine", "")
	for _, success := range []bool{true, false} {
		state := "failed"
		if success {
			state = "succeeded"
		}
		t.Run(state, func(t *testing.T) {
			e := e.With(t)
			tn := e.newVideoTenant(admin)
			v := e.submitVideo(tn)
			// Submit to node 1, immediately read the durable initial snapshot from 2.
			e.assertVideoSnapshot(e.NodeURLs[1], tn, v, "queued")
			// Observe the first held upstream GET before unrelated account/user
			// setup consumes its 20-second HTTP budget. Keep it held across a full
			// 10-second scheduler tick so both nodes can attempt reconciliation.
			initial := e.waitVideoActive(v)
			if len(initial.Queries) != 1 {
				t.Fatalf("missed the first video monitor before the concurrency check: %+v", initial)
			}
			holdUntil := time.Now().Add(12 * time.Second)
			for {
				for _, base := range e.NodeURLs {
					e.assertVideoSnapshot(base, tn, v, "queued")
				}
				if stats := e.videoTaskStats(v.UpstreamID); stats.Active != 1 || len(stats.Queries) != 1 || stats.MaxActive != 1 {
					t.Fatalf("another monitor or client GET started an upstream query: %+v", stats)
				}
				if !time.Now().Before(holdUntil) {
					break
				}
				time.Sleep(time.Second)
			}
			// Release promptly, then exercise authorization and account changes
			// without keeping a background HTTP call close to its timeout.
			e.controlVideo(v.Account.Key, "running", false)
			e.waitVideoState(e.NodeURLs[1], tn, v, "running", 30*time.Second)
			// Make the other account preferable to ordinary scheduling. Monitoring
			// must still use the submission's original account, on either host.
			for _, a := range tn.Accounts {
				priority := 1
				if a.ID != v.Account.ID {
					priority = 100
				}
				admin.OK(t, http.MethodPatch, fmt.Sprintf("/accounts/%d", a.ID), map[string]any{"priority": priority})
			}
			// Another user in the SAME group still cannot read the task.
			other := e.CreateUser(admin, UserSpec{})
			e.GrantGroups(admin, other.UserID, []int64{tn.GroupID})
			_, otherKey := e.CreateAPIKey(other, tn.GroupID)
			if r := e.queryVideo(e.NodeURLs[1], otherKey, v.PublicID); r.Status != http.StatusNotFound {
				t.Fatalf("cross-user task access: %s", r)
			}
			// A second key of the owner in the original group is allowed.
			_, ownerKey := e.CreateAPIKey(tn.User, tn.GroupID)
			if r := e.queryVideo(e.NodeURLs[1], ownerKey, v.PublicID); r.Status != 200 {
				t.Fatalf("same owner/group key: %s", r)
			}
			// An owner key in another group is not authority over this task.
			otherGroup := e.CreateGroup(admin, e.Name("other-video-group"), "restricted", 1, nil)
			e.GrantGroups(admin, tn.User.UserID, []int64{tn.GroupID, otherGroup})
			_, wrongGroupKey := e.CreateAPIKey(tn.User, otherGroup)
			if r := e.queryVideo(e.NodeURLs[1], wrongGroupKey, v.PublicID); r.Status != http.StatusNotFound {
				t.Fatalf("cross-group task access: %s", r)
			}
			e.controlVideo(v.Account.Key, state, false)
			e.waitVideoState(e.NodeURLs[1], tn, v, state, 90*time.Second)
			e.assertVideoAccounting(admin, tn, v, success)
			e.assertVideoTerminalStable(admin, tn, v, success)
		})
	}
}

func TestAC24_VideoMonitorSIGKILLTakeover(t *testing.T) {
	e := Setup(t)
	e.RequireDocker()
	if len(e.NodeURLs) != 2 {
		t.Fatal("AC24 requires exactly two nodes")
	}
	admin := e.Admin()
	e.EnsurePlugin(admin, "volcengine", "")
	tn := e.newVideoTenant(admin)
	v := e.submitVideo(tn)
	stats := e.waitVideoActive(v)
	source := ""
	for _, q := range stats.Queries {
		if q.Active {
			source = q.Source
		}
	}
	killed := 0
	for n := 1; n <= 2; n++ {
		ips := strings.Fields(e.Docker("inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}", e.Container(fmt.Sprintf("node-%d", n))))
		for _, ip := range ips {
			if ip == source {
				killed = n
			}
		}
	}
	if killed == 0 {
		t.Fatalf("cannot map active monitor source %q to either node", source)
	}
	// Require a non-restarting fixture: a revived owner would not prove takeover.
	policy := strings.TrimSpace(e.Docker("inspect", "--format", "{{.HostConfig.RestartPolicy.Name}}", e.Container(fmt.Sprintf("node-%d", killed))))
	if policy != "no" && policy != "" {
		t.Fatalf("node restart policy must be no for SIGKILL takeover; got %s", policy)
	}
	t.Cleanup(func() { e.StartNode(killed) })
	e.KillNode(killed)
	Eventually(t, 10*time.Second, 200*time.Millisecond, "crashed monitor connection canceled", func() bool { return e.videoTaskStats(v.UpstreamID).Active == 0 })
	e.controlVideo(v.Account.Key, "succeeded", false)
	survivor := 3 - killed
	e.assertVideoSnapshot(e.NodeURLs[survivor-1], tn, v, "queued")
	// The dead owner's PG claim lasts 3 minutes. No SQL/Redis mutation shortens
	// it: the survivor must reclaim and settle through the production scheduler.
	e.waitVideoState(e.NodeURLs[survivor-1], tn, v, "succeeded", 4*time.Minute)
	stats = e.videoTaskStats(v.UpstreamID)
	if len(stats.Queries) < 2 || !stats.Queries[0].Canceled || stats.Queries[len(stats.Queries)-1].Source == source {
		t.Fatalf("surviving node did not take over: %+v", stats)
	}
	e.assertVideoAccounting(admin, tn, v, true)
	e.StartNode(killed)
	e.WaitPlugin(admin, "volcengine", "enabled", "")
	e.assertVideoTerminalStable(admin, tn, v, true)
}
