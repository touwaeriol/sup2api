import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount, type DOMWrapper, type VueWrapper } from '@vue/test-utils'
import { rangeBounds } from '@sub2api/ui'
import { mountPinned } from '@sub2api/ui/testing'
import type { ApiKey } from '@/api/types'
import { i18n } from '@/i18n'
import ProblemIcon from '@/components/ProblemIcon.vue'
import { startOfToday } from '@/utils/format'
import MyApiKeysView from './MyApiKeysView.vue'
import KeyEditor from './KeyEditor.vue'
import KeyGroupSwitch from './KeyGroupSwitch.vue'

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  list: vi.fn(),
  post: vi.fn(),
  patch: vi.fn(),
  del: vi.fn(),
  confirm: vi.fn(),
  toast: vi.fn(),
  notifyError: vi.fn(),
  copyText: vi.fn(),
  permissions: new Set<string>()
}))
vi.mock('@sub2api/host', async original => ({
  ...(await original<typeof import('@sub2api/host')>()),
  api: { get: mocks.get, list: mocks.list, post: mocks.post, patch: mocks.patch, del: mocks.del }
}))
vi.mock('@sub2api/ui', async original => ({
  ...(await original<typeof import('@sub2api/ui')>()),
  confirm: mocks.confirm,
  toast: mocks.toast
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: (p: string) => mocks.permissions.has(p) }) }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ theme: 'light' }) }))
vi.mock('@/composables/lookups', () => ({ fetchMyGroups: async () => GROUPS }))
vi.mock('@/composables/platforms', () => ({
  usePlatforms: () => ({ platforms: { value: [] }, load: vi.fn(), label: (value: string) => value })
}))
vi.mock('@/utils/errors', () => ({ fieldErrors: () => ({}), errorMessage: (e: Error) => e.message, notifyError: mocks.notifyError }))
vi.mock('@/utils/format', async original => ({ ...(await original<typeof import('@/utils/format')>()), copyText: mocks.copyText }))
enableAutoUnmount(afterEach)

const GROUPS = [
  { id: 1, name: 'default', rate_multiplier: '1', platforms: ['anthropic'], model_allowlist: [] },
  { id: 2, name: 'vip', rate_multiplier: '0.8', effective_rate: '0.72', exclusive: true, platforms: ['anthropic', 'openai'], served_protocols: [{ protocol: 'anthropic.messages' }, { protocol: 'openai.chat' }], model_allowlist: ['claude-sonnet-4-5'] }
]
const key = (k: Partial<ApiKey> = {}): ApiKey => ({
  id: 7,
  name: 'laptop',
  group_id: 1,
  group_name: 'default',
  group_status: 'active',
  platforms: ['anthropic'],
  key_prefix: 'sk-s2a-lap07',
  copyable: true,
  status: 'active',
  created_at: '2026-10-01T00:00:00Z',
  last_used_at: null,
  expires_at: null,
  allowed_ips: ['192.0.2.0/24'],
  quota_limit: '10',
  quota_used: '4',
  rpm_limit: 60,
  live: { rpm: 12, tpm: 300, concurrency: 1 },
  ...k
})
const runner = () =>
  key({
    id: 8,
    name: 'ci-runner',
    key_prefix: 'sk-s2a-run08',
    group_id: 2,
    group_name: 'vip',
    platforms: ['anthropic', 'openai'],
    allowed_ips: []
  })
const legacy = () => key({ id: 9, name: 'old-script', key_prefix: 'sk-s2a-old09', copyable: false })

/** The keys the server holds; `matching` narrows what a filtered request gets. */
let stored: ApiKey[] = []
/** Changes a stored key and answers with it, as PATCH does. */
function patched(path: string, body: Partial<ApiKey>) {
  const k = stored.find(x => path.endsWith(`/${x.id}`))!
  return { ...Object.assign(k, body) }
}
let matching: (k: ApiKey, query: Record<string, unknown>) => boolean = () => true
type ListQuery = Record<string, unknown> & { page: number; page_size: number }
const listQueries = () => mocks.list.mock.calls.map(([, query]) => query as ListQuery)
const lastListQuery = () => listQueries().at(-1)!

// The quick-setup menu and guide have their own specs; here they show what they were given.
const stubs = {
  Teleport: true,
  SChart: true,
  RouterLink: true,
  KeyUseMenu: {
    props: ['apiKey', 'platforms'],
    emits: ['guide'],
    template: '<button data-testid="key-quick-setup" :data-platforms="(platforms || []).join()" @click="$emit(\'guide\')" />'
  },
  KeyUseGuide: {
    props: ['apiKey', 'platforms', 'served'],
    template: '<i v-if="apiKey" data-testid="key-use-guide" :data-key="apiKey.id" :data-platforms="(platforms || []).join()" :data-served="(served || []).map(s => s.protocol).join()" />'
  }
}
const options = { global: { plugins: [i18n], stubs } }
async function render() {
  const w = mount(MyApiKeysView, options)
  await flushPromises()
  return w
}
async function renderPhone() {
  const w = mountPinned(MyApiKeysView, options)
  await flushPromises()
  return w
}

const tid = (id: string) => `[data-testid="${id}"]`
const rows = (w: VueWrapper) => w.findAll('tbody tr')
const rowOf = (w: VueWrapper, name: string) => rows(w).find(r => r.text().includes(name))!
const badge = (row: DOMWrapper<Element>) => row.get(tid('status-badge')).text()
const tick = (row: Pick<DOMWrapper<Element>, 'get'>) => row.get('input[type="checkbox"]').setValue(true)
/** Picks the option reading `label` of a filter's native select. */
async function choose(w: VueWrapper, filter: string, label: string) {
  const select = w.get(tid(filter))
  await select.setValue(
    select
      .findAll('option')
      .find(o => o.text() === label)!
      .attributes('value')
  )
}
/** Waits out the debounce of a changed filter. */
const reloaded = (query: Record<string, unknown>) => vi.waitFor(() => expect(lastListQuery()).toMatchObject(query))
async function click(w: VueWrapper | DOMWrapper<Element>, id: string) {
  await w.get(tid(id)).trigger('click')
  await flushPromises()
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  i18n.global.locale.value = 'zh'
  mocks.permissions = new Set(['apikey:self:manage', 'usage:self:read'])
  stored = [key(), runner(), legacy()]
  matching = () => true
  mocks.list.mockImplementation(async (_path: string, query: ListQuery) => {
    const found = stored.filter(k => matching(k, query))
    const start = (query.page - 1) * query.page_size
    return {
      // As from a server: every answer is a new object, never the row answered before.
      items: found.slice(start, start + query.page_size).map(k => ({ ...k })),
      page: { page: query.page, page_size: query.page_size, total: found.length },
      paged: true
    }
  })
  // Today's and the last 30 days' usage per key (group_by=api_key).
  mocks.get.mockImplementation(async (_path: string, q: { from: string }) => [
    {
      key: '7',
      requests: q.from === startOfToday().toISOString() ? 3 : 12,
      total_cost: q.from === startOfToday().toISOString() ? '0.25' : '1.5'
    }
  ])
  mocks.post.mockImplementation(async (path: string) => ({ key: `secret-of-${path.split('/')[3]}` }))
  mocks.copyText.mockResolvedValue(true)
  mocks.confirm.mockResolvedValue(true)
})
afterEach(() => (document.body.innerHTML = ''))

describe('API key list', () => {
  it('shows each key in the columns of the design, the limits and creation time hidden until asked for', async () => {
    const w = await render()
    expect(mocks.list).toHaveBeenCalledWith('/me/api-keys', expect.objectContaining({ page: 1, q: '', status: '', group_id: '' }))
    expect(
      w
        .findAll('thead th')
        .map(th => th.text())
        .filter(Boolean)
    ).toEqual(['名称', '状态', '额度', '账号分组', '密钥', '可用模型', 'IP 限制', '用量', '最近使用', '过期时间', '操作'])
    const row = rowOf(w, 'laptop')
    expect(badge(row)).toBe('已启用')
    expect(row.get(tid('key-quota')).text().replace(/\s+/g, '')).toBe('$6.00/$10.00')
    expect(row.get(tid('group-badge')).text()).toBe('default×1')
    expect(row.get(tid('api-key')).text()).toBe('sk-s2a-lap07******')
    expect(row.get(tid('key-ip-restricted')).text()).toBe('1 条')
    expect(row.text()).toContain('从未')
    expect(row.text()).toContain('永不')
    expect(rowOf(w, 'ci-runner').text()).toContain('1 项')
    expect(w.get(tid('api-base')).text()).toContain(window.location.origin)
  })

  it('badges a key by what stops it: switched off, expired, quota used up', async () => {
    stored = [
      key({ id: 1, name: 'off', status: 'disabled', expires_at: '2020-01-01T00:00:00Z' }),
      key({ id: 2, name: 'late', expires_at: '2020-01-01T00:00:00Z', quota_used: '10' }),
      key({ id: 3, name: 'spent', quota_used: '10' })
    ]
    const w = await render()
    expect(rows(w).map(badge)).toEqual(['已禁用', '已过期', '额度用尽'])
    expect(rowOf(w, 'late').find('.text-red-600').exists()).toBe(true)
  })

  it('shows a key whose group was deleted with a problem icon in place of the group, and no platform to set up', async () => {
    stored = [key({ group_id: null, group_name: '', group_status: 'deleted', platforms: [] })]
    const w = await render()
    const row = rowOf(w, 'laptop')
    expect(row.find(tid('group-badge')).exists()).toBe(false)
    expect(row.getComponent(ProblemIcon).props()).toMatchObject({ text: '账号分组已删除，请重新选择账号分组', tone: 'danger' })
    expect(row.get(tid('key-quick-setup')).attributes('data-platforms')).toBe('')
    // The owner can still pick another group from the cell.
    expect(row.get(tid('key-group-switch')).attributes('disabled')).toBeUndefined()
  })

  it('shows the usage of each key today and in the last 30 days, and none of it without the permission', async () => {
    const w = await render()
    expect(mocks.get).toHaveBeenCalledWith(
      '/me/usage/summary',
      expect.objectContaining({ group_by: 'api_key', from: rangeBounds('30d').from })
    )
    const usage = rowOf(w, 'laptop').get(tid('key-usage')).text()
    for (const figure of ['$0.25', '3 次', '$1.50', '12 次']) expect(usage).toContain(figure)

    mocks.permissions.delete('usage:self:read')
    mocks.get.mockClear()
    const limited = await render()
    expect(limited.find(tid('key-usage')).exists()).toBe(false)
    expect(limited.findAll('thead th').map(th => th.text())).not.toContain('用量')
    expect(mocks.get).not.toHaveBeenCalled()
  })

  it('opens the usage of a key in a drawer, 7 days first, then 30', async () => {
    const w = await render()
    expect(w.find(tid('key-usage-drawer')).exists()).toBe(false)
    await click(rowOf(w, 'laptop'), 'open-key-usage')
    expect(w.find(tid('key-usage-drawer')).exists()).toBe(true)
    const seven = new Date()
    seven.setHours(0, 0, 0, 0)
    seven.setDate(seven.getDate() - 6)
    for (const g of ['none', 'day', 'model'])
      expect(mocks.get).toHaveBeenCalledWith(
        '/me/usage/summary',
        expect.objectContaining({ api_key_id: '7', group_by: g, from: seven.toISOString() })
      )
    mocks.get.mockClear()
    await w.get(tid('key-usage-days')).findAll('input')[1].setValue(true)
    await flushPromises()
    expect(mocks.get).toHaveBeenCalledWith('/me/usage/summary', expect.objectContaining({ api_key_id: '7', group_by: 'day' }))
    expect(new Date(mocks.get.mock.calls[0][1].from).getTime()).toBeLessThan(seven.getTime())
  })
})

describe('API key list filters', () => {
  it('searches by what is typed, from the first page', async () => {
    const w = await render()
    await w.get(tid('key-search')).setValue(' lap ')
    await reloaded({ q: 'lap', page: 1 })
  })

  it('searches a pasted key by its prefix only, the box keeping what was pasted', async () => {
    const w = await render()
    const pasted = `sk-s2a-lap07${'x'.repeat(35)}`
    await w.get(tid('key-search')).setValue(pasted)
    await reloaded({ q: 'sk-s2a-lap07' })
    expect((w.get(tid('key-search')).element as HTMLInputElement).value).toBe(pasted)
    expect(JSON.stringify(mocks.list.mock.calls)).not.toContain('xxx')
  })

  it('filters by status and by group', async () => {
    const w = await render()
    expect(
      w
        .get(tid('key-status-filter'))
        .findAll('option')
        .map(o => o.text())
    ).toEqual(['全部状态', '已启用', '已禁用', '已过期'])
    await choose(w, 'key-status-filter', '已过期')
    await reloaded({ status: 'expired', group_id: '' })
    expect(
      w
        .get(tid('key-group-filter'))
        .findAll('option')
        .map(o => o.text())
    ).toEqual(['全部账号分组', 'default', 'vip'])
    await choose(w, 'key-group-filter', 'vip')
    await reloaded({ status: 'expired', group_id: 2 })
    await choose(w, 'key-status-filter', '全部状态')
    await reloaded({ status: '', group_id: 2 })
  })

  it('says that nothing matches when a search or filter comes up empty, and invites a first key only when there is none', async () => {
    matching = (_k, query) => !query.q && !query.status
    const w = await render()
    await w.get(tid('key-search')).setValue('nothing-like-it')
    await reloaded({ q: 'nothing-like-it' })
    await flushPromises()
    expect(w.get(tid('keys-empty-filtered')).text()).toContain('没有符合条件的 API Key')
    expect(w.find(tid('keys-empty')).exists()).toBe(false)

    await w.get(tid('key-search')).setValue('')
    await choose(w, 'key-status-filter', '已禁用')
    await reloaded({ q: '', status: 'disabled' })
    await flushPromises()
    expect(w.find(tid('keys-empty-filtered')).exists()).toBe(true)

    stored = []
    await choose(w, 'key-status-filter', '全部状态')
    await reloaded({ status: '' })
    await flushPromises()
    expect(w.find(tid('keys-empty-filtered')).exists()).toBe(false)
    expect(w.get(tid('keys-empty')).text()).toContain('还没有 API Key')
    await w.get(tid('keys-empty')).get('button').trigger('click')
    expect(w.getComponent(KeyEditor).props()).toMatchObject({ open: true, apiKey: null })
  })
})

describe('API key selection and bulk actions', () => {
  const bar = (w: VueWrapper) => w.find(tid('key-bulk-bar'))

  it('shows the bulk bar only while keys are ticked', async () => {
    const w = await render()
    expect(bar(w).exists()).toBe(false)
    await tick(rowOf(w, 'laptop'))
    await tick(rowOf(w, 'old-script'))
    expect(bar(w).text()).toContain('已选 2 项')
    expect(
      bar(w)
        .findAll('button')
        .map(b => b.text())
    ).toEqual(expect.arrayContaining(['复制密钥', '启用', '禁用', '删除']))
    await click(w, 'bulk-clear')
    expect(bar(w).exists()).toBe(false)
  })

  it('copies the ticked keys, skipping the legacy one', async () => {
    const w = await render()
    for (const name of ['laptop', 'old-script', 'ci-runner']) await tick(rowOf(w, name))
    await click(w, 'bulk-copy-keys')
    expect(mocks.post.mock.calls.map(([path]) => path).sort()).toEqual(['/me/api-keys/7/reveal', '/me/api-keys/8/reveal'])
    expect(mocks.copyText).toHaveBeenCalledWith('laptop\tsecret-of-7\nci-runner\tsecret-of-8')
    expect(mocks.toast).toHaveBeenCalledWith(expect.stringContaining('跳过 1 个'), 'success')
  })

  it('disables and enables the ticked keys, then reloads the list', async () => {
    mocks.patch.mockImplementation(async (path: string, body: Partial<ApiKey>) => patched(path, body))
    const w = await render()
    await tick(rowOf(w, 'laptop'))
    await tick(rowOf(w, 'ci-runner'))
    mocks.list.mockClear()
    await click(w, 'bulk-disable-keys')
    expect(mocks.patch.mock.calls).toEqual([
      ['/me/api-keys/7', { status: 'disabled' }],
      ['/me/api-keys/8', { status: 'disabled' }]
    ])
    expect(mocks.list).toHaveBeenCalledTimes(1)
    expect(rows(w).map(badge)).toEqual(['已禁用', '已禁用', '已启用'])
    await click(w, 'bulk-enable-keys')
    expect(mocks.patch).toHaveBeenLastCalledWith('/me/api-keys/8', { status: 'active' })
    expect(bar(w).text()).toContain('已选 2 项')
  })

  it('leaves alone the ticked keys that are in the state already, and says so when that is all of them', async () => {
    stored = [key(), key({ id: 8, name: 'paused', status: 'disabled' })]
    mocks.patch.mockImplementation(async (path: string, body: Partial<ApiKey>) => patched(path, body))
    const w = await render()
    await tick(rowOf(w, 'laptop'))
    await tick(rowOf(w, 'paused'))
    await click(w, 'bulk-disable-keys')
    expect(mocks.patch.mock.calls).toEqual([['/me/api-keys/7', { status: 'disabled' }]])
    mocks.patch.mockClear()
    mocks.list.mockClear()
    await click(w, 'bulk-disable-keys')
    expect(mocks.patch).not.toHaveBeenCalled()
    expect(mocks.list).not.toHaveBeenCalled()
    expect(mocks.toast).toHaveBeenLastCalledWith('所选密钥已是该状态', 'info')
    expect(bar(w).text()).toContain('已选 2 项')
  })

  it('drops the selection after switching keys on or off under a status filter: they left the list', async () => {
    matching = (k, query) => !query.status || k.status === query.status
    mocks.patch.mockImplementation(async (path: string, body: Partial<ApiKey>) => patched(path, body))
    const w = await render()
    await choose(w, 'key-status-filter', '已启用')
    await reloaded({ status: 'active' })
    await flushPromises()
    await tick(rowOf(w, 'laptop'))
    await tick(rowOf(w, 'ci-runner'))

    // Nothing to switch on among enabled keys: the selection stays for the action meant.
    await click(w, 'bulk-enable-keys')
    expect(bar(w).text()).toContain('已选 2 项')

    await click(w, 'bulk-disable-keys')
    expect(mocks.patch).toHaveBeenCalledTimes(2)
    expect(rows(w).map(r => r.text())).toEqual([expect.stringContaining('old-script')])
    expect(bar(w).exists()).toBe(false)
  })

  it('deletes the ticked keys once confirmed and takes them out of the selection', async () => {
    mocks.del.mockImplementation(async (path: string) => {
      stored = stored.filter(k => !path.endsWith(`/${k.id}`))
    })
    const w = await render()
    await tick(rowOf(w, 'laptop'))
    await tick(rowOf(w, 'ci-runner'))
    mocks.confirm.mockResolvedValueOnce(false)
    await click(w, 'bulk-delete-keys')
    expect(mocks.del).not.toHaveBeenCalled()

    await click(w, 'bulk-delete-keys')
    expect(mocks.del.mock.calls.map(([path]) => path)).toEqual(['/me/api-keys/7', '/me/api-keys/8'])
    expect(rows(w).map(r => r.text())).toEqual([expect.stringContaining('old-script')])
    expect(bar(w).exists()).toBe(false)
  })

  it('invites a first key only when the owner has none, not for an empty page of some', async () => {
    mocks.list.mockResolvedValue({ items: [], page: { page: 1, page_size: 20, total: 3 }, paged: true })
    const w = await render()
    expect(w.find(tid('keys-empty')).exists()).toBe(false)
    expect(w.find(tid('keys-empty-filtered')).exists()).toBe(false)
    expect(w.get('tbody').text()).toBe('暂无数据')
  })

  it('falls back to the last page when the page it is on was deleted whole, instead of inviting a first key', async () => {
    stored = Array.from({ length: 22 }, (_, i) => key({ id: i + 1, name: `key-${i + 1}` }))
    mocks.del.mockImplementation(async (path: string) => {
      stored = stored.filter(k => !path.endsWith(`/${k.id}`))
    })
    const w = await render()
    await w
      .get('nav')
      .findAll('button')
      .find(b => b.text() === '2')!
      .trigger('click')
    await flushPromises()
    expect(rows(w).map(r => r.text())).toEqual([expect.stringContaining('key-21'), expect.stringContaining('key-22')])
    for (const row of rows(w)) await tick(row)
    await click(w, 'bulk-delete-keys')
    expect(mocks.del).toHaveBeenCalledTimes(2)
    expect(lastListQuery().page).toBe(1)
    expect(rows(w)).toHaveLength(20)
    expect(w.find(tid('keys-empty')).exists()).toBe(false)
    expect(bar(w).exists()).toBe(false)
  })

  it('selects every result across pages with the filters in force', async () => {
    stored = Array.from({ length: 23 }, (_, i) => key({ id: i + 1, name: `key-${i + 1}` }))
    const w = await render()
    await choose(w, 'key-status-filter', '已启用')
    await w.get(tid('key-search')).setValue('key')
    await reloaded({ status: 'active', q: 'key' })
    await flushPromises()
    expect(rows(w)).toHaveLength(20)
    await tick(rows(w)[0])
    mocks.list.mockClear()
    await click(w, 'bulk-select-all')
    expect(mocks.list).toHaveBeenCalledWith('/me/api-keys', { q: 'key', status: 'active', group_id: '', page: 1, page_size: 200 })
    expect(bar(w).text()).toContain('23')
    expect(w.find(tid('bulk-select-all')).exists()).toBe(false)

    // The rows of the other page are acted on too.
    mocks.patch.mockResolvedValue({})
    await click(w, 'bulk-disable-keys')
    expect(mocks.patch).toHaveBeenCalledTimes(23)
  })

  it('starts a new selection when a filter changes: rows it hides would be acted on unseen', async () => {
    const w = await render()
    await tick(rowOf(w, 'laptop'))
    await choose(w, 'key-group-filter', 'vip')
    expect(bar(w).exists()).toBe(false)
    await reloaded({ group_id: 2 })
  })
})

describe('API key row actions', () => {
  it('disables and enables a key from its row in place', async () => {
    const w = await render()
    const row = rowOf(w, 'laptop')
    expect(row.get(tid('toggle-key')).text()).toBe('禁用')
    mocks.patch.mockResolvedValueOnce(key({ status: 'disabled' }))
    mocks.list.mockClear()
    await click(row, 'toggle-key')
    expect(mocks.patch).toHaveBeenCalledWith('/me/api-keys/7', { status: 'disabled' })
    expect(badge(row)).toBe('已禁用')
    expect(row.get(tid('toggle-key')).text()).toBe('启用')
    expect(mocks.toast).toHaveBeenCalledWith(expect.stringContaining('已停用'), 'success')

    mocks.patch.mockResolvedValueOnce(key())
    await click(row, 'toggle-key')
    expect(mocks.patch).toHaveBeenLastCalledWith('/me/api-keys/7', { status: 'active' })
    expect(badge(row)).toBe('已启用')
    expect(mocks.list).not.toHaveBeenCalled()
  })

  it('leaves the key as it was when the server refuses the change', async () => {
    const err = new Error('forbidden')
    mocks.patch.mockRejectedValueOnce(err)
    const w = await render()
    const row = rowOf(w, 'laptop')
    await click(row, 'toggle-key')
    expect(mocks.notifyError).toHaveBeenCalledWith(err)
    expect(badge(row)).toBe('已启用')
    expect(row.get(tid('toggle-key')).text()).toBe('禁用')
  })

  it('offers the row actions as words, only delete in red', async () => {
    const w = await render()
    const actions = rowOf(w, 'laptop').get('td.s-table-actions')
    expect(
      actions
        .findAll('button')
        .map(b => b.text())
        .filter(Boolean)
    ).toEqual(['禁用', '编辑', '删除'])
    expect(actions.find('[role="switch"], input[type="checkbox"]').exists()).toBe(false)
    expect(actions.get(tid('delete-key')).classes().join(' ')).toMatch(/red/)
    expect(actions.get(tid('edit-key')).classes().join(' ')).not.toMatch(/red/)
  })

  it('moves a key to another group in place', async () => {
    mocks.patch.mockResolvedValueOnce(key({ group_id: 2, group_name: 'vip' }))
    const w = await render()
    mocks.list.mockClear()
    // The menu itself is SDropdown's (it closes at once without layout here).
    const move = rowOf(w, 'laptop').getComponent(KeyGroupSwitch)
    expect(move.props('options')).toEqual([
      { value: 1, label: 'default · ×1 · anthropic' },
      { value: 2, label: 'vip · ×0.72 · 专属 · anthropic, openai' }
    ])
    move.vm.$emit('change', 2)
    await flushPromises()
    expect(mocks.patch).toHaveBeenCalledWith('/me/api-keys/7', { group_id: 2 })
    // The rate on the badge is the owner's own in the group, not its default.
    expect(rowOf(w, 'laptop').get(tid('group-badge')).text()).toBe('vip×0.72')
    expect(mocks.list).not.toHaveBeenCalled()
  })

  it('deletes a key once confirmed, out of the list and out of the selection', async () => {
    mocks.del.mockImplementation(async () => {
      stored = stored.filter(k => k.id !== 7)
    })
    const w = await render()
    await tick(rowOf(w, 'laptop'))
    mocks.confirm.mockResolvedValueOnce(false)
    await click(rowOf(w, 'laptop'), 'delete-key')
    expect(mocks.confirm).toHaveBeenCalledWith(expect.objectContaining({ message: expect.stringContaining('laptop'), danger: true }))
    expect(mocks.del).not.toHaveBeenCalled()

    await click(rowOf(w, 'laptop'), 'delete-key')
    expect(mocks.del).toHaveBeenCalledWith('/me/api-keys/7', undefined)
    expect(
      rows(w)
        .map(r => r.text())
        .join()
    ).not.toContain('laptop')
    expect(w.find(tid('key-bulk-bar')).exists()).toBe(false)
  })

  it('hands the quick setup the platforms the key reaches and opens the manual guide for it, with what its group serves', async () => {
    const w = await render()
    const setup = rowOf(w, 'ci-runner').get(tid('key-quick-setup'))
    expect(setup.attributes('data-platforms')).toBe('anthropic,openai')
    expect(w.find(tid('key-use-guide')).exists()).toBe(false)
    await setup.trigger('click')
    expect(w.get(tid('key-use-guide')).attributes()).toMatchObject({ 'data-key': '8', 'data-platforms': 'anthropic,openai', 'data-served': 'anthropic.messages,openai.chat' })
  })
})

describe('API key editor in the list', () => {
  const issued = (w: VueWrapper) => w.find(tid('copy-plaintext')).element?.closest('[role="dialog"]')?.textContent ?? ''

  it('edits a key from its row in place', async () => {
    const w = await render()
    await click(rowOf(w, 'laptop'), 'edit-key')
    const editor = w.getComponent(KeyEditor)
    expect(editor.props()).toMatchObject({ open: true, apiKey: { id: 7 } })
    mocks.list.mockClear()
    editor.vm.$emit('saved', key({ name: 'renamed' }))
    await flushPromises()
    expect(rows(w)[0].text()).toContain('renamed')
    expect(mocks.toast).toHaveBeenCalledWith('已保存', 'success')
    expect(mocks.list).not.toHaveBeenCalled()
    expect(w.find(tid('copy-plaintext')).exists()).toBe(false)
  })

  it('creates a key with the same editor and shows its plaintext once, as created', async () => {
    const w = await render()
    await click(w, 'create-key')
    const editor = w.getComponent(KeyEditor)
    expect(editor.props()).toMatchObject({ open: true, apiKey: null })
    mocks.list.mockClear()
    editor.vm.$emit('saved', { ...key({ id: 10, name: 'new' }), key: 'sk-s2a-new10-plaintext' })
    await flushPromises()
    expect(mocks.list).toHaveBeenCalledTimes(1)
    expect(issued(w)).toContain('API Key 已创建')
    expect(issued(w)).toContain('sk-s2a-new10-plaintext')
    await click(w, 'close-key-issued')
    expect(w.find(tid('copy-plaintext')).exists()).toBe(false)
  })

  it('shows a regenerated key once, as regenerated, and its new prefix in the row', async () => {
    const w = await render()
    const row = rowOf(w, 'laptop')
    await click(row, 'reveal-api-key')
    expect(row.get(tid('api-key')).text()).toBe('secret-of-7')
    await click(row, 'edit-key')
    mocks.list.mockClear()
    w.getComponent(KeyEditor).vm.$emit('rotated', { ...key({ key_prefix: 'sk-s2a-rot07' }), key: 'sk-s2a-rot07-plaintext' })
    await flushPromises()
    expect(issued(w)).toContain('API Key 已重新生成')
    expect(issued(w)).not.toContain('已创建')
    expect(issued(w)).toContain('sk-s2a-rot07-plaintext')
    // The old key is no longer on show, and the plaintext is not kept on the row.
    expect(row.get(tid('api-key')).text()).toBe('sk-s2a-rot07******')
    expect(mocks.list).not.toHaveBeenCalled()
    expect(mocks.toast).not.toHaveBeenCalled()
  })

  describe('when the list reloaded while the editor was open', () => {
    /** Opens the editor on laptop, its key on show, then has the list reload behind it: the row is a new object. */
    async function editThenReload() {
      const w = await render()
      await click(rowOf(w, 'laptop'), 'reveal-api-key')
      await click(rowOf(w, 'laptop'), 'edit-key')
      await w.get(tid('key-search')).setValue('lap')
      await reloaded({ q: 'lap' })
      await flushPromises()
      expect(rowOf(w, 'laptop').get(tid('api-key')).text()).toBe('secret-of-7')
      mocks.list.mockClear()
      return w
    }

    it('takes the old key off show once it is regenerated: the row of now gets the new prefix', async () => {
      const w = await editThenReload()
      w.getComponent(KeyEditor).vm.$emit('rotated', { ...key({ key_prefix: 'sk-s2a-rot07' }), key: 'sk-s2a-rot07-plaintext' })
      await flushPromises()
      const row = rowOf(w, 'laptop')
      expect(row.get(tid('api-key')).text()).toBe('sk-s2a-rot07******')
      // Copying asks for the key again: the old plaintext is not kept.
      mocks.post.mockClear()
      await click(row, 'copy-api-key')
      expect(mocks.post).toHaveBeenCalledWith('/me/api-keys/7/reveal', {})
      expect(mocks.list).not.toHaveBeenCalled()
    })

    it('shows what was saved in the row of now', async () => {
      const w = await editThenReload()
      w.getComponent(KeyEditor).vm.$emit('saved', key({ name: 'laptop-2', quota_limit: '50' }))
      await flushPromises()
      expect(rowOf(w, 'laptop-2').get(tid('key-quota')).text().replace(/\s+/g, '')).toBe('$46.00/$50.00')
    })

    it('shows a reset quota in the row of now and goes on editing that row', async () => {
      const w = await editThenReload()
      const editor = w.getComponent(KeyEditor)
      editor.vm.$emit('updated', key({ quota_used: '0' }))
      await flushPromises()
      expect(rowOf(w, 'laptop').get(tid('key-quota')).text().replace(/\s+/g, '')).toBe('$10.00/$10.00')
      expect(editor.props()).toMatchObject({ open: true, apiKey: { id: 7, quota_used: '0' } })
    })

    it('fetches the list again when the key is no longer on the page', async () => {
      matching = (k, query) => !query.q || k.name.includes(String(query.q))
      const w = await render()
      await click(rowOf(w, 'laptop'), 'edit-key')
      await w.get(tid('key-search')).setValue('runner')
      await reloaded({ q: 'runner' })
      await flushPromises()
      mocks.list.mockClear()
      w.getComponent(KeyEditor).vm.$emit('rotated', { ...key({ key_prefix: 'sk-s2a-rot07' }), key: 'sk-s2a-rot07-plaintext' })
      await flushPromises()
      expect(mocks.list).toHaveBeenCalledTimes(1)
      expect(issued(w)).toContain('sk-s2a-rot07-plaintext')
    })
  })
})

describe('API key list on a phone', () => {
  it('shows a key as a card: name and status on top, the key under the name, the rest as labelled fields', async () => {
    const w = await renderPhone()
    const card = w.get('ul[role="list"] > li')
    const top = card.get(`#${card.attributes('aria-labelledby')}`).element.parentElement!
    expect(top.textContent).toContain('laptop')
    expect(top.querySelector(tid('status-badge'))?.textContent).toBe('已启用')
    expect(top.nextElementSibling!.querySelector(tid('api-key'))?.textContent).toContain('sk-s2a-lap07******')
    expect(card.findAll('dt').map(dt => dt.text())).toEqual(['额度', '账号分组', '用量', '最近使用', '过期时间'])
    expect(card.findAll('button').map(b => b.text())).toEqual(expect.arrayContaining(['禁用', '编辑', '删除']))
    expect(card.find(tid('key-quick-setup')).exists()).toBe(true)
    expect(card.find('[role="switch"]').exists()).toBe(false)
  })

  it('keeps the models, IP limit, limits and creation time behind the details of a card', async () => {
    const w = await renderPhone()
    const card = w.get('ul[role="list"] > li')
    expect(card.find(tid('key-ip-restricted')).exists()).toBe(false)
    await card
      .findAll('button')
      .find(b => b.text() === '详情')!
      .trigger('click')
    expect(card.findAll('dt').map(dt => dt.text())).toEqual(expect.arrayContaining(['可用模型', 'IP 限制']))
    expect(card.get(tid('key-ip-restricted')).text()).toBe('1 条')
  })

  it('docks the bulk actions at the bottom once a card is ticked', async () => {
    const w = await renderPhone()
    expect(w.find(tid('key-bulk-bar')).exists()).toBe(false)
    await tick(w.get('ul[role="list"] > li'))
    const dock = w.get(tid('key-bulk-bar'))
    expect(dock.get(tid('bulk-menu')).text()).toContain('已选 1 项')
    expect(dock.findAll('button').map(b => b.text())).toEqual(expect.arrayContaining(['复制密钥', '启用', '禁用', '删除']))
  })
})
