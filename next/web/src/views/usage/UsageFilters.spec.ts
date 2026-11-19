import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { reactive, ref } from 'vue'
import { createPinia } from 'pinia'
import { i18n } from '@/i18n'
import EntityPicker from '@/components/EntityPicker.vue'
import { resetEntityLookups } from '@/components/entity/lookup'
import UsageFilters from './UsageFilters.vue'
import UsageOverviewView from './UsageOverviewView.vue'
import UsageView from './UsageView.vue'
import UsageIPPanel from './UsageIPPanel.vue'
import { mountPinned } from '@sub2api/ui/testing'

const get = vi.hoisted(() => vi.fn(async (..._args: unknown[]): Promise<unknown> => []))
const route = vi.hoisted(() => ({ query: {} as Record<string, string> }))
vi.mock('@sub2api/host', async original => ({ ...(await original<typeof import('@sub2api/host')>()), api: { get, list: async () => ({ items: [], page: {} }) } }))
vi.mock('vue-router', () => ({ useRoute: () => route, useRouter: () => ({ replace: vi.fn() }) }))
// Every permission but the accounts': the pickers look their entities up, the pages show no account.
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: (permission: string | string[]) => permission !== 'account:read', hasAction: () => true }) }))
vi.mock('@/composables/useList', async importOriginal => ({ ...(await importOriginal<object>()), useList: (_path: string, filters: Record<string, unknown>) => ({ items: ref([]), loading: ref(false), page: ref(1), pageSize: ref(20), total: ref(0), filters: reactive(filters), reload: vi.fn() }) }))

enableAutoUnmount(afterEach)
const global = { plugins: [i18n] }

const ENTITIES: Record<string, Array<{ id: number; name: string; user?: number }>> = {
  '/lookup/groups': [{ id: 7, name: 'Team' }],
  '/lookup/accounts': [{ id: 25, name: 'cc-main' }],
  '/lookup/api-keys': [{ id: 104, name: 'laptop', user: 7 }]
}
/** The lookup as the core answers `ids`: the entities it knows, an API key only under its user. */
function lookup(path: string, query: Record<string, unknown> = {}) {
  const ids = String(query.ids ?? '').split(',')
  const items = (ENTITIES[path] ?? []).filter(e => ids.includes(String(e.id)) && (!query.user_id || String(e.user) === String(query.user_id)))
  return { items: items.map(({ id, name }) => ({ id, name })), more: false }
}
beforeEach(() => {
  resetEntityLookups()
  get.mockReset().mockImplementation(async (path, query) => (String(path).startsWith('/lookup/') ? lookup(String(path), query as Record<string, unknown>) : []))
})
/** The picker of an entity search field. */
const picker = (w: VueWrapper, kind: string) => w.findAllComponents(EntityPicker).find(p => p.props('kind') === kind)!
/** What the filters applied last. */
const applied = (w: VueWrapper) => w.emitted('update:modelValue')?.at(-1)?.[0] as Record<string, string>

describe('shared usage search', () => {
  it('uses the same search criteria for the totals and every chart', async () => {
    const w = mount(UsageOverviewView, { props: { view: 'all' }, global: { plugins: [i18n, createPinia()], stubs: { SChart: true } } })
    await flushPromises()
    w.getComponent(UsageFilters).vm.$emit('update:modelValue', { client_ip: '192.0.2.7', api_key_name: 'laptop', status_code: '429', stream: 'true' })
    await flushPromises()
    // default range today: hourly buckets; an operator's second breakdown is by group
    for (const group_by of ['none', 'model', 'group', 'hour']) {
      expect(get).toHaveBeenCalledWith('/usage/summary', expect.objectContaining({ client_ip: '192.0.2.7', api_key_name: 'laptop', status_code: '429', stream: 'true', group_by }))
    }
  })

  it('shows client IP statistics on their tab and filters records when an address is selected', async () => {
    route.query = { tab: 'ips' }
    const w = mount(UsageView, { global: { plugins: [i18n, createPinia()], stubs: { UsageTable: true, SChart: true } } })
    await flushPromises()
    expect(w.get('[data-testid="usage-ip-panel"]').isVisible()).toBe(true)
    w.getComponent(UsageIPPanel).vm.$emit('filter', '192.0.2.7')
    await flushPromises()
    expect(w.getComponent(UsageFilters).props('modelValue').client_ip).toBe('192.0.2.7')
    expect(get).toHaveBeenCalledWith('/usage/summary', expect.objectContaining({ client_ip: '192.0.2.7', group_by: 'ip' }))
  })

  /** The More filters panel of a wide screen, and its toggle. */
  const advanced = (w: VueWrapper) => w.get('[data-testid="filter-advanced"]')
  const toggleAdvanced = (w: VueWrapper) => w.get('[data-testid="filter-advanced-toggle"]').trigger('click')
  const inAdvanced = (w: VueWrapper, field: string) => w.get(`[data-testid="filter-${field}"]`).element.closest('[data-testid="filter-advanced"]') !== null

  it('picks the group by id: among a user\'s own on their first row, among all behind an operator\'s More filters', async () => {
    for (const admin of [false, true]) {
      const w = mount(UsageFilters, { props: { modelValue: { group_id: '7' }, view: admin ? 'all' : 'self' }, global })
      await flushPromises()
      expect(inAdvanced(w, 'group')).toBe(admin)
      // no name search: the group is picked, group_name only comes with a link
      expect(picker(w, 'group').props()).toMatchObject({ id: '7', text: undefined, mine: !admin })
      expect(get).toHaveBeenCalledWith('/lookup/groups', expect.objectContaining({ ids: '7', mine: admin ? undefined : true }))
      expect((w.get('[data-testid="filter-group"] input').element as HTMLInputElement).value).toBe('Team')
      picker(w, 'group').vm.$emit('change', { id: '3', text: '' })
      await flushPromises()
      expect(applied(w)).toMatchObject({ group_id: '3' })
    }
  })

  it('binds each picker to its id and name-search parameters, and narrows it by the draft', async () => {
    const w = mount(UsageFilters, {
      props: { modelValue: { model: 'gpt-4o', user_email: 'ann', api_key_id: '104', account_name: 'main' }, modelOptions: ['gpt-4o', 'o3'], view: 'all', accounts: true },
      global
    })
    expect(picker(w, 'model').props()).toMatchObject({ id: 'gpt-4o', text: '', extra: [{ id: 'gpt-4o', name: 'gpt-4o' }, { id: 'o3', name: 'o3' }] })
    expect(picker(w, 'user').props()).toMatchObject({ id: '', text: 'ann', mine: false })
    expect(picker(w, 'api_key').props()).toMatchObject({ id: '104', text: '', scope: { user_id: '' } })
    expect(picker(w, 'account').props()).toMatchObject({ id: '', text: 'main', scope: { prefer_group_id: '' } })
    // a picker that is set says so, for the phone's first row to give it the room
    expect(['model', 'user', 'api_key', 'group'].map(field => w.get(`[data-testid="filter-${field}"]`).attributes('data-set'))).toEqual(['true', 'true', 'true', undefined])
    // typed text kept as a name search clears the id, and the other way round
    picker(w, 'model').vm.$emit('change', { id: '', text: 'opus' })
    await flushPromises()
    expect(applied(w)).toMatchObject({ model: '', model_contains: 'opus' })
    picker(w, 'group').vm.$emit('change', { id: '7', text: '' })
    picker(w, 'user').vm.$emit('change', { id: '7', text: '' })
    await flushPromises()
    expect(applied(w)).toMatchObject({ user_id: '7', user_email: '', group_id: '7' })
    // the keys are the picked user's, the picked group's accounts come first
    expect(picker(w, 'api_key').props('scope')).toEqual({ user_id: '7' })
    expect(picker(w, 'account').props('scope')).toEqual({ prefer_group_id: '7' })
  })

  it('a new user keeps the picked API key only when it is theirs', async () => {
    const w = mount(UsageFilters, { props: { modelValue: { api_key_id: '104' }, view: 'all' }, global })
    picker(w, 'user').vm.$emit('change', { id: '7', text: '' })
    await flushPromises()
    expect(get).toHaveBeenCalledWith('/lookup/api-keys', expect.objectContaining({ ids: '104', user_id: '7' }))
    expect(w.emitted('update:modelValue')).toHaveLength(1)
    expect(applied(w)).toMatchObject({ user_id: '7', api_key_id: '104' })
    picker(w, 'user').vm.$emit('change', { id: '12', text: '' })
    await flushPromises()
    // one search, once the key is known not to be theirs
    expect(w.emitted('update:modelValue')).toHaveLength(2)
    expect(applied(w)).toMatchObject({ user_id: '12', api_key_id: '', api_key_name: '' })
    // clearing the user leaves the key alone
    await w.setProps({ modelValue: { user_id: '7', api_key_id: '104' } })
    picker(w, 'user').vm.$emit('change', { id: '', text: '' })
    await flushPromises()
    expect(applied(w)).toMatchObject({ user_id: '', api_key_id: '104' })
  })

  it('leaves the search to the latest pick while an earlier user\'s API key is still looked up', async () => {
    const answers: Array<(theirs: boolean) => void> = []
    get.mockImplementation(async (path, query) => {
      const asked = query as Record<string, unknown>
      if (path !== '/lookup/api-keys' || !asked.user_id) return lookup(String(path), asked)
      return new Promise(resolve => answers.push(theirs => resolve({ items: theirs ? [{ id: Number(asked.ids), name: 'laptop' }] : [], more: false })))
    })
    const w = mount(UsageFilters, { props: { modelValue: { api_key_id: '104' }, view: 'all' }, global })
    picker(w, 'user').vm.$emit('change', { id: '12', text: '' })
    picker(w, 'user').vm.$emit('change', { id: '7', text: '' })
    await flushPromises()
    // the key is not the first user's, who is no longer the pick: nothing is cleared, nothing searched yet
    answers[0](false)
    await flushPromises()
    expect(w.emitted('update:modelValue')).toBeUndefined()
    answers[1](true)
    await flushPromises()
    expect(w.emitted('update:modelValue')).toHaveLength(1)
    expect(applied(w)).toMatchObject({ user_id: '7', api_key_id: '104' })
    // a key picked while the next user's is looked up applies at once and is not the one the late answer speaks of
    picker(w, 'user').vm.$emit('change', { id: '12', text: '' })
    picker(w, 'api_key').vm.$emit('change', { id: '105', text: '' })
    await flushPromises()
    expect(applied(w)).toMatchObject({ user_id: '12', api_key_id: '105' })
    answers[2](false)
    await flushPromises()
    expect(w.emitted('update:modelValue')).toHaveLength(2)
  })

  it('applies typed text on Search only and a pick at once with what is typed, and resets without changing time bounds', async () => {
    const w = mount(UsageFilters, {
      props: { modelValue: { from: '2026-10-01T00:00:00Z', client_request_id: 'request-one' }, view: 'all', logType: 'errors' },
      global,
      attachTo: document.body
    })
    expect(advanced(w).isVisible()).toBe(false)
    await toggleAdvanced(w)
    expect(advanced(w).isVisible()).toBe(true)
    await w.get('[data-testid="filter-client_ip"]').setValue('192.0.2.7')
    await w.get('[data-testid="filter-status_code"]').setValue('429')
    expect(w.emitted('update:modelValue')).toBeUndefined()
    picker(w, 'api_key').vm.$emit('change', { id: '7', text: '' })
    await flushPromises()
    // the pick sets the id and clears the name; what is typed goes along, and a parameter without a field is kept
    expect(applied(w)).toMatchObject({ client_ip: '192.0.2.7', api_key_id: '7', api_key_name: '', status_code: '429', client_request_id: 'request-one' })
    expect(applied(w)).not.toHaveProperty('from')
    await w.get('[data-testid="filter-error_type"]').setValue('rate_limit')
    expect(w.emitted('update:modelValue')).toHaveLength(1)
    // a select is a pick too
    await w.get('[data-testid="filter-billing_status"]').setValue('1')
    expect(applied(w)).toMatchObject({ billing_status: 'billed', error_type: 'rate_limit', api_key_id: '7' })
    await w.get('[data-testid="filter-request_id"]').setValue('req-1')
    expect(w.emitted('update:modelValue')).toHaveLength(2)
    await w.get('form').trigger('submit')
    expect(applied(w)).toMatchObject({ request_id: 'req-1', billing_status: 'billed' })
    await w.get('[data-testid="reset-usage-filters"]').trigger('click')
    expect(Object.values(applied(w)).every(value => value === '')).toBe(true)
    expect(applied(w)).toHaveProperty('model')
    expect(w.emitted('reset')).toHaveLength(1)
    document.body.innerHTML = ''
  })

  it('offers the account filter only to an operator who may read accounts', async () => {
    const w = mount(UsageFilters, { props: { modelValue: {}, view: 'all' }, global })
    expect(inAdvanced(w, 'user')).toBe(false)
    expect(w.find('[data-testid="filter-account"]').exists()).toBe(false)
    await w.get('form').trigger('submit')
    expect(applied(w)).not.toHaveProperty('account_id')
    expect(applied(w)).not.toHaveProperty('account_name')
    await w.setProps({ accounts: true })
    expect(inAdvanced(w, 'account')).toBe(true)
    picker(w, 'account').vm.$emit('change', { id: '', text: 'Main' })
    await flushPromises()
    expect(applied(w)).toMatchObject({ account_id: '', account_name: 'Main' })
  })

  it('names the entities picked behind More filters on their chips', async () => {
    const w = mount(UsageFilters, { props: { modelValue: { group_id: '7', account_id: '25', user_id: '3', group_name: 'vip' }, view: 'all', accounts: true }, global })
    await flushPromises()
    expect(w.get('[data-testid="filter-advanced-count"]').text()).toBe('2')
    expect(w.get('[data-filter-key="group_id"]').text()).toContain('Team #7')
    expect(w.get('[data-filter-key="account_id"]').text()).toContain('cc-main #25')
    expect(w.get('[data-filter-key="group_name"]').text()).toContain('vip')
    // the first row shows its own
    expect(w.find('[data-filter-key="user_id"]').exists()).toBe(false)
    await w.get('[data-filter-key="account_id"] button').trigger('click')
    expect(applied(w)).toEqual({ account_id: '' })
  })

  it('filters operators by protocol conversion (CONVERT-MATRIX §6.2)', async () => {
    const locale = i18n.global.locale.value
    i18n.global.locale.value = 'zh'
    const w = mount(UsageFilters, { props: { modelValue: { conversion: 'lossy' }, view: 'all' }, global })
    expect(inAdvanced(w, 'conversion')).toBe(true)
    const select = w.get('[data-testid="filter-conversion"]')
    const options = select.findAll('option')
    expect(options.map(option => option.text())).toEqual(['协议转换', '原生', '已转换', '良好', '一般', '不推荐', '本地估算', '有丢失'])
    expect(options[(select.element as HTMLSelectElement).selectedIndex].text()).toBe('有丢失')
    // set behind the folded More filters: counted on the toggle, listed as a chip
    expect(w.get('[data-testid="filter-advanced-count"]').text()).toBe('1')
    expect(w.get('[data-filter-key="conversion"]').text()).toContain('协议转换：有丢失')
    await select.setValue('4')
    expect(applied(w)).toMatchObject({ conversion: 'fair' })
    const self = mount(UsageFilters, { props: { view: 'self', modelValue: {} }, global })
    expect(self.find('[data-testid="filter-conversion"]').exists()).toBe(false)
    i18n.global.locale.value = locale
  })

  it('filters operators and users alike by whether the request was retried (CONTRACTS §98)', async () => {
    const locale = i18n.global.locale.value
    i18n.global.locale.value = 'zh'
    for (const admin of [true, false]) {
      const w = mount(UsageFilters, { props: { modelValue: { retried: 'true' }, view: admin ? 'all' : 'self' }, global })
      expect(inAdvanced(w, 'retried')).toBe(true)
      const select = w.get('[data-testid="filter-retried"]')
      expect(select.findAll('option').map(option => option.text())).toEqual(['重试', '重试过', '未重试'])
      expect(w.get('[data-testid="filter-advanced-count"]').text()).toBe('1')
      expect(w.get('[data-filter-key="retried"]').text()).toContain('重试：重试过')
      await select.setValue('2')
      expect(applied(w)).toMatchObject({ retried: 'false' })
      await w.get('[data-filter-key="retried"] button').trigger('click')
      expect(applied(w)).toEqual({ retried: '' })
    }
    i18n.global.locale.value = locale
  })

  it('offers the unreported executions (usage_unreported) as an error type and names them on the chip (design §8.5)', async () => {
    const locale = i18n.global.locale.value
    i18n.global.locale.value = 'zh'
    for (const view of ['all', 'self', 'account'] as const) {
      const w = mount(UsageFilters, { props: { modelValue: { error_type: 'usage_unreported' }, view, logType: 'errors' }, global })
      const field = w.get('[data-testid="filter-error_type"]')
      const list = w.get('[data-testid="filter-error_type-suggestions"]')
      expect(field.attributes('list')).toBe(list.attributes('id'))
      expect(list.find('option[value="usage_unreported"]').attributes('label')).toBe('未上报用量（未计费）')
      expect(w.get('[data-filter-key="error_type"]').text()).toContain('错误类型：未上报用量（未计费）')
      await w.get('form').trigger('submit')
      expect(applied(w)).toMatchObject({ error_type: 'usage_unreported' })
    }
    // a type the console does not word shows as typed
    const w = mount(UsageFilters, { props: { modelValue: { error_type: 'rate_limit' }, view: 'all', logType: 'errors' }, global })
    expect(w.get('[data-filter-key="error_type"]').text()).toContain('错误类型：rate_limit')
    i18n.global.locale.value = locale
  })

  it('selects the request type behind More filters from the types under the search, keeping a preset one', async () => {
    const w = mount(UsageFilters, { props: { view: 'self', modelValue: { protocol: 'vid.gen' }, protocolOptions: ['anthropic.messages', 'openai.chat'] }, global })
    expect(inAdvanced(w, 'protocol')).toBe(true)
    const select = w.get('[data-testid="filter-protocol"]')
    const options = select.findAll('option')
    expect(options.map(option => option.text()).slice(1)).toEqual(['anthropic.messages', 'openai.chat', 'vid.gen'])
    expect(options[0].text()).not.toContain('usage.search.')
    expect(options[(select.element as HTMLSelectElement).selectedIndex].text()).toBe('vid.gen')
    await select.setValue('2')
    expect(applied(w)).toMatchObject({ protocol: 'openai.chat' })
  })

  it('keeps administrative fields out of self-service and synchronizes external request-id filters', async () => {
    const w = mount(UsageFilters, { props: { view: 'self', modelValue: {} }, global })
    for (const field of ['user', 'account', 'node_id', 'conversion']) expect(w.find(`[data-testid="filter-${field}"]`).exists()).toBe(false)
    expect(inAdvanced(w, 'request_id')).toBe(true)
    expect(w.find('[data-testid="filter-chips"]').exists()).toBe(false)
    // a request id set from outside fills its field; a client request id, which has none, shows as a chip
    await w.setProps({ modelValue: { request_id: 'req-9', client_request_id: 'selected-request' } })
    expect((w.get('[data-testid="filter-request_id"]').element as HTMLInputElement).value).toBe('req-9')
    expect(w.get('[data-filter-key="client_request_id"]').text()).toContain('selected-request')
    await w.get('form').trigger('submit')
    const applied = w.emitted('update:modelValue')![0]![0]
    expect(applied).toMatchObject({ request_id: 'req-9', client_request_id: 'selected-request' })
    expect(applied).not.toHaveProperty('user_id')
    await w.get('[data-filter-key="client_request_id"] button').trigger('click')
    expect(w.emitted('update:modelValue')![1]![0]).toEqual({ client_request_id: '' })
    expect(w.text()).not.toContain('usage.search.')
  })

  it('drops the account type with its plugin chip, and Clear all clears the chips only', async () => {
    const w = mount(UsageFilters, { props: { modelValue: { model_contains: 'gpt', plugin_key: 'p', account_type: 'apikey' }, view: 'all' }, global })
    // the API refuses account_type without plugin_key
    await w.get('[data-filter-key="plugin_key"] button').trigger('click')
    expect(w.emitted('update:modelValue')![0]![0]).toEqual({ plugin_key: '', account_type: '' })
    await w.get('[data-testid="filter-chips-clear"]').trigger('click')
    const cleared = w.emitted('update:modelValue')![1]![0] as Record<string, string>
    expect(cleared).toMatchObject({ plugin_key: '', account_type: '' })
    expect(cleared).not.toHaveProperty('model_contains')
  })

  it('ends the filter row with reset and search when there is no stats or actions row', () => {
    const bare = mount(UsageFilters, { props: { view: 'self', modelValue: {} }, global })
    expect(bare.find('.mt-3').exists()).toBe(false)
    expect(bare.get('[data-testid="filter-success"]').element.closest('form > div')).toBe(bare.get('[data-testid="search-usage"]').element.closest('form > div'))
    const full = mount(UsageFilters, { props: { view: 'self', modelValue: {} }, slots: { stats: '<span>stats</span>' }, global })
    expect(full.get('.mt-3').find('[data-testid="search-usage"]').exists()).toBe(true)
    expect(full.findAll('[data-testid="search-usage"]')).toHaveLength(1)
  })

  it('puts the tools after the actions, before Reset / Search, on a wide screen', () => {
    const w = mount(UsageFilters, { props: { view: 'self', modelValue: {} }, slots: { actions: '<button type="button" class="act" />', tools: '<button type="button" class="tool" />' }, global })
    expect(w.get('.mt-3').findAll('button').map(b => b.classes('act') ? 'act' : b.classes('tool') ? 'tool' : b.attributes('data-testid'))).toEqual(['act', 'tool', 'reset-usage-filters', 'search-usage'])
  })

  it('keeps the range, the log type and the first row in sight on a phone, folds More filters into the Filters sheet and applies it on Done', async () => {
    const logTypes = [{ value: 'records', label: 'Records' }, { value: 'errors', label: 'Errors' }]
    const w = mountPinned(UsageFilters, {
      props: { view: 'self', modelValue: { protocol: 'openai.chat', stream: 'true', success: 'false' }, logTypes, logType: 'errors' },
      slots: { range: '<button class="range">Today</button>', stats: '<span>stats</span>', tools: '<button type="button" class="tool">Export</button>' },
      global
    })
    expect(w.find('.tool').exists()).toBe(false)
    for (const key of ['log-type', 'model', 'group', 'api_key']) expect(w.find(`[data-testid="filter-${key}"]`).exists()).toBe(true)
    expect(w.find('.range').exists()).toBe(true)
    for (const key of ['protocol', 'client_ip']) expect(w.find(`[data-testid="filter-${key}"]`).exists()).toBe(false)
    expect(w.find('[data-testid="filter-advanced-toggle"]').exists()).toBe(false)
    // only the request type is a sheet filter: the stream has no field (a chip) and failed requests set the status themselves
    expect(w.get('[data-testid="filter-sheet-open"]').text()).toContain('1')
    expect(w.find('[data-filter-key="stream"]').exists()).toBe(true)
    expect(w.find('[data-filter-key="success"]').exists()).toBe(false)
    await w.get('[data-testid="filter-sheet-open"]').trigger('click')
    await flushPromises()
    const sheet = document.querySelector('[data-testid="filter-sheet"]')!
    expect(sheet.querySelector('[data-testid="filter-protocol"]')).not.toBeNull()
    expect(sheet.querySelector('[data-testid="filter-status_code"]')).not.toBeNull()
    expect(document.querySelector('[role="dialog"] .tool')).not.toBeNull()
    const requestId = sheet.querySelector<HTMLInputElement>('input[data-testid="filter-request_id"]')!
    requestId.value = 'req-1'
    requestId.dispatchEvent(new Event('input'))
    // in the sheet a pick waits for Done like the typed text
    const billing = sheet.querySelector<HTMLSelectElement>('select[data-testid="filter-billing_status"]')!
    billing.value = '2'
    billing.dispatchEvent(new Event('change'))
    await flushPromises()
    expect(w.emitted('update:modelValue')).toBeUndefined()
    document.querySelector<HTMLButtonElement>('[data-testid="filter-sheet-done"]')!.click()
    await flushPromises()
    expect(applied(w)).toMatchObject({ request_id: 'req-1', billing_status: 'free', protocol: 'openai.chat', stream: 'true' })
    // the first row stays out of the sheet: its picks apply at once
    picker(w, 'model').vm.$emit('change', { id: 'gpt-4o', text: '' })
    await flushPromises()
    expect(w.emitted('update:modelValue')).toHaveLength(2)
    expect(applied(w)).toMatchObject({ model: 'gpt-4o', request_id: 'req-1' })
    expect(document.querySelector('[data-testid="filter-sheet"]')).toBeNull()
    document.body.innerHTML = ''
  })
})
