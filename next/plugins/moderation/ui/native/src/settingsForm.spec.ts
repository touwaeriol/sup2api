import { describe, expect, it } from 'vitest'
import schema from '../../../forms/settings.schema.json'
import ui from '../../../forms/settings.ui.json'
import { CUSTOM_KEYS, displayValues, editableKeys, LEGACY_KEY, schemaKeys, sectionsOf, sentSetting, settingSections, settingsKey, verdictCacheOff, withRetiredMirrored } from './settingsForm'
import { NAV_GROUPS } from './nav'
import { mergeSettings, type PageValues } from './settingsMerge'

const MASK = '******'
/** The converted 0.1 fields and the keys retired in 0.8.0: in the schema (the core validates and masks them), never on the page. */
const LEGACY = ['base_url', 'api_key', 'model', 'system_prompt', 'categories', 'tool_choice', 'max_turns', 'temperature', 'max_tokens', 'timeout_ms', 'on_error', 'sample_rate', 'group_ids', 'model_patterns', 'retention_days', 'exempt_user_ids', 'cache_ttl_seconds']

const sections = settingSections(schema, ui)
const zh = (v: unknown) => (v as { zh: string }).zh

describe('settingSections', () => {
  it('groups every field but the 0.1 and retired ones by ui:section, in ui:order', () => {
    expect(sections.map((g) => `${g.page}/${zh(g.title)}`)).toEqual([
      'policy/执行方式',
      'cache/判定缓存与重放',
      'cache/送审输入上限',
      'lists/固定名单',
      'lists/自动观察与封禁',
      'analysis/AI 分析',
      'analysis/分析资源',
      'jev/Jev 默认题集',
      'response/拦截响应',
      'records/记录内容',
      'records/保留期',
      'advanced/性能',
      'advanced/告警与通知'
    ])
    const shown = sections.flatMap((g) => g.keys)
    expect(shown).toEqual(ui['ui:order'].filter((k) => !LEGACY.includes(k)))
    expect(new Set(shown).size).toBe(shown.length)
    expect(Object.keys(schema.properties).filter((k) => !shown.includes(k)).sort()).toEqual([...LEGACY].sort())
    expect(sections.filter((g) => g.collapsed).map((g) => g.id)).toEqual(['analysis_timeout_ms'])
  })

  it('puts every section on a settings page of the sidebar, and every such page has a section', () => {
    const pages = NAV_GROUPS.flatMap((g) => g.items.filter((i) => i.access === 'settings').map((i) => i.key as string))
    expect([...new Set(sections.map((g) => g.page))].sort()).toEqual([...pages].sort())
    for (const p of pages) expect(sectionsOf(sections, p).length).toBeGreaterThan(0)
  })

  it('shows the 0.8.0 settings where the design puts them', () => {
    expect(sections[0].keys).toEqual(['mode', 'fail_policy'])
    expect(sectionsOf(sections, 'lists')[0].keys).toEqual(['allowlist_user_ids', 'blocklist_user_ids'])
    const shown = sections.flatMap((g) => g.keys)
    expect(shown[shown.indexOf('store_text') + 1]).toBe('record_pass_input_message')
    expect(sections.find((g) => g.keys.includes('verdict_cache_days'))?.keys).toContain('cache_entries')
  })

  it('keeps api_key a secret of the schema, so the core masks it', () => {
    expect(schema.properties.api_key.writeOnly).toBe(true)
  })

  it('leaves out fields before any section only when hidden, and groups without visible fields', () => {
    const s = { properties: { a: {}, b: {}, c: {} } }
    expect(settingSections(s, { 'ui:order': ['a', 'b', 'c'], b: { 'ui:section': 'B' }, c: { 'ui:widget': 'hidden' } })).toEqual([
      { id: '', page: '', title: '', description: '', collapsed: false, keys: ['a'] },
      { id: 'b', page: '', title: 'B', description: '', collapsed: false, keys: ['b'] }
    ])
    expect(settingSections(s, { a: { 'ui:widget': 'hidden' }, b: { 'ui:widget': 'hidden' }, c: { 'ui:section': { title: 'C', collapsed: true }, 'ui:widget': 'hidden' } })).toEqual([])
    expect(settingSections(null, ui)).toEqual([])
  })
})

describe('editableKeys', () => {
  it('adds the 0.1 API key only while one is stored, and never renders it with a schema widget', () => {
    expect(editableKeys(sections, {})).not.toContain(LEGACY_KEY)
    const keys = editableKeys(sections, { api_key: MASK })
    expect(keys).toContain(LEGACY_KEY)
    const widgets = schemaKeys(keys)
    expect(widgets).not.toContain(LEGACY_KEY)
    for (const k of CUSTOM_KEYS) expect(widgets).not.toContain(k)
    expect(widgets).toContain('mode')
  })
})

describe('the save of the settings tab', () => {
  // What the page sends for a key: the widget value, or the mask of a kept secret.
  const page = (stored: Record<string, unknown>, form: Record<string, unknown>): PageValues => {
    const out: PageValues = {}
    for (const k of schemaKeys(editableKeys(sections, stored))) out[k] = sentSetting((schema.properties as Record<string, any>)[k], stored, k, form[k])
    out.analysis_channel_id = (form.analysis_channel_id as number) ?? 0
    out.notify_webhook_url = stored.notify_webhook_url === MASK ? MASK : undefined
    out.notify_webhook_secret = stored.notify_webhook_secret === MASK ? MASK : undefined
    out.jev_default_set = (stored.jev_default_set as unknown) ?? null
    if (stored.api_key) out.api_key = form.api_key
    return out
  }
  const stored = {
    mode: 'observe',
    watch_days: 14,
    notify_webhook_url: MASK,
    notify_webhook_secret: MASK,
    api_key: MASK,
    base_url: 'https://old.example',
    model: 'gpt-4o-mini',
    categories: [{ id: 'illegal', description: 'x' }],
    group_ids: [3],
    retention_days: 60
  }
  const keys = editableKeys(sections, stored)
  const form: Record<string, unknown> = { ...displayValues(schema, stored, schemaKeys(keys)), analysis_channel_id: 0, api_key: MASK }
  const initial = page(stored, form)

  it('shows schema defaults for keys never stored, without writing them', () => {
    expect(form.block_status).toBe(403)
    expect(form.auto_watch).toBe(true)
    const m = mergeSettings(keys, stored, stored, initial, page(stored, form))
    expect(m.changed).toEqual([])
    expect(m.values).toEqual(stored)
  })

  it('writes only the changed keys and sends the hidden 0.1 fields and kept secrets back as stored', () => {
    const m = mergeSettings(keys, stored, stored, initial, page(stored, { ...form, mode: 'enforce', verdict_cache_days: 3 }))
    expect(m.changed.sort()).toEqual(['mode', 'verdict_cache_days'])
    expect(m.values).toEqual({ ...stored, mode: 'enforce', verdict_cache_days: 3 })
  })

  // The same reading as settings.go DecodeSettings (TestExemptBecomesAllowlist).
  describe('the allowlist of settings saved before 0.8.0', () => {
    const old = { ...stored, exempt_user_ids: ['5', '9'] }
    const oldKeys = editableKeys(sections, old)
    const oldForm: Record<string, unknown> = { ...displayValues(schema, old, schemaKeys(oldKeys)), analysis_channel_id: 0, api_key: MASK }
    const oldInitial = page(old, oldForm)

    it('starts from the stored exempt_user_ids while allowlist_user_ids was never stored', () => {
      expect(oldForm.allowlist_user_ids).toEqual(['5', '9'])
      expect(oldForm.blocklist_user_ids).toEqual([])
      expect(oldKeys).not.toContain('exempt_user_ids')
      // nothing is written while nothing is edited
      const m = mergeSettings(oldKeys, old, old, oldInitial, page(old, oldForm))
      expect(m.changed).toEqual([])
      expect(m.values).toEqual(old)
    })

    it('uses allowlist_user_ids once it is stored, also when empty', () => {
      const both = { ...old, allowlist_user_ids: ['7'] }
      expect(displayValues(schema, both, ['allowlist_user_ids']).allowlist_user_ids).toEqual(['7'])
      expect(displayValues(schema, { ...old, allowlist_user_ids: [] }, ['allowlist_user_ids']).allowlist_user_ids).toEqual([])
      expect(displayValues(schema, stored, ['allowlist_user_ids']).allowlist_user_ids).toEqual([])
    })

    it('writes allowlist_user_ids and the same list to the retired key, so a 0.7.2 node exempts exactly the allowlisted users', () => {
      const m = mergeSettings(oldKeys, old, old, oldInitial, page(old, { ...oldForm, allowlist_user_ids: ['9', '12'] }))
      expect(m.changed).toEqual(['allowlist_user_ids'])
      expect(m.values).toEqual({ ...old, allowlist_user_ids: ['9', '12'] })
      // user 9 stays exempt on a 0.7.2 node, the removed user 5 does not, the added user 12 is
      const sent = withRetiredMirrored(m.values, m.changed)
      expect(sent).toEqual({ ...old, allowlist_user_ids: ['9', '12'], exempt_user_ids: ['9', '12'] })
      expect(sent.exempt_user_ids).not.toBe(sent.allowlist_user_ids)
      // m.values itself is not mutated
      expect(m.values.exempt_user_ids).toEqual(['5', '9'])
    })

    it('leaves the retired key alone when the allowlist is not saved', () => {
      const other = mergeSettings(oldKeys, old, old, oldInitial, page(old, { ...oldForm, mode: 'enforce' }))
      expect(other.changed).toEqual(['mode'])
      expect(withRetiredMirrored(other.values, other.changed)).toBe(other.values)
    })

    it('mirrors a saved allowlist also when no retired list was stored, or another one meanwhile', () => {
      const fresh = { ...stored, allowlist_user_ids: ['7'] }
      expect(withRetiredMirrored(fresh, ['allowlist_user_ids'])).toEqual({ ...fresh, exempt_user_ids: ['7'] })
      expect(withRetiredMirrored({ ...fresh, exempt_user_ids: [] }, ['allowlist_user_ids']).exempt_user_ids).toEqual(['7'])
      // the list another admin stored meanwhile (latest) is replaced too
      expect(withRetiredMirrored({ ...fresh, exempt_user_ids: ['3'] }, ['mode', 'allowlist_user_ids']).exempt_user_ids).toEqual(['7'])
      // an allowlist removed by the save: a stored retired list is emptied, none is added
      expect(withRetiredMirrored({ ...stored, exempt_user_ids: ['3'] }, ['allowlist_user_ids']).exempt_user_ids).toEqual([])
      expect(withRetiredMirrored(stored, ['allowlist_user_ids'])).toBe(stored)
    })

    it('keeps a blocklisted user out of the retired key, whichever of the two lists is saved', () => {
      // on both lists: moderated before sending on 0.8.0, so not exempt on a 0.7.2 node
      const both = { ...stored, allowlist_user_ids: ['7', '8'], blocklist_user_ids: ['8'], exempt_user_ids: ['7', '8'] }
      expect(withRetiredMirrored(both, ['allowlist_user_ids']).exempt_user_ids).toEqual(['7'])
      // only the blocklist is saved: the retired key is rewritten all the same, the allowlist stays
      const blocked = withRetiredMirrored(both, ['blocklist_user_ids'])
      expect(blocked).toEqual({ ...both, exempt_user_ids: ['7'] })
      // taken off the blocklist again: exempt again
      expect(withRetiredMirrored({ ...both, blocklist_user_ids: [], exempt_user_ids: ['7'] }, ['blocklist_user_ids']).exempt_user_ids).toEqual(['7', '8'])
      // ids compare as text (a list stored as numbers)
      expect(withRetiredMirrored({ ...both, blocklist_user_ids: [8] }, ['blocklist_user_ids']).exempt_user_ids).toEqual(['7'])
      // no retired key stored yet: a saved blocklist adds the mirror too
      expect(withRetiredMirrored({ ...stored, allowlist_user_ids: ['7', '8'], blocklist_user_ids: ['8'] }, ['blocklist_user_ids']).exempt_user_ids).toEqual(['7'])
    })

    it('saving the blocklist while the allowlist is still the retired list', () => {
      // a blocklisted user on it: the list becomes allowlist_user_ids as it is, the retired key loses the user
      const m = mergeSettings(oldKeys, old, old, oldInitial, page(old, { ...oldForm, blocklist_user_ids: ['9'] }))
      expect(m.changed).toEqual(['blocklist_user_ids'])
      expect(withRetiredMirrored(m.values, m.changed)).toEqual({ ...old, blocklist_user_ids: ['9'], allowlist_user_ids: ['5', '9'], exempt_user_ids: ['5'] })
      expect(m.values.exempt_user_ids).toEqual(['5', '9'])
      // nobody on both: nothing else is written
      const other = mergeSettings(oldKeys, old, old, oldInitial, page(old, { ...oldForm, blocklist_user_ids: ['12'] }))
      expect(withRetiredMirrored(other.values, other.changed)).toBe(other.values)
      // neither an allowlist nor a retired list: nothing to mirror
      const none = { ...stored, blocklist_user_ids: ['12'] }
      expect(withRetiredMirrored(none, ['blocklist_user_ids'])).toBe(none)
    })

    it('stores an emptied allowlist as [], so the retired list is not read again', () => {
      for (const emptied of [[], undefined, null]) {
        const m = mergeSettings(oldKeys, old, old, oldInitial, page(old, { ...oldForm, allowlist_user_ids: emptied }))
        expect(m.changed).toEqual(['allowlist_user_ids'])
        expect(m.values.allowlist_user_ids).toEqual([])
        expect(withRetiredMirrored(m.values, m.changed).exempt_user_ids).toEqual([])
      }
      // without a retired list an emptied allowlist is simply removed
      expect(sentSetting(schema.properties.allowlist_user_ids, stored, 'allowlist_user_ids', undefined)).toBeUndefined()
      expect(sentSetting(schema.properties.blocklist_user_ids, old, 'blocklist_user_ids', undefined)).toBeUndefined()
    })
  })

  describe('the verdict cache of settings saved before 0.8.0', () => {
    const days = (s: Record<string, unknown>) => displayValues(schema, s, ['verdict_cache_days']).verdict_cache_days

    it('shows the days the retired cache_ttl_seconds gives, rounded up, 1 to 30', () => {
      expect(days({})).toBe(7)
      expect(days({ cache_ttl_seconds: 600 })).toBe(1)
      expect(days({ cache_ttl_seconds: 86400 })).toBe(1)
      expect(days({ cache_ttl_seconds: 86401 })).toBe(2)
      expect(days({ cache_ttl_seconds: 604800 })).toBe(7)
      expect(days({ cache_ttl_seconds: 600, verdict_cache_days: 14 })).toBe(14)
    })

    it('shows no days while the retired key keeps the cache off, and saving days turns it on', () => {
      const off = { ...stored, cache_ttl_seconds: 0 }
      expect(verdictCacheOff(off)).toBe(true)
      expect(verdictCacheOff({ ...off, verdict_cache_days: 7 })).toBe(false)
      expect(verdictCacheOff(stored)).toBe(false)
      expect(days(off)).toBeUndefined()
      const offKeys = editableKeys(sections, off)
      const offForm: Record<string, unknown> = { ...displayValues(schema, off, schemaKeys(offKeys)), analysis_channel_id: 0, api_key: MASK }
      const m = mergeSettings(offKeys, off, off, page(off, offForm), page(off, { ...offForm, verdict_cache_days: 7 }))
      expect(m.changed).toEqual(['verdict_cache_days'])
      expect(m.values).toEqual({ ...off, verdict_cache_days: 7 })
    })
  })

  // settings.go verdictCache: without verdict_cache_days a stored cache_ttl_seconds is read again.
  describe('cleared cache days while the retired cache_ttl_seconds is stored', () => {
    const cleared = (s: Record<string, unknown>) => {
      const ks = editableKeys(sections, s)
      const f: Record<string, unknown> = { ...displayValues(schema, s, schemaKeys(ks)), analysis_channel_id: 0, api_key: MASK }
      return mergeSettings(ks, s, s, page(s, f), page(s, { ...f, verdict_cache_days: '' }))
    }

    it('sends the schema default instead of removing the key', () => {
      // the retired key gives 1 day: without the new key the cache would stay at 1 day
      const hour = { ...stored, cache_ttl_seconds: 3600 }
      expect(cleared(hour).changed).toEqual(['verdict_cache_days'])
      expect(cleared(hour).values).toEqual({ ...hour, verdict_cache_days: 7 })
      // days stored over a retired 0: without the new key the cache would be off
      const both = { ...stored, cache_ttl_seconds: 0, verdict_cache_days: 3 }
      expect(cleared(both).values).toEqual({ ...both, verdict_cache_days: 7 })
      for (const empty of [undefined, null, '', ' ']) expect(sentSetting(schema.properties.verdict_cache_days, hour, 'verdict_cache_days', empty)).toBe(7)
    })

    it('removes the key without a retired one, and leaves an off cache off while the field was never filled', () => {
      const days = { ...stored, verdict_cache_days: 3 }
      expect(cleared(days).changed).toEqual(['verdict_cache_days'])
      expect(cleared(days).values).toEqual(stored)
      // the field is empty because the retired 0 keeps the cache off: nothing was cleared, nothing is written
      const off = { ...stored, cache_ttl_seconds: 0 }
      expect(cleared(off).changed).toEqual([])
      expect(cleared(off).values).toEqual(off)
      // other numbers are still removed
      expect(sentSetting(schema.properties.cache_entries, { ...stored, cache_ttl_seconds: 3600 }, 'cache_entries', '')).toBeUndefined()
    })
  })

  it('removes a number left empty, so its default applies', () => {
    const m = mergeSettings(keys, stored, stored, initial, page(stored, { ...form, watch_days: undefined }))
    expect(m.changed).toEqual(['watch_days'])
    expect(m.values).not.toHaveProperty('watch_days')
    expect(sentSetting({ type: 'integer' }, stored, 'watch_days', '')).toBeUndefined()
    expect(sentSetting({ type: 'string' }, stored, 'block_message', '')).toBe('')
  })

  it('clears the stored 0.1 API key on request and keeps the other 0.1 fields', () => {
    const m = mergeSettings(keys, stored, stored, initial, page(stored, { ...form, api_key: undefined }))
    expect(m.changed).toEqual([LEGACY_KEY])
    const { api_key: _, ...rest } = stored
    expect(m.values).toEqual(rest)
  })
})

describe('settingsKey', () => {
  it('maps API field paths to the settings key', () => {
    expect(settingsKey('values/allowlist_user_ids/3')).toBe('allowlist_user_ids')
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
