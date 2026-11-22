import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { h, type Component, type SetupContext } from 'vue'
import { i18n } from '@/i18n'
import type { Group } from '@/api/types'
import type { SegmentRef } from '@/api/pricing'
import GroupPricingModal from './GroupPricingModal.vue'

enableAutoUnmount(afterEach)
/** The props a stub is handed. */
type Props = Record<string, any>

const vip: SegmentRef = { id: 2, kind: 'named', name: 'VIP' }
const rows = [{ id: 2, segment: vip }]
/** The user groups tab: hands its rows over. */
const segmentsEditor: Component = {
  props: ['groupId', 'visibility', 'defaultRate', 'editable'],
  emits: ['rows'],
  setup(props: Props, { emit }: SetupContext) {
    emit('rows', rows)
    return () => h('div', { 'data-testid': 'segments', 'data-editable': String(!!props.editable), 'data-rate': props.defaultRate })
  }
}
const pricingEditor: Component = {
  props: ['groupId', 'segments', 'defaultRate', 'editable'],
  setup: (props: Props) => () => h('div', { 'data-testid': 'pricing', 'data-editable': String(!!props.editable), 'data-rows': props.segments?.length, 'data-rate': props.defaultRate })
}

const group = { id: 3, name: 'pro', visibility: 'restricted', rate_multiplier: 1.2 } as unknown as Group

async function render() {
  const w = mount(GroupPricingModal, { props: { open: true, group }, global: { plugins: [i18n], stubs: { Teleport: true, GroupSegmentsEditor: segmentsEditor, GroupPricingEditor: pricingEditor } } })
  await flushPromises()
  return { w, selected: () => w.get('[role="tab"][aria-selected="true"]').text() }
}

beforeEach(() => {
  i18n.global.locale.value = 'zh'
})

describe('prices and user groups of a group, read-only', () => {
  it('shows both tabs without editing, the prices tab with the rows of the other', async () => {
    const { w, selected: tab } = await render()
    expect(w.text()).toContain('pro 的价格与用户组')
    expect(w.findAll('[role="tab"]').map((t) => t.text())).toEqual(['价格', '用户组'])
    expect(tab()).toBe('价格')
    expect(w.get('[data-testid="segments"]').attributes()).toMatchObject({ 'data-editable': 'false', 'data-rate': '1.2' })
    expect(w.get('[data-testid="pricing"]').attributes()).toMatchObject({ 'data-editable': 'false', 'data-rows': '1', 'data-rate': '1.2' })
  })

  it('closes', async () => {
    const { w } = await render()
    await w.get('[data-testid="group-pricing-view-close"]').trigger('click')
    expect(w.emitted('update:open')).toEqual([[false]])
  })
})
