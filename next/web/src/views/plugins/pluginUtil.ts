// Shared helpers and DTOs of the plugin pages (list, consent, detail,
// rollout, market). Shapes not frozen by CONTRACTS.md are typed loosely and
// read tolerantly (camelCase or snake_case).
import type { LText, PluginSummary, Trust, ResourceConflict } from '@/api/types'
import type { Tone } from '@sub2api/ui'
import { useOptionalT } from '@/composables/optionalText'

export type Risk = 'low' | 'medium' | 'high' | 'critical'

/** Copy of sdk/manifest.HostPermissionRisk. */
export const HOST_PERMISSION_RISK: Record<string, Risk> = {
  kv: 'low',
  secrets: 'low',
  prices: 'low',
  config: 'low',
  log: 'low',
  broadcast: 'low',
  'routes.admin': 'medium',
  'routes.user': 'medium',
  events: 'medium',
  jobs: 'medium',
  'ui.menu': 'medium',
  'ui.iframe': 'medium',
  'accounts.read': 'medium',
  lock: 'medium',
  'db.schema': 'high',
  net: 'high',
  'routes.public': 'high',
  'routes.webhook': 'high',
  'gateway.hook': 'high',
  'gateway.endpoint': 'high',
  'platform.register': 'high',
  'accounts.auto_state': 'high',
  'scheduler.rank': 'high',
  'sticky.clear': 'high',
  'notify.send': 'high',
  'gateway.observe': 'high',
  'users.read': 'high',
  'accounts.credentials': 'critical',
  'ledger.credit': 'critical',
  'ledger.debit': 'critical',
  'ui.native': 'critical',
  'users.write': 'critical',
  'db.core_views': 'critical'
}

export const RISK_ORDER: Record<string, number> = { critical: 0, high: 1, medium: 2, low: 3 }

/** Copy of the plugin key rule of sdk/manifest/check (keyRe). */
const PLUGIN_KEY = /^[a-z][a-z0-9_]{1,29}$/

export function isPluginKey(key: string): boolean {
  return PLUGIN_KEY.test(key)
}

export function riskOf(id: string, fallback?: string): Risk {
  const r = (fallback || HOST_PERMISSION_RISK[id] || 'high') as Risk
  return r in RISK_ORDER ? r : 'high'
}

export function riskDotClass(r: string): string {
  switch (r) {
    case 'low':
      return 'bg-emerald-500'
    case 'medium':
      return 'bg-yellow-400'
    case 'high':
      return 'bg-orange-500'
    default:
      return 'bg-red-600'
  }
}

/** The status a plugin is shown with: "abnormal" while it holds resource conflicts. */
export function shownStatus(p: { status: string; resource_conflicts?: ResourceConflict[] | null }): string {
  return p.resource_conflicts?.length ? 'abnormal' : p.status
}

/** One line per conflict: "platform video: <reason>". */
export function conflictLines(p: { resource_conflicts?: ResourceConflict[] | null }): string[] {
  return (p.resource_conflicts || []).map((c) => `${c.resource} ${c.id}: ${c.reason}`)
}

/**
 * Plugin lifecycle / rollout / node state tones, complete on their own (not a
 * patch over the SDK's generic statusTone, whose vocabulary may change):
 * in-flight states are primary, anything unlisted gray.
 */
const PLUGIN_TONES: Record<string, Tone> = {
  enabled: 'success',
  active: 'success',
  ready: 'success',
  running: 'success',
  ok: 'success',
  success: 'success',
  succeeded: 'success',
  continued: 'primary',
  approved: 'success',
  enabling: 'primary',
  upgrading: 'primary',
  preparing: 'primary',
  activating: 'primary',
  pending: 'primary',
  starting: 'primary',
  awaiting_consent: 'warning',
  installed: 'warning',
  failed: 'danger',
  error: 'danger',
  crashed: 'danger',
  rolled_back: 'danger',
  rejected: 'danger',
  revoked: 'danger',
  unavailable: 'danger',
  abnormal: 'danger'
}

/** Badge tone of a plugin state (PluginStatusBadge). */
export function pluginStatusTone(status: string | null | undefined): Tone {
  return PLUGIN_TONES[status || ''] ?? 'gray'
}

/** Localized plugin state label (`plugins.status.*`); unknown values are shown raw. */
export function usePluginStatusLabel(): (status: string | null | undefined) => string {
  const tr = useOptionalT()
  return (status) => {
    const v = status || 'unknown'
    return tr(`plugins.status.${v}`, undefined, v)
  }
}

export function trustTone(t: Trust | undefined | null): Tone {
  switch (t) {
    case 'official':
      return 'success'
    case 'verified':
      return 'primary'
    case 'community':
      return 'gray'
    default:
      return 'danger'
  }
}

/** i18n key suffix for a host permission id ("db.schema" -> "db_schema"). */
export function hpKey(id: string): string {
  return id.replace(/[^a-zA-Z0-9]/g, '_')
}

/** Reads the first present property among several spellings. */
export function pick<T = any>(o: unknown, ...keys: string[]): T | undefined {
  if (!o || typeof o !== 'object') return undefined
  const r = o as Record<string, unknown>
  for (const k of keys) if (r[k] !== undefined && r[k] !== null) return r[k] as T
  return undefined
}

export function asArray<T = any>(v: unknown): T[] {
  return Array.isArray(v) ? (v as T[]) : []
}

export function display(v: unknown): string {
  if (v === null || v === undefined || v === '') return '—'
  if (Array.isArray(v)) return v.map(display).join(', ')
  if (typeof v === 'object') return JSON.stringify(v)
  return String(v)
}

/** Capability ids of a manifest / review (entries may be ids or {id} objects). */
export function capabilityIdsOf(list: unknown): string[] {
  return asArray(list).map((c) => (typeof c === 'string' ? c : String(pick(c, 'id') ?? display(c))))
}

/** "POST /v1/messages (anthropic)" of a gateway endpoint declaration. */
export function endpointText(e: Record<string, any>): string {
  const head = [pick(e, 'method'), pick(e, 'path', 'route')].filter(Boolean).join(' ')
  const proto = pick<string>(e, 'protocol', 'id')
  return head ? (proto ? `${head} (${proto})` : head) : display(e)
}

/**
 * Field errors of PUT /plugins/:key/settings -> SchemaForm paths. Schema
 * errors are JSON pointers under "values" (values/a/0/b), plugin errors may
 * be dotted (values.a.0.b); errors of the whole object come back in `root`.
 */
export function settingsFieldErrors(fields: Record<string, string>): { fields: Record<string, string>; root: string[] } {
  const out: Record<string, string> = {}
  const root: string[] = []
  for (const [k, v] of Object.entries(fields)) {
    const p = k.replace(/^values(?=[./]|$)/, '').replace(/^[./]/, '').replace(/\//g, '.')
    if (p) out[p] = v
    else root.push(v)
  }
  return { fields: out, root }
}

export interface AccountTypeSummary {
  id: string
  label: LText | undefined
  /** Supported platform ids (built-in, this plugin's or other plugins'). */
  platforms: string[]
  platformDeclarations?: Array<{ platform: string; endpoints?: string[] }>
}

/**
 * Account types declared by a plugin: top-level `account_types` of a review
 * or manifest summary (CONTRACTS §13). Platforms may be ids or manifest
 * objects ({platform, requestFields, ...}).
 */
export function accountTypesOf(o: unknown): AccountTypeSummary[] {
  return asArray<Record<string, any>>(pick(o, 'account_types', 'accountTypes')).map((a) => ({
    id: String(pick(a, 'id', 'type') ?? ''),
    label: pick<LText>(a, 'label'),
    platformDeclarations: asArray(pick(a, 'platform_declarations') ?? pick(a, 'platforms')).map((p) => ({
      platform: typeof p === 'string' ? p : String(pick(p, 'platform', 'id') ?? ''),
      endpoints: typeof p === 'string' || pick(p, 'endpoints') == null ? undefined : asArray<string>(pick(p, 'endpoints'))
    })),
    platforms: asArray(pick(a, 'platforms'))
      .map((p) => (typeof p === 'string' ? p : String(pick(p, 'platform', 'id') ?? '')))
      .filter(Boolean)
  }))
}

export interface PlatformSummary {
  id: string
  label: LText | undefined
  endpoints: Array<{ id?: string; method: string; path: string; protocol: string; billing: string }>
}

/** New platforms declared by a plugin (review or manifest `platforms`), with their endpoints. */
export function platformsOf(o: unknown): PlatformSummary[] {
  return asArray<Record<string, any>>(pick(o, 'platforms')).map((p) => ({
    id: String(pick(p, 'id') ?? ''),
    label: pick<LText>(p, 'label'),
    endpoints: asArray<Record<string, any>>(pick(p, 'endpoints')).map((e) => ({
      id: String(pick(e, 'id') ?? ''),
      method: String(pick(e, 'method') ?? ''),
      path: String(pick(e, 'path') ?? ''),
      protocol: String(pick(e, 'protocol') ?? ''),
      billing: String(pick(e, 'billing') ?? 'usage')
    }))
  }))
}

/** First letter used as avatar when the plugin has no icon. */
export function initialOf(name: string, key: string): string {
  const s = (name || key || '?').trim()
  return s.charAt(0).toUpperCase()
}

/** Compares two semver-ish strings; returns <0, 0, >0. */
export function compareVersions(a: string | null | undefined, b: string | null | undefined): number {
  const pa = String(a || '').replace(/^v/, '').split(/[.+-]/)
  const pb = String(b || '').replace(/^v/, '').split(/[.+-]/)
  const n = Math.max(pa.length, pb.length)
  for (let i = 0; i < n; i++) {
    const x = pa[i] ?? '0'
    const y = pb[i] ?? '0'
    const nx = Number(x)
    const ny = Number(y)
    if (Number.isFinite(nx) && Number.isFinite(ny)) {
      if (nx !== ny) return nx - ny
    } else if (x !== y) return x < y ? -1 : 1
  }
  return 0
}

/** Scope object -> list of [key, values] for chip rendering. */
export function scopeEntries(scope: unknown): Array<{ key: string; values: string[] }> {
  if (!scope || typeof scope !== 'object') return []
  const out: Array<{ key: string; values: string[] }> = []
  for (const [k, v] of Object.entries(scope as Record<string, unknown>)) {
    if (v === null || v === undefined) continue
    if (Array.isArray(v)) out.push({ key: k, values: v.map((x) => display(x)) })
    else if (typeof v === 'object') {
      out.push({
        key: k,
        values: Object.entries(v as Record<string, unknown>).map(([k2, v2]) => `${k2}: ${display(v2)}`)
      })
    } else out.push({ key: k, values: [String(v)] })
  }
  return out
}

export function durationMs(start?: string | null, end?: string | null): number | null {
  if (!start || !end) return null
  const d = new Date(end).getTime() - new Date(start).getTime()
  return Number.isFinite(d) && d >= 0 ? d : null
}

export function formatDuration(ms: number | null | undefined): string {
  if (ms === null || ms === undefined) return '—'
  if (ms < 1000) return `${Math.round(ms)}ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`
  return `${Math.floor(ms / 60_000)}m${Math.round((ms % 60_000) / 1000)}s`
}

// ------------------------------------------------------------------ detail DTOs

export interface PluginGrant {
  permission: string
  scope?: Record<string, unknown> | null
  status: string
  plugin_version?: string
  granted_by?: string | number | null
  granted_by_email?: string // assumed
  granted_at?: string | null
}

export interface PluginNode {
  node_id: string
  boot_id?: string
  addr?: string
  last_heartbeat?: string
  state?: Record<string, any> | string | null
}

export interface HookStats {
  calls: number
  denied: number
  timeouts: number
  p99_ms: number
  breaker_open: boolean
}

export interface PluginHook {
  point: string
  id?: string
  order?: number
  failure?: string
  timeout_ms?: number
  needs?: string[]
  stats?: HookStats | null
}

export interface JobRun {
  status: string
  node_id?: string
  started_at?: string
  finished_at?: string | null
  message?: string
}

export interface PluginJob {
  id: string
  title?: Record<string, string>
  scope?: 'cluster' | 'node'
  /** The job's name on the scheduled task page (plugin.<key>.<id>). */
  task?: string
  schedule: string
  timeout_sec?: number
  /** The schedule and timeout the core runs it with (defaults and limits applied). */
  effective_schedule?: string
  effective_timeout_sec?: number
  /** What was cut to the limits, e.g. "timeout 2h0m0s cut to 1h0m0s". */
  adjusted?: string[]
  next_run_at?: string | null // assumed
  last_run?: JobRun | null
}

export interface PluginEvents {
  subscribe?: string[]
  cursor?: number | string | null
  backlog?: number | null
  deadletters?: number | Array<Record<string, any>> | null
}

export interface PluginResources {
  memory_mb?: number | null
  cpu?: number | null
  max_threads?: number | null
  max_open_files?: number | null
}

/** Plugin detail `resources` as the server returns it (CONTRACTS §15.6); older shapes are flat. */
export interface PluginResourcesInfo extends PluginResources {
  requested?: PluginResources
  overrides?: PluginResources
  effective?: PluginResources
  max_memory_mb?: number
}

export interface PluginVersion {
  version: string
  consent_status: string
  created_at?: string
}

export interface PluginDetail {
  key: string
  name: LText
  status: string
  /** Non-empty: the plugin is abnormal (see ResourceConflict). */
  resource_conflicts?: ResourceConflict[]
  /** Ships with the image: can be disabled, not uninstalled. */
  builtin?: boolean
  status_reason?: string
  active_version?: string | null
  desired_version?: string | null
  publisher?: string
  trust?: Trust
  manifest?: Record<string, any> | null
  grants?: PluginGrant[]
  nodes?: PluginNode[]
  hooks?: PluginHook[]
  jobs?: PluginJob[]
  events?: PluginEvents | null
  resources?: PluginResourcesInfo | null
  egress_policy?: string
  versions?: PluginVersion[]
}

/** ccgateway keeps its settings in a core view (settings:read), not in a plugin settings form. */
const CORE_SETTINGS_KEY = 'ccgateway'

/**
 * The page of that core view, a page of system settings (CONTRACTS §90). Its
 * first segment is one the SDK reserves for the console
 * (manifest.ConsoleSegments), so no plugin endpoint can take the address.
 */
export const CCGATEWAY_PAGE = '/system/ccgateway'

/** Whether the plugin's settings are that core view and the caller may open it. */
export function hasCoreSettings(key: string, has: (perm: string) => boolean): boolean {
  return key === CORE_SETTINGS_KEY && has('settings:read')
}

/**
 * The page of system settings that holds a plugin's settings (CONTRACTS §90):
 * the core view's, or the page of the form the console renders (has_settings
 * is false for native settings, §75.5); undefined without either.
 */
export function consoleSettingsPath(p: Pick<PluginSummary, 'key' | 'has_settings'>, has: (perm: string) => boolean): string | undefined {
  if (hasCoreSettings(p.key, has)) return CCGATEWAY_PAGE
  return p.has_settings && p.key !== CORE_SETTINGS_KEY ? pluginSettingsPath(p.key) : undefined
}

/**
 * Where the plugin list opens a plugin's settings: their page of system
 * settings while the plugin is enabled, as the settings view lists it only
 * then, else the settings tab of its detail page.
 */
export function pluginListSettingsPath(p: Pick<PluginSummary, 'key' | 'has_settings' | 'status'>, has: (perm: string) => boolean): string | undefined {
  const page = consoleSettingsPath(p, has)
  return page && p.status !== 'enabled' ? `/plugins/${encodeURIComponent(p.key)}?tab=settings` : page
}

export interface PluginSettings {
  /** schema: the console renders the form; native: the plugin edits them on its own page (ui.settings.page, §75.5). */
  mode?: string
  /** The plugin page (ui.pages key) holding native settings. */
  page?: string
  schema: Record<string, any> | null
  ui_schema?: Record<string, any> | null
  values: Record<string, any> | null
  /** RBAC key of manifest ui.settings.permission: its holders may edit these settings too. */
  permission?: string
}

export const ROLLOUT_RUNNING = new Set(['preparing', 'activating'])
export const ROLLOUT_DONE = new Set(['active', 'failed', 'cancelled', 'rolled_back'])

export function isPendingConsent(s: string | undefined): boolean {
  return s === 'pending' || s === 'awaiting_consent' || s === 'awaiting'
}

/** Settings the console renders as a schema form; native settings, even with a schema, live on the plugin's page (§75.5). */
export function consoleSettings(s: PluginSettings | null | undefined): PluginSettings | null {
  return s && s.schema && (s.mode ?? 'schema') === 'schema' ? s : null
}

/** A plugin's settings form on its own page, a page of system settings (CONTRACTS §90). */
export function pluginSettingsPath(key: string): string {
  return `/plugins/${encodeURIComponent(key)}/settings`
}

/** The plugin page that edits native settings, as a console path; null when the console edits them. */
export function nativeSettingsPath(key: string, s: PluginSettings | null | undefined): string | null {
  if (!s || s.mode !== 'native' || !s.page) return null
  return `/p/${encodeURIComponent(key)}/${encodeURIComponent(s.page)}?tab=settings`
}
