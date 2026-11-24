// Package config loads server configuration from environment variables.
// Every variable is documented in docs/CONTRACTS.md.
package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	rc "github.com/Sub2API-Devs/sup2api/next/runtime-contract"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// SensitiveEnv are the variables Load reads that carry secrets (database and
// Redis credentials, the master key, the bootstrap password,
// the supervisor token). main removes them from the process environment once
// the configuration is loaded (procguard.ScrubEnv): everything after that
// reads the loaded Config, never the environment. Load does not read the
// SUB2API_DATABASE_URL / SUB2API_REDIS_URL spellings, but deployment files
// still set them next to DATABASE_URL / REDIS_URL, so they are removed too.
var SensitiveEnv = []string{
	"DATABASE_URL",
	"REDIS_URL",
	"SUB2API_DATABASE_URL",
	"SUB2API_REDIS_URL",
	"SUB2API_MASTER_KEY",
	"SUB2API_BOOTSTRAP_ADMIN_PASSWORD",
	"SUB2API_CONTROL_TOKEN",
}

type Config struct {
	HTTPAddr  string // SUB2API_HTTP_ADDR, default ":8080"
	PublicURL string // SUB2API_PUBLIC_URL, e.g. "http://127.0.0.1:3120"
	// WebAuthnRPID is the passkey relying party (SUB2API_WEBAUTHN_RP_ID), a parent
	// domain to share passkeys across hosts. Empty: the host the console is on.
	WebAuthnRPID string
	DatabaseURL  string // DATABASE_URL (required)
	RedisURL     string // REDIS_URL (required): any Redis-protocol server, Valkey by default; redis://, rediss://, valkey://, valkeys:// or unix:// (cluster.ParseRedisURL)
	NodeID       string // NODE_ID, default hostname
	LogLevel     string // SUB2API_LOG_LEVEL: debug | info | warn | error

	MasterKey []byte // SUB2API_MASTER_KEY, base64 of 32 bytes (AES-256-GCM)

	// A console session (s2s_, CONTRACTS §100) ends unused for
	// SessionIdleTTL and SessionMaxTTL after its login, used or not.
	SessionIdleTTL time.Duration // SUB2API_SESSION_IDLE_TTL, default 168h
	SessionMaxTTL  time.Duration // SUB2API_SESSION_MAX_TTL, default 720h

	BootstrapAdminEmail    string // SUB2API_BOOTSTRAP_ADMIN_EMAIL
	BootstrapAdminPassword string // SUB2API_BOOTSTRAP_ADMIN_PASSWORD

	Plugins PluginConfig

	// AllowPrivateUpstream disables the SSRF guard for upstream URLs built by
	// platform plugins (SUB2API_GATEWAY_ALLOW_PRIVATE_UPSTREAM). Test only.
	AllowPrivateUpstream bool

	// TrustedProxies lists proxy IPs/CIDRs whose X-Forwarded-For is trusted
	// (SUB2API_TRUSTED_PROXIES, comma separated; empty = trust none). A
	// managed core also trusts loopback: its shell.
	TrustedProxies []string

	// Managed is enabled only when a local supervisor owns process admission.
	Managed ManagedConfig

	// ShutdownDelay is how long the node reports unhealthy before it stops
	// accepting connections, so load balancers take it out first
	// (SUB2API_SHUTDOWN_DELAY, default 3s; `sub2api dev` sets 0).
	ShutdownDelay time.Duration

	// Environment is the process layer of the environment variables
	// (CONTRACTS §74.3): each declared ProcessEnv that is set.
	Environment core.EnvironmentValues
}

type ManagedConfig struct {
	Enabled                              bool
	Socket, Token, ReleaseDigest, BootID string
	// UpdaterSocket is the shell's management socket (UPDATER_SOCKET) and
	// UpdaterToken authenticates the core to it: read from the file
	// SUB2API_UPDATER_TOKEN_FILE names, 64 hex characters, kept in memory
	// only. Only a managed core reads them, and it requires the token.
	UpdaterSocket, UpdaterToken string
}

// readUpdaterToken reads the file SUB2API_UPDATER_TOKEN_FILE names.
func readUpdaterToken() (string, error) {
	path := os.Getenv("SUB2API_UPDATER_TOKEN_FILE")
	if path == "" {
		return "", fmt.Errorf("managed core requires SUB2API_UPDATER_TOKEN_FILE")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("SUB2API_UPDATER_TOKEN_FILE: %w", err)
	}
	tok := strings.TrimSpace(string(raw))
	if len(tok) != 64 {
		return "", fmt.Errorf("SUB2API_UPDATER_TOKEN_FILE must hold 64 hexadecimal characters")
	}
	for _, r := range tok {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return "", fmt.Errorf("SUB2API_UPDATER_TOKEN_FILE must hold 64 hexadecimal characters")
		}
	}
	return tok, nil
}

type PluginConfig struct {
	DataDir          string   // SUB2API_PLUGIN_DIR, default "/var/lib/sub2api/plugins"
	DevMode          bool     // SUB2API_PLUGIN_DEV_MODE: allow non-Linux, no sandbox
	AllowUnsigned    bool     // SUB2API_PLUGIN_ALLOW_UNSIGNED
	VerifySignatures bool     // SUB2API_PLUGIN_VERIFY_SIGNATURES, default true
	OfficialRootKeys []string // SUB2API_PLUGIN_OFFICIAL_KEYS: comma separated "keyId=base64pub"
	StrictNetwork    bool     // SUB2API_PLUGIN_STRICT_NETWORK, default true on linux
	Seccomp          bool     // SUB2API_PLUGIN_SECCOMP, default true on linux
	MaxPackageBytes  int64    // SUB2API_PLUGIN_MAX_PACKAGE_BYTES, default rc.PluginPackageMaxBytes (64 MiB)
	MaxMemoryMB      int      // SUB2API_PLUGIN_MAX_MEMORY_MB, node cap on per-plugin memory; default 0 = no cap
	Landlock         bool     // SUB2API_PLUGIN_LANDLOCK: Landlock (files, unix sockets, signals), default true on linux (CONTRACTS §43.8)
	MarketBaseURL    string   // SUB2API_MARKET_BASE_URL: base of the plugin store's channel indexes, "" = official; local markets of test stacks only (CONTRACTS §137.6)
	// MarketAllowHTTP accepts http:// market sources and package URLs
	// besides https:// (SUB2API_MARKET_ALLOW_HTTP, default false): local
	// markets of test stacks and development only (CONTRACTS §136).
	MarketAllowHTTP bool
	// BuiltinDir holds the built-in plugin packages installed at startup
	// (SUB2API_BUILTIN_PLUGIN_DIR, default "/opt/sub2api/builtin").
	BuiltinDir string
	// RunDir holds one private 0700 directory per plugin process: the
	// go-plugin sockets and the plugin's TMPDIR (SUB2API_PLUGIN_RUN_DIR,
	// default "<DataDir>/.run"). Unix socket paths are limited to ~107
	// bytes, so it must be short.
	RunDir string
	// EgressAllowPrivate lets the plugin egress tunnel reach private,
	// loopback and link-local addresses (SUB2API_PLUGIN_EGRESS_ALLOW_PRIVATE,
	// default false). Test and development only; the PostgreSQL and Redis
	// addresses of the core stay blocked either way.
	EgressAllowPrivate bool
}

// Load reads the environment. goos is runtime.GOOS (passed in for tests).
func Load(goos string) (*Config, error) {
	c := &Config{
		HTTPAddr:               env("SUB2API_HTTP_ADDR", ":8080"),
		PublicURL:              env("SUB2API_PUBLIC_URL", ""),
		WebAuthnRPID:           env("SUB2API_WEBAUTHN_RP_ID", ""),
		DatabaseURL:            os.Getenv("DATABASE_URL"),
		RedisURL:               os.Getenv("REDIS_URL"),
		NodeID:                 os.Getenv("NODE_ID"),
		LogLevel:               env("SUB2API_LOG_LEVEL", "info"),
		BootstrapAdminEmail:    os.Getenv("SUB2API_BOOTSTRAP_ADMIN_EMAIL"),
		BootstrapAdminPassword: os.Getenv("SUB2API_BOOTSTRAP_ADMIN_PASSWORD"),
	}
	c.Managed = ManagedConfig{Enabled: boolEnv("SUB2API_MANAGED", false), Socket: os.Getenv("SUB2API_CONTROL_SOCKET"), Token: os.Getenv("SUB2API_CONTROL_TOKEN"), ReleaseDigest: os.Getenv("SUB2API_RELEASE_DIGEST"), BootID: os.Getenv("SUB2API_CORE_BOOT_ID")}
	var err error
	if c.Managed.Enabled {
		if c.Managed.Socket == "" || len(c.Managed.Token) < 32 || c.Managed.ReleaseDigest == "" || c.Managed.BootID == "" {
			return nil, fmt.Errorf("managed core requires control socket, >=32 byte boot token, release digest and core boot ID")
		}
		if c.Managed.UpdaterToken, err = readUpdaterToken(); err != nil {
			return nil, err
		}
		c.Managed.UpdaterSocket = os.Getenv("UPDATER_SOCKET")
	}
	if c.NodeID == "" {
		h, _ := os.Hostname()
		c.NodeID = h
	}
	if c.ShutdownDelay, err = durationEnv("SUB2API_SHUTDOWN_DELAY", 3*time.Second); err != nil {
		return nil, err
	}
	if c.SessionIdleTTL, err = durationEnv("SUB2API_SESSION_IDLE_TTL", 7*24*time.Hour); err != nil {
		return nil, err
	}
	if c.SessionMaxTTL, err = durationEnv("SUB2API_SESSION_MAX_TTL", 30*24*time.Hour); err != nil {
		return nil, err
	}
	if c.SessionIdleTTL <= 0 || c.SessionMaxTTL < c.SessionIdleTTL {
		return nil, fmt.Errorf("SUB2API_SESSION_IDLE_TTL must be positive and at most SUB2API_SESSION_MAX_TTL")
	}
	if c.Environment, err = environmentEnv(); err != nil {
		return nil, err
	}

	if c.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if c.RedisURL == "" {
		return nil, fmt.Errorf("REDIS_URL is required")
	}
	mk := os.Getenv("SUB2API_MASTER_KEY")
	if c.MasterKey, err = base64.StdEncoding.DecodeString(mk); err != nil || len(c.MasterKey) != 32 {
		return nil, fmt.Errorf("SUB2API_MASTER_KEY must be base64 of exactly 32 bytes")
	}

	linux := goos == "linux"
	c.AllowPrivateUpstream = boolEnv("SUB2API_GATEWAY_ALLOW_PRIVATE_UPSTREAM", false)
	for _, p := range strings.Split(os.Getenv("SUB2API_TRUSTED_PROXIES"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			c.TrustedProxies = append(c.TrustedProxies, p)
		}
	}
	// A managed core listens on loopback behind its shell, which drops the
	// client's forwarding headers and sets X-Forwarded-For to the peer it saw:
	// every request comes from the shell, so loopback is the trusted proxy.
	if c.Managed.Enabled {
		c.TrustedProxies = append(c.TrustedProxies, "127.0.0.1", "::1")
	}
	p := &c.Plugins
	p.DataDir = env("SUB2API_PLUGIN_DIR", "/var/lib/sub2api/plugins")
	p.DevMode = boolEnv("SUB2API_PLUGIN_DEV_MODE", false)
	p.AllowUnsigned = boolEnv("SUB2API_PLUGIN_ALLOW_UNSIGNED", false)
	p.VerifySignatures = boolEnv("SUB2API_PLUGIN_VERIFY_SIGNATURES", true)
	p.StrictNetwork = boolEnv("SUB2API_PLUGIN_STRICT_NETWORK", linux)
	p.Seccomp = boolEnv("SUB2API_PLUGIN_SECCOMP", linux)
	p.Landlock = boolEnv("SUB2API_PLUGIN_LANDLOCK", linux)
	p.RunDir = env("SUB2API_PLUGIN_RUN_DIR", filepath.Join(p.DataDir, ".run"))
	p.EgressAllowPrivate = boolEnv("SUB2API_PLUGIN_EGRESS_ALLOW_PRIVATE", false)
	p.loadMarket()
	p.BuiltinDir = env("SUB2API_BUILTIN_PLUGIN_DIR", "/opt/sub2api/builtin")
	if s := os.Getenv("SUB2API_PLUGIN_OFFICIAL_KEYS"); s != "" {
		for _, k := range strings.Split(s, ",") {
			if k = strings.TrimSpace(k); k != "" {
				p.OfficialRootKeys = append(p.OfficialRootKeys, k)
			}
		}
	}
	// The key that signed the built-in packages of this core is always
	// trusted as official: SUB2API_BUILTIN_TRUST_KEY ("keyId=base64pub") when
	// set, otherwise builtin/trust.pub + trust.keyid shipped next to the
	// packages (every image and release bundle carries them, so gateway-managed
	// cores need no extra configuration).
	if k := builtinTrustKey(p.BuiltinDir); k != "" {
		id, _, _ := strings.Cut(k, "=")
		dup := false
		for _, have := range p.OfficialRootKeys {
			if hid, _, _ := strings.Cut(have, "="); hid == id {
				dup = true
			}
		}
		if !dup {
			p.OfficialRootKeys = append(p.OfficialRootKeys, k)
		}
	}
	if p.MaxPackageBytes, err = int64Env("SUB2API_PLUGIN_MAX_PACKAGE_BYTES", rc.PluginPackageMaxBytes); err != nil {
		return nil, err
	}
	mm, err := int64Env("SUB2API_PLUGIN_MAX_MEMORY_MB", 0)
	if err != nil {
		return nil, err
	}
	p.MaxMemoryMB = int(mm)
	if !linux && !p.DevMode {
		// Plugins only run on Linux in production; the rest of the server works.
		p.StrictNetwork, p.Seccomp = false, false
	}
	return c, nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// loadMarket reads the market settings: the store index and whether http
// market URLs are accepted.
func (p *PluginConfig) loadMarket() {
	p.MarketBaseURL = strings.TrimSpace(os.Getenv("SUB2API_MARKET_BASE_URL"))
	p.MarketAllowHTTP = boolEnv("SUB2API_MARKET_ALLOW_HTTP", false)
}

func boolEnv(k string, def bool) bool {
	v := strings.ToLower(os.Getenv(k))
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func durationEnv(k string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(k)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", k, err)
	}
	return d, nil
}

// environmentEnv reads the process layer of the environment variables.
func environmentEnv() (core.EnvironmentValues, error) {
	out := core.EnvironmentValues{}
	for _, v := range core.EnvironmentVariables {
		if v.ProcessEnv == "" {
			continue
		}
		x, err := int64Env(v.ProcessEnv, 0)
		switch {
		case err != nil:
			return nil, err
		case x == 0: // unset
		case x < v.Min || x > v.Max:
			return nil, fmt.Errorf("%s must be between %d and %d", v.ProcessEnv, v.Min, v.Max)
		default:
			out[v.Key] = x
		}
	}
	return out, nil
}

func int64Env(k string, def int64) (int64, error) {
	v := os.Getenv(k)
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", k, err)
	}
	return n, nil
}

// builtinTrustKey returns "keyId=base64pub" for the key that signed the
// built-in plugin packages in dir, or "" when neither the environment nor the
// directory names one.
func builtinTrustKey(dir string) string {
	if k := strings.TrimSpace(os.Getenv("SUB2API_BUILTIN_TRUST_KEY")); k != "" {
		return k
	}
	pub, err := os.ReadFile(filepath.Join(dir, "trust.pub"))
	if err != nil {
		return ""
	}
	key := strings.Join(strings.Fields(string(pub)), "")
	if key == "" {
		return ""
	}
	id := "sub2api-dev"
	if b, err := os.ReadFile(filepath.Join(dir, "trust.keyid")); err == nil {
		if v := strings.Join(strings.Fields(string(b)), ""); v != "" {
			id = v
		}
	}
	return id + "=" + key
}
