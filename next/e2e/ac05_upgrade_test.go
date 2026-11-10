package e2e

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// AC 5: upgrade anthropic from 0.2.1 to the 0.3.2-test fixture: the migration runs once, old rows are
// backfilled and requests keep succeeding on both nodes during the switch.
func TestAC05_UpgradeAnthropicWithoutDowntime(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	e.EnsurePlugin(admin, "anthropic", "0.2.1")
	tn := e.NewTenant(admin, TenantOpts{Accounts: 2, Balance: "50"})

	before := admin.OK(t, http.MethodGet, "/p/anthropic/models", nil).Array()
	if len(before) == 0 {
		t.Fatal("model catalog empty before upgrade")
	}

	// Upload the fixture: it adds a migration and asks for no new permission, so it
	// should not need consent; if it does, approve it.
	rev := e.InstallFromMarket(admin, "anthropic", "0.3.2-test")
	if d := rev.Get("diff"); d.Exists() {
		t.Logf("permission diff: %s", d.Raw)
	}
	if len(rev.Get("diff.added").Array())+len(rev.Get("diff.widened").Array()) > 0 {
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
	e.Upgrade(admin, "anthropic", "0.3.2-test")
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
	d := e.WaitPlugin(admin, "anthropic", "enabled", "0.3.2-test")
	if d.Get("desired_version").Exists() && d.Get("desired_version").String() != "0.3.2-test" {
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
