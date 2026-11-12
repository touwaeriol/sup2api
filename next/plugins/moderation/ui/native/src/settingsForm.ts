import { childUI, orderedKeys, uiGet, type JSONSchema, type UISchema } from '@sub2api/ui'

// The settings tab as a whole settings page (CONTRACTS §72.15, §75.5): the
// fields of forms/settings.schema.json in the order and ui:section groups of
// forms/settings.ui.json, without the hidden ones (the converted 0.1 fields
// and the keys retired in 0.8.0, kept in the schema so the core still
// validates and masks them). The values of the keys this page does not edit
// go back as stored (settingsMerge.ts).
//
// Two keys of 0.8.0 replace a retired one that the plugin still reads until
// the new key is stored (settings.go DecodeSettings); the page shows what
// the plugin reads and writes the new key:
//   allowlist_user_ids  ← exempt_user_ids
//   verdict_cache_days  ← cache_ttl_seconds (0 = cache off, else whole days, rounded up, 1–30)
// A new key is never removed while its retired key would be read again
// (sentSetting), and a save of the allowlist or of the blocklist writes "the
// allowlist without the blocklisted users" to the retired exempt_user_ids
// too (withRetiredMirrored): nodes still on 0.7.2 read only that key and do
// not moderate its users at all.

/** Keys with their own editor on the settings tab instead of the schema widget. */
export const CUSTOM_KEYS = ['analysis_channel_id', 'notify_webhook_url', 'notify_webhook_secret', 'jev_default_set'] as const
/** The stored 0.1 API key: hidden, but the page offers to clear it (a kept credential makes moderation:manage re-enter it, §75.1). */
export const LEGACY_KEY = 'api_key'

/** A ui:section group of visible fields; title / description are LocalizedText; page = the settings page showing it (nav.ts). */
export interface SettingSection {
  id: string
  page: string
  title: unknown
  description: unknown
  collapsed: boolean
  keys: string[]
}

export const isHidden = (ui: UISchema | null | undefined, key: string) => uiGet(childUI(ui || undefined, key), 'widget') === 'hidden'

/**
 * The visible fields grouped by ui:section, in ui:order. Fields before the
 * first section form an untitled group (id ""); groups without a visible
 * field are left out.
 */
export function settingSections(schema: JSONSchema | null | undefined, ui: UISchema | null | undefined): SettingSection[] {
  if (!schema?.properties) return []
  const out: SettingSection[] = []
  let cur: SettingSection = { id: '', page: '', title: '', description: '', collapsed: false, keys: [] }
  for (const k of orderedKeys(schema, ui || undefined)) {
    const s = childUI(ui || undefined, k)?.['ui:section']
    if (s) {
      out.push(cur)
      const obj = typeof s === 'object' && !Array.isArray(s) && 'title' in s
      cur = { id: k, page: obj ? String(s.page || '') : '', title: obj ? s.title : s, description: obj ? s.description || '' : '', collapsed: obj && s.collapsed === true, keys: [] }
    }
    if (!isHidden(ui, k)) cur.keys.push(k)
  }
  out.push(cur)
  return out.filter((g) => g.keys.length)
}

/** The page that offers to clear a stored 0.1 API key. */
export const LEGACY_KEY_PAGE = 'policy'

/** The sections of one settings page. */
export const sectionsOf = (sections: SettingSection[], page: string) => sections.filter((g) => g.page === page)

/** The keys a page edits: its visible fields, and on LEGACY_KEY_PAGE the 0.1 API key while one is stored (to clear it). */
export function editableKeys(sections: SettingSection[], stored: Record<string, unknown>, page: string): string[] {
  const keys = sections.flatMap((g) => g.keys)
  return stored[LEGACY_KEY] && page === LEGACY_KEY_PAGE ? [...keys, LEGACY_KEY] : keys
}

/** The keys rendered by the schema widgets (editable keys without a custom editor). */
export const schemaKeys = (keys: readonly string[]) => keys.filter((k) => k !== LEGACY_KEY && !(CUSTOM_KEYS as readonly string[]).includes(k))

const clone = <T>(v: T): T => (v === undefined ? v : JSON.parse(JSON.stringify(v)))

const ALLOWLIST_KEY = 'allowlist_user_ids'
const BLOCKLIST_KEY = 'blocklist_user_ids'
export const CACHE_DAYS_KEY = 'verdict_cache_days'
const RETIRED_ALLOWLIST = 'exempt_user_ids'
const RETIRED_CACHE_TTL = 'cache_ttl_seconds'
const DAY_SECONDS = 86400
/**
 * The permission of each change the AI may decide (prototype, design §9, D6:
 * auto = the AI does it, suggest = the AI proposes it, off = not allowed),
 * the retired switch each replaces and that switch's built-in default. A
 * switch that was on let the AI do the change, one that was off left it to a
 * moderator, so: on = auto, off = suggest. A configured install that never
 * stored a switch ran on its default and keeps doing so; the schema default
 * is for a new install only.
 */
const EFFECT_KEYS: Record<string, { retired: string; wasOn: boolean }> = {
  analysis_upgrade: { retired: 'analysis_allow_upgrade', wasOn: true },
  analysis_downgrade: { retired: 'analysis_allow_downgrade', wasOn: true },
  analysis_remove: { retired: 'analysis_allow_remove', wasOn: false }
}
const CACHE_DAYS_MAX = 30

/** The stored settings turn the verdict cache off: only the retired cache_ttl_seconds = 0 does, until verdict_cache_days is saved. */
export const verdictCacheOff = (stored: Record<string, unknown>) => stored[CACHE_DAYS_KEY] === undefined && stored[RETIRED_CACHE_TTL] === 0

/** What the plugin reads for a key never stored whose retired key is: undefined = nothing (the schema default applies). */
function retiredValue(stored: Record<string, unknown>, k: string): unknown {
  if (k === ALLOWLIST_KEY) return stored[RETIRED_ALLOWLIST]
  const effect = EFFECT_KEYS[k]
  if (effect) {
    const on = stored[effect.retired]
    if (typeof on === 'boolean') return on ? 'auto' : 'suggest'
    // a configured install (anything stored) ran on the switch's default
    return Object.keys(stored).length ? (effect.wasOn ? 'auto' : 'suggest') : undefined
  }
  const ttl = stored[RETIRED_CACHE_TTL]
  if (k !== CACHE_DAYS_KEY || typeof ttl !== 'number' || !(ttl > 0)) return undefined
  return Math.min(CACHE_DAYS_MAX, Math.max(1, Math.ceil(ttl / DAY_SECONDS)))
}

/**
 * The value a schema widget shows for each key: the stored one, else what
 * its retired key gives, else the schema default. The cache days stay empty
 * while the retired key keeps the cache off (the default would look like
 * a running cache).
 */
export function displayValues(schema: JSONSchema | null | undefined, stored: Record<string, unknown>, keys: readonly string[]): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const k of keys) {
    const v = stored[k] !== undefined ? stored[k] : retiredValue(stored, k)
    out[k] = clone(v !== undefined || (k === CACHE_DAYS_KEY && verdictCacheOff(stored)) ? v : schema?.properties?.[k]?.default)
  }
  return out
}

/** A widget value as sent: null, and a blank in a field that is not a string (a cleared number), are undefined, so the default applies. */
function sentValue(schema: JSONSchema | undefined, v: unknown): unknown {
  if (v === undefined || v === null) return undefined
  if (typeof v === 'string' && v.trim() === '' && schema?.type !== 'string') return undefined
  return v
}

/**
 * sentValue of a key of the page. A new key is not removed while the plugin
 * would then read its retired key again instead of the schema default:
 * - an allowlist emptied while the retired exempt_user_ids still holds users
 *   is sent as [];
 * - cleared cache days are sent as the schema default (7) while a retired
 *   cache_ttl_seconds is stored (it would give "off" for 0, else its own
 *   days). The one exception is the field left empty because the retired
 *   key keeps the cache off (verdictCacheOff: nothing was cleared, the page
 *   says the cache is off and it stays off).
 */
export function sentSetting(schema: JSONSchema | undefined, stored: Record<string, unknown>, k: string, v: unknown): unknown {
  const sent = sentValue(schema, v)
  if (sent !== undefined) return sent
  if (k === ALLOWLIST_KEY) {
    const retired = stored[RETIRED_ALLOWLIST]
    return Array.isArray(retired) && retired.length ? [] : undefined
  }
  if (k === CACHE_DAYS_KEY && stored[RETIRED_CACHE_TTL] !== undefined && !verdictCacheOff(stored)) return schema?.default
  return undefined
}

/**
 * The values to PUT (mergeSettings) with the retired exempt_user_ids made
 * "the allowlist without the blocklisted users" when this save changes
 * either list: nodes still on 0.7.2 (a rollout in progress, or after a
 * rollback) read only the retired list and do not moderate its users at
 * all, so it must hold the allowlisted users, nobody taken off the
 * allowlist and nobody on the blocklist (whom 0.8.0 moderates before the
 * request is sent on, allowlisted or not).
 * - An allowlist removed by this save (its default, empty) empties a stored
 *   retired list and adds none.
 * - A blocklist saved while allowlist_user_ids was never stored: the
 *   allowlist is the retired list itself. When a blocklisted user is on it,
 *   the list is stored as allowlist_user_ids as it is (0.8.0 keeps reading
 *   the same allowlist) and the retired key gets it without those users.
 */
export function withRetiredMirrored(values: Record<string, unknown>, changed: readonly string[]): Record<string, unknown> {
  return withEffectsMirrored(withAllowlistMirrored(values, changed), changed)
}

/** A saved effect key also writes its retired switch (on = auto), for nodes that read only the switch. */
function withEffectsMirrored(values: Record<string, unknown>, changed: readonly string[]): Record<string, unknown> {
  const saved = Object.entries(EFFECT_KEYS).filter(([k]) => changed.includes(k))
  if (!saved.length) return values
  return { ...values, ...Object.fromEntries(saved.map(([k, { retired }]) => [retired, values[k] === undefined ? undefined : values[k] === 'auto'])) }
}

/**
 * The effect keys this save changes whose retired switch someone changed
 * since the page loaded (an older console or node): the mirror would
 * overwrite it, so the save asks first, as for a key of the page.
 */
export function retiredConflicts(changed: readonly string[], loaded: Record<string, unknown>, latest: Record<string, unknown>): string[] {
  return Object.entries(EFFECT_KEYS)
    .filter(([k, { retired }]) => changed.includes(k) && JSON.stringify(loaded[retired]) !== JSON.stringify(latest[retired]))
    .map(([k]) => k)
}

function withAllowlistMirrored(values: Record<string, unknown>, changed: readonly string[]): Record<string, unknown> {
  const allowSaved = changed.includes(ALLOWLIST_KEY)
  if (!allowSaved && !changed.includes(BLOCKLIST_KEY)) return values
  const retired = values[RETIRED_ALLOWLIST]
  const stored = values[ALLOWLIST_KEY]
  // the allowlist 0.8.0 reads: the new key, else (never stored) the retired one
  const allow: unknown[] = Array.isArray(stored) ? stored : !allowSaved && Array.isArray(retired) ? retired : []
  if (!Array.isArray(stored) && retired === undefined) return values
  const block = values[BLOCKLIST_KEY]
  const blocked = new Set((Array.isArray(block) ? block : []).map(String))
  const mirror = allow.filter((id) => !blocked.has(String(id)))
  if (Array.isArray(stored) || allowSaved) return { ...values, [RETIRED_ALLOWLIST]: mirror }
  if (mirror.length === allow.length) return values
  return { ...values, [ALLOWLIST_KEY]: [...allow], [RETIRED_ALLOWLIST]: mirror }
}

/** The settings key of an API field path: "values.<key>[.<path>]", "values/<key>/…" or "<key>". */
export const settingsKey = (path: string) =>
  path
    .replace(/^values(?=[./]|$)/, '')
    .replace(/^[./]/, '')
    .replace(/\//g, '.')
    .split(/[.[]/)[0]
