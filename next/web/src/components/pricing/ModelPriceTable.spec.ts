import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import type { CatalogPrice, GroupModelPrices, ModelPriceRow, PriceDef } from '@/api/pricing'
import AddSelect from './AddSelect.vue'
import ModelPriceTable from './ModelPriceTable.vue'
import { singleTier } from '@/views/prices/priceExpr'

const mocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), toast: vi.fn() }))
vi.mock('@sub2api/host', async (original) => ({ ...(await original<typeof import('@sub2api/host')>()), api: { get: mocks.get, put: mocks.put } }))
vi.mock('@sub2api/ui', async (original) => ({ ...(await original<object>()), toast: (...a: unknown[]) => mocks.toast(...a) }))
enableAutoUnmount(afterEach)

i18n.global.locale.value = 'en'

const price = (p: number, c: number): PriceDef => ({ mode: 'visual', config: singleTier({ p, c }), expression: '' })
const catalog: CatalogPrice[] = [
  { model: 'claude-haiku-4-5', price: price(1, 5) },
  { model: 'gpt-4o', price: price(3, 15) },
  { model: 'gpt-5', price: price(1.25, 10) },
  { model: 'o3', price: price(2, 8) }
]

function prices(models: ModelPriceRow[]): GroupModelPrices {
  return { group: { id: 3, name: 'team' }, rate: { value: '1', source: 'group', group_default: '1', discount: '1' }, models }
}
const groupView = () =>
  prices([
    // A multiplier rule: the server gives the original price as it is, with the multiplier beside it.
    { model: 'gpt-4o', offered: true, base: price(3, 15), group_rule: { kind: 'multiplier', multiplier: '1.5' }, list_price: { ...price(3, 15), multiplier: '1.5' }, source: 'group' },
    { model: 'o3', offered: true, base: price(2, 8), group_rule: null, list_price: price(2, 8), source: 'global' },
    // Priced before an account provides it.
    { model: 'gpt-5', offered: false, base: price(1.25, 10), group_rule: { kind: 'multiplier', multiplier: '0.8' }, list_price: { ...price(1.25, 10), multiplier: '0.8' }, source: 'group' }
  ])

interface Props {
  groupId: number | null
  rate?: string
  editable?: boolean
}
async function render(props: Props) {
  const w = mount(ModelPriceTable, { props: { rate: '1', ...props }, global: { plugins: [i18n], stubs: { Teleport: true } } })
  await flushPromises()
  const row = (model: string) => w.get(`[data-model="${model}"]`)
  const cell = (model: string, name: string) => row(model).get(`[data-testid="model-price-${name}"]`).text()
  /** The one open rule editor, in the row under its model's. */
  const editor = () => w.get('[data-testid="price-rule-editor"]')
  const add = async (model: string) => {
    w.getComponent(AddSelect).vm.$emit('add', model)
    await flushPromises()
  }
  return { w, row, cell, editor, add }
}

beforeEach(() => {
  mocks.get.mockReset().mockImplementation(async (path: string) => (path === '/pricing/catalog' ? catalog : groupView()))
  mocks.put.mockReset()
  mocks.toast.mockReset()
})

describe('model price table', () => {
  it('lists each model at its original price, the group price and what is paid at the rate', async () => {
    const { w, cell, row } = await render({ groupId: 3, rate: '1.2' })
    expect(mocks.get.mock.calls.map((c) => c[0])).toEqual(['/groups/3/model-prices'])
    expect(cell('gpt-4o', 'base')).toBe('$3 / $15')
    expect(row('gpt-4o').get('[data-testid="model-price-base"]').classes()).toContain('line-through')
    expect(cell('gpt-4o', 'group')).toMatch(/^\$4\.5 \/ \$22\.5 ?Original × 1\.5$/)
    expect(cell('gpt-4o', 'paid')).toBe('$5.4 / $27')
    expect(cell('o3', 'group')).toBe('Original price')
    expect(row('o3').get('[data-testid="model-price-base"]').classes()).not.toContain('line-through')
    expect(cell('o3', 'paid')).toBe('$2.4 / $9.6')
    expect(cell('gpt-5', 'not-offered')).toBe('Not provided yet')
    expect(row('o3').find('[data-testid="model-price-not-offered"]').exists()).toBe(false)
    expect(w.get('[data-testid="model-price-count"]').text()).toBe('3 models · 2 repriced')
    expect(w.text()).toContain('Paid (×1.2)')
    // Read-only: nothing to change, nothing to add.
    expect(w.find('[data-testid="model-price-set"]').exists()).toBe(false)
    expect(w.find('[data-testid="model-price-add"]').exists()).toBe(false)
  })

  it('pays nothing it can name while the rate is not one', async () => {
    const { cell } = await render({ groupId: 3, rate: '' })
    expect(cell('o3', 'paid')).toBe('—')
  })

  it('keeps edits in drafts, shows the price they give, and saves them as patches', async () => {
    const { w, row, cell, editor } = await render({ groupId: 3, editable: true })
    await row('o3').get('[data-testid="model-price-set"]').trigger('click')
    expect(w.text()).toContain('Price of o3 in this group')
    await editor().get('input[type="radio"][value="multiplier"]').setValue(true)
    await editor().get('input[data-testid="price-rule-multiplier"]').setValue('2')
    expect(cell('o3', 'group')).toMatch(/^\$4 \/ \$16 ?Original × 2$/)
    expect(row('o3').find('[data-testid="model-price-unsaved"]').exists()).toBe(true)
    await row('gpt-4o').get('[data-testid="model-price-clear"]').trigger('click')
    expect(cell('gpt-4o', 'group')).toBe('Original price')
    expect(w.vm.dirty).toBe(true)
    expect(w.get('[data-testid="model-price-dirty"]').text()).toBe('2 unsaved changes')

    mocks.put.mockResolvedValue(prices([]))
    expect(await w.vm.save(3)).toBe(true)
    expect(mocks.put).toHaveBeenCalledWith('/groups/3/model-prices', { items: [{ model: 'o3', rule: { kind: 'multiplier', multiplier: '2' } }, { model: 'gpt-4o', rule: null }] })
    expect(w.vm.dirty).toBe(false)
    expect(w.find('[data-testid="price-rule-editor"]').exists()).toBe(false)
  })

  it('shows a draft as the saved rule will read', async () => {
    const { w, row, cell, editor } = await render({ groupId: 3, editable: true })
    await row('o3').get('[data-testid="model-price-set"]').trigger('click')
    await editor().get('input[type="radio"][value="multiplier"]').setValue(true)
    await editor().get('input[data-testid="price-rule-multiplier"]').setValue('1.5')
    const drafted = cell('o3', 'group')

    mocks.put.mockResolvedValue(prices([{ model: 'o3', offered: true, base: price(2, 8), group_rule: { kind: 'multiplier', multiplier: '1.5' }, list_price: { ...price(2, 8), multiplier: '1.5' }, source: 'group' }]))
    await w.vm.save(3)
    await flushPromises()
    expect(row('o3').find('[data-testid="model-price-unsaved"]').exists()).toBe(false)
    expect(cell('o3', 'group')).toBe(drafted)
  })

  it('adds any model with an original price, which can go again until it is saved', async () => {
    const { w, row, cell, add } = await render({ groupId: 3, editable: true })
    expect(mocks.get).toHaveBeenCalledWith('/pricing/catalog')
    const options = () => w.getComponent(AddSelect).props('options').map((o: { value: unknown; hint?: string }) => [o.value, o.hint])
    expect(options()).toEqual([['claude-haiku-4-5', '$1 / $5']])
    await add('claude-haiku-4-5')
    expect(cell('claude-haiku-4-5', 'base')).toBe('$1 / $5')
    expect(row('claude-haiku-4-5').get('[data-testid="model-price-not-offered"]').attributes('title')).toBe('No account in the group has this model yet; the price is kept until one does')
    expect(options()).toEqual([])
    expect(row('gpt-5').find('[data-testid="model-price-remove"]').exists()).toBe(false)
    await row('claude-haiku-4-5').get('[data-testid="model-price-remove"]').trigger('click')
    expect(w.find('[data-model="claude-haiku-4-5"]').exists()).toBe(false)
  })

  it('drops the draft of a model it removes', async () => {
    const { w, row, editor, add } = await render({ groupId: 3, editable: true })
    await add('claude-haiku-4-5')
    await row('claude-haiku-4-5').get('[data-testid="model-price-set"]').trigger('click')
    await editor().get('input[type="radio"][value="multiplier"]').setValue(true)
    expect(w.vm.dirty).toBe(true)
    await row('claude-haiku-4-5').get('[data-testid="model-price-remove"]').trigger('click')
    expect(w.vm.dirty).toBe(false)
  })

  it('names the multiplier after a price billed by an expression it cannot break down', async () => {
    const opaque: PriceDef = { mode: 'expression', config: null, expression: 'p * 2 + unknown(1)' }
    mocks.get.mockResolvedValue(prices([{ model: 'custom', offered: true, base: opaque, group_rule: { kind: 'multiplier', multiplier: '1.5' }, list_price: { ...opaque, multiplier: '1.5' }, source: 'group' }]))
    const { cell } = await render({ groupId: 3, rate: '2' })
    expect(cell('custom', 'base')).toBe('Billed by expression')
    expect(cell('custom', 'group')).toMatch(/^Billed by expression ×1\.5 ?Original × 1\.5$/)
    expect(cell('custom', 'paid')).toBe('Billed by expression ×3')
  })

  it('keeps what is edited while a save is under way', async () => {
    const { w, row, editor } = await render({ groupId: 3, editable: true })
    await row('gpt-4o').get('[data-testid="model-price-clear"]').trigger('click')
    let answer!: (v: GroupModelPrices) => void
    mocks.put.mockReturnValue(new Promise<GroupModelPrices>((resolve) => (answer = resolve)))
    const saving = w.vm.save(3)

    await row('o3').get('[data-testid="model-price-set"]').trigger('click')
    await editor().get('input[type="radio"][value="multiplier"]').setValue(true)
    const [gpt4o, o3, gpt5] = groupView().models
    answer(prices([{ ...gpt4o, group_rule: null, list_price: price(3, 15), source: 'global' }, o3, gpt5]))
    expect(await saving).toBe(true)
    await flushPromises()

    expect(mocks.put.mock.calls[0][1].items).toEqual([{ model: 'gpt-4o', rule: null }])
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
    expect([set('gpt-4o').text(), set('o3').text()]).toEqual(['Done', 'Change'])
    await set('gpt-4o').trigger('click')
    expect(w.find('[data-testid="price-rule-editor"]').exists()).toBe(false)
    expect(await w.vm.save(3)).toBe(true)
    expect(mocks.put).not.toHaveBeenCalled()
  })

  it('does not save a rule that is not filled in', async () => {
    const { w, row, editor } = await render({ groupId: 3, editable: true })
    await row('o3').get('[data-testid="model-price-set"]').trigger('click')
    await editor().get('input[type="radio"][value="multiplier"]').setValue(true)
    await editor().get('input[data-testid="price-rule-multiplier"]').setValue('')
    await row('o3').get('[data-testid="model-price-set"]').trigger('click')
    expect(await w.vm.save(3)).toBe(false)
    expect(mocks.put).not.toHaveBeenCalled()
    expect(mocks.toast).toHaveBeenCalledWith('The price of o3 is incomplete. Correct it first.', 'error')
    expect(row('o3').get('[data-testid="model-price-unsaved"]').classes()).toContain('badge-danger')
    expect(w.vm.dirty).toBe(true)
  })

  it('searches and filters the repriced models once there are many', async () => {
    const many = Array.from({ length: 9 }, (_, i): ModelPriceRow => ({ model: `m${i}`, offered: true, base: price(1, 2), group_rule: i === 4 ? { kind: 'multiplier', multiplier: '2' } : null, list_price: price(1, 2), source: i === 4 ? 'group' : 'global' }))
    mocks.get.mockResolvedValue(prices(many))
    const { w } = await render({ groupId: 3 })
    const shown = () => w.findAll('[data-testid="model-price-row"]').map((r) => r.attributes('data-model'))
    await w.get('input[data-testid="model-price-only-ruled"]').setValue(true)
    expect(shown()).toEqual(['m4'])
    expect((await render({ groupId: 3 })).w.find('[data-testid="model-price-search"]').exists()).toBe(true)
    mocks.get.mockResolvedValue(prices(many.slice(0, 8)))
    expect((await render({ groupId: 3 })).w.find('[data-testid="model-price-search"]').exists()).toBe(false)
  })
})

describe('the model prices of a group not saved yet', () => {
  it('starts empty, as a group without accounts provides nothing yet', async () => {
    const { w } = await render({ groupId: null, editable: true })
    expect(mocks.get.mock.calls.map((c) => c[0])).toEqual(['/pricing/catalog'])
    expect(w.find('[data-testid="model-price-row"]').exists()).toBe(false)
    expect(w.get('[data-testid="model-price-empty"]').text()).toContain('Once accounts are put in this group')
  })

  it('prices a model before any account, and saves everything under the id the group gets', async () => {
    const { w, row, editor, add } = await render({ groupId: null, editable: true })
    await add('gpt-5')
    expect(row('gpt-5').find('[data-testid="model-price-not-offered"]').exists()).toBe(true)
    expect(w.find('[data-testid="model-price-no-accounts"]').exists()).toBe(true)
    await row('gpt-5').get('[data-testid="model-price-set"]').trigger('click')
    await editor().get('input[type="radio"][value="multiplier"]').setValue(true)
    await editor().get('input[data-testid="price-rule-multiplier"]').setValue('0.8')

    mocks.put.mockResolvedValue(groupView())
    expect(await w.vm.save(9)).toBe(true)
    expect(mocks.put).toHaveBeenCalledWith('/groups/9/model-prices', { items: [{ model: 'gpt-5', rule: { kind: 'multiplier', multiplier: '0.8' } }] })
    expect(w.vm.dirty).toBe(false)
  })
})
