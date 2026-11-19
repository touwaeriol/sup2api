import { describe, expect, it } from 'vitest'
import { effectScope } from 'vue'
import { useForm } from '@sub2api/ui'
import { currentSettings, displayValues, JEV_KEY, JEV_LIMITS, LEGACY_KEY, pageKeys, RETIRED, SECTIONS, sectionsOf, sentValue, SETTINGS, settingDefault, settingRules, settingsField, settingsKey, specOf } from './settingsForm'
import { NAV_GROUPS } from './nav'
import { mergeSettings, type PageValues } from './settingsMerge'

const MASK = '******'
/** Keys removed (0.11.0) that settings stored before may still hold. */
const REMOVED = ['allowlist_user_ids', 'blocklist_user_ids', 'exempt_user_ids', 'base_url', 'model', 'categories', 'group_ids', 'retention_days', 'on_error', 'analysis_channel_id', 'cache_ttl_seconds', 'ban_window_hours']
/** The order of the 39 keys of the settings form removed in 0.12.2 (forms/settings.ui.json ui:order). */
const ORDER = [
  'mode', 'fail_policy', 'verdict_cache_days', 'hit_replay', 'cache_entries', 'min_chars', 'input_max_chars', 'input_max_chunks', 'input_overflow', 'input_max_images',
  'auto_watch', 'watch_days', 'escalate_violations', 'escalate_window_seconds', 'ban_threshold', 'ban_window_seconds', 'ban_duration_hours', 'jev_default_set',
  'block_status', 'block_message', 'record_pass', 'record_overturned', 'record_errors', 'store_text', 'record_pass_input_message', 'error_text_max_chars',
  'hit_retention_days', 'overturned_retention_days', 'error_retention_days', 'pass_retention_days', 'stats_retention_days', 'max_concurrency', 'queue_size',
  'max_inflight_mb', 'ttft_timeout_ms', 'error_rate_alert_percent', 'notify_webhook_url', 'notify_webhook_secret', 'api_key'
]

describe('the settings', () => {
  it('are the 39 keys, every one but the 0.1 API key on a page, each once, in the order of the settings form', () => {
    expect(Object.keys(SETTINGS).sort()).toEqual([...ORDER].sort())
    const shown = SECTIONS.flatMap((g) => g.keys)
    expect(new Set(shown).size).toBe(shown.length)
    expect(shown).toEqual(ORDER.filter((k) => k !== LEGACY_KEY))
    expect(specOf(LEGACY_KEY).kind).toBe('hidden')
  })

  it('puts every section on a settings page of the sidebar, and every such page has a section', () => {
    const pages = NAV_GROUPS.flatMap((g) => g.items.filter((i) => i.access === 'settings').map((i) => i.key as string))
    expect(SECTIONS.map((g) => `${g.page}/${g.id}`)).toEqual([
      'policy/policy',
      'cache/cache',
      'cache/input',
      'lists/watch',
      'jev/jev',
      'response/response',
      'records/records',
      'records/retention',
      'advanced/performance',
      'advanced/alerts'
    ])
    expect([...new Set(SECTIONS.map((g) => g.page as string))].sort()).toEqual([...pages].sort())
    for (const p of pages) expect(sectionsOf(p).length).toBeGreaterThan(0)
  })

  it('shows the 0.8.0 settings where the design puts them', () => {
    expect(SECTIONS[0].keys).toEqual(['mode', 'fail_policy'])
    expect(pageKeys('lists')[0]).toBe('auto_watch')
    const shown = SECTIONS.flatMap((g) => g.keys)
    expect(shown[shown.indexOf('store_text') + 1]).toBe('record_pass_input_message')
    expect(SECTIONS.find((g) => g.keys.includes('verdict_cache_days'))?.keys).toContain('cache_entries')
    // the ban window in seconds, with the duration control like the escalation window
    expect(pageKeys('lists')).toContain('ban_window_seconds')
    expect(specOf('ban_window_seconds').kind).toBe('duration')
  })

  it('lists the new key of each retired one, and no longer the removed keys', () => {
    for (const [old, r] of Object.entries(RETIRED)) {
      expect(SETTINGS).not.toHaveProperty(old)
      expect(SETTINGS).toHaveProperty(r.key)
    }
    for (const k of REMOVED) expect(SETTINGS).not.toHaveProperty(k)
  })
})

describe('currentSettings', () => {
  it('drops every key the plugin does not accept, and keeps the others', () => {
    const stored = { mode: 'enforce', notify_webhook_secret: MASK, allowlist_user_ids: ['7'], exempt_user_ids: ['7'], base_url: 'https://old.example', analysis_channel_id: 5 }
    expect(currentSettings(stored)).toEqual({ mode: 'enforce', notify_webhook_secret: MASK })
  })

  it('drops the 0.1 API key, masked or not', () => {
    for (const v of [MASK, 'sk-old']) expect(currentSettings({ mode: 'enforce', [LEGACY_KEY]: v })).toEqual({ mode: 'enforce' })
  })

  it('folds cache_ttl_seconds into the cache days never stored: 0 = off, else whole days rounded up, at most 7', () => {
    const days = (ttl: unknown, s: Record<string, unknown> = {}) => currentSettings({ ...s, cache_ttl_seconds: ttl }).verdict_cache_days
    expect(days(0)).toBe(0)
    expect(days(600)).toBe(1)
    expect(days(86400)).toBe(1)
    expect(days(86401)).toBe(2)
    expect(days(604800)).toBe(7)
    expect(days(604801)).toBe(7)
    expect(days(86400 * 90)).toBe(7)
    expect(days(600, { verdict_cache_days: 14 })).toBe(14)
    expect(days('x')).toBeUndefined()
  })

  it('folds ban_window_hours into ban_window_seconds never stored', () => {
    expect(currentSettings({ ban_window_hours: 12 })).toEqual({ ban_window_seconds: 43200 })
    expect(currentSettings({ ban_window_hours: 12, ban_window_seconds: 600 })).toEqual({ ban_window_seconds: 600 })
  })

  it('folds retention_days into hit_retention_days never stored: never below its default, at most its maximum', () => {
    expect(specOf('hit_retention_days')).toMatchObject({ default: 180, max: 3650 })
    const days = (v: unknown, s: Record<string, unknown> = {}) => currentSettings({ ...s, retention_days: v }).hit_retention_days
    expect(days(30)).toBe(180)
    expect(days(400)).toBe(400)
    expect(days(9999)).toBe(3650)
    expect(days(400, { hit_retention_days: 20 })).toBe(20)
    expect(days('x')).toBeUndefined()
  })

  it('leaves settings already current as they are', () => {
    const s = { mode: 'observe', verdict_cache_days: 3, ban_window_seconds: 7200 }
    expect(currentSettings(s)).toEqual(s)
  })
})

describe('the save of the settings tab', () => {
  /** Settings stored by an older version: current keys, retired keys and removed ones. */
  const raw = {
    mode: 'observe',
    watch_days: 14,
    notify_webhook_url: MASK,
    notify_webhook_secret: MASK,
    api_key: MASK,
    base_url: 'https://old.example',
    categories: [{ id: 'illegal', description: 'x' }],
    blocklist_user_ids: ['8'],
    analysis_every_violations: 30,
    cache_ttl_seconds: 3600,
    ban_window_hours: 12,
    retention_days: 400
  }
  // the page loads, and saves over, the stored values brought to the current keys
  const stored = currentSettings(raw)
  const keys = SECTIONS.flatMap((g) => g.keys)
  const form = displayValues(stored, keys)
  const page = (shown: Record<string, unknown>): PageValues => Object.fromEntries(keys.map((k) => [k, sentValue(k, shown[k])]))
  const initial = page(form)
  const save = (edit: Record<string, unknown>) => mergeSettings(keys, stored, stored, initial, page({ ...form, ...edit }))
  const RETIRED_KEYS = Object.keys(RETIRED)

  it('shows the folded values of the retired keys, and defaults for keys never stored', () => {
    expect(form.verdict_cache_days).toBe(1)
    expect(form.ban_window_seconds).toBe(43200)
    expect(form.hit_retention_days).toBe(400)
    expect(form.block_status).toBe(403)
    expect(form.auto_watch).toBe(true)
    expect(form.notify_webhook_secret).toBe(MASK)
  })

  it('saves the settings brought to the current keys even when nothing was edited', () => {
    const m = save({})
    expect(m.changed).toEqual([])
    expect(m.values).toEqual({ mode: 'observe', watch_days: 14, notify_webhook_url: MASK, notify_webhook_secret: MASK, verdict_cache_days: 1, ban_window_seconds: 43200, hit_retention_days: 400 })
    // differs from what is stored, so the page PUTs it (SettingsTab save)
    expect(JSON.stringify(m.values)).not.toBe(JSON.stringify(raw))
  })

  it('writes the changed keys, leaving every retired and removed key absent', () => {
    const m = save({ mode: 'enforce', verdict_cache_days: 3 })
    expect(m.changed.sort()).toEqual(['mode', 'verdict_cache_days'])
    expect(m.values).toEqual({ ...stored, mode: 'enforce', verdict_cache_days: 3 })
    for (const k of [...RETIRED_KEYS, ...REMOVED, LEGACY_KEY]) expect(m.values).not.toHaveProperty(k)
  })

  it('removes cleared cache days: the retired key is gone, so the default applies', () => {
    const m = save({ verdict_cache_days: null })
    expect(m.changed).toEqual(['verdict_cache_days'])
    expect(m.values).not.toHaveProperty('verdict_cache_days')
    expect(m.values).not.toHaveProperty('cache_ttl_seconds')
  })

  it('shows a cache turned off by the retired key as 0 days', () => {
    expect(displayValues(currentSettings({ cache_ttl_seconds: 0 }), ['verdict_cache_days'])).toEqual({ verdict_cache_days: 0 })
  })

  it('removes a number left empty, an empty webhook address and a cleared secret, so their defaults apply', () => {
    const m = save({ watch_days: undefined, notify_webhook_url: '  ', notify_webhook_secret: '' })
    expect(m.changed.sort()).toEqual(['notify_webhook_secret', 'notify_webhook_url', 'watch_days'])
    for (const k of m.changed) expect(m.values).not.toHaveProperty(k)
    expect(sentValue('watch_days', '')).toBeUndefined()
    expect(sentValue('block_message', '')).toBe('')
    expect(sentValue('notify_webhook_url', ' https://x ')).toBe('https://x')
  })
})

describe('the defaults', () => {
  // The defaults of forms/settings.schema.json (removed in 0.12.2); the plugin's DefaultSettings holds the same.
  it('are those of the settings form', () => {
    expect(Object.fromEntries(Object.keys(SETTINGS).map((k) => [k, settingDefault(k)]))).toEqual({
      mode: 'off', fail_policy: 'open', block_status: 403, block_message: 'Your request was blocked by content moderation. Please revise your input and try again.',
      auto_watch: true, watch_days: 30, escalate_violations: 0, escalate_window_seconds: 3600, ban_threshold: 0, ban_window_seconds: 86400, ban_duration_hours: 24,
      error_rate_alert_percent: 5, notify_webhook_url: '', notify_webhook_secret: '', record_pass: false, record_overturned: true, record_errors: true, store_text: true,
      record_pass_input_message: true, hit_retention_days: 180, overturned_retention_days: 30, error_retention_days: 7, pass_retention_days: 3, stats_retention_days: 90,
      max_concurrency: 16, queue_size: 1000, ttft_timeout_ms: 10000, verdict_cache_days: 7, hit_replay: 'new_content', cache_entries: 100000, input_max_chars: 12000,
      input_max_chunks: 8, input_overflow: 'sample', input_max_images: 8, max_inflight_mb: 128, error_text_max_chars: 2000, min_chars: 2, jev_default_set: null, api_key: ''
    })
  })
})

describe('the question set rules', () => {
  const t = (k: string, p?: Record<string, unknown>) => `${k}${p ? ' ' + JSON.stringify(p) : ''}`
  const row = { category: 'violence', label: '', question: 'q?', answer_yes: '', answer_no: '', threshold: 0.5 }
  const OPENAI = ['violence', 'hate']
  const set = (over: Record<string, unknown> = {}, rows = [row]) => ({ preamble: '', items: rows, ...over })
  /** The messages of the page's rules on a question set, by form path. */
  async function issues(v: unknown, openai = OPENAI): Promise<Record<string, string>> {
    const scope = effectScope()
    const form = scope.run(() => useForm({ model: () => ({ [JEV_KEY]: v }), update: () => {}, rules: settingRules([JEV_KEY], t, openai) }))!
    await form.validate()
    scope.stop()
    return { ...form.errors }
  }
  const cell = (i: number, field: string) => `${JEV_KEY}.items.${i}.${field}`

  it('accepts the built-in set (null) and a valid table, thresholds typed as text too', async () => {
    expect(await issues(null)).toEqual({})
    expect(await issues(set({}, [{ ...row, threshold: '1' as unknown as number }]))).toEqual({})
  })
  it('refuses what the settings form refused, the number of questions at the set and the rest at its cell', async () => {
    expect(await issues(set({}, []))).toEqual({ [JEV_KEY]: 'settingsTab.jev.items {"min":1,"max":32}' })
    const many = Array.from({ length: JEV_LIMITS.items.max + 1 }, (_, i) => ({ ...row, category: `c${i}`, label: 'L' }))
    expect(await issues(set({}, many))).toEqual({ [JEV_KEY]: 'settingsTab.jev.items {"min":1,"max":32}' })
    expect(await issues(set({ preamble: 'x'.repeat(2001) }))).toEqual({ [`${JEV_KEY}.preamble`]: 'settingsTab.jev.preamble {"n":2000}' })
    const at = async (i: number, field: string, over: Record<string, unknown>, message: string) =>
      expect(await issues(set({}, [row, { ...row, category: 'hate', ...over }]))).toEqual({ [cell(i, field)]: message })
    await at(1, 'category', { category: 'Bad' }, 'settingsTab.jev.category')
    await at(1, 'category', { category: 'x'.repeat(65) }, 'settingsTab.jev.category')
    await at(1, 'category', { category: '' }, 'settingsTab.jev.category')
    await at(1, 'label', { label: 'x'.repeat(65) }, 'settingsTab.jev.label {"n":64}')
    await at(1, 'question', { question: '' }, 'settingsTab.jev.question {"n":2000}')
    await at(1, 'question', { question: 'x'.repeat(2001) }, 'settingsTab.jev.question {"n":2000}')
    await at(1, 'answer_yes', { answer_yes: 'x'.repeat(1001) }, 'settingsTab.jev.answer {"n":1000}')
    await at(1, 'answer_no', { answer_no: 'x'.repeat(1001) }, 'settingsTab.jev.answer {"n":1000}')
    for (const threshold of [0, 1.01, Number.NaN, '']) await at(1, 'threshold', { threshold }, 'settingsTab.jev.threshold')
  })
  it('refuses what JevQuestions.Validate refuses: a repeated category, a custom one without a label, a blank question', async () => {
    expect(await issues(set({}, [row, { ...row, category: 'hate' }, row]))).toEqual({ [cell(2, 'category')]: 'settingsTab.jev.duplicate {"first":1}' })
    expect(await issues(set({}, [row, { ...row, category: 'custom/x' }]))).toEqual({ [cell(1, 'label')]: 'settingsTab.jev.labelRequired' })
    expect(await issues(set({}, [{ ...row, category: 'custom/x', label: ' \t' }]))).toEqual({ [cell(0, 'label')]: 'settingsTab.jev.labelRequired' })
    expect(await issues(set({}, [{ ...row, category: 'custom/x', label: 'X' }]))).toEqual({})
    expect(await issues(set({}, [row]), [])).toEqual({ [cell(0, 'label')]: 'settingsTab.jev.labelRequired' })
    expect(await issues(set({}, [{ ...row, question: ' \n ' }]))).toEqual({ [cell(0, 'question')]: 'settingsTab.jev.question {"n":2000}' })
    // as checkJevRow: a bad category is not checked for repeats, nor asks for a label
    expect(await issues(set({}, [{ ...row, category: 'Bad' }, { ...row, category: 'Bad' }]))).toEqual({ [cell(0, 'category')]: 'settingsTab.jev.category', [cell(1, 'category')]: 'settingsTab.jev.category' })
  })
})

describe('settingsField', () => {
  it('keeps the path of a cell of the question set, and maps every other to its settings key', () => {
    expect(settingsField('values/jev_default_set/items/2/label')).toBe('jev_default_set.items.2.label')
    expect(settingsField('values/jev_default_set/preamble')).toBe('jev_default_set.preamble')
    expect(settingsField('values/jev_default_set/items')).toBe('jev_default_set')
    expect(settingsField('values/jev_default_set/items/3')).toBe('jev_default_set')
    // a field the editor has no cell for (a stored table edited by hand)
    expect(settingsField('values/jev_default_set/items/0/legacy')).toBe('jev_default_set')
    expect(settingsField('values/block_status')).toBe('block_status')
  })
})

describe('settingsKey', () => {
  it('maps API field paths to the settings key', () => {
    expect(settingsKey('values/jev_default_set/items/3')).toBe('jev_default_set')
    expect(settingsKey('values.jev_default_set.items[0].question')).toBe('jev_default_set')
    expect(settingsKey('block_status')).toBe('block_status')
  })
})

describe('mergeSettings', () => {
  const KEYS = ['block_status', 'block_message', 'notify_webhook_secret', 'jev_default_set']
  const loaded = { block_status: 403, block_message: 'no', notify_webhook_secret: '******', jev_default_set: null, mode: 'observe' }
  const initial = { block_status: 403, block_message: 'no', notify_webhook_secret: '******', jev_default_set: null }

  it('writes only the keys this page changed, keeping later changes elsewhere', () => {
    const latest = { ...loaded, block_message: 'changed elsewhere', mode: 'enforce', other: 1 }
    const m = mergeSettings(KEYS, loaded, latest, initial, { ...initial, block_status: 451 })
    expect(m.changed).toEqual(['block_status'])
    expect(m.conflicts).toEqual([])
    expect(m.values).toEqual({ ...latest, block_status: 451 })
  })

  it('reports a key changed both here and elsewhere, and this page wins', () => {
    const latest = { ...loaded, block_status: 429 }
    const m = mergeSettings(KEYS, loaded, latest, initial, { ...initial, block_status: 451 })
    expect(m.conflicts).toEqual(['block_status'])
    expect(m.values.block_status).toBe(451)
  })

  it('removes a key emptied here (its default applies)', () => {
    const m = mergeSettings(KEYS, loaded, loaded, initial, { ...initial, block_message: undefined })
    expect(m.changed).toEqual(['block_message'])
    expect('block_message' in m.values).toBe(false)
  })

  it('keeps a stored secret (mask) and changes nothing when nothing was edited', () => {
    const m = mergeSettings(KEYS, loaded, loaded, initial, { ...initial })
    expect(m.changed).toEqual([])
    expect(m.values).toEqual(loaded)
  })

  it('compares structured values by content', () => {
    const set = { preamble: '', items: [{ category: 'a', threshold: 0.5 }] }
    const m = mergeSettings(KEYS, { ...loaded, jev_default_set: set }, { ...loaded, jev_default_set: set }, { ...initial, jev_default_set: set }, {
      ...initial,
      jev_default_set: { preamble: '', items: [{ category: 'a', threshold: 0.5 }] }
    })
    expect(m.changed).toEqual([])
  })
})
