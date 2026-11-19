import { afterEach, describe, expect, it } from 'vitest'
import type { VueWrapper } from '@vue/test-utils'
import { cleanup, mountSettings as mount } from '@sub2api/ui/testing'
import CodexSettings from './CodexSettings.vue'
import { setHost } from './host'

type M = Record<string, any>

/** CodexSettings over a fake core holding `values` (no secrets: ui.settings.secrets is empty). */
const mountSettings = (values: M = {}, granted = ['plugin:manage']) => mount(CodexSettings, { plugin: 'codex', setHost, values, granted })
const input = (w: VueWrapper, id: string) => w.get(`[data-testid="codex-settings-${id}"]`)

afterEach(cleanup)

describe('CodexSettings', () => {
  it('fills both headers empty (the built-in ones) and saves the whole value', async () => {
    const { w, core, settings } = await mountSettings({})
    expect(settings.value()).toEqual({ user_agent: '', originator: '' })
    expect(settings.dirty.value).toBe(false)
    await input(w, 'user-agent').setValue('my-agent/1.0')
    expect(await settings.save()).toBe(true)
    expect(core.puts).toEqual([{ user_agent: 'my-agent/1.0', originator: '' }])
  })

  it('labels, help and placeholders', async () => {
    const { w } = await mountSettings({ originator: 'ops' })
    for (const key of ['settings.userAgent', 'settings.userAgentHint', 'settings.originator', 'settings.originatorHint']) expect(w.text()).toContain(key)
    expect(input(w, 'user-agent').attributes('placeholder')).toBe('sup2api-codex/<version>')
    expect(input(w, 'originator').attributes('placeholder')).toBe('sup2api')
    expect((input(w, 'originator').element as HTMLInputElement).value).toBe('ops')
  })

  it('without edit rights every control is disabled and there is no save bar', async () => {
    const { w } = await mountSettings({}, [])
    expect(w.find('fieldset').attributes('disabled')).toBeDefined()
    expect(w.find('[data-testid="settings-save-bar"]').exists()).toBe(false)
  })
})

// One case per constraint of the settings: the value the form saves and
// whether it lets it be saved.
describe('CodexSettings rules', () => {
  const cases: Array<{ name: string; set: M; ok: boolean }> = [
    { name: 'defaults', set: {}, ok: true },
    { name: 'both set', set: { user_agent: 'sup2api-codex/0.2.0', originator: 'codex_cli_rs' }, ok: true },
    { name: 'a tab is allowed', set: { user_agent: 'a\tb' }, ok: true },
    { name: 'a line break in the User-Agent', set: { user_agent: 'a\nb' }, ok: false },
    { name: 'a NUL in the originator', set: { originator: 'a\u0000' }, ok: false },
    { name: 'DEL in the originator', set: { originator: 'a\u007f' }, ok: false },
    { name: 'a unit separator in the User-Agent', set: { user_agent: '\u001f' }, ok: false },
    // Kept as it is; the plugin's ValidateSettings refuses it (code unknown).
    { name: 'a key the form does not know', set: { other: 'x' }, ok: true }
  ]
  for (const c of cases) {
    it(c.name, async () => {
      const { settings } = await mountSettings({})
      for (const [k, v] of Object.entries(c.set)) settings.set(k, v)
      expect(settings.value()).toEqual({ user_agent: '', originator: '', ...c.set })
      expect(await settings.validate()).toBe(c.ok)
    })
  }
})
