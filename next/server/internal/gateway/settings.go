package gateway

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

const (
	settingsKeyGateway = protocol.SettingsGateway
	settingsKeySticky  = protocol.SettingsSticky

	settingsTTL = 10 * time.Second

	// maxHookTimeout caps any hook call, including a manifest timeoutMs
	// (CONTRACTS §20.1). The admin default for hooks without timeoutMs has
	// its own, lower ceiling (maxDefaultHookTimeoutMs).
	maxHookTimeout = 30 * time.Second
)

// GatewaySettings is the "gateway" row of the settings table (CONTRACTS §8).
type GatewaySettings struct {
	MaxAttempts           int `json:"max_attempts"`
	PlatformCallTimeoutMs int `json:"platform_call_timeout_ms"`
	DefaultHookTimeoutMs  int `json:"default_hook_timeout_ms"`
	// PlatformHotpathTimeoutMs bounds the PlatformService calls that are part
	// of serving one gateway request rather than of a hook:
	// ResolveModel and EstimateUsage (before scheduling).
	//
	// It used to be default_hook_timeout_ms, which was wrong in both
	// directions: an administrator widening the hook default for one slow
	// hook also widened a hot-path RPC that has nothing to do with it, and
	// tightening the hook default tightened that RPC too. The two have no
	// reason to move together.
	PlatformHotpathTimeoutMs int `json:"platform_hotpath_timeout_ms"`
	// A request finding its account full queues for it
	// (docs/GATEWAY-CONCURRENCY-DESIGN.md §3). Sticky: its session's bound
	// account. Fallback: the least loaded candidate when all are full. 0 for
	// either number turns that queue off.
	QueueStickyMaxWaiting   int `json:"queue_sticky_max_waiting"`
	QueueStickyWaitSeconds  int `json:"queue_sticky_wait_seconds"`
	QueueFallbackMaxWaiting int `json:"queue_fallback_max_waiting"`
	QueueFallbackWaitSecond int `json:"queue_fallback_wait_seconds"`
	// ClaudeMaxTokens is the built-in max_tokens override of the attempts
	// sent to Anthropic, off by default (claude_max_tokens.go). It replaces
	// claude_default_max_tokens, which a row may still hold and is ignored.
	ClaudeMaxTokens ClaudeMaxTokensRule `json:"claude_max_tokens"`
	// MediaDownloadMaxMB caps one media file the core downloads to inline it
	// into a request converted to Gemini (CONVERT-MATRIX §9.2).
	MediaDownloadMaxMB int `json:"media_download_max_mb"`
	// MaxRequestBodyMB is the one limit on a client request: the body of an
	// HTTP request and one WebSocket message. No endpoint, group or process
	// environment variable sets another, and it bounds nothing between the
	// core and a plugin (MaxRequestBodyBytes).
	MaxRequestBodyMB int `json:"max_request_body_mb"`
}

// StickySettings is the "sticky" row of the settings table.
type StickySettings struct {
	Enabled               bool `json:"enabled"`
	DefaultTTLSeconds     int  `json:"default_ttl_seconds"`
	KeepOnAccountDisabled bool `json:"keep_on_account_disabled"`
}

func defaultGatewaySettings() GatewaySettings {
	return GatewaySettings{MaxAttempts: 3, PlatformCallTimeoutMs: 2000, DefaultHookTimeoutMs: 300,
		PlatformHotpathTimeoutMs: 300, QueueStickyMaxWaiting: 8, QueueStickyWaitSeconds: 120,
		QueueFallbackMaxWaiting: 100, QueueFallbackWaitSecond: 30, MediaDownloadMaxMB: 64,
		MaxRequestBodyMB: 128}
}

// Accepted ranges of the gateway settings (CONTRACTS §14.4).
const (
	minMaxAttempts           = 1
	maxMaxAttempts           = 10
	minPlatformCallTimeoutMs = 100
	maxPlatformCallTimeoutMs = 30000
	minDefaultHookTimeoutMs  = 50
	maxDefaultHookTimeoutMs  = 2000
	// The hot-path RPCs share the hook default's range: one of them delays
	// every request to its endpoint, the other holds a usage record open, and
	// neither is a place to wait seconds.
	minPlatformHotpathTimeoutMs = 50
	maxPlatformHotpathTimeoutMs = 2000
	maxQueueWaiting             = 10000
	maxQueueWaitSeconds         = 600
	minMediaDownloadMaxMB       = 1
	maxMediaDownloadMaxMB       = 1024
	minMaxRequestBodyMB         = 1
	maxMaxRequestBodyMB         = 1024
	// claude_max_tokens: the fallback key, its suggested value, and the caps
	// on the models (one override operation each), a name and a value.
	claudeDefaultKey         = "default"
	defaultClaudeMaxTokens   = 8192
	maxClaudeMaxTokensModels = core.MaxParamOverrideOperations
	maxClaudeMaxTokensName   = 200
	maxClaudeMaxTokens       = 1 << 20
)

func defaultStickySettings() StickySettings {
	return StickySettings{Enabled: true, DefaultTTLSeconds: 3600}
}

// normalized fills unset (<= 0) fields with the defaults and clamps the
// others into the accepted ranges, so a stored row written before the
// validation existed still yields usable values.
func (s GatewaySettings) normalized() GatewaySettings {
	d := defaultGatewaySettings()
	clamp := func(v, def, lo, hi int) int {
		switch {
		case v <= 0:
			return def
		case v < lo:
			return lo
		case v > hi:
			return hi
		}
		return v
	}
	s.MaxAttempts = clamp(s.MaxAttempts, d.MaxAttempts, minMaxAttempts, maxMaxAttempts)
	s.PlatformCallTimeoutMs = clamp(s.PlatformCallTimeoutMs, d.PlatformCallTimeoutMs, minPlatformCallTimeoutMs, maxPlatformCallTimeoutMs)
	s.DefaultHookTimeoutMs = clamp(s.DefaultHookTimeoutMs, d.DefaultHookTimeoutMs, minDefaultHookTimeoutMs, maxDefaultHookTimeoutMs)
	s.PlatformHotpathTimeoutMs = clamp(s.PlatformHotpathTimeoutMs, d.PlatformHotpathTimeoutMs,
		minPlatformHotpathTimeoutMs, maxPlatformHotpathTimeoutMs)
	// Unlike the fields above, 0 means something here (off): a row written
	// before these fields existed reads them as their defaults because the
	// row is decoded over defaultGatewaySettings.
	bound := func(v, hi int) int { return min(max(v, 0), hi) }
	s.QueueStickyMaxWaiting = bound(s.QueueStickyMaxWaiting, maxQueueWaiting)
	s.QueueStickyWaitSeconds = bound(s.QueueStickyWaitSeconds, maxQueueWaitSeconds)
	s.QueueFallbackMaxWaiting = bound(s.QueueFallbackMaxWaiting, maxQueueWaiting)
	s.QueueFallbackWaitSecond = bound(s.QueueFallbackWaitSecond, maxQueueWaitSeconds)
	s.MediaDownloadMaxMB = clamp(s.MediaDownloadMaxMB, d.MediaDownloadMaxMB, minMediaDownloadMaxMB, maxMediaDownloadMaxMB)
	s.MaxRequestBodyMB = clamp(s.MaxRequestBodyMB, d.MaxRequestBodyMB, minMaxRequestBodyMB, maxMaxRequestBodyMB)
	s.ClaudeMaxTokens = s.ClaudeMaxTokens.normalized()
	return s
}

// mediaDownloadMaxBytes caps one media download (CONVERT-MATRIX §9.2).
func (s GatewaySettings) mediaDownloadMaxBytes() int64 {
	mb := s.MediaDownloadMaxMB
	if mb <= 0 {
		mb = defaultGatewaySettings().MediaDownloadMaxMB
	}
	return int64(mb) << 20
}

// MaxRequestBodyBytes is the request body limit in bytes: the largest client
// request body and WebSocket message the gateway reads. An unnormalised
// setting reads as the default.
func (s GatewaySettings) MaxRequestBodyBytes() int64 {
	mb := s.MaxRequestBodyMB
	if mb <= 0 {
		mb = defaultGatewaySettings().MaxRequestBodyMB
	}
	return int64(mb) << 20
}

// platformTimeout bounds one PlatformService.ClassifyError call (an upstream
// failure of a relay that did not wait) and the lookup of the client
// disconnect policy. It does not time Execute: nothing does.
func (s GatewaySettings) platformTimeout() time.Duration {
	return time.Duration(s.PlatformCallTimeoutMs) * time.Millisecond
}

// hotpathTimeout bounds one PlatformService.ResolveModel or
// PlatformService.EstimateUsage call: the RPCs before scheduling that belong
// to serving one gateway request but are not hooks. A zero or unnormalised setting falls back
// to the default instead of "no deadline".
func (s GatewaySettings) hotpathTimeout() time.Duration {
	ms := s.PlatformHotpathTimeoutMs
	if ms <= 0 {
		ms = defaultGatewaySettings().PlatformHotpathTimeoutMs
	}
	return time.Duration(ms) * time.Millisecond
}

func (s GatewaySettings) queueStickyWait() time.Duration {
	return time.Duration(s.QueueStickyWaitSeconds) * time.Second
}

func (s GatewaySettings) queueFallbackWait() time.Duration {
	return time.Duration(s.QueueFallbackWaitSecond) * time.Second
}

type settingsSnapshot struct {
	at               time.Time
	gateway          GatewaySettings
	sticky           StickySettings
	autoDisable      AutoDisableSettings
	scheduling       SchedulingSettings
	failurePolicy    *FailurePolicy
	failurePolicyErr error
}

// settingsCache reads the gateway, sticky, auto-disable and scheduling rows
// with a short TTL; the config:changed broadcast invalidates it immediately.
type settingsCache struct {
	db    *store.DB
	mu    sync.Mutex
	snap  *settingsSnapshot
	epoch uint64
	// override replaces the DB in tests.
	override *settingsSnapshot
}

func newSettingsCache(db *store.DB) *settingsCache { return &settingsCache{db: db} }

func (c *settingsCache) invalidate() {
	c.mu.Lock()
	c.epoch++
	c.snap = nil
	c.mu.Unlock()
}

func (c *settingsCache) get(ctx context.Context) settingsSnapshot {
	c.mu.Lock()
	if c.override != nil {
		o := *c.override
		c.mu.Unlock()
		o.gateway, o.scheduling = o.gateway.normalized(), o.scheduling.normalized()
		return o
	}
	snap := c.snap
	epoch := c.epoch
	c.mu.Unlock()
	if snap != nil && time.Since(snap.at) < settingsTTL {
		return *snap
	}
	s := c.load(ctx)
	c.mu.Lock()
	if c.epoch == epoch && s.at != (time.Time{}) {
		c.snap = &s
	}
	c.mu.Unlock()
	return s
}

// load reads the rows. On read errors it keeps the defaults of the rows
// not read and leaves at zero so the result is not cached.
func (c *settingsCache) load(ctx context.Context) settingsSnapshot {
	s := settingsSnapshot{gateway: defaultGatewaySettings(), sticky: defaultStickySettings(),
		autoDisable: defaultAutoDisableSettings(), scheduling: defaultSchedulingSettings()}
	if c.db == nil {
		s.at = time.Now()
		return s
	}
	s.failurePolicy, s.failurePolicyErr = loadFailurePolicy(ctx, c.db.Gorm)
	if s.failurePolicyErr != nil {
		return s
	}
	gw, err := loadGatewaySettings(ctx, c.db.Gorm)
	if err != nil {
		return s
	}
	s.gateway = gw.normalized()
	st, err := loadStickySettings(ctx, c.db.Gorm)
	if err != nil {
		return s
	}
	s.sticky = st
	ad, err := loadAutoDisableSettings(ctx, c.db.Gorm)
	if err != nil {
		return s
	}
	s.autoDisable = ad
	sc, err := loadSchedulingSettings(ctx, c.db.Gorm)
	if err != nil {
		return s
	}
	s.scheduling, s.at = sc, time.Now()
	return s
}

// settingValue reads the document of settings row key, nil when there is no
// row (the column is NOT NULL): one row read through Row. The settings
// cache reloads it once per settingsTTL, in the request that finds the
// cache stale.
func settingValue(ctx context.Context, q *gorm.DB, key string) ([]byte, error) {
	var raw []byte
	err := store.G(ctx, q).Model(&model.Setting{}).Select("value").Where("key = ?", key).Row().Scan(&raw)
	if store.IsNoRows(err) {
		return nil, nil
	}
	return raw, err
}

func loadSettingsRow(ctx context.Context, q *gorm.DB, key string, into any) error {
	raw, err := settingValue(ctx, q, key)
	if err != nil || raw == nil {
		return err
	}
	// Fields absent from the stored document keep the defaults in into.
	return json.Unmarshal(raw, into)
}

func loadGatewaySettings(ctx context.Context, q *gorm.DB) (GatewaySettings, error) {
	v := defaultGatewaySettings()
	err := loadSettingsRow(ctx, q, settingsKeyGateway, &v)
	return v, err
}

func loadStickySettings(ctx context.Context, q *gorm.DB) (StickySettings, error) {
	v := defaultStickySettings()
	err := loadSettingsRow(ctx, q, settingsKeySticky, &v)
	if v.DefaultTTLSeconds <= 0 {
		v.DefaultTTLSeconds = defaultStickySettings().DefaultTTLSeconds
	}
	return v, err
}
