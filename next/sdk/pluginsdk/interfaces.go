package pluginsdk

import (
	"context"
	"encoding/json"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

// Platform is PlatformService ("platform.adapter.v1"): the behaviour of the
// account types a plugin declares that needs code. Besides it a platform
// plugin implements the one path of a gateway request, Execute: an
// UpstreamBuilder, which the SDK runs with ExecuteDefault, or an Executor of
// its own, which takes precedence; Serve refuses a Platform with neither.
// Platform itself validates credentials, classifies upstream errors and
// describes the console's test request.
//
// A plugin implements it for the account types it declares in the
// top-level manifest accountTypes (ARCHITECTURE 6.6). Platforms own the
// gateway endpoints: the core provides the built-in platforms (anthropic,
// openai, gemini) and a plugin may declare new ones in manifest platforms[];
// an account type lists the platforms it serves in accountTypes[].platforms
// (built-in or plugin platforms), so a plugin may declare account types
// without declaring any platform or endpoint.
//
// The host calls it only for accounts of the plugin's own types:
//   - Account.type is the account type id;
//   - Account.platform is the platform the client endpoint belongs to;
//   - RequestMeta.protocol is the upstream protocol the request Execute
//     builds must speak, a protocol of one of the platforms the account type
//     serves; the body handed to Execute is already in it;
//   - RequestMeta.client_protocol is the client endpoint's protocol; it
//     differs from protocol only when the core converts the request and
//     response between the two.
//
// ClassifyError is called by the host for the upstream error of a relay
// that did not wait; ExecuteDefault calls it in process for one that did.
type Platform interface {
	ErrorClassifier
	ValidateCredentials(context.Context, *pluginv1.ValidateCredentialsRequest) (*pluginv1.ValidateCredentialsResponse, error)
	BuildTestRequest(context.Context, *pluginv1.BuildTestRequestRequest) (*pluginv1.BuildTestRequestResponse, error)
}

type ModelAvailabilityChecker interface {
	CheckModelAvailability(context.Context, *pluginv1.CheckModelAvailabilityRequest) (*pluginv1.CheckModelAvailabilityResponse, error)
}

// ModelLister is implemented by platforms whose upstream can list the models
// an account may use (console "fetch models", CONTRACTS §19). Optional: a
// Platform without it answers BuildModelsRequest with UNIMPLEMENTED.
type ModelLister interface {
	BuildModelsRequest(context.Context, *pluginv1.BuildModelsRequestRequest) (*pluginv1.BuildModelsRequestResponse, error)
}

// ModelResolver is implemented by platforms with endpoints whose model is not
// in the request (manifest request.modelSource: "plugin"). The host calls it
// before scheduling — no account exists yet — so it is the plugin declaring
// the PLATFORM that answers, not the one owning the account type. Optional: a
// Platform without it answers ResolveModel with UNIMPLEMENTED, which the host
// turns into 400 "model is required" like any other failure.
type ModelResolver interface {
	ResolveModel(context.Context, *pluginv1.ResolveModelRequest) (*pluginv1.ResolveModelResponse, error)
}

// QuotaReader is implemented by account types whose upstream can report the
// subscription quota of an account (5-hour / weekly windows, CONTRACTS §44)
// and that declare manifest accountTypes[].quota.query. Like ModelLister the
// plugin only describes the request and reads the answer; the host sends it
// through the account's proxy, at most once per account every 30 seconds.
// Optional: a Platform without it answers both with UNIMPLEMENTED and the
// host relies on the quota headers it samples from gateway responses.
type QuotaReader interface {
	BuildQuotaRequest(context.Context, *pluginv1.BuildQuotaRequestRequest) (*pluginv1.BuildQuotaRequestResponse, error)
	ParseQuotaResponse(context.Context, *pluginv1.ParseQuotaResponseRequest) (*pluginv1.QuotaResult, error)
}

// BalanceReader is implemented by account types whose upstream reports the
// balance of an account and that declare manifest accountTypes[].balance
// (CONTRACTS §52). Like QuotaReader the plugin only describes the requests
// (one or more, sent in order) and reads the answers (one per request, in
// the same order); the host checks every URL against the manifest's upstream
// host rules before it sends any, sends them through the account's proxy
// without following redirects, decides when, and only shows the result.
// Optional: a Platform without it answers both with UNIMPLEMENTED and the
// host treats the account type as having no balance.
type BalanceReader interface {
	BuildBalanceRequest(context.Context, *pluginv1.BuildBalanceRequestRequest) (*pluginv1.BuildBalanceRequestResponse, error)
	ParseBalanceResponse(context.Context, *pluginv1.ParseBalanceResponseRequest) (*pluginv1.ParseBalanceResponseResponse, error)
}

// CredentialRefresher is implemented by account types whose credentials
// expire and that declare manifest accountTypes[].refresh (CONTRACTS §48),
// e.g. an OAuth access token renewed from its refresh token. The plugin only
// describes the request and reads the answer; the host decides when,
// sends it through the account's proxy under a cluster-wide lock per account,
// and merges the returned fields into the stored credentials. Optional: a
// Platform without it answers both with UNIMPLEMENTED and nothing is renewed.
type CredentialRefresher interface {
	BuildRefreshRequest(context.Context, *pluginv1.BuildRefreshRequestRequest) (*pluginv1.BuildRefreshRequestResponse, error)
	ParseRefreshResponse(context.Context, *pluginv1.ParseRefreshResponseRequest) (*pluginv1.RefreshResult, error)
}

// Hook mirrors pluginv1.HookServiceServer ("gateway.hook.v1").
type Hook interface {
	OnGatewayRequest(context.Context, *pluginv1.GatewayRequestHookRequest) (*pluginv1.GatewayRequestHookResponse, error)
}

// JobRunner is the job half of pluginv1.AppServiceServer ("app.jobs.v1").
type JobRunner interface {
	RunJob(context.Context, *pluginv1.RunJobRequest) (*pluginv1.RunJobResponse, error)
}

// EventHandler is the event half of pluginv1.AppServiceServer
// ("app.events.v1"). Handlers must be idempotent on Event.Id.
type EventHandler interface {
	OnEvents(context.Context, *pluginv1.OnEventsRequest) (*pluginv1.OnEventsResponse, error)
}

// App mirrors pluginv1.AppServiceServer (jobs and events).
type App interface {
	JobRunner
	EventHandler
}

// HTTP mirrors pluginv1.HTTPServiceServer ("http.routes.v1"). See Router for
// a small dispatcher.
type HTTP interface {
	HandleHTTP(context.Context, *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error)
}

// AccountRanker is a policy extension that rewrites the
// priority/weight of the candidate accounts of a request
// ("scheduler.rank.v1", declared in manifest scheduler.rank). Plugins take
// no part in sticky sessions: the core and the administrator own the rules
// and the session values.
type AccountRanker interface {
	RankAccounts(context.Context, *pluginv1.RankAccountsRequest) (*pluginv1.RankAccountsResponse, error)
}

// NotificationChannel mirrors pluginv1.NotificationServiceServer
// ("notify.channel.v1", declared in manifest notifications): the plugin
// delivers the notifications of the channels it declares. Delivery is at least
// once; an error is retried, a response with `rejected` is final.
type NotificationChannel interface {
	DeliverNotification(context.Context, *pluginv1.DeliverNotificationRequest) (*pluginv1.DeliverNotificationResponse, error)
}

// Migration mirrors pluginv1.MigrationServiceServer ("migration.data.v1").
// The host calls MigrateData once per rollout, on one node, and records it;
// a record lost after a successful call makes it run again, so it MUST be
// idempotent (CONTRACTS §11.7, §36).
type Migration interface {
	MigrateData(context.Context, *pluginv1.MigrateDataRequest) (*pluginv1.MigrateDataResponse, error)
}

// ---------------------------------------------------------------- lifecycle

// Initializer is called once per plugin PROCESS after InitHost succeeded,
// before the first Configure. Use it to open the database and set up local
// state.
//
// In a cluster every node runs its own plugin process, so Init runs once on
// EVERY node, and a goroutine started here runs once per node too. Work that
// must happen once for the whole cluster does not belong here: declare
// periodic work as manifest jobs[] (each trigger runs on one node only), and
// guard other work that must not overlap across nodes with Host.Locks. See
// CONTRACTS §27.4. For upstream async tasks, declare endpoint.task, return
// the task from Execute and implement TaskMonitor: the host persists,
// schedules and serves shared progress; no plugin timer or task polling job
// is needed.
type Initializer interface {
	Init(ctx context.Context, host Host) error
}

// Configurer receives the plugin settings and approved grants. It is called
// after Init and again whenever the administrator changes settings or
// grants. Return a FieldErrors value to reject the configuration field by
// field; any other error rejects it as a whole.
type Configurer interface {
	Configure(ctx context.Context, cfg Config) error
}

// SettingsValidator checks plugin settings before the host stores them
// (PluginService.ValidateSettings, CONTRACTS §75.1). Optional: without it
// the host stores what the console submits and Configure is the only check.
// cfg.JSON holds the submitted settings with masked secrets replaced by the
// stored values; cfg.Grants are the plugin's current grants, as Configure
// gets them. Return a FieldErrors value (fields are JSON pointers below the
// settings, "a/b") to reject field by field, any other error to reject the
// settings as a whole, and a non-empty normalized to store it instead of what
// was submitted. The console shows error messages as they are, so a message
// must not hold a field's value (it may be a secret). Share the decoding and
// checks with Configure so that whatever is stored configures.
type SettingsValidator interface {
	ValidateSettings(ctx context.Context, cfg Config) (normalized json.RawMessage, err error)
}

// HealthChecker overrides the default health report (always healthy once
// InitHost succeeded).
type HealthChecker interface {
	Health(ctx context.Context) (*pluginv1.HealthResponse, error)
}

// Shutdowner is called when the host asks the plugin to stop. The SDK closes
// the database pool afterwards.
type Shutdowner interface {
	Shutdown(ctx context.Context) error
}
