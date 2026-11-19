import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { SECRET_MASK } from '@sub2api/ui'
import { cleanup, control, fakeHost, fillControls, keyAndUrlCases, messagesT, mountAccountForm, type AccountFormCase } from '@sub2api/ui/testing'
import { forms } from './forms'
import messages from './messages'

const { OpenAIKeyForm } = forms(messagesT(messages))
const LABELS = { api_key: 'API key', base_url: 'Base URL', organization: 'Organization ID', project: 'Project ID' }

beforeEach(() => fakeHost())
afterEach(cleanup)

/** The "Advanced" section's toggle (organization and project start behind it). */
const advanced = (w: VueWrapper) => w.get('[data-testid="form-section"] button')

async function openAdvanced(w: VueWrapper) {
  await flushPromises()
  await advanced(w).trigger('click')
}

describe('OpenAIKeyForm', () => {
  const accountType = { plugin_key: 'openai', type: 'apikey' }
  // The shared api_key and base_url cases (keyAndUrlCases).
  const cases: AccountFormCase[] = [
    ...keyAndUrlCases('sk-proj-abcdef', 'https://x.example.com/openai', false),
    { name: 'organization and project', input: { api_key: 'sk-proj-abcdef', organization: 'org-abc', project: 'proj_abc' }, out: { api_key: 'sk-proj-abcdef', organization: 'org-abc', project: 'proj_abc' }, ok: true },
    { name: 'organization of 128 characters', input: { api_key: 'sk-proj-abcdef', organization: 'o'.repeat(128) }, out: { api_key: 'sk-proj-abcdef', organization: 'o'.repeat(128) }, ok: true },
    { name: 'organization of 129 characters', input: { api_key: 'sk-proj-abcdef', organization: 'o'.repeat(129) }, out: { api_key: 'sk-proj-abcdef', organization: 'o'.repeat(129) }, ok: false },
    { name: 'organization with a space', input: { api_key: 'sk-proj-abcdef', organization: 'org abc' }, out: { api_key: 'sk-proj-abcdef', organization: 'org abc' }, ok: false },
    { name: 'project of 129 characters', input: { api_key: 'sk-proj-abcdef', project: 'p'.repeat(129) }, out: { api_key: 'sk-proj-abcdef', project: 'p'.repeat(129) }, ok: false },
    { name: 'project not ASCII', input: { api_key: 'sk-proj-abcdef', project: 'proj_项目' }, out: { api_key: 'sk-proj-abcdef', project: 'proj_项目' }, ok: false },
    { name: 'a cleared organization', input: { api_key: 'sk-proj-abcdef', organization: '' }, out: { api_key: 'sk-proj-abcdef', organization: '' }, ok: true },
    { name: 'edit: stored organization and project kept', stored: { api_key: SECRET_MASK, organization: 'org-abc', project: 'proj_abc' }, out: { api_key: SECRET_MASK, organization: 'org-abc', project: 'proj_abc' }, ok: true }
  ]
  for (const c of cases) {
    it(c.name, async () => {
      const { w, saved, validate } = mountAccountForm(OpenAIKeyForm, { accountType, stored: c.stored })
      if (c.input && ('organization' in c.input || 'project' in c.input)) await openAdvanced(w)
      await fillControls(w, LABELS, c.input ?? {})
      expect({ value: saved(), ok: await validate() }).toEqual({ value: c.out, ok: c.ok })
    })
  }

  it('labels, help and placeholders', async () => {
    const { w } = mountAccountForm(OpenAIKeyForm, { accountType })
    await openAdvanced(w)
    expect(w.findAll('label').map((l) => l.text())).toEqual(['API key*', 'Base URL', 'Organization ID', 'Project ID'])
    expect(control(w, LABELS.api_key).attributes('placeholder')).toBe('sk-...')
    expect(control(w, LABELS.base_url).attributes('placeholder')).toBe(messages.en.baseUrl.placeholder)
    expect(control(w, LABELS.organization).attributes('placeholder')).toBe('org-...')
    expect(control(w, LABELS.project).attributes('placeholder')).toBe('proj_...')
    for (const help of [messages.en.apiKey.help, messages.en.baseUrl.help, messages.en.organization.help, messages.en.project.help]) expect(w.text()).toContain(help)
    // The official address, offered as a preset.
    expect(w.findAll('datalist option').map((o) => o.attributes('value'))).toEqual(['https://api.openai.com'])
  })

  it('organization and project sit in one "Advanced" section, closed at first', async () => {
    const { w } = mountAccountForm(OpenAIKeyForm, { accountType })
    await flushPromises()
    expect(w.findAll('[data-testid="form-section"]')).toHaveLength(1)
    expect(advanced(w).text()).toBe(messages.en.advanced)
    expect(advanced(w).attributes('aria-expanded')).toBe('false')
    expect(w.findAll('label').map((l) => l.text())).toEqual(['API key*', 'Base URL'])
  })

  it('the section opens by itself on an account holding either field', async () => {
    for (const stored of [{ organization: 'org-abc' }, { project: 'proj_abc' }]) {
      const { w } = mountAccountForm(OpenAIKeyForm, { accountType, stored: { api_key: SECRET_MASK, ...stored } })
      await flushPromises()
      expect(advanced(w).attributes('aria-expanded')).toBe('true')
      expect(w.findAll('label').map((l) => l.text())).toEqual(['API key*', 'Base URL', 'Organization ID', 'Project ID'])
    }
  })

  it('the section opens by itself on a server error in it', async () => {
    const { w } = mountAccountForm(OpenAIKeyForm, { accountType, errors: { project: 'invalid project' } })
    await flushPromises()
    expect(advanced(w).attributes('aria-expanded')).toBe('true')
    expect(w.text()).toContain('invalid project')
  })

  it('guarded base URL: the official value only, unless the caller holds account:settings:custom', async () => {
    const guardedType = { ...accountType, guarded_settings: [{ field: 'base_url', allowed: ['https://api.openai.com'] }] }
    const guarded = mountAccountForm(OpenAIKeyForm, { accountType: guardedType })
    await flushPromises()
    expect(guarded.w.find('select').attributes('disabled')).toBeDefined()
    expect(guarded.w.find('select').text()).toContain('https://api.openai.com')
    fakeHost({ granted: ['account:settings:custom'] })
    const free = mountAccountForm(OpenAIKeyForm, { accountType: guardedType })
    await flushPromises()
    expect(free.w.find('input[type="url"]').exists()).toBe(true)
  })

  it('shows a server error at its field', async () => {
    const { w } = mountAccountForm(OpenAIKeyForm, { accountType, errors: { api_key: 'invalid key' } })
    await flushPromises()
    expect(w.text()).toContain('invalid key')
  })
})
