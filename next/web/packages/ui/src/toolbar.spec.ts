import { afterEach, describe, expect, it } from 'vitest'
import { enableAutoUnmount } from '@vue/test-utils'
import { defineComponent, h, shallowRef, type VNode } from 'vue'
import SButton from './SButton.vue'
import SModal from './SModal.vue'
import SPageHeader from './SPageHeader.vue'
import STabs from './STabs.vue'
import { provideTitleContext, type PageHead } from './titleContext'
import { mountPinned } from './testViewport'

enableAutoUnmount(afterEach)
afterEach(() => {
  document.body.innerHTML = ''
})

// A page header directly in an app shell (lifted) or on its own (nested).
function page(actions: () => VNode[], inShell = true) {
  return defineComponent({
    setup() {
      if (inShell) provideTitleContext(() => '页面', { head: shallowRef<PageHead | null>(null) })
      return () => h(SPageHeader, { title: '页面' }, { actions })
    }
  })
}
const create = (props: Record<string, unknown> = {}) => h(SButton, { variant: 'primary', icon: 'plus', ...props }, () => '新建')
const fabs = () => document.querySelectorAll('[data-fab]').length

describe('buttons in a toolbar', () => {
  it('take the toolbar size, whatever size the page asks for', () => {
    const w = mountPinned(page(() => [h(SButton, { size: 'sm' }, () => '刷新')]), {}, { compact: false })
    expect(w.get('button').classes()).toContain('btn-md')
  })

  it('float a primary button with an icon on a phone in a page toolbar', () => {
    mountPinned(page(() => [create(), h(SButton, { icon: 'refresh' }, () => '刷新')]))
    expect(fabs()).toBe(1)
  })

  it('keep a primary button in place when the page says :fab="false"', () => {
    mountPinned(page(() => [create({ fab: false })]))
    expect(fabs()).toBe(0)
  })

  it('do not float in a nested header or at the end of a tab bar', () => {
    mountPinned(page(() => [create()], false))
    const tabs = [{ key: 'a', label: '甲' }, { key: 'b', label: '乙' }]
    mountPinned(STabs, { props: { tabs, modelValue: 'a' }, slots: { end: () => create({ size: 'xs' }) } })
    expect(fabs()).toBe(0)
    expect(document.querySelector('[data-testid="tab-bar-end"] button')?.classList).toContain('btn-md')
  })

  it('do not float as a block button', () => {
    mountPinned(page(() => [create({ block: true })]))
    expect(fabs()).toBe(0)
  })

  it('take the toolbar size at the end of tabs whose bar is hidden', () => {
    mountPinned(STabs, { props: { tabs: [{ key: 'a', label: '甲' }], modelValue: 'a' }, slots: { end: () => create({ size: 'xs' }) } })
    expect(document.querySelector('button')?.classList).toContain('btn-md')
  })

  it('in a dialog opened from a page toolbar keep their own size and do not float', () => {
    mountPinned(page(() => [h(SModal, { open: true, title: '弹窗' }, () => create({ size: 'xs' }))]))
    const inDialog = document.querySelector('[role="dialog"] .btn-primary')
    expect(inDialog?.classList).toContain('btn-xs')
    expect(fabs()).toBe(0)
  })

  it('outside a toolbar keep their own size and float only with fab', () => {
    const w = mountPinned(SButton, { props: { size: 'sm', variant: 'primary', icon: 'plus' } })
    expect(w.classes()).toContain('btn-sm')
    expect(fabs()).toBe(0)
  })
})

describe('filters in a page header', () => {
  it('never float, even a primary button with an icon', () => {
    const header = defineComponent({
      setup() {
        provideTitleContext(() => '页面', { head: shallowRef<PageHead | null>(null) })
        return () => h(SPageHeader, { title: '页面' }, { 'filters-primary': () => create() })
      }
    })
    mountPinned(header)
    expect(fabs()).toBe(0)
  })
})
