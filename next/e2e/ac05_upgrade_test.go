package e2e

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// anthropicUpgradeFixture is the market's test build of anthropic that adds
// a migration (the plugins/anthropic/testdata/upgrade overlay).
const anthropicUpgradeFixture = "0.4.1-test"

// AC 5: upgrade anthropic from the market's base version to the fixture: the migration runs once, old rows are
// backfilled and requests keep succeeding on both nodes during the switch.
func TestAC05_UpgradeAnthropicWithoutDowntime(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	e.EnsurePlugin(admin, "anthropic", e.defaultVersion("anthropic"))
	tn := e.NewTenant(admin, TenantOpts{Accounts: 2, Balance: "50"})

	before := admin.OK(t, http.MethodGet, "/p/anthropic/models", nil).Array()
	if len(before) == 0 {
		t.Fatal("model catalog empty before upgrade")
	}

	// Upload the fixture: it adds a migration and asks for no new permission, so it
	// should not need consent; if it does, approve it.
	rev := e.InstallFromMarket(admin, "anthropic", anthropicUpgradeFixture)
	if d := rev.Get("diff"); d.Exists() {
		t.Logf("permission diff: %s", d.Raw)
	}
	if rev.Get("consent_status").String() == "awaiting_consent" {
		e.ConsentAll(admin, rev, []string{"admin"})
	}

	// Traffic generator: 4 workers alternating stream/non-stream through the LB.
	var ok, failed atomic.Int64
	var failures sync.Map
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				g, err := GatewayTry(e.BaseURL, "/v1/messages", tn.APIKey, MessagesBody(tn.Model, "upgrade traffic", (w+i)%2 == 0), nil)
				switch {
				case err != nil:
					failed.Add(1)
					failures.Store(time.Now().String(), err.Error())
				case g.Status == 200:
					ok.Add(1)
				default:
					failed.Add(1)
					failures.Store(g.RequestID+"/"+itoa(int64(g.Status)), string(g.Body))
				}
				time.Sleep(50 * time.Millisecond)
			}
		}(w)
	}

	time.Sleep(2 * time.Second) // baseline traffic before the switch
	e.Upgrade(admin, "anthropic", anthropicUpgradeFixture)
	time.Sleep(3 * time.Second) // traffic after the switch
	close(stop)
	wg.Wait()

	if failed.Load() > 0 {
		failures.Range(func(k, v any) bool { t.Logf("failed request %v: %v", k, v); return true })
		t.Fatalf("%d of %d requests failed during the upgrade", failed.Load(), ok.Load()+failed.Load())
	}
	if ok.Load() < 20 {
		t.Fatalf("too little traffic during the upgrade: %d", ok.Load())
	}

	// Both nodes run the fixture and serve the backfilled catalog.
	d := e.WaitPlugin(admin, "anthropic", "enabled", anthropicUpgradeFixture)
	if d.Get("desired_version").Exists() && d.Get("desired_version").String() != anthropicUpgradeFixture {
		t.Fatalf("desired_version: %s", d.Raw)
	}
	e.OnEachNode(admin, func(n int, c *Client) {
		r := c.API(t, http.MethodGet, "/p/anthropic/models", nil)
		if r.Status != 200 {
			t.Fatalf("node-%d models: %s", n, r)
		}
		rows := r.Data().Array()
		if len(rows) < len(before) {
			t.Fatalf("node-%d: %d rows after upgrade, %d before", n, len(rows), len(before))
		}
		for _, row := range rows {
			if row.Get("family").String() == "" {
				t.Fatalf("node-%d: row not backfilled: %s", n, row.Raw)
			}
		}
	})

	// The migration ran exactly once (needs PG access).
	if e.DockerHost != "" {
		rows := e.SQL(`SELECT migration_id FROM plugin_migrations WHERE plugin_key='anthropic' ORDER BY migration_id`)
		n := 0
		for _, r := range rows {
			if r == "0002_add_family.sql" {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("0002_add_family.sql recorded %d times: %v", n, rows)
		}
		cols := e.SQL(`SELECT count(*) FROM information_schema.columns WHERE table_schema='plg_anthropic' AND table_name='model_catalog' AND column_name='family'`)
		if len(cols) != 1 || cols[0] != "1" {
			t.Fatalf("family column: %v", cols)
		}
	}
}

// AC 5 (enable): enabling an install-only bundled plugin keeps every node
// ready and the load balancer serving, and the plugin can be disabled again
// at once. The bundled plugins gate a node's
// readiness at start only; before, the plugin's "enabling" row made /healthz
// answer 503 on both nodes at once (each node re-checks every 5 s) and the
// load balancer answered every request with an empty 503.
func TestAC05_EnableBuiltinKeepsNodesServing(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	const key = "mailer"
	if d, ok := e.Plugin(admin, key); !ok || !d.Get("builtin").Bool() {
		t.Fatalf("%s is not a bundled plugin here: %s", key, d.Raw)
	} else if d.Get("status").String() == "enabled" {
		e.Disable(admin, key)
	}

	var failures []string
	var mu sync.Mutex
	var probes atomic.Int64
	fail := func(f string) {
		mu.Lock()
		failures = append(failures, time.Now().Format("15:04:05.000")+" "+f)
		mu.Unlock()
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			for n := range e.NodeURLs {
				if !e.NodeHealthy(n + 1) {
					fail("node-" + itoa(int64(n+1)) + " /healthz not 200")
				}
			}
			// Without an API key the gateway answers 401; the load balancer
			// answers 503 when it sees no healthy node.
			if g, err := GatewayTry(e.BaseURL, "/v1/messages", "", map[string]any{}, nil); err != nil {
				fail("load balancer: " + err.Error())
			} else if g.Status >= 500 {
				fail("load balancer: " + itoa(int64(g.Status)) + " " + string(g.Body))
			}
			probes.Add(1)
			time.Sleep(100 * time.Millisecond)
		}
	}()

	time.Sleep(time.Second)
	e.Enable(admin, key)
	// An operator's next plugin change is accepted at once: the rollout
	// starts no round over the bundled plugins holding the cluster change
	// lock (it answered 409 cluster_change_busy).
	e.Disable(admin, key)
	// Past the next readiness check of both nodes and the balancer's health check.
	time.Sleep(7 * time.Second)
	close(stop)
	wg.Wait()

	if len(failures) > 0 {
		t.Fatalf("%d of %d probes failed while %s was enabled and disabled: %v", len(failures), probes.Load(), key, failures)
	}
	if probes.Load() < 20 {
		t.Fatalf("too few probes: %d", probes.Load())
	}
}
