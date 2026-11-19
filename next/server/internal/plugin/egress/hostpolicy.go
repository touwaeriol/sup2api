package egress

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/idna"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// Plugin network access (CONTRACTS §113): the administrator's hosts every
// plugin may or may not reach through the tunnel, one global setting on top
// of each plugin's own grant and egress policy.
const (
	// HostSettingsKey is the settings row; config:changed names it as
	// hostConfigKey.
	HostSettingsKey = "plugin_egress"
	hostConfigKey   = "settings." + HostSettingsKey

	// HostAllowAll allows every host but the blocked ones (the default);
	// HostBlockAll blocks every host but the allowed ones.
	HostAllowAll = "allow_all"
	HostBlockAll = "block_all"

	// hostPolicyTTL is how stale the cached setting gets on a node that
	// missed the config:changed broadcast: the refresh interval of the other
	// system settings (gateway settingsTTL, environment globalTTL).
	hostPolicyTTL = 10 * time.Second
)

// HostPolicy is the "plugin_egress" settings row. Both lists are kept
// whatever the mode, so switching modes loses neither.
type HostPolicy struct {
	Mode           string   `json:"mode"`
	BlockedDomains []string `json:"blocked_domains"`
	AllowedDomains []string `json:"allowed_domains"`
}

// DefaultHostPolicy allows every host: what the tunnel did before the
// setting existed.
func DefaultHostPolicy() HostPolicy {
	return HostPolicy{Mode: HostAllowAll, BlockedDomains: []string{}, AllowedDomains: []string{}}
}

// normalized reads an unknown mode as the default and missing lists as empty.
func (h HostPolicy) normalized() HostPolicy {
	if h.Mode != HostBlockAll {
		h.Mode = HostAllowAll
	}
	if h.BlockedDomains == nil {
		h.BlockedDomains = []string{}
	}
	if h.AllowedDomains == nil {
		h.AllowedDomains = []string{}
	}
	return h
}

// refusal is why host (lower-case, without port, as the plugin dialled or
// queried it) may not be reached, "" when it may. A listed domain matches
// itself and its subdomains (manifest.ExternalServiceHost).
func (h HostPolicy) refusal(host string) string {
	if h.Mode == HostBlockAll {
		if !manifest.ExternalServiceHost(h.AllowedDomains, host) {
			return host + " is not on the allowed list of plugin network access"
		}
		return ""
	}
	if manifest.ExternalServiceHost(h.BlockedDomains, host) {
		return host + " is on the blocked list of plugin network access"
	}
	return ""
}

var domainLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// NormalizeDomain turns one entry of a list into its stored form: trimmed,
// lower-case, without a trailing dot, an internationalized name in
// punycode, an IPv6 literal in brackets (the form ExternalServiceHost
// reads). ok is false for anything else: a scheme, path, port, wildcard,
// blank or a name that is not a host name.
func NormalizeDomain(s string) (string, bool) {
	s = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
	if s == "" {
		return "", false
	}
	if addr, err := netip.ParseAddr(strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")); err == nil {
		switch {
		case addr.Zone() != "":
			return "", false
		case addr.Is4():
			return addr.String(), true
		}
		return "[" + addr.String() + "]", true
	}
	ascii, err := idna.Lookup.ToASCII(s)
	if err != nil || len(ascii) > 253 {
		return "", false
	}
	for label := range strings.SplitSeq(ascii, ".") {
		if !domainLabel.MatchString(label) {
			return "", false
		}
	}
	return ascii, true
}

// errHostPolicyUnread: the setting was never read on this node.
var errHostPolicyUnread = errors.New("plugin network access settings could not be read")

// hostPolicies caches the setting on one node. Fresh (read within
// hostPolicyTTL): served as is. Stale: served while one background read
// refreshes it. Never read, or invalidated by a save: callers wait for one
// read. A failed read keeps the last setting read; with none, the tunnel
// refuses (failing open would undo block_all during an outage).
type hostPolicies struct {
	load func(context.Context) (HostPolicy, error)
	log  *slog.Logger

	mu     sync.Mutex
	last   *HostPolicy
	lastAt time.Time // zero once invalidated
	epoch  uint64
	refill singleflight.Group
}

func newHostPolicies(load func(context.Context) (HostPolicy, error), log *slog.Logger) *hostPolicies {
	return &hostPolicies{load: load, log: log}
}

func (c *hostPolicies) invalidate() {
	c.mu.Lock()
	c.lastAt = time.Time{}
	c.epoch++
	c.mu.Unlock()
}

func (c *hostPolicies) get(ctx context.Context) (HostPolicy, error) {
	c.mu.Lock()
	last, at, epoch := c.last, c.lastAt, c.epoch
	c.mu.Unlock()
	key := strconv.FormatUint(epoch, 10)
	if last != nil && !at.IsZero() {
		if time.Since(at) >= hostPolicyTTL {
			c.refill.DoChan(key, func() (any, error) { return c.read(context.WithoutCancel(ctx), epoch) })
		}
		return *last, nil
	}
	v, err, _ := c.refill.Do(key, func() (any, error) { return c.read(context.WithoutCancel(ctx), epoch) })
	if err != nil {
		return HostPolicy{}, err
	}
	return v.(HostPolicy), nil
}

// read loads the setting and caches it unless a save invalidated the cache
// meanwhile. After a failure (logged) it returns the last setting read, if
// any, or errHostPolicyUnread.
func (c *hostPolicies) read(ctx context.Context, epoch uint64) (HostPolicy, error) {
	h, err := c.load(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.log.WarnContext(ctx, "egress: read plugin network access settings", "err", err, "have_last", c.last != nil)
		if c.last == nil {
			return HostPolicy{}, errHostPolicyUnread
		}
		// Kept for another interval, so a failing database is not read on
		// every connection.
		if c.epoch == epoch {
			c.lastAt = time.Now()
		}
		return *c.last, nil
	}
	h = h.normalized()
	if c.epoch == epoch {
		c.last, c.lastAt = &h, time.Now()
	}
	return h, nil
}

// loadHostPolicy reads the settings row; no row is the default.
func loadHostPolicy(ctx context.Context, q *gorm.DB) (HostPolicy, error) {
	h := DefaultHostPolicy()
	raw, err := store.SettingValue(store.G(ctx, q), HostSettingsKey)
	if store.IsNoRows(err) {
		return h, nil
	}
	if err != nil {
		return h, err
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		return h, err
	}
	return h.normalized(), nil
}
