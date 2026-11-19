package gateway

import (
	"context"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// SettingsReaders reads the settings sections the gateway owns as plugins
// see them (CONTRACTS §107): each row read now and normalised exactly as
// settingsCache.load does, so a plugin notified of a change never reads the
// value the cache held before it. It only needs the database, so it is
// built before the plugin runtime and the gateway.
func SettingsReaders(db *store.DB) map[string]core.SettingsReader {
	read := func(load func(context.Context, *gorm.DB) (protocol.SettingsValues, error)) core.SettingsReader {
		return func(ctx context.Context) (protocol.SettingsValues, error) { return load(ctx, db.Gorm) }
	}
	return map[string]core.SettingsReader{
		settingsKeyGateway:     read(pluginGatewaySettings),
		settingsKeyScheduling:  read(pluginSchedulingSettings),
		settingsKeySticky:      read(pluginStickySettings),
		settingsKeyAutoDisable: read(pluginAutoDisableSettings),
		failurePolicyKey:       read(pluginFailurePolicy),
	}
}

func pluginGatewaySettings(ctx context.Context, q *gorm.DB) (protocol.SettingsValues, error) {
	v, err := loadGatewaySettings(ctx, q)
	if err != nil {
		return nil, err
	}
	v = v.normalized()
	return protocol.GatewaySettings{
		MaxAttempts: v.MaxAttempts, PlatformCallTimeoutMs: v.PlatformCallTimeoutMs,
		DefaultHookTimeoutMs: v.DefaultHookTimeoutMs, PlatformHotpathTimeoutMs: v.PlatformHotpathTimeoutMs,
		QueueStickyMaxWaiting: v.QueueStickyMaxWaiting, QueueStickyWaitSeconds: v.QueueStickyWaitSeconds,
		QueueFallbackMaxWaiting: v.QueueFallbackMaxWaiting, QueueFallbackWaitSeconds: v.QueueFallbackWaitSecond,
		MediaDownloadMaxMB: v.MediaDownloadMaxMB, MaxRequestBodyMB: v.MaxRequestBodyMB,
		ClaudeMaxTokens: protocol.ClaudeMaxTokens{Enabled: v.ClaudeMaxTokens.Enabled, Models: v.ClaudeMaxTokens.Models},
	}, nil
}

func pluginSchedulingSettings(ctx context.Context, q *gorm.DB) (protocol.SettingsValues, error) {
	v, err := loadSchedulingSettings(ctx, q)
	if err != nil {
		return nil, err
	}
	return protocol.SchedulingSettings{Metric: v.Metric, LimitLoad: v.LimitLoad, ModelLimits: v.ModelLimits}, nil
}

func pluginStickySettings(ctx context.Context, q *gorm.DB) (protocol.SettingsValues, error) {
	v, err := loadStickySettings(ctx, q)
	if err != nil {
		return nil, err
	}
	return protocol.StickySettings{Enabled: v.Enabled, DefaultTTLSeconds: v.DefaultTTLSeconds, KeepOnAccountDisabled: v.KeepOnAccountDisabled}, nil
}

func pluginAutoDisableSettings(ctx context.Context, q *gorm.DB) (protocol.SettingsValues, error) {
	v, err := loadAutoDisableSettings(ctx, q)
	if err != nil {
		return nil, err
	}
	return protocol.AutoDisableSettings{Enabled: v.Enabled, StatusCodes: v.StatusCodes, Keywords: v.Keywords}, nil
}

// pluginFailurePolicy hands out the switches of the published policy, not its
// rules: the core alone evaluates them.
func pluginFailurePolicy(ctx context.Context, q *gorm.DB) (protocol.SettingsValues, error) {
	p, err := loadFailurePolicy(ctx, q)
	if err != nil || p == nil {
		return protocol.FailurePolicySettings{}, err
	}
	return protocol.FailurePolicySettings{
		Configured: true, Revision: p.Revision, SchemaVersion: p.SchemaVersion, RetryEnabled: p.RetryEnabled, MaxRetries: p.MaxRetries,
		RetriesPerAccount: p.RetriesPerAccount, SwitchAccount: p.SwitchAccount, ResetSticky: p.ResetSticky,
		BackoffMs: p.BackoffMS, MaxWaitMs: p.MaxWaitMS, RespectRetryAfter: p.RespectRetryAfter, AccountEnabled: p.AccountEnabled,
	}, nil
}
