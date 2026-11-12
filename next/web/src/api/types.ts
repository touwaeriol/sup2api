// DTOs of the console REST API (docs/CONTRACTS.md §5). Fields marked
// "assumed" are not spelled out in the contract; views must tolerate their
// absence.
import type { AccountFeaturePolicy, FeatureGroup } from '@/views/ccgateway/requestPolicy'
import type { GroupPricingSummary, MyGroupPricing, SegmentRef } from './pricing'

export type LText = string | Record<string, string>

/** Money is a decimal string ("12.34000000"). */
export type Money = string

// ------------------------------------------------------------------ auth / me

export interface TokenResponse {
  access_token: string
  refresh_token: string
  expires_in: number
  user?: Me
  /** A correct password on an account with two-factor: POST /auth/login/2fa finishes the login. */
  two_factor_required?: boolean
  flow_token?: string
}

export interface Me {
  id: number
  email: string
  display_name: string
  roles: string[]
  permissions: string[]
  superuser: boolean
}

export interface MenuItem {
  id: string
  label: LText
  icon?: string
  path: string
  plugin_key?: string
}

export interface MenuSection {
  section: string | Record<string, string>
  label?: string | Record<string, string>
  items: MenuItem[]
}

// ------------------------------------------------------------------ users, roles, permissions

export interface User {
  id: number
  email: string
  display_name: string
  status: string
  roles?: string[] // assumed
  balance?: Money // assumed
  /** The user group the user is priced by (api/pricing.ts). */
  segment?: SegmentRef
  last_login_at?: string | null
  created_at?: string
}

export interface Role {
  id: number
  key: string
  name: LText
  description?: LText
  builtin: boolean
  superuser: boolean
  permission_keys?: string[]
  user_count?: number
  member_count?: number // deprecated alias
}

export interface PermissionItem {
  key: string
  label: LText
  sensitive: boolean
  status: string // active | disabled | removed
}

export interface PermissionModule {
  module: string
  label: LText
  source: string // core | plugin
  plugin_key?: string | null
  status: string
  permissions: PermissionItem[]
}

// ------------------------------------------------------------------ keys, groups, proxies

/** Caps the owner sets on an API key; 0 = unlimited (CONTRACTS §60). */
export interface KeyLimits {
  rpm_limit: number
  tpm_limit: number
  max_concurrency: number
}

export interface ApiKey extends Partial<KeyLimits> {
  /** Spend cap in USD (decimal string, "0" = unlimited) and what was charged (CONTRACTS §61). */
  quota_limit?: string
  quota_used?: string
  id: number
  user_id?: number
  user_email?: string // assumed
  /** null once the key's group was deleted: the key is kept, unbound, until its owner picks another group. */
  group_id: number | null
  group_name?: string // assumed
  /** Whether the group serves the key (deleted: unbound; unavailable: no longer open to the owner). */
  group_status?: 'active' | 'disabled' | 'unavailable' | 'deleted'
  name: string
  key_prefix: string
  allowed_ips?: string[]
  copyable?: boolean
  status: string
  expires_at?: string | null
  last_used_at?: string | null
  created_at: string
  key?: string // plaintext, only in create or authorized reveal responses
  /** Platforms reachable with this key (same as its group's). */
  platforms?: string[]
  /** Current use across the nodes: this minute's requests and tokens, requests in flight. */
  live?: { rpm: number; tpm: number; concurrency: number }
}

export interface Group extends Partial<GroupPricingSummary>, Partial<MyGroupPricing> {
  id: number
  name: string
  description: string
  status: string
  rate_multiplier: string | number
  visibility: 'public' | 'restricted'
  model_allowlist: string[]
  model_filter_mode?: 'whitelist' | 'blacklist'
  account_count?: number // assumed
  key_count?: number // assumed
  api_key_count?: number // server spelling of key_count
  /**
   * Platforms served by the group: supported by the account types of its
   * accounts (sorted ids). API keys bound to the group can call their endpoints.
   */
  platforms?: string[]
  /** The group layer of the environment variables (CONTRACTS §74.3). */
  environment?: EnvironmentValues
  /** Loss policy for its accounts' converted requests; null leaves it to the accounts (effective default allow). */
  conversion_loss_policy?: ConversionLossPolicy | null
  created_at?: string
}

// ------------------------------------------------------------------ protocol conversion (CONVERT-MATRIX §2.3, §2.4, §7)

/** What a converted request may lose: allow serves and records losses; safe refuses the account when a
 *  tool definition, tool choice or tool call would be lost; strict refuses it on any loss. */
export type ConversionLossPolicy = 'allow' | 'safe' | 'strict'

/** Route grade: native, a converted grade, or local (count_tokens estimated by the core without an upstream). */
export type ConversionGrade = 'native' | 'good' | 'fair' | 'discouraged' | 'local'

export type ConversionLossCategory = 'tool' | 'reasoning' | 'image' | 'other'

/** Paths lost per category; a path prefixed "1:" belongs to the second hop. */
export type ConversionLosses = Partial<Record<ConversionLossCategory, string[]>>

/** An account type refused for the request: by policy (policy, category, path) or not convertible (error). */
export interface ConversionRejection {
  type: string
  policy?: ConversionLossPolicy
  category?: ConversionLossCategory
  path?: string
  error?: string
}

/** usage_logs.conversion: how the request was converted, what was lost and which account types were refused. */
export interface ConversionTrace {
  v: number
  /** Absent when no account served (all refused). */
  grade?: ConversionGrade
  /** Protocols passed through, client first. */
  hops?: string[]
  /** Effective policy of the serving account. */
  policy?: ConversionLossPolicy
  lost?: ConversionLosses
  response_lost?: ConversionLosses
  /** Paths left out over the record's cap. */
  omitted?: number
  rejected?: ConversionRejection[]
}

// ------------------------------------------------------------------ environment variables (CONTRACTS §74.3)

/** A layer an environment variable can be set on. */
export type EnvironmentLayer = 'global' | 'group' | 'account'

/** The values set on one layer; an absent key follows the next layer. */
export type EnvironmentValues = Record<string, number>

/** A variable the core declares (GET /environment/variables). */
export interface EnvironmentVariable {
  key: string
  unit: 'seconds' | 'minutes' | 'mb' | 'count'
  min: number
  max: number
  name: LText
  help: LText
  layers: EnvironmentLayer[]
  /** The process environment variable the global layer falls back to; empty without a global layer. */
  process_env: string
}

// ------------------------------------------------------------------ notifications (CONTRACTS §74.4)

/** A channel a plugin delivers (GET /notifications/channels). */
export interface NotificationChannel {
  id: string
  plugin_key: string
  plugin_name: Record<string, string>
  label: Record<string, string>
  description: Record<string, string>
  recipient: 'email' | 'text'
}

export type NotificationStatus = 'pending' | 'sending' | 'sent' | 'failed'

/** One queued or delivered notification (GET /notifications). */
export interface NotificationRecord {
  id: number
  /** Plugin key, or "core". */
  source: string
  channel: string
  recipients: string[]
  subject: string
  body: string
  severity: 'info' | 'warning' | 'critical'
  status: NotificationStatus
  attempts: number
  last_error: string
  next_attempt_at: string
  created_at: string
  sent_at: string | null
}

// ------------------------------------------------------------------ platforms (CONTRACTS §13)

/** A gateway endpoint declared by a platform. */
export interface PlatformEndpoint {
  id?: string
  method: string
  /** Path pattern; segments may be ":param" or ":param:suffix". */
  path: string
  protocol: string
  /** usage: billed by usage; free: not billed (e.g. count_tokens). */
  billing: 'usage' | 'free' | string
}

/**
 * A platform: built into the core (anthropic, openai, gemini) or declared by
 * an enabled plugin. Endpoints belong to exactly one platform. As listed by
 * GET /me/platforms (CONTRACTS §14.1): every platform available now; any
 * logged-in user may read it.
 */
export interface Platform {
  id: string
  label: LText
  builtin: boolean
  endpoints: PlatformEndpoint[]
}

export interface Proxy {
  id: number
  name: string
  protocol: 'http' | 'https' | 'socks5'
  host: string
  port: number
  username: string
  /** The password is never returned (CONTRACTS §15.4); this tells whether one is stored. */
  has_password?: boolean
  status: string
  account_count?: number
  /** Owner (CONTRACTS §21.2); null for legacy rows, which only all-level keys can see. */
  created_by?: number | null
  /** Email of the owner, kept after the user was deleted. */
  created_by_email?: string | null
  created_at?: string
  updated_at?: string
  real_ip?: string | null
  real_ip_updated_at?: string | null
}

export interface ProxyTestResult {
  ok: boolean
  latency_ms?: number
  ip?: string
  message?: string
}

/** The exit IP's location (GET /proxies/:id/location, POST /proxies/probe). */
export interface ProxyLocation {
  ip: string
  country_code: string
  timezone: string
}

export interface ProxyProbeAttempt {
  protocol: 'http' | 'https' | 'socks5'
  ok: boolean
  status?: number
  latency_ms: number
  message?: string
}

export interface ProxyProbeResult {
  ok: boolean
  protocol?: 'http' | 'https' | 'socks5'
  proxy_url?: string
  ip?: string
  latency_ms?: number
  location?: ProxyLocation
  attempts: ProxyProbeAttempt[]
  proxy_id?: number
  proxy_created: boolean
}

// ------------------------------------------------------------------ accounts

export interface AccountAction {
  id: string
  label: LText
  icon?: string
  plugin_key: string
  account_type?: string
  permission?: string
}

export interface AccountFormRef {
  mode: 'schema' | 'iframe' | 'native'
  page?: string
  component?: string
}

/** An endpoint an account type can serve (CONTRACTS §12, §13). */
export interface AccountTypeEndpoint {
  method: string
  path: string
  /** Protocol of the endpoint (client side). */
  protocol: string
  /** Platform that declares the endpoint. */
  platform: string
  /** true: the type supports the platform; false: served through a core protocol converter. */
  native: boolean
  /** Route grade (native iff native). */
  grade: ConversionGrade
  /** Protocols passed through when converted, client first (three for a two-hop composition). */
  via?: string[]
}

/** A platform an account type declares support for. */
export interface AccountTypePlatform {
  id: string
  label: LText
  builtin: boolean
  /** false: the platform does not exist now (its plugin is not enabled). */
  available: boolean
}

/**
 * An account type is identified by (plugin_key, type). Any plugin can declare
 * account types; each declares the platforms it supports (ARCHITECTURE §6.6).
 */
export interface AccountType {
  icon?: string
  plugin_key: string
  plugin_name: LText
  plugin_version?: string
  asset_base?: string
  trust?: Trust
  type: string
  label: LText
  description?: LText
  creation_group?: string
  auth_method_label?: LText
  form: AccountFormRef
  sensitive_fields: string[]
  /** Plugin suggestions prefilled into a new account's models / model_mapping (CONTRACTS §41). */
  default_models?: string[]
  default_models_by_subscription?: Record<string, string[]>
  default_model_mapping?: Record<string, string>
  default_limits?: AccountDefaultLimits
  /** How the editor shows the common controls (docs/COMMON-CONTROLS.md §1); absent names keep the core behaviour. */
  common_controls?: Partial<Record<CommonControlName, CommonControlDecl>>
  extensions_schema?: { type: 'object'; properties: Record<string, unknown> }
  lifecycle?: AccountLifecycle
  /** Platforms the type supports (built-in or plugin platforms). */
  platforms: AccountTypePlatform[]
  /** Endpoints of available supported platforms (native) plus converted ones. */
  endpoints: AccountTypeEndpoint[]
  /** Client headers the type receives: the names an upstream template may read as {client_header.<name>}. */
  pass_headers: string[]
  /** Resolved upstream declaration of a declarative type (docs/UPSTREAM-DECLARATIVE.md §4.4; without the account layer). */
  upstream?: Upstream
}

/** manifest.HeaderWrite: exactly one mode, its value a template. */
export type UpstreamHeaderWrite = Partial<Record<'set' | 'default' | 'append', string>>

/** manifest.UpstreamAuth: a header or query parameter carrying the credential, or none. */
export interface UpstreamAuth {
  type?: 'header' | 'query' | 'none'
  name?: string
  value?: string
}

/** manifest.UpstreamRoute: the upstream request of one protocol. */
export interface UpstreamRoute {
  method?: string
  path?: string
  transport?: 'http' | 'websocket'
  headers?: Record<string, UpstreamHeaderWrite | null>
  patches?: Array<{ op: string; path: string; value?: unknown; when?: string }>
  model?: { from?: string; trimPrefix?: string; required?: boolean }
}

/** manifest.Upstream (docs/UPSTREAM-DECLARATIVE.md §2): how the core builds an account type's upstream requests. */
export interface Upstream {
  preset?: string
  extend?: boolean
  accountConfig?: boolean
  baseURL?: { default?: string; required?: boolean; stripSuffixes?: string[] }
  credentials?: { keyValidator?: { prefixes?: string[]; minLength?: number }; texts?: Record<string, string>; fields?: string[] }
  auth?: UpstreamAuth
  headers?: Record<string, UpstreamHeaderWrite | null>
  routes?: Record<string, UpstreamRoute | null>
  defaultProtocol?: string
  filterFields?: string[]
  paramOverride?: unknown
  classify?: string
  test?: { protocol?: string; model?: string; body?: string }
  models?: { path?: string; idPath?: string; stripPrefix?: string } | null
}

/**
 * The account layer of a type declaring accountConfig (settings field
 * `upstream`, docs/UPSTREAM-DECLARATIVE.md §2.3): merged over the type's
 * declaration like a JSON merge patch, null deletes.
 */
export interface UpstreamAccountLayer {
  auth?: { type?: UpstreamAuth['type']; name?: string | null; value?: string | null }
  headers?: Record<string, UpstreamHeaderWrite | null>
  routes?: Record<string, { path: string }>
  /** filterFields the account lets through. */
  allowFields?: string[]
}

/** manifest.AccountLifecycle: preparation, required lifecycle, row actions and deletion. */
export interface AccountLifecycle {
  preparation?: boolean
  required?: boolean
  actions?: AccountLifecycleAction[]
  deletion?: AccountDeletion
}

/** manifest.AccountLifecycleAction: a plugin action on the account row. */
export interface AccountLifecycleAction {
  id: string
  label: LText
  icon?: string
}

/** manifest.AccountDeletion: runtime resources may be kept 1..30 days after the account is deleted. */
export interface AccountDeletion {
  retentionDays: number
}

/** Caps of a list of client models on one account; its models share one set of counters, 0 = unlimited (CONTRACTS §68, §71). */
export interface ModelLimit {
  models: string[]
  max_concurrency: number
  rpm_limit: number
  tpm_limit: number
  /** Sessions active within spm_window_seconds (CONTRACTS §71). */
  spm_limit: number
  /** Window of spm_limit in seconds, 60–86400; absent = 60. */
  spm_window_seconds?: number
}

/** Usage of one model list, in the order of the account's model_limits. */
export interface ModelRateUsage {
  models: string[]
  in_use: number
  rpm: number
  tpm: number
  /** Sessions within the list's spm_window_seconds. */
  sessions?: number
}

/** Account fields an account type may declare a common control for (manifest accountTypes[].commonControls). */
export const COMMON_CONTROL_NAMES = [
  'schedulable', 'auto_disable', 'priority', 'weight', 'max_concurrency', 'rpm_limit', 'tpm_limit', 'spm_limit', 'spm_window_seconds',
  'model_limits', 'usage_limit', 'model_mapping', 'param_override', 'header_passthrough', 'environment', 'proxy'
] as const
export type CommonControlName = (typeof COMMON_CONTROL_NAMES)[number]

/** One common control declaration; every field is optional. */
export interface CommonControlDecl {
  /** false: the editor hides it (the API still accepts it). Default true. */
  visible?: boolean
  /** false: read-only (a new account gets `default`, a saved one keeps its value). Default true. */
  configurable?: boolean
  /** A new account's value (same shape as the account field). */
  default?: unknown
  /** true: always `default`, for new and saved accounts. */
  locked?: boolean
  /** `proxy` only: the account must have a proxy. */
  required?: boolean
}

/** Scheduling and limits a plugin suggests for a new account (CONTRACTS §68.5); absent fields keep the console defaults. */
export interface AccountDefaultLimits {
  priority?: number
  weight?: number
  max_concurrency?: number
  rpm_limit?: number
  tpm_limit?: number
  spm_limit?: number
  model_limits?: ModelLimit[]
}

/** Usage of an account over some time (CONTRACTS §73); money as decimal strings in USD. */
export interface AccountUsageFigures {
  /** Charged, after the group multiplier. */
  cost: string
  /** Before the group multiplier ("raw ratio"). */
  raw_cost: string
  requests: number
  tokens: number
}

export type UsageLimitPeriod = 'hour' | 'day' | 'week' | 'month' | 'none'

/** The usage limit of an account and what its current period used, in the limit's basis. */
export interface AccountUsageLimitState {
  amount: string
  used: string
  period: UsageLimitPeriod
  period_count: number
  window_start: string | null
  /** When the period resets; null for a lifetime limit ("none"). */
  window_end: string | null
  /** Released by hand in this period: the limit does not disable the account again until the next one. */
  waived: boolean
}

/** Account.usage: the core's usage record of the account (CONTRACTS §73). */
export interface AccountUsage {
  /** Usage counts at the raw ratio: the main figure is raw_cost. */
  raw_cost_accounting: boolean
  total: AccountUsageFigures
  /** Since midnight in the request's ?tz= (default UTC). */
  today: AccountUsageFigures
  /** Null when no usage limit is on. */
  limit: AccountUsageLimitState | null
  /** The account is auto-disabled by its usage limit. */
  limit_disabled: boolean
}

/** One row of GET /accounts/:id/rate-models (CONTRACTS §68.7); no model on the account's own row. */
export interface AccountRateFigures {
  model?: string
  in_use: number
  rpm: number
  tpm: number
  /** Throughput tokens today. */
  tpd: number
  sessions: number
}

/** GET /accounts/:id/rate-models: the account's figures, then each client model's, the busiest first. */
export interface AccountRateModels {
  total: AccountRateFigures
  models: AccountRateFigures[]
}

/** GET /accounts/:id/usage: the details behind the usage cell. */
export interface AccountUsageDetail {
  raw_cost_accounting: boolean
  today: AccountUsageFigures
  last_7_days: AccountUsageFigures
  last_30_days: AccountUsageFigures
  total: AccountUsageFigures
  limit: AccountUsageLimitState | null
}

/** GET/PUT /accounts/:id/usage-limit (CONTRACTS §73). */
export interface AccountUsageLimit {
  raw_cost_accounting: boolean
  enabled: boolean
  amount: string
  period: UsageLimitPeriod
  period_count: number
  /** Periods are counted from here (whole hours, from 2000 up to 2100); null = 2024-01-01 00:00 in `timezone`. */
  anchor: string | null
  /** IANA time zone of the periods' boundaries ("" while never saved; an older core sends "UTC"); the console saves the browser's. */
  timezone: string
  /** Whether the account ever saved a limit (sent by GET; ignored by PUT); absent from an older core. */
  configured?: boolean
}

/** Success-rate figure of an account (CONTRACTS §73.1): the upstream's 400s, cancellations and local failures are not failures. */
export interface AccountOutcomes {
  success: number
  failed: number
}

export interface Account {
  cc_feature_policy?: AccountFeaturePolicy
  lifecycle_sync?: { status: 'pending' | 'applied' | 'failed'; code?: string }
  id: number
  name: string
  /** Plugin that declares the account type. */
  plugin_key: string
  type: string
  type_label?: LText
  group_ids: number[]
  groups?: Array<{ id: number; name: string }>
  proxy_id: number | null
  priority: number
  /** Weighted random order inside one priority (1–1000, default 1, CONTRACTS §18.1). */
  weight: number
  max_concurrency: number
  schedulable: boolean
  /** Lets the gateway disable the account on credential or quota failures; off only cools it down (CONTRACTS §42). */
  auto_disable: boolean
  /** Models this account can serve; empty = all models. */
  models: string[]
  /** Null/absent follows the account plugin's default. */
  auto_models?: boolean | null
  /** Client model -> upstream model, rewritten before the plugin builds the request. */
  model_mapping: Record<string, string>
  /** 0 = unlimited. */
  rpm_limit: number
  tpm_limit: number
  /** Session cap: sessions active within spm_window_seconds by sticky session key, 0 = unlimited (CONTRACTS §71). */
  spm_limit: number
  /** Window of spm_limit in seconds, 60–86400; absent = 60. */
  spm_window_seconds?: number
  /** Limits of lists of client models on top of the account's own (CONTRACTS §68). */
  model_limits?: ModelLimit[]
  /** The account layer of the environment variables (CONTRACTS §74.3). */
  environment?: EnvironmentValues
  /** Client headers copied onto upstream requests: "*", "regex:<pattern>" or header names (CONTRACTS §81). */
  header_passthrough?: string[]
  /** Request parameter override in new-api's param_override format (CONTRACTS §84); null when unset. */
  param_override?: Record<string, unknown> | null
  /** Loss policy for converted requests; null inherits the group's. */
  conversion_loss_policy?: ConversionLossPolicy | null
  /** Read-only usage of the last 60 seconds, sessions within each layer's spm_window_seconds (0 when Redis is unavailable), with one entry per model list. */
  rate_usage?: { rpm: number; tpm: number; sessions?: number; models?: ModelRateUsage[] }
  /** Successful requests and failed attempts of the last minute (CONTRACTS §73); null when unavailable. */
  outcomes?: AccountOutcomes | null
  /** The core's usage record and usage limit (CONTRACTS §73); null when unavailable. */
  usage?: AccountUsage | null
  status: string
  status_reason?: string
  auto_disabled_until?: string | null
  credentials?: Record<string, unknown>
  settings?: { locale?: string; timezone?: string; [key: string]: unknown }
  in_use?: number
  /** Requests queueing for a free slot of the account. */
  waiting?: number
  cooldown_until?: string | null
  cooldown_reason?: string // assumed
  orphaned?: boolean
  /** CCGateway only: the account stayed red long enough and its container is stopped; testing or re-authorizing starts it. */
  runtime_parked?: boolean
  /** Owner (CONTRACTS §21.2); null for legacy rows, which only all-level keys can see. */
  created_by?: number | null
  /** Email of the owner, kept after the user was deleted. */
  created_by_email?: string | null
  last_used_at?: string | null
  created_at?: string
  /**
   * Only in the create / update responses (CONTRACTS §21.4): true when the
   * proxy_url of the request created a new proxy instead of reusing one.
   */
  proxy_created?: boolean
  /**
   * Subscription quota windows (5h / 7d / ...) of the account, from the list
   * endpoint; null for types without plan limits (API keys) — nothing is shown.
   */
  quota?: QuotaSnapshot | null
  /**
   * Credential refresh state (CONTRACTS §48); null for types whose
   * credentials do not expire. The core renews them before they expire.
   */
  refresh?: AccountRefresh | null
  /** Outcome of the latest test of the account (any model); null when never tested. */
  last_test?: AccountLastTest | null
  /** Plan the account's runtime reported ("max", "pro" ...), CONTRACTS §59; null while unknown. */
  subscription_type?: string | null
  /** Rate limit tier the runtime reported, as the upstream names it. */
  rate_limit_tier?: string | null
  /** Balance of the account (CONTRACTS §52); null when the type does not support balance. */
  balance?: AccountBalance | null
  /** Plugin-registered custom actions available for this account (CONTRACTS §52). */
  actions?: AccountAction[]
}

// ---------------------------------------------------------------- CCGateway runtimes (docs/CCGATEWAY-DRAFT-RUNTIMES.md)

/**
 * Extra field of POST /accounts for plugin_key=ccgateway, type=managed: the
 * key of an authorized draft runtime the new account adopts (§4). Refused
 * with 400 details.reason = draft_not_found / draft_not_authorized.
 */
export interface CcgAccountCreateExtras {
  ccgateway_runtime?: string
}

/** POST /system/ccgateway/drafts {proxy_id} → 201; PUT drafts/:key {proxy_id} → the same. */
export interface CcgDraft {
  /** `d` + 16 lowercase hex characters. */
  key: string
}

/** GET .../status of a draft or account runtime; status and reason are English codes (ccgateway.status.* / ccgateway.reason.*). */
export interface CcgRuntimeStatus {
  /** Runtime key (the account id, or the draft key it adopted): POST /system/ccgateway/runtimes/:key/worker updates its worker. */
  key?: string
	current_image?: string
	target_image?: string
	image_update_available?: boolean
	target_image_available?: boolean
  status: 'creating' | 'ready' | 'blocked' | 'pending' | (string & {})
  /** Why a blocked runtime is stopped: account_deleted, or account_parked (the account stayed red for park_after_hours). */
  reason?: string
  container?: string
}

/** GET .../health: the Claude login state inside the container. */
export interface CcgRuntimeHealth {
  subscription_type?: string
  healthy: boolean
  logged_in: boolean
  auth_method?: string
  status_source?: 'local_snapshot'
  online_verified?: false
  selection_verified?: false
  credential_present?: boolean
  credential_sources?: Array<'api_key_env' | 'bearer_env' | 'oauth_token_env' | 'stored_oauth' | 'managed_api_key' | 'external_credential_source' | 'credential_helper_configured'>
  credential_source_unresolved?: boolean
  access_token_expired?: boolean
  /** Accounts only: the Claude Code version the Worker observed (e.g. 2.1.292); absent when unknown. */
  cli_version?: string
}

/** POST .../start and GET .../session (null when no login is pending). */
export interface CcgAuthSession {
  session_id: string
  url: string
  expires_at: string
}

/** POST .../sync */
export interface CcgSyncResult {
  synced?: boolean
  status?: string
  reason?: string
}

/**
 * GET /system/ccgateway/runtime: the container images and controller the core
 * expects (published on GHCR) and what the Docker host runs; POST
 * .../runtime/install installs or upgrades them over SSH.
 */
export interface CcgRuntimeImages {
  expected: { app: string; egress: string; controller: string }
  /** null: nothing installed yet. */
  installed: { controller_image: string; app_image: string; egress_image: string; version?: string } | null
  up_to_date: boolean
  /** Why the installed state could not be read (ssh_failed, controller_unhealthy, ...). */
  reason?: string
  /**
   * Images bundled in the enabled ccgateway plugin package (CONTRACTS §53.10), role -> ref;
   * null: the package carries none. Absent from cores older than §53.10.
   */
  bundled?: CcgBundledImages | null
}

export type CcgBundledRole = 'app' | 'egress' | 'controller' | 'gateway'
export interface CcgBundledImages {
  /** ccgateway plugin version the images were built for. */
  version: string
  images: Partial<Record<CcgBundledRole, string>>
}
/** loaded: uploaded, loaded and applied; present: the controller already had the ref (skipped). */
export type CcgBundledStatus = 'loaded' | 'present' | 'failed'
export interface CcgBundledResult {
  role: string
  ref: string
  status: CcgBundledStatus
  reason?: string
}
/** POST /system/ccgateway/runtime/bundled (CONTRACTS §53.10). */
export interface CcgBundledReport {
  results: CcgBundledResult[]
  /** An app image applied with update_existing: the in-place worker update of the existing runtimes (§53.7). */
  workers?: CcgWorkersReport
  runtime?: CcgRuntimeImages
}

/** Outcome of one runtime's in-place worker update (CONTRACTS §53.7); `failed` carries the controller's error code, `unreachable` or `timeout` as reason. */
export type CcgWorkerStatus = 'updated' | 'unchanged' | 'busy' | 'not_running' | 'rolled_back' | 'failed'
export interface CcgWorkerResult {
  key: string
  /** Absent for a draft runtime no account adopted yet. */
  account_id?: number
  status: CcgWorkerStatus
  sha256?: string
  previous_sha256?: string
  reason?: string
  cli_version?: string
  cli_patch?: string
  cli_sha256?: string
  online?: boolean
}
/** POST /system/ccgateway/runtime/workers, POST .../runtimes/:key/worker (one runtime), and `workers` of an app image applied with update_existing; `reason` set (results empty) when the runtimes could not be listed. */
export interface CcgWorkersReport {
  image: string
  /** Set for the update of one runtime (POST /system/ccgateway/runtimes/:key/worker). */
  key?: string
  results: CcgWorkerResult[]
  reason?: string
  components?: string[]
  cli_version?: string
}

/** Image overrides of the CCGateway connection; '' uses the core's fixed reference. `gateway` is the Caddy image of the control panel (§53.3), `guard` the ccgateway guard of a compose install (§76). */
export interface CcgRemoteImages {
  app: string
  egress: string
  controller: string
  gateway?: string
  guard?: string
}

/**
 * Public view of GET/PUT /system/ccgateway/remote-config (CONTRACTS §49.16,
 * §53.2). Secrets are write-only: only has_* flags come back. In `controller`
 * mode host/port are the control panel's HTTPS address and the SSH fields are empty.
 */
export interface CcgRemoteConfig {
  cc_feature_groups?: Record<string, FeatureGroup>
  feature_revision?: number
  /** Always true for configurations saved by this console: per-account containers are the only mode. */
  account_runtimes: boolean
  /** Hours an account stays red before its container is stopped; 0 never stops it (default 3). */
  park_after_hours?: number
  /** controller: connected to a controller endpoint; ssh / local: legacy connections still honored (CONTRACTS §53.9). */
  mode: 'disabled' | 'local' | 'ssh' | 'controller'
  /** Controller endpoint protocol; '' or absent means https (§53.9). */
  scheme?: '' | 'https' | 'http'
  /** Path prefix of the controller behind a reverse proxy: '' (none) or '/seg[/seg…]' (§53.9). */
  base_path?: string
  host: string
  port: number
  user: string
  auth_mode: 'password' | 'private_key'
  host_key_fingerprint: string
  has_password: boolean
  has_private_key: boolean
  has_passphrase: boolean
  has_admin_key: boolean
  has_api_key: boolean
  /** Control panel mode: root certificates are pinned (controller_ca); the PEM itself is never returned. */
  has_controller_ca?: boolean
  /** SHA-256 (lowercase hex) of the DER of the first pinned certificate. */
  controller_ca_fingerprint?: string
  images?: Partial<CcgRemoteImages> | null
  effective_images?: Partial<CcgRemoteImages>
  network?: { pool: string; allocation: 'random' | 'sequential' }
}

/** Latest test of an account, kept on the account (POST /accounts/:id/test). */
export interface AccountLastTest {
  status?: number
  error_code?: string
  error_source?: string
  at: string
  ok: boolean
  latency_ms: number
  model: string
  message: string
}


/** Credential refresh state of an account (CONTRACTS §48). */
export interface AccountRefresh {
  /** Expiry read from the credentials at the last attempt. */
  expires_at: string | null
  last_attempt_at: string | null
  last_success_at: string | null
  /** '' ok, 'auth_rejected' (authorize the account again), 'transient' (retried automatically). */
  error_type: '' | 'auth_rejected' | 'transient'
  error: string
}

/** Balance snapshot of an account (CONTRACTS §52). */
export interface AccountBalance {
  /** Decimal amount as a string (e.g., "1.23"). */
  amount: Money
  /** ISO 4217 currency code (e.g., "USD", "CNY"). */
  currency: string
  /** When the snapshot was taken. */
  updated_at: string
  /** Source of the balance: "active" (explicit query) or "" (passive). */
  source: 'active' | ''
  /** Last query error message if any. */
  error?: string
}

/** Answer of POST /accounts/:id/refresh-credentials. */
export interface CredentialRefreshOutcome {
  refreshed: boolean
  /** Why nothing was attempted, e.g. 'in_progress' or 'changed'. */
  skipped?: string
  error_type?: '' | 'auth_rejected' | 'transient'
  error?: string
  refresh: AccountRefresh
}
/** Upstream verdict of one quota window ("" = not reported). */
export type QuotaWindowStatus = 'allowed' | 'allowed_warning' | 'rejected' | ''

/** One rolling plan window of a subscription account. */
export interface QuotaWindow {
  /** "5h", "7d", "7d_sonnet", "7d_fable"; unknown keys are shown as they are. */
  key: string
  label?: Record<string, string>
  short_label?: Record<string, string>
  /** Used share in percent (0–100, may exceed 100). */
  utilization: number
  utilization_unknown?: boolean
  resets_at: string | null
  expired?: boolean
  status: QuotaWindowStatus
}

/**
 * Quota of a subscription account (GET /accounts/:id/quota, `quota` of a list
 * row). `passive` comes from response headers of served requests, `active`
 * from an explicit upstream query (force refresh, at most every 30 s).
 */
export interface QuotaSnapshot {
  supported: boolean
  source: 'passive' | 'active' | ''
  updated_at: string | null
  /** Last query error; the windows then hold the previous values. */
  error: string
  windows: QuotaWindow[]
  next_check_at?: string | null
  refresh_status?: 'pending' | 'ready' | 'error'
}

/** Token counters the plugin reported for the probe request (all optional). */
export interface AccountTestUsage {
  input_tokens?: number
  output_tokens?: number
  cache_read_tokens?: number
  cache_creation_tokens?: number
}

export interface AccountTestResult {
  test_mode?: 'native_build'
  /** CCGateway: the test ran with the global CC feature policy, not the account's groups. */
  policy_scope?: 'global'
  response_headers?: Record<string, string>
  error_code?: string
  error_source?: string
  ok: boolean
  status: number
  latency_ms: number
  /** Transport error or failure summary. */
  message?: string
  /** Model actually requested, as reported by the plugin (may differ from the input). */
  model?: string
  /** The model the caller asked for (before the account's model mapping); '' for the plugin default. */
  requested_model?: string
  /** Upstream address: scheme://host + path (the server strips the query). */
  upstream?: string
  /** Snippet of the upstream response, present on success and failure (~4 KiB max). */
  body?: string
  usage?: AccountTestUsage
  /** Failure cause classified by the plugin. */
  reason?: string
  /**
   * The account effect the plugin would suggest ('cooldown' | 'disable' | '').
   * A diagnosis only: a test never cools down or disables the account.
   */
  effect?: string
}

// ------------------------------------------------------------------ billing

export type PriceMode = 'per_request' | 'per_token' | 'expression'

/** Prices are global per model (no platform); the result is the base price. */
export interface Price {
  id: number
  model: string
  mode: PriceMode
  config: Record<string, any>
  expression: string
  expr_version: number
  expr_hash: string
  /** manual: entered by an admin; sync: imported from a price source. */
  source: PriceOrigin
  /** Price source the price was imported from; null when manual or the source was deleted. */
  sync_source_id?: number | null
  sync_source_name?: string | null
  synced_at?: string | null
  enabled: boolean
  note: string
  updated_by?: number | null
  updated_at?: string
  /** Detail only: parameters, headers and metrics the expression reads. */
  analysis?: Record<string, unknown>
}

export type PriceOrigin = 'manual' | 'sync'

export type PriceSourceKind = 'litellm' | 'models_dev' | 'sup2api'

/** A price sync source (GET /price-sources). */
export interface PriceSource {
  id: number
  name: string
  kind: PriceSourceKind
  url: string
  has_api_key: boolean
  /** litellm / models_dev: {providers: string[]}; sup2api: {apply_multiplier: boolean}. */
  options: { providers?: string[]; apply_multiplier?: boolean; [k: string]: unknown }
  enabled: boolean
  last_synced_at: string | null
  last_error: string
  price_count: number
  created_at?: string
  updated_at?: string
}

/** A price definition as compared by a sync preview. */
export interface PriceSyncDef {
  mode: PriceMode
  config: Record<string, any>
  expression: string
}

/**
 * create: no local price; update: the local price was synced and differs;
 * manual: the local price is manual and differs (unticked by default);
 * unchanged: identical.
 */
export type PriceSyncAction = 'create' | 'update' | 'manual' | 'unchanged'

export interface PriceSyncItem {
  model: string
  action: PriceSyncAction
  incoming: PriceSyncDef
  current: null | (PriceSyncDef & { id: number; source: PriceOrigin; sync_source_id: number | null; enabled: boolean })
}

/** POST /price-sources/:id/preview */
export interface PriceSyncPreview {
  source_id: number
  fetched_at: string
  total: number
  skipped: number
  items: PriceSyncItem[]
}

/** POST /price-sources/:id/apply */
export interface PriceSyncResult {
  created: number
  updated: number
  unchanged: number
  skipped: Array<{ model: string; reason: string }>
}

export interface PriceValidateResult {
  ok: boolean
  expression: string
  errors: Array<string | { message: string; [k: string]: unknown }>
  warnings: Array<string | { message: string; [k: string]: unknown }>
}

export interface PricePreviewResult {
  cost: Money
  tier: string
  rules: Array<{ cond: string; multiplier: number | string; matched: boolean }>
  breakdown: Record<string, unknown>
}

export interface LedgerEntry {
  id: number
  user_id: number
  user_email?: string // assumed
  delta: Money
  balance_after: Money
  kind: string
  ref_type: string
  ref_id: string
  operator_id?: number | null
  plugin_key?: string | null
  note: string
  created_at: string
}

export interface UsageLog {
  id: number
  request_id: string
  /** Client's X-Request-Id (truncated to 128 characters), empty when absent (CONTRACTS §14.4). */
  client_request_id?: string | null
  user_id: number
  user_email?: string // assumed
  api_key_id: number
  api_key_name?: string
  client_ip?: string
  user_agent?: string
  node_id?: string
  attempts?: number
  group_id: number
  group_name?: string // assumed
  account_id: number | null
  account_name?: string // assumed
  /** Plugin that declares the account type. */
  plugin_key: string
  /** Account type id (within plugin_key). */
  account_type?: string
  /** Platform of the client endpoint. */
  platform: string
  /** Protocol of the client endpoint. */
  protocol: string
  /** Protocol sent upstream; differs from protocol when converted. */
  upstream_protocol?: string
  /** Conversion record (operators only); null when native and nothing was refused. */
  conversion?: ConversionTrace | null
  endpoint: string
  model: string
  upstream_model?: string
  stream: boolean
  status_code: number
  success: boolean
  error_type: string
  error_message?: string
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_creation_tokens: number
  cache_creation_1h_tokens?: number
  total_cost: Money
  rate_multiplier?: string | number
  billing_status: string
  billing_mode?: string
  matched_tier?: string
  price_id?: number | null
  expr_hash?: string
  billing_detail?: Record<string, any>
  hook_decisions?: Array<{ plugin_key: string; hook_id: string; decision: string; latency_ms: number; note?: string }>
  anomalies?: Record<string, string>
  sticky_rule?: string
  sticky_hit?: boolean
  latency_ms: number
  first_token_ms?: number
  /** Request start until the body was read (CONTRACTS §63): a large body from a distant client is part of the first token. */
  upload_ms?: number
  ledger_id?: number | null // assumed
  created_at: string
}

export interface UsageSummaryRow {
  key: string
  requests: number
  success?: number
  input_tokens?: number
  output_tokens?: number
  cache_read_tokens?: number
  cache_creation_tokens?: number
  /** Full prompt including cache; null when historical usage semantics are unknown. */
  prompt_tokens?: number | null
  total_cost: Money
  /** group_by=ip: the newest request from the address. */
  last_at?: string
  /** group_by=user: the user's email. */
  user_email?: string
  /** Rows carrying an observability marker (CONTRACTS §25.4). */
  anomalies?: number
  /** Rows billed at a plugin's estimate, and what they contribute to total_cost. */
  estimated?: number
  estimated_cost?: Money
  /** input + output + cache read + cache write (CONTRACTS §66). */
  total_tokens?: number
  /** Cost before the group multiplier. */
  standard_cost?: Money
  /** Averages over the successful requests that report a time. */
  latency_ms_avg?: number
  first_token_ms_avg?: number
}

/** One window of GET /me/usage/overview (CONTRACTS §66). */
export interface UsageTotals {
  requests: number
  success: number
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_creation_tokens: number
  prompt_tokens: number | null
  total_cost: Money
  standard_cost: Money
  latency_ms_avg: number
  first_token_ms_avg: number
}

/** GET /me/usage/overview and GET /usage/overview: the dashboard's figures in one answer. */
export interface UsageOverview {
  tz: string
  today: UsageTotals
  yesterday: UsageTotals
  last_30d: UsageTotals
  last_5m: { requests: number; tokens: number }
  /** The caller's keys (self view only). */
  api_keys?: { total: number; active: number }
  /** Operator view with user:read. */
  users?: { total: number; new_today: number; active_today: number }
  /** Operator view with account:read. */
  accounts?: { total: number; available: number; auto_disabled: number; disabled: number }
}

export interface BillingSettings {
	pre_consume_tokens: number
  missing_price_policy: 'reject' | 'free'
  min_balance: Money
  big_cost_warning_usd: Money
  /** What a request pays when its client hangs up mid-answer (CONTRACTS §25.8). */
  client_disconnect_policy: 'precharge' | 'continue' | 'free'
  /** How long the upstream is still read under "continue", 5-1800. */
  client_disconnect_wait_seconds: number
}

/** GET/PUT /settings/gateway (CONTRACTS §8, §14.4). */
export interface GatewaySettings {
  /** Attempts per request incl. failover, 1–10 (default 3). */
  max_attempts: number
  /** Timeout of platform calls on the request hot path, 100–30000 ms (default 2000). */
  platform_call_timeout_ms: number
  /** Default hook timeout when the manifest sets none, 50–2000 ms (default 300). */
  default_hook_timeout_ms: number
  /** Timeout of the per-request platform RPCs that are not hooks (ResolveModel, ExtractUsage), 50–2000 ms (default 300). */
  platform_hotpath_timeout_ms: number
  /** Requests that may queue for a session's bound account when it is full, 0–10000 (default 3); 0 turns this queue off. */
  queue_sticky_max_waiting: number
  /** Longest wait in that queue, 0–600 s (default 120). */
  queue_sticky_wait_seconds: number
  /** Requests that may queue for an account when all candidates are full, 0–10000 (default 100); 0 turns this queue off. */
  queue_fallback_max_waiting: number
  /** Longest wait in that queue, 0–600 s (default 30). */
  queue_fallback_wait_seconds: number
  /** max_tokens of a request converted to Anthropic without an output limit: model name -> tokens, always with "default". */
  claude_default_max_tokens: Record<string, number>
  /** Size cap of one http(s) media file downloaded for Gemini, 1–1024 MB (default 64). */
  media_download_max_mb: number
}

/** The numeric gateway settings (one value each). */
export type GatewayNumberSetting = Exclude<keyof GatewaySettings, 'claude_default_max_tokens'>

/** Core load balancing of new sessions within a tier (GET/PUT /settings/scheduling, CONTRACTS §89). */
export interface SchedulingSettings {
  /** Always "traffic" since 2026-11-12; still sent so the cores before it balance by traffic too. */
  mode: 'traffic'
  /** The usage both the traffic balance and the limit load are computed on. */
  metric: 'rpm' | 'concurrency' | 'tpm' | 'sessions'
  /** Lower the weight as the metric nears the account's limit. */
  limit_load: boolean
  /** Balance by the request's model: the traffic balance compares that model's own usage; with limit_load, also count the model list holding it. */
  model_limits: boolean
}

/** Limits of claude_default_max_tokens, mirrored from the server. */
export const CLAUDE_MAX_TOKENS = { fallbackKey: 'default', fallback: 8192, max: 1048576, models: 200, nameLength: 200 } as const

/** Validation ranges of GatewaySettings, mirrored from the server. */
export const GATEWAY_SETTINGS_RANGES: Record<GatewayNumberSetting, [number, number]> = {
  max_attempts: [1, 10],
  platform_call_timeout_ms: [100, 30000],
  default_hook_timeout_ms: [50, 2000],
  platform_hotpath_timeout_ms: [50, 2000],
  queue_sticky_max_waiting: [0, 10000],
  queue_sticky_wait_seconds: [0, 600],
  queue_fallback_max_waiting: [0, 10000],
  queue_fallback_wait_seconds: [0, 600],
  media_download_max_mb: [1, 1024]
}

// ------------------------------------------------------------------ sticky

// Built-in rules come with the core; plugins take no part in sticky sessions.
export interface StickyRule {
  id: number
  name: string
  source: 'builtin' | 'admin'
  enabled: boolean
  priority: number
  match: { protocols?: string[]; models?: string[]; user_agent_contains?: string[] }
  key_sources: Array<{ type: 'body' | 'header' | 'api_key' | 'user'; path?: string; name?: string }>
  value_regex: string
  ttl_seconds: number
  key_includes: string[]
  on_failure: 'failover' | 'stick'
  // Client headers a matching request forwards upstream ("*", "regex:…" or names).
  pass_headers?: string[]
  // A built-in rule whose definition an administrator edited; upgrades keep it.
  customized?: boolean
}

export interface StickyStats {
  rule: string
  hits: number
  misses: number
  rebinds: number
}

export interface StickySettings {
  failure_policy_managed?: boolean
  enabled: boolean
  default_ttl_seconds: number
  keep_on_account_disabled: boolean
}

// ------------------------------------------------------------------ plugins

export type Trust = 'official' | 'verified' | 'community' | 'unsigned' | string

/** An exclusive resource the plugin declares but this node's generation did not give it. */
export interface ResourceConflict {
  resource: string
  id: string
  reason: string
}

export interface PluginSummary {
  has_settings?: boolean
  /** Non-empty: the plugin is abnormal (another plugin claimed a resource first, or the core holds it). */
  resource_conflicts?: ResourceConflict[]
  key: string
  name: LText
  status: string
  /** Ships with the image: can be disabled, not uninstalled. */
  builtin?: boolean
  status_reason?: string
  active_version?: string | null
  desired_version?: string | null
  publisher?: string
  trust?: Trust
  /** Live nodes of the cluster; nodes that did not report the plugin count as `absent`. */
  node_summary?: { total: number; states: Record<string, number> }
  description?: LText
}

export interface HostPermissionReview {
  id: string
  risk: 'low' | 'medium' | 'high' | 'critical'
  scope?: Record<string, unknown> | null
  reason?: LText
  optional?: boolean
  requires?: string
}

export interface ReviewAccountType {
  id: string
  label: LText
  form_mode?: string
  /** Supported platforms (ids, or manifest objects {platform, ...}). */
  platforms: Array<string | { platform: string; [k: string]: unknown }>
}

/** A new platform declared by the plugin, with its endpoints. */
export interface ReviewPlatform {
  id: string
  label?: LText
  endpoints: PlatformEndpoint[]
}

export interface PluginReview {
  plugin_key: string
  version: string
  name: LText
  publisher: string
  trust: Trust
  signature_status: string
  host_compat_ok: boolean
  host_compat?: string
  capabilities: Array<string | { id: string }>
  /** Derived from platforms[].endpoints. */
  gateway_endpoints: Array<Record<string, any>>
  /** New platforms declared by the plugin (CONTRACTS §13). */
  platforms?: ReviewPlatform[]
  /** Account types declared by the plugin (top level, any plugin). */
  account_types?: ReviewAccountType[]
  hooks: Array<Record<string, any>>
  jobs: Array<Record<string, any>>
  events: Array<string | Record<string, any>>
  routes: Array<Record<string, any>>
  menus: Array<Record<string, any>>
  user_permissions: Array<{ key: string; label: LText; description?: LText; sensitive?: boolean }>
  database?: { schema: string; migrations: string[] } | null
  resources?: Record<string, any> | null
  external_services: string[]
  host_permissions: HostPermissionReview[]
  diff?: { added: string[]; widened: string[]; removed: string[] } | null
}

export interface RolloutNode {
  node_id: string
  boot_id: string
  state: string
  error: string
}

export interface Rollout {
  id: number
  plugin_key: string
  action: string
  from_version: string
  target_version: string
  phase: string
  coordinator: string
  error: string
  nodes: RolloutNode[]
}

export interface UIPluginPage {
  type: 'table' | 'form' | 'iframe' | 'native'
  title?: LText
  source?: string
  columns?: Array<{ key: string; label: LText; format?: string }>
  schema?: string
  submit?: string
  src?: string
  component?: string
  /**
   * Query parameter the `source` route honours as a free-text search (mirrors
   * manifest.Page.Search). A table page renders a search box only when this is
   * set; the typed text is sent as `?<search>=…`.
   */
  search?: string
  /**
   * A table page's "New" button: the form page it opens (manifest
   * Page.Create). The core drops it for callers without its permission.
   */
  create?: { page: string; permission?: string }
  /**
   * The page's sections, shown in the sidebar while the page is open
   * (manifest Page.Nav, UI 3.14): each item opens `?tab=<key>`. The core has
   * already dropped the items the caller may not see.
   */
  nav?: UIPageNav
}

/** A page's sidebar sections (manifest Page.Nav). */
export interface UIPageNav {
  groups: UIPageNavGroup[]
}

export interface UIPageNavGroup {
  key: string
  label: LText
  items: UIPageNavItem[]
}

export interface UIPageNavItem {
  key: string
  label: LText
  icon?: string
}

/**
 * A plugin's declarative account action (manifest `ui.accountActions`,
 * CONTRACTS §23.3 UI 3.6): a row menu entry (and, with `bulk`, a bulk bar
 * entry) that POSTs the account id(s) to the plugin's own `route`.
 */
export interface PluginAccountAction {
  id: string
  label: LText
  icon?: string
  /** Plugin-local permission key, the same as the route's. */
  permission: string
  /** `POST <path>`, one of the plugin's admin routes. */
  route: string
  bulk?: boolean
  /** Confirmation text; `{n}` is the number of accounts. */
  confirm?: LText
  danger?: boolean
  /** Which rows offer it: `editable` (default) only those the caller may update, `all` every row shown; orphaned accounts never. */
  visibleFor?: 'editable' | 'all'
  /** Only rows of these account types of the plugin (UI 3.8; every row when absent). */
  accountTypes?: string[]
}

/** manifest ui.slots[] as GET /ui/plugins hands it down. */
export interface UIPluginSlot {
  slot: string
  component: string
  permission?: string
  /** account.editor.section: the navigation entry. */
  label?: LText
  icon?: string
  /** account.editor.section, account.detail.tabs and account.list.quota (UI 3.8): only for these account types of the plugin (all when absent). */
  accountTypes?: string[]
}

export interface UIPlugin {
  key: string
  version: string
  name?: LText
  asset_base: string
  menus: Array<{ id: string; section: string; label: LText; icon?: string; page: string; permission?: string; order?: number }>
  pages: Record<string, UIPluginPage>
  slots: UIPluginSlot[]
  account_actions?: PluginAccountAction[]
  native_entry: string
  trust: Trust
  host_ui_compat?: string
}

export interface NodeInfo {
  node_id: string
  boot_id: string
  addr: string
  host_version: string
  started_at: string
  last_heartbeat: string
  plugins: Record<string, string | Record<string, any>>
}

export interface Publisher {
  id: number
  name: string
  trust_level: string
  status: string
  created_at: string
  revoked_at?: string | null
  keys?: Array<{ key_id: string; public_key: string; status: string; not_before?: string | null; not_after?: string | null; created_at?: string }>
}

export interface MarketSource {
  id: number
  name: string
  url: string
  enabled: boolean
}

export interface MarketPlugin {
  key: string
  name: LText
  description?: LText
  publisher: string
  trust?: Trust
  versions: MarketVersion[]
  installed_version?: string | null // assumed
  latest_version?: string // assumed
  categories?: string[] // assumed
}

/** A version of a market plugin (CONTRACTS §11.1, §14.3). */
export interface MarketVersion {
  version: string
  url?: string
  sha256?: string
  size?: number
  host_compat?: string
  /** host_compat satisfied by the running core version; absent on old servers. */
  compatible?: boolean
}

// ------------------------------------------------------------------ plugin egress (CONTRACTS §14.2)

/** Egress logs aggregated per destination (GET /plugins/:key/egress `summary`). */
export interface EgressSummaryRow {
  host: string
  port?: number | null
  count: number
  ok?: number
  denied?: number
  errors?: number
  bytes_in: number
  bytes_out: number
  last_at?: string | null
}

/** A host the plugin ever connected to (`domains`). */
export interface EgressDomain {
  host: string
  first_seen_at: string
  last_seen_at: string
  connections: number
  /** First seen within the last 24 hours. */
  new: boolean
}

/** One egress connection; result is "open" while the connection is alive. */
export interface EgressLogRow {
  id?: number
  node_id?: string
  network?: string
  host: string
  port?: number
  started_at?: string
  duration_ms?: number | null
  bytes_in?: number | null
  bytes_out?: number | null
  result?: 'open' | 'ok' | 'denied' | 'error' | string
  error?: string
  closed_at?: string | null
}

export interface EgressReport {
  from?: string
  to?: string
  summary?: EgressSummaryRow[]
  domains?: EgressDomain[]
  items?: EgressLogRow[]
}

/** GET /plugins/retired-keys item (CONTRACTS §55.1). */
export interface RetiredKey {
  key: string
  /** Publisher that owned the key; empty for an unsigned plugin. */
  publisher: string
  retired_at: string
  /** The same publisher installed the key again. */
  installed: boolean
  /** Accounts left behind (not deleted). */
  accounts: number
  /** The plg_<key> schema still exists. */
  schema: boolean
}

/** Response of POST /plugins/retired-keys/:key/release. */
export interface ReleaseRetiredKeyResult {
  accounts_deleted: number
  kv_deleted: number
}

/** Response of DELETE /plugins/:key (CONTRACTS §14.3); 204 on older servers. */
export interface UninstallResult {
  /** Accounts soft-deleted with purge_accounts=true. */
  accounts_deleted?: number
}
