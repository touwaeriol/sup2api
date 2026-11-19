import { toFieldPath } from '@sub2api/host'
import type { Rule, RuleContext, Rules } from '@sub2api/ui'
import { jevPayload } from './host'
import type { SectionKey } from './nav'
import type { JevQuestions, PluginSettings } from './types'

// The moderation settings (CONTRACTS §72.10) as the settings pages edit
// them (CONTRACTS §72.15, §75.5): every key with its kind, default and
// limits (SETTINGS), and the sections of each settings page in order
// (SECTIONS). Labels, help and option names are the messages
// settingsTab.field.<key>.*, settingsTab.section.<id>.* and
// settingsTab.option.<key>.<value>. The plugin's ValidateSettings
// (settings_check.go) holds the same limits and is the final check; the
// rules here only give feedback while typing.
//
// The plugin cannot rewrite its stored settings, so the page brings them to
// the current keys (currentSettings) before it shows or saves them: a key
// SETTINGS does not list is dropped (ValidateSettings refuses it), and the
// value of a retired key is folded into the key that replaced it while that
// was never stored (RETIRED); the 0.1 API key is dropped too (LEGACY_KEY).
// The values of the keys a page does not edit go back as stored
// (settingsMerge.ts).

/** How a setting is edited. */
export type SettingKind = 'integer' | 'duration' | 'enum' | 'switch' | 'text' | 'url' | 'secret' | 'jev' | 'hidden'
/** Units shown after a number (settingsTab.unit.*). */
export type SettingUnit = 'days' | 'hours' | 'chars' | 'MiB'

export interface SettingSpec {
  kind: SettingKind
  default: unknown
  /** integer / duration: bounds */
  min?: number
  max?: number
  /** text / url / secret: the most characters */
  maxLength?: number
  /** enum: the values, in the order shown */
  options?: readonly string[]
  unit?: SettingUnit
  /** settingsTab.field.<key>.help exists */
  help?: true
  /** settingsTab.field.<key>.details exists (a longer explanation behind a toggle) */
  details?: true
}

export const DEFAULT_BLOCK_MESSAGE = 'Your request was blocked by content moderation. Please revise your input and try again.'
/** notify_webhook_url: empty, or an http(s) address without spaces. */
export const WEBHOOK_URL = /^$|^https?:\/\/\S+$/

const int = (min: number, max: number, def: number, more: Partial<SettingSpec> = {}): SettingSpec => ({ kind: 'integer', min, max, default: def, help: true, ...more })
const days = (min: number, max: number, def: number, more: Partial<SettingSpec> = {}) => int(min, max, def, { unit: 'days', ...more })
const toggle = (def: boolean, more: Partial<SettingSpec> = {}): SettingSpec => ({ kind: 'switch', default: def, help: true, ...more })
const choice = (options: readonly string[], more: Partial<SettingSpec> = {}): SettingSpec => ({ kind: 'enum', options, default: options[0], help: true, ...more })
const NO_HELP = { help: undefined }

/** Every setting (39 keys), the keys the plugin accepts. */
export const SETTINGS = {
  mode: choice(['off', 'observe', 'enforce']),
  fail_policy: choice(['open', 'close_pre']),
  block_status: int(400, 599, 403),
  block_message: { kind: 'text', maxLength: 500, default: DEFAULT_BLOCK_MESSAGE, help: true },
  auto_watch: toggle(true),
  watch_days: days(1, 365, 30),
  escalate_violations: int(0, 1000, 0),
  escalate_window_seconds: { kind: 'duration', min: 1, max: 31536000, default: 3600 },
  ban_threshold: int(0, 1000, 0),
  ban_window_seconds: { kind: 'duration', min: 1, max: 31536000, default: 86400 },
  ban_duration_hours: int(0, 87600, 24, { unit: 'hours' }),
  error_rate_alert_percent: int(0, 100, 5),
  notify_webhook_url: { kind: 'url', maxLength: 1000, default: '', help: true },
  notify_webhook_secret: { kind: 'secret', maxLength: 200, default: '', help: true },
  record_pass: toggle(false),
  record_overturned: toggle(true, NO_HELP),
  record_errors: toggle(true, NO_HELP),
  store_text: toggle(true, { details: true }),
  record_pass_input_message: toggle(true),
  hit_retention_days: days(1, 3650, 180, NO_HELP),
  overturned_retention_days: days(1, 3650, 30, NO_HELP),
  error_retention_days: days(1, 3650, 7, NO_HELP),
  pass_retention_days: days(1, 3, 3, NO_HELP),
  stats_retention_days: int(7, 3650, 90),
  max_concurrency: int(1, 256, 16),
  queue_size: int(1, 100000, 1000, { details: true }),
  ttft_timeout_ms: int(1000, 60000, 10000, { details: true }),
  verdict_cache_days: days(0, 30, 7, { details: true }),
  hit_replay: choice(['new_content', 'whole']),
  cache_entries: int(1000, 1000000, 100000),
  input_max_chars: int(256, 100000, 12000, { unit: 'chars' }),
  input_max_chunks: int(1, 32, 8),
  input_overflow: choice(['sample', 'block']),
  input_max_images: int(0, 32, 8),
  max_inflight_mb: int(16, 4096, 128, { unit: 'MiB' }),
  error_text_max_chars: int(0, 100000, 2000, { unit: 'chars' }),
  min_chars: int(0, 1000, 2, { unit: 'chars' }),
  jev_default_set: { kind: 'jev', default: null, help: true },
  api_key: { kind: 'hidden', maxLength: 1000, default: '' }
} satisfies Record<string, SettingSpec>

export type SettingKey = keyof typeof SETTINGS
const SPECS: Readonly<Record<string, SettingSpec>> = SETTINGS
export const specOf = (key: string): SettingSpec => SPECS[key]

/** The default of a setting. */
export const settingDefault = (key: string): unknown => SPECS[key]?.default

/** A setting as stored, else its default. */
export const settingValue = (s: PluginSettings | null | undefined, key: string): unknown => s?.values?.[key] ?? settingDefault(key)

/** A group of settings on a settings page; id names its messages (settingsTab.section.<id>). */
export interface SettingSection {
  id: string
  page: SectionKey
  /** settingsTab.section.<id>.description exists */
  description?: true
  keys: readonly SettingKey[]
}

/** The sections of the settings pages (nav.ts), each page's in the order shown. */
export const SECTIONS: readonly SettingSection[] = [
  { id: 'policy', page: 'policy', description: true, keys: ['mode', 'fail_policy'] },
  { id: 'cache', page: 'cache', keys: ['verdict_cache_days', 'hit_replay', 'cache_entries'] },
  { id: 'input', page: 'cache', description: true, keys: ['min_chars', 'input_max_chars', 'input_max_chunks', 'input_overflow', 'input_max_images'] },
  { id: 'watch', page: 'lists', keys: ['auto_watch', 'watch_days', 'escalate_violations', 'escalate_window_seconds', 'ban_threshold', 'ban_window_seconds', 'ban_duration_hours'] },
  { id: 'jev', page: 'jev', description: true, keys: ['jev_default_set'] },
  { id: 'response', page: 'response', keys: ['block_status', 'block_message'] },
  { id: 'records', page: 'records', keys: ['record_pass', 'record_overturned', 'record_errors', 'store_text', 'record_pass_input_message', 'error_text_max_chars'] },
  { id: 'retention', page: 'records', keys: ['hit_retention_days', 'overturned_retention_days', 'error_retention_days', 'pass_retention_days', 'stats_retention_days'] },
  { id: 'performance', page: 'advanced', description: true, keys: ['max_concurrency', 'queue_size', 'max_inflight_mb', 'ttft_timeout_ms'] },
  { id: 'alerts', page: 'advanced', keys: ['error_rate_alert_percent', 'notify_webhook_url', 'notify_webhook_secret'] }
]

/** The sections of one settings page. */
export const sectionsOf = (page: string) => SECTIONS.filter((g) => g.page === page)

/** The keys a page edits: the keys of its sections. */
export const pageKeys = (page: string): SettingKey[] => sectionsOf(page).flatMap((g) => g.keys)

/**
 * The stored 0.1 API key: no longer read, listed in the manifest's
 * ui.settings.secrets only so the core keeps masking it in GET /settings.
 * currentSettings drops it, so every save removes it (a key absent from the
 * PUT is deleted; it cannot be typed again, and a caller with only
 * moderation:manage would otherwise be asked to re-enter it, §75.1).
 */
export const LEGACY_KEY = 'api_key'
export const CACHE_DAYS_KEY = 'verdict_cache_days'
const DAY_SECONDS = 86400
/** The old maximum of cache_ttl_seconds, 604800 s, in days. */
const CACHE_TTL_MAX_DAYS = 7
const HOUR_SECONDS = 3600
const HIT_DAYS = { default: Number(SETTINGS.hit_retention_days.default), max: Number(SETTINGS.hit_retention_days.max) }

/** A finite number through f, else undefined (nothing to fold). */
const numberTo = (f: (n: number) => number) => (v: unknown) => (typeof v === 'number' && Number.isFinite(v) ? f(v) : undefined)

/**
 * Retired keys and the key that replaced each, with the conversion of a
 * stored value; the plugin reads them the same way until they are saved
 * (settings.go retiredKeys).
 */
export const RETIRED: Readonly<Record<string, { key: SettingKey; convert: (v: unknown) => unknown }>> = {
  // 0 = cache off, else whole days, rounded up, at most 7 (the old maximum of 604800 s)
  cache_ttl_seconds: { key: CACHE_DAYS_KEY, convert: numberTo((s) => (s > 0 ? Math.ceil(Math.min(s, CACHE_TTL_MAX_DAYS * DAY_SECONDS) / DAY_SECONDS) : 0)) },
  ban_window_hours: { key: 'ban_window_seconds', convert: numberTo((h) => h * HOUR_SECONDS) },
  // 0.1 kept every record retention_days: the hits are kept at least as long
  retention_days: { key: 'hit_retention_days', convert: numberTo((d) => Math.max(HIT_DAYS.default, Math.min(HIT_DAYS.max, Math.round(d)))) }
}

/**
 * The stored settings as the current keys read them: each retired value
 * folded into its new key when that was never stored, then every key
 * SETTINGS does not list dropped, and LEGACY_KEY.
 */
export function currentSettings(stored: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = { ...stored }
  for (const [old, r] of Object.entries(RETIRED)) {
    const v = out[r.key] === undefined ? r.convert(stored[old]) : undefined
    if (v !== undefined) out[r.key] = v
  }
  for (const k of Object.keys(out)) if (k === LEGACY_KEY || !(k in SPECS)) delete out[k]
  return out
}

const clone = <T>(v: T): T => (v === undefined ? v : JSON.parse(JSON.stringify(v)))

/** The value each key shows: the stored one (currentSettings), else its default. */
export function displayValues(stored: Record<string, unknown>, keys: readonly string[]): Record<string, unknown> {
  return Object.fromEntries(keys.map((k) => [k, clone(stored[k] !== undefined ? stored[k] : settingDefault(k))]))
}

/**
 * A shown value as sent; undefined = removed (its default applies): null,
 * a number left empty, an empty webhook address or secret. The webhook
 * address is sent trimmed, the question set as jevPayload makes it (null:
 * the built-in set).
 */
export function sentValue(key: string, v: unknown): unknown {
  if (specOf(key)?.kind === 'jev') return v ? jevPayload(v as JevQuestions) : null
  if (v === undefined || v === null) return undefined
  switch (specOf(key)?.kind) {
    case 'integer':
    case 'duration':
      return typeof v === 'string' && v.trim() === '' ? undefined : v
    case 'url':
      return typeof v === 'string' ? v.trim() || undefined : v
    case 'secret':
      return v === '' ? undefined : v
  }
  return v
}

/** The settings key of an API field path: "values.<key>[.<path>]", "values/<key>/…" or "<key>". */
export const settingsKey = (path: string) =>
  path
    .replace(/^values(?=[./]|$)/, '')
    .replace(/^[./]/, '')
    .replace(/\//g, '.')
    .split(/[.[]/)[0]

export const JEV_KEY = 'jev_default_set'
/** The paths of the question set's preamble and cells ('jev_default_set.items.<i>.<field>', a field the editor has a cell for). */
const JEV_CELL = new RegExp(`^${JEV_KEY}\\.(preamble|items\\.\\d+\\.(category|label|question|answer_yes|answer_no|threshold))$`)

/**
 * The form path of an API field path: the preamble and the cells of the
 * question set keep their whole path (the editor shows each at its cell,
 * jevRules), every other path - an unknown field of a question too - is its
 * settings key.
 */
export function settingsField(path: string): string {
  const p = toFieldPath(path)
  return JEV_CELL.test(p) ? p : settingsKey(path)
}

/** Limits of the question set (the plugin's ValidateSettings checks the same, with JevQuestions.Validate's row rules). */
export const JEV_LIMITS = { items: { min: 1, max: 32 }, preamble: 2000, label: 64, question: 2000, answer: 1000 } as const
const JEV_CATEGORY = /^[a-z0-9/_-]{1,64}$/

type Translate = (key: string, params?: Record<string, unknown>) => string

const blank = (s: unknown) => (typeof s === 'string' ? s.trim() : '') === ''

/** The row of a cell path ('jev_default_set.items.<i>.<field>'): its index, and the table's rows. */
function rowOf(ctx: RuleContext): { i: number; items: JevQuestions['items'] } {
  const i = Number(ctx.path.split('.')[2])
  return { i, items: (ctx.model[JEV_KEY] as JevQuestions).items }
}

/**
 * The rules of the question set, by cell (the editor shows each at its cell,
 * JevQuestionsEditor prop): the number of questions at the set itself, the
 * rest at the preamble and the cells of each question. openaiCategories (GET
 * /jev/default-questions) are the categories that need no label; every
 * other one is custom. A rule on another cell (a repeated category, a label
 * the category requires) is checked again when its own cell is.
 */
function jevRules(t: Translate, openaiCategories: readonly string[]): Rules {
  const L = JEV_LIMITS
  const row = `${JEV_KEY}.items.*`
  const answer: Rule = { max: L.answer, message: () => t('settingsTab.jev.answer', { n: L.answer }) }
  return {
    [JEV_KEY]: {
      validator: (v) => {
        const n = (v as JevQuestions | null)?.items.length
        return n !== undefined && (n < L.items.min || n > L.items.max) ? t('settingsTab.jev.items', L.items) : undefined
      }
    },
    [`${JEV_KEY}.preamble`]: { max: L.preamble, message: () => t('settingsTab.jev.preamble', { n: L.preamble }) },
    [`${row}.category`]: [
      { required: true, pattern: JEV_CATEGORY, message: () => t('settingsTab.jev.category') },
      {
        validator: (v, ctx) => {
          const { i, items } = rowOf(ctx)
          const first = items.findIndex((q) => q.category === v)
          return first < i ? t('settingsTab.jev.duplicate', { first: first + 1 }) : undefined
        }
      }
    ],
    [`${row}.label`]: [
      { max: L.label, message: () => t('settingsTab.jev.label', { n: L.label }) },
      {
        validator: (v, ctx) => {
          const { i, items } = rowOf(ctx)
          const c = items[i].category
          return JEV_CATEGORY.test(c ?? '') && !openaiCategories.includes(c) && blank(v) ? t('settingsTab.jev.labelRequired') : undefined
        }
      }
    ],
    [`${row}.question`]: { required: true, whitespace: true, max: L.question, message: () => t('settingsTab.jev.question', { n: L.question }) },
    [`${row}.answer_yes`]: answer,
    [`${row}.answer_no`]: answer,
    [`${row}.threshold`]: { validator: (v) => (Number(v) > 0 && Number(v) <= 1 ? undefined : t('settingsTab.jev.threshold')) }
  }
}

/** The rule of one key: its kind and limits (the question set's are jevRules'). */
function ruleOf(key: string, t: Translate): Rule | Rule[] {
  const s = specOf(key)
  switch (s.kind) {
    case 'integer':
    case 'duration':
      return { type: 'integer', min: s.min, max: s.max }
    case 'enum':
      return { enum: s.options }
    case 'switch':
      return { type: 'boolean' }
    case 'url':
      return [{ type: 'string', max: s.maxLength }, { validator: (v) => (typeof v === 'string' && !WEBHOOK_URL.test(v.trim()) ? t('settingsTab.invalidUrl') : undefined) }]
  }
  return { type: 'string', max: s.maxLength }
}

/** The rules of a page's keys, the question set's cells too (openaiCategories: jevRules). */
export const settingRules = (keys: readonly string[], t: Translate, openaiCategories: readonly string[]): Rules =>
  Object.assign({}, ...keys.map((k) => (k === JEV_KEY ? jevRules(t, openaiCategories) : { [k]: ruleOf(k, t) })))
