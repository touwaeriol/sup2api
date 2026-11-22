import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, shallowMount } from '@vue/test-utils'
import { ApiError } from '@sub2api/host'
import { i18n } from '@/i18n'
import type { Account, AccountType, Group } from '@/api/types'
import { useGroupsLookup } from '@/composables/lookups'
import AccountEditor from './AccountEditor.vue'
import ProxyPicker from '@/components/ProxyPicker.vue'
import ModelLimitsEditor from './ModelLimitsEditor.vue'
import ModelMappingEditor from './ModelMappingEditor.vue'
import PluginSlot from '@/components/plugin/PluginSlot.vue'
import { SSpinner, STagInput } from '@sub2api/ui'
import AccountEditorNav from './AccountEditorNav.vue'
import ParamOverrideField from './paramOverride/ParamOverrideField.vue'
import HeaderOverrideField from './headerOverride/HeaderOverrideField.vue'
import AccountUsageLimitCard from './AccountUsageLimitCard.vue'
import RateLimitEditor from './controls/RateLimitEditor.vue'
import ModelProtocolsCard from './protocols/ModelProtocolsCard.vue'
import OwnProtocolsField from './protocols/OwnProtocolsField.vue'
import { codex, openai } from './protocols/fixtures'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), patch: vi.fn(), put: vi.fn(), list: vi.fn().mockResolvedValue({ items: [] }), toast: vi.fn() }))
vi.mock('@sub2api/host', async (original) => ({ ...await original<object>(), api: mocks }))
vi.mock('@sub2api/ui', async (original) => ({ ...await original<typeof import('@sub2api/ui')>(), toast: mocks.toast }))
const ownership = vi.hoisted(() => ({ scope: 'all' as 'all' | 'own' }))
const pluginStore = vi.hoisted(() => ({ listed: true, pending: {} as Record<string, boolean>, errors: {} as Record<string, string>, plugin: () => null, uiMismatch: () => null, component: (() => null) as (key: string, name?: string) => unknown, slotEntries: (() => []) as (slot: string) => unknown[], refresh: vi.fn() }))
vi.mock('@/stores/plugins', () => ({ usePluginStore: () => pluginStore, assetURL: () => '' }))
const perms = vi.hoisted(() => ({ denied: [] as string[] }))
// Keys compare in their canonical spelling (CONTRACTS §21).
vi.mock('@/stores/auth', async () => {
  const { canonicalPermission } = await import('@/authz/implies')
  const canonical = (k: string) => canonicalPermission(k) ?? k
  return { useAuthStore: () => ({ has: (p: string | string[]) => [p].flat().some(k => !perms.denied.map(canonical).includes(canonical(k))), scopes: () => [] }) }
})
vi.mock('@/composables/useScope', async original => ({ ...await original<typeof import('@/composables/useScope')>(), useScope: () => ({ can: () => true, level: () => ownership.scope }) }))
vi.mock('@/composables/lookups', async (original) => ({ ...await original<object>(), useProxiesLookup: () => ({}) }))

enableAutoUnmount(afterEach)
// The plugin's native credentials form; each test starts with it registered.
const KeyForm = { name: 'KeyForm', template: '<div />' }
beforeEach(() => {
  Object.assign(pluginStore, { listed: true, pending: {}, errors: {}, component: () => KeyForm })
})
const accountType = { plugin_key: 'claude', type: 'apikey', creation_group: 'claude', form: { mode: 'native', component: 'KeyForm' }, platforms: [], default_models: [], default_model_mapping: {} } as unknown as AccountType

function render(account?: Account, realSelect = false, type: AccountType = accountType) {
  return shallowMount(AccountEditor, { props: { accountType: type, account }, global: { plugins: [i18n], stubs: {
    // The models card is rendered (not stubbed) so its tag select is reachable.
    AccountModelsCard: false,
    RateLimitEditor: false,
    SessionLimitInput: false,
    HeaderPassthroughEditor: false,
    SCard: { template: '<div><slot name="title" /><slot name="actions" /><slot /></div>' },
    SSettingRow: { template: '<div><slot /></div>' },
    SSettingsGroup: { template: '<div><slot /></div>' },
    SField: { props: ['error'], template: '<div><slot /><p v-if="error">{{ error }}</p><slot v-else name="hint" /></div>' },
    SHint: { template: '<p><slot /></p>' },
    SAlert: { props: ['title'], template: '<div><b v-if="title">{{ title }}</b><slot /></div>' },
    SSegmented: false,
    SSelect: realSelect ? false : { props: ['modelValue', 'options', 'disabled', 'searchable'], emits: ['update:modelValue'], template: '<input :value="modelValue" :disabled="disabled" @input="$emit(\'update:modelValue\', $event.target.value)" />' },
    // Like SInput, type="number" emits a number (null when empty).
    SInput: { props: ['modelValue', 'type'], emits: ['update:modelValue'], template: '<input :type="type" :value="modelValue" @input="$emit(\'update:modelValue\', type === \'number\' ? ($event.target.value === \'\' ? null : Number($event.target.value)) : $event.target.value)" />' },
    // Mounts a native form the way plugin pages do: rendered for real.
    PluginComponent: false,
    // A native form whose expose (validate) the editor calls.
    ValidatingForm: false,
  } } })
}

function savedAccount(): Account {
  return { id: 1, name: 'saved', proxy_id: 10, settings: {}, credentials: {}, group_ids: [], models: [], model_mapping: {} } as unknown as Account
}

async function chooseProxy(w: ReturnType<typeof render>, id: number | null) {
  w.getComponent(ProxyPicker).vm.$emit('update:modelValue', id)
  await flushPromises()
}

describe('a form mode this console does not render', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    i18n.global.locale.value = 'zh'
    mocks.get.mockResolvedValue({ enabled: true })
    mocks.patch.mockResolvedValue(savedAccount())
  })

  // An older package's form mode (no native or iframe form): the editor asks
  // to upgrade the plugin and sends the credentials back unchanged.
  const oldType = { ...accountType, form: { mode: 'schema' } } as unknown as AccountType

  it('asks to upgrade the plugin and saves the other sections with the credentials as they were', async () => {
    const w = render({ ...savedAccount(), credentials: { api_key: '******' } } as unknown as Account, false, oldType)
    await flushPromises()
    expect(w.find('[data-testid="form-unavailable-old"]').exists()).toBe(true)
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.patch).toHaveBeenCalledWith('/accounts/1', expect.objectContaining({ credentials: { api_key: '******' } }))
  })

  // A refused credential field has no plugin form to show it: the editor lists it.
  const refusedKey = () => new ApiError(400, { code: 'invalid_argument', message: 'invalid', details: { fields: [{ field: 'credentials.extra', code: 'unknown', message: 'unknown field' }] } })

  it('lists the refused credential fields itself', async () => {
    mocks.patch.mockRejectedValueOnce(refusedKey())
    const w = render({ ...savedAccount(), credentials: { api_key: '******', extra: 'x' } } as unknown as Account, false, oldType)
    await flushPromises()
    expect(w.find('[data-testid="credential-errors"]').exists()).toBe(false)
    await w.get('form').trigger('submit'); await flushPromises()
    expect(w.get('[data-testid="credential-errors"]').text()).toBe('插件未接受以下凭证字段extra: unknown field')
    expect(w.getComponent(AccountEditorNav).props('errors')).toMatchObject({ connection: true })
  })

  it('lists them when the native form failed to load, and leaves them to a loaded form', async () => {
    pluginStore.component = () => null
    mocks.patch.mockRejectedValueOnce(refusedKey())
    const w = render(savedAccount())
    await flushPromises()
    await w.get('form').trigger('submit'); await flushPromises()
    expect(w.get('[data-testid="credential-errors"]').text()).toContain('extra: unknown field')

    pluginStore.component = () => KeyForm
    mocks.patch.mockRejectedValueOnce(refusedKey())
    const loaded = render(savedAccount())
    await flushPromises()
    await loaded.get('form').trigger('submit'); await flushPromises()
    expect(loaded.getComponent(KeyForm).vm.$attrs.errors).toEqual({ extra: 'unknown field' })
    expect(loaded.find('[data-testid="credential-errors"]').exists()).toBe(false)
  })
})

describe('account forwarding and proxy', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    i18n.global.locale.value = 'zh'
    mocks.get.mockImplementation((path: string) => Promise.resolve(path === '/accounts/auto-disable' ? { enabled: true } : {}))
    mocks.patch.mockResolvedValue(savedAccount())
    mocks.post.mockResolvedValue(savedAccount())
  })

  it('loads and saves the header passthrough rules (CONTRACTS §81)', async () => {
    const w = render({ ...savedAccount(), header_passthrough: ['*'] } as Account)
    await flushPromises()
    const rules = w.getComponent(STagInput)
    expect(rules.props('modelValue')).toEqual(['*'])
    expect(w.get('[data-testid="account-header-passthrough"]').element.closest('[data-editor-section]')?.getAttribute('data-editor-section')).toBe('advanced')
    const advanced = () => w.getComponent(AccountEditorNav).props('sections').find(s => s.key === 'advanced')
    expect(advanced()).toMatchObject({ label: '高级', summary: '透传 * · 超时跟随' })
    rules.vm.$emit('update:modelValue', ['*', 'regex:^x-claude-code-'])
    await flushPromises()
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.patch).toHaveBeenCalledWith('/accounts/1', expect.objectContaining({ header_passthrough: ['*', 'regex:^x-claude-code-'] }))
  })

  it('loads, summarizes and saves the parameter override (CONTRACTS §84)', async () => {
    const override = { operations: [{ mode: 'set', path: 'temperature', value: 0.7 }, { mode: 'delete_header', path: 'x-foo' }] }
    const w = render({ ...savedAccount(), param_override: override } as Account)
    await flushPromises()
    const field = w.getComponent(ParamOverrideField)
    expect(field.props('modelValue')).toEqual(override)
    expect(field.element.closest('[data-editor-section]')?.getAttribute('data-editor-section')).toBe('advanced')
    const advanced = () => w.getComponent(AccountEditorNav).props('sections').find(s => s.key === 'advanced')
    expect(advanced()?.summary).toBe('不透传 · 覆盖 2 条 · 超时跟随')
    field.vm.$emit('update:modelValue', null)
    await flushPromises()
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.patch).toHaveBeenCalledWith('/accounts/1', expect.objectContaining({ param_override: null }))
  })

  it('loads, summarizes and saves the header override, handing it to the parameter override dry run (CONTRACTS §108)', async () => {
    const headers = { 'User-Agent': 'my-client/1.0', 'x-foo': '{unset}' }
    const w = render({ ...savedAccount(), header_override: headers } as Account)
    await flushPromises()
    const field = w.getComponent(HeaderOverrideField)
    expect(field.props('modelValue')).toEqual(headers)
    expect(field.element.closest('[data-editor-section]')?.getAttribute('data-editor-section')).toBe('advanced')
    expect(w.getComponent(ParamOverrideField).props('headerOverride')).toEqual(headers)
    const advanced = () => w.getComponent(AccountEditorNav).props('sections').find(s => s.key === 'advanced')
    expect(advanced()?.summary).toBe('不透传 · 请求头覆盖 2 项 · 超时跟随')
    mocks.patch.mockRejectedValueOnce(new ApiError(400, { code: 'invalid_argument', message: 'invalid', details: { fields: [{ field: 'header_override', code: 'blocked', message: 'Authorization: blocked' }] } }))
    field.vm.$emit('update:modelValue', { Authorization: 'x' })
    await flushPromises()
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.patch).toHaveBeenCalledWith('/accounts/1', expect.objectContaining({ header_override: { Authorization: 'x' } }))
    expect(field.props('error')).toBe('Authorization: blocked')
    expect(w.getComponent(AccountEditorNav).props('errors')).toMatchObject({ advanced: true })
    field.vm.$emit('update:modelValue', null)
    await flushPromises()
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.patch).toHaveBeenLastCalledWith('/accounts/1', expect.objectContaining({ header_override: null }))
  })

  it('loads, explains and saves the conversion loss policy (CONVERT-MATRIX §6.3)', async () => {
    // A Codex account adapts chat to Responses: something may be lost, so the policy applies.
    const w = render({ ...savedAccount(), group_ids: [1, 2] } as Account, false, { ...accountType, ...codex })
    await flushPromises()
    const { groups } = useGroupsLookup()
    const hint = () => w.get('[data-testid="account-conversion-policy-hint"]').text()
    expect(hint()).toBe('生效：允许（默认）')
    groups.value = [{ id: 1, name: 'default' }, { id: 2, name: 'vip', conversion_loss_policy: 'safe' }, { id: 3, name: 'other', conversion_loss_policy: 'strict' }] as Group[]
    await flushPromises()
    expect(hint()).toBe('继承账号分组：default=允许, vip=安全')
    expect(w.get('[data-testid="account-conversion-policy"]').element.closest('[data-editor-section]')?.getAttribute('data-editor-section')).toBe('advanced')
    await w.get('[data-testid="account-conversion-policy-select"]').setValue('strict')
    expect(hint()).toBe('生效：严格（覆盖账号分组设置）')
    expect(w.getComponent(AccountEditorNav).props('sections').find(s => s.key === 'advanced')?.summary).toBe('不透传 · 转换：严格 · 超时跟随')
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.patch).toHaveBeenCalledWith('/accounts/1', expect.objectContaining({ conversion_loss_policy: 'strict' }))
    groups.value = []
  })

  it('keeps an inherited conversion loss policy as null', async () => {
    const w = render(savedAccount())
    await flushPromises()
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.patch).toHaveBeenCalledWith('/accounts/1', expect.objectContaining({ conversion_loss_policy: null }))
  })

  it('tests a pasted proxy for a new account, then selects it', async () => {
    const w = render()
    await flushPromises()
    mocks.post.mockResolvedValueOnce({ ok: true, protocol: 'http', ip: '1.2.3.4', proxy_url: 'http://u___x:p@h:8443', proxy_id: 42, proxy_created: true, attempts: [] })
    await w.get('[data-testid="proxy-mode"] input[value="url"]').setValue(true)
    await w.get('[data-testid="proxy-url"]').setValue('h:8443:u___x:p')
    await w.get('[data-testid="proxy-url"]').trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect(mocks.post).toHaveBeenCalledWith('/proxies/probe', { proxy_url: 'h:8443:u___x:p', save: true })
    expect(w.getComponent(ProxyPicker).props('modelValue')).toBe(42)
    expect(w.get('[data-testid="proxy-probe-result"]').text()).toContain('#42')
  })

  it('keeps a pasted proxy that does not work and lists every protocol tried', async () => {
    const w = render()
    mocks.post.mockResolvedValueOnce({ ok: false, attempts: [{ protocol: 'socks5', ok: false, latency_ms: 1, message: 'refused' }, { protocol: 'http', ok: false, latency_ms: 1, status: 407 }], proxy_created: false })
    await w.get('[data-testid="proxy-mode"] input[value="url"]').setValue(true)
    await w.get('[data-testid="proxy-url"]').setValue('h:1:u:p')
    await w.get('[data-testid="proxy-url"]').trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect(w.find('[data-testid="proxy-url"]').exists()).toBe(true)
    expect(w.text()).toContain('socks5: refused; http: 407')
  })

})

// Fetching the models has no time limit (CONTRACTS §19): the user stops it,
// or closing the editor does, and a stopped fetch is no error.
describe('fetching models from the upstream', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    i18n.global.locale.value = 'zh'
    mocks.get.mockImplementation((path: string) => Promise.resolve(path === '/accounts/auto-disable' ? { enabled: true } : {}))
  })

  /** A models request that never answers: it rejects like fetch once its signal aborts. */
  function hangingFetch() {
    const signals: AbortSignal[] = []
    mocks.post.mockImplementation((_path: string, _body: unknown, opts?: { signal?: AbortSignal }) => new Promise((_resolve, reject) => {
      signals.push(opts!.signal!)
      opts!.signal!.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
    }))
    return signals
  }
  const click = (w: ReturnType<typeof render>, id: string) => w.findAllComponents({ name: 'SButton' }).find((b) => b.attributes('data-testid') === id)!.vm.$emit('click')

  it('stops the fetch on request: aborted, the button back, no error', async () => {
    const signals = hangingFetch()
    const w = render(savedAccount())
    await flushPromises()
    expect(w.find('[data-testid="models-fetch-stop"]').exists()).toBe(false)
    click(w, 'models-fetch')
    await flushPromises()
    expect(mocks.post).toHaveBeenCalledWith('/accounts/1/models/fetch', expect.anything(), { signal: signals[0] })
    click(w, 'models-fetch-stop')
    await flushPromises()
    expect(signals[0].aborted).toBe(true)
    expect(w.find('[data-testid="models-fetch-stop"]').exists()).toBe(false)
    expect(mocks.toast).not.toHaveBeenCalled()
  })

  it('stops the fetch when the editor closes', async () => {
    const signals = hangingFetch()
    const w = render(savedAccount())
    await flushPromises()
    click(w, 'models-fetch')
    await flushPromises()
    w.unmount()
    expect(signals[0].aborted).toBe(true)
  })
})

// A plugin's native account form (form.mode "native", e.g. codex's
// CodexAuthForm) comes from the plugin store: wait while the plugin list or
// the plugin's module loads, then show the component or why it is missing
// (with Retry); the rest of the editor stays usable either way.
describe('native plugin account form', () => {
  const nativeType = { ...accountType, plugin_key: 'codex', type: 'codex_device', creation_group: 'codex', form: { mode: 'native', component: 'CodexAuthForm' } } as unknown as AccountType
  const CodexAuthForm = { name: 'CodexAuthForm', props: ['mode'], template: '<div />' }
  const unavailable = () => i18n.global.t('pluginHost.nativeMissing', { component: 'CodexAuthForm' })

  beforeEach(() => {
    vi.clearAllMocks()
    i18n.global.locale.value = 'zh'
    mocks.get.mockResolvedValue({ enabled: true })
    pluginStore.component = () => null
  })

  it('waits for the plugin list and the plugin module instead of reporting the form unavailable', async () => {
    pluginStore.listed = false
    let w = render(undefined, false, nativeType)
    await flushPromises()
    expect(w.findComponent(SSpinner).exists()).toBe(true)
    expect(w.text()).not.toContain(unavailable())

    Object.assign(pluginStore, { listed: true, pending: { codex: true } })
    w = render(undefined, false, nativeType)
    await flushPromises()
    expect(w.findComponent(SSpinner).exists()).toBe(true)
    expect(w.text()).not.toContain(unavailable())
  })

  it('shows the registered component, or the load error once loading is over', async () => {
    pluginStore.component = (key, name) => (key === 'codex' && name === 'CodexAuthForm' ? CodexAuthForm : null)
    let w = render(undefined, false, nativeType)
    await flushPromises()
    expect(w.findComponent(CodexAuthForm).props('mode')).toBe('create')

    pluginStore.component = () => null
    w = render(undefined, false, nativeType)
    await flushPromises()
    expect(w.findComponent(SSpinner).exists()).toBe(false)
    expect(w.text()).toContain(unavailable())

    pluginStore.errors = { codex: 'entry does not export register(host)' }
    w = render(undefined, false, nativeType)
    await flushPromises()
    expect(w.text()).toContain('entry does not export register(host)')
  })

  it('offers Retry when the plugin module failed, and still saves the other sections with the credentials as they were', async () => {
    pluginStore.errors = { codex: 'Failed to fetch dynamically imported module' }
    mocks.patch.mockResolvedValue(savedAccount())
    const w = render({ ...savedAccount(), type: 'codex_device', credentials: { access_token: '******' } } as unknown as Account, false, nativeType)
    await flushPromises()
    await w.get('[data-testid="plugin-component-retry"]').trigger('click')
    expect(pluginStore.refresh).toHaveBeenCalledTimes(1)
    await w.getComponent(ProxyPicker).vm.$emit('update:modelValue', 7)
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.patch).toHaveBeenCalledWith('/accounts/1', expect.objectContaining({ proxy_id: 7, credentials: { access_token: '******' } }))
  })

  it('does not save while the native form says its value is invalid (expose validate, §23.5)', async () => {
    let valid = false
    pluginStore.component = () => ({ name: 'ValidatingForm', template: '<div />', setup: (_: unknown, { expose }: { expose: (e: object) => void }) => expose({ validate: async () => valid }) })
    const w = render({ ...savedAccount(), type: 'codex_device' } as unknown as Account, false, nativeType)
    await flushPromises()
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.patch).not.toHaveBeenCalled()
    valid = true
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.patch).toHaveBeenCalledWith('/accounts/1', expect.anything())
  })

  // The form's proxy choice reaches an authorization flow as a proxy ticket
  // (CONTRACTS §117): whatever the proxy field holds when the flow asks.
  it('hands the native form a proxy ticket of the current proxy choice: an existing proxy, a pasted one or none', async () => {
    const Form = { name: 'NativeForm', props: ['proxyTicket'], template: '<div />' }
    pluginStore.component = () => Form
    const w = render(undefined, false, nativeType)
    await flushPromises()
    const ticket = () => (w.getComponent(Form).props('proxyTicket') as () => Promise<string>)()
    const url = '/account-types/codex/codex_device/proxy-ticket'
    mocks.post.mockResolvedValue({ ticket: 'sealed' })

    expect(await ticket()).toBe('sealed')
    expect(mocks.post).toHaveBeenLastCalledWith(url, { proxy_id: null })
    await chooseProxy(w, 7)
    await ticket()
    expect(mocks.post).toHaveBeenLastCalledWith(url, { proxy_id: 7 })
    await w.get('[data-testid="proxy-mode"] input[value="url"]').setValue(true)
    await w.get('[data-testid="proxy-url"]').setValue(' socks5://u:p@h:1080 ')
    await ticket()
    expect(mocks.post).toHaveBeenLastCalledWith(url, { proxy_url: 'socks5://u:p@h:1080' })

    // A refused choice shows under the proxy field and goes back to the form.
    const refused = new ApiError(400, { code: 'invalid_argument', message: 'invalid', details: { fields: [{ field: 'proxy_url', code: 'invalid', message: 'invalid proxy URL: missing port' }] } })
    mocks.post.mockRejectedValueOnce(refused)
    await expect(ticket()).rejects.toBe(refused)
    await flushPromises()
    expect(w.get('[data-testid="proxy-field"]').text()).toContain('invalid proxy URL: missing port')
  })
})

describe('account model limits', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    i18n.global.locale.value = 'zh'
    mocks.get.mockImplementation((path: string) => Promise.resolve(path === '/accounts/auto-disable' ? { enabled: true } : {}))
    mocks.patch.mockResolvedValue(savedAccount())
  })

  const caps = { max_concurrency: 0, rpm_limit: 0, tpm_limit: 0, spm_limit: 0 }
  /** A card's caps: the window is always there, saved entries leave out the default. */
  const cardCaps = { ...caps, spm_window_seconds: 60 }

  it('edits the saved lists as cards and submits one entry per list', async () => {
    const limits = [{ models: ['m-a'], ...caps, max_concurrency: 1, spm_limit: 2 }, { models: ['m-b'], ...caps, max_concurrency: 1, spm_limit: 2 }]
    const w = render({ ...savedAccount(), model_limits: limits } as Account)
    await flushPromises()
    const editor = w.getComponent(ModelLimitsEditor)
    expect(editor.props('modelValue')).toEqual([{ lists: [['m-a'], ['m-b']], ...cardCaps, max_concurrency: 1, spm_limit: 2 }])
    editor.vm.$emit('update:modelValue', [{ lists: [['m-a', 'm-c'], []], ...cardCaps, rpm_limit: 9, spm_limit: 3 }])
    await flushPromises()
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.patch).toHaveBeenCalledWith('/accounts/1', expect.objectContaining({ model_limits: [{ models: ['m-a', 'm-c'], ...caps, rpm_limit: 9, spm_limit: 3 }] }))
  })

  it('edits the session limit and its period of the account and of a model list', async () => {
    const w = render({ ...savedAccount(), spm_limit: 5, spm_window_seconds: 300, model_limits: [{ models: ['m-b'], ...caps, spm_window_seconds: 7200 }] } as Account)
    await flushPromises()
    const value = (id: string) => (w.get(`[data-testid="${id}"]`).element as HTMLInputElement).value
    expect([value('account-spm_limit'), value('account-spm_limit-window'), value('account-spm_limit-window-unit')]).toEqual(['5', '5', 'minute'])
    expect(w.getComponent(ModelLimitsEditor).props('modelValue')).toEqual([{ lists: [['m-b']], ...cardCaps, spm_window_seconds: 7200 }])
    await w.get('input[data-testid="account-spm_limit"]').setValue('7')
    await w.get('input[data-testid="account-spm_limit-window"]').setValue('2')
    await w.get('[data-testid="account-spm_limit-window-unit"]').setValue('hour')
    w.getComponent(ModelLimitsEditor).vm.$emit('update:modelValue', [{ lists: [['m-a']], ...cardCaps, spm_limit: 2 }, { lists: [['m-c']], ...cardCaps, spm_window_seconds: 600 }])
    await flushPromises()
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.patch).toHaveBeenCalledWith('/accounts/1', expect.objectContaining({
      spm_limit: 7,
      spm_window_seconds: 7200,
      model_limits: [{ models: ['m-a'], ...caps, spm_limit: 2 }, { models: ['m-c'], ...caps, spm_window_seconds: 600 }]
    }))
  })

  it('sends the default period of an account saved without one, and shows the server\'s period error on the session row', async () => {
    mocks.patch.mockRejectedValueOnce(new ApiError(400, { code: 'invalid_argument', message: 'invalid', details: { fields: [{ field: 'spm_window_seconds', code: 'invalid', message: 'spm_window_seconds: invalid' }] } }))
    const w = render(savedAccount())
    await flushPromises()
    expect((w.get('input[data-testid="account-spm_limit-window"]').element as HTMLInputElement).value).toBe('1')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.patch).toHaveBeenCalledWith('/accounts/1', expect.objectContaining({ spm_window_seconds: 60, model_limits: [] }))
    expect(w.getComponent(RateLimitEditor).props('errors')).toEqual({ spm_window_seconds: 'spm_window_seconds: invalid' })
    expect(w.getComponent(AccountEditorNav).props('errors')).toMatchObject({ scheduling: true })
  })

  it('leaves a period the type fixes to the server, and hides one the type hides', async () => {
    const fixedType = { ...accountType, common_controls: { spm_window_seconds: { configurable: false } } } as unknown as AccountType
    const w = render({ ...savedAccount(), spm_window_seconds: 600 } as Account, false, fixedType)
    await flushPromises()
    expect(w.get('input[data-testid="account-spm_limit-window"]').attributes('disabled')).toBeDefined()
    expect(w.get('input[data-testid="account-spm_limit"]').attributes('disabled')).toBeUndefined()
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.patch.mock.calls[0][1]).not.toHaveProperty('spm_window_seconds')
    const hidden = render(savedAccount(), false, { ...accountType, common_controls: { spm_window_seconds: { visible: false } } } as unknown as AccountType)
    await flushPromises()
    expect(hidden.find('[data-testid="account-spm_limit-window"]').exists()).toBe(false)
    expect(hidden.find('input[data-testid="account-spm_limit"]').exists()).toBe(true)
    // A fixed cap leaves its period editable.
    const capFixed = render(savedAccount(), false, { ...accountType, common_controls: { spm_limit: { configurable: false } } } as unknown as AccountType)
    await flushPromises()
    expect(capFixed.get('input[data-testid="account-spm_limit"]').attributes('disabled')).toBeDefined()
    expect(capFixed.get('input[data-testid="account-spm_limit-window"]').attributes('disabled')).toBeUndefined()
  })

  it('shows the concurrency cap in the limits group, ahead of the rate limits', async () => {
    const w = render(savedAccount())
    await flushPromises()
    expect(w.find('[data-testid="account-scheduling"] [data-testid="account-max_concurrency"]').exists()).toBe(false)
    const limits = w.get('[data-testid="account-limits"]').findAll('input').map(i => i.attributes('data-testid'))
    expect(limits.slice(0, 2)).toEqual(['account-max_concurrency', 'account-rpm_limit'])
  })

  it('hides the limits group of a type hiding every limit, its undeclared period included', async () => {
    const hidden = { visible: false }
    const w = render(savedAccount(), false, { ...accountType, common_controls: { max_concurrency: hidden, rpm_limit: hidden, tpm_limit: hidden, spm_limit: hidden } } as unknown as AccountType)
    await flushPromises()
    expect(w.find('[data-testid="account-limits"]').exists()).toBe(false)
  })

  it('refuses to save a model listed twice', async () => {
    const w = render(savedAccount())
    await flushPromises()
    w.getComponent(ModelLimitsEditor).vm.$emit('update:modelValue', [{ lists: [['m-a'], ['m-a']], ...cardCaps, rpm_limit: 1 }])
    await flushPromises()
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.patch).not.toHaveBeenCalled()
    expect(w.getComponent(ModelLimitsEditor).props('errors')).toEqual({ model_limits: 'm-a 已在另一个列表中' })
  })

  it.each([['all', '0'], ['own', '10']] as const)('starts a new account of an %s-level caller with max concurrency %s', async (scope, value) => {
    ownership.scope = scope
    const w = render(undefined)
    await flushPromises()
    expect((w.get('input[data-testid="account-max_concurrency"]').element as HTMLInputElement).value).toBe(value)
    ownership.scope = 'all'
  })
  it('fits the plugin\'s suggested limits into an own-level caller\'s caps', async () => {
    ownership.scope = 'own'
    const w = render(undefined, false, { ...accountType, default_limits: { priority: 500, weight: 300, max_concurrency: 0 } } as AccountType)
    await flushPromises()
    expect(['priority', 'weight', 'max_concurrency'].map(k => (w.get(`input[data-testid="account-${k}"]`).element as HTMLInputElement).value)).toEqual(['100', '100', '10'])
    ownership.scope = 'all'
  })
  it('starts a new account from the limits its plugin suggests', async () => {
    const type = { ...accountType, default_limits: { priority: 3, spm_limit: 6, model_limits: [{ models: ['m-a'], ...caps, max_concurrency: 2 }] } } as AccountType
    const w = render(undefined, false, type)
    await flushPromises()
    expect(w.getComponent(ModelLimitsEditor).props('modelValue')).toEqual([{ lists: [['m-a']], ...cardCaps, max_concurrency: 2 }])
    expect((w.get('input[data-testid="account-spm_limit-window"]').element as HTMLInputElement).value).toBe('1')
    expect((w.get('input[data-testid="account-spm_limit"]').element as HTMLInputElement).value).toBe('6')
  })
})

describe('account usage limit', () => {
  const saved = { raw_cost_accounting: false, enabled: true, amount: '200', period: 'week', period_count: 1, anchor: null, timezone: 'UTC', configured: true }
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone
  let limitGet: () => Promise<unknown>

  beforeEach(() => {
    vi.clearAllMocks()
    i18n.global.locale.value = 'zh'
    limitGet = () => Promise.resolve(saved)
    mocks.get.mockImplementation((path: string) =>
      path.endsWith('/usage-limit') ? limitGet() : Promise.resolve(path === '/accounts/auto-disable' ? { enabled: true } : {})
    )
    mocks.patch.mockResolvedValue(savedAccount())
    mocks.put.mockResolvedValue({})
  })

  const card = (w: ReturnType<typeof render>) => w.getComponent(AccountUsageLimitCard)
  async function open() {
    const w = render(savedAccount())
    await flushPromises()
    w.getComponent(AccountEditorNav).vm.$emit('update:modelValue', 'scheduling')
    await flushPromises()
    return w
  }
  async function submit(w: ReturnType<typeof render>) {
    await w.get('form').trigger('submit')
    await flushPromises()
  }

  it('loads the limit when the section is shown and saves it after the account, in the browser time zone', async () => {
    const w = render(savedAccount())
    await flushPromises()
    expect(mocks.get).not.toHaveBeenCalledWith('/accounts/1/usage-limit')
    w.getComponent(AccountEditorNav).vm.$emit('update:modelValue', 'scheduling')
    await flushPromises()
    expect(card(w).props()).toMatchObject({ status: 'ready', modelValue: saved })
    expect(w.getComponent(AccountEditorNav).props('sections')[2].summary).toContain('用量限制 $200.00 每周')
    await submit(w)
    expect(mocks.patch).toHaveBeenCalledOnce()
    expect(mocks.put).not.toHaveBeenCalled()

    card(w).vm.$emit('update:modelValue', { ...saved, amount: '300', period_count: 2 })
    await flushPromises()
    expect(card(w).props('modelValue').timezone).toBe(zone)
    expect(w.getComponent(AccountEditorNav).props('sections')[2].summary).toContain('用量限制 $300.00 每 2 周')
    await submit(w)
    expect(mocks.put).toHaveBeenCalledWith('/accounts/1/usage-limit', { ...saved, amount: '300', period_count: 2, timezone: zone })
    expect(mocks.patch.mock.invocationCallOrder[1]).toBeLessThan(mocks.put.mock.invocationCallOrder[0])
    expect(w.emitted('saved')).toHaveLength(2)
  })

  it('keeps the saved time zone unless the periods are edited', async () => {
    const chatham = { ...saved, timezone: 'Pacific/Chatham' }
    limitGet = () => Promise.resolve(chatham)
    const w = await open()
    card(w).vm.$emit('update:modelValue', { ...chatham, raw_cost_accounting: true })
    await flushPromises()
    await submit(w)
    expect(mocks.put).toHaveBeenCalledWith('/accounts/1/usage-limit', { ...chatham, raw_cost_accounting: true })

    card(w).vm.$emit('update:modelValue', { ...chatham, raw_cost_accounting: true, period: 'month' })
    card(w).vm.$emit('update:modelValue', { ...chatham, raw_cost_accounting: true })
    await flushPromises()
    expect(card(w).props('modelValue').timezone).toBe(zone)
    await submit(w)
    expect(mocks.put).toHaveBeenCalledOnce()
  })

  it('keeps the card read-only and never saves a limit that did not load', async () => {
    limitGet = () => Promise.reject(new Error('network down'))
    const w = await open()
    expect(card(w).props()).toMatchObject({ status: 'failed', loadError: 'network down' })
    card(w).vm.$emit('update:modelValue', { ...saved, amount: '1' })
    await submit(w)
    expect(mocks.patch).toHaveBeenCalledOnce()
    expect(mocks.put).not.toHaveBeenCalled()

    limitGet = () => Promise.resolve(saved)
    card(w).vm.$emit('retry')
    await flushPromises()
    expect(card(w).props()).toMatchObject({ status: 'ready', modelValue: saved })
  })

  it('holds the card while the limit loads', async () => {
    let resolve!: (v: unknown) => void
    limitGet = () => new Promise((r) => (resolve = r))
    const w = await open()
    expect(card(w).props('status')).toBe('loading')
    await submit(w)
    expect(mocks.put).not.toHaveBeenCalled()
    resolve(saved)
    await flushPromises()
    expect(card(w).props()).toMatchObject({ status: 'ready', modelValue: saved })
  })

  it('reports a refused limit without failing the saved account', async () => {
    mocks.put.mockRejectedValue(new Error('period_count must be 1-12 for month'))
    const w = await open()
    card(w).vm.$emit('update:modelValue', { ...saved, amount: '300' })
    await submit(w)
    expect(mocks.toast).toHaveBeenCalledWith('账号已保存，但用量限制保存失败：period_count must be 1-12 for month', 'error')
    expect(w.emitted('saved')).toHaveLength(1)
  })

  it('checks the period length against its period before saving anything', async () => {
    const w = await open()
    card(w).vm.$emit('update:modelValue', { ...saved, period: 'month', period_count: 13 })
    await submit(w)
    expect(mocks.patch).not.toHaveBeenCalled()
    expect(card(w).props('errors')).toEqual({ period_count: '周期长度必须为 1-12' })
    expect(w.getComponent(AccountEditorNav).props('errors').scheduling).toBe(true)
  })

  it('starts a limit the account never saved in the browser time zone', async () => {
    const never = { raw_cost_accounting: false, enabled: false, amount: '0', period: 'month', period_count: 1, anchor: null, timezone: '', configured: false }
    limitGet = () => Promise.resolve(never)
    const w = await open()
    expect(card(w).props('modelValue').timezone).toBe(zone)
    card(w).vm.$emit('update:modelValue', { ...card(w).props('modelValue'), enabled: true, amount: '50' })
    await submit(w)
    expect(mocks.put).toHaveBeenCalledWith('/accounts/1/usage-limit', { ...never, enabled: true, amount: '50', timezone: zone })
  })

  it('saves a switched-off limit whose amount was cleared', async () => {
    const w = await open()
    card(w).vm.$emit('update:modelValue', { ...saved, enabled: false, amount: '' })
    await submit(w)
    expect(card(w).props('errors')).toEqual({})
    expect(mocks.put).toHaveBeenCalledWith('/accounts/1/usage-limit', { ...saved, enabled: false, amount: '' })
  })

  it('saves a lifetime limit whatever the hidden period length was', async () => {
    limitGet = () => Promise.resolve({ ...saved, period: 'month', period_count: 12 })
    const w = await open()
    card(w).vm.$emit('update:modelValue', { ...saved, period: 'none', period_count: 12 })
    await submit(w)
    expect(card(w).props('errors')).toEqual({})
    expect(mocks.put).toHaveBeenCalledWith('/accounts/1/usage-limit', { ...saved, period: 'none', period_count: 12, timezone: zone })
  })

  it('saves an amount with more than 2 decimals (the card input takes step="any")', async () => {
    const w = await open()
    card(w).vm.$emit('update:modelValue', { ...saved, amount: '0.12345678' })
    await submit(w)
    expect(mocks.patch).toHaveBeenCalledOnce()
    expect(mocks.put).toHaveBeenCalledWith('/accounts/1/usage-limit', { ...saved, amount: '0.12345678' })
  })
})

describe('common controls of the account type (docs/COMMON-CONTROLS.md §2)', () => {
  const declared = {
    ...accountType, plugin_key: 'demo', type: 'controls', creation_group: undefined,
    default_limits: { weight: 4, tpm_limit: 8 },
    common_controls: {
      priority: { visible: false },
      weight: { locked: true, default: 5 },
      rpm_limit: { configurable: false, default: 7 },
      header_passthrough: { visible: false }, header_override: { visible: false }, param_override: { visible: false }, environment: { visible: false },
      model_mapping: { default: { a: 'b' } },
      proxy: { required: true }
    }
  } as unknown as AccountType
  const input = (w: ReturnType<typeof render>, key: string) => w.get(`input[data-testid="account-${key}"]`)
  const sections = (w: ReturnType<typeof render>) => w.getComponent(AccountEditorNav).props('sections')

  beforeEach(() => {
    vi.clearAllMocks()
    i18n.global.locale.value = 'zh'
    mocks.get.mockImplementation((path: string) => Promise.resolve(path === '/accounts/auto-disable' ? { enabled: true } : {}))
    mocks.post.mockResolvedValue(savedAccount())
    mocks.patch.mockResolvedValue(savedAccount())
  })

  it('hides, fixes and prefills a new account\'s controls as declared', async () => {
    const w = render(undefined, false, declared)
    await flushPromises()
    expect(w.find('[data-testid="account-priority"]').exists()).toBe(false)
    expect((input(w, 'weight').element as HTMLInputElement).value).toBe('5')
    expect(input(w, 'weight').attributes('disabled')).toBeDefined()
    expect((input(w, 'rpm_limit').element as HTMLInputElement).value).toBe('7')
    expect(input(w, 'rpm_limit').attributes('disabled')).toBeDefined()
    // default_limits still applies where nothing is declared.
    expect((input(w, 'tpm_limit').element as HTMLInputElement).value).toBe('8')
    expect(input(w, 'tpm_limit').attributes('disabled')).toBeUndefined()
    expect(w.get('[data-testid="account-scheduling"]').html()).toContain('插件将此项固定为预设值')
    expect(w.find('[data-testid="account-header-passthrough"]').exists()).toBe(false)
    expect(w.findComponent(HeaderOverrideField).exists()).toBe(false)
    // The advanced section keeps the conversion loss policy, which is no common control.
    expect(sections(w).map(s => s.key)).toEqual(['connection', 'models', 'scheduling', 'advanced'])
    expect(w.find('[data-testid="account-conversion-policy"]').exists()).toBe(true)
    expect(sections(w).find(s => s.key === 'scheduling')!.summary).toBe('权重 5 · 并发 ∞')
    expect(w.getComponent(ModelMappingEditor).props('modelValue')).toEqual({ a: 'b' })
    expect(w.get('[data-testid="proxy-field"]').text()).toContain('*')
  })

  it('requires the declared proxy and leaves the fixed fields to the server', async () => {
    const w = render(undefined, false, declared)
    await flushPromises()
    await w.get('[data-editor-section="connection"] input').setValue('demo')
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.post).not.toHaveBeenCalledWith('/accounts', expect.anything())
    expect(w.get('[data-testid="proxy-field"]').text()).toContain('此账号类型必须配置代理')
    await chooseProxy(w, 3)
    await w.get('form').trigger('submit'); await flushPromises()
    const body = mocks.post.mock.calls.find(c => c[0] === '/accounts')![1]
    expect(body).toMatchObject({ proxy_id: 3, priority: 10, tpm_limit: 8, model_mapping: { a: 'b' } })
    expect(body).not.toHaveProperty('weight')
    expect(body).not.toHaveProperty('rpm_limit')
  })

  it('switching the auth method applies the new type\'s fixed controls and untouched defaults, keeping edits', async () => {
    const group = { ...accountType, plugin_key: 'demo', creation_group: 'demo' }
    const w = render(undefined, false, { ...group, type: 'a' } as unknown as AccountType)
    await flushPromises()
    await input(w, 'priority').setValue('42')
    w.getComponent(ModelLimitsEditor).vm.$emit('update:modelValue', [{ lists: [['m-1']], max_concurrency: 0, rpm_limit: 3, tpm_limit: 0, spm_limit: 0, spm_window_seconds: 60 }])
    await w.setProps({ accountType: { ...group, type: 'b', default_limits: { weight: 6, priority: 1 }, common_controls: { model_limits: { configurable: false } } } as unknown as AccountType })
    await flushPromises()
    expect((input(w, 'priority').element as HTMLInputElement).value).toBe('42')
    expect((input(w, 'weight').element as HTMLInputElement).value).toBe('6')
    expect(w.getComponent(ModelLimitsEditor).props('modelValue')).toEqual([])
  })

  it('shows a saved account\'s stored values, saying saving moves a locked one to its value', async () => {
    const w = render({ ...savedAccount(), weight: 9, rpm_limit: 3 } as Account, false, declared)
    await flushPromises()
    expect((input(w, 'weight').element as HTMLInputElement).value).toBe('9')
    expect(w.get('[data-testid="account-scheduling"]').html()).toContain('保存后改为锁定值 5')
    expect((input(w, 'rpm_limit').element as HTMLInputElement).value).toBe('3')
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.patch.mock.calls[0][1]).not.toHaveProperty('weight')
    expect(mocks.patch.mock.calls[0][1]).not.toHaveProperty('rpm_limit')
  })
})

describe('plugin sections (account.editor.section)', () => {
  const entries = [
    { pluginKey: 'demo', name: 'DemoSection', component: {}, label: { zh: '演示分区', en: 'Demo' }, icon: 'chart', accountTypes: ['controls'] },
    { pluginKey: 'other', name: 'OtherSection', component: {}, label: '其他', accountTypes: ['controls'] },
    { pluginKey: 'any', name: 'AnySection', component: {} }
  ]
  const type = { ...accountType, plugin_key: 'demo', type: 'controls', creation_group: undefined } as unknown as AccountType
  const plugin = (w: ReturnType<typeof render>) => w.getComponent(AccountEditorNav).props('sections').filter(s => s.key.startsWith('plugin:'))

  beforeEach(() => {
    vi.clearAllMocks()
    i18n.global.locale.value = 'zh'
    mocks.get.mockImplementation((path: string) => Promise.resolve(path === '/accounts/auto-disable' ? { enabled: true } : {}))
    pluginStore.slotEntries = (slot: string) => (slot === 'account.editor.section' ? entries : [])
  })
  afterEach(() => {
    pluginStore.slotEntries = () => []
  })

  it('appends the sections declared for the type, configurable once the account is saved', async () => {
    const w = render(undefined, false, type)
    await flushPromises()
    expect(plugin(w)).toEqual([
      { key: 'plugin:demo:DemoSection', icon: 'chart', label: '演示分区', summary: '保存账号后可配置' },
      { key: 'plugin:any:AnySection', icon: 'puzzle', label: 'AnySection', summary: '保存账号后可配置' }
    ])
    expect(w.findAll('[data-testid="plugin-section-after-save"]')).toHaveLength(2)
  })

  it('renders each section with the account, its type and whether it is read-only', async () => {
    const account = savedAccount()
    const w = render(account, false, type)
    await flushPromises()
    const slots = w.findAllComponents(PluginSlot).filter(s => s.props('name') === 'account.editor.section')
    expect(slots).toHaveLength(2)
    expect(slots[0].props('props')).toEqual({ account, accountType: type, readonly: false })
    const only = slots[0].props('only') as (e: unknown) => boolean
    expect(entries.map(only)).toEqual([true, false, false])
    expect(slots[0].element.closest('[data-editor-section]')?.getAttribute('data-editor-section')).toBe('plugin:demo:DemoSection')
  })
})

// T75: what an own-level caller may set beyond editing (useAccountCaps).
describe('account field permissions', () => {
  const relayType = { ...accountType, plugin_key: 'relay', type: 'relay', creation_group: undefined, custom_upstream: true, guarded_settings: [] } as unknown as AccountType
  beforeEach(() => {
    vi.clearAllMocks()
    i18n.global.locale.value = 'zh'
    mocks.get.mockImplementation((path: string) => Promise.resolve(path === '/accounts/auto-disable' ? { enabled: true } : {}))
    mocks.patch.mockResolvedValue(savedAccount())
    mocks.post.mockResolvedValue(savedAccount())
  })
  afterEach(() => {
    ownership.scope = 'all'
    perms.denied = []
  })

  it('locks the groups and leaves them out of the save without a bind permission', async () => {
    ownership.scope = 'own'
    perms.denied = ['group:manage', 'account:group:bind']
    const w = render({ ...savedAccount(), group_ids: [3] } as Account)
    await flushPromises()
    expect(w.findComponent({ name: 'GroupPicker' }).props('disabled')).toBe(true)
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.patch.mock.calls[0][1]).not.toHaveProperty('group_ids')
  })

  it('sends group_ids only when they changed', async () => {
    const w = render({ ...savedAccount(), group_ids: [3, 4] } as Account)
    await flushPromises()
    expect(w.findComponent({ name: 'GroupPicker' }).props('disabled')).toBe(false)
    w.findComponent({ name: 'GroupPicker' }).vm.$emit('update:modelValue', [4, 3])
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.patch.mock.calls[0][1]).not.toHaveProperty('group_ids')
    w.findComponent({ name: 'GroupPicker' }).vm.$emit('update:modelValue', [4])
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.patch.mock.calls[1][1]).toMatchObject({ group_ids: [4] })
  })

  it('offers pasting a proxy URL only with a proxy management key', async () => {
    perms.denied = ['proxy:manage', 'proxy:manage:own']
    const w = render(savedAccount())
    await flushPromises()
    expect(w.find('[data-testid="proxy-mode"]').exists()).toBe(false)
    expect(w.find('[data-testid="proxy-url"]').exists()).toBe(false)
  })

  it('greys out a custom-upstream auth method for an own-level caller without account:relay', async () => {
    ownership.scope = 'own'
    perms.denied = ['account:relay']
    const official = { ...accountType, plugin_key: 'relay', type: 'official', creation_group: 'relay', custom_upstream: false } as unknown as AccountType
    const custom = { ...official, type: 'custom', custom_upstream: true } as unknown as AccountType
    const w = shallowMount(AccountEditor, { props: { accountType: official, accountTypeOptions: [official, custom] }, global: { plugins: [i18n], stubs: { SCard: { template: '<div><slot /></div>' }, SField: { template: '<div><slot /></div>' } } } })
    await flushPromises()
    const options = w.findComponent({ name: 'SChoiceCards' }).props('options') as { value: string; disabled: boolean }[]
    expect(options).toEqual([expect.objectContaining({ value: 'official', disabled: false }), expect.objectContaining({ value: 'custom', disabled: true })])
  })

  it('says guarded settings keep the official values without account:settings:custom', async () => {
    perms.denied = ['account:settings:custom']
    const guarded = { ...relayType, guarded_settings: [{ field: 'base_url', allowed: ['https://api.example.com'] }] } as unknown as AccountType
    const w = render(savedAccount(), false, guarded)
    await flushPromises()
    expect(w.find('[data-testid="guarded-settings-hint"]').exists()).toBe(true)
    perms.denied = []
    expect(render(savedAccount(), false, guarded).find('[data-testid="guarded-settings-hint"]').exists()).toBe(false)
  })
})

describe('account protocols (DESIGN-2026-11-17-PROTOCOL-BINDING §5.1)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    i18n.global.locale.value = 'zh'
    mocks.get.mockImplementation((path: string) => Promise.resolve(path === '/accounts/auto-disable' ? { enabled: true } : {}))
    mocks.patch.mockResolvedValue(savedAccount())
    mocks.post.mockResolvedValue(savedAccount())
  })
  const openaiType = { ...accountType, ...openai } as AccountType
  const bound = () => ({ ...savedAccount(), models: ['gpt-5.6', 'claude-sonnet-4-5'], protocols: ['openai.chat', 'openai.responses'], model_protocols: [{ models: ['claude-sonnet-4-5'], protocols: ['anthropic.messages'] }] }) as Account
  const nav = (w: ReturnType<typeof render>) => w.getComponent(AccountEditorNav)
  const section = (w: ReturnType<typeof render>) => nav(w).props('sections').find(s => s.key === 'models')!
  const editorOf = (w: ReturnType<typeof render>) => w.getComponent(OwnProtocolsField).props('editor')
  const policy = (w: ReturnType<typeof render>) => w.get('[data-testid="account-conversion-policy-select"]')

  it('names the section "protocols and models" and orders its cards: own protocols, models and mapping, protocols per model', async () => {
    const w = render(bound(), false, openaiType)
    await flushPromises()
    expect(section(w).label).toBe('协议与模型')
    const cards = [...w.get('[data-editor-section="models"]').element.children].map(c => c.getAttribute('data-testid') || c.tagName.toLowerCase())
    expect(cards).toEqual(['own-protocols-card', 'div', 'model-protocols-card-stub'])
    expect(w.get('[data-testid="own-protocols-card"]').findComponent(OwnProtocolsField).exists()).toBe(true)
    // Both cards work on one state; the matrix gets the model list and the mapping.
    const card = w.getComponent(ModelProtocolsCard)
    expect(card.props('editor')).toBe(editorOf(w))
    expect([card.props('models'), card.props('mapping')]).toEqual([['gpt-5.6', 'claude-sonnet-4-5'], {}])
  })

  it('loads the binding of the account, sums it up in the navigation and saves both fields', async () => {
    const w = render(bound(), false, openaiType)
    await flushPromises()
    expect(section(w).summary).toBe('协议 2/4 · 限定 1 个模型 · 转换 1 个 · 2 个模型')
    const editor = editorOf(w)
    editor.setMode('all')
    editor.rules.value = { ...editor.rules.value, 'gpt-5.6': ['openai.chat'] }
    await flushPromises()
    expect(section(w).summary).toBe('协议 4/4 · 限定 2 个模型 · 转换 1 个 · 2 个模型')
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.patch).toHaveBeenCalledWith('/accounts/1', expect.objectContaining({
      protocols: null,
      model_protocols: [{ models: ['claude-sonnet-4-5'], protocols: ['anthropic.messages'] }, { models: ['gpt-5.6'], protocols: ['openai.chat'] }]
    }))
  })

  it('creates an account on every own protocol and without conversion unless told otherwise', async () => {
    const w = render(undefined, false, openaiType)
    await flushPromises()
    expect(section(w).summary).toBe('协议 4/4 · 不转换 · 全部模型')
    await w.get('input').setValue('new')
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.post).toHaveBeenCalledWith('/accounts', expect.objectContaining({ protocols: null, model_protocols: [] }))
  })

  it('leaves the protocols of an account whose type is gone out of the request', async () => {
    const w = shallowMount(AccountEditor, { props: { accountType: null, account: { ...bound(), orphaned: true } as Account }, global: { plugins: [i18n] } })
    await flushPromises()
    expect(w.findComponent(OwnProtocolsField).exists()).toBe(false)
    expect(w.findComponent(ModelProtocolsCard).exists()).toBe(false)
    await w.get('form').trigger('submit'); await flushPromises()
    const body = mocks.patch.mock.calls[0][1]
    expect(body).not.toHaveProperty('protocols')
    expect(body).not.toHaveProperty('model_protocols')
  })

  it('does not save while a model has no protocol or the account opens none, and shows the section', async () => {
    const w = render({ ...bound(), model_protocols: [{ models: ['gpt-5.6'], protocols: [] }] } as Account, false, openaiType)
    await flushPromises()
    expect(nav(w).props('errors')).toMatchObject({ models: true })
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.patch).not.toHaveBeenCalled()
    expect(nav(w).props('modelValue')).toBe('models')
    const editor = editorOf(w)
    editor.rules.value = {}
    await flushPromises()
    expect(nav(w).props('errors')).toMatchObject({ models: false })
    // Every own protocol closed and nothing converted.
    editor.setMode('custom')
    for (const c of editor.columns.value.own) if (editor.protocols.value!.includes(c.key)) editor.toggleOwn(c)
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.patch).not.toHaveBeenCalled()
    editor.rules.value = { 'gpt-5.6': ['anthropic.messages'] }
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.patch).toHaveBeenCalledWith('/accounts/1', expect.objectContaining({ protocols: [], model_protocols: [{ models: ['gpt-5.6'], protocols: ['anthropic.messages'] }] }))
  })

  it('does not save while the JSON view holds a text it cannot take', async () => {
    const w = render(bound(), false, openaiType)
    await flushPromises()
    const editor = editorOf(w)
    editor.setView('json')
    editor.text.value = '{"model_protocols": 1}'
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.patch).not.toHaveBeenCalled()
    expect(nav(w).props('modelValue')).toBe('models')
    editor.text.value = '{"protocols": ["openai.chat"]}'
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.patch).toHaveBeenCalledWith('/accounts/1', expect.objectContaining({ protocols: ['openai.chat'], model_protocols: [] }))
  })

  it('shows the server errors of the two fields in their cards and marks the section', async () => {
    mocks.patch.mockRejectedValueOnce(new ApiError(400, { code: 'invalid_argument', message: 'invalid', details: { fields: [
      { field: 'protocols[1]', code: 'invalid', message: '不是本平台协议' },
      { field: 'model_protocols[0].protocols[0]', code: 'invalid', message: '不能服务此协议' }
    ] } }))
    const w = render(bound(), false, openaiType)
    await flushPromises()
    await w.get('form').trigger('submit'); await flushPromises()
    expect(nav(w).props('modelValue')).toBe('models')
    expect(nav(w).props('errors')).toMatchObject({ models: true })
    expect(w.getComponent(OwnProtocolsField).props('errors')).toEqual(['openai.responses: 不是本平台协议'])
    expect(w.getComponent(ModelProtocolsCard).props('errors')).toMatchObject({ 'model_protocols[0].protocols[0]': '不能服务此协议' })
  })

  it('greys the conversion loss policy while no model is converted and no protocol adapted', async () => {
    const hint = (w: ReturnType<typeof render>) => w.get('[data-testid="account-conversion-policy-hint"]').text()
    const plain = render({ ...savedAccount(), conversion_loss_policy: 'safe' } as Account, false, openaiType)
    await flushPromises()
    expect(policy(plain).attributes('disabled')).toBeDefined()
    expect(hint(plain)).toBe('未开启协议转换：没有模型开了转换，也没有开放需要适配的协议')
    // The saved policy is kept and sent as it is.
    await plain.get('form').trigger('submit'); await flushPromises()
    expect(mocks.patch).toHaveBeenCalledWith('/accounts/1', expect.objectContaining({ conversion_loss_policy: 'safe' }))
    // A model with a conversion turns it on.
    editorOf(plain).rules.value = { 'gpt-5.6': ['openai.chat', 'anthropic.messages'] }
    await flushPromises()
    expect(policy(plain).attributes('disabled')).toBeUndefined()
    expect(hint(plain)).toBe('生效：安全（覆盖账号分组设置）')
    // So does an adapted own protocol (Codex chat), until the account closes it.
    const adapted = render(savedAccount(), false, { ...accountType, ...codex } as AccountType)
    await flushPromises()
    expect(policy(adapted).attributes('disabled')).toBeUndefined()
    const editor = editorOf(adapted)
    editor.setMode('custom')
    editor.toggleOwn(editor.columns.value.own.find(c => c.via === 'adapted')!)
    await flushPromises()
    expect(policy(adapted).attributes('disabled')).toBeDefined()
  })

  it('keeps what was ticked when the auth method changes to a type with the same protocols, and starts over otherwise', async () => {
    const device = { ...openaiType, type: 'device', creation_group: 'g' } as AccountType
    const oauth = { ...openaiType, type: 'oauth', creation_group: 'g' } as AccountType
    const other = { ...accountType, ...codex, type: 'codex', creation_group: 'g' } as AccountType
    const w = render(undefined, false, device)
    await flushPromises()
    const editor = editorOf(w)
    editor.setMode('custom')
    editor.toggleOwn(editor.columns.value.own[0])
    await w.setProps({ accountType: oauth })
    expect(editor.binding().protocols).toEqual(['openai.responses', 'openai.responses_ws', 'openai.embeddings'])
    await w.setProps({ accountType: other })
    expect(editor.binding()).toEqual({ protocols: null, model_protocols: [] })
  })
})
