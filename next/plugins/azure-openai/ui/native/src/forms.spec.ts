import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { SECRET_MASK } from '@sub2api/ui'
import { cleanup, control, fakeHost, fillControls, keyAndUrlCases, messagesT, mountAccountForm, type AccountFormCase } from '@sub2api/ui/testing'
import { forms } from './forms'
import messages from './messages'

type M = Record<string, unknown>

const { AzureV1Form, AzureDeploymentForm } = forms(messagesT(messages))
const LABELS = { api_key: 'API key', base_url: 'Resource endpoint', api_version: 'API version' }

beforeEach(() => fakeHost())
afterEach(cleanup)

/** The endpoint forms the rule accepts and pasted request URLs it refuses; `fill` the type's other fields. */
function endpointCases(fill: M = {}): AccountFormCase[] {
  const typed = (u: string, ok: boolean) => ({ name: `endpoint ${u}`, input: { api_key: '0123456789abcdef', base_url: u }, out: { ...fill, api_key: '0123456789abcdef', base_url: u }, ok })
  return [
    ...['https://res.openai.azure.com/', 'https://res.openai.azure.com/openai', 'https://res.openai.azure.com/openai/v1/', 'https://res.services.ai.azure.com/api/projects/p1', 'https://res.services.ai.azure.com/api/projects/a%2Fb'].map((u) => typed(u, true)),
    ...['https://res.openai.azure.com/openai/deployments/gpt/chat/completions', 'https://res.openai.azure.com/openai/v1/chat', 'https://user@res.openai.azure.com', 'https://res.openai.azure.com/other'].map((u) => typed(u, false))
  ]
}

describe('AzureV1Form', () => {
  const accountType = { plugin_key: 'azure-openai', type: 'azure_v1' }
  // The shared api_key and base_url cases (keyAndUrlCases) and the endpoint cases.
  const cases = [...keyAndUrlCases('0123456789abcdef', 'https://res.openai.azure.com', true), ...endpointCases()]
  for (const c of cases) {
    it(c.name, async () => {
      const { w, saved, validate } = mountAccountForm(AzureV1Form, { accountType, stored: c.stored })
      await fillControls(w, LABELS, c.input ?? {})
      expect({ value: saved(), ok: await validate() }).toEqual({ value: c.out, ok: c.ok })
    })
  }

  it('labels, help and placeholders', async () => {
    const { w } = mountAccountForm(AzureV1Form, { accountType })
    await flushPromises()
    expect(w.findAll('label').map((l) => l.text())).toEqual(['Resource endpoint*', 'API key*'])
    expect(control(w, LABELS.base_url).attributes('placeholder')).toBe(messages.en.baseUrl.placeholder)
    for (const help of [messages.en.apiKey.help, messages.en.baseUrl.helpV1]) expect(w.text()).toContain(help)
  })

  it('a refused endpoint says what to leave out', async () => {
    const { w, validate } = mountAccountForm(AzureV1Form, { accountType })
    await fillControls(w, LABELS, { base_url: 'https://res.openai.azure.com/openai/deployments/gpt' })
    expect(await validate()).toBe(false)
    expect(w.text()).toContain(messages.en.baseUrl.pattern)
  })

  it('shows a server error at its field', async () => {
    const { w } = mountAccountForm(AzureV1Form, { accountType, errors: { api_key: 'invalid key' } })
    await flushPromises()
    expect(w.text()).toContain('invalid key')
  })
})

describe('AzureDeploymentForm', () => {
  const accountType = { plugin_key: 'azure-openai', type: 'azure_deployment' }
  const typed = (apiVersion: string) => ({
    input: { api_key: '0123456789abcdef', base_url: 'https://res.openai.azure.com', api_version: apiVersion },
    out: { api_key: '0123456789abcdef', base_url: 'https://res.openai.azure.com', api_version: apiVersion }
  })
  // The shared api_key and base_url cases (keyAndUrlCases) and the endpoint cases.
  const cases: AccountFormCase[] = [
    ...keyAndUrlCases('0123456789abcdef', 'https://res.openai.azure.com', true, { api_version: '2024-10-21' }),
    ...endpointCases({ api_version: '2024-10-21' }),
    { name: 'a preview API version', ...typed('2025-04-01-preview'), ok: true },
    { name: 'a cleared API version', ...typed(''), ok: false },
    { name: 'an API version not a date', ...typed('latest'), ok: false },
    { name: 'an API version with another suffix', ...typed('2024-10-21-beta'), ok: false },
    { name: 'edit: an account without an API version gets none filled in', stored: { api_key: SECRET_MASK, base_url: 'https://res.openai.azure.com' }, out: { api_key: SECRET_MASK, base_url: 'https://res.openai.azure.com' }, ok: false }
  ]
  for (const c of cases) {
    it(c.name, async () => {
      const { w, saved, validate } = mountAccountForm(AzureDeploymentForm, { accountType, stored: c.stored })
      await fillControls(w, LABELS, c.input ?? {})
      expect({ value: saved(), ok: await validate() }).toEqual({ value: c.out, ok: c.ok })
    })
  }

  it('labels, help and placeholders', async () => {
    const { w } = mountAccountForm(AzureDeploymentForm, { accountType })
    await flushPromises()
    expect(w.findAll('label').map((l) => l.text())).toEqual(['Resource endpoint*', 'API key*', 'API version*'])
    expect(control(w, LABELS.base_url).attributes('placeholder')).toBe(messages.en.baseUrl.placeholder)
    for (const help of [messages.en.apiKey.help, messages.en.baseUrl.helpDeployment, messages.en.apiVersion.help]) expect(w.text()).toContain(help)
  })

  it('a new account starts with API version 2024-10-21', async () => {
    const { w, credentials } = mountAccountForm(AzureDeploymentForm, { accountType })
    await flushPromises()
    expect(credentials.value).toEqual({ api_version: '2024-10-21' })
    expect((control(w, LABELS.api_version).element as HTMLInputElement).value).toBe('2024-10-21')
  })

  it('shows a server error at its field', async () => {
    const { w } = mountAccountForm(AzureDeploymentForm, { accountType, errors: { api_key: 'invalid key' } })
    await flushPromises()
    expect(w.text()).toContain('invalid key')
  })
})
