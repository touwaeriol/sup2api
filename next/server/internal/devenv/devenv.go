// Package devenv prepares the environment of `sub2api dev`: a complete core
// on one machine with nothing installed. PostgreSQL is the local server of
// sdk/testpg (the real PostgreSQL 16, run from Go) with its own database,
// Redis is an in-process miniredis, and the secrets and admin account are
// generated once and kept in a state directory, so data survives restarts.
//
// Prepare only fills in variables that are not already set: pointing
// DATABASE_URL or REDIS_URL elsewhere uses those servers instead. The core
// then starts through config.Load and app.Run like any other node; dev mode
// changes where the configuration comes from, nothing else.
package devenv

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg"
	"github.com/Sub2API-Devs/sup2api/next/sdk/testpg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// Database is the database of the dev core with the default state directory
// on the local PostgreSQL server. Another state directory gets its own
// database (DatabaseFor): the secrets in the state directory must match
// the data they encrypted.
const Database = "sub2api_dev"

// DatabaseFor returns the dev database of a state directory.
func DatabaseFor(stateDir string) string {
	if def, err := DefaultStateDir(); err == nil && filepath.Clean(def) == filepath.Clean(stateDir) {
		return Database
	}
	sum := sha256.Sum256([]byte(filepath.Clean(stateDir)))
	return fmt.Sprintf("%s_%x", Database, sum[:4])
}

// AdminEmail is the bootstrap super admin of a dev core.
const AdminEmail = "admin@sub2api.localhost"

// Options configures Prepare.
type Options struct {
	// StateDir keeps secrets, plugin data and the default built-in
	// directory (default: <user cache>/sub2api-dev).
	StateDir string
	// Addr is the HTTP listen address (default 127.0.0.1:8080).
	Addr string
	// BuiltinDir holds the plugin packages installed at startup (default:
	// <StateDir>/builtin, empty).
	BuiltinDir string
	// Reset drops the dev database and plugin data first.
	Reset bool
}

// Env is a prepared dev environment.
type Env struct {
	StateDir      string
	URL           string // console / API base URL
	AdminEmail    string
	AdminPassword string
	DatabaseURL   string
	RedisURL      string
	// MemoryRedis is true when Redis is the in-process miniredis: its data
	// is lost when the process exits.
	MemoryRedis bool

	redis     *miniredis.Miniredis
	stopClock func()
}

// Close stops what Prepare started in this process (miniredis). The local
// PostgreSQL server keeps running, like after tests. Calling it again does
// nothing.
func (e *Env) Close() {
	if e.redis != nil {
		e.stopClock()
		e.redis.Close()
		e.redis = nil
	}
}

// clockTick is how often the in-memory Redis ages its keys.
const clockTick = 100 * time.Millisecond

// runClock ages the keys of m with the wall clock until the returned stop
// is called: miniredis only expires keys on FastForward, and cooldowns,
// sessions, slots and locks rely on their TTLs running out.
func runClock(m *miniredis.Miniredis) (stop func()) {
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		t := time.NewTicker(clockTick)
		defer t.Stop()
		last := time.Now()
		for {
			select {
			case <-done:
				return
			case now := <-t.C:
				m.FastForward(now.Sub(last))
				last = now
			}
		}
	}()
	return func() { close(done); <-stopped }
}

// secrets are generated on the first run and reused, so encrypted data
// stays readable across restarts.
type secrets struct {
	MasterKey     string `json:"master_key"`
	AdminPassword string `json:"admin_password"`
}

// DefaultStateDir is <user cache>/sub2api-dev.
func DefaultStateDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "sub2api-dev"), nil
}

// Prepare starts the dependencies and sets the variables config.Load
// reads. Variables that are already set are left alone.
func Prepare(ctx context.Context, opt Options) (*Env, error) {
	var err error
	if opt.StateDir == "" {
		if opt.StateDir, err = DefaultStateDir(); err != nil {
			return nil, err
		}
	}
	if opt.StateDir, err = filepath.Abs(opt.StateDir); err != nil {
		return nil, err
	}
	if opt.Addr == "" {
		opt.Addr = "127.0.0.1:8080"
	}
	pluginDir := filepath.Join(opt.StateDir, "plugins")
	if opt.BuiltinDir == "" {
		opt.BuiltinDir = filepath.Join(opt.StateDir, "builtin")
	}
	if opt.BuiltinDir, err = filepath.Abs(opt.BuiltinDir); err != nil {
		return nil, err
	}
	for _, d := range []string{opt.StateDir, opt.BuiltinDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
	}
	sec, err := loadSecrets(filepath.Join(opt.StateDir, "secrets.json"))
	if err != nil {
		return nil, err
	}
	env := &Env{StateDir: opt.StateDir, URL: "http://" + opt.Addr, AdminEmail: AdminEmail, AdminPassword: sec.AdminPassword}

	env.DatabaseURL = os.Getenv("DATABASE_URL")
	if env.DatabaseURL == "" {
		if env.DatabaseURL, err = localDatabase(ctx, DatabaseFor(opt.StateDir), opt.Reset); err != nil {
			return nil, err
		}
	} else if opt.Reset {
		return nil, errors.New("--reset only resets the local dev database; DATABASE_URL is set")
	}
	if opt.Reset {
		if err := os.RemoveAll(pluginDir); err != nil {
			return nil, err
		}
	}

	env.RedisURL = os.Getenv("REDIS_URL")
	if env.RedisURL == "" {
		env.redis = miniredis.NewMiniRedis()
		if err := env.redis.StartAddr("127.0.0.1:0"); err != nil {
			return nil, fmt.Errorf("in-memory redis: %w", err)
		}
		env.stopClock = runClock(env.redis)
		env.RedisURL = "redis://" + env.redis.Addr()
		env.MemoryRedis = true
	}

	vars := []struct{ k, v string }{
		{"SUB2API_HTTP_ADDR", opt.Addr},
		{"SUB2API_PUBLIC_URL", env.URL},
		{"DATABASE_URL", env.DatabaseURL},
		{"REDIS_URL", env.RedisURL},
		{"SUB2API_MASTER_KEY", sec.MasterKey},
		{"SUB2API_BOOTSTRAP_ADMIN_EMAIL", AdminEmail},
		{"SUB2API_BOOTSTRAP_ADMIN_PASSWORD", sec.AdminPassword},
		{"NODE_ID", "dev"},
		{"SUB2API_PLUGIN_DIR", pluginDir},
		{"SUB2API_BUILTIN_PLUGIN_DIR", opt.BuiltinDir},
		// Plugins run as plain child processes on any OS: no sandbox,
		// network namespace or seccomp filter.
		{"SUB2API_PLUGIN_DEV_MODE", "true"},
		{"SUB2API_PLUGIN_STRICT_NETWORK", "false"},
		{"SUB2API_PLUGIN_SECCOMP", "false"},
		// Nothing balances traffic in front of a dev core; restart at once.
		{"SUB2API_SHUTDOWN_DELAY", "0s"},
	}
	for _, kv := range vars {
		if os.Getenv(kv.k) == "" {
			if err := os.Setenv(kv.k, kv.v); err != nil {
				return nil, err
			}
		}
	}
	// What the core actually uses, after the caller's own variables.
	env.AdminEmail = os.Getenv("SUB2API_BOOTSTRAP_ADMIN_EMAIL")
	env.AdminPassword = os.Getenv("SUB2API_BOOTSTRAP_ADMIN_PASSWORD")
	return env, nil
}

// localDatabase starts (or reuses) the local PostgreSQL server and returns
// the DSN of the dev database, creating it when missing.
func localDatabase(ctx context.Context, database string, reset bool) (string, error) {
	base, err := testpg.Ensure(ctx)
	if err != nil {
		return "", fmt.Errorf("local PostgreSQL: %w", err)
	}
	admin, err := store.OpenSized(ctx, base, 1)
	if err != nil {
		return "", fmt.Errorf("local PostgreSQL: %w", err)
	}
	defer admin.Close()
	conn := store.G(ctx, admin.Gorm)
	name := store.Ident(database)
	if reset {
		if err := store.DDL(conn, "DROP DATABASE IF EXISTS %s WITH (FORCE)", name); err != nil {
			return "", fmt.Errorf("drop %s: %w", database, err)
		}
	}
	exists, err := gormpg.Exists(conn, conn.Table("pg_database").Where("datname = ?", database))
	if err != nil {
		return "", err
	}
	if !exists {
		if err := store.DDL(conn, "CREATE DATABASE %s", name); err != nil {
			return "", fmt.Errorf("create %s: %w", database, err)
		}
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u.Path = "/" + database
	return u.String(), nil
}

func loadSecrets(path string) (*secrets, error) {
	var s secrets
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &s); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if mk, err := base64.StdEncoding.DecodeString(s.MasterKey); err != nil || len(mk) != 32 || s.AdminPassword == "" {
			return nil, fmt.Errorf("%s is incomplete; delete it to generate new secrets (the dev data encrypted with the old key becomes unreadable: use --reset)", path)
		}
		return &s, nil
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	mk := make([]byte, 32)
	pw := make([]byte, 9)
	for _, b := range [][]byte{mk, pw} {
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
	}
	s = secrets{
		MasterKey:     base64.StdEncoding.EncodeToString(mk),
		AdminPassword: base64.RawURLEncoding.EncodeToString(pw),
	}
	b, err = json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return nil, err
	}
	return &s, nil
}
