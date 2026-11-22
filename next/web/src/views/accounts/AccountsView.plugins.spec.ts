import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from './testing/accountsView'
import { list, mocks } from './testing/accountsState'
import { flushPromises } from '@vue/test-utils'
import type { Account } from '@/api/types'
import { i18n } from '@/i18n'
import { SButton, SDropdown, STabs } from '@sub2api/ui'
import PluginSlot from '@/components/plugin/PluginSlot.vue'
import type { PluginAccountActionEntry, SlotEntry } from '@/stores/plugins'

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
    const w = render({ AccountDetailModal: false, SModal: { template: '<div><slot /></div>' }, STabs: { props: ['tabs', 'modelValue'], emits: ['update:modelValue'], template: '<div />' } })
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

  /** The list with plugin a's ids [12, 21] and plugin b's [21, 7] set from the toolbar. */
  async function twoSets() {
    list.items.value = []
    const w = render(stubs)
    slotProps(w, 'a').setIds([12, 21], 'A')
    slotProps(w, 'b').setIds([21, 7], 'B')
    await flushPromises()
    return w
  }

  it('mounts the toolbar slot among the primary filters and filters the list to the ids common to every plugin, one chip each', async () => {
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
  })

  it('closes the set of one plugin with its chip, and asks for id 0 when the sets have no id in common', async () => {
    const w = await twoSets()
    await w.findAll('[data-testid="chip-close"]')[0].trigger('click')
    expect(list.filters.ids).toBe('21,7')
    expect(chips(w)).toEqual(['B'])
    expect(slotProps(w, 'a').ids).toBeNull()
    // Sets without a common id ask for id 0, which no account has.
    slotProps(w, 'a').setIds([12], 'A')
    await flushPromises()
    expect(list.filters.ids).toBe('0')
    expect(chips(w)).toEqual(['B', 'A'])
  })

  it('clears the set of every plugin with the filters', async () => {
    const w = await twoSets()
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
