import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { SECRET_MASK } from '@sub2api/ui'
import { cleanup, fakeHost, fillControls, messagesT, mountAccountForm } from '@sub2api/ui/testing'
import VertexKeyFileForm from './VertexKeyFileForm.vue'
import { apiKeyForm } from './apiKeyForm'
import messages from './messages'
import { setHost } from './host'
import { CASES, LABELS, type VertexType } from './form.cases'

const VertexApiKeyForm = apiKeyForm(messagesT(messages))
const FORM: Record<VertexType, object> = { vertex_apikey: VertexApiKeyForm, vertex_sa: VertexKeyFileForm, vertex_authorized_user: VertexKeyFileForm }
const form = (type: VertexType, opts: { stored?: Record<string, unknown>; errors?: Record<string, string> } = {}) =>
  mountAccountForm(FORM[type], { accountType: { plugin_key: 'vertex', type }, ...opts })

beforeEach(() => setHost(fakeHost({ messages, plugin: 'vertex' })))
afterEach(() => {
  cleanup()
  setHost(null)
})

describe('vertex forms: v-model and verdict of every case', () => {
  for (const c of CASES) {
    it(c.name, async () => {
      const { w, saved, validate } = form(c.type, { stored: c.stored })
      await fillControls(w, LABELS[c.type], c.input ?? {})
      expect(saved()).toEqual(c.out)
      expect(await validate()).toBe(c.ok)
    })
  }
})

// access_token, expires_at and token_for are written by the core when it
// renews the token. A save of the form must hand back exactly what the
// editor loaded for them, whatever else changes: a dropped or rewritten
// token would make the next request wait for a fresh one.
describe('VertexKeyFileForm: the token state the core writes', () => {
  const token = { access_token: SECRET_MASK, expires_at: 1800000000, token_for: 'fp' }
  for (const type of ['vertex_sa', 'vertex_authorized_user'] as const) {
    it(`${type}: never shown, kept through every change`, async () => {
      const field = Object.keys(LABELS[type])[0]
      const stored = { [field]: SECRET_MASK, project_id: 'my-project-123456', location: 'global', ...token }
      const { w, credentials, saved, validate } = form(type, { stored })
      await flushPromises()
      expect(w.findAll('input').length).toBe(3)
      expect(w.html()).not.toContain('1800000000')
      await fillControls(w, LABELS[type], { [field]: '{"type":"x"}', project_id: 'other-project-1', location: '' })
      expect(credentials.value).toMatchObject(token)
      expect(saved()).toEqual({ ...stored, [field]: '{"type":"x"}', project_id: 'other-project-1', location: '' })
      expect(await validate()).toBe(true)
    })
  }
})

describe('vertex forms: fields and texts', () => {
  const labels = (type: VertexType) => form(type).w.findAll('label').map((l) => l.text())

  it('shows the key file of the type, then project and location', () => {
    expect(labels('vertex_sa')).toEqual(['Service account key*', 'Project ID', 'Location'])
    expect(labels('vertex_authorized_user')).toEqual(['User credentials file*', 'Project ID', 'Location'])
    expect(labels('vertex_apikey')).toEqual(['API key*', 'Project ID (optional)', 'Location (optional)'])
  })

  it('key files are secret boxes with their own placeholders', () => {
    const sa = form('vertex_sa').w.findAll('input')
    expect(sa.map((i) => i.attributes('type'))).toEqual(['password', 'text', 'text'])
    expect(sa.map((i) => i.attributes('placeholder'))).toEqual(['Paste the whole JSON key file', "Leave empty to use the key file's project", 'global'])
    const user = form('vertex_authorized_user').w.findAll('input')
    expect(user.map((i) => i.attributes('placeholder'))).toEqual(['Paste the whole application_default_credentials.json', 'my-project-123456', 'global'])
  })

  it('the API key form names the optional project address', () => {
    const { w } = form('vertex_apikey')
    expect(w.find('h4').text()).toBe('Project address (optional)')
    expect(w.text()).toContain('or leave both empty to use the express address')
    expect(w.findAll('input').map((i) => i.attributes('placeholder'))).toEqual([undefined, 'Leave empty for the express address', 'Leave empty for the express address'])
    expect(w.findAll('input').map((i) => i.classes('font-mono'))).toEqual([false, true, true])
  })

  it('shows the server errors at their fields until the field changes', async () => {
    const { w } = form('vertex_apikey', { errors: { location: 'location is required when the other of project ID and location is set' } })
    await flushPromises()
    expect(w.text()).toContain('location is required')
    await fillControls(w, LABELS.vertex_apikey, { location: 'global' })
    expect(w.text()).not.toContain('location is required')
  })
})
