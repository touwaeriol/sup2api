// Package manifest defines manifest.json, the static description of a plugin
// package. It is the single source of truth shared by the host (validation,
// registry, consent screen) and plugin tooling (pack, lint).
package manifest

import (
	"encoding/json"
	"net/mail"
	"slices"
	"strings"
	"time"
)

// APIVersion is the only manifest apiVersion this SDK understands.
const APIVersion = 1

// LocalizedText is either a bare string (treated as {"en": s}) or a map of
// locale -> text that must contain "en". Normalize with Text.Normalize.
type LocalizedText map[string]string

// Manifest is the root of manifest.json.
type Manifest struct {
	APIVersion   int           `json:"apiVersion"`
	Key          string        `json:"key"` // ^[a-z](_?[a-z0-9])+$, at most 30 bytes
	Name         LocalizedText `json:"name"`
	Description  LocalizedText `json:"description,omitempty"`
	Version      string        `json:"version"` // semver
	Publisher    string        `json:"publisher"`
	Icon         string        `json:"icon,omitempty"` // path inside the package or "text:<label>"
	Runtime      string        `json:"runtime"`        // RuntimeGRPC ("js" reserved)
	Entry        Entry         `json:"entry"`
	HostCompat   string        `json:"hostCompat"`             // semver range, e.g. ">=0.1.0 <0.2.0"
	HostUICompat string        `json:"hostUICompat,omitempty"` // required when ui.native is set

	Capabilities []Capability `json:"capabilities"`

	Platforms    []Platform    `json:"platforms,omitempty"` // new platforms with their endpoints
	AccountTypes []AccountType `json:"accountTypes,omitempty"`
	Hooks        []Hook        `json:"hooks,omitempty"`
	// Scheduler declares how the plugin takes part in gateway scheduling.
	Scheduler    *Scheduler    `json:"scheduler,omitempty"`
	Observations *Observations `json:"observations,omitempty"`
	Events       *Events       `json:"events,omitempty"`
	Jobs         []Job         `json:"jobs,omitempty"`
	Database     *Database     `json:"database,omitempty"`

	// Notifications declares the notification channels the plugin delivers.
	Notifications *Notifications `json:"notifications,omitempty"`
	// Payments declares the payment gateways the plugin provides.
	Payments *Payments `json:"payments,omitempty"`

	// PermissionGroups are the groups userPermissions[].group names; the role
	// editor shows plugin, group, permission in this order (UI 3.16).
	PermissionGroups []PermissionGroup `json:"permissionGroups,omitempty"`
	UserPermissions  []UserPermission  `json:"userPermissions,omitempty"`
	Routes           []Route           `json:"routes,omitempty"`
	UI               *UI               `json:"ui,omitempty"`
	Resources        *Resources        `json:"resources,omitempty"`

	HostPermissions  []HostPermission `json:"hostPermissions,omitempty"`
	ExternalServices []string         `json:"externalServices,omitempty"` // shown at review; with the account's upstream addresses, the hosts the core sends to for the plugin (UpstreamHostAllowed, CONTRACTS §112.17)
}

// Entry locates the runtime artifact inside the package.
type Entry struct {
	GRPC *GRPCEntry `json:"grpc,omitempty"`
	JS   *JSEntry   `json:"js,omitempty"` // reserved
}

type GRPCEntry struct {
	// Path template with {os} and {arch}, e.g. "runtimes/{os}-{arch}/plugin".
	Binaries string `json:"binaries"`
}

type JSEntry struct {
	Source string `json:"source"`
}

// RuntimeGRPC is the only value of Manifest.Runtime: a plugin process
// speaking the gRPC plugin protocol. Every plugin has a process.
const RuntimeGRPC = "grpc"

// Capability ids understood by host version 0.1.
const (
	CapPlatformAdapter   = "platform.adapter.v1"
	CapPlatformTasks     = "platform.tasks.v1"
	CapPlatformMonitor   = "platform.monitor.v1"
	CapPlatformWebSocket = "platform.websocket.v1" // upstream requests for websocket endpoints
	CapGatewayHook       = "gateway.hook.v1"
	CapGatewayObserve    = "gateway.observe.v1"
	CapAppJobs           = "app.jobs.v1"
	CapAppEvents         = "app.events.v1"
	CapHTTPRoutes        = "http.routes.v1"
	CapMigrationData     = "migration.data.v1"
	CapSchedulerRank     = "scheduler.rank.v1"
	CapAppBroadcast      = "app.broadcast.v1"
	CapNotifyChannel     = "notify.channel.v1"
	CapPaymentGateway    = "payment.gateway.v1" // PaymentGatewayService for the gateways in payments
)

type Capability struct {
	ID string `json:"id"`
}

type Observations struct {
	Events  []string `json:"events"`
	Metrics []string `json:"metrics,omitempty"`
}

// Notifications declares the channels a plugin delivers notifications on
// ("notify.channel.v1", CONTRACTS §74.4). A channel id is unique across the
// enabled plugins: other plugins send through HostService.Notify by the id
// and never talk to the provider themselves.
type Notifications struct {
	Channels []NotificationChannel `json:"channels"`
}

// Recipient formats of a notification channel.
const (
	RecipientEmail = "email"
	RecipientText  = "text" // anything the provider understands, e.g. a chat id
)

type NotificationChannel struct {
	ID          string        `json:"id"` // ^[a-z][a-z0-9_]{1,29}$, e.g. "email"
	Label       LocalizedText `json:"label"`
	Description LocalizedText `json:"description,omitempty"`
	// Recipient is the format of a recipient: RecipientEmail, or
	// RecipientText (the default).
	Recipient string `json:"recipient,omitempty"`
}

// RecipientFormat is the channel's recipient format, RecipientText when
// unset.
func (c NotificationChannel) RecipientFormat() string {
	if c.Recipient == "" {
		return RecipientText
	}
	return c.Recipient
}

// ValidRecipient reports whether r is a bare address when format is
// RecipientEmail; any other format accepts every recipient.
func ValidRecipient(format, r string) bool {
	if format != RecipientEmail {
		return true
	}
	a, err := mail.ParseAddress(r)
	return err == nil && a.Address == r
}

// ---------------------------------------------------------------- platforms and endpoints

// Built-in platform ids provided by the core (ARCHITECTURE 6.6). Plugins
// cannot declare platforms with these ids.
const (
	PlatformAnthropic = "anthropic"
	PlatformOpenAI    = "openai"
	PlatformGemini    = "gemini"
)

// Platform groups client-facing gateway endpoints. The core defines the
// built-in platforms; a plugin may declare new ones (unique id, endpoints
// must not conflict with any other platform). Account types serve platforms.
type Platform struct {
	ID        string        `json:"id"` // ^[a-z][a-z0-9_]{1,29}$
	Label     LocalizedText `json:"label,omitempty"`
	Endpoints []Endpoint    `json:"endpoints"`
	// Defaults for every endpoint of the platform; an endpoint (Usage) or an
	// account type (AccountPlatform) may override them.
	// Client headers the host forwards to PlatformService.Execute (lower-case).
	PassHeaders []string   `json:"passHeaders,omitempty"`
	Usage       UsageRules `json:"usage"`
	// StickyRules are the core's default sticky rules of a built-in platform
	// (sdk/platforms). A plugin manifest may not declare any: plugins take no
	// part in sticky sessions (CONTRACTS §5.6).
	StickyRules []StickyRule `json:"stickyRules,omitempty"`
}

// Protocols returns the distinct protocols of the platform's endpoints.
func (p *Platform) Protocols() []string {
	var out []string
	seen := map[string]bool{}
	for _, e := range p.Endpoints {
		if !seen[e.Protocol] {
			seen[e.Protocol] = true
			out = append(out, e.Protocol)
		}
	}
	return out
}

type Endpoint struct {
	ID     string `json:"id"`
	Method string `json:"method"` // GET/POST/PUT/PATCH/DELETE
	// Path in gin-like syntax: "/v1/messages", "/v1beta/models/:model:generateContent"
	// (a segment may be ":param" or ":param:suffix" for a literal suffix).
	Path string `json:"path"`
	// Protocol id, "<platform id>.<name>", e.g. "anthropic.messages".
	Protocol    string          `json:"protocol"`
	Kind        string          `json:"kind"` // EndpointKindProxy or EndpointKindWebSocket ("custom" reserved)
	Auth        EndpointAuth    `json:"auth"`
	Request     EndpointRequest `json:"request"`
	Response    EndpointResp    `json:"response"`
	ErrorFormat string          `json:"errorFormat"` // anthropic | openai | gemini | plain
	Billing     string          `json:"billing"`     // usage | free
	// BillingTypes restricts model prices accepted at request admission.
	// Every metered endpoint must declare its supported types explicitly.
	BillingTypes []string `json:"billingTypes,omitempty"` // per_request | per_token | expression | video
	// Task opts into durable host-managed async work. Submission is recorded
	// before a successful response; queries read the shared, owner-checked
	// snapshot. Polling and account affinity belong to the host.
	Task *AsyncTaskEndpoint `json:"task,omitempty"`
	// Usage overrides the platform usage rules for this endpoint.
	Usage *UsageRules `json:"usage,omitempty"`
	// UsageSource decides WHO reads the token usage out of this endpoint's
	// upstream responses: UsageSourceRules (the default, and what "" means)
	// applies the declarative sse/json maps of the usage rules in effect,
	// UsageSourcePlugin has the plugin declaring the endpoint's platform read
	// it from the observation of a waited relay and return it from
	// PlatformService.Execute.
	//
	// It sits on the endpoint, beside Request.ModelSource, and deliberately
	// NOT on UsageRules. The rules follow the override chain of CONTRACTS
	// §13, whose last link - AccountPlatform.usage[protocol] - belongs to a
	// THIRD plugin and replaces the whole block. A source living there could
	// be redirected by that plugin, and, worse, an override that simply does
	// not mention it would switch the endpoint back to the declarative rules
	// without saying anything. On the endpoint there is exactly one reading
	// of "who reads the usage", and no ban is needed to keep it.
	UsageSource string `json:"usageSource,omitempty"`
	// UsageStreamEvents are the SSE event names the host collects into the
	// observation of a streaming response - and the only ones. The whole
	// stream is never buffered. Only read with UsageSource
	// UsageSourcePlugin.
	UsageStreamEvents []string `json:"usageStreamEvents,omitempty"`
	// UsageRequestFields are request body paths (gjson) whose values the host
	// hands to PlatformService.EstimateUsage before an account is chosen, and
	// repeats in the observation (ExtractUsageRequest.fields) - and the only
	// ones. They are for endpoints that only START work: the response is
	// {"id": ...} and the request is where the resolution and the duration
	// of the job are, which is what a pre-charge estimate has to be made
	// from. Values travel whole, of any size. Only read with UsageSource
	// UsageSourcePlugin or TaskSubmit().
	UsageRequestFields []string `json:"usageRequestFields,omitempty"`
	// Test says when the console's account test may use this endpoint's
	// protocol (CONTRACTS §122): "" whenever the account serves it,
	// EndpointTestExplicit only when the test names a model,
	// EndpointTestNever not at all.
	Test string `json:"test,omitempty"`
}

// Values of Endpoint.Test.
const (
	// EndpointTestExplicit is a protocol whose test costs real money (an
	// image generation): tested only when the test request names the model
	// (the console's model tests, one, selected or all, each name theirs),
	// never by a test of the account's default model.
	EndpointTestExplicit = "explicit"
	// EndpointTestNever is a protocol a test cannot exercise on its own (an
	// image edit needs an input image).
	EndpointTestNever = "never"
)

// PluginUsage reports whether this endpoint's usage is read by the plugin
// declaring its platform (ExecuteRequest.extract_usage) instead of by the
// declarative usage rules.
func (e Endpoint) PluginUsage() bool { return e.UsageSource == UsageSourcePlugin }

const (
	TaskActionSubmit = "submit"
	TaskActionQuery  = "query"
)

// AsyncTaskEndpoint pairs one JSON submission and query endpoint of a task
// kind within a plugin. IDPaths are simple dot-separated JSON object paths,
// e.g. "id" or "data.id", whose matching upstream IDs the host replaces.
type AsyncTaskEndpoint struct {
	Action  string   `json:"action"`
	Kind    string   `json:"kind"`
	IDParam string   `json:"idParam,omitempty"`
	IDPaths []string `json:"idPaths"`
}

func (e Endpoint) TaskSubmit() bool { return e.Task != nil && e.Task.Action == TaskActionSubmit }
func (e Endpoint) TaskQuery() bool  { return e.Task != nil && e.Task.Action == TaskActionQuery }

type EndpointAuth struct {
	// Request headers carrying the API key, checked in order. "authorization"
	// accepts the "Bearer <key>" form.
	Headers []string `json:"headers"`
	// Optional query parameter name (e.g. "key" for Gemini style clients).
	Query string `json:"query,omitempty"`
}

type EndpointRequest struct {
	ModelPath string `json:"modelPath,omitempty"` // gjson path in the body
	// ModelReferences declare additional model invocations in the request.
	// The host applies group/account admission and snapshots their prices.
	ModelReferences []RequestModelReference `json:"modelReferences,omitempty"`
	// ModelParam reads the model from a path parameter instead (e.g. "model").
	ModelParam string `json:"modelParam,omitempty"`
	// ModelSource asks a plugin for the model instead of reading it out of the
	// request. The only value is ModelSourcePlugin: the plugin declaring the
	// endpoint's platform answers PlatformService.ResolveModel. Mutually
	// exclusive with ModelPath and ModelParam, and with StreamPath (ResolveModel
	// answers both the model and whether the response streams).
	ModelSource string `json:"modelSource,omitempty"`
	StreamPath  string `json:"streamPath,omitempty"`
	// Stream marks endpoints that always stream (e.g. streamGenerateContent).
	Stream          bool     `json:"stream,omitempty"`
	PromptTextPaths []string `json:"promptTextPaths,omitempty"`
	// QueryParams are the query parameter names the host puts in
	// RequestMeta.query, and the only ones.
	// Matched case-insensitively; the endpoint's Auth.Query parameter may not
	// be listed and is never passed on.
	QueryParams []string `json:"queryParams,omitempty"`
}

// ModelSourcePlugin is the only value of EndpointRequest.ModelSource: the
// plugin declaring the endpoint's platform answers PlatformService.ResolveModel
// with the model (and whether the response streams) for every request.
const ModelSourcePlugin = "plugin"

type EndpointResp struct {
	Stream    string `json:"stream,omitempty"` // "sse", or "websocket" on a websocket endpoint
	NonStream string `json:"nonStream"`        // "json"
}

// AccountType is a kind of upstream credential. Any plugin may declare
// account types (ARCHITECTURE 6.6); the declaring plugin builds upstream
// requests and classifies errors for accounts of this type. An account type
// serves the endpoints of the platforms it lists (built-in or declared by a
// plugin), plus endpoints the core can convert to one of those protocols.
type AccountType struct {
	Icon        string        `json:"icon,omitempty"` // Package asset; falls back to the plugin icon.
	ID          string        `json:"id"`
	Label       LocalizedText `json:"label"`
	Description LocalizedText `json:"description,omitempty"`
	Form        Form          `json:"form"`
	// Optional presentation grouping for authentication variants of one product.
	CreationGroup   string        `json:"creationGroup,omitempty"`
	AuthMethodLabel LocalizedText `json:"authMethodLabel,omitempty"`
	SensitiveFields []string      `json:"sensitiveFields,omitempty"`
	// Top-level credential keys stored as plain settings (not encrypted).
	SettingsFields []string `json:"settingsFields,omitempty"`
	// GuardedSettings restricts the values of some settings fields for
	// callers without the account:settings:custom permission (CONTRACTS
	// §21.3), e.g. base_url may only be the official endpoint. Types that
	// declare none are unrestricted.
	GuardedSettings []GuardedSetting `json:"guardedSettings,omitempty"`
	// UpstreamFields are the top-level credentials/settings keys that say
	// where requests go (base_url, endpoints); required. []: the type has no
	// caller-set address (OAuth and managed types). omitzero keeps [] apart
	// from a missing key, which the manifest check rejects.
	UpstreamFields []string          `json:"upstreamFields,omitzero"`
	Platforms      []AccountPlatform `json:"platforms"`
	// DefaultModels and DefaultModelMapping prefill the core account fields
	// models / model_mapping when the console creates an account of this
	// type (CONTRACTS §41). They are suggestions only: the host never applies
	// them to an account by itself, and an account created through the API
	// without models still serves every model. Complete model ids
	// (ValidModelID), at most MaxDefaultModels each; every mapping key must
	// be in DefaultModels when that list is not empty, or the mapped model
	// could never be scheduled.
	DefaultModels       []string          `json:"defaultModels,omitempty"`
	DefaultModelMapping map[string]string `json:"defaultModelMapping,omitempty"`
	// DefaultLimits prefill the scheduling and limit fields of a new account
	// of this type in the console (CONTRACTS §68.5), like DefaultModels:
	// suggestions only.
	DefaultLimits *AccountDefaultLimits `json:"defaultLimits,omitempty"`
	// CommonControls declare, per common account setting the core edits and
	// enforces (CommonControlSpecs), its default and whether an operator may
	// see and change it (CONTRACTS §87).
	CommonControls    map[string]AccountCommonControl `json:"commonControls,omitempty"`
	ExtensionsSchema  json.RawMessage                 `json:"extensionsSchema,omitempty"`
	ModelAvailability bool                            `json:"modelAvailability,omitempty"`
	// Quota declares that accounts of this type have subscription quota
	// windows (5-hour / weekly limits and the like) the host can track
	// (CONTRACTS §44). nil: the type has no quota (API keys); the console
	// shows nothing for it.
	Quota *AccountQuota `json:"quota,omitempty"`
	// Balance declares that accounts of this type have a prepaid balance
	// (credits) the plugin can query (CONTRACTS §52). nil: the type has no
	// balance or the plugin does not track it; the console shows nothing.
	Balance *AccountBalance `json:"balance,omitempty"`
	// Refresh declares that the credentials of this type expire and that the
	// plugin implements BuildRefreshRequest / ParseRefreshResponse to renew
	// them (CONTRACTS §48). nil: the host never refreshes them.
	Refresh *AccountRefresh `json:"refresh,omitempty"`
}

// CustomUpstream reports whether a caller without account:settings:custom
// can still point an account of the type anywhere: some upstream field is
// not pinned by a guardedSetting.
func (at AccountType) CustomUpstream() bool {
	return slices.ContainsFunc(at.UpstreamFields, func(f string) bool {
		return !slices.ContainsFunc(at.GuardedSettings, func(g GuardedSetting) bool { return g.Field == f })
	})
}

// AccountRefresh tells the host when to renew the credentials of an
// account type (CONTRACTS §48).
type AccountRefresh struct {
	// ExpiresAtField is the top-level credentials key holding the expiry,
	// Unix seconds as a number or a numeric string (milliseconds are
	// detected). Default "expires_at". An account without a readable expiry
	// is only refreshed on request.
	ExpiresAtField string `json:"expiresAtField,omitempty"`
	// BeforeExpirySec is how long before the expiry the host renews the
	// credentials, 60-86400 (default 1800).
	BeforeExpirySec int `json:"beforeExpirySec,omitempty"`
}

// AccountQuota is how the host learns the subscription quota of an account
// type (CONTRACTS §44). Headers are read from the gateway's own upstream
// responses (success and 429) at no extra upstream cost; Query says the
// plugin also implements BuildQuotaRequest / ParseQuotaResponse, which the
// host calls when its snapshot is stale. At least one of the two is required.
type AccountQuota struct {
	Headers     []QuotaHeader           `json:"headers,omitempty"`
	Query       bool                    `json:"query,omitempty"`
	AutoRefresh bool                    `json:"autoRefresh,omitempty"`
	Windows     []QuotaWindowDefinition `json:"windows,omitempty"`
}

type QuotaWindowDefinition struct {
	Key        string        `json:"key"`
	Label      LocalizedText `json:"label"`
	ShortLabel LocalizedText `json:"shortLabel,omitempty"`
}

// AccountBalance declares that an account type has a balance the host reads
// through the plugin's BuildBalanceRequest / ParseBalanceResponse (CONTRACTS
// §52); "balance": {} is a complete declaration. The host never reads the
// balance from headers, and only shows it: it does not disable, reschedule or
// alert on it. The currency is not declared here: the plugin gives it per
// account in each result (a relay site may count in USD or CNY).
type AccountBalance struct {
	// UpdateInterval is how often the host reads the balance of each
	// account, in seconds: 60-86400, 0 meaning the host's default (180). A
	// failed read waits the same interval; an administrator's refresh does
	// not wait at all.
	UpdateInterval int `json:"updateInterval,omitempty"`
}

// MaxQuotaHeaders bounds AccountQuota.Headers.
const MaxQuotaHeaders = 16

// QuotaHeader maps upstream response headers to one quota window. Header
// names are matched case-insensitively; a header that is absent or does not
// parse leaves that value unknown, and a window none of whose headers is
// present is not reported at all.
type QuotaHeader struct {
	// Key names the window (^[a-z0-9][a-z0-9_]{0,31}$). The console knows
	// "5h", "7d", "7d_sonnet" and "7d_fable"; others are shown as is.
	// Exactly one of Key and WindowMinutes is set.
	Key string `json:"key,omitempty"`
	// WindowMinutes is the header carrying the window's length in minutes;
	// the window is then named by that length (QuotaWindowKey), for an
	// upstream whose header slots do not stand for a fixed window. A
	// response whose length header is absent or unusable reports nothing
	// for this declaration.
	WindowMinutes string `json:"windowMinutes,omitempty"`
	// Utilization is the header carrying the share of the window used.
	Utilization string `json:"utilization,omitempty"`
	// UtilizationUnit is "ratio" (default: 0-1, e.g. Anthropic's 0.42) or
	// "percent" (0-100).
	UtilizationUnit string `json:"utilizationUnit,omitempty"`
	// Reset is the header carrying when the window resets.
	Reset string `json:"reset,omitempty"`
	// ResetFormat is "unix" (default: Unix seconds, milliseconds detected),
	// "rfc3339" or "delta" (seconds from now).
	ResetFormat string `json:"resetFormat,omitempty"`
	// Status is the header carrying "allowed", "allowed_warning" or
	// "rejected"; other values are recorded as unknown.
	Status string `json:"status,omitempty"`
}

// Values of QuotaHeader.UtilizationUnit and QuotaHeader.ResetFormat.
const (
	QuotaUnitRatio    = "ratio"
	QuotaUnitPercent  = "percent"
	QuotaResetUnix    = "unix"
	QuotaResetRFC3339 = "rfc3339"
	QuotaResetDelta   = "delta"
)

// Supported reports whether the declaration tracks anything.
func (q *AccountQuota) Supported() bool {
	return q != nil && (len(q.Headers) > 0 || q.Query)
}

// AccountDefaultLimits are the suggested scheduling and limits of a new
// account; an absent field keeps the console's own default. The ranges are
// those of the account fields (CONTRACTS §18, §68, §71).
type AccountDefaultLimits struct {
	Priority       *int                `json:"priority,omitempty"`
	Weight         *int                `json:"weight,omitempty"`
	MaxConcurrency *int                `json:"maxConcurrency,omitempty"`
	RPMLimit       *int                `json:"rpmLimit,omitempty"`
	TPMLimit       *int64              `json:"tpmLimit,omitempty"`
	SPMLimit       *int                `json:"spmLimit,omitempty"`
	ModelLimits    []DefaultModelLimit `json:"modelLimits,omitempty"`
}

// DefaultModelLimit is a suggested limit of a list of models; the models of
// a list share one set of counters. 0 = unlimited.
type DefaultModelLimit struct {
	Models         []string `json:"models"`
	MaxConcurrency int      `json:"maxConcurrency,omitempty"`
	RPMLimit       int      `json:"rpmLimit,omitempty"`
	TPMLimit       int64    `json:"tpmLimit,omitempty"`
	SPMLimit       int      `json:"spmLimit,omitempty"`
}

// MaxDefaultModelLimits bounds AccountDefaultLimits.ModelLimits; it equals
// the per-account limit of model lists.
const MaxDefaultModelLimits = MaxAccountModelLimits

// MaxDefaultModels bounds AccountType.DefaultModels and DefaultModelMapping;
// it equals the per-account limit of models and model_mapping (CONTRACTS §18).
const MaxDefaultModels = MaxAccountModels

// MaxAccountBatch bounds the accounts of one account action request (UI
// 3.6) and of one GET /accounts?ids= filter; the console never sends more
// per request and a plugin route need not accept more.
const MaxAccountBatch = 500

// GuardedSetting is one restricted settings field of an account type
// (CONTRACTS §21.3). Field must be one of SettingsFields; Allowed is the
// non-empty list of absolute http(s) URLs the field may take. The host
// compares values after trimming whitespace and a trailing "/" and
// lower-casing scheme and host; an empty value (the plugin default) and, on
// update, the account's previous value are always accepted.
//
// Versions (optional) are the version segments a value of the field may end
// with, each starting and not ending with "/" (e.g. "/v1"). Before comparing,
// the host removes at most one of them from each side: the longest that ends
// the escaped path, matched whole and case-sensitively, so ".../a%2Fv1" does
// not end with "/v1" (TrimVersion). It is the rule of the plugin SDK's
// apikey.Spec.Versions and should list the same segments.
type GuardedSetting struct {
	Field    string   `json:"field"`
	Allowed  []string `json:"allowed"`
	Versions []string `json:"versions,omitempty"`
}

// TrimVersion is escapedPath without the longest of versions that ends it, at
// most one, matched whole and case-sensitively on the escaped form: a version
// starts with "/", so ".../xv1" and ".../a%2Fv1" do not end with "/v1". It is
// the one rule behind GuardedSetting.Versions and apikey.Spec.Versions.
func TrimVersion(escapedPath string, versions []string) string {
	best := ""
	for _, v := range versions {
		if len(v) > len(best) && strings.HasSuffix(escapedPath, v) {
			best = v
		}
	}
	return strings.TrimSuffix(escapedPath, best)
}

// AccountPlatform is one platform an account type serves. Empty fields fall
// back to the platform (and endpoint) defaults.
type AccountPlatform struct {
	Platform string `json:"platform"`
	// Endpoints selects endpoint IDs within Platform. Omitted means all endpoints
	// for compatibility; an explicit empty list is invalid.
	Endpoints []string `json:"endpoints,omitempty"`
	// PassHeaders are the client headers the core hands the plugin for this
	// account type on Platform (ClientHeaders). Absent means the platform's
	// list; an explicit empty list means none. omitzero keeps that
	// difference when the struct is encoded again: a nil list is left out,
	// an empty one is written as [].
	PassHeaders []string `json:"passHeaders,omitzero"`
	// Usage overrides the usage rules per protocol of the platform.
	Usage map[string]UsageRules `json:"usage,omitempty"`
	// StreamOnly lists the upstream protocols of Platform whose upstream
	// only accepts streaming requests for this account type (CONTRACTS §83).
	// When the upstream protocol of a non-streaming request is listed, the
	// host sets the upstream body's request.streamPath to true, reads the
	// SSE answer and hands the endpoint's non-streaming path the response
	// object of the terminal event. Each entry is the protocol of a proxy
	// endpoint of Platform the type serves, with response.stream "sse" and a
	// request.streamPath.
	StreamOnly []string `json:"streamOnly,omitempty"`
	// Adapted lists the endpoints of Platform outside Endpoints the account
	// type serves through a core conversion to an endpoint of its subset,
	// keyed by endpoint id (CONTRACTS §83.3, §121.1). An endpoint of
	// Platform neither in the subset nor listed here is not served.
	Adapted map[string]AdaptOptions `json:"adapted,omitempty"`
}

// AdaptOptions are an account type's options for the conversion serving
// an adapted endpoint.
type AdaptOptions struct {
	// Model is the upstream model the conversion uses where the client's
	// model does not go (the chat model driving an image generation tool,
	// CONTRACTS §121.3); "" when the conversion needs none.
	Model string `json:"model,omitempty"`
}

// Adapts reports whether the account type serves the endpoint of p, its
// platform, speaking protocol through a conversion (Adapted).
func (a AccountPlatform) Adapts(p *Platform, protocol string) bool {
	return slices.ContainsFunc(p.Endpoints, func(e Endpoint) bool {
		_, ok := a.Adapted[e.ID]
		return ok && e.Protocol == protocol
	})
}

// StreamsOnly reports whether the upstream of protocol only accepts
// streaming requests for this account type.
func (a AccountPlatform) StreamsOnly(protocol string) bool {
	return slices.Contains(a.StreamOnly, protocol)
}

// ClientHeaders are the client headers the account type receives on p, the
// platform of a, as the gateway hands them to the plugin: its own
// passHeaders when the manifest writes the field (an empty list: none),
// else p's (nil p: none).
func (a AccountPlatform) ClientHeaders(p *Platform) []string {
	if a.PassHeaders == nil && p != nil {
		return p.PassHeaders
	}
	return a.PassHeaders
}

// SupportsEndpoint checks the platform-local endpoint selection.
func (a AccountPlatform) SupportsEndpoint(id string) bool {
	if a.Endpoints == nil {
		return true
	}
	for _, endpoint := range a.Endpoints {
		if endpoint == id {
			return true
		}
	}
	return false
}

// ServesPluginFacts reports whether a serves an endpoint of p whose usage or
// task facts the plugin declaring p states (usageSource "plugin", a task
// submission). Those facts come back from the Execute of the plugin serving
// the account, so an account type of another plugin cannot serve such an
// endpoint (forwarding v2, design 4.3): the manifest check refuses it and
// the registry does not bind it. A built-in platform is declared by no
// plugin and is nobody's to keep: callers ask only for another plugin's
// platform.
func (a AccountPlatform) ServesPluginFacts(p Platform) bool {
	return slices.ContainsFunc(p.Endpoints, func(e Endpoint) bool {
		return a.SupportsEndpoint(e.ID) && (e.PluginUsage() || e.TaskSubmit())
	})
}

// Form describes a console form contributed by a plugin.
type Form struct {
	Mode      string `json:"mode"`           // iframe | native
	Page      string `json:"page,omitempty"` // ui/iframe/... entry (mode=iframe); a native page (native ui.settings)
	Component string `json:"component,omitempty"`
	// Permission (ui.settings only) is one of the plugin's own
	// userPermissions whose holders may read and edit the plugin settings
	// through the core, besides plugin:read / plugin:manage (CONTRACTS §75.1).
	Permission string `json:"permission,omitempty"`
	// Secrets (ui.settings only, native or iframe) are the settings the core
	// masks (CONTRACTS §75.1); an account type marks its secrets in
	// sensitiveFields. nil (the key absent) says nothing about which settings
	// are secret, so the core masks every one; empty ([]) says none is.
	// omitzero keeps an empty list when a manifest is built in Go.
	Secrets []SettingsSecret `json:"secrets,omitzero"`
}

// UsageRules tell the host how to read token usage from upstream responses.
type UsageRules struct {
	// "exclusive": input tokens exclude cache (Anthropic);
	// "inclusive": prompt tokens include cache (OpenAI).
	Semantics string        `json:"semantics"`
	SSE       []SSEUsageMap `json:"sse,omitempty"`
	JSON      *UsageMap     `json:"json,omitempty"`
	// Extra metering facts usable in price expressions as u("key").
	Facts map[string]UsageFact `json:"facts,omitempty"`
	// Additional meters model usage excluded from the main counters. Each
	// observed array is a complete snapshot, never a stream delta to sum.
	Additional []AdditionalUsageRule `json:"additional,omitempty"`
	// Attempts replaces main-counter pricing with verified per-attempt usage.
	Attempts *AttemptUsageRule `json:"attempts,omitempty"`
}

type AdditionalUsageRule struct {
	// UsePrimaryModel takes the admitted upstream identity supplied by the
	// host. A conflicting model reported by the response is rejected.
	UsePrimaryModel bool              `json:"usePrimaryModel,omitempty"`
	Name            string            `json:"name"`
	JSONPath        string            `json:"jsonPath,omitempty"`
	SSEPath         string            `json:"ssePath,omitempty"`
	SSEEvent        string            `json:"sseEvent,omitempty"`
	TypePath        string            `json:"typePath"`
	TypeValue       string            `json:"typeValue"`
	ModelPath       string            `json:"modelPath"`
	Map             map[string]string `json:"map"`
	Semantics       string            `json:"semantics"`
}

// Values of Endpoint.UsageSource.
const (
	// UsageSourceRules is the default: the host reads usage from the
	// declarative sse/json maps of the rules in effect. An empty
	// Endpoint.UsageSource means this.
	UsageSourceRules = "rules"
	// UsageSourcePlugin has the plugin return the usage from
	// PlatformService.Execute instead (ExecuteResponse.usage).
	//
	// The declarative rules are not replaced by the plugin: the host keeps
	// applying them while it relays (they cost nothing extra and detect
	// stream errors), and uses what they produced when the plugin returns
	// host_usage.
	UsageSourcePlugin = "plugin"
)

// Usage map values are gjson paths; "a+b" sums several numeric paths
// (missing ones count as 0), e.g. output tokens plus thinking tokens.
type SSEUsageMap struct {
	Event string            `json:"event"` // SSE event name, "" = any
	Map   map[string]string `json:"map"`   // usage field -> gjson path(s) in data
}

type UsageMap struct {
	Map map[string]string `json:"map"` // usage field -> gjson path(s), "+" sums
}

// Standard usage field names produced by UsageRules maps.
const (
	UsageModel               = "model"
	UsageInputTokens         = "input_tokens"
	UsageOutputTokens        = "output_tokens"
	UsageCacheReadTokens     = "cache_read_tokens"
	UsageCacheCreationTokens = "cache_creation_tokens"
	UsageCacheCreation1h     = "cache_creation_1h_tokens"
	// UsageImageInputTokens and UsageImageOutputTokens are the image tokens
	// among the input and output tokens (an image generation or edit). They
	// are part of input_tokens / output_tokens, not added to them, whatever
	// the rules' semantics; a price expression using img / img_o bills them
	// at the image price and the rest of p / c at the text price.
	UsageImageInputTokens  = "image_input_tokens"
	UsageImageOutputTokens = "image_output_tokens"
)

type UsageFact struct {
	Type        string        `json:"type"` // number | boolean | enum | string
	Unit        string        `json:"unit,omitempty"`
	Enum        []string      `json:"enum,omitempty"`
	Path        string        `json:"path,omitempty"`
	Description LocalizedText `json:"description,omitempty"`
}

type StickyRule struct {
	Name        string            `json:"name"`
	Match       StickyMatch       `json:"match"`
	KeySources  []StickyKeySource `json:"keySources"`
	ValueRegex  string            `json:"valueRegex,omitempty"`
	TTLSeconds  int               `json:"ttlSeconds,omitempty"`
	KeyIncludes []string          `json:"keyIncludes,omitempty"` // group | model | rule
	OnFailure   string            `json:"onFailure,omitempty"`   // failover (default) | stick
	// PassHeaders are the client headers a request matching the rule
	// forwards upstream, with the rules of an account's header passthrough
	// ("*", "regex:<pattern>" or exact names; CONTRACTS §81).
	PassHeaders []string `json:"passHeaders,omitempty"`
}

type StickyMatch struct {
	Protocols         []string `json:"protocols,omitempty"`
	Models            []string `json:"models,omitempty"` // globs
	UserAgentContains []string `json:"userAgentContains,omitempty"`
}

// StickyKeySource is where a sticky rule reads the session value from. The
// core reads it itself; no plugin computes it.
type StickyKeySource struct {
	Type string `json:"type"` // body | header | api_key | user
	Path string `json:"path,omitempty"`
	Name string `json:"name,omitempty"`
}

// ---------------------------------------------------------------- hooks, events, jobs

// Hook points (CONTRACTS §20.1, §85).
const (
	// HookPointRequest runs once per request, after auth and group
	// resolution, before the balance check and scheduling.
	HookPointRequest = "gateway.request"
	// HookPointAttempt runs once the account is chosen, before the request
	// is sent to it: every HTTP attempt and every WebSocket turn.
	HookPointAttempt = "gateway.attempt"
)

type Hook struct {
	ID             string    `json:"id,omitempty"`
	Point          string    `json:"point"` // HookPointRequest | HookPointAttempt
	Order          int       `json:"order"`
	Match          HookMatch `json:"match"`
	Needs          []string  `json:"needs,omitempty"`
	MaxPromptBytes int       `json:"maxPromptBytes,omitempty"`
	TimeoutMs      int       `json:"timeoutMs,omitempty"`
	Failure        string    `json:"failure,omitempty"` // open (default) | closed
}

type HookMatch struct {
	Protocols []string `json:"protocols,omitempty"`
	Models    []string `json:"models,omitempty"`
	Groups    []string `json:"groups,omitempty"`
}

// ---------------------------------------------------------------- scheduling

// Scheduler is the plugin's part in gateway scheduling. Sticky sessions are
// not part of it: the core and the administrator own them.
type Scheduler struct {
	// Rank lets the plugin rewrite the priority/weight of the candidate
	// accounts of requests that did not hit a sticky binding.
	Rank *SchedulerRank `json:"rank,omitempty"`
}

// SchedulerRank declares SchedulerService.RankAccounts ("scheduler.rank.v1").
// Several plugins declaring it run serially in Order; each sees the values
// left by the previous one. There is deliberately no `failure` field: the
// call is always fail open, because refusing a request over a weight the
// plugin could not compute would be far too aggressive. On error, timeout or
// a malformed answer the host keeps the accounts' own priority and weight.
type SchedulerRank struct {
	// Order sorts plugins that declare rank; the smaller one runs first
	// (ties are broken by plugin key).
	Order int `json:"order,omitempty"`
	// Match restricts the requests the host calls for (protocols / models /
	// groups); empty matches every request.
	Match HookMatch `json:"match,omitempty"`
	// TimeoutMs bounds one call; 0 uses the host default.
	TimeoutMs int `json:"timeoutMs,omitempty"`
}

type Events struct {
	Subscribe []string `json:"subscribe"` // event types or "prefix.*"
	BatchSize int      `json:"batchSize,omitempty"`
}

type Job struct {
	ID string `json:"id"`
	// Schedule is a 5-field cron (UTC) or "@every <duration>", at most every
	// MinJobEvery.
	Schedule string `json:"schedule"`
	// TimeoutSec bounds one run: 0 is 60 s; at most MaxClusterJobTimeoutSec
	// for a cluster job, MaxNodeJobTimeoutSec for a node job. Work that may
	// take longer is done in batches that stop before the run's ctx deadline
	// and continue in the next run (pluginsdk.JobResult.ContinueAfter).
	// Older manifests may declare more; the core cuts it to the limit.
	TimeoutSec int `json:"timeoutSec,omitempty"`
	// Scope is JobScopeCluster (the default: one run per slot in the
	// cluster) or JobScopeNode (every node runs it on its own instance).
	Scope string `json:"scope,omitempty"`
	// Title names the job on the console's scheduled task page; without it
	// the console shows the plugin name and the job id.
	Title LocalizedText `json:"title,omitempty"`
}

// Job scopes (manifest jobs[].scope).
const (
	JobScopeCluster = "cluster"
	JobScopeNode    = "node"
)

// Limits of manifest jobs; the task framework runs nothing beyond them
// (CONTRACTS §77.8).
const (
	MaxClusterJobTimeoutSec = 3600
	MaxNodeJobTimeoutSec    = 600
	MaxNodeJobs             = 10
	MinJobEvery             = 5 * time.Second
	// DefaultJobTimeoutSec is the timeout of a job declaring none.
	DefaultJobTimeoutSec = 60
)

// NodeScope reports whether every node runs the job.
func (j Job) NodeScope() bool { return j.Scope == JobScopeNode }

// Database is the plugin's own tables. They live in the core's schema, every
// table, index and sequence named with TablePrefix; the plugin's database
// identity owns them and reaches nothing else but what Access asks for
// (CONTRACTS §130).
type Database struct {
	Migrations string `json:"migrations"` // package directory, e.g. "migrations/"
	// Access lists the tables outside the plugin it uses, granted as asked
	// on the administrator's consent (host permission "db.core_read").
	Access []DatabaseAccess `json:"access,omitempty"`
}

// DatabaseAccess asks for privileges on one table outside the plugin.
type DatabaseAccess struct {
	Table      string   `json:"table"`
	Columns    []string `json:"columns,omitempty"` // none: the whole table (what "delete" needs)
	Privileges []string `json:"privileges"`        // DatabasePrivileges
}

// DatabasePrivileges are the privileges a plugin may ask for on a table
// outside it. Writing the core's tables directly bypasses the core's
// caches, authorization versions and audit: the consent page says so.
var DatabasePrivileges = []string{"select", "insert", "update", "delete", "references"}

// TablePrefix is the prefix of every table, index and sequence of plugin
// pluginKey: "plg_<key>__". Plugin keys hold no "__" and do not end in "_",
// so no plugin's prefix starts another's.
func TablePrefix(pluginKey string) string { return TablePrefixHead + pluginKey + TablePrefixTail }

// TablePrefixHead and TablePrefixTail enclose the key in TablePrefix, for
// SQL that builds the prefix from a key column.
const (
	TablePrefixHead = "plg_"
	TablePrefixTail = "__"
)

// ---------------------------------------------------------------- permissions, routes, ui

// PermissionGroup is one group of a plugin's permissions; its position in
// permissionGroups is its order.
type PermissionGroup struct {
	Key   string        `json:"key"`
	Label LocalizedText `json:"label"`
}

// UserPermission is registered into the RBAC catalog as
// "plugin.<key>:<Key>".
type UserPermission struct {
	Key         string        `json:"key"` // ^[a-z0-9_]+(:[a-z0-9_]+)+$, not ending in :own
	Label       LocalizedText `json:"label"`
	Description LocalizedText `json:"description,omitempty"`
	Group       string        `json:"group,omitempty"` // a permissionGroups key
	// Risk is RiskLow (the default), RiskSensitive or RiskCritical. A
	// critical permission is super-admin only: no defaults, and only a
	// holder may grant it (initially only the super admin).
	Risk string `json:"risk,omitempty"`
	// Requires are local keys of this manifest the permission is of little
	// use without; the role editor hints at them, authorization ignores them.
	Requires []string `json:"requires,omitempty"`
	// Scope lets a grant reach part of the plugin's data; nil is unscoped.
	Scope *PermissionScope `json:"scope,omitempty"`
	// Defaults are the built-in roles (BuiltinRoleKeys) the permission is
	// suggested for when the catalog first creates it: true for an unscoped
	// permission, a list of scopes ("*", "own" or a values key) for a scoped
	// one. A role left out gets nothing; the super admin is never listed.
	Defaults map[string]RoleDefault `json:"defaults,omitempty"`
	// InheritFrom are local keys of this plugin's permissions whose holders
	// get this permission when the catalog first creates it: a renamed or
	// split permission keeps its holders. Only grants that exist before the
	// sync count, so a chain through another permission created in the same
	// sync is not followed.
	InheritFrom []string `json:"inheritFrom,omitempty"`
}

// PermissionScope is where the scopes of a permission come from: the
// plugin's own resource (Resource, with candidate ids from the Options route
// and/or Own), or a fixed list of Values. Never a core resource: a plugin
// scopes accounts through routes[].accounts.
type PermissionScope struct {
	Resource string       `json:"resource,omitempty"` // ScopeResourcePattern, not reserved
	Options  string       `json:"options,omitempty"`  // "/path" of a declared GET admin route
	Own      bool         `json:"own,omitempty"`      // "own" (created by the holder) may be granted
	Values   []ScopeValue `json:"values,omitempty"`
}

// ScopeValue is one value of a values scope.
type ScopeValue struct {
	Key   string        `json:"key"` // ScopeValuePattern
	Label LocalizedText `json:"label"`
}

// Route is a plugin-owned console endpoint under /api/v1/p/<key>.
type Route struct {
	Method     string `json:"method"`
	Path       string `json:"path"`
	Scope      string `json:"scope"`                // admin | user | public | webhook
	Permission string `json:"permission,omitempty"` // plugin-local key; required for admin/user
	// Accounts makes the core check, before forwarding, that the caller may
	// act on the accounts the request names (admin/user routes only; see
	// EffectiveRouteAccounts for the routes account actions post to).
	Accounts *RouteAccounts `json:"accounts,omitempty"`
	// ScopeFrom names where the request carries the id the core checks
	// against the caller's scope of Permission before forwarding:
	// "path:<param>", "query:<name>" or "body:<field>" (the accounts.from
	// DSL, ParseAccountsFrom). Admin/user routes whose permission declares a
	// scope only (UI 3.16).
	ScopeFrom string `json:"scopeFrom,omitempty"`
}

type UI struct {
	// Sections are sidebar groups of the plugin's own (see Menu.Section).
	Sections []MenuSection   `json:"sections,omitempty"`
	Menus    []Menu          `json:"menus,omitempty"`
	Pages    map[string]Page `json:"pages,omitempty"`
	Slots    []Slot          `json:"slots,omitempty"`
	Native   *NativeUI       `json:"native,omitempty"`
	Settings *Form           `json:"settings,omitempty"`
	// AccountActions are entries the console adds to every account row's
	// action menu (and, with Bulk, to the bulk bar); clicking one posts to
	// Route (UI 3.6).
	AccountActions []AccountAction `json:"accountActions,omitempty"`
}

// MenuSection is a sidebar group declared by a plugin. It is shown as its
// own group, placed among the core groups (CoreMenuSections) by Order;
// without Order it goes after plugins (PluginMenuSectionOrder). Its id must
// not be a core section id.
type MenuSection struct {
	ID    string        `json:"id"`
	Label LocalizedText `json:"label"`
	Order int           `json:"order,omitempty"`
}

// CoreMenuSection is a sidebar group of the console: plugin menus may join
// it by id, and plugin sections are placed among the core ones by Order.
type CoreMenuSection struct {
	ID    string
	Order int
}

// coreMenuSections is the one list of core sidebar sections shared by the
// host (authz menus) and manifest validation. "plugins" is the shared group
// plugin menus join by default.
var coreMenuSections = []CoreMenuSection{
	{"overview", 100}, {"gateway", 200}, {"security", 300}, {"finance", 400}, {"topup", 450},
	{"users", 500}, {"plugins", 600}, {"system", 700}, {"me", 800},
}

// PluginMenuSectionOrder places a plugin section that declares no order:
// after the shared "plugins" section, before system.
const PluginMenuSectionOrder = 650

// CoreMenuSections returns the core sidebar sections in display order.
func CoreMenuSections() []CoreMenuSection {
	return append([]CoreMenuSection(nil), coreMenuSections...)
}

// CoreMenuSectionOrder returns the order of core section id, or 0 and false
// when id is not a core section.
func CoreMenuSectionOrder(id string) (int, bool) {
	for _, sec := range coreMenuSections {
		if sec.ID == id {
			return sec.Order, true
		}
	}
	return 0, false
}

type Menu struct {
	ID string `json:"id"`
	// Section is a core group (CoreMenuSections; "plugins" is the shared
	// plugin group: the item is appended to it) or the id of one of
	// UI.Sections.
	Section    string        `json:"section"`
	Label      LocalizedText `json:"label"`
	Icon       string        `json:"icon,omitempty"`
	Page       string        `json:"page"`
	Permission string        `json:"permission,omitempty"`
	Order      int           `json:"order,omitempty"`
}

// Page is a plugin page: type=iframe loads Page.Src in a sandbox,
// type=native mounts Component.
type Page struct {
	Type      string        `json:"type"`
	Title     LocalizedText `json:"title,omitempty"`
	Src       string        `json:"src,omitempty"`
	Component string        `json:"component,omitempty"`
	// Nav lists the sections of a native page so the console can draw them
	// in its left sidebar, the way it draws the core system settings
	// (UI 3.14). The page shows the section named by ?tab=<item key>, and
	// the first item the caller may see is the default.
	Nav *PageNav `json:"nav,omitempty"`
}

// PageNav is a page's sub-navigation: groups of items, in order.
type PageNav struct {
	Groups []NavGroup `json:"groups"`
}

// NavGroup is one labelled group of a page's sub-navigation.
type NavGroup struct {
	Key   string        `json:"key"`
	Label LocalizedText `json:"label"`
	Items []NavItem     `json:"items"`
}

// NavItem is one section of a page. The core drops the items the caller may
// not see before the console gets the manifest, so the sidebar never offers
// a section whose routes would refuse the caller anyway.
type NavItem struct {
	Key   string        `json:"key"`
	Label LocalizedText `json:"label"`
	Icon  string        `json:"icon,omitempty"`
	// Permission is a plugin permission key (userPermissions).
	Permission string `json:"permission,omitempty"`
	// CorePermission is a core permission key. A section can be meant for
	// the people who manage the matching core area as well as for holders
	// of the plugin's own permission, so the item shows when the caller
	// holds either; with neither set it shows to everyone who sees the page.
	CorePermission string `json:"corePermission,omitempty"`
}

type Slot struct {
	Slot       string `json:"slot"` // dashboard.widgets | account.detail.tabs | account.form.widgets | account.list.weight | account.list.toolbar | account.editor.section | account.list.quota
	Component  string `json:"component"`
	Permission string `json:"permission,omitempty"`
	// Label and Icon describe an account.editor.section slot (UI 3.7) only:
	// the navigation entry the account editor appends.
	Label LocalizedText `json:"label,omitempty"`
	Icon  string        `json:"icon,omitempty"`
	// AccountTypes are the ids of the plugin's own account types the slot is
	// shown for (all types when empty), on account.editor.section (UI 3.7),
	// account.detail.tabs and account.list.quota (UI 3.8) only.
	AccountTypes []string `json:"accountTypes,omitempty"`
}

// SlotAccountEditorSection is the account editor section slot (UI 3.7).
const SlotAccountEditorSection = "account.editor.section"

// SlotAccountDetailTabs is the account detail tab slot.
const SlotAccountDetailTabs = "account.detail.tabs"

// SlotAccountListQuota is the decoration slot of the account list's quota
// column (UI 3.8).
const SlotAccountListQuota = "account.list.quota"

// AccountAction is a declarative account operation. The console calls
// Route as "POST /api/v1/p/<key><path>" with {"account_ids": [...]}, a
// one-element array for a single row, at most MaxAccountBatch accounts per
// request, looks only at the status code and reloads the list on success.
type AccountAction struct {
	ID         string        `json:"id"`                // unique within the plugin
	Label      LocalizedText `json:"label"`             // menu text
	Icon       string        `json:"icon,omitempty"`    // SIcon name
	Permission string        `json:"permission"`        // required: plugin-local key in userPermissions, equal to the route's
	Route      string        `json:"route"`             // "POST /path": an admin route declared in routes
	Bulk       bool          `json:"bulk,omitempty"`    // also offered in the bulk bar
	Confirm    LocalizedText `json:"confirm,omitempty"` // confirmation text, {n} = account count; none means no prompt
	Danger     bool          `json:"danger,omitempty"`  // rendered as a destructive item
	// VisibleFor says which rows offer the action: "editable" (the default
	// when empty) only rows the user may update (account:update), "all"
	// every row the user can see. Orphaned accounts never offer it.
	VisibleFor string `json:"visibleFor,omitempty"`
	// AccountTypes limits the action to rows of the plugin's own account
	// types with these ids (UI 3.8); every row when empty.
	AccountTypes []string `json:"accountTypes,omitempty"`
}

type NativeUI struct {
	Entry string `json:"entry"` // e.g. "ui/native/entry.js"
}

type Resources struct {
	MemoryMB     int     `json:"memoryMB,omitempty"`
	CPU          float64 `json:"cpu,omitempty"`
	MaxThreads   int     `json:"maxThreads,omitempty"`
	MaxOpenFiles int     `json:"maxOpenFiles,omitempty"`
}

// HostPermission is a host capability the plugin asks the administrator for.
type HostPermission struct {
	ID       string         `json:"id"`
	Scope    map[string]any `json:"scope,omitempty"`
	Reason   LocalizedText  `json:"reason,omitempty"`
	Optional bool           `json:"optional,omitempty"`
}

// Host permission ids and their risk level.
const (
	RiskLow      = "low"
	RiskMedium   = "medium"
	RiskHigh     = "high"
	RiskCritical = "critical"
	// RiskSensitive is a userPermissions risk only (UserPermissionRisks);
	// host permissions keep the four levels above.
	RiskSensitive = "sensitive"
)

// HostPermissionRisk keys are string literals, never constants such as
// PermPaymentGateway: the console's tests read this map from the source
// text (web/src/views/plugins/pluginUtil.spec.ts).
var HostPermissionRisk = map[string]string{
	"kv":                  RiskLow,
	"secrets":             RiskLow,
	"prices":              RiskLow, // read-only pricing of token usage (CONTRACTS §72.13.1)
	"config":              RiskLow,
	"log":                 RiskLow,
	"broadcast":           RiskLow,
	"routes.admin":        RiskMedium,
	"routes.user":         RiskMedium,
	"events":              RiskMedium,
	"jobs":                RiskMedium,
	"ui.menu":             RiskMedium,
	"ui.iframe":           RiskMedium,
	"lock":                RiskMedium, // not low: low is granted without the administrator (install/consent.go)
	"fs.data":             RiskMedium, // read and write the plugin's own data directory (CONTRACTS §43.8)
	"authz.read":          RiskMedium, // AuthzCheck on keys that are not the plugin's own
	"accounts.auto_state": RiskHigh,
	"db.schema":           RiskHigh,
	"net":                 RiskHigh,
	"routes.public":       RiskHigh,
	"routes.webhook":      RiskHigh,
	"gateway.hook":        RiskHigh,
	"gateway.endpoint":    RiskHigh,
	"platform.register":   RiskHigh,
	"scheduler.rank":      RiskHigh,
	"sticky.clear":        RiskHigh,
	"notify.send":         RiskHigh, // free-form mail from the deployment to any recipient (phishing), even rate limited
	"payment.gateway":     RiskHigh, // what its gateways verify marks their orders paid (the order's amount, checked by the core)
	"gateway.observe":     RiskHigh,
	"users.read":          RiskHigh,
	"ledger.credit":       RiskCritical,
	"ledger.debit":        RiskCritical,
	"ui.native":           RiskCritical,
	"users.write":         RiskCritical,
	"db.core_read":        RiskCritical, // database.access: access to tables outside the plugin
}

// Values of Endpoint.Kind.
const (
	// EndpointKindProxy forwards one HTTP request to one upstream request.
	EndpointKindProxy = "proxy"
	// EndpointKindWebSocket upgrades the client connection and keeps one
	// upstream WebSocket per connection. Every client message carrying a model
	// at request.modelPath starts a turn that is scheduled, metered with the
	// usage.sse rules (matched by the message "type") and billed on its own.
	EndpointKindWebSocket = "websocket"
	// ResponseWebSocket is EndpointResp.Stream of a websocket endpoint.
	ResponseWebSocket = "websocket"
)

// WebSocket reports whether the endpoint is a websocket endpoint.
func (e Endpoint) WebSocket() bool { return e.Kind == EndpointKindWebSocket }
