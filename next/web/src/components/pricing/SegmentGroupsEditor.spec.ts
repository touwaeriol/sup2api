import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h, ref, type PropType } from 'vue'
import { ApiError } from '@sub2api/host'
import { useTabbedForm } from '@sub2api/ui'
import { i18n } from '@/i18n'
import type { Group } from '@/api/types'
import type { GroupModelPrices, SegmentGroupSetting } from '@/api/pricing'
import { singleTier } from '@/views/prices/priceExpr'
import AddSelect from './AddSelect.vue'
import SegmentGroupsEditor from './SegmentGroupsEditor.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), toast: vi.fn(), allPages: vi.fn() }))
vi.mock('@sub2api/host', async (original) => ({ ...(await original<typeof import('@sub2api/host')>()), api: { get: mocks.get } }))
vi.mock('@sub2api/ui', async (original) => ({ ...(await original<object>()), toast: (...a: unknown[]) => mocks.toast(...a) }))
vi.mock('@/composables/lookups', () => ({ fetchAllPages: mocks.allPages }))
enableAutoUnmount(afterEach)

i18n.global.locale.value = 'en'

function setting(id: number, visibility: 'public' | 'restricted', over: Partial<SegmentGroupSetting> = {}): SegmentGroupSetting {
  return {
    segment: { id: 2, kind: 'named', name: 'VIP' },
    group: { id, name: `g${id}`, description: '', status: 'active', visibility, rate_multiplier: '1.2' },
    accessible: visibility === 'public',
    granted: false,
    rate_override: null,
    rate: { value: '1.2', source: 'group', group_default: '1.2' },
    model_rates: [],
    ...over
  }
}
const saved = () => [setting(1, 'public', { model_rates: [{ model: 'gpt-4o', rate: '0.85' }] }), setting(2, 'restricted', { rate_override: '0.5', rate: { value: '0.5', source: 'segment', group_default: '1.2' } })]
const group = (id: number, visibility: Group['visibility'], rate: string): Group => ({ id, name: `g${id}`, description: '', status: 'active', visibility, rate_multiplier: rate, model_allowlist: [] })
const price = { mode: 'visual' as const, config: singleTier({ p: 3, c: 15 }), expression: '' }
const modelPrices = (): GroupModelPrices => ({
  group: { id: 1, name: 'g1' },
  rate: { value: '1.2', source: 'group', group_default: '1.2' },
  models: [
    { model: 'gpt-4o', offered: true, base: price, price: null, rate: null },
    { model: 'o3', offered: true, base: price, price: null, rate: null },
    { model: 'unpriced', offered: true, base: null, price: null, rate: null }
  ]
})

/** The editor as the part of a tabbed form, as the segment editor and the pricing drawer hold it. */
const Host = defineComponent({
  props: { segmentId: { type: Number as PropType<number | null>, default: null }, editable: Boolean },
  setup(props, { expose }) {
    const form = useTabbedForm(ref('groups'), ['groups'])
    expose({ form })
    return () => h(SegmentGroupsEditor, { segmentId: props.segmentId, editable: props.editable, tab: 'groups' })
  }
})

async function render(props: { segmentId: number | null; editable?: boolean }) {
  const w = mount(Host, { props, global: { plugins: [i18n] } })
  await flushPromises()
  const row = (id: number) => w.get(`[data-group="${id}"]`)
  const rate = (id: number) => row(id).get('[data-testid="access-rate-value"]').text()
  const source = (id: number) => row(id).get('[data-testid="access-rate-source"]').text()
  const form = (w.vm as unknown as { form: ReturnType<typeof useTabbedForm> }).form
  const sent = async (answer: () => Promise<unknown> = async () => ({})) => {
    const send = vi.fn((_body: Record<string, unknown>) => answer())
    const result = await form.submit(send)
    await flushPromises()
    return { result, body: send.mock.calls[0]?.[0] }
  }
  const openModels = async (id: number) => {
    await row(id).get('[data-testid="segment-model-rates-toggle"]').trigger('click')
    await flushPromises()
    return w.get(`[data-testid="segment-model-rates"][data-group="${id}"]`)
  }
  return { w, row, rate, source, form, sent, openModels }
}

beforeEach(() => {
  mocks.get.mockReset().mockImplementation(async (path: string) => (path.endsWith('/model-prices') ? modelPrices() : saved()))
  mocks.toast.mockReset()
  mocks.allPages.mockReset()
})

describe('the account groups of a segment', () => {
  it('lists what the segment gets in each account group, in words without fields', async () => {
    const { w, row, source } = await render({ segmentId: 2 })
    expect(mocks.get).toHaveBeenCalledWith('/segments/2/groups')
    expect(w.findAll('thead th').map((th) => th.text())).toEqual(['Account group', 'Available', 'Rate', 'Paid rate'])
    expect(row(1).text()).toContain('Base rate ×1.2')
    expect(row(1).get('[data-testid="segment-model-rates-toggle"]').text()).toBe('Model rates (1)')
    expect(row(1).get('[data-testid="access-rate-public"]').text()).toBe('Public')
    expect(row(1).get('[data-testid="rate-override-value"]').text()).toBe('Follows')
    expect(source(1)).toBe('Follows the account group')
    expect(row(2).get('[data-testid="segment-group-private"]').text()).toBe('Private')
    expect(row(2).get('[data-testid="access-rate-access"]').text()).toBe('Not granted')
    expect(row(2).get('[data-testid="rate-override-value"]').text()).toBe('×0.5')
    expect(source(2)).toBe('Not available')
    expect(w.find('[data-testid="rate-override-input"], [data-testid="access-rate-grant"]').exists()).toBe(false)
  })

  it('keeps grants and rates in drafts and sends the changed account groups as part of the segment', async () => {
    const { row, rate, source, form, sent } = await render({ segmentId: 2, editable: true })
    await row(1).get('input[data-testid="rate-override-input"]').setValue('0.6')
    expect([rate(1), source(1)]).toEqual(['×0.6', 'Set by the user group'])
    await row(2).get('input[data-testid="access-rate-grant"]').setValue(true)
    expect(row(2).find('[data-testid="access-rate-unsaved"]').exists()).toBe(true)
    expect(form.dirtyTabs.value).toEqual(['groups'])
    expect((await sent()).body).toEqual({ group_rates: [{ group_id: 1, rate: '0.6' }, { group_id: 2, granted: true, rate: '0.5' }] })
  })

  it('sends nothing without a change, and keeps a rate that is not one back', async () => {
    const { row, sent } = await render({ segmentId: 2, editable: true })
    expect((await sent()).body).toEqual({})
    await row(1).get('input[data-testid="rate-override-input"]').setValue('0.')
    expect((await sent()).result).toBe(false)
    expect(mocks.toast).toHaveBeenCalledWith('The rate of "g1" is not valid: enter a number from 0 to 999999.9999, up to 4 decimals.', 'error')
  })

  it('shows what the server refused on the account group it was sent for', async () => {
    const { row, sent } = await render({ segmentId: 2, editable: true })
    await row(2).get('input[data-testid="rate-override-input"]').setValue('0.4')
    const refusal = new ApiError(400, { code: 'invalid_argument', message: 'invalid', details: { fields: [{ field: 'group_rates.0.rate', code: 'invalid', message: 'too low' }] } })
    expect((await sent(() => Promise.reject(refusal))).result).toBe(false)
    expect(row(2).get('[data-testid="access-rate-error"]').text()).toBe('too low')
  })

  it('sends its drafts once it turns editable in place, as a user made personal in the pricing drawer', async () => {
    const { w, row, sent } = await render({ segmentId: 2 })
    expect((await sent()).body).toEqual({})
    await w.setProps({ segmentId: 31, editable: true })
    await flushPromises()
    await row(1).get('input[data-testid="rate-override-input"]').setValue('0.6')
    expect((await sent()).body).toEqual({ group_rates: [{ group_id: 1, rate: '0.6' }] })
    await w.setProps({ editable: false })
    expect((await sent()).body).toEqual({})
  })

  it('starts a segment not created yet from every account group, with nothing set', async () => {
    mocks.allPages.mockResolvedValue([group(1, 'public', '1'), group(5, 'restricted', '0.8')])
    const { row, rate, sent } = await render({ segmentId: null, editable: true })
    expect(mocks.allPages).toHaveBeenCalledWith('/groups')
    expect(rate(1)).toBe('×1')
    await row(5).get('input[data-testid="access-rate-grant"]').setValue(true)
    expect((await sent()).body).toEqual({ group_rates: [{ group_id: 5, granted: true, rate: null }] })
  })
})

describe('the model rates of a segment in an account group', () => {
  it('lists them under the row, in words without fields', async () => {
    const { openModels } = await render({ segmentId: 2 })
    const rates = await openModels(1)
    expect(rates.get('[data-testid="segment-model-rate-row"]').attributes('data-model')).toBe('gpt-4o')
    expect(rates.get('[data-testid="segment-model-rate-value"]').text()).toBe('×0.85')
    expect(rates.find('[data-testid="segment-model-rate-add"]').exists()).toBe(false)
    expect(mocks.get).not.toHaveBeenCalledWith('/groups/1/model-prices')
  })

  it('adds a model at the rate it pays now: the account group\'s model rate, or the row\'s once it has one', async () => {
    mocks.get.mockImplementation(async (path: string) => {
      if (!path.endsWith('/model-prices')) return saved()
      const prices = modelPrices()
      prices.models[1].rate = '0.9'
      return prices
    })
    const { w, openModels } = await render({ segmentId: 2, editable: true })
    const rates = await openModels(1)
    rates.getComponent(AddSelect).vm.$emit('add', 'o3')
    await flushPromises()
    expect((w.get('[data-testid="segment-model-rate-row"][data-model="o3"] input').element as HTMLInputElement).value).toBe('0.9')
    await w.get('[data-group="1"] input[data-testid="rate-override-input"]').setValue('0.6')
    rates.getComponent(AddSelect).vm.$emit('add', 'gpt-5')
    await flushPromises()
    expect((w.get('[data-testid="segment-model-rate-row"][data-model="gpt-5"] input').element as HTMLInputElement).value).toBe('0.6')
    expect(rates.text()).toContain('the others pay the rate that applies to each')
  })

  it('adds a model with an original price at the rate of the row, changes and removes one, all sent with the segment', async () => {
    const { w, row, openModels, sent } = await render({ segmentId: 2, editable: true })
    const rates = await openModels(1)
    expect(mocks.get).toHaveBeenCalledWith('/groups/1/model-prices')
    const add = rates.getComponent(AddSelect)
    expect(add.props('options').map((o: { value: unknown }) => o.value)).toEqual(['o3'])
    add.vm.$emit('add', 'o3')
    await flushPromises()
    const input = (model: string) => w.get(`[data-testid="segment-model-rate-row"][data-model="${model}"] input`)
    expect((input('o3').element as HTMLInputElement).value).toBe('1.2')
    await input('o3').setValue('0.7')
    await w.get('[data-model="gpt-4o"] [data-testid="segment-model-rate-remove"]').trigger('click')
    expect(row(1).get('[data-testid="segment-model-rates-toggle"]').text()).toBe('Model rates (2)')
    expect((await sent()).body).toEqual({ model_rates: [{ group_id: 1, model: 'o3', rate: '0.7' }, { group_id: 1, model: 'gpt-4o', rate: null }] })
  })

  it('keeps a model rate that is not one back, naming the account group', async () => {
    const { w, openModels, sent } = await render({ segmentId: 2, editable: true })
    await openModels(1)
    await w.get('[data-model="gpt-4o"] input').setValue('x')
    expect((await sent()).result).toBe(false)
    expect(mocks.toast).toHaveBeenCalledWith('The model rate of gpt-4o in "g1" is not valid: enter a number from 0 to 999999.9999, up to 4 decimals.', 'error')
  })
})
