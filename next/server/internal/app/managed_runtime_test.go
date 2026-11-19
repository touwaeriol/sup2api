package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	runtimecontract "github.com/Sub2API-Devs/sup2api/next/runtime-contract"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/config"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/migrations"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// This exercises the actual app.Run and its private control listener with a
// real PostgreSQL database. The Redis double is sufficient for cluster cache
// and heartbeat behavior; cluster E2E separately uses real Redis.
func TestManagedRuntimeCandidatePrepareAdmitAndDrain(t *testing.T) {
	testManagedRuntime(t, false)
}

func TestManagedRuntimeMigrationDoesNotBootstrap(t *testing.T) {
	testManagedRuntime(t, true)
}

// managedRuntimeDB is a migrated database; through 0099 when the run
// replays migrations: 0100 drops the role tables the migrations before it
// write, so they replay on the schema before it (0100 then runs for the
// first time).
func managedRuntimeDB(t *testing.T, migrate bool) *store.DB {
	if migrate {
		return testutil.DBThrough(t, "0099_plugin_permission_meta.sql")
	}
	return testutil.DB(t)
}

func testManagedRuntime(t *testing.T, migrate bool) {
	t.Helper()
	db := managedRuntimeDB(t, migrate)
	mr := miniredis.RunT(t)
	dir, err := os.MkdirTemp("", "s2core-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	prepare := runtimecontract.PrepareRequest{BootID: "boot", ReleaseDigest: "release"}
	if migrate {
		// Replay the suffix from FirstIdempotentMigration: every migration from
		// there on can run again (migrations_db_test.go holds them to it).
		// 0023 and 0024 were replayable too, but 0025 shipped as a plain
		// CREATE TABLE and is immutable now that it is deployed. Keep the
		// recorded history a prefix, rather than removing one row from the
		// middle of the inventory.
		// This must run the embedded migration without importing the deliberately
		// invalid bundled plugin or creating the configured bootstrap account.
		const rewindFrom = migrations.FirstIdempotentMigration
		_, err = db.SQL.ExecContext(context.Background(), `DELETE FROM schema_migrations WHERE id >= $1`, rewindFrom)
		if err != nil {
			t.Fatal(err)
		}
		inventory, err := migrationInventory(migrations.FS)
		if err != nil {
			t.Fatal(err)
		}
		prefix := slices.IndexFunc(inventory, func(m coreMigration) bool { return m.name == rewindFrom })
		if prefix < 0 {
			t.Fatal("rewind migration missing from inventory")
		}
		prepare.AllowMigration = true
		prepare.CoordinatePlugins = true
		prepare.ExpectedSchemaBefore = inventoryContract(inventory[:prefix])
		prepare.ExpectedSchemaAfter = inventoryContract(inventory)
	}
	if err = os.WriteFile(filepath.Join(dir, "invalid.s2plugin"), []byte("must never import this bundle"), 0600); err != nil {
		t.Fatal(err)
	}
	port, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := port.Addr().String()
	port.Close()
	cfg := &config.Config{HTTPAddr: addr, NodeID: "managed-runtime-test", DatabaseURL: testutil.DSN(db), RedisURL: "redis://" + mr.Addr(), MasterKey: bytes.Repeat([]byte{1}, 32), BootstrapAdminEmail: "must-not-bootstrap@example.com", BootstrapAdminPassword: "must-not-bootstrap-password",
		// A drained core must not wait this out (the shell already moved its traffic).
		ShutdownDelay: 5 * time.Second,
		Managed:       config.ManagedConfig{Enabled: true, Socket: filepath.Join(dir, "control.sock"), Token: strings.Repeat("t", 32), ReleaseDigest: "release", BootID: "boot"},
		Plugins:       config.PluginConfig{DataDir: filepath.Join(dir, "plugins"), BuiltinDir: dir, DevMode: true, AllowUnsigned: true}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		done <- Run(ctx, cfg, "0.1.0-dev", slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			t.Error("managed runtime cleanup did not finish")
		}
	})
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", cfg.Managed.Socket)
	}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 2 * time.Second}
	call := func(method, path string, body any) (int, []byte, error) {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, "http://core"+path, bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+cfg.Managed.Token)
		resp, err := client.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		out, err := io.ReadAll(resp.Body)
		return resp.StatusCode, out, err
	}
	waitStatus := func(mode string) runtimecontract.Status {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			_, b, err := call("GET", runtimecontract.StatusPath, nil)
			if err == nil {
				var s runtimecontract.Status
				if json.Unmarshal(b, &s) == nil {
					if s.Mode == "failed" {
						t.Fatalf("core failed: %+v", s)
					}
					if s.Mode == mode && (mode != "prepared" || len(s.Blockers) == 0) {
						return s
					}
				}
			}
			select {
			case err := <-done:
				t.Fatalf("core exited before %s: %v", mode, err)
			default:
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s", mode)
		return runtimecontract.Status{}
	}
	hello := waitStatus("candidate")
	if hello.Protocol != runtimecontract.Protocol || hello.HostAPIVersion != protocol.HostAPIVersion || hello.TaskProtocol != (runtimecontract.Range{Min: 1, Max: 1}) || hello.ClusterProtocol != (runtimecontract.Range{Min: 1, Max: 1}) {
		t.Fatalf("reported capabilities differ from compiled runtime: %+v", hello)
	}
	var users int
	if err = db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&users); err != nil || users != 0 {
		t.Fatalf("candidate bootstrapped users: %d %v", users, err)
	}
	if conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond); err == nil {
		conn.Close()
		t.Fatal("candidate exposed business listener")
	}
	if code, b, err := call("POST", runtimecontract.PreparePath, prepare); err != nil || code != 202 {
		t.Fatalf("prepare: %d %s %v", code, b, err)
	}
	s := waitStatus("prepared")
	if err = VerifyCoreSchema(ctx, db); err != nil {
		t.Fatalf("embedded schema not prepared: %v", err)
	}
	if s.Ready {
		t.Fatal("prepared core became ready without permit")
	}
	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatal("prepared HTTP readiness", resp.StatusCode)
	}
	if err = db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&users); err != nil || users != 0 {
		t.Fatalf("prepare bootstrapped users: %d %v", users, err)
	}
	_, err = db.SQL.ExecContext(ctx, `CREATE SCHEMA updater;CREATE TABLE updater.node_admissions(node_id text PRIMARY KEY,core_boot_id text,release_digest text,revision bigint,serve_http bool,claim_background bool,coordinate_plugins bool);
		INSERT INTO updater.node_admissions VALUES('managed-runtime-test','boot','release',1,true,true,true)`)
	if err != nil {
		t.Fatal(err)
	}
	a := runtimecontract.Admission{BootID: "boot", ReleaseDigest: "release", Revision: 1, ServeHTTP: true, ClaimBackground: true, CoordinatePlugins: true}
	if code, b, err := call("POST", runtimecontract.AdmissionPath, a); err != nil || code != 200 {
		t.Fatalf("admission: %d %s %v", code, b, err)
	}
	if s = waitStatus("serving"); !s.Ready {
		t.Fatalf("not ready: %+v", s)
	}
	resp, err = http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal("admitted HTTP readiness", resp.StatusCode)
	}
	// Every module registered its cluster tasks (TASK-FRAMEWORK-DESIGN §14.1).
	wantTasks := []string{"core.account.automatic_recovery", "core.account.balance_sweep", "core.account.credential_refresh", "core.account.usage_limits",
		"core.audit.plugin_writes_purge", "core.egress.logs_prune",
		"core.events.retention", "core.jobs.retention", "core.notify.purge", "core.task.housekeeping", "core.usage.reconcile", "core.usage.rollup"}
	var gotTasks []string
	if err = db.SQL.QueryRowContext(ctx, `SELECT coalesce(array_agg(name ORDER BY name), '{}') FROM scheduled_tasks WHERE source = 'core'`).Scan(&gotTasks); err != nil || !slices.Equal(gotTasks, wantTasks) {
		t.Fatalf("cluster tasks: %v %v", gotTasks, err)
	}
	// Revoking the persisted permit changes nothing in the core: it keeps
	// serving until the shell drains it (2026-11-08, CONTRACTS §29.1).
	_, err = db.SQL.ExecContext(ctx, `DELETE FROM updater.node_admissions`)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, b, err := call("GET", runtimecontract.StatusPath, nil); err != nil || json.Unmarshal(b, &s) != nil {
		t.Fatalf("status: %s %v", b, err)
	}
	if s.Mode != "serving" || !s.Ready {
		t.Fatalf("core stopped itself on revocation: %+v", s)
	}
	if resp, err = http.Get("http://" + cfg.HTTPAddr + "/readyz"); err != nil || resp.StatusCode != 200 {
		t.Fatalf("HTTP closed on revocation: %v %v", resp, err)
	}
	resp.Body.Close()
	// The shell's drain closes HTTP and every new background claim, then waits
	// for the real runtime cleanup before reporting drain complete.
	if code, b, err := call("POST", runtimecontract.DrainPath, runtimecontract.DrainRequest{OperationID: "shell-stop", Deadline: time.Now().Add(time.Minute)}); err != nil || code != 202 {
		t.Fatalf("drain: %d %s %v", code, b, err)
	}
	revoked := time.Now()
	s = waitStatus("drained")
	if !s.DrainComplete || s.Ready {
		t.Fatalf("bad drained status: %+v", s)
	}
	if took := time.Since(revoked); took > 3*time.Second {
		t.Fatalf("drain took %s: a drained core must skip the shutdown delay and grace", took)
	}
	if code, b, err := call("POST", runtimecontract.ShutdownPath, nil); err != nil || code != 200 {
		t.Fatalf("shutdown: %d %s %v", code, b, err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("managed core did not exit")
	}
}
