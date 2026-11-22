import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { h, type Component } from 'vue'
import { i18n } from '@/i18n'
import type { Group } from '@/api/types'
import GroupPricingModal from './GroupPricingModal.vue'

enableAutoUnmount(afterEach)

const table: Component = {
  props: ['groupId', 'baseRate', 'editable'],
  setup: (props: Record<string, any>) => () => h('div', { 'data-testid': 'pricing', 'data-group': props.groupId, 'data-editable': String(!!props.editable), 'data-rate': props.baseRate })
}

const group = { id: 3, name: 'pro', visibility: 'restricted', rate_multiplier: 1.2 } as unknown as Group

async function render() {
  const w = mount(GroupPricingModal, { props: { open: true, group }, global: { plugins: [i18n], stubs: { Teleport: true, ModelPriceTable: table } } })
  await flushPromises()
  return w
}

beforeEach(() => {
  i18n.global.locale.value = 'zh'
})

describe('model prices of a group, read-only', () => {
  it('shows the price table without editing, at the base rate, and no tabs', async () => {
    const w = await render()
    expect(w.text()).toContain('pro 的价格')
    expect(w.find('[role="tab"]').exists()).toBe(false)
    expect(w.get('[data-testid="pricing"]').attributes()).toMatchObject({ 'data-group': '3', 'data-editable': 'false', 'data-rate': '1.2' })
  })

  it('closes', async () => {
    const w = await render()
    await w.get('[data-testid="group-pricing-view-close"]').trigger('click')
    expect(w.emitted('update:open')).toEqual([[false]])
  })
})
