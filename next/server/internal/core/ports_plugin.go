package core

import (
	"context"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// ============================================================ plugin extension points (registry owner: C plugin-runtime)
//
// Capability interfaces are runtime-agnostic: the gRPC runtime implements them
// over go-plugin today; a JS runtime would implement the same interfaces.
// Implementations apply per-plugin concurrency limits and call timeouts, and
// return ErrPluginUnavailable when the plugin instance is down.

// ClassifyPrefixBytes is how much of an upstream error body the failure
// rules read: the prefix handed to ClassifyError, and the text the
// administrator's failure policy and auto-disable keywords match.
const ClassifyPrefixBytes = 4 << 10

// ClassifyPrefix is body cut to ClassifyPrefixBytes.
func ClassifyPrefix(body []byte) []byte {
	if len(body) > ClassifyPrefixBytes {
		return body[:ClassifyPrefixBytes]
	}
	return body
}

// PlatformPlugin is the PlatformService of a plugin declaring
// platform.adapter.v1: Execute, the one path of a gateway request
// (ExecutePlugin), Monitor (MonitorPlugin), and the calls below.
type PlatformPlugin interface {
	ExecutePlugin
	MonitorPlugin
	ValidateCredentials(ctx context.Context, in *pluginv1.ValidateCredentialsRequest) (*pluginv1.ValidateCredentialsResponse, error)
	// ClassifyError classifies one failed upstream attempt of a relay that
	// did not wait (the host read the error). The error of a relay that
	// waited is classified by the plugin in ExecuteResponse.classification.
	ClassifyError(ctx context.Context, in *pluginv1.ClassifyErrorRequest) (*pluginv1.ClassifyErrorResponse, error)
	BuildTestRequest(ctx context.Context, in *pluginv1.BuildTestRequestRequest) (*pluginv1.BuildTestRequestResponse, error)
	// BuildModelsRequest is optional for plugins: a plugin without it answers
	// gRPC Unimplemented (CONTRACTS §19).
	BuildModelsRequest(ctx context.Context, in *pluginv1.BuildModelsRequestRequest) (*pluginv1.BuildModelsRequestResponse, error)
	// ResolveModel answers the model (and whether the response streams) of a
	// request to an endpoint declaring request.modelSource "plugin"
	// (CONTRACTS §25.2). It is called on the PlatformBinding.Client of the
	// endpoint's platform, before any account is picked. Optional for
	// plugins: one without it answers gRPC Unimplemented, and the gateway
	// turns any failure into 400 "model is required".
	ResolveModel(ctx context.Context, in *pluginv1.ResolveModelRequest) (*pluginv1.ResolveModelResponse, error)
	// QuotaQuery reads the subscription quota of one account (CONTRACTS
	// §44): the plugin declaring the account type builds the request and
	// parses the answer, the account module sends it through the account's
	// proxy behind netguard. Called on the AccountTypeBinding.Client, only
	// for types declaring manifest quota.query. Optional for plugins: one
	// without them answers gRPC Unimplemented, and the account keeps the
	// snapshot sampled passively from the gateway's upstream responses.
	BuildQuotaRequest(ctx context.Context, in *pluginv1.BuildQuotaRequestRequest) (*pluginv1.BuildQuotaRequestResponse, error)
	ParseQuotaResponse(ctx context.Context, in *pluginv1.ParseQuotaResponseRequest) (*pluginv1.QuotaResult, error)
	// CredentialRefresh renews the credentials of one account (CONTRACTS
	// §48): the plugin declaring the account type builds the request and
	// parses the answer, the account module sends it through the account's
	// proxy behind netguard, under a cluster lock per account, and saves the
	// merged credentials only if nobody changed them meanwhile. Called on
	// the AccountTypeBinding.Client, only for types declaring manifest
	// refresh. Optional for plugins: one without them answers gRPC
	// Unimplemented and nothing is refreshed.
	BuildRefreshRequest(ctx context.Context, in *pluginv1.BuildRefreshRequestRequest) (*pluginv1.BuildRefreshRequestResponse, error)
	ParseRefreshResponse(ctx context.Context, in *pluginv1.ParseRefreshResponseRequest) (*pluginv1.RefreshResult, error)
	// BalanceQuery reads the balance of one account (CONTRACTS §52): the
	// plugin declaring the account type builds the requests and parses the
	// answers, the account module checks every target against the upstream
	// host rules (CheckPluginUpstream) and sends them through the account's
	// proxy behind netguard without following redirects. Called on the
	// AccountTypeBinding.Client, only for types declaring manifest balance.
	// Optional for plugins: one without them answers gRPC Unimplemented and
	// the type has no balance.
	BuildBalanceRequest(ctx context.Context, in *pluginv1.BuildBalanceRequestRequest) (*pluginv1.BuildBalanceRequestResponse, error)
	ParseBalanceResponse(ctx context.Context, in *pluginv1.ParseBalanceResponseRequest) (*pluginv1.ParseBalanceResponseResponse, error)
}

type HookPlugin interface {
	OnGatewayRequest(ctx context.Context, in *pluginv1.GatewayRequestHookRequest) (*pluginv1.GatewayRequestHookResponse, error)
}

type AppPlugin interface {
	RunJob(ctx context.Context, in *pluginv1.RunJobRequest) (*pluginv1.RunJobResponse, error)
	OnEvents(ctx context.Context, in *pluginv1.OnEventsRequest) (*pluginv1.OnEventsResponse, error)
}

type HTTPPlugin interface {
	HandleHTTP(ctx context.Context, in *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error)
}

// SettingsValidator is a plugin's check of its own settings before the core
// stores them (PluginService.ValidateSettings, CONTRACTS §75.1). Optional for
// plugins: one without it answers UNIMPLEMENTED, which means accepted.
type SettingsValidator interface {
	ValidateSettings(ctx context.Context, in *pluginv1.ValidateSettingsRequest) (*pluginv1.ValidateSettingsResponse, error)
}

// SchedulerPlugin is a plugin's part in scheduling. Plugins take no part in
// sticky sessions (CONTRACTS §5.6).
type SchedulerPlugin interface {
	RankAccounts(ctx context.Context, in *pluginv1.RankAccountsRequest) (*pluginv1.RankAccountsResponse, error)
}

// PluginInfo identifies one active plugin version on this node.
type PluginInfo struct {
	// GrantedPermissions is the local generation's authority snapshot. It is
	// checked against PG before a managed node is declared ready.
	GrantedPermissions []string `json:"-"`
	Key                string
	Version            string
	Manifest           *manifest.Manifest
	Publisher          string
	Trust              string // official | verified | community | unsigned
	// AssetBase is the public URL prefix for package assets, e.g.
	// "/plugin-ui/guard/0.1.0-3fa9c1" (version + package hash for caching).
	AssetBase string
	// Conflicts lists the exclusive resources this generation could not give
	// the plugin (another plugin claimed them first, or the core holds them);
	// the plugin runs without them and the console shows it as abnormal.
	Conflicts []ResourceConflict
}

// ResourceConflict is one exclusive resource a plugin declares but does not
// hold in a generation.
type ResourceConflict struct {
	Resource string `json:"resource"` // platform | notification_channel
	ID       string `json:"id"`
	Reason   string `json:"reason"`
}

// EndpointBinding is one gateway endpoint of an available platform. Plugin
// is the declaring plugin (zero for built-in platforms).
type EndpointBinding struct {
	Plugin   PluginInfo
	Platform string // platform id
	Endpoint manifest.Endpoint
}

// PlatformBinding is a built-in platform (Builtin, zero Plugin) or one
// declared by an enabled plugin (ARCHITECTURE 6.6).
type PlatformBinding struct {
	Plugin   PluginInfo
	Builtin  bool
	Platform manifest.Platform
	// Client is the declaring plugin's PlatformService; nil for built-in
	// platforms and for plugins that do not implement platform.adapter.v1.
	// Every caller must check it.
	Client PlatformPlugin
}

// AccountTypeKey identifies an account type: the declaring plugin and the
// type id (ARCHITECTURE 6.6).
type AccountTypeKey struct {
	PluginKey string
	Type      string
}

// AccountTypeBinding is one account type of an enabled plugin. Client is the
// declaring plugin, never nil: every account type is served by a plugin
// process. It validates credentials, executes gateway requests (ExecutePlugin)
// and classifies errors for accounts of this type.
type AccountTypeBinding struct {
	Plugin    PluginInfo
	Type      manifest.AccountType
	DeclIndex int // index in the plugin's manifest.AccountTypes (preserves declaration order)
	Client    PlatformPlugin
}

// Key returns the account type key.
func (b AccountTypeBinding) Key() AccountTypeKey {
	return AccountTypeKey{PluginKey: b.Plugin.Key, Type: b.Type.ID}
}

// PassHeaders are the client headers the account type receives
// (manifest.AccountType.PassHeaders) with platform, a Generation's Platform,
// looking the platforms up.
func (b AccountTypeBinding) PassHeaders(platform func(id string) (PlatformBinding, bool)) []string {
	return b.Type.PassHeaders(func(id string) *manifest.Platform {
		if pb, ok := platform(id); ok {
			return &pb.Platform
		}
		return nil
	})
}

// Supports returns the account type's entry for platform, if it serves it.
func (b AccountTypeBinding) Supports(platformID string) (manifest.AccountPlatform, bool) {
	for _, p := range b.Type.Platforms {
		if p.Platform == platformID {
			return p, true
		}
	}
	return manifest.AccountPlatform{}, false
}

type HookBinding struct {
	Plugin        PluginInfo
	Hook          manifest.Hook
	GrantedFields []string // hook needs ∩ approved gateway.hook scope
	Client        HookPlugin
}

type RouteBinding struct {
	Plugin PluginInfo
	Route  manifest.Route
	Client HTTPPlugin
}

// AccountRankerBinding is one plugin taking part in account scheduling
// through manifest scheduler.rank ("scheduler.rank.v1"). The gateway calls
// Client.RankAccounts for requests Rank.Match covers that did not hit a
// sticky binding, and falls back to the accounts' own priority and weight on
// error (rank is always fail open).
type AccountRankerBinding struct {
	Plugin PluginInfo
	Rank   manifest.SchedulerRank
	Client SchedulerPlugin
}

type JobBinding struct {
	Plugin PluginInfo
	Job    manifest.Job
	Client AppPlugin
}

type SubscriptionBinding struct {
	Plugin PluginInfo
	Events manifest.Events
	Client AppPlugin
}

// Generation is an immutable snapshot of every active extension on this
// node. A request pins one generation for its whole lifetime.
type Generation interface {
	Number() uint64
	Plugins() []PluginInfo
	Plugin(key string) (PluginInfo, bool)
	Endpoints() []EndpointBinding
	// Platforms lists the built-in platforms and those of enabled plugins.
	Platforms() []PlatformBinding
	Platform(platformID string) (PlatformBinding, bool)
	// PlatformForProtocol returns the platform owning an endpoint protocol
	// (protocols are "<platform id>.<name>", unique across platforms).
	PlatformForProtocol(protocol string) (PlatformBinding, bool)
	AccountTypes() []AccountTypeBinding
	AccountType(pluginKey, typeID string) (AccountTypeBinding, bool)
	// AccountTypesForPlatform lists account types serving the platform
	// natively (conversion is decided by the gateway).
	AccountTypesForPlatform(platformID string) []AccountTypeBinding
	Hooks(point string) []HookBinding // sorted by order
	Scheduler(pluginKey string) (SchedulerPlugin, bool)
	// AccountRankers lists the plugins declaring scheduler.rank, sorted by
	// declared order then plugin key; the gateway calls them in that order.
	AccountRankers() []AccountRankerBinding
	Routes(pluginKey string) []RouteBinding
	Jobs() []JobBinding
	// JobRunner is the job runner of a plugin granted "jobs": it runs the
	// plugin's runtime tasks (HostService.SyncTasks ...), which no manifest
	// job describes.
	JobRunner(pluginKey string) (AppPlugin, bool)
	// SettingsValidator checks the settings of an enabled plugin before
	// PUT /plugins/:key/settings stores them.
	SettingsValidator(pluginKey string) (SettingsValidator, bool)
	Subscriptions() []SubscriptionBinding
	// ReadAsset reads a file from the active package of a plugin.
	ReadAsset(pluginKey, path string) (data []byte, contentType string, err error)
}

// PluginRegistry exposes the current generation and change notifications.
type PluginRegistry interface {
	Current() Generation
	// OnChange is invoked (synchronously, in order) after each switch.
	OnChange(fn func(Generation)) (cancel func())
}

// ActivePluginKeys lists the plugins of the current generation, for SQL
// filters that hide accounts of disabled or uninstalled plugins
// (`plugin_key = ANY($n)`). It is nil — meaning "do not filter" — when reg is
// nil or no generation is loaded yet.
func ActivePluginKeys(reg PluginRegistry) []string {
	if reg == nil {
		return nil
	}
	g := reg.Current()
	if g == nil {
		return nil
	}
	ps := g.Plugins()
	keys := make([]string, 0, len(ps))
	for _, p := range ps {
		keys = append(keys, p.Key)
	}
	return keys
}

// ProtocolConverters (owner: gateway) reports which protocol pairs the core
// can convert (ARCHITECTURE 6.6): a client endpoint speaking clientProtocol
// can be served by an account type whose upstream speaks upstreamProtocol,
// with the quality and through the protocols Conversion describes.
type ProtocolConverters interface {
	Conversion(clientProtocol, upstreamProtocol string) (Conversion, bool)
}
