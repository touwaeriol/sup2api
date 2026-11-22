import { beforeEach, describe, expect, it, vi } from 'vitest'
import { KeyForm, accountType, chooseProxy, marked, nav, render, savedAccount, sectionOf, sections } from './testing/accountEditor'
import { mocks, pluginStore } from './testing/editorState'
import { flushPromises } from '@vue/test-utils'
import { ApiError } from '@sub2api/host'
import { i18n } from '@/i18n'
import type { Account, AccountType, Group } from '@/api/types'
import { useGroupsLookup } from '@/composables/lookups'
import ProxyPicker from '@/components/ProxyPicker.vue'
import ParamOverrideField from './paramOverride/ParamOverrideField.vue'
import HeaderOverrideField from './headerOverride/HeaderOverrideField.vue'
import { SSpinner, STagInput } from '@sub2api/ui'
import { codex } from './protocols/fixtures'

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
    expect(marked(w)).toContain('connection')
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
    expect(sectionOf(w.get('[data-testid="account-header-passthrough"]').element)).toBe('advanced')
    const advanced = () => sections(w).find(s => s.key === 'advanced')
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
    expect(sectionOf(field.element)).toBe('advanced')
    const advanced = () => sections(w).find(s => s.key === 'advanced')
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
    expect(sectionOf(field.element)).toBe('advanced')
    expect(w.getComponent(ParamOverrideField).props('headerOverride')).toEqual(headers)
    const advanced = () => sections(w).find(s => s.key === 'advanced')
    expect(advanced()?.summary).toBe('不透传 · 请求头覆盖 2 项 · 超时跟随')
    mocks.patch.mockRejectedValueOnce(new ApiError(400, { code: 'invalid_argument', message: 'invalid', details: { fields: [{ field: 'header_override', code: 'blocked', message: 'Authorization: blocked' }] } }))
    field.vm.$emit('update:modelValue', { Authorization: 'x' })
    await flushPromises()
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.patch).toHaveBeenCalledWith('/accounts/1', expect.objectContaining({ header_override: { Authorization: 'x' } }))
    expect(field.props('error')).toBe('Authorization: blocked')
    expect(marked(w)).toContain('advanced')
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
    expect(sectionOf(w.get('[data-testid="account-conversion-policy"]').element)).toBe('advanced')
    await w.get('[data-testid="account-conversion-policy-select"]').setValue('strict')
    expect(hint()).toBe('生效：严格（覆盖账号分组设置）')
    expect(sections(w).find(s => s.key === 'advanced')?.summary).toBe('不透传 · 转换：严格 · 超时跟随')
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.patch).toHaveBeenCalledWith('/accounts/1', expect.objectContaining({ conversion_loss_policy: 'strict' }))
    groups.value = []
  })

  it('opens the section of a field the browser finds invalid, and sends nothing', async () => {
    const w = render(savedAccount())
    await flushPromises()
    await w.get('input[data-testid="account-priority"]').setValue('-5')
    await w.get('form').trigger('submit'); await flushPromises()
    expect(nav(w).props('modelValue')).toBe('scheduling')
    expect(mocks.patch).not.toHaveBeenCalled()
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
