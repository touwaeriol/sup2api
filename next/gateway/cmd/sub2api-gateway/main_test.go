package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/gateway/internal/control"
	"github.com/Sub2API-Devs/sup2api/next/gateway/internal/release"
	rc "github.com/Sub2API-Devs/sup2api/next/runtime-contract"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestConfigPropagatesResolvedClusterStoresToCore(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://shell-db/test")
	t.Setenv("REDIS_URL", "redis://:cache%40pass@shell-redis:6379/2")
	c := config{ClusterID: "test", NodeID: "a", PrimaryNode: "a", RuntimeABI: "test", PeerURL: "https://node-a", DatabaseURL: "postgres://old/test", RedisURL: "redis://old", Root: t.TempDir(), CoreEnv: []string{"DATABASE_URL=postgres://wrong/test", "REDIS_URL=redis://wrong/9", "SUB2API_LOG_LEVEL=debug"}}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "shell.json")
	if err = os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	effective := map[string]string{}
	for _, entry := range got.coreEnvironment() {
		key, value, _ := strings.Cut(entry, "=")
		effective[key] = value
	}
	if effective["DATABASE_URL"] != "postgres://shell-db/test" || effective["REDIS_URL"] != "redis://:cache%40pass@shell-redis:6379/2" {
		t.Fatalf("core stores differ from shell: %+v", effective)
	}
	if effective["SUB2API_LOG_LEVEL"] != "debug" {
		t.Fatal("unrelated core environment was lost")
	}
	if len(got.CoreEnv) != 3 {
		t.Fatal("coreEnvironment mutated config slice")
	}
	if runtime.GOOS == "linux" {
		if effective[pluginLandlockEnv] != "true" {
			t.Fatalf("plugin Landlock is not on by default: %+v", effective)
		}
		got.PluginLandlockDisabled = true
		if envMap(got.coreEnvironment())[pluginLandlockEnv] != "false" {
			t.Fatal("plugin_landlock_disabled did not turn Landlock off")
		}
		got.PluginLandlockDisabled = false
		t.Setenv(pluginLandlockEnv, "false")
		if _, set := envMap(got.coreEnvironment())[pluginLandlockEnv]; set {
			t.Fatal("a value in the gateway's environment (inherited by the core) was overridden")
		}
		os.Unsetenv(pluginLandlockEnv)
		got.CoreEnv = append(got.CoreEnv, pluginLandlockEnv+"=false")
		if envMap(got.coreEnvironment())[pluginLandlockEnv] != "false" {
			t.Fatal("an explicit core_env value was overridden")
		}
	}
}

func envMap(env []string) map[string]string {
	out := map[string]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		out[key] = value
	}
	return out
}

func TestPeerAuthKeySources(t *testing.T) {
	configured, fromEnv := strings.Repeat("c", 32), strings.Repeat("e", 40)
	write := func(key string) string {
		c := config{ClusterID: "test", NodeID: "a", PrimaryNode: "a", RuntimeABI: "test", PeerURL: "https://node-a", DatabaseURL: "postgres://db/test", RedisURL: "redis://r", Root: t.TempDir(), PeerAuthKey: key}
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "shell.json")
		if err = os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	got, err := readConfig(write(""))
	if err != nil || got.PeerAuthKey != "" {
		t.Fatalf("unset key must select automatic mode: %q %v", got.PeerAuthKey, err)
	}
	if got, err = readConfig(write(configured)); err != nil || got.PeerAuthKey != configured {
		t.Fatalf("configured key: %q %v", got.PeerAuthKey, err)
	}
	if _, err = readConfig(write("short")); err == nil {
		t.Fatal("weak configured key accepted")
	}
	t.Setenv("SUB2API_PEER_AUTH_KEY", fromEnv)
	if got, err = readConfig(write(configured)); err != nil || got.PeerAuthKey != fromEnv {
		t.Fatalf("environment must override the file: %q %v", got.PeerAuthKey, err)
	}
	// A present but invalid override fails closed instead of falling back.
	t.Setenv("SUB2API_PEER_AUTH_KEY", "short")
	if _, err = readConfig(write(configured)); err == nil {
		t.Fatal("invalid environment key fell back to the file")
	}
	// Compose renders an unset ${NODE_X_PEER_AUTH_KEY:-} as an empty value.
	t.Setenv("SUB2API_PEER_AUTH_KEY", "")
	if got, err = readConfig(write(configured)); err != nil || got.PeerAuthKey != configured {
		t.Fatalf("empty environment must keep the configured key: %q %v", got.PeerAuthKey, err)
	}
	if got, err = readConfig(write("")); err != nil || got.PeerAuthKey != "" {
		t.Fatalf("empty environment must keep automatic mode: %q %v", got.PeerAuthKey, err)
	}
}

func TestNodeCommandsUseLocalManagementRoutes(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "shell.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal("unix sockets unavailable:", err)
	}
	token, err := control.NewManagementToken()
	if err != nil {
		t.Fatal(err)
	}
	tokenFile := defaultTokenFile(socket)
	if err = control.WriteManagementToken(tokenFile, token); err != nil {
		t.Fatal(err)
	}
	seen := make(chan string, 4)
	srv := &http.Server{Handler: control.RequireManagementToken(token, http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		seen <- q.Method + " " + q.URL.EscapedPath() + " " + q.Header.Get("X-Updater-Actor")
		if strings.Contains(q.URL.Path, "missing") {
			w.WriteHeader(404)
		}
	}))}
	go srv.Serve(listener)
	t.Cleanup(func() { _ = srv.Close() })
	for command, want := range map[string]string{
		"disable-node": "POST /system/nodes/node%2Fb/disable local-operator",
		"enable-node":  "POST /system/nodes/node%2Fb/enable local-operator",
		"status":       "GET /system/upgrades local-operator",
		"yield":        "POST /system/nodes/self/yield local-operator",
		"unyield":      "DELETE /system/nodes/self/yield local-operator",
		"yield-status": "GET /system/nodes/self/yield local-operator",
	} {
		if err = localCommand(command, socket, tokenFile, "node/b"); err != nil {
			t.Fatal(command, err)
		}
		if got := <-seen; got != want {
			t.Fatalf("%s: got %q want %q", command, got, want)
		}
	}
	if err = localCommand("disable-node", socket, tokenFile, "missing"); err == nil {
		t.Fatal("HTTP failure reported as success")
	}
	<-seen
	if err = localCommand("enable-node", socket, tokenFile, ""); err == nil {
		t.Fatal("node command without -id accepted")
	}
	// A stale token (an earlier gateway boot) or no token file fails: the
	// request never reaches the management API.
	stale := filepath.Join(dir, "stale.token")
	old, err := control.NewManagementToken()
	if err != nil {
		t.Fatal(err)
	}
	if err = control.WriteManagementToken(stale, old); err != nil {
		t.Fatal(err)
	}
	if err = localCommand("status", socket, stale, ""); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("stale token accepted: %v", err)
	}
	if err = localCommand("status", socket, filepath.Join(dir, "absent.token"), ""); err == nil {
		t.Fatal("missing token file accepted")
	}
	select {
	case got := <-seen:
		t.Fatalf("unauthenticated request reached the API: %s", got)
	default:
	}
}

// The token file defaults to the shell's runtime directory beside the
// socket, so the CLI finds the serving gateway's token from the same config.
func TestManagementTokenFileDefaults(t *testing.T) {
	root := t.TempDir()
	write := func(c config) string {
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "shell.json")
		if err = os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	base := config{ClusterID: "test", NodeID: "a", PrimaryNode: "a", RuntimeABI: "test", PeerURL: "https://node-a", DatabaseURL: "postgres://db/test", RedisURL: "redis://r", Root: root}
	got, err := readConfig(write(base))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "runtime", "shell.token"); got.ManagementTokenFile != want {
		t.Fatalf("default: %q", got.ManagementTokenFile)
	}
	custom := base
	custom.ManagementSocket = filepath.Join(root, "sock", "m.sock")
	if got, err = readConfig(write(custom)); err != nil || got.ManagementTokenFile != filepath.Join(root, "sock", "shell.token") {
		t.Fatalf("socket-relative default: %q %v", got.ManagementTokenFile, err)
	}
	custom.ManagementTokenFile = "relative.token"
	if _, err = readConfig(write(custom)); err == nil {
		t.Fatal("relative token path accepted")
	}
}

// init/import fetch the manifest with the same client as serve, so a release
// origin signed by the cluster CA works for every command.
func TestPublisherClientTrustsClusterCA(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer origin.Close()
	ca := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: origin.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := publisherClient(ca)
	if err != nil {
		t.Fatal(err)
	}
	res, err := client.Get(origin.URL)
	if err != nil || res.StatusCode != 204 {
		t.Fatalf("cluster-CA origin rejected: %v %v", res, err)
	}
	res.Body.Close()
	if _, err = publisherClient(filepath.Join(t.TempDir(), "missing.crt")); err == nil {
		t.Fatal("missing CA file accepted")
	}
}

// import -manifest reads a plain release manifest from the release origin:
// the release digest is the SHA-256 of the bytes served, which it keeps for
// the release row. A signed envelope of earlier releases is no manifest.
func TestImportReleaseReadsThePlainManifest(t *testing.T) {
	m := rc.Manifest{CoreVersion: "0.1.292", ManifestVersion: 1, ReleaseID: "core-v0.1.292", BuildID: "b", Strategy: "rolling",
		ShellProtocol: rc.Range{Min: rc.Protocol, Max: rc.Protocol}, CoreControlProtocol: rc.Range{Min: rc.Protocol, Max: rc.Protocol}}
	manifest, _ := json.MarshalIndent(m, "", "  ")
	compact, _ := json.Marshal(m)
	envelope, _ := json.Marshal(map[string]any{"key_id": "k", "payload": compact, "signature": []byte("sig")})
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rel/next-core-manifest.json":
			_, _ = w.Write(manifest)
		case "/rel/signed.json":
			_, _ = w.Write(envelope)
		default:
			http.NotFound(w, r)
		}
	}))
	defer origin.Close()
	c := config{ReleaseOrigin: origin.URL + "/rel/"}
	mgr := &release.Manager{Client: origin.Client()}
	ctx := context.Background()
	r, err := importRelease(ctx, c, mgr, origin.URL+"/rel/next-core-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(manifest); r.Digest != hex.EncodeToString(sum[:]) || !bytes.Equal(r.Raw, manifest) || r.Manifest.ReleaseID != m.ReleaseID || r.BundleBase != origin.URL+"/rel" {
		t.Fatalf("imported %+v", r)
	}
	if _, err = importRelease(ctx, c, mgr, origin.URL+"/rel/signed.json"); err == nil {
		t.Fatal("a signed envelope was imported as a manifest")
	}
	if _, err = importRelease(ctx, c, mgr, origin.URL+"/other/next-core-manifest.json"); err == nil {
		t.Fatal("a manifest outside the release origin was imported")
	}
}

func TestParseRedisURLAcceptsValkeySchemes(t *testing.T) {
	for raw, tls := range map[string]bool{
		"redis://cache:6379/0":    false,
		"valkey://cache:6379/0":   false,
		"valkeys://cache:6380/0":  true,
		"rediss://cache:6380/0":   true,
		" VALKEY://cache:6379/0 ": false,
	} {
		opt, err := parseRedisURL(raw)
		if err != nil {
			t.Errorf("%q: %v", raw, err)
			continue
		}
		if (opt.TLSConfig != nil) != tls || !strings.HasPrefix(opt.Addr, "cache:") {
			t.Errorf("%q: addr=%s tls=%v", raw, opt.Addr, opt.TLSConfig != nil)
		}
	}
	if _, err := parseRedisURL("memcached://cache:11211"); err == nil {
		t.Error("memcached:// accepted")
	}
}

// A password-protected cache URL keeps its credentials through parsing, in
// every accepted scheme, including percent-encoded characters.
func TestParseRedisURLWithPassword(t *testing.T) {
	for raw, want := range map[string]struct {
		user, password, addr string
		db                   int
		tls                  bool
	}{
		"redis://:s3cret@redis:6379/0":           {"", "s3cret", "redis:6379", 0, false},
		"valkey://:p%40ss%2Fw%3Ard@cache:6379/2": {"", "p@ss/w:rd", "cache:6379", 2, false},
		"valkeys://default:pw@cache:6380/1":      {"default", "pw", "cache:6380", 1, true},
		"rediss://:pw@cache:6380/0":              {"", "pw", "cache:6380", 0, true},
	} {
		opt, err := parseRedisURL(raw)
		if err != nil {
			t.Errorf("%q: %v", raw, err)
			continue
		}
		if opt.Username != want.user || opt.Password != want.password || opt.Addr != want.addr || opt.DB != want.db || (opt.TLSConfig != nil) != want.tls {
			t.Errorf("%q: user=%q password=%q addr=%s db=%d tls=%v", raw, opt.Username, opt.Password, opt.Addr, opt.DB, opt.TLSConfig != nil)
		}
	}
}

// With requirepass on, only clients built from the URL with the right
// password can use the cache; the same URL also works against a cache that
// has no password yet, which is what lets clients switch first during a
// migration (go-redis authenticates with HELLO 3 AUTH default <password>).
func TestRedisPasswordIsEnforced(t *testing.T) {
	ctx := context.Background()
	ping := func(raw string) error {
		opt, err := parseRedisURL(raw)
		if err != nil {
			return err
		}
		client := redis.NewClient(opt)
		defer client.Close()
		return client.Ping(ctx).Err()
	}
	open := miniredis.RunT(t)
	if err := ping("valkey://:s3cret@" + open.Addr() + "/0"); err != nil {
		t.Fatalf("password URL against a cache without a password: %v", err)
	}
	locked := miniredis.RunT(t)
	locked.RequireAuth("s3cret")
	if err := ping("valkey://:s3cret@" + locked.Addr() + "/0"); err != nil {
		t.Fatalf("correct password rejected: %v", err)
	}
	if err := ping("redis://" + locked.Addr() + "/0"); err == nil || !strings.Contains(err.Error(), "NOAUTH") {
		t.Fatalf("client without password accepted: %v", err)
	}
	if err := ping("redis://:wrong@" + locked.Addr() + "/0"); err == nil {
		t.Fatal("wrong password accepted")
	}
}

// The pool is sized by SUB2API_GATEWAY_POOL_MAX_CONNS, else gatewayPool.
func TestOpenPoolSize(t *testing.T) {
	for env, maxConns := range map[string]int{"": gatewayPool.MaxConns, "3": 3, "x": gatewayPool.MaxConns} {
		t.Setenv("SUB2API_GATEWAY_POOL_MAX_CONNS", env)
		db, err := openPool(config{DatabaseURL: "host=db dbname=app"})
		if err != nil {
			t.Fatal(err)
		}
		if sqlDB, _ := db.DB(); sqlDB.Stats().MaxOpenConnections != maxConns {
			t.Errorf("SUB2API_GATEWAY_POOL_MAX_CONNS=%q: max_conns=%d, want %d", env, sqlDB.Stats().MaxOpenConnections, maxConns)
		}
		closePool(db)
	}
}

// An Exec on the pool reads its result before its connection goes back:
// database/sql reads RowsAffected after, under the connection's mutex,
// which the connection's next statement holds while it waits for a row
// lock. GORM reads the result right after the Exec, so the test widens the
// window on the pool GORM runs on: T holds a row lock on one of two
// connections, A's UPDATE runs on the other, B takes it and waits for T's
// lock, and only then is A's result read.
func TestOpenPoolExecDoesNotWaitForRowLocks(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	ctx := context.Background()
	t.Setenv("SUB2API_GATEWAY_POOL_MAX_CONNS", "2")
	db, err := openPool(config{DatabaseURL: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer closePool(db)
	t.Setenv("SUB2API_GATEWAY_POOL_MAX_CONNS", "1")
	watch, err := openPool(config{DatabaseURL: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer closePool(watch)

	table := fmt.Sprintf("gateway_exec_lock_%d_%d", os.Getpid(), time.Now().UnixNano())
	if err := db.Exec(`CREATE TABLE ` + table + ` (id int PRIMARY KEY, n int NOT NULL DEFAULT 0)`).Error; err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DROP TABLE ` + table)
	if err := db.Exec(`INSERT INTO ` + table + ` (id) VALUES (1), (2)`).Error; err != nil {
		t.Fatal(err)
	}

	tx := db.WithContext(ctx).Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()
	if err := tx.Exec(`UPDATE ` + table + ` SET n = 1 WHERE id = 1`).Error; err != nil {
		t.Fatal(err)
	}
	res, err := db.WithContext(ctx).Statement.ConnPool.ExecContext(ctx, `UPDATE `+table+` SET n = 2 WHERE id = 2`)
	if err != nil {
		t.Fatal(err)
	}
	waiter := make(chan error, 1)
	go func() { waiter <- db.WithContext(ctx).Exec(`UPDATE ` + table + ` SET n = 3 WHERE id = 1`).Error }()
	for deadline := time.Now().Add(10 * time.Second); ; {
		var n int
		err := watch.Raw(`SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND query LIKE ?`, "%"+table+"%").Row().Scan(&n)
		if err == nil && n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("B never waited for T's lock: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	read := make(chan int64, 1)
	go func() {
		n, _ := res.RowsAffected()
		read <- n
	}()
	select {
	case n := <-read:
		if n != 1 {
			t.Errorf("RowsAffected = %d, want 1", n)
		}
	case <-time.After(5 * time.Second):
		tx.Rollback() // frees B, and with it the read
		<-read
		t.Fatal("A's RowsAffected waits for B, which waits for T's row lock")
	}
	if err := tx.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	if err := <-waiter; err != nil {
		t.Fatal(err)
	}
}

func TestMasterCipherFollowsTheCoreEnvironment(t *testing.T) {
	key := func(b byte) string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32)) }
	t.Setenv(masterKeyEnv, "")
	if c, err := (config{}).masterCipher(); c != nil || err != nil {
		t.Fatalf("no key: %v %v", c, err)
	}
	t.Setenv(masterKeyEnv, key(1))
	fromEnv, err := (config{}).masterCipher()
	if err != nil || fromEnv == nil {
		t.Fatal(err)
	}
	sealed, _ := fromEnv.Encrypt([]byte("x"), nil)
	// core_env wins over the gateway's environment, as for the core.
	fromCoreEnv, err := (config{CoreEnv: []string{masterKeyEnv + "=" + key(2)}}).masterCipher()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fromCoreEnv.Decrypt(sealed, nil); err == nil {
		t.Fatal("core_env key ignored")
	}
	if _, err = (config{CoreEnv: []string{masterKeyEnv + "=short"}}).masterCipher(); err == nil {
		t.Fatal("malformed master key accepted")
	}
}

func TestVersionCommand(t *testing.T) {
	opts, err := parseCLI([]string{"version"})
	if err != nil || opts.command != "version" || Version == "" {
		t.Fatalf("%+v %v %q", opts, err, Version)
	}
}
