import { describe, expect, it } from 'vitest'
import { fakeHost, editor, typeInto, type M } from './testHost'
import { flushPromises } from '@vue/test-utils'
import { createApiKeyForm } from './apiKeyForm'
import { SECRET_MASK } from './rules'

describe('createApiKeyForm: the anthropic form', () => {
  const Anthropic = createApiKeyForm({
    baseUrl: { presets: ['https://api.anthropic.com'], placeholder: () => 'Leave empty to use https://api.anthropic.com' },
    apiKey: { placeholder: () => 'sk-ant-…', help: () => 'Anthropic Console API key.' }
  })
  const KEY = 'input[type="password"]'
  const URL = 'input[type="url"]'
  /** What the account editor would save (JSON, as sent). */
  const saved = (v: M) => JSON.parse(JSON.stringify(v))

  const cases: Array<{ name: string; key?: string; url?: string; out: M; ok: boolean }> = [
    { name: 'key only', key: 'sk-ant-api03-abcdef', out: { api_key: 'sk-ant-api03-abcdef' }, ok: true },
    { name: 'key and base URL', key: 'sk-ant-api03-abcdef', url: 'https://proxy.example.com/anthropic', out: { api_key: 'sk-ant-api03-abcdef', base_url: 'https://proxy.example.com/anthropic' }, ok: true },
    { name: 'a cleared base URL is not sent', key: 'sk-ant-api03-abcdef', url: '', out: { api_key: 'sk-ant-api03-abcdef' }, ok: true },
    { name: 'no key', out: {}, ok: false },
    { name: 'blank key', key: '   ', out: { api_key: '   ' }, ok: false },
    { name: 'short key', key: 'sk-1234', out: { api_key: 'sk-1234' }, ok: true },
    { name: 'key with a space', key: 'sk-ant api03', out: { api_key: 'sk-ant api03' }, ok: true },
    { name: 'base URL with a query', key: 'sk-ant-api03-abcdef', url: 'https://x.example.com/?a=1', out: { api_key: 'sk-ant-api03-abcdef', base_url: 'https://x.example.com/?a=1' }, ok: false },
    { name: 'base URL not http', key: 'sk-ant-api03-abcdef', url: 'ftp://x.example.com', out: { api_key: 'sk-ant-api03-abcdef', base_url: 'ftp://x.example.com' }, ok: false },
    { name: 'base URL with an upper-case scheme', key: 'sk-ant-api03-abcdef', url: 'HTTPS://x.example.com', out: { api_key: 'sk-ant-api03-abcdef', base_url: 'HTTPS://x.example.com' }, ok: true },
    { name: 'base URL with @ in the path', key: 'sk-ant-api03-abcdef', url: 'https://x.example.com/a@b', out: { api_key: 'sk-ant-api03-abcdef', base_url: 'https://x.example.com/a@b' }, ok: true },
    { name: 'base URL with userinfo', key: 'sk-ant-api03-abcdef', url: 'https://user:pass@x.example.com', out: { api_key: 'sk-ant-api03-abcdef', base_url: 'https://user:pass@x.example.com' }, ok: false },
    { name: 'base URL without a host', key: 'sk-ant-api03-abcdef', url: 'https:///v1', out: { api_key: 'sk-ant-api03-abcdef', base_url: 'https:///v1' }, ok: false },
    { name: 'key of 513 characters', key: 'k'.repeat(513), out: { api_key: 'k'.repeat(513) }, ok: true }
  ]
  for (const c of cases) {
    it(`saved keys and verdict: ${c.name}`, async () => {
      const { w, credentials, validate } = editor(Anthropic)
      await flushPromises()
      if (c.key !== undefined) await typeInto(w, KEY, c.key)
      if (c.url !== undefined) {
        await typeInto(w, URL, 'https://tmp.example.com')
        await typeInto(w, URL, c.url)
      }
      expect(saved(credentials.value)).toEqual(c.out)
      expect(await validate()).toBe(c.ok)
    })
  }

  // A URL box strips surrounding ASCII spaces itself; other Unicode spaces
  // (a pasted no-break space) reach the rules, and the plugin trims them too.
  it('trims surrounding Unicode spaces of the base URL as the plugin does, and takes a blank one as empty', async () => {
    const verdicts: Record<string, boolean> = {}
    for (const base_url of ['\u00a0https://x.example.com/a\u00a0', '\u00a0', '\u00a0https://user@x.example.com']) {
      const { validate } = editor(Anthropic, { modelValue: { api_key: 'sk-ant-api03-abcdef', base_url } })
      await flushPromises()
      verdicts[base_url] = await validate()
    }
    expect(verdicts).toEqual({ '\u00a0https://x.example.com/a\u00a0': true, '\u00a0': true, '\u00a0https://user@x.example.com': false })
  })

  it('labels, star, placeholders and the preset menu', async () => {
    const { w } = editor(Anthropic)
    await flushPromises()
    expect(w.findAll('label').map((l) => l.text())).toEqual(['API key*', 'Base URL'])
    expect(w.find(KEY).attributes('placeholder')).toBe('sk-ant-…')
    expect(w.find(URL).attributes('placeholder')).toBe('Leave empty to use https://api.anthropic.com')
    expect(w.text()).toContain('Anthropic Console API key.')
    expect(w.findAll('button').map((b) => b.text())).toContain('Presets')
  })

  it('edit: an untouched stored key and every other key go back as they are', async () => {
    const stored = { api_key: SECRET_MASK, base_url: 'https://custom.example.com', legacy: 'x' }
    const { w, credentials, validate } = editor(Anthropic, { mode: 'edit', modelValue: { ...stored } })
    await flushPromises()
    expect(await validate()).toBe(true)
    expect(credentials.value).toEqual(stored)
    await typeInto(w, URL, 'https://other.example.com')
    expect(credentials.value).toEqual({ ...stored, base_url: 'https://other.example.com' })
  })

  it('shows the server errors at their fields until the field changes', async () => {
    const { w } = editor(Anthropic, { errors: { api_key: 'invalid x-api-key' } })
    await flushPromises()
    expect(w.text()).toContain('invalid x-api-key')
    await typeInto(w, KEY, 'sk-ant-api03-abcdef')
    expect(w.text()).not.toContain('invalid x-api-key')
  })

  it('guarded base URL: the official values only, unless the caller holds account:settings:custom', async () => {
    const accountType = { plugin_key: 'anthropic', type: 'apikey', guarded_settings: [{ field: 'base_url', allowed: ['https://api.anthropic.com'] }] }
    const guarded = editor(Anthropic, { accountType })
    await flushPromises()
    expect(guarded.w.find('select').attributes('disabled')).toBeDefined()
    expect(guarded.w.text()).toContain('Set by an administrator')
    guarded.w.unmount()
    fakeHost(['account:settings:custom'])
    const free = editor(Anthropic, { accountType })
    await flushPromises()
    expect(free.w.find('select').exists()).toBe(false)
    expect(free.w.find(URL).exists()).toBe(true)
  })

  it('a guarded url extra field offers the official values only, like base_url', async () => {
    const Form = createApiKeyForm({
      baseUrl: false,
      extra: [{ key: 'asset_base_url', kind: 'url', label: () => 'Asset endpoint', presets: ['https://assets.example.com'] }]
    })
    const accountType = { plugin_key: 'demo', type: 'apikey', guarded_settings: [{ field: 'asset_base_url', allowed: ['https://assets.example.com'] }] }
    const guarded = editor(Form, { accountType })
    await flushPromises()
    expect(guarded.w.find('select').attributes('disabled')).toBeDefined()
    expect(guarded.w.find(URL).exists()).toBe(false)
    guarded.w.unmount()
    fakeHost(['account:settings:custom'])
    const free = editor(Form, { accountType })
    await flushPromises()
    expect(free.w.find('select').exists()).toBe(false)
    await typeInto(free.w, URL, 'https://other.example.com')
    expect(free.credentials.value).toEqual({ asset_base_url: 'https://other.example.com' })
  })

  it('base URL first, with its own label and pattern message', async () => {
    const Site = createApiKeyForm({
      baseUrl: { required: true, first: true, label: () => 'Site address', message: () => 'The site root only', pattern: /^https:\/\/[^/\s?#]+\/?$/ }
    })
    const { w, validate } = editor(Site)
    await flushPromises()
    expect(w.findAll('label').map((l) => l.text())).toEqual(['Site address*', 'API key*'])
    await typeInto(w, KEY, 'sk-ant-api03-abcdef')
    expect(await validate()).toBe(false)
    expect(w.text()).not.toContain('The site root only')
    await typeInto(w, URL, 'https://site.example.com/v1/chat')
    expect(await validate()).toBe(false)
    expect(w.text()).toContain('The site root only')
    await typeInto(w, URL, 'https://site.example.com')
    expect(await validate()).toBe(true)
  })

  it('sections: a title with help, a collapsed one that opens with a value, fields grouped until the next title', async () => {
    const asset = { title: () => 'Asset library (optional)', help: () => 'Fill these to use the asset library.' }
    const advanced = { title: () => 'Advanced', collapsed: true }
    const Form = createApiKeyForm({
      extra: [
        { key: 'access_key', kind: 'text', label: () => 'Access key', section: asset },
        { key: 'asset_region', kind: 'text', label: () => 'Region' },
        { key: 'organization', kind: 'text', label: () => 'Organization', section: advanced },
        { key: 'project', kind: 'text', label: () => 'Project', section: advanced }
      ]
    })
    const created = editor(Form)
    await flushPromises()
    const sections = created.w.findAll('[data-testid="form-section"]')
    expect(sections.map((x) => x.findAll('label').map((l) => l.text()))).toEqual([['Access key', 'Region'], []])
    expect(sections[0].get('h4').text()).toBe('Asset library (optional)')
    expect(sections[0].text()).toContain('Fill these to use the asset library.')
    expect(sections[1].text()).toBe('Advanced')
    await sections[1].get('button').trigger('click')
    expect(sections[1].findAll('label').map((l) => l.text())).toEqual(['Organization', 'Project'])

    const edited = editor(Form, { mode: 'edit', modelValue: { api_key: SECRET_MASK, project: 'proj_abc' } })
    await flushPromises()
    expect(edited.w.findAll('[data-testid="form-section"]')[1].findAll('label').map((l) => l.text())).toEqual(['Organization', 'Project'])
  })

  it('mono: a text or textarea extra field is monospaced when it asks', async () => {
    const Form = createApiKeyForm({
      baseUrl: false,
      extra: [
        { key: 'project_id', kind: 'text', label: () => 'Project', mono: true },
        { key: 'name', kind: 'text', label: () => 'Name' },
        { key: 'policy', kind: 'textarea', label: () => 'Policy', mono: true }
      ]
    })
    const { w } = editor(Form)
    await flushPromises()
    expect(w.findAll('input[type="text"]').map((i) => i.classes('font-mono'))).toEqual([true, false])
    expect(w.get('textarea').classes()).toContain('font-mono')
  })

  /** An API key form with extra fields: a region with a pattern, a prefix to select, a date-time while the prefix is "us". */
  const bedrock = () =>
    createApiKeyForm({
      baseUrl: false,
      apiKey: { max: 2048 },
      defaults: { region: 'us-east-1', inference_profile_prefix: 'global' },
      extra: [
        { key: 'region', kind: 'text', label: () => 'Region', section: () => 'Where', rules: { pattern: /^([a-z]{2}(-[a-z]+)+-\d)?$/ } },
        { key: 'inference_profile_prefix', kind: 'select', label: () => 'Prefix', section: () => 'Where', options: ['none', 'us', 'global'].map((v) => ({ value: v, label: () => v.toUpperCase() })), rules: { enum: ['none', 'us', 'global'] } },
        { key: 'expires', kind: 'date-time', label: () => 'Expires', when: (m) => m.inference_profile_prefix === 'us', rules: { required: true, type: 'date-time' } }
      ]
    })

  it('extra fields: select with defaults, sections, no base URL, and their own rules', async () => {
    const { w, credentials, validate } = editor(bedrock())
    await flushPromises()
    expect(credentials.value).toEqual({ region: 'us-east-1', inference_profile_prefix: 'global' })
    expect(w.find('input[type="url"]').exists()).toBe(false)
    expect(w.findAll('h4').map((x) => x.text())).toEqual(['Where'])
    await typeInto(w, KEY, 'k'.repeat(1000))
    expect(await validate()).toBe(true)
    await typeInto(w, 'input[type="text"]', 'nowhere')
    expect(await validate()).toBe(false)
    expect(w.text()).toContain('Invalid format')
  })

  it('extra fields: `when`, and a date-time in RFC 3339', async () => {
    const { w, credentials, validate } = editor(bedrock())
    await flushPromises()
    expect(w.find('input[type="datetime-local"]').exists()).toBe(false)
    await typeInto(w, KEY, 'k')
    await w.find('select').setValue(String(1))
    await flushPromises()
    expect(credentials.value.inference_profile_prefix).toBe('us')
    expect(await validate()).toBe(false)
    await w.find('input[type="datetime-local"]').setValue('2026-11-19T10:00')
    await w.find('input[type="datetime-local"]').trigger('change')
    await flushPromises()
    expect(credentials.value.expires).toMatch(/^2026-11-19T\d{2}:00:00(\.000)?(Z|[+-]\d{2}:\d{2})$/)
    expect(await validate()).toBe(true)
  })

  it('apiKey min, max and pattern apply when given', async () => {
    const { w, validate } = editor(createApiKeyForm({ baseUrl: false, apiKey: { min: 4, max: 6, pattern: /^k+$/ } }))
    await flushPromises()
    const verdicts: boolean[] = []
    for (const key of ['kkk', 'kkkk', 'kkkkkkk', 'kk kk']) {
      await typeInto(w, KEY, key)
      verdicts.push(await validate())
    }
    expect(verdicts).toEqual([false, true, false, false])
  })
})
