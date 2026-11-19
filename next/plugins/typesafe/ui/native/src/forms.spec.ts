import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { cleanup, control, fakeHost, fillControls, keyAndUrlCases, messagesT, mountAccountForm } from '@sub2api/ui/testing'
import { forms } from './forms'
import messages from './messages'

const { TypesafeKeyForm } = forms(messagesT(messages))
const LABELS = { api_key: 'API key', base_url: 'Base URL' }

beforeEach(() => fakeHost())
afterEach(cleanup)

describe('TypesafeKeyForm', () => {
  const accountType = { plugin_key: 'typesafe', type: 'apikey' }
  // The shared api_key and base_url cases (keyAndUrlCases).
  for (const c of keyAndUrlCases('ts-abcdefgh', 'https://x.example.com/typesafe', false)) {
    it(c.name, async () => {
      const { w, saved, validate } = mountAccountForm(TypesafeKeyForm, { accountType, stored: c.stored })
      await fillControls(w, LABELS, c.input ?? {})
      expect({ value: saved(), ok: await validate() }).toEqual({ value: c.out, ok: c.ok })
    })
  }

  it('labels, help and placeholders', async () => {
    const { w } = mountAccountForm(TypesafeKeyForm, { accountType })
    await flushPromises()
    expect(w.findAll('label').map((l) => l.text())).toEqual(['API key*', 'Base URL'])
    expect(control(w, LABELS.base_url).attributes('placeholder')).toBe(messages.en.baseUrl.placeholder)
    for (const help of [messages.en.apiKey.help, messages.en.baseUrl.help]) expect(w.text()).toContain(help)
    // The official address, offered as a preset.
    expect(w.findAll('datalist option').map((o) => o.attributes('value'))).toEqual(['https://api.typesafe.ai'])
  })

  it('guarded base URL: the official value only, unless the caller holds account:settings:custom', async () => {
    const guardedType = { ...accountType, guarded_settings: [{ field: 'base_url', allowed: ['https://api.typesafe.ai'] }] }
    const guarded = mountAccountForm(TypesafeKeyForm, { accountType: guardedType })
    await flushPromises()
    expect(guarded.w.find('select').attributes('disabled')).toBeDefined()
    expect(guarded.w.find('select').text()).toContain('https://api.typesafe.ai')
    fakeHost({ granted: ['account:settings:custom'] })
    const free = mountAccountForm(TypesafeKeyForm, { accountType: guardedType })
    await flushPromises()
    expect(free.w.find('input[type="url"]').exists()).toBe(true)
  })

  it('shows a server error at its field', async () => {
    const { w } = mountAccountForm(TypesafeKeyForm, { accountType, errors: { api_key: 'invalid key' } })
    await flushPromises()
    expect(w.text()).toContain('invalid key')
  })
})
