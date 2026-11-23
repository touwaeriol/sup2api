import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import { h, nextTick } from 'vue'
import STabs from './STabs.vue'
import SSettingsGroup from './SSettingsGroup.vue'
import SPageHeader from './SPageHeader.vue'
import SSectionTitle from './SSectionTitle.vue'
import SModal from './SModal.vue'
import SDrawer from './SDrawer.vue'

enableAutoUnmount(afterEach)
const tabs = [{ key: 'billing', label: '计费' }, { key: 'other', label: '其他' }]

describe('platform tab title ownership', () => {
  it('hides matching titles but keeps controls, description, distinct sections and panel labels', () => {
    const wrapper = mount(STabs, { props: { tabs, modelValue: 'billing', idPrefix: 'settings' }, slots: { default: () => [
      h(SPageHeader, { title: '计费' }),
      h(SSectionTitle, { title: '计费' }),
      h(SSettingsGroup, { title: '计费', description: '计费说明' }, { default: () => '内容', actions: () => h('button', '保存') }),
      h(SSettingsGroup, { title: '价格' }, () => '价格设置')
    ] } })
    expect(wrapper.find('h1').exists()).toBe(false)
    expect(wrapper.find('h3').exists()).toBe(false)
    expect(wrapper.findAll('h5').map(el => el.text())).toEqual(['价格'])
    expect(wrapper.text()).toContain('计费说明')
    expect(wrapper.findAll('button').some(el => el.text() === '保存')).toBe(true)
    expect(wrapper.get('[role="tabpanel"]').attributes('aria-labelledby')).toBe('settings-tab-billing')
    expect(wrapper.get('#settings-tab-billing').attributes('aria-controls')).toBe('settings-panel-billing')
  })

  it('removes empty title bars and follows locale and tab changes reactively', async () => {
    const wrapper = mount(STabs, { props: { tabs, modelValue: 'billing' }, slots: { default: () => h(SSettingsGroup, { title: '计费' }, () => '内容') } })
    expect(wrapper.find('header').exists()).toBe(false)
    await wrapper.setProps({ modelValue: 'other' })
    expect(wrapper.get('h5').text()).toBe('计费')
    await wrapper.setProps({ modelValue: 'billing', tabs: [{ key: 'billing', label: 'Billing' }, tabs[1]!] })
    expect(wrapper.get('h5').text()).toBe('计费')
    await wrapper.setProps({ tabs })
    expect(wrapper.find('header').exists()).toBe(false)
  })

  it('retains titles when a single tab is not displayed and when standalone', () => {
    const wrapper = mount(STabs, { props: { tabs: [tabs[0]!], modelValue: 'billing' }, slots: { default: () => h(SSettingsGroup, { title: '计费' }) } })
    expect(wrapper.find('[role="tablist"]').exists()).toBe(false)
    expect(wrapper.get('h5').text()).toBe('计费')
    expect(mount(SPageHeader, { props: { title: '计费' } }).get('h1').text()).toBe('计费')
  })

  it('keeps collapse actions and custom title slots intact', async () => {
    const wrapper = mount(STabs, { props: { tabs, modelValue: 'billing' }, slots: { default: () => [
      h(SSettingsGroup, { title: '计费', collapsible: true }, () => '可折叠内容'),
      h(SSettingsGroup, { title: '计费' }, { title: () => h('strong', '自定义标题和状态') })
    ] } })
    expect(wrapper.get('h5').text()).toBe('计费')
    const toggle = wrapper.get('button[aria-expanded]')
    await toggle.trigger('click')
    expect(toggle.attributes('aria-expanded')).toBe('false')
    expect(wrapper.text()).toContain('自定义标题和状态')
  })

  it('isolates nested tabs and dialogs from outer titles', () => {
    const wrapper = mount(STabs, { props: { tabs, modelValue: 'billing' }, global: { stubs: { teleport: true } }, slots: { default: () => [
      h(STabs, { tabs, modelValue: 'other' }, () => h(SSettingsGroup, { title: '计费' })),
      h(SModal, { open: true, title: '计费' }, () => h(SSectionTitle, { title: '计费' })),
      h(SDrawer, { open: true, title: '计费' }, () => h(SPageHeader, { title: '计费' }))
    ] } })
    expect(wrapper.get('h5').text()).toBe('计费')
    expect(wrapper.findComponent(SModal).findAll('h3')).toHaveLength(2)
    expect(wrapper.findComponent(SDrawer).get('h1').text()).toBe('计费')
  })
})

describe('tab bar that scrolls', () => {
  const many = [{ key: 'a', label: 'A' }, { key: 'b', label: 'B' }, { key: 'c', label: 'C' }]
  afterEach(() => vi.restoreAllMocks())

  it('fades the edges that hide tabs', async () => {
    const wrapper = mount(STabs, { props: { tabs: many, modelValue: 'a' } })
    const scroller = wrapper.get('[role="tablist"]').element.parentElement!
    Object.defineProperties(scroller, { scrollWidth: { value: 500 }, clientWidth: { value: 200 } })
    const scrollTo = async (left: number) => {
      scroller.scrollLeft = left
      scroller.dispatchEvent(new Event('scroll'))
      await nextTick()
      return scroller.className
    }
    expect(await scrollTo(0)).toContain('to_left')
    expect(await scrollTo(100)).toContain('calc(100%-2rem)')
    expect(await scrollTo(300)).toContain('to_right,transparent,#000_2rem)')
  })

  it('centres the selected tab when it changes', async () => {
    const scrollTo = vi.spyOn(HTMLElement.prototype, 'scrollTo').mockImplementation(() => {})
    const wrapper = mount(STabs, { props: { tabs: many, modelValue: 'a' } })
    Object.defineProperty(wrapper.get('[role="tablist"]').element.parentElement!, 'clientWidth', { value: 200 })
    Object.defineProperties(wrapper.get('[data-key="c"]').element, { offsetLeft: { value: 400 }, offsetWidth: { value: 80 } })
    await wrapper.setProps({ modelValue: 'c' })
    expect(scrollTo).toHaveBeenLastCalledWith({ left: 340, behavior: 'smooth' })
  })
})

describe('tab bar end', () => {
  const two = [{ key: 'a', label: '甲' }, { key: 'b', label: '乙' }]
  it('puts #end at the right end of the bar', () => {
    const w = mount(STabs, { props: { tabs: two, modelValue: 'a' }, slots: { end: () => h('button', '刷新') } })
    expect(w.get('[role="tablist"]').text()).not.toContain('刷新')
    expect(w.get('[data-testid="tab-bar-end"]').text()).toBe('刷新')
  })
  it('keeps #end on its own row when the bar is not shown', () => {
    const w = mount(STabs, { props: { tabs: two.slice(0, 1), modelValue: 'a' }, slots: { end: () => h('button', '刷新') } })
    expect(w.find('[role="tablist"]').exists()).toBe(false)
    expect(w.text()).toContain('刷新')
  })
})
