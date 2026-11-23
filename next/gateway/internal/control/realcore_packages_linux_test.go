//go:build linux

package control

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestRealCorePluginPackagesTravelBetweenNodes checks where plugin package
// bytes come from on real cores: never PostgreSQL, and never only the
// primary. Bundled and uploaded packages are kept by the node that stored
// them and pulled by the others from any online node that holds them; a
// market package is downloaded by every node from the market. With the
// primary down, a plugin uploaded through a follower is installed on the
// other follower, and a follower that lost its package copies restarts and
// fetches them from the other one. A new node still needs the primary for
// the core release.
//
// Besides the real-core variables it needs TEST_UPLOAD_PACKAGE (a signed
// package that is neither bundled nor in the market) and TEST_MARKET_DIR (a
// directory with index.json and its packages).
func TestRealCorePluginPackagesTravelBetweenNodes(t *testing.T) {
	uploadPath, marketDir := os.Getenv("TEST_UPLOAD_PACKAGE"), os.Getenv("TEST_MARKET_DIR")
	if uploadPath == "" || marketDir == "" {
		t.Skip("requires TEST_UPLOAD_PACKAGE and TEST_MARKET_DIR in addition to the real-core variables")
	}
	uploaded, err := os.ReadFile(uploadPath)
	if err != nil {
		t.Fatal(err)
	}
	var marketPackageGets atomic.Int64
	market := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".s2plugin") {
			marketPackageGets.Add(1)
		}
		http.FileServer(http.Dir(marketDir)).ServeHTTP(w, r)
	}))
	defer market.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	const adminEmail, adminPassword = "admin@real.test", "Real-core-admin-pass!"
	c := newRealCluster(t, ctx, realOptions{coreEnv: []string{
		"SUB2API_BOOTSTRAP_ADMIN_EMAIL=" + adminEmail, "SUB2API_BOOTSTRAP_ADMIN_PASSWORD=" + adminPassword,
		`SUB2API_MARKET_SOURCES=[{"name":"test","url":"` + market.URL + `/index.json"}]`,
	}})
	db := c.db
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	wait := func(what string, timeout time.Duration, fn func() bool) {
		t.Helper()
		deadline := time.Now().Add(timeout)
		for !fn() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out after %s waiting for %s", timeout, what)
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-tick.C:
			}
		}
	}
	held := func(node, sum string) bool {
		_, err := os.Stat(filepath.Join(c.roots[node], "plugin-blobs", sum[:2], sum))
		return err == nil
	}
	digestOf := func(key string) string {
		var sum string
		_ = db.WithContext(ctx).Raw(`SELECT lower(package_sha256) FROM plugin_versions v JOIN plugins p ON p.key=v.plugin_key AND p.active_version=v.version WHERE p.key=?`, key).Row().Scan(&sum)
		return sum
	}
	// Every current core boot of the cluster runs an instance of key.
	enabledEverywhere := func(key string) bool {
		var status string
		var running, cores int
		_ = db.WithContext(ctx).Raw(`SELECT p.status,
			(SELECT count(*) FROM plugin_runtime_nodes r JOIN updater.nodes n ON n.core_boot_id=r.boot_id AND n.cluster_id=? WHERE r.plugin_key=p.key AND NOT r.stopped AND n.ready),
			(SELECT count(*) FROM updater.nodes WHERE cluster_id=? AND ready)
			FROM plugins p WHERE p.key=?`, c.dbname, c.dbname, key).Row().Scan(&status, &running, &cores)
		return status == "enabled" && cores >= 3 && running == cores
	}
	probe := func(n *realNode) int {
		res, err := http.Get(n.public.URL + "/api/v1/key/prices")
		if err != nil {
			return 0
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
		return res.StatusCode
	}

	for _, id := range []string{"a", "b", "c"} {
		c.startShell(id)
	}
	if err = c.node("a").engine.Recover(ctx, true); err != nil {
		t.Fatal("bootstrap primary", err)
	}
	_ = c.node("a").engine.Heartbeat(ctx)
	for _, id := range []string{"b", "c"} {
		if err = c.node(id).engine.Recover(ctx, false); err != nil {
			t.Fatal("join follower", err)
		}
		_ = c.node(id).engine.Heartbeat(ctx)
	}
	for _, n := range c.list() {
		c.run(n)
	}
	var column bool
	if err = db.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name='plugin_versions' AND column_name='package')`).Row().Scan(&column); err != nil || column {
		t.Fatalf("plugin_versions still has a package column: %v", err)
	}

	// ---- bundled packages: imported on the primary, pulled by followers
	for _, key := range []string{"anthropic", "openai"} {
		wait(key+" enabled on every node", 3*time.Minute, func() bool { return enabledEverywhere(key) })
		sum := digestOf(key)
		for _, id := range []string{"a", "b", "c"} {
			if !held(id, sum) {
				t.Fatalf("%s package %s is not in %s's shell store", key, sum[:12], id)
			}
		}
	}
	t.Log("bundled packages: kept by the node that imported them, pulled by the others over the node network")

	// ---- a market package is downloaded by every node from the market
	adminC := newAPIClient(t, c.node("c").public.URL).login(adminEmail, adminPassword)
	sources := adminC.ok(http.MethodGet, "/market/sources", nil)
	var sourceID int64
	if list, ok := sources.get("data").([]any); ok && len(list) > 0 {
		if m, ok := list[0].(map[string]any); ok {
			v, _ := m["id"].(float64)
			sourceID = int64(v)
		}
	}
	listing := adminC.ok(http.MethodGet, "/market/plugins?source_id="+strconv.FormatInt(sourceID, 10), nil)
	marketKey2, marketVersion := firstMarketPlugin(t, listing)
	before := marketPackageGets.Load()
	review := adminC.ok(http.MethodPost, "/plugins/install-from-market", map[string]any{"source_id": sourceID, "key": marketKey2, "version": marketVersion})
	adminC.consentAll(review)
	adminC.ok(http.MethodPost, "/plugins/"+marketKey2+"/enable", nil)
	wait(marketKey2+" enabled on every node", 3*time.Minute, func() bool { return enabledEverywhere(marketKey2) })
	var marketSum, packageURL string
	if err = db.WithContext(ctx).Raw(`SELECT lower(package_sha256),package_url FROM plugin_versions WHERE plugin_key=? AND version=?`, marketKey2, marketVersion).Row().Scan(&marketSum, &packageURL); err != nil {
		t.Fatal(err)
	}
	gets := marketPackageGets.Load() - before
	if !strings.HasPrefix(packageURL, market.URL) || gets < 4 {
		t.Fatalf("market package must be fetched by the installer and each node: url=%q gets=%d", packageURL, gets)
	}
	if held("b", marketSum) {
		t.Fatal("follower b pulled a market package from another node instead of the market")
	}
	if !held("c", marketSum) {
		t.Fatal("the installing node keeps no fallback copy of the market package")
	}
	t.Logf("market %s@%s: %d downloads from the market (install + each node); b never used the node network for it", marketKey2, marketVersion, gets)

	// ---- primary down: an upload through a follower is installed on the
	// other nodes from that follower
	c.stopShell(c.node("a"))
	t.Log("primary a stopped")
	// The current core boot of node runs an instance of key.
	runningOn := func(node, key string) bool {
		var running int
		_ = db.WithContext(ctx).Raw(`SELECT count(*) FROM plugin_runtime_nodes r JOIN updater.nodes n ON n.core_boot_id=r.boot_id AND n.cluster_id=? WHERE n.node_id=? AND r.plugin_key=? AND NOT r.stopped`, c.dbname, node, key).Row().Scan(&running)
		return running > 0
	}
	adminB := newAPIClient(t, c.node("b").public.URL).login(adminEmail, adminPassword)
	review = adminB.upload("/plugins/upload", filepath.Base(uploadPath), uploaded, nil)
	if review.status != 200 && review.status != 201 {
		t.Fatalf("upload through follower b with the primary down: %s", review)
	}
	uploadKey, _ := adminB.consentAll(review)
	var uploadSum string
	if err = db.WithContext(ctx).Raw(`SELECT lower(package_sha256) FROM plugin_versions WHERE plugin_key=?`, uploadKey).Row().Scan(&uploadSum); err != nil {
		t.Fatal(err)
	}
	if !held("b", uploadSum) || held("a", uploadSum) {
		t.Fatal("an upload is stored on the node that took it and is not pushed to the primary")
	}
	adminB.ok(http.MethodPost, "/plugins/"+uploadKey+"/enable", nil)
	wait(uploadKey+" running on b and c", 3*time.Minute, func() bool { return runningOn("b", uploadKey) && runningOn("c", uploadKey) })
	if !held("c", uploadSum) {
		t.Fatalf("follower c runs %s without holding its package", uploadKey)
	}
	t.Logf("upload %s through follower b with the primary down: kept by b, pulled by c from b", uploadKey)

	// ---- primary down: a node that lost its package copies fetches them
	// from the other online node when it restarts
	// Both copies go: the shell's store and the core's unpacked packages
	// (SUB2API_PLUGIN_DIR), from which a restarted core would otherwise load
	// without asking for the package.
	for _, dir := range []string{"plugin-blobs", "plugins"} {
		if err = os.RemoveAll(filepath.Join(c.roots["c"], dir)); err != nil {
			t.Fatal(err)
		}
	}
	bootC := ""
	if st, _, err := c.node("c").runtime.Status(ctx); err == nil {
		bootC = st.BootID
	}
	restartedC := c.restartShell("c")
	wait("follower c serving without the primary or its package cache", 3*time.Minute, func() bool {
		st, mode, err := restartedC.runtime.Status(ctx)
		return err == nil && st.Ready && mode == "local" && st.BootID != bootC && probe(restartedC) == 401
	})
	wait(uploadKey+" running on the restarted c", 3*time.Minute, func() bool { return runningOn("c", uploadKey) })
	for _, key := range []string{"anthropic", "openai", uploadKey} {
		if !held("c", digestOf(key)) {
			t.Fatalf("restarted c did not fetch %s from b", key)
		}
	}
	t.Log("follower c lost its package copies and restarted with the primary down; it fetched bundled and uploaded packages from b")

	// ---- a node with nothing cached cannot start without the primary: the
	// core release still comes from the primary (plugin packages do not)
	joined := c.startShell("d")
	if err = joined.engine.Recover(ctx, false); err == nil {
		t.Fatal("a node without the release started while the primary was down")
	}
	c.run(joined)
	time.Sleep(5 * time.Second)
	if joined.supervisor.Status().Running || probe(joined) != 503 {
		t.Fatal("node d runs a core without the primary")
	}
	t.Logf("node d cannot start while the primary is down (core release): %v", err)
	restartedA := c.restartShell("a")
	wait("node d serving after the primary returned", 5*time.Minute, func() bool {
		st, mode, err := joined.runtime.Status(ctx)
		return err == nil && st.Ready && mode == "local" && probe(joined) == 401
	})
	wait("primary a serving again", 5*time.Minute, func() bool {
		st, mode, err := restartedA.runtime.Status(ctx)
		return err == nil && st.Ready && mode == "local"
	})
	wait(uploadKey+" running on a and d", 3*time.Minute, func() bool { return runningOn("a", uploadKey) && runningOn("d", uploadKey) })
	for _, id := range []string{"a", "d"} {
		if !held(id, uploadSum) {
			t.Fatalf("node %s runs %s without fetching its package from b or c", id, uploadKey)
		}
	}
	if held("d", marketSum) {
		t.Fatal("node d pulled the market package from another node instead of the market")
	}
	t.Log("after the primary returned, d started and both a and d fetched the uploaded package from the nodes that held it; the market package came from the market")
}

func firstMarketPlugin(t *testing.T, listing apiResponse) (string, string) {
	t.Helper()
	items, _ := listing.get("data").([]any)
	if m, ok := listing.get("data").(map[string]any); ok {
		items, _ = m["items"].([]any)
	}
	for _, it := range items {
		m, _ := it.(map[string]any)
		key, _ := m["key"].(string)
		versions, _ := m["versions"].([]any)
		if key == "" || len(versions) == 0 {
			continue
		}
		v, _ := versions[0].(map[string]any)
		if version, _ := v["version"].(string); version != "" {
			return key, version
		}
	}
	t.Fatalf("market lists no plugin: %s", listing)
	return "", ""
}
