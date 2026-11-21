import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import type { GroupModelPrices, ModelPriceRow, PriceDef, SegmentRef } from '@/api/pricing'
import ModelPriceTable from './ModelPriceTable.vue'
import { singleTier } from '@/views/prices/priceExpr'

const mocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), toast: vi.fn() }))
vi.mock('@sub2api/host', async (original) => ({ ...(await original<typeof import('@sub2api/host')>()), api: { get: mocks.get, put: mocks.put } }))
vi.mock('@sub2api/ui', async (original) => ({ ...(await original<object>()), toast: (...a: unknown[]) => mocks.toast(...a) }))

i18n.global.locale.value = 'en'

const price = (p: number, c: number): PriceDef => ({ mode: 'visual', config: singleTier({ p, c }), expression: '' })
const vip: SegmentRef = { id: 7, kind: 'named', name: 'VIP' }

function prices(models: ModelPriceRow[], segment?: SegmentRef): GroupModelPrices {
  return { group: { id: 3, name: 'team' }, segment, rate: { value: '1', source: 'group', group_default: '1', discount: '1' }, models }
}
const groupView = () =>
  prices([
    // A multiplier rule: the server gives the global price as it is, with the multiplier beside it.
    { model: 'gpt-4o', base: price(3, 15), group_rule: { kind: 'multiplier', multiplier: '1.5' }, segment_rule_count: 2, list_price: { ...price(3, 15), multiplier: '1.5' }, source: 'group' },
    { model: 'o3', base: price(2, 8), group_rule: null, segment_rule_count: 0, list_price: price(2, 8), source: 'global' }
  ])

async function render(props: { groupId: number | null; segment?: SegmentRef; editable?: boolean }) {
  const w = mount(ModelPriceTable, { props, global: { plugins: [i18n] } })
  await flushPromises()
  const row = (model: string) => w.get(`[data-model="${model}"]`)
  /** The one open rule editor, in the row under its model's. */
  const editor = () => w.get('[data-testid="price-rule-editor"]')
  return { w, row, editor }
}

beforeEach(() => {
  mocks.get.mockReset().mockResolvedValue(groupView())
  mocks.put.mockReset()
  mocks.toast.mockReset()
})

describe('model price table', () => {
  it('lists what each model costs in the group and where the price comes from', async () => {
    const { w, row } = await render({ groupId: 3 })
    expect(mocks.get).toHaveBeenCalledWith('/groups/3/model-prices', undefined)
    // A rule in effect is all its cell says: what the model would follow shows only without one.
    expect(row('gpt-4o').findAll('[data-testid="model-price-group-rule"]').map((c) => c.text())).toEqual(['Global ×1.5'])
    expect(row('gpt-4o').get('[data-testid="model-price-list"]').text()).toBe('$4.5 / $22.5')
    expect(row('gpt-4o').get('[data-testid="model-price-source"]').text()).toBe('Group pricing')
    expect(row('gpt-4o').get('[data-testid="model-price-segment-rules"]').text()).toBe('2 user groups have a price of their own')
    expect(row('o3').get('[data-testid="model-price-group-rule"]').text()).toBe('Follows global')
    expect(w.find('[data-testid="model-price-set"]').exists()).toBe(false)
  })

  it('keeps edits in drafts, shows the list price they give, and saves them as patches', async () => {
    const { w, row, editor } = await render({ groupId: 3, editable: true })
    await row('o3').get('[data-testid="model-price-set"]').trigger('click')
    await editor().get('input[type="radio"][value="multiplier"]').setValue(true)
    await editor().get('[data-testid="price-rule-multiplier"]').setValue('2')
    expect(row('o3').get('[data-testid="model-price-list"]').text()).toBe('$4 / $16')
    expect(row('o3').find('[data-testid="model-price-unsaved"]').exists()).toBe(true)
    await row('gpt-4o').get('[data-testid="model-price-clear"]').trigger('click')
    expect(row('gpt-4o').get('[data-testid="model-price-source"]').text()).toBe('Global')
    expect(w.vm.dirty).toBe(true)

    mocks.put.mockResolvedValue(prices([]))
    expect(await w.vm.save()).toBe(true)
    expect(mocks.put).toHaveBeenCalledWith(
      '/groups/3/model-prices',
      { items: [{ segment_id: null, model: 'o3', rule: { kind: 'multiplier', multiplier: '2' } }, { segment_id: null, model: 'gpt-4o', rule: null }] },
      { query: undefined }
    )
    expect(w.vm.dirty).toBe(false)
    expect(w.find('[data-testid="price-rule-editor"]').exists()).toBe(false)
  })

  it('shows a draft as the saved rule will read, the list price through the same path', async () => {
    const { w, row, editor } = await render({ groupId: 3, editable: true })
    await row('o3').get('[data-testid="model-price-set"]').trigger('click')
    await editor().get('input[type="radio"][value="multiplier"]').setValue(true)
    await editor().get('[data-testid="price-rule-multiplier"]').setValue('1.5')
    const drafted = row('o3').get('[data-testid="model-price-list"]').text()
    expect(drafted).toBe('$3 / $12')

    mocks.put.mockResolvedValue(prices([{ model: 'o3', base: price(2, 8), group_rule: { kind: 'multiplier', multiplier: '1.5' }, segment_rule_count: 0, list_price: { ...price(2, 8), multiplier: '1.5' }, source: 'group' }]))
    await w.vm.save()
    await flushPromises()
    expect(row('o3').find('[data-testid="model-price-unsaved"]').exists()).toBe(false)
    expect(row('o3').get('[data-testid="model-price-list"]').text()).toBe(drafted)
  })

  it('names the multiplier after a list price billed by an expression it cannot break down', async () => {
    const opaque: PriceDef = { mode: 'expression', config: null, expression: 'p * 2 + unknown(1)' }
    mocks.get.mockResolvedValue(prices([{ model: 'custom', base: opaque, group_rule: { kind: 'multiplier', multiplier: '1.5' }, segment_rule_count: 0, list_price: { ...opaque, multiplier: '1.5' }, source: 'group' }]))
    const { row } = await render({ groupId: 3 })
    expect(row('custom').get('[data-testid="model-price-base"]').text()).toBe('Billed by expression')
    expect(row('custom').get('[data-testid="model-price-list"]').text()).toBe('Billed by expression ×1.5')
  })

  it('keeps what is edited while a save is under way', async () => {
    const { w, row, editor } = await render({ groupId: 3, editable: true })
    await row('gpt-4o').get('[data-testid="model-price-clear"]').trigger('click')
    let answer!: (v: GroupModelPrices) => void
    mocks.put.mockReturnValue(new Promise<GroupModelPrices>((resolve) => (answer = resolve)))
    const saving = w.vm.save()

    await row('o3').get('[data-testid="model-price-set"]').trigger('click')
    await editor().get('input[type="radio"][value="multiplier"]').setValue(true)
    answer(prices([groupView().models[1], { ...groupView().models[0], group_rule: null, list_price: price(3, 15), source: 'global' }]))
    expect(await saving).toBe(true)
    await flushPromises()

    expect(mocks.put.mock.calls[0][1].items).toEqual([{ segment_id: null, model: 'gpt-4o', rule: null }])
    expect(row('gpt-4o').find('[data-testid="model-price-unsaved"]').exists()).toBe(false)
    expect(row('o3').find('[data-testid="model-price-unsaved"]').exists()).toBe(true)
    expect(editor().get('input[type="radio"][value="multiplier"]').element).toHaveProperty('checked', true)
    expect(w.vm.dirty).toBe(true)
  })

  it('opens one editor at a time, under its row, and sends nothing without a change', async () => {
    const { w, row } = await render({ groupId: 3, editable: true })
    const set = (model: string) => row(model).get('[data-testid="model-price-set"]')
    await set('o3').trigger('click')
    await set('gpt-4o').trigger('click')
    expect(w.findAll('[data-testid="price-rule-editor"]')).toHaveLength(1)
    expect(row('gpt-4o').element.nextElementSibling?.querySelector('[data-testid="price-rule-editor"]')).not.toBeNull()
    expect([set('gpt-4o').text(), set('o3').text()]).toEqual(['Done', 'Set'])
    await set('gpt-4o').trigger('click')
    expect(w.find('[data-testid="price-rule-editor"]').exists()).toBe(false)
    expect(await w.vm.save()).toBe(true)
    expect(mocks.put).not.toHaveBeenCalled()
  })

  it('does not save a rule that is not filled in', async () => {
    const { w, row, editor } = await render({ groupId: 3, editable: true })
    await row('o3').get('[data-testid="model-price-set"]').trigger('click')
    await editor().get('input[type="radio"][value="multiplier"]').setValue(true)
    await editor().get('[data-testid="price-rule-multiplier"]').setValue('')
    await row('o3').get('[data-testid="model-price-set"]').trigger('click')
    expect(await w.vm.save()).toBe(false)
    expect(mocks.put).not.toHaveBeenCalled()
    expect(mocks.toast).toHaveBeenCalledWith('The price of o3 is incomplete. Correct it first.', 'error')
    expect(row('o3').get('[data-testid="model-price-unsaved"]').classes()).toContain('badge-danger')
    expect(w.vm.dirty).toBe(true)
  })

  it('edits the prices of a segment and leaves the group pricing read-only', async () => {
    mocks.get.mockResolvedValue(
      prices([{ model: 'gpt-4o', base: price(3, 15), group_rule: { kind: 'multiplier', multiplier: '1.5' }, segment_rule: null, list_price: { ...price(3, 15), multiplier: '1.5' }, source: 'group' }], vip)
    )
    const { w, row, editor } = await render({ groupId: 3, segment: vip, editable: true })
    expect(mocks.get).toHaveBeenCalledWith('/groups/3/model-prices', { segment_id: 7 })
    expect(row('gpt-4o').get('[data-testid="model-price-segment-rule"]').text()).toBe('Follows group pricing')
    expect(row('gpt-4o').find('[data-testid="model-price-clear"]').exists()).toBe(false)
    await row('gpt-4o').get('[data-testid="model-price-set"]').trigger('click')
    await editor().get('input[type="radio"][value="custom"]').setValue(true)
    await editor().get('[data-testid="price-rule-p"]').setValue('1')
    expect(row('gpt-4o').get('[data-testid="model-price-group-rule"]').text()).toBe('Global ×1.5')
    expect(row('gpt-4o').get('[data-testid="model-price-list"]').text()).toBe('$1 / $15')
    expect(row('gpt-4o').get('[data-testid="model-price-source"]').text()).toBe('Own price')

    mocks.put.mockResolvedValue(prices([], vip))
    await w.vm.save()
    expect(mocks.put.mock.calls[0][1].items).toMatchObject([{ segment_id: 7, model: 'gpt-4o', rule: { kind: 'custom', mode: 'visual', config: singleTier({ p: 1, c: 15 }) } }])
    expect(mocks.put.mock.calls[0][2]).toEqual({ query: { segment_id: 7 } })
  })

  it('explains itself instead of loading for a group not saved yet, or one without models', async () => {
    const unsaved = await render({ groupId: null })
    expect(mocks.get).not.toHaveBeenCalled()
    expect(unsaved.w.get('[data-testid="model-price-empty"]').text()).toContain('once the group is saved')
    mocks.get.mockResolvedValue(prices([]))
    const empty = await render({ groupId: 3 })
    expect(empty.w.get('[data-testid="model-price-empty"]').text()).toContain('provides no models yet')
  })
})
