package e2e

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// AC 10: a guard rule hit returns 403 without calling the upstream; when the
// guard process is killed the hook fails open and the breaker trips.
func TestAC10_GuardBlocksAndFailsOpen(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	tn := e.NewTenant(admin, TenantOpts{})
	e.EnsurePlugin(admin, "guard", "")
	e.SetGuardRules(admin, e.ForbiddenRule())
	m := e.Mock()

	// Blocked on every node (rules cached in each plugin process).
	for i := 0; i < 4; i++ {
		mark := m.Mark(t)
		prompt := fmt.Sprintf("please say %s now (%d)", e.ForbiddenWord(), i)
		g := e.Messages(tn.APIKey, MessagesBody(tn.Model, prompt, i%2 == 0), nil)
		if g.Status != 403 || g.JSON().Get("type").String() != "error" {
			t.Fatalf("blocked request: HTTP %d %s", g.Status, g.Body)
		}
		if i == 0 && !strings.Contains(string(g.Body), guardDenyCode) && !strings.Contains(string(g.Body), "guard") {
			t.Logf("deny body does not mention %s: %s", guardDenyCode, g.Body)
		}
		if used := KeysUsed(m.Since(t, mark), "/v1/messages"); len(used) != 0 {
			t.Fatalf("upstream called for a blocked request: %v", used)
		}
		u := e.UsageByRequest(admin, tn.User.UserID, g.RequestID)
		if u.Get("error_type").String() != "blocked_by_hook" || u.Get("billing_status").String() != "free" ||
			Money(t, u.Get("total_cost").String()).Sign() != 0 {
			t.Fatalf("blocked usage record: %s", u.Raw)
		}
		if hd := u.Get("hook_decisions").Array(); len(hd) == 0 {
			t.Fatalf("hook_decisions empty: %s", u.Raw)
		}
	}
	// Clean prompts pass.
	e.MustMessages(tn.APIKey, MessagesBody(tn.Model, "a harmless prompt", false), nil)

	// Kill guard on both nodes repeatedly while sending forbidden prompts:
	// failure=open lets them through (never 5xx), and the breaker opens.
	e.RequireDocker()
	// The breaker opens after 10 consecutive failures of the hook on one node
	// (ARCHITECTURE 6.3), so keep going until it does rather than stopping
	// after a fixed number of requests.
	breakerOpen := func() (bool, string) {
		d, _ := e.Plugin(admin, "guard")
		stats := d.Get("hooks")
		for _, h := range stats.Array() {
			if h.Get("stats.breaker_open").Bool() || h.Get("breaker_open").Bool() {
				return true, stats.Raw
			}
		}
		return false, stats.Raw
	}
	var passed, blocked, other atomic.Int64
	opened, lastStats := false, ""
	deadline := time.Now().Add(60 * time.Second)
	for !opened && time.Now().Before(deadline) {
		e.KillPluginProcesses(1, "guard")
		e.KillPluginProcesses(2, "guard")
		for i := 0; i < 30; i++ {
			g := e.Messages(tn.APIKey, MessagesBody(tn.Model, "still "+e.ForbiddenWord(), false), nil)
			switch g.Status {
			case 200:
				passed.Add(1)
			case 403:
				blocked.Add(1) // guard already restarted
			default:
				other.Add(1)
				t.Errorf("unexpected HTTP %d while guard is down: %s", g.Status, g.Body)
			}
		}
		opened, lastStats = breakerOpen()
	}
	if passed.Load() == 0 {
		t.Fatalf("no request failed open (blocked=%d other=%d)", blocked.Load(), other.Load())
	}
	if !opened {
		t.Fatalf("breaker never opened; hook stats: %s", lastStats)
	}

	// Recovery: guard restarts (backoff) and blocks again.
	Eventually(t, 90*time.Second, 2*time.Second, "guard blocking again", func() bool {
		g := e.Messages(tn.APIKey, MessagesBody(tn.Model, "again "+e.ForbiddenWord(), false), nil)
		return g.Status == 403
	})
	e.SetGuardRules(admin)
}

// AC 11: guard's event-subscription statistics match the usage records;
// after disable + enable delivery resumes from the cursor.
func TestAC11_GuardEventStatsMatchUsage(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	tn := e.NewTenant(admin, TenantOpts{})
	e.EnsurePlugin(admin, "guard", "")
	e.SetGuardRules(admin)

	// guard aggregates by minute and counts buckets in [from, to), so both
	// sides use minute-aligned windows (earlier tests' traffic in the same
	// minute is counted by both).
	from := time.Now().UTC().Truncate(time.Minute)
	nextMinute := func() time.Time { return time.Now().UTC().Add(time.Minute).Truncate(time.Minute) }
	send := func(n int) {
		for i := 0; i < n; i++ {
			g := e.MustMessages(tn.APIKey, MessagesBody(tn.Model, fmt.Sprintf("count %d", i), i%2 == 0), nil)
			e.UsageByRequest(admin, tn.User.UserID, g.RequestID)
		}
	}
	// guard counts every usage.recorded event (requests_total), so compare with
	// all usage records in the same window; tests run sequentially.
	countUsage := func(to time.Time) int64 {
		return int64(len(admin.ListAll(t, "/usage",
			"from", from.UTC().Format(time.RFC3339), "to", to.UTC().Format(time.RFC3339))))
	}
	statsFor := func(to time.Time) int64 {
		return e.GuardStats(admin, from, to).Get("requests_total").Int()
	}

	send(6)
	to := nextMinute()
	Eventually(t, 60*time.Second, 2*time.Second, "guard stats catch up", func() bool {
		return statsFor(to) == countUsage(to)
	})

	// Disable, generate events, re-enable: the cursor resumes, nothing lost.
	cur := admin.OK(t, http.MethodGet, "/plugins/guard/events", nil)
	e.Disable(admin, "guard")
	send(4)
	e.Enable(admin, "guard")
	to = nextMinute()
	Eventually(t, 60*time.Second, 2*time.Second, "guard stats after re-enable", func() bool {
		return statsFor(to) == countUsage(to)
	})
	after := admin.OK(t, http.MethodGet, "/plugins/guard/events", nil)
	if after.Get("cursor.last_event_id").Int() <= cur.Get("cursor.last_event_id").Int() {
		t.Fatalf("cursor did not advance: before %s after %s", cur.Raw, after.Raw)
	}
	if n := len(after.Get("deadletters").Array()); n != 0 {
		t.Fatalf("dead letters: %s", after.Get("deadletters").Raw)
	}
}

// AC 12: guard's rollup job runs once per cycle with two nodes.
func TestAC12_RollupOncePerCycle(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	e.EnsurePlugin(admin, "guard", "")

	runs := func() map[string][]string {
		out := map[string][]string{} // scheduled_at -> node ids
		// GET /plugins/:key/jobs -> {jobs:[{id, schedule, last_run}], recent_runs:[...]}
		d := admin.OK(t, http.MethodGet, "/plugins/guard/jobs", nil)
		for _, run := range d.Get("recent_runs").Array() {
			if run.Get("job_id").String() != "rollup" || run.Get("manual").Bool() {
				continue
			}
			k := run.Get("scheduled_at").String()
			out[k] = append(out[k], run.Get("node_id").String())
		}
		return out
	}

	// Manual trigger works and is recorded as manual. A node registers the
	// jobs of a plugin enabled moments ago on its next task reconciliation
	// (at the latest the one-minute resync),
	// answering 503 plugin_unavailable until then.
	var run *Resp
	Eventually(t, 90*time.Second, time.Second, func() string { return "manual rollup run: " + run.String() }, func() bool {
		run = admin.API(t, http.MethodPost, "/plugins/guard/jobs/rollup/run", nil)
		if run.Status/100 != 2 && run.ErrCode() != "plugin_unavailable" {
			t.Fatalf("manual rollup run: %s", run)
		}
		return run.Status/100 == 2
	})

	e.RequireLong() // @every 5m: wait for two scheduled cycles
	Eventually(t, 11*time.Minute, 15*time.Second, "two scheduled rollup cycles", func() bool {
		return len(runs()) >= 2
	})
	nodes := map[string]bool{}
	for at, ns := range runs() {
		if len(ns) != 1 {
			t.Fatalf("rollup at %s ran %d times on %v", at, len(ns), ns)
		}
		nodes[ns[0]] = true
	}
	t.Logf("rollup executed by nodes %v", nodes)
	if e.DockerHost != "" {
		dup := e.SQL(`SELECT scheduled_at, count(*) FROM plugin_job_runs WHERE plugin_key='guard' AND job_id='rollup' AND NOT manual GROUP BY 1 HAVING count(*) > 1`)
		if len(dup) != 0 {
			t.Fatalf("duplicate rollup runs: %v", dup)
		}
	}
}
