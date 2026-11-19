package dbschema_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"gorm.io/gorm"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pgdb"
	sdkegress "github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/egress"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/dbschema"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/egress"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// tunnel serves the egress tunnel of plugin key, with the database proxy of
// m, over bufconn and installs the SDK's tunnel client, as a sandboxed
// plugin process has it. database is the plugin's Database policy.
func tunnel(t *testing.T, m *dbschema.Manager, key string, database bool) {
	t.Helper()
	p := egress.New(nil, egress.Options{NodeID: "node-1", DatabaseProxy: m.ServeTunnel})
	t.Cleanup(p.Close)
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	pluginv1.RegisterEgressServiceServer(srv, p.ServerFor(key, func() core.EgressPolicy {
		return core.EgressPolicy{Mode: egress.PolicyAllowAll, Database: database}
	}))
	go func() { _ = srv.Serve(lis) }()
	cc, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cc.Close(); srv.Stop() })
	oldT, oldR := http.DefaultTransport, net.DefaultResolver
	t.Cleanup(func() { http.DefaultTransport, net.DefaultResolver = oldT, oldR })
	if err := sdkegress.Install(pluginv1.NewEgressServiceClient(cc)); err != nil {
		t.Fatal(err)
	}
}

// byAddress resolves the host of dsn now: the SDK's tunnel client replaces
// the process's resolver, which a plugin process has to itself but this
// test shares with the core's side of the proxy.
func byAddress(t *testing.T, dsn string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	addrs, err := net.DefaultResolver.LookupHost(context.Background(), u.Hostname())
	if err != nil || len(addrs) == 0 {
		t.Fatalf("resolve %s: %v", u.Hostname(), err)
	}
	u.Host = net.JoinHostPort(addrs[0], u.Port())
	return u.String()
}

// pluginDB opens the handle the SDK's Host.DB opens behind the tunnel.
func pluginDB(t *testing.T, dsn, schema string, conns int) *gorm.DB {
	t.Helper()
	db, err := pgdb.Open(dsn, pgdb.Options{SearchPath: schema, MaxConns: conns, Dial: sdkegress.DialContext, DialByName: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pgdb.Close(db) })
	return db
}

// A plugin behind the tunnel gets a DSN without credentials, and the core
// logs in for it with its own user (password authentication) and the
// plugin's schema as search_path.
func TestTunnelProxy(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	host, hostDSN, core := coreUser(t, db)
	u, _ := url.Parse(hostDSN)
	password, _ := u.User.Password()
	key := randKey()
	m := dbschema.New(host, byAddress(t, hostDSN), nil)
	t.Cleanup(func() { _ = m.Drop(context.Background(), key) })
	tunnel(t, m, key, true)

	dsn, schema, err := m.DSN(ctx, key, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dsn, password) || strings.Contains(dsn, core) || !strings.Contains(dsn, egress.DatabaseHost) {
		t.Fatalf("tunnel DSN %s carries the core's credentials or not the virtual address", dsn)
	}
	pdb := pluginDB(t, dsn, schema, 8)

	// Its own table, its schema, the core's user.
	if err := pdb.Exec(`CREATE TABLE items (id int PRIMARY KEY, v text)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := pdb.Exec(`INSERT INTO items VALUES (1, 'a')`).Error; err != nil {
		t.Fatal(err)
	}
	var who struct {
		User, Path string
		N          int
	}
	if err := pdb.Raw(`SELECT current_user AS "user", current_setting('search_path') AS path, (SELECT count(*) FROM items) AS n`).Scan(&who).Error; err != nil {
		t.Fatal(err)
	}
	if who.User != core || who.Path != schema || who.N != 1 {
		t.Fatalf("session = %+v, want user %s, search_path %s, 1 row", who, core, schema)
	}
	// The server connection is encrypted exactly when the core's own are.
	var coreTLS, pluginTLS bool
	if err := host.SQL.QueryRowContext(ctx, `SELECT ssl FROM pg_stat_ssl WHERE pid = pg_backend_pid()`).Scan(&coreTLS); err != nil {
		t.Fatal(err)
	}
	if err := pdb.Raw(`SELECT ssl FROM pg_stat_ssl WHERE pid = pg_backend_pid()`).Scan(&pluginTLS).Error; err != nil || pluginTLS != coreTLS {
		t.Fatalf("plugin session TLS %v, core's %v: %v", pluginTLS, coreTLS, err)
	}
	t.Logf("server connections encrypted: %v", coreTLS)
	var owner string
	if err := host.SQL.QueryRowContext(ctx, `SELECT tableowner FROM pg_tables WHERE schemaname = $1 AND tablename = 'items'`, schema).Scan(&owner); err != nil || owner != core {
		t.Fatalf("table owner %s %v", owner, err)
	}

	// A large result set.
	var rows []struct {
		G int
		V string
	}
	if err := pdb.Raw(`SELECT g, repeat('x', 1000) AS v FROM generate_series(1, 20000) g`).Scan(&rows).Error; err != nil || len(rows) != 20000 || len(rows[19999].V) != 1000 {
		t.Fatalf("large result: %d rows, %v", len(rows), err)
	}

	// Transactions: a rollback leaves nothing, a commit stays.
	boom := errors.New("boom")
	if err := pdb.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`INSERT INTO items VALUES (2, 'b')`).Error; err != nil {
			return err
		}
		return boom
	}); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if err := pdb.Transaction(func(tx *gorm.DB) error { return tx.Exec(`INSERT INTO items VALUES (3, 'c')`).Error }); err != nil {
		t.Fatal(err)
	}
	var ids []int
	if err := pdb.Raw(`SELECT id FROM items ORDER BY id`).Scan(&ids).Error; err != nil || len(ids) != 2 || ids[1] != 3 {
		t.Fatalf("after the transactions: %v %v", ids, err)
	}

	// Concurrent connections.
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var n int
			if err := pdb.Raw(`SELECT count(*) + ? FROM items, pg_sleep(0.05)`, i).Scan(&n).Error; err != nil || n != 2+i {
				errs <- errors.Join(err, errors.New("wrong count"))
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	// A cancelled query: pgx asks for its cancellation (a cancel request
	// through the tunnel to the virtual address), so the server stops it
	// at once instead of sleeping on.
	qctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	start := time.Now()
	err = pdb.WithContext(qctx).Exec(`SELECT pg_sleep(60)`).Error
	cancel()
	if err == nil || time.Since(start) > 20*time.Second {
		t.Fatalf("cancelled query: %v after %s", err, time.Since(start))
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var n int
		if err := host.SQL.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity WHERE query = 'SELECT pg_sleep(60)' AND state = 'active'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the cancelled query still runs on the server")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// The handle still works afterwards.
	var n int
	if err := pdb.Raw(`SELECT count(*) FROM items`).Scan(&n).Error; err != nil || n != 2 {
		t.Fatalf("after the cancel: %d %v", n, err)
	}

	// The start-up the plugin receives: authentication ok without a
	// password exchange, parameters, key data, ready - and no password.
	got := rawStartup(t)
	if bytes.Contains(got, []byte(password)) {
		t.Fatal("the start-up bytes contain the core's password")
	}
	if len(got) < 9 || got[0] != 'R' || binary.BigEndian.Uint32(got[5:9]) != 0 {
		t.Fatalf("first message is not AuthenticationOk: %q", got[:min(len(got), 16)])
	}
}

// rawStartup sends a start-up message to the virtual address through the
// tunnel and returns every byte received up to and including ReadyForQuery.
func rawStartup(t *testing.T) []byte {
	t.Helper()
	c, err := sdkegress.DialContext(context.Background(), "tcp", net.JoinHostPort(egress.DatabaseHost, "5432"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	body := []byte{0, 3, 0, 0} // protocol 3.0
	body = append(body, "user\x00plugin\x00database\x00plugin\x00\x00"...)
	msg := binary.BigEndian.AppendUint32(nil, uint32(len(body)+4))
	if _, err := c.Write(append(msg, body...)); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(30 * time.Second))
	var all []byte
	for {
		head := make([]byte, 5)
		if _, err := io.ReadFull(c, head); err != nil {
			t.Fatalf("read start-up reply: %v (got %q)", err, all)
		}
		rest := make([]byte, binary.BigEndian.Uint32(head[1:])-4)
		if _, err := io.ReadFull(c, rest); err != nil {
			t.Fatal(err)
		}
		all = append(append(all, head...), rest...)
		switch head[0] {
		case 'Z':
			return all
		case 'E':
			t.Fatalf("start-up refused: %q", rest)
		}
	}
}

// Without the database permission the virtual address is refused.
func TestTunnelProxyNeedsDatabaseGrant(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	key := randKey()
	m := dbschema.New(db, testutil.DSN(db), nil)
	t.Cleanup(func() { _ = m.Drop(context.Background(), key) })
	tunnel(t, m, key, false)
	dsn, schema, err := m.DSN(ctx, key, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := pluginDB(t, dsn, schema, 1).Exec(`SELECT 1`).Error; err == nil || !strings.Contains(err.Error(), "no database") {
		t.Fatalf("plugin without db.schema: %v", err)
	}
	var exists bool
	if err := db.SQL.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE application_name = $1)`, "sub2api plugin "+key).Scan(&exists); err != nil || exists {
		t.Fatalf("a server session was opened: %v %v", exists, err)
	}
}
