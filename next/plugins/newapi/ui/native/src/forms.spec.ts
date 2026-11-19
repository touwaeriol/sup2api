import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { cleanup, control, fakeHost, fillControls, keyAndUrlCases, messagesT, mountAccountForm } from '@sub2api/ui/testing'
import { forms } from './forms'
import messages from './messages'

const { NewAPIKeyForm } = forms(messagesT(messages))
const LABELS = { api_key: 'API key', base_url: messages.en.baseUrl.label }

beforeEach(() => fakeHost())
afterEach(cleanup)

describe('NewAPIKeyForm', () => {
  const accountType = { plugin_key: 'newapi', type: 'apikey' }
  // The shared api_key and base_url cases (keyAndUrlCases).
  for (const c of keyAndUrlCases('sk-abcdefgh', 'https://site.example.com', true)) {
    it(c.name, async () => {
      const { w, saved, validate } = mountAccountForm(NewAPIKeyForm, { accountType, stored: c.stored })
      await fillControls(w, LABELS, c.input ?? {})
      expect({ value: saved(), ok: await validate() }).toEqual({ value: c.out, ok: c.ok })
    })
  }

  it('labels, help and placeholders', async () => {
    const { w } = mountAccountForm(NewAPIKeyForm, { accountType })
    await flushPromises()
    expect(w.findAll('label').map((l) => l.text())).toEqual([`${messages.en.baseUrl.label}*`, 'API key*'])
    expect(control(w, LABELS.api_key).attributes('placeholder')).toBe('sk-...')
    expect(control(w, LABELS.base_url).attributes('placeholder')).toBe('https://your-new-api.example.com')
    for (const help of [messages.en.apiKey.help, messages.en.baseUrl.help]) expect(w.text()).toContain(help)
  })

  it('shows a server error at its field', async () => {
    const { w } = mountAccountForm(NewAPIKeyForm, { accountType, errors: { api_key: 'invalid key' } })
    await flushPromises()
    expect(w.text()).toContain('invalid key')
  })
})
