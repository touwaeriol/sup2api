/// <reference types="vite/client" />
import { afterEach, describe, expect, it } from 'vitest'
import type { VueWrapper } from '@vue/test-utils'
import { checkMessages, cleanup, mountSettings as mount } from '@sub2api/ui/testing'
import GuardSettings from './GuardSettings.vue'
import messages from './messages'
import { setHost } from './host'

type M = Record<string, any>

/** The value the form fills into empty settings (the plugin's own defaults). */
const DEFAULTS = { webhook_url: '', record_snippets: false, alert_cooldown_sec: 60 }

/** GuardSettings over a fake core holding `values` (no secrets: ui.settings.secrets is []). */
const mountSettings = (values: M = {}, granted = ['plugin:manage']) => mount(GuardSettings, { plugin: 'guard', setHost, values, granted })
const field = (w: VueWrapper, id: string) => w.get(`[data-testid="guard-${id}"]`)

afterEach(cleanup)

describe('GuardSettings', () => {
  it('fills the defaults into unset settings and saves the whole value', async () => {
    const { w, core, settings } = await mountSettings({})
    expect(settings.value()).toEqual(DEFAULTS)
    expect(settings.dirty.value).toBe(false)
    await field(w, 'webhook-url').get('input').setValue('https://hooks.example.com/guard')
    await field(w, 'snippets').setValue(true)
    await field(w, 'cooldown').setValue('0')
    expect(await settings.save()).toBe(true)
    expect(core.puts).toEqual([{ webhook_url: 'https://hooks.example.com/guard', record_snippets: true, alert_cooldown_sec: 0 }])
  })

  it('keeps stored keys and fills only the absent ones (older settings)', async () => {
    const { settings } = await mountSettings({ webhook_url: 'https://hooks.example.com/x' })
    expect(settings.value()).toEqual({ ...DEFAULTS, webhook_url: 'https://hooks.example.com/x' })
  })

  it('labels, help, unit and placeholder', async () => {
    const { w } = await mountSettings({})
    for (const key of ['settings.webhookUrl.label', 'settings.webhookUrl.help', 'settings.cooldown.label', 'settings.cooldown.help', 'settings.cooldown.unit', 'settings.snippets.label', 'settings.snippets.help'])
      expect(w.text()).toContain(key)
    expect(field(w, 'webhook-url').get('input').attributes('placeholder')).toBe('https://hooks.example.com/guard')
  })

  it('a cleared webhook URL or cooldown sends no key (the plugin then sends no alert / uses 60 s)', async () => {
    const { w, core, settings } = await mountSettings({ webhook_url: 'https://hooks.example.com/x', alert_cooldown_sec: 30 })
    await field(w, 'webhook-url').get('input').setValue('')
    await field(w, 'cooldown').setValue('')
    expect(await settings.save()).toBe(true)
    expect(core.puts[0]).toEqual({ record_snippets: false })
  })

  it('without edit rights every control is disabled and there is no save bar', async () => {
    const { w } = await mountSettings({}, [])
    expect(w.find('fieldset').attributes('disabled')).toBeDefined()
    expect(w.find('[data-testid="settings-save-bar"]').exists()).toBe(false)
  })
})

// One case per constraint of the settings: the value the form saves and
// whether it lets it be saved.
describe('GuardSettings rules', () => {
  const cases: Array<{ name: string; set: M; ok: boolean }> = [
    { name: 'defaults', set: {}, ok: true },
    { name: 'a webhook with a token', set: { webhook_url: 'https://hooks.example.com/guard?token=x', record_snippets: true, alert_cooldown_sec: 0 }, ok: true },
    { name: 'an http webhook', set: { webhook_url: 'http://10.0.0.5:8080/hook' }, ok: true },
    { name: 'a webhook that is not http(s)', set: { webhook_url: 'ftp://hooks.example.com' }, ok: false },
    { name: 'a webhook with a space', set: { webhook_url: 'https://hooks.example.com/a b' }, ok: false },
    { name: 'a scheme without a host', set: { webhook_url: 'https://' }, ok: false },
    { name: 'cooldown below 0', set: { alert_cooldown_sec: -1 }, ok: false },
    { name: 'cooldown above 300', set: { alert_cooldown_sec: 301 }, ok: false },
    { name: 'cooldown 300', set: { alert_cooldown_sec: 300 }, ok: true },
    { name: 'fractional cooldown', set: { alert_cooldown_sec: 1.5 }, ok: false },
    // Kept as it is; the plugin's ValidateSettings refuses it (code unknown).
    { name: 'a key the form does not know', set: { other: 'x' }, ok: true }
  ]
  for (const c of cases) {
    it(c.name, async () => {
      const { settings } = await mountSettings({})
      for (const [k, v] of Object.entries(c.set)) settings.set(k, v)
      expect(settings.value()).toEqual({ ...DEFAULTS, ...c.set })
      expect(await settings.validate()).toBe(c.ok)
    })
  }
})

describe('messages', () =>
  checkMessages(messages, {
    sources: import.meta.glob<string>(['./*.vue', './*.ts', '!./*.spec.ts', '!./messages.ts'], { query: '?raw', import: 'default', eager: true })
  }))
