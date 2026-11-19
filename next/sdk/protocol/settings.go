package protocol

// System settings as plugins read them (CONTRACTS §107). The core answers
// HostService.GetSettings with one of these per section, filled by the
// section's owner from the same read and normalisation its own caches use,
// and announces changes with PluginService.SettingsChanged on every node
// and with the settings.changed event. The types are the contract: the
// core's own structures stay internal and may change without notice.

// Settings sections: the groups of the console's system settings.
const (
	SettingsBilling       = "billing"
	SettingsGateway       = "gateway"
	SettingsScheduling    = "scheduling"
	SettingsSticky        = "sticky"
	SettingsAutoDisable   = "auto_disable"
	SettingsFailurePolicy = "failure_policy"
	SettingsEnvironment   = "environment"
)

// SettingsSections lists every section, in display order.
var SettingsSections = []string{SettingsBilling, SettingsGateway, SettingsScheduling, SettingsSticky,
	SettingsAutoDisable, SettingsFailurePolicy, SettingsEnvironment}

// EventSettingsChanged is the outbox event written in the transaction that
// saves a section; its payload is a SettingsChange.
const EventSettingsChanged = "settings.changed"

// SettingsChange is the payload of EventSettingsChanged and the sections of
// PluginService.SettingsChanged.
type SettingsChange struct {
	Sections []string `json:"sections"`
}

// SettingsValues is implemented by the type of each section.
type SettingsValues interface {
	SettingsSection() string
}

// BillingSettings is the "billing" section.
type BillingSettings struct {
	// MinBalance is the balance (USD, decimal string) a request needs.
	MinBalance string `json:"min_balance"`
	// BigCostWarningUSD (decimal string) flags expensive price rules.
	BigCostWarningUSD string `json:"big_cost_warning_usd"`
	// PreConsumeTokens is the least a request pre-charges, in tokens.
	PreConsumeTokens int64 `json:"pre_consume_tokens"`
	// OutputReserveTokens is pre-charged as output when a request names no
	// max_tokens; 0 reserves none.
	OutputReserveTokens int64 `json:"output_reserve_tokens"`
	// ClientDisconnectPolicy: "continue", "precharge" or "free"
	// (CONTRACTS §25.8).
	ClientDisconnectPolicy string `json:"client_disconnect_policy"`
}

// GatewaySettings is the "gateway" section (CONTRACTS §14.4).
type GatewaySettings struct {
	// MaxAttempts bounds the attempts of one request while no failure policy
	// is published (FailurePolicySettings.Configured false).
	MaxAttempts              int `json:"max_attempts"`
	PlatformCallTimeoutMs    int `json:"platform_call_timeout_ms"`
	DefaultHookTimeoutMs     int `json:"default_hook_timeout_ms"`
	PlatformHotpathTimeoutMs int `json:"platform_hotpath_timeout_ms"`
	// The account queues; 0 turns one off.
	QueueStickyMaxWaiting    int `json:"queue_sticky_max_waiting"`
	QueueStickyWaitSeconds   int `json:"queue_sticky_wait_seconds"`
	QueueFallbackMaxWaiting  int `json:"queue_fallback_max_waiting"`
	QueueFallbackWaitSeconds int `json:"queue_fallback_wait_seconds"`
	MediaDownloadMaxMB       int `json:"media_download_max_mb"`
	// MaxRequestBodyMB is the largest client request body (and WebSocket
	// message) the gateway accepts; no other layer sets one.
	MaxRequestBodyMB int `json:"max_request_body_mb"`
	// ClaudeMaxTokens is the built-in max_tokens override of the attempts
	// sent to Anthropic (CONTRACTS §84.1).
	ClaudeMaxTokens ClaudeMaxTokens `json:"claude_max_tokens"`
}

// ClaudeMaxTokens is the max_tokens of each model ("default" for the rest),
// applied only when Enabled.
type ClaudeMaxTokens struct {
	Enabled bool           `json:"enabled"`
	Models  map[string]int `json:"models"`
}

// SchedulingSettings is the "scheduling" section: the load balancing of new
// sessions inside a tier (CONTRACTS §89).
type SchedulingSettings struct {
	// Metric: "sessions", "rpm", "tpm" or "concurrency".
	Metric      string `json:"metric"`
	LimitLoad   bool   `json:"limit_load"`
	ModelLimits bool   `json:"model_limits"`
}

// StickySettings is the "sticky" section (CONTRACTS §15.5).
type StickySettings struct {
	// Enabled false: the core neither binds sessions nor reads bindings.
	Enabled               bool `json:"enabled"`
	DefaultTTLSeconds     int  `json:"default_ttl_seconds"`
	KeepOnAccountDisabled bool `json:"keep_on_account_disabled"`
}

// AutoDisableSettings is the "auto_disable" section (CONTRACTS §42.2). While
// a failure policy is published its account rules decide instead, and
// FailurePolicySettings.AccountEnabled replaces Enabled.
type AutoDisableSettings struct {
	Enabled     bool     `json:"enabled"`
	StatusCodes string   `json:"status_codes"`
	Keywords    []string `json:"keywords"`
}

// FailurePolicySettings is the "failure_policy" section: the switches of the
// published failure policy (CONTRACTS §69). Its rules stay with the core,
// which alone evaluates them. Every field but Configured is zero while no
// policy is published.
type FailurePolicySettings struct {
	Configured bool  `json:"configured"`
	Revision   int64 `json:"revision"`
	// SchemaVersion (1-4) decides which switches apply. At 1 every retry
	// rule carries its own same-account retries and what follows them, so
	// RetriesPerAccount, SwitchAccount and ResetSticky do not apply; from 2
	// they apply to every rule (RetriesPerAccount only with SwitchAccount,
	// else MaxRetries are all on one account); from 3 MaxWaitMs and
	// RespectRetryAfter do not apply.
	SchemaVersion     int  `json:"schema_version"`
	RetryEnabled      bool `json:"retry_enabled"`
	MaxRetries        int  `json:"max_retries"`
	RetriesPerAccount int  `json:"retries_per_account"`
	SwitchAccount     bool `json:"switch_account"`
	ResetSticky       bool `json:"reset_sticky"`
	BackoffMs         int  `json:"backoff_ms"`
	MaxWaitMs         int  `json:"max_wait_ms"`
	RespectRetryAfter bool `json:"respect_retry_after"`
	// AccountEnabled: whether the core may disable or cool down accounts.
	AccountEnabled bool `json:"account_enabled"`
}

// EnvironmentSettings is the "environment" section: the global layer of the
// environment variables over the process layer (CONTRACTS §74.3). The
// values of a group or an account come from HostService.GetEnvironment.
type EnvironmentSettings struct {
	Values map[string]int64 `json:"values"`
}

func (BillingSettings) SettingsSection() string       { return SettingsBilling }
func (GatewaySettings) SettingsSection() string       { return SettingsGateway }
func (SchedulingSettings) SettingsSection() string    { return SettingsScheduling }
func (StickySettings) SettingsSection() string        { return SettingsSticky }
func (AutoDisableSettings) SettingsSection() string   { return SettingsAutoDisable }
func (FailurePolicySettings) SettingsSection() string { return SettingsFailurePolicy }
func (EnvironmentSettings) SettingsSection() string   { return SettingsEnvironment }
