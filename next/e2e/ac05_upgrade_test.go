package e2e

import (
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// The market's test builds that upgrade a release (each plugin's
// testdata/upgrade overlay): anthropic's changes the version only,
// dynweight's adds a migration.
const (
	anthropicUpgradeFixture   = "0.4.1-test"
	dynweightUpgradeFixture   = "0.7.1-test"
	dynweightFixtureMigration = "0004_config_rule_count.sql"
)

// AC 5: upgrade two plugins on the request path from the market's base
// version to their fixtures while requests flow through the load balancer:
// anthropic, whose platform builds every upstream request, and dynweight
// (switched on), which ranks the accounts and observes every attempt and
// keeps its data in its plg_dynweight__ tables. Requests keep succeeding on both nodes
// during both switches; dynweight's migration runs once and backfills the
// configuration saved before the upgrade, which the new version serves on
// both nodes.
func TestAC05_UpgradePluginsWithoutDowntime(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	e.EnsurePlugin(admin, "anthropic", e.defaultVersion("anthropic"))
	d, _ := e.Plugin(admin, "dynweight")
	if d.Get("status").String() != "enabled" {
		// Registered before EnsurePlugin, which enables it even when it skips.
		t.Cleanup(func() { e.Disable(admin, "dynweight") })
	}
	e.EnsurePlugin(admin, "dynweight", e.defaultVersion("dynweight"))
	tn := e.NewTenant(admin, TenantOpts{Accounts: 2, Balance: "50"})

	// The plugin's data: a configuration saved before the upgrade, which also
	// switches dynweight on. The factory one and the plugin state come back
	// afterwards, so later tests schedule as before.
	original := admin.OK(t, http.MethodGet, "/p/dynweight/config", nil)
	var cfg map[string]any
	if err := json.Unmarshal([]byte(original.Get("config").Raw), &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["enabled"] = true
	saved := admin.OK(t, http.MethodPut, "/p/dynweight/config", map[string]any{"config": cfg, "version": original.Get("version").Int()})
	t.Cleanup(func() {
		cur := admin.OK(t, http.MethodGet, "/p/dynweight/config", nil)
		admin.OK(t, http.MethodPut, "/p/dynweight/config", map[string]any{"config": json.RawMessage(original.Get("config").Raw), "version": cur.Get("version").Int()})
	})

	// Stage both fixtures: they ask for no new permission, so they should
	// not need consent; if they do, approve it.
	fixtures := map[string]string{"anthropic": anthropicUpgradeFixture, "dynweight": dynweightUpgradeFixture}
	for key, version := range fixtures {
		rev := e.InstallFromMarket(admin, key, version)
		if d := rev.Get("diff"); d.Exists() {
			t.Logf("%s permission diff: %s", key, d.Raw)
		}
		if rev.Get("consent_status").String() == "awaiting_consent" {
			e.ConsentAll(admin, rev, []string{"admin"})
		}
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

	time.Sleep(2 * time.Second) // baseline traffic before the switches
	e.Upgrade(admin, "anthropic", anthropicUpgradeFixture)
	e.Upgrade(admin, "dynweight", dynweightUpgradeFixture)
	time.Sleep(3 * time.Second) // traffic after the switches
	close(stop)
	wg.Wait()

	if failed.Load() > 0 {
		failures.Range(func(k, v any) bool { t.Logf("failed request %v: %v", k, v); return true })
		t.Fatalf("%d of %d requests failed during the upgrades", failed.Load(), ok.Load()+failed.Load())
	}
	if ok.Load() < 20 {
		t.Fatalf("too little traffic during the upgrades: %d", ok.Load())
	}

	// Both nodes run the fixtures; the new dynweight serves the saved
	// configuration on each.
	for key, version := range fixtures {
		d := e.WaitPlugin(admin, key, "enabled", version)
		if d.Get("desired_version").Exists() && d.Get("desired_version").String() != version {
			t.Fatalf("%s desired_version: %s", key, d.Raw)
		}
	}
	e.OnEachNode(admin, func(n int, c *Client) {
		r := c.API(t, http.MethodGet, "/p/dynweight/config", nil)
		if r.Status != 200 || r.Data().Get("version").Int() != saved.Get("version").Int() || !r.Data().Get("config.enabled").Bool() {
			t.Fatalf("node-%d config after the upgrade: %s, saved %s", n, r, saved.Raw)
		}
	})

	// The migration ran exactly once and backfilled the saved row (needs PG
	// access). psql logs in as the core's user: its search_path finds the
	// core's schema, where the plugin's tables are.
	if e.DockerHost != "" {
		prefix := manifest.TablePrefix("dynweight")
		if n := e.SQL(`SELECT count(*) FROM ` + prefix + `schema_migrations WHERE migration_id=` + sqlText(dynweightFixtureMigration)); len(n) != 1 || n[0] != "1" {
			t.Fatalf("%s recorded %v times", dynweightFixtureMigration, n)
		}
		if n := e.SQL(`SELECT count(*) FROM ` + prefix + `config WHERE rule_count = jsonb_array_length(doc->'rules')`); len(n) != 1 || n[0] != "1" {
			t.Fatalf("configuration rows backfilled: %v", n)
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
