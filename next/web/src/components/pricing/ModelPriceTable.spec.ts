import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h, ref, type PropType } from 'vue'
import { ApiError } from '@sub2api/host'
import { useTabbedForm } from '@sub2api/ui'
import { i18n } from '@/i18n'
import type { CatalogPrice, CustomPrice, GroupModelPrices, ModelPriceRow, PriceDef } from '@/api/pricing'
import AddSelect from './AddSelect.vue'
import ModelPriceTable from './ModelPriceTable.vue'
import { singleTier } from '@/views/prices/priceExpr'

const mocks = vi.hoisted(() => ({ get: vi.fn(), toast: vi.fn() }))
vi.mock('@sub2api/host', async (original) => ({ ...(await original<typeof import('@sub2api/host')>()), api: { get: mocks.get } }))
vi.mock('@sub2api/ui', async (original) => ({ ...(await original<object>()), toast: (...a: unknown[]) => mocks.toast(...a) }))
enableAutoUnmount(afterEach)

i18n.global.locale.value = 'en'

const price = (p: number, c: number): PriceDef => ({ mode: 'visual', config: singleTier({ p, c }), expression: '' })
const own: CustomPrice = { kind: 'custom', mode: 'visual', config: singleTier({ p: 1, c: 8 }) }
const catalog: CatalogPrice[] = [
  { model: 'claude-haiku-4-5', price: price(1, 5) },
  { model: 'gpt-4o', price: price(3, 15) },
  { model: 'gpt-5', price: price(1.25, 10) },
  { model: 'o3', price: price(2, 8) }
]

function prices(models: ModelPriceRow[]): GroupModelPrices {
  return { group: { id: 3, name: 'team' }, rate: { value: '1', source: 'group', group_default: '1' }, models }
}
const groupView = () =>
  prices([
    { model: 'gpt-4o', offered: true, base: price(3, 15), price: null, rate: '1.5' },
    { model: 'o3', offered: true, base: price(2, 8), price: null, rate: null },
    // Priced before an account provides it.
    { model: 'gpt-5', offered: false, base: price(1.25, 10), price: own, rate: null }
  ])

interface Props {
  groupId: number | null
  baseRate?: string
  editable?: boolean
}

/** The table as the part of a tabbed form, as the group editor holds it. */
const Host = defineComponent({
  props: { groupId: { type: Number as PropType<number | null>, default: null }, baseRate: { type: String, default: '1' } },
  setup(props, { expose }) {
    const form = useTabbedForm(ref('pricing'), ['pricing'])
    expose({ form })
    return () => h(ModelPriceTable, { groupId: props.groupId, baseRate: props.baseRate, editable: true, tab: 'pricing' })
  }
})

async function render(props: Props, hosted = false) {
  const w = hosted ? mount(Host, { props, global: { plugins: [i18n], stubs: { Teleport: true } } }) : mount(ModelPriceTable, { props: { baseRate: '1', ...props }, global: { plugins: [i18n], stubs: { Teleport: true } } })
  await flushPromises()
  const row = (model: string) => w.get(`[data-model="${model}"]`)
  const cell = (model: string, name: string) => row(model).get(`[data-testid="model-price-${name}"]`).text()
  const rate = (model: string) => row(model).get('input[data-testid="rate-override-input"]')
  /** The one open price editor, in the row under its model's. */
  const editor = () => w.get('[data-testid="price-rule-editor"]')
  const add = async (model: string) => {
    w.getComponent(AddSelect).vm.$emit('add', model)
    await flushPromises()
  }
  return { w, row, cell, rate, editor, add }
}
const hosted = async (props: Props) => {
  const r = await render(props, true)
  const form = (r.w.vm as unknown as { form: ReturnType<typeof useTabbedForm> }).form
  return { ...r, form, sent: async (answer: () => Promise<unknown> = async () => ({})) => {
    const send = vi.fn((_body: Record<string, unknown>) => answer())
    const result = await form.submit(send)
    await flushPromises()
    return { result, body: send.mock.calls[0]?.[0] }
  } }
}

beforeEach(() => {
  mocks.get.mockReset().mockImplementation(async (path: string) => (path === '/pricing/catalog' ? catalog : groupView()))
  mocks.toast.mockReset()
})

describe('model price table', () => {
  it('lists each model at its original price, its model price and model rate, and what is paid at the one rate that applies', async () => {
    const { w, cell, row } = await render({ groupId: 3, baseRate: '0.8' })
    expect(mocks.get.mock.calls.map((c) => c[0])).toEqual(['/groups/3/model-prices'])
    expect(cell('gpt-4o', 'base')).toBe('$3 / $15')
    expect(cell('gpt-4o', 'price')).toBe('Follows the original price')
    expect(cell('gpt-4o', 'rate')).toBe('×1.5')
    // The model rate replaces the base rate: ×1.5, not ×1.5 × 0.8.
    expect(cell('gpt-4o', 'paid')).toBe('$4.5 / $22.5')
    expect(cell('gpt-4o', 'paid-source')).toBe('×1.5 · Model rate')
    expect(cell('o3', 'rate')).toBe('Follows the base rate')
    expect(cell('o3', 'paid')).toBe('$1.6 / $6.4')
    expect(cell('o3', 'paid-source')).toBe('×0.8 · Follows the base rate')
    expect(cell('gpt-5', 'price')).toBe('$1 / $8')
    expect(row('gpt-5').get('[data-testid="model-price-base"]').classes()).toContain('line-through')
    expect(cell('gpt-5', 'not-offered')).toBe('Not provided yet')
    expect(row('o3').find('[data-testid="model-price-not-offered"]').exists()).toBe(false)
    expect(w.get('[data-testid="model-price-count"]').text()).toBe('3 models · 2 set')
    // Read-only: nothing to change, nothing to add.
    expect(w.find('[data-testid="model-price-set"]').exists()).toBe(false)
    expect(w.find('[data-testid="model-price-add"]').exists()).toBe(false)
  })

  it('follows the base rate of the form as it is typed', async () => {
    const { w, cell } = await render({ groupId: 3, baseRate: '1' })
    await w.setProps({ baseRate: '2' })
    expect(cell('o3', 'paid')).toBe('$4 / $16')
    expect(cell('gpt-4o', 'paid')).toBe('$4.5 / $22.5')
  })

  it('keeps a model rate and a model price in drafts and sends the changes as part of the group', async () => {
    const { w, row, cell, rate, editor, sent } = await hosted({ groupId: 3 })
    await rate('o3').setValue('0.5')
    expect(cell('o3', 'paid')).toBe('$1 / $4')
    expect(cell('o3', 'paid-source')).toBe('×0.5 · Model rate')
    expect(row('o3').find('[data-testid="model-price-unsaved"]').exists()).toBe(true)
    await row('gpt-4o').get('[data-testid="model-price-set"]').trigger('click')
    await editor().get('input[type="radio"][value="custom"]').setValue(true)
    await row('gpt-5').get('[data-testid="model-price-clear"]').trigger('click')
    expect(w.get('[data-testid="model-price-dirty"]').text()).toBe('3 unsaved changes')

    const { result, body } = await sent()
    expect(result).toEqual({})
    expect(body).toEqual({
      model_prices: [{ model: 'gpt-4o', rule: { kind: 'custom', mode: 'visual', config: singleTier({ p: 3, c: 15 }) } }, { model: 'gpt-5', rule: null }],
      model_rates: [{ model: 'o3', rate: '0.5' }]
    })
  })

  it('sends nothing of a row that reads as saved again, and nothing without a change', async () => {
    const { rate, form, sent } = await hosted({ groupId: 3 })
    await rate('gpt-4o').setValue('1.50')
    expect(form.anyDirty.value).toBe(false)
    expect((await sent()).body).toEqual({})
  })

  it('keeps the group back while a rate is not one, and shows every row so the wrong one is seen', async () => {
    const { row, rate, form, sent } = await hosted({ groupId: 3 })
    await rate('o3').setValue('0.')
    const { result, body } = await sent()
    expect([result, body]).toEqual([false, undefined])
    expect(mocks.toast).toHaveBeenCalledWith('The model price or model rate of o3 is not valid. Correct it first.', 'error')
    expect(form.errorTabs.value).toEqual(['pricing'])
    expect(row('o3').get('[data-testid="model-price-unsaved"]').classes()).toContain('badge-danger')
  })

  it('shows what the server refused on the row it was sent for', async () => {
    const { row, rate, sent } = await hosted({ groupId: 3 })
    await rate('gpt-4o').setValue('2')
    await rate('o3').setValue('0.5')
    const refusal = new ApiError(400, { code: 'invalid_argument', message: 'invalid', details: { fields: [{ field: 'model_rates.1.rate', code: 'invalid', message: 'too low' }] } })
    expect((await sent(() => Promise.reject(refusal))).result).toBe(false)
    expect(row('o3').get('[data-testid="model-price-error"]').text()).toBe('too low')
    expect(row('gpt-4o').find('[data-testid="model-price-error"]').exists()).toBe(false)
    await rate('o3').setValue('0.6')
    expect(row('o3').find('[data-testid="model-price-error"]').exists()).toBe(false)
  })

  it('opens one price editor at a time, under its row', async () => {
    const { w, row } = await render({ groupId: 3, editable: true })
    const set = (model: string) => row(model).get('[data-testid="model-price-set"]')
    await set('gpt-4o').trigger('click')
    await set('o3').trigger('click')
    expect(w.findAll('[data-testid="price-rule-editor"]')).toHaveLength(1)
    expect(row('o3').element.nextElementSibling?.querySelector('[data-testid="price-rule-editor"]')).not.toBeNull()
    await set('o3').trigger('click')
    expect(w.find('[data-testid="price-rule-editor"]').exists()).toBe(false)
  })

  it('adds any model with an original price, which can go again with its draft until it is saved', async () => {
    const { w, row, cell, rate, add, form } = await hosted({ groupId: 3 })
    expect(mocks.get).toHaveBeenCalledWith('/pricing/catalog')
    const options = () => w.getComponent(AddSelect).props('options').map((o: { value: unknown; hint?: string }) => [o.value, o.hint])
    expect(options()).toEqual([['claude-haiku-4-5', '$1 / $5']])
    await add('claude-haiku-4-5')
    expect(cell('claude-haiku-4-5', 'base')).toBe('$1 / $5')
    expect(options()).toEqual([])
    expect(row('gpt-5').find('[data-testid="model-price-remove"]').exists()).toBe(false)
    await rate('claude-haiku-4-5').setValue('0.9')
    expect(form.anyDirty.value).toBe(true)
    await row('claude-haiku-4-5').get('[data-testid="model-price-remove"]').trigger('click')
    expect(w.find('[data-model="claude-haiku-4-5"]').exists()).toBe(false)
    expect(form.anyDirty.value).toBe(false)
  })

  it('searches and filters the models set on once there are many', async () => {
    const many = Array.from({ length: 9 }, (_, i): ModelPriceRow => ({ model: `m${i}`, offered: true, base: price(1, 2), price: null, rate: i === 4 ? '2' : null }))
    mocks.get.mockResolvedValue(prices(many))
    const { w } = await render({ groupId: 3 })
    const shown = () => w.findAll('[data-testid="model-price-row"]').map((r) => r.attributes('data-model'))
    await w.get('input[data-testid="model-price-only-set"]').setValue(true)
    expect(shown()).toEqual(['m4'])
    mocks.get.mockResolvedValue(prices(many.slice(0, 8)))
    expect((await render({ groupId: 3 })).w.find('[data-testid="model-price-search"]').exists()).toBe(false)
  })
})

describe('the model prices of a group not created yet', () => {
  it('starts empty, as a group without accounts provides nothing yet', async () => {
    const { w } = await render({ groupId: null, editable: true })
    expect(mocks.get.mock.calls.map((c) => c[0])).toEqual(['/pricing/catalog'])
    expect(w.find('[data-testid="model-price-row"]').exists()).toBe(false)
    expect(w.get('[data-testid="model-price-empty"]').text()).toContain('A new account group has no accounts yet')
  })

  it('prices a model before any account, sent with the group that creates it', async () => {
    const { w, row, rate, add, sent } = await hosted({ groupId: null })
    await add('gpt-5')
    expect(row('gpt-5').find('[data-testid="model-price-not-offered"]').exists()).toBe(true)
    expect(w.find('[data-testid="model-price-no-accounts"]').exists()).toBe(true)
    await rate('gpt-5').setValue('0.8')
    expect((await sent()).body).toEqual({ model_rates: [{ model: 'gpt-5', rate: '0.8' }] })
  })
})
