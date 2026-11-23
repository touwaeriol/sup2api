import { afterEach, describe, expect, it } from 'vitest'
import { enableAutoUnmount } from '@vue/test-utils'
import { defineComponent, h, nextTick, ref, shallowRef, type VNode } from 'vue'
import SPageHeader from './SPageHeader.vue'
import STitleScope from './STitleScope.vue'
import { provideTitleContext, type PageHead } from './titleContext'
import { mountPinned } from './testViewport'

enableAutoUnmount(afterEach)

afterEach(() => {
  document.body.innerHTML = ''
})

// An app shell: a top bar that takes the page's title and what goes with it, and the page under it.
const head = shallowRef<PageHead | null>(null)
function shell(slots: Record<string, () => unknown>, title = '分组', page: () => VNode = () => h(SPageHeader, { title, description: '说明' }, slots)) {
  head.value = null
  return defineComponent({
    setup() {
      provideTitleContext(() => head.value?.title || '路由标题', { head, extra: '#page-title-extra' })
      return () => h('div', [h('div', { id: 'page-title-extra' }), page()])
    }
  })
}
const bar = (id: string) => document.querySelector(id)?.textContent

describe('page header in an app shell', () => {
  it('gives its title and description to the top bar, whatever the title says, and draws neither', async () => {
    const w = mountPinned(shell({}, '个人中心'), {}, { compact: false })
    await nextTick()
    expect(head.value).toMatchObject({ title: '个人中心', description: '说明' })
    expect(w.find('h1').exists()).toBe(false)
    expect(w.text()).toBe('')
  })

  it('follows a title that changes and takes its own back when it goes', async () => {
    const title = ref('加载中')
    const w = mountPinned(shell({}, '', () => h(SPageHeader, { title: title.value })), {}, { compact: false })
    await nextTick()
    title.value = '插件甲'
    await nextTick()
    expect(head.value).toMatchObject({ title: '插件甲', description: '' })
    w.unmount()
    expect(head.value).toBeNull()
  })

  it('leaves the top bar to the header that came after it', async () => {
    const first = mountPinned(shell({}, '甲'), {}, { compact: false })
    await nextTick()
    const mine = head.value
    head.value = { owner: -1, title: '乙', description: '' }
    first.unmount()
    expect(mine?.title).toBe('甲')
    expect(head.value?.title).toBe('乙')
  })

  it('puts what goes with the title into the top bar on a wide screen', async () => {
    const w = mountPinned(shell({ before: () => h('i', '图'), 'title-extra': () => h('b', 'v1.2') }), {}, { compact: false })
    await nextTick()
    expect(bar('#page-title-extra')).toBe('图v1.2')
    expect(w.find('.mb-4').exists()).toBe(false)
  })

  it('keeps what goes with the title in the page on a narrow screen, without the title', async () => {
    const w = mountPinned(shell({ 'title-extra': () => h('b', 'v1.2'), actions: () => h('button', '启用') }))
    await nextTick()
    expect(bar('#page-title-extra')).toBe('')
    expect(w.get('.mb-4').text()).toBe('v1.2启用')
    expect(w.find('h1').exists()).toBe(false)
  })

  it('stays in place in a nested scope, leaving out only a title that repeats the scope\'s', async () => {
    const nested = (title: string) => () => h(STitleScope, { title: '常规' }, () => h(SPageHeader, { title, description: '说明' }, { actions: () => h('button', '保存') }))
    const same = mountPinned(shell({}, '', nested('常规')), {}, { compact: false })
    await nextTick()
    expect(head.value).toBeNull()
    expect(same.find('h1').exists()).toBe(false)
    expect(same.text()).toBe('说明保存')
    same.unmount()
    const other = mountPinned(shell({}, '', nested('高级')), {}, { compact: false })
    await nextTick()
    expect(other.get('h1').text()).toBe('高级')
  })
})

describe('page header outside a shell', () => {
  it('draws its title, description and actions itself', async () => {
    const w = mountPinned(SPageHeader, { props: { title: '详情', description: '说明' }, slots: { actions: () => h('button', '新建') } }, { compact: false })
    await nextTick()
    expect(w.get('h1').text()).toBe('详情')
    expect(w.text()).toBe('详情说明新建')
  })
})

describe('page header actions', () => {
  it('keeps the actions of a header with no filters in the page, at the end of their own row', async () => {
    const w = mountPinned(shell({ actions: () => h('button', '新建分组') }), {}, { compact: false })
    await nextTick()
    expect(w.get('.mb-3').text()).toBe('新建分组')
    expect(w.get('.mb-3 > .ms-auto').text()).toBe('新建分组')
  })

  it('keeps the actions in the page on a narrow screen', async () => {
    const w = mountPinned(shell({ actions: () => h('button', '新建分组') }))
    await nextTick()
    expect(w.get('.mb-3').text()).toBe('新建分组')
  })

  it('keeps the actions beside the filters when there are filters', async () => {
    const w = mountPinned(shell({ filters: () => h('input'), actions: () => h('button', '新建分组') }), {}, { compact: false })
    await nextTick()
    expect(w.get('.mb-3').text()).toBe('新建分组')
  })

  it('keeps the actions in the page beside what goes with the title in the top bar', async () => {
    const w = mountPinned(shell({ 'title-extra': () => h('b', 'v1.2'), actions: () => h('button', '启用') }), {}, { compact: false })
    await nextTick()
    expect(bar('#page-title-extra')).toBe('v1.2')
    expect(w.get('.mb-3').text()).toBe('启用')
  })
})

describe('page header filters on a phone', () => {
  it('folds #filters behind a Filters button, keeping #filters-primary in the row', async () => {
    const slots = { 'filters-primary': () => h('input', { id: 'q' }), filters: () => h('select', { id: 'status' }) }
    const w = mountPinned(SPageHeader, { props: { title: '分组', filtersActiveCount: 1 }, slots })
    await nextTick()
    expect(w.find('#q').exists()).toBe(true)
    expect(document.querySelector('#status')).toBeNull()
    const open = w.get('[data-testid="filter-sheet-open"]')
    expect(open.attributes('aria-label')).toBe('Filters, 1 active')
    await open.trigger('click')
    expect(document.querySelector('[data-testid="filter-sheet"] #status')).not.toBeNull()
  })

  it('keeps #filters-primary inline when there is nothing else to fold', async () => {
    const w = mountPinned(SPageHeader, { props: { title: '分组' }, slots: { 'filters-primary': () => h('input', { id: 'q' }) } })
    await nextTick()
    expect(w.find('#q').exists()).toBe(true)
    expect(w.find('[data-testid="filter-sheet-open"]').exists()).toBe(false)
  })

  it('puts #filters-primary before #filters on a wide screen', async () => {
    const slots = { filters: () => h('select', { id: 'status' }), 'filters-primary': () => h('input', { id: 'q' }) }
    const w = mountPinned(SPageHeader, { props: { title: '分组' }, slots }, { compact: false })
    await nextTick()
    expect(w.findAll('input, select').map(e => e.attributes('id'))).toEqual(['q', 'status'])
  })
})
