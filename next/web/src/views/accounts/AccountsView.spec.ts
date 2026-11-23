import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flip, render, renderRow, statusTip, today } from './testing/accountsView'
import { list, mocks } from './testing/accountsState'
import { flushPromises, shallowMount } from '@vue/test-utils'
import type { Account } from '@/api/types'
import { i18n } from '@/i18n'
import { SAutoRefresh, SDropdown, SModal, SSwitch } from '@sub2api/ui'
import AccountModelTest from './AccountModelTest.vue'
import AccountGroupsCell from './AccountGroupsCell.vue'
import PlatformIcon from '@/components/platform/PlatformIcon.vue'
import EntityPicker from '@/components/EntityPicker.vue'

describe('account editor dialog', () => {
  it('keeps one fixed size for every account type and step', () => {
    const editor = render().findAllComponents(SModal).find(m => m.props('width') === '2xl' && m.props('persistent'))
    expect(editor?.props('fixed')).toBe(true)
  })
})

describe('account list toolbar', () => {
  it('refreshes from the auto-refresh control, whose button spins while the list loads', () => {
    vi.clearAllMocks()
    list.loading.value = true
    try {
      const auto = render({ SPageHeader: { template: '<div><slot name="actions" /></div>' } }).getComponent(SAutoRefresh)
      expect(auto.props()).toMatchObject({ button: true, loading: true })
      auto.props('onRefresh')?.()
      expect(mocks.reload).toHaveBeenCalledOnce()
    } finally {
      list.loading.value = false
    }
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

describe('account list layout', () => {
  const row = (id: number, over: Partial<Account> = {}) => ({ id, name: 'a@example.com', plugin_key: 'codex', type: 'codex_oauth', status: 'active', schedulable: true, created_by: 1, settings: {}, ...over }) as unknown as Account
  const renderLayout = () => render({ AccountProtocolMarks: false, STable: { props: ['rows', 'columns'], template: '<div><i data-testid="cols">{{ columns.map((c) => c.key).join() }}</i><div v-for="row in rows" :key="row.id"><slot name="cell-name" :row="row" /><slot name="cell-groups" :row="row" /></div></div>' } })
  beforeEach(() => {
    mocks.findType.mockReset()
    mocks.get.mockResolvedValue([])
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
