import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h, ref, type PropType } from 'vue'
import { useTabbedForm } from '@sub2api/ui'
import { i18n } from '@/i18n'
import type { CatalogPrice, GroupModelPrices, Segment, SegmentGroupSetting } from '@/api/pricing'
import AddSelect from '@/components/pricing/AddSelect.vue'
import { singleTier } from '@/views/prices/priceExpr'
import GroupSegmentsEditor from './GroupSegmentsEditor.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), toast: vi.fn(), allPages: vi.fn() }))
vi.mock('@sub2api/host', async (original) => ({ ...(await original<typeof import('@sub2api/host')>()), api: { get: mocks.get } }))
vi.mock('@sub2api/ui', async (original) => ({ ...(await original<object>()), toast: (...a: unknown[]) => mocks.toast(...a) }))
vi.mock('@/composables/lookups', () => ({ fetchAllPages: mocks.allPages }))
enableAutoUnmount(afterEach)

i18n.global.locale.value = 'en'

function setting(id: number, name: string, over: Partial<SegmentGroupSetting> = {}): SegmentGroupSetting {
  return {
    segment: { id, kind: 'named', name },
    group: { id: 7, name: 'g7', description: '', status: 'active', visibility: 'public', rate_multiplier: '1.2' },
    accessible: true,
    granted: false,
    rate_override: null,
    rate: { value: '1.2', source: 'group', group_default: '1.2' },
    model_rates: [],
    member_count: 1,
    ...over
  }
}
const saved = () => [
  setting(2, 'VIP', { rate_override: '0.5', rate: { value: '0.5', source: 'segment', group_default: '1.2' }, model_rates: [{ model: 'gpt-4o', rate: '0.85' }] }),
  setting(3, 'Team')
]
const price = { mode: 'visual' as const, config: singleTier({ p: 3, c: 15 }), expression: '' }
const modelPrices = (): GroupModelPrices => ({
  group: { id: 7, name: 'g7' },
  rate: { value: '1.2', source: 'group', group_default: '1.2' },
  models: [
    { model: 'gpt-4o', offered: true, base: price, price: null, rate: null },
    { model: 'o3', offered: true, base: price, price: null, rate: '0.9' },
    { model: 'unpriced', offered: true, base: null, price: null, rate: null }
  ]
})
const catalog = (): CatalogPrice[] => [{ model: 'claude', price }]
const segment = (id: number, name: string): Segment => ({ id, kind: 'named', name, description: '', is_default: false, member_count: 0, granted_groups: [], rate_overrides: [], created_at: '' })

/** The editor as the part of the account group's tabbed form. */
const Host = defineComponent({
  props: { groupId: { type: Number as PropType<number | null>, default: null } },
  setup(props, { expose }) {
    const form = useTabbedForm(ref('segments'), ['segments'])
    expose({ form })
    return () => h(GroupSegmentsEditor, { groupId: props.groupId, visibility: 'public', defaultRate: '1.2', editable: true, tab: 'segments' })
  }
})

async function render(groupId: number | null) {
  const w = mount(Host, { props: { groupId }, global: { plugins: [i18n], stubs: { PersonalSegmentPicker: true } } })
  await flushPromises()
  const row = (id: number) => w.get(`[data-testid="access-rate-row"][data-segment="${id}"]`)
  const form = (w.vm as unknown as { form: ReturnType<typeof useTabbedForm> }).form
  const sent = async () => {
    const send = vi.fn(async (_body: Record<string, unknown>) => ({}))
    const result = await form.submit(send)
    await flushPromises()
    return { result, body: send.mock.calls[0]?.[0] }
  }
  const openModels = async (id: number) => {
    await row(id).get('[data-testid="group-segment-model-rates-toggle"]').trigger('click')
    await flushPromises()
    return w.get('[data-testid="segment-model-rates"]')
  }
  const input = (model: string) => w.get(`[data-testid="segment-model-rate-row"][data-model="${model}"] input`)
  return { w, row, sent, openModels, input }
}

beforeEach(() => {
  mocks.get.mockReset().mockImplementation(async (path: string) => {
    if (path.endsWith('/model-prices')) return modelPrices()
    if (path === '/pricing/catalog') return catalog()
    return saved()
  })
  mocks.toast.mockReset()
  mocks.allPages.mockReset().mockResolvedValue([segment(2, 'VIP')])
})

describe('GroupSegmentsEditor model rates', () => {
  it('lists a user group’s model rates under its row and sends the changes as segment_model_rates', async () => {
    const { row, sent, openModels, input, w } = await render(7)
    expect(row(2).get('[data-testid="group-segment-model-rates-toggle"]').text()).toBe('Model rates (1)')
    const rates = await openModels(2)
    expect(mocks.get).toHaveBeenCalledWith('/groups/7/model-prices')
    const add = rates.getComponent(AddSelect)
    expect(add.props('options').map((o: { value: unknown }) => o.value)).toEqual(['o3'])
    add.vm.$emit('add', 'o3')
    await flushPromises()
    expect((input('o3').element as HTMLInputElement).value).toBe('0.5')
    await input('o3').setValue('0.7')
    await w.get('[data-model="gpt-4o"] [data-testid="segment-model-rate-remove"]').trigger('click')
    expect((await sent()).body).toEqual({
      segment_model_rates: [
        { segment_id: 2, model: 'o3', rate: '0.7' },
        { segment_id: 2, model: 'gpt-4o', rate: null }
      ]
    })
  })

  it('drops the unsaved model rates of a row removed, which takes its saved ones', async () => {
    const { row, sent, openModels } = await render(7)
    const rates = await openModels(2)
    rates.getComponent(AddSelect).vm.$emit('add', 'o3')
    await flushPromises()
    await row(2).get('[data-testid="access-rate-remove"]').trigger('click')
    expect((await sent()).body).toEqual({ segment_rates: [{ segment_id: 2, remove: true }] })
  })

  it('offers every model with a global price while the account group is not created', async () => {
    const { w, openModels } = await render(null)
    w.getComponent(AddSelect).vm.$emit('add', 2)
    await flushPromises()
    const rates = await openModels(2)
    expect(mocks.get).toHaveBeenCalledWith('/pricing/catalog')
    expect(rates.getComponent(AddSelect).props('options').map((o: { value: unknown }) => o.value)).toEqual(['claude'])
  })

  it('keeps a model rate that is not one back, naming the user group', async () => {
    const { sent, openModels, input } = await render(7)
    await openModels(2)
    await input('gpt-4o').setValue('x')
    expect((await sent()).result).toBe(false)
    expect(mocks.toast).toHaveBeenCalledWith('The model rate of gpt-4o in "VIP" is not valid: enter a number from 0 to 999999.9999, up to 4 decimals.', 'error')
  })
})
