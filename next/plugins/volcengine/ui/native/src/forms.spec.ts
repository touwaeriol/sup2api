// The account forms (apikey: createApiKeyForm; relay: RelayForm) as the
// account editor mounts them: each case is an input and the value and
// verdict the form must give for it. The apikey form's api_key and base_url
// cases are the shared ones (keyAndUrlCases); the relay's api_key takes any
// key that is not blank (a short, long, spaced or non-ASCII key is accepted).
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { cleanup, control, fakeHost, fillControls, keyAndUrlCases, mountAccountForm } from '@sub2api/ui/testing'
import { ApiKeyForm } from './apikeyForm'
import RelayForm from './RelayForm.vue'
import messages from './messages'
import { setHost } from './host'

type M = Record<string, any>

const host = (granted: string[] = []) => setHost(fakeHost({ granted, messages, plugin: 'volcengine' }))
beforeEach(() => host())
afterEach(() => {
  cleanup()
  setHost(null)
})

const editor = (form: object, type: string, opts: { stored?: M; errors?: Record<string, string>; accountType?: M } = {}) =>
  mountAccountForm(form, { accountType: { plugin_key: 'volcengine', type, ...opts.accountType }, stored: opts.stored, errors: opts.errors })

const legacyToggle = (w: VueWrapper) => w.get('[data-testid="volcengine-legacy"] button')

/** One input: the stored account (edit) or none (create), then fields typed in order ('' clears). */
interface Case {
  name: string
  stored?: M
  typed: [string, string][]
  out: M
  ok: boolean
}

const LABELS: Record<string, string> = {
  api_key: 'API key',
  base_url: 'Base URL',
  access_key: 'Asset library access key',
  secret_key: 'Asset library secret key',
  asset_base_url: 'Asset endpoint',
  asset_region: 'Asset region'
}
const RELAY_LABELS: Record<string, string> = {
  ...LABELS,
  api_key: 'ui.form.apiKey',
  base_url: 'ui.form.baseUrl',
  video_endpoint: 'Video task collection endpoint',
  asset_endpoint: 'Asset library endpoint',
  api_prefix: 'Legacy text API prefix',
  video_api_prefix: 'Legacy video API prefix',
  asset_base_url: 'Legacy asset URL'
}
const LEGACY = ['api_prefix', 'video_api_prefix', 'asset_base_url']

/** Types a case into the form (opening the legacy section of the relay form for its fields) and reads the result. */
async function run(form: object, labels: Record<string, string>, c: Case, type: string) {
  const { w, saved, validate } = editor(form, type, { stored: c.stored })
  await flushPromises()
  for (const [key, value] of c.typed) {
    if (LEGACY.includes(key) && type === 'relay' && legacyToggle(w).attributes('aria-expanded') === 'false') await legacyToggle(w).trigger('click')
    await fillControls(w, labels, { [key]: value })
  }
  return { value: saved(), ok: await validate() }
}

// Typing into a stored secret and emptying the box again keeps the stored
// value (******, valid; design 5.9): keyAndUrlCases' "edit: the emptied key
// box keeps the stored key", and the relay case below.
const APIKEY: Case[] = [
  { name: 'AK/SK pair', typed: [['api_key', 'ark-key-12345678'], ['access_key', 'AKLT12345678'], ['secret_key', 'secret12345678']], out: { api_key: 'ark-key-12345678', access_key: 'AKLT12345678', secret_key: 'secret12345678' }, ok: true },
  { name: 'access key alone (the pair is the server rule)', typed: [['api_key', 'ark-key-12345678'], ['access_key', 'AKLT12345678']], out: { api_key: 'ark-key-12345678', access_key: 'AKLT12345678' }, ok: true },
  { name: 'access key of 257 characters', typed: [['api_key', 'ark-key-12345678'], ['access_key', 'a'.repeat(257)]], out: { api_key: 'ark-key-12345678', access_key: 'a'.repeat(257) }, ok: false },
  { name: 'access key with a space', typed: [['api_key', 'ark-key-12345678'], ['access_key', 'AKLT 1234']], out: { api_key: 'ark-key-12345678', access_key: 'AKLT 1234' }, ok: false },
  { name: 'secret key with a space', typed: [['api_key', 'ark-key-12345678'], ['secret_key', 'sec ret']], out: { api_key: 'ark-key-12345678', secret_key: 'sec ret' }, ok: false },
  { name: 'secret key of 257 characters', typed: [['api_key', 'ark-key-12345678'], ['secret_key', 's'.repeat(257)]], out: { api_key: 'ark-key-12345678', secret_key: 's'.repeat(257) }, ok: false },
  { name: 'cleared access key', typed: [['api_key', 'ark-key-12345678'], ['access_key', '']], out: { api_key: 'ark-key-12345678', access_key: '' }, ok: true },
  { name: 'cleared secret key', typed: [['api_key', 'ark-key-12345678'], ['secret_key', '']], out: { api_key: 'ark-key-12345678', secret_key: '' }, ok: true },
  { name: 'region', typed: [['api_key', 'ark-key-12345678'], ['asset_region', 'ap-southeast-1']], out: { api_key: 'ark-key-12345678', asset_region: 'ap-southeast-1' }, ok: true },
  { name: 'region upper case', typed: [['api_key', 'ark-key-12345678'], ['asset_region', 'CN-Beijing']], out: { api_key: 'ark-key-12345678', asset_region: 'CN-Beijing' }, ok: false },
  { name: 'region of 65 characters', typed: [['api_key', 'ark-key-12345678'], ['asset_region', 'a'.repeat(65)]], out: { api_key: 'ark-key-12345678', asset_region: 'a'.repeat(65) }, ok: false },
  { name: 'cleared region', typed: [['api_key', 'ark-key-12345678'], ['asset_region', '']], out: { api_key: 'ark-key-12345678', asset_region: '' }, ok: true },
  { name: 'edit: stored secrets untouched, an unknown key kept', stored: { api_key: '******', secret_key: '******', access_key: 'AKLT12345678', legacy: 'x' }, typed: [], out: { api_key: '******', secret_key: '******', access_key: 'AKLT12345678', legacy: 'x' }, ok: true },
  { name: 'edit: stored secrets, a field changed', stored: { api_key: '******', secret_key: '******', access_key: 'AKLT12345678' }, typed: [['asset_region', 'cn-beijing']], out: { api_key: '******', secret_key: '******', access_key: 'AKLT12345678', asset_region: 'cn-beijing' }, ok: true },
  { name: 'edit: a stored secret replaced', stored: { api_key: '******', secret_key: '******', access_key: 'AKLT12345678' }, typed: [['secret_key', 'new-secret-123']], out: { api_key: '******', secret_key: 'new-secret-123', access_key: 'AKLT12345678' }, ok: true },
  { name: 'asset endpoint', typed: [['api_key', 'ark-key-12345678'], ['asset_base_url', 'https://ark.cn-beijing.volcengineapi.com']], out: { api_key: 'ark-key-12345678', asset_base_url: 'https://ark.cn-beijing.volcengineapi.com' }, ok: true },
  { name: 'cleared asset endpoint', typed: [['api_key', 'ark-key-12345678'], ['asset_base_url', '']], out: { api_key: 'ark-key-12345678' }, ok: true },
  { name: 'asset endpoint with a fragment', typed: [['api_key', 'ark-key-12345678'], ['asset_base_url', 'https://ark.example.com/#a']], out: { api_key: 'ark-key-12345678', asset_base_url: 'https://ark.example.com/#a' }, ok: false },
]
const RELAY: Case[] = [
  { name: 'key only', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com' }, ok: true },
  { name: 'no key', typed: [], out: {}, ok: false },
  { name: 'short key', typed: [['api_key', '1234567'], ['base_url', 'https://relay.example.com']], out: { api_key: '1234567', base_url: 'https://relay.example.com' }, ok: true },
  { name: 'key of 4096 characters', typed: [['api_key', 'k'.repeat(4096)], ['base_url', 'https://relay.example.com']], out: { api_key: 'k'.repeat(4096), base_url: 'https://relay.example.com' }, ok: true },
  { name: 'key with a space', typed: [['api_key', 'ark key 12345678'], ['base_url', 'https://relay.example.com']], out: { api_key: 'ark key 12345678', base_url: 'https://relay.example.com' }, ok: true },
  { name: 'key not ASCII', typed: [['api_key', 'ark-密钥-12345678'], ['base_url', 'https://relay.example.com']], out: { api_key: 'ark-密钥-12345678', base_url: 'https://relay.example.com' }, ok: true },
  { name: 'blank key', typed: [['api_key', '   '], ['base_url', 'https://relay.example.com']], out: { api_key: '   ', base_url: 'https://relay.example.com' }, ok: false },
  { name: 'cleared key', typed: [['api_key', ''], ['base_url', 'https://relay.example.com']], out: { api_key: '', base_url: 'https://relay.example.com' }, ok: false },
  { name: 'AK/SK pair', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['access_key', 'AKLT12345678'], ['secret_key', 'secret12345678']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', access_key: 'AKLT12345678', secret_key: 'secret12345678' }, ok: true },
  { name: 'access key alone (the pair is the server rule)', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['access_key', 'AKLT12345678']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', access_key: 'AKLT12345678' }, ok: true },
  { name: 'access key of 257 characters', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['access_key', 'a'.repeat(257)]], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', access_key: 'a'.repeat(257) }, ok: false },
  { name: 'access key with a space', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['access_key', 'AKLT 1234']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', access_key: 'AKLT 1234' }, ok: false },
  { name: 'secret key with a space', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['secret_key', 'sec ret']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', secret_key: 'sec ret' }, ok: false },
  { name: 'secret key of 257 characters', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['secret_key', 's'.repeat(257)]], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', secret_key: 's'.repeat(257) }, ok: false },
  { name: 'cleared access key', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['access_key', '']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', access_key: '' }, ok: true },
  { name: 'cleared secret key', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['secret_key', '']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', secret_key: '' }, ok: true },
  { name: 'region', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['asset_region', 'ap-southeast-1']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', asset_region: 'ap-southeast-1' }, ok: true },
  { name: 'region upper case', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['asset_region', 'CN-Beijing']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', asset_region: 'CN-Beijing' }, ok: false },
  { name: 'region of 65 characters', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['asset_region', 'a'.repeat(65)]], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', asset_region: 'a'.repeat(65) }, ok: false },
  { name: 'cleared region', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['asset_region', '']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', asset_region: '' }, ok: true },
  { name: 'edit: stored secrets untouched, an unknown key kept', stored: { api_key: '******', secret_key: '******', access_key: 'AKLT12345678', legacy: 'x', base_url: 'https://relay.example.com' }, typed: [], out: { api_key: '******', secret_key: '******', access_key: 'AKLT12345678', legacy: 'x', base_url: 'https://relay.example.com' }, ok: true },
  { name: 'edit: stored secrets, a field changed', stored: { api_key: '******', secret_key: '******', access_key: 'AKLT12345678', base_url: 'https://relay.example.com' }, typed: [['asset_region', 'cn-beijing']], out: { api_key: '******', secret_key: '******', access_key: 'AKLT12345678', base_url: 'https://relay.example.com', asset_region: 'cn-beijing' }, ok: true },
  { name: 'edit: a stored secret replaced', stored: { api_key: '******', secret_key: '******', access_key: 'AKLT12345678', base_url: 'https://relay.example.com' }, typed: [['secret_key', 'new-secret-123']], out: { api_key: '******', secret_key: 'new-secret-123', access_key: 'AKLT12345678', base_url: 'https://relay.example.com' }, ok: true },
  { name: 'edit: a stored secret typed then emptied', stored: { api_key: '******', base_url: 'https://relay.example.com' }, typed: [['api_key', '']], out: { api_key: '******', base_url: 'https://relay.example.com' }, ok: true },
  { name: 'no base URL', typed: [['api_key', 'ark-key-12345678']], out: { api_key: 'ark-key-12345678' }, ok: false },
  { name: 'cleared base URL', typed: [['api_key', 'ark-key-12345678'], ['base_url', '']], out: { api_key: 'ark-key-12345678', base_url: '' }, ok: false },
  { name: 'base URL of 7 characters', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'http://']], out: { api_key: 'ark-key-12345678', base_url: 'http://' }, ok: false },
  { name: 'base URL with a query', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com/?a=1']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com/?a=1' }, ok: false },
  { name: 'video endpoint path', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['video_endpoint', '/api/v3/contents/generations/tasks']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', video_endpoint: '/api/v3/contents/generations/tasks' }, ok: true },
  { name: 'video endpoint URL', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['video_endpoint', 'https://video.example.com/tasks']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', video_endpoint: 'https://video.example.com/tasks' }, ok: true },
  { name: 'video endpoint relative', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['video_endpoint', 'api/v3/tasks']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', video_endpoint: 'api/v3/tasks' }, ok: false },
  { name: 'video endpoint //', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['video_endpoint', '//x.example.com/tasks']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', video_endpoint: '//x.example.com/tasks' }, ok: false },
  { name: 'video endpoint of 2049 characters', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['video_endpoint', '/' + 'e'.repeat(2048)]], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', video_endpoint: '/' + 'e'.repeat(2048) }, ok: false },
  { name: 'cleared video endpoint', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['video_endpoint', '']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', video_endpoint: '' }, ok: true },
  { name: 'asset endpoint path', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['asset_endpoint', '/api/support/v1/asset']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', asset_endpoint: '/api/support/v1/asset' }, ok: true },
  { name: 'asset endpoint with a space', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['asset_endpoint', '/api/a b']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', asset_endpoint: '/api/a b' }, ok: false },
  { name: 'asset endpoint URL', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['asset_endpoint', 'https://assets.example.com/api/support/v1/asset']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', asset_endpoint: 'https://assets.example.com/api/support/v1/asset' }, ok: true },
  { name: 'asset endpoint of 2049 characters', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['asset_endpoint', '/' + 'e'.repeat(2048)]], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', asset_endpoint: '/' + 'e'.repeat(2048) }, ok: false },
  { name: 'cleared asset endpoint', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['asset_endpoint', '']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', asset_endpoint: '' }, ok: true },
  { name: 'api prefix', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['api_prefix', '/api/v3']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', api_prefix: '/api/v3' }, ok: true },
  { name: 'api prefix /', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['api_prefix', '/']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', api_prefix: '/' }, ok: true },
  { name: 'api prefix without a slash', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['api_prefix', 'v1']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', api_prefix: 'v1' }, ok: false },
  { name: 'api prefix of 129 characters', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['api_prefix', '/' + 'p'.repeat(128)]], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', api_prefix: '/' + 'p'.repeat(128) }, ok: false },
  { name: 'cleared api prefix', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['api_prefix', '']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', api_prefix: '' }, ok: true },
  { name: 'video api prefix', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['video_api_prefix', '/doubao/api/v3']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', video_api_prefix: '/doubao/api/v3' }, ok: true },
  { name: 'video api prefix //', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['video_api_prefix', '//v1']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', video_api_prefix: '//v1' }, ok: false },
  { name: 'cleared video api prefix', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['video_api_prefix', '']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', video_api_prefix: '' }, ok: true },
  { name: 'legacy asset URL', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['asset_base_url', 'https://relay.example.com/api/support/v1/asset']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', asset_base_url: 'https://relay.example.com/api/support/v1/asset' }, ok: true },
  { name: 'legacy asset URL with a query', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['asset_base_url', 'https://relay.example.com/?a=1']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', asset_base_url: 'https://relay.example.com/?a=1' }, ok: false },
  { name: 'cleared legacy asset URL', typed: [['api_key', 'ark-key-12345678'], ['base_url', 'https://relay.example.com'], ['asset_base_url', '']], out: { api_key: 'ark-key-12345678', base_url: 'https://relay.example.com', asset_base_url: '' }, ok: true },
  { name: 'edit: legacy values kept', stored: { api_key: '******', base_url: 'https://relay.example.com', api_prefix: '/api/v3', asset_base_url: 'https://r.example.com/asset' }, typed: [], out: { api_key: '******', base_url: 'https://relay.example.com', api_prefix: '/api/v3', asset_base_url: 'https://r.example.com/asset' }, ok: true },
]

describe('apikey form', () => {
  for (const c of keyAndUrlCases('ark-key-12345678', 'https://ark.ap-southeast.bytepluses.com', false)) {
    it(`v-model and verdict: ${c.name}`, async () => {
      const { w, saved, validate } = editor(ApiKeyForm, 'apikey', { stored: c.stored })
      await fillControls(w, LABELS, c.input ?? {})
      expect({ value: saved(), ok: await validate() }).toEqual({ value: c.out, ok: c.ok })
    })
  }
  for (const c of APIKEY) {
    it(`v-model and verdict: ${c.name}`, async () => {
      expect(await run(ApiKeyForm, LABELS, c, 'apikey')).toEqual({ value: c.out, ok: c.ok })
    })
  }

  it('labels, section, placeholders and help', async () => {
    const { w } = editor(ApiKeyForm, 'apikey')
    await flushPromises()
    expect(w.findAll('label').map((l) => l.text())).toEqual(['API key*', 'Base URL', 'Asset library access key', 'Asset library secret key', 'Asset endpoint', 'Asset region'])
    expect(w.findAll('h4').map((x) => x.text())).toEqual(['Asset library (optional)'])
    expect(w.text()).toContain(messages.en.asset.sectionHelp)
    expect(control(w, 'API key').attributes('placeholder')).toBe('xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx')
    expect(control(w, 'Base URL').attributes('placeholder')).toBe(messages.en.apikey.baseUrlPlaceholder)
    expect(control(w, 'Asset region').attributes('placeholder')).toBe(messages.en.asset.regionPlaceholder)
    expect(w.text()).toContain(messages.en.apikey.accessKeyHelp)
  })

  it('guarded base URL and asset endpoint: the official values only, unless the caller holds account:settings:custom', async () => {
    const accountType = {
      plugin_key: 'volcengine',
      type: 'apikey',
      guarded_settings: [
        { field: 'base_url', allowed: ['https://ark.cn-beijing.volces.com', 'https://ark.ap-southeast.bytepluses.com'] },
        { field: 'asset_base_url', allowed: ['https://ark.cn-beijing.volcengineapi.com'] }
      ]
    }
    const guarded = editor(ApiKeyForm, 'apikey', { accountType })
    await flushPromises()
    const selects = guarded.w.findAll('select')
    expect(selects).toHaveLength(2)
    expect(selects[0].attributes('disabled')).toBeUndefined()
    expect(selects[1].attributes('disabled')).toBeDefined()
    expect(guarded.w.find('input[type="url"]').exists()).toBe(false)
    host(['account:settings:custom'])
    const free = editor(ApiKeyForm, 'apikey', { accountType })
    await flushPromises()
    expect(free.w.findAll('select')).toHaveLength(0)
    expect(free.w.findAll('input[type="url"]')).toHaveLength(2)
  })

  it('a stored secret key can be cleared, the API key only replaced', async () => {
    const { w, credentials } = editor(ApiKeyForm, 'apikey', { stored: { api_key: '******', access_key: 'AKLT12345678', secret_key: '******' } })
    await flushPromises()
    const clear = w.findAll('button').filter((b) => b.text() === 'Clear the saved value')
    expect(clear).toHaveLength(1)
    await clear[0].trigger('click')
    await flushPromises()
    expect(credentials.value).toEqual({ api_key: '******', access_key: 'AKLT12345678', secret_key: '' })
  })

  it('server errors at their fields, the rest above the form', async () => {
    const { w } = editor(ApiKeyForm, 'apikey', { errors: { secret_key: 'secret_key is required when access_key is set', '': 'credentials must be a JSON object' } })
    await flushPromises()
    expect(w.get('[data-testid="credential-form-errors"]').text()).toBe('credentials must be a JSON object')
    expect(w.text()).toContain('secret_key is required when access_key is set')
  })
})

describe('relay form', () => {
  for (const c of RELAY) {
    it(`v-model and verdict: ${c.name}`, async () => {
      expect(await run(RelayForm, RELAY_LABELS, c, 'relay')).toEqual({ value: c.out, ok: c.ok })
    })
  }

  it('labels, sections and the legacy settings closed for a new account', async () => {
    const { w } = editor(RelayForm, 'relay')
    await flushPromises()
    expect(w.findAll('label').map((l) => l.text())).toEqual([
      'ui.form.apiKey*',
      'ui.form.baseUrl*',
      'Video task collection endpoint',
      'Asset library endpoint',
      'Asset library access key',
      'Asset library secret key',
      'Asset region'
    ])
    expect(w.text()).toContain('Asset library (optional)')
    expect(w.text()).toContain(messages.en.asset.sectionHelp)
    expect(legacyToggle(w).text()).toContain('Legacy settings')
    expect(legacyToggle(w).attributes('aria-expanded')).toBe('false')
    expect(w.findAll('button').filter((b) => b.text() === 'Clear the saved value')).toHaveLength(0)
  })

  it('the legacy settings open by themselves while one holds a value or an error', async () => {
    const stored = editor(RelayForm, 'relay', { stored: { api_key: '******', base_url: 'https://relay.example.com', video_api_prefix: '/v3' } })
    await flushPromises()
    expect(legacyToggle(stored.w).attributes('aria-expanded')).toBe('true')
    expect(control(stored.w, 'Legacy video API prefix').element).toHaveProperty('value', '/v3')
    const refused = editor(RelayForm, 'relay', { errors: { 'credentials.api_prefix': 'api_prefix must start with "/"' } })
    await flushPromises()
    expect(legacyToggle(refused.w).attributes('aria-expanded')).toBe('true')
    expect(refused.w.text()).toContain('api_prefix must start with "/"')
  })

  it('a stored secret key can be cleared', async () => {
    const { w, credentials } = editor(RelayForm, 'relay', { stored: { api_key: '******', base_url: 'https://relay.example.com', secret_key: '******' } })
    await flushPromises()
    const clear = w.findAll('button').filter((b) => b.text() === 'Clear the saved value')
    expect(clear).toHaveLength(1)
    await clear[0].trigger('click')
    await flushPromises()
    expect(credentials.value.secret_key).toBe('')
  })
})
