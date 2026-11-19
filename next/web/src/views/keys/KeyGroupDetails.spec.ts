import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { i18n } from '@/i18n'
import type { Group } from '@/api/types'
import GroupBadge from '@/components/platform/GroupBadge.vue'
import PlatformEndpointsPreview from '@/views/platforms/PlatformEndpointsPreview.vue'
import KeyGroupDetails from './KeyGroupDetails.vue'

const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/me/models', component: { template: '<div />' } }] })
const render = (group: Partial<Group>) => {
  i18n.global.locale.value = 'zh'
  return mount(KeyGroupDetails, {
    props: { group: { id: 1, name: 'default', rate_multiplier: '1', ...group } as Group },
    global: { plugins: [i18n, router], stubs: { PlatformEndpointsPreview: true, GroupBadge: true } }
  })
}

describe('API key group details', () => {
  it('lists a few endpoints of each protocol the group serves, not every endpoint of its platforms', () => {
    const served = [{ protocol: 'anthropic.messages' }]
    const w = render({ platforms: ['anthropic', 'openai'], served_protocols: served })
    expect(w.text()).toContain('此分组的 Key 可以调用')
    expect(w.getComponent(PlatformEndpointsPreview).props()).toEqual({ served, max: 3 })
  })

  it('warns instead when the group serves no protocol', () => {
    const w = render({ platforms: ['anthropic'], served_protocols: [] })
    expect(w.text()).toContain('该分组暂时无法提供服务')
    expect(w.findComponent(PlatformEndpointsPreview).exists()).toBe(false)
  })

  it('shows the rate of the owner, next to the default of the group when it is another', () => {
    const own = render({ id: 2, name: 'vip', rate_multiplier: '0.8', effective_rate: '0.72', exclusive: true })
    expect(own.getComponent(GroupBadge).props('rate')).toBe('0.72')
    expect(own.get('[data-testid="key-group-own-rate"]').text()).toBe('分组默认 ×0.8，你的倍率 ×0.72')
    expect(own.get('[data-testid="key-group-exclusive"]').text()).toBe('专属')

    const plain = render({ effective_rate: '1' })
    expect(plain.getComponent(GroupBadge).props('rate')).toBe('1')
    expect(plain.find('[data-testid="key-group-own-rate"]').exists()).toBe(false)
    expect(plain.find('[data-testid="key-group-exclusive"]').exists()).toBe(false)
  })

  it('links to the models and prices of the group, in a new tab', () => {
    const link = render({ id: 2 }).get('[data-testid="key-group-prices"]')
    expect(link.attributes('href')).toBe('/me/models?group=2')
    expect(link.attributes('target')).toBe('_blank')
  })
})
