import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, shallowMount, type DOMWrapper } from '@vue/test-utils'
import { reactive, ref } from 'vue'
import type { Account, AccountType } from '@/api/types'
import { i18n } from '@/i18n'
import { SButton, SDropdown, SModal, SSwitch, STabs } from '@sub2api/ui'
import AccountsView from './AccountsView.vue'
import AccountModelTest from './AccountModelTest.vue'
import AccountEditor from './AccountEditor.vue'
import AccountGroupsCell from './AccountGroupsCell.vue'
import PlatformIcon from '@/components/platform/PlatformIcon.vue'
import EntityPicker from '@/components/EntityPicker.vue'
import PluginSlot from '@/components/plugin/PluginSlot.vue'
import type { PluginAccountActionEntry, SlotEntry } from '@/stores/plugins'

/** Toggles a switch: SSwitch is a checkbox (jsdom only activates connected ones on click), stubs are buttons. */
const flip = (el: Omit<DOMWrapper<Element>, 'exists'>) => (el.element instanceof HTMLInputElement ? el.setValue(!el.element.checked) : el.trigger('click'))

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), patch: vi.fn(), findType: vi.fn(), confirm: vi.fn(), toast: vi.fn(), notifyError: vi.fn(), reload: vi.fn(), canUpdate: true, scope: 'all' as 'all' | 'team' | 'own', slotEntries: vi.fn((_slot: string) => [] as SlotEntry[]), accountActions: [] as PluginAccountActionEntry[], groups: [] as { id: number; name: string; platforms?: string[] }[] }))
vi.mock('@sub2api/host', async (original) => ({ ...await original<typeof import('@sub2api/host')>(), api: { get: mocks.get, post: mocks.post, patch: mocks.patch, list: async () => ({ items: [] }) } }))
vi.mock('@sub2api/ui', async (original) => ({ ...await original<typeof import('@sub2api/ui')>(), confirm: mocks.confirm, toast: mocks.toast }))
vi.mock('@/utils/errors', () => ({ notifyError: mocks.notifyError, errorMessage: String }))
vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }), useRouter: () => ({ replace: vi.fn() }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: () => false, hasAction: () => false }) }))
vi.mock('@/stores/plugins', () => ({ usePluginStore: () => ({ slotEntries: mocks.slotEntries, get accountActions() { return mocks.accountActions }, plugin: () => undefined, component: () => undefined }) }))
vi.mock('@/composables/lookups', async (original) => ({ ...await original<object>(), useGroupsLookup: () => ({ groups: ref(mocks.groups) }), useProxiesLookup: () => ({ proxies: ref([]) }) }))
vi.mock('@/composables/useScope', async (original) => ({ ...await original<typeof import('@/composables/useScope')>(), useScope: () => ({ level: () => mocks.scope, can: (_a: Account, action: string) => action === 'account:update' && mocks.canUpdate }) }))
vi.mock('./accountTypes', () => ({ typeKey: () => '', useAccountTypes: () => ({ load: vi.fn(), find: mocks.findType, grouped: ref([]), types: ref([]), pluginName: (key: string) => key, typeLabel: (_key: string, type: string) => type }) }))
vi.mock('@/composables/useList', async (original) => ({ ...await original<object>(), useList: () => list }))

const list = { items: ref<Account[]>([]), total: ref(1), page: ref(1), pageSize: ref(20), loading: ref(false), error: ref(''), filters: reactive<Record<string, string>>({}), reload: mocks.reload }
enableAutoUnmount(afterEach)

function render(stubs: Record<string, unknown> = {}) {
  return shallowMount(AccountsView, { global: { plugins: [i18n], stubs: {
    STable: { props: ['rows'], template: '<div><div v-for="row in rows" :key="row.id"><slot name="cell-status" :row="row" /><slot name="cell-actions" :row="row" /></div></div>' },
    SLink: { template: '<button><slot /></button>' },
    SBadge: { template: '<span><slot /></span>' },
    SHint: { template: '<span><slot /></span>' },
    STooltip: { props: ['content', 'disabled'], template: '<span :data-tip="disabled ? undefined : content"><slot /></span>' },
    SSwitch: { props: ['modelValue'], emits: ['update:modelValue'], template: '<button role="switch" :aria-checked="modelValue" @click="$emit(\'update:modelValue\', !modelValue)" />' },
    ...stubs
  } } })
}

/** The reason of the status badge: its tooltip (hover or keyboard focus), CONTRACTS §73.4. */
const statusTip = (w: ReturnType<typeof render>) => w.get('[data-tip]').attributes('data-tip')
/** The list with its row buttons rendered (text and attributes), for the enable / disable button. */
const renderRow = () => render({ SButton: { template: '<button><slot /></button>' } })

describe('account automatic disabling policy', () => {
  const account = { id: 23, name: 'test', auto_disable: true, models: [], model_mapping: {}, credentials: {} } as unknown as Account
  const accountType = { plugin_key: 'test', type: 'key', form: { mode: 'native' }, platforms: [], endpoints: [], default_models: [] } as unknown as AccountType
  const renderEditor = (saved: Account | null = account) => shallowMount(AccountEditor, { props: { account: saved, accountType }, global: { plugins: [i18n], stubs: {
    SCard: { template: '<section><slot /></section>' },
    SSettingsGroup: false,
    SSettingRow: false,
    SSwitch: false,
  } } })

  beforeEach(() => {
    vi.clearAllMocks()
    mocks.get.mockReset().mockResolvedValue({ enabled: true })
    mocks.patch.mockResolvedValue(account)
    i18n.global.locale.value = 'zh'
  })

  it.each([true, false])('locks the switch without clearing a saved %s preference when globally off', async (preference) => {
    mocks.get.mockResolvedValue({ enabled: false })
    const wrapper = renderEditor({ ...account, auto_disable: preference })
    await flushPromises()
    const toggle = wrapper.get('[data-testid="account-auto-disable"] [role="switch"], [role="switch"][data-testid="account-auto-disable"]')
    expect(toggle.attributes('disabled')).toBeDefined()
    expect(toggle.attributes('aria-checked')).toBe(String(preference))
    expect(wrapper.text()).toContain('全局自动禁用已关闭')
    await flip(toggle)
    expect(toggle.attributes('aria-checked')).toBe(String(preference))
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.patch).toHaveBeenCalled()
    expect(mocks.patch.mock.calls[0][1]).not.toHaveProperty('auto_disable')
  })

  it('locks new accounts too and permits an individual opt-out when globally enabled', async () => {
    mocks.get.mockResolvedValueOnce({ enabled: false })
    const wrapper = renderEditor(null)
    await flushPromises()
    const toggle = wrapper.get('[data-testid="account-auto-disable"] [role="switch"], [role="switch"][data-testid="account-auto-disable"]')
    expect(toggle.attributes('disabled')).toBeDefined()
    window.dispatchEvent(new Event('focus'))
    await flushPromises()
    expect(toggle.attributes('disabled')).toBeUndefined()
    await flip(toggle)
    expect(toggle.attributes('aria-checked')).toBe('false')
    expect(mocks.get).toHaveBeenCalledWith('/accounts/auto-disable')
  })

  it('keeps the switch locked while loading and on failure, then recovers on focus', async () => {
    let reject!: (error: Error) => void
    mocks.get.mockImplementationOnce(() => new Promise((_resolve, fail) => { reject = fail }))
    const wrapper = renderEditor()
    const toggle = wrapper.get('[data-testid="account-auto-disable"] [role="switch"], [role="switch"][data-testid="account-auto-disable"]')
    expect(toggle.attributes('disabled')).toBeDefined()
    reject(new Error('network unavailable'))
    await flushPromises()
    expect(toggle.attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('暂时无法修改')
    window.dispatchEvent(new Event('focus'))
    await flushPromises()
    expect(toggle.attributes('disabled')).toBeUndefined()
    await flip(toggle)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.patch.mock.calls[0][1]).toMatchObject({ auto_disable: false })
  })

  it('does not let an older enabled response overwrite a newer disabled policy', async () => {
    let resolve!: (policy: { enabled: boolean }) => void
    mocks.get.mockImplementationOnce(() => new Promise(done => { resolve = done }))
    const wrapper = renderEditor()
    mocks.get.mockResolvedValue({ enabled: false })
    window.dispatchEvent(new Event('focus'))
    await flushPromises()
    resolve({ enabled: true })
    await flushPromises()
    expect(wrapper.get('[data-testid="account-auto-disable"] [role="switch"], [role="switch"][data-testid="account-auto-disable"]').attributes('disabled')).toBeDefined()
  })

  it('applies plugin defaults to new accounts and keeps existing read-only preferences', async () => {
    const controlledType: AccountType = { ...accountType, common_controls: {
      auto_disable: { configurable: false, default: false }, schedulable: { configurable: false, default: false },
    } }
    const wrapper = renderEditor(null)
    await wrapper.setProps({ accountType: controlledType })
    await flushPromises()
    for (const key of ['account-auto-disable', 'account-scheduling-control']) {
      const button = wrapper.get(`[data-testid="${key}"] [role="switch"], [role="switch"][data-testid="${key}"]`)
      expect(button.attributes('disabled')).toBeDefined()
      expect(button.attributes('aria-checked')).toBe('false')
    }
    await wrapper.setProps({ account: { ...account, schedulable: true } })
    await flushPromises()
    expect(wrapper.get('[data-testid="account-auto-disable"] [role="switch"], [role="switch"][data-testid="account-auto-disable"]').attributes('aria-checked')).toBe('true')
    expect(wrapper.get('[data-testid="account-scheduling-control"] [role="switch"], [role="switch"][data-testid="account-scheduling-control"]').attributes('aria-checked')).toBe('true')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.patch.mock.calls[0][1]).not.toHaveProperty('auto_disable')
    expect(mocks.patch.mock.calls[0][1]).not.toHaveProperty('schedulable')
  })

  it('updates untouched defaults when switching authentication variants', async () => {
    const wrapper = renderEditor(null)
    await wrapper.setProps({ accountType: { ...accountType, creation_group: 'test', common_controls: { auto_disable: { default: false } } } })
    await flushPromises()
    expect(wrapper.get('[data-testid="account-auto-disable"] [role="switch"], [role="switch"][data-testid="account-auto-disable"]').attributes('aria-checked')).toBe('false')
    await wrapper.setProps({ accountType: { ...accountType, type: 'other', creation_group: 'test', common_controls: { auto_disable: { default: true } } } })
    await flushPromises()
    expect(wrapper.get('[data-testid="account-auto-disable"] [role="switch"], [role="switch"][data-testid="account-auto-disable"]').attributes('aria-checked')).toBe('true')
  })

  it('rechecks the global gate before saving an edited preference', async () => {
    const wrapper = renderEditor()
    await flushPromises()
    await flip(wrapper.get('[data-testid="account-auto-disable"] [role="switch"], [role="switch"][data-testid="account-auto-disable"]'))
    mocks.get.mockResolvedValue({ enabled: false })
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.patch.mock.calls[0][1]).not.toHaveProperty('auto_disable')
    expect(wrapper.get('[data-testid="account-auto-disable"] [role="switch"], [role="switch"][data-testid="account-auto-disable"]').attributes('disabled')).toBeDefined()
  })
})

const today = { query: { tz: Intl.DateTimeFormat().resolvedOptions().timeZone } }

describe('account editor dialog', () => {
  it('keeps one fixed size for every account type and step', () => {
    const editor = render().findAllComponents(SModal).find(m => m.props('width') === '2xl' && m.props('persistent'))
    expect(editor?.props('fixed')).toBe(true)
  })
})

describe('account status recovery', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.canUpdate = true
    mocks.findType.mockReset()
    mocks.confirm.mockResolvedValue(true)
    i18n.global.locale.value = 'zh'
    list.items.value = [{ id: 23, name: 'cc-test', status: 'disabled', status_reason: 'credentials rejected', schedulable: true, auto_disable: true, plugin_key: 'codex', type: 'codex_oauth', orphaned: false, cooldown_until: null } as Account]
  })

  it('locks the enable button according to the plugin declaration', async () => {
    mocks.findType.mockReturnValue({ common_controls: { schedulable: { configurable: false } } })
    const wrapper = renderRow()
    const toggle = wrapper.get('[data-testid="account-enable-toggle"]')
    expect(toggle.attributes('disabled')).toBeDefined()
    await flip(toggle)
    expect(mocks.patch).not.toHaveBeenCalled()
  })

  it('does not let an own-level caller enable or reset an account an administrator disabled', async () => {
    Object.assign(list.items.value[0], { status: 'disabled', schedulable: false, admin_locked: true })
    mocks.scope = 'own'
    try {
      const wrapper = renderRow()
      const toggle = wrapper.get('[data-testid="account-enable-toggle"]')
      expect(toggle.text()).toBe('启用')
      expect(toggle.attributes('disabled')).toBeDefined()
      expect(toggle.attributes('title')).toBe('已被管理员禁用，只有管理员可以重新启用')
      await flip(toggle)
      expect(mocks.patch).not.toHaveBeenCalled()
      // A stale lock on an account the gateway disabled since holds nothing back, as on the server: the
      // reset link is offered.
      list.items.value[0].status = 'error'
      await flushPromises()
      expect(wrapper.find('[data-testid="account-reset-status"]').exists()).toBe(true)

      // The full scope enables it as any other account.
      mocks.scope = 'all'
      list.items.value[0].status = 'disabled'
      const admin = renderRow().get('[data-testid="account-enable-toggle"]')
      expect(admin.attributes('disabled')).toBeUndefined()
      expect(admin.attributes('title')).toBe('启用账号')
    } finally {
      mocks.scope = 'all'
    }
  })

  it.each(['auto_disabled', 'error'])('offers a direct reset for %s without a cooldown', (status) => {
    list.items.value[0].status = status
    const w = render()
    const btn = w.get('[data-testid="account-reset-status"]')
    expect(btn.text()).toBe('')
    expect(btn.attributes('aria-label')).toBe('重置状态')
    expect(btn.attributes('title')).toContain('重置状态')
  })

  it('leaves a manual disable to Enable', () => {
    Object.assign(list.items.value[0], { status: 'disabled', schedulable: false })
    expect(render().find('[data-testid="account-reset-status"]').exists()).toBe(false)
  })

  it('applies the confirmed server state immediately without changing manual scheduling', async () => {
    Object.assign(list.items.value[0], { status: 'auto_disabled', schedulable: false })
    mocks.post.mockResolvedValue({ ...list.items.value[0], status: 'active', status_reason: '', cooldown_until: null })
    const w = render()
    await w.get('[data-testid="account-reset-status"]').trigger('click')
    await flushPromises()
    expect(mocks.post).toHaveBeenCalledWith('/accounts/23/reset-status', undefined, { query: { tz: Intl.DateTimeFormat().resolvedOptions().timeZone } })
    expect(list.items.value[0]).toMatchObject({ status: 'active', status_reason: '', schedulable: false })
    expect(w.find('[data-testid="account-reset-status"]').exists()).toBe(false)
    expect(mocks.reload).toHaveBeenCalledOnce()
    expect(mocks.toast).toHaveBeenCalledWith('账号状态已重置', 'success')
  })

  it('keeps the disabled state and does not report success if reset fails', async () => {
    list.items.value[0].status = 'auto_disabled'
    const error = new Error('reset failed')
    mocks.post.mockRejectedValue(error)
    const w = render()
    await w.get('[data-testid="account-reset-status"]').trigger('click')
    await flushPromises()
    expect(list.items.value[0].status).toBe('auto_disabled')
    expect(mocks.notifyError).toHaveBeenCalledWith(error)
    expect(mocks.toast).not.toHaveBeenCalled()
  })

  it('does not reset when confirmation is canceled', async () => {
    list.items.value[0].status = 'auto_disabled'
    mocks.confirm.mockResolvedValue(false)
    const w = render()
    await w.get('[data-testid="account-reset-status"]').trigger('click')
    await flushPromises()
    expect(mocks.post).not.toHaveBeenCalled()
  })

  it('does not show recovery for readers or orphaned accounts', () => {
    list.items.value[0].status = 'auto_disabled'
    mocks.canUpdate = false
    const reader = render()
    expect(reader.find('[data-testid="account-reset-status"]').exists()).toBe(false)
    mocks.canUpdate = true
    list.items.value[0].orphaned = true
    const orphaned = render()
    expect(orphaned.find('[data-testid="account-reset-status"]').exists()).toBe(false)
  })

  it('shows an automatic disable as a plain badge, with the reason and the enable time in a focusable tooltip', () => {
    Object.assign(list.items.value[0], { status: 'auto_disabled', status_reason: 'rate limited until 2026-10-12T08:00:00Z', auto_disabled_until: '2026-10-12T08:00:00Z' })
    const w = renderRow()
    const badge = w.get('[data-testid="account-status"]')
    expect(badge.text()).toBe('自动禁用')
    expect(badge.attributes('title')).toBeUndefined()
    expect(w.get('[data-testid="account-status-tip"]').attributes('tabindex')).toBe('0')
    const [reason, release] = statusTip(w)!.split('\n')
    expect(reason).toBe('rate limited until 2026-10-12T08:00:00Z')
    expect(release).toMatch(/自动启用$/)
    expect(w.find('[data-testid="account-status-note"]').exists()).toBe(false)
    expect(w.text()).not.toContain('rate limited until')
    // Not enabled: the button offers Enable, which enables the account by hand.
    const toggle = w.get('[data-testid="account-enable-toggle"]')
    expect(toggle.text()).toBe('启用')
    expect(toggle.attributes('title')).toContain('点击即手动启用')
  })

  it('keeps a badge without a reason out of the tab order', () => {
    Object.assign(list.items.value[0], { status: 'active', schedulable: true, status_reason: '', cooldown_until: null })
    const w = render()
    expect(w.get('[data-testid="account-status-tip"]').attributes('tabindex')).toBeUndefined()
    expect(w.find('[data-tip]').exists()).toBe(false)
  })

  it('words the reason of the usage limit from the usage figures (CONTRACTS §73)', () => {
    const usage = { raw_cost_accounting: false, limit_disabled: true, limit: { amount: '200', used: '200.004', period: 'week', period_count: 1, window_start: null, window_end: null, waived: false } }
    Object.assign(list.items.value[0], { status: 'auto_disabled', status_reason: 'usage limit reached: $200.00 / $200.00 (per week)', auto_disabled_until: null, usage })
    expect(statusTip(render())).toBe('用量已达上限：$200.00 / $200.00（每周）')
    i18n.global.locale.value = 'en'
    usage.limit.period_count = 2
    expect(statusTip(render())).toBe('Usage limit reached: $200.00 / $200.00 (per 2 weeks)')
    Object.assign(list.items.value[0], { usage: { ...usage, limit: null } })
    expect(statusTip(render())).toBe('Usage limit reached')
  })

  it('keeps the server reason when the usage limit did not disable the account', () => {
    Object.assign(list.items.value[0], { status: 'auto_disabled', status_reason: 'usage limit reached: $1 / $1 (total)', auto_disabled_until: null, usage: { limit_disabled: false, limit: null } })
    expect(statusTip(render())).toBe('usage limit reached: $1 / $1 (total)')
  })

  it('shows a manual disable as a plain badge without the reason or a note', () => {
    const w = render()
    expect(w.get('[data-testid="account-status"]').text()).toBe('禁用')
    expect(statusTip(w)).toBe('credentials rejected')
    expect(w.text()).not.toContain('credentials rejected')
    expect(w.find('[data-testid="account-status-note"]').exists()).toBe(false)
  })

  it.each([
    [{ status: 'error' }, '自动禁用'],
    [{ status: 'active', schedulable: false }, '自动禁用'],
    [{ status: 'active', schedulable: true, orphaned: true }, '启用']
  ])('shows only the three statuses (%o)', (row, label) => {
    Object.assign(list.items.value[0], row)
    expect(render().get('[data-testid="account-status"]').text()).toBe(label)
  })

  it('keeps a cooling account enabled and notes the cooldown end', () => {
    Object.assign(list.items.value[0], { status: 'active', schedulable: true, status_reason: '', cooldown_until: new Date(Date.now() + 60_000).toISOString(), cooldown_reason: 'upstream 529' })
    const w = render()
    expect(w.get('[data-testid="account-status"]').text()).toBe('启用')
    expect(statusTip(w)).toBe('upstream 529')
    expect(w.get('[data-testid="account-status-note"]').text()).toMatch(/^冷却至 /)
  })

  it.each([
    { status: 0, error_code: 'proxy_unavailable', error_source: 'gateway' },
    { status: 401, error_code: 'token_revoked', error_source: 'response' },
    { status: 502, error_code: 'ECONNRESET', error_source: 'transport', message: '{"error":{"code":"ECONNRESET","message":"read ECONNRESET"}}' },
    { status: 429, error_code: 'rate_limit_error', error_source: 'response', message: '{"error":{"type":"rate_limit_error","message":"Error"}}', reason: 'rate limited until tomorrow (default)', effect: 'disable', response_headers: { 'request-id': 'req-test-429' } },
  ])('shows diagnostic code without fabricating HTTP status: $error_code', async (diagnostic) => {
    mocks.post.mockResolvedValue({ ok: false, latency_ms: 0, message: 'failure', ...diagnostic })
    const w = shallowMount(AccountModelTest, { props: { open: false, account: { ...list.items.value[0], models: ['claude-test'] } }, global: { plugins: [i18n], stubs: {
      SModal: { template: '<div><slot /></div>' },
      STable: { props: ['rows'], template: '<div><div v-for="row in rows" :key="row.model"><slot name="cell-state" :row="row" /><slot name="cell-actions" :row="row" /></div></div>' },
      SButton: { template: '<button><slot /></button>' },
      SBadge: { template: '<span><slot /></span>' },
      SHint: { template: '<span><slot /></span>' },
    } } })
    await w.setProps({ open: true })
    await w.get('[data-testid="model-test-one"]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid="model-test-error-code"]').text()).toContain(diagnostic.error_code)
    expect(w.text().includes('401')).toBe(diagnostic.status === 401)
    expect(w.emitted('tested')?.[0]?.[1]).toMatchObject({ status: diagnostic.status, error_code: diagnostic.error_code, error_source: diagnostic.error_source })
    if (diagnostic.error_source === 'transport') {
      expect(w.get('[data-testid="model-test-error-body"]').text()).toBe(diagnostic.message)
      expect(w.text()).toContain('传输链路')
      expect(w.text()).not.toContain('代理故障')
      expect(w.text()).not.toContain('HTTP 响应')
    }
    if (diagnostic.status === 429) {
      expect(w.get('[data-testid="model-test-error-body"]').text()).toBe(diagnostic.message)
      expect(w.text()).not.toContain('rate limited until')
      expect(w.text()).not.toContain('原因尚不能确认')
      expect(w.get('[data-testid="model-test-request-id"]').text()).toContain('req-test-429')
      expect(w.text()).not.toContain('建议禁用')
    }
  })

  it('shows a successful model test like new-api: outcome and request time, request id only in the details', async () => {
    mocks.post.mockResolvedValue({ ok: true, status: 200, latency_ms: 3550, model: 'claude-test', protocol: 'anthropic.messages', message: 'ok', response_headers: { 'request-id': 'req_ok_1' } })
    const w = shallowMount(AccountModelTest, { props: { open: false, account: { ...list.items.value[0], models: ['claude-test'] } }, global: { plugins: [i18n], stubs: {
      SModal: { template: '<div><slot /></div>' },
      STable: { props: ['rows'], template: '<div><div v-for="row in rows" :key="row.model"><slot name="cell-state" :row="row" /><slot name="cell-actions" :row="row" /></div></div>' },
      SButton: { template: '<button><slot /></button>' },
      SBadge: { template: '<span><slot /></span>' },
      SHint: { template: '<span><slot /></span>' },
    } } })
    await w.setProps({ open: true })
    await w.get('[data-testid="model-test-one"]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid="model-test-duration"]').text()).toBe('请求时长 3.55 s')
    expect(w.get('[data-testid="model-test-protocol"]').text()).toBe('anthropic.messages')
    expect(w.emitted('tested')?.[0]?.[1]).toMatchObject({ protocol: 'anthropic.messages' })
    expect(w.find('[data-testid="model-test-request-id"]').exists()).toBe(false)
    expect(w.text()).not.toContain('req_ok_1')
  })

  it('turns an enabled unschedulable account back on with one click, then disables it by hand', async () => {
    Object.assign(list.items.value[0], { status: 'active', schedulable: false, status_reason: '' })
    mocks.patch.mockImplementation(async (_path: string, values: Partial<Account>) => ({ ...list.items.value[0], ...values, status_reason: '' }))
    const w = renderRow()
    const toggle = w.get('[data-testid="account-enable-toggle"]')
    expect(w.text()).toContain('自动禁用')
    expect(toggle.text()).toBe('启用')
    expect(toggle.attributes('aria-label')).toBe('启用：cc-test')
    await flip(toggle)
    await flushPromises()
    expect(mocks.patch).toHaveBeenLastCalledWith('/accounts/23', { status: 'active', schedulable: true }, today)
    expect(list.items.value[0]).toMatchObject({ status: 'active', schedulable: true })
    expect(toggle.text()).toBe('禁用')
    expect(w.text()).not.toContain('自动禁用')
    expect(mocks.toast).toHaveBeenLastCalledWith('已启用「cc-test」', 'success')
    await flip(toggle)
    await flushPromises()
    expect(mocks.patch).toHaveBeenLastCalledWith('/accounts/23', { status: 'disabled', schedulable: false }, today)
    expect(list.items.value[0].status).toBe('disabled')
    expect(toggle.text()).toBe('启用')
    expect(mocks.toast).toHaveBeenLastCalledWith('已禁用「cc-test」', 'success')
    expect(mocks.post).not.toHaveBeenCalled()
  })

  it('shows the new state at once and puts the old one back when the request fails', async () => {
    Object.assign(list.items.value[0], { status: 'auto_disabled', status_reason: 'matched auto-disable rule: status 401', auto_disabled_until: '2026-10-12T08:00:00Z' })
    let reject!: (e: unknown) => void
    mocks.patch.mockReturnValueOnce(new Promise((_, r) => { reject = r }))
    const w = renderRow()
    const toggle = w.get('[data-testid="account-enable-toggle"]')
    await flip(toggle)
    await w.vm.$nextTick()
    // Optimistic: enabled, reason gone, the button locked until the server answers.
    expect(list.items.value[0]).toMatchObject({ status: 'active', schedulable: true, status_reason: '', auto_disabled_until: null })
    expect(toggle.text()).toBe('禁用')
    expect(toggle.attributes('disabled')).toBeDefined()
    const error = new Error('enable failed')
    reject(error)
    await flushPromises()
    expect(list.items.value[0]).toMatchObject({ status: 'auto_disabled', schedulable: true, status_reason: 'matched auto-disable rule: status 401', auto_disabled_until: '2026-10-12T08:00:00Z' })
    expect(toggle.text()).toBe('启用')
    expect(toggle.attributes('disabled')).toBeUndefined()
    expect(mocks.notifyError).toHaveBeenCalledWith(error)
    expect(mocks.toast).not.toHaveBeenCalled()
  })

  it('keeps a manually disabled account off even when scheduling is on, and retains that state if enabling fails', async () => {
    const error = new Error('enable failed')
    mocks.patch.mockRejectedValueOnce(error)
    const w = renderRow()
    const toggle = w.get('[data-testid="account-enable-toggle"]')
    expect(toggle.text()).toBe('启用')
    expect(toggle.attributes('title')).toBe('启用账号')
    await flip(toggle)
    await flushPromises()
    expect(mocks.patch).toHaveBeenCalledWith('/accounts/23', { status: 'active', schedulable: true }, today)
    expect(toggle.text()).toBe('启用')
    expect(list.items.value[0]).toMatchObject({ status: 'disabled', status_reason: 'credentials rejected' })
    expect(mocks.notifyError).toHaveBeenCalledWith(error)
  })

  it('shows readers and orphaned accounts the badge without the enable button', () => {
    mocks.canUpdate = false
    expect(renderRow().find('[data-testid="account-enable-toggle"]').exists()).toBe(false)
    mocks.canUpdate = true
    list.items.value[0].orphaned = true
    const w = render()
    expect(w.find('[data-testid="account-enable-toggle"]').exists()).toBe(false)
    expect(w.get('[data-testid="account-status"]').text()).toBe('禁用')
  })

  it('offers an automatically disabled account a manual disable in the row menu', async () => {
    Object.assign(list.items.value[0], { status: 'auto_disabled', auto_disabled_until: '2026-10-12T08:00:00Z' })
    mocks.patch.mockImplementation(async (_path: string, values: Partial<Account>) => ({ ...list.items.value[0], ...values, status_reason: 'disabled by administrator' }))
    const w = render({ STable: { props: ['rows'], template: '<div><div v-for="row in rows" :key="row.id"><slot name="cell-actions" :row="row" /></div></div>' } })
    const menu = w.findAllComponents(SDropdown).at(-1)!
    expect(menu.props('actions').find((x: { key: string }) => x.key === 'disable')).toMatchObject({ label: '转为手动禁用', hidden: false })
    menu.vm.$emit('select', 'disable')
    await flushPromises()
    expect(mocks.patch).toHaveBeenLastCalledWith('/accounts/23', { status: 'disabled', schedulable: false }, today)
    expect(list.items.value[0].status).toBe('disabled')
    expect(menu.props('actions').find((x: { key: string }) => x.key === 'disable')?.hidden).toBe(true)
  })
})

describe('declared plugin account actions and the toolbar slot (CONTRACTS §23.3 UI 3.6)', () => {
  const reset: PluginAccountActionEntry = { pluginKey: 'dynweight', id: 'reset', label: { en: 'Reset dynamic weight', zh: '重置动态权重' }, permission: 'dynweight:manage', route: 'POST /reset', bulk: true, danger: true, confirm: { en: 'Reset {n}?', zh: '重置 {n} 个账号的动态权重？' } }
  const row = (id: number, over: Partial<Account> = {}) => ({ id, name: `acc-${id}`, status: 'active', schedulable: true, plugin_key: 'anthropic', type: 'apikey', orphaned: false, ...over }) as Account
  // The bulk bar and the header render their slots; the table offers a button that selects every row.
  const stubs = {
    SBulkBar: { template: '<div><slot /></div>' },
    STable: { props: ['rows'], emits: ['update:selected'], template: '<div><button data-testid="select-rows" @click="$emit(\'update:selected\', rows.map(r => r.id))" /><div v-for="row in rows" :key="row.id"><slot name="cell-actions" :row="row" /></div></div>' },
    SPageHeader: { template: '<div><slot name="filters-primary" /><slot name="filters" /></div>' },
    SChip: { emits: ['close'], template: '<span><slot /><button data-testid="chip-close" @click="$emit(\'close\')" /></span>' }
  }
  const dropdowns = (w: ReturnType<typeof render>) => w.findAllComponents(SDropdown)
  const bulkMore = (w: ReturnType<typeof render>) => dropdowns(w).filter((d) => d.attributes('data-testid') === 'account-bulk-more')[0]
  const bulkKeys = (w: ReturnType<typeof render>) => (bulkMore(w).props('actions') as Array<{ key: string }>).map((x) => x.key)
  const posted = (ids: number[]) => expect(mocks.post).toHaveBeenCalledWith('/p/dynweight/reset', { account_ids: ids }, expect.objectContaining({ signal: expect.any(AbortSignal) }))
  /** The props the toolbar slot hands to plugin `key`'s component. */
  const slotProps = (w: ReturnType<typeof render>, key: string) => {
    const p = w.getComponent(PluginSlot).props('props')
    return (typeof p === 'function' ? p({ pluginKey: key, name: 'DegradedBadge', component: {} }) : p) ?? {}
  }
  const chips = (w: ReturnType<typeof render>) => w.findAll('[data-testid="ids-filter"]').map((c) => c.text())

  beforeEach(() => {
    vi.clearAllMocks()
    mocks.canUpdate = true
    mocks.accountActions = [reset]
    mocks.slotEntries.mockReturnValue([])
    mocks.confirm.mockResolvedValue(true)
    mocks.post.mockResolvedValue({})
    i18n.global.locale.value = 'zh'
    list.items.value = [row(29), row(30)]
    for (const key of Object.keys(list.filters)) delete list.filters[key]
  })

  it('offers the action in the row menu and posts the account id to the plugin route after confirming', async () => {
    const w = render()
    const items = dropdowns(w)[0].props('actions')
    expect(items.find((x) => x.key === 'declared:dynweight:reset')).toMatchObject({ label: '重置动态权重', hidden: false })
    dropdowns(w)[0].vm.$emit('select', 'declared:dynweight:reset')
    await flushPromises()
    expect(mocks.confirm).toHaveBeenCalledWith(expect.objectContaining({ message: '重置 1 个账号的动态权重？', danger: true }))
    posted([29])
    expect(mocks.toast).toHaveBeenLastCalledWith('「重置动态权重」已完成', 'success')
    expect(mocks.reload).toHaveBeenCalled()
  })

  it('hides the action from readers and on orphaned accounts', () => {
    list.items.value = [row(29), row(31, { orphaned: true })]
    const hidden = (w: ReturnType<typeof render>, i: number) => dropdowns(w)[i].props('actions').find((x) => x.key === 'declared:dynweight:reset')?.hidden
    const w = render()
    expect([hidden(w, 0), hidden(w, 1)]).toEqual([false, true])
    mocks.canUpdate = false
    expect(hidden(render(), 0)).toBe(true)
  })

  it('adds bulk actions to the "more" menu, counting the editable selection, and sends the ids together', async () => {
    const w = render(stubs)
    await w.get('[data-testid="select-rows"]').trigger('click')
    await flushPromises()
    expect(bulkMore(w).props('actions')).toContainEqual(expect.objectContaining({ key: 'declared:dynweight:reset', label: '重置动态权重（2）', danger: true, disabled: false }))
    bulkMore(w).vm.$emit('select', 'declared:dynweight:reset')
    await flushPromises()
    expect(mocks.confirm).toHaveBeenCalledWith(expect.objectContaining({ message: '重置 2 个账号的动态权重？' }))
    posted([29, 30])
    expect(mocks.reload).toHaveBeenCalled()
    mocks.accountActions = [{ ...reset, bulk: false }]
    const without = render(stubs)
    await without.get('[data-testid="select-rows"]').trigger('click')
    await flushPromises()
    expect(bulkKeys(without).some((key) => key.startsWith('declared:'))).toBe(false)
    mocks.accountActions = []
    const none = render(stubs)
    await none.get('[data-testid="select-rows"]').trigger('click')
    await flushPromises()
    expect(bulkKeys(none)).toEqual(['enable', 'disable', 'reset'])
  })

  it('bulk enables every selected account that is not enabled, automatic disables included, like the row switch', async () => {
    list.items.value = [row(29), row(30, { status: 'auto_disabled' }), row(31, { status: 'disabled', schedulable: false }), row(32, { schedulable: false })]
    mocks.patch.mockImplementation(async (path: string, values: Partial<Account>) => ({ ...list.items.value.find((a) => path.endsWith(`/${a.id}`)), ...values }))
    const w = render(stubs)
    await w.get('[data-testid="select-rows"]').trigger('click')
    await flushPromises()
    bulkMore(w).vm.$emit('select', 'enable')
    await flushPromises()
    expect(mocks.patch.mock.calls.map((c) => c[0]).sort()).toEqual(['/accounts/30', '/accounts/31', '/accounts/32'])
    for (const c of mocks.patch.mock.calls) expect(c[1]).toEqual({ status: 'active', schedulable: true })
    expect(list.items.value.every((a) => a.status === 'active' && a.schedulable)).toBe(true)
    mocks.patch.mockClear()
    list.items.value[1].status = 'auto_disabled'
    bulkMore(w).vm.$emit('select', 'disable')
    await flushPromises()
    expect(mocks.patch.mock.calls.map((c) => c[0]).sort()).toEqual(['/accounts/29', '/accounts/30', '/accounts/31', '/accounts/32'])
    for (const c of mocks.patch.mock.calls) expect(c[1]).toEqual({ status: 'disabled', schedulable: false })
  })

  it('lets a visibleFor "all" bulk action target every selected non-orphaned row, not just the editable ones', async () => {
    mocks.accountActions = [{ ...reset, visibleFor: 'all' }]
    mocks.canUpdate = false
    list.items.value = [row(29), row(30), row(31, { orphaned: true })]
    const w = render(stubs)
    await w.get('[data-testid="select-rows"]').trigger('click')
    await flushPromises()
    expect(bulkMore(w).props('actions')).toContainEqual(expect.objectContaining({ key: 'declared:dynweight:reset', label: '重置动态权重（2）', disabled: false }))
    bulkMore(w).vm.$emit('select', 'declared:dynweight:reset')
    await flushPromises()
    posted([29, 30])
    // The default visibility keeps the bulk action to editable rows, so a reader has none.
    mocks.accountActions = [reset]
    const reader = render(stubs)
    await reader.get('[data-testid="select-rows"]').trigger('click')
    await flushPromises()
    expect(bulkMore(reader).props('actions')).toContainEqual(expect.objectContaining({ key: 'declared:dynweight:reset', label: '重置动态权重（0）', disabled: true }))
  })

  it('limits an action declaring accountTypes to its plugin rows of those types, in the row menu and the bulk bar (UI 3.8)', async () => {
    mocks.accountActions = [{ ...reset, accountTypes: ['device'] }]
    list.items.value = [row(29), row(30, { plugin_key: 'dynweight', type: 'device' }), row(31, { type: 'device' }), row(32, { plugin_key: 'dynweight', type: 'device', orphaned: true }), row(33, { plugin_key: 'dynweight', type: 'device' })]
    const w = render(stubs)
    const hidden = dropdowns(w).slice(0, 5).map((d) => d.props('actions').find((x: { key: string }) => x.key === 'declared:dynweight:reset')?.hidden)
    expect(hidden).toEqual([true, false, true, true, false])
    await w.get('[data-testid="select-rows"]').trigger('click')
    await flushPromises()
    expect(bulkMore(w).props('actions')).toContainEqual(expect.objectContaining({ key: 'declared:dynweight:reset', label: '重置动态权重（2）', disabled: false }))
    bulkMore(w).vm.$emit('select', 'declared:dynweight:reset')
    await flushPromises()
    posted([30, 33])
  })

  it('shows a detail tab declaring accountTypes only on its plugin accounts of those types, back on the overview for each account (UI 3.8)', async () => {
    const tab = (pluginKey: string, name: string, accountTypes?: string[]): SlotEntry => ({ pluginKey, name, component: {}, accountTypes })
    mocks.slotEntries.mockImplementation((slot: string) => (slot === 'account.detail.tabs' ? [tab('anthropic', 'Usage'), tab('anthropic', 'Quota', ['oauth']), tab('other', 'Keys', ['apikey'])] : []))
    list.items.value = [row(29), row(30, { type: 'oauth' })]
    mocks.get.mockImplementation(async (path: string) => list.items.value.find((a) => path === `/accounts/${a.id}`))
    const w = render({ SModal: { template: '<div><slot /></div>' }, STabs: { props: ['tabs', 'modelValue'], emits: ['update:modelValue'], template: '<div />' } })
    const open = async (i: number) => {
      dropdowns(w)[i].vm.$emit('select', 'detail')
      await flushPromises()
      const tabs = w.getComponent(STabs)
      return { tabs, keys: (tabs.props('tabs') as Array<{ key: string }>).map((x) => x.key), active: tabs.props('modelValue') }
    }
    const oauth = await open(1)
    expect(oauth.keys).toEqual(['overview', 'anthropic:Usage', 'anthropic:Quota'])
    oauth.tabs.vm.$emit('update:modelValue', 'anthropic:Quota')
    await flushPromises()
    expect(w.getComponent(STabs).props('modelValue')).toBe('anthropic:Quota')
    expect(await open(0)).toMatchObject({ keys: ['overview', 'anthropic:Usage'], active: 'overview' })
  })

  it('shows quota column decorations only on rows of their plugin and account types, adding the column for them (UI 3.8)', async () => {
    const badge = (name: string, accountTypes?: string[]): SlotEntry => ({ pluginKey: 'dynweight', name, component: {}, accountTypes })
    const device = badge('DeviceQuota', ['device'])
    mocks.slotEntries.mockImplementation((slot: string) => (slot === 'account.list.quota' ? [device] : []))
    const table = { STable: { props: ['rows', 'columns'], template: '<div><i data-testid="cols">{{ columns.map(c => c.key).join() }}</i><div v-for="row in rows" :key="row.id"><slot name="cell-quota" :row="row" /></div></div>' } }
    const quotaSlots = (w: ReturnType<typeof render>) => w.findAllComponents(PluginSlot).filter((s) => s.props('name') === 'account.list.quota')
    // No row of the type, no quota or balance: no column.
    list.items.value = [row(29), row(30, { type: 'device' })]
    expect(render(table).get('[data-testid="cols"]').text()).not.toContain('quota')
    list.items.value = [row(29), row(31, { plugin_key: 'dynweight', type: 'device' })]
    const w = render(table)
    expect(w.get('[data-testid="cols"]').text()).toContain('quota')
    const slots = quotaSlots(w)
    expect(slots).toHaveLength(1)
    expect(slots[0].props('props')).toEqual({ account: list.items.value[1] })
    // Entries reach the plugin component only for the row they match.
    const only = slots[0].props('only') as (e: SlotEntry) => boolean
    expect([only(device), only(badge('KeyQuota', ['apikey'])), only(badge('Any'))]).toEqual([true, false, true])
  })

  it('keeps only distinct positive integer ids from setIds', async () => {
    list.items.value = []
    const w = render(stubs)
    slotProps(w, 'a').setIds([3, 3, 0, -1, 2.5, NaN, 7] as number[], 'A')
    await flushPromises()
    expect(slotProps(w, 'a').ids).toEqual([3, 7])
    expect(list.filters.ids).toBe('3,7')
    slotProps(w, 'a').setIds([0, -2], 'A')
    await flushPromises()
    expect(slotProps(w, 'a').ids).toBeNull()
    expect(list.filters.ids).toBe('')
  })

  it('mounts the toolbar slot among the primary filters and filters the list to the ids common to every plugin, one closable chip each', async () => {
    list.items.value = []
    const w = render(stubs)
    expect(w.getComponent(PluginSlot).props('name')).toBe('account.list.toolbar')
    expect(slotProps(w, 'a').ids).toBeNull()
    expect(chips(w)).toEqual([])
    slotProps(w, 'a').setIds([12, 21], 'A')
    await flushPromises()
    expect(list.filters.ids).toBe('12,21')
    expect(chips(w)).toEqual(['A'])
    slotProps(w, 'b').setIds([21, 7], 'B')
    await flushPromises()
    expect(list.filters.ids).toBe('21')
    expect(chips(w)).toEqual(['A', 'B'])
    // Each plugin sees only its own set.
    expect(slotProps(w, 'a').ids).toEqual([12, 21])
    expect(slotProps(w, 'b').ids).toEqual([21, 7])
    await w.findAll('[data-testid="chip-close"]')[0].trigger('click')
    expect(list.filters.ids).toBe('21,7')
    expect(chips(w)).toEqual(['B'])
    expect(slotProps(w, 'a').ids).toBeNull()
    // Sets without a common id ask for id 0, which no account has.
    slotProps(w, 'a').setIds([12], 'A')
    await flushPromises()
    expect(list.filters.ids).toBe('0')
    expect(chips(w)).toEqual(['B', 'A'])
    w.findAllComponents(SButton).filter((b) => b.attributes('data-testid') === 'clear-filters')[0].vm.$emit('click')
    await flushPromises()
    expect(list.filters.ids).toBe('')
    expect(chips(w)).toEqual([])
    expect(slotProps(w, 'b').ids).toBeNull()
  })

  it('cuts the common ids at MAX_ACCOUNT_BATCH, says so on the chips, and treats an empty array as clearing', async () => {
    list.items.value = []
    const w = render(stubs)
    slotProps(w, 'a').setIds(Array.from({ length: 600 }, (_, i) => i + 1), '动态权重降权 600')
    await flushPromises()
    const sent = String(list.filters.ids).split(',')
    expect(sent).toHaveLength(500)
    expect(sent[499]).toBe('500')
    expect(slotProps(w, 'a').ids).toHaveLength(600)
    expect(chips(w)).toEqual(['动态权重降权 600（前 500 个）'])
    // A second plugin's chip carries no suffix: the cut applies to the intersection once.
    slotProps(w, 'b').setIds(Array.from({ length: 600 }, (_, i) => i + 1), 'B')
    await flushPromises()
    expect(chips(w)).toEqual(['动态权重降权 600（前 500 个）', 'B'])
    slotProps(w, 'b').setIds(null, 'B')
    slotProps(w, 'a').setIds([], 'x')
    await flushPromises()
    expect(list.filters.ids).toBe('')
    expect(chips(w)).toEqual([])
  })
})

describe('account list layout', () => {
  const row = (id: number, over: Partial<Account> = {}) => ({ id, name: 'a@example.com', plugin_key: 'codex', type: 'codex_oauth', status: 'active', schedulable: true, created_by: 1, settings: {}, ...over }) as unknown as Account
  const renderLayout = () => render({ AccountProtocolMarks: false, STable: { props: ['rows', 'columns'], template: '<div><i data-testid="cols">{{ columns.map((c) => c.key).join() }}</i><div v-for="row in rows" :key="row.id"><slot name="cell-name" :row="row" /><slot name="cell-groups" :row="row" /></div></div>' } })
  beforeEach(() => {
    mocks.findType.mockReset()
    mocks.groups = []
  })

  it('shows the creator column only when the page has more than one creator', () => {
    list.items.value = [row(1), row(2)]
    expect(renderLayout().get('[data-testid="cols"]').text()).not.toContain('created_by')
    list.items.value = [row(1), row(2, { created_by: 2 })]
    expect(renderLayout().get('[data-testid="cols"]').text()).toContain('created_by')
  })

  it('shows the owner column to every reader past its own rows, and the creator only to one over every row', () => {
    list.items.value = [row(1, { owner_id: 7 }), row(2, { owner_id: 7 })]
    const cols = () => renderLayout().get('[data-testid="cols"]').text().split(',')
    expect(cols()).toContain('owner')
    expect(cols()).not.toContain('created_by')
    mocks.scope = 'team'
    try {
      list.items.value = [row(1, { owner_id: 7 }), row(2, { owner_id: 8, created_by: 2 })]
      expect(cols()).toContain('owner')
      expect(cols()).not.toContain('created_by')
      mocks.scope = 'own'
      expect(cols()).not.toContain('owner')
    } finally {
      mocks.scope = 'all'
    }
  })

  it("leads the type line with the marks of the platforms the account's type serves now", () => {
    mocks.findType.mockReturnValue({ platforms: [{ id: 'anthropic', available: true }, { id: 'foo', available: false }, { id: 'openai' }] })
    list.items.value = [row(1)]
    const w = renderLayout()
    expect(w.find('[data-testid="account-platforms"]').exists()).toBe(true)
    expect(w.findAllComponents(PlatformIcon).map((icon) => icon.props('id'))).toEqual(['anthropic', 'openai'])
  })

  it('shows no platform mark for a type that is gone or serves no platform now', () => {
    list.items.value = [row(1)]
    expect(renderLayout().find('[data-testid="account-platforms"]').exists()).toBe(false)
    mocks.findType.mockReturnValue({ platforms: [{ id: 'foo', available: false }] })
    expect(renderLayout().find('[data-testid="account-platforms"]').exists()).toBe(false)
  })

  it('hands the groups cell every group of the account: the name the row carries, the platforms the lookup knows', () => {
    mocks.groups = [{ id: 2, name: 'vip', platforms: ['anthropic'] }, { id: 3, name: 'renamed-since', platforms: ['gemini', 'openai'] }]
    list.items.value = [row(1, { group_ids: [2, 3, 9], groups: [{ id: 3, name: 'batch' }] })]
    expect(renderLayout().getComponent(AccountGroupsCell).props('groups')).toEqual([
      { id: 2, name: 'vip', platforms: ['anthropic'] },
      { id: 3, name: 'batch', platforms: ['gemini', 'openai'] },
      { id: 9, name: '#9', platforms: undefined }
    ])
  })
})

describe('account list filters', () => {
  const renderFilters = () => render({ SPageHeader: { template: '<div><slot name="filters" /></div>' }, SButton: { template: '<button><slot /></button>' } })
  /** [kind, test id] of the pickers on screen. */
  const pickers = (w: ReturnType<typeof render>) => w.findAllComponents(EntityPicker).map((p) => [p.props('kind'), p.attributes('data-testid')])
  beforeEach(() => {
    for (const key of Object.keys(list.filters)) delete list.filters[key]
  })

  it('picks the group, and under the advanced filters the model, the owner and the creator, into the list filters', async () => {
    const w = renderFilters()
    expect(pickers(w)).toEqual([['group', 'group-filter']])
    await w.get('[aria-expanded]').trigger('click')
    expect(pickers(w)).toEqual([['group', 'group-filter'], ['model', 'model-filter'], ['user', 'owner-filter'], ['user', 'created-by-filter']])
    expect(w.find('[data-testid="only-subordinates"]').exists()).toBe(false)
    const pick = (testid: string, id: string) => w.getComponent<typeof EntityPicker>(`[data-testid="${testid}"]`).vm.$emit('update:id', id)
    pick('group-filter', '3')
    pick('model-filter', 'claude-opus-5')
    pick('owner-filter', '8')
    pick('created-by-filter', '7')
    expect(list.filters).toEqual({ group_id: '3', model: 'claude-opus-5', owner_id: '8', created_by: '7' })
    await flushPromises()
    expect(w.getComponent<typeof EntityPicker>('[data-testid="group-filter"]').props('id')).toBe('3')
    // The filters count and clear as before.
    await w.get('[data-testid="clear-filters"]').trigger('click')
    expect(Object.values(list.filters).filter(Boolean)).toEqual([])
  })

  it('gives a team reader the owner filter and its team switch, and no creator filter (CONTRACTS §103.4)', async () => {
    mocks.scope = 'team'
    try {
      const w = renderFilters()
      await w.get('[aria-expanded]').trigger('click')
      expect(pickers(w)).toEqual([['group', 'group-filter'], ['model', 'model-filter'], ['user', 'owner-filter']])
      w.getComponent<typeof SSwitch>('[data-testid="only-subordinates"]').vm.$emit('update:modelValue', true)
      expect(list.filters.subordinates).toBe('true')
    } finally {
      mocks.scope = 'all'
    }
  })
})
