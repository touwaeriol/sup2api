import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import { h, ref, type Component, type SetupContext } from 'vue'
import { SSelect, type SelectOption } from '@sub2api/ui'
import { i18n } from '@/i18n'
import type { SegmentRef } from '@/api/pricing'
import type { DraftedSetting } from '@/components/pricing/accessRateDrafts'
import GroupPricingEditor from './GroupPricingEditor.vue'

enableAutoUnmount(afterEach)

/** The price table: the rate it is given, whether it has drafts and what saving them answers. */
const tableDirty = ref(false)
const tableSave = vi.fn()
const table: Component = {
  props: ['groupId', 'offer', 'rate', 'editable'],
  setup(props: Record<string, any>, { expose, slots }: SetupContext) {
    expose({ save: tableSave, dirty: tableDirty })
    return () => h('div', { 'data-testid': 'table', 'data-rate': props.rate }, slots.rate?.())
  }
}

/** A row of the user groups tab, as it hands its rows over: only what the price tab reads. */
const row = (segment: SegmentRef, rate: string, accessible = true) => ({ id: segment.id, segment, accessible, rate: { value: rate } }) as DraftedSetting
const segments = [
  row({ id: 2, kind: 'named', name: 'VIP' }, '0.72'),
  row({ id: 4, kind: 'named', name: 'Staff' }, '0.8', false),
  row({ id: 104, kind: 'personal', name: 'a@b.c', user_id: 9 }, '0.65')
]

function render(props: { defaultRate?: string; segments?: DraftedSetting[] } = {}) {
  const w = mount(GroupPricingEditor, {
    props: { groupId: 3, segments, defaultRate: '0.8', editable: true, ...props },
    global: { plugins: [i18n], stubs: { Teleport: true, ModelPriceTable: table } }
  })
  const payer = () => w.getComponent<typeof SSelect>('[data-testid="group-pricing-payer"]')
  const rate = () => w.get('[data-testid="table"]').attributes('data-rate')
  const pick = async (value: SelectOption['value']) => {
    payer().vm.$emit('update:modelValue', value)
    await w.vm.$nextTick()
  }
  return { w, payer, rate, pick }
}

beforeEach(() => {
  i18n.global.locale.value = 'zh'
  tableDirty.value = false
  tableSave.mockReset().mockResolvedValue(true)
})

describe('group pricing editor', () => {
  it('pays at the group rate of the form, then at the rate of the user group or person picked', async () => {
    const { payer, rate, pick, w } = render()
    expect(rate()).toBe('0.8')
    const options = payer().props('options') as SelectOption[]
    expect(options.map((o) => [o.label, o.options?.map((c) => [c.label, c.tag])])).toEqual([
      ['所有人（分组倍率）×0.8', undefined],
      ['用户组', [['VIP ×0.72', undefined], ['Staff ×0.8', '未开通']]],
      ['个人', [['a@b.c ×0.65', undefined]]]
    ])
    await pick(104)
    expect(rate()).toBe('0.65')
    await w.setProps({ defaultRate: '1.2' })
    expect(rate()).toBe('0.65')
    await pick('all')
    expect(rate()).toBe('1.2')
  })

  it('goes back to everyone when the user group picked leaves the rows, and pays at no rate while the group rate is not one', async () => {
    const { w, rate, pick } = render()
    await pick(2)
    await w.setProps({ segments: segments.slice(1) })
    expect(rate()).toBe('0.8')
    await w.setProps({ defaultRate: '1.' })
    expect(rate()).toBe('')
  })

  it('lists only everyone without user groups', () => {
    const { payer } = render({ segments: [] })
    expect((payer().props('options') as SelectOption[]).map((o) => o.label)).toEqual(['所有人（分组倍率）×0.8'])
  })

  it('saves the table under the id it is given and says whether it has drafts', async () => {
    const { w } = render()
    const vm = w.vm as unknown as { save: (gid: number) => Promise<boolean>; dirty: boolean }
    expect(vm.dirty).toBe(false)
    tableDirty.value = true
    expect(vm.dirty).toBe(true)
    expect(await vm.save(9)).toBe(true)
    expect(tableSave).toHaveBeenCalledWith(9)
  })
})
