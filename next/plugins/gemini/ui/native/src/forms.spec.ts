import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { cleanup, control, fakeHost, fillControls, keyAndUrlCases, messagesT, mountAccountForm } from '@sub2api/ui/testing'
import { forms } from './forms'
import messages from './messages'

const { GeminiKeyForm } = forms(messagesT(messages))
const LABELS = { api_key: 'API key', base_url: 'Base URL' }

beforeEach(() => fakeHost())
afterEach(cleanup)

describe('GeminiKeyForm', () => {
  const accountType = { plugin_key: 'gemini', type: 'apikey' }
  // The shared api_key and base_url cases (keyAndUrlCases).
  for (const c of keyAndUrlCases('AIzaSyAbcdefgh', 'https://x.example.com/gemini', false)) {
    it(c.name, async () => {
      const { w, saved, validate } = mountAccountForm(GeminiKeyForm, { accountType, stored: c.stored })
      await fillControls(w, LABELS, c.input ?? {})
      expect({ value: saved(), ok: await validate() }).toEqual({ value: c.out, ok: c.ok })
    })
  }

  it('labels, help and placeholders', async () => {
    const { w } = mountAccountForm(GeminiKeyForm, { accountType })
    await flushPromises()
    expect(w.findAll('label').map((l) => l.text())).toEqual(['API key*', 'Base URL'])
    expect(control(w, LABELS.api_key).attributes('placeholder')).toBe('AIza...')
    expect(control(w, LABELS.base_url).attributes('placeholder')).toBe(messages.en.baseUrl.placeholder)
    for (const help of [messages.en.apiKey.help, messages.en.baseUrl.help]) expect(w.text()).toContain(help)
    // The official address, offered as a preset.
    expect(w.findAll('datalist option').map((o) => o.attributes('value'))).toEqual(['https://generativelanguage.googleapis.com'])
  })

  it('guarded base URL: the official value only, unless the caller holds account:settings:custom', async () => {
    const guardedType = { ...accountType, guarded_settings: [{ field: 'base_url', allowed: ['https://generativelanguage.googleapis.com'] }] }
    const guarded = mountAccountForm(GeminiKeyForm, { accountType: guardedType })
    await flushPromises()
    expect(guarded.w.find('select').attributes('disabled')).toBeDefined()
    expect(guarded.w.find('select').text()).toContain('https://generativelanguage.googleapis.com')
    fakeHost({ granted: ['account:settings:custom'] })
    const free = mountAccountForm(GeminiKeyForm, { accountType: guardedType })
    await flushPromises()
    expect(free.w.find('input[type="url"]').exists()).toBe(true)
  })

  it('shows a server error at its field', async () => {
    const { w } = mountAccountForm(GeminiKeyForm, { accountType, errors: { api_key: 'invalid key' } })
    await flushPromises()
    expect(w.text()).toContain('invalid key')
  })
})
