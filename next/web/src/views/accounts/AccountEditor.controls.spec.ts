import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { accountType, chooseProxy, marked, nav, render, savedAccount, sectionOf, sections } from './testing/accountEditor'
import { mocks, ownership, perms, pluginStore } from './testing/editorState'
import { flushPromises, shallowMount } from '@vue/test-utils'
import { ApiError } from '@sub2api/host'
import { i18n } from '@/i18n'
import type { Account, AccountType } from '@/api/types'
import AccountEditor from './AccountEditor.vue'
import ModelLimitsEditor from './ModelLimitsEditor.vue'
import ModelMappingEditor from './ModelMappingEditor.vue'
import PluginSlot from '@/components/plugin/PluginSlot.vue'
import HeaderOverrideField from './headerOverride/HeaderOverrideField.vue'
import ModelProtocolsCard from './protocols/ModelProtocolsCard.vue'
import OwnProtocolsField from './protocols/OwnProtocolsField.vue'
import { codex, openai } from './protocols/fixtures'

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
    await w.get('[data-tab="connection"] input').setValue('demo')
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
  const plugin = (w: ReturnType<typeof render>) => sections(w).filter(s => s.key.startsWith('plugin:'))

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
    expect(sectionOf(slots[0].element)).toBe('plugin:demo:DemoSection')
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
    const w = shallowMount(AccountEditor, { props: { accountType: official, accountTypeOptions: [official, custom] }, global: { plugins: [i18n], stubs: { STabbedForm: false, STitleScope: false, AccountCredentialsCard: false, SCard: { template: '<div><slot /></div>' }, SField: { template: '<div><slot /></div>' } } } })
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
  const section = (w: ReturnType<typeof render>) => sections(w).find(s => s.key === 'models')!
  const editorOf = (w: ReturnType<typeof render>) => w.getComponent(OwnProtocolsField).props('editor')
  const policy = (w: ReturnType<typeof render>) => w.get('[data-testid="account-conversion-policy-select"]')

  it('names the section "protocols and models" and orders its cards: own protocols, models and mapping, protocols per model', async () => {
    const w = render(bound(), false, openaiType)
    await flushPromises()
    expect(section(w).label).toBe('协议与模型')
    const cards = [...w.get('[data-tab="models"]').element.children].map(c => c.getAttribute('data-testid') || c.tagName.toLowerCase())
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
    expect(marked(w)).toContain('models')
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.patch).not.toHaveBeenCalled()
    expect(nav(w).props('modelValue')).toBe('models')
    const editor = editorOf(w)
    editor.rules.value = {}
    await flushPromises()
    expect(marked(w)).not.toContain('models')
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
    expect(marked(w)).toContain('models')
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
