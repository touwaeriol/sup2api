// The spec helpers of @sub2api/ui/testing, used the way a plugin's specs
// use them (through the alias).
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { defineComponent, h } from 'vue'
import { useHost, type PluginHost } from '@sub2api/host'
import { checkMessages, cleanup, control, fakeCore, fakeHost, fillControls, keyAndUrlCases, messagesT, mountAccountForm, mountSettings } from '@sub2api/ui/testing'
import { messageProblems } from './messages'
import { createApiKeyForm, SECRET_MASK, SSettingsPage, SSettingsGroup, SSettingRow, SInput, SSecretInput, SSelect, usePluginSettings } from '../index'

afterEach(cleanup)

describe('account forms: mountAccountForm, fillControls, keyAndUrlCases', () => {
  const Form = createApiKeyForm({
    baseUrl: { presets: ['https://api.example.com'] },
    extra: [
      { key: 'tier', kind: 'select', label: () => 'Tier', options: ['free', 'paid'].map((v) => ({ value: v, label: () => v.toUpperCase() })) },
      { key: 'expires', kind: 'date-time', label: () => 'Expires' }
    ]
  })
  const LABELS = { api_key: 'API key', base_url: 'Base URL', tier: 'Tier', expires: 'Expires' }
  const accountType = { plugin_key: 'demo', type: 'apikey' }
  beforeEach(() => fakeHost())

  for (const c of keyAndUrlCases('sk-demo-abcdef', 'https://x.example.com/demo', false)) {
    it(c.name, async () => {
      const { w, saved, validate } = mountAccountForm(Form, { accountType, stored: c.stored })
      await fillControls(w, LABELS, c.input ?? {})
      expect({ value: saved(), ok: await validate() }).toEqual({ value: c.out, ok: c.ok })
    })
  }

  it('a select takes the option text, a date box its local value; an unknown label throws', async () => {
    const { w, saved } = mountAccountForm(Form, { accountType })
    await fillControls(w, LABELS, { tier: 'PAID', expires: '2026-11-19T10:00' })
    expect(saved()).toEqual({ tier: 'paid', expires: new Date('2026-11-19T10:00').toISOString() })
    expect(() => control(w, 'Nope')).toThrow('no label "Nope"')
    await expect(fillControls(w, {}, { tier: 'FREE' })).rejects.toThrow('no label given for tier')
  })

  it('edits with the stored credentials, and shows server errors', async () => {
    const { w, credentials } = mountAccountForm(Form, { accountType, stored: { api_key: SECRET_MASK }, errors: { api_key: 'invalid key' } })
    expect(credentials.value).toEqual({ api_key: SECRET_MASK })
    expect(w.text()).toContain('invalid key')
  })

  it('fakeHost grants the permissions given: a guarded base URL is free with account:settings:custom', async () => {
    const guarded = { ...accountType, guarded_settings: [{ field: 'base_url', allowed: ['https://api.example.com'] }] }
    expect(mountAccountForm(Form, { accountType: guarded }).w.find('input[type="url"]').exists()).toBe(false)
    fakeHost({ granted: ['account:settings:custom'] })
    expect(mountAccountForm(Form, { accountType: guarded }).w.find('input[type="url"]').exists()).toBe(true)
  })

  it('cleanup unmounts, empties the page and withdraws the console', () => {
    fakeHost({ granted: ['account:settings:custom'] })
    mountAccountForm(Form, { accountType })
    expect(document.body.innerHTML).not.toBe('')
    cleanup()
    expect(document.body.innerHTML).toBe('')
    expect(() => useHost()).toThrow('host context not initialised')
  })

  it('a cleared box drops the stored value', async () => {
    const { w, saved } = mountAccountForm(Form, { accountType, stored: { api_key: SECRET_MASK, base_url: 'https://x.example.com' } })
    await fillControls(w, LABELS, { base_url: '' })
    expect(saved()).toEqual({ api_key: SECRET_MASK })
  })
})

describe('messagesT', () => {
  it('looks up dotted keys, fills params, answers an unknown key with itself', () => {
    const host = { t: messagesT({ en: { a: { b: 'Hello {name}' } } }) }
    expect(host.t('a.b', { name: 'x' })).toBe('Hello x')
    expect(host.t('a.b')).toBe('Hello {name}')
    expect(host.t('a.c')).toBe('a.c')
    expect(fakeHost({ messages: { en: { k: 'Key' } } }).t('k')).toBe('Key')
  })
})

describe('settings: fakeCore, mountSettings', () => {
  let current: PluginHost | null = null
  const setHost = (h: PluginHost | null) => (current = h)
  const Settings = defineComponent({
    setup() {
      const host = current!
      const settings = usePluginSettings<Record<string, unknown>>({ pluginKey: host.plugin.key, api: host.api, rules: {}, defaults: { port: 587 } })
      return () =>
        h(SSettingsPage, { settings }, () =>
          h(SSettingsGroup, { title: 'SMTP' }, () => [
            h(SSettingRow, { prop: 'port', label: host.t('port') }, () => h(SInput, { type: 'number', ...settings.bindNumber('port'), 'data-testid': 'port' })),
            h(SSettingRow, { prop: 'password', label: 'Password' }, () => h(SSecretInput, { ...settings.bind('password'), clearable: true })),
            h(SSettingRow, { prop: 'mode', label: 'Mode' }, () => h(SSelect, { ...settings.bind('mode'), options: [{ value: 'a', label: 'A' }] }))
          ])
        )
    }
  })

  it('loads through the fake core, masks its secrets, keeps them on a masked PUT, records every PUT', async () => {
    const { w, core, settings } = await mountSettings(Settings, { plugin: 'demo', setHost, values: { password: 'hunter2', mode: 'a' }, secrets: ['password'] })
    expect(current?.plugin.key).toBe('demo')
    expect(w.text()).toContain('port')
    expect(settings.value()).toEqual({ password: SECRET_MASK, mode: 'a', port: 587 })
    await w.get('[data-testid="port"]').setValue('')
    expect(settings.value()).toEqual({ password: SECRET_MASK, mode: 'a' })
    expect(await settings.save()).toBe(true)
    expect(core.puts).toEqual([{ password: SECRET_MASK, mode: 'a' }])
    expect(core.stored()).toEqual({ password: 'hunter2', mode: 'a' })
    core.refuse(new Error('down'))
    settings.set('mode', undefined)
    expect(await settings.save()).toBe(false)
    expect(core.puts).toHaveLength(2)
    cleanup()
    expect(current).toBeNull()
  })

  it('without plugin:manage the form cannot be edited', async () => {
    const { w } = await mountSettings(Settings, { plugin: 'demo', setHost, granted: [] })
    expect(w.find('fieldset').attributes('disabled')).toBeDefined()
  })

  it('fakeCore checks the path and stores a plain PUT as it is', async () => {
    const core = fakeCore('demo', { a: 1 })
    expect(await core.api.get('/plugins/demo/settings')).toEqual({ values: { a: 1 } })
    await core.api.put('/plugins/demo/settings', { values: { b: 2 } })
    expect(core.stored()).toEqual({ b: 2 })
  })
})

describe('checkMessages', () => {
  checkMessages(
    { zh: { a: { b: '你好 {name}' }, tabs: { x: 'X' } }, en: { a: { b: 'Hello {name}' }, tabs: { x: 'X' } } },
    { sources: { './Demo.vue': "host.t('a.b')" }, dynamic: ['tabs.'] }
  )

  it('finds keys in one language only, special characters, unused keys', () => {
    const messages = {
      zh: { a: '甲', only: '仅中文', at: '邮箱 a@b' },
      en: { a: 'A', extra: 'Extra', at: 'mail a@b', brace: 'a { b', pipe: 'a | b', ok: 'Hi {name}' }
    }
    expect(messageProblems(messages, { sources: { './x.ts': "t('a') t('at') t('brace') t('pipe') t('ok')" } })).toEqual({
      onlyZh: ['only'],
      onlyEn: ['extra', 'brace', 'pipe', 'ok'],
      special: ['邮箱 a@b', 'mail a@b', 'a { b', 'a | b'],
      unused: ['extra']
    })
  })
})
